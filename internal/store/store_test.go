package store

import (
	"path/filepath"
	"testing"
	"time"

	"trader-evolver/internal/evolution"
	"trader-evolver/internal/types"
)

// Store must satisfy the evolution.Store interface.
var _ evolution.Store = (*Store)(nil)

func openTemp(t *testing.T) *Store {
	t.Helper()
	p := filepath.Join(t.TempDir(), "test.sqlite3")
	s, err := Open(p)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestCandlesRoundTripAndCoverage(t *testing.T) {
	s := openTemp(t)
	candles := []Candle{
		{OpenTimeMs: 1000, Open: 1, High: 2, Low: 0.5, Close: 1.5, Volume: 10},
		{OpenTimeMs: 2000, Open: 1.5, High: 2.5, Low: 1, Close: 2, Volume: 20},
		{OpenTimeMs: 3000, Open: 2, High: 3, Low: 1.5, Close: 2.5, Volume: 30},
	}
	if err := s.UpsertCandles("BTC", "1d", candles); err != nil {
		t.Fatal(err)
	}
	// Upsert again (idempotent) with a changed close on one bar.
	candles[1].Close = 2.2
	if err := s.UpsertCandles("BTC", "1d", candles); err != nil {
		t.Fatal(err)
	}

	got, err := s.GetCandles("BTC", "1d", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("want 3 candles, got %d", len(got))
	}
	if got[1].Close != 2.2 {
		t.Errorf("upsert should update close, got %v", got[1].Close)
	}

	cnt, minMs, maxMs, err := s.CandleCoverage("BTC", "1d")
	if err != nil {
		t.Fatal(err)
	}
	if cnt != 3 || minMs != 1000 || maxMs != 3000 {
		t.Errorf("coverage got cnt=%d min=%d max=%d", cnt, minMs, maxMs)
	}

	// Range query.
	mid, _ := s.GetCandles("BTC", "1d", 2000, 2000)
	if len(mid) != 1 || mid[0].OpenTimeMs != 2000 {
		t.Errorf("range query got %+v", mid)
	}
}

func TestDailyMacroAndFearGreed(t *testing.T) {
	s := openTemp(t)
	vix := 18.5
	if err := s.UpsertDailyMacro([]DailyMacro{{Date: "2020-01-02", VIX: &vix}}); err != nil {
		t.Fatal(err)
	}
	m, err := s.GetDailyMacro("2020-01-02")
	if err != nil || m == nil || m.VIX == nil || *m.VIX != 18.5 {
		t.Fatalf("macro got %+v err=%v", m, err)
	}
	if absent, _ := s.GetDailyMacro("1999-01-01"); absent != nil {
		t.Error("expected nil for absent date")
	}

	fgs := []FearGreed{
		{Date: "2020-01-01", Value: 30, Classification: "Fear"},
		{Date: "2020-01-05", Value: 70, Classification: "Greed"},
	}
	if err := s.UpsertFearGreed(fgs); err != nil {
		t.Fatal(err)
	}
	// As-of 2020-01-03 should return the 01-01 reading.
	asof, _ := s.GetFearGreedAsOf("2020-01-03")
	if asof == nil || asof.Date != "2020-01-01" || asof.Value != 30 {
		t.Fatalf("as-of got %+v", asof)
	}
}

func TestEvolutionStoreImpl(t *testing.T) {
	s := openTemp(t)
	// ensureDefaults seeds 5 modules at weight 1.0.
	weights := s.GetDarwinWeights()
	if len(weights) != len(types.DefaultModuleIDs) {
		t.Fatalf("expected %d default weights, got %d", len(types.DefaultModuleIDs), len(weights))
	}

	sharpe := 1.2
	hit := 0.6
	s.UpdateDarwinWeight("ict_trader", 1.05, &sharpe, &hit)
	for _, w := range s.GetDarwinWeights() {
		if w.ModuleID == "ict_trader" {
			if w.Weight != 1.05 || w.Sharpe30d == nil || *w.Sharpe30d != 1.2 {
				t.Errorf("weight update wrong: %+v", w)
			}
		}
	}
	if h := s.GetWeightHistory("ict_trader", 10); len(h) != 1 {
		t.Errorf("expected 1 history row, got %d", len(h))
	}

	now := time.Now().UTC()
	old := now.Add(-10 * 24 * time.Hour).Format(time.RFC3339)
	s.InsertRecommendation(types.Recommendation{
		ModuleID: "ict_trader", InstrumentKey: "BTC", Signal: types.SignalLong,
		Conviction: 80, PriceAtRecommendation: 100, RecommendedAt: old,
	})
	recs := s.GetModuleRecommendations("ict_trader", 30)
	if len(recs) != 1 {
		t.Fatalf("expected 1 rec, got %d", len(recs))
	}
	unfilled := s.GetUnfilledRecommendations("return_5d", 10)
	if len(unfilled) != 1 {
		t.Fatalf("expected 1 unfilled, got %d", len(unfilled))
	}
	s.UpdateReturn(unfilled[0].ID, "return_5d", 0.1)
	if again := s.GetUnfilledRecommendations("return_5d", 10); len(again) != 0 {
		t.Errorf("expected 0 unfilled after update, got %d", len(again))
	}
}
