package main

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/metrics"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cockroachdb/pebble/vfs"
)

// TestUtxoBitcoinCashLargeBlockReplay builds near-32 MB blocks, the size of Bitcoin Cash's stress-test blocks, and replays them under the dashboard's caps.
func TestUtxoBitcoinCashLargeBlockReplay(t *testing.T) {
	if os.Getenv("UTXO_REPLAY_IN_CONTAINER") == "1" {
		t.Skip("running inside the capped replay container")
	}
	if os.Getenv("UTXO_BCH_LARGE") != "1" {
		t.Skip("set UTXO_BCH_LARGE=1 and BCH_REGTEST_IMAGE to build and replay near-32 MB Bitcoin Cash blocks")
	}
	image := os.Getenv("BCH_REGTEST_IMAGE")
	if image == "" {
		t.Skip("set BCH_REGTEST_IMAGE")
	}
	blocks, _ := strconv.Atoi(os.Getenv("UTXO_BCH_LARGE_BLOCKS"))
	if blocks <= 0 {
		blocks = 4
	}
	data := os.Getenv("UTXO_BCH_LARGE_DATA")
	if data == "" {
		data = t.TempDir()
	}
	saved, _ := os.ReadFile(filepath.Join(data, "scripts.txt"))
	n := runLiveNode(t, "18443", "-v", data+":/data", "--entrypoint", "bitcoind", image, "-regtest", "-datadir=/data", "-fallbackfee=0.00001",
		"-blockmaxsize=32000000", "-maxmempool=1000", "-printtoconsole=0", "-rpcuser=test", "-rpcpassword=test", "-rpcbind=0.0.0.0", "-rpcallowip=0.0.0.0/0")
	n.rpc.client.Timeout = 10 * time.Minute
	scripts := strings.Fields(string(saved))
	first := 111
	if len(scripts) == 0 {
		n.do("createwallet", "w")
		n.do("createwallet", "sink")
		w, sink := n.wallet("w"), n.wallet("sink")
		mine := w.str("getnewaddress")
		n.do("generatetoaddress", 110, mine)
		outputs := map[string]any{}
		for i := range 1000 {
			a := sink.str("getnewaddress")
			outputs[a] = "0.00001000"
			if i < 4 {
				var info struct {
					ScriptPubKey string `json:"scriptPubKey"`
				}
				_ = json.Unmarshal(n.do("validateaddress", a), &info)
				scripts = append(scripts, info.ScriptPubKey)
			}
		}
		_ = n.rpc.call(context.Background(), "unloadwallet", []any{"sink"}, new(json.RawMessage))
		for b := range blocks {
			// 908 transactions of 1,000 outputs make a 31 MB block.
			for range 908 {
				w.str("sendmany", "", outputs)
			}
			n.do("generatetoaddress", 1, mine)
			t.Logf("built large block %d of %d at height %d", b+1, blocks, n.height())
		}
		if err := os.WriteFile(filepath.Join(data, "scripts.txt"), []byte(strings.Join(scripts, "\n")), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for h := first; h <= n.height(); h++ {
		hash := n.str("getblockhash", h)
		var hdr struct {
			Size int `json:"size"`
			NTx  int `json:"nTx"`
		}
		_ = json.Unmarshal(n.do("getblock", hash, 1), &hdr)
		rawBytes := rpcResponseBytes(t, n, "getblock", []any{hash, 0})
		t.Logf("height %d: %d transactions, %.1f MB block, %.1f MB of getblock verbosity 0", h, hdr.NTx, float64(hdr.Size)/1e6, float64(rawBytes)/1e6)
	}

	arch := seedLiveDocker(t, "version", "--format", "{{.Server.Arch}}")
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
	stop := make(chan struct{})
	nodePeak := make(chan float64, 1)
	go func() {
		peak := 0.0
		for {
			select {
			case <-stop:
				nodePeak <- peak
				return
			case <-time.After(time.Second):
			}
			out, _ := exec.Command("docker", "stats", "--no-stream", "--format", "{{.MemUsage}}", n.name).Output()
			if mib, ok := parseDockerMiB(string(out)); ok && mib > peak {
				peak = mib
			}
		}
	}()
	out, err := exec.Command("docker", "run", "--rm", "--name", name, "--network", "container:"+n.name,
		"--cpus", "0.5", "--memory", cmp.Or(os.Getenv("UTXO_BCH_LARGE_MEM"), "1g"), "--memory-swap", cmp.Or(os.Getenv("UTXO_BCH_LARGE_MEM"), "1g"), "-v", bin+":/t:ro",
		"-e", "UTXO_REPLAY_IN_CONTAINER=1", "-e", "REPLAY_SCRIPTS="+strings.Join(scripts, ","), "-e", "GOMEMLIMIT="+cmp.Or(os.Getenv("UTXO_BCH_LARGE_GOMEMLIMIT"), "off"),
		"--entrypoint", "/t/replay.test", image,
		"-test.run", "^TestUtxoBitcoinCashLargeBlockReplayInContainer$", "-test.v", "-test.timeout", "120m").CombinedOutput()
	close(stop)
	t.Logf("Bitcoin Cash Node memory peaked at about %.0f MiB during the replay (sampled every second)", <-nodePeak)
	t.Logf("replay container output:\n%s", out)
	if err != nil {
		t.Fatalf("replay inside the 1 GB container failed: %v", err)
	}
}

func rpcResponseBytes(t *testing.T, n *liveNode, method string, params []any) int64 {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"jsonrpc": "1.0", "id": "size", "method": method, "params": params})
	req, err := http.NewRequest(http.MethodPost, n.rpc.url, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.SetBasicAuth(n.rpc.user, n.rpc.password)
	res, err := n.rpc.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	size, err := io.Copy(io.Discard, res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return size
}

func parseDockerMiB(s string) (float64, bool) {
	fields := strings.Fields(strings.Split(s, "/")[0])
	if len(fields) == 0 {
		return 0, false
	}
	v := strings.TrimSpace(fields[0])
	for unit, scale := range map[string]float64{"GiB": 1024, "MiB": 1, "KiB": 1.0 / 1024} {
		if strings.HasSuffix(v, unit) {
			n, err := strconv.ParseFloat(strings.TrimSuffix(v, unit), 64)
			return n * scale, err == nil
		}
	}
	return 0, false
}

func peakRSS(t *testing.T) string {
	f, err := os.Open("/proc/self/status")
	if err != nil {
		return "unknown"
	}
	defer f.Close()
	for s := bufio.NewScanner(f); s.Scan(); {
		if strings.HasPrefix(s.Text(), "VmHWM:") {
			return strings.TrimSpace(strings.TrimPrefix(s.Text(), "VmHWM:"))
		}
	}
	return "unknown"
}

func TestUtxoBitcoinCashLargeBlockReplayInContainer(t *testing.T) {
	if os.Getenv("UTXO_REPLAY_IN_CONTAINER") != "1" || os.Getenv("REPLAY_SCRIPTS") == "" {
		t.Skip("runs inside the capped container of TestUtxoBitcoinCashLargeBlockReplay")
	}
	rpc := &rpcClient{url: "http://127.0.0.1:18443", user: "test", password: "test",
		client: &http.Client{Timeout: 10 * time.Minute}, maxBody: 1 << 30}
	n := &liveNode{t: t, rpc: rpc}
	tip := uint32(n.height())
	dir := t.TempDir()
	store, err := openUtxoStoreFS(dir, vfs.Default, utxoBitcoinCash.ReverseIndex)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	f := newUtxoFollower(store, rpc)
	f.chain = utxoBitcoinCash
	start := time.Now()
	sample := []metrics.Sample{{Name: "/gc/heap/live:bytes"}}
	var liveMax atomic.Uint64
	sampling := make(chan struct{})
	go func() {
		for {
			select {
			case <-sampling:
				return
			case <-time.After(200 * time.Millisecond):
			}
			metrics.Read(sample)
			if v := sample[0].Value.Uint64(); v > liveMax.Load() {
				liveMax.Store(v)
			}
		}
	}()
	err = f.applyRange(context.Background(), 0, tip)
	close(sampling)
	if err != nil {
		t.Fatalf("replay: %v (peak RSS %s)", err, peakRSS(t))
	}
	t.Logf("replayed %d blocks in %.1fs; peak RSS %s; largest live heap after a GC %.0f MiB", tip+1, time.Since(start).Seconds(), peakRSS(t), float64(liveMax.Load())/(1<<20))
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	var onDisk int64
	_ = filepath.Walk(dir, func(_ string, fi os.FileInfo, err error) error {
		if err == nil && !fi.IsDir() {
			onDisk += fi.Size()
		}
		return nil
	})
	var set struct {
		Txouts int64 `json:"txouts"`
	}
	_ = json.Unmarshal(n.do("gettxoutsetinfo"), &set)
	t.Logf("index directory %.1f MB for %d coins: %.1f bytes per coin with the reverse key", float64(onDisk)/1e6, set.Txouts, float64(onDisk)/float64(max(set.Txouts, 1)))
	store, err = openUtxoStoreFS(dir, vfs.Default, utxoBitcoinCash.ReverseIndex)
	if err != nil {
		t.Fatal(err)
	}
	for _, script := range strings.Split(os.Getenv("REPLAY_SCRIPTS"), ",") {
		want, got := n.scan(script), lookupAmounts(t, store, script)
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Fatalf("script %s: index %d coins, scantxoutset %d", script, len(got), len(want))
		}
	}
	t.Logf("index matched scantxoutset on the sampled sink scripts")
}
