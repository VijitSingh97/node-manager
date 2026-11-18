package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func startBitcoinCashNode(t *testing.T, extra ...string) *liveNode {
	t.Helper()
	image := os.Getenv("BCH_REGTEST_IMAGE")
	if image == "" {
		t.Skip("set BCH_REGTEST_IMAGE to a node-manager-bitcoincash image to run against a regtest Bitcoin Cash Node")
	}
	args := append([]string{"--entrypoint", "bitcoind", image, "-regtest", "-datadir=/tmp", "-fallbackfee=0.00001",
		"-printtoconsole=0", "-rpcuser=test", "-rpcpassword=test", "-rpcbind=0.0.0.0", "-rpcallowip=0.0.0.0/0"}, extra...)
	return runLiveNode(t, "18443", args...)
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
	return n.sendRaw(n.str("createrawtransaction", inputs, outputs))
}

func (n *liveNode) sendRaw(raw string) string {
	var signed struct {
		Hex      string `json:"hex"`
		Complete bool   `json:"complete"`
	}
	if err := json.Unmarshal(n.do("signrawtransactionwithwallet", raw), &signed); err != nil || !signed.Complete {
		n.t.Fatalf("signing a transaction: %v", err)
	}
	return n.str("sendrawtransaction", signed.Hex)
}

// withOutputScripts rewrites chosen output scripts of an unsigned raw transaction, to plant prefixes the wallet would never write.
func withOutputScripts(t *testing.T, rawHex string, scripts map[int][]byte) string {
	raw, _ := hex.DecodeString(rawHex)
	r := &rawReader{b: raw}
	r.take(4)
	for range r.compact() {
		r.take(36)
		r.take(r.compact())
		r.take(4)
	}
	out := append([]byte{}, raw[:r.off]...)
	outs := r.compact()
	out = append(out, raw[len(out):r.off]...)
	for i := range int(outs) {
		out = append(out, r.take(8)...)
		script := r.take(r.compact())
		if s, ok := scripts[i]; ok {
			script = s
		}
		out = append(out, byte(len(script)))
		out = append(out, script...)
	}
	out = append(out, raw[r.off:]...)
	if r.err != nil {
		t.Fatal(r.err)
	}
	return hex.EncodeToString(out)
}

// TestUtxoBitcoinCashRawReplayRegtestLive checks the raw parser against the node's own verbose view of every block, then replays through the seed path.
func TestUtxoBitcoinCashRawReplayRegtestLive(t *testing.T) {
	n := startBitcoinCashNode(t, "-acceptnonstdtxn=1", "-upgrade9activationheight=200")
	ctx := context.Background()
	n.do("createwallet", "w")
	w := n.wallet("w")
	mine, mineScript := w.cashScript()
	n.do("generatetoaddress", 110, mine)
	scripts := []string{mineScript}
	var unspent []struct {
		Txid   string      `json:"txid"`
		Vout   int         `json:"vout"`
		Amount json.Number `json:"amount"`
	}
	sats := func(a json.Number) int64 {
		var v rpcAmount
		if err := v.UnmarshalJSON([]byte(a.String())); err != nil {
			t.Fatal(err)
		}
		return int64(v)
	}
	coin := func(vout0 bool) (string, int, int64) {
		t.Helper()
		if err := json.Unmarshal(w.do("listunspent", 1), &unspent); err != nil {
			t.Fatal(err)
		}
		for _, u := range unspent {
			if (!vout0 || u.Vout == 0) && sats(u.Amount) >= 1e7 {
				return u.Txid, u.Vout, sats(u.Amount)
			}
		}
		t.Fatal("no spendable coin")
		return "", 0, 0
	}

	// Before activation: every token shape and a malformed prefix, planted by rewriting the scripts.
	change, changeScript := w.cashScript()
	scripts = append(scripts, changeScript)
	txid, vout, amount := coin(false)
	category, _ := hex.DecodeString(txid)
	planted := map[int][]byte{}
	var plantedOuts []any
	var oneCoin []string
	fungible, nftBare, nftCommitment, fungibleAndNFT := []byte{0x10, 5}, []byte{0x20}, []byte{0x60, 2, 0xbe, 0xef}, []byte{0x71, 1, 0xaa, 7}
	for i, field := range [][]byte{fungible, nftBare, nftCommitment, fungibleAndNFT, nil} {
		a, s := w.cashScript()
		p2pkh, _ := hex.DecodeString(s)
		planted[i] = append(append(append([]byte{cashTokenPrefix}, category...), field...), p2pkh...)
		if field == nil {
			planted[i] = append([]byte{cashTokenPrefix, 0x00, 0x01}, p2pkh...)
			s = hex.EncodeToString(planted[i])
		}
		scripts, oneCoin = append(scripts, s), append(oneCoin, s)
		plantedOuts = append(plantedOuts, map[string]any{a: "0.10000000"})
	}
	rawTx := w.str("createrawtransaction", []any{map[string]any{"txid": txid, "vout": vout}},
		append(plantedOuts, map[string]any{change: bchAmount(amount - 5e7 - 10_000)}))
	w.sendRaw(withOutputScripts(t, rawTx, planted))
	n.do("generatetoaddress", 95, mine)

	// After activation: every token shape the wallet can write, then a spend that moves tokens on.
	holders := map[string]map[string]any{}
	var holderAddrs []string
	var ftMint, ftCategory string
	for i, nft := range []map[string]any{nil, {"capability": "none", "commitment": "beef"}, {"capability": "mutable"}, {"capability": "minting", "commitment": "cafe"}} {
		a, s := w.cashScript()
		scripts = append(scripts, s)
		if i > 0 {
			oneCoin = append(oneCoin, s)
		}
		token := map[string]any{}
		if nft == nil || nft["capability"] == "minting" {
			token["amount"] = 1000
		}
		if nft != nil {
			token["nft"] = nft
		}
		holders[a], holderAddrs = token, append(holderAddrs, a)
	}
	for _, a := range holderAddrs {
		gen, _, funds := coin(true)
		token := holders[a]
		token["category"] = gen
		mint := w.tokenTx([]any{map[string]any{"txid": gen, "vout": 0}},
			[]any{map[string]any{a: map[string]any{"amount": bchAmount(1000), "tokenData": token}}, map[string]any{change: bchAmount(funds - 1000 - 10_000)}})
		if ftMint == "" {
			ftMint, ftCategory = mint, gen
		}
		n.do("generatetoaddress", 1, mine)
	}
	mover, moverScript := w.cashScript()
	scripts = append(scripts, moverScript)
	feeTxid, feeVout, feeSats := coin(false)
	w.tokenTx([]any{map[string]any{"txid": ftMint, "vout": 0}, map[string]any{"txid": feeTxid, "vout": feeVout}}, []any{
		map[string]any{mover: map[string]any{"amount": bchAmount(1000), "tokenData": map[string]any{"category": ftCategory, "amount": 400}}},
		map[string]any{change: bchAmount(feeSats - 10_000)}})
	n.do("generatetoaddress", 1, mine)
	oneCoin = append(oneCoin, moverScript)

	// After activation a malformed prefix breaks consensus, so the node must refuse it.
	badTxid, badVout, badSats := coin(false)
	badAddr, badScript := w.cashScript()
	badP2pkh, _ := hex.DecodeString(badScript)
	rawBad := w.str("createrawtransaction", []any{map[string]any{"txid": badTxid, "vout": badVout}},
		[]any{map[string]any{badAddr: "0.10000000"}, map[string]any{change: bchAmount(badSats - 1e7 - 10_000)}})
	accept := func(raw string) (bool, string) {
		t.Helper()
		var signed struct {
			Hex string `json:"hex"`
		}
		var res []struct {
			Allowed bool   `json:"allowed"`
			Reason  string `json:"reject-reason"`
		}
		if err := json.Unmarshal(w.do("signrawtransactionwithwallet", raw), &signed); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(n.do("testmempoolaccept", []string{signed.Hex}), &res); err != nil || len(res) != 1 {
			t.Fatalf("testmempoolaccept: %v", err)
		}
		return res[0].Allowed, res[0].Reason
	}
	if ok, why := accept(rawBad); !ok {
		t.Fatalf("the transaction without a prefix was refused: %s", why)
	}
	if ok, why := accept(withOutputScripts(t, rawBad, map[int][]byte{0: append([]byte{cashTokenPrefix, 0x00, 0x01}, badP2pkh...)})); ok || !strings.Contains(why, "bad-txns-vout-tokenprefix") {
		t.Fatalf("after activation a malformed token prefix should fail bad-txns-vout-tokenprefix: allowed %v, %q", ok, why)
	} else {
		t.Logf("after activation the node refuses a malformed token prefix (%s); the same transaction without it is accepted", why)
	}

	compared := 0
	for h := 0; h <= n.height(); h++ {
		hash := n.str("getblockhash", h)
		var verbose utxoV3Block
		if err := json.Unmarshal(n.do("getblock", hash, 3), &verbose); err != nil {
			t.Fatal(err)
		}
		want, err := convertUtxoBlock(verbose, false)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := hex.DecodeString(n.str("getblock", hash, 0))
		got, err := parseRawBlock(raw, uint32(h), utxoBitcoinCash)
		if err != nil {
			t.Fatalf("height %d: %v", h, err)
		}
		if got.Hash != want.Hash || got.PrevHash != want.PrevHash && h > 0 || len(got.Creates) != len(want.Creates) || len(got.Spends) != len(want.Spends) {
			t.Fatalf("height %d: raw block %d creates %d spends, verbose %d and %d", h, len(got.Creates), len(got.Spends), len(want.Creates), len(want.Spends))
		}
		for i, c := range got.Creates {
			if w := want.Creates[i]; c.utxoOutpoint != w.utxoOutpoint || c.Amount != w.Amount || hex.EncodeToString(c.Script) != hex.EncodeToString(w.Script) {
				t.Fatalf("height %d output %x:%d: raw %x %d, verbose %x %d", h, c.Txid, c.Vout, c.Script, c.Amount, w.Script, w.Amount)
			}
		}
		for i, s := range got.Spends {
			if s.utxoOutpoint != want.Spends[i].utxoOutpoint {
				t.Fatalf("height %d spend %d differs", h, i)
			}
		}
		compared += len(got.Creates)
	}
	t.Logf("the raw parser matched getblock verbosity 3 on all %d outputs of %d blocks", compared, n.height()+1)

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
		t.Logf("%s: index matched scantxoutset on %d coins across %d scripts", when, checked, len(scripts))
	}
	compare("after the replay")
	for _, s := range oneCoin {
		if got := lookupAmounts(t, m.currentStore(), s); len(got) != 1 {
			t.Fatalf("token output not indexed for its holder script %s: %v", s, got)
		}
	}

	n.invalidate(n.str("getbestblockhash"))
	other, otherScript := w.cashScript()
	scripts = append(scripts, otherScript)
	w.do("sendtoaddress", other, "0.5")
	n.do("generatetoaddress", 2, mine)
	m.tick(ctx)
	if st := m.Status(); st.State != utxoStateReady || int(*st.IndexTip) != n.height() {
		t.Fatalf("after the reorg: %+v", st)
	}
	compare(fmt.Sprintf("after a one-block reorg to height %d", n.height()))
}
