package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestLowerHexStrict(t *testing.T) {
	for value, want := range map[string]bool{"0123456789abcdef01234567": true, "0123456789ABCDEF01234567": false, "0123456789abcdef0123456": false, "0123456789abcdef0123456g": false} {
		if lowerHex(value, 24) != want {
			t.Fatalf("lowerHex(%q, 24) != %v", value, want)
		}
	}
}

func TestNewRequestID(t *testing.T) {
	id := newRequestID()
	if !validPortControlID(id) || id == newRequestID() {
		t.Fatalf("bad request id %q", id)
	}
}

func TestHostControlShapeAcceptsEveryIndexableChain(t *testing.T) {
	for node := range utxoChains {
		if !validHostControlShape("utxo_seed", node, nil, "", nil, "", nil) {
			t.Fatalf("utxo_seed rejected for %s", node)
		}
	}
	if !validHostControlShape("utxo_seed", nodeBitcoin, nil, "", nil, "", nil) || validHostControlShape("utxo_seed", nodeMonero, nil, "", nil, "", nil) {
		t.Fatal("utxo_seed node set wrong")
	}
}

func TestDecodeStrictBytesRules(t *testing.T) {
	var v struct {
		A int `json:"a"`
	}
	for body, want := range map[string]bool{`{"a":1}`: true, `{"a":1,"b":2}`: false, `{"a":1}{}`: false, `{"a":1} x`: false, `[`: false} {
		if decodeStrictBytes([]byte(body), &v) != want {
			t.Fatalf("decodeStrictBytes(%q) != %v", body, want)
		}
	}
}

func TestHostControlRevisionMustBeLowerHex(t *testing.T) {
	upper, lower := strings.Repeat("A", 64), strings.Repeat("a", 64)
	capability := func(rev string) []byte {
		return []byte(`{"timestamp":` + strconv.FormatInt(time.Now().Unix(), 10) + `,"platform":"linux","boot_kind":"boot","config_pending":true,"config_revision":"` + rev + `"}`)
	}
	if _, ok := decodeHostControlCapability(capability(lower)); !ok {
		t.Fatal("lowercase revision rejected")
	}
	if _, ok := decodeHostControlCapability(capability(upper)); ok {
		t.Fatal("uppercase capability revision accepted")
	}
	for rev, want := range map[string]bool{lower: true, upper: false, lower[:63]: false} {
		request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"action":"apply_config","config_revision":"`+rev+`"}`))
		if _, err := decodeHostControlInput(httptest.NewRecorder(), request); (err == nil) != want {
			t.Fatalf("revision %q accepted=%v want %v", rev, err == nil, want)
		}
	}
}

func TestImportedDogecoinIDsMustBeLowerHex(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "imported.json")
	good, bad := strings.Repeat("a", 24), strings.Repeat("A", 24)
	if err := os.WriteFile(path, []byte(`{"imported":["`+good+`","`+bad+`"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	store := &familyWatchStore{imported: map[string]bool{}, importedPath: path}
	store.loadImported()
	if !store.imported[good] || store.imported[bad] {
		t.Fatalf("imported = %v", store.imported)
	}
}
