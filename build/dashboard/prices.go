package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"sync"
	"time"
)

const (
	coinGeckoPriceURL     = "https://api.coingecko.com/api/v3/simple/price?ids=bitcoin,monero,minotari,litecoin,dogecoin,bitcoin-cash,zcash&vs_currencies=usd,eur,gbp,inr,cad,aud,jpy,rub,cny&include_last_updated_at=true"
	priceUserAgent        = "node-manager-dashboard (+https://github.com/VijitSingh97/node-manager)"
	priceStartupRetry     = 10 * time.Second
	priceRefreshEvery     = 5 * time.Minute
	priceStaleAfter       = 15 * time.Minute
	maxPriceResponseBytes = 64 << 10
)

var priceCurrencies = []string{"usd", "eur", "gbp", "inr", "cad", "aud", "jpy", "rub", "cny"}
var priceAssets = []string{"bitcoin", "monero", "tari", "litecoin", "dogecoin", "bitcoin-cash", "zcash"}
var priceProviderAssets = map[string]string{"tari": "minotari"}

type PriceCache struct {
	client       *http.Client
	url          string
	now          func() time.Time
	refreshEvery time.Duration
	startupRetry time.Duration

	mu             sync.RWMutex
	assetPrices    map[string]map[string]float64
	assetUpdatedAt map[string]int64
	assetFailed    map[string]bool
}

func NewPriceCache() *PriceCache {
	return &PriceCache{
		client:         newTorHTTPClient(torProxy),
		url:            coinGeckoPriceURL,
		now:            time.Now,
		refreshEvery:   priceRefreshEvery,
		startupRetry:   priceStartupRetry,
		assetPrices:    make(map[string]map[string]float64),
		assetUpdatedAt: make(map[string]int64),
		assetFailed:    map[string]bool{"bitcoin": true, "monero": true, "tari": true, "litecoin": true, "dogecoin": true, "bitcoin-cash": true, "zcash": true},
	}
}

func (c *PriceCache) Run(ctx context.Context) {
	delay := time.Duration(0)
	retryDelay := c.startupRetry
	ready := false
	for {
		if delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
		err := c.refresh(ctx)
		if ctx.Err() != nil {
			return
		}
		if err == nil {
			ready = true
			delay = c.refreshEvery
			continue
		}
		if ready {
			delay = c.refreshEvery
			continue
		}
		delay = retryDelay
		retryDelay *= 2
		if retryDelay > c.refreshEvery {
			retryDelay = c.refreshEvery
		}
	}
}

func (c *PriceCache) Snapshot(asset ...string) map[string]any {
	selected := "bitcoin"
	if len(asset) > 0 {
		selected = asset[0]
	}
	return c.SnapshotAsset(selected)
}

func (c *PriceCache) SnapshotAsset(asset string) map[string]any {
	if asset == "" {
		asset = "bitcoin"
	}
	c.mu.RLock()
	assetPrices, updatedAt := c.assetPrices[asset], c.assetUpdatedAt[asset]
	prices := make(map[string]float64, len(assetPrices))
	for currency, price := range assetPrices {
		prices[currency] = price
	}
	failed, known := c.assetFailed[asset]
	if !known {
		failed = true
	}
	c.mu.RUnlock()

	available := len(prices) != 0
	stale := failed || !available || c.now().Sub(time.Unix(updatedAt, 0)) > priceStaleAfter
	return map[string]any{
		"prices":     prices,
		"updated_at": updatedAt,
		"stale":      stale,
		"available":  available,
	}
}

func (c *PriceCache) refresh(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url, nil)
	if err != nil {
		return c.fail(errors.New("price provider request failed"))
	}
	req.Header.Set("User-Agent", priceUserAgent)
	resp, err := c.client.Do(req)
	if err != nil {
		return c.fail(errors.New("price provider request failed"))
	}
	if resp.Body == nil {
		return c.fail(errors.New("price provider response failed"))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return c.fail(errors.New("price provider response failed"))
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxPriceResponseBytes+1))
	if err != nil || len(body) > maxPriceResponseBytes {
		return c.fail(errors.New("price provider response failed"))
	}

	var payload map[string]map[string]json.Number
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return c.fail(errors.New("price provider response invalid"))
	}
	if err := ensureJSONEnd(decoder); err != nil {
		return c.fail(errors.New("price provider response invalid"))
	}

	now := c.now()
	prices := make(map[string]map[string]float64, len(priceAssets))
	updatedAt := make(map[string]int64, len(priceAssets))
	for _, asset := range priceAssets {
		providerAsset := asset
		if mapped := priceProviderAssets[asset]; mapped != "" {
			providerAsset = mapped
		}
		assetUpdatedAt, err := payload[providerAsset]["last_updated_at"].Int64()
		if err != nil || assetUpdatedAt <= 0 || time.Unix(assetUpdatedAt, 0).After(now.Add(priceRefreshEvery)) {
			if asset == "bitcoin" {
				return c.fail(errors.New("price provider response invalid"))
			}
			continue
		}
		assetPrices := make(map[string]float64, len(priceCurrencies))
		valid := true
		for _, currency := range priceCurrencies {
			price, err := payload[providerAsset][currency].Float64()
			if err != nil || math.IsNaN(price) || math.IsInf(price, 0) || price <= 0 {
				valid = false
				break
			}
			assetPrices[currency] = price
		}
		if !valid {
			if asset == "bitcoin" {
				return c.fail(errors.New("price provider response invalid"))
			}
			continue
		}
		prices[asset] = assetPrices
		updatedAt[asset] = assetUpdatedAt
	}

	c.mu.Lock()
	for _, asset := range priceAssets {
		if prices[asset] != nil {
			c.assetPrices[asset], c.assetUpdatedAt[asset], c.assetFailed[asset] = prices[asset], updatedAt[asset], false
		} else {
			c.assetFailed[asset] = true
		}
	}
	c.mu.Unlock()
	return nil
}

func (c *PriceCache) fail(err error) error {
	c.mu.Lock()
	for _, asset := range priceAssets {
		c.assetFailed[asset] = true
	}
	c.mu.Unlock()
	return err
}

func ensureJSONEnd(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("trailing JSON")
		}
		return err
	}
	return nil
}
