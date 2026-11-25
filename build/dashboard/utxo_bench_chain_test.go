package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"maps"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

const (
	benchChainCoins      = 200_000
	benchChainBlockCoins = 10_000
	benchScanCoinCost    = time.Microsecond
	benchSingleMargin    = 50
	benchWalletMargin    = 2
)

var raceEnabled bool

type benchFakeCoin struct {
	txid   string
	height int64
	script string
	sats   int64
}

type benchFakeNode struct {
	coins         []benchFakeCoin
	addresses     map[string][]string
	scripts       map[string]string
	height        int64
	best          string
	noDescriptors bool
	aliases       map[string]string
}

type benchChainWallet struct {
	key   string
	want  benchTotals
	multi bool
}

// benchChainScript gives a P2PKH script and its address under the version byte, so only the right chain can decode it.
func benchChainScript(label string, i int, version byte) (string, string) {
	sum := sha256.Sum256(fmt.Appendf(nil, "%s/%d", label, i))
	return base58EncodeCheck(append([]byte{version}, sum[:20]...)), "76a914" + hex.EncodeToString(sum[:20]) + "88ac"
}

func (n *benchFakeNode) call(method string, params []any) (any, string) {
	if n.noDescriptors && (method == "getdescriptorinfo" || method == "deriveaddresses") {
		return nil, "Method not found"
	}
	switch method {
	case "getdescriptorinfo":
		desc, _ := params[0].(string)
		if _, ok := n.addresses[desc]; !ok {
			return nil, "unknown descriptor"
		}
		return map[string]any{"descriptor": desc + "#benchsum", "isrange": strings.Contains(desc, "*"), "hasprivatekeys": false}, ""
	case "deriveaddresses":
		desc, _ := params[0].(string)
		desc, _, _ = strings.Cut(desc, "#")
		return n.addresses[desc], ""
	case "validateaddress":
		address, _ := params[0].(string)
		if canonical, ok := n.aliases[address]; ok {
			address = canonical
		}
		script, ok := n.scripts[address]
		return map[string]any{"isvalid": ok, "address": address, "scriptPubKey": script}, ""
	case "scantxoutset":
		if params[0] == "status" {
			return nil, ""
		}
		objects, _ := params[1].([]any)
		return n.scan(objects), ""
	}
	return nil, "method not found"
}

// scan walks every coin as scantxoutset does and charges benchScanCoinCost per coin, the order of mini-1's measured rate.
func (n *benchFakeNode) scan(objects []any) map[string]any {
	started := time.Now()
	needles := map[string]bool{}
	for _, object := range objects {
		desc, _ := object.(string)
		if ranged, ok := object.(map[string]any); ok {
			desc, _ = ranged["desc"].(string)
		}
		desc, _, _ = strings.Cut(desc, "#")
		for _, address := range n.addresses[desc] {
			needles[n.scripts[address]] = true
		}
	}
	unspents := []map[string]any{}
	var total int64
	for _, c := range n.coins {
		if needles[c.script] {
			unspent := map[string]any{"txid": c.txid, "vout": 0, "scriptPubKey": c.script, "amount": float64(c.sats) / 1e8, "height": c.height}
			if !n.noDescriptors {
				unspent["desc"] = "raw(" + c.script + ")#benchsum"
			}
			unspents = append(unspents, unspent)
			total += c.sats
		}
	}
	time.Sleep(time.Duration(len(n.coins))*benchScanCoinCost - time.Since(started))
	result := map[string]any{"success": true, "height": n.height, "bestblock": n.best, "unspents": unspents, "total_amount": float64(total) / 1e8}
	// Bitcoin Cash Node 29.2.0 names the count searched_items and gives no desc per coin.
	if n.noDescriptors {
		result["searched_items"] = len(n.coins)
	} else {
		result["txouts"] = len(n.coins)
	}
	return result
}

func (n *benchFakeNode) serve(w http.ResponseWriter, r *http.Request) {
	type call struct {
		ID     any    `json:"id"`
		Method string `json:"method"`
		Params []any  `json:"params"`
	}
	reply := func(c call) map[string]any {
		result, failure := n.call(c.Method, c.Params)
		var rpcErr any
		if failure != "" {
			rpcErr = map[string]any{"code": -1, "message": failure}
		}
		return map[string]any{"id": c.ID, "result": result, "error": rpcErr}
	}
	body, _ := io.ReadAll(r.Body)
	var batch []call
	if json.Unmarshal(body, &batch) == nil {
		out := make([]map[string]any, len(batch))
		for i, c := range batch {
			out[i] = reply(c)
		}
		writeJSON(w, out)
		return
	}
	var single call
	_ = json.Unmarshal(body, &single)
	writeJSON(w, reply(single))
}

// benchChainFixture builds a synthetic coin set with random single-address and 2,000-script wallets, indexes it for the
// chain, and serves the same coins from a fake node.
func benchChainFixture(t *testing.T, chain *utxoChainParams, seed uint64) (*utxoManager, *benchFakeNode, []benchChainWallet) {
	t.Helper()
	rng := rand.New(rand.NewPCG(seed, 0))
	version := byte(0x6f)
	if !chain.LocalScripts {
		version = 0x30
	}
	n := &benchFakeNode{addresses: map[string][]string{}, scripts: map[string]string{}}
	var wallets []benchChainWallet
	addCoins := func(w *benchChainWallet, scripts []string, count int) {
		for range count {
			c := benchFakeCoin{script: scripts[rng.IntN(len(scripts))], sats: 546 + rng.Int64N(100_000_000)}
			n.coins = append(n.coins, c)
			w.want.Coins++
			w.want.Sats += c.sats
		}
	}
	for i := range 6 {
		address, script := benchChainScript(fmt.Sprintf("single-%d", seed), i, version)
		desc := "addr(" + address + ")"
		n.addresses[desc], n.scripts[address] = []string{address}, script
		w := benchChainWallet{key: desc}
		count := rng.IntN(30)
		if i == 0 {
			count = 1_500
		}
		addCoins(&w, []string{script}, count)
		wallets = append(wallets, w)
	}
	for i := range 3 {
		w := benchChainWallet{key: fmt.Sprintf("pkh(%s/%d/<0;1>/*)", testXpub, i), multi: true}
		var scripts []string
		for branch := range 2 {
			desc := fmt.Sprintf("pkh(%s/%d/%d/*)", testXpub, i, branch)
			for k := range 1000 {
				address, script := benchChainScript(desc, k, version)
				n.addresses[desc] = append(n.addresses[desc], address)
				n.scripts[address] = script
				scripts = append(scripts, script)
			}
		}
		addCoins(&w, scripts, rng.IntN(40))
		wallets = append(wallets, w)
	}
	noise := make([]string, 40_000)
	for i := range noise {
		_, noise[i] = benchChainScript(fmt.Sprintf("noise-%d", seed), i, version)
	}
	addCoins(&benchChainWallet{}, noise, benchChainCoins-len(n.coins))
	rng.Shuffle(len(n.coins), func(i, j int) { n.coins[i], n.coins[j] = n.coins[j], n.coins[i] })

	c := newFakeUtxoChain()
	genesis(c)
	dir := t.TempDir()
	s, err := openUtxoStoreFS(dir, nil, chain.ReverseIndex)
	if err != nil {
		t.Fatal(err)
	}
	hash, _ := utxoRevHex(c.hashes[0])
	if err := s.ApplyBlock(utxoBlock{Hash: hash}); err != nil {
		t.Fatal(err)
	}
	for start := 0; start < len(n.coins); start += benchChainBlockCoins {
		h := len(c.hashes)
		c.add("bench", nil, nil)
		blk := utxoBlock{Height: uint32(h), PrevHash: hash}
		blk.Hash, _ = utxoRevHex(c.hashes[h])
		hash = blk.Hash
		for i := start; i < min(start+benchChainBlockCoins, len(n.coins)); i++ {
			coin := &n.coins[i]
			coin.txid, coin.height = fakeDisplayHash("benchtx", i), int64(h)
			txid, _ := utxoRevHex(coin.txid)
			script, _ := hex.DecodeString(coin.script)
			blk.Creates = append(blk.Creates, utxoCreate{utxoOutpoint: utxoOutpoint{Txid: txid, Vout: 0}, Script: script, Amount: uint64(coin.sats)})
		}
		if err := s.ApplyBlock(blk); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	n.height, n.best = int64(len(c.hashes)-1), c.hashes[len(c.hashes)-1]
	m := utxoMgrNewChain(t, dir, nil, newFakeUtxoNode(c), io.Discard, chain)
	m.tick(context.Background())
	if st := m.Status(); st.State != utxoStateReady || st.Behind == nil || *st.Behind != 0 {
		t.Fatalf("index state %+v", st)
	}
	return m, n, wallets
}

func benchChainRun(t *testing.T, m *utxoManager, id string, wallets []benchChainWallet) benchResult {
	t.Helper()
	req := benchRequest{ID: benchTestID(len(wallets)), Chain: id, RequestedAt: time.Now().Unix(), Repeat: 20, Scan: true}
	for i, w := range wallets {
		req.Wallets = append(req.Wallets, benchWallet{Name: fmt.Sprintf("w%d", i), Key: w.key})
	}
	raw, _ := json.Marshal(req)
	h := m.bench
	if err := os.WriteFile(filepath.Join(h.stateDir, h.prefix+benchRequestName), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	m.pollBench(context.Background())
	out, err := os.ReadFile(filepath.Join(h.stateDir, h.prefix+benchResultName))
	if err != nil {
		t.Fatal(err)
	}
	var res benchResult
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatal(err)
	}
	return res
}

// TestBenchIndexMatchesScanAndIsFaster runs the bench with --scan on every indexed chain through that chain's own hook.
func TestBenchIndexMatchesScanAndIsFaster(t *testing.T) {
	if raceEnabled {
		t.Skip("the race detector slows index lookups 40 to 80 times; CI runs this test without it")
	}
	chains := maps.Clone(utxoChains)
	chains[nodeBitcoin] = utxoBitcoin
	for n, id := range slices.Sorted(maps.Keys(chains)) {
		seed := uint64(n + 1)
		t.Run(id, func(t *testing.T) {
			if id == nodeDogecoin {
				t.Skip("Dogecoin Core 1.14.9 has no scantxoutset, and the command line refuses its bench")
			}
			m, node, wallets := benchChainFixture(t, chains[id], seed)
			if len(chains[id].Descriptors) == 0 {
				node.noDescriptors, node.aliases = true, map[string]string{}
				wallets = slices.DeleteFunc(wallets, func(w benchChainWallet) bool { return w.multi })
				for i, w := range wallets {
					address := strings.TrimSuffix(strings.TrimPrefix(w.key, "addr("), ")")
					node.aliases["spelled:"+address] = address
					wallets[i].key = "addr(spelled:" + address + ")"
				}
			}
			srv := httptest.NewServer(http.HandlerFunc(node.serve))
			t.Cleanup(srv.Close)
			a := &app{cfg: config{stateDir: t.TempDir()}}
			m.configureBench(a.chainBenchHost(id, &rpcClient{url: srv.URL, client: srv.Client()}, chains[id], func() bool { return true }, func() {}))

			res := benchChainRun(t, m, id, wallets)
			if len(res.Refusals) != 0 || len(res.Failures) != 0 || res.Chain != id || res.Scan == nil || len(res.Scan.Wallets) != len(wallets) {
				t.Fatalf("result = %+v", res)
			}
			for i, w := range wallets {
				got, scan := res.Wallets[i], res.Scan.Wallets[i]
				scripts, margin := 1, float64(benchSingleMargin)
				if w.multi {
					scripts, margin = 2000, benchWalletMargin
				}
				if got.Fallback || got.Lookup == nil || got.Scripts != scripts {
					t.Fatalf("wallet %d was not indexed: %+v", i, got)
				}
				if (benchTotals{got.Coins, got.Sats}) != w.want || (benchTotals{scan.Coins, scan.Sats}) != w.want || scan.Match == nil || !*scan.Match {
					t.Fatalf("wallet %d: index %d coins %d sats, scan %d coins %d sats, want %+v", i, got.Coins, got.Sats, scan.Coins, scan.Sats, w.want)
				}
				if speedup := scan.Seconds / got.Lookup.P50; speedup < margin {
					t.Errorf("wallet %d (%d scripts): index p50 %.4fs is only %.1fx faster than the %.3fs scan, want %.0fx", i, scripts, got.Lookup.P50, speedup, scan.Seconds, margin)
				} else {
					t.Logf("wallet %d (%d scripts, %d coins): index p50 %.5fs, scan %.3fs, %.0fx", i, scripts, got.Coins, got.Lookup.P50, scan.Seconds, speedup)
				}
			}

			_, busy := benchChainScript(fmt.Sprintf("single-%d", seed), 0, 0)
			dropped := slices.IndexFunc(node.coins, func(c benchFakeCoin) bool { return c.script == busy })
			node.coins = slices.Delete(node.coins, dropped, dropped+1)
			res = benchChainRun(t, m, id, wallets)
			if sw := res.Scan.Wallets; len(res.Failures) != 1 || !strings.HasPrefix(res.Failures[0], "wallet 1: the index and the scan differ") || sw[0].Match == nil || *sw[0].Match || sw[1].Match == nil || !*sw[1].Match {
				t.Fatalf("a coin missing from the node did not fail the run: %q", res.Failures)
			}

			if node.noDescriptors {
				res = benchChainRun(t, m, id, []benchChainWallet{{key: fmt.Sprintf("pkh(%s/0/<0;1>/*)", testXpub)}})
				if len(res.Refusals) != 1 || res.Refusals[0] != "wallet 1 is not accepted as a public watch key" {
					t.Fatalf("a descriptor on a node without descriptor calls was not refused: %q", res.Refusals)
				}
			}
		})
	}
}

func TestBenchScanCountsBalancesAboveBitcoinSupply(t *testing.T) {
	address, script := benchChainScript("rich", 0, 0x30)
	n := &benchFakeNode{addresses: map[string][]string{"addr(" + address + ")": {address}}, scripts: map[string]string{address: script}, height: 1, best: strings.Repeat("ab", 32),
		coins: []benchFakeCoin{{txid: strings.Repeat("cd", 32), height: 1, script: script, sats: 30_000_000 * 100_000_000}}}
	srv := httptest.NewServer(http.HandlerFunc(n.serve))
	defer srv.Close()
	run, err := benchScanRPC(context.Background(), &rpcClient{url: srv.URL, client: srv.Client()}, []any{"addr(" + address + ")"})
	if err != nil || run.Totals != (benchTotals{1, 30_000_000 * 100_000_000}) {
		t.Fatalf("a 30M-coin Litecoin wallet: %+v, %v", run, err)
	}
}

func TestAddrKeyScriptTakesOnlyAnAddressTheNodeKnows(t *testing.T) {
	address, script := benchChainScript("addr-key", 0, 0x30)
	n := &benchFakeNode{scripts: map[string]string{address: script, "noscript": ""}}
	srv := httptest.NewServer(http.HandlerFunc(n.serve))
	defer srv.Close()
	rpc := &rpcClient{url: srv.URL, client: srv.Client()}
	if got, raw, err := addrKeyScript(context.Background(), rpc, "addr("+address+")"); err != nil || got != address || hex.EncodeToString(raw) != script {
		t.Fatalf("addr key: %q %x %v", got, raw, err)
	}
	for _, key := range []string{address, "addr(" + address + ")#abcdefgh", "addr(" + address + ",x)", "addr((" + address + "))", "addr( " + address + ")", "addr(unknown)", "addr(noscript)", "addr()"} {
		if _, _, err := addrKeyScript(context.Background(), rpc, key); err == nil {
			t.Errorf("%q was accepted", key)
		}
	}
}

func TestEveryIndexedChainStartsItsBench(t *testing.T) {
	for id, chain := range utxoChains {
		a := &app{cfg: config{stateDir: t.TempDir()}, familyWatch: &familyWatchManager{stores: map[string]*familyWatchStore{}}}
		m := a.newChainUtxo(nodeConfig{ID: id, RPCURL: "http://127.0.0.1:1", DataDir: t.TempDir(), UTXOIndexDir: t.TempDir()}, chain, log.New(io.Discard, "", 0))
		t.Cleanup(func() { _ = m.store.Close() })
		if h := m.bench; h == nil || h.chain != id || h.prefix != id+"-" || h.stateDir != a.cfg.stateDir || h.validate == nil || h.resolve == nil || h.scan == nil || h.txOutSet == nil {
			t.Fatalf("%s bench host = %+v", id, h)
		}
	}

	a, _ := testApp(t, &rpcFixture{}, "")
	m := indexTestManager(t, indexTestChain(nil))
	host := benchTestHost(a.cfg.stateDir)
	host.chain, host.prefix = nodeLitecoin, "litecoin-"
	m.configureBench(host)
	a.chainUtxo = map[string]*utxoManager{nodeLitecoin: m}
	req := benchRequest{ID: benchTestID(21), Chain: nodeLitecoin, RequestedAt: time.Now().Unix(), Wallets: []benchWallet{{Name: "a", Key: genesisAddress}}, Repeat: 1}
	raw, _ := json.Marshal(req)
	if err := os.WriteFile(filepath.Join(a.cfg.stateDir, "litecoin-"+benchRequestName), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := a.startBackground(ctx)
	defer func() {
		cancel()
		<-done
	}()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(filepath.Join(a.cfg.stateDir, "litecoin-"+benchResultName)); err == nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("the background workers never ran the litecoin bench")
}
