package main

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

func TestReadCompactSizeIsCanonical(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want uint64
		ok   bool
	}{
		{"00", 0, true}, {"fc", 0xfc, true}, {"fdfd00", 0xfd, true}, {"fdfc00", 0, false},
		{"fe00000100", 0x10000, true}, {"feffff0000", 0, false}, {"ff0000000001000000", 0x100000000, true},
		{"ffffffffff00000000", 0, false}, {"fd01", 0, false}, {"", 0, false},
	} {
		b, _ := hex.DecodeString(tc.in)
		n, _, ok := readCompactSize(b)
		if ok != tc.ok || ok && n != tc.want {
			t.Errorf("%s: got %d %v, want %d %v", tc.in, n, ok, tc.want, tc.ok)
		}
	}
}

func TestUnwrapCashTokenScriptFollowsBitcoinCashNode(t *testing.T) {
	p2pkh := "76a914" + strings.Repeat("ab", 20) + "88ac"
	category := strings.Repeat("11", 32)
	prefix := func(field string, rest string) string { return "ef" + category + field + rest + p2pkh }
	for _, tc := range []struct {
		name     string
		script   string
		stripped bool
	}{
		{"no prefix", p2pkh, false},
		{"fungible amount", prefix("10", "fd0004"), true},
		{"NFT without capability or commitment", prefix("20", ""), true},
		{"mutable NFT with commitment", prefix("61", "02beef"), true},
		{"minting NFT with commitment and amount", prefix("72", "02beef05"), true},
		{"reserved bit", prefix("90", "05"), false},
		{"capability 3", prefix("23", ""), false},
		{"neither NFT nor amount", prefix("40", "02beef"), false},
		{"capability without NFT", prefix("11", "05"), false},
		{"commitment without NFT", prefix("50", "02beef05"), false},
		{"empty commitment", prefix("60", "00"), false},
		{"zero amount", prefix("10", "00"), false},
		{"non-canonical amount", prefix("10", "fd0500"), false},
		{"amount over int64", prefix("10", "ff0000000000000080"), false},
		{"commitment longer than the script", "ef" + category + "60" + "50" + "beef", false},
		{"too short for a category", "ef0001" + p2pkh, false},
	} {
		b, _ := hex.DecodeString(tc.script)
		want := b
		if tc.stripped {
			want, _ = hex.DecodeString(p2pkh)
		}
		if got := unwrapCashTokenScript(b); !bytes.Equal(got, want) {
			t.Errorf("%s: got %x, want %x", tc.name, got, want)
		}
	}
	if got := unwrapCashTokenScript(mustHex("ef" + category + "10" + "05")); len(got) != 0 {
		t.Errorf("a prefix with nothing after it should leave an empty script, got %x", got)
	}
}

func TestParseCashBlockReadsTheRegtestGenesis(t *testing.T) {
	header := "01000000" + strings.Repeat("00", 32) + "3ba3edfd7a7b12b27ac72c3e67768f617fc81bc3888a51323a9fb8aa4b1e5e4a" + "dae5494d" + "ffff7f20" + "02000000"
	coinbase := "01000000010000000000000000000000000000000000000000000000000000000000000000ffffffff4d04ffff001d0104455468652054696d65732030332f4a616e2f32303039204368616e63656c6c6f72206f6e206272696e6b206f66207365636f6e64206261696c6f757420666f722062616e6b73ffffffff0100f2052a01000000434104678afdb0fe5548271967f1a67130b7105cd6a828e03909a67962e0ea1f61deb649f6bc3f4cef38c4f35504e51ec112de5c384df7ba0b8d578a4c702b6bf11d5fac00000000"
	raw := mustHex(header + "01" + coinbase)
	blk, err := parseRawBlock(raw, 1, utxoBitcoinCash)
	if err != nil {
		t.Fatal(err)
	}
	if want, _ := utxoRevHex("0f9188f13cb7b2c71f2a335e3a4fc328bf5beb436012afca590b1a11466e2206"); blk.Hash != want || blk.PrevHash != ([32]byte{}) {
		t.Fatalf("hash %x prev %x", blk.Hash, blk.PrevHash)
	}
	txid, _ := utxoRevHex("4a5e1e4baab89f3a32518a88c31bc87f618f76673e2cc77ab2127b7afdeda33b")
	if len(blk.Spends) != 0 || len(blk.Creates) != 1 || blk.Creates[0].Txid != txid || blk.Creates[0].Amount != 50_0000_0000 || len(blk.Creates[0].Script) != 67 {
		t.Fatalf("coinbase read as %+v", blk)
	}
	if genesis, _ := parseRawBlock(raw, 0, utxoBitcoinCash); len(genesis.Creates) != 0 {
		t.Fatal("the genesis coinbase was indexed")
	}
	for _, bad := range [][]byte{raw[:len(raw)-1], append(append([]byte{}, raw...), 0)} {
		if _, err := parseRawBlock(bad, 1, utxoBitcoinCash); err == nil {
			t.Fatalf("a malformed block of %d bytes parsed", len(bad))
		}
	}
	own := append([]byte{}, raw...)
	parsed, _ := parseRawBlock(own, 1, utxoBitcoinCash)
	clear(own)
	if string(parsed.Creates[0].Script) != string(blk.Creates[0].Script) {
		t.Fatal("a parsed script still points into the raw block")
	}
}

func TestRPCCallHexDecodesTheResultWithoutCopying(t *testing.T) {
	for _, tc := range []struct {
		body, want, err string
	}{
		{`{"result":"00ff10","error":null,"id":"dashboard"}`, "00ff10", ""},
		{`{"result":null,"error":{"code":-5,"message":"Block not found"},"id":"dashboard"}`, "", "Block not found"},
		{`{"result":null,"error":null,"id":"dashboard"}`, "", "no result"},
		{`{"result":"zz","error":null,"id":"dashboard"}`, "", "invalid byte"},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, tc.body) }))
		got, err := (&rpcClient{url: srv.URL, client: srv.Client()}).callHex(t.Context(), "getblock", []any{"h", 0})
		srv.Close()
		if tc.err == "" && (err != nil || hex.EncodeToString(got) != tc.want) || tc.err != "" && (err == nil || !strings.Contains(err.Error(), tc.err)) {
			t.Errorf("%s: got %x, %v", tc.body, got, err)
		}
	}
}

type cashTestOut struct {
	amount uint64
	script []byte
}

func appendCompactSize(b []byte, n uint64) []byte {
	switch {
	case n < 0xfd:
		return append(b, byte(n))
	case n <= 0xffff:
		return binary.LittleEndian.AppendUint16(append(b, 0xfd), uint16(n))
	case n <= 0xffffffff:
		return binary.LittleEndian.AppendUint32(append(b, 0xfe), uint32(n))
	}
	return binary.LittleEndian.AppendUint64(append(b, 0xff), n)
}

func cashTestTx(ins []utxoOutpoint, outs []cashTestOut) []byte {
	tx := binary.LittleEndian.AppendUint32(nil, 2)
	tx = appendCompactSize(tx, uint64(len(ins)))
	for _, in := range ins {
		tx = binary.LittleEndian.AppendUint32(append(tx, in.Txid[:]...), in.Vout)
		tx = binary.LittleEndian.AppendUint32(append(tx, 1, 0x51), 0xffffffff)
	}
	tx = appendCompactSize(tx, uint64(len(outs)))
	for _, o := range outs {
		tx = appendCompactSize(binary.LittleEndian.AppendUint64(tx, o.amount), uint64(len(o.script)))
		tx = append(tx, o.script...)
	}
	return binary.LittleEndian.AppendUint32(tx, 0)
}

func cashTestBlock(prev [32]byte, txs ...[]byte) []byte {
	return rawTestBlock(0x20000000, prev, nil, txs...)
}

func rawTestBlock(version uint32, prev [32]byte, auxpow []byte, txs ...[]byte) []byte {
	block := append(binary.LittleEndian.AppendUint32(nil, version), prev[:]...)
	block = append(append(block, make([]byte, 44)...), auxpow...)
	block = appendCompactSize(block, uint64(len(txs)))
	for _, tx := range txs {
		block = append(block, tx...)
	}
	return block
}

func TestParseCashBlockReadsSpendsCreatesAndTokenOutputs(t *testing.T) {
	p2pkh := mustHex("76a914" + strings.Repeat("11", 20) + "88ac")
	holder := mustHex("76a914" + strings.Repeat("22", 20) + "88ac")
	token := append(append(append([]byte{cashTokenPrefix}, bytes.Repeat([]byte{0x33}, 32)...), 0x10, 7), holder...)
	external := utxoOutpoint{Txid: [32]byte{0xaa}, Vout: 3}
	prev := [32]byte{0x44}
	coinbase := cashTestTx([]utxoOutpoint{{Vout: 0xffffffff}}, []cashTestOut{{50_0000_0000, p2pkh}})
	first := cashTestTx([]utxoOutpoint{external}, []cashTestOut{{1000, p2pkh}, {2000, holder}})
	firstID := doubleSHA256(first)
	second := cashTestTx([]utxoOutpoint{{Txid: firstID, Vout: 1}}, []cashTestOut{{500, p2pkh}, {1500, token}})
	secondID := doubleSHA256(second)
	raw := cashTestBlock(prev, coinbase, first, second)
	blk, err := parseRawBlock(raw, 7, utxoBitcoinCash)
	if err != nil {
		t.Fatal(err)
	}
	if blk.Height != 7 || blk.Hash != doubleSHA256(raw[:80]) || blk.PrevHash != prev {
		t.Fatalf("header read as height %d hash %x prev %x", blk.Height, blk.Hash, blk.PrevHash)
	}
	if len(blk.Spends) != 2 || blk.Spends[0].utxoOutpoint != external || blk.Spends[1].utxoOutpoint != (utxoOutpoint{firstID, 1}) {
		t.Fatalf("spends %+v", blk.Spends)
	}
	want := []utxoCreate{
		{utxoOutpoint{doubleSHA256(coinbase), 0}, p2pkh, 50_0000_0000},
		{utxoOutpoint{firstID, 0}, p2pkh, 1000},
		{utxoOutpoint{firstID, 1}, holder, 2000},
		{utxoOutpoint{secondID, 0}, p2pkh, 500},
		{utxoOutpoint{secondID, 1}, holder, 1500},
	}
	if len(blk.Creates) != len(want) {
		t.Fatalf("%d creates, want %d", len(blk.Creates), len(want))
	}
	for i, c := range blk.Creates {
		if c.utxoOutpoint != want[i].utxoOutpoint || c.Amount != want[i].Amount || !bytes.Equal(c.Script, want[i].Script) {
			t.Fatalf("create %d is %x:%d %d %x, want %x:%d %d %x", i, c.Txid, c.Vout, c.Amount, c.Script, want[i].Txid, want[i].Vout, want[i].Amount, want[i].Script)
		}
	}
	for n := range len(raw) {
		if _, err := parseRawBlock(raw[:n:n], 7, utxoBitcoinCash); !errors.Is(err, errUtxoBadBlock) {
			t.Fatalf("a block cut to %d of %d bytes gave %v", n, len(raw), err)
		}
	}
	inputs := cashTestTx([]utxoOutpoint{external}, nil)
	huge := binary.LittleEndian.AppendUint64(append(inputs[:len(inputs)-5], 1), 1)
	huge = append(binary.LittleEndian.AppendUint64(append(huge, 0xff), 1<<62), 0x51, 0, 0, 0, 0)
	if _, err := parseRawBlock(cashTestBlock(prev, coinbase, huge), 7, utxoBitcoinCash); !errors.Is(err, errUtxoBadBlock) {
		t.Fatalf("a script length past the end of the block gave %v", err)
	}
}

func TestParseCashBlockRefusesABlockOverTheCoinOperationCap(t *testing.T) {
	limit := utxoBlockOpsCap
	utxoBlockOpsCap = 6
	t.Cleanup(func() { utxoBlockOpsCap = limit })
	coinbase := cashTestTx([]utxoOutpoint{{Vout: 0xffffffff}}, []cashTestOut{{1, nil}})
	tiny := func(n int) []byte {
		return cashTestTx([]utxoOutpoint{{Txid: [32]byte{byte(n)}}}, make([]cashTestOut, n))
	}
	if blk, err := parseRawBlock(cashTestBlock([32]byte{}, coinbase, tiny(4)), 9, utxoBitcoinCash); err != nil || len(blk.Creates)+len(blk.Spends) != 6 {
		t.Fatalf("a block at the cap: %v", err)
	}
	_, err := parseRawBlock(cashTestBlock([32]byte{}, coinbase, tiny(4), tiny(2)), 9, utxoBitcoinCash)
	if !errors.Is(err, errUtxoBlockTooLarge) || !strings.Contains(err.Error(), "block 9 has 9 coin operations, more than the 6") || !strings.Contains(err.Error(), "#363") {
		t.Fatalf("a block of empty-script outputs over the cap gave %v", err)
	}
}

// A parent chain's coinbase in the witness serialization: marker and flag, one input, one output, one 32-byte witness item.
func witnessTestTx() []byte {
	tx := append(binary.LittleEndian.AppendUint32(nil, 1), 0, 1, 1)
	tx = binary.LittleEndian.AppendUint32(append(tx, make([]byte, 32)...), 0xffffffff)
	tx = binary.LittleEndian.AppendUint32(append(tx, 4, 1, 2, 3, 4), 0xffffffff)
	tx = append(binary.LittleEndian.AppendUint64(append(tx, 1), 5000), 1, 0x51)
	tx = append(append(tx, 1, 32), make([]byte, 32)...)
	return binary.LittleEndian.AppendUint32(tx, 0)
}

func auxPowTestProof(parentCoinbase []byte) []byte {
	proof := append(slices.Clone(parentCoinbase), bytes.Repeat([]byte{0xbb}, 32)...)
	proof = append(append(proof, 2), bytes.Repeat([]byte{0xcc}, 64)...)
	proof = append(binary.LittleEndian.AppendUint32(proof, 0), 1)
	proof = binary.LittleEndian.AppendUint32(append(proof, bytes.Repeat([]byte{0xdd}, 32)...), 0)
	return append(proof, bytes.Repeat([]byte{0xee}, 80)...)
}

func TestParseRawBlockReadsPlainAndMergeMinedDogecoinBlocks(t *testing.T) {
	p2pkh := mustHex("76a914" + strings.Repeat("11", 20) + "88ac")
	tokenLike := append(append([]byte{cashTokenPrefix}, bytes.Repeat([]byte{0x33}, 32)...), append([]byte{0x10, 7}, p2pkh...)...)
	external := utxoOutpoint{Txid: [32]byte{0xaa}, Vout: 3}
	coinbase := cashTestTx([]utxoOutpoint{{Vout: 0xffffffff}}, []cashTestOut{{10_000 * 100_000_000, p2pkh}})
	spend := cashTestTx([]utxoOutpoint{external}, []cashTestOut{{1000, tokenLike}})
	prev := [32]byte{0x44}
	check := func(name string, raw []byte) {
		t.Helper()
		blk, err := parseRawBlock(raw, 400, utxoDogecoin)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if blk.Hash != doubleSHA256(raw[:80]) || blk.PrevHash != prev || len(blk.Spends) != 1 || blk.Spends[0].utxoOutpoint != external || len(blk.Creates) != 2 ||
			blk.Creates[0].utxoOutpoint != (utxoOutpoint{doubleSHA256(coinbase), 0}) || blk.Creates[1].utxoOutpoint != (utxoOutpoint{doubleSHA256(spend), 0}) ||
			!bytes.Equal(blk.Creates[1].Script, tokenLike) || blk.Creates[1].Amount != 1000 {
			t.Fatalf("%s: read as %+v", name, blk)
		}
		for n := range len(raw) {
			if _, err := parseRawBlock(raw[:n:n], 400, utxoDogecoin); !errors.Is(err, errUtxoBadBlock) {
				t.Fatalf("%s cut to %d of %d bytes gave %v", name, n, len(raw), err)
			}
		}
	}
	check("plain block", rawTestBlock(0x00620004, prev, nil, coinbase, spend))
	for name, parent := range map[string][]byte{"legacy parent coinbase": coinbase, "witness parent coinbase": witnessTestTx()} {
		check("merge-mined block with a "+name, rawTestBlock(0x00620104, prev, auxPowTestProof(parent), coinbase, spend))
	}
	huge := auxPowTestProof(coinbase)
	huge[len(coinbase)+32] = 0xfe
	if _, err := parseRawBlock(rawTestBlock(0x00620104, prev, huge, coinbase), 400, utxoDogecoin); !errors.Is(err, errUtxoBadBlock) {
		t.Fatalf("a merkle branch count past the end of the block gave %v", err)
	}
	if _, err := parseRawBlock(rawTestBlock(0x00620104, prev, auxPowTestProof(coinbase), coinbase, spend), 400, utxoBitcoinCash); err == nil {
		t.Fatal("a chain without merge mining read past a proof")
	}
}
