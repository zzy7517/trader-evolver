package modules

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"trader-evolver/internal/llm"
	"trader-evolver/internal/types"
)

// AdversarialReviewer is the CRO gate: a risk-officer LLM challenge before
// execution. Ported from tradex/pipeline/adversarial.ts. Fails safe to reject.
type AdversarialReviewer struct {
	composer *PromptComposer
	provider llm.Provider
}

func NewAdversarialReviewer(composer *PromptComposer, provider llm.Provider) *AdversarialReviewer {
	return &AdversarialReviewer{composer: composer, provider: provider}
}

// Review challenges a synthesized trade candidate. On any LLM/parse failure it
// returns a fail-safe rejection (approved=false, riskLevel=HIGH).
func (a *AdversarialReviewer) Review(ctx context.Context, in types.CROInput) types.CROOutput {
	candidate := map[string]any{
		"signal":           in.Synthesis.AggregatedSignal,
		"conviction":       in.Synthesis.WeightedConviction,
		"entry":            in.Synthesis.ConsensusEntry,
		"stop_loss":        in.Synthesis.ConsensusSL,
		"take_profit":      in.Synthesis.ConsensusTP,
		"modules_agreeing": in.Synthesis.ModulesAgreeing,
		"modules_total":    in.Synthesis.ModulesTotal,
		"reasoning":        in.Synthesis.Reasoning,
	}
	candidateBytes, _ := json.MarshalIndent(candidate, "", "  ")

	lines := []string{
		"Instrument: " + in.InstrumentKey,
		fmt.Sprintf("Current Price: %v", in.CurrentPrice),
		fmt.Sprintf("Regime: %s / Vol:%s / Trend:%s", in.Regime.Market, in.Regime.Volatility, in.Regime.Trend),
	}
	if in.FundingRate != nil {
		lines = append(lines, fmt.Sprintf("Funding Rate: %.4f%%", *in.FundingRate*100))
	}
	if in.LongShortRatio != nil {
		lines = append(lines, fmt.Sprintf("Long/Short Ratio: %.2f", *in.LongShortRatio))
	}
	if in.OIDelta != nil {
		lines = append(lines, fmt.Sprintf("OI Delta 1h: $%s", formatThousands(math.Round(*in.OIDelta))))
	}

	composed := a.composer.ComposeCRO(string(candidateBytes), strings.Join(lines, "\n"))

	content, _, err := a.provider.Call(ctx, composed.SystemPrompt, composed.UserPrompt)
	if err != nil {
		// CRO failed → fail safe to reject.
		return types.CROOutput{
			Approved:           false,
			Objections:         []string{"CRO review failed — defaulting to reject"},
			ReflexivityFlags:   []string{},
			RiskLevel:          "HIGH",
			AdjustedConviction: 0,
			Reasoning:          "CRO module error",
		}
	}
	return parseCROOutput(content)
}

func parseCROOutput(raw string) types.CROOutput {
	jsonStr := strings.TrimSpace(raw)
	if strings.HasPrefix(jsonStr, "```") {
		if m := fenceRe.FindStringSubmatch(jsonStr); m != nil {
			jsonStr = strings.TrimSpace(m[1])
		}
	}

	var parsed map[string]any
	if err := json.Unmarshal([]byte(jsonStr), &parsed); err != nil {
		return types.CROOutput{
			Approved:           false,
			Objections:         []string{"Failed to parse CRO output"},
			ReflexivityFlags:   []string{},
			RiskLevel:          "HIGH",
			AdjustedConviction: 0,
			Reasoning:          "Parse error",
		}
	}

	convRaw, ok := toNumber(parsed["adjusted_conviction"])
	if !ok {
		convRaw = 0
	}

	return types.CROOutput{
		Approved:           toBool(parsed["approved"]),
		Objections:         toStringSlice(parsed["objections"]),
		ReflexivityFlags:   toStringSlice(parsed["reflexivity_flags"]),
		RiskLevel:          validateRiskLevel(parsed["risk_level"]),
		AdjustedConviction: math.Max(0, math.Min(100, convRaw)),
		Reasoning:          truncate(toStr(parsed["reasoning"]), 500),
	}
}

func validateRiskLevel(raw any) string {
	s := strings.ToUpper(toStr(raw))
	switch s {
	case "LOW", "MEDIUM", "HIGH", "EXTREME":
		return s
	default:
		return "MEDIUM"
	}
}

// toBool mirrors JS Boolean(): truthy values become true.
func toBool(raw any) bool {
	switch v := raw.(type) {
	case bool:
		return v
	case nil:
		return false
	case float64:
		return v != 0
	case string:
		return v != ""
	default:
		return true // non-empty objects/arrays are truthy
	}
}

func toStringSlice(raw any) []string {
	arr, ok := raw.([]any)
	if !ok {
		return []string{}
	}
	out := make([]string, 0, len(arr))
	for _, e := range arr {
		out = append(out, toStr(e))
	}
	return out
}

// formatThousands renders an integer-valued float with comma grouping,
// approximating JS Number.toLocaleString() for whole numbers.
func formatThousands(v float64) string {
	neg := v < 0
	if neg {
		v = -v
	}
	s := fmt.Sprintf("%.0f", v)
	n := len(s)
	if n <= 3 {
		if neg {
			return "-" + s
		}
		return s
	}
	var b strings.Builder
	pre := n % 3
	if pre > 0 {
		b.WriteString(s[:pre])
		if n > pre {
			b.WriteByte(',')
		}
	}
	for i := pre; i < n; i += 3 {
		b.WriteString(s[i : i+3])
		if i+3 < n {
			b.WriteByte(',')
		}
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}
