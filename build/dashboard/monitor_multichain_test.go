package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestConfiguredNodeAlertsAreIndependentAndNameChain(t *testing.T) {
	var messages []string
	m := configuredMonitor(t, func(r *http.Request) (*http.Response, error) {
		var payload struct {
			Text string `json:"text"`
		}
		_ = json.NewDecoder(r.Body).Decode(&payload)
		messages = append(messages, payload.Text)
		return response(http.StatusOK, `{"ok":true}`), nil
	})
	m.configureNodes(false, false, "")
	m.configureAdditionalNodes(map[string]nodeConfig{
		nodeLitecoin: {ID: nodeLitecoin, Enabled: true, Supported: true, DataDir: t.TempDir()},
		nodeDogecoin: {ID: nodeDogecoin, Enabled: true, Supported: true, DataDir: t.TempDir()},
	})
	m.settings.AlertNewBlockLitecoin = true
	m.settings.AlertNewBlockDogecoin = false
	m.healthTicks = 1
	first := map[string]map[string]any{
		nodeLitecoin: {"active": true, "initialblockdownload": false, "blocks": float64(100)},
		nodeDogecoin: {"active": true, "initialblockdownload": false, "blocks": float64(200)},
	}
	second := map[string]map[string]any{
		nodeLitecoin: {"active": true, "initialblockdownload": false, "blocks": float64(101)},
		nodeDogecoin: {"active": true, "initialblockdownload": false, "blocks": float64(201)},
	}
	m.tickConfiguredNodes(t.Context(), first)
	m.tickConfiguredNodes(t.Context(), second)
	if got := strings.Join(messages, "\n"); got != "[alice] Litecoin: 🟧 new block 101" {
		t.Fatalf("messages = %q", got)
	}
}

func TestConfiguredNodeMonitorSuppressesPendingTransitionAndResumesAfterFailure(t *testing.T) {
	var messages []string
	m := configuredMonitor(t, func(r *http.Request) (*http.Response, error) {
		var payload struct {
			Text string `json:"text"`
		}
		_ = json.NewDecoder(r.Body).Decode(&payload)
		messages = append(messages, payload.Text)
		return response(http.StatusOK, `{"ok":true}`), nil
	})
	dir := t.TempDir()
	node := nodeConfig{ID: nodeLitecoin, Enabled: true, Supported: true, DataDir: t.TempDir()}
	a := &app{cfg: config{stateDir: dir, nodes: map[string]nodeConfig{nodeLitecoin: node}}}
	m.configureNodes(false, false, "")
	m.configureAdditionalNodes(map[string]nodeConfig{nodeLitecoin: node})
	m.transitioning = a.nodeTransitioning
	m.healthTicks = 1
	state := m.extraStates[nodeLitecoin]
	state.chainKnown, state.ibd = true, false
	request := fmt.Sprintf(`{"id":"11111111111111111111111111111111","node":"litecoin","active":false,"confirmation":"deactivate","requested_at":%d}`, time.Now().Unix())
	if err := os.WriteFile(a.nodeControlPath("processing"), []byte(request), 0o600); err != nil {
		t.Fatal(err)
	}
	for range downAfterTicks + 1 {
		m.tickConfiguredNodes(t.Context(), map[string]map[string]any{nodeLitecoin: nil})
	}
	if state.fails != 0 || len(messages) != 0 || m.healthTicks != 1 {
		t.Fatalf("pending transition changed monitor state=%#v messages=%q healthTicks=%d", state, messages, m.healthTicks)
	}
	if err := os.Remove(a.nodeControlPath("processing")); err != nil {
		t.Fatal(err)
	}
	failed := `{"id":"11111111111111111111111111111111","node":"litecoin","active":false,"state":"failed"}`
	if err := os.WriteFile(filepath.Join(dir, "node-control.status"), []byte(failed), 0o600); err != nil {
		t.Fatal(err)
	}
	for range downAfterTicks {
		m.tickConfiguredNodes(t.Context(), map[string]map[string]any{nodeLitecoin: nil})
	}
	if !state.downAlerted || !strings.Contains(strings.Join(messages, "\n"), "Litecoin: 🔴 node down") {
		t.Fatalf("failed transition did not resume monitoring: state=%#v messages=%q", state, messages)
	}
}

func TestConfiguredNodeMonitorSkipsDeactivatedNode(t *testing.T) {
	requests := 0
	m := configuredMonitor(t, func(*http.Request) (*http.Response, error) {
		requests++
		return response(http.StatusOK, `{"ok":true}`), nil
	})
	m.configureNodes(false, false, "")
	m.configureAdditionalNodes(map[string]nodeConfig{
		nodeTari: {ID: nodeTari, Enabled: true, Supported: true, DataDir: t.TempDir()},
	})
	m.settings.HealthchecksEnabled = true
	m.settings.HealthchecksURL = "https://hc-ping.com/shared"
	m.healthTicks = 1
	for range downAfterTicks + 1 {
		m.tickConfiguredNodes(t.Context(), map[string]map[string]any{nodeTari: {"active": false}})
	}
	state := m.extraStates[nodeTari]
	if requests != 0 || state.fails != 0 || state.downAlerted || m.healthTicks != 1 {
		t.Fatalf("inactive node caused requests=%d state=%#v healthTicks=%d", requests, state, m.healthTicks)
	}
}

func TestZcashMonitorStateSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	m := NewMonitor("1.2.0", t.TempDir(), dir)
	m.configureAdditionalNodes(map[string]nodeConfig{nodeZcash: {ID: nodeZcash, Enabled: true, Supported: true}})
	m.extraStates[nodeZcash].fails = 2
	m.extraStates[nodeZcash].lastBlock = 123
	m.extraStates[nodeZcash].hasLastBlock = true
	m.saveState()
	restored := NewMonitor("1.2.0", t.TempDir(), dir)
	restored.configureAdditionalNodes(map[string]nodeConfig{nodeZcash: {ID: nodeZcash, Enabled: true, Supported: true}})
	state := restored.extraStates[nodeZcash]
	if state == nil || state.fails != 2 || state.lastBlock != 123 || !state.hasLastBlock {
		t.Fatalf("restored Zcash monitor state = %#v", state)
	}
}

func TestLastSeenHeightAndProgressSurviveRestartAndOutages(t *testing.T) {
	t.Setenv("HEALTHCHECKS_URL", "")
	dir := t.TempDir()
	nodes := map[string]nodeConfig{nodeTari: {ID: nodeTari, Enabled: true, Supported: true}, nodeLitecoin: {ID: nodeLitecoin, Enabled: true, Supported: true}}
	m := NewMonitor("1.2.0", t.TempDir(), dir)
	m.configureNodes(true, false, "")
	m.configureAdditionalNodes(nodes)
	m.output = io.Discard
	m.healthTicks = 1
	m.diskFreeGB = func(string) (float64, error) { return 1000, nil }
	m.tickConfiguredNodes(t.Context(), map[string]map[string]any{
		nodeBitcoin: {"initialblockdownload": false, "blocks": float64(900000), "verificationprogress": 0.9999},
		nodeTari:    {"active": true, "initialblockdownload": true, "blocks": int64(150000), "sync_progress": 0.5},
	})
	if seen := m.LastSeen(nodeTari); seen.LastBlock != 150000 || !seen.HasLastBlock || seen.SyncPercent != 50 {
		t.Fatalf("Tari last seen before restart = %#v", seen)
	}
	m.tickConfiguredNodes(t.Context(), map[string]map[string]any{nodeBitcoin: nil, nodeTari: nil, nodeLitecoin: nil})

	restarted := NewMonitor("1.2.0", t.TempDir(), dir)
	for id, want := range map[string][2]float64{nodeBitcoin: {900000, 100}, nodeTari: {150000, 50}} {
		if seen := restarted.LastSeen(id); !seen.HasLastBlock || float64(seen.LastBlock) != want[0] || seen.SyncPercent != want[1] {
			t.Fatalf("%s last seen after restart = %#v", id, seen)
		}
	}
	if seen := restarted.LastSeen(nodeLitecoin); seen.HasLastBlock {
		t.Fatalf("a node that never answered has a last height: %#v", seen)
	}
	var missing *Monitor
	if seen := missing.LastSeen(nodeBitcoin); seen.HasLastBlock {
		t.Fatalf("nil monitor last seen = %#v", seen)
	}
}
