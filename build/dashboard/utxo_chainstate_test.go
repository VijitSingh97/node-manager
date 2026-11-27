package main

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"runtime/metrics"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cockroachdb/pebble"
	"github.com/cockroachdb/pebble/record"
	"github.com/cockroachdb/pebble/vfs"
)

func appendCoreVarInt(b []byte, n uint64) []byte {
	var tmp [10]byte
	i := len(tmp) - 1
	tmp[i] = byte(n & 0x7f)
	for n > 0x7f {
		n = n>>7 - 1
		i--
		tmp[i] = byte(n&0x7f) | 0x80
	}
	return append(b, tmp[i:]...)
}

func readChainstateRow(t *testing.T, row []byte) ([]utxoRecord, error) {
	t.Helper()
	u := &utxoReader{r: bufio.NewReader(bytes.NewReader(row)), oversizeUnspendable: true}
	var coins []utxoRecord
	err := u.chainstateCoins([32]byte{0x77}, len(row), func(r utxoRecord) error {
		coins = append(coins, r)
		return nil
	})
	return coins, err
}

func TestChainstateCoinsDecodeTheDocumentedExamples(t *testing.T) {
	// Both rows and their meaning come from the comment above CCoins in Dogecoin Core 1.14.9's coins.h.
	for _, tc := range []struct {
		row      string
		coinbase bool
		height   uint32
		want     map[uint64][2]string
	}{
		{"0104835800816115944e077fe7c803cfa57f29b36bf87c1d358bb85e", false, 203998,
			map[uint64][2]string{1: {"60000000000", "76a914816115944e077fe7c803cfa57f29b36bf87c1d3588ac"}}},
		{"0109044086ef97d5790061b01caab50f1b8e9c50a5057eb43c2d9563a4eebbd123008c988f1a4a4de2161e0f50aac7f17e7f9555caa486af3b", true, 120891,
			map[uint64][2]string{4: {"234925952", "76a91461b01caab50f1b8e9c50a5057eb43c2d9563a4ee88ac"}, 16: {"110397", "76a9148c988f1a4a4de2161e0f50aac7f17e7f9555caa488ac"}}},
	} {
		coins, err := readChainstateRow(t, mustHex(tc.row))
		if err != nil || len(coins) != len(tc.want) {
			t.Fatalf("row %s: %d coins, %v", tc.row, len(coins), err)
		}
		for _, c := range coins {
			want, ok := tc.want[c.Vout]
			if !ok || c.Txid != [32]byte{0x77} || c.Height != tc.height || c.Coinbase != tc.coinbase ||
				want[0] != strconv.FormatUint(c.Amount, 10) || hex.EncodeToString(c.ScriptPubKey) != want[1] {
				t.Fatalf("row %s: vout %d amount %d script %x height %d coinbase %v", tc.row, c.Vout, c.Amount, c.ScriptPubKey, c.Height, c.Coinbase)
			}
		}
	}
}

func TestChainstateCoinsDecodeEveryScriptForm(t *testing.T) {
	hash := bytes.Repeat([]byte{0x11}, 20)
	gx := mustHex("79be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798")
	gy := mustHex("483ada7726a3c4655da4fbfc0e1108a8fd17b448a68554199c47d08ffb10d4b8")
	for _, tc := range []struct {
		name   string
		script []byte
		want   []byte
	}{
		{"pay to script hash", append([]byte{1}, hash...), append(append([]byte{0xa9, 20}, hash...), 0x87)},
		{"compressed public key, even y", append([]byte{2}, gx...), append(append([]byte{33, 2}, gx...), 0xac)},
		{"compressed public key, odd y", append([]byte{3}, gx...), append(append([]byte{33, 3}, gx...), 0xac)},
		{"uncompressed public key, even y", append([]byte{4}, gx...), append(append(append([]byte{65, 4}, gx...), gy...), 0xac)},
		{"uncompressed public key, odd y", append([]byte{5}, gx...), append(append(append([]byte{65, 4}, gx...), mustHex("b7c52588d95c3b9aa25b0403f1eef75702e84bb7597aabe663b82f6f04ef2777")...), 0xac)},
		{"raw script", append(appendCoreVarInt(nil, 6+3), 0x51, 0x51, 0x87), []byte{0x51, 0x51, 0x87}},
		{"script over the size limit", append(appendCoreVarInt(nil, 6+10001), make([]byte, 10001)...), []byte{0x6a}},
	} {
		// Version 1, code 2 (only output 0 unspent), one coin (amount 9 decompresses to 1e8), height 500.
		row := append(append([]byte{1, 2, 9}, tc.script...), appendCoreVarInt(nil, 500)...)
		coins, err := readChainstateRow(t, row)
		if err != nil || len(coins) != 1 || coins[0].Vout != 0 || coins[0].Amount != 100_000_000 || coins[0].Height != 500 || !bytes.Equal(coins[0].ScriptPubKey, tc.want) {
			t.Fatalf("%s: %+v, %v", tc.name, coins, err)
		}
	}
	// No first-two outputs, so one non-zero mask byte is stored as zero; a zero byte inside the mask is not counted.
	row := []byte{1, 1, 0x00, 0x01, 9, 0}
	row = append(append(row, hash...), appendCoreVarInt(nil, 7)...)
	if coins, err := readChainstateRow(t, row); err != nil || len(coins) != 1 || coins[0].Vout != 10 || !coins[0].Coinbase {
		t.Fatalf("mask with a zero byte: %+v, %v", coins, err)
	}
	// The last row's mask claims 8 outputs per byte with no outputs behind it.
	for _, bad := range [][]byte{row[:len(row)-1], append(slices.Clone(row), 0), {1, 0x18, 0, 0, 0}, append([]byte{1, 0x78}, bytes.Repeat([]byte{0xff}, 16)...)} {
		if _, err := readChainstateRow(t, bad); err == nil {
			t.Fatalf("a malformed row %x parsed", bad)
		}
	}
}

func TestChainstateCopyReadsADogecoinRegtestChainstate(t *testing.T) {
	// Copied from Dogecoin Core 1.14.9 on regtest after a clean stop, with 000005.ldb renamed as the host helper does.
	// The node reported height 202, best block 2f9d…68dc, 205 transactions, 210 outputs and 87,750,000 DOGE in gettxoutsetinfo.
	dir := "testdata/dogecoin-chainstate-regtest"
	before, _ := os.ReadDir(dir)
	c, err := openChainstateCopy(dir, vfs.Default, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	want, _ := utxoRevHex("2f9d802dd325448211bc2d04b33f63739f0e4f9e49319588c1700c37e23868dc")
	if c.Best != want || len(c.xor) != 8 || bytes.Equal(c.xor, make([]byte, 8)) {
		t.Fatalf("best block %x, obfuscation key %x", c.Best, c.xor)
	}
	rows := 0
	if err := c.rows(func([32]byte, uint32, []byte) error { rows++; return nil }); err != nil || rows != 205 {
		t.Fatalf("%d coins rows, %v", rows, err)
	}
	var coins, total uint64
	h, err := c.parse(nil, func(r utxoRecord) error {
		coins++
		total += r.Amount
		if r.Height > 202 || len(r.ScriptPubKey) == 0 {
			t.Errorf("coin %x:%d at height %d with script %x", r.Txid, r.Vout, r.Height, r.ScriptPubKey)
		}
		return nil
	})
	if err != nil || h.CoinsCount != 210 || coins != 210 || total != 87_750_000*100_000_000 || h.BaseHash != want {
		t.Fatalf("header %+v, %d coins worth %d, %v", h, coins, total, err)
	}
	if after, _ := os.ReadDir(dir); len(after) != len(before) {
		t.Fatal("reading the copy wrote to it")
	}
}

func maskChainstate(key, v []byte) []byte {
	out := bytes.Clone(v)
	for i := range out {
		if len(key) > 1 {
			out[i] ^= key[1+i%(len(key)-1)]
		}
	}
	return out
}

// writeTestChainstate writes a chainstate the way 1.14 lays it out, obfuscated with xor, using Pebble since the reader is format-blind; the rows end up in tables.
func writeTestChainstate(t *testing.T, key []byte, best []byte, rows map[string][]byte) string {
	t.Helper()
	dir := t.TempDir()
	db, err := pebble.Open(dir, &pebble.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if key != nil {
		_ = db.Set(chainstateObfuscateKey, key, nil)
	}
	if best != nil {
		_ = db.Set([]byte{'B'}, maskChainstate(key, best), nil)
	}
	for k, row := range rows {
		_ = db.Set([]byte(k), maskChainstate(key, row), nil)
	}
	if err := errors.Join(db.Flush(), db.Close()); err != nil {
		t.Fatal(err)
	}
	return dir
}

var logBatchSeq atomic.Uint64

// logBatch encodes a LevelDB write batch as a node logs it: a sequence number above every earlier batch's, the count, then each put, or a delete where the value is nil.
func logBatch(n int, entry func(i int) (key, val []byte)) []byte {
	b := binary.LittleEndian.AppendUint32(binary.LittleEndian.AppendUint64(nil, 1<<40+logBatchSeq.Add(uint64(n))), uint32(n))
	for i := range n {
		k, v := entry(i)
		kind := byte(pebble.InternalKeyKindSet)
		if v == nil {
			kind = byte(pebble.InternalKeyKindDelete)
		}
		b = append(binary.AppendUvarint(append(b, kind), uint64(len(k))), k...)
		if v != nil {
			b = append(binary.AppendUvarint(b, uint64(len(v))), v...)
		}
	}
	return b
}

func logEntries(kv ...[]byte) []byte {
	return logBatch(len(kv)/2, func(i int) ([]byte, []byte) { return kv[2*i], kv[2*i+1] })
}

func writeChainstateLog(t *testing.T, dir string, num int, batches ...[]byte) {
	t.Helper()
	f, err := os.Create(filepath.Join(dir, fmt.Sprintf("%06d.log", num)))
	if err != nil {
		t.Fatal(err)
	}
	w := record.NewWriter(f)
	for _, b := range batches {
		if _, err := w.WriteRecord(b); err != nil {
			t.Fatal(err)
		}
	}
	if err := errors.Join(w.Close(), f.Close()); err != nil {
		t.Fatal(err)
	}
}

func TestChainstateCopyAppliesItsLogsOverItsTablesInOrder(t *testing.T) {
	key := append([]byte{8}, 1, 2, 3, 4, 5, 6, 7, 8)
	txid := func(b byte) []byte { return append([]byte{'c'}, bytes.Repeat([]byte{b}, 32)...) }
	plain := func(height byte) []byte {
		return append(append([]byte{1, 2, 9, 0}, bytes.Repeat([]byte{0x11}, 20)...), height)
	}
	row := func(height byte) []byte { return maskChainstate(key, plain(height)) }
	dir := writeTestChainstate(t, key, bytes.Repeat([]byte{0x42}, 32), map[string][]byte{
		string(txid(0xa)): plain(1), string(txid(0xb)): plain(2), string(txid(0xc)): plain(3),
	})
	// Pebble skips a log older than its tables, so this one must change nothing.
	writeChainstateLog(t, dir, 1, logEntries(txid(0xa), row(99), txid(0xe), row(99)))
	writeChainstateLog(t, dir, 998,
		logEntries(txid(0xb), row(20)),
		logEntries(txid(0xc), nil, txid(0xd), row(4), []byte{'B'}, maskChainstate(key, bytes.Repeat([]byte{0x43}, 32))))
	writeChainstateLog(t, dir, 999, logEntries(txid(0xb), row(21), txid(0xa), nil))
	logDir := filepath.Join(t.TempDir(), "log")
	c, err := openChainstateCopy(dir, vfs.Default, logDir)
	if err != nil {
		t.Fatal(err)
	}
	heights := map[byte]uint32{}
	h, err := c.parse(nil, func(r utxoRecord) error { heights[r.Txid[0]] = r.Height; return nil })
	if err != nil || h.CoinsCount != 2 || len(heights) != 2 || heights[0xb] != 21 || heights[0xd] != 4 {
		t.Fatalf("coins %v, header %+v, %v", heights, h, err)
	}
	if want := [32]byte(bytes.Repeat([]byte{0x43}, 32)); c.Best != want {
		t.Fatalf("best block %x, the log named %x", c.Best, want)
	}
	// Pebble reaches the same rows by replaying the logs in memory, so only the scratch store shows they were streamed.
	if _, closer, err := c.log.Get(txid(0xd)); err != nil {
		t.Fatalf("the log's rows are not in the scratch store: %v", err)
	} else {
		_ = closer.Close()
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(logDir); !os.IsNotExist(err) {
		t.Fatalf("the replayed log was left behind: %v", err)
	}
}

func TestChainstateCopyRefusesACorruptLog(t *testing.T) {
	key := append([]byte{8}, 1, 2, 3, 4, 5, 6, 7, 8)
	k := append([]byte{'c'}, bytes.Repeat([]byte{0xab}, 32)...)
	good := logEntries(k, []byte{1})
	for name, batch := range map[string][]byte{
		"merge record":    append(slices.Clone(good[:12]), append([]byte{byte(pebble.InternalKeyKindMerge), 33}, append(k, 1, 1)...)...),
		"oversized row":   binary.AppendUvarint(append(slices.Clone(good[:12]), append([]byte{1, 33}, k...)...), chainstateMaxLogField+1),
		"missing entry":   append(slices.Clone(good[:8]), append([]byte{2, 0, 0, 0}, good[12:]...)...),
		"trailing bytes":  append(slices.Clone(good), 0),
		"short header":    good[:11],
		"truncated value": good[:len(good)-1],
	} {
		dir := writeTestChainstate(t, key, bytes.Repeat([]byte{0x42}, 32), nil)
		writeChainstateLog(t, dir, 999, batch)
		if c, err := openChainstateCopy(dir, vfs.Default, t.TempDir()); !errors.Is(err, errChainstateFormat) {
			if c != nil {
				_ = c.Close()
			}
			t.Errorf("%s: opened with %v", name, err)
		}
	}
}

// A Dogecoin 1.14 node writes its whole dbcache, 450 MiB by default, as one log batch when it stops; Pebble's replay held four times the batch, the reader a fixed few tens of MiB.
func TestChainstateCopyKeepsALargeLogOutOfMemory(t *testing.T) {
	const logBytes = 64 << 20
	key := append([]byte{8}, 1, 2, 3, 4, 5, 6, 7, 8)
	dir := writeTestChainstate(t, key, bytes.Repeat([]byte{0x42}, 32), nil)
	val := maskChainstate(key, append(append([]byte{1, 2, 9, 0}, bytes.Repeat([]byte{0x11}, 20)...), 77))
	k := make([]byte, 33)
	k[0] = 'c'
	n := logBytes / (len(k) + len(val) + 3)
	writeChainstateLog(t, dir, 999, logBatch(n, func(i int) ([]byte, []byte) {
		binary.BigEndian.PutUint64(k[1:], uint64(i))
		return k, val
	}))
	defer debug.SetGCPercent(debug.SetGCPercent(25))
	runtime.GC()
	sample := []metrics.Sample{{Name: "/memory/classes/heap/objects:bytes"}}
	metrics.Read(sample)
	base := sample[0].Value.Uint64()
	var peak atomic.Uint64
	done := make(chan struct{})
	sampled := make(chan struct{})
	go func() {
		defer close(sampled)
		s := []metrics.Sample{{Name: "/memory/classes/heap/objects:bytes"}}
		for {
			metrics.Read(s)
			peak.Store(max(peak.Load(), s[0].Value.Uint64()))
			select {
			case <-done:
				return
			case <-time.After(time.Millisecond):
			}
		}
	}()
	c, err := openChainstateCopy(dir, vfs.Default, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	rows := 0
	if err := c.rows(func([32]byte, uint32, []byte) error { rows++; return nil }); err != nil || rows != n {
		t.Fatalf("%d of %d rows, %v", rows, n, err)
	}
	close(done)
	<-sampled
	if grew := peak.Load() - min(base, peak.Load()); grew > logBytes {
		t.Fatalf("the heap grew by %d MiB reading a %d MiB log", grew>>20, logBytes>>20)
	}
}

func TestChainstateCopyReadsAnOversizedScriptAsUnspendable(t *testing.T) {
	key := append([]byte{8}, 1, 2, 3, 4, 5, 6, 7, 8)
	best := bytes.Repeat([]byte{0x42}, 32)
	big := append(appendCoreVarInt(nil, 6+10001), make([]byte, 10001)...)
	// Code 6: outputs 0 and 1 unspent, the first with a script over the limit, the second pay-to-script-hash.
	row := append(append(append([]byte{1, 6, 9}, big...), 9, 1), bytes.Repeat([]byte{0x11}, 20)...)
	row = append(row, appendCoreVarInt(nil, 77)...)
	c, err := openChainstateCopy(writeTestChainstate(t, key, best, map[string][]byte{"c" + string(bytes.Repeat([]byte{0xab}, 32)): row}), vfs.Default, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	var coins []utxoRecord
	h, err := c.parse(nil, func(r utxoRecord) error { coins = append(coins, r); return nil })
	if err != nil || h.CoinsCount != 2 || len(coins) != 2 || !bytes.Equal(coins[0].ScriptPubKey, []byte{0x6a}) || utxoSpendable(coins[0].ScriptPubKey) || coins[1].Height != 77 {
		t.Fatalf("an oversized script failed the seed or read wrongly: %+v %+v %v", h, coins, err)
	}
}

func TestChainstateCopyRefusesACorruptChainstate(t *testing.T) {
	key := append([]byte{8}, 1, 2, 3, 4, 5, 6, 7, 8)
	best := bytes.Repeat([]byte{0x42}, 32)
	for name, tc := range map[string]struct {
		key, best []byte
		rows      map[string][]byte
		atOpen    bool
	}{
		"obfuscation key length byte": {key: []byte{4, 1, 2, 3, 4, 5, 6, 7, 8}, best: best, atOpen: true},
		"short obfuscation key":       {key: []byte{8, 1, 2, 3, 4}, best: best, atOpen: true},
		"long obfuscation key":        {key: append(slices.Clone(key), 9), best: best, atOpen: true},
		"short best block":            {key: key, best: best[:16], atOpen: true},
		"long best block":             {key: key, best: append(slices.Clone(best), 0x42), atOpen: true},
		"missing best block":          {key: key, atOpen: true},
		// The row itself parses, so only the key length can refuse it.
		"coins key of 32 bytes": {key: key, best: best, rows: map[string][]byte{"c" + string(bytes.Repeat([]byte{0xab}, 31)): append(append([]byte{1, 2, 9, 0}, bytes.Repeat([]byte{0x11}, 20)...), 77)}},
	} {
		c, err := openChainstateCopy(writeTestChainstate(t, tc.key, tc.best, tc.rows), vfs.Default, t.TempDir())
		if tc.atOpen {
			if !errors.Is(err, errChainstateFormat) {
				t.Fatalf("%s: opened with %v", name, err)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		_, err = c.parse(nil, func(utxoRecord) error { return nil })
		_ = c.Close()
		if !errors.Is(err, errChainstateFormat) || !strings.Contains(err.Error(), "coins key of 32 bytes") {
			t.Fatalf("%s: parsed with %v", name, err)
		}
	}
}
