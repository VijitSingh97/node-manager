package main

import (
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

type dashboardRPC struct {
	mu          sync.RWMutex
	unavailable bool
	chain       blockchainInfo
	wallets     map[string]float64
	entries     map[string]WatchEntry
}

func (f *dashboardRPC) setChain(chain blockchainInfo) {
	f.mu.Lock()
	f.chain = chain
	f.mu.Unlock()
}

func (f *dashboardRPC) setUnavailable(value bool) {
	f.mu.Lock()
	f.unavailable = value
	f.mu.Unlock()
}

func (f *dashboardRPC) handler(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Method string `json:"method"`
		Params []any  `json:"params"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	f.mu.RLock()
	unavailable := f.unavailable
	chain := f.chain
	balance, knownWallet := f.wallets[strings.TrimPrefix(r.URL.Path, "/wallet/")]
	entry := f.entries[strings.TrimPrefix(r.URL.Path, "/wallet/")]
	f.mu.RUnlock()
	if unavailable && (request.Method == "getblockchaininfo" || strings.HasPrefix(r.URL.Path, "/wallet/")) {
		http.Error(w, "node unavailable", http.StatusServiceUnavailable)
		return
	}

	var result any
	switch request.Method {
	case "getblockchaininfo":
		result = chain
	case "getnetworkinfo":
		result = map[string]any{"subversion": "/Satoshi:31.1.0/", "connections": 2}
	case "getpeerinfo":
		result = []any{map[string]any{"inbound": true}, map[string]any{"inbound": false}}
	case "uptime":
		result = 3661
	case "getmempoolinfo":
		result = map[string]any{"size": 42, "bytes": 2 * 1024 * 1024}
	case "estimatesmartfee":
		result = map[string]any{"feerate": 0.00001234}
	case "getwalletinfo":
		if knownWallet {
			result = map[string]any{"descriptors": true, "private_keys_enabled": false, "scanning": false}
		}
	case "getbalances":
		if knownWallet {
			result = map[string]any{"mine": map[string]any{"trusted": balance}}
		}
	case "getdescriptorinfo":
		if knownWallet {
			result = descriptorInfo(request.Params)
		}
	case "listdescriptors":
		if knownWallet {
			result = listedDescriptors(entry)
		}
	}
	if result == nil {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"result": result, "error": nil})
}

type dashboardFixture struct {
	app    *app
	server *httptest.Server
	rpc    *dashboardRPC
}

func newDashboardFixture(t *testing.T) *dashboardFixture {
	t.Helper()
	t.Setenv("TELEGRAM_BOT_TOKEN", "")
	t.Setenv("TELEGRAM_CHAT_ID", "")
	t.Setenv("HEALTHCHECKS_URL", "")
	rpcFixture := &dashboardRPC{
		chain:   blockchainInfo{Blocks: 900000, Headers: 900001, VerificationProgress: 0.9999, InitialBlockDownload: false, SizeOnDisk: 12 * 1024 * 1024},
		wallets: make(map[string]float64), entries: make(map[string]WatchEntry),
	}
	rpcServer := httptest.NewServer(http.HandlerFunc(rpcFixture.handler))
	t.Cleanup(rpcServer.Close)
	root := filepath.Dir(sourceFile(t))
	cfg := config{
		rpcURL: rpcServer.URL, rpcUser: "rpc-user", rpcPassword: "rpc-password",
		dashboardPassword: "dashboard-secret",
		dataDir:           t.TempDir(), stateDir: t.TempDir(), version: "1.2.0",
	}
	a, err := newApp(cfg, &rpcClient{url: rpcServer.URL, user: cfg.rpcUser, password: cfg.rpcPassword, client: rpcServer.Client()}, filepath.Join(root, "index.html"), filepath.Join(root, "static"), log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatal(err)
	}
	blocked := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return nil, errors.New("unexpected external request: " + r.URL.String())
	})}
	a.prices.client = blocked
	a.monitor.client = blocked
	a.monitor.output = io.Discard
	server := httptest.NewServer(a.routes())
	t.Cleanup(server.Close)
	return &dashboardFixture{app: a, server: server, rpc: rpcFixture}
}

func (f *dashboardFixture) request(t *testing.T, method, path string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, f.server.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.SetBasicAuth("admin", "dashboard-secret")
	if method != http.MethodGet {
		req.Header.Set("X-Requested-With", "fetch")
	}
	resp, err := f.server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func responseBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func responseJSON(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	defer resp.Body.Close()
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	return body
}

func TestDashboardNodePageTransitionsFromSyncedToInitializing(t *testing.T) {
	fixture := newDashboardFixture(t)
	resp := fixture.request(t, http.MethodGet, "/")
	body := responseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("synced page status = %d", resp.StatusCode)
	}
	for _, want := range []string{
		"Bitcoin Node Status", "900000 / 900001", "42 tx · 2.0 MB", "1.2 / 1.2 / 1.2",
		`id="btc-price"`, `id="watch"`, `/static/prices.js`, `/static/watch.js`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("synced page missing %q", want)
		}
	}

	fixture.rpc.setUnavailable(true)
	resp = fixture.request(t, http.MethodGet, "/")
	body = responseBody(t, resp)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "Bitcoin Node Initializing") || strings.Contains(body, "900000 / 900001") {
		t.Fatalf("initializing page = %d %q", resp.StatusCode, body)
	}
}

func TestDashboardKeepsOnlyLastConfirmedInProgressSyncForRPCFailures(t *testing.T) {
	fixture := newDashboardFixture(t)
	fixture.rpc.setChain(blockchainInfo{Blocks: 300000, Headers: 900001, VerificationProgress: 0.25, InitialBlockDownload: true, Pruned: true})

	body := responseBody(t, fixture.request(t, http.MethodGet, "/"))
	if !strings.Contains(body, "25.00% synced") || !strings.Contains(body, `data-sync-percent="25.000000"`) || strings.Contains(body, "Last known:") {
		t.Fatalf("live sync page = %q", body)
	}

	fixture.rpc.setUnavailable(true)
	body = responseBody(t, fixture.request(t, http.MethodGet, "/"))
	for _, want := range []string{"Bitcoin Node Syncing", "Waiting for a fresh response", "Last known: 25.00% synced", "Last confirmed ", "block 300000 of 900001", `data-sync-percent="25.000000"`} {
		if !strings.Contains(body, want) {
			t.Errorf("stale sync page missing %q", want)
		}
	}
	if strings.Contains(body, "<h2>Bitcoin Node Status") {
		t.Error("stale sync page displayed normal status as live")
	}

	fixture.rpc.setUnavailable(false)
	fixture.rpc.setChain(blockchainInfo{Blocks: 400000, Headers: 900002, VerificationProgress: 0.5, InitialBlockDownload: true, Pruned: true})
	body = responseBody(t, fixture.request(t, http.MethodGet, "/"))
	if !strings.Contains(body, "50.00% synced") || !strings.Contains(body, `data-sync-percent="50.000000"`) || strings.Contains(body, "Last known:") {
		t.Fatalf("fresh recovered sync page = %q", body)
	}

	fixture.rpc.setChain(blockchainInfo{Blocks: 900002, Headers: 900002, VerificationProgress: 1, InitialBlockDownload: false, Pruned: true})
	body = responseBody(t, fixture.request(t, http.MethodGet, "/"))
	if !strings.Contains(body, "Bitcoin Node Status") || !strings.Contains(body, `data-sync-percent="100.000000"`) || strings.Contains(body, "% synced") {
		t.Fatalf("synced page = %q", body)
	}

	fixture.rpc.setUnavailable(true)
	body = responseBody(t, fixture.request(t, http.MethodGet, "/"))
	if !strings.Contains(body, "Bitcoin Node Initializing") || strings.Contains(body, "Last known:") || strings.Contains(body, "% synced") {
		t.Fatalf("post-sync failure page = %q", body)
	}
}

func TestDashboardPricesExposeFreshUnavailableAndCachedStaleStates(t *testing.T) {
	fixture := newDashboardFixture(t)
	now := time.Unix(1_800_000_000, 0)
	fixture.app.prices.now = func() time.Time { return now }
	providerStatus := http.StatusOK
	fixture.app.prices.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != coinGeckoPriceURL {
			return nil, errors.New("unexpected provider URL: " + r.URL.String())
		}
		if providerStatus != http.StatusOK {
			return response(providerStatus, "provider down"), nil
		}
		return response(http.StatusOK, `{"bitcoin":{"usd":70000,"eur":65000,"gbp":55000,"inr":5800000,"cad":95000,"aud":105000,"jpy":11000000,"rub":6500000,"cny":500000,"last_updated_at":1800000000}}`), nil
	})}

	if err := fixture.app.prices.refresh(t.Context()); err != nil {
		t.Fatal(err)
	}
	fresh := responseJSON(t, fixture.request(t, http.MethodGet, "/api/prices"))
	prices, ok := fresh["prices"].(map[string]any)
	if !ok || len(prices) != 9 || fresh["available"] != true || fresh["stale"] != false || prices["usd"] != float64(70000) {
		t.Fatalf("fresh prices = %#v", fresh)
	}

	providerStatus = http.StatusServiceUnavailable
	if err := fixture.app.prices.refresh(t.Context()); err == nil {
		t.Fatal("provider failure unexpectedly succeeded")
	}
	cached := responseJSON(t, fixture.request(t, http.MethodGet, "/api/prices"))
	if cached["available"] != true || cached["stale"] != true || cached["updated_at"] != float64(1_800_000_000) {
		t.Fatalf("cached stale prices = %#v", cached)
	}

	empty := newDashboardFixture(t)
	unavailable := responseJSON(t, empty.request(t, http.MethodGet, "/api/prices"))
	if unavailable["available"] != false || unavailable["stale"] != true || unavailable["updated_at"] != float64(0) {
		t.Fatalf("unavailable prices = %#v", unavailable)
	}
}

func TestDashboardWalletViewFormatsBalancesAndPersonalTotal(t *testing.T) {
	fixture := newDashboardFixture(t)
	entries := []WatchEntry{
		{Name: "Savings", Key: genesisAddress},
		{Name: "Pending", Key: "bc1qcr8te4kr609gcawutmrza0j4xv80jy8z306fyu"},
		{Name: publicExampleName, Key: publicExampleAddress},
	}
	balances := []float64{1.23456789, 8, 500}
	for i, entry := range entries {
		saved, err := fixture.app.watch.Add(entry.Name, entry.Key, "")
		if err != nil {
			t.Fatal(err)
		}
		name := coreWalletName(saved)
		fixture.app.watch.SetCoreWallet(saved.Key, name)
		fixture.rpc.wallets[name] = balances[i]
		fixture.rpc.entries[name] = saved
	}
	fixture.app.watch.SetPending(entries[1].Key, true)

	view := responseJSON(t, fixture.request(t, http.MethodGet, "/api/watch"))
	rows, ok := view["wallets"].([]any)
	if !ok || len(rows) != 3 {
		t.Fatalf("wallet rows = %#v", view["wallets"])
	}
	savings := rows[0].(map[string]any)
	pending := rows[1].(map[string]any)
	example := rows[2].(map[string]any)
	if savings["btc"] != "1.23456789" || savings["state"] != "ok" {
		t.Fatalf("savings row = %#v", savings)
	}
	if pending["btc"] != nil || pending["state"] != "pending" {
		t.Fatalf("pending row = %#v", pending)
	}
	if example["btc"] != "500" || example["display_name"] != "Binance" || example["example"] != true {
		t.Fatalf("public example row = %#v", example)
	}
	if view["total"] != "1.23456789" || view["total_state"] != "pending" || view["show_total"] != true {
		t.Fatalf("personal total = %#v", view)
	}

	fixture.app.watch.history.record(entries[0].Key, balances[0], time.Now())
	fixture.rpc.setUnavailable(true)
	stale := responseJSON(t, fixture.request(t, http.MethodGet, "/api/watch"))
	staleSavings := stale["wallets"].([]any)[0].(map[string]any)
	if staleSavings["state"] != "stale" || staleSavings["btc"] != "1.23456789" {
		t.Fatalf("stale savings row = %#v", staleSavings)
	}
}

func TestDashboardStartupRemovesOnlyLegacyUpgradeRequest(t *testing.T) {
	root := filepath.Dir(sourceFile(t))
	stateDir := t.TempDir()
	marker := filepath.Join(stateDir, "upgrade.request")
	other := filepath.Join(stateDir, "unrelated.json")
	if err := os.WriteFile(marker, []byte("requested\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(other, []byte("keep\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config{dashboardPassword: "dashboard-secret", stateDir: stateDir, dataDir: t.TempDir(), version: "1.2.0"}
	if _, err := newApp(cfg, &rpcClient{}, filepath.Join(root, "index.html"), filepath.Join(root, "static"), log.New(io.Discard, "", 0)); err != nil {
		t.Fatalf("start dashboard: %v", err)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("legacy marker still exists: %v", err)
	}
	if data, err := os.ReadFile(other); err != nil || string(data) != "keep\n" {
		t.Fatalf("unrelated state changed: %q, %v", data, err)
	}
}

func TestDashboardStartupFailsWhenLegacyUpgradeRequestCannotBeRemoved(t *testing.T) {
	root := filepath.Dir(sourceFile(t))
	stateDir := t.TempDir()
	marker := filepath.Join(stateDir, "upgrade.request")
	if err := os.Mkdir(marker, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(marker, "unexpected"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config{dashboardPassword: "dashboard-secret", stateDir: stateDir, dataDir: t.TempDir(), version: "1.2.0"}
	_, err := newApp(cfg, &rpcClient{}, filepath.Join(root, "index.html"), filepath.Join(root, "static"), log.New(io.Discard, "", 0))
	if err == nil || err.Error() != "legacy upgrade request cleanup unavailable" {
		t.Fatalf("startup error = %v", err)
	}
}

func TestDashboardUpdateNoticeIsInformational(t *testing.T) {
	fixture := newDashboardFixture(t)
	fixture.app.monitor.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "api.github.com" || r.URL.Path != "/repos/VijitSingh97/node-manager/releases/latest" {
			return nil, errors.New("unexpected external request: " + r.URL.String())
		}
		return response(http.StatusOK, `{"tag_name":"v1.3.0"}`), nil
	})}
	fixture.app.monitor.checkUpdates(t.Context())

	page := responseBody(t, fixture.request(t, http.MethodGet, "/"))
	for _, want := range []string{
		"stack v1.3.0 available (running v1.2.0)",
		"sudo apt install --only-upgrade node-manager",
		"node-manager pull",
		"brew upgrade node-manager",
		"node-manager apply",
		"https://github.com/VijitSingh97/node-manager/releases/latest",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("update notice missing %q", want)
		}
	}
	if strings.Contains(page, "Upgrade now") || strings.Contains(page, "/static/upgrade.js") {
		t.Fatal("page still offers an in-app upgrade")
	}
	if strings.Contains(page, "sudo node-manager pull") || strings.Contains(page, "sudo node-manager apply") {
		t.Fatal("APT instructions still require manual container activation")
	}
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		resp := fixture.request(t, method, "/api/upgrade")
		if resp.StatusCode < 400 {
			t.Errorf("%s /api/upgrade = %d", method, resp.StatusCode)
		}
		resp.Body.Close()
	}
	if _, err := os.Stat(filepath.Join(fixture.app.cfg.stateDir, "upgrade.request")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("upgrade marker exists: %v", err)
	}

	for _, state := range []string{"bitcoin disabled", "bitcoin loading"} {
		t.Run(state, func(t *testing.T) {
			fixture := newDashboardFixture(t)
			fixture.app.monitor.updateNote = "stack v1.3.0 available (running v1.2.0)"
			if state == "bitcoin disabled" {
				fixture.app.cfg.bitcoinDisabled = true
			} else {
				fixture.rpc.setUnavailable(true)
			}
			page := responseBody(t, fixture.request(t, http.MethodGet, "/"))
			for _, want := range []string{`id="update-notice"`, `id="settings-update"`, "stack v1.3.0 available", "docs/operations.md#upgrading"} {
				if !strings.Contains(page, want) {
					t.Errorf("%s page missing %q", state, want)
				}
			}
		})
	}
}
