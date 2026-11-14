package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strings"
	"time"
)

func (a *app) familyWatchCapability(id string) (bool, string) {
	switch id {
	case nodeLitecoin, nodeBitcoinCash:
		return true, ""
	case nodeDogecoin:
		return false, "Dogecoin Core 1.14 has no pruned-compatible UTXO set address scan."
	case nodeZcash:
		if a.cfg.zcashAddressIndex {
			return true, ""
		}
		return false, "Zcash checkpoint pruning skips the address index needed for an accurate balance."
	case nodeTari:
		return false, "Tari watch-only balances require a view key and wallet service."
	default:
		return false, "Single-address UTXO scanning is not available for this node."
	}
}

func (a *app) apiFamilyWatchList(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("node")
	node, known := a.cfg.nodes[id]
	if !known {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	supported, reason := a.familyWatchCapability(id)
	active := node.Enabled && a.nodeActive(id, node.Enabled)
	response := map[string]any{"supported": supported, "enabled": node.Enabled, "active": active, "has_password": a.hasDashboardPassword(), "can_configure": a.configurationAllowed(r), "reason": reason, "wallets": []familyWatchWalletView{}}
	if !supported {
		writeJSON(w, response)
		return
	}
	store := a.familyWatch.stores[id]
	wallets, err := store.list(!a.configurationAllowed(r))
	if errors.Is(err, os.ErrPermission) {
		http.Error(w, "set dashboard.password to access watch-only wallets", http.StatusForbidden)
		return
	}
	if err != nil {
		http.Error(w, errFamilyWatchUnavailable.Error(), http.StatusServiceUnavailable)
		return
	}
	response["wallets"] = wallets
	if !node.Enabled {
		response["reason"] = node.Name + " is not configured."
	} else if !active {
		response["reason"] = node.Name + " is inactive."
	}
	writeJSON(w, response)
}

func (a *app) apiFamilyWatchAdd(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("node")
	store, ok := a.familyWatchMutation(w, r, id)
	if !ok {
		return
	}
	var input struct {
		Label   string `json:"label"`
		Address string `json:"address"`
	}
	if !decodeStrictJSON(w, r, 16<<10, &input) {
		return
	}
	input.Label, input.Address = strings.TrimSpace(input.Label), strings.TrimSpace(input.Address)
	if !validFamilyWatchLabel(input.Label) || !validFamilyWatchAddress(input.Address) {
		http.Error(w, "A label and public address are required.", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	address, err := store.backend.validate(ctx, store, input.Address)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	entry, err := store.add(input.Label, address)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, errFamilyWatchUnavailable) {
			status = http.StatusServiceUnavailable
		}
		http.Error(w, err.Error(), status)
		return
	}
	store.requestScan()
	writeJSON(w, map[string]any{"ok": true, "id": entry.ID})
}

func (a *app) apiFamilyWatchRemove(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("node")
	store, ok := a.familyWatchMutation(w, r, id)
	if !ok {
		return
	}
	walletID := r.PathValue("id")
	if !validFamilyWatchID(walletID) {
		http.Error(w, "no such address", http.StatusNotFound)
		return
	}
	removed, err := store.remove(walletID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	if !removed {
		http.Error(w, "no such address", http.StatusNotFound)
		return
	}
	store.requestScan()
	writeJSON(w, map[string]any{"ok": true})
}

func (a *app) familyWatchMutation(w http.ResponseWriter, r *http.Request, id string) (*familyWatchStore, bool) {
	node, known := a.cfg.nodes[id]
	if !known {
		http.NotFound(w, r)
		return nil, false
	}
	if supported, reason := a.familyWatchCapability(id); !supported {
		http.Error(w, reason, http.StatusNotImplemented)
		return nil, false
	}
	if rejectCredentialCSRF(w, r) || a.rejectUnsafeSettingsHost(w, r) {
		return nil, false
	}
	if !node.Enabled || !a.nodeWorkerRunnable(id, node.Enabled) {
		http.Error(w, node.Name+" is not active.", http.StatusServiceUnavailable)
		return nil, false
	}
	store := a.familyWatch.stores[id]
	if store == nil || store.Err() != nil {
		http.Error(w, errFamilyWatchUnavailable.Error(), http.StatusServiceUnavailable)
		return nil, false
	}
	return store, true
}
