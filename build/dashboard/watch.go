package main

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const maxWatchWallets = 50
const genesisAddress = "1A1zP1eP5QGefi2DMPTfTL5SLmv7DivfNa"
const publicExampleName = "Binance (public)"
const publicExampleAddress = "34xp4vRoCGJym3xR7yCVPFHoCNxv4Twseo"

var ErrWatchUnavailable = errors.New("watch wallet storage unavailable")
var ErrScanCacheUnavailable = errors.New("watch scan cache unavailable")
var chmodExistingFile = os.Chmod
var writeWatchFile = writePrivateFileAtomic

type WatchEntry struct {
	Name     string `json:"name"`
	Key      string `json:"key"`
	Birthday string `json:"birthday"`
}

type WatchStore struct {
	mu           sync.Mutex
	path         string
	scanPath     string
	entries      []WatchEntry
	history      *balanceHistory
	pending      map[string]bool
	coreNames    map[string]string
	scans        map[string]prunedScanCache
	scanStates   map[string]string
	unsafeRoster bool
	storeErr     error
	scanErr      error
}

func NewWatchStore(stateDir string) *WatchStore {
	if stateDir == "" {
		stateDir = "/state"
	}
	storePath := os.Getenv("WATCH_STORE")
	if storePath == "" {
		storePath = filepath.Join(stateDir, "wallets.json")
	}
	historyPath := os.Getenv("BALANCE_HISTORY")
	if historyPath == "" {
		historyPath = filepath.Join(stateDir, "balance_history.json")
	}
	scanPath := filepath.Join(stateDir, "watch_scan_cache.json")
	secureParent(storePath)
	store := &WatchStore{path: storePath, scanPath: scanPath, history: newBalanceHistory(historyPath)}
	store.load()
	store.loadScanCache()
	return store
}

func (s *WatchStore) load() {
	if err := chmodExistingFile(s.path, 0o600); err != nil && !os.IsNotExist(err) {
		s.entries = nil
		s.unsafeRoster = true
		s.storeErr = ErrWatchUnavailable
		return
	}
	b, err := os.ReadFile(s.path)
	if err == nil {
		var items []map[string]any
		if json.Unmarshal(b, &items) != nil || items == nil {
			s.entries = nil
			s.storeErr = ErrWatchUnavailable
			return
		}
		s.entries, s.unsafeRoster = normalizeEntries(items)
		return
	}
	if !os.IsNotExist(err) {
		s.entries = nil
		s.storeErr = ErrWatchUnavailable
		return
	}
	entries, err := loadWatchConfig()
	if err != nil {
		s.entries = nil
		s.storeErr = ErrWatchUnavailable
		return
	}
	s.entries = entries
	if s.saveLocked() != nil {
		s.storeErr = ErrWatchUnavailable
	}
}

func loadWatchConfig() ([]WatchEntry, error) {
	raw := os.Getenv("WATCH_WALLETS_B64")
	if raw == "" {
		return []WatchEntry{}, nil
	}
	b, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil, ErrWatchUnavailable
	}
	var items []map[string]any
	if json.Unmarshal(b, &items) != nil || items == nil {
		return nil, ErrWatchUnavailable
	}
	for _, item := range items {
		key := strings.TrimSpace(stringValue(item["key"]))
		if key != "" && !hasPrivateMaterial(key) {
			if _, err := descriptorsFor(key); err != nil {
				return nil, ErrWatchUnavailable
			}
		}
	}
	entries, _ := normalizeEntries(items)
	return entries, nil
}

func isPublicExample(entry WatchEntry) bool {
	return entry.Name == publicExampleName && entry.Key == publicExampleAddress && entry.Birthday == ""
}

func normalizeEntries(items []map[string]any) ([]WatchEntry, bool) {
	entries := make([]WatchEntry, 0, len(items))
	privateFound := false
	seenKeys := map[string]bool{}
	for _, item := range items {
		name := strings.TrimSpace(stringValue(item["name"]))
		key := strings.TrimSpace(stringValue(item["key"]))
		if hasPrivateMaterial(key) {
			privateFound = true
			continue
		}
		if key != "" {
			if _, err := descriptorsFor(key); err != nil {
				privateFound = true
				continue
			}
		}
		identity := watchKeyID(key)
		if name != "" && key != "" && !seenKeys[identity] {
			entries = append(entries, WatchEntry{Name: name, Key: key, Birthday: stringValue(item["birthday"])})
			seenKeys[identity] = true
		}
	}
	return entries, privateFound
}

func stringValue(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	return fmt.Sprint(value)
}

func (s *WatchStore) saveLocked() error {
	b, err := json.Marshal(s.entries)
	if err != nil {
		return ErrWatchUnavailable
	}
	if writeWatchFile(s.path, b) != nil {
		return ErrWatchUnavailable
	}
	return nil
}

func (s *WatchStore) ScanErr() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.scanErr
}

func secureParent(path string) bool {
	dir := filepath.Dir(path)
	if dir == "." || dir == string(filepath.Separator) {
		return true
	}
	if os.MkdirAll(dir, 0o700) != nil {
		return false
	}
	return os.Chmod(dir, 0o700) == nil
}

func writePrivateFileAtomic(path string, data []byte) error {
	if !secureParent(path) {
		return ErrWatchUnavailable
	}
	tmp := path + ".tmp"
	file, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return ErrWatchUnavailable
	}
	if file.Chmod(0o600) != nil {
		_ = file.Close()
		_ = os.Remove(tmp)
		return ErrWatchUnavailable
	}
	if _, err = file.Write(data); err != nil {
		_ = file.Close()
		_ = os.Remove(tmp)
		return ErrWatchUnavailable
	}
	if file.Close() != nil {
		_ = os.Remove(tmp)
		return ErrWatchUnavailable
	}
	if os.Rename(tmp, path) != nil {
		_ = os.Remove(tmp)
		return ErrWatchUnavailable
	}
	return nil
}

func (s *WatchStore) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.storeErr
}

func (s *WatchStore) Entries() []WatchEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]WatchEntry(nil), s.entries...)
}

func (s *WatchStore) Contains(entry WatchEntry) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, current := range s.entries {
		if current == entry {
			return true
		}
	}
	return false
}

func watchKeyID(key string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(key)))
	return hex.EncodeToString(sum[:])
}

func coreWalletName(entry WatchEntry) string {
	return "watch_" + watchKeyID(entry.Key)
}

func (s *WatchStore) SetPending(key string, pending bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	identity := watchKeyID(key)
	if pending {
		if s.pending == nil {
			s.pending = map[string]bool{}
		}
		s.pending[identity] = true
		return
	}
	delete(s.pending, identity)
}

func (s *WatchStore) ClearPendingIfCurrent(entry WatchEntry) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, current := range s.entries {
		if current == entry {
			delete(s.pending, watchKeyID(entry.Key))
			return true
		}
	}
	return false
}

func (s *WatchStore) SetCoreWallet(key, name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	identity := watchKeyID(key)
	if name == "" {
		delete(s.coreNames, identity)
		return
	}
	if s.coreNames == nil {
		s.coreNames = map[string]string{}
	}
	s.coreNames[identity] = name
}

func (s *WatchStore) SetCoreWalletIfCurrent(entry WatchEntry, name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	current := false
	for _, item := range s.entries {
		if item == entry {
			current = true
			break
		}
	}
	if !current {
		return false
	}
	if s.coreNames == nil {
		s.coreNames = map[string]string{}
	}
	s.coreNames[watchKeyID(entry.Key)] = name
	return true
}

func (s *WatchStore) snapshot() ([]WatchEntry, map[string]bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries := append([]WatchEntry(nil), s.entries...)
	pending := make(map[string]bool, len(s.pending))
	for identity := range s.pending {
		pending[identity] = true
	}
	return entries, pending
}

func (s *WatchStore) Add(name, key, birthday string) (WatchEntry, error) {
	name, key, birthday = strings.TrimSpace(name), strings.TrimSpace(key), strings.TrimSpace(birthday)
	if name == "" || key == "" {
		return WatchEntry{}, errors.New("Name and key are both required.")
	}
	if birthday != "" && birthdayTimestamp(birthday) == 0 {
		return WatchEntry{}, errors.New("Birthday must be a date like 2021-03-15.")
	}
	if birthday > time.Now().UTC().Format("2006-01-02") {
		return WatchEntry{}, errors.New("Birthday cannot be in the future.")
	}
	if _, err := descriptorsFor(key); err != nil {
		return WatchEntry{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.storeErr != nil {
		return WatchEntry{}, ErrWatchUnavailable
	}
	if s.unsafeRoster {
		return WatchEntry{}, errors.New("wallet roster contains private key material; remove it manually before making changes")
	}
	if len(s.entries) >= maxWatchWallets {
		return WatchEntry{}, fmt.Errorf("At most %d wallets.", maxWatchWallets)
	}
	for _, entry := range s.entries {
		if strings.EqualFold(entry.Name, name) {
			return WatchEntry{}, errors.New("A wallet with that name already exists.")
		}
		if watchKeyID(entry.Key) == watchKeyID(key) {
			return WatchEntry{}, errors.New("That watch key is already configured.")
		}
		if walletName(entry.Name) == walletName(name) {
			return WatchEntry{}, errors.New("That wallet name conflicts with an existing wallet.")
		}
	}
	entry := WatchEntry{Name: name, Key: key, Birthday: birthday}
	previous := s.entries
	s.entries = append(s.entries, entry)
	if s.saveLocked() != nil {
		s.entries = previous
		s.storeErr = ErrWatchUnavailable
		return WatchEntry{}, ErrWatchUnavailable
	}
	return entry, nil
}

func (s *WatchStore) Remove(name string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.storeErr != nil {
		return false, ErrWatchUnavailable
	}
	if s.unsafeRoster {
		return false, ErrWatchUnavailable
	}
	for index, entry := range s.entries {
		if entry.Name == name {
			return s.removeIndexLocked(index, entry)
		}
	}
	return false, nil
}

func (s *WatchStore) RemoveExact(entry WatchEntry) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.storeErr != nil {
		return false, ErrWatchUnavailable
	}
	if s.unsafeRoster {
		return false, ErrWatchUnavailable
	}
	for index, current := range s.entries {
		if current == entry {
			return s.removeIndexLocked(index, current)
		}
	}
	return false, nil
}

func (s *WatchStore) removeIndexLocked(index int, entry WatchEntry) (bool, error) {
	previous := s.entries
	next := make([]WatchEntry, 0, len(s.entries)-1)
	next = append(next, s.entries[:index]...)
	next = append(next, s.entries[index+1:]...)
	s.entries = next
	if s.saveLocked() != nil {
		s.entries = previous
		s.storeErr = ErrWatchUnavailable
		return false, ErrWatchUnavailable
	}
	identity := watchKeyID(entry.Key)
	delete(s.pending, identity)
	delete(s.coreNames, identity)
	delete(s.scans, identity)
	delete(s.scanStates, identity)
	_ = s.saveScanCacheLocked()
	return true, nil
}
