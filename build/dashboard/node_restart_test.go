package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func restartTestApp(t *testing.T) *app {
	t.Helper()
	nodes := map[string]nodeConfig{
		nodeLitecoin: {ID: nodeLitecoin, Name: "Litecoin", Enabled: true, Supported: true},
		nodeDogecoin: {ID: nodeDogecoin, Name: "Dogecoin", Enabled: true, Supported: true},
		nodeZcash:    {ID: nodeZcash, Name: "Zcash", Enabled: true, Supported: true},
	}
	a := &app{cfg: config{bitcoinDisabled: true, dashboardPassword: "secret", stateDir: t.TempDir(), nodes: nodes}}
	initializeTestDashboardAuth(t, a)
	return a
}

func writeControlFile(t *testing.T, path string, value any, age time.Duration) {
	t.Helper()
	b, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-age)
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
}

func hostRequestFile(t *testing.T, a *app, action, node string, age time.Duration) {
	t.Helper()
	enabled := false
	request := hostControlRequest{ID: "0123456789abcdef0123456789abcdef", Action: action, Node: node, RequestedAt: time.Now().Unix()}
	if action == "clearnet_over_tor" || action == "inbound_connections" || action == "launch_at_boot" {
		request.Enabled = &enabled
	}
	if action == "migrate" {
		request.DataDir = "/srv/data"
	}
	if action == "apply_config" {
		request.Revision = strings.Repeat("a", 64)
	}
	writeControlFile(t, a.nodeControlPath("request"), request, age)
}

func hostStatusFile(t *testing.T, a *app, action string, state string, age time.Duration) {
	t.Helper()
	writeControlFile(t, a.nodeControlPath("status"), hostControlStatus{ID: "0123456789abcdef0123456789abcdef", Action: action, State: state}, age)
}

func restartingSet(a *app) []string {
	var ids []string
	for _, id := range nodeOrder {
		if a.restartingNodes()[id] {
			ids = append(ids, id)
		}
	}
	return ids
}

func equalIDs(got []string, want ...string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func TestPendingNetworkChangeRestartsEveryActiveNode(t *testing.T) {
	for _, action := range []string{"clearnet_over_tor", "inbound_connections"} {
		a := restartTestApp(t)
		hostRequestFile(t, a, action, "", 0)
		if got := restartingSet(a); !equalIDs(got, nodeLitecoin, nodeDogecoin, nodeZcash) {
			t.Fatalf("%s restarting = %v", action, got)
		}
	}
}

func TestInactiveNodesAreNotRestarting(t *testing.T) {
	a := restartTestApp(t)
	writeControlFile(t, filepath.Join(a.cfg.stateDir, "node-runtime.json"), map[string]bool{nodeDogecoin: false}, 0)
	hostRequestFile(t, a, "apply_config", "", 0)
	if got := restartingSet(a); !equalIDs(got, nodeLitecoin, nodeZcash) {
		t.Fatalf("restarting = %v", got)
	}
}

func TestInstallAndMigrateRestartOnlyTheirNode(t *testing.T) {
	for _, action := range []string{"install", "migrate"} {
		a := restartTestApp(t)
		hostRequestFile(t, a, action, nodeLitecoin, 0)
		if got := restartingSet(a); !equalIDs(got, nodeLitecoin) {
			t.Fatalf("%s restarting = %v", action, got)
		}
	}
}

func TestChainstateCopyMarksOnlyItsNodeRestartingUntilItAnswersAfterTheStart(t *testing.T) {
	a := restartTestApp(t)
	mark := filepath.Join(a.cfg.stateDir, nodeDogecoin+"-chainstate-copy")
	writeControlFile(t, mark+".processing", nil, time.Minute)
	writeControlFile(t, filepath.Join(a.cfg.stateDir, nodeLitecoin+"-chainstate-copy.processing"), nil, time.Minute)
	a.recordNodeHealth(nodeDogecoin, true)
	if got := restartingSet(a); !equalIDs(got, nodeDogecoin) {
		t.Fatalf("while the copy has the node down = %v", got)
	}
	if err := os.Remove(mark + ".processing"); err != nil {
		t.Fatal(err)
	}
	writeControlFile(t, mark+".status", nil, time.Minute)
	a.nodeHealth[nodeDogecoin] = nodeHealthSnapshot{Available: true, UpdatedAt: time.Now().Add(-2 * time.Minute)}
	if got := restartingSet(a); !equalIDs(got, nodeDogecoin) {
		t.Fatalf("after the start, before an answer = %v", got)
	}
	a.recordNodeHealth(nodeDogecoin, true)
	if a.restartingNodes()[nodeDogecoin] {
		t.Fatal("the node still counts as restarting after it answered")
	}
	if err := os.Remove(mark + ".status"); err != nil {
		t.Fatal(err)
	}
	writeControlFile(t, mark+".processing", nil, nodeRestartWindow+time.Minute)
	if a.restartingNodes()[nodeDogecoin] {
		t.Fatal("a mark left by a killed copy held back alerts past the window")
	}
}

func TestHostChangesThatLeaveNodesRunningDoNotMarkRestarting(t *testing.T) {
	for _, action := range []string{"launch_at_boot", "utxo_index", "utxo_seed"} {
		a := restartTestApp(t)
		hostRequestFile(t, a, action, "", 0)
		if action == "utxo_seed" {
			hostRequestFile(t, a, action, nodeLitecoin, 0)
		}
		if got := restartingSet(a); len(got) != 0 {
			t.Fatalf("%s restarting = %v", action, got)
		}
	}
	a := restartTestApp(t)
	hostStatusFile(t, a, "launch_at_boot", "applied", time.Second)
	hostStatusFile(t, a, "clearnet_over_tor", "rejected", time.Second)
	if got := restartingSet(a); len(got) != 0 {
		t.Fatalf("rejected change restarting = %v", got)
	}
	hostStatusFile(t, a, "clearnet_over_tor", "failed", time.Second)
	if got := restartingSet(a); len(got) != 0 {
		t.Fatalf("failed change restarting = %v", got)
	}
}

func TestAppliedChangeKeepsNodesRestartingUntilTheyAnswer(t *testing.T) {
	a := restartTestApp(t)
	hostStatusFile(t, a, "inbound_connections", "applied", time.Minute)
	if got := restartingSet(a); !equalIDs(got, nodeLitecoin, nodeDogecoin, nodeZcash) {
		t.Fatalf("before any answer = %v", got)
	}
	a.recordNodeHealth(nodeLitecoin, false)
	if !a.restartingNodes()[nodeLitecoin] {
		t.Fatal("a failed poll ended the restart")
	}
	a.nodeHealth[nodeDogecoin] = nodeHealthSnapshot{Available: true, UpdatedAt: time.Now().Add(-2 * time.Minute)}
	if !a.restartingNodes()[nodeDogecoin] {
		t.Fatal("an answer from before the change ended the restart")
	}
	a.recordNodeHealth(nodeLitecoin, true)
	if got := restartingSet(a); !equalIDs(got, nodeDogecoin, nodeZcash) {
		t.Fatalf("after litecoin answered = %v", got)
	}
}

func TestRestartingEndsAtTheWindow(t *testing.T) {
	a := restartTestApp(t)
	hostStatusFile(t, a, "apply_config", "applied", nodeRestartWindow+time.Second)
	if got := restartingSet(a); len(got) != 0 {
		t.Fatalf("expired applied change = %v", got)
	}
	hostRequestFile(t, a, "apply_config", "", nodeRestartWindow+time.Second)
	if got := restartingSet(a); len(got) != 0 {
		t.Fatalf("expired pending change = %v", got)
	}
	hostRequestFile(t, a, "apply_config", "", nodeRestartWindow-time.Minute)
	if got := restartingSet(a); len(got) != 3 {
		t.Fatalf("recent pending change = %v", got)
	}
}

func TestNodeActivationRestartsOnlyThatNode(t *testing.T) {
	a := restartTestApp(t)
	id := "0123456789abcdef0123456789abcdef"
	request := nodeControlRequest{ID: id, Node: nodeLitecoin, Active: false, Confirmation: "deactivate", RequestedAt: time.Now().Unix()}
	writeControlFile(t, a.nodeControlPath("request"), request, 0)
	if got := restartingSet(a); len(got) != 0 {
		t.Fatalf("deactivating = %v", got)
	}
	request.Active, request.Confirmation = true, ""
	writeControlFile(t, a.nodeControlPath("request"), request, 0)
	if got := restartingSet(a); !equalIDs(got, nodeLitecoin) {
		t.Fatalf("activating = %v", got)
	}
	os.Remove(a.nodeControlPath("request"))
	writeControlFile(t, a.nodeControlPath("status"), nodeControlStatus{ID: id, Node: nodeDogecoin, Active: true, State: "applied"}, time.Second)
	if got := restartingSet(a); !equalIDs(got, nodeDogecoin) {
		t.Fatalf("activated = %v", got)
	}
	writeControlFile(t, a.nodeControlPath("status"), nodeControlStatus{ID: id, Node: nodeDogecoin, Active: false, State: "applied"}, time.Second)
	if got := restartingSet(a); len(got) != 0 {
		t.Fatalf("deactivated = %v", got)
	}
}

func startingSet(a *app) []string {
	_, starting := a.restartStates()
	var ids []string
	for _, id := range nodeOrder {
		if starting[id] {
			ids = append(ids, id)
		}
	}
	return ids
}

func TestActivationIsStartingAndSettingsChangesAreNot(t *testing.T) {
	a := restartTestApp(t)
	id := "0123456789abcdef0123456789abcdef"
	writeControlFile(t, a.nodeControlPath("status"), nodeControlStatus{ID: id, Node: nodeLitecoin, Active: true, State: "applied"}, time.Second)
	if got := startingSet(a); !equalIDs(got, nodeLitecoin) {
		t.Fatalf("activated = %v", got)
	}
	response := nodeControlHTTP(a, http.MethodGet, "/api/nodes", "", false)
	var view struct {
		Nodes []nodeControlNode `json:"nodes"`
	}
	if json.Unmarshal(response.Body.Bytes(), &view) != nil {
		t.Fatal(response.Body.String())
	}
	for _, node := range view.Nodes {
		if node.Starting != (node.ID == nodeLitecoin) || node.Restarting != (node.ID == nodeLitecoin) {
			t.Fatalf("%s restarting=%v starting=%v", node.ID, node.Restarting, node.Starting)
		}
	}
	b := restartTestApp(t)
	hostStatusFile(t, b, "apply_config", "applied", time.Second)
	if got := restartingSet(b); len(got) != 3 || len(startingSet(b)) != 0 {
		t.Fatalf("settings change restarting = %v starting = %v", got, startingSet(b))
	}
}

func TestNewestRestartEventDecidesStarting(t *testing.T) {
	id := "0123456789abcdef0123456789abcdef"
	a := restartTestApp(t)
	writeControlFile(t, a.nodeControlPath("status"), nodeControlStatus{ID: id, Node: nodeLitecoin, Active: true, State: "applied"}, time.Minute)
	writeControlFile(t, a.credentialPath("status"), credentialStatus{ID: id, Node: nodeLitecoin, State: "applied"}, time.Second)
	if got := restartingSet(a); !equalIDs(got, nodeLitecoin) || len(startingSet(a)) != 0 {
		t.Fatalf("newer settings change: restarting = %v starting = %v", got, startingSet(a))
	}
	b := restartTestApp(t)
	writeControlFile(t, b.nodeControlPath("status"), nodeControlStatus{ID: id, Node: nodeLitecoin, Active: true, State: "applied"}, time.Second)
	writeControlFile(t, b.credentialPath("status"), credentialStatus{ID: id, Node: nodeLitecoin, State: "applied"}, time.Minute)
	if got := startingSet(b); !equalIDs(got, nodeLitecoin) {
		t.Fatalf("newer activation: starting = %v", got)
	}
}

func TestCredentialResetRestartsItsNodeButNotZcash(t *testing.T) {
	a := restartTestApp(t)
	id := "0123456789abcdef0123456789abcdef"
	writeControlFile(t, a.credentialPath("request"), credentialRequest{ID: id, Node: nodeLitecoin, RequestedAt: time.Now().Unix()}, 0)
	if got := restartingSet(a); !equalIDs(got, nodeLitecoin) {
		t.Fatalf("pending reset = %v", got)
	}
	os.Remove(a.credentialPath("request"))
	writeControlFile(t, a.credentialPath("status"), credentialStatus{ID: id, Node: nodeDogecoin, State: "applied"}, time.Second)
	if got := restartingSet(a); !equalIDs(got, nodeDogecoin) {
		t.Fatalf("applied reset = %v", got)
	}
	writeControlFile(t, a.credentialPath("status"), credentialStatus{ID: id, Node: nodeZcash, State: "applied"}, time.Second)
	if got := restartingSet(a); len(got) != 0 {
		t.Fatalf("zcash reset = %v", got)
	}
}

func TestPortChangesDoNotMarkNodesRestarting(t *testing.T) {
	a := restartTestApp(t)
	writeControlFile(t, a.portControlPath("request"), portControlRequest{ID: "0123456789abcdef0123456789abcdef", RequestedAt: time.Now().Unix()}, 0)
	if got := restartingSet(a); len(got) != 0 {
		t.Fatalf("restarting = %v", got)
	}
}

func TestRestartingNodesAreFlaggedInAPINodesAndSilenceAlerts(t *testing.T) {
	a := restartTestApp(t)
	hostRequestFile(t, a, "clearnet_over_tor", "", 0)
	response := nodeControlHTTP(a, http.MethodGet, "/api/nodes", "", false)
	var view struct {
		Nodes []nodeControlNode `json:"nodes"`
	}
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &view) != nil {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
	flags := map[string]bool{}
	for _, node := range view.Nodes {
		flags[node.ID] = node.Restarting
	}
	if !flags[nodeLitecoin] || !flags[nodeDogecoin] || flags[nodeBitcoin] || flags[nodeMonero] {
		t.Fatalf("restarting flags = %v", flags)
	}
	if !a.nodeRestarting(nodeLitecoin) || a.nodeRestarting(nodeBitcoin) {
		t.Fatal("alerts are not silenced exactly for restarting nodes")
	}
	os.Remove(a.nodeControlPath("request"))
	if a.nodeRestarting(nodeLitecoin) {
		t.Fatal("alerts stay silenced after the restart")
	}
}

func TestKnownStartFailureOutranksRestarting(t *testing.T) {
	a := restartTestApp(t)
	hostStatusFile(t, a, "apply_config", "applied", time.Second)
	writeControlFile(t, filepath.Join(a.cfg.stateDir, "host-stats.json"), hostStatsFile{
		Updated: time.Now().Unix(), CPUs: 4, MemTotalBytes: 1 << 30,
		Running: map[string]bool{nodeLitecoin: false, nodeDogecoin: false},
		Failed:  map[string]string{nodeLitecoin: "needs adx"},
	}, 0)
	response := nodeControlHTTP(a, http.MethodGet, "/api/nodes", "", false)
	var view struct {
		Nodes []nodeControlNode `json:"nodes"`
	}
	if json.Unmarshal(response.Body.Bytes(), &view) != nil {
		t.Fatal(response.Body.String())
	}
	for _, node := range view.Nodes {
		switch node.ID {
		case nodeLitecoin:
			if node.Restarting || !node.Stopped || node.Failure == "" {
				t.Fatalf("litecoin = %#v", node)
			}
		case nodeDogecoin:
			if !node.Restarting {
				t.Fatalf("dogecoin = %#v", node)
			}
		}
	}
}

func TestMonitorKeepsProbingRestartingNodesSoTheyClear(t *testing.T) {
	a := restartTestApp(t)
	hostStatusFile(t, a, "apply_config", "applied", time.Minute)
	m := configuredMonitor(t, func(*http.Request) (*http.Response, error) {
		return response(http.StatusOK, `{"ok":true}`), nil
	})
	m.tickEvery = time.Millisecond
	m.transitioning, m.restarting = a.monitorTransitioning, a.nodeRestarting
	var probes atomic.Int32
	probe := func() map[string]any {
		probes.Add(1)
		a.recordNodeHealth(nodeLitecoin, true)
		return map[string]any{"active": true}
	}
	if !a.nodeRestarting(nodeLitecoin) {
		t.Fatal("litecoin is not restarting before the first probe")
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go m.RunConfiguredNodes(ctx, map[string]func() map[string]any{nodeLitecoin: probe})
	deadline := time.Now().Add(2 * time.Second)
	for a.nodeRestarting(nodeLitecoin) && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if a.nodeRestarting(nodeLitecoin) || probes.Load() == 0 {
		t.Fatalf("restarting node was not probed (probes=%d)", probes.Load())
	}
}

func TestMonitorSuppressesAlertsForRestartingNodesOnly(t *testing.T) {
	m := &Monitor{}
	m.restarting = func(id string) bool { return id == nodeLitecoin }
	if !m.alertsSuppressed(nodeLitecoin) || m.alertsSuppressed(nodeDogecoin) {
		t.Fatal("restarting nodes are not suppressed exactly")
	}
	m.restarting = nil
	m.transitioning = func(id string) bool { return id == nodeDogecoin }
	if m.alertsSuppressed(nodeLitecoin) || !m.alertsSuppressed(nodeDogecoin) {
		t.Fatal("transitioning nodes are not suppressed exactly")
	}
}

func TestAppliedChangeRestartsOnlyRecreatedServices(t *testing.T) {
	a := restartTestApp(t)
	id := "0123456789abcdef0123456789abcdef"
	status := func(restarted []string) {
		value := map[string]any{"id": id, "action": "apply_config", "state": "applied"}
		if restarted != nil {
			value["restarted"] = restarted
		}
		writeControlFile(t, a.nodeControlPath("status"), value, time.Minute)
	}
	status([]string{"tor", nodeDogecoin, "dashboard", "bogus"})
	if got := restartingSet(a); !equalIDs(got, nodeDogecoin) {
		t.Fatalf("recreated dogecoin = %v", got)
	}
	status([]string{})
	if got := restartingSet(a); len(got) != 0 {
		t.Fatalf("nothing recreated = %v", got)
	}
	status(nil)
	if got := restartingSet(a); !equalIDs(got, nodeLitecoin, nodeDogecoin, nodeZcash) {
		t.Fatalf("unknown recreated set = %v", got)
	}
}
