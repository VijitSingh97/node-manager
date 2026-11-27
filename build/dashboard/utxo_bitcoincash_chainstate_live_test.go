package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"maps"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func startBitcoinCashNodeWithData(t *testing.T, data string) *liveNode {
	t.Helper()
	image := os.Getenv("BCH_REGTEST_IMAGE")
	if image == "" {
		t.Skip("set BCH_REGTEST_IMAGE to a node-manager-bitcoincash image to run against a regtest Bitcoin Cash Node")
	}
	n := runLiveNode(t, "18443", "--user", fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()), "-v", data+":/data", "--entrypoint", "bitcoind", image, "-regtest", "-datadir=/data", "-fallbackfee=0.00001",
		"-printtoconsole=0", "-rpcuser=test", "-rpcpassword=test", "-rpcbind=0.0.0.0", "-rpcallowip=0.0.0.0/0", "-upgrade9activationheight=200")
	_ = n.rpc.call(context.Background(), "loadwallet", []any{"w"}, new(json.RawMessage))
	return n
}

// TestUtxoBitcoinCashChainstateSeedRegtestLive seeds from a stopped node's chainstate copy as the pruned-node path does, follows new blocks from the snapshot tip, and compares with scantxoutset.
func TestUtxoBitcoinCashChainstateSeedRegtestLive(t *testing.T) {
	data := t.TempDir()
	if err := os.Chmod(data, 0o777); err != nil {
		t.Fatal(err)
	}
	n := startBitcoinCashNodeWithData(t, data)
	n.do("createwallet", "w")
	w := n.wallet("w")
	mine, mineScript := w.cashScript()
	n.do("generatetoaddress", 230, mine)
	scripts := []string{mineScript}
	watched, watchedScript := w.cashScript()
	scripts = append(scripts, watchedScript)
	w.do("sendtoaddress", watched, "2.5")

	holder, holderScript := w.cashScript()
	scripts = append(scripts, holderScript)
	var unspent []struct {
		Txid   string      `json:"txid"`
		Vout   int         `json:"vout"`
		Amount json.Number `json:"amount"`
	}
	n.do("generatetoaddress", 1, mine)
	if err := json.Unmarshal(w.do("listunspent", 1), &unspent); err != nil {
		t.Fatal(err)
	}
	var category string
	var funds rpcAmount
	moved := false
	for _, u := range unspent {
		if v := new(rpcAmount); u.Vout == 0 && v.UnmarshalJSON([]byte(u.Amount.String())) == nil && int64(*v) >= 1e7 {
			category, funds = u.Txid, *v
			break
		}
	}
	if category == "" {
		t.Fatal("no coin to mint a token from")
	}
	change, _ := w.cashScript()
	mint := w.tokenTx([]any{map[string]any{"txid": category, "vout": 0}}, []any{
		map[string]any{holder: map[string]any{"amount": bchAmount(1000), "tokenData": map[string]any{"category": category, "amount": 1000}}},
		map[string]any{change: bchAmount(int64(funds) - 1000 - 10_000)}})
	n.do("generatetoaddress", 1, mine)

	stopDogecoinNode(t, n)
	n = startBitcoinCashNodeWithData(t, data)
	n.do("generatetoaddress", 1, mine)
	snapshot := n.height()

	stopDogecoinNode(t, n)
	copyChainstateLikeTheHost(t, filepath.Join(data, "regtest", "chainstate"), filepath.Join(data, utxoChainstateCopyName))
	if tables, _ := filepath.Glob(filepath.Join(data, utxoChainstateCopyName, "*.sst")); len(tables) == 0 {
		t.Fatal("the chainstate copy has no table, so the hardlinked path is not exercised")
	}
	n = startBitcoinCashNodeWithData(t, data)
	w = n.wallet("w")

	m := newUtxoManager(t.TempDir(), nil, n.rpc, log.New(io.Discard, "", 0), utxoBitcoinCash)
	t.Cleanup(func() { _ = m.currentStore().Close() })
	m.configureSeed(utxoSeedHost{prefix: "bitcoincash-", stateDir: t.TempDir(), dataDir: data, lock: &sync.Mutex{},
		hold: func() bool { return true }, release: func() {}, helper: func() bool { return true }})
	m.temp = func() (float64, error) { return 50, nil }
	m.guards = func(ctx context.Context) seedGuardInput {
		return seedGuardInput{State: m.Status().State, TempC: 50, CPUs: 0.5, FreeBytes: 1 << 40, Chain: blockchainInfo{Chain: "regtest", Pruned: true}}
	}
	ctx := context.Background()
	m.tick(ctx)
	m.seedRequested(ctx, newRequestID())
	if st := m.Status(); st.LastSeed == nil || st.LastSeed.Outcome != "ok" || m.seed.Mode != seedModeChainstate {
		t.Fatalf("seed from the chainstate copy: %+v mode %q", st, m.seed.Mode)
	}
	if h, _, _, _ := m.currentStore().Tip(); int(h) != snapshot {
		t.Fatalf("index tip %d after the seed, the copy was taken at %d", h, snapshot)
	}
	compare := func(when string) {
		t.Helper()
		coins := 0
		for _, script := range scripts {
			got, want := lookupAmounts(t, m.currentStore(), script), n.scan(script)
			if !maps.Equal(got, want) {
				t.Fatalf("%s: script %s: index %v, scantxoutset %v", when, script, got, want)
			}
			coins += len(want)
		}
		t.Logf("%s: index matched scantxoutset on %d coins across %d scripts", when, coins, len(scripts))
	}
	compare("after the seed")
	if len(lookupAmounts(t, m.currentStore(), holderScript)) != 1 {
		t.Fatal("the token output is not indexed for its holder")
	}

	w.do("sendtoaddress", watched, "1.5")
	if err := json.Unmarshal(w.do("listunspent", 1), &unspent); err != nil {
		t.Fatal(err)
	}
	for _, u := range unspent {
		if v := new(rpcAmount); u.Txid != mint && v.UnmarshalJSON([]byte(u.Amount.String())) == nil && int64(*v) >= 1e7 {
			mover, moverScript := w.cashScript()
			scripts = append(scripts, moverScript)
			w.tokenTx([]any{map[string]any{"txid": mint, "vout": 0}, map[string]any{"txid": u.Txid, "vout": u.Vout}}, []any{
				map[string]any{mover: map[string]any{"amount": bchAmount(1000), "tokenData": map[string]any{"category": category, "amount": 400}}},
				map[string]any{change: bchAmount(int64(*v) - 10_000)}})
			moved = true
			break
		}
	}
	if !moved {
		t.Fatal("no coin to fund the token move")
	}
	n.do("generatetoaddress", 3, mine)
	m.tick(ctx)
	if st := m.Status(); st.State != utxoStateReady || int(*st.IndexTip) != n.height() {
		t.Fatalf("after following: %+v", st)
	}
	compare("after following")
	verifyLiveStore(t, n, nodeBitcoinCash, utxoBitcoinCash, m.currentStore())
	benchHostMatchesScan(t, n, nodeBitcoinCash, utxoBitcoinCash, []string{mine, watched}, []string{mineScript, watchedScript})
}
