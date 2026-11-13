package main

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	familyScanHash100 = "0000000000000000000000000000000000000000000000000000000000000100"
	familyScanHash101 = "0000000000000000000000000000000000000000000000000000000000000101"
	familyScanHash102 = "0000000000000000000000000000000000000000000000000000000000000102"
	familyScanFork102 = "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff02"
)

type familyScanRPCFixture struct {
	mu              sync.Mutex
	ibd             bool
	warnings        any
	tipTime         int64
	chainHeight     int64
	chainHash       string
	scanHeight      int64
	scanHash        string
	canonicalHashes map[int64]string
	scanned         []string
	calls           chan string
}

func (f *familyScanRPCFixture) handler(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Method string `json:"method"`
		Params []any  `json:"params"`
	}
	if json.NewDecoder(r.Body).Decode(&request) != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	ibd, warnings, tipTime := f.ibd, f.warnings, f.tipTime
	chainHeight, chainHash := f.chainHeight, f.chainHash
	scanHeight, scanHash := f.scanHeight, f.scanHash
	canonical := make(map[int64]string, len(f.canonicalHashes))
	for height, hash := range f.canonicalHashes {
		canonical[height] = hash
	}
	f.mu.Unlock()
	select {
	case f.calls <- request.Method:
	default:
	}
	var result any
	switch request.Method {
	case "getblockchaininfo":
		result = map[string]any{"blocks": chainHeight, "headers": chainHeight, "bestblockhash": chainHash, "initialblockdownload": ibd, "warnings": warnings}
	case "getblockheader":
		result = map[string]any{"time": tipTime}
	case "scantxoutset":
		descriptors, _ := request.Params[1].([]any)
		descriptor, _ := descriptors[0].(string)
		f.mu.Lock()
		f.scanned = append(f.scanned, descriptor)
		f.mu.Unlock()
		result = map[string]any{"success": true, "total_amount": json.RawMessage("1.25000000"), "height": scanHeight, "bestblock": scanHash}
	case "getblockhash":
		height, _ := request.Params[0].(float64)
		result = canonical[int64(height)]
	default:
		http.Error(w, "unexpected method", http.StatusBadRequest)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"result": result, "error": nil})
}

func (f *familyScanRPCFixture) update(fn func(*familyScanRPCFixture)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f)
}

func (f *familyScanRPCFixture) scans() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.scanned...)
}

func newFamilyScanTest(t *testing.T, now time.Time, wallets ...familyWatchEntry) (*familyWatchManager, *familyWatchStore, *familyScanRPCFixture) {
	t.Helper()
	fixture := &familyScanRPCFixture{
		tipTime: now.Add(-90 * time.Minute).Unix(), chainHeight: 100, chainHash: familyScanHash100,
		scanHeight: 100, scanHash: familyScanHash100, canonicalHashes: map[int64]string{100: familyScanHash100}, calls: make(chan string, 32),
	}
	server := httptest.NewServer(http.HandlerFunc(fixture.handler))
	t.Cleanup(server.Close)
	store := &familyWatchStore{
		cachePath: t.TempDir() + "/cache.json", wallets: wallets, balances: make(map[string]familyWatchBalance), states: make(map[string]string),
		trigger: make(chan struct{}, 1), now: func() time.Time { return now }, maxSupply: 84_000_000 * 100_000_000,
		rpc: &rpcClient{url: server.URL, client: server.Client()},
	}
	a := &app{cfg: config{nodes: map[string]nodeConfig{nodeLitecoin: {ID: nodeLitecoin, Enabled: true, Active: true, ActiveSet: true}}}, log: log.New(io.Discard, "", 0), nodeHealth: make(map[string]nodeHealthSnapshot)}
	manager := &familyWatchManager{app: a, stores: map[string]*familyWatchStore{nodeLitecoin: store}}
	return manager, store, fixture
}

func waitFamilyScanCall(t *testing.T, fixture *familyScanRPCFixture, method string) {
	t.Helper()
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case called := <-fixture.calls:
			if called == method {
				return
			}
		case <-deadline.C:
			t.Fatalf("timed out waiting for %s", method)
		}
	}
}

func waitFamilyScanState(t *testing.T, store *familyWatchStore, id, state string) {
	t.Helper()
	deadline := time.NewTimer(2 * time.Second)
	ticker := time.NewTicker(time.Millisecond)
	defer deadline.Stop()
	defer ticker.Stop()
	for {
		store.mu.RLock()
		current := store.states[id]
		store.mu.RUnlock()
		if current == state {
			return
		}
		select {
		case <-ticker.C:
		case <-deadline.C:
			t.Fatalf("state = %q, want %q", current, state)
		}
	}
}

func TestFamilyWatchPollsReadinessBeforeHourlyRescan(t *testing.T) {
	now := time.Unix(2_000_000_000, 0)
	wallet := familyWatchEntry{ID: "wallet-ready", Address: "ready-address"}
	manager, store, fixture := newFamilyScanTest(t, now, wallet)
	fixture.update(func(f *familyScanRPCFixture) { f.ibd = true })
	ctx, cancel := context.WithCancel(t.Context())
	ticks := make(chan time.Time)
	done := make(chan struct{})
	go func() {
		manager.runStore(ctx, nodeLitecoin, store, ticks)
		close(done)
	}()
	waitFamilyScanCall(t, fixture, "getblockchaininfo")
	waitFamilyScanState(t, store, wallet.ID, "waiting")
	fixture.update(func(f *familyScanRPCFixture) { f.ibd = false })
	ticks <- now.Add(familyWatchPollInterval)
	waitFamilyScanCall(t, fixture, "scantxoutset")
	waitFamilyScanState(t, store, wallet.ID, "ok")
	cancel()
	<-done
}

func TestFamilyWatchSkipsFreshWalletsAfterRosterChanges(t *testing.T) {
	now := time.Unix(2_000_000_000, 0)
	fresh := familyWatchEntry{ID: "wallet-fresh", Address: "fresh-address"}
	added := familyWatchEntry{ID: "wallet-added", Address: "added-address"}
	manager, store, fixture := newFamilyScanTest(t, now, fresh, added)
	store.balances[fresh.ID] = familyWatchBalance{Amount: "1", ScanHeight: 100, ScanTime: now.Add(-time.Minute).Unix(), BestBlock: familyScanHash100}
	store.states[fresh.ID] = "ok"
	store.states[added.ID] = "pending"
	manager.scanNode(t.Context(), nodeLitecoin, store)
	if scans := fixture.scans(); len(scans) != 1 || !strings.Contains(scans[0], added.Address) {
		t.Fatalf("add scans = %#v", scans)
	}
	store.mu.Lock()
	store.wallets = store.wallets[:1]
	store.mu.Unlock()
	manager.scanNode(t.Context(), nodeLitecoin, store)
	if scans := fixture.scans(); len(scans) != 1 {
		t.Fatalf("delete rescanned fresh wallets: %#v", scans)
	}
}

func TestFamilyWatchAcceptsAdvancedScanAndRejectsReorg(t *testing.T) {
	now := time.Unix(2_000_000_000, 0)
	verified := familyWatchEntry{ID: "wallet-verified", Address: "verified-address"}
	unverified := familyWatchEntry{ID: "wallet-unverified", Address: "unverified-address"}
	manager, store, fixture := newFamilyScanTest(t, now, verified)
	logs := &lockedLog{}
	manager.app.log.SetOutput(logs)
	fixture.update(func(f *familyScanRPCFixture) {
		f.scanHeight, f.scanHash = 101, familyScanHash101
		f.canonicalHashes[101] = familyScanHash101
	})
	manager.scanNode(t.Context(), nodeLitecoin, store)
	balance := store.balances[verified.ID]
	if balance.ScanHeight != 101 || balance.BestBlock != familyScanHash101 || balance.ScanTime != now.Unix() || len(balance.History) != 1 {
		t.Fatalf("advanced scan = %#v", balance)
	}
	store.mu.Lock()
	store.states[verified.ID] = "stale"
	store.wallets = append(store.wallets, unverified)
	store.states[unverified.ID] = "pending"
	store.mu.Unlock()
	fixture.update(func(f *familyScanRPCFixture) {
		f.chainHeight, f.chainHash = 101, familyScanHash101
		f.scanHeight, f.scanHash = 102, familyScanFork102
		f.canonicalHashes[102] = familyScanHash102
	})
	manager.scanNode(t.Context(), nodeLitecoin, store)
	if current := store.balances[verified.ID]; current.ScanHeight != 101 || len(current.History) != 1 || store.states[verified.ID] != "stale" {
		t.Fatalf("reorg replaced verified balance: %#v, state %q", current, store.states[verified.ID])
	}
	if _, cached := store.balances[unverified.ID]; cached || store.states[unverified.ID] != "error" {
		t.Fatalf("unverified scan cached: %#v, state %q", store.balances[unverified.ID], store.states[unverified.ID])
	}
	output := logs.String()
	if !strings.Contains(output, "litecoin watch scan: outcome=ok scan=") || !strings.Contains(output, "litecoin watch scan: outcome=off_chain scan=") || strings.Contains(output, "-address") {
		t.Fatalf("family scan timing lines = %q", output)
	}
}

func TestFamilyWatchRejectsFutureTipsAndWarnings(t *testing.T) {
	now := time.Unix(2_000_000_000, 0)
	for _, test := range []struct {
		name   string
		update func(*familyScanRPCFixture)
	}{
		{"future tip", func(f *familyScanRPCFixture) { f.tipTime = now.Add(time.Second).Unix() }},
		{"warning", func(f *familyScanRPCFixture) { f.warnings = "native warning" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			wallet := familyWatchEntry{ID: "wallet-waiting", Address: "waiting-address"}
			manager, store, fixture := newFamilyScanTest(t, now, wallet)
			fixture.update(test.update)
			manager.scanNode(t.Context(), nodeLitecoin, store)
			if scans := fixture.scans(); len(scans) != 0 || store.states[wallet.ID] != "waiting" {
				t.Fatalf("unsafe readiness scanned: %#v, state %q", scans, store.states[wallet.ID])
			}
		})
	}
}
