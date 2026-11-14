package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

const seedTestCoins = 20000

var seedTestZeroHash = strings.Repeat("0", 64)

type fakeCore struct {
	dir     string
	data    []byte
	res     seedDumpResult
	err     error
	skip    bool
	stallAt int
	onCall  func()

	stalled chan struct{}
	release chan struct{}
	done    chan struct{}

	mu       sync.Mutex
	writeErr error
	calls    int
}

func newFakeCore(t *testing.T) *fakeCore {
	t.Helper()
	requireLinuxFIFO(t)
	data, err := io.ReadAll(utxoTestRandomDump(seedTestCoins))
	if err != nil {
		t.Fatal(err)
	}
	return &fakeCore{data: data, res: seedDumpResult{CoinsWritten: seedTestCoins, BaseHash: seedTestZeroHash, BaseHeight: 7},
		stalled: make(chan struct{}), release: make(chan struct{}), done: make(chan struct{})}
}

func (c *fakeCore) call(_ context.Context, method string, params any, out any) error {
	if method != "dumptxoutset" {
		return errors.New("unexpected " + method)
	}
	defer close(c.done)
	c.mu.Lock()
	c.calls++
	c.mu.Unlock()
	if args, ok := params.([]any); !ok || len(args) != 2 || args[0] != seedDumpName || args[1] != "latest" {
		return fmt.Errorf("unexpected params %v", params)
	}
	if c.onCall != nil {
		c.onCall()
	}
	if c.skip {
		return c.err
	}
	f, err := os.OpenFile(filepath.Join(c.dir, seedIncompleteName), os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	stalled := false
	for off := 0; off < len(c.data); {
		if c.stallAt > 0 && off >= c.stallAt && !stalled {
			stalled = true
			close(c.stalled)
			<-c.release
		}
		end := min(off+32<<10, len(c.data))
		if _, err := f.Write(c.data[off:end]); err != nil {
			c.mu.Lock()
			c.writeErr = err
			c.mu.Unlock()
			return errors.New("write failed")
		}
		off = end
	}
	if c.err != nil {
		return c.err
	}
	_ = os.Rename(filepath.Join(c.dir, seedIncompleteName), filepath.Join(c.dir, seedDumpName))
	*out.(*seedDumpResult) = c.res
	return nil
}

type seedFixture struct {
	m       *utxoManager
	core    *fakeCore
	state   string
	data    string
	holds   atomic.Int32
	release atomic.Int32
	holdOK  atomic.Bool
}

func seedTestRequestID(n int) string { return fmt.Sprintf("%032x", n) }

func newSeedFixture(t *testing.T, core *fakeCore) *seedFixture {
	t.Helper()
	utxoTestSmallRuns(t, 1000, 20<<10)
	fx := &seedFixture{core: core, state: t.TempDir(), data: t.TempDir()}
	fx.holdOK.Store(true)
	core.dir = fx.data
	if err := syscall.Mkfifo(filepath.Join(fx.data, seedIncompleteName), 0o600); err != nil {
		t.Fatal(err)
	}
	fx.m = utxoMgrNew(t, t.TempDir(), nil, newFakeUtxoNode(utxoMgrChain(2, "b")), io.Discard)
	fx.m.configureSeed(utxoSeedHost{stateDir: fx.state, dataDir: fx.data, dump: core,
		hold:    func() bool { fx.holds.Add(1); return fx.holdOK.Load() },
		release: func() { fx.release.Add(1) }})
	fx.m.setSeedStatePath(filepath.Join(fx.state, "utxo-seed.json"))
	fx.m.checkEvery = time.Millisecond
	fx.m.temp = func() (float64, error) { return 50, nil }
	fx.m.guards = func(context.Context) seedGuardInput {
		now := time.Now()
		return seedGuardInput{State: utxoStateNotSeeded, TempC: 50, CPUs: 0.5, FreeBytes: 1 << 40,
			Chain: blockchainInfo{Chain: "regtest", Blocks: 1, Headers: 1}, TipTime: now, Now: now}
	}
	return fx
}

func (fx *seedFixture) request(t *testing.T, id string, at time.Time) {
	t.Helper()
	raw, _ := json.Marshal(seedRequest{ID: id, RequestedAt: at.Unix()})
	if err := os.WriteFile(filepath.Join(fx.state, seedRequestName), raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func (fx *seedFixture) persisted(t *testing.T) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(fx.state, "utxo-seed.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

func (fx *seedFixture) assertEmptyStore(t *testing.T) {
	t.Helper()
	if st := fx.m.Status(); st.State != utxoStateNotSeeded || st.IndexTip != nil {
		t.Fatalf("status %+v", st)
	}
	if err := fx.m.currentStore().RequireEmpty(); err != nil {
		t.Fatalf("partial index survived: %v", err)
	}
}

func TestSeedRequestValidation(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	id := seedTestRequestID(0xabc)
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"fresh", fmt.Sprintf(`{"id":%q,"requested_at":%d}`, id, now.Unix()-5), ""},
		{"stale", fmt.Sprintf(`{"id":%q,"requested_at":%d}`, id, now.Unix()-121), "stale"},
		{"edge", fmt.Sprintf(`{"id":%q,"requested_at":%d}`, id, now.Unix()-120), ""},
		{"future", fmt.Sprintf(`{"id":%q,"requested_at":%d}`, id, now.Unix()+60), "stale"},
		{"no time", fmt.Sprintf(`{"id":%q}`, id), "stale"},
		{"short id", fmt.Sprintf(`{"id":"abc","requested_at":%d}`, now.Unix()), "malformed"},
		{"upper id", fmt.Sprintf(`{"id":%q,"requested_at":%d}`, strings.ToUpper(id), now.Unix()), "malformed"},
		{"not json", `nope`, "malformed"},
		{"empty", ``, "malformed"},
	}
	for _, c := range cases {
		_, r := validSeedRequest([]byte(c.raw), now)
		switch {
		case c.want == "" && r != nil, c.want != "" && (r == nil || !strings.Contains(r.Detail, c.want)):
			t.Errorf("%s: refusal %v, want %q", c.name, r, c.want)
		}
	}
}

func TestSeedPollConsumesRequestAndIgnoresDuplicate(t *testing.T) {
	fx := newSeedFixture(t, newFakeCore(t))
	calls := 0
	fx.m.guards = func(context.Context) seedGuardInput { calls++; return seedGuardInput{State: utxoStateInvalid} }
	reqPath := filepath.Join(fx.state, seedRequestName)

	fx.m.pollSeed(t.Context())
	if calls != 0 {
		t.Fatal("guards ran without a request")
	}
	id := seedTestRequestID(2)
	fx.request(t, id, time.Now())
	fx.m.pollSeed(t.Context())
	if _, err := os.Stat(reqPath); !os.IsNotExist(err) {
		t.Fatal("request file was not consumed")
	}
	if calls != 1 || fx.persisted(t)["request_id"] != id {
		t.Fatalf("calls %d, persisted %v", calls, fx.persisted(t))
	}
	fx.request(t, id, time.Now())
	fx.m.pollSeed(t.Context())
	if calls != 1 {
		t.Fatal("a duplicate request id ran again")
	}
	if _, err := os.Stat(reqPath); !os.IsNotExist(err) {
		t.Fatal("duplicate request file was left behind")
	}
	fx.request(t, seedTestRequestID(3), time.Now().Add(-5*time.Minute))
	fx.m.pollSeed(t.Context())
	doc := fx.persisted(t)
	if calls != 1 || doc["seed_state"] != utxoSeedRefused || doc["request_id"] != seedTestRequestID(3) {
		t.Fatalf("stale request: calls %d doc %v", calls, doc)
	}
	if err := os.WriteFile(reqPath, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	fx.m.pollSeed(t.Context())
	if st := fx.m.Status(); len(st.Refusals) != 1 || st.Refusals[0].Guard != "request" || calls != 1 {
		t.Fatalf("malformed request: %+v", st.Refusals)
	}
	if err := os.WriteFile(reqPath, make([]byte, seedRequestMaxBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	fx.m.pollSeed(t.Context())
	if _, err := os.Stat(reqPath); !os.IsNotExist(err) || calls != 1 {
		t.Fatal("oversized request was not discarded")
	}
}

func TestSeedPollListsEveryRefusal(t *testing.T) {
	fx := newSeedFixture(t, newFakeCore(t))
	fx.m.guards = func(context.Context) seedGuardInput {
		return seedGuardInput{State: utxoStateReady, TempErr: errors.New("x"), CPUErr: errors.New("x"), FreeErr: errors.New("x"),
			ChainErr: errors.New("x"), RPCErr: errors.New("x")}
	}
	if err := os.Remove(filepath.Join(fx.data, seedIncompleteName)); err != nil {
		t.Fatal(err)
	}
	fx.request(t, seedTestRequestID(4), time.Now())
	fx.m.pollSeed(t.Context())
	st := fx.m.Status()
	var names []string
	for _, r := range st.Refusals {
		names = append(names, r.Guard)
	}
	if got := strings.Join(names, " "); got != "state thermal cpu_cap free_space node rpc_idle fifo" {
		t.Fatalf("refusals = %s", got)
	}
	if fx.holds.Load() != 0 || fx.core.calls != 0 || st.State != utxoStateNotSeeded {
		t.Fatalf("a refused request took the scanner or the dump: holds %d calls %d", fx.holds.Load(), fx.core.calls)
	}
	doc := fx.persisted(t)
	if doc["seed_state"] != utxoSeedRefused || len(doc["refusals"].([]any)) != 7 {
		t.Fatalf("persisted %v", doc)
	}
	raw, _ := json.Marshal(doc)
	if strings.Contains(string(raw), "address") || strings.Contains(string(raw), "descriptor") {
		t.Fatalf("status file carries wallet data: %s", raw)
	}
}

func TestSeedPollRefusesWhenScannerBusyOrChainUnknown(t *testing.T) {
	fx := newSeedFixture(t, newFakeCore(t))
	fx.holdOK.Store(false)
	fx.request(t, seedTestRequestID(5), time.Now())
	fx.m.pollSeed(t.Context())
	if st := fx.m.Status(); len(st.Refusals) != 1 || st.Refusals[0].Guard != "scanner" || fx.release.Load() != 0 {
		t.Fatalf("%+v", st.Refusals)
	}
	base := fx.m.guards
	fx.m.guards = func(ctx context.Context) seedGuardInput {
		in := base(ctx)
		in.Chain.Chain = "signet"
		return in
	}
	fx.request(t, seedTestRequestID(6), time.Now())
	fx.m.pollSeed(t.Context())
	if st := fx.m.Status(); len(st.Refusals) != 1 || st.Refusals[0].Guard != "node" {
		t.Fatalf("%+v", st.Refusals)
	}
}

func TestSeedFIFOGuard(t *testing.T) {
	dir := t.TempDir()
	if r := seedFIFOGuard(dir); r == nil || !strings.Contains(r.Detail, "does not exist") {
		t.Fatalf("missing pipe: %v", r)
	}
	inc := filepath.Join(dir, seedIncompleteName)
	if err := os.WriteFile(inc, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if r := seedFIFOGuard(dir); r == nil || !strings.Contains(r.Detail, "not a named pipe") {
		t.Fatalf("regular file: %v", r)
	}
	if _, err := openSeedFIFO(dir, nil); err == nil || !strings.Contains(err.Error(), "not a named pipe") {
		t.Fatalf("open on a regular file: %v", err)
	}
	_ = os.Remove(inc)
	if err := os.Symlink("/dev/null", inc); err != nil {
		t.Fatal(err)
	}
	if r := seedFIFOGuard(dir); r == nil {
		t.Fatal("a symlink passed as a named pipe")
	}
	_ = os.Remove(inc)
	if err := syscall.Mkfifo(inc, 0o600); err != nil {
		t.Fatal(err)
	}
	if r := seedFIFOGuard(dir); r != nil {
		t.Fatalf("pipe refused: %v", r)
	}
	if err := os.WriteFile(filepath.Join(dir, seedDumpName), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if r := seedFIFOGuard(dir); r == nil || !strings.Contains(r.Detail, "already exists") {
		t.Fatalf("stale dump: %v", r)
	}
}

func requireLinuxFIFO(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("the seed reader is Linux only; kqueue does not wake a pending read on a FIFO")
	}
}

func seedTestFIFODir(t *testing.T) string {
	t.Helper()
	requireLinuxFIFO(t)
	dir := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(dir, seedIncompleteName), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestSeedFIFOReaderEOFAndKeeper(t *testing.T) {
	dir := seedTestFIFODir(t)
	var first atomic.Int32
	f, err := openSeedFIFO(dir, func() { first.Add(1) })
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if f.keeper == nil {
		t.Fatal("no keeper writer")
	}
	got := make(chan []byte, 1)
	go func() {
		b, err := io.ReadAll(f)
		if err != nil {
			t.Log("read error:", err)
		}
		got <- b
	}()
	select {
	case <-got:
		t.Fatal("EOF before Core wrote anything: the keeper did not hold the pipe")
	case <-time.After(100 * time.Millisecond):
	}
	w, err := os.OpenFile(filepath.Join(dir, seedIncompleteName), os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte(strings.Repeat("coins", 40000))
	if _, err := w.Write(payload); err != nil {
		t.Fatal(err)
	}
	_ = w.Close()
	select {
	case b := <-got:
		if string(b) != string(payload) {
			t.Fatalf("read %d bytes, want %d", len(b), len(payload))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no EOF after the writer closed: the keeper stayed open")
	}
	if first.Load() != 1 {
		t.Fatalf("first-byte callback ran %d times", first.Load())
	}
	f.mu.Lock()
	kept := f.keeper
	f.mu.Unlock()
	if kept != nil {
		t.Fatal("keeper still open after the first byte")
	}
}

func TestSeedFIFOReaderClosesEarly(t *testing.T) {
	dir := seedTestFIFODir(t)
	f, err := openSeedFIFO(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	w, err := os.OpenFile(filepath.Join(dir, seedIncompleteName), os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	readErr := make(chan error, 1)
	go func() { _, err := f.Read(make([]byte, 16)); readErr <- err }()
	time.Sleep(50 * time.Millisecond)
	_ = f.Close()
	select {
	case err := <-readErr:
		if err == nil {
			t.Fatal("blocked read returned no error after close")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("close did not unblock the reader")
	}
	if _, err := w.Write([]byte("late")); !errors.Is(err, syscall.EPIPE) {
		t.Fatalf("writer error = %v, want EPIPE", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
}

func TestSeedFromDumpSucceeds(t *testing.T) {
	core := newFakeCore(t)
	fx := newSeedFixture(t, core)
	var during struct {
		suppressed bool
		state      string
		phase      string
	}
	core.onCall = func() {
		st := fx.m.Status()
		during.suppressed, during.state, during.phase = fx.m.AlertsSuppressed(), st.State, st.Phase
	}
	if fx.m.AlertsSuppressed() {
		t.Fatal("alerts suppressed before the seed")
	}
	id := seedTestRequestID(7)
	fx.request(t, id, time.Now())
	fx.m.pollSeed(t.Context())
	if !during.suppressed || during.state != utxoStateSeeding || during.phase != utxoPhaseWaiting {
		t.Fatalf("at dump time: %+v", during)
	}
	st := fx.m.Status()
	if st.State != utxoStateCatching || st.LastSeed == nil || st.LastSeed.Outcome != "ok" || st.LastSeed.Reason != "" || st.Phase != "" {
		t.Fatalf("status %+v", st)
	}
	h, hash, ok, err := fx.m.currentStore().Tip()
	if err != nil || !ok || h != 7 || hash != ([32]byte{}) {
		t.Fatalf("tip %d %v %v", h, ok, err)
	}
	if fx.holds.Load() != 1 || fx.release.Load() != 1 {
		t.Fatalf("scanner hold %d release %d", fx.holds.Load(), fx.release.Load())
	}
	doc := fx.persisted(t)
	if doc["seed_state"] != "ok" || doc["request_id"] != id {
		t.Fatalf("persisted %v", doc)
	}
	fx.request(t, id, time.Now())
	fx.m.pollSeed(t.Context())
	if core.calls != 1 {
		t.Fatal("the same request seeded twice")
	}
}

func TestSeedFromDumpMismatchDiscardsIndex(t *testing.T) {
	for name, mutate := range map[string]func(*seedDumpResult){
		"hash":  func(r *seedDumpResult) { r.BaseHash = strings.Repeat("ff", 32) },
		"coins": func(r *seedDumpResult) { r.CoinsWritten++ },
		"bad":   func(r *seedDumpResult) { r.BaseHash = "zz" },
	} {
		t.Run(name, func(t *testing.T) {
			core := newFakeCore(t)
			mutate(&core.res)
			fx := newSeedFixture(t, core)
			fx.request(t, seedTestRequestID(8), time.Now())
			fx.m.pollSeed(t.Context())
			fx.assertEmptyStore(t)
			if st := fx.m.Status(); st.LastSeed.Outcome != "failed" || st.LastSeed.Reason != errSeedMismatch.Error() {
				t.Fatalf("last seed %+v", st.LastSeed)
			}
			if fx.release.Load() != 1 {
				t.Fatal("scanner not released")
			}
		})
	}
}

func TestSeedFromDumpRPCFailureBeforeWriting(t *testing.T) {
	core := newFakeCore(t)
	core.skip, core.err = true, errors.New("RPC dumptxoutset returned an error")
	fx := newSeedFixture(t, core)
	fx.request(t, seedTestRequestID(9), time.Now())
	done := make(chan struct{})
	go func() { fx.m.pollSeed(t.Context()); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the seed hung waiting for a pipe that nothing writes")
	}
	fx.assertEmptyStore(t)
	if st := fx.m.Status(); st.LastSeed.Outcome != "failed" || st.LastSeed.Reason != errSeedDump.Error() {
		t.Fatalf("last seed %+v", st.LastSeed)
	}
}

func TestSeedFromDumpCancelClosesReader(t *testing.T) {
	core := newFakeCore(t)
	core.stallAt = 100 << 10
	fx := newSeedFixture(t, core)
	fx.request(t, seedTestRequestID(10), time.Now())
	done := make(chan struct{})
	go func() { fx.m.pollSeed(t.Context()); close(done) }()
	select {
	case <-core.stalled:
	case <-time.After(10 * time.Second):
		t.Fatal("dump never started")
	}
	if err := os.WriteFile(filepath.Join(fx.state, seedCancelName), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("cancel did not stop the seed")
	}
	fx.assertEmptyStore(t)
	if st := fx.m.Status(); st.LastSeed.Outcome != "cancelled" || st.LastSeed.Reason != "" {
		t.Fatalf("last seed %+v", st.LastSeed)
	}
	if _, err := os.Stat(filepath.Join(fx.state, seedCancelName)); !os.IsNotExist(err) {
		t.Fatal("cancel file not consumed")
	}
	close(core.release)
	<-core.done
	core.mu.Lock()
	defer core.mu.Unlock()
	if !errors.Is(core.writeErr, syscall.EPIPE) {
		t.Fatalf("Core's write error = %v, want EPIPE from the closed reader", core.writeErr)
	}
}

func TestSeedStaleCancelIsClearedAndDoesNotCancelNextSeed(t *testing.T) {
	core := newFakeCore(t)
	fx := newSeedFixture(t, core)
	cancelPath := filepath.Join(fx.state, seedCancelName)
	if err := os.WriteFile(cancelPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	fx.m.pollSeed(t.Context())
	if _, err := os.Stat(cancelPath); !os.IsNotExist(err) {
		t.Fatal("idle poll left a stale cancel file")
	}
	if err := os.WriteFile(cancelPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	fx.request(t, seedTestRequestID(11), time.Now())
	fx.m.pollSeed(t.Context())
	if st := fx.m.Status(); st.LastSeed == nil || st.LastSeed.Outcome != "ok" {
		t.Fatalf("a cancel written before the seed aborted it: %+v", st.LastSeed)
	}
}

func TestSeedThermalPauseAndResume(t *testing.T) {
	core := newFakeCore(t)
	fx := newSeedFixture(t, core)
	var mu sync.Mutex
	var seen []string
	temps := []float64{50, 90, 80, 71.9}
	n := 0
	fx.m.temp = func() (float64, error) {
		mu.Lock()
		defer mu.Unlock()
		seen = append(seen, fx.m.Status().Phase)
		n++
		if n <= len(temps) {
			return temps[n-1], nil
		}
		return 50, nil
	}
	fx.request(t, seedTestRequestID(12), time.Now())
	fx.m.pollSeed(t.Context())
	mu.Lock()
	defer mu.Unlock()
	if len(seen) < 5 {
		t.Fatalf("temperature read %d times", len(seen))
	}
	if seen[1] != utxoPhaseImporting || seen[2] != utxoPhasePaused || seen[3] != utxoPhasePaused || seen[4] == utxoPhasePaused {
		t.Fatalf("phases at each reading: %v", seen)
	}
	if st := fx.m.Status(); st.LastSeed == nil || st.LastSeed.Outcome != "ok" {
		t.Fatalf("seed after pause: %+v", st.LastSeed)
	}
}

func TestSeedThermalSensorLossPausesAndCancelEnds(t *testing.T) {
	core := newFakeCore(t)
	fx := newSeedFixture(t, core)
	var lost atomic.Bool
	n := 0
	fx.m.temp = func() (float64, error) {
		if n++; n > 1 {
			lost.Store(true)
			return 0, errors.New("sensor gone")
		}
		return 50, nil
	}
	fx.request(t, seedTestRequestID(13), time.Now())
	done := make(chan struct{})
	go func() { fx.m.pollSeed(t.Context()); close(done) }()
	for i := 0; !lost.Load() || fx.m.Status().Phase != utxoPhasePaused; i++ {
		if i > 2000 {
			t.Fatal("never paused on a lost sensor")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := os.WriteFile(filepath.Join(fx.state, seedCancelName), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("cancel did not end a paused seed")
	}
	fx.assertEmptyStore(t)
	if fx.m.Status().LastSeed.Outcome != "cancelled" {
		t.Fatalf("%+v", fx.m.Status().LastSeed)
	}
}

func TestSeedLowDiskAborts(t *testing.T) {
	core := newFakeCore(t)
	fx := newSeedFixture(t, core)
	var calls atomic.Int32
	fx.m.freeBytes = func() uint64 {
		if calls.Add(1) > 1 {
			return utxoMinFreeBytes - 1
		}
		return 1 << 40
	}
	fx.request(t, seedTestRequestID(14), time.Now())
	fx.m.pollSeed(t.Context())
	fx.assertEmptyStore(t)
	if st := fx.m.Status(); st.LastSeed.Outcome != "failed" || st.LastSeed.Reason != errSeedLowDisk.Error() {
		t.Fatalf("last seed %+v", st.LastSeed)
	}
}

func TestSeedThermalRecheckBeforeDump(t *testing.T) {
	core := newFakeCore(t)
	fx := newSeedFixture(t, core)
	fx.m.temp = func() (float64, error) { return 80, nil }
	fx.request(t, seedTestRequestID(15), time.Now())
	fx.m.pollSeed(t.Context())
	if core.calls != 0 {
		t.Fatal("dump issued on a hot host")
	}
	if st := fx.m.Status(); st.LastSeed == nil || st.LastSeed.Reason != errSeedThermal.Error() {
		t.Fatalf("last seed %+v", st.LastSeed)
	}
	fx.assertEmptyStore(t)
}

func TestSeedBootRecovery(t *testing.T) {
	for _, state := range []string{utxoSeedRunning, utxoSeedChecking} {
		t.Run(state, func(t *testing.T) {
			c := utxoMgrChain(10, "b")
			dir := t.TempDir()
			utxoMgrSeed(t, dir, nil, c)
			path := filepath.Join(t.TempDir(), "utxo-seed.json")
			now := time.Now()
			raw, _ := json.Marshal(utxoSeedState{RequestID: seedTestRequestID(16), SeedState: state, Phase: utxoPhaseImporting,
				StartedAt: now.Add(-time.Minute), AlertsUntil: now.Add(time.Minute)})
			if err := os.WriteFile(path, raw, 0o600); err != nil {
				t.Fatal(err)
			}
			m := utxoMgrNew(t, dir, nil, newFakeUtxoNode(c), io.Discard)
			if _, _, has, _ := m.store.Tip(); !has {
				t.Fatal("fixture has no partial index")
			}
			m.setSeedStatePath(path)
			st := m.Status()
			if m.AlertsSuppressed() || st.Phase != "" || st.SeedProgress != nil {
				t.Fatalf("status %+v", st)
			}
			var doc map[string]any
			raw, _ = os.ReadFile(path)
			_ = json.Unmarshal(raw, &doc)
			if doc["seed_state"] != "failed" || doc["request_id"] != seedTestRequestID(16) {
				t.Fatalf("persisted %v", doc)
			}
			if state == utxoSeedRunning {
				if st.State != utxoStateNotSeeded || st.IndexTip != nil || st.LastSeed == nil || st.LastSeed.Reason != errSeedInterrupted.Error() {
					t.Fatalf("running: %+v", st)
				}
				if _, _, has, _ := m.currentStore().Tip(); has {
					t.Fatal("partial index not discarded")
				}
			} else if st.State != utxoStateCatching {
				t.Fatalf("checking must not wipe: %+v", st)
			}
		})
	}
}

func TestSeedRunLoopPollsRequestFile(t *testing.T) {
	core := newFakeCore(t)
	fx := newSeedFixture(t, core)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { fx.m.run(ctx); close(done) }()
	fx.request(t, seedTestRequestID(17), time.Now())
	deadline := time.Now().Add(15 * time.Second)
	for fx.m.Status().LastSeed == nil {
		if time.Now().After(deadline) {
			t.Fatal("the run loop never picked up the request")
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	<-done
	if core.calls != 1 {
		t.Fatalf("dump calls %d", core.calls)
	}
}

func TestSeedErrorClass(t *testing.T) {
	if seedErrorClass(nil) != "" || seedErrorClass(context.Canceled) != "" {
		t.Fatal("success or cancel carries a reason")
	}
	if got := seedErrorClass(fmt.Errorf("wrapped: %w", errSeedLowDisk)); got != errSeedLowDisk.Error() {
		t.Fatal(got)
	}
	if got := seedErrorClass(errors.New("coin 5 of 9 for script deadbeef")); got != "seed failed" {
		t.Fatalf("unclassified error leaked detail: %q", got)
	}
}

func seedUnparkWriter(t *testing.T, dir string) {
	t.Helper()
	fd, err := syscall.Open(filepath.Join(dir, seedIncompleteName), syscall.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err == nil {
		time.Sleep(50 * time.Millisecond)
		_ = syscall.Close(fd)
	}
}

func TestSeedWaitsThroughSilentHashPass(t *testing.T) {
	core := newFakeCore(t)
	fx := newSeedFixture(t, core)
	core.onCall = func() { time.Sleep(200 * fx.m.checkEvery) }
	fx.request(t, seedTestRequestID(20), time.Now())
	fx.m.pollSeed(t.Context())
	if st := fx.m.Status(); st.LastSeed == nil || st.LastSeed.Outcome != "ok" {
		t.Fatalf("last seed %+v", st.LastSeed)
	}
}

func TestSeedAbortsWhenPipeIsReplacedBeforeFirstByte(t *testing.T) {
	core := newFakeCore(t)
	gate := make(chan struct{})
	fx := newSeedFixture(t, core)
	path := filepath.Join(fx.data, seedIncompleteName)
	core.onCall = func() {
		_ = os.Remove(path)
		_ = syscall.Mkfifo(path, 0o600)
		<-gate
	}
	fx.request(t, seedTestRequestID(21), time.Now())
	fx.m.pollSeed(t.Context())
	fx.assertEmptyStore(t)
	if st := fx.m.Status(); st.LastSeed.Outcome != "failed" || st.LastSeed.Reason != errSeedPipeGone.Error() {
		t.Fatalf("last seed %+v", st.LastSeed)
	}
	core.skip = true
	close(gate)
	<-core.done
}

func TestSeedCancelDuringChecksIsHonoured(t *testing.T) {
	core := newFakeCore(t)
	fx := newSeedFixture(t, core)
	guards := fx.m.guards
	fx.m.guards = func(ctx context.Context) seedGuardInput {
		_ = os.WriteFile(filepath.Join(fx.state, seedCancelName), nil, 0o600)
		return guards(ctx)
	}
	fx.request(t, seedTestRequestID(22), time.Now())
	fx.m.pollSeed(t.Context())
	fx.assertEmptyStore(t)
	if st := fx.m.Status(); st.LastSeed == nil || st.LastSeed.Outcome != "cancelled" {
		t.Fatalf("a cancel during the checks was discarded: %+v", st.LastSeed)
	}
	seedUnparkWriter(t, fx.data)
}
