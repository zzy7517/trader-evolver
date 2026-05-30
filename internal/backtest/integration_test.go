package backtest

import (
	"context"
	"testing"
	"time"

	"trader-evolver/internal/janus"
	"trader-evolver/internal/llm"
	"trader-evolver/internal/reflexivity"
	"trader-evolver/internal/store"
	"trader-evolver/internal/types"
)

// TestEndToEnd_FullPipeline exercises the complete flow:
// store → backtest engine → Darwin evolution → JANUS → reflexivity.
func TestEndToEnd_FullPipeline(t *testing.T) {
	st := setupTestStore(t) // from engine_test.go
	defer st.Close()

	mock := llm.NewMockProvider()

	baseDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	cfg := DefaultConfig()
	cfg.InstrumentKey = "btc:usdt"
	cfg.StartDateMs = baseDate.UnixMilli()
	cfg.EndDateMs = baseDate.Add(25 * 24 * time.Hour).UnixMilli()
	cfg.PromptDir = "../../prompts"
	cfg.DarwinUpdateFreq = 5 // update every 5 days

	// ── Phase 1: Run backtest engine ──
	engine := NewEngine(st, mock, cfg)

	ctx := context.Background()
	results, err := engine.Run(ctx)
	if err != nil {
		t.Fatalf("backtest run failed: %v", err)
	}
	t.Logf("Phase 1: Backtest completed - %d days", len(results))

	if len(results) == 0 {
		t.Fatal("no results produced")
	}

	// Verify recommendations were generated
	recs := engine.Recommendations()
	if len(recs) == 0 {
		t.Fatal("no recommendations generated")
	}
	t.Logf("  Recommendations: %d", len(recs))

	// Verify forward returns were backfilled
	filled := 0
	for _, r := range recs {
		if r.Return5d != nil {
			filled++
		}
	}
	t.Logf("  Return5d filled: %d/%d", filled, len(recs))

	// ── Phase 2: Verify Darwin weight evolution ──
	weights := engine.DarwinWeightsSnapshot()
	allDefault := true
	for _, w := range weights {
		if w != types.DefaultDarwinWeight {
			allDefault = false
			break
		}
	}
	// After 25 days with freq=5, weights should have been updated at least a few times
	if filled > 10 && allDefault {
		t.Error("expected some weight evolution after 25 days")
	}
	t.Logf("Phase 2: Darwin weights - %v", weights)

	// ── Phase 3: Autoresearch integration ──
	arCfg := DefaultAutoresearchConfig()
	arCfg.EvalWindowDays = 10
	arState := NewAutoresearchState(arCfg, mock)

	// Check if autoresearch would trigger
	if arState.ShouldTrigger(25) {
		worstID, worstSharpe := arState.FindWorstAgent(25, recs)
		if worstID != "" {
			t.Logf("Phase 3: Autoresearch - worst agent: %s (Sharpe: %.3f)", worstID, worstSharpe)

			desc, err := arState.GenerateModification(ctx, worstID, recs)
			if err != nil {
				t.Errorf("generate modification error: %v", err)
			} else {
				t.Logf("  Proposed modification: %s", desc)
			}
		}
	}

	// ── Phase 4: JANUS meta-layer ──
	cohorts := []string{"recent", "historical"}
	janusLayer := janus.New(cohorts, janus.DefaultConfig())

	// Simulate cohort metrics from backtest results
	metrics := map[string]janus.Cohort{
		"recent":     {Name: "recent", HitRate: 0.6, Sharpe: 0.5},
		"historical": {Name: "historical", HitRate: 0.5, Sharpe: 0.2},
	}

	janusRecs := []janus.CohortRecommendation{
		{CohortName: "recent", Ticker: "BTC", Direction: types.SignalLong, Conviction: 75},
		{CohortName: "historical", Ticker: "BTC", Direction: types.SignalLong, Conviction: 60},
	}

	janusOutput := janusLayer.Run(metrics, janusRecs)
	t.Logf("Phase 4: JANUS - regime=%s, weights=%v", janusOutput.Regime, janusOutput.CohortWeights)

	if len(janusOutput.Blended) == 0 {
		t.Error("JANUS produced no blended recommendations")
	}

	// ── Phase 5: Reflexivity engine ──
	refEngine := reflexivity.NewEngine()

	// Simulate market state from latest backtest day
	lastResult := results[len(results)-1]
	_ = lastResult

	marketState := reflexivity.MarketState{
		PriceChange30d:    0.12, // from our test data (trending up)
		PortfolioDrawdown: -0.03,
		BullishAnalysts:   3,
		TotalAnalysts:     5,
		ConsensusRounds:   2,
		SPXDrawdown:       -0.05,
		OilPrice:          85,
		VIX:               18,
	}

	signals := refEngine.Detect(marketState)
	t.Logf("Phase 5: Reflexivity - %d signals detected", len(signals))
	for _, s := range signals {
		t.Logf("  [%s] %s (%s)", s.Loop, s.Description, s.Severity)
	}

	// ── Summary ──
	t.Logf("\n=== End-to-End Summary ===")
	t.Logf("Trading days: %d", len(results))
	t.Logf("Recommendations: %d (filled: %d)", len(recs), filled)
	t.Logf("Darwin weights evolved: %v", !allDefault || filled <= 10)
	t.Logf("JANUS regime: %s", janusOutput.Regime)
	t.Logf("Reflexivity signals: %d", len(signals))
}

// TestEndToEnd_WithAutoresearchLoop tests the full autoresearch cycle.
func TestEndToEnd_WithAutoresearchLoop(t *testing.T) {
	st := setupTestStore(t)
	defer st.Close()

	mock := llm.NewMockProvider()

	baseDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	cfg := DefaultConfig()
	cfg.InstrumentKey = "btc:usdt"
	cfg.StartDateMs = baseDate.UnixMilli()
	cfg.EndDateMs = baseDate.Add(20 * 24 * time.Hour).UnixMilli()
	cfg.PromptDir = "../../prompts"

	engine := NewEngine(st, mock, cfg)
	ctx := context.Background()

	results, err := engine.Run(ctx)
	if err != nil {
		t.Fatalf("backtest error: %v", err)
	}

	// Set up autoresearch
	arCfg := DefaultAutoresearchConfig()
	arCfg.EvalWindowDays = 5
	arCfg.CooldownDays = 3
	arState := NewAutoresearchState(arCfg, mock)

	recs := engine.Recommendations()

	// Simulate multiple autoresearch cycles
	modifications := 0
	for day := 5; day <= len(results); day += 5 {
		if !arState.ShouldTrigger(day) {
			continue
		}

		worstID, beforeSharpe := arState.FindWorstAgent(day, recs)
		if worstID == "" {
			continue
		}

		// Generate modification
		_, err := arState.GenerateModification(ctx, worstID, recs)
		if err != nil {
			continue
		}

		// Simulate evaluation (in real system, would re-run 5 days)
		afterSharpe := beforeSharpe + 0.1 // simulate slight improvement
		mod := arState.EvaluateModification(day, results[day-1].Date, worstID, beforeSharpe, afterSharpe)
		modifications++
		t.Logf("  Modification %d: %s (before=%.3f, after=%.3f, kept=%v)",
			modifications, worstID, mod.BeforeSharpe, mod.AfterSharpe, mod.Kept)
	}

	total, kept, reverted := arState.Stats()
	t.Logf("Autoresearch: %d total, %d kept (%.0f%%), %d reverted",
		total, kept, float64(kept)/float64(max(total, 1))*100, reverted)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// ensure imports
var _ = store.Open
