package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func priceResponse(updatedAt int64) string {
	timestamp := json.Number(strconv.FormatInt(updatedAt, 10)).String()
	return `{"bitcoin":{"usd":70000,"eur":65000,"gbp":55000,"inr":5800000,"cad":95000,"aud":105000,"jpy":11000000,"rub":6500000,"cny":500000,"last_updated_at":` + timestamp +
		`},"monero":{"usd":350,"eur":325,"gbp":275,"inr":29000,"cad":475,"aud":525,"jpy":55000,"rub":32500,"cny":2500,"last_updated_at":` + timestamp + `}}`
}

func priceCacheForTest(now time.Time, handler roundTripFunc) *PriceCache {
	cache := NewPriceCache()
	cache.client = &http.Client{Transport: handler}
	cache.now = func() time.Time { return now }
	cache.url = "https://provider.test/simple/price?fixed=true"
	return cache
}

func allAssetPriceResponse(t *testing.T, updatedAt int64) string {
	t.Helper()
	payload := make(map[string]map[string]any, len(priceAssets))
	for assetIndex, asset := range priceAssets {
		providerAsset := asset
		if mapped := priceProviderAssets[asset]; mapped != "" {
			providerAsset = mapped
		}
		quote := map[string]any{"last_updated_at": updatedAt}
		for currencyIndex, currency := range priceCurrencies {
			quote[currency] = float64((assetIndex + 1) * (currencyIndex + 1))
		}
		payload[providerAsset] = quote
	}
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestNewPriceCacheUsesFixedCoinGeckoURLAndTorClient(t *testing.T) {
	cache := NewPriceCache()
	if cache.url != "https://api.coingecko.com/api/v3/simple/price?ids=bitcoin,monero,minotari,litecoin,dogecoin,bitcoin-cash,zcash&vs_currencies=usd,eur,gbp,inr,cad,aud,jpy,rub,cny&include_last_updated_at=true" {
		t.Fatalf("provider URL = %q", cache.url)
	}
	if cache.client == nil || cache.client.Transport == nil || cache.client.Timeout != 30*time.Second {
		t.Fatalf("provider client = %#v", cache.client)
	}
	if cache.refreshEvery != 5*time.Minute {
		t.Fatalf("refresh interval = %s", cache.refreshEvery)
	}
	if cache.startupRetry != 10*time.Second {
		t.Fatalf("startup retry = %s", cache.startupRetry)
	}
}

func TestPriceCacheRefreshAndSnapshot(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	var calls atomic.Int32
	const providerURL = "https://provider.test/simple/price?fixed=true"
	cache := priceCacheForTest(now, func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		if r.Method != http.MethodGet || r.URL.String() != providerURL {
			t.Fatalf("request = %s %s", r.Method, r.URL)
		}
		userAgent := r.UserAgent()
		if userAgent == "" || strings.HasPrefix(userAgent, "Go-http-client/") || !strings.Contains(userAgent, "node-manager-dashboard") || !strings.Contains(userAgent, "github.com/VijitSingh97/node-manager") {
			return response(http.StatusTooManyRequests, ""), nil
		}
		return response(http.StatusOK, priceResponse(now.Unix())), nil
	})
	cache.url = providerURL
	if err := cache.refresh(context.Background()); err != nil {
		t.Fatal(err)
	}

	snapshot := cache.Snapshot()
	if snapshot["updated_at"] != now.Unix() || snapshot["stale"] != false || snapshot["available"] != true {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	prices, ok := snapshot["prices"].(map[string]float64)
	if !ok || len(prices) != 9 || prices["usd"] != 70000 || prices["jpy"] != 11000000 || prices["rub"] != 6500000 || prices["cny"] != 500000 {
		t.Fatalf("prices = %#v", snapshot["prices"])
	}
	prices["usd"] = 1
	if got := cache.Snapshot()["prices"].(map[string]float64)["usd"]; got != 70000 {
		t.Fatalf("snapshot mutated cache: usd = %v", got)
	}
	monero := cache.SnapshotAsset("monero")
	if monero["available"] != true || monero["updated_at"] != now.Unix() || monero["prices"].(map[string]float64)["usd"] != 350 {
		t.Fatalf("monero snapshot = %#v", monero)
	}
	if unknown := cache.SnapshotAsset("dogecoin"); unknown["available"] != false || unknown["stale"] != true {
		t.Fatalf("unknown asset snapshot = %#v", unknown)
	}
	for range 100 {
		_ = cache.Snapshot()
	}
	if calls.Load() != 1 {
		t.Fatalf("Snapshot made provider requests: calls = %d", calls.Load())
	}
}

func TestPriceCachePublishesEveryConfiguredAsset(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	cache := priceCacheForTest(now, func(*http.Request) (*http.Response, error) {
		return response(http.StatusOK, allAssetPriceResponse(t, now.Unix())), nil
	})
	if err := cache.refresh(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, asset := range priceAssets {
		snapshot := cache.SnapshotAsset(asset)
		if snapshot["available"] != true || snapshot["stale"] != false || len(snapshot["prices"].(map[string]float64)) != len(priceCurrencies) {
			t.Fatalf("%s snapshot = %#v", asset, snapshot)
		}
	}
}

func TestPriceCacheMapsMinotariProviderAssetToTari(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	payload := map[string]map[string]any{
		"bitcoin":  {"last_updated_at": now.Unix()},
		"minotari": {"last_updated_at": now.Unix()},
	}
	for _, currency := range priceCurrencies {
		payload["bitcoin"][currency] = 70000
		payload["minotari"][currency] = 0.02
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	cache := priceCacheForTest(now, func(*http.Request) (*http.Response, error) {
		return response(http.StatusOK, string(body)), nil
	})
	if err := cache.refresh(t.Context()); err != nil {
		t.Fatal(err)
	}
	tari := cache.SnapshotAsset("tari")
	if tari["available"] != true || tari["stale"] != false || tari["prices"].(map[string]float64)["usd"] != 0.02 {
		t.Fatalf("Tari snapshot = %#v", tari)
	}
}

func TestPriceCacheKeepsBitcoinCompatibleWhenMoneroQuoteIsMissing(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	body := strings.Split(priceResponse(now.Unix()), `,"monero"`)[0] + `}`
	cache := priceCacheForTest(now, func(*http.Request) (*http.Response, error) {
		return response(http.StatusOK, body), nil
	})
	if err := cache.refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if snapshot := cache.Snapshot(); snapshot["available"] != true {
		t.Fatalf("bitcoin snapshot = %#v", snapshot)
	}
	if snapshot := cache.SnapshotAsset("monero"); snapshot["available"] != false {
		t.Fatalf("monero snapshot = %#v", snapshot)
	}
}

func TestPartialRefreshMarksRetainedMoneroQuoteStaleButBitcoinFresh(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	currentBody := priceResponse(now.Unix())
	cache := priceCacheForTest(now, func(*http.Request) (*http.Response, error) {
		return response(http.StatusOK, currentBody), nil
	})
	if err := cache.refresh(context.Background()); err != nil {
		t.Fatal(err)
	}

	now = now.Add(time.Minute)
	cache.now = func() time.Time { return now }
	currentBody = strings.Split(priceResponse(now.Unix()), `,"monero"`)[0] + `}`
	if err := cache.refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	bitcoin := cache.Snapshot()
	if bitcoin["available"] != true || bitcoin["stale"] != false || bitcoin["updated_at"] != now.Unix() {
		t.Fatalf("bitcoin snapshot = %#v", bitcoin)
	}
	monero := cache.SnapshotAsset("monero")
	if monero["available"] != true || monero["stale"] != true || monero["updated_at"] != now.Add(-time.Minute).Unix() || monero["prices"].(map[string]float64)["usd"] != 350 {
		t.Fatalf("retained Monero snapshot = %#v", monero)
	}
}

func TestInvalidMoneroRefreshMarksRetainedQuoteStale(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	body := priceResponse(now.Unix())
	cache := priceCacheForTest(now, func(*http.Request) (*http.Response, error) {
		return response(http.StatusOK, body), nil
	})
	if err := cache.refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	cache.now = func() time.Time { return now }
	body = strings.Replace(priceResponse(now.Unix()), `"usd":350`, `"usd":0`, 1)
	if err := cache.refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if bitcoin, monero := cache.Snapshot(), cache.SnapshotAsset("monero"); bitcoin["stale"] != false || monero["available"] != true || monero["stale"] != true || monero["prices"].(map[string]float64)["usd"] != 350 {
		t.Fatalf("snapshots = bitcoin %#v, monero %#v", bitcoin, monero)
	}
}

func TestPriceCacheRunRefreshesImmediatelyAndStops(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	started := make(chan struct{})
	requestCanceled := make(chan struct{})
	cache := priceCacheForTest(now, func(r *http.Request) (*http.Response, error) {
		close(started)
		<-r.Context().Done()
		close(requestCanceled)
		return nil, r.Context().Err()
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		cache.Run(ctx)
		close(done)
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("startup refresh did not begin")
	}
	cancel()
	select {
	case <-requestCanceled:
	case <-time.After(time.Second):
		t.Fatal("provider request was not canceled")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not stop")
	}
	snapshot := cache.Snapshot()
	if snapshot["available"] != false || snapshot["stale"] != true || snapshot["updated_at"] != int64(0) {
		t.Fatalf("first failure snapshot = %#v", snapshot)
	}
	if len(snapshot["prices"].(map[string]float64)) != 0 {
		t.Fatalf("first failure prices = %#v", snapshot["prices"])
	}
}

func TestPriceCacheRunRefreshesOnInterval(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	calls := make(chan struct{}, 3)
	cache := priceCacheForTest(now, func(*http.Request) (*http.Response, error) {
		calls <- struct{}{}
		return response(http.StatusOK, priceResponse(now.Unix())), nil
	})
	cache.refreshEvery = time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		cache.Run(ctx)
		close(done)
	}()
	for range 2 {
		select {
		case <-calls:
		case <-time.After(time.Second):
			t.Fatal("scheduled refresh did not run")
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not stop")
	}
}

func TestPriceCacheRunRetriesInitialFailure(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	var calls atomic.Int32
	cache := priceCacheForTest(now, func(*http.Request) (*http.Response, error) {
		if calls.Add(1) <= 3 {
			return nil, errors.New("proxy starting")
		}
		return response(http.StatusOK, priceResponse(now.Unix())), nil
	})
	cache.startupRetry = time.Millisecond
	cache.refreshEvery = time.Hour
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		cache.Run(ctx)
		close(done)
	}()
	deadline := time.After(time.Second)
	for calls.Load() < 4 {
		select {
		case <-deadline:
			t.Fatal("initial price failure was not retried")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not stop")
	}
	if snapshot := cache.Snapshot(); snapshot["available"] != true || snapshot["stale"] != false {
		t.Fatalf("retry snapshot = %#v", snapshot)
	}
}

func TestPriceCacheRunStopsWhileWaitingToRetry(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	failed := make(chan struct{}, 1)
	cache := priceCacheForTest(now, func(*http.Request) (*http.Response, error) {
		failed <- struct{}{}
		return nil, errors.New("proxy starting")
	})
	cache.startupRetry = time.Hour
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		cache.Run(ctx)
		close(done)
	}()
	select {
	case <-failed:
	case <-time.After(time.Second):
		t.Fatal("startup refresh did not fail")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not stop while waiting to retry")
	}
}

func TestPriceCacheRetainsLastSuccessAndMarksFailuresOrAgeStale(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	var fail atomic.Bool
	cache := priceCacheForTest(now, func(*http.Request) (*http.Response, error) {
		if fail.Load() {
			return nil, errors.New("provider unavailable")
		}
		return response(http.StatusOK, priceResponse(now.Unix())), nil
	})
	if err := cache.refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	fail.Store(true)
	if err := cache.refresh(context.Background()); err == nil {
		t.Fatal("refresh succeeded during provider failure")
	}
	if snapshot := cache.Snapshot(); snapshot["stale"] != true || snapshot["available"] != true || snapshot["updated_at"] != now.Unix() {
		t.Fatalf("failure snapshot = %#v", snapshot)
	}

	fail.Store(false)
	cache.now = func() time.Time { return now.Add(15 * time.Minute) }
	if snapshot := cache.Snapshot(); snapshot["stale"] != true {
		t.Fatalf("failed snapshot at age boundary = %#v", snapshot)
	}
	if err := cache.refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if snapshot := cache.Snapshot(); snapshot["stale"] != false {
		t.Fatalf("healthy snapshot at age boundary = %#v", snapshot)
	}
	cache.now = func() time.Time { return now.Add(15*time.Minute + time.Second) }
	if snapshot := cache.Snapshot(); snapshot["stale"] != true {
		t.Fatalf("aged snapshot = %#v", snapshot)
	}
}

func TestPriceCacheConcurrentSnapshots(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	cache := priceCacheForTest(now, func(*http.Request) (*http.Response, error) {
		return response(http.StatusOK, priceResponse(now.Unix())), nil
	})
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				_ = cache.Snapshot()
				_ = cache.refresh(context.Background())
			}
		}()
	}
	wg.Wait()
}

func TestPriceCacheRejectsHTTPAndMalformedResponses(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	tests := []struct {
		name string
		code int
		body string
	}{
		{name: "rate limited", code: http.StatusTooManyRequests, body: priceResponse(now.Unix())},
		{name: "server error", code: http.StatusBadGateway, body: priceResponse(now.Unix())},
		{name: "invalid json", code: http.StatusOK, body: `{"bitcoin":`},
		{name: "missing currency", code: http.StatusOK, body: `{"bitcoin":{"usd":1,"last_updated_at":1800000000}}`},
		{name: "zero price", code: http.StatusOK, body: strings.Replace(priceResponse(now.Unix()), `"usd":70000`, `"usd":0`, 1)},
		{name: "negative price", code: http.StatusOK, body: strings.Replace(priceResponse(now.Unix()), `"eur":65000`, `"eur":-1`, 1)},
		{name: "nonfinite price", code: http.StatusOK, body: strings.Replace(priceResponse(now.Unix()), `"gbp":55000`, `"gbp":1e999`, 1)},
		{name: "zero timestamp", code: http.StatusOK, body: priceResponse(0)},
		{name: "fractional timestamp", code: http.StatusOK, body: strings.Replace(priceResponse(now.Unix()), `"last_updated_at":1800000000`, `"last_updated_at":1800000000.5`, 1)},
		{name: "trailing json", code: http.StatusOK, body: priceResponse(now.Unix()) + `{}`},
		{name: "oversized", code: http.StatusOK, body: priceResponse(now.Unix()) + strings.Repeat(" ", maxPriceResponseBytes)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cache := priceCacheForTest(now, func(*http.Request) (*http.Response, error) {
				return response(tt.code, tt.body), nil
			})
			if err := cache.refresh(context.Background()); err == nil {
				t.Fatal("refresh unexpectedly succeeded")
			}
			if snapshot := cache.Snapshot(); snapshot["available"] != false || snapshot["stale"] != true {
				t.Fatalf("rejected snapshot = %#v", snapshot)
			}
		})
	}
}

func TestPriceCacheClosesResponseBody(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	closed := atomic.Bool{}
	body := &closeTrackingBody{Reader: strings.NewReader(priceResponse(now.Unix())), closed: &closed}
	cache := priceCacheForTest(now, func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: body, Header: make(http.Header)}, nil
	})
	if err := cache.refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !closed.Load() {
		t.Fatal("response body was not closed")
	}
}

type closeTrackingBody struct {
	io.Reader
	closed *atomic.Bool
}

func (b *closeTrackingBody) Close() error {
	b.closed.Store(true)
	return nil
}
