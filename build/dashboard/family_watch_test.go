package main

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type familyWatchRPCFixture struct {
	mu       sync.Mutex
	user     string
	password string
	methods  []string
	ibd      bool
	failScan bool
	tipTime  int64
}

func (f *familyWatchRPCFixture) handler(w http.ResponseWriter, r *http.Request) {
	user, password, ok := r.BasicAuth()
	if !ok || user != f.user || password != f.password {
		http.Error(w, "bad auth", http.StatusUnauthorized)
		return
	}
	var request struct {
		Method string `json:"method"`
		Params []any  `json:"params"`
	}
	if json.NewDecoder(r.Body).Decode(&request) != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	f.methods = append(f.methods, request.Method)
	ibd, failScan, tipTime := f.ibd, f.failScan, f.tipTime
	f.mu.Unlock()
	var result any
	switch request.Method {
	case "validateaddress":
		address, _ := request.Params[0].(string)
		if strings.HasPrefix(address, "private-") {
			result = map[string]any{"isvalid": false}
		} else if strings.HasPrefix(address, "mine-") {
			result = map[string]any{"isvalid": true, "address": address, "scriptPubKey": "76a914", "ismine": true}
		} else if strings.HasPrefix(address, "mweb-") {
			result = map[string]any{"isvalid": true, "address": address, "scriptPubKey": "51", "ismweb": true}
		} else if strings.HasPrefix(address, "empty-script-") {
			result = map[string]any{"isvalid": true, "address": address, "scriptPubKey": "", "ismweb": false}
		} else {
			result = map[string]any{"isvalid": true, "address": "canonical-" + address, "scriptPubKey": "76a914", "ismweb": false}
		}
	case "getblockchaininfo":
		result = map[string]any{"blocks": 200, "headers": 200, "bestblockhash": "tip", "initialblockdownload": ibd}
	case "getblockheader":
		if len(request.Params) != 1 || request.Params[0] != "tip" {
			http.Error(w, "bad getblockheader params", http.StatusBadRequest)
			return
		}
		if tipTime == 0 {
			result = map[string]any{"time": time.Now().Unix()}
		} else {
			result = map[string]any{"time": tipTime}
		}
	case "getblockhash":
		if len(request.Params) != 1 || request.Params[0] != float64(200) {
			http.Error(w, "bad getblockhash params", http.StatusBadRequest)
			return
		}
		result = "tip"
	case "scantxoutset":
		if len(request.Params) != 2 || request.Params[0] != "start" {
			http.Error(w, "bad scantxoutset params", http.StatusBadRequest)
			return
		}
		if failScan {
			_ = json.NewEncoder(w).Encode(map[string]any{"result": nil, "error": map[string]any{"code": -8, "message": "scan in progress"}})
			return
		}
		descriptors, _ := request.Params[1].([]any)
		amount := json.RawMessage("0.00000000")
		if len(descriptors) == 1 && strings.Contains(descriptors[0].(string), "canonical-") {
			amount = json.RawMessage("1.23456789")
		}
		result = map[string]any{"success": true, "total_amount": amount, "height": 200, "bestblock": "tip"}
	default:
		http.Error(w, "unexpected method", http.StatusBadRequest)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"result": result, "error": nil})
}

func (f *familyWatchRPCFixture) setIBD(value bool) {
	f.mu.Lock()
	f.ibd = value
	f.mu.Unlock()
}

func (f *familyWatchRPCFixture) setFailScan(value bool) {
	f.mu.Lock()
	f.failScan = value
	f.mu.Unlock()
}

func (f *familyWatchRPCFixture) setTipTime(value int64) {
	f.mu.Lock()
	f.tipTime = value
	f.mu.Unlock()
}

func (f *familyWatchRPCFixture) calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.methods...)
}

func newFamilyWatchTestApp(t *testing.T, password string) (*app, http.Handler, map[string]*familyWatchRPCFixture, string) {
	t.Helper()
	if password == "" {
		password = "dashboard-secret"
	}
	stateDir := t.TempDir()
	nodes := make(map[string]nodeConfig)
	fixtures := make(map[string]*familyWatchRPCFixture)
	for _, id := range []string{nodeLitecoin, nodeBitcoinCash} {
		fixture := &familyWatchRPCFixture{user: id + "-user", password: id + "-password", tipTime: time.Now().Add(-time.Minute).Unix()}
		server := httptest.NewServer(http.HandlerFunc(fixture.handler))
		t.Cleanup(server.Close)
		fixtures[id] = fixture
		nodes[id] = nodeConfig{ID: id, Name: map[string]string{nodeLitecoin: "Litecoin", nodeBitcoinCash: "Bitcoin Cash"}[id], Enabled: true, Active: true, ActiveSet: true, Supported: true, Protocol: "bitcoin-json-rpc", RPCURL: server.URL, RPCUser: fixture.user, RPCPassword: fixture.password}
	}
	a := &app{cfg: config{stateDir: stateDir, dashboardPassword: password, nodes: nodes}, log: log.New(io.Discard, "", 0), nodeHealth: make(map[string]nodeHealthSnapshot)}
	initializeTestDashboardAuth(t, a)
	a.familyWatch = newFamilyWatchManager(a)
	return a, a.routes(), fixtures, stateDir
}

func familyWatchRequest(t *testing.T, handler http.Handler, method, path, password string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(data)
	}
	request := httptest.NewRequest(method, path, reader)
	request.Header.Set("X-Requested-With", "fetch")
	if password != "" {
		request.SetBasicAuth("admin", password)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func TestFamilyWatchHTTPValidatesWithNativeRPCAndReturnsExactBalances(t *testing.T) {
	a, handler, fixtures, _ := newFamilyWatchTestApp(t, "secret")
	for _, id := range []string{nodeLitecoin, nodeBitcoinCash} {
		response := familyWatchRequest(t, handler, http.MethodPost, "/api/"+id+"/watch", "secret", map[string]any{"label": "Personal " + id, "address": id + "-address"})
		if response.Code != http.StatusOK {
			t.Fatalf("%s add = %d %s", id, response.Code, response.Body.String())
		}
		a.familyWatch.scanNode(t.Context(), id, a.familyWatch.stores[id])
		response = familyWatchRequest(t, handler, http.MethodGet, "/api/"+id+"/watch", "secret", nil)
		var view struct {
			Supported   bool                    `json:"supported"`
			Enabled     bool                    `json:"enabled"`
			Active      bool                    `json:"active"`
			HasPassword bool                    `json:"has_password"`
			Wallets     []familyWatchWalletView `json:"wallets"`
		}
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &view) != nil || !view.Supported || !view.Enabled || !view.Active || !view.HasPassword || len(view.Wallets) != 2 {
			t.Fatalf("%s list = %d %s", id, response.Code, response.Body.String())
		}
		if view.Wallets[0].Amount == nil || *view.Wallets[0].Amount != "0.00000000" || view.Wallets[0].State != "ok" {
			t.Fatalf("%s zero balance = %#v", id, view.Wallets[0])
		}
		if view.Wallets[1].Address != "canonical-"+id+"-address" || view.Wallets[1].Amount == nil || *view.Wallets[1].Amount != "1.23456789" || view.Wallets[1].ScanHeight != 200 || len(view.Wallets[1].History) != 1 {
			t.Fatalf("%s personal balance = %#v", id, view.Wallets[1])
		}
		for _, method := range fixtures[id].calls() {
			switch method {
			case "validateaddress", "getblockchaininfo", "getblockheader", "getblockhash", "scantxoutset":
			default:
				t.Fatalf("%s unsafe RPC method %q", id, method)
			}
		}
	}
}

func TestFamilyWatchRequiresAuthAndRejectsPrivateInput(t *testing.T) {
	a, handler, _, _ := newFamilyWatchTestApp(t, "secret")
	if response := familyWatchRequest(t, handler, http.MethodPost, "/api/litecoin/watch", "", map[string]any{"label": "Private", "address": "private-key"}); response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated add = %d", response.Code)
	}
	if response := familyWatchRequest(t, handler, http.MethodPost, "/api/litecoin/watch", "secret", map[string]any{"label": "Private", "address": "private-key"}); response.Code != http.StatusBadRequest {
		t.Fatalf("private input = %d %s", response.Code, response.Body.String())
	}
	if response := familyWatchRequest(t, handler, http.MethodPost, "/api/litecoin/watch", "secret", map[string]any{"label": "Owned address", "address": "mine-address"}); response.Code != http.StatusOK {
		t.Fatalf("owned public Litecoin address = %d %s", response.Code, response.Body.String())
	}
	for _, address := range []string{"mweb-address", "empty-script-address"} {
		if response := familyWatchRequest(t, handler, http.MethodPost, "/api/litecoin/watch", "secret", map[string]any{"label": address, "address": address}); response.Code != http.StatusBadRequest {
			t.Fatalf("unsafe Litecoin validation %q = %d %s", address, response.Code, response.Body.String())
		}
	}
	request := httptest.NewRequest(http.MethodPost, "/api/litecoin/watch", strings.NewReader(`{"label":"No CSRF","address":"address"}`))
	request.SetBasicAuth("admin", "secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("missing same-origin header = %d %s", response.Code, response.Body.String())
	}

	if response := familyWatchRequest(t, handler, http.MethodGet, "/api/litecoin/watch", "", nil); response.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous public example = %d %s", response.Code, response.Body.String())
	}
	store := a.familyWatch.stores[nodeLitecoin]
	if _, err := store.add("Personal", "canonical-personal"); err != nil {
		t.Fatal(err)
	}
	if response := familyWatchRequest(t, handler, http.MethodGet, "/api/litecoin/watch", "", nil); response.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous personal roster = %d %s", response.Code, response.Body.String())
	}
}

func TestFamilyWatchSyncWaitingAndFailedScanPreserveStaleBalance(t *testing.T) {
	a, _, fixtures, _ := newFamilyWatchTestApp(t, "secret")
	store := a.familyWatch.stores[nodeLitecoin]
	fixedNow := time.Unix(1_800_000_000, 0)
	store.now = func() time.Time { return fixedNow }
	fixtures[nodeLitecoin].setIBD(true)
	a.familyWatch.scanNode(t.Context(), nodeLitecoin, store)
	wallets, err := store.list(false)
	if err != nil || wallets[0].State != "waiting" || wallets[0].Amount != nil {
		t.Fatalf("sync waiting = %#v, %v", wallets, err)
	}
	fixtures[nodeLitecoin].setIBD(false)
	for _, tipTime := range []int64{fixedNow.Add(-121 * time.Minute).Unix(), fixedNow.Add(time.Second).Unix()} {
		fixtures[nodeLitecoin].setTipTime(tipTime)
		a.familyWatch.scanNode(t.Context(), nodeLitecoin, store)
		wallets, _ = store.list(false)
		if wallets[0].State != "waiting" || wallets[0].Amount != nil {
			t.Fatalf("unfresh tip %d = %#v", tipTime, wallets[0])
		}
	}
	fixtures[nodeLitecoin].setTipTime(fixedNow.Unix())
	a.familyWatch.scanNode(t.Context(), nodeLitecoin, store)
	wallets, _ = store.list(false)
	if wallets[0].State != "ok" || wallets[0].Amount == nil || *wallets[0].Amount != "0.00000000" {
		t.Fatalf("successful zero = %#v", wallets[0])
	}
	restarted := &app{cfg: a.cfg}
	restarted.familyWatch = newFamilyWatchManager(restarted)
	restartedWallets, err := restarted.familyWatch.stores[nodeLitecoin].list(false)
	if err != nil || restartedWallets[0].State != "stale" || restartedWallets[0].Amount == nil || *restartedWallets[0].Amount != "0.00000000" {
		t.Fatalf("restarted stale cache = %#v, %v", restartedWallets, err)
	}
	fixtures[nodeLitecoin].setFailScan(true)
	fixedNow = fixedNow.Add(familyWatchInterval)
	a.familyWatch.scanNode(t.Context(), nodeLitecoin, store)
	wallets, _ = store.list(false)
	if wallets[0].State != "stale" || wallets[0].Amount == nil || *wallets[0].Amount != "0.00000000" || len(wallets[0].History) != 1 {
		t.Fatalf("failed stale cache = %#v", wallets[0])
	}
	for i := range 100 {
		store.record(wallets[0].ID, "0", int64(201+i), "tip")
	}
	wallets, _ = store.list(false)
	if len(wallets[0].History) != maxFamilyWatchHistory {
		t.Fatalf("history length = %d", len(wallets[0].History))
	}
}
