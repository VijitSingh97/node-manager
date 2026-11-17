package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeStateFiles(t *testing.T, dir string, names ...string) {
	t.Helper()
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func stateFileExists(dir, name string) bool {
	_, err := os.Stat(filepath.Join(dir, name))
	return err == nil
}

func TestNodeDeletionMarkerPurgesOnlyThatCoinsWallets(t *testing.T) {
	a, _ := testApp(t, &rpcFixture{}, "")
	dir := a.cfg.stateDir
	address := "bc1qar0srrr7xfkvy5l643lydnw9re59gtzzwf5mdq"
	if _, err := a.watch.Add("Savings", address, ""); err != nil {
		t.Fatal(err)
	}
	a.watch.history.record(address, 1.5, time.Now())
	writeStateFiles(t, dir, "family_watch_litecoin.json", "family_watch_litecoin_cache.json", "family_watch_dogecoin.json", "family_watch_dogecoin_imported.json", "family_watch_bitcoincash.json", "monero_watch.json", "tari_watch.json", "watch_scan_cache.json")
	writeStateFiles(t, dir, "node-delete.litecoin")
	a.familyWatch.stores[nodeLitecoin].balances["x"] = familyWatchBalance{Amount: "1"}

	a.processNodeDeletions()

	if stateFileExists(dir, "node-delete.litecoin") || stateFileExists(dir, "family_watch_litecoin.json") || stateFileExists(dir, "family_watch_litecoin_cache.json") {
		t.Fatal("Litecoin wallets or their marker were kept")
	}
	if len(a.familyWatch.stores[nodeLitecoin].balances) != 0 || len(a.familyWatch.stores[nodeLitecoin].wallets) != 0 {
		t.Fatal("Litecoin wallets stayed in memory")
	}
	for _, name := range []string{"family_watch_dogecoin.json", "family_watch_dogecoin_imported.json", "family_watch_bitcoincash.json", "monero_watch.json", "tari_watch.json", "watch_scan_cache.json", "wallets.json", "balance_history.json"} {
		if !stateFileExists(dir, name) {
			t.Fatalf("%s was removed with Litecoin", name)
		}
	}
	if len(a.watch.Entries()) != 1 {
		t.Fatal("Bitcoin wallets were removed with Litecoin")
	}
}

func TestNodeDeletionRemovesEveryBitcoinWalletRecord(t *testing.T) {
	a, _ := testApp(t, &rpcFixture{}, "")
	dir := a.cfg.stateDir
	address := "bc1qar0srrr7xfkvy5l643lydnw9re59gtzzwf5mdq"
	if _, err := a.watch.Add("Savings", address, ""); err != nil {
		t.Fatal(err)
	}
	a.watch.history.record(address, 1.5, time.Now())
	writeStateFiles(t, dir, "watch_scan_cache.json", "node-delete.bitcoin", "family_watch_litecoin.json")
	if !stateFileExists(dir, "wallets.json") || !stateFileExists(dir, "balance_history.json") || len(a.watch.history.series(address)) != 1 {
		t.Fatal("test wallet and history were not saved")
	}

	a.processNodeDeletions()

	for _, name := range []string{"wallets.json", "balance_history.json", "watch_scan_cache.json", "node-delete.bitcoin"} {
		if stateFileExists(dir, name) {
			t.Fatalf("%s survived the Bitcoin deletion", name)
		}
	}
	if len(a.watch.Entries()) != 0 || len(a.watch.history.series(address)) != 0 || a.watch.Err() != nil {
		t.Fatal("Bitcoin wallets or history stayed in memory")
	}
	if !stateFileExists(dir, "family_watch_litecoin.json") {
		t.Fatal("Litecoin wallets were removed with Bitcoin")
	}
	if _, err := a.watch.Add("Again", address, ""); err != nil {
		t.Fatalf("a reinstalled Bitcoin cannot save wallets: %v", err)
	}
}

func TestNodeDeletionRemovesMoneroTariAndUnloadedFamilyWalletsAcrossRestarts(t *testing.T) {
	a, _ := testApp(t, &rpcFixture{}, "")
	dir := a.cfg.stateDir
	writeStateFiles(t, dir, "monero_watch.json", "tari_watch.json", "family_watch_zcash.json", "family_watch_zcash_cache.json", "node-delete.monero", "node-delete.tari", "node-delete.zcash")

	a.processNodeDeletions()

	for _, name := range []string{"monero_watch.json", "tari_watch.json", "family_watch_zcash.json", "family_watch_zcash_cache.json", "node-delete.monero", "node-delete.tari", "node-delete.zcash"} {
		if stateFileExists(dir, name) {
			t.Fatalf("%s survived", name)
		}
	}
}

func TestNodeDeletionKeepsItsMarkerUntilTheWalletsAreGone(t *testing.T) {
	a, _ := testApp(t, &rpcFixture{}, "")
	dir := a.cfg.stateDir
	if err := os.Mkdir(filepath.Join(dir, "family_watch_zcash.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeStateFiles(t, dir, "family_watch_zcash.json/keep", "node-delete.zcash")

	a.processNodeDeletions()
	if !stateFileExists(dir, "node-delete.zcash") {
		t.Fatal("marker was dropped although the wallet files could not be removed")
	}

	if err := os.RemoveAll(filepath.Join(dir, "family_watch_zcash.json")); err != nil {
		t.Fatal(err)
	}
	a.processNodeDeletions()
	if stateFileExists(dir, "node-delete.zcash") {
		t.Fatal("marker stayed after the retry succeeded")
	}
}

func TestNodeDeletionDoesNothingWithoutAMarker(t *testing.T) {
	a, _ := testApp(t, &rpcFixture{}, "")
	dir := a.cfg.stateDir
	writeStateFiles(t, dir, "monero_watch.json", "family_watch_zcash.json")
	a.processNodeDeletions()
	if !stateFileExists(dir, "monero_watch.json") || !stateFileExists(dir, "family_watch_zcash.json") {
		t.Fatal("wallets were removed without a deletion marker")
	}
}

func TestDashboardStartupAndBackgroundHonourADeletionMarker(t *testing.T) {
	a, _ := testApp(t, &rpcFixture{}, "")
	dir := a.cfg.stateDir
	writeStateFiles(t, dir, "family_watch_litecoin.json", "node-delete.litecoin")
	ctx, cancel := context.WithCancel(t.Context())
	done := a.startBackground(ctx)
	deadline := time.Now().Add(2 * time.Second)
	for stateFileExists(dir, "node-delete.litecoin") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if stateFileExists(dir, "node-delete.litecoin") || stateFileExists(dir, "family_watch_litecoin.json") {
		t.Fatal("the background worker did not remove the deleted node's wallets")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("background workers did not stop")
	}
}
