package main

import (
	"encoding/hex"
	"slices"
	"testing"
)

func TestHash160MatchesKnownAddresses(t *testing.T) {
	for addr, key := range map[string]string{p2pkAddrGCompressed: p2pkKeyGCompressed, p2pkAddrGUncompressed: p2pkKeyGUncompressed, genesisAddress: p2pkKeyGenesis} {
		raw, _ := hex.DecodeString(key)
		h := utxoHash160(raw)
		if want := p2pkAddr(t, addr)[3:23]; !slices.Equal(h[:], want) {
			t.Fatalf("hash160 of %s is %x, want %x", key[:8], h, want)
		}
	}
	var zero [32]byte
	h := ripemd160Of32(zero)
	if got := hex.EncodeToString(h[:]); got != "d1a70126ff7a149ca6f9b638db084480440ff842" {
		t.Fatalf("ripemd160 of 32 zero bytes is %s", got)
	}
}

func TestP2PKTemplate(t *testing.T) {
	key33 := "02" + "11223344556677889900aabbccddeeff11223344556677889900aabbccddeeff"
	key65 := "04" + "11223344556677889900aabbccddeeff11223344556677889900aabbccddeeff" + "11223344556677889900aabbccddeeff11223344556677889900aabbccddeeff"
	mk := func(push byte, key string, tail ...byte) []byte {
		raw, _ := hex.DecodeString(key)
		return append(append([]byte{push}, raw...), tail...)
	}
	p2pkh := p2pkAddr(t, genesisAddress)
	cases := []struct {
		name   string
		script []byte
		ok     bool
	}{
		{"compressed 02", mk(33, key33, 0xac), true},
		{"compressed 03", mk(33, "03"+key33[2:], 0xac), true},
		{"uncompressed 04", mk(65, key65, 0xac), true},
		{"hybrid 06", mk(65, "06"+key65[2:], 0xac), true},
		{"hybrid 07", mk(65, "07"+key65[2:], 0xac), true},
		{"header 05", mk(65, "05"+key65[2:], 0xac), false},
		{"33 bytes with header 04", mk(33, "04"+key33[2:], 0xac), false},
		{"65 bytes with header 02", mk(65, "02"+key65[2:], 0xac), false},
		{"no checksig", mk(33, key33), false},
		{"extra byte", mk(33, key33, 0xac, 0x00), false},
		{"pushdata1", append([]byte{0x4c}, mk(33, key33, 0xac)...), false},
		{"p2pkh", p2pkh, false},
	}
	for _, tc := range cases {
		if _, ok := utxoP2PKKeyHash(tc.script); ok != tc.ok {
			t.Errorf("%s: matched=%v, want %v", tc.name, ok, tc.ok)
		}
	}
	if h, p2pk := utxoCanonHash(p2pkh); p2pk || h != utxoScriptHash(p2pkh) {
		t.Fatal("a P2PKH script must hash unchanged")
	}
	if allocs := testing.AllocsPerRun(100, func() { utxoCanonHash(p2pkh) }); allocs != 0 {
		t.Fatalf("a non-P2PK script allocates %v times", allocs)
	}
}

func p2pkOpenLegacy(t *testing.T, dir string, reverse bool) *utxoStore {
	t.Helper()
	s, err := openUtxoStoreFS(dir, nil, reverse)
	if err != nil {
		t.Fatal(err)
	}
	s.canon = false
	s.meta.Schema = s.baseSchema()
	return s
}

func TestLegacyStoreKeepsRawKeysAndFollows(t *testing.T) {
	dir := t.TempDir()
	s := p2pkOpenLegacy(t, dir, false)
	f := newP2PKFixture(t, false)
	if err := s.ApplyBlock(utxoBlock{Height: 1, Hash: utxoTestHash(1), PrevHash: utxoTestHash(0), Creates: f.initialCreates()}); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	s, err := openUtxoStoreFS(dir, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if !s.Legacy() || s.meta.Schema != utxoSchemaVersion {
		t.Fatalf("an old store with a tip must stay legacy, schema %d", s.meta.Schema)
	}
	got, err := s.Lookup([][]byte{f.pkh[genesisAddress]})
	if err != nil || len(got) != 1 {
		t.Fatalf("a legacy address lookup returned %v, %v", got, err)
	}
	if direct, err := s.Lookup([][]byte{f.p2pk[genesisAddress]}); err != nil || len(direct) != 1 {
		t.Fatalf("a legacy P2PK coin must stay under its own script: %v, %v", direct, err)
	}
	spend := utxoBlock{Height: 2, Hash: utxoTestHash(2), PrevHash: utxoTestHash(1),
		Spends: []utxoSpend{{utxoOutpoint: f.outpoint(1, 1), Script: f.p2pk[genesisAddress]}}}
	if err := s.ApplyBlock(spend); err != nil {
		t.Fatalf("a legacy store must follow a P2PK spend: %v", err)
	}
	if !s.Legacy() {
		t.Fatal("following must not change the keying")
	}
}

func TestOldBinaryRejectsCanonicalStore(t *testing.T) {
	dir := t.TempDir()
	s, err := openUtxoStoreFS(dir, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if s.meta.Schema != utxoSchemaVersion+utxoSchemaCanonical {
		t.Fatalf("a new store has schema %d", s.meta.Schema)
	}
	_ = s.Close()
}

func TestEmptyLegacyStoreIsPromoted(t *testing.T) {
	dir := t.TempDir()
	s, err := openUtxoStoreFS(dir, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	utxoMgrRewriteMeta(t, dir, func(m *utxoMeta) { m.Schema = utxoSchemaReverse })
	s, err = openUtxoStoreFS(dir, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.Legacy() || s.meta.Schema != utxoSchemaReverse+utxoSchemaCanonical || !s.Valid() {
		t.Fatalf("an empty old store reads schema %d valid=%v", s.meta.Schema, s.Valid())
	}
}

func TestTotalsReportP2PKCensus(t *testing.T) {
	s := utxoTestOpen(t, t.TempDir())
	f := newP2PKFixture(t, false)
	if err := s.ApplyBlock(utxoBlock{Height: 1, Hash: utxoTestHash(1), PrevHash: utxoTestHash(0), Creates: f.initialCreates()}); err != nil {
		t.Fatal(err)
	}
	tot, err := s.TotalsP2PK(t.Context())
	if err != nil || tot.Coins != 7 || tot.P2PKCoins != 4 {
		t.Fatalf("census %+v, %v", tot, err)
	}
}
