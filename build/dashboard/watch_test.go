package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestDescriptorParityVectors(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("parity", "oracle.json"))
	if err != nil {
		t.Fatal(err)
	}
	var oracle struct {
		Slip132 struct {
			Versions []struct {
				Input       string       `json:"input"`
				Descriptors []descriptor `json:"descriptors"`
			} `json:"versions"`
			BadChecksum struct {
				Input string `json:"input"`
			} `json:"bad_checksum"`
		} `json:"slip132"`
		Descriptors []struct {
			Input       string       `json:"input"`
			Descriptors []descriptor `json:"descriptors"`
		} `json:"descriptors"`
		Helpers struct {
			WalletNames []struct {
				Input  string `json:"input"`
				Output string `json:"output"`
			} `json:"wallet_names"`
			Birthdays []struct {
				Input     string `json:"input"`
				Timestamp int64  `json:"timestamp"`
			} `json:"birthdays"`
			BTCFormat []struct {
				Input  float64 `json:"input"`
				Output string  `json:"output"`
			} `json:"btc_format"`
		} `json:"helpers"`
	}
	if err := json.Unmarshal(b, &oracle); err != nil {
		t.Fatal(err)
	}
	for _, vector := range oracle.Slip132.Versions {
		got, err := descriptorsFor(vector.Input)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, vector.Descriptors) {
			t.Fatalf("descriptors for %.4s = %#v, want %#v", vector.Input, got, vector.Descriptors)
		}
	}
	if _, err := descriptorsFor(oracle.Slip132.BadChecksum.Input); err == nil {
		t.Fatal("bad checksum accepted")
	}
	for _, vector := range oracle.Descriptors {
		got, err := descriptorsFor(vector.Input)
		if err != nil || !reflect.DeepEqual(got, vector.Descriptors) {
			t.Fatalf("descriptor %q = %#v, %v; want %#v", vector.Input, got, err, vector.Descriptors)
		}
	}
	for _, vector := range oracle.Helpers.WalletNames {
		if got := walletName(vector.Input); got != vector.Output {
			t.Errorf("walletName(%q) = %q, want %q", vector.Input, got, vector.Output)
		}
	}
	for _, vector := range oracle.Helpers.Birthdays {
		if got := birthdayTimestamp(vector.Input); got != vector.Timestamp {
			t.Errorf("birthdayTimestamp(%q) = %d, want %d", vector.Input, got, vector.Timestamp)
		}
	}
	for _, vector := range oracle.Helpers.BTCFormat {
		if got := formatBTC(vector.Input); got != vector.Output {
			t.Errorf("formatBTC(%v) = %q, want %q", vector.Input, got, vector.Output)
		}
	}
}

func TestPrivateMaterialRejected(t *testing.T) {
	for _, prefix := range privatePrefixes {
		malformed := prefix + strings.Repeat("1", 60)
		if !hasPrivateMaterial("wpkh(" + malformed + ")") {
			t.Errorf("private prefix %s was not detected", prefix)
		}
	}
	versions := []string{
		"0488ade4", "049d7878", "04b2430c", "04358394", "044a4e28", "045f18bc",
		"0295b005", "02aa7a99", "024285b5", "02575048",
	}
	for _, version := range versions {
		private := syntheticExtendedPrivate(version)
		for _, value := range []string{private, "wpkh(" + private + "/0/*)"} {
			if !hasPrivateMaterial(value) {
				t.Errorf("private extended key version %s was not detected", version)
			}
			if _, err := descriptorsFor(value); err == nil || !strings.Contains(err.Error(), "private key") {
				t.Errorf("descriptorsFor private version %s error = %v", version, err)
			}
		}
	}
	unknownVersion := make([]byte, 78)
	copy(unknownVersion, mustHex("deadbeef"))
	for index := 4; index < len(unknownVersion); index++ {
		unknownVersion[index] = byte(index)
	}
	unknownVersion[45] = 0
	if private := base58EncodeCheck(unknownVersion); !hasPrivateMaterial("wpkh(" + private + "/0/*)") {
		t.Fatal("extended private key with an unknown version was not detected")
	}
	for _, version := range []byte{0x80, 0xef} {
		for _, compressed := range []bool{false, true} {
			payload := append([]byte{version}, make([]byte, 32)...)
			if compressed {
				payload = append(payload, 1)
			}
			private := base58EncodeCheck(payload)
			if !hasPrivateMaterial("pkh(" + private + ")") {
				t.Errorf("WIF version %x compressed=%v was not detected", version, compressed)
			}
		}
	}
	if hasPrivateMaterial(parityZpub) || hasPrivateMaterial("1A1zP1eP5QGefi2DMPTfTL5SLmv7DivfNa") {
		t.Fatal("public watch material identified as private")
	}
}

func TestPrivateEntriesFilteredWithoutRewritingExistingRoster(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "wallets.json")
	private := syntheticExtendedPrivate("0488ade4")
	original, err := json.Marshal([]WatchEntry{
		{Name: "Private", Key: "wpkh(" + private + "/0/*)"},
		{Name: "Public", Key: parityZpub},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, original, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WATCH_STORE", path)
	t.Setenv("BALANCE_HISTORY", filepath.Join(dir, "balance_history.json"))
	store := NewWatchStore(dir)
	if got := store.Entries(); len(got) != 1 || got[0].Name != "Public" {
		t.Fatalf("filtered entries = %#v", got)
	}
	if _, err := store.Add("Rejected", private, ""); err == nil {
		t.Fatal("private key was added")
	}
	if _, err := store.Add("Another", parityZpub, ""); err == nil || !strings.Contains(err.Error(), "remove it manually") {
		t.Fatalf("public add with unsafe legacy roster error = %v", err)
	}
	if removed, err := store.Remove("Public"); removed || !errors.Is(err, ErrWatchUnavailable) {
		t.Fatal("remove modified an unsafe legacy roster")
	}
	unchanged, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(unchanged, original) {
		t.Fatal("loading filtered private material rewrote the existing roster")
	}
	view, err := json.Marshal(store.List(func(string, string, []any, time.Duration) any { return nil }, false))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(view), private) {
		t.Fatal("private material exposed by List")
	}
	assertMode(t, dir, 0o700)
	assertMode(t, path, 0o600)
}

func TestPrivateConfigSeedDroppedBeforePersistence(t *testing.T) {
	dir := t.TempDir()
	private := syntheticExtendedPrivate("04358394")
	seed, err := json.Marshal([]WatchEntry{{Name: "Private", Key: private}, {Name: "Public", Key: parityZpub}})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "wallets.json")
	t.Setenv("WATCH_STORE", path)
	t.Setenv("BALANCE_HISTORY", filepath.Join(dir, "balance_history.json"))
	t.Setenv("WATCH_WALLETS_B64", base64.StdEncoding.EncodeToString(seed))
	store := NewWatchStore(dir)
	if got := store.Entries(); len(got) != 1 || got[0].Name != "Public" {
		t.Fatalf("seeded entries = %#v", got)
	}
	persisted, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(persisted), private) {
		t.Fatal("private seed was persisted")
	}
	assertMode(t, path, 0o600)
}

func TestInvalidConfigSeedFailsClosed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wallets.json")
	t.Setenv("WATCH_STORE", path)
	t.Setenv("BALANCE_HISTORY", filepath.Join(dir, "balance_history.json"))
	t.Setenv("WATCH_WALLETS_B64", "not base64")
	store := NewWatchStore(dir)
	if !errors.Is(store.Err(), ErrWatchUnavailable) || len(store.Entries()) != 0 {
		t.Fatalf("invalid seed state = %v, %#v", store.Err(), store.Entries())
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("invalid seed created roster: %v", err)
	}
}

func TestPersistedRosterOverridesPublicExampleSeed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wallets.json")
	seed, err := json.Marshal([]WatchEntry{{Name: publicExampleName, Key: publicExampleAddress}})
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("WATCH_STORE", path)
	t.Setenv("BALANCE_HISTORY", filepath.Join(dir, "balance_history.json"))
	t.Setenv("WATCH_WALLETS_B64", base64.StdEncoding.EncodeToString(seed))
	store := NewWatchStore(dir)
	if got := store.Entries(); len(got) != 1 || !isPublicExample(got[0]) {
		t.Fatalf("initial public example = %#v", got)
	}
	if removed, err := store.Remove(publicExampleName); !removed || err != nil {
		t.Fatalf("remove public example = %v, %v", removed, err)
	}
	store = NewWatchStore(dir)
	if got := store.Entries(); len(got) != 0 {
		t.Fatalf("removed public example was reseeded: %#v", got)
	}
}

func TestWatchStoreFailsClosedWhenExistingFileCannotBeSecured(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wallets.json")
	original := []byte(`[{"name":"Public","key":"` + parityZpub + `"}]`)
	if err := os.WriteFile(path, original, 0o644); err != nil {
		t.Fatal(err)
	}
	originalChmod := chmodExistingFile
	chmodExistingFile = func(candidate string, mode os.FileMode) error {
		if candidate == path {
			return os.ErrPermission
		}
		return originalChmod(candidate, mode)
	}
	t.Cleanup(func() { chmodExistingFile = originalChmod })
	t.Setenv("WATCH_STORE", path)
	t.Setenv("BALANCE_HISTORY", filepath.Join(dir, "balance_history.json"))
	store := NewWatchStore(dir)
	if got := store.Entries(); len(got) != 0 {
		t.Fatalf("entries loaded after chmod failure: %#v", got)
	}
	if _, err := store.Add("Another", parityZpub, ""); !errors.Is(err, ErrWatchUnavailable) {
		t.Fatalf("add after chmod failure error = %v", err)
	}
	if !errors.Is(store.Err(), ErrWatchUnavailable) {
		t.Fatalf("store error = %v", store.Err())
	}
	unchanged, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(unchanged, original) {
		t.Fatal("chmod failure rewrote the existing roster")
	}
}

func TestWatchMutationWriteFailuresRollbackMemoryAndFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wallets.json")
	t.Setenv("WATCH_STORE", path)
	t.Setenv("BALANCE_HISTORY", filepath.Join(dir, "history.json"))
	store := NewWatchStore(dir)
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	originalWriter := writeWatchFile
	writeWatchFile = func(string, []byte) error { return ErrWatchUnavailable }
	t.Cleanup(func() { writeWatchFile = originalWriter })
	if _, err := store.Add("Cold", genesisAddress, ""); !errors.Is(err, ErrWatchUnavailable) {
		t.Fatalf("failed add error = %v", err)
	}
	if len(store.Entries()) != 0 {
		t.Fatal("failed add changed memory")
	}
	unchanged, _ := os.ReadFile(path)
	if !reflect.DeepEqual(unchanged, original) {
		t.Fatal("failed add changed roster file")
	}

	writeWatchFile = originalWriter
	store = NewWatchStore(dir)
	entry, err := store.Add("Cold", genesisAddress, "")
	if err != nil {
		t.Fatal(err)
	}
	original, _ = os.ReadFile(path)
	store.SetPending(entry.Key, true)
	store.SetCoreWallet(entry.Key, coreWalletName(entry))
	writeWatchFile = func(string, []byte) error { return ErrWatchUnavailable }
	if removed, err := store.RemoveExact(entry); removed || !errors.Is(err, ErrWatchUnavailable) {
		t.Fatalf("failed remove = %v, %v", removed, err)
	}
	if !reflect.DeepEqual(store.Entries(), []WatchEntry{entry}) {
		t.Fatal("failed remove changed memory")
	}
	unchanged, _ = os.ReadFile(path)
	if !reflect.DeepEqual(unchanged, original) {
		t.Fatal("failed remove changed roster file")
	}
}

func TestMalformedRosterRemainsUnavailableAndBytePreserved(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wallets.json")
	original := []byte(`{"broken"`)
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WATCH_STORE", path)
	t.Setenv("BALANCE_HISTORY", filepath.Join(dir, "history.json"))
	store := NewWatchStore(dir)
	if !errors.Is(store.Err(), ErrWatchUnavailable) || len(store.Entries()) != 0 {
		t.Fatalf("malformed store state = %v, %#v", store.Err(), store.Entries())
	}
	if _, err := store.Add("Cold", genesisAddress, ""); !errors.Is(err, ErrWatchUnavailable) {
		t.Fatalf("malformed add error = %v", err)
	}
	unchanged, _ := os.ReadFile(path)
	if !reflect.DeepEqual(unchanged, original) {
		t.Fatal("malformed roster was overwritten")
	}
}

func TestNullRosterRemainsUnavailableAndBytePreserved(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wallets.json")
	original := []byte("null")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WATCH_STORE", path)
	t.Setenv("BALANCE_HISTORY", filepath.Join(dir, "history.json"))
	store := NewWatchStore(dir)
	if !errors.Is(store.Err(), ErrWatchUnavailable) || len(store.Entries()) != 0 {
		t.Fatalf("null store state = %v, %#v", store.Err(), store.Entries())
	}
	if _, err := store.Add("Cold", genesisAddress, ""); !errors.Is(err, ErrWatchUnavailable) {
		t.Fatalf("null roster add error = %v", err)
	}
	unchanged, _ := os.ReadFile(path)
	if !reflect.DeepEqual(unchanged, original) {
		t.Fatal("null roster was overwritten")
	}
}

func TestInitialSeedWriteFailureMarksStoreUnavailable(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WATCH_STORE", filepath.Join(dir, "wallets.json"))
	t.Setenv("BALANCE_HISTORY", filepath.Join(dir, "history.json"))
	originalWriter := writeWatchFile
	writeWatchFile = func(string, []byte) error { return ErrWatchUnavailable }
	t.Cleanup(func() { writeWatchFile = originalWriter })
	store := NewWatchStore(dir)
	if !errors.Is(store.Err(), ErrWatchUnavailable) {
		t.Fatalf("seed write store error = %v", store.Err())
	}
}

func TestSanitizedLegacyWalletNameCollisionRejected(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WATCH_STORE", filepath.Join(dir, "wallets.json"))
	t.Setenv("BALANCE_HISTORY", filepath.Join(dir, "history.json"))
	store := NewWatchStore(dir)
	if _, err := store.Add("A/B", genesisAddress, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Add("A?B", parityZpub, ""); err == nil || !strings.Contains(err.Error(), "conflicts") {
		t.Fatalf("sanitized name collision error = %v", err)
	}
}

func TestWatchStoreCRUDAndStaleBalance(t *testing.T) {
	t.Setenv("WATCH_STORE", filepath.Join(t.TempDir(), "wallets.json"))
	t.Setenv("BALANCE_HISTORY", filepath.Join(t.TempDir(), "history.json"))
	store := NewWatchStore(t.TempDir())
	key := parityZpub
	if _, err := store.Add("Cold", key, "2021-01-01"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Add("cold", key, ""); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("duplicate error = %v", err)
	}
	if _, err := store.Add("Other", key, ""); err == nil || !strings.Contains(err.Error(), "already configured") {
		t.Fatalf("duplicate key error = %v", err)
	}
	store.history.record(key, 1.25, time.Unix(0, 0))
	walletRPC := func(wallet, method string, params []any, timeout time.Duration) any { return nil }
	view := store.List(walletRPC, false)
	rows := view["wallets"].([]map[string]any)
	if rows[0]["state"] != "stale" || rows[0]["btc"] != "1.25" {
		t.Fatalf("stale row = %#v", rows[0])
	}
	removed, err := store.Remove("Cold")
	removedAgain, errAgain := store.Remove("Cold")
	if !removed || err != nil || removedAgain || errAgain != nil {
		t.Fatal("remove result mismatch")
	}
	if got := store.history.series(key); !reflect.DeepEqual(got, []float64{1.25}) {
		t.Fatalf("history after removal = %v", got)
	}
}

func TestWatchStoreAcceptsFiftyWallets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wallets.json")
	fillers := make([]WatchEntry, 0, 48)
	for i := range 48 {
		fillers = append(fillers, WatchEntry{Name: fmt.Sprintf("Filler %d", i), Key: fmt.Sprintf("wpkh(%s/%d/*)", testXpub, i)})
	}
	b, err := json.Marshal(fillers)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WATCH_STORE", path)
	t.Setenv("BALANCE_HISTORY", filepath.Join(t.TempDir(), "history.json"))
	store := NewWatchStore(t.TempDir())
	if _, err := store.Add("Forty-ninth", genesisAddress, ""); err != nil {
		t.Fatalf("49th wallet: %v", err)
	}
	if _, err := store.Add("Fiftieth", parityZpub, ""); err != nil {
		t.Fatalf("50th wallet: %v", err)
	}
	if _, err := store.Add("Fifty-first", testXpub, ""); err == nil || err.Error() != "At most 50 wallets." {
		t.Fatalf("51st wallet error = %v", err)
	}
}

func TestLegacyDuplicateKeysAreDeduplicatedUntilNextMutation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wallets.json")
	original, err := json.Marshal([]WatchEntry{{Name: "First", Key: genesisAddress}, {Name: "Second", Key: "  " + genesisAddress + "  "}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WATCH_STORE", path)
	t.Setenv("BALANCE_HISTORY", filepath.Join(dir, "history.json"))
	store := NewWatchStore(dir)
	if got := store.Entries(); len(got) != 1 || got[0].Name != "First" {
		t.Fatalf("deduplicated entries = %#v", got)
	}
	unchanged, _ := os.ReadFile(path)
	if !reflect.DeepEqual(unchanged, original) {
		t.Fatal("load rewrote legacy duplicate roster")
	}
	walletRPC := func(wallet, method string, params []any, timeout time.Duration) any {
		switch method {
		case "getwalletinfo":
			return watchInfo(false)
		case "getdescriptorinfo":
			return descriptorInfo(params)
		case "listdescriptors":
			return listedDescriptors(WatchEntry{Name: "First", Key: genesisAddress})
		case "getbalances":
			return map[string]any{"mine": map[string]any{"trusted": 1.0}}
		}
		return nil
	}
	view := store.List(walletRPC, false)
	if rows := view["wallets"].([]map[string]any); len(rows) != 1 || view["total"] != "1" {
		t.Fatalf("duplicate roster view = %#v", view)
	}
	if removed, err := store.Remove("First"); !removed || err != nil {
		t.Fatal("deduplicated entry was not removed")
	}
	persisted, _ := os.ReadFile(path)
	if string(persisted) != "[]" {
		t.Fatalf("post-mutation roster = %s", persisted)
	}
}

func TestPendingWalletAvoidsFalseZeroAndKeepsGenesisIdentity(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WATCH_STORE", filepath.Join(dir, "wallets.json"))
	t.Setenv("BALANCE_HISTORY", filepath.Join(dir, "history.json"))
	store := NewWatchStore(dir)
	if _, err := store.Add("Satoshi", genesisAddress, ""); err != nil {
		t.Fatal(err)
	}
	store.SetPending(genesisAddress, true)
	scanning := false
	balanceCalls := 0
	listCalls := 0
	walletRPC := func(wallet, method string, params []any, timeout time.Duration) any {
		switch method {
		case "getwalletinfo":
			if scanning {
				return watchInfo(map[string]any{"progress": 0.5})
			}
			return watchInfo(false)
		case "listdescriptors":
			listCalls++
			return listedDescriptors(WatchEntry{Name: "Satoshi", Key: genesisAddress})
		case "getdescriptorinfo":
			return descriptorInfo(params)
		case "getbalances":
			balanceCalls++
			return map[string]any{"mine": map[string]any{"trusted": 0.0}}
		}
		return nil
	}
	rows := store.List(walletRPC, false)["wallets"].([]map[string]any)
	if rows[0]["state"] != "pending" || rows[0]["btc"] != nil || rows[0]["display_name"] != "Genesis" || rows[0]["name"] != "Satoshi" {
		t.Fatalf("pending Genesis row = %#v", rows[0])
	}
	store.Sample(walletRPC)
	if balanceCalls != 0 || len(store.history.series(genesisAddress)) != 0 {
		t.Fatal("pending wallet sampled a false zero balance")
	}
	scanning = true
	rows = store.List(walletRPC, false)["wallets"].([]map[string]any)
	if rows[0]["state"] != "scanning" {
		t.Fatalf("pending scanning row = %#v", rows[0])
	}
	scanning = false
	store.SetPending(genesisAddress, false)
	view := store.List(walletRPC, true)
	rows = view["wallets"].([]map[string]any)
	if rows[0]["state"] != "pending" || view["total_state"] != "pending" || balanceCalls != 0 {
		t.Fatalf("IBD Genesis view = %#v", view)
	}
	rows = store.List(walletRPC, false)["wallets"].([]map[string]any)
	if rows[0]["state"] != "ok" || rows[0]["btc"] != "0" || balanceCalls != 1 || listCalls == 0 {
		t.Fatalf("completed Genesis row = %#v, balance calls = %d, descriptor checks = %d", rows[0], balanceCalls, listCalls)
	}
}

func TestPublicExampleIsExcludedFromPersonalTotals(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wallets.json")
	entries := []WatchEntry{
		{Name: publicExampleName, Key: publicExampleAddress},
		{Name: "First", Key: genesisAddress},
		{Name: "Second", Key: parityZpub},
	}
	b, err := json.Marshal(entries)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WATCH_STORE", path)
	t.Setenv("BALANCE_HISTORY", filepath.Join(dir, "history.json"))
	store := NewWatchStore(dir)
	store.SetPending(publicExampleAddress, true)
	walletRPC := func(wallet, method string, params []any, timeout time.Duration) any {
		var entry WatchEntry
		for _, candidate := range entries {
			if wallet == coreWalletName(candidate) {
				entry = candidate
				break
			}
		}
		switch method {
		case "getwalletinfo":
			return watchInfo(false)
		case "listdescriptors":
			return listedDescriptors(entry)
		case "getdescriptorinfo":
			return descriptorInfo(params)
		case "getbalances":
			balance := 1.0
			if entry.Name == "Second" {
				balance = 2.0
			}
			return map[string]any{"mine": map[string]any{"trusted": balance}}
		}
		return nil
	}
	view := store.List(walletRPC, false)
	rows := view["wallets"].([]map[string]any)
	if rows[0]["example"] != true || rows[0]["display_name"] != "Binance" || rows[0]["state"] != "pending" {
		t.Fatalf("public example row = %#v", rows[0])
	}
	if view["total"] != "3" || view["total_state"] != "ok" || view["show_total"] != true {
		t.Fatalf("personal total includes public example or its pending state: %#v", view)
	}
}

func TestMissingBalanceResponseUsesErrorAndStaleStates(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WATCH_STORE", filepath.Join(dir, "wallets.json"))
	t.Setenv("BALANCE_HISTORY", filepath.Join(dir, "history.json"))
	store := NewWatchStore(dir)
	if _, err := store.Add("Cold", parityZpub, ""); err != nil {
		t.Fatal(err)
	}
	walletRPC := func(wallet, method string, params []any, timeout time.Duration) any {
		if method == "getwalletinfo" {
			return watchInfo(false)
		}
		if method == "listdescriptors" {
			return listedDescriptors(WatchEntry{Name: "Cold", Key: parityZpub})
		}
		if method == "getdescriptorinfo" {
			return descriptorInfo(params)
		}
		return nil
	}
	view := store.List(walletRPC, false)
	rows := view["wallets"].([]map[string]any)
	if rows[0]["state"] != "error" || rows[0]["btc"] != nil {
		t.Fatalf("missing balance row = %#v", rows[0])
	}
	if view["total_state"] != "unavailable" {
		t.Fatalf("missing balance total state = %v", view["total_state"])
	}
	store.history.record(parityZpub, 1.25, time.Unix(0, 0))
	rows = store.List(walletRPC, false)["wallets"].([]map[string]any)
	if rows[0]["state"] != "stale" || rows[0]["btc"] != "1.25" {
		t.Fatalf("missing balance stale row = %#v", rows[0])
	}
}

func TestInvalidTrustedBalancesAreErrors(t *testing.T) {
	for _, trusted := range []any{nil, "1", math.NaN(), math.Inf(1), -1.0} {
		dir := t.TempDir()
		t.Setenv("WATCH_STORE", filepath.Join(dir, "wallets.json"))
		t.Setenv("BALANCE_HISTORY", filepath.Join(dir, "history.json"))
		store := NewWatchStore(dir)
		if _, err := store.Add("Cold", genesisAddress, ""); err != nil {
			t.Fatal(err)
		}
		walletRPC := func(wallet, method string, params []any, timeout time.Duration) any {
			switch method {
			case "getwalletinfo":
				return watchInfo(false)
			case "getdescriptorinfo":
				return descriptorInfo(params)
			case "listdescriptors":
				return listedDescriptors(WatchEntry{Name: "Cold", Key: genesisAddress})
			case "getbalances":
				return map[string]any{"mine": map[string]any{"trusted": trusted}}
			}
			return nil
		}
		row := store.List(walletRPC, false)["wallets"].([]map[string]any)[0]
		if row["state"] != "error" || row["btc"] != nil {
			t.Fatalf("trusted %v row = %#v", trusted, row)
		}
	}
}

func TestChangedKeyNeverReadsMismatchedLegacyBalance(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WATCH_STORE", filepath.Join(dir, "wallets.json"))
	t.Setenv("BALANCE_HISTORY", filepath.Join(dir, "history.json"))
	store := NewWatchStore(dir)
	entry, err := store.Add("Ledger", parityZpub, "")
	if err != nil {
		t.Fatal(err)
	}
	oldEntry := WatchEntry{Name: "Ledger", Key: genesisAddress}
	balanceCalls := 0
	walletRPC := func(wallet, method string, params []any, timeout time.Duration) any {
		if wallet != walletName(entry.Name) {
			return nil
		}
		switch method {
		case "getwalletinfo":
			return watchInfo(false)
		case "listdescriptors":
			return listedDescriptors(oldEntry)
		case "getdescriptorinfo":
			return descriptorInfo(params)
		case "getbalances":
			balanceCalls++
			return map[string]any{"mine": map[string]any{"trusted": 57.0}}
		}
		return nil
	}
	rows := store.List(walletRPC, false)["wallets"].([]map[string]any)
	if rows[0]["state"] != "error" || rows[0]["btc"] != nil || balanceCalls != 0 {
		t.Fatalf("mismatched legacy row = %#v, balance calls = %d", rows[0], balanceCalls)
	}
}

func TestPendingIdentityDoesNotFollowReusedLabel(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WATCH_STORE", filepath.Join(dir, "wallets.json"))
	t.Setenv("BALANCE_HISTORY", filepath.Join(dir, "history.json"))
	store := NewWatchStore(dir)
	if _, err := store.Add("Ledger", genesisAddress, ""); err != nil {
		t.Fatal(err)
	}
	store.SetPending(genesisAddress, true)
	if removed, err := store.Remove("Ledger"); !removed || err != nil {
		t.Fatal("old Ledger was not removed")
	}
	if _, err := store.Add("Ledger", parityZpub, ""); err != nil {
		t.Fatal(err)
	}
	_, pending := store.snapshot()
	if pending[watchKeyID(parityZpub)] || len(pending) != 0 {
		t.Fatalf("pending state followed reused label: %#v", pending)
	}
}

func TestRemoveExactDoesNotDeleteSameLabelReplacement(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WATCH_STORE", filepath.Join(dir, "wallets.json"))
	t.Setenv("BALANCE_HISTORY", filepath.Join(dir, "history.json"))
	store := NewWatchStore(dir)
	old, err := store.Add("Ledger", genesisAddress, "")
	if err != nil {
		t.Fatal(err)
	}
	if removed, err := store.RemoveExact(old); !removed || err != nil {
		t.Fatal("old entry was not removed")
	}
	replacement, err := store.Add("Ledger", parityZpub, "2021-01-01")
	if err != nil {
		t.Fatal(err)
	}
	store.SetPending(replacement.Key, true)
	store.SetCoreWallet(replacement.Key, coreWalletName(replacement))
	if removed, err := store.RemoveExact(old); removed || err != nil {
		t.Fatal("stale exact removal deleted a replacement")
	}
	if got := store.Entries(); !reflect.DeepEqual(got, []WatchEntry{replacement}) {
		t.Fatalf("entries after stale removal = %#v", got)
	}
	_, pending := store.snapshot()
	if !pending[watchKeyID(replacement.Key)] {
		t.Fatal("stale removal cleared replacement pending state")
	}
	store.mu.Lock()
	coreName := store.coreNames[watchKeyID(replacement.Key)]
	store.mu.Unlock()
	if coreName != coreWalletName(replacement) {
		t.Fatal("stale removal cleared replacement Core route")
	}
}

func TestProvisionCreatesWatchOnlyWallet(t *testing.T) {
	var nodeCalls []rpcCall
	var walletCalls []walletCall
	rpc := func(method string, params []any) any {
		nodeCalls = append(nodeCalls, rpcCall{method, params})
		switch method {
		case "createwallet":
			return map[string]any{"name": params[0]}
		case "getdescriptorinfo":
			return map[string]any{"descriptor": params[0].(string) + "#test", "isrange": true, "hasprivatekeys": false}
		}
		return nil
	}
	walletRPC := func(wallet, method string, params []any, timeout time.Duration) any {
		walletCalls = append(walletCalls, walletCall{wallet, method, params, timeout})
		if method == "importdescriptors" {
			results := make([]any, len(params[0].([]any)))
			for index := range results {
				results[index] = map[string]any{"success": true}
			}
			return results
		}
		return nil
	}
	ProvisionOne(rpc, walletRPC, WatchEntry{Name: "Cold", Key: parityZpub, Birthday: "2021-01-01"}, false)
	var create []any
	for _, call := range nodeCalls {
		if call.method == "createwallet" {
			create = call.params
		}
	}
	if len(create) != 7 || create[1] != true || create[5] != true || create[6] != true {
		t.Fatalf("createwallet params = %#v", create)
	}
	if got := walletCalls[len(walletCalls)-1]; got.method != "importdescriptors" || got.timeout != time.Hour {
		t.Fatalf("import call = %#v", got)
	}
}

func TestProvisionReusesOnlyDescriptorMatchingLegacyWallet(t *testing.T) {
	entry := WatchEntry{Name: "Satoshi", Key: genesisAddress}
	var nodeCalls []rpcCall
	rpc := func(method string, params []any) any {
		nodeCalls = append(nodeCalls, rpcCall{method, params})
		return nil
	}
	walletRPC := func(wallet, method string, params []any, timeout time.Duration) any {
		if wallet != walletName(entry.Name) {
			return nil
		}
		if method == "getwalletinfo" {
			return watchInfo(false)
		}
		if method == "listdescriptors" {
			return listedDescriptors(entry)
		}
		if method == "getdescriptorinfo" {
			return descriptorInfo(params)
		}
		return nil
	}
	name, ok := ProvisionOne(rpc, walletRPC, entry, false)
	if !ok || name != walletName(entry.Name) || len(nodeCalls) != 0 {
		t.Fatalf("legacy reuse = %q, %v; node calls = %#v", name, ok, nodeCalls)
	}
}

func TestProvisionChangedKeyUsesHashWalletAndPreservesLegacy(t *testing.T) {
	oldEntry := WatchEntry{Name: "Ledger", Key: genesisAddress}
	newEntry := WatchEntry{Name: "Ledger", Key: parityZpub}
	for _, loaded := range []bool{false, true} {
		t.Run(map[bool]string{false: "unloaded", true: "loaded"}[loaded], func(t *testing.T) {
			var created string
			var unloaded []string
			rpc := func(method string, params []any) any {
				switch method {
				case "loadwallet":
					if params[0] == walletName(oldEntry.Name) && !loaded {
						return map[string]any{"name": params[0]}
					}
				case "createwallet":
					created = params[0].(string)
					return map[string]any{"name": created}
				case "getdescriptorinfo":
					return map[string]any{"descriptor": params[0], "isrange": true, "hasprivatekeys": false}
				case "unloadwallet":
					unloaded = append(unloaded, params[0].(string))
				}
				return nil
			}
			walletRPC := func(wallet, method string, params []any, timeout time.Duration) any {
				if method == "getdescriptorinfo" {
					return descriptorInfo(params)
				}
				if wallet == walletName(oldEntry.Name) {
					if method == "getwalletinfo" && loaded {
						return watchInfo(false)
					}
					if method == "listdescriptors" {
						return listedDescriptors(oldEntry)
					}
				}
				if wallet == coreWalletName(newEntry) && method == "importdescriptors" {
					results := make([]any, len(params[0].([]any)))
					for index := range results {
						results[index] = map[string]any{"success": true}
					}
					return results
				}
				return nil
			}
			name, ok := ProvisionOne(rpc, walletRPC, newEntry, false)
			if !ok || name != coreWalletName(newEntry) || created != coreWalletName(newEntry) {
				t.Fatalf("changed-key provision = %q, %v; created %q", name, ok, created)
			}
			if loaded && len(unloaded) != 0 || !loaded && !reflect.DeepEqual(unloaded, []string{walletName(oldEntry.Name)}) {
				t.Fatalf("loaded=%v unloads = %v", loaded, unloaded)
			}
		})
	}
}

func TestProvisionHashWalletMismatchFailsClosed(t *testing.T) {
	entry := WatchEntry{Name: "Ledger", Key: parityZpub}
	wrong := WatchEntry{Name: "Other", Key: genesisAddress}
	created := false
	rpc := func(method string, params []any) any {
		switch method {
		case "createwallet":
			created = true
		case "getdescriptorinfo":
			return descriptorInfo(params)
		case "unloadwallet":
			t.Fatal("preloaded mismatched wallet was unloaded")
		}
		return nil
	}
	walletRPC := func(wallet, method string, params []any, timeout time.Duration) any {
		if wallet != coreWalletName(entry) {
			return nil
		}
		if method == "getwalletinfo" {
			return watchInfo(false)
		}
		if method == "listdescriptors" {
			return listedDescriptors(wrong)
		}
		if method == "getdescriptorinfo" {
			return descriptorInfo(params)
		}
		return nil
	}
	if name, ok := ProvisionOne(rpc, walletRPC, entry, false); ok || name != "" || created {
		t.Fatalf("mismatched hash wallet = %q, %v; created=%v", name, ok, created)
	}
}

func TestProvisionRetriesCompatiblePartialHashWallet(t *testing.T) {
	entry := WatchEntry{Name: "Cold", Key: parityZpub, Birthday: "2021-01-01"}
	walletExists := false
	listed := []any{}
	createCalls := 0
	importSizes := []int{}
	rpc := func(method string, params []any) any {
		switch method {
		case "createwallet":
			createCalls++
			walletExists = true
			return map[string]any{"name": params[0]}
		case "getdescriptorinfo":
			return descriptorInfo(params)
		}
		return nil
	}
	walletRPC := func(wallet, method string, params []any, timeout time.Duration) any {
		if wallet != coreWalletName(entry) || !walletExists {
			return nil
		}
		switch method {
		case "getwalletinfo":
			return watchInfo(false)
		case "getdescriptorinfo":
			return descriptorInfo(params)
		case "listdescriptors":
			return map[string]any{"descriptors": append([]any(nil), listed...)}
		case "importdescriptors":
			requests := params[0].([]any)
			importSizes = append(importSizes, len(requests))
			results := make([]any, len(requests))
			for index, value := range requests {
				request := mapValue(value)
				if len(importSizes) == 1 && index == 1 {
					results[index] = map[string]any{"success": false}
					continue
				}
				item := map[string]any{
					"desc": request["desc"], "internal": request["internal"], "timestamp": request["timestamp"],
				}
				if _, ranged := request["range"]; ranged {
					item["range"] = []any{float64(0), float64(999)}
				}
				listed = append(listed, item)
				results[index] = map[string]any{"success": true}
			}
			return results
		}
		return nil
	}
	if name, ok := ProvisionOne(rpc, walletRPC, entry, false); ok || name != "" {
		t.Fatalf("partial first import = %q, %v", name, ok)
	}
	name, ok := ProvisionOne(rpc, walletRPC, entry, false)
	if !ok || name != coreWalletName(entry) {
		t.Fatalf("retry = %q, %v", name, ok)
	}
	if createCalls != 1 || !reflect.DeepEqual(importSizes, []int{2, 1}) || len(listed) != 2 {
		t.Fatalf("create calls=%d import sizes=%v descriptors=%#v", createCalls, importSizes, listed)
	}
}

func TestProvisionRetriesEmptyHashWallet(t *testing.T) {
	entry := WatchEntry{Name: "Cold", Key: parityZpub}
	imported := 0
	rpc := func(method string, params []any) any {
		switch method {
		case "getdescriptorinfo":
			return descriptorInfo(params)
		case "createwallet", "unloadwallet":
			t.Fatalf("unexpected %s", method)
		}
		return nil
	}
	walletRPC := func(wallet, method string, params []any, timeout time.Duration) any {
		if wallet != coreWalletName(entry) {
			return nil
		}
		switch method {
		case "getwalletinfo":
			return watchInfo(false)
		case "getdescriptorinfo":
			return descriptorInfo(params)
		case "listdescriptors":
			return map[string]any{"descriptors": []any{}}
		case "importdescriptors":
			requests := params[0].([]any)
			imported = len(requests)
			return []any{map[string]any{"success": true}, map[string]any{"success": true}}
		}
		return nil
	}
	name, ok := ProvisionOne(rpc, walletRPC, entry, false)
	if !ok || name != coreWalletName(entry) || imported != 2 {
		t.Fatalf("empty retry = %q, %v; imported=%d", name, ok, imported)
	}
}

func TestProvisionDoesNotRetryHashWalletOnPrunedNode(t *testing.T) {
	entry := WatchEntry{Name: "Cold", Key: parityZpub}
	imported := false
	rpc := func(method string, params []any) any {
		if method == "loadwallet" && params[0] == walletName(entry.Name) {
			return nil
		}
		t.Fatalf("unexpected node RPC %s", method)
		return nil
	}
	walletRPC := func(wallet, method string, params []any, timeout time.Duration) any {
		if wallet != coreWalletName(entry) {
			return nil
		}
		switch method {
		case "getwalletinfo":
			return watchInfo(false)
		case "getdescriptorinfo":
			return descriptorInfo(params)
		case "listdescriptors":
			return map[string]any{"descriptors": []any{}}
		case "importdescriptors":
			imported = true
		}
		return nil
	}
	if name, ok := ProvisionOne(rpc, walletRPC, entry, true); ok || name != "" || imported {
		t.Fatalf("pruned retry = %q, %v; imported=%v", name, ok, imported)
	}
}

func TestProvisionRejectsUnverifiableHashWalletRetries(t *testing.T) {
	entry := WatchEntry{Name: "Cold", Key: parityZpub, Birthday: "2021-01-01"}
	valid := listedDescriptors(entry)["descriptors"].([]any)[0].(map[string]any)
	tests := []struct {
		name string
		info map[string]any
		item map[string]any
	}{
		{name: "private keys enabled", info: map[string]any{"descriptors": true, "private_keys_enabled": true}, item: valid},
		{name: "missing required timestamp", info: watchInfo(false), item: map[string]any{"desc": valid["desc"], "internal": false, "range": valid["range"]}},
		{name: "invalid optional role", info: watchInfo(false), item: map[string]any{"desc": valid["desc"], "internal": "false", "timestamp": valid["timestamp"], "range": valid["range"]}},
		{name: "foreign descriptor", info: watchInfo(false), item: map[string]any{"desc": "addr(" + genesisAddress + ")#foreign", "internal": false, "timestamp": 0}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			imported := false
			unloaded := false
			rpc := func(method string, params []any) any {
				switch method {
				case "getdescriptorinfo":
					return descriptorInfo(params)
				case "unloadwallet":
					unloaded = true
				}
				return nil
			}
			walletRPC := func(wallet, method string, params []any, timeout time.Duration) any {
				if wallet != coreWalletName(entry) {
					return nil
				}
				switch method {
				case "getwalletinfo":
					return tt.info
				case "getdescriptorinfo":
					return descriptorInfo(params)
				case "listdescriptors":
					return map[string]any{"descriptors": []any{tt.item}}
				case "importdescriptors":
					imported = true
				}
				return nil
			}
			if name, ok := ProvisionOne(rpc, walletRPC, entry, false); ok || name != "" || imported || unloaded {
				t.Fatalf("retry = %q, %v; imported=%v unloaded=%v", name, ok, imported, unloaded)
			}
		})
	}
}

func TestHotLegacyCollisionIsNeverReusedOrUnexpectedlyUnloaded(t *testing.T) {
	entry := WatchEntry{Name: "Ledger", Key: parityZpub}
	for _, preloaded := range []bool{false, true} {
		t.Run(map[bool]string{false: "unloaded", true: "loaded"}[preloaded], func(t *testing.T) {
			loaded := preloaded
			unloads := 0
			rpc := func(method string, params []any) any {
				switch method {
				case "loadwallet":
					if params[0] == walletName(entry.Name) && !loaded {
						loaded = true
						return map[string]any{"name": params[0]}
					}
				case "unloadwallet":
					unloads++
					loaded = false
				case "createwallet":
					return map[string]any{"name": params[0]}
				case "getdescriptorinfo":
					return descriptorInfo(params)
				}
				return nil
			}
			walletRPC := func(wallet, method string, params []any, timeout time.Duration) any {
				if wallet == walletName(entry.Name) && method == "getwalletinfo" && loaded {
					return map[string]any{"descriptors": true, "private_keys_enabled": true, "scanning": false}
				}
				if wallet == coreWalletName(entry) && method == "importdescriptors" {
					results := make([]any, len(params[0].([]any)))
					for index := range results {
						results[index] = map[string]any{"success": true}
					}
					return results
				}
				return nil
			}
			name, ok := ProvisionOne(rpc, walletRPC, entry, false)
			if !ok || name != coreWalletName(entry) {
				t.Fatalf("hot collision provision = %q, %v", name, ok)
			}
			if preloaded && unloads != 0 || !preloaded && unloads != 1 {
				t.Fatalf("preloaded=%v unloads=%d", preloaded, unloads)
			}
		})
	}
}

func TestWalletMatchChecksInternalRoleAndBirthdayCoverage(t *testing.T) {
	entry := WatchEntry{Name: "Cold", Key: parityZpub, Birthday: "2021-01-01"}
	listed := listedDescriptors(entry)
	walletRPC := func(wallet, method string, params []any, timeout time.Duration) any {
		if method == "getdescriptorinfo" {
			return descriptorInfo(params)
		}
		return listed
	}
	if !walletMatches(walletRPC, "watch_test", entry) {
		t.Fatal("matching descriptors were rejected")
	}
	items := listed["descriptors"].([]any)
	first := items[0].(map[string]any)
	for _, value := range items {
		delete(value.(map[string]any), "internal")
	}
	if !walletMatches(walletRPC, "watch_test", entry) {
		t.Fatal("inactive descriptors without internal metadata were rejected")
	}
	for index, value := range items {
		value.(map[string]any)["internal"] = index == 1
	}
	first["internal"] = !first["internal"].(bool)
	if walletMatches(walletRPC, "watch_test", entry) {
		t.Fatal("receive/change descriptor role mismatch was accepted")
	}
	first["internal"] = !first["internal"].(bool)
	delete(first, "range")
	if walletMatches(walletRPC, "watch_test", entry) {
		t.Fatal("ranged descriptor without range metadata was accepted")
	}
	first["range"] = []any{float64(0), float64(998)}
	if walletMatches(walletRPC, "watch_test", entry) {
		t.Fatal("insufficient descriptor range was accepted")
	}
	first["range"] = []any{float64(0), float64(999)}
	first["timestamp"] = birthdayTimestamp(entry.Birthday) + 1
	if walletMatches(walletRPC, "watch_test", entry) {
		t.Fatal("wallet with later descriptor timestamp was treated as complete")
	}
}

func TestProvisionReusesInactiveAccountDescriptorsWithoutInternalMetadata(t *testing.T) {
	entry := WatchEntry{Name: "Ledger", Key: parityZpub, Birthday: "2021-01-01"}
	listed := listedDescriptors(entry)
	for _, value := range listed["descriptors"].([]any) {
		delete(value.(map[string]any), "internal")
	}
	created := false
	imported := false
	rpc := func(method string, params []any) any {
		if method == "createwallet" {
			created = true
		}
		return nil
	}
	walletRPC := func(wallet, method string, params []any, timeout time.Duration) any {
		switch method {
		case "getwalletinfo":
			return watchInfo(false)
		case "getdescriptorinfo":
			return descriptorInfo(params)
		case "listdescriptors":
			return listed
		case "importdescriptors":
			imported = true
		}
		return nil
	}
	name, ok := ProvisionOne(rpc, walletRPC, entry, false)
	if !ok || name != walletName(entry.Name) || created || imported {
		t.Fatalf("inactive account reuse = %q, %v; created=%v imported=%v", name, ok, created, imported)
	}
}

func TestProvisionReusesCoreCanonicalizedPublicDescriptor(t *testing.T) {
	xpub, _, err := toXpub(parityZpub)
	if err != nil {
		t.Fatal(err)
	}
	raw := "wpkh([d34db33f/84'/0'/0']" + xpub + "/0/*)"
	canonical := strings.ReplaceAll(raw, "'", "h")
	entry := WatchEntry{Name: "Exported", Key: raw}
	created := false
	rpc := func(method string, params []any) any {
		if method == "createwallet" {
			created = true
		}
		return nil
	}
	walletRPC := func(wallet, method string, params []any, timeout time.Duration) any {
		switch method {
		case "getwalletinfo":
			return watchInfo(false)
		case "getdescriptorinfo":
			return map[string]any{"descriptor": strings.ReplaceAll(params[0].(string), "'", "h") + "#canonical", "hasprivatekeys": false}
		case "listdescriptors":
			return map[string]any{"descriptors": []any{map[string]any{"desc": canonical + "#stored", "internal": false, "timestamp": 0, "range": []any{float64(0), float64(999)}}}}
		}
		return nil
	}
	name, ok := ProvisionOne(rpc, walletRPC, entry, false)
	if !ok || name != walletName(entry.Name) || created {
		t.Fatalf("canonical legacy reuse = %q, %v; created=%v", name, ok, created)
	}
}

func TestProvisionRequiresCompleteSuccessfulImport(t *testing.T) {
	entry := WatchEntry{Name: "Cold", Key: parityZpub}
	for _, mode := range []string{"missing descriptor info", "partial import failure"} {
		t.Run(mode, func(t *testing.T) {
			descriptorCalls := 0
			importCalls := 0
			createCalls := 0
			rpc := func(method string, params []any) any {
				switch method {
				case "createwallet":
					createCalls++
					return map[string]any{"name": params[0]}
				case "getdescriptorinfo":
					descriptorCalls++
					if mode == "missing descriptor info" && descriptorCalls == 2 {
						return nil
					}
					return descriptorInfo(params)
				}
				return nil
			}
			walletRPC := func(wallet, method string, params []any, timeout time.Duration) any {
				if method != "importdescriptors" {
					return nil
				}
				importCalls++
				return []any{map[string]any{"success": true}, map[string]any{"success": false}}
			}
			if name, ok := ProvisionOne(rpc, walletRPC, entry, false); ok || name != "" {
				t.Fatalf("incomplete import reported success: %q, %v", name, ok)
			}
			if mode == "missing descriptor info" && importCalls != 0 {
				t.Fatal("partial descriptor request was imported")
			}
			if mode == "missing descriptor info" && createCalls != 0 {
				t.Fatal("wallet was created before descriptor validation completed")
			}
		})
	}
}

type rpcCall struct {
	method string
	params []any
}

type walletCall struct {
	wallet  string
	method  string
	params  []any
	timeout time.Duration
}

func listedDescriptors(entry WatchEntry) map[string]any {
	descriptors, _ := descriptorsFor(entry.Key)
	items := make([]any, 0, len(descriptors))
	for _, item := range descriptors {
		listed := map[string]any{
			"desc": item.Desc + "#checksum", "internal": item.Internal,
			"timestamp": birthdayTimestamp(entry.Birthday),
		}
		if strings.Contains(item.Desc, "/*") {
			listed["range"] = []any{float64(0), float64(999)}
		}
		items = append(items, listed)
	}
	return map[string]any{"descriptors": items}
}

func watchInfo(scanning any) map[string]any {
	return map[string]any{"scanning": scanning, "descriptors": true, "private_keys_enabled": false}
}

func descriptorInfo(params []any) map[string]any {
	return map[string]any{"descriptor": params[0].(string) + "#canonical", "isrange": strings.Contains(params[0].(string), "/*"), "hasprivatekeys": false}
}

func syntheticExtendedPrivate(version string) string {
	payload := make([]byte, 78)
	copy(payload, mustHex(version))
	for index := 4; index < len(payload); index++ {
		payload[index] = byte(index)
	}
	return base58EncodeCheck(payload)
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s mode = %o, want %o", path, got, want)
	}
}

const parityZpub = "zpub6rFR7y4Q2AijBEqTUquhVz398htDFrtymD9xYYfG1m4wAcvPhXNfE3EfH1r1ADqtfSdVCToUG868RvUUkgDKf31mGDtKsAYz2oz2AGutZYs"
