package synth

import (
	"testing"

	"trader-evolver/internal/types"
)

func f(v float64) *float64 { return &v }

func mod(id string, w float64, sig types.SignalDirection, conv float64, entry *float64) types.ModuleRunResult {
	return types.ModuleRunResult{
		ModuleID:     id,
		DarwinWeight: w,
		Output: types.ModuleOutput{
			ModuleID: id, Signal: sig, Conviction: conv, Entry: entry,
		},
	}
}

func errMod(id string) types.ModuleRunResult {
	e := "boom"
	return types.ModuleRunResult{ModuleID: id, DarwinWeight: 1, Error: &e}
}

func TestSynthesizeAllErroredNeutral(t *testing.T) {
	out := Synthesize(types.SynthesisInput{
		ModuleResults: []types.ModuleRunResult{errMod("a"), errMod("b")},
		Regime:        types.RegimeSignal{Volatility: types.VolMedium},
	})
	if out.AggregatedSignal != types.SignalNeutral || out.ModulesTotal != 2 {
		t.Fatalf("expected neutral with total=2, got %+v", out)
	}
}

func TestSynthesizeWeightedVote(t *testing.T) {
	// LONG weight 2.0 (conv 80) vs SHORT weight 1.0 -> LONG wins.
	in := types.SynthesisInput{
		Regime: types.RegimeSignal{Volatility: types.VolMedium}, // modifier 1.0
		ModuleResults: []types.ModuleRunResult{
			mod("a", 2.0, types.SignalLong, 80, f(100)),
			mod("b", 1.0, types.SignalShort, 90, f(50)),
		},
	}
	out := Synthesize(in)
	if out.AggregatedSignal != types.SignalLong {
		t.Fatalf("want LONG got %s", out.AggregatedSignal)
	}
	if out.ModulesAgreeing != 1 {
		t.Fatalf("want 1 agreeing got %d", out.ModulesAgreeing)
	}
	// conviction = 80 * 1.0 = 80
	if out.WeightedConviction != 80 {
		t.Fatalf("want conviction 80 got %v", out.WeightedConviction)
	}
	if out.ConsensusEntry == nil || *out.ConsensusEntry != 100 {
		t.Fatalf("want consensus entry 100 got %v", out.ConsensusEntry)
	}
}

func TestSynthesizeExtremeVolModifier(t *testing.T) {
	in := types.SynthesisInput{
		Regime: types.RegimeSignal{Volatility: types.VolExtreme}, // 0.6
		ModuleResults: []types.ModuleRunResult{
			mod("a", 1.0, types.SignalLong, 100, nil),
		},
	}
	out := Synthesize(in)
	if out.WeightedConviction != 60 { // round(100*0.6)
		t.Fatalf("want 60 got %v", out.WeightedConviction)
	}
}

func TestMedianEvenOdd(t *testing.T) {
	if m := median([]float64{3, 1, 2}); m == nil || *m != 2 {
		t.Fatalf("odd median want 2 got %v", m)
	}
	if m := median([]float64{1, 2, 3, 4}); m == nil || *m != 2.5 {
		t.Fatalf("even median want 2.5 got %v", m)
	}
	if m := median(nil); m != nil {
		t.Fatalf("empty median want nil got %v", m)
	}
}
