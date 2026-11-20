package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	seedRequestName     = "utxo-seed.request"
	seedCancelName      = "utxo-seed.cancel"
	seedDumpName        = "utxo-seed.dat"
	seedIncompleteName  = seedDumpName + ".incomplete"
	seedRequestFreshFor = 120 * time.Second
	seedPollEvery       = 2 * time.Second
	seedCheckEvery      = time.Second
	seedRequestMaxBytes = 4096
	utxoAutoSeedEvery   = time.Hour
	utxoAutoSeedTries   = 3

	utxoSeedChecking = "checking"
	utxoSeedRunning  = "seeding"
	utxoSeedRefused  = "refused"

	utxoOutcomeFailed = "failed"
)

var (
	errSeedDump        = errors.New("dumptxoutset failed")
	errSeedLowDisk     = errors.New("less than 2 GB free during the seed")
	errSeedMismatch    = errors.New("dump does not match the dumptxoutset result")
	errSeedThermal     = errors.New("temperature rose above the start limit before the dump")
	errSeedInterrupted = errors.New("dashboard restarted during the seed")
	errSeedPipeGone    = errors.New("the seed pipe was replaced during the seed")
	errSeedChainstate  = errors.New("the chainstate copy could not be read")
	errSeedOffChain    = errors.New("the chainstate copy's best block is not on the node's chain")
	errSeedUserCancel  = fmt.Errorf("%w by the user", context.Canceled)
)

// prefix names the request and cancel files per chain; lock is shared by every index so two seeds never overlap.
type utxoSeedHost struct {
	prefix   string
	stateDir string
	dataDir  string
	dump     utxoRPC
	hold     func() bool
	release  func()
	lock     *sync.Mutex
	autoSeed func() (id, refusal string)
	helper   func() bool
	// autoResult reports node-control's answer to a seed request it was sent; otherSeedActive reports a seed running in any index.
	autoResult      func(id string) (hostSeedResult, bool)
	otherSeedActive func() bool
}

type hostSeedResult struct {
	State, Code, Detail string
}

// A failed automatic seed stalls the node's RPC for minutes, so retries back off and stop after utxoAutoSeedTries.
var utxoAutoSeedBackoff = []time.Duration{6 * time.Hour, 24 * time.Hour}

// A node restart, an RPC timeout or a reorg near the tip is retried within the replay; only this many attempts in a row without progress make it wait for the hourly check.
var (
	utxoReplayRetries   = 30
	utxoReplayRetryWait = utxoFollowInterval
)

type seedRequest struct {
	ID          string `json:"id"`
	RequestedAt int64  `json:"requested_at"`
	Mode        string `json:"mode,omitempty"`
}

const (
	seedModeChainstate = "chainstate"
	seedModeReplay     = "replay"
)

// seedMode resolves how a seed runs: the mode a request names when the chain supports it, else a chainstate copy when the host helper can take one
// (a chain with no replay always uses it), else a replay of every block, else a dumptxoutset seed, named by "".
func (m *utxoManager) seedMode(asked string) string {
	switch {
	case asked == seedModeChainstate && m.chain.Chainstate, asked == seedModeReplay && m.chain.Replay:
		return asked
	case m.chain.Chainstate && (!m.chain.Replay || m.host != nil && m.host.helper != nil && m.host.helper()):
		return seedModeChainstate
	case m.chain.Replay:
		return seedModeReplay
	}
	return ""
}

// replayingLocked reports whether the seed running now, or else the next one, replays every block.
func (m *utxoManager) replayingLocked() bool {
	if m.seed.SeedState == utxoSeedChecking || m.seed.SeedState == utxoSeedRunning {
		return m.seed.Mode == seedModeReplay || m.seed.Mode == "" && m.chain.Replay
	}
	return m.seedMode("") == seedModeReplay
}

func (m *utxoManager) replays() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.replayingLocked()
}

type seedDumpResult struct {
	CoinsWritten uint64 `json:"coins_written"`
	BaseHash     string `json:"base_hash"`
	BaseHeight   uint32 `json:"base_height"`
}

type seedDumpOutcome struct {
	res seedDumpResult
	err error
}

func (m *utxoManager) configureSeed(h utxoSeedHost) {
	m.host = &h
}

func seedErrorClass(err error) string {
	for _, target := range []error{errSeedDump, errSeedLowDisk, errSeedMismatch, errSeedThermal, errSeedInterrupted, errSeedPipeGone, errSeedChainstate, errSeedOffChain, errUtxoBlockTooLarge} {
		if errors.Is(err, target) {
			return target.Error()
		}
	}
	if err == nil || errors.Is(err, context.Canceled) {
		return ""
	}
	return "seed failed"
}

func seedFIFOGuard(dir string) *seedRefusal {
	if _, err := os.Lstat(filepath.Join(dir, seedDumpName)); err == nil {
		return &seedRefusal{"fifo", seedDumpName + " already exists"}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return &seedRefusal{"fifo", "the node data directory cannot be inspected"}
	}
	fi, err := os.Lstat(filepath.Join(dir, seedIncompleteName))
	switch {
	case err != nil:
		return &seedRefusal{"fifo", "the seed pipe does not exist"}
	case fi.Mode()&os.ModeNamedPipe == 0:
		return &seedRefusal{"fifo", "the seed path is not a named pipe"}
	}
	st, err := os.Stat(dir)
	if err != nil {
		return &seedRefusal{"fifo", "the node data directory cannot be inspected"}
	}
	sys, ok := st.Sys().(*syscall.Stat_t)
	if !ok || os.Getuid() != 0 && int(sys.Uid) != os.Getuid() {
		return &seedRefusal{"fifo", "the dashboard user does not own the node data directory"}
	}
	return nil
}

type seedFIFO struct {
	r       *os.File
	path    string
	onFirst func()
	started chan struct{}

	mu     sync.Mutex
	keeper *os.File
	first  sync.Once
	closed sync.Once
}

func openSeedFIFO(dir string, onFirst func()) (*seedFIFO, error) {
	if r := seedFIFOGuard(dir); r != nil {
		return nil, errors.New(r.Detail)
	}
	path := filepath.Join(dir, seedIncompleteName)
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, errors.New("the seed pipe cannot be opened for reading")
	}
	r := os.NewFile(uintptr(fd), path)
	if fi, err := r.Stat(); err != nil || fi.Mode()&os.ModeNamedPipe == 0 {
		_ = r.Close()
		return nil, errors.New("the seed path is not a named pipe")
	}
	keeper, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		_ = r.Close()
		return nil, errors.New("the seed pipe keeper cannot be opened")
	}
	return &seedFIFO{r: r, path: path, keeper: keeper, onFirst: onFirst, started: make(chan struct{})}, nil
}

func (f *seedFIFO) Read(p []byte) (int, error) {
	n, err := f.r.Read(p)
	if n > 0 {
		f.first.Do(func() {
			close(f.started)
			f.closeKeeper()
			if f.onFirst != nil {
				f.onFirst()
			}
		})
	}
	return n, err
}

func (f *seedFIFO) intact() bool {
	a, err := f.r.Stat()
	if err != nil {
		return false
	}
	// Core renames a finished dump to its final name, which a tiny dump can do before the first read returns.
	for _, name := range []string{f.path, filepath.Join(filepath.Dir(f.path), seedDumpName)} {
		if b, err := os.Lstat(name); err == nil && b.Mode()&os.ModeNamedPipe != 0 && os.SameFile(a, b) {
			return true
		}
	}
	return false
}

func (f *seedFIFO) closeKeeper() {
	f.mu.Lock()
	k := f.keeper
	f.keeper = nil
	f.mu.Unlock()
	if k != nil {
		_ = k.Close()
	}
}

func (f *seedFIFO) Close() error {
	var err error
	f.closed.Do(func() {
		f.closeKeeper()
		err = f.r.Close()
	})
	return err
}

func validSeedRequest(raw []byte, now time.Time) (seedRequest, *seedRefusal) {
	var req seedRequest
	if json.Unmarshal(raw, &req) != nil || !validPortControlID(req.ID) {
		return seedRequest{}, &seedRefusal{"request", "the seed request is malformed"}
	}
	if req.Mode != "" && req.Mode != seedModeChainstate && req.Mode != seedModeReplay {
		return seedRequest{}, &seedRefusal{"request", "the seed request is malformed"}
	}
	age := now.Unix() - req.RequestedAt
	if req.RequestedAt <= 0 || age > int64(seedRequestFreshFor/time.Second) || age < -30 {
		return req, &seedRefusal{"request", "the seed request is stale"}
	}
	return req, nil
}

func (m *utxoManager) takeSeedRequest() (req seedRequest, present bool, refusal *seedRefusal) {
	path := filepath.Join(m.host.stateDir, m.host.prefix+seedRequestName)
	f, err := os.Open(path)
	if err != nil {
		return req, false, nil
	}
	raw, _ := io.ReadAll(io.LimitReader(f, seedRequestMaxBytes+1))
	_ = f.Close()
	_ = os.Remove(path)
	if len(raw) > seedRequestMaxBytes {
		return req, true, &seedRefusal{"request", "the seed request is malformed"}
	}
	req, refusal = validSeedRequest(raw, m.now())
	return req, true, refusal
}

func (m *utxoManager) markSeedRequest(id, state string, refusals []seedRefusal) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.seed.RequestID, m.seed.SeedState, m.refusals = id, state, refusals
	m.persistSeedLocked()
}

func (m *utxoManager) lastSeedRequest() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.seed.RequestID
}

func (m *utxoManager) pollSeed(ctx context.Context) {
	h := m.host
	req, present, refusal := m.takeSeedRequest()
	if !present {
		_ = os.Remove(filepath.Join(h.stateDir, h.prefix+seedCancelName))
		return
	}
	if refusal != nil {
		m.markSeedRequest(req.ID, utxoSeedRefused, []seedRefusal{*refusal})
		return
	}
	if req.ID == m.lastSeedRequest() {
		return
	}
	m.mu.Lock()
	if req.ID != m.seed.AutoRequestID {
		m.seed.AutoFailures = 0
	}
	m.mu.Unlock()
	m.seedRequestedAs(ctx, req.ID, req.Mode)
}

func (m *utxoManager) seedRequested(ctx context.Context, id string) {
	m.seedRequestedAs(ctx, id, "")
}

func (m *utxoManager) seedRequestedAs(ctx context.Context, id, asked string) {
	h := m.host
	_ = os.Remove(filepath.Join(h.stateDir, h.prefix+seedCancelName))
	mode := m.seedMode(asked)
	m.mu.Lock()
	m.seed.Mode = mode
	m.waiting = ""
	m.mu.Unlock()
	m.markSeedRequest(id, utxoSeedChecking, nil)
	network, refusals := m.checkSeed(ctx, mode)
	seed := func(ctx context.Context) error { return m.seedFromDump(ctx, network) }
	switch mode {
	case seedModeReplay:
		seed = m.seedFromReplay
	case seedModeChainstate:
		seed = m.seedFromChainstate
		// The host helper copied the chainstate while the node was synced and has just restarted it, so its warm-up is no reason to refuse.
		refusals = slices.DeleteFunc(refusals, func(r seedRefusal) bool { return r.Guard == "node" })
	default:
		if r := seedFIFOGuard(h.dataDir); r != nil {
			refusals = append(refusals, *r)
		}
	}
	if len(refusals) == 0 && h.lock != nil {
		if h.lock.TryLock() {
			defer h.lock.Unlock()
		} else {
			refusals = append(refusals, seedRefusal{"lock", "another balance index is seeding"})
		}
	}
	// A replay runs for hours and a chainstate seed reads a file, so the watch keeps scanning through either.
	if len(refusals) == 0 && mode == "" {
		if h.hold() {
			defer h.release()
		} else {
			refusals = append(refusals, seedRefusal{"scanner", "a watch scan is running"})
		}
	}
	if len(refusals) > 0 {
		m.markSeedRequest(id, utxoSeedRefused, refusals)
		return
	}
	if err := m.execSeed(ctx, seed); errors.Is(err, errUtxoSeedState) {
		m.markSeedRequest(id, utxoSeedRefused, []seedRefusal{{"state", "the index state does not allow a seed"}})
	}
}

func (m *utxoManager) checkSeed(ctx context.Context, mode string) ([4]byte, []seedRefusal) {
	in := m.guards(ctx)
	in.Replay, in.Chainstate = mode == seedModeReplay, mode == seedModeChainstate
	refusals := evaluateSeedGuards(in)
	m.mu.Lock()
	m.checked = true
	m.mu.Unlock()
	network, known := m.chain.Networks[in.Chain.Chain]
	if in.ChainErr == nil && !known {
		refusals = append(refusals, seedRefusal{"node", "this chain cannot be seeded"})
	}
	return network, refusals
}

const (
	waitNodeSync  = "node_sync"
	waitNodeStart = "node_start"
	waitOtherSeed = "other_seed"
	waitHostBusy  = "host_busy"
	waitBlocked   = "blocked"

	utxoAutoSeedRecheck   = 30 * time.Second
	utxoAutoSeedHostLimit = 10 * time.Minute
	utxoInvalidReseedWait = 24 * time.Hour
)

func nodeSyncRefusal(r seedRefusal) bool {
	return r.Guard == "node" && !strings.HasPrefix(r.Detail, "the node is pruned") && r.Detail != "this chain cannot be seeded"
}

// waitingFor names what a refused automatic seed is waiting on; only "blocked" needs the owner, so it alone keeps the hourly cadence.
func waitingFor(refusals []seedRefusal) string {
	switch {
	case slices.ContainsFunc(refusals, nodeSyncRefusal):
		return waitNodeSync
	case slices.ContainsFunc(refusals, func(r seedRefusal) bool { return r.Guard == "lock" }):
		return waitOtherSeed
	case slices.ContainsFunc(refusals, func(r seedRefusal) bool { return r.Guard == "thermal" && strings.Contains(r.Detail, "above") }):
		return waitHostBusy
	case len(refusals) > 0:
		return waitBlocked
	}
	return ""
}

func (m *utxoManager) setWaiting(waiting string) {
	m.mu.Lock()
	m.waiting = waiting
	m.mu.Unlock()
}

func (m *utxoManager) autoSeedStartableLocked(now time.Time, replay bool) bool {
	switch {
	case m.state == utxoStateNotSeeded, m.replayBehindLocked():
		return true
	case m.state == utxoStateInvalid && !replay:
		// An index that went invalid right after a good seed is not reseeded at once, which would stop the node again and again.
		return m.seed.Last == nil || m.seed.Last.Outcome != "ok" || now.Sub(m.seed.Last.FinishedAt) >= utxoInvalidReseedWait
	}
	return false
}

// settleAutoHostResult reads node-control's answer to the last automatic seed request: a failure counts towards the stop, a refusal only waits.
func (m *utxoManager) settleAutoHostResult() {
	m.mu.Lock()
	id, since := m.hostPendingID, m.hostPendingAt
	started := id != "" && m.seed.RequestID == id
	now := m.now()
	m.mu.Unlock()
	if id == "" {
		return
	}
	if started || m.host.autoResult == nil || now.Sub(since) > utxoAutoSeedHostLimit {
		m.mu.Lock()
		m.hostPendingID = ""
		m.mu.Unlock()
		return
	}
	res, found := m.host.autoResult(id)
	if !found || res.State == "applied" {
		return
	}
	m.mu.Lock()
	m.hostPendingID = ""
	if res.State == "failed" {
		m.seed.AutoFailures++
		m.seed.Last = &utxoSeedResult{Outcome: "failed", Reason: res.Detail, FinishedAt: now}
		m.waiting = ""
		m.persistSeedLocked()
		m.mu.Unlock()
		return
	}
	switch res.Code {
	case "busy", "seed_running":
		m.waiting = waitOtherSeed
	case "recently_started":
		m.waiting = waitNodeStart
	default:
		m.waiting = waitBlocked
	}
	if m.waiting != waitBlocked {
		m.autoSeedAt = now
	}
	m.mu.Unlock()
	m.markSeedRequest(m.lastSeedRequest(), utxoSeedRefused, []seedRefusal{{"host", res.Detail}})
}

// maybeAutoSeed asks the host for a seed once an hour while the index has none, and every 30 seconds while a node syncs, cools down or another seed runs; a refusal stays in the status.
// A replay chain needs no host, so it starts or resumes its replay here.
func (m *utxoManager) maybeAutoSeed(ctx context.Context) {
	replay := m.replays()
	if m.host.autoSeed == nil && !replay {
		return
	}
	m.settleAutoHostResult()
	m.mu.Lock()
	now, last := m.now(), m.autoSeedAt
	every := utxoAutoSeedEvery
	if m.waiting != "" && m.waiting != waitBlocked {
		every = utxoAutoSeedRecheck
	}
	due := m.autoSeedStartableLocked(now, replay) && !m.seeding && m.hostPendingID == "" && now.Sub(m.autoSeedAt) >= every && m.autoSeedBackedOffLocked(now)
	if due {
		m.autoSeedAt = now
	}
	m.mu.Unlock()
	if !due {
		return
	}
	if replay {
		id := newSeedRequestID()
		m.mu.Lock()
		m.seed.AutoRequestID = id
		m.mu.Unlock()
		m.seedRequestedAs(ctx, id, seedModeReplay)
		m.mu.Lock()
		if m.seed.SeedState == utxoSeedRefused {
			m.waiting = waitingFor(m.refusals)
		}
		m.mu.Unlock()
		return
	}
	_, refusals := m.checkSeed(ctx, m.seedMode(""))
	if len(refusals) == 0 && m.host.otherSeedActive != nil && m.host.otherSeedActive() {
		refusals = append(refusals, seedRefusal{"lock", "another balance index is seeding"})
	}
	if len(refusals) == 0 {
		id, detail := m.host.autoSeed()
		if detail == "" {
			m.mu.Lock()
			m.seed.AutoRequestID, m.hostPendingID, m.hostPendingAt, m.waiting = id, id, now, ""
			m.persistSeedLocked()
			m.mu.Unlock()
			return
		}
		// Another index's seed request is usually gone within seconds, so try again on the next poll.
		if detail == hostChangePending {
			m.mu.Lock()
			m.autoSeedAt, m.waiting = last, waitHostBusy
			m.mu.Unlock()
			return
		}
		refusals = append(refusals, seedRefusal{"host", detail})
	}
	m.setWaiting(waitingFor(refusals))
	m.markSeedRequest(m.lastSeedRequest(), utxoSeedRefused, refusals)
}

func newSeedRequestID() string {
	raw := make([]byte, 16)
	_, _ = rand.Read(raw)
	return hex.EncodeToString(raw)
}

func (m *utxoManager) autoSeedBackedOffLocked(now time.Time) bool {
	f := m.seed.AutoFailures
	if f >= utxoAutoSeedTries {
		return false
	}
	return f == 0 || m.seed.Last == nil || !now.Before(m.seed.Last.FinishedAt.Add(utxoAutoSeedBackoff[f-1]))
}

func (m *utxoManager) countAutoSeedLocked(ok bool) {
	switch {
	case ok:
		m.seed.AutoFailures = 0
	case m.seed.AutoRequestID != "" && m.seed.RequestID == m.seed.AutoRequestID:
		m.seed.AutoFailures++
	}
}

func (m *utxoManager) watchSeedCancel(ctx context.Context, cancel context.CancelCauseFunc) {
	path := filepath.Join(m.host.stateDir, m.host.prefix+seedCancelName)
	t := time.NewTicker(m.checkEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if _, err := os.Stat(path); err == nil {
				_ = os.Remove(path)
				m.setSeedPhase(utxoPhaseCancelling)
				cancel(errSeedUserCancel)
				return
			}
		}
	}
}

// No time limit: on mainnet Core hashes the coin set for about 300 s before it writes the first byte.
func (m *utxoManager) watchSeedFirstByte(ctx context.Context, cancel context.CancelCauseFunc, f *seedFIFO) {
	t := time.NewTicker(m.checkEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-f.started:
			return
		case <-t.C:
			switch {
			case !f.intact():
				cancel(errSeedPipeGone)
				return
			case m.freeBytes() < utxoMinFreeBytes:
				cancel(errSeedLowDisk)
				return
			}
		}
	}
}

// seedFromReplay applies every block from the index tip, or from genesis, through the follower; a stop keeps what it applied.
func (m *utxoManager) seedFromReplay(parent context.Context) error {
	ctx, cancel := context.WithCancelCause(parent)
	defer cancel(nil)
	go m.watchSeedCancel(ctx, cancel)
	m.setSeedPhase(utxoPhaseReplaying)
	gate := &seedGate{m: m, ctx: ctx}
	f := m.follower
	f.gate = gate.checkpoint
	defer func() { f.gate = nil }()
	for retries := 0; ; {
		if err := gate.checkpoint(); err != nil {
			return err
		}
		before := m.replayHeight()
		done, err := m.replayStep(ctx)
		switch {
		case err == nil && done:
			return nil
		case err == nil:
			retries = 0
			continue
		case context.Cause(ctx) != nil:
			return context.Cause(ctx)
		case errors.Is(err, errUtxoBlockTooLarge):
			return err
		case f.Status().State == utxoFollowError || utxoIntegrityError(err) || errors.Is(err, errUtxoChainMismatch) || errors.Is(err, syscall.ENOSPC):
			m.fail(err)
			return err
		}
		// A step that committed blocks before it failed made progress, so only consecutive stalls use up the retries.
		if m.replayHeight() != before {
			retries = 0
		}
		if retries >= utxoReplayRetries {
			return err
		}
		retries++
		m.setSeedPhase(utxoPhaseRetrying)
		select {
		case <-ctx.Done():
			return context.Cause(ctx)
		case <-time.After(utxoReplayRetryWait):
		}
		m.setSeedPhase(utxoPhaseReplaying)
	}
}

func (m *utxoManager) replayHeight() int64 {
	h, _, ok, err := m.currentStore().Tip()
	if err != nil || !ok {
		return -1
	}
	return int64(h)
}

// replayStep applies blocks up to the node's tip and reports whether the index is close enough to hand to the follower.
func (m *utxoManager) replayStep(ctx context.Context) (bool, error) {
	var genesis string
	if err := m.rpc.call(ctx, "getblockhash", []any{uint32(0)}, &genesis); err != nil {
		return false, err
	}
	var info blockchainInfo
	if err := m.rpc.call(ctx, "getblockchaininfo", nil, &info); err != nil {
		return false, err
	}
	store := m.currentStore()
	if err := store.BindChain(info.Chain, genesis); err != nil {
		return false, err
	}
	m.bound = true
	m.mu.Lock()
	m.nodeTip = info.Blocks
	m.mu.Unlock()
	height, _, hasTip, err := store.Tip()
	if err != nil {
		return false, err
	}
	if hasTip && info.Blocks-int64(height) <= utxoReplayGap {
		return true, nil
	}
	return false, m.follower.Step(ctx)
}

type seedGate struct {
	m      *utxoManager
	ctx    context.Context
	prog   *utxoIngestProgress
	last   time.Time
	paused bool
}

func (g *seedGate) phase() string {
	if g.prog == nil {
		return utxoPhaseReplaying
	}
	if t := g.prog.Total.Load(); t > 0 && g.prog.Parsed.Load() >= t {
		return utxoPhaseMerging
	}
	return utxoPhaseImporting
}

func (g *seedGate) checkpoint() error {
	if g.ctx.Err() != nil {
		return context.Cause(g.ctx)
	}
	if g.prog == nil {
		g.m.setSeedProgress(g.m.replayFraction())
	} else if t := g.prog.Total.Load(); t > 0 {
		g.m.setSeedProgress(min(1, float64(g.prog.Parsed.Load()+g.prog.Written.Load())/float64(2*t)))
	}
	if time.Since(g.last) < g.m.checkEvery {
		return nil
	}
	g.last = time.Now()
	if g.m.freeBytes() < utxoMinFreeBytes {
		return errSeedLowDisk
	}
	showed := false
	for {
		c, err := g.m.temp()
		if g.paused = thermalPaused(g.paused, c, err); !g.paused {
			break
		}
		if !showed {
			showed = true
			g.m.setSeedPhase(utxoPhasePaused)
		}
		select {
		case <-g.ctx.Done():
			return context.Cause(g.ctx)
		case <-time.After(g.m.checkEvery):
		}
	}
	if showed {
		g.m.setSeedPhase(g.phase())
	}
	return nil
}

// utxoChainstateCopyName is the directory the host helper fills with the stopped node's chainstate, inside the node's data directory.
const utxoChainstateCopyName = "utxo-seed-chainstate"

func (m *utxoManager) seedFromChainstate(parent context.Context) (err error) {
	ctx, cancel := context.WithCancelCause(parent)
	defer cancel(nil)
	go m.watchSeedCancel(ctx, cancel)
	if c, err := m.temp(); thermalStartGuard(c, err) != nil {
		return errSeedThermal
	}
	// A tampered copy can make Pebble panic; that fails this seed instead of crash-looping the dashboard.
	defer func() {
		if r := recover(); r != nil {
			m.log.Printf("utxo index: reading the chainstate copy panicked: %v", r)
			err = errSeedChainstate
		}
	}()
	copied, err := openChainstateCopy(filepath.Join(m.host.dataDir, utxoChainstateCopyName))
	if err != nil {
		m.log.Printf("utxo index: %v", err)
		return errSeedChainstate
	}
	defer copied.Close()
	copied.txout = m.chain.TxOutRows
	m.setSeedPhase(utxoPhaseImporting)
	prog := &utxoIngestProgress{}
	gate := &seedGate{m: m, ctx: ctx, prog: prog}
	prog.Checkpoint = gate.checkpoint
	_, err = ingestUtxoRecords(m.currentStore(), copied.parse, func(hash [32]byte) (uint32, error) { return m.chainstateBaseHeight(ctx, hash) }, prog)
	if err != nil {
		if cause := context.Cause(ctx); cause != nil {
			return cause
		}
		if errors.Is(err, errChainstateFormat) {
			m.log.Printf("utxo index: %v", err)
			return errSeedChainstate
		}
		return err
	}
	return nil
}

// chainstateBaseHeight retries while the node, restarted right after the copy, is still loading its block index.
func (m *utxoManager) chainstateBaseHeight(ctx context.Context, hash [32]byte) (uint32, error) {
	var header struct {
		Height        uint32 `json:"height"`
		Confirmations int64  `json:"confirmations"`
	}
	for try := 0; ; try++ {
		err := m.rpc.call(ctx, "getblockheader", []any{reverseHex(hash)}, &header)
		switch {
		case err == nil && header.Confirmations < 1:
			return 0, errSeedOffChain
		case err == nil:
			return header.Height, nil
		case try >= utxoReplayRetries:
			return 0, err
		}
		select {
		case <-ctx.Done():
			return 0, context.Cause(ctx)
		case <-time.After(utxoReplayRetryWait):
		}
	}
}

func (m *utxoManager) seedFromDump(parent context.Context, network [4]byte) error {
	h := m.host
	ctx, cancel := context.WithCancelCause(parent)
	defer cancel(nil)
	go m.watchSeedCancel(ctx, cancel)

	fifo, err := openSeedFIFO(h.dataDir, func() { m.setSeedPhase(utxoPhaseImporting) })
	if err != nil {
		return err
	}
	defer fifo.Close()
	defer context.AfterFunc(ctx, func() { _ = fifo.Close() })()
	go m.watchSeedFirstByte(ctx, cancel, fifo)
	if c, err := m.temp(); thermalStartGuard(c, err) != nil {
		return errSeedThermal
	}

	m.suppressAlerts(m.now().Add(utxoAlertCap))
	dumped := make(chan seedDumpOutcome, 1)
	go func() {
		var res seedDumpResult
		err := h.dump.call(ctx, "dumptxoutset", m.chain.dumpArgs(seedDumpName), &res)
		if err != nil {
			cancel(errSeedDump)
		}
		dumped <- seedDumpOutcome{res, err}
	}()

	prog := &utxoIngestProgress{}
	gate := &seedGate{m: m, ctx: ctx, prog: prog}
	prog.Checkpoint = gate.checkpoint
	_, err = ingestUtxoSnapshot(m.currentStore(), fifo, m.chain, network, func(hash [32]byte) (uint32, error) {
		select {
		case out := <-dumped:
			want, herr := utxoRevHex(out.res.BaseHash)
			switch {
			case out.err != nil:
				return 0, errSeedDump
			case herr != nil || want != hash || out.res.CoinsWritten != prog.Total.Load():
				return 0, errSeedMismatch
			}
			return out.res.BaseHeight, nil
		case <-ctx.Done():
			return 0, context.Cause(ctx)
		}
	}, prog)
	if err != nil {
		if cause := context.Cause(ctx); cause != nil {
			return cause
		}
		return err
	}
	return nil
}
