package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func writeHostCapability(t *testing.T, a *app, pending bool) string {
	t.Helper()
	revision := ""
	if pending {
		revision = strings.Repeat("a", 64)
	}
	b, _ := json.Marshal(hostControlCapability{Timestamp: time.Now().Unix(), Platform: "linux", BootKind: "boot", InstallSupported: true, MigrateSupported: true, BootSupported: true, BootEnabled: false, ConfigPending: pending, ConfigRevision: revision, ConfigPath: "/etc/node-manager/config.json", DataPaths: map[string]string{"bitcoin": "/var/lib/node-manager/data/bitcoin", "monero": "/var/lib/node-manager/data/monero"}})
	if err := os.WriteFile(a.nodeControlPath("capability"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	return revision
}

func hostControlHTTP(a *app, method, body string, csrf bool, remote string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "/api/settings/host", strings.NewReader(body))
	r.Host = "mini-1.local"
	r.RemoteAddr = remote
	r.SetBasicAuth("admin", "dashboard-secret")
	if csrf {
		r.Header.Set("X-Requested-With", "fetch")
	}
	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, r)
	return w
}

func TestHostControlQueuesInstallAndReportsBootState(t *testing.T) {
	a, _ := testApp(t, &rpcFixture{}, "")
	writeHostCapability(t, a, false)
	response := hostControlHTTP(a, http.MethodGet, "", false, "192.168.1.20:4321")
	var view hostControlView
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &view) != nil || !view.Available || !view.InstallSupported || !view.MigrateSupported || !view.BootSupported || view.BootEnabled || view.ConfigPath != "/etc/node-manager/config.json" || view.Nodes[1].DataDir != "/var/lib/node-manager/data/monero" {
		t.Fatalf("host view = %d %#v", response.Code, view)
	}
	response = hostControlHTTP(a, http.MethodPost, `{"action":"install","node":"monero"}`, true, "192.168.1.20:4321")
	if response.Code != http.StatusAccepted {
		t.Fatalf("install = %d %s", response.Code, response.Body.String())
	}
	b, err := os.ReadFile(a.nodeControlPath("request"))
	var request hostControlRequest
	if err != nil || json.Unmarshal(b, &request) != nil || request.Action != "install" || request.Node != "monero" || !validPortControlID(request.ID) {
		t.Fatalf("request = %#v, %v", request, err)
	}
}

func TestHostControlQueuesValidatedInstallOptions(t *testing.T) {
	a, _ := testApp(t, &rpcFixture{}, "")
	writeHostCapability(t, a, false)
	body := `{"action":"install","node":"monero","data_dir":"/srv/nodes/monero","interfaces":[{"id":"monero_rpc","enabled":true,"host_port":28081},{"id":"monero_zmq","enabled":false,"host_port":18083}]}`
	response := hostControlHTTP(a, http.MethodPost, body, true, "192.168.1.20:4321")
	if response.Code != http.StatusAccepted {
		t.Fatalf("install = %d %s", response.Code, response.Body.String())
	}
	b, err := os.ReadFile(a.nodeControlPath("request"))
	var request hostControlRequest
	if err != nil || json.Unmarshal(b, &request) != nil || request.DataDir != "/srv/nodes/monero" || len(request.Interfaces) != 2 || !request.Interfaces[0].Enabled || request.Interfaces[0].HostPort != 28081 {
		t.Fatalf("request = %#v, %v", request, err)
	}
}

func TestHostControlKeepsEmptyInstallInterfaces(t *testing.T) {
	for _, suffix := range []string{`,"interfaces":[]`, `,"interfaces":null`, ""} {
		a, _ := testApp(t, &rpcFixture{}, "")
		writeHostCapability(t, a, false)
		body := `{"action":"install","node":"zcash","data_dir":"/srv/nodes/zcash"` + suffix + `}`
		response := hostControlHTTP(a, http.MethodPost, body, true, "192.168.1.20:4321")
		if response.Code != http.StatusAccepted {
			t.Fatalf("install = %d %s", response.Code, response.Body.String())
		}
		b, err := os.ReadFile(a.nodeControlPath("request"))
		var raw map[string]json.RawMessage
		if err != nil || json.Unmarshal(b, &raw) != nil || string(raw["interfaces"]) != "[]" {
			t.Fatalf("request = %s, %v", b, err)
		}
	}
}

func TestHostControlKeepsAPendingInstallVisibleAndRejectsAnotherChange(t *testing.T) {
	a, _ := testApp(t, &rpcFixture{}, "")
	writeHostCapability(t, a, false)
	if got := hostControlHTTP(a, http.MethodPost, `{"action":"install","node":"monero"}`, true, "192.168.1.20:4321"); got.Code != http.StatusAccepted {
		t.Fatalf("install = %d %s", got.Code, got.Body.String())
	}
	status := func() *hostControlStatus {
		t.Helper()
		var view hostControlView
		if err := json.Unmarshal(hostControlHTTP(a, http.MethodGet, "", false, "192.168.1.20:4321").Body.Bytes(), &view); err != nil {
			t.Fatal(err)
		}
		return view.Status
	}
	for _, step := range []string{"request", "processing"} {
		if got := status(); got == nil || got.State != "pending" || got.Action != "install" || got.Node != "monero" {
			t.Fatalf("%s status = %#v", step, got)
		}
		for _, body := range []string{`{"action":"install","node":"monero"}`, `{"action":"migrate","node":"bitcoin","data_dir":"/mnt/big/bitcoin"}`, `{"action":"launch_at_boot","enabled":true}`} {
			if got := hostControlHTTP(a, http.MethodPost, body, true, "192.168.1.20:4321"); got.Code != http.StatusConflict {
				t.Fatalf("%s: second change %s = %d", step, body, got.Code)
			}
		}
		if step == "request" {
			if err := os.Rename(a.nodeControlPath("request"), a.nodeControlPath("processing")); err != nil {
				t.Fatal(err)
			}
		}
	}
	b, err := os.ReadFile(a.nodeControlPath("processing"))
	if err != nil {
		t.Fatal(err)
	}
	var request hostControlRequest
	if err := json.Unmarshal(b, &request); err != nil {
		t.Fatal(err)
	}
	failed, _ := json.Marshal(hostControlStatus{ID: request.ID, Action: "install", Node: "monero", State: "failed", ErrorCode: "apply_failed"})
	if err := os.WriteFile(a.nodeControlPath("status"), failed, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(a.nodeControlPath("processing")); err != nil {
		t.Fatal(err)
	}
	if got := status(); got == nil || got.State != "failed" || got.Node != "monero" || got.Message != "The host could not apply that change." {
		t.Fatalf("failed status = %#v", got)
	}
}

func TestHostControlRejectsUnsafeInstallOptions(t *testing.T) {
	for _, body := range []string{
		`{"action":"install","node":"monero","data_dir":"../monero","interfaces":[]}`,
		`{"action":"install","node":"monero","data_dir":"/srv/monero","interfaces":[{"id":"bitcoin_rpc","enabled":true,"host_port":8332}]}`,
		`{"action":"install","node":"monero","data_dir":"/srv/monero","interfaces":[{"id":"monero_rpc","enabled":true,"host_port":0}]}`,
		`{"action":"install","node":"monero","data_dir":"/srv/monero","interfaces":[{"id":"monero_rpc","enabled":true,"host_port":18081},{"id":"monero_rpc","enabled":false,"host_port":18082}]}`,
	} {
		a, _ := testApp(t, &rpcFixture{}, "")
		writeHostCapability(t, a, false)
		if got := hostControlHTTP(a, http.MethodPost, body, true, "192.168.1.20:4321"); got.Code != http.StatusBadRequest {
			t.Fatalf("unsafe install accepted: %s = %d", body, got.Code)
		}
	}
}

func TestHostControlRequiresTrustedLANAndRevisionBoundApproval(t *testing.T) {
	a, _ := testApp(t, &rpcFixture{}, "")
	revision := writeHostCapability(t, a, true)
	if got := hostControlHTTP(a, http.MethodPost, `{"action":"apply_config","config_revision":"`+strings.Repeat("b", 64)+`"}`, true, "192.168.1.20:4321"); got.Code != http.StatusServiceUnavailable {
		t.Fatalf("wrong revision = %d", got.Code)
	}
	if got := hostControlHTTP(a, http.MethodPost, `{"action":"install","node":"monero"}`, true, "192.168.1.20:4321"); got.Code != http.StatusConflict {
		t.Fatalf("install bypassed pending config = %d", got.Code)
	}
	if got := hostControlHTTP(a, http.MethodPost, `{"action":"apply_config","config_revision":"`+revision+`"}`, false, "192.168.1.20:4321"); got.Code != http.StatusForbidden {
		t.Fatalf("missing CSRF accepted = %d", got.Code)
	}
	if got := hostControlHTTP(a, http.MethodPost, `{"action":"apply_config","config_revision":"`+revision+`"}`, true, "192.168.1.20:4321"); got.Code != http.StatusAccepted {
		t.Fatalf("approval = %d %s", got.Code, got.Body.String())
	}
}

func TestHostControlQueuesNetworkToggles(t *testing.T) {
	a, _ := testApp(t, &rpcFixture{}, "")
	b, _ := json.Marshal(hostControlCapability{Timestamp: time.Now().Unix(), Platform: "source", BootKind: "unsupported", ClearnetOverTor: true, InboundTor: false})
	if err := os.WriteFile(a.nodeControlPath("capability"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	var view hostControlView
	response := hostControlHTTP(a, http.MethodGet, "", false, "192.168.1.20:4321")
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &view) != nil || !view.Available || !view.ClearnetOverTor || view.InboundTor {
		t.Fatalf("network view = %d %s", response.Code, response.Body.String())
	}
	for _, body := range []string{`{"action":"inbound_connections"}`, `{"action":"clearnet_over_tor","enabled":true,"node":"bitcoin"}`, `{"action":"clearnet_over_tor","enabled":"yes"}`} {
		if got := hostControlHTTP(a, http.MethodPost, body, true, "192.168.1.20:4321"); got.Code != http.StatusBadRequest {
			t.Fatalf("%s = %d", body, got.Code)
		}
	}
	response = hostControlHTTP(a, http.MethodPost, `{"action":"inbound_connections","enabled":true}`, true, "192.168.1.20:4321")
	if response.Code != http.StatusAccepted {
		t.Fatalf("inbound toggle = %d %s", response.Code, response.Body.String())
	}
	b, err := os.ReadFile(a.nodeControlPath("request"))
	var request map[string]any
	if err != nil || json.Unmarshal(b, &request) != nil || request["action"] != "inbound_connections" || request["enabled"] != true || len(request) != 4 {
		t.Fatalf("request = %s, %v", b, err)
	}
	disabled := false
	for action, want := range map[string]string{"clearnet_over_tor": "Nodes now use onion peers only.", "inbound_connections": "Incoming Tor connections disabled."} {
		if got := canonicalHostStatusMessage(hostControlStatus{Action: action, Enabled: &disabled, State: "applied"}); got != want {
			t.Fatalf("%s message = %q", action, got)
		}
	}
}

func TestHostStatusCanonicalizesHelperErrors(t *testing.T) {
	enabled := true
	status := hostControlStatus{Action: "launch_at_boot", Enabled: &enabled, State: "failed", Message: "untrusted host output", ErrorCode: "ownership_conflict"}
	if got := canonicalHostStatusMessage(status); got != "The node data directory is managed by another installation." {
		t.Fatalf("canonical message = %q", got)
	}
	status.ErrorCode = "invented"
	if got := canonicalHostStatusMessage(status); got != "Host change could not be applied." {
		t.Fatalf("unknown failure = %q", got)
	}
}

func TestHostControlQueuesMigrateForInstalledNodesOnly(t *testing.T) {
	a, _ := testApp(t, &rpcFixture{}, "")
	writeHostCapability(t, a, false)
	response := hostControlHTTP(a, http.MethodPost, `{"action":"migrate","node":"bitcoin","data_dir":"/mnt/big/bitcoin"}`, true, "192.168.1.20:4321")
	if response.Code != http.StatusAccepted {
		t.Fatalf("migrate = %d %s", response.Code, response.Body.String())
	}
	b, err := os.ReadFile(a.nodeControlPath("request"))
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if json.Unmarshal(b, &raw) != nil || len(raw) != 5 || raw["action"] != "migrate" || raw["node"] != "bitcoin" || raw["data_dir"] != "/mnt/big/bitcoin" {
		t.Fatalf("request = %s", b)
	}
	if err := os.Remove(a.nodeControlPath("request")); err != nil {
		t.Fatal(err)
	}
	response = hostControlHTTP(a, http.MethodPost, `{"action":"migrate","node":"monero","data_dir":"/mnt/big/monero"}`, true, "192.168.1.20:4321")
	if response.Code != http.StatusConflict {
		t.Fatalf("migrate of uninstalled node = %d", response.Code)
	}
	response = hostControlHTTP(a, http.MethodPost, `{"action":"migrate","node":"bitcoin","data_dir":"relative"}`, true, "192.168.1.20:4321")
	if response.Code != http.StatusBadRequest {
		t.Fatalf("migrate with relative path = %d", response.Code)
	}
	applied := canonicalHostStatusMessage(hostControlStatus{Action: "migrate", State: "applied", Node: "bitcoin"})
	if applied != "Bitcoin data migrated." {
		t.Fatalf("applied message = %q", applied)
	}
	space := canonicalHostStatusMessage(hostControlStatus{Action: "migrate", State: "rejected", ErrorCode: "insufficient_space"})
	if space != "The destination disk does not have enough free space." {
		t.Fatalf("space message = %q", space)
	}
}

func TestHostControlShowsMigrateProgressWhileProcessing(t *testing.T) {
	a, _ := testApp(t, &rpcFixture{}, "")
	writeHostCapability(t, a, false)
	id := strings.Repeat("ab", 16)
	request, _ := json.Marshal(map[string]any{"id": id, "action": "migrate", "node": "bitcoin", "data_dir": "/mnt/big/bitcoin", "requested_at": time.Now().Unix()})
	if err := os.WriteFile(a.nodeControlPath("processing"), request, 0o600); err != nil {
		t.Fatal(err)
	}
	progress, _ := json.Marshal(migrateProgress{ID: id, Node: "bitcoin", Percent: 42, CopiedKB: 44 * 1024 * 1024, TotalKB: 104 * 1024 * 1024, UpdatedAt: time.Now().Unix()})
	if err := os.WriteFile(a.nodeControlPath("progress"), progress, 0o600); err != nil {
		t.Fatal(err)
	}
	status, pending := a.hostControlStatus()
	if !pending || status == nil || status.State != "pending" || !strings.Contains(status.Message, "42%") || !strings.Contains(status.Message, "44.0 of 104.0 GiB") {
		t.Fatalf("status = %#v pending=%v", status, pending)
	}
	stale, _ := json.Marshal(migrateProgress{ID: id, Node: "bitcoin", Percent: 50, CopiedKB: 1, TotalKB: 2, UpdatedAt: time.Now().Unix() - 600})
	if err := os.WriteFile(a.nodeControlPath("progress"), stale, 0o600); err != nil {
		t.Fatal(err)
	}
	status, _ = a.hostControlStatus()
	if status == nil || !strings.Contains(status.Message, "can take a while") {
		t.Fatalf("stale progress was trusted: %#v", status)
	}
}
