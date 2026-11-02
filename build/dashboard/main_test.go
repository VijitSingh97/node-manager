package main

import (
	"context"
	"encoding/json"
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

	nethtml "golang.org/x/net/html"
)

type rpcFixture struct {
	mu                   sync.Mutex
	calls                []string
	invalid              bool
	initialBlockDownload bool
}

type lifecycleRPC struct {
	mu            sync.Mutex
	wallets       map[string][]map[string]any
	unloaded      []string
	calls         []string
	blockWallet   string
	importStarted chan struct{}
	releaseImport chan struct{}
	chainStarted  chan struct{}
	releaseChain  chan struct{}
	importOnce    sync.Once
	chainOnce     sync.Once
}

func newLifecycleRPC() *lifecycleRPC {
	return &lifecycleRPC{wallets: map[string][]map[string]any{}}
}

func (f *lifecycleRPC) handler(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Method string `json:"method"`
		Params []any  `json:"params"`
	}
	_ = json.NewDecoder(r.Body).Decode(&request)
	f.mu.Lock()
	f.calls = append(f.calls, request.Method)
	f.mu.Unlock()
	wallet := strings.TrimPrefix(r.URL.Path, "/wallet/")
	var result any
	switch request.Method {
	case "getblockchaininfo":
		if f.chainStarted != nil {
			f.chainOnce.Do(func() { close(f.chainStarted) })
		}
		if f.releaseChain != nil {
			<-f.releaseChain
		}
		result = map[string]any{"pruned": false, "initialblockdownload": false}
	case "getnetworkinfo":
		result = map[string]any{"subversion": "/Satoshi:31.1.0/"}
	case "createwallet":
		name := request.Params[0].(string)
		f.mu.Lock()
		f.wallets[name] = nil
		f.mu.Unlock()
		result = map[string]any{"name": name}
	case "loadwallet":
	case "unloadwallet":
		name := request.Params[0].(string)
		f.mu.Lock()
		f.unloaded = append(f.unloaded, name)
		f.mu.Unlock()
		result = map[string]any{}
	case "getwalletinfo":
		f.mu.Lock()
		_, ok := f.wallets[wallet]
		f.mu.Unlock()
		if ok {
			result = map[string]any{"descriptors": true, "private_keys_enabled": false, "scanning": false}
		}
	case "getdescriptorinfo":
		desc := request.Params[0].(string)
		result = map[string]any{"descriptor": strings.SplitN(desc, "#", 2)[0] + "#fixture", "hasprivatekeys": false, "isrange": strings.Contains(desc, "/*")}
	case "importdescriptors":
		if wallet == f.blockWallet && f.importStarted != nil {
			f.importOnce.Do(func() { close(f.importStarted) })
			<-f.releaseImport
		}
		requests := request.Params[0].([]any)
		listed := make([]map[string]any, 0, len(requests))
		results := make([]any, 0, len(requests))
		for _, value := range requests {
			item := value.(map[string]any)
			listedItem := map[string]any{"desc": item["desc"], "internal": item["internal"], "timestamp": item["timestamp"]}
			if _, ok := item["range"]; ok {
				listedItem["range"] = []any{float64(0), float64(999)}
			}
			listed = append(listed, listedItem)
			results = append(results, map[string]any{"success": true})
		}
		f.mu.Lock()
		f.wallets[wallet] = listed
		f.mu.Unlock()
		result = results
	case "listdescriptors":
		f.mu.Lock()
		descriptors, ok := f.wallets[wallet]
		f.mu.Unlock()
		if ok {
			values := make([]any, len(descriptors))
			for index := range descriptors {
				values[index] = descriptors[index]
			}
			result = map[string]any{"descriptors": values}
		}
	}
	status := http.StatusOK
	if result == nil {
		status = http.StatusInternalServerError
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"result": result, "error": nil})
}

func (f *lifecycleRPC) countMethod(method string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	count := 0
	for _, call := range f.calls {
		if call == method {
			count++
		}
	}
	return count
}

func (f *rpcFixture) handler(w http.ResponseWriter, r *http.Request) {
	if user, password, ok := r.BasicAuth(); !ok || user != "rpc-user" || password != "rpc-password" {
		http.Error(w, "bad auth", http.StatusUnauthorized)
		return
	}
	var request struct {
		Method string `json:"method"`
	}
	if json.NewDecoder(r.Body).Decode(&request) != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	f.calls = append(f.calls, request.Method)
	f.mu.Unlock()
	if f.invalid {
		_, _ = io.WriteString(w, "not json")
		return
	}
	results := map[string]any{
		"getblockchaininfo": map[string]any{"blocks": 900000, "headers": 900001, "verificationprogress": 0.9999, "initialblockdownload": f.initialBlockDownload, "pruned": true, "prune_target_size": 10 * 1024 * 1024 * 1024, "size_on_disk": 11 * 1024 * 1024 * 1024},
		"getnetworkinfo":    map[string]any{"subversion": `<script>alert("x")</script>`, "connections": 10},
		"getpeerinfo":       []any{map[string]any{"inbound": true}, map[string]any{"inbound": false}},
		"uptime":            90061,
		"getmempoolinfo":    map[string]any{"size": 4200, "bytes": 12 * 1024 * 1024},
		"estimatesmartfee":  map[string]any{"feerate": 0.0001},
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"result": results[request.Method], "error": nil, "id": "go-dashboard"})
}

func (f *rpcFixture) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func testApp(t *testing.T, fixture *rpcFixture, password string) (*app, http.Handler) {
	t.Helper()
	if password == "" {
		password = "dashboard-secret"
	}
	rpcServer := httptest.NewServer(http.HandlerFunc(fixture.handler))
	t.Cleanup(rpcServer.Close)
	root := filepath.Dir(sourceFile(t))
	cfg := config{rpcURL: rpcServer.URL, rpcUser: "rpc-user", rpcPassword: "rpc-password", dashboardPassword: password, dataDir: t.TempDir(), stateDir: t.TempDir(), version: "1.31.1"}
	rpc := &rpcClient{url: cfg.rpcURL, user: cfg.rpcUser, password: cfg.rpcPassword, client: &http.Client{Timeout: time.Second}}
	a, err := newApp(cfg, rpc, filepath.Join(root, "index.html"), filepath.Join(root, "static"), log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatal(err)
	}
	a.monitor.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return response(http.StatusServiceUnavailable, ""), nil
	})}
	a.monitor.output = io.Discard
	return a, a.routes()
}

func sourceFile(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, "main_test.go")
}

func request(t *testing.T, handler http.Handler, method, path, password string) *httptest.ResponseRecorder {
	t.Helper()
	if password == "" {
		password = "dashboard-secret"
	}
	req := httptest.NewRequest(method, path, nil)
	req.SetBasicAuth("admin", password)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	return w
}

func anonymousRequest(t *testing.T, handler http.Handler, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	return w
}

func initializeTestDashboardAuth(t *testing.T, a *app) {
	t.Helper()
	var err error
	a.dashboardAuth, err = newDashboardAuth(a.cfg.stateDir, a.cfg.dashboardPassword)
	if err != nil {
		t.Fatal(err)
	}
	a.sessions = newDashboardSessions()
	a.loginLimiter = newLoginLimiter()
	a.passwordWork = newPasswordWorkLimiter()
	a.passwordAttempts = make(chan struct{}, 2)
}

func assertBitcoinPersistentMounts(t *testing.T, body string, enabled bool) {
	t.Helper()
	want := 0
	if enabled {
		want = 1
	}
	for _, marker := range []string{`id="tower-stats"`, `id="watch"`, `id="btc-price"`, `/static/watch.js`} {
		if count := strings.Count(body, marker); count != want {
			t.Errorf("%q count = %d, want %d", marker, count, want)
		}
	}
}

func TestDashboardCannotBeFramed(t *testing.T) {
	_, handler := testApp(t, &rpcFixture{}, "dashboard-secret")
	for _, path := range []string{"/", "/api/settings", "/api/settings/ports", "/missing"} {
		for _, password := range []string{"", "dashboard-secret"} {
			response := request(t, handler, http.MethodGet, path, password)
			if password == "" {
				response = anonymousRequest(t, handler, http.MethodGet, path)
			}
			if !strings.Contains(response.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") || response.Header().Get("X-Frame-Options") != "DENY" {
				t.Errorf("%s omitted frame protections for status %d", path, response.Code)
			}
		}
	}
}

func TestIndexAuthRPCParityAndEscaping(t *testing.T) {
	t.Setenv("NODE_NAME", "mini-1 <node>")
	fixture := &rpcFixture{}
	a, _ := testApp(t, fixture, "dashboard-secret")
	a.cfg.bitcoinRPCEndpoint = "192.168.1.50:8332"
	handler := a.routes()
	if got := anonymousRequest(t, handler, http.MethodGet, "/"); got.Code != http.StatusFound || got.Header().Get("Location") != "/login" || got.Header().Get("WWW-Authenticate") != "" {
		t.Fatalf("unauthenticated response = %d, location %q, challenge %q", got.Code, got.Header().Get("Location"), got.Header().Get("WWW-Authenticate"))
	}
	if got := request(t, handler, http.MethodGet, "/", "wrong"); got.Code != http.StatusUnauthorized || got.Header().Get("WWW-Authenticate") != "" {
		t.Fatalf("wrong password = %d, challenge %q", got.Code, got.Header().Get("WWW-Authenticate"))
	}
	if got := anonymousRequest(t, handler, http.MethodGet, "/api/prices"); got.Code != http.StatusUnauthorized || got.Header().Get("WWW-Authenticate") != "" {
		t.Fatalf("unauthenticated prices = %d, challenge %q", got.Code, got.Header().Get("WWW-Authenticate"))
	}
	if got := request(t, handler, http.MethodGet, "/api/prices", "dashboard-secret"); got.Code != http.StatusOK {
		t.Fatalf("authenticated prices = %d", got.Code)
	}
	got := request(t, handler, http.MethodGet, "/", "dashboard-secret")
	if got.Code != http.StatusOK {
		t.Fatalf("status = %d", got.Code)
	}
	body := got.Body.String()
	assertBitcoinPersistentMounts(t, body, true)
	for _, want := range []string{"900000 / 900001", "4200 tx", "10 / 10 / 10", "Pruned", `id="btc-price"`, `/static/prices.js`, "192.168.1.50:8332 · LAN", "Configured LAN access", `class="node-set-title">mini-1 &lt;node&gt; Node Set</h1>`} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
	if strings.Contains(body, `<script>alert("x")</script>`) || !strings.Contains(body, "&lt;script&gt;") {
		t.Error("RPC value was not HTML escaped")
	}
	if fixture.count() != 8 {
		t.Fatalf("root RPC calls = %d, want 8", fixture.count())
	}
}

func TestInvalidRPCRendersLoading(t *testing.T) {
	t.Setenv("NODE_NAME", "mini-1")
	fixture := &rpcFixture{invalid: true}
	_, handler := testApp(t, fixture, "")
	got := request(t, handler, http.MethodGet, "/", "")
	if got.Code != http.StatusOK || !strings.Contains(got.Body.String(), "Bitcoin Node Initializing") {
		t.Fatalf("response = %d %q", got.Code, got.Body.String())
	}
	assertBitcoinPersistentMounts(t, got.Body.String(), true)
	if strings.Contains(got.Body.String(), "% synced") {
		t.Fatal("unavailable Core must not invent a sync percentage")
	}
	if !strings.Contains(got.Body.String(), `id="monero-panel"`) || !strings.Contains(got.Body.String(), `/static/nodes.js`) {
		t.Fatal("Bitcoin loading page omitted the independent Monero panel")
	}
	if !strings.Contains(got.Body.String(), `class="node-set-title">mini-1 Node Set</h1>`) {
		t.Fatal("loading overview omitted the node set title")
	}
}

func TestInitialSyncShowsLivePercentageWithoutHidingStatus(t *testing.T) {
	fixture := &rpcFixture{initialBlockDownload: true}
	_, handler := testApp(t, fixture, "")
	got := request(t, handler, http.MethodGet, "/", "")
	body := got.Body.String()
	for _, want := range []string{"Syncing Bitcoin Core", `class="sync-percentage"`, "99.99% synced", "Bitcoin Node Status", "900000 / 900001", `id="watch"`} {
		if !strings.Contains(body, want) {
			t.Errorf("sync page missing %q", want)
		}
	}
	if strings.Contains(body, "Bitcoin Node Initializing") {
		t.Error("a syncing node must retain its normal status view")
	}
}

func TestHealthBlockchainUsesDedicatedDeadlineWithoutCachingHealth(t *testing.T) {
	available := true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(20 * time.Millisecond)
		if !available {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{
			"blocks": 300000, "headers": 900000, "verificationprogress": 0.25, "initialblockdownload": true,
		}, "error": nil})
	}))
	defer server.Close()

	a := &app{rpc: &rpcClient{url: server.URL, client: &http.Client{Transport: server.Client().Transport, Timeout: time.Millisecond}}}
	if info := a.healthBlockchain(t.Context()); info == nil {
		t.Fatal("health poll inherited the interactive RPC timeout")
	}
	loading := a.loadingPageData()
	if !loading.LastKnownSync || loading.Progress != 25 || loading.Blocks != 300000 {
		t.Fatalf("display sync snapshot = %#v", loading)
	}

	available = false
	if info := a.healthBlockchain(t.Context()); info != nil {
		t.Fatalf("failed fresh health poll returned cached data: %#v", info)
	}
	if loading = a.loadingPageData(); !loading.LastKnownSync || loading.Progress != 25 {
		t.Fatalf("display snapshot was not retained after health failure: %#v", loading)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	started := time.Now()
	if info := a.healthBlockchain(ctx); info != nil || time.Since(started) > 100*time.Millisecond {
		t.Fatalf("cancelled health poll = %#v after %s", info, time.Since(started))
	}
}

func TestRPCNilParamsUsesEmptyArray(t *testing.T) {
	var params json.RawMessage
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Params json.RawMessage `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		params = body.Params
		_ = json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{"blocks": 1}, "error": nil})
	}))
	defer server.Close()
	rpc := &rpcClient{url: server.URL, client: server.Client()}
	var result blockchainInfo
	if err := rpc.call(t.Context(), "getblockchaininfo", nil, &result); err != nil {
		t.Fatal(err)
	}
	if string(params) != "[]" {
		t.Fatalf("params = %s, want []", params)
	}
}

func TestMetricsAndReadAPIs(t *testing.T) {
	fixture := &rpcFixture{}
	a, handler := testApp(t, fixture, "")
	a.recordFee(4.2)
	metrics := request(t, handler, http.MethodGet, "/metrics", "")
	for _, want := range []string{"bitcoin_node_up 1", "bitcoin_blocks 900000", `bitcoin_peers{direction="in"} 1`, `bitcoin_fee_sat_vb{blocks="6"} 10`} {
		if !strings.Contains(metrics.Body.String(), want) {
			t.Errorf("metrics missing %q", want)
		}
	}
	if fixture.count() != 8 {
		t.Fatalf("metrics RPC calls = %d, want 8", fixture.count())
	}
	fees := request(t, handler, http.MethodGet, "/api/fees", "")
	if fees.Code != http.StatusOK || !strings.Contains(fees.Body.String(), "4.2") {
		t.Fatalf("fees = %d %q", fees.Code, fees.Body.String())
	}
	a.prices.mu.Lock()
	a.prices.assetPrices["bitcoin"] = map[string]float64{"usd": 70000}
	a.prices.assetUpdatedAt["bitcoin"] = 1_800_000_000
	a.prices.assetFailed["bitcoin"] = false
	a.prices.mu.Unlock()
	a.prices.now = func() time.Time { return time.Unix(1_800_000_000, 0) }
	priceCalls := 0
	a.prices.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		priceCalls++
		return response(http.StatusInternalServerError, ""), nil
	})}
	prices := request(t, handler, http.MethodGet, "/api/prices", "")
	if prices.Code != http.StatusOK || !strings.Contains(prices.Body.String(), `"usd":70000`) || !strings.Contains(prices.Body.String(), `"available":true`) {
		t.Fatalf("prices = %d %q", prices.Code, prices.Body.String())
	}
	if priceCalls != 0 {
		t.Fatalf("prices handler fetched provider: calls = %d", priceCalls)
	}
	watch := request(t, handler, http.MethodGet, "/api/watch", "")
	if watch.Code != http.StatusOK || !strings.Contains(watch.Body.String(), `"wallets":[]`) {
		t.Fatalf("watch = %d %q", watch.Code, watch.Body.String())
	}
}

func TestFeeHistoryBoundedAndResets(t *testing.T) {
	a, _ := testApp(t, &rpcFixture{}, "")
	for i := 0; i < maxFees+2; i++ {
		a.recordFee(float64(i))
	}
	if len(a.fees) != maxFees || a.fees[0] != 2 {
		t.Fatal("fee ring lost bound or order")
	}
	b, _ := testApp(t, &rpcFixture{}, "")
	if len(b.fees) != 0 {
		t.Fatal("fee history must reset on restart like Python")
	}
}

func TestRuntimeConfigDefersSavedPasswordLoadingToStartup(t *testing.T) {
	t.Setenv("RPC_USER", "rpc-user")
	t.Setenv("RPC_PASSWORD", "rpc-password")
	t.Setenv("DASHBOARD_PASSWORD", "")
	t.Setenv("STACK_VERSION", " 1.31.1 ")
	t.Setenv("SETTINGS_PORT", "")
	cfg, err := envConfig()
	if err != nil || cfg.dashboardPassword != "" || cfg.version != "1.31.1" || cfg.settingsPort != "" {
		t.Fatalf("configuration: %#v %v", cfg, err)
	}
}

func TestBitcoinEnabledDefaultsOnAndCanBeDisabledWithoutRPCCredentials(t *testing.T) {
	t.Setenv("MONERO_ENABLED", "")
	t.Setenv("BITCOIN_ENABLED", "")
	t.Setenv("RPC_USER", "rpc-user")
	t.Setenv("RPC_PASSWORD", "rpc-password")
	cfg, err := envConfig()
	if err != nil || cfg.bitcoinDisabled {
		t.Fatalf("default Bitcoin configuration: %#v %v", cfg, err)
	}

	t.Setenv("BITCOIN_ENABLED", "false")
	t.Setenv("RPC_USER", "")
	t.Setenv("RPC_PASSWORD", "")
	cfg, err = envConfig()
	if err != nil || !cfg.bitcoinDisabled {
		t.Fatalf("disabled Bitcoin configuration: %#v %v", cfg, err)
	}
}

func TestBitcoinDisabledDoesNotReachRPCOrStartBitcoinWorkers(t *testing.T) {
	fixture := &rpcFixture{}
	rpcServer := httptest.NewServer(http.HandlerFunc(fixture.handler))
	defer rpcServer.Close()

	cfg := config{bitcoinDisabled: true, rpcURL: rpcServer.URL, dashboardPassword: "dashboard-secret", stateDir: t.TempDir(), dataDir: t.TempDir(), version: "1.31.1"}
	a, err := newApp(cfg, &rpcClient{url: rpcServer.URL, client: rpcServer.Client()}, "index.html", "static", log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatal(err)
	}

	handler := a.routes()
	index := request(t, handler, http.MethodGet, "/", "")
	if index.Code != http.StatusOK || !strings.Contains(index.Body.String(), "Not configured") || strings.Contains(index.Body.String(), "Bitcoin Node Initializing") || strings.Contains(index.Body.String(), "/static/watch.js") {
		t.Fatalf("disabled index = %d %q", index.Code, index.Body.String())
	}
	assertBitcoinPersistentMounts(t, index.Body.String(), false)
	for _, marker := range []string{`id="monero-panel"`, `id="tari-panel"`, `id="litecoin-panel"`, `id="dogecoin-panel"`, `id="bitcoincash-panel"`, `id="zcash-panel"`, `id="settings-dialog"`, `/static/nodes.js`, `/static/prices.js`, `/static/settings.js`} {
		if !strings.Contains(index.Body.String(), marker) {
			t.Fatalf("Bitcoin-disabled page omitted shared dashboard marker %q", marker)
		}
	}
	metrics := request(t, handler, http.MethodGet, "/metrics", "")
	if metrics.Code != http.StatusOK || !strings.Contains(metrics.Body.String(), "bitcoin_node_enabled 0") || !strings.Contains(metrics.Body.String(), "bitcoin_node_up 0") {
		t.Fatalf("disabled metrics = %d %q", metrics.Code, metrics.Body.String())
	}
	for _, check := range []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/watch"},
		{http.MethodPost, "/api/watch"},
		{http.MethodDelete, "/api/watch/Cold"},
	} {
		response := request(t, handler, check.method, check.path, "")
		if response.Code != http.StatusServiceUnavailable || strings.TrimSpace(response.Body.String()) != "Bitcoin node is not configured" {
			t.Fatalf("%s %s = %d %q", check.method, check.path, response.Code, response.Body.String())
		}
	}
	if a.nodeRPC("getblockchaininfo", nil) != nil || a.walletRPC("wallet", "getbalances", nil, time.Second) != nil {
		t.Fatal("disabled Bitcoin RPC helpers returned data")
	}

	done := a.startBackground(context.Background())
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("disabled Bitcoin background workers did not stop immediately")
	}
	if fixture.count() != 0 {
		t.Fatalf("disabled Bitcoin made %d RPC requests", fixture.count())
	}
}

func TestBitcoinUnavailablePageKeepsSharedPanelsAndSettings(t *testing.T) {
	_, handler := testApp(t, &rpcFixture{invalid: true}, "")
	response := request(t, handler, http.MethodGet, "/", "")
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Bitcoin Node Initializing") {
		t.Fatalf("unavailable Bitcoin page = %d %q", response.Code, response.Body.String())
	}
	for _, marker := range []string{`id="monero-panel"`, `id="tari-panel"`, `id="litecoin-panel"`, `id="dogecoin-panel"`, `id="bitcoincash-panel"`, `id="zcash-panel"`, `id="settings-dialog"`} {
		if !strings.Contains(response.Body.String(), marker) {
			t.Fatalf("unavailable Bitcoin page omitted shared dashboard marker %q", marker)
		}
	}
}

func TestSettingsPortConfigIsOptionalDistinctAndValid(t *testing.T) {
	t.Setenv("RPC_USER", "rpc-user")
	t.Setenv("RPC_PASSWORD", "rpc-password")
	t.Setenv("PORT", "8000")
	t.Setenv("SETTINGS_PORT", "8001")
	cfg, err := envConfig()
	if err != nil || cfg.settingsPort != "8001" {
		t.Fatalf("valid settings port: %#v %v", cfg, err)
	}
	for _, value := range []string{"8000", "08000", "0", "65536", "invalid"} {
		t.Setenv("SETTINGS_PORT", value)
		if _, err := envConfig(); err == nil {
			t.Fatalf("SETTINGS_PORT=%q was accepted", value)
		}
	}
}

func TestServeHTTPServersStopsAllListeners(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	servers := []*http.Server{
		{Addr: "127.0.0.1:0", Handler: http.NotFoundHandler()},
		{Addr: "127.0.0.1:0", Handler: http.NotFoundHandler()},
	}
	done := make(chan error, 1)
	go func() { done <- serveHTTPServers(ctx, servers) }()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("HTTP servers did not stop after cancellation")
	}
}

func TestDashboardWorkersDrainWhenServerBindFails(t *testing.T) {
	a, _ := testApp(t, &rpcFixture{}, "")
	blocked := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		<-r.Context().Done()
		return nil, r.Context().Err()
	})}
	a.rpc.client = blocked
	a.prices.client = blocked
	a.monitor.client = blocked
	a.monitor.output = io.Discard
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- runDashboardServers(ctx, cancel, a, []*http.Server{{Addr: "invalid address"}})
	}()
	select {
	case err := <-done:
		if err == nil || ctx.Err() == nil {
			t.Fatalf("bind failure did not stop workers: err=%v context=%v", err, ctx.Err())
		}
	case <-time.After(time.Second):
		t.Fatal("background workers did not drain after bind failure")
	}
}

func TestRPCRejectsInvalidResponses(t *testing.T) {
	cases := []struct {
		name, body string
		status     int
	}{
		{"oversize", strings.Repeat("x", maxRPCBody+1), 200},
		{"json", "invalid", 200},
		{"rpc-error", `{"result":{},"error":{"code":-1}}`, 200},
		{"null", `{"result":null,"error":null}`, 200},
		{"http", `{"result":{}}`, 500},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.status); io.WriteString(w, tc.body) }))
			defer server.Close()
			rpc := &rpcClient{url: server.URL, client: server.Client()}
			var result any
			if rpc.call(t.Context(), "uptime", nil, &result) == nil {
				t.Fatal("invalid RPC response accepted")
			}
		})
	}
}

func TestSensitiveRoutesRequireAuthentication(t *testing.T) {
	fixture := &rpcFixture{}
	a, handler := testApp(t, fixture, "")
	for _, target := range []struct{ method, path string }{{"POST", "/api/watch"}, {"DELETE", "/api/watch/Cold"}} {
		response := anonymousRequest(t, handler, target.method, target.path)
		if response.Code != http.StatusUnauthorized {
			t.Errorf("%s %s = %d", target.method, target.path, response.Code)
		}
	}
	if fixture.count() != 0 {
		t.Fatal("unauthenticated mutation reached RPC")
	}
	if response := anonymousRequest(t, handler, "GET", "/api/watch"); response.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous view = %d", response.Code)
	}
	for _, name := range []string{"Satoshi", "Genesis", "Cold"} {
		if _, err := a.watch.Add(name, genesisAddress, ""); err != nil {
			t.Fatal(err)
		}
		response := anonymousRequest(t, handler, "GET", "/api/watch")
		if response.Code != http.StatusUnauthorized || strings.Contains(response.Body.String(), genesisAddress) {
			t.Fatalf("unauthenticated %s roster exposed", name)
		}
		if removed, err := a.watch.Remove(name); !removed || err != nil {
			t.Fatal("could not remove test entry")
		}
	}
}

func TestPublicExampleRequiresAuthentication(t *testing.T) {
	fixture := &rpcFixture{}
	a, handler := testApp(t, fixture, "")
	entry, err := a.watch.Add(publicExampleName, publicExampleAddress, "")
	if err != nil {
		t.Fatal(err)
	}
	response := anonymousRequest(t, handler, http.MethodGet, "/api/watch")
	if response.Code != http.StatusUnauthorized || strings.Contains(response.Body.String(), publicExampleAddress) {
		t.Fatalf("anonymous public example = %d %q", response.Code, response.Body.String())
	}
	response = request(t, handler, http.MethodGet, "/api/watch", "")
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"example":true`) || !strings.Contains(response.Body.String(), `"display_name":"Binance"`) {
		t.Fatalf("public example read = %d %q", response.Code, response.Body.String())
	}
	if removed, err := a.watch.RemoveExact(entry); !removed || err != nil {
		t.Fatal("could not remove public example")
	}
}

func TestGenesisWatchUsesActualIBDState(t *testing.T) {
	fixture := &rpcFixture{initialBlockDownload: true}
	a, handler := testApp(t, fixture, "dashboard-secret")
	if _, err := a.watch.Add("Satoshi", genesisAddress, ""); err != nil {
		t.Fatal(err)
	}
	response := request(t, handler, http.MethodGet, "/api/watch", "dashboard-secret")
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"state":"pending"`) || strings.Contains(response.Body.String(), `"btc":"0"`) {
		t.Fatalf("IBD watch response = %d %q", response.Code, response.Body.String())
	}
}

func TestWatchListRejectsMalformedRosterWithoutChangingIt(t *testing.T) {
	stateDir := t.TempDir()
	rosterPath := filepath.Join(stateDir, "wallets.json")
	original := []byte(`[{"name":"Cold","key":"secret-material"}`)
	if err := os.WriteFile(rosterPath, original, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WATCH_STORE", rosterPath)
	t.Setenv("BALANCE_HISTORY", filepath.Join(stateDir, "history.json"))
	fixture := &rpcFixture{}
	a, handler := testApp(t, fixture, "dashboard-secret")
	if a.watch.Err() == nil {
		t.Fatal("malformed roster was treated as available")
	}
	response := request(t, handler, http.MethodGet, "/api/watch", "dashboard-secret")
	if response.Code != http.StatusServiceUnavailable || strings.TrimSpace(response.Body.String()) != "watch wallet storage unavailable" {
		t.Fatalf("malformed roster response = %d %q", response.Code, response.Body.String())
	}
	if fixture.count() != 0 {
		t.Fatal("unavailable roster reached node RPC")
	}
	after, err := os.ReadFile(rosterPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(original) {
		t.Fatalf("malformed roster changed from %q to %q", original, after)
	}
	if strings.Contains(response.Body.String(), rosterPath) || strings.Contains(response.Body.String(), "secret-material") {
		t.Fatal("unavailable response exposed roster details")
	}
}

func TestWatchAddWriteFailureDoesNotProvisionOrClaimSuccess(t *testing.T) {
	fixture := newLifecycleRPC()
	server := httptest.NewServer(http.HandlerFunc(fixture.handler))
	defer server.Close()
	stateDir := t.TempDir()
	cfg := config{dashboardPassword: "dashboard-secret", stateDir: stateDir, dataDir: t.TempDir(), version: "1.31.1"}
	a, err := newApp(cfg, &rpcClient{url: server.URL, client: server.Client()}, "index.html", "static", log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatal(err)
	}
	originalWriter := writeWatchFile
	writeWatchFile = func(string, []byte) error { return ErrWatchUnavailable }
	t.Cleanup(func() { writeWatchFile = originalWriter })
	req := httptest.NewRequest(http.MethodPost, "/api/watch", strings.NewReader(`{"name":"Cold","key":"`+genesisAddress+`"}`))
	req.SetBasicAuth("admin", "dashboard-secret")
	req.Header.Set("X-Requested-With", "fetch")
	response := httptest.NewRecorder()
	a.routes().ServeHTTP(response, req)
	if response.Code != http.StatusServiceUnavailable || strings.TrimSpace(response.Body.String()) != "watch wallet storage unavailable" {
		t.Fatalf("failed add response = %d %q", response.Code, response.Body.String())
	}
	if len(a.watch.Entries()) != 0 {
		t.Fatal("failed add changed the in-memory roster")
	}
	if fixture.countMethod("createwallet") != 0 || fixture.countMethod("loadwallet") != 0 || fixture.countMethod("importdescriptors") != 0 {
		t.Fatal("failed add launched provisioning")
	}
	if strings.Contains(response.Body.String(), stateDir) || strings.Contains(response.Body.String(), genesisAddress) {
		t.Fatal("failed add response exposed roster details")
	}
}

func TestWatchAddRejectsFutureBirthdayWithoutChangingWallets(t *testing.T) {
	fixture := newLifecycleRPC()
	server := httptest.NewServer(http.HandlerFunc(fixture.handler))
	defer server.Close()
	cfg := config{dashboardPassword: "dashboard-secret", stateDir: t.TempDir(), dataDir: t.TempDir(), version: "1.31.1"}
	a, err := newApp(cfg, &rpcClient{url: server.URL, client: server.Client()}, "index.html", "static", log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/watch", strings.NewReader(`{"name":"Future","key":"`+genesisAddress+`","birthday":"2999-01-01"}`))
	req.SetBasicAuth("admin", "dashboard-secret")
	req.Header.Set("X-Requested-With", "fetch")
	response := httptest.NewRecorder()
	a.routes().ServeHTTP(response, req)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "Birthday cannot be in the future") {
		t.Fatalf("future birthday response = %d %q", response.Code, response.Body.String())
	}
	if len(a.watch.Entries()) != 0 {
		t.Fatal("future birthday changed the wallet roster")
	}
	for _, method := range []string{"createwallet", "loadwallet", "importdescriptors"} {
		if fixture.countMethod(method) != 0 {
			t.Fatalf("future birthday called mutating RPC %s", method)
		}
	}
}

func TestWatchRemoveWriteFailureDoesNotUnloadOrClaimSuccess(t *testing.T) {
	fixture := newLifecycleRPC()
	server := httptest.NewServer(http.HandlerFunc(fixture.handler))
	defer server.Close()
	stateDir := t.TempDir()
	cfg := config{dashboardPassword: "dashboard-secret", stateDir: stateDir, dataDir: t.TempDir(), version: "1.31.1"}
	a, err := newApp(cfg, &rpcClient{url: server.URL, client: server.Client()}, "index.html", "static", log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatal(err)
	}
	entry, err := a.watch.Add("Cold", genesisAddress, "")
	if err != nil {
		t.Fatal(err)
	}
	a.watch.SetPending(entry.Key, true)
	a.provision(entry, false)
	originalWriter := writeWatchFile
	writeWatchFile = func(string, []byte) error { return ErrWatchUnavailable }
	t.Cleanup(func() { writeWatchFile = originalWriter })
	req := httptest.NewRequest(http.MethodDelete, "/api/watch/Cold", nil)
	req.SetBasicAuth("admin", "dashboard-secret")
	req.Header.Set("X-Requested-With", "fetch")
	response := httptest.NewRecorder()
	a.routes().ServeHTTP(response, req)
	if response.Code != http.StatusServiceUnavailable || strings.TrimSpace(response.Body.String()) != "watch wallet storage unavailable" {
		t.Fatalf("failed remove response = %d %q", response.Code, response.Body.String())
	}
	if !a.watch.Contains(entry) {
		t.Fatal("failed remove changed the in-memory roster")
	}
	fixture.mu.Lock()
	unloaded := append([]string(nil), fixture.unloaded...)
	fixture.mu.Unlock()
	if len(unloaded) != 0 {
		t.Fatalf("failed remove unloaded wallets: %v", unloaded)
	}
	if strings.Contains(response.Body.String(), stateDir) || strings.Contains(response.Body.String(), genesisAddress) {
		t.Fatal("failed remove response exposed roster details")
	}
}

func TestWatchAddSetsAndClearsPendingByKey(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Method string `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&request)
		var result any
		switch request.Method {
		case "getblockchaininfo":
			result = map[string]any{"pruned": false, "initialblockdownload": false}
		case "getdescriptorinfo":
			result = map[string]any{"descriptor": "addr(" + genesisAddress + ")", "hasprivatekeys": false, "isrange": false}
		case "getwalletinfo":
			<-release
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"result": result, "error": nil})
	}))
	defer server.Close()
	cfg := config{dashboardPassword: "dashboard-secret", stateDir: t.TempDir(), dataDir: t.TempDir(), version: "1.31.1"}
	rpc := &rpcClient{url: server.URL, client: server.Client()}
	a, err := newApp(cfg, rpc, "index.html", "static", log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/watch", strings.NewReader(`{"name":"Cold","key":"`+genesisAddress+`"}`))
	req.SetBasicAuth("admin", "dashboard-secret")
	req.Header.Set("X-Requested-With", "fetch")
	response := httptest.NewRecorder()
	a.routes().ServeHTTP(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("add response = %d %q", response.Code, response.Body.String())
	}
	_, pending := a.watch.snapshot()
	if !pending[watchKeyID(genesisAddress)] {
		t.Fatal("new wallet was not marked pending before provisioning")
	}
	close(release)
	deadline := time.Now().Add(time.Second)
	for {
		_, pending = a.watch.snapshot()
		if !pending[watchKeyID(genesisAddress)] {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("pending state was not cleared after provisioning")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestProvisionCompletionDoesNotAttachOrUnloadReplacement(t *testing.T) {
	tests := []struct {
		name        string
		replacement WatchEntry
		wantUnload  bool
	}{
		{name: "different key", replacement: WatchEntry{Name: "Cold", Key: parityZpub}, wantUnload: true},
		{name: "same key new generation", replacement: WatchEntry{Name: "Cold", Key: genesisAddress, Birthday: "2021-01-01"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := newLifecycleRPC()
			fixture.importStarted = make(chan struct{})
			fixture.releaseImport = make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(fixture.handler))
			defer server.Close()
			cfg := config{dashboardPassword: "secret", stateDir: t.TempDir(), dataDir: t.TempDir(), version: "1.31.1"}
			a, err := newApp(cfg, &rpcClient{url: server.URL, client: server.Client()}, "index.html", "static", log.New(io.Discard, "", 0))
			if err != nil {
				t.Fatal(err)
			}
			a.ctx = context.Background()
			original, err := a.watch.Add("Cold", genesisAddress, "")
			if err != nil {
				t.Fatal(err)
			}
			fixture.blockWallet = coreWalletName(original)
			a.watch.SetPending(original.Key, true)
			done := make(chan struct{})
			go func() {
				a.provision(original, false)
				close(done)
			}()
			select {
			case <-fixture.importStarted:
			case <-time.After(time.Second):
				t.Fatal("provisioning did not reach blocked import")
			}
			if removed, err := a.watch.Remove(original.Name); !removed || err != nil {
				t.Fatal("original entry was not removed")
			}
			replacement, err := a.watch.Add(tt.replacement.Name, tt.replacement.Key, tt.replacement.Birthday)
			if err != nil {
				t.Fatal(err)
			}
			a.watch.SetPending(replacement.Key, true)
			close(fixture.releaseImport)
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("stale provisioning did not finish")
			}

			fixture.mu.Lock()
			unloaded := append([]string(nil), fixture.unloaded...)
			fixture.mu.Unlock()
			if got := len(unloaded) > 0; got != tt.wantUnload {
				t.Fatalf("unloaded wallets = %v, want unload %v", unloaded, tt.wantUnload)
			}
			a.watch.mu.Lock()
			mapped := a.watch.coreNames[watchKeyID(original.Key)]
			pending := a.watch.pending[watchKeyID(replacement.Key)]
			a.watch.mu.Unlock()
			if mapped != "" {
				t.Fatalf("stale wallet mapping retained: %q", mapped)
			}
			if !pending {
				t.Fatal("replacement pending state was cleared by stale provisioning")
			}
		})
	}
}

func TestDeleteCleanupPreservesSameKeyReadd(t *testing.T) {
	fixture := newLifecycleRPC()
	server := httptest.NewServer(http.HandlerFunc(fixture.handler))
	defer server.Close()
	cfg := config{dashboardPassword: "secret", stateDir: t.TempDir(), dataDir: t.TempDir(), version: "1.31.1"}
	a, err := newApp(cfg, &rpcClient{url: server.URL, client: server.Client()}, "index.html", "static", log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatal(err)
	}
	a.ctx = context.Background()
	entry, err := a.watch.Add("Cold", genesisAddress, "")
	if err != nil {
		t.Fatal(err)
	}
	a.watch.SetPending(entry.Key, true)
	a.provision(entry, false)
	name, _ := a.watch.resolveWallet(a.walletRPC, entry)
	if name != coreWalletName(entry) {
		t.Fatalf("resolved wallet = %q", name)
	}
	if removed, err := a.watch.RemoveExact(entry); !removed || err != nil {
		t.Fatal("exact entry was not removed")
	}
	replacement, err := a.watch.Add("Cold", genesisAddress, "2021-01-01")
	if err != nil {
		t.Fatal(err)
	}
	a.watch.SetPending(replacement.Key, true)
	a.watch.DeprovisionNameIfUnused(a.nodeRPC, a.walletRPC, entry, name)

	fixture.mu.Lock()
	unloaded := append([]string(nil), fixture.unloaded...)
	fixture.mu.Unlock()
	if len(unloaded) != 0 {
		t.Fatalf("same-key replacement wallet was unloaded: %v", unloaded)
	}
	if !a.watch.Contains(replacement) {
		t.Fatal("same-key replacement was removed")
	}
	a.watch.mu.Lock()
	pending := a.watch.pending[watchKeyID(replacement.Key)]
	a.watch.mu.Unlock()
	if !pending {
		t.Fatal("same-key replacement pending state was cleared")
	}
}

func TestStartupProvisionsCurrentRosterAfterRPCBecomesAvailable(t *testing.T) {
	fixture := newLifecycleRPC()
	fixture.chainStarted = make(chan struct{})
	fixture.releaseChain = make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(fixture.handler))
	defer server.Close()
	cfg := config{dashboardPassword: "dashboard-secret", stateDir: t.TempDir(), dataDir: t.TempDir(), version: "1.31.1"}
	a, err := newApp(cfg, &rpcClient{url: server.URL, client: server.Client()}, "index.html", "static", log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatal(err)
	}
	old, err := a.watch.Add("Cold", genesisAddress, "")
	if err != nil {
		t.Fatal(err)
	}
	a.prices.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return response(http.StatusServiceUnavailable, ""), nil
	})}
	a.prices.url = "https://provider.test/prices"
	a.monitor.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return response(http.StatusServiceUnavailable, ""), nil
	})}
	a.monitor.output = io.Discard
	ctx, cancel := context.WithCancel(context.Background())
	done := a.startBackground(ctx)
	defer func() {
		cancel()
		<-done
	}()
	select {
	case <-fixture.chainStarted:
	case <-time.After(time.Second):
		t.Fatal("startup did not wait for blockchain RPC")
	}
	if removed, err := a.watch.Remove(old.Name); !removed || err != nil {
		t.Fatal("old startup entry was not removed")
	}
	replacement, err := a.watch.Add("Cold", parityZpub, "")
	if err != nil {
		t.Fatal(err)
	}
	close(fixture.releaseChain)
	deadline := time.Now().Add(time.Second)
	for {
		a.watch.mu.Lock()
		mapped := a.watch.coreNames[watchKeyID(replacement.Key)]
		a.watch.mu.Unlock()
		if mapped == coreWalletName(replacement) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("current startup roster was not provisioned")
		}
		time.Sleep(time.Millisecond)
	}
	fixture.mu.Lock()
	_, oldCreated := fixture.wallets[coreWalletName(old)]
	fixture.mu.Unlock()
	if oldCreated {
		t.Fatal("removed startup entry was provisioned after RPC recovered")
	}
}

func TestStartupMarksWalletsPendingBeforeWorkers(t *testing.T) {
	a, _ := testApp(t, &rpcFixture{}, "")
	entry, err := a.watch.Add("Cold", genesisAddress, "")
	if err != nil {
		t.Fatal(err)
	}
	a.rpc.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		<-r.Context().Done()
		return nil, r.Context().Err()
	})}
	a.prices.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return response(http.StatusServiceUnavailable, ""), nil
	})}
	a.prices.url = "https://provider.test/prices"
	ctx, cancel := context.WithCancel(context.Background())
	done := a.startBackground(ctx)
	defer func() {
		cancel()
		<-done
	}()
	_, pending := a.watch.snapshot()
	if !pending[watchKeyID(entry.Key)] {
		t.Fatal("startup wallet was not marked pending synchronously")
	}
}

func TestPriceCacheStartsWithBackgroundWorkers(t *testing.T) {
	a, _ := testApp(t, &rpcFixture{}, "")
	started := make(chan struct{})
	a.prices.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		close(started)
		<-r.Context().Done()
		return nil, r.Context().Err()
	})}
	a.prices.url = "https://provider.test/prices"
	ctx, cancel := context.WithCancel(context.Background())
	done := a.startBackground(ctx)
	defer func() {
		cancel()
		<-done
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("price cache did not start")
	}
}

func TestPriceWidgetLivesInsideStatusCard(t *testing.T) {
	_, handler := testApp(t, &rpcFixture{}, "")
	rendered := request(t, handler, http.MethodGet, "/", "").Body.String()
	doc, err := nethtml.Parse(strings.NewReader(rendered))
	if err != nil {
		t.Fatal(err)
	}
	var findID func(*nethtml.Node, string) *nethtml.Node
	findID = func(node *nethtml.Node, id string) *nethtml.Node {
		for _, attr := range node.Attr {
			if attr.Key == "id" && attr.Val == id {
				return node
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			if found := findID(child, id); found != nil {
				return found
			}
		}
		return nil
	}
	liveNode := findID(doc, "live")
	widgetNode := findID(doc, "btc-price")
	insideLive := false
	for ancestor := widgetNode; ancestor != nil; ancestor = ancestor.Parent {
		if ancestor == liveNode {
			insideLive = true
			break
		}
	}
	if liveNode == nil || widgetNode == nil || !insideLive {
		t.Fatal("price widget must be nested inside #live")
	}

	live := strings.Index(rendered, `<div id="live"`)
	fees := strings.Index(rendered, `id="spark-fee"`)
	widget := strings.Index(rendered, `id="btc-price"`)
	priceRow := strings.Index(rendered, `class="price-row"`)
	footer := strings.Index(rendered, `class="footer"`)
	watch := strings.Index(rendered, `id="watch"`)
	if live < 0 || fees < live || widget < fees || priceRow < widget || footer < priceRow || watch < footer {
		t.Fatal("price widget must be inside #live after fees and before the status footer")
	}
	if !strings.Contains(rendered[widget:priceRow], "<hr>") {
		t.Fatal("price widget must put a divider before the price row and footer")
	}
	if strings.Count(rendered, `/static/prices.js`) != 1 {
		t.Fatal("price module must load exactly once")
	}
}

func TestCorePrivateKeyFlagPreventsPersistence(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Method string `json:"method"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		var result any = map[string]any{"pruned": false}
		if body.Method == "getdescriptorinfo" {
			result = map[string]any{"descriptor": "private", "hasprivatekeys": true}
		}
		json.NewEncoder(w).Encode(map[string]any{"result": result, "error": nil})
	}))
	defer server.Close()
	cfg := config{dashboardPassword: "dashboard-secret", stateDir: t.TempDir(), dataDir: t.TempDir(), version: "1.31.1"}
	rpc := &rpcClient{url: server.URL, client: server.Client()}
	a, err := newApp(cfg, rpc, "index.html", "static", log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/api/watch", strings.NewReader(`{"name":"Cold","key":"addr(1A1zP1eP5QGefi2DMPTfTL5SLmv7DivfNa)"}`))
	req.SetBasicAuth("admin", "dashboard-secret")
	req.Header.Set("X-Requested-With", "fetch")
	response := httptest.NewRecorder()
	a.routes().ServeHTTP(response, req)
	if response.Code != 400 || len(a.watch.Entries()) != 0 {
		t.Fatal("Core private-key flag was ignored")
	}
	raw, _ := os.ReadFile(filepath.Join(cfg.stateDir, "wallets.json"))
	if strings.Contains(string(raw), "Cold") || strings.Contains(response.Body.String(), "private") {
		t.Fatal("rejected input was persisted or echoed")
	}
}
