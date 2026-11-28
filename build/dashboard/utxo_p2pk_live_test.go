package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"os/exec"
	"path/filepath"
	"testing"
)

const (
	p2pkLiveWIFCompressed   = "cMahea7zqjxrtgAbB7LSGbcQUr1uX1ojuat9jZodMN87JcbXMTcA"
	p2pkLiveWIFUncompressed = "91avARGdfge8E4tZfYLoxeJ5sGBdNJQH4kvjJoQFacbgwmaKkrx"
)

func (n *liveNode) withChecksum(desc string) string {
	var info struct {
		Checksum string `json:"checksum"`
	}
	if err := json.Unmarshal(n.do("getdescriptorinfo", desc), &info); err != nil {
		n.t.Fatal(err)
	}
	return desc + "#" + info.Checksum
}

func (n *liveNode) scanDescriptors(descs ...string) map[string]uint64 {
	var res struct {
		Success  bool `json:"success"`
		Unspents []struct {
			Txid   string      `json:"txid"`
			Vout   int         `json:"vout"`
			Amount json.Number `json:"amount"`
		} `json:"unspents"`
	}
	if err := json.Unmarshal(n.do("scantxoutset", "start", descs), &res); err != nil || !res.Success {
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

// TestUtxoP2PKRegtestLive checks the index against the node's own descriptor engine: an address counts what pkh(key) and pk(key) scan to.
func TestUtxoP2PKRegtestLive(t *testing.T) {
	n := startLiveNode(t)
	n.do("createwallet", "w")
	for _, wif := range []string{p2pkLiveWIFCompressed, p2pkLiveWIFUncompressed} {
		var res []struct {
			Success bool `json:"success"`
		}
		if err := json.Unmarshal(n.do("importdescriptors", []any{map[string]any{"desc": n.withChecksum("combo(" + wif + ")"), "timestamp": "now"}}), &res); err != nil || len(res) != 1 || !res[0].Success {
			t.Fatalf("importdescriptors: %v %+v", err, res)
		}
	}
	pkC, pkU := "pk("+p2pkKeyGCompressed+")", "pk("+p2pkKeyGUncompressed+")"
	pkhC, pkhU := "pkh("+p2pkKeyGCompressed+")", "pkh("+p2pkKeyGUncompressed+")"
	var addrC, addrU []string
	for _, d := range []struct {
		desc string
		to   *[]string
	}{{pkhC, &addrC}, {pkhU, &addrU}} {
		if err := json.Unmarshal(n.do("deriveaddresses", n.withChecksum(d.desc)), d.to); err != nil || len(*d.to) != 1 {
			t.Fatalf("deriveaddresses: %v", err)
		}
	}
	scriptC, scriptU := n.script(addrC[0]), n.script(addrU[0])
	mine := n.str("getnewaddress", "", "bech32")

	n.do("generatetodescriptor", 3, n.withChecksum(pkU))
	n.do("generatetodescriptor", 101, n.withChecksum(pkC))
	n.str("sendtoaddress", addrC[0], "1.0")
	n.str("sendtoaddress", addrU[0], "2.0")
	n.do("generatetoaddress", 1, mine)

	oracle := func(script string, pk string) map[string]uint64 {
		return n.scanDescriptors("raw("+script+")", pk)
	}
	check := func(label string, s *utxoStore) {
		t.Helper()
		for _, c := range []struct{ script, pk string }{{scriptC, pkC}, {scriptU, pkU}} {
			want, got := oracle(c.script, c.pk), lookupAmounts(t, s, c.script)
			if len(want) < 1 || !maps.Equal(got, want) {
				t.Fatalf("%s: address %s index %v, node %v", label, c.script, got, want)
			}
		}
		verifyLiveStore(t, n, nodeBitcoin, utxoBitcoin, s)
	}

	dumpPath := "/tmp/p2pk.dat"
	n.do("dumptxoutset", dumpPath, "latest")
	local := filepath.Join(t.TempDir(), "p2pk.dat")
	if out, err := exec.Command("docker", "cp", n.name+":"+dumpPath, local).CombinedOutput(); err != nil {
		t.Fatalf("docker cp: %v\n%s", err, out)
	}
	seeded := utxoTestOpen(t, t.TempDir())
	utxoTestIngestFile(t, seeded, local, uint32(n.height()))
	check("after the dump seed", seeded)
	flagged := 0
	for _, script := range []string{scriptC, scriptU} {
		raw, _ := hex.DecodeString(script)
		coins, err := seeded.Lookup([][]byte{raw})
		if err != nil {
			t.Fatal(err)
		}
		_, p2pk := splitP2PKCoins(coins)
		flagged += len(p2pk)
		if err := confirmP2PKCoins(context.Background(), n.rpc, p2pk); err != nil {
			t.Fatalf("the node does not confirm the P2PK coins: %v", err)
		}
	}
	if flagged < 2 {
		t.Fatalf("only %d P2PK coins were flagged", flagged)
	}

	followed := openFollowStore(t)
	f := newUtxoFollower(followed, n.rpc)
	ctx := context.Background()
	if err := f.Step(ctx); err != nil {
		t.Fatal(err)
	}
	check("after following from genesis", followed)

	var coinbase struct {
		Tx []string `json:"tx"`
	}
	if err := json.Unmarshal(n.do("getblock", n.str("getblockhash", 1)), &coinbase); err != nil || len(coinbase.Tx) == 0 {
		t.Fatalf("getblock: %v", err)
	}
	raw := n.str("createrawtransaction", []any{map[string]any{"txid": coinbase.Tx[0], "vout": 0}}, []any{map[string]any{addrC[0]: "49.999"}})
	var signed struct {
		Hex      string `json:"hex"`
		Complete bool   `json:"complete"`
	}
	if err := json.Unmarshal(n.do("signrawtransactionwithwallet", raw), &signed); err != nil || !signed.Complete {
		t.Fatalf("signing the P2PK spend: %v %+v", err, signed)
	}
	n.str("sendrawtransaction", signed.Hex)
	n.do("generatetodescriptor", 2, n.withChecksum(pkU))
	if err := f.Step(ctx); err != nil {
		t.Fatal(err)
	}
	check("after spending a P2PK coin", followed)

	tip := n.str("getblockhash", n.height()-1)
	n.invalidate(tip)
	n.do("generatetodescriptor", 3, n.withChecksum(pkC))
	if err := f.Step(ctx); err != nil {
		t.Fatal(err)
	}
	check("after a reorg", followed)

	if err := followed.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := openUtxoStore(followed.dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	check("after a reopen", reopened)
}
