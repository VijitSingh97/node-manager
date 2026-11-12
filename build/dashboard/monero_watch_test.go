package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

var testMoneroAddress = "4" + strings.Repeat("1", 94)

type moneroWalletFixture struct {
	mu            sync.Mutex
	methods       []string
	unauthMethods []string
	generated     map[string]any
	imported      bool
	newOutput     bool
	generateFails bool
	refreshFails  bool
	secondAccount bool
	scanPending   bool
	daemonPending bool
}

func (f *moneroWalletFixture) handler(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Method string         `json:"method"`
		Params map[string]any `json:"params"`
	}
	if json.NewDecoder(r.Body).Decode(&request) != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if r.Header.Get("Authorization") == "" {
		f.mu.Lock()
		f.unauthMethods = append(f.unauthMethods, request.Method)
		f.mu.Unlock()
		w.Header().Set("WWW-Authenticate", `Digest realm="wallet", nonce="fixture-nonce", qop="auth", algorithm=MD5`)
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	f.mu.Lock()
	f.methods = append(f.methods, request.Method)
	if request.Method == "generate_from_keys" {
		f.generated = request.Params
	}
	if request.Method == "import_key_images" {
		f.imported = true
	}
	generatedAddress, generatedViewKey := testMoneroAddress, strings.Repeat("a", 64)
	if address, ok := f.generated["address"].(string); ok {
		generatedAddress = address
	}
	if viewKey, ok := f.generated["viewkey"].(string); ok {
		generatedViewKey = viewKey
	}
	imported, newOutput, generateFails, refreshFails, secondAccount, scanPending, daemonPending := f.imported, f.newOutput, f.generateFails, f.refreshFails, f.secondAccount, f.scanPending, f.daemonPending
	f.mu.Unlock()
	if request.Method == "generate_from_keys" && generateFails {
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": "dashboard", "error": map[string]any{"code": -1, "message": "wallet exists"}})
		return
	}
	if request.Method == "refresh" && refreshFails {
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": "dashboard", "error": map[string]any{"code": -1, "message": "refresh failed"}})
		return
	}
	result := any(map[string]any{})
	switch request.Method {
	case "generate_from_keys":
		result = map[string]any{"address": generatedAddress, "info": "wallet generated"}
	case "get_address":
		result = map[string]any{"address": generatedAddress, "addresses": []any{map[string]any{"address": generatedAddress, "address_index": 0}}}
	case "get_height":
		walletHeight := uint64(310)
		if scanPending {
			walletHeight = 300
		}
		result = map[string]any{"height": walletHeight}
	case "get_info":
		result = map[string]any{"synchronized": !daemonPending, "busy_syncing": daemonPending, "height": 310, "target_height": 310}
	case "query_key":
		result = map[string]any{"key": generatedViewKey}
	case "get_balance":
		balance, unlocked := uint64(1_000_000_000_000), uint64(900_000_000_000)
		if secondAccount {
			balance, unlocked = 1_500_000_000_000, 1_400_000_000_000
		}
		result = map[string]any{"balance": balance, "unlocked_balance": unlocked}
	case "get_accounts":
		accounts := []any{map[string]any{"account_index": 0}}
		if secondAccount {
			accounts = append(accounts, map[string]any{"account_index": 1})
		}
		result = map[string]any{"subaddress_accounts": accounts}
	case "incoming_transfers":
		accountIndex, _ := request.Params["account_index"].(float64)
		if accountIndex == 1 {
			result = map[string]any{"transfers": []any{map[string]any{"amount": 500_000_000_000, "key_image": ""}}}
			break
		}
		keyImage := ""
		if imported {
			keyImage = strings.Repeat("c", 64)
		}
		transfers := []any{map[string]any{"amount": 1_000_000_000_000, "key_image": keyImage}}
		if newOutput {
			transfers = append(transfers, map[string]any{"amount": 500_000_000_000, "key_image": ""})
		}
		result = map[string]any{"transfers": transfers}
	case "import_key_images":
		result = map[string]any{"height": 300, "spent": 0, "unspent": 1_000_000_000_000}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": "dashboard", "result": result})
}

func newMoneroWatchTestApp(t *testing.T, fixture *moneroWalletFixture) (*app, http.Handler, string) {
	return newMoneroWatchTestAppWithOptions(t, fixture, false, "dashboard-secret")
}

func newMoneroWatchTestAppWithOptions(t *testing.T, fixture *moneroWalletFixture, publicExample bool, dashboardPassword string) (*app, http.Handler, string) {
	t.Helper()
	if dashboardPassword == "" {
		dashboardPassword = "dashboard-secret"
	}
	walletServer := httptest.NewServer(http.HandlerFunc(fixture.handler))
	t.Cleanup(walletServer.Close)
	bitcoinFixture := &rpcFixture{}
	bitcoinServer := httptest.NewServer(http.HandlerFunc(bitcoinFixture.handler))
	t.Cleanup(bitcoinServer.Close)
	stateDir := t.TempDir()
	cfg := config{
		rpcURL: bitcoinServer.URL, rpcUser: "rpc-user", rpcPassword: "rpc-password", dashboardPassword: dashboardPassword,
		stateDir: stateDir, dataDir: t.TempDir(), version: "test", moneroEnabled: true, moneroWalletEnabled: true, moneroPublicExample: publicExample,
		moneroRPCURL: walletServer.URL, moneroRPCUser: "wallet-user", moneroRPCPassword: "wallet-rpc-password",
		moneroWalletRPCURL: walletServer.URL, moneroWalletRPCUser: "wallet-user", moneroWalletRPCPassword: "wallet-rpc-password", moneroWalletPassword: "encrypted-wallet-password",
	}
	rpc := &rpcClient{url: bitcoinServer.URL, user: cfg.rpcUser, password: cfg.rpcPassword, client: bitcoinServer.Client()}
	a, err := newApp(cfg, rpc, "index.html", "static", log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatal(err)
	}
	a.ctx = context.Background()
	return a, a.routes(), stateDir
}

func moneroMutation(t *testing.T, handler http.Handler, method, path, body string, csrf bool) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.SetBasicAuth("admin", "dashboard-secret")
	if csrf {
		req.Header.Set("X-Requested-With", "fetch")
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	return response
}

func TestMoneroWatchAddStoresNoViewKeyAndRestarts(t *testing.T) {
	fixture := &moneroWalletFixture{}
	a, handler, stateDir := newMoneroWatchTestApp(t, fixture)
	viewKey := strings.Repeat("a", 64)
	body := `{"name":"Cold view","address":"` + testMoneroAddress + `","view_key":"` + viewKey + `","restore_height":123}`
	response := moneroMutation(t, handler, http.MethodPost, "/api/monero/watch", body, true)
	if response.Code != http.StatusOK {
		t.Fatalf("add = %d %q", response.Code, response.Body.String())
	}
	fixture.mu.Lock()
	generated := fixture.generated
	unauthMethods := append([]string(nil), fixture.unauthMethods...)
	fixture.mu.Unlock()
	if generated["spendkey"] != nil || generated["viewkey"] != viewKey || generated["password"] != "encrypted-wallet-password" {
		t.Fatalf("unsafe generate_from_keys params: %#v", generated)
	}
	if len(unauthMethods) == 0 {
		t.Fatal("wallet RPC Digest challenge was not obtained")
	}
	for _, method := range unauthMethods {
		if method != "get_version" && method != "get_info" {
			t.Fatalf("sensitive wallet RPC method sent before authentication: %q", method)
		}
	}
	data, err := os.ReadFile(filepath.Join(stateDir, "monero_watch.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), viewKey) || strings.Contains(string(data), "view_key") || strings.Contains(string(data), "encrypted-wallet-password") {
		t.Fatal("private view key or wallet password persisted in dashboard state")
	}
	info, err := os.Stat(filepath.Join(stateDir, "monero_watch.json"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("state mode = %v, err %v", info.Mode().Perm(), err)
	}

	restarted := NewMoneroWatchStore(a.cfg)
	wallets := restarted.List()
	internal, found := restarted.walletByID(moneroWalletID(testMoneroAddress))
	if restarted.Err() != nil || len(wallets) != 1 || wallets[0].Name != "Cold view" || wallets[0].State != "pending" || wallets[0].WalletFile != "" || !found || !validManagedWalletFilename(internal.ID, internal.WalletFile) {
		t.Fatalf("restart = %#v, err %v", wallets, restarted.Err())
	}
	encoded, _ := json.Marshal(wallets)
	if strings.Contains(string(encoded), viewKey) || strings.Contains(string(encoded), "wallet_file") {
		t.Fatal("private or internal wallet material returned by API model")
	}
	listed := request(t, handler, http.MethodGet, "/api/monero/watch", "dashboard-secret")
	if listed.Code != http.StatusOK || strings.Contains(listed.Body.String(), viewKey) || strings.Contains(listed.Body.String(), "wallet_file") || !strings.Contains(listed.Body.String(), `"available":true`) {
		t.Fatalf("list response leaked private material: %d %q", listed.Code, listed.Body.String())
	}
}

func TestMoneroWatchRequiresAuthCSRFAndRejectsPrivateSpendMaterial(t *testing.T) {
	fixture := &moneroWalletFixture{}
	_, handler, _ := newMoneroWatchTestApp(t, fixture)
	viewKey := strings.Repeat("a", 64)
	valid := `{"name":"Cold","address":"` + testMoneroAddress + `","view_key":"` + viewKey + `","restore_height":0}`
	req := httptest.NewRequest(http.MethodPost, "/api/monero/watch", strings.NewReader(valid))
	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(unauthorized, req)
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized add = %d", unauthorized.Code)
	}
	if response := moneroMutation(t, handler, http.MethodPost, "/api/monero/watch", valid, false); response.Code != http.StatusForbidden {
		t.Fatalf("missing CSRF = %d", response.Code)
	}
	unsafe := strings.TrimSuffix(valid, "}") + `,"spendkey":"` + strings.Repeat("b", 64) + `"}`
	if response := moneroMutation(t, handler, http.MethodPost, "/api/monero/watch", unsafe, true); response.Code != http.StatusBadRequest {
		t.Fatalf("spend key accepted = %d %q", response.Code, response.Body.String())
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if len(fixture.methods) != 0 {
		t.Fatalf("unsafe requests reached wallet RPC: %v", fixture.methods)
	}
}

func TestMoneroWatchKeyImageCoverageControlsBalanceClaims(t *testing.T) {
	fixture := &moneroWalletFixture{}
	a, _, stateDir := newMoneroWatchTestApp(t, fixture)
	wallet, err := a.moneroWatch.Add(context.Background(), "Cold", testMoneroAddress, strings.Repeat("a", 64), 0)
	if err != nil {
		t.Fatal(err)
	}
	a.moneroWatch.refresh(context.Background(), wallet)
	unknown := a.moneroWatch.List()[0]
	if unknown.ReceivedAtomic != "1000000000000" || unknown.BalanceAtomic != "" || unknown.SpendingStatus != "unknown" || unknown.KeyImages.Complete || len(unknown.History) != 0 {
		t.Fatalf("unknown balance state = %#v", unknown)
	}
	upload := moneroKeyImageUpload{SignedKeyImages: []moneroSignedKeyImage{{KeyImage: strings.Repeat("c", 64), Signature: strings.Repeat("d", 128)}}}
	if err := a.moneroWatch.ImportKeyImages(context.Background(), wallet.ID, upload); err != nil {
		t.Fatal(err)
	}
	a.moneroWatch.refresh(context.Background(), wallet)
	verified := a.moneroWatch.List()[0]
	if verified.BalanceAtomic != "1000000000000" || verified.BalanceXMR != "1.000000000000" || verified.SpendingStatus != "verified" || !verified.KeyImages.Complete || len(verified.History) != 1 || !verified.History[0].Verified {
		t.Fatalf("verified balance state = %#v", verified)
	}
	if err := a.moneroWatch.ImportKeyImages(context.Background(), wallet.ID, upload); err != nil {
		t.Fatal(err)
	}
	importPending := a.moneroWatch.List()[0]
	if importPending.KeyImages.Complete || importPending.BalanceAtomic != "" || importPending.UnlockedBalanceAtomic != "" || importPending.State != "pending" {
		t.Fatalf("key-image import retained an old verified balance: %#v", importPending)
	}
	a.moneroWatch.refresh(context.Background(), wallet)
	fixture.mu.Lock()
	fixture.scanPending = true
	fixture.mu.Unlock()
	a.moneroWatch.refresh(context.Background(), wallet)
	pending := a.moneroWatch.List()[0]
	if pending.State != "pending" || pending.BalanceAtomic != "" || pending.BalanceXMR != "" || pending.UnlockedBalanceAtomic != "" || pending.KeyImages.Complete || pending.SpendingStatus != "stale" {
		t.Fatalf("pending refresh retained a verified current balance: %#v", pending)
	}
	fixture.mu.Lock()
	fixture.scanPending = false
	fixture.newOutput = true
	fixture.mu.Unlock()
	a.moneroWatch.refresh(context.Background(), wallet)
	stale := a.moneroWatch.List()[0]
	if stale.ReceivedAtomic != "1500000000000" || stale.BalanceAtomic != "" || stale.SpendingStatus != "stale" || stale.KeyImages.Complete || len(stale.History) != 1 {
		t.Fatalf("new output did not invalidate coverage = %#v", stale)
	}
	restarted := NewMoneroWatchStore(config{stateDir: stateDir})
	restored := restarted.List()
	if restarted.Err() != nil || len(restored) != 1 || len(restored[0].History) != 1 || !restored[0].History[0].Verified || restored[0].State != "pending" || restored[0].BalanceAtomic != "" || restored[0].KeyImages.Complete || restored[0].SpendingStatus != "stale" {
		t.Fatalf("verified history did not survive restart safely: %#v, %v", restored, restarted.Err())
	}
}

func TestMoneroWatchHTTPKeyImagesValidationAndRemoval(t *testing.T) {
	fixture := &moneroWalletFixture{}
	a, handler, _ := newMoneroWatchTestApp(t, fixture)
	wallet, err := a.moneroWatch.Add(context.Background(), "Cold", testMoneroAddress, strings.Repeat("a", 64), 0)
	if err != nil {
		t.Fatal(err)
	}
	bad := `{"offset":0,"signed_key_images":[{"key_image":"bad","signature":"bad"}]}`
	if response := moneroMutation(t, handler, http.MethodPost, "/api/monero/watch/"+wallet.ID+"/key-images", bad, true); response.Code != http.StatusBadRequest {
		t.Fatalf("bad key images = %d", response.Code)
	}
	valid := `{"offset":0,"signed_key_images":[{"key_image":"` + strings.Repeat("c", 64) + `","signature":"` + strings.Repeat("d", 128) + `"}]}`
	if response := moneroMutation(t, handler, http.MethodPost, "/api/monero/watch/"+wallet.ID+"/key-images", valid, true); response.Code != http.StatusOK {
		t.Fatalf("key images = %d %q", response.Code, response.Body.String())
	}
	if response := moneroMutation(t, handler, http.MethodDelete, "/api/monero/watch/"+wallet.ID, "", true); response.Code != http.StatusOK {
		t.Fatalf("remove = %d %q", response.Code, response.Body.String())
	}
	if len(a.moneroWatch.List()) != 0 {
		t.Fatal("wallet remained after removal")
	}
}

func TestMoneroWatchRequiresCompleteCoverageAcrossEveryAccount(t *testing.T) {
	fixture := &moneroWalletFixture{secondAccount: true}
	a, _, _ := newMoneroWatchTestApp(t, fixture)
	wallet, err := a.moneroWatch.Add(context.Background(), "Cold", testMoneroAddress, strings.Repeat("a", 64), 0)
	if err != nil {
		t.Fatal(err)
	}
	upload := moneroKeyImageUpload{SignedKeyImages: []moneroSignedKeyImage{{KeyImage: strings.Repeat("c", 64), Signature: strings.Repeat("d", 128)}}}
	if err := a.moneroWatch.ImportKeyImages(context.Background(), wallet.ID, upload); err != nil {
		t.Fatal(err)
	}
	a.moneroWatch.refresh(context.Background(), wallet)
	status := a.moneroWatch.List()[0]
	if status.KeyImages.TotalOutputs != 2 || status.KeyImages.CoveredOutputs != 1 || status.KeyImages.Complete || status.BalanceAtomic != "" || status.SpendingStatus != "stale" {
		t.Fatalf("secondary account was omitted from coverage: %#v", status)
	}
}

func TestMoneroWatchStaysPendingUntilWalletReachesDaemonHeight(t *testing.T) {
	fixture := &moneroWalletFixture{scanPending: true}
	a, _, _ := newMoneroWatchTestApp(t, fixture)
	wallet, err := a.moneroWatch.Add(context.Background(), "Cold", testMoneroAddress, strings.Repeat("a", 64), 0)
	if err != nil {
		t.Fatal(err)
	}
	fixture.mu.Lock()
	fixture.methods = nil
	fixture.mu.Unlock()
	a.moneroWatch.refresh(context.Background(), wallet)
	status := a.moneroWatch.List()[0]
	if status.State != "pending" || status.ScanHeight != 300 || status.TargetHeight != 310 || status.BalanceAtomic != "" || len(status.History) != 0 {
		t.Fatalf("incomplete scan claimed readiness: %#v", status)
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if slices.Contains(fixture.methods, "refresh") || !slices.Contains(fixture.methods, "auto_refresh") {
		t.Fatalf("behind wallet must scan via auto_refresh, not a blocking refresh: %v", fixture.methods)
	}
}

func TestMoneroWatchStaysPendingDuringDaemonInitialSync(t *testing.T) {
	fixture := &moneroWalletFixture{daemonPending: true}
	a, _, _ := newMoneroWatchTestApp(t, fixture)
	wallet, err := a.moneroWatch.Add(context.Background(), "Cold", testMoneroAddress, strings.Repeat("a", 64), 0)
	if err != nil {
		t.Fatal(err)
	}
	fixture.mu.Lock()
	fixture.methods = nil
	fixture.mu.Unlock()
	a.moneroWatch.refresh(context.Background(), wallet)
	status := a.moneroWatch.List()[0]
	if status.State != "pending" || status.BalanceAtomic != "" || status.Error != "Monero wallet scan is still pending." {
		t.Fatalf("daemon IBD claimed readiness: %#v", status)
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if len(fixture.methods) != 1 || fixture.methods[0] != "get_info" {
		t.Fatalf("daemon IBD reached wallet RPC: %v", fixture.methods)
	}
}

func TestMoneroWatchRejectsFutureRestoreHeight(t *testing.T) {
	fixture := &moneroWalletFixture{}
	a, _, _ := newMoneroWatchTestApp(t, fixture)
	_, err := a.moneroWatch.Add(context.Background(), "Future", testMoneroAddress, strings.Repeat("a", 64), 311)
	if err == nil || !strings.Contains(err.Error(), "beyond") {
		t.Fatalf("future restore height accepted: %v", err)
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	for _, method := range fixture.methods {
		if method == "generate_from_keys" {
			t.Fatal("future restore height reached wallet creation")
		}
	}
}

func TestMoneroWatchRefreshFailureIsActionable(t *testing.T) {
	fixture := &moneroWalletFixture{}
	a, _, _ := newMoneroWatchTestApp(t, fixture)
	wallet, err := a.moneroWatch.Add(context.Background(), "Cold", testMoneroAddress, strings.Repeat("a", 64), 0)
	if err != nil {
		t.Fatal(err)
	}
	fixture.mu.Lock()
	fixture.refreshFails = true
	fixture.mu.Unlock()
	a.moneroWatch.refresh(context.Background(), wallet)
	status := a.moneroWatch.List()[0]
	if status.State != "error" || status.BalanceAtomic != "" || status.Error != "Monero wallet refresh failed." {
		t.Fatalf("refresh failure mislabeled: %#v", status)
	}
}

func TestMoneroWatchRejectsUnknownExistingWalletFile(t *testing.T) {
	fixture := &moneroWalletFixture{generateFails: true}
	a, _, stateDir := newMoneroWatchTestApp(t, fixture)
	_, err := a.moneroWatch.Add(context.Background(), "Unsafe", testMoneroAddress, strings.Repeat("a", 64), 0)
	if err == nil || len(a.moneroWatch.List()) != 0 {
		t.Fatal("unknown existing wallet accepted")
	}
	if _, statErr := os.Stat(filepath.Join(stateDir, "monero_watch.json")); !os.IsNotExist(statErr) {
		t.Fatalf("unsafe wallet persisted: %v", statErr)
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	for _, method := range fixture.methods {
		if method == "open_wallet" {
			t.Fatal("unknown wallet file was opened")
		}
	}
}

func TestMoneroWatchReaddUsesNewManagedWalletFile(t *testing.T) {
	fixture := &moneroWalletFixture{}
	a, _, _ := newMoneroWatchTestApp(t, fixture)
	viewKey := strings.Repeat("a", 64)
	wallet, err := a.moneroWatch.Add(context.Background(), "Cold", testMoneroAddress, viewKey, 0)
	if err != nil {
		t.Fatal(err)
	}
	if removed, err := a.moneroWatch.Remove(wallet.ID); err != nil || !removed {
		t.Fatalf("remove = %v, %v", removed, err)
	}
	readded, err := a.moneroWatch.Add(context.Background(), "Cold again", testMoneroAddress, viewKey, 0)
	if err != nil {
		t.Fatalf("readd encrypted wallet file: %v", err)
	}
	if wallet.WalletFile == readded.WalletFile || !validManagedWalletFilename(readded.ID, readded.WalletFile) {
		t.Fatal("readd reused an unmanaged wallet filename")
	}
}

func TestMoneroWatchSerializedWaitHonorsContext(t *testing.T) {
	fixture := &moneroWalletFixture{}
	a, _, _ := newMoneroWatchTestApp(t, fixture)
	<-a.moneroWatch.gate
	defer func() { a.moneroWatch.gate <- struct{}{} }()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	_, err := a.moneroWatch.Add(ctx, "Cold", testMoneroAddress, strings.Repeat("a", 64), 0)
	if err == nil {
		t.Fatal("serialized wallet RPC ignored canceled context")
	}
}

func TestMoneroWatchInterruptedRPCPoisonsService(t *testing.T) {
	fixture := &moneroWalletFixture{}
	a, _, _ := newMoneroWatchTestApp(t, fixture)
	ctx, cancel := context.WithCancel(context.Background())
	err := a.moneroWatch.serialized(ctx, func(ctx context.Context) error {
		cancel()
		<-ctx.Done()
		return ctx.Err()
	})
	if !errors.Is(err, context.Canceled) || !a.moneroWatch.isRPCPoisoned() {
		t.Fatalf("interrupted RPC did not poison service: %v", err)
	}
	fixture.mu.Lock()
	before := len(fixture.methods)
	fixture.mu.Unlock()
	_, err = a.moneroWatch.Add(context.Background(), "Cold", testMoneroAddress, strings.Repeat("a", 64), 0)
	if err == nil || !strings.Contains(err.Error(), "requires a dashboard restart") {
		t.Fatalf("poisoned service accepted another wallet RPC: %v", err)
	}
	fixture.mu.Lock()
	after := len(fixture.methods)
	fixture.mu.Unlock()
	if after != before {
		t.Fatalf("poisoned service sent another wallet RPC: before=%d after=%d", before, after)
	}
}

func TestMoneroWalletRPCRejectsSpendKeyQuery(t *testing.T) {
	fixture := &moneroWalletFixture{}
	a, _, _ := newMoneroWatchTestApp(t, fixture)
	var result any
	err := a.moneroWatch.walletCall(context.Background(), "query_key", map[string]any{"key_type": "spend_key"}, &result)
	if err == nil {
		t.Fatal("private spend-key query was allowed")
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if len(fixture.methods) != 0 || len(fixture.unauthMethods) != 0 {
		t.Fatalf("rejected spend-key query reached wallet RPC: %v %v", fixture.methods, fixture.unauthMethods)
	}
}

func TestMoneroWatchRemoteUnknownPoisonsService(t *testing.T) {
	fixture := &moneroWalletFixture{}
	a, _, _ := newMoneroWatchTestApp(t, fixture)
	err := a.moneroWatch.serialized(context.Background(), func(context.Context) error {
		return errMoneroRemoteUnknown
	})
	if !errors.Is(err, errMoneroRemoteUnknown) || !a.moneroWatch.isRPCPoisoned() {
		t.Fatalf("unknown remote result did not poison service: %v", err)
	}
}

func TestMoneroWatchInterruptedCreationBlocksFurtherWalletChanges(t *testing.T) {
	fixture := &moneroWalletFixture{}
	a, _, _ := newMoneroWatchTestApp(t, fixture)
	calls := 0
	a.moneroWatch.rpc.client = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		if request.Header.Get("Authorization") == "" {
			return &http.Response{
				StatusCode: http.StatusUnauthorized,
				Header:     http.Header{"Www-Authenticate": []string{`Digest realm="wallet", nonce="fixture-nonce", qop="auth", algorithm=MD5`}},
				Body:       io.NopCloser(strings.NewReader("")),
			}, nil
		}
		return nil, io.ErrUnexpectedEOF
	})}
	_, err := a.moneroWatch.Add(context.Background(), "Cold", testMoneroAddress, strings.Repeat("a", 64), 0)
	if err == nil || !a.moneroWatch.isRPCPoisoned() || !strings.Contains(err.Error(), "requires a dashboard restart") {
		t.Fatalf("interrupted creation did not stop wallet changes: %v", err)
	}
	_, err = a.moneroWatch.Add(context.Background(), "Another", testMoneroAddress, strings.Repeat("a", 64), 0)
	if err == nil || calls != 2 {
		t.Fatalf("another wallet operation reached the interrupted service: %v, calls=%d", err, calls)
	}
}

func TestMoneroWalletDigestClassifiesAuthenticatedTransportFailures(t *testing.T) {
	for _, test := range []struct {
		name string
		body io.ReadCloser
		err  error
	}{
		{name: "transport", err: io.ErrUnexpectedEOF},
		{name: "body", body: failingReadCloser{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				calls++
				if calls == 1 {
					return &http.Response{
						StatusCode: http.StatusUnauthorized,
						Header:     http.Header{"Www-Authenticate": []string{`Digest realm="wallet", nonce="fixture-nonce", qop="auth", algorithm=MD5`}},
						Body:       io.NopCloser(strings.NewReader("")),
					}, nil
				}
				if test.err != nil {
					return nil, test.err
				}
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: test.body}, nil
			})}
			rpc := moneroRPC{url: "http://wallet.invalid", user: "user", password: "password", client: client}
			_, err := rpc.doDigest(context.Background(), http.MethodPost, "/json_rpc", []byte(`{"jsonrpc":"2.0","method":"store"}`), []byte(`{"jsonrpc":"2.0","method":"get_version"}`))
			if !errors.Is(err, errMoneroRemoteUnknown) || calls != 2 {
				t.Fatalf("authenticated failure = %v, calls = %d", err, calls)
			}
		})
	}
}

type failingReadCloser struct{}

func (failingReadCloser) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
func (failingReadCloser) Close() error             { return nil }

func TestMoneroWalletDigestProbeFailsClosedWithoutAuthentication(t *testing.T) {
	var methods []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		methods = append(methods, request.Method)
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": "dashboard", "result": map[string]any{}})
	}))
	defer server.Close()
	rpc := moneroRPC{url: server.URL, user: "wallet-user", password: "wallet-password", client: server.Client()}
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": "dashboard", "method": "generate_from_keys", "params": map[string]any{"viewkey": strings.Repeat("a", 64)}})
	probe, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": "dashboard-auth", "method": "get_version"})
	if _, err := rpc.doDigest(context.Background(), http.MethodPost, "/json_rpc", body, probe); err == nil {
		t.Fatal("wallet RPC without Digest authentication was accepted")
	}
	if len(methods) != 1 || methods[0] != "get_version" {
		t.Fatalf("sensitive wallet RPC body sent without authentication: %v", methods)
	}
}

func TestMoneroWatchRejectsSecretFieldsInStoredRoster(t *testing.T) {
	stateDir := t.TempDir()
	path := filepath.Join(stateDir, "monero_watch.json")
	data := `{"wallets":[{"id":"` + moneroWalletID(testMoneroAddress) + `","name":"Cold","address":"` + testMoneroAddress + `","restore_height":0,"view_key":"` + strings.Repeat("a", 64) + `"}]}`
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	store := NewMoneroWatchStore(config{stateDir: stateDir})
	if store.Err() == nil || len(store.List()) != 0 {
		t.Fatal("stored secret field was silently accepted")
	}
}

func TestMoneroWalletConfigRequiresCompleteDedicatedCredentials(t *testing.T) {
	t.Setenv("BITCOIN_ENABLED", "false")
	t.Setenv("MONERO_ENABLED", "true")
	t.Setenv("MONERO_RPC_USER", "node-user")
	t.Setenv("MONERO_RPC_PASSWORD", "node-password")
	t.Setenv("MONERO_WALLET_RPC_USER", "wallet-user")
	t.Setenv("MONERO_WALLET_RPC_PASSWORD", "")
	t.Setenv("MONERO_WALLET_PASSWORD", "")
	if _, err := envConfig(); err == nil {
		t.Fatal("partial wallet credentials accepted")
	}
	t.Setenv("MONERO_WALLET_RPC_PASSWORD", "wallet-rpc-password")
	t.Setenv("MONERO_WALLET_PASSWORD", "wallet-file-password")
	t.Setenv("MONERO_WALLET_RPC_URL", "https://example.com")
	if _, err := envConfig(); err == nil {
		t.Fatal("non-HTTP wallet RPC URL accepted")
	}
	t.Setenv("MONERO_WALLET_RPC_URL", "http://172.29.0.31:18082")
	cfg, err := envConfig()
	if err != nil || !cfg.moneroWalletEnabled || cfg.moneroWalletRPCUser != "wallet-user" || cfg.moneroWalletPassword != "wallet-file-password" {
		t.Fatalf("wallet configuration = %#v, %v", cfg, err)
	}
	if !cfg.moneroPublicExample {
		t.Fatal("public Monero example was not enabled by default")
	}
	t.Setenv("MONERO_PUBLIC_EXAMPLE", "false")
	cfg, err = envConfig()
	if err != nil || cfg.moneroPublicExample {
		t.Fatalf("public Monero example opt-out = %#v, %v", cfg, err)
	}
}

func TestMoneroWatchAcceptsOnlyMainnetPrimaryAddressShape(t *testing.T) {
	if !validMoneroAddress(testMoneroAddress) {
		t.Fatal("mainnet primary address shape rejected")
	}
	for _, address := range []string{"8" + strings.Repeat("1", 94), "9" + strings.Repeat("1", 94), "4" + strings.Repeat("1", 105), "4" + strings.Repeat("0", 94)} {
		if validMoneroAddress(address) {
			t.Fatalf("non-primary or non-mainnet address shape accepted: %q", address[:1])
		}
	}
}
