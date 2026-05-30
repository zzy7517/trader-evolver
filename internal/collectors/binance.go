// Package collectors fetches multi-year historical market data into the store.
//
// This file: Binance USDT-M futures klines (crypto: BTC/ETH/SOL/...).
// Design principle #5 routes crypto history to Binance. Candles are written
// via store.UpsertCandles([]types.Candle), each tagged with its instrument_key
// and interval so the canonical instrument-key format stays stable end to end.
package collectors

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"trader-evolver/internal/store"
	"trader-evolver/internal/types"
)

// DefaultBinanceBaseURL is the USDT-M futures REST endpoint.
const DefaultBinanceBaseURL = "https://fapi.binance.com"

// binanceMaxLimit is the max klines per request for /fapi/v1/klines.
const binanceMaxLimit = 1500

// BinanceCollector pages /fapi/v1/klines to backfill history.
type BinanceCollector struct {
	BaseURL    string
	HTTPClient *http.Client
	// PageDelay throttles between paged requests (rate-limit friendliness).
	PageDelay time.Duration
	// MaxRetries per request on transient (429/5xx/network) failure.
	MaxRetries int
}

// NewBinanceCollector returns a collector with sane defaults.
func NewBinanceCollector() *BinanceCollector {
	return &BinanceCollector{
		BaseURL:    DefaultBinanceBaseURL,
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
		PageDelay:  250 * time.Millisecond,
		MaxRetries: 4,
	}
}

// FetchKlines pages klines in [startMs, endMs] (inclusive) and returns candles
// sorted ascending, each tagged with instrumentKey + interval. Stops when a
// page returns no further data or a partial page (end of history).
func (c *BinanceCollector) FetchKlines(ctx context.Context, instrumentKey, symbol, interval string, startMs, endMs int64) ([]types.Candle, error) {
	var out []types.Candle
	cursor := startMs
	for {
		page, err := c.fetchPage(ctx, instrumentKey, symbol, interval, cursor, endMs)
		if err != nil {
			return out, err
		}
		if len(page) == 0 {
			break
		}
		out = append(out, page...)
		last := page[len(page)-1].OpenTimeMs
		// Advance past the last open time to avoid re-fetching it.
		next := last + 1
		if next <= cursor {
			break // no forward progress; guard against infinite loop
		}
		cursor = next
		if endMs > 0 && cursor > endMs {
			break
		}
		if len(page) < binanceMaxLimit {
			break // last partial page reached
		}
		if c.PageDelay > 0 {
			select {
			case <-ctx.Done():
				return out, ctx.Err()
			case <-time.After(c.PageDelay):
			}
		}
	}
	return out, nil
}

// fetchPage retrieves one klines page with retry/backoff.
func (c *BinanceCollector) fetchPage(ctx context.Context, instrumentKey, symbol, interval string, startMs, endMs int64) ([]types.Candle, error) {
	q := url.Values{}
	q.Set("symbol", symbol)
	q.Set("interval", interval) // Binance uses the same tokens (1m,5m,1h,4h,1d,...)
	q.Set("startTime", strconv.FormatInt(startMs, 10))
	if endMs > 0 {
		q.Set("endTime", strconv.FormatInt(endMs, 10))
	}
	q.Set("limit", strconv.Itoa(binanceMaxLimit))
	endpoint := c.BaseURL + "/fapi/v1/klines?" + q.Encode()

	var lastErr error
	for attempt := 0; attempt <= c.MaxRetries; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(1<<uint(attempt-1)) * 500 * time.Millisecond
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff):
			}
		}
		candles, err := c.doRequest(ctx, endpoint, instrumentKey, interval)
		if err == nil {
			return candles, nil
		}
		lastErr = err
	}
	return nil, fmt.Errorf("binance klines after %d retries: %w", c.MaxRetries, lastErr)
}

func (c *BinanceCollector) doRequest(ctx context.Context, endpoint, instrumentKey, interval string) ([]types.Candle, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("binance status %d: %s", resp.StatusCode, string(body))
	}
	return parseBinanceKlines(body, instrumentKey, interval)
}

// parseBinanceKlines decodes the array-of-arrays klines payload, tagging each
// candle with instrumentKey + interval.
// Each kline: [openTime, open, high, low, close, volume, closeTime, ...].
func parseBinanceKlines(body []byte, instrumentKey, interval string) ([]types.Candle, error) {
	var raw [][]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("binance: decode klines: %w", err)
	}
	out := make([]types.Candle, 0, len(raw))
	for _, k := range raw {
		if len(k) < 6 {
			continue
		}
		openTime, ok := jsonNumberToInt64(k[0])
		if !ok {
			continue
		}
		out = append(out, types.Candle{
			InstrumentKey: instrumentKey,
			Interval:      interval,
			OpenTimeMs:    openTime,
			Open:          jsonStrFloat(k[1]),
			High:          jsonStrFloat(k[2]),
			Low:           jsonStrFloat(k[3]),
			Close:         jsonStrFloat(k[4]),
			Volume:        jsonStrFloat(k[5]),
		})
	}
	return out, nil
}

// Collect fetches [startMs, endMs] klines for symbol and upserts them under
// instrumentKey. Returns the number of candles stored.
func (c *BinanceCollector) Collect(ctx context.Context, st *store.Store, instrumentKey, symbol, interval string, startMs, endMs int64) (int, error) {
	candles, err := c.FetchKlines(ctx, instrumentKey, symbol, interval, startMs, endMs)
	if err != nil {
		return 0, err
	}
	if len(candles) == 0 {
		return 0, nil
	}
	if err := st.UpsertCandles(candles); err != nil {
		return 0, err
	}
	return len(candles), nil
}

// CollectIncremental resumes from the latest stored bar for (instrumentKey,
// interval): it queries store.LatestCandleTime and only fetches newer data up
// to endMs. If nothing is stored yet it falls back to fullStartMs.
func (c *BinanceCollector) CollectIncremental(ctx context.Context, st *store.Store, instrumentKey, symbol, interval string, fullStartMs, endMs int64) (int, error) {
	last, err := st.LatestCandleTime(instrumentKey, interval)
	if err != nil {
		return 0, err
	}
	start := fullStartMs
	if last > 0 {
		start = last + 1
	}
	if endMs > 0 && start > endMs {
		return 0, nil // already up to date
	}
	return c.Collect(ctx, st, instrumentKey, symbol, interval, start, endMs)
}

// ── numeric coercion helpers (Binance returns OHLCV as JSON strings) ──

func jsonStrFloat(v any) float64 {
	switch n := v.(type) {
	case string:
		f, _ := strconv.ParseFloat(n, 64)
		return f
	case float64:
		return n
	default:
		return 0
	}
}

func jsonNumberToInt64(v any) (int64, bool) {
	switch n := v.(type) {
	case float64:
		return int64(n), true
	case string:
		i, err := strconv.ParseInt(n, 10, 64)
		return i, err == nil
	default:
		return 0, false
	}
}
