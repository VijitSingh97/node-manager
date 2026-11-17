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
	"testing"
)

func startBitcoinCashNode(t *testing.T) *liveNode {
	t.Helper()
	image := os.Getenv("BCH_REGTEST_IMAGE")
	if image == "" {
		t.Skip("set BCH_REGTEST_IMAGE to a node-manager-bitcoincash image to run against a regtest Bitcoin Cash Node")
	}
	return runLiveNode(t, "18443", "--entrypoint", "bitcoind", image, "-regtest", "-datadir=/tmp", "-fallbackfee=0.0001",
		"-printtoconsole=0", "-rpcuser=test", "-rpcpassword=test", "-rpcbind=0.0.0.0", "-rpcallowip=0.0.0.0/0")
}

func (n *liveNode) cashScript() (addr, script string) {
	addr = n.str("getnewaddress")
	var info struct {
		ScriptPubKey string `json:"scriptPubKey"`
	}
	if err := json.Unmarshal(n.do("validateaddress", addr), &info); err != nil || info.ScriptPubKey == "" {
		n.t.Fatalf("validateaddress %s: %v", addr, err)
	}
	return addr, info.ScriptPubKey
}

func bchAmount(sats int64) string { return fmt.Sprintf("%d.%08d", sats/1e8, sats%1e8) }

// tokenTx signs and sends a transaction built from raw inputs and outputs, with token outputs given as tokenData.
func (n *liveNode) tokenTx(inputs, outputs []any) string {
	raw := n.str("createrawtransaction", inputs, outputs)
	var signed struct {
		Hex      string `json:"hex"`
		Complete bool   `json:"complete"`
	}
	if err := json.Unmarshal(n.do("signrawtransactionwithwallet", raw), &signed); err != nil || !signed.Complete {
		n.t.Fatalf("signing a token transaction: %v", err)
	}
	return n.str("sendrawtransaction", signed.Hex)
}

func TestUtxoBitcoinCashReplayRegtestLive(t *testing.T) {
	n := startBitcoinCashNode(t)
	ctx := context.Background()
	mine, mineScript := n.cashScript()
	n.do("generatetoaddress", 101, mine)
	scripts := []string{mineScript}
	var addrs []string
	for range 6 {
		a, s := n.cashScript()
		addrs, scripts = append(addrs, a), append(scripts, s)
	}
	many := map[string]any{}
	for _, a := range addrs {
		many[a] = "0.25"
	}
	n.do("sendmany", "", many)
	n.do("generatetoaddress", 1, mine)

	var unspent []struct {
		Txid   string      `json:"txid"`
		Vout   int         `json:"vout"`
		Amount json.Number `json:"amount"`
	}
	if err := json.Unmarshal(n.do("listunspent", 1), &unspent); err != nil {
		t.Fatal(err)
	}
	genesis, funds := "", int64(0)
	for _, u := range unspent {
		// A token category is the txid of the outpoint spent at index 0.
		if u.Vout == 0 {
			var a rpcAmount
			if err := a.UnmarshalJSON([]byte(u.Amount.String())); err != nil {
				t.Fatal(err)
			}
			genesis, funds = u.Txid, int64(a)
			break
		}
	}
	if genesis == "" {
		t.Fatal("no spendable output at index 0")
	}
	holder, holderScript := n.cashScript()
	next, nextScript := n.cashScript()
	change, changeScript := n.cashScript()
	scripts = append(scripts, holderScript, nextScript, changeScript)
	const dust, fee = 1000, 10_000
	nft := map[string]any{"capability": "minting", "commitment": "beef"}
	minted := n.tokenTx([]any{map[string]any{"txid": genesis, "vout": 0}}, []any{
		map[string]any{holder: map[string]any{"amount": bchAmount(dust), "tokenData": map[string]any{"category": genesis, "amount": 1000, "nft": nft}}},
		map[string]any{change: bchAmount(funds - dust - fee)}})
	n.do("generatetoaddress", 1, mine)
	n.tokenTx([]any{map[string]any{"txid": minted, "vout": 0}, map[string]any{"txid": minted, "vout": 1}}, []any{
		map[string]any{next: map[string]any{"amount": bchAmount(dust), "tokenData": map[string]any{"category": genesis, "amount": 400, "nft": nft}}},
		map[string]any{change: bchAmount(funds - dust - 2*fee)}})
	n.do("generatetoaddress", 1, mine)

	state := t.TempDir()
	m := newUtxoManager(t.TempDir(), nil, n.rpc, log.New(io.Discard, "", 0), utxoBitcoinCash)
	t.Cleanup(func() { _ = m.currentStore().Close() })
	m.configureSeed(utxoSeedHost{prefix: "bitcoincash-", stateDir: state, hold: func() bool { return true }, release: func() {}})
	m.setSeedStatePath(filepath.Join(state, "bitcoincash-utxo-seed.json"))
	m.temp = func() (float64, error) { return 50, nil }
	m.guards = func(ctx context.Context) seedGuardInput {
		in := m.gatherSeedGuards(ctx)
		in.TempC, in.TempErr, in.CPUs, in.CPUErr, in.FreeBytes, in.FreeErr = 50, nil, 0.5, nil, 1<<40, nil
		return in
	}
	m.tick(ctx)
	m.maybeAutoSeed(ctx)
	if st := m.Status(); st.LastSeed == nil || st.LastSeed.Outcome != "ok" {
		t.Fatalf("replay: %+v", st)
	}
	m.tick(ctx)
	if st := m.Status(); st.State != utxoStateReady || int(*st.IndexTip) != n.height() {
		t.Fatalf("after the replay: %+v", st)
	}
	compare := func(when string) {
		t.Helper()
		checked := 0
		for _, script := range scripts {
			got, want := lookupAmounts(t, m.currentStore(), script), n.scan(script)
			if !maps.Equal(got, want) {
				t.Fatalf("%s: script %s: index %v, scantxoutset %v", when, script, got, want)
			}
			checked += len(want)
		}
		t.Logf("%s: index matched scantxoutset on %d coins", when, checked)
	}
	compare("after the replay")
	if got := lookupAmounts(t, m.currentStore(), nextScript); len(got) != 1 {
		t.Fatalf("the token output's BCH is not indexed for its holder: %v", got)
	}
	if got := lookupAmounts(t, m.currentStore(), holderScript); len(got) != 0 {
		t.Fatalf("a spent token output is still indexed: %v", got)
	}

	n.invalidate(n.str("getbestblockhash"))
	other, otherScript := n.cashScript()
	scripts = append(scripts, otherScript)
	n.do("sendtoaddress", other, "0.5")
	n.do("generatetoaddress", 2, mine)
	m.tick(ctx)
	if st := m.Status(); st.State != utxoStateReady || int(*st.IndexTip) != n.height() {
		t.Fatalf("after the reorg: %+v", st)
	}
	compare(fmt.Sprintf("after a one-block reorg to height %d", n.height()))
}
