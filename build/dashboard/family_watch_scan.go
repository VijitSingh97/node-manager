package main

import (
	"context"
	"encoding/json"
	"sync"
	"time"
)

const (
	familyWatchPollInterval = time.Minute
	familyWatchTipMaxAge    = 2 * time.Hour
)

func (s *familyWatchStore) requestScan() {
	select {
	case s.trigger <- struct{}{}:
	default:
	}
}

func (m *familyWatchManager) run(ctx context.Context) {
	var workers sync.WaitGroup
	for id, store := range m.stores {
		id, store := id, store
		workers.Add(1)
		go func() {
			defer workers.Done()
			ticker := time.NewTicker(familyWatchPollInterval)
			defer ticker.Stop()
			m.runStore(ctx, id, store, ticker.C)
		}()
	}
	workers.Wait()
}

func (m *familyWatchManager) runStore(ctx context.Context, id string, store *familyWatchStore, ticks <-chan time.Time) {
	m.scanNode(ctx, id, store)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticks:
			m.scanNode(ctx, id, store)
		case <-store.trigger:
			m.scanNode(ctx, id, store)
		}
	}
}

func (m *familyWatchManager) scanNode(parent context.Context, id string, store *familyWatchStore) {
	if !store.scanMu.TryLock() {
		return
	}
	defer store.scanMu.Unlock()
	if store.Err() != nil {
		return
	}
	node, ok := m.app.cfg.nodes[id]
	if !ok || !node.Enabled || !m.app.nodeWorkerRunnable(id, node.Enabled) {
		store.markWaiting()
		return
	}
	ctx, cancel := context.WithTimeout(parent, familyWatchRPCTimeout)
	defer cancel()
	now := store.now().Unix()
	tip, ready := store.backend.ready(ctx, store, now)
	if !ready {
		store.markWaiting()
		return
	}
	if store.fromIndex(ctx, tip, now) {
		return
	}
	due := store.walletsDue(now, tip)
	if store.backend.scanBatch != nil && len(due) > 0 {
		started := time.Now()
		amounts, result, outcome := store.backend.scanBatch(ctx, store, due, tip)
		for _, wallet := range due {
			if outcome == "ok" {
				store.record(wallet.ID, amounts[wallet.ID], result.Height, result.Best)
			} else {
				store.markFailed(wallet.ID)
			}
		}
		m.logScan(id, outcome, started, result.Height)
		return
	}
	for _, wallet := range due {
		if ctx.Err() != nil {
			store.markFailed(wallet.ID)
			return
		}
		started := time.Now()
		result, outcome := store.backend.scan(ctx, store, wallet.Address, tip)
		if outcome == "rescanning" {
			store.markRescanning(wallet.ID)
			continue
		}
		if outcome != "ok" {
			store.markFailed(wallet.ID)
			m.logScan(id, outcome, started, result.Height)
			continue
		}
		store.record(wallet.ID, result.Atomic, result.Height, result.Best)
		m.logScan(id, "ok", started, result.Height)
	}
}

func (m *familyWatchManager) logScan(chain, outcome string, started time.Time, height int64) {
	m.app.log.Printf("%s watch scan: outcome=%s scan=%.1fs height=%d", chain, outcome, time.Since(started).Seconds(), height)
}

func familyWatchWarningsClear(raw json.RawMessage) bool {
	if len(raw) == 0 || string(raw) == "null" {
		return true
	}
	var warning string
	if json.Unmarshal(raw, &warning) == nil {
		return warning == ""
	}
	var warnings []json.RawMessage
	return json.Unmarshal(raw, &warnings) == nil && len(warnings) == 0
}

func (s *familyWatchStore) walletsDue(now int64, tip familyWatchTip) []familyWatchEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	wallets := make([]familyWatchEntry, 0, len(s.wallets))
	for _, wallet := range s.wallets {
		if wallet.Example && s.skipExample {
			continue
		}
		balance, cached := s.balances[wallet.ID]
		state := s.states[wallet.ID]
		if !cached || state == "" || state == "error" || state == "stale" || state == "rescanning" || balance.ScanTime > now || now-balance.ScanTime >= int64(familyWatchInterval/time.Second) || s.backend.refreshOnTip && balance.BestBlock != tip.Best {
			wallets = append(wallets, wallet)
		}
	}
	return wallets
}

func (s *familyWatchStore) markWaiting() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, wallet := range s.wallets {
		_, cached := s.balances[wallet.ID]
		switch {
		case cached:
			s.states[wallet.ID] = "stale"
		case wallet.Example && s.skipExample:
		case s.importing != "":
			s.states[wallet.ID] = "rescanning"
		case s.indexOnly && s.index == nil:
			s.states[wallet.ID] = "index_off"
		default:
			s.states[wallet.ID] = "waiting"
		}
	}
}

func (s *familyWatchStore) markFailed(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.balances[id]; ok {
		s.states[id] = "stale"
	} else {
		s.states[id] = "error"
	}
}

func (s *familyWatchStore) record(id, amount string, height int64, bestBlock string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, wallet := range s.wallets {
		if wallet.ID != id {
			continue
		}
		now := s.now().Unix()
		balance := s.balances[id]
		balance.Amount, balance.ScanHeight, balance.ScanTime, balance.BestBlock = amount, height, now, bestBlock
		point := familyWatchHistory{Time: now, Amount: formatFamilyWatchAmount(amount)}
		// Balances read on every block would fill the history in hours, so a change within the hour updates that hour's point.
		if last := len(balance.History) - 1; (s.backend.refreshOnTip || s.index != nil) && last >= 0 && now-balance.History[last].Time < int64(familyWatchInterval/time.Second) {
			balance.History[last].Amount = point.Amount
		} else {
			balance.History = append(balance.History, point)
		}
		if len(balance.History) > maxFamilyWatchHistory {
			balance.History = append([]familyWatchHistory(nil), balance.History[len(balance.History)-maxFamilyWatchHistory:]...)
		}
		s.balances[id], s.states[id] = balance, "ok"
		if err := s.saveCacheLocked(); err != nil {
			s.states[id] = "stale"
			if s.logf != nil {
				s.logf("%s watch cache not saved; will retry on the next pass", s.chain)
			}
		}
		return
	}
}

func (s *familyWatchStore) markRescanning(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.states[id] = "rescanning"
}

// fromIndex serves every wallet from the chain's balance index when it sits at the node's tip, recording a wallet
// when its amount changed or its hourly point is due so the history keeps its spacing.
func (s *familyWatchStore) fromIndex(ctx context.Context, tip familyWatchTip, now int64) bool {
	if s.index == nil {
		return false
	}
	started := time.Now()
	s.mu.RLock()
	wallets := append([]familyWatchEntry(nil), s.wallets...)
	s.mu.RUnlock()
	hashes := make(map[string][utxoPrefixLen]byte, len(wallets))
	scripts := make([][]byte, 0, len(wallets))
	for _, wallet := range wallets {
		script, ok := s.script(ctx, wallet.Address)
		if !ok {
			return false
		}
		hashes[wallet.ID] = utxoScriptHash(script)
		scripts = append(scripts, script)
	}
	coins, height, hash, ok := s.index.Lookup(scripts)
	if !ok || reverseHex(hash) != tip.Best {
		return false
	}
	byHash := map[[utxoPrefixLen]byte]uint64{}
	for _, coin := range coins {
		byHash[coin.ScriptHash] += coin.Amount
	}
	sums := make(map[string]uint64, len(wallets))
	for _, wallet := range wallets {
		sums[wallet.ID] = byHash[hashes[wallet.ID]]
	}
	amounts, ok := familyWatchAmounts(wallets, sums, s.maxSupply)
	if !ok {
		return false
	}
	due := map[string]bool{}
	for _, wallet := range s.walletsDue(now, tip) {
		due[wallet.ID] = true
	}
	for _, wallet := range wallets {
		s.mu.RLock()
		balance, cached := s.balances[wallet.ID]
		state := s.states[wallet.ID]
		s.mu.RUnlock()
		if due[wallet.ID] || !cached || state != "ok" || balance.Amount != amounts[wallet.ID] {
			s.record(wallet.ID, amounts[wallet.ID], int64(height), tip.Best)
		}
	}
	if s.logf != nil && int64(height) != s.loggedHeight {
		s.loggedHeight = int64(height)
		s.logf("%s watch index: outcome=ok wallets=%d height=%d took=%.3fs", s.chain, len(wallets), height, time.Since(started).Seconds())
	}
	return true
}
