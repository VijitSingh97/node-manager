package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func startDogecoinNode(t *testing.T, dataDir string) *liveNode {
	t.Helper()
	image := os.Getenv("DOGE_REGTEST_IMAGE")
	if image == "" {
		t.Skip("set DOGE_REGTEST_IMAGE to a node-manager-dogecoin image to run against a regtest Dogecoin Core")
	}
	return runLiveNode(t, "18443", "-v", dataDir+":/data", "--entrypoint", "dogecoind", image, "-regtest", "-datadir=/data", "-rpcport=18443",
		"-acceptnonstdtxn=1", "-printtoconsole=0", "-rpcuser=test", "-rpcpassword=test", "-rpcbind=0.0.0.0", "-rpcallowip=0.0.0.0/0")
}

// stopDogecoinNode asks the node to stop and waits for it to exit, so its chainstate is flushed and closed.
func stopDogecoinNode(t *testing.T, n *liveNode) {
	t.Helper()
	_ = n.rpc.call(context.Background(), "stop", nil, new(json.RawMessage))
	for range 120 {
		out, _ := exec.Command("docker", "inspect", "-f", "{{.State.Running}}", n.name).Output()
		if strings.TrimSpace(string(out)) != "true" {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatal("dogecoind did not stop")
}

// copyChainstateLikeTheHost does what `stack utxo-chainstate-copy` does: hardlink the tables as .sst, copy the rest, leave LOCK and LOG.
func copyChainstateLikeTheHost(t *testing.T, src, dst string) {
	t.Helper()
	if err := os.MkdirAll(dst, 0o700); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		name := e.Name()
		switch {
		case name == "LOCK" || strings.HasPrefix(name, "LOG"):
		case strings.HasSuffix(name, ".ldb"):
			if err := os.Link(filepath.Join(src, name), filepath.Join(dst, strings.TrimSuffix(name, ".ldb")+".sst")); err != nil {
				t.Fatal(err)
			}
		default:
			b, err := os.ReadFile(filepath.Join(src, name))
			if err != nil || os.WriteFile(filepath.Join(dst, name), b, 0o600) != nil {
				t.Fatalf("copy %s: %v", name, err)
			}
		}
	}
}

func (n *liveNode) dogeScript(addr string) string {
	var info struct {
		ScriptPubKey string `json:"scriptPubKey"`
	}
	if err := json.Unmarshal(n.do("validateaddress", addr), &info); err != nil || info.ScriptPubKey == "" {
		n.t.Fatalf("validateaddress %s: %v", addr, err)
	}
	return info.ScriptPubKey
}

// dogeSend sends through dogecoin-cli in the container, so a rejection shows the node's reason, which the RPC client's HTTP 500 hides.
func (n *liveNode) dogeSend(hexTx string) string {
	out, err := exec.Command("docker", "exec", n.name, "dogecoin-cli", "-regtest", "-datadir=/data", "-rpcport=18443", "-rpcuser=test", "-rpcpassword=test", "sendrawtransaction", hexTx).CombinedOutput()
	if err != nil {
		n.t.Fatalf("sendrawtransaction: %s", out)
	}
	return strings.TrimSpace(string(out))
}

func (n *liveNode) signAndSend(raw string) string {
	var signed struct {
		Hex      string `json:"hex"`
		Complete bool   `json:"complete"`
	}
	if err := json.Unmarshal(n.do("signrawtransaction", raw), &signed); err != nil || !signed.Complete {
		n.t.Fatalf("signing a transaction: %v %+v", err, signed)
	}
	return n.dogeSend(signed.Hex)
}

// dogecoinIndexMatchesTheNode checks every output of every block: unspent ones must be indexed for their script with the node's value, spent ones must be gone.
func dogecoinIndexMatchesTheNode(t *testing.T, n *liveNode, s *utxoStore) int {
	t.Helper()
	want := map[string]map[string]uint64{}
	for h := 1; h <= n.height(); h++ {
		var blk struct {
			Tx []struct {
				Txid string `json:"txid"`
				Vout []struct {
					N            int             `json:"n"`
					Value        json.RawMessage `json:"value"`
					ScriptPubKey struct {
						Hex string `json:"hex"`
					} `json:"scriptPubKey"`
				} `json:"vout"`
			} `json:"tx"`
		}
		if err := json.Unmarshal(n.do("getblock", n.str("getblockhash", h), 2), &blk); err != nil {
			t.Fatal(err)
		}
		for _, tx := range blk.Tx {
			for _, out := range tx.Vout {
				script := out.ScriptPubKey.Hex
				if want[script] == nil {
					want[script] = map[string]uint64{}
				}
				var coin json.RawMessage
				if err := n.rpc.call(context.Background(), "gettxout", []any{tx.Txid, out.N, false}, &coin); errors.As(err, new(rpcNoResultError)) {
					continue
				} else if err != nil {
					t.Fatalf("gettxout %s:%d: %v", tx.Txid, out.N, err)
				}
				var amount rpcAmount
				if err := amount.UnmarshalJSON(out.Value); err != nil {
					t.Fatal(err)
				}
				raw, _ := hex.DecodeString(script)
				if utxoSpendable(raw) {
					want[script][fmt.Sprintf("%s:%d", tx.Txid, out.N)] = uint64(amount)
				}
			}
		}
	}
	coins := 0
	for script, outs := range want {
		if got := lookupAmounts(t, s, script); !maps.Equal(got, outs) {
			t.Fatalf("script %s: index %v, gettxout %v", script, got, outs)
		}
		coins += len(outs)
	}
	return coins
}

// TestUtxoDogecoinChainstateSeedRegtestLive seeds from a stopped node's chainstate copy, follows raw blocks after it, and checks every output with gettxout.
func TestUtxoDogecoinChainstateSeedRegtestLive(t *testing.T) {
	data := t.TempDir()
	if err := os.Chmod(data, 0o777); err != nil {
		t.Fatal(err)
	}
	n := startDogecoinNode(t, data)
	mine := n.str("getnewaddress")
	n.do("generate", 150)
	multisig := n.str("addmultisigaddress", 1, []string{n.str("getnewaddress")})
	n.str("sendtoaddress", multisig, "50")
	watched := []string{n.str("getnewaddress"), n.str("getnewaddress")}
	for i, a := range watched {
		n.str("sendtoaddress", a, fmt.Sprintf("%d", 10+i))
	}
	// Plant outputs the wallet would not write: an uncompressed key, a bare multisig and anyone-can-spend, then spend one so the mask has a gap.
	g := "79be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798"
	uncompressed := mustHex("41" + "04" + g + "483ada7726a3c4655da4fbfc0e1108a8fd17b448a68554199c47d08ffb10d4b8" + "ac")
	bare := mustHex("5121" + "02" + g + "51ae")
	anyone := []byte{0x51}
	var unspent []struct {
		Txid   string          `json:"txid"`
		Vout   int             `json:"vout"`
		Amount json.RawMessage `json:"amount"`
	}
	if err := json.Unmarshal(n.do("listunspent", 1), &unspent); err != nil || len(unspent) == 0 {
		t.Fatalf("listunspent: %v", err)
	}
	var funds rpcAmount
	_ = funds.UnmarshalJSON(unspent[0].Amount)
	// Dogecoin 1.14 takes outputs only as an object, in key order, and Go would sort a map's keys, so the change must be written last by hand.
	outs := json.RawMessage(fmt.Sprintf(`{%q:1,%q:1,%q:1,%q:%d.%08d}`, n.str("getnewaddress"), n.str("getnewaddress"), n.str("getnewaddress"),
		mine, (uint64(funds)-4e8)/1e8, (uint64(funds)-4e8)%1e8))
	raw := n.str("createrawtransaction", []any{map[string]any{"txid": unspent[0].Txid, "vout": unspent[0].Vout}}, outs)
	planted := n.signAndSend(withOutputScripts(t, raw, map[int][]byte{0: uncompressed, 1: bare, 2: anyone}))
	n.do("generate", 1)
	var plantedTx struct {
		Vout []struct {
			N            int `json:"n"`
			ScriptPubKey struct {
				Hex string `json:"hex"`
			} `json:"scriptPubKey"`
		} `json:"vout"`
	}
	if err := json.Unmarshal(n.do("getrawtransaction", planted, 1), &plantedTx); err != nil {
		t.Fatal(err)
	}
	for _, out := range plantedTx.Vout {
		if out.ScriptPubKey.Hex == "51" {
			spend := n.str("createrawtransaction", []any{map[string]any{"txid": planted, "vout": out.N}}, map[string]any{mine: "0.5"})
			n.dogeSend(spend)
		}
	}
	n.do("generate", 1)
	// A restart makes LevelDB write its log into a table, so the copy has hardlinked tables as well as a log.
	stopDogecoinNode(t, n)
	n = startDogecoinNode(t, data)
	n.do("generate", 1)
	snapshot := n.height()

	stopDogecoinNode(t, n)
	copyChainstateLikeTheHost(t, filepath.Join(data, "regtest", "chainstate"), filepath.Join(data, utxoChainstateCopyName))
	if tables, _ := filepath.Glob(filepath.Join(data, utxoChainstateCopyName, "*.sst")); len(tables) == 0 {
		t.Fatal("the chainstate copy has no table, so the hardlinked path is not exercised")
	}
	n = startDogecoinNode(t, data)

	m := newUtxoManager(t.TempDir(), nil, n.rpc, log.New(io.Discard, "", 0), utxoDogecoin)
	t.Cleanup(func() { _ = m.currentStore().Close() })
	m.configureSeed(utxoSeedHost{prefix: "dogecoin-", stateDir: t.TempDir(), dataDir: data, lock: &sync.Mutex{}, hold: func() bool { return true }, release: func() {}})
	m.temp = func() (float64, error) { return 50, nil }
	m.guards = func(ctx context.Context) seedGuardInput {
		return seedGuardInput{State: m.Status().State, TempC: 50, CPUs: 0.5, FreeBytes: 1 << 40, Chain: blockchainInfo{Chain: "regtest"}}
	}
	ctx := context.Background()
	m.tick(ctx)
	m.seedRequested(ctx, newRequestID())
	if st := m.Status(); st.LastSeed == nil || st.LastSeed.Outcome != "ok" {
		t.Fatalf("seed from the chainstate copy: %+v", st)
	}
	if h, _, _, _ := m.currentStore().Tip(); int(h) != snapshot {
		t.Fatalf("index tip %d after the seed, the copy was taken at %d", h, snapshot)
	}
	t.Logf("seeded at height %d: %d coins match gettxout", snapshot, dogecoinIndexMatchesTheNode(t, n, m.currentStore()))

	// After the seed the index follows raw blocks from the copy's tip.
	n.str("sendtoaddress", watched[0], "3")
	n.do("generate", 3)
	m.tick(ctx)
	if st := m.Status(); st.State != utxoStateReady || int(*st.IndexTip) != n.height() {
		t.Fatalf("after following: %+v", st)
	}
	coins := dogecoinIndexMatchesTheNode(t, n, m.currentStore())
	for _, a := range watched {
		var held []struct {
			Txid   string          `json:"txid"`
			Vout   int             `json:"vout"`
			Amount json.RawMessage `json:"amount"`
		}
		if err := json.Unmarshal(n.do("listunspent", 1, 9999999, []string{a}), &held); err != nil {
			t.Fatal(err)
		}
		want := map[string]uint64{}
		for _, c := range held {
			var v rpcAmount
			_ = v.UnmarshalJSON(c.Amount)
			want[fmt.Sprintf("%s:%d", c.Txid, c.Vout)] = uint64(v)
		}
		if got := lookupAmounts(t, m.currentStore(), n.dogeScript(a)); len(want) == 0 || !maps.Equal(got, want) {
			t.Fatalf("address %s: index %v, wallet %v", a, got, want)
		}
	}
	t.Logf("followed to height %d: %d coins match gettxout, and the watched addresses match listunspent", n.height(), coins)
	verifyLiveStore(t, n, nodeDogecoin, utxoDogecoin, m.currentStore())
}
