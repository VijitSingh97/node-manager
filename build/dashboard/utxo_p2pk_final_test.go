package main

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"syscall"
	"testing"
	"time"
)

func TestRebuildBuildErrorLeavesTheLegacyIndexValid(t *testing.T) {
	r := newRebuildFixture(t)
	m := r.m
	r.node.hook = func(method string, params any) error {
		if p, ok := params.([]any); ok && method == "getblockhash" && len(p) == 1 && p[0] == uint32(0) {
			return syscall.ENOSPC
		}
		return nil
	}
	if err := m.execSeed(t.Context(), m.seedFromReplay); !errors.Is(err, syscall.ENOSPC) {
		t.Fatalf("seed returned %v", err)
	}
	r.node.hook = nil
	m.tick(t.Context())
	if st := m.Status(); st.State != utxoStateReady || !m.store.Valid() || !m.store.Legacy() {
		t.Fatalf("a failed build invalidated the legacy index: %+v", st)
	}
}

func TestRebuildSwitchFailureKeepsTheLegacyIndexServing(t *testing.T) {
	r := newRebuildFixture(t)
	m := r.m
	if err := os.MkdirAll(filepath.Join(r.dir, utxoActiveName, "x"), 0o700); err != nil {
		t.Fatal(err)
	}
	_ = m.execSeed(t.Context(), m.seedFromReplay)
	if st := m.Status(); st.State != utxoStateReady || !st.P2PKMissing || st.LastSeed == nil || st.LastSeed.Outcome != utxoOutcomeFailed {
		t.Fatalf("after the failed switch %+v", st)
	}
	if got := r.amounts(t); !slices.Equal(got, []uint64{100_000_000}) {
		t.Fatalf("the legacy index counts %v after the failed switch", got)
	}
	if names := rebuildEntries(t, r.dir); hasPrefixEntry(names, utxoRebuildPrefix) {
		t.Fatalf("the failed switch left %v", names)
	}
}

func writeSeedState(t *testing.T, st utxoSeedState) string {
	t.Helper()
	raw, _ := json.Marshal(st)
	path := filepath.Join(t.TempDir(), "seed.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRestartAfterTheSwitchKeepsTheRebuiltIndex(t *testing.T) {
	r := newRebuildFixture(t)
	if err := r.m.execSeed(t.Context(), r.m.seedFromReplay); err != nil {
		t.Fatal(err)
	}
	_ = r.m.store.Close()
	m := utxoMgrNew(t, r.dir, nil, r.node, io.Discard)
	m.setSeedStatePath(writeSeedState(t, utxoSeedState{SeedState: utxoSeedRunning, StartedAt: time.Now(), RebuildDir: filepath.Base(m.storeDir)}))
	if st := m.Status(); st.State == utxoStateNotSeeded || !m.store.canon {
		t.Fatalf("the restart wiped the rebuilt index: %+v", st)
	}
	if h, _, ok, _ := m.store.Tip(); !ok || h != 10 {
		t.Fatalf("the kept index tip is %d", h)
	}
	if m.seed.Last == nil || m.seed.Last.Outcome != utxoOutcomeOK {
		t.Fatalf("the finished rebuild was recorded as %+v", m.seed.Last)
	}
}

func TestInterruptedReseedOfARebuiltIndexCountsAsAFailure(t *testing.T) {
	r := newRebuildFixture(t)
	if err := r.m.execSeed(t.Context(), r.m.seedFromReplay); err != nil {
		t.Fatal(err)
	}
	_ = r.m.store.Close()
	m := utxoMgrNew(t, r.dir, nil, r.node, io.Discard)
	if m.storeDir == m.dir {
		t.Fatal("the rebuilt index is not in its own directory")
	}
	m.setSeedStatePath(writeSeedState(t, utxoSeedState{SeedState: utxoSeedRunning, Mode: seedModeChainstate, RequestID: "a", AutoRequestID: "a", StartedAt: time.Now()}))
	if m.seed.AutoFailures != 1 || m.seed.Last == nil || m.seed.Last.Outcome != utxoOutcomeFailed {
		t.Fatalf("the interrupted reseed was recorded as failures=%d last=%+v", m.seed.AutoFailures, m.seed.Last)
	}
}

func TestInterruptedLegacyRebuildsStopAfterTheAutoSeedTries(t *testing.T) {
	r := newRebuildFixture(t)
	_ = r.m.store.Close()
	var m *utxoManager
	for i := range utxoAutoSeedTries {
		m = utxoMgrNew(t, r.dir, nil, r.node, io.Discard)
		m.setSeedStatePath(writeSeedState(t, utxoSeedState{SeedState: utxoSeedRunning, RequestID: "a", AutoRequestID: "a", AutoFailures: i, StartedAt: time.Now()}))
		if m.seed.AutoFailures != i+1 || !m.store.Legacy() {
			t.Fatalf("restart %d: failures=%d legacy=%v", i, m.seed.AutoFailures, m.store.Legacy())
		}
		_ = m.store.Close()
	}
	if st := m.Status(); st.SeedStopped != "failed" {
		t.Fatalf("status %+v", st)
	}
}

func TestBehindLegacyReplayIndexIsWipedInPlaceNotRebuiltBeside(t *testing.T) {
	r := newRebuildFixture(t)
	m := r.m
	m.mu.Lock()
	m.nodeTip = int64(*m.indexTip) + utxoReplayGap + 5
	m.state = utxoStateCatching
	m.mu.Unlock()
	if err := m.startSeed(); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	beside, legacy := m.build != nil, m.legacy
	m.mu.Unlock()
	if beside || legacy {
		t.Fatalf("a legacy index that serves nothing was rebuilt beside: beside=%v legacy=%v", beside, legacy)
	}
}
