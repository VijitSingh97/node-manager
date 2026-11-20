package main

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
)

func writeHostNodeStats(t *testing.T, a *app, age time.Duration, usage hostStatsUsage) {
	t.Helper()
	writeControlFile(t, filepath.Join(a.cfg.stateDir, "host-stats.json"), hostStatsFile{
		Updated: time.Now().Add(-age).Unix(), CPUs: 4, MemTotalBytes: 1 << 30,
		Nodes: map[string]hostStatsUsage{nodeTari: usage, nodeBitcoin: usage},
	}, 0)
}

func TestFillFromHostStats(t *testing.T) {
	started := time.Now().Add(-2 * time.Hour).Unix()
	host := hostStatsUsage{DiskBytes: 5 << 30, StartedAt: started}
	for name, tc := range map[string]struct {
		age        time.Duration
		usage      hostStatsUsage
		status     genericNodeStatus
		wantSize   int64
		wantSizeOK bool
		wantUptime bool
	}{
		"node reports both": {usage: host, status: genericNodeStatus{DatabaseSizeBytes: 7, DatabaseSizeAvailable: true, UptimeSeconds: 9, UptimeAvailable: true}, wantSize: 7, wantSizeOK: true, wantUptime: true},
		"node silent":       {usage: host, wantSize: 5 << 30, wantSizeOK: true, wantUptime: true},
		"node size zero":    {usage: host, status: genericNodeStatus{DatabaseSizeAvailable: true}, wantSize: 5 << 30, wantSizeOK: true, wantUptime: true},
		"stale stats":       {age: 10 * time.Minute, usage: host},
		"no host values":    {},
		"future start":      {usage: hostStatsUsage{StartedAt: time.Now().Add(time.Hour).Unix()}},
	} {
		t.Run(name, func(t *testing.T) {
			a := &app{cfg: config{stateDir: t.TempDir()}}
			writeHostNodeStats(t, a, tc.age, tc.usage)
			status := tc.status
			a.fillFromHostStats(nodeTari, &status)
			if status.DatabaseSizeBytes != tc.wantSize || status.DatabaseSizeAvailable != tc.wantSizeOK {
				t.Errorf("size = %d available=%v", status.DatabaseSizeBytes, status.DatabaseSizeAvailable)
			}
			if status.UptimeAvailable != tc.wantUptime {
				t.Fatalf("uptime available = %v", status.UptimeAvailable)
			}
			if tc.status.UptimeAvailable && status.UptimeSeconds != 9 {
				t.Errorf("node uptime replaced: %d", status.UptimeSeconds)
			}
			if tc.wantUptime && !tc.status.UptimeAvailable && (status.UptimeSeconds < 7199 || status.UptimeSeconds > 7205) {
				t.Errorf("uptime = %d", status.UptimeSeconds)
			}
		})
	}
	a := &app{cfg: config{stateDir: t.TempDir()}}
	status := genericNodeStatus{}
	a.fillFromHostStats(nodeTari, &status)
	if status.DatabaseSizeAvailable || status.UptimeAvailable {
		t.Error("missing host stats produced values")
	}
}

func TestTariStatusUsesHostSizeAndUptime(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	RegisterBaseNodeServer(server, tariFixture{timestamp: uint64(time.Now().Unix())})
	go server.Serve(listener)
	defer server.Stop()
	dir := t.TempDir()
	node := nodeConfig{ID: nodeTari, Enabled: true, Supported: true, RPCURL: "http://" + listener.Addr().String(), DataDir: dir}
	a := &app{cfg: config{stateDir: dir}}
	writeHostNodeStats(t, a, 0, hostStatsUsage{DiskBytes: 3 << 30, StartedAt: time.Now().Add(-time.Hour).Unix()})
	status, err := a.fetchTariStatus(t.Context(), node, genericNodeStatus{Enabled: true, Active: true, Supported: true})
	if err != nil || !status.DatabaseSizeAvailable || status.DatabaseSizeBytes != 3<<30 || !status.UptimeAvailable || status.UptimeSeconds < 3599 {
		t.Fatalf("Tari status = %#v, %v", status, err)
	}
}

func bitcoinPageBody(t *testing.T, fixture *rpcFixture, age time.Duration, usage *hostStatsUsage) string {
	t.Helper()
	a, handler := testApp(t, fixture, "")
	if usage != nil {
		writeHostNodeStats(t, a, age, *usage)
	}
	response := request(t, handler, http.MethodGet, "/", "")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	return response.Body.String()
}

func TestBitcoinPageFallsBackToHostStats(t *testing.T) {
	usage := &hostStatsUsage{DiskBytes: 3 << 30, StartedAt: time.Now().Add(-2*time.Hour - time.Minute).Unix()}
	body := bitcoinPageBody(t, &rpcFixture{idleSynced: true}, 0, usage)
	for _, want := range []string{"3.00 GiB", "2h 1m"} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks host fallback %q", want)
		}
	}
	for name, body := range map[string]string{
		"stale":   bitcoinPageBody(t, &rpcFixture{idleSynced: true}, 10*time.Minute, usage),
		"missing": bitcoinPageBody(t, &rpcFixture{idleSynced: true}, 0, nil),
	} {
		if strings.Contains(body, "GiB</span>") || !strings.Contains(body, "Uptime:</span> <span>—</span>") || !strings.Contains(body, "Node Data Size:</span> <span>—</span>") {
			t.Errorf("%s host stats still filled the page", name)
		}
	}
	reported := bitcoinPageBody(t, &rpcFixture{}, 0, usage)
	if !strings.Contains(reported, "11.00 GiB") || !strings.Contains(reported, "1d 1h 1m") {
		t.Error("the node's own size and uptime must win over host stats")
	}
}

func TestBitcoinPageShowsDashWhenPeersFail(t *testing.T) {
	body := bitcoinPageBody(t, &rpcFixture{noPeers: true}, 0, nil)
	if !strings.Contains(body, "Connections:</span> <span>—</span>") || strings.Contains(body, "0 in") {
		t.Error("failed getpeerinfo must render a dash")
	}
	if ok := bitcoinPageBody(t, &rpcFixture{}, 0, nil); !strings.Contains(ok, "1 in · 1 out") {
		t.Error("peer counts missing")
	}
}

func TestOpenPortsRowsAreGone(t *testing.T) {
	if body := bitcoinPageBody(t, &rpcFixture{}, 0, nil); strings.Contains(body, "Open ports") {
		t.Error("dead Open ports row returned")
	}
}

func TestFamilyAndZcashStatusUseOwnHostStats(t *testing.T) {
	now := time.Now()
	usages := map[string]hostStatsUsage{}
	for i, id := range []string{nodeTari, nodeBitcoin, nodeLitecoin, nodeDogecoin, nodeBitcoinCash, nodeZcash} {
		usages[id] = hostStatsUsage{DiskBytes: uint64(i+1) << 30, StartedAt: now.Add(-time.Duration(i+1) * time.Hour).Unix()}
	}
	for _, tc := range []struct {
		id       string
		protocol string
	}{
		{nodeDogecoin, "bitcoin-json-rpc"},
		{nodeLitecoin, "bitcoin-json-rpc"},
		{nodeBitcoinCash, "bitcoin-json-rpc"},
		{nodeZcash, "zcash-json-rpc"},
	} {
		t.Run(tc.id, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request struct {
					Method string `json:"method"`
				}
				json.NewDecoder(r.Body).Decode(&request)
				if request.Method != "getblockchaininfo" {
					writeJSON(w, map[string]any{"result": nil, "error": map[string]any{"code": -32601, "message": "Method not found"}})
					return
				}
				writeJSON(w, map[string]any{"result": map[string]any{"blocks": 10, "headers": 10}, "error": nil})
			}))
			defer server.Close()
			dir := t.TempDir()
			node := nodeConfig{ID: tc.id, Name: tc.id, Enabled: true, Supported: true, Protocol: tc.protocol, RPCURL: server.URL, DataDir: dir}
			a := &app{cfg: config{stateDir: dir, nodes: map[string]nodeConfig{tc.id: node}}}
			writeControlFile(t, filepath.Join(dir, "host-stats.json"), hostStatsFile{Updated: now.Unix(), CPUs: 4, MemTotalBytes: 1 << 30, Nodes: usages}, 0)
			status := fetchNodeStatusForTest(t, a, tc.id)
			want := usages[tc.id]
			if !status.DatabaseSizeAvailable || uint64(status.DatabaseSizeBytes) != want.DiskBytes || !status.UptimeAvailable || status.UptimeSeconds < now.Unix()-want.StartedAt-1 || status.UptimeSeconds > now.Unix()-want.StartedAt+5 {
				t.Fatalf("%s status = %#v", tc.id, status)
			}
			writeHostNodeStats(t, a, 10*time.Minute, want)
			if stale := fetchNodeStatusForTest(t, a, tc.id); stale.DatabaseSizeAvailable || stale.UptimeAvailable {
				t.Fatalf("%s stale host stats filled %#v", tc.id, stale)
			}
		})
	}
}

func fetchNodeStatusForTest(t *testing.T, a *app, id string) genericNodeStatus {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/api/"+id, nil)
	request.SetPathValue("node", id)
	response := httptest.NewRecorder()
	a.apiAdditionalNode(response, request)
	var status genericNodeStatus
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &status) != nil {
		t.Fatalf("status response = %d %s", response.Code, response.Body.String())
	}
	return status
}
