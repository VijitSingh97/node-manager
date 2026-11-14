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

func TestAdditionalNodeConfigRequiresSeparateCredentials(t *testing.T) {
	t.Setenv("BITCOIN_ENABLED", "false")
	t.Setenv("LITECOIN_ENABLED", "true")
	t.Setenv("LITECOIN_RPC_USER", "litecoin-user")
	t.Setenv("LITECOIN_RPC_PASSWORD", "litecoin-password")
	t.Setenv("LITECOIN_STEADY_CPUS", "1.5")
	t.Setenv("LITECOIN_STEADY_MEM_LIMIT_MB", "3072")
	t.Setenv("LITECOIN_RESOURCE_PROFILE", "compact")
	cfg, err := envConfig()
	if err != nil || !cfg.nodes[nodeLitecoin].Enabled || cfg.nodes[nodeLitecoin].RPCURL != "http://172.29.0.33:9332" || cfg.nodes[nodeLitecoin].SteadyCPUs != 1.5 || cfg.nodes[nodeLitecoin].SteadyMemLimitMB != 3072 || cfg.nodes[nodeLitecoin].ResourceProfile != "compact" {
		t.Fatalf("Litecoin config = %#v, %v", cfg.nodes[nodeLitecoin], err)
	}
	t.Setenv("LITECOIN_RPC_PASSWORD", "")
	if _, err := envConfig(); err == nil || !strings.Contains(err.Error(), "LITECOIN_RPC_USER") {
		t.Fatalf("missing Litecoin password error = %v", err)
	}
}

func TestAdditionalNodeConfigRejectsInvalidResourceAndEndpointValues(t *testing.T) {
	t.Setenv("BITCOIN_ENABLED", "false")
	t.Setenv("TARI_GRPC_URL", "http://172.29.0.32:18142/path")
	if _, err := envConfig(); err == nil || !strings.Contains(err.Error(), "TARI_GRPC_URL") {
		t.Fatalf("Tari path error = %v", err)
	}
	t.Setenv("TARI_GRPC_URL", "http://172.29.0.32:18142")
	t.Setenv("LITECOIN_RPC_URL", "http://rpc.public.example:9332")
	if _, err := envConfig(); err == nil || !strings.Contains(err.Error(), "internal HTTP URL") {
		t.Fatalf("public RPC error = %v", err)
	}
	t.Setenv("LITECOIN_RPC_URL", "http://litecoin:9332")
	t.Setenv("DOGECOIN_STEADY_MEM_LIMIT_MB", "64")
	if _, err := envConfig(); err == nil || !strings.Contains(err.Error(), "DOGECOIN_STEADY_MEM_LIMIT_MB") {
		t.Fatalf("Dogecoin memory error = %v", err)
	}
	t.Setenv("DOGECOIN_STEADY_MEM_LIMIT_MB", "4096")
	t.Setenv("BITCOINCASH_RESOURCE_PROFILE", "fast")
	if _, err := envConfig(); err == nil || !strings.Contains(err.Error(), "BITCOINCASH_RESOURCE_PROFILE") {
		t.Fatalf("Bitcoin Cash profile error = %v", err)
	}
}

func TestZcashConfigSeparatesPrivateNativeRPCFromAuthenticatedExposure(t *testing.T) {
	t.Setenv("BITCOIN_ENABLED", "false")
	t.Setenv("ZCASH_ENABLED", "true")
	t.Setenv("ZCASH_ACTIVE", "false")
	t.Setenv("ZCASH_RPC_EXPOSE", "false")
	cfg, err := envConfig()
	node := cfg.nodes[nodeZcash]
	if err != nil || !node.Enabled || node.Active || !node.ActiveSet || !node.Supported || node.Protocol != "zcash-json-rpc" || node.RPCURL != "http://172.29.0.36:8232" || node.RPCEndpoint != "" {
		t.Fatalf("private Zcash config = %#v, %v", node, err)
	}
	t.Setenv("ZCASH_RPC_EXPOSE", "true")
	t.Setenv("ZCASH_RPC_BIND_IP", "192.168.1.20")
	if _, err := envConfig(); err == nil || !strings.Contains(err.Error(), "ZCASH_RPC_USER") {
		t.Fatalf("missing proxy credentials error = %v", err)
	}
	t.Setenv("ZCASH_RPC_USER", "zcash")
	t.Setenv("ZCASH_RPC_PASSWORD", "password")
	cfg, err = envConfig()
	if err != nil || cfg.nodes[nodeZcash].RPCEndpoint != "192.168.1.20:8232" {
		t.Fatalf("exposed Zcash config = %#v, %v", cfg.nodes[nodeZcash], err)
	}
	t.Setenv("PORT", "18232")
	if _, err := envConfig(); err == nil || !strings.Contains(err.Error(), "internal Zcash proxy port") {
		t.Fatalf("proxy listener collision error = %v", err)
	}
}

func TestExistingNodeRPCsRejectDirectPublicEgress(t *testing.T) {
	t.Setenv("RPC_USER", "bitcoin-user")
	t.Setenv("RPC_PASSWORD", "bitcoin-password")
	t.Setenv("RPC_URL", "http://rpc.public.example:8332")
	if _, err := envConfig(); err == nil || !strings.Contains(err.Error(), "RPC_URL must be an internal HTTP URL") {
		t.Fatalf("public Bitcoin RPC error = %v", err)
	}
	t.Setenv("BITCOIN_ENABLED", "false")
	t.Setenv("MONERO_ENABLED", "true")
	t.Setenv("MONERO_RPC_USER", "monero-user")
	t.Setenv("MONERO_RPC_PASSWORD", "monero-password")
	t.Setenv("MONERO_RPC_URL", "http://rpc.public.example:18081")
	if _, err := envConfig(); err == nil || !strings.Contains(err.Error(), "MONERO_RPC_URL must be an internal HTTP URL") {
		t.Fatalf("public Monero RPC error = %v", err)
	}
	t.Setenv("MONERO_RPC_URL", "http://monero:18081")
	t.Setenv("MONERO_WALLET_RPC_USER", "wallet-user")
	t.Setenv("MONERO_WALLET_RPC_PASSWORD", "wallet-rpc-password")
	t.Setenv("MONERO_WALLET_PASSWORD", "wallet-password")
	t.Setenv("MONERO_WALLET_RPC_URL", "http://wallet.public.example:18082")
	if _, err := envConfig(); err == nil || !strings.Contains(err.Error(), "MONERO_WALLET_RPC_URL must be an internal HTTP URL") {
		t.Fatalf("public Monero wallet RPC error = %v", err)
	}
}

func TestBitcoinFamilyStatusUsesOnlyPublicNodeRPC(t *testing.T) {
	for _, tc := range []struct{ id, name, version, unit, fee string }{
		{nodeLitecoin, "Litecoin", "/LitecoinCore:0.21.4/", "litoshi/vB", "1"},
		{nodeDogecoin, "Dogecoin", "/Shibetoshi:1.14.9/", "DOGE/kB", "0.00001"},
		{nodeBitcoinCash, "Bitcoin Cash", "/Bitcoin Cash Node:29.2.0/", "sat/B", "1"},
	} {
		t.Run(tc.id, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				user, password, ok := r.BasicAuth()
				if !ok || user != tc.id+"-user" || password != tc.id+"-password" {
					http.Error(w, "unauthorized", http.StatusUnauthorized)
					return
				}
				var request struct {
					Method string `json:"method"`
					Params []any  `json:"params"`
				}
				if json.NewDecoder(r.Body).Decode(&request) != nil {
					t.Error("invalid request")
					return
				}
				var result any
				switch request.Method {
				case "getblockchaininfo":
					result = map[string]any{"blocks": 100, "headers": 120, "bestblockhash": "tip", "verificationprogress": .5, "initialblockdownload": true, "pruned": true, "size_on_disk": 1234}
				case "getnetworkinfo":
					result = map[string]any{"subversion": tc.version, "connections": 2}
				case "getpeerinfo":
					result = []map[string]any{{"inbound": true}, {"inbound": false}}
				case "uptime":
					result = 90
				case "getmempoolinfo":
					result = map[string]any{"size": 3, "bytes": 456}
				case "getblockheader":
					result = map[string]any{"time": time.Now().Add(-time.Minute).Unix()}
				case "estimatefee":
					if tc.id != nodeBitcoinCash || len(request.Params) != 0 {
						t.Errorf("unexpected estimatefee request: %#v", request)
						return
					}
					result = .00001
				case "estimatesmartfee":
					if tc.id == nodeBitcoinCash || len(request.Params) != 1 {
						t.Errorf("unexpected estimatesmartfee request: %#v", request)
						return
					}
					result = map[string]any{"feerate": .00001}
				default:
					t.Errorf("unexpected public RPC method %q", request.Method)
					return
				}
				writeJSON(w, map[string]any{"result": result, "error": nil})
			}))
			defer server.Close()
			dir := t.TempDir()
			node := nodeConfig{ID: tc.id, Name: tc.name, Enabled: true, Supported: true, Protocol: "bitcoin-json-rpc", RPCURL: server.URL, RPCUser: tc.id + "-user", RPCPassword: tc.id + "-password", DataDir: dir, SteadyCPUs: 1, SteadyMemLimitMB: 4096, ResourceProfile: "compact"}
			a := &app{cfg: config{stateDir: dir, nodes: map[string]nodeConfig{tc.id: node}}}
			request := httptest.NewRequest(http.MethodGet, "/api/"+tc.id, nil)
			request.SetPathValue("node", tc.id)
			response := httptest.NewRecorder()
			a.apiAdditionalNode(response, request)
			var status genericNodeStatus
			if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &status) != nil {
				t.Fatalf("status response = %d %s", response.Code, response.Body.String())
			}
			walletViewSupported := tc.id == nodeLitecoin || tc.id == nodeBitcoinCash
			if !status.Available || status.Synced || status.Height != 100 || status.TargetHeight != 120 || status.SyncPercent != 50 || !status.Pruned || !status.PrunedAvailable || status.Version != tc.version || !status.ConnectionsAvailable || status.IncomingConnections != 1 || status.OutgoingConnections != 1 || !status.MempoolBytesAvailable || !status.FeesAvailable || status.FeeUnit != tc.unit || status.FeeEstimates[0] != tc.fee || status.WalletViewSupported != walletViewSupported || status.SteadyCPUs != 1 || status.SteadyMemLimitMB != 4096 || status.ResourceProfile != "compact" {
				t.Fatalf("status = %#v", status)
			}
		})
	}
}

func TestFamilyUnavailableFeeDoesNotBecomeANegativePrice(t *testing.T) {
	for _, id := range []string{nodeLitecoin, nodeDogecoin, nodeBitcoinCash} {
		t.Run(id, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request struct {
					Method string `json:"method"`
				}
				if json.NewDecoder(r.Body).Decode(&request) != nil {
					t.Error("invalid request")
					return
				}
				var result any = map[string]any{}
				switch request.Method {
				case "getblockchaininfo":
					result = map[string]any{"blocks": 0, "headers": 0, "initialblockdownload": true, "pruned": true}
				case "getpeerinfo":
					result = []any{}
				case "uptime":
					result = 0
				case "estimatefee":
					result = -1
				case "estimatesmartfee":
					result = map[string]any{"feerate": -1}
				}
				writeJSON(w, map[string]any{"result": result, "error": nil})
			}))
			defer server.Close()
			dir := t.TempDir()
			node := nodeConfig{ID: id, Name: id, Enabled: true, Supported: true, Protocol: "bitcoin-json-rpc", RPCURL: server.URL, DataDir: dir}
			a := &app{cfg: config{stateDir: dir, nodes: map[string]nodeConfig{id: node}}}
			request := httptest.NewRequest(http.MethodGet, "/api/"+id, nil)
			request.SetPathValue("node", id)
			response := httptest.NewRecorder()
			a.apiAdditionalNode(response, request)
			var status genericNodeStatus
			if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &status) != nil || !status.Available || status.FeesAvailable || len(status.FeeEstimates) != 0 || status.FeeUnit != "" {
				t.Fatalf("unavailable fee response = %d %#v", response.Code, status)
			}
		})
	}
}

func TestZcashStatusUsesNativeShapeWithoutBitcoinIBDOrAuth(t *testing.T) {
	cookie := filepath.Join(t.TempDir(), ".cookie")
	if err := os.WriteFile(cookie, []byte("__cookie__:"+testZcashCookie), 0o600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if user, password, ok := r.BasicAuth(); !ok || user != "__cookie__" || password != testZcashCookie {
			t.Fatal("native Zakura request omitted cookie authentication")
		}
		var request struct {
			Method string `json:"method"`
		}
		if json.NewDecoder(r.Body).Decode(&request) != nil {
			t.Fatal("invalid request")
		}
		var result any
		switch request.Method {
		case "getblockchaininfo":
			result = map[string]any{"blocks": 3_500_000, "headers": 3_500_000, "estimatedheight": 3_500_000, "bestblockhash": "tip", "verificationprogress": 1.0, "pruned": true, "size_on_disk": 11 << 30}
		case "getnetworkinfo":
			result = map[string]any{"subversion": "/Zakura:1.7.0/", "connections": 2}
		case "getpeerinfo":
			result = []map[string]any{{"inbound": false}, {"inbound": false}}
		case "getmempoolinfo":
			result = map[string]any{"size": 4, "bytes": 800}
		case "getblockheader":
			result = map[string]any{"time": time.Now().Add(-time.Minute).Unix()}
		case "getstandardfee":
			result = map[string]any{"standard_fee": 400, "version": 0}
		case "getdeprecationinfo":
			result = map[string]any{}
		default:
			t.Fatalf("unexpected Zakura method %q", request.Method)
		}
		writeJSON(w, map[string]any{"jsonrpc": "2.0", "result": result, "error": nil})
	}))
	defer server.Close()
	dir := t.TempDir()
	node := nodeConfig{ID: nodeZcash, Name: "Zcash", Enabled: true, Supported: true, Protocol: "zcash-json-rpc", RPCURL: server.URL, RPCUser: "proxy-user", RPCPassword: "proxy-password", RPCCookiePath: cookie, DataDir: dir, SteadyCPUs: 1, SteadyMemLimitMB: 4096, ResourceProfile: "compact"}
	a := &app{cfg: config{stateDir: dir, nodes: map[string]nodeConfig{nodeZcash: node}}}
	request := httptest.NewRequest(http.MethodGet, "/api/zcash", nil)
	request.SetPathValue("node", nodeZcash)
	response := httptest.NewRecorder()
	a.apiAdditionalNode(response, request)
	var status genericNodeStatus
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &status) != nil || !status.Supported || !status.Available || !status.Synced || status.Height != 3_500_000 || status.TargetHeight != 3_500_000 || !status.Pruned || !status.LatestBlockAvailable || !status.FeesAvailable || status.FeeUnit != "zats/action" || len(status.FeeEstimates) != 1 || status.FeeEstimates[0] != "400" || status.WalletViewSupported {
		t.Fatalf("Zcash status = %d %#v", response.Code, status)
	}
}

func TestAdditionalNodePortControlsUseChainSpecificIdentifiers(t *testing.T) {
	t.Setenv("DASHBOARD_BIND_IP", "192.168.1.20")
	nodes := map[string]nodeConfig{
		nodeTari:        {ID: nodeTari, Name: "Tari", Enabled: true, RPCEndpoint: "192.168.1.20:18142", DefaultPort: "18142"},
		nodeLitecoin:    {ID: nodeLitecoin, Name: "Litecoin", Enabled: true, DefaultPort: "9332"},
		nodeDogecoin:    {ID: nodeDogecoin, Name: "Dogecoin", Enabled: true, DefaultPort: "22555"},
		nodeBitcoinCash: {ID: nodeBitcoinCash, Name: "Bitcoin Cash", Enabled: true, DefaultPort: "8332"},
		nodeZcash:       {ID: nodeZcash, Name: "Zcash", Enabled: true, DefaultPort: "8232"},
	}
	a := &app{cfg: config{bitcoinDisabled: true, nodes: nodes}}
	ports := a.portControlPorts()
	if len(ports) != 5 {
		t.Fatalf("ports = %#v", ports)
	}
	want := []string{"tari_grpc", "litecoin_rpc", "dogecoin_rpc", "bitcoincash_rpc", "zcash_rpc"}
	for index, id := range want {
		if ports[index].ID != id || portControlLabel(id) == "" {
			t.Fatalf("port %d = %#v", index, ports[index])
		}
	}
	if !ports[0].Enabled || ports[1].Enabled {
		t.Fatalf("exposure state = %#v", ports)
	}
}
