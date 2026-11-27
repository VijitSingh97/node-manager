package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestFamilyFeeHistoryUsesNativeUnitsAndSkipsUnavailable(t *testing.T) {
	for _, tc := range []struct {
		id   string
		want float64
	}{
		{nodeLitecoin, 1},
		{nodeDogecoin, .00001},
		{nodeBitcoinCash, 1},
	} {
		t.Run(tc.id, func(t *testing.T) {
			var rate atomic.Int64
			var syncing, stale atomic.Bool
			rate.Store(1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request struct {
					Method string `json:"method"`
					Params []any  `json:"params"`
				}
				if json.NewDecoder(r.Body).Decode(&request) != nil {
					t.Error("invalid RPC request")
					return
				}
				var result any
				switch request.Method {
				case "getblockchaininfo":
					result = map[string]any{"blocks": 100, "headers": 100, "bestblockhash": map[bool]string{false: "tip", true: "old-tip"}[stale.Load()], "initialblockdownload": syncing.Load()}
				case "getblockheader":
					age := time.Minute
					if stale.Load() {
						age = 3 * time.Hour
					}
					result = map[string]any{"time": time.Now().Add(-age).Unix()}
				case "estimatefee":
					if tc.id != nodeBitcoinCash || len(request.Params) != 0 {
						t.Errorf("wrong native fee method or parameters: %#v", request)
					}
					result = float64(rate.Load()) / 100000
				case "estimatesmartfee":
					if tc.id == nodeBitcoinCash || len(request.Params) != 1 || request.Params[0] != float64(1) {
						t.Errorf("wrong native fee method or parameters: %#v", request)
					}
					result = map[string]any{"feerate": float64(rate.Load()) / 100000}
				default:
					t.Errorf("unexpected RPC %q", request.Method)
				}
				writeJSON(w, map[string]any{"result": result, "error": nil})
			}))
			defer server.Close()
			node := nodeConfig{ID: tc.id, Enabled: true, Protocol: "bitcoin-json-rpc", RPCURL: server.URL}
			a := &app{cfg: config{nodes: map[string]nodeConfig{tc.id: node}}, additionalFees: map[string][]float64{}}
			a.sampleAdditionalFee(context.Background(), tc.id, node)
			rate.Store(-100000)
			a.sampleAdditionalFee(context.Background(), tc.id, node)
			rate.Store(0)
			a.sampleAdditionalFee(context.Background(), tc.id, node)
			syncing.Store(true)
			rate.Store(10)
			a.sampleAdditionalFee(context.Background(), tc.id, node)
			syncing.Store(false)
			stale.Store(true)
			a.sampleAdditionalFee(context.Background(), tc.id, node)
			request := httptest.NewRequest(http.MethodGet, "/api/"+tc.id+"/fees", nil)
			request.SetPathValue("node", tc.id)
			response := httptest.NewRecorder()
			a.apiAdditionalFees(response, request)
			var history struct {
				Fee []float64 `json:"fee"`
			}
			if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &history) != nil || len(history.Fee) != 2 || history.Fee[0] != tc.want || history.Fee[1] != 0 {
				t.Fatalf("fee history = %d %s", response.Code, response.Body.String())
			}
		})
	}
}
