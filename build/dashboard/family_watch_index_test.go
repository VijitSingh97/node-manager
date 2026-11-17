package main

import (
	"encoding/json"
	"fmt"
	"io"
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
