package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	portControlCapabilityAge = 90 * time.Second
	maxPortControlBody       = 4096
)

var errPortControlBusy = errors.New("port control request already pending")

type portControlPort struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Enabled  bool   `json:"enabled"`
	BindIP   string `json:"bind_ip"`
	HostPort int    `json:"host_port"`
}

type portControlStatus struct {
	ID               string   `json:"id,omitempty"`
	State            string   `json:"state"`
	Message          string   `json:"message,omitempty"`
	AffectedServices []string `json:"affected_services,omitempty"`
	ErrorCode        string   `json:"error_code,omitempty"`
	Port             string   `json:"port,omitempty"`
}

type portControlView struct {
	Available bool               `json:"available"`
	Ports     []portControlPort  `json:"ports"`
	Status    *portControlStatus `json:"status,omitempty"`
}

type portControlChangeInput struct {
	Port            string `json:"port"`
	Enabled         *bool  `json:"enabled"`
	HostPort        *int   `json:"host_port,omitempty"`
	hostPortPresent bool
}

type portControlInput struct {
	Changes []portControlChangeInput `json:"changes"`
}

type portControlChange struct {
	Port     string `json:"port"`
	Enabled  bool   `json:"enabled"`
	BindIP   string `json:"bind_ip,omitempty"`
	HostPort int    `json:"host_port,omitempty"`
}

type portControlRequest struct {
	ID          string              `json:"id"`
	Changes     []portControlChange `json:"changes"`
	RequestedAt int64               `json:"requested_at"`
}

func (input *portControlChangeInput) UnmarshalJSON(data []byte) error {
	type wireInput struct {
		Port     string `json:"port"`
		Enabled  *bool  `json:"enabled"`
		HostPort *int   `json:"host_port"`
	}
	var wire wireInput
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	input.Port, input.Enabled, input.HostPort = wire.Port, wire.Enabled, wire.HostPort
	_, input.hostPortPresent = fields["host_port"]
	return nil
}

func (a *app) portControlPath(name string) string {
	return filepath.Join(a.cfg.stateDir, "port-control."+name)
}

func configuredPort(id, label, endpoint, bindEnv, portEnv, defaultPort string) portControlPort {
	bindIP := strings.TrimSpace(os.Getenv(bindEnv))
	hostPort, _ := strconv.Atoi(strings.TrimSpace(os.Getenv(portEnv)))
	if hostPort == 0 {
		hostPort, _ = strconv.Atoi(defaultPort)
	}
	if endpoint != "" {
		if host, port, err := net.SplitHostPort(endpoint); err == nil {
			bindIP = host
			if parsed, err := strconv.Atoi(port); err == nil {
				hostPort = parsed
			}
		}
	}
	if !privateLANAddress(bindIP) {
		bindIP = strings.TrimSpace(os.Getenv("DASHBOARD_BIND_IP"))
		if !privateLANAddress(bindIP) {
			bindIP = ""
		}
	}
	return portControlPort{ID: id, Label: label, Enabled: endpoint != "", BindIP: bindIP, HostPort: hostPort}
}

func privateLANAddress(value string) bool {
	address, err := netip.ParseAddr(value)
	return err == nil && address.Is4() && address.IsPrivate()
}

var portControlLabels = func() map[string]string {
	labels := map[string]string{"monero_zmq": "Monero ZMQ"}
	for _, id := range nodeOrder {
		labels[id+"_"+strings.ToLower(rpcPortKind(id))] = nodeDisplayName(id) + " " + rpcPortKind(id)
	}
	return labels
}()

func rpcPortKind(id string) string {
	if chainPages[id].grpc {
		return "gRPC"
	}
	return "RPC"
}

func portControlLabel(id string) string {
	return portControlLabels[id]
}

func (a *app) portControlPorts() []portControlPort {
	ports := make([]portControlPort, 0, 8)
	if !a.cfg.bitcoinDisabled {
		ports = append(ports, configuredPort("bitcoin_rpc", portControlLabel("bitcoin_rpc"), a.cfg.bitcoinRPCEndpoint, "BITCOIN_RPC_BIND_IP", "BITCOIN_RPC_PORT", "8332"))
	}
	if a.cfg.moneroEnabled {
		ports = append(ports,
			configuredPort("monero_rpc", portControlLabel("monero_rpc"), a.cfg.moneroRPCEndpoint, "MONERO_RPC_BIND_IP", "MONERO_RPC_PORT", "18081"),
			configuredPort("monero_zmq", portControlLabel("monero_zmq"), a.cfg.moneroZMQEndpoint, "MONERO_ZMQ_BIND_IP", "MONERO_ZMQ_PORT", "18083"),
		)
	}
	for _, id := range []string{nodeTari, nodeLitecoin, nodeDogecoin, nodeBitcoinCash, nodeZcash} {
		node, ok := a.cfg.nodes[id]
		if !ok || !node.Enabled {
			continue
		}
		prefix, kind := strings.ToUpper(id), strings.ToUpper(rpcPortKind(id))
		portID := id + "_" + strings.ToLower(kind)
		ports = append(ports, configuredPort(portID, portControlLabel(portID), node.RPCEndpoint, prefix+"_"+kind+"_BIND_IP", prefix+"_"+kind+"_PORT", node.DefaultPort))
	}
	return ports
}

func affectedPortServices() []string {
	return []string{"tor", "dashboard"}
}

func (a *app) portControlAvailable() bool {
	path := a.portControlPath("capability")
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() != int64(len("ready\n")) || time.Since(info.ModTime()) < 0 || time.Since(info.ModTime()) > portControlCapabilityAge {
		return false
	}
	b, err := os.ReadFile(path)
	return err == nil && string(b) == "ready\n"
}

func (a *app) pendingPortControl() (*portControlStatus, bool) {
	for _, name := range []string{"request", "processing"} {
		b, err := readSmallFile(a.portControlPath(name), 4096)
		if err == nil {
			var request portControlRequest
			_ = json.Unmarshal(b, &request)
			return &portControlStatus{ID: request.ID, State: "pending", Message: "Checking LAN port changes…", AffectedServices: affectedPortServices()}, true
		}
		if !os.IsNotExist(err) {
			return &portControlStatus{State: "pending", Message: "Port changes are being checked…", AffectedServices: affectedPortServices()}, true
		}
	}
	return nil, false
}

func (a *app) readPortControlStatus() *portControlStatus {
	b, err := readSmallFile(a.portControlPath("status"), 4096)
	if err != nil {
		return nil
	}
	var status portControlStatus
	if !decodeStrictBytes(b, &status) || !validPortControlID(status.ID) {
		return nil
	}
	if status.State != "applied" && status.State != "rejected" && status.State != "failed" {
		return nil
	}
	validConflict := status.ErrorCode == "port_in_use" && status.State == "rejected" && portControlLabel(status.Port) != ""
	checkFailed := status.ErrorCode == "port_check_failed" && status.State == "failed"
	if checkFailed {
		status.Port = ""
	}
	if !validConflict && !checkFailed {
		status.ErrorCode, status.Port = "", ""
	}
	switch status.State {
	case "applied":
		status.Message = "LAN port changes applied."
	case "rejected":
		status.Message = "LAN port changes were rejected."
		if status.ErrorCode == "port_in_use" && portControlLabel(status.Port) != "" {
			status.Message = fmt.Sprintf("Selected port for %s is already in use.", portControlLabel(status.Port))
		}
	case "failed":
		status.Message = "LAN port changes could not be applied."
		if status.ErrorCode == "port_check_failed" {
			status.Message = "Port availability could not be checked; no services were restarted."
		}
	}
	status.AffectedServices = affectedPortServices()
	return &status
}

func readSmallFile(path string, limit int64) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return nil, errors.New("invalid state file")
	}
	return os.ReadFile(path)
}

func (a *app) portControlView() portControlView {
	view := portControlView{Available: a.portControlAvailable(), Ports: a.portControlPorts()}
	if pending, ok := a.pendingPortControl(); ok {
		view.Status = pending
	} else {
		view.Status = a.readPortControlStatus()
	}
	return view
}

func (a *app) apiPortControlGet(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if a.rejectUnsafeSettingsHost(w, r) {
		return
	}
	writeJSON(w, a.portControlView())
}

func validatePortControlChanges(input portControlInput, ports []portControlPort) ([]portControlChange, error) {
	allowed := make(map[string]portControlPort, len(ports))
	desired := make(map[string]portControlPort, len(ports))
	for _, port := range ports {
		allowed[port.ID] = port
		desired[port.ID] = port
	}
	if len(input.Changes) == 0 || len(input.Changes) > len(allowed) {
		return nil, errors.New("invalid port changes")
	}
	seen := make(map[string]bool, len(input.Changes))
	changes := make([]portControlChange, 0, len(input.Changes))
	for _, inputChange := range input.Changes {
		current, ok := allowed[inputChange.Port]
		if !ok || seen[inputChange.Port] || inputChange.Enabled == nil {
			return nil, errors.New("invalid port change")
		}
		seen[inputChange.Port] = true
		change := portControlChange{Port: inputChange.Port, Enabled: *inputChange.Enabled}
		if change.Enabled {
			if !inputChange.hostPortPresent || inputChange.HostPort == nil || *inputChange.HostPort < 1 || *inputChange.HostPort > 65535 {
				return nil, errors.New("enter a port from 1 to 65535")
			}
			if !privateLANAddress(current.BindIP) {
				return nil, fmt.Errorf("configure a private LAN address on the server before opening %s", portControlLabel(current.ID))
			}
			change.BindIP, change.HostPort = current.BindIP, *inputChange.HostPort
			if current.Enabled && current.BindIP == change.BindIP && current.HostPort == change.HostPort {
				return nil, errors.New("port change has no effect")
			}
		} else if inputChange.hostPortPresent || !current.Enabled {
			return nil, errors.New("disabled port change is invalid")
		}
		next := current
		next.Enabled, next.BindIP, next.HostPort = change.Enabled, change.BindIP, change.HostPort
		desired[change.Port] = next
		changes = append(changes, change)
	}
	dashboardPort, err := strconv.Atoi(os.Getenv("DASHBOARD_PORT"))
	if err != nil || dashboardPort < 1 || dashboardPort > 65535 {
		dashboardPort = 80
	}
	for _, change := range changes {
		if !change.Enabled {
			continue
		}
		if change.HostPort == dashboardPort && change.BindIP == strings.TrimSpace(os.Getenv("DASHBOARD_BIND_IP")) {
			return nil, fmt.Errorf("Selected port for %s is already in use.", portControlLabel(change.Port))
		}
		for _, port := range desired {
			if port.ID != change.Port && port.Enabled && port.BindIP == change.BindIP && port.HostPort == change.HostPort {
				return nil, fmt.Errorf("Selected port for %s is already in use.", portControlLabel(change.Port))
			}
		}
	}
	return changes, nil
}

func (a *app) apiPortControlPost(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if rejectCredentialCSRF(w, r) || a.rejectUnsafeSettingsHost(w, r) {
		return
	}
	if !a.portControlAvailable() {
		http.Error(w, "host port control is unavailable", http.StatusServiceUnavailable)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxPortControlBody)
	var input portControlInput
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || ensureJSONEnd(decoder) != nil {
		http.Error(w, "invalid port control request", http.StatusBadRequest)
		return
	}
	changes, err := validatePortControlChanges(input, a.portControlPorts())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	a.controlMu.Lock()
	defer a.controlMu.Unlock()
	if a.controlRequestPending() {
		http.Error(w, errPortControlBusy.Error(), http.StatusConflict)
		return
	}
	request := portControlRequest{ID: newRequestID(), Changes: changes, RequestedAt: time.Now().Unix()}
	b, err := json.Marshal(request)
	if err != nil || writePrivateFileAtomic(a.portControlPath("request"), append(b, '\n')) != nil {
		http.Error(w, "port control unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	status := &portControlStatus{ID: request.ID, State: "pending", Message: "Applying LAN port changes…", AffectedServices: affectedPortServices()}
	writeJSON(w, portControlView{Available: true, Ports: a.portControlPorts(), Status: status})
}

func validPortControlID(value string) bool {
	return lowerHex(value, 32)
}

func newRequestID() string {
	raw := make([]byte, 16)
	_, _ = rand.Read(raw)
	return hex.EncodeToString(raw)
}
