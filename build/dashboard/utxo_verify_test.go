package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// Two coins of 90 billion units each put the total past int64, as Dogecoin's mainnet total already is.
const (
	verifyBigUnits  = 90_000_000_000 * 100_000_000
	verifyCoins     = 3
	verifyTotal     = "180000000000.00012345"
	verifyTestEvery = 20 * time.Millisecond
)

type verifyFakeNode struct {
	mu     sync.Mutex
	m      *utxoManager
	reply  map[string]any
	params []any
	calls  int
	held   bool
	during func()
}

func (n *verifyFakeNode) serve(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID     any    `json:"id"`
		Method string `json:"method"`
		Params []any  `json:"params"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if req.Method != "gettxoutsetinfo" {
		writeJSON(w, map[string]any{"id": req.ID, "result": nil, "error": map[string]any{"code": -32601, "message": "method not found"}})
		return
	}
	n.mu.Lock()
	n.params, n.held = req.Params, n.m.AlertsSuppressed()
	n.calls++
	result, during := maps.Clone(n.reply), n.during
	n.mu.Unlock()
	if during != nil {
		during()
	}
	writeJSON(w, map[string]any{"id": req.ID, "result": result, "error": nil})
}

func (n *verifyFakeNode) set(key string, value any) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.reply[key] = value
}

// verifyFixture indexes three coins at height 3, past a spent coin and an OP_RETURN output the node leaves out of its UTXO set too.
func verifyFixture(t *testing.T, id string, chain *utxoChainParams) (*utxoManager, *fakeUtxoChain, *verifyFakeNode) {
	t.Helper()
	c := newFakeUtxoChain()
	genesis(c)
	for range 3 {
		c.add("verify", nil, nil)
	}
	dir := t.TempDir()
	s, err := openUtxoStoreFS(dir, nil, chain.ReverseIndex)
	if err != nil {
		t.Fatal(err)
	}
	coin := func(label string, vout uint32, script string, units uint64) utxoCreate {
		txid, _ := utxoRevHex(fakeDisplayHash(label, 0))
		return utxoCreate{utxoOutpoint{txid, vout}, []byte(script), units}
	}
	spent := coin("c", 0, "\x76\xa9\x14spent", 7)
	blocks := []utxoBlock{
		{},
		{Creates: []utxoCreate{coin("a", 0, "\x76\xa9\x14a", verifyBigUnits), coin("a", 1, "\x6a\x04data", 5), spent}},
		{Creates: []utxoCreate{coin("d", 0, "\x00\x14d", 12345)}, Spends: []utxoSpend{{spent.utxoOutpoint, spent.Script}}},
		{Creates: []utxoCreate{coin("e", 0, "\x51", verifyBigUnits)}},
	}
	for h := range blocks {
		blocks[h].Height = uint32(h)
		blocks[h].Hash, _ = utxoRevHex(c.hashes[h])
		if h > 0 {
			blocks[h].PrevHash = blocks[h-1].Hash
		}
		if err := s.ApplyBlock(blocks[h]); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	m := utxoMgrNewChain(t, dir, nil, newFakeUtxoNode(c), io.Discard, chain)
	m.tick(context.Background())
	if st := m.Status(); st.State != utxoStateReady || st.Behind == nil || *st.Behind != 0 {
		t.Fatalf("index state %+v", st)
	}
	node := &verifyFakeNode{m: m, reply: map[string]any{"height": 3, "bestblock": c.hashes[3], "txouts": verifyCoins, "total_amount": json.RawMessage(verifyTotal)}}
	srv := httptest.NewServer(http.HandlerFunc(node.serve))
	t.Cleanup(srv.Close)
	a := &app{cfg: config{stateDir: t.TempDir()}}
	m.configureBench(a.chainBenchHost(id, &rpcClient{url: srv.URL, client: srv.Client()}, chain, func() bool { return true }, func() {}))
	return m, c, node
}

func verifyRequestFile(m *utxoManager) string {
	return filepath.Join(m.bench.stateDir, m.bench.prefix+verifyRequestName)
}

func writeVerifyRequest(t *testing.T, m *utxoManager, id string, n int, requested time.Time) {
	t.Helper()
	raw, _ := json.Marshal(verifyRequest{ID: benchTestID(n), Chain: id, RequestedAt: requested.Unix()})
	if err := os.WriteFile(verifyRequestFile(m), raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func readVerifyResult(t *testing.T, m *utxoManager) (verifyResult, bool) {
	t.Helper()
	out, err := os.ReadFile(filepath.Join(m.bench.stateDir, m.bench.prefix+verifyResultName))
	if errors.Is(err, fs.ErrNotExist) {
		return verifyResult{}, false
	}
	var res verifyResult
	if err != nil || json.Unmarshal(out, &res) != nil {
		t.Fatalf("result: %s, %v", out, err)
	}
	return res, true
}

func runVerify(t *testing.T, m *utxoManager, id string, n int) verifyResult {
	t.Helper()
	writeVerifyRequest(t, m, id, n, time.Now())
	m.pollVerify(context.Background())
	res, ok := readVerifyResult(t, m)
	if !ok || res.ID != benchTestID(n) {
		t.Fatalf("no result for request %d: %+v", n, res)
	}
	if _, err := os.Stat(verifyRequestFile(m)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("request %d was left behind: %v", n, err)
	}
	return res
}

func TestVerifyComparesTheIndexWithTheNodeOnEveryChain(t *testing.T) {
	chains := maps.Clone(utxoChains)
	chains[nodeBitcoin] = utxoBitcoin
	for _, id := range slices.Sorted(maps.Keys(chains)) {
		t.Run(id, func(t *testing.T) {
			m, c, node := verifyFixture(t, id, chains[id])
			wantParams, wantHash := []any{"none"}, "none"
			if id == nodeDogecoin {
				wantParams, wantHash = []any{}, "hash_serialized"
			}

			res := runVerify(t, m, id, 1)
			want := verifyTotals{Coins: verifyCoins, Amount: verifyTotal}
			if len(res.Refusals) != 0 || len(res.Failures) != 0 || res.Match == nil || !*res.Match || res.Chain != id || res.HashType != wantHash ||
				res.Height != 3 || res.BestBlock != c.hashes[3] || res.Index == nil || res.Node == nil {
				t.Fatalf("result = %+v", res)
			}
			if index, n := *res.Index, *res.Node; index.Coins != want.Coins || index.Amount != want.Amount || n.Coins != want.Coins || n.Amount != want.Amount {
				t.Fatalf("index %+v, node %+v, want %+v", index, n, want)
			}
			node.mu.Lock()
			params, held := node.params, node.held
			node.mu.Unlock()
			if !slices.Equal(params, wantParams) || !held || m.AlertsSuppressed() {
				t.Fatalf("gettxoutsetinfo params %#v, alerts held during the call %t, after %t", params, held, m.AlertsSuppressed())
			}

			node.set("txouts", verifyCoins+1)
			res = runVerify(t, m, id, 2)
			if res.Match == nil || *res.Match || len(res.Failures) != 1 || res.Failures[0] != "the index and the node's UTXO set differ at height 3" || res.Node.Coins != verifyCoins+1 {
				t.Fatalf("a node with one more coin did not fail: %+v", res)
			}
			node.set("txouts", verifyCoins)
			node.set("total_amount", json.RawMessage("180000000000.00012346"))
			if res = runVerify(t, m, id, 3); res.Match == nil || *res.Match || len(res.Failures) != 1 {
				t.Fatalf("a total one unit off did not fail: %+v", res)
			}

			node.set("bestblock", c.hashes[2])
			res = runVerify(t, m, id, 4)
			if res.Match != nil || len(res.Refusals) != 1 || !strings.Contains(res.Refusals[0], "not the index tip") {
				t.Fatalf("a node walk at another block was compared: %+v", res)
			}

			writeVerifyRequest(t, m, nodeBitcoin+"x", 5, time.Now())
			m.pollVerify(context.Background())
			if res, _ := readVerifyResult(t, m); len(res.Refusals) != 1 || res.Refusals[0] != "the verify request names another chain" {
				t.Fatalf("a request for another chain ran: %+v", res)
			}
		})
	}
}

func TestVerifyWaitsForTheIndexToReachTheNodeTip(t *testing.T) {
	m, c, node := verifyFixture(t, nodeBitcoin, utxoBitcoin)
	c.add("verify", nil, nil)
	writeVerifyRequest(t, m, nodeBitcoin, 1, time.Now())
	m.pollVerify(context.Background())
	if _, ok := readVerifyResult(t, m); ok {
		t.Fatal("verify ran with the index a block behind the node")
	}
	if _, err := os.Stat(verifyRequestFile(m)); err != nil {
		t.Fatalf("the waiting request was dropped: %v", err)
	}

	m.tick(context.Background())
	node.set("height", 4)
	node.set("bestblock", c.hashes[4])
	m.pollVerify(context.Background())
	if res, ok := readVerifyResult(t, m); !ok || res.Height != 4 || res.Match == nil || !*res.Match {
		t.Fatalf("verify did not run once the index caught up: %+v", res)
	}

	c.add("verify", nil, nil)
	writeVerifyRequest(t, m, nodeBitcoin, 2, time.Now().Add(-verifyLevelWait))
	m.pollVerify(context.Background())
	if res, _ := readVerifyResult(t, m); res.ID != benchTestID(2) || len(res.Refusals) != 1 || res.Refusals[0] != "the index (height 4) did not reach the node tip within 10 minutes" {
		t.Fatalf("an index that stayed behind was not refused: %+v", res)
	}
	node.mu.Lock()
	defer node.mu.Unlock()
	if node.calls != 1 {
		t.Fatalf("gettxoutsetinfo ran %d times, want once", node.calls)
	}
}

func TestVerifyRefusesAnIndexThatIsNotSeeded(t *testing.T) {
	c := newFakeUtxoChain()
	genesis(c)
	m := utxoMgrNewChain(t, t.TempDir(), nil, newFakeUtxoNode(c), io.Discard, utxoLitecoin)
	m.tick(context.Background())
	a := &app{cfg: config{stateDir: t.TempDir()}}
	m.configureBench(a.chainBenchHost(nodeLitecoin, &rpcClient{url: "http://127.0.0.1:1", client: http.DefaultClient}, utxoLitecoin, func() bool { return true }, func() {}))
	if res := runVerify(t, m, nodeLitecoin, 1); len(res.Refusals) != 1 || res.Refusals[0] != "the index is not_seeded; verify needs a seeded index that follows the node" {
		t.Fatalf("result = %+v", res)
	}
}

func TestVerifyAlertHoldSurvivesADashboardRestart(t *testing.T) {
	m, _, node := verifyFixture(t, nodeBitcoin, utxoBitcoin)
	seedState := filepath.Join(t.TempDir(), "utxo-seed.json")
	m.setSeedStatePath(seedState)
	midCall := filepath.Join(t.TempDir(), "mid-call.json")
	node.during = func() {
		raw, _ := os.ReadFile(seedState)
		_ = os.WriteFile(midCall, raw, 0o600)
	}
	if res := runVerify(t, m, nodeBitcoin, 1); res.Match == nil || !*res.Match {
		t.Fatalf("result = %+v", res)
	}
	held := func(path string, at time.Time) bool {
		r := utxoMgrNew(t, t.TempDir(), nil, newFakeUtxoNode(newFakeUtxoChain()), io.Discard)
		r.now = func() time.Time { return at }
		r.setSeedStatePath(path)
		return r.AlertsSuppressed()
	}
	if !held(midCall, time.Now()) {
		t.Fatal("a dashboard restarted during the node's walk dropped the alert hold")
	}
	if held(midCall, time.Now().Add(verifyCallLimit+time.Minute)) {
		t.Fatal("the restored alert hold outlived its deadline")
	}
	if held(seedState, time.Now()) {
		t.Fatal("a finished check left the alert hold persisted")
	}
}

func TestVerifyRunsFromTheIndexRunLoop(t *testing.T) {
	m, _, _ := verifyFixture(t, nodeLitecoin, utxoLitecoin)
	m.host = &utxoSeedHost{stateDir: m.bench.stateDir, prefix: m.bench.prefix}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		m.run(ctx)
		close(done)
	}()
	defer func() {
		cancel()
		<-done
	}()
	writeVerifyRequest(t, m, nodeLitecoin, 1, time.Now())
	for deadline := time.Now().Add(3 * seedPollEvery); time.Now().Before(deadline); time.Sleep(verifyTestEvery) {
		if res, ok := readVerifyResult(t, m); ok {
			if res.Match == nil || !*res.Match {
				t.Fatalf("result = %+v", res)
			}
			return
		}
	}
	t.Fatal("the run loop did not answer the verify request")
}

// verifyLiveStore runs verify's two counts against a live regtest node, which proves the hash_type argument and the coin rules on real blocks.
func verifyLiveStore(t *testing.T, n *liveNode, id string, chain *utxoChainParams, store *utxoStore) {
	t.Helper()
	ctx := context.Background()
	a := &app{cfg: config{stateDir: t.TempDir()}}
	info, err := a.chainBenchHost(id, n.rpc, chain, func() bool { return true }, func() {}).txOutSet(ctx)
	if err != nil {
		t.Fatalf("gettxoutsetinfo: %v", err)
	}
	coins, units, err := store.Totals(ctx)
	_, tip, _, _ := store.Tip()
	if err != nil || info.BestBlock != reverseHex(tip) || info.TxOuts != coins || uint64(info.Total) != units {
		t.Fatalf("index %d coins worth %s at %s (%v), node %d worth %s at %s", coins, verifyAmount(units), reverseHex(tip), err, info.TxOuts, verifyAmount(uint64(info.Total)), info.BestBlock)
	}
	t.Logf("verify at height %d: %d coins worth %s match gettxoutsetinfo", info.Height, coins, verifyAmount(units))
}
