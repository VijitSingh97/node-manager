package main

import (
	"context"
	"errors"
	"log"
	"math"
	"net/http"
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
)

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

	mu           sync.Mutex
	state        string
	reason       string
	nodeTip      int64
	indexTip     *uint32
	seeding      bool
	seedProgress *float64
}

type utxoIndexStatus struct {
	Enabled      bool     `json:"enabled"`
	State        string   `json:"state"`
	Reason       string   `json:"reason"`
	IndexTip     *uint32  `json:"index_tip_height"`
	NodeTip      *int64   `json:"node_tip_height"`
	Behind       *int64   `json:"blocks_behind"`
	SeedProgress *float64 `json:"seed_progress"`
}

func newUtxoManager(dir string, fs vfs.FS, rpc utxoRPC, logger *log.Logger) *utxoManager {
	if fs == nil {
		fs = vfs.Default
	}
	m := &utxoManager{dir: dir, fs: fs, rpc: rpc, log: logger, state: utxoStateNotSeeded,
		freeBytes: func() uint64 {
			if d := diskUsage(dir); d.Total > 0 {
				return d.Free
			}
			return math.MaxUint64
		}}
	if err := fs.MkdirAll(dir, 0o700); err != nil {
		m.log.Printf("utxo index: cannot create the index directory: %v", err)
		m.store = &utxoStore{invalidReason: "cannot create the index directory"}
		m.set(utxoStateInvalid, m.store.invalidReason)
		return m
	}
	store, err := openUtxoStoreFS(dir, fs)
	if err != nil {
		m.log.Printf("utxo index: cannot open the store: %v", err)
		m.store = &utxoStore{invalidReason: "cannot open the index store"}
		m.set(utxoStateInvalid, m.store.invalidReason)
		return m
	}
	m.store = store
	m.follower = newUtxoFollower(store, rpc)
	m.follower.onBlock = m.progress
	switch height, _, hasTip, _ := store.Tip(); {
	case !store.Valid():
		m.set(utxoStateInvalid, store.InvalidReason())
	case hasTip:
		m.indexTip = &height
		m.set(utxoStateCatching, "")
	default:
		m.set(utxoStateNotSeeded, "")
	}
	return m
}

func (m *utxoManager) set(state, reason string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.setLocked(state, reason)
}

func (m *utxoManager) setLocked(state, reason string) {
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
		m.stop()
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
	st := utxoIndexStatus{Enabled: true, State: m.state, Reason: m.reason, IndexTip: m.indexTip, SeedProgress: m.seedProgress}
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

// SetSeed is the hook the seeding flow uses to pause following and report progress.
func (m *utxoManager) SetSeed(active bool, progress float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.seeding = active
	if active {
		m.seedProgress = &progress
		m.setLocked(utxoStateSeeding, "")
		return
	}
	m.seedProgress = nil
	if m.state == utxoStateSeeding {
		m.setLocked(utxoStateCatching, "")
	}
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
			_ = m.store.Close()
			return
		case <-t.C:
		}
	}
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
			m.fail(err)
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
	m.stop = stop
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
		m.invalidate("follower error: " + err.Error())
	}
}

func (m *utxoManager) invalidate(reason string) {
	err := m.store.MarkInvalid(reason)
	if err != nil {
		if ns, oerr := openUtxoStoreFS(m.dir, m.fs); oerr == nil {
			err = ns.MarkInvalid(reason)
			m.store = ns
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
