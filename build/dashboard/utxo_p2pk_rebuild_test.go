package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

type rebuildFixture struct {
	fx    *replayFixture
	m     *utxoManager
	chain *fakeUtxoChain
	node  *fakeUtxoNode
	dir   string
	pkh   []byte
}

func rebuildChain(t *testing.T) *fakeUtxoChain {
	t.Helper()
	pkh := hex.EncodeToString(p2pkAddr(t, p2pkAddrGCompressed))
	p2pk := hex.EncodeToString(p2pkScript(t, p2pkKeyGCompressed))
	c := utxoMgrChain(10, "l")
	c.add("p", []fakeOut{{pkh, "1.00000000"}, {p2pk, "2.00000000"}}, nil)
	return c
}

func newRebuildFixture(t *testing.T) *rebuildFixture {
	t.Helper()
	c := rebuildChain(t)
	node := newFakeUtxoNode(c)
	dir := t.TempDir()
	legacy := p2pkOpenLegacy(t, dir, false)
	if err := newUtxoFollower(legacy, node).Step(t.Context()); err != nil {
		t.Fatal(err)
	}
	_ = legacy.Close()
	fx := newReplayFixture(t, dir, node)
	fx.m.tick(t.Context())
	r := &rebuildFixture{fx: fx, m: fx.m, chain: c, node: node, dir: dir, pkh: p2pkAddr(t, p2pkAddrGCompressed)}
	if st := r.m.Status(); st.State != utxoStateReady || !st.P2PKMissing {
		t.Fatalf("the legacy fixture reads %+v", st)
	}
	return r
}

func (r *rebuildFixture) amounts(t *testing.T) []uint64 {
	t.Helper()
	coins, _, _, ok := r.m.Lookup([][]byte{r.pkh})
	if !ok {
		t.Fatal("the index does not serve")
	}
	var out []uint64
	for _, c := range coins {
		out = append(out, c.Amount)
	}
	slices.Sort(out)
	return out
}

func rebuildEntries(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func hasPrefixEntry(names []string, prefix string) bool {
	return slices.ContainsFunc(names, func(n string) bool { return strings.HasPrefix(n, prefix) })
}

func TestLegacyRebuildSwitchesOnlyAfterTheSeedSucceeds(t *testing.T) {
	r := newRebuildFixture(t)
	m := r.m
	if got := r.amounts(t); !slices.Equal(got, []uint64{100_000_000}) {
		t.Fatalf("the legacy index counts %v before the rebuild", got)
	}
	old := m.store
	var during string
	err := m.execSeed(t.Context(), func(ctx context.Context) error {
		st := m.Status()
		if st.State != utxoStateReady || !st.Rebuilding || !st.P2PKMissing {
			t.Errorf("during the rebuild the status is %+v", st)
		}
		if got := r.amounts(t); !slices.Equal(got, []uint64{100_000_000}) {
			t.Errorf("the legacy index stopped serving during the rebuild: %v", got)
		}
		during = strings.Join(rebuildEntries(t, r.dir), ",")
		return m.seedFromReplay(ctx)
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(during, utxoRebuildPrefix) {
		t.Fatalf("the rebuild had no directory of its own: %s", during)
	}
	old.mu.RLock()
	closed := old.db == nil
	old.mu.RUnlock()
	if !closed {
		t.Fatal("the replaced index was left open")
	}
	m.tick(t.Context())
	st := m.Status()
	if st.P2PKMissing || st.Rebuilding || st.State != utxoStateReady || !m.store.canon {
		t.Fatalf("after the switch %+v canonical=%v", st, m.store.canon)
	}
	if got := r.amounts(t); !slices.Equal(got, []uint64{100_000_000, 200_000_000}) {
		t.Fatalf("the rebuilt index counts %v", got)
	}
	names := rebuildEntries(t, r.dir)
	if len(names) != 2 || !slices.Contains(names, utxoActiveName) || !hasPrefixEntry(names, utxoRebuildPrefix) {
		t.Fatalf("the old files were not deleted after the switch: %v", names)
	}

	_ = m.store.Close()
	again := utxoMgrNewChain(t, r.dir, nil, r.node, io.Discard, replayTestChain())
	again.tick(t.Context())
	if st := again.Status(); st.P2PKMissing || st.State != utxoStateReady {
		t.Fatalf("after a restart %+v", st)
	}
	coins, _, _, ok := again.Lookup([][]byte{r.pkh})
	if !ok || len(coins) != 2 {
		t.Fatalf("after a restart the lookup returned %d coins", len(coins))
	}
}

func TestLegacyRebuildFailureLeavesTheLegacyIndexServingAndFollowing(t *testing.T) {
	for _, tc := range []struct {
		name string
		fail error
	}{{"seed failure", errSeedChainstate}, {"cancelled", context.Canceled}, {"thermal", errSeedThermal}, {"low disk", errSeedLowDisk}, {"dump", errSeedDump}} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRebuildFixture(t)
			m := r.m
			err := m.execSeed(t.Context(), func(ctx context.Context) error {
				r.chain.add("late", []fakeOut{{"51", "1.00000000"}}, nil)
				select {
				case m.wake <- struct{}{}:
				default:
				}
				deadline := time.Now().Add(10 * time.Second)
				for {
					if h, _, _, _ := m.store.Tip(); h == 11 {
						break
					}
					if time.Now().After(deadline) {
						t.Error("the legacy index did not follow a block while its replacement was building")
						break
					}
					time.Sleep(5 * time.Millisecond)
				}
				if got := r.amounts(t); !slices.Equal(got, []uint64{100_000_000}) {
					t.Errorf("the legacy index stopped serving during the rebuild: %v", got)
				}
				return tc.fail
			})
			if !errors.Is(err, tc.fail) {
				t.Fatalf("seed returned %v", err)
			}
			st := m.Status()
			if st.State != utxoStateReady || !st.P2PKMissing || st.Rebuilding || !m.store.Legacy() {
				t.Fatalf("after the failure %+v", st)
			}
			if got := r.amounts(t); !slices.Equal(got, []uint64{100_000_000}) {
				t.Fatalf("the legacy index counts %v after the failure", got)
			}
			names := rebuildEntries(t, r.dir)
			if hasPrefixEntry(names, utxoRebuildPrefix) || slices.Contains(names, utxoActiveName) {
				t.Fatalf("a failed rebuild left %v", names)
			}
			if st.LastSeed == nil || st.LastSeed.Outcome == "ok" {
				t.Fatalf("the failure was not recorded: %+v", st.LastSeed)
			}
		})
	}
}

func TestLegacyReseedStillWipesAnInvalidIndex(t *testing.T) {
	r := newRebuildFixture(t)
	r.m.invalidate("storage error")
	if err := r.m.startSeed(); err != nil {
		t.Fatal(err)
	}
	if r.m.build != nil || r.m.Status().State != utxoStateSeeding || r.m.Status().Rebuilding || !r.m.store.canon || r.m.store.Legacy() {
		t.Fatalf("an invalid index must be wiped in place: %+v", r.m.Status())
	}
	if hasPrefixEntry(rebuildEntries(t, r.dir), utxoRebuildPrefix) {
		t.Fatal("an invalid index was rebuilt beside")
	}
}

func TestSeedableStateMatchesWhatStartSeedAllows(t *testing.T) {
	for _, tc := range []struct {
		state          string
		replay, legacy bool
		want           bool
	}{
		{utxoStateNotSeeded, false, false, true},
		{utxoStateInvalid, false, false, true},
		{utxoStateReady, false, false, false},
		{utxoStateReady, false, true, true},
		{utxoStateCatching, false, true, true},
		{utxoStateCatching, false, false, false},
		{utxoStateCatching, true, false, true},
		{utxoStateCatching, true, true, true},
		{utxoStateDegraded, false, true, false},
		{utxoStateDegraded, true, true, false},
		{utxoStateSeeding, false, true, false},
	} {
		if got := seedableState(tc.state, tc.replay, tc.legacy); got != tc.want {
			t.Errorf("seedableState(%s, replay=%v, legacy=%v) = %v, want %v", tc.state, tc.replay, tc.legacy, got, tc.want)
		}
		if got := stateGuard(tc.state, tc.replay, tc.legacy) == nil; got != tc.want {
			t.Errorf("stateGuard(%s, replay=%v, legacy=%v) allows %v, want %v", tc.state, tc.replay, tc.legacy, got, tc.want)
		}
	}
	for _, state := range []string{utxoStateReady, utxoStateCatching, utxoStateDegraded} {
		r := newRebuildFixture(t)
		r.m.set(state, "")
		err := r.m.startSeed()
		allowed := state != utxoStateDegraded
		if allowed == errors.Is(err, errUtxoSeedState) || allowed && err != nil {
			t.Errorf("startSeed in %s returned %v, allowed=%v", state, err, allowed)
		}
		if allowed && (r.m.build == nil || r.m.Status().State != state) {
			t.Errorf("a legacy index in %s must rebuild beside and keep its state: %+v", state, r.m.Status())
		}
		if !allowed && r.m.build != nil {
			t.Errorf("a legacy index in %s started a rebuild", state)
		}
		_ = r.m.store.Close()
	}
}

func TestLegacyReplayIndexInCatchingUpStateRebuilds(t *testing.T) {
	r := newRebuildFixture(t)
	m := r.m
	m.set(utxoStateCatching, "")
	if err := m.execSeed(t.Context(), m.seedFromReplay); err != nil {
		t.Fatal(err)
	}
	m.tick(t.Context())
	if st := m.Status(); st.P2PKMissing || !m.store.canon {
		t.Fatalf("the catching legacy replay index did not switch: %+v", st)
	}
	if got := r.amounts(t); !slices.Equal(got, []uint64{100_000_000, 200_000_000}) {
		t.Fatalf("the rebuilt replay index counts %v", got)
	}
}

func TestLayoutRecoversFromACrashBeforeAndAfterTheSwitch(t *testing.T) {
	build := func(t *testing.T, dir, name string) {
		t.Helper()
		sub := filepath.Join(dir, name)
		if err := os.MkdirAll(sub, 0o700); err != nil {
			t.Fatal(err)
		}
		s, err := openUtxoStoreFS(sub, nil, false)
		if err != nil {
			t.Fatal(err)
		}
		if err := newUtxoFollower(s, newFakeUtxoNode(rebuildChain(t))).Step(t.Context()); err != nil {
			t.Fatal(err)
		}
		_ = s.Close()
	}
	legacyDir := func(t *testing.T) string {
		t.Helper()
		dir := t.TempDir()
		legacy := p2pkOpenLegacy(t, dir, false)
		if err := newUtxoFollower(legacy, newFakeUtxoNode(rebuildChain(t))).Step(t.Context()); err != nil {
			t.Fatal(err)
		}
		_ = legacy.Close()
		return dir
	}
	open := func(t *testing.T, dir string) *utxoManager {
		return utxoMgrNew(t, dir, nil, newFakeUtxoNode(rebuildChain(t)), io.Discard)
	}

	t.Run("crash before the switch", func(t *testing.T) {
		dir := legacyDir(t)
		build(t, dir, utxoRebuildPrefix+"111")
		m := open(t, dir)
		if st := m.Status(); !st.P2PKMissing || m.store.canon {
			t.Fatalf("the legacy index must keep serving: %+v", st)
		}
		if names := rebuildEntries(t, dir); hasPrefixEntry(names, utxoRebuildPrefix) {
			t.Fatalf("the partial rebuild was not removed: %v", names)
		}
	})
	t.Run("crash after the pointer moved", func(t *testing.T) {
		dir := legacyDir(t)
		m := open(t, dir)
		_ = m.store.Close()
		build(t, dir, utxoRebuildPrefix+"222")
		m.mu.Lock()
		err := m.writeActive(utxoRebuildPrefix + "222")
		m.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		m = open(t, dir)
		if st := m.Status(); st.P2PKMissing || !m.store.canon {
			t.Fatalf("the new index must be active after the pointer moved: %+v", st)
		}
		names := rebuildEntries(t, dir)
		if len(names) != 2 || !slices.Contains(names, utxoActiveName) || !slices.Contains(names, utxoRebuildPrefix+"222") {
			t.Fatalf("the old files must be removed on recovery: %v", names)
		}
		if h, _, ok, _ := m.store.Tip(); !ok || h != 10 {
			t.Fatalf("the recovered index tip is %d", h)
		}
	})
	t.Run("pointer to a missing directory", func(t *testing.T) {
		dir := legacyDir(t)
		m := open(t, dir)
		m.mu.Lock()
		err := m.writeActive(utxoRebuildPrefix + "333")
		m.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		_ = m.store.Close()
		m = open(t, dir)
		if st := m.Status(); !st.P2PKMissing || !m.store.Valid() {
			t.Fatalf("a dangling pointer must fall back to the volume root: %+v", st)
		}
		if names := rebuildEntries(t, dir); slices.Contains(names, utxoActiveName) {
			t.Fatalf("the dangling pointer stayed: %v", names)
		}
	})
	t.Run("a pointer cannot leave the index directory", func(t *testing.T) {
		dir := legacyDir(t)
		m := open(t, dir)
		for _, name := range []string{"../escape", utxoRebuildPrefix + "../x", "other"} {
			m.mu.Lock()
			err := m.writeActive(name)
			m.mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			if got := m.readActive(); got != "" {
				t.Fatalf("pointer %q was accepted as %q", name, got)
			}
		}
	})
}

func TestFreeSpaceRuleForALegacyRebuildNeedsRoomForTheSecondCopy(t *testing.T) {
	in := seedGuardInput{State: utxoStateReady, Legacy: true, LegacyBytes: 40 << 30, TempC: 50, CPUs: 0.5,
		Chain: blockchainInfo{Chain: "main", Blocks: 1, Headers: 1}, Now: time.Unix(1_800_000_000, 0), TipTime: time.Unix(1_800_000_000, 0), Replay: true}
	blocked := func(free uint64) bool {
		in.FreeBytes = free
		return slices.ContainsFunc(evaluateSeedGuards(in), func(r seedRefusal) bool { return r.Guard == "free_space" })
	}
	need := utxoMinFreeBytes + in.LegacyBytes
	if !blocked(need-1) || blocked(need) {
		t.Fatal("a legacy replay rebuild must need the free-space floor plus a copy of the old index")
	}
	in.Replay, in.LegacyBytes = false, 40<<30
	if !blocked(seedMinFreeBytes-1) || blocked(seedMinFreeBytes) {
		t.Fatalf("a dump rebuild needs a full seed's room free, with no credit for the old index")
	}
}

func TestGatherSeedGuardsGivesNoCreditForTheLegacyIndex(t *testing.T) {
	r := newRebuildFixture(t)
	m := r.m
	m.guards = m.gatherSeedGuards
	filler, err := os.Create(filepath.Join(r.dir, "filler"))
	if err != nil {
		t.Fatal(err)
	}
	const sparse = 64 << 30
	if err := filler.Truncate(sparse); err != nil {
		t.Skipf("no sparse files here: %v", err)
	}
	_ = filler.Close()
	free := diskUsage(r.dir).Free
	in := m.gatherSeedGuards(t.Context())
	if !in.Legacy || in.LegacyBytes < sparse {
		t.Fatalf("legacy input %+v", in)
	}
	if in.FreeBytes > free+sparse/2 {
		t.Fatalf("the old index's %d bytes were counted as free space: %d free, input says %d", in.LegacyBytes, free, in.FreeBytes)
	}
}

func TestSeedNowOffersALegacyIndexItsRebuild(t *testing.T) {
	a, _ := testApp(t, &rpcFixture{}, "")
	capability := func(supported bool) {
		b, _ := json.Marshal(hostControlCapability{Timestamp: time.Now().Unix(), Platform: "linux", BootKind: "boot", UTXOSeedSupported: supported})
		if err := os.WriteFile(a.nodeControlPath("capability"), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	capability(true)
	a.utxo = p2pkLegacyManager(t)
	if !a.seedNow(nodeBitcoin) {
		t.Fatal("Seed now was not offered for a ready legacy index")
	}
	if got := hostControlHTTP(a, http.MethodPost, `{"action":"utxo_seed","node":"bitcoin"}`, true, "192.168.1.20:4321").Code; got != http.StatusAccepted {
		t.Fatalf("Seed now for a legacy index = %d", got)
	}
	a.utxo.set(utxoStateCatching, "")
	if !a.seedNow(nodeBitcoin) {
		t.Fatal("Seed now was not offered for a catching legacy index")
	}
	if err := a.utxo.startSeed(); err != nil {
		t.Fatal(err)
	}
	if a.seedNow(nodeBitcoin) {
		t.Fatal("Seed now was offered while the legacy index rebuilds")
	}
	a.utxo.finishSeed(context.Canceled)
	capability(false)
	if a.seedNow(nodeBitcoin) {
		t.Fatal("Seed now was offered by a host helper that cannot prepare a seed")
	}
	capability(true)
	a.utxo.set(utxoStateDegraded, "")
	if a.seedNow(nodeBitcoin) {
		t.Fatal("Seed now was offered for a degraded index")
	}
	fresh := utxoMgrNew(t, t.TempDir(), nil, newFakeUtxoNode(utxoMgrChain(2, "b")), io.Discard)
	a.utxo = fresh
	fresh.set(utxoStateReady, "")
	if a.seedNow(nodeBitcoin) {
		t.Fatal("Seed now was offered for a ready current index")
	}
}

func TestSeedNowOffersALegacyReplayIndexItsRebuild(t *testing.T) {
	a, _ := testApp(t, &rpcFixture{}, "")
	r := newRebuildFixture(t)
	a.chainUtxo = map[string]*utxoManager{nodeLitecoin: r.m}
	if !a.seedNow(nodeLitecoin) {
		t.Fatalf("Seed now was not offered for a ready legacy replay index: %+v", r.m.Status())
	}
	r.m.set(utxoStateCatching, "")
	if !a.seedNow(nodeLitecoin) {
		t.Fatalf("Seed now was not offered for a legacy replay index catching up within the gap: %+v", r.m.Status())
	}
	r.m.set(utxoStateDegraded, "x")
	if a.seedNow(nodeLitecoin) {
		t.Fatal("Seed now was offered for a degraded replay index")
	}
}

func TestStatusAndNeedsHostExplainALegacyIndexThatCannotRebuild(t *testing.T) {
	a, _ := testApp(t, &rpcFixture{}, "")
	m := p2pkLegacyManager(t)
	m.configureSeed(utxoSeedHost{stateDir: t.TempDir(), dataDir: t.TempDir(), hold: func() bool { return true }, release: func() {}})
	a.utxo = m
	st := m.Status()
	if !a.needsHost(m, st) {
		t.Fatal("a legacy index with no host helper must say it needs the host")
	}
	m.mu.Lock()
	m.waiting = waitBlocked
	m.seed.AutoFailures = utxoAutoSeedTries
	m.mu.Unlock()
	st = m.Status()
	if st.WaitingFor != waitBlocked || st.SeedStopped != "failed" {
		t.Fatalf("a ready legacy index must expose why its rebuild is not starting: %+v", st)
	}
	m.mu.Lock()
	m.seeding, m.build = true, &utxoStore{}
	m.mu.Unlock()
	st = m.Status()
	if !st.Rebuilding || a.needsHost(m, st) || !a.anySeedActive() {
		t.Fatalf("a running rebuild: %+v", st)
	}
	m.mu.Lock()
	m.seeding, m.build = false, nil
	m.mu.Unlock()
}

func TestHeightGuardsRejectTheP2PKBit(t *testing.T) {
	s := utxoTestOpen(t, t.TempDir())
	f := newP2PKFixture(t, false)
	err := s.ApplyBlock(utxoBlock{Height: utxoP2PKBit, Hash: utxoTestHash(1), PrevHash: utxoTestHash(0), Creates: f.initialCreates()})
	if err == nil || !strings.Contains(err.Error(), "out of range") {
		t.Fatalf("a block at height 2^31 was applied: %v", err)
	}
	ok := utxoBlock{Height: utxoP2PKBit - 1, Hash: utxoTestHash(1), PrevHash: utxoTestHash(0), Creates: f.initialCreates()}
	if err := s.ApplyBlock(ok); err != nil {
		t.Fatalf("the highest height below the flag must be accepted: %v", err)
	}
	coins, err := s.Lookup([][]byte{f.pkh[p2pkAddrGCompressed]})
	if err != nil || len(coins) != 2 {
		t.Fatalf("lookup %v %v", coins, err)
	}
	for _, c := range coins {
		if c.Height != utxoP2PKBit-1 {
			t.Fatalf("the stored height %d lost bits to the flag", c.Height)
		}
	}

	ingest := func(height uint32) error {
		s := utxoTestOpen(t, t.TempDir())
		rec := utxoRecord{ScriptPubKey: f.pkh[p2pkAddrGCompressed], Txid: utxoTestTxid(5), Amount: 5, Height: height}
		parse := func(onHeader func(utxoHeader), fn func(utxoRecord) error) (utxoHeader, error) {
			hdr := utxoHeader{BaseHash: utxoTestHash(1), CoinsCount: 1}
			onHeader(hdr)
			return hdr, fn(rec)
		}
		_, err := ingestUtxoRecords(s, parse, func([32]byte) (uint32, error) { return 1, nil }, nil)
		return err
	}
	if err := ingest(utxoP2PKBit); err == nil || !strings.Contains(err.Error(), "out of range") {
		t.Fatalf("a snapshot coin at height 2^31 was ingested: %v", err)
	}
	if err := ingest(utxoP2PKBit - 1); err != nil {
		t.Fatalf("a snapshot coin below the flag must be accepted: %v", err)
	}
}
