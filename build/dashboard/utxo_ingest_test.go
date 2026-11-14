package main

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"testing"
	"time"
)

type scanCoin struct {
	Txid     string `json:"txid"`
	Vout     uint64 `json:"vout"`
	Amount   uint64 `json:"amount"`
	Height   uint32 `json:"height"`
	Coinbase bool   `json:"coinbase"`
}

type scanFixture struct {
	fixtureFile
	Scan map[string][]scanCoin `json:"scan"`
}

func utxoTestIngestFile(t testing.TB, s *utxoStore, datPath string, height uint32) (utxoHeader, *utxoIngestProgress) {
	t.Helper()
	f, err := os.Open(datPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	prog := &utxoIngestProgress{}
	h, err := ingestUtxoSnapshot(s, f, utxoNetRegtest, height, prog)
	if err != nil {
		t.Fatalf("ingest %s: %v", datPath, err)
	}
	return h, prog
}

func utxoTestLoadFixture(t testing.TB, datPath string) scanFixture {
	t.Helper()
	raw, err := os.ReadFile(datPath[:len(datPath)-len(".dat")] + ".json")
	if err != nil {
		t.Fatal(err)
	}
	var want scanFixture
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatal(err)
	}
	return want
}

func utxoTestCoinString(c utxoCoin) string {
	b, _ := json.Marshal(scanCoin{Txid: utxoRecord{Txid: c.Txid}.TxidHex(), Vout: uint64(c.Vout), Amount: c.Amount, Height: c.Height})
	return string(b)
}

func utxoTestAssertScript(t testing.TB, s *utxoStore, script string, want []scanCoin) {
	t.Helper()
	raw, err := hex.DecodeString(script)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Lookup([][]byte{raw})
	if err != nil {
		t.Fatal(err)
	}
	var gs, ws []string
	for _, c := range got {
		gs = append(gs, utxoTestCoinString(c))
	}
	for _, c := range want {
		c.Coinbase = false
		b, _ := json.Marshal(c)
		ws = append(ws, string(b))
	}
	sort.Strings(gs)
	sort.Strings(ws)
	if len(gs) != len(ws) {
		t.Fatalf("script %s: %d coins, want %d", script, len(gs), len(ws))
	}
	for i := range gs {
		if gs[i] != ws[i] {
			t.Fatalf("script %s: coin %s, want %s", script, gs[i], ws[i])
		}
	}
}

func TestUtxoIngestFixtures(t *testing.T) {
	files, _ := filepath.Glob("testdata/utxo/*.dat")
	if len(files) == 0 {
		t.Fatal("no regtest fixtures in testdata/utxo")
	}
	for _, p := range files {
		t.Run(filepath.Base(p), func(t *testing.T) {
			want := utxoTestLoadFixture(t, p)
			dir := t.TempDir()
			s := utxoTestOpen(t, dir)
			h, prog := utxoTestIngestFile(t, s, p, uint32(want.BaseHeight))
			byScript := map[string][]scanCoin{}
			for _, c := range want.Coins {
				byScript[c.Script] = append(byScript[c.Script], scanCoin{Txid: c.Txid, Vout: c.Vout, Amount: c.Amount, Height: c.Height})
			}
			for script, coins := range byScript {
				utxoTestAssertScript(t, s, script, coins)
			}
			height, hash, ok, err := s.Tip()
			if err != nil || !ok || height != uint32(want.BaseHeight) || hash != h.BaseHash {
				t.Fatalf("tip = %d %x %v %v", height, hash, ok, err)
			}
			if prog.Total.Load() != h.CoinsCount || prog.Written.Load() != h.CoinsCount || prog.Fraction() != 1 {
				t.Fatalf("progress %d/%d (parsed %d)", prog.Written.Load(), prog.Total.Load(), prog.Parsed.Load())
			}
			if _, err := os.Stat(filepath.Join(dir, utxoIngestTmpDir)); !os.IsNotExist(err) {
				t.Fatalf("temp dir left behind: %v", err)
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			s = utxoTestOpen(t, dir)
			utxoTestAssertScript(t, s, want.Coins[0].Script, byScript[want.Coins[0].Script])
			if _, _, ok, _ := s.Tip(); !ok {
				t.Fatal("tip lost on reopen")
			}
		})
	}
}

func TestUtxoIngestThenApplyBlock(t *testing.T) {
	s := utxoTestOpen(t, t.TempDir())
	utxoTestIngestFile(t, s, "testdata/utxo/same_block.dat", 7)
	_, hash, _, _ := s.Tip()
	blk := utxoTestBlock(8)
	blk.PrevHash = hash
	blk.Spends = nil
	if err := s.ApplyBlock(blk); err != nil {
		t.Fatal(err)
	}
	if err := s.DisconnectTip(); err != nil {
		t.Fatal(err)
	}
	if err := s.DisconnectTip(); !errors.Is(err, errUtxoNoUndo) {
		t.Fatalf("snapshot base must have no undo record, got %v", err)
	}
}

func TestUtxoIngestRefusesNonEmptyStore(t *testing.T) {
	s := utxoTestOpen(t, t.TempDir())
	utxoTestIngestFile(t, s, "testdata/utxo/all_types.dat", 103)
	f, _ := os.Open("testdata/utxo/all_types.dat")
	defer f.Close()
	if _, err := ingestUtxoSnapshot(s, f, utxoNetRegtest, 103, nil); !errors.Is(err, errUtxoNotEmpty) {
		t.Fatalf("second ingest = %v, want errUtxoNotEmpty", err)
	}

	s2 := utxoTestOpen(t, t.TempDir())
	utxoTestApply(t, s2, 1, 3)
	f2, _ := os.Open("testdata/utxo/all_types.dat")
	defer f2.Close()
	if _, err := ingestUtxoSnapshot(s2, f2, utxoNetRegtest, 103, nil); !errors.Is(err, errUtxoNotEmpty) {
		t.Fatalf("ingest over applied blocks = %v, want errUtxoNotEmpty", err)
	}
	utxoTestExpectTip(t, s2, 3)
}

func TestUtxoIngestDiscardKeepsTheIndexDirectory(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "utxo-index")
	s := utxoTestOpen(t, dir)
	s.beforeIngest = func() error { return errors.New("simulated crash before ready") }
	f, _ := os.Open("testdata/utxo/all_types.dat")
	defer f.Close()
	if _, err := ingestUtxoSnapshot(s, f, utxoNetRegtest, 103, nil); err == nil {
		t.Fatal("expected the interrupted ingest to fail")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(parent, 0o555); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(parent, 0o755)
	s = utxoTestOpen(t, dir)
	if !s.Valid() {
		t.Fatalf("reopen inside an undeletable directory failed: %s", s.InvalidReason())
	}
	if err := s.RequireEmpty(); err != nil {
		t.Fatalf("discarded store not empty: %v", err)
	}
}

func TestUtxoIngestInterruptedIsDiscardedAndNeverServed(t *testing.T) {
	dir := t.TempDir()
	s := utxoTestOpen(t, dir)
	s.beforeIngest = func() error { return errors.New("simulated crash before ready") }
	f, _ := os.Open("testdata/utxo/all_types.dat")
	defer f.Close()
	if _, err := ingestUtxoSnapshot(s, f, utxoNetRegtest, 103, nil); err == nil {
		t.Fatal("expected the interrupted ingest to fail")
	}
	if s.Valid() {
		t.Fatal("half-built index reports valid")
	}
	if _, err := s.Lookup([][]byte{{0x51}}); !errors.Is(err, errUtxoInvalid) {
		t.Fatalf("lookup on half-built index = %v", err)
	}
	if _, _, _, err := s.Tip(); !errors.Is(err, errUtxoInvalid) {
		t.Fatalf("tip on half-built index = %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s = utxoTestOpen(t, dir)
	if !s.Valid() {
		t.Fatalf("reopen after interruption invalid: %s", s.InvalidReason())
	}
	if _, _, ok, err := s.Tip(); err != nil || ok {
		t.Fatalf("tip after discard = %v %v", ok, err)
	}
	if err := s.RequireEmpty(); err != nil {
		t.Fatalf("discarded store not empty: %v", err)
	}
	want := utxoTestLoadFixture(t, "testdata/utxo/all_types.dat")
	utxoTestIngestFile(t, s, "testdata/utxo/all_types.dat", uint32(want.BaseHeight))
	var same []scanCoin
	for _, c := range want.Coins {
		if c.Script == want.Coins[0].Script {
			same = append(same, scanCoin{Txid: c.Txid, Vout: c.Vout, Amount: c.Amount, Height: c.Height})
		}
	}
	utxoTestAssertScript(t, s, want.Coins[0].Script, same)
}

func TestUtxoIngestCrashedBeforeMarkLeavesTempThatReopenClears(t *testing.T) {
	dir := t.TempDir()
	s := utxoTestOpen(t, dir)
	tmp := filepath.Join(dir, utxoIngestTmpDir)
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "run-000000"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	s = utxoTestOpen(t, dir)
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Fatalf("stale temp dir survived reopen: %v", err)
	}
	if !s.Valid() {
		t.Fatal("store invalid")
	}
}

func TestUtxoIngestSkipsUnspendableAndMatchesStoreKeys(t *testing.T) {
	data := buildDump(func(b dumpBuilder) {
		b.header(2, utxoNetRegtest, 4)
		b.txid(1, 4)
		b.coin(0, 5, false, 1000, []byte{0x6a, 0x01, 0x02})
		b.coin(1, 5, false, 2000, []byte{0x51})
		b.coin(2, 5, true, 3000, []byte{})
		b.coin(3, 6, false, 4000, append([]byte{0x00, 0x14}, make([]byte, 20)...))
	})
	s := utxoTestOpen(t, t.TempDir())
	prog := &utxoIngestProgress{}
	h, err := ingestUtxoSnapshot(s, bytes.NewReader(data), utxoNetRegtest, 9, prog)
	if err != nil {
		t.Fatal(err)
	}
	if prog.Written.Load() != 4 || prog.Total.Load() != h.CoinsCount {
		t.Fatalf("progress %d/%d", prog.Written.Load(), prog.Total.Load())
	}
	got, err := s.Lookup([][]byte{{0x6a, 0x01, 0x02}, {0x51}, {}, append([]byte{0x00, 0x14}, make([]byte, 20)...)})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("coins = %d, want 3 (OP_RETURN skipped)", len(got))
	}
	var id [32]byte
	binary.LittleEndian.PutUint64(id[:], 1)
	one, _ := s.Lookup([][]byte{{0x51}})
	if len(one) != 1 || one[0].Txid != id || one[0].Vout != 1 || one[0].Amount != 2000 || one[0].Height != 5 {
		t.Fatalf("coin = %+v", one)
	}
}

func TestUtxoIngestRejectsBadInputWithoutChange(t *testing.T) {
	dup := buildDump(func(b dumpBuilder) {
		b.header(2, utxoNetRegtest, 2)
		b.txid(1, 1)
		b.coin(0, 5, false, 1000, []byte{0x51})
		b.txid(1, 1)
		b.coin(0, 5, false, 1000, []byte{0x51})
	})
	bigVout := buildDump(func(b dumpBuilder) {
		b.header(2, utxoNetRegtest, 1)
		b.txid(1, 1)
		b.coin(1<<32, 5, false, 1000, []byte{0x51})
	})
	truncated := buildDump(func(b dumpBuilder) {
		b.header(2, utxoNetRegtest, 3)
		b.txid(1, 3)
		b.coin(0, 5, false, 1000, []byte{0x51})
	})
	for name, data := range map[string][]byte{"duplicate": dup, "vout": bigVout, "truncated": truncated} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			s := utxoTestOpen(t, dir)
			if _, err := ingestUtxoSnapshot(s, bytes.NewReader(data), utxoNetRegtest, 1, nil); err == nil {
				t.Fatal("expected an error")
			}
			if !s.Valid() {
				t.Fatalf("store invalid after rejected input: %s", s.InvalidReason())
			}
			if err := s.RequireEmpty(); err != nil {
				t.Fatalf("store changed: %v", err)
			}
			if _, err := os.Stat(filepath.Join(dir, utxoIngestTmpDir)); !os.IsNotExist(err) {
				t.Fatalf("temp dir left behind: %v", err)
			}
		})
	}
}

func utxoTestSmallRuns(t *testing.T, runRecords, sstBytes uint64) {
	oldRun, oldSST := utxoIngestRunRecords, utxoIngestSSTBytes
	utxoIngestRunRecords, utxoIngestSSTBytes = runRecords, sstBytes
	t.Cleanup(func() { utxoIngestRunRecords, utxoIngestSSTBytes = oldRun, oldSST })
}

func utxoTestSplitmix(x *uint64) uint64 {
	*x += 0x9e3779b97f4a7c15
	z := *x
	z = (z ^ z>>30) * 0xbf58476d1ce4e5b9
	z = (z ^ z>>27) * 0x94d049bb133111eb
	return z ^ z>>31
}

func utxoTestScriptFor(i uint64) []byte {
	seed := i
	s := []byte{0x00, 0x14}
	for len(s) < 22 {
		s = binary.BigEndian.AppendUint64(s, utxoTestSplitmix(&seed))
	}
	return s[:22]
}

func utxoTestRandomDump(coins int) io.Reader {
	pr, pw := io.Pipe()
	go func() {
		w := bufio.NewWriterSize(pw, 1<<20)
		b := dumpBuilder{w}
		b.header(2, utxoNetRegtest, uint64(coins))
		for i := 0; i < coins; i += 2 {
			n := 2
			if coins-i < 2 {
				n = 1
			}
			seed := uint64(i) + 1<<40
			var id []byte
			for len(id) < 32 {
				id = binary.BigEndian.AppendUint64(id, utxoTestSplitmix(&seed))
			}
			b.bytes(id...)
			b.compactSize(uint64(n))
			for v := 0; v < n; v++ {
				b.coin(uint64(v), utxoTestSplitmix(&seed)%900000, v == 0, 1000+utxoTestSplitmix(&seed)%10_000_000_000, utxoTestScriptFor(uint64(i+v)))
			}
		}
		w.Flush()
		pw.Close()
	}()
	return pr
}

func TestUtxoIngestMultiRunMultiSST(t *testing.T) {
	utxoTestSmallRuns(t, 1000, 20<<10)
	const coins = 25000
	dir := t.TempDir()
	s := utxoTestOpen(t, dir)
	var maxSSTs int
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			m, _ := filepath.Glob(filepath.Join(dir, utxoIngestTmpDir, "*.sst"))
			maxSSTs = max(maxSSTs, len(m))
			select {
			case <-stop:
				return
			case <-time.After(time.Millisecond):
			}
		}
	}()
	prog := &utxoIngestProgress{}
	_, err := ingestUtxoSnapshot(s, utxoTestRandomDump(coins), utxoNetRegtest, 50, prog)
	close(stop)
	wg.Wait()
	if err != nil {
		t.Fatal(err)
	}
	if prog.Written.Load() != coins || prog.Parsed.Load() != coins {
		t.Fatalf("progress parsed %d written %d", prog.Parsed.Load(), prog.Written.Load())
	}
	if maxSSTs < 2 {
		t.Logf("saw at most %d SST in the temp dir while polling", maxSSTs)
	}
	for i := uint64(0); i < coins; i += 997 {
		got, err := s.Lookup([][]byte{utxoTestScriptFor(i)})
		if err != nil || len(got) != 1 || got[0].Amount < 1000 {
			t.Fatalf("script %d: %v %+v", i, err, got)
		}
	}
	it, err := s.db.NewIter(nil)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for ok := it.First(); ok; ok = it.Next() {
		if len(it.Key()) == utxoKeyLen {
			n++
		}
	}
	_ = it.Close()
	if n != coins {
		t.Fatalf("store holds %d coins, want %d", n, coins)
	}
}

func TestUtxoIngestRegtestLive(t *testing.T) {
	if os.Getenv("UTXO_REGTEST") != "1" {
		t.Skip("set UTXO_REGTEST=1 to generate a dump from the pinned regtest bitcoind")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not installed")
	}
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skip("docker daemon not running")
	}
	out := t.TempDir()
	if o, err := exec.Command("python3", "../../tests/utxo_regtest.py", out, "--scan").CombinedOutput(); err != nil {
		t.Fatalf("harness: %v\n%s", err, o)
	}
	files, _ := filepath.Glob(filepath.Join(out, "*.dat"))
	if len(files) != 6 {
		t.Fatalf("harness wrote %d dumps, want 6", len(files))
	}
	for _, p := range files {
		t.Run(filepath.Base(p), func(t *testing.T) {
			want := utxoTestLoadFixture(t, p)
			if len(want.Scan) == 0 {
				t.Fatal("fixture has no scantxoutset results")
			}
			s := utxoTestOpen(t, t.TempDir())
			utxoTestIngestFile(t, s, p, uint32(want.BaseHeight))
			for script, coins := range want.Scan {
				utxoTestAssertScript(t, s, script, coins)
			}
		})
	}
}

func TestUtxoIngestLargeMeasure(t *testing.T) {
	raw := os.Getenv("UTXO_INGEST_BENCH")
	if raw == "" {
		t.Skip("set UTXO_INGEST_BENCH=<coins> to measure a large synthetic ingest")
	}
	coins, err := strconv.Atoi(raw)
	if err != nil || coins <= 0 {
		t.Fatalf("bad UTXO_INGEST_BENCH %q", raw)
	}
	dir := t.TempDir()
	if base := os.Getenv("UTXO_INGEST_BENCH_DIR"); base != "" {
		dir = filepath.Join(base, "index")
		if err := os.RemoveAll(dir); err != nil {
			t.Fatal(err)
		}
	}
	s := utxoTestOpen(t, dir)
	var peakTmp, peakAll int64
	var samples int
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			tmp := utxoTestDirSize(filepath.Join(dir, utxoIngestTmpDir))
			all := utxoTestDirSize(dir)
			peakTmp, peakAll, samples = max(peakTmp, tmp), max(peakAll, all), samples+1
			select {
			case <-stop:
				return
			case <-time.After(500 * time.Millisecond):
			}
		}
	}()
	prog := &utxoIngestProgress{}
	start := time.Now()
	_, err = ingestUtxoSnapshot(s, utxoTestRandomDump(coins), utxoNetRegtest, 1, prog)
	elapsed := time.Since(start)
	close(stop)
	<-done
	if err != nil {
		t.Fatal(err)
	}
	_, statErr := os.Stat(filepath.Join(dir, utxoIngestTmpDir))
	final := utxoTestDirSize(dir)
	t.Logf("coins=%d wall=%s peak_temp=%d B (%.2f GiB, %.1f B/coin) peak_index_dir_total=%d B (%.2f GiB, %.1f B/coin) final_index=%d B (%.1f B/coin) temp_removed=%v samples=%d written=%d",
		coins, elapsed.Round(time.Second), peakTmp, float64(peakTmp)/(1<<30), float64(peakTmp)/float64(coins),
		peakAll, float64(peakAll)/(1<<30), float64(peakAll)/float64(coins), final, float64(final)/float64(coins),
		os.IsNotExist(statErr), samples, prog.Written.Load())
	got, err := s.Lookup([][]byte{utxoTestScriptFor(uint64(coins / 2))})
	if err != nil || len(got) != 1 {
		t.Fatalf("spot lookup: %v %+v", err, got)
	}
}

func utxoTestDirSize(dir string) int64 {
	var n int64
	_ = filepath.Walk(dir, func(_ string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			n += info.Size()
		}
		return nil
	})
	return n
}
