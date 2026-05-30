package modules

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"trader-evolver/internal/llm"
	"trader-evolver/internal/types"
)

// ModuleRunner executes a single analysis module via an LLM provider.
//
// Faithful port of tradex/pipeline/module_runner.ts:
//   - compose system+user prompt via PromptComposer
//   - single provider.Call (JSON-only output expected)
//   - parse module output, tolerating ```json fences
//   - on ANY error (call or parse), return a neutral output with Error set
//     (graceful degradation — never panics, never crashes the pipeline)
type ModuleRunner struct {
	composer *PromptComposer
	provider llm.Provider
}

// NewModuleRunner wires a composer and an LLM provider.
func NewModuleRunner(composer *PromptComposer, provider llm.Provider) *ModuleRunner {
	return &ModuleRunner{composer: composer, provider: provider}
}

// Run executes one module and always returns a ModuleRunResult (never an error
// at the Go level — failures are captured in result.Error with a neutral output).
func (r *ModuleRunner) Run(
	ctx context.Context,
	moduleID, instrumentKey string,
	regime types.RegimeSignal,
	candleData string,
	darwinWeight float64,
	additionalContext string,
) types.ModuleRunResult {
	start := time.Now()

	prompt := r.composer.Compose(ModulePromptInput{
		ModuleID:          moduleID,
		InstrumentKey:     instrumentKey,
		Regime:            regime,
		CandleData:        candleData,
		AdditionalContext: additionalContext,
	})

	content, tokens, err := r.provider.Call(ctx, prompt.SystemPrompt, prompt.UserPrompt)
	if err != nil {
		return r.failResult(moduleID, darwinWeight, start, fmt.Sprintf("llm call failed: %v", err))
	}

	output, perr := parseModuleOutput(moduleID, content)
	if perr != nil {
		return r.failResult(moduleID, darwinWeight, start, perr.Error())
	}

	return types.ModuleRunResult{
		ModuleID:     moduleID,
		DarwinWeight: darwinWeight,
		Output:       output,
		TokensUsed:   tokens,
		DurationMs:   time.Since(start).Milliseconds(),
		Error:        nil,
	}
}

func (r *ModuleRunner) failResult(moduleID string, darwinWeight float64, start time.Time, msg string) types.ModuleRunResult {
	return types.ModuleRunResult{
		ModuleID:     moduleID,
		DarwinWeight: darwinWeight,
		Output:       neutralOutput(moduleID),
		TokensUsed:   0,
		DurationMs:   time.Since(start).Milliseconds(),
		Error:        &msg,
	}
}

var fenceRe = regexp.MustCompile("(?s)```(?:json)?\\s*(.*?)```")

// rawModuleOutput is the loose JSON shape the LLM emits. snake_case and
// camelCase variants are both accepted (matching the TS parser).
type rawModuleOutput struct {
	Signal     any `json:"signal"`
	Conviction any `json:"conviction"`
	Entry      any `json:"entry"`
	StopLoss   any `json:"stop_loss"`
	StopLossC  any `json:"stopLoss"`
	TakeProfit any `json:"take_profit"`
	TakeProfC  any `json:"takeProfit"`
	KeyLevels  *struct {
		Support    []float64 `json:"support"`
		Resistance []float64 `json:"resistance"`
	} `json:"key_levels"`
	Reasoning any `json:"reasoning"`
}

// parseModuleOutput mirrors ModuleRunner.parseOutput in the TS implementation.
func parseModuleOutput(moduleID, raw string) (types.ModuleOutput, error) {
	jsonStr := strings.TrimSpace(raw)
	if strings.HasPrefix(jsonStr, "```") {
		if m := fenceRe.FindStringSubmatch(jsonStr); m != nil {
			jsonStr = strings.TrimSpace(m[1])
		}
	}

	var parsed rawModuleOutput
	if err := json.Unmarshal([]byte(jsonStr), &parsed); err != nil {
		return types.ModuleOutput{}, fmt.Errorf("failed to parse module output: %v", err)
	}

	stopLoss := parsed.StopLoss
	if stopLoss == nil {
		stopLoss = parsed.StopLossC
	}
	takeProfit := parsed.TakeProfit
	if takeProfit == nil {
		takeProfit = parsed.TakeProfC
	}

	out := types.ModuleOutput{
		ModuleID:   moduleID,
		Signal:     validateSignal(parsed.Signal),
		Conviction: clampConviction(parsed.Conviction),
		Entry:      optionalNumber(parsed.Entry),
		StopLoss:   optionalNumber(stopLoss),
		TakeProfit: optionalNumber(takeProfit),
		KeyLevels:  types.KeyLevels{Support: []float64{}, Resistance: []float64{}},
		Reasoning:  truncate(toStr(parsed.Reasoning), 500),
	}
	if parsed.KeyLevels != nil {
		if parsed.KeyLevels.Support != nil {
			out.KeyLevels.Support = parsed.KeyLevels.Support
		}
		if parsed.KeyLevels.Resistance != nil {
			out.KeyLevels.Resistance = parsed.KeyLevels.Resistance
		}
	}
	return out, nil
}

func neutralOutput(moduleID string) types.ModuleOutput {
	return types.ModuleOutput{
		ModuleID:   moduleID,
		Signal:     types.SignalNeutral,
		Conviction: 0,
		Entry:      nil,
		StopLoss:   nil,
		TakeProfit: nil,
		KeyLevels:  types.KeyLevels{Support: []float64{}, Resistance: []float64{}},
		Reasoning:  "Module failed to produce output",
	}
}

func validateSignal(raw any) types.SignalDirection {
	s := strings.ToUpper(strings.TrimSpace(toStr(raw)))
	switch s {
	case "LONG":
		return types.SignalLong
	case "SHORT":
		return types.SignalShort
	default:
		return types.SignalNeutral
	}
}

// clampConviction parses any → number, clamps to [0,100]; non-numeric → 0.
func clampConviction(raw any) float64 {
	v, ok := toNumber(raw)
	if !ok {
		return 0
	}
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

// optionalNumber mirrors the TS optionalNumber: null/undefined/"" → nil,
// otherwise a finite number or nil.
func optionalNumber(raw any) *float64 {
	if raw == nil {
		return nil
	}
	if s, ok := raw.(string); ok && s == "" {
		return nil
	}
	v, ok := toNumber(raw)
	if !ok {
		return nil
	}
	return &v
}

func toNumber(raw any) (float64, bool) {
	switch n := raw.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	case string:
		s := strings.TrimSpace(n)
		if s == "" {
			return 0, false
		}
		f, err := strconv.ParseFloat(s, 64)
		return f, err == nil
	default:
		return 0, false
	}
}

func toStr(raw any) string {
	if raw == nil {
		return ""
	}
	if s, ok := raw.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", raw)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
