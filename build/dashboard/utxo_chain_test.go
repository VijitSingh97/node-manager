package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"
)

const litecoinTestTotal = 1293750000000

type recordUtxoRPC struct {
	utxoRPC
	getblock []string
}

func (r *recordUtxoRPC) call(ctx context.Context, method string, params any, out any) error {
	if method == "getblock" {
		raw, _ := json.Marshal(params)
		r.getblock = append(r.getblock, string(raw))
	}
	return r.utxoRPC.call(ctx, method, params, out)
}

func marshalParams(t *testing.T, params []any) string {
	t.Helper()
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestUtxoChainParamsKeepBitcoinRequestsByteIdentical(t *testing.T) {
	regtest := [4]byte{0xfa, 0xbf, 0xb5, 0xda}
	for _, tc := range []struct {
		name                           string
		chain                          *utxoChainParams
		networks                       map[string][4]byte
		legacy, reverse                bool
		dump, wait, waitTip, verbosity string
		descriptors                    []string
	}{
		{"bitcoin", utxoBitcoin, map[string][4]byte{"main": {0xf9, 0xbe, 0xb4, 0xd9}, "regtest": regtest}, false, false,
			`["utxo-seed.dat","latest"]`, `[60000]`, `[60000,"aa"]`, "3", []string{"addr(", "pkh(", "wpkh(", "sh(wpkh(", "wsh(", "tr("}},
		{"litecoin", utxoLitecoin, map[string][4]byte{"main": {0xfb, 0xc0, 0xb6, 0xdb}, "regtest": regtest}, true, true,
			`["utxo-seed.dat"]`, `[60000]`, `[60000]`, "2", []string{"addr(", "pkh(", "wpkh(", "sh(wpkh(", "wsh("}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if !reflect.DeepEqual(tc.chain.Networks, tc.networks) || tc.chain.LegacyDump != tc.legacy || tc.chain.ReverseIndex != tc.reverse {
				t.Fatalf("networks %x legacy %v reverse %v", tc.chain.Networks, tc.chain.LegacyDump, tc.chain.ReverseIndex)
			}
			if got := marshalParams(t, tc.chain.dumpArgs(seedDumpName)); got != tc.dump {
				t.Errorf("dumptxoutset params %s, want %s", got, tc.dump)
			}
			if got := marshalParams(t, tc.chain.waitArgs("")); got != tc.wait {
				t.Errorf("waitfornewblock params %s, want %s", got, tc.wait)
			}
			if got := marshalParams(t, tc.chain.waitArgs("aa")); got != tc.waitTip {
				t.Errorf("waitfornewblock params with a tip %s, want %s", got, tc.waitTip)
			}
			if !reflect.DeepEqual(tc.chain.Descriptors, tc.descriptors) || tc.chain.descriptorSupported(" tr(K)") != (tc.chain == utxoBitcoin) {
				t.Errorf("descriptors %q", tc.chain.Descriptors)
			}
			c := newFakeUtxoChain()
			genesis(c)
			rec := &recordUtxoRPC{utxoRPC: c}
			f := newUtxoFollower(openFollowStore(t), rec)
			f.chain = tc.chain
			_ = f.Step(context.Background())
			if want := fmt.Sprintf(`[%q,%s]`, c.hashes[0], tc.verbosity); len(rec.getblock) == 0 || rec.getblock[0] != want {
				t.Errorf("getblock params %q, want %s", rec.getblock, want)
			}
		})
	}
	if m := utxoMgrNew(t, t.TempDir(), nil, newFakeUtxoChain(), io.Discard); m.chain != utxoBitcoin || m.follower.chain != utxoBitcoin || m.store.reverse {
		t.Fatal("the manager does not default to the Bitcoin parameters")
	}
}

type litecoinTxOutSetInfo struct {
	Height    uint32    `json:"height"`
	BestBlock string    `json:"bestblock"`
	TxOuts    uint64    `json:"txouts"`
	Total     rpcAmount `json:"total_amount"`
}

func litecoinFixture(t *testing.T) ([]byte, litecoinTxOutSetInfo, [32]byte) {
	t.Helper()
	raw, err := os.ReadFile("testdata/utxo_litecoin/dump.dat")
	if err != nil {
		t.Fatal(err)
	}
	infoRaw, err := os.ReadFile("testdata/utxo_litecoin/gettxoutsetinfo.json")
	if err != nil {
		t.Fatal(err)
	}
	var info litecoinTxOutSetInfo
	if err := json.Unmarshal(infoRaw, &info); err != nil {
		t.Fatal(err)
	}
	base, err := utxoRevHex(info.BestBlock)
	if err != nil {
		t.Fatal(err)
	}
	return raw, info, base
}

func TestParseLitecoinRegtestDumpMatchesTheNode(t *testing.T) {
	raw, info, base := litecoinFixture(t)
	baseCoinbase, _ := utxoRevHex("18b64a52615cd366f746bd61c957a9ef2aa3d14b1b49ff18590b2e5590e7ef7c")
	var coins, total, maxVout uint64
	var sawBaseCoinbase bool
	h, err := utxoLitecoin.parseDump(bytes.NewReader(raw), [4]byte{}, nil, func(r utxoRecord) error {
		coins++
		total += r.Amount
		maxVout = max(maxVout, r.Vout)
		if r.Txid == baseCoinbase && r.Vout == 0 {
			sawBaseCoinbase = r.Amount == 1250000000 && r.Height == info.Height && r.Coinbase &&
				hex.EncodeToString(r.ScriptPubKey) == "0014119b8080c9f7f59269fa32511051d1c980765840"
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if coins != 449 || h.CoinsCount != coins || info.TxOuts != coins {
		t.Fatalf("parsed %d coins, header %d, gettxoutsetinfo %d; want 449", coins, h.CoinsCount, info.TxOuts)
	}
	if total != litecoinTestTotal || uint64(info.Total) != total {
		t.Fatalf("total %d, gettxoutsetinfo %d; want %d", total, info.Total, uint64(litecoinTestTotal))
	}
	if !sawBaseCoinbase || maxVout != 1 {
		t.Fatalf("outpoints decoded wrongly: base block coinbase found %v, largest vout %d", sawBaseCoinbase, maxVout)
	}
	if h.BaseHash != base {
		t.Fatalf("base hash %x is not the node's best block %s", h.BaseHash, info.BestBlock)
	}
	if _, err := parseUtxoDump(bytes.NewReader(raw), utxoNetRegtest, func(utxoRecord) error { return nil }); err == nil || !strings.Contains(err.Error(), "bad magic") {
		t.Fatalf("the v2 reader accepted a Litecoin dump: %v", err)
	}
}

func TestParseLegacyUtxoDumpRejectsMalformedInput(t *testing.T) {
	valid, _, _ := litecoinFixture(t)
	withCount := func(n uint64) []byte {
		d := bytes.Clone(valid)
		binary.LittleEndian.PutUint64(d[32:40], n)
		return d
	}
	for _, tc := range []struct {
		name string
		data []byte
		want string
	}{
		{"genesis only", withCount(0)[:44], ""},
		{"empty", nil, "header"},
		{"short header", valid[:43], "header"},
		{"truncated outpoint", valid[:44+20], "coin 1 of 449: unexpected EOF"},
		{"truncated body", valid[:len(valid)-1], "coin 449 of 449: unexpected EOF"},
		{"count too high", withCount(450), "coin 450 of 450: unexpected EOF"},
		{"count too low", withCount(448), "more data than the 448 coins"},
		{"trailing data", append(bytes.Clone(valid), 0), "more data than the 449 coins"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseLegacyUtxoDump(bytes.NewReader(tc.data), nil, func(utxoRecord) error { return nil })
			if tc.want == "" && err != nil || tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)) {
				t.Fatalf("error %v, want %q", err, tc.want)
			}
		})
	}
}

func TestIngestLitecoinDumpSeedsTheIndex(t *testing.T) {
	raw, info, base := litecoinFixture(t)
	var scripts [][]byte
	if _, err := parseLegacyUtxoDump(bytes.NewReader(raw), nil, func(r utxoRecord) error {
		scripts = append(scripts, bytes.Clone(r.ScriptPubKey))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	s := openFollowStore(t)
	if _, err := ingestUtxoSnapshot(s, bytes.NewReader(raw), utxoLitecoin, [4]byte{}, func(hash [32]byte) (uint32, error) {
		if hash != base {
			return 0, errSeedMismatch
		}
		return info.Height, nil
	}, nil); err != nil {
		t.Fatal(err)
	}
	if h, tip, ok, err := s.Tip(); err != nil || !ok || h != info.Height || tip != base {
		t.Fatalf("tip %d %x %v %v", h, tip, ok, err)
	}
	coins, err := s.Lookup(scripts)
	if err != nil {
		t.Fatal(err)
	}
	var total uint64
	for _, c := range coins {
		total += c.Amount
	}
	if len(coins) != 449 || total != litecoinTestTotal {
		t.Fatalf("index holds %d coins worth %d, want 449 worth %d", len(coins), total, uint64(litecoinTestTotal))
	}
}
