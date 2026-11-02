package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func (a *app) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /login", a.loginPage)
	mux.HandleFunc("POST /login", a.loginPost)
	mux.HandleFunc("POST /logout", a.logout)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("GET /", a.index)
	mux.HandleFunc("GET /metrics", a.metrics)
	mux.HandleFunc("GET /api/fees", a.apiFees)
	mux.HandleFunc("GET /api/prices", a.apiPrices)
	mux.HandleFunc("GET /api/monero", a.apiMonero)
	mux.HandleFunc("GET /api/{node}", a.apiAdditionalNode)
	mux.HandleFunc("GET /api/{node}/fees", a.apiAdditionalFees)
	mux.HandleFunc("GET /api/{node}/watch", a.apiFamilyWatchList)
	mux.HandleFunc("POST /api/{node}/watch", a.apiFamilyWatchAdd)
	mux.HandleFunc("DELETE /api/{node}/watch/{id}", a.apiFamilyWatchRemove)
	mux.HandleFunc("GET /api/nodes", a.apiNodes)
	mux.HandleFunc("POST /api/nodes/{node}/power", a.apiNodePower)
	mux.HandleFunc("GET /api/settings/host", a.apiHostControlGet)
	mux.HandleFunc("POST /api/settings/host", a.apiHostControlPost)
	mux.HandleFunc("GET /api/monero/fees", a.apiMoneroFees)
	mux.HandleFunc("GET /api/monero/watch", a.apiMoneroWatchList)
	mux.HandleFunc("POST /api/monero/watch", a.apiMoneroWatchAdd)
	mux.HandleFunc("DELETE /api/monero/watch/{id}", a.apiMoneroWatchRemove)
	mux.HandleFunc("POST /api/monero/watch/{id}/key-images", a.apiMoneroWatchKeyImages)
	mux.HandleFunc("GET /api/tari/watch", a.apiTariWatch)
	mux.HandleFunc("POST /api/tari/watch", a.apiTariWatchAdd)
	mux.HandleFunc("DELETE /api/tari/watch/{id}", a.apiTariWatchRemove)
	mux.HandleFunc("POST /api/settings/password", a.apiDashboardPassword)
	mux.HandleFunc("GET /api/settings/credentials", a.apiCredentialsGet)
	mux.HandleFunc("POST /api/settings/credentials/reveal", a.apiCredentialsReveal)
	mux.HandleFunc("POST /api/settings/credentials/reset", a.apiCredentialsReset)
	mux.HandleFunc("GET /api/settings", a.apiSettingsGet)
	mux.HandleFunc("POST /api/settings", a.apiSettingsPost)
	mux.HandleFunc("GET /api/settings/ports", a.apiPortControlGet)
	mux.HandleFunc("POST /api/settings/ports", a.apiPortControlPost)
	mux.HandleFunc("GET /api/watch", a.apiWatchList)
	mux.HandleFunc("POST /api/watch", a.apiWatchAdd)
	mux.HandleFunc("DELETE /api/watch/{name}", a.apiWatchRemove)
	mux.Handle("GET /static/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache, max-age=0")
		a.static.ServeHTTP(w, r)
	}))
	return a.auth(mux)
}

func (a *app) metrics(w http.ResponseWriter, r *http.Request) {
	if a.cfg.bitcoinDisabled {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		fmt.Fprint(w, "# TYPE bitcoin_node_enabled gauge\nbitcoin_node_enabled 0\n# TYPE bitcoin_node_up gauge\nbitcoin_node_up 0\n")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	var chain blockchainInfo
	if err := a.rpc.call(ctx, "getblockchaininfo", []any{}, &chain); err != nil {
		fmt.Fprint(w, "# TYPE bitcoin_node_up gauge\nbitcoin_node_up 0\n")
		return
	}
	var network networkInfo
	var peers []peerInfo
	var uptime float64
	_ = a.rpc.call(ctx, "getnetworkinfo", []any{}, &network)
	_ = a.rpc.call(ctx, "getpeerinfo", []any{}, &peers)
	_ = a.rpc.call(ctx, "uptime", []any{}, &uptime)
	inbound := 0
	for _, peer := range peers {
		if peer.Inbound {
			inbound++
		}
	}
	disk := diskUsage(a.cfg.dataDir)
	gauge := func(name string, value any) { fmt.Fprintf(w, "# TYPE %s gauge\n%s %v\n", name, name, value) }
	gauge("bitcoin_node_up", 1)
	gauge("bitcoin_blocks", chain.Blocks)
	gauge("bitcoin_headers", chain.Headers)
	gauge("bitcoin_verification_progress", chain.VerificationProgress)
	gauge("bitcoin_pruned", boolInt(chain.Pruned))
	gauge("bitcoin_size_on_disk_bytes", chain.SizeOnDisk)
	gauge("bitcoin_disk_free_bytes", disk.Free)
	gauge("bitcoin_uptime_seconds", uptime)
	gauge("bitcoin_connections", network.Connections)
	fmt.Fprintf(w, "# TYPE bitcoin_peers gauge\nbitcoin_peers{direction=\"in\"} %d\nbitcoin_peers{direction=\"out\"} %d\n", inbound, len(peers)-inbound)
	if !chain.InitialBlockDownload {
		var mempool mempoolInfo
		if a.rpc.call(ctx, "getmempoolinfo", []any{}, &mempool) == nil {
			gauge("bitcoin_mempool_txs", mempool.Size)
			gauge("bitcoin_mempool_bytes", mempool.Bytes)
		}
		fmt.Fprintln(w, "# TYPE bitcoin_fee_sat_vb gauge")
		for _, blocks := range []int{1, 3, 6} {
			if fee := a.fee(ctx, blocks); fee != "—" {
				fmt.Fprintf(w, "bitcoin_fee_sat_vb{blocks=\"%d\"} %s\n", blocks, fee)
			}
		}
	}
}

func (a *app) apiFees(w http.ResponseWriter, _ *http.Request) {
	a.feesMu.RLock()
	fees := append([]float64{}, a.fees...)
	a.feesMu.RUnlock()
	writeJSON(w, map[string]any{"fee": fees})
}

func (a *app) apiPrices(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, a.prices.SnapshotAsset(r.URL.Query().Get("asset")))
}

func (a *app) nodeRPC(method string, params []any) any {
	if a.cfg.bitcoinDisabled {
		return nil
	}
	var result any
	if a.rpc.call(a.ctx, method, params, &result) != nil {
		return nil
	}
	return result
}

func (a *app) walletRPC(wallet, method string, params []any, timeout time.Duration) any {
	if a.cfg.bitcoinDisabled {
		return nil
	}
	rpc := *a.rpc
	rpc.url = strings.TrimRight(rpc.url, "/") + "/wallet/" + url.PathEscape(wallet)
	rpc.client = &http.Client{Timeout: timeout}
	var result any
	if rpc.call(a.ctx, method, params, &result) != nil {
		return nil
	}
	return result
}

func rejectCSRF(w http.ResponseWriter, r *http.Request) bool {
	if r.Header.Get("X-Requested-With") != "fetch" {
		http.Error(w, "missing X-Requested-With", http.StatusForbidden)
		return true
	}
	return false
}

func (a *app) apiWatchList(w http.ResponseWriter, r *http.Request) {
	if a.rejectDisabledBitcoin(w) {
		return
	}
	if a.watch.Err() != nil {
		http.Error(w, "watch wallet storage unavailable", http.StatusServiceUnavailable)
		return
	}
	entries := a.watch.Entries()
	if !a.configurationAllowed(r) && !publicExampleOnly(entries) {
		http.Error(w, "set dashboard.password to access watch-only wallets", 403)
		return
	}
	var chain blockchainInfo
	_ = a.rpc.call(r.Context(), "getblockchaininfo", nil, &chain)
	view := a.watch.ListWithChain(a.walletRPC, chain)
	view["has_password"] = a.hasDashboardPassword()
	view["can_configure"] = a.configurationAllowed(r)
	writeJSON(w, view)
}

func publicExampleOnly(entries []WatchEntry) bool {
	return len(entries) == 0 || len(entries) == 1 && isPublicExample(entries[0])
}

func (a *app) apiWatchAdd(w http.ResponseWriter, r *http.Request) {
	if a.rejectDisabledBitcoin(w) {
		return
	}
	if rejectCredentialCSRF(w, r) || a.rejectUnsafeSettingsHost(w, r) {
		return
	}
	if a.watch.Err() != nil {
		http.Error(w, "watch wallet storage unavailable", http.StatusServiceUnavailable)
		return
	}
	var entry WatchEntry
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := json.NewDecoder(r.Body).Decode(&entry); err != nil {
		http.Error(w, "Name and key are both required.", 400)
		return
	}
	descs, err := descriptorsFor(entry.Key)
	if err != nil {
		http.Error(w, "A valid public key, address, or descriptor is required.", 400)
		return
	}
	type validatedDescriptor struct {
		desc   string
		ranged bool
	}
	validated := make([]validatedDescriptor, 0, len(descs))
	for _, desc := range descs {
		var info struct {
			Descriptor     string `json:"descriptor"`
			IsRange        *bool  `json:"isrange"`
			HasPrivateKeys *bool  `json:"hasprivatekeys"`
		}
		if err := a.rpc.call(r.Context(), "getdescriptorinfo", []any{desc.Desc}, &info); err != nil || info.Descriptor == "" || info.IsRange == nil || info.HasPrivateKeys == nil || *info.HasPrivateKeys {
			http.Error(w, "A valid public key, address, or descriptor is required.", 400)
			return
		}
		validated = append(validated, validatedDescriptor{desc: info.Descriptor, ranged: *info.IsRange})
	}
	var chain blockchainInfo
	_ = a.rpc.call(r.Context(), "getblockchaininfo", nil, &chain)
	if chain.Pruned {
		for _, desc := range validated {
			params := []any{desc.desc}
			if desc.ranged {
				params = append(params, []int{0, 999})
			}
			var addresses []string
			if err := a.rpc.call(r.Context(), "deriveaddresses", params, &addresses); err != nil || len(addresses) == 0 {
				http.Error(w, "A valid public key, address, or descriptor is required.", 400)
				return
			}
		}
	}
	saved, err := a.watch.Add(entry.Name, entry.Key, entry.Birthday)
	if err != nil {
		if errors.Is(err, ErrWatchUnavailable) {
			http.Error(w, "watch wallet storage unavailable", http.StatusServiceUnavailable)
			return
		}
		http.Error(w, err.Error(), 400)
		return
	}
	a.watch.SetPending(saved.Key, true)
	go a.provisionForChain(saved, chain)
	writeJSON(w, map[string]any{"ok": true})
}

func (a *app) apiWatchRemove(w http.ResponseWriter, r *http.Request) {
	if a.rejectDisabledBitcoin(w) {
		return
	}
	if rejectCredentialCSRF(w, r) || a.rejectUnsafeSettingsHost(w, r) {
		return
	}
	if a.watch.Err() != nil {
		http.Error(w, "watch wallet storage unavailable", http.StatusServiceUnavailable)
		return
	}
	name := r.PathValue("name")
	var entry WatchEntry
	for _, candidate := range a.watch.Entries() {
		if candidate.Name == name {
			entry = candidate
			break
		}
	}
	if entry.Name == "" {
		http.Error(w, "no such wallet", 404)
		return
	}
	coreName, _ := a.watch.resolveWallet(a.walletRPC, entry)
	removed, err := a.watch.RemoveExact(entry)
	if errors.Is(err, ErrWatchUnavailable) {
		http.Error(w, "watch wallet storage unavailable", http.StatusServiceUnavailable)
		return
	}
	if !removed {
		http.Error(w, "no such wallet", 404)
		return
	}
	a.watch.DeprovisionNameIfUnused(a.nodeRPC, a.walletRPC, entry, coreName)
	writeJSON(w, map[string]any{"ok": true})
}

func (a *app) rejectDisabledBitcoin(w http.ResponseWriter) bool {
	if !a.cfg.bitcoinDisabled {
		return false
	}
	http.Error(w, "Bitcoin node is not configured", http.StatusServiceUnavailable)
	return true
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}
