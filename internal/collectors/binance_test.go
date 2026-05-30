package collectors

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"

	"trader-evolver/internal/store"
)

func TestParseBinanceKlines(t *testing.T) {
	body := []byte(`[
		[1000,"1.0","2.0","0.5","1.5","10.0",1999,"x",1,"y","z","0"],
		[2000,"1.5","2.5","1.0","2.0","20.0",2999,"x",1,"y","z","0"]
	]`)
	candles, err := parseBinanceKlines(body, "binance:BTCUSDT", "1d")
	if err != nil {
		t.Fatal(err)
	}
	if len(candles) != 2 {
		t.Fatalf("want 2, got %d", len(candles))
	}
	if candles[0].OpenTimeMs != 1000 || candles[0].Close != 1.5 || candles[1].Volume != 20 {
		t.Errorf("parsed wrong: %+v", candles)
	}
	// Tagging: instrument key + interval must be set on every candle.
	if candles[0].InstrumentKey != "binance:BTCUSDT" || candles[1].Interval != "1d" {
		t.Errorf("candles not tagged: %+v", candles)
	}
}

// fakeBinance serves paginated klines: full maxLimit-sized pages until the
// history (allTimes) is exhausted, forcing the collector to paginate.
func fakeBinance(t *testing.T, allTimes []int64) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start, _ := strconv.ParseInt(r.URL.Query().Get("startTime"), 10, 64)
		var page [][]any
		for _, ts := range allTimes {
			if ts < start {
				continue
			}
			page = append(page, []any{
				ts, "1.0", "2.0", "0.5", "1.5", "10.0", ts + 999, "x", 1, "y", "z", "0",
			})
			if len(page) >= binanceMaxLimit {
				break
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(page)
	}))
}

func TestBinanceCollectorPaginates(t *testing.T) {
	// More than one maxLimit-sized page worth of bars to force multiple fetches.
	n := binanceMaxLimit + 50
	times := make([]int64, n)
	for i := 0; i < n; i++ {
		times[i] = int64(1000 + i*1000)
	}
	srv := fakeBinance(t, times)
	defer srv.Close()

	c := NewBinanceCollector()
	c.BaseURL = srv.URL
	c.PageDelay = 0

	candles, err := c.FetchKlines(context.Background(), "binance:BTCUSDT", "BTCUSDT", "1d", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(candles) != n {
		t.Fatalf("expected %d candles across pages, got %d", n, len(candles))
	}
	// Ascending order and no duplicates across page boundary.
	for i := 1; i < len(candles); i++ {
		if candles[i].OpenTimeMs <= candles[i-1].OpenTimeMs {
			t.Fatalf("non-monotonic at %d: %d <= %d", i, candles[i].OpenTimeMs, candles[i-1].OpenTimeMs)
		}
	}
}

func TestBinanceCollectStoresToDB(t *testing.T) {
	srv := fakeBinance(t, []int64{1000, 2000, 3000})
	defer srv.Close()

	st, err := store.Open(filepath.Join(t.TempDir(), "c.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	c := NewBinanceCollector()
	c.BaseURL = srv.URL
	c.PageDelay = 0

	cnt, err := c.Collect(context.Background(), st, "binance:BTCUSDT", "BTCUSDT", "1d", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if cnt != 3 {
		t.Fatalf("expected 3 candles stored, got %d", cnt)
	}
	cov, _ := st.CandleCoverage("binance:BTCUSDT", "1d")
	if cov.Count != int64(cnt) {
		t.Errorf("stored %d but coverage count %d", cnt, cov.Count)
	}
	if cov.FirstOpenMs != 1000 || cov.LastOpenMs != 3000 {
		t.Errorf("coverage range wrong: %+v", cov)
	}
}

func TestBinanceCollectIncrementalResumes(t *testing.T) {
	srv := fakeBinance(t, []int64{1000, 2000, 3000, 4000})
	defer srv.Close()

	st, err := store.Open(filepath.Join(t.TempDir(), "c.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	c := NewBinanceCollector()
	c.BaseURL = srv.URL
	c.PageDelay = 0

	// First pass stores everything.
	if _, err := c.Collect(context.Background(), st, "binance:BTCUSDT", "BTCUSDT", "1d", 0, 0); err != nil {
		t.Fatal(err)
	}
	// Incremental should resume from last+1 and store nothing new (no newer bars).
	n2, err := c.CollectIncremental(context.Background(), st, "binance:BTCUSDT", "BTCUSDT", "1d", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if n2 != 0 {
		t.Fatalf("incremental should fetch 0 new bars, got %d", n2)
	}
}

func TestBinanceRetriesOn500(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, "boom")
			return
		}
		_ = json.NewEncoder(w).Encode([][]any{{1000, "1", "2", "0.5", "1.5", "10", 1999}})
	}))
	defer srv.Close()

	c := NewBinanceCollector()
	c.BaseURL = srv.URL
	c.PageDelay = 0
	c.MaxRetries = 5

	candles, err := c.FetchKlines(context.Background(), "binance:BTCUSDT", "BTCUSDT", "1d", 0, 2000)
	if err != nil {
		t.Fatalf("expected success after retries, got %v", err)
	}
	if len(candles) != 1 || calls < 3 {
		t.Errorf("expected retry then success, calls=%d candles=%d", calls, len(candles))
	}
}
