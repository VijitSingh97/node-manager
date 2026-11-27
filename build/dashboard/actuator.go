package main

import (
	"encoding/json"
	"net"
	"net/http"
	"strconv"
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

	Services []actuatorService `json:"services,omitempty"`
}

type actuatorService struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Exposed     bool   `json:"exposed"`
	Protocol    string `json:"protocol"`
	Auth        string `json:"auth"`
	BindAddress string `json:"bind_address,omitempty"`
	Port        int    `json:"port,omitempty"`
	URI         string `json:"uri,omitempty"`
	Username    string `json:"username,omitempty"`
	Password    string `json:"password,omitempty"`
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
				stale, _ := info["tip_stale"].(bool)
				syncing = syncing || stale
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
			case a.monitorTransitioning(id) || a.nodeRestarting(id):
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
		if price := a.prices.SnapshotAsset("bitcoin"); price["stale"] == true || price["refresh_failed"] == true {
			tor.Status = statusDegraded
		}
		components["tor"] = tor
	}
	if state := a.utxo.Status().State; state != utxoStateOff {
		components["balance_index"] = balanceIndexComponent(state)
	}
	return components
}

func (a *app) actuatorHealth(w http.ResponseWriter, _ *http.Request) {
	components := a.healthComponents()
	status := aggregateStatus(components)
	body := map[string]any{"status": status, "components": components}
	w.Header().Set("Content-Type", "application/json")
	if status == statusDown {
		w.WriteHeader(http.StatusServiceUnavailable)
	}
	_ = json.NewEncoder(w).Encode(body)
}

type lanServiceSpec struct {
	id, scheme, protocol, auth string
}

var nodeLANServices = map[string][]lanServiceSpec{
	nodeBitcoin:     {{"bitcoin_rpc", "http", "json-rpc", "basic"}},
	nodeMonero:      {{"monero_rpc", "http", "json-rpc", "digest"}, {"monero_zmq", "tcp", "zmq", "none"}},
	nodeTari:        {{"tari_grpc", "", "grpc", "none"}},
	nodeLitecoin:    {{"litecoin_rpc", "http", "json-rpc", "basic"}},
	nodeDogecoin:    {{"dogecoin_rpc", "http", "json-rpc", "basic"}},
	nodeBitcoinCash: {{"bitcoincash_rpc", "http", "json-rpc", "basic"}},
	nodeZcash:       {{"zcash_rpc", "http", "json-rpc", "basic"}},
}

func (a *app) lanEndpoint(node, id string) string {
	switch id {
	case "bitcoin_rpc":
		return a.cfg.bitcoinRPCEndpoint
	case "monero_rpc":
		return a.cfg.moneroRPCEndpoint
	case "monero_zmq":
		return a.cfg.moneroZMQEndpoint
	}
	return a.cfg.nodes[node].RPCEndpoint
}

func (a *app) lanServices(node string) []actuatorService {
	user, password, available := a.interfaceCredentials(node)
	var services []actuatorService
	for _, spec := range nodeLANServices[node] {
		service := actuatorService{ID: spec.id, Name: portControlLabel(spec.id), Protocol: spec.protocol, Auth: spec.auth}
		if host, portText, err := net.SplitHostPort(a.lanEndpoint(node, spec.id)); err == nil {
			service.Exposed, service.BindAddress = true, host
			service.Port, _ = strconv.Atoi(portText)
			service.URI = spec.scheme + "://" + net.JoinHostPort(host, portText)
			if spec.scheme == "" {
				service.URI = net.JoinHostPort(host, portText)
			}
			if spec.auth != "none" && available {
				service.Username, service.Password = user, password
			}
		}
		services = append(services, service)
	}
	return services
}

func (a *app) installKind() string {
	b, err := readSmallFile(a.nodeControlPath("capability"), 16384)
	if err != nil {
		return "unknown"
	}
	// The install type never changes, so a stale capability still tells it.
	var capability struct {
		BootKind string `json:"boot_kind"`
	}
	if json.Unmarshal(b, &capability) != nil {
		return "unknown"
	}
	switch capability.BootKind {
	case "boot":
		return "apt"
	case "login":
		return "homebrew"
	case "unsupported":
		return "source"
	}
	return "unknown"
}

func (a *app) commandName() string {
	switch a.installKind() {
	case "apt":
		return "sudo node-manager"
	case "homebrew":
		return "node-manager"
	}
	return "./stack"
}

func (a *app) actuatorInfo(w http.ResponseWriter, _ *http.Request) {
	chains := map[string]actuatorChain{}
	for _, chain := range a.actuatorChains() {
		chain.Services = a.lanServices(chain.id)
		chains[chain.id] = chain
	}
	writeJSON(w, map[string]any{
		"app":    map[string]string{"name": "node-manager", "version": versionLabel(a.cfg.version), "install_kind": a.installKind()},
		"chains": chains,
	})
}
