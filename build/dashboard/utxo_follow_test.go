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
	"time"
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

type hookedUtxoRPC struct {
	utxoRPC
	before func(method string)
}

func (h hookedUtxoRPC) call(ctx context.Context, method string, params any, out any) error {
	h.before(method)
	return h.utxoRPC.call(ctx, method, params, out)
}

func TestFollowerRetriesNonContiguousBlockWhenChainMoved(t *testing.T) {
	c := newFakeUtxoChain()
	genesis(c)
	c.add("a", nil, nil)
	c.add("b", nil, nil)
	prev := c.blocks[c.hashes[2]]["previousblockhash"]
	c.blocks[c.hashes[2]]["previousblockhash"] = fakeDisplayHash("elsewhere", 1)
	s := openFollowStore(t)
	f := newUtxoFollower(s, hookedUtxoRPC{c, func(method string) {
		if method == "getblock" && len(c.hashes) == 3 {
			c.add("c", nil, nil)
		}
	}})
	if err := f.Step(context.Background()); !errors.Is(err, errUtxoChainMoved) {
		t.Fatalf("err %v", err)
	}
	if st := f.Status(); st.State != utxoFollowRetry {
		t.Fatalf("status %+v", st)
	}
	c.blocks[c.hashes[2]]["previousblockhash"] = prev
	stepOK(t, f)
	if h, _, _, _ := s.Tip(); h != 3 {
		t.Fatalf("tip %d", h)
	}
}

func (c *fakeUtxoChain) forkAt(n int) { c.hashes = c.hashes[:n] }

func stepOK(t *testing.T, f *utxoFollower) {
	t.Helper()
	if err := f.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func sameIndex(t *testing.T, c *fakeUtxoChain, s *utxoStore, scripts ...string) {
	t.Helper()
	fresh := openFollowStore(t)
	stepOK(t, newUtxoFollower(fresh, c))
	for _, sc := range scripts {
		if got, want := lookupAmounts(t, s, sc), lookupAmounts(t, fresh, sc); fmt.Sprint(got) != fmt.Sprint(want) {
			t.Fatalf("script %s: got %v, fresh replay %v", sc, got, want)
		}
	}
	fh, ft, _, _ := fresh.Tip()
	if h, tp, _, _ := s.Tip(); h != fh || tp != ft {
		t.Fatalf("tip %d differs from fresh replay %d", h, fh)
	}
}

func TestFollowerReorgOneBlock(t *testing.T) {
	c := newFakeUtxoChain()
	genesis(c)
	c.add("a", []fakeOut{{"5152", "1.00000000"}}, nil)
	c.add("b", []fakeOut{{"5153", "2.00000000"}}, nil)
	s := openFollowStore(t)
	f := newUtxoFollower(s, c)
	stepOK(t, f)
	c.forkAt(2)
	c.add("f", []fakeOut{{"5154", "3.00000000"}}, nil)
	stepOK(t, f)
	if st := f.Status(); st.State != utxoFollowIdle || st.Error != "" {
		t.Fatalf("status %+v", st)
	}
	if len(lookupAmounts(t, s, "5153")) != 0 || len(lookupAmounts(t, s, "5154")) != 1 {
		t.Fatal("reorged outputs wrong")
	}
	sameIndex(t, c, s, "5152", "5153", "5154")
}

func TestFollowerReorgMultiBlockLongerChain(t *testing.T) {
	c := newFakeUtxoChain()
	genesis(c)
	for _, l := range []string{"a", "b", "c", "d"} {
		c.add(l, []fakeOut{{"51" + hex.EncodeToString([]byte(l)), "1.00000000"}}, nil)
	}
	s := openFollowStore(t)
	f := newUtxoFollower(s, c)
	stepOK(t, f)
	c.forkAt(2)
	for _, l := range []string{"x", "y", "z", "w"} {
		c.add(l, []fakeOut{{"52" + hex.EncodeToString([]byte(l)), "1.00000000"}}, nil)
	}
	stepOK(t, f)
	if h, _, _, _ := s.Tip(); h != 5 {
		t.Fatalf("tip %d", h)
	}
	sameIndex(t, c, s, "5161", "5162", "5163", "5164", "5278", "5279", "527a", "5277")
}

func TestFollowerReorgToShorterChain(t *testing.T) {
	c := newFakeUtxoChain()
	genesis(c)
	c.add("a", []fakeOut{{"5152", "1.00000000"}}, nil)
	c.add("b", []fakeOut{{"5153", "1.00000000"}}, nil)
	s := openFollowStore(t)
	f := newUtxoFollower(s, c)
	stepOK(t, f)
	c.forkAt(1)
	c.add("s", []fakeOut{{"5154", "1.00000000"}}, nil)
	stepOK(t, f)
	if h, _, _, _ := s.Tip(); h != 1 {
		t.Fatalf("tip %d", h)
	}
	sameIndex(t, c, s, "5152", "5153", "5154")
}

func TestFollowerReorgSpendsSameOutputsDifferently(t *testing.T) {
	c := newFakeUtxoChain()
	genesis(c)
	c.add("a", []fakeOut{{"5152", "1.00000000"}, {"5153", "2.00000000"}}, nil)
	txA := fakeDisplayHash("txa", 1)
	c.add("b", []fakeOut{{"5154", "0.90000000"}}, []fakeIn{{txA, 0, "5152"}})
	s := openFollowStore(t)
	f := newUtxoFollower(s, c)
	stepOK(t, f)
	if len(lookupAmounts(t, s, "5152")) != 0 {
		t.Fatal("spend not applied")
	}
	c.forkAt(2)
	c.add("b2", []fakeOut{{"5155", "1.90000000"}}, []fakeIn{{txA, 1, "5153"}})
	c.add("c2", nil, nil)
	stepOK(t, f)
	if len(lookupAmounts(t, s, "5152")) != 1 || len(lookupAmounts(t, s, "5153")) != 0 {
		t.Fatal("outputs not restored and respent")
	}
	sameIndex(t, c, s, "5152", "5153", "5154", "5155")
}

func TestFollowerReorgTooDeepForJournal(t *testing.T) {
	c := newFakeUtxoChain()
	genesis(c)
	for i := 0; i < utxoJournalDepth+6; i++ {
		c.add("a", nil, nil)
	}
	s := openFollowStore(t)
	f := newUtxoFollower(s, c)
	stepOK(t, f)
	c.forkAt(2)
	for i := 0; i < utxoJournalDepth+6; i++ {
		c.add("f", nil, nil)
	}
	err := f.Step(context.Background())
	if !errors.Is(err, errUtxoReorgTooDeep) {
		t.Fatalf("err %v", err)
	}
	if st := f.Status(); st.State != utxoFollowError || st.Error == "" {
		t.Fatalf("status %+v", st)
	}
	if err := f.Step(context.Background()); !errors.Is(err, errUtxoFollowStop) {
		t.Fatalf("latched follower stepped: %v", err)
	}
	if h, _, _, _ := s.Tip(); h != utxoJournalDepth+6 {
		t.Fatalf("store mutated, tip %d", h)
	}
}

func TestFollowerWaitsWhileNodeIsFarBehind(t *testing.T) {
	c := newFakeUtxoChain()
	genesis(c)
	for i := 0; i < utxoJournalDepth+60; i++ {
		c.add("a", nil, nil)
	}
	s := openFollowStore(t)
	f := newUtxoFollower(s, c)
	stepOK(t, f)
	full := c.hashes
	c.hashes = full[:20]
	if err := f.Step(context.Background()); !errors.Is(err, errUtxoNodeBehind) {
		t.Fatalf("err %v", err)
	}
	if st := f.Status(); st.State != utxoFollowRetry {
		t.Fatalf("status %+v", st)
	}
	if h, _, _, _ := s.Tip(); h != utxoJournalDepth+60 {
		t.Fatalf("store mutated, tip %d", h)
	}
	c.hashes = full
	c.add("a", nil, nil)
	stepOK(t, f)
	if h, _, _, _ := s.Tip(); h != utxoJournalDepth+61 {
		t.Fatalf("tip %d after the node caught up", h)
	}
}

func snapshotBaseFollower(t *testing.T, c *fakeUtxoChain, base int) (*utxoStore, *utxoFollower) {
	t.Helper()
	s := openFollowStore(t)
	h, err := utxoRevHex(c.hashes[base])
	if err != nil {
		t.Fatal(err)
	}
	if err := s.IngestFiles(nil, uint32(base), h); err != nil {
		t.Fatal(err)
	}
	return s, newUtxoFollower(s, c)
}

func TestFollowerReorgBelowSnapshotBase(t *testing.T) {
	c := newFakeUtxoChain()
	genesis(c)
	for i := 0; i < 6; i++ {
		c.add("a", nil, nil)
	}
	s, f := snapshotBaseFollower(t, c, 3)
	stepOK(t, f)
	c.forkAt(3)
	for i := 0; i < 4; i++ {
		c.add("f", nil, nil)
	}
	if err := f.Step(context.Background()); !errors.Is(err, errUtxoReorgTooDeep) {
		t.Fatalf("err %v", err)
	}
	if f.Status().State != utxoFollowError {
		t.Fatalf("status %+v", f.Status())
	}
	if h, _, _, _ := s.Tip(); h != 6 {
		t.Fatalf("tip %d", h)
	}
}

func TestFollowerReorgAtSnapshotBaseSucceeds(t *testing.T) {
	c := newFakeUtxoChain()
	genesis(c)
	for i := 0; i < 5; i++ {
		c.add("a", nil, nil)
	}
	s, f := snapshotBaseFollower(t, c, 3)
	stepOK(t, f)
	c.forkAt(4)
	for i := 0; i < 3; i++ {
		c.add("f", nil, nil)
	}
	stepOK(t, f)
	if h, _, _, _ := s.Tip(); h != 6 {
		t.Fatalf("tip %d", h)
	}
}

func TestFollowerRollingBackState(t *testing.T) {
	c := newFakeUtxoChain()
	genesis(c)
	c.add("a", nil, nil)
	s := openFollowStore(t)
	f := newUtxoFollower(s, c)
	stepOK(t, f)
	c.forkAt(1)
	c.add("f", nil, nil)
	var seen bool
	s.beforeCommit = func() error {
		seen = seen || f.Status().State == utxoFollowRollback
		return nil
	}
	stepOK(t, f)
	if !seen {
		t.Fatal("never reported rolling_back")
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

type flakyUtxoRPC struct {
	utxoRPC
	failNext int
}

func (r *flakyUtxoRPC) call(ctx context.Context, method string, params any, out any) error {
	if r.failNext > 0 {
		r.failNext--
		return errors.New("rpc: connection refused")
	}
	return r.utxoRPC.call(ctx, method, params, out)
}

func TestFollowerRecoversFromTransientRPCError(t *testing.T) {
	c := newFakeUtxoChain()
	genesis(c)
	c.add("p", []fakeOut{{"5152", "1.00000000"}}, nil)
	s := openFollowStore(t)
	r := &flakyUtxoRPC{utxoRPC: c, failNext: 1}
	f := newUtxoFollower(s, r)
	if err := f.Step(context.Background()); err == nil {
		t.Fatal("expected the transient error")
	}
	if st := f.Status(); st.State != utxoFollowRetry || st.Error == "" {
		t.Fatalf("status %+v", st)
	}
	if err := f.Step(context.Background()); err != nil {
		t.Fatalf("did not resume: %v", err)
	}
	if st := f.Status(); st.State != utxoFollowIdle || st.Error != "" {
		t.Fatalf("status %+v", st)
	}
	if h, _, _, _ := s.Tip(); h != 1 {
		t.Fatalf("tip %d", h)
	}
}

func TestFollowerRunSurvivesTransientErrorAndStopsOnIntegrity(t *testing.T) {
	c := newFakeUtxoChain()
	genesis(c)
	c.add("p", nil, []fakeIn{{fakeDisplayHash("nothing", 1), 0, "5152"}})
	s := openFollowStore(t)
	f := newUtxoFollower(s, &flakyUtxoRPC{utxoRPC: c, failNext: 2})
	err := f.Run(context.Background(), time.Millisecond)
	if !errors.Is(err, errUtxoMissingCoin) {
		t.Fatalf("run returned %v", err)
	}
	if f.Status().State != utxoFollowError {
		t.Fatalf("status %+v", f.Status())
	}
}
