package main

import (
	"context"
	"io"
	"path/filepath"
	"testing"
	"time"
)

func p2pkLegacyManager(t *testing.T) *utxoManager {
	t.Helper()
	dir := t.TempDir()
	s := p2pkOpenLegacy(t, dir, false)
	f := newP2PKFixture(t, false)
	if err := s.ApplyBlock(utxoBlock{Height: 1, Hash: utxoTestHash(1), PrevHash: utxoTestHash(0), Creates: f.initialCreates()}); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	m := utxoMgrNew(t, dir, nil, newFakeUtxoNode(utxoMgrChain(2, "b")), io.Discard)
	m.set(utxoStateReady, "")
	return m
}

func TestLegacyIndexReportsAndReseedsOnce(t *testing.T) {
	m := p2pkLegacyManager(t)
	if st := m.Status(); !st.P2PKMissing || st.State != utxoStateReady {
		t.Fatalf("a ready legacy index reads %+v", st)
	}
	if stateGuard(utxoStateReady, false, true) != nil || stateGuard(utxoStateReady, false, false) == nil {
		t.Fatal("only a legacy index may be reseeded while ready")
	}
	clock := time.Unix(1_800_000_000, 0)
	m.now = func() time.Time { return clock }
	asks := 0
	m.configureSeed(utxoSeedHost{stateDir: t.TempDir(), dataDir: t.TempDir(), hold: func() bool { return true }, release: func() {},
		autoSeed: func() (string, string) { asks++; return seedTestRequestID(asks), "" }})
	m.setSeedStatePath(filepath.Join(m.host.stateDir, "utxo-seed.json"))
	m.guards = func(ctx context.Context) seedGuardInput {
		return seedGuardInput{State: utxoStateReady, Legacy: true, TempC: 50, CPUs: 0.5, FreeBytes: 1 << 40,
			Chain: blockchainInfo{Chain: "regtest", Blocks: 1, Headers: 1}, TipTime: clock, Now: clock}
	}
	m.maybeAutoSeed(t.Context())
	if asks != 1 {
		t.Fatalf("a ready legacy index asked the host %d times, want 1", asks)
	}

	if err := m.startSeed(); err != nil {
		t.Fatal(err)
	}
	st := m.Status()
	if !st.P2PKMissing || !st.Rebuilding || st.State != utxoStateReady || m.store.canon || m.build == nil || !m.build.canon {
		t.Fatalf("the legacy index must keep serving while its replacement builds: %+v canonical=%v", st, m.store.canon)
	}
}

func TestFreshIndexNeverShowsTheNotice(t *testing.T) {
	m := utxoMgrNew(t, t.TempDir(), nil, newFakeUtxoNode(utxoMgrChain(2, "b")), io.Discard)
	if m.Status().P2PKMissing {
		t.Fatal("a new index is canonical")
	}
}
