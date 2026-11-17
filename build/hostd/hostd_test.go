package main

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"syscall"
	"testing"
	"time"
)

func TestDiffWorkedExample(t *testing.T) {
	a := Snapshot{HaveCPU: true, HostTotal: 1000, HostIdle: 800, Containers: map[string]uint64{"c": 1_000_000, "gone": 5}}
	b := Snapshot{HaveCPU: true, HostTotal: 2000, HostIdle: 1500, Containers: map[string]uint64{"c": 3_500_000, "new": 9}}
	r := Diff(a, b, 100)
	if r.HostCPUPct != 30 {
		t.Errorf("host CPU = %v, want (1000-700)/1000 = 30", r.HostCPUPct)
	}
	if got := r.ContainerCPU["c"]; got != 25 {
		t.Errorf("container CPU = %v, want 2.5 s * 100 Hz = 250 of 1000 host ticks = 25", got)
	}
	if _, ok := r.ContainerCPU["gone"]; ok {
		t.Error("a container missing from the second sample got a rate")
	}
	if _, ok := r.ContainerCPU["new"]; ok {
		t.Error("a container missing from the first sample got a rate")
	}
	if got := Diff(a, b, 250).ContainerCPU["c"]; got != 62.5 {
		t.Errorf("clock ticks are not part of the formula: %v", got)
	}
}

func TestDiffEdges(t *testing.T) {
	a := Snapshot{HaveCPU: true, HostTotal: 1000, HostIdle: 800, Containers: map[string]uint64{"c": 100}}
	for name, b := range map[string]Snapshot{
		"no ticks":        {HaveCPU: true, HostTotal: 1000, HostIdle: 800, Containers: map[string]uint64{"c": 200}},
		"clock backwards": {HaveCPU: true, HostTotal: 900, HostIdle: 700, Containers: map[string]uint64{"c": 200}},
		"no stat file":    {Containers: map[string]uint64{"c": 200}},
	} {
		r := Diff(a, b, 100)
		if r.HostCPUPct != 0 || len(r.ContainerCPU) != 0 {
			t.Errorf("%s: %+v", name, r)
		}
	}
	b := Snapshot{HaveCPU: true, HostTotal: 2000, HostIdle: 1000, Containers: map[string]uint64{"c": 50}}
	if r := Diff(a, b, 100); len(r.ContainerCPU) != 0 || r.HostCPUPct != 80 {
		t.Errorf("a cgroup counter that went backwards produced a rate: %+v", r)
	}
	if r := Diff(a, a, 100); r.HostCPUPct != 0 {
		t.Errorf("identical samples: %+v", r)
	}
}

func TestNetRates(t *testing.T) {
	base := Snapshot{HaveNet: true, HaveUptime: true, Uptime: 100, NetRx: 1000, NetTx: 500}
	r := Diff(base, Snapshot{HaveNet: true, HaveUptime: true, Uptime: 102.5, NetRx: 6000, NetTx: 1000}, 100)
	if !r.HaveNet || r.NetRx != 2000 || r.NetTx != 200 {
		t.Errorf("rates = %+v", r)
	}
	for name, b := range map[string]Snapshot{
		"counter reset": {HaveNet: true, HaveUptime: true, Uptime: 101, NetRx: 10, NetTx: 600},
		"same uptime":   {HaveNet: true, HaveUptime: true, Uptime: 100, NetRx: 2000, NetTx: 600},
		"no uptime":     {HaveNet: true, NetRx: 2000, NetTx: 600},
		"no interfaces": {HaveUptime: true, Uptime: 101},
	} {
		if Diff(base, b, 100).HaveNet {
			t.Errorf("%s still produced a rate", name)
		}
	}
}

func TestProcReadersSurviveMissingAndGarbage(t *testing.T) {
	e := newEnv(t)
	if _, _, ok := readCPU(e.cfg.ProcRoot); ok {
		t.Error("missing stat file read as ok")
	}
	if _, ok := readMemUsed(e.cfg.ProcRoot); ok {
		t.Error("missing meminfo read as ok")
	}
	if _, ok := readUptime(e.cfg.ProcRoot); ok {
		t.Error("missing uptime read as ok")
	}
	e.put("proc/stat", "intr 5\n")
	e.put("proc/meminfo", "MemTotal: lots kB\nMemAvailable: 5 kB\nnonsense\n")
	e.put("proc/uptime", "soon\n")
	if _, _, ok := readCPU(e.cfg.ProcRoot); ok {
		t.Error("stat without a cpu line read as ok")
	}
	if used, ok := readMemUsed(e.cfg.ProcRoot); !ok || used != 0 {
		t.Errorf("available above total = %d, %v; want 0", used, ok)
	}
	if _, ok := readUptime(e.cfg.ProcRoot); ok {
		t.Error("garbage uptime read as ok")
	}
	e.put("proc/stat", "cpu  1 x 3 4 5 6\n")
	if total, idle, ok := readCPU(e.cfg.ProcRoot); !ok || total != 19 || idle != 9 {
		t.Errorf("cpu = %d %d %v, want 19 9 true (garbage field counts as 0)", total, idle, ok)
	}
	e.put("proc/meminfo", "MemTotal: 8000000 kB\nMemAvailable: 4000000 kB\n")
	if used, _ := readMemUsed(e.cfg.ProcRoot); used != 4_096_000_000 {
		t.Errorf("host memory = %d", used)
	}
}

func TestNetOnlyCountsPhysicalInterfaces(t *testing.T) {
	e := newEnv(t)
	hdr := "h1\nh2\n"
	e.put("proc/net/dev", hdr+"    lo: 100 1 0 0 0 0 0 0 100 1 0 0 0 0 0 0\neth0:1000 1 0 0 0 0 0 0 2000 1 0 0 0 0 0 0\nveth1: 7 1 0 0 0 0 0 0 7 1 0 0 0 0 0 0\nshort: 1 2\n")
	if _, _, ok := readNet(e.cfg.ProcRoot, physIfaces(e.cfg.SysRoot)); ok {
		t.Error("no physical interface still produced a sample")
	}
	e.put("sys/class/net/eth0/device", "")
	e.put("sys/class/net/veth1/uevent", "")
	e.put("sys/class/net/short/device", "")
	rx, tx, ok := readNet(e.cfg.ProcRoot, physIfaces(e.cfg.SysRoot))
	if !ok || rx != 1000 || tx != 2000 {
		t.Errorf("net = %d %d %v", rx, tx, ok)
	}
}

func TestThermal(t *testing.T) {
	e := newEnv(t)
	if cpu, board := readThermal(e.cfg.ThermalGlob); cpu != nil || board != nil {
		t.Error("no zones must leave both temperatures absent")
	}
	zone := func(n, temp, kind string) { e.put("zones/"+n+"/temp", temp); e.put("zones/"+n+"/type", kind) }
	zone("0", "59000\n", "x86_pkg_temp\n")
	zone("1", "61250\n", "cpu-thermal\n")
	zone("2", "45000\n", "acpitz\n")
	zone("3", "99000\n", "iwlwifi_1\n")
	zone("4", "oops\n", "TCPU\n")
	zone("5", "-5000\n", "TCPU\n")
	zone("6", "50000\n", "soc_thermal\n")
	e.put("zones/7/temp", "88000\n")
	cpu, board := readThermal(e.cfg.ThermalGlob)
	if cpu == nil || *cpu != 61.2 {
		t.Errorf("cpu temp = %v, want hottest cpu-class zone rounded to one decimal (61.2)", cpu)
	}
	if board == nil || *board != 45 {
		t.Errorf("board temp = %v", board)
	}
	e2 := newEnv(t)
	e2.put("zones/0/temp", "45000\n")
	e2.put("zones/0/type", "acpitz\n")
	if cpu, board := readThermal(e2.cfg.ThermalGlob); cpu != nil || board == nil {
		t.Errorf("board only: %v %v", cpu, board)
	}
}

func TestFans(t *testing.T) {
	e := newEnv(t)
	if rpm, max := readFans(e.cfg.SysRoot); rpm != nil || max != nil {
		t.Error("no hwmon must leave fans absent")
	}
	e.put("sys/class/hwmon/hwmon3/fan2_input", "0\n")
	e.put("sys/class/hwmon/hwmon2/device/fan1_input", "2506\n")
	e.put("sys/class/hwmon/hwmon2/device/fan1_max", "4825\n")
	e.put("sys/class/hwmon/hwmon4/fan1_input", "n/a\n")
	e.put("sys/class/hwmon/hwmon5/fan1_input", "1800\n")
	e.put("sys/class/hwmon/hwmon5/fan1_max", "garbage\n")
	rpm, max := readFans(e.cfg.SysRoot)
	if !reflect.DeepEqual(rpm, []int{0, 1800, 2506}) || !reflect.DeepEqual(max, []int{0, 0, 4825}) {
		t.Errorf("fans = %v max %v: direct hwmon files come before device/ ones, bad max reads as 0", rpm, max)
	}
}

func TestFansMaxOmittedWhenNoFanReportsOne(t *testing.T) {
	e := newEnv(t)
	e.put("sys/class/hwmon/hwmon1/fan1_input", "900\n")
	e.put("sys/class/hwmon/hwmon1/fan1_max", "0\n")
	e.put("sys/class/hwmon/hwmon2/device/fan1_input", "800\n")
	st := sampleOnce(t, e, []Container{ctr("dash000000000000", "dashboard", "running")})
	if !reflect.DeepEqual(st.FansRPM, []int{900, 800}) || st.FansMaxRPM != nil {
		t.Errorf("fans %v max %v", st.FansRPM, st.FansMaxRPM)
	}
	e.put("sys/class/hwmon/hwmon2/device/fan1_max", "4000\n")
	st = sampleOnce(t, e, []Container{ctr("dash000000000000", "dashboard", "running")})
	if !reflect.DeepEqual(st.FansMaxRPM, []int{0, 4000}) {
		t.Errorf("max %v", st.FansMaxRPM)
	}
}

func sampleOnce(t *testing.T, e *env, containers []Container) *Stats {
	t.Helper()
	st, _, err := e.sampler(t, containers).Sample(nil)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func TestCgroupLayoutsAndGarbage(t *testing.T) {
	e := newEnv(t)
	e.cgroup("systemd", "aaaa", 10, 100)
	e.cgroup("cgroupfs", "bbbb", 20, 200)
	if v, ok := readUsageUsec(e.cfg.CgroupRoot, "aaaa"); !ok || v != 10 {
		t.Errorf("systemd layout: %d %v", v, ok)
	}
	if v, ok := readUsageUsec(e.cfg.CgroupRoot, "bbbb"); !ok || v != 20 {
		t.Errorf("cgroupfs layout: %d %v", v, ok)
	}
	if m, ok := readCgroupMem(e.cfg.CgroupRoot, "bbbb"); !ok || m != 210 {
		t.Errorf("cgroupfs memory: %d %v (anon + sock)", m, ok)
	}
	if _, ok := readUsageUsec(e.cfg.CgroupRoot, "cccc"); ok {
		t.Error("unknown container read as ok")
	}
	e.put("cg/system.slice/docker-dddd.scope/cpu.stat", "user_usec 5\n")
	e.put("cg/system.slice/docker-dddd.scope/memory.stat", "file 5\nsock 9\n")
	if _, ok := readUsageUsec(e.cfg.CgroupRoot, "dddd"); ok {
		t.Error("cpu.stat without usage_usec read as ok")
	}
	if _, ok := readCgroupMem(e.cfg.CgroupRoot, "dddd"); ok {
		t.Error("memory.stat without anon must not count (cgroup v1 style)")
	}
	e.put("cg/system.slice/docker-eeee.scope/cpu.stat", "usage_usec many\n")
	if v, ok := readUsageUsec(e.cfg.CgroupRoot, "eeee"); !ok || v != 0 {
		t.Errorf("garbage usage: %d %v", v, ok)
	}
}

func TestNodeMapping(t *testing.T) {
	e := newEnv(t)
	boxes := []Container{
		ctr("dash000000000000", "dashboard", "running"),
		ctr("xmr0000000000000", "monero", "running"),
		ctr("xmrw000000000000", "monero-wallet", "running"),
		ctr("tor0000000000000", "tor", "running"),
		ctr("odd0000000000000", "mystery-wallet", "running"),
		ctr("nocg000000000000", "bitcoin", "running"),
		ctr("ltc0000000000000", "litecoin", "exited"),
		ctr("zec0000000000000", "zcash", "dead"),
		ctr("tari000000000000", "tari", "restarting"),
	}
	for _, id := range []string{"dash000000000000", "xmr0000000000000", "xmrw000000000000", "tor0000000000000", "odd0000000000000"} {
		e.cgroup("systemd", id, 0, 1000)
	}
	st := sampleOnce(t, e, boxes)
	if st.Nodes["monero"].MemBytes != 2*1010 {
		t.Errorf("monero and its wallet were not added together: %+v", st.Nodes["monero"])
	}
	if _, ok := st.Nodes["monero-wallet"]; ok {
		t.Error("wallet got its own node entry")
	}
	if _, ok := st.Nodes["bitcoin"]; ok {
		t.Error("a node without cgroup numbers got an entry")
	}
	if st.Other.MemBytes != 3*1010 {
		t.Errorf("dashboard, tor and an unknown -wallet service belong to other: %+v", st.Other)
	}
	want := map[string]bool{"monero": true, "bitcoin": true, "litecoin": false, "zcash": false, "tari": true}
	if !reflect.DeepEqual(st.Running, want) {
		t.Errorf("running = %v, want %v", st.Running, want)
	}
	if st.Failed == nil || st.Disks == nil {
		t.Error("failed and disks must marshal as {} and []")
	}
}

func TestFailedRecords(t *testing.T) {
	e := newEnv(t)
	full := "d09e00000000aaaaaaaaaaaaaaaa"
	rec := func(service, id string) {
		e.put("state/resource-profile/failed-"+service, id+"\t1\t1\tThe reason.\tmessage\n")
	}
	rec("dogecoin", "d09e00000000")
	rec("litecoin", "11ec00000000")
	rec("bitcoincash", "00old0000000")
	rec("zcash", "tooshort")
	rec("monero", "a0e100000000")
	e.put("state/resource-profile/failed-tari", "\t\t\n")
	st := sampleOnce(t, e, []Container{
		ctr("dash000000000000", "dashboard", "running"),
		ctr(full, "dogecoin", "exited"),
		ctr("11ec0000000000000000", "litecoin", "running"),
		ctr("bc4000000000aaaa", "bitcoincash", "exited"),
		ctr("2ca500000000aaaa", "zcash", "exited"),
		ctr("a0e100000000aaaa", "monero", "restarting"),
		ctr("7a4100000000aaaa", "tari", "exited"),
	})
	if want := map[string]string{"dogecoin": "The reason."}; !reflect.DeepEqual(st.Failed, want) {
		t.Errorf("failed = %v, want %v: only a record for the current, exited container counts", st.Failed, want)
	}
}

func TestDiskSizes(t *testing.T) {
	e := newEnv(t)
	for _, d := range []string{"state/data/bitcoin", "state/data/tari", "xmr", "stack", "state/data/litecoin"} {
		os.MkdirAll(filepath.Join(e.root, d), 0o755)
	}
	e.put("state/.env", "MONERO_DATA_DIR="+filepath.Join(e.root, "xmr")+"\nMONERO_DATA_DIR=\nBITCOIN_DATA_DIR=\n")
	kb := map[string]uint64{
		filepath.Join(e.root, "state/data/bitcoin"):  4096,
		filepath.Join(e.root, "state/data/tari"):     1023,
		filepath.Join(e.root, "state/data/litecoin"): 2048,
		filepath.Join(e.root, "stack"):               100,
		filepath.Join(e.root, "state"):               7000,
	}
	s := e.sampler(t, []Container{ctr("dash000000000000", "dashboard", "running")})
	s.Sizes.Run = func(dir string) (uint64, bool) { v, ok := kb[dir]; return v, ok }
	st, _, err := s.Sample(nil)
	if err != nil {
		t.Fatal(err)
	}
	if st.Nodes["bitcoin"].DiskBytes != 4096*1024 || st.Nodes["litecoin"].DiskBytes != 2048*1024 {
		t.Errorf("nodes = %+v", st.Nodes)
	}
	if _, ok := st.Nodes["tari"]; ok {
		t.Error("a node dir under 1 MiB must be left out")
	}
	if _, ok := st.Nodes["monero"]; ok {
		t.Error("an empty last MONERO_DATA_DIR line wins and falls back to a missing default")
	}
	if got := st.Nodes["stack"].DiskBytes; got != (100+7000-4096-2048)*1024 {
		t.Errorf("stack = %d, want both roots minus the node dirs inside them", got)
	}
}

func TestMounts(t *testing.T) {
	e := newEnv(t)
	e.put("proc/self/mounts", "/dev/sda1 / ext4 rw 0 0\ntmpfs /run tmpfs rw 0 0\n/dev/sdb1 /mnt/my\\040disk ext4 rw 0 0\n/dev/loop3 /snap/x squashfs ro 0 0\n/dev/sdc1 /gone ext4 rw 0 0\nbroken\n")
	stat := func(p string) (uint64, uint64, bool) {
		switch p {
		case "/":
			return 30, 70, true
		case "/mnt/my disk":
			return 1, 2, true
		case "/snap/x":
			return 0, 0, true
		}
		return 0, 0, false
	}
	got := readMounts(e.cfg.ProcRoot, stat)
	want := []mount{{"/dev/sda1", "/", 100, 70}, {"/dev/sdb1", "/mnt/my disk", 3, 2}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("mounts = %+v", got)
	}
	if got := readMounts(filepath.Join(e.root, "none"), stat); len(got) != 0 {
		t.Errorf("missing mounts file: %v", got)
	}
}

func TestDiskLabelsAndGrouping(t *testing.T) {
	e := newEnv(t)
	disk := func(name, rota, model string) {
		e.put("sys/block/"+name+"/queue/rotational", rota)
		if model != "" {
			e.put("sys/block/"+name+"/device/model", model)
		}
	}
	disk("sda", "0\n", "Samsung SSD 860   \n")
	e.put("sys/block/sda/sda1/partition", "1")
	disk("sdb", "1\n", "ST4000VN006\n")
	e.put("sys/block/sdb/sdb1/partition", "1")
	disk("nvme0n1", "0\n", "")
	e.put("sys/block/nvme0n1/nvme0n1p1/partition", "1")
	disk("sdc", "0\n", "Thing\n")
	e.put("sys/block/loop3/queue/rotational", "0\n")
	e.put("sys/block/dm-0/slaves/sdb1", "")
	e.put("sys/block/dm-0/queue/rotational", "0\n")
	for dev, want := range map[string]string{
		"/dev/sda1": "sda|Samsung SSD 860", "/dev/sdb1": "sdb|ST4000VN006 HDD", "/dev/nvme0n1p1": "nvme0n1|NVMe nvme0n1",
		"/dev/sdc": "sdc|Thing SSD", "/dev/mapper/vg-root": "", "/dev/dm-0": "sdb|ST4000VN006 HDD", "/dev/loop3": "", "/dev/zzz": "",
	} {
		phys, label, ok := diskLabel(e.cfg.SysRoot, dev)
		got := ""
		if ok {
			got = phys + "|" + label
		}
		if got != want {
			t.Errorf("%s = %q, want %q", dev, got, want)
		}
	}
	mounts := []mount{
		{"/dev/sda1", "/", 100, 50}, {"/dev/sdb1", "/data", 400, 300}, {"/dev/sdb1", "/data/bind", 400, 300},
		{"/dev/loop3", "/snap/x", 5, 0},
	}
	disks := buildDisks(e.cfg.SysRoot, mounts, []nodeDir{{"bitcoin", "/data/btc"}, {"monero", "/data/btc/../x"}, {"tari", "/other"}, {"bitcoin", "/data/btc"}})
	want := []Disk{
		{Name: "/dev/loop3", TotalBytes: 5, Nodes: []string{}},
		{Name: "/dev/sda", Label: "Samsung SSD 860", TotalBytes: 100, FreeBytes: 50, Nodes: []string{"tari"}},
		{Name: "/dev/sdb", Label: "ST4000VN006 HDD", TotalBytes: 400, FreeBytes: 300, Nodes: []string{"bitcoin", "monero"}},
	}
	if !reflect.DeepEqual(disks, want) {
		t.Errorf("disks = %+v", disks)
	}
}

func TestEnvFile(t *testing.T) {
	e := newEnv(t)
	e.put("state/.env", "COMPOSE_PROJECT_NAME=first\n #COMPOSE_PROJECT_NAME=no\nCOMPOSE_PROJECT_NAME=last\nXCOMPOSE_PROJECT_NAME=x\n")
	if got := e.cfg.envGet("COMPOSE_PROJECT_NAME"); got != "last" {
		t.Errorf("env value = %q, want the last assignment", got)
	}
	t.Setenv("BITCOIN_STACK_STATE_DIR", filepath.Join(e.root, "state"))
	t.Setenv("BITCOIN_STACK_PROJECT_NAME", "")
	if got := ConfigFromEnv().Project; got != "last" {
		t.Errorf("project = %q", got)
	}
	os.Remove(filepath.Join(e.root, "state/.env"))
	if got := ConfigFromEnv().Project; got != "bitcoin-starter-stack-apt" {
		t.Errorf("default project = %q", got)
	}
	t.Setenv("BITCOIN_STACK_PROJECT_NAME", "forced")
	if got := ConfigFromEnv().Project; got != "forced" {
		t.Errorf("override = %q", got)
	}
	t.Setenv("RESOURCE_THERMAL_GLOB", "")
	t.Setenv("RESOURCE_SYS_ROOT", "/fake")
	if got := ConfigFromEnv().ThermalGlob; got != "/fake/class/thermal/thermal_zone*/temp" {
		t.Errorf("thermal glob = %q", got)
	}
}

func TestSizeCache(t *testing.T) {
	now := time.Unix(0, 0)
	calls, fail := 0, false
	c := &SizeCache{MaxAge: time.Minute, Now: func() time.Time { return now }, Run: func(string) (uint64, bool) {
		calls++
		return uint64(calls), !fail
	}}
	for i, want := range []uint64{1, 1} {
		if v, ok := c.Get("d"); !ok || v != want {
			t.Fatalf("call %d: %d %v", i, v, ok)
		}
	}
	now = now.Add(2 * time.Minute)
	if v, _ := c.Get("d"); v != 2 {
		t.Errorf("expired entry was reused: %d", v)
	}
	now = now.Add(2 * time.Minute)
	fail = true
	if _, ok := c.Get("d"); ok {
		t.Error("a failed run was served from the cache")
	}
	fail = false
	if v, ok := c.Get("d"); !ok || v != 4 {
		t.Errorf("failure was cached: %d %v", v, ok)
	}
}

func TestRunDU(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "f"), make([]byte, 64<<10), 0o644)
	if kb, ok := runDU(dir); !ok || kb < 64 {
		t.Errorf("du = %d %v", kb, ok)
	}
	if _, ok := runDU(filepath.Join(dir, "missing")); ok {
		t.Error("du of a missing dir read as ok")
	}
}

func TestWriteStats(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "host-stats.json")
	os.WriteFile(out, []byte("old"), 0o644)
	st := &Stats{Updated: 5, CPUs: 2, Nodes: map[string]Usage{}, Failed: map[string]string{}, Running: map[string]bool{}, Disks: []Disk{}}
	if err := writeStats(out, st); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(out)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("stat %v %v", info, err)
	}
	if _, err := os.Stat(out + ".tmp"); !os.IsNotExist(err) {
		t.Error("temp file left behind")
	}
	if err := writeStats(filepath.Join(dir, "missing", "x.json"), st); err == nil {
		t.Error("write into a missing dir succeeded")
	}
}

func TestDockerFailures(t *testing.T) {
	boxes := []Container{ctr("dash000000000000", "dashboard", "running")}
	good := DockerInfo{NCPU: 4, MemTotal: 1 << 30}
	cases := map[string]struct {
		socket func(t *testing.T) string
		want   error
	}{
		"daemon down": {func(t *testing.T) string { return filepath.Join(t.TempDir(), "none.sock") }, nil},
		"500": {func(t *testing.T) string {
			return dockerServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "boom", 500) }))
		}, nil},
		"garbage body": {func(t *testing.T) string {
			return dockerServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("<html>")) }))
		}, nil},
		"dashboard missing": {func(t *testing.T) string {
			return staticDocker(t, good, []Container{ctr("x00000000000", "bitcoin", "running")})
		}, ErrNoDashboard},
		"dashboard stopped": {func(t *testing.T) string {
			return staticDocker(t, good, []Container{ctr("d00000000000", "dashboard", "exited")})
		}, ErrNoDashboard},
		"no containers": {func(t *testing.T) string { return staticDocker(t, good, nil) }, ErrNoDashboard},
		"zero cpus":     {func(t *testing.T) string { return staticDocker(t, DockerInfo{MemTotal: 1}, boxes) }, ErrBadInfo},
		"zero memory":   {func(t *testing.T) string { return staticDocker(t, DockerInfo{NCPU: 1}, boxes) }, ErrBadInfo},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			out := filepath.Join(e.root, "out.json")
			os.WriteFile(out, []byte("previous"), 0o600)
			err := run(e.cfg, c.socket(t), out, func() {})
			if err == nil {
				t.Fatal("run succeeded")
			}
			if c.want != nil && !errors.Is(err, c.want) {
				t.Errorf("error = %v, want %v", err, c.want)
			}
			if b, _ := os.ReadFile(out); string(b) != "previous" {
				t.Errorf("a failed sample replaced the old file: %q", b)
			}
		})
	}
}

func TestDockerRequestShape(t *testing.T) {
	var paths, queries []string
	socket := dockerServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths, queries = append(paths, r.URL.Path), append(queries, r.URL.Query().Get("filters")+"|"+r.URL.Query().Get("all"))
		w.Write([]byte(`[]`))
	}))
	if _, err := NewDocker(socket).Containers("my-proj"); err != nil {
		t.Fatal(err)
	}
	if paths[0] != "/containers/json" || queries[0] != `{"label":["com.docker.compose.project=my-proj"]}|1` {
		t.Errorf("request = %v %v", paths, queries)
	}
}

func TestRunWritesFile(t *testing.T) {
	e := newEnv(t)
	e.put("proc/stat", "cpu  1 0 1 8 0\n")
	e.put("proc/meminfo", "MemTotal: 2000 kB\nMemAvailable: 500 kB\n")
	e.put("proc/uptime", "10.5 1\n")
	e.cgroup("systemd", "dash000000000000", 0, 4096)
	socket := staticDocker(t, DockerInfo{NCPU: 2, MemTotal: 1 << 30}, []Container{ctr("dash000000000000", "dashboard", "running")})
	out := filepath.Join(e.root, "host-stats.json")
	waited := false
	if err := run(e.cfg, socket, out, func() { waited = true }); err != nil {
		t.Fatal(err)
	}
	if !waited {
		t.Error("the second sample did not wait")
	}
	info, err := os.Stat(out)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("output %v %v", info, err)
	}
}

func TestWriteStatsDoesNotFollowPlantedTmpSymlink(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "host-stats.json")
	victim := filepath.Join(t.TempDir(), "victim")
	os.WriteFile(victim, []byte("PRECIOUS"), 0o600)
	if err := os.Symlink(victim, out+".tmp"); err != nil {
		t.Fatal(err)
	}
	if err := writeStats(out, &Stats{}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(victim); string(b) != "PRECIOUS" {
		t.Errorf("symlink target overwritten: %q", b)
	}
	if b, _ := os.ReadFile(out); len(b) == 0 {
		t.Error("stats not written")
	}
}

func TestWriteStatsChownsToDirOwner(t *testing.T) {
	dir := t.TempDir()
	var got [2]int
	called := false
	old := fchown
	fchown = func(_ *os.File, uid, gid int) error { got, called = [2]int{uid, gid}, true; return nil }
	defer func() { fchown = old }()
	if err := writeStats(filepath.Join(dir, "s.json"), &Stats{}); err != nil {
		t.Fatal(err)
	}
	st := mustStat(t, dir)
	if !called || got != [2]int{int(st.Uid), int(st.Gid)} {
		t.Errorf("chown called=%v got=%v want dir owner %d:%d", called, got, st.Uid, st.Gid)
	}
}

func mustStat(t *testing.T, path string) *syscall.Stat_t {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Sys().(*syscall.Stat_t)
}

func TestRunDoesNoDuBeforeRateWindowCloses(t *testing.T) {
	e := newEnv(t)
	e.put("proc/stat", "cpu  1 0 1 8 0\n")
	e.put("proc/meminfo", "MemTotal: 2000 kB\nMemAvailable: 500 kB\n")
	e.put("proc/uptime", "10.5 1\n")
	e.put("stack/x", "x")
	e.cgroup("systemd", "dash000000000000", 0, 4096)
	bin := t.TempDir()
	log := filepath.Join(bin, "calls")
	os.WriteFile(filepath.Join(bin, "du"), []byte("#!/bin/sh\necho x >> "+log+"\necho 2048 \"$3\"\n"), 0o755)
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	socket := staticDocker(t, DockerInfo{NCPU: 2, MemTotal: 1 << 30}, []Container{ctr("dash000000000000", "dashboard", "running")})
	out := filepath.Join(e.root, "host-stats.json")
	err := run(e.cfg, socket, out, func() {
		if _, err := os.Stat(log); err == nil {
			t.Error("du ran before the rate window closed")
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(log); err != nil {
		t.Error("du never ran")
	}
}
