package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func nodeControlHTTP(a *app, method, path, body string, csrf bool) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Host = "mini-1.local"
	request.SetBasicAuth("admin", a.cfg.dashboardPassword)
	if csrf {
		request.Header.Set("X-Requested-With", "fetch")
	}
	response := httptest.NewRecorder()
	a.routes().ServeHTTP(response, request)
	return response
}

func TestNodeControlRequiresExactConfirmationAndQueuesPrivateRequest(t *testing.T) {
	dir := t.TempDir()
	node := nodeConfig{ID: nodeLitecoin, Name: "Litecoin", Enabled: true, Supported: true}
	a := &app{cfg: config{bitcoinDisabled: true, dashboardPassword: "secret", stateDir: dir, dataDir: dir, nodes: map[string]nodeConfig{nodeLitecoin: node}}}
	initializeTestDashboardAuth(t, a)
	if err := os.WriteFile(a.nodeControlPath("capability"), []byte("ready\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{
		`{"active":false}`, `{"active":false,"confirmation":"Deactivate"}`, `{"active":true,"confirmation":"deactivate"}`, `{"active":false,"confirmation":"deactivate","extra":true}`,
	} {
		if response := nodeControlHTTP(a, http.MethodPost, "/api/nodes/litecoin/power", body, true); response.Code != http.StatusBadRequest {
			t.Fatalf("invalid %s = %d %s", body, response.Code, response.Body.String())
		}
	}
	if response := nodeControlHTTP(a, http.MethodPost, "/api/nodes/litecoin/power", `{"active":false,"confirmation":"deactivate"}`, false); response.Code != http.StatusForbidden {
		t.Fatalf("missing CSRF = %d", response.Code)
	}
	response := nodeControlHTTP(a, http.MethodPost, "/api/nodes/litecoin/power", `{"active":false,"confirmation":"deactivate"}`, true)
	if response.Code != http.StatusAccepted {
		t.Fatalf("accepted = %d %s", response.Code, response.Body.String())
	}
	info, err := os.Stat(a.nodeControlPath("request"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("request mode = %#v, %v", info, err)
	}
	var queued nodeControlRequest
	b, _ := os.ReadFile(a.nodeControlPath("request"))
	if json.Unmarshal(b, &queued) != nil || queued.Node != nodeLitecoin || queued.Active || queued.Confirmation != "deactivate" || !validPortControlID(queued.ID) {
		t.Fatalf("request = %#v", queued)
	}
	nodesResponse := nodeControlHTTP(a, http.MethodGet, "/api/nodes", "", false)
	var nodesView struct {
		ControlPending bool               `json:"control_pending"`
		Status         *nodeControlStatus `json:"status"`
		Host           struct {
			DiskAvailable  bool   `json:"disk_available"`
			DiskTotalBytes uint64 `json:"disk_total_bytes"`
			DiskFreeBytes  uint64 `json:"disk_free_bytes"`
		} `json:"host"`
	}
	if nodesResponse.Code != http.StatusOK || json.Unmarshal(nodesResponse.Body.Bytes(), &nodesView) != nil || !nodesView.ControlPending || nodesView.Status == nil || nodesView.Status.ID != queued.ID || nodesView.Status.Node != nodeLitecoin || nodesView.Status.Active || nodesView.Status.State != "pending" ||
		!nodesView.Host.DiskAvailable || nodesView.Host.DiskTotalBytes == 0 || nodesView.Host.DiskFreeBytes > nodesView.Host.DiskTotalBytes {
		t.Fatalf("pending nodes view = %d %#v", nodesResponse.Code, nodesView)
	}
}

func TestNodesReadsPrivateRuntimeStateAndRejectsUnknownKeys(t *testing.T) {
	dir := t.TempDir()
	a := &app{cfg: config{bitcoinDisabled: true, stateDir: dir, nodes: map[string]nodeConfig{nodeLitecoin: {ID: nodeLitecoin, Enabled: true, Supported: true}}}}
	path := filepath.Join(dir, "node-runtime.json")
	if err := os.WriteFile(path, []byte(`{"litecoin":false}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if a.nodeActive(nodeLitecoin, true) {
		t.Fatal("saved inactive state ignored")
	}
	if err := os.WriteFile(path, []byte(`{"litecoin":false,"unknown":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if !a.nodeActive(nodeLitecoin, true) {
		t.Fatal("invalid runtime state did not fall back to configured state")
	}
}

func TestNodeRuntimeFallsBackToConfiguredActiveFlagsWhenStateIsMissing(t *testing.T) {
	for key, value := range map[string]string{
		"BITCOIN_ENABLED": "1", "BITCOIN_ACTIVE": "0", "RPC_USER": "bitcoin", "RPC_PASSWORD": "password",
		"MONERO_ENABLED": "1", "MONERO_ACTIVE": "1", "MONERO_RPC_USER": "monero", "MONERO_RPC_PASSWORD": "password",
		"TARI_ENABLED": "1", "TARI_ACTIVE": "0",
		"LITECOIN_ENABLED": "1", "LITECOIN_ACTIVE": "1", "LITECOIN_RPC_USER": "litecoin", "LITECOIN_RPC_PASSWORD": "password",
		"DOGECOIN_ENABLED": "1", "DOGECOIN_ACTIVE": "0", "DOGECOIN_RPC_USER": "dogecoin", "DOGECOIN_RPC_PASSWORD": "password",
		"BITCOINCASH_ENABLED": "1", "BITCOINCASH_ACTIVE": "1", "BITCOINCASH_RPC_USER": "bch", "BITCOINCASH_RPC_PASSWORD": "password",
		"ZCASH_ENABLED": "1", "ZCASH_ACTIVE": "0", "ZCASH_RPC_EXPOSE": "0",
	} {
		t.Setenv(key, value)
	}
	cfg, err := envConfig()
	if err != nil {
		t.Fatal(err)
	}
	cfg.stateDir = t.TempDir()
	a := &app{cfg: cfg}
	want := map[string]bool{nodeBitcoin: false, nodeMonero: true, nodeTari: false, nodeLitecoin: true, nodeDogecoin: false, nodeBitcoinCash: true, nodeZcash: false}
	for id, expected := range want {
		if got := a.nodeActive(id, !expected); got != expected {
			t.Errorf("%s active = %v, want %v", id, got, expected)
		}
	}
}

func TestNodeControlActivationAndAvailabilityAreIndependent(t *testing.T) {
	dir := t.TempDir()
	node := nodeConfig{ID: nodeLitecoin, Name: "Litecoin", Enabled: true, Supported: true}
	a := &app{cfg: config{bitcoinDisabled: true, dashboardPassword: "secret", stateDir: dir, dataDir: dir, nodes: map[string]nodeConfig{nodeLitecoin: node}}}
	initializeTestDashboardAuth(t, a)
	if err := os.WriteFile(filepath.Join(dir, "node-runtime.json"), []byte(`{"litecoin":false}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(a.nodeControlPath("capability"), []byte("ready\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	response := nodeControlHTTP(a, http.MethodPost, "/api/nodes/litecoin/power", `{"active":true,"confirmation":""}`, true)
	if response.Code != http.StatusAccepted {
		t.Fatalf("activate = %d %s", response.Code, response.Body.String())
	}
	var queued nodeControlRequest
	b, _ := os.ReadFile(a.nodeControlPath("request"))
	if json.Unmarshal(b, &queued) != nil || !queued.Active || queued.Confirmation != "" {
		t.Fatalf("activation request = %#v", queued)
	}
	if a.nodeAvailable(nodeLitecoin, true) {
		t.Fatal("active node was reported available before a successful probe")
	}
	a.recordNodeHealth(nodeLitecoin, true)
	if !a.nodeAvailable(nodeLitecoin, true) || a.nodeAvailable(nodeLitecoin, false) {
		t.Fatal("availability did not remain separate from active state")
	}
}

func TestNodeControlStatusCanonicalizesHostMessage(t *testing.T) {
	dir := t.TempDir()
	a := &app{cfg: config{stateDir: dir, nodes: map[string]nodeConfig{nodeDogecoin: {ID: nodeDogecoin, Enabled: true, Supported: true}}}}
	status := `{"id":"11111111111111111111111111111111","node":"dogecoin","active":false,"state":"failed","message":"private host path","error_code":"untrusted"}`
	if err := os.WriteFile(a.nodeControlPath("status"), []byte(status), 0o600); err != nil {
		t.Fatal(err)
	}
	got := a.readNodeControlStatus()
	if got == nil || got.Message != "Dogecoin state could not be changed." || got.ErrorCode != "" {
		t.Fatalf("status = %#v", got)
	}
}

func TestNodesReportsMalformedPrivateRequestAsPendingWithoutExposingIt(t *testing.T) {
	dir := t.TempDir()
	a := &app{cfg: config{bitcoinDisabled: true, dashboardPassword: "secret", stateDir: dir, nodes: map[string]nodeConfig{nodeDogecoin: {ID: nodeDogecoin, Enabled: true, Supported: true}}}}
	initializeTestDashboardAuth(t, a)
	if err := os.WriteFile(a.nodeControlPath("processing"), []byte(`{"private":"value"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	response := nodeControlHTTP(a, http.MethodGet, "/api/nodes", "", false)
	var view struct {
		ControlPending bool               `json:"control_pending"`
		Status         *nodeControlStatus `json:"status"`
	}
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &view) != nil || !view.ControlPending || view.Status == nil || view.Status.State != "pending" || view.Status.ID != "" || view.Status.Node != "" || strings.Contains(response.Body.String(), "private") {
		t.Fatalf("malformed pending view = %d %#v %s", response.Code, view, response.Body.String())
	}
}

func TestNodesHostStatsServedOnlyWhenFresh(t *testing.T) {
	dir := t.TempDir()
	node := nodeConfig{ID: nodeLitecoin, Name: "Litecoin", Enabled: true, Supported: true}
	a := &app{cfg: config{bitcoinDisabled: true, dashboardPassword: "secret", stateDir: dir, dataDir: dir, nodes: map[string]nodeConfig{nodeLitecoin: node}}}
	initializeTestDashboardAuth(t, a)
	write := func(updated int64) {
		stats := fmt.Sprintf(`{"updated":%d,"cpus":8,"mem_total_bytes":17179869184,"nodes":{"bitcoin":{"cpu_pct":12.5,"mem_bytes":1073741824}},"other":{"cpu_pct":1,"mem_bytes":2048}}`, updated)
		if err := os.WriteFile(filepath.Join(dir, "host-stats.json"), []byte(stats), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	read := func() *hostStatsFile {
		t.Helper()
		response := nodeControlHTTP(a, http.MethodGet, "/api/nodes", "", false)
		var view struct {
			Host struct {
				Stats *hostStatsFile `json:"stats"`
			} `json:"host"`
		}
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &view) != nil {
			t.Fatalf("nodes view = %d %s", response.Code, response.Body.String())
		}
		return view.Host.Stats
	}
	if read() != nil {
		t.Fatal("stats served without a collector file")
	}
	write(time.Now().Unix())
	stats := read()
	if stats == nil || stats.CPUs != 8 || stats.Nodes["bitcoin"].CPUPct != 12.5 || stats.Other.MemBytes != 2048 {
		t.Fatalf("fresh stats = %#v", stats)
	}
	write(time.Now().Add(-10 * time.Minute).Unix())
	if read() != nil {
		t.Fatal("stale stats were served")
	}
}
