package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	seedStartMaxC    = 75.0
	seedPauseC       = 85.0
	seedResumeC      = 72.0
	seedMaxCPU       = 0.5
	seedMinFreeBytes = 27_000_000_000
	seedMaxTipAge    = 2 * time.Hour
	seedMinMemAvail  = 512 << 20
	seedMinMemLimit  = 512 << 20

	seedThermalGlob = "/sys/class/thermal/thermal_zone*/temp"
	seedCgroupRoot  = "/sys/fs/cgroup"
)

type seedRefusal struct {
	Guard  string `json:"guard"`
	Detail string `json:"detail"`
}

type seedGuardInput struct {
	Replay     bool
	Chainstate bool
	// ChainstateBytes is the node's live chainstate on disk, which a chainstate seed's sort runs and tables scale with.
	ChainstateBytes uint64
	ChainstateErr   error
	State           string
	TempC           float64
	TempErr         error
	CPUs            float64
	CPUErr          error
	FreeBytes       uint64
	FreeErr         error
	MemAvailBytes   uint64
	MemErr          error
	MemLimitBytes   uint64
	Chain           blockchainInfo
	TipTime         time.Time
	ChainErr        error
	RPCMethods      []string
	RPCErr          error
	Now             time.Time
}

// dirBytes sums the regular files directly in dir; a chainstate keeps no subdirectories.
func dirBytes(dir string) (uint64, error) {
	entries, err := os.ReadDir(dir)
	var total uint64
	for _, e := range entries {
		if info, err := e.Info(); err == nil && info.Mode().IsRegular() {
			total += uint64(info.Size())
		}
	}
	return total, err
}

func evaluateSeedGuards(in seedGuardInput) []seedRefusal {
	var out []seedRefusal
	// A replay writes no sort runs, so it needs only the runtime free-space floor.
	need := uint64(seedMinFreeBytes)
	switch {
	case in.Replay:
		need = utxoMinFreeBytes
	// Bitcoin's measured seed needs about 2.5 times its chainstate, so a chainstate seed asks for three times its own.
	case in.Chainstate:
		need = max(utxoMinFreeBytes, 3*in.ChainstateBytes)
	}
	guards := []*seedRefusal{
		stateGuard(in.State, in.Replay),
		thermalStartGuard(in.TempC, in.TempErr),
		cpuGuard(in.CPUs, in.CPUErr),
		freeSpaceGuard(in.FreeBytes, need, cmp.Or(in.FreeErr, chainstateErr(in))),
		memoryGuard(in.MemAvailBytes, in.MemLimitBytes),
		nodeGuard(in.Chain, in.TipTime, in.Now, in.ChainErr),
	}
	switch {
	case in.Replay:
		guards = append(guards, archivalGuard(in.Chain, in.ChainErr))
	case !in.Chainstate:
		guards = append(guards, rpcIdleGuard(in.RPCMethods, in.RPCErr))
	}
	for _, r := range guards {
		if r != nil {
			out = append(out, *r)
		}
	}
	return out
}

func chainstateErr(in seedGuardInput) error {
	if in.Chainstate {
		return in.ChainstateErr
	}
	return nil
}

func stateGuard(state string, replay bool) *seedRefusal {
	if seedableState(state, replay) {
		return nil
	}
	return &seedRefusal{"state", "index state is " + state}
}

func archivalGuard(info blockchainInfo, err error) *seedRefusal {
	if err == nil && info.Pruned {
		return &seedRefusal{"node", "the node is pruned; replaying every block needs an archival node"}
	}
	return nil
}

func thermalStartGuard(c float64, err error) *seedRefusal {
	switch {
	case err != nil:
		return &seedRefusal{"thermal", "temperature sensor unreadable"}
	case c > seedStartMaxC:
		return &seedRefusal{"thermal", fmt.Sprintf("hottest zone %.0f C is above %.0f C", c, seedStartMaxC)}
	}
	return nil
}

func thermalPaused(paused bool, c float64, err error) bool {
	switch {
	case err != nil:
		return true
	case paused:
		return c > seedResumeC
	}
	return c >= seedPauseC
}

func cpuGuard(cpus float64, err error) *seedRefusal {
	switch {
	case err != nil:
		return &seedRefusal{"cpu_cap", "cpu limit unreadable"}
	case cpus > seedMaxCPU:
		return &seedRefusal{"cpu_cap", "cpu limit is above 0.5"}
	}
	return nil
}

func freeSpaceGuard(free, need uint64, err error) *seedRefusal {
	if err != nil {
		return &seedRefusal{"free_space", "free space is unreadable"}
	}
	if free < need {
		return &seedRefusal{"free_space", fmt.Sprintf("%.1f GB free, %.0f GB needed", float64(free)/1e9, float64(need)/1e9)}
	}
	return nil
}

func memoryGuard(avail, limit uint64) *seedRefusal {
	switch {
	case avail > 0 && avail < seedMinMemAvail:
		return &seedRefusal{"memory", fmt.Sprintf("%d MiB of host memory available, %d MiB needed", avail>>20, seedMinMemAvail>>20)}
	case limit > 0 && limit < seedMinMemLimit:
		return &seedRefusal{"memory", fmt.Sprintf("dashboard memory limit is %d MiB, %d MiB needed", limit>>20, seedMinMemLimit>>20)}
	}
	return nil
}

func nodeGuard(info blockchainInfo, tipTime, now time.Time, err error) *seedRefusal {
	switch {
	case err != nil:
		return &seedRefusal{"node", "node did not answer"}
	case info.InitialBlockDownload:
		return &seedRefusal{"node", "node is in initial block download"}
	case info.Headers != info.Blocks:
		return &seedRefusal{"node", "headers are ahead of blocks"}
	case tipTime.IsZero() || now.Sub(tipTime) > seedMaxTipAge:
		return &seedRefusal{"node", "tip is older than 2 hours"}
	}
	return nil
}

func rpcIdleGuard(methods []string, err error) *seedRefusal {
	if err != nil {
		return &seedRefusal{"rpc_idle", "active commands unreadable"}
	}
	if slices.ContainsFunc(methods, func(m string) bool { return m == "dumptxoutset" || m == "scantxoutset" }) {
		return &seedRefusal{"rpc_idle", "a dump or scan is already running"}
	}
	return nil
}

func hottestThermalC(glob string) (float64, error) {
	paths, err := filepath.Glob(glob)
	if err != nil || len(paths) == 0 {
		return 0, errors.New("no thermal zones")
	}
	hottest := math.Inf(-1)
	for _, p := range paths {
		raw, err := os.ReadFile(p)
		if err != nil {
			return 0, err
		}
		milli, err := strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64)
		if err != nil {
			return 0, err
		}
		hottest = max(hottest, float64(milli)/1000)
	}
	return hottest, nil
}

func parseCgroupV2CPUMax(raw string) (float64, error) {
	f := strings.Fields(raw)
	if len(f) != 2 {
		return 0, errors.New("malformed cpu.max")
	}
	return cpuLimit(f[0], f[1], "max")
}

func parseCgroupV1CPU(quota, period string) (float64, error) {
	return cpuLimit(strings.TrimSpace(quota), strings.TrimSpace(period), "-1")
}

func cpuLimit(quota, period, unlimited string) (float64, error) {
	p, err := strconv.ParseFloat(period, 64)
	if err != nil || p <= 0 {
		return 0, errors.New("malformed cpu period")
	}
	if quota == unlimited {
		return math.Inf(1), nil
	}
	q, err := strconv.ParseFloat(quota, 64)
	if err != nil || q <= 0 {
		return 0, errors.New("malformed cpu quota")
	}
	return q / p, nil
}

func readCPULimit(root string) (float64, error) {
	if raw, err := os.ReadFile(filepath.Join(root, "cpu.max")); err == nil {
		return parseCgroupV2CPUMax(string(raw))
	}
	for _, dir := range []string{"cpu", "cpu,cpuacct"} {
		quota, err := os.ReadFile(filepath.Join(root, dir, "cpu.cfs_quota_us"))
		if err != nil {
			continue
		}
		period, err := os.ReadFile(filepath.Join(root, dir, "cpu.cfs_period_us"))
		if err != nil {
			continue
		}
		return parseCgroupV1CPU(string(quota), string(period))
	}
	return 0, errors.New("no cgroup cpu limit found")
}

func readMemLimit(root string) uint64 {
	for _, name := range []string{"memory.max", "memory/memory.limit_in_bytes"} {
		raw, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			continue
		}
		if n, err := strconv.ParseUint(strings.TrimSpace(string(raw)), 10, 64); err == nil && n < 1<<50 {
			return n
		}
		return 0
	}
	return 0
}

func readMemAvailable(path string) (uint64, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if f := strings.Fields(line); len(f) >= 2 && f[0] == "MemAvailable:" {
			kb, err := strconv.ParseUint(f[1], 10, 64)
			return kb << 10, err
		}
	}
	return 0, errors.New("MemAvailable missing")
}

func readActiveRPCMethods(ctx context.Context, rpc utxoRPC) ([]string, error) {
	var info struct {
		Active []struct {
			Method string `json:"method"`
		} `json:"active_commands"`
	}
	if err := rpc.call(ctx, "getrpcinfo", nil, &info); err != nil {
		return nil, err
	}
	methods := make([]string, 0, len(info.Active))
	for _, c := range info.Active {
		methods = append(methods, c.Method)
	}
	return methods, nil
}

func readSeedChain(ctx context.Context, rpc utxoRPC) (blockchainInfo, time.Time, error) {
	var info blockchainInfo
	if err := rpc.call(ctx, "getblockchaininfo", nil, &info); err != nil {
		return info, time.Time{}, err
	}
	var header struct {
		Time int64 `json:"time"`
	}
	if err := rpc.call(ctx, "getblockheader", []any{info.BestBlockHash}, &header); err != nil {
		return info, time.Time{}, err
	}
	return info, time.Unix(header.Time, 0), nil
}

func (m *utxoManager) gatherSeedGuards(ctx context.Context) seedGuardInput {
	in := seedGuardInput{State: m.Status().State, Now: m.now()}
	if d := diskUsage(m.dir); d.Total > 0 {
		in.FreeBytes = d.Free
	} else {
		in.FreeErr = errors.New("statfs failed")
	}
	in.TempC, in.TempErr = m.temp()
	in.CPUs, in.CPUErr = readCPULimit(seedCgroupRoot)
	in.MemAvailBytes, in.MemErr = readMemAvailable("/proc/meminfo")
	in.MemLimitBytes = readMemLimit(seedCgroupRoot)
	in.Chain, in.TipTime, in.ChainErr = readSeedChain(ctx, m.rpc)
	if m.chain.Chainstate && m.host != nil {
		// Without the chainstate's size the space a seed needs is unknown, so it counts as unreadable free space.
		if bytes, err := dirBytes(filepath.Join(m.host.dataDir, "chainstate")); err != nil {
			in.ChainstateErr = err
			if !m.chain.Replay {
				in.FreeErr = err
			}
		} else {
			in.ChainstateBytes = bytes
		}
		return in
	}
	in.RPCMethods, in.RPCErr = readActiveRPCMethods(ctx, m.rpc)
	return in
}
