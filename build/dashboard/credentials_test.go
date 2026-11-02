package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func credentialHTTP(a *app, method, path, password, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Host = "mini-1.local"
	r.Header.Set("X-Requested-With", "fetch")
	if password != "" {
		r.SetBasicAuth("admin", password)
	}
	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, r)
	return w
}

func TestDashboardPasswordChangePersistsAndRevokesOldPassword(t *testing.T) {
	a, _ := testApp(t, &rpcFixture{}, "original-password")
	for _, body := range []string{
		`{"old_password":"wrong","new_password":"a-new-password","repeat_password":"a-new-password"}`,
		`{"old_password":"original-password","new_password":"short","repeat_password":"short"}`,
		`{"old_password":"original-password","new_password":"a-new-password","repeat_password":"different-password"}`,
		`{"old_password":"original-password","new_password":"a-new-password","repeat_password":"a-new-password","unexpected":true}`,
	} {
		response := credentialHTTP(a, "POST", "/api/settings/password", "original-password", body)
		if response.Code != http.StatusBadRequest && response.Code != http.StatusForbidden {
			t.Fatalf("invalid password change accepted: %d", response.Code)
		}
	}
	body := `{"old_password":"original-password","new_password":"a-new-password","repeat_password":"a-new-password"}`
	response := credentialHTTP(a, "POST", "/api/settings/password", "original-password", body)
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" || strings.Contains(response.Body.String(), "password") {
		t.Fatalf("password change response: %d", response.Code)
	}
	if credentialHTTP(a, "GET", "/api/settings", "original-password", "").Code != http.StatusUnauthorized || credentialHTTP(a, "GET", "/api/settings", "a-new-password", "").Code != http.StatusOK {
		t.Fatal("password change did not revoke previous access")
	}
	path := filepath.Join(a.cfg.stateDir, "dashboard_auth.json")
	b, _ := os.ReadFile(path)
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 || strings.Contains(string(b), "a-new-password") || strings.Contains(string(b), "original-password") {
		t.Fatal("password storage is not private hashed data")
	}
	restarted, err := newDashboardAuth(a.cfg.stateDir, "original-password")
	if err != nil || restarted.matches("original-password") || !restarted.matches("a-new-password") || restarted.matches("original-password") {
		t.Fatal("password change did not survive restart")
	}
	if credentialHTTP(a, "POST", "/api/settings", "a-new-password", `{"reset":true}`).Code != http.StatusOK || !a.dashboardPasswordMatches("a-new-password") {
		t.Fatal("notification reset affected dashboard authentication")
	}
}

func TestDashboardPasswordStorageFailsClosed(t *testing.T) {
	a, _ := testApp(t, &rpcFixture{}, "original-password")
	a.dashboardAuth.path = filepath.Join(t.TempDir(), "missing", "password")
	if err := os.WriteFile(filepath.Dir(a.dashboardAuth.path), []byte("blocked"), 0o600); err != nil {
		t.Fatal(err)
	}
	response := credentialHTTP(a, "POST", "/api/settings/password", "original-password", `{"old_password":"original-password","new_password":"a-new-password","repeat_password":"a-new-password"}`)
	if response.Code != http.StatusServiceUnavailable || !a.dashboardPasswordMatches("original-password") || a.dashboardPasswordMatches("a-new-password") {
		t.Fatal("failed save changed active password")
	}
	for _, contents := range []string{`{}`, `null`, `{"version":1,"iterations":999999999999}`, `{"version":1,"iterations":210000,"salt":"bad","hash":"bad"}`} {
		path := filepath.Join(t.TempDir(), "dashboard_auth.json")
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := newDashboardAuth(filepath.Dir(path), "original-password"); err == nil {
			t.Fatal("invalid password override fell back to old password")
		}
	}
}

func TestAnonymousDashboardPasswordChangeIsRejected(t *testing.T) {
	a, _ := testApp(t, &rpcFixture{}, "")
	body := `{"old_password":"dashboard-secret","new_password":"a-new-password","repeat_password":"a-new-password"}`
	for _, remote := range []string{"203.0.113.8:4321", "192.168.1.20:4321"} {
		r := httptest.NewRequest("POST", "/api/settings/password", strings.NewReader(body))
		r.Host = "mini-1.local"
		r.RemoteAddr = remote
		r.Header.Set("X-Requested-With", "fetch")
		w := httptest.NewRecorder()
		a.routes().ServeHTTP(w, r)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("anonymous password change from %s = %d", remote, w.Code)
		}
	}
	if !a.dashboardPasswordMatches("dashboard-secret") || a.dashboardPasswordMatches("a-new-password") {
		t.Fatal("anonymous password change altered authentication")
	}
}

func TestRPCCredentialsRevealAndResetRequireLANAuthAndConfirmation(t *testing.T) {
	a, _ := testApp(t, &rpcFixture{}, "dashboard-secret")
	a.cfg.bitcoinRPCEndpoint = "192.168.1.50:8332"
	for _, body := range []string{`{"node":"bitcoin","confirmation":"RESET"}`, `{"node":"bitcoin"}`, `{"node":"tari","confirmation":"reset"}`, `{"node":"monero_zmq","confirmation":"reset"}`, `{"node":"zcash","confirmation":"reset"}`, `{"node":"bitcoin","confirmation":"reset","password":"chosen"}`} {
		if response := credentialHTTP(a, "POST", "/api/settings/credentials/reset", "dashboard-secret", body); response.Code != http.StatusBadRequest {
			t.Fatalf("invalid reset accepted: %d", response.Code)
		}
	}
	for _, path := range []string{"/api/settings/credentials", "/api/settings/credentials/reveal", "/api/settings/credentials/reset"} {
		if response := credentialHTTP(a, "POST", path, "", `{"node":"bitcoin"}`); response.Code != http.StatusUnauthorized {
			t.Fatal("unauthenticated credential request accepted")
		}
	}
	response := credentialHTTP(a, "POST", "/api/settings/credentials/reveal", "dashboard-secret", `{"node":"bitcoin"}`)
	var revealed map[string]string
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" || json.Unmarshal(response.Body.Bytes(), &revealed) != nil || revealed["username"] != a.cfg.rpcUser || revealed["password"] != a.cfg.rpcPassword {
		t.Fatalf("credential reveal failed: %d", response.Code)
	}
	if response := credentialHTTP(a, "GET", "/api/settings/credentials", "dashboard-secret", ""); strings.Contains(response.Body.String(), a.cfg.rpcPassword) {
		t.Fatal("credential status exposed RPC password")
	}
	if response := credentialHTTP(a, "POST", "/api/settings/credentials/reset", "dashboard-secret", `{"node":"bitcoin","confirmation":"reset"}`); response.Code != http.StatusServiceUnavailable {
		t.Fatal("reset accepted without host helper")
	}
	capability, _ := json.Marshal(map[string]int64{"timestamp": time.Now().Unix()})
	if err := os.WriteFile(a.credentialPath("capability"), capability, 0o600); err != nil {
		t.Fatal(err)
	}
	response = credentialHTTP(a, "POST", "/api/settings/credentials/reset", "dashboard-secret", `{"node":"bitcoin","confirmation":"reset"}`)
	if response.Code != http.StatusAccepted {
		t.Fatalf("valid reset rejected: %d", response.Code)
	}
	b, _ := os.ReadFile(a.credentialPath("request"))
	var request credentialRequest
	if json.Unmarshal(b, &request) != nil || !validPortControlID(request.ID) || request.Node != nodeBitcoin || strings.Contains(string(b), "password") {
		t.Fatal("reset did not queue a private password-free request")
	}
	if !a.nodeTransitioning(nodeBitcoin) || a.nodeTransitioning(nodeMonero) {
		t.Fatal("planned node restart was not tracked")
	}
	if credentialHTTP(a, "POST", "/api/settings/credentials/reset", "dashboard-secret", `{"node":"bitcoin","confirmation":"reset"}`).Code != http.StatusConflict || credentialHTTP(a, "POST", "/api/settings/credentials/reveal", "dashboard-secret", `{"node":"bitcoin"}`).Code != http.StatusConflict {
		t.Fatal("credential actions allowed during a pending reset")
	}
}

func TestNodeSettingsDoNotTransmitGlobalSecretsOrOverwriteThem(t *testing.T) {
	a, _ := testApp(t, &rpcFixture{}, "dashboard-secret")
	setup := `{"alert_new_block":false,"telegram_enabled":true,"telegram_bot_token":"123456:example-token-for-tests","telegram_chat_id":"1234","healthchecks_enabled":true,"healthchecks_url":"https://hc-ping.com/12345678-1234-1234-1234-123456789abc"}`
	if credentialHTTP(a, "POST", "/api/settings", "dashboard-secret", setup).Code != http.StatusOK {
		t.Fatal("fixture notification settings were rejected")
	}
	for _, method := range []string{"GET", "POST"} {
		body := ""
		if method == "POST" {
			body = `{"alert_new_block_monero":true}`
		}
		response := credentialHTTP(a, method, "/api/settings?scope=nodes", "dashboard-secret", body)
		var view map[string]any
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &view) != nil || !view["can_configure"].(bool) {
			t.Fatalf("node settings unavailable: %d", response.Code)
		}
		for _, field := range []string{"telegram_bot_token", "telegram_chat_id", "healthchecks_url"} {
			if _, present := view[field]; present {
				t.Fatal("node settings included a global credential")
			}
		}
	}
	if response := credentialHTTP(a, "POST", "/api/settings?scope=nodes", "dashboard-secret", `{"telegram_enabled":false}`); response.Code != http.StatusBadRequest {
		t.Fatal("node settings accepted a global change")
	}
	if response := credentialHTTP(a, "POST", "/api/settings?scope=global", "dashboard-secret", `{"tower_labels":true,"default_currency":"EUR"}`); response.Code != http.StatusOK {
		t.Fatal("global settings rejected an independent appearance change")
	}
	response := credentialHTTP(a, "GET", "/api/settings", "dashboard-secret", "")
	var view map[string]any
	_ = json.Unmarshal(response.Body.Bytes(), &view)
	if view["telegram_bot_token"] != "123456:example-token-for-tests" || view["telegram_enabled"] != true || view["healthchecks_enabled"] != true || view["alert_new_block_monero"] != true || view["default_currency"] != "EUR" || view["tower_labels"] != true {
		t.Fatal("node-only save changed global notification settings")
	}
}

func TestInactiveNodeCannotRevealOrResetRPCPassword(t *testing.T) {
	a, _ := testApp(t, &rpcFixture{}, "dashboard-secret")
	a.cfg.bitcoinRPCEndpoint = "192.168.1.50:8332"
	if err := os.WriteFile(filepath.Join(a.cfg.stateDir, "node-runtime.json"), []byte(`{"bitcoin":false}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"reveal", "reset"} {
		body := `{"node":"bitcoin"}`
		if action == "reset" {
			body = `{"node":"bitcoin","confirmation":"reset"}`
		}
		if response := credentialHTTP(a, "POST", "/api/settings/credentials/"+action, "dashboard-secret", body); response.Code != http.StatusBadRequest {
			t.Fatalf("inactive node credential action accepted: %d", response.Code)
		}
	}
	if _, err := os.Stat(a.credentialPath("request")); !os.IsNotExist(err) {
		t.Fatal("inactive node queued a restart")
	}
}
