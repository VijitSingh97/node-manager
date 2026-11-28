package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"testing"
)

func p2pkFamilyFollowed(t *testing.T, n *liveNode, chain *utxoChainParams) func(*testing.T) *utxoStore {
	t.Helper()
	s, err := openUtxoStoreFS(t.TempDir(), nil, chain.ReverseIndex)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	f := newUtxoFollower(s, n.rpc)
	f.chain = chain
	return func(t *testing.T) *utxoStore {
		t.Helper()
		if err := f.Step(context.Background()); err != nil {
			t.Fatal(err)
		}
		return s
	}
}

func (n *liveNode) unspentAmount(txid string, vout int) (uint64, bool) {
	var out struct {
		Value json.Number `json:"value"`
	}
	err := n.rpc.call(context.Background(), "gettxout", []any{txid, vout, false}, &out)
	if errors.As(err, new(rpcNoResultError)) {
		return 0, false
	}
	if err != nil {
		n.t.Fatalf("gettxout: %v", err)
	}
	var a rpcAmount
	if err := a.UnmarshalJSON([]byte(out.Value.String())); err != nil {
		n.t.Fatal(err)
	}
	return uint64(a), true
}

// p2pkFamilyLive plants compressed and uncompressed P2PK outputs beside a P2PKH output of the same key, then checks the address lookups against the node and spends one P2PK coin.
func p2pkFamilyLive(t *testing.T, n, w *liveNode, chain *utxoChainParams, id, miner string, indexes ...func(*testing.T) *utxoStore) {
	t.Helper()
	ctx := context.Background()
	n.do("generatetoaddress", 110, miner)
	var unspent []struct {
		Txid   string      `json:"txid"`
		Vout   int         `json:"vout"`
		Amount json.Number `json:"amount"`
	}
	if err := json.Unmarshal(w.do("listunspent", 1), &unspent); err != nil {
		t.Fatal(err)
	}
	var fundTxid string
	var fundVout int
	var fundSats int64
	for _, u := range unspent {
		var a rpcAmount
		if err := a.UnmarshalJSON([]byte(u.Amount.String())); err != nil {
			t.Fatal(err)
		}
		if int64(a) >= 20e8 {
			fundTxid, fundVout, fundSats = u.Txid, u.Vout, int64(a)
			break
		}
	}
	if fundTxid == "" {
		t.Fatal("no coin to fund the planted outputs")
	}
	compressed, uncompressed := p2pkScript(t, p2pkKeyGCompressed), p2pkScript(t, p2pkKeyGUncompressed)
	pkhC, pkhU := p2pkAddr(t, p2pkAddrGCompressed), p2pkAddr(t, p2pkAddrGUncompressed)
	outs := []any{
		map[string]any{w.str("getnewaddress"): bchAmount(1e8)},
		map[string]any{w.str("getnewaddress"): bchAmount(2e8)},
		map[string]any{w.str("getnewaddress"): bchAmount(3e8)},
		map[string]any{w.str("getnewaddress"): bchAmount(fundSats - 6e8 - 10_000)},
	}
	raw := w.str("createrawtransaction", []any{map[string]any{"txid": fundTxid, "vout": fundVout}}, outs)
	planted := w.sendRaw(withOutputScripts(t, raw, map[int][]byte{0: compressed, 1: uncompressed, 2: pkhC}))
	n.do("generatetoaddress", 1, miner)

	check := func(when string, index func(*testing.T) *utxoStore) {
		t.Helper()
		s := index(t)
		for _, c := range []struct {
			name   string
			script []byte
			outs   []int
		}{{"compressed", pkhC, []int{0, 2}}, {"uncompressed", pkhU, []int{1}}} {
			want := map[string]uint64{}
			for _, vout := range c.outs {
				amount, ok := n.unspentAmount(planted, vout)
				if !ok {
					continue
				}
				want[fmt.Sprintf("%s:%d", planted, vout)] = amount
			}
			got := lookupAmounts(t, s, hex.EncodeToString(c.script))
			if !maps.Equal(got, want) {
				t.Fatalf("%s: the %s key's address is indexed as %v, the node holds %v", when, c.name, got, want)
			}
		}
		own := lookupAmounts(t, s, hex.EncodeToString(pkhC))
		if direct := lookupAmounts(t, s, hex.EncodeToString(compressed)); !maps.Equal(direct, own) {
			t.Fatalf("%s: the P2PK script and the P2PKH script of one key must share a lookup: %v and %v", when, direct, own)
		}
		coins, err := s.Lookup([][]byte{pkhC, pkhU})
		if err != nil {
			t.Fatal(err)
		}
		_, flagged := splitP2PKCoins(coins)
		if len(flagged) == 0 {
			t.Fatalf("%s: no coin is flagged P2PK", when)
		}
		if _, err := confirmP2PKCoins(ctx, n.rpc, flagged, n.str("getbestblockhash")); err != nil {
			t.Fatalf("%s: the node does not confirm the P2PK coins: %v", when, err)
		}
		verifyLiveStore(t, n, id, chain, s)
	}
	for i, index := range indexes {
		check(fmt.Sprintf("index %d after planting", i), index)
	}
	if scanned := n.scan(hex.EncodeToString(pkhC)); len(scanned) != 1 {
		t.Fatalf("scantxoutset on the P2PKH script sees %v, which must be the P2PKH output alone", scanned)
	}

	raw = w.str("createrawtransaction", []any{map[string]any{"txid": planted, "vout": 0}}, []any{map[string]any{w.str("getnewaddress"): bchAmount(1e8 - 10_000)}})
	var signed struct {
		Hex      string `json:"hex"`
		Complete bool   `json:"complete"`
	}
	prev := []any{map[string]any{"txid": planted, "vout": 0, "scriptPubKey": hex.EncodeToString(compressed), "amount": json.Number("1.00000000")}}
	if err := json.Unmarshal(n.do("signrawtransactionwithkey", raw, []string{p2pkLiveWIFCompressed}, prev), &signed); err != nil || !signed.Complete {
		t.Fatalf("signing the P2PK spend: %v %+v", err, signed)
	}
	n.str("sendrawtransaction", signed.Hex)
	n.do("generatetoaddress", 1, miner)
	if _, ok := n.unspentAmount(planted, 0); ok {
		t.Fatal("the P2PK output is still unspent after its spend was mined")
	}
	for i, index := range indexes {
		check(fmt.Sprintf("index %d after spending a P2PK coin", i), index)
	}
}

func TestUtxoP2PKBitcoinCashRegtestLive(t *testing.T) {
	n := startBitcoinCashNode(t, "-acceptnonstdtxn=1")
	n.do("createwallet", "w")
	w := n.wallet("w")
	miner, _ := w.cashScript()
	p2pkFamilyLive(t, n, w, utxoBitcoinCash, nodeBitcoinCash, miner, p2pkFamilyFollowed(t, n, utxoBitcoinCash))
}

func TestUtxoP2PKLitecoinRegtestLive(t *testing.T) {
	n := startLitecoinNode(t)
	n.do("createwallet", "w")
	w := n.wallet("w")
	miner := w.str("getnewaddress")
	seq := 0
	dumped := func(t *testing.T) *utxoStore {
		t.Helper()
		seq++
		return n.reseed(t, fmt.Sprintf("p2pk-%d.dat", seq))
	}
	p2pkFamilyLive(t, n, w, utxoLitecoin, nodeLitecoin, miner, p2pkFamilyFollowed(t, n, utxoLitecoin), dumped)
}
