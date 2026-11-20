package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
)

type familyWatchTip struct {
	Blocks int64
	Best   string
}

type familyWatchResult struct {
	Atomic string
	Height int64
	Best   string
}

// scanBatch, when set, scans every due wallet in one walk and returns each wallet's amount by id.
type familyWatchBackend struct {
	refreshOnTip bool
	ready        func(ctx context.Context, s *familyWatchStore, now int64) (familyWatchTip, bool)
	scan         func(ctx context.Context, s *familyWatchStore, address string, tip familyWatchTip) (familyWatchResult, string)
	scanBatch    func(ctx context.Context, s *familyWatchStore, wallets []familyWatchEntry, tip familyWatchTip) (map[string]string, familyWatchResult, string)
	validate     func(ctx context.Context, s *familyWatchStore, address string) (string, error)
}

func (a *app) familyWatchNodes() []string {
	ids := []string{nodeLitecoin, nodeBitcoinCash}
	if a.cfg.dogecoinWatchWallet || a.cfg.nodes[nodeDogecoin].UTXOIndex {
		ids = append(ids, nodeDogecoin)
	}
	if a.cfg.zcashAddressIndex {
		ids = append(ids, nodeZcash)
	}
	return ids
}

func (a *app) familyWatchWorkerWanted() bool {
	for _, id := range a.familyWatchNodes() {
		if a.cfg.nodes[id].Enabled {
			return true
		}
	}
	return false
}

func utxoFamilyWatchBackend(litecoin bool) familyWatchBackend {
	return familyWatchBackend{
		ready: func(ctx context.Context, s *familyWatchStore, now int64) (familyWatchTip, bool) {
			var chain struct {
				Blocks               int64           `json:"blocks"`
				Headers              int64           `json:"headers"`
				BestBlockHash        string          `json:"bestblockhash"`
				InitialBlockDownload bool            `json:"initialblockdownload"`
				Warnings             json.RawMessage `json:"warnings"`
			}
			if s.rpc.call(ctx, "getblockchaininfo", nil, &chain) != nil || chain.InitialBlockDownload || chain.Blocks != chain.Headers || chain.BestBlockHash == "" || !familyWatchWarningsClear(chain.Warnings) {
				return familyWatchTip{}, false
			}
			return familyWatchTip{chain.Blocks, chain.BestBlockHash}, s.tipFresh(ctx, chain.BestBlockHash, now)
		},
		scan: func(ctx context.Context, s *familyWatchStore, address string, tip familyWatchTip) (familyWatchResult, string) {
			var scan struct {
				Success     bool            `json:"success"`
				TotalAmount json.RawMessage `json:"total_amount"`
				Height      int64           `json:"height"`
				BestBlock   string          `json:"bestblock"`
			}
			err := s.rpc.call(ctx, "scantxoutset", []any{"start", []any{"addr(" + address + ")"}}, &scan)
			atomic, parseErr := parseFamilyWatchAmount(scan.TotalAmount, s.maxSupply)
			if err != nil || parseErr != nil || !scan.Success || scan.Height < tip.Blocks || scan.BestBlock == "" {
				return familyWatchResult{}, "failed"
			}
			var mainChainBlock string
			if s.rpc.call(ctx, "getblockhash", []any{scan.Height}, &mainChainBlock) != nil || mainChainBlock != scan.BestBlock {
				return familyWatchResult{}, "off_chain"
			}
			return familyWatchResult{atomic, scan.Height, scan.BestBlock}, "ok"
		},
		scanBatch: func(ctx context.Context, s *familyWatchStore, wallets []familyWatchEntry, tip familyWatchTip) (map[string]string, familyWatchResult, string) {
			descriptors := make([]any, 0, len(wallets))
			owners := map[string][]string{}
			for _, wallet := range wallets {
				script, ok := s.script(ctx, wallet.Address)
				if !ok {
					return nil, familyWatchResult{}, "failed"
				}
				descriptors = append(descriptors, "addr("+wallet.Address+")")
				owners[string(script)] = append(owners[string(script)], wallet.ID)
			}
			var scan struct {
				Success   bool   `json:"success"`
				Height    int64  `json:"height"`
				BestBlock string `json:"bestblock"`
				Unspents  []struct {
					ScriptPubKey string    `json:"scriptPubKey"`
					Amount       rpcAmount `json:"amount"`
				} `json:"unspents"`
			}
			if err := s.rpc.call(ctx, "scantxoutset", []any{"start", descriptors}, &scan); err != nil || !scan.Success || scan.Height < tip.Blocks || scan.BestBlock == "" {
				return nil, familyWatchResult{}, "failed"
			}
			var mainChainBlock string
			if s.rpc.call(ctx, "getblockhash", []any{scan.Height}, &mainChainBlock) != nil || mainChainBlock != scan.BestBlock {
				return nil, familyWatchResult{}, "off_chain"
			}
			sums := map[string]uint64{}
			for _, coin := range scan.Unspents {
				script, err := hex.DecodeString(coin.ScriptPubKey)
				if err != nil {
					return nil, familyWatchResult{}, "failed"
				}
				for _, id := range owners[string(script)] {
					sums[id] += uint64(coin.Amount)
				}
			}
			amounts, ok := familyWatchAmounts(wallets, sums, s.maxSupply)
			if !ok {
				return nil, familyWatchResult{}, "failed"
			}
			return amounts, familyWatchResult{Height: scan.Height, Best: scan.BestBlock}, "ok"
		},
		validate: func(ctx context.Context, s *familyWatchStore, address string) (string, error) {
			var validated struct {
				IsValid      bool   `json:"isvalid"`
				Address      string `json:"address"`
				ScriptPubKey string `json:"scriptPubKey"`
				IsMWEB       bool   `json:"ismweb"`
			}
			if err := s.rpc.call(ctx, "validateaddress", []any{address}, &validated); err != nil || !validated.IsValid || !validFamilyWatchAddress(validated.Address) || validated.ScriptPubKey == "" {
				return "", errors.New("A valid public address for this node is required.")
			}
			if litecoin && validated.IsMWEB {
				return "", errors.New("A valid public Litecoin address is required.")
			}
			return validated.Address, nil
		},
	}
}

func zcashFamilyWatchBackend() familyWatchBackend {
	return familyWatchBackend{
		refreshOnTip: true,
		ready: func(ctx context.Context, s *familyWatchStore, now int64) (familyWatchTip, bool) {
			var chain zcashBlockchainInfo
			if s.rpc.call(ctx, "getblockchaininfo", nil, &chain) != nil || chain.BestBlockHash == "" || chain.Blocks < max(chain.Headers, chain.EstimatedHeight) || chain.VerificationProgress < 0.999999 {
				return familyWatchTip{}, false
			}
			return familyWatchTip{chain.Blocks, chain.BestBlockHash}, s.tipFresh(ctx, chain.BestBlockHash, now)
		},
		scan: func(ctx context.Context, s *familyWatchStore, address string, tip familyWatchTip) (familyWatchResult, string) {
			var balance struct {
				Balance uint64 `json:"balance"`
			}
			err := s.rpc.call(ctx, "getaddressbalance", []any{map[string]any{"addresses": []string{address}}}, &balance)
			if err != nil {
				if strings.Contains(strings.ReplaceAll(strings.ToLower(err.Error()), " ", ""), "indexesunavailable") {
					return familyWatchResult{}, "index_unavailable"
				}
				return familyWatchResult{}, "failed"
			}
			var after zcashBlockchainInfo
			if balance.Balance > s.maxSupply || s.rpc.call(ctx, "getblockchaininfo", nil, &after) != nil || after.BestBlockHash != tip.Best {
				return familyWatchResult{}, "failed"
			}
			return familyWatchResult{strconv.FormatUint(balance.Balance, 10), tip.Blocks, tip.Best}, "ok"
		},
		validate: func(ctx context.Context, s *familyWatchStore, address string) (string, error) {
			if !strings.HasPrefix(address, "t1") && !strings.HasPrefix(address, "t3") {
				return "", errors.New("Only transparent Zcash addresses (t1 or t3) can be watched; shielded and unified addresses need a viewing key.")
			}
			var validated struct {
				IsValid bool   `json:"isvalid"`
				Address string `json:"address"`
			}
			if err := s.rpc.call(ctx, "validateaddress", []any{address}, &validated); err != nil || !validated.IsValid || validated.Address != address {
				return "", errors.New("A valid public address for this node is required.")
			}
			return address, nil
		},
	}
}

func (s *familyWatchStore) tipFresh(ctx context.Context, best string, now int64) bool {
	var header familyBlockHeader
	return s.rpc.call(ctx, "getblockheader", []any{best}, &header) == nil && header.Time > 0 && header.Time <= now && now-header.Time <= int64(familyWatchTipMaxAge.Seconds())
}

func familyWatchAmounts(wallets []familyWatchEntry, sums map[string]uint64, maxSupply uint64) (map[string]string, bool) {
	amounts := make(map[string]string, len(wallets))
	for _, wallet := range wallets {
		if sums[wallet.ID] > maxSupply {
			return nil, false
		}
		amounts[wallet.ID] = strconv.FormatUint(sums[wallet.ID], 10)
	}
	return amounts, true
}

// script returns an address's scriptPubKey from the node, cached because an address never changes script.
func (s *familyWatchStore) script(ctx context.Context, address string) ([]byte, bool) {
	s.mu.RLock()
	script, ok := s.scripts[address]
	s.mu.RUnlock()
	if ok {
		return script, true
	}
	var validated struct {
		IsValid      bool   `json:"isvalid"`
		ScriptPubKey string `json:"scriptPubKey"`
	}
	if s.rpc.call(ctx, "validateaddress", []any{address}, &validated) != nil || !validated.IsValid {
		return nil, false
	}
	script, err := hex.DecodeString(validated.ScriptPubKey)
	if err != nil || len(script) == 0 {
		return nil, false
	}
	s.mu.Lock()
	if s.scripts == nil {
		s.scripts = map[string][]byte{}
	}
	s.scripts[address] = script
	s.mu.Unlock()
	return script, true
}
