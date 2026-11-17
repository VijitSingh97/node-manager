package main

import (
	"bytes"
	"cmp"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode"
)

type hostControlCapability struct {
	Timestamp         int64             `json:"timestamp"`
	Platform          string            `json:"platform"`
	BootKind          string            `json:"boot_kind"`
	InstallSupported  bool              `json:"install_supported"`
	MigrateSupported  bool              `json:"migrate_supported,omitempty"`
	DeleteSupported   bool              `json:"delete_supported,omitempty"`
	UTXOSeedSupported bool              `json:"utxo_seed_supported,omitempty"`
	BootSupported     bool              `json:"boot_supported"`
	BootEnabled       bool              `json:"boot_enabled"`
	ClearnetOverTor   bool              `json:"clearnet_over_tor"`
	InboundTor        bool              `json:"inbound_connections"`
	UtxoIndex         bool              `json:"utxo_index"`
	UtxoIndexLTC      bool              `json:"utxo_index_litecoin,omitempty"`
	ConfigPending     bool              `json:"config_pending"`
	ConfigRevision    string            `json:"config_revision"`
	ConfigPath        string            `json:"config_path,omitempty"`
	DataPaths         map[string]string `json:"data_paths,omitempty"`
	UnsupportedNodes  []string          `json:"unsupported_nodes,omitempty"`
}

const zcashCPUMessage = "Zcash needs a processor with the BMI2 and ADX instructions (Intel Core 5th generation or newer, or AMD Ryzen), and this one lacks them."

func (c hostControlCapability) unsupportedReason(node string) string {
	if node == nodeZcash && slices.Contains(c.UnsupportedNodes, node) {
		return zcashCPUMessage
	}
	return ""
}

type hostInstallInterface struct {
	ID       string `json:"id"`
	Name     string `json:"name,omitempty"`
	Enabled  bool   `json:"enabled"`
	HostPort int    `json:"host_port"`
}

type hostControlNode struct {
	ID           string                 `json:"id"`
	Name         string                 `json:"name"`
	Installed    bool                   `json:"installed"`
	DataDir      string                 `json:"data_dir,omitempty"`
	Active       bool                   `json:"active"`
	LastEnabled  bool                   `json:"last_enabled"`
	DiskEstimate string                 `json:"disk_estimate"`
	DiskNote     string                 `json:"disk_note"`
	Interfaces   []hostInstallInterface `json:"interfaces"`
	Unsupported  string                 `json:"unsupported,omitempty"`
}

type hostControlInput struct {
	Action     string                 `json:"action"`
	Node       string                 `json:"node,omitempty"`
	Enabled    *bool                  `json:"enabled,omitempty"`
	Revision   string                 `json:"config_revision,omitempty"`
	DataDir    string                 `json:"data_dir,omitempty"`
	Interfaces []hostInstallInterface `json:"interfaces,omitempty"`
	Confirm    string                 `json:"confirmation,omitempty"`
}

type hostControlRequest struct {
	ID          string                 `json:"id"`
	Action      string                 `json:"action"`
	Node        string                 `json:"node,omitempty"`
	Enabled     *bool                  `json:"enabled,omitempty"`
	Revision    string                 `json:"config_revision,omitempty"`
	DataDir     string                 `json:"data_dir,omitempty"`
	Interfaces  []hostInstallInterface `json:"interfaces,omitempty"`
	Confirm     string                 `json:"confirmation,omitempty"`
	RequestedAt int64                  `json:"requested_at"`
}

type hostControlStatus struct {
	ID        string `json:"id,omitempty"`
	Action    string `json:"action,omitempty"`
	Node      string `json:"node,omitempty"`
	Enabled   *bool  `json:"enabled,omitempty"`
	State     string `json:"state"`
	Message   string `json:"message,omitempty"`
	ErrorCode string `json:"error_code,omitempty"`
	Port      string `json:"port,omitempty"`
	Detail    string `json:"detail,omitempty"`
}

func hostStatusDetail(status hostControlStatus) string {
	if status.State != "failed" {
		return ""
	}
	detail := strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, status.Detail))
	if runes := []rune(detail); len(runes) > 240 {
		detail = string(runes[:240])
	}
	return detail
}

type hostControlView struct {
	Available        bool               `json:"available"`
	InstallSupported bool               `json:"install_supported"`
	MigrateSupported bool               `json:"migrate_supported"`
	DeleteSupported  bool               `json:"delete_supported"`
	BootSupported    bool               `json:"boot_supported"`
	BootEnabled      bool               `json:"boot_enabled"`
	BootKind         string             `json:"boot_kind"`
	ClearnetOverTor  bool               `json:"clearnet_over_tor"`
	InboundTor       bool               `json:"inbound_connections"`
	UtxoIndex        bool               `json:"utxo_index"`
	UtxoSeedNow      bool               `json:"utxo_seed_now"`
	UtxoIndexLTC     bool               `json:"utxo_index_litecoin"`
	UtxoSeedNowLTC   bool               `json:"utxo_seed_now_litecoin"`
	ConfigPending    bool               `json:"config_pending"`
	ConfigRevision   string             `json:"config_revision,omitempty"`
	ConfigPath       string             `json:"config_path,omitempty"`
	Nodes            []hostControlNode  `json:"nodes"`
	Status           *hostControlStatus `json:"status,omitempty"`
}

var hostInstallCatalog = map[string]struct {
	DiskEstimate string
	DiskNote     string
	Interfaces   []hostInstallInterface
}{
	"bitcoin":     {"28–56 GiB", "Planning estimate; 24-hour compact-mode qualification is pending.", []hostInstallInterface{{ID: "bitcoin_rpc", Name: "RPC", HostPort: 8332}}},
	"monero":      {"120–150 GiB", "Upstream-based pruned estimate; this stack has not completed a qualified full sync.", []hostInstallInterface{{ID: "monero_rpc", Name: "RPC", HostPort: 18081}, {ID: "monero_zmq", Name: "ZMQ", HostPort: 18083}}},
	"tari":        {"Not measured", "Use expandable storage; a pruned full sync has not been qualified.", []hostInstallInterface{{ID: "tari_grpc", Name: "gRPC", HostPort: 18142}}},
	"litecoin":    {"28–56 GiB", "Provisional planning estimate; a full sync has not been qualified.", []hostInstallInterface{{ID: "litecoin_rpc", Name: "RPC", HostPort: 9332}}},
	"dogecoin":    {"28–56 GiB", "Provisional planning estimate; a full sync has not been qualified.", []hostInstallInterface{{ID: "dogecoin_rpc", Name: "RPC", HostPort: 22555}}},
	"bitcoincash": {"28–56 GiB", "Provisional planning estimate; a full sync has not been qualified.", []hostInstallInterface{{ID: "bitcoincash_rpc", Name: "RPC", HostPort: 8332}}},
	"zcash":       {"28–56 GiB", "Expandable-volume estimate; a full sync has not been qualified.", []hostInstallInterface{{ID: "zcash_rpc", Name: "Read-only RPC", HostPort: 8232}}},
}

func safeHostPath(path string) bool {
	return len(path) > 1 && len(path) <= 1024 && filepath.IsAbs(path) && filepath.Clean(path) == path && !strings.ContainsAny(path, "\x00\r\n")
}

func reportedHostPath(path string) bool {
	return len(path) > 1 && len(path) <= 1024 && filepath.IsAbs(path) && !strings.ContainsAny(path, "\x00\r\n")
}

func validInstallInterfaces(node string, interfaces []hostInstallInterface) bool {
	catalog, ok := hostInstallCatalog[node]
	if !ok || len(interfaces) > len(catalog.Interfaces) {
		return false
	}
	seen := map[string]bool{}
	for _, requested := range interfaces {
		allowed := false
		for _, candidate := range catalog.Interfaces {
			if requested.ID == candidate.ID {
				allowed = true
				break
			}
		}
		if !allowed || seen[requested.ID] || requested.Name != "" || requested.HostPort < 1 || requested.HostPort > 65535 {
			return false
		}
		seen[requested.ID] = true
	}
	return true
}

func validInstallOptions(node, dataDir string, interfaces []hostInstallInterface) bool {
	return safeHostPath(dataDir) && validInstallInterfaces(node, interfaces)
}

func decodeHostControlCapability(b []byte) (hostControlCapability, bool) {
	var capability hostControlCapability
	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&capability) != nil || ensureJSONEnd(decoder) != nil {
		return capability, false
	}
	validPlatform := capability.Platform == "linux" || capability.Platform == "mac" || capability.Platform == "source"
	validBootKind := capability.BootKind == "boot" || capability.BootKind == "login" || capability.BootKind == "unsupported"
	validRevision := !capability.ConfigPending && capability.ConfigRevision == "" || capability.ConfigPending && len(capability.ConfigRevision) == 64 && len(bytes.Trim([]byte(capability.ConfigRevision), "0123456789abcdef")) == 0
	validPaths := capability.ConfigPath == "" || reportedHostPath(capability.ConfigPath)
	for node, path := range capability.DataPaths {
		if normalizeNodeID(node) == "" || !reportedHostPath(path) {
			validPaths = false
		}
	}
	if capability.Timestamp <= 0 || !validPlatform || !validBootKind || !validRevision || !validPaths {
		return capability, false
	}
	now := time.Now().Unix()
	if capability.Timestamp > now+30 || now-capability.Timestamp > int64(nodeControlCapabilityAge/time.Second) {
		return capability, false
	}
	return capability, true
}

func (a *app) hostControlCapability() (hostControlCapability, bool) {
	b, err := readSmallFile(a.nodeControlPath("capability"), 16384)
	if err != nil {
		return hostControlCapability{}, false
	}
	return decodeHostControlCapability(b)
}

type migrateProgress struct {
	ID        string `json:"id"`
	Node      string `json:"node"`
	Percent   int64  `json:"percent"`
	CopiedKB  int64  `json:"copied_kb"`
	TotalKB   int64  `json:"total_kb"`
	UpdatedAt int64  `json:"updated_at"`
}

func (a *app) migrateProgressFor(id string) (migrateProgress, bool) {
	var progress migrateProgress
	b, err := readSmallFile(a.nodeControlPath("progress"), 4096)
	if err != nil {
		return progress, false
	}
	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.DisallowUnknownFields()
	now := time.Now().Unix()
	if decoder.Decode(&progress) != nil || ensureJSONEnd(decoder) != nil || progress.ID != id ||
		progress.Percent < 0 || progress.Percent > 100 || progress.CopiedKB < 0 || progress.TotalKB <= 0 ||
		progress.UpdatedAt > now+30 || now-progress.UpdatedAt > 180 {
		return progress, false
	}
	return progress, true
}

func hostControlPendingMessage(action string) string {
	if action == "migrate" {
		return "Migrating node data… this can take a while."
	}
	if action == "delete" {
		return "Deleting node data… this can take a while."
	}
	return "Applying host change…"
}

func validHostAction(action string) bool {
	return action == "install" || action == "migrate" || action == "delete" || action == "launch_at_boot" || action == "apply_config" || action == "utxo_index" || action == "utxo_seed" || networkHostAction(action)
}

func networkHostAction(action string) bool {
	return action == "clearnet_over_tor" || action == "inbound_connections"
}

func toggleHostAction(action string) bool {
	return action == "launch_at_boot" || action == "utxo_index" || networkHostAction(action)
}

func validHostControlShape(action, node string, enabled *bool, dataDir string, interfaces []hostInstallInterface, confirmation string) bool {
	if action != "delete" && confirmation != "" {
		return false
	}
	return action == "delete" && normalizeNodeID(node) != "" && confirmation == node && enabled == nil && dataDir == "" && interfaces == nil ||
		action == "install" && normalizeNodeID(node) != "" && enabled == nil && (dataDir == "" && interfaces == nil || validInstallOptions(node, dataDir, interfaces)) ||
		action == "migrate" && normalizeNodeID(node) != "" && enabled == nil && safeHostPath(dataDir) && interfaces == nil ||
		toggleHostAction(action) && node == "" && enabled != nil && dataDir == "" && interfaces == nil ||
		action == "utxo_index" && node == nodeLitecoin && enabled != nil && dataDir == "" && interfaces == nil ||
		action == "utxo_seed" && (node == nodeBitcoin || node == nodeLitecoin) && enabled == nil && dataDir == "" && interfaces == nil ||
		action == "apply_config" && node == "" && enabled == nil && dataDir == "" && interfaces == nil
}

func canonicalHostStatusMessage(status hostControlStatus) string {
	if status.State == "applied" {
		switch status.Action {
		case "install":
			if normalizeNodeID(status.Node) == "" {
				return "Node installed."
			}
			return nodeDisplayName(status.Node) + " installed."
		case "launch_at_boot":
			if status.Enabled == nil {
				return "Automatic launch setting updated."
			}
			if *status.Enabled {
				return "Automatic launch enabled."
			}
			return "Automatic launch disabled."
		case "clearnet_over_tor":
			if status.Enabled != nil && !*status.Enabled {
				return "Nodes now use onion peers only."
			}
			return "Nodes now also reach clearnet peers through Tor."
		case "inbound_connections":
			if status.Enabled != nil && !*status.Enabled {
				return "Incoming Tor connections disabled."
			}
			return "Incoming Tor connections enabled."
		case "utxo_index":
			if status.Enabled != nil && !*status.Enabled {
				return "Balance index turned off."
			}
			return "Balance index turned on. It seeds itself once the node has synced."
		case "utxo_seed":
			return "Balance index seed started."
		case "migrate":
			if normalizeNodeID(status.Node) == "" {
				return "Node data migrated."
			}
			return nodeDisplayName(status.Node) + " data migrated."
		case "delete":
			if normalizeNodeID(status.Node) == "" {
				return "Node deleted."
			}
			return nodeDisplayName(status.Node) + " deleted. It can be installed again."
		case "apply_config":
			return "New configuration applied."
		}
	}
	messages := map[string]string{
		"config_pending":     "Apply the pending configuration first.",
		"config_changed":     "The config changed again. Review and apply the latest version.",
		"already_installed":  "That node is already installed.",
		"validation_failed":  "The requested host change did not pass validation.",
		"apply_failed":       "The host could not apply that change.",
		"approval_failed":    "The config could not be approved.",
		"rollback_failed":    "The host could not restore its previous configuration.",
		"unsupported":        "That host action is not supported by this installation.",
		"ownership_conflict": "The node data directory is managed by another installation.",
		"unchanged":          "The host already has that setting.",
		"seed_running":       "A balance index seed is running. Cancel it with utxo-seed cancel, then try again.",
		"not_installed":      "That node is not installed.",
		"node_active":        "Deactivate the node before deleting it.",
		"last_node":          "At least one node must stay installed.",
		"delete_incomplete":  "The node was removed from the configuration, but some of its data could not be deleted. Remove what is left by hand before installing it again.",
		"data_path_invalid":  "The data folder must be a new or empty writable folder outside the app and config roots, without symbolic links.",
		"insufficient_space": "The destination disk does not have enough free space.",
		"unsupported_cpu":    zcashCPUMessage,
		"move_failed":        "The data move failed; the node was restarted in place.",
		"verify_failed":      "The copied data failed verification; the node was restarted in place.",
	}
	if status.ErrorCode == "port_in_use" && portControlLabel(status.Port) != "" {
		return "Selected port for " + portControlLabel(status.Port) + " is already in use."
	}
	if status.Action == "delete" && status.ErrorCode == "data_path_invalid" {
		return "The data folder is not a safe place to delete, so nothing was changed."
	}
	if message := messages[status.ErrorCode]; message != "" {
		return message
	}
	if status.State == "rejected" {
		return "Host change was rejected."
	}
	return "Host change could not be applied."
}

func (a *app) hostControlProcessingMessage(request hostControlRequest) string {
	message := hostControlPendingMessage(request.Action)
	if request.Action == "migrate" {
		if progress, ok := a.migrateProgressFor(request.ID); ok {
			message = fmt.Sprintf("Migrating node data… %d%% — %.1f of %.1f GiB", progress.Percent, float64(progress.CopiedKB)/(1024*1024), float64(progress.TotalKB)/(1024*1024))
		}
	}
	return message
}

func (a *app) installedNodeCount() int {
	count := 0
	for _, id := range nodeOrder {
		if configured, _ := a.nodeConfigured(id); configured {
			count++
		}
	}
	return count
}

func (a *app) hostControlStatus() (*hostControlStatus, bool) {
	for _, name := range []string{"request", "processing"} {
		b, err := readSmallFile(a.nodeControlPath(name), 4096)
		if err == nil {
			var request hostControlRequest
			decoder := json.NewDecoder(bytes.NewReader(b))
			decoder.DisallowUnknownFields()
			if decoder.Decode(&request) == nil && ensureJSONEnd(decoder) == nil && validPortControlID(request.ID) && validHostControlShape(request.Action, request.Node, request.Enabled, request.DataDir, request.Interfaces, request.Confirm) {
				return &hostControlStatus{ID: request.ID, Action: request.Action, Node: normalizeNodeID(request.Node), Enabled: request.Enabled, State: "pending", Message: a.hostControlProcessingMessage(request)}, true
			}
			return nil, true
		}
		if !os.IsNotExist(err) {
			return nil, true
		}
	}
	b, err := readSmallFile(a.nodeControlPath("status"), 4096)
	if err != nil {
		return nil, false
	}
	var status hostControlStatus
	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&status) != nil || ensureJSONEnd(decoder) != nil || !validPortControlID(status.ID) || !validHostAction(status.Action) || status.Node != "" && normalizeNodeID(status.Node) == "" || status.Port != "" && portControlLabel(status.Port) == "" || status.State != "applied" && status.State != "rejected" && status.State != "failed" {
		return nil, false
	}
	status.Message = canonicalHostStatusMessage(status)
	status.Detail = hostStatusDetail(status)
	status.ErrorCode = ""
	return &status, false
}

func (a *app) hostControlView() hostControlView {
	status, _ := a.hostControlStatus()
	capability, available := a.hostControlCapability()
	nodes := make([]hostControlNode, 0, len(nodeOrder))
	runtime, installed := a.nodeRuntime(), a.installedNodeCount()
	for _, id := range nodeOrder {
		configured, _ := a.nodeConfigured(id)
		catalog := hostInstallCatalog[id]
		interfaces := append([]hostInstallInterface(nil), catalog.Interfaces...)
		nodes = append(nodes, hostControlNode{ID: id, Name: nodeDisplayName(id), Installed: configured, DataDir: capability.DataPaths[id], Active: configured && runtime[id], LastEnabled: configured && installed <= 1, DiskEstimate: catalog.DiskEstimate, DiskNote: catalog.DiskNote, Interfaces: interfaces, Unsupported: capability.unsupportedReason(id)})
	}
	return hostControlView{Available: available, InstallSupported: available && capability.InstallSupported, MigrateSupported: available && capability.MigrateSupported, DeleteSupported: available && capability.DeleteSupported, BootSupported: available && capability.BootSupported, BootEnabled: capability.BootEnabled, BootKind: capability.BootKind, ClearnetOverTor: capability.ClearnetOverTor, InboundTor: capability.InboundTor, UtxoIndex: capability.UtxoIndex, UtxoSeedNow: a.seedNow(nodeBitcoin), UtxoIndexLTC: capability.UtxoIndexLTC, UtxoSeedNowLTC: a.seedNow(nodeLitecoin), ConfigPending: capability.ConfigPending, ConfigRevision: capability.ConfigRevision, ConfigPath: capability.ConfigPath, Nodes: nodes, Status: status}
}

func (a *app) apiHostControlGet(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if a.rejectUnsafeSettingsHost(w, r) {
		return
	}
	writeJSON(w, a.hostControlView())
}

func decodeHostControlInput(w http.ResponseWriter, r *http.Request) (hostControlInput, error) {
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	var input hostControlInput
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || ensureJSONEnd(decoder) != nil || !validHostAction(input.Action) {
		return input, errors.New("invalid host control request")
	}
	validRevision := len(input.Revision) == 64 && len(bytes.Trim([]byte(input.Revision), "0123456789abcdef")) == 0
	if input.Action != "delete" && input.Confirm != "" {
		return input, errors.New("invalid host control request")
	}
	legacyInstall := input.Action == "install" && input.DataDir == "" && input.Interfaces == nil
	configuredInstall := input.Action == "install" && validInstallOptions(normalizeNodeID(input.Node), input.DataDir, input.Interfaces)
	deleteNode := input.Action == "delete" && normalizeNodeID(input.Node) != "" && input.Confirm == input.Node && input.Enabled == nil && input.Revision == "" && input.DataDir == "" && input.Interfaces == nil
	valid := deleteNode || (legacyInstall || configuredInstall) && normalizeNodeID(input.Node) != "" && input.Enabled == nil && input.Revision == "" ||
		input.Action == "migrate" && normalizeNodeID(input.Node) != "" && input.Enabled == nil && input.Revision == "" && safeHostPath(input.DataDir) && input.Interfaces == nil || toggleHostAction(input.Action) && input.Node == "" && input.Enabled != nil && input.Revision == "" && input.DataDir == "" && input.Interfaces == nil ||
		input.Action == "utxo_index" && utxoChains[input.Node] != nil && input.Enabled != nil && input.Revision == "" && input.DataDir == "" && input.Interfaces == nil || input.Action == "apply_config" && input.Node == "" && input.Enabled == nil && validRevision && input.DataDir == "" && input.Interfaces == nil ||
		input.Action == "utxo_seed" && (input.Node == nodeBitcoin || utxoChains[input.Node] != nil) && input.Enabled == nil && input.Revision == "" && input.DataDir == "" && input.Interfaces == nil
	if !valid {
		return input, errors.New("invalid host control request")
	}
	input.Node = normalizeNodeID(input.Node)
	return input, nil
}

// seedNow reports whether a node's index needs a seed and the host helper can prepare one.
func (a *app) seedNow(node string) bool {
	m := a.utxo
	if node != nodeBitcoin {
		m = a.chainUtxo[node]
	}
	if m == nil {
		return false
	}
	st := m.Status()
	if m.chain.Replay {
		// A replay needs no seed pipe, so the dashboard starts it without the host helper, also to resume a far-behind index.
		return m.host != nil && (st.State == utxoStateNotSeeded || st.State == utxoStateInvalid || st.State == utxoStateCatching && st.Behind != nil && *st.Behind > utxoReplayGap)
	}
	capability, ok := a.hostControlCapability()
	return (st.State == utxoStateNotSeeded || st.State == utxoStateInvalid) && ok && capability.UTXOSeedSupported && !capability.ConfigPending
}

func (a *app) requestReplaySeed(w http.ResponseWriter, m *utxoManager, node string) {
	if !a.seedNow(node) {
		http.Error(w, "The balance index cannot be seeded now.", http.StatusConflict)
		return
	}
	id := newSeedRequestID()
	raw, _ := json.Marshal(seedRequest{ID: id, RequestedAt: m.now().Unix()})
	if writePrivateFileAtomic(filepath.Join(m.host.stateDir, m.host.prefix+seedRequestName), raw) != nil {
		http.Error(w, "The seed request could not be written.", http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusAccepted)
	writeJSON(w, hostControlStatus{ID: id, Action: "utxo_seed", Node: node, State: "applied", Message: "Balance index replay requested."})
}

const hostChangePending = "another host change is pending"

// queueSeedHostAction asks node-control to create a node's seed pipe and a seed request named by the returned id.
func (a *app) queueSeedHostAction(node string) (id, refusal string) {
	capability, ok := a.hostControlCapability()
	switch {
	case !ok || !capability.UTXOSeedSupported:
		return "", "automatic seeding needs the host helper; start the seed with utxo-seed"
	case capability.ConfigPending:
		return "", "a configuration change is waiting to be applied"
	}
	a.controlMu.Lock()
	defer a.controlMu.Unlock()
	if a.controlRequestPending() {
		return "", hostChangePending
	}
	id = newSeedRequestID()
	b, _ := json.Marshal(hostControlRequest{ID: id, Action: "utxo_seed", Node: node, RequestedAt: time.Now().Unix()})
	if writePrivateFileAtomic(a.nodeControlPath("request"), append(b, '\n')) != nil {
		return "", "the host request could not be written"
	}
	return id, ""
}

func (a *app) apiHostControlPost(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if rejectCredentialCSRF(w, r) || a.rejectUnsafeSettingsHost(w, r) {
		return
	}
	input, err := decodeHostControlInput(w, r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if m := a.chainUtxo[input.Node]; input.Action == "utxo_seed" && m != nil && m.chain.Replay {
		a.requestReplaySeed(w, m, input.Node)
		return
	}
	capability, available := a.hostControlCapability()
	if !available || input.Action == "install" && !capability.InstallSupported || input.Action == "migrate" && !capability.MigrateSupported || input.Action == "delete" && !capability.DeleteSupported || input.Action == "launch_at_boot" && !capability.BootSupported || input.Action == "apply_config" && (!capability.ConfigPending || input.Revision != capability.ConfigRevision) {
		http.Error(w, "host control is unavailable", http.StatusServiceUnavailable)
		return
	}
	if capability.ConfigPending && input.Action != "apply_config" {
		http.Error(w, "apply the pending configuration first", http.StatusConflict)
		return
	}
	if input.Action == "install" {
		if reason := capability.unsupportedReason(input.Node); reason != "" {
			http.Error(w, reason, http.StatusConflict)
			return
		}
		configured, _ := a.nodeConfigured(input.Node)
		if configured {
			http.Error(w, "node is already installed", http.StatusConflict)
			return
		}
	}
	if input.Action == "migrate" {
		configured, _ := a.nodeConfigured(input.Node)
		if !configured {
			http.Error(w, "node is not installed", http.StatusConflict)
			return
		}
	}
	if input.Action == "delete" {
		configured, _ := a.nodeConfigured(input.Node)
		switch {
		case !configured:
			http.Error(w, "node is not installed", http.StatusConflict)
			return
		case a.nodeRuntime()[input.Node]:
			http.Error(w, "deactivate the node before deleting it", http.StatusConflict)
			return
		case a.installedNodeCount() <= 1:
			http.Error(w, "at least one node must stay installed", http.StatusConflict)
			return
		}
	}
	if input.Action == "utxo_seed" && !a.seedNow(input.Node) {
		http.Error(w, "The balance index cannot be seeded now.", http.StatusConflict)
		return
	}
	if input.Action == "utxo_index" && *input.Enabled {
		node := cmp.Or(input.Node, nodeBitcoin)
		if configured, _ := a.nodeConfigured(node); !configured {
			http.Error(w, nodeDisplayName(node)+" is not installed", http.StatusConflict)
			return
		}
	}
	a.controlMu.Lock()
	defer a.controlMu.Unlock()
	if a.controlRequestPending() {
		http.Error(w, "another host change is already pending", http.StatusConflict)
		return
	}
	id := make([]byte, 16)
	if _, err := rand.Read(id); err != nil {
		http.Error(w, "host control unavailable", http.StatusServiceUnavailable)
		return
	}
	if input.Action == "install" && input.DataDir != "" && input.Interfaces == nil {
		input.Interfaces = []hostInstallInterface{}
	}
	request := hostControlRequest{ID: hex.EncodeToString(id), Action: input.Action, Node: input.Node, Enabled: input.Enabled, Revision: input.Revision, DataDir: input.DataDir, Interfaces: input.Interfaces, Confirm: input.Confirm, RequestedAt: time.Now().Unix()}
	var b []byte
	if request.Action == "delete" {
		b, _ = json.Marshal(struct {
			ID          string `json:"id"`
			Action      string `json:"action"`
			Node        string `json:"node"`
			Confirm     string `json:"confirmation"`
			RequestedAt int64  `json:"requested_at"`
		}{request.ID, request.Action, request.Node, request.Confirm, request.RequestedAt})
	} else if request.Action == "migrate" {
		b, _ = json.Marshal(struct {
			ID          string `json:"id"`
			Action      string `json:"action"`
			Node        string `json:"node"`
			DataDir     string `json:"data_dir"`
			RequestedAt int64  `json:"requested_at"`
		}{request.ID, request.Action, request.Node, request.DataDir, request.RequestedAt})
	} else if request.Action == "install" && request.DataDir != "" {
		b, _ = json.Marshal(struct {
			ID          string                 `json:"id"`
			Action      string                 `json:"action"`
			Node        string                 `json:"node"`
			DataDir     string                 `json:"data_dir"`
			Interfaces  []hostInstallInterface `json:"interfaces"`
			RequestedAt int64                  `json:"requested_at"`
		}{request.ID, request.Action, request.Node, request.DataDir, request.Interfaces, request.RequestedAt})
	} else {
		b, _ = json.Marshal(request)
	}
	if writePrivateFileAtomic(a.nodeControlPath("request"), append(b, '\n')) != nil {
		http.Error(w, "host control unavailable", http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusAccepted)
	writeJSON(w, hostControlStatus{ID: request.ID, Action: request.Action, Node: request.Node, Enabled: request.Enabled, State: "pending", Message: hostControlPendingMessage(request.Action)})
}
