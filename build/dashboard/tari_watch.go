package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const (
	tariWatchPollInterval = time.Minute
	tariWatchMaxHistory   = 96
	tariWatchHistoryEvery = time.Hour
	tariWatchTipLag       = 2
	tariWatchControlGrace = 30 * time.Second
)

var (
	errTariWatchUnavailable = errors.New("Tari watch-only wallet storage unavailable")
	errTariWatchConflict    = errors.New("Only one Tari watch-only wallet can run at a time.")
	writeTariWatchFile      = writePrivateFileAtomic
)

type tariWatchHistory struct {
	Time          int64  `json:"time"`
	BalanceAtomic string `json:"balance_atomic"`
}

type TariWatchWallet struct {
	ID                    string             `json:"id"`
	Name                  string             `json:"name"`
	PublicSpendKey        string             `json:"public_spend_key"`
	Birthday              string             `json:"birthday"`
	State                 string             `json:"state"`
	BalanceAtomic         string             `json:"balance_atomic"`
	AvailableAtomic       string             `json:"available_atomic"`
	TimelockedAtomic      string             `json:"timelocked_atomic"`
	PendingIncomingAtomic string             `json:"pending_incoming_atomic"`
	PendingOutgoingAtomic string             `json:"pending_outgoing_atomic"`
	ScanHeight            uint64             `json:"scan_height"`
	TargetHeight          uint64             `json:"target_height"`
	ScanTime              int64              `json:"scan_time"`
	History               []tariWatchHistory `json:"history"`
}

type tariStoredWallet struct {
	TariWatchWallet
	BirthdayDays  uint16 `json:"birthday_days"`
	RequestID     string `json:"request_id,omitempty"`
	RequestAction string `json:"request_action,omitempty"`
}

type tariWatchFile struct {
	Wallet *tariStoredWallet `json:"wallet"`
}

type tariWatchRequest struct {
	ID             string `json:"id"`
	Action         string `json:"action"`
	WalletID       string `json:"wallet_id"`
	RequestedAt    int64  `json:"requested_at"`
	Name           string `json:"name,omitempty"`
	PublicSpendKey string `json:"public_spend_key,omitempty"`
	ViewKey        string `json:"view_key,omitempty"`
	BirthdayDays   uint16 `json:"birthday_days,omitempty"`
}

type tariWatchStatus struct {
	ID       string `json:"id"`
	Action   string `json:"action"`
	WalletID string `json:"wallet_id"`
	State    string `json:"state"`
	Message  string `json:"message,omitempty"`
}

type tariWatchIdentity struct {
	WalletID       string `json:"wallet_id"`
	PublicSpendKey string `json:"public_spend_key"`
	BirthdayDays   uint16 `json:"birthday_days"`
}

type tariWatchNodeSnapshot struct {
	Ready        bool
	Height       uint64
	TargetHeight uint64
	Reason       string
}

type tariWatchNodeProbe func(context.Context) (tariWatchNodeSnapshot, error)

type TariWatchStore struct {
	mu                sync.RWMutex
	path              string
	requestPath       string
	statusPath        string
	identityPath      string
	capabilityPath    string
	walletURL         string
	walletRPCUser     string
	walletRPCPassword string
	wallet            *tariStoredWallet
	err               error
	now               func() time.Time
	requestMissingAt  time.Time
}

func NewTariWatchStore(cfg config) *TariWatchStore {
	walletDir := cfg.tariWalletDir
	if walletDir == "" {
		walletDir = "/tari-wallet"
	}
	stateDir := cfg.stateDir
	if stateDir == "" {
		stateDir = "/state"
	}
	s := &TariWatchStore{
		path: filepath.Join(stateDir, "tari_watch.json"), requestPath: filepath.Join(walletDir, "control", "request.json"),
		statusPath: filepath.Join(walletDir, "control", "status.json"), identityPath: filepath.Join(walletDir, "identity.json"),
		capabilityPath: filepath.Join(walletDir, "control", "capability.json"), walletURL: cfg.tariWalletURL,
		walletRPCUser: cfg.tariWalletRPCUser, walletRPCPassword: cfg.tariWalletRPCPassword, now: time.Now,
	}
	s.load()
	return s
}

func (s *TariWatchStore) load() {
	if err := chmodExistingFile(s.path, 0o600); err != nil && !errors.Is(err, os.ErrNotExist) {
		s.err = errTariWatchUnavailable
		return
	}
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	var stored tariWatchFile
	if err != nil || !decodeStrictBytes(data, &stored) || stored.Wallet != nil && !validTariStoredWallet(stored.Wallet) {
		s.err = errTariWatchUnavailable
		return
	}
	s.wallet = stored.Wallet
	if s.wallet != nil && s.wallet.State == "ok" {
		s.wallet.State = "stale"
	}
}

func (s *TariWatchStore) Err() error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.err
}

func (s *TariWatchStore) List() []TariWatchWallet {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.wallet == nil {
		return []TariWatchWallet{}
	}
	wallet := s.wallet.TariWatchWallet
	wallet.History = append([]tariWatchHistory(nil), wallet.History...)
	return []TariWatchWallet{wallet}
}

func (s *TariWatchStore) Add(name, publicSpendKey, viewKey, birthday string) (TariWatchWallet, error) {
	if !s.Capable() {
		return TariWatchWallet{}, errTariWatchUnavailable
	}
	name = strings.TrimSpace(name)
	publicSpendKey = strings.ToLower(strings.TrimSpace(publicSpendKey))
	viewKey = strings.ToLower(strings.TrimSpace(viewKey))
	birthday = strings.TrimSpace(birthday)
	if !validTariWatchName(name) {
		return TariWatchWallet{}, errors.New("Name must be 1 to 80 printable characters.")
	}
	if !validTariKey(publicSpendKey) {
		return TariWatchWallet{}, errors.New("A 64-character hexadecimal public spend key is required.")
	}
	if !validTariKey(viewKey) {
		return TariWatchWallet{}, errors.New("A 64-character hexadecimal private view key is required.")
	}
	birthdayDays, canonicalBirthday, err := parseTariBirthday(birthday, s.now())
	if err != nil {
		return TariWatchWallet{}, err
	}
	walletID, err := tariWatchRandomID()
	if err != nil {
		return TariWatchWallet{}, errTariWatchUnavailable
	}
	requestID := walletID
	wallet := &tariStoredWallet{TariWatchWallet: TariWatchWallet{
		ID: walletID, Name: name, PublicSpendKey: publicSpendKey, Birthday: canonicalBirthday, State: "pending", History: []tariWatchHistory{},
	}, BirthdayDays: birthdayDays, RequestID: requestID, RequestAction: "create"}
	request := tariWatchRequest{ID: requestID, Action: "create", WalletID: walletID, RequestedAt: s.now().Unix(), Name: name, PublicSpendKey: publicSpendKey, ViewKey: viewKey, BirthdayDays: birthdayDays}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return TariWatchWallet{}, s.err
	}
	if s.wallet != nil {
		return TariWatchWallet{}, errTariWatchConflict
	}
	s.wallet = wallet
	if s.saveLocked() != nil {
		s.wallet = nil
		return TariWatchWallet{}, errTariWatchUnavailable
	}
	if s.writeRequestLocked(request) != nil {
		s.wallet = nil
		if s.saveLocked() != nil {
			s.err = errTariWatchUnavailable
		}
		return TariWatchWallet{}, errTariWatchUnavailable
	}
	return wallet.TariWatchWallet, nil
}

func (s *TariWatchStore) Remove(id string) (bool, error) {
	if !s.Capable() {
		return false, errTariWatchUnavailable
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return false, s.err
	}
	if s.wallet == nil || s.wallet.ID != id {
		return false, nil
	}
	if s.wallet.State == "removing" {
		return true, nil
	}
	if s.wallet.State == "error" && !s.identityMatchesWallet(s.wallet) {
		previous := s.wallet
		s.wallet = nil
		if s.saveLocked() != nil {
			s.wallet = previous
			return false, errTariWatchUnavailable
		}
		removeTariWatchControlFile(s.requestPath)
		return true, nil
	}
	requestID, err := tariWatchRandomID()
	if err != nil {
		return false, errTariWatchUnavailable
	}
	previous := *s.wallet
	s.wallet.State, s.wallet.RequestID, s.wallet.RequestAction = "removing", requestID, "remove"
	if s.saveLocked() != nil {
		*s.wallet = previous
		return false, errTariWatchUnavailable
	}
	request := tariWatchRequest{ID: requestID, Action: "remove", WalletID: id, RequestedAt: s.now().Unix()}
	if s.writeRequestLocked(request) != nil {
		*s.wallet = previous
		if s.saveLocked() != nil {
			s.err = errTariWatchUnavailable
		}
		return false, errTariWatchUnavailable
	}
	return true, nil
}

func (s *TariWatchStore) run(ctx context.Context, runnable func() bool, probe tariWatchNodeProbe) {
	s.step(ctx, runnable, probe)
	ticker := time.NewTicker(tariWatchPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.step(ctx, runnable, probe)
		}
	}
}

func (s *TariWatchStore) step(ctx context.Context, runnable func() bool, probe tariWatchNodeProbe) {
	if s.Err() != nil {
		return
	}
	if !s.reconcileControl() {
		return
	}
	s.mu.RLock()
	wallet := s.wallet
	ready := wallet != nil && wallet.RequestID == "" && wallet.State != "removing"
	s.mu.RUnlock()
	if !ready {
		return
	}
	if !s.Capable() {
		s.markUnready("waiting")
		return
	}
	if !s.identityMatchesCurrentWallet() {
		s.markIdentityMismatch()
		return
	}
	if !runnable() {
		s.markUnready("waiting")
		return
	}
	node, err := probe(ctx)
	if err != nil || !node.Ready {
		s.markUnready("waiting")
		return
	}
	s.refresh(ctx, node)
}

func (s *TariWatchStore) reconcileControl() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.wallet == nil || s.wallet.RequestID == "" {
		return true
	}
	requestData, requestErr := os.ReadFile(s.requestPath)
	requestMissing := errors.Is(requestErr, os.ErrNotExist)
	if requestErr != nil && !requestMissing {
		return false
	}
	if requestErr == nil {
		s.requestMissingAt = time.Time{}
		var request tariWatchRequest
		if !decodeStrictBytes(requestData, &request) || !validTariWatchRequest(request) || request.ID != s.wallet.RequestID || request.Action != s.wallet.RequestAction || request.WalletID != s.wallet.ID {
			s.failControlLocked()
			return false
		}
	}
	statusData, statusErr := os.ReadFile(s.statusPath)
	if errors.Is(statusErr, os.ErrNotExist) {
		if requestMissing && s.missingRequestExpiredLocked() {
			s.failControlLocked()
		}
		return false
	}
	var status tariWatchStatus
	if statusErr != nil || !decodeStrictBytes(statusData, &status) || !validTariWatchStatus(status) {
		if requestMissing && s.missingRequestExpiredLocked() {
			s.failControlLocked()
		}
		return false
	}
	if status.ID != s.wallet.RequestID || status.Action != s.wallet.RequestAction || status.WalletID != s.wallet.ID {
		if requestMissing && s.missingRequestExpiredLocked() {
			s.failControlLocked()
		}
		return false
	}
	if status.State == "pending" {
		if requestMissing && s.missingRequestExpiredLocked() {
			s.failControlLocked()
		}
		return false
	}
	if status.State == "error" {
		s.wallet.State = "error"
		s.wallet.RequestID, s.wallet.RequestAction = "", ""
		if s.saveLocked() != nil {
			s.err = errTariWatchUnavailable
		}
		removeTariWatchControlFile(s.requestPath)
		return false
	}
	if status.Action == "remove" && status.State == "removed" {
		s.wallet = nil
		if s.saveLocked() != nil {
			s.err = errTariWatchUnavailable
			return false
		}
		removeTariWatchControlFile(s.requestPath)
		return false
	}
	if status.Action != "create" || status.State != "ready" {
		return false
	}
	identityData, err := os.ReadFile(s.identityPath)
	var identity tariWatchIdentity
	if err != nil || !decodeStrictBytes(identityData, &identity) || identity.WalletID != s.wallet.ID || strings.ToLower(identity.PublicSpendKey) != s.wallet.PublicSpendKey || identity.BirthdayDays != s.wallet.BirthdayDays {
		s.wallet.State = "error"
		s.wallet.RequestID, s.wallet.RequestAction = "", ""
		if s.saveLocked() != nil {
			s.err = errTariWatchUnavailable
		}
		removeTariWatchControlFile(s.requestPath)
		return false
	}
	s.wallet.State = "waiting"
	s.wallet.RequestID, s.wallet.RequestAction = "", ""
	if s.saveLocked() != nil {
		s.err = errTariWatchUnavailable
		return false
	}
	removeTariWatchControlFile(s.requestPath)
	return true
}

func (s *TariWatchStore) failControlLocked() {
	s.requestMissingAt = time.Time{}
	s.wallet.State = "error"
	s.wallet.RequestID, s.wallet.RequestAction = "", ""
	if s.saveLocked() != nil {
		s.err = errTariWatchUnavailable
	}
	removeTariWatchControlFile(s.requestPath)
}

func (s *TariWatchStore) missingRequestExpiredLocked() bool {
	now := s.now()
	if s.requestMissingAt.IsZero() || now.Before(s.requestMissingAt) {
		s.requestMissingAt = now
		return false
	}
	return now.Sub(s.requestMissingAt) >= tariWatchControlGrace
}

func (s *TariWatchStore) refresh(ctx context.Context, node tariWatchNodeSnapshot) {
	expected := s.refreshWalletSnapshot()
	if expected == nil {
		return
	}
	if !s.Capable() {
		s.markUnreadyFor(expected.ID, "waiting")
		return
	}
	if !s.identityMatchesWallet(expected) {
		s.markIdentityMismatchFor(expected.ID)
		return
	}
	endpoint, err := url.Parse(s.walletURL)
	if err != nil || endpoint.Host == "" {
		s.markUnreadyFor(expected.ID, "error")
		return
	}
	connection, err := grpc.NewClient(endpoint.Host, grpc.WithNoProxy(), internalGRPCDialer, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithPerRPCCredentials(tariWalletBasicAuth{user: s.walletRPCUser, password: s.walletRPCPassword}))
	if err != nil {
		s.markUnreadyFor(expected.ID, "error")
		return
	}
	defer connection.Close()
	client := NewWalletClient(connection)
	callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	version, err := client.GetVersion(callCtx, &TariGetVersionRequest{})
	if err != nil || strings.TrimSpace(version.Version) == "" {
		s.markUnreadyFor(expected.ID, "error")
		return
	}
	address, err := client.GetCompleteAddress(callCtx, &TariWalletEmpty{})
	if err != nil || address.InteractiveAddressBase58 == "" && address.OneSidedAddressBase58 == "" {
		s.markUnreadyFor(expected.ID, "error")
		return
	}
	state, err := client.GetState(callCtx, &TariGetStateRequest{})
	if err != nil || state.Network == nil {
		s.markUnreadyFor(expected.ID, "error")
		return
	}
	if state.Network.Status != TariWalletConnectivityStatus_TARI_WALLET_ONLINE || !state.HasDoneInitialValidation || !tariHeightReady(state.ScannedHeight, node) {
		s.markWaitingProgressFor(expected.ID, state.ScannedHeight, node)
		s.markUnreadyFor(expected.ID, "waiting")
		return
	}
	balance, err := client.GetBalance(callCtx, &TariGetBalanceRequest{})
	if err != nil {
		s.markUnreadyFor(expected.ID, "error")
		return
	}
	total, ok := tariBalanceTotal(balance)
	if !ok {
		s.markUnreadyFor(expected.ID, "error")
		return
	}
	now := s.now().Unix()
	if !s.Capable() {
		s.markUnreadyFor(expected.ID, "waiting")
		return
	}
	if !s.identityMatchesWallet(expected) {
		s.markIdentityMismatchFor(expected.ID)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.wallet == nil || s.wallet.ID != expected.ID || s.wallet.RequestID != "" || s.wallet.State == "removing" {
		return
	}
	s.wallet.BalanceAtomic = strconv.FormatUint(total, 10)
	s.wallet.AvailableAtomic = strconv.FormatUint(balance.AvailableBalance, 10)
	s.wallet.TimelockedAtomic = strconv.FormatUint(balance.TimelockedBalance, 10)
	s.wallet.PendingIncomingAtomic = strconv.FormatUint(balance.PendingIncomingBalance, 10)
	s.wallet.PendingOutgoingAtomic = strconv.FormatUint(balance.PendingOutgoingBalance, 10)
	s.wallet.ScanHeight, s.wallet.TargetHeight, s.wallet.ScanTime, s.wallet.State = state.ScannedHeight, node.TargetHeight, now, "ok"
	if s.wallet.TargetHeight == 0 {
		s.wallet.TargetHeight = node.Height
	}
	if len(s.wallet.History) == 0 || now-s.wallet.History[len(s.wallet.History)-1].Time >= int64(tariWatchHistoryEvery/time.Second) {
		s.wallet.History = append(s.wallet.History, tariWatchHistory{Time: now, BalanceAtomic: s.wallet.BalanceAtomic})
		if len(s.wallet.History) > tariWatchMaxHistory {
			s.wallet.History = append([]tariWatchHistory(nil), s.wallet.History[len(s.wallet.History)-tariWatchMaxHistory:]...)
		}
	}
	if s.saveLocked() != nil {
		s.wallet.State = "stale"
		s.err = errTariWatchUnavailable
	}
}

func (s *TariWatchStore) refreshWalletSnapshot() *tariStoredWallet {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.wallet == nil || s.wallet.RequestID != "" || s.wallet.State == "removing" {
		return nil
	}
	expected := *s.wallet
	return &expected
}

func (s *TariWatchStore) identityMatchesCurrentWallet() bool {
	s.mu.RLock()
	wallet := s.wallet
	if wallet == nil {
		s.mu.RUnlock()
		return false
	}
	expected := *wallet
	s.mu.RUnlock()
	return s.identityMatchesWallet(&expected)
}

func (s *TariWatchStore) identityMatchesWallet(wallet *tariStoredWallet) bool {
	data, err := os.ReadFile(s.identityPath)
	var identity tariWatchIdentity
	return err == nil && decodeStrictBytes(data, &identity) && identity.WalletID == wallet.ID && strings.ToLower(identity.PublicSpendKey) == wallet.PublicSpendKey && identity.BirthdayDays == wallet.BirthdayDays
}

func (s *TariWatchStore) markIdentityMismatch() {
	s.mu.RLock()
	id := ""
	if s.wallet != nil {
		id = s.wallet.ID
	}
	s.mu.RUnlock()
	s.markIdentityMismatchFor(id)
}

func (s *TariWatchStore) markIdentityMismatchFor(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.wallet == nil || s.wallet.ID != id || s.wallet.State == "removing" {
		return
	}
	if s.wallet.BalanceAtomic == "" {
		s.wallet.State = "error"
	} else {
		s.wallet.State = "stale"
	}
	_ = s.saveLocked()
}

func (s *TariWatchStore) markWaitingProgressFor(id string, scanned uint64, node tariWatchNodeSnapshot) {
	target := node.TargetHeight
	if target == 0 {
		target = node.Height
	}
	if target == 0 || scanned > target+tariWatchTipLag {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.wallet == nil || s.wallet.ID != id || s.wallet.BalanceAtomic != "" || s.wallet.State == "removing" {
		return
	}
	s.wallet.ScanHeight, s.wallet.TargetHeight = scanned, target
	_ = s.saveLocked()
}

func (s *TariWatchStore) markUnready(emptyState string) {
	s.mu.RLock()
	id := ""
	if s.wallet != nil {
		id = s.wallet.ID
	}
	s.mu.RUnlock()
	s.markUnreadyFor(id, emptyState)
}

func (s *TariWatchStore) markUnreadyFor(id, emptyState string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.wallet == nil || s.wallet.ID != id || s.wallet.State == "pending" || s.wallet.State == "removing" || s.wallet.State == "error" {
		return
	}
	if s.wallet.BalanceAtomic != "" {
		s.wallet.State = "stale"
	} else {
		s.wallet.State = emptyState
	}
	_ = s.saveLocked()
}

func (s *TariWatchStore) saveLocked() error {
	data, err := json.Marshal(tariWatchFile{Wallet: s.wallet})
	if err != nil || writeTariWatchFile(s.path, append(data, '\n')) != nil {
		return errTariWatchUnavailable
	}
	return nil
}

func (s *TariWatchStore) writeRequestLocked(request tariWatchRequest) error {
	data, err := json.Marshal(request)
	if err != nil || writeTariWatchFile(s.requestPath, append(data, '\n')) != nil {
		return errTariWatchUnavailable
	}
	return nil
}

func removeTariWatchControlFile(path string) {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return
	}
}

func tariWatchRandomID() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

func validTariWatchName(name string) bool {
	if name == "" || len(name) > 80 || !utf8.ValidString(name) || strings.TrimSpace(name) != name {
		return false
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

func validTariKey(key string) bool {
	if len(key) != 64 {
		return false
	}
	decoded, err := hex.DecodeString(key)
	return err == nil && hex.EncodeToString(decoded) == key
}

func parseTariBirthday(value string, now time.Time) (uint16, string, error) {
	if value == "" {
		return 0, "", nil
	}
	birthday, err := time.Parse("2006-01-02", value)
	genesis := time.Date(2022, time.January, 1, 0, 0, 0, 0, time.UTC)
	today := time.Date(now.UTC().Year(), now.UTC().Month(), now.UTC().Day(), 0, 0, 0, 0, time.UTC)
	if err != nil || birthday.Before(genesis) || birthday.After(today) {
		return 0, "", errors.New("Birthday must be a date from 2022-01-01 through today.")
	}
	days := int(birthday.Sub(genesis) / (24 * time.Hour))
	if days > int(^uint16(0)) {
		return 0, "", errors.New("Birthday is outside the supported range.")
	}
	return uint16(days), value, nil
}

func validTariStoredWallet(wallet *tariStoredWallet) bool {
	if wallet == nil || len(wallet.ID) != 32 || strings.Trim(wallet.ID, "0123456789abcdef") != "" || !validTariWatchName(wallet.Name) || !validTariKey(wallet.PublicSpendKey) {
		return false
	}
	if wallet.Birthday != "" {
		days, canonical, err := parseTariBirthday(wallet.Birthday, time.Now())
		if err != nil || canonical != wallet.Birthday || days != wallet.BirthdayDays {
			return false
		}
	}
	if !validTariWatchState(wallet.State) || len(wallet.History) > tariWatchMaxHistory || wallet.RequestID != "" && (len(wallet.RequestID) != 32 || strings.Trim(wallet.RequestID, "0123456789abcdef") != "") {
		return false
	}
	if wallet.RequestAction != "" && wallet.RequestAction != "create" && wallet.RequestAction != "remove" {
		return false
	}
	if (wallet.RequestID == "") != (wallet.RequestAction == "") {
		return false
	}
	for _, value := range []string{wallet.BalanceAtomic, wallet.AvailableAtomic, wallet.TimelockedAtomic, wallet.PendingIncomingAtomic, wallet.PendingOutgoingAtomic} {
		if value != "" {
			if _, err := strconv.ParseUint(value, 10, 64); err != nil {
				return false
			}
		}
	}
	for _, point := range wallet.History {
		if point.Time <= 0 {
			return false
		}
		if _, err := strconv.ParseUint(point.BalanceAtomic, 10, 64); err != nil {
			return false
		}
	}
	return true
}

func validTariWatchState(state string) bool {
	switch state {
	case "pending", "waiting", "ok", "stale", "error", "removing":
		return true
	default:
		return false
	}
}

func validTariWatchStatus(status tariWatchStatus) bool {
	if len(status.ID) != 32 || strings.Trim(status.ID, "0123456789abcdef") != "" || len(status.WalletID) != 32 || strings.Trim(status.WalletID, "0123456789abcdef") != "" {
		return false
	}
	if status.Action == "create" {
		return status.State == "pending" || status.State == "ready" || status.State == "error"
	}
	return status.Action == "remove" && (status.State == "pending" || status.State == "removed" || status.State == "error")
}

func validTariWatchRequest(request tariWatchRequest) bool {
	if len(request.ID) != 32 || strings.Trim(request.ID, "0123456789abcdef") != "" || len(request.WalletID) != 32 || strings.Trim(request.WalletID, "0123456789abcdef") != "" || request.RequestedAt <= 0 {
		return false
	}
	if request.Action == "create" {
		return request.ID == request.WalletID && validTariWatchName(request.Name) && validTariKey(request.PublicSpendKey) && validTariKey(request.ViewKey)
	}
	return request.Action == "remove" && request.Name == "" && request.PublicSpendKey == "" && request.ViewKey == "" && request.BirthdayDays == 0
}

func tariHeightReady(scanned uint64, node tariWatchNodeSnapshot) bool {
	target := node.TargetHeight
	if target == 0 {
		target = node.Height
	}
	if target == 0 || scanned > target+tariWatchTipLag {
		return false
	}
	return scanned+tariWatchTipLag >= target
}

func tariBalanceTotal(balance *TariGetBalanceResponse) (uint64, bool) {
	if balance == nil {
		return 0, false
	}
	total := balance.AvailableBalance
	if ^uint64(0)-total < balance.TimelockedBalance {
		return 0, false
	}
	return total + balance.TimelockedBalance, true
}

type tariWatchCapability struct {
	Schema          int      `json:"schema"`
	UpdatedAt       int64    `json:"updated_at"`
	Actions         []string `json:"actions"`
	MaxRequestBytes int      `json:"max_request_bytes"`
}

func (s *TariWatchStore) Capable() bool {
	data, err := os.ReadFile(s.capabilityPath)
	var capability tariWatchCapability
	if err != nil || !decodeStrictBytes(data, &capability) || capability.Schema != 1 || capability.MaxRequestBytes < 4096 {
		return false
	}
	age := s.now().Unix() - capability.UpdatedAt
	if age < -30 || age > 30 {
		return false
	}
	actions := make(map[string]bool, len(capability.Actions))
	for _, action := range capability.Actions {
		actions[action] = true
	}
	return actions["create"] && actions["remove"]
}

type tariWalletBasicAuth struct {
	user     string
	password string
}

func (a tariWalletBasicAuth) GetRequestMetadata(context.Context, ...string) (map[string]string, error) {
	value := base64.StdEncoding.EncodeToString([]byte(a.user + ":" + a.password))
	return map[string]string{"authorization": "Basic " + value}, nil
}

func (tariWalletBasicAuth) RequireTransportSecurity() bool { return false }
