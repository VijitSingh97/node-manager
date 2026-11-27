package main

import (
	"context"
	"encoding/hex"
	"testing"
)

func benchHostMatchesScan(t *testing.T, n *liveNode, id string, chain *utxoChainParams, addrs, scripts []string) {
	t.Helper()
	ctx := context.Background()
	host := (&app{cfg: config{stateDir: t.TempDir()}}).chainBenchHost(id, n.rpc, chain, func() bool { return true }, func() {})
	keys := make([]string, len(addrs))
	var wantCoins int
	var wantSats uint64
	for i, a := range addrs {
		keys[i] = "addr(" + a + ")"
		if err := host.validate(ctx, keys[i]); err != nil {
			t.Fatalf("validate %s: %v", keys[i], err)
		}
		script := scripts[i]
		for _, sats := range n.scan(script) {
			wantCoins++
			wantSats += sats
		}
		resolved, err := host.resolve(ctx, keys[i:i+1])
		if err != nil || len(resolved) != 1 || len(resolved[0].Scripts) == 0 || hex.EncodeToString(resolved[0].Scripts[0]) != script {
			t.Fatalf("resolve %s: %+v %v, want script %s", keys[i], resolved, err, script)
		}
	}
	run, err := host.scan(ctx, keys)
	if err != nil {
		t.Fatalf("bench scan: %v", err)
	}
	if wantCoins == 0 || run.Totals.Coins != wantCoins || uint64(run.Totals.Sats) != wantSats {
		t.Fatalf("bench scan found %d coins worth %d sats, direct scantxoutset %d and %d", run.Totals.Coins, run.Totals.Sats, wantCoins, wantSats)
	}
	t.Logf("bench host matched scantxoutset on %d coins worth %d sats across %d addresses", wantCoins, wantSats, len(addrs))
}
