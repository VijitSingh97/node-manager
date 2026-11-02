package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

type credentialRequest struct {
	ID          string `json:"id"`
	Node        string `json:"node"`
	RequestedAt int64  `json:"requested_at"`
}

type credentialStatus struct {
	ID               string   `json:"id,omitempty"`
	Node             string   `json:"node,omitempty"`
	State            string   `json:"state"`
	Message          string   `json:"message"`
	AffectedServices []string `json:"affected_services,omitempty"`
}

type credentialInterface struct {
	Node    string `json:"node"`
	Enabled bool   `json:"enabled"`
}

func credentialServices(node string) []string {
	if node == nodeZcash {
		return []string{"dashboard"}
	}
	if node == nodeMonero {
		return []string{nodeMonero, "monero-wallet", "dashboard"}
	}
	return []string{node, "dashboard"}
}

func (a *app) credentialPath(name string) string {
	return filepath.Join(a.cfg.stateDir, "credential-control."+name)
}

func (a *app) interfaceCredentials(node string) (string, string, bool) {
	user, password, endpoint := "", "", ""
	switch node {
	case nodeBitcoin:
		if !a.cfg.bitcoinDisabled {
			user, password, endpoint = a.cfg.rpcUser, a.cfg.rpcPassword, a.cfg.bitcoinRPCEndpoint
		}
	case nodeMonero:
		if a.cfg.moneroEnabled {
			user, password, endpoint = a.cfg.moneroRPCUser, a.cfg.moneroRPCPassword, a.cfg.moneroRPCEndpoint
		}
	case nodeLitecoin, nodeDogecoin, nodeBitcoinCash, nodeZcash:
		if cfg, ok := a.cfg.nodes[node]; ok && cfg.Enabled && cfg.Supported {
			user, password, endpoint = cfg.RPCUser, cfg.RPCPassword, cfg.RPCEndpoint
		}
	}
	return user, password, endpoint != "" && user != "" && password != "" && a.nodeActive(node, true)
}

func passwordNode(node string) bool {
	switch node {
	case nodeBitcoin, nodeMonero, nodeLitecoin, nodeDogecoin, nodeBitcoinCash, nodeZcash:
		return true
	}
	return false
}

func (a *app) credentialControlAvailable() bool {
	if !a.hasDashboardPassword() {
		return false
	}
	b, err := readSmallFile(a.credentialPath("capability"), 128)
	var capability struct {
		Timestamp int64 `json:"timestamp"`
	}
	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.DisallowUnknownFields()
	return err == nil && decoder.Decode(&capability) == nil && ensureJSONEnd(decoder) == nil && capability.Timestamp > 0 && time.Since(time.Unix(capability.Timestamp, 0)) >= 0 && time.Since(time.Unix(capability.Timestamp, 0)) <= portControlCapabilityAge
}

func (a *app) pendingCredentials() (*credentialStatus, bool) {
	for _, name := range []string{"request", "processing"} {
		b, err := readSmallFile(a.credentialPath(name), 1024)
		if os.IsNotExist(err) {
			continue
		}
		status := &credentialStatus{State: "pending", Message: "Checking password reset…"}
		var request credentialRequest
		decoder := json.NewDecoder(bytes.NewReader(b))
		decoder.DisallowUnknownFields()
		if err == nil && decoder.Decode(&request) == nil && ensureJSONEnd(decoder) == nil && validPortControlID(request.ID) && passwordNode(request.Node) && request.RequestedAt > 0 && request.RequestedAt <= time.Now().Unix()+30 && time.Now().Unix()-request.RequestedAt <= 900 {
			status.ID, status.Node = request.ID, request.Node
			status.Message = "Resetting " + nodeDisplayName(request.Node) + " RPC password…"
			status.AffectedServices = credentialServices(request.Node)
		}
		return status, true
	}
	return nil, false
}

func (a *app) credentialTransitioning(node string) bool {
	status, pending := a.pendingCredentials()
	return pending && status.Node == node && status.ID != ""
}

func (a *app) controlRequestPending() bool {
	for _, prefix := range []string{"node-control", "port-control", "credential-control"} {
		for _, suffix := range []string{"request", "processing"} {
			if _, err := os.Lstat(filepath.Join(a.cfg.stateDir, prefix+"."+suffix)); !os.IsNotExist(err) {
				return true
			}
		}
	}
	return false
}

func (a *app) credentialControlView() map[string]any {
	interfaces := []credentialInterface{}
	for _, node := range nodeOrder {
		configured, supported := a.nodeConfigured(node)
		if !passwordNode(node) || !configured || !supported {
			continue
		}
		_, _, enabled := a.interfaceCredentials(node)
		interfaces = append(interfaces, credentialInterface{Node: node, Enabled: enabled})
	}
	status, pending := a.pendingCredentials()
	if !pending {
		b, err := readSmallFile(a.credentialPath("status"), 2048)
		var saved credentialStatus
		decoder := json.NewDecoder(bytes.NewReader(b))
		decoder.DisallowUnknownFields()
		if err == nil && decoder.Decode(&saved) == nil && ensureJSONEnd(decoder) == nil && validPortControlID(saved.ID) && passwordNode(saved.Node) {
			switch saved.State {
			case "applied":
				saved.Message = nodeDisplayName(saved.Node) + " RPC password reset. Reveal the new credentials to reconnect your apps."
			case "rejected":
				saved.Message = "Password reset was rejected; no credentials changed."
			case "failed":
				saved.Message = "Password reset failed. Check the host helper status before retrying."
			default:
				saved.State = ""
			}
			if saved.State != "" {
				saved.AffectedServices = credentialServices(saved.Node)
				status = &saved
			}
		}
	}
	return map[string]any{"available": a.credentialControlAvailable(), "requires_password": !a.hasDashboardPassword(), "interfaces": interfaces, "status": status}
}

func (a *app) apiCredentialsGet(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if a.rejectUnsafeSettingsHost(w, r) {
		return
	}
	writeJSON(w, a.credentialControlView())
}

func (a *app) credentialInput(w http.ResponseWriter, r *http.Request, reset bool) (string, bool) {
	w.Header().Set("Cache-Control", "no-store")
	if !a.hasDashboardPassword() {
		http.Error(w, "set a dashboard password to manage RPC credentials", http.StatusForbidden)
		return "", false
	}
	if !a.requireRecentSignIn(w, r) {
		return "", false
	}
	if rejectCredentialCSRF(w, r) {
		return "", false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1024)
	var input struct {
		Node         string `json:"node"`
		Confirmation string `json:"confirmation,omitempty"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || ensureJSONEnd(decoder) != nil || !passwordNode(input.Node) || reset && input.Confirmation != "reset" || !reset && input.Confirmation != "" {
		http.Error(w, "invalid credential request", http.StatusBadRequest)
		return "", false
	}
	if _, _, enabled := a.interfaceCredentials(input.Node); !enabled {
		http.Error(w, "LAN RPC is not enabled for this node", http.StatusBadRequest)
		return "", false
	}
	return input.Node, true
}

func (a *app) apiCredentialsReveal(w http.ResponseWriter, r *http.Request) {
	node, ok := a.credentialInput(w, r, false)
	if !ok {
		return
	}
	a.controlMu.Lock()
	defer a.controlMu.Unlock()
	if a.controlRequestPending() {
		http.Error(w, "wait for pending node changes to finish", http.StatusConflict)
		return
	}
	user, password, _ := a.interfaceCredentials(node)
	writeJSON(w, map[string]string{"username": user, "password": password})
}

func (a *app) apiCredentialsReset(w http.ResponseWriter, r *http.Request) {
	node, ok := a.credentialInput(w, r, true)
	if !ok {
		return
	}
	if !a.credentialControlAvailable() {
		http.Error(w, "host password control is unavailable", http.StatusServiceUnavailable)
		return
	}
	a.controlMu.Lock()
	defer a.controlMu.Unlock()
	if a.controlRequestPending() {
		http.Error(w, "wait for pending node changes to finish", http.StatusConflict)
		return
	}
	id := make([]byte, 16)
	if _, err := rand.Read(id); err != nil {
		http.Error(w, "password reset unavailable", http.StatusServiceUnavailable)
		return
	}
	request := credentialRequest{ID: hex.EncodeToString(id), Node: node, RequestedAt: time.Now().Unix()}
	b, _ := json.Marshal(request)
	if writePrivateFileAtomic(a.credentialPath("request"), append(b, '\n')) != nil {
		http.Error(w, "password reset could not be saved", http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusAccepted)
	writeJSON(w, a.credentialControlView())
}
