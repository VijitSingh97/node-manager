package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"math"
	"net/http"
	"os"
	"slices"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/cockroachdb/pebble/vfs"
)

const (
	utxoStateOff       = "off"
	utxoStateNotSeeded = "not_seeded"
	utxoStateSeeding   = "seeding"
	utxoStateCatching  = "catching_up"
	utxoStateReady     = "ready"
	utxoStateDegraded  = "degraded"
	utxoStateInvalid   = "invalid"

	utxoReadyLag     = 2
	utxoMinFreeBytes = 2_000_000_000
	utxoMilestone    = 1000

	utxoPhaseWaiting    = "waiting"
	utxoPhaseImporting  = "importing"
	utxoPhaseMerging    = "merging"
	utxoPhasePaused     = "paused"
	utxoPhaseCancelling = "cancelling"

	utxoAlertCap = 15 * time.Minute
)

var errUtxoSeedState = errors.New("index state does not allow a seed or reset")

type utxoSeedResult struct {
	Outcome         string    `json:"outcome"`
	FinishedAt      time.Time `json:"finished_at"`
	DurationSeconds float64   `json:"duration_seconds"`
}

type utxoSeedState struct {
	StartedAt   time.Time       `json:"started_at"`
	AlertsUntil time.Time       `json:"alerts_suppressed_until"`
	Last        *utxoSeedResult `json:"last_seed"`
}

type utxoSeedJob struct {
	ctx  context.Context
	fn   func(context.Context) error
	done chan error
}

type utxoManager struct {
	dir       string
	fs        vfs.FS
	rpc       utxoRPC
	log       *log.Logger
	freeBytes func() uint64

	store    *utxoStore
	follower *utxoFollower
	bound    bool
	lowDisk  atomic.Bool
	stop     context.CancelFunc

	now      func() time.Time
	seedJobs chan utxoSeedJob
	seedPath string

	mu           sync.Mutex
	state        string
	reason       string
	nodeTip      int64
	indexTip     *uint32
	seeding      bool
	seedProgress *float64
	phase        string
	refusals     []seedRefusal
	seed         utxoSeedState
}

type utxoIndexStatus struct {
	Enabled      bool     `json:"enabled"`
	State        string   `json:"state"`
	Reason       string   `json:"reason"`
	IndexTip     *uint32  `json:"index_tip_height"`
	NodeTip      *int64   `json:"node_tip_height"`
	Behind       *int64   `json:"blocks_behind"`
	SeedProgress *float64 `json:"seed_progress"`

	Phase                 string          `json:"phase"`
	StartedAt             *time.Time      `json:"started_at"`
	Refusals              []seedRefusal   `json:"refusals"`
	LastSeed              *utxoSeedResult `json:"last_seed"`
	AlertsSuppressedUntil *time.Time      `json:"alerts_suppressed_until"`
}

func newUtxoManager(dir string, fs vfs.FS, rpc utxoRPC, logger *log.Logger) *utxoManager {
	if fs == nil {
		fs = vfs.Default
	}
	m := &utxoManager{dir: dir, fs: fs, rpc: rpc, log: logger, state: utxoStateNotSeeded,
		now: time.Now, seedJobs: make(chan utxoSeedJob),
		freeBytes: func() uint64 {
			if d := diskUsage(dir); d.Total > 0 {
				return d.Free
			}
			return math.MaxUint64
		}}
	m.mu.Lock()
	m.openLocked()
	m.mu.Unlock()
	return m
}

func (m *utxoManager) openLocked() {
	m.bound, m.indexTip, m.follower = false, nil, nil
	if err := m.fs.MkdirAll(m.dir, 0o700); err != nil {
		m.log.Printf("utxo index: cannot create the index directory: %v", err)
		m.store = &utxoStore{invalidReason: "cannot create the index directory"}
		m.setLocked(utxoStateInvalid, m.store.invalidReason)
		return
	}
	store, err := openUtxoStoreFS(m.dir, m.fs)
	if err != nil {
		m.log.Printf("utxo index: cannot open the store: %v", err)
		m.store = &utxoStore{invalidReason: "cannot open the index store"}
		m.setLocked(utxoStateInvalid, m.store.invalidReason)
		return
	}
	m.store = store
	m.follower = newUtxoFollower(store, m.rpc)
	m.follower.onBlock = m.progress
	switch height, _, hasTip, _ := store.Tip(); {
	case !store.Valid():
		m.setLocked(utxoStateInvalid, store.InvalidReason())
	case hasTip:
		m.indexTip = &height
		m.setLocked(utxoStateCatching, "")
	default:
		m.setLocked(utxoStateNotSeeded, "")
	}
}

func (m *utxoManager) set(state, reason string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.setLocked(state, reason)
}

func (m *utxoManager) setLocked(state, reason string) {
	if m.seeding && state != utxoStateSeeding && state != utxoStateInvalid {
		return
	}
	if m.state == state && m.reason == reason {
		return
	}
	m.state, m.reason = state, reason
	if reason != "" {
		m.log.Printf("utxo index: state=%s reason=%q", state, reason)
		return
	}
	m.log.Printf("utxo index: state=%s", state)
}

func (m *utxoManager) setTip(height uint32) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.indexTip = &height
}

func (m *utxoManager) progress(height uint32) {
	m.setTip(height)
	if m.freeBytes() < utxoMinFreeBytes {
		m.lowDisk.Store(true)
		m.mu.Lock()
		stop := m.stop
		m.mu.Unlock()
		if stop != nil {
			stop()
		}
	}
	if height%utxoMilestone == 0 {
		m.mu.Lock()
		defer m.mu.Unlock()
		m.log.Printf("utxo index: following height=%d node_tip=%d", height, m.nodeTip)
	}
}

func (m *utxoManager) Status() utxoIndexStatus {
	if m == nil {
		return utxoIndexStatus{State: utxoStateOff}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	st := utxoIndexStatus{Enabled: true, State: m.state, Reason: m.reason, IndexTip: m.indexTip, SeedProgress: m.seedProgress,
		Phase: m.phase, Refusals: slices.Clone(m.refusals), LastSeed: m.seed.Last}
	if st.Refusals == nil {
		st.Refusals = []seedRefusal{}
	}
	if m.seeding {
		started := m.seed.StartedAt
		st.StartedAt = &started
	}
	if m.alertsSuppressedLocked() {
		until := m.seed.AlertsUntil
		st.AlertsSuppressedUntil = &until
	}
	if m.nodeTip > 0 {
		tip := m.nodeTip
		st.NodeTip = &tip
		if m.indexTip != nil {
			behind := max(0, tip-int64(*m.indexTip))
			st.Behind = &behind
		}
	}
	if st.State == utxoStateReady && ((st.Behind != nil && *st.Behind > utxoReadyLag) || (m.follower != nil && m.follower.Status().State == utxoFollowRollback)) {
		st.State = utxoStateCatching
	}
	return st
}

func (m *utxoManager) currentStore() *utxoStore {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.store
}

func (m *utxoManager) Tip() (height uint32, hash [32]byte, ok bool) {
	if m == nil || m.Status().State != utxoStateReady {
		return
	}
	height, hash, has, err := m.currentStore().Tip()
	return height, hash, has && err == nil
}

func (m *utxoManager) Lookup(scripts [][]byte) (coins []utxoCoin, height uint32, hash [32]byte, ok bool) {
	if m == nil || m.Status().State != utxoStateReady {
		return
	}
	store := m.currentStore()
	for range 2 {
		h, tipHash, has, err := store.Tip()
		if err != nil || !has {
			return
		}
		found, err := store.Lookup(scripts)
		h2, tipHash2, _, err2 := store.Tip()
		if err != nil || err2 != nil {
			return
		}
		if h == h2 && tipHash == tipHash2 {
			return found, h, tipHash, true
		}
	}
	return
}

func (m *utxoManager) run(ctx context.Context) {
	t := time.NewTicker(utxoFollowInterval)
	defer t.Stop()
	for {
		if m.follower != nil {
			m.tick(ctx)
		}
		select {
		case <-ctx.Done():
			_ = m.currentStore().Close()
			return
		case <-t.C:
		case job := <-m.seedJobs:
			job.done <- m.execSeed(job.ctx, job.fn)
		}
	}
}

// runSeed executes fn inside the run goroutine, so no follower step runs or starts while it does.
func (m *utxoManager) runSeed(ctx context.Context, fn func(context.Context) error) error {
	job := utxoSeedJob{ctx: ctx, fn: fn, done: make(chan error, 1)}
	select {
	case m.seedJobs <- job:
		return <-job.done
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *utxoManager) execSeed(ctx context.Context, fn func(context.Context) error) error {
	if err := m.startSeed(); err != nil {
		return err
	}
	err := fn(ctx)
	m.finishSeed(err)
	return err
}

func (m *utxoManager) startSeed() error {
	m.mu.Lock()
	state, busy := m.state, m.seeding
	m.mu.Unlock()
	if busy || state != utxoStateNotSeeded && state != utxoStateInvalid {
		return errUtxoSeedState
	}
	if state == utxoStateInvalid {
		if err := m.resetStore(); err != nil {
			return err
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state != utxoStateNotSeeded {
		return errUtxoSeedState
	}
	zero := 0.0
	m.seeding, m.phase, m.seedProgress, m.refusals = true, utxoPhaseWaiting, &zero, nil
	m.seed.StartedAt, m.seed.AlertsUntil = m.now(), time.Time{}
	m.setLocked(utxoStateSeeding, "")
	m.persistSeedLocked()
	return nil
}

func (m *utxoManager) finishSeed(err error) {
	m.mu.Lock()
	outcome := "ok"
	switch {
	case errors.Is(err, context.Canceled):
		outcome = "cancelled"
	case err != nil:
		outcome = "failed"
	}
	now := m.now()
	m.seeding, m.phase, m.seedProgress = false, "", nil
	m.seed.AlertsUntil = time.Time{}
	m.seed.Last = &utxoSeedResult{Outcome: outcome, FinishedAt: now, DurationSeconds: now.Sub(m.seed.StartedAt).Seconds()}
	m.persistSeedLocked()
	m.log.Printf("utxo index: seed outcome=%s duration=%.0fs", outcome, m.seed.Last.DurationSeconds)
	if err == nil {
		m.setLocked(utxoStateCatching, "")
		m.mu.Unlock()
		return
	}
	m.setLocked(utxoStateNotSeeded, "")
	m.mu.Unlock()
	_ = m.resetStore()
}

func (m *utxoManager) setSeedPhase(phase string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.seeding {
		m.phase = phase
	}
}

func (m *utxoManager) setSeedProgress(fraction float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.seeding {
		m.seedProgress = &fraction
	}
}

func (m *utxoManager) setRefusals(refusals []seedRefusal) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.refusals = refusals
}

func (m *utxoManager) resetStore() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.seeding || m.state != utxoStateNotSeeded && m.state != utxoStateInvalid {
		return errUtxoSeedState
	}
	_ = m.store.Close()
	if err := clearUtxoDir(m.fs, m.dir); err != nil {
		m.log.Printf("utxo index: cannot clear the index directory: %v", err)
		m.store = &utxoStore{invalidReason: "cannot clear the index directory"}
		m.follower, m.indexTip = nil, nil
		m.setLocked(utxoStateInvalid, m.store.invalidReason)
		return err
	}
	m.openLocked()
	return nil
}

func (m *utxoManager) setSeedStatePath(path string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.seedPath = path
	if raw, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(raw, &m.seed)
	}
}

func (m *utxoManager) persistSeedLocked() {
	if m.seedPath == "" {
		return
	}
	raw, _ := json.Marshal(m.seed)
	if err := writePrivateFileAtomic(m.seedPath, raw); err != nil {
		m.log.Print("utxo index: seed state could not be persisted")
	}
}

func (m *utxoManager) suppressAlerts(until time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.seed.StartedAt.IsZero() {
		m.seed.StartedAt = m.now()
	}
	m.seed.AlertsUntil = until
	m.persistSeedLocked()
}

func (m *utxoManager) alertsSuppressedLocked() bool {
	if m.seed.AlertsUntil.IsZero() || m.seed.StartedAt.IsZero() {
		return false
	}
	end := m.seed.AlertsUntil
	if limit := m.seed.StartedAt.Add(utxoAlertCap); limit.Before(end) {
		end = limit
	}
	return m.now().Before(end)
}

func (m *utxoManager) AlertsSuppressed() bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.alertsSuppressedLocked()
}

func (m *utxoManager) tick(ctx context.Context) {
	m.mu.Lock()
	skip := m.state == utxoStateInvalid || m.seeding
	m.mu.Unlock()
	if skip {
		return
	}
	var info blockchainInfo
	if m.rpc.call(ctx, "getblockchaininfo", nil, &info) != nil {
		m.mu.Lock()
		if m.state == utxoStateReady {
			m.setLocked(utxoStateCatching, "node is not answering")
		}
		m.mu.Unlock()
		return
	}
	m.mu.Lock()
	m.nodeTip = info.Blocks
	m.mu.Unlock()
	if !m.bound {
		var genesis string
		if m.rpc.call(ctx, "getblockhash", []any{uint32(0)}, &genesis) != nil {
			return
		}
		if err := m.store.BindChain(info.Chain, genesis); err != nil {
			if errors.Is(err, errUtxoChainMismatch) || errors.Is(err, errUtxoBroken) {
				m.fail(err)
				return
			}
			m.mu.Lock()
			if !m.seeding {
				m.setLocked(m.state, "chain identity not recorded yet")
			}
			m.mu.Unlock()
			return
		}
		m.bound = true
	}
	height, _, hasTip, err := m.store.Tip()
	if err != nil {
		m.fail(err)
		return
	}
	if !hasTip {
		m.set(utxoStateNotSeeded, "")
		return
	}
	m.setTip(height)
	if info.Pruned && int64(height)+1 < info.PruneHeight {
		m.invalidate("index is behind the node's retained blocks")
		return
	}
	if m.freeBytes() < utxoMinFreeBytes {
		m.set(utxoStateDegraded, "less than 2 GB free on the index filesystem")
		return
	}
	if info.Blocks-int64(height) > utxoReadyLag {
		m.set(utxoStateCatching, "")
	}
	m.lowDisk.Store(false)
	stepCtx, stop := context.WithCancel(ctx)
	m.mu.Lock()
	m.stop = stop
	m.mu.Unlock()
	err = m.follower.Step(stepCtx)
	stop()
	if ctx.Err() != nil {
		return
	}
	if m.lowDisk.Load() {
		height, _, _, _ = m.store.Tip()
		m.setTip(height)
		m.set(utxoStateDegraded, "less than 2 GB free on the index filesystem")
		return
	}
	if st := m.follower.Status().State; st == utxoFollowError || errors.Is(err, syscall.ENOSPC) {
		m.fail(err)
		return
	}
	height, _, _, _ = m.store.Tip()
	m.setTip(height)
	if err == nil && info.Blocks-int64(height) <= utxoReadyLag {
		m.set(utxoStateReady, "")
		return
	}
	m.set(utxoStateCatching, "")
}

func (m *utxoManager) fail(err error) {
	switch {
	case errors.Is(err, errUtxoChainMismatch):
		m.invalidate("index was built for a different chain")
	case errors.Is(err, errUtxoReorgTooDeep):
		m.invalidate("indexed tip is no longer on the node's chain and the fork is deeper than the undo journal")
	case errors.Is(err, errUtxoBroken), errors.Is(err, syscall.ENOSPC):
		m.invalidate("storage error while following the chain")
	case err == nil:
		m.invalidate("follower stopped")
	default:
		m.invalidate("follower error: " + utxoErrorClass(err))
	}
}

func utxoErrorClass(err error) string {
	for _, target := range slices.Concat(utxoIntegrityErrors, []error{errUtxoFollowStop}) {
		if errors.Is(err, target) {
			return target.Error()
		}
	}
	return "unclassified error"
}

func (m *utxoManager) invalidate(reason string) {
	err := m.store.MarkInvalid(reason)
	if err != nil {
		if ns, oerr := openUtxoStoreFS(m.dir, m.fs); oerr == nil {
			err = ns.MarkInvalid(reason)
			m.mu.Lock()
			m.store = ns
			m.mu.Unlock()
		}
	}
	if err != nil {
		m.log.Printf("utxo index: invalid state could not be persisted: %v", err)
	}
	m.set(utxoStateInvalid, reason)
}

func (a *app) apiUtxoIndex(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, a.utxo.Status())
}
