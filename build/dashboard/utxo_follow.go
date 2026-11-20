package main

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const utxoFollowInterval = 10 * time.Second

const utxoBlockWait = 60 * time.Second

const utxoReplayPrefetch = 32

// A replay batch stops at whichever comes first, so a run of large blocks cannot hold gigabytes in one batch.
var (
	utxoReplayBatch    = 1000
	utxoReplayBatchOps = 200_000
	// Blocks fetched ahead plus the open batch stay within this replayCost; a larger block waits until everything before it is committed.
	utxoReplayInflightOps = 400_000
)

const (
	utxoFollowIdle     = "idle"
	utxoFollowCatching = "catching_up"
	utxoFollowRollback = "rolling_back"
	utxoFollowError    = "error"
	utxoFollowRetry    = "retrying"
)

var (
	errUtxoReorgTooDeep = errors.New("reorg is deeper than the undo journal or below the snapshot base")
	errUtxoNoPrevout    = errors.New("input has no prevout data")
	errUtxoBadBlock     = errors.New("malformed block from node")
	errUtxoFollowStop   = errors.New("follower stopped")
	errUtxoChainMoved   = errors.New("chain changed while catching up")
	errUtxoNodeBehind   = errors.New("node is further behind the index than the undo journal")
	// A block too large to apply stops the index without invalidating it, so no retry fetches it again.
	errUtxoBlockTooLarge = errors.New("the balance index cannot apply this block within its memory cap")
)

type utxoRPC interface {
	call(ctx context.Context, method string, params any, out any) error
}

type utxoFollower struct {
	store   *utxoStore
	rpc     utxoRPC
	chain   *utxoChainParams
	onBlock func(height uint32)
	gate    func() error

	mu       sync.Mutex
	state    string
	lastErr  string
	blocks   int
	fetchDur time.Duration
	applyDur time.Duration
}

type utxoFollowStatus struct {
	State    string
	Error    string
	Blocks   int
	FetchDur time.Duration
	ApplyDur time.Duration
}

type utxoV3Block struct {
	Hash     string `json:"hash"`
	Height   uint32 `json:"height"`
	Previous string `json:"previousblockhash"`
	Tx       []struct {
		Txid string `json:"txid"`
		Vin  []struct {
			IsMWEB   bool   `json:"ismweb"`
			Coinbase string `json:"coinbase"`
			Txid     string `json:"txid"`
			Vout     uint32 `json:"vout"`
			Prevout  *struct {
				ScriptPubKey struct {
					Hex string `json:"hex"`
				} `json:"scriptPubKey"`
			} `json:"prevout"`
		} `json:"vin"`
		Vout []struct {
			IsMWEB       bool      `json:"ismweb"`
			Value        rpcAmount `json:"value"`
			N            uint32    `json:"n"`
			ScriptPubKey struct {
				Hex string `json:"hex"`
			} `json:"scriptPubKey"`
		} `json:"vout"`
	} `json:"tx"`
}

type rpcAmount uint64

func (a *rpcAmount) UnmarshalJSON(b []byte) error {
	whole, frac, _ := strings.Cut(string(b), ".")
	if whole == "" || len(frac) > 8 || strings.ContainsAny(string(b), "eE-+") {
		return fmt.Errorf("bad amount %q", b)
	}
	var n uint64
	for _, c := range whole + frac + strings.Repeat("0", 8-len(frac)) {
		if c < '0' || c > '9' || n > (1<<64-1-9)/10 {
			return fmt.Errorf("bad amount %q", b)
		}
		n = n*10 + uint64(c-'0')
	}
	*a = rpcAmount(n)
	return nil
}

func newUtxoFollower(store *utxoStore, rpc utxoRPC) *utxoFollower {
	return &utxoFollower{store: store, rpc: rpc, chain: utxoBitcoin, state: utxoFollowIdle}
}

func (f *utxoFollower) Status() utxoFollowStatus {
	f.mu.Lock()
	defer f.mu.Unlock()
	return utxoFollowStatus{f.state, f.lastErr, f.blocks, f.fetchDur, f.applyDur}
}

func (f *utxoFollower) set(state string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.state = state
	f.lastErr = ""
	if err != nil {
		f.lastErr = err.Error()
	}
}

func (f *utxoFollower) Run(ctx context.Context, interval time.Duration) error {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		if err := f.Step(ctx); err != nil && f.Status().State != utxoFollowRetry && ctx.Err() == nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
}

var utxoIntegrityErrors = []error{errUtxoNoPrevout, errUtxoMissingCoin, errUtxoNotContiguous, errUtxoBadBlock, errUtxoReorgTooDeep, errUtxoInvalid, errUtxoBroken}

func utxoIntegrityError(err error) bool {
	for _, target := range utxoIntegrityErrors {
		if errors.Is(err, target) {
			return true
		}
	}
	return false
}

func utxoRevHex(s string) ([32]byte, error) {
	var h [32]byte
	b, err := hex.DecodeString(s)
	if err != nil || len(b) != len(h) {
		return h, fmt.Errorf("%w: bad hash %q", errUtxoBadBlock, s)
	}
	for i, c := range b {
		h[31-i] = c
	}
	return h, nil
}

func (f *utxoFollower) Step(ctx context.Context) (err error) {
	if f.Status().State == utxoFollowError {
		return errUtxoFollowStop
	}
	defer func() {
		switch {
		case err == nil:
			f.set(utxoFollowIdle, nil)
		case ctx.Err() != nil:
			f.set(utxoFollowIdle, nil)
		case utxoIntegrityError(err):
			f.set(utxoFollowError, err)
		default:
			f.set(utxoFollowRetry, err)
		}
	}()
	var best string
	if err := f.rpc.call(ctx, "getbestblockhash", nil, &best); err != nil {
		return err
	}
	bestHash, err := utxoRevHex(best)
	if err != nil {
		return err
	}
	height, tip, hasTip, err := f.store.Tip()
	if err != nil {
		return err
	}
	if hasTip && tip == bestHash {
		return nil
	}
	var header struct {
		Height uint32 `json:"height"`
	}
	if err := f.rpc.call(ctx, "getblockheader", []any{best}, &header); err != nil {
		return err
	}
	next := uint32(0)
	if hasTip {
		if _, err := f.rollBack(ctx, header.Height); err != nil {
			return err
		}
		if height, _, hasTip, err = f.store.Tip(); err != nil {
			return err
		}
		if hasTip {
			next = height + 1
		}
	}
	f.set(utxoFollowCatching, nil)
	apply := f.applyHeights
	if f.chain.Replay {
		apply = f.applyRange
	}
	if err := apply(ctx, next, header.Height); err != nil {
		if errors.Is(err, errUtxoNotContiguous) {
			n, rerr := f.rollBack(ctx, header.Height)
			if rerr != nil {
				return rerr
			}
			var now string
			if n == 0 {
				if rerr := f.rpc.call(ctx, "getbestblockhash", nil, &now); rerr != nil {
					return rerr
				}
			}
			if n > 0 || now != best {
				return errUtxoChainMoved
			}
		}
		return err
	}
	return nil
}

func (f *utxoFollower) checkForkReachable(ctx context.Context, bestHeight uint32) error {
	height, hash, ok, err := f.store.Tip()
	if err != nil || !ok {
		return err
	}
	for {
		if height <= bestHeight {
			var onChain string
			if err := f.rpc.call(ctx, "getblockhash", []any{height}, &onChain); err != nil {
				return err
			}
			h, err := utxoRevHex(onChain)
			if err != nil {
				return err
			}
			if h == hash {
				return nil
			}
		}
		prev, ok, err := f.store.UndoPrevHash(height)
		if err != nil {
			return err
		}
		if !ok || height == 0 {
			// A reindexing or lagging node has not reached the journal yet, which is not a fork.
			if height > bestHeight {
				return fmt.Errorf("%w: node height %d", errUtxoNodeBehind, bestHeight)
			}
			return fmt.Errorf("%w: no undo record at height %d", errUtxoReorgTooDeep, height)
		}
		height, hash = height-1, prev
	}
}

func (f *utxoFollower) rollBack(ctx context.Context, bestHeight uint32) (int, error) {
	if err := f.checkForkReachable(ctx, bestHeight); err != nil {
		return 0, err
	}
	n := 0
	for {
		height, tip, ok, err := f.store.Tip()
		if err != nil || !ok {
			return n, err
		}
		if height <= bestHeight {
			var onChain string
			if err := f.rpc.call(ctx, "getblockhash", []any{height}, &onChain); err != nil {
				return n, err
			}
			h, err := utxoRevHex(onChain)
			if err != nil {
				return n, err
			}
			if h == tip {
				return n, nil
			}
		}
		if err := ctx.Err(); err != nil {
			return n, err
		}
		f.set(utxoFollowRollback, nil)
		if err := f.store.DisconnectTip(); err != nil {
			if errors.Is(err, errUtxoNoUndo) {
				return n, fmt.Errorf("%w: tip height %d", errUtxoReorgTooDeep, height)
			}
			return n, err
		}
		n++
	}
}

func (f *utxoFollower) applyHeights(ctx context.Context, from, to uint32) error {
	for h := from; h <= to; h++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := f.applyHeight(ctx, h); err != nil {
			return err
		}
	}
	return nil
}

func (f *utxoFollower) applyHeight(ctx context.Context, height uint32) error {
	start := time.Now()
	blk, err := f.fetchBlock(ctx, height)
	if err != nil {
		return err
	}
	return f.applyBlocks([]utxoBlock{blk}, time.Since(start))
}

func (f *utxoFollower) fetchBlock(ctx context.Context, height uint32) (utxoBlock, error) {
	var hash string
	if err := f.rpc.call(ctx, "getblockhash", []any{height}, &hash); err != nil {
		return utxoBlock{}, err
	}
	if f.chain.BlockVerbosity == 0 {
		return f.fetchRawBlock(ctx, height, hash)
	}
	var raw utxoV3Block
	if err := f.rpc.call(ctx, "getblock", []any{hash, f.chain.BlockVerbosity}, &raw); err != nil {
		return utxoBlock{}, err
	}
	blk, err := convertUtxoBlock(raw, f.chain.ReverseIndex)
	if err != nil {
		return utxoBlock{}, err
	}
	if blk.Height != height {
		return utxoBlock{}, fmt.Errorf("%w: asked for height %d, got %d", errUtxoBadBlock, height, blk.Height)
	}
	return blk, nil
}

// fetchRawBlock reads the serialized block, which costs the node far less memory than verbose JSON on large blocks.
func (f *utxoFollower) fetchRawBlock(ctx context.Context, height uint32, hash string) (utxoBlock, error) {
	want, err := utxoRevHex(hash)
	if err != nil {
		return utxoBlock{}, err
	}
	var raw []byte
	if hc, ok := f.rpc.(interface {
		callHex(context.Context, string, any) ([]byte, error)
	}); ok {
		raw, err = hc.callHex(ctx, "getblock", []any{hash, 0})
	} else {
		var hexBlock string
		if err = f.rpc.call(ctx, "getblock", []any{hash, 0}, &hexBlock); err == nil {
			raw, err = hex.DecodeString(hexBlock)
		}
	}
	if errors.Is(err, errRPCTooLarge) {
		return utxoBlock{}, fmt.Errorf("%w: block %d is larger than the dashboard reads over RPC", errUtxoBlockTooLarge, height)
	}
	if err != nil {
		return utxoBlock{}, err
	}
	blk, err := parseRawBlock(raw, height, f.chain)
	if err != nil {
		return utxoBlock{}, err
	}
	if blk.Hash != want {
		return utxoBlock{}, fmt.Errorf("%w: block at height %d does not hash to %s", errUtxoBadBlock, height, hash)
	}
	return blk, nil
}

func (f *utxoFollower) applyBlocks(blks []utxoBlock, fetchDur time.Duration) error {
	started := time.Now()
	if err := f.store.ApplyBlocks(blks); err != nil {
		return fmt.Errorf("apply block %d: %w", blks[0].Height, err)
	}
	f.mu.Lock()
	f.blocks += len(blks)
	f.fetchDur += fetchDur
	f.applyDur += time.Since(started)
	f.mu.Unlock()
	if f.onBlock != nil {
		f.onBlock(blks[len(blks)-1].Height)
	}
	return nil
}

// applyRange replays a long stretch: one goroutine fetches ahead while bounded batches commit with one sync each.
func (f *utxoFollower) applyRange(ctx context.Context, from, to uint32) error {
	if from > to {
		return nil
	}
	ctx, cancel := context.WithCancel(ctx)
	var fetcher sync.WaitGroup
	defer fetcher.Wait()
	defer cancel()
	type fetched struct {
		blk utxoBlock
		dur time.Duration
		err error
	}
	queue := make(chan fetched, utxoReplayPrefetch)
	var freed atomic.Int64
	var waiting atomic.Bool
	wake, starved := make(chan struct{}, 1), make(chan struct{}, 1)
	fetcher.Go(func() {
		defer close(queue)
		var taken int64
		room := func(cost int64) bool {
			for held := taken - freed.Load(); held > 0 && held+cost > int64(utxoReplayInflightOps); held = taken - freed.Load() {
				waiting.Store(true)
				select {
				case starved <- struct{}{}:
				default:
				}
				select {
				case <-wake:
				case <-ctx.Done():
					return false
				}
			}
			waiting.Store(false)
			return true
		}
		for h := from; h <= to && ctx.Err() == nil; h++ {
			// Fetch nothing while more than the budget is in flight, so a block over it is committed before the next is read.
			if !room(0) {
				return
			}
			start := time.Now()
			blk, err := f.fetchBlock(ctx, h)
			cost := replayCost(blk)
			if !room(cost) {
				return
			}
			taken += cost
			select {
			case queue <- fetched{blk, time.Since(start), err}:
			case <-ctx.Done():
				return
			}
			if err != nil || h == to {
				return
			}
		}
	})
	var (
		batch    []utxoBlock
		ops      int64
		fetchDur time.Duration
	)
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		err := f.applyBlocks(batch, fetchDur)
		freed.Add(ops)
		select {
		case wake <- struct{}{}:
		default:
		}
		batch, ops, fetchDur = batch[:0], 0, 0
		if err == nil && f.gate != nil {
			err = f.gate()
		}
		return err
	}
	for {
		var item fetched
		var open bool
		select {
		case item, open = <-queue:
		case <-starved:
			// The fetcher is waiting for budget held by the open batch, so commit it now.
			if err := flush(); err != nil {
				return err
			}
			continue
		}
		if !open {
			break
		}
		if item.err != nil {
			if err := flush(); err != nil {
				return err
			}
			return item.err
		}
		batch = append(batch, item.blk)
		ops += replayCost(item.blk)
		fetchDur += item.dur
		if len(batch) >= utxoReplayBatch || ops >= int64(utxoReplayBatchOps) || waiting.Load() && len(queue) == 0 {
			if err := flush(); err != nil {
				return err
			}
		}
	}
	if err := flush(); err != nil {
		return err
	}
	return ctx.Err()
}

// replayCost weighs a block by its coin operations plus one per KiB of output script, since a block of few outputs can still be 32 MB.
func replayCost(blk utxoBlock) int64 {
	var scripts int
	for _, c := range blk.Creates {
		scripts += len(c.Script)
	}
	return int64(len(blk.Creates)+len(blk.Spends)) + int64(scripts>>10)
}

// With reverse set, spends carry no script and the store resolves them through its reverse keys.
func convertUtxoBlock(raw utxoV3Block, reverse bool) (utxoBlock, error) {
	var blk utxoBlock
	var err error
	blk.Height = raw.Height
	if blk.Hash, err = utxoRevHex(raw.Hash); err != nil {
		return blk, err
	}
	if raw.Height > 0 {
		if blk.PrevHash, err = utxoRevHex(raw.Previous); err != nil {
			return blk, err
		}
	}
	for _, tx := range raw.Tx {
		txid, err := utxoRevHex(tx.Txid)
		if err != nil {
			return blk, err
		}
		for _, in := range tx.Vin {
			// MWEB inputs spend MWEB outputs, which are never in the transparent index.
			if in.Coinbase != "" || in.IsMWEB {
				continue
			}
			if in.Prevout == nil && !reverse {
				return blk, fmt.Errorf("%w: %s:%d", errUtxoNoPrevout, tx.Txid, in.Vout)
			}
			sp := utxoSpend{}
			if sp.Txid, err = utxoRevHex(in.Txid); err != nil {
				return blk, err
			}
			sp.Vout = in.Vout
			if !reverse {
				if sp.Script, err = hex.DecodeString(in.Prevout.ScriptPubKey.Hex); err != nil {
					return blk, fmt.Errorf("%w: prevout script", errUtxoBadBlock)
				}
			}
			blk.Spends = append(blk.Spends, sp)
		}
		if raw.Height == 0 {
			continue
		}
		for _, out := range tx.Vout {
			if out.IsMWEB {
				continue
			}
			c := utxoCreate{Amount: uint64(out.Value)}
			c.Txid, c.Vout = txid, out.N
			if c.Script, err = hex.DecodeString(out.ScriptPubKey.Hex); err != nil {
				return blk, fmt.Errorf("%w: output script", errUtxoBadBlock)
			}
			blk.Creates = append(blk.Creates, c)
		}
	}
	return blk, nil
}
