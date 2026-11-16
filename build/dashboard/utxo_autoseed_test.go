package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func TestUtxoAutoSeedAsksTheHostAtMostHourly(t *testing.T) {
	m := utxoMgrNew(t, t.TempDir(), nil, newFakeUtxoNode(utxoMgrChain(2, "b")), io.Discard)
	clock := time.Unix(1_800_000_000, 0)
	m.now = func() time.Time { return clock }
	asks, answer := 0, ""
	m.configureSeed(utxoSeedHost{stateDir: t.TempDir(), dataDir: t.TempDir(), hold: func() bool { return true }, release: func() {},
		autoSeed: func() (string, string) { asks++; return seedTestRequestID(asks), answer }})
	m.setSeedStatePath(filepath.Join(m.host.stateDir, "utxo-seed.json"))
	synced := func(context.Context) seedGuardInput {
		return seedGuardInput{State: utxoStateNotSeeded, TempC: 50, CPUs: 0.5, FreeBytes: 1 << 40,
			Chain: blockchainInfo{Chain: "regtest", Blocks: 1, Headers: 1}, TipTime: clock, Now: clock}
	}
	m.guards = synced
	guardsOf := func() []string {
		var out []string
		for _, r := range m.Status().Refusals {
			out = append(out, r.Guard)
		}
		return out
	}

	m.maybeAutoSeed(t.Context())
	m.maybeAutoSeed(t.Context())
	if asks != 1 {
		t.Fatalf("asked the host %d times within the hour, want 1", asks)
	}
	clock = clock.Add(time.Hour)
	answer = "another host change is pending"
	m.maybeAutoSeed(t.Context())
	if asks != 2 || !slices.Equal(guardsOf(), []string{"host"}) {
		t.Fatalf("asks %d, refusals %v", asks, m.Status().Refusals)
	}

	clock = clock.Add(time.Hour)
	m.guards = func(ctx context.Context) seedGuardInput {
		in := synced(ctx)
		in.Chain.Headers, in.Chain.InitialBlockDownload = 5, true
		return in
	}
	m.maybeAutoSeed(t.Context())
	if asks != 2 || !slices.Contains(guardsOf(), "node") {
		t.Fatalf("a syncing node was seeded: asks %d, refusals %v", asks, m.Status().Refusals)
	}

	clock = clock.Add(time.Hour)
	m.guards = synced
	m.set(utxoStateReady, "")
	m.maybeAutoSeed(t.Context())
	if asks != 2 {
		t.Fatal("a seeded index asked for another seed")
	}
}

func TestQueueSeedHostActionWritesOneRequest(t *testing.T) {
	a := &app{cfg: config{stateDir: t.TempDir()}}
	capability := func(extra string) {
		raw := fmt.Sprintf(`{"timestamp":%d,"platform":"linux","boot_kind":"boot","install_supported":true,"boot_supported":true,"boot_enabled":true,"clearnet_over_tor":false,"inbound_connections":false,"utxo_index":true,"config_revision":""%s}`, time.Now().Unix(), extra)
		if err := os.WriteFile(a.nodeControlPath("capability"), []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	capability(`,"config_pending":false`)
	if _, got := a.queueSeedHostAction(nodeLitecoin); got == "" {
		t.Fatal("queued a seed on a host helper that does not support it")
	}
	capability(`,"config_pending":false,"utxo_seed_supported":true`)
	id, got := a.queueSeedHostAction(nodeLitecoin)
	if got != "" {
		t.Fatalf("refused: %s", got)
	}
	raw, err := os.ReadFile(a.nodeControlPath("request"))
	if err != nil {
		t.Fatal(err)
	}
	var req map[string]any
	if err := json.Unmarshal(raw, &req); err != nil || len(req) != 4 || req["action"] != "utxo_seed" || req["node"] != nodeLitecoin || req["id"] != id || !validPortControlID(id) {
		t.Fatalf("request %s", raw)
	}
	if _, got := a.queueSeedHostAction(nodeBitcoin); got != "another host change is pending" {
		t.Fatalf("second request: %q", got)
	}
	if canonicalHostStatusMessage(hostControlStatus{State: "applied", Action: "utxo_seed"}) != "Balance index seed started." {
		t.Fatal("applied seed status has no message")
	}
}

func TestUtxoAutoSeedBacksOffAfterFailedSeeds(t *testing.T) {
	m := utxoMgrNew(t, t.TempDir(), nil, newFakeUtxoNode(utxoMgrChain(2, "b")), io.Discard)
	clock := time.Unix(1_800_000_000, 0)
	m.now = func() time.Time { return clock }
	state := t.TempDir()
	var asked []string
	m.configureSeed(utxoSeedHost{stateDir: state, dataDir: t.TempDir(), hold: func() bool { return true }, release: func() {},
		autoSeed: func() (string, string) {
			asked = append(asked, seedTestRequestID(len(asked)+1))
			return asked[len(asked)-1], ""
		}})
	m.setSeedStatePath(filepath.Join(state, "utxo-seed.json"))
	m.guards = func(context.Context) seedGuardInput {
		return seedGuardInput{State: utxoStateNotSeeded, TempC: 50, CPUs: 0.5, FreeBytes: 1 << 40,
			Chain: blockchainInfo{Chain: "regtest", Blocks: 1, Headers: 1}, TipTime: clock, Now: clock}
	}
	failLastAsk := func(err error) {
		t.Helper()
		m.markSeedRequest(asked[len(asked)-1], utxoSeedRunning, nil)
		m.finishSeed(err)
	}
	askAfter := func(wait time.Duration, want int) {
		t.Helper()
		clock = clock.Add(wait)
		m.maybeAutoSeed(t.Context())
		if len(asked) != want {
			t.Fatalf("after %s: asked %d times, want %d (failures %d)", wait, len(asked), want, m.seed.AutoFailures)
		}
	}
	askAfter(0, 1)
	failLastAsk(errSeedDump)
	askAfter(time.Hour, 1)
	askAfter(5*time.Hour, 2)
	failLastAsk(context.Canceled)
	askAfter(23*time.Hour, 2)
	askAfter(time.Hour, 3)
	failLastAsk(errSeedThermal)
	askAfter(30*24*time.Hour, 3)

	raw, _ := json.Marshal(seedRequest{ID: seedTestRequestID(99), RequestedAt: clock.Unix()})
	if err := os.WriteFile(filepath.Join(state, seedRequestName), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	m.pollSeed(t.Context())
	if m.seed.AutoFailures != 0 {
		t.Fatalf("a user's seed request left %d automatic failures", m.seed.AutoFailures)
	}
	askAfter(time.Hour, 4)

	m.markSeedRequest(asked[3], utxoSeedRunning, nil)
	restarted := utxoMgrNew(t, t.TempDir(), nil, newFakeUtxoNode(utxoMgrChain(2, "b")), io.Discard)
	restarted.setSeedStatePath(filepath.Join(state, "utxo-seed.json"))
	if restarted.seed.AutoFailures != 1 {
		t.Fatalf("an automatic seed cut short by a restart counted %d failures, want 1", restarted.seed.AutoFailures)
	}
}

func TestSeedNowQueuesOnlyWhenTheIndexNeedsASeed(t *testing.T) {
	a, _ := testApp(t, &rpcFixture{}, "")
	capability := func(supported bool) {
		b, _ := json.Marshal(hostControlCapability{Timestamp: time.Now().Unix(), Platform: "linux", BootKind: "boot", UTXOSeedSupported: supported})
		if err := os.WriteFile(a.nodeControlPath("capability"), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	post := func(body string) int {
		return hostControlHTTP(a, http.MethodPost, body, true, "192.168.1.20:4321").Code
	}
	const seed = `{"action":"utxo_seed","node":"bitcoin"}`
	capability(true)
	if a.seedNow(nodeBitcoin) || post(seed) != http.StatusConflict {
		t.Fatal("Seed now was offered without an index")
	}
	a.utxo = utxoMgrNew(t, t.TempDir(), nil, newFakeUtxoNode(utxoMgrChain(2, "b")), io.Discard)
	capability(false)
	if a.seedNow(nodeBitcoin) || post(seed) != http.StatusConflict {
		t.Fatal("Seed now was offered by a host helper that cannot prepare a seed")
	}
	capability(true)
	var view hostControlView
	if got := hostControlHTTP(a, http.MethodGet, "", false, "192.168.1.20:4321"); json.Unmarshal(got.Body.Bytes(), &view) != nil || !view.UtxoSeedNow {
		t.Fatalf("host view %s", got.Body.String())
	}
	for _, body := range []string{`{"action":"utxo_seed"}`, `{"action":"utxo_seed","node":"dogecoin"}`, `{"action":"utxo_seed","node":"bitcoin","enabled":true}`} {
		if got := post(body); got != http.StatusBadRequest {
			t.Fatalf("%s = %d", body, got)
		}
	}
	if got := post(seed); got != http.StatusAccepted {
		t.Fatalf("Seed now = %d", got)
	}
	raw, err := os.ReadFile(a.nodeControlPath("request"))
	var req map[string]any
	if err != nil || json.Unmarshal(raw, &req) != nil || req["action"] != "utxo_seed" || req["node"] != nodeBitcoin || len(req) != 4 {
		t.Fatalf("request %s %v", raw, err)
	}
	if err := os.Remove(a.nodeControlPath("request")); err != nil {
		t.Fatal(err)
	}
	a.utxo.set(utxoStateReady, "")
	if a.seedNow(nodeBitcoin) || post(seed) != http.StatusConflict {
		t.Fatal("Seed now was offered for a ready index")
	}
}
