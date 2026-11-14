package main

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

const zcashTestAddress = "t1Hsc1LR8yKnbbe3twRp88p6vFfC5t7DLbs"

type zcashWatchFixture struct {
	mu         sync.Mutex
	balance    uint64
	balanceErr bool
	tipTime    int64
	progress   float64
	methods    []string
	params     []any
	auth       string
}

func (f *zcashWatchFixture) handler(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Method string `json:"method"`
		Params []any  `json:"params"`
	}
	_ = json.NewDecoder(r.Body).Decode(&request)
	user, password, _ := r.BasicAuth()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.auth = user + ":" + password
	f.methods = append(f.methods, request.Method)
	var result any
	switch request.Method {
	case "getblockchaininfo":
		result = map[string]any{"blocks": 500, "headers": 500, "estimatedheight": 500, "bestblockhash": "abc", "verificationprogress": f.progress}
	case "getblockheader":
		result = map[string]any{"time": f.tipTime}
	case "validateaddress":
		address, _ := request.Params[0].(string)
		result = map[string]any{"isvalid": address == zcashTestAddress, "address": address}
	case "getaddressbalance":
		f.params = request.Params
		if f.balanceErr {
			_ = json.NewEncoder(w).Encode(map[string]any{"result": nil, "error": map[string]any{"code": -1, "message": "transparent indexes unavailable"}})
			return
		}
		result = map[string]any{"balance": f.balance, "received": f.balance + 5}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"result": result, "error": nil})
}

func newZcashWatchTest(t *testing.T, balance uint64) (*familyWatchManager, *familyWatchStore, *zcashWatchFixture) {
	t.Helper()
	fixture := &zcashWatchFixture{balance: balance, tipTime: time.Now().Add(-time.Minute).Unix(), progress: 1}
	server := httptest.NewServer(http.HandlerFunc(fixture.handler))
	t.Cleanup(server.Close)
	dir := t.TempDir()
	cookie := filepath.Join(dir, ".cookie")
	if err := os.WriteFile(cookie, []byte("__cookie__:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="), 0o600); err != nil {
		t.Fatal(err)
	}
	a := &app{cfg: config{stateDir: dir, zcashAddressIndex: true, nodes: map[string]nodeConfig{
		nodeZcash:       {ID: nodeZcash, Enabled: true, Active: true, ActiveSet: true, RPCURL: server.URL, RPCCookiePath: cookie},
		nodeLitecoin:    {ID: nodeLitecoin},
		nodeBitcoinCash: {ID: nodeBitcoinCash},
	}}, log: log.New(io.Discard, "", 0), nodeHealth: make(map[string]nodeHealthSnapshot)}
	manager := newFamilyWatchManager(a)
	store := manager.stores[nodeZcash]
	if store == nil {
		t.Fatal("zcash store missing")
	}
	store.mu.Lock()
	store.wallets = []familyWatchEntry{{ID: familyWatchID(zcashTestAddress), Label: "Test", Address: zcashTestAddress}}
	store.mu.Unlock()
	return manager, store, fixture
}

func zcashWalletState(store *familyWatchStore) (string, string) {
	views, _ := store.list(false)
	amount := ""
	if views[0].Amount != nil {
		amount = *views[0].Amount
	}
	return views[0].State, amount
}

func TestZcashWatchScansBalanceWithCookieAuth(t *testing.T) {
	manager, store, fixture := newZcashWatchTest(t, 123456789)
	manager.scanNode(t.Context(), nodeZcash, store)
	if state, amount := zcashWalletState(store); state != "ok" || amount != "1.23456789" {
		t.Fatalf("state=%s amount=%s", state, amount)
	}
	if fixture.auth != "__cookie__:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=" {
		t.Fatalf("auth = %q", fixture.auth)
	}
	encoded, _ := json.Marshal(fixture.params)
	if string(encoded) != `[{"addresses":["`+zcashTestAddress+`"]}]` {
		t.Fatalf("params = %s", encoded)
	}
}

func TestZcashWatchZeroBalance(t *testing.T) {
	manager, store, _ := newZcashWatchTest(t, 0)
	manager.scanNode(t.Context(), nodeZcash, store)
	if state, amount := zcashWalletState(store); state != "ok" || amount != "0.00000000" {
		t.Fatalf("state=%s amount=%s", state, amount)
	}
}

func TestZcashWatchIndexUnavailableIsAnError(t *testing.T) {
	manager, store, fixture := newZcashWatchTest(t, 1)
	fixture.balanceErr = true
	manager.scanNode(t.Context(), nodeZcash, store)
	if state, amount := zcashWalletState(store); state != "error" || amount != "" {
		t.Fatalf("state=%s amount=%s", state, amount)
	}
}

func TestZcashWatchWaitsWhileSyncing(t *testing.T) {
	manager, store, fixture := newZcashWatchTest(t, 1)
	fixture.progress = 0.5
	manager.scanNode(t.Context(), nodeZcash, store)
	if state, _ := zcashWalletState(store); state != "waiting" {
		t.Fatalf("state=%s", state)
	}
	for _, method := range fixture.methods {
		if method == "getaddressbalance" {
			t.Fatal("balance queried before the node was ready")
		}
	}
}

func TestZcashWatchRejectsNonTransparentAddresses(t *testing.T) {
	_, store, fixture := newZcashWatchTest(t, 0)
	for _, address := range []string{"zs1z7rejlpsa98s2rrrfkwmaxu53e4ue0ulcrw0h4x5g8jl04tak0d3mm47vdtahatqrlkngh9slya", "u1abcdefghijklmnopqrstuvwxyz", "tmBsTi2xWTjUdEXnuTceL7fecEQKeWaPDJd", "bc1qar0srrr7xfkvy5l643lydnw9re59gtzzwf5mdq"} {
		if _, err := store.backend.validate(t.Context(), store, address); err == nil {
			t.Fatalf("%s accepted", address)
		}
	}
	if len(fixture.methods) != 0 {
		t.Fatalf("shielded address reached the node: %v", fixture.methods)
	}
	if _, err := store.backend.validate(t.Context(), store, "t1Invalid"); err == nil {
		t.Fatal("invalid transparent address accepted")
	}
	if got, err := store.backend.validate(t.Context(), store, zcashTestAddress); err != nil || got != zcashTestAddress {
		t.Fatalf("valid address = %q, %v", got, err)
	}
}

func TestZcashWatchCapabilityFollowsIndexFlag(t *testing.T) {
	off := &app{}
	if supported, reason := off.familyWatchCapability(nodeZcash); supported || reason == "" {
		t.Fatalf("default capability = %v %q", supported, reason)
	}
	on := &app{cfg: config{zcashAddressIndex: true}}
	if supported, _ := on.familyWatchCapability(nodeZcash); !supported {
		t.Fatal("index mode did not enable Zcash watch")
	}
	if supported, _ := on.familyWatchCapability(nodeDogecoin); supported {
		t.Fatal("Dogecoin became supported")
	}
	if got := defaultFamilyWatchEntry(nodeZcash); got.Address != "t3cFfPt1Bcvgez9ZbMBFWeZsskxTkPzGCow" || !got.Example || got.SourceURL != "https://zips.z.cash/zip-0214" {
		t.Fatalf("zcash seed = %+v", got)
	}
	if got := defaultFamilyWatchEntry(nodeDogecoin); got.Address != "" {
		t.Fatalf("dogecoin got a seed: %+v", got)
	}
}
