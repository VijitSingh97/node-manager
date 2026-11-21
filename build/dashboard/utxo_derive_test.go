package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestAddressScriptVectors(t *testing.T) {
	cases := []struct{ name, addr, script string }{
		{"p2pkh", "1A1zP1eP5QGefi2DMPTfTL5SLmv7DivfNa", "76a91462e907b15cbf27d5425399ebf6f0fb50ebb88f1888ac"},
		{"p2sh", "3J98t1WpEZ73CNmQviecrnyiWrnqRhWNLy", "a914b472a266d0bd89c13706a4132ccfb16f7c3b9fcb87"},
		{"p2sh-p2wpkh", "2Mww8dCYPUpKHofjgcXcBCEGmniw9CoaiD2", "a914336caa13e08b96080a32b5d818d59b4ab3b3674287"},
		{"p2wpkh", "bc1qw508d6qejxtdg4y5r3zarvary0c5xw7kv8f3t4", "0014751e76e8199196d454941c45d1b3a323f1433bd6"},
		{"p2wpkh-regtest", "bcrt1qw508d6qejxtdg4y5r3zarvary0c5xw7kygt080", "0014751e76e8199196d454941c45d1b3a323f1433bd6"},
		{"p2wsh", "tb1qrp33g0q5c5txsp9arysrx4k6zdkfs4nce4xj0gdcccefvpysxf3q0sl5k7", "00201863143c14c5166804bd19203356da136c985678cd4d27a1b8c6329604903262"},
		{"p2tr", "bc1p0xlxvlhemja6c4dqv22uapctqupfhlxm9h8z3k2e72q4k9hcz7vqzk5jj0", "512079be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798"},
		{"upper-case", strings.ToUpper("bc1qw508d6qejxtdg4y5r3zarvary0c5xw7kv8f3t4"), "0014751e76e8199196d454941c45d1b3a323f1433bd6"},
	}
	for _, c := range cases {
		got, err := addressScript(c.addr)
		if err != nil || hex.EncodeToString(got) != c.script {
			t.Errorf("%s: got %x, %v; want %s", c.name, got, err, c.script)
		}
	}
}

func TestAddressScriptRejectsBadInput(t *testing.T) {
	for _, addr := range []string{
		"1A1zP1eP5QGefi2DMPTfTL5SLmv7DivfNb",
		"bc1qw508d6qejxtdg4y5r3zarvary0c5xw7kv8f3t5",
		"bc1p0xlxvlhemja6c4dqv22uapctqupfhlxm9h8z3k2e72q4k9hcz7vqh2y7hd",
		"bc1qw508d6qejxtdg4y5r3zarvary0c5xw7KV8f3t4",
		"BC1QW508D6QEJXTDG4Y5R3ZARVARY0C5XW7kv8f3t4",
		"bc1",
		"",
	} {
		if script, err := addressScript(addr); err == nil {
			t.Errorf("%q accepted as %x", addr, script)
		}
	}
}

type fakeBatch struct {
	t          *testing.T
	batches    atomic.Int32
	perAddr    time.Duration
	inflight   atomic.Int32
	peak       atomic.Int32
	ranges     [][2]int
	mu         sync.Mutex
	deriveFn   func(desc string) (any, string)
	infoFail   func(desc string) bool
	addrAt     func(desc string, idx int) string
	validateFn func(address string) any
}

func (f *fakeBatch) server() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.batches.Add(1)
		n := f.inflight.Add(1)
		defer f.inflight.Add(-1)
		for {
			p := f.peak.Load()
			if n <= p || f.peak.CompareAndSwap(p, n) {
				break
			}
		}
		var calls []struct {
			ID     int
			Method string
			Params []any
		}
		if err := json.NewDecoder(r.Body).Decode(&calls); err != nil {
			f.t.Errorf("request was not a batch: %v", err)
		}
		out := make([]map[string]any, 0, len(calls))
		for i := len(calls) - 1; i >= 0; i-- {
			c := calls[i]
			desc, _ := c.Params[0].(string)
			item := map[string]any{"id": c.ID, "error": nil}
			switch c.Method {
			case "getdescriptorinfo":
				if f.infoFail != nil && f.infoFail(desc) {
					item["result"], item["error"] = nil, map[string]any{"code": -5, "message": "key is not valid"}
					break
				}
				item["result"] = map[string]any{"descriptor": desc + "#abcdefgh", "isrange": strings.Contains(desc, "*"), "hasprivatekeys": false}
			case "validateaddress":
				item["result"] = f.validateFn(desc)
			case "deriveaddresses":
				result, failure := f.deriveFn(desc)
				if failure != "" {
					item["result"], item["error"] = nil, map[string]any{"code": -5, "message": failure}
				} else {
					item["result"] = result
					if list, ok := result.([]string); ok && strings.Contains(desc, "*") {
						lo, hi := 0, 999
						if len(c.Params) > 1 {
							r, _ := c.Params[1].([]any)
							lo, hi = int(r[0].(float64)), int(r[1].(float64))
							f.mu.Lock()
							f.ranges = append(f.ranges, [2]int{lo, hi})
							f.mu.Unlock()
						}
						time.Sleep(f.perAddr * time.Duration(hi-lo+1))
						if f.addrAt != nil {
							time.Sleep(time.Duration(deriveRangeEnd-lo) * 20 * time.Microsecond)
						}
						padded := make([]string, hi-lo+1)
						for k := range padded {
							padded[k] = list[k%len(list)]
							if f.addrAt != nil {
								padded[k] = f.addrAt(desc, lo+k)
							}
						}
						item["result"] = padded
					}
				}
			}
			out = append(out, item)
		}
		writeJSON(w, out)
	}))
}

const testXpub = "xpub661MyMwAqRbcFtXgS5sYJABqqG9YLmC4Q1Rdap9gSE8NqtwybGhePY2gZ29ESFjqJoCu1Rupje8YtGqsefD265TMg7usUDFdp6W1EGMcet8"

func TestResolveWalletScriptsBatchesSortsAndFallsBack(t *testing.T) {
	f := &fakeBatch{t: t, deriveFn: func(desc string) (any, string) {
		switch {
		case strings.HasPrefix(desc, "pk("):
			return nil, "Descriptor does not have a corresponding address"
		case strings.Contains(desc, "/1/*"):
			return []string{"bcrt1qw508d6qejxtdg4y5r3zarvary0c5xw7kygt080", "2Mww8dCYPUpKHofjgcXcBCEGmniw9CoaiD2"}, ""
		}
		return []string{"bcrt1qw508d6qejxtdg4y5r3zarvary0c5xw7kygt080"}, ""
	}}
	srv := f.server()
	defer srv.Close()
	rpc := &rpcClient{url: srv.URL, client: srv.Client()}
	got, err := resolveWalletScripts(context.Background(), rpc, utxoBitcoin, []string{testXpub, "pk(" + strings.Repeat("02", 33) + ")", "bcrt1qw508d6qejxtdg4y5r3zarvary0c5xw7kygt080"})
	if err != nil {
		t.Fatal(err)
	}
	if f.batches.Load() != 4 {
		t.Fatalf("%d HTTP calls for three wallets, want 1 info + 3 derive", f.batches.Load())
	}
	if got[0].Fallback || len(got[0].Scripts) != 2 || hex.EncodeToString(got[0].Scripts[0]) != "0014751e76e8199196d454941c45d1b3a323f1433bd6" {
		t.Fatalf("xpub wallet: %+v", got[0])
	}
	if !got[1].Fallback || got[1].Scripts != nil {
		t.Fatalf("pk wallet did not fall back: %+v", got[1])
	}
	if got[2].Fallback || len(got[2].Scripts) != 1 {
		t.Fatalf("address wallet: %+v", got[2])
	}
}

func TestResolveWalletScriptsRejectsPrivateKeys(t *testing.T) {
	rpc := &rpcClient{url: "http://127.0.0.1:1", client: http.DefaultClient}
	xprv := "xprv9s21ZrQH143K3QTDL4LXw2F7HEK3wJUD2nW2nRk4stbPy6cq3jPPqjiChkVvvNKmPGJxWUtg6LnF5kejMRNNU3TGtRBeJgk33yuGBxrMPHi"
	if _, err := resolveWalletScripts(context.Background(), rpc, utxoBitcoin, []string{xprv}); err == nil {
		t.Fatal("private key accepted")
	}
}

func TestRPCBatchRejectsBadFraming(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, []map[string]any{{"id": 0, "result": 1, "error": nil}})
	}))
	defer srv.Close()
	rpc := &rpcClient{url: srv.URL, client: srv.Client()}
	if _, err := rpc.callBatch(context.Background(), []rpcBatchCall{{"a", nil}, {"b", nil}}); err == nil {
		t.Fatal("incomplete batch accepted")
	}
}

func TestResolveWalletScriptsFallsBackPerWallet(t *testing.T) {
	var derived atomic.Int32
	f := &fakeBatch{t: t, deriveFn: func(desc string) (any, string) {
		derived.Add(1)
		return []string{"bcrt1qw508d6qejxtdg4y5r3zarvary0c5xw7kygt080"}, ""
	}, infoFail: func(desc string) bool { return strings.Contains(desc, "badkey") }}
	srv := f.server()
	defer srv.Close()
	rpc := &rpcClient{url: srv.URL, client: srv.Client()}
	keys := []string{
		testXpub,
		"wpkh(badkey/0/*)",
		"combo(" + testXpub + "/0/*)",
		"not a key",
		"pk(" + strings.Repeat("02", 33) + ")",
	}
	got, err := resolveWalletScripts(context.Background(), rpc, utxoBitcoin, keys)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Fallback || len(got[0].Scripts) != 1 {
		t.Fatalf("valid wallet: %+v", got[0])
	}
	for w := 1; w < len(keys); w++ {
		if !got[w].Fallback || got[w].Scripts != nil {
			t.Fatalf("wallet %d did not fall back: %+v", w, got[w])
		}
	}
	if derived.Load() != 8 {
		t.Fatalf("%d derive calls, want 8 (two branches in four chunks, valid wallet only)", derived.Load())
	}
}

func TestResolveWalletScriptsFallsBackOnShortRange(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var calls []struct {
			ID     int
			Method string
			Params []any
		}
		_ = json.NewDecoder(r.Body).Decode(&calls)
		out := []map[string]any{}
		for _, c := range calls {
			item := map[string]any{"id": c.ID, "error": nil}
			if c.Method == "getdescriptorinfo" {
				item["result"] = map[string]any{"descriptor": c.Params[0].(string) + "#abcdefgh", "isrange": true, "hasprivatekeys": false}
			} else {
				item["result"] = []string{"bcrt1qw508d6qejxtdg4y5r3zarvary0c5xw7kygt080"}
			}
			out = append(out, item)
		}
		writeJSON(w, out)
	}))
	defer srv.Close()
	rpc := &rpcClient{url: srv.URL, client: srv.Client()}
	got, err := resolveWalletScripts(context.Background(), rpc, utxoBitcoin, []string{testXpub})
	if err != nil || !got[0].Fallback {
		t.Fatalf("short derivation accepted: %+v, %v", got, err)
	}
}

func TestResolveWalletScriptsChunksLargeSets(t *testing.T) {
	f := &fakeBatch{t: t, deriveFn: func(string) (any, string) {
		return []string{"bcrt1qw508d6qejxtdg4y5r3zarvary0c5xw7kygt080"}, ""
	}}
	srv := f.server()
	defer srv.Close()
	rpc := &rpcClient{url: srv.URL, client: srv.Client()}
	keys := make([]string, 30)
	for i := range keys {
		keys[i] = testXpub
	}
	got, err := resolveWalletScripts(context.Background(), rpc, utxoBitcoin, keys)
	if err != nil {
		t.Fatal(err)
	}
	for w := range got {
		if got[w].Fallback || len(got[w].Scripts) != 1 {
			t.Fatalf("wallet %d: %+v", w, got[w])
		}
	}
	if f.batches.Load() != 3+3*4 {
		t.Fatalf("%d HTTP calls for 60 descriptors, want 15", f.batches.Load())
	}
}

func TestResolveWalletScriptsPipelinesWithoutGapsOrDuplicates(t *testing.T) {
	f := &fakeBatch{t: t, perAddr: 20 * time.Microsecond, deriveFn: func(string) (any, string) {
		return []string{"bcrt1qw508d6qejxtdg4y5r3zarvary0c5xw7kygt080"}, ""
	}}
	srv := f.server()
	defer srv.Close()
	rpc := &rpcClient{url: srv.URL, client: srv.Client()}
	got, err := resolveWalletScripts(context.Background(), rpc, utxoBitcoin, []string{testXpub})
	if err != nil || got[0].Fallback {
		t.Fatalf("%+v, %v", got, err)
	}
	covered := make([]int, deriveRangeEnd+1)
	for _, r := range f.ranges {
		for i := r[0]; i <= r[1]; i++ {
			covered[i]++
		}
	}
	for i, n := range covered {
		if n != 2 {
			t.Fatalf("index %d requested %d times, want once per branch", i, n)
		}
	}
	if f.peak.Load() < 2 {
		t.Fatalf("derive requests never overlapped (peak %d)", f.peak.Load())
	}
}

func bech32TestAddress(branch, idx int) string {
	prog := make([]byte, 20)
	prog[0], prog[1], prog[2] = byte(branch), byte(idx>>8), byte(idx)
	var data []byte
	var acc, bits uint32
	for _, b := range prog {
		acc = acc<<8 | uint32(b)
		bits += 8
		for bits >= 5 {
			bits -= 5
			data = append(data, byte(acc>>bits&31))
		}
	}
	if bits > 0 {
		data = append(data, byte(acc<<(5-bits)&31))
	}
	values := []byte{'b' >> 5, 'c' >> 5, 'r' >> 5, 't' >> 5, 0, 'b' & 31, 'c' & 31, 'r' & 31, 't' & 31}
	values = append(append(values, 0), data...)
	poly := bech32Polymod(append(values, 0, 0, 0, 0, 0, 0)) ^ 1
	out := "bcrt1q"
	for _, d := range data {
		out += string(bech32Charset[d])
	}
	for i := 0; i < 6; i++ {
		out += string(bech32Charset[poly>>uint(5*(5-i))&31])
	}
	return out
}

func TestResolveWalletScriptsMergesEveryChunkOfEveryBranch(t *testing.T) {
	f := &fakeBatch{t: t, deriveFn: func(string) (any, string) {
		return []string{bech32TestAddress(0, 0)}, ""
	}, addrAt: func(desc string, idx int) string {
		branch := 0
		if strings.Contains(desc, "/1/*") {
			branch = 1
		}
		return bech32TestAddress(branch, idx)
	}}
	srv := f.server()
	defer srv.Close()
	rpc := &rpcClient{url: srv.URL, client: srv.Client()}
	got, err := resolveWalletScripts(context.Background(), rpc, utxoBitcoin, []string{testXpub})
	if err != nil || got[0].Fallback {
		t.Fatalf("%+v, %v", got, err)
	}
	want := map[string]bool{}
	for branch := 0; branch < 2; branch++ {
		for i := 0; i <= deriveRangeEnd; i++ {
			script, err := addressScript(bech32TestAddress(branch, i))
			if err != nil {
				t.Fatal(err)
			}
			want[string(script)] = true
		}
	}
	if len(got[0].Scripts) != len(want) || len(want) != 2*(deriveRangeEnd+1) {
		t.Fatalf("%d scripts, want %d distinct", len(got[0].Scripts), len(want))
	}
	for i, s := range got[0].Scripts {
		if !want[string(s)] || i > 0 && bytes.Compare(got[0].Scripts[i-1], s) >= 0 {
			t.Fatalf("script %d unexpected or out of order", i)
		}
	}
}

func TestResolveWalletScriptsKeepsEveryDistinctScriptSorted(t *testing.T) {
	addrs := []string{"bcrt1qw508d6qejxtdg4y5r3zarvary0c5xw7kygt080", "2Mww8dCYPUpKHofjgcXcBCEGmniw9CoaiD2", "1A1zP1eP5QGefi2DMPTfTL5SLmv7DivfNa", "3J98t1WpEZ73CNmQviecrnyiWrnqRhWNLy"}
	f := &fakeBatch{t: t, deriveFn: func(string) (any, string) { return addrs, "" }}
	srv := f.server()
	defer srv.Close()
	rpc := &rpcClient{url: srv.URL, client: srv.Client()}
	got, err := resolveWalletScripts(context.Background(), rpc, utxoBitcoin, []string{testXpub})
	if err != nil || got[0].Fallback || len(got[0].Scripts) != len(addrs) {
		t.Fatalf("%+v, %v", got, err)
	}
	for i := 1; i < len(got[0].Scripts); i++ {
		if bytes.Compare(got[0].Scripts[i-1], got[0].Scripts[i]) >= 0 {
			t.Fatal("scripts are not sorted and unique")
		}
	}
}

func TestResolveWalletScriptsFallsBackWhenOneChunkFails(t *testing.T) {
	var calls atomic.Int32
	f := &fakeBatch{t: t, deriveFn: func(string) (any, string) {
		if calls.Add(1) == 3 {
			return nil, "boom"
		}
		return []string{"bcrt1qw508d6qejxtdg4y5r3zarvary0c5xw7kygt080"}, ""
	}}
	srv := f.server()
	defer srv.Close()
	rpc := &rpcClient{url: srv.URL, client: srv.Client()}
	got, err := resolveWalletScripts(context.Background(), rpc, utxoBitcoin, []string{testXpub})
	if err != nil || !got[0].Fallback || len(got[0].Scripts) != 0 {
		t.Fatalf("%+v, %v", got, err)
	}
}

func BenchmarkResolveWalletScriptsColdAdd(b *testing.B) {
	f := &fakeBatch{t: &testing.T{}, perAddr: 500 * time.Microsecond, deriveFn: func(string) (any, string) {
		return []string{"bcrt1qw508d6qejxtdg4y5r3zarvary0c5xw7kygt080"}, ""
	}}
	srv := f.server()
	defer srv.Close()
	rpc := &rpcClient{url: srv.URL, client: srv.Client()}
	for b.Loop() {
		if _, err := resolveWalletScripts(context.Background(), rpc, utxoBitcoin, []string{testXpub}); err != nil {
			b.Fatal(err)
		}
	}
}

func TestResolveWalletScriptsAsksTheNodeForFamilyScripts(t *testing.T) {
	f := &fakeBatch{t: t, deriveFn: func(desc string) (any, string) {
		switch {
		case strings.Contains(desc, "/1/*"):
			return []string{"rltc1-two", "rltc1-one"}, ""
		case strings.Contains(desc, "bad"):
			return []string{"rltc1-one", "rltc1-invalid"}, ""
		case strings.Contains(desc, "odd"):
			return []string{"rltc1-nothex"}, ""
		}
		return []string{"rltc1-one"}, ""
	}, validateFn: func(address string) any {
		switch address {
		case "rltc1-one":
			return map[string]any{"isvalid": true, "scriptPubKey": "0014aa"}
		case "rltc1-two":
			return map[string]any{"isvalid": true, "scriptPubKey": "0014bb"}
		case "rltc1-nothex":
			return map[string]any{"isvalid": true, "scriptPubKey": "zz"}
		}
		return map[string]any{"isvalid": false}
	}}
	srv := f.server()
	defer srv.Close()
	rpc := &rpcClient{url: srv.URL, client: srv.Client()}
	got, err := resolveWalletScripts(context.Background(), rpc, utxoLitecoin, []string{testXpub, "addr(bad)", "addr(odd)", "addr(fine)"})
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Fallback || len(got[0].Scripts) != 2 || hex.EncodeToString(got[0].Scripts[0]) != "0014aa" || hex.EncodeToString(got[0].Scripts[1]) != "0014bb" {
		t.Fatalf("xpub wallet: %+v", got[0])
	}
	if !got[1].Fallback || got[1].Scripts != nil || !got[2].Fallback || got[2].Scripts != nil {
		t.Fatalf("an address the node rejects or a bad script did not fall back: %+v %+v", got[1], got[2])
	}
	if got[3].Fallback || len(got[3].Scripts) != 1 {
		t.Fatalf("address wallet: %+v", got[3])
	}
	deriveCalls := 2*((deriveRangeEnd-deriveRangeStart+deriveChunkScripts)/deriveChunkScripts) + 3
	deriveSize := max(1, min(deriveBatchSize, (deriveCalls+deriveParallelCalls-1)/deriveParallelCalls))
	if want := 1 + (deriveCalls+deriveSize-1)/deriveSize + (2000+4+deriveBatchSize-1)/deriveBatchSize; int(f.batches.Load()) != want {
		t.Fatalf("%d HTTP calls, want %d: one info batch, %d derive calls in batches of %d, then 2,004 addresses validated %d at a time", f.batches.Load(), want, deriveCalls, deriveSize, deriveBatchSize)
	}
}
