package main

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"sort"
	"sync"
	"time"
)

const (
	prunedScanTimeout  = 30 * time.Minute
	prunedScanCooldown = 10 * time.Minute
	prunedDeltaBlocks  = 144
	prunedBlockTimeout = time.Minute
)

type prunedScanner struct {
	app      *app
	mu       sync.Mutex
	queued   map[string]bool
	running  bool
	failures map[string]int
	lastTry  map[string]time.Time
	jobs     chan WatchEntry
	now      func() time.Time
	derived  map[string]derivedAddresses
}

type derivedAddresses struct {
	descriptorID string
	set          map[string]bool
}

type deltaBlock struct {
	Hash              string `json:"hash"`
	PreviousBlockHash string `json:"previousblockhash"`
	Tx                []struct {
		Txid string `json:"txid"`
		Vin  []struct {
			Txid string `json:"txid"`
			Vout int64  `json:"vout"`
		} `json:"vin"`
		Vout []struct {
			Value        float64 `json:"value"`
			N            int64   `json:"n"`
			ScriptPubKey struct {
				Address string `json:"address"`
			} `json:"scriptPubKey"`
		} `json:"vout"`
	} `json:"tx"`
}

type scanTxOutSetResult struct {
	Success     bool                  `json:"success"`
	TxOuts      int64                 `json:"txouts"`
	Height      int64                 `json:"height"`
	BestBlock   string                `json:"bestblock"`
	Unspents    []scanTxOutSetUnspent `json:"unspents"`
	TotalAmount float64               `json:"total_amount"`
}

type scanTxOutSetUnspent struct {
	TxID         string  `json:"txid"`
	Vout         int64   `json:"vout"`
	ScriptPubKey string  `json:"scriptPubKey"`
	Desc         string  `json:"desc"`
	Amount       float64 `json:"amount"`
	Height       int64   `json:"height"`
}

type validatedScanOutput struct {
	scanOutpoint
	Desc string
}

type scanBatchPlan struct {
	entry        WatchEntry
	identity     string
	descriptorID string
	addresses    map[string]bool
}

func newPrunedScanner(a *app) *prunedScanner {
	return &prunedScanner{
		app: a, queued: map[string]bool{}, failures: map[string]int{}, lastTry: map[string]time.Time{},
		jobs: make(chan WatchEntry, maxWatchWallets), now: time.Now, derived: map[string]derivedAddresses{},
	}
}

func (s *prunedScanner) run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case entry := <-s.jobs:
			if !s.app.nodeWorkerRunnable(nodeBitcoin, true) {
				s.mu.Lock()
				delete(s.queued, watchKeyID(entry.Key))
				s.mu.Unlock()
				continue
			}
			triggered := []WatchEntry{entry}
			s.mu.Lock()
			s.running = true
			s.mu.Unlock()
			for {
				select {
				case next := <-s.jobs:
					triggered = append(triggered, next)
				default:
					s.scanBatch(ctx)
					s.mu.Lock()
					for _, item := range triggered {
						delete(s.queued, watchKeyID(item.Key))
					}
					s.running = false
					s.mu.Unlock()
					goto done
				}
			}
		done:
		}
	}
}

func (s *prunedScanner) enqueue(entry WatchEntry, force bool) bool {
	if !s.app.nodeWorkerRunnable(nodeBitcoin, true) || !s.app.watch.Contains(entry) || s.app.watch.ScanErr() != nil {
		return false
	}
	identity := watchKeyID(entry.Key)
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running || s.queued[identity] {
		return false
	}
	elapsed := now.Sub(s.lastTry[identity])
	if !force && elapsed >= 0 && elapsed < scanRetryDelay(s.failures[identity]) {
		return false
	}
	s.queued[identity] = true
	s.app.watch.SetPending(entry.Key, true)
	select {
	case s.jobs <- entry:
		return true
	default:
		delete(s.queued, identity)
		return false
	}
}

func scanRetryDelay(failures int) time.Duration {
	if failures <= 0 {
		return 0
	}
	delay := 15 * time.Second * time.Duration(1<<min(failures-1, 5))
	return min(delay, prunedScanCooldown)
}

func (s *prunedScanner) refresh(entries []WatchEntry, chain blockchainInfo) {
	if !s.app.nodeWorkerRunnable(nodeBitcoin, true) || !chain.Pruned || chain.InitialBlockDownload || chain.BestBlockHash == "" || s.app.watch.ScanErr() != nil {
		return
	}
	behind := map[string][]WatchEntry{}
	for _, entry := range entries {
		if s.app.watch.hasResolvedWallet(s.app.walletRPC, entry) {
			s.app.watch.ClearPendingIfCurrent(entry)
			continue
		}
		cache, _, ok := s.app.watch.scanSnapshot(entry)
		if ok && cache.BestBlock != chain.BestBlockHash && chain.Blocks > cache.Height && chain.Blocks-cache.Height <= prunedDeltaBlocks {
			behind[cache.BestBlock] = append(behind[cache.BestBlock], entry)
			continue
		}
		s.rescanIfStale(entry, cache, ok, chain)
	}
	for base, group := range behind {
		if !s.applyBlocks(group, base, chain) {
			for _, entry := range group {
				cache, _, ok := s.app.watch.scanSnapshot(entry)
				s.rescanIfStale(entry, cache, ok, chain)
			}
		}
	}
}

func (s *prunedScanner) rescanIfStale(entry WatchEntry, cache prunedScanCache, cached bool, chain blockchainInfo) {
	age := s.now().Sub(cache.ScannedAt)
	if !cached || cache.BestBlock != chain.BestBlockHash && (age < 0 || age >= prunedScanCooldown) {
		s.enqueue(entry, false)
	}
}

// Applies the blocks after a shared cached tip to each wallet's outpoints; false means a full scan is needed instead.
func (s *prunedScanner) applyBlocks(entries []WatchEntry, base string, chain blockchainInfo) bool {
	s.mu.Lock()
	if s.running || len(s.queued) > 0 {
		s.mu.Unlock()
		return true
	}
	s.running = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.running = false
		s.mu.Unlock()
	}()
	started := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), prunedScanTimeout)
	defer cancel()
	type wallet struct {
		entry     WatchEntry
		cache     prunedScanCache
		addresses map[string]bool
		outpoints map[string]scanOutpoint
	}
	wallets := make([]wallet, 0, len(entries))
	height := int64(-1)
	for _, entry := range entries {
		cache, _, ok := s.app.watch.scanSnapshot(entry)
		if !ok || cache.BestBlock != base || height >= 0 && cache.Height != height {
			return false
		}
		height = cache.Height
		addresses, err := s.walletAddresses(ctx, entry)
		if err != nil {
			return false
		}
		outpoints := make(map[string]scanOutpoint, len(cache.Outpoints))
		for _, outpoint := range cache.Outpoints {
			outpoints[fmt.Sprintf("%s:%d", outpoint.TxID, outpoint.Vout)] = outpoint
		}
		wallets = append(wallets, wallet{entry: entry, cache: cache, addresses: addresses, outpoints: outpoints})
	}
	previous := base
	for next := height + 1; next <= chain.Blocks; next++ {
		var hash string
		var block deltaBlock
		blockCtx, blockCancel := context.WithTimeout(ctx, prunedBlockTimeout)
		err := s.rpcCall(blockCtx, "getblockhash", []any{next}, &hash)
		if err == nil {
			err = s.rpcCall(blockCtx, "getblock", []any{hash, 2}, &block)
		}
		blockCancel()
		if err != nil || block.Hash != hash || block.PreviousBlockHash != previous {
			return false
		}
		for _, tx := range block.Tx {
			for _, input := range tx.Vin {
				if input.Txid == "" {
					continue
				}
				spent := fmt.Sprintf("%s:%d", input.Txid, input.Vout)
				for i := range wallets {
					delete(wallets[i].outpoints, spent)
				}
			}
			for _, output := range tx.Vout {
				if output.ScriptPubKey.Address == "" {
					continue
				}
				for i := range wallets {
					if !wallets[i].addresses[output.ScriptPubKey.Address] {
						continue
					}
					sats, ok := btcSatoshis(output.Value)
					if !ok {
						return false
					}
					wallets[i].outpoints[fmt.Sprintf("%s:%d", tx.Txid, output.N)] = scanOutpoint{TxID: tx.Txid, Vout: output.N, Sats: sats, Height: next}
				}
			}
		}
		previous = hash
	}
	if previous != chain.BestBlockHash {
		return false
	}
	now := s.now().UTC()
	caches := make(map[string]entryScanCache, len(wallets))
	for _, item := range wallets {
		outpoints := make([]scanOutpoint, 0, len(item.outpoints))
		var sats int64
		for _, outpoint := range item.outpoints {
			outpoints = append(outpoints, outpoint)
			sats += outpoint.Sats
		}
		sort.Slice(outpoints, func(i, j int) bool {
			return outpoints[i].TxID < outpoints[j].TxID || outpoints[i].TxID == outpoints[j].TxID && outpoints[i].Vout < outpoints[j].Vout
		})
		caches[watchKeyID(item.entry.Key)] = entryScanCache{entry: item.entry, cache: prunedScanCache{
			DescriptorID: item.cache.DescriptorID, BTC: float64(sats) / 1e8, Height: chain.Blocks,
			BestBlock: previous, ScannedAt: now, Outpoints: outpoints,
		}}
	}
	stored := s.app.watch.StoreScanBatch(caches)
	if len(stored) != len(caches) {
		return false
	}
	for _, item := range stored {
		s.app.watch.history.record(item.entry.Key, item.cache.BTC, item.cache.ScannedAt)
	}
	s.app.log.Printf("watch block update: wallets=%d blocks=%d height=%d took=%.1fs", len(stored), chain.Blocks-height, chain.Blocks, time.Since(started).Seconds())
	return true
}

func (s *prunedScanner) walletAddresses(ctx context.Context, entry WatchEntry) (map[string]bool, error) {
	identity, descriptorID := watchKeyID(entry.Key), descriptorSetID(entry)
	s.mu.Lock()
	cached, ok := s.derived[identity]
	s.mu.Unlock()
	if ok && cached.descriptorID == descriptorID {
		return cached.set, nil
	}
	_, addresses, err := s.scanObjects(ctx, entry)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.derived[identity] = derivedAddresses{descriptorID: descriptorID, set: addresses}
	s.mu.Unlock()
	return addresses, nil
}

func (s *prunedScanner) scanBatch(parent context.Context) {
	if !s.app.nodeWorkerRunnable(nodeBitcoin, true) {
		return
	}
	ctx, cancel := context.WithTimeout(parent, prunedScanTimeout)
	defer cancel()
	entries, pending := s.app.watch.snapshot()
	plans := make([]scanBatchPlan, 0, len(entries))
	objectsByDescriptor := map[string]any{}
	for _, entry := range entries {
		identity := watchKeyID(entry.Key)
		if s.app.watch.hasResolvedWallet(s.app.walletRPC, entry) {
			s.app.watch.ClearPendingIfCurrent(entry)
			s.succeeded(identity)
			continue
		}
		if _, _, cached := s.app.watch.scanSnapshot(entry); cached && !pending[identity] {
			continue
		}
		objects, addresses, err := s.scanObjects(ctx, entry)
		if err != nil {
			s.failed(entry, identity)
			continue
		}
		for descriptor, object := range objects {
			objectsByDescriptor[descriptor] = object
		}
		plans = append(plans, scanBatchPlan{entry: entry, identity: identity, descriptorID: descriptorSetID(entry), addresses: addresses})
		s.mu.Lock()
		s.lastTry[identity] = s.now()
		s.mu.Unlock()
	}
	if len(plans) == 0 {
		return
	}
	objects := make([]any, 0, len(objectsByDescriptor))
	descriptors := make([]string, 0, len(objectsByDescriptor))
	for descriptor := range objectsByDescriptor {
		descriptors = append(descriptors, descriptor)
	}
	sort.Strings(descriptors)
	for _, descriptor := range descriptors {
		objects = append(objects, objectsByDescriptor[descriptor])
	}
	var status map[string]any
	if s.rpcCall(ctx, "scantxoutset", []any{"status"}, &status) == nil && len(status) > 0 {
		s.failPlans(plans)
		return
	}
	active := plans[:0]
	for _, plan := range plans {
		if s.app.watch.SetScanStateIfCurrent(plan.entry, "scanning") {
			active = append(active, plan)
		}
	}
	plans = active
	if len(plans) == 0 {
		return
	}
	var result scanTxOutSetResult
	if err := s.rpcCall(ctx, "scantxoutset", []any{"start", objects}, &result); err != nil {
		s.failPlans(plans)
		return
	}
	outputs, err := validatedScanOutputs(result)
	if err != nil {
		s.failPlans(plans)
		return
	}
	matched := make(map[string][]scanOutpoint, len(plans))
	derivedOutputs := map[string][]string{}
	for _, output := range outputs {
		addresses, derived := derivedOutputs[output.Desc]
		if !derived {
			shortCtx, shortCancel := context.WithTimeout(ctx, 15*time.Second)
			err := s.app.rpc.call(shortCtx, "deriveaddresses", []any{output.Desc}, &addresses)
			shortCancel()
			if err != nil || len(addresses) == 0 {
				s.failPlans(plans)
				return
			}
			derivedOutputs[output.Desc] = addresses
		}
		owners := map[string]bool{}
		for _, plan := range plans {
			for _, address := range addresses {
				if plan.addresses[address] {
					owners[plan.identity] = true
				}
			}
		}
		if len(owners) == 0 {
			s.failPlans(plans)
			return
		}
		for identity := range owners {
			matched[identity] = append(matched[identity], output.scanOutpoint)
		}
	}
	now := s.now().UTC()
	caches := make(map[string]entryScanCache, len(plans))
	for _, plan := range plans {
		outpoints := matched[plan.identity]
		if outpoints == nil {
			outpoints = []scanOutpoint{}
		}
		var sats int64
		for _, outpoint := range outpoints {
			sats += outpoint.Sats
		}
		caches[plan.identity] = entryScanCache{entry: plan.entry, cache: prunedScanCache{
			DescriptorID: plan.descriptorID, BTC: float64(sats) / 1e8, Height: result.Height,
			BestBlock: result.BestBlock, ScannedAt: now, Outpoints: outpoints,
		}}
	}
	stored := s.app.watch.StoreScanBatch(caches)
	if len(stored) != len(caches) {
		if s.app.watch.ScanErr() != nil {
			s.app.log.Print("watch balance cache unavailable; pruned balance scans disabled until the cache is repaired and the dashboard restarts")
		}
		s.failPlans(plans)
		return
	}
	for _, item := range stored {
		s.app.watch.history.record(item.entry.Key, item.cache.BTC, item.cache.ScannedAt)
		s.succeeded(watchKeyID(item.entry.Key))
	}
}

func (s *prunedScanner) rpcCall(ctx context.Context, method string, params []any, out any) error {
	if !s.app.nodeWorkerRunnable(nodeBitcoin, true) {
		return context.Canceled
	}
	rpc := *s.app.rpc
	client := *s.app.rpc.client
	client.Timeout = 0
	rpc.client = &client
	// getblock verbosity 2 of a full mainnet block is well over the default 4 MiB.
	rpc.maxBody = 64 << 20
	return rpc.call(ctx, method, params, out)
}

func (s *prunedScanner) scanObjects(ctx context.Context, entry WatchEntry) (map[string]any, map[string]bool, error) {
	descriptors, err := descriptorsFor(entry.Key)
	if err != nil {
		return nil, nil, err
	}
	objects := make(map[string]any, len(descriptors))
	addresses := map[string]bool{}
	for _, item := range descriptors {
		object, derived, descriptor, err := s.publicScanObject(ctx, item.Desc)
		if err != nil {
			return nil, nil, err
		}
		objects[descriptor] = object
		for _, address := range derived {
			addresses[address] = true
		}
	}
	if len(addresses) == 0 {
		return nil, nil, errors.New("descriptor has no addresses")
	}
	return objects, addresses, nil
}

func (s *prunedScanner) publicScanObject(ctx context.Context, descriptor string) (any, []string, string, error) {
	var info struct {
		Descriptor     string `json:"descriptor"`
		IsRange        *bool  `json:"isrange"`
		HasPrivateKeys *bool  `json:"hasprivatekeys"`
	}
	shortCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	err := s.app.rpc.call(shortCtx, "getdescriptorinfo", []any{descriptor}, &info)
	cancel()
	if err != nil || info.Descriptor == "" || info.IsRange == nil || info.HasPrivateKeys == nil || *info.HasPrivateKeys {
		return nil, nil, "", errors.New("invalid public descriptor")
	}
	params := []any{info.Descriptor}
	var object any = info.Descriptor
	if *info.IsRange {
		rangeValue := []int{0, 999}
		params = append(params, rangeValue)
		object = map[string]any{"desc": info.Descriptor, "range": rangeValue}
	}
	var addresses []string
	shortCtx, cancel = context.WithTimeout(ctx, 15*time.Second)
	err = s.app.rpc.call(shortCtx, "deriveaddresses", params, &addresses)
	cancel()
	if err != nil || len(addresses) == 0 {
		return nil, nil, "", errors.New("descriptor cannot derive addresses")
	}
	return object, addresses, info.Descriptor, nil
}

func validatedScanOutputs(result scanTxOutSetResult) ([]validatedScanOutput, error) {
	if !result.Success || result.Height < 0 || len(result.BestBlock) != 64 || result.TxOuts < int64(len(result.Unspents)) {
		return nil, errors.New("invalid scantxoutset result")
	}
	if _, err := hex.DecodeString(result.BestBlock); err != nil {
		return nil, errors.New("invalid scantxoutset block")
	}
	seen := make(map[string]int64, len(result.Unspents))
	outputs := make([]validatedScanOutput, 0, len(result.Unspents))
	var total int64
	const maxSatoshis = int64(21_000_000 * 1e8)
	for _, output := range result.Unspents {
		if len(output.TxID) != 64 || output.Vout < 0 || output.ScriptPubKey == "" || output.Desc == "" || output.Height < 0 || output.Height > result.Height {
			return nil, errors.New("invalid scantxoutset output")
		}
		if _, err := hex.DecodeString(output.TxID); err != nil {
			return nil, errors.New("invalid scantxoutset transaction")
		}
		sats, ok := btcSatoshis(output.Amount)
		if !ok {
			return nil, errors.New("invalid scantxoutset amount")
		}
		key := fmt.Sprintf("%s:%d", output.TxID, output.Vout)
		if previous, duplicate := seen[key]; duplicate {
			if previous != sats {
				return nil, errors.New("conflicting duplicate scantxoutset output")
			}
			continue
		}
		seen[key] = sats
		if sats > maxSatoshis-total {
			return nil, errors.New("scantxoutset total exceeds bitcoin supply")
		}
		total += sats
		outputs = append(outputs, validatedScanOutput{scanOutpoint: scanOutpoint{TxID: output.TxID, Vout: output.Vout, Sats: sats, Height: output.Height}, Desc: output.Desc})
	}
	expected, ok := btcSatoshis(result.TotalAmount)
	if !ok || expected != total {
		return nil, errors.New("scantxoutset total does not match outputs")
	}
	return outputs, nil
}

func validatedScanBalance(result scanTxOutSetResult) (float64, error) {
	outputs, err := validatedScanOutputs(result)
	if err != nil {
		return 0, err
	}
	var sats int64
	for _, output := range outputs {
		sats += output.Sats
	}
	return float64(sats) / 1e8, nil
}

func btcSatoshis(value float64) (int64, bool) {
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 21_000_000 {
		return 0, false
	}
	sats := math.Round(value * 1e8)
	return int64(sats), math.Abs(value-sats/1e8) < 0.000000001
}

func (s *prunedScanner) failPlans(plans []scanBatchPlan) {
	for _, plan := range plans {
		s.failed(plan.entry, plan.identity)
	}
}

func (s *prunedScanner) failed(entry WatchEntry, identity string) {
	s.mu.Lock()
	s.failures[identity]++
	s.lastTry[identity] = s.now()
	s.mu.Unlock()
	s.app.watch.SetScanStateIfCurrent(entry, "error")
	s.app.watch.ClearPendingIfCurrent(entry)
}

func (s *prunedScanner) succeeded(identity string) {
	s.mu.Lock()
	delete(s.failures, identity)
	s.mu.Unlock()
}
