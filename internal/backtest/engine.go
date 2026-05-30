// Package backtest implements the time-travel replay engine that iterates over
// historical days, reconstructs the regime from store as-of lookups, runs the
// pipeline (with LLM module calls), records recommendations, and backfills
// forward returns once future data becomes available.
//
// This is the core payoff of trader-evolver: replaying many years of history to
// "fast-forward time" and validate whether regime detection, multi-module analysis,
// and Darwinian module-weight evolution actually work.
//
// Design mirrors atlas-gic's backtest_loop.py but in Go with SQLite as the store.
package backtest

import (
	"context"
	"fmt"
	"log"
	"time"

	"trader-evolver/internal/llm"
	"trader-evolver/internal/modules"
	"trader-evolver/internal/orchestrator"
	"trader-evolver/internal/store"
	"trader-evolver/internal/types"
)

// ─── Configuration ───

// Config holds backtest parameters.
type Config struct {
	// StartDateMs / EndDateMs define the replay window (midnight-UTC epoch millis).
	StartDateMs int64
	EndDateMs   int64
	// InstrumentKey is the asset being backtested (e.g. "btc:usdt").
	InstrumentKey string
	// Interval for candles used in module analysis (default "1d").
	Interval string
	// LookbackBars is how many candles to include in each module prompt.
	LookbackBars int
	// DarwinUpdateFrequency: update Darwin weights every N trading days.
	DarwinUpdateFreq int
	// MinRecommendationsForSharpe: minimum recs needed before computing Sharpe.
	MinRecsForSharpe int
	// PromptDir is the path to prompt template files.
	PromptDir string
}

// DefaultConfig returns a sensible default configuration.
func DefaultConfig() Config {
	return Config{
		Interval:         "1d",
		LookbackBars:     60,
		DarwinUpdateFreq: 1,  // daily, like atlas-gic
		MinRecsForSharpe: 10,
		PromptDir:        "./prompts",
	}
}

// ─── Engine ───

// Engine orchestrates the backtest loop.
type Engine struct {
	Store    *store.Store
	Provider llm.Provider
	Config   Config

	// State
	darwinWeights   map[string]float64
	recommendations []types.Recommendation
	dayResults      []DayResult

	// Callbacks for observability
	OnDayComplete func(day DayResult)
}

// DayResult captures what happened on a single simulated day.
type DayResult struct {
	Day           int
	DateMs        int64
	Date          string // YYYY-MM-DD
	Regime        types.RegimeSignal
	Decision      *types.TradeDecision
	ModuleResults []types.ModuleRunResult
	DarwinWeights map[string]float64 // snapshot after update
	Error         *string
}

// NewEngine creates a backtest engine.
func NewEngine(st *store.Store, provider llm.Provider, cfg Config) *Engine {
	// Initialize Darwin weights to default
	weights := make(map[string]float64)
	for _, id := range types.DefaultModuleIDs {
		weights[id] = types.DefaultDarwinWeight
	}

	return &Engine{
		Store:         st,
		Provider:      provider,
		Config:        cfg,
		darwinWeights: weights,
	}
}

// Run executes the full backtest loop from StartDateMs to EndDateMs.
func (e *Engine) Run(ctx context.Context) ([]DayResult, error) {
	if e.Config.StartDateMs == 0 || e.Config.EndDateMs == 0 {
		return nil, fmt.Errorf("backtest: StartDateMs and EndDateMs must be set")
	}
	if e.Config.InstrumentKey == "" {
		return nil, fmt.Errorf("backtest: InstrumentKey must be set")
	}

	// Get all trading days from candle data
	tradingDays, err := e.getTradingDays()
	if err != nil {
		return nil, fmt.Errorf("backtest: get trading days: %w", err)
	}
	if len(tradingDays) == 0 {
		return nil, fmt.Errorf("backtest: no trading days found in [%d, %d]", e.Config.StartDateMs, e.Config.EndDateMs)
	}

	log.Printf("[backtest] %d trading days from %s to %s",
		len(tradingDays),
		time.UnixMilli(tradingDays[0]).UTC().Format("2006-01-02"),
		time.UnixMilli(tradingDays[len(tradingDays)-1]).UTC().Format("2006-01-02"),
	)

	for dayIdx, dayMs := range tradingDays {
		if ctx.Err() != nil {
			return e.dayResults, ctx.Err()
		}

		result := e.runSingleDay(ctx, dayIdx+1, dayMs, tradingDays)
		e.dayResults = append(e.dayResults, result)

		// Backfill forward returns for older recommendations
		e.backfillReturns(dayMs, tradingDays)

		// Update Darwin weights periodically
		if (dayIdx+1)%e.Config.DarwinUpdateFreq == 0 {
			e.updateDarwinWeights()
		}

		if e.OnDayComplete != nil {
			e.OnDayComplete(result)
		}
	}

	return e.dayResults, nil
}

// runSingleDay executes the pipeline for one simulated trading day.
func (e *Engine) runSingleDay(ctx context.Context, dayNum int, dayMs int64, allDays []int64) DayResult {
	dateStr := time.UnixMilli(dayMs).UTC().Format("2006-01-02")
	result := DayResult{
		Day:    dayNum,
		DateMs: dayMs,
		Date:   dateStr,
	}

	// 1. Get current price (close of this day's candle)
	currentPrice, err := e.getCurrentPrice(dayMs)
	if err != nil {
		errStr := fmt.Sprintf("no price data: %v", err)
		result.Error = &errStr
		return result
	}

	// 2. Get candle data string for prompts (lookback)
	candleData := e.getCandleDataForPrompt(dayMs)

	// 3. Build regime indicators from as-of lookups
	indicators := e.resolveRegimeIndicators(dayMs)

	// 4. Build orchestrator with backtest deps and run pipeline
	composer := modules.NewPromptComposer(e.Config.PromptDir)
	darwinEntries := e.buildDarwinEntries()

	deps := orchestrator.Deps{
		ResolveRegimeIndicators: func(instrumentKey string) types.RegimeIndicators {
			return indicators
		},
		GetCandleData: func(instrumentKey string) string {
			return candleData
		},
		GetCurrentPrice: func(instrumentKey string) *float64 {
			p := currentPrice
			return &p
		},
		GetDarwinWeights: func() []types.DarwinWeightEntry {
			return darwinEntries
		},
		GetFundamentalContext: func(instrumentKey string) string {
			return "" // no fundamental data in backtest
		},
		GetFundingRate: func(instrumentKey string) *float64 {
			return nil // not available historically
		},
		GetLongShortRatio: func(instrumentKey string) *float64 {
			return nil // not available historically
		},
		GetOIDelta: func(instrumentKey string) *float64 {
			return nil // not available historically
		},
	}

	orch := orchestrator.New(deps, composer, e.Provider)
	run, err := orch.Run(ctx, e.Config.InstrumentKey, types.TriggerCron)
	if err != nil {
		errStr := err.Error()
		result.Error = &errStr
		return result
	}

	result.Regime = run.Regime
	result.Decision = run.Decision
	result.ModuleResults = run.ModuleResults

	// 5. Record recommendations from module results
	e.recordRecommendations(dayMs, dateStr, currentPrice, run.ModuleResults)

	// 6. Snapshot Darwin weights
	wSnap := make(map[string]float64)
	for k, v := range e.darwinWeights {
		wSnap[k] = v
	}
	result.DarwinWeights = wSnap

	return result
}

// resolveRegimeIndicators builds RegimeIndicators from store as-of lookups.
func (e *Engine) resolveRegimeIndicators(dayMs int64) types.RegimeIndicators {
	var indicators types.RegimeIndicators

	// VIX
	if vixVal, found, err := e.Store.MacroAsOf("VIX", dayMs); err == nil && found {
		indicators.VIX = &vixVal
	}

	// DXY
	if dxyVal, found, err := e.Store.MacroAsOf("DXY", dayMs); err == nil && found {
		indicators.DXY = &dxyVal
	}

	// Fear & Greed
	if fgVal, found, err := e.Store.FearGreedAsOf(dayMs); err == nil && found {
		fgFloat := float64(fgVal)
		indicators.FearGreed = &fgFloat
	}

	// Funding rate, OI, long/short ratio → nil in backtest (graceful degradation)
	return indicators
}

// getCurrentPrice gets the close price for the instrument on this day.
func (e *Engine) getCurrentPrice(dayMs int64) (float64, error) {
	candles, err := e.Store.GetCandles(e.Config.InstrumentKey, e.Config.Interval, dayMs, dayMs)
	if err != nil {
		return 0, err
	}
	if len(candles) == 0 {
		return 0, fmt.Errorf("no candle at %d for %s", dayMs, e.Config.InstrumentKey)
	}
	return candles[0].Close, nil
}

// getCandleDataForPrompt builds a text summary of recent candles for LLM prompts.
func (e *Engine) getCandleDataForPrompt(dayMs int64) string {
	// Compute lookback start: N bars back
	lookbackMs := int64(e.Config.LookbackBars) * 86400 * 1000 // rough estimate for daily
	startMs := dayMs - lookbackMs

	candles, err := e.Store.GetCandles(e.Config.InstrumentKey, e.Config.Interval, startMs, dayMs)
	if err != nil || len(candles) == 0 {
		return "No candle data available."
	}

	// Format as compact text for LLM
	var s string
	for _, c := range candles {
		d := time.UnixMilli(c.OpenTimeMs).UTC().Format("2006-01-02")
		s += fmt.Sprintf("%s O:%.2f H:%.2f L:%.2f C:%.2f V:%.0f\n", d, c.Open, c.High, c.Low, c.Close, c.Volume)
	}
	return s
}

// buildDarwinEntries converts the weight map to the expected entry format.
func (e *Engine) buildDarwinEntries() []types.DarwinWeightEntry {
	entries := make([]types.DarwinWeightEntry, 0, len(e.darwinWeights))
	for id, w := range e.darwinWeights {
		entries = append(entries, types.DarwinWeightEntry{
			ModuleID:  id,
			Weight:    w,
			UpdatedAt: time.Now().UTC().Format(time.RFC3339),
		})
	}
	return entries
}

// recordRecommendations stores the pipeline's module signals as recommendations
// for later scoring.
func (e *Engine) recordRecommendations(dayMs int64, dateStr string, currentPrice float64, results []types.ModuleRunResult) {
	for _, mr := range results {
		if mr.Error != nil {
			continue // skip errored modules
		}
		if mr.Output.Signal == types.SignalNeutral {
			continue // don't track neutral signals
		}
		rec := types.Recommendation{
			ModuleID:              mr.ModuleID,
			InstrumentKey:         e.Config.InstrumentKey,
			Signal:                mr.Output.Signal,
			Conviction:            mr.Output.Conviction,
			PriceAtRecommendation: currentPrice,
			RecommendedAt:         dateStr,
		}
		e.recommendations = append(e.recommendations, rec)
	}
}

// backfillReturns fills forward returns (1d, 5d, 20d) for past recommendations
// now that we have future price data.
func (e *Engine) backfillReturns(currentDayMs int64, allDays []int64) {
	for i := range e.recommendations {
		rec := &e.recommendations[i]
		if rec.Return1d != nil && rec.Return5d != nil && rec.Return20d != nil {
			continue // already fully filled
		}

		recDate, err := time.Parse("2006-01-02", rec.RecommendedAt)
		if err != nil {
			continue
		}
		recMs := time.Date(recDate.Year(), recDate.Month(), recDate.Day(), 0, 0, 0, 0, time.UTC).UnixMilli()

		// Find the index of the recommendation day
		recIdx := -1
		for j, d := range allDays {
			if d == recMs {
				recIdx = j
				break
			}
		}
		if recIdx < 0 {
			continue
		}

		// 1d return
		if rec.Return1d == nil && recIdx+1 < len(allDays) {
			if allDays[recIdx+1] <= currentDayMs {
				rec.Return1d = e.calcReturn(rec.PriceAtRecommendation, allDays[recIdx+1])
			}
		}

		// 5d return
		if rec.Return5d == nil && recIdx+5 < len(allDays) {
			if allDays[recIdx+5] <= currentDayMs {
				rec.Return5d = e.calcReturn(rec.PriceAtRecommendation, allDays[recIdx+5])
			}
		}

		// 20d return
		if rec.Return20d == nil && recIdx+20 < len(allDays) {
			if allDays[recIdx+20] <= currentDayMs {
				rec.Return20d = e.calcReturn(rec.PriceAtRecommendation, allDays[recIdx+20])
			}
		}
	}
}

// calcReturn computes the percentage return from entryPrice to the close
// price at futureMs. Returns nil if price is unavailable.
func (e *Engine) calcReturn(entryPrice float64, futureMs int64) *float64 {
	candles, err := e.Store.GetCandles(e.Config.InstrumentKey, e.Config.Interval, futureMs, futureMs)
	if err != nil || len(candles) == 0 {
		return nil
	}
	futurePrice := candles[0].Close
	if entryPrice == 0 {
		return nil
	}
	ret := (futurePrice - entryPrice) / entryPrice
	return &ret
}

// updateDarwinWeights applies the Darwinian evolution logic:
// top quartile agents get weight × 1.05, bottom quartile get × 0.95.
// Matches atlas-gic's autoresearch.md specification.
func (e *Engine) updateDarwinWeights() {
	if len(e.recommendations) < e.Config.MinRecsForSharpe {
		return
	}

	// Calculate rolling Sharpe for each module
	sharpes := make(map[string]float64)
	for _, id := range types.DefaultModuleIDs {
		sharpes[id] = e.calcModuleSharpe(id)
	}

	// Collect values for quartile thresholds
	vals := make([]float64, 0, len(sharpes))
	for _, s := range sharpes {
		vals = append(vals, s)
	}
	if len(vals) < 2 {
		return
	}

	sortFloat64s(vals)
	q1Idx := len(vals) / 4
	q3Idx := len(vals) * 3 / 4
	q1Threshold := vals[q1Idx]
	q3Threshold := vals[q3Idx]

	// Apply weight updates
	for _, id := range types.DefaultModuleIDs {
		s := sharpes[id]
		w := e.darwinWeights[id]

		if s >= q3Threshold {
			w *= types.WeightGrowthFactor
		} else if s <= q1Threshold {
			w *= types.WeightDecayFactor
		}

		// Clamp to bounds
		if w > types.MaxDarwinWeight {
			w = types.MaxDarwinWeight
		}
		if w < types.MinDarwinWeight {
			w = types.MinDarwinWeight
		}
		e.darwinWeights[id] = w
	}
}

// calcModuleSharpe computes a rolling Sharpe ratio for a module's recommendations
// using conviction-weighted returns (matching atlas-gic's methodology).
func (e *Engine) calcModuleSharpe(moduleID string) float64 {
	var returns []float64

	for _, rec := range e.recommendations {
		if rec.ModuleID != moduleID {
			continue
		}
		if rec.Return5d == nil {
			continue
		}

		// Conviction-weighted return
		convictionWeight := rec.Conviction / 100.0
		ret := *rec.Return5d * convictionWeight

		// Flip sign for SHORT recommendations
		if rec.Signal == types.SignalShort {
			ret = -ret
		}

		returns = append(returns, ret)
	}

	if len(returns) < 2 {
		return 0
	}

	var sum float64
	for _, r := range returns {
		sum += r
	}
	mean := sum / float64(len(returns))

	var variance float64
	for _, r := range returns {
		diff := r - mean
		variance += diff * diff
	}
	variance /= float64(len(returns) - 1)

	if variance <= 0 {
		return 0
	}

	stdDev := sqrt(variance)
	return mean / stdDev
}

// getTradingDays returns all unique day timestamps for the instrument in the range.
func (e *Engine) getTradingDays() ([]int64, error) {
	candles, err := e.Store.GetCandles(
		e.Config.InstrumentKey,
		e.Config.Interval,
		e.Config.StartDateMs,
		e.Config.EndDateMs,
	)
	if err != nil {
		return nil, err
	}

	days := make([]int64, 0, len(candles))
	for _, c := range candles {
		days = append(days, c.OpenTimeMs)
	}
	return days, nil
}

// Recommendations returns all recorded recommendations (for reporting).
func (e *Engine) Recommendations() []types.Recommendation {
	return e.recommendations
}

// DarwinWeightsSnapshot returns the current Darwin weights.
func (e *Engine) DarwinWeightsSnapshot() map[string]float64 {
	result := make(map[string]float64)
	for k, v := range e.darwinWeights {
		result[k] = v
	}
	return result
}

// ── helpers ──

func sqrt(x float64) float64 {
	if x <= 0 {
		return 0
	}
	z := x
	for i := 0; i < 20; i++ {
		z = (z + x/z) / 2
	}
	return z
}

func sortFloat64s(s []float64) {
	// Insertion sort (N is small — 5 modules)
	for i := 1; i < len(s); i++ {
		key := s[i]
		j := i - 1
		for j >= 0 && s[j] > key {
			s[j+1] = s[j]
			j--
		}
		s[j+1] = key
	}
}