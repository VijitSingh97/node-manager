package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestFamilyWatchPersistenceFailuresRollbackMemory(t *testing.T) {
	a, _, _, _ := newFamilyWatchTestApp(t, "secret")
	store := a.familyWatch.stores[nodeLitecoin]
	badRoster := filepath.Join(t.TempDir(), "roster-directory")
	if err := os.Mkdir(badRoster, 0o700); err != nil {
		t.Fatal(err)
	}
	store.rosterPath = badRoster
	if _, err := store.add("Will fail", "failure-address"); err == nil {
		t.Fatal("add succeeded despite roster persistence failure")
	}
	store.mu.RLock()
	if len(store.wallets) != 1 {
		t.Fatalf("failed add left %d wallets", len(store.wallets))
	}
	store.mu.RUnlock()

	a, _, _, _ = newFamilyWatchTestApp(t, "secret")
	store = a.familyWatch.stores[nodeLitecoin]
	wallets, _ := store.list(false)
	store.record(wallets[0].ID, "123", 200, "tip")
	badCache := filepath.Join(t.TempDir(), "cache-directory")
	if err := os.Mkdir(badCache, 0o700); err != nil {
		t.Fatal(err)
	}
	store.cachePath = badCache
	if removed, err := store.remove(wallets[0].ID); err == nil || removed {
		t.Fatalf("remove persistence failure = %v, %v", removed, err)
	}
	store.mu.RLock()
	_, hasBalance := store.balances[wallets[0].ID]
	state := store.states[wallets[0].ID]
	if len(store.wallets) != 1 || store.wallets[0].ID != wallets[0].ID || !hasBalance || state != "ok" {
		t.Fatalf("failed remove state = wallets %#v balance %v state %q", store.wallets, hasBalance, state)
	}
	store.mu.RUnlock()

	a, _, _, _ = newFamilyWatchTestApp(t, "secret")
	store = a.familyWatch.stores[nodeLitecoin]
	wallets, _ = store.list(false)
	store.record(wallets[0].ID, "456", 200, "tip")
	cachePath := store.cachePath
	badRoster = filepath.Join(t.TempDir(), "remove-roster-directory")
	if err := os.Mkdir(badRoster, 0o700); err != nil {
		t.Fatal(err)
	}
	store.rosterPath = badRoster
	if removed, err := store.remove(wallets[0].ID); err == nil || removed {
		t.Fatalf("remove roster failure = %v, %v", removed, err)
	}
	store.mu.RLock()
	_, hasBalance = store.balances[wallets[0].ID]
	if len(store.wallets) != 1 || !hasBalance {
		t.Fatalf("roster failure state = wallets %#v balance %v", store.wallets, hasBalance)
	}
	store.mu.RUnlock()
	data, err := os.ReadFile(cachePath)
	var cached familyWatchCacheFile
	if err != nil || !decodeStrictBytes(data, &cached) {
		t.Fatalf("restored cache = %q, %v", data, err)
	}
	if _, ok := cached.Balances[wallets[0].ID]; !ok {
		t.Fatal("roster failure did not restore persisted cache")
	}
}

func TestFamilyWatchNativeRPCRejectsRedirects(t *testing.T) {
	var redirected atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		redirected.Add(1)
		writeJSON(w, map[string]any{"result": map[string]any{"isvalid": true}, "error": nil})
	}))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	node := nodeConfig{ID: nodeLitecoin, RPCURL: redirect.URL, RPCUser: "user", RPCPassword: "password"}
	a := &app{cfg: config{stateDir: t.TempDir(), nodes: map[string]nodeConfig{nodeLitecoin: node}}}
	store := newFamilyWatchManager(a).stores[nodeLitecoin]
	var result any
	if err := store.rpc.call(t.Context(), "validateaddress", []any{"address"}, &result); err == nil {
		t.Fatal("redirected native RPC succeeded")
	}
	if redirected.Load() != 0 {
		t.Fatalf("native RPC followed %d redirects", redirected.Load())
	}
}

func TestFamilyWatchExampleDeletionPersistsAndFilesArePrivate(t *testing.T) {
	a, _, _, stateDir := newFamilyWatchTestApp(t, "secret")
	store := a.familyWatch.stores[nodeBitcoinCash]
	wallets, _ := store.list(false)
	if len(wallets) != 1 || !wallets[0].Example || wallets[0].Address != "bitcoincash:pq22wvjxu3et9rxgw8zgwcs0j3270gs6lumlq52mxl" || wallets[0].SourceURL != "https://bitcoincashnode.org/en/" {
		t.Fatalf("seed = %#v", wallets)
	}
	if removed, err := store.remove(wallets[0].ID); err != nil || !removed {
		t.Fatalf("remove example = %v, %v", removed, err)
	}
	restarted := &app{cfg: a.cfg}
	restarted.familyWatch = newFamilyWatchManager(restarted)
	wallets, err := restarted.familyWatch.stores[nodeBitcoinCash].list(false)
	if err != nil || len(wallets) != 0 {
		t.Fatalf("restarted wallets = %#v, %v", wallets, err)
	}
	for _, name := range []string{"family_watch_bitcoincash.json", "family_watch_bitcoincash_cache.json"} {
		info, err := os.Stat(filepath.Join(stateDir, name))
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode = %v, %v", name, info, err)
		}
	}
}

func TestFamilyWatchUnsupportedReasonsAndAtomicParser(t *testing.T) {
	for id, phrase := range map[string]string{nodeDogecoin: "no pruned-compatible", nodeZcash: "checkpoint pruning", nodeTari: "view key"} {
		supported, reason := (&app{}).familyWatchCapability(id)
		if supported || !strings.Contains(reason, phrase) {
			t.Fatalf("%s capability = %v %q", id, supported, reason)
		}
	}
	for _, test := range []struct {
		raw, want string
		max       uint64
		ok        bool
	}{
		{"1.23456789", "123456789", 21_000_000 * 100_000_000, true},
		{"0", "0", 21_000_000 * 100_000_000, true},
		{"84000000.00000000", "8400000000000000", 84_000_000 * 100_000_000, true},
		{"0.000000001", "", 21_000_000 * 100_000_000, false},
		{"21000000.00000001", "", 21_000_000 * 100_000_000, false},
		{"1e-8", "", 21_000_000 * 100_000_000, false},
	} {
		got, err := parseFamilyWatchAmount(json.RawMessage(test.raw), test.max)
		if (err == nil) != test.ok || got != test.want {
			t.Errorf("parse %q = %q, %v", test.raw, got, err)
		}
	}
}

func TestFamilyWatchBackgroundStopsWithContext(t *testing.T) {
	a, _, _, _ := newFamilyWatchTestApp(t, "secret")
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		a.familyWatch.run(ctx)
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("family watch workers did not stop")
	}
}
