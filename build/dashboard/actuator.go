package main

import (
	"encoding/json"
	"net/http"
	"time"
)

const (
	statusUp       = "UP"
	statusDegraded = "DEGRADED"
	statusDown     = "DOWN"
	statusOff      = "OFF"
)

type nodeInfoSnapshot struct {
	Network  string
	Height   *int64
	Progress *float64
	Syncing  *bool
	Peers    *int64
	Version  string
}

type actuatorComponent struct {
	Status  string            `json:"status"`
	Details map[string]string `json:"details,omitempty"`
}

type actuatorChain struct {
	id           string
	state        string
	Name         string   `json:"name"`
	Active       bool     `json:"active"`
	Status       string   `json:"status"`
	Network      string   `json:"network,omitempty"`
	Height       *int64   `json:"height,omitempty"`
	SyncProgress *float64 `json:"sync_progress,omitempty"`
	Peers        *int64   `json:"peers,omitempty"`
	Version      string   `json:"version,omitempty"`
}

func (a *app) healthEndpointsEnabled() bool {
	return a.monitor != nil && a.monitor.notificationSettings().HealthEndpoints
}

func anonymousHealthRequest(r *http.Request) bool {
	return r.URL.EscapedPath() == "/actuator/health"
}

func (a *app) updateNodeInfo(id string, update func(*nodeInfoSnapshot)) {
	a.nodeHealthMu.Lock()
	defer a.nodeHealthMu.Unlock()
	if a.nodeInfo == nil {
		a.nodeInfo = make(map[string]nodeInfoSnapshot)
	}
	snapshot := a.nodeInfo[id]
	update(&snapshot)
	a.nodeInfo[id] = snapshot
}

func (a *app) noteNodeNetwork(id, version string, peers int64) {
	a.updateNodeInfo(id, func(snapshot *nodeInfoSnapshot) {
		if version != "" {
			snapshot.Version = version
		}
		snapshot.Peers = &peers
	})
}

func (a *app) recordNodeInfo(id string, probe func() map[string]any) func() map[string]any {
	return func() map[string]any {
		info := probe()
		if info == nil || info["active"] == false {
			return info
		}
		a.updateNodeInfo(id, func(snapshot *nodeInfoSnapshot) {
			if height, ok := integer(info["blocks"]); ok {
				snapshot.Height = &height
			}
			if syncing, ok := info["initialblockdownload"].(bool); ok {
				snapshot.Syncing = &syncing
			}
			for _, key := range []string{"verificationprogress", "sync_progress"} {
				if progress, ok := info[key].(float64); ok {
					progress = min(1, max(0, progress))
					snapshot.Progress = &progress
				}
			}
			for _, key := range []string{"chain", "network"} {
				if network, ok := info[key].(string); ok && network != "" {
					snapshot.Network = network
				}
			}
			if peers, ok := integer(info["peers"]); ok {
				snapshot.Peers = &peers
			}
			if version, ok := info["version"].(string); ok && version != "" {
				snapshot.Version = version
			}
		})
		return info
	}
}

// Unlike nodeAvailable, this ignores node-runtime.json changes so activating one node never marks the others down.
func (a *app) nodeAnsweredRecently(id string) bool {
	a.nodeHealthMu.RLock()
	status, ok := a.nodeHealth[id]
	a.nodeHealthMu.RUnlock()
	age := time.Since(status.UpdatedAt)
	return ok && status.Available && age >= 0 && age <= nodeHealthFreshFor
}

func (a *app) actuatorChains() []actuatorChain {
	runtime := a.nodeRuntime()
	var chains []actuatorChain
	for _, id := range nodeOrder {
		configured, supported := a.nodeConfigured(id)
		if !configured || !supported {
			continue
		}
		chain := actuatorChain{id: id, Name: nodeDisplayName(id), Active: runtime[id], Status: statusOff, state: "off"}
		if chain.Active {
			chain.Status, chain.state = statusDown, "down"
			switch {
			case a.monitorTransitioning(id):
				chain.Status, chain.state = statusDegraded, "busy"
			case a.nodeAnsweredRecently(id):
				a.nodeHealthMu.RLock()
				snapshot := a.nodeInfo[id]
				a.nodeHealthMu.RUnlock()
				chain.Status, chain.state = statusUp, "synced"
				if snapshot.Syncing != nil && *snapshot.Syncing {
					chain.Status, chain.state = statusDegraded, "syncing"
				}
				chain.Network, chain.Height, chain.SyncProgress, chain.Peers, chain.Version = snapshot.Network, snapshot.Height, snapshot.Progress, snapshot.Peers, snapshot.Version
			}
		}
		chains = append(chains, chain)
	}
	return chains
}

func balanceIndexComponent(state string) actuatorComponent {
	status := statusDegraded
	if state == utxoStateReady {
		status = statusUp
	}
	return actuatorComponent{Status: status, Details: map[string]string{"state": state}}
}

func aggregateStatus(components map[string]actuatorComponent) string {
	overall := statusUp
	for _, component := range components {
		if component.Status == statusDown {
			return statusDown
		}
		if component.Status == statusDegraded {
			overall = statusDegraded
		}
	}
	return overall
}

func (a *app) healthComponents() map[string]actuatorComponent {
	components := map[string]actuatorComponent{"dashboard": {Status: statusUp}}
	chains := a.actuatorChains()
	for _, chain := range chains {
		if chain.Active {
			components[chain.id] = actuatorComponent{Status: chain.Status, Details: map[string]string{"state": chain.state}}
		}
	}
	if len(chains) > 0 && a.prices != nil {
		tor := actuatorComponent{Status: statusUp}
		if a.prices.SnapshotAsset("bitcoin")["stale"] == true {
			tor.Status = statusDegraded
		}
		components["tor"] = tor
	}
	if state := a.utxo.Status().State; state != utxoStateOff {
		components["balance_index"] = balanceIndexComponent(state)
	}
	return components
}

func (a *app) actuatorHealth(w http.ResponseWriter, r *http.Request) {
	if !a.healthEndpointsEnabled() {
		http.NotFound(w, r)
		return
	}
	components := a.healthComponents()
	status := aggregateStatus(components)
	body := map[string]any{"status": status}
	if a.configurationAllowed(r) {
		body["components"] = components
	}
	w.Header().Set("Content-Type", "application/json")
	if status == statusDown {
		w.WriteHeader(http.StatusServiceUnavailable)
	}
	_ = json.NewEncoder(w).Encode(body)
}

func (a *app) installKind() string {
	capability, ok := a.hostControlCapability()
	if !ok {
		return "unknown"
	}
	switch capability.BootKind {
	case "boot":
		return "apt"
	case "login":
		return "homebrew"
	}
	return "source"
}

func (a *app) actuatorInfo(w http.ResponseWriter, r *http.Request) {
	if !a.healthEndpointsEnabled() {
		http.NotFound(w, r)
		return
	}
	chains := map[string]actuatorChain{}
	for _, chain := range a.actuatorChains() {
		chains[chain.id] = chain
	}
	writeJSON(w, map[string]any{
		"app":    map[string]string{"name": "node-manager", "version": versionLabel(a.cfg.version), "install_kind": a.installKind()},
		"chains": chains,
	})
}
