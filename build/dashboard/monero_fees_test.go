package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestMoneroFeeHistoryPersistsPrivatelyAndStaysBounded(t *testing.T) {
	dir := t.TempDir()
	history := NewMoneroFeeHistory(dir)
	now := time.Date(2026, time.October, 30, 12, 0, 0, 0, time.UTC)
	history.now = func() time.Time { return now }
	for i := 1; i <= moneroFeeHistoryPoints+2; i++ {
		history.recordAt(float64(i)/1e9, now.Add(time.Duration(i-moneroFeeHistoryPoints-2)*time.Minute))
	}
	values := history.snapshot()
	if len(values) != moneroFeeHistoryPoints || values[0] != 3e-9 || values[len(values)-1] != float64(moneroFeeHistoryPoints+2)/1e9 {
		t.Fatalf("bounded values = len %d, first %g, last %g", len(values), values[0], values[len(values)-1])
	}
	path := filepath.Join(dir, "monero_fee_history.json")
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("history file = %#v, %v", info, err)
	}
	reloaded := &MoneroFeeHistory{path: path, now: func() time.Time { return now }}
	reloaded.load()
	if !reflect.DeepEqual(reloaded.snapshot(), values) {
		t.Fatal("reloaded fee history differs")
	}
}

func TestMoneroFeeHistoryKeepsOneSamplePerMinuteAndDropsOldPoints(t *testing.T) {
	history := NewMoneroFeeHistory(t.TempDir())
	now := time.Now().Truncate(time.Minute)
	history.recordAt(0.1, now.Add(-25*time.Hour))
	history.recordAt(0.2, now)
	history.recordAt(0.3, now.Add(20*time.Second))
	if got := history.snapshot(); !reflect.DeepEqual(got, []float64{0.3}) {
		t.Fatalf("rolling history = %#v", got)
	}
}

func TestMoneroFeeHistoryRejectsInvalidValuesAndFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "monero_fee_history.json")
	if err := os.WriteFile(path, []byte(`[0,0.1]`), 0o600); err != nil {
		t.Fatal(err)
	}
	history := NewMoneroFeeHistory(dir)
	history.record(0)
	if len(history.snapshot()) != 0 {
		t.Fatalf("invalid values loaded or recorded: %#v", history.snapshot())
	}
}

func TestRunMoneroFeesSamplesNormalPriorityAndStops(t *testing.T) {
	called := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{
			"status": "OK", "fees": []uint64{20_000, 80_000, 320_000, 4_000_000}, "synchronized": true,
			"block_header": map[string]any{"timestamp": time.Now().Unix()},
		}})
		select {
		case called <- struct{}{}:
		default:
		}
	}))
	defer server.Close()
	a := &app{cfg: config{moneroEnabled: true, moneroRPCURL: server.URL, stateDir: t.TempDir()}, moneroFees: NewMoneroFeeHistory(t.TempDir())}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		a.runMoneroFees(ctx)
		close(done)
	}()
	select {
	case <-called:
	case <-time.After(time.Second):
		t.Fatal("initial fee sample did not run")
	}
	deadline := time.Now().Add(time.Second)
	for len(a.moneroFees.snapshot()) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("fee sampler did not stop")
	}
	if got := a.moneroFees.snapshot(); !reflect.DeepEqual(got, []float64{0.00008}) {
		t.Fatalf("fee history = %#v", got)
	}
}

func TestMoneroFeeWorkerPausesDuringNodeTransition(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		_ = json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{"status": "OK", "fees": []uint64{20_000, 80_000, 320_000, 4_000_000}}})
	}))
	defer server.Close()
	a := &app{cfg: config{moneroEnabled: true, moneroRPCURL: server.URL, stateDir: t.TempDir()}, moneroFees: NewMoneroFeeHistory(t.TempDir())}
	writePendingNodeTransition(t, a, nodeMonero)
	a.sampleMoneroFee(t.Context())
	if calls != 0 || len(a.moneroFees.snapshot()) != 0 {
		t.Fatalf("pending Monero transition sampled fees: calls=%d history=%v", calls, a.moneroFees.snapshot())
	}
}

func TestMoneroFeeFailureDoesNotRecordZero(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{"status": "BUSY", "fees": []uint64{0, 0, 0, 0}, "synchronized": true}})
	}))
	defer server.Close()
	a := &app{cfg: config{moneroRPCURL: server.URL}, moneroFees: NewMoneroFeeHistory(t.TempDir())}
	a.sampleMoneroFee(context.Background())
	if got := a.moneroFees.snapshot(); len(got) != 0 {
		t.Fatalf("failed estimate recorded: %#v", got)
	}
}

func TestMoneroFeesAPIUsesNativeUnits(t *testing.T) {
	history := NewMoneroFeeHistory(t.TempDir())
	history.record(0.00008)
	a := &app{moneroFees: history}
	response := httptest.NewRecorder()
	a.apiMoneroFees(response, httptest.NewRequest(http.MethodGet, "/api/monero/fees", nil))
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" || response.Body.String() != "{\"fee_xmr_per_kb\":[0.00008]}\n" {
		t.Fatalf("response = %d %q, cache = %q", response.Code, response.Body.String(), response.Header().Get("Cache-Control"))
	}
}

func TestMoneroFeeSamplingSkipsWhileNotSynchronized(t *testing.T) {
	for name, info := range map[string]map[string]any{
		"not synchronized": {"synchronized": false},
		"busy syncing":     {"synchronized": true, "busy_syncing": true},
		"stale tip":        {"synchronized": true, "block_header": map[string]any{"timestamp": time.Now().Add(-3 * time.Hour).Unix()}},
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				result := map[string]any{"status": "OK", "fees": []uint64{20_000, 80_000, 320_000, 4_000_000}}
				for key, value := range info {
					result[key] = value
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"result": result})
			}))
			defer server.Close()
			a := &app{cfg: config{moneroRPCURL: server.URL}, moneroFees: NewMoneroFeeHistory(t.TempDir())}
			a.sampleMoneroFee(context.Background())
			if got := a.moneroFees.snapshot(); len(got) != 0 {
				t.Fatalf("sampled while behind: %#v", got)
			}
		})
	}
}
