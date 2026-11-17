package main

import (
	"errors"
	"os"
	"path/filepath"
)

func removeFiles(paths ...string) error {
	var failed error
	for _, path := range paths {
		if path == "" {
			continue
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			failed = err
		}
	}
	return failed
}

func (a *app) nodeDeleteMarkerPath(node string) string {
	return filepath.Join(a.cfg.stateDir, "node-delete."+node)
}

func (s *WatchStore) purge() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries, s.unsafeRoster, s.storeErr, s.scanErr = nil, false, nil, nil
	s.pending, s.coreNames, s.scans, s.scanStates = map[string]bool{}, map[string]string{}, map[string]prunedScanCache{}, map[string]string{}
	s.history.mu.Lock()
	s.history.data, s.history.loaded = map[string][][]float64{}, true
	s.history.mu.Unlock()
	return removeFiles(s.path, s.scanPath, s.history.path)
}

func (s *familyWatchStore) purge() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.wallets, s.balances, s.states, s.err = nil, map[string]familyWatchBalance{}, map[string]string{}, nil
	if s.imported != nil {
		s.imported, s.retryAt, s.importing = map[string]bool{}, map[string]int64{}, ""
	}
	return removeFiles(s.rosterPath, s.cachePath, s.importedPath)
}

func (s *MoneroWatchStore) purge() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.wallets, s.err, s.rpcPoisoned = nil, nil, false
	return removeFiles(s.path)
}

func (s *TariWatchStore) purge() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.wallet, s.err = nil, nil
	return removeFiles(s.path)
}

func (a *app) purgeNodeWallets(node string) error {
	switch node {
	case nodeBitcoin:
		return a.watch.purge()
	case nodeMonero:
		return a.moneroWatch.purge()
	case nodeTari:
		return a.tariWatch.purge()
	}
	if store := a.familyWatch.stores[node]; store != nil {
		return store.purge()
	}
	return removeFiles(filepath.Join(a.cfg.stateDir, "family_watch_"+node+".json"), filepath.Join(a.cfg.stateDir, "family_watch_"+node+"_cache.json"), filepath.Join(a.cfg.stateDir, "family_watch_"+node+"_imported.json"))
}

func (a *app) processNodeDeletions() {
	if a.cfg.stateDir == "" {
		return
	}
	for _, node := range nodeOrder {
		path := a.nodeDeleteMarkerPath(node)
		if _, err := os.Lstat(path); err != nil {
			continue
		}
		if a.purgeNodeWallets(node) == nil {
			_ = os.Remove(path)
		} else if a.log != nil {
			a.log.Printf("%s watch-only wallets could not be fully removed; retrying", nodeDisplayName(node))
		}
	}
}
