package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strings"
	"unicode"
)

const maxSettingsBody = 16 << 10

const defaultPriceCurrency = "USD"

const (
	defaultNodeDownAfterMinutes = 3
	defaultLowDiskGiB           = 50
)

var allowedPriceCurrencies = map[string]bool{
	"USD": true, "EUR": true, "GBP": true, "INR": true, "CAD": true,
	"AUD": true, "JPY": true, "RUB": true, "CNY": true,
}

var (
	ErrSettingsUnavailable = errors.New("notification settings unavailable")
	ErrSettingsInvalid     = errors.New("invalid notification settings")
	writeSettingsFile      = writePrivateFileAtomic
	removeSettingsFile     = os.Remove
)

type notificationSettings struct {
	TelegramEnabled          bool   `json:"telegram_enabled"`
	TelegramBotToken         string `json:"telegram_bot_token"`
	TelegramChatID           string `json:"telegram_chat_id"`
	HealthchecksEnabled      bool   `json:"healthchecks_enabled"`
	HealthchecksURL          string `json:"healthchecks_url"`
	AlertNewBlock            bool   `json:"alert_new_block"`
	AlertNewBlockMonero      bool   `json:"alert_new_block_monero"`
	AlertNewBlockTari        bool   `json:"alert_new_block_tari"`
	AlertNewBlockLitecoin    bool   `json:"alert_new_block_litecoin"`
	AlertNewBlockDogecoin    bool   `json:"alert_new_block_dogecoin"`
	AlertNewBlockBitcoinCash bool   `json:"alert_new_block_bitcoincash"`
	AlertNewBlockZcash       bool   `json:"alert_new_block_zcash"`
	AlertMonitorOnline       bool   `json:"alert_monitor_online"`
	AlertNodeDown            bool   `json:"alert_node_down"`
	AlertNodeRecovered       bool   `json:"alert_node_recovered"`
	AlertSyncComplete        bool   `json:"alert_sync_complete"`
	AlertLowDisk             bool   `json:"alert_low_disk"`
	AlertUpdates             bool   `json:"alert_updates"`
	NodeDownAfterMinutes     int    `json:"node_down_after_minutes"`
	LowDiskGiB               int    `json:"low_disk_gib"`
	TowerLabels              bool   `json:"tower_labels"`
	ComputerStats            bool   `json:"computer_stats"`
	DefaultCurrency          string `json:"default_currency"`
}

type settingsUpdate struct {
	Reset                    *bool   `json:"reset,omitempty"`
	TelegramEnabled          *bool   `json:"telegram_enabled,omitempty"`
	TelegramBotToken         *string `json:"telegram_bot_token,omitempty"`
	TelegramChatID           *string `json:"telegram_chat_id,omitempty"`
	HealthchecksEnabled      *bool   `json:"healthchecks_enabled,omitempty"`
	HealthchecksURL          *string `json:"healthchecks_url,omitempty"`
	AlertNewBlock            *bool   `json:"alert_new_block,omitempty"`
	AlertNewBlockMonero      *bool   `json:"alert_new_block_monero,omitempty"`
	AlertNewBlockTari        *bool   `json:"alert_new_block_tari,omitempty"`
	AlertNewBlockLitecoin    *bool   `json:"alert_new_block_litecoin,omitempty"`
	AlertNewBlockDogecoin    *bool   `json:"alert_new_block_dogecoin,omitempty"`
	AlertNewBlockBitcoinCash *bool   `json:"alert_new_block_bitcoincash,omitempty"`
	AlertNewBlockZcash       *bool   `json:"alert_new_block_zcash,omitempty"`
	AlertMonitorOnline       *bool   `json:"alert_monitor_online,omitempty"`
	AlertNodeDown            *bool   `json:"alert_node_down,omitempty"`
	AlertNodeRecovered       *bool   `json:"alert_node_recovered,omitempty"`
	AlertSyncComplete        *bool   `json:"alert_sync_complete,omitempty"`
	AlertLowDisk             *bool   `json:"alert_low_disk,omitempty"`
	AlertUpdates             *bool   `json:"alert_updates,omitempty"`
	NodeDownAfterMinutes     *int    `json:"node_down_after_minutes,omitempty"`
	LowDiskGiB               *int    `json:"low_disk_gib,omitempty"`
	TowerLabels              *bool   `json:"tower_labels,omitempty"`
	ComputerStats            *bool   `json:"computer_stats,omitempty"`
	DefaultCurrency          *string `json:"default_currency,omitempty"`
	present                  map[string]bool
}

func notificationSettingsFromEnv() notificationSettings {
	token := strings.TrimSpace(os.Getenv("TELEGRAM_BOT_TOKEN"))
	chatID := strings.TrimSpace(os.Getenv("TELEGRAM_CHAT_ID"))
	healthchecks := strings.TrimRight(strings.TrimSpace(os.Getenv("HEALTHCHECKS_URL")), "/")
	return notificationSettings{
		TelegramEnabled:          token != "" && chatID != "",
		TelegramBotToken:         token,
		TelegramChatID:           chatID,
		HealthchecksEnabled:      healthchecks != "",
		HealthchecksURL:          healthchecks,
		AlertNewBlock:            enabled(os.Getenv("ALERT_NEW_BLOCK")),
		AlertNewBlockMonero:      enabled(os.Getenv("ALERT_NEW_BLOCK_MONERO")),
		AlertNewBlockTari:        enabled(os.Getenv("ALERT_NEW_BLOCK_TARI")),
		AlertNewBlockLitecoin:    enabled(os.Getenv("ALERT_NEW_BLOCK_LITECOIN")),
		AlertNewBlockDogecoin:    enabled(os.Getenv("ALERT_NEW_BLOCK_DOGECOIN")),
		AlertNewBlockBitcoinCash: enabled(os.Getenv("ALERT_NEW_BLOCK_BITCOINCASH")),
		AlertNewBlockZcash:       enabled(os.Getenv("ALERT_NEW_BLOCK_ZCASH")),
		AlertMonitorOnline:       true,
		AlertNodeDown:            true,
		AlertNodeRecovered:       true,
		AlertSyncComplete:        true,
		AlertLowDisk:             true,
		AlertUpdates:             true,
		NodeDownAfterMinutes:     defaultNodeDownAfterMinutes,
		LowDiskGiB:               defaultLowDiskGiB,
		DefaultCurrency:          defaultPriceCurrency,
	}
}

func loadNotificationSettings(path string, baseline notificationSettings) (notificationSettings, bool, error) {
	if err := os.Chmod(path, 0o600); err != nil {
		if os.IsNotExist(err) {
			return baseline, false, nil
		}
		return notificationSettings{}, false, err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return notificationSettings{}, false, err
	}
	settings := notificationSettings{
		AlertMonitorOnline:   true,
		AlertNodeDown:        true,
		AlertNodeRecovered:   true,
		AlertSyncComplete:    true,
		AlertLowDisk:         true,
		AlertUpdates:         true,
		NodeDownAfterMinutes: defaultNodeDownAfterMinutes,
		LowDiskGiB:           defaultLowDiskGiB,
		DefaultCurrency:      defaultPriceCurrency,
	}
	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&settings); err != nil {
		return notificationSettings{}, false, err
	}
	if err := ensureJSONEnd(decoder); err != nil {
		return notificationSettings{}, false, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(b, &fields); err != nil {
		return notificationSettings{}, false, err
	}
	for _, name := range []string{"telegram_enabled", "telegram_bot_token", "telegram_chat_id", "healthchecks_enabled", "healthchecks_url", "alert_new_block"} {
		value, ok := fields[name]
		if !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return notificationSettings{}, false, errors.New("notification settings field missing")
		}
	}
	if err := validateStoredSettings(settings); err != nil {
		return notificationSettings{}, false, err
	}
	return settings, true, nil
}

func validateStoredSettings(settings notificationSettings) error {
	if !allowedPriceCurrencies[settings.DefaultCurrency] {
		return errors.New("invalid default currency")
	}
	if settings.NodeDownAfterMinutes < 1 || settings.NodeDownAfterMinutes > 60 {
		return errors.New("node down delay must be between 1 and 60 minutes")
	}
	if settings.LowDiskGiB < 1 || settings.LowDiskGiB > 1_048_576 {
		return errors.New("low disk threshold must be between 1 and 1048576 GiB")
	}
	if settings.TelegramEnabled {
		if err := validateSecret(settings.TelegramBotToken, 10, 256); err != nil {
			return err
		}
		if err := validateSecret(settings.TelegramChatID, 1, 100); err != nil {
			return err
		}
	} else if settings.TelegramBotToken != "" || settings.TelegramChatID != "" {
		return errors.New("disabled Telegram settings contain credentials")
	}
	if settings.HealthchecksEnabled {
		return validateHealthchecksURL(settings.HealthchecksURL)
	}
	if settings.HealthchecksURL != "" {
		return errors.New("disabled Healthchecks settings contain a URL")
	}
	return nil
}

func validateSecret(value string, min, max int) error {
	if len(value) < min || len(value) > max || strings.TrimSpace(value) != value {
		return errors.New("invalid value length")
	}
	for _, r := range value {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return errors.New("invalid control or whitespace character")
		}
	}
	return nil
}

func validateHealthchecksURL(value string) error {
	if len(value) > 2048 || strings.TrimSpace(value) != value {
		return errors.New("invalid Healthchecks URL")
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return errors.New("invalid Healthchecks URL")
		}
	}
	endpoint, err := url.Parse(value)
	if err != nil || endpoint.Scheme != "https" || endpoint.Hostname() == "" || endpoint.User != nil || endpoint.Fragment != "" {
		return errors.New("Healthchecks URL must be HTTPS without credentials or a fragment")
	}
	return nil
}

func decodeSettingsUpdate(body io.Reader) (settingsUpdate, error) {
	var update settingsUpdate
	b, err := io.ReadAll(body)
	if err != nil {
		return update, err
	}
	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&update); err != nil {
		return update, err
	}
	if err := ensureJSONEnd(decoder); err != nil {
		return update, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(b, &fields); err != nil {
		return update, err
	}
	update.present = make(map[string]bool, len(fields))
	for name, value := range fields {
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return settingsUpdate{}, errors.New("null settings field")
		}
		update.present[name] = true
	}
	return update, nil
}

func (update settingsUpdate) isReset() bool {
	return update.Reset != nil && *update.Reset && len(update.present) == 1 && update.present["reset"]
}

func (update settingsUpdate) apply(current notificationSettings) (notificationSettings, error) {
	if update.Reset != nil || update.TelegramEnabled == nil || update.TelegramChatID == nil || update.HealthchecksEnabled == nil || update.AlertNewBlock == nil {
		return notificationSettings{}, errors.New("all settings fields are required")
	}
	next := current
	next.TelegramEnabled = *update.TelegramEnabled
	next.HealthchecksEnabled = *update.HealthchecksEnabled
	next.AlertNewBlock = *update.AlertNewBlock
	if update.AlertNewBlockMonero != nil {
		next.AlertNewBlockMonero = *update.AlertNewBlockMonero
	}
	if update.AlertNewBlockTari != nil {
		next.AlertNewBlockTari = *update.AlertNewBlockTari
	}
	if update.AlertNewBlockLitecoin != nil {
		next.AlertNewBlockLitecoin = *update.AlertNewBlockLitecoin
	}
	if update.AlertNewBlockDogecoin != nil {
		next.AlertNewBlockDogecoin = *update.AlertNewBlockDogecoin
	}
	if update.AlertNewBlockBitcoinCash != nil {
		next.AlertNewBlockBitcoinCash = *update.AlertNewBlockBitcoinCash
	}
	if update.AlertNewBlockZcash != nil {
		next.AlertNewBlockZcash = *update.AlertNewBlockZcash
	}
	if update.AlertMonitorOnline != nil {
		next.AlertMonitorOnline = *update.AlertMonitorOnline
	}
	if update.AlertNodeDown != nil {
		next.AlertNodeDown = *update.AlertNodeDown
	}
	if update.AlertNodeRecovered != nil {
		next.AlertNodeRecovered = *update.AlertNodeRecovered
	}
	if update.AlertSyncComplete != nil {
		next.AlertSyncComplete = *update.AlertSyncComplete
	}
	if update.AlertLowDisk != nil {
		next.AlertLowDisk = *update.AlertLowDisk
	}
	if update.AlertUpdates != nil {
		next.AlertUpdates = *update.AlertUpdates
	}
	if update.NodeDownAfterMinutes != nil {
		next.NodeDownAfterMinutes = *update.NodeDownAfterMinutes
	}
	if update.LowDiskGiB != nil {
		next.LowDiskGiB = *update.LowDiskGiB
	}
	if update.TowerLabels != nil {
		next.TowerLabels = *update.TowerLabels
	}
	if update.ComputerStats != nil {
		next.ComputerStats = *update.ComputerStats
	}
	if update.DefaultCurrency != nil {
		next.DefaultCurrency = *update.DefaultCurrency
	}
	if next.TelegramEnabled {
		if update.TelegramBotToken != nil && *update.TelegramBotToken != "" {
			next.TelegramBotToken = *update.TelegramBotToken
		}
		next.TelegramChatID = *update.TelegramChatID
	} else {
		next.TelegramBotToken = ""
		next.TelegramChatID = ""
	}
	if next.HealthchecksEnabled {
		if update.HealthchecksURL != nil && *update.HealthchecksURL != "" {
			next.HealthchecksURL = strings.TrimRight(*update.HealthchecksURL, "/")
		}
	} else {
		next.HealthchecksURL = ""
	}
	if err := validateStoredSettings(next); err != nil {
		return notificationSettings{}, err
	}
	return next, nil
}

func (m *Monitor) notificationSettings() notificationSettings {
	m.settingsMu.RLock()
	defer m.settingsMu.RUnlock()
	return m.settings
}

func (m *Monitor) settingsView(canConfigure bool) (map[string]any, error) {
	m.settingsMu.RLock()
	defer m.settingsMu.RUnlock()
	if m.settingsErr != nil {
		return nil, ErrSettingsUnavailable
	}
	return map[string]any{
		"telegram_enabled":            m.settings.TelegramEnabled,
		"telegram_bot_token":          m.settings.TelegramBotToken,
		"telegram_chat_id":            m.settings.TelegramChatID,
		"healthchecks_enabled":        m.settings.HealthchecksEnabled,
		"healthchecks_url":            m.settings.HealthchecksURL,
		"alert_new_block":             m.settings.AlertNewBlock,
		"alert_new_block_bitcoin":     m.settings.AlertNewBlock,
		"alert_new_block_monero":      m.settings.AlertNewBlockMonero,
		"alert_new_block_tari":        m.settings.AlertNewBlockTari,
		"alert_new_block_litecoin":    m.settings.AlertNewBlockLitecoin,
		"alert_new_block_dogecoin":    m.settings.AlertNewBlockDogecoin,
		"alert_new_block_bitcoincash": m.settings.AlertNewBlockBitcoinCash,
		"alert_new_block_zcash":       m.settings.AlertNewBlockZcash,
		"alert_monitor_online":        m.settings.AlertMonitorOnline,
		"alert_node_down":             m.settings.AlertNodeDown,
		"alert_node_recovered":        m.settings.AlertNodeRecovered,
		"alert_sync_complete":         m.settings.AlertSyncComplete,
		"alert_low_disk":              m.settings.AlertLowDisk,
		"alert_updates":               m.settings.AlertUpdates,
		"node_down_after_minutes":     m.settings.NodeDownAfterMinutes,
		"low_disk_gib":                m.settings.LowDiskGiB,
		"bitcoin_enabled":             m.bitcoinEnabled,
		"monero_enabled":              m.moneroEnabled,
		"tari_enabled":                m.extraEnabled[nodeTari],
		"litecoin_enabled":            m.extraEnabled[nodeLitecoin],
		"dogecoin_enabled":            m.extraEnabled[nodeDogecoin],
		"bitcoincash_enabled":         m.extraEnabled[nodeBitcoinCash],
		"zcash_enabled":               m.extraEnabled[nodeZcash],
		"tower_labels":                m.settings.TowerLabels,
		"computer_stats":              m.settings.ComputerStats,
		"default_currency":            m.settings.DefaultCurrency,
		"overridden":                  m.settingsOverridden,
		"can_configure":               canConfigure,
	}, nil
}

func (m *Monitor) updateSettings(update settingsUpdate) error {
	return m.updateSettingsScope(update, "")
}

func (m *Monitor) updateSettingsScope(update settingsUpdate, scope string) error {
	m.settingsMu.Lock()
	defer m.settingsMu.Unlock()
	if m.settingsErr != nil {
		return ErrSettingsUnavailable
	}
	if scope != "" {
		if scope != "global" && scope != "nodes" || len(update.present) == 0 {
			return ErrSettingsInvalid
		}
		for field := range update.present {
			isAlert := field == "alert_new_block" || strings.HasPrefix(field, "alert_new_block_")
			if isAlert != (scope == "nodes") || field == "reset" {
				return ErrSettingsInvalid
			}
		}
		if update.TelegramEnabled == nil {
			update.TelegramEnabled = &m.settings.TelegramEnabled
		}
		if update.TelegramChatID == nil {
			update.TelegramChatID = &m.settings.TelegramChatID
		}
		if update.HealthchecksEnabled == nil {
			update.HealthchecksEnabled = &m.settings.HealthchecksEnabled
		}
		if update.AlertNewBlock == nil {
			update.AlertNewBlock = &m.settings.AlertNewBlock
		}
	}
	next, err := update.apply(m.settings)
	if err != nil {
		return errors.Join(ErrSettingsInvalid, err)
	}
	b, err := json.Marshal(next)
	if err != nil {
		return ErrSettingsUnavailable
	}
	if err := writeSettingsFile(m.settingsPath, b); err != nil {
		return ErrSettingsUnavailable
	}
	m.settings = next
	m.settingsOverridden = true
	return nil
}

func (m *Monitor) resetSettings() error {
	m.settingsMu.Lock()
	defer m.settingsMu.Unlock()
	if m.settingsErr != nil {
		return ErrSettingsUnavailable
	}
	if err := removeSettingsFile(m.settingsPath); err != nil && !os.IsNotExist(err) {
		return ErrSettingsUnavailable
	}
	m.settings = m.settingsBaseline
	m.settingsOverridden = false
	return nil
}

func validSettingsDNSName(host string) bool {
	if len(host) > 253 {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, character := range label {
			if character != '-' && (character < 'a' || character > 'z') && (character < '0' || character > '9') {
				return false
			}
		}
	}
	return true
}

func (a *app) rejectUnsafeSettingsHost(w http.ResponseWriter, r *http.Request) bool {
	if a.configurationAllowed(r) {
		return false
	}
	http.Error(w, "Sign-in required to configure this node set", http.StatusForbidden)
	return true
}

func (a *app) configurationAllowed(r *http.Request) bool {
	method, _ := r.Context().Value(dashboardAuthMethod{}).(string)
	return method == "session" || method == "basic"
}

func privateRemoteAllowed(remote string) bool {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		return false
	}
	address, err := netip.ParseAddr(host)
	return err == nil && (address.IsLoopback() || address.IsPrivate())
}

func (a *app) apiSettingsGet(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if a.rejectUnsafeSettingsHost(w, r) {
		return
	}
	view, err := a.monitor.settingsView(true)
	if err != nil {
		http.Error(w, "notification settings unavailable", http.StatusServiceUnavailable)
		return
	}
	if r.URL.Query().Get("scope") == "appearance" {
		writeJSON(w, map[string]any{
			"tower_labels":     view["tower_labels"],
			"computer_stats":   view["computer_stats"],
			"default_currency": view["default_currency"],
		})
		return
	}
	view["port_control"] = a.portControlView()
	view["dashboard_password_set"] = a.hasDashboardPassword()
	if r.URL.Query().Get("scope") == "nodes" {
		delete(view, "telegram_bot_token")
		delete(view, "telegram_chat_id")
		delete(view, "healthchecks_url")
	}
	writeJSON(w, view)
}

func (a *app) apiSettingsPost(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if a.rejectUnsafeSettingsHost(w, r) {
		return
	}
	if rejectCredentialCSRF(w, r) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxSettingsBody)
	update, err := decodeSettingsUpdate(r.Body)
	if err != nil {
		http.Error(w, "invalid notification settings", http.StatusBadRequest)
		return
	}
	if update.Reset != nil {
		if !update.isReset() {
			http.Error(w, "reset cannot be combined with other settings", http.StatusBadRequest)
			return
		}
		err = a.monitor.resetSettings()
	} else {
		err = a.monitor.updateSettingsScope(update, r.URL.Query().Get("scope"))
	}
	if errors.Is(err, ErrSettingsInvalid) {
		http.Error(w, "invalid notification settings", http.StatusBadRequest)
		return
	}
	if err != nil {
		http.Error(w, "notification settings unavailable", http.StatusServiceUnavailable)
		return
	}
	view, err := a.monitor.settingsView(true)
	if err != nil {
		http.Error(w, "notification settings unavailable", http.StatusServiceUnavailable)
		return
	}
	view["port_control"] = a.portControlView()
	view["dashboard_password_set"] = a.hasDashboardPassword()
	if r.URL.Query().Get("scope") == "nodes" {
		delete(view, "telegram_bot_token")
		delete(view, "telegram_chat_id")
		delete(view, "healthchecks_url")
	}
	writeJSON(w, view)
}
