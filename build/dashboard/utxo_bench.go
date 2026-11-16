package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

const (
	benchRequestName     = "utxo-bench.request"
	benchResultName      = "utxo-bench.result"
	benchRequestMaxBytes = 64 << 10
	benchMaxRepeat       = 200
	benchMaxAge          = 2 * time.Minute
	benchRunLimit        = 8 * time.Minute
	benchScanRunLimit    = 40 * time.Minute
)

var benchBatchSizes = []int{1, 5, 20}

type utxoBenchHost struct {
	stateDir   string
	validate   func(context.Context, string) error
	resolve    func(context.Context, []string) ([]walletScripts, error)
	cachedRead func(context.Context) (func(), int, error)
	scan       func(context.Context, []string) (benchScanRun, error)
	hold       func() bool
	release    func()
}

type benchWallet struct {
	Name string `json:"name"`
	Key  string `json:"key"`
}

type benchRequest struct {
	ID          string        `json:"id"`
	RequestedAt int64         `json:"requested_at"`
	Wallets     []benchWallet `json:"wallets"`
	Repeat      int           `json:"repeat"`
	Scan        bool          `json:"scan"`
}

type benchTimes struct {
	First float64 `json:"first_s"`
	P50   float64 `json:"p50_s"`
	P99   float64 `json:"p99_s"`
	Max   float64 `json:"max_s"`
}

type benchWalletResult struct {
	Name          string      `json:"name"`
	DeriveSeconds float64     `json:"derive_s"`
	Scripts       int         `json:"scripts"`
	Fallback      bool        `json:"fallback"`
	Lookup        *benchTimes `json:"lookup,omitempty"`
	Coins         int         `json:"coins"`
	Sats          int64       `json:"sats"`
}

type benchBatch struct {
	Wallets  int        `json:"wallets"`
	Scripts  int        `json:"scripts"`
	Expected int        `json:"expected_scripts"`
	Fallback int        `json:"fallback_wallets"`
	Coins    int        `json:"coins"`
	Lookup   benchTimes `json:"lookup"`
}

type benchCached struct {
	benchTimes
	Roster int `json:"roster_wallets"`
}

type benchTotals struct {
	Coins int
	Sats  int64
}

type benchScanRun struct {
	Seconds float64
	Height  int64
	Wallets []benchTotals
}

type benchScanWallet struct {
	Name       string `json:"name"`
	Coins      int    `json:"coins"`
	Sats       int64  `json:"sats"`
	IndexCoins int    `json:"index_coins"`
	IndexSats  int64  `json:"index_sats"`
	Match      *bool  `json:"match,omitempty"`
}

type benchScan struct {
	Height        int64             `json:"height"`
	HeightSkew    bool              `json:"height_skew,omitempty"`
	BatchSeconds  float64           `json:"batch_s"`
	SingleSeconds float64           `json:"single_s"`
	Wallets       []benchScanWallet `json:"wallets"`
}

type benchResult struct {
	ID         string              `json:"id"`
	Started    time.Time           `json:"started"`
	Finished   time.Time           `json:"finished"`
	Repeat     int                 `json:"repeat"`
	NodeTip    *int64              `json:"node_tip_height,omitempty"`
	IndexTip   *uint32             `json:"index_tip_height,omitempty"`
	State      string              `json:"state,omitempty"`
	Wallets    []benchWalletResult `json:"wallets,omitempty"`
	Batches    []benchBatch        `json:"batches,omitempty"`
	CachedRead *benchCached        `json:"cached_read,omitempty"`
	Scan       *benchScan          `json:"scan,omitempty"`
	Refusals   []string            `json:"refusals,omitempty"`
}

func (m *utxoManager) configureBench(h utxoBenchHost) {
	m.bench = &h
}

func parseBenchRequest(raw []byte) (benchRequest, string) {
	var req benchRequest
	err := json.Unmarshal(raw, &req)
	switch {
	case err != nil || len(raw) > benchRequestMaxBytes:
		return req, "the bench request is malformed"
	case len(req.Wallets) < 1 || len(req.Wallets) > maxWatchWallets:
		return req, fmt.Sprintf("the bench request needs 1 to %d wallets", maxWatchWallets)
	case req.Repeat < 1 || req.Repeat > benchMaxRepeat:
		return req, fmt.Sprintf("repeat must be 1 to %d", benchMaxRepeat)
	}
	for i, w := range req.Wallets {
		if strings.TrimSpace(w.Name) == "" {
			return req, fmt.Sprintf("wallet %d has no name", i+1)
		}
		if _, err := descriptorsFor(w.Key); err != nil {
			return req, fmt.Sprintf("wallet %d is not a public watch key", i+1)
		}
	}
	return req, ""
}

func benchSummary(samples []float64) benchTimes {
	sorted := slices.Sorted(slices.Values(samples))
	at := func(p float64) float64 { return sorted[int(math.Ceil(p*float64(len(sorted))))-1] }
	return benchTimes{First: samples[0], P50: at(0.5), P99: at(0.99), Max: sorted[len(sorted)-1]}
}

func (m *utxoManager) benchLookup(ctx context.Context, scripts [][]byte, repeat int) (benchTimes, []utxoCoin, bool) {
	samples := make([]float64, repeat)
	var coins []utxoCoin
	for i := range samples {
		if ctx.Err() != nil {
			return benchTimes{}, nil, false
		}
		started := time.Now()
		found, _, _, ok := m.Lookup(scripts)
		samples[i] = time.Since(started).Seconds()
		if !ok {
			return benchTimes{}, nil, false
		}
		coins = found
	}
	return benchSummary(samples), coins, true
}

func benchTotalsOf(coins []utxoCoin) benchTotals {
	t := benchTotals{Coins: len(coins)}
	for _, c := range coins {
		t.Sats += int64(c.Amount)
	}
	return t
}

func (m *utxoManager) benchLoop(ctx context.Context) {
	t := time.NewTicker(seedPollEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.pollBench(ctx)
		}
	}
}

func (m *utxoManager) pollBench(ctx context.Context) {
	h := m.bench
	path := filepath.Join(h.stateDir, benchRequestName)
	f, err := os.Open(path)
	if err != nil {
		return
	}
	raw, _ := io.ReadAll(io.LimitReader(f, benchRequestMaxBytes+1))
	_ = f.Close()
	_ = os.Remove(path)
	req, refusal := parseBenchRequest(raw)
	if !validPortControlID(req.ID) {
		m.log.Print("utxo bench: request without a valid id dropped")
		return
	}
	if age := m.now().Sub(time.Unix(req.RequestedAt, 0)); refusal == "" && (age > benchMaxAge || age < -benchMaxAge) {
		refusal = "the bench request is stale"
	}
	res := benchResult{ID: req.ID, Started: m.now().UTC(), Repeat: req.Repeat}
	if refusal != "" {
		res.Refusals = []string{refusal}
	} else {
		m.log.Printf("utxo bench: start wallets=%d repeat=%d scan=%t", len(req.Wallets), req.Repeat, req.Scan)
		limit := benchRunLimit
		if req.Scan {
			limit = benchScanRunLimit
		}
		runCtx, cancel := context.WithTimeout(ctx, limit)
		m.execBench(runCtx, req, &res)
		cancel()
	}
	res.Finished = m.now().UTC()
	out, _ := json.Marshal(res)
	if writePrivateFileAtomic(filepath.Join(h.stateDir, benchResultName), out) != nil {
		m.log.Print("utxo bench: result could not be written")
	}
	m.log.Printf("utxo bench: end wallets=%d refusals=%d duration=%.1fs", len(res.Wallets), len(res.Refusals), res.Finished.Sub(res.Started).Seconds())
}

func (m *utxoManager) execBench(ctx context.Context, req benchRequest, res *benchResult) {
	h := m.bench
	refuse := func(format string, args ...any) { res.Refusals = append(res.Refusals, fmt.Sprintf(format, args...)) }
	st := m.Status()
	res.State, res.NodeTip, res.IndexTip = st.State, st.NodeTip, st.IndexTip
	switch {
	case st.State != utxoStateReady:
		refuse("the index is %s, not ready", st.State)
		return
	case st.Behind == nil || *st.Behind != 0:
		refuse("the index is not level with the node tip")
		return
	}
	for i, w := range req.Wallets {
		if err := h.validate(ctx, w.Key); err != nil {
			refuse("wallet %d is not accepted as a public watch key", i+1)
		}
	}
	if len(res.Refusals) > 0 {
		return
	}
	scripts := make([][][]byte, len(req.Wallets))
	totals := make([]benchTotals, len(req.Wallets))
	for i, w := range req.Wallets {
		started := time.Now()
		resolved, err := h.resolve(ctx, []string{w.Key})
		derive := time.Since(started).Seconds()
		if err != nil || len(resolved) != 1 {
			refuse("wallet %d: script derivation failed", i+1)
			return
		}
		wr := benchWalletResult{Name: w.Name, DeriveSeconds: derive, Scripts: len(resolved[0].Scripts), Fallback: resolved[0].Fallback}
		if !wr.Fallback {
			scripts[i] = resolved[0].Scripts
			times, coins, ok := m.benchLookup(ctx, scripts[i], req.Repeat)
			if !ok {
				refuse("wallet %d: the index stopped answering or the run timed out", i+1)
				return
			}
			totals[i] = benchTotalsOf(coins)
			wr.Lookup, wr.Coins, wr.Sats = &times, totals[i].Coins, totals[i].Sats
		}
		res.Wallets = append(res.Wallets, wr)
	}
	seen := map[int]bool{}
	for _, n := range benchBatchSizes {
		n = min(n, len(req.Wallets))
		if seen[n] {
			continue
		}
		seen[n] = true
		batch := sortedUniqueScripts(slices.Concat(scripts[:n]...))
		times, coins, ok := m.benchLookup(ctx, batch, req.Repeat)
		if !ok {
			refuse("batch of %d: the index stopped answering or the run timed out", n)
			return
		}
		b := benchBatch{Wallets: n, Scripts: len(batch), Coins: len(coins), Lookup: times}
		for _, w := range res.Wallets[:n] {
			if w.Fallback {
				b.Fallback++
			} else {
				b.Expected += w.Scripts
			}
		}
		res.Batches = append(res.Batches, b)
	}
	if read, roster, err := h.cachedRead(ctx); err != nil {
		refuse("the cached read could not be set up")
	} else {
		samples := make([]float64, req.Repeat)
		for i := range samples {
			if ctx.Err() != nil {
				refuse("the run timed out during the cached read")
				return
			}
			started := time.Now()
			read()
			samples[i] = time.Since(started).Seconds()
		}
		res.CachedRead = &benchCached{benchTimes: benchSummary(samples), Roster: roster}
	}
	if req.Scan {
		m.benchScan(ctx, req, res, totals)
	}
}

func (m *utxoManager) benchScan(ctx context.Context, req benchRequest, res *benchResult, totals []benchTotals) {
	h := m.bench
	if !h.hold() {
		res.Refusals = append(res.Refusals, "a watch scan is running")
		return
	}
	defer h.release()
	keys := make([]string, len(req.Wallets))
	for i, w := range req.Wallets {
		keys[i] = w.Key
	}
	batch, err := h.scan(ctx, keys)
	if err != nil || len(batch.Wallets) != len(keys) {
		res.Refusals = append(res.Refusals, "the batched scan failed or timed out")
		return
	}
	single, err := h.scan(ctx, keys[:1])
	if err != nil {
		res.Refusals = append(res.Refusals, "the single-wallet scan failed")
		return
	}
	scan := benchScan{Height: batch.Height, BatchSeconds: batch.Seconds, SingleSeconds: single.Seconds}
	now := m.Status().IndexTip
	level := now != nil && res.IndexTip != nil && *now == *res.IndexTip && int64(*now) == batch.Height && single.Height == batch.Height
	scan.HeightSkew = !level
	for i, w := range req.Wallets {
		sw := benchScanWallet{Name: w.Name, Coins: batch.Wallets[i].Coins, Sats: batch.Wallets[i].Sats, IndexCoins: totals[i].Coins, IndexSats: totals[i].Sats}
		if level && !res.Wallets[i].Fallback {
			match := batch.Wallets[i] == totals[i]
			sw.Match = &match
		}
		scan.Wallets = append(scan.Wallets, sw)
	}
	res.Scan = &scan
}

func (a *app) benchHost() utxoBenchHost {
	return utxoBenchHost{
		stateDir: a.cfg.stateDir,
		validate: func(ctx context.Context, key string) error {
			_, err := a.validateWatchKey(ctx, key)
			return err
		},
		resolve: func(ctx context.Context, keys []string) ([]walletScripts, error) {
			return a.scanner.resolveScripts(ctx, keys)
		},
		cachedRead: func(ctx context.Context) (func(), int, error) {
			var chain blockchainInfo
			if err := a.rpc.call(ctx, "getblockchaininfo", nil, &chain); err != nil {
				return nil, 0, err
			}
			roster, _ := a.watch.snapshot()
			return func() { a.watch.ListWithChain(a.walletRPC, chain) }, len(roster), nil
		},
		scan:    a.benchScanRPC,
		hold:    a.scanner.hold,
		release: a.scanner.release,
	}
}

// Expands descriptors and matches outputs to wallets the way scanBatch does, timing only the scantxoutset call.
func (a *app) benchScanRPC(ctx context.Context, keys []string) (benchScanRun, error) {
	s := a.scanner
	var status map[string]any
	if s.rpcCall(ctx, "scantxoutset", []any{"status"}, &status) == nil && len(status) > 0 {
		return benchScanRun{}, errors.New("scantxoutset is busy")
	}
	objects := map[string]any{}
	owned := make([]map[string]bool, len(keys))
	for i, key := range keys {
		wallet, addresses, err := s.scanObjects(ctx, WatchEntry{Key: key})
		if err != nil {
			return benchScanRun{}, err
		}
		maps.Copy(objects, wallet)
		owned[i] = addresses
	}
	list := make([]any, 0, len(objects))
	for _, descriptor := range slices.Sorted(maps.Keys(objects)) {
		list = append(list, objects[descriptor])
	}
	scanCtx, cancel := context.WithTimeout(ctx, prunedScanTimeout)
	defer cancel()
	var result scanTxOutSetResult
	started := time.Now()
	if err := s.rpcCall(scanCtx, "scantxoutset", []any{"start", list}, &result); err != nil {
		abortCtx, abortCancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
		var ignored any
		_ = s.rpcCall(abortCtx, "scantxoutset", []any{"abort"}, &ignored)
		abortCancel()
		return benchScanRun{}, err
	}
	run := benchScanRun{Seconds: time.Since(started).Seconds(), Height: result.Height, Wallets: make([]benchTotals, len(keys))}
	outputs, err := validatedScanOutputs(result)
	if err != nil {
		return benchScanRun{}, err
	}
	derived := map[string][]string{}
	for _, output := range outputs {
		addresses, ok := derived[output.Desc]
		if !ok {
			shortCtx, shortCancel := context.WithTimeout(ctx, 15*time.Second)
			err := a.rpc.call(shortCtx, "deriveaddresses", []any{output.Desc}, &addresses)
			shortCancel()
			if err != nil || len(addresses) == 0 {
				return benchScanRun{}, errors.New("scan output cannot be derived")
			}
			derived[output.Desc] = addresses
		}
		for i := range keys {
			if slices.ContainsFunc(addresses, func(address string) bool { return owned[i][address] }) {
				run.Wallets[i].Coins++
				run.Wallets[i].Sats += output.Sats
			}
		}
	}
	return run, nil
}
