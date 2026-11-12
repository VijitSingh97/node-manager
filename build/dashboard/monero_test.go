package main

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestMoneroAPIReportsDigestAuthenticatedNode(t *testing.T) {
	for _, algorithm := range []string{"MD5", "SHA-256"} {
		t.Run(algorithm, func(t *testing.T) {
			calls := 0
			blockTime := time.Now().Add(-2 * time.Minute).Unix()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Header.Get("Authorization") == "" {
					w.Header().Set("WWW-Authenticate", `Digest realm="monero-rpc", nonce="server-nonce", qop="auth", algorithm=`+algorithm+`, opaque="opaque-value"`)
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				if !validDigestRequest(r, algorithm, "rpc-user", "rpc-password") {
					http.Error(w, "bad digest", http.StatusUnauthorized)
					return
				}
				if r.URL.Path == "/get_transaction_pool_stats" {
					_ = json.NewEncoder(w).Encode(map[string]any{"status": "OK", "pool_stats": map[string]any{"bytes_total": 2_500_000, "txs_total": 23}})
					return
				}
				var rpcRequest struct {
					Method string `json:"method"`
				}
				if json.NewDecoder(r.Body).Decode(&rpcRequest) != nil {
					http.Error(w, "bad request", http.StatusBadRequest)
					return
				}
				result := map[string]any{}
				switch rpcRequest.Method {
				case "get_info":
					result = map[string]any{
						"synchronized": true, "busy_syncing": false, "height": 3500000, "target_height": 3500000,
						"version": "0.18.4.2-release", "start_time": time.Now().Add(-2 * time.Hour).Unix(),
						"incoming_connections_count": 4, "outgoing_connections_count": 12,
						"pruning_seed": 386, "database_size": 84_000_000_000, "difficulty": 720_000_000_000, "target": 120, "tx_pool_size": 19,
					}
				case "get_last_block_header":
					result = map[string]any{"block_header": map[string]any{"timestamp": blockTime}}
				case "get_fee_estimate":
					result = map[string]any{"fees": []uint64{20_000, 80_000, 320_000, 4_000_000}, "status": "OK"}
				default:
					http.Error(w, "bad method", http.StatusBadRequest)
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": "dashboard", "result": result})
			}))
			defer server.Close()

			a, _ := testApp(t, &rpcFixture{}, "dashboard-secret")
			a.cfg.moneroEnabled = true
			a.cfg.moneroRPCURL = server.URL
			a.cfg.moneroRPCUser = "rpc-user"
			a.cfg.moneroRPCPassword = "rpc-password"
			a.cfg.moneroDataDir = t.TempDir()
			a.cfg.moneroRPCEndpoint = "192.168.1.9:18081"
			a.cfg.moneroZMQEndpoint = "192.168.1.9:18083"
			response := request(t, a.routes(), http.MethodGet, "/api/monero", "dashboard-secret")
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
			var status moneroStatus
			if err := json.Unmarshal(response.Body.Bytes(), &status); err != nil {
				t.Fatal(err)
			}
			if calls != 8 || !status.Enabled || !status.Available || !status.Synced || status.SyncPercent != 100 || status.Height != 3500000 || status.Version != "0.18.4.2-release" || status.IncomingConnections != 4 || status.OutgoingConnections != 12 || !status.Pruned || status.DatabaseSizeBytes != 84_000_000_000 || !status.DatabaseSizeAvailable || status.UptimeSeconds < 7198 || !status.UptimeAvailable || status.MempoolTxs != 23 || !status.MempoolTxsAvailable || status.MempoolBytes != 2_500_000 || !status.MempoolBytesAvailable || status.Difficulty != 720_000_000_000 || !status.DifficultyAvailable || status.NetworkHashrate != 6_000_000_000 || !status.HashrateAvailable || !status.LatestBlockAvailable || status.LatestBlockAgeSeconds < 119 || !status.FeesAvailable || len(status.FeeEstimates) != 4 || status.FeeEstimates[2] != 320_000 || status.UpdatedAt <= 0 || !status.DiskAvailable || !status.RPCExposureEnabled || !status.ZMQExposureEnabled {
				t.Fatalf("calls = %d, status = %#v", calls, status)
			}
			if response.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("cache control = %q", response.Header().Get("Cache-Control"))
			}
		})
	}
}

func TestMoneroAPIDisabledAndUnavailableRemainJSON(t *testing.T) {
	a, handler := testApp(t, &rpcFixture{}, "dashboard-secret")
	disabled := request(t, handler, http.MethodGet, "/api/monero", "dashboard-secret")
	assertMoneroResponseShape(t, disabled, http.StatusOK, false, false, "")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", `Digest realm="monero-rpc", nonce="server-nonce", qop="auth", algorithm=MD5`)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()
	a.cfg.moneroEnabled = true
	a.cfg.moneroRPCURL = server.URL
	a.cfg.moneroRPCUser = "wrong-user"
	a.cfg.moneroRPCPassword = "wrong-password"
	unavailable := request(t, a.routes(), http.MethodGet, "/api/monero", "dashboard-secret")
	assertMoneroResponseShape(t, unavailable, http.StatusServiceUnavailable, true, false, "Monero RPC unavailable")
	if strings.Contains(unavailable.Body.String(), "wrong-user") || strings.Contains(unavailable.Body.String(), "wrong-password") || strings.Contains(unavailable.Body.String(), server.URL) {
		t.Fatal("unavailable response disclosed RPC connection details")
	}
}

func TestMoneroAPIMalformedResponseAndDashboardAuth(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "not json")
	}))
	defer server.Close()
	a, _ := testApp(t, &rpcFixture{}, "dashboard-secret")
	a.cfg.moneroEnabled = true
	a.cfg.moneroRPCURL = server.URL
	a.cfg.moneroRPCUser = "rpc-user"
	a.cfg.moneroRPCPassword = "rpc-password"
	handler := a.routes()
	if response := anonymousRequest(t, handler, http.MethodGet, "/api/monero"); response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d", response.Code)
	}
	malformed := request(t, handler, http.MethodGet, "/api/monero", "dashboard-secret")
	assertMoneroResponseShape(t, malformed, http.StatusServiceUnavailable, true, false, "Monero RPC unavailable")
}

func TestMoneroAPIKeepsCoreStatusWhenOptionalMetricsFail(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/get_transaction_pool_stats" {
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "Failed", "error": "busy"})
			return
		}
		if r.URL.Path != "/json_rpc" {
			http.Error(w, "optional metric unavailable", http.StatusServiceUnavailable)
			return
		}
		var rpcRequest struct {
			Method string `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&rpcRequest)
		if rpcRequest.Method != "get_info" {
			http.Error(w, "optional metric unavailable", http.StatusServiceUnavailable)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{
			"height": 3_500_000, "target_height": 3_500_001,
		}})
	}))
	defer server.Close()
	a, _ := testApp(t, &rpcFixture{}, "dashboard-secret")
	a.cfg.moneroEnabled = true
	a.cfg.moneroRPCURL = server.URL
	a.cfg.moneroRPCUser = "rpc-user"
	a.cfg.moneroRPCPassword = "rpc-password"
	response := request(t, a.routes(), http.MethodGet, "/api/monero", "dashboard-secret")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var status moneroStatus
	if err := json.Unmarshal(response.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if !status.Available || status.UptimeAvailable || status.DatabaseSizeAvailable || status.DifficultyAvailable || status.MempoolTxsAvailable || status.MempoolBytesAvailable || status.LatestBlockAvailable || status.FeesAvailable || status.Error != "" {
		t.Fatalf("status = %#v", status)
	}
}

func TestMoneroRPCCancellationStopsRequest(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer func() {
		close(release)
		server.Close()
	}()
	rpc := moneroRPC{url: server.URL, user: "user", password: "password", client: server.Client()}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		var info moneroInfo
		done <- rpc.call(ctx, "get_info", &info)
	}()
	<-started
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("canceled request succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("Monero RPC request ignored cancellation")
	}
}

func TestMoneroEnvironmentRequiresSeparateCredentials(t *testing.T) {
	t.Setenv("RPC_USER", "bitcoin-user")
	t.Setenv("RPC_PASSWORD", "bitcoin-password")
	t.Setenv("MONERO_ENABLED", "true")
	t.Setenv("MONERO_RPC_URL", "")
	t.Setenv("MONERO_RPC_USER", "monero-user")
	t.Setenv("MONERO_RPC_PASSWORD", "monero-password")
	defaultConfig, err := envConfig()
	if err != nil || defaultConfig.moneroRPCURL != "http://172.29.0.30:18085" {
		t.Fatalf("default Monero RPC configuration = %#v, %v", defaultConfig, err)
	}
	t.Setenv("MONERO_RPC_URL", "http://monero:18081")
	cfg, err := envConfig()
	if err != nil || !cfg.moneroEnabled || cfg.moneroRPCUser != "monero-user" || cfg.moneroRPCPassword != "monero-password" {
		t.Fatalf("configuration = %#v, %v", cfg, err)
	}
	t.Setenv("MONERO_RPC_PASSWORD", "")
	if _, err := envConfig(); err == nil {
		t.Fatal("enabled Monero accepted missing separate credentials")
	}
	t.Setenv("MONERO_RPC_PASSWORD", "monero-password")
	t.Setenv("MONERO_RPC_URL", "http://embedded:secret@monero:18081")
	if _, err := envConfig(); err == nil {
		t.Fatal("Monero URL accepted embedded credentials")
	}
}

func TestConfiguredLANEndpointsAreValidated(t *testing.T) {
	t.Setenv("RPC_USER", "bitcoin-user")
	t.Setenv("RPC_PASSWORD", "bitcoin-password")
	t.Setenv("MONERO_ENABLED", "1")
	t.Setenv("MONERO_RPC_USER", "monero-user")
	t.Setenv("MONERO_RPC_PASSWORD", "monero-password")
	t.Setenv("BITCOIN_RPC_EXPOSE", "1")
	t.Setenv("BITCOIN_RPC_BIND_IP", "192.168.10.20")
	t.Setenv("BITCOIN_RPC_PORT", "18332")
	t.Setenv("MONERO_RPC_EXPOSE", "1")
	t.Setenv("MONERO_RPC_BIND_IP", "192.168.10.20")
	t.Setenv("MONERO_RPC_PORT", "28081")
	t.Setenv("MONERO_ZMQ_EXPOSE", "1")
	t.Setenv("MONERO_ZMQ_BIND_IP", "192.168.10.20")
	t.Setenv("MONERO_ZMQ_PORT", "28083")
	cfg, err := envConfig()
	if err != nil || cfg.bitcoinRPCEndpoint != "192.168.10.20:18332" || cfg.moneroRPCEndpoint != "192.168.10.20:28081" || cfg.moneroZMQEndpoint != "192.168.10.20:28083" {
		t.Fatalf("LAN endpoints = %#v, %v", cfg, err)
	}
	t.Setenv("MONERO_RPC_BIND_IP", "monero.example")
	if _, err := envConfig(); err == nil {
		t.Fatal("enabled LAN endpoint accepted a hostname")
	}
	t.Setenv("MONERO_RPC_BIND_IP", "192.168.10.20")
	t.Setenv("MONERO_ZMQ_PORT", "70000")
	if _, err := envConfig(); err == nil {
		t.Fatal("enabled LAN endpoint accepted an invalid port")
	}
	t.Setenv("MONERO_ZMQ_PORT", "18083")
	t.Setenv("MONERO_ZMQ_BIND_IP", "8.8.8.8")
	if _, err := envConfig(); err == nil {
		t.Fatal("enabled LAN endpoint accepted a public bind address")
	}
}

func TestDisabledNodesIgnoreStaleExposureEnvironment(t *testing.T) {
	t.Setenv("BITCOIN_ENABLED", "0")
	t.Setenv("MONERO_ENABLED", "0")
	t.Setenv("BITCOIN_RPC_EXPOSE", "1")
	t.Setenv("MONERO_RPC_EXPOSE", "1")
	t.Setenv("MONERO_ZMQ_EXPOSE", "1")
	cfg, err := envConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.bitcoinRPCEndpoint != "" || cfg.moneroRPCEndpoint != "" || cfg.moneroZMQEndpoint != "" {
		t.Fatalf("disabled node endpoints = %#v", cfg)
	}
}

func assertMoneroResponseShape(t *testing.T, response *httptest.ResponseRecorder, code int, enabled, available bool, message string) {
	t.Helper()
	if response.Code != code || response.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("response = %d %q, content type = %q", response.Code, response.Body.String(), response.Header().Get("Content-Type"))
	}
	var raw map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	if raw["enabled"] != enabled || raw["available"] != available || raw["error"] != message || raw["mempool_txs"] == nil || raw["rpc_exposure_enabled"] == nil {
		t.Fatalf("response shape = %#v", raw)
	}
}

func validDigestRequest(r *http.Request, algorithm, username, password string) bool {
	authorization := r.Header.Get("Authorization")
	if !strings.HasPrefix(authorization, "Digest ") {
		return false
	}
	parameters := parseDigestParameters(strings.TrimPrefix(authorization, "Digest "))
	if parameters["username"] != username || parameters["realm"] != "monero-rpc" || parameters["nonce"] != "server-nonce" || parameters["uri"] != r.URL.RequestURI() || parameters["qop"] != "auth" || parameters["nc"] != "00000001" || parameters["opaque"] != "opaque-value" {
		return false
	}
	var digest func(string) string
	if algorithm == "SHA-256" {
		digest = func(value string) string {
			sum := sha256.Sum256([]byte(value))
			return hex.EncodeToString(sum[:])
		}
	} else {
		digest = func(value string) string {
			sum := md5.Sum([]byte(value))
			return hex.EncodeToString(sum[:])
		}
	}
	ha1 := digest(username + ":monero-rpc:" + password)
	ha2 := digest(r.Method + ":" + parameters["uri"])
	expected := digest(strings.Join([]string{ha1, "server-nonce", parameters["nc"], parameters["cnonce"], "auth", ha2}, ":"))
	return parameters["response"] == expected
}

func TestMoneroRPCRetriesStaleDigestNonce(t *testing.T) {
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		requests = append(requests, auth)
		switch {
		case auth == "":
			w.Header().Set("WWW-Authenticate", `Digest qop="auth", algorithm=MD5, realm="monero-rpc", nonce="first"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
		case strings.Contains(auth, `nonce="first"`):
			w.Header().Set("WWW-Authenticate", `Digest qop="auth", algorithm=MD5, realm="monero-rpc", nonce="second", stale=true`)
			http.Error(w, "stale", http.StatusUnauthorized)
		case strings.Contains(auth, `nonce="second"`):
			w.Header().Set("Content-Type", "application/json")
			if _, err := w.Write([]byte(`{"result":{"status":"OK"}}`)); err != nil {
				t.Error(err)
			}
		default:
			t.Errorf("unexpected authorization %q", auth)
		}
	}))
	defer server.Close()

	rpc := moneroRPC{url: server.URL, user: "alice", password: "secret", client: server.Client()}
	var out struct {
		Status string `json:"status"`
	}
	if err := rpc.call(t.Context(), "get_info", &out); err != nil {
		t.Fatalf("stale nonce was not retried: %v", err)
	}
	if out.Status != "OK" || len(requests) != 3 {
		t.Fatalf("status=%q requests=%d", out.Status, len(requests))
	}
}
