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

func TestNodeControlStatusUsesDashboardWordingForKnownCode(t *testing.T) {
	dir := t.TempDir()
	a := &app{cfg: config{stateDir: dir, nodes: map[string]nodeConfig{nodeDogecoin: {ID: nodeDogecoin, Enabled: true, Supported: true}}}}
	status := `{"id":"11111111111111111111111111111111","node":"dogecoin","active":true,"state":"rejected","message":"private host path","error_code":"config_pending"}`
	if err := os.WriteFile(a.nodeControlPath("status"), []byte(status), 0o600); err != nil {
		t.Fatal(err)
	}
	got := a.readNodeControlStatus()
	if got == nil || got.Message != "Dogecoin was not changed: review the pending config change first." || got.ErrorCode != "config_pending" {
		t.Fatalf("status = %#v", got)
	}
}

func TestNodeControlStatusShowsSanitizedDetailOnlyForFailures(t *testing.T) {
	dir := t.TempDir()
	a := &app{cfg: config{stateDir: dir, nodes: map[string]nodeConfig{nodeLitecoin: {ID: nodeLitecoin, Enabled: true, Supported: true}}}}
	read := func(state, code, detail string) *nodeControlStatus {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"id": "11111111111111111111111111111111", "node": "litecoin", "active": true, "state": state, "message": "private", "error_code": code, "detail": detail})
		if err := os.WriteFile(a.nodeControlPath("status"), body, 0o600); err != nil {
			t.Fatal(err)
		}
		return a.readNodeControlStatus()
	}
	reason := "Litecoin was not changed: its image could not be downloaded. Check the host's internet connection, then try again."
	if got := read("failed", "image_unavailable", "Error response from daemon: manifest unknown"); got == nil || got.Message != reason+" Error response from daemon: manifest unknown" || got.ErrorCode != "image_unavailable" || got.Detail != "" {
		t.Fatalf("status = %#v", got)
	}
	if got := read("rejected", "unchanged", "kept"); got == nil || !strings.HasSuffix(got.Message, " kept") {
		t.Fatalf("status = %#v", got)
	}
	if got := read("applied", "", "stale detail"); got == nil || got.Message != "Litecoin activated." {
		t.Fatalf("status = %#v", got)
	}
	if got := read("failed", "image_unavailable", ""); got == nil || got.Message != reason {
		t.Fatalf("status = %#v", got)
	}
	got := read("failed", "apply_failed", "bad\x1b[31m\nline\u0007"+strings.Repeat("x", 400))
	if got == nil || strings.ContainsAny(got.Message, "\x1b\n\a") || len([]rune(got.Message)) > len("Litecoin could not be changed; the prior state was restored. ")+240 {
		t.Fatalf("status = %#v", got)
	}
}

func TestNodeControlStatusStillRejectsUnknownFields(t *testing.T) {
	dir := t.TempDir()
	a := &app{cfg: config{stateDir: dir, nodes: map[string]nodeConfig{nodeLitecoin: {ID: nodeLitecoin, Enabled: true, Supported: true}}}}
	status := `{"id":"11111111111111111111111111111111","node":"litecoin","active":true,"state":"failed","error_code":"image_unavailable","detail":"x","extra":true}`
	if err := os.WriteFile(a.nodeControlPath("status"), []byte(status), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := a.readNodeControlStatus(); got != nil {
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
		stats := fmt.Sprintf(`{"updated":%d,"cpus":8,"mem_total_bytes":17179869184,"host_cpu_pct":37.5,"host_mem_bytes":8589934592,"nodes":{"bitcoin":{"cpu_pct":12.5,"mem_bytes":1073741824,"disk_bytes":650000000000}},"other":{"cpu_pct":1,"mem_bytes":2048},"disks":[{"name":"/dev/nvme0n1p2","label":"Samsung SSD 990 PRO NVMe","disk_total_bytes":1000,"disk_free_bytes":400,"nodes":["bitcoin"]},{"name":"/dev/sda1","disk_total_bytes":500,"disk_free_bytes":450,"nodes":[]}]}`, updated)
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
	if stats == nil || stats.CPUs != 8 || stats.HostCPUPct != 37.5 || stats.HostMemBytes != 8589934592 || stats.Nodes["bitcoin"].CPUPct != 12.5 || stats.Nodes["bitcoin"].DiskBytes != 650000000000 || stats.Other.MemBytes != 2048 {
		t.Fatalf("fresh stats = %#v", stats)
	}
	disks := a.hostDisks()
	if len(disks) != 2 || disks[0].Name != "/dev/nvme0n1p2" || disks[0].Label != "Samsung SSD 990 PRO NVMe" || disks[0].Nodes[0] != "bitcoin" || disks[1].Name != "/dev/sda1" || len(disks[1].Nodes) != 0 {
		t.Fatalf("collector disks = %#v", disks)
	}
	write(time.Now().Add(-10 * time.Minute).Unix())
	if read() != nil {
		t.Fatal("stale stats were served")
	}
}

func TestNodesHostStatsHealthReadingsKeepZeroAndOmitMissing(t *testing.T) {
	dir := t.TempDir()
	a := &app{cfg: config{bitcoinDisabled: true, dashboardPassword: "secret", stateDir: dir, dataDir: dir}}
	initializeTestDashboardAuth(t, a)
	read := func(extra string) map[string]any {
		t.Helper()
		stats := fmt.Sprintf(`{"updated":%d,"cpus":4,"mem_total_bytes":8589934592,"nodes":{},"other":{}%s}`, time.Now().Unix(), extra)
		if err := os.WriteFile(filepath.Join(dir, "host-stats.json"), []byte(stats), 0o600); err != nil {
			t.Fatal(err)
		}
		response := nodeControlHTTP(a, http.MethodGet, "/api/nodes", "", false)
		var view struct {
			Host struct {
				Stats map[string]any `json:"stats"`
			} `json:"host"`
		}
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &view) != nil || view.Host.Stats == nil {
			t.Fatalf("nodes view = %d %s", response.Code, response.Body.String())
		}
		return view.Host.Stats
	}
	got := read(`,"cpu_temp_c":59.5,"board_temp_c":45,"fans_rpm":[0,2506],"fans_max_rpm":[0,4825],"uptime_s":89878,"net_rx_bps":0,"net_tx_bps":1536`)
	want := map[string]any{"cpu_temp_c": 59.5, "board_temp_c": 45.0, "fans_rpm": []any{0.0, 2506.0}, "fans_max_rpm": []any{0.0, 4825.0}, "uptime_s": 89878.0, "net_rx_bps": 0.0, "net_tx_bps": 1536.0}
	for key, value := range want {
		if fmt.Sprint(got[key]) != fmt.Sprint(value) {
			t.Fatalf("%s = %#v, want %#v (stats %#v)", key, got[key], value, got)
		}
	}
	got = read("")
	for key := range want {
		if _, ok := got[key]; ok {
			t.Fatalf("missing sensor %s was served as %#v", key, got[key])
		}
	}
}

func TestMountDevicePicksLongestMountpointPrefix(t *testing.T) {
	mounts := "/dev/nvme0n1p2 / ext4 rw 0 0\n/dev/sdb2 /home ext4 rw 0 0\ntmpfs /run tmpfs rw 0 0\n/dev/sdb2 /home/nodes ext4 rw 0 0\n"
	if got := mountDevice(mounts, "/home/nodes/bitcoin"); got != "/dev/sdb2" {
		t.Fatalf("device = %q", got)
	}
	if got := mountDevice(mounts, "/var/lib/data"); got != "/dev/nvme0n1p2" {
		t.Fatalf("root fallback = %q", got)
	}
	if got := mountDevice("", "/anything"); got != "" {
		t.Fatalf("empty mounts = %q", got)
	}
}

func TestNodesReportWhyAStoppedNodeFailedToStart(t *testing.T) {
	dir := t.TempDir()
	node := nodeConfig{ID: nodeZcash, Name: "Zcash", Enabled: true, Supported: true}
	a := &app{cfg: config{bitcoinDisabled: true, dashboardPassword: "secret", stateDir: dir, dataDir: dir, nodes: map[string]nodeConfig{nodeZcash: node}}}
	initializeTestDashboardAuth(t, a)
	zcash := func(running bool) nodeControlNode {
		t.Helper()
		stats := fmt.Sprintf(`{"updated":%d,"cpus":4,"mem_total_bytes":8197607424,"nodes":{},"other":{"cpu_pct":0,"mem_bytes":0},"running":{"zcash":%t},"failed":{"zcash":"This Zakura binary requires CPU features this machine lacks: adx."}}`, time.Now().Unix(), running)
		if err := os.WriteFile(filepath.Join(dir, "host-stats.json"), []byte(stats), 0o600); err != nil {
			t.Fatal(err)
		}
		response := nodeControlHTTP(a, http.MethodGet, "/api/nodes", "", false)
		var view struct {
			Nodes []nodeControlNode `json:"nodes"`
		}
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &view) != nil {
			t.Fatalf("nodes view = %d %s", response.Code, response.Body.String())
		}
		for _, n := range view.Nodes {
			if n.ID == nodeZcash {
				return n
			}
		}
		t.Fatal("zcash missing")
		return nodeControlNode{}
	}
	if got := zcash(false); !got.Stopped || got.Failure != "This Zakura binary requires CPU features this machine lacks: adx." {
		t.Fatalf("stopped zcash = %#v", got)
	}
	if got := zcash(true); got.Stopped || got.Failure != "" {
		t.Fatalf("running zcash kept a stale failure: %#v", got)
	}
}

func TestNodesStoppedFromHostStats(t *testing.T) {
	dir := t.TempDir()
	node := nodeConfig{ID: nodeLitecoin, Name: "Litecoin", Enabled: true, Supported: true}
	a := &app{cfg: config{bitcoinDisabled: true, dashboardPassword: "secret", stateDir: dir, dataDir: dir, nodes: map[string]nodeConfig{nodeLitecoin: node}}}
	initializeTestDashboardAuth(t, a)
	stopped := func() bool {
		t.Helper()
		response := nodeControlHTTP(a, http.MethodGet, "/api/nodes", "", false)
		var view struct {
			Nodes []nodeControlNode `json:"nodes"`
		}
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &view) != nil {
			t.Fatalf("nodes view = %d %s", response.Code, response.Body.String())
		}
		for _, n := range view.Nodes {
			if n.ID == nodeLitecoin {
				return n.Stopped
			}
		}
		t.Fatal("litecoin missing")
		return false
	}
	write := func(updated time.Time, running string) {
		stats := fmt.Sprintf(`{"updated":%d,"cpus":8,"mem_total_bytes":17179869184,"nodes":{},"other":{"cpu_pct":0,"mem_bytes":0}%s}`, updated.Unix(), running)
		if err := os.WriteFile(filepath.Join(dir, "host-stats.json"), []byte(stats), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if stopped() {
		t.Fatal("stopped without host stats")
	}
	write(time.Now(), `,"running":{"litecoin":false}`)
	if !stopped() {
		t.Fatal("fresh report of a stopped container was ignored")
	}
	write(time.Now(), `,"running":{"litecoin":true}`)
	if stopped() {
		t.Fatal("running container marked stopped")
	}
	write(time.Now(), `,"running":{"tari":false}`)
	if stopped() {
		t.Fatal("unknown node marked stopped")
	}
	write(time.Now().Add(-10*time.Minute), `,"running":{"litecoin":false}`)
	if stopped() {
		t.Fatal("stale report marked stopped")
	}
	write(time.Now().Add(-time.Minute), `,"running":{"litecoin":false}`)
	if err := os.WriteFile(filepath.Join(dir, "node-runtime.json"), []byte(`{"litecoin":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if stopped() {
		t.Fatal("report older than the last node state change marked stopped")
	}
	if err := os.WriteFile(filepath.Join(dir, "node-runtime.json"), []byte(`{"litecoin":false}`), 0o600); err != nil {
		t.Fatal(err)
	}
	write(time.Now(), `,"running":{"litecoin":false}`)
	if stopped() {
		t.Fatal("deactivated node marked stopped")
	}
}

func TestNodeListCarriesTheLastHeightTheMonitorPersisted(t *testing.T) {
	dir := t.TempDir()
	state := `{"bitcoin":{"last_block":900000,"has_last_block":true,"sync_percent":100},"nodes":{"tari":{"last_block":150000,"has_last_block":true,"sync_percent":61.5}}}`
	if err := os.WriteFile(filepath.Join(dir, "monitor_state.json"), []byte(state), 0o600); err != nil {
		t.Fatal(err)
	}
	nodes := map[string]nodeConfig{nodeTari: {ID: nodeTari, Enabled: true, Supported: true}, nodeLitecoin: {ID: nodeLitecoin, Enabled: true, Supported: true}}
	a := &app{cfg: config{dashboardPassword: "secret", stateDir: dir, dataDir: dir, nodes: nodes}, monitor: NewMonitor("1.2.0", dir, dir)}
	initializeTestDashboardAuth(t, a)
	response := nodeControlHTTP(a, http.MethodGet, "/api/nodes", "", false)
	var payload struct {
		Nodes []map[string]any `json:"nodes"`
	}
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &payload) != nil {
		t.Fatalf("nodes = %d %s", response.Code, response.Body.String())
	}
	got := map[string][2]any{}
	for _, node := range payload.Nodes {
		got[node["id"].(string)] = [2]any{node["last_height"], node["last_sync_percent"]}
	}
	want := map[string][2]any{
		nodeBitcoin: {float64(900000), float64(100)}, nodeMonero: {nil, nil}, nodeTari: {float64(150000), 61.5},
		nodeLitecoin: {nil, nil}, nodeDogecoin: {nil, nil}, nodeBitcoinCash: {nil, nil}, nodeZcash: {nil, nil},
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("last heights = %v, want %v", got, want)
	}
}
