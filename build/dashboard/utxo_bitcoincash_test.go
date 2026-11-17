package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestBitcoinCashIndexConfigAndWiring(t *testing.T) {
	t.Setenv("BITCOINCASH_ENABLED", "1")
	t.Setenv("BITCOINCASH_RPC_USER", "u")
	t.Setenv("BITCOINCASH_RPC_PASSWORD", "p")
	t.Setenv("BITCOINCASH_UTXO_INDEX_ENABLED", "1")
	var defaults config
	if err := loadAdditionalNodes(&defaults); err != nil || defaults.nodes[nodeBitcoinCash].UTXOIndexDir != "/utxo-index-bitcoincash" {
		t.Fatalf("default index dir %q (%v)", defaults.nodes[nodeBitcoinCash].UTXOIndexDir, err)
	}
	dir := filepath.Join(t.TempDir(), "bch-index")
	t.Setenv("BITCOINCASH_UTXO_INDEX_DIR", dir)
	var cfg config
	if err := loadAdditionalNodes(&cfg); err != nil || !cfg.nodes[nodeBitcoinCash].UTXOIndex || cfg.nodes[nodeBitcoinCash].UTXOIndexDir != dir {
		t.Fatalf("bitcoin cash %+v (%v)", cfg.nodes[nodeBitcoinCash], err)
	}
	cfg.dataDir, cfg.stateDir = t.TempDir(), t.TempDir()
	a := chainTestApp(t, cfg, io.Discard)
	m := a.chainUtxo[nodeBitcoinCash]
	if m == nil || m.chain != utxoBitcoinCash || !m.currentStore().reverse || m.host.prefix != "bitcoincash-" || m.host.lock != &a.utxoSeedLock {
		t.Fatalf("bitcoin cash index wired as %+v", m)
	}
	if store := a.familyWatch.stores[nodeBitcoinCash]; store.index != m || m.onApplied == nil || store.maxWallets != 50 {
		t.Fatal("the Bitcoin Cash watch does not read the Bitcoin Cash index")
	}
}

func TestBitcoinCashSeedNowRequestsAReplayWithoutTheHostHelper(t *testing.T) {
	a, _ := testApp(t, &rpcFixture{}, "")
	m := utxoMgrNewChain(t, t.TempDir(), nil, newFakeUtxoNode(utxoMgrChain(4, "c")), io.Discard, replayTestChain())
	state := t.TempDir()
	m.configureSeed(utxoSeedHost{prefix: "bitcoincash-", stateDir: state, lock: &sync.Mutex{}, hold: func() bool { return true }, release: func() {}})
	m.temp = func() (float64, error) { return 50, nil }
	m.guards = func(context.Context) seedGuardInput {
		return seedGuardInput{State: m.Status().State, TempC: 50, CPUs: 0.5, FreeBytes: 1 << 40,
			Chain: blockchainInfo{Chain: "regtest", Blocks: 3, Headers: 3}, TipTime: time.Now(), Now: time.Now()}
	}
	a.chainUtxo = map[string]*utxoManager{nodeBitcoinCash: m}
	post := func() int {
		return hostControlHTTP(a, http.MethodPost, `{"action":"utxo_seed","node":"bitcoincash"}`, true, "192.168.1.20:4321").Code
	}

	var view hostControlView
	if got := hostControlHTTP(a, http.MethodGet, "", false, "192.168.1.20:4321"); json.Unmarshal(got.Body.Bytes(), &view) != nil || !view.UtxoSeedNowBCH || view.UtxoSeedNow {
		t.Fatalf("host view without a host helper %s", got.Body.String())
	}
	if got := post(); got != http.StatusAccepted {
		t.Fatalf("Seed now = %d", got)
	}
	if _, err := os.Stat(a.nodeControlPath("request")); !os.IsNotExist(err) {
		t.Fatal("a replay seed went through the host helper")
	}
	raw, err := os.ReadFile(filepath.Join(state, "bitcoincash-"+seedRequestName))
	if err != nil {
		t.Fatal(err)
	}
	if _, refusal := validSeedRequest(raw, time.Now()); refusal != nil {
		t.Fatalf("seed request %s refused: %+v", raw, refusal)
	}
	m.tick(t.Context())
	m.pollSeed(t.Context())
	if st := m.Status(); st.LastSeed == nil || st.LastSeed.Outcome != "ok" {
		t.Fatalf("the requested replay did not run: %+v", st)
	}
	m.tick(t.Context())
	if a.seedNow(nodeBitcoinCash) || post() != http.StatusConflict {
		t.Fatal("Seed now was offered for a ready index")
	}
	utxoMgrCheck(t, m.store, newFakeUtxoNode(utxoMgrChain(4, "c")).fakeUtxoChain, "c", 3)
}

func TestHostControlOffersTheBitcoinCashIndexWithItsArchivalSize(t *testing.T) {
	a, _ := testApp(t, &rpcFixture{}, "")
	b, _ := json.Marshal(hostControlCapability{Timestamp: time.Now().Unix(), Platform: "linux", BootKind: "boot", UtxoIndexBCH: true})
	if err := os.WriteFile(a.nodeControlPath("capability"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	var view hostControlView
	if got := hostControlHTTP(a, http.MethodGet, "", false, "192.168.1.20:4321"); json.Unmarshal(got.Body.Bytes(), &view) != nil || !view.UtxoIndexBCH || view.UtxoIndexLTC {
		t.Fatalf("host view %+v", view)
	}
	for _, node := range view.Nodes {
		if node.ID == nodeBitcoinCash && (node.DiskEstimate != "231 GiB or more" || !strings.Contains(node.DiskNote, "archival")) {
			t.Fatalf("Bitcoin Cash install estimate %q %q", node.DiskEstimate, node.DiskNote)
		}
	}
	a.cfg.nodes = map[string]nodeConfig{nodeBitcoinCash: {ID: nodeBitcoinCash, Enabled: true, Supported: true}}
	if got := hostControlHTTP(a, http.MethodPost, `{"action":"utxo_index","node":"bitcoincash","enabled":true}`, true, "192.168.1.20:4321"); got.Code != http.StatusAccepted {
		t.Fatalf("toggle = %d %s", got.Code, got.Body.String())
	}
	raw, err := os.ReadFile(a.nodeControlPath("request"))
	var req map[string]any
	if err != nil || json.Unmarshal(raw, &req) != nil || req["action"] != "utxo_index" || req["node"] != nodeBitcoinCash || req["enabled"] != true {
		t.Fatalf("request %s %v", raw, err)
	}
}
