package reflexivity

import (
	"testing"
)

func TestDetect_NoSignals(t *testing.T) {
	e := NewEngine()
	state := MarketState{
		PriceChange30d:    0.05,  // +5%, within normal
		PortfolioDrawdown: -0.03, // -3%, minor
		BullishAnalysts:   2,
		TotalAnalysts:     5,
		SPXDrawdown:       -0.05,
		OilPrice:          80,
	}
	signals := e.Detect(state)
	if len(signals) != 0 {
		t.Errorf("expected no signals for normal state, got %d: %v", len(signals), signals)
	}
}

func TestDetect_PriceDrop(t *testing.T) {
	e := NewEngine()
	state := MarketState{
		PriceChange30d: -0.20, // -20% drop
	}
	signals := e.Detect(state)

	found := false
	for _, s := range signals {
		if s.Loop == LoopPriceFundamentals && s.Direction == "BEARISH" {
			found = true
			if s.Severity != "MEDIUM" && s.Severity != "HIGH" {
				t.Errorf("expected MEDIUM or HIGH severity for -20%%, got %s", s.Severity)
			}
		}
	}
	if !found {
		t.Error("expected PRICE_FUNDAMENTALS bearish signal for -20% drop")
	}
}

func TestDetect_PriceRise(t *testing.T) {
	e := NewEngine()
	state := MarketState{
		PriceChange30d: 0.25, // +25%
	}
	signals := e.Detect(state)

	found := false
	for _, s := range signals {
		if s.Loop == LoopPriceFundamentals && s.Direction == "BULLISH" {
			found = true
		}
	}
	if !found {
		t.Error("expected bullish signal for +25% price rise")
	}
}

func TestDetect_DrawdownCascade(t *testing.T) {
	e := NewEngine()
	state := MarketState{
		PortfolioDrawdown: -0.15, // -15%
	}
	signals := e.Detect(state)

	found := false
	for _, s := range signals {
		if s.Loop == LoopPnLBehaviour && s.Direction == "BEARISH" {
			found = true
			t.Logf("P&L signal: %s (severity: %s)", s.Description, s.Severity)
		}
	}
	if !found {
		t.Error("expected PNL_BEHAVIOUR bearish signal for -15% drawdown")
	}
}

func TestDetect_GainsConcentration(t *testing.T) {
	e := NewEngine()
	state := MarketState{
		PortfolioGain30d: 0.20, // +20%
	}
	signals := e.Detect(state)

	found := false
	for _, s := range signals {
		if s.Loop == LoopPnLBehaviour && s.Direction == "BULLISH" {
			found = true
		}
	}
	if !found {
		t.Error("expected PNL_BEHAVIOUR bullish signal for +20% gains")
	}
}

func TestDetect_NarrativeConsensus(t *testing.T) {
	e := NewEngine()
	state := MarketState{
		BullishAnalysts: 4,
		TotalAnalysts:   5,
		ConsensusRounds: 3,
	}
	signals := e.Detect(state)

	found := false
	for _, s := range signals {
		if s.Loop == LoopNarrativeFlows {
			found = true
			t.Logf("Narrative signal: %s", s.Description)
		}
	}
	if !found {
		t.Error("expected NARRATIVE_FLOWS signal for 4/5 bullish consensus")
	}
}

func TestDetect_MarketPolicy_SPXDrop(t *testing.T) {
	e := NewEngine()
	state := MarketState{
		SPXDrawdown: -0.18, // -18%
	}
	signals := e.Detect(state)

	found := false
	for _, s := range signals {
		if s.Loop == LoopMarketPolicy && s.Direction == "BULLISH" {
			found = true
		}
	}
	if !found {
		t.Error("expected MARKET_POLICY bullish (easing) signal for -18% SPX drawdown")
	}
}

func TestDetect_MarketPolicy_OilSpike(t *testing.T) {
	e := NewEngine()
	state := MarketState{
		OilPrice: 135, // $135
	}
	signals := e.Detect(state)

	found := false
	for _, s := range signals {
		if s.Loop == LoopMarketPolicy && s.Direction == "BEARISH" {
			found = true
		}
	}
	if !found {
		t.Error("expected MARKET_POLICY bearish signal for oil at $135")
	}
}

func TestDetect_ReversalExtreme(t *testing.T) {
	e := NewEngine()
	state := MarketState{
		LoopDirectionRounds: map[LoopType]int{
			LoopNarrativeFlows: 7, // 7 rounds of same direction
		},
	}
	signals := e.Detect(state)

	found := false
	for _, s := range signals {
		if s.Loop == LoopReversalDetect {
			found = true
			if s.Direction != "REVERSAL" {
				t.Errorf("expected REVERSAL direction, got %s", s.Direction)
			}
			t.Logf("Reversal signal: %s (severity: %s)", s.Description, s.Severity)
		}
	}
	if !found {
		t.Error("expected REVERSAL_DETECTION signal for 7 rounds")
	}
}

func TestDetect_NoReversalBelowThreshold(t *testing.T) {
	e := NewEngine()
	state := MarketState{
		LoopDirectionRounds: map[LoopType]int{
			LoopNarrativeFlows: 3, // below 5-round threshold
		},
	}
	signals := e.Detect(state)
	for _, s := range signals {
		if s.Loop == LoopReversalDetect {
			t.Error("should not detect reversal for only 3 rounds")
		}
	}
}

func TestDetect_MultipleSignals(t *testing.T) {
	e := NewEngine()
	state := MarketState{
		PriceChange30d:    -0.25,
		PortfolioDrawdown: -0.15,
		SPXDrawdown:       -0.18,
		LoopDirectionRounds: map[LoopType]int{
			LoopPriceFundamentals: 6,
		},
	}
	signals := e.Detect(state)
	if len(signals) < 3 {
		t.Errorf("expected at least 3 signals for severe market stress, got %d", len(signals))
	}
	t.Logf("Detected %d signals:", len(signals))
	for _, s := range signals {
		t.Logf("  [%s] %s (%s)", s.Loop, s.Description, s.Severity)
	}
}

func TestFormatFlags(t *testing.T) {
	signals := []Signal{
		{Loop: LoopPriceFundamentals, Description: "test", Severity: "HIGH", Direction: "BEARISH", Confidence: 0.8},
	}
	flags := FormatFlags(signals)
	if len(flags) != 1 {
		t.Fatal("expected 1 flag")
	}
	if flags[0] == "" {
		t.Error("expected non-empty flag string")
	}
	t.Logf("Flag: %s", flags[0])
}

func TestExtremeDropSeverity(t *testing.T) {
	e := NewEngine()
	state := MarketState{
		PriceChange30d: -0.40, // -40% crash
	}
	signals := e.Detect(state)
	for _, s := range signals {
		if s.Loop == LoopPriceFundamentals {
			if s.Severity != "EXTREME" {
				t.Errorf("expected EXTREME severity for -40%%, got %s", s.Severity)
			}
		}
	}
}
