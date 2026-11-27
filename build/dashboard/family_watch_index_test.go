package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"strings"
	"testing"
	"time"
)

func scantxoutsetCalls(fixture *familyScanRPCFixture) int {
	n := 0
	for {
		select {
		case method := <-fixture.calls:
			if method == "scantxoutset" {
				n++
			}
		default:
			return n
		}
	}
}

func TestLitecoinWatchServesBalancesFromTheIndex(t *testing.T) {
	now := time.Unix(2_000_000_000, 0)
	a := familyWatchEntry{ID: "wallet-a", Address: "address-a"}
	b := familyWatchEntry{ID: "wallet-b", Address: "address-b"}
	manager, store, fixture := newFamilyScanTest(t, now, a, b)
	index := utxoMgrNew(t, t.TempDir(), nil, newFakeUtxoNode(utxoMgrChain(2, "b")), io.Discard)
	store.index = index
	tip100, _ := utxoRevHex(familyScanHash100)
	tip101, _ := utxoRevHex(familyScanHash101)
	coin := func(vout uint32, amount uint64) utxoCreate {
		return utxoCreate{utxoOutpoint: utxoOutpoint{Txid: utxoTestTxid(1), Vout: vout}, Script: []byte(a.Address), Amount: amount}
	}
	if err := index.currentStore().ApplyBlock(utxoBlock{Height: 100, Hash: tip100, Creates: []utxoCreate{coin(0, 100_000_000), coin(1, 50_000_000)}}); err != nil {
		t.Fatal(err)
	}
	index.set(utxoStateReady, "")

	manager.scanNode(t.Context(), nodeLitecoin, store)
	manager.scanNode(t.Context(), nodeLitecoin, store)
	if calls := scantxoutsetCalls(fixture); calls != 0 {
		t.Fatalf("a ready index still walked the coin set %d times", calls)
	}
	if got := store.balances[a.ID]; got.Amount != "150000000" || got.BestBlock != familyScanHash100 || len(got.History) != 1 {
		t.Fatalf("a = %+v", got)
	}
	if got := store.balances[b.ID]; got.Amount != "0" || len(got.History) != 1 {
		t.Fatalf("b = %+v", got)
	}

	if err := index.currentStore().ApplyBlock(utxoBlock{Height: 101, Hash: tip101, PrevHash: tip100,
		Spends: []utxoSpend{{utxoOutpoint: coin(0, 0).utxoOutpoint, Script: []byte(a.Address)}}}); err != nil {
		t.Fatal(err)
	}
	fixture.update(func(f *familyScanRPCFixture) {
		f.chainHeight, f.chainHash = 101, familyScanHash101
		f.canonicalHashes[101] = familyScanHash101
	})
	manager.scanNode(t.Context(), nodeLitecoin, store)
	if got := store.balances[a.ID]; got.Amount != "50000000" || got.ScanHeight != 101 || len(got.History) != 1 || got.History[0].Amount != formatFamilyWatchAmount("50000000") {
		t.Fatalf("a spend within the hour did not update that hour's point: a = %+v", got)
	}
	if got := store.balances[b.ID]; len(got.History) != 1 {
		t.Fatalf("an unchanged balance gained a history point: %+v", got)
	}

	fixture.update(func(f *familyScanRPCFixture) {
		f.chainHeight, f.chainHash = 102, familyScanHash102
		f.scanHeight, f.scanHash = 102, familyScanHash102
		f.canonicalHashes[102] = familyScanHash102
	})
	store.mu.Lock()
	store.states[b.ID] = "stale"
	store.mu.Unlock()
	manager.scanNode(t.Context(), nodeLitecoin, store)
	if calls, scans := scantxoutsetCalls(fixture), fixture.scans(); calls != 1 || len(scans) != 1 || !strings.Contains(scans[0], b.Address) {
		t.Fatalf("an index behind the node did not fall back to one scan of the due wallet: %d calls, %v", calls, scans)
	}
}

func TestFamilyWatchBatchesEveryDueWalletIntoOneScan(t *testing.T) {
	now := time.Unix(2_000_000_000, 0)
	var wallets []familyWatchEntry
	for i := range 3 {
		wallets = append(wallets, familyWatchEntry{ID: fmt.Sprintf("wallet-%d", i), Address: fmt.Sprintf("address-%d", i)})
	}
	manager, store, fixture := newFamilyScanTest(t, now, wallets...)
	manager.scanNode(t.Context(), nodeLitecoin, store)
	if calls, scans := scantxoutsetCalls(fixture), fixture.scans(); calls != 1 || len(scans) != 3 {
		t.Fatalf("%d scantxoutset calls covering %v, want one call for three wallets", calls, scans)
	}
	for _, wallet := range wallets {
		if got := store.balances[wallet.ID]; got.Amount != "125000000" || store.states[wallet.ID] != "ok" {
			t.Fatalf("%s = %+v %s", wallet.ID, got, store.states[wallet.ID])
		}
	}
}

func TestLitecoinAndBitcoinCashWatchesHoldFiftyWallets(t *testing.T) {
	a := &app{cfg: config{stateDir: t.TempDir(), nodes: map[string]nodeConfig{}, dogecoinWatchWallet: true}}
	m := newFamilyWatchManager(a)
	ltc, bch := m.stores[nodeLitecoin], m.stores[nodeBitcoinCash]
	if ltc.maxWallets != 50 || bch.maxWallets != 50 || m.stores[nodeDogecoin].maxWallets != 25 || ltc.rpc.maxBody != 64<<20 {
		t.Fatalf("caps: litecoin %d, bitcoin cash %d", ltc.maxWallets, bch.maxWallets)
	}
	for i := len(ltc.wallets); i < 50; i++ {
		if _, err := ltc.add(fmt.Sprintf("w%02d", i), fmt.Sprintf("address-%02d", i)); err != nil {
			t.Fatalf("wallet %d: %v", i+1, err)
		}
	}
	if _, err := ltc.add("w51", "one-too-many"); err == nil || !strings.Contains(err.Error(), "At most 50") {
		t.Fatalf("51st wallet: %v", err)
	}
	roster, _ := json.Marshal(familyWatchFile{Wallets: ltc.wallets})
	var loaded []familyWatchEntry
	if !decodeFamilyWatchRoster(roster, &loaded, defaultFamilyWatchEntry(nodeLitecoin), 50) || decodeFamilyWatchRoster(roster, &loaded, defaultFamilyWatchEntry(nodeDogecoin), 25) {
		t.Fatal("a 50-wallet roster loads only under the 50 cap")
	}
}

func TestDogecoinIndexOnlyWatchWaitsForAnIndexOneBlockBehind(t *testing.T) {
	now := time.Unix(2_000_000_000, 0)
	_, store, fixture := newFamilyScanTest(t, now, familyWatchEntry{ID: "wallet-a", Address: "address-a"})
	store.backend = dogecoinIndexFamilyWatchBackend()
	index := utxoMgrNew(t, t.TempDir(), nil, newFakeUtxoNode(utxoMgrChain(2, "b")), io.Discard)
	store.index = index
	index.set(utxoStateReady, "")
	index.setTip(100)
	if _, ok := store.backend.ready(t.Context(), store, now.Unix()); !ok {
		t.Fatal("an index at the node's tip was not ready to serve")
	}
	fixture.update(func(f *familyScanRPCFixture) {
		f.chainHeight, f.chainHash = 101, familyScanHash101
		f.canonicalHashes[101] = familyScanHash101
	})
	if _, ok := store.backend.ready(t.Context(), store, now.Unix()); ok {
		t.Fatal("an index one block behind the node let the wallets scan and fail")
	}
}

func dogecoinSkipsExample(t *testing.T, watchWallet bool) bool {
	t.Helper()
	a := &app{cfg: config{stateDir: t.TempDir(), dogecoinWatchWallet: watchWallet, nodes: map[string]nodeConfig{
		nodeDogecoin: {ID: nodeDogecoin, Enabled: true, Active: true, ActiveSet: true, UTXOIndex: true, RPCURL: "http://127.0.0.1:1"}, nodeLitecoin: {ID: nodeLitecoin}, nodeBitcoinCash: {ID: nodeBitcoinCash},
	}}, log: log.New(io.Discard, "", 0), nodeHealth: make(map[string]nodeHealthSnapshot)}
	return newFamilyWatchManager(a).stores[nodeDogecoin].skipExample
}

func readyDogecoinIndexStore(t *testing.T, now *time.Time, skipExample bool, wallets ...familyWatchEntry) (*familyWatchManager, *familyWatchStore, *familyScanRPCFixture) {
	t.Helper()
	manager, store, fixture := newFamilyScanTest(t, *now, wallets...)
	store.now = func() time.Time { return *now }
	store.backend, store.skipExample = dogecoinIndexFamilyWatchBackend(), skipExample
	manager.app.cfg.nodes[nodeDogecoin] = nodeConfig{ID: nodeDogecoin, Enabled: true, Active: true, ActiveSet: true}
	manager.stores = map[string]*familyWatchStore{nodeDogecoin: store}
	index := utxoMgrNew(t, t.TempDir(), nil, newFakeUtxoNode(utxoMgrChain(2, "b")), io.Discard)
	store.index = index
	tip100, _ := utxoRevHex(familyScanHash100)
	coin := utxoCreate{utxoOutpoint: utxoOutpoint{Txid: utxoTestTxid(1), Vout: 0}, Script: []byte(wallets[0].Address), Amount: 100_000_000}
	if err := index.currentStore().ApplyBlock(utxoBlock{Height: 100, Hash: tip100, Creates: []utxoCreate{coin}}); err != nil {
		t.Fatal(err)
	}
	index.set(utxoStateReady, "")
	index.setTip(100)
	return manager, store, fixture
}

func TestDogecoinIndexOnlyServesAndRefreshesTheExample(t *testing.T) {
	now := time.Unix(2_000_000_000, 0)
	example := familyWatchEntry{ID: "wallet-example", Label: "Example", Address: "address-a", Example: true}
	manager, store, fixture := readyDogecoinIndexStore(t, &now, dogecoinSkipsExample(t, false), example)
	manager.scanNode(t.Context(), nodeDogecoin, store)
	views, _ := store.list(false)
	if v := views[0]; v.State != "ok" || v.ScanTime != now.Unix() || v.Amount == nil {
		t.Fatalf("example = %+v", v)
	}
	now = now.Add(time.Hour)
	fixture.update(func(f *familyScanRPCFixture) { f.tipTime = now.Add(-time.Minute).Unix() })
	manager.scanNode(t.Context(), nodeDogecoin, store)
	if views, _ = store.list(false); views[0].State != "ok" || views[0].ScanTime != now.Unix() {
		t.Fatalf("example not refreshed an hour later: %+v", views[0])
	}
}

func TestDogecoinWatchWalletNodeNeverRecordsTheExampleFromTheIndex(t *testing.T) {
	now := time.Unix(2_000_000_000, 0)
	example := familyWatchEntry{ID: "wallet-example", Label: "Example", Address: "address-a", Example: true}
	manager, store, _ := readyDogecoinIndexStore(t, &now, dogecoinSkipsExample(t, true), example)
	manager.scanNode(t.Context(), nodeDogecoin, store)
	views, _ := store.list(false)
	if v := views[0]; v.State != "unscanned" || v.Amount != nil || len(store.balances) != 0 {
		t.Fatalf("example = %+v balances=%v", v, store.balances)
	}
}

func TestFamilyWatchConfirmsCachedBalancesOnTheFirstPassAfterARestart(t *testing.T) {
	now := time.Unix(2_000_000_000, 0)
	wallet := familyWatchEntry{ID: "wallet-a", Label: "A", Address: "address-a"}
	manager, store, _ := readyDogecoinIndexStore(t, &now, false, wallet)
	store.balances[wallet.ID] = familyWatchBalance{Amount: "100000000", ScanHeight: 100, ScanTime: now.Add(-10 * time.Minute).Unix(), BestBlock: familyScanHash100}
	if views, _ := store.list(false); views[0].State != "stale" {
		t.Fatalf("a cached balance with no state should read stale before the first pass: %+v", views[0])
	}
	manager.scanNode(t.Context(), nodeDogecoin, store)
	if views, _ := store.list(false); views[0].State != "ok" {
		t.Fatalf("first pass left %+v", views[0])
	}
}
