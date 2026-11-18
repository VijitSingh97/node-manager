package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestDogecoinIndexConfigAndWiring(t *testing.T) {
	t.Setenv("DOGECOIN_ENABLED", "1")
	t.Setenv("DOGECOIN_RPC_USER", "u")
	t.Setenv("DOGECOIN_RPC_PASSWORD", "p")
	t.Setenv("DOGECOIN_UTXO_INDEX_ENABLED", "1")
	t.Setenv("DOGECOIN_UTXO_INDEX_DIR", filepath.Join(t.TempDir(), "doge-index"))
	var cfg config
	if err := loadAdditionalNodes(&cfg); err != nil || !cfg.nodes[nodeDogecoin].UTXOIndex {
		t.Fatalf("dogecoin %+v (%v)", cfg.nodes[nodeDogecoin], err)
	}
	cfg.dataDir, cfg.stateDir = t.TempDir(), t.TempDir()
	a := chainTestApp(t, cfg, io.Discard)
	m := a.chainUtxo[nodeDogecoin]
	if m == nil || m.chain != utxoDogecoin || !m.currentStore().reverse || m.host.prefix != "dogecoin-" || m.host.lock != &a.utxoSeedLock {
		t.Fatalf("dogecoin index wired as %+v", m)
	}
	store := a.familyWatch.stores[nodeDogecoin]
	if store == nil || store.index != m || m.onApplied == nil || store.maxWallets != maxIndexedWatchWallets {
		t.Fatal("the Dogecoin watch does not read the Dogecoin index")
	}
	// Without the archival watch wallet the index is the only source, so wallets wait for it rather than calling the wallet RPCs.
	if _, ready := store.backend.ready(t.Context(), store, time.Now().Unix()); ready || store.imported != nil {
		t.Fatal("a pruned node's Dogecoin watch scanned before its index was ready")
	}
	if ok, reason := a.familyWatchCapability(nodeDogecoin); !ok {
		t.Fatalf("the Dogecoin watch is refused with the index on: %s", reason)
	}
}

// chainstateSeedRPC answers like a Dogecoin node restarted right after the copy: the first header lookups fail while it loads its block index.
type chainstateSeedRPC struct {
	mu          sync.Mutex
	best        string
	height      uint32
	warmup      int
	offMain     bool
	panics      bool
	pruneHeight int
}

func (r *chainstateSeedRPC) call(_ context.Context, method string, params any, out any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	switch method {
	case "getblockchaininfo":
		return json.Unmarshal(fmt.Appendf(nil, `{"chain":"regtest","blocks":400,"headers":400,"pruned":true,"pruneheight":%d}`, r.pruneHeight), out)
	case "getblockhash":
		return json.Unmarshal([]byte(`"0f9188f13cb7b2c71f2a335e3a4fc328bf5beb436012afca590b1a11466e2206"`), out)
	case "getblockheader":
		if r.panics {
			panic("a read of the copy panicked")
		}
		if r.warmup > 0 {
			r.warmup--
			return errors.New("RPC getblockheader returned an error: Loading block index...")
		}
		if params.([]any)[0] != r.best {
			return errors.New("RPC getblockheader returned an error: Block not found")
		}
		confirmations := 1
		if r.offMain {
			confirmations = -1
		}
		return json.Unmarshal(fmt.Appendf(nil, `{"height":%d,"confirmations":%d}`, r.height, confirmations), out)
	}
	return fmt.Errorf("unexpected RPC %s", method)
}

func newChainstateSeedManager(t *testing.T, rpc utxoRPC) (*utxoManager, string) {
	t.Helper()
	data := t.TempDir()
	if err := os.CopyFS(filepath.Join(data, utxoChainstateCopyName), os.DirFS("testdata/dogecoin-chainstate-regtest")); err != nil {
		t.Fatal(err)
	}
	m := utxoMgrNewChain(t, t.TempDir(), nil, rpc, io.Discard, utxoDogecoin)
	m.configureSeed(utxoSeedHost{prefix: "dogecoin-", stateDir: t.TempDir(), dataDir: data, lock: &sync.Mutex{},
		hold: func() bool { t.Error("a chainstate seed held the watch scan"); return false }, release: func() {}})
	m.temp = func() (float64, error) { return 50, nil }
	m.guards = func(context.Context) seedGuardInput {
		return seedGuardInput{State: utxoStateNotSeeded, TempC: 50, CPUs: 0.5, FreeBytes: 1 << 40, ChainErr: errors.New("the node is loading its block index")}
	}
	m.set(utxoStateNotSeeded, "")
	return m, data
}

func TestDogecoinIndexSeedsFromAChainstateCopy(t *testing.T) {
	swapReplayRetries(t, 3, time.Millisecond)
	rpc := &chainstateSeedRPC{best: "2f9d802dd325448211bc2d04b33f63739f0e4f9e49319588c1700c37e23868dc", height: 202, warmup: 2}
	m, _ := newChainstateSeedManager(t, rpc)
	m.seedRequested(t.Context(), newSeedRequestID())
	if st := m.Status(); st.LastSeed == nil || st.LastSeed.Outcome != "ok" || st.State != utxoStateCatching {
		t.Fatalf("the seed from the chainstate copy ended as %+v, last %+v", st, st.LastSeed)
	}
	want, _ := utxoRevHex(rpc.best)
	if h, hash, ok, err := m.currentStore().Tip(); err != nil || !ok || h != 202 || hash != want {
		t.Fatalf("index tip %d %x %v %v", h, hash, ok, err)
	}
	coins := 0
	for key := range utxoTestKeys(t, m.currentStore()) {
		if len(key) == utxoKeyLen {
			coins++
		}
	}
	if coins != 210 {
		t.Fatalf("%d coins indexed, the node reported 210", coins)
	}
}

func TestDogecoinChainstateSeedFailsCleanlyOnABadCopyOrAForkedTip(t *testing.T) {
	swapReplayRetries(t, 1, time.Millisecond)
	for name, setup := range map[string]func(rpc *chainstateSeedRPC, data string){
		"best block off the node's chain": func(rpc *chainstateSeedRPC, _ string) { rpc.offMain = true },
		"node never answers":              func(rpc *chainstateSeedRPC, _ string) { rpc.warmup = 10 },
		"copy missing":                    func(_ *chainstateSeedRPC, data string) { _ = os.RemoveAll(filepath.Join(data, utxoChainstateCopyName)) },
		"a read panics":                   func(rpc *chainstateSeedRPC, _ string) { rpc.panics = true },
		"a symlink in the copy": func(_ *chainstateSeedRPC, data string) {
			current, moved := filepath.Join(data, utxoChainstateCopyName, "CURRENT"), filepath.Join(data, "CURRENT")
			if os.Rename(current, moved) != nil || os.Symlink(moved, current) != nil {
				t.Fatal("could not plant the symlink")
			}
		},
		"the copy is a symlink": func(_ *chainstateSeedRPC, data string) {
			copied, moved := filepath.Join(data, utxoChainstateCopyName), filepath.Join(data, "elsewhere")
			if os.Rename(copied, moved) != nil || os.Symlink(moved, copied) != nil {
				t.Fatal("could not plant the symlink")
			}
		},
	} {
		rpc := &chainstateSeedRPC{best: "2f9d802dd325448211bc2d04b33f63739f0e4f9e49319588c1700c37e23868dc", height: 202}
		m, data := newChainstateSeedManager(t, rpc)
		setup(rpc, data)
		m.seedRequested(t.Context(), newSeedRequestID())
		st := m.Status()
		if st.LastSeed == nil || st.LastSeed.Outcome != "failed" || st.State != utxoStateNotSeeded {
			t.Fatalf("%s: %+v, last %+v", name, st, st.LastSeed)
		}
		if name != "best block off the node's chain" && name != "node never answers" && st.LastSeed.Reason != errSeedChainstate.Error() {
			t.Fatalf("%s: failed as %q", name, st.LastSeed.Reason)
		}
		if _, _, ok, _ := m.currentStore().Tip(); ok {
			t.Fatalf("%s: a failed seed left an index", name)
		}
	}
}

func TestDogecoinIndexAsksForASeedOnceAPrunedNodeDropsBlocksItNeeds(t *testing.T) {
	swapReplayRetries(t, 1, time.Millisecond)
	rpc := &chainstateSeedRPC{best: "2f9d802dd325448211bc2d04b33f63739f0e4f9e49319588c1700c37e23868dc", height: 202, pruneHeight: 300}
	m, _ := newChainstateSeedManager(t, rpc)
	m.seedRequested(t.Context(), newSeedRequestID())
	m.tick(t.Context())
	if st := m.Status(); st.State != utxoStateInvalid || !strings.Contains(st.Reason, "seed it again") {
		t.Fatalf("an index behind a pruned node's blocks reads %+v", st)
	}
	if !seedableState(m.Status().State, false) {
		t.Fatal("the stopped index cannot be seeded again")
	}
}

func TestDogecoinSeedIsRefusedWhenTheChainstateSizeIsUnreadable(t *testing.T) {
	m := utxoMgrNewChain(t, t.TempDir(), nil, &chainstateSeedRPC{}, io.Discard, utxoDogecoin)
	m.configureSeed(utxoSeedHost{prefix: "dogecoin-", stateDir: t.TempDir(), dataDir: filepath.Join(t.TempDir(), "missing"), lock: &sync.Mutex{}, hold: func() bool { return true }, release: func() {}})
	in := m.gatherSeedGuards(t.Context())
	in.Chainstate = true
	if in.FreeErr == nil || !slices.ContainsFunc(evaluateSeedGuards(in), func(r seedRefusal) bool { return r.Guard == "free_space" }) {
		t.Fatalf("an unreadable chainstate passed the free-space guard: %+v", in)
	}
}

func TestChainstateSeedNeedsThreeTimesTheChainstateFree(t *testing.T) {
	now := time.Now()
	refused := func(in seedGuardInput) bool {
		return slices.ContainsFunc(evaluateSeedGuards(in), func(r seedRefusal) bool { return r.Guard == "free_space" })
	}
	base := seedGuardInput{Chainstate: true, State: utxoStateNotSeeded, TempC: 50, CPUs: 0.5, Chain: blockchainInfo{Blocks: 1, Headers: 1}, TipTime: now, Now: now, RPCErr: errors.New("no getrpcinfo on 1.14")}
	for _, tc := range []struct {
		chainstate, free uint64
		refused          bool
	}{
		{10_000_000_000, 30_000_000_000 - 1, true},
		{10_000_000_000, 30_000_000_000, false},
		{100_000_000, utxoMinFreeBytes - 1, true},
		{100_000_000, utxoMinFreeBytes, false},
	} {
		in := base
		in.ChainstateBytes, in.FreeBytes = tc.chainstate, tc.free
		if refused(in) != tc.refused {
			t.Fatalf("chainstate %d with %d free: refused %v", tc.chainstate, tc.free, !tc.refused)
		}
		if slices.ContainsFunc(evaluateSeedGuards(in), func(r seedRefusal) bool { return r.Guard == "rpc_idle" }) {
			t.Fatal("a chainstate seed asked for getrpcinfo")
		}
	}
	data := t.TempDir()
	if err := os.MkdirAll(filepath.Join(data, "chainstate"), 0o700); err != nil {
		t.Fatal(err)
	}
	for name, size := range map[string]int{"000005.ldb": 1000, "000006.log": 500} {
		if err := os.WriteFile(filepath.Join(data, "chainstate", name), make([]byte, size), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	m := utxoMgrNewChain(t, t.TempDir(), nil, &chainstateSeedRPC{}, io.Discard, utxoDogecoin)
	m.configureSeed(utxoSeedHost{prefix: "dogecoin-", stateDir: t.TempDir(), dataDir: data, lock: &sync.Mutex{}, hold: func() bool { return true }, release: func() {}})
	if in := m.gatherSeedGuards(t.Context()); in.ChainstateBytes != 1500 || in.FreeErr != nil {
		t.Fatalf("the guard read the chainstate as %d bytes (%v)", in.ChainstateBytes, in.FreeErr)
	}
}

func TestHostControlAcceptsADogecoinSeedButNotZcash(t *testing.T) {
	if !validHostControlShape("utxo_seed", nodeDogecoin, nil, "", nil, "") || validHostControlShape("utxo_seed", nodeZcash, nil, "", nil, "") {
		t.Fatal("the pending seed shape is wrong for Dogecoin or Zcash")
	}
}
