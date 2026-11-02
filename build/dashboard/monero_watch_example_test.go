package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMoneroPublicExampleSeedsAsynchronouslyAndStaysReceivedOnly(t *testing.T) {
	fixture := &moneroWalletFixture{}
	a, handler, stateDir := newMoneroWatchTestAppWithOptions(t, fixture, true, "dashboard-secret")
	fixture.mu.Lock()
	if len(fixture.methods) != 0 || len(fixture.unauthMethods) != 0 {
		t.Fatalf("constructor reached RPC: %v %v", fixture.methods, fixture.unauthMethods)
	}
	fixture.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		a.moneroWatch.run(ctx, func() bool { return true })
		close(done)
	}()
	wallet := waitForMoneroPublicExample(t, a.moneroWatch)
	cancel()
	<-done

	if !wallet.Example || wallet.Name != moneroPublicExampleName || wallet.Address != moneroPublicExampleAddress || wallet.SourceURL != moneroPublicExampleSourceURL {
		t.Fatalf("public example = %#v", wallet)
	}
	if wallet.ReceivedAtomic != "1000000000000" || wallet.BalanceAtomic != "" || wallet.SpendingStatus != "unknown" || wallet.KeyImages.Complete || len(wallet.History) != 0 {
		t.Fatalf("public example claimed a current balance: %#v", wallet)
	}
	fixture.mu.Lock()
	generated := fixture.generated
	fixture.mu.Unlock()
	if generated["address"] != moneroPublicExampleAddress || generated["viewkey"] != moneroPublicExampleViewKey || generated["spendkey"] != nil {
		t.Fatalf("unsafe public example generation: %#v", generated)
	}
	data, err := os.ReadFile(filepath.Join(stateDir, "monero_watch.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), moneroPublicExampleViewKey) || strings.Contains(string(data), "viewkey") {
		t.Fatal("public view key was persisted in roster state")
	}

	response := request(t, handler, http.MethodGet, "/api/monero/watch", "dashboard-secret")
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"example":true`) || !strings.Contains(response.Body.String(), moneroPublicExampleSourceURL) || strings.Contains(response.Body.String(), moneroPublicExampleViewKey) {
		t.Fatalf("public example response = %d %q", response.Code, response.Body.String())
	}
	upload := moneroKeyImageUpload{SignedKeyImages: []moneroSignedKeyImage{{KeyImage: strings.Repeat("c", 64), Signature: strings.Repeat("d", 128)}}}
	if err := a.moneroWatch.ImportKeyImages(context.Background(), wallet.ID, upload); err == nil || !strings.Contains(err.Error(), "does not accept") {
		t.Fatalf("public example accepted key images: %v", err)
	}
}

func TestMoneroPublicExampleRequiresAuthenticationAndDeletionPersists(t *testing.T) {
	fixture := &moneroWalletFixture{}
	a, handler, stateDir := newMoneroWatchTestAppWithOptions(t, fixture, true, "")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		a.moneroWatch.run(ctx, func() bool { return true })
		close(done)
	}()
	wallet := waitForMoneroPublicExample(t, a.moneroWatch)
	cancel()
	<-done

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/monero/watch", nil))
	if response.Code != http.StatusUnauthorized || strings.Contains(response.Body.String(), moneroPublicExampleAddress) {
		t.Fatalf("anonymous public response = %d %q", response.Code, response.Body.String())
	}
	response = request(t, handler, http.MethodGet, "/api/monero/watch", "")
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"has_password":true`) || !strings.Contains(response.Body.String(), moneroPublicExampleAddress) {
		t.Fatalf("authenticated public response = %d %q", response.Code, response.Body.String())
	}
	mutation := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/monero/watch/"+wallet.ID, nil)
	req.Header.Set("X-Requested-With", "fetch")
	handler.ServeHTTP(mutation, req)
	if mutation.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous mutation = %d", mutation.Code)
	}

	if removed, err := a.moneroWatch.Remove(wallet.ID); err != nil || !removed {
		t.Fatalf("remove public example = %v, %v", removed, err)
	}
	restarted := NewMoneroWatchStore(config{stateDir: stateDir, moneroWalletEnabled: true, moneroPublicExample: true})
	if restarted.Err() != nil || restarted.publicExamplePending || len(restarted.List()) != 0 {
		t.Fatalf("deleted example returned after restart: %#v, %v", restarted.List(), restarted.Err())
	}
}

func TestMoneroPublicExampleMetadataIsAllowlisted(t *testing.T) {
	stateDir := t.TempDir()
	wallet := MoneroWatchWallet{ID: moneroWalletID(moneroPublicExampleAddress), Name: moneroPublicExampleName, Address: moneroPublicExampleAddress, Example: true, SourceURL: "https://example.com/", RestoreHeight: 0}
	record := moneroStoredWallet{MoneroWatchWallet: wallet, WalletFile: "dashboard_" + wallet.ID + "_" + strings.Repeat("a", 32)}
	data, err := json.Marshal(moneroWatchFile{Wallets: []moneroStoredWallet{record}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "monero_watch.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	store := NewMoneroWatchStore(config{stateDir: stateDir, moneroWalletEnabled: true, moneroPublicExample: true})
	if store.Err() == nil || len(store.List()) != 0 {
		t.Fatal("untrusted example source metadata was accepted")
	}
}

func TestMoneroPublicExampleInitializationRetriesWithoutDuplicates(t *testing.T) {
	fixture := &moneroWalletFixture{generateFails: true}
	a, _, _ := newMoneroWatchTestAppWithOptions(t, fixture, true, "dashboard-secret")
	a.moneroWatch.ensurePublicExample(context.Background())
	if len(a.moneroWatch.List()) != 0 || !a.moneroWatch.publicExamplePending {
		t.Fatal("failed initialization discarded the pending public example")
	}
	fixture.mu.Lock()
	fixture.generateFails = false
	fixture.mu.Unlock()
	a.moneroWatch.ensurePublicExample(context.Background())
	a.moneroWatch.ensurePublicExample(context.Background())
	wallets := a.moneroWatch.List()
	if len(wallets) != 1 || !wallets[0].Example || a.moneroWatch.publicExamplePending {
		t.Fatalf("retried public example = %#v", wallets)
	}
	fixture.mu.Lock()
	generateCalls := 0
	for _, method := range fixture.methods {
		if method == "generate_from_keys" {
			generateCalls++
		}
	}
	fixture.mu.Unlock()
	if generateCalls != 2 {
		t.Fatalf("generate_from_keys calls = %d", generateCalls)
	}
}

func waitForMoneroPublicExample(t *testing.T, store *MoneroWatchStore) MoneroWatchWallet {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		wallets := store.List()
		if len(wallets) == 1 && wallets[0].Example && wallets[0].ReceivedAtomic != "" {
			return wallets[0]
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("public example did not finish its initial refresh: %#v", store.List())
	return MoneroWatchWallet{}
}
