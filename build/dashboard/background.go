package main

import (
	"context"
	"math"
	"net/http"
	"sync"
	"time"
)

const healthRPCTimeout = 60 * time.Second

func (a *app) recordFee(value float64) {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return
	}
	a.feesMu.Lock()
	defer a.feesMu.Unlock()
	a.fees = append(a.fees, value)
	if len(a.fees) > maxFees {
		a.fees = append([]float64{}, a.fees[len(a.fees)-maxFees:]...)
	}
}

func (a *app) startBackground(ctx context.Context) <-chan struct{} {
	a.ctx = ctx
	var workers sync.WaitGroup
	start := func(worker func()) {
		workers.Add(1)
		go func() {
			defer workers.Done()
			worker()
		}()
	}
	hasNode := !a.cfg.bitcoinDisabled || a.cfg.moneroEnabled
	for _, node := range a.cfg.nodes {
		hasNode = hasNode || node.Enabled && node.Supported
	}
	if hasNode {
		start(func() { a.prices.Run(ctx) })
	}
	if a.familyWatch != nil && a.familyWatchWorkerWanted() {
		start(func() { a.familyWatch.run(ctx) })
	}
	if a.cfg.moneroWalletEnabled {
		start(func() { a.moneroWatch.run(ctx, func() bool { return a.nodeWorkerRunnable(nodeMonero, true) }) })
	}
	if a.cfg.tariWalletEnabled {
		start(func() {
			a.tariWatch.run(ctx, func() bool { return a.nodeWorkerRunnable(nodeTari, true) }, func(ctx context.Context) (tariWatchNodeSnapshot, error) {
				node := a.cfg.nodes[nodeTari]
				status, err := a.fetchTariStatus(ctx, node, genericNodeStatus{})
				return tariWatchNodeSnapshot{
					Ready:  status.Synced && status.LatestBlockAvailable && status.LatestBlockAgeSeconds <= int64(tariTipFreshFor/time.Second),
					Height: uint64(max(0, status.Height)), TargetHeight: uint64(max(0, status.TargetHeight)),
					Reason: "Waiting for the Tari node to sync.",
				}, err
			})
		})
	}
	if a.cfg.moneroEnabled {
		start(func() { a.runMoneroFees(ctx) })
	}
	for id, node := range a.cfg.nodes {
		if !node.Enabled || node.Protocol != "bitcoin-json-rpc" && node.Protocol != "zcash-json-rpc" {
			continue
		}
		id, node := id, node
		start(func() {
			a.every(ctx, time.Minute, func(ctx context.Context) {
				a.sampleAdditionalFee(ctx, id, node)
			})
		})
	}
	if hasNode {
		probes := make(map[string]func() map[string]any)
		if !a.cfg.bitcoinDisabled {
			probes[nodeBitcoin] = func() map[string]any { return a.healthBlockchain(ctx) }
		}
		if a.cfg.moneroEnabled {
			probes[nodeMonero] = func() map[string]any { return a.healthMonero(ctx) }
		}
		for id, node := range a.cfg.nodes {
			if !node.Enabled || !node.Supported {
				continue
			}
			id, node := id, node
			probes[id] = func() map[string]any { return a.healthAdditionalNode(ctx, id, node) }
		}
		start(func() { a.monitor.RunConfiguredNodes(ctx, probes) })
	}
	if a.cfg.bitcoinDisabled {
		done := make(chan struct{})
		go func() {
			workers.Wait()
			close(done)
		}()
		return done
	}
	start(func() { a.scanner.run(ctx) })
	entries := a.watch.Entries()
	if a.nodeWorkerRunnable(nodeBitcoin, true) {
		for _, entry := range entries {
			a.watch.SetPending(entry.Key, true)
		}
	}
	chain := func() map[string]any { return a.healthBlockchain(ctx) }
	start(func() {
		a.every(ctx, time.Minute, func(ctx context.Context) {
			if !a.nodeWorkerRunnable(nodeBitcoin, true) {
				return
			}
			var info blockchainInfo
			if a.rpc.call(ctx, "getblockchaininfo", nil, &info) != nil || info.InitialBlockDownload {
				return
			}
			var estimate feeEstimate
			if a.rpc.call(ctx, "estimatesmartfee", []any{1}, &estimate) == nil && estimate.FeeRate != nil {
				a.recordFee(math.RoundToEven(*estimate.FeeRate*1000000) / 10)
			}
		})
	})
	start(func() {
		a.every(ctx, 10*time.Minute, func(context.Context) {
			if !a.nodeWorkerRunnable(nodeBitcoin, true) {
				return
			}
			info := chain()
			if info != nil && info["initialblockdownload"] != true {
				a.watch.Sample(a.walletRPC)
			}
		})
	})
	start(func() {
		a.every(ctx, 30*time.Second, func(ctx context.Context) {
			if !a.nodeWorkerRunnable(nodeBitcoin, true) {
				return
			}
			var info blockchainInfo
			if a.rpc.call(ctx, "getblockchaininfo", nil, &info) == nil {
				a.scanner.refresh(a.watch.Entries(), info)
			}
		})
	})
	start(func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			if !a.nodeWorkerRunnable(nodeBitcoin, true) {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					continue
				}
			}
			var info blockchainInfo
			if a.rpc.call(ctx, "getblockchaininfo", nil, &info) == nil {
				if !a.nodeWorkerRunnable(nodeBitcoin, true) {
					continue
				}
				for _, entry := range a.watch.Entries() {
					a.watch.SetPending(entry.Key, true)
					a.provisionForChain(entry, info)
				}
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	})
	done := make(chan struct{})
	go func() {
		workers.Wait()
		close(done)
	}()
	return done
}

func (a *app) healthMonero(parent context.Context) map[string]any {
	if a.cfg.moneroEnabled && !a.nodeActive(nodeMonero, true) {
		a.recordNodeHealth(nodeMonero, false)
		return map[string]any{"active": false}
	}
	ctx, cancel := context.WithTimeout(parent, healthRPCTimeout)
	defer cancel()
	rpc := moneroRPC{
		url: a.cfg.moneroRPCURL, user: a.cfg.moneroRPCUser, password: a.cfg.moneroRPCPassword,
		client: &http.Client{Timeout: healthRPCTimeout, CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}},
	}
	var info moneroInfo
	if rpc.call(ctx, "get_info", &info) != nil {
		a.recordNodeHealth(nodeMonero, false)
		return nil
	}
	a.recordNodeHealth(nodeMonero, true)
	return map[string]any{
		"initialblockdownload": !info.Synchronized || info.BusySyncing,
		"blocks":               int64(info.Height),
	}
}

func (a *app) healthBlockchain(parent context.Context) map[string]any {
	if !a.nodeActive(nodeBitcoin, !a.cfg.bitcoinDisabled) {
		a.recordNodeHealth(nodeBitcoin, false)
		return map[string]any{"active": false}
	}
	ctx, cancel := context.WithTimeout(parent, healthRPCTimeout)
	defer cancel()
	rpc := *a.rpc
	client := *a.rpc.client
	client.Timeout = 0
	rpc.client = &client
	var result map[string]any
	if rpc.call(ctx, "getblockchaininfo", nil, &result) != nil {
		a.recordNodeHealth(nodeBitcoin, false)
		return nil
	}
	a.recordNodeHealth(nodeBitcoin, true)
	ibd, ok := result["initialblockdownload"].(bool)
	if !ok {
		return result
	}
	if !ibd {
		a.rememberSync(blockchainInfo{})
		return result
	}
	blocks, blocksOK := integer(result["blocks"])
	headers, headersOK := integer(result["headers"])
	progress, progressOK := result["verificationprogress"].(float64)
	if blocksOK && headersOK && progressOK {
		a.rememberSync(blockchainInfo{Blocks: blocks, Headers: headers, VerificationProgress: progress, InitialBlockDownload: true})
	}
	return result
}

func (a *app) healthAdditionalNode(parent context.Context, id string, node nodeConfig) map[string]any {
	if !a.nodeActive(id, node.Enabled) {
		a.recordNodeHealth(id, false)
		if id == nodeTari {
			a.updateTariSyncMarker(false)
		}
		return map[string]any{"active": false}
	}
	ctx, cancel := context.WithTimeout(parent, healthRPCTimeout)
	defer cancel()
	if node.Protocol == "tari-grpc" {
		status, err := a.fetchTariStatus(ctx, node, genericNodeStatus{Enabled: true, Active: true, Supported: true})
		if err != nil {
			a.recordNodeHealth(id, false)
			a.updateTariSyncMarker(false)
			return nil
		}
		return map[string]any{"active": true, "initialblockdownload": !status.Synced, "blocks": status.Height}
	}
	if node.Protocol == "zcash-json-rpc" {
		status, err := a.fetchZcashStatus(ctx, node, genericNodeStatus{Enabled: true, Active: true, Supported: true})
		if err != nil {
			a.recordNodeHealth(id, false)
			return nil
		}
		a.recordNodeHealth(id, true)
		return map[string]any{"active": true, "initialblockdownload": !status.Synced, "blocks": status.Height}
	}
	rpc := &rpcClient{url: node.RPCURL, user: node.RPCUser, password: node.RPCPassword, client: &http.Client{}}
	var result map[string]any
	if rpc.call(ctx, "getblockchaininfo", nil, &result) != nil {
		a.recordNodeHealth(id, false)
		return nil
	}
	a.recordNodeHealth(id, true)
	result["active"] = true
	return result
}

func (a *app) provision(entry WatchEntry, pruned bool) {
	if !a.nodeWorkerRunnable(nodeBitcoin, true) {
		return
	}
	a.provisionState(entry, pruned, false)
}

func (a *app) provisionForChain(entry WatchEntry, chain blockchainInfo) {
	if !a.nodeWorkerRunnable(nodeBitcoin, true) {
		return
	}
	if chain.Pruned && !chain.InitialBlockDownload && chain.BestBlockHash != "" {
		if cache, _, ok := a.watch.scanSnapshot(entry); ok && cache.BestBlock == chain.BestBlockHash {
			a.watch.ClearPendingIfCurrent(entry)
			return
		}
	}
	a.provisionState(entry, chain.Pruned, chain.InitialBlockDownload)
}

func (a *app) provisionState(entry WatchEntry, pruned, initialBlockDownload bool) {
	if !a.nodeWorkerRunnable(nodeBitcoin, true) || !a.watch.Contains(entry) {
		return
	}
	name, ok := ProvisionOne(a.nodeRPC, a.walletRPC, entry, pruned)
	if !ok {
		if pruned {
			if initialBlockDownload {
				return
			}
			if a.watch.ScanErr() != nil {
				a.watch.SetScanStateIfCurrent(entry, "error")
				a.watch.ClearPendingIfCurrent(entry)
				return
			}
			a.scanner.enqueue(entry, true)
			return
		}
		a.watch.ClearPendingIfCurrent(entry)
		return
	}
	a.watch.ClearPendingIfCurrent(entry)
	if !a.watch.SetCoreWalletIfCurrent(entry, name) {
		a.watch.DeprovisionNameIfUnused(a.nodeRPC, a.walletRPC, entry, name)
	}
}

func (a *app) nodeWorkerRunnable(id string, fallback bool) bool {
	return a.nodeActive(id, fallback) && !a.nodeTransitioning(id)
}

func (a *app) every(ctx context.Context, interval time.Duration, fn func(context.Context)) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		fn(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
