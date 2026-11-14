package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"
)

func startRegtestNode(t *testing.T) *rpcClient {
	t.Helper()
	if os.Getenv("UTXO_REGTEST") != "1" {
		t.Skip("set UTXO_REGTEST=1 to run against the pinned regtest bitcoind")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not installed")
	}
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skip("docker daemon not running")
	}
	compose, err := os.ReadFile("../../docker-compose.yml")
	if err != nil {
		t.Fatal(err)
	}
	image := regexp.MustCompile(`image: (bitcoin/bitcoin:\S+)`).FindSubmatch(compose)[1]
	name := "derive-regtest-" + time.Now().Format("150405.000")[7:]
	out, err := exec.Command("docker", "run", "-d", "--rm", "--name", name, "--cpus", "0.5", "-p", "127.0.0.1::18443", string(image),
		"bitcoind", "-regtest", "-printtoconsole=0", "-rpcuser=test", "-rpcpassword=test", "-rpcallowip=0.0.0.0/0", "-rpcbind=0.0.0.0").CombinedOutput()
	if err != nil {
		t.Fatalf("docker run: %v\n%s", err, out)
	}
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", name).Run() })
	portOut, err := exec.Command("docker", "port", name, "18443/tcp").Output()
	if err != nil {
		t.Fatal(err)
	}
	addr := strings.TrimSpace(strings.SplitN(string(portOut), "\n", 2)[0])
	rpc := &rpcClient{url: "http://" + addr, user: "test", password: "test", client: &http.Client{Timeout: 30 * time.Second}}
	for i := 0; i < 60; i++ {
		var n int
		if rpc.call(context.Background(), "getblockcount", nil, &n) == nil {
			return rpc
		}
		time.Sleep(time.Second)
	}
	t.Fatal("bitcoind did not start")
	return nil
}

func regtestKey(t *testing.T) string {
	t.Helper()
	raw, err := base58DecodeCheck(testXpub)
	if err != nil {
		t.Fatal(err)
	}
	return base58EncodeCheck(append(mustHex("043587cf"), raw[4:]...))
}

func TestResolveWalletScriptsRegtestLive(t *testing.T) {
	rpc := startRegtestNode(t)
	ctx := context.Background()
	tpub := regtestKey(t)
	keys := []string{
		tpub,
		"tr(" + tpub + "/0/*)",
		"pkh(" + tpub + "/<0;1>/*)",
		"sh(wpkh(" + tpub + "/<0;1>/*))",
		"pk(" + strings.Repeat("02", 1) + "79be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798)",
		"combo(" + tpub + "/0/*)",
		"wpkh(" + testXpub + "/0/*)",
	}
	start := time.Now()
	got, err := resolveWalletScripts(ctx, rpc, keys)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("resolved %d wallets in %v (docker --cpus 0.5)", len(keys), elapsed)
	for _, w := range []int{4, 5, 6} {
		if !got[w].Fallback {
			t.Fatalf("wallet %d (pk, combo, mainnet key on regtest) did not fall back", w)
		}
	}
	for w := 0; w < 4; w++ {
		if got[w].Fallback || len(got[w].Scripts) == 0 {
			t.Fatalf("wallet %d fell back", w)
		}
		descs, _ := descriptorsFor(keys[w])
		var want [][]byte
		for _, d := range descs {
			var info struct {
				Descriptor string `json:"descriptor"`
				IsRange    bool   `json:"isrange"`
			}
			if err := rpc.call(ctx, "getdescriptorinfo", []any{d.Desc}, &info); err != nil {
				t.Fatal(err)
			}
			params := []any{info.Descriptor}
			if info.IsRange {
				params = append(params, []int{0, 999})
			}
			var addresses []string
			if err := rpc.call(ctx, "deriveaddresses", params, &addresses); err != nil {
				t.Fatal(err)
			}
			calls := make([]rpcBatchCall, len(addresses))
			for i, a := range addresses {
				calls[i] = rpcBatchCall{"validateaddress", []any{a}}
			}
			results, err := rpc.callBatch(ctx, calls)
			if err != nil {
				t.Fatal(err)
			}
			for _, r := range results {
				var v struct {
					ScriptPubKey string `json:"scriptPubKey"`
				}
				if err := json.Unmarshal(r.Result, &v); err != nil || v.ScriptPubKey == "" {
					t.Fatalf("validateaddress: %v", err)
				}
				script, _ := hex.DecodeString(v.ScriptPubKey)
				want = append(want, script)
			}
		}
		if wantCount := 1000 * len(descs); len(want) != wantCount {
			t.Fatalf("wallet %d: bitcoind derived %d addresses, want %d", w, len(want), wantCount)
		}
		want = sortedUniqueScripts(want)
		if len(want) != len(got[w].Scripts) {
			t.Fatalf("wallet %d: %d scripts, bitcoind says %d", w, len(got[w].Scripts), len(want))
		}
		for i := range want {
			if !bytes.Equal(want[i], got[w].Scripts[i]) {
				t.Fatalf("wallet %d script %d: %x != %x", w, i, got[w].Scripts[i], want[i])
			}
		}
	}
}

func TestResolveTwoThousandAddressesRegtestLiveTiming(t *testing.T) {
	rpc := startRegtestNode(t)
	var best time.Duration
	for i := 0; i < 5; i++ {
		start := time.Now()
		got, err := resolveWalletScripts(context.Background(), rpc, []string{regtestKey(t)})
		took := time.Since(start)
		if err != nil || got[0].Fallback || len(got[0].Scripts) != 2000 {
			t.Fatalf("resolve: %v %+v", err, got)
		}
		t.Logf("run %d: 2000 addresses (1 xpub, 0..999 receive and change) resolved in %v", i, took)
		if best == 0 || took < best {
			best = took
		}
	}
	t.Logf("best of 5 (docker --cpus 0.5): %v; under 1 s: %v", best, best < time.Second)
}
