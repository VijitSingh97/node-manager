package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type prunedRPCMock struct {
	mu                 sync.Mutex
	bestBlock          string
	ibd                bool
	wallet             string
	walletEntry        WatchEntry
	foreignScan        bool
	deriveFailure      string
	result             scanTxOutSetResult
	startCalls         int
	allCalls           int
	active             int
	maxActive          int
	objects            [][]any
	deriveCalls        map[string]int
	descriptorCalls    int
	descriptorFailures int
	startObserved      chan struct{}
	release            <-chan struct{}
}

func (m *prunedRPCMock) handler(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Method string `json:"method"`
		Params []any  `json:"params"`
	}
	_ = json.NewDecoder(r.Body).Decode(&request)
	m.mu.Lock()
	m.allCalls++
	bestBlock := m.bestBlock
	ibd := m.ibd
	wallet := m.wallet
	scanResult := m.result
	foreignScan := m.foreignScan
	m.mu.Unlock()
	var result any
	var rpcError any
	switch request.Method {
	case "getblockchaininfo":
		result = map[string]any{"pruned": true, "initialblockdownload": ibd, "bestblockhash": bestBlock, "blocks": scanResult.Height}
	case "getdescriptorinfo":
		desc := request.Params[0].(string)
		m.mu.Lock()
		m.descriptorCalls++
		fail := m.descriptorFailures > 0
		if fail {
			m.descriptorFailures--
		}
		m.mu.Unlock()
		if fail {
			rpcError = map[string]any{"code": -1, "message": "transient descriptor failure"}
		} else {
			result = map[string]any{"descriptor": desc + "#scan", "hasprivatekeys": false, "isrange": strings.Contains(desc, "/*")}
		}
	case "deriveaddresses":
		desc, _ := request.Params[0].(string)
		m.mu.Lock()
		if m.deriveCalls == nil {
			m.deriveCalls = map[string]int{}
		}
		m.deriveCalls[desc]++
		m.mu.Unlock()
		if m.deriveFailure != "" && strings.Contains(desc, m.deriveFailure) {
			rpcError = map[string]any{"code": -5, "message": "cannot derive"}
		} else {
			result = []string{"mock-address"}
		}
	case "getwalletinfo":
		if strings.TrimPrefix(r.URL.Path, "/wallet/") == wallet && wallet != "" {
			result = watchInfo(false)
		}
	case "listdescriptors":
		if strings.TrimPrefix(r.URL.Path, "/wallet/") == wallet && wallet != "" {
			result = listedDescriptors(m.walletEntry)
		}
	case "scantxoutset":
		action, _ := request.Params[0].(string)
		if action == "status" {
			if foreignScan {
				result = map[string]any{"progress": 0.5}
			}
			break
		}
		m.mu.Lock()
		m.startCalls++
		m.active++
		if m.active > m.maxActive {
			m.maxActive = m.active
		}
		m.objects = append(m.objects, request.Params[1].([]any))
		m.mu.Unlock()
		if m.startObserved != nil {
			select {
			case m.startObserved <- struct{}{}:
			default:
			}
		}
		if m.release != nil {
			<-m.release
		}
		m.mu.Lock()
		m.active--
		m.mu.Unlock()
		result = scanResult
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"result": result, "error": rpcError})
}

func (m *prunedRPCMock) setIBD(value bool) {
	m.mu.Lock()
	m.ibd = value
	m.mu.Unlock()
}

func (m *prunedRPCMock) setScanResult(bestBlock string, result scanTxOutSetResult) {
	m.mu.Lock()
	m.bestBlock = bestBlock
	m.result = result
	m.mu.Unlock()
}

func validPrunedScan(bestBlock string) scanTxOutSetResult {
	return scanTxOutSetResult{
		Success: true, TxOuts: 200_000_000, Height: 900000, BestBlock: bestBlock, TotalAmount: 1.25,
		Unspents: []scanTxOutSetUnspent{{TxID: strings.Repeat("a", 64), Vout: 0, ScriptPubKey: "0014abcd", Desc: "addr(test)#scan", Amount: 1.25}},
	}
}

func cachedPrunedScan(entry WatchEntry, btc float64, height int64, bestBlock string, scannedAt time.Time) prunedScanCache {
	sats, _ := btcSatoshis(btc)
	return prunedScanCache{
		DescriptorID: descriptorSetID(entry), BTC: btc, Height: height, BestBlock: bestBlock, ScannedAt: scannedAt,
		Outpoints: []scanOutpoint{{TxID: strings.Repeat("f", 64), Vout: 0, Sats: sats}},
	}
}

func TestValidatedPrunedScanBalanceUsesMatchedOutputsNotChainstateCount(t *testing.T) {
	result := validPrunedScan(strings.Repeat("0", 64))
	duplicate := result.Unspents[0]
	result.Unspents = append(result.Unspents, duplicate)
	if btc, err := validatedScanBalance(result); err != nil || btc != 1.25 {
		t.Fatalf("deduplicated balance = %v, %v", btc, err)
	}
	result.Unspents[1].Amount = 1
	if _, err := validatedScanBalance(result); err == nil {
		t.Fatal("conflicting duplicate output was accepted")
	}
	result = validPrunedScan(strings.Repeat("0", 64))
	result.TxOuts = 0
	if _, err := validatedScanBalance(result); err == nil {
		t.Fatal("chainstate count below matched outputs was accepted")
	}
	result = validPrunedScan(strings.Repeat("0", 64))
	result.Unspents[0].Amount = 0.000000001
	result.TotalAmount = result.Unspents[0].Amount
	if _, err := validatedScanBalance(result); err == nil {
		t.Fatal("sub-satoshi amount was accepted")
	}
}

func TestPrunedBatchDerivesRepeatedOutputDescriptorOnce(t *testing.T) {
	bestBlock := strings.Repeat("0", 64)
	result := validPrunedScan(bestBlock)
	result.Unspents = append(result.Unspents, scanTxOutSetUnspent{
		TxID: strings.Repeat("b", 64), Vout: 1, ScriptPubKey: "0014dcba", Desc: result.Unspents[0].Desc, Amount: 0.75,
	})
	result.TotalAmount = 2
	mock := &prunedRPCMock{bestBlock: bestBlock, result: result}
	a, handler, _ := newPrunedTestApp(t, t.TempDir(), mock)
	if response := addPrunedWatch(t, handler, "Cold", genesisAddress); response.Code != http.StatusOK {
		t.Fatalf("add response = %d %q", response.Code, response.Body.String())
	}
	row := waitPrunedState(t, handler, "ok")
	waitPrunedScannerIdle(t, a.scanner)
	if row["btc"] != "2" {
		t.Fatalf("repeated descriptor balance row = %#v", row)
	}
	mock.mu.Lock()
	defer mock.mu.Unlock()
	if mock.deriveCalls[result.Unspents[0].Desc] != 1 {
		t.Fatalf("output descriptor derive calls = %d", mock.deriveCalls[result.Unspents[0].Desc])
	}
}

func TestPrunedRefreshBacksOffAfterTransientDescriptorFailure(t *testing.T) {
	bestBlock := strings.Repeat("1", 64)
	mock := &prunedRPCMock{bestBlock: bestBlock, result: validPrunedScan(bestBlock), descriptorFailures: 1}
	a, handler, _ := newPrunedTestApp(t, t.TempDir(), mock)
	_, err := a.watch.Add("Cold", genesisAddress, "")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_800_000_000, 0)
	a.scanner.now = func() time.Time { return now }
	chain := blockchainInfo{Pruned: true, BestBlockHash: bestBlock}
	a.scanner.refresh(a.watch.Entries(), chain)
	waitPrunedScannerIdle(t, a.scanner)
	if row := prunedRow(t, handler); row["state"] != "error" {
		t.Fatalf("transient failure row = %#v", row)
	}
	mock.mu.Lock()
	firstCalls, firstStarts := mock.descriptorCalls, mock.startCalls
	mock.mu.Unlock()
	a.scanner.refresh(a.watch.Entries(), chain)
	time.Sleep(10 * time.Millisecond)
	mock.mu.Lock()
	backoffCalls, backoffStarts := mock.descriptorCalls, mock.startCalls
	mock.mu.Unlock()
	if backoffCalls != firstCalls || backoffStarts != firstStarts {
		t.Fatalf("retry bypassed backoff: descriptor calls %d->%d, starts %d->%d", firstCalls, backoffCalls, firstStarts, backoffStarts)
	}
	now = now.Add(16 * time.Second)
	a.scanner.refresh(a.watch.Entries(), chain)
	if row := waitPrunedState(t, handler, "ok"); row["btc"] != "1.25" {
		t.Fatalf("post-backoff row = %#v", row)
	}
	waitPrunedScannerIdle(t, a.scanner)
	mock.mu.Lock()
	defer mock.mu.Unlock()
	if mock.descriptorCalls <= firstCalls || mock.startCalls != 1 {
		t.Fatalf("post-backoff descriptor calls=%d starts=%d", mock.descriptorCalls, mock.startCalls)
	}
}

func newPrunedTestApp(t *testing.T, stateDir string, mock *prunedRPCMock) (*app, http.Handler, context.CancelFunc) {
	t.Helper()
	t.Setenv("WATCH_STORE", "")
	t.Setenv("BALANCE_HISTORY", "")
	t.Setenv("TELEGRAM_BOT_TOKEN", "")
	t.Setenv("TELEGRAM_CHAT_ID", "")
	t.Setenv("HEALTHCHECKS_URL", "")
	server := httptest.NewServer(http.HandlerFunc(mock.handler))
	t.Cleanup(server.Close)
	cfg := config{rpcURL: server.URL, dashboardPassword: "secret", stateDir: stateDir, dataDir: t.TempDir(), version: "test"}
	a, err := newApp(cfg, &rpcClient{url: server.URL, client: server.Client()}, "index.html", "static", log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.ctx = ctx
	go a.scanner.run(ctx)
	t.Cleanup(cancel)
	return a, a.routes(), cancel
}

func addPrunedWatch(t *testing.T, handler http.Handler, name, key string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/watch", strings.NewReader(`{"name":"`+name+`","key":"`+key+`"}`))
	req.SetBasicAuth("admin", "secret")
	req.Header.Set("X-Requested-With", "fetch")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	return response
}

func prunedRow(t *testing.T, handler http.Handler) map[string]any {
	t.Helper()
	return watchResponseRow(t, request(t, handler, http.MethodGet, "/api/watch", "secret"))
}

func waitPrunedState(t *testing.T, handler http.Handler, state string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		row := prunedRow(t, handler)
		if row["state"] == state {
			return row
		}
		if time.Now().After(deadline) {
			t.Fatalf("wallet row = %#v, want state %s", row, state)
		}
		time.Sleep(time.Millisecond)
	}
}

func waitPrunedScannerIdle(t *testing.T, scanner *prunedScanner) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		scanner.mu.Lock()
		queued := len(scanner.queued)
		scanner.mu.Unlock()
		if queued == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("scanner still has %d queued jobs", queued)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestPrunedWatchAddressScansAsynchronouslyAndReusesPersistedCache(t *testing.T) {
	bestBlock := strings.Repeat("b", 64)
	release := make(chan struct{})
	mock := &prunedRPCMock{bestBlock: bestBlock, result: validPrunedScan(bestBlock), startObserved: make(chan struct{}, 1), release: release}
	stateDir := t.TempDir()
	a, handler, stop := newPrunedTestApp(t, stateDir, mock)
	response := addPrunedWatch(t, handler, "Cold", genesisAddress)
	if response.Code != http.StatusOK {
		t.Fatalf("add response = %d %q", response.Code, response.Body.String())
	}
	select {
	case <-mock.startObserved:
	case <-time.After(time.Second):
		t.Fatal("pruned scan did not start")
	}
	if row := prunedRow(t, handler); row["state"] != "scanning" || row["btc"] != nil {
		t.Fatalf("active scan row = %#v", row)
	}
	close(release)
	row := waitPrunedState(t, handler, "ok")
	if row["btc"] != "1.25" || row["balance_kind"] != "confirmed" || row["scan_height"] != float64(900000) || row["scan_time"] == "" || len(row["history"].([]any)) != 1 {
		t.Fatalf("completed scan row = %#v", row)
	}
	waitPrunedScannerIdle(t, a.scanner)
	newBlock := strings.Repeat("7", 64)
	newResult := validPrunedScan(newBlock)
	newResult.TotalAmount = 2
	newResult.Unspents[0].Amount = 2
	mock.setScanResult(newBlock, newResult)
	a.scanner.now = func() time.Time { return time.Now().Add(2 * time.Hour) }
	a.scanner.refresh(a.watch.Entries(), blockchainInfo{Pruned: true, BestBlockHash: newBlock})
	row = waitPrunedState(t, handler, "ok")
	waitPrunedScannerIdle(t, a.scanner)
	if row["btc"] != "2" || len(row["history"].([]any)) != 2 {
		t.Fatalf("refreshed scan history = %#v", row)
	}
	_ = prunedRow(t, handler)
	mock.mu.Lock()
	startCalls := mock.startCalls
	mock.mu.Unlock()
	if startCalls != 2 {
		t.Fatalf("page reads started %d scans", startCalls)
	}
	stop()

	restarted, restartedHandler, _ := newPrunedTestApp(t, stateDir, mock)
	restarted.ctx = context.Background()
	row = prunedRow(t, restartedHandler)
	if row["state"] != "ok" || row["btc"] != "2" || len(row["history"].([]any)) != 2 {
		t.Fatalf("persisted scan row = %#v", row)
	}
	if _, err := filepath.Abs(restarted.watch.scanPath); err != nil {
		t.Fatal(err)
	}
	mock.mu.Lock()
	defer mock.mu.Unlock()
	if mock.startCalls != 2 {
		t.Fatalf("restart cache read started %d scans", mock.startCalls)
	}
}

func TestPrunedStartupReusesCurrentPersistedCache(t *testing.T) {
	bestBlock := strings.Repeat("6", 64)
	mock := &prunedRPCMock{bestBlock: bestBlock, result: validPrunedScan(bestBlock)}
	stateDir := t.TempDir()
	first, _, stop := newPrunedTestApp(t, stateDir, mock)
	entry, err := first.watch.Add("Cold", genesisAddress, "")
	if err != nil {
		t.Fatal(err)
	}
	if !first.watch.StoreScanIfCurrent(entry, cachedPrunedScan(entry, 1.25, 900000, bestBlock, time.Now())) {
		t.Fatal("could not persist current cache")
	}
	stop()

	restarted, _, _ := newPrunedTestApp(t, stateDir, mock)
	restartedEntry := restarted.watch.Entries()[0]
	restarted.watch.SetPending(restartedEntry.Key, true)
	chain := blockchainInfo{Pruned: true, BestBlockHash: bestBlock}
	restarted.provisionForChain(restartedEntry, chain)
	restarted.scanner.refresh(restarted.watch.Entries(), chain)
	time.Sleep(20 * time.Millisecond)
	_, pending := restarted.watch.snapshot()
	if pending[watchKeyID(restartedEntry.Key)] {
		t.Fatal("current persisted cache remained pending after startup")
	}
	mock.mu.Lock()
	defer mock.mu.Unlock()
	if mock.startCalls != 0 {
		t.Fatalf("startup started %d scans for a current persisted cache", mock.startCalls)
	}
}

func TestPrunedScanCacheReadFailuresDisableScans(t *testing.T) {
	tests := []struct {
		name  string
		setup func(string)
	}{
		{name: "malformed", setup: func(path string) { _ = os.WriteFile(path, []byte("private-marker"), 0o600) }},
		{name: "unreadable", setup: func(string) {
			readScanFile = func(string) ([]byte, error) { return nil, errors.New("private read failure") }
		}},
		{name: "chmod failure", setup: func(string) {
			chmodScanFile = func(string, os.FileMode) error { return errors.New("private chmod failure") }
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			originalRead, originalChmod := readScanFile, chmodScanFile
			t.Cleanup(func() { readScanFile, chmodScanFile = originalRead, originalChmod })
			stateDir := t.TempDir()
			roster, _ := json.Marshal([]WatchEntry{{Name: "Cold", Key: genesisAddress}})
			if err := os.WriteFile(filepath.Join(stateDir, "wallets.json"), roster, 0o600); err != nil {
				t.Fatal(err)
			}
			tt.setup(filepath.Join(stateDir, "watch_scan_cache.json"))
			bestBlock := strings.Repeat("7", 64)
			mock := &prunedRPCMock{bestBlock: bestBlock, result: validPrunedScan(bestBlock)}
			a, handler, _ := newPrunedTestApp(t, stateDir, mock)
			entry := a.watch.Entries()[0]
			a.watch.SetPending(entry.Key, true)
			a.provisionForChain(entry, blockchainInfo{Pruned: true, BestBlockHash: bestBlock})
			a.scanner.refresh(a.watch.Entries(), blockchainInfo{Pruned: true, BestBlockHash: bestBlock})
			time.Sleep(10 * time.Millisecond)
			response := request(t, handler, http.MethodGet, "/api/watch", "secret")
			if strings.Contains(response.Body.String(), "private-marker") || strings.Contains(response.Body.String(), stateDir) {
				t.Fatalf("cache failure leaked private details: %s", response.Body.String())
			}
			var view map[string]any
			if err := json.Unmarshal(response.Body.Bytes(), &view); err != nil {
				t.Fatal(err)
			}
			row := view["wallets"].([]any)[0].(map[string]any)
			if view["scan_cache_unavailable"] != true || view["total_state"] != "unavailable" || view["total_reason"] != "scan_cache_unavailable" || row["state"] != "error" || row["btc"] != nil {
				t.Fatalf("cache failure view = %#v", view)
			}
			mock.mu.Lock()
			defer mock.mu.Unlock()
			if mock.startCalls != 0 {
				t.Fatalf("cache failure started %d scans", mock.startCalls)
			}
		})
	}
}

func TestPrunedScanCacheWriteFailureStopsRetriesAndKeepsPriorCacheStale(t *testing.T) {
	oldBlock := strings.Repeat("8", 64)
	newBlock := strings.Repeat("9", 64)
	mock := &prunedRPCMock{bestBlock: newBlock, result: validPrunedScan(newBlock)}
	a, handler, _ := newPrunedTestApp(t, t.TempDir(), mock)
	entry, err := a.watch.Add("Cold", genesisAddress, "")
	if err != nil {
		t.Fatal(err)
	}
	if !a.watch.StoreScanIfCurrent(entry, cachedPrunedScan(entry, 1, 899999, oldBlock, time.Now().Add(-time.Hour))) {
		t.Fatal("could not seed prior cache")
	}
	originalWrite := writeScanFile
	writeScanFile = func(string, []byte) error { return errors.New("private write failure") }
	t.Cleanup(func() { writeScanFile = originalWrite })
	a.scanner.now = func() time.Time { return time.Now().Add(2 * time.Hour) }
	a.scanner.refresh(a.watch.Entries(), blockchainInfo{Pruned: true, BestBlockHash: newBlock})
	deadline := time.Now().Add(time.Second)
	for a.watch.ScanErr() == nil && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if a.watch.ScanErr() == nil {
		t.Fatal("scan cache write failure was not retained")
	}
	waitPrunedScannerIdle(t, a.scanner)
	response := request(t, handler, http.MethodGet, "/api/watch", "secret")
	var view map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	row := view["wallets"].([]any)[0].(map[string]any)
	if view["scan_cache_unavailable"] != true || view["total_reason"] != "scan_cache_unavailable" || row["state"] != "stale" || row["btc"] != "1" {
		t.Fatalf("write failure view = %#v", view)
	}
	a.scanner.refresh(a.watch.Entries(), blockchainInfo{Pruned: true, BestBlockHash: newBlock})
	a.watch.SetPending(entry.Key, true)
	a.provisionForChain(entry, blockchainInfo{Pruned: true, BestBlockHash: newBlock})
	time.Sleep(10 * time.Millisecond)
	mock.mu.Lock()
	defer mock.mu.Unlock()
	if mock.startCalls != 1 {
		t.Fatalf("write failure retried scan %d times", mock.startCalls)
	}
}

func TestScanCacheFailureDoesNotDisableMatchedCoreWallet(t *testing.T) {
	originalRead := readScanFile
	readScanFile = func(string) ([]byte, error) { return nil, errors.New("read failure") }
	t.Cleanup(func() { readScanFile = originalRead })
	dir := t.TempDir()
	t.Setenv("WATCH_STORE", filepath.Join(dir, "wallets.json"))
	t.Setenv("BALANCE_HISTORY", filepath.Join(dir, "history.json"))
	roster, _ := json.Marshal([]WatchEntry{{Name: "Cold", Key: genesisAddress}})
	if err := os.WriteFile(filepath.Join(dir, "wallets.json"), roster, 0o600); err != nil {
		t.Fatal(err)
	}
	store := NewWatchStore(dir)
	entry := store.Entries()[0]
	name := coreWalletName(entry)
	store.SetCoreWallet(entry.Key, name)
	walletRPC := func(wallet, method string, params []any, timeout time.Duration) any {
		if wallet != name {
			return nil
		}
		switch method {
		case "getwalletinfo":
			return watchInfo(false)
		case "getdescriptorinfo":
			return descriptorInfo(params)
		case "listdescriptors":
			return listedDescriptors(entry)
		case "getbalances":
			return map[string]any{"mine": map[string]any{"trusted": 2.0}}
		}
		return nil
	}
	view := store.ListWithChain(walletRPC, blockchainInfo{Pruned: true, BestBlockHash: strings.Repeat("a", 64)})
	row := view["wallets"].([]map[string]any)[0]
	if row["state"] != "ok" || row["btc"] != "2" || view["total_state"] != "ok" || view["total"] != "2" || view["scan_cache_unavailable"] != true {
		t.Fatalf("Core wallet with unavailable scan cache = %#v", view)
	}
}

func TestPrunedWatchDefersInitialScanDuringIBD(t *testing.T) {
	bestBlock := strings.Repeat("9", 64)
	mock := &prunedRPCMock{bestBlock: bestBlock, ibd: true, result: validPrunedScan(bestBlock)}
	a, handler, _ := newPrunedTestApp(t, t.TempDir(), mock)
	if response := addPrunedWatch(t, handler, "Ledger", parityZpub); response.Code != http.StatusOK {
		t.Fatalf("add response = %d %q", response.Code, response.Body.String())
	}
	time.Sleep(20 * time.Millisecond)
	row := prunedRow(t, handler)
	if row["state"] != "pending" || row["balance_kind"] != "confirmed" || len(row["history"].([]any)) != 0 {
		t.Fatalf("IBD pending row = %#v", row)
	}
	if got := row["range"].(map[string]any); got["start"] != float64(0) || got["end"] != float64(999) {
		t.Fatalf("IBD pending range = %#v", got)
	}
	mock.mu.Lock()
	starts := mock.startCalls
	mock.mu.Unlock()
	if starts != 0 {
		t.Fatalf("IBD started %d scans", starts)
	}
	mock.setIBD(false)
	a.scanner.refresh(a.watch.Entries(), blockchainInfo{Pruned: true, BestBlockHash: bestBlock})
	if row = waitPrunedState(t, handler, "ok"); row["btc"] != "1.25" {
		t.Fatalf("post-IBD row = %#v", row)
	}
	waitPrunedScannerIdle(t, a.scanner)
}

func TestPrunedRefreshSkipsResolvedCoreWallet(t *testing.T) {
	bestBlock := strings.Repeat("8", 64)
	mock := &prunedRPCMock{bestBlock: bestBlock, result: validPrunedScan(bestBlock)}
	a, _, _ := newPrunedTestApp(t, t.TempDir(), mock)
	entry, err := a.watch.Add("Cold", genesisAddress, "")
	if err != nil {
		t.Fatal(err)
	}
	mock.mu.Lock()
	mock.wallet = coreWalletName(entry)
	mock.walletEntry = entry
	mock.mu.Unlock()
	if !a.watch.StoreScanIfCurrent(entry, cachedPrunedScan(entry, 1, 899999, strings.Repeat("6", 64), time.Now().Add(-time.Hour))) {
		t.Fatal("could not seed prior scan cache")
	}
	a.watch.SetCoreWallet(entry.Key, coreWalletName(entry))
	a.watch.SetPending(entry.Key, true)
	chain := blockchainInfo{Pruned: true, BestBlockHash: bestBlock}
	a.scanner.refresh([]WatchEntry{entry}, chain)
	a.scanner.refresh([]WatchEntry{entry}, chain)
	_, pending := a.watch.snapshot()
	if pending[watchKeyID(entry.Key)] {
		t.Fatal("resolved Core wallet was left pending")
	}
	mock.mu.Lock()
	defer mock.mu.Unlock()
	if mock.startCalls != 0 {
		t.Fatalf("resolved Core wallet started %d scans", mock.startCalls)
	}
}

func TestPrunedWatchXpubScansReceiveAndChangeRange(t *testing.T) {
	bestBlock := strings.Repeat("c", 64)
	mock := &prunedRPCMock{bestBlock: bestBlock, result: validPrunedScan(bestBlock)}
	a, handler, _ := newPrunedTestApp(t, t.TempDir(), mock)
	if response := addPrunedWatch(t, handler, "Ledger", parityZpub); response.Code != http.StatusOK {
		t.Fatalf("add response = %d %q", response.Code, response.Body.String())
	}
	row := waitPrunedState(t, handler, "ok")
	waitPrunedScannerIdle(t, a.scanner)
	if got := row["range"].(map[string]any); got["start"] != float64(0) || got["end"] != float64(999) {
		t.Fatalf("scan range = %#v", got)
	}
	mock.mu.Lock()
	defer mock.mu.Unlock()
	if len(mock.objects) != 1 || len(mock.objects[0]) != 2 {
		t.Fatalf("scan objects = %#v", mock.objects)
	}
	for _, object := range mock.objects[0] {
		values, ok := object.(map[string]any)
		if !ok {
			t.Fatalf("ranged scan object = %#v", object)
		}
		rangeValues := values["range"].([]any)
		if rangeValues[0] != float64(0) || rangeValues[1] != float64(999) {
			t.Fatalf("ranged scan object = %#v", object)
		}
	}
}

func TestPrunedWatchScanFailureAndForeignScanDoNotClaimBalance(t *testing.T) {
	bestBlock := strings.Repeat("d", 64)
	tests := []struct {
		name       string
		foreign    bool
		result     scanTxOutSetResult
		wantStarts int
	}{
		{name: "foreign scan", foreign: true, result: validPrunedScan(bestBlock)},
		{name: "invalid total", result: func() scanTxOutSetResult { result := validPrunedScan(bestBlock); result.TotalAmount = 2; return result }(), wantStarts: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := &prunedRPCMock{bestBlock: bestBlock, foreignScan: tt.foreign, result: tt.result}
			a, handler, _ := newPrunedTestApp(t, t.TempDir(), mock)
			if response := addPrunedWatch(t, handler, "Cold", genesisAddress); response.Code != http.StatusOK {
				t.Fatalf("add response = %d %q", response.Code, response.Body.String())
			}
			row := waitPrunedState(t, handler, "error")
			waitPrunedScannerIdle(t, a.scanner)
			if row["btc"] != nil {
				t.Fatalf("failed scan row = %#v", row)
			}
			mock.mu.Lock()
			defer mock.mu.Unlock()
			if mock.startCalls != tt.wantStarts {
				t.Fatalf("scan starts = %d, want %d", mock.startCalls, tt.wantStarts)
			}
		})
	}
}

func TestPrunedWatchScansAreSerialized(t *testing.T) {
	bestBlock := strings.Repeat("e", 64)
	release := make(chan struct{})
	mock := &prunedRPCMock{bestBlock: bestBlock, result: validPrunedScan(bestBlock), startObserved: make(chan struct{}, 2), release: release}
	a, handler, _ := newPrunedTestApp(t, t.TempDir(), mock)
	first, err := a.watch.Add("One", genesisAddress, "")
	if err != nil {
		t.Fatal(err)
	}
	second, err := a.watch.Add("Two", "bc1qcr8te4kr609gcawutmrza0j4xv80jy8z306fyu", "")
	if err != nil {
		t.Fatal(err)
	}
	a.watch.SetPending(first.Key, true)
	a.watch.SetPending(second.Key, true)
	if !a.scanner.enqueue(first, true) {
		t.Fatal("scan was not queued")
	}
	select {
	case <-mock.startObserved:
	case <-time.After(time.Second):
		t.Fatal("first scan did not start")
	}
	time.Sleep(20 * time.Millisecond)
	mock.mu.Lock()
	startsBeforeRelease, maxActive := mock.startCalls, mock.maxActive
	mock.mu.Unlock()
	if startsBeforeRelease != 1 || maxActive != 1 {
		t.Fatalf("before release starts=%d max_active=%d", startsBeforeRelease, maxActive)
	}
	response := request(t, handler, http.MethodGet, "/api/watch", "secret")
	var view map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	rows := view["wallets"].([]any)
	if rows[0].(map[string]any)["state"] != "scanning" || rows[1].(map[string]any)["state"] != "scanning" {
		close(release)
		t.Fatalf("serialized scan states = %#v", rows)
	}
	close(release)
	deadline := time.Now().Add(time.Second)
	for {
		mock.mu.Lock()
		starts, maxActive, active := mock.startCalls, mock.maxActive, mock.active
		mock.mu.Unlock()
		if starts == 1 && active == 0 {
			if maxActive != 1 {
				t.Fatalf("max concurrent scans = %d", maxActive)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("batched scan started %d jobs", starts)
		}
		time.Sleep(time.Millisecond)
	}
	waitPrunedScannerIdle(t, a.scanner)
	response = request(t, handler, http.MethodGet, "/api/watch", "secret")
	if err := json.Unmarshal(response.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if view["total"] != "1.25" || view["total_state"] != "ok" {
		t.Fatalf("overlapping scan total = %#v", view)
	}
}

func TestPrunedRefreshRejectsWrongCachedCoreWallet(t *testing.T) {
	bestBlock := strings.Repeat("3", 64)
	mock := &prunedRPCMock{bestBlock: bestBlock, result: validPrunedScan(bestBlock)}
	a, handler, _ := newPrunedTestApp(t, t.TempDir(), mock)
	entry, err := a.watch.Add("Cold", genesisAddress, "")
	if err != nil {
		t.Fatal(err)
	}
	mock.mu.Lock()
	mock.wallet = coreWalletName(entry)
	mock.walletEntry = WatchEntry{Name: "Other", Key: "bc1qcr8te4kr609gcawutmrza0j4xv80jy8z306fyu"}
	mock.mu.Unlock()
	a.watch.SetCoreWallet(entry.Key, coreWalletName(entry))
	a.scanner.refresh(a.watch.Entries(), blockchainInfo{Pruned: true, BestBlockHash: bestBlock})
	if row := waitPrunedState(t, handler, "ok"); row["btc"] != "1.25" {
		t.Fatalf("wrong cached wallet scan row = %#v", row)
	}
	waitPrunedScannerIdle(t, a.scanner)
	mock.mu.Lock()
	defer mock.mu.Unlock()
	if mock.startCalls != 1 {
		t.Fatalf("wrong cached wallet started %d scans", mock.startCalls)
	}
}

func TestPrunedBatchExcludesUnaddressableEntry(t *testing.T) {
	bestBlock := strings.Repeat("5", 64)
	badAddress := "bc1qcr8te4kr609gcawutmrza0j4xv80jy8z306fyu"
	mock := &prunedRPCMock{bestBlock: bestBlock, result: validPrunedScan(bestBlock), deriveFailure: badAddress}
	a, handler, _ := newPrunedTestApp(t, t.TempDir(), mock)
	good, err := a.watch.Add("Good", genesisAddress, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.watch.Add("Unsupported", badAddress, ""); err != nil {
		t.Fatal(err)
	}
	if !a.scanner.enqueue(good, true) {
		t.Fatal("scan was not queued")
	}
	deadline := time.Now().Add(time.Second)
	for {
		response := request(t, handler, http.MethodGet, "/api/watch", "secret")
		var view map[string]any
		if err := json.Unmarshal(response.Body.Bytes(), &view); err != nil {
			t.Fatal(err)
		}
		rows := view["wallets"].([]any)
		if rows[0].(map[string]any)["state"] == "ok" && rows[1].(map[string]any)["state"] == "error" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("batch rows = %#v", rows)
		}
		time.Sleep(time.Millisecond)
	}
	waitPrunedScannerIdle(t, a.scanner)
	mock.mu.Lock()
	defer mock.mu.Unlock()
	if mock.startCalls != 1 {
		t.Fatalf("valid entry started %d scans", mock.startCalls)
	}
}

func TestWatchConfigRejectsMnemonicBeforePersistence(t *testing.T) {
	stateDir := t.TempDir()
	path := filepath.Join(stateDir, "wallets.json")
	t.Setenv("WATCH_STORE", path)
	t.Setenv("BALANCE_HISTORY", filepath.Join(stateDir, "history.json"))
	items := `[{"name":"Secret","key":"abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about"}]`
	t.Setenv("WATCH_WALLETS_B64", base64.StdEncoding.EncodeToString([]byte(items)))
	store := NewWatchStore(stateDir)
	if !errors.Is(store.Err(), ErrWatchUnavailable) {
		t.Fatalf("mnemonic config error = %v", store.Err())
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("mnemonic config persisted: %v", err)
	}
}

func TestPrunedTotalsRejectMixedAndConflictingSources(t *testing.T) {
	bestBlock := strings.Repeat("4", 64)
	newStore := func(t *testing.T) (*WatchStore, WatchEntry, WatchEntry) {
		t.Helper()
		dir := t.TempDir()
		t.Setenv("WATCH_STORE", filepath.Join(dir, "wallets.json"))
		t.Setenv("BALANCE_HISTORY", filepath.Join(dir, "history.json"))
		store := NewWatchStore(dir)
		first, _ := store.Add("One", genesisAddress, "")
		second, _ := store.Add("Two", "bc1qcr8te4kr609gcawutmrza0j4xv80jy8z306fyu", "")
		return store, first, second
	}
	t.Run("mixed sources", func(t *testing.T) {
		store, scanned, core := newStore(t)
		if !store.StoreScanIfCurrent(scanned, cachedPrunedScan(scanned, 1, 1, bestBlock, time.Now())) {
			t.Fatal("could not seed scan")
		}
		coreName := coreWalletName(core)
		store.SetCoreWallet(core.Key, coreName)
		walletRPC := func(wallet, method string, params []any, timeout time.Duration) any {
			if wallet != coreName {
				return nil
			}
			switch method {
			case "getwalletinfo":
				return watchInfo(false)
			case "getdescriptorinfo":
				return descriptorInfo(params)
			case "listdescriptors":
				return listedDescriptors(core)
			case "getbalances":
				return map[string]any{"mine": map[string]any{"trusted": 2.0}}
			}
			return nil
		}
		view := store.ListWithChain(walletRPC, blockchainInfo{Pruned: true, BestBlockHash: bestBlock})
		if view["total_state"] != "unavailable" || view["total_reason"] != "mixed_sources" || view["total"] != "0" {
			t.Fatalf("mixed total = %#v", view)
		}
	})
	t.Run("conflicting outpoints", func(t *testing.T) {
		store, first, second := newStore(t)
		firstCache := cachedPrunedScan(first, 1, 1, bestBlock, time.Now())
		secondCache := cachedPrunedScan(second, 2, 1, bestBlock, time.Now())
		if !store.StoreScanIfCurrent(first, firstCache) || !store.StoreScanIfCurrent(second, secondCache) {
			t.Fatal("could not seed conflicting scans")
		}
		view := store.ListWithChain(func(string, string, []any, time.Duration) any { return nil }, blockchainInfo{Pruned: true, BestBlockHash: bestBlock})
		if view["total_state"] != "unavailable" || view["total_reason"] != "conflicting_outpoints" || view["total"] != "0" {
			t.Fatalf("conflicting total = %#v", view)
		}
	})
}

func TestPrunedWatchCacheBecomesStaleAcrossTipChange(t *testing.T) {
	oldBlock := strings.Repeat("1", 64)
	newBlock := strings.Repeat("2", 64)
	mock := &prunedRPCMock{bestBlock: newBlock, result: validPrunedScan(newBlock)}
	a, handler, _ := newPrunedTestApp(t, t.TempDir(), mock)
	entry, err := a.watch.Add("Cold", genesisAddress, "")
	if err != nil {
		t.Fatal(err)
	}
	cache := cachedPrunedScan(entry, 3, 900001, oldBlock, time.Now().Add(-time.Hour))
	if !a.watch.StoreScanIfCurrent(entry, cache) {
		t.Fatal("could not seed scan cache")
	}
	view := responseJSON(t, func() *http.Response {
		server := httptest.NewServer(handler)
		defer server.Close()
		req, _ := http.NewRequest(http.MethodGet, server.URL+"/api/watch", nil)
		req.SetBasicAuth("admin", "secret")
		resp, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}())
	row := view["wallets"].([]any)[0].(map[string]any)
	if row["state"] != "stale" || row["btc"] != "3" || view["total_state"] != "unavailable" || view["total"] != "0" {
		t.Fatalf("stale cache view = %#v", view)
	}
}

func TestPrunedWatchRejectsPrivateMaterialBeforeRPC(t *testing.T) {
	bestBlock := strings.Repeat("f", 64)
	mock := &prunedRPCMock{bestBlock: bestBlock, result: validPrunedScan(bestBlock)}
	_, handler, _ := newPrunedTestApp(t, t.TempDir(), mock)
	response := addPrunedWatch(t, handler, "Secret", syntheticExtendedPrivate("0488ade4"))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("private add response = %d %q", response.Code, response.Body.String())
	}
	mock.mu.Lock()
	defer mock.mu.Unlock()
	if mock.allCalls != 0 {
		t.Fatalf("private material reached RPC %d times", mock.allCalls)
	}
}

func TestPrunedWatchAgainstRealCore(t *testing.T) {
	rpcURL := os.Getenv("PRUNED_WATCH_E2E_RPC_URL")
	address := os.Getenv("PRUNED_WATCH_E2E_ADDRESS")
	if rpcURL == "" || address == "" {
		t.Skip("set PRUNED_WATCH_E2E_RPC_URL and PRUNED_WATCH_E2E_ADDRESS")
	}
	cfg := config{
		rpcURL: rpcURL, rpcUser: os.Getenv("PRUNED_WATCH_E2E_RPC_USER"), rpcPassword: os.Getenv("PRUNED_WATCH_E2E_RPC_PASSWORD"),
		dashboardPassword: "secret", stateDir: t.TempDir(), dataDir: t.TempDir(), version: "test",
	}
	a, err := newApp(cfg, &rpcClient{url: rpcURL, user: cfg.rpcUser, password: cfg.rpcPassword, client: &http.Client{Timeout: 3 * time.Second}}, "index.html", "static", log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a.ctx = ctx
	go a.scanner.run(ctx)
	handler := a.routes()
	if response := addPrunedWatch(t, handler, "Regtest", address); response.Code != http.StatusOK {
		t.Fatalf("add response = %d %q", response.Code, response.Body.String())
	}
	deadline := time.Now().Add(30 * time.Second)
	wantBTC := os.Getenv("PRUNED_WATCH_E2E_BTC")
	if wantBTC == "" {
		wantBTC = "50"
	}
	for {
		row := prunedRow(t, handler)
		if row["state"] == "ok" {
			if row["btc"] != wantBTC || row["balance_kind"] != "confirmed" || row["scan_height"] == nil || row["scan_time"] == nil || len(row["history"].([]any)) != 1 {
				t.Fatalf("real Core scan row = %#v", row)
			}
			waitPrunedScannerIdle(t, a.scanner)
			return
		}
		if row["state"] == "error" || time.Now().After(deadline) {
			t.Fatalf("real Core scan row = %#v", row)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
