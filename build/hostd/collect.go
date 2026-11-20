package main

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

var nodeServices = []string{"bitcoin", "monero", "tari", "litecoin", "dogecoin", "bitcoincash", "zcash"}

var (
	ErrNoDashboard = errors.New("dashboard container is not running")
	ErrBadInfo     = errors.New("docker reported no CPU count or memory total")
)

type Config struct {
	ProcRoot, SysRoot, CgroupRoot, ThermalGlob string
	StackRoot, StateRoot, EnvFile, Project     string
	ClockTicks                                 int
	Statfs                                     func(string) (uint64, uint64, bool)
}

type Usage struct {
	CPUPct    float64 `json:"cpu_pct"`
	MemBytes  uint64  `json:"mem_bytes"`
	DiskBytes uint64  `json:"disk_bytes,omitempty"`
	StartedAt int64   `json:"started_at,omitempty"`
}

type Stats struct {
	Updated       int64             `json:"updated"`
	CPUs          int               `json:"cpus"`
	MemTotalBytes uint64            `json:"mem_total_bytes"`
	HostCPUPct    float64           `json:"host_cpu_pct"`
	HostMemBytes  uint64            `json:"host_mem_bytes"`
	Nodes         map[string]Usage  `json:"nodes"`
	Other         Usage             `json:"other"`
	Failed        map[string]string `json:"failed"`
	Running       map[string]bool   `json:"running"`
	Disks         []Disk            `json:"disks"`
	CPUTempC      *float64          `json:"cpu_temp_c,omitempty"`
	BoardTempC    *float64          `json:"board_temp_c,omitempty"`
	FansRPM       []int             `json:"fans_rpm,omitempty"`
	FansMaxRPM    []int             `json:"fans_max_rpm,omitempty"`
	UptimeS       *int64            `json:"uptime_s,omitempty"`
	NetRxBps      *float64          `json:"net_rx_bps,omitempty"`
	NetTxBps      *float64          `json:"net_tx_bps,omitempty"`
}

type Snapshot struct {
	HostTotal, HostIdle uint64
	HaveCPU             bool
	Containers          map[string]uint64
	Uptime              float64
	HaveUptime          bool
	NetRx, NetTx        uint64
	HaveNet             bool
}

type Rates struct {
	HostCPUPct   float64
	ContainerCPU map[string]float64
	NetRx, NetTx float64
	HaveNet      bool
}

func ConfigFromEnv() Config {
	get := func(key, def string) string {
		if v := os.Getenv(key); v != "" {
			return v
		}
		return def
	}
	c := Config{
		ProcRoot:   get("RESOURCE_PROC_ROOT", "/proc"),
		SysRoot:    get("RESOURCE_SYS_ROOT", "/sys"),
		CgroupRoot: get("RESOURCE_CGROUP_ROOT", "/sys/fs/cgroup"),
		StackRoot:  get("BITCOIN_STACK_ROOT", "/usr/lib/node-manager"),
		StateRoot:  get("BITCOIN_STACK_STATE_DIR", "/var/lib/node-manager"),
		ClockTicks: 100,
		Statfs:     statfs,
	}
	c.ThermalGlob = get("RESOURCE_THERMAL_GLOB", filepath.Join(c.SysRoot, "class/thermal/thermal_zone*/temp"))
	c.EnvFile = get("BITCOIN_STACK_ENV_FILE", filepath.Join(c.StateRoot, ".env"))
	c.Project = get("BITCOIN_STACK_PROJECT_NAME", c.envGet("COMPOSE_PROJECT_NAME"))
	if c.Project == "" {
		c.Project = "bitcoin-starter-stack-apt"
	}
	return c
}

func (c Config) envGet(key string) string {
	text, _ := readText(c.EnvFile)
	value := ""
	for _, line := range strings.Split(text, "\n") {
		if v, ok := strings.CutPrefix(line, key+"="); ok {
			value = v
		}
	}
	return value
}

func (c Config) dataDir(service string) string {
	if dir := c.envGet(strings.ToUpper(service) + "_DATA_DIR"); dir != "" {
		return dir
	}
	return filepath.Join(c.StateRoot, "data", service)
}

func (c Config) Snapshot(ids []string) Snapshot {
	s := Snapshot{Containers: map[string]uint64{}}
	s.HostTotal, s.HostIdle, s.HaveCPU = readCPU(c.ProcRoot)
	for _, id := range ids {
		if v, ok := readUsageUsec(c.CgroupRoot, id); ok {
			s.Containers[id] = v
		}
	}
	s.Uptime, s.HaveUptime = readUptime(c.ProcRoot)
	s.NetRx, s.NetTx, s.HaveNet = readNet(c.ProcRoot, physIfaces(c.SysRoot))
	return s
}

func Diff(a, b Snapshot, hz int) Rates {
	r := Rates{ContainerCPU: map[string]float64{}}
	var ticks int64
	if a.HaveCPU && b.HaveCPU {
		ticks = int64(b.HostTotal) - int64(a.HostTotal)
		if ticks > 0 {
			busy := ticks - (int64(b.HostIdle) - int64(a.HostIdle))
			r.HostCPUPct = roundTo(float64(busy)*100/float64(ticks), 1)
		}
	}
	if ticks > 0 {
		for id, after := range b.Containers {
			if before, ok := a.Containers[id]; ok && after >= before {
				r.ContainerCPU[id] = roundTo(float64(after-before)*float64(hz)/(float64(ticks)*10000), 4)
			}
		}
	}
	if a.HaveNet && b.HaveNet && a.HaveUptime && b.HaveUptime && b.Uptime > a.Uptime && b.NetRx >= a.NetRx && b.NetTx >= a.NetTx {
		dt := b.Uptime - a.Uptime
		r.NetRx = roundTo(float64(b.NetRx-a.NetRx)/dt, 0)
		r.NetTx = roundTo(float64(b.NetTx-a.NetTx)/dt, 0)
		r.HaveNet = true
	}
	return r
}

type Sampler struct {
	Cfg    Config
	Docker *Docker
	Sizes  *SizeCache
	Now    func() time.Time
}

func nodeOf(service string) (string, bool) {
	node := strings.TrimSuffix(service, "-wallet")
	for _, n := range nodeServices {
		if n == node {
			return node, true
		}
	}
	return "", false
}

func isNodeService(service string) bool {
	for _, n := range nodeServices {
		if n == service {
			return true
		}
	}
	return false
}

func (s *Sampler) Prime() (*Snapshot, error) {
	list, err := s.Docker.Containers(s.Cfg.Project)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(list))
	for _, ct := range list {
		ids = append(ids, ct.ID)
	}
	snap := s.Cfg.Snapshot(ids)
	return &snap, nil
}

func (s *Sampler) Sample(prev *Snapshot) (*Stats, *Snapshot, error) {
	info, err := s.Docker.Info()
	if err != nil {
		return nil, nil, err
	}
	if info.NCPU <= 0 || info.MemTotal == 0 {
		return nil, nil, ErrBadInfo
	}
	list, err := s.Docker.Containers(s.Cfg.Project)
	if err != nil {
		return nil, nil, err
	}
	dashboard := false
	ids := make([]string, 0, len(list))
	for _, ct := range list {
		ids = append(ids, ct.ID)
		if ct.Labels["com.docker.compose.service"] == "dashboard" && ct.State == "running" {
			dashboard = true
		}
	}
	if !dashboard {
		return nil, nil, ErrNoDashboard
	}
	snap := s.Cfg.Snapshot(ids)
	if prev == nil {
		prev = &snap
	}
	rates := Diff(*prev, snap, s.Cfg.ClockTicks)

	st := &Stats{
		Updated: s.Now().Unix(), CPUs: info.NCPU, MemTotalBytes: info.MemTotal, HostCPUPct: rates.HostCPUPct,
		Nodes: map[string]Usage{}, Failed: map[string]string{}, Running: map[string]bool{},
	}
	if used, ok := readMemUsed(s.Cfg.ProcRoot); ok {
		st.HostMemBytes = used
	}
	for _, ct := range list {
		service := ct.Labels["com.docker.compose.service"]
		if isNodeService(service) {
			alive := ct.State != "exited" && ct.State != "dead"
			st.Running[service] = st.Running[service] || alive
		}
		mem, ok := readCgroupMem(s.Cfg.CgroupRoot, ct.ID)
		if !ok {
			continue
		}
		cpu := rates.ContainerCPU[ct.ID]
		if node, isNode := nodeOf(service); isNode {
			u := st.Nodes[node]
			u.CPUPct += cpu
			u.MemBytes += mem
			if service == node && ct.State == "running" {
				if started, err := s.Docker.StartedAt(ct.ID); err == nil {
					u.StartedAt = started
				}
			}
			st.Nodes[node] = u
		} else {
			st.Other.CPUPct += cpu
			st.Other.MemBytes += mem
		}
	}
	for _, ct := range list {
		service := ct.Labels["com.docker.compose.service"]
		if !isNodeService(service) || ct.State != "exited" {
			continue
		}
		if id, reason, ok := s.failedRecord(service); ok && strings.HasPrefix(ct.ID, id) {
			st.Failed[service] = reason
		}
	}
	s.addSizes(st, info.DockerRootDir)
	st.CPUTempC, st.BoardTempC = readThermal(s.Cfg.ThermalGlob)
	st.FansRPM, st.FansMaxRPM = readFans(s.Cfg.SysRoot)
	anyMax := false
	for _, m := range st.FansMaxRPM {
		anyMax = anyMax || m > 0
	}
	if !anyMax {
		st.FansMaxRPM = nil
	}
	if snap.HaveUptime {
		v, _ := strconv.ParseInt(strconv.FormatFloat(snap.Uptime, 'f', 0, 64), 10, 64)
		st.UptimeS = &v
	}
	if rates.HaveNet {
		st.NetRxBps, st.NetTxBps = &rates.NetRx, &rates.NetTx
	}
	return st, &snap, nil
}

func (s *Sampler) failedRecord(service string) (id, reason string, ok bool) {
	text, readable := readText(filepath.Join(s.Cfg.StateRoot, "resource-profile", "failed-"+service))
	if !readable {
		return "", "", false
	}
	line, _, _ := strings.Cut(text, "\n")
	f := strings.Split(line, "\t")
	if len(f) < 4 || len(f[0]) < 12 {
		return "", "", false
	}
	return f[0], f[3], true
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func (s *Sampler) sizeDirs() []string {
	var dirs []string
	for _, service := range nodeServices {
		dirs = append(dirs, s.Cfg.dataDir(service))
	}
	dirs = append(dirs, s.Cfg.StackRoot, s.Cfg.StateRoot)
	kept := dirs[:0]
	for _, dir := range dirs {
		if isDir(dir) {
			kept = append(kept, dir)
		}
	}
	return kept
}

func (s *Sampler) addSizes(st *Stats, dockerRoot string) {
	if s.Sizes.Lazy {
		s.Sizes.RefreshOne(s.sizeDirs())
	}
	var dirs []nodeDir
	var subKB uint64
	for _, service := range nodeServices {
		dir := s.Cfg.dataDir(service)
		dirs = append(dirs, nodeDir{service, dir})
		if !isDir(dir) {
			continue
		}
		kb, ok := s.Sizes.Get(dir)
		if !ok || kb < 1024 {
			continue
		}
		u := st.Nodes[service]
		u.DiskBytes = kb * 1024
		st.Nodes[service] = u
		if strings.HasPrefix(dir, s.Cfg.StateRoot+"/") || strings.HasPrefix(dir, s.Cfg.StackRoot+"/") {
			subKB += kb
		}
	}
	var stackKB uint64
	for _, dir := range []string{s.Cfg.StackRoot, s.Cfg.StateRoot} {
		if !isDir(dir) {
			continue
		}
		if kb, ok := s.Sizes.Get(dir); ok {
			stackKB += kb
		}
	}
	stackKB -= min(stackKB, subKB)
	if stackKB > 0 {
		st.Nodes["stack"] = Usage{DiskBytes: stackKB * 1024}
	}
	dirs = append(dirs, nodeDir{"stack", s.Cfg.StackRoot}, nodeDir{"stack", s.Cfg.StateRoot})
	if dockerRoot != "" {
		dirs = append(dirs, nodeDir{"stack", dockerRoot})
	}
	st.Disks = buildDisks(s.Cfg.SysRoot, readMounts(s.Cfg.ProcRoot, s.Cfg.Statfs), dirs)
}
