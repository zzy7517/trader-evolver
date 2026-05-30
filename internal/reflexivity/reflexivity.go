// Package reflexivity implements Soros-style reflexive feedback loops for the
// trading pipeline, ported from atlas-gic's reflexivity engine.
//
// Markets don't just reflect reality — they change it. Five feedback loops:
//
//  1. PRICE → FUNDAMENTALS: Stock drops >15% → credit risk, talent flight.
//     Stock rises >20% → cheap capital, M&A capability.
//
//  2. P&L → BEHAVIOUR: Fund drawdown >10% → forced selling cascade.
//     Gains >15% → increased position sizes.
//
//  3. NARRATIVE → FLOWS: 3+ analysts converge → retail follows.
//     Extended consensus → contrarian reversal.
//
//  4. MARKET → POLICY: Equity drawdown >15% → central bank easing.
//     Oil >$130 → strategic reserve release.
//
//  5. REFLEXIVE REVERSAL DETECTION: Loop running 5+ rounds in one direction
//     → reflexive extreme. Maximum consensus = maximum fragility.
package reflexivity

import (
	"fmt"
	"math"
)

// LoopType identifies which feedback loop is active.
type LoopType string

const (
	LoopPriceFundamentals LoopType = "PRICE_FUNDAMENTALS"
	LoopPnLBehaviour     LoopType = "PNL_BEHAVIOUR"
	LoopNarrativeFlows   LoopType = "NARRATIVE_FLOWS"
	LoopMarketPolicy     LoopType = "MARKET_POLICY"
	LoopReversalDetect   LoopType = "REVERSAL_DETECTION"
)

// Signal represents a detected reflexive signal.
type Signal struct {
	Loop        LoopType
	Description string
	Severity    string // LOW, MEDIUM, HIGH, EXTREME
	Direction   string // BULLISH, BEARISH, REVERSAL
	Confidence  float64 // 0-1
}

// MarketState provides the inputs needed to detect reflexive loops.
type MarketState struct {
	// Price change metrics
	PriceChange30d float64 // percent change over 30 days (e.g., -0.15 = -15%)
	PriceChange7d  float64 // percent change over 7 days

	// Fund P&L
	PortfolioDrawdown float64 // max drawdown from peak (e.g., -0.12 = -12%)
	PortfolioGain30d  float64 // 30-day gain (e.g., 0.18 = +18%)

	// Narrative/consensus
	BullishAnalysts int // number of analysts with bullish stance
	TotalAnalysts   int // total analysts covering
	ConsensusRounds int // how many consecutive rounds of same consensus

	// Market-wide
	SPXDrawdown   float64 // S&P 500 drawdown from recent high
	OilPrice      float64 // current oil price
	VIX           float64 // current VIX level

	// Historical loop state
	LoopDirectionRounds map[LoopType]int // how many rounds each loop has been active
}

// Engine detects reflexive feedback loops and generates warning signals.
type Engine struct {
	// Thresholds (configurable)
	PriceDropThreshold   float64 // default: -0.15
	PriceRiseThreshold   float64 // default: 0.20
	DrawdownThreshold    float64 // default: -0.10
	GainThreshold        float64 // default: 0.15
	ConsensusThreshold   int     // default: 3 analysts
	SPXDropThreshold     float64 // default: -0.15
	OilSpikeThreshold    float64 // default: 130
	ReversalRoundThresh  int     // default: 5 rounds
}

// NewEngine creates a reflexivity engine with atlas-gic's default thresholds.
func NewEngine() *Engine {
	return &Engine{
		PriceDropThreshold:  -0.15,
		PriceRiseThreshold:  0.20,
		DrawdownThreshold:   -0.10,
		GainThreshold:       0.15,
		ConsensusThreshold:  3,
		SPXDropThreshold:    -0.15,
		OilSpikeThreshold:   130,
		ReversalRoundThresh: 5,
	}
}

// Detect analyzes the current market state and returns all active reflexive signals.
func (e *Engine) Detect(state MarketState) []Signal {
	var signals []Signal

	// Loop 1: Price → Fundamentals
	if s := e.detectPriceFundamentals(state); s != nil {
		signals = append(signals, *s)
	}

	// Loop 2: P&L → Behaviour
	if s := e.detectPnLBehaviour(state); s != nil {
		signals = append(signals, *s)
	}

	// Loop 3: Narrative → Flows
	if s := e.detectNarrativeFlows(state); s != nil {
		signals = append(signals, *s)
	}

	// Loop 4: Market → Policy
	if s := e.detectMarketPolicy(state); s != nil {
		signals = append(signals, *s)
	}

	// Loop 5: Reversal Detection
	for _, s := range e.detectReversals(state) {
		signals = append(signals, s)
	}

	return signals
}

// detectPriceFundamentals: large price moves that change fundamentals.
func (e *Engine) detectPriceFundamentals(state MarketState) *Signal {
	if state.PriceChange30d <= e.PriceDropThreshold {
		severity := "MEDIUM"
		if state.PriceChange30d <= -0.25 {
			severity = "HIGH"
		}
		if state.PriceChange30d <= -0.35 {
			severity = "EXTREME"
		}
		return &Signal{
			Loop:        LoopPriceFundamentals,
			Description: fmt.Sprintf("Price drop %.1f%% triggers credit risk, talent flight, capex cuts", state.PriceChange30d*100),
			Severity:    severity,
			Direction:   "BEARISH",
			Confidence:  math.Min(1, math.Abs(state.PriceChange30d)/0.30),
		}
	}

	if state.PriceChange30d >= e.PriceRiseThreshold {
		severity := "LOW"
		if state.PriceChange30d >= 0.30 {
			severity = "MEDIUM"
		}
		return &Signal{
			Loop:        LoopPriceFundamentals,
			Description: fmt.Sprintf("Price rise +%.1f%% enables cheap capital, M&A capability, talent attraction", state.PriceChange30d*100),
			Severity:    severity,
			Direction:   "BULLISH",
			Confidence:  math.Min(1, state.PriceChange30d/0.30),
		}
	}

	return nil
}

// detectPnLBehaviour: fund performance affecting trading behaviour.
func (e *Engine) detectPnLBehaviour(state MarketState) *Signal {
	if state.PortfolioDrawdown <= e.DrawdownThreshold {
		severity := "MEDIUM"
		if state.PortfolioDrawdown <= -0.20 {
			severity = "HIGH"
		}
		return &Signal{
			Loop:        LoopPnLBehaviour,
			Description: fmt.Sprintf("Drawdown %.1f%% → forced selling cascade risk, margin pressure", state.PortfolioDrawdown*100),
			Severity:    severity,
			Direction:   "BEARISH",
			Confidence:  math.Min(1, math.Abs(state.PortfolioDrawdown)/0.20),
		}
	}

	if state.PortfolioGain30d >= e.GainThreshold {
		return &Signal{
			Loop:        LoopPnLBehaviour,
			Description: fmt.Sprintf("Gains +%.1f%% → position size increase, concentration risk", state.PortfolioGain30d*100),
			Severity:    "LOW",
			Direction:   "BULLISH",
			Confidence:  math.Min(1, state.PortfolioGain30d/0.25),
		}
	}

	return nil
}

// detectNarrativeFlows: consensus-driven flow patterns.
func (e *Engine) detectNarrativeFlows(state MarketState) *Signal {
	if state.TotalAnalysts == 0 {
		return nil
	}

	bullishRatio := float64(state.BullishAnalysts) / float64(state.TotalAnalysts)

	// Strong consensus → retail follows
	if state.BullishAnalysts >= e.ConsensusThreshold && bullishRatio > 0.7 {
		severity := "LOW"
		if state.ConsensusRounds >= 5 {
			severity = "MEDIUM" // extended consensus → fragile
		}
		return &Signal{
			Loop:        LoopNarrativeFlows,
			Description: fmt.Sprintf("%d/%d analysts bullish (%.0f%%) for %d rounds → crowded trade risk", state.BullishAnalysts, state.TotalAnalysts, bullishRatio*100, state.ConsensusRounds),
			Severity:    severity,
			Direction:   "BULLISH",
			Confidence:  bullishRatio,
		}
	}

	// Strong bearish consensus
	bearishRatio := 1 - bullishRatio
	if state.TotalAnalysts-state.BullishAnalysts >= e.ConsensusThreshold && bearishRatio > 0.7 {
		return &Signal{
			Loop:        LoopNarrativeFlows,
			Description: fmt.Sprintf("%.0f%% bearish consensus → potential contrarian reversal", bearishRatio*100),
			Severity:    "MEDIUM",
			Direction:   "BEARISH",
			Confidence:  bearishRatio,
		}
	}

	return nil
}

// detectMarketPolicy: market conditions that trigger policy responses.
func (e *Engine) detectMarketPolicy(state MarketState) *Signal {
	if state.SPXDrawdown <= e.SPXDropThreshold {
		return &Signal{
			Loop:        LoopMarketPolicy,
			Description: fmt.Sprintf("SPX drawdown %.1f%% → central bank easing signals likely", state.SPXDrawdown*100),
			Severity:    "HIGH",
			Direction:   "BULLISH", // policy response is bullish
			Confidence:  math.Min(1, math.Abs(state.SPXDrawdown)/0.20),
		}
	}

	if state.OilPrice >= e.OilSpikeThreshold {
		return &Signal{
			Loop:        LoopMarketPolicy,
			Description: fmt.Sprintf("Oil at $%.0f → strategic reserve release, demand destruction", state.OilPrice),
			Severity:    "MEDIUM",
			Direction:   "BEARISH",
			Confidence:  math.Min(1, (state.OilPrice-100)/50),
		}
	}

	return nil
}

// detectReversals: loops running too long in one direction.
func (e *Engine) detectReversals(state MarketState) []Signal {
	var signals []Signal

	for loopType, rounds := range state.LoopDirectionRounds {
		absRounds := rounds
		if absRounds < 0 {
			absRounds = -absRounds
		}

		if absRounds >= e.ReversalRoundThresh {
			direction := "REVERSAL"
			desc := fmt.Sprintf("%s loop active for %d rounds → reflexive extreme, maximum fragility", loopType, absRounds)

			severity := "MEDIUM"
			if absRounds >= 8 {
				severity = "HIGH"
			}
			if absRounds >= 12 {
				severity = "EXTREME"
			}

			signals = append(signals, Signal{
				Loop:        LoopReversalDetect,
				Description: desc,
				Severity:    severity,
				Direction:   direction,
				Confidence:  math.Min(1, float64(absRounds)/10.0),
			})
		}
	}

	return signals
}

// FormatFlags converts signals to the string slice format used by CROOutput.
func FormatFlags(signals []Signal) []string {
	flags := make([]string, len(signals))
	for i, s := range signals {
		flags[i] = fmt.Sprintf("[%s] %s (%s, conf=%.0f%%)", s.Loop, s.Description, s.Severity, s.Confidence*100)
	}
	return flags
}
