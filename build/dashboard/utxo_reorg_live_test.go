package main

import (
	"context"
	"encoding/json"
	"testing"
)

func (n *liveNode) script(addr string) string {
	var info struct {
		ScriptPubKey string `json:"scriptPubKey"`
	}
	if err := json.Unmarshal(n.do("getaddressinfo", addr), &info); err != nil {
		n.t.Fatal(err)
	}
	return info.ScriptPubKey
}

func (n *liveNode) invalidate(hash string) {
	var out json.RawMessage
	err := n.rpc.call(context.Background(), "invalidateblock", []any{hash}, &out)
	if err != nil && err.Error() != "RPC invalidateblock returned no result" {
		n.t.Fatal(err)
	}
}

func TestUtxoFollowerRegtestReorg(t *testing.T) {
	n := startLiveNode(t)
	n.do("createwallet", "w")
	mineA := n.str("getnewaddress", "", "bech32")
	mineB := n.str("getnewaddress", "", "bech32m")
	var addrs, scripts []string
	for i := 0; i < 6; i++ {
		a := n.str("getnewaddress", "", []string{"legacy", "p2sh-segwit", "bech32"}[i%3])
		addrs, scripts = append(addrs, a), append(scripts, n.script(a))
	}
	scripts = append(scripts, n.script(mineA), n.script(mineB))
	n.do("generatetoaddress", 101, mineA)

	store := openFollowStore(t)
	f := newUtxoFollower(store, n.rpc)
	ctx := context.Background()
	if err := f.Step(ctx); err != nil {
		t.Fatal(err)
	}
	many := map[string]any{}
	for _, a := range addrs {
		many[a] = "0.5"
	}
	for i := 0; i < 4; i++ {
		n.do("sendmany", "", many)
		n.do("generatetoaddress", 1, mineA)
	}
	if err := f.Step(ctx); err != nil {
		t.Fatal(err)
	}

	compare := func(label string) {
		t.Helper()
		if h, _, _, _ := store.Tip(); int(h) != n.height() {
			t.Fatalf("%s: tip %d, node %d", label, h, n.height())
		}
		coins := 0
		for _, s := range scripts {
			want, got := n.scan(s), lookupAmounts(t, store, s)
			if len(got) != len(want) {
				t.Fatalf("%s: script %s index %d coins, scantxoutset %d", label, s, len(got), len(want))
			}
			for k, v := range want {
				if got[k] != v {
					t.Fatalf("%s: coin %s index %d, scantxoutset %d", label, k, got[k], v)
				}
			}
			coins += len(want)
		}
		t.Logf("%s: %d coins match scantxoutset at height %d", label, coins, n.height())
	}
	compare("before reorg")

	logUndo := func(from, to int) {
		for h := from; h <= to; h++ {
			v, closer, err := store.db.Get(utxoUndoKey(uint32(h)))
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("undo record height %d: %d bytes", h, len(v))
			closer.Close()
		}
	}
	logUndo(n.height()-3, n.height())

	n.do("sendmany", "", many)
	n.do("generatetoaddress", 1, mineA)
	if err := f.Step(ctx); err != nil {
		t.Fatal(err)
	}
	n.invalidate(n.str("getbestblockhash"))
	n.do("generateblock", mineB, []string{})
	n.do("generateblock", mineB, []string{})
	if err := f.Step(ctx); err != nil {
		t.Fatalf("1-block reorg: %v", err)
	}
	compare("1-block reorg")

	n.do("sendmany", "", many)
	n.do("generatetoaddress", 3, mineA)
	if err := f.Step(ctx); err != nil {
		t.Fatal(err)
	}
	logUndo(n.height()-3, n.height())
	n.invalidate(n.str("getblockhash", n.height()-2))
	n.do("generatetoaddress", 4, mineB)
	if err := f.Step(ctx); err != nil {
		t.Fatalf("3-block reorg: %v", err)
	}
	compare("3-block reorg")

	if st := f.Status(); st.State != utxoFollowIdle || st.Error != "" {
		t.Fatalf("status %+v", st)
	}
}
