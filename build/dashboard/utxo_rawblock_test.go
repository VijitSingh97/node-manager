package main

import (
	"bytes"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
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
	blk, err := parseCashBlock(raw, 1)
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
	if genesis, _ := parseCashBlock(raw, 0); len(genesis.Creates) != 0 {
		t.Fatal("the genesis coinbase was indexed")
	}
	for _, bad := range [][]byte{raw[:len(raw)-1], append(append([]byte{}, raw...), 0)} {
		if _, err := parseCashBlock(bad, 1); err == nil {
			t.Fatalf("a malformed block of %d bytes parsed", len(bad))
		}
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
