package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

const (
	verifyRequestName = "utxo-verify.request"
	verifyResultName  = "utxo-verify.result"
	verifyLevelWait   = 10 * time.Minute
	verifyCallLimit   = 30 * time.Minute
)

type verifyRequest struct {
	ID          string `json:"id"`
	Chain       string `json:"chain"`
	RequestedAt int64  `json:"requested_at"`
}

type txOutSetInfo struct {
	Height    uint32    `json:"height"`
	BestBlock string    `json:"bestblock"`
	TxOuts    uint64    `json:"txouts"`
	Total     rpcAmount `json:"total_amount"`
}

type verifyTotals struct {
	Coins   uint64  `json:"coins"`
	Amount  string  `json:"amount"`
	Seconds float64 `json:"seconds"`
}

type verifyResult struct {
	ID        string        `json:"id"`
	Chain     string        `json:"chain"`
	Started   time.Time     `json:"started"`
	Finished  time.Time     `json:"finished"`
	HashType  string        `json:"hash_type"`
	Height    uint32        `json:"height,omitempty"`
	BestBlock string        `json:"best_block,omitempty"`
	Index     *verifyTotals `json:"index,omitempty"`
	Node      *verifyTotals `json:"node,omitempty"`
	Match     *bool         `json:"match,omitempty"`
	Refusals  []string      `json:"refusals,omitempty"`
	Failures  []string      `json:"failures,omitempty"`
}

func verifyAmount(units uint64) string {
	return fmt.Sprintf("%d.%08d", units/100_000_000, units%100_000_000)
}

// pollVerify runs on the run goroutine, so no follower step moves the index while it is counted.
func (m *utxoManager) pollVerify(ctx context.Context) {
	h := m.bench
	path := filepath.Join(h.stateDir, h.prefix+verifyRequestName)
	f, err := os.Open(path)
	if err != nil {
		return
	}
	raw, _ := io.ReadAll(io.LimitReader(f, benchRequestMaxBytes+1))
	_ = f.Close()
	var req verifyRequest
	if len(raw) > benchRequestMaxBytes || json.Unmarshal(raw, &req) != nil || !validPortControlID(req.ID) {
		_ = os.Remove(path)
		m.log.Print("utxo verify: request without a valid id dropped")
		return
	}
	res := verifyResult{ID: req.ID, Chain: h.chain, Started: m.now().UTC(), HashType: "none"}
	if !m.chain.TxOutSetNone {
		res.HashType = "hash_serialized"
	}
	var height uint32
	var hash [32]byte
	refusal := ""
	switch age := m.now().Sub(time.Unix(req.RequestedAt, 0)); {
	case req.Chain != h.chain:
		refusal = "the verify request names another chain"
	case age < -benchMaxAge || age > verifyLevelWait+benchMaxAge:
		refusal = "the verify request is stale"
	default:
		var retry bool
		height, hash, refusal, retry = m.verifyTip(ctx)
		if retry && age < verifyLevelWait {
			return
		}
	}
	_ = os.Remove(path)
	if refusal != "" {
		res.Refusals = []string{refusal}
	} else {
		m.log.Printf("utxo verify: start chain=%s height=%d hash_type=%s", h.chain, height, res.HashType)
		m.execVerify(ctx, &res, height, hash)
	}
	res.Finished = m.now().UTC()
	out, _ := json.Marshal(res)
	if writePrivateFileAtomic(filepath.Join(h.stateDir, h.prefix+verifyResultName), out) != nil {
		m.log.Print("utxo verify: result could not be written")
	}
	m.log.Printf("utxo verify: end chain=%s match=%t refusals=%d failures=%d duration=%.1fs", h.chain, res.Match != nil && *res.Match, len(res.Refusals), len(res.Failures), res.Finished.Sub(res.Started).Seconds())
}

// verifyTip returns the index tip once it is the node's best block; retry means the follower may still get there.
func (m *utxoManager) verifyTip(ctx context.Context) (height uint32, hash [32]byte, refusal string, retry bool) {
	if st := m.Status(); st.State != utxoStateReady && st.State != utxoStateCatching {
		return 0, hash, fmt.Sprintf("the index is %s; verify needs a seeded index that follows the node", st.State), false
	}
	height, hash, has, err := m.currentStore().Tip()
	if err != nil || !has {
		return 0, hash, "the index has no tip", false
	}
	var best string
	if m.rpc.call(ctx, "getbestblockhash", nil, &best) != nil {
		return height, hash, "the node did not answer getbestblockhash", true
	}
	if best != reverseHex(hash) {
		return height, hash, fmt.Sprintf("the index (height %d) did not reach the node tip within %d minutes", height, int(verifyLevelWait.Minutes())), true
	}
	return height, hash, "", false
}

func (m *utxoManager) execVerify(ctx context.Context, res *verifyResult, height uint32, hash [32]byte) {
	res.Height, res.BestBlock = height, reverseHex(hash)
	ctx, cancel := context.WithTimeout(ctx, verifyCallLimit)
	defer cancel()
	// The walk takes no cs_main, but a CPU-capped node may still answer health probes too slowly.
	m.mu.Lock()
	m.verifyUntil = m.now().Add(verifyCallLimit)
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		m.verifyUntil = time.Time{}
		m.mu.Unlock()
	}()
	type reply struct {
		info    txOutSetInfo
		err     error
		seconds float64
	}
	node := make(chan reply, 1)
	go func() {
		started := time.Now()
		info, err := m.bench.txOutSet(ctx)
		node <- reply{info, err, time.Since(started).Seconds()}
	}()
	started := time.Now()
	coins, units, err := m.currentStore().Totals(ctx)
	if err != nil {
		cancel()
		<-node
		m.log.Printf("utxo verify: index count failed: %v", err)
		res.Refusals = append(res.Refusals, "the index could not be counted; the dashboard log has the error")
		return
	}
	res.Index = &verifyTotals{Coins: coins, Amount: verifyAmount(units), Seconds: time.Since(started).Seconds()}
	r := <-node
	if r.err != nil {
		m.log.Printf("utxo verify: gettxoutsetinfo failed: %v", r.err)
		res.Refusals = append(res.Refusals, fmt.Sprintf("the node's gettxoutsetinfo failed, stopped or ran past %d minutes; the dashboard log has the error", int(verifyCallLimit.Minutes())))
		return
	}
	res.Node = &verifyTotals{Coins: r.info.TxOuts, Amount: verifyAmount(uint64(r.info.Total)), Seconds: r.seconds}
	if r.info.BestBlock != res.BestBlock {
		res.Refusals = append(res.Refusals, fmt.Sprintf("the node counted block %s at height %d, not the index tip; run it again", r.info.BestBlock, r.info.Height))
		return
	}
	match := r.info.TxOuts == coins && uint64(r.info.Total) == units
	res.Match = &match
	if !match {
		res.Failures = append(res.Failures, fmt.Sprintf("the index and the node's UTXO set differ at height %d", height))
	}
}
