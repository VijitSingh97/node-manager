package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	benchScanEachLimit   = 5 * time.Minute
)

var benchBatchSizes = []int{1, 5, 20}

type utxoBenchHost struct {
	chain      string
	prefix     string
	stateDir   string
	validate   func(context.Context, string) error
	resolve    func(context.Context, []string) ([]walletScripts, error)
	cachedRead func(context.Context) (func(), int, error)
	scan       func(context.Context, []string) (benchScanRun, error)
	txOutSet   func(context.Context) (txOutSetInfo, error)
	hold       func() bool
	release    func()

	confirmP2PK func(context.Context, []utxoCoin, string) (int, error)
}

type benchWallet struct {
	Name string `json:"name"`
	Key  string `json:"key"`
}

type benchRequest struct {
	ID          string        `json:"id"`
	Chain       string        `json:"chain"`
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
	Best    string
	Totals  benchTotals
}

type benchScanWallet struct {
	Name       string  `json:"name"`
	Seconds    float64 `json:"scan_s"`
	Height     int64   `json:"height"`
	Coins      int     `json:"coins"`
	Sats       int64   `json:"sats"`
	IndexCoins int     `json:"index_coins"`
	IndexSats  int64   `json:"index_sats"`
	Match      *bool   `json:"match,omitempty"`
	P2PKCoins  int     `json:"p2pk_coins,omitempty"`
	P2PKSats   int64   `json:"p2pk_sats,omitempty"`

	P2PKSpentAfter int `json:"p2pk_spent_after_scan,omitempty"`
	P2PKUnchecked  int `json:"p2pk_unchecked,omitempty"`
}

type benchScan struct {
	BatchSeconds float64           `json:"batch_s"`
	BatchHeight  int64             `json:"batch_height"`
	Wallets      []benchScanWallet `json:"wallets"`
}

type benchResult struct {
	ID         string              `json:"id"`
	Chain      string              `json:"chain"`
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
	Failures   []string            `json:"failures,omitempty"`
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
	if req.Chain == "" {
		req.Chain = nodeBitcoin
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

const benchP2PKConfirmMax = 2000

func splitP2PKCoins(coins []utxoCoin) (plain, p2pk []utxoCoin) {
	for _, c := range coins {
		if c.P2PK {
			p2pk = append(p2pk, c)
		} else {
			plain = append(plain, c)
		}
	}
	return plain, p2pk
}

// benchScanCovers reports whether scanning the key can see P2PK outputs, which only a top-level pk() or combo() descriptor can.
func benchScanCovers(key string) bool {
	key = strings.TrimSpace(key)
	return strings.HasPrefix(key, "pk(") || strings.HasPrefix(key, "combo(")
}

// splitBenchCoins separates the coins a scan of the key must reproduce from the P2PK coins only the node's own copy can confirm.
func splitBenchCoins(key string, coins []utxoCoin) (compared, unseen []utxoCoin) {
	if benchScanCovers(key) {
		return coins, nil
	}
	return splitP2PKCoins(coins)
}

// confirmP2PKCoins checks each flagged coin against the node's own copy of its output, which no scan can see.
// A coin the node no longer has counts as spent after the scan when the tip moved past the compared block.
func confirmP2PKCoins(ctx context.Context, rpc *rpcClient, coins []utxoCoin, compared string) (int, error) {
	calls := make([]rpcBatchCall, len(coins))
	for i, c := range coins {
		calls[i] = rpcBatchCall{Method: "gettxout", Params: []any{reverseHex(c.Txid), c.Vout, false}}
	}
	replies, err := batchChunks(ctx, rpc, calls)
	if err != nil {
		return 0, err
	}
	var gone []string
	for i, c := range coins {
		var out struct {
			Value        float64 `json:"value"`
			ScriptPubKey struct {
				Hex string `json:"hex"`
			} `json:"scriptPubKey"`
		}
		if len(replies[i].Error) > 0 && string(replies[i].Error) != "null" || json.Unmarshal(replies[i].Result, &out) != nil || out.ScriptPubKey.Hex == "" {
			gone = append(gone, fmt.Sprintf("%s:%d", reverseHex(c.Txid), c.Vout))
			continue
		}
		script, err := hex.DecodeString(out.ScriptPubKey.Hex)
		if err != nil {
			return 0, err
		}
		if h, p2pk := utxoCanonHash(script); !p2pk || h != c.ScriptHash || uint64(math.Round(out.Value*1e8)) != c.Amount {
			return 0, fmt.Errorf("%s:%d differs from the node's output", reverseHex(c.Txid), c.Vout)
		}
	}
	if len(gone) == 0 {
		return 0, nil
	}
	var best string
	if err := rpc.call(ctx, "getbestblockhash", nil, &best); err != nil {
		return 0, err
	}
	if best == compared {
		return 0, fmt.Errorf("%s is not unspent", gone[0])
	}
	return len(gone), nil
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
	path := filepath.Join(h.stateDir, h.prefix+benchRequestName)
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
	if refusal == "" && req.Chain != h.chain {
		refusal = "the bench request names another chain"
	}
	res := benchResult{ID: req.ID, Chain: h.chain, Started: m.now().UTC(), Repeat: req.Repeat}
	if refusal != "" {
		res.Refusals = []string{refusal}
	} else {
		m.log.Printf("utxo bench: start chain=%s wallets=%d repeat=%d scan=%t", h.chain, len(req.Wallets), req.Repeat, req.Scan)
		limit := benchRunLimit
		if req.Scan {
			limit = max(benchScanRunLimit, limit+time.Duration(len(req.Wallets)+1)*benchScanEachLimit)
		}
		runCtx, cancel := context.WithTimeout(ctx, limit)
		m.execBench(runCtx, req, &res)
		cancel()
	}
	res.Finished = m.now().UTC()
	out, _ := json.Marshal(res)
	if writePrivateFileAtomic(filepath.Join(h.stateDir, h.prefix+benchResultName), out) != nil {
		m.log.Print("utxo bench: result could not be written")
	}
	m.log.Printf("utxo bench: end chain=%s wallets=%d refusals=%d failures=%d duration=%.1fs", h.chain, len(res.Wallets), len(res.Refusals), len(res.Failures), res.Finished.Sub(res.Started).Seconds())
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
			totals := benchTotalsOf(coins)
			wr.Lookup, wr.Coins, wr.Sats = &times, totals.Coins, totals.Sats
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
	if h.cachedRead != nil {
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
	}
	if req.Scan {
		m.benchScan(ctx, req, res, scripts)
	}
}

// Each wallet's comparison lookup runs just before its own scan and counts only when both answered at the same block.
func (m *utxoManager) benchScan(ctx context.Context, req benchRequest, res *benchResult, scripts [][][]byte) {
	h := m.bench
	refuse := func(format string, args ...any) { res.Refusals = append(res.Refusals, fmt.Sprintf(format, args...)) }
	if h.scan == nil {
		refuse("the %s node has no scan to compare against", h.chain)
		return
	}
	var keys []string
	for i, w := range req.Wallets {
		if !res.Wallets[i].Fallback {
			keys = append(keys, w.Key)
		}
	}
	if len(keys) == 0 {
		refuse("every wallet fell back, so no wallet can be compared with a scan")
		return
	}
	if !h.hold() {
		refuse("a watch scan is running")
		return
	}
	defer h.release()
	batch, err := h.scan(ctx, keys)
	if err != nil {
		refuse("the batched scan failed or timed out")
		return
	}
	res.Scan = &benchScan{BatchSeconds: batch.Seconds, BatchHeight: batch.Height, Wallets: []benchScanWallet{}}
	compared := 0
	for i, w := range req.Wallets {
		sw := benchScanWallet{Name: w.Name}
		if res.Wallets[i].Fallback {
			res.Scan.Wallets = append(res.Scan.Wallets, sw)
			continue
		}
		// A slow node can flip the index out of ready during the previous scan without changing it; the hash check below suffices.
		coins, _, hash, indexed := m.lookupAtTip(scripts[i])
		run, err := h.scan(ctx, []string{w.Key})
		if err != nil {
			refuse("wallet %d: the scan failed or timed out", i+1)
			return
		}
		_, flaggedCoins := splitP2PKCoins(coins)
		plain, p2pk := splitBenchCoins(w.Key, coins)
		index, flagged := benchTotalsOf(plain), benchTotalsOf(flaggedCoins)
		sw.P2PKCoins, sw.P2PKSats = flagged.Coins, flagged.Sats
		sw.Seconds, sw.Height, sw.Coins, sw.Sats, sw.IndexCoins, sw.IndexSats = run.Seconds, run.Height, run.Totals.Coins, run.Totals.Sats, index.Coins, index.Sats
		if indexed && reverseHex(hash) == run.Best {
			match := run.Totals == index
			sw.Match = &match
			compared++
			if !match {
				res.Failures = append(res.Failures, fmt.Sprintf("wallet %d: the index and the scan differ at height %d", i+1, run.Height))
			}
			if len(p2pk) > 0 && h.confirmP2PK != nil {
				spent, err := h.confirmP2PK(ctx, p2pk[:min(len(p2pk), benchP2PKConfirmMax)], run.Best)
				if err != nil {
					res.Failures = append(res.Failures, fmt.Sprintf("wallet %d: a P2PK coin the index attributes to it is not confirmed by the node: %v", i+1, err))
				}
				sw.P2PKSpentAfter, sw.P2PKUnchecked = spent, max(0, len(p2pk)-benchP2PKConfirmMax)
			}
		}
		res.Scan.Wallets = append(res.Scan.Wallets, sw)
	}
	if compared == 0 {
		refuse("no wallet was compared with its scan: each met a new block; run it again")
	}
}

func (a *app) benchHost() utxoBenchHost {
	h := a.chainBenchHost(nodeBitcoin, a.rpc, utxoBitcoin, a.scanner.hold, a.scanner.release)
	h.resolve = a.scanner.resolveScripts
	h.cachedRead = func(ctx context.Context) (func(), int, error) {
		var chain blockchainInfo
		if err := a.rpc.call(ctx, "getblockchaininfo", nil, &chain); err != nil {
			return nil, 0, err
		}
		roster, _ := a.watch.snapshot()
		return func() { a.watch.ListWithChain(a.walletRPC, chain) }, len(roster), nil
	}
	return h
}

// chainBenchHost is the bench hook for one indexed chain: its node resolves the wallets and its scantxoutset is the baseline.
func (a *app) chainBenchHost(id string, rpc *rpcClient, chain *utxoChainParams, hold func() bool, release func()) utxoBenchHost {
	prefix := id + "-"
	if id == nodeBitcoin {
		prefix = ""
	}
	scanRPC := *rpc
	client := *rpc.client
	client.Timeout = 0
	scanRPC.client, scanRPC.maxBody = &client, 64<<20
	validate := func(ctx context.Context, key string) error {
		_, err := validateWatchKeyRPC(ctx, rpc, key)
		return err
	}
	resolve := func(ctx context.Context, keys []string) ([]walletScripts, error) {
		return resolveWalletScripts(ctx, rpc, chain, keys)
	}
	scanObjects := func(ctx context.Context, keys []string) ([]any, error) {
		var objects []any
		for _, key := range keys {
			descs, err := validateWatchKeyRPC(ctx, rpc, key)
			if err != nil {
				return nil, err
			}
			for _, d := range descs {
				if d.ranged {
					objects = append(objects, map[string]any{"desc": d.desc, "range": []int{deriveRangeStart, deriveRangeEnd}})
				} else {
					objects = append(objects, d.desc)
				}
			}
		}
		return objects, nil
	}
	// Bitcoin Cash Node has no getdescriptorinfo or deriveaddresses, so its bench reads addr() keys the way its watch card does.
	if len(chain.Descriptors) == 0 {
		validate = func(ctx context.Context, key string) error {
			_, _, err := addrKeyScript(ctx, rpc, key)
			return err
		}
		resolve = func(ctx context.Context, keys []string) ([]walletScripts, error) {
			resolved := make([]walletScripts, len(keys))
			for i, key := range keys {
				_, script, err := addrKeyScript(ctx, rpc, key)
				if err != nil {
					return nil, err
				}
				resolved[i].Scripts = [][]byte{script}
			}
			return resolved, nil
		}
		scanObjects = func(ctx context.Context, keys []string) ([]any, error) {
			objects := make([]any, len(keys))
			for i, key := range keys {
				address, _, err := addrKeyScript(ctx, rpc, key)
				if err != nil {
					return nil, err
				}
				objects[i] = "addr(" + address + ")"
			}
			return objects, nil
		}
	}
	host := utxoBenchHost{
		chain:    id,
		prefix:   prefix,
		stateDir: a.cfg.stateDir,
		validate: validate,
		resolve:  resolve,
		scan: func(ctx context.Context, keys []string) (benchScanRun, error) {
			if !a.nodeWorkerRunnable(id, true) {
				return benchScanRun{}, context.Canceled
			}
			objects, err := scanObjects(ctx, keys)
			if err != nil {
				return benchScanRun{}, err
			}
			return benchScanRPC(ctx, &scanRPC, objects)
		},
		txOutSet: func(ctx context.Context) (txOutSetInfo, error) {
			var info txOutSetInfo
			if !a.nodeWorkerRunnable(id, true) {
				return info, context.Canceled
			}
			args := []any{}
			if chain.TxOutSetNone {
				args = append(args, "none")
			}
			return info, scanRPC.call(ctx, "gettxoutsetinfo", args, &info)
		},
		confirmP2PK: func(ctx context.Context, coins []utxoCoin, compared string) (int, error) {
			return confirmP2PKCoins(ctx, &scanRPC, coins, compared)
		},
		hold:    hold,
		release: release,
	}
	// Dogecoin Core 1.14.9 has no scantxoutset, so a nil scan makes --scan refuse.
	if id == nodeDogecoin {
		host.scan = nil
	}
	return host
}

// addrKeyScript resolves an addr() key with validateaddress and returns the node's spelling of the address and its script.
func addrKeyScript(ctx context.Context, rpc *rpcClient, key string) (string, []byte, error) {
	address, opened := strings.CutPrefix(key, "addr(")
	address, closed := strings.CutSuffix(address, ")")
	if !opened || !closed || !validFamilyWatchAddress(address) || strings.ContainsAny(address, "()#,") {
		return "", nil, errors.New("not an addr() key")
	}
	var validated struct {
		IsValid      bool   `json:"isvalid"`
		Address      string `json:"address"`
		ScriptPubKey string `json:"scriptPubKey"`
	}
	if err := rpc.call(ctx, "validateaddress", []any{address}, &validated); err != nil || !validated.IsValid || !validFamilyWatchAddress(validated.Address) {
		return "", nil, errors.New("invalid address")
	}
	script, err := hex.DecodeString(validated.ScriptPubKey)
	if err != nil || len(script) == 0 {
		return "", nil, errors.New("invalid address script")
	}
	return validated.Address, script, nil
}

func benchScanRPC(ctx context.Context, rpc *rpcClient, objects []any) (benchScanRun, error) {
	var status map[string]any
	if rpc.call(ctx, "scantxoutset", []any{"status"}, &status) == nil && len(status) > 0 {
		return benchScanRun{}, errors.New("scantxoutset is busy")
	}
	scanCtx, cancel := context.WithTimeout(ctx, prunedScanTimeout)
	defer cancel()
	var result scanTxOutSetResult
	started := time.Now()
	if err := rpc.call(scanCtx, "scantxoutset", []any{"start", objects}, &result); err != nil {
		abortCtx, abortCancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
		var ignored any
		_ = rpc.call(abortCtx, "scantxoutset", []any{"abort"}, &ignored)
		abortCancel()
		return benchScanRun{}, err
	}
	run := benchScanRun{Seconds: time.Since(started).Seconds(), Height: result.Height, Best: result.BestBlock}
	outputs, err := scanOutputsUpTo(result, math.MaxInt64)
	if err != nil {
		return benchScanRun{}, err
	}
	for _, output := range outputs {
		run.Totals.Coins++
		run.Totals.Sats += output.Sats
	}
	return run, nil
}
