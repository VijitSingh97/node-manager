package main

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"io"
	"net/http"
	"time"
)

const maxZcashProxyRequest = 64 << 10

var zcashReadOnlyMethods = map[string]bool{
	"decoderawtransaction": true,
	"decodescript":         true,
	"getaddressbalance":    true,
	"getaddressmempool":    true,
	"getaddresstxids":      true,
	"getaddressutxos":      true,
	"getbestblockhash":     true,
	"getblock":             true,
	"getblockchaininfo":    true,
	"getblockcount":        true,
	"getblockhash":         true,
	"getblockheader":       true,
	"getchaintips":         true,
	"getdifficulty":        true,
	"getinfo":              true,
	"getmempoolinfo":       true,
	"getnetworkinfo":       true,
	"getpeerinfo":          true,
	"getrawmempool":        true,
	"getrawtransaction":    true,
	"getstandardfee":       true,
	"gettxout":             true,
	"validateaddress":      true,
	"z_getsubtreesbyindex": true,
	"z_gettreestate":       true,
	"z_validateaddress":    true,
}

type zcashProxyRequest struct {
	JSONRPC string          `json:"jsonrpc,omitempty"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

func validBasicAuth(r *http.Request, wantUser, wantPassword string) bool {
	user, password, ok := r.BasicAuth()
	return ok && len(user) == len(wantUser) && len(password) == len(wantPassword) && subtle.ConstantTimeCompare([]byte(user), []byte(wantUser)) == 1 && subtle.ConstantTimeCompare([]byte(password), []byte(wantPassword)) == 1
}

func decodeZcashProxyRequest(body []byte) (zcashProxyRequest, bool) {
	var request zcashProxyRequest
	if !decodeStrictBytes(body, &request) || request.Method == "" || !zcashReadOnlyMethods[request.Method] {
		return request, false
	}
	if len(request.Params) > 0 {
		var params any
		if json.Unmarshal(request.Params, &params) != nil {
			return request, false
		}
		switch params.(type) {
		case []any, map[string]any, nil:
		default:
			return request, false
		}
	}
	return request, true
}

func (a *app) zcashRPCProxy() http.Handler {
	node := a.cfg.nodes[nodeZcash]
	client := newInternalHTTPClient(10 * time.Second)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.Method != http.MethodPost || r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		if node.RPCUser == "" || node.RPCPassword == "" || !validBasicAuth(r, node.RPCUser, node.RPCPassword) {
			w.Header().Set("WWW-Authenticate", `Basic realm="Zcash read-only RPC"`)
			http.Error(w, "authentication required", http.StatusUnauthorized)
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxZcashProxyRequest))
		if err != nil {
			http.Error(w, "invalid RPC request", http.StatusBadRequest)
			return
		}
		if _, ok := decodeZcashProxyRequest(body); !ok {
			http.Error(w, "RPC method is unavailable", http.StatusForbidden)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		upstream, err := http.NewRequestWithContext(ctx, http.MethodPost, node.RPCURL, bytes.NewReader(body))
		if err != nil {
			http.Error(w, "Zcash RPC unavailable", http.StatusBadGateway)
			return
		}
		upstream.Header.Set("Content-Type", "application/json")
		cookieUser, cookiePassword, err := readRPCCookie(node.RPCCookiePath)
		if err != nil {
			http.Error(w, "Zcash RPC unavailable", http.StatusBadGateway)
			return
		}
		upstream.SetBasicAuth(cookieUser, cookiePassword)
		response, err := client.Do(upstream)
		if err != nil {
			http.Error(w, "Zcash RPC unavailable", http.StatusBadGateway)
			return
		}
		defer response.Body.Close()
		responseBody, err := io.ReadAll(io.LimitReader(response.Body, maxRPCBody+1))
		if err != nil || len(responseBody) > maxRPCBody || !json.Valid(responseBody) {
			http.Error(w, "invalid Zcash RPC response", http.StatusBadGateway)
			return
		}
		if response.StatusCode != http.StatusOK {
			http.Error(w, "Zcash RPC unavailable", http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(responseBody)
	})
}
