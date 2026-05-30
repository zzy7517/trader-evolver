// Yahoo Finance daily OHLCV collector for stocks, indices, commodities, VIX, DXY.
//
// Uses the unofficial chart API: /v8/finance/chart/{symbol}?period1=...&period2=...&interval=1d
// which requires no API key. Returns daily bars that are stored either as
// DailyMacro (for VIX/DXY/SPX) or Candles (for individual stocks/ETFs).
//
// Source routing (README principle #5): stocks/indices/commodities/VIX/DXY → Yahoo Finance.
package collectors

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"trader-evolver/internal/store"
	"trader-evolver/internal/types"
)

// DefaultYahooBaseURL is the Yahoo Finance chart endpoint.
const DefaultYahooBaseURL = "https://query1.finance.yahoo.com"

// YahooCollector fetches daily OHLCV from Yahoo Finance chart API.
type YahooCollector struct {
	BaseURL    string
	HTTPClient *http.Client
	MaxRetries int
	// PageDelay between requests when doing multi-symbol fetches.
	PageDelay time.Duration
}

// NewYahooCollector returns a collector with sane defaults.
func NewYahooCollector() *YahooCollector {
	return &YahooCollector{
		BaseURL: DefaultYahooBaseURL,
		HTTPClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		MaxRetries: 3,
		PageDelay:  500 * time.Millisecond,
	}
}

// MacroSeries maps a series name to its Yahoo ticker symbol.
// These are stored as DailyMacro rows (single "close" value per day).
var MacroSeries = map[string]string{
	"VIX": "^VIX",
	"DXY": "DX-Y.NYB",
	"SPX": "^GSPC",
}

// yahooChartResponse is the top-level Yahoo chart JSON structure.
type yahooChartResponse struct {
	Chart struct {
		Result []yahooChartResult `json:"result"`
		Error  *struct {
			Code        string `json:"code"`
			Description string `json:"description"`
		} `json:"error"`
	} `json:"chart"`
}

type yahooChartResult struct {
	Meta       yahooMeta       `json:"meta"`
	Timestamp  []int64         `json:"timestamp"`
	Indicators yahooIndicators `json:"indicators"`
}

type yahooMeta struct {
	Symbol             string  `json:"symbol"`
	Currency           string  `json:"currency"`
	RegularMarketPrice float64 `json:"regularMarketPrice"`
	GMTOffset          int     `json:"gmtoffset"`
}

type yahooIndicators struct {
	Quote []yahooQuote `json:"quote"`
}

type yahooQuote struct {
	Open   []*float64 `json:"open"`
	High   []*float64 `json:"high"`
	Low    []*float64 `json:"low"`
	Close  []*float64 `json:"close"`
	Volume []*float64 `json:"volume"`
}

// FetchDaily fetches daily OHLCV for a single Yahoo symbol in [startMs, endMs].
// Returns (candles, error). Each candle's OpenTimeMs is the midnight-UTC timestamp
// of the trading day.
func (c *YahooCollector) FetchDaily(ctx context.Context, instrumentKey, yahooSymbol string, startMs, endMs int64) ([]types.Candle, error) {
	period1 := startMs / 1000 // Yahoo uses seconds
	period2 := endMs / 1000

	endpoint := fmt.Sprintf("%s/v8/finance/chart/%s?period1=%d&period2=%d&interval=1d&events=history",
		c.BaseURL, yahooSymbol, period1, period2)

	body, err := c.doRequestWithRetry(ctx, endpoint)
	if err != nil {
		return nil, err
	}

	return parseYahooDaily(body, instrumentKey)
}

// doRequestWithRetry performs an HTTP GET with exponential backoff.
func (c *YahooCollector) doRequestWithRetry(ctx context.Context, endpoint string) ([]byte, error) {
	var lastErr error
	for attempt := 0; attempt <= c.MaxRetries; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(1<<uint(attempt-1)) * time.Second
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff):
			}
		}
		body, err := c.doRequest(ctx, endpoint)
		if err == nil {
			return body, nil
		}
		lastErr = err
	}
	return nil, fmt.Errorf("yahoo chart after %d retries: %w", c.MaxRetries, lastErr)
}

func (c *YahooCollector) doRequest(ctx context.Context, endpoint string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	// Yahoo requires a User-Agent header; otherwise it may return 403.
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; trader-evolver/1.0)")
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("yahoo status %d: %s", resp.StatusCode, truncateBody(body))
	}
	return body, nil
}

// parseYahooDaily extracts candles from the Yahoo chart JSON response.
func parseYahooDaily(body []byte, instrumentKey string) ([]types.Candle, error) {
	var resp yahooChartResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("yahoo: decode: %w", err)
	}
	if resp.Chart.Error != nil {
		return nil, fmt.Errorf("yahoo API error: %s: %s", resp.Chart.Error.Code, resp.Chart.Error.Description)
	}
	if len(resp.Chart.Result) == 0 {
		return nil, fmt.Errorf("yahoo: empty result for %s", instrumentKey)
	}

	result := resp.Chart.Result[0]
	if len(result.Indicators.Quote) == 0 {
		return nil, fmt.Errorf("yahoo: no quote data for %s", instrumentKey)
	}

	q := result.Indicators.Quote[0]
	timestamps := result.Timestamp
	n := len(timestamps)

	out := make([]types.Candle, 0, n)
	for i := 0; i < n; i++ {
		// Skip bars where any OHLC is nil (market holidays, missing data)
		if i >= len(q.Open) || i >= len(q.Close) || i >= len(q.High) || i >= len(q.Low) {
			continue
		}
		if q.Open[i] == nil || q.Close[i] == nil || q.High[i] == nil || q.Low[i] == nil {
			continue
		}

		vol := 0.0
		if i < len(q.Volume) && q.Volume[i] != nil {
			vol = *q.Volume[i]
		}

		// Convert Unix seconds to millis; normalize to midnight UTC
		dayMs := normalizeToMidnightUTC(timestamps[i])

		out = append(out, types.Candle{
			InstrumentKey: instrumentKey,
			Interval:      "1d",
			OpenTimeMs:    dayMs,
			Open:          *q.Open[i],
			High:          *q.High[i],
			Low:           *q.Low[i],
			Close:         *q.Close[i],
			Volume:        vol,
		})
	}
	return out, nil
}

// normalizeToMidnightUTC takes a Unix timestamp (seconds) and returns the
// midnight-UTC epoch millis for that day.
func normalizeToMidnightUTC(ts int64) int64 {
	t := time.Unix(ts, 0).UTC()
	midnight := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	return midnight.UnixMilli()
}

// ─── Store integration ───

// CollectCandles fetches daily bars for a single symbol and upserts as Candles.
// instrumentKey is the canonical key (e.g. "AAPL", "SPY").
func (c *YahooCollector) CollectCandles(ctx context.Context, st *store.Store, instrumentKey, yahooSymbol string, startMs, endMs int64) (int, error) {
	candles, err := c.FetchDaily(ctx, instrumentKey, yahooSymbol, startMs, endMs)
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

// CollectMacro fetches a macro series (VIX/DXY/SPX) and upserts as DailyMacro.
// Uses the Close as the daily value.
func (c *YahooCollector) CollectMacro(ctx context.Context, st *store.Store, series, yahooSymbol string, startMs, endMs int64) (int, error) {
	candles, err := c.FetchDaily(ctx, series, yahooSymbol, startMs, endMs)
	if err != nil {
		return 0, err
	}
	if len(candles) == 0 {
		return 0, nil
	}

	macros := make([]types.DailyMacro, len(candles))
	for i, cd := range candles {
		macros[i] = types.DailyMacro{
			Series: series,
			DateMs: cd.OpenTimeMs,
			Close:  cd.Close,
		}
	}
	if err := st.UpsertDailyMacro(macros); err != nil {
		return 0, err
	}
	return len(macros), nil
}

// CollectCandlesIncremental resumes from the latest stored candle.
func (c *YahooCollector) CollectCandlesIncremental(ctx context.Context, st *store.Store, instrumentKey, yahooSymbol string, fullStartMs, endMs int64) (int, error) {
	last, err := st.LatestCandleTime(instrumentKey, "1d")
	if err != nil {
		return 0, err
	}
	start := fullStartMs
	if last > 0 {
		start = last + 1
	}
	if endMs > 0 && start > endMs {
		return 0, nil
	}
	return c.CollectCandles(ctx, st, instrumentKey, yahooSymbol, start, endMs)
}

// ── helpers ──

func truncateBody(b []byte) string {
	s := string(b)
	if len(s) > 200 {
		return s[:200] + "..."
	}
	return s
}

// safeFloat converts a *float64 to float64 (nil → 0).
func safeFloat(p *float64) float64 {
	if p == nil {
		return 0
	}
	return *p
}

// unused but may be useful for future extensions
var _ = safeFloat
var _ = strconv.Itoa
