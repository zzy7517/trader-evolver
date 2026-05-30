package backtest

import (
	"context"
	"fmt"
	"testing"

	"trader-evolver/internal/llm"
	"trader-evolver/internal/types"
)

func TestFindWorstAgent(t *testing.T) {
	cfg := DefaultAutoresearchConfig()
	mock := llm.NewMockProvider()
	state := NewAutoresearchState(cfg, mock)

	// Create recommendations where fundamental_analyst consistently loses
	var recs []types.Recommendation
	for i := 0; i < 10; i++ {
		goodRet := 0.03
		badRet := -0.02

		recs = append(recs, types.Recommendation{
			ModuleID:   "ict_trader",
			Signal:     types.SignalLong,
			Conviction: 80,
			Return5d:   &goodRet,
		})
		recs = append(recs, types.Recommendation{
			ModuleID:   "fundamental_analyst",
			Signal:     types.SignalLong,
			Conviction: 70,
			Return5d:   &badRet,
		})
		recs = append(recs, types.Recommendation{
			ModuleID:   "wave_analyst",
			Signal:     types.SignalLong,
			Conviction: 60,
			Return5d:   &goodRet,
		})
	}

	worstID, worstSharpe := state.FindWorstAgent(10, recs)
	if worstID != "fundamental_analyst" {
		t.Errorf("expected worst=fundamental_analyst, got %s", worstID)
	}
	if worstSharpe >= 0 {
		t.Errorf("expected negative Sharpe for worst agent, got %f", worstSharpe)
	}
	t.Logf("Worst: %s (Sharpe: %.3f)", worstID, worstSharpe)
}

func TestFindWorstAgent_CooldownRespected(t *testing.T) {
	cfg := DefaultAutoresearchConfig()
	cfg.CooldownDays = 10
	mock := llm.NewMockProvider()
	state := NewAutoresearchState(cfg, mock)

	// Mark fundamental_analyst as recently modified
	state.LastModifiedDay["fundamental_analyst"] = 5

	var recs []types.Recommendation
	badRet := -0.05
	for i := 0; i < 5; i++ {
		recs = append(recs, types.Recommendation{
			ModuleID:   "fundamental_analyst",
			Signal:     types.SignalLong,
			Conviction: 70,
			Return5d:   &badRet,
		})
	}

	// At day 10, still within cooldown (10 - 5 = 5 < 10)
	worstID, _ := state.FindWorstAgent(10, recs)
	if worstID == "fundamental_analyst" {
		t.Error("fundamental_analyst should be in cooldown")
	}
}

func TestShouldTrigger(t *testing.T) {
	cfg := DefaultAutoresearchConfig()
	cfg.EvalWindowDays = 5
	mock := llm.NewMockProvider()
	state := NewAutoresearchState(cfg, mock)

	tests := []struct {
		day    int
		expect bool
	}{
		{0, false},
		{1, false},
		{5, true},
		{10, true},
		{7, false},
	}

	for _, tt := range tests {
		got := state.ShouldTrigger(tt.day)
		if got != tt.expect {
			t.Errorf("ShouldTrigger(%d) = %v, want %v", tt.day, got, tt.expect)
		}
	}
}

func TestShouldTrigger_MaxModifications(t *testing.T) {
	cfg := DefaultAutoresearchConfig()
	cfg.MaxModifications = 2
	mock := llm.NewMockProvider()
	state := NewAutoresearchState(cfg, mock)

	// Fill up modifications
	state.Modifications = []PromptModification{{}, {}}

	if state.ShouldTrigger(5) {
		t.Error("should not trigger when at max modifications")
	}
}

func TestGenerateModification(t *testing.T) {
	cfg := DefaultAutoresearchConfig()
	mock := llm.NewMockProvider()
	state := NewAutoresearchState(cfg, mock)

	var recs []types.Recommendation
	badRet := -0.02
	for i := 0; i < 5; i++ {
		recs = append(recs, types.Recommendation{
			ModuleID:      "fundamental_analyst",
			Signal:        types.SignalLong,
			Conviction:    70,
			InstrumentKey: "btc:usdt",
			RecommendedAt: fmt.Sprintf("2024-01-%02d", i+1),
			Return5d:      &badRet,
		})
	}

	ctx := context.Background()
	desc, err := state.GenerateModification(ctx, "fundamental_analyst", recs)
	if err != nil {
		t.Fatalf("GenerateModification error: %v", err)
	}
	if desc == "" {
		t.Error("expected non-empty modification description")
	}
	t.Logf("Generated modification: %s", desc)
}

func TestEvaluateModification_Keep(t *testing.T) {
	cfg := DefaultAutoresearchConfig()
	mock := llm.NewMockProvider()
	state := NewAutoresearchState(cfg, mock)

	// Sharpe improved: 0.5 > 0.2
	mod := state.EvaluateModification(10, "2024-01-10", "fundamental_analyst", 0.2, 0.5)
	if !mod.Kept {
		t.Error("expected modification to be kept (Sharpe improved)")
	}

	total, kept, reverted := state.Stats()
	if total != 1 || kept != 1 || reverted != 0 {
		t.Errorf("stats: total=%d kept=%d reverted=%d", total, kept, reverted)
	}
}

func TestEvaluateModification_Revert(t *testing.T) {
	cfg := DefaultAutoresearchConfig()
	mock := llm.NewMockProvider()
	state := NewAutoresearchState(cfg, mock)

	// Sharpe got worse: -0.1 < 0.2
	mod := state.EvaluateModification(10, "2024-01-10", "wave_analyst", 0.2, -0.1)
	if mod.Kept {
		t.Error("expected modification to be reverted (Sharpe worsened)")
	}

	total, kept, reverted := state.Stats()
	if total != 1 || kept != 0 || reverted != 1 {
		t.Errorf("stats: total=%d kept=%d reverted=%d", total, kept, reverted)
	}
}

func TestCalcSharpeForModule(t *testing.T) {
	var recs []types.Recommendation
	goodRet := 0.05

	for i := 0; i < 10; i++ {
		recs = append(recs, types.Recommendation{
			ModuleID:   "ict_trader",
			Signal:     types.SignalLong,
			Conviction: 80,
			Return5d:   &goodRet,
		})
	}

	sharpe := calcSharpeForModule("ict_trader", recs)
	// Constant positive returns → very high Sharpe (or undefined if std=0)
	// With identical returns, std should be 0, which returns 0
	// Let's vary returns slightly
	recs = nil
	for i := 0; i < 10; i++ {
		r := 0.03 + float64(i)*0.005
		recs = append(recs, types.Recommendation{
			ModuleID:   "ict_trader",
			Signal:     types.SignalLong,
			Conviction: 80,
			Return5d:   &r,
		})
	}

	sharpe = calcSharpeForModule("ict_trader", recs)
	if sharpe <= 0 {
		t.Errorf("expected positive Sharpe for consistently positive returns, got %f", sharpe)
	}
	t.Logf("Sharpe for ict_trader: %.3f", sharpe)
}
