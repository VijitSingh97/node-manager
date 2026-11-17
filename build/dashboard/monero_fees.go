package main

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const moneroFeeHistoryPoints = 24 * 60

type MoneroFeeHistory struct {
	mu     sync.Mutex
	path   string
	points []moneroFeePoint
	now    func() time.Time
}

type moneroFeePoint struct {
	Timestamp int64   `json:"timestamp"`
	XMRPerKB  float64 `json:"xmr_per_kb"`
}

func NewMoneroFeeHistory(stateDir string) *MoneroFeeHistory {
	if stateDir == "" {
		stateDir = "/state"
	}
	history := &MoneroFeeHistory{path: filepath.Join(stateDir, "monero_fee_history.json"), now: time.Now}
	history.load()
	return history
}

func (h *MoneroFeeHistory) load() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := chmodExistingFile(h.path, 0o600); err != nil && !os.IsNotExist(err) {
		return
	}
	data, err := os.ReadFile(h.path)
	if err != nil {
		return
	}
	var points []moneroFeePoint
	if json.Unmarshal(data, &points) != nil {
		return
	}
	var previous int64
	for _, point := range points {
		if point.Timestamp <= previous || math.IsNaN(point.XMRPerKB) || math.IsInf(point.XMRPerKB, 0) || point.XMRPerKB <= 0 {
			return
		}
		previous = point.Timestamp
	}
	h.points = points
	h.pruneLocked(h.now())
}

func (h *MoneroFeeHistory) pruneLocked(now time.Time) {
	cutoff := now.Add(-24 * time.Hour).Unix()
	first := 0
	for first < len(h.points) && h.points[first].Timestamp <= cutoff {
		first++
	}
	if first > 0 {
		h.points = append([]moneroFeePoint(nil), h.points[first:]...)
	}
	if len(h.points) > moneroFeeHistoryPoints {
		h.points = append([]moneroFeePoint(nil), h.points[len(h.points)-moneroFeeHistoryPoints:]...)
	}
}

func (h *MoneroFeeHistory) record(value float64) {
	h.recordAt(value, h.now())
}

func (h *MoneroFeeHistory) recordAt(value float64, now time.Time) {
	if math.IsNaN(value) || math.IsInf(value, 0) || value <= 0 {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	point := moneroFeePoint{Timestamp: now.Unix(), XMRPerKB: math.RoundToEven(value*1e12) / 1e12}
	if len(h.points) > 0 && h.points[len(h.points)-1].Timestamp/60 == point.Timestamp/60 {
		h.points[len(h.points)-1] = point
	} else {
		h.points = append(h.points, point)
	}
	h.pruneLocked(now)
	data, err := json.Marshal(h.points)
	if err == nil {
		_ = writePrivateFileAtomic(h.path, data)
	}
}

func (h *MoneroFeeHistory) snapshot() []float64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.pruneLocked(h.now())
	values := make([]float64, len(h.points))
	for index, point := range h.points {
		values[index] = point.XMRPerKB
	}
	return values
}

func (a *app) fetchMoneroFeeEstimate(ctx context.Context) ([]uint64, error) {
	rpc := moneroRPC{
		url:      a.cfg.moneroRPCURL,
		user:     a.cfg.moneroRPCUser,
		password: a.cfg.moneroRPCPassword,
		client:   newInternalHTTPClient(5 * time.Second),
	}
	var estimate moneroFeeEstimate
	if err := rpc.call(ctx, "get_fee_estimate", &estimate); err != nil || !strings.EqualFold(estimate.Status, "OK") || len(estimate.Fees) < 4 {
		return nil, errors.New("Monero fee estimate unavailable")
	}
	fees := append([]uint64(nil), estimate.Fees[:4]...)
	for _, fee := range fees {
		if fee == 0 {
			return nil, errors.New("Monero fee estimate unavailable")
		}
	}
	return fees, nil
}

func (a *app) sampleMoneroFee(ctx context.Context) {
	if !a.nodeWorkerRunnable(nodeMonero, true) {
		return
	}
	fees, err := a.fetchMoneroFeeEstimate(ctx)
	if err != nil || a.moneroFees == nil {
		return
	}
	a.moneroFees.record(float64(fees[1]) / 1e9)
}

func (a *app) runMoneroFees(ctx context.Context) {
	a.sampleMoneroFee(ctx)
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sampleCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			a.sampleMoneroFee(sampleCtx)
			cancel()
		}
	}
}

func (a *app) apiMoneroFees(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	values := []float64{}
	if a.moneroFees != nil {
		values = a.moneroFees.snapshot()
	}
	writeJSON(w, map[string]any{"fee_xmr_per_kb": values})
}
