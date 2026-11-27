package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

const (
	utxoRecoveryIngestCoins = 20000
	utxoRecoveryIngestBase  = 50
)

var utxoRecoveryReorgKills = []int{1, 2, 4, 6, 7, 8, 10, 13, 14, 15}

type utxoRecoveryRPC struct{ cur *fakeUtxoChain }

func (r *utxoRecoveryRPC) call(ctx context.Context, method string, params any, out any) error {
	return r.cur.call(ctx, method, params, out)
}

func utxoRecoveryChain(branch string, n int) *fakeUtxoChain {
	c := newFakeUtxoChain()
	genesis(c)
	label := func(h int) string {
		if h < 5 {
			return "c"
		}
		return branch
	}
	for h := 1; h < 5+n; h++ {
		l := label(h)
		outs := []fakeOut{{fmt.Sprintf("51%02x", h), "1.00000000"}, {fmt.Sprintf("52%02x%02x", l[0], h), "2.00000000"}}
		var ins []fakeIn
		if h > 1 {
			ins = append(ins, fakeIn{fakeDisplayHash("tx"+label(h-1), h-1), 0, fmt.Sprintf("51%02x", h-1)})
		}
		if h == 5 && branch == "b" {
			ins = append(ins, fakeIn{fakeDisplayHash("txc", 3), 1, fmt.Sprintf("52%02x%02x", 'c', 3)})
		}
		c.add(l, outs, ins)
	}
	return c
}

func utxoRecoveryPrint(t testing.TB, s *utxoStore) string {
	t.Helper()
	height, hash, ok, err := s.Tip()
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%v %d %x\n", ok, height, hash)
	it, err := s.db.NewIter(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer it.Close()
	for valid := it.First(); valid; valid = it.Next() {
		if len(it.Key()) == utxoKeyLen {
			fmt.Fprintf(&b, "%x %x\n", it.Key(), it.Value())
		}
	}
	return b.String()
}

func utxoRecoveryPrefixStates(t *testing.T) map[string]bool {
	t.Helper()
	states := map[string]bool{}
	for _, full := range []*fakeUtxoChain{utxoRecoveryChain("a", 6), utxoRecoveryChain("b", 8)} {
		for p := 1; p <= len(full.hashes); p++ {
			prefix := &fakeUtxoChain{hashes: full.hashes[:p], blocks: full.blocks, calls: map[string]int{}, verbosity: full.verbosity}
			s := openFollowStore(t)
			stepOK(t, newUtxoFollower(s, prefix))
			states[utxoRecoveryPrint(t, s)] = true
		}
	}
	return states
}

func utxoRecoveryKill(t *testing.T, dir, mode, stop string, env ...string) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestUtxoRecoveryHelper$")
	cmd.Env = append(append(os.Environ(), "UTXO_RECOVERY_DIR="+dir, "UTXO_RECOVERY_MODE="+mode, "UTXO_RECOVERY_STOP="+stop), env...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	line := make([]byte, 8)
	hung := time.AfterFunc(time.Minute, func() { _ = cmd.Process.Kill() })
	n, rerr := io.ReadFull(out, line)
	hung.Stop()
	_ = cmd.Process.Signal(syscall.SIGKILL)
	_ = cmd.Wait()
	if rerr != nil || string(line[:n]) != "reached\n" {
		t.Fatalf("%s/%s: helper never reached the kill point: %v %q", mode, stop, rerr, stderr.String())
	}
}

func TestUtxoRecoveryHelper(t *testing.T) {
	dir := os.Getenv("UTXO_RECOVERY_DIR")
	if dir == "" {
		t.Skip("helper for the recovery kill loops")
	}
	stop := os.Getenv("UTXO_RECOVERY_STOP")
	reached := func() {
		fmt.Println("reached")
		select {}
	}
	fail := func(code int, err error) {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(code)
	}
	if n, _ := strconv.Atoi(os.Getenv("UTXO_PIECE_OPS")); n > 0 {
		utxoBlockPieceOps = n
	}
	s, err := openUtxoStore(dir)
	if err != nil {
		fail(2, err)
	}
	if os.Getenv("UTXO_RECOVERY_MODE") == "ingest" {
		utxoIngestRunRecords, utxoIngestSSTBytes = 1000, 20<<10
		prog := &utxoIngestProgress{}
		prog.Checkpoint = func() error {
			if stop == "runs" && prog.Parsed.Load() >= 8192 && prog.Written.Load() == 0 ||
				stop == "merge" && prog.Written.Load() >= 8192 {
				reached()
			}
			return nil
		}
		s.beforeIngest = func() error {
			if stop == "ingested" {
				reached()
			}
			return nil
		}
		baseHeight := func([32]byte) (uint32, error) {
			if stop == "baseheight" {
				reached()
			}
			return utxoRecoveryIngestBase, nil
		}
		if _, err := ingestUtxoSnapshot(s, utxoTestRandomDump(utxoRecoveryIngestCoins), utxoBitcoin, utxoNetRegtest, baseHeight, prog); err != nil {
			fail(3, err)
		}
		reached()
	}
	rpc := &utxoRecoveryRPC{cur: utxoRecoveryChain("a", 6)}
	f := newUtxoFollower(s, rpc)
	if err := f.Step(context.Background()); err != nil {
		fail(3, err)
	}
	var want int
	fmt.Sscanf(stop, "%d", &want)
	var commits atomic.Int64
	s.beforeCommit = func() error {
		if commits.Add(1) == int64(want) {
			reached()
		}
		return nil
	}
	rpc.cur = utxoRecoveryChain("b", 8)
	if err := f.Step(context.Background()); err != nil {
		fail(3, err)
	}
	reached()
}

func TestUtxoRecoveryKillLoopSeedIngest(t *testing.T) {
	if testing.Short() {
		t.Skip("kill loop skipped in short mode")
	}
	for _, stop := range []string{"runs", "merge", "baseheight", "ingested", "done"} {
		dir := t.TempDir()
		utxoRecoveryKill(t, dir, "ingest", stop)
		s, err := openUtxoStore(dir)
		if err != nil {
			t.Fatalf("%s: reopen: %v", stop, err)
		}
		if !s.Valid() {
			t.Fatalf("%s: reopened store invalid: %s", stop, s.InvalidReason())
		}
		height, hash, seeded, err := s.Tip()
		if err != nil {
			t.Fatal(err)
		}
		coins := 0
		it, err := s.db.NewIter(nil)
		if err != nil {
			t.Fatal(err)
		}
		for ok := it.First(); ok; ok = it.Next() {
			if len(it.Key()) == utxoKeyLen {
				coins++
			}
		}
		_ = it.Close()
		if stop == "done" {
			if !seeded || height != utxoRecoveryIngestBase || hash != ([32]byte{}) || coins != utxoRecoveryIngestCoins {
				t.Fatalf("done: seeded=%v tip=%d hash=%x coins=%d", seeded, height, hash, coins)
			}
		} else if seeded || coins != 0 || s.RequireEmpty() != nil {
			t.Fatalf("%s: partial store survived the kill: seeded=%v coins=%d", stop, seeded, coins)
		}
		if _, err := os.Stat(filepath.Join(dir, utxoIngestTmpDir)); !os.IsNotExist(err) {
			t.Fatalf("%s: ingest temp dir survived reopen: %v", stop, err)
		}
		_ = s.Close()
	}
}

func TestUtxoRecoveryKillLoopRollbacks(t *testing.T) {
	if testing.Short() {
		t.Skip("kill loop skipped in short mode")
	}
	states := utxoRecoveryPrefixStates(t)
	if len(states) < 12 {
		t.Fatalf("only %d distinct prefix states", len(states))
	}
	for _, k := range utxoRecoveryReorgKills {
		dir := t.TempDir()
		utxoRecoveryKill(t, dir, "reorg", fmt.Sprint(k))
		s, err := openUtxoStore(dir)
		if err != nil {
			t.Fatalf("kill %d: reopen: %v", k, err)
		}
		if s.Valid() && !states[utxoRecoveryPrint(t, s)] {
			t.Fatalf("kill %d: reopened store is not a prefix of either branch:\n%s", k, utxoRecoveryPrint(t, s))
		}
		_ = s.Close()
	}
}

func TestUtxoRecoveryKillLoopPiecedReorg(t *testing.T) {
	if testing.Short() {
		t.Skip("kill loop skipped in short mode")
	}
	pieces := utxoBlockPieceOps
	utxoBlockPieceOps = 1
	t.Cleanup(func() { utxoBlockPieceOps = pieces })
	states := utxoRecoveryPrefixStates(t)
	s := openFollowStore(t)
	rpc := &utxoRecoveryRPC{cur: utxoRecoveryChain("a", 6)}
	f := newUtxoFollower(s, rpc)
	stepOK(t, f)
	commits := 0
	s.beforeCommit = func() error { commits++; return nil }
	rpc.cur = utxoRecoveryChain("b", 8)
	stepOK(t, f)
	want := utxoRecoveryPrint(t, s)
	t.Logf("the pieced reorg takes %d commits", commits)
	if commits < 30 {
		t.Fatalf("the pieced reorg took only %d commits", commits)
	}
	// Each block here takes three commits, so a stride of 4 still reaches every kind of boundary.
	for k := 1; k <= commits; k += 4 {
		dir := t.TempDir()
		utxoRecoveryKill(t, dir, "reorg", fmt.Sprint(k), "UTXO_PIECE_OPS=1")
		s, err := openUtxoStore(dir)
		if err != nil {
			t.Fatalf("kill %d: reopen: %v", k, err)
		}
		if !s.Valid() || !states[utxoRecoveryPrint(t, s)] {
			t.Fatalf("kill %d: reopened store is not a prefix of either branch:\n%s", k, utxoRecoveryPrint(t, s))
		}
		stepOK(t, newUtxoFollower(s, &utxoRecoveryRPC{cur: utxoRecoveryChain("b", 8)}))
		if got := utxoRecoveryPrint(t, s); got != want {
			t.Fatalf("kill %d: resumed store differs from branch b:\n%s", k, got)
		}
		_ = s.Close()
	}
}

func utxoRecoveryBlockWrites(t *testing.T, path string) func() {
	t.Helper()
	if err := os.MkdirAll(path+".tmp", 0o700); err != nil {
		t.Fatal(err)
	}
	return func() {
		if err := os.Remove(path + ".tmp"); err != nil {
			t.Fatal(err)
		}
	}
}

func TestUtxoSeedStatusWriteFailureKeepsStateAndRecovers(t *testing.T) {
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	path := filepath.Join(t.TempDir(), "utxo-seed.json")
	logs := &lockedLog{}
	m := utxoMgrNew(t, t.TempDir(), nil, newFakeUtxoNode(utxoMgrChain(2, "b")), logs)
	m.now = func() time.Time { return now }
	m.setSeedStatePath(path)
	unblock := utxoRecoveryBlockWrites(t, path)
	m.suppressAlerts(now.Add(time.Minute))
	if !m.AlertsSuppressed() || m.Status().AlertsSuppressedUntil == nil {
		t.Fatal("in-memory suppression lost after a failed write")
	}
	if !strings.Contains(logs.String(), "seed state could not be persisted") {
		t.Fatalf("failure not logged: %q", logs.String())
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("status file exists despite the failed write: %v", err)
	}
	m.tick(context.Background())
	if m.Status().State == "" {
		t.Fatal("manager wedged")
	}
	unblock()
	m.suppressAlerts(now.Add(2 * time.Minute))
	m2 := utxoMgrNew(t, t.TempDir(), nil, newFakeUtxoNode(utxoMgrChain(2, "b")), io.Discard)
	m2.now = m.now
	m2.setSeedStatePath(path)
	if !m2.AlertsSuppressed() {
		t.Fatal("write after space returned did not persist")
	}
}

func TestPrunedScriptRegistryWriteFailureKeepsServingAndRecovers(t *testing.T) {
	c := indexTestChain(nil)
	m := indexTestManager(t, c)
	a, handler, mock := indexTestApp(t, c, m)
	logs := &lockedLog{}
	a.log.SetOutput(logs)
	calls := 0
	resolve := indexTestScripts(indexTestScript)
	a.scanner.resolveScripts = func(ctx context.Context, keys []string, onChunk func([][]byte)) ([]walletScripts, error) {
		calls++
		return resolve(ctx, keys, onChunk)
	}
	unblock := utxoRecoveryBlockWrites(t, a.scanner.scriptsPath())
	if response := addPrunedWatch(t, handler, "Cold", genesisAddress); response.Code != http.StatusOK {
		t.Fatalf("add response = %d", response.Code)
	}
	row := waitPrunedState(t, handler, "ok")
	waitPrunedScannerIdle(t, a.scanner)
	if row["balance_source"] != "index" || scanCalls(mock) != 0 {
		t.Fatalf("index not used after a failed registry write: %#v", row)
	}
	if !strings.Contains(logs.String(), "script registry not saved") {
		t.Fatalf("failure not logged: %q", logs.String())
	}
	entry := a.watch.Entries()[0]
	if len(a.scanner.indexScripts(context.Background(), entry, nil)) != 1 || calls != 1 {
		t.Fatalf("in-memory registry lost: %d derivations", calls)
	}
	unblock()
	a.scanner.scriptMu.Lock()
	delete(a.scanner.scripts, watchKeyID(entry.Key))
	a.scanner.scriptMu.Unlock()
	if len(a.scanner.indexScripts(context.Background(), entry, nil)) != 1 {
		t.Fatal("scripts missing after space returned")
	}
	if b, _ := os.ReadFile(a.scanner.scriptsPath()); !strings.Contains(string(b), indexTestScript) {
		t.Fatalf("registry not written after space returned: %s", b)
	}
}
