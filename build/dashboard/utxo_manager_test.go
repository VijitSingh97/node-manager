package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/cockroachdb/pebble"
	"github.com/cockroachdb/pebble/vfs"
	"github.com/cockroachdb/pebble/vfs/errorfs"
)

type fakeUtxoNode struct {
	*fakeUtxoChain
	chain       string
	pruned      bool
	pruneHeight int64
	hook        func(method string, params any) error
}

func (n *fakeUtxoNode) call(ctx context.Context, method string, params any, out any) error {
	if n.hook != nil {
		if err := n.hook(method, params); err != nil {
			return err
		}
	}
	if method == "getblockchaininfo" {
		raw, _ := json.Marshal(map[string]any{"chain": n.chain, "blocks": len(n.hashes) - 1, "pruned": n.pruned, "pruneheight": n.pruneHeight})
		return json.Unmarshal(raw, out)
	}
	return n.fakeUtxoChain.call(ctx, method, params, out)
}

func newFakeUtxoNode(c *fakeUtxoChain) *fakeUtxoNode {
	return &fakeUtxoNode{fakeUtxoChain: c, chain: "regtest"}
}

func utxoMgrScript(h, v int) string { return fmt.Sprintf("0014%02xaabb", (h+v)%7) }

func utxoMgrAddBlock(c *fakeUtxoChain, label string, h int) {
	var outs []fakeOut
	for v := 0; v < 3; v++ {
		outs = append(outs, fakeOut{utxoMgrScript(h, v), fmt.Sprintf("0.%08d", h*10+v)})
	}
	var ins []fakeIn
	if h >= 2 {
		ins = []fakeIn{{fakeDisplayHash("tx"+label, h-1), 0, utxoMgrScript(h-1, 0)}}
	}
	c.add(label, outs, ins)
}

func utxoMgrChain(n int, label string) *fakeUtxoChain {
	c := newFakeUtxoChain()
	genesis(c)
	for h := 1; h < n; h++ {
		utxoMgrAddBlock(c, label, h)
	}
	return c
}

func utxoMgrCheck(t *testing.T, s *utxoStore, c *fakeUtxoChain, label string, tip uint32) {
	t.Helper()
	h, hash, ok, err := s.Tip()
	if err != nil || !ok || h != tip {
		t.Fatalf("tip = %d %v %v, want %d", h, ok, err, tip)
	}
	if want, _ := utxoRevHex(c.hashes[tip]); hash != want {
		t.Fatalf("tip hash mismatch at %d", tip)
	}
	want := map[string]uint64{}
	for hh := 1; hh <= int(tip); hh++ {
		for v := 0; v < 3; v++ {
			if v == 0 && hh < int(tip) {
				continue
			}
			want[fmt.Sprintf("%s:%d", fakeDisplayHash("tx"+label, hh), v)] = uint64(hh*10 + v)
		}
	}
	got := map[string]uint64{}
	for i := 0; i < 7; i++ {
		for k, v := range lookupAmounts(t, s, fmt.Sprintf("0014%02xaabb", i)) {
			got[k] = v
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("coins do not match tip %d: got %d, want %d", tip, len(got), len(want))
	}
}

func utxoMgrSeed(t testing.TB, dir string, fs vfs.FS, c *fakeUtxoChain) {
	t.Helper()
	s, err := openUtxoStoreFS(dir, fs, false)
	if err != nil {
		t.Fatal(err)
	}
	hash, _ := utxoRevHex(c.hashes[0])
	if err := s.ApplyBlock(utxoBlock{Hash: hash}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
}

func utxoMgrNew(t testing.TB, dir string, fs vfs.FS, node utxoRPC, logs io.Writer) *utxoManager {
	t.Helper()
	return utxoMgrNewChain(t, dir, fs, node, logs, utxoBitcoin)
}

func utxoMgrNewChain(t testing.TB, dir string, fs vfs.FS, node utxoRPC, logs io.Writer, chain *utxoChainParams) *utxoManager {
	t.Helper()
	m := newUtxoManager(dir, fs, node, log.New(logs, "", 0), chain)
	m.freeBytes = func() uint64 { return 1 << 40 }
	t.Cleanup(func() { _ = m.store.Close() })
	return m
}

func utxoMgrState(m *utxoManager) string { return m.Status().State }

func TestUtxoSeedPhaseChangeIsLoggedOnce(t *testing.T) {
	logs := &bytes.Buffer{}
	m := utxoMgrNew(t, t.TempDir(), nil, newFakeUtxoNode(utxoMgrChain(10, "b")), logs)
	if err := m.startSeed(); err != nil {
		t.Fatal(err)
	}
	m.setSeedPhase(utxoPhaseImporting)
	m.setSeedPhase(utxoPhaseImporting)
	m.setSeedPhase(utxoPhaseMerging)
	if got := strings.Count(logs.String(), "utxo index: seed phase=importing\n"); got != 1 {
		t.Fatalf("importing logged %d times: %q", got, logs.String())
	}
	if !strings.Contains(logs.String(), "utxo index: seed phase=merging\n") {
		t.Fatalf("merging not logged: %q", logs.String())
	}
}

func TestUtxoManagerStateMachine(t *testing.T) {
	ctx := context.Background()
	c := utxoMgrChain(10, "b")
	node := newFakeUtxoNode(c)
	var logs bytes.Buffer

	empty := utxoMgrNew(t, t.TempDir(), nil, node, io.Discard)
	empty.tick(ctx)
	if st := empty.Status(); st.State != utxoStateNotSeeded || st.IndexTip != nil || st.SeedProgress != nil {
		t.Fatalf("empty store: %+v", st)
	}
	if c.calls["getblock"] != 0 {
		t.Fatal("an unseeded index must not follow the chain")
	}

	dir := t.TempDir()
	utxoMgrSeed(t, dir, nil, c)
	var seenDuring []string
	m := utxoMgrNew(t, dir, nil, node, &logs)
	node.hook = func(method string, params any) error {
		if method == "getblock" && params.([]any)[0] == c.hashes[5] {
			st := m.Status()
			seenDuring = append(seenDuring, fmt.Sprintf("%s@%d", st.State, *st.IndexTip))
		}
		return nil
	}
	m.tick(ctx)
	if !reflect.DeepEqual(seenDuring, []string{"catching_up@4"}) {
		t.Fatalf("state while behind: %v", seenDuring)
	}
	st := m.Status()
	if st.State != utxoStateReady || *st.IndexTip != 9 || *st.NodeTip != 9 || *st.Behind != 0 || st.Reason != "" {
		t.Fatalf("after catch-up: %+v", st)
	}
	utxoMgrCheck(t, m.store, c, "b", 9)

	for h := 10; h < 12; h++ {
		utxoMgrAddBlock(c, "b", h)
	}
	m.freeBytes = func() uint64 { return utxoMinFreeBytes - 1 }
	m.tick(ctx)
	if st := m.Status(); st.State != utxoStateDegraded || *st.IndexTip != 9 || *st.Behind != 2 || st.Reason == "" {
		t.Fatalf("low disk: %+v", st)
	}
	m.freeBytes = func() uint64 { return utxoMinFreeBytes }
	m.tick(ctx)
	if st := m.Status(); st.State != utxoStateReady || *st.IndexTip != 11 {
		t.Fatalf("after space returned: %+v", st)
	}
	utxoMgrCheck(t, m.store, c, "b", 11)

	node.hook = func(method string, _ any) error { return errors.New("no rpc while seeding: " + method) }
	m.set(utxoStateNotSeeded, "")
	if err := m.startSeed(); err != nil {
		t.Fatal(err)
	}
	m.setSeedProgress(0.25)
	m.tick(ctx)
	if st := m.Status(); st.State != utxoStateSeeding || st.SeedProgress == nil || *st.SeedProgress != 0.25 {
		t.Fatalf("seeding: %+v", st)
	}
	m.finishSeed(nil)
	if st := m.Status(); st.State != utxoStateCatching || st.SeedProgress != nil {
		t.Fatalf("after seeding: %+v", st)
	}
	node.hook = nil

	node.hook = func(method string, _ any) error {
		if method == "getblockchaininfo" {
			return errors.New("node down")
		}
		return nil
	}
	m.tick(ctx)
	node.hook = nil
	m.tick(ctx)
	if utxoMgrState(m) != utxoStateReady {
		t.Fatalf("state after a node outage: %s", utxoMgrState(m))
	}

	for _, s := range []string{"state=catching_up", "state=degraded", "state=ready", "state=seeding"} {
		if strings.Count(logs.String(), s) < 1 {
			t.Fatalf("transition %s not logged:\n%s", s, logs.String())
		}
	}
	if n := strings.Count(logs.String(), "state=ready"); n != 3 {
		t.Fatalf("ready logged %d times, want one per transition into it (3):\n%s", n, logs.String())
	}
	if strings.Contains(logs.String(), utxoMgrScript(1, 0)) {
		t.Fatal("logs contain script data")
	}
}

func TestUtxoSeedProgressMilestonesLoggedOncePerStep(t *testing.T) {
	logs := &bytes.Buffer{}
	m := utxoMgrNew(t, t.TempDir(), nil, newFakeUtxoNode(utxoMgrChain(10, "b")), logs)
	seed := func() {
		if err := m.startSeed(); err != nil {
			t.Fatal(err)
		}
		for _, f := range []float64{0.05, 0.12, 0.15, 0.31, 0.31, 1.0} {
			m.setSeedProgress(f)
		}
	}
	seed()
	want := "utxo index: state=not_seeded\nutxo index: state=seeding\nutxo index: seed progress=10%\nutxo index: seed progress=30%\nutxo index: seed progress=100%\n"
	if logs.String() != want {
		t.Fatalf("log %q, want %q", logs.String(), want)
	}
	m.finishSeed(context.Canceled)
	logs.Reset()
	m.set(utxoStateNotSeeded, "")
	seed()
	if got := strings.Count(logs.String(), "seed progress="); got != 3 {
		t.Fatalf("second seed logged %d milestones: %q", got, logs.String())
	}
}

func TestUtxoFollowingLogsOnlyAtMilestoneWithoutScriptData(t *testing.T) {
	logs := &bytes.Buffer{}
	m := utxoMgrNew(t, t.TempDir(), nil, newFakeUtxoNode(utxoMgrChain(3, "a")), logs)
	m.nodeTip = 3000
	logs.Reset()
	for _, h := range []uint32{utxoMilestone - 1, utxoMilestone, utxoMilestone + 1, 2 * utxoMilestone} {
		m.progress(h)
	}
	want := "utxo index: following height=1000 node_tip=3000\nutxo index: following height=2000 node_tip=3000\n"
	if logs.String() != want {
		t.Fatalf("log %q, want %q", logs.String(), want)
	}
}

func TestUtxoManagerRestartMidCatchUpResumesWithoutReseed(t *testing.T) {
	ctx := context.Background()
	c := utxoMgrChain(30, "b")
	heights := map[string]int{}
	for i, h := range c.hashes {
		heights[h] = i
	}
	node := newFakeUtxoNode(c)
	dir := t.TempDir()
	utxoMgrSeed(t, dir, nil, c)
	node.hook = func(method string, params any) error {
		if method == "getblock" && heights[params.([]any)[0].(string)] >= 12 {
			return errors.New("outage")
		}
		return nil
	}
	m := utxoMgrNew(t, dir, nil, node, io.Discard)
	m.tick(ctx)
	if st := m.Status(); st.State != utxoStateCatching || *st.IndexTip != 11 {
		t.Fatalf("mid catch-up: %+v", st)
	}
	if err := m.store.Close(); err != nil {
		t.Fatal(err)
	}

	var fetched []int
	node.hook = func(method string, params any) error {
		if method == "getblock" {
			fetched = append(fetched, heights[params.([]any)[0].(string)])
		}
		return nil
	}
	m2 := utxoMgrNew(t, dir, nil, node, io.Discard)
	if st := m2.Status(); st.State != utxoStateCatching || *st.IndexTip != 11 {
		t.Fatalf("restart state: %+v", st)
	}
	m2.tick(ctx)
	if len(fetched) != 18 || fetched[0] != 12 || fetched[len(fetched)-1] != 29 {
		t.Fatalf("restart fetched blocks %v, want 12..29", fetched)
	}
	if st := m2.Status(); st.State != utxoStateReady {
		t.Fatalf("after restart: %+v", st)
	}
	utxoMgrCheck(t, m2.store, c, "b", 29)
}

func utxoMgrRewriteMeta(t testing.TB, dir string, edit func(*utxoMeta)) {
	t.Helper()
	raw, err := pebble.Open(dir, &pebble.Options{FormatMajorVersion: utxoPebbleFormat})
	if err != nil {
		t.Fatal(err)
	}
	b, closer, err := raw.Get(utxoMetaKey)
	if err != nil {
		t.Fatal(err)
	}
	var meta utxoMeta
	if err := json.Unmarshal(b, &meta); err != nil {
		t.Fatal(err)
	}
	closer.Close()
	edit(&meta)
	if err := raw.Set(utxoMetaKey, mustMarshalUtxoMeta(meta), pebble.Sync); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestUtxoManagerInvalidStates(t *testing.T) {
	ctx := context.Background()
	type setup struct {
		prepare func(t *testing.T, dir string, c *fakeUtxoChain, node *fakeUtxoNode) (fs vfs.FS)
		reason  string
	}
	cases := map[string]setup{
		"schema bump": {func(t *testing.T, dir string, c *fakeUtxoChain, _ *fakeUtxoNode) vfs.FS {
			utxoMgrRewriteMeta(t, dir, func(m *utxoMeta) { m.Schema++ })
			return nil
		}, "format version"},
		"pebble format bump": {func(t *testing.T, dir string, c *fakeUtxoChain, _ *fakeUtxoNode) vfs.FS {
			utxoMgrRewriteMeta(t, dir, func(m *utxoMeta) { m.PebbleFormat++ })
			return nil
		}, "format version"},
		"chain mismatch": {func(t *testing.T, dir string, c *fakeUtxoChain, node *fakeUtxoNode) vfs.FS {
			utxoMgrRewriteMeta(t, dir, func(m *utxoMeta) { m.ChainName, m.GenesisHash = "main", "00" })
			return nil
		}, "different chain"},
		"behind retained blocks": {func(t *testing.T, dir string, c *fakeUtxoChain, node *fakeUtxoNode) vfs.FS {
			node.pruned, node.pruneHeight = true, 15
			return nil
		}, "retained blocks"},
		"latched follower error": {func(t *testing.T, dir string, c *fakeUtxoChain, node *fakeUtxoNode) vfs.FS {
			c.add("bad", nil, []fakeIn{{fakeDisplayHash("txb", 1), 0, "-"}})
			return nil
		}, "prevout"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			c := utxoMgrChain(20, "b")
			node := newFakeUtxoNode(c)
			dir := t.TempDir()
			utxoMgrSeed(t, dir, nil, c)
			bind := utxoMgrNew(t, dir, nil, node, io.Discard)
			heights := map[string]int{}
			for i, h := range c.hashes {
				heights[h] = i
			}
			node.hook = func(m string, params any) error {
				if m == "getblock" && heights[params.([]any)[0].(string)] >= 10 {
					return errors.New("stop at height 9")
				}
				return nil
			}
			bind.tick(ctx)
			if err := bind.store.Close(); err != nil {
				t.Fatal(err)
			}
			node.hook = nil
			tc.prepare(t, dir, c, node)
			m := utxoMgrNew(t, dir, nil, node, io.Discard)
			m.tick(ctx)
			st := m.Status()
			if st.State != utxoStateInvalid || !strings.Contains(st.Reason, tc.reason) {
				t.Fatalf("status %+v, want invalid containing %q", st, tc.reason)
			}
			if err := m.store.Close(); err != nil {
				t.Fatal(err)
			}
			node.fakeUtxoChain, node.pruned = c, false
			calls := node.calls["getblock"]
			again := utxoMgrNew(t, dir, nil, node, io.Discard)
			again.tick(ctx)
			if st := again.Status(); st.State != utxoStateInvalid || st.Reason == "" {
				t.Fatalf("invalid state did not survive a restart: %+v", st)
			}
			if node.calls["getblock"] != calls {
				t.Fatal("an invalid index fetched blocks")
			}
			if _, err := again.store.Lookup([][]byte{{0x51}}); !errors.Is(err, errUtxoInvalid) {
				t.Fatalf("lookup on invalid store: %v", err)
			}
		})
	}
}

func TestUtxoManagerReorgWithinJournalStaysReady(t *testing.T) {
	ctx := context.Background()
	c := utxoMgrChain(20, "b")
	node := newFakeUtxoNode(c)
	dir := t.TempDir()
	utxoMgrSeed(t, dir, nil, c)
	m := utxoMgrNew(t, dir, nil, node, io.Discard)
	m.tick(ctx)
	utxoMgrCheck(t, m.store, c, "b", 19)
	fork := utxoMgrChain(23, "x")
	node.fakeUtxoChain = fork
	m.tick(ctx)
	if st := m.Status(); st.State != utxoStateReady || !m.store.Valid() {
		t.Fatalf("after a reorg within the journal: %+v valid=%v", st, m.store.Valid())
	}
	utxoMgrCheck(t, m.store, fork, "x", 22)
}

func TestUtxoManagerReorgDeeperThanJournalIsInvalid(t *testing.T) {
	ctx := context.Background()
	n := utxoJournalDepth + 10
	c := utxoMgrChain(n, "b")
	node := newFakeUtxoNode(c)
	dir := t.TempDir()
	utxoMgrSeed(t, dir, nil, c)
	m := utxoMgrNew(t, dir, nil, node, io.Discard)
	m.tick(ctx)
	utxoMgrCheck(t, m.store, c, "b", uint32(n-1))
	node.fakeUtxoChain = utxoMgrChain(n+2, "x")
	m.tick(ctx)
	if st := m.Status(); st.State != utxoStateInvalid || !strings.Contains(st.Reason, "deeper than the undo journal") {
		t.Fatalf("status %+v", st)
	}
	if err := m.store.Close(); err != nil {
		t.Fatal(err)
	}
	again := utxoMgrNew(t, dir, nil, node, io.Discard)
	again.tick(ctx)
	if st := again.Status(); st.State != utxoStateInvalid {
		t.Fatalf("invalid state did not survive a restart: %+v", st)
	}
}

func TestUtxoManagerReadyOnlyWhenIdleAndAtTip(t *testing.T) {
	c := utxoMgrChain(20, "b")
	node := newFakeUtxoNode(c)
	dir := t.TempDir()
	utxoMgrSeed(t, dir, nil, c)
	m := utxoMgrNew(t, dir, nil, node, io.Discard)
	m.tick(context.Background())
	if st := m.Status(); st.State != utxoStateReady {
		t.Fatalf("status %+v", st)
	}
	m.follower.set(utxoFollowRollback, nil)
	if st := m.Status(); st.State != utxoStateCatching {
		t.Fatalf("rolling back reported %+v", st)
	}
	m.follower.set(utxoFollowIdle, nil)
	m.mu.Lock()
	m.nodeTip += utxoReadyLag + 1
	m.mu.Unlock()
	if st := m.Status(); st.State != utxoStateCatching {
		t.Fatalf("behind reported %+v", st)
	}
}

func TestUtxoManagerNoTickDuringRunSeed(t *testing.T) {
	c := utxoMgrChain(20, "b")
	node := newFakeUtxoNode(c)
	m := utxoMgrNew(t, t.TempDir(), nil, node, io.Discard)
	var inSeed, violated atomic.Bool
	node.hook = func(string, any) error {
		if inSeed.Load() {
			violated.Store(true)
		}
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { m.run(ctx); close(done) }()
	for i := 0; i < 20; i++ {
		err := m.runSeed(ctx, func(context.Context) error {
			inSeed.Store(true)
			defer inSeed.Store(false)
			time.Sleep(5 * time.Millisecond)
			if st := m.Status(); st.State != utxoStateSeeding || st.Phase != utxoPhaseWaiting || st.StartedAt == nil {
				t.Errorf("status during seed: %+v", st)
			}
			return errors.New("seed failed")
		})
		if err == nil {
			t.Fatal("seed error not returned")
		}
	}
	cancel()
	<-done
	if violated.Load() {
		t.Fatal("a follower tick ran during a seed")
	}
}

func TestUtxoManagerSeedLifecycle(t *testing.T) {
	m := utxoMgrNew(t, t.TempDir(), nil, newFakeUtxoNode(utxoMgrChain(5, "b")), io.Discard)
	if err := m.startSeed(); err != nil {
		t.Fatal(err)
	}
	if err := m.startSeed(); !errors.Is(err, errUtxoSeedState) {
		t.Fatalf("second start: %v", err)
	}
	if err := m.resetStore(); !errors.Is(err, errUtxoSeedState) {
		t.Fatalf("reset while seeding: %v", err)
	}
	m.setSeedPhase(utxoPhaseImporting)
	m.setSeedProgress(0.4)
	if st := m.Status(); st.Phase != utxoPhaseImporting || *st.SeedProgress != 0.4 {
		t.Fatalf("%+v", st)
	}
	m.finishSeed(context.Canceled)
	st := m.Status()
	if st.State != utxoStateNotSeeded || st.Phase != "" || st.SeedProgress != nil || st.StartedAt != nil || st.LastSeed == nil || st.LastSeed.Outcome != "cancelled" {
		t.Fatalf("after cancel: %+v", st)
	}
	if err := m.startSeed(); err != nil {
		t.Fatal(err)
	}
	m.finishSeed(nil)
	if st := m.Status(); st.State != utxoStateCatching || st.LastSeed.Outcome != "ok" {
		t.Fatalf("after success: %+v", st)
	}
	if err := m.resetStore(); !errors.Is(err, errUtxoSeedState) {
		t.Fatalf("reset from catching_up: %v", err)
	}
}

func TestUtxoManagerResetStore(t *testing.T) {
	c := utxoMgrChain(10, "b")
	dir := t.TempDir()
	utxoMgrSeed(t, dir, nil, c)
	m := utxoMgrNew(t, dir, nil, newFakeUtxoNode(c), io.Discard)
	if err := m.resetStore(); !errors.Is(err, errUtxoSeedState) {
		t.Fatalf("reset from catching_up: %v", err)
	}
	m.invalidate("test")
	if st := m.Status(); st.State != utxoStateInvalid {
		t.Fatalf("%+v", st)
	}
	old := m.store
	if err := m.resetStore(); err != nil {
		t.Fatal(err)
	}
	if st := m.Status(); st.State != utxoStateNotSeeded || st.IndexTip != nil || m.store == old || m.follower == nil || m.bound {
		t.Fatalf("after reset: %+v", st)
	}
	if _, _, has, err := m.store.Tip(); err != nil || has {
		t.Fatalf("store not empty: %v %v", has, err)
	}
	if err := m.startSeed(); err != nil {
		t.Fatal(err)
	}
	m.finishSeed(errors.New("boom"))
	if st := m.Status(); st.State != utxoStateNotSeeded || st.LastSeed.Outcome != "failed" {
		t.Fatalf("%+v", st)
	}
}

func TestUtxoManagerRunSeedInvalidWipesFirst(t *testing.T) {
	c := utxoMgrChain(10, "b")
	dir := t.TempDir()
	utxoMgrSeed(t, dir, nil, c)
	m := utxoMgrNew(t, dir, nil, newFakeUtxoNode(c), io.Discard)
	m.invalidate("test")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go m.run(ctx)
	err := m.runSeed(ctx, func(context.Context) error {
		if _, _, has, _ := m.currentStore().Tip(); has {
			t.Error("seed started on a non-empty store")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestUtxoManagerAlertsSuppressedCap(t *testing.T) {
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	path := filepath.Join(t.TempDir(), "utxo-seed.json")
	m := utxoMgrNew(t, t.TempDir(), nil, newFakeUtxoNode(utxoMgrChain(2, "b")), io.Discard)
	m.now = func() time.Time { return now }
	m.setSeedStatePath(path)
	var nilMgr *utxoManager
	if nilMgr.AlertsSuppressed() || m.AlertsSuppressed() {
		t.Fatal("suppressed without a deadline")
	}
	m.suppressAlerts(now.Add(time.Hour))
	if !m.AlertsSuppressed() || m.Status().AlertsSuppressedUntil == nil {
		t.Fatal("not suppressed inside the window")
	}
	if until := m.Status().AlertsSuppressedUntil; !until.Equal(now.Add(utxoAlertCap)) {
		t.Fatalf("status shows suppression until %v, past the 15 minute cap", until)
	}
	m.now = func() time.Time { return now.Add(utxoAlertCap - time.Second) }
	if !m.AlertsSuppressed() {
		t.Fatal("suppression ended before the cap")
	}
	m.now = func() time.Time { return now.Add(utxoAlertCap) }
	if m.AlertsSuppressed() {
		t.Fatal("deadline beyond the 15 minute cap was honoured")
	}
	m.now = func() time.Time { return now.Add(time.Minute) }
	m2 := utxoMgrNew(t, t.TempDir(), nil, newFakeUtxoNode(utxoMgrChain(2, "b")), io.Discard)
	m2.now = m.now
	m2.setSeedStatePath(path)
	if !m2.AlertsSuppressed() {
		t.Fatal("deadline was not persisted")
	}
	m2.suppressAlerts(now)
	if m2.AlertsSuppressed() {
		t.Fatal("past deadline still suppresses")
	}
}

func TestUtxoStatusJSONSeedFields(t *testing.T) {
	m := utxoMgrNew(t, t.TempDir(), nil, newFakeUtxoNode(utxoMgrChain(2, "b")), io.Discard)
	raw, err := json.Marshal(m.Status())
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"phase", "started_at", "refusals", "last_seed", "alerts_suppressed_until"} {
		if _, ok := got[key]; !ok {
			t.Fatalf("missing %s in %s", key, raw)
		}
	}
	if list, ok := got["refusals"].([]any); !ok || len(list) != 0 || got["last_seed"] != nil || got["started_at"] != nil || got["phase"] != "" {
		t.Fatalf("idle status %s", raw)
	}
}

func TestUtxoManagerNodeDownIsNotReady(t *testing.T) {
	ctx := context.Background()
	c := utxoMgrChain(20, "b")
	node := newFakeUtxoNode(c)
	dir := t.TempDir()
	utxoMgrSeed(t, dir, nil, c)
	m := utxoMgrNew(t, dir, nil, node, io.Discard)
	m.tick(ctx)
	if st := m.Status(); st.State != utxoStateReady {
		t.Fatalf("status %+v", st)
	}
	node.hook = func(string, any) error { return errors.New("connection refused") }
	m.tick(ctx)
	if st := m.Status(); st.State != utxoStateCatching || st.Reason == "" {
		t.Fatalf("node down reported %+v", st)
	}
	node.hook = nil
	m.tick(ctx)
	if st := m.Status(); st.State != utxoStateReady || st.Reason != "" {
		t.Fatalf("node back reported %+v", st)
	}
}

func TestUtxoManagerBindErrorRetries(t *testing.T) {
	c := utxoMgrChain(20, "b")
	node := newFakeUtxoNode(c)
	dir := t.TempDir()
	utxoMgrSeed(t, dir, nil, c)
	m := utxoMgrNew(t, dir, nil, node, io.Discard)
	if err := m.store.Close(); err != nil {
		t.Fatal(err)
	}
	m.tick(context.Background())
	if st := m.Status(); st.State == utxoStateInvalid {
		t.Fatalf("a failed chain bind invalidated the index: %+v", st)
	}
	s, err := openUtxoStoreFS(dir, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if !s.Valid() {
		t.Fatalf("invalid persisted: %s", s.InvalidReason())
	}
}

func TestUtxoErrorClassDropsDetail(t *testing.T) {
	err := fmt.Errorf("%w: %s:%d", errUtxoNoPrevout, fakeDisplayHash("tx", 1), 0)
	if got := utxoErrorClass(err); got != errUtxoNoPrevout.Error() {
		t.Fatalf("class %q", got)
	}
	if got := utxoErrorClass(errors.New("boom /utxo-index/000001.log")); got != "unclassified error" {
		t.Fatalf("class %q", got)
	}
}

func TestUtxoStoreMarkInvalidPersists(t *testing.T) {
	dir := t.TempDir()
	s := utxoTestOpen(t, dir)
	utxoTestApply(t, s, 1, 3)
	if err := s.BindChain("regtest", "aa"); err != nil {
		t.Fatal(err)
	}
	if err := s.BindChain("regtest", "bb"); !errors.Is(err, errUtxoChainMismatch) {
		t.Fatalf("different genesis: %v", err)
	}
	if err := s.MarkInvalid("because"); err != nil {
		t.Fatal(err)
	}
	if s.Valid() || s.InvalidReason() != "because" {
		t.Fatalf("not invalid: %q", s.InvalidReason())
	}
	re := utxoTestOpen(t, dir)
	if re.Valid() || re.InvalidReason() != "because" {
		t.Fatalf("reopened as %q", re.InvalidReason())
	}
}

func TestUtxoStoreMetaKeepsIdentityAcrossBlocksAndDisconnect(t *testing.T) {
	s := utxoTestOpen(t, t.TempDir())
	if err := s.BindChain("regtest", "aa"); err != nil {
		t.Fatal(err)
	}
	utxoTestApply(t, s, 1, 3)
	if err := s.DisconnectTip(); err != nil {
		t.Fatal(err)
	}
	if err := s.BindChain("regtest", "aa"); err != nil {
		t.Fatalf("identity lost: %v", err)
	}
	if err := s.BindChain("signet", "aa"); !errors.Is(err, errUtxoChainMismatch) {
		t.Fatalf("different chain: %v", err)
	}
}

func TestUtxoManagerStorageFullMarksInvalidAndKeepsServing(t *testing.T) {
	for _, stayFull := range []bool{false, true} {
		t.Run(fmt.Sprintf("stay full=%v", stayFull), func(t *testing.T) {
			var armed atomic.Bool
			fs := errorfs.Wrap(vfs.NewMem(), errorfs.InjectorFunc(func(op errorfs.Op, _ string) error {
				if armed.Load() && (op == errorfs.OpFileWrite || op == errorfs.OpFileSync || op == errorfs.OpCreate) {
					if !stayFull {
						armed.Store(false)
					}
					return syscall.ENOSPC
				}
				return nil
			}))
			c := utxoMgrChain(10, "b")
			node := newFakeUtxoNode(c)
			utxoMgrSeed(t, "idx", fs, c)
			m := utxoMgrNew(t, "idx", fs, node, io.Discard)
			node.hook = func(method string, _ any) error {
				if method == "getblock" {
					armed.Store(true)
				}
				return nil
			}
			m.tick(context.Background())
			if st := m.Status(); st.State != utxoStateInvalid || !strings.Contains(st.Reason, "storage error") {
				t.Fatalf("status %+v", st)
			}
			m.tick(context.Background())
			armed.Store(false)
			again := utxoMgrNew(t, "idx", fs, node, io.Discard)
			if stayFull {
				if st := again.Status(); st.State == utxoStateInvalid {
					t.Fatalf("restart without a persisted marker should reopen a consistent store: %+v", st)
				}
				return
			}
			if st := again.Status(); st.State != utxoStateInvalid {
				t.Fatalf("restart: %+v", st)
			}
		})
	}
}

func utxoMgrKillChainLen() int { return 4000 }

func TestUtxoManagerKillLoopOverFollowPhase(t *testing.T) {
	if testing.Short() {
		t.Skip("kill loop skipped in short mode")
	}
	dir := t.TempDir()
	c := utxoMgrChain(utxoMgrKillChainLen(), "b")
	rng := rand.New(rand.NewSource(7))
	var lastTip uint32
	for round := 0; round < 6; round++ {
		cmd := exec.Command(os.Args[0], "-test.run=^TestUtxoManagerKillHelper$")
		cmd.Env = append(os.Environ(), "UTXO_MGR_KILL_DIR="+dir)
		out, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		if _, err := bufio.NewReader(out).ReadString('\n'); err != nil {
			t.Fatalf("round %d: helper never started: %v", round, err)
		}
		time.Sleep(time.Duration(50+rng.Intn(400)) * time.Millisecond)
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		s, err := openUtxoStore(dir)
		if err != nil {
			t.Fatalf("round %d reopen: %v", round, err)
		}
		if !s.Valid() {
			_ = s.Close()
			t.Fatalf("round %d: index invalid after kill: %s", round, s.InvalidReason())
		}
		tip, _, _, _ := s.Tip()
		if tip < lastTip {
			t.Fatalf("round %d: tip went back from %d to %d", round, lastTip, tip)
		}
		utxoMgrCheck(t, s, c, "b", tip)
		lastTip = tip
		_ = s.Close()
	}
	if lastTip == 0 {
		t.Fatal("the helper never applied a block")
	}
	node := newFakeUtxoNode(c)
	m := utxoMgrNew(t, dir, nil, node, io.Discard)
	m.tick(context.Background())
	if m.Status().State != utxoStateReady {
		t.Fatalf("final catch-up: %+v", m.Status())
	}
	utxoMgrCheck(t, m.store, c, "b", uint32(len(c.hashes)-1))
	t.Logf("killed 6 times, last tip before the final catch-up %d of %d", lastTip, len(c.hashes)-1)
}

func TestUtxoManagerKillHelper(t *testing.T) {
	dir := os.Getenv("UTXO_MGR_KILL_DIR")
	if dir == "" {
		t.Skip("helper for the kill loop")
	}
	c := utxoMgrChain(utxoMgrKillChainLen(), "b")
	s, err := openUtxoStore(dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	_, _, has, _ := s.Tip()
	_ = s.Close()
	if !has {
		utxoMgrSeed(t, dir, nil, c)
	}
	m := newUtxoManager(dir, nil, newFakeUtxoNode(c), log.New(io.Discard, "", 0), utxoBitcoin)
	m.freeBytes = func() uint64 { return 1 << 40 }
	fmt.Println("started")
	for {
		m.tick(context.Background())
		time.Sleep(5 * time.Millisecond)
	}
}

type utxoAPIStatus struct {
	Enabled      bool     `json:"enabled"`
	State        string   `json:"state"`
	Reason       string   `json:"reason"`
	IndexTip     *float64 `json:"index_tip_height"`
	NodeTip      *float64 `json:"node_tip_height"`
	Behind       *float64 `json:"blocks_behind"`
	SeedProgress *float64 `json:"seed_progress"`

	Phase                 string                    `json:"phase"`
	StartedAt             *string                   `json:"started_at"`
	Refusals              []seedRefusal             `json:"refusals"`
	LastSeed              *struct{ Outcome string } `json:"last_seed"`
	AlertsSuppressedUntil *string                   `json:"alerts_suppressed_until"`
	NodePruned            *bool                     `json:"node_pruned"`
}

func TestUtxoIndexAPIPerState(t *testing.T) {
	ctx := context.Background()
	type fixture func(t *testing.T) *utxoManager
	seeded := func(t *testing.T, n int, node **fakeUtxoNode) *utxoManager {
		c := utxoMgrChain(n, "b")
		dir := t.TempDir()
		utxoMgrSeed(t, dir, nil, c)
		*node = newFakeUtxoNode(c)
		return utxoMgrNew(t, dir, nil, *node, io.Discard)
	}
	cases := []struct {
		state  string
		build  fixture
		verify func(t *testing.T, st utxoAPIStatus)
	}{
		{utxoStateNotSeeded, func(t *testing.T) *utxoManager {
			m := utxoMgrNew(t, t.TempDir(), nil, newFakeUtxoNode(utxoMgrChain(5, "b")), io.Discard)
			m.tick(ctx)
			return m
		}, func(t *testing.T, st utxoAPIStatus) {
			if st.IndexTip != nil || st.NodeTip == nil || *st.NodeTip != 4 || st.Behind != nil {
				t.Fatalf("%+v", st)
			}
		}},
		{utxoStateSeeding, func(t *testing.T) *utxoManager {
			var n *fakeUtxoNode
			m := seeded(t, 5, &n)
			m.set(utxoStateNotSeeded, "")
			if err := m.startSeed(); err != nil {
				t.Fatal(err)
			}
			m.setSeedProgress(0.5)
			m.setSeedPhase(utxoPhaseMerging)
			m.setRefusals([]seedRefusal{{"thermal", "hot"}})
			m.suppressAlerts(m.now().Add(time.Minute))
			return m
		}, func(t *testing.T, st utxoAPIStatus) {
			if st.Phase != utxoPhaseMerging || st.StartedAt == nil || st.AlertsSuppressedUntil == nil || len(st.Refusals) != 1 || st.Refusals[0].Guard != "thermal" {
				t.Fatalf("%+v", st)
			}
			if st.SeedProgress == nil || *st.SeedProgress != 0.5 {
				t.Fatalf("%+v", st)
			}
		}},
		{utxoStateCatching, func(t *testing.T) *utxoManager {
			var n *fakeUtxoNode
			m := seeded(t, 20, &n)
			n.hook = func(method string, _ any) error {
				if method == "getblockhash" {
					return errors.New("outage")
				}
				return nil
			}
			m.tick(ctx)
			return m
		}, func(t *testing.T, st utxoAPIStatus) {
			if st.IndexTip == nil || *st.IndexTip != 0 || *st.NodeTip != 19 || *st.Behind != 19 {
				t.Fatalf("%+v", st)
			}
		}},
		{utxoStateReady, func(t *testing.T) *utxoManager {
			var n *fakeUtxoNode
			m := seeded(t, 6, &n)
			m.tick(ctx)
			return m
		}, func(t *testing.T, st utxoAPIStatus) {
			if *st.IndexTip != 5 || *st.NodeTip != 5 || *st.Behind != 0 || st.Reason != "" {
				t.Fatalf("%+v", st)
			}
		}},
		{utxoStateDegraded, func(t *testing.T) *utxoManager {
			var n *fakeUtxoNode
			m := seeded(t, 6, &n)
			m.freeBytes = func() uint64 { return 1 }
			m.tick(ctx)
			return m
		}, func(t *testing.T, st utxoAPIStatus) {
			if *st.IndexTip != 0 || *st.Behind != 5 || st.Reason == "" {
				t.Fatalf("%+v", st)
			}
		}},
		{utxoStateInvalid, func(t *testing.T) *utxoManager {
			var n *fakeUtxoNode
			m := seeded(t, 6, &n)
			m.invalidate("test reason")
			return m
		}, func(t *testing.T, st utxoAPIStatus) {
			if st.Reason != "test reason" {
				t.Fatalf("%+v", st)
			}
		}},
	}
	a, handler := testApp(t, &rpcFixture{}, "dashboard-secret")
	if resp := anonymousRequest(t, handler, http.MethodGet, "/api/utxo-index"); resp.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous request: %d", resp.Code)
	}
	for _, tc := range cases {
		t.Run(tc.state, func(t *testing.T) {
			a.utxo = tc.build(t)
			resp := request(t, handler, http.MethodGet, "/api/utxo-index", "")
			if resp.Code != http.StatusOK || !strings.HasPrefix(resp.Header().Get("Content-Type"), "application/json") {
				t.Fatalf("response %d %s", resp.Code, resp.Header().Get("Content-Type"))
			}
			var st utxoAPIStatus
			if err := json.Unmarshal(resp.Body.Bytes(), &st); err != nil {
				t.Fatal(err)
			}
			if !st.Enabled || st.State != tc.state {
				t.Fatalf("%+v, want state %s", st, tc.state)
			}
			tc.verify(t, st)
		})
	}
}

func TestUtxoIndexOffByDefault(t *testing.T) {
	root := filepath.Dir(sourceFile(t))
	build := func(enabled bool, dir string) *app {
		server := httptest.NewServer(http.HandlerFunc((&rpcFixture{}).handler))
		t.Cleanup(server.Close)
		cfg := config{rpcURL: server.URL, rpcUser: "u", rpcPassword: "p", dashboardPassword: "dashboard-secret",
			dataDir: t.TempDir(), stateDir: t.TempDir(), version: "1.0.0", utxoIndexEnabled: enabled, utxoIndexDir: dir}
		rpc := &rpcClient{url: cfg.rpcURL, user: cfg.rpcUser, password: cfg.rpcPassword, client: &http.Client{Timeout: time.Second}}
		a, err := newApp(cfg, rpc, filepath.Join(root, "index.html"), filepath.Join(root, "static"), log.New(io.Discard, "", 0))
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	dir := filepath.Join(t.TempDir(), "idx")
	off := build(false, dir)
	if off.utxo != nil {
		t.Fatal("manager created while disabled")
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := off.startBackground(ctx)
	cancel()
	<-done
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("disabled index created %s: %v", dir, err)
	}
	resp := request(t, off.routes(), http.MethodGet, "/api/utxo-index", "")
	var st utxoAPIStatus
	if err := json.Unmarshal(resp.Body.Bytes(), &st); err != nil || resp.Code != http.StatusOK {
		t.Fatalf("%d %v", resp.Code, err)
	}
	if st.State != utxoStateOff || st.Enabled || st.IndexTip != nil || st.NodeTip != nil {
		t.Fatalf("off status %+v", st)
	}

	on := build(true, dir)
	info, err := os.Stat(dir)
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("index dir: %v %v", info, err)
	}
	if on.utxo == nil || on.utxo.Status().State != utxoStateNotSeeded {
		t.Fatalf("enabled status %+v", on.utxo.Status())
	}
	if on.utxo.blockWait == nil || on.watchWake == nil || off.watchWake != nil {
		t.Fatal("only an enabled index gets the new-block wake-up")
	}
	on.utxo.onApplied()
	on.utxo.onApplied()
	if len(on.watchWake) != 1 {
		t.Fatalf("wallet wake-ups queued: %d", len(on.watchWake))
	}
	_ = on.utxo.store.Close()
}

func TestUtxoIndexConfigRejectsRelativeDir(t *testing.T) {
	t.Setenv("RPC_USER", "u")
	t.Setenv("RPC_PASSWORD", "p")
	t.Setenv("UTXO_INDEX_ENABLED", "1")
	t.Setenv("UTXO_INDEX_DIR", "relative/dir")
	if _, err := envConfig(); err == nil || !strings.Contains(err.Error(), "UTXO_INDEX_DIR") {
		t.Fatalf("envConfig: %v", err)
	}
	t.Setenv("UTXO_INDEX_DIR", "/utxo-index")
	cfg, err := envConfig()
	if err != nil || !cfg.utxoIndexEnabled || cfg.utxoIndexDir != "/utxo-index" {
		t.Fatalf("envConfig: %+v %v", cfg, err)
	}
	for value, want := range map[string]bool{"": false, "0": false, "FALSE": false, "off": false, "no": false, "2": false, "TRUE": true, "on": true, "yes": true} {
		t.Setenv("UTXO_INDEX_ENABLED", value)
		if cfg, err := envConfig(); err != nil || cfg.utxoIndexEnabled != want {
			t.Fatalf("UTXO_INDEX_ENABLED=%q: enabled=%v err=%v", value, cfg.utxoIndexEnabled, err)
		}
	}
}

func TestUtxoManagerLowDiskMidCatchUpPausesWithoutInvalidating(t *testing.T) {
	ctx := context.Background()
	c := utxoMgrChain(10, "b")
	node := newFakeUtxoNode(c)
	dir := t.TempDir()
	utxoMgrSeed(t, dir, nil, c)
	m := utxoMgrNew(t, dir, nil, node, io.Discard)
	var free atomic.Uint64
	free.Store(1 << 40)
	m.freeBytes = free.Load
	node.hook = func(method string, params any) error {
		if method == "getblock" && params.([]any)[0] == c.hashes[4] {
			free.Store(utxoMinFreeBytes - 1)
		}
		return nil
	}
	m.tick(ctx)
	st := m.Status()
	if st.State != utxoStateDegraded || st.IndexTip == nil || *st.IndexTip != 4 {
		t.Fatalf("status %+v, want degraded at height 4", st)
	}
	if !m.store.Valid() {
		t.Fatal("low disk must not invalidate the store")
	}
	node.hook = nil
	free.Store(1 << 40)
	m.tick(ctx)
	if st := m.Status(); st.State != utxoStateReady || *st.IndexTip != 9 {
		t.Fatalf("after space returned: %+v", st)
	}
}

func TestUtxoStartSeedRefusesWhenReopenFails(t *testing.T) {
	var armed atomic.Bool
	fs := errorfs.Wrap(vfs.NewMem(), errorfs.InjectorFunc(func(op errorfs.Op, _ string) error {
		if armed.Load() && op == errorfs.OpMkdirAll {
			return errors.New("injected")
		}
		return nil
	}))
	m := utxoMgrNew(t, t.TempDir(), fs, newFakeUtxoNode(utxoMgrChain(5, "b")), io.Discard)
	m.invalidate("test")
	armed.Store(true)
	if err := m.startSeed(); !errors.Is(err, errUtxoSeedState) {
		t.Fatalf("start on a failed reopen: %v", err)
	}
	if st := m.Status(); st.State != utxoStateInvalid || m.seeding {
		t.Fatalf("%+v seeding=%v", st, m.seeding)
	}
}

func TestUtxoManagerRestrictsExistingIndexDirAndLogsStartState(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	utxoMgrNew(t, dir, nil, newFakeUtxoNode(utxoMgrChain(3, "a")), &logs)
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o700 {
		t.Fatalf("index directory mode = %o, want 700", mode)
	}
	if !strings.Contains(logs.String(), "utxo index: state=not_seeded") {
		t.Fatalf("start state not logged: %q", logs.String())
	}
}

func TestUtxoIndexOffReportsPrunedNode(t *testing.T) {
	yes, no := true, false
	cases := []struct {
		name    string
		fixture *rpcFixture
		want    *bool
	}{
		{"pruned", &rpcFixture{}, &yes},
		{"unpruned", &rpcFixture{unpruned: true}, &no},
		{"unknown", &rpcFixture{invalid: true}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, handler := testApp(t, tc.fixture, "dashboard-secret")
			var st utxoAPIStatus
			if err := json.Unmarshal(request(t, handler, http.MethodGet, "/api/utxo-index", "").Body.Bytes(), &st); err != nil {
				t.Fatal(err)
			}
			if st.State != utxoStateOff || (st.NodePruned == nil) != (tc.want == nil) || (tc.want != nil && *st.NodePruned != *tc.want) {
				t.Fatalf("off status %+v, want node_pruned %v", st, tc.want)
			}
		})
	}
}

func TestUtxoIndexOffSkipsPrunedLookupWhenBitcoinDisabled(t *testing.T) {
	fixture := &rpcFixture{}
	a, handler := testApp(t, fixture, "dashboard-secret")
	a.cfg.bitcoinDisabled = true
	var st utxoAPIStatus
	if err := json.Unmarshal(request(t, handler, http.MethodGet, "/api/utxo-index", "").Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if st.NodePruned != nil || len(fixture.calls) != 0 {
		t.Fatalf("node_pruned %v, rpc calls %v", st.NodePruned, fixture.calls)
	}
}
