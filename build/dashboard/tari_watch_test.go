package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type tariWatchRPCFixture struct {
	UnimplementedWalletServer
	mu        sync.Mutex
	online    bool
	scanned   uint64
	validated bool
	balance   *TariGetBalanceResponse
	failures  int
	started   chan struct{}
	release   chan struct{}
}

func (f *tariWatchRPCFixture) GetVersion(context.Context, *TariGetVersionRequest) (*TariGetVersionResponse, error) {
	return &TariGetVersionResponse{Version: "6.1.0"}, nil
}

func (f *tariWatchRPCFixture) GetCompleteAddress(context.Context, *TariWalletEmpty) (*TariGetCompleteAddressResponse, error) {
	return &TariGetCompleteAddressResponse{OneSidedAddressBase58: "tari-address"}, nil
}

func (f *tariWatchRPCFixture) GetState(context.Context, *TariGetStateRequest) (*TariGetStateResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	connectivity := TariWalletConnectivityStatus_TARI_WALLET_OFFLINE
	if f.online {
		connectivity = TariWalletConnectivityStatus_TARI_WALLET_ONLINE
	}
	balance := cloneTariBalance(f.balance)
	return &TariGetStateResponse{ScannedHeight: f.scanned, Balance: balance, Network: &TariWalletNetworkStatusResponse{Status: connectivity}, HasDoneInitialValidation: f.validated}, nil
}

func (f *tariWatchRPCFixture) GetBalance(context.Context, *TariGetBalanceRequest) (*TariGetBalanceResponse, error) {
	f.mu.Lock()
	if f.failures > 0 {
		f.failures--
		f.mu.Unlock()
		return nil, status.Error(codes.Unavailable, "wallet temporarily unavailable")
	}
	balance := cloneTariBalance(f.balance)
	started, release := f.started, f.release
	f.mu.Unlock()
	if started != nil {
		select {
		case <-started:
		default:
			close(started)
		}
	}
	if release != nil {
		<-release
	}
	return balance, nil
}

func (f *tariWatchRPCFixture) set(online bool, balance *TariGetBalanceResponse) {
	f.mu.Lock()
	f.online, f.scanned, f.validated, f.balance = online, 100, true, balance
	f.mu.Unlock()
}

func (f *tariWatchRPCFixture) setSync(scanned uint64, validated bool) {
	f.mu.Lock()
	f.scanned, f.validated = scanned, validated
	f.mu.Unlock()
}

func (f *tariWatchRPCFixture) failBalance(count int) {
	f.mu.Lock()
	f.failures = count
	f.mu.Unlock()
}

func (f *tariWatchRPCFixture) blockBalance() (<-chan struct{}, chan<- struct{}) {
	f.mu.Lock()
	f.started, f.release = make(chan struct{}), make(chan struct{})
	started, release := f.started, f.release
	f.mu.Unlock()
	return started, release
}

func cloneTariBalance(balance *TariGetBalanceResponse) *TariGetBalanceResponse {
	if balance == nil {
		return &TariGetBalanceResponse{}
	}
	return &TariGetBalanceResponse{
		AvailableBalance: balance.AvailableBalance, TimelockedBalance: balance.TimelockedBalance,
		PendingIncomingBalance: balance.PendingIncomingBalance, PendingOutgoingBalance: balance.PendingOutgoingBalance,
	}
}

func startTariWatchRPC(t *testing.T, fixture *tariWatchRPCFixture, user, password string) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+password))
	server := grpc.NewServer(grpc.UnaryInterceptor(func(ctx context.Context, request any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		values, _ := metadata.FromIncomingContext(ctx)
		if got := values.Get("authorization"); len(got) != 1 || got[0] != want {
			return nil, status.Error(codes.Unauthenticated, "authentication required")
		}
		return handler(ctx, request)
	}))
	RegisterWalletServer(server, fixture)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
	})
	return "http://" + listener.Addr().String()
}

func writeTariCapability(t *testing.T, store *TariWatchStore, now time.Time) {
	t.Helper()
	data, err := json.Marshal(tariWatchCapability{Schema: 1, UpdatedAt: now.Unix(), Actions: []string{"create", "remove"}, MaxRequestBytes: 4096})
	if err != nil || writePrivateFileAtomic(store.capabilityPath, data) != nil {
		t.Fatal("write capability")
	}
}

func readTariRequest(t *testing.T, store *TariWatchStore) tariWatchRequest {
	t.Helper()
	data, err := os.ReadFile(store.requestPath)
	var request tariWatchRequest
	if err != nil || !decodeStrictBytes(data, &request) {
		t.Fatal("read request")
	}
	return request
}

func TestTariWatchLifecycleUsesAuthenticatedRPCAndPreservesVerifiedBalance(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, time.October, 31, 12, 0, 0, 0, time.UTC)
	fixture := &tariWatchRPCFixture{}
	fixture.set(true, &TariGetBalanceResponse{AvailableBalance: 1_000_000, TimelockedBalance: 20, PendingIncomingBalance: 3, PendingOutgoingBalance: 4})
	endpoint := startTariWatchRPC(t, fixture, "wallet-user", "wallet-password")
	cfg := config{stateDir: filepath.Join(dir, "state"), tariWalletDir: filepath.Join(dir, "wallet"), tariWalletURL: endpoint, tariWalletRPCUser: "wallet-user", tariWalletRPCPassword: "wallet-password"}
	store := NewTariWatchStore(cfg)
	store.now = func() time.Time { return now }
	writeTariCapability(t, store, now)
	viewKey := strings.Repeat("a", 64)
	spendKey := strings.Repeat("b", 64)
	wallet, err := store.Add("Cold wallet", spendKey, viewKey, "2025-01-01")
	if err != nil || wallet.State != "pending" {
		t.Fatalf("add = %#v, %v", wallet, err)
	}
	request := readTariRequest(t, store)
	if request.ViewKey != viewKey || request.PublicSpendKey != spendKey || request.Action != "create" {
		t.Fatal("create request did not preserve the one-time wallet inputs")
	}
	if info, err := os.Stat(store.requestPath); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("request permissions = %v, %v", info, err)
	}
	stateData, err := os.ReadFile(store.path)
	if err != nil || strings.Contains(string(stateData), viewKey) {
		t.Fatal("private view key reached persistent dashboard state")
	}
	identity := tariWatchIdentity{WalletID: wallet.ID, PublicSpendKey: spendKey, BirthdayDays: request.BirthdayDays}
	identityData, _ := json.Marshal(identity)
	statusData, _ := json.Marshal(tariWatchStatus{ID: request.ID, Action: "create", WalletID: wallet.ID, State: "ready"})
	if writePrivateFileAtomic(store.identityPath, identityData) != nil || writePrivateFileAtomic(store.statusPath, statusData) != nil {
		t.Fatal("write ready status")
	}
	probe := func(context.Context) (tariWatchNodeSnapshot, error) {
		return tariWatchNodeSnapshot{Ready: true, Height: 100, TargetHeight: 100}, nil
	}
	fixture.setSync(50, false)
	store.step(context.Background(), func() bool { return true }, probe)
	got := store.List()[0]
	if got.State != "waiting" || got.BalanceAtomic != "" || got.ScanHeight != 50 || got.TargetHeight != 100 {
		t.Fatalf("scanning wallet = %#v", got)
	}
	fixture.setSync(100, true)
	store.step(context.Background(), func() bool { return true }, probe)
	got = store.List()[0]
	if got.State != "ok" || got.BalanceAtomic != "1000020" || got.AvailableAtomic != "1000000" || got.TimelockedAtomic != "20" || got.PendingIncomingAtomic != "3" || got.PendingOutgoingAtomic != "4" || got.ScanHeight != 100 || got.TargetHeight != 100 || len(got.History) != 1 {
		t.Fatalf("verified wallet = %#v", got)
	}
	now = now.Add(time.Hour)
	writeTariCapability(t, store, now)
	fixture.set(true, &TariGetBalanceResponse{})
	store.step(context.Background(), func() bool { return true }, probe)
	got = store.List()[0]
	if got.State != "ok" || got.BalanceAtomic != "0" || got.AvailableAtomic != "0" || len(got.History) != 2 {
		t.Fatalf("verified zero = %#v", got)
	}
	fixture.set(false, &TariGetBalanceResponse{AvailableBalance: 9_999_999})
	store.step(context.Background(), func() bool { return true }, probe)
	got = store.List()[0]
	if got.State != "stale" || got.BalanceAtomic != "0" {
		t.Fatalf("stale wallet = %#v", got)
	}
	writeTariCapability(t, store, now)
	removed, err := store.Remove(wallet.ID)
	if err != nil || !removed || store.List()[0].State != "removing" {
		t.Fatalf("remove = %v, %v", removed, err)
	}
	removeRequest := readTariRequest(t, store)
	removeTariWatchControlFile(store.requestPath)
	if store.reconcileControl() || store.List()[0].State != "removing" {
		t.Fatal("request-to-status handoff failed the removal early")
	}
	removedData, _ := json.Marshal(tariWatchStatus{ID: removeRequest.ID, Action: "remove", WalletID: wallet.ID, State: "removed"})
	if writePrivateFileAtomic(store.statusPath, removedData) != nil {
		t.Fatal("write removed status")
	}
	store.step(context.Background(), func() bool { return true }, probe)
	if len(store.List()) != 0 || len(NewTariWatchStore(cfg).List()) != 0 {
		t.Fatal("removed wallet returned after restart")
	}
}

func TestTariWatchHTTPRejectsSecretsAndRequiresCSRF(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, time.October, 31, 12, 0, 0, 0, time.UTC)
	node := nodeConfig{ID: nodeTari, Name: "Tari", Enabled: true, Supported: true}
	cfg := config{dashboardPassword: "secret", stateDir: filepath.Join(dir, "state"), tariWalletDir: filepath.Join(dir, "wallet"), tariWalletEnabled: true, nodes: map[string]nodeConfig{nodeTari: node}}
	store := NewTariWatchStore(cfg)
	store.now = func() time.Time { return now }
	writeTariCapability(t, store, now)
	a := &app{cfg: cfg, tariWatch: store, log: log.New(io.Discard, "", 0)}
	initializeTestDashboardAuth(t, a)
	handler := a.routes()
	body := `{"name":"Cold","public_spend_key":"` + strings.Repeat("b", 64) + `","view_key":"` + strings.Repeat("a", 64) + `","birthday":"2025-01-01"}`
	request := httptest.NewRequest(http.MethodPost, "/api/tari/watch", strings.NewReader(body))
	request.SetBasicAuth("admin", "secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("missing CSRF = %d", response.Code)
	}
	request = httptest.NewRequest(http.MethodPost, "/api/tari/watch", strings.NewReader(strings.TrimSuffix(body, "}")+`,"seed":"unsafe"}`))
	request.SetBasicAuth("admin", "secret")
	request.Header.Set("X-Requested-With", "fetch")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("private seed field = %d", response.Code)
	}
	request = httptest.NewRequest(http.MethodPost, "/api/tari/watch", strings.NewReader(body))
	request.SetBasicAuth("admin", "secret")
	request.Header.Set("X-Requested-With", "fetch")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted || strings.Contains(response.Body.String(), strings.Repeat("a", 64)) {
		t.Fatalf("valid create = %d", response.Code)
	}
	request = httptest.NewRequest(http.MethodPost, "/api/tari/watch", strings.NewReader(body))
	request.SetBasicAuth("admin", "secret")
	request.Header.Set("X-Requested-With", "fetch")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusConflict {
		t.Fatalf("second wallet = %d", response.Code)
	}
	walletID := store.List()[0].ID
	request = httptest.NewRequest(http.MethodDelete, "/api/tari/watch/"+walletID, nil)
	request.SetBasicAuth("admin", "secret")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("delete missing CSRF = %d", response.Code)
	}
	now = now.Add(31 * time.Second)
	response = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodGet, "/api/tari/watch", nil)
	request.SetBasicAuth("admin", "secret")
	handler.ServeHTTP(response, request)
	var unavailable struct {
		Available bool              `json:"available"`
		Wallets   []TariWatchWallet `json:"wallets"`
	}
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &unavailable) != nil || unavailable.Available || len(unavailable.Wallets) != 1 {
		t.Fatalf("stale capability response = %d, %#v", response.Code, unavailable)
	}
}

func TestTariWatchRejectsSecretOrMalformedPersistentState(t *testing.T) {
	dir := t.TempDir()
	cfg := config{stateDir: filepath.Join(dir, "state"), tariWalletDir: filepath.Join(dir, "wallet")}
	path := filepath.Join(cfg.stateDir, "tari_watch.json")
	data := []byte(`{"wallet":{"id":"0123456789abcdef0123456789abcdef","name":"Cold","public_spend_key":"` + strings.Repeat("b", 64) + `","birthday":"","state":"pending","balance_atomic":"","available_atomic":"","timelocked_atomic":"","pending_incoming_atomic":"","pending_outgoing_atomic":"","scan_height":0,"target_height":0,"scan_time":0,"history":[],"birthday_days":0,"view_key":"unsafe"}}`)
	if writePrivateFileAtomic(path, data) != nil {
		t.Fatal("write malformed state")
	}
	if NewTariWatchStore(cfg).Err() == nil {
		t.Fatal("state containing a private key was accepted")
	}
}

func TestTariWatchRejectsMismatchedSidecarIdentityWithoutShowingZero(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, time.October, 31, 12, 0, 0, 0, time.UTC)
	cfg := config{stateDir: filepath.Join(dir, "state"), tariWalletDir: filepath.Join(dir, "wallet")}
	store := NewTariWatchStore(cfg)
	store.now = func() time.Time { return now }
	writeTariCapability(t, store, now)
	wallet, err := store.Add("Cold", strings.Repeat("b", 64), strings.Repeat("a", 64), "")
	if err != nil {
		t.Fatal(err)
	}
	request := readTariRequest(t, store)
	identityData, _ := json.Marshal(tariWatchIdentity{WalletID: strings.Repeat("0", 32), PublicSpendKey: wallet.PublicSpendKey})
	statusData, _ := json.Marshal(tariWatchStatus{ID: request.ID, Action: "create", WalletID: wallet.ID, State: "ready"})
	if writePrivateFileAtomic(store.identityPath, identityData) != nil || writePrivateFileAtomic(store.statusPath, statusData) != nil {
		t.Fatal("write mismatched identity")
	}
	store.step(context.Background(), func() bool { return true }, func(context.Context) (tariWatchNodeSnapshot, error) {
		t.Fatal("node probe called for mismatched wallet")
		return tariWatchNodeSnapshot{}, nil
	})
	got := store.List()[0]
	if got.State != "error" || got.BalanceAtomic != "" || got.ScanTime != 0 || len(got.History) != 0 {
		t.Fatalf("mismatched wallet = %#v", got)
	}
}

func TestTariWatchRetriesWalletRPCAndRejectsChangedIdentity(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, time.October, 31, 12, 0, 0, 0, time.UTC)
	fixture := &tariWatchRPCFixture{}
	fixture.set(true, &TariGetBalanceResponse{AvailableBalance: 700, TimelockedBalance: 20, PendingIncomingBalance: 30})
	fixture.failBalance(1)
	endpoint := startTariWatchRPC(t, fixture, "wallet-user", "wallet-password")
	cfg := config{stateDir: filepath.Join(dir, "state"), tariWalletDir: filepath.Join(dir, "wallet"), tariWalletURL: endpoint, tariWalletRPCUser: "wallet-user", tariWalletRPCPassword: "wallet-password"}
	store := NewTariWatchStore(cfg)
	store.now = func() time.Time { return now }
	writeTariCapability(t, store, now)
	wallet, err := store.Add("Cold", strings.Repeat("b", 64), strings.Repeat("a", 64), "")
	if err != nil {
		t.Fatal(err)
	}
	request := readTariRequest(t, store)
	identity := tariWatchIdentity{WalletID: wallet.ID, PublicSpendKey: wallet.PublicSpendKey}
	identityData, _ := json.Marshal(identity)
	statusData, _ := json.Marshal(tariWatchStatus{ID: request.ID, Action: "create", WalletID: wallet.ID, State: "ready"})
	if writePrivateFileAtomic(store.identityPath, identityData) != nil || writePrivateFileAtomic(store.statusPath, statusData) != nil {
		t.Fatal("write ready state")
	}
	probe := func(context.Context) (tariWatchNodeSnapshot, error) {
		return tariWatchNodeSnapshot{Ready: true, Height: 100, TargetHeight: 100}, nil
	}
	store.step(context.Background(), func() bool { return true }, probe)
	if got := store.List()[0]; got.State != "error" || got.BalanceAtomic != "" {
		t.Fatalf("first RPC failure = %#v", got)
	}
	store.step(context.Background(), func() bool { return true }, probe)
	got := store.List()[0]
	if got.State != "ok" || got.BalanceAtomic != "720" || got.PendingIncomingAtomic != "30" {
		t.Fatalf("recovered RPC = %#v", got)
	}

	changedIdentity, _ := json.Marshal(tariWatchIdentity{WalletID: strings.Repeat("0", 32), PublicSpendKey: wallet.PublicSpendKey})
	fixture.set(true, &TariGetBalanceResponse{AvailableBalance: 9_999})
	if writePrivateFileAtomic(store.identityPath, changedIdentity) != nil {
		t.Fatal("change identity")
	}
	store.step(context.Background(), func() bool { return true }, probe)
	got = store.List()[0]
	if got.State != "stale" || got.BalanceAtomic != "720" || got.AvailableAtomic != "700" || len(got.History) != 1 {
		t.Fatalf("changed identity overwrote verified result = %#v", got)
	}
}

func TestTariWatchFailedProvisionCanBeRemovedAndRecreated(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, time.October, 31, 12, 0, 0, 0, time.UTC)
	cfg := config{stateDir: filepath.Join(dir, "state"), tariWalletDir: filepath.Join(dir, "wallet")}
	store := NewTariWatchStore(cfg)
	store.now = func() time.Time { return now }
	writeTariCapability(t, store, now)
	wallet, err := store.Add("Cold", strings.Repeat("b", 64), strings.Repeat("a", 64), "")
	if err != nil {
		t.Fatal(err)
	}
	request := readTariRequest(t, store)
	failed, _ := json.Marshal(tariWatchStatus{ID: request.ID, Action: "create", WalletID: wallet.ID, State: "error"})
	if writePrivateFileAtomic(store.statusPath, failed) != nil {
		t.Fatal("write failed status")
	}
	store.step(context.Background(), func() bool { return true }, func(context.Context) (tariWatchNodeSnapshot, error) {
		t.Fatal("node probe called for failed provisioning request")
		return tariWatchNodeSnapshot{}, nil
	})
	got := store.List()[0]
	if got.State != "error" || got.ID != wallet.ID {
		t.Fatalf("failed request = %#v; request=%#v", got, request)
	}
	removed, err := store.Remove(wallet.ID)
	if err != nil || !removed || len(store.List()) != 0 {
		t.Fatalf("remove failed provision = %v, %v, %#v", removed, err, store.List())
	}
	replacement, err := store.Add("Replacement", strings.Repeat("c", 64), strings.Repeat("d", 64), "2026-01-01")
	if err != nil {
		t.Fatalf("replace failed provision: %v", err)
	}
	removeTariWatchControlFile(store.requestPath)
	unrelated, _ := json.Marshal(tariWatchStatus{ID: strings.Repeat("0", 32), Action: "create", WalletID: strings.Repeat("0", 32), State: "ready"})
	if writePrivateFileAtomic(store.statusPath, unrelated) != nil {
		t.Fatal("write unrelated status")
	}
	store.step(context.Background(), func() bool { return true }, func(context.Context) (tariWatchNodeSnapshot, error) {
		t.Fatal("node probe called for lost replacement request")
		return tariWatchNodeSnapshot{}, nil
	})
	if got := store.List()[0]; got.State != "pending" {
		t.Fatalf("request handoff grace was skipped = %#v", got)
	}
	now = now.Add(tariWatchControlGrace + time.Second)
	store.step(context.Background(), func() bool { return true }, func(context.Context) (tariWatchNodeSnapshot, error) {
		t.Fatal("node probe called for expired replacement request")
		return tariWatchNodeSnapshot{}, nil
	})
	got = store.List()[0]
	if got.State != "error" || got.ID != replacement.ID {
		t.Fatalf("lost request did not terminate = %#v", got)
	}
	store.mu.RLock()
	requestID := store.wallet.RequestID
	store.mu.RUnlock()
	if requestID != "" {
		t.Fatalf("lost request ID was retained: %q", requestID)
	}
}

func TestTariWatchInFlightBalanceCannotCrossWalletReplacement(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, time.October, 31, 12, 0, 0, 0, time.UTC)
	fixture := &tariWatchRPCFixture{}
	fixture.set(true, &TariGetBalanceResponse{AvailableBalance: 9_999_999})
	started, release := fixture.blockBalance()
	endpoint := startTariWatchRPC(t, fixture, "wallet-user", "wallet-password")
	cfg := config{stateDir: filepath.Join(dir, "state"), tariWalletDir: filepath.Join(dir, "wallet"), tariWalletURL: endpoint, tariWalletRPCUser: "wallet-user", tariWalletRPCPassword: "wallet-password"}
	store := NewTariWatchStore(cfg)
	store.now = func() time.Time { return now }
	writeTariCapability(t, store, now)
	first, err := store.Add("First", strings.Repeat("b", 64), strings.Repeat("a", 64), "")
	if err != nil {
		t.Fatal(err)
	}
	firstRequest := readTariRequest(t, store)
	firstIdentity, _ := json.Marshal(tariWatchIdentity{WalletID: first.ID, PublicSpendKey: first.PublicSpendKey})
	firstStatus, _ := json.Marshal(tariWatchStatus{ID: firstRequest.ID, Action: "create", WalletID: first.ID, State: "ready"})
	if writePrivateFileAtomic(store.identityPath, firstIdentity) != nil || writePrivateFileAtomic(store.statusPath, firstStatus) != nil {
		t.Fatal("write first ready state")
	}
	probe := func(context.Context) (tariWatchNodeSnapshot, error) {
		return tariWatchNodeSnapshot{Ready: true, Height: 100, TargetHeight: 100}, nil
	}
	done := make(chan struct{})
	go func() {
		store.step(context.Background(), func() bool { return true }, probe)
		close(done)
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("balance RPC did not start")
	}
	removed, err := store.Remove(first.ID)
	if err != nil || !removed {
		t.Fatalf("remove first = %v, %v", removed, err)
	}
	removeRequest := readTariRequest(t, store)
	removeTariWatchControlFile(store.requestPath)
	removedStatus, _ := json.Marshal(tariWatchStatus{ID: removeRequest.ID, Action: "remove", WalletID: first.ID, State: "removed"})
	if writePrivateFileAtomic(store.statusPath, removedStatus) != nil || store.reconcileControl() {
		t.Fatal("remove acknowledgement did not clear first wallet")
	}
	second, err := store.Add("Second", strings.Repeat("c", 64), strings.Repeat("d", 64), "")
	if err != nil {
		t.Fatal(err)
	}
	secondRequest := readTariRequest(t, store)
	secondIdentity, _ := json.Marshal(tariWatchIdentity{WalletID: second.ID, PublicSpendKey: second.PublicSpendKey})
	secondStatus, _ := json.Marshal(tariWatchStatus{ID: secondRequest.ID, Action: "create", WalletID: second.ID, State: "ready"})
	if writePrivateFileAtomic(store.identityPath, secondIdentity) != nil || writePrivateFileAtomic(store.statusPath, secondStatus) != nil || !store.reconcileControl() {
		t.Fatal("second wallet did not become ready")
	}
	close(release)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("balance RPC did not return")
	}
	got := store.List()[0]
	if got.ID != second.ID || got.State != "waiting" || got.BalanceAtomic != "" || got.ScanTime != 0 || len(got.History) != 0 {
		t.Fatalf("old RPC result crossed wallet replacement = %#v", got)
	}
}
