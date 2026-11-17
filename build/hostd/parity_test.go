package main

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

func toAny(t *testing.T, v any) any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func compare(path string, got, want any, diffs *[]string) {
	switch w := want.(type) {
	case map[string]any:
		g, ok := got.(map[string]any)
		if !ok {
			*diffs = append(*diffs, fmt.Sprintf("%s: want object, got %T", path, got))
			return
		}
		keys := map[string]bool{}
		for k := range w {
			keys[k] = true
		}
		for k := range g {
			keys[k] = true
		}
		sorted := make([]string, 0, len(keys))
		for k := range keys {
			sorted = append(sorted, k)
		}
		sort.Strings(sorted)
		for _, k := range sorted {
			gv, gok := g[k]
			wv, wok := w[k]
			switch {
			case !gok:
				*diffs = append(*diffs, fmt.Sprintf("%s.%s: missing in hostd (shell has %v)", path, k, wv))
			case !wok:
				*diffs = append(*diffs, fmt.Sprintf("%s.%s: only in hostd (%v)", path, k, gv))
			default:
				compare(path+"."+k, gv, wv, diffs)
			}
		}
	case []any:
		g, ok := got.([]any)
		if !ok || len(g) != len(w) {
			*diffs = append(*diffs, fmt.Sprintf("%s: want %v, got %v", path, want, got))
			return
		}
		for i := range w {
			compare(fmt.Sprintf("%s[%d]", path, i), g[i], w[i], diffs)
		}
	case float64:
		g, ok := got.(float64)
		if !ok || math.Abs(g-w) > 1e-6*math.Max(1, math.Abs(w)) {
			*diffs = append(*diffs, fmt.Sprintf("%s: want %v, got %v", path, want, got))
		}
	default:
		if got != want {
			*diffs = append(*diffs, fmt.Sprintf("%s: want %v, got %v", path, want, got))
		}
	}
}

func TestParityWithShellWriter(t *testing.T) {
	root := t.TempDir()
	copyTree(t, "testdata/parity", root, root)
	for _, d := range []string{"state/data/bitcoin", "xmr", "root"} {
		os.MkdirAll(filepath.Join(root, d), 0o755)
	}
	read := func(name string) []byte {
		b, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	columns := func(name string) map[string][]string {
		out := map[string][]string{}
		for _, line := range strings.Split(string(read(name)), "\n") {
			if f := strings.Fields(line); len(f) > 1 {
				out[f[len(f)-1]] = f
			}
		}
		return out
	}
	df := columns("df.txt")
	duKB := map[string]uint64{}
	for _, line := range strings.Split(string(read("du.txt")), "\n") {
		if f := strings.Fields(line); len(f) == 2 {
			duKB[f[0]], _ = strconv.ParseUint(f[1], 10, 64)
		}
	}
	var containers []Container
	if err := json.Unmarshal(read("containers.json"), &containers); err != nil {
		t.Fatal(err)
	}
	var info DockerInfo
	if err := json.Unmarshal(read("info.json"), &info); err != nil {
		t.Fatal(err)
	}
	socket := staticDocker(t, info, containers)
	cfg := Config{
		ProcRoot: filepath.Join(root, "proc"), SysRoot: filepath.Join(root, "sys"), CgroupRoot: filepath.Join(root, "cgroup"),
		ThermalGlob: filepath.Join(root, "zones/*/temp"), StackRoot: filepath.Join(root, "root"), StateRoot: filepath.Join(root, "state"),
		EnvFile: filepath.Join(root, "state/.env"), Project: "bitcoin-starter-stack-apt", ClockTicks: 100,
		Statfs: func(point string) (uint64, uint64, bool) {
			f, ok := df[point]
			if !ok {
				return 0, 0, false
			}
			used, _ := strconv.ParseUint(f[2], 10, 64)
			avail, _ := strconv.ParseUint(f[3], 10, 64)
			return used, avail, true
		},
	}
	s := &Sampler{Cfg: cfg, Docker: NewDocker(socket), Now: func() time.Time { return time.Unix(100000, 0) },
		Sizes: &SizeCache{MaxAge: time.Hour, Run: func(dir string) (uint64, bool) { kb, ok := duKB[dir]; return kb, ok }}}

	_, first, err := s.Sample(nil)
	if err != nil {
		t.Fatal(err)
	}
	copyTree(t, filepath.Join(root, "proc-next"), cfg.ProcRoot, root)
	copyTree(t, filepath.Join(root, "cgroup-next"), cfg.CgroupRoot, root)
	st, _, err := s.Sample(first)
	if err != nil {
		t.Fatal(err)
	}

	var want any
	if err := json.Unmarshal(read("expected.json"), &want); err != nil {
		t.Fatal(err)
	}
	var diffs []string
	compare("$", toAny(t, st), want, &diffs)
	if len(diffs) > 0 {
		t.Fatalf("hostd differs from the shell writer:\n%s", strings.Join(diffs, "\n"))
	}
}

func TestParityComparatorCatchesDrift(t *testing.T) {
	want := map[string]any{"a": 1.0, "fans_max_rpm": []any{0.0, 4825.0}, "nodes": map[string]any{"x": map[string]any{"cpu_pct": 5.0}}}
	for name, got := range map[string]any{
		"missing field": map[string]any{"a": 1.0, "nodes": want["nodes"]},
		"unit mismatch": map[string]any{"a": 1000.0, "fans_max_rpm": want["fans_max_rpm"], "nodes": want["nodes"]},
		"extra field":   map[string]any{"a": 1.0, "b": 2.0, "fans_max_rpm": want["fans_max_rpm"], "nodes": want["nodes"]},
		"nested":        map[string]any{"a": 1.0, "fans_max_rpm": want["fans_max_rpm"], "nodes": map[string]any{"x": map[string]any{"cpu_pct": 0.05}}},
	} {
		var diffs []string
		compare("$", got, want, &diffs)
		if len(diffs) == 0 {
			t.Errorf("%s was not detected", name)
		}
	}
	var diffs []string
	compare("$", map[string]any{"a": 1.0000000001}, map[string]any{"a": 1.0}, &diffs)
	if len(diffs) != 0 {
		t.Errorf("float tolerance rejected rounding noise: %v", diffs)
	}
}

func TestContractFieldNames(t *testing.T) {
	st := &Stats{Nodes: map[string]Usage{}, Failed: map[string]string{}, Running: map[string]bool{}, Disks: []Disk{}}
	one, two, three := 1.0, 2.0, int64(3)
	st.CPUTempC, st.BoardTempC, st.UptimeS, st.NetRxBps, st.NetTxBps = &one, &two, &three, &one, &two
	st.FansRPM, st.FansMaxRPM = []int{1}, []int{2}
	got := toAny(t, st).(map[string]any)
	for _, k := range []string{"updated", "cpus", "mem_total_bytes", "host_cpu_pct", "host_mem_bytes", "nodes", "other", "failed", "running",
		"disks", "cpu_temp_c", "board_temp_c", "fans_rpm", "fans_max_rpm", "uptime_s", "net_rx_bps", "net_tx_bps"} {
		if _, ok := got[k]; !ok {
			t.Errorf("host stats lost %s", k)
		}
	}
	if len(got) != 17 {
		t.Errorf("unexpected extra fields: %v", got)
	}
}
