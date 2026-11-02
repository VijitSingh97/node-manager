package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

const nativeWalletDashboardPassword = "native-wallet-dashboard"

type nativeWalletList struct {
	Wallets []familyWatchWalletView `json:"wallets"`
}

type nativeWalletRoundTripFunc func(*http.Request) (*http.Response, error)

func (f nativeWalletRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestNativeFamilyWatchHTTP(t *testing.T) {
	if os.Getenv("NATIVE_WALLET_INTEGRATION") != "1" {
		t.Skip("set NATIVE_WALLET_INTEGRATION=1 to run against a real regtest node")
	}
	chain := os.Getenv("NATIVE_WALLET_CHAIN")
	if chain != nodeLitecoin && chain != nodeBitcoinCash {
		t.Fatal("NATIVE_WALLET_CHAIN must be litecoin or bitcoincash")
	}
	rpcURL := os.Getenv("NATIVE_WALLET_RPC_URL")
	rpcUser := os.Getenv("NATIVE_WALLET_RPC_USER")
	rpcPassword := os.Getenv("NATIVE_WALLET_RPC_PASSWORD")
	addresses := []string{os.Getenv("NATIVE_WALLET_FUNDED_ADDRESS"), os.Getenv("NATIVE_WALLET_ZERO_ADDRESS"), os.Getenv("NATIVE_WALLET_ERROR_ADDRESS")}
	if rpcURL == "" || rpcUser == "" || rpcPassword == "" {
		t.Fatal("native wallet RPC URL and credentials are required")
	}
	for _, address := range addresses {
		if !validFamilyWatchAddress(address) {
			t.Fatal("three public fixture addresses are required")
		}
	}

	stateDir := t.TempDir()
	a, handler := newNativeWalletApp(t, stateDir, chain, rpcURL, rpcUser, rpcPassword)
	initial := nativeWalletGet(t, handler, chain)
	if len(initial.Wallets) != 1 || !initial.Wallets[0].Example {
		t.Fatalf("initial roster = %#v", initial.Wallets)
	}
	nativeWalletRequest(t, handler, http.MethodDelete, "/api/"+chain+"/watch/"+initial.Wallets[0].ID, nil, http.StatusOK)

	a, handler = newNativeWalletApp(t, stateDir, chain, rpcURL, rpcUser, rpcPassword)
	if wallets := nativeWalletGet(t, handler, chain).Wallets; len(wallets) != 0 {
		t.Fatalf("seed deletion did not persist: %#v", wallets)
	}

	for index, fixture := range []struct {
		label   string
		address string
	}{{"Funded fixture", addresses[0]}, {"Zero fixture", addresses[1]}} {
		nativeWalletRequest(t, handler, http.MethodPost, "/api/"+chain+"/watch", map[string]string{"label": fixture.label, "address": fixture.address}, http.StatusOK)
		wallets := nativeWalletGet(t, handler, chain).Wallets
		if len(wallets) != index+1 || wallets[index].State != "pending" || wallets[index].Amount != nil {
			t.Fatalf("pending wallet %d = %#v", index, wallets)
		}
	}

	store := a.familyWatch.stores[chain]
	a.familyWatch.scanNode(t.Context(), chain, store)
	assertNativeWallet(t, nativeWalletGet(t, handler, chain).Wallets, "Funded fixture", "ok", "5050.00000000", 101)
	assertNativeWallet(t, nativeWalletGet(t, handler, chain).Wallets, "Zero fixture", "ok", "0.00000000", 101)

	nativeWalletRequest(t, handler, http.MethodPost, "/api/"+chain+"/watch", map[string]string{"label": "Error fixture", "address": addresses[2]}, http.StatusOK)
	assertNativeWallet(t, nativeWalletGet(t, handler, chain).Wallets, "Error fixture", "pending", "", 0)
	base := store.rpc.client.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	store.rpc.client.Transport = nativeWalletRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			return nil, err
		}
		request.Body = io.NopCloser(bytes.NewReader(body))
		var call struct {
			Method string `json:"method"`
		}
		if json.Unmarshal(body, &call) == nil && call.Method == "scantxoutset" {
			return nil, errors.New("injected scan transport failure")
		}
		return base.RoundTrip(request)
	})
	a.familyWatch.scanNode(t.Context(), chain, store)
	wallets := nativeWalletGet(t, handler, chain).Wallets
	assertNativeWallet(t, wallets, "Funded fixture", "ok", "5050.00000000", 101)
	assertNativeWallet(t, wallets, "Zero fixture", "ok", "0.00000000", 101)
	assertNativeWallet(t, wallets, "Error fixture", "error", "", 0)
}

func newNativeWalletApp(t *testing.T, stateDir, chain, rpcURL, rpcUser, rpcPassword string) (*app, http.Handler) {
	t.Helper()
	node := nodeConfig{ID: chain, Name: chain, Enabled: true, Active: true, ActiveSet: true, Supported: true, Protocol: "bitcoin-json-rpc", RPCURL: rpcURL, RPCUser: rpcUser, RPCPassword: rpcPassword}
	a := &app{cfg: config{stateDir: stateDir, dashboardPassword: nativeWalletDashboardPassword, nodes: map[string]nodeConfig{chain: node}}, log: log.New(io.Discard, "", 0), nodeHealth: make(map[string]nodeHealthSnapshot)}
	initializeTestDashboardAuth(t, a)
	a.familyWatch = newFamilyWatchManager(a)
	return a, a.routes()
}

func nativeWalletRequest(t *testing.T, handler http.Handler, method, path string, body any, wantStatus int) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(encoded)
	}
	request := httptest.NewRequest(method, path, reader)
	request.SetBasicAuth("admin", nativeWalletDashboardPassword)
	request.Header.Set("X-Requested-With", "fetch")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != wantStatus {
		t.Fatalf("%s %s = %d %s", method, path, response.Code, strings.TrimSpace(response.Body.String()))
	}
	return response
}

func nativeWalletGet(t *testing.T, handler http.Handler, chain string) nativeWalletList {
	t.Helper()
	response := nativeWalletRequest(t, handler, http.MethodGet, "/api/"+chain+"/watch", nil, http.StatusOK)
	var result nativeWalletList
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func assertNativeWallet(t *testing.T, wallets []familyWatchWalletView, label, state, amount string, height int64) {
	t.Helper()
	for _, wallet := range wallets {
		if wallet.Label != label {
			continue
		}
		if wallet.State != state || wallet.ScanHeight != height {
			t.Fatalf("%s state = %#v", label, wallet)
		}
		if amount == "" && wallet.Amount != nil || amount != "" && (wallet.Amount == nil || *wallet.Amount != amount) {
			t.Fatalf("%s amount = %#v", label, wallet.Amount)
		}
		return
	}
	t.Fatalf("missing %s in %#v", label, wallets)
}
