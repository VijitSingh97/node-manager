package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type retryBitcoinMock struct {
	mu          sync.Mutex
	wallet      string
	descriptors []map[string]any
	creates     int
	imports     int
	unloads     int
	createDelay time.Duration
}

func (m *retryBitcoinMock) handler(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Method string `json:"method"`
		Params []any  `json:"params"`
	}
	_ = json.NewDecoder(r.Body).Decode(&request)
	wallet := strings.TrimPrefix(r.URL.Path, "/wallet/")
	if request.Method == "createwallet" {
		time.Sleep(m.createDelay)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var result any
	switch request.Method {
	case "getblockchaininfo":
		result = map[string]any{"pruned": false, "initialblockdownload": false}
	case "createwallet":
		m.creates++
		m.wallet = request.Params[0].(string)
		result = map[string]any{"name": m.wallet}
	case "getwalletinfo":
		if wallet == m.wallet && m.wallet != "" {
			result = watchInfo(false)
		}
	case "getdescriptorinfo":
		result = descriptorInfo(request.Params)
	case "listdescriptors":
		if wallet == m.wallet && m.wallet != "" {
			values := make([]any, len(m.descriptors))
			for index := range m.descriptors {
				values[index] = m.descriptors[index]
			}
			result = map[string]any{"descriptors": values}
		}
	case "importdescriptors":
		m.imports++
		requests := request.Params[0].([]any)
		results := make([]any, len(requests))
		for index, value := range requests {
			if m.imports == 1 && index == 1 {
				results[index] = map[string]any{"success": false}
				continue
			}
			item := mapValue(value)
			listed := map[string]any{
				"desc": item["desc"], "timestamp": item["timestamp"],
			}
			if _, ranged := item["range"]; ranged {
				listed["range"] = []any{float64(0), float64(999)}
			}
			m.descriptors = append(m.descriptors, listed)
			results[index] = map[string]any{"success": true}
		}
		result = results
	case "getbalances":
		if wallet == m.wallet && len(m.descriptors) == 2 {
			result = map[string]any{"mine": map[string]any{"trusted": 1.25}}
		}
	case "unloadwallet":
		m.unloads++
		result = map[string]any{}
	}
	status := http.StatusOK
	if result == nil {
		status = http.StatusInternalServerError
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"result": result, "error": nil})
}

func startRetryDashboard(t *testing.T, server *httptest.Server, stateDir string, seed bool) (*app, context.CancelFunc) {
	t.Helper()
	cfg := config{dashboardPassword: "secret", stateDir: stateDir, dataDir: t.TempDir(), version: "1.31.1"}
	dashboard, err := newApp(cfg, &rpcClient{url: server.URL, client: server.Client()}, "index.html", "static", log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatal(err)
	}
	blocked := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("mock provider unavailable")
	})}
	dashboard.prices.client = blocked
	dashboard.monitor.client = blocked
	dashboard.monitor.output = io.Discard
	if seed {
		if _, err := dashboard.watch.Add("Cold", parityZpub, "2021-01-01"); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := dashboard.startBackground(ctx)
	stop := func() {
		cancel()
		<-done
	}
	t.Cleanup(stop)
	return dashboard, stop
}

func TestWatchImportFailureRecoversThroughDashboardAfterRestart(t *testing.T) {
	t.Setenv("TELEGRAM_BOT_TOKEN", "")
	t.Setenv("TELEGRAM_CHAT_ID", "")
	t.Setenv("HEALTHCHECKS_URL", "")
	bitcoin := &retryBitcoinMock{}
	server := httptest.NewServer(http.HandlerFunc(bitcoin.handler))
	defer server.Close()
	stateDir := t.TempDir()
	first, stopFirst := startRetryDashboard(t, server, stateDir, true)
	row := waitForWalletState(t, first, "error")
	if row["btc"] != nil {
		t.Fatalf("failed import row = %#v", row)
	}
	stopFirst()
	restarted, _ := startRetryDashboard(t, server, stateDir, false)
	row = waitForWalletState(t, restarted, "ok")
	if row["name"] != "Cold" || row["btc"] != "1.25" {
		t.Fatalf("recovered row = %#v", row)
	}
}

func TestWatchAddThroughAPIProvisionsWallet(t *testing.T) {
	t.Setenv("TELEGRAM_BOT_TOKEN", "")
	t.Setenv("TELEGRAM_CHAT_ID", "")
	t.Setenv("HEALTHCHECKS_URL", "")
	bitcoin := &retryBitcoinMock{}
	server := httptest.NewServer(http.HandlerFunc(bitcoin.handler))
	defer server.Close()
	dashboard, _ := startRetryDashboard(t, server, t.TempDir(), false)
	token, err := dashboard.sessions.issue(time.Now(), false)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/watch", strings.NewReader(`{"name":"Cold","key":"`+parityZpub+`","birthday":"2021-01-01"}`))
	req.AddCookie(&http.Cookie{Name: "node-manager-session", Value: token})
	req.Header.Set("X-Requested-With", "fetch")
	response := httptest.NewRecorder()
	dashboard.routes().ServeHTTP(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("add response = %d %q", response.Code, response.Body.String())
	}
	waitForWalletState(t, dashboard, "error")
	bitcoin.mu.Lock()
	defer bitcoin.mu.Unlock()
	if bitcoin.creates < 1 {
		t.Fatalf("add through the API created %d wallets", bitcoin.creates)
	}
}

func TestProvisionStateSerializesConcurrentCallsForOneWallet(t *testing.T) {
	t.Setenv("TELEGRAM_BOT_TOKEN", "")
	t.Setenv("TELEGRAM_CHAT_ID", "")
	t.Setenv("HEALTHCHECKS_URL", "")
	bitcoin := &retryBitcoinMock{createDelay: 100 * time.Millisecond}
	server := httptest.NewServer(http.HandlerFunc(bitcoin.handler))
	defer server.Close()
	cfg := config{dashboardPassword: "secret", stateDir: t.TempDir(), dataDir: t.TempDir(), version: "1.31.1"}
	dashboard, err := newApp(cfg, &rpcClient{url: server.URL, client: server.Client()}, "index.html", "static", log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatal(err)
	}
	entry, err := dashboard.watch.Add("Cold", parityZpub, "2021-01-01")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			dashboard.provisionState(entry, false, false)
		}()
	}
	wg.Wait()
	bitcoin.mu.Lock()
	defer bitcoin.mu.Unlock()
	if bitcoin.creates != 1 {
		t.Fatalf("concurrent provisioning created %d wallets, want 1", bitcoin.creates)
	}
}

func waitForWalletState(t *testing.T, dashboard *app, state string) map[string]any {
	t.Helper()
	token, err := dashboard.sessions.issue(time.Now(), false)
	if err != nil {
		t.Fatal(err)
	}
	handler := dashboard.routes()
	deadline := time.Now().Add(10 * time.Second)
	for {
		req := httptest.NewRequest(http.MethodGet, "/api/watch", nil)
		req.AddCookie(&http.Cookie{Name: "node-manager-session", Value: token})
		poll := httptest.NewRecorder()
		handler.ServeHTTP(poll, req)
		row := watchResponseRow(t, poll)
		if row["state"] == state {
			return row
		}
		if time.Now().After(deadline) {
			t.Fatalf("wallet row = %#v, want state %s", row, state)
		}
		time.Sleep(time.Millisecond)
	}
}

func watchResponseRow(t *testing.T, response *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	if response.Code != http.StatusOK {
		t.Fatalf("watch response = %d %q", response.Code, response.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	wallets := body["wallets"].([]any)
	return wallets[0].(map[string]any)
}
