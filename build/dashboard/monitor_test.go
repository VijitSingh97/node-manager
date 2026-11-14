package main

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func response(code int, body string) *http.Response {
	return &http.Response{
		StatusCode: code,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}

func testFreshTorClient() *http.Client {
	return &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("fresh circuits unavailable in tests")
	})}
}

func configuredMonitor(t *testing.T, handler roundTripFunc) *Monitor {
	t.Helper()
	t.Setenv("TELEGRAM_BOT_TOKEN", "123:secret")
	t.Setenv("TELEGRAM_CHAT_ID", "42")
	t.Setenv("NODE_NAME", "alice")
	t.Setenv("ALERT_NEW_BLOCK", "1")
	t.Setenv("HEALTHCHECKS_URL", "")
	m := NewMonitor("1.2.0", t.TempDir(), t.TempDir())
	m.client = &http.Client{Transport: handler}
	m.freshTorClient = testFreshTorClient
	m.diskFreeGB = func(string) (float64, error) { return 1000, nil }
	m.output = io.Discard
	return m
}

func TestMonitorStateTransitionsAndExactMessages(t *testing.T) {
	var messages []string
	m := configuredMonitor(t, func(r *http.Request) (*http.Response, error) {
		var payload struct {
			ChatID string `json:"chat_id"`
			Text   string `json:"text"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload.ChatID != "42" {
			t.Fatalf("chat id = %q", payload.ChatID)
		}
		messages = append(messages, payload.Text)
		return response(http.StatusOK, `{"ok":true}`), nil
	})

	m.tick(t.Context(), map[string]any{"initialblockdownload": true, "blocks": float64(100)}, nil)
	m.tick(t.Context(), nil, nil)
	m.tick(t.Context(), nil, nil)
	m.tick(t.Context(), nil, nil)
	m.tick(t.Context(), map[string]any{"initialblockdownload": true, "blocks": float64(200)}, nil)
	m.tick(t.Context(), map[string]any{"initialblockdownload": false, "blocks": float64(900000)}, nil)
	m.tick(t.Context(), map[string]any{"initialblockdownload": false, "blocks": float64(900001)}, nil)

	want := []string{
		"[alice] ✅ initial sync complete at height 900000",
		"[alice] 🟧 new block 900001",
	}
	if strings.Join(messages, "\n") != strings.Join(want, "\n") {
		t.Fatalf("messages:\n%s\nwant:\n%s", strings.Join(messages, "\n"), strings.Join(want, "\n"))
	}
}

func TestMultiNodeAlertsKeepIndependentState(t *testing.T) {
	var messages []string
	m := configuredMonitor(t, func(r *http.Request) (*http.Response, error) {
		var payload struct {
			Text string `json:"text"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		messages = append(messages, payload.Text)
		return response(http.StatusOK, `{"ok":true}`), nil
	})
	m.configureNodes(true, true, t.TempDir())
	m.settings.AlertNewBlock = false
	m.healthTicks = 1
	synced := map[string]any{"initialblockdownload": false, "blocks": float64(100)}
	m.tickNodes(t.Context(), synced, synced)
	for range downAfterTicks {
		m.tickNodes(t.Context(), nil, synced)
	}
	m.tickNodes(t.Context(), synced, synced)

	want := []string{
		"[alice] Bitcoin: 🔴 node down — RPC unreachable for 3 minutes",
		"[alice] Bitcoin: 🟢 node recovered — RPC is answering again",
	}
	if strings.Join(messages, "\n") != strings.Join(want, "\n") {
		t.Fatalf("messages:\n%s\nwant:\n%s", strings.Join(messages, "\n"), strings.Join(want, "\n"))
	}
	if m.moneroState.fails != 0 || m.moneroState.downAlerted {
		t.Fatalf("Bitcoin failure changed Monero state: %#v", m.moneroState)
	}
}

func TestHealthchecksStayLivenessOnlyWhileANodeIsDown(t *testing.T) {
	var pings []string
	m := configuredMonitor(t, func(r *http.Request) (*http.Response, error) {
		pings = append(pings, r.URL.Path)
		return response(http.StatusOK, "ok"), nil
	})
	m.configureNodes(true, true, t.TempDir())
	m.settings.TelegramEnabled = false
	m.settings.HealthchecksEnabled = true
	m.settings.HealthchecksURL = "https://hc-ping.com/shared-check"
	m.healthTicks = 1
	m.healthPingDue = 0
	synced := map[string]any{"initialblockdownload": false, "blocks": float64(100)}
	m.tickNodes(t.Context(), synced, synced)
	for range downAfterTicks {
		m.healthPingDue = 0
		m.tickNodes(t.Context(), synced, nil)
	}

	want := []string{"/shared-check", "/shared-check", "/shared-check", "/shared-check"}
	if strings.Join(pings, "\n") != strings.Join(want, "\n") {
		t.Fatalf("Healthchecks pings = %#v, want %#v", pings, want)
	}
}

func TestMultiNodeIgnoresDisabledNodesAndSuppressesSyncFailures(t *testing.T) {
	var telegrams int
	m := configuredMonitor(t, func(r *http.Request) (*http.Response, error) {
		telegrams++
		return response(http.StatusOK, `{"ok":true}`), nil
	})
	m.healthTicks = 1
	m.configureNodes(true, false, t.TempDir())
	syncing := map[string]any{"initialblockdownload": true, "blocks": float64(100)}
	m.tickNodes(t.Context(), syncing, nil)
	for range downAfterTicks {
		m.tickNodes(t.Context(), nil, nil)
	}
	if telegrams != 0 || m.moneroState.fails != 0 {
		t.Fatalf("syncing or disabled node produced alerts=%d, Monero state=%#v", telegrams, m.moneroState)
	}
}

func TestMultiNodeNewBlockChoicesAreIndependent(t *testing.T) {
	var messages []string
	m := configuredMonitor(t, func(r *http.Request) (*http.Response, error) {
		var payload struct {
			Text string `json:"text"`
		}
		_ = json.NewDecoder(r.Body).Decode(&payload)
		messages = append(messages, payload.Text)
		return response(http.StatusOK, `{"ok":true}`), nil
	})
	m.configureNodes(true, true, t.TempDir())
	m.settings.AlertNewBlock = false
	m.settings.AlertNewBlockMonero = true
	m.healthTicks = 1
	m.tickNodes(t.Context(), map[string]any{"initialblockdownload": false, "blocks": float64(100)}, map[string]any{"initialblockdownload": false, "blocks": float64(100)})
	m.tickNodes(t.Context(), map[string]any{"initialblockdownload": false, "blocks": float64(101)}, map[string]any{"initialblockdownload": false, "blocks": float64(101)})
	if len(messages) != 1 || messages[0] != "[alice] Monero: 🟧 new block 101" {
		t.Fatalf("new-block messages = %#v", messages)
	}
}

func TestMultiNodeSyncAndDiskAlertsIdentifyMonero(t *testing.T) {
	var messages []string
	m := configuredMonitor(t, func(r *http.Request) (*http.Response, error) {
		var payload struct {
			Text string `json:"text"`
		}
		_ = json.NewDecoder(r.Body).Decode(&payload)
		messages = append(messages, payload.Text)
		return response(http.StatusOK, `{"ok":true}`), nil
	})
	moneroDir := t.TempDir()
	m.configureNodes(true, true, moneroDir)
	m.settings.AlertNewBlock = false
	m.healthTicks = 1
	m.diskFreeGB = func(path string) (float64, error) {
		if path == moneroDir {
			return 40, nil
		}
		return 1000, nil
	}
	bitcoin := map[string]any{"initialblockdownload": false, "blocks": float64(900000)}
	m.tickNodes(t.Context(), bitcoin, map[string]any{"initialblockdownload": true, "blocks": float64(100)})
	m.tickNodes(t.Context(), bitcoin, map[string]any{"initialblockdownload": false, "blocks": float64(200)})
	want := []string{
		"[alice] Monero: ✅ initial sync complete at height 200",
		"[alice] Monero: 💾 disk low: 40 GiB free",
	}
	if strings.Join(messages, "\n") != strings.Join(want, "\n") {
		t.Fatalf("messages = %#v, want %#v", messages, want)
	}
}

func TestSyncCompleteIgnoresBriefSyncFlapsAfterSync(t *testing.T) {
	var messages []string
	m := configuredMonitor(t, func(r *http.Request) (*http.Response, error) {
		var payload struct {
			Text string `json:"text"`
		}
		_ = json.NewDecoder(r.Body).Decode(&payload)
		messages = append(messages, payload.Text)
		return response(http.StatusOK, `{"ok":true}`), nil
	})
	m.configureNodes(true, true, t.TempDir())
	m.settings.AlertNewBlock = false
	m.healthTicks = 1
	m.diskFreeGB = func(string) (float64, error) { return 1000, nil }
	bitcoin := map[string]any{"initialblockdownload": false, "blocks": float64(900000)}
	monero := func(ibd bool, height int) map[string]any {
		return map[string]any{"initialblockdownload": ibd, "blocks": float64(height)}
	}
	flap := func(height int) {
		m.tickNodes(t.Context(), bitcoin, monero(true, height))
		m.tickNodes(t.Context(), bitcoin, monero(false, height))
	}

	m.tickNodes(t.Context(), bitcoin, monero(true, 100))
	m.tickNodes(t.Context(), bitcoin, monero(false, 200))
	flap(201)
	flap(202)

	encoded, err := json.Marshal(persistedNodeFromState(m.moneroState))
	if err != nil {
		t.Fatal(err)
	}
	var saved persistedNodeState
	if err := json.Unmarshal(encoded, &saved); err != nil {
		t.Fatal(err)
	}
	m.moneroState = monitorState{}
	applyPersistedNodeState(&m.moneroState, saved)
	flap(203)

	for height := 300; height < 300+resyncAlertTicks; height++ {
		m.tickNodes(t.Context(), bitcoin, monero(true, height))
	}
	m.tickNodes(t.Context(), bitcoin, monero(false, 500))
	flap(501)

	want := []string{
		"[alice] Monero: ✅ initial sync complete at height 200",
		"[alice] Monero: ✅ initial sync complete at height 500",
	}
	if strings.Join(messages, "\n") != strings.Join(want, "\n") {
		t.Fatalf("messages = %#v, want %#v", messages, want)
	}
}

func TestMultiNodeOperationalStatePersistsAcrossRestart(t *testing.T) {
	stateDir := t.TempDir()
	bitcoinDir, moneroDir := t.TempDir(), t.TempDir()
	m := NewMonitor("dev", bitcoinDir, stateDir)
	m.configureNodes(true, true, moneroDir)
	m.output = io.Discard
	m.healthTicks = 1
	m.diskFreeGB = func(string) (float64, error) { return 1000, nil }
	synced := map[string]any{"initialblockdownload": false, "blocks": float64(100)}
	m.tickNodes(t.Context(), synced, map[string]any{"initialblockdownload": true, "blocks": float64(50)})
	m.tickNodes(t.Context(), nil, map[string]any{"initialblockdownload": false, "blocks": float64(75)})
	m.tickNodes(t.Context(), nil, map[string]any{"initialblockdownload": false, "blocks": float64(76)})
	m.tickNodes(t.Context(), nil, map[string]any{"initialblockdownload": false, "blocks": float64(77)})

	restarted := NewMonitor("dev", bitcoinDir, stateDir)
	if restarted.state.chainKnown || !restarted.state.downAlerted || restarted.state.fails != downAfterTicks {
		t.Fatalf("Bitcoin state after restart = %#v", restarted.state)
	}
	if restarted.moneroState.chainKnown || restarted.moneroState.ibd || restarted.moneroState.lastBlock != 77 || !restarted.moneroState.hasLastBlock {
		t.Fatalf("Monero state after restart = %#v", restarted.moneroState)
	}
}

func TestRunNodesRestartAfterSyncOnlyAnnouncesCompletion(t *testing.T) {
	initial := configuredMonitor(t, func(*http.Request) (*http.Response, error) {
		return response(http.StatusOK, `{"ok":true}`), nil
	})
	initial.state.chainKnown = true
	initial.state.ibd = true
	initial.state.lastBlock = 100
	initial.state.hasLastBlock = true
	initial.saveState()

	messages := make(chan string, 4)
	restarted := NewMonitor("1.2.0", initial.dataDir, filepath.Dir(initial.statePath))
	restarted.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var payload struct {
			Text string `json:"text"`
		}
		_ = json.NewDecoder(r.Body).Decode(&payload)
		messages <- payload.Text
		return response(http.StatusOK, `{"ok":true}`), nil
	})}
	restarted.configureNodes(true, false, "")
	restarted.diskFreeGB = func(string) (float64, error) { return 1000, nil }
	restarted.output = io.Discard
	restarted.healthTicks = 1
	restarted.tickEvery = time.Hour
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		restarted.RunNodes(ctx, func() map[string]any {
			return map[string]any{"initialblockdownload": false, "blocks": float64(200)}
		}, nil)
		close(done)
	}()

	select {
	case got := <-messages:
		if got != "[alice] Bitcoin: ✅ initial sync complete at height 200" {
			t.Fatalf("restart sync message = %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("restart sync completion was not sent")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("RunNodes did not stop after cancellation")
	}
	select {
	case extra := <-messages:
		t.Fatalf("restart sent an extra online message: %q", extra)
	default:
	}
}

func TestRunNodesRestartRequiresFreshChainStateBeforeDownAlert(t *testing.T) {
	initial := configuredMonitor(t, func(*http.Request) (*http.Response, error) {
		return response(http.StatusOK, `{"ok":true}`), nil
	})
	initial.state.chainKnown = true
	initial.state.ibd = false
	initial.state.lastBlock = 100
	initial.state.hasLastBlock = true
	initial.saveState()

	var telegrams atomic.Int32
	healthchecks := make(chan string, 1)
	restarted := NewMonitor("1.2.0", initial.dataDir, filepath.Dir(initial.statePath))
	restarted.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "api.telegram.org" {
			telegrams.Add(1)
			return response(http.StatusOK, `{"ok":true}`), nil
		}
		healthchecks <- r.URL.Path
		return response(http.StatusOK, "ok"), nil
	})}
	restarted.configureNodes(true, false, "")
	restarted.settings.HealthchecksEnabled = true
	restarted.settings.HealthchecksURL = "https://hc-ping.com/shared"
	restarted.output = io.Discard
	restarted.healthTicks = 1
	restarted.tickEvery = time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		restarted.RunNodes(ctx, func() map[string]any { return nil }, nil)
		close(done)
	}()

	select {
	case got := <-healthchecks:
		if got != "/shared" {
			t.Fatalf("Healthchecks path = %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("liveness Healthchecks ping was not sent")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("RunNodes did not stop after cancellation")
	}
	if got := telegrams.Load(); got != 0 {
		t.Fatalf("restart without a fresh chain state sent %d down alerts", got)
	}
}

func TestRunNodesPollsEnabledNodesConcurrently(t *testing.T) {
	m := NewMonitor("dev", t.TempDir(), t.TempDir())
	m.configureNodes(true, true, t.TempDir())
	m.healthTicks = 1
	m.tickEvery = time.Hour
	m.output = io.Discard
	m.diskFreeGB = func(string) (float64, error) { return 1000, nil }
	bitcoinStarted, moneroStarted := make(chan struct{}), make(chan struct{})
	release := make(chan struct{})
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		m.RunNodes(ctx,
			func() map[string]any {
				close(bitcoinStarted)
				<-release
				return map[string]any{"initialblockdownload": true}
			},
			func() map[string]any {
				close(moneroStarted)
				<-release
				return map[string]any{"initialblockdownload": true}
			},
		)
	}()
	for _, started := range []<-chan struct{}{bitcoinStarted, moneroStarted} {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("enabled node polls did not start concurrently")
		}
	}
	close(release)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("monitor did not stop after cancellation")
	}
}

func TestMoneroHealthUsesRPCState(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": "dashboard", "result": map[string]any{
			"synchronized": true, "busy_syncing": false, "height": 3500000,
		}})
	}))
	defer server.Close()
	a := &app{cfg: config{moneroRPCURL: server.URL, moneroRPCUser: "rpc-user", moneroRPCPassword: "rpc-password"}}
	got := a.healthMonero(t.Context())
	if got == nil || got["initialblockdownload"] != false || got["blocks"] != int64(3500000) {
		t.Fatalf("Monero health = %#v", got)
	}
}

func TestMoneroHealthDoesNotFollowRPCRedirects(t *testing.T) {
	var redirected atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { redirected.Add(1) }))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer server.Close()
	a := &app{cfg: config{moneroRPCURL: server.URL, moneroRPCUser: "rpc-user", moneroRPCPassword: "rpc-password"}}
	if got := a.healthMonero(t.Context()); got != nil || redirected.Load() != 0 {
		t.Fatalf("redirected Monero health = %#v, target calls = %d", got, redirected.Load())
	}
}

func TestUnknownStartupAndInitialSyncSuppressAutomaticTelegram(t *testing.T) {
	var telegrams int
	m := configuredMonitor(t, func(r *http.Request) (*http.Response, error) {
		switch r.URL.Host {
		case "api.github.com":
			return response(http.StatusOK, `{"tag_name":"v1.3.0"}`), nil
		case "api.telegram.org":
			telegrams++
			return response(http.StatusOK, `{"ok":true}`), nil
		default:
			t.Fatalf("unexpected host %q", r.URL.Host)
			return nil, nil
		}
	})
	m.diskFreeGB = func(string) (float64, error) { return 40, nil }

	m.tick(t.Context(), nil, nil)
	m.tick(t.Context(), nil, nil)
	m.tick(t.Context(), nil, nil)
	m.state.ticks = 0
	m.tick(t.Context(), map[string]any{"blocks": float64(1)}, map[string]any{})
	m.state.ticks = 0
	m.tick(t.Context(), map[string]any{"initialblockdownload": true, "blocks": float64(100)}, map[string]any{})

	if telegrams != 0 {
		t.Fatalf("automatic Telegram sends during unknown startup or sync = %d", telegrams)
	}
	if got := m.UpdateNote(); got != "stack v1.3.0 available (running v1.2.0)" {
		t.Fatalf("silent release note = %q", got)
	}
	if m.state.notifiedStack != "" {
		t.Fatalf("silent release was marked notified: %q", m.state.notifiedStack)
	}
}

func TestOperationalAlertsRetryUntilDelivered(t *testing.T) {
	var messages []string
	attempt := 0
	m := configuredMonitor(t, func(r *http.Request) (*http.Response, error) {
		var payload struct {
			Text string `json:"text"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		messages = append(messages, payload.Text)
		attempt++
		switch attempt {
		case 1:
			return response(http.StatusBadGateway, `{"ok":false}`), nil
		case 3:
			return response(http.StatusOK, `{"ok":false}`), nil
		default:
			return response(http.StatusOK, `{"ok":true}`), nil
		}
	})

	m.tick(t.Context(), map[string]any{"initialblockdownload": false, "blocks": float64(100)}, nil)
	m.tick(t.Context(), nil, nil)
	m.tick(t.Context(), nil, nil)
	m.tick(t.Context(), nil, nil)
	m.tick(t.Context(), nil, nil)
	m.tick(t.Context(), map[string]any{"initialblockdownload": false}, nil)
	m.tick(t.Context(), map[string]any{"initialblockdownload": false}, nil)
	m.tick(t.Context(), map[string]any{"initialblockdownload": false}, nil)

	want := []string{
		"[alice] 🔴 node down — RPC unreachable for 3 minutes",
		"[alice] 🔴 node down — RPC unreachable for 3 minutes",
		"[alice] 🟢 node recovered — RPC is answering again",
		"[alice] 🟢 node recovered — RPC is answering again",
	}
	if strings.Join(messages, "\n") != strings.Join(want, "\n") {
		t.Fatalf("operational retries:\n%s\nwant:\n%s", strings.Join(messages, "\n"), strings.Join(want, "\n"))
	}
	if m.state.downAlerted || m.state.fails != 0 {
		t.Fatalf("recovered state = downAlerted %v, fails %d", m.state.downAlerted, m.state.fails)
	}
}

func TestSyncAndNewBlockAlertsRetryUntilDelivered(t *testing.T) {
	for _, test := range []struct {
		name  string
		ticks []map[string]any
		want  string
	}{
		{
			name: "sync complete",
			ticks: []map[string]any{
				{"initialblockdownload": true, "blocks": float64(100)},
				{"initialblockdownload": false, "blocks": float64(100)},
				{"initialblockdownload": false, "blocks": float64(100)},
				{"initialblockdownload": false, "blocks": float64(100)},
			},
			want: "[alice] ✅ initial sync complete at height 100",
		},
		{
			name: "new block",
			ticks: []map[string]any{
				{"initialblockdownload": false, "blocks": float64(100)},
				{"initialblockdownload": false, "blocks": float64(101)},
				{"initialblockdownload": false, "blocks": float64(101)},
				{"initialblockdownload": false, "blocks": float64(101)},
			},
			want: "[alice] 🟧 new block 101",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			var messages []string
			m := configuredMonitor(t, func(r *http.Request) (*http.Response, error) {
				var payload struct {
					Text string `json:"text"`
				}
				_ = json.NewDecoder(r.Body).Decode(&payload)
				messages = append(messages, payload.Text)
				if len(messages) == 1 {
					return response(http.StatusBadGateway, `{"ok":false}`), nil
				}
				return response(http.StatusOK, `{"ok":true}`), nil
			})
			for _, info := range test.ticks {
				m.tick(t.Context(), info, nil)
			}
			if len(messages) != 2 || messages[0] != test.want || messages[1] != test.want {
				t.Fatalf("retry messages = %#v, want two %q", messages, test.want)
			}
		})
	}
}

func TestSyncCompleteRetryWaitsForConfirmedCompletedState(t *testing.T) {
	var messages []string
	m := configuredMonitor(t, func(r *http.Request) (*http.Response, error) {
		var payload struct {
			Text string `json:"text"`
		}
		_ = json.NewDecoder(r.Body).Decode(&payload)
		messages = append(messages, payload.Text)
		if len(messages) == 1 {
			return response(http.StatusBadGateway, `{"ok":false}`), nil
		}
		return response(http.StatusOK, `{"ok":true}`), nil
	})
	m.settings.AlertNewBlock = false

	m.tick(t.Context(), map[string]any{"initialblockdownload": true, "blocks": float64(100)}, nil)
	m.tick(t.Context(), map[string]any{"initialblockdownload": false, "blocks": float64(200)}, nil)
	m.tick(t.Context(), map[string]any{"initialblockdownload": true, "blocks": float64(201)}, nil)
	m.tick(t.Context(), map[string]any{"blocks": float64(202)}, nil)
	if len(messages) != 1 {
		t.Fatalf("sync completion retried without a confirmed completed state: %#v", messages)
	}
	m.tick(t.Context(), map[string]any{"initialblockdownload": false, "blocks": float64(203)}, nil)

	want := "[alice] ✅ initial sync complete"
	if len(messages) != 2 || !strings.Contains(messages[0], want) || !strings.Contains(messages[1], want) {
		t.Fatalf("sync completion retries = %#v", messages)
	}
}

func TestPendingNewBlockAlertIsClearedWhenSyncResumes(t *testing.T) {
	attempts := 0
	m := configuredMonitor(t, func(r *http.Request) (*http.Response, error) {
		attempts++
		return response(http.StatusBadGateway, `{"ok":false}`), nil
	})
	m.tick(t.Context(), map[string]any{"initialblockdownload": false, "blocks": float64(100)}, nil)
	m.tick(t.Context(), map[string]any{"initialblockdownload": false, "blocks": float64(101)}, nil)
	if attempts != 1 || m.state.pendingBlock != 101 {
		t.Fatalf("failed alert attempts=%d state=%#v", attempts, m.state)
	}
	m.tick(t.Context(), map[string]any{"initialblockdownload": true, "blocks": float64(101)}, nil)
	if attempts != 1 || m.state.pendingBlock != 0 {
		t.Fatalf("syncing retried stale block alert: attempts=%d state=%#v", attempts, m.state)
	}
}

func TestDiskAlertRetriesUntilDelivered(t *testing.T) {
	free := 40.0
	var messages []string
	m := configuredMonitor(t, func(r *http.Request) (*http.Response, error) {
		var payload struct {
			Text string `json:"text"`
		}
		_ = json.NewDecoder(r.Body).Decode(&payload)
		messages = append(messages, payload.Text)
		if len(messages) == 1 {
			return response(http.StatusBadGateway, `{"ok":false}`), nil
		}
		return response(http.StatusOK, `{"ok":true}`), nil
	})
	m.diskFreeGB = func(string) (float64, error) { return free, nil }
	info := map[string]any{"initialblockdownload": false}
	m.tick(t.Context(), info, nil)
	m.tick(t.Context(), info, nil)
	m.tick(t.Context(), info, nil)
	free = 100
	m.tick(t.Context(), info, nil)
	free = 40
	m.tick(t.Context(), info, nil)
	if got, want := strings.Join(messages, "\n"), strings.Join([]string{
		"[alice] 💾 disk low: 40 GiB free",
		"[alice] 💾 disk low: 40 GiB free",
		"[alice] 💾 disk low: 40 GiB free",
	}, "\n"); got != want {
		t.Fatalf("disk retry messages:\n%s\nwant:\n%s", got, want)
	}
}

func TestGlobalAlertClassesSuppressEpisodesAndReenable(t *testing.T) {
	var messages []string
	m := configuredMonitor(t, func(r *http.Request) (*http.Response, error) {
		var payload struct {
			Text string `json:"text"`
		}
		_ = json.NewDecoder(r.Body).Decode(&payload)
		messages = append(messages, payload.Text)
		return response(http.StatusOK, `{"ok":true}`), nil
	})
	m.diskFreeGB = func(string) (float64, error) { return 100, nil }
	m.settings.AlertNewBlock = false
	m.settings.AlertNodeDown = false
	m.settings.AlertNodeRecovered = false
	m.settings.AlertSyncComplete = false
	m.settings.AlertLowDisk = false
	m.settings.NodeDownAfterMinutes = 2
	m.settings.LowDiskGiB = 30

	m.tick(t.Context(), map[string]any{"initialblockdownload": false, "blocks": float64(100)}, nil)
	m.tick(t.Context(), nil, nil)
	m.tick(t.Context(), nil, nil)
	m.tick(t.Context(), map[string]any{"initialblockdownload": false, "blocks": float64(100)}, nil)
	for range resyncAlertTicks {
		m.tick(t.Context(), map[string]any{"initialblockdownload": true, "blocks": float64(100)}, nil)
	}
	m.tick(t.Context(), map[string]any{"initialblockdownload": false, "blocks": float64(100)}, nil)
	m.diskFreeGB = func(string) (float64, error) { return 20, nil }
	m.tick(t.Context(), map[string]any{"initialblockdownload": false, "blocks": float64(100)}, nil)
	if len(messages) != 0 || m.state.downAlerted || m.state.syncPending || !m.state.diskAlerted {
		t.Fatalf("disabled alert classes messages=%#v state=%#v", messages, m.state)
	}

	m.settings.AlertNodeDown = true
	m.settings.AlertNodeRecovered = true
	m.settings.AlertSyncComplete = true
	m.settings.AlertLowDisk = true
	m.diskFreeGB = func(string) (float64, error) { return 100, nil }
	m.tick(t.Context(), map[string]any{"initialblockdownload": false, "blocks": float64(100)}, nil)
	m.tick(t.Context(), nil, nil)
	m.tick(t.Context(), nil, nil)
	m.tick(t.Context(), map[string]any{"initialblockdownload": false, "blocks": float64(100)}, nil)
	for range resyncAlertTicks {
		m.tick(t.Context(), map[string]any{"initialblockdownload": true, "blocks": float64(100)}, nil)
	}
	m.tick(t.Context(), map[string]any{"initialblockdownload": false, "blocks": float64(100)}, nil)
	m.diskFreeGB = func(string) (float64, error) { return 20, nil }
	m.tick(t.Context(), map[string]any{"initialblockdownload": false, "blocks": float64(100)}, nil)

	want := []string{
		"[alice] 🔴 node down — RPC unreachable for 2 minutes",
		"[alice] 🟢 node recovered — RPC is answering again",
		"[alice] ✅ initial sync complete at height 100",
		"[alice] 💾 disk low: 20 GiB free",
	}
	if strings.Join(messages, "\n") != strings.Join(want, "\n") {
		t.Fatalf("reenabled alerts:\n%s\nwant:\n%s", strings.Join(messages, "\n"), strings.Join(want, "\n"))
	}
}

func TestMonitorOnlineAndUpdateAlertTogglesKeepMonitoring(t *testing.T) {
	var messages []string
	release := "1.3.0"
	m := configuredMonitor(t, func(r *http.Request) (*http.Response, error) {
		switch r.URL.Host {
		case "api.github.com":
			return response(http.StatusOK, `{"tag_name":"v`+release+`"}`), nil
		case "api.telegram.org":
			var payload struct {
				Text string `json:"text"`
			}
			_ = json.NewDecoder(r.Body).Decode(&payload)
			messages = append(messages, payload.Text)
			return response(http.StatusOK, `{"ok":true}`), nil
		default:
			t.Fatalf("unexpected host %q", r.URL.Host)
			return nil, nil
		}
	})
	m.settings.AlertMonitorOnline = false
	if pending := m.announceOnline(t.Context(), "Bitcoin", &m.state, map[string]any{"initialblockdownload": false}, true); pending {
		t.Fatal("online announcement remained pending after a confirmed node response")
	}
	m.settings.AlertUpdates = false
	m.checkUpdates(t.Context())
	if len(messages) != 0 || m.UpdateNote() == "" || m.state.notifiedStack != release {
		t.Fatalf("disabled alerts messages=%#v note=%q notified=%q", messages, m.UpdateNote(), m.state.notifiedStack)
	}

	m.settings.AlertMonitorOnline = true
	m.announceOnline(t.Context(), "Bitcoin", &monitorState{}, map[string]any{"initialblockdownload": false}, true)
	m.settings.AlertUpdates = true
	release = "1.4.0"
	m.checkUpdates(t.Context())
	if len(messages) != 2 || messages[0] != "[alice] Bitcoin: 🚀 monitor online" || !strings.Contains(messages[1], "stack v1.4.0 available") {
		t.Fatalf("reenabled monitor/update alerts = %#v", messages)
	}
}

func TestDisabledTelegramDoesNotRetryOperationalAlerts(t *testing.T) {
	t.Setenv("TELEGRAM_BOT_TOKEN", "")
	t.Setenv("TELEGRAM_CHAT_ID", "")
	stateDir := t.TempDir()
	m := NewMonitor("dev", t.TempDir(), stateDir)
	m.output = io.Discard
	m.diskFreeGB = func(string) (float64, error) { return 40, nil }
	m.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("disabled Telegram made an HTTP request")
		return nil, nil
	})}
	info := map[string]any{"initialblockdownload": false}
	m.tick(t.Context(), info, nil)
	m.tick(t.Context(), info, nil)
	m.tick(t.Context(), nil, nil)
	m.tick(t.Context(), nil, nil)
	m.tick(t.Context(), nil, nil)
	m.tick(t.Context(), nil, nil)

	events, err := os.ReadFile(filepath.Join(stateDir, "events.log"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(events), "disk low") != 1 || strings.Count(string(events), "node down") != 1 {
		t.Fatalf("disabled Telegram repeated operational events: %q", events)
	}
}

func TestHealthchecksRetryThrottleAndLivenessWhileNodeDown(t *testing.T) {
	t.Setenv("TELEGRAM_BOT_TOKEN", "")
	t.Setenv("HEALTHCHECKS_URL", "https://hc-ping.com/private-uuid/?source=node")
	m := NewMonitor("dev", t.TempDir(), t.TempDir())
	m.freshTorClient = testFreshTorClient
	m.diskFreeGB = func(string) (float64, error) { return 1000, nil }
	m.output = io.Discard
	m.settings.NodeDownAfterMinutes = 60
	var paths []string
	attempt := 0
	m.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		paths = append(paths, r.URL.RequestURI())
		attempt++
		if attempt < 3 {
			return response(http.StatusBadGateway, ""), nil
		}
		return response(http.StatusOK, ""), nil
	})}

	for range 3 {
		m.tick(t.Context(), nil, nil)
	}
	if got, want := strings.Join(paths, ","), "/private-uuid/?source=node,/private-uuid/?source=node,/private-uuid/?source=node"; got != want {
		t.Fatalf("retried due pings = %q, want %q", got, want)
	}
	for range 4 {
		m.tick(t.Context(), nil, nil)
	}
	if len(paths) != 3 {
		t.Fatalf("throttled window pinged Healthchecks: %v", paths)
	}
	m.tick(t.Context(), nil, nil)
	if len(paths) != 4 || paths[3] != "/private-uuid/?source=node" {
		t.Fatalf("down node changed the liveness ping: %v", paths)
	}
	for range 5 {
		m.tick(t.Context(), map[string]any{"initialblockdownload": false}, nil)
	}
	if len(paths) != 5 || paths[4] != "/private-uuid/?source=node" {
		t.Fatalf("paths after cadence = %v", paths)
	}
}

func TestUpdateCheckKeepsBadgeAndDoesNotRepeatAlert(t *testing.T) {
	var telegrams, stackChecks, coreChecks int
	m := configuredMonitor(t, func(r *http.Request) (*http.Response, error) {
		switch r.URL.Host {
		case "api.github.com":
			if r.URL.Path == "/repos/VijitSingh97/node-manager/releases/latest" {
				stackChecks++
				return response(http.StatusOK, `{"tag_name":"v1.3.0"}`), nil
			}
			coreChecks++
			return response(http.StatusOK, `{"tag_name":"v32.0"}`), nil
		case "api.telegram.org":
			telegrams++
			return response(http.StatusOK, `{"ok":true}`), nil
		default:
			t.Fatalf("unexpected host %q", r.URL.Host)
			return nil, nil
		}
	})

	m.checkUpdates(t.Context())
	m.checkUpdates(t.Context())
	want := "stack v1.3.0 available (running v1.2.0)"
	if got := m.UpdateNote(); got != want {
		t.Fatalf("UpdateNote() = %q, want %q", got, want)
	}
	if telegrams != 1 {
		t.Fatalf("telegram sends = %d, want 1", telegrams)
	}
	if stackChecks != 2 || coreChecks != 0 {
		t.Fatalf("release checks = stack %d, Core %d", stackChecks, coreChecks)
	}
	if newer("31.1", "31.1.0") || newer("1.12.0", "1.12.1") || !newer("32.0", "31.1.0") {
		t.Fatal("numeric version ordering changed")
	}
}

func TestUpdateCheckUsesBakedBaselineAndPersistsAlert(t *testing.T) {
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	imageDirectory := t.TempDir()
	if err := os.WriteFile(filepath.Join(imageDirectory, "VERSION"), []byte("1.31.1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(imageDirectory); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(workingDirectory) })

	t.Setenv("TELEGRAM_BOT_TOKEN", "123:secret")
	t.Setenv("TELEGRAM_CHAT_ID", "42")
	stateDir := t.TempDir()
	telegrams := 0
	handler := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Host {
		case "api.github.com":
			return response(http.StatusOK, `{"tag_name":"v1.32.0"}`), nil
		case "api.telegram.org":
			telegrams++
			switch telegrams {
			case 1:
				return response(http.StatusBadGateway, `{"ok":false}`), nil
			case 2:
				return response(http.StatusOK, `{"ok":false}`), nil
			default:
				return response(http.StatusOK, `{"ok":true}`), nil
			}
		default:
			t.Fatalf("unexpected host %q", r.URL.Host)
			return nil, nil
		}
	})
	m := NewMonitor("7101c3a", t.TempDir(), stateDir)
	m.client = &http.Client{Transport: handler}
	m.output = io.Discard
	m.checkUpdates(t.Context())
	if got, want := m.UpdateNote(), "stack v1.32.0 available (running v1.31.1)"; got != want {
		t.Fatalf("UpdateNote() = %q, want %q", got, want)
	}
	if _, err := os.Stat(filepath.Join(stateDir, "monitor_state.json")); !os.IsNotExist(err) {
		t.Fatalf("failed delivery persisted monitor state: %v", err)
	}
	m.checkUpdates(t.Context())
	if _, err := os.Stat(filepath.Join(stateDir, "monitor_state.json")); !os.IsNotExist(err) {
		t.Fatalf("rejected Telegram response persisted monitor state: %v", err)
	}
	m.checkUpdates(t.Context())

	persisted, err := os.ReadFile(filepath.Join(stateDir, "monitor_state.json"))
	if err != nil || !strings.Contains(string(persisted), `"notified_stack":"1.32.0"`) {
		t.Fatalf("successful delivery state = %q, %v", persisted, err)
	}
	legacy := []byte(`{"notified_stack":"1.32.0","notified_core":"32.0"}`)
	if err = os.WriteFile(filepath.Join(stateDir, "monitor_state.json"), legacy, 0o600); err != nil {
		t.Fatal(err)
	}
	restarted := NewMonitor("7101c3a", t.TempDir(), stateDir)
	restarted.client = &http.Client{Transport: handler}
	restarted.output = io.Discard
	restarted.checkUpdates(t.Context())
	if telegrams != 3 {
		t.Fatalf("telegram sends across retries and restart = %d, want 3", telegrams)
	}
}

func TestUpdateCheckWaitsForTelegramConfiguration(t *testing.T) {
	t.Setenv("TELEGRAM_BOT_TOKEN", "")
	t.Setenv("TELEGRAM_CHAT_ID", "")
	stateDir := t.TempDir()
	release := func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "api.github.com" {
			t.Fatalf("unexpected request to %q", r.URL.Host)
		}
		return response(http.StatusOK, `{"tag_name":"v1.3.0"}`), nil
	}
	m := NewMonitor("1.2.0", t.TempDir(), stateDir)
	m.client = &http.Client{Transport: roundTripFunc(release)}
	m.output = io.Discard
	m.checkUpdates(t.Context())
	if _, err := os.Stat(filepath.Join(stateDir, "monitor_state.json")); !os.IsNotExist(err) {
		t.Fatalf("disabled Telegram persisted monitor state: %v", err)
	}

	t.Setenv("TELEGRAM_BOT_TOKEN", "123:secret")
	t.Setenv("TELEGRAM_CHAT_ID", "42")
	sent := 0
	restarted := NewMonitor("1.2.0", t.TempDir(), stateDir)
	restarted.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "api.telegram.org" {
			sent++
			return response(http.StatusOK, `{"ok":true}`), nil
		}
		return release(r)
	})}
	restarted.output = io.Discard
	restarted.checkUpdates(t.Context())
	if sent != 1 {
		t.Fatalf("Telegram sends after configuration = %d, want 1", sent)
	}
}

func TestEventLogRotatesAndUsesUTCTimestamp(t *testing.T) {
	stateDir := t.TempDir()
	logPath := filepath.Join(stateDir, "events.log")
	if err := os.WriteFile(logPath, []byte(strings.Repeat("old line\n", 120000)), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TELEGRAM_BOT_TOKEN", "")
	m := NewMonitor("dev", t.TempDir(), stateDir)
	m.output = io.Discard
	m.now = func() time.Time { return time.Date(2026, 10, 27, 1, 2, 3, 0, time.FixedZone("local", -5*3600)) }
	m.sendTelegram(t.Context(), "🔴 node down")
	b, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
	if len(lines) != 60001 || lines[len(lines)-1] != "2026-10-27T06:02:03Z 🔴 node down" {
		t.Fatalf("rotated log has %d lines, last %q", len(lines), lines[len(lines)-1])
	}
}

func TestRunSendsOnlineAndStopsWithContext(t *testing.T) {
	seen := make(chan string, 2)
	m := configuredMonitor(t, func(r *http.Request) (*http.Response, error) {
		var payload struct {
			Text string `json:"text"`
		}
		_ = json.NewDecoder(r.Body).Decode(&payload)
		seen <- payload.Text
		return response(http.StatusOK, `{"ok":true}`), nil
	})
	m.tickEvery = time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		m.Run(ctx, func() map[string]any { return map[string]any{"initialblockdownload": false} }, nil)
		close(done)
	}()
	select {
	case got := <-seen:
		if got != "[alice] 🚀 node monitor online" {
			t.Fatalf("online message = %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("online message was not sent")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not stop after cancellation")
	}
}

func TestRunDoesNotSendOnlineDuringInitialSync(t *testing.T) {
	var telegrams atomic.Int32
	m := configuredMonitor(t, func(*http.Request) (*http.Response, error) {
		telegrams.Add(1)
		return response(http.StatusOK, `{"ok":true}`), nil
	})
	m.tickEvery = time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		m.Run(ctx, func() map[string]any { return map[string]any{"initialblockdownload": true} }, nil)
		close(done)
	}()
	time.Sleep(10 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not stop after cancellation")
	}
	if got := telegrams.Load(); got != 0 {
		t.Fatalf("initial sync Telegram sends = %d", got)
	}
}

func TestRunCancellationStopsReleaseCheck(t *testing.T) {
	m := NewMonitor("1.2.0", t.TempDir(), t.TempDir())
	m.diskFreeGB = func(string) (float64, error) { return 1000, nil }
	started := make(chan struct{})
	m.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		close(started)
		<-r.Context().Done()
		return nil, r.Context().Err()
	})}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		m.Run(ctx,
			func() map[string]any { return map[string]any{"initialblockdownload": false} },
			func() map[string]any { return map[string]any{} },
		)
		close(done)
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("release check did not start")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not cancel an in-flight release check")
	}
}

func TestMonitorRequestsHonorParentCancellation(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*Monitor)
		call  func(context.Context, *Monitor) bool
	}{
		{name: "Telegram", call: func(ctx context.Context, m *Monitor) bool { return m.sendTelegram(ctx, "test") }},
		{
			name: "Healthchecks",
			setup: func(m *Monitor) {
				m.settings.HealthchecksEnabled = true
				m.settings.HealthchecksURL = "https://healthchecks.io/test"
			},
			call: func(ctx context.Context, m *Monitor) bool { return m.pingHealthchecks(ctx) },
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			started := make(chan struct{})
			m := configuredMonitor(t, func(r *http.Request) (*http.Response, error) {
				close(started)
				<-r.Context().Done()
				return nil, r.Context().Err()
			})
			if test.setup != nil {
				test.setup(m)
			}
			ctx, cancel := context.WithCancel(t.Context())
			done := make(chan struct{})
			go func() {
				test.call(ctx, m)
				close(done)
			}()
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("request did not start")
			}
			cancel()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("request did not stop after parent cancellation")
			}
		})
	}
}

func TestRunSyncsWithoutOnlineAndSendsOneCompletion(t *testing.T) {
	messages := make(chan string, 4)
	m := configuredMonitor(t, func(r *http.Request) (*http.Response, error) {
		var payload struct {
			Text string `json:"text"`
		}
		_ = json.NewDecoder(r.Body).Decode(&payload)
		messages <- payload.Text
		return response(http.StatusOK, `{"ok":true}`), nil
	})
	m.tickEvery = time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var calls atomic.Int32
	done := make(chan struct{})
	go func() {
		m.Run(ctx, func() map[string]any {
			switch calls.Add(1) {
			case 1:
				return map[string]any{"initialblockdownload": true, "blocks": float64(100)}
			case 2:
				return map[string]any{"initialblockdownload": false, "blocks": float64(200)}
			default:
				<-ctx.Done()
				return nil
			}
		}, nil)
		close(done)
	}()

	select {
	case got := <-messages:
		if got != "[alice] ✅ initial sync complete at height 200" {
			t.Fatalf("sync transition message = %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("sync completion message was not sent")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not stop after cancellation")
	}
	select {
	case extra := <-messages:
		t.Fatalf("unexpected extra sync transition message: %q", extra)
	default:
	}
}

func TestTorTransportUsesRemoteDNSForHTTPAndHTTPS(t *testing.T) {
	for _, scheme := range []string{"http", "https"} {
		t.Run(scheme, func(t *testing.T) {
			var backend *httptest.Server
			if scheme == "https" {
				backend = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
			} else {
				backend = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
			}
			defer backend.Close()
			proxy, domains := fakeSOCKSProxy(t, backend.Listener.Addr().String(), false)
			client := newTorHTTPClient(proxy)
			if scheme == "https" {
				client.Transport.(*http.Transport).TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
			}
			resp, err := client.Get(scheme + "://remote.example:9443/check")
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusNoContent {
				t.Fatalf("status = %d", resp.StatusCode)
			}
			select {
			case got := <-domains:
				if got != "remote.example" {
					t.Fatalf("SOCKS destination = %q", got)
				}
			case <-time.After(time.Second):
				t.Fatal("SOCKS handshake not observed")
			}
		})
	}
}

func TestTorTransportNeverFallsBackToDirect(t *testing.T) {
	var direct atomic.Int32
	origin := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { direct.Add(1) }))
	defer origin.Close()
	proxy, _ := fakeSOCKSProxy(t, "", true)
	client := newTorHTTPClient(proxy)
	client.Timeout = time.Second
	if _, err := client.Get(origin.URL); err == nil {
		t.Fatal("request unexpectedly succeeded")
	}
	if direct.Load() != 0 {
		t.Fatal("request fell back to a direct connection")
	}
}

func TestHealthchecksRejectsUnencryptedOrCredentialedURLs(t *testing.T) {
	for _, endpoint := range []string{"http://hc-ping.com/secret", "https://user:secret@hc-ping.com/id", "https:///id"} {
		t.Run(endpoint, func(t *testing.T) {
			m := configuredMonitor(t, func(*http.Request) (*http.Response, error) {
				t.Fatal("invalid URL reached the network")
				return nil, nil
			})
			m.settings.HealthchecksEnabled = true
			m.settings.HealthchecksURL = endpoint
			if m.pingHealthchecks(t.Context()) {
				t.Fatal("invalid Healthchecks URL was accepted")
			}
		})
	}
}

func TestHealthchecksPingRetriesOnFreshCircuits(t *testing.T) {
	var primary, fresh atomic.Int32
	m := configuredMonitor(t, func(*http.Request) (*http.Response, error) {
		primary.Add(1)
		return nil, errors.New("circuit failed")
	})
	m.settings.HealthchecksEnabled = true
	m.settings.HealthchecksURL = "https://hc-ping.com/id"
	m.freshTorClient = func() *http.Client {
		return &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			if fresh.Add(1) == 2 {
				return response(http.StatusOK, "ok"), nil
			}
			return nil, errors.New("circuit failed")
		})}
	}
	if !m.pingHealthchecks(t.Context()) {
		t.Fatal("ping failed despite a successful fresh-circuit retry")
	}
	if primary.Load() != 1 || fresh.Load() != 2 {
		t.Fatalf("attempts: primary=%d fresh=%d, want 1 and 2", primary.Load(), fresh.Load())
	}
}

func TestSOCKS5ConnectAuthenticatesWithCredentials(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	serverErr := make(chan error, 1)
	go func() {
		serverErr <- func() error {
			greeting := make([]byte, 3)
			if _, err := io.ReadFull(server, greeting); err != nil {
				return err
			}
			if greeting[2] != 2 {
				return fmt.Errorf("auth method = %d, want 2", greeting[2])
			}
			if _, err := server.Write([]byte{5, 2}); err != nil {
				return err
			}
			head := make([]byte, 2)
			if _, err := io.ReadFull(server, head); err != nil {
				return err
			}
			user := make([]byte, int(head[1]))
			if _, err := io.ReadFull(server, user); err != nil {
				return err
			}
			if string(user) != "alice" {
				return fmt.Errorf("username = %q", user)
			}
			passLen := make([]byte, 1)
			if _, err := io.ReadFull(server, passLen); err != nil {
				return err
			}
			if _, err := io.CopyN(io.Discard, server, int64(passLen[0])); err != nil {
				return err
			}
			if _, err := server.Write([]byte{1, 0}); err != nil {
				return err
			}
			connect := make([]byte, 4+1+len("remote.example")+2)
			if _, err := io.ReadFull(server, connect); err != nil {
				return err
			}
			_, err := server.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0})
			return err
		}()
	}()
	if err := socks5Connect(client, "remote.example:443", "alice", "0"); err != nil {
		t.Fatalf("socks5Connect with credentials failed: %v", err)
	}
	if err := <-serverErr; err != nil {
		t.Fatalf("proxy side: %v", err)
	}
}

func TestTorClientDoesNotFollowRedirects(t *testing.T) {
	client := newTorHTTPClient("127.0.0.1:1")
	var requests int
	client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requests++
		resp := response(http.StatusFound, "")
		resp.Header.Set("Location", "http://other.example/leaked-secret")
		return resp, nil
	})
	resp, err := client.Get("https://original.example/secret")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if requests != 1 || resp.StatusCode != http.StatusFound {
		t.Fatalf("redirect followed: requests=%d status=%d", requests, resp.StatusCode)
	}
}

func TestSOCKSHandshakeCancellationClosesStalledConnection(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	accepted := make(chan struct{})
	closed := make(chan struct{})
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		close(accepted)
		_, _ = io.Copy(io.Discard, conn)
		close(closed)
	}()

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := socks5DialContext(ln.Addr().String(), "", "")(ctx, "tcp", "remote.example:443")
		result <- err
	}()
	select {
	case <-accepted:
	case <-time.After(time.Second):
		t.Fatal("proxy did not accept connection")
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("dial error = %v, want context canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled SOCKS handshake did not return")
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("canceled SOCKS handshake did not close connection")
	}
}

func TestTransportErrorsRedactSecrets(t *testing.T) {
	var output strings.Builder
	t.Setenv("TELEGRAM_BOT_TOKEN", "123:top-secret")
	t.Setenv("TELEGRAM_CHAT_ID", "42")
	t.Setenv("HEALTHCHECKS_URL", "https://hc-ping.com/private-uuid")
	m := NewMonitor("dev", t.TempDir(), t.TempDir())
	m.output = &output
	m.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return nil, errors.New(r.URL.String())
	})}
	m.freshTorClient = testFreshTorClient
	m.sendTelegram(t.Context(), "hello")
	m.pingHealthchecks(t.Context())
	got := output.String()
	if strings.Contains(got, "top-secret") || strings.Contains(got, "private-uuid") {
		t.Fatalf("secret leaked in output: %q", got)
	}
}

func fakeSOCKSProxy(t *testing.T, backend string, reject bool) (string, <-chan string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	domains := make(chan string, 2)
	var wg sync.WaitGroup
	t.Cleanup(func() {
		ln.Close()
		wg.Wait()
	})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				handleSOCKSConn(conn, backend, reject, domains)
			}()
		}
	}()
	return ln.Addr().String(), domains
}

func handleSOCKSConn(conn net.Conn, backend string, reject bool, domains chan<- string) {
	defer conn.Close()
	r := bufio.NewReader(conn)
	hello := make([]byte, 3)
	if _, err := io.ReadFull(r, hello); err != nil || string(hello) != "\x05\x01\x00" {
		return
	}
	_, _ = conn.Write([]byte{5, 0})
	head := make([]byte, 4)
	if _, err := io.ReadFull(r, head); err != nil || head[0] != 5 || head[1] != 1 {
		return
	}
	var host string
	switch head[3] {
	case 1:
		b := make([]byte, 4)
		_, _ = io.ReadFull(r, b)
		host = net.IP(b).String()
	case 3:
		n, err := r.ReadByte()
		if err != nil {
			return
		}
		b := make([]byte, int(n))
		_, _ = io.ReadFull(r, b)
		host = string(b)
	case 4:
		b := make([]byte, 16)
		_, _ = io.ReadFull(r, b)
		host = net.IP(b).String()
	default:
		return
	}
	port := make([]byte, 2)
	_, _ = io.ReadFull(r, port)
	_ = binary.BigEndian.Uint16(port)
	domains <- host
	if reject {
		_, _ = conn.Write([]byte{5, 5, 0, 1, 0, 0, 0, 0, 0, 0})
		return
	}
	upstream, err := net.Dial("tcp", backend)
	if err != nil {
		_, _ = conn.Write([]byte{5, 1, 0, 1, 0, 0, 0, 0, 0, 0})
		return
	}
	defer upstream.Close()
	_, _ = conn.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 0})
	go io.Copy(upstream, r)
	_, _ = io.Copy(conn, upstream)
}

func TestForwardHostEventsTelegramsNewHostLinesOnce(t *testing.T) {
	var messages []string
	m := configuredMonitor(t, func(r *http.Request) (*http.Response, error) {
		var payload struct {
			Text string `json:"text"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		messages = append(messages, payload.Text)
		return response(http.StatusOK, `{"ok":true}`), nil
	})

	m.forwardHostEvents(t.Context())
	append2 := func(line string) {
		f, err := os.OpenFile(m.eventsLog, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if _, err := f.WriteString(line); err != nil {
			t.Fatal(err)
		}
	}
	append2("2026-11-10T20:00:00Z Bitcoin: 🔴 node down — RPC unreachable for 3 minutes\n")
	append2("2026-11-10T20:01:00Z Host: thermal throttle at 91C — capped monero at 2.00 CPUs\n")
	append2("2026-11-10T20:02:00Z Host: thermal throttle released at 75C — restored monero\n")
	m.forwardHostEvents(t.Context())
	m.forwardHostEvents(t.Context())

	want := []string{
		"[alice] 🌡 thermal throttle at 91C — capped monero at 2.00 CPUs",
		"[alice] 🌡 thermal throttle released at 75C — restored monero",
	}
	if strings.Join(messages, "\n") != strings.Join(want, "\n") {
		t.Fatalf("messages:\n%s\nwant:\n%s", strings.Join(messages, "\n"), strings.Join(want, "\n"))
	}

	saved, err := os.ReadFile(m.statePath)
	if err != nil || !strings.Contains(string(saved), `"host_events_offset"`) {
		t.Fatalf("offset not persisted: %v %s", err, saved)
	}

	append2("2026-11-10T20:03:00Z Host: thermal throttle at 92C — capped bitcoin at 0.25 CPUs")
	m.forwardHostEvents(t.Context())
	if len(messages) != 2 {
		t.Fatalf("partial line without newline was forwarded: %v", messages)
	}
	append2("\n")
	m.forwardHostEvents(t.Context())
	if len(messages) != 3 || !strings.Contains(messages[2], "capped bitcoin at 0.25 CPUs") {
		t.Fatalf("completed line was not forwarded: %v", messages)
	}
}

func TestForwardHostEventsRetriesFailedSends(t *testing.T) {
	var messages []string
	failing := true
	m := configuredMonitor(t, func(r *http.Request) (*http.Response, error) {
		if failing {
			return response(http.StatusInternalServerError, `{}`), nil
		}
		var payload struct {
			Text string `json:"text"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		messages = append(messages, payload.Text)
		return response(http.StatusOK, `{"ok":true}`), nil
	})
	m.forwardHostEvents(t.Context())
	if err := os.WriteFile(m.eventsLog, []byte("2026-11-10T21:00:00Z Host: thermal throttle at 95C — capped monero at 1.00 CPUs\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	m.forwardHostEvents(t.Context())
	if len(messages) != 0 {
		t.Fatalf("failed send delivered anyway: %v", messages)
	}
	failing = false
	m.forwardHostEvents(t.Context())
	m.forwardHostEvents(t.Context())
	if len(messages) != 1 || !strings.Contains(messages[0], "capped monero at 1.00 CPUs") {
		t.Fatalf("failed send was not retried exactly once: %v", messages)
	}
}

func TestForwardHostEventsSurvivesLogRotation(t *testing.T) {
	var messages []string
	m := configuredMonitor(t, func(r *http.Request) (*http.Response, error) {
		var payload struct {
			Text string `json:"text"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		messages = append(messages, payload.Text)
		return response(http.StatusOK, `{"ok":true}`), nil
	})
	filler := strings.Repeat("2026-11-10T20:00:00Z filler line\n", 120000)
	hostLine := "2026-11-10T21:00:00Z Host: thermal throttle at 93C — capped bitcoin at 0.50 CPUs\n"
	if err := os.WriteFile(m.eventsLog, []byte(filler+hostLine), 0o600); err != nil {
		t.Fatal(err)
	}
	m.hostEventsOffset = int64(len(filler))
	m.logEvent("trigger rotation")
	m.forwardHostEvents(t.Context())
	if len(messages) != 1 || !strings.Contains(messages[0], "capped bitcoin at 0.50 CPUs") {
		t.Fatalf("host line lost or duplicated across rotation: %v", messages)
	}
}
