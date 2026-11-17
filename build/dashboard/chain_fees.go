package main

import (
	"context"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func familyFeeRate(ctx context.Context, rpc *rpcClient, id string, blocks int) (float64, bool) {
	var rate float64
	if id == nodeBitcoinCash {
		if rpc.call(ctx, "estimatefee", nil, &rate) != nil {
			return 0, false
		}
	} else {
		var estimate feeEstimate
		if rpc.call(ctx, "estimatesmartfee", []any{blocks}, &estimate) != nil || estimate.FeeRate == nil {
			return 0, false
		}
		rate = *estimate.FeeRate
	}
	if rate < 0 || math.IsNaN(rate) || math.IsInf(rate, 0) {
		return 0, false
	}
	if id != nodeDogecoin {
		rate *= 100000
	}
	return rate, !math.IsInf(rate, 0)
}

func formatFamilyFeeRate(id string, rate float64) string {
	precision := 3
	if id == nodeDogecoin {
		precision = 8
	}
	return strings.TrimRight(strings.TrimRight(strconv.FormatFloat(rate, 'f', precision, 64), "0"), ".")
}

func (a *app) sampleAdditionalFee(ctx context.Context, id string, node nodeConfig) {
	if !a.nodeWorkerRunnable(id, true) {
		return
	}
	rpc := &rpcClient{url: node.RPCURL, user: node.RPCUser, password: node.RPCPassword, client: newInternalHTTPClient(5 * time.Second)}
	if node.Protocol == "zcash-json-rpc" {
		rpc.user, rpc.password, rpc.cookiePath = "", "", node.RPCCookiePath
		var chain zcashBlockchainInfo
		var fee zcashStandardFee
		if rpc.call(ctx, "getblockchaininfo", nil, &chain) == nil && chain.Blocks >= max(chain.Headers, chain.EstimatedHeight) && chain.VerificationProgress >= 0.999999 && rpc.call(ctx, "getstandardfee", nil, &fee) == nil && fee.StandardFee > 0 {
			a.recordAdditionalFee(id, float64(fee.StandardFee))
		}
		return
	}
	var chain familyBlockchainInfo
	if rpc.call(ctx, "getblockchaininfo", nil, &chain) != nil || chain.InitialBlockDownload {
		return
	}
	if rate, ok := familyFeeRate(ctx, rpc, id, 1); ok {
		a.recordAdditionalFee(id, rate)
	}
}

func (a *app) recordAdditionalFee(id string, value float64) {
	a.additionalFeesMu.Lock()
	defer a.additionalFeesMu.Unlock()
	fees := append(a.additionalFees[id], value)
	if len(fees) > maxFees {
		fees = append([]float64{}, fees[len(fees)-maxFees:]...)
	}
	a.additionalFees[id] = fees
}

func (a *app) apiAdditionalFees(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("node")
	if _, ok := a.additionalNode(id); !ok {
		http.NotFound(w, r)
		return
	}
	a.additionalFeesMu.RLock()
	fees := append([]float64{}, a.additionalFees[id]...)
	a.additionalFeesMu.RUnlock()
	writeJSON(w, map[string]any{"fee": fees})
}
