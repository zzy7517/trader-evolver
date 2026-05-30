package orchestrator

import (
	"context"
	"testing"

	"trader-evolver/internal/llm"
	"trader-evolver/internal/modules"
	"trader-evolver/internal/types"
)

func f(v float64) *float64 { return &v }

// baseDeps returns Deps wired with simple fakes for an end-to-end mock run.
func baseDeps(onComplete func(types.PipelineRun)) Deps {
	price := 100.0
	return Deps{
		ResolveRegimeIndicators: func(string) types.RegimeIndicators {
			// Backtest-style: only price-derived indicators present; micro = nil.
			return types.RegimeIndicators{VIX: f(18), ADX: f(30), FearGreed: f(60)}
		},
		GetCandleData:         func(string) string { return "2020-01-01 O:1 H:2 L:0 C:1 V:5" },
		GetCurrentPrice:       func(string) *float64 { return &price },
		GetDarwinWeights:      func() []types.DarwinWeightEntry { return nil },
		GetFundamentalContext: func(string) string { return "Funding Rate: 0.01%" },
		GetFundingRate:        func(string) *float64 { return nil },
		GetLongShortRatio:     func(string) *float64 { return nil },
		GetOIDelta:            func(string) *float64 { return nil },
		OnComplete:            onComplete,
	}
}

func TestOrchestratorEndToEndMock(t *testing.T) {
	var completed *types.PipelineRun
	deps := baseDeps(func(r types.PipelineRun) { completed = &r })

	o := New(deps, modules.NewPromptComposer(""), llm.NewMockProvider())
	run, err := o.Run(context.Background(), "USDT-FUTURES:BTCUSDT", types.TriggerManual)
	if err != nil {
		t.Fatalf("run error: %v", err)
	}
	if run.Status != types.StatusCompleted {
		t.Fatalf("status = %s", run.Status)
	}
	if len(run.ModuleResults) != 5 {
		t.Fatalf("expected 5 module results, got %d", len(run.ModuleResults))
	}
	if run.Decision == nil {
		t.Fatal("decision should not be nil")
	}
	if run.ID == "" || run.CompletedAt == nil {
		t.Fatal("run id/completedAt should be set")
	}
	if completed == nil || completed.ID != run.ID {
		t.Fatal("OnComplete should fire with the same run")
	}
	// Mock modules never error, so all 5 should have no Error.
	for _, r := range run.ModuleResults {
		if r.Error != nil {
			t.Errorf("module %s unexpectedly errored: %v", r.ModuleID, *r.Error)
		}
	}
	// Regime should be exposed.
	if o.CurrentRegime == nil || o.LastRun == nil {
		t.Fatal("CurrentRegime/LastRun should be set")
	}
}

func TestOrchestratorConcurrentGuard(t *testing.T) {
	o := New(baseDeps(nil), modules.NewPromptComposer(""), llm.NewMockProvider())
	// Manually flip running and ensure Run rejects.
	o.running = true
	_, err := o.Run(context.Background(), "X", types.TriggerManual)
	if err == nil {
		t.Fatal("expected error when already running")
	}
}

func TestDecideShortCircuitsLowConsensus(t *testing.T) {
	o := New(baseDeps(nil), modules.NewPromptComposer(""), llm.NewMockProvider())
	// Only 2 modules agreeing → PASS without CRO.
	s := types.SynthesisOutput{
		AggregatedSignal: types.SignalLong, WeightedConviction: 80,
		ModulesAgreeing: 2, ModulesTotal: 5,
	}
	d := o.decide(context.Background(), "X", types.RegimeSignal{Volatility: types.VolMedium}, s, 100)
	if d.Action != types.ActionPass || d.SurvivedCRO {
		t.Fatalf("low consensus should PASS without CRO, got %+v", d)
	}
}

func TestCalcRRAndPositionSize(t *testing.T) {
	// entry 100, sl 95 (risk 5), tp 115 (reward 15) -> rr 3.0
	rr := calcRR(f(100), f(95), f(115))
	if rr == nil || *rr != 3.0 {
		t.Fatalf("rr got %v", rr)
	}
	if calcRR(nil, f(95), f(115)) != nil {
		t.Fatal("nil entry -> nil rr")
	}
	if calcPositionSize(4) != 2.0 || calcPositionSize(3) != 1.0 || calcPositionSize(2) != 0.5 {
		t.Fatal("position size tiers wrong")
	}
}
