package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
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
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	user, password := r.user, r.password
	if r.cookiePath != "" {
		user, password, err = readRPCCookie(r.cookiePath)
		if err != nil {
			return fmt.Errorf("read RPC cookie: %w", err)
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
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("RPC %s returned HTTP %d", method, resp.StatusCode)
	}
	limit := r.maxBody
	if limit <= 0 {
		limit = maxRPCBody
	}
	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return fmt.Errorf("read RPC %s: %w", method, err)
	}
	if int64(len(responseBody)) > limit {
		return fmt.Errorf("RPC %s response exceeds %d bytes", method, limit)
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
