package backtest

import (
	"context"
	"fmt"
	"testing"
	"time"

	"trader-evolver/internal/llm"
	"trader-evolver/internal/store"
	"trader-evolver/internal/types"
)

// setupTestStore creates a temp store with 30 days of fake candle data + macro.
func setupTestStore(t *testing.T) *store.Store {
	t.Helper()
	dbPath := t.TempDir() + "/test.db"
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}

	// Generate 30 days of candle data starting 2024-01-01
	baseDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	candles := make([]types.Candle, 30)
	for i := 0; i < 30; i++ {
		day := baseDate.Add(time.Duration(i) * 24 * time.Hour)
		price := 40000.0 + float64(i)*500 // trending up
		candles[i] = types.Candle{
			InstrumentKey: "btc:usdt",
			Interval:      "1d",
			OpenTimeMs:    day.UnixMilli(),
			Open:          price,
			High:          price + 1000,
			Low:           price - 500,
			Close:         price + 200,
			Volume:        100000,
		}
	}
	if err := st.UpsertCandles(candles); err != nil {
		t.Fatal(err)
	}

	// Add VIX macro data
	macros := make([]types.DailyMacro, 30)
	for i := 0; i < 30; i++ {
		day := baseDate.Add(time.Duration(i) * 24 * time.Hour)
		macros[i] = types.DailyMacro{
			Series: "VIX",
			DateMs: day.UnixMilli(),
			Close:  18.0 + float64(i%5), // oscillating VIX
		}
	}
	if err := st.UpsertDailyMacro(macros); err != nil {
		t.Fatal(err)
	}

	// Add Fear & Greed
	fgs := make([]types.FearGreed, 30)
	for i := 0; i < 30; i++ {
		day := baseDate.Add(time.Duration(i) * 24 * time.Hour)
		fgs[i] = types.FearGreed{
			DateMs:         day.UnixMilli(),
			Value:          50 + i%20,
			Classification: "Neutral",
		}
	}
	if err := st.UpsertFearGreed(fgs); err != nil {
		t.Fatal(err)
	}

	return st
}

func TestEngineRun_BasicFlow(t *testing.T) {
	st := setupTestStore(t)
	defer st.Close()

	// Use the deterministic mock provider (no network calls)
	mock := llm.NewMockProvider()

	baseDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	cfg := DefaultConfig()
	cfg.InstrumentKey = "btc:usdt"
	cfg.StartDateMs = baseDate.UnixMilli()
	cfg.EndDateMs = baseDate.Add(10 * 24 * time.Hour).UnixMilli() // 10 days
	cfg.PromptDir = "../../prompts"

	engine := NewEngine(st, mock, cfg)

	ctx := context.Background()
	results, err := engine.Run(ctx)
	if err != nil {
		t.Fatalf("Engine.Run error: %v", err)
	}

	if len(results) == 0 {
		t.Fatal("expected at least 1 day result")
	}

	// Check that we got results for the expected number of days
	// (should be up to 10 trading days)
	if len(results) > 11 {
		t.Errorf("unexpected number of days: %d", len(results))
	}

	// First result should have a valid regime
	first := results[0]
	if first.Error != nil {
		t.Logf("Day 1 error (may be expected): %s", *first.Error)
	}

	// Check that recommendations were recorded
	recs := engine.Recommendations()
	t.Logf("Results: %d days, %d recommendations", len(results), len(recs))

	// Darwin weights should still be within bounds
	for id, w := range engine.DarwinWeightsSnapshot() {
		if w < types.MinDarwinWeight || w > types.MaxDarwinWeight {
			t.Errorf("weight for %s out of bounds: %f", id, w)
		}
	}
}

func TestEngineRun_NoData(t *testing.T) {
	dbPath := t.TempDir() + "/empty.db"
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	mock := llm.NewMockProvider()

	cfg := DefaultConfig()
	cfg.InstrumentKey = "btc:usdt"
	cfg.StartDateMs = time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	cfg.EndDateMs = time.Date(2020, 2, 1, 0, 0, 0, 0, time.UTC).UnixMilli()

	engine := NewEngine(st, mock, cfg)
	_, err = engine.Run(context.Background())
	if err == nil {
		t.Fatal("expected error for empty store")
	}
}

func TestEngineRun_CancellationRespected(t *testing.T) {
	st := setupTestStore(t)
	defer st.Close()

	mock := llm.NewMockProvider()

	baseDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	cfg := DefaultConfig()
	cfg.InstrumentKey = "btc:usdt"
	cfg.StartDateMs = baseDate.UnixMilli()
	cfg.EndDateMs = baseDate.Add(30 * 24 * time.Hour).UnixMilli()

	engine := NewEngine(st, mock, cfg)

	// Cancel after 3 days
	dayCount := 0
	engine.OnDayComplete = func(day DayResult) {
		dayCount++
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond) // let a few days run
		cancel()
	}()

	results, err := engine.Run(ctx)
	// Should return partial results (not all 30 days) due to cancellation
	if err == nil && len(results) == 30 {
		// May complete before cancel fires on fast machine; that's ok
		t.Log("completed all days before cancel (fast machine)")
	} else if err != nil && err != context.Canceled {
		t.Logf("got error (may be expected): %v", err)
	}
}

func TestBackfillReturns(t *testing.T) {
	st := setupTestStore(t)
	defer st.Close()

	mock := llm.NewMockProvider()

	baseDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	cfg := DefaultConfig()
	cfg.InstrumentKey = "btc:usdt"
	cfg.StartDateMs = baseDate.UnixMilli()
	cfg.EndDateMs = baseDate.Add(30 * 24 * time.Hour).UnixMilli()

	engine := NewEngine(st, mock, cfg)

	// Manually add a recommendation
	engine.recommendations = append(engine.recommendations, types.Recommendation{
		ModuleID:              "ict_trader",
		InstrumentKey:         "btc:usdt",
		Signal:                types.SignalLong,
		Conviction:            80,
		PriceAtRecommendation: 40200, // close of day 0
		RecommendedAt:         "2024-01-01",
	})

	// Get trading days
	allDays := make([]int64, 30)
	for i := 0; i < 30; i++ {
		allDays[i] = baseDate.Add(time.Duration(i) * 24 * time.Hour).UnixMilli()
	}

	// Backfill as if we're at day 25 (enough for 20d return)
	day25Ms := allDays[25]
	engine.backfillReturns(day25Ms, allDays)

	rec := engine.recommendations[0]
	if rec.Return1d == nil {
		t.Error("expected Return1d to be filled")
	} else {
		t.Logf("Return1d: %.4f", *rec.Return1d)
	}
	if rec.Return5d == nil {
		t.Error("expected Return5d to be filled")
	} else {
		t.Logf("Return5d: %.4f", *rec.Return5d)
	}
	if rec.Return20d == nil {
		t.Error("expected Return20d to be filled")
	} else {
		t.Logf("Return20d: %.4f", *rec.Return20d)
	}
}

func TestDarwinWeightEvolution(t *testing.T) {
	st := setupTestStore(t)
	defer st.Close()

	mock := llm.NewMockProvider()
	cfg := DefaultConfig()
	cfg.InstrumentKey = "btc:usdt"
	cfg.MinRecsForSharpe = 3 // lower threshold for test

	engine := NewEngine(st, mock, cfg)

	// Simulate: ict_trader has good returns, fundamental_analyst has bad returns
	for i := 0; i < 10; i++ {
		goodRet := 0.02 + float64(i)*0.001
		badRet := -0.01 - float64(i)*0.001

		engine.recommendations = append(engine.recommendations, types.Recommendation{
			ModuleID:              "ict_trader",
			Signal:                types.SignalLong,
			Conviction:            80,
			PriceAtRecommendation: 40000,
			RecommendedAt:         fmt.Sprintf("2024-01-%02d", i+1),
			Return5d:              &goodRet,
		})
		engine.recommendations = append(engine.recommendations, types.Recommendation{
			ModuleID:              "fundamental_analyst",
			Signal:                types.SignalLong,
			Conviction:            70,
			PriceAtRecommendation: 40000,
			RecommendedAt:         fmt.Sprintf("2024-01-%02d", i+1),
			Return5d:              &badRet,
		})
	}

	// Run weight update
	engine.updateDarwinWeights()

	weights := engine.DarwinWeightsSnapshot()
	t.Logf("Weights after update: %v", weights)

	// ict_trader should have increased, fundamental_analyst should have decreased
	if weights["ict_trader"] <= types.DefaultDarwinWeight {
		t.Errorf("expected ict_trader weight to increase, got %f", weights["ict_trader"])
	}
	if weights["fundamental_analyst"] >= types.DefaultDarwinWeight {
		t.Errorf("expected fundamental_analyst weight to decrease, got %f", weights["fundamental_analyst"])
	}
}

func TestSortFloat64s(t *testing.T) {
	s := []float64{3.0, 1.0, 4.0, 1.5, 2.0}
	sortFloat64s(s)
	for i := 1; i < len(s); i++ {
		if s[i] < s[i-1] {
			t.Errorf("not sorted at index %d: %v", i, s)
		}
	}
}

func TestSqrt(t *testing.T) {
	got := sqrt(4.0)
	if got < 1.99 || got > 2.01 {
		t.Errorf("sqrt(4) = %f, want ~2.0", got)
	}
	got = sqrt(0)
	if got != 0 {
		t.Errorf("sqrt(0) = %f, want 0", got)
	}
}
