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
}

func (m *retryBitcoinMock) handler(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Method string `json:"method"`
		Params []any  `json:"params"`
	}
	_ = json.NewDecoder(r.Body).Decode(&request)
	wallet := strings.TrimPrefix(r.URL.Path, "/wallet/")
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

func TestWatchImportFailureRecoversThroughDashboardAfterRestart(t *testing.T) {
	t.Setenv("TELEGRAM_BOT_TOKEN", "")
	t.Setenv("TELEGRAM_CHAT_ID", "")
	t.Setenv("HEALTHCHECKS_URL", "")
	bitcoin := &retryBitcoinMock{}
	server := httptest.NewServer(http.HandlerFunc(bitcoin.handler))
	defer server.Close()
	stateDir := t.TempDir()
	newDashboard := func() (*app, context.CancelFunc) {
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
		ctx, cancel := context.WithCancel(t.Context())
		done := dashboard.startBackground(ctx)
		stop := func() {
			cancel()
			<-done
		}
		t.Cleanup(stop)
		return dashboard, stop
	}
	first, stopFirst := newDashboard()
	req := httptest.NewRequest(http.MethodPost, "/api/watch", strings.NewReader(`{"name":"Cold","key":"`+parityZpub+`","birthday":"2021-01-01"}`))
	req.SetBasicAuth("admin", "secret")
	req.Header.Set("X-Requested-With", "fetch")
	response := httptest.NewRecorder()
	first.routes().ServeHTTP(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("add response = %d %q", response.Code, response.Body.String())
	}
	row := waitForWalletState(t, first.routes(), "error")
	if row["btc"] != nil {
		t.Fatalf("failed import row = %#v", row)
	}
	stopFirst()
	restarted, _ := newDashboard()
	row = waitForWalletState(t, restarted.routes(), "ok")
	if row["name"] != "Cold" || row["btc"] != "1.25" {
		t.Fatalf("recovered row = %#v", row)
	}
}

func waitForWalletState(t *testing.T, handler http.Handler, state string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		row := watchResponseRow(t, request(t, handler, http.MethodGet, "/api/watch", "secret"))
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
