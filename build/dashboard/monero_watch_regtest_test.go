package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"
)

const moneroRegtestForeignAddress = "44AFFq5kSiGBoZ4NMDwYtN18obc8AemS33DBLWs3H7otXft3XjrpDtQGv7SqSsaBYBb98uNbr2VBBEt7f2wfn3RVGQBEP3A"

func moneroRegtestCall(t *testing.T, rpc moneroRPC, method string, params map[string]any, out any) {
	t.Helper()
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": "regtest", "method": method, "params": params})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	response, err := rpc.do(ctx, "POST", "/json_rpc", body)
	if err != nil {
		t.Fatalf("%s: %v", method, err)
	}
	var envelope struct {
		Result json.RawMessage `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(response, &envelope); err != nil || (len(envelope.Error) > 0 && string(envelope.Error) != "null") {
		t.Fatalf("%s: %s", method, response)
	}
	t.Logf("%s reply: %.600s", method, envelope.Result)
	if out != nil {
		if err := json.Unmarshal(envelope.Result, out); err != nil {
			t.Fatalf("%s result: %v", method, err)
		}
	}
}

func TestMoneroWatchRegtest(t *testing.T) {
	if os.Getenv("MONERO_REGTEST") != "1" {
		t.Skip("set MONERO_REGTEST=1 and the MONERO_REGTEST_* URLs to run against a regtest monerod and monero-wallet-rpc")
	}
	user, password := os.Getenv("MONERO_REGTEST_USER"), os.Getenv("MONERO_REGTEST_PASSWORD")
	newRPC := func(url string) moneroRPC {
		return moneroRPC{url: url, user: user, password: password, maxBody: 64 << 20, client: newInternalHTTPClient(2 * time.Minute)}
	}
	daemon, signer := newRPC(os.Getenv("MONERO_REGTEST_DAEMON_URL")), newRPC(os.Getenv("MONERO_REGTEST_SIGNER_URL"))

	moneroRegtestCall(t, signer, "create_wallet", map[string]any{"filename": "signer", "password": "", "language": "English"}, nil)
	var address moneroWalletAddress
	moneroRegtestCall(t, signer, "get_address", map[string]any{}, &address)
	var viewKey struct {
		Key string `json:"key"`
	}
	moneroRegtestCall(t, signer, "query_key", map[string]any{"key_type": "view_key"}, &viewKey)
	const funded = 70
	moneroRegtestCall(t, daemon, "generateblocks", map[string]any{"amount_of_blocks": funded, "wallet_address": address.Address}, nil)
	moneroRegtestCall(t, signer, "refresh", map[string]any{}, nil)
	var balance moneroWalletBalance
	moneroRegtestCall(t, signer, "get_balance", map[string]any{"all_accounts": true, "strict": true}, &balance)
	if balance.Balance == 0 || balance.UnlockedBalance == 0 || balance.UnlockedBalance >= balance.Balance {
		t.Fatalf("signer balance %+v: want funded with some outputs still locked", balance)
	}
	var exported struct {
		Offset          uint64                 `json:"offset"`
		SignedKeyImages []moneroSignedKeyImage `json:"signed_key_images"`
	}
	moneroRegtestCall(t, signer, "export_key_images", map[string]any{"all": true}, &exported)
	if len(exported.SignedKeyImages) != funded {
		t.Fatalf("exported %d key images, want %d", len(exported.SignedKeyImages), funded)
	}

	store := NewMoneroWatchStore(config{
		stateDir: t.TempDir(), moneroWalletEnabled: true, moneroWalletPassword: "regtest-wallet-password",
		moneroWalletRPCURL: os.Getenv("MONERO_REGTEST_WALLET_URL"), moneroWalletRPCUser: user, moneroWalletRPCPassword: password,
		moneroRPCURL: os.Getenv("MONERO_REGTEST_DAEMON_URL"), moneroRPCUser: user, moneroRPCPassword: password,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	wallet, err := store.Add(ctx, "Regtest", address.Address, viewKey.Key, 0)
	if err != nil {
		t.Fatal(err)
	}
	current := func() MoneroWatchWallet { return store.List()[0] }
	refreshUntil := func(done func(MoneroWatchWallet) bool) MoneroWatchWallet {
		t.Helper()
		deadline := time.Now().Add(2 * time.Minute)
		for {
			store.refresh(ctx, wallet)
			if w := current(); done(w) {
				return w
			}
			if time.Now().After(deadline) {
				t.Fatalf("wallet never reached the expected state: %+v", current())
			}
			time.Sleep(2 * time.Second)
		}
	}

	ready := refreshUntil(func(w MoneroWatchWallet) bool { return w.State == "ready" })
	if ready.ReceivedAtomic != fmt.Sprint(balance.Balance) || ready.SpendingStatus != "unknown" || ready.BalanceAtomic != "" || ready.KeyImages.TotalOutputs != funded || ready.KeyImages.CoveredOutputs != 0 {
		t.Fatalf("before key images: %+v", ready)
	}
	if err := store.ImportKeyImages(ctx, wallet.ID, moneroKeyImageUpload{Offset: exported.Offset, SignedKeyImages: exported.SignedKeyImages}); err != nil {
		t.Fatal(err)
	}
	verified := refreshUntil(func(w MoneroWatchWallet) bool { return w.State == "ready" && w.SpendingStatus == "verified" })
	if verified.BalanceAtomic != fmt.Sprint(balance.Balance) || verified.UnlockedBalanceAtomic != fmt.Sprint(balance.UnlockedBalance) || !verified.KeyImages.Complete || verified.KeyImages.CoveredOutputs != funded {
		t.Fatalf("after key images: %+v", verified)
	}

	for round := 1; round <= 5; round++ {
		moneroRegtestCall(t, daemon, "generateblocks", map[string]any{"amount_of_blocks": 1, "wallet_address": moneroRegtestForeignAddress}, nil)
		store.refresh(ctx, wallet)
		after := current()
		t.Logf("round %d: state=%s scan=%d target=%d", round, after.State, after.ScanHeight, after.TargetHeight)
		if after.State != "ready" || after.BalanceAtomic != verified.BalanceAtomic || after.SpendingStatus != "verified" {
			t.Fatalf("balance blanked after the daemon mined one block between refreshes: %+v", after)
		}
	}
}
