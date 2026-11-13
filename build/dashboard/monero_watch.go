package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	maxMoneroWatchWallets = 8
	maxMoneroWatchHistory = 288
	moneroRefreshTimeout  = 30 * time.Minute
)

var (
	errMoneroWatchUnavailable = errors.New("Monero watch-only wallet storage unavailable")
	errMoneroScanPending      = errors.New("Monero watch-only wallet scan pending")
	moneroNamePattern         = regexp.MustCompile(`^[[:print:]]{1,40}$`)
	moneroAddressPattern      = regexp.MustCompile(`^4[123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz]{94}$`)
	hex64Pattern              = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)
	hex128Pattern             = regexp.MustCompile(`^[0-9a-fA-F]{128}$`)
)

type MoneroWatchHistory struct {
	Timestamp    int64  `json:"timestamp"`
	AmountAtomic string `json:"amount_atomic"`
	Verified     bool   `json:"verified"`
}

type MoneroKeyImageStatus struct {
	Present        bool  `json:"present"`
	CoveredOutputs int   `json:"covered_outputs"`
	TotalOutputs   int   `json:"total_outputs"`
	Complete       bool  `json:"complete"`
	UpdatedAt      int64 `json:"updated_at"`
}

type MoneroWatchWallet struct {
	ID                    string               `json:"id"`
	Name                  string               `json:"name"`
	Address               string               `json:"address"`
	Example               bool                 `json:"example"`
	SourceURL             string               `json:"source_url,omitempty"`
	RestoreHeight         uint64               `json:"restore_height"`
	State                 string               `json:"state"`
	ReceivedAtomic        string               `json:"received_atomic"`
	ReceivedXMR           string               `json:"received_xmr"`
	BalanceAtomic         string               `json:"balance_atomic"`
	BalanceXMR            string               `json:"balance_xmr"`
	UnlockedBalanceAtomic string               `json:"unlocked_balance_atomic"`
	ScanHeight            uint64               `json:"scan_height"`
	TargetHeight          uint64               `json:"target_height"`
	SpendingStatus        string               `json:"spending_status"`
	KeyImages             MoneroKeyImageStatus `json:"key_images"`
	UpdatedAt             int64                `json:"updated_at"`
	Error                 string               `json:"error"`
	History               []MoneroWatchHistory `json:"history"`
	WalletFile            string               `json:"-"`
}

type moneroWatchFile struct {
	Wallets []moneroStoredWallet `json:"wallets"`
}

type moneroStoredWallet struct {
	MoneroWatchWallet
	WalletFile string `json:"wallet_file"`
}

type MoneroWatchStore struct {
	mu                   sync.RWMutex
	path                 string
	wallets              []MoneroWatchWallet
	err                  error
	rpcPoisoned          bool
	enabled              bool
	password             string
	rpc                  moneroRPC
	daemon               moneroRPC
	gate                 chan struct{}
	queue                chan string
	now                  func() time.Time
	publicExamplePending bool
}

type moneroSignedKeyImage struct {
	KeyImage  string `json:"key_image"`
	Signature string `json:"signature"`
}

type moneroKeyImageUpload struct {
	Offset          uint64                 `json:"offset"`
	SignedKeyImages []moneroSignedKeyImage `json:"signed_key_images"`
}

func NewMoneroWatchStore(cfg config) *MoneroWatchStore {
	s := &MoneroWatchStore{
		path: filepath.Join(cfg.stateDir, "monero_watch.json"), enabled: cfg.moneroWalletEnabled,
		password: cfg.moneroWalletPassword, gate: make(chan struct{}, 1), queue: make(chan string, maxMoneroWatchWallets), now: time.Now,
		rpc:    moneroRPC{url: cfg.moneroWalletRPCURL, user: cfg.moneroWalletRPCUser, password: cfg.moneroWalletRPCPassword, client: &http.Client{Timeout: moneroRefreshTimeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}},
		daemon: moneroRPC{url: cfg.moneroRPCURL, user: cfg.moneroRPCUser, password: cfg.moneroRPCPassword, client: &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}},
	}
	s.gate <- struct{}{}
	if err := chmodExistingFile(s.path, 0600); err != nil && !errors.Is(err, os.ErrNotExist) {
		s.err = errMoneroWatchUnavailable
		return s
	}
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		s.publicExamplePending = cfg.moneroPublicExample
		return s
	}
	if err != nil {
		s.err = errMoneroWatchUnavailable
		return s
	}
	var stored moneroWatchFile
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&stored) != nil || ensureJSONEnd(decoder) != nil || len(stored.Wallets) > maxMoneroWatchWallets {
		s.err = errMoneroWatchUnavailable
		return s
	}
	for i := range stored.Wallets {
		record := &stored.Wallets[i]
		wallet := &record.MoneroWatchWallet
		if wallet.ID != moneroWalletID(wallet.Address) || wallet.WalletFile != "" || !validManagedWalletFilename(wallet.ID, record.WalletFile) || !validStoredMoneroWallet(*wallet) {
			s.err = errMoneroWatchUnavailable
			return s
		}
		wallet.WalletFile = record.WalletFile
		wallet.State = "pending"
		wallet.Error = ""
		wallet.KeyImages.Complete = false
		wallet.BalanceAtomic, wallet.BalanceXMR, wallet.UnlockedBalanceAtomic = "", "", ""
		if wallet.KeyImages.Present {
			wallet.SpendingStatus = "stale"
		} else {
			wallet.SpendingStatus = "unknown"
		}
	}
	s.wallets = make([]MoneroWatchWallet, len(stored.Wallets))
	for i := range stored.Wallets {
		s.wallets[i] = stored.Wallets[i].MoneroWatchWallet
	}
	return s
}

func (s *MoneroWatchStore) Err() error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.err
}

func (s *MoneroWatchStore) List() []MoneroWatchWallet {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]MoneroWatchWallet, len(s.wallets))
	for i := range s.wallets {
		result[i] = s.wallets[i]
		result[i].History = append([]MoneroWatchHistory(nil), s.wallets[i].History...)
		result[i].WalletFile = ""
	}
	return result
}

func (s *MoneroWatchStore) Add(ctx context.Context, name, address, viewKey string, restoreHeight uint64) (MoneroWatchWallet, error) {
	return s.add(ctx, name, address, viewKey, restoreHeight, false, "")
}

func (s *MoneroWatchStore) add(ctx context.Context, name, address, viewKey string, restoreHeight uint64, example bool, sourceURL string) (MoneroWatchWallet, error) {
	name, address, viewKey = strings.TrimSpace(name), strings.TrimSpace(address), strings.TrimSpace(viewKey)
	if !validMoneroName(name) {
		return MoneroWatchWallet{}, errors.New("Name must be 1 to 40 printable characters.")
	}
	if !validMoneroAddress(address) {
		return MoneroWatchWallet{}, errors.New("A valid mainnet primary Monero address is required.")
	}
	if !hex64Pattern.MatchString(viewKey) || strings.Trim(viewKey, "0") == "" {
		return MoneroWatchWallet{}, errors.New("A valid private view key is required.")
	}
	if restoreHeight > 100_000_000 {
		return MoneroWatchWallet{}, errors.New("Restore height is out of range.")
	}
	if s.Err() != nil {
		return MoneroWatchWallet{}, errMoneroWatchUnavailable
	}
	if s.isRPCPoisoned() {
		return MoneroWatchWallet{}, errors.New("Monero wallet service requires a dashboard restart after an interrupted operation")
	}
	s.mu.RLock()
	if len(s.wallets) >= maxMoneroWatchWallets {
		s.mu.RUnlock()
		return MoneroWatchWallet{}, errors.New("At most 8 Monero wallets can be monitored.")
	}
	for _, wallet := range s.wallets {
		if strings.EqualFold(wallet.Name, name) || wallet.Address == address {
			s.mu.RUnlock()
			return MoneroWatchWallet{}, errors.New("That Monero wallet is already configured.")
		}
	}
	s.mu.RUnlock()

	var daemonInfo moneroInfo
	if err := s.daemon.call(ctx, "get_info", &daemonInfo); err != nil {
		return MoneroWatchWallet{}, errors.New("Monero node could not validate the restore height")
	}
	knownHeight := daemonInfo.TargetHeight
	if knownHeight < daemonInfo.Height {
		knownHeight = daemonInfo.Height
	}
	if restoreHeight > knownHeight {
		return MoneroWatchWallet{}, errors.New("Restore height is beyond the Monero node's known chain height.")
	}

	id := moneroWalletID(address)
	walletFile, err := newMoneroWalletFilename(id)
	if err != nil {
		return MoneroWatchWallet{}, errMoneroWatchUnavailable
	}
	wallet := MoneroWatchWallet{ID: id, Name: name, Address: address, Example: example, SourceURL: sourceURL, RestoreHeight: restoreHeight, State: "pending", SpendingStatus: "unknown", WalletFile: walletFile}
	err = s.serialized(ctx, func(ctx context.Context) error {
		var generated struct {
			Address string `json:"address"`
		}
		generateErr := s.walletCall(ctx, "generate_from_keys", map[string]any{
			"restore_height": restoreHeight, "filename": wallet.WalletFile, "address": address,
			"viewkey": strings.ToLower(viewKey), "password": s.password, "autosave_current": true,
		}, &generated)
		if generateErr != nil {
			return errors.New("Monero wallet service could not create the watch-only wallet")
		}
		if generated.Address != address {
			return errors.New("Monero wallet service returned a different address")
		}
		var current moneroWalletAddress
		if err := s.walletCall(ctx, "get_address", map[string]any{}, &current); err != nil || current.Address != address {
			return errors.New("Monero wallet service did not create a verified watch-only wallet")
		}
		var key struct {
			Key string `json:"key"`
		}
		if err := s.walletCall(ctx, "query_key", map[string]any{"key_type": "view_key"}, &key); err != nil || !strings.EqualFold(key.Key, viewKey) {
			return errors.New("The private view key does not match this watch-only wallet")
		}
		return s.walletCall(ctx, "store", map[string]any{}, nil)
	})
	viewKey = ""
	if err != nil {
		return MoneroWatchWallet{}, err
	}
	s.mu.Lock()
	if example && !s.publicExamplePending {
		s.mu.Unlock()
		return MoneroWatchWallet{}, errMoneroPublicExampleSuperseded
	}
	if len(s.wallets) >= maxMoneroWatchWallets {
		s.mu.Unlock()
		return MoneroWatchWallet{}, errors.New("At most 8 Monero wallets can be monitored.")
	}
	for _, current := range s.wallets {
		if strings.EqualFold(current.Name, name) || current.Address == address {
			s.mu.Unlock()
			return MoneroWatchWallet{}, errors.New("That Monero wallet is already configured.")
		}
	}
	s.wallets = append(s.wallets, wallet)
	err = s.saveLocked()
	if err != nil {
		s.wallets = s.wallets[:len(s.wallets)-1]
	} else {
		s.publicExamplePending = false
	}
	s.mu.Unlock()
	if err != nil {
		return MoneroWatchWallet{}, err
	}
	return wallet, nil
}

func (s *MoneroWatchStore) Remove(id string) (bool, error) {
	if s.Err() != nil {
		return false, errMoneroWatchUnavailable
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.wallets {
		if s.wallets[i].ID != id {
			continue
		}
		previous := append([]MoneroWatchWallet(nil), s.wallets...)
		s.wallets = append(s.wallets[:i], s.wallets[i+1:]...)
		if err := s.saveLocked(); err != nil {
			s.wallets = previous
			return false, err
		}
		return true, nil
	}
	return false, nil
}

func (s *MoneroWatchStore) ImportKeyImages(ctx context.Context, id string, upload moneroKeyImageUpload) error {
	if len(upload.SignedKeyImages) == 0 || len(upload.SignedKeyImages) > 5000 {
		return errors.New("Signed key images must contain between 1 and 5000 entries.")
	}
	for _, item := range upload.SignedKeyImages {
		if !validNonzeroHex(item.KeyImage, hex64Pattern) || !validNonzeroHex(item.Signature, hex128Pattern) {
			return errors.New("Signed key images are invalid.")
		}
	}
	wallet, ok := s.walletByID(id)
	if !ok {
		return os.ErrNotExist
	}
	if wallet.Example {
		return errors.New("The public Monero example does not accept key images.")
	}
	if err := s.serialized(ctx, func(ctx context.Context) error {
		if err := s.openWallet(ctx, wallet); err != nil {
			return err
		}
		var result struct {
			Height uint64 `json:"height"`
		}
		if err := s.walletCall(ctx, "import_key_images", upload, &result); err != nil {
			return errors.New("Monero wallet service could not import the signed key images")
		}
		return s.walletCall(ctx, "store", map[string]any{}, nil)
	}); err != nil {
		return err
	}
	s.mu.Lock()
	for i := range s.wallets {
		if s.wallets[i].ID == id {
			s.wallets[i].KeyImages.Present = true
			s.wallets[i].KeyImages.UpdatedAt = s.now().Unix()
			s.wallets[i].KeyImages.Complete = false
			s.wallets[i].BalanceAtomic, s.wallets[i].BalanceXMR, s.wallets[i].UnlockedBalanceAtomic = "", "", ""
			s.wallets[i].SpendingStatus = "stale"
			s.wallets[i].State = "pending"
			s.wallets[i].Error = ""
			if err := s.saveLocked(); err != nil {
				s.mu.Unlock()
				return err
			}
			break
		}
	}
	s.mu.Unlock()
	return nil
}

func (s *MoneroWatchStore) run(ctx context.Context, active func() bool) {
	if !s.enabled || s.Err() != nil {
		return
	}
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	if active() {
		s.ensurePublicExample(ctx)
		for _, wallet := range s.List() {
			s.Queue(wallet.ID)
		}
	}
	index := 0
	for {
		select {
		case <-ctx.Done():
			return
		case id := <-s.queue:
			if active() {
				if wallet, ok := s.walletByID(id); ok {
					s.refresh(ctx, wallet)
				}
			}
		case <-ticker.C:
			if !active() {
				continue
			}
			s.ensurePublicExample(ctx)
			wallets := s.List()
			if len(wallets) > 0 {
				if index >= len(wallets) {
					index = 0
				}
				s.refresh(ctx, wallets[index])
				index++
			}
		}
	}
}

func (s *MoneroWatchStore) Queue(id string) {
	select {
	case s.queue <- id:
	default:
	}
}

func (s *MoneroWatchStore) refresh(parent context.Context, wallet MoneroWatchWallet) {
	wallet, ok := s.walletByID(wallet.ID)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(parent, moneroRefreshTimeout)
	defer cancel()
	var snapshot moneroWalletSnapshot
	var daemonInfo moneroInfo
	if err := s.daemon.call(ctx, "get_info", &daemonInfo); err != nil {
		s.applyRefresh(wallet.ID, snapshot, err)
		return
	}
	targetHeight := daemonInfo.TargetHeight
	if targetHeight < daemonInfo.Height {
		targetHeight = daemonInfo.Height
	}
	snapshot.daemonHeight = targetHeight
	if !daemonInfo.Synchronized || daemonInfo.BusySyncing || targetHeight == 0 {
		s.applyRefresh(wallet.ID, snapshot, errMoneroScanPending)
		return
	}
	err := s.serialized(ctx, func(ctx context.Context) error {
		if err := s.openWallet(ctx, wallet); err != nil {
			return err
		}
		var current moneroWalletAddress
		if err := s.walletCall(ctx, "get_address", map[string]any{}, &current); err != nil || current.Address != wallet.Address {
			return errors.New("wallet identity check failed")
		}
		// A far-behind wallet scans in the RPC's background auto-refresh; a blocking refresh would outlive moneroRefreshTimeout and poison the service.
		if err := s.walletCall(ctx, "auto_refresh", map[string]any{"enable": true}, nil); err != nil {
			return err
		}
		var walletHeight moneroWalletHeight
		if err := s.walletCall(ctx, "get_height", map[string]any{}, &walletHeight); err != nil {
			return err
		}
		snapshot.walletHeight, snapshot.daemonHeight = walletHeight.Height, targetHeight
		if walletHeight.Height < targetHeight {
			return errMoneroScanPending
		}
		if err := s.walletCall(ctx, "refresh", map[string]any{}, nil); err != nil {
			return err
		}
		var balance moneroWalletBalance
		if err := s.walletCall(ctx, "get_balance", map[string]any{"all_accounts": true, "strict": true}, &balance); err != nil {
			return err
		}
		var accounts moneroWalletAccounts
		if err := s.walletCall(ctx, "get_accounts", map[string]any{}, &accounts); err != nil || len(accounts.SubaddressAccounts) == 0 || len(accounts.SubaddressAccounts) > 64 {
			return errors.New("invalid Monero wallet accounts")
		}
		seenAccounts := make(map[uint32]bool, len(accounts.SubaddressAccounts))
		var incoming moneroIncomingTransfers
		for _, account := range accounts.SubaddressAccounts {
			if seenAccounts[account.AccountIndex] {
				return errors.New("invalid Monero wallet accounts")
			}
			seenAccounts[account.AccountIndex] = true
			var accountIncoming moneroIncomingTransfers
			if err := s.walletCall(ctx, "incoming_transfers", map[string]any{"transfer_type": "all", "account_index": account.AccountIndex}, &accountIncoming); err != nil {
				return err
			}
			incoming.Transfers = append(incoming.Transfers, accountIncoming.Transfers...)
			if len(incoming.Transfers) > 100_000 {
				return errors.New("Monero watch-only wallet has too many outputs")
			}
		}
		var err error
		snapshot, err = summarizeMoneroWallet(walletHeight.Height, targetHeight, balance, incoming)
		if err != nil || wallet.State == "ready" {
			return err
		}
		return s.walletCall(ctx, "store", map[string]any{}, nil)
	})
	s.applyRefresh(wallet.ID, snapshot, err)
}

func (s *MoneroWatchStore) applyRefresh(id string, snapshot moneroWalletSnapshot, refreshErr error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.wallets {
		wallet := &s.wallets[i]
		if wallet.ID != id {
			continue
		}
		if refreshErr != nil {
			wallet.State = "error"
			if errors.Is(refreshErr, errMoneroScanPending) {
				wallet.State = "pending"
			}
			if snapshot.walletHeight > 0 {
				wallet.ScanHeight, wallet.TargetHeight = snapshot.walletHeight, snapshot.daemonHeight
			}
			wallet.KeyImages.Complete = false
			wallet.BalanceAtomic, wallet.BalanceXMR, wallet.UnlockedBalanceAtomic = "", "", ""
			if wallet.KeyImages.Present {
				wallet.SpendingStatus = "stale"
			} else {
				wallet.SpendingStatus = "unknown"
			}
			switch {
			case errors.Is(refreshErr, errMoneroScanPending):
				wallet.Error = "Monero wallet scan is still pending."
			case errors.Is(refreshErr, context.Canceled), errors.Is(refreshErr, context.DeadlineExceeded), s.rpcPoisoned:
				wallet.Error = "Monero wallet service needs a dashboard restart after an interrupted operation."
			default:
				wallet.Error = "Monero wallet refresh failed."
			}
			_ = s.saveLocked()
			return
		}
		wallet.State, wallet.Error = "ready", ""
		wallet.ReceivedAtomic = fmt.Sprint(snapshot.received)
		wallet.ReceivedXMR = formatXMR(snapshot.received)
		wallet.ScanHeight, wallet.TargetHeight = snapshot.walletHeight, snapshot.daemonHeight
		wallet.KeyImages.CoveredOutputs = snapshot.coveredOutputs
		wallet.KeyImages.TotalOutputs = snapshot.totalOutputs
		wallet.KeyImages.Complete = !wallet.Example && wallet.KeyImages.Present && snapshot.totalOutputs > 0 && snapshot.coveredOutputs == snapshot.totalOutputs
		if wallet.KeyImages.Complete {
			wallet.SpendingStatus = "verified"
			wallet.BalanceAtomic = fmt.Sprint(snapshot.balance)
			wallet.BalanceXMR = formatXMR(snapshot.balance)
			wallet.UnlockedBalanceAtomic = fmt.Sprint(snapshot.unlocked)
		} else {
			wallet.BalanceAtomic, wallet.BalanceXMR, wallet.UnlockedBalanceAtomic = "", "", ""
			if wallet.KeyImages.Present {
				wallet.SpendingStatus = "stale"
			} else {
				wallet.SpendingStatus = "unknown"
			}
		}
		wallet.UpdatedAt = s.now().Unix()
		verified := wallet.KeyImages.Complete
		if verified {
			amount := wallet.BalanceAtomic
			if len(wallet.History) == 0 || wallet.History[len(wallet.History)-1].AmountAtomic != amount {
				wallet.History = append(wallet.History, MoneroWatchHistory{Timestamp: wallet.UpdatedAt, AmountAtomic: amount, Verified: true})
			}
			if len(wallet.History) > maxMoneroWatchHistory {
				wallet.History = append([]MoneroWatchHistory(nil), wallet.History[len(wallet.History)-maxMoneroWatchHistory:]...)
			}
		}
		_ = s.saveLocked()
		return
	}
}

type moneroWalletAddress struct {
	Address string `json:"address"`
}

type moneroWalletHeight struct {
	Height uint64 `json:"height"`
}

type moneroWalletBalance struct {
	Balance         uint64 `json:"balance"`
	UnlockedBalance uint64 `json:"unlocked_balance"`
}

type moneroWalletAccounts struct {
	SubaddressAccounts []struct {
		AccountIndex uint32 `json:"account_index"`
	} `json:"subaddress_accounts"`
}

type moneroIncomingTransfers struct {
	Transfers []struct {
		Amount   uint64 `json:"amount"`
		KeyImage string `json:"key_image"`
	} `json:"transfers"`
}

type moneroWalletSnapshot struct {
	received, balance, unlocked  uint64
	walletHeight, daemonHeight   uint64
	coveredOutputs, totalOutputs int
}

func summarizeMoneroWallet(walletHeight, daemonHeight uint64, balance moneroWalletBalance, incoming moneroIncomingTransfers) (moneroWalletSnapshot, error) {
	result := moneroWalletSnapshot{balance: balance.Balance, unlocked: balance.UnlockedBalance, walletHeight: walletHeight, daemonHeight: daemonHeight, totalOutputs: len(incoming.Transfers)}
	for _, transfer := range incoming.Transfers {
		if math.MaxUint64-result.received < transfer.Amount {
			return moneroWalletSnapshot{}, errors.New("invalid Monero wallet amount")
		}
		result.received += transfer.Amount
		if validNonzeroHex(transfer.KeyImage, hex64Pattern) {
			result.coveredOutputs++
		}
	}
	if result.unlocked > result.balance || result.balance > result.received {
		return moneroWalletSnapshot{}, errors.New("invalid Monero wallet balance")
	}
	return result, nil
}

// Reopening stores and reloads the whole wallet cache and stops its background scan, so an already-open wallet is kept.
func (s *MoneroWatchStore) openWallet(ctx context.Context, wallet MoneroWatchWallet) error {
	var current moneroWalletAddress
	if s.walletCall(ctx, "get_address", map[string]any{}, &current) == nil && current.Address == wallet.Address {
		return nil
	}
	_ = s.walletCall(ctx, "close_wallet", map[string]any{}, nil)
	if err := s.walletCall(ctx, "open_wallet", map[string]any{"filename": wallet.WalletFile, "password": s.password}, nil); err != nil {
		return errors.New("Monero watch-only wallet could not be opened")
	}
	return nil
}

func (s *MoneroWatchStore) serialized(ctx context.Context, fn func(context.Context) error) error {
	if s.isRPCPoisoned() {
		return errors.New("Monero wallet service requires a dashboard restart after an interrupted operation")
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-s.gate:
	}
	defer func() { s.gate <- struct{}{} }()
	if s.isRPCPoisoned() {
		return errors.New("Monero wallet service requires a dashboard restart after an interrupted operation")
	}
	err := fn(ctx)
	if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, errMoneroRemoteUnknown) {
		s.poisonRPC(errors.Join(err, ctx.Err()))
	}
	if s.isRPCPoisoned() {
		return fmt.Errorf("Monero wallet service requires a dashboard restart after an interrupted operation: %w", errors.Join(errMoneroRemoteUnknown, err))
	}
	return err
}

func (s *MoneroWatchStore) poisonRPC(cause error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.rpcPoisoned {
		return
	}
	log.Printf("Monero wallet service stopped until the dashboard restarts: %v", cause)
	s.rpcPoisoned = true
	for i := range s.wallets {
		wallet := &s.wallets[i]
		wallet.State, wallet.SpendingStatus = "error", "stale"
		wallet.KeyImages.Complete = false
		wallet.BalanceAtomic, wallet.BalanceXMR, wallet.UnlockedBalanceAtomic = "", "", ""
		wallet.Error = "Restart the dashboard after an interrupted wallet operation."
	}
	_ = s.saveLocked()
}

func (s *MoneroWatchStore) isRPCPoisoned() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.rpcPoisoned
}

// A lost reply to one of these leaves nothing ambiguous: the next refresh checks which wallet is open and re-reads.
var moneroRetryableWalletMethods = map[string]bool{"close_wallet": true, "open_wallet": true, "get_address": true, "get_height": true, "get_balance": true, "get_accounts": true, "incoming_transfers": true, "auto_refresh": true}

func (s *MoneroWatchStore) walletCall(ctx context.Context, method string, params any, out any) (callErr error) {
	defer func() {
		if !errors.Is(callErr, errMoneroRemoteUnknown) {
			return
		}
		if moneroRetryableWalletMethods[method] {
			callErr = fmt.Errorf("Monero wallet RPC %s failed", method)
			return
		}
		s.poisonRPC(fmt.Errorf("%s: %w", method, callErr))
	}()
	switch method {
	case "close_wallet", "open_wallet", "generate_from_keys", "get_address", "get_height", "get_accounts", "get_balance", "incoming_transfers", "import_key_images", "query_key", "refresh", "auto_refresh", "store":
	default:
		return errors.New("unsupported Monero wallet RPC method")
	}
	if method == "query_key" {
		query, ok := params.(map[string]any)
		if !ok || len(query) != 1 || query["key_type"] != "view_key" {
			return errors.New("unsupported Monero wallet RPC method")
		}
	}
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": "dashboard", "method": method, "params": params})
	if err != nil {
		return err
	}
	probe, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": "dashboard-auth", "method": "get_version"})
	if err != nil {
		return err
	}
	responseBody, err := s.rpc.doDigest(ctx, http.MethodPost, "/json_rpc", body, probe)
	if err != nil {
		return err
	}
	var envelope struct {
		Result json.RawMessage `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	if json.Unmarshal(responseBody, &envelope) != nil {
		return fmt.Errorf("%w: invalid JSON response", errMoneroRemoteUnknown)
	}
	if len(envelope.Error) > 0 && string(envelope.Error) != "null" {
		return errors.New("Monero wallet RPC error")
	}
	if len(envelope.Result) == 0 || string(envelope.Result) == "null" {
		return fmt.Errorf("%w: missing result", errMoneroRemoteUnknown)
	}
	if out == nil {
		return nil
	}
	if json.Unmarshal(envelope.Result, out) != nil {
		return fmt.Errorf("%w: invalid result", errMoneroRemoteUnknown)
	}
	return nil
}

func (s *MoneroWatchStore) walletByID(id string) (MoneroWatchWallet, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, wallet := range s.wallets {
		if wallet.ID == id {
			return wallet, true
		}
	}
	return MoneroWatchWallet{}, false
}

func (s *MoneroWatchStore) saveLocked() error {
	wallets := make([]moneroStoredWallet, len(s.wallets))
	for i := range s.wallets {
		wallets[i] = moneroStoredWallet{MoneroWatchWallet: s.wallets[i], WalletFile: s.wallets[i].WalletFile}
	}
	data, err := json.Marshal(moneroWatchFile{Wallets: wallets})
	if err != nil {
		return err
	}
	if writePrivateFileAtomic(s.path, data) != nil {
		s.err = errMoneroWatchUnavailable
		return s.err
	}
	return nil
}

func moneroWalletID(address string) string {
	sum := sha256.Sum256([]byte(address))
	return hex.EncodeToString(sum[:12])
}

func newMoneroWalletFilename(id string) (string, error) {
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	return "dashboard_" + id + "_" + hex.EncodeToString(random), nil
}

func validManagedWalletFilename(id, value string) bool {
	prefix := "dashboard_" + id + "_"
	return strings.HasPrefix(value, prefix) && len(value) == len(prefix)+32 && strings.Trim(value[len(prefix):], "0123456789abcdef") == ""
}
func validMoneroName(name string) bool {
	return moneroNamePattern.MatchString(name) && strings.TrimSpace(name) == name
}
func validMoneroAddress(address string) bool { return moneroAddressPattern.MatchString(address) }

func validNonzeroHex(value string, pattern *regexp.Regexp) bool {
	return pattern.MatchString(value) && strings.Trim(value, "0") != ""
}

func validStoredMoneroWallet(wallet MoneroWatchWallet) bool {
	if !validMoneroName(wallet.Name) || !validMoneroAddress(wallet.Address) || wallet.RestoreHeight > 100_000_000 {
		return false
	}
	if wallet.SpendingStatus != "" && wallet.SpendingStatus != "unknown" && wallet.SpendingStatus != "stale" && wallet.SpendingStatus != "verified" {
		return false
	}
	if wallet.Example {
		if !validMoneroPublicExample(wallet) {
			return false
		}
	} else if wallet.SourceURL != "" {
		return false
	}
	for _, point := range wallet.History {
		if !point.Verified || point.Timestamp <= 0 || !decimalAtomic(point.AmountAtomic) {
			return false
		}
	}
	return len(wallet.History) <= maxMoneroWatchHistory && wallet.KeyImages.CoveredOutputs >= 0 && wallet.KeyImages.TotalOutputs >= wallet.KeyImages.CoveredOutputs
}

func decimalAtomic(value string) bool {
	if len(value) == 0 || len(value) > 20 {
		return false
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

func formatXMR(atomic uint64) string {
	whole, fraction := atomic/1_000_000_000_000, atomic%1_000_000_000_000
	return fmt.Sprintf("%d.%012d", whole, fraction)
}
