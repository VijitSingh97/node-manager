package main

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"testing"
	"time"
)

func TestPrunedWalletIndexMatchesScantxoutsetRegtestLive(t *testing.T) {
	n := startLiveNode(t)
	n.do("createwallet", "w")
	mine := n.str("getnewaddress", "", "bech32")
	n.do("generatetoaddress", 101, mine)
	desc := "wpkh(" + regtestKey(t) + "/0/*)"
	var info struct {
		Descriptor string `json:"descriptor"`
	}
	if err := json.Unmarshal(n.do("getdescriptorinfo", desc), &info); err != nil {
		t.Fatal(err)
	}
	var derived []string
	if err := json.Unmarshal(n.do("deriveaddresses", info.Descriptor, []int{0, 1500}), &derived); err != nil {
		t.Fatal(err)
	}
	for _, i := range []int{3, 3, 700, 1500} {
		n.do("sendtoaddress", derived[i], "1.25")
		n.do("generatetoaddress", 1, mine)
	}

	dir := t.TempDir()
	store, err := openUtxoStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := newUtxoFollower(store, n.rpc).Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	m := newUtxoManager(dir, nil, n.rpc, log.New(io.Discard, "", 0))
	t.Cleanup(func() { _ = m.store.Close() })
	m.tick(context.Background())
	if st := m.Status().State; st != utxoStateReady {
		t.Fatalf("index state %s", st)
	}

	cfg := config{rpcURL: n.rpc.url, rpcUser: "test", rpcPassword: "test", dashboardPassword: "secret", stateDir: t.TempDir(), dataDir: t.TempDir(), version: "test"}
	a, err := newApp(cfg, &rpcClient{url: n.rpc.url, user: "test", password: "test", client: &http.Client{Timeout: 60 * time.Second}}, "index.html", "static", log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatal(err)
	}
	a.utxo = m
	entry, err := a.watch.Add("Regtest", desc, "")
	if err != nil {
		t.Fatal(err)
	}
	var chain blockchainInfo
	if err := n.rpc.call(context.Background(), "getblockchaininfo", nil, &chain); err != nil {
		t.Fatal(err)
	}
	chain.Pruned = true
	a.scanner.refresh(a.watch.Entries(), chain)
	cache, _, ok := a.watch.scanSnapshot(entry)
	if !ok || cache.Source != scanSourceIndex || cache.BestBlock != chain.BestBlockHash {
		t.Fatalf("index cache = %#v ok=%v", cache, ok)
	}

	var scan scanTxOutSetResult
	object := map[string]any{"desc": info.Descriptor, "range": []int{0, 999}}
	if err := n.rpc.call(context.Background(), "scantxoutset", []any{"start", []any{object}}, &scan); err != nil {
		t.Fatal(err)
	}
	want, err := validatedScanBalance(scan)
	if err != nil {
		t.Fatal(err)
	}
	if cache.BTC != want || len(cache.Outpoints) != len(scan.Unspents) || want != 3.75 {
		t.Fatalf("index %v BTC over %d outpoints, scantxoutset %v BTC over %d", cache.BTC, len(cache.Outpoints), want, len(scan.Unspents))
	}
	byOutpoint := map[string]int64{}
	for _, u := range scan.Unspents {
		sats, _ := btcSatoshis(u.Amount)
		byOutpoint[u.TxID+":"+stringValue(u.Vout)] = sats
	}
	for _, o := range cache.Outpoints {
		if byOutpoint[o.TxID+":"+stringValue(o.Vout)] != o.Sats {
			t.Fatalf("outpoint %s:%d differs from scantxoutset", o.TxID, o.Vout)
		}
	}
}
