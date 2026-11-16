package main

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"maps"
	"os"
	"strconv"
	"strings"
	"testing"
)

func litecoinBlock(t *testing.T, name string) utxoV3Block {
	t.Helper()
	raw, err := os.ReadFile("testdata/utxo_litecoin/" + name)
	if err != nil {
		t.Fatal(err)
	}
	var blk utxoV3Block
	if err := json.Unmarshal(raw, &blk); err != nil {
		t.Fatal(err)
	}
	return blk
}

func mustOutpoint(t *testing.T, s string) utxoOutpoint {
	t.Helper()
	txid, vout, _ := strings.Cut(s, ":")
	h, err := utxoRevHex(txid)
	n, verr := strconv.ParseUint(vout, 10, 32)
	if err != nil || verr != nil {
		t.Fatalf("outpoint %q: %v %v", s, err, verr)
	}
	return utxoOutpoint{Txid: h, Vout: uint32(n)}
}

func mustScript(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestLitecoinMWEBBlocksApplyThroughTheReverseKey(t *testing.T) {
	for _, tc := range []struct {
		file          string
		spent         []string
		gone, created []string
	}{
		{"block432-pegin.json",
			[]string{"d62a46d0996c5cceae51808cea8cfbe779cc20801e9cc0a60dd8e8c30a31b76d:1"},
			[]string{"59205a58097a1db91ae7c7246d9ee8493d7d67322bf7596c118076f7bbdd981ab6f9"},
			[]string{"5820fc14e6ddf83de8d8a50114f87622645620c5e96a01e060977ff72b9375349265", "001472e6065615fd20f6def5b4b28b38788b4f427cc7"}},
		{"block445-pegout.json",
			[]string{"3c54e36f9ec60b221366cd7ae3aa6af134b9771023c8c6fcd611b30e42ab38aa:0"},
			nil,
			[]string{"5820780bb5803c33f6acce552b6bb9e9cac6189eb77d4990c453b28c235a5097f637", "0014f7adffb319d10eaf712b84d6be901c43af8ba7cb"}},
	} {
		t.Run(tc.file, func(t *testing.T) {
			raw := litecoinBlock(t, tc.file)
			if _, err := convertUtxoBlock(raw, false); !errors.Is(err, errUtxoNoPrevout) {
				t.Fatalf("a block without prevouts converted for a chain with no reverse key: %v", err)
			}
			blk, err := convertUtxoBlock(raw, true)
			if err != nil {
				t.Fatal(err)
			}
			for _, sp := range blk.Spends {
				if sp.Script != nil {
					t.Fatalf("spend %x:%d carries a script", sp.Txid, sp.Vout)
				}
			}
			parent := utxoBlock{Height: blk.Height - 1, Hash: blk.PrevHash}
			for _, s := range tc.spent {
				parent.Creates = append(parent.Creates, utxoCreate{utxoOutpoint: mustOutpoint(t, s), Script: []byte{0x51}, Amount: 1})
			}
			s := utxoTestOpenReverse(t, t.TempDir())
			if err := s.ApplyBlock(parent); err != nil {
				t.Fatal(err)
			}
			if err := s.ApplyBlock(blk); err != nil {
				t.Fatal(err)
			}
			utxoTestKeys(t, s)
			if got, _ := s.Lookup([][]byte{{0x51}}); len(got) != 0 {
				t.Fatalf("spent coins are still indexed: %+v", got)
			}
			for _, script := range tc.gone {
				if got, _ := s.Lookup([][]byte{mustScript(t, script)}); len(got) != 0 {
					t.Fatalf("peg-in output spent by the HogEx in the same block is still indexed: %+v", got)
				}
			}
			for _, script := range tc.created {
				if got, _ := s.Lookup([][]byte{mustScript(t, script)}); len(got) != 1 {
					t.Fatalf("output %s indexed %d times, want 1", script, len(got))
				}
			}
		})
	}
}

func TestFollowerFollowsLitecoinBlocksWithoutPrevouts(t *testing.T) {
	c := newFakeUtxoChain()
	c.verbosity = 2
	genesis(c)
	c.add("a", []fakeOut{{"5152", "10.00000000"}}, nil)
	txA := fakeDisplayHash("txa", 1)
	pegin, hogex, pegout := "5920"+strings.Repeat("aa", 32), "5820"+strings.Repeat("bb", 32), "0014"+strings.Repeat("cc", 20)
	mweb := map[string]any{"ismweb": true, "output_id": strings.Repeat("dd", 32)}
	c.add("b", nil, nil)
	pegTx, hogTx := fakeDisplayHash("peg", 2), fakeDisplayHash("hog", 2)
	c.blocks[c.hashes[2]]["tx"] = []any{
		map[string]any{"txid": fakeDisplayHash("txb", 2), "vin": []any{map[string]any{"coinbase": "00"}}, "vout": []any{}},
		map[string]any{"txid": pegTx, "vin": []any{map[string]any{"ismweb": false, "txid": txA, "vout": 0}, mweb},
			"vout": []any{jsonNumberMap(fakeOut{pegin, "9.00000000"}, 0), map[string]any{"ismweb": true, "n": 1}}},
		map[string]any{"txid": hogTx, "vin": []any{map[string]any{"txid": pegTx, "vout": 0}},
			"vout": []any{jsonNumberMap(fakeOut{hogex, "9.00000000"}, 0)}},
	}
	s := utxoTestOpenReverse(t, t.TempDir())
	f := newUtxoFollower(s, c)
	f.chain = utxoLitecoin
	stepOK(t, f)
	balance := func(script string) map[string]uint64 { return lookupAmounts(t, s, script) }
	if len(balance("5152")) != 0 || len(balance(pegin)) != 0 || len(balance(hogex)) != 1 || len(balance("")) != 0 {
		t.Fatalf("after the peg-in: a %v, peg-in %v, HogEx %v, MWEB output %v", balance("5152"), balance(pegin), balance(hogex), balance(""))
	}
	atB := utxoTestKeys(t, s)

	c.add("c", nil, nil)
	c.blocks[c.hashes[3]]["tx"] = []any{
		map[string]any{"txid": fakeDisplayHash("txc", 3), "vin": []any{map[string]any{"coinbase": "00"}}, "vout": []any{}},
		map[string]any{"txid": fakeDisplayHash("hog", 3), "vin": []any{map[string]any{"txid": hogTx, "vout": 0}},
			"vout": []any{jsonNumberMap(fakeOut{hogex, "7.00000000"}, 0), jsonNumberMap(fakeOut{pegout, "2.00000000"}, 1)}},
	}
	stepOK(t, f)
	if got := balance(hogex); len(got) != 1 || got[fakeDisplayHash("hog", 3)+":0"] != 700_000_000 || len(balance(pegout)) != 1 {
		t.Fatalf("after the peg-out: HogEx %v, peg-out %v", got, balance(pegout))
	}

	c.hashes = c.hashes[:3]
	c.add("c2", []fakeOut{{"5153", "1.00000000"}}, nil)
	stepOK(t, f)
	if got := balance(hogex); len(got) != 1 || got[hogTx+":0"] != 900_000_000 || len(balance(pegout)) != 0 || len(balance("5153")) != 1 {
		t.Fatalf("after the reorg: HogEx %v, peg-out %v", got, balance(pegout))
	}
	if err := s.DisconnectTip(); err != nil {
		t.Fatal(err)
	}
	if !maps.Equal(utxoTestKeys(t, s), atB) {
		t.Fatal("the reorged block did not undo cleanly")
	}
}
