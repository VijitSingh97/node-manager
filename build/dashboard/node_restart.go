package main

import (
	"os"
	"path/filepath"
	"slices"
	"time"
)

const nodeRestartWindow = 10 * time.Minute

type nodeRestartEvent struct {
	nodes   []string
	since   time.Time
	pending bool
}

func (a *app) controlFileTime(prefix string, names ...string) (time.Time, bool) {
	for _, name := range names {
		if info, err := os.Lstat(filepath.Join(a.cfg.stateDir, prefix+"."+name)); err == nil {
			return info.ModTime(), true
		}
	}
	return time.Time{}, false
}

func hostRestartNodes(status hostControlStatus, pending bool) []string {
	switch status.Action {
	case "clearnet_over_tor", "inbound_connections", "apply_config":
		if pending || status.Restarted == nil {
			return nodeOrder
		}
		var nodes []string
		for _, id := range nodeOrder {
			if slices.Contains(status.Restarted, id) {
				nodes = append(nodes, id)
			}
		}
		return nodes
	case "install", "migrate":
		return []string{status.Node}
	}
	return nil
}

func (a *app) nodeRestartEvents() []nodeRestartEvent {
	var events []nodeRestartEvent
	add := func(prefix string, nodes []string, pending bool) {
		names := []string{"status"}
		if pending {
			names = []string{"request", "processing"}
		}
		if since, ok := a.controlFileTime(prefix, names...); ok && len(nodes) > 0 {
			events = append(events, nodeRestartEvent{nodes, since, pending})
		}
	}
	if status, pending := a.hostControlStatus(); status != nil && (pending || status.State == "applied") {
		add("node-control", hostRestartNodes(*status, pending), pending)
	}
	if status, pending := a.pendingNodeControlStatus(); pending {
		if status.Active && status.ID != "" {
			add("node-control", []string{status.Node}, true)
		}
	} else if status := a.readNodeControlStatus(); status != nil && status.State == "applied" && status.Active {
		add("node-control", []string{status.Node}, false)
	}
	credentialNodes := func(node string) []string {
		if node == nodeZcash {
			return nil
		}
		return []string{node}
	}
	if status, pending := a.pendingCredentials(); pending {
		if status.ID != "" {
			add("credential-control", credentialNodes(status.Node), true)
		}
	} else if saved, ok := a.savedCredentialStatus(); ok && saved.State == "applied" {
		add("credential-control", credentialNodes(saved.Node), false)
	}
	return events
}

func (a *app) nodeAnsweredSince(id string, since time.Time) bool {
	a.nodeHealthMu.RLock()
	status, ok := a.nodeHealth[id]
	a.nodeHealthMu.RUnlock()
	return ok && status.Available && status.UpdatedAt.After(since)
}

// A node counts as restarting from the moment a change that restarts it is requested until its RPC answers again, for at most nodeRestartWindow.
func (a *app) restartingNodes() map[string]bool {
	restarting := map[string]bool{}
	now := time.Now()
	runtime := a.nodeRuntime()
	for _, event := range a.nodeRestartEvents() {
		if now.Sub(event.since) > nodeRestartWindow {
			continue
		}
		for _, id := range event.nodes {
			configured, supported := a.nodeConfigured(id)
			if configured && supported && runtime[id] && (event.pending || !a.nodeAnsweredSince(id, event.since)) {
				restarting[id] = true
			}
		}
	}
	return restarting
}

func (a *app) nodeRestarting(id string) bool {
	return a.restartingNodes()[id]
}
