package store

import (
	"path/filepath"
	"testing"

	"trader-evolver/internal/types"
)

func openTemp(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.sqlite3")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestCandleUpsertAndRange(t *testing.T) {
	s := openTemp(t)
	candles := []types.Candle{
		{InstrumentKey: "hyperliquid:BTC", Interval: "1d", OpenTimeMs: 1000, Open: 1, High: 2, Low: 0.5, Close: 1.5, Volume: 10},
		{InstrumentKey: "hyperliquid:BTC", Interval: "1d", OpenTimeMs: 2000, Open: 1.5, High: 3, Low: 1, Close: 2.5, Volume: 20},
		{InstrumentKey: "hyperliquid:BTC", Interval: "1d", OpenTimeMs: 3000, Open: 2.5, High: 4, Low: 2, Close: 3, Volume: 30},
	}
	if err := s.UpsertCandles(candles); err != nil {
		t.Fatal(err)
	}

	// Idempotent re-upsert with a changed close should update, not duplicate.
	candles[1].Close = 99
	if err := s.UpsertCandles(candles[1:2]); err != nil {
		t.Fatal(err)
	}

	all, err := s.GetCandles("hyperliquid:BTC", "1d", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("expected 3 candles (idempotent), got %d", len(all))
	}
	if all[1].Close != 99 {
		t.Fatalf("upsert did not update close, got %v", all[1].Close)
	}
	// Ordering ascending by open time.
	if all[0].OpenTimeMs != 1000 || all[2].OpenTimeMs != 3000 {
		t.Fatalf("not ordered: %+v", all)
	}

	// Range filter [2000,3000].
	mid, _ := s.GetCandles("hyperliquid:BTC", "1d", 2000, 3000)
	if len(mid) != 2 || mid[0].OpenTimeMs != 2000 {
		t.Fatalf("range filter wrong: %+v", mid)
	}
}

func TestCandleCoverageAndLatest(t *testing.T) {
	s := openTemp(t)
	// Empty coverage.
	cov, err := s.CandleCoverage("X", "1h")
	if err != nil {
		t.Fatal(err)
	}
	if cov.Count != 0 || cov.FirstOpenMs != 0 || cov.LastOpenMs != 0 {
		t.Fatalf("empty coverage should be zero: %+v", cov)
	}
	latest, _ := s.LatestCandleTime("X", "1h")
	if latest != 0 {
		t.Fatalf("empty latest should be 0, got %d", latest)
	}

	_ = s.UpsertCandles([]types.Candle{
		{InstrumentKey: "X", Interval: "1h", OpenTimeMs: 500, Open: 1, High: 1, Low: 1, Close: 1, Volume: 1},
		{InstrumentKey: "X", Interval: "1h", OpenTimeMs: 1500, Open: 1, High: 1, Low: 1, Close: 1, Volume: 1},
	})
	cov, _ = s.CandleCoverage("X", "1h")
	if cov.Count != 2 || cov.FirstOpenMs != 500 || cov.LastOpenMs != 1500 {
		t.Fatalf("coverage wrong: %+v", cov)
	}
	latest, _ = s.LatestCandleTime("X", "1h")
	if latest != 1500 {
		t.Fatalf("latest=%d want 1500", latest)
	}
}

func TestDailyMacroAsOf(t *testing.T) {
	s := openTemp(t)
	if err := s.UpsertDailyMacro([]types.DailyMacro{
		{Series: "VIX", DateMs: 1000, Close: 18},
		{Series: "VIX", DateMs: 2000, Close: 22},
		{Series: "DXY", DateMs: 1000, Close: 100},
	}); err != nil {
		t.Fatal(err)
	}

	// As-of between 1000 and 2000 → returns the 1000 reading (most recent <=).
	v, ok, err := s.MacroAsOf("VIX", 1500)
	if err != nil || !ok || v != 18 {
		t.Fatalf("as-of 1500 got v=%v ok=%v err=%v want 18", v, ok, err)
	}
	v, ok, _ = s.MacroAsOf("VIX", 2500)
	if !ok || v != 22 {
		t.Fatalf("as-of 2500 want 22, got %v", v)
	}
	// Before any data → not found.
	_, ok, _ = s.MacroAsOf("VIX", 500)
	if ok {
		t.Fatal("as-of before data should be not-found")
	}
	// Series isolation.
	v, ok, _ = s.MacroAsOf("DXY", 5000)
	if !ok || v != 100 {
		t.Fatalf("DXY as-of want 100, got %v", v)
	}
}

func TestFearGreedAsOfAndCount(t *testing.T) {
	s := openTemp(t)
	n, _ := s.FearGreedCount()
	if n != 0 {
		t.Fatalf("empty count should be 0, got %d", n)
	}
	if err := s.UpsertFearGreed([]types.FearGreed{
		{DateMs: 1000, Value: 30, Classification: "Fear"},
		{DateMs: 2000, Value: 70, Classification: "Greed"},
	}); err != nil {
		t.Fatal(err)
	}
	n, _ = s.FearGreedCount()
	if n != 2 {
		t.Fatalf("count=%d want 2", n)
	}
	v, ok, _ := s.FearGreedAsOf(1999)
	if !ok || v != 30 {
		t.Fatalf("as-of 1999 want 30, got %v", v)
	}
	v, ok, _ = s.FearGreedAsOf(99999)
	if !ok || v != 70 {
		t.Fatalf("as-of latest want 70, got %v", v)
	}
}

func TestEmptyUpsertsNoOp(t *testing.T) {
	s := openTemp(t)
	if err := s.UpsertCandles(nil); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertDailyMacro(nil); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertFearGreed(nil); err != nil {
		t.Fatal(err)
	}
}
