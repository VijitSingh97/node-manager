package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func startLitecoinNode(t *testing.T) *liveNode {
	t.Helper()
	image := os.Getenv("LTC_REGTEST_IMAGE")
	if image == "" {
		t.Skip("set LTC_REGTEST_IMAGE to a node-manager-litecoin image to run against a regtest litecoind")
	}
	return runLiveNode(t, "19443", "--entrypoint", "litecoind", image, "-regtest", "-datadir=/tmp", "-rpcport=19443",
		"-fallbackfee=0.0001", "-printtoconsole=0", "-rpcuser=test", "-rpcpassword=test", "-rpcbind=0.0.0.0", "-rpcallowip=0.0.0.0/0")
}

func (n *liveNode) wallet(name string) *liveNode {
	w, rpc := *n, *n.rpc
	rpc.url += "/wallet/" + name
	w.rpc = &rpc
	return &w
}

// reseed dumps the node's coin set and ingests it into a fresh reverse store.
func (n *liveNode) reseed(t *testing.T, name string) *utxoStore {
	t.Helper()
	var res seedDumpResult
	if err := json.Unmarshal(n.do("dumptxoutset", "/tmp/"+name), &res); err != nil {
		t.Fatal(err)
	}
	raw, err := exec.Command("docker", "exec", n.name, "cat", "/tmp/"+name).Output()
	if err != nil {
		t.Fatal(err)
	}
	want, err := utxoRevHex(res.BaseHash)
	if err != nil {
		t.Fatal(err)
	}
	s := utxoTestOpenReverse(t, t.TempDir())
	hdr, err := ingestUtxoSnapshot(s, bytes.NewReader(raw), utxoLitecoin, [4]byte{}, func(h [32]byte) (uint32, error) {
		if h != want {
			return 0, errSeedMismatch
		}
		return res.BaseHeight, nil
	}, nil)
	if err != nil || hdr.CoinsCount != res.CoinsWritten {
		t.Fatalf("ingest %s: %v, %d coins against %d written", name, err, hdr.CoinsCount, res.CoinsWritten)
	}
	return s
}

func recordDiff(got, want map[string]string) string {
	var out []string
	for k, v := range got {
		if want[k] != v {
			out = append(out, fmt.Sprintf("%x is %x, reseed has %x", k, v, want[k]))
		}
	}
	for k := range want {
		if _, ok := got[k]; !ok {
			out = append(out, fmt.Sprintf("%x missing", k))
		}
	}
	return strings.Join(out, "; ")
}

func TestUtxoLitecoinMWEBRegtestLive(t *testing.T) {
	n := startLitecoinNode(t)
	n.do("createwallet", "w")
	n.do("createwallet", "mw")
	w, mw := n.wallet("w"), n.wallet("mw")
	miner := w.str("getnewaddress")
	n.do("generatetoaddress", 431, miner)
	// The first MWEB block needs a peg-in for its HogEx to spend.
	w.do("sendtoaddress", mw.str("getnewaddress", "", "mweb"), 10)
	n.do("generatetoaddress", 1, miner)

	index := n.reseed(t, "seed.dat")
	f := newUtxoFollower(index, n.rpc)
	f.chain = utxoLitecoin

	w.do("sendtoaddress", mw.str("getnewaddress", "", "mweb"), 3)
	w.do("sendtoaddress", w.str("getnewaddress"), 1.5)
	n.do("generatetoaddress", 1, miner)
	pegoutAddr, pegoutScript := w.newScript("bech32")
	mw.do("sendtoaddress", pegoutAddr, 2)
	mw.do("sendtoaddress", mw.str("getnewaddress", "", "mweb"), 1)
	n.do("generatetoaddress", 2, miner)
	stepOK(t, f)
	if got, want := lookupAmounts(t, index, pegoutScript), n.scan(pegoutScript); len(want) != 1 || !maps.Equal(got, want) {
		t.Fatalf("peg-out coins: index %v, scantxoutset %v", got, want)
	}
	if got, want := utxoTestKeys(t, index), utxoTestKeys(t, n.reseed(t, "tip.dat")); !maps.Equal(got, want) {
		t.Fatalf("following from the seed differs from a reseed at the tip: %s", recordDiff(got, want))
	}

	n.invalidate(n.str("getblockhash", n.height()-1))
	n.do("generatetoaddress", 3, w.str("getnewaddress"))
	if err := f.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got, want := utxoTestKeys(t, index), utxoTestKeys(t, n.reseed(t, fmt.Sprintf("reorg-%d.dat", n.height()))); !maps.Equal(got, want) {
		t.Fatalf("after the reorg the index differs from a reseed: %s", recordDiff(got, want))
	}
	verifyLiveStore(t, n, nodeLitecoin, utxoLitecoin, index)
	benchHostMatchesScan(t, n, nodeLitecoin, utxoLitecoin, []string{pegoutAddr, miner}, []string{pegoutScript, w.script(miner)})
}
