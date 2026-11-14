package main

import (
	"context"
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

type familyWatchBackend struct {
	ready    func(ctx context.Context, s *familyWatchStore, now int64) (familyWatchTip, bool)
	scan     func(ctx context.Context, s *familyWatchStore, address string, tip familyWatchTip) (familyWatchResult, string)
	validate func(ctx context.Context, s *familyWatchStore, address string) (string, error)
}

func (a *app) familyWatchNodes() []string {
	ids := []string{nodeLitecoin, nodeBitcoinCash}
	if a.cfg.dogecoinWatchWallet {
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
