package main

import (
	"context"
	"fmt"
)

const zakuraEndOfSupportWarnBlocks = 14 * 3456

type zakuraDeprecationInfo struct {
	EndOfService *struct {
		BlockHeight int64 `json:"block_height"`
	} `json:"end_of_service"`
}

func applyZakuraEndOfSupport(ctx context.Context, rpc *rpcClient, status *genericNodeStatus) {
	var info zakuraDeprecationInfo
	if rpc.call(ctx, "getdeprecationinfo", nil, &info) != nil || info.EndOfService == nil || info.EndOfService.BlockHeight <= 0 {
		return
	}
	status.EndOfSupportHeight = info.EndOfService.BlockHeight
	status.EndOfSupportRemaining = status.EndOfSupportHeight - status.Height
	status.EndOfSupportWarning = status.EndOfSupportRemaining <= zakuraEndOfSupportWarnBlocks
}

func (m *Monitor) tickEndOfSupport(ctx context.Context, state *monitorState, chain string, info map[string]any) {
	if info == nil {
		return
	}
	height, ok := integer(info["end_of_support_height"])
	if !ok {
		state.eosAlerted = false
		return
	}
	if state.eosAlerted {
		return
	}
	remaining, _ := integer(info["end_of_support_remaining_blocks"])
	message := fmt.Sprintf("⚠️ Zakura stops at block %d (%d blocks left) — upgrade node-manager once a newer Zakura is pinned", height, remaining)
	state.eosAlerted = m.sendOperationalTelegram(ctx, chainAlert(chain, message))
}
