package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

type p2pkBenchSetup struct {
	m       *utxoManager
	dir     string
	host    utxoBenchHost
	best    string
	height  uint32
	plain   benchTotals
	flagged benchTotals
	all     benchTotals
	asked   [][]utxoCoin
	against []string
}

func newP2PKBenchSetup(t *testing.T) *p2pkBenchSetup {
	t.Helper()
	pkhScript := hex.EncodeToString(p2pkAddr(t, p2pkAddrGCompressed))
	p2pk := hex.EncodeToString(p2pkScript(t, p2pkKeyGCompressed))
	m := indexTestManager(t, indexTestChain([]fakeOut{{pkhScript, "1.00000000"}, {p2pk, "2.00000000"}}))
	raw, _ := hex.DecodeString(pkhScript)
	coins, err := m.store.Lookup([][]byte{raw})
	if err != nil {
		t.Fatal(err)
	}
	plainCoins, flaggedCoins := splitP2PKCoins(coins)
	if len(plainCoins) != 1 || len(flaggedCoins) != 1 {
		t.Fatalf("fixture has %d plain and %d P2PK coins", len(plainCoins), len(flaggedCoins))
	}
	s := &p2pkBenchSetup{m: m, dir: t.TempDir(), host: benchTestHost(t.TempDir())}
	s.host.stateDir = s.dir
	s.host.resolve = indexTestScripts(pkhScript)
	height, hash, _ := m.Tip()
	s.height, s.best = height, reverseHex(hash)
	s.plain, s.flagged = benchTotalsOf(plainCoins), benchTotalsOf(flaggedCoins)
	s.all = benchTotalsOf(coins)
	s.host.confirmP2PK = func(_ context.Context, c []utxoCoin, compared string) (int, error) {
		s.asked = append(s.asked, c)
		s.against = append(s.against, compared)
		return 0, nil
	}
	return s
}

func (s *p2pkBenchSetup) run(t *testing.T, key string, scanned benchTotals) benchResult {
	t.Helper()
	s.host.scan = func(context.Context, []string) (benchScanRun, error) {
		return benchScanRun{Height: int64(s.height), Best: s.best, Totals: scanned}, nil
	}
	s.m.configureBench(s.host)
	return benchTestPoll(t, s.m, s.dir, benchTestRequest(benchTestID(31), []string{key}, 1, true))
}

func TestBenchScanSplitsP2PKCoinsOutOfTheComparison(t *testing.T) {
	s := newP2PKBenchSetup(t)
	res := s.run(t, "pkh("+p2pkKeyGCompressed+")", s.plain)
	if len(res.Failures) != 0 || len(res.Refusals) != 0 || res.Scan == nil || len(res.Scan.Wallets) != 1 {
		t.Fatalf("result %+v", res)
	}
	sw := res.Scan.Wallets[0]
	if sw.Match == nil || !*sw.Match || sw.IndexCoins != s.plain.Coins || sw.IndexSats != s.plain.Sats {
		t.Fatalf("a pkh wallet must be compared on its non-P2PK coins: %+v", sw)
	}
	if sw.P2PKCoins != 1 || sw.P2PKSats != s.flagged.Sats || sw.P2PKSpentAfter != 0 {
		t.Fatalf("the P2PK coins must be reported separately: %+v", sw)
	}
	if len(s.asked) != 1 || len(s.asked[0]) != 1 || !s.asked[0][0].P2PK || s.against[0] != s.best {
		t.Fatalf("the P2PK coin must be confirmed against the compared block: %v %v", s.asked, s.against)
	}
	raw, _ := json.Marshal(res.Scan.Wallets[0])
	if !strings.Contains(string(raw), `"p2pk_coins":1`) || !strings.Contains(string(raw), `"p2pk_sats":`) {
		t.Fatalf("the result file must carry the P2PK fields: %s", raw)
	}
}

func TestBenchScanComparesLikeWithLikeForPkAndComboWallets(t *testing.T) {
	for _, key := range []string{"combo(" + p2pkKeyGCompressed + ")", "pk(" + p2pkKeyGCompressed + ")"} {
		s := newP2PKBenchSetup(t)
		res := s.run(t, key, s.all)
		sw := res.Scan.Wallets[0]
		if len(res.Failures) != 0 || sw.Match == nil || !*sw.Match || sw.IndexCoins != s.all.Coins || sw.IndexSats != s.all.Sats {
			t.Fatalf("%s: a scan that sees P2PK must match the index including them: %+v %v", key, sw, res.Failures)
		}
		if len(s.asked) != 0 {
			t.Fatalf("%s: coins the scan already compared need no separate confirmation", key)
		}
		res = s.run(t, key, s.plain)
		if len(res.Failures) != 1 || *res.Scan.Wallets[0].Match {
			t.Fatalf("%s: a scan that misses a P2PK coin the index has must fail: %+v", key, res)
		}
	}
}

func TestBenchScanReportsAP2PKCoinTheNodeDoesNotConfirm(t *testing.T) {
	s := newP2PKBenchSetup(t)
	s.host.confirmP2PK = func(context.Context, []utxoCoin, string) (int, error) {
		return 0, errors.New("a:0 differs from the node's output")
	}
	res := s.run(t, "pkh("+p2pkKeyGCompressed+")", s.plain)
	if len(res.Failures) != 1 || !strings.Contains(res.Failures[0], "a P2PK coin the index attributes to it is not confirmed by the node: a:0 differs") {
		t.Fatalf("failures %q", res.Failures)
	}
	s.host.confirmP2PK = func(context.Context, []utxoCoin, string) (int, error) { return 1, nil }
	res = s.run(t, "pkh("+p2pkKeyGCompressed+")", s.plain)
	if len(res.Failures) != 0 || res.Scan.Wallets[0].P2PKSpentAfter != 1 {
		t.Fatalf("a coin spent after the scan is reported, not failed: %+v %q", res.Scan.Wallets[0], res.Failures)
	}
}

type p2pkConfirmNode struct {
	mu      sync.Mutex
	unspent map[string]map[string]any
	best    string
}

func (n *p2pkConfirmNode) server(t *testing.T) *rpcClient {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var raw json.RawMessage
		_ = json.NewDecoder(r.Body).Decode(&raw)
		n.mu.Lock()
		defer n.mu.Unlock()
		answer := func(method string, params []any) any {
			if method == "getbestblockhash" {
				return n.best
			}
			key := params[0].(string) + ":" + string(rune('0'+int(params[1].(float64))))
			if out, ok := n.unspent[key]; ok {
				return out
			}
			return nil
		}
		var batch []struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
			Params []any  `json:"params"`
		}
		if json.Unmarshal(raw, &batch) == nil {
			out := make([]map[string]any, len(batch))
			for i, c := range batch {
				out[i] = map[string]any{"id": c.ID, "result": answer(c.Method, c.Params), "error": nil}
			}
			_ = json.NewEncoder(w).Encode(out)
			return
		}
		var one struct {
			Method string `json:"method"`
			Params []any  `json:"params"`
		}
		_ = json.Unmarshal(raw, &one)
		_ = json.NewEncoder(w).Encode(map[string]any{"result": answer(one.Method, one.Params), "error": nil})
	}))
	t.Cleanup(srv.Close)
	return &rpcClient{url: srv.URL, client: srv.Client()}
}

func TestConfirmP2PKCoinsAgainstTheComparedBlock(t *testing.T) {
	script := p2pkScript(t, p2pkKeyGCompressed)
	h, _ := utxoCanonHash(script)
	txid := utxoTestTxid(7)
	coin := utxoCoin{utxoOutpoint: utxoOutpoint{Txid: txid, Vout: 0}, ScriptHash: h, Amount: 200_000_000, P2PK: true}
	out := map[string]any{"value": 2.0, "scriptPubKey": map[string]any{"hex": hex.EncodeToString(script)}}
	node := &p2pkConfirmNode{unspent: map[string]map[string]any{reverseHex(txid) + ":0": out}, best: "tip"}
	rpc := node.server(t)
	ctx := context.Background()

	if spent, err := confirmP2PKCoins(ctx, rpc, []utxoCoin{coin}, "tip"); err != nil || spent != 0 {
		t.Fatalf("a coin the node has: spent %d, %v", spent, err)
	}
	wrong := coin
	wrong.Amount++
	if _, err := confirmP2PKCoins(ctx, rpc, []utxoCoin{wrong}, "tip"); err == nil || !strings.Contains(err.Error(), "differs from the node's output") {
		t.Fatalf("a coin with another amount: %v", err)
	}
	node.unspent = nil
	if _, err := confirmP2PKCoins(ctx, rpc, []utxoCoin{coin}, "tip"); err == nil || !strings.Contains(err.Error(), "is not unspent") {
		t.Fatalf("a coin gone while the tip is the compared block must fail: %v", err)
	}
	node.best = "newer"
	if spent, err := confirmP2PKCoins(ctx, rpc, []utxoCoin{coin}, "tip"); err != nil || spent != 1 {
		t.Fatalf("a coin gone after the tip moved is spent after the scan: spent %d, %v", spent, err)
	}
}
