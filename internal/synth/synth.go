// Package synth ports tradex/pipeline/synthesizer.ts.
// Pure logic: Darwin-weighted voting across module outputs, no LLM call.
package synth

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"trader-evolver/internal/types"
)

// Synthesize aggregates module results into a consensus signal.
func Synthesize(in types.SynthesisInput) types.SynthesisOutput {
	valid := make([]types.ModuleRunResult, 0, len(in.ModuleResults))
	for _, r := range in.ModuleResults {
		if r.Error == nil {
			valid = append(valid, r)
		}
	}

	if len(valid) == 0 {
		return neutralOutput(len(in.ModuleResults))
	}

	// Weighted voting.
	votes := map[types.SignalDirection]float64{
		types.SignalLong:    0,
		types.SignalShort:   0,
		types.SignalNeutral: 0,
	}
	for _, r := range valid {
		votes[r.Output.Signal] += r.DarwinWeight
	}

	aggregated := dominantSignal(votes)

	// Modules agreeing with dominant signal.
	agreeing := make([]types.ModuleRunResult, 0, len(valid))
	for _, r := range valid {
		if r.Output.Signal == aggregated {
			agreeing = append(agreeing, r)
		}
	}
	modulesAgreeing := len(agreeing)

	// Weighted conviction normalized by agreeing weight.
	agreeingWeight := 0.0
	weightedConviction := 0.0
	for _, r := range agreeing {
		agreeingWeight += r.DarwinWeight
		weightedConviction += r.Output.Conviction * r.DarwinWeight
	}
	normalizedConviction := 0.0
	if agreeingWeight > 0 {
		normalizedConviction = weightedConviction / agreeingWeight
	}

	regimeModifier := regimeConvictionModifier(in.Regime.Volatility)
	finalConviction := math.Round(normalizedConviction * regimeModifier)

	entries := collectLevels(agreeing, func(o types.ModuleOutput) *float64 { return o.Entry })
	sls := collectLevels(agreeing, func(o types.ModuleOutput) *float64 { return o.StopLoss })
	tps := collectLevels(agreeing, func(o types.ModuleOutput) *float64 { return o.TakeProfit })

	return types.SynthesisOutput{
		AggregatedSignal:   aggregated,
		WeightedConviction: finalConviction,
		ModulesAgreeing:    modulesAgreeing,
		ModulesTotal:       len(valid),
		ConsensusEntry:     median(entries),
		ConsensusSL:        median(sls),
		ConsensusTP:        median(tps),
		Reasoning:          buildReasoning(valid, aggregated, modulesAgreeing),
	}
}

func dominantSignal(votes map[types.SignalDirection]float64) types.SignalDirection {
	l, s, n := votes[types.SignalLong], votes[types.SignalShort], votes[types.SignalNeutral]
	if l > s && l > n {
		return types.SignalLong
	}
	if s > l && s > n {
		return types.SignalShort
	}
	return types.SignalNeutral
}

func regimeConvictionModifier(vol types.VolatilityRegime) float64 {
	switch vol {
	case types.VolExtreme:
		return 0.6
	case types.VolHigh:
		return 0.8
	case types.VolMedium:
		return 1.0
	case types.VolLow:
		return 1.0
	default:
		return 1.0
	}
}

func collectLevels(rs []types.ModuleRunResult, pick func(types.ModuleOutput) *float64) []float64 {
	out := make([]float64, 0, len(rs))
	for _, r := range rs {
		if v := pick(r.Output); v != nil {
			out = append(out, *v)
		}
	}
	return out
}

func median(values []float64) *float64 {
	if len(values) == 0 {
		return nil
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	mid := len(sorted) / 2
	var m float64
	if len(sorted)%2 != 0 {
		m = sorted[mid]
	} else {
		m = (sorted[mid-1] + sorted[mid]) / 2
	}
	return &m
}

func buildReasoning(results []types.ModuleRunResult, signal types.SignalDirection, agreeing int) string {
	parts := make([]string, 0)
	for _, r := range results {
		if r.Output.Signal != signal {
			continue
		}
		reason := r.Output.Reasoning
		if rs := []rune(reason); len(rs) > 80 {
			reason = string(rs[:80])
		}
		parts = append(parts, fmt.Sprintf("%s(w=%.1f): %s", r.ModuleID, r.DarwinWeight, reason))
	}
	return fmt.Sprintf("%d/%d模块共振%s。%s", agreeing, len(results), signal, strings.Join(parts, "; "))
}

func neutralOutput(total int) types.SynthesisOutput {
	return types.SynthesisOutput{
		AggregatedSignal:   types.SignalNeutral,
		WeightedConviction: 0,
		ModulesAgreeing:    0,
		ModulesTotal:       total,
		ConsensusEntry:     nil,
		ConsensusSL:        nil,
		ConsensusTP:        nil,
		Reasoning:          "No valid module outputs",
	}
}
