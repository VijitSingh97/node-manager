package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func captureChown(t *testing.T) *[][2]int {
	t.Helper()
	var calls [][2]int
	old := fchown
	fchown = func(_ *os.File, uid, gid int) error { calls = append(calls, [2]int{uid, gid}); return nil }
	t.Cleanup(func() { fchown = old })
	return &calls
}

func TestVolumeWriterWritesOwnedPrivateFile(t *testing.T) {
	calls := captureChown(t)
	dir := t.TempDir()
	if err := writeStatsVolume(dir, &Stats{Updated: 7}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, statsName))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("stat %v %v", info, err)
	}
	owner := mustStat(t, dir)
	if len(*calls) != 1 || (*calls)[0] != [2]int{int(owner.Uid), int(owner.Gid)} {
		t.Errorf("chown calls %v, want dir owner %d:%d", *calls, owner.Uid, owner.Gid)
	}
	if _, err := os.Stat(filepath.Join(dir, statsTmp)); !os.IsNotExist(err) {
		t.Error("tmp file left behind")
	}
}

func TestVolumeWriterIgnoresPlantedSymlinks(t *testing.T) {
	captureChown(t)
	for _, name := range []string{statsTmp, statsName} {
		dir := t.TempDir()
		victim := filepath.Join(t.TempDir(), "victim")
		os.WriteFile(victim, []byte("PRECIOUS"), 0o600)
		if err := os.Symlink(victim, filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
		if err := writeStatsVolume(dir, &Stats{Updated: 7}); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if b, _ := os.ReadFile(victim); string(b) != "PRECIOUS" {
			t.Errorf("%s: symlink target overwritten: %q", name, b)
		}
		info, err := os.Lstat(filepath.Join(dir, statsName))
		if err != nil || !info.Mode().IsRegular() {
			t.Errorf("%s: result %v %v", name, info, err)
		}
	}
}

func TestVolumeWriterRejectsSymlinkOrFileMountpoint(t *testing.T) {
	captureChown(t)
	target := t.TempDir()
	link := filepath.Join(t.TempDir(), "mp")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := writeStatsVolume(link, &Stats{}); err == nil {
		t.Error("symlink mountpoint accepted")
	}
	file := filepath.Join(t.TempDir(), "f")
	os.WriteFile(file, nil, 0o600)
	if err := writeStatsVolume(file, &Stats{}); err == nil {
		t.Error("file mountpoint accepted")
	}
	if err := writeStatsVolume(filepath.Join(target, "missing"), &Stats{}); err == nil {
		t.Error("missing mountpoint accepted")
	}
	if entries, _ := os.ReadDir(target); len(entries) != 0 {
		t.Errorf("wrote through a bad mountpoint: %v", entries)
	}
}

func TestVolumeWriterStaysInsideDir(t *testing.T) {
	captureChown(t)
	outside := t.TempDir()
	dir := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dir, "sub")); err != nil {
		t.Fatal(err)
	}
	if err := writeStatsVolume(dir, &Stats{}); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Errorf("wrote outside the volume: %v", entries)
	}
}

func TestVolumeLookup(t *testing.T) {
	var paths []string
	socket := dockerServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		switch r.URL.Path {
		case "/volumes/proj_dashboard_state":
			json.NewEncoder(w).Encode(map[string]string{"Name": "proj_dashboard_state", "Mountpoint": "/vol/_data"})
		case "/volumes/liar_dashboard_state":
			json.NewEncoder(w).Encode(map[string]string{"Name": "other", "Mountpoint": "/vol/_data"})
		case "/volumes/rel_dashboard_state":
			json.NewEncoder(w).Encode(map[string]string{"Name": "rel_dashboard_state", "Mountpoint": "vol/_data"})
		default:
			http.NotFound(w, r)
		}
	}))
	d := NewDocker(socket)
	if mp, err := d.VolumeMountpoint("proj_dashboard_state"); err != nil || mp != "/vol/_data" {
		t.Errorf("mountpoint %q %v", mp, err)
	}
	for _, name := range []string{"absent_dashboard_state", "liar_dashboard_state", "rel_dashboard_state"} {
		if mp, err := d.VolumeMountpoint(name); err == nil {
			t.Errorf("%s resolved to %q", name, mp)
		}
	}
	if paths[0] != "/volumes/proj_dashboard_state" {
		t.Errorf("paths %v", paths)
	}
}

func TestSizeCacheRefreshOneOldestFirstAndOmittedUntilMeasured(t *testing.T) {
	now := time.Unix(10000, 0)
	var ran []string
	c := &SizeCache{MaxAge: 10 * time.Minute, Lazy: true, Now: func() time.Time { return now },
		Run: func(dir string) (uint64, bool) { ran = append(ran, dir); return 5, true }}
	c.Import(map[string]duRecord{
		"fresh": {KB: 1, MeasuredAt: now.Unix() - 60}, "old": {KB: 2, MeasuredAt: now.Unix() - 3000}, "older": {KB: 3, MeasuredAt: now.Unix() - 5000},
	})
	dirs := []string{"fresh", "old", "never", "older"}
	if _, ok := c.Get("never"); ok {
		t.Error("never-measured dir was served")
	}
	if kb, ok := c.Get("old"); !ok || kb != 2 {
		t.Errorf("stale value not served: %d %v", kb, ok)
	}
	for _, want := range []string{"never", "older", "old"} {
		c.RefreshOne(dirs)
		if got := ran[len(ran)-1]; got != want {
			t.Fatalf("refreshed %q, want %q (all %v)", got, want, ran)
		}
	}
	c.RefreshOne(dirs)
	if len(ran) != 3 {
		t.Errorf("fresh dirs refreshed: %v", ran)
	}
	if kb, ok := c.Get("never"); !ok || kb != 5 {
		t.Errorf("measured dir not served: %d %v", kb, ok)
	}
}

func TestStateLoadRejectsBadFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	if loadState(path) != nil {
		t.Error("missing file loaded")
	}
	good, _ := json.Marshal(&State{Version: stateVersion, SavedAt: 5})
	for name, body := range map[string][]byte{
		"corrupt": []byte("{nope"),
		"old":     []byte(`{"version":0}`),
		"future":  []byte(`{"version":99}`),
		"big":     append(bytes.TrimSuffix(good, []byte("}")), append([]byte(`,"pad":"`), append(bytes.Repeat([]byte("x"), stateMaxSize), []byte(`"}`)...)...)...),
	} {
		os.WriteFile(path, body, 0o600)
		if loadState(path) != nil {
			t.Errorf("%s state loaded", name)
		}
	}
	os.WriteFile(path, good, 0o600)
	if loadState(path) == nil {
		t.Error("good state rejected")
	}
}

func TestSaveStateIsPrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "state.json")
	if err := saveState(path, &State{Version: stateVersion}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("stat %v %v", info, err)
	}
}

type fakeHost struct {
	e      *env
	socket string
	now    time.Time
	waits  int
	state  string
}

func newFakeHost(t *testing.T) *fakeHost {
	e := newEnv(t)
	e.put("proc/stat", "cpu  1 0 1 8 0\n")
	e.put("proc/meminfo", "MemTotal: 2000 kB\nMemAvailable: 500 kB\n")
	e.put("proc/uptime", "100.0 1\n")
	e.cgroup("systemd", "dash000000000000", 0, 4096)
	socket := staticDocker(t, DockerInfo{NCPU: 2, MemTotal: 1 << 30}, []Container{ctr("dash000000000000", "dashboard", "running")})
	return &fakeHost{e: e, socket: socket, now: time.Unix(5000, 0), state: filepath.Join(e.root, "hostd/state.json")}
}

func (h *fakeHost) run(t *testing.T) error {
	t.Helper()
	return job{Cfg: h.e.cfg, Socket: h.socket, StateFile: h.state, Wait: func() { h.waits++ }, Now: func() time.Time { return h.now },
		Write: func(*Stats) error { return nil }}.execute()
}

func TestStateDeltaPathSkipsSleep(t *testing.T) {
	captureChown(t)
	h := newFakeHost(t)
	if err := h.run(t); err != nil || h.waits != 1 {
		t.Fatalf("first run: %v waits=%d", err, h.waits)
	}
	h.now = h.now.Add(10 * time.Second)
	h.e.put("proc/uptime", "110.0 1\n")
	if err := h.run(t); err != nil || h.waits != 1 {
		t.Fatalf("second run: %v waits=%d, want no extra sleep", err, h.waits)
	}
}

func TestStateIgnoredWhenStaleOrUptimeReset(t *testing.T) {
	captureChown(t)
	h := newFakeHost(t)
	h.run(t)
	h.now = h.now.Add(61 * time.Second)
	h.e.put("proc/uptime", "161.0 1\n")
	h.run(t)
	if h.waits != 2 {
		t.Errorf("stale state reused: waits=%d", h.waits)
	}
	h.now = h.now.Add(10 * time.Second)
	h.e.put("proc/uptime", "5.0 1\n")
	h.run(t)
	if h.waits != 3 {
		t.Errorf("state reused across an uptime reset: waits=%d", h.waits)
	}
}

func TestStateCorruptIsRebuiltNotFatal(t *testing.T) {
	h := newFakeHost(t)
	os.MkdirAll(filepath.Dir(h.state), 0o700)
	os.WriteFile(h.state, []byte("garbage"), 0o600)
	if err := h.run(t); err != nil || h.waits != 1 {
		t.Fatalf("%v waits=%d", err, h.waits)
	}
	if loadState(h.state) == nil {
		t.Error("state not rebuilt")
	}
}

func TestExecuteMeasuresOneDirPerRun(t *testing.T) {
	h := newFakeHost(t)
	h.e.put("stack/x", "x")
	h.e.put("state/x", "x")
	bin := t.TempDir()
	log := filepath.Join(bin, "calls")
	os.WriteFile(filepath.Join(bin, "du"), []byte("#!/bin/sh\necho \"$3\" >> "+log+"\necho 2048 \"$3\"\n"), 0o755)
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	h.run(t)
	h.now = h.now.Add(10 * time.Second)
	h.e.put("proc/uptime", "110.0 1\n")
	h.run(t)
	b, _ := os.ReadFile(log)
	if lines := strings.Fields(string(b)); len(lines) != 2 || lines[0] == lines[1] {
		t.Errorf("du calls %v, want two different dirs", lines)
	}
}

func TestMainRunExitCodes(t *testing.T) {
	var sink bytes.Buffer
	for _, args := range [][]string{
		{}, {"-once"}, {"-volume"}, {"-once", "-bogus"},
	} {
		if code := mainRun(args, &sink); code != 2 {
			t.Errorf("%v: exit %d, want 2", args, code)
		}
	}
	h := newFakeHost(t)
	t.Setenv("RESOURCE_PROC_ROOT", h.e.cfg.ProcRoot)
	t.Setenv("RESOURCE_SYS_ROOT", h.e.cfg.SysRoot)
	t.Setenv("RESOURCE_CGROUP_ROOT", h.e.cfg.CgroupRoot)
	t.Setenv("BITCOIN_STACK_STATE_DIR", h.e.cfg.StateRoot)
	t.Setenv("BITCOIN_STACK_ROOT", h.e.cfg.StackRoot)
	t.Setenv("BITCOIN_STACK_PROJECT_NAME", "p")
	t.Setenv("HOSTD_DOCKER_SOCKET", filepath.Join(h.e.root, "nosuch.sock"))
	if code := mainRun([]string{"-once", "-volume"}, &sink); code != 1 {
		t.Errorf("docker down: exit %d, want 1", code)
	}
	t.Setenv("HOSTD_DOCKER_SOCKET", h.socket)
	if code := mainRun([]string{"-once", "-volume"}, &sink); code != 1 {
		t.Errorf("missing volume: exit %d, want 1", code)
	}
}

func TestMainRunVolumeWritesIntoMountpoint(t *testing.T) {
	captureChown(t)
	h := newFakeHost(t)
	mount := t.TempDir()
	socket := dockerServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/info":
			json.NewEncoder(w).Encode(DockerInfo{NCPU: 2, MemTotal: 1 << 30})
		case "/containers/json":
			json.NewEncoder(w).Encode([]Container{ctr("dash000000000000", "dashboard", "running")})
		case "/volumes/p_dashboard_state":
			json.NewEncoder(w).Encode(map[string]string{"Name": "p_dashboard_state", "Mountpoint": mount})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Setenv("RESOURCE_PROC_ROOT", h.e.cfg.ProcRoot)
	t.Setenv("RESOURCE_SYS_ROOT", h.e.cfg.SysRoot)
	t.Setenv("RESOURCE_CGROUP_ROOT", h.e.cfg.CgroupRoot)
	t.Setenv("BITCOIN_STACK_STATE_DIR", h.e.cfg.StateRoot)
	t.Setenv("BITCOIN_STACK_PROJECT_NAME", "p")
	t.Setenv("HOSTD_DOCKER_SOCKET", socket)
	var sink bytes.Buffer
	if code := mainRun([]string{"-once", "-volume", "-state-file", h.state}, &sink); code != 0 {
		t.Fatalf("exit %d: %s", code, sink.String())
	}
	if _, err := os.Stat(filepath.Join(mount, statsName)); err != nil {
		t.Error(err)
	}
}

func TestRunDUTimesOut(t *testing.T) {
	bin := t.TempDir()
	os.WriteFile(filepath.Join(bin, "du"), []byte("#!/bin/sh\nexec /bin/sleep 30\n"), 0o755)
	t.Setenv("PATH", bin)
	old := duTimeout
	duTimeout = 200 * time.Millisecond
	defer func() { duTimeout = old }()
	start := time.Now()
	if _, ok := runDU(t.TempDir()); ok {
		t.Error("a du that outran the deadline read as ok")
	}
	if time.Since(start) > 10*time.Second {
		t.Error("du was not stopped at the deadline")
	}
}

func TestRefreshOneBacksOffAfterFailure(t *testing.T) {
	now := time.Unix(10000, 0)
	var ran []string
	c := &SizeCache{MaxAge: 10 * time.Minute, Lazy: true, Now: func() time.Time { return now },
		Run: func(dir string) (uint64, bool) { ran = append(ran, dir); return 5, dir != "bad" }}
	dirs := []string{"bad", "good"}
	c.RefreshOne(dirs)
	c.RefreshOne(dirs)
	if len(ran) != 2 || ran[0] != "bad" || ran[1] != "good" {
		t.Fatalf("runs %v, want bad then good", ran)
	}
	if _, ok := c.Get("bad"); ok {
		t.Error("a failed dir was served")
	}
	c2 := &SizeCache{MaxAge: 10 * time.Minute, Lazy: true, Now: c.Now, Run: c.Run}
	c2.Import(c.Export())
	c2.RefreshOne(dirs)
	if len(ran) != 2 {
		t.Errorf("the failed attempt was not carried over: %v", ran)
	}
	now = now.Add(11 * time.Minute)
	c2.RefreshOne(dirs)
	if len(ran) != 3 {
		t.Errorf("dir not retried after MaxAge: %v", ran)
	}
}

func TestMainRunNoDashboardExitsClean(t *testing.T) {
	h := newFakeHost(t)
	socket := staticDocker(t, DockerInfo{NCPU: 2, MemTotal: 1 << 30}, nil)
	t.Setenv("RESOURCE_PROC_ROOT", h.e.cfg.ProcRoot)
	t.Setenv("BITCOIN_STACK_PROJECT_NAME", "p")
	t.Setenv("HOSTD_DOCKER_SOCKET", socket)
	var sink bytes.Buffer
	if code := mainRun([]string{"-once", "-volume"}, &sink); code != 0 || sink.Len() != 0 {
		t.Errorf("exit %d output %q, want a silent 0", code, sink.String())
	}
}
