package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
)

type fakeUtxoChain struct {
	hashes []string
	blocks map[string]map[string]any
	calls  map[string]int
}

func fakeDisplayHash(label string, h int) string {
	sum := sha256.Sum256(binary.BigEndian.AppendUint32([]byte(label), uint32(h)))
	return hex.EncodeToString(sum[:])
}

func newFakeUtxoChain() *fakeUtxoChain {
	return &fakeUtxoChain{blocks: map[string]map[string]any{}, calls: map[string]int{}}
}

type fakeOut struct {
	script string
	value  string
}

type fakeIn struct {
	txid   string
	vout   int
	script string
}

func (c *fakeUtxoChain) add(label string, outs []fakeOut, ins []fakeIn) string {
	h := len(c.hashes)
	hash := fakeDisplayHash(label, h)
	var vins []any
	vins = append(vins, map[string]any{"coinbase": "00"})
	for _, in := range ins {
		v := map[string]any{"txid": in.txid, "vout": in.vout}
		if in.script != "-" {
			v["prevout"] = map[string]any{"scriptPubKey": map[string]any{"hex": in.script}}
		}
		vins = append(vins, v)
	}
	var vouts []any
	for i, o := range outs {
		vouts = append(vouts, jsonNumberMap(o, i))
	}
	blk := map[string]any{
		"hash": hash, "height": h,
		"tx": []any{map[string]any{"txid": fakeDisplayHash("tx"+label, h), "vin": vins, "vout": vouts}},
	}
	if h > 0 {
		blk["previousblockhash"] = c.hashes[h-1]
	}
	c.hashes = append(c.hashes, hash)
	c.blocks[hash] = blk
	return hash
}

func jsonNumberMap(o fakeOut, n int) map[string]any {
	return map[string]any{"value": json.Number(o.value), "n": n, "scriptPubKey": map[string]any{"hex": o.script}}
}

func (c *fakeUtxoChain) call(_ context.Context, method string, params any, out any) error {
	c.calls[method]++
	var res any
	args, _ := params.([]any)
	switch method {
	case "getbestblockhash":
		res = c.hashes[len(c.hashes)-1]
	case "getblockheader":
		for i, h := range c.hashes {
			if h == args[0] {
				res = map[string]any{"height": i}
			}
		}
	case "getblockhash":
		res = c.hashes[args[0].(uint32)]
	case "getblock":
		b, ok := c.blocks[args[0].(string)]
		if !ok || args[1] != 3 {
			return errors.New("fake: no such block")
		}
		res = b
	}
	raw, err := json.Marshal(res)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}

func openFollowStore(t *testing.T) *utxoStore {
	t.Helper()
	s, err := openUtxoStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func lookupAmounts(t *testing.T, s *utxoStore, script string) map[string]uint64 {
	t.Helper()
	b, _ := hex.DecodeString(script)
	coins, err := s.Lookup([][]byte{b})
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]uint64{}
	for _, c := range coins {
		var rev [32]byte
		for i, x := range c.Txid {
			rev[31-i] = x
		}
		out[fmt.Sprintf("%x:%d", rev, c.Vout)] = c.Amount
	}
	return out
}

func genesis(c *fakeUtxoChain) {
	c.add("g", []fakeOut{{"51", "50.00000000"}}, nil)
}

func TestFollowerCatchUpAndSameBlockSpend(t *testing.T) {
	c := newFakeUtxoChain()
	genesis(c)
	c.add("b", []fakeOut{{"5152", "1.00000001"}, {"5153", "0.00000546"}}, nil)
	txB := fakeDisplayHash("txb", 1)
	s := openFollowStore(t)
	f := newUtxoFollower(s, c)
	if err := f.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	if h, _, ok, _ := s.Tip(); !ok || h != 1 {
		t.Fatalf("tip %d %v", h, ok)
	}
	if got := lookupAmounts(t, s, "51"); len(got) != 0 {
		t.Fatalf("genesis output indexed: %v", got)
	}

	// Block 2 creates txid X output 0 and spends it in the same block; also spends block 1 output 0.
	c.add("c", []fakeOut{{"5154", "2.50000000"}}, nil)
	x := fakeDisplayHash("txc", 2)
	c.blocks[c.hashes[2]]["tx"] = []any{
		map[string]any{"txid": x, "vin": []any{map[string]any{"coinbase": "00"}},
			"vout": []any{jsonNumberMap(fakeOut{"5154", "2.50000000"}, 0)}},
		map[string]any{"txid": fakeDisplayHash("txd", 2), "vin": []any{
			map[string]any{"txid": x, "vout": 0, "prevout": map[string]any{"scriptPubKey": map[string]any{"hex": "5154"}}},
			map[string]any{"txid": txB, "vout": 0, "prevout": map[string]any{"scriptPubKey": map[string]any{"hex": "5152"}}},
		}, "vout": []any{jsonNumberMap(fakeOut{"5155", "3.49999999"}, 0)}},
	}
	c.add("d", nil, nil)
	if err := f.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	if h, _, _, _ := s.Tip(); h != 3 {
		t.Fatalf("tip %d", h)
	}
	if got := lookupAmounts(t, s, "5154"); len(got) != 0 {
		t.Fatalf("same-block output survived: %v", got)
	}
	if got := lookupAmounts(t, s, "5152"); len(got) != 0 {
		t.Fatalf("spent coin survived: %v", got)
	}
	if got := lookupAmounts(t, s, "5155"); got[fakeDisplayHash("txd", 2)+":0"] != 349999999 {
		t.Fatalf("created coin: %v", got)
	}
	if got := lookupAmounts(t, s, "5153"); got[txB+":1"] != 546 {
		t.Fatalf("untouched coin: %v", got)
	}
	if st := f.Status(); st.State != utxoFollowIdle || st.Blocks != 4 {
		t.Fatalf("status %+v", st)
	}
	before := c.calls["getblock"]
	if err := f.Step(context.Background()); err != nil || c.calls["getblock"] != before {
		t.Fatalf("idle poll fetched blocks: %v", err)
	}
}

func TestFollowerUnspendableOutputs(t *testing.T) {
	c := newFakeUtxoChain()
	genesis(c)
	long := func(n int) string { return hex.EncodeToString(bytes.Repeat([]byte{0x51}, n)) }
	c.add("u", []fakeOut{
		{"6a0b68656c6c6f", "0.00000000"},
		{"6a", "0.00000000"},
		{long(10000), "0.00000001"},
		{long(10001), "0.00000002"},
		{"", "0.00000003"},
		{"516a", "0.00000004"},
	}, nil)
	s := openFollowStore(t)
	if err := newUtxoFollower(s, c).Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	for script, want := range map[string]int{"6a0b68656c6c6f": 0, "6a": 0, long(10000): 1, long(10001): 0, "": 1, "516a": 1} {
		if got := len(lookupAmounts(t, s, script)); got != want {
			t.Errorf("script len %d: %d coins, want %d", len(script)/2, got, want)
		}
	}
}

func TestFollowerStopsOnMissingPrevout(t *testing.T) {
	c := newFakeUtxoChain()
	genesis(c)
	c.add("p", []fakeOut{{"5152", "1.00000000"}}, nil)
	c.add("q", nil, []fakeIn{{fakeDisplayHash("txp", 1), 0, "-"}})
	s := openFollowStore(t)
	f := newUtxoFollower(s, c)
	err := f.Step(context.Background())
	if !errors.Is(err, errUtxoNoPrevout) {
		t.Fatalf("err %v", err)
	}
	if h, _, _, _ := s.Tip(); h != 1 {
		t.Fatalf("tip advanced past the bad block: %d", h)
	}
	if st := f.Status(); st.State != utxoFollowError || st.Error == "" {
		t.Fatalf("status %+v", st)
	}
	if err := f.Step(context.Background()); !errors.Is(err, errUtxoFollowStop) {
		t.Fatalf("stopped follower stepped again: %v", err)
	}
}

func TestFollowerStopsOnMissingCoin(t *testing.T) {
	c := newFakeUtxoChain()
	genesis(c)
	c.add("p", nil, []fakeIn{{fakeDisplayHash("nothing", 1), 0, "5152"}})
	s := openFollowStore(t)
	f := newUtxoFollower(s, c)
	if err := f.Step(context.Background()); !errors.Is(err, errUtxoMissingCoin) {
		t.Fatalf("err %v", err)
	}
	if h, _, _, _ := s.Tip(); h != 0 {
		t.Fatalf("tip %d", h)
	}
	if f.Status().State != utxoFollowError {
		t.Fatalf("status %+v", f.Status())
	}
}

func TestFollowerStopsOnNonContiguousBlock(t *testing.T) {
	c := newFakeUtxoChain()
	genesis(c)
	c.add("a", nil, nil)
	c.add("b", nil, nil)
	c.blocks[c.hashes[2]]["previousblockhash"] = fakeDisplayHash("elsewhere", 1)
	s := openFollowStore(t)
	f := newUtxoFollower(s, c)
	if err := f.Step(context.Background()); !errors.Is(err, errUtxoNotContiguous) {
		t.Fatalf("err %v", err)
	}
	if h, _, _, _ := s.Tip(); h != 1 {
		t.Fatalf("tip %d", h)
	}
	if f.Status().State != utxoFollowError {
		t.Fatalf("status %+v", f.Status())
	}
}

func TestFollowerReorgDetected(t *testing.T) {
	c := newFakeUtxoChain()
	genesis(c)
	c.add("a", nil, nil)
	s := openFollowStore(t)
	f := newUtxoFollower(s, c)
	if err := f.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	c.hashes[1] = fakeDisplayHash("fork", 1)
	c.add("f", nil, nil)
	before := c.calls["getblock"]
	if err := f.Step(context.Background()); !errors.Is(err, errUtxoReorg) {
		t.Fatalf("err %v", err)
	}
	if c.calls["getblock"] != before {
		t.Fatal("fetched blocks on a reorged chain")
	}
	if f.Status().State != utxoFollowReorg {
		t.Fatalf("status %+v", f.Status())
	}
	if h, _, _, _ := s.Tip(); h != 1 {
		t.Fatalf("store changed: %d", h)
	}
}

func TestFollowerReorgToShorterChain(t *testing.T) {
	c := newFakeUtxoChain()
	genesis(c)
	c.add("a", nil, nil)
	c.add("b", nil, nil)
	s := openFollowStore(t)
	f := newUtxoFollower(s, c)
	if err := f.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	c.hashes = append(c.hashes[:1], fakeDisplayHash("short", 1))
	c.blocks[c.hashes[1]] = map[string]any{}
	if err := f.Step(context.Background()); !errors.Is(err, errUtxoReorg) {
		t.Fatalf("err %v", err)
	}
}

func TestRPCAmount(t *testing.T) {
	for in, want := range map[string]uint64{"0": 0, "50.00000000": 5000000000, "0.00000001": 1, "20999999.97690000": 2099999997690000, "1.5": 150000000} {
		var a rpcAmount
		if err := json.Unmarshal([]byte(in), &a); err != nil || uint64(a) != want {
			t.Errorf("%s -> %d, %v", in, a, err)
		}
	}
	for _, in := range []string{"1e-05", "-1", "0.123456789", ".5", "99999999999999999999"} {
		var a rpcAmount
		if json.Unmarshal([]byte(in), &a) == nil {
			t.Errorf("%s accepted", in)
		}
	}
}
