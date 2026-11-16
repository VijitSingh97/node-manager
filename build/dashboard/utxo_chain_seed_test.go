package main

import (
	"context"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
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
	t.Setenv("LITECOIN_UTXO_INDEX_DIR", "relative")
	if err := loadAdditionalNodes(&config{}); err == nil || !strings.Contains(err.Error(), "LITECOIN_UTXO_INDEX_DIR") {
		t.Fatalf("relative index dir accepted: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc((&rpcFixture{}).handler))
	t.Cleanup(server.Close)
	cfg.rpcURL, cfg.rpcUser, cfg.rpcPassword, cfg.dashboardPassword = server.URL, "u", "p", "dashboard-secret"
	cfg.dataDir, cfg.stateDir, cfg.version = t.TempDir(), t.TempDir(), "1.0.0"
	rpc := &rpcClient{url: cfg.rpcURL, user: cfg.rpcUser, password: cfg.rpcPassword, client: &http.Client{Timeout: time.Second}}
	root := filepath.Dir(sourceFile(t))
	a, err := newApp(cfg, rpc, filepath.Join(root, "index.html"), filepath.Join(root, "static"), log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatal(err)
	}
	m := a.chainUtxo[nodeLitecoin]
	if m == nil || a.utxo != nil || len(a.chainUtxo) != 1 {
		t.Fatalf("bitcoin index %v, chain indexes %v", a.utxo, a.chainUtxo)
	}
	t.Cleanup(func() { _ = m.currentStore().Close() })
	if m.chain != utxoLitecoin || !m.currentStore().reverse || m.dir != dir || m.host.prefix != "litecoin-" ||
		m.host.dataDir != cfg.nodes[nodeLitecoin].DataDir || m.host.lock != &a.utxoSeedLock || m.seedPath != filepath.Join(cfg.stateDir, "litecoin-utxo-seed.json") {
		t.Fatalf("litecoin index wired as chain %p dir %s host %+v seed %s", m.chain, m.dir, *m.host, m.seedPath)
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
	m.suppressAlerts(time.Now().Add(time.Minute))
	if !a.monitorTransitioning(nodeLitecoin) || a.monitorTransitioning(nodeDogecoin) {
		t.Fatal("seed alert suppression is not per chain")
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := a.startBackground(ctx)
	cancel()
	<-done
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
