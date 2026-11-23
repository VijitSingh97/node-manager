package main

import (
	"errors"
	"net/http"
	"strings"
)

func (a *app) apiTariWatch(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	node, configured := a.cfg.nodes[nodeTari]
	active := configured && node.Enabled && a.nodeWorkerRunnable(nodeTari, node.Enabled)
	response := map[string]any{
		"has_password": a.hasDashboardPassword(), "can_configure": a.configurationAllowed(r), "available": false, "active": active,
		"reason": "Tari watch-only wallet service is not configured.", "wallets": []TariWatchWallet{},
	}
	if !configured || !node.Enabled {
		response["reason"] = "Tari node is not configured."
		writeJSON(w, response)
		return
	}
	if !a.cfg.tariWalletEnabled {
		writeJSON(w, response)
		return
	}
	if a.tariWatch == nil || a.tariWatch.Err() != nil {
		http.Error(w, errTariWatchUnavailable.Error(), http.StatusServiceUnavailable)
		return
	}
	wallets := a.tariWatch.List()
	if !a.configurationAllowed(r) && len(wallets) != 0 {
		http.Error(w, "set dashboard.password to access personal Tari watch-only wallets", http.StatusForbidden)
		return
	}
	response["available"], response["wallets"] = a.tariWatch.Capable(), wallets
	if active {
		if a.tariWatch.Capable() {
			response["reason"] = ""
		} else {
			response["reason"] = "Tari watch-only wallet service is starting."
		}
	} else {
		response["reason"] = "Tari node is inactive."
	}
	writeJSON(w, response)
}

func (a *app) apiTariWatchAdd(w http.ResponseWriter, r *http.Request) {
	if a.rejectTariWatchMutation(w, r) {
		return
	}
	var input struct {
		Name           string `json:"name"`
		PublicSpendKey string `json:"public_spend_key"`
		ViewKey        string `json:"view_key"`
		Birthday       string `json:"birthday"`
	}
	if !decodeStrictJSON(w, r, 16<<10, &input) {
		return
	}
	wallet, err := a.tariWatch.Add(input.Name, input.PublicSpendKey, input.ViewKey, input.Birthday)
	input.ViewKey = ""
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, errTariWatchUnavailable) {
			status = http.StatusServiceUnavailable
		} else if errors.Is(err, errTariWatchConflict) {
			status = http.StatusConflict
		}
		http.Error(w, err.Error(), status)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	writeJSON(w, map[string]any{"ok": true, "id": wallet.ID})
}

func (a *app) apiTariWatchRemove(w http.ResponseWriter, r *http.Request) {
	if a.rejectTariWatchMutation(w, r) {
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if !lowerHex(id, 32) {
		http.Error(w, "no such wallet", http.StatusNotFound)
		return
	}
	removed, err := a.tariWatch.Remove(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	if !removed {
		http.Error(w, "no such wallet", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	writeJSON(w, map[string]any{"ok": true})
}

func (a *app) rejectTariWatchMutation(w http.ResponseWriter, r *http.Request) bool {
	node, configured := a.cfg.nodes[nodeTari]
	if !configured || !node.Enabled {
		http.Error(w, "Tari node is not configured", http.StatusServiceUnavailable)
		return true
	}
	if !a.cfg.tariWalletEnabled {
		http.Error(w, "Tari watch-only wallet service is not configured", http.StatusServiceUnavailable)
		return true
	}
	if rejectCredentialCSRF(w, r) || a.rejectUnsafeSettingsHost(w, r) {
		return true
	}
	if !a.nodeWorkerRunnable(nodeTari, node.Enabled) {
		http.Error(w, "Tari node is inactive.", http.StatusServiceUnavailable)
		return true
	}
	if a.tariWatch == nil || a.tariWatch.Err() != nil {
		http.Error(w, errTariWatchUnavailable.Error(), http.StatusServiceUnavailable)
		return true
	}
	if !a.tariWatch.Capable() {
		http.Error(w, "Tari watch-only wallet service is starting", http.StatusServiceUnavailable)
		return true
	}
	return false
}
