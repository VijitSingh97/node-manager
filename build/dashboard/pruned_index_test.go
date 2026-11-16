package main

import (
	"context"
	"encoding/hex"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const indexTestScript = "001401aabb"

func indexTestChain(extra []fakeOut) *fakeUtxoChain {
	c := utxoMgrChain(10, "b")
	if extra != nil {
		c.add("x", extra, nil)
	}
	return c
}

func indexTestManager(t *testing.T, c *fakeUtxoChain) *utxoManager {
	t.Helper()
	dir := t.TempDir()
	utxoMgrSeed(t, dir, nil, c)
	m := utxoMgrNew(t, dir, nil, newFakeUtxoNode(c), io.Discard)
	m.tick(context.Background())
	if st := m.Status().State; st != utxoStateReady {
		t.Fatalf("index state %s", st)
	}
	return m
}

func indexTestScripts(scripts ...string) func(context.Context, []string) ([]walletScripts, error) {
	return func(_ context.Context, keys []string) ([]walletScripts, error) {
		out := make([]walletScripts, len(keys))
		for i := range out {
			for _, s := range scripts {
				b, _ := hex.DecodeString(s)
				out[i].Scripts = append(out[i].Scripts, b)
			}
		}
		return out, nil
	}
}

func indexExpected(t *testing.T, m *utxoManager, scripts ...string) (int, float64) {
	t.Helper()
	var list [][]byte
	for _, s := range scripts {
		b, _ := hex.DecodeString(s)
		list = append(list, b)
	}
	coins, err := m.store.Lookup(list)
	if err != nil || len(coins) == 0 {
		t.Fatalf("lookup: %v, %d coins", err, len(coins))
	}
	var sats uint64
	for _, c := range coins {
		sats += c.Amount
	}
	return len(coins), float64(sats) / 1e8
}

func indexTestApp(t *testing.T, c *fakeUtxoChain, m *utxoManager) (*app, http.Handler, *prunedRPCMock) {
	t.Helper()
	best := c.hashes[len(c.hashes)-1]
	mock := &prunedRPCMock{bestBlock: best, result: validPrunedScan(best)}
	a, handler, _ := newPrunedTestApp(t, t.TempDir(), mock)
	a.utxo = m
	return a, handler, mock
}

func scanCalls(mock *prunedRPCMock) int {
	mock.mu.Lock()
	defer mock.mu.Unlock()
	return mock.startCalls
}

func TestPrunedWalletServedFromReadyIndex(t *testing.T) {
	c := indexTestChain(nil)
	m := indexTestManager(t, c)
	a, handler, mock := indexTestApp(t, c, m)
	logs := &lockedLog{}
	a.log.SetOutput(logs)
	a.scanner.resolveScripts = indexTestScripts(indexTestScript)
	count, btc := indexExpected(t, m, indexTestScript)
	if response := addPrunedWatch(t, handler, "Cold", genesisAddress); response.Code != http.StatusOK {
		t.Fatalf("add response = %d", response.Code)
	}
	row := waitPrunedState(t, handler, "ok")
	waitPrunedScannerIdle(t, a.scanner)
	if row["balance_source"] != "index" || row["btc"] != formatBTC(btc) || row["balance_kind"] != "confirmed" || row["scan_height"] != float64(9) {
		t.Fatalf("row = %#v", row)
	}
	if scanCalls(mock) != 0 {
		t.Fatal("scantxoutset ran although the index was ready")
	}
	cache, _, ok := a.watch.scanSnapshot(a.watch.Entries()[0])
	if !ok || cache.Source != "index" || len(cache.Outpoints) != count || !validPrunedScanCache(cache) {
		t.Fatalf("cache = %#v", cache)
	}
	path := a.scanner.scriptsPath()
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("registry %v %v", info, err)
	}
	if out := logs.String(); !strings.Contains(out, "watch index: outcome=ok") || strings.Contains(out, genesisAddress) || strings.Contains(out, indexTestScript) || strings.Contains(out, formatBTC(btc)) {
		t.Fatalf("log = %q", out)
	}
}

func TestPrunedIndexScriptsDerivedOncePerDescriptor(t *testing.T) {
	c := indexTestChain(nil)
	m := indexTestManager(t, c)
	a, _, _ := indexTestApp(t, c, m)
	entry, _ := a.watch.Add("Cold", genesisAddress, "")
	var mu sync.Mutex
	calls := 0
	inner := indexTestScripts(indexTestScript)
	a.scanner.resolveScripts = func(ctx context.Context, keys []string) ([]walletScripts, error) {
		mu.Lock()
		calls++
		mu.Unlock()
		return inner(ctx, keys)
	}
	for range 3 {
		if len(a.scanner.indexScripts(context.Background(), entry)) != 1 {
			t.Fatal("scripts missing")
		}
	}
	fresh := newPrunedScanner(a)
	fresh.resolveScripts = a.scanner.resolveScripts
	if len(fresh.indexScripts(context.Background(), entry)) != 1 || calls != 1 {
		t.Fatalf("derivations = %d, want 1 including after reload", calls)
	}
}

func TestPrunedFallsBackToScanWhenIndexCannotServe(t *testing.T) {
	c := indexTestChain(nil)
	invalid := indexTestManager(t, c)
	invalid.invalidate("test")
	empty := newUtxoManager(t.TempDir(), nil, newFakeUtxoNode(c), log.New(io.Discard, "", 0), utxoBitcoin)
	t.Cleanup(func() { _ = empty.store.Close() })
	ready := indexTestManager(t, c)
	cases := map[string]struct {
		m       *utxoManager
		resolve func(context.Context, []string) ([]walletScripts, error)
	}{
		"off":        {nil, indexTestScripts(indexTestScript)},
		"not_seeded": {empty, indexTestScripts(indexTestScript)},
		"invalid":    {invalid, indexTestScripts(indexTestScript)},
		"unsupported": {ready, func(context.Context, []string) ([]walletScripts, error) {
			return []walletScripts{{Fallback: true}}, nil
		}},
		"rpc error": {ready, func(context.Context, []string) ([]walletScripts, error) { return nil, errors.New("down") }},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			a, handler, mock := indexTestApp(t, c, tc.m)
			a.scanner.resolveScripts = tc.resolve
			if response := addPrunedWatch(t, handler, "Cold", genesisAddress); response.Code != http.StatusOK {
				t.Fatalf("add response = %d", response.Code)
			}
			row := waitPrunedState(t, handler, "ok")
			waitPrunedScannerIdle(t, a.scanner)
			if scanCalls(mock) != 1 || row["balance_source"] != "scan" || row["btc"] != "1.25" {
				t.Fatalf("scans=%d row=%#v", scanCalls(mock), row)
			}
		})
	}
}

func TestPrunedIndexLagServesFromScanUntilReady(t *testing.T) {
	c := indexTestChain(nil)
	m := indexTestManager(t, c)
	m.nodeTip = 100
	if m.Status().State == utxoStateReady {
		t.Fatal("index should be behind the node")
	}
	a, handler, mock := indexTestApp(t, c, m)
	a.scanner.resolveScripts = indexTestScripts(indexTestScript)
	addPrunedWatch(t, handler, "Cold", genesisAddress)
	row := waitPrunedState(t, handler, "ok")
	if scanCalls(mock) != 1 || row["balance_source"] != "scan" {
		t.Fatalf("scans=%d row=%#v", scanCalls(mock), row)
	}
}

func TestPrunedInvalidIndexResultDoesNotDisableScan(t *testing.T) {
	half := "15000000.00000000"
	c := indexTestChain([]fakeOut{{indexTestScript, half}, {indexTestScript, half}})
	m := indexTestManager(t, c)
	a, handler, mock := indexTestApp(t, c, m)
	logs := &lockedLog{}
	a.log.SetOutput(logs)
	a.scanner.resolveScripts = indexTestScripts(indexTestScript)
	addPrunedWatch(t, handler, "Cold", genesisAddress)
	row := waitPrunedState(t, handler, "ok")
	waitPrunedScannerIdle(t, a.scanner)
	if !strings.Contains(logs.String(), "rejected by the balance cache") {
		t.Fatalf("index result was not rejected: %q", logs.String())
	}
	if a.watch.ScanErr() != nil || scanCalls(mock) != 1 || row["balance_source"] != "scan" || row["btc"] != "1.25" {
		t.Fatalf("scanErr=%v scans=%d row=%#v", a.watch.ScanErr(), scanCalls(mock), row)
	}
}

func TestPrunedIndexErrorKeepsScanFallbackAvailable(t *testing.T) {
	c := indexTestChain(nil)
	m := indexTestManager(t, c)
	a, handler, mock := indexTestApp(t, c, m)
	a.scanner.resolveScripts = indexTestScripts(indexTestScript)
	addPrunedWatch(t, handler, "Cold", genesisAddress)
	waitPrunedState(t, handler, "ok")
	waitPrunedScannerIdle(t, a.scanner)
	m.invalidate("test")
	if a.watch.ScanErr() != nil {
		t.Fatal("index invalidation disabled scans")
	}
	entry := a.watch.Entries()[0]
	if !a.scanner.enqueue(entry, true) {
		t.Fatal("scan could not be enqueued after the index went invalid")
	}
	waitPrunedScannerIdle(t, a.scanner)
	row := waitPrunedState(t, handler, "ok")
	if scanCalls(mock) != 1 || row["balance_source"] != "scan" {
		t.Fatalf("scans=%d row=%#v", scanCalls(mock), row)
	}
}

func TestPrunedNonPrunedNodeSkipsIndex(t *testing.T) {
	c := indexTestChain(nil)
	m := indexTestManager(t, c)
	a, _, _ := indexTestApp(t, c, m)
	a.scanner.resolveScripts = func(context.Context, []string) ([]walletScripts, error) {
		t.Fatal("derivation attempted on a non-pruned node")
		return nil, nil
	}
	entry, _ := a.watch.Add("Cold", genesisAddress, "")
	a.scanner.refresh(a.watch.Entries(), blockchainInfo{Pruned: false, BestBlockHash: c.hashes[9], Blocks: 9})
	if _, _, ok := a.watch.scanSnapshot(entry); ok {
		t.Fatal("non-pruned refresh wrote a scan cache")
	}
}

func TestPrunedIndexAndScanSourcesCombineSafely(t *testing.T) {
	best := strings.Repeat("4", 64)
	dir := t.TempDir()
	t.Setenv("WATCH_STORE", filepath.Join(dir, "wallets.json"))
	t.Setenv("BALANCE_HISTORY", filepath.Join(dir, "history.json"))
	store := NewWatchStore(dir)
	first, _ := store.Add("One", genesisAddress, "")
	second, _ := store.Add("Two", "bc1qcr8te4kr609gcawutmrza0j4xv80jy8z306fyu", "")
	chain := blockchainInfo{Pruned: true, BestBlockHash: best}
	none := func(string, string, []any, time.Duration) any { return nil }
	shared := scanOutpoint{TxID: strings.Repeat("a", 64), Vout: 0, Sats: 100_000_000, Height: 1}
	own := scanOutpoint{TxID: strings.Repeat("b", 64), Vout: 1, Sats: 50_000_000, Height: 1}
	put := func(entry WatchEntry, source string, outputs ...scanOutpoint) {
		var sats int64
		for _, o := range outputs {
			sats += o.Sats
		}
		cache := prunedScanCache{DescriptorID: descriptorSetID(entry), BTC: float64(sats) / 1e8, Height: 1, BestBlock: best, ScannedAt: time.Now(), Outpoints: outputs, Source: source}
		if !store.StoreScanIfCurrent(entry, cache) {
			t.Fatal("seed failed")
		}
	}
	put(first, "index", shared, own)
	put(second, "", shared)
	view := store.ListWithChain(none, chain)
	if view["total"] != "1.5" || view["total_state"] != "ok" {
		t.Fatalf("deduped mixed total = %#v", view)
	}
	rows := view["wallets"].([]map[string]any)
	if rows[0]["balance_source"] != "index" || rows[1]["balance_source"] != "scan" {
		t.Fatalf("sources = %v %v", rows[0]["balance_source"], rows[1]["balance_source"])
	}
	conflict := shared
	conflict.Sats = 1
	put(second, "", conflict)
	view = store.ListWithChain(none, chain)
	if view["total"] != "0" || view["total_reason"] != "conflicting_outpoints" {
		t.Fatalf("conflicting mixed total = %#v", view)
	}
	late := strings.Repeat("5", 64)
	view = store.ListWithChain(none, blockchainInfo{Pruned: true, BestBlockHash: late})
	if view["total"] != "0" || view["total_state"] != "unavailable" {
		t.Fatalf("stale index row counted in total: %#v", view)
	}
}

func TestUtxoManagerLookupRaceWithInvalidate(t *testing.T) {
	c := indexTestChain(nil)
	m := indexTestManager(t, c)
	script, _ := hex.DecodeString(indexTestScript)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for range 200 {
			m.Lookup([][]byte{script})
		}
	}()
	go func() {
		defer wg.Done()
		m.invalidate("test")
	}()
	wg.Wait()
	if _, _, _, ok := m.Lookup([][]byte{script}); ok {
		t.Fatal("invalid index served a lookup")
	}
}

func TestPrunedIndexOneBlockBehindFallsThroughToScan(t *testing.T) {
	c := indexTestChain(nil)
	m := indexTestManager(t, c)
	m.nodeTip = 10
	if st := m.Status(); st.State != utxoStateReady || *st.Behind != 1 {
		t.Fatalf("status = %#v", st)
	}
	a, handler, mock := indexTestApp(t, c, m)
	a.scanner.resolveScripts = indexTestScripts(indexTestScript)
	addPrunedWatch(t, handler, "Cold", genesisAddress)
	row := waitPrunedState(t, handler, "ok")
	if scanCalls(mock) != 1 || row["balance_source"] != "scan" {
		t.Fatalf("scans=%d row=%#v", scanCalls(mock), row)
	}
}

func TestPrunedIndexCurrentCacheClearsPending(t *testing.T) {
	c := indexTestChain(nil)
	m := indexTestManager(t, c)
	a, handler, _ := indexTestApp(t, c, m)
	a.scanner.resolveScripts = indexTestScripts(indexTestScript)
	addPrunedWatch(t, handler, "Cold", genesisAddress)
	waitPrunedState(t, handler, "ok")
	waitPrunedScannerIdle(t, a.scanner)
	entry := a.watch.Entries()[0]
	identity := watchKeyID(entry.Key)
	a.watch.SetPending(entry.Key, true)
	a.scanner.mu.Lock()
	a.scanner.failures[identity] = 3
	a.scanner.mu.Unlock()
	a.scanner.scanBatch(context.Background())
	if _, pending := a.watch.snapshot(); pending[identity] {
		t.Fatal("pending flag stuck after the index served a current cache")
	}
	a.scanner.mu.Lock()
	failures := a.scanner.failures[identity]
	a.scanner.mu.Unlock()
	if failures != 0 {
		t.Fatalf("failures = %d", failures)
	}
}

func TestPrunedIndexRegistryPrunedWhenWalletRemoved(t *testing.T) {
	c := indexTestChain(nil)
	m := indexTestManager(t, c)
	a, handler, _ := indexTestApp(t, c, m)
	a.scanner.resolveScripts = indexTestScripts(indexTestScript)
	addPrunedWatch(t, handler, "Cold", genesisAddress)
	waitPrunedState(t, handler, "ok")
	waitPrunedScannerIdle(t, a.scanner)
	if b, _ := os.ReadFile(a.scanner.scriptsPath()); !strings.Contains(string(b), indexTestScript) {
		t.Fatalf("registry = %s", b)
	}
	if removed, err := a.watch.Remove("Cold"); !removed || err != nil {
		t.Fatalf("remove: %v %v", removed, err)
	}
	a.scanner.refresh(a.watch.Entries(), blockchainInfo{Pruned: true, BestBlockHash: reverseHex(m.mustTip()), Blocks: 9})
	if b, _ := os.ReadFile(a.scanner.scriptsPath()); strings.Contains(string(b), indexTestScript) {
		t.Fatalf("registry still holds a removed wallet: %s", b)
	}
}

func TestPrunedIndexRegistryRederivesWhenRangeChanges(t *testing.T) {
	c := indexTestChain(nil)
	m := indexTestManager(t, c)
	a, handler, _ := indexTestApp(t, c, m)
	a.scanner.resolveScripts = indexTestScripts(indexTestScript)
	addPrunedWatch(t, handler, "Cold", genesisAddress)
	waitPrunedState(t, handler, "ok")
	waitPrunedScannerIdle(t, a.scanner)
	entry := a.watch.Entries()[0]
	identity := watchKeyID(entry.Key)
	a.scanner.scriptMu.Lock()
	record := a.scanner.scripts[identity]
	record.DescriptorID = descriptorSetID(entry)
	a.scanner.scripts[identity] = record
	a.scanner.scriptMu.Unlock()
	calls := 0
	resolve := indexTestScripts(indexTestScript)
	a.scanner.resolveScripts = func(ctx context.Context, keys []string) ([]walletScripts, error) {
		calls++
		return resolve(ctx, keys)
	}
	for range 2 {
		if scripts := a.scanner.indexScripts(context.Background(), entry); len(scripts) != 1 {
			t.Fatalf("scripts = %d", len(scripts))
		}
	}
	if calls != 1 {
		t.Fatalf("a record made for another derive range was reused or re-derived %d times", calls)
	}
}

func (m *utxoManager) mustTip() [32]byte {
	_, hash, _ := m.Tip()
	return hash
}
