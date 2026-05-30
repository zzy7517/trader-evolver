package modules

import (
	"context"
	"errors"
	"testing"

	"trader-evolver/internal/llm"
	"trader-evolver/internal/types"
)

func croWith(content string, callErr error) *llm.MockProvider {
	return &llm.MockProvider{CallFn: func(_ context.Context, _, _ string) (string, int, error) {
		if callErr != nil {
			return "", 0, callErr
		}
		return content, 5, nil
	}}
}

func croReview(t *testing.T, p llm.Provider) types.CROOutput {
	t.Helper()
	pc := NewPromptComposer("")
	a := NewAdversarialReviewer(pc, p)
	fr := 0.0005
	return a.Review(context.Background(), types.CROInput{
		Synthesis:     types.SynthesisOutput{AggregatedSignal: types.SignalLong, WeightedConviction: 70},
		Regime:        types.RegimeSignal{Market: types.RiskOn, Volatility: types.VolMedium, Trend: types.TrendUp},
		InstrumentKey: "X", CurrentPrice: 100, FundingRate: &fr,
	})
}

func TestCROParsesApproval(t *testing.T) {
	out := croReview(t, croWith(`{"approved":true,"objections":["minor"],"reflexivity_flags":[],
		"risk_level":"low","adjusted_conviction":65,"reasoning":"ok"}`, nil))
	if !out.Approved {
		t.Error("expected approved")
	}
	if out.RiskLevel != "LOW" {
		t.Errorf("risk level got %s", out.RiskLevel)
	}
	if out.AdjustedConviction != 65 {
		t.Errorf("adj conviction got %v", out.AdjustedConviction)
	}
	if len(out.Objections) != 1 || out.Objections[0] != "minor" {
		t.Errorf("objections got %+v", out.Objections)
	}
}

func TestCROParseFailureFailsSafe(t *testing.T) {
	out := croReview(t, croWith("garbage not json", nil))
	if out.Approved {
		t.Error("parse failure must fail safe to reject")
	}
	if out.RiskLevel != "HIGH" || out.Reasoning != "Parse error" {
		t.Errorf("expected HIGH/Parse error, got %s/%s", out.RiskLevel, out.Reasoning)
	}
}

func TestCROLLMErrorFailsSafe(t *testing.T) {
	out := croReview(t, croWith("", errors.New("timeout")))
	if out.Approved {
		t.Error("LLM error must fail safe to reject")
	}
	if out.RiskLevel != "HIGH" || out.Reasoning != "CRO module error" {
		t.Errorf("got %s/%s", out.RiskLevel, out.Reasoning)
	}
}

func TestCROClampsAndDefaults(t *testing.T) {
	out := croReview(t, croWith(`{"approved":1,"risk_level":"catastrophic","adjusted_conviction":300}`, nil))
	if !out.Approved {
		t.Error("approved:1 should be truthy")
	}
	if out.RiskLevel != "MEDIUM" {
		t.Errorf("invalid risk level should default MEDIUM, got %s", out.RiskLevel)
	}
	if out.AdjustedConviction != 100 {
		t.Errorf("conviction should clamp 100, got %v", out.AdjustedConviction)
	}
	if out.Objections == nil || out.ReflexivityFlags == nil {
		t.Error("slices should be non-nil")
	}
}

func TestFormatThousands(t *testing.T) {
	cases := map[float64]string{0: "0", 999: "999", 1000: "1,000", 1234567: "1,234,567", -1234: "-1,234"}
	for in, want := range cases {
		if got := formatThousands(in); got != want {
			t.Errorf("formatThousands(%v)=%q want %q", in, got, want)
		}
	}
}
