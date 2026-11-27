package main

import (
	"fmt"
	"strconv"
)

const probeTipLag = 2

func probeGate(online, validated bool, scanned, proofHeight uint64) (bool, string) {
	switch {
	case !online:
		return false, "wallet network status is not ONLINE"
	case !validated:
		return false, "initial validation is not done"
	case scanned+probeTipLag < proofHeight:
		return false, fmt.Sprintf("scanned height %d is behind proof node height %d", scanned, proofHeight)
	}
	return true, ""
}

func probeExpectation(totalText, heightText string) (total, height uint64, enabled bool, err error) {
	if totalText == "" && heightText == "" {
		return 0, 0, false, nil
	}
	if total, err = strconv.ParseUint(totalText, 10, 64); err != nil {
		return 0, 0, false, fmt.Errorf("TARI_PROOF_EXPECT_TOTAL: %w", err)
	}
	if height, err = strconv.ParseUint(heightText, 10, 64); err != nil || height == 0 {
		return 0, 0, false, fmt.Errorf("TARI_PROOF_NODE_HEIGHT must be a positive integer")
	}
	return total, height, true, nil
}
