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

const testZakuraCap = 3_643_309

func zakuraEOSNode(t *testing.T, tip int64, deprecation func(w http.ResponseWriter)) (*app, nodeConfig) {
	t.Helper()
	cookie := filepath.Join(t.TempDir(), ".cookie")
	if err := os.WriteFile(cookie, []byte("__cookie__:"+testZcashCookie), 0o600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Method string `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&request)
		if request.Method == "getdeprecationinfo" {
			if deprecation == nil {
				http.Error(w, "unknown method", http.StatusNotFound)
				return
			}
			deprecation(w)
			return
		}
		var result any
		switch request.Method {
		case "getblockchaininfo":
			result = map[string]any{"blocks": tip, "headers": tip, "bestblockhash": "tip", "verificationprogress": 1.0}
		case "getblockheader":
			result = map[string]any{"time": time.Now().Unix()}
		default:
			result = map[string]any{}
		}
		writeJSON(w, map[string]any{"jsonrpc": "2.0", "result": result, "error": nil})
	}))
	t.Cleanup(server.Close)
	node := nodeConfig{ID: nodeZcash, Name: "Zcash", Enabled: true, Supported: true, Protocol: "zcash-json-rpc", RPCURL: server.URL, RPCCookiePath: cookie, DataDir: t.TempDir()}
	return &app{cfg: config{stateDir: t.TempDir(), nodes: map[string]nodeConfig{nodeZcash: node}}}, node
}

func zakuraCapResponse(w http.ResponseWriter) {
	writeJSON(w, map[string]any{"jsonrpc": "2.0", "result": map[string]any{"end_of_service": map[string]any{"block_height": testZakuraCap, "estimated_time": 1_796_000_000}}, "error": nil})
}

func TestZakuraEndOfSupportStatus(t *testing.T) {
	cases := []struct {
		name        string
		tip         int64
		deprecation func(http.ResponseWriter)
		height      int64
		remaining   int64
		warning     bool
	}{
		{"far from cap", testZakuraCap - zakuraEndOfSupportWarnBlocks - 1, zakuraCapResponse, testZakuraCap, zakuraEndOfSupportWarnBlocks + 1, false},
		{"inside window", testZakuraCap - 1000, zakuraCapResponse, testZakuraCap, 1000, true},
		{"past cap", testZakuraCap + 5, zakuraCapResponse, testZakuraCap, -5, true},
		{"rpc missing", testZakuraCap - 1, nil, 0, 0, false},
		{"no end of service", testZakuraCap - 1, func(w http.ResponseWriter) {
			writeJSON(w, map[string]any{"jsonrpc": "2.0", "result": map[string]any{}, "error": nil})
		}, 0, 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a, node := zakuraEOSNode(t, c.tip, c.deprecation)
			status, err := a.fetchZcashStatus(t.Context(), node, genericNodeStatus{})
			if err != nil || status.EndOfSupportHeight != c.height || status.EndOfSupportRemaining != c.remaining || status.EndOfSupportWarning != c.warning {
				t.Fatalf("status = %#v, %v", status, err)
			}
		})
	}
}

func TestZakuraEndOfSupportEventIsRecordedOncePerCrossing(t *testing.T) {
	var messages []string
	m := configuredMonitor(t, func(r *http.Request) (*http.Response, error) {
		var payload struct {
			Text string `json:"text"`
		}
		_ = json.NewDecoder(r.Body).Decode(&payload)
		messages = append(messages, payload.Text)
		return response(http.StatusOK, `{"ok":true}`), nil
	})
	m.configureNodes(false, false, "")
	m.configureAdditionalNodes(map[string]nodeConfig{nodeZcash: {ID: nodeZcash, Enabled: true, Supported: true, DataDir: t.TempDir()}})
	m.healthTicks = 1
	far := map[string]map[string]any{nodeZcash: {"active": true, "initialblockdownload": false, "blocks": float64(3_000_000)}}
	near := map[string]map[string]any{nodeZcash: {"active": true, "initialblockdownload": false, "blocks": float64(testZakuraCap - 100),
		"end_of_support_height": int64(testZakuraCap), "end_of_support_remaining_blocks": int64(100)}}
	down := map[string]map[string]any{nodeZcash: nil}
	count := func() int {
		n := 0
		for _, message := range messages {
			if strings.Contains(message, "Zakura stops at block 3643309") {
				n++
			}
		}
		return n
	}
	m.tickConfiguredNodes(t.Context(), far)
	if count() != 0 {
		t.Fatalf("far from cap alerted: %q", messages)
	}
	for _, infos := range []map[string]map[string]any{near, near, down, near} {
		m.tickConfiguredNodes(t.Context(), infos)
	}
	if count() != 1 {
		t.Fatalf("inside window alerts = %d: %q", count(), messages)
	}
	m.tickConfiguredNodes(t.Context(), far)
	m.tickConfiguredNodes(t.Context(), near)
	if count() != 2 {
		t.Fatalf("second crossing alerts = %d: %q", count(), messages)
	}
}

func TestZakuraEndOfSupportNodeDownReturnsNoStatus(t *testing.T) {
	a, node := zakuraEOSNode(t, testZakuraCap+5, zakuraCapResponse)
	node.RPCURL = "http://127.0.0.1:1"
	if status, err := a.fetchZcashStatus(t.Context(), node, genericNodeStatus{}); err == nil || status.EndOfSupportWarning {
		t.Fatalf("down node status = %#v, %v", status, err)
	}
}
