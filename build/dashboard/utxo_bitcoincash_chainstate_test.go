package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

const bchChainstateFixture = "testdata/bitcoincash-chainstate-regtest"

type bchFixtureOutput struct {
	Txid      string          `json:"txid"`
	Vout      uint32          `json:"vout"`
	Value     uint64          `json:"value"`
	Script    string          `json:"script"`
	Height    uint32          `json:"height"`
	Coinbase  bool            `json:"coinbase"`
	TokenData json.RawMessage `json:"tokenData"`
}

type bchFixture struct {
	Height     int                `json:"height"`
	BestBlock  string             `json:"best_block"`
	TxOuts     uint64             `json:"txouts"`
	TotalSats  uint64             `json:"total_amount_sats"`
	Outputs    []bchFixtureOutput `json:"outputs"`
	Pruned     bool               `json:"pruned"`
	byOutpoint map[string]bchFixtureOutput
}

func loadBCHFixture(t *testing.T) *bchFixture {
	t.Helper()
	raw, err := os.ReadFile(bchChainstateFixture + ".json")
	if err != nil {
		t.Fatal(err)
	}
	f := &bchFixture{byOutpoint: map[string]bchFixtureOutput{}}
	if err := json.Unmarshal(raw, f); err != nil {
		t.Fatal(err)
	}
	for _, o := range f.Outputs {
		f.byOutpoint[fmt.Sprintf("%s:%d", o.Txid, o.Vout)] = o
	}
	return f
}

func openBCHChainstate(t *testing.T, dir string) *chainstateCopy {
	t.Helper()
	c, err := openChainstateCopy(dir)
	if err != nil {
		t.Fatal(err)
	}
	c.txout = true
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestBitcoinCashChainstateMatchesTheNode(t *testing.T) {
	// Copied from Bitcoin Cash Node 29.2.0 on regtest after a clean stop with -prune=1; tools/regenerate-bitcoincash-chainstate-fixture.py rebuilds it.
	// The expectations are what the node reported through gettxout and gettxoutsetinfo for the same best block.
	want := loadBCHFixture(t)
	before, _ := os.ReadDir(bchChainstateFixture)
	c := openBCHChainstate(t, bchChainstateFixture)
	best, _ := utxoRevHex(want.BestBlock)
	if c.Best != best || len(c.xor) != 8 || bytes.Equal(c.xor, make([]byte, 8)) {
		t.Fatalf("best block %x, obfuscation key %x", c.Best, c.xor)
	}
	seen := map[string]bool{}
	var total uint64
	h, err := c.parse(nil, func(r utxoRecord) error {
		key := fmt.Sprintf("%s:%d", r.TxidHex(), r.Vout)
		o, ok := want.byOutpoint[key]
		if !ok || seen[key] {
			t.Errorf("unexpected or repeated coin %s", key)
			return nil
		}
		seen[key] = true
		total += r.Amount
		if hex.EncodeToString(r.ScriptPubKey) != o.Script || r.Amount != o.Value || r.Height != o.Height || r.Coinbase != o.Coinbase {
			t.Errorf("coin %s: script %x amount %d height %d coinbase %v, node says %+v", key, r.ScriptPubKey, r.Amount, r.Height, r.Coinbase, o)
		}
		return nil
	})
	if err != nil || h.CoinsCount != want.TxOuts || uint64(len(seen)) != want.TxOuts || total != want.TotalSats || h.BaseHash != best {
		t.Fatalf("header %+v, %d coins worth %d, node says %d worth %d, %v", h, len(seen), total, want.TxOuts, want.TotalSats, err)
	}
	if after, _ := os.ReadDir(bchChainstateFixture); len(after) != len(before) {
		t.Fatal("reading the copy wrote to it")
	}
}

func TestBitcoinCashChainstateCoversEveryScriptKind(t *testing.T) {
	want := loadBCHFixture(t)
	kinds := map[string]int{}
	var tokens int
	for _, o := range want.Outputs {
		s, _ := hex.DecodeString(o.Script)
		switch {
		case len(s) == 25 && s[0] == 0x76:
			kinds["p2pkh"]++
		case len(s) == 23 && s[0] == 0xa9:
			kinds["p2sh"]++
		case len(s) == 35 && s[0] == 0x21:
			kinds["p2pk compressed"]++
		case len(s) == 67 && s[0] == 0x41:
			kinds["p2pk uncompressed"]++
		case len(s) == 35 && s[0] == 0xaa:
			kinds["p2sh32"]++
		case len(s) == 1:
			kinds["bare"]++
		}
		if o.Coinbase {
			kinds["coinbase"]++
		}
		if len(o.TokenData) > 0 {
			tokens++
		}
		if o.Vout > 127 {
			kinds["vout above 127"]++
		}
		if o.Vout > 255 {
			kinds["vout above 255"]++
		}
	}
	for _, kind := range []string{"p2pkh", "p2sh", "p2pk compressed", "p2pk uncompressed", "p2sh32", "bare", "coinbase", "vout above 127", "vout above 255"} {
		if kinds[kind] == 0 {
			t.Errorf("the fixture has no %s output", kind)
		}
	}
	if tokens != 13 {
		t.Errorf("the fixture has %d token outputs, want 13", tokens)
	}
}

func TestBitcoinCashChainstateKeys(t *testing.T) {
	c := &chainstateCopy{txout: true}
	txid := bytes.Repeat([]byte{0x11}, 32)
	for _, tc := range []struct {
		vout uint32
		raw  []byte
		ok   bool
	}{
		{0, append([]byte{'C'}, append(txid, 0)...), true},
		{127, append([]byte{'C'}, append(txid, 0x7f)...), true},
		{128, append([]byte{'C'}, append(txid, 0x80, 0x00)...), true},
		{300, append([]byte{'C'}, append(txid, appendCoreVarInt(nil, 300)...)...), true},
		{math.MaxUint32, append([]byte{'C'}, append(txid, appendCoreVarInt(nil, math.MaxUint32)...)...), true},
		{0, append([]byte{'C'}, txid...), false},
		{0, append([]byte{'C'}, append(txid, 0x80)...), false},
		{0, append([]byte{'C'}, append(txid, 0, 0)...), false},
		{0, append([]byte{'C'}, append(txid, appendCoreVarInt(nil, math.MaxUint32+1)...)...), false},
		{0, append([]byte{'C'}, append(txid, 0x80, 0x80, 0x80, 0x80, 0x80, 0x00)...), false},
	} {
		got, vout, ok := c.splitKey(tc.raw)
		if ok != tc.ok || ok && (vout != tc.vout || !bytes.Equal(got[:], txid)) {
			t.Errorf("key %x: vout %d ok %v, want %d %v", tc.raw, vout, ok, tc.vout, tc.ok)
		}
	}
}

func bchTokenScript(category []byte, field byte, rest ...[]byte) []byte {
	out := append([]byte{cashTokenPrefix}, category...)
	out = append(out, field)
	for _, r := range rest {
		out = append(out, r...)
	}
	return out
}

func bchRow(code uint64, amount uint64, script []byte) []byte {
	row := appendCoreVarInt(nil, code)
	row = appendCoreVarInt(row, amount)
	row = appendCoreVarInt(row, uint64(len(script))+6)
	return append(row, script...)
}

func writeBCHChainstate(t *testing.T, rows map[string][]byte) string {
	t.Helper()
	return writeTestChainstate(t, append([]byte{8}, 1, 2, 3, 4, 5, 6, 7, 8), bytes.Repeat([]byte{0x42}, 32), rows)
}

func bchKey(vout uint64) string {
	return "C" + string(bytes.Repeat([]byte{0xab}, 32)) + string(appendCoreVarInt(nil, vout))
}

func parseBCH(t *testing.T, rows map[string][]byte) ([]utxoRecord, utxoHeader, error) {
	t.Helper()
	c := openBCHChainstate(t, writeBCHChainstate(t, rows))
	var coins []utxoRecord
	h, err := c.parse(nil, func(r utxoRecord) error { coins = append(coins, r); return nil })
	return coins, h, err
}

func TestBitcoinCashChainstateDecodesRows(t *testing.T) {
	p2pkh := append(append([]byte{0x76, 0xa9, 20}, bytes.Repeat([]byte{0x33}, 20)...), 0x88, 0xac)
	category := bytes.Repeat([]byte{0x99}, 32)
	wrapped := bchTokenScript(category, 0x10|0x20|0x40|0x02, []byte{2, 0xde, 0xad}, []byte{0xfd, 0x00, 0x01}, p2pkh)
	coins, h, err := parseBCH(t, map[string][]byte{
		bchKey(0):   bchRow(5<<1|1, 0, p2pkh),
		bchKey(127): bchRow(6<<1, 1, []byte{0x51}),
		bchKey(128): bchRow(7<<1, 50, wrapped),
		bchKey(300): bchRow(8<<1, 7, p2pkh),
	})
	if err != nil || h.CoinsCount != 4 || len(coins) != 4 {
		t.Fatalf("%+v %d coins, %v", h, len(coins), err)
	}
	byVout := map[uint64]utxoRecord{}
	for _, r := range coins {
		byVout[r.Vout] = r
	}
	if r := byVout[0]; !r.Coinbase || r.Height != 5 || r.Amount != 0 || !bytes.Equal(r.ScriptPubKey, p2pkh) {
		t.Errorf("vout 0: %+v", r)
	}
	if r := byVout[127]; r.Coinbase || r.Height != 6 || r.Amount != 1 || !bytes.Equal(r.ScriptPubKey, []byte{0x51}) {
		t.Errorf("vout 127: %+v", r)
	}
	if r := byVout[128]; r.Height != 7 || !bytes.Equal(r.ScriptPubKey, p2pkh) {
		t.Errorf("a token output kept its token prefix: %+v", r)
	}
	if r := byVout[300]; r.Vout != 300 || r.Height != 8 {
		t.Errorf("vout 300: %+v", r)
	}
}

func TestBitcoinCashChainstateRefusesCorruptRows(t *testing.T) {
	p2pkh := append(append([]byte{0x76, 0xa9, 20}, bytes.Repeat([]byte{0x33}, 20)...), 0x88, 0xac)
	good := bchRow(10, 5, p2pkh)
	for name, row := range map[string][]byte{
		"empty row":            {},
		"height only":          {10},
		"no script":            {10, 5},
		"short script":         good[:len(good)-1],
		"trailing byte":        append(append([]byte{}, good...), 0),
		"height above 32 bits": bchRow(1<<40, 5, p2pkh),
	} {
		if _, _, err := parseBCH(t, map[string][]byte{bchKey(0): row}); err == nil {
			t.Errorf("%s: a corrupt row parsed", name)
		}
	}
}

func TestBitcoinCashChainstateKeepsScriptsWithoutValidTokenData(t *testing.T) {
	p2pkh := append(append([]byte{0x76, 0xa9, 20}, bytes.Repeat([]byte{0x33}, 20)...), 0x88, 0xac)
	category := bytes.Repeat([]byte{0x99}, 32)
	for name, tc := range map[string]struct{ script, want []byte }{
		"prefix without a field": {[]byte{cashTokenPrefix, 1, 2}, []byte{cashTokenPrefix, 1, 2}},
		"reserved token bit":     {bchTokenScript(category, 0x80|0x10, []byte{5}, p2pkh), bchTokenScript(category, 0x80|0x10, []byte{5}, p2pkh)},
		"token amount of zero":   {bchTokenScript(category, 0x10, []byte{0}, p2pkh), bchTokenScript(category, 0x10, []byte{0}, p2pkh)},
		"token with no script":   {bchTokenScript(category, 0x10, []byte{5}), []byte{}},
	} {
		coins, _, err := parseBCH(t, map[string][]byte{bchKey(0): bchRow(10, 5, tc.script)})
		if err != nil || len(coins) != 1 || !bytes.Equal(coins[0].ScriptPubKey, tc.want) {
			t.Errorf("%s: %v %+v", name, err, coins)
		}
	}
}

func TestBitcoinCashChainstateRefusesABadKey(t *testing.T) {
	p2pkh := append(append([]byte{0x76, 0xa9, 20}, bytes.Repeat([]byte{0x33}, 20)...), 0x88, 0xac)
	for name, key := range map[string]string{
		"no vout":      "C" + string(bytes.Repeat([]byte{0xab}, 32)),
		"open varint":  "C" + string(bytes.Repeat([]byte{0xab}, 32)) + "\x80",
		"1.14 rows":    "c" + string(bytes.Repeat([]byte{0xab}, 32)),
		"vout of zero": "C" + string(bytes.Repeat([]byte{0xab}, 32)) + "\x00\x00",
	} {
		coins, _, err := parseBCH(t, map[string][]byte{key: bchRow(10, 5, p2pkh)})
		if key[0] == 'c' {
			if err != nil || len(coins) != 0 {
				t.Errorf("%s: %v %d", name, err, len(coins))
			}
			continue
		}
		if err == nil {
			t.Errorf("%s: a bad key parsed", name)
		}
	}
}

func TestChainstateCopyRefusesACopyTakenMidFlush(t *testing.T) {
	key := append([]byte{8}, 1, 2, 3, 4, 5, 6, 7, 8)
	dir := writeTestChainstate(t, key, bytes.Repeat([]byte{0x42}, 32), map[string][]byte{"H": bytes.Repeat([]byte{0x01}, 64)})
	if c, err := openChainstateCopy(dir); err == nil {
		_ = c.Close()
		t.Fatal("a chainstate with a head-blocks marker opened")
	}
}

func newBitcoinCashSeedManager(t *testing.T, helper bool, rpc utxoRPC) (*utxoManager, string) {
	t.Helper()
	data := t.TempDir()
	if err := os.CopyFS(filepath.Join(data, utxoChainstateCopyName), os.DirFS(bchChainstateFixture)); err != nil {
		t.Fatal(err)
	}
	m := utxoMgrNewChain(t, t.TempDir(), nil, rpc, io.Discard, utxoBitcoinCash)
	m.configureSeed(utxoSeedHost{prefix: "bitcoincash-", stateDir: t.TempDir(), dataDir: data, lock: &sync.Mutex{},
		hold: func() bool { t.Error("a seed held the watch scan"); return false }, release: func() {},
		helper: func() bool { return helper }})
	m.temp = func() (float64, error) { return 50, nil }
	m.guards = func(context.Context) seedGuardInput {
		return seedGuardInput{State: utxoStateNotSeeded, TempC: 50, CPUs: 0.5, FreeBytes: 1 << 40, Chain: blockchainInfo{Pruned: true},
			ChainErr: errors.New("the node is loading its block index")}
	}
	m.set(utxoStateNotSeeded, "")
	return m, data
}

func TestBitcoinCashSeedModeFollowsTheHostHelper(t *testing.T) {
	for _, tc := range []struct {
		helper bool
		asked  string
		want   string
	}{
		{true, "", seedModeChainstate},
		{false, "", seedModeReplay},
		{false, seedModeChainstate, seedModeChainstate},
		{true, seedModeReplay, seedModeReplay},
		{true, "bogus", seedModeChainstate},
	} {
		m, _ := newBitcoinCashSeedManager(t, tc.helper, &chainstateSeedRPC{})
		if got := m.seedMode(tc.asked); got != tc.want {
			t.Errorf("helper %v asked %q: %q, want %q", tc.helper, tc.asked, got, tc.want)
		}
	}
	doge := utxoMgrNewChain(t, t.TempDir(), nil, &chainstateSeedRPC{}, io.Discard, utxoDogecoin)
	if doge.seedMode("") != seedModeChainstate || doge.seedMode(seedModeReplay) != seedModeChainstate {
		t.Error("a chain with no replay stopped seeding from its chainstate")
	}
	ltc := utxoMgrNewChain(t, t.TempDir(), nil, &chainstateSeedRPC{}, io.Discard, utxoLitecoin)
	if ltc.seedMode("") != "" || ltc.replays() {
		t.Error("a chain with a dump seeded another way")
	}
}

func TestBitcoinCashIndexSeedsAPrunedNodeFromAChainstateCopy(t *testing.T) {
	swapReplayRetries(t, 3, time.Millisecond)
	want := loadBCHFixture(t)
	for name, tc := range map[string]struct {
		helper bool
		asked  string
	}{"with the host helper": {true, ""}, "from the command line": {false, seedModeChainstate}} {
		rpc := &chainstateSeedRPC{best: want.BestBlock, height: uint32(want.Height), warmup: 2}
		m, _ := newBitcoinCashSeedManager(t, tc.helper, rpc)
		m.seedRequestedAs(t.Context(), newRequestID(), tc.asked)
		if st := m.Status(); st.LastSeed == nil || st.LastSeed.Outcome != "ok" || st.State != utxoStateCatching {
			t.Fatalf("%s: the seed ended as %+v, last %+v", name, st, st.LastSeed)
		}
		best, _ := utxoRevHex(want.BestBlock)
		if h, hash, ok, err := m.currentStore().Tip(); err != nil || !ok || h != uint32(want.Height) || hash != best {
			t.Fatalf("%s: index tip %d %x %v %v", name, h, hash, ok, err)
		}
		coins := uint64(0)
		for key := range utxoTestKeys(t, m.currentStore()) {
			if len(key) == utxoKeyLen {
				coins++
			}
		}
		if coins != want.TxOuts {
			t.Fatalf("%s: %d coins indexed, the node reported %d", name, coins, want.TxOuts)
		}
		if m.seed.Mode != seedModeChainstate {
			t.Fatalf("%s: seed mode %q", name, m.seed.Mode)
		}
	}
}

func TestBitcoinCashIndexWithoutTheHelperReplaysAndRefusesAPrunedNode(t *testing.T) {
	rpc := &chainstateSeedRPC{best: loadBCHFixture(t).BestBlock, height: 117}
	m, _ := newBitcoinCashSeedManager(t, false, rpc)
	m.seedRequested(t.Context(), newRequestID())
	st := m.Status()
	if st.State != utxoStateNotSeeded || len(st.Refusals) == 0 || st.Refusals[0].Guard != "node" {
		t.Fatalf("a pruned node was replayed or seeded: %+v", st)
	}
	if got := evaluateSeedGuards(seedGuardInput{Chainstate: true, State: utxoStateNotSeeded, TempC: 50, CPUs: 0.5, FreeBytes: 1 << 40,
		Chain: blockchainInfo{Pruned: true, Blocks: 1, Headers: 1}, TipTime: time.Now(), Now: time.Now()}); len(got) != 0 {
		t.Fatalf("a chainstate seed was refused for a pruned node: %+v", got)
	}
}

func TestBitcoinCashFailedChainstateSeedDiscardsTheIndexAndCountsTowardsTheStop(t *testing.T) {
	swapReplayRetries(t, 1, time.Millisecond)
	rpc := &chainstateSeedRPC{best: loadBCHFixture(t).BestBlock, height: 117, offMain: true}
	m, _ := newBitcoinCashSeedManager(t, true, rpc)
	m.mu.Lock()
	m.seed.AutoRequestID = "auto"
	m.mu.Unlock()
	m.seedRequested(t.Context(), "auto")
	st := m.Status()
	if st.LastSeed == nil || st.LastSeed.Outcome != "failed" || st.State != utxoStateNotSeeded {
		t.Fatalf("a seed from the wrong chain ended as %+v", st)
	}
	if _, _, ok, _ := m.currentStore().Tip(); ok {
		t.Fatal("a failed chainstate seed left a partial index")
	}
	m.mu.Lock()
	failures := m.seed.AutoFailures
	m.mu.Unlock()
	if failures != 1 {
		t.Fatalf("%d automatic failures counted", failures)
	}
}

func TestSeedRequestNamesItsModeAndRefusesOthers(t *testing.T) {
	now := time.Now()
	for mode, ok := range map[string]bool{"": true, seedModeChainstate: true, seedModeReplay: true, "dump": false, "Chainstate": false} {
		raw, _ := json.Marshal(seedRequest{ID: newRequestID(), RequestedAt: now.Unix(), Mode: mode})
		if _, refusal := validSeedRequest(raw, now); (refusal == nil) != ok {
			t.Errorf("mode %q: refusal %+v", mode, refusal)
		}
	}
}

func TestBitcoinCashSeedGoesThroughTheHostHelperWhenOneIsPresent(t *testing.T) {
	a, _ := testApp(t, &rpcFixture{}, "")
	b, _ := json.Marshal(hostControlCapability{Timestamp: time.Now().Unix(), Platform: "linux", BootKind: "boot", UTXOSeedSupported: true})
	if err := os.WriteFile(a.nodeControlPath("capability"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	m, _ := newBitcoinCashSeedManager(t, false, &chainstateSeedRPC{})
	m.host.helper = a.seedHelperAvailable
	a.chainUtxo = map[string]*utxoManager{nodeBitcoinCash: m}
	if m.replays() || !a.seedNow(nodeBitcoinCash) {
		t.Fatal("a pruned Bitcoin Cash node with the host helper was not offered a chainstate seed")
	}
	if got := hostControlHTTP(a, http.MethodPost, `{"action":"utxo_seed","node":"bitcoincash"}`, true, "192.168.1.20:4321"); got.Code != http.StatusAccepted {
		t.Fatalf("seed = %d %s", got.Code, got.Body.String())
	}
	raw, err := os.ReadFile(a.nodeControlPath("request"))
	if err != nil || !bytes.Contains(raw, []byte(`"node":"bitcoincash"`)) || !bytes.Contains(raw, []byte(`"action":"utxo_seed"`)) {
		t.Fatalf("host request %s %v", raw, err)
	}
	if id, refusal := a.queueSeedHostAction(nodeBitcoinCash); id != "" || refusal != hostChangePending {
		t.Fatalf("a second seed request while one waits: %q %q", id, refusal)
	}
}
