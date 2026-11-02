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

func portControlHTTP(handler http.Handler, method, password, body string, csrf bool) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "/api/settings/ports", strings.NewReader(body))
	req.Host = "mini-1.local"
	if password != "" {
		req.SetBasicAuth("admin", password)
	}
	if csrf {
		req.Header.Set("X-Requested-With", "fetch")
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	return w
}

func enablePortControl(t *testing.T, a *app) {
	t.Helper()
	if err := os.WriteFile(a.portControlPath("capability"), []byte("ready\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestPortControlViewRequiresPasswordAndFreshCapability(t *testing.T) {
	a, _ := testApp(t, &rpcFixture{}, "dashboard-secret")
	a.cfg.bitcoinRPCEndpoint = "192.168.1.50:8332"
	a.cfg.moneroEnabled = true
	a.cfg.moneroRPCEndpoint = "192.168.1.50:18081"
	a.cfg.moneroZMQEndpoint = ""
	t.Setenv("MONERO_ZMQ_BIND_IP", "192.168.1.60")
	t.Setenv("MONERO_ZMQ_PORT", "28083")
	handler := a.routes()

	var view portControlView
	response := portControlHTTP(handler, http.MethodGet, "dashboard-secret", "", false)
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &view) != nil || view.Available || len(view.Ports) != 3 || !view.Ports[0].Enabled || view.Ports[2].Enabled || view.Ports[2].BindIP != "192.168.1.60" || view.Ports[2].HostPort != 28083 {
		t.Fatalf("unavailable view = %d %#v", response.Code, view)
	}
	enablePortControl(t, a)
	response = portControlHTTP(handler, http.MethodGet, "dashboard-secret", "", false)
	if json.Unmarshal(response.Body.Bytes(), &view) != nil || !view.Available {
		t.Fatalf("available view = %d %#v", response.Code, view)
	}
	stale := time.Now().Add(-portControlCapabilityAge - time.Second)
	if err := os.Chtimes(a.portControlPath("capability"), stale, stale); err != nil {
		t.Fatal(err)
	}
	response = portControlHTTP(handler, http.MethodGet, "dashboard-secret", "", false)
	if json.Unmarshal(response.Body.Bytes(), &view) != nil || view.Available {
		t.Fatalf("stale capability view = %d %#v", response.Code, view)
	}

	protected, _ := testApp(t, &rpcFixture{}, "")
	protected.cfg.bitcoinRPCEndpoint = "192.168.1.50:8332"
	enablePortControl(t, protected)
	unsafeRequest := httptest.NewRequest(http.MethodGet, "/api/settings/ports", nil)
	unsafeRequest.Host = "dashboard.example.com"
	unsafeResponse := httptest.NewRecorder()
	protected.routes().ServeHTTP(unsafeResponse, unsafeRequest)
	if unsafeResponse.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous public-host view = %d: %s", unsafeResponse.Code, unsafeResponse.Body.String())
	}
	response = portControlHTTP(protected.routes(), http.MethodPost, "", `{"changes":[{"port":"bitcoin_rpc","enabled":false}]}`, true)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous close = %d: %s", response.Code, response.Body.String())
	}
}

func TestPortControlQueuesStrictOpenAndCloseRequestAndReportsStatus(t *testing.T) {
	a, _ := testApp(t, &rpcFixture{}, "dashboard-secret")
	a.cfg.bitcoinRPCEndpoint = "192.168.1.50:8332"
	a.cfg.moneroEnabled = true
	a.cfg.moneroRPCEndpoint = ""
	a.cfg.moneroZMQEndpoint = "192.168.1.50:18083"
	t.Setenv("MONERO_RPC_BIND_IP", "192.168.1.50")
	enablePortControl(t, a)
	handler := a.routes()

	if got := portControlHTTP(handler, http.MethodPost, "dashboard-secret", `{"changes":[{"port":"bitcoin_rpc","enabled":false}]}`, false); got.Code != http.StatusForbidden {
		t.Fatalf("missing CSRF = %d", got.Code)
	}
	for _, body := range []string{
		`{"changes":[]}`,
		`{"changes":[{"port":"unknown","enabled":true,"bind_ip":"192.168.1.50","host_port":18081}]}`,
		`{"changes":[{"port":"monero_rpc","enabled":false}]}`,
		`{"changes":[{"port":"bitcoin_rpc","enabled":false,"bind_ip":"192.168.1.50"}]}`,
		`{"changes":[{"port":"monero_rpc","enabled":true}]}`,
		`{"changes":[{"port":"monero_rpc","enabled":true,"host_port":0}]}`,
		`{"changes":[{"port":"monero_rpc","enabled":true,"host_port":8332}]}`,
		`{"changes":[{"port":"monero_rpc","enabled":true,"host_port":null}]}`,
		`{"changes":[{"port":"monero_rpc","enabled":true,"bind_ip":"127.0.0.1","host_port":18081}]}`,
		`{"changes":[{"port":"monero_rpc","enabled":true,"bind_ip":"8.8.8.8","host_port":18081}]}`,
		`{"changes":[{"port":"monero_rpc","enabled":true,"bind_ip":"fc00::1","host_port":18081}]}`,
		`{"changes":[{"port":"monero_rpc","enabled":true,"bind_ip":"192.168.1.50","host_port":0}]}`,
		`{"changes":[{"port":"monero_rpc","enabled":true,"bind_ip":"192.168.1.50","host_port":8332}]}`,
		`{"changes":[{"port":"bitcoin_rpc","enabled":true,"bind_ip":"192.168.1.50","host_port":8332}]}`,
		`{"changes":[{"port":"bitcoin_rpc","enabled":false},{"port":"bitcoin_rpc","enabled":false}]}`,
		`{"changes":[{"port":"bitcoin_rpc","enabled":false,"bind_ip":null}]}`,
		`{"changes":[{"port":"bitcoin_rpc","enabled":false,"host_port":null}]}`,
		`{"changes":[{"port":"bitcoin_rpc","enabled":false}],"open":true}`,
	} {
		got := portControlHTTP(handler, http.MethodPost, "dashboard-secret", body, true)
		if got.Code != http.StatusBadRequest && got.Code != http.StatusConflict {
			t.Fatalf("invalid %s = %d: %s", body, got.Code, got.Body.String())
		}
	}

	response := portControlHTTP(handler, http.MethodPost, "dashboard-secret", `{"changes":[{"port":"bitcoin_rpc","enabled":false},{"port":"monero_rpc","enabled":true,"host_port":18081}]}`, true)
	if response.Code != http.StatusAccepted || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("valid close = %d: %s", response.Code, response.Body.String())
	}
	var accepted portControlView
	if json.Unmarshal(response.Body.Bytes(), &accepted) != nil || accepted.Status == nil || accepted.Status.State != "pending" || !validPortControlID(accepted.Status.ID) {
		t.Fatalf("accepted view = %#v", accepted)
	}
	requestPath := a.portControlPath("request")
	info, err := os.Stat(requestPath)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("request file = %#v, %v", info, err)
	}
	var request portControlRequest
	b, _ := os.ReadFile(requestPath)
	if json.Unmarshal(b, &request) != nil || request.ID != accepted.Status.ID || len(request.Changes) != 2 || request.Changes[0] != (portControlChange{Port: "bitcoin_rpc", Enabled: false}) || request.Changes[1] != (portControlChange{Port: "monero_rpc", Enabled: true, BindIP: "192.168.1.50", HostPort: 18081}) || time.Since(time.Unix(request.RequestedAt, 0)) > time.Minute {
		t.Fatalf("queued request = %#v", request)
	}
	if got := portControlHTTP(handler, http.MethodPost, "dashboard-secret", `{"changes":[{"port":"bitcoin_rpc","enabled":false}]}`, true); got.Code != http.StatusConflict {
		t.Fatalf("second request = %d", got.Code)
	}

	if err := os.Rename(requestPath, a.portControlPath("processing")); err != nil {
		t.Fatal(err)
	}
	response = portControlHTTP(handler, http.MethodGet, "dashboard-secret", "", false)
	var pending portControlView
	if json.Unmarshal(response.Body.Bytes(), &pending) != nil || pending.Status == nil || pending.Status.State != "pending" || pending.Status.ID != request.ID {
		t.Fatalf("processing view = %#v", pending)
	}
	if err := os.Remove(a.portControlPath("processing")); err != nil {
		t.Fatal(err)
	}
	status, _ := json.Marshal(portControlStatus{ID: request.ID, State: "applied", Message: "LAN port closed.", AffectedServices: []string{"tor", "dashboard"}})
	if err := os.WriteFile(a.portControlPath("status"), status, 0o600); err != nil {
		t.Fatal(err)
	}
	response = portControlHTTP(handler, http.MethodGet, "dashboard-secret", "", false)
	var applied portControlView
	if json.Unmarshal(response.Body.Bytes(), &applied) != nil || applied.Status == nil || applied.Status.State != "applied" || applied.Status.Message != "LAN port changes applied." || strings.Join(applied.Status.AffectedServices, ",") != "tor,dashboard" {
		t.Fatalf("applied view = %#v", applied)
	}
}

func TestPortControlRejectsStaleCapabilityAndMalformedStatus(t *testing.T) {
	a, _ := testApp(t, &rpcFixture{}, "dashboard-secret")
	a.cfg.bitcoinRPCEndpoint = "192.168.1.50:8332"
	enablePortControl(t, a)
	if err := os.WriteFile(filepath.Join(a.cfg.stateDir, "port-control.status"), []byte(`{"id":"bad","state":"applied","message":"secret\npath"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	view := a.portControlView()
	if view.Status != nil {
		t.Fatalf("malformed status exposed: %#v", view.Status)
	}
	stale := time.Now().Add(-portControlCapabilityAge - time.Second)
	if err := os.Chtimes(a.portControlPath("capability"), stale, stale); err != nil {
		t.Fatal(err)
	}
	response := portControlHTTP(a.routes(), http.MethodPost, "dashboard-secret", `{"changes":[{"port":"bitcoin_rpc","enabled":false}]}`, true)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("stale capability POST = %d", response.Code)
	}
	if _, err := os.Stat(a.portControlPath("request")); !os.IsNotExist(err) {
		t.Fatalf("stale capability wrote request: %v", err)
	}
}

func TestPortControlUsesServerLANAddress(t *testing.T) {
	for _, tc := range []struct {
		name, savedBind, dashboardBind, wantBind string
	}{
		{"saved node address", "192.168.2.10", "192.168.1.10", "192.168.2.10"},
		{"first enable on LAN", "", "192.168.1.10", "192.168.1.10"},
		{"compose loopback default", "127.0.0.1", "192.168.1.10", "192.168.1.10"},
		{"no private server address", "", "127.0.0.1", ""},
		{"wildcard dashboard", "", "0.0.0.0", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, _ := testApp(t, &rpcFixture{}, "dashboard-secret")
			a.cfg.bitcoinDisabled = true
			a.cfg.moneroEnabled = true
			t.Setenv("MONERO_RPC_BIND_IP", tc.savedBind)
			t.Setenv("DASHBOARD_BIND_IP", tc.dashboardBind)
			enablePortControl(t, a)
			handler := a.routes()
			response := portControlHTTP(handler, http.MethodPost, "dashboard-secret", `{"changes":[{"port":"monero_rpc","enabled":true,"host_port":28081,"bind_ip":"192.168.8.8"}]}`, true)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("client address override accepted: %d", response.Code)
			}
			response = portControlHTTP(handler, http.MethodPost, "dashboard-secret", `{"changes":[{"port":"monero_rpc","enabled":true,"host_port":28081}]}`, true)
			if tc.wantBind == "" {
				if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "configure a private LAN address on the server") {
					t.Fatalf("missing server LAN address = %d %s", response.Code, response.Body.String())
				}
				return
			}
			if response.Code != http.StatusAccepted {
				t.Fatalf("server LAN address rejected: %d %s", response.Code, response.Body.String())
			}
			b, err := os.ReadFile(a.portControlPath("request"))
			var request portControlRequest
			if err != nil || json.Unmarshal(b, &request) != nil || len(request.Changes) != 1 || request.Changes[0].BindIP != tc.wantBind {
				t.Fatalf("server-derived binding = %#v, %v", request, err)
			}
		})
	}
}

func TestPortControlNamesConflictingService(t *testing.T) {
	a, _ := testApp(t, &rpcFixture{}, "dashboard-secret")
	a.cfg.bitcoinRPCEndpoint = "192.168.1.50:8332"
	a.cfg.moneroEnabled = true
	t.Setenv("MONERO_RPC_BIND_IP", "192.168.1.50")
	t.Setenv("DASHBOARD_BIND_IP", "192.168.1.50")
	enablePortControl(t, a)
	want := "Selected port for Monero RPC is already in use."
	for _, body := range []string{
		`{"changes":[{"port":"monero_rpc","enabled":true,"host_port":8332}]}`,
		`{"changes":[{"port":"monero_rpc","enabled":true,"host_port":80}]}`,
	} {
		response := portControlHTTP(a.routes(), http.MethodPost, "dashboard-secret", body, true)
		if response.Code != http.StatusBadRequest || strings.TrimSpace(response.Body.String()) != want {
			t.Fatalf("configured conflict = %d %s", response.Code, response.Body.String())
		}
	}
	status := `{"id":"11111111111111111111111111111111","state":"rejected","error_code":"port_in_use","port":"monero_rpc","message":"untrusted host detail"}`
	if err := os.WriteFile(a.portControlPath("status"), []byte(status), 0o600); err != nil {
		t.Fatal(err)
	}
	view := a.portControlView()
	if view.Status == nil || view.Status.Message != want {
		t.Fatalf("host conflict = %#v", view.Status)
	}
}

func TestPortControlCustomDashboardPort(t *testing.T) {
	a, _ := testApp(t, &rpcFixture{}, "dashboard-secret")
	a.cfg.moneroEnabled = true
	t.Setenv("MONERO_RPC_BIND_IP", "192.168.1.50")
	t.Setenv("DASHBOARD_BIND_IP", "192.168.1.50")
	t.Setenv("DASHBOARD_PORT", "8080")
	enablePortControl(t, a)
	response := portControlHTTP(a.routes(), http.MethodPost, "dashboard-secret", `{"changes":[{"port":"monero_rpc","enabled":true,"host_port":8080}]}`, true)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "already in use") {
		t.Fatalf("custom dashboard conflict = %d %s", response.Code, response.Body.String())
	}
	response = portControlHTTP(a.routes(), http.MethodPost, "dashboard-secret", `{"changes":[{"port":"monero_rpc","enabled":true,"host_port":80}]}`, true)
	if response.Code != http.StatusAccepted {
		t.Fatalf("unused default dashboard port = %d %s", response.Code, response.Body.String())
	}
}
