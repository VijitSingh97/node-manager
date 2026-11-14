package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"
)

type liveNode struct {
	t   *testing.T
	rpc *rpcClient
}

func startLiveNode(t *testing.T) *liveNode {
	t.Helper()
	if os.Getenv("UTXO_REGTEST") != "1" {
		t.Skip("set UTXO_REGTEST=1 to run against the pinned regtest bitcoind")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not installed")
	}
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skip("docker daemon not running")
	}
	compose, err := os.ReadFile("../../docker-compose.yml")
	if err != nil {
		t.Fatal(err)
	}
	image := regexp.MustCompile(`image: (bitcoin/bitcoin:\S+)`).FindStringSubmatch(string(compose))[1]
	name := fmt.Sprintf("utxo-follow-%d", time.Now().UnixNano())
	out, err := exec.Command("docker", "run", "-d", "--rm", "--name", name, "-p", "127.0.0.1::18443", image,
		"bitcoind", "-regtest", "-acceptnonstdtxn=1", "-fallbackfee=0.0001", "-printtoconsole=0",
		"-rpcuser=test", "-rpcpassword=test", "-rpcbind=0.0.0.0", "-rpcallowip=0.0.0.0/0").CombinedOutput()
	if err != nil {
		t.Fatalf("docker run: %v\n%s", err, out)
	}
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", name).Run() })
	port, err := exec.Command("docker", "port", name, "18443/tcp").Output()
	if err != nil {
		t.Fatal(err)
	}
	hostPort := strings.TrimSpace(strings.Split(string(port), "\n")[0])
	n := &liveNode{t: t, rpc: &rpcClient{url: "http://" + hostPort, user: "test", password: "test",
		client: &http.Client{Timeout: 60 * time.Second}, maxBody: 64 << 20}}
	for i := 0; ; i++ {
		var count int
		if n.rpc.call(context.Background(), "getblockcount", nil, &count) == nil {
			return n
		}
		if i > 60 {
			t.Fatal("bitcoind did not start")
		}
		time.Sleep(time.Second)
	}
}

func (n *liveNode) do(method string, params ...any) json.RawMessage {
	n.t.Helper()
	var out json.RawMessage
	if err := n.rpc.call(context.Background(), method, params, &out); err != nil {
		n.t.Fatalf("%s: %v", method, err)
	}
	return out
}

func (n *liveNode) str(method string, params ...any) string {
	var s string
	if err := json.Unmarshal(n.do(method, params...), &s); err != nil {
		n.t.Fatal(err)
	}
	return s
}

func (n *liveNode) height() int {
	var h int
	_ = json.Unmarshal(n.do("getblockcount"), &h)
	return h
}

func (n *liveNode) newScript(addrType string) (addr, script string) {
	addr = n.str("getnewaddress", "", addrType)
	var info struct {
		ScriptPubKey string `json:"scriptPubKey"`
	}
	if err := json.Unmarshal(n.do("getaddressinfo", addr), &info); err != nil {
		n.t.Fatal(err)
	}
	return addr, info.ScriptPubKey
}

func (n *liveNode) scan(script string) map[string]uint64 {
	var res struct {
		Success  bool `json:"success"`
		Unspents []struct {
			Txid   string      `json:"txid"`
			Vout   int         `json:"vout"`
			Amount json.Number `json:"amount"`
		} `json:"unspents"`
	}
	if err := json.Unmarshal(n.do("scantxoutset", "start", []string{"raw(" + script + ")"}), &res); err != nil || !res.Success {
		n.t.Fatalf("scantxoutset: %v", err)
	}
	out := map[string]uint64{}
	for _, u := range res.Unspents {
		var a rpcAmount
		if err := a.UnmarshalJSON([]byte(u.Amount.String())); err != nil {
			n.t.Fatal(err)
		}
		out[fmt.Sprintf("%s:%d", u.Txid, u.Vout)] = uint64(a)
	}
	return out
}

func TestUtxoFollowerRegtestLive(t *testing.T) {
	n := startLiveNode(t)
	n.do("createwallet", "w")
	types := []string{"legacy", "p2sh-segwit", "bech32", "bech32m"}
	var scripts []string
	var addrs []string
	for i := 0; i < 8; i++ {
		a, s := n.newScript(types[i%len(types)])
		addrs, scripts = append(addrs, a), append(scripts, s)
	}
	mine := n.str("getnewaddress", "", "bech32")
	var mineInfo struct {
		ScriptPubKey string `json:"scriptPubKey"`
	}
	if err := json.Unmarshal(n.do("getaddressinfo", mine), &mineInfo); err != nil {
		t.Fatal(err)
	}
	scripts = append(scripts, mineInfo.ScriptPubKey)
	n.do("generatetoaddress", 101, mine)

	store := openFollowStore(t)
	f := newUtxoFollower(store, n.rpc)
	ctx := context.Background()
	if err := f.Step(ctx); err != nil {
		t.Fatal(err)
	}
	if h, _, _, _ := store.Tip(); int(h) != n.height() {
		t.Fatalf("tip %d, node %d", h, n.height())
	}

	for i := 0; i < 6; i++ {
		to := addrs[i%len(addrs)]
		a := n.str("sendtoaddress", to, "1.5")
		var tx struct {
			Vout []struct {
				N            int `json:"n"`
				ScriptPubKey struct {
					Address string `json:"address"`
				} `json:"scriptPubKey"`
			} `json:"vout"`
		}
		_ = json.Unmarshal(n.do("getrawtransaction", a, true), &tx)
		vout := -1
		for _, o := range tx.Vout {
			if o.ScriptPubKey.Address == to {
				vout = o.N
			}
		}
		raw := n.str("createrawtransaction", []any{map[string]any{"txid": a, "vout": vout}},
			[]any{map[string]any{addrs[(i+3)%len(addrs)]: "1.4999"}, map[string]any{"data": "6865726f"}})
		var signed struct {
			Hex      string `json:"hex"`
			Complete bool   `json:"complete"`
		}
		_ = json.Unmarshal(n.do("signrawtransactionwithwallet", raw), &signed)
		if !signed.Complete {
			t.Fatal("signing failed")
		}
		b := n.str("sendrawtransaction", signed.Hex)
		n.do("generateblock", mine, []string{a, b})
		if i == 2 {
			if err := f.Step(ctx); err != nil {
				t.Fatal(err)
			}
		}
	}
	n.do("generatetoaddress", 3, mine)
	many := map[string]any{}
	for _, a := range addrs {
		many[a] = "0.01"
	}
	for i := 0; i < 5; i++ {
		n.do("sendmany", "", many)
		n.do("generatetoaddress", 1, mine)
	}

	if err := f.Step(ctx); err != nil {
		t.Fatal(err)
	}
	if h, _, _, _ := store.Tip(); int(h) != n.height() {
		t.Fatalf("tip %d, node %d", h, n.height())
	}
	var unspent []struct {
		ScriptPubKey string `json:"scriptPubKey"`
	}
	if err := json.Unmarshal(n.do("listunspent", 0), &unspent); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, s := range scripts {
		seen[s] = true
	}
	for _, u := range unspent {
		if !seen[u.ScriptPubKey] {
			seen[u.ScriptPubKey] = true
			scripts = append(scripts, u.ScriptPubKey)
		}
	}
	checked := 0
	for _, s := range scripts {
		want := n.scan(s)
		got := lookupAmounts(t, store, s)
		if len(got) != len(want) {
			t.Fatalf("script %s: index has %d coins, scantxoutset %d", s, len(got), len(want))
		}
		for k, v := range want {
			if got[k] != v {
				t.Fatalf("script %s coin %s: index %d, scantxoutset %d", s, k, got[k], v)
			}
		}
		checked += len(want)
	}
	var setInfo struct {
		TxOuts int `json:"txouts"`
	}
	if err := json.Unmarshal(n.do("gettxoutsetinfo", "none"), &setInfo); err != nil {
		t.Fatal(err)
	}
	if setInfo.TxOuts != checked {
		t.Fatalf("index holds %d coins at wallet scripts, node utxo set has %d", checked, setInfo.TxOuts)
	}
	if got := len(lookupAmounts(t, store, mineInfo.ScriptPubKey)); got < 100 {
		t.Fatalf("only %d coinbase coins at the mining script", got)
	}
	if checked < 20 {
		t.Fatalf("only %d coins compared", checked)
	}
	if st := f.Status(); st.State != utxoFollowIdle || st.Blocks != n.height()+1 {
		t.Fatalf("status %+v, height %d", st, n.height())
	}
	t.Logf("compared %d coins over %d scripts; %d blocks, fetch %v total, apply %v total",
		checked, len(scripts), f.Status().Blocks, f.Status().FetchDur, f.Status().ApplyDur)
}

func TestUtxoFollowerRegtestPerBlockCost(t *testing.T) {
	n := startLiveNode(t)
	n.do("createwallet", "w")
	mine := n.str("getnewaddress", "", "bech32")
	n.do("generatetoaddress", 101, mine)
	store := openFollowStore(t)
	f := newUtxoFollower(store, n.rpc)
	if err := f.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	base := f.Status()
	many := map[string]any{}
	for i := 0; i < 500; i++ {
		many[n.str("getnewaddress", "", "bech32")] = "0.001"
	}
	for i := 0; i < 20; i++ {
		n.do("sendmany", "", many)
		n.do("generatetoaddress", 1, mine)
	}
	var blk struct {
		Tx []json.RawMessage `json:"tx"`
	}
	_ = json.Unmarshal(n.do("getblock", n.str("getbestblockhash"), 3), &blk)
	size := len(n.do("getblock", n.str("getbestblockhash"), 3))

	if err := f.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	st := f.Status()
	st.Blocks -= base.Blocks
	st.FetchDur -= base.FetchDur
	st.ApplyDur -= base.ApplyDur
	t.Logf("last block: %d tx, %d bytes of getblock v3 JSON", len(blk.Tx), size)
	t.Logf("%d blocks of 500 outputs: fetch %v (%v/block), apply %v (%v/block)", st.Blocks, st.FetchDur,
		st.FetchDur/time.Duration(st.Blocks), st.ApplyDur, st.ApplyDur/time.Duration(st.Blocks))
}
