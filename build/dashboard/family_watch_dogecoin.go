package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"sort"
	"strconv"
	"time"
)

const (
	dogecoinMaxSupply     = 180_000_000_000 * 100_000_000
	dogecoinRescanTimeout = 72 * time.Hour
	dogecoinImportRetry   = 6 * time.Hour
)

func dogecoinFamilyWatchBackend() familyWatchBackend {
	base := utxoFamilyWatchBackend(false)
	return familyWatchBackend{
		ready: func(ctx context.Context, s *familyWatchStore, now int64) (familyWatchTip, bool) {
			if s.rescanActive() {
				return familyWatchTip{}, false
			}
			return base.ready(ctx, s, now)
		},
		scan: func(ctx context.Context, s *familyWatchStore, address string, tip familyWatchTip) (familyWatchResult, string) {
			if s.rescanActive() {
				return familyWatchResult{}, "rescanning"
			}
			var known struct {
				IsValid bool `json:"isvalid"`
			}
			if s.rpc.call(ctx, "validateaddress", []any{address}, &known) != nil || !known.IsValid {
				return familyWatchResult{}, "failed"
			}
			if !s.isImported(address) {
				if s.retryPending(address) {
					return familyWatchResult{}, "failed"
				}
				s.startRescan(address)
				return familyWatchResult{}, "rescanning"
			}
			var unspent []struct {
				Amount json.RawMessage `json:"amount"`
			}
			if s.rpc.call(ctx, "listunspent", []any{1, 9999999, []string{address}}, &unspent) != nil {
				return familyWatchResult{}, "failed"
			}
			var total uint64
			for _, output := range unspent {
				atomic, err := parseFamilyWatchAmount(output.Amount, s.maxSupply)
				if err != nil {
					return familyWatchResult{}, "failed"
				}
				value, _ := strconv.ParseUint(atomic, 10, 64)
				if value > s.maxSupply-total {
					return familyWatchResult{}, "failed"
				}
				total += value
			}
			return familyWatchResult{strconv.FormatUint(total, 10), tip.Blocks, tip.Best}, "ok"
		},
		validate: func(ctx context.Context, s *familyWatchStore, address string) (string, error) {
			if s.rescanActive() {
				return "", errors.New("Dogecoin is rescanning for another address; add this one when that finishes.")
			}
			var validated struct {
				IsValid bool   `json:"isvalid"`
				Address string `json:"address"`
			}
			if err := s.rpc.call(ctx, "validateaddress", []any{address}, &validated); err != nil || !validated.IsValid || !validFamilyWatchAddress(validated.Address) {
				return "", errors.New("A valid public address for this node is required.")
			}
			return validated.Address, nil
		},
	}
}

func (s *familyWatchStore) rescanActive() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.importing != ""
}

func (s *familyWatchStore) startRescan(address string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.importing != "" {
		return
	}
	s.importing = address
	go s.importAddress(address)
}

func (s *familyWatchStore) importAddress(address string) {
	long := *s.rpc
	long.client = &http.Client{Timeout: dogecoinRescanTimeout}
	ctx, cancel := context.WithTimeout(context.Background(), dogecoinRescanTimeout)
	defer cancel()
	err := long.call(ctx, "importaddress", []any{address, "", true}, new(json.RawMessage))
	var empty rpcNoResultError
	if errors.As(err, &empty) {
		err = nil
	}
	s.mu.Lock()
	s.importing = ""
	s.mu.Unlock()
	id := familyWatchID(address)
	if err != nil {
		s.mu.Lock()
		s.retryAt[id] = s.now().Add(dogecoinImportRetry).Unix()
		s.mu.Unlock()
		s.markFailed(id)
		s.logf("dogecoin watch import failed, retrying after %s: %v", dogecoinImportRetry, err)
		return
	}
	s.mu.Lock()
	s.imported[id] = true
	delete(s.retryAt, id)
	saved := s.saveImportedLocked()
	s.mu.Unlock()
	if saved != nil {
		s.logf("dogecoin watch import marker not saved: %v", saved)
	}
	s.requestScan()
}

func (s *familyWatchStore) isImported(address string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.imported[familyWatchID(address)]
}

func (s *familyWatchStore) retryPending(address string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.retryAt[familyWatchID(address)] > s.now().Unix()
}

func (s *familyWatchStore) loadImported() {
	data, err := os.ReadFile(s.importedPath)
	if err != nil {
		return
	}
	var stored struct {
		Imported []string `json:"imported"`
	}
	if !decodeStrictBytes(data, &stored) {
		return
	}
	for _, id := range stored.Imported {
		if validFamilyWatchID(id) {
			s.imported[id] = true
		}
	}
}

func (s *familyWatchStore) saveImportedLocked() error {
	ids := make([]string, 0, len(s.imported))
	for id := range s.imported {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	data, err := json.Marshal(map[string][]string{"imported": ids})
	if err != nil {
		return err
	}
	return writePrivateFileAtomic(s.importedPath, data)
}
