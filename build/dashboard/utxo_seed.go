package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
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
)

type utxoSeedHost struct {
	stateDir string
	dataDir  string
	dump     utxoRPC
	hold     func() bool
	release  func()
}

type seedRequest struct {
	ID          string `json:"id"`
	RequestedAt int64  `json:"requested_at"`
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
	for _, target := range []error{errSeedDump, errSeedLowDisk, errSeedMismatch, errSeedThermal, errSeedInterrupted, errSeedPipeGone} {
		if errors.Is(err, target) {
			return target.Error()
		}
	}
	if err == nil || errors.Is(err, context.Canceled) {
		return ""
	}
	return "seed failed"
}

func seedNetwork(chain string) ([4]byte, bool) {
	switch chain {
	case "main":
		return utxoNetMainnet, true
	case "regtest":
		return utxoNetRegtest, true
	}
	return [4]byte{}, false
}

func seedFIFOGuard(dir string) *seedRefusal {
	if _, err := os.Lstat(filepath.Join(dir, seedDumpName)); err == nil {
		return &seedRefusal{"fifo", seedDumpName + " already exists"}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return &seedRefusal{"fifo", "the bitcoin data directory cannot be inspected"}
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
		return &seedRefusal{"fifo", "the bitcoin data directory cannot be inspected"}
	}
	sys, ok := st.Sys().(*syscall.Stat_t)
	if !ok || os.Getuid() != 0 && int(sys.Uid) != os.Getuid() {
		return &seedRefusal{"fifo", "the dashboard user does not own the bitcoin data directory"}
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
	b, err := os.Lstat(f.path)
	return err == nil && b.Mode()&os.ModeNamedPipe != 0 && os.SameFile(a, b)
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
	age := now.Unix() - req.RequestedAt
	if req.RequestedAt <= 0 || age > int64(seedRequestFreshFor/time.Second) || age < -30 {
		return req, &seedRefusal{"request", "the seed request is stale"}
	}
	return req, nil
}

func (m *utxoManager) takeSeedRequest() (req seedRequest, present bool, refusal *seedRefusal) {
	path := filepath.Join(m.host.stateDir, seedRequestName)
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
		_ = os.Remove(filepath.Join(h.stateDir, seedCancelName))
		return
	}
	if refusal != nil {
		m.markSeedRequest(req.ID, utxoSeedRefused, []seedRefusal{*refusal})
		return
	}
	if req.ID == m.lastSeedRequest() {
		return
	}
	_ = os.Remove(filepath.Join(h.stateDir, seedCancelName))
	m.markSeedRequest(req.ID, utxoSeedChecking, nil)
	in := m.guards(ctx)
	refusals := evaluateSeedGuards(in)
	network, known := seedNetwork(in.Chain.Chain)
	if in.ChainErr == nil && !known {
		refusals = append(refusals, seedRefusal{"node", "this chain cannot be seeded"})
	}
	if r := seedFIFOGuard(h.dataDir); r != nil {
		refusals = append(refusals, *r)
	}
	if len(refusals) == 0 {
		if h.hold() {
			defer h.release()
		} else {
			refusals = append(refusals, seedRefusal{"scanner", "a watch scan is running"})
		}
	}
	if len(refusals) > 0 {
		m.markSeedRequest(req.ID, utxoSeedRefused, refusals)
		return
	}
	if err := m.execSeed(ctx, func(ctx context.Context) error { return m.seedFromDump(ctx, network) }); errors.Is(err, errUtxoSeedState) {
		m.markSeedRequest(req.ID, utxoSeedRefused, []seedRefusal{{"state", "the index state does not allow a seed"}})
	}
}

func (m *utxoManager) watchSeedCancel(ctx context.Context, cancel context.CancelCauseFunc) {
	path := filepath.Join(m.host.stateDir, seedCancelName)
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
				cancel(nil)
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

type seedGate struct {
	m      *utxoManager
	ctx    context.Context
	prog   *utxoIngestProgress
	last   time.Time
	paused bool
}

func (g *seedGate) phase() string {
	if t := g.prog.Total.Load(); t > 0 && g.prog.Parsed.Load() >= t {
		return utxoPhaseMerging
	}
	return utxoPhaseImporting
}

func (g *seedGate) checkpoint() error {
	if g.ctx.Err() != nil {
		return context.Cause(g.ctx)
	}
	if t := g.prog.Total.Load(); t > 0 {
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

func (m *utxoManager) seedFromDump(parent context.Context, network [4]byte) error {
	h := m.host
	ctx, cancel := context.WithCancelCause(parent)
	defer cancel(nil)
	m.suppressAlerts(m.now().Add(utxoAlertCap))
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

	dumped := make(chan seedDumpOutcome, 1)
	go func() {
		var res seedDumpResult
		err := h.dump.call(ctx, "dumptxoutset", []any{seedDumpName, "latest"}, &res)
		if err != nil {
			cancel(errSeedDump)
		}
		dumped <- seedDumpOutcome{res, err}
	}()

	prog := &utxoIngestProgress{}
	gate := &seedGate{m: m, ctx: ctx, prog: prog}
	prog.Checkpoint = gate.checkpoint
	_, err = ingestUtxoSnapshot(m.currentStore(), fifo, network, func(hash [32]byte) (uint32, error) {
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
