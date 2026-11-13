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
	var chain struct {
		Blocks               int64           `json:"blocks"`
		Headers              int64           `json:"headers"`
		BestBlockHash        string          `json:"bestblockhash"`
		InitialBlockDownload bool            `json:"initialblockdownload"`
		Warnings             json.RawMessage `json:"warnings"`
	}
	if store.rpc.call(ctx, "getblockchaininfo", nil, &chain) != nil || chain.InitialBlockDownload || chain.Blocks != chain.Headers || chain.BestBlockHash == "" || !familyWatchWarningsClear(chain.Warnings) {
		store.markWaiting()
		return
	}
	var header familyBlockHeader
	now := store.now().Unix()
	if store.rpc.call(ctx, "getblockheader", []any{chain.BestBlockHash}, &header) != nil || header.Time <= 0 || header.Time > now || now-header.Time > int64(familyWatchTipMaxAge/time.Second) {
		store.markWaiting()
		return
	}
	wallets := store.walletsDue(now)
	for _, wallet := range wallets {
		if ctx.Err() != nil {
			store.markFailed(wallet.ID)
			return
		}
		var scan struct {
			Success     bool            `json:"success"`
			TotalAmount json.RawMessage `json:"total_amount"`
			Height      int64           `json:"height"`
			BestBlock   string          `json:"bestblock"`
		}
		started := time.Now()
		err := store.rpc.call(ctx, "scantxoutset", []any{"start", []any{"addr(" + wallet.Address + ")"}}, &scan)
		atomic, parseErr := parseFamilyWatchAmount(scan.TotalAmount, store.maxSupply)
		if err != nil || parseErr != nil || !scan.Success || scan.Height < chain.Blocks || scan.BestBlock == "" {
			store.markFailed(wallet.ID)
			m.logScan(id, "failed", started)
			continue
		}
		var mainChainBlock string
		if store.rpc.call(ctx, "getblockhash", []any{scan.Height}, &mainChainBlock) != nil || mainChainBlock != scan.BestBlock {
			store.markFailed(wallet.ID)
			m.logScan(id, "off_chain", started)
			continue
		}
		store.record(wallet.ID, atomic, scan.Height, scan.BestBlock)
		m.logScan(id, "ok", started)
	}
}

func (m *familyWatchManager) logScan(chain, outcome string, started time.Time) {
	m.app.log.Printf("%s watch scan: outcome=%s scan=%.1fs", chain, outcome, time.Since(started).Seconds())
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

func (s *familyWatchStore) walletsDue(now int64) []familyWatchEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	wallets := make([]familyWatchEntry, 0, len(s.wallets))
	for _, wallet := range s.wallets {
		balance, cached := s.balances[wallet.ID]
		state := s.states[wallet.ID]
		if !cached || state == "error" || state == "stale" || balance.ScanTime > now || now-balance.ScanTime >= int64(familyWatchInterval/time.Second) {
			wallets = append(wallets, wallet)
		}
	}
	return wallets
}

func (s *familyWatchStore) markWaiting() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, wallet := range s.wallets {
		if _, ok := s.balances[wallet.ID]; ok {
			s.states[wallet.ID] = "stale"
		} else {
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
		balance.History = append(balance.History, familyWatchHistory{Time: now, Amount: formatFamilyWatchAmount(amount)})
		if len(balance.History) > maxFamilyWatchHistory {
			balance.History = append([]familyWatchHistory(nil), balance.History[len(balance.History)-maxFamilyWatchHistory:]...)
		}
		s.balances[id], s.states[id] = balance, "ok"
		if err := s.saveCacheLocked(); err != nil {
			s.err = err
			s.states[id] = "stale"
		}
		return
	}
}
