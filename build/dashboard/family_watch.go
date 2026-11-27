package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	maxFamilyWatchWallets  = 25
	maxIndexedWatchWallets = 50
	maxFamilyWatchHistory  = 96
	familyWatchInterval    = time.Hour
	familyWatchRPCTimeout  = 10 * time.Minute
)

var errFamilyWatchUnavailable = errors.New("watch-only wallet storage unavailable")

type familyWatchEntry struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	Address   string `json:"address"`
	SourceURL string `json:"source_url,omitempty"`
	Example   bool   `json:"example,omitempty"`
}

type familyWatchHistory struct {
	Time   int64  `json:"time"`
	Amount string `json:"amount"`
}

type familyWatchBalance struct {
	Amount     string               `json:"amount"`
	ScanHeight int64                `json:"scan_height"`
	ScanTime   int64                `json:"scan_time"`
	BestBlock  string               `json:"best_block"`
	History    []familyWatchHistory `json:"history,omitempty"`
}

type familyWatchWalletView struct {
	ID         string               `json:"id"`
	Label      string               `json:"label"`
	Address    string               `json:"address"`
	SourceURL  string               `json:"source_url,omitempty"`
	Example    bool                 `json:"example"`
	State      string               `json:"state"`
	Amount     *string              `json:"amount"`
	ScanHeight int64                `json:"scan_height"`
	ScanTime   int64                `json:"scan_time"`
	History    []familyWatchHistory `json:"history"`
}

type familyWatchFile struct {
	Wallets []familyWatchEntry `json:"wallets"`
}

type familyWatchCacheFile struct {
	Balances map[string]familyWatchBalance `json:"balances"`
}

type familyWatchStore struct {
	mu           sync.RWMutex
	scanMu       sync.Mutex
	rosterPath   string
	cachePath    string
	wallets      []familyWatchEntry
	balances     map[string]familyWatchBalance
	states       map[string]string
	err          error
	trigger      chan struct{}
	now          func() time.Time
	rpc          *rpcClient
	maxSupply    uint64
	backend      familyWatchBackend
	importing    string
	imported     map[string]bool
	retryAt      map[string]int64
	importedPath string
	skipExample  bool
	logf         func(format string, args ...any)
	maxWallets   int
	index        *utxoManager
	scripts      map[string][]byte
}

type familyWatchManager struct {
	app    *app
	stores map[string]*familyWatchStore
}

func newFamilyWatchManager(a *app) *familyWatchManager {
	m := &familyWatchManager{app: a, stores: make(map[string]*familyWatchStore)}
	for _, id := range a.familyWatchNodes() {
		node := a.cfg.nodes[id]
		store := &familyWatchStore{
			rosterPath: filepath.Join(a.cfg.stateDir, "family_watch_"+id+".json"), cachePath: filepath.Join(a.cfg.stateDir, "family_watch_"+id+"_cache.json"),
			balances: make(map[string]familyWatchBalance), states: make(map[string]string), trigger: make(chan struct{}, 1), now: time.Now,
			// One batched scan lists every coin of every due wallet, well past the default 4 MiB for busy addresses.
			rpc:       &rpcClient{url: node.RPCURL, user: node.RPCUser, password: node.RPCPassword, maxBody: 64 << 20, client: newInternalHTTPClient(familyWatchRPCTimeout)},
			maxSupply: 21_000_000 * 100_000_000, backend: utxoFamilyWatchBackend(false), maxWallets: maxFamilyWatchWallets, scripts: map[string][]byte{}, logf: func(format string, args ...any) {
				if a.log != nil {
					a.log.Printf(format, args...)
				}
			},
		}
		switch id {
		case nodeLitecoin:
			store.maxSupply, store.backend, store.maxWallets = 84_000_000*100_000_000, utxoFamilyWatchBackend(true), maxIndexedWatchWallets
		case nodeBitcoinCash:
			store.maxWallets = maxIndexedWatchWallets
		case nodeDogecoin:
			store.maxSupply, store.backend, store.skipExample = dogecoinMaxSupply, dogecoinFamilyWatchBackend(), a.cfg.dogecoinWatchWallet
			if node.UTXOIndex {
				store.maxWallets = maxIndexedWatchWallets
			}
			if !a.cfg.dogecoinWatchWallet {
				store.backend = dogecoinIndexFamilyWatchBackend()
				break
			}
			store.imported, store.retryAt = make(map[string]bool), make(map[string]int64)
			store.importedPath = filepath.Join(a.cfg.stateDir, "family_watch_dogecoin_imported.json")
			store.loadImported()
		case nodeZcash:
			store.rpc.user, store.rpc.password, store.rpc.cookiePath = "", "", node.RPCCookiePath
			store.backend = zcashFamilyWatchBackend()
		}
		store.load(defaultFamilyWatchEntry(id))
		m.stores[id] = store
	}
	return m
}

func (s *familyWatchStore) load(seed familyWatchEntry) {
	if err := chmodExistingFile(s.rosterPath, 0o600); err != nil && !errors.Is(err, os.ErrNotExist) {
		s.err = errFamilyWatchUnavailable
		return
	}
	data, err := os.ReadFile(s.rosterPath)
	if errors.Is(err, os.ErrNotExist) {
		s.wallets = []familyWatchEntry{seed}
		if s.saveRosterLocked() != nil {
			s.err = errFamilyWatchUnavailable
			return
		}
	} else if err != nil || !decodeFamilyWatchRoster(data, &s.wallets, seed, s.maxWallets) {
		s.err = errFamilyWatchUnavailable
		return
	}
	if err := chmodExistingFile(s.cachePath, 0o600); err != nil && !errors.Is(err, os.ErrNotExist) {
		s.err = errFamilyWatchUnavailable
		return
	}
	data, err = os.ReadFile(s.cachePath)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	var cached familyWatchCacheFile
	if err != nil || !decodeStrictBytes(data, &cached) || cached.Balances == nil {
		s.err = errFamilyWatchUnavailable
		return
	}
	for id, balance := range cached.Balances {
		if !lowerHex(id, 24) || !validFamilyWatchBalance(balance, s.maxSupply) {
			s.err = errFamilyWatchUnavailable
			return
		}
	}
	s.balances = cached.Balances
}

func (s *familyWatchStore) Err() error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.err
}

func decodeFamilyWatchRoster(data []byte, wallets *[]familyWatchEntry, seed familyWatchEntry, maxWallets int) bool {
	var stored familyWatchFile
	if !decodeStrictBytes(data, &stored) || len(stored.Wallets) > maxWallets {
		return false
	}
	seen := make(map[string]bool, len(stored.Wallets))
	for _, wallet := range stored.Wallets {
		if !validFamilyWatchLabel(wallet.Label) || !validFamilyWatchAddress(wallet.Address) || wallet.ID != familyWatchID(wallet.Address) || seen[wallet.ID] {
			return false
		}
		if wallet.Example && wallet != seed || !wallet.Example && wallet.SourceURL != "" {
			return false
		}
		seen[wallet.ID] = true
	}
	*wallets = stored.Wallets
	return true
}

func decodeStrictReader(reader io.Reader, value any) bool {
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	return decoder.Decode(value) == nil && ensureJSONEnd(decoder) == nil
}

func decodeStrictBytes(data []byte, value any) bool {
	return decodeStrictReader(bytes.NewReader(data), value)
}

func decodeStrictBody(w http.ResponseWriter, r *http.Request, limit int64, value any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	return decodeStrictReader(r.Body, value)
}

func decodeStrictJSON(w http.ResponseWriter, r *http.Request, limit int64, value any) bool {
	if !decodeStrictBody(w, r, limit, value) {
		http.Error(w, "Invalid request.", http.StatusBadRequest)
		return false
	}
	return true
}

func (s *familyWatchStore) saveRosterLocked() error {
	data, err := json.Marshal(familyWatchFile{Wallets: s.wallets})
	if err != nil || writePrivateFileAtomic(s.rosterPath, data) != nil {
		return errFamilyWatchUnavailable
	}
	return nil
}

func (s *familyWatchStore) saveCacheLocked() error {
	data, err := json.Marshal(familyWatchCacheFile{Balances: s.balances})
	if err != nil || writePrivateFileAtomic(s.cachePath, data) != nil {
		return errFamilyWatchUnavailable
	}
	return nil
}

func (s *familyWatchStore) list(publicOnly bool) ([]familyWatchWalletView, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.err != nil {
		return nil, s.err
	}
	if publicOnly {
		for _, wallet := range s.wallets {
			if !wallet.Example {
				return nil, os.ErrPermission
			}
		}
	}
	result := make([]familyWatchWalletView, 0, len(s.wallets))
	for _, wallet := range s.wallets {
		view := familyWatchWalletView{ID: wallet.ID, Label: wallet.Label, Address: wallet.Address, SourceURL: wallet.SourceURL, Example: wallet.Example, State: s.states[wallet.ID], History: []familyWatchHistory{}}
		if view.State == "" {
			view.State = "pending"
		}
		if wallet.Example && s.skipExample {
			view.State = "unscanned"
		}
		if balance, ok := s.balances[wallet.ID]; ok {
			amount := formatFamilyWatchAmount(balance.Amount)
			view.Amount, view.ScanHeight, view.ScanTime = &amount, balance.ScanHeight, balance.ScanTime
			view.History = append(view.History, balance.History...)
			if view.State != "ok" {
				view.State = "stale"
			}
		}
		result = append(result, view)
	}
	return result, nil
}

func (s *familyWatchStore) add(label, address string) (familyWatchEntry, error) {
	label, address = strings.TrimSpace(label), strings.TrimSpace(address)
	if !validFamilyWatchLabel(label) {
		return familyWatchEntry{}, errors.New("Label must be 1 to 80 printable characters.")
	}
	if !validFamilyWatchAddress(address) {
		return familyWatchEntry{}, errors.New("A valid public address is required.")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return familyWatchEntry{}, s.err
	}
	if len(s.wallets) >= s.maxWallets {
		return familyWatchEntry{}, fmt.Errorf("At most %d addresses can be monitored.", s.maxWallets)
	}
	id := familyWatchID(address)
	for _, wallet := range s.wallets {
		if wallet.ID == id || strings.EqualFold(wallet.Label, label) {
			return familyWatchEntry{}, errors.New("That address or label is already configured.")
		}
	}
	entry := familyWatchEntry{ID: id, Label: label, Address: address}
	s.wallets = append(s.wallets, entry)
	if err := s.saveRosterLocked(); err != nil {
		s.wallets = s.wallets[:len(s.wallets)-1]
		s.err = err
		return familyWatchEntry{}, err
	}
	s.states[id] = "pending"
	return entry, nil
}

func (s *familyWatchStore) remove(id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return false, s.err
	}
	for i := range s.wallets {
		if s.wallets[i].ID != id {
			continue
		}
		previous := append([]familyWatchEntry(nil), s.wallets...)
		balance, hadBalance := s.balances[id]
		state, hadState := s.states[id]
		s.wallets = append(s.wallets[:i], s.wallets[i+1:]...)
		delete(s.balances, id)
		delete(s.states, id)
		if err := s.saveCacheLocked(); err != nil {
			s.restoreRemovedLocked(previous, id, balance, hadBalance, state, hadState)
			s.err = err
			return false, err
		}
		if err := s.saveRosterLocked(); err != nil {
			s.restoreRemovedLocked(previous, id, balance, hadBalance, state, hadState)
			_ = s.saveCacheLocked()
			s.err = err
			return false, err
		}
		return true, nil
	}
	return false, nil
}

func (s *familyWatchStore) restoreRemovedLocked(wallets []familyWatchEntry, id string, balance familyWatchBalance, hadBalance bool, state string, hadState bool) {
	s.wallets = wallets
	if hadBalance {
		s.balances[id] = balance
	}
	if hadState {
		s.states[id] = state
	}
}
