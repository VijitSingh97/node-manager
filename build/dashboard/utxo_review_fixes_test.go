package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func TestLegacyReplayStateResumesWhenTheHostHelperExists(t *testing.T) {
	m, _ := newBitcoinCashSeedManager(t, true, &chainstateSeedRPC{})
	logs := &bytes.Buffer{}
	m.log = log.New(logs, "", 0)
	path := filepath.Join(t.TempDir(), "bitcoincash-utxo-seed.json")
	raw, _ := json.Marshal(map[string]any{"seed_state": utxoSeedRunning, "phase": "replaying"})
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	m.setSeedStatePath(path)
	if !bytes.Contains(logs.Bytes(), []byte("resumes from its last block")) || m.seed.AutoFailures != 0 {
		t.Fatalf("a legacy replay was discarded: %q, failures %d", logs.String(), m.seed.AutoFailures)
	}
}

func TestReplayAutoSeedRefusalWaitsForTheNodeAndRechecksQuickly(t *testing.T) {
	m, _ := newBitcoinCashSeedManager(t, false, &chainstateSeedRPC{})
	clock := time.Unix(1_800_000_000, 0)
	m.now = func() time.Time { return clock }
	m.guards = func(context.Context) seedGuardInput {
		return seedGuardInput{State: utxoStateNotSeeded, TempC: 50, CPUs: 0.5, FreeBytes: 1 << 40,
			Chain: blockchainInfo{Chain: "regtest", Blocks: 1, Headers: 5, InitialBlockDownload: true}, TipTime: clock, Now: clock}
	}
	m.maybeAutoSeed(t.Context())
	if st := m.Status(); st.WaitingFor != waitNodeSync {
		t.Fatalf("a refused replay did not wait for the node: %+v", st)
	}
}

func TestChainstateSeedRefusesAnUnreadableChainstateOnAReplayChain(t *testing.T) {
	m, _ := newBitcoinCashSeedManager(t, true, &chainstateSeedRPC{})
	m.guards = func(context.Context) seedGuardInput {
		return seedGuardInput{State: utxoStateNotSeeded, TempC: 50, CPUs: 0.5, FreeBytes: 1 << 40, ChainstateErr: os.ErrNotExist,
			Chain: blockchainInfo{Chain: "regtest", Blocks: 1, Headers: 1}, TipTime: time.Now(), Now: time.Now()}
	}
	spaceRefused := func(mode string) bool {
		_, refusals := m.checkSeed(t.Context(), mode)
		return slices.ContainsFunc(refusals, func(r seedRefusal) bool { return r.Guard == "free_space" })
	}
	if !spaceRefused(seedModeChainstate) || spaceRefused(seedModeReplay) {
		t.Fatal("the chainstate size guard is skipped for a chainstate seed or wrongly applied to a replay")
	}
}

func TestAutoSeedForgetsARequestTheDashboardAdopted(t *testing.T) {
	r := newAutoSeedRig(t)
	r.m.maybeAutoSeed(t.Context())
	id := r.m.hostPendingID
	if id == "" {
		t.Fatal("no request pending")
	}
	r.m.markSeedRequest(id, utxoSeedRunning, nil)
	r.m.settleAutoHostResult()
	if r.m.hostPendingID != "" {
		t.Fatal("a started seed still holds the pending request")
	}
}
