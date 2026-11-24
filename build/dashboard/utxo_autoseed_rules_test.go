package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type autoSeedRig struct {
	m          *utxoManager
	clock      *time.Time
	asks       *int
	hostResult *hostSeedResult
	other      *bool
	sync       *bool
}

func newAutoSeedRig(t *testing.T) autoSeedRig {
	t.Helper()
	m := utxoMgrNew(t, t.TempDir(), nil, newFakeUtxoNode(utxoMgrChain(2, "b")), io.Discard)
	clock := time.Unix(1_800_000_000, 0)
	m.now = func() time.Time { return clock }
	asks, other, syncing := 0, false, false
	var result hostSeedResult
	state := t.TempDir()
	m.configureSeed(utxoSeedHost{stateDir: state, dataDir: t.TempDir(), hold: func() bool { return true }, release: func() {},
		autoSeed:        func() (string, string) { asks++; return seedTestRequestID(asks), "" },
		autoResult:      func(id string) (hostSeedResult, bool) { return result, result.State != "" },
		otherSeedActive: func() bool { return other }})
	m.setSeedStatePath(filepath.Join(state, "utxo-seed.json"))
	m.guards = func(context.Context) seedGuardInput {
		in := seedGuardInput{State: utxoStateNotSeeded, TempC: 50, CPUs: 0.5, FreeBytes: 1 << 40,
			Chain: blockchainInfo{Chain: "regtest", Blocks: 1, Headers: 1}, TipTime: clock, Now: clock}
		if syncing {
			in.Chain.Headers, in.Chain.InitialBlockDownload = 5, true
		}
		return in
	}
	return autoSeedRig{m: m, clock: &clock, asks: &asks, hostResult: &result, other: &other, sync: &syncing}
}

func TestAutoSeedWaitsForTheNodeAndRechecksQuickly(t *testing.T) {
	r := newAutoSeedRig(t)
	*r.sync = true
	r.m.maybeAutoSeed(t.Context())
	st := r.m.Status()
	if *r.asks != 0 || st.WaitingFor != waitNodeSync || st.NodeSynced {
		t.Fatalf("syncing node: asks %d, status %+v", *r.asks, st)
	}
	*r.clock = r.clock.Add(10 * time.Second)
	r.m.maybeAutoSeed(t.Context())
	if *r.asks != 0 {
		t.Fatal("rechecked before the short cadence")
	}
	*r.sync = false
	*r.clock = r.clock.Add(utxoAutoSeedRecheck)
	r.m.maybeAutoSeed(t.Context())
	if *r.asks != 1 || r.m.Status().WaitingFor != "" {
		t.Fatalf("a synced node did not seed within a minute: asks %d, status %+v", *r.asks, r.m.Status())
	}
}

func TestAutoSeedNeverStartsBesideAnotherSeed(t *testing.T) {
	r := newAutoSeedRig(t)
	*r.other = true
	r.m.maybeAutoSeed(t.Context())
	st := r.m.Status()
	if *r.asks != 0 || st.WaitingFor != waitOtherSeed || st.AutoStoppedForTest() {
		t.Fatalf("another seed running: asks %d, status %+v", *r.asks, st)
	}
	if r.m.seed.AutoFailures != 0 {
		t.Fatal("waiting for another seed counted as a failure")
	}
	*r.other = false
	*r.clock = r.clock.Add(utxoAutoSeedRecheck)
	r.m.maybeAutoSeed(t.Context())
	if *r.asks != 1 {
		t.Fatalf("asks %d after the other seed ended", *r.asks)
	}
}

func (s utxoIndexStatus) AutoStoppedForTest() bool { return s.SeedStopped != "" }

func TestAutoSeedHostResultsCountOnlyFailures(t *testing.T) {
	r := newAutoSeedRig(t)
	r.m.maybeAutoSeed(t.Context())
	if *r.asks != 1 {
		t.Fatalf("asks %d", *r.asks)
	}
	*r.clock = r.clock.Add(time.Second)
	*r.hostResult = hostSeedResult{State: "rejected", Code: "busy", Detail: "Another balance index is seeding."}
	r.m.maybeAutoSeed(t.Context())
	st := r.m.Status()
	if st.WaitingFor != waitOtherSeed || r.m.seed.AutoFailures != 0 || len(st.Refusals) != 1 || st.Refusals[0].Detail != "Another balance index is seeding." {
		t.Fatalf("busy: failures %d, status %+v", r.m.seed.AutoFailures, st)
	}
	*r.clock = r.clock.Add(utxoAutoSeedRecheck)
	*r.hostResult = hostSeedResult{}
	r.m.maybeAutoSeed(t.Context())
	if *r.asks != 2 {
		t.Fatalf("a busy host was not retried within the short cadence: asks %d", *r.asks)
	}
	*r.hostResult = hostSeedResult{State: "rejected", Code: "recently_started", Detail: "The node started less than 10 minutes ago."}
	r.m.maybeAutoSeed(t.Context())
	if r.m.Status().WaitingFor != waitNodeStart || r.m.seed.AutoFailures != 0 {
		t.Fatalf("recently started: %+v", r.m.Status())
	}
	*r.clock = r.clock.Add(utxoAutoSeedRecheck)
	*r.hostResult = hostSeedResult{}
	r.m.maybeAutoSeed(t.Context())
	*r.hostResult = hostSeedResult{State: "failed", Code: "apply_failed", Detail: "The node's chainstate could not be copied; the node was started again."}
	r.m.maybeAutoSeed(t.Context())
	if r.m.seed.AutoFailures != 1 || r.m.seed.Last == nil || r.m.seed.Last.Outcome != "failed" {
		t.Fatalf("a failed copy was not counted: %+v", r.m.seed)
	}
	asked := *r.asks
	*r.clock = r.clock.Add(time.Hour)
	r.m.maybeAutoSeed(t.Context())
	if *r.asks != asked {
		t.Fatal("a host failure was retried within its backoff, stopping the node again")
	}
	for range 2 {
		*r.clock = r.clock.Add(48 * time.Hour)
		*r.hostResult = hostSeedResult{}
		r.m.maybeAutoSeed(t.Context())
		*r.hostResult = hostSeedResult{State: "failed", Code: "apply_failed", Detail: "again"}
		r.m.maybeAutoSeed(t.Context())
	}
	stopped := *r.asks
	if r.m.seed.AutoFailures != utxoAutoSeedTries || r.m.Status().SeedStopped != "failed" {
		t.Fatalf("failures %d, stopped %q", r.m.seed.AutoFailures, r.m.Status().SeedStopped)
	}
	*r.clock = r.clock.Add(90 * 24 * time.Hour)
	*r.hostResult = hostSeedResult{}
	r.m.maybeAutoSeed(t.Context())
	if *r.asks != stopped {
		t.Fatal("the third failure did not stop automatic seeding")
	}
}

func TestAutoSeedReseedsAnInvalidIndexButNotRightAfterAGoodSeed(t *testing.T) {
	r := newAutoSeedRig(t)
	r.m.set(utxoStateInvalid, "behind the node's pruned blocks")
	r.m.maybeAutoSeed(t.Context())
	if *r.asks != 1 {
		t.Fatalf("an invalid index was not reseeded: asks %d", *r.asks)
	}
	r.m.mu.Lock()
	r.m.hostPendingID = ""
	r.m.seed.Last = &utxoSeedResult{Outcome: "ok", FinishedAt: *r.clock}
	r.m.mu.Unlock()
	*r.clock = r.clock.Add(2 * time.Hour)
	r.m.maybeAutoSeed(t.Context())
	if *r.asks != 1 {
		t.Fatal("an index that went invalid right after a good seed was reseeded at once")
	}
	*r.clock = r.clock.Add(utxoInvalidReseedWait)
	r.m.maybeAutoSeed(t.Context())
	if *r.asks != 2 {
		t.Fatalf("asks %d a day later", *r.asks)
	}
}

func TestWaitingForNamesWhatBlocksTheSeed(t *testing.T) {
	cases := []struct {
		refusals []seedRefusal
		want     string
	}{
		{nil, ""},
		{[]seedRefusal{{"node", "node is in initial block download"}}, waitNodeSync},
		{[]seedRefusal{{"node", "the node is pruned; replaying every block needs an archival node"}}, waitBlocked},
		{[]seedRefusal{{"lock", "another balance index is seeding"}}, waitOtherSeed},
		{[]seedRefusal{{"thermal", "hottest zone 80 C is above 75 C"}}, waitHostBusy},
		{[]seedRefusal{{"thermal", "temperature sensor unreadable"}}, waitBlocked},
		{[]seedRefusal{{"cpu_cap", "cpu limit is above 0.5"}}, waitBlocked},
	}
	for _, c := range cases {
		if got := waitingFor(c.refusals); got != c.want {
			t.Fatalf("%v = %q, want %q", c.refusals, got, c.want)
		}
	}
}

func TestAnySeedActiveSeesALockedOrSeedingIndex(t *testing.T) {
	a := &app{}
	a.utxo = utxoMgrNew(t, t.TempDir(), nil, newFakeUtxoNode(utxoMgrChain(2, "b")), io.Discard)
	a.chainUtxo = map[string]*utxoManager{nodeLitecoin: utxoMgrNewChain(t, t.TempDir(), nil, newFakeUtxoNode(utxoMgrChain(2, "b")), io.Discard, utxoLitecoin)}
	if a.anySeedActive() {
		t.Fatal("an idle dashboard reported a seed")
	}
	a.utxoSeedLock.Lock()
	if !a.anySeedActive() {
		t.Fatal("a held seed lock was not seen")
	}
	a.utxoSeedLock.Unlock()
	a.chainUtxo[nodeLitecoin].mu.Lock()
	a.chainUtxo[nodeLitecoin].state = utxoStateSeeding
	a.chainUtxo[nodeLitecoin].mu.Unlock()
	if !a.anySeedActive() {
		t.Fatal("a seeding sibling was not seen")
	}
}

func TestStatusReportsNeedsHostAndAStoppedSeed(t *testing.T) {
	a, _ := testApp(t, &rpcFixture{}, "")
	a.chainUtxo = map[string]*utxoManager{nodeDogecoin: utxoMgrNewChain(t, t.TempDir(), nil, newFakeUtxoNode(utxoMgrChain(2, "b")), io.Discard, utxoDogecoin)}
	m := a.chainUtxo[nodeDogecoin]
	m.configureSeed(utxoSeedHost{stateDir: t.TempDir(), dataDir: t.TempDir(), hold: func() bool { return true }, release: func() {}, autoSeed: func() (string, string) { return "", "x" }})
	if st := m.Status(); !a.needsHost(m, st) {
		t.Fatal("a source install with no helper was not told it needs the host")
	}
	b, _ := json.Marshal(hostControlCapability{Timestamp: time.Now().Unix(), Platform: "linux", BootKind: "boot", UTXOSeedSupported: true})
	if err := os.WriteFile(a.nodeControlPath("capability"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	if st := m.Status(); a.needsHost(m, st) {
		t.Fatal("a host with the helper was told it needs the host")
	}
	m.mu.Lock()
	m.seed.AutoFailures = utxoAutoSeedTries
	m.seed.Last = &utxoSeedResult{Outcome: "failed", Reason: "the chainstate copy could not be read"}
	m.mu.Unlock()
	if got := m.Status().SeedStopped; got != "failed" {
		t.Fatalf("stopped = %q", got)
	}
	m.mu.Lock()
	m.seed.Last = &utxoSeedResult{Outcome: "cancelled"}
	m.mu.Unlock()
	if got := m.Status().SeedStopped; got != "cancelled" {
		t.Fatalf("stopped = %q", got)
	}
}

func TestHostControlInstallCarriesTheBalanceIndexOption(t *testing.T) {
	a, _ := testApp(t, &rpcFixture{}, "")
	b, _ := json.Marshal(hostControlCapability{Timestamp: time.Now().Unix(), Platform: "linux", BootKind: "boot", InstallSupported: true, InstallIndexSupported: true,
		DataPaths: map[string]string{"dogecoin": "/srv/doge", "monero": "/srv/monero"}, UtxoIndexBlocked: map[string]string{"dogecoin": "The balance index requires Dogecoin to be enabled."}})
	if err := os.WriteFile(a.nodeControlPath("capability"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	post := func(body string) (int, string) {
		w := hostControlHTTP(a, http.MethodPost, body, true, "192.168.1.20:4321")
		return w.Code, w.Body.String()
	}
	read := func() map[string]any {
		raw, err := os.ReadFile(a.nodeControlPath("request"))
		var req map[string]any
		if err != nil || json.Unmarshal(raw, &req) != nil {
			t.Fatalf("request %s %v", raw, err)
		}
		_ = os.Remove(a.nodeControlPath("request"))
		return req
	}
	const base = `"action":"install","node":"dogecoin","data_dir":"/srv/doge","interfaces":[]`
	if code, body := post(`{` + base + `,"utxo_index":false}`); code != http.StatusAccepted {
		t.Fatalf("install with the index off = %d %s", code, body)
	}
	if req := read(); req["utxo_index"] != false || req["data_dir"] != "/srv/doge" || len(req) != 7 {
		t.Fatalf("request %v", req)
	}
	if code, _ := post(`{` + base + `,"utxo_index":true}`); code != http.StatusAccepted || read()["utxo_index"] != true {
		t.Fatal("install with the index on was not queued with it")
	}
	if code, _ := post(`{` + base + `}`); code != http.StatusAccepted || func() bool { _, has := read()["utxo_index"]; return has }() {
		t.Fatal("an install without the option gained one")
	}
	if code, _ := post(`{"action":"install","node":"dogecoin"}`); code != http.StatusAccepted || len(read()) != 4 {
		t.Fatal("the legacy install form changed")
	}
	for _, body := range []string{
		`{"action":"install","node":"monero","data_dir":"/srv/monero","interfaces":[],"utxo_index":true}`,
		`{"action":"install","node":"dogecoin","utxo_index":true}`,
		`{` + base + `,"utxo_index":"yes"}`,
		`{"action":"utxo_index","node":"dogecoin","enabled":true,"utxo_index":true}`,
	} {
		if code, _ := post(body); code != http.StatusBadRequest {
			t.Fatalf("%s = %d", body, code)
		}
	}
	if validHostControlShape("install", nodeMonero, nil, "/srv/m", nil, "", ptrBool(true), "") || !validHostControlShape("install", nodeDogecoin, nil, "/srv/d", nil, "", ptrBool(false), "") {
		t.Fatal("request shape validation is wrong")
	}
}

func ptrBool(v bool) *bool { return &v }

func TestHostViewOffersTheInstallOptionAndTheIndexRefusal(t *testing.T) {
	a, _ := testApp(t, &rpcFixture{}, "")
	a.cfg.nodes = map[string]nodeConfig{nodeLitecoin: {ID: nodeLitecoin, Enabled: true, Supported: true}}
	b, _ := json.Marshal(hostControlCapability{Timestamp: time.Now().Unix(), Platform: "linux", BootKind: "boot", InstallSupported: true, InstallIndexSupported: true,
		UtxoIndexBlocked: map[string]string{"litecoin": "The balance index requires Litecoin to be enabled.", "dogecoin": "The balance index requires Dogecoin to be enabled."}})
	if err := os.WriteFile(a.nodeControlPath("capability"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	w := hostControlHTTP(a, http.MethodGet, "", false, "192.168.1.20:4321")
	if strings.Contains(w.Body.String(), "utxo_seed_now") {
		t.Fatalf("the removed seed fields are still served: %s", w.Body.String())
	}
	var view hostControlView
	if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil || !view.InstallIndex {
		t.Fatalf("view %s", w.Body.String())
	}
	got := map[string]hostControlNode{}
	for _, n := range view.Nodes {
		got[n.ID] = n
	}
	if !got[nodeBitcoin].CanIndex || !got[nodeDogecoin].CanIndex || !got[nodeBitcoinCash].CanIndex || got[nodeMonero].CanIndex || got[nodeTari].CanIndex || got[nodeZcash].CanIndex {
		t.Fatalf("can_index: %+v", got)
	}
	if got[nodeLitecoin].IndexRefusal == "" || got[nodeDogecoin].IndexRefusal != "" {
		t.Fatalf("a refusal is only shown for an installed node: litecoin %q dogecoin %q", got[nodeLitecoin].IndexRefusal, got[nodeDogecoin].IndexRefusal)
	}
	if code := hostControlHTTP(a, http.MethodPost, `{"action":"utxo_index","node":"litecoin","enabled":true}`, true, "192.168.1.20:4321"); code.Code != http.StatusConflict || !strings.Contains(code.Body.String(), "requires Litecoin") {
		t.Fatalf("a refused option was queued: %d %s", code.Code, code.Body.String())
	}
	if _, err := os.Stat(a.nodeControlPath("request")); !os.IsNotExist(err) {
		t.Fatal("a refused option still queued a request")
	}
}

func TestHostSeedResultReadsTheStatusOfTheRequestedSeed(t *testing.T) {
	a, _ := testApp(t, &rpcFixture{}, "")
	id := strings.Repeat("c", 32)
	if _, ok := a.seedHostResult(id); ok {
		t.Fatal("a missing status was reported")
	}
	raw, _ := json.Marshal(hostControlStatus{ID: id, Action: "utxo_seed", Node: "dogecoin", State: "rejected", ErrorCode: "busy", Message: "x"})
	if err := os.WriteFile(a.nodeControlPath("status"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	res, ok := a.seedHostResult(id)
	if !ok || res.State != "rejected" || res.Code != "busy" || !strings.Contains(res.Detail, "Another balance index") {
		t.Fatalf("%+v %v", res, ok)
	}
	if _, ok := a.seedHostResult(strings.Repeat("d", 32)); ok {
		t.Fatal("another request's status was taken as the answer")
	}
}
