package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestUtxoReplayRegtestRate(t *testing.T) {
	if os.Getenv("UTXO_REPLAY_IN_CONTAINER") == "1" {
		t.Skip("running inside the capped replay container")
	}
	chain := os.Getenv("UTXO_REPLAY_CHAIN")
	var n *liveNode
	newScript := func(i int) (string, string) {
		return n.newScript([]string{"legacy", "p2sh-segwit", "bech32", "bech32m"}[i%4])
	}
	if chain == nodeBitcoinCash {
		n, newScript = startBitcoinCashNode(t), func(int) (string, string) { return n.cashScript() }
	} else {
		n = startLiveNode(t)
		n.do("createwallet", "w")
	}
	image := seedLiveDocker(t, "inspect", n.name, "--format", "{{.Config.Image}}")
	arch := seedLiveDocker(t, "version", "--format", "{{.Server.Arch}}")
	mine, mineScript := newScript(2)
	for i := 0; i < 40; i++ {
		n.do("generatetoaddress", 500, mine)
	}
	small := n.height()

	many, scripts := map[string]any{}, []string{mineScript}
	for i := 0; i < 200; i++ {
		a, s := newScript(i)
		many[a] = "0.001"
		if i < 8 {
			scripts = append(scripts, s)
		}
	}
	for i := 0; i < 300; i++ {
		n.do("sendmany", "", many)
		n.do("generatetoaddress", 1, mine)
	}

	bin := t.TempDir()
	if err := os.Chmod(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	build := exec.Command("go", "test", "-c", "-o", filepath.Join(bin, "replay.test"), ".")
	build.Env = append(os.Environ(), "GOOS=linux", "GOARCH="+arch, "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building the linux test binary: %v\n%s", err, out)
	}
	name := n.name + "-replay"
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", "-v", name).Run() })
	mem, goMem := "1g", "off"
	if chain == nodeBitcoinCash {
		mem, goMem = "2g", "1600MiB"
	}
	out, err := exec.Command("docker", "run", "--rm", "--name", name, "--network", "container:"+n.name,
		"--cpus", "0.5", "--memory", mem, "-e", "GOMEMLIMIT="+goMem, "-v", bin+":/t:ro",
		"-e", "UTXO_REPLAY_IN_CONTAINER=1", "-e", "UTXO_REPLAY_CHAIN="+chain, "-e", "REPLAY_SMALL_TIP="+strconv.Itoa(small), "-e", "REPLAY_SCRIPTS="+strings.Join(scripts, ","),
		"--entrypoint", "/t/replay.test", image,
		"-test.run", "^TestUtxoReplayRateInContainer$", "-test.count", "3", "-test.v", "-test.timeout", "20m").CombinedOutput()
	t.Logf("replay container output:\n%s", out)
	if err != nil {
		t.Fatalf("replay inside the container failed: %v", err)
	}
}

func TestUtxoReplayRateInContainer(t *testing.T) {
	if os.Getenv("UTXO_REPLAY_IN_CONTAINER") != "1" {
		t.Skip("runs inside the capped container of TestUtxoReplayRegtestRate")
	}
	rpc := &rpcClient{url: "http://127.0.0.1:18443", user: "test", password: "test",
		client: &http.Client{Timeout: 2 * time.Minute}, maxBody: 64 << 20}
	n := &liveNode{t: t, rpc: rpc}
	small, err := strconv.Atoi(os.Getenv("REPLAY_SMALL_TIP"))
	if err != nil {
		t.Fatal(err)
	}
	tip := uint32(n.height())
	for _, replay := range []bool{false, true} {
		store := openFollowStore(t)
		if os.Getenv("UTXO_REPLAY_CHAIN") == nodeBitcoinCash {
			store = utxoTestOpenReverse(t, t.TempDir())
		}
		f := newUtxoFollower(store, rpc)
		apply, mode := f.applyHeights, "per block"
		if replay {
			f.chain, apply, mode = replayTestChain(), f.applyRange, "batched"
		}
		if os.Getenv("UTXO_REPLAY_CHAIN") == nodeBitcoinCash {
			f.chain = utxoBitcoinCash
		}
		for _, leg := range []struct {
			what     string
			from, to uint32
		}{{"coinbase-only blocks", 0, uint32(small)}, {"blocks with 200-output transactions", uint32(small) + 1, tip}} {
			start := time.Now()
			if err := apply(context.Background(), leg.from, leg.to); err != nil {
				t.Fatalf("%s %s: %v", mode, leg.what, err)
			}
			blocks := leg.to - leg.from + 1
			took := time.Since(start)
			t.Logf("%s, %d %s: %.1fs, %.0f blocks/s", mode, blocks, leg.what, took.Seconds(), float64(blocks)/took.Seconds())
		}
		checked := 0
		for _, script := range strings.Split(os.Getenv("REPLAY_SCRIPTS"), ",") {
			want, got := n.scan(script), lookupAmounts(t, store, script)
			if fmt.Sprint(got) != fmt.Sprint(want) {
				t.Fatalf("%s: script %s has %d coins in the index, %d in scantxoutset", mode, script, len(got), len(want))
			}
			checked += len(want)
		}
		t.Logf("%s replay matched scantxoutset on %d coins", mode, checked)
	}
}
