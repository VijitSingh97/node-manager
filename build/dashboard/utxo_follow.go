package main

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

const utxoFollowInterval = 10 * time.Second

const (
	utxoFollowIdle     = "idle"
	utxoFollowCatching = "catching_up"
	utxoFollowReorg    = "reorg detected"
	utxoFollowError    = "error"
)

var (
	errUtxoReorg      = errors.New("reorg detected: indexed tip is not on the active chain")
	errUtxoNoPrevout  = errors.New("input has no prevout data")
	errUtxoBadBlock   = errors.New("malformed block from node")
	errUtxoFollowStop = errors.New("follower stopped")
)

type utxoRPC interface {
	call(ctx context.Context, method string, params any, out any) error
}

type utxoFollower struct {
	store *utxoStore
	rpc   utxoRPC

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
	return &utxoFollower{store: store, rpc: rpc, state: utxoFollowIdle}
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
		if err := f.Step(ctx); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
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
	if st := f.Status().State; st == utxoFollowReorg || st == utxoFollowError {
		return errUtxoFollowStop
	}
	defer func() {
		switch {
		case err == nil:
			f.set(utxoFollowIdle, nil)
		case errors.Is(err, errUtxoReorg):
			f.set(utxoFollowReorg, err)
		case ctx.Err() != nil:
			f.set(utxoFollowIdle, nil)
		default:
			f.set(utxoFollowError, err)
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
		if header.Height < height {
			return errUtxoReorg
		}
		if err := f.checkTipActive(ctx, height, tip); err != nil {
			return err
		}
		next = height + 1
	}
	f.set(utxoFollowCatching, nil)
	for h := next; h <= header.Height; h++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := f.applyHeight(ctx, h); err != nil {
			if errors.Is(err, errUtxoNotContiguous) {
				if th, tt, ok, terr := f.store.Tip(); terr == nil && ok {
					if cerr := f.checkTipActive(ctx, th, tt); cerr != nil {
						return cerr
					}
				}
			}
			return err
		}
	}
	return nil
}

func (f *utxoFollower) checkTipActive(ctx context.Context, height uint32, tip [32]byte) error {
	var onChain string
	if err := f.rpc.call(ctx, "getblockhash", []any{height}, &onChain); err != nil {
		return err
	}
	h, err := utxoRevHex(onChain)
	if err != nil {
		return err
	}
	if h != tip {
		return errUtxoReorg
	}
	return nil
}

func (f *utxoFollower) applyHeight(ctx context.Context, height uint32) error {
	start := time.Now()
	var hash string
	if err := f.rpc.call(ctx, "getblockhash", []any{height}, &hash); err != nil {
		return err
	}
	var raw utxoV3Block
	if err := f.rpc.call(ctx, "getblock", []any{hash, 3}, &raw); err != nil {
		return err
	}
	blk, err := convertUtxoBlock(raw)
	if err != nil {
		return err
	}
	if blk.Height != height {
		return fmt.Errorf("%w: asked for height %d, got %d", errUtxoBadBlock, height, blk.Height)
	}
	fetched := time.Now()
	if err := f.store.ApplyBlock(blk); err != nil {
		return fmt.Errorf("apply block %d: %w", height, err)
	}
	f.mu.Lock()
	f.blocks++
	f.fetchDur += fetched.Sub(start)
	f.applyDur += time.Since(fetched)
	f.mu.Unlock()
	return nil
}

func convertUtxoBlock(raw utxoV3Block) (utxoBlock, error) {
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
			if in.Coinbase != "" {
				continue
			}
			if in.Prevout == nil {
				return blk, fmt.Errorf("%w: %s:%d", errUtxoNoPrevout, tx.Txid, in.Vout)
			}
			sp := utxoSpend{}
			if sp.Txid, err = utxoRevHex(in.Txid); err != nil {
				return blk, err
			}
			sp.Vout = in.Vout
			if sp.Script, err = hex.DecodeString(in.Prevout.ScriptPubKey.Hex); err != nil {
				return blk, fmt.Errorf("%w: prevout script", errUtxoBadBlock)
			}
			blk.Spends = append(blk.Spends, sp)
		}
		if raw.Height == 0 {
			continue
		}
		for _, out := range tx.Vout {
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
