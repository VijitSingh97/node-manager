package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

const maxRPCBody = 4 << 20

type rpcClient struct {
	url        string
	user       string
	password   string
	cookiePath string
	client     *http.Client
	maxBody    int64
}

type rpcResponse struct {
	Result json.RawMessage `json:"result"`
	Error  json.RawMessage `json:"error"`
}

func (r *rpcClient) call(ctx context.Context, method string, params any, out any) error {
	if params == nil {
		params = []any{}
	}
	body, err := json.Marshal(map[string]any{"jsonrpc": "1.0", "id": "dashboard", "method": method, "params": params})
	if err != nil {
		return err
	}
	responseBody, err := r.post(ctx, body, method)
	if err != nil {
		return err
	}
	var envelope rpcResponse
	if err := json.Unmarshal(responseBody, &envelope); err != nil {
		return fmt.Errorf("decode RPC %s: %w", method, err)
	}
	if len(envelope.Error) > 0 && string(envelope.Error) != "null" {
		return fmt.Errorf("RPC %s returned an error", method)
	}
	if len(envelope.Result) == 0 || string(envelope.Result) == "null" {
		return fmt.Errorf("RPC %s returned no result", method)
	}
	if err := json.Unmarshal(envelope.Result, out); err != nil {
		return fmt.Errorf("decode RPC %s result: %w", method, err)
	}
	return nil
}

type rpcBatchCall struct {
	Method string
	Params any
}

// callBatch sends one JSON-RPC batch; a failed item has a non-empty Error, the batch itself fails only on transport or framing errors.
func (r *rpcClient) callBatch(ctx context.Context, calls []rpcBatchCall) ([]rpcResponse, error) {
	if len(calls) == 0 {
		return nil, nil
	}
	items := make([]map[string]any, len(calls))
	for i, c := range calls {
		params := c.Params
		if params == nil {
			params = []any{}
		}
		items[i] = map[string]any{"jsonrpc": "1.0", "id": i, "method": c.Method, "params": params}
	}
	body, err := json.Marshal(items)
	if err != nil {
		return nil, err
	}
	responseBody, err := r.post(ctx, body, "batch")
	if err != nil {
		return nil, err
	}
	var envelopes []struct {
		rpcResponse
		ID *int `json:"id"`
	}
	if err := json.Unmarshal(responseBody, &envelopes); err != nil {
		return nil, fmt.Errorf("decode RPC batch: %w", err)
	}
	out := make([]rpcResponse, len(calls))
	seen := make([]bool, len(calls))
	for _, e := range envelopes {
		if e.ID == nil || *e.ID < 0 || *e.ID >= len(calls) || seen[*e.ID] {
			return nil, errors.New("RPC batch has an unexpected response id")
		}
		seen[*e.ID] = true
		out[*e.ID] = e.rpcResponse
	}
	for _, ok := range seen {
		if !ok {
			return nil, errors.New("RPC batch response is incomplete")
		}
	}
	return out, nil
}

func (r *rpcClient) post(ctx context.Context, body []byte, label string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	user, password := r.user, r.password
	if r.cookiePath != "" {
		user, password, err = readRPCCookie(r.cookiePath)
		if err != nil {
			return nil, fmt.Errorf("read RPC cookie: %w", err)
		}
	}
	if user != "" || password != "" {
		req.SetBasicAuth(user, password)
	}
	req.Header.Set("Content-Type", "application/json")
	client := *r.client
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("RPC %s returned HTTP %d", label, resp.StatusCode)
	}
	limit := r.maxBody
	if limit <= 0 {
		limit = maxRPCBody
	}
	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read RPC %s: %w", label, err)
	}
	if int64(len(responseBody)) > limit {
		return nil, fmt.Errorf("RPC %s response exceeds %d bytes", label, limit)
	}
	return responseBody, nil
}

func readRPCCookie(path string) (string, string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", "", err
	}
	if !info.Mode().IsRegular() {
		return "", "", fmt.Errorf("cookie is not a regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", "", err
	}
	defer file.Close()
	contents, err := io.ReadAll(io.LimitReader(file, 1025))
	if err != nil {
		return "", "", err
	}
	if len(contents) > 1024 {
		return "", "", fmt.Errorf("cookie is too large")
	}
	user, password, ok := strings.Cut(strings.TrimSpace(string(contents)), ":")
	decoded, decodeErr := base64.StdEncoding.DecodeString(password)
	if !ok || user != "__cookie__" || len(decoded) != 32 || decodeErr != nil || strings.ContainsAny(password, "\r\n") {
		return "", "", fmt.Errorf("cookie is malformed")
	}
	return user, password, nil
}
