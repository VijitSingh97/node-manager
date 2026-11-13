package main

import (
	"encoding/json"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestRequestValidation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "request.json")
	valid := `{"id":"11111111111111111111111111111111","action":"create","wallet_id":"11111111111111111111111111111111","requested_at":1,"name":"Watch","public_spend_key":"` + strings.Repeat("a", 64) + `","view_key":"` + strings.Repeat("b", 64) + `","birthday_days":12}`
	if err := os.WriteFile(path, []byte(valid), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readRequest(path); err != nil {
		t.Fatalf("valid request rejected: %v", err)
	}
	for name, body := range map[string]string{
		"unknown field": strings.TrimSuffix(valid, "}") + `,"extra":true}`,
		"mismatched id": strings.Replace(valid, `"wallet_id":"11111111111111111111111111111111"`, `"wallet_id":"22222222222222222222222222222222"`, 1),
		"private name":  strings.Replace(valid, `"Watch"`, `"bad\nname"`, 1),
		"trailing json": valid + `{}`,
	} {
		t.Run(name, func(t *testing.T) {
			if err := os.WriteFile(path, []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := readRequest(path); err == nil {
				t.Fatal("invalid request accepted")
			}
		})
	}
	if err := os.WriteFile(path, make([]byte, maxRequestBytes+1), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readRequest(path); err == nil {
		t.Fatal("oversized request accepted")
	}
}

func TestChildEnvironmentKeepsKeysProvisionOnly(t *testing.T) {
	t.Setenv("TARI_WALLET_PASSWORD", "databasepassword")
	t.Setenv("MINOTARI_WALLET_VIEW_PRIVATE_KEY", "inherited-view-key")
	t.Setenv("MINOTARI_WALLET_SPEND_KEY", "inherited-spend-key")
	t.Setenv("TARI_WALLET_RPC_PASSWORD", "rpcpassword")
	env := strings.Join(childEnvironment(map[string]string{"MINOTARI_WALLET_VIEW_PRIVATE_KEY": "transient"}), "\n")
	if !strings.Contains(env, "MINOTARI_WALLET_PASSWORD=databasepassword") || !strings.Contains(env, "MINOTARI_WALLET_VIEW_PRIVATE_KEY=transient") {
		t.Fatal("required child environment is missing")
	}
	if strings.Contains(env, "inherited-view-key") || strings.Contains(env, "inherited-spend-key") || strings.Contains(env, "rpcpassword") {
		t.Fatal("launcher leaked inherited keys or RPC credentials")
	}
}

func TestWatchOnlyLifecycleUsesFakeWallet(t *testing.T) {
	root := t.TempDir()
	fake := buildFakeWallet(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	t.Setenv("TARI_WALLET_PASSWORD", "databasepassword")
	s := &supervisor{root: root, executable: fake, config: filepath.Join(root, "unused.toml"), grpcAddr: listener.Addr().String(), timeout: 3 * time.Second, poll: time.Millisecond}
	if err := s.prepare(); err != nil {
		t.Fatal(err)
	}
	name := "Watch"
	public := strings.Repeat("a", 64)
	view := strings.Repeat("b", 64)
	birthday := uint16(25)
	r := request{ID: strings.Repeat("1", 32), Action: "create", WalletID: strings.Repeat("1", 32), RequestedAt: 1, Name: &name, PublicSpendKey: &public, ViewKey: &view, BirthdayDays: &birthday}
	if err := s.create(r); err != nil {
		t.Fatalf("create failed: %v", err)
	}
	defer s.stopChild()
	if !regularFile(s.dbPath()) || !regularFile(s.identityPath()) || s.child == nil {
		t.Fatal("watch-only wallet did not reach ready state")
	}
	b, err := os.ReadFile(s.statusPath())
	if err != nil {
		t.Fatal(err)
	}
	var got status
	if json.Unmarshal(b, &got) != nil || got.State != "ready" || strings.Contains(string(b), view) {
		t.Fatal("ready status is invalid or leaked the view key")
	}
	remove := request{ID: strings.Repeat("2", 32), Action: "remove", WalletID: r.WalletID, RequestedAt: 2}
	if err := s.remove(remove); err != nil {
		t.Fatalf("remove failed: %v", err)
	}
	if pathExists(s.dbPath()) || pathExists(s.identityPath()) || !regularFile(filepath.Join(root, "private", "archive", r.WalletID, "data", "wallet.db")) {
		t.Fatal("remove did not archive the wallet privately")
	}
}

func TestRestoreFailsClosed(t *testing.T) {
	root := t.TempDir()
	s := &supervisor{root: root}
	if err := os.MkdirAll(filepath.Dir(s.dbPath()), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.dbPath(), []byte("encrypted"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.restore(); err == nil {
		t.Fatal("database without identity marker was accepted")
	}
}

func TestFailedProvisionArchiveErrorPreservesDatabaseForRetry(t *testing.T) {
	s := &supervisor{root: t.TempDir()}
	if err := s.prepare(); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(s.dataPath(), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.dbPath(), []byte("wallet"), 0600); err != nil {
		t.Fatal(err)
	}
	s.renamePath = func(string, string) error { return errors.New("injected rename failure") }
	if err := s.archiveFailed(strings.Repeat("1", 32)); err == nil || !strings.Contains(err.Error(), "move incomplete") {
		t.Fatalf("archive failure was not reported: %v", err)
	}
	if !regularFile(s.dbPath()) {
		t.Fatal("archive failure lost the active database")
	}
	s.renamePath = nil
	if err := s.archiveFailed(strings.Repeat("1", 32)); err != nil {
		t.Fatalf("archive retry failed: %v", err)
	}
	archives, err := filepath.Glob(filepath.Join(s.archiveRoot(), "failed-"+strings.Repeat("1", 32)+"-*"))
	if err != nil || len(archives) != 1 || pathExists(s.dataPath()) {
		t.Fatalf("archive retry did not preserve the incomplete database: %v", err)
	}
}

func TestRemoveRollbackRestoresDatabaseForRetry(t *testing.T) {
	s, id, r := seededRemoval(t)
	s.removePath = func(path string) error {
		if path == s.identityPath() {
			return errors.New("injected identity removal failure")
		}
		return os.Remove(path)
	}
	if err := s.remove(r); err == nil || !strings.Contains(err.Error(), "remove active wallet identity") {
		t.Fatalf("identity removal failure was not reported: %v", err)
	}
	if !regularFile(s.dbPath()) || !regularFile(s.identityPath()) || pathExists(filepath.Join(s.archiveRoot(), id.WalletID)) {
		t.Fatal("successful rollback did not restore the active wallet")
	}
	s.removePath = nil
	if err := s.remove(r); err != nil {
		t.Fatalf("removal retry failed: %v", err)
	}
}

func TestRemoveRollbackFailureKeepsArchiveRecoverable(t *testing.T) {
	s, id, r := seededRemoval(t)
	destination := filepath.Join(s.archiveRoot(), id.WalletID)
	s.removePath = func(path string) error {
		if path == s.identityPath() {
			return errors.New("injected identity removal failure")
		}
		return os.Remove(path)
	}
	s.renamePath = func(oldPath, newPath string) error {
		if oldPath == filepath.Join(destination, "data") && newPath == s.dataPath() {
			return errors.New("injected rollback failure")
		}
		return os.Rename(oldPath, newPath)
	}
	if err := s.remove(r); err == nil || !strings.Contains(err.Error(), "roll back wallet archive") {
		t.Fatalf("rollback failure was not reported: %v", err)
	}
	if !regularFile(filepath.Join(destination, "data", "wallet.db")) || !regularFile(s.identityPath()) || pathExists(s.dbPath()) {
		t.Fatal("rollback failure did not preserve the database in private archive storage")
	}
	s.removePath = nil
	s.renamePath = nil
	if err := s.remove(r); err != nil {
		t.Fatalf("removal retry did not recover the interrupted archive: %v", err)
	}
}

func TestRenderConfigAcceptsTheDefaultRPCUserAndRequiresAStrongPassword(t *testing.T) {
	for _, test := range []struct {
		user, password string
		ok             bool
	}{
		{"admin", strings.Repeat("p", 48), true},
		{"admin", "short", false},
		{"", strings.Repeat("p", 48), false},
		{"bad user", strings.Repeat("p", 48), false},
	} {
		root := t.TempDir()
		config := filepath.Join(root, "config.toml")
		if err := os.WriteFile(config, []byte(`grpc_authentication = { username = "__RPC_USER__", password = "__RPC_PASSWORD__" }`), 0600); err != nil {
			t.Fatal(err)
		}
		t.Setenv("TARI_WALLET_RPC_USER", test.user)
		t.Setenv("TARI_WALLET_RPC_PASSWORD", test.password)
		s := &supervisor{root: root, config: config}
		if err := s.renderConfig(); (err == nil) != test.ok {
			t.Fatalf("user %q password length %d: renderConfig error %v, want ok=%v", test.user, len(test.password), err, test.ok)
		}
	}
}

func TestRunFailsClosedWhenIdentityIsIncomplete(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TARI_WALLET_PASSWORD", "databasepassword")
	t.Setenv("TARI_WALLET_RPC_USER", "walletuser")
	t.Setenv("TARI_WALLET_RPC_PASSWORD", "rpcpassword")
	config := filepath.Join(root, "config.toml")
	if err := os.WriteFile(config, []byte(`grpc_authentication = { username = "__RPC_USER__", password = "__RPC_PASSWORD__" }`), 0600); err != nil {
		t.Fatal(err)
	}
	s := &supervisor{root: root, executable: filepath.Join(root, "unused"), config: config, poll: time.Millisecond}
	if err := os.WriteFile(s.identityPath(), []byte(`{"wallet_id":"11111111111111111111111111111111","public_spend_key":"`+strings.Repeat("a", 64)+`","birthday_days":0}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.run(); err == nil || !strings.Contains(err.Error(), "must both exist") {
		t.Fatalf("unsafe restore did not stop the supervisor: %v", err)
	}
	if pathExists(filepath.Join(root, "control", "capability.json")) {
		t.Fatal("failed supervisor left a healthy capability heartbeat")
	}
}

func TestRunExitsAfterUnexpectedWalletStop(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TARI_WALLET_PASSWORD", "databasepassword")
	t.Setenv("TARI_WALLET_RPC_USER", "walletuser")
	t.Setenv("TARI_WALLET_RPC_PASSWORD", "rpcpassword")
	config := filepath.Join(root, "config.toml")
	if err := os.WriteFile(config, []byte(`grpc_authentication = { username = "__RPC_USER__", password = "__RPC_PASSWORD__" }`), 0600); err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(root, "wallet")
	if err := os.WriteFile(executable, []byte("#!/bin/sh\nsleep 1\nexit 7\n"), 0700); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	s := &supervisor{root: root, executable: executable, config: config, grpcAddr: listener.Addr().String(), timeout: 3 * time.Second, poll: time.Millisecond}
	if err := s.prepare(); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(s.dataPath(), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.dbPath(), []byte("wallet"), 0600); err != nil {
		t.Fatal(err)
	}
	id := identity{WalletID: strings.Repeat("1", 32), PublicSpendKey: strings.Repeat("a", 64)}
	if err := writeJSONAtomic(s.identityPath(), id); err != nil {
		t.Fatal(err)
	}
	if err := s.run(); err == nil || !strings.Contains(err.Error(), "stopped unexpectedly") {
		t.Fatalf("unexpected child exit did not stop the supervisor: %v", err)
	}
	b, err := os.ReadFile(s.statusPath())
	if err != nil || !strings.Contains(string(b), `"state":"error"`) {
		t.Fatalf("unexpected child exit did not write error status: %v", err)
	}
}

func seededRemoval(t *testing.T) (*supervisor, identity, request) {
	t.Helper()
	s := &supervisor{root: t.TempDir()}
	if err := s.prepare(); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(s.dataPath(), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.dbPath(), []byte("wallet"), 0600); err != nil {
		t.Fatal(err)
	}
	id := identity{WalletID: strings.Repeat("1", 32), PublicSpendKey: strings.Repeat("a", 64)}
	if err := writeJSONAtomic(s.identityPath(), id); err != nil {
		t.Fatal(err)
	}
	r := request{ID: strings.Repeat("2", 32), Action: "remove", WalletID: id.WalletID, RequestedAt: 2}
	return s, id, r
}

func buildFakeWallet(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	source := `package main
import("os";"os/signal";"path/filepath";"syscall")
func main(){root:=""; for i,v:=range os.Args {if v=="--base-path" && i+1<len(os.Args){root=os.Args[i+1]}}; if root==""{os.Exit(2)}; os.MkdirAll(root,0700); os.WriteFile(filepath.Join(root,"wallet.db"),[]byte("encrypted"),0600); c:=make(chan os.Signal,1); signal.Notify(c,syscall.SIGTERM,syscall.SIGINT); <-c}
`
	sourcePath := filepath.Join(dir, "fake.go")
	if err := os.WriteFile(sourcePath, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(dir, "fake-wallet")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", binary, sourcePath)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build fake wallet: %v: %s", err, output)
	}
	return binary
}
