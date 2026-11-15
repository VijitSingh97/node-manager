package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHardCodedPrunedModesMatchProductConfig(t *testing.T) {
	root := filepath.Join("..", "..")
	read := func(rel string) string {
		b, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("product config unreadable: %v", err)
		}
		return string(b)
	}
	if !strings.Contains(read("build/monero/entrypoint.sh"), "\nprune-blockchain=1\n") {
		t.Fatal("monerod must always run pruned: dashboard reports Monero as pruned")
	}
	for _, name := range []string{"zakurad.toml", "zakurad-address-index.toml"} {
		if !strings.Contains(read("build/zcash/"+name), "[state.storage_mode.pruned]") {
			t.Fatalf("%s must use pruned storage: dashboard reports Zcash as pruned", name)
		}
	}
	if !strings.Contains(read("configure.sh"), "'' | 0 | *[!0-9]*) echo \"tari.pruning_horizon must be a positive whole number.\"") {
		t.Fatal("configure.sh must reject tari.pruning_horizon 0: dashboard reports Tari as pruned")
	}
}

func TestFormatUptimeMatchesClientFormat(t *testing.T) {
	for seconds, want := range map[float64]string{0: "0m", 300: "5m", 3660: "1h 1m", 90061: "1d 1h 1m"} {
		if got := formatUptime(seconds); got != want {
			t.Fatalf("formatUptime(%v) = %q, want %q", seconds, got, want)
		}
	}
}
