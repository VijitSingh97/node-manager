package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const benchTestXprv = "xprv9s21ZrQH143K3QTDL4LXw2F7HEK3wJUD2nW2nRk4stbPy6cq3jPPqjiChkVvvNKmPGJxWUtg6LnF5kejMRNNU3TGtRBeJgk33yuGBxrMPHi"

func benchTestID(n int) string { return fmt.Sprintf("%032x", n) }

func benchTestRequest(id string, keys []string, repeat int, scan bool) []byte {
	wallets := make([]benchWallet, len(keys))
	for i, key := range keys {
		wallets[i] = benchWallet{Name: fmt.Sprintf("w%d", i), Key: key}
	}
	raw, _ := json.Marshal(benchRequest{ID: id, RequestedAt: time.Now().Unix(), Wallets: wallets, Repeat: repeat, Scan: scan})
	return raw
}

func benchTestHost(dir string) utxoBenchHost {
	return utxoBenchHost{
		stateDir: dir,
		validate: func(context.Context, string) error { return nil },
		resolve:  indexTestScripts(indexTestScript),
		cachedRead: func(context.Context) (func(), int, error) {
			return func() {}, 3, nil
		},
		hold:    func() bool { return true },
		release: func() {},
	}
}

func benchTestPoll(t *testing.T, m *utxoManager, dir string, raw []byte) benchResult {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, benchRequestName), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	m.pollBench(context.Background())
	if _, err := os.Stat(filepath.Join(dir, benchRequestName)); err == nil {
		t.Fatal("request file was not consumed")
	}
	out, err := os.ReadFile(filepath.Join(dir, benchResultName))
	if err != nil {
		t.Fatal(err)
	}
	var res benchResult
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatal(err)
	}
	return res
}

func TestBenchRequestParsing(t *testing.T) {
	id := benchTestID(7)
	keys := func(n int) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = genesisAddress
		}
		return out
	}
	cases := []struct {
		name string
		raw  []byte
		want string
	}{
		{"valid", benchTestRequest(id, keys(3), 20, false), ""},
		{"max wallets", benchTestRequest(id, keys(20), 200, true), ""},
		{"private key", benchTestRequest(id, []string{genesisAddress, benchTestXprv}, 20, false), "wallet 2 is not a public watch key"},
		{"private descriptor", benchTestRequest(id, []string{"wpkh(" + benchTestXprv + "/0/*)"}, 20, false), "not a public watch key"},
		{"too many", benchTestRequest(id, keys(51), 20, false), "1 to 50 wallets"},
		{"none", benchTestRequest(id, nil, 20, false), "1 to 50 wallets"},
		{"repeat zero", benchTestRequest(id, keys(1), 0, false), "repeat must be"},
		{"repeat high", benchTestRequest(id, keys(1), 201, false), "repeat must be"},
		{"bad key", benchTestRequest(id, []string{"not a key"}, 20, false), "wallet 1 is not a public watch key"},
		{"no name", []byte(fmt.Sprintf(`{"id":%q,"wallets":[{"name":" ","key":%q}],"repeat":1}`, id, genesisAddress)), "has no name"},
		{"key not a string", []byte(fmt.Sprintf(`{"id":%q,"wallets":[{"name":"a","key":5}],"repeat":1}`, id)), "malformed"},
		{"not json", []byte("nope"), "malformed"},
		{"oversized", append(benchTestRequest(id, keys(1), 1, false), make([]byte, benchRequestMaxBytes)...), "malformed"},
	}
	for _, c := range cases {
		_, refusal := parseBenchRequest(c.raw)
		if c.want == "" && refusal != "" || c.want != "" && !strings.Contains(refusal, c.want) {
			t.Errorf("%s: refusal %q, want %q", c.name, refusal, c.want)
		}
		if strings.Contains(refusal, benchTestXprv) || strings.Contains(refusal, genesisAddress) {
			t.Errorf("%s: refusal leaked a key: %q", c.name, refusal)
		}
	}
}

func TestBenchSummaryPercentiles(t *testing.T) {
	samples := make([]float64, 100)
	for i := range samples {
		samples[i] = float64(100 - i)
	}
	got := benchSummary(samples)
	if got.First != 100 || got.P50 != 50 || got.P99 != 99 || got.Max != 100 {
		t.Fatalf("summary = %+v", got)
	}
	if one := benchSummary([]float64{2}); one != (benchTimes{2, 2, 2, 2}) {
		t.Fatalf("single sample summary = %+v", one)
	}
}

func TestBenchPollRefusals(t *testing.T) {
	dir := t.TempDir()
	m := utxoMgrNew(t, t.TempDir(), nil, newFakeUtxoNode(utxoMgrChain(10, "b")), &strings.Builder{})
	m.configureBench(benchTestHost(dir))
	id := benchTestID(1)

	res := benchTestPoll(t, m, dir, benchTestRequest(id, []string{genesisAddress, benchTestXprv}, 5, false))
	if res.ID != id || len(res.Refusals) != 1 || res.Wallets != nil || !strings.Contains(res.Refusals[0], "not a public watch key") {
		t.Fatalf("private key result = %+v", res)
	}
	res = benchTestPoll(t, m, dir, benchTestRequest(id, []string{genesisAddress}, 5, false))
	if len(res.Refusals) != 1 || !strings.Contains(res.Refusals[0], "not ready") || res.State != utxoStateNotSeeded || res.Wallets != nil {
		t.Fatalf("not ready result = %+v", res)
	}

	if err := os.WriteFile(filepath.Join(dir, benchRequestName), []byte("nope"), 0o600); err != nil {
		t.Fatal(err)
	}
	os.Remove(filepath.Join(dir, benchResultName))
	m.pollBench(context.Background())
	if _, err := os.Stat(filepath.Join(dir, benchResultName)); err == nil {
		t.Fatal("a request without an id produced a result")
	}
}

func TestBenchRefusesWhenBehindNode(t *testing.T) {
	c := indexTestChain(nil)
	m := indexTestManager(t, c)
	m.mu.Lock()
	m.nodeTip += 1
	m.mu.Unlock()
	dir := t.TempDir()
	m.configureBench(benchTestHost(dir))
	res := benchTestPoll(t, m, dir, benchTestRequest(benchTestID(2), []string{genesisAddress}, 5, false))
	if len(res.Refusals) != 1 || !strings.Contains(res.Refusals[0], "not level") {
		t.Fatalf("result = %+v", res)
	}
}

func TestBenchRefusesWalletTheNodeRejects(t *testing.T) {
	m := indexTestManager(t, indexTestChain(nil))
	dir := t.TempDir()
	host := benchTestHost(dir)
	host.validate = func(_ context.Context, key string) error {
		if key == publicExampleAddress {
			return fmt.Errorf("rejected")
		}
		return nil
	}
	m.configureBench(host)
	res := benchTestPoll(t, m, dir, benchTestRequest(benchTestID(3), []string{genesisAddress, publicExampleAddress}, 5, false))
	if len(res.Refusals) != 1 || !strings.Contains(res.Refusals[0], "wallet 2") || res.Wallets != nil {
		t.Fatalf("result = %+v", res)
	}
}

func TestBenchFullRunWithoutScan(t *testing.T) {
	m := indexTestManager(t, indexTestChain(nil))
	logs := &lockedLog{}
	m.log.SetOutput(logs)
	wantCoins, wantBTC := indexExpected(t, m, indexTestScript)
	dir := t.TempDir()
	host := benchTestHost(dir)
	var reads atomic.Int32
	host.cachedRead = func(context.Context) (func(), int, error) { return func() { reads.Add(1) }, 3, nil }
	host.scan = func(context.Context, []string) (benchScanRun, error) {
		t.Fatal("scan ran without scan=true")
		return benchScanRun{}, nil
	}
	const fallbackKey = "pkh(placeholder)"
	inner := host.resolve
	host.resolve = func(ctx context.Context, got []string) ([]walletScripts, error) {
		out, err := inner(ctx, got)
		if got[0] == fallbackKey {
			out[0] = walletScripts{Fallback: true}
		}
		return out, err
	}
	m.configureBench(host)
	keys := []string{genesisAddress, publicExampleAddress, fallbackKey, genesisAddress, publicExampleAddress, genesisAddress}

	id := benchTestID(4)
	res := benchTestPoll(t, m, dir, benchTestRequest(id, keys, 4, false))
	if len(res.Refusals) != 0 || res.ID != id || res.State != utxoStateReady || res.IndexTip == nil || res.NodeTip == nil || *res.NodeTip != int64(*res.IndexTip) {
		t.Fatalf("result = %+v", res)
	}
	if len(res.Wallets) != len(keys) {
		t.Fatalf("wallets = %d", len(res.Wallets))
	}
	for i, w := range res.Wallets {
		if i == 2 {
			if !w.Fallback || w.Lookup != nil || w.Coins != 0 {
				t.Errorf("fallback wallet = %+v", w)
			}
			continue
		}
		if w.Fallback || w.Lookup == nil || w.Coins != wantCoins || w.Scripts != 1 || w.Lookup.P50 <= 0 || w.Lookup.Max < w.Lookup.P99 || w.Lookup.P99 < w.Lookup.P50 {
			t.Errorf("wallet %d = %+v", i, w)
		}
		if w.Sats != int64(wantBTC*1e8+0.5) {
			t.Errorf("wallet %d sats = %d, want %v BTC", i, w.Sats, wantBTC)
		}
	}
	if len(res.Batches) != 3 || res.Batches[0].Wallets != 1 || res.Batches[1].Wallets != 5 || res.Batches[2].Wallets != len(keys) {
		t.Fatalf("batches = %+v", res.Batches)
	}
	for _, b := range res.Batches {
		if b.Scripts != 1 || b.Coins != wantCoins {
			t.Errorf("batch %+v: scripts are deduplicated across wallets", b)
		}
	}
	if b := res.Batches[1]; b.Expected != 4 || b.Fallback != 1 {
		t.Errorf("batch of 5 = %+v, want 4 expected scripts and 1 fallback wallet", b)
	}
	if res.CachedRead == nil || res.CachedRead.Roster != 3 || reads.Load() != 4 || res.Scan != nil {
		t.Fatalf("cached read %+v after %d reads, scan %+v", res.CachedRead, reads.Load(), res.Scan)
	}
	info, err := os.Stat(filepath.Join(dir, benchResultName))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("result file %v %v", info, err)
	}
	out := logs.String()
	if !strings.Contains(out, "utxo bench: start wallets=6 repeat=4 scan=false") || !strings.Contains(out, "utxo bench: end wallets=6 refusals=0 duration=") {
		t.Fatalf("log = %q", out)
	}
	for _, leak := range []string{genesisAddress, publicExampleAddress, "w0", formatBTC(wantBTC), indexTestScript} {
		if strings.Contains(out, leak) {
			t.Fatalf("log leaked %q: %q", leak, out)
		}
	}
}

func TestBenchBatchSizesFollowWalletCount(t *testing.T) {
	m := indexTestManager(t, indexTestChain(nil))
	dir := t.TempDir()
	m.configureBench(benchTestHost(dir))
	res := benchTestPoll(t, m, dir, benchTestRequest(benchTestID(5), []string{genesisAddress}, 2, false))
	if len(res.Batches) != 1 || res.Batches[0].Wallets != 1 {
		t.Fatalf("batches = %+v", res.Batches)
	}
}

func TestBenchScanSectionComparesAgainstIndex(t *testing.T) {
	m := indexTestManager(t, indexTestChain(nil))
	wantCoins, wantBTC := indexExpected(t, m, indexTestScript)
	sats := int64(wantBTC*1e8 + 0.5)
	dir := t.TempDir()
	host := benchTestHost(dir)
	var scans [][]string
	var holds, releases atomic.Int32
	host.hold = func() bool { holds.Add(1); return true }
	host.release = func() { releases.Add(1) }
	host.scan = func(_ context.Context, keys []string) (benchScanRun, error) {
		scans = append(scans, keys)
		run := benchScanRun{Seconds: 1.5, Height: int64(*m.Status().IndexTip)}
		for i := range keys {
			run.Wallets = append(run.Wallets, benchTotals{Coins: wantCoins + i, Sats: sats})
		}
		return run, nil
	}
	m.configureBench(host)
	res := benchTestPoll(t, m, dir, benchTestRequest(benchTestID(6), []string{genesisAddress, publicExampleAddress}, 2, true))
	if len(res.Refusals) != 0 || res.Scan == nil || len(scans) != 2 || len(scans[0]) != 2 || len(scans[1]) != 1 || scans[1][0] != genesisAddress {
		t.Fatalf("result %+v, scans %v", res, scans)
	}
	if holds.Load() != 1 || releases.Load() != 1 {
		t.Fatalf("hold/release = %d/%d", holds.Load(), releases.Load())
	}
	sw := res.Scan.Wallets
	if len(sw) != 2 || sw[0].Match == nil || !*sw[0].Match || sw[1].Match == nil || *sw[1].Match || res.Scan.BatchSeconds != 1.5 || res.Scan.SingleSeconds != 1.5 || res.Scan.HeightSkew {
		t.Fatalf("scan = %+v", res.Scan)
	}

	host.hold = func() bool { return true }
	host.scan = func(context.Context, []string) (benchScanRun, error) {
		return benchScanRun{Height: 1, Wallets: make([]benchTotals, 2)}, nil
	}
	m.configureBench(host)
	res = benchTestPoll(t, m, dir, benchTestRequest(benchTestID(12), []string{genesisAddress, publicExampleAddress}, 2, true))
	if res.Scan == nil || !res.Scan.HeightSkew || res.Scan.Wallets[0].Match != nil || res.Scan.Wallets[1].Match != nil {
		t.Fatalf("a scan at another height still compared: %+v", res.Scan)
	}

	host.hold = func() bool { return false }
	m.configureBench(host)
	res = benchTestPoll(t, m, dir, benchTestRequest(benchTestID(8), []string{genesisAddress}, 2, true))
	if res.Scan != nil || len(res.Refusals) != 1 || !strings.Contains(res.Refusals[0], "scan is running") || len(res.Wallets) != 1 {
		t.Fatalf("busy scanner result = %+v", res)
	}
}

func TestBenchRunsAgainstAppWithScan(t *testing.T) {
	c := indexTestChain(nil)
	m := indexTestManager(t, c)
	a, _, mock := indexTestApp(t, c, m)
	a.scanner.resolveScripts = indexTestScripts(indexTestScript)
	m.configureBench(a.benchHost())
	res := benchTestPoll(t, m, a.cfg.stateDir, benchTestRequest(benchTestID(9), []string{genesisAddress, publicExampleAddress}, 3, true))
	if len(res.Refusals) != 0 || res.Scan == nil || res.CachedRead == nil || len(res.Wallets) != 2 {
		t.Fatalf("result = %+v", res)
	}
	if scanCalls(mock) != 2 {
		t.Fatalf("scantxoutset starts = %d, want a batch and a single", scanCalls(mock))
	}
	if sw := res.Scan.Wallets; len(sw) != 2 || sw[0].Coins != 1 || sw[0].Sats != 125_000_000 || sw[1].Coins != 1 || res.Scan.Height != 900000 {
		t.Fatalf("scan = %+v", res.Scan)
	}
	if a.scanner.hold() == false {
		t.Fatal("the scanner stayed held after the bench")
	}
	a.scanner.release()
}

func TestBenchRefusesStaleRequest(t *testing.T) {
	m := indexTestManager(t, indexTestChain(nil))
	dir := t.TempDir()
	host := benchTestHost(dir)
	host.scan = func(context.Context, []string) (benchScanRun, error) {
		t.Fatal("a stale request ran a scan")
		return benchScanRun{}, nil
	}
	m.configureBench(host)
	for _, at := range []time.Time{time.Now().Add(-3 * time.Minute), time.Now().Add(3 * time.Minute), {}} {
		raw, _ := json.Marshal(benchRequest{ID: benchTestID(13), RequestedAt: at.Unix(), Wallets: []benchWallet{{Name: "a", Key: genesisAddress}}, Repeat: 2, Scan: true})
		res := benchTestPoll(t, m, dir, raw)
		if len(res.Refusals) != 1 || !strings.Contains(res.Refusals[0], "stale") || res.Wallets != nil {
			t.Fatalf("request at %v: %+v", at, res)
		}
	}
}

func TestBenchStopsWhenContextEnds(t *testing.T) {
	m := indexTestManager(t, indexTestChain(nil))
	m.configureBench(benchTestHost(t.TempDir()))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res := benchResult{}
	m.execBench(ctx, benchRequest{Wallets: []benchWallet{{Name: "a", Key: genesisAddress}}, Repeat: 5}, &res)
	if len(res.Refusals) != 1 || !strings.Contains(res.Refusals[0], "timed out") || res.Batches != nil {
		t.Fatalf("result = %+v", res)
	}
}

func TestBenchLoopPollsRequestFile(t *testing.T) {
	m := indexTestManager(t, indexTestChain(nil))
	dir := t.TempDir()
	m.configureBench(benchTestHost(dir))
	if err := os.WriteFile(filepath.Join(dir, benchRequestName), benchTestRequest(benchTestID(10), []string{genesisAddress}, 1, false), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go m.benchLoop(ctx)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(filepath.Join(dir, benchResultName)); err == nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("the loop never wrote a result")
}

func TestBenchWalletFileIsAcceptedByWatchValidation(t *testing.T) {
	raw, err := os.ReadFile("../../docs/measurements/bench-wallets.json")
	if err != nil {
		t.Fatal(err)
	}
	var wallets []benchWallet
	if err := json.Unmarshal(raw, &wallets); err != nil || len(wallets) < 6 {
		t.Fatalf("wallet file: %v, %d entries", err, len(wallets))
	}
	store := NewWatchStore(t.TempDir())
	for _, w := range wallets {
		if _, err := descriptorsFor(w.Key); err != nil || hasPrivateMaterial(w.Key) {
			t.Errorf("%s: %v", w.Name, err)
		}
		if w.Key != genesisAddress && w.Key != publicExampleAddress {
			descs, _ := descriptorsFor(w.Key)
			for _, d := range descs {
				if !utxoBitcoin.descriptorSupported(d.Desc) {
					t.Errorf("%s: the index does not support %q", w.Name, d.Desc)
				}
			}
		}
		if _, err := store.Add(w.Name, w.Key, ""); err != nil {
			t.Errorf("%s was refused by the watch store: %v", w.Name, err)
		}
	}
	if _, refusal := parseBenchRequest(benchTestRequest(benchTestID(11), keysOf(wallets), 20, true)); refusal != "" {
		t.Fatalf("request refused: %s", refusal)
	}
	if !strings.Contains(string(raw), genesisAddress) || !strings.Contains(string(raw), publicExampleAddress) {
		t.Fatal("the genesis and public example addresses are missing")
	}
}

func keysOf(wallets []benchWallet) []string {
	keys := make([]string, len(wallets))
	for i, w := range wallets {
		keys[i] = w.Key
	}
	return keys
}
