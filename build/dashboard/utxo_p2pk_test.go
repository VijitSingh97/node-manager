package main

import (
	"context"
	"encoding/hex"
	"slices"
	"testing"
)

const (
	p2pkAddrGCompressed   = "1BgGZ9tcN4rm9KBzDn7KprQz87SZ26SAMH"
	p2pkAddrGUncompressed = "1EHNa6Q4Jz2uvNExL497mE43ikXhwF6kZm"
	p2pkKeyGCompressed    = "0279be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798"
	p2pkKeyGUncompressed  = "0479be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798483ada7726a3c4655da4fbfc0e1108a8fd17b448a68554199c47d08ffb10d4b8"
	p2pkKeyGenesis        = "04678afdb0fe5548271967f1a67130b7105cd6a828e03909a67962e0ea1f61deb649f6bc3f4cef38c4f35504e51ec112de5c384df7ba0b8d578a4c702b6bf11d5f"
)

func p2pkScript(t testing.TB, keyHex string) []byte {
	t.Helper()
	key, err := hex.DecodeString(keyHex)
	if err != nil {
		t.Fatal(err)
	}
	return append(append([]byte{byte(len(key))}, key...), 0xac)
}

func p2pkAddr(t testing.TB, addr string) []byte {
	t.Helper()
	script, err := addressScript(addr)
	if err != nil {
		t.Fatal(err)
	}
	return script
}

type p2pkModelCoin struct {
	addr   string
	amount uint64
}

type p2pkFixture struct {
	t       testing.TB
	pkh     map[string][]byte
	p2pk    map[string][]byte
	other   []byte
	model   map[utxoOutpoint]p2pkModelCoin
	addrs   []string
	pkhOf   map[string]string
	reverse bool
}

func newP2PKFixture(t testing.TB, reverse bool) *p2pkFixture {
	f := &p2pkFixture{t: t, pkh: map[string][]byte{}, p2pk: map[string][]byte{}, model: map[utxoOutpoint]p2pkModelCoin{}, reverse: reverse}
	keys := map[string]string{p2pkAddrGCompressed: p2pkKeyGCompressed, p2pkAddrGUncompressed: p2pkKeyGUncompressed, genesisAddress: p2pkKeyGenesis}
	for addr, key := range keys {
		f.addrs = append(f.addrs, addr)
		f.pkh[addr], f.p2pk[addr] = p2pkAddr(t, addr), p2pkScript(t, key)
	}
	slices.Sort(f.addrs)
	f.other = p2pkScript(t, "02"+"11223344556677889900aabbccddeeff11223344556677889900aabbccddeeff")
	return f
}

func (f *p2pkFixture) outpoint(txid uint32, vout uint32) utxoOutpoint {
	return utxoOutpoint{Txid: utxoTestTxid(txid), Vout: vout}
}

func (f *p2pkFixture) initialCreates() []utxoCreate {
	var out []utxoCreate
	vout := uint32(0)
	for i, addr := range f.addrs {
		amount := uint64(i+1) * 100
		for _, c := range []struct {
			script []byte
			amount uint64
		}{{f.pkh[addr], amount}, {f.p2pk[addr], amount * 50}} {
			op := f.outpoint(1, vout)
			vout++
			out = append(out, utxoCreate{utxoOutpoint: op, Script: c.script, Amount: c.amount})
			f.model[op] = p2pkModelCoin{addr, c.amount}
		}
	}
	op := f.outpoint(1, vout)
	out = append(out, utxoCreate{utxoOutpoint: op, Script: f.other, Amount: 7})
	f.model[op] = p2pkModelCoin{"", 7}
	return out
}

func (f *p2pkFixture) seed(s *utxoStore, creates []utxoCreate) {
	f.t.Helper()
	recs := make([]utxoRecord, len(creates))
	for i, c := range creates {
		recs[i] = utxoRecord{ScriptPubKey: c.Script, Txid: c.Txid, Vout: uint64(c.Vout), Amount: c.Amount, Height: 1}
	}
	parse := func(onHeader func(utxoHeader), fn func(utxoRecord) error) (utxoHeader, error) {
		hdr := utxoHeader{BaseHash: utxoTestHash(1), CoinsCount: uint64(len(recs))}
		onHeader(hdr)
		for _, r := range recs {
			if err := fn(r); err != nil {
				return hdr, err
			}
		}
		return hdr, nil
	}
	if _, err := ingestUtxoRecords(s, parse, func([32]byte) (uint32, error) { return 1, nil }, nil); err != nil {
		f.t.Fatal(err)
	}
}

func (f *p2pkFixture) check(s *utxoStore, label string) {
	f.t.Helper()
	for _, addr := range f.addrs {
		var want []uint64
		for _, c := range f.model {
			if c.addr == addr {
				want = append(want, c.amount)
			}
		}
		slices.Sort(want)
		for _, script := range [][]byte{f.pkh[addr], f.p2pk[addr]} {
			coins, err := s.Lookup([][]byte{script})
			if err != nil {
				f.t.Fatal(err)
			}
			var got []uint64
			seen := map[utxoOutpoint]bool{}
			for _, c := range coins {
				if seen[c.utxoOutpoint] {
					f.t.Fatalf("%s: outpoint %v returned twice for %s", label, c.utxoOutpoint, addr)
				}
				seen[c.utxoOutpoint] = true
				got = append(got, c.Amount)
			}
			slices.Sort(got)
			if !slices.Equal(got, want) {
				f.t.Fatalf("%s: %s lookup returned %v, want %v", label, addr, got, want)
			}
		}
		both, err := s.Lookup([][]byte{f.pkh[addr], f.p2pk[addr]})
		if err != nil || len(both) != len(want) {
			f.t.Fatalf("%s: a P2PKH and P2PK script of one key counted %d coins for %s, want %d (%v)", label, len(both), addr, len(want), err)
		}
	}
	var wantSats uint64
	for _, c := range f.model {
		wantSats += c.amount
	}
	coins, sats, err := s.Totals(context.Background())
	if err != nil || coins != uint64(len(f.model)) || sats != wantSats {
		f.t.Fatalf("%s: totals %d coins %d sats, want %d and %d (%v)", label, coins, sats, len(f.model), wantSats, err)
	}
}

func TestAddressLookupCountsP2PKOutputsOfTheSameKey(t *testing.T) {
	for _, tc := range []struct {
		name          string
		reverse, seed bool
	}{{"follow", false, false}, {"seed", false, true}, {"follow reverse", true, false}, {"seed reverse", true, true}} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			s, err := openUtxoStoreFS(dir, nil, tc.reverse)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = s.Close() })
			f := newP2PKFixture(t, tc.reverse)
			creates := f.initialCreates()
			if tc.seed {
				f.seed(s, creates)
			} else if err := s.ApplyBlock(utxoBlock{Height: 1, Hash: utxoTestHash(1), PrevHash: utxoTestHash(0), Creates: creates}); err != nil {
				t.Fatal(err)
			}
			f.check(s, "after the first block")

			cross, err := s.Lookup([][]byte{f.pkh[p2pkAddrGCompressed]})
			if err != nil || len(cross) != 2 {
				t.Fatalf("the compressed key address counts %d coins, want its P2PKH and its own P2PK only (%v)", len(cross), err)
			}

			next := utxoBlock{Height: 2, Hash: utxoTestHash(2), PrevHash: utxoTestHash(1)}
			for i, addr := range f.addrs {
				op := f.outpoint(2, uint32(i))
				next.Creates = append(next.Creates, utxoCreate{utxoOutpoint: op, Script: f.p2pk[addr], Amount: 9000 + uint64(i)})
				f.model[op] = p2pkModelCoin{addr, 9000 + uint64(i)}
				spent := f.outpoint(1, uint32(2*i+1))
				next.Spends = append(next.Spends, utxoSpend{utxoOutpoint: spent, Script: f.p2pk[addr]})
			}
			saved := cloneModel(f.model)
			for i := range f.addrs {
				delete(f.model, f.outpoint(1, uint32(2*i+1)))
			}
			if err := s.ApplyBlock(next); err != nil {
				t.Fatal(err)
			}
			f.check(s, "after creating and spending P2PK coins")

			if err := s.DisconnectTip(); err != nil {
				t.Fatal(err)
			}
			f.model = saved
			for i := range f.addrs {
				delete(f.model, f.outpoint(2, uint32(i)))
			}
			f.check(s, "after a reorg restored the spent coins")

			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			s2, err := openUtxoStoreFS(dir, nil, tc.reverse)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = s2.Close() })
			f.check(s2, "after a reopen")
		})
	}
}

func cloneModel(m map[utxoOutpoint]p2pkModelCoin) map[utxoOutpoint]p2pkModelCoin {
	out := make(map[utxoOutpoint]p2pkModelCoin, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
