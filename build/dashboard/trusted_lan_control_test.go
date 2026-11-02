package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func anonymousMutation(handler http.Handler, method, path, body, remote string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Host = "mini-1.local"
	request.RemoteAddr = remote
	request.Header.Set("X-Requested-With", "fetch")
	request.Header.Set("Origin", "http://mini-1.local")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func TestAnonymousWalletMutationsRequireAuthentication(t *testing.T) {
	bitcoin, bitcoinHandler := testApp(t, &rpcFixture{}, "")
	_ = bitcoin
	moneroFixture := &moneroWalletFixture{}
	_, moneroHandler, _ := newMoneroWatchTestAppWithOptions(t, moneroFixture, false, "")
	_, familyHandler, _, _ := newFamilyWatchTestApp(t, "")
	for name, fixture := range map[string]struct {
		handler http.Handler
		path    string
	}{
		"bitcoin":  {bitcoinHandler, "/api/watch"},
		"monero":   {moneroHandler, "/api/monero/watch"},
		"litecoin": {familyHandler, "/api/litecoin/watch"},
	} {
		t.Run(name, func(t *testing.T) {
			trusted := anonymousMutation(fixture.handler, http.MethodPost, fixture.path, `{}`, "192.168.1.20:4321")
			if trusted.Code != http.StatusUnauthorized {
				t.Fatalf("trusted LAN anonymous request = %d: %s", trusted.Code, trusted.Body.String())
			}
			public := anonymousMutation(fixture.handler, http.MethodPost, fixture.path, `{}`, "203.0.113.8:4321")
			if public.Code != http.StatusUnauthorized {
				t.Fatalf("public remote accepted = %d: %s", public.Code, public.Body.String())
			}
		})
	}
}

func TestAnonymousTrustedLANCannotQueueNodeAndPortChanges(t *testing.T) {
	nodeDir := t.TempDir()
	node := nodeConfig{ID: nodeLitecoin, Name: "Litecoin", Enabled: true, Supported: true}
	nodeApp := &app{cfg: config{bitcoinDisabled: true, dashboardPassword: "dashboard-secret", stateDir: nodeDir, nodes: map[string]nodeConfig{nodeLitecoin: node}}}
	initializeTestDashboardAuth(t, nodeApp)
	if err := os.WriteFile(nodeApp.nodeControlPath("capability"), []byte("ready\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	nodeResponse := anonymousMutation(nodeApp.routes(), http.MethodPost, "/api/nodes/litecoin/power", `{"active":false,"confirmation":"deactivate"}`, "192.168.1.20:4321")
	if nodeResponse.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous trusted node change = %d: %s", nodeResponse.Code, nodeResponse.Body.String())
	}

	portApp, _ := testApp(t, &rpcFixture{}, "")
	portApp.cfg.bitcoinRPCEndpoint = "192.168.1.50:8332"
	enablePortControl(t, portApp)
	portResponse := anonymousMutation(portApp.routes(), http.MethodPost, "/api/settings/ports", `{"changes":[{"port":"bitcoin_rpc","enabled":false}]}`, "192.168.1.20:4321")
	if portResponse.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous trusted port change = %d: %s", portResponse.Code, portResponse.Body.String())
	}
}
