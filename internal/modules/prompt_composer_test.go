package modules

import (
	"strings"
	"testing"

	"trader-evolver/internal/types"
)

func TestComposeFindsPromptsAndAssembles(t *testing.T) {
	// Auto-discovery should walk up to the repo-root prompts/ dir.
	pc := NewPromptComposer("")

	in := ModulePromptInput{
		ModuleID:      "ict_trader",
		InstrumentKey: "USDT-FUTURES:BTCUSDT",
		Regime: types.RegimeSignal{
			Market:     types.RiskOn,
			Volatility: types.VolMedium,
			Trend:      types.TrendUp,
		},
		CandleData: "2020-01-01 O:1 H:2 L:0 C:1 V:5",
	}
	out := pc.Compose(in)

	if strings.Contains(out.SystemPrompt, "Prompt file not found") {
		t.Fatalf("persona/regime/exec prompts not loaded:\n%s", out.SystemPrompt)
	}
	// System prompt must contain the three joined sections (two --- separators).
	if strings.Count(out.SystemPrompt, "---") < 2 {
		t.Errorf("expected 2 separators in system prompt")
	}
	// User prompt structure.
	for _, want := range []string{"## 分析目标: USDT-FUTURES:BTCUSDT", "- Market: RISK_ON", "## K线数据", "只输出JSON"} {
		if !strings.Contains(out.UserPrompt, want) {
			t.Errorf("user prompt missing %q", want)
		}
	}
}

func TestRegimeToFile(t *testing.T) {
	cases := []struct {
		r    types.RegimeSignal
		want string
	}{
		{types.RegimeSignal{Volatility: types.VolExtreme, Trend: types.TrendUp}, "volatile"},
		{types.RegimeSignal{Volatility: types.VolHigh, Trend: types.TrendRange}, "volatile"},
		{types.RegimeSignal{Volatility: types.VolMedium, Trend: types.TrendRange}, "ranging"},
		{types.RegimeSignal{Volatility: types.VolLow, Trend: types.TrendUp}, "trending"},
	}
	for _, c := range cases {
		if got := regimeToFile(c.r); got != c.want {
			t.Errorf("regimeToFile(%+v)=%s want %s", c.r, got, c.want)
		}
	}
}

func TestComposeCROAndAdditionalContext(t *testing.T) {
	pc := NewPromptComposer("")
	cro := pc.ComposeCRO("OPEN_LONG BTC", "vix=20")
	if !strings.Contains(cro.UserPrompt, "候选交易决策") || !strings.Contains(cro.UserPrompt, "OPEN_LONG BTC") {
		t.Errorf("CRO user prompt malformed: %s", cro.UserPrompt)
	}

	in := ModulePromptInput{
		ModuleID: "fundamental_analyst", InstrumentKey: "X",
		Regime:            types.RegimeSignal{Market: types.Neutral, Volatility: types.VolMedium, Trend: types.TrendUp},
		CandleData:        "data",
		AdditionalContext: "Funding Rate: 0.01%",
	}
	out := pc.Compose(in)
	if !strings.Contains(out.UserPrompt, "## 附加数据") || !strings.Contains(out.UserPrompt, "Funding Rate: 0.01%") {
		t.Errorf("additional context not included: %s", out.UserPrompt)
	}
}
