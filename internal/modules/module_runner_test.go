package modules

import (
	"context"
	"errors"
	"testing"

	"trader-evolver/internal/types"
)

// stubProvider lets tests control the LLM response.
type stubProvider struct {
	content string
	tokens  int
	err     error
}

func (s stubProvider) Name() string { return "stub" }
func (s stubProvider) Call(ctx context.Context, sys, usr string) (string, int, error) {
	return s.content, s.tokens, s.err
}

func runWith(t *testing.T, p stubProvider) types.ModuleRunResult {
	t.Helper()
	pc := NewPromptComposer("")
	r := NewModuleRunner(pc, p)
	return r.Run(context.Background(), "ict_trader", "X",
		types.RegimeSignal{Market: types.Neutral, Volatility: types.VolMedium, Trend: types.TrendUp},
		"candles", 1.0, "")
}

func TestModuleRunnerParsesValidJSON(t *testing.T) {
	res := runWith(t, stubProvider{
		content: `{"signal":"long","conviction":75,"entry":100,"stop_loss":90,"take_profit":120,
		           "key_levels":{"support":[95,90],"resistance":[110]},"reasoning":"trend up"}`,
		tokens: 33,
	})
	if res.Error != nil {
		t.Fatalf("unexpected error: %v", *res.Error)
	}
	if res.Output.Signal != types.SignalLong {
		t.Errorf("signal got %s", res.Output.Signal)
	}
	if res.Output.Conviction != 75 {
		t.Errorf("conviction got %v", res.Output.Conviction)
	}
	if res.Output.Entry == nil || *res.Output.Entry != 100 {
		t.Errorf("entry got %v", res.Output.Entry)
	}
	if res.Output.StopLoss == nil || *res.Output.StopLoss != 90 {
		t.Errorf("stopLoss got %v", res.Output.StopLoss)
	}
	if len(res.Output.KeyLevels.Support) != 2 || res.Output.KeyLevels.Resistance[0] != 110 {
		t.Errorf("key levels got %+v", res.Output.KeyLevels)
	}
	if res.TokensUsed != 33 {
		t.Errorf("tokens got %d", res.TokensUsed)
	}
}

func TestModuleRunnerStripsCodeFence(t *testing.T) {
	res := runWith(t, stubProvider{
		content: "```json\n{\"signal\":\"SHORT\",\"conviction\":50}\n```",
	})
	if res.Error != nil {
		t.Fatalf("unexpected error: %v", *res.Error)
	}
	if res.Output.Signal != types.SignalShort || res.Output.Conviction != 50 {
		t.Errorf("got %s %v", res.Output.Signal, res.Output.Conviction)
	}
}

func TestModuleRunnerParseFailureFallsBackNeutral(t *testing.T) {
	res := runWith(t, stubProvider{content: "not json at all"})
	if res.Error == nil {
		t.Fatal("expected error set on parse failure")
	}
	if res.Output.Signal != types.SignalNeutral || res.Output.Conviction != 0 {
		t.Errorf("expected neutral fallback, got %s %v", res.Output.Signal, res.Output.Conviction)
	}
	if res.Output.Reasoning != "Module failed to produce output" {
		t.Errorf("reasoning got %q", res.Output.Reasoning)
	}
}

func TestModuleRunnerLLMErrorFallsBackNeutral(t *testing.T) {
	res := runWith(t, stubProvider{err: errors.New("network down")})
	if res.Error == nil || res.Output.Signal != types.SignalNeutral {
		t.Fatalf("expected neutral with error, got %+v", res)
	}
}

func TestModuleRunnerClampsAndDefaults(t *testing.T) {
	// conviction over 100 clamps; invalid signal -> NEUTRAL; missing optionals -> nil.
	res := runWith(t, stubProvider{content: `{"signal":"sideways","conviction":250}`})
	if res.Output.Signal != types.SignalNeutral {
		t.Errorf("invalid signal should be NEUTRAL, got %s", res.Output.Signal)
	}
	if res.Output.Conviction != 100 {
		t.Errorf("conviction should clamp to 100, got %v", res.Output.Conviction)
	}
	if res.Output.Entry != nil {
		t.Errorf("missing entry should be nil")
	}
	if res.Output.KeyLevels.Support == nil {
		t.Errorf("support should default to empty slice not nil")
	}
}
