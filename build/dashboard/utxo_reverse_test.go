package main

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cockroachdb/pebble/vfs"
)

func utxoTestOpenReverse(t testing.TB, dir string) *utxoStore {
	t.Helper()
	s, err := openUtxoStoreFS(dir, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// utxoTestKeys returns every coin and reverse record and checks that each coin has exactly one matching reverse key.
func utxoTestKeys(t testing.TB, s *utxoStore) map[string]string {
	t.Helper()
	it, err := s.db.NewIter(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer it.Close()
	out := map[string]string{}
	var coins, revs int
	for ok := it.First(); ok; ok = it.Next() {
		k, v := it.Key(), it.Value()
		switch {
		case bytes.Equal(k, utxoMetaKey) || len(k) == 8 && bytes.HasPrefix(k, []byte("undo")):
			continue
		case len(k) == utxoKeyLen:
			coins++
		case len(k) == utxoRevKeyLen && k[0] == utxoRevTag && len(v) == utxoPrefixLen:
			revs++
		default:
			t.Fatalf("unexpected record %x = %x", k, v)
		}
		out[string(k)] = string(v)
	}
	for k, v := range out {
		if len(k) == utxoKeyLen && s.reverse && out[string(utxoRevKey([]byte(k)))] != k[:utxoPrefixLen] {
			t.Fatalf("coin %x has no reverse key", k)
		}
		if len(k) == utxoRevKeyLen {
			if _, ok := out[v+k[1:]]; !ok {
				t.Fatalf("reverse key %x points at no coin", k)
			}
		}
	}
	if s.reverse && revs != coins || !s.reverse && revs != 0 {
		t.Fatalf("%d coins and %d reverse keys in a reverse=%v store", coins, revs, s.reverse)
	}
	return out
}

// runSizeFS records each ingest run file's size as the ingest removes it.
type runSizeFS struct {
	vfs.FS
	sizes []int64
}

func (f *runSizeFS) Remove(name string) error {
	if strings.HasPrefix(f.PathBase(name), "run-") {
		if st, err := f.Stat(name); err == nil {
			f.sizes = append(f.sizes, st.Size())
		}
	}
	return f.FS.Remove(name)
}

func TestUtxoReverseSeedSplitsTheRunBuffer(t *testing.T) {
	utxoTestSmallRuns(t, 64, 4<<10)
	raw, info, _ := litecoinFixture(t)
	for _, tc := range []struct {
		reverse          bool
		runs, perRunRecs int
	}{
		{false, 8, 64},
		{true, 2 * 15, 32},
	} {
		fs := &runSizeFS{FS: vfs.Default}
		s, err := openUtxoStoreFS(t.TempDir(), fs, tc.reverse)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ingestUtxoSnapshot(s, bytes.NewReader(raw), utxoLitecoin, [4]byte{}, utxoTestHeight(info.Height), nil); err != nil {
			t.Fatal(err)
		}
		_ = s.Close()
		if len(fs.sizes) != tc.runs || slices.Max(fs.sizes) != int64(tc.perRunRecs*utxoIngestRecLen) {
			t.Fatalf("reverse=%v: run sizes %v B, want %d runs of up to %d records", tc.reverse, fs.sizes, tc.runs, tc.perRunRecs)
		}
	}
}

func TestUtxoReverseKeyFixtureMeasure(t *testing.T) {
	if os.Getenv("UTXO_REVERSE_MEASURE") == "" {
		t.Skip("set UTXO_REVERSE_MEASURE=1 to print the fixture's SST bytes per coin with and without reverse keys")
	}
	raw, info, _ := litecoinFixture(t)
	for _, reverse := range []bool{false, true} {
		dir := t.TempDir()
		s, err := openUtxoStoreFS(dir, nil, reverse)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ingestUtxoSnapshot(s, bytes.NewReader(raw), utxoLitecoin, [4]byte{}, utxoTestHeight(info.Height), nil); err != nil {
			t.Fatal(err)
		}
		_ = s.Close()
		ssts, _ := filepath.Glob(filepath.Join(dir, "*.sst"))
		var n int64
		for _, p := range ssts {
			n += utxoTestDirSize(p)
		}
		t.Logf("reverse=%v coins=%d sst_files=%d sst_bytes=%d (%.1f B/coin)", reverse, info.TxOuts, len(ssts), n, float64(n)/float64(info.TxOuts))
	}
}

func withoutPrevoutScripts(blk utxoBlock) utxoBlock {
	for i := range blk.Spends {
		blk.Spends[i].Script = nil
	}
	return blk
}

func TestUtxoReverseStoreAppliesAndUndoesBlocksWithoutPrevoutScripts(t *testing.T) {
	s := utxoTestOpenReverse(t, t.TempDir())
	before := []map[string]string{utxoTestKeys(t, s)}
	for h := uint32(1); h <= 5; h++ {
		if err := s.ApplyBlock(withoutPrevoutScripts(utxoTestBlock(h))); err != nil {
			t.Fatalf("apply %d: %v", h, err)
		}
		before = append(before, utxoTestKeys(t, s))
	}
	if got, want := utxoTestAll(t, s), utxoTestExpected(5); !slices.Equal(got, want) {
		t.Fatalf("coins %+v, want %+v", got, want)
	}

	same := utxoBlock{Height: 6, Hash: utxoTestHash(6), PrevHash: utxoTestHash(5)}
	x := utxoOutpoint{Txid: utxoTestTxid(6), Vout: 0}
	same.Creates = []utxoCreate{{utxoOutpoint: x, Script: utxoTestScript(1), Amount: 7}}
	same.Spends = []utxoSpend{{utxoOutpoint: x}, {utxoOutpoint: utxoOutpoint{Txid: utxoTestTxid(5), Vout: 1}}}
	if err := s.ApplyBlock(same); err != nil {
		t.Fatal(err)
	}
	keys := utxoTestKeys(t, s)
	if len(keys) != len(before[5])-2 {
		t.Fatalf("same-block spend left %d records, want %d", len(keys), len(before[5])-2)
	}

	missing := utxoBlock{Height: 7, Hash: utxoTestHash(7), PrevHash: utxoTestHash(6),
		Spends: []utxoSpend{{utxoOutpoint: utxoOutpoint{Txid: utxoTestTxid(99), Vout: 0}}}}
	if err := s.ApplyBlock(missing); !errors.Is(err, errUtxoMissingCoin) {
		t.Fatalf("unknown outpoint: %v", err)
	}
	if !maps.Equal(utxoTestKeys(t, s), keys) {
		t.Fatal("a failed block changed the store")
	}

	for h := len(before); h > 0; h-- {
		if err := s.DisconnectTip(); err != nil {
			t.Fatal(err)
		}
		if !maps.Equal(utxoTestKeys(t, s), before[h-1]) {
			t.Fatalf("disconnecting block %d did not restore the records", h)
		}
	}
}

func legacyUtxoDump(base [32]byte, coins []utxoRecord) []byte {
	var buf bytes.Buffer
	b := dumpBuilder{bufio.NewWriter(&buf)}
	b.bytes(base[:]...)
	b.bytes(binary.LittleEndian.AppendUint64(nil, uint64(len(coins)))...)
	b.bytes(0, 0, 0, 0)
	for _, c := range coins {
		b.bytes(c.Txid[:]...)
		b.bytes(binary.LittleEndian.AppendUint32(nil, uint32(c.Vout))...)
		b.varint(uint64(c.Height) << 1)
		b.varint(compressUtxoAmountForTest(c.Amount))
		b.varint(uint64(len(c.ScriptPubKey)) + 6)
		b.bytes(c.ScriptPubKey...)
	}
	b.w.Flush()
	return buf.Bytes()
}

func TestUtxoReverseSeedThenFollowEqualsAReseed(t *testing.T) {
	utxoTestSmallRuns(t, 64, 4<<10)
	raw, info, base := litecoinFixture(t)
	coins := map[utxoOutpoint]utxoRecord{}
	var order []utxoOutpoint
	var hogex utxoOutpoint
	if _, err := parseLegacyUtxoDump(bytes.NewReader(raw), nil, func(r utxoRecord) error {
		o := utxoOutpoint{Txid: r.Txid, Vout: uint32(r.Vout)}
		r.ScriptPubKey = bytes.Clone(r.ScriptPubKey)
		coins[o], order = r, append(order, o)
		if len(r.ScriptPubKey) == 34 && r.ScriptPubKey[0] == 0x58 && r.ScriptPubKey[1] == 0x20 {
			hogex = o
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if hogex == (utxoOutpoint{}) {
		t.Fatal("the fixture has no HogEx coin")
	}

	newHogex := append([]byte{0x58, 0x20}, bytes.Repeat([]byte{0x11}, 32)...)
	p2wpkh, _ := hex.DecodeString("0014119b8080c9f7f59269fa32511051d1c980765840")
	tx := func(h uint32, i byte) [32]byte { return utxoTestTxid(h*256 + uint32(i)) }
	blocks := []utxoBlock{
		{Creates: []utxoCreate{
			{utxoOutpoint{tx(439, 0), 0}, newHogex, 1_500_000_000},
			{utxoOutpoint{tx(439, 0), 1}, p2wpkh, 99_000_000},
		}, Spends: []utxoSpend{{utxoOutpoint: hogex}, {utxoOutpoint: order[0]}, {utxoOutpoint: order[1]}}},
		{Creates: []utxoCreate{
			{utxoOutpoint{tx(440, 0), 0}, p2wpkh, 5_000},
			{utxoOutpoint{tx(440, 1), 0}, coins[order[2]].ScriptPubKey, 4_000},
		}, Spends: []utxoSpend{{utxoOutpoint: utxoOutpoint{tx(440, 0), 0}}, {utxoOutpoint: utxoOutpoint{tx(439, 0), 1}}}},
		{Spends: []utxoSpend{{utxoOutpoint: order[3]}, {utxoOutpoint: utxoOutpoint{tx(439, 0), 0}}}},
	}
	prev := base
	for i := range blocks {
		blk := &blocks[i]
		blk.Height, blk.Hash, blk.PrevHash = info.Height+uint32(i)+1, utxoTestHash(info.Height+uint32(i)+1), prev
		prev = blk.Hash
		for _, c := range blk.Creates {
			coins[c.utxoOutpoint] = utxoRecord{ScriptPubKey: c.Script, Txid: c.Txid, Vout: uint64(c.Vout), Amount: c.Amount, Height: blk.Height}
		}
		for _, sp := range blk.Spends {
			delete(coins, sp.utxoOutpoint)
		}
	}

	followed := utxoTestOpenReverse(t, t.TempDir())
	prog := &utxoIngestProgress{}
	if _, err := ingestUtxoSnapshot(followed, bytes.NewReader(raw), utxoLitecoin, [4]byte{}, utxoTestHeight(info.Height), prog); err != nil {
		t.Fatal(err)
	}
	if prog.Written.Load() != info.TxOuts || prog.Total.Load() != info.TxOuts {
		t.Fatalf("progress wrote %d of %d coins, want %d", prog.Written.Load(), prog.Total.Load(), info.TxOuts)
	}
	seeded := utxoTestKeys(t, followed)
	for round := 0; round < 2; round++ {
		for _, blk := range blocks {
			if err := followed.ApplyBlock(blk); err != nil {
				t.Fatalf("apply %d: %v", blk.Height, err)
			}
		}
		if got, _ := followed.Lookup([][]byte{coins[hogex].ScriptPubKey}); round == 0 && len(got) != 0 {
			t.Fatalf("the spent HogEx coin is still indexed: %+v", got)
		}
		if round == 0 {
			for range blocks {
				if err := followed.DisconnectTip(); err != nil {
					t.Fatal(err)
				}
			}
			if !maps.Equal(utxoTestKeys(t, followed), seeded) {
				t.Fatal("rolling back to the seed did not restore it")
			}
		}
	}

	final := slices.Collect(maps.Values(coins))
	slices.SortFunc(final, func(a, b utxoRecord) int { return bytes.Compare(a.Txid[:], b.Txid[:]) })
	reseeded := utxoTestOpenReverse(t, t.TempDir())
	if _, err := ingestUtxoSnapshot(reseeded, bytes.NewReader(legacyUtxoDump(prev, final)), utxoLitecoin, [4]byte{}, utxoTestHeight(blocks[2].Height), nil); err != nil {
		t.Fatal(err)
	}
	want, got := utxoTestKeys(t, reseeded), utxoTestKeys(t, followed)
	if len(final) != 449-4+1 || !maps.Equal(got, want) {
		t.Fatalf("seed then follow has %d records, a reseed of the same %d coins has %d", len(got), len(final), len(want))
	}
}

func TestUtxoStoreSchemaKeepsBitcoinAndReverseStoresApart(t *testing.T) {
	bitcoin, litecoin := t.TempDir(), t.TempDir()
	utxoTestIngestFile(t, utxoTestOpen(t, t.TempDir()), "testdata/utxo/all_types.dat", 103)
	s := utxoTestOpen(t, bitcoin)
	utxoTestApply(t, s, 1, 3)
	if err := s.DisconnectTip(); err != nil {
		t.Fatal(err)
	}
	utxoTestKeys(t, s)
	if s.meta.Schema != 3 {
		t.Fatalf("bitcoin schema %d", s.meta.Schema)
	}
	r := utxoTestOpenReverse(t, litecoin)
	utxoTestApply(t, r, 1, 2)
	if r.meta.Schema != 4 {
		t.Fatalf("reverse schema %d", r.meta.Schema)
	}
	for dir, reverse := range map[string]bool{bitcoin: true, litecoin: false} {
		for _, st := range []*utxoStore{s, r} {
			_ = st.Close()
		}
		other, err := openUtxoStoreFS(dir, nil, reverse)
		if err != nil {
			t.Fatal(err)
		}
		if other.Valid() {
			t.Fatalf("store at %s opened with reverse=%v", dir, reverse)
		}
		_ = other.Close()
	}
}
