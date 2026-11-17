package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func setActuator(t *testing.T, handler http.Handler, health, info bool) {
	t.Helper()
	body := fmt.Sprintf(`{"actuator_health":%t,"actuator_info":%t}`, health, info)
	req := httptest.NewRequest(http.MethodPost, "/api/settings?scope=global", strings.NewReader(body))
	req.SetBasicAuth("admin", "dashboard-secret")
	req.Header.Set("X-Requested-With", "fetch")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	want := fmt.Sprintf(`"actuator_health":%t,"actuator_info":%t`, health, info)
	if w.Code != http.StatusOK || !strings.Contains(strings.ReplaceAll(w.Body.String(), "\n", ""), want) {
		t.Fatalf("settings update = %d %s", w.Code, w.Body.String())
	}
}

func enableActuator(t *testing.T, handler http.Handler) {
	t.Helper()
	setActuator(t, handler, true, true)
}

func actuatorGet(a *app, handler http.Handler, path string) *httptest.ResponseRecorder {
	return actuatorGetAs(handler, path, a.actuator.Username, a.actuator.Password)
}

func actuatorGetAs(handler http.Handler, path, user, password string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.SetBasicAuth(user, password)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	return w
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

func TestActuatorEndpointsFollowTheirOwnToggleAndAlwaysNeedTheActuatorLogin(t *testing.T) {
	a, handler := testApp(t, &rpcFixture{}, "")
	freshPrices(a)
	probeBitcoin(a, map[string]any{"blocks": float64(870000), "initialblockdownload": false, "chain": "main"})
	paths := map[string]func(notificationSettings) bool{
		"/actuator/health": func(s notificationSettings) bool { return s.ActuatorHealth },
		"/actuator/info":   func(s notificationSettings) bool { return s.ActuatorInfo },
	}
	for _, combo := range [][2]bool{{false, false}, {true, false}, {false, true}, {true, true}} {
		setActuator(t, handler, combo[0], combo[1])
		for path, enabled := range paths {
			on := enabled(a.monitor.notificationSettings())
			name := fmt.Sprintf("%s health=%t info=%t", path, combo[0], combo[1])
			if !on {
				for label, w := range map[string]*httptest.ResponseRecorder{
					"actuator login": actuatorGet(a, handler, path),
					"wrong login":    actuatorGetAs(handler, path, a.actuator.Username, "wrong"),
					"anonymous":      anonymousRequest(t, handler, http.MethodGet, path),
					"admin login":    request(t, handler, http.MethodGet, path, ""),
				} {
					if w.Code != http.StatusNotFound {
						t.Fatalf("%s disabled, %s = %d", name, label, w.Code)
					}
				}
				continue
			}
			if w := actuatorGet(a, handler, path); w.Code != http.StatusOK {
				t.Fatalf("%s with the actuator login = %d %s", name, w.Code, w.Body.String())
			}
			for label, w := range map[string]*httptest.ResponseRecorder{
				"anonymous":      anonymousRequest(t, handler, http.MethodGet, path),
				"wrong password": actuatorGetAs(handler, path, a.actuator.Username, "wrong"),
				"wrong user":     actuatorGetAs(handler, path, "admin", a.actuator.Password),
				"admin login":    request(t, handler, http.MethodGet, path, ""),
			} {
				if w.Code != http.StatusUnauthorized || !strings.HasPrefix(w.Header().Get("WWW-Authenticate"), "Basic ") {
					t.Fatalf("%s enabled, %s = %d, challenge %q", name, label, w.Code, w.Header().Get("WWW-Authenticate"))
				}
			}
		}
	}
}

func TestActuatorSessionCookieDoesNotOpenTheEndpoints(t *testing.T) {
	a, handler := testApp(t, &rpcFixture{}, "")
	enableActuator(t, handler)
	token, err := a.sessions.issue(time.Now(), false)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/actuator/info", nil)
	req.AddCookie(&http.Cookie{Name: "node-manager-session", Value: token})
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("signed-in session reached info = %d", w.Code)
	}
}

func TestActuatorHealthReportsComponentsToTheActuatorLogin(t *testing.T) {
	a, handler := testApp(t, &rpcFixture{}, "")
	enableActuator(t, handler)
	health := func(want int) map[string]any {
		return decodeActuator(t, actuatorGet(a, handler, "/actuator/health"), want)
	}

	detailed := health(http.StatusServiceUnavailable)
	if detailed["status"] != statusDown || component(t, detailed, "dashboard")["status"] != statusUp || component(t, detailed, "tor")["status"] != statusDegraded {
		t.Fatalf("detailed unprobed health = %v", detailed)
	}
	if bitcoin := component(t, detailed, nodeBitcoin); bitcoin["status"] != statusDown || bitcoin["details"].(map[string]any)["state"] != "down" {
		t.Fatalf("unprobed bitcoin = %v", bitcoin)
	}

	freshPrices(a)
	probeBitcoin(a, map[string]any{"blocks": float64(860000), "initialblockdownload": true, "chain": "main", "verificationprogress": 0.5})
	detailed = health(http.StatusOK)
	if detailed["status"] != statusDegraded {
		t.Fatalf("syncing overall status = %v", detailed)
	}
	if bitcoin := component(t, detailed, nodeBitcoin); bitcoin["status"] != statusDegraded || bitcoin["details"].(map[string]any)["state"] != "syncing" || component(t, detailed, "tor")["status"] != statusUp {
		t.Fatalf("syncing health = %v", detailed)
	}

	probeBitcoin(a, map[string]any{"blocks": float64(870000), "initialblockdownload": false})
	if synced := health(http.StatusOK); synced["status"] != statusUp {
		t.Fatalf("synced health = %v", synced)
	}

	if err := os.WriteFile(filepath.Join(a.cfg.stateDir, "node-runtime.json"), []byte(`{"bitcoin":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if synced := health(http.StatusOK); synced["status"] != statusUp {
		t.Fatalf("an activation change elsewhere marked bitcoin down: %v", synced)
	}

	seeding := utxoMgrNew(t, t.TempDir(), nil, newFakeUtxoNode(utxoMgrChain(2, "b")), io.Discard)
	seeding.setSeedStatePath(filepath.Join(t.TempDir(), "utxo-seed.json"))
	seeding.suppressAlerts(time.Now().Add(time.Minute))
	a.utxo = seeding
	a.recordNodeHealth(nodeBitcoin, false)
	detailed = health(http.StatusOK)
	if bitcoin := component(t, detailed, nodeBitcoin); bitcoin["status"] != statusDegraded || bitcoin["details"].(map[string]any)["state"] != "busy" {
		t.Fatalf("bitcoin during a seed = %v", detailed)
	}
	a.utxo = nil

	if err := os.WriteFile(filepath.Join(a.cfg.stateDir, "node-runtime.json"), []byte(`{"bitcoin":false}`), 0o600); err != nil {
		t.Fatal(err)
	}
	detailed = health(http.StatusOK)
	if _, listed := detailed["components"].(map[string]any)[nodeBitcoin]; listed {
		t.Fatalf("deactivated node in health = %v", detailed)
	}
}

func TestActuatorInfoReportsCachedChainState(t *testing.T) {
	a, handler := testApp(t, &rpcFixture{}, "")
	enableActuator(t, handler)
	capability := fmt.Sprintf(`{"platform":"linux","boot_kind":"boot","boot_supported":true,"timestamp":%d}`, time.Now().Unix())
	if err := os.WriteFile(a.nodeControlPath("capability"), []byte(capability), 0o600); err != nil {
		t.Fatal(err)
	}
	probeBitcoin(a, map[string]any{"blocks": float64(870000), "initialblockdownload": false, "chain": "main", "verificationprogress": 0.9999})
	a.noteNodeNetwork(nodeBitcoin, "/Satoshi:28.1.0/", 9)

	info := decodeActuator(t, actuatorGet(a, handler, "/actuator/info"), http.StatusOK)
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
	bitcoin = decodeActuator(t, actuatorGet(a, handler, "/actuator/info"), http.StatusOK)["chains"].(map[string]any)[nodeBitcoin].(map[string]any)
	if bitcoin["active"] != false || bitcoin["status"] != statusOff || bitcoin["height"] != nil || bitcoin["version"] != nil {
		t.Fatalf("deactivated chain info = %v", bitcoin)
	}
}

func TestActuatorResponsesLeakNoCredentialsPathsOrAddressesWhenNothingIsExposed(t *testing.T) {
	a, handler := testApp(t, &rpcFixture{}, "")
	enableActuator(t, handler)
	freshPrices(a)
	probeBitcoin(a, map[string]any{"blocks": float64(870000), "initialblockdownload": false, "chain": "main", "bestblockhash": "00000000000000000001", "warnings": "x"})
	a.noteNodeNetwork(nodeBitcoin, "/Satoshi:28.1.0/", 9)
	rpcHost, _ := url.Parse(a.cfg.rpcURL)
	secrets := []string{"rpc-password", "rpc-user", "dashboard-secret", a.actuator.Password, rpcHost.Host, a.cfg.dataDir, a.cfg.stateDir, ".onion", "bestblockhash", "xpub", "descriptor", "wallet"}
	for _, path := range []string{"/actuator/health", "/actuator/info"} {
		w := actuatorGet(a, handler, path)
		for _, secret := range secrets {
			if strings.Contains(w.Body.String(), secret) {
				t.Fatalf("%s leaks %q: %s", path, secret, w.Body.String())
			}
		}
	}
}

func TestActuatorLoginPathsAreLiteral(t *testing.T) {
	a, handler := testApp(t, &rpcFixture{}, "")
	enableActuator(t, handler)
	for _, path := range []string{"/actuator%2Fhealth", "/actuator/health/", "/actuator%2Finfo", "/actuator/info/"} {
		for label, w := range map[string]*httptest.ResponseRecorder{
			"anonymous":       anonymousRequest(t, handler, http.MethodGet, path),
			"actuator login":  actuatorGet(a, handler, path),
			"dashboard login": request(t, handler, http.MethodGet, path, ""),
		} {
			if label == "dashboard login" {
				if w.Code == http.StatusOK {
					t.Fatalf("%s served an actuator body to the dashboard login: %s", path, w.Body.String())
				}
				continue
			}
			if w.Code != http.StatusUnauthorized {
				t.Fatalf("%s with %s skipped dashboard sign-in: %d %s", path, label, w.Code, w.Body.String())
			}
		}
	}
	req := httptest.NewRequest(http.MethodPost, "/actuator/health", nil)
	req.SetBasicAuth(a.actuator.Username, a.actuator.Password)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("POST with the actuator login = %d", w.Code)
	}
	a.cfg.dashboardPublicURL = "https://nodes.example"
	req = httptest.NewRequest(http.MethodGet, "http://attacker.example/actuator/health", nil)
	w = httptest.NewRecorder()
	a.routes().ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("foreign host = %d", w.Code)
	}
}

func TestActuatorFailuresAreThrottledPerProxyClientAndSeparateFromDashboardSignIn(t *testing.T) {
	a, handler := testApp(t, &rpcFixture{}, "dashboard-secret")
	enableActuator(t, handler)
	a.cfg.dashboardPublicURL = "https://nodes.example"
	get := func(client, user, password string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "https://nodes.example/actuator/health", nil)
		req.RemoteAddr = "172.29.0.25:1234"
		req.Header.Set("X-Node-Manager-Client-IP", client)
		req.SetBasicAuth(user, password)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		return w
	}
	for i := 0; i <= loginFailureLimit; i++ {
		w := get("198.51.100.42", a.actuator.Username, "wrong")
		want := http.StatusUnauthorized
		if i == loginFailureLimit {
			want = http.StatusTooManyRequests
			if w.Header().Get("Retry-After") == "" {
				t.Fatal("throttled response lacks a retry delay")
			}
		}
		if w.Code != want {
			t.Fatalf("attempt %d = %d, want %d", i, w.Code, want)
		}
	}
	if w := get("198.51.100.42", a.actuator.Username, a.actuator.Password); w.Code != http.StatusTooManyRequests {
		t.Fatalf("a throttled client got through with the right login: %d", w.Code)
	}
	if w := get("198.51.100.43", a.actuator.Username, a.actuator.Password); w.Code == http.StatusUnauthorized || w.Code == http.StatusTooManyRequests {
		t.Fatalf("another proxy client was locked out: %d", w.Code)
	}
	req := httptest.NewRequest(http.MethodGet, "https://nodes.example/api/fees", nil)
	req.RemoteAddr = "172.29.0.25:1234"
	req.Header.Set("X-Node-Manager-Client-IP", "198.51.100.42")
	req.SetBasicAuth("admin", "dashboard-secret")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("actuator failures locked the same client out of the dashboard: %d", w.Code)
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

func TestActuatorCredentialsArePersistedPrivateAndStrong(t *testing.T) {
	dir := t.TempDir()
	first, err := loadOrCreateActuatorCredentials(dir)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := base64.RawURLEncoding.DecodeString(first.Password)
	if err != nil || len(raw) < 16 || first.Username != actuatorUsername {
		t.Fatalf("credentials = %+v (%d random bytes, %v)", first, len(raw), err)
	}
	path := filepath.Join(dir, actuatorCredentialsFile)
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("credentials file = %v, %v", info, err)
	}
	again, err := loadOrCreateActuatorCredentials(dir)
	if err != nil || again != first {
		t.Fatalf("restart changed the credentials: %+v vs %+v (%v)", again, first, err)
	}
	other, err := loadOrCreateActuatorCredentials(t.TempDir())
	if err != nil || other.Password == first.Password {
		t.Fatalf("two installs share a password: %v", err)
	}
}

func TestActuatorCredentialsAreRegeneratedWhenTheFileCannotBeTrusted(t *testing.T) {
	good := actuatorCredentials{Username: actuatorUsername, Password: base64.RawURLEncoding.EncodeToString(make([]byte, 32))}
	goodJSON, _ := json.Marshal(good)
	for name, test := range map[string]struct {
		content string
		mode    os.FileMode
	}{
		"corrupt":        {"{not json", 0o600},
		"empty":          {"", 0o600},
		"short password": {`{"username":"actuator","password":"AAAA"}`, 0o600},
		"wrong user":     {`{"username":"admin","password":"` + good.Password + `"}`, 0o600},
		"unknown field":  {`{"username":"actuator","password":"` + good.Password + `","x":1}`, 0o600},
		"trailing data":  {string(goodJSON) + `{}`, 0o600},
		"world readable": {string(goodJSON), 0o644},
		"group readable": {string(goodJSON), 0o640},
	} {
		dir := t.TempDir()
		path := filepath.Join(dir, actuatorCredentialsFile)
		if err := os.WriteFile(path, []byte(test.content), test.mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, test.mode); err != nil {
			t.Fatal(err)
		}
		got, err := loadOrCreateActuatorCredentials(dir)
		if err != nil || !got.valid() || got == good {
			t.Fatalf("%s: credentials = %+v, %v", name, got, err)
		}
		if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
			t.Fatalf("%s: regenerated file mode = %v", name, info.Mode())
		}
		if again, _ := loadOrCreateActuatorCredentials(dir); again != got {
			t.Fatalf("%s: regenerated credentials were not kept", name)
		}
	}
	good2, _ := loadOrCreateActuatorCredentials(t.TempDir())
	if !good2.valid() || (actuatorCredentials{Username: actuatorUsername}).valid() {
		t.Fatal("valid() accepts an empty password")
	}
}

func TestActuatorCredentialsSurviveWhenTheStateDirectoryIsNotWritable(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	creds, err := loadOrCreateActuatorCredentials(filepath.Join(blocker, "state"))
	if err == nil || !creds.valid() {
		t.Fatalf("unwritable state = %+v, %v", creds, err)
	}
}

func TestActuatorCredentialsAreStableAcrossDashboardRestartsAndShownOnlyInGlobalSettings(t *testing.T) {
	a, handler := testApp(t, &rpcFixture{}, "")
	get := func(path string) map[string]any {
		return decodeActuator(t, request(t, handler, http.MethodGet, path, ""), http.StatusOK)
	}
	view := get("/api/settings")
	if view["actuator_username"] != a.actuator.Username || view["actuator_password"] != a.actuator.Password || a.actuator.Password == "" {
		t.Fatalf("settings do not show the actuator login: %v", view)
	}
	for _, scope := range []string{"/api/settings?scope=nodes", "/api/settings?scope=appearance"} {
		body := request(t, handler, http.MethodGet, scope, "").Body.String()
		if strings.Contains(body, a.actuator.Password) || strings.Contains(body, "actuator_username") || strings.Contains(body, "actuator_password") {
			t.Fatalf("%s leaks the actuator login: %s", scope, body)
		}
	}
	if w := anonymousRequest(t, handler, http.MethodGet, "/api/settings"); strings.Contains(w.Body.String(), a.actuator.Password) {
		t.Fatalf("anonymous settings leak the actuator password")
	}
	rpc := a.rpc
	restarted, err := newApp(a.cfg, rpc, filepath.Join(filepath.Dir(sourceFile(t)), "index.html"), filepath.Join(filepath.Dir(sourceFile(t)), "static"), log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatal(err)
	}
	if restarted.actuator != a.actuator {
		t.Fatal("a restart changed the actuator login")
	}
}

func TestActuatorLoginIsNeverWrittenToTheLog(t *testing.T) {
	var logs strings.Builder
	a, handler := testApp(t, &rpcFixture{}, "")
	a.log = log.New(&logs, "", 0)
	enableActuator(t, handler)
	actuatorGet(a, handler, "/actuator/info")
	actuatorGetAs(handler, "/actuator/info", a.actuator.Username, "wrong")
	if strings.Contains(logs.String(), a.actuator.Password) {
		t.Fatalf("log contains the actuator password: %s", logs.String())
	}
}

func TestSavedLegacyHealthEndpointsSettingEnablesBothActuatorEndpoints(t *testing.T) {
	const base = `"telegram_enabled":false,"telegram_bot_token":"","telegram_chat_id":"","healthchecks_enabled":false,"healthchecks_url":"","alert_new_block":false`
	for name, test := range map[string]struct {
		extra        string
		health, info bool
	}{
		"legacy on":             {`,"health_endpoints":true`, true, true},
		"legacy off":            {`,"health_endpoints":false`, false, false},
		"never set":             {``, false, false},
		"new keys":              {`,"actuator_health":true,"actuator_info":false`, true, false},
		"legacy adds to new on": {`,"health_endpoints":true,"actuator_health":false,"actuator_info":false`, true, true},
	} {
		stateDir := t.TempDir()
		path := filepath.Join(stateDir, "notification_settings.json")
		if err := os.WriteFile(path, []byte("{"+base+test.extra+"}"), 0o600); err != nil {
			t.Fatal(err)
		}
		m := NewMonitor("dev", t.TempDir(), stateDir)
		got := m.notificationSettings()
		if m.settingsErr != nil || got.ActuatorHealth != test.health || got.ActuatorInfo != test.info || got.LegacyHealthEndpoints != nil {
			t.Fatalf("%s: settings = %+v (%v)", name, got, m.settingsErr)
		}
		if err := m.updateSettingsScope(settingsUpdate{ActuatorInfo: boolPointer(false), present: map[string]bool{"actuator_info": true}}, "global"); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		saved, _ := os.ReadFile(path)
		if strings.Contains(string(saved), "health_endpoints") || !strings.Contains(string(saved), `"actuator_info":false`) {
			t.Fatalf("%s: saved file keeps the legacy key: %s", name, saved)
		}
		reloaded := NewMonitor("dev", t.TempDir(), stateDir).notificationSettings()
		if reloaded.ActuatorHealth != test.health || reloaded.ActuatorInfo {
			t.Fatalf("%s: reloaded = %+v", name, reloaded)
		}
	}
}

func TestResetRestoresBothActuatorEndpointsToOff(t *testing.T) {
	m := NewMonitor("dev", t.TempDir(), t.TempDir())
	if err := m.updateSettingsScope(settingsUpdate{ActuatorHealth: boolPointer(true), ActuatorInfo: boolPointer(true), present: map[string]bool{"actuator_health": true, "actuator_info": true}}, "global"); err != nil {
		t.Fatal(err)
	}
	if got := m.notificationSettings(); !got.ActuatorHealth || !got.ActuatorInfo {
		t.Fatalf("toggles did not save: %+v", got)
	}
	view, _ := m.settingsView(true)
	if defaults := view["defaults"].(map[string]any); defaults["actuator_health"] != false || defaults["actuator_info"] != false {
		t.Fatalf("defaults = %v", defaults)
	}
	if err := m.resetSettings(); err != nil {
		t.Fatal(err)
	}
	if got := m.notificationSettings(); got.ActuatorHealth || got.ActuatorInfo {
		t.Fatalf("reset left %+v", got)
	}
}

func TestSettingsRejectTheRetiredHealthEndpointsField(t *testing.T) {
	_, handler := testApp(t, &rpcFixture{}, "")
	req := httptest.NewRequest(http.MethodPost, "/api/settings?scope=global", strings.NewReader(`{"health_endpoints":true}`))
	req.SetBasicAuth("admin", "dashboard-secret")
	req.Header.Set("X-Requested-With", "fetch")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("retired field = %d", w.Code)
	}
}

func lanService(t *testing.T, info map[string]any, node, id string) map[string]any {
	t.Helper()
	chain, _ := info["chains"].(map[string]any)[node].(map[string]any)
	for _, raw := range chain["services"].([]any) {
		if service := raw.(map[string]any); service["id"] == id {
			return service
		}
	}
	t.Fatalf("%s has no service %s: %v", node, id, chain)
	return nil
}

func TestActuatorInfoListsEveryLANServiceWithEndpointAndLogin(t *testing.T) {
	a, handler := testApp(t, &rpcFixture{}, "")
	enableActuator(t, handler)
	a.cfg.bitcoinRPCEndpoint = "192.168.1.117:8332"
	a.cfg.moneroEnabled, a.cfg.moneroRPCUser, a.cfg.moneroRPCPassword = true, "monero-user", "monero-pass"
	a.cfg.moneroRPCEndpoint, a.cfg.moneroZMQEndpoint = "192.168.1.117:18081", "192.168.1.117:18083"
	a.cfg.nodes = map[string]nodeConfig{
		nodeTari:        {ID: nodeTari, Enabled: true, Supported: true, RPCEndpoint: "192.168.1.117:18142"},
		nodeLitecoin:    {ID: nodeLitecoin, Enabled: true, Supported: true, RPCUser: "ltc-user", RPCPassword: "ltc-pass", RPCEndpoint: "10.0.0.5:9332"},
		nodeDogecoin:    {ID: nodeDogecoin, Enabled: true, Supported: true, RPCUser: "doge-user", RPCPassword: "doge-pass"},
		nodeBitcoinCash: {ID: nodeBitcoinCash, Enabled: true, Supported: true, RPCUser: "bch-user", RPCPassword: "bch-pass", RPCEndpoint: "192.168.1.117:8333"},
		nodeZcash:       {ID: nodeZcash, Enabled: true, Supported: true, RPCUser: "zec-user", RPCPassword: "zec-pass", RPCEndpoint: "192.168.1.117:8232"},
	}
	info := decodeActuator(t, actuatorGet(a, handler, "/actuator/info"), http.StatusOK)

	for node, want := range map[string]int{nodeBitcoin: 1, nodeMonero: 2, nodeTari: 1, nodeLitecoin: 1, nodeDogecoin: 1, nodeBitcoinCash: 1, nodeZcash: 1} {
		chain, _ := info["chains"].(map[string]any)[node].(map[string]any)
		if chain == nil || len(chain["services"].([]any)) != want {
			t.Fatalf("%s services = %v, want %d", node, chain, want)
		}
	}
	cases := []struct {
		node, id, protocol, auth, bind, uri, user, password string
		port                                                float64
	}{
		{nodeBitcoin, "bitcoin_rpc", "json-rpc", "basic", "192.168.1.117", "http://192.168.1.117:8332", "rpc-user", "rpc-password", 8332},
		{nodeMonero, "monero_rpc", "json-rpc", "digest", "192.168.1.117", "http://192.168.1.117:18081", "monero-user", "monero-pass", 18081},
		{nodeMonero, "monero_zmq", "zmq", "none", "192.168.1.117", "tcp://192.168.1.117:18083", "", "", 18083},
		{nodeTari, "tari_grpc", "grpc", "none", "192.168.1.117", "192.168.1.117:18142", "", "", 18142},
		{nodeLitecoin, "litecoin_rpc", "json-rpc", "basic", "10.0.0.5", "http://10.0.0.5:9332", "ltc-user", "ltc-pass", 9332},
		{nodeBitcoinCash, "bitcoincash_rpc", "json-rpc", "basic", "192.168.1.117", "http://192.168.1.117:8333", "bch-user", "bch-pass", 8333},
		{nodeZcash, "zcash_rpc", "json-rpc", "basic", "192.168.1.117", "http://192.168.1.117:8232", "zec-user", "zec-pass", 8232},
	}
	for _, c := range cases {
		service := lanService(t, info, c.node, c.id)
		got := []any{service["exposed"], service["protocol"], service["auth"], service["bind_address"], service["port"], service["uri"], service["username"], service["password"]}
		want := []any{true, c.protocol, c.auth, c.bind, c.port, c.uri, nil, nil}
		if c.user != "" {
			want[6], want[7] = c.user, c.password
		}
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Fatalf("%s %s = %v, want %v", c.node, c.id, got, want)
		}
	}
	dogecoin := lanService(t, info, nodeDogecoin, "dogecoin_rpc")
	if dogecoin["exposed"] != false || dogecoin["protocol"] != "json-rpc" || dogecoin["auth"] != "basic" {
		t.Fatalf("unexposed dogecoin = %v", dogecoin)
	}
	for _, field := range []string{"bind_address", "port", "uri", "username", "password"} {
		if _, present := dogecoin[field]; present {
			t.Fatalf("unexposed dogecoin reports %s: %v", field, dogecoin)
		}
	}
	for _, id := range []string{"monero_zmq", "tari_grpc"} {
		node := nodeMonero
		if id == "tari_grpc" {
			node = nodeTari
		}
		service := lanService(t, info, node, id)
		if service["username"] != nil || service["password"] != nil {
			t.Fatalf("%s reports a login: %v", id, service)
		}
	}
	if err := os.WriteFile(filepath.Join(a.cfg.stateDir, "node-runtime.json"), []byte(`{"litecoin":false}`), 0o600); err != nil {
		t.Fatal(err)
	}
	info = decodeActuator(t, actuatorGet(a, handler, "/actuator/info"), http.StatusOK)
	if litecoin := lanService(t, info, nodeLitecoin, "litecoin_rpc"); litecoin["username"] != nil || litecoin["password"] != nil {
		t.Fatalf("a deactivated node's RPC login was listed: %v", litecoin)
	}
	health := decodeActuator(t, actuatorGet(a, handler, "/actuator/health"), http.StatusServiceUnavailable)
	if strings.Contains(fmt.Sprint(health), "ltc-pass") || strings.Contains(fmt.Sprint(health), "uri") {
		t.Fatalf("health leaks service details: %v", health)
	}
}

func TestActuatorInfoMarksServicesNotExposedByDefault(t *testing.T) {
	a, handler := testApp(t, &rpcFixture{}, "")
	enableActuator(t, handler)
	info := decodeActuator(t, actuatorGet(a, handler, "/actuator/info"), http.StatusOK)
	bitcoin := lanService(t, info, nodeBitcoin, "bitcoin_rpc")
	if bitcoin["exposed"] != false || bitcoin["username"] != nil || bitcoin["password"] != nil || bitcoin["uri"] != nil {
		t.Fatalf("unexposed bitcoin = %v", bitcoin)
	}
	if strings.Contains(actuatorGet(a, handler, "/actuator/info").Body.String(), "rpc-password") {
		t.Fatal("info reports a login for a service that is not exposed")
	}
}

func TestOperationsDocsStateThatOneLoginOpensBothActuatorEndpoints(t *testing.T) {
	a, handler := testApp(t, &rpcFixture{}, "")
	freshPrices(a)
	probeBitcoin(a, map[string]any{"blocks": float64(870000), "initialblockdownload": false, "chain": "main"})
	setActuator(t, handler, true, true)
	for _, path := range []string{"/actuator/health", "/actuator/info"} {
		if w := actuatorGet(a, handler, path); w.Code != http.StatusOK {
			t.Fatalf("%s with the shared login = %d", path, w.Code)
		}
	}
	doc, err := os.ReadFile("../../docs/operations.md")
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Join(strings.Fields(string(doc)), " ")
	if strings.Contains(text, "monitors that do not need them") {
		t.Fatal("docs still imply /actuator/health can be shared without exposing /actuator/info")
	}
	for _, want := range []string{"one actuator login for both endpoints", "leave **Serve /actuator/info** off"} {
		if !strings.Contains(text, want) {
			t.Fatalf("docs missing %q", want)
		}
	}
}
