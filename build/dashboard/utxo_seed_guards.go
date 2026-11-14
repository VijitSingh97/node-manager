package main

import (
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

	seedThermalGlob = "/sys/class/thermal/thermal_zone*/temp"
	seedCgroupRoot  = "/sys/fs/cgroup"
)

type seedRefusal struct {
	Guard  string `json:"guard"`
	Detail string `json:"detail"`
}

type seedGuardInput struct {
	State      string
	TempC      float64
	TempErr    error
	CPUs       float64
	CPUErr     error
	FreeBytes  uint64
	FreeErr    error
	Chain      blockchainInfo
	TipTime    time.Time
	ChainErr   error
	RPCMethods []string
	RPCErr     error
	Now        time.Time
}

func evaluateSeedGuards(in seedGuardInput) []seedRefusal {
	var out []seedRefusal
	for _, r := range []*seedRefusal{
		stateGuard(in.State),
		thermalStartGuard(in.TempC, in.TempErr),
		cpuGuard(in.CPUs, in.CPUErr),
		freeSpaceGuard(in.FreeBytes, in.FreeErr),
		nodeGuard(in.Chain, in.TipTime, in.Now, in.ChainErr),
		rpcIdleGuard(in.RPCMethods, in.RPCErr),
	} {
		if r != nil {
			out = append(out, *r)
		}
	}
	return out
}

func stateGuard(state string) *seedRefusal {
	if state == utxoStateNotSeeded || state == utxoStateInvalid {
		return nil
	}
	return &seedRefusal{"state", "index state is " + state}
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

// thermalPaused returns the next paused state: pause at seedPauseC, resume at seedResumeC, pause on an unreadable sensor.
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

func freeSpaceGuard(free uint64, err error) *seedRefusal {
	if err != nil {
		return &seedRefusal{"free_space", "free space is unreadable"}
	}
	if free < seedMinFreeBytes {
		return &seedRefusal{"free_space", fmt.Sprintf("%.1f GB free, 27 GB needed", float64(free)/1e9)}
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
	in.TempC, in.TempErr = hottestThermalC(seedThermalGlob)
	in.CPUs, in.CPUErr = readCPULimit(seedCgroupRoot)
	in.Chain, in.TipTime, in.ChainErr = readSeedChain(ctx, m.rpc)
	in.RPCMethods, in.RPCErr = readActiveRPCMethods(ctx, m.rpc)
	return in
}
