package main

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestLitecoinIndexConfigAndWiring(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ltc-index")
	t.Setenv("LITECOIN_ENABLED", "1")
	t.Setenv("LITECOIN_RPC_USER", "u")
	t.Setenv("LITECOIN_RPC_PASSWORD", "p")
	t.Setenv("LITECOIN_UTXO_INDEX_ENABLED", "1")
	t.Setenv("LITECOIN_UTXO_INDEX_DIR", dir)
	t.Setenv("ZCASH_UTXO_INDEX_ENABLED", "1")
	var cfg config
	if err := loadAdditionalNodes(&cfg); err != nil {
		t.Fatal(err)
	}
	if ltc := cfg.nodes[nodeLitecoin]; !ltc.UTXOIndex || ltc.UTXOIndexDir != dir || cfg.nodes[nodeZcash].UTXOIndex {
		t.Fatalf("litecoin %+v, zcash index %v", ltc, cfg.nodes[nodeZcash].UTXOIndex)
	}
	for value, want := range map[string]bool{"no": false, "off": false, "FALSE": false, "0": false, "Yes": true, "ON": true} {
		t.Setenv("LITECOIN_UTXO_INDEX_ENABLED", value)
		var c config
		if err := loadAdditionalNodes(&c); err != nil || c.nodes[nodeLitecoin].UTXOIndex != want {
			t.Fatalf("LITECOIN_UTXO_INDEX_ENABLED=%s gave %v, want %v (%v)", value, c.nodes[nodeLitecoin].UTXOIndex, want, err)
		}
	}
	t.Setenv("LITECOIN_UTXO_INDEX_ENABLED", "1")
	t.Setenv("LITECOIN_UTXO_INDEX_DIR", "relative")
	if err := loadAdditionalNodes(&config{}); err == nil || !strings.Contains(err.Error(), "LITECOIN_UTXO_INDEX_DIR") {
		t.Fatalf("relative index dir accepted: %v", err)
	}

	cfg.dataDir, cfg.stateDir = t.TempDir(), t.TempDir()
	cfg.utxoIndexEnabled, cfg.utxoIndexDir = true, filepath.Join(t.TempDir(), "btc-index")
	a := chainTestApp(t, cfg, io.Discard)
	m := a.chainUtxo[nodeLitecoin]
	if m == nil || a.utxo == nil || len(a.chainUtxo) != 1 {
		t.Fatalf("bitcoin index %v, chain indexes %v", a.utxo, a.chainUtxo)
	}
	if m.chain != utxoLitecoin || !m.currentStore().reverse || m.dir != dir || m.host.prefix != "litecoin-" ||
		m.host.dataDir != cfg.nodes[nodeLitecoin].DataDir || m.host.lock != &a.utxoSeedLock || m.seedPath != filepath.Join(cfg.stateDir, "litecoin-utxo-seed.json") {
		t.Fatalf("litecoin index wired as chain %p dir %s host %+v seed %s", m.chain, m.dir, *m.host, m.seedPath)
	}
	if store := a.familyWatch.stores[nodeLitecoin]; store.index != m || m.onApplied == nil {
		t.Fatal("the Litecoin watch does not read the Litecoin index")
	} else {
		m.onApplied()
		select {
		case <-store.trigger:
		default:
			t.Fatal("a new indexed block did not wake the Litecoin watch")
		}
	}
	scan := &a.familyWatch.stores[nodeLitecoin].scanMu
	if !m.host.hold() || scan.TryLock() {
		t.Fatal("the seed hold does not stop the Litecoin watch scan")
	}
	m.host.release()
	if !scan.TryLock() || m.host.hold() {
		t.Fatal("a running Litecoin watch scan does not block the seed hold")
	}
	scan.Unlock()

	if a.utxo.host.lock != &a.utxoSeedLock || m.host.lock != a.utxo.host.lock {
		t.Fatal("the Bitcoin and Litecoin seeds do not share one lock")
	}
	a.utxo.guards = func(context.Context) seedGuardInput {
		now := time.Now()
		return seedGuardInput{State: utxoStateNotSeeded, TempC: 50, CPUs: 0.5, FreeBytes: 1 << 40,
			Chain: blockchainInfo{Chain: "regtest", Blocks: 1, Headers: 1}, TipTime: now, Now: now}
	}
	if err := syscall.Mkfifo(filepath.Join(cfg.dataDir, seedIncompleteName), 0o600); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(seedRequest{ID: seedTestRequestID(12), RequestedAt: time.Now().Unix()})
	if err := os.WriteFile(filepath.Join(cfg.stateDir, seedRequestName), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	a.utxoSeedLock.Lock()
	a.utxo.pollSeed(t.Context())
	a.utxoSeedLock.Unlock()
	if r := a.utxo.Status().Refusals; len(r) != 1 || r[0].Guard != "lock" {
		t.Fatalf("a Bitcoin seed while another index seeds was not refused by the lock: %+v", r)
	}

	m.suppressAlerts(time.Now().Add(time.Minute))
	if !a.monitorTransitioning(nodeLitecoin) || a.monitorTransitioning(nodeDogecoin) {
		t.Fatal("seed alert suppression is not per chain")
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := a.startBackground(ctx)
	cancel()
	<-done
}

func chainTestApp(t *testing.T, cfg config, logs io.Writer) *app {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc((&rpcFixture{}).handler))
	t.Cleanup(server.Close)
	cfg.rpcURL, cfg.rpcUser, cfg.rpcPassword, cfg.dashboardPassword, cfg.version = server.URL, "u", "p", "dashboard-secret", "1.0.0"
	rpc := &rpcClient{url: cfg.rpcURL, user: cfg.rpcUser, password: cfg.rpcPassword, client: &http.Client{Timeout: time.Second}}
	root := filepath.Dir(sourceFile(t))
	a, err := newApp(cfg, rpc, filepath.Join(root, "index.html"), filepath.Join(root, "static"), log.New(logs, "", 0))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if a.utxo != nil {
			_ = a.utxo.currentStore().Close()
		}
		for _, m := range a.chainUtxo {
			_ = m.currentStore().Close()
		}
	})
	return a
}

func TestChainIndexRefusesADirectoryThatOverlapsAnother(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{{"/a", "/a", true}, {"/a/b", "/a", true}, {"/a", "/a/b/", true}, {"/", "/x", true}, {"/utxo-index", "/utxo-index-litecoin", false}, {"/a/b", "/a/c", false}} {
		if pathsOverlap(c.a, c.b) != c.want {
			t.Errorf("pathsOverlap(%q, %q) = %v", c.a, c.b, !c.want)
		}
	}
	t.Setenv("LITECOIN_ENABLED", "1")
	t.Setenv("LITECOIN_RPC_USER", "u")
	t.Setenv("LITECOIN_RPC_PASSWORD", "p")
	t.Setenv("LITECOIN_UTXO_INDEX_ENABLED", "1")
	parent := t.TempDir()
	t.Setenv("LITECOIN_DATA_DIR", filepath.Join(parent, "ltc-data"))
	state, data, btc := t.TempDir(), t.TempDir(), t.TempDir()
	for name, dir := range map[string]string{
		"the Bitcoin index":      btc,
		"inside the state dir":   filepath.Join(state, "ltc-index"),
		"the Bitcoin data dir":   data,
		"around the node's data": parent,
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("LITECOIN_UTXO_INDEX_DIR", dir)
			var cfg config
			if err := loadAdditionalNodes(&cfg); err != nil {
				t.Fatal(err)
			}
			cfg.stateDir, cfg.dataDir, cfg.utxoIndexEnabled, cfg.utxoIndexDir = state, data, true, btc
			var logs strings.Builder
			a := chainTestApp(t, cfg, &logs)
			if a.chainUtxo[nodeLitecoin] != nil || a.utxo == nil || !strings.Contains(logs.String(), "Litecoin balance index disabled: "+dir+" overlaps") {
				t.Fatalf("litecoin index %v, bitcoin index %v, logs %q", a.chainUtxo[nodeLitecoin], a.utxo, logs.String())
			}
		})
	}
}

func TestLitecoinSeedThroughTheFIFO(t *testing.T) {
	raw, info, base := litecoinFixture(t)
	core := newFakeCore(t)
	core.data, core.args = raw, []any{seedDumpName}
	core.res = seedDumpResult{CoinsWritten: info.TxOuts, BaseHash: info.BestBlock, BaseHeight: info.Height}
	fx := newChainSeedFixture(t, core, utxoLitecoin, "litecoin-")
	fx.request(t, seedTestRequestID(9), time.Now())
	fx.m.pollSeed(t.Context())
	if st := fx.m.Status(); st.LastSeed == nil || st.LastSeed.Outcome != "ok" {
		t.Fatalf("status %+v, last %+v", st, st.LastSeed)
	}
	if h, hash, ok, err := fx.m.currentStore().Tip(); err != nil || !ok || h != info.Height || hash != base {
		t.Fatalf("tip %d %x %v %v", h, hash, ok, err)
	}
	if keys := utxoTestKeys(t, fx.m.currentStore()); len(keys) != 2*449 {
		t.Fatalf("%d records, want a coin and a reverse key for each of 449 coins", len(keys))
	}
	if fx.holds.Load() != 1 || fx.release.Load() != 1 || fx.persisted(t)["seed_state"] != "ok" {
		t.Fatalf("hold %d release %d persisted %v", fx.holds.Load(), fx.release.Load(), fx.persisted(t))
	}
}

func TestSeedLockKeepsTwoIndexesFromSeedingAtOnce(t *testing.T) {
	core := newFakeCore(t)
	fx := newSeedFixture(t, core)
	lock := &sync.Mutex{}
	fx.m.host.lock = lock
	lock.Lock()
	fx.request(t, seedTestRequestID(10), time.Now())
	fx.m.pollSeed(t.Context())
	refusals, _ := fx.persisted(t)["refusals"].([]any)
	if len(refusals) != 1 || refusals[0].(map[string]any)["guard"] != "lock" || fx.holds.Load() != 0 || core.calls != 0 {
		t.Fatalf("refusals %v, holds %d, dumps %d", refusals, fx.holds.Load(), core.calls)
	}
	lock.Unlock()
	fx.request(t, seedTestRequestID(11), time.Now())
	fx.m.pollSeed(t.Context())
	if st := fx.m.Status(); st.LastSeed == nil || st.LastSeed.Outcome != "ok" || !lock.TryLock() {
		t.Fatalf("seed after the lock freed: %+v", st.LastSeed)
	}
}
