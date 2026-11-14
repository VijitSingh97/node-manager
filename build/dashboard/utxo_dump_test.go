package main

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

type dumpBuilder struct {
	w *bufio.Writer
}

func (b dumpBuilder) bytes(p ...byte) { b.w.Write(p) }

func (b dumpBuilder) varint(n uint64) {
	var tmp [10]byte
	i := len(tmp) - 1
	tmp[i] = byte(n & 0x7f)
	for n > 0x7f {
		n = n>>7 - 1
		i--
		tmp[i] = byte(n&0x7f) | 0x80
	}
	b.w.Write(tmp[i:])
}

func (b dumpBuilder) compactSize(n uint64) {
	switch {
	case n < 253:
		b.bytes(byte(n))
	case n <= 0xffff:
		b.bytes(253, byte(n), byte(n>>8))
	case n <= 0xffffffff:
		var t [4]byte
		binary.LittleEndian.PutUint32(t[:], uint32(n))
		b.bytes(254)
		b.bytes(t[:]...)
	default:
		var t [8]byte
		binary.LittleEndian.PutUint64(t[:], n)
		b.bytes(255)
		b.bytes(t[:]...)
	}
}

func (b dumpBuilder) header(version uint16, network [4]byte, count uint64) {
	b.bytes(utxoDumpMagic...)
	b.bytes(byte(version), byte(version>>8))
	b.bytes(network[:]...)
	b.bytes(make([]byte, 32)...)
	var c [8]byte
	binary.LittleEndian.PutUint64(c[:], count)
	b.bytes(c[:]...)
}

func compressUtxoAmountForTest(n uint64) uint64 {
	if n == 0 {
		return 0
	}
	e := uint64(0)
	for n%10 == 0 && e < 9 {
		n /= 10
		e++
	}
	if e < 9 {
		d := n % 10
		n /= 10
		return 1 + (n*9+d-1)*10 + e
	}
	return 1 + (n-1)*10 + 9
}

func (b dumpBuilder) coin(vout, height uint64, coinbase bool, amount uint64, script []byte) {
	b.compactSize(vout)
	code := height << 1
	if coinbase {
		code |= 1
	}
	b.varint(code)
	b.varint(compressUtxoAmountForTest(amount))
	b.varint(uint64(len(script)) + 6)
	b.bytes(script...)
}

func (b dumpBuilder) txid(seed uint64, coins uint64) {
	var id [32]byte
	binary.LittleEndian.PutUint64(id[:], seed)
	b.bytes(id[:]...)
	b.compactSize(coins)
}

func buildDump(f func(b dumpBuilder)) []byte {
	var buf bytes.Buffer
	b := dumpBuilder{bufio.NewWriter(&buf)}
	f(b)
	b.w.Flush()
	return buf.Bytes()
}

func parseAll(data []byte, network [4]byte) ([]utxoRecord, error) {
	var out []utxoRecord
	_, err := parseUtxoDump(bytes.NewReader(data), network, func(r utxoRecord) error {
		r.ScriptPubKey = append([]byte(nil), r.ScriptPubKey...)
		out = append(out, r)
		return nil
	})
	return out, err
}

func TestDecompressUtxoAmountVectors(t *testing.T) {
	for compressed, want := range map[uint64]uint64{
		0: 0, 1: 1, 7: 1000000, 9: 100000000, 0x32: 5000000000, 0x1406f40: 2100000000000000,
	} {
		if got := decompressUtxoAmount(compressed); got != want {
			t.Errorf("decompress(%#x) = %d, want %d", compressed, got, want)
		}
	}
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 20000; i++ {
		v := uint64(rng.Int63n(21_000_000 * 100_000_000))
		if i%3 == 0 {
			v -= v % 1000
		}
		if got := decompressUtxoAmount(compressUtxoAmountForTest(v)); got != v {
			t.Fatalf("round trip %d -> %d", v, got)
		}
	}
}

func TestDecompressUtxoPubkeyGenesisVector(t *testing.T) {
	x, _ := hex.DecodeString("678afdb0fe5548271967f1a67130b7105cd6a828e03909a67962e0ea1f61deb6")
	wantY := "49f6bc3f4cef38c4f35504e51ec112de5c384df7ba0b8d578a4c702b6bf11d5f"
	got, err := decompressUtxoPubkey(3, x)
	if err != nil {
		t.Fatal(err)
	}
	if hex.EncodeToString(got) != "04"+hex.EncodeToString(x)+wantY {
		t.Fatalf("odd tag: %x", got)
	}
	even, err := decompressUtxoPubkey(2, x)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(even[33:], got[33:]) || even[64]&1 != 0 {
		t.Fatalf("even tag did not flip y: %x", even)
	}
	bad := make([]byte, 32)
	bad[31] = 5
	if _, err := decompressUtxoPubkey(2, bad); err == nil {
		t.Fatal("x=5 is not on the curve but was accepted")
	}
}

func TestUtxoVarintAndCompactSize(t *testing.T) {
	for _, n := range []uint64{0, 1, 127, 128, 255, 256, 16383, 16384, 1 << 32, 1<<64 - 2} {
		data := buildDump(func(b dumpBuilder) { b.varint(n) })
		u := &utxoReader{r: bufio.NewReader(bytes.NewReader(data))}
		if got, err := u.varint(); err != nil || got != n {
			t.Errorf("varint %d: got %d, %v", n, got, err)
		}
	}
	for _, n := range []uint64{0, 252, 253, 0xffff, 0x10000, 0xffffffff, 0x100000000} {
		data := buildDump(func(b dumpBuilder) { b.compactSize(n) })
		u := &utxoReader{r: bufio.NewReader(bytes.NewReader(data))}
		if got, err := u.compactSize(); err != nil || got != n {
			t.Errorf("compactSize %d: got %d, %v", n, got, err)
		}
	}
	u := &utxoReader{r: bufio.NewReader(bytes.NewReader([]byte{253, 10, 0}))}
	if _, err := u.compactSize(); err == nil {
		t.Error("non-canonical CompactSize accepted")
	}
	u = &utxoReader{r: bufio.NewReader(bytes.NewReader(bytes.Repeat([]byte{0xff}, 12)))}
	if _, err := u.varint(); err == nil {
		t.Error("overflowing varint accepted")
	}
}

func TestParseUtxoDumpScriptTypes(t *testing.T) {
	genesisX, _ := hex.DecodeString("678afdb0fe5548271967f1a67130b7105cd6a828e03909a67962e0ea1f61deb6")
	h20 := bytes.Repeat([]byte{0xab}, 20)
	h32 := bytes.Repeat([]byte{0xcd}, 32)
	data := buildDump(func(b dumpBuilder) {
		b.header(2, utxoNetRegtest, 8)
		b.txid(1, 8)
		b.compactSize(0)
		b.varint(200<<1 | 1)
		b.varint(compressUtxoAmountForTest(5000000000))
		b.varint(0)
		b.bytes(h20...)
		b.compactSize(1)
		b.varint(300 << 1)
		b.varint(compressUtxoAmountForTest(1234))
		b.varint(1)
		b.bytes(h20...)
		b.compactSize(2)
		b.varint(3 << 1)
		b.varint(compressUtxoAmountForTest(1))
		b.varint(2)
		b.bytes(h32...)
		b.compactSize(3)
		b.varint(3 << 1)
		b.varint(compressUtxoAmountForTest(1))
		b.varint(3)
		b.bytes(h32...)
		b.compactSize(4)
		b.varint(4 << 1)
		b.varint(compressUtxoAmountForTest(1))
		b.varint(3 + 2)
		b.bytes(genesisX...)
		b.coin(5, 5, false, 99, append([]byte{0}, 20, 1, 2))
		b.coin(6, 6, false, 100, append([]byte{0x00, 0x14}, h20...))
		b.coin(7, 7, false, 0, nil)
	})
	got, err := parseAll(data, utxoNetRegtest)
	if err != nil {
		t.Fatal(err)
	}
	wantScripts := []string{
		"76a914" + hex.EncodeToString(h20) + "88ac",
		"a914" + hex.EncodeToString(h20) + "87",
		"21" + "02" + hex.EncodeToString(h32) + "ac",
		"21" + "03" + hex.EncodeToString(h32) + "ac",
		"41" + "04" + hex.EncodeToString(genesisX) + "49f6bc3f4cef38c4f35504e51ec112de5c384df7ba0b8d578a4c702b6bf11d5f" + "ac",
		"001401" + "02",
		"0014" + hex.EncodeToString(h20),
		"",
	}
	if len(got) != len(wantScripts) {
		t.Fatalf("got %d records", len(got))
	}
	for i, w := range wantScripts {
		if hex.EncodeToString(got[i].ScriptPubKey) != w {
			t.Errorf("record %d script %x, want %s", i, got[i].ScriptPubKey, w)
		}
		if got[i].Vout != uint64(i) {
			t.Errorf("record %d vout %d", i, got[i].Vout)
		}
	}
	if got[0].Height != 200 || !got[0].Coinbase || got[0].Amount != 5000000000 {
		t.Errorf("record 0 = %+v", got[0])
	}
	if got[1].Height != 300 || got[1].Coinbase || got[1].Amount != 1234 {
		t.Errorf("record 1 = %+v", got[1])
	}
	if got[0].TxidHex() != strings.Repeat("00", 31)+"01" {
		t.Errorf("txid hex %s", got[0].TxidHex())
	}
}

func TestParseUtxoDumpRejections(t *testing.T) {
	good := func(b dumpBuilder) {
		b.header(2, utxoNetRegtest, 2)
		b.txid(1, 2)
		b.coin(0, 1, true, 5000, []byte{0x51})
		b.coin(1, 1, true, 5000, []byte{0x51})
	}
	valid := buildDump(good)
	if recs, err := parseAll(valid, utxoNetRegtest); err != nil || len(recs) != 2 {
		t.Fatalf("baseline dump: %d records, %v", len(recs), err)
	}
	mutate := func(f func([]byte) []byte) []byte { return f(append([]byte(nil), valid...)) }
	cases := []struct {
		name string
		data []byte
		net  [4]byte
		want string
	}{
		{"magic", mutate(func(d []byte) []byte { d[0] = 'x'; return d }), utxoNetRegtest, "bad magic"},
		{"version", mutate(func(d []byte) []byte { d[5] = 3; return d }), utxoNetRegtest, "unsupported utxo dump version 3"},
		{"network", valid, utxoNetMainnet, "network magic"},
		{"empty", nil, utxoNetRegtest, "header"},
		{"short header", valid[:20], utxoNetRegtest, "header"},
		{"truncated body", valid[:len(valid)-1], utxoNetRegtest, "unexpected EOF"},
		{"truncated in txid", valid[:55], utxoNetRegtest, "unexpected EOF"},
		{"count too high", mutate(func(d []byte) []byte { d[43] = 3; return d }), utxoNetRegtest, "coin 3 of 3"},
		{"count too low", mutate(func(d []byte) []byte { d[43] = 1; return d }), utxoNetRegtest, "header says 1 coins"},
		{"trailing data", append(append([]byte(nil), valid...), 0), utxoNetRegtest, "more data"},
		{"zero group", buildDump(func(b dumpBuilder) { b.header(2, utxoNetRegtest, 1); b.txid(1, 0) }), utxoNetRegtest, "zero coins"},
		{"huge script", buildDump(func(b dumpBuilder) {
			b.header(2, utxoNetRegtest, 1)
			b.txid(1, 1)
			b.compactSize(0)
			b.varint(2)
			b.varint(1)
			b.varint(utxoMaxScriptSize + 7)
		}), utxoNetRegtest, "exceeds"},
	}
	for _, c := range cases {
		_, err := parseAll(c.data, c.net)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want containing %q", c.name, err, c.want)
		}
	}
	_, err := parseUtxoDump(bytes.NewReader(valid), utxoNetRegtest, func(utxoRecord) error { return errors.New("stop") })
	if err == nil || err.Error() != "stop" {
		t.Errorf("callback error not propagated: %v", err)
	}
}

type fixtureCoin struct {
	Txid     string `json:"txid"`
	Vout     uint64 `json:"vout"`
	Amount   uint64 `json:"amount"`
	Height   uint32 `json:"height"`
	Coinbase bool   `json:"coinbase"`
	Script   string `json:"script"`
}

type fixtureFile struct {
	BaseHash   string        `json:"base_hash"`
	BaseHeight int           `json:"base_height"`
	Coins      []fixtureCoin `json:"coins"`
}

func compareFixture(t *testing.T, datPath string) {
	t.Helper()
	raw, err := os.ReadFile(strings.TrimSuffix(datPath, ".dat") + ".json")
	if err != nil {
		t.Fatal(err)
	}
	var want fixtureFile
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(datPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var got []fixtureCoin
	h, err := parseUtxoDump(f, utxoNetRegtest, func(r utxoRecord) error {
		got = append(got, fixtureCoin{r.TxidHex(), r.Vout, r.Amount, r.Height, r.Coinbase, hex.EncodeToString(r.ScriptPubKey)})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Slice(got, func(i, j int) bool {
		if got[i].Txid != got[j].Txid {
			return got[i].Txid < got[j].Txid
		}
		return got[i].Vout < got[j].Vout
	})
	if len(got) != len(want.Coins) || h.CoinsCount != uint64(len(want.Coins)) {
		t.Fatalf("%s: parsed %d coins (header %d), expected %d", datPath, len(got), h.CoinsCount, len(want.Coins))
	}
	for i := range got {
		if got[i] != want.Coins[i] {
			t.Fatalf("%s: coin %d = %+v, expected %+v", datPath, i, got[i], want.Coins[i])
		}
	}
	var rev [32]byte
	for i, b := range h.BaseHash {
		rev[31-i] = b
	}
	if hex.EncodeToString(rev[:]) != want.BaseHash {
		t.Fatalf("%s: base hash %x, expected %s", datPath, rev, want.BaseHash)
	}
}

func TestParseUtxoRegtestFixtures(t *testing.T) {
	files, _ := filepath.Glob("testdata/utxo/*.dat")
	if len(files) == 0 {
		t.Fatal("no regtest fixtures in testdata/utxo")
	}
	for _, p := range files {
		t.Run(filepath.Base(p), func(t *testing.T) { compareFixture(t, p) })
	}
}

func TestAllTypesFixtureCoversEveryScriptType(t *testing.T) {
	raw, err := os.ReadFile("testdata/utxo/all_types.dat")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	_, err = parseUtxoDump(bytes.NewReader(raw), utxoNetRegtest, func(r utxoRecord) error {
		s := r.ScriptPubKey
		switch {
		case len(s) == 25 && s[0] == 0x76:
			seen["p2pkh"] = true
		case len(s) == 23 && s[0] == 0xa9:
			seen["p2sh"] = true
		case len(s) == 22 && s[0] == 0x00:
			seen["p2wpkh"] = true
		case len(s) == 34 && s[0] == 0x00:
			seen["p2wsh"] = true
		case len(s) == 34 && s[0] == 0x51:
			seen["p2tr"] = true
		case len(s) == 35 && s[0] == 33:
			seen["p2pk-compressed"] = true
		case len(s) == 67 && s[0] == 65:
			seen["p2pk-uncompressed"] = true
		case len(s) == 0 || s[0] == 0x51 && len(s) < 10:
			seen["raw"] = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"p2pkh", "p2sh", "p2wpkh", "p2wsh", "p2tr", "p2pk-compressed", "p2pk-uncompressed", "raw"} {
		if !seen[k] {
			t.Errorf("fixture has no %s output", k)
		}
	}
}

func TestParseUtxoRegtestLive(t *testing.T) {
	if os.Getenv("UTXO_REGTEST") != "1" {
		t.Skip("set UTXO_REGTEST=1 to generate fixtures from the pinned regtest bitcoind")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not installed")
	}
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skip("docker daemon not running")
	}
	dir := t.TempDir()
	if out, err := exec.Command("python3", "../../tests/utxo_regtest.py", dir).CombinedOutput(); err != nil {
		t.Fatalf("harness: %v\n%s", err, out)
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*.dat"))
	if len(files) != 6 {
		t.Fatalf("harness wrote %d dumps, want 6", len(files))
	}
	for _, p := range files {
		t.Run(filepath.Base(p), func(t *testing.T) { compareFixture(t, p) })
	}
}

func newSyntheticDump(coins int) io.Reader {
	pr, pw := io.Pipe()
	go func() {
		w := bufio.NewWriterSize(pw, 1<<16)
		b := dumpBuilder{w}
		b.header(2, utxoNetRegtest, uint64(coins))
		script := bytes.Repeat([]byte{7}, 34)
		for i := 0; i < coins; i += 2 {
			n := 2
			if coins-i < 2 {
				n = 1
			}
			b.txid(uint64(i), uint64(n))
			for v := 0; v < n; v++ {
				b.coin(uint64(v), uint64(i), v == 0, uint64(1000+i), script)
			}
		}
		w.Flush()
		pw.Close()
	}()
	return pr
}

type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

func peakHeap(t *testing.T, coins int) (uint64, int64) {
	t.Helper()
	var peak, seen uint64
	var ms runtime.MemStats
	in := &countingReader{r: newSyntheticDump(coins)}
	_, err := parseUtxoDump(in, utxoNetRegtest, func(utxoRecord) error {
		seen++
		if seen%100000 == 0 {
			runtime.GC()
			runtime.ReadMemStats(&ms)
			if ms.HeapAlloc > peak {
				peak = ms.HeapAlloc
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if seen != uint64(coins) {
		t.Fatalf("saw %d coins, want %d", seen, coins)
	}
	return peak, in.n
}

func TestParseUtxoDumpMemoryIsBounded(t *testing.T) {
	if testing.Short() {
		t.Skip("large generated input")
	}
	small, _ := peakHeap(t, 200000)
	large, size := peakHeap(t, 2000000)
	t.Logf("peak heap: 200k coins %d bytes, 2M coins %d bytes (2M-coin input is %d bytes)", small, large, size)
	if large > small+4<<20 {
		t.Fatalf("heap grew with input size: %d -> %d bytes", small, large)
	}
}
