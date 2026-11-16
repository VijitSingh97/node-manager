package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func setHealthEndpoints(t *testing.T, handler http.Handler, enabled bool) {
	t.Helper()
	body := fmt.Sprintf(`{"health_endpoints":%t}`, enabled)
	req := httptest.NewRequest(http.MethodPost, "/api/settings?scope=global", strings.NewReader(body))
	req.SetBasicAuth("admin", "dashboard-secret")
	req.Header.Set("X-Requested-With", "fetch")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), fmt.Sprintf(`"health_endpoints":%t`, enabled)) {
		t.Fatalf("settings update = %d %s", w.Code, w.Body.String())
	}
}

func decodeActuator(t *testing.T, w *httptest.ResponseRecorder, want int) map[string]any {
	t.Helper()
	if w.Code != want {
		t.Fatalf("status = %d, want %d: %s", w.Code, want, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("body %q: %v", w.Body.String(), err)
	}
	return body
}

func component(t *testing.T, health map[string]any, name string) map[string]any {
	t.Helper()
	components, _ := health["components"].(map[string]any)
	value, _ := components[name].(map[string]any)
	if value == nil {
		t.Fatalf("component %s missing from %v", name, health)
	}
	return value
}

func freshPrices(a *app) {
	a.prices.mu.Lock()
	a.prices.assetPrices["bitcoin"] = map[string]float64{"usd": 1}
	a.prices.assetUpdatedAt["bitcoin"] = time.Now().Unix()
	a.prices.assetFailed["bitcoin"] = false
	a.prices.mu.Unlock()
}

func probeBitcoin(a *app, info map[string]any) {
	a.recordNodeInfo(nodeBitcoin, func() map[string]any { return info })()
	a.recordNodeHealth(nodeBitcoin, true)
}

func TestActuatorEndpointsStayOffUntilTheSettingIsOn(t *testing.T) {
	_, handler := testApp(t, &rpcFixture{}, "")
	for _, path := range []string{"/actuator/health", "/actuator/info"} {
		if w := anonymousRequest(t, handler, http.MethodGet, path); w.Code != http.StatusUnauthorized {
			t.Fatalf("%s while off, anonymous = %d", path, w.Code)
		}
		if w := request(t, handler, http.MethodGet, path, ""); w.Code != http.StatusNotFound {
			t.Fatalf("%s while off, signed in = %d", path, w.Code)
		}
	}
	setHealthEndpoints(t, handler, true)
	setHealthEndpoints(t, handler, false)
	if w := anonymousRequest(t, handler, http.MethodGet, "/actuator/health"); w.Code != http.StatusUnauthorized {
		t.Fatalf("health after turning off = %d", w.Code)
	}
}

func TestActuatorHealthGivesAnonymousCallersOnlyTheOverallStatus(t *testing.T) {
	a, handler := testApp(t, &rpcFixture{}, "")
	setHealthEndpoints(t, handler, true)

	anonymous := decodeActuator(t, anonymousRequest(t, handler, http.MethodGet, "/actuator/health"), http.StatusServiceUnavailable)
	if len(anonymous) != 1 || anonymous["status"] != statusDown {
		t.Fatalf("anonymous unprobed health = %v", anonymous)
	}
	detailed := decodeActuator(t, request(t, handler, http.MethodGet, "/actuator/health", ""), http.StatusServiceUnavailable)
	if component(t, detailed, "dashboard")["status"] != statusUp || component(t, detailed, "tor")["status"] != statusDegraded {
		t.Fatalf("detailed unprobed health = %v", detailed)
	}
	if bitcoin := component(t, detailed, nodeBitcoin); bitcoin["status"] != statusDown || bitcoin["details"].(map[string]any)["state"] != "down" {
		t.Fatalf("unprobed bitcoin = %v", bitcoin)
	}

	freshPrices(a)
	probeBitcoin(a, map[string]any{"blocks": float64(860000), "initialblockdownload": true, "chain": "main", "verificationprogress": 0.5})
	anonymous = decodeActuator(t, anonymousRequest(t, handler, http.MethodGet, "/actuator/health"), http.StatusOK)
	if len(anonymous) != 1 || anonymous["status"] != statusDegraded {
		t.Fatalf("anonymous syncing health = %v", anonymous)
	}
	detailed = decodeActuator(t, request(t, handler, http.MethodGet, "/actuator/health", ""), http.StatusOK)
	if bitcoin := component(t, detailed, nodeBitcoin); bitcoin["status"] != statusDegraded || bitcoin["details"].(map[string]any)["state"] != "syncing" || component(t, detailed, "tor")["status"] != statusUp {
		t.Fatalf("syncing health = %v", detailed)
	}

	probeBitcoin(a, map[string]any{"blocks": float64(870000), "initialblockdownload": false})
	if health := decodeActuator(t, anonymousRequest(t, handler, http.MethodGet, "/actuator/health"), http.StatusOK); health["status"] != statusUp {
		t.Fatalf("synced health = %v", health)
	}

	if err := os.WriteFile(filepath.Join(a.cfg.stateDir, "node-runtime.json"), []byte(`{"bitcoin":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if health := decodeActuator(t, anonymousRequest(t, handler, http.MethodGet, "/actuator/health"), http.StatusOK); health["status"] != statusUp {
		t.Fatalf("an activation change elsewhere marked bitcoin down: %v", health)
	}

	seeding := utxoMgrNew(t, t.TempDir(), nil, newFakeUtxoNode(utxoMgrChain(2, "b")), io.Discard)
	seeding.setSeedStatePath(filepath.Join(t.TempDir(), "utxo-seed.json"))
	seeding.suppressAlerts(time.Now().Add(time.Minute))
	a.utxo = seeding
	a.recordNodeHealth(nodeBitcoin, false)
	detailed = decodeActuator(t, request(t, handler, http.MethodGet, "/actuator/health", ""), http.StatusOK)
	if bitcoin := component(t, detailed, nodeBitcoin); bitcoin["status"] != statusDegraded || bitcoin["details"].(map[string]any)["state"] != "busy" {
		t.Fatalf("bitcoin during a seed = %v", detailed)
	}
	a.utxo = nil

	if err := os.WriteFile(filepath.Join(a.cfg.stateDir, "node-runtime.json"), []byte(`{"bitcoin":false}`), 0o600); err != nil {
		t.Fatal(err)
	}
	detailed = decodeActuator(t, request(t, handler, http.MethodGet, "/actuator/health", ""), http.StatusOK)
	if _, listed := detailed["components"].(map[string]any)[nodeBitcoin]; listed {
		t.Fatalf("deactivated node in health = %v", detailed)
	}
}

func TestActuatorInfoNeedsSignInAndReportsCachedChainState(t *testing.T) {
	a, handler := testApp(t, &rpcFixture{}, "")
	setHealthEndpoints(t, handler, true)
	if w := anonymousRequest(t, handler, http.MethodGet, "/actuator/info"); w.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous info = %d", w.Code)
	}
	capability := fmt.Sprintf(`{"platform":"linux","boot_kind":"boot","boot_supported":true,"timestamp":%d}`, time.Now().Unix())
	if err := os.WriteFile(a.nodeControlPath("capability"), []byte(capability), 0o600); err != nil {
		t.Fatal(err)
	}
	probeBitcoin(a, map[string]any{"blocks": float64(870000), "initialblockdownload": false, "chain": "main", "verificationprogress": 0.9999})
	a.noteNodeNetwork(nodeBitcoin, "/Satoshi:28.1.0/", 9)

	info := decodeActuator(t, request(t, handler, http.MethodGet, "/actuator/info", ""), http.StatusOK)
	if app := info["app"].(map[string]any); app["name"] != "node-manager" || app["version"] != "v1.31.1" || app["install_kind"] != "apt" {
		t.Fatalf("app info = %v", app)
	}
	chains := info["chains"].(map[string]any)
	bitcoin := chains[nodeBitcoin].(map[string]any)
	want := map[string]any{"name": "Bitcoin", "active": true, "status": statusUp, "network": "main", "height": float64(870000), "sync_progress": 0.9999, "peers": float64(9), "version": "/Satoshi:28.1.0/"}
	for key, value := range want {
		if bitcoin[key] != value {
			t.Fatalf("bitcoin %s = %v, want %v (%v)", key, bitcoin[key], value, bitcoin)
		}
	}
	if _, listed := chains[nodeMonero]; listed {
		t.Fatalf("unconfigured chain listed: %v", chains)
	}

	if err := os.WriteFile(filepath.Join(a.cfg.stateDir, "node-runtime.json"), []byte(`{"bitcoin":false}`), 0o600); err != nil {
		t.Fatal(err)
	}
	bitcoin = decodeActuator(t, request(t, handler, http.MethodGet, "/actuator/info", ""), http.StatusOK)["chains"].(map[string]any)[nodeBitcoin].(map[string]any)
	if bitcoin["active"] != false || bitcoin["status"] != statusOff || bitcoin["height"] != nil || bitcoin["version"] != nil {
		t.Fatalf("deactivated chain info = %v", bitcoin)
	}
}

func TestActuatorResponsesLeakNoCredentialsPathsOrAddresses(t *testing.T) {
	a, handler := testApp(t, &rpcFixture{}, "")
	setHealthEndpoints(t, handler, true)
	freshPrices(a)
	probeBitcoin(a, map[string]any{"blocks": float64(870000), "initialblockdownload": false, "chain": "main", "bestblockhash": "00000000000000000001", "warnings": "x"})
	a.noteNodeNetwork(nodeBitcoin, "/Satoshi:28.1.0/", 9)
	rpcHost, _ := url.Parse(a.cfg.rpcURL)
	secrets := []string{"rpc-password", "rpc-user", "dashboard-secret", rpcHost.Host, a.cfg.dataDir, a.cfg.stateDir, ".onion", "bestblockhash", "xpub", "descriptor", "wallet"}
	for _, w := range []*httptest.ResponseRecorder{
		request(t, handler, http.MethodGet, "/actuator/health", ""),
		request(t, handler, http.MethodGet, "/actuator/info", ""),
		anonymousRequest(t, handler, http.MethodGet, "/actuator/health"),
	} {
		for _, secret := range secrets {
			if strings.Contains(w.Body.String(), secret) {
				t.Fatalf("response leaks %q: %s", secret, w.Body.String())
			}
		}
	}
}

func TestActuatorAnonymousAccessCoversOnlyTheLiteralHealthPath(t *testing.T) {
	a, handler := testApp(t, &rpcFixture{}, "")
	setHealthEndpoints(t, handler, true)
	for _, path := range []string{"/actuator%2Fhealth", "/actuator/health/", "/actuator/info", "/actuator%2Finfo"} {
		if w := anonymousRequest(t, handler, http.MethodGet, path); w.Code != http.StatusUnauthorized {
			t.Fatalf("%s skipped sign-in: %d %s", path, w.Code, w.Body.String())
		}
	}
	if w := request(t, handler, http.MethodGet, "/actuator/health", "wrong-password"); w.Code != http.StatusUnauthorized {
		t.Fatalf("wrong Basic credentials = %d", w.Code)
	}
	a.cfg.dashboardPublicURL = "https://nodes.example"
	req := httptest.NewRequest(http.MethodGet, "http://attacker.example/actuator/health", nil)
	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("foreign host = %d", w.Code)
	}
}

func TestActuatorStatusRules(t *testing.T) {
	if got := aggregateStatus(map[string]actuatorComponent{"a": {Status: statusUp}, "b": {Status: statusDegraded}, "c": {Status: statusDown}}); got != statusDown {
		t.Fatalf("DOWN must win, got %s", got)
	}
	if got := aggregateStatus(map[string]actuatorComponent{"a": {Status: statusUp}, "b": {Status: statusDegraded}}); got != statusDegraded {
		t.Fatalf("DEGRADED must beat UP, got %s", got)
	}
	for state, want := range map[string]string{utxoStateReady: statusUp, utxoStateCatching: statusDegraded, utxoStateSeeding: statusDegraded, utxoStateNotSeeded: statusDegraded, utxoStateDegraded: statusDegraded, utxoStateInvalid: statusDegraded} {
		if got := balanceIndexComponent(state); got.Status != want || got.Details["state"] != state {
			t.Fatalf("balance index %s = %v, want %s", state, got, want)
		}
	}
}
