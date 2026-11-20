package main

import (
	"bytes"
	"errors"
	"maps"
	"slices"
	"strings"
	"testing"
)

func utxoTestDump(t testing.TB, s *utxoStore) map[string]string {
	t.Helper()
	it, err := s.db.NewIter(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer it.Close()
	out := map[string]string{}
	for ok := it.First(); ok; ok = it.Next() {
		out[string(it.Key())] = string(it.Value())
	}
	return out
}

func utxoTestCoinRecords(dump map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range dump {
		if len(k) == utxoKeyLen || len(k) == utxoRevKeyLen && k[0] == utxoRevTag {
			out[k] = v
		}
	}
	return out
}

func utxoTestPieceKeys(dump map[string]string) int {
	n := 0
	for k := range dump {
		if len(k) == len(utxoPieceKey(0, 0)) && strings.HasPrefix(k, "undo") {
			n++
		}
	}
	return n
}

func withPieceOps(n int, f func()) {
	old := utxoBlockPieceOps
	utxoBlockPieceOps = n
	defer func() { utxoBlockPieceOps = old }()
	f()
}

func TestUtxoPiecedBlockMatchesWholeBlockAndUndoes(t *testing.T) {
	p2pkh := mustHex("76a914" + strings.Repeat("11", 20) + "88ac")
	holder := mustHex("76a914" + strings.Repeat("22", 20) + "88ac")
	token := slices.Concat([]byte{cashTokenPrefix}, bytes.Repeat([]byte{0x33}, 32), []byte{0x10, 7}, holder)
	parse := func(raw []byte, height uint32) utxoBlock {
		t.Helper()
		blk, err := parseRawBlock(raw, height, utxoBitcoinCash)
		if err != nil {
			t.Fatal(err)
		}
		return blk
	}
	coinbaseOf := func(tag byte) []byte {
		return cashTestTx([]utxoOutpoint{{Vout: 0xffffffff}}, []cashTestOut{{50_0000_0000, slices.Concat(p2pkh, []byte{tag})}, {1000, token}})
	}
	parentCoinbase := cashTestTx([]utxoOutpoint{{Vout: 0xffffffff}}, []cashTestOut{{50_0000_0000, p2pkh}, {1, holder}})
	parent := parse(cashTestBlock([32]byte{}, parentCoinbase), 1)
	// The spending transaction comes first, as canonical ordering allows, so a spend precedes the create it consumes.
	big := func(tag byte) utxoBlock {
		first := cashTestTx([]utxoOutpoint{{Txid: doubleSHA256(parentCoinbase)}}, []cashTestOut{{1000, p2pkh}, {2000, holder}, {0, []byte{0x6a, tag}}})
		second := cashTestTx([]utxoOutpoint{{Txid: doubleSHA256(first), Vout: 1}}, []cashTestOut{{1500, token}, {500, p2pkh}})
		return parse(cashTestBlock(parent.Hash, coinbaseOf(tag), second, first), 2)
	}
	child := func(tag byte, spends ...utxoOutpoint) utxoBlock {
		tx := cashTestTx(spends, []cashTestOut{{700, p2pkh}, {300, holder}})
		return parse(cashTestBlock(big(2).Hash, coinbaseOf(tag), tx), 3)
	}
	whole, pieced := utxoTestOpenReverse(t, t.TempDir()), utxoTestOpenReverse(t, t.TempDir())
	for _, s := range []*utxoStore{whole, pieced} {
		if err := s.ApplyBlock(parent); err != nil {
			t.Fatal(err)
		}
	}
	before := utxoTestDump(t, pieced)
	apply := func(blk utxoBlock) {
		t.Helper()
		if err := whole.ApplyBlock(blk); err != nil {
			t.Fatal(err)
		}
		withPieceOps(1, func() {
			if err := pieced.ApplyBlock(blk); err != nil {
				t.Fatal(err)
			}
		})
		got, want := utxoTestDump(t, pieced), utxoTestDump(t, whole)
		if !maps.Equal(utxoTestCoinRecords(got), utxoTestCoinRecords(want)) || got[string(utxoMetaKey)] != want[string(utxoMetaKey)] {
			t.Fatal("a block applied in pieces differs from the same block applied whole")
		}
		if n := utxoTestPieceKeys(got); n != len(blk.Creates)+len(blk.Spends)-1 {
			t.Fatalf("%d piece records, want one per piece before the last", n)
		}
	}

	apply(big(1))
	coins, err := pieced.Lookup([][]byte{holder})
	if err != nil {
		t.Fatal(err)
	}
	var sum uint64
	for _, c := range coins {
		sum += c.Amount
	}
	if len(coins) != 3 || sum != 2501 {
		t.Fatalf("the token holder has %d coins worth %d, want the parent's, the coinbase token and the token it was paid: 3 worth 2501", len(coins), sum)
	}
	if err := pieced.DisconnectTip(); err != nil {
		t.Fatal(err)
	}
	if err := whole.DisconnectTip(); err != nil {
		t.Fatal(err)
	}
	if !maps.Equal(utxoTestDump(t, pieced), before) {
		t.Fatal("undoing a block applied in pieces did not restore the store exactly")
	}
	apply(big(2))

	spendable := utxoOutpoint{Txid: doubleSHA256(coinbaseOf(2))}
	mid := utxoTestDump(t, pieced)
	withPieceOps(1, func() {
		if err := pieced.ApplyBlock(child(3, spendable, utxoOutpoint{Txid: [32]byte{0xee}})); !errors.Is(err, errUtxoMissingCoin) {
			t.Fatalf("a missing coin in the last piece gave %v", err)
		}
	})
	if !maps.Equal(utxoTestDump(t, pieced), mid) {
		t.Fatal("a block that failed in its last piece left earlier pieces behind")
	}

	commits := 0
	pieced.beforeCommit = func() error {
		if commits++; commits == 3 || commits == 4 {
			return errors.New("simulated failure")
		}
		return nil
	}
	next := child(4, spendable)
	withPieceOps(1, func() {
		if err := pieced.ApplyBlock(next); !errors.Is(err, errUtxoBroken) {
			t.Fatalf("a failed piece whose rollback also failed gave %v", err)
		}
		if _, _, _, err := pieced.Tip(); !errors.Is(err, errUtxoBroken) {
			t.Fatalf("the store kept serving with a partial block: %v", err)
		}
		reopened, err := openUtxoStoreFS(pieced.dir, nil, true)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = reopened.Close() })
		if !maps.Equal(utxoTestDump(t, reopened), mid) {
			t.Fatal("reopening did not roll back the partial block")
		}
		if err := reopened.ApplyBlock(next); err != nil {
			t.Fatalf("the block did not apply after the rollback: %v", err)
		}
	})
}
