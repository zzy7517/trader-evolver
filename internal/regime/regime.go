// Package regime ports tradex/pipeline/regime_detector.ts.
//
// The detector is pure: it takes already-resolved indicators and emits a
// RegimeSignal via deterministic scoring. Data sourcing (VIX/ADX/feeds) is the
// caller's responsibility (orchestrator/backtest), mirroring how the TS
// RegimeDetector received values through injected dependency callbacks.
package regime

import (
	"time"

	"trader-evolver/internal/types"
)

// Detect builds a RegimeSignal from resolved indicators.
// Equivalent to RegimeDetector.detect() after the deps have been read.
func Detect(ind types.RegimeIndicators) types.RegimeSignal {
	return types.RegimeSignal{
		Market:     detectMarketRegime(ind),
		Volatility: detectVolatility(ind),
		Trend:      detectTrend(ind),
		Indicators: ind,
		DetectedAt: time.Now().UTC().Format(time.RFC3339),
	}
}

// detectMarketRegime mirrors the TS scoring exactly.
func detectMarketRegime(ind types.RegimeIndicators) types.MarketRegime {
	score := 0.0 // positive = risk on, negative = risk off

	// VIX
	if ind.VIX != nil {
		v := *ind.VIX
		switch {
		case v < 16:
			score += 2
		case v < 20:
			score += 1
		case v > 30:
			score -= 2
		case v > 25:
			score -= 1
		}
	}

	// Fear & Greed
	if ind.FearGreed != nil {
		fg := *ind.FearGreed
		switch {
		case fg > 70:
			score += 1
		case fg > 55:
			score += 0.5
		case fg < 25:
			score -= 1
		case fg < 40:
			score -= 0.5
		}
	}

	// Funding rate (extreme = crowded)
	if ind.FundingRate != nil {
		fr := *ind.FundingRate
		if fr > 0.001 {
			score += 0.5 // bullish crowd
		} else if fr < -0.001 {
			score -= 0.5
		}
	}

	// OI growth = money flowing in = bullish
	if ind.OIDelta1h != nil {
		oi := *ind.OIDelta1h
		if oi > 0 {
			score += 0.5
		} else if oi < 0 {
			score -= 0.5
		}
	}

	if score >= 2 {
		return types.RiskOn
	}
	if score <= -2 {
		return types.RiskOff
	}
	return types.Neutral
}

func detectVolatility(ind types.RegimeIndicators) types.VolatilityRegime {
	if ind.VIX == nil {
		return types.VolMedium
	}
	v := *ind.VIX
	switch {
	case v >= 35:
		return types.VolExtreme
	case v >= 25:
		return types.VolHigh
	case v >= 16:
		return types.VolMedium
	default:
		return types.VolLow
	}
}

func detectTrend(ind types.RegimeIndicators) types.TrendRegime {
	if ind.ADX == nil {
		return types.TrendRange
	}
	adx := *ind.ADX

	// ADX only tells strength, not direction. Combine with Fear/Greed as proxy.
	fg := 50.0
	if ind.FearGreed != nil {
		fg = *ind.FearGreed
	}
	bullish := fg > 50

	if adx >= 40 {
		if bullish {
			return types.TrendStrongUp
		}
		return types.TrendStrongDown
	}
	if adx >= 25 {
		if bullish {
			return types.TrendUp
		}
		return types.TrendDown
	}
	return types.TrendRange
}
