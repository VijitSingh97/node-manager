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
	s, err := openUtxoStoreFS(dir, fs)
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
	m := newUtxoManager(dir, fs, node, log.New(logs, "", 0))
	m.freeBytes = func() uint64 { return 1 << 40 }
	t.Cleanup(func() { _ = m.store.Close() })
	return m
}

func utxoMgrState(m *utxoManager) string { return m.Status().State }

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
	m.SetSeed(true, 0.25)
	m.tick(ctx)
	if st := m.Status(); st.State != utxoStateSeeding || st.SeedProgress == nil || *st.SeedProgress != 0.25 {
		t.Fatalf("seeding: %+v", st)
	}
	m.SetSeed(false, 0)
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
		"node resynced": {func(t *testing.T, dir string, c *fakeUtxoChain, node *fakeUtxoNode) vfs.FS {
			node.fakeUtxoChain = utxoMgrChain(20, "x")
			return nil
		}, "no longer on the node's chain"},
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
			t.Logf("round %d: index invalid after kill (%s), accepted", round, s.InvalidReason())
			return
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
	m := newUtxoManager(dir, nil, newFakeUtxoNode(c), log.New(io.Discard, "", 0))
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
			m.SetSeed(true, 0.5)
			return m
		}, func(t *testing.T, st utxoAPIStatus) {
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
	t.Setenv("UTXO_INDEX_ENABLED", "")
	if cfg, err := envConfig(); err != nil || cfg.utxoIndexEnabled {
		t.Fatalf("default config: %+v %v", cfg, err)
	}
}
