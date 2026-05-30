package janus

import (
	"testing"

	"trader-evolver/internal/types"
)

func TestNew_EqualWeights(t *testing.T) {
	cohorts := []string{"18month", "10year"}
	j := New(cohorts, DefaultConfig())

	for _, c := range cohorts {
		w := j.Weights[c]
		if w < 0.49 || w > 0.51 {
			t.Errorf("expected ~0.5 for %s, got %f", c, w)
		}
	}
}

func TestUpdateWeights_BetterCohortGetsMore(t *testing.T) {
	cohorts := []string{"short", "long"}
	j := New(cohorts, DefaultConfig())

	// Short cohort performing much better
	metrics := map[string]Cohort{
		"short": {Name: "short", HitRate: 0.8, Sharpe: 1.5},
		"long":  {Name: "long", HitRate: 0.4, Sharpe: -0.5},
	}

	j.UpdateWeights(metrics)

	if j.Weights["short"] <= j.Weights["long"] {
		t.Errorf("expected short > long weight; got short=%f, long=%f",
			j.Weights["short"], j.Weights["long"])
	}
	t.Logf("Weights: short=%f, long=%f", j.Weights["short"], j.Weights["long"])
}

func TestUpdateWeights_FloorConstraint(t *testing.T) {
	cohorts := []string{"good", "terrible"}
	cfg := DefaultConfig()
	cfg.MinWeight = 0.2
	j := New(cohorts, cfg)

	metrics := map[string]Cohort{
		"good":     {HitRate: 1.0, Sharpe: 3.0},
		"terrible": {HitRate: 0.0, Sharpe: -3.0},
	}

	j.UpdateWeights(metrics)

	if j.Weights["terrible"] < cfg.MinWeight-0.01 {
		t.Errorf("terrible weight %f below floor %f", j.Weights["terrible"], cfg.MinWeight)
	}
}

func TestDetectRegime_Novel(t *testing.T) {
	cohorts := []string{"short", "long"}
	cfg := DefaultConfig()
	cfg.RegimeThreshold = 0.1
	j := New(cohorts, cfg)

	// Short cohort much higher
	j.Weights = map[string]float64{"short": 0.7, "long": 0.3}

	regime := j.DetectRegime()
	if regime != RegimeNovel {
		t.Errorf("expected NOVEL_REGIME, got %s", regime)
	}
}

func TestDetectRegime_Historical(t *testing.T) {
	cohorts := []string{"short", "long"}
	cfg := DefaultConfig()
	cfg.RegimeThreshold = 0.1
	j := New(cohorts, cfg)

	// Long cohort much higher
	j.Weights = map[string]float64{"short": 0.3, "long": 0.7}

	regime := j.DetectRegime()
	if regime != RegimeHistorical {
		t.Errorf("expected HISTORICAL_REGIME, got %s", regime)
	}
}

func TestDetectRegime_Mixed(t *testing.T) {
	cohorts := []string{"short", "long"}
	j := New(cohorts, DefaultConfig())
	// Equal weights → mixed
	regime := j.DetectRegime()
	if regime != RegimeMixed {
		t.Errorf("expected MIXED, got %s", regime)
	}
}

func TestBlendRecommendations_Consensus(t *testing.T) {
	cohorts := []string{"short", "long"}
	j := New(cohorts, DefaultConfig())
	j.Weights = map[string]float64{"short": 0.6, "long": 0.4}

	recs := []CohortRecommendation{
		{CohortName: "short", Ticker: "BTC", Direction: types.SignalLong, Conviction: 80},
		{CohortName: "long", Ticker: "BTC", Direction: types.SignalLong, Conviction: 70},
	}

	blended := j.BlendRecommendations(recs)
	if len(blended) != 1 {
		t.Fatalf("expected 1 blended rec, got %d", len(blended))
	}
	if blended[0].Ticker != "BTC" {
		t.Error("expected BTC")
	}
	if blended[0].Direction != types.SignalLong {
		t.Error("expected LONG")
	}
	if blended[0].Contested {
		t.Error("should not be contested (both LONG)")
	}
	// Expected conviction: 80*0.6 + 70*0.4 = 48+28 = 76
	if blended[0].Conviction < 75 || blended[0].Conviction > 77 {
		t.Errorf("expected conviction ~76, got %f", blended[0].Conviction)
	}
}

func TestBlendRecommendations_Contested(t *testing.T) {
	cohorts := []string{"short", "long"}
	j := New(cohorts, DefaultConfig())
	j.Weights = map[string]float64{"short": 0.5, "long": 0.5}

	recs := []CohortRecommendation{
		{CohortName: "short", Ticker: "ETH", Direction: types.SignalLong, Conviction: 80},
		{CohortName: "long", Ticker: "ETH", Direction: types.SignalShort, Conviction: 60},
	}

	blended := j.BlendRecommendations(recs)
	if len(blended) != 1 {
		t.Fatalf("expected 1, got %d", len(blended))
	}
	if !blended[0].Contested {
		t.Error("should be contested (LONG vs SHORT)")
	}
	// LONG wins: 80*0.5=40, SHORT: 60*0.5=30
	// Final: 40 - 30*0.5 = 25
	if blended[0].Direction != types.SignalLong {
		t.Errorf("expected LONG to win, got %s", blended[0].Direction)
	}
	t.Logf("Contested conviction: %f", blended[0].Conviction)
}

func TestRun_FullCycle(t *testing.T) {
	cohorts := []string{"18month", "10year"}
	j := New(cohorts, DefaultConfig())

	metrics := map[string]Cohort{
		"18month": {HitRate: 0.7, Sharpe: 1.0},
		"10year":  {HitRate: 0.5, Sharpe: 0.3},
	}

	recs := []CohortRecommendation{
		{CohortName: "18month", Ticker: "NVDA", Direction: types.SignalLong, Conviction: 90},
		{CohortName: "10year", Ticker: "NVDA", Direction: types.SignalLong, Conviction: 70},
		{CohortName: "18month", Ticker: "XOM", Direction: types.SignalShort, Conviction: 60},
	}

	output := j.Run(metrics, recs)

	if len(output.Blended) < 1 {
		t.Fatal("expected at least 1 blended rec")
	}
	if output.CohortWeights["18month"] <= output.CohortWeights["10year"] {
		t.Error("18month should outweigh 10year based on metrics")
	}
	t.Logf("Regime: %s, Weights: %v, Contested: %d",
		output.Regime, output.CohortWeights, output.ContestedCount)
}

func TestSortBlended(t *testing.T) {
	recs := []BlendedRecommendation{
		{Ticker: "A", Conviction: 30},
		{Ticker: "B", Conviction: 90},
		{Ticker: "C", Conviction: 60},
	}
	sortBlended(recs)
	if recs[0].Ticker != "B" || recs[1].Ticker != "C" || recs[2].Ticker != "A" {
		t.Errorf("not sorted by conviction desc: %v", recs)
	}
}
