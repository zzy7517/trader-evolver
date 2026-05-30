package collectors

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"trader-evolver/internal/store"
	"trader-evolver/internal/types"
)

// sampleYahooResponse builds a minimal chart JSON with N bars.
func sampleYahooResponse(symbol string, n int) []byte {
	baseTs := int64(1700000000) // some date in 2023
	timestamps := make([]int64, n)
	opens := make([]*float64, n)
	highs := make([]*float64, n)
	lows := make([]*float64, n)
	closes := make([]*float64, n)
	volumes := make([]*float64, n)

	for i := 0; i < n; i++ {
		timestamps[i] = baseTs + int64(i*86400) // one day apart
		o := 100.0 + float64(i)
		h := o + 2.0
		l := o - 1.0
		c := o + 1.0
		v := 1000000.0
		opens[i] = &o
		highs[i] = &h
		lows[i] = &l
		closes[i] = &c
		volumes[i] = &v
	}

	resp := map[string]any{
		"chart": map[string]any{
			"result": []map[string]any{
				{
					"meta": map[string]any{
						"symbol":   symbol,
						"currency": "USD",
					},
					"timestamp": timestamps,
					"indicators": map[string]any{
						"quote": []map[string]any{
							{
								"open":   opens,
								"high":   highs,
								"low":    lows,
								"close":  closes,
								"volume": volumes,
							},
						},
					},
				},
			},
			"error": nil,
		},
	}

	b, _ := json.Marshal(resp)
	return b
}

func TestYahooParseDaily(t *testing.T) {
	body := sampleYahooResponse("AAPL", 5)
	candles, err := parseYahooDaily(body, "AAPL")
	if err != nil {
		t.Fatalf("parseYahooDaily error: %v", err)
	}
	if len(candles) != 5 {
		t.Fatalf("expected 5 candles, got %d", len(candles))
	}
	for _, cd := range candles {
		if cd.InstrumentKey != "AAPL" {
			t.Errorf("expected instrumentKey=AAPL, got %s", cd.InstrumentKey)
		}
		if cd.Interval != "1d" {
			t.Errorf("expected interval=1d, got %s", cd.Interval)
		}
		if cd.Open <= 0 || cd.Close <= 0 {
			t.Error("OHLC should be positive")
		}
	}
}

func TestYahooParseDaily_NilBars(t *testing.T) {
	// Build a response with some nil values in the middle
	baseTs := int64(1700000000)
	timestamps := []int64{baseTs, baseTs + 86400, baseTs + 172800}
	o1, h1, l1, c1 := 100.0, 102.0, 99.0, 101.0
	o3, h3, l3, c3 := 102.0, 104.0, 101.0, 103.0
	v := 1000000.0

	resp := map[string]any{
		"chart": map[string]any{
			"result": []map[string]any{
				{
					"meta":      map[string]any{"symbol": "TEST"},
					"timestamp": timestamps,
					"indicators": map[string]any{
						"quote": []map[string]any{
							{
								"open":   []*float64{&o1, nil, &o3},
								"high":   []*float64{&h1, nil, &h3},
								"low":    []*float64{&l1, nil, &l3},
								"close":  []*float64{&c1, nil, &c3},
								"volume": []*float64{&v, nil, &v},
							},
						},
					},
				},
			},
			"error": nil,
		},
	}
	body, _ := json.Marshal(resp)

	candles, err := parseYahooDaily(body, "TEST")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	// Should skip the nil bar in the middle
	if len(candles) != 2 {
		t.Fatalf("expected 2 candles (skip nil), got %d", len(candles))
	}
}

func TestYahooFetchDaily_HTTPTest(t *testing.T) {
	body := sampleYahooResponse("SPY", 10)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write(body)
	}))
	defer ts.Close()

	yc := &YahooCollector{
		BaseURL:    ts.URL,
		HTTPClient: ts.Client(),
		MaxRetries: 1,
	}

	ctx := context.Background()
	candles, err := yc.FetchDaily(ctx, "SPY", "SPY", 1700000000000, 1701000000000)
	if err != nil {
		t.Fatalf("FetchDaily error: %v", err)
	}
	if len(candles) != 10 {
		t.Fatalf("expected 10 candles, got %d", len(candles))
	}
}

func TestYahooCollectCandles_StoreIntegration(t *testing.T) {
	body := sampleYahooResponse("AAPL", 5)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write(body)
	}))
	defer ts.Close()

	// Open a temp store
	dbPath := t.TempDir() + "/test.db"
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer st.Close()

	yc := &YahooCollector{
		BaseURL:    ts.URL,
		HTTPClient: ts.Client(),
		MaxRetries: 1,
	}

	ctx := context.Background()
	n, err := yc.CollectCandles(ctx, st, "AAPL", "AAPL", 1700000000000, 1701000000000)
	if err != nil {
		t.Fatalf("CollectCandles error: %v", err)
	}
	if n != 5 {
		t.Fatalf("expected 5 stored, got %d", n)
	}

	// Verify via coverage
	cov, err := st.CandleCoverage("AAPL", "1d")
	if err != nil {
		t.Fatal(err)
	}
	if cov.Count != 5 {
		t.Errorf("expected coverage count=5, got %d", cov.Count)
	}
}

func TestYahooCollectMacro(t *testing.T) {
	body := sampleYahooResponse("^VIX", 3)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write(body)
	}))
	defer ts.Close()

	dbPath := t.TempDir() + "/test.db"
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer st.Close()

	yc := &YahooCollector{
		BaseURL:    ts.URL,
		HTTPClient: ts.Client(),
		MaxRetries: 1,
	}

	ctx := context.Background()
	n, err := yc.CollectMacro(ctx, st, "VIX", "^VIX", 1700000000000, 1701000000000)
	if err != nil {
		t.Fatalf("CollectMacro error: %v", err)
	}
	if n != 3 {
		t.Fatalf("expected 3, got %d", n)
	}

	// Verify as-of lookup
	// The 3rd candle's time: normalizeToMidnightUTC(1700000000 + 2*86400)
	ts3 := normalizeToMidnightUTC(1700000000 + 2*86400)
	val, found, err := st.MacroAsOf("VIX", ts3)
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("expected macro value, got not found")
	}
	if val <= 0 {
		t.Errorf("expected positive close, got %f", val)
	}
}

func TestYahooCollectCandlesIncremental(t *testing.T) {
	callCount := 0
	body := sampleYahooResponse("MSFT", 3)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.WriteHeader(http.StatusOK)
		w.Write(body)
	}))
	defer ts.Close()

	dbPath := t.TempDir() + "/test.db"
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	yc := &YahooCollector{
		BaseURL:    ts.URL,
		HTTPClient: ts.Client(),
		MaxRetries: 1,
	}

	ctx := context.Background()

	// First call: full fetch
	n, err := yc.CollectCandlesIncremental(ctx, st, "MSFT", "MSFT", 1700000000000, 1701000000000)
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("first call: expected 3, got %d", n)
	}

	// Second call: incremental (starts from last+1)
	// The latest stored time should be > the fullStartMs, so it fetches with a later start
	n2, err := yc.CollectCandlesIncremental(ctx, st, "MSFT", "MSFT", 1700000000000, 1701000000000)
	if err != nil {
		t.Fatal(err)
	}
	// Still returns 3 because our mock always returns the same data,
	// but the point is it called the server (callCount == 2)
	_ = n2
	if callCount != 2 {
		t.Errorf("expected 2 server calls, got %d", callCount)
	}
}

func TestYahooRetryOn500(t *testing.T) {
	attempts := 0
	body := sampleYahooResponse("GLD", 2)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts <= 2 {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("server error"))
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write(body)
	}))
	defer ts.Close()

	yc := &YahooCollector{
		BaseURL:    ts.URL,
		HTTPClient: ts.Client(),
		MaxRetries: 3,
	}

	ctx := context.Background()
	candles, err := yc.FetchDaily(ctx, "GLD", "GLD", 1700000000000, 1701000000000)
	if err != nil {
		t.Fatalf("expected success after retries, got: %v", err)
	}
	if len(candles) != 2 {
		t.Fatalf("expected 2 candles, got %d", len(candles))
	}
	if attempts != 3 {
		t.Errorf("expected 3 attempts, got %d", attempts)
	}
}

func TestNormalizeToMidnightUTC(t *testing.T) {
	// 2023-11-14 15:30:00 UTC
	ts := int64(1700000000)
	got := normalizeToMidnightUTC(ts)
	expected := time.Date(2023, 11, 14, 0, 0, 0, 0, time.UTC).UnixMilli()
	if got != expected {
		t.Errorf("normalizeToMidnightUTC(%d) = %d, want %d", ts, got, expected)
	}
}

// Ensure unused imports don't cause issues
var _ = os.DevNull
var _ = types.Candle{}
