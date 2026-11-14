package main

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"reflect"
	"sort"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/cockroachdb/pebble"
	"github.com/cockroachdb/pebble/vfs"
	"github.com/cockroachdb/pebble/vfs/errorfs"
)

const utxoTestScripts = 7

func utxoTestScript(i int) []byte {
	return []byte{0x00, 0x14, byte(i), 0xaa, 0xbb}
}

func utxoTestAllScripts() [][]byte {
	out := make([][]byte, utxoTestScripts)
	for i := range out {
		out[i] = utxoTestScript(i)
	}
	return out
}

func utxoTestHash(h uint32) [32]byte {
	return sha256.Sum256(binary.BigEndian.AppendUint32([]byte("block"), h))
}

func utxoTestTxid(h uint32) [32]byte {
	return sha256.Sum256(binary.BigEndian.AppendUint32([]byte("tx"), h))
}

// Block h creates three coins and spends output 0 of block h-1.
func utxoTestBlock(h uint32) utxoBlock {
	blk := utxoBlock{Height: h, Hash: utxoTestHash(h), PrevHash: utxoTestHash(h - 1)}
	for v := uint32(0); v < 3; v++ {
		blk.Creates = append(blk.Creates, utxoCreate{
			utxoOutpoint: utxoOutpoint{Txid: utxoTestTxid(h), Vout: v},
			Script:       utxoTestScript(int(h+v) % utxoTestScripts),
			Amount:       uint64(h)*10 + uint64(v),
		})
	}
	if h > 1 {
		blk.Spends = append(blk.Spends, utxoSpend{
			utxoOutpoint: utxoOutpoint{Txid: utxoTestTxid(h - 1), Vout: 0},
			Script:       utxoTestScript(int(h-1) % utxoTestScripts),
		})
	}
	return blk
}

func utxoTestExpected(tip uint32) []utxoCoin {
	var out []utxoCoin
	for h := uint32(1); h <= tip; h++ {
		for v := uint32(0); v < 3; v++ {
			if v == 0 && h < tip {
				continue
			}
			sh := utxoScriptHash(utxoTestScript(int(h+v) % utxoTestScripts))
			out = append(out, utxoCoin{ScriptHash: sh, utxoOutpoint: utxoOutpoint{Txid: utxoTestTxid(h), Vout: v},
				Amount: uint64(h)*10 + uint64(v), Height: h})
		}
	}
	sortUtxoCoins(out)
	return out
}

func sortUtxoCoins(c []utxoCoin) {
	sort.Slice(c, func(i, j int) bool {
		return string(utxoCoinKey(c[i].ScriptHash, c[i].utxoOutpoint)) < string(utxoCoinKey(c[j].ScriptHash, c[j].utxoOutpoint))
	})
}

func utxoTestAll(t testing.TB, s *utxoStore) []utxoCoin {
	t.Helper()
	got, err := s.Lookup(utxoTestAllScripts())
	if err != nil {
		t.Fatal(err)
	}
	sortUtxoCoins(got)
	return got
}

func utxoTestOpen(t testing.TB, dir string) *utxoStore {
	t.Helper()
	s, err := openUtxoStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func utxoTestApply(t testing.TB, s *utxoStore, from, to uint32) {
	t.Helper()
	for h := from; h <= to; h++ {
		if err := s.ApplyBlock(utxoTestBlock(h)); err != nil {
			t.Fatalf("apply %d: %v", h, err)
		}
	}
}

func utxoTestExpectTip(t testing.TB, s *utxoStore, height uint32) {
	t.Helper()
	h, hash, ok, err := s.Tip()
	if err != nil || !ok || h != height || hash != utxoTestHash(height) {
		t.Fatalf("tip = %d %x %v %v, want %d", h, hash, ok, err, height)
	}
}

func TestUtxoStoreApplyLookupAndReopen(t *testing.T) {
	dir := t.TempDir()
	s := utxoTestOpen(t, dir)
	if _, _, ok, err := s.Tip(); err != nil || ok {
		t.Fatalf("fresh store tip ok=%v err=%v", ok, err)
	}
	utxoTestApply(t, s, 1, 20)
	utxoTestExpectTip(t, s, 20)
	if got := utxoTestAll(t, s); !reflect.DeepEqual(got, utxoTestExpected(20)) {
		t.Fatalf("coins after apply:\n got %v\nwant %v", got, utxoTestExpected(20))
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s = utxoTestOpen(t, dir)
	utxoTestExpectTip(t, s, 20)
	if got := utxoTestAll(t, s); !reflect.DeepEqual(got, utxoTestExpected(20)) {
		t.Fatal("coins differ after reopen")
	}
}

func TestUtxoStoreKeyAndValueLayout(t *testing.T) {
	script := utxoTestScript(1)
	o := utxoOutpoint{Txid: utxoTestTxid(9), Vout: 0x01020304}
	h := utxoScriptHash(script)
	sum := sha256.Sum256(script)
	key := utxoCoinKey(h, o)
	if len(key) != 52 || string(key[:16]) != string(sum[:16]) || string(key[16:48]) != string(o.Txid[:]) ||
		string(key[48:]) != "\x01\x02\x03\x04" {
		t.Fatalf("bad key %x", key)
	}
	if v := utxoCoinValue(0x0102030405060708, 0x0a0b0c0d); string(v) != "\x01\x02\x03\x04\x05\x06\x07\x08\x0a\x0b\x0c\x0d" {
		t.Fatalf("bad value %x", v)
	}
}

func TestUtxoStoreLookupSetAndUnknownScript(t *testing.T) {
	s := utxoTestOpen(t, t.TempDir())
	utxoTestApply(t, s, 1, 5)
	one := utxoTestScript(2)
	got, err := s.Lookup([][]byte{one, one, []byte("unknown")})
	if err != nil {
		t.Fatal(err)
	}
	want := 0
	for _, c := range utxoTestExpected(5) {
		if c.ScriptHash == utxoScriptHash(one) {
			want++
		}
	}
	if len(got) != want || want == 0 {
		t.Fatalf("lookup returned %d coins, want %d", len(got), want)
	}
}

func TestUtxoStoreDisconnectRestoresPreviousCoins(t *testing.T) {
	s := utxoTestOpen(t, t.TempDir())
	utxoTestApply(t, s, 1, 10)
	before := utxoTestAll(t, s)
	utxoTestApply(t, s, 11, 11)
	if err := s.DisconnectTip(); err != nil {
		t.Fatal(err)
	}
	utxoTestExpectTip(t, s, 10)
	if got := utxoTestAll(t, s); !reflect.DeepEqual(got, before) {
		t.Fatal("disconnect did not restore the previous coins")
	}
	for h := uint32(10); h >= 1; h-- {
		if err := s.DisconnectTip(); err != nil {
			t.Fatalf("disconnect %d: %v", h, err)
		}
		if got := utxoTestAll(t, s); !reflect.DeepEqual(got, utxoTestExpected(h-1)) {
			t.Fatalf("coins wrong after disconnecting %d", h)
		}
	}
	h, _, ok, _ := s.Tip()
	if !ok || h != 0 {
		t.Fatalf("tip after full unwind = %d %v", h, ok)
	}
}

func TestUtxoStoreJournalDepth(t *testing.T) {
	s := utxoTestOpen(t, t.TempDir())
	utxoTestApply(t, s, 1, 200)
	for i := 0; i < utxoJournalDepth; i++ {
		if err := s.DisconnectTip(); err != nil {
			t.Fatalf("disconnect %d: %v", i, err)
		}
	}
	utxoTestExpectTip(t, s, 200-utxoJournalDepth)
	if err := s.DisconnectTip(); !errors.Is(err, errUtxoNoUndo) {
		t.Fatalf("disconnect past the journal: %v", err)
	}
	utxoTestExpectTip(t, s, 200-utxoJournalDepth)
}

func TestUtxoStoreIntraBlockSpendAndOverwriteUndo(t *testing.T) {
	s := utxoTestOpen(t, t.TempDir())
	script := utxoTestScript(3)
	op := utxoOutpoint{Txid: utxoTestTxid(500), Vout: 1}
	first := utxoBlock{Height: 1, Hash: utxoTestHash(1), PrevHash: utxoTestHash(0),
		Creates: []utxoCreate{{utxoOutpoint: op, Script: script, Amount: 5}}}
	second := utxoBlock{Height: 2, Hash: utxoTestHash(2), PrevHash: utxoTestHash(1),
		Creates: []utxoCreate{
			{utxoOutpoint: op, Script: script, Amount: 7},
			{utxoOutpoint: utxoOutpoint{Txid: utxoTestTxid(501)}, Script: script, Amount: 9},
			{utxoOutpoint: utxoOutpoint{Txid: utxoTestTxid(502)}, Script: []byte{0x6a, 0x01}, Amount: 0},
		},
		Spends: []utxoSpend{{utxoOutpoint: utxoOutpoint{Txid: utxoTestTxid(501)}, Script: script}}}
	for _, b := range []utxoBlock{first, second} {
		if err := s.ApplyBlock(b); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := s.Lookup([][]byte{script})
	if len(got) != 1 || got[0].Amount != 7 || got[0].Height != 2 {
		t.Fatalf("after block 2: %+v", got)
	}
	if err := s.DisconnectTip(); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Lookup([][]byte{script})
	if len(got) != 1 || got[0].Amount != 5 || got[0].Height != 1 {
		t.Fatalf("after disconnect: %+v", got)
	}
}

func TestUtxoStoreRejectsBadBlocksWithoutChange(t *testing.T) {
	dir := t.TempDir()
	s := utxoTestOpen(t, dir)
	utxoTestApply(t, s, 1, 3)
	before := utxoTestAll(t, s)

	missing := utxoTestBlock(4)
	missing.Spends = append(missing.Spends, utxoSpend{utxoOutpoint: utxoOutpoint{Txid: utxoTestTxid(999)}, Script: utxoTestScript(0)})
	if err := s.ApplyBlock(missing); !errors.Is(err, errUtxoMissingCoin) {
		t.Fatalf("missing coin: %v", err)
	}
	if err := s.ApplyBlock(utxoTestBlock(5)); !errors.Is(err, errUtxoNotContiguous) {
		t.Fatalf("gap: %v", err)
	}
	wrongParent := utxoTestBlock(4)
	wrongParent.PrevHash = utxoTestHash(77)
	if err := s.ApplyBlock(wrongParent); !errors.Is(err, errUtxoNotContiguous) {
		t.Fatalf("wrong parent: %v", err)
	}
	utxoTestExpectTip(t, s, 3)
	if got := utxoTestAll(t, s); !reflect.DeepEqual(got, before) {
		t.Fatal("rejected block changed the store")
	}
}

func TestUtxoStoreInterruptedApplyKeepsOldState(t *testing.T) {
	dir := t.TempDir()
	s := utxoTestOpen(t, dir)
	utxoTestApply(t, s, 1, 10)
	before := utxoTestAll(t, s)
	crash := errors.New("simulated crash before commit")
	s.beforeCommit = func() error { return crash }
	if err := s.ApplyBlock(utxoTestBlock(11)); !errors.Is(err, crash) {
		t.Fatalf("apply: %v", err)
	}
	if err := s.DisconnectTip(); !errors.Is(err, crash) {
		t.Fatalf("disconnect: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s = utxoTestOpen(t, dir)
	utxoTestExpectTip(t, s, 10)
	if got := utxoTestAll(t, s); !reflect.DeepEqual(got, before) {
		t.Fatal("interrupted apply left a mixed state")
	}
	utxoTestApply(t, s, 11, 11)
	utxoTestExpectTip(t, s, 11)
}

func TestUtxoStoreFormatMismatchIsInvalid(t *testing.T) {
	for name, meta := range map[string]utxoMeta{
		"schema":        {Schema: utxoSchemaVersion + 1, PebbleFormat: int(utxoPebbleFormat)},
		"pebble format": {Schema: utxoSchemaVersion, PebbleFormat: int(utxoPebbleFormat) + 1},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			s := utxoTestOpen(t, dir)
			utxoTestApply(t, s, 1, 3)
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			raw, err := pebble.Open(dir, &pebble.Options{FormatMajorVersion: utxoPebbleFormat})
			if err != nil {
				t.Fatal(err)
			}
			if err := raw.Set(utxoMetaKey, mustMarshalUtxoMeta(meta), pebble.Sync); err != nil {
				t.Fatal(err)
			}
			if err := raw.Close(); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 2; i++ {
				s = utxoTestOpen(t, dir)
				if s.Valid() || s.InvalidReason() == "" {
					t.Fatal("mismatched store reported valid")
				}
				if _, err := s.Lookup(utxoTestAllScripts()); !errors.Is(err, errUtxoInvalid) {
					t.Fatalf("lookup: %v", err)
				}
				if err := s.ApplyBlock(utxoTestBlock(4)); !errors.Is(err, errUtxoInvalid) {
					t.Fatalf("apply: %v", err)
				}
				if _, _, _, err := s.Tip(); !errors.Is(err, errUtxoInvalid) {
					t.Fatalf("tip: %v", err)
				}
				if err := s.Close(); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestUtxoStoreDataWithoutMetadataIsInvalid(t *testing.T) {
	dir := t.TempDir()
	raw, err := pebble.Open(dir, &pebble.Options{FormatMajorVersion: utxoPebbleFormat})
	if err != nil {
		t.Fatal(err)
	}
	if err := raw.Set([]byte("stray"), []byte("x"), pebble.Sync); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	if s := utxoTestOpen(t, dir); s.Valid() {
		t.Fatal("store without metadata reported valid")
	}
}

func TestUtxoStoreDiskFullLeavesStoreOpenable(t *testing.T) {
	var full atomic.Bool
	fs := errorfs.Wrap(vfs.NewMem(), errorfs.InjectorFunc(func(op errorfs.Op, _ string) error {
		if full.Load() && (op == errorfs.OpFileWrite || op == errorfs.OpFileSync || op == errorfs.OpCreate) {
			return syscall.ENOSPC
		}
		return nil
	}))
	s, err := openUtxoStoreFS("db", fs)
	if err != nil {
		t.Fatal(err)
	}
	utxoTestApply(t, s, 1, 10)
	before := utxoTestAll(t, s)
	full.Store(true)
	err = s.ApplyBlock(utxoTestBlock(11))
	full.Store(false)
	if err == nil {
		t.Fatal("apply succeeded on a full disk")
	}
	if _, _, _, err := s.Tip(); !errors.Is(err, errUtxoClosed) {
		t.Fatalf("handle after failed commit: %v", err)
	}
	s, err = openUtxoStoreFS("db", fs)
	if err != nil {
		t.Fatalf("reopen after ENOSPC: %v", err)
	}
	defer s.Close()
	utxoTestExpectTip(t, s, 10)
	if got := utxoTestAll(t, s); !reflect.DeepEqual(got, before) {
		t.Fatal("store changed after ENOSPC")
	}
	utxoTestApply(t, s, 11, 11)
}

func TestUtxoStoreKillLoop(t *testing.T) {
	if testing.Short() {
		t.Skip("kill loop skipped in short mode")
	}
	dir := t.TempDir()
	rng := rand.New(rand.NewSource(1))
	var lastTip uint32
	for round := 0; round < 8; round++ {
		cmd := exec.Command(os.Args[0], "-test.run=^TestUtxoStoreKillHelper$")
		cmd.Env = append(os.Environ(), "UTXO_KILL_DIR="+dir)
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Duration(300+rng.Intn(500)) * time.Millisecond)
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		s, err := openUtxoStore(dir)
		if err != nil {
			t.Fatalf("round %d reopen: %v", round, err)
		}
		tip, hash, ok, err := s.Tip()
		if err != nil {
			t.Fatal(err)
		}
		if ok && hash != utxoTestHash(tip) {
			t.Fatalf("round %d: tip hash does not match height %d", round, tip)
		}
		if tip < lastTip {
			t.Fatalf("round %d: tip went back from %d to %d", round, lastTip, tip)
		}
		if got := utxoTestAll(t, s); !reflect.DeepEqual(got, utxoTestExpected(tip)) {
			t.Fatalf("round %d: contents do not match tip %d", round, tip)
		}
		lastTip = tip
		_ = s.Close()
	}
	if lastTip == 0 {
		t.Fatal("helper never committed a block")
	}
	t.Logf("kill loop finished at tip %d", lastTip)
}

func TestUtxoStoreKillHelper(t *testing.T) {
	dir := os.Getenv("UTXO_KILL_DIR")
	if dir == "" {
		t.Skip("helper for TestUtxoStoreKillLoop")
	}
	s, err := openUtxoStore(dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	next := uint32(1)
	if tip, _, ok, _ := s.Tip(); ok {
		next = tip + 1
	}
	for ; ; next++ {
		if err := s.ApplyBlock(utxoTestBlock(next)); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(3)
		}
	}
}
