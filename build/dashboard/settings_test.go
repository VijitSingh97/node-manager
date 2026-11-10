package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func settingsRequest(handler http.Handler, method, password, body string, csrf bool) *httptest.ResponseRecorder {
	return settingsRequestHost(handler, method, password, body, csrf, "mini-1.local")
}

func settingsRequestHost(handler http.Handler, method, password, body string, csrf bool, host string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "/api/settings", strings.NewReader(body))
	req.RemoteAddr = "192.168.1.20:12345"
	req.Host = host
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("X-Forwarded-Host", "mini-1.local")
	if password != "" {
		req.SetBasicAuth("admin", password)
	}
	if csrf {
		req.Header.Set("X-Requested-With", "fetch")
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	return w
}

func TestSettingsOnionHostRequiresAuthentication(t *testing.T) {
	onion := strings.Repeat("a", 56) + ".onion"
	body := `{"telegram_enabled":false,"telegram_chat_id":"","healthchecks_enabled":false,"alert_new_block":true}`
	a, handler := testApp(t, &rpcFixture{}, "")
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		got := settingsRequestHost(handler, method, "", body, true, onion)
		if got.Code != http.StatusUnauthorized {
			t.Fatalf("anonymous onion %s = %d: %s", method, got.Code, got.Body.String())
		}
	}
	if a.monitor.settingsOverridden {
		t.Fatal("rejected onion request changed settings")
	}
	_, protectedHandler := testApp(t, &rpcFixture{}, "dashboard-secret")
	if got := settingsRequestHost(protectedHandler, http.MethodGet, "dashboard-secret", "", false, onion); got.Code != http.StatusOK {
		t.Fatalf("authenticated onion GET = %d: %s", got.Code, got.Body.String())
	}
	if got := settingsRequestHost(protectedHandler, http.MethodPost, "dashboard-secret", body, true, onion); got.Code != http.StatusOK {
		t.Fatalf("authenticated onion POST = %d: %s", got.Code, got.Body.String())
	}
}

func TestSettingsAPIReturnsValuesAndRespectsConfiguredPassword(t *testing.T) {
	t.Setenv("TELEGRAM_BOT_TOKEN", "123:top-secret")
	t.Setenv("TELEGRAM_CHAT_ID", "-10042")
	t.Setenv("HEALTHCHECKS_URL", "https://hc-ping.com/private-uuid")
	t.Setenv("ALERT_NEW_BLOCK", "1")

	_, handler := testApp(t, &rpcFixture{}, "dashboard-secret")
	if got := settingsRequest(handler, http.MethodGet, "", "", false); got.Code != http.StatusUnauthorized || strings.Contains(got.Body.String(), "top-secret") || strings.Contains(got.Body.String(), "private-uuid") {
		t.Fatalf("unauthenticated GET = %d: %s", got.Code, got.Body.String())
	}
	if got := settingsRequest(handler, http.MethodGet, "wrong-password", "", false); got.Code != http.StatusUnauthorized || strings.Contains(got.Body.String(), "top-secret") || strings.Contains(got.Body.String(), "private-uuid") {
		t.Fatalf("wrong-password GET = %d: %s", got.Code, got.Body.String())
	}
	response := settingsRequest(handler, http.MethodGet, "dashboard-secret", "", false)
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("settings GET allows saved credentials to be cached")
	}
	if response.Code != http.StatusOK {
		t.Fatalf("GET status = %d: %s", response.Code, response.Body.String())
	}
	var view map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if view["telegram_enabled"] != true || view["telegram_bot_token"] != "123:top-secret" || view["telegram_chat_id"] != "-10042" || view["healthchecks_enabled"] != true || view["healthchecks_url"] != "https://hc-ping.com/private-uuid" || view["alert_new_block"] != true || view["tower_labels"] != false || view["default_currency"] != "USD" || view["overridden"] != false {
		t.Fatalf("unexpected settings view: %#v", view)
	}
	if view["can_configure"] != true {
		t.Fatalf("authenticated LAN settings view is not configurable: %#v", view)
	}
	if view["bitcoin_enabled"] != true || view["monero_enabled"] != false || view["alert_new_block_bitcoin"] != true || view["alert_new_block_monero"] != false {
		t.Fatalf("node-specific settings view: %#v", view)
	}
}

func TestSettingsAppearanceScopeOmitsNotificationCredentials(t *testing.T) {
	t.Setenv("TELEGRAM_BOT_TOKEN", "123:top-secret")
	t.Setenv("TELEGRAM_CHAT_ID", "-10042")
	t.Setenv("HEALTHCHECKS_URL", "https://hc-ping.com/private-uuid")
	_, handler := testApp(t, &rpcFixture{}, "dashboard-secret")
	req := httptest.NewRequest(http.MethodGet, "/api/settings?scope=appearance", nil)
	req.RemoteAddr = "192.168.1.20:12345"
	req.Host = "mini-1.local"
	req.SetBasicAuth("admin", "dashboard-secret")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("appearance GET = %d: %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "top-secret") || strings.Contains(w.Body.String(), "private-uuid") {
		t.Fatalf("appearance response leaked notification settings: %s", w.Body.String())
	}
	var view map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil || len(view) != 3 || view["tower_labels"] != false || view["computer_stats"] != false || view["default_currency"] != "USD" {
		t.Fatalf("appearance response = %#v, %v", view, err)
	}
}

func TestSettingsAuthenticatedLANWriteRequiresCSRFAndPersistsAcrossRestart(t *testing.T) {
	a, handler := testApp(t, &rpcFixture{}, "")
	body := `{"telegram_enabled":true,"telegram_bot_token":"123:test-secret","telegram_chat_id":"-10042","healthchecks_enabled":false,"alert_new_block":true,"tower_labels":true,"computer_stats":true,"default_currency":"CNY"}`
	if got := settingsRequest(handler, http.MethodPost, "dashboard-secret", body, false); got.Code != http.StatusForbidden {
		t.Fatalf("LAN POST without CSRF header = %d, want 403", got.Code)
	}
	if a.monitor.settingsOverridden {
		t.Fatal("rejected LAN POST changed settings")
	}
	post := settingsRequest(handler, http.MethodPost, "dashboard-secret", body, true)
	if post.Code != http.StatusOK {
		t.Fatalf("authenticated LAN POST = %d: %s", post.Code, post.Body.String())
	}
	if !strings.Contains(post.Body.String(), "test-secret") {
		t.Fatalf("authenticated LAN POST did not return the saved token: %s", post.Body.String())
	}

	root := filepath.Dir(sourceFile(t))
	restarted, err := newApp(a.cfg, a.rpc, filepath.Join(root, "index.html"), filepath.Join(root, "static"), log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatal(err)
	}
	get := settingsRequestHost(restarted.routes(), http.MethodGet, "dashboard-secret", "", false, "192.168.1.117")
	var view map[string]any
	if get.Code != http.StatusOK || json.Unmarshal(get.Body.Bytes(), &view) != nil {
		t.Fatalf("restarted LAN GET = %d %#v", get.Code, view)
	}
	if view["can_configure"] != true || view["overridden"] != true || view["telegram_bot_token"] != "123:test-secret" || view["alert_new_block"] != true || view["tower_labels"] != true || view["computer_stats"] != true || view["default_currency"] != "CNY" {
		t.Fatalf("restarted LAN settings view = %#v", view)
	}
}

func TestSettingsAnonymousPublicHostRejected(t *testing.T) {
	a, handler := testApp(t, &rpcFixture{}, "")
	body := `{"telegram_enabled":false,"telegram_chat_id":"","healthchecks_enabled":false,"alert_new_block":true}`
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		response := settingsRequestHost(handler, method, "", body, true, "attacker.example")
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("public-host LAN %s = %d: %s", method, response.Code, response.Body.String())
		}
	}
	if a.monitor.settingsOverridden {
		t.Fatal("forwarded local host bypassed the public-host guard")
	}
}

func TestSettingsConfiguredPasswordProtectsLANWrites(t *testing.T) {
	a, handler := testApp(t, &rpcFixture{}, "dashboard-secret")
	body := `{"telegram_enabled":false,"telegram_chat_id":"","healthchecks_enabled":false,"alert_new_block":true}`
	if got := settingsRequest(handler, http.MethodPost, "", body, true); got.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated LAN POST = %d, want 401", got.Code)
	}
	if got := settingsRequest(handler, http.MethodPost, "wrong-password", body, true); got.Code != http.StatusUnauthorized {
		t.Fatalf("wrong-password LAN POST = %d, want 401", got.Code)
	}
	if a.monitor.settingsOverridden {
		t.Fatal("rejected LAN POST changed settings")
	}
	if got := settingsRequestHost(handler, http.MethodPost, "dashboard-secret", body, true, "dashboard.example.com"); got.Code != http.StatusOK {
		t.Fatalf("authenticated custom-host LAN POST = %d: %s", got.Code, got.Body.String())
	}
}

func TestSettingsPersistRestartAndResetToServerConfig(t *testing.T) {
	t.Setenv("TELEGRAM_BOT_TOKEN", "123:server-token")
	t.Setenv("TELEGRAM_CHAT_ID", "server-chat")
	t.Setenv("HEALTHCHECKS_URL", "https://hc-ping.com/server-id")
	t.Setenv("ALERT_NEW_BLOCK", "0")
	stateDir := t.TempDir()
	m := NewMonitor("dev", t.TempDir(), stateDir)

	update := settingsUpdate{
		TelegramEnabled:     boolPointer(true),
		TelegramBotToken:    stringPointer("456:dashboard-token"),
		TelegramChatID:      stringPointer("dashboard-chat"),
		HealthchecksEnabled: boolPointer(true),
		HealthchecksURL:     stringPointer("https://hc-ping.com/dashboard-id/"),
		AlertNewBlock:       boolPointer(true),
		AlertNewBlockMonero: boolPointer(true),
		TowerLabels:         boolPointer(true),
		DefaultCurrency:     stringPointer("EUR"),
	}
	if err := m.updateSettings(update); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(stateDir, "notification_settings.json")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("settings mode = %o, want 600", info.Mode().Perm())
	}

	restarted := NewMonitor("dev", t.TempDir(), stateDir)
	got := restarted.notificationSettings()
	if got.TelegramBotToken != "456:dashboard-token" || got.TelegramChatID != "dashboard-chat" || got.HealthchecksURL != "https://hc-ping.com/dashboard-id" || !got.AlertNewBlock || !got.AlertNewBlockMonero || !got.TowerLabels || got.DefaultCurrency != "EUR" {
		t.Fatalf("restarted settings = %#v", got)
	}
	view, err := restarted.settingsView(true)
	if err != nil || view["overridden"] != true {
		t.Fatalf("restarted view = %#v, %v", view, err)
	}
	if err := restarted.updateSettings(settingsUpdate{
		TelegramEnabled: boolPointer(false), TelegramChatID: stringPointer(""),
		HealthchecksEnabled: boolPointer(false), AlertNewBlock: boolPointer(false),
	}); err != nil {
		t.Fatal(err)
	}
	if !restarted.notificationSettings().TowerLabels || !restarted.notificationSettings().AlertNewBlockMonero || restarted.notificationSettings().DefaultCurrency != "EUR" {
		t.Fatal("legacy settings update changed newer preferences")
	}

	if err := restarted.resetSettings(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("override still exists after reset: %v", err)
	}
	got = restarted.notificationSettings()
	if got.TelegramBotToken != "123:server-token" || got.TelegramChatID != "server-chat" || got.HealthchecksURL != "https://hc-ping.com/server-id" || got.AlertNewBlock {
		t.Fatalf("reset settings = %#v", got)
	}
}

func TestSettingsLoadOldOverrideDefaultsTowerLabelsOff(t *testing.T) {
	stateDir := t.TempDir()
	old := `{"telegram_enabled":false,"telegram_bot_token":"","telegram_chat_id":"","healthchecks_enabled":false,"healthchecks_url":"","alert_new_block":true}`
	if err := os.WriteFile(filepath.Join(stateDir, "notification_settings.json"), []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	m := NewMonitor("dev", t.TempDir(), stateDir)
	view, err := m.settingsView(true)
	if err != nil || view["overridden"] != true || view["alert_new_block"] != true || view["alert_new_block_bitcoin"] != true || view["alert_new_block_monero"] != false || view["tower_labels"] != false || view["default_currency"] != "USD" {
		t.Fatalf("old override view = %#v, %v", view, err)
	}
	for _, field := range []string{"alert_monitor_online", "alert_node_down", "alert_node_recovered", "alert_sync_complete", "alert_low_disk", "alert_updates"} {
		if view[field] != true {
			t.Fatalf("old override %s = %#v, want true", field, view[field])
		}
	}
	if view["node_down_after_minutes"] != 3 || view["low_disk_gib"] != 50 {
		t.Fatalf("old override thresholds = down %#v disk %#v", view["node_down_after_minutes"], view["low_disk_gib"])
	}
}

func TestGlobalAlertSettingsValidateAndPreserveScopedValues(t *testing.T) {
	m := NewMonitor("dev", t.TempDir(), t.TempDir())
	if err := m.updateSettingsScope(settingsUpdate{
		AlertLowDisk:         boolPointer(false),
		AlertNodeRecovered:   boolPointer(false),
		NodeDownAfterMinutes: intPointer(7),
		LowDiskGiB:           intPointer(25),
		present: map[string]bool{
			"alert_low_disk": true, "alert_node_recovered": true,
			"node_down_after_minutes": true, "low_disk_gib": true,
		},
	}, "global"); err != nil {
		t.Fatal(err)
	}
	if err := m.updateSettingsScope(settingsUpdate{
		AlertNewBlockMonero: boolPointer(true),
		present:             map[string]bool{"alert_new_block_monero": true},
	}, "nodes"); err != nil {
		t.Fatal(err)
	}
	got := m.notificationSettings()
	if got.AlertLowDisk || got.AlertNodeRecovered || got.NodeDownAfterMinutes != 7 || got.LowDiskGiB != 25 || !got.AlertNewBlockMonero || !got.AlertNodeDown || !got.AlertUpdates {
		t.Fatalf("scoped settings = %#v", got)
	}
	for _, test := range []settingsUpdate{
		{NodeDownAfterMinutes: intPointer(0), present: map[string]bool{"node_down_after_minutes": true}},
		{NodeDownAfterMinutes: intPointer(61), present: map[string]bool{"node_down_after_minutes": true}},
		{LowDiskGiB: intPointer(0), present: map[string]bool{"low_disk_gib": true}},
		{LowDiskGiB: intPointer(1_048_577), present: map[string]bool{"low_disk_gib": true}},
	} {
		if err := m.updateSettingsScope(test, "global"); !errors.Is(err, ErrSettingsInvalid) {
			t.Fatalf("invalid thresholds accepted: %#v: %v", test, err)
		}
	}
	if after := m.notificationSettings(); after != got {
		t.Fatalf("invalid update changed settings: %#v", after)
	}
}

func TestSettingsAPIValidationCSRFAndSecretReplacement(t *testing.T) {
	t.Setenv("TELEGRAM_BOT_TOKEN", "123:original-token")
	t.Setenv("TELEGRAM_CHAT_ID", "original-chat")
	t.Setenv("HEALTHCHECKS_URL", "https://hc-ping.com/original-id")
	a, handler := testApp(t, &rpcFixture{}, "dashboard-secret")

	valid := `{"telegram_enabled":true,"telegram_bot_token":"","telegram_chat_id":"new-chat","healthchecks_enabled":true,"healthchecks_url":"","alert_new_block":true}`
	if got := settingsRequest(handler, http.MethodPost, "dashboard-secret", valid, false); got.Code != http.StatusForbidden {
		t.Fatalf("POST without CSRF header = %d, want 403", got.Code)
	}
	response := settingsRequest(handler, http.MethodPost, "dashboard-secret", valid, true)
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("settings POST allows saved credentials to be cached")
	}
	if response.Code != http.StatusOK {
		t.Fatalf("valid POST = %d: %s", response.Code, response.Body.String())
	}
	var savedView map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &savedView); err != nil || savedView["overridden"] != true || savedView["telegram_chat_id"] != "new-chat" || savedView["telegram_bot_token"] != "123:original-token" || savedView["healthchecks_url"] != "https://hc-ping.com/original-id" || savedView["port_control"] == nil {
		t.Fatalf("POST response view = %#v, %v", savedView, err)
	}
	settings := a.monitor.notificationSettings()
	if settings.TelegramBotToken != "123:original-token" || settings.TelegramChatID != "new-chat" || settings.HealthchecksURL != "https://hc-ping.com/original-id" || !settings.AlertNewBlock {
		t.Fatalf("empty replacements did not keep secrets: %#v", settings)
	}

	invalid := []string{
		`{"telegram_enabled":true,"telegram_chat_id":"new-chat","healthchecks_enabled":true,"alert_new_block":true,"unexpected":1}`,
		`{"telegram_enabled":true,"telegram_bot_token":"bad token","telegram_chat_id":"new-chat","healthchecks_enabled":true,"healthchecks_url":"https://hc-ping.com/id","alert_new_block":true}`,
		`{"telegram_enabled":true,"telegram_bot_token":"ok","telegram_chat_id":"new-chat","healthchecks_enabled":true,"healthchecks_url":"http://hc-ping.com/id","alert_new_block":true}`,
		`{"telegram_enabled":true,"telegram_chat_id":"new-chat","healthchecks_enabled":true,"alert_new_block":true,"default_currency":"DOGE"}`,
		`{"reset":true,"alert_new_block":true}`,
		`{"reset":true,"alert_new_block":null}`,
		`{"reset":false}`,
	}
	for _, body := range invalid {
		if got := settingsRequest(handler, http.MethodPost, "dashboard-secret", body, true); got.Code != http.StatusBadRequest {
			t.Fatalf("invalid POST %s = %d, want 400", body, got.Code)
		}
	}

	disable := `{"telegram_enabled":false,"telegram_chat_id":"","healthchecks_enabled":false,"alert_new_block":false}`
	response = settingsRequest(handler, http.MethodPost, "dashboard-secret", disable, true)
	if response.Code != http.StatusOK {
		t.Fatalf("disable POST = %d: %s", response.Code, response.Body.String())
	}
	settings = a.monitor.notificationSettings()
	if settings.TelegramBotToken != "" || settings.TelegramChatID != "" || settings.HealthchecksURL != "" {
		t.Fatalf("disabled services retained credentials: %#v", settings)
	}
}

func TestSettingsFailureAndCorruptionLeaveNotificationsDisabled(t *testing.T) {
	t.Setenv("TELEGRAM_BOT_TOKEN", "123:server-token")
	t.Setenv("TELEGRAM_CHAT_ID", "server-chat")
	stateDir := t.TempDir()
	m := NewMonitor("dev", t.TempDir(), stateDir)
	before := m.notificationSettings()
	originalWriter := writeSettingsFile
	writeSettingsFile = func(string, []byte) error { return errors.New("disk full") }
	t.Cleanup(func() { writeSettingsFile = originalWriter })
	update := settingsUpdate{
		TelegramEnabled: boolPointer(false), TelegramChatID: stringPointer(""),
		HealthchecksEnabled: boolPointer(false), AlertNewBlock: boolPointer(true),
	}
	if err := m.updateSettings(update); !errors.Is(err, ErrSettingsUnavailable) {
		t.Fatalf("write failure = %v", err)
	}
	if got := m.notificationSettings(); got != before {
		t.Fatalf("active settings changed after failed write: %#v", got)
	}

	corruptDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(corruptDir, "notification_settings.json"), []byte(`{"telegram_enabled":true,"telegram_bot_token":"secret"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	corrupt := NewMonitor("dev", t.TempDir(), corruptDir)
	if _, err := corrupt.settingsView(true); !errors.Is(err, ErrSettingsUnavailable) {
		t.Fatalf("corrupt settings status = %v", err)
	}
	if got := corrupt.notificationSettings(); got != (notificationSettings{}) {
		t.Fatalf("corrupt settings did not fail closed: %#v", got)
	}
}

func TestCorruptSettingsLogRedactedStartupWarning(t *testing.T) {
	stateDir := t.TempDir()
	settingsPath := filepath.Join(stateDir, "notification_settings.json")
	if err := os.WriteFile(settingsPath, []byte(`{"telegram_bot_token":"top-secret"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	root := filepath.Dir(sourceFile(t))
	var logs strings.Builder
	cfg := config{dashboardPassword: "dashboard-secret", stateDir: stateDir, dataDir: t.TempDir(), version: "dev"}
	if _, err := newApp(cfg, &rpcClient{}, filepath.Join(root, "index.html"), filepath.Join(root, "static"), log.New(&logs, "", 0)); err != nil {
		t.Fatal(err)
	}
	got := logs.String()
	if !strings.Contains(got, "notification settings unavailable; alerts disabled") {
		t.Fatalf("startup warning missing: %q", got)
	}
	if strings.Contains(got, "top-secret") || strings.Contains(got, settingsPath) {
		t.Fatalf("startup warning leaked settings details: %q", got)
	}
}

func TestSettingsResetFailureKeepsActiveOverride(t *testing.T) {
	t.Setenv("TELEGRAM_BOT_TOKEN", "123:server-token")
	t.Setenv("TELEGRAM_CHAT_ID", "server-chat")
	m := NewMonitor("dev", t.TempDir(), t.TempDir())
	if err := m.updateSettings(settingsUpdate{
		TelegramEnabled: boolPointer(false), TelegramChatID: stringPointer(""),
		HealthchecksEnabled: boolPointer(false), AlertNewBlock: boolPointer(true),
	}); err != nil {
		t.Fatal(err)
	}
	originalRemover := removeSettingsFile
	removeSettingsFile = func(string) error { return errors.New("read-only filesystem") }
	t.Cleanup(func() { removeSettingsFile = originalRemover })
	if err := m.resetSettings(); !errors.Is(err, ErrSettingsUnavailable) {
		t.Fatalf("reset failure = %v", err)
	}
	settings := m.notificationSettings()
	if !settings.AlertNewBlock || !m.settingsOverridden {
		t.Fatalf("failed reset changed active settings: %#v", settings)
	}
}

func TestSettingsLiveReadsAreRaceSafe(t *testing.T) {
	t.Setenv("TELEGRAM_BOT_TOKEN", "")
	m := NewMonitor("dev", t.TempDir(), t.TempDir())
	m.output = io.Discard
	m.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return response(http.StatusOK, `{"ok":true}`), nil
	})}
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Add(3)
		go func() {
			defer wg.Done()
			m.sendTelegram(t.Context(), "test")
			_ = m.pingHealthchecks(t.Context(), true)
		}()
		go func() {
			defer wg.Done()
			_ = m.notificationSettings()
		}()
		go func(enabled bool) {
			defer wg.Done()
			_ = m.updateSettings(settingsUpdate{
				TelegramEnabled: boolPointer(false), TelegramChatID: stringPointer(""),
				HealthchecksEnabled: boolPointer(false), AlertNewBlock: boolPointer(enabled),
			})
		}(i%2 == 0)
	}
	wg.Wait()
}

func TestSettingsBodyLimit(t *testing.T) {
	_, handler := testApp(t, &rpcFixture{}, "dashboard-secret")
	body := bytes.Repeat([]byte("x"), maxSettingsBody+1)
	req := httptest.NewRequest(http.MethodPost, "/api/settings", bytes.NewReader(body))
	req.RemoteAddr = "127.0.0.1:12345"
	req.SetBasicAuth("admin", "dashboard-secret")
	req.Header.Set("X-Requested-With", "fetch")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("oversized body = %d, want 400", w.Code)
	}
}

func boolPointer(value bool) *bool       { return &value }
func stringPointer(value string) *string { return &value }
func intPointer(value int) *int          { return &value }

func TestThermalThrottleSettingRoundTrips(t *testing.T) {
	m := NewMonitor("dev", t.TempDir(), t.TempDir())
	if m.notificationSettings().ThermalThrottle {
		t.Fatal("thermal throttle should default off")
	}
	if err := m.updateSettingsScope(settingsUpdate{
		ThermalThrottle: boolPointer(true),
		present:         map[string]bool{"thermal_throttle": true},
	}, "global"); err != nil {
		t.Fatal(err)
	}
	if !m.notificationSettings().ThermalThrottle {
		t.Fatal("thermal throttle did not persist")
	}
	view, err := m.settingsView(true)
	if err != nil || view["thermal_throttle"] != true {
		t.Fatalf("view thermal_throttle = %#v (%v)", view["thermal_throttle"], err)
	}
	saved, err := os.ReadFile(m.settingsPath)
	if err != nil || !strings.Contains(string(saved), `"thermal_throttle":true`) {
		t.Fatalf("settings file missing thermal_throttle: %v %s", err, saved)
	}
}
