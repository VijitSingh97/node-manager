package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func replayTestChain() *utxoChainParams {
	c := *utxoBitcoin
	c.Replay = true
	return &c
}

func swapReplayKnobs(t *testing.T, batch, ops int, gap int64) {
	b, o, g := utxoReplayBatch, utxoReplayBatchOps, utxoReplayGap
	utxoReplayBatch, utxoReplayBatchOps, utxoReplayGap = batch, ops, gap
	t.Cleanup(func() { utxoReplayBatch, utxoReplayBatchOps, utxoReplayGap = b, o, g })
}

func TestFollowerReplayBatchesCommitsAndBitcoinDoesNot(t *testing.T) {
	for _, tc := range []struct {
		name        string
		replay      bool
		batch, ops  int
		wantCommits int
	}{
		{"bitcoin commits every block", false, 4, 1000, 11},
		{"replay batches by block count", true, 4, 1000, 3},
		{"replay batches by op count", true, 1000, 6, 5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			swapReplayKnobs(t, tc.batch, tc.ops, utxoReplayGap)
			c := utxoMgrChain(11, "q")
			s := openFollowStore(t)
			commits := 0
			s.beforeCommit = func() error { commits++; return nil }
			f := newUtxoFollower(s, c)
			if tc.replay {
				f.chain = replayTestChain()
			}
			stepOK(t, f)
			utxoMgrCheck(t, s, c, "q", 10)
			if commits != tc.wantCommits {
				t.Fatalf("%d commits, want %d", commits, tc.wantCommits)
			}
			if st := f.Status(); st.Blocks != 11 || st.State != utxoFollowIdle {
				t.Fatalf("status %+v", st)
			}
		})
	}
}

func TestFollowerReplayKeepsBlocksAppliedBeforeAnRPCError(t *testing.T) {
	swapReplayKnobs(t, 4, 1000, utxoReplayGap)
	c := utxoMgrChain(11, "e")
	node := newFakeUtxoNode(c)
	outage := true
	node.hook = func(method string, params any) error {
		if outage && method == "getblockhash" && params.([]any)[0].(uint32) == 7 {
			return errors.New("outage")
		}
		return nil
	}
	s := openFollowStore(t)
	f := newUtxoFollower(s, node)
	f.chain = replayTestChain()
	if err := f.Step(t.Context()); err == nil || f.Status().State != utxoFollowRetry {
		t.Fatalf("err %v, status %+v", err, f.Status())
	}
	if h, _, _, _ := s.Tip(); h != 6 {
		t.Fatalf("tip %d after the outage, want the 6 blocks fetched before it", h)
	}
	outage = false
	stepOK(t, f)
	utxoMgrCheck(t, s, c, "e", 10)
}

func TestFollowerReplayReorg(t *testing.T) {
	swapReplayKnobs(t, 2, 1000, utxoReplayGap)
	c := newFakeUtxoChain()
	genesis(c)
	for _, l := range []string{"a", "b", "c", "d"} {
		c.add(l, []fakeOut{{"51" + hex.EncodeToString([]byte(l)), "1.00000000"}}, nil)
	}
	s := openFollowStore(t)
	f := newUtxoFollower(s, c)
	f.chain = replayTestChain()
	stepOK(t, f)
	c.forkAt(2)
	for _, l := range []string{"x", "y", "z", "w"} {
		c.add(l, []fakeOut{{"52" + hex.EncodeToString([]byte(l)), "1.00000000"}}, nil)
	}
	stepOK(t, f)
	if h, _, _, _ := s.Tip(); h != 5 {
		t.Fatalf("tip %d", h)
	}
	sameIndex(t, c, s, "5161", "5162", "5163", "5164", "5278", "5279", "527a", "5277")
}

type replayFixture struct {
	m     *utxoManager
	dir   string
	state string
	clock time.Time
	logs  *bytes.Buffer
}

func newReplayFixture(t *testing.T, dir string, node utxoRPC) *replayFixture {
	t.Helper()
	fx := &replayFixture{dir: dir, state: t.TempDir(), clock: time.Unix(1_800_000_000, 0), logs: &bytes.Buffer{}}
	fx.m = utxoMgrNewChain(t, dir, nil, node, fx.logs, replayTestChain())
	fx.attach(t, fx.m)
	return fx
}

func (fx *replayFixture) attach(t *testing.T, m *utxoManager) {
	m.now = func() time.Time { return fx.clock }
	m.checkEvery = time.Microsecond
	m.temp = func() (float64, error) { return 50, nil }
	m.configureSeed(utxoSeedHost{prefix: "bitcoincash-", stateDir: fx.state, lock: &sync.Mutex{},
		hold: func() bool { t.Error("a replay held the watch scan"); return false }, release: func() {}})
	m.setSeedStatePath(filepath.Join(fx.state, "bitcoincash-utxo-seed.json"))
	m.guards = func(context.Context) seedGuardInput {
		return seedGuardInput{State: m.Status().State, TempC: 50, CPUs: 0.5, FreeBytes: 1 << 40,
			Chain: blockchainInfo{Chain: "regtest", Blocks: 1, Headers: 1}, TipTime: fx.clock, Now: fx.clock}
	}
}

func TestUtxoReplaySeedsFromGenesisUnderTheSeedGuards(t *testing.T) {
	swapReplayKnobs(t, 7, 1000, utxoReplayGap)
	ctx := t.Context()
	c := utxoMgrChain(40, "r")
	node := newFakeUtxoNode(c)
	fx := newReplayFixture(t, t.TempDir(), node)
	m := fx.m
	node.hook = func(method string, _ any) error {
		if method == "getblock" && m.host.lock.TryLock() {
			m.host.lock.Unlock()
			t.Error("the replay ran without the seed lock")
		}
		return nil
	}

	m.tick(ctx)
	if st := m.Status(); st.State != utxoStateNotSeeded {
		t.Fatalf("before the replay: %+v", st)
	}
	pruned := m.guards
	m.guards = func(ctx context.Context) seedGuardInput {
		in := pruned(ctx)
		in.Chain.Pruned = true
		return in
	}
	m.maybeAutoSeed(ctx)
	if st := m.Status(); st.State != utxoStateNotSeeded || len(st.Refusals) != 1 || !strings.Contains(st.Refusals[0].Detail, "archival") {
		t.Fatalf("a pruned node was replayed: %+v", st)
	}

	m.guards = pruned
	fx.clock = fx.clock.Add(time.Hour)
	reads, pausedAt := 0, uint32(0)
	m.temp = func() (float64, error) {
		if reads++; reads == 2 {
			pausedAt = *m.Status().IndexTip
			return 90, nil
		}
		return 50, nil
	}
	commits := 0
	m.store.beforeCommit = func() error { commits++; return nil }
	m.maybeAutoSeed(ctx)
	st := m.Status()
	if st.State != utxoStateCatching || st.LastSeed == nil || st.LastSeed.Outcome != "ok" || st.AlertsSuppressedUntil != nil {
		t.Fatalf("after the replay: %+v", st)
	}
	if pausedAt == 0 || pausedAt >= 39 {
		t.Fatalf("the heat pause came at tip %d, want between batches", pausedAt)
	}
	if commits != 6 {
		t.Fatalf("%d commits for 40 blocks in batches of 7, want 6", commits)
	}
	for _, line := range []string{"seed phase=replaying", "seed phase=paused", "seed outcome=ok"} {
		if !strings.Contains(fx.logs.String(), line) {
			t.Errorf("log lacks %q:\n%s", line, fx.logs)
		}
	}
	m.tick(ctx)
	if st := m.Status(); st.State != utxoStateReady {
		t.Fatalf("after the first tick: %+v", st)
	}
	utxoMgrCheck(t, m.store, c, "r", 39)
}

func TestUtxoReplayResumesAfterARestartWithoutStartingOver(t *testing.T) {
	swapReplayKnobs(t, 5, 1000, 10)
	ctx := t.Context()
	c := utxoMgrChain(60, "s")
	node := newFakeUtxoNode(c)
	dir := t.TempDir()
	fx := newReplayFixture(t, dir, node)
	stop, cancel := context.WithCancel(ctx)
	node.hook = func(method string, params any) error {
		if method == "getblockhash" && params.([]any)[0].(uint32) == 30 {
			cancel()
		}
		return nil
	}
	fx.m.tick(ctx)
	fx.m.maybeAutoSeed(stop)
	cut := *fx.m.Status().IndexTip
	if cut == 0 || cut >= 30 {
		t.Fatalf("the stopped replay kept tip %d, want some blocks under 30", cut)
	}
	raw, _ := os.ReadFile(filepath.Join(fx.state, "bitcoincash-utxo-seed.json"))
	var persisted map[string]any
	_ = json.Unmarshal(raw, &persisted)
	persisted["seed_state"] = utxoSeedRunning
	raw, _ = json.Marshal(persisted)
	if err := os.WriteFile(filepath.Join(fx.state, "bitcoincash-utxo-seed.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := fx.m.store.Close(); err != nil {
		t.Fatal(err)
	}

	var fetched []uint32
	node.hook = func(method string, params any) error {
		if method == "getblockhash" {
			fetched = append(fetched, params.([]any)[0].(uint32))
		}
		return nil
	}
	m := utxoMgrNewChain(t, dir, nil, node, fx.logs, replayTestChain())
	fx.attach(t, m)
	if st := m.Status(); st.State != utxoStateCatching || *st.IndexTip != cut || m.seed.AutoFailures != 0 {
		t.Fatalf("after the restart: %+v, failures %d", st, m.seed.AutoFailures)
	}
	if !strings.Contains(fx.logs.String(), "resumes from its last block") {
		t.Fatalf("restart log:\n%s", fx.logs)
	}
	m.tick(ctx)
	if h, _, _, _ := m.store.Tip(); h != cut || len(fetched) > 1 {
		t.Fatalf("the follower stepped a far-behind replay chain: tip %d, fetched %v", h, fetched)
	}
	fetched = nil
	m.maybeAutoSeed(ctx)
	above := 0
	for _, h := range fetched {
		if h > 0 && h < cut {
			t.Fatalf("the resumed replay fetched height %d below its tip %d: %v", h, cut, fetched)
		}
		if h > cut {
			above++
		}
	}
	if above != int(59-cut) {
		t.Fatalf("the resumed replay fetched %v, want each of %d..59 once", fetched, cut+1)
	}
	m.tick(ctx)
	if st := m.Status(); st.State != utxoStateReady {
		t.Fatalf("after the resumed replay: %+v", st)
	}
	utxoMgrCheck(t, m.store, c, "s", 59)
}

func TestUtxoReplayIntegrityErrorInvalidatesTheIndex(t *testing.T) {
	c := utxoMgrChain(6, "i")
	c.add("i", nil, []fakeIn{{fakeDisplayHash("nothing", 1), 0, "5152"}})
	fx := newReplayFixture(t, t.TempDir(), newFakeUtxoNode(c))
	fx.m.tick(t.Context())
	fx.m.maybeAutoSeed(t.Context())
	st := fx.m.Status()
	if st.State != utxoStateInvalid || st.LastSeed == nil || st.LastSeed.Outcome != utxoOutcomeFailed {
		t.Fatalf("after a replay hit a missing coin: %+v", st)
	}
	fx.clock = fx.clock.Add(time.Hour)
	fx.m.maybeAutoSeed(t.Context())
	if fx.m.Status().LastSeed.FinishedAt != st.LastSeed.FinishedAt {
		t.Fatal("an invalid index replayed again without the user")
	}
}

func swapReplayRetries(t *testing.T, retries int, wait time.Duration) {
	r, w := utxoReplayRetries, utxoReplayRetryWait
	utxoReplayRetries, utxoReplayRetryWait = retries, wait
	t.Cleanup(func() { utxoReplayRetries, utxoReplayRetryWait = r, w })
}

func TestUtxoReplayRetriesTransientErrorsWithinTheSeed(t *testing.T) {
	swapReplayKnobs(t, 5, 1000, 10)
	swapReplayRetries(t, 3, time.Millisecond)
	ctx := t.Context()
	c := utxoMgrChain(40, "t")
	node := newFakeUtxoNode(c)
	fx := newReplayFixture(t, t.TempDir(), node)
	failures := 0
	node.hook = func(method string, params any) error {
		if failures < 3 && (method == "getblockchaininfo" || method == "getblockhash" && params.([]any)[0].(uint32) == uint32(20+failures)) {
			failures++
			return errors.New("connection refused")
		}
		return nil
	}
	fx.m.tick(ctx)
	fx.m.maybeAutoSeed(ctx)
	if st := fx.m.Status(); failures != 3 || st.LastSeed == nil || st.LastSeed.Outcome != "ok" {
		t.Fatalf("three node outages within the retry budget failed the replay: %+v after %d failures", st, failures)
	}
	fx.m.tick(ctx)
	utxoMgrCheck(t, fx.m.store, c, "t", 39)
}

func TestUtxoReplayRetriesHourlyThroughNodeOutagesWithoutBackingOff(t *testing.T) {
	swapReplayKnobs(t, 5, 1000, 10)
	swapReplayRetries(t, 0, time.Millisecond)
	ctx := t.Context()
	c := utxoMgrChain(40, "o")
	node := newFakeUtxoNode(c)
	fx := newReplayFixture(t, t.TempDir(), node)
	m := fx.m
	outages := 0
	node.hook = func(method string, params any) error {
		if outages < utxoAutoSeedTries+1 && method == "getblockhash" && params.([]any)[0].(uint32) == uint32(20+outages) {
			outages++
			return errors.New("connection refused")
		}
		return nil
	}
	m.tick(ctx)
	for attempt := 1; attempt <= utxoAutoSeedTries+1; attempt++ {
		m.maybeAutoSeed(ctx)
		if st := m.Status(); st.LastSeed == nil || st.LastSeed.Outcome != utxoOutcomeFailed || st.State != utxoStateCatching || m.seed.AutoFailures != 0 {
			t.Fatalf("attempt %d: %+v, failures %d", attempt, st, m.seed.AutoFailures)
		}
		m.tick(ctx)
		fx.clock = fx.clock.Add(time.Hour)
	}
	m.maybeAutoSeed(ctx)
	if st := m.Status(); st.LastSeed.Outcome != "ok" {
		t.Fatalf("the replay did not finish after the outages: %+v", st)
	}
	m.tick(ctx)
	utxoMgrCheck(t, m.store, c, "o", 39)
}

func TestFollowerReplayBoundsBlocksFetchedAheadByCoinOperations(t *testing.T) {
	swapReplayKnobs(t, 1000, 8, utxoReplayGap)
	budget := utxoReplayInflightOps
	utxoReplayInflightOps = 8
	t.Cleanup(func() { utxoReplayInflightOps = budget })
	c := utxoMgrChain(40, "b")
	node := newFakeUtxoNode(c)
	s := openFollowStore(t)
	s.beforeCommit = func() error { time.Sleep(2 * time.Millisecond); return nil }
	var committed atomic.Int64
	committed.Store(-1)
	ahead := 0
	node.hook = func(method string, params any) error {
		if method == "getblockhash" {
			ahead = max(ahead, int(int64(params.([]any)[0].(uint32))-committed.Load()))
		}
		return nil
	}
	f := newUtxoFollower(s, node)
	f.chain = replayTestChain()
	f.onBlock = func(h uint32) { committed.Store(int64(h)) }
	stepOK(t, f)
	utxoMgrCheck(t, s, c, "b", 39)
	// Queued blocks within 8 operations, an open batch and one being committed, and the fetcher's own; without the budget it runs over 30 ahead.
	if ahead > 10 {
		t.Fatalf("the fetcher ran %d blocks past the last commit with 8 coin operations of queue budget (4 per block)", ahead)
	}
}

func TestFollowerReplayBoundsBlocksFetchedAheadByScriptBytes(t *testing.T) {
	swapReplayKnobs(t, 1000, 1000, utxoReplayGap)
	budget := utxoReplayInflightOps
	utxoReplayInflightOps = 16
	t.Cleanup(func() { utxoReplayInflightOps = budget })
	c := newFakeUtxoChain()
	genesis(c)
	for i := range 40 {
		c.add(fmt.Sprintf("pad%d", i), []fakeOut{{fmt.Sprintf("%02x", i) + strings.Repeat("ab", 8<<10), "0.00010000"}}, nil)
	}
	node := newFakeUtxoNode(c)
	s := openFollowStore(t)
	s.beforeCommit = func() error { time.Sleep(2 * time.Millisecond); return nil }
	var committed atomic.Int64
	committed.Store(-1)
	ahead := 0
	node.hook = func(method string, params any) error {
		if method == "getblockhash" {
			ahead = max(ahead, int(int64(params.([]any)[0].(uint32))-committed.Load()))
		}
		return nil
	}
	f := newUtxoFollower(s, node)
	f.chain = replayTestChain()
	f.onBlock = func(h uint32) { committed.Store(int64(h)) }
	stepOK(t, f)
	// One output per block but 8 KiB of script, so 16 of budget holds about two blocks; counting outputs alone it runs over 15 ahead.
	if ahead > 6 {
		t.Fatalf("the fetcher ran %d blocks past the last commit on blocks of one 8 KiB output each", ahead)
	}
	if got := lookupAmounts(t, s, "27"+strings.Repeat("ab", 8<<10)); len(got) != 1 {
		t.Fatalf("the last padded output is not indexed: %v", got)
	}
}

func TestFollowerReplayPassesABlockLargerThanTheBudgetWhileABatchIsOpen(t *testing.T) {
	swapReplayKnobs(t, 1000, 1000, utxoReplayGap)
	budget := utxoReplayInflightOps
	utxoReplayInflightOps = 8
	t.Cleanup(func() { utxoReplayInflightOps = budget })
	c := utxoMgrChain(10, "g")
	var outs []fakeOut
	for i := range 30 {
		outs = append(outs, fakeOut{fmt.Sprintf("0014%02xccdd", i), "0.00010000"})
	}
	c.add("big", outs, nil)
	s := openFollowStore(t)
	f := newUtxoFollower(s, c)
	f.chain = replayTestChain()
	done := make(chan error, 1)
	go func() { done <- f.Step(context.Background()) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("a block larger than the queue budget stalled the replay")
	}
	if h, _, _, _ := s.Tip(); h != 10 || len(lookupAmounts(t, s, "001400ccdd")) != 1 {
		t.Fatalf("tip %d after the large block", h)
	}
}

func TestAppendReversedUtxoOpsMatchesASplitAndReverse(t *testing.T) {
	rng := rand.New(rand.NewPCG(7, 9))
	for range 50 {
		var ops []byte
		for range rng.IntN(40) {
			key := make([]byte, utxoKeyLen)
			for i := range key {
				key[i] = byte(rng.IntN(256))
			}
			if rng.IntN(2) == 0 {
				ops = appendUtxoDelete(ops, key)
			} else {
				ops = appendUtxoSet(ops, key, make([]byte, utxoValueLen))
			}
		}
		parts, err := splitUtxoOps(ops)
		if err != nil {
			t.Fatal(err)
		}
		var want []byte
		for i := len(parts) - 1; i >= 0; i-- {
			want = append(want, parts[i]...)
		}
		if got := appendReversedUtxoOps([]byte("hdr"), ops); !bytes.Equal(got, append([]byte("hdr"), want...)) {
			t.Fatalf("reversed ops differ for %d parts", len(parts))
		}
	}
}

func TestFollowerReplayNeverHoldsTwoOverBudgetBlocks(t *testing.T) {
	swapReplayKnobs(t, 1000, 1000, utxoReplayGap)
	budget := utxoReplayInflightOps
	utxoReplayInflightOps = 8
	t.Cleanup(func() { utxoReplayInflightOps = budget })
	c := utxoMgrChain(10, "h")
	for b := range 4 {
		var outs []fakeOut
		for i := range 30 {
			outs = append(outs, fakeOut{fmt.Sprintf("0014%02x%02xee", b, i), "0.00010000"})
		}
		c.add(fmt.Sprintf("big%d", b), outs, nil)
	}
	node := newFakeUtxoNode(c)
	s := openFollowStore(t)
	s.beforeCommit = func() error { time.Sleep(20 * time.Millisecond); return nil }
	var committed atomic.Int64
	committed.Store(-1)
	ahead := 0
	node.hook = func(method string, params any) error {
		if method == "getblockhash" {
			if h := params.([]any)[0].(uint32); h > 10 {
				ahead = max(ahead, int(int64(h)-committed.Load()))
			}
		}
		return nil
	}
	f := newUtxoFollower(s, node)
	f.chain = replayTestChain()
	f.onBlock = func(h uint32) { committed.Store(int64(h)) }
	stepOK(t, f)
	if tip, _, _, _ := s.Tip(); tip != 13 || ahead != 1 {
		t.Fatalf("tip %d; the fetcher was %d blocks past the last commit while fetching over-budget blocks, want 1", tip, ahead)
	}
}

func TestUtxoReplayRetriesResetWhenTheReplayMakesProgress(t *testing.T) {
	swapReplayKnobs(t, 5, 1000, 10)
	swapReplayRetries(t, 3, time.Millisecond)
	ctx := t.Context()
	c := utxoMgrChain(80, "p")
	node := newFakeUtxoNode(c)
	fx := newReplayFixture(t, t.TempDir(), node)
	failed := map[uint32]bool{}
	node.hook = func(method string, params any) error {
		if method == "getblockhash" {
			if h := params.([]any)[0].(uint32); h > 0 && h%10 == 0 && !failed[h] {
				failed[h] = true
				return errors.New("connection refused")
			}
		}
		return nil
	}
	fx.m.tick(ctx)
	fx.m.maybeAutoSeed(ctx)
	if st := fx.m.Status(); len(failed) != 7 || st.LastSeed == nil || st.LastSeed.Outcome != "ok" {
		t.Fatalf("%d outages with progress between them, more than the 3 retries, stopped the replay: %+v", len(failed), st)
	}
	if !strings.Contains(fx.logs.String(), "seed phase=retrying") {
		t.Fatalf("the retry wait never showed in the seed phase:\n%s", fx.logs)
	}
	fx.m.tick(ctx)
	utxoMgrCheck(t, fx.m.store, c, "p", 79)
}

func TestUtxoReplayStopsAfterConsecutiveStalls(t *testing.T) {
	swapReplayRetries(t, 3, time.Millisecond)
	ctx := t.Context()
	node := newFakeUtxoNode(utxoMgrChain(20, "q"))
	fx := newReplayFixture(t, t.TempDir(), node)
	fx.m.tick(ctx)
	attempts := 0
	node.hook = func(method string, params any) error {
		if method == "getblockhash" && params.([]any)[0].(uint32) == 0 {
			attempts++
			return errors.New("connection refused")
		}
		return nil
	}
	fx.m.maybeAutoSeed(ctx)
	if st := fx.m.Status(); attempts != 4 || st.LastSeed == nil || st.LastSeed.Outcome != utxoOutcomeFailed || fx.m.seed.AutoFailures != 0 {
		t.Fatalf("after %d stalled attempts: %+v, failures %d", attempts, st, fx.m.seed.AutoFailures)
	}
}

func TestUtxoReplayStorageErrorsFailWithoutRetrying(t *testing.T) {
	swapReplayRetries(t, 30, time.Second)
	ctx := t.Context()
	node := newFakeUtxoNode(utxoMgrChain(20, "r"))
	fx := newReplayFixture(t, t.TempDir(), node)
	fx.m.tick(ctx)
	attempts := 0
	node.hook = func(method string, params any) error {
		if method == "getblockchaininfo" {
			if attempts++; attempts == 1 {
				_ = fx.m.currentStore().MarkInvalid("the disk went away")
			}
		}
		return nil
	}
	start := time.Now()
	fx.m.maybeAutoSeed(ctx)
	if st := fx.m.Status(); attempts != 1 || time.Since(start) > 5*time.Second || st.State != utxoStateInvalid || st.LastSeed == nil || st.LastSeed.Outcome != utxoOutcomeFailed {
		t.Fatalf("a storage error was retried: %d attempts in %s, %+v", attempts, time.Since(start), st)
	}
}
