package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
	t        *testing.T
	batches  int
	deriveFn func(desc string) (any, string)
	infoFail func(desc string) bool
}

func (f *fakeBatch) server() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.batches++
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
			case "deriveaddresses":
				result, failure := f.deriveFn(desc)
				if failure != "" {
					item["result"], item["error"] = nil, map[string]any{"code": -5, "message": failure}
				} else {
					item["result"] = result
					if list, ok := result.([]string); ok && strings.Contains(desc, "*") {
						padded := make([]string, 1000)
						for k := range padded {
							padded[k] = list[k%len(list)]
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
	got, err := resolveWalletScripts(context.Background(), rpc, []string{testXpub, "pk(" + strings.Repeat("02", 33) + ")", "bcrt1qw508d6qejxtdg4y5r3zarvary0c5xw7kygt080"})
	if err != nil {
		t.Fatal(err)
	}
	if f.batches != 2 {
		t.Fatalf("%d HTTP calls for three wallets, want 2", f.batches)
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
	if _, err := resolveWalletScripts(context.Background(), rpc, []string{xprv}); err == nil {
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
	derived := 0
	f := &fakeBatch{t: t, deriveFn: func(desc string) (any, string) {
		derived++
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
	got, err := resolveWalletScripts(context.Background(), rpc, keys)
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
	if derived != 2 {
		t.Fatalf("%d derive calls, want 2 (only the valid wallet)", derived)
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
	got, err := resolveWalletScripts(context.Background(), rpc, []string{testXpub})
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
	got, err := resolveWalletScripts(context.Background(), rpc, keys)
	if err != nil {
		t.Fatal(err)
	}
	for w := range got {
		if got[w].Fallback || len(got[w].Scripts) != 1 {
			t.Fatalf("wallet %d: %+v", w, got[w])
		}
	}
	if f.batches != 6 {
		t.Fatalf("%d HTTP calls for 60 descriptors, want 6", f.batches)
	}
}
