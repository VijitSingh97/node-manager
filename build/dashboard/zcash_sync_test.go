package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestZcashSyncGatesShareTipRule(t *testing.T) {
	const tip = 3_500_000
	for _, tc := range []struct {
		name               string
		headers, estimated int64
		tipAge             time.Duration
		want               bool
	}{
		{"estimate two blocks ahead", tip, tip + 2, 3 * time.Minute, true},
		{"headers ahead", tip + 5, tip, time.Minute, false},
		{"progress below threshold", tip, tip + 10, time.Minute, false},
		{"stale tip as Zakura reports it", tip, tip + 144, 3 * time.Hour, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tipTime := time.Now().Add(-tc.tipAge).Unix()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request struct {
					Method string `json:"method"`
				}
				_ = json.NewDecoder(r.Body).Decode(&request)
				var result any
				switch request.Method {
				case "getblockchaininfo":
					result = map[string]any{"blocks": tip, "headers": tc.headers, "estimatedheight": tc.estimated, "bestblockhash": "tip", "verificationprogress": float64(tip) / float64(tc.estimated)}
				case "getblockheader":
					result = map[string]any{"time": tipTime}
				case "getstandardfee":
					result = map[string]any{"standard_fee": 400}
				}
				writeJSON(w, map[string]any{"result": result, "error": nil})
			}))
			defer server.Close()
			dir := t.TempDir()
			node := nodeConfig{ID: nodeZcash, Enabled: true, Supported: true, Protocol: "zcash-json-rpc", RPCURL: server.URL, DataDir: dir}
			a := &app{cfg: config{stateDir: dir, nodes: map[string]nodeConfig{nodeZcash: node}}, additionalFees: map[string][]float64{}}

			status, err := a.fetchZcashStatus(t.Context(), node, genericNodeStatus{})
			if err != nil || status.Synced != tc.want || tc.want && (status.TargetHeight != tip || status.SyncPercent != 100) {
				t.Fatalf("status err=%v %#v", err, status)
			}
			store := &familyWatchStore{rpc: &rpcClient{url: server.URL, client: newInternalHTTPClient(5 * time.Second)}}
			if _, ready := zcashFamilyWatchBackend().ready(t.Context(), store, time.Now().Unix()); ready != tc.want {
				t.Fatalf("watch ready = %v", ready)
			}
			a.sampleAdditionalFee(t.Context(), nodeZcash, node)
			if recorded := len(a.additionalFees[nodeZcash]) == 1; recorded != tc.want {
				t.Fatalf("fee samples = %v", a.additionalFees[nodeZcash])
			}
		})
	}
}
