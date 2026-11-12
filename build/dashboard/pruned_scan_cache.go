package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

type scanOutpoint struct {
	TxID   string `json:"txid"`
	Vout   int64  `json:"vout"`
	Sats   int64  `json:"sats"`
	Height int64  `json:"height,omitempty"`
}

type prunedScanCache struct {
	DescriptorID string         `json:"descriptor_id"`
	BTC          float64        `json:"btc"`
	Height       int64          `json:"height"`
	BestBlock    string         `json:"best_block"`
	ScannedAt    time.Time      `json:"scanned_at"`
	Outpoints    []scanOutpoint `json:"outpoints"`
}

type entryScanCache struct {
	entry WatchEntry
	cache prunedScanCache
}

var chmodScanFile = os.Chmod
var readScanFile = os.ReadFile
var writeScanFile = writePrivateFileAtomic

func descriptorSetID(entry WatchEntry) string {
	descriptors, err := descriptorsFor(entry.Key)
	if err != nil {
		return ""
	}
	identities := make([]string, 0, len(descriptors))
	for _, item := range descriptors {
		identities = append(identities, descriptorIdentity(item))
	}
	sort.Strings(identities)
	sum := sha256.Sum256([]byte(strings.Join(identities, "\n")))
	return hex.EncodeToString(sum[:])
}

func (s *WatchStore) loadScanCache() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.scans = map[string]prunedScanCache{}
	s.scanStates = map[string]string{}
	if err := chmodScanFile(s.scanPath, 0o600); err != nil && !os.IsNotExist(err) {
		s.scanErr = ErrScanCacheUnavailable
		return
	}
	b, err := readScanFile(s.scanPath)
	if err != nil {
		if !os.IsNotExist(err) {
			s.scanErr = ErrScanCacheUnavailable
		}
		return
	}
	var scans map[string]prunedScanCache
	if json.Unmarshal(b, &scans) != nil || scans == nil {
		s.scanErr = ErrScanCacheUnavailable
		return
	}
	for identity, cache := range scans {
		if validPrunedScanCache(cache) {
			s.scans[identity] = cache
		} else {
			s.scanErr = ErrScanCacheUnavailable
		}
	}
}

func validPrunedScanCache(cache prunedScanCache) bool {
	if cache.DescriptorID == "" || cache.Height < 0 || cache.ScannedAt.IsZero() || len(cache.BestBlock) != 64 || cache.Outpoints == nil {
		return false
	}
	if _, err := hex.DecodeString(cache.BestBlock); err != nil {
		return false
	}
	btcSats, ok := btcSatoshis(cache.BTC)
	if !ok {
		return false
	}
	seen := map[string]int64{}
	var sats int64
	const maxSatoshis = int64(21_000_000 * 1e8)
	for _, output := range cache.Outpoints {
		if len(output.TxID) != 64 || output.Vout < 0 || output.Sats < 0 || output.Height < 0 || output.Height > cache.Height {
			return false
		}
		if _, err := hex.DecodeString(output.TxID); err != nil {
			return false
		}
		key := fmt.Sprintf("%s:%d", output.TxID, output.Vout)
		if previous, duplicate := seen[key]; duplicate {
			if previous != output.Sats {
				return false
			}
			continue
		}
		seen[key] = output.Sats
		if output.Sats > maxSatoshis-sats {
			return false
		}
		sats += output.Sats
	}
	return sats == btcSats
}

func (s *WatchStore) saveScanCacheLocked() error {
	b, err := json.Marshal(s.scans)
	if err != nil {
		return err
	}
	if err := writeScanFile(s.scanPath, b); err != nil {
		s.scanErr = ErrScanCacheUnavailable
		return err
	}
	return nil
}

func (s *WatchStore) scanSnapshot(entry WatchEntry) (prunedScanCache, string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	identity := watchKeyID(entry.Key)
	cache, ok := s.scans[identity]
	if ok && cache.DescriptorID != descriptorSetID(entry) {
		ok = false
	}
	return cache, s.scanStates[identity], ok
}

func (s *WatchStore) SetScanStateIfCurrent(entry WatchEntry, state string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.containsLocked(entry) {
		return false
	}
	if s.scanStates == nil {
		s.scanStates = map[string]string{}
	}
	s.scanStates[watchKeyID(entry.Key)] = state
	return true
}

func (s *WatchStore) StoreScanBatch(caches map[string]entryScanCache) []entryScanCache {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.scanErr != nil {
		return nil
	}
	for identity, item := range caches {
		if identity != watchKeyID(item.entry.Key) || !s.containsLocked(item.entry) || item.cache.DescriptorID != descriptorSetID(item.entry) || !validPrunedScanCache(item.cache) {
			return nil
		}
	}
	previousScans := s.scans
	previousStates := s.scanStates
	previousPending := s.pending
	s.scans = cloneScanCaches(s.scans)
	s.scanStates = cloneStringMap(s.scanStates)
	s.pending = cloneBoolMap(s.pending)
	stored := make([]entryScanCache, 0, len(caches))
	for identity, item := range caches {
		s.scans[identity] = item.cache
		s.scanStates[identity] = "ok"
		delete(s.pending, identity)
		stored = append(stored, item)
	}
	if s.saveScanCacheLocked() != nil {
		s.scans = previousScans
		s.scanStates = previousStates
		s.pending = previousPending
		return nil
	}
	return stored
}

func cloneScanCaches(source map[string]prunedScanCache) map[string]prunedScanCache {
	result := make(map[string]prunedScanCache, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func cloneStringMap(source map[string]string) map[string]string {
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func cloneBoolMap(source map[string]bool) map[string]bool {
	result := make(map[string]bool, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func (s *WatchStore) StoreScanIfCurrent(entry WatchEntry, cache prunedScanCache) bool {
	return len(s.StoreScanBatch(map[string]entryScanCache{watchKeyID(entry.Key): {entry: entry, cache: cache}})) == 1
}

func (s *WatchStore) containsLocked(entry WatchEntry) bool {
	for _, current := range s.entries {
		if current == entry {
			return true
		}
	}
	return false
}
