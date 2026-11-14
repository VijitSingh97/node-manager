package main

import (
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

const (
	dogecoinSeedAddress = "D89DhnsgKncmN12RejxudfU8AwXp3946q1"
	dogecoinUserAddress = "DUserAddress1"
)

type dogecoinWatchFixture struct {
	mu          sync.Mutex
	watching    bool
	release     chan struct{}
	unblock     func()
	importErr   bool
	unspent     []string
	unspentErr  bool
	imports     int
	listParams  []any
	importParam []any
}

func (f *dogecoinWatchFixture) handler(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Method string `json:"method"`
		Params []any  `json:"params"`
	}
	_ = json.NewDecoder(r.Body).Decode(&request)
	reply := func(result any, rpcErr any) {
		_ = json.NewEncoder(w).Encode(map[string]any{"result": result, "error": rpcErr})
	}
	f.mu.Lock()
	release := f.release
	f.mu.Unlock()
	switch request.Method {
	case "getblockchaininfo":
		reply(map[string]any{"blocks": 900, "headers": 900, "bestblockhash": "abc", "initialblockdownload": false}, nil)
	case "getblockheader":
		reply(map[string]any{"time": time.Now().Add(-time.Minute).Unix()}, nil)
	case "validateaddress":
		address, _ := request.Params[0].(string)
		f.mu.Lock()
		watching := f.watching
		f.mu.Unlock()
		reply(map[string]any{"isvalid": !strings.HasPrefix(address, "not"), "address": address, "iswatchonly": watching}, nil)
	case "importaddress":
		f.mu.Lock()
		f.imports++
		f.importParam = request.Params
		fail := f.importErr
		f.mu.Unlock()
		if release != nil {
			<-release
		}
		if fail {
			reply(nil, map[string]any{"code": -4, "message": "Rescan is disabled in pruned mode"})
			return
		}
		f.mu.Lock()
		f.watching = true
		f.mu.Unlock()
		reply(nil, nil)
	case "listunspent":
		f.mu.Lock()
		f.listParams = request.Params
		unspent, fail := f.unspent, f.unspentErr
		f.mu.Unlock()
		if fail {
			reply(nil, map[string]any{"code": -1, "message": "boom"})
			return
		}
		items := make([]json.RawMessage, 0, len(unspent))
		for _, amount := range unspent {
			items = append(items, json.RawMessage(`{"amount":`+amount+`,"spendable":false}`))
		}
		reply(items, nil)
	default:
		reply(nil, map[string]any{"code": -32601, "message": "unknown"})
	}
}

func newDogecoinWatchTest(t *testing.T) (*familyWatchManager, *familyWatchStore, *dogecoinWatchFixture) {
	t.Helper()
	fixture := &dogecoinWatchFixture{}
	server := httptest.NewServer(http.HandlerFunc(fixture.handler))
	t.Cleanup(server.Close)
	a := &app{cfg: config{stateDir: t.TempDir(), dogecoinWatchWallet: true, nodes: map[string]nodeConfig{
		nodeDogecoin:    {ID: nodeDogecoin, Enabled: true, Active: true, ActiveSet: true, RPCURL: server.URL, RPCUser: "u", RPCPassword: "p"},
		nodeLitecoin:    {ID: nodeLitecoin},
		nodeBitcoinCash: {ID: nodeBitcoinCash},
	}}, log: log.New(io.Discard, "", 0), nodeHealth: make(map[string]nodeHealthSnapshot)}
	manager := newFamilyWatchManager(a)
	store := manager.stores[nodeDogecoin]
	if store == nil {
		t.Fatal("dogecoin store missing")
	}
	if _, err := store.add("Test", dogecoinUserAddress); err != nil {
		t.Fatal(err)
	}
	return manager, store, fixture
}

func dogecoinWalletView(t *testing.T, store *familyWatchStore, address string) familyWatchWalletView {
	t.Helper()
	views, err := store.list(false)
	if err != nil {
		t.Fatal(err)
	}
	for _, view := range views {
		if view.Address == address {
			return view
		}
	}
	t.Fatalf("%s not listed", address)
	return familyWatchWalletView{}
}

func markDogecoinImported(store *familyWatchStore, address string) {
	store.mu.Lock()
	store.imported[familyWatchID(address)] = true
	store.mu.Unlock()
}

func waitImports(t *testing.T, f *dogecoinWatchFixture, want int) {
	t.Helper()
	for i := 0; i < 500; i++ {
		f.mu.Lock()
		got := f.imports
		f.mu.Unlock()
		if got == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("imports never reached %d", want)
}

func blockImports(t *testing.T, f *dogecoinWatchFixture) {
	t.Helper()
	release := make(chan struct{})
	var once sync.Once
	f.mu.Lock()
	f.release = release
	f.mu.Unlock()
	f.unblock = func() { once.Do(func() { close(release) }) }
	t.Cleanup(f.unblock)
}

func waitRescanDone(t *testing.T, store *familyWatchStore) {
	t.Helper()
	for i := 0; i < 500; i++ {
		if !store.rescanActive() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("rescan did not finish")
}

func TestDogecoinWatchRescansNewAddressThenSumsUnspent(t *testing.T) {
	manager, store, fixture := newDogecoinWatchTest(t)
	blockImports(t, fixture)
	fixture.unspent = []string{"1234.5", "0.00000001", "100000000.12345678"}
	manager.scanNode(t.Context(), nodeDogecoin, store)
	waitImports(t, fixture, 1)
	if view := dogecoinWalletView(t, store, dogecoinUserAddress); view.State != "rescanning" || view.Amount != nil {
		t.Fatalf("during rescan: %+v", view)
	}
	manager.scanNode(t.Context(), nodeDogecoin, store)
	if fixture.imports != 1 {
		t.Fatalf("second scan started another import: %d", fixture.imports)
	}
	encoded, _ := json.Marshal(fixture.importParam)
	if string(encoded) != `["`+dogecoinUserAddress+`","",true]` {
		t.Fatalf("importaddress params = %s", encoded)
	}
	fixture.unblock()
	waitRescanDone(t, store)
	manager.scanNode(t.Context(), nodeDogecoin, store)
	view := dogecoinWalletView(t, store, dogecoinUserAddress)
	if view.State != "ok" || view.Amount == nil || *view.Amount != "100001234.62345679" {
		t.Fatalf("after rescan: %+v", view)
	}
	encoded, _ = json.Marshal(fixture.listParams)
	if !strings.HasPrefix(string(encoded), `[1,9999999,["D`) {
		t.Fatalf("listunspent params = %s", encoded)
	}
}

func TestDogecoinWatchSkipsRescanForAlreadyWatchedAddress(t *testing.T) {
	manager, store, fixture := newDogecoinWatchTest(t)
	markDogecoinImported(store, dogecoinUserAddress)
	manager.scanNode(t.Context(), nodeDogecoin, store)
	view := dogecoinWalletView(t, store, dogecoinUserAddress)
	if view.State != "ok" || *view.Amount != "0.00000000" || fixture.imports != 0 {
		t.Fatalf("view=%+v imports=%d", view, fixture.imports)
	}
}

func TestDogecoinWatchOnlyOneRescanAtATime(t *testing.T) {
	manager, store, fixture := newDogecoinWatchTest(t)
	blockImports(t, fixture)
	manager.scanNode(t.Context(), nodeDogecoin, store)
	waitImports(t, fixture, 1)
	if view := dogecoinWalletView(t, store, dogecoinUserAddress); view.State != "rescanning" {
		t.Fatalf("state = %s", view.State)
	}
	if _, err := store.backend.validate(t.Context(), store, "DThird"); err == nil || !strings.Contains(err.Error(), "rescanning") {
		t.Fatalf("validate during rescan = %v", err)
	}
	fixture.unblock()
	waitRescanDone(t, store)
}

func TestDogecoinWatchImportFailureShowsErrorAndBacksOff(t *testing.T) {
	manager, store, fixture := newDogecoinWatchTest(t)
	fixture.importErr = true
	manager.scanNode(t.Context(), nodeDogecoin, store)
	waitRescanDone(t, store)
	for i := 0; i < 200 && !store.retryPending(dogecoinUserAddress); i++ {
		time.Sleep(10 * time.Millisecond)
	}
	fixture.importErr = false
	fixture.unspent = []string{"7"}
	manager.scanNode(t.Context(), nodeDogecoin, store)
	if view := dogecoinWalletView(t, store, dogecoinUserAddress); view.State != "error" || view.Amount != nil || fixture.imports != 1 {
		t.Fatalf("view = %+v imports=%d", view, fixture.imports)
	}
	if store.isImported(dogecoinUserAddress) {
		t.Fatal("failed import was marked complete")
	}
	store.mu.Lock()
	store.retryAt = map[string]int64{}
	store.mu.Unlock()
	manager.scanNode(t.Context(), nodeDogecoin, store)
	waitImports(t, fixture, 2)
	waitRescanDone(t, store)
	manager.scanNode(t.Context(), nodeDogecoin, store)
	if view := dogecoinWalletView(t, store, dogecoinUserAddress); view.State != "ok" || *view.Amount != "7.00000000" {
		t.Fatalf("after retry = %+v", view)
	}
}

func TestDogecoinWatchPartialRescanIsNotReportedAsBalance(t *testing.T) {
	manager, store, fixture := newDogecoinWatchTest(t)
	fixture.watching = true
	fixture.unspent = []string{"1"}
	blockImports(t, fixture)
	manager.scanNode(t.Context(), nodeDogecoin, store)
	waitImports(t, fixture, 1)
	if view := dogecoinWalletView(t, store, dogecoinUserAddress); view.State != "rescanning" || view.Amount != nil {
		t.Fatalf("watch-only flag treated as finished rescan: %+v", view)
	}
	fixture.unblock()
	waitRescanDone(t, store)
	restarted := &app{cfg: manager.app.cfg, log: manager.app.log}
	restarted.familyWatch = newFamilyWatchManager(restarted)
	again := restarted.familyWatch.stores[nodeDogecoin]
	if !again.isImported(dogecoinUserAddress) {
		t.Fatal("completed import marker not persisted")
	}
	other := familyWatchID("DOtherAddress1")
	if again.isImported("DOtherAddress1") || again.imported[other] {
		t.Fatal("unimported address treated as imported")
	}
}

func TestDogecoinWatchExampleIsNeverScanned(t *testing.T) {
	manager, store, fixture := newDogecoinWatchTest(t)
	markDogecoinImported(store, dogecoinUserAddress)
	manager.scanNode(t.Context(), nodeDogecoin, store)
	if view := dogecoinWalletView(t, store, dogecoinSeedAddress); view.State != "unscanned" || view.Amount != nil || fixture.imports != 0 {
		t.Fatalf("example view = %+v imports=%d", view, fixture.imports)
	}
}

func TestDogecoinWatchListunspentErrorAndBadAmounts(t *testing.T) {
	manager, store, fixture := newDogecoinWatchTest(t)
	markDogecoinImported(store, dogecoinUserAddress)
	fixture.unspentErr = true
	manager.scanNode(t.Context(), nodeDogecoin, store)
	if view := dogecoinWalletView(t, store, dogecoinUserAddress); view.State != "error" || view.Amount != nil {
		t.Fatalf("rpc error view = %+v", view)
	}
	fixture.unspentErr = false
	for _, bad := range [][]string{{"-1.0"}, {"1e3"}, {"0.123456789"}, {"180000000001.0"}} {
		fixture.unspent = bad
		manager.scanNode(t.Context(), nodeDogecoin, store)
		if view := dogecoinWalletView(t, store, dogecoinUserAddress); view.State != "error" || view.Amount != nil {
			t.Fatalf("%v view = %+v", bad, view)
		}
	}
}

func TestDogecoinWatchSumsNearSupplyLimit(t *testing.T) {
	manager, store, fixture := newDogecoinWatchTest(t)
	markDogecoinImported(store, dogecoinUserAddress)
	fixture.unspent = []string{"150000000000.12345678", "29999999999.87654321"}
	manager.scanNode(t.Context(), nodeDogecoin, store)
	view := dogecoinWalletView(t, store, dogecoinUserAddress)
	if view.State != "ok" || *view.Amount != "179999999999.99999999" {
		t.Fatalf("view = %+v", view)
	}
	fixture.unspent = []string{"150000000000.00000000", "30000000000.00000001"}
	store.mu.Lock()
	delete(store.balances, view.ID)
	store.mu.Unlock()
	manager.scanNode(t.Context(), nodeDogecoin, store)
	if view := dogecoinWalletView(t, store, dogecoinUserAddress); view.State != "error" || view.Amount != nil {
		t.Fatalf("over-limit view = %+v", view)
	}
}

func TestDogecoinWatchRejectsInvalidAddress(t *testing.T) {
	_, store, _ := newDogecoinWatchTest(t)
	if _, err := store.backend.validate(t.Context(), store, "not-an-address"); err == nil {
		t.Fatal("invalid address accepted")
	}
	if got, err := store.backend.validate(t.Context(), store, dogecoinUserAddress); err != nil || got != dogecoinUserAddress {
		t.Fatalf("valid address = %q, %v", got, err)
	}
}

func TestDogecoinWatchRosterSurvivesRestart(t *testing.T) {
	manager, store, fixture := newDogecoinWatchTest(t)
	markDogecoinImported(store, dogecoinUserAddress)
	fixture.unspent = []string{"5"}
	manager.scanNode(t.Context(), nodeDogecoin, store)
	restarted := &app{cfg: manager.app.cfg, log: manager.app.log}
	restarted.familyWatch = newFamilyWatchManager(restarted)
	views, err := restarted.familyWatch.stores[nodeDogecoin].list(false)
	if err != nil || len(views) != 2 || !views[0].Example || views[1].Address != dogecoinUserAddress || views[1].Amount == nil || *views[1].Amount != "5.00000000" {
		t.Fatalf("restarted roster = %+v, %v", views, err)
	}
}

func TestDogecoinWatchCapabilityAndSeed(t *testing.T) {
	off := &app{}
	if supported, reason := off.familyWatchCapability(nodeDogecoin); supported || reason == "" {
		t.Fatalf("default capability = %v %q", supported, reason)
	}
	if len(off.familyWatchNodes()) != 2 {
		t.Fatalf("default nodes = %v", off.familyWatchNodes())
	}
	on := &app{cfg: config{dogecoinWatchWallet: true}}
	if supported, _ := on.familyWatchCapability(nodeDogecoin); !supported {
		t.Fatal("watch wallet did not enable Dogecoin")
	}
	seed := defaultFamilyWatchEntry(nodeDogecoin)
	if seed.Address != dogecoinSeedAddress || seed.Label != "Dogecoin Foundation" || seed.SourceURL != "https://foundation.dogecoin.com/about/" || !seed.Example {
		t.Fatalf("seed = %+v", seed)
	}
}
