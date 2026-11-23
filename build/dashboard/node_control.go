package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const nodeControlCapabilityAge = 90 * time.Second
const nodeHealthFreshFor = 2 * time.Minute
const nodeControlRequestFreshFor = 15 * time.Minute

type nodeHealthSnapshot struct {
	Available bool
	UpdatedAt time.Time
}

type nodeControlNode struct {
	ID         string  `json:"id"`
	Active     bool    `json:"active"`
	Available  bool    `json:"available"`
	Supported  bool    `json:"supported"`
	Configured bool    `json:"configured"`
	Stopped    bool    `json:"stopped"`
	Restarting bool    `json:"restarting"`
	Starting   bool    `json:"starting,omitempty"`
	Failure    string  `json:"failure,omitempty"`
	LastHeight *int64  `json:"last_height,omitempty"`
	LastSync   float64 `json:"last_sync_percent,omitempty"`
}

type nodeControlInput struct {
	Active       *bool  `json:"active"`
	Confirmation string `json:"confirmation"`
}

type nodeControlRequest struct {
	ID           string `json:"id"`
	Node         string `json:"node"`
	Active       bool   `json:"active"`
	Confirmation string `json:"confirmation"`
	RequestedAt  int64  `json:"requested_at"`
}

type nodeControlStatus struct {
	ID        string `json:"id,omitempty"`
	Node      string `json:"node,omitempty"`
	Active    bool   `json:"active"`
	State     string `json:"state"`
	Message   string `json:"message,omitempty"`
	ErrorCode string `json:"error_code,omitempty"`
	Detail    string `json:"detail,omitempty"`
}

func (a *app) nodeControlPath(name string) string {
	return filepath.Join(a.cfg.stateDir, "node-control."+name)
}

func (a *app) nodeConfigured(id string) (bool, bool) {
	switch id {
	case nodeBitcoin:
		return !a.cfg.bitcoinDisabled, true
	case nodeMonero:
		return a.cfg.moneroEnabled, true
	default:
		node, ok := a.cfg.nodes[id]
		return ok && node.Enabled, ok && node.Supported
	}
}

func (a *app) nodeRuntime() map[string]bool {
	active := make(map[string]bool, len(nodeOrder))
	active[nodeBitcoin] = !a.cfg.bitcoinDisabled
	if a.cfg.bitcoinActiveSet {
		active[nodeBitcoin] = a.cfg.bitcoinActive
	}
	active[nodeMonero] = a.cfg.moneroEnabled
	if a.cfg.moneroActiveSet {
		active[nodeMonero] = a.cfg.moneroActive
	}
	for id, node := range a.cfg.nodes {
		active[id] = node.Enabled
		if node.ActiveSet {
			active[id] = node.Active
		}
	}
	b, err := readSmallFile(filepath.Join(a.cfg.stateDir, "node-runtime.json"), 4096)
	if err != nil {
		return active
	}
	var saved map[string]bool
	decoder := json.NewDecoder(bytes.NewReader(b))
	if decoder.Decode(&saved) != nil || ensureJSONEnd(decoder) != nil {
		return active
	}
	for id := range saved {
		configured, supported := a.nodeConfigured(id)
		if normalizeNodeID(id) == "" || !configured || !supported {
			return active
		}
	}
	for id, value := range saved {
		active[id] = value
	}
	return active
}

func (a *app) nodeTransitioning(id string) bool {
	status, pending := a.pendingNodeControlStatus()
	return pending && status != nil && status.ID != "" && status.Node == id || a.credentialTransitioning(id)
}

func (a *app) monitorTransitioning(id string) bool {
	return a.nodeTransitioning(id) || id == nodeBitcoin && a.utxo.AlertsSuppressed() || a.chainUtxo[id].AlertsSuppressed()
}

func (a *app) nodeActive(id string, fallback bool) bool {
	if value, ok := a.nodeRuntime()[id]; ok {
		return value
	}
	return fallback
}

func (a *app) recordNodeHealth(id string, available bool) {
	a.nodeHealthMu.Lock()
	if a.nodeHealth == nil {
		a.nodeHealth = make(map[string]nodeHealthSnapshot)
	}
	a.nodeHealth[id] = nodeHealthSnapshot{Available: available, UpdatedAt: time.Now()}
	a.nodeHealthMu.Unlock()
}

func (a *app) nodeAvailable(id string, active bool) bool {
	if !active {
		return false
	}
	a.nodeHealthMu.RLock()
	status, ok := a.nodeHealth[id]
	a.nodeHealthMu.RUnlock()
	if !ok || !status.Available || time.Since(status.UpdatedAt) < 0 || time.Since(status.UpdatedAt) > nodeHealthFreshFor {
		return false
	}
	if info, err := os.Stat(filepath.Join(a.cfg.stateDir, "node-runtime.json")); err == nil && info.ModTime().After(status.UpdatedAt) {
		return false
	}
	return true
}

func (a *app) nodeStopped(id string, stats *hostStatsFile) bool {
	running, known := false, false
	if stats != nil {
		running, known = stats.Running[id]
	}
	if !known || running {
		return false
	}
	info, err := os.Stat(filepath.Join(a.cfg.stateDir, "node-runtime.json"))
	return err != nil || !info.ModTime().After(time.Unix(stats.Updated, 0))
}

func (a *app) apiNodes(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	runtime := a.nodeRuntime()
	stats := a.hostStats()
	restarting, starting := a.restartStates()
	nodes := make([]nodeControlNode, 0, len(nodeOrder))
	for _, id := range nodeOrder {
		configured, supported := a.nodeConfigured(id)
		active := configured && supported && runtime[id]
		stopped := active && a.nodeStopped(id, stats)
		failure := ""
		if stopped {
			failure = stats.Failed[id]
		}
		entry := nodeControlNode{ID: id, Active: active, Available: a.nodeAvailable(id, active), Supported: supported, Configured: configured, Stopped: stopped, Restarting: restarting[id] && failure == "", Starting: restarting[id] && starting[id] && failure == "", Failure: failure}
		if seen := a.monitor.LastSeen(id); seen.HasLastBlock && seen.LastBlock >= 0 {
			entry.LastHeight, entry.LastSync = &seen.LastBlock, seen.SyncPercent
		}
		nodes = append(nodes, entry)
	}
	status, pending := a.pendingNodeControlStatus()
	if !pending {
		status = a.readNodeControlStatus()
	}
	disk := diskUsage(a.cfg.dataDir)
	host := map[string]any{"disk_available": disk.Total > 0, "disk_total_bytes": disk.Total, "disk_free_bytes": disk.Free, "disks": a.hostDisks()}
	if stats != nil {
		host["stats"] = stats
	}
	writeJSON(w, map[string]any{"nodes": nodes, "control_available": a.nodeControlAvailable(), "control_pending": pending, "status": status, "host": host})
}

type hostStatsUsage struct {
	CPUPct    float64 `json:"cpu_pct"`
	MemBytes  uint64  `json:"mem_bytes"`
	DiskBytes uint64  `json:"disk_bytes,omitempty"`
	StartedAt int64   `json:"started_at,omitempty"`
}

type hostStatsFile struct {
	Updated       int64                     `json:"updated"`
	CPUs          int                       `json:"cpus"`
	MemTotalBytes uint64                    `json:"mem_total_bytes"`
	HostCPUPct    float64                   `json:"host_cpu_pct,omitempty"`
	HostMemBytes  uint64                    `json:"host_mem_bytes,omitempty"`
	Nodes         map[string]hostStatsUsage `json:"nodes"`
	Running       map[string]bool           `json:"running,omitempty"`
	Failed        map[string]string         `json:"failed,omitempty"`
	Other         hostStatsUsage            `json:"other"`
	Disks         []*hostDisk               `json:"disks,omitempty"`
	CPUTempC      *float64                  `json:"cpu_temp_c,omitempty"`
	BoardTempC    *float64                  `json:"board_temp_c,omitempty"`
	FansRPM       []int                     `json:"fans_rpm,omitempty"`
	FansMaxRPM    []int                     `json:"fans_max_rpm,omitempty"`
	UptimeS       int64                     `json:"uptime_s,omitempty"`
	NetRxBps      *float64                  `json:"net_rx_bps,omitempty"`
	NetTxBps      *float64                  `json:"net_tx_bps,omitempty"`
}

type hostDisk struct {
	Name       string   `json:"name,omitempty"`
	Label      string   `json:"label,omitempty"`
	TotalBytes uint64   `json:"disk_total_bytes"`
	FreeBytes  uint64   `json:"disk_free_bytes"`
	Nodes      []string `json:"nodes"`
}

func mountDevice(mounts, path string) string {
	device, longest := "", -1
	for _, line := range strings.Split(mounts, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		point := fields[1]
		if point != "/" {
			point = strings.TrimRight(point, "/")
		}
		if path != point && !strings.HasPrefix(path, point+"/") && point != "/" {
			continue
		}
		if len(point) > longest {
			device, longest = fields[0], len(point)
		}
	}
	return device
}

func (a *app) hostDisks() []*hostDisk {
	if stats := a.hostStats(); stats != nil {
		var disks []*hostDisk
		for _, disk := range stats.Disks {
			if disk != nil && disk.TotalBytes > 0 {
				disks = append(disks, disk)
			}
		}
		if len(disks) > 0 {
			return disks
		}
	}
	mounts := []struct {
		path string
		node string
	}{{a.cfg.dataDir, "bitcoin"}, {a.cfg.moneroDataDir, "monero"}}
	var disks []*hostDisk
	for _, mount := range mounts {
		if mount.path == "" {
			continue
		}
		info := diskUsage(mount.path)
		if info.Total == 0 {
			continue
		}
		name := diskDevice(mount.path)
		var found *hostDisk
		for _, disk := range disks {
			// shortcut: without device names, equal size and free space means the same filesystem
			if (name != "" && disk.Name == name) || (name == "" && disk.Name == "" && disk.TotalBytes == info.Total && disk.FreeBytes == info.Free) {
				found = disk
				break
			}
		}
		if found == nil {
			found = &hostDisk{Name: name, TotalBytes: info.Total, FreeBytes: info.Free}
			disks = append(disks, found)
		}
		found.Nodes = append(found.Nodes, mount.node)
	}
	if len(disks) > 0 {
		for _, id := range nodeOrder {
			if id == "bitcoin" || id == "monero" {
				continue
			}
			if configured, supported := a.nodeConfigured(id); configured && supported {
				disks[0].Nodes = append(disks[0].Nodes, id)
			}
		}
	}
	return disks
}

const hostStatsFreshFor = 3 * time.Minute

func (a *app) hostStats() *hostStatsFile {
	b, err := os.ReadFile(filepath.Join(a.cfg.stateDir, "host-stats.json"))
	if err != nil || len(b) > 1<<16 {
		return nil
	}
	var stats hostStatsFile
	if json.Unmarshal(b, &stats) != nil || stats.CPUs <= 0 || stats.MemTotalBytes == 0 {
		return nil
	}
	if age := time.Since(time.Unix(stats.Updated, 0)); age < 0 || age > hostStatsFreshFor {
		return nil
	}
	return &stats
}

func (a *app) hostNodeUsage(id string) hostStatsUsage {
	if stats := a.hostStats(); stats != nil {
		return stats.Nodes[id]
	}
	return hostStatsUsage{}
}

func hostUptime(usage hostStatsUsage) (int64, bool) {
	now := time.Now().Unix()
	return now - usage.StartedAt, usage.StartedAt > 0 && usage.StartedAt <= now
}

func (a *app) fillFromHostStats(id string, status *genericNodeStatus) {
	usage := a.hostNodeUsage(id)
	if (!status.DatabaseSizeAvailable || status.DatabaseSizeBytes <= 0) && usage.DiskBytes > 0 {
		status.DatabaseSizeBytes, status.DatabaseSizeAvailable = int64(usage.DiskBytes), true
	}
	if seconds, ok := hostUptime(usage); !status.UptimeAvailable && ok {
		status.UptimeSeconds, status.UptimeAvailable = seconds, true
	}
}

func (a *app) nodeControlAvailable() bool {
	info, err := os.Stat(a.nodeControlPath("capability"))
	if err != nil || !info.Mode().IsRegular() || info.Size() > 1024 || time.Since(info.ModTime()) < 0 || time.Since(info.ModTime()) > nodeControlCapabilityAge {
		return false
	}
	b, err := os.ReadFile(a.nodeControlPath("capability"))
	if err != nil {
		return false
	}
	if string(b) == "ready\n" {
		return true
	}
	_, ok := decodeHostControlCapability(b)
	return ok
}

func (a *app) pendingNodeControlStatus() (*nodeControlStatus, bool) {
	for _, name := range []string{"request", "processing"} {
		b, err := readSmallFile(a.nodeControlPath(name), 4096)
		if err == nil {
			var request nodeControlRequest
			decoded := decodeStrictBytes(b, &request)
			configured, supported := a.nodeConfigured(request.Node)
			now := time.Now().Unix()
			valid := decoded && validPortControlID(request.ID) && normalizeNodeID(request.Node) != "" && configured && supported && request.RequestedAt > 0 && request.RequestedAt <= now+30 && now-request.RequestedAt <= int64(nodeControlRequestFreshFor/time.Second)
			valid = valid && (request.Active && request.Confirmation == "" || !request.Active && request.Confirmation == "deactivate")
			if valid {
				return &nodeControlStatus{ID: request.ID, Node: request.Node, Active: request.Active, State: "pending", Message: "Applying " + nodeDisplayName(request.Node) + " state change…"}, true
			}
			return &nodeControlStatus{State: "pending", Message: "Node state change is being checked…"}, true
		}
		if !os.IsNotExist(err) {
			return &nodeControlStatus{State: "pending", Message: "Node state change is being checked…"}, true
		}
	}
	return nil, false
}

func (a *app) readNodeControlStatus() *nodeControlStatus {
	b, err := readSmallFile(a.nodeControlPath("status"), 4096)
	if err != nil {
		return nil
	}
	var status nodeControlStatus
	if !decodeStrictBytes(b, &status) || !validPortControlID(status.ID) || normalizeNodeID(status.Node) == "" {
		return nil
	}
	if status.State != "applied" && status.State != "rejected" && status.State != "failed" {
		return nil
	}
	switch status.State {
	case "applied":
		if status.Active {
			status.Message = nodeDisplayName(status.Node) + " activated."
		} else {
			status.Message = nodeDisplayName(status.Node) + " deactivated."
		}
	case "rejected":
		status.Message = nodeDisplayName(status.Node) + " state change was rejected."
	case "failed":
		status.Message = nodeDisplayName(status.Node) + " state could not be changed."
	}
	if reason, ok := nodeControlReasons[status.ErrorCode]; ok && status.State != "applied" {
		status.Message = nodeDisplayName(status.Node) + " " + reason
	} else {
		status.ErrorCode = ""
	}
	if detail := hostStatusDetail(hostControlStatus{State: status.State, Detail: status.Detail}); detail != "" {
		status.Message += " " + detail
	}
	status.Detail = ""
	return &status
}

var nodeControlReasons = map[string]string{
	"config_pending":      "was not changed: review the pending config change first.",
	"node_not_configured": "was not changed: it is not configured.",
	"unchanged":           "was not changed: it is already in that state.",
	"validation_failed":   "was not changed: its settings did not pass validation.",
	"replayed":            "was not changed: that request was already processed.",
	"image_unavailable":   "was not changed: its image could not be downloaded. Check the host's internet connection, then try again.",
	"apply_failed":        "could not be changed; the prior state was restored.",
	"rollback_failed":     "could not be changed and its prior state could not be fully restored. Check the node.",
}

func normalizeNodeID(id string) string {
	for _, allowed := range nodeOrder {
		if id == allowed {
			return id
		}
	}
	return ""
}

func decodeNodeControlInput(w http.ResponseWriter, r *http.Request) (nodeControlInput, error) {
	r.Body = http.MaxBytesReader(w, r.Body, 1024)
	var input nodeControlInput
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || ensureJSONEnd(decoder) != nil || input.Active == nil {
		return input, errors.New("invalid node control request")
	}
	return input, nil
}

func (a *app) apiNodePower(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if rejectCredentialCSRF(w, r) || a.rejectUnsafeSettingsHost(w, r) {
		return
	}
	id := normalizeNodeID(r.PathValue("node"))
	configured, supported := a.nodeConfigured(id)
	if id == "" || !configured || !supported {
		http.Error(w, "node is not configured", http.StatusBadRequest)
		return
	}
	input, err := decodeNodeControlInput(w, r)
	if err != nil || (!*input.Active && input.Confirmation != "deactivate") || (*input.Active && input.Confirmation != "") {
		http.Error(w, "invalid node control request", http.StatusBadRequest)
		return
	}
	if !a.nodeControlAvailable() {
		http.Error(w, "host node control is unavailable", http.StatusServiceUnavailable)
		return
	}
	a.controlMu.Lock()
	defer a.controlMu.Unlock()
	if a.controlRequestPending() {
		http.Error(w, "node control request already pending", http.StatusConflict)
		return
	}
	request := nodeControlRequest{ID: newRequestID(), Node: id, Active: *input.Active, Confirmation: input.Confirmation, RequestedAt: time.Now().Unix()}
	b, _ := json.Marshal(request)
	if err := writePrivateFileAtomic(a.nodeControlPath("request"), append(b, '\n')); err != nil {
		http.Error(w, "node control unavailable", http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusAccepted)
	writeJSON(w, nodeControlStatus{ID: request.ID, Node: id, Active: request.Active, State: "pending", Message: "Applying node state change…"})
}
