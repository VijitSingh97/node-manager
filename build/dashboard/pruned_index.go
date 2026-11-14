package main

import (
	"cmp"
	"context"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

const (
	scanSourceScan  = "scan"
	scanSourceIndex = "index"
	indexQueryLimit = 2 * time.Minute
)

type indexScriptRecord struct {
	DescriptorID string   `json:"descriptor_id"`
	Fallback     bool     `json:"fallback,omitempty"`
	Scripts      []string `json:"scripts,omitempty"`
}

func (s *prunedScanner) scriptsPath() string {
	return filepath.Join(filepath.Dir(s.app.watch.scanPath), "utxo_scripts.json")
}

func (s *prunedScanner) indexScripts(ctx context.Context, entry WatchEntry) [][]byte {
	identity, descriptorID := watchKeyID(entry.Key), descriptorSetID(entry)
	if descriptorID == "" {
		return nil
	}
	s.scriptMu.Lock()
	defer s.scriptMu.Unlock()
	if s.scripts == nil {
		s.scripts = map[string]indexScriptRecord{}
		if b, err := os.ReadFile(s.scriptsPath()); err == nil {
			_ = json.Unmarshal(b, &s.scripts)
		}
	}
	record, ok := s.scripts[identity]
	if !ok || record.DescriptorID != descriptorID {
		resolved, err := s.resolveScripts(ctx, []string{entry.Key})
		if err != nil || len(resolved) != 1 {
			return nil
		}
		record = indexScriptRecord{DescriptorID: descriptorID, Fallback: resolved[0].Fallback}
		for _, script := range resolved[0].Scripts {
			record.Scripts = append(record.Scripts, hex.EncodeToString(script))
		}
		s.scripts[identity] = record
		current := map[string]bool{}
		for _, item := range s.app.watch.Entries() {
			current[watchKeyID(item.Key)] = true
		}
		for key := range s.scripts {
			if !current[key] {
				delete(s.scripts, key)
			}
		}
		if b, err := json.Marshal(s.scripts); err != nil || writePrivateFileAtomic(s.scriptsPath(), b) != nil {
			s.app.log.Print("watch index: script registry not saved")
		}
	}
	if record.Fallback {
		return nil
	}
	scripts := make([][]byte, 0, len(record.Scripts))
	for _, encoded := range record.Scripts {
		script, err := hex.DecodeString(encoded)
		if err != nil {
			return nil
		}
		scripts = append(scripts, script)
	}
	return scripts
}

// True means the index owns this refresh and no scan is needed; false falls back to scantxoutset.
func (s *prunedScanner) fromIndex(entry WatchEntry) bool {
	if s.app.utxo.Status().State != utxoStateReady {
		return false
	}
	started := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), indexQueryLimit)
	defer cancel()
	scripts := s.indexScripts(ctx, entry)
	if len(scripts) == 0 {
		return false
	}
	coins, height, hash, ok := s.app.utxo.Lookup(scripts)
	if !ok {
		return false
	}
	best := reverseHex(hash)
	if cache, _, cached := s.app.watch.scanSnapshot(entry); cached && cache.Source == scanSourceIndex && cache.BestBlock == best {
		return true
	}
	outpoints := make([]scanOutpoint, 0, len(coins))
	var sats int64
	for _, coin := range coins {
		amount := int64(coin.Amount)
		if amount < 0 {
			return false
		}
		outpoints = append(outpoints, scanOutpoint{TxID: reverseHex(coin.Txid), Vout: int64(coin.Vout), Sats: amount, Height: int64(coin.Height)})
		sats += amount
	}
	slices.SortFunc(outpoints, func(a, b scanOutpoint) int {
		return cmp.Or(strings.Compare(a.TxID, b.TxID), cmp.Compare(a.Vout, b.Vout))
	})
	now := s.now().UTC()
	identity := watchKeyID(entry.Key)
	stored := s.app.watch.StoreScanBatch(map[string]entryScanCache{identity: {entry: entry, cache: prunedScanCache{
		DescriptorID: descriptorSetID(entry), BTC: float64(sats) / 1e8, Height: int64(height), BestBlock: best,
		ScannedAt: now, Outpoints: outpoints, Source: scanSourceIndex,
	}}})
	if len(stored) != 1 {
		s.app.log.Print("watch index: result rejected by the balance cache; falling back to scan")
		return false
	}
	s.app.watch.history.record(entry.Key, stored[0].cache.BTC, now)
	s.succeeded(identity)
	s.app.log.Printf("watch index: outcome=ok outpoints=%d height=%d took=%.3fs", len(outpoints), height, time.Since(started).Seconds())
	return true
}

func reverseHex[T ~[32]byte](b T) string {
	raw := [32]byte(b)
	slices.Reverse(raw[:])
	return hex.EncodeToString(raw[:])
}
