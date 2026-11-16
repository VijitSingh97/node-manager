package main

import (
	"context"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSeedThermalStartGuard(t *testing.T) {
	cases := []struct {
		c      float64
		err    error
		refuse bool
	}{
		{60, nil, false},
		{75, nil, false},
		{75.1, nil, true},
		{90, nil, true},
		{0, errors.New("unreadable"), true},
	}
	for _, tc := range cases {
		if got := thermalStartGuard(tc.c, tc.err) != nil; got != tc.refuse {
			t.Errorf("start guard %v %v: refused=%v", tc.c, tc.err, got)
		}
	}
}

func TestSeedThermalHysteresis(t *testing.T) {
	paused := false
	steps := []struct {
		c    float64
		err  error
		want bool
	}{
		{80, nil, false},
		{84.9, nil, false},
		{85, nil, true},
		{80, nil, true},
		{72.1, nil, true},
		{72, nil, false},
		{80, nil, false},
		{0, errors.New("unreadable"), true},
		{73, nil, true},
		{72, nil, false},
	}
	for i, st := range steps {
		paused = thermalPaused(paused, st.c, st.err)
		if paused != st.want {
			t.Fatalf("step %d (%v %v): paused=%v, want %v", i, st.c, st.err, paused, st.want)
		}
	}
}

func TestHottestThermalC(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.MkdirAll(filepath.Join(dir, name), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name, "temp"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	glob := filepath.Join(dir, "thermal_zone*", "temp")
	if _, err := hottestThermalC(glob); err == nil {
		t.Fatal("no zones must be an error")
	}
	write("thermal_zone0", "51000\n")
	write("thermal_zone1", "78500\n")
	write("thermal_zone2", "40000\n")
	if got, err := hottestThermalC(glob); err != nil || got != 78.5 {
		t.Fatalf("hottest = %v %v", got, err)
	}
	write("thermal_zone3", "not a number")
	if _, err := hottestThermalC(glob); err == nil {
		t.Fatal("an unparseable zone must be an error")
	}
	write("thermal_zone3", "")
	if _, err := hottestThermalC(glob); err == nil {
		t.Fatal("an empty zone must be an error")
	}
	if err := os.Remove(filepath.Join(dir, "thermal_zone3", "temp")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "thermal_zone3", "temp"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := hottestThermalC(glob); err == nil {
		t.Fatal("an unreadable zone must be an error")
	}
}

func TestCgroupCPUParsers(t *testing.T) {
	v2 := []struct {
		in   string
		want float64
		bad  bool
	}{
		{"50000 100000\n", 0.5, false},
		{"25000 100000", 0.25, false},
		{"max 100000", math.Inf(1), false},
		{"200000 100000", 2, false},
		{"", 0, true},
		{"50000", 0, true},
		{"abc 100000", 0, true},
		{"50000 0", 0, true},
		{"0 100000", 0, true},
	}
	for _, tc := range v2 {
		got, err := parseCgroupV2CPUMax(tc.in)
		if (err != nil) != tc.bad || err == nil && got != tc.want {
			t.Errorf("v2 %q = %v %v", tc.in, got, err)
		}
	}
	if got, err := parseCgroupV1CPU("50000\n", "100000\n"); err != nil || got != 0.5 {
		t.Errorf("v1 = %v %v", got, err)
	}
	if got, err := parseCgroupV1CPU("-1", "100000"); err != nil || !math.IsInf(got, 1) {
		t.Errorf("v1 unlimited = %v %v", got, err)
	}
	if _, err := parseCgroupV1CPU("x", "100000"); err == nil {
		t.Error("v1 malformed quota accepted")
	}
}

func TestReadCPULimitPrefersV2ThenV1(t *testing.T) {
	root := t.TempDir()
	if _, err := readCPULimit(root); err == nil {
		t.Fatal("no cgroup files must be an error")
	}
	v1 := filepath.Join(root, "cpu")
	if err := os.MkdirAll(v1, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(v1, "cpu.cfs_quota_us"), []byte("50000\n"), 0o644)
	if _, err := readCPULimit(root); err == nil {
		t.Fatal("a missing period must be an error")
	}
	os.WriteFile(filepath.Join(v1, "cpu.cfs_period_us"), []byte("100000\n"), 0o644)
	if got, err := readCPULimit(root); err != nil || got != 0.5 {
		t.Fatalf("v1 = %v %v", got, err)
	}
	os.WriteFile(filepath.Join(root, "cpu.max"), []byte("100000 100000\n"), 0o644)
	if got, err := readCPULimit(root); err != nil || got != 1 {
		t.Fatalf("v2 must win, got %v %v", got, err)
	}
	os.WriteFile(filepath.Join(root, "cpu.max"), []byte("garbage"), 0o644)
	if _, err := readCPULimit(root); err == nil {
		t.Fatal("a malformed cpu.max must refuse, not fall back to v1")
	}
}

func TestSeedSimpleGuards(t *testing.T) {
	if cpuGuard(0.5, nil) != nil || cpuGuard(0.25, nil) != nil {
		t.Error("cpu at or under 0.5 refused")
	}
	if cpuGuard(0.51, nil) == nil || cpuGuard(math.Inf(1), nil) == nil || cpuGuard(0, errors.New("x")) == nil {
		t.Error("cpu over the cap or unreadable passed")
	}
	if freeSpaceGuard(seedMinFreeBytes, nil) != nil || freeSpaceGuard(seedMinFreeBytes-1, nil) == nil || freeSpaceGuard(seedMinFreeBytes, errors.New("x")) == nil {
		t.Error("free space boundary")
	}
	for state, refused := range map[string]bool{
		utxoStateNotSeeded: false, utxoStateInvalid: false, utxoStateSeeding: true,
		utxoStateCatching: true, utxoStateReady: true, utxoStateDegraded: true, utxoStateOff: true,
	} {
		if (stateGuard(state) != nil) != refused {
			t.Errorf("state %s refused=%v", state, !refused)
		}
	}
	if rpcIdleGuard(nil, nil) != nil || rpcIdleGuard([]string{"getrpcinfo"}, nil) != nil {
		t.Error("idle rpc refused")
	}
	for _, busy := range []string{"dumptxoutset", "scantxoutset"} {
		if rpcIdleGuard([]string{"getrpcinfo", busy}, nil) == nil {
			t.Errorf("%s did not refuse", busy)
		}
	}
	if rpcIdleGuard(nil, errors.New("x")) == nil {
		t.Error("an unreadable rpc list must refuse")
	}
}

func TestSeedNodeGuard(t *testing.T) {
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	good := blockchainInfo{Blocks: 900, Headers: 900}
	fresh := now.Add(-time.Hour)
	if nodeGuard(good, fresh, now, nil) != nil {
		t.Fatal("healthy node refused")
	}
	if nodeGuard(good, now.Add(-seedMaxTipAge), now, nil) != nil {
		t.Fatal("tip exactly 2 h old refused")
	}
	bad := []struct {
		name string
		info blockchainInfo
		tip  time.Time
		err  error
	}{
		{"ibd", blockchainInfo{Blocks: 900, Headers: 900, InitialBlockDownload: true}, fresh, nil},
		{"headers ahead", blockchainInfo{Blocks: 900, Headers: 901}, fresh, nil},
		{"stale tip", good, now.Add(-seedMaxTipAge - time.Second), nil},
		{"no tip time", good, time.Time{}, nil},
		{"rpc error", good, fresh, errors.New("down")},
	}
	for _, tc := range bad {
		if r := nodeGuard(tc.info, tc.tip, now, tc.err); r == nil || r.Guard != "node" {
			t.Errorf("%s was not refused", tc.name)
		}
	}
}

func TestEvaluateSeedGuardsListsEveryFailure(t *testing.T) {
	now := time.Now()
	ok := seedGuardInput{State: utxoStateNotSeeded, TempC: 60, CPUs: 0.5, FreeBytes: seedMinFreeBytes,
		Chain: blockchainInfo{Blocks: 5, Headers: 5}, TipTime: now, Now: now}
	if got := evaluateSeedGuards(ok); len(got) != 0 {
		t.Fatalf("all guards pass, got %v", got)
	}
	bad := ok
	bad.State = utxoStateReady
	bad.TempErr = errors.New("x")
	bad.CPUs = 4
	bad.FreeBytes = 1
	bad.ChainErr = errors.New("x")
	bad.RPCMethods = []string{"dumptxoutset"}
	got := evaluateSeedGuards(bad)
	var names []string
	for _, r := range got {
		if r.Detail == "" {
			t.Errorf("refusal %s has no detail", r.Guard)
		}
		names = append(names, r.Guard)
	}
	if want := "state thermal cpu_cap free_space node rpc_idle"; strings.Join(names, " ") != want {
		t.Fatalf("refusals = %v, want %s", names, want)
	}
}

type seedGuardRPC struct{ methods []string }

func (r seedGuardRPC) call(_ context.Context, method string, _ any, out any) error {
	switch method {
	case "getblockchaininfo":
		*out.(*blockchainInfo) = blockchainInfo{Blocks: 7, Headers: 7, BestBlockHash: "aa"}
	case "getblockheader":
		out.(*struct {
			Time int64 `json:"time"`
		}).Time = 1234
	case "getrpcinfo":
		info := out.(*struct {
			Active []struct {
				Method string `json:"method"`
			} `json:"active_commands"`
		})
		for _, m := range r.methods {
			info.Active = append(info.Active, struct {
				Method string `json:"method"`
			}{m})
		}
	}
	return nil
}

func TestSeedGuardDataSources(t *testing.T) {
	rpc := seedGuardRPC{methods: []string{"getrpcinfo", "dumptxoutset"}}
	info, tip, err := readSeedChain(t.Context(), rpc)
	if err != nil || info.Blocks != 7 || tip.Unix() != 1234 {
		t.Fatalf("chain %+v %v %v", info, tip, err)
	}
	methods, err := readActiveRPCMethods(t.Context(), rpc)
	if err != nil || strings.Join(methods, ",") != "getrpcinfo,dumptxoutset" {
		t.Fatalf("methods %v %v", methods, err)
	}
	m := utxoMgrNew(t, t.TempDir(), nil, rpc, io.Discard)
	in := m.gatherSeedGuards(t.Context())
	if in.State != utxoStateNotSeeded || in.Chain.Blocks != 7 || len(in.RPCMethods) != 2 || in.FreeBytes == 0 {
		t.Fatalf("gathered %+v", in)
	}
}

func TestPrunedScannerHold(t *testing.T) {
	mock := &prunedRPCMock{bestBlock: strings.Repeat("e", 64), result: validPrunedScan(strings.Repeat("e", 64))}
	a, _, _ := newPrunedTestApp(t, t.TempDir(), mock)
	entry, err := a.watch.Add("One", genesisAddress, "")
	if err != nil {
		t.Fatal(err)
	}
	s := a.scanner

	s.mu.Lock()
	s.running = true
	s.mu.Unlock()
	if s.hold() {
		t.Fatal("hold succeeded while a scan was running")
	}
	s.mu.Lock()
	s.running = false
	s.queued["x"] = true
	s.mu.Unlock()
	if s.hold() {
		t.Fatal("hold succeeded while a scan was queued")
	}
	s.mu.Lock()
	delete(s.queued, "x")
	s.mu.Unlock()

	if !s.hold() || s.hold() {
		t.Fatal("hold must succeed once")
	}
	if s.enqueue(entry, true) || s.enqueue(entry, false) {
		t.Fatal("a scan was enqueued while held")
	}
	if !s.applyBlocks([]WatchEntry{entry}, "base", blockchainInfo{Blocks: 5}) {
		t.Fatal("applyBlocks must report nothing to do while held")
	}
	mock.mu.Lock()
	calls := mock.allCalls
	mock.mu.Unlock()
	if calls != 0 {
		t.Fatalf("rpc calls while held: %d", calls)
	}
	s.mu.Lock()
	s.failures[watchKeyID(entry.Key)] = 3
	s.lastTry[watchKeyID(entry.Key)] = s.now()
	s.mu.Unlock()
	s.release()
	if s.enqueue(entry, false) {
		t.Fatal("retry backoff did not apply after release")
	}
	if !s.enqueue(entry, true) {
		t.Fatal("a forced scan was refused after release")
	}
	waitPrunedScannerIdle(t, s)
}

func TestMonitorTransitioningComposesAlertWindowForBitcoinOnly(t *testing.T) {
	a := &app{cfg: config{stateDir: t.TempDir()}}
	if a.monitorTransitioning(nodeBitcoin) {
		t.Fatal("transitioning without a manager")
	}
	m := utxoMgrNew(t, t.TempDir(), nil, newFakeUtxoNode(utxoMgrChain(2, "b")), io.Discard)
	a.utxo = m
	if a.monitorTransitioning(nodeBitcoin) {
		t.Fatal("transitioning without a deadline")
	}
	m.suppressAlerts(m.now().Add(time.Minute))
	if !a.monitorTransitioning(nodeBitcoin) {
		t.Fatal("bitcoin alerts not suppressed")
	}
	if a.monitorTransitioning(nodeMonero) {
		t.Fatal("the alert window leaked to another node")
	}
	if a.nodeTransitioning(nodeBitcoin) {
		t.Fatal("the alert window must not pause bitcoin workers")
	}
}

func TestIngestBaseHeightErrorLeavesStoreEmpty(t *testing.T) {
	dir := t.TempDir()
	s := utxoTestOpen(t, dir)
	f, err := os.Open("testdata/utxo/all_types.dat")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var seen [32]byte
	mismatch := errors.New("dump base hash does not match the node")
	_, err = ingestUtxoSnapshot(s, f, utxoBitcoin, utxoNetRegtest, func(h [32]byte) (uint32, error) { seen = h; return 0, mismatch }, nil)
	if !errors.Is(err, mismatch) {
		t.Fatalf("err = %v", err)
	}
	if seen == ([32]byte{}) {
		t.Fatal("baseHeight did not receive the dump header hash")
	}
	if err := s.RequireEmpty(); err != nil {
		t.Fatalf("store not empty after a rejected hash: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, utxoIngestTmpDir)); !os.IsNotExist(err) {
		t.Fatalf("temp dir left behind: %v", err)
	}
}

func TestIngestBaseHeightRunsAfterMerge(t *testing.T) {
	s := utxoTestOpen(t, t.TempDir())
	prog := &utxoIngestProgress{}
	f, _ := os.Open("testdata/utxo/all_types.dat")
	defer f.Close()
	hdr, err := ingestUtxoSnapshot(s, f, utxoBitcoin, utxoNetRegtest, func([32]byte) (uint32, error) {
		if prog.Written.Load() != prog.Total.Load() || prog.Total.Load() == 0 {
			t.Errorf("baseHeight called before the merge finished: %d of %d", prog.Written.Load(), prog.Total.Load())
		}
		return 103, nil
	}, prog)
	if err != nil {
		t.Fatal(err)
	}
	if h, hash, ok, err := s.Tip(); err != nil || !ok || h != 103 || hash != hdr.BaseHash {
		t.Fatalf("tip = %d %v %v", h, ok, err)
	}
}

func TestIngestCheckpointCancelDiscardsPartialIndex(t *testing.T) {
	utxoTestSmallRuns(t, 1000, 20<<10)
	for _, stage := range []string{"parse", "merge"} {
		t.Run(stage, func(t *testing.T) {
			dir := t.TempDir()
			s := utxoTestOpen(t, dir)
			prog := &utxoIngestProgress{}
			calls := 0
			prog.Checkpoint = func() error {
				calls++
				if stage == "parse" && calls == 2 || stage == "merge" && prog.Parsed.Load() == 20000 {
					return context.Canceled
				}
				return nil
			}
			dump := utxoTestRandomDump(20000)
			_, err := ingestUtxoSnapshot(s, dump, utxoBitcoin, utxoNetRegtest, utxoTestHeight(1), prog)
			dump.(io.Closer).Close()
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("err = %v after %d checkpoints", err, calls)
			}
			if stage == "parse" && prog.Parsed.Load() != 2*utxoIngestCheckpointEvery {
				t.Fatalf("parsed %d records, want checkpoints every %d", prog.Parsed.Load(), utxoIngestCheckpointEvery)
			}
			if err := s.RequireEmpty(); err != nil {
				t.Fatalf("partial index survived: %v", err)
			}
			if _, err := os.Stat(filepath.Join(dir, utxoIngestTmpDir)); !os.IsNotExist(err) {
				t.Fatalf("temp dir left behind: %v", err)
			}
		})
	}
}

func TestGatherSeedGuardsUnreadableFreeSpace(t *testing.T) {
	m := utxoMgrNew(t, t.TempDir(), nil, newFakeUtxoNode(utxoMgrChain(5, "b")), io.Discard)
	m.dir = filepath.Join(t.TempDir(), "missing")
	in := m.gatherSeedGuards(t.Context())
	if in.FreeErr == nil || freeSpaceGuard(in.FreeBytes, in.FreeErr) == nil {
		t.Fatalf("unreadable free space must refuse: %+v", in)
	}
}
