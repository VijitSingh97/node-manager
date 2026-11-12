package main

import (
	"encoding/json"
	"math"
	"sort"
	"strings"
	"time"
)

// Cumulative confirmed unspent funds sampled at evenly spaced heights from the
// first arrival to the scan tip; nil when any outpoint predates height caching.
func chainHistorySeries(outpoints []scanOutpoint, tip int64) []float64 {
	first := int64(0)
	for _, output := range outpoints {
		if output.Height <= 0 || output.Height > tip {
			return nil
		}
		if first == 0 || output.Height < first {
			first = output.Height
		}
	}
	if first == 0 {
		return nil
	}
	sorted := append([]scanOutpoint(nil), outpoints...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Height < sorted[j].Height })
	const samples = 60
	series := make([]float64, 0, samples)
	index, sats := 0, int64(0)
	for i := int64(0); i < samples; i++ {
		height := first + (tip-first)*i/(samples-1)
		for index < len(sorted) && sorted[index].Height <= height {
			sats += sorted[index].Sats
			index++
		}
		series = append(series, float64(sats)/1e8)
	}
	return series
}

func (s *WatchStore) List(walletRPC WalletRPCFunc, initialBlockDownload bool) map[string]any {
	return s.ListWithChain(walletRPC, blockchainInfo{InitialBlockDownload: initialBlockDownload})
}

func (s *WatchStore) ListWithChain(walletRPC WalletRPCFunc, chain blockchainInfo) map[string]any {
	entries, pending := s.snapshot()
	rows, total, totalReason := s.walletBalances(walletRPC, entries, pending, chain, nil)
	totalState := "ok"
	personalCount := 0
	for _, row := range rows {
		history := s.history.series(row["key"].(string))
		row["history"] = history
		if row["balance_kind"] != "confirmed" && row["state"] == "error" && len(history) > 0 {
			row["state"] = "stale"
			row["btc"] = formatBTC(history[len(history)-1])
		}
		if row["example"] == true {
			continue
		}
		personalCount++
		if row["state"] == "pending" || row["state"] == "scanning" {
			totalState = "pending"
		} else if totalState == "ok" && (row["state"] == "error" || row["state"] == "stale") {
			totalState = "unavailable"
		}
	}
	if totalReason != "" {
		total = 0
		if totalState == "ok" {
			totalState = "unavailable"
		}
	}
	view := map[string]any{"wallets": rows, "total": formatBTC(total), "total_state": totalState, "show_total": personalCount > 1, "pruned": chain.Pruned}
	if s.ScanErr() != nil {
		view["scan_cache_unavailable"] = true
	}
	if totalReason != "" {
		view["total_reason"] = totalReason
	}
	return view
}

func (s *WatchStore) Sample(walletRPC WalletRPCFunc) {
	entries, pending := s.snapshot()
	s.walletBalances(walletRPC, entries, pending, blockchainInfo{}, func(key string, btc float64) {
		s.history.record(key, btc, time.Now())
	})
}

func (s *WatchStore) resolveWallet(walletRPC WalletRPCFunc, entry WatchEntry) (string, map[string]any) {
	identity := watchKeyID(entry.Key)
	s.mu.Lock()
	name := s.coreNames[identity]
	s.mu.Unlock()
	if name != "" {
		if info := mapValue(walletRPC(name, "getwalletinfo", []any{}, 8*time.Second)); safeWatchWallet(info) && walletMatches(walletRPC, name, entry) {
			return name, info
		}
		s.SetCoreWallet(entry.Key, "")
	}
	name, info := resolveWallet(walletRPC, entry)
	if name != "" {
		s.SetCoreWallet(entry.Key, name)
	}
	return name, info
}

func (s *WatchStore) hasResolvedWallet(walletRPC WalletRPCFunc, entry WatchEntry) bool {
	identity := watchKeyID(entry.Key)
	s.mu.Lock()
	name := s.coreNames[identity]
	s.mu.Unlock()
	if name == "" {
		return false
	}
	if safeWatchWallet(mapValue(walletRPC(name, "getwalletinfo", []any{}, 8*time.Second))) && walletMatches(walletRPC, name, entry) {
		return true
	}
	s.SetCoreWallet(entry.Key, "")
	return false
}

func (s *WatchStore) walletBalances(walletRPC WalletRPCFunc, entries []WatchEntry, pending map[string]bool, chain blockchainInfo, sample func(string, float64)) ([]map[string]any, float64, string) {
	rows := make([]map[string]any, 0, len(entries))
	coreTotal := 0.0
	coreCount := 0
	scanCount := 0
	scanOutpoints := map[string]int64{}
	totalReason := ""
	scanUnavailable := s.ScanErr() != nil
	for _, entry := range entries {
		name, info := s.resolveWallet(walletRPC, entry)
		example := isPublicExample(entry)
		row := map[string]any{"name": entry.Name, "key": entry.Key, "btc": nil}
		if example {
			row["example"] = true
			row["display_name"] = "Binance"
		}
		isPending := pending[watchKeyID(entry.Key)] || chain.InitialBlockDownload
		if entry.Name == "Satoshi" && entry.Key == genesisAddress {
			row["display_name"] = "Genesis"
		}
		if info == nil {
			cache, scanState, cached := s.scanSnapshot(entry)
			if scanUnavailable && chain.Pruned && !example {
				totalReason = "scan_cache_unavailable"
			}
			if chain.Pruned || cached || scanState != "" {
				row["balance_kind"] = "confirmed"
				if descriptors, err := descriptorsFor(entry.Key); err == nil {
					for _, item := range descriptors {
						if strings.Contains(item.Desc, "/*") {
							row["range"] = map[string]int{"start": 0, "end": 999}
							break
						}
					}
				}
			}
			if cached {
				row["btc"] = formatBTC(cache.BTC)
				row["scan_height"] = cache.Height
				row["scan_time"] = cache.ScannedAt.Format(time.RFC3339)
				if series := chainHistorySeries(cache.Outpoints, cache.Height); series != nil {
					row["chain_history"] = series
				}
				if !scanUnavailable && chain.Pruned && !chain.InitialBlockDownload && chain.BestBlockHash != "" && cache.BestBlock == chain.BestBlockHash {
					row["state"] = "ok"
					if !example {
						scanCount++
						for _, output := range cache.Outpoints {
							key := output.TxID + ":" + stringValue(output.Vout)
							if previous, exists := scanOutpoints[key]; exists && previous != output.Sats {
								totalReason = "conflicting_outpoints"
							} else {
								scanOutpoints[key] = output.Sats
							}
						}
					}
				} else {
					row["state"] = "stale"
					if scanState == "scanning" {
						row["refreshing"] = true
					}
				}
			} else if scanUnavailable && chain.Pruned {
				row["state"] = "error"
			} else if scanState == "scanning" {
				row["state"] = "scanning"
			} else if isPending {
				row["state"] = "pending"
			} else {
				row["state"] = "error"
			}
		} else if truthy(info["scanning"]) {
			row["state"] = "scanning"
		} else if isPending {
			row["state"] = "pending"
		} else {
			balances := mapValue(walletRPC(name, "getbalances", []any{}, 8*time.Second))
			mine := mapValue(valueFrom(balances, "mine"))
			trusted, ok := mine["trusted"]
			if balances == nil || mine == nil || !ok {
				row["state"] = "error"
				rows = append(rows, row)
				continue
			}
			btc, valid := balanceValue(trusted)
			if !valid {
				row["state"] = "error"
				rows = append(rows, row)
				continue
			}
			if !example {
				coreTotal += btc
				coreCount++
			}
			row["state"] = "ok"
			row["btc"] = formatBTC(btc)
			if sample != nil {
				sample(entry.Key, btc)
			}
		}
		rows = append(rows, row)
	}
	if coreCount > 0 && scanCount > 0 {
		totalReason = "mixed_sources"
	}
	total := coreTotal
	if coreCount == 0 {
		for _, sats := range scanOutpoints {
			total += float64(sats) / 1e8
		}
	}
	return rows, total, totalReason
}
func mapValue(value any) map[string]any {
	result, _ := value.(map[string]any)
	return result
}

func valueFrom(values map[string]any, key string) any {
	if values == nil {
		return nil
	}
	return values[key]
}

func numberValue(value any) float64 {
	switch number := value.(type) {
	case float64:
		return number
	case float32:
		return float64(number)
	case int:
		return float64(number)
	case int64:
		return float64(number)
	case json.Number:
		result, _ := number.Float64()
		return result
	default:
		return 0
	}
}

func balanceValue(value any) (float64, bool) {
	var btc float64
	switch amount := value.(type) {
	case float64:
		btc = amount
	case float32:
		btc = float64(amount)
	case int:
		btc = float64(amount)
	case int64:
		btc = float64(amount)
	case json.Number:
		var err error
		btc, err = amount.Float64()
		if err != nil {
			return 0, false
		}
	default:
		return 0, false
	}
	return btc, !math.IsNaN(btc) && !math.IsInf(btc, 0) && btc >= 0
}

func truthy(value any) bool {
	switch typed := value.(type) {
	case nil:
		return false
	case bool:
		return typed
	case string:
		return typed != ""
	case float64:
		return typed != 0
	case int:
		return typed != 0
	case map[string]any:
		return len(typed) > 0
	case []any:
		return len(typed) > 0
	default:
		return true
	}
}
