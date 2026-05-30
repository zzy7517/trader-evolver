// Package orchestrator wires the 4-layer pipeline, ported from
// tradex/pipeline/orchestrator.ts.
//
//	L1 Regime (pure rules) -> L2 Modules x5 (LLM, parallel) ->
//	L3 Synthesizer (Darwin-weighted vote) + CRO (LLM) -> L4 Decision
package orchestrator

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/google/uuid"

	"trader-evolver/internal/llm"
	"trader-evolver/internal/modules"
	"trader-evolver/internal/regime"
	"trader-evolver/internal/synth"
	"trader-evolver/internal/types"
)

// analysisModules is the canonical L2 module set (matches tradex ANALYSIS_MODULES).
var analysisModules = []string{
	"ict_trader",
	"chanlun_analyst",
	"wave_analyst",
	"indicator_analyst",
	"fundamental_analyst",
}

// Deps supplies the data + side-channels the orchestrator needs, mirroring
// tradex OrchestratorDeps. Backtest and live wiring both implement these.
type Deps struct {
	// ResolveRegimeIndicators returns the regime inputs for the instrument at
	// the current point in time. (In backtests, funding/OI/LS are typically nil.)
	ResolveRegimeIndicators func(instrumentKey string) types.RegimeIndicators

	GetCandleData         func(instrumentKey string) string
	GetCurrentPrice       func(instrumentKey string) *float64
	GetDarwinWeights      func() []types.DarwinWeightEntry
	GetFundamentalContext func(instrumentKey string) string
	GetFundingRate        func(instrumentKey string) *float64
	GetLongShortRatio     func(instrumentKey string) *float64
	GetOIDelta            func(instrumentKey string) *float64

	// OnComplete is called with every finished run (completed or failed).
	OnComplete func(run types.PipelineRun)
}

// Orchestrator runs the full pipeline for one instrument at a time.
type Orchestrator struct {
	deps         Deps
	moduleRunner *modules.ModuleRunner
	cro          *modules.AdversarialReviewer

	mu      sync.Mutex
	running bool

	// CurrentRegime / LastRun expose the most recent state (for snapshots).
	CurrentRegime *types.RegimeSignal
	LastRun       *types.PipelineRun
}

// New builds an Orchestrator. The same composer/provider used elsewhere is
// injected so backtests can pass a MockProvider.
func New(deps Deps, composer *modules.PromptComposer, provider llm.Provider) *Orchestrator {
	return &Orchestrator{
		deps:         deps,
		moduleRunner: modules.NewModuleRunner(composer, provider),
		cro:          modules.NewAdversarialReviewer(composer, provider),
	}
}

// IsRunning reports whether a run is in progress.
func (o *Orchestrator) IsRunning() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.running
}

// Run executes the 4-layer pipeline for one instrument.
func (o *Orchestrator) Run(ctx context.Context, instrumentKey string, trigger types.PipelineTrigger) (types.PipelineRun, error) {
	o.mu.Lock()
	if o.running {
		o.mu.Unlock()
		return types.PipelineRun{}, fmt.Errorf("Pipeline already running")
	}
	o.running = true
	o.mu.Unlock()
	defer func() {
		o.mu.Lock()
		o.running = false
		o.mu.Unlock()
	}()

	id := uuid.NewString()
	startedAt := time.Now().UTC()
	startedAtStr := startedAt.Format(time.RFC3339)

	// ── LAYER 1: Regime detection ──
	reg := regime.Detect(o.deps.ResolveRegimeIndicators(instrumentKey))
	o.CurrentRegime = &reg

	// ── LAYER 2: Multi-method analysis (parallel) ──
	candleData := o.deps.GetCandleData(instrumentKey)
	weightMap := map[string]float64{}
	for _, w := range o.deps.GetDarwinWeights() {
		weightMap[w.ModuleID] = w.Weight
	}

	results := make([]types.ModuleRunResult, len(analysisModules))
	var wg sync.WaitGroup
	for i, moduleID := range analysisModules {
		weight := 1.0
		if w, ok := weightMap[moduleID]; ok {
			weight = w
		}
		additional := ""
		if moduleID == "fundamental_analyst" && o.deps.GetFundamentalContext != nil {
			additional = o.deps.GetFundamentalContext(instrumentKey)
		}
		wg.Add(1)
		go func(idx int, mid string, w float64, add string) {
			defer wg.Done()
			results[idx] = o.moduleRunner.Run(ctx, mid, instrumentKey, reg, candleData, w, add)
		}(i, moduleID, weight, additional)
	}
	wg.Wait()

	// ── LAYER 3: Synthesis + CRO ──
	currentPrice := o.deps.GetCurrentPrice(instrumentKey)
	price := 0.0
	if currentPrice != nil {
		price = *currentPrice
	}

	synthesis := synth.Synthesize(types.SynthesisInput{
		Regime:        reg,
		ModuleResults: results,
		InstrumentKey: instrumentKey,
		CurrentPrice:  price,
	})

	decision := o.decide(ctx, instrumentKey, reg, synthesis, price)

	// Aggregate tokens.
	totalTokens := 0
	for _, r := range results {
		totalTokens += r.TokensUsed
	}

	completedAt := time.Now().UTC().Format(time.RFC3339)
	run := types.PipelineRun{
		ID:            id,
		TriggeredBy:   trigger,
		InstrumentKey: instrumentKey,
		Regime:        reg,
		StartedAt:     startedAtStr,
		CompletedAt:   &completedAt,
		Status:        types.StatusCompleted,
		ModuleResults: results,
		Decision:      &decision,
		TotalTokens:   totalTokens,
		TotalCostUsd:  float64(totalTokens) * 0.000003,
		DurationMs:    time.Since(startedAt).Milliseconds(),
	}
	o.LastRun = &run
	if o.deps.OnComplete != nil {
		o.deps.OnComplete(run)
	}
	return run, nil
}

// decide implements the L3 short-circuit + CRO + L4 decision logic.
func (o *Orchestrator) decide(ctx context.Context, instrumentKey string, reg types.RegimeSignal, s types.SynthesisOutput, price float64) types.TradeDecision {
	// Short-circuit: consensus too low → PASS without CRO.
	if s.ModulesAgreeing < 3 || s.AggregatedSignal == types.SignalNeutral {
		return types.TradeDecision{
			Action:           types.ActionPass,
			InstrumentKey:    instrumentKey,
			Confidence:       s.WeightedConviction,
			ModulesAgreeing:  s.ModulesAgreeing,
			ModulesTotal:     s.ModulesTotal,
			SurvivedCRO:      false,
			CROObjections:    []string{},
			ReflexivityFlags: []string{},
			Reasoning:        fmt.Sprintf("共振不足 (%d/%d)，观望", s.ModulesAgreeing, s.ModulesTotal),
		}
	}

	cro := o.cro.Review(ctx, types.CROInput{
		Synthesis:      s,
		Regime:         reg,
		InstrumentKey:  instrumentKey,
		CurrentPrice:   price,
		FundingRate:    o.deps.GetFundingRate(instrumentKey),
		LongShortRatio: o.deps.GetLongShortRatio(instrumentKey),
		OIDelta:        o.deps.GetOIDelta(instrumentKey),
	})

	if !cro.Approved {
		return types.TradeDecision{
			Action:           types.ActionPass,
			InstrumentKey:    instrumentKey,
			Entry:            s.ConsensusEntry,
			StopLoss:         s.ConsensusSL,
			TakeProfit:       s.ConsensusTP,
			Confidence:       cro.AdjustedConviction,
			ModulesAgreeing:  s.ModulesAgreeing,
			ModulesTotal:     s.ModulesTotal,
			SurvivedCRO:      false,
			CROObjections:    cro.Objections,
			ReflexivityFlags: cro.ReflexivityFlags,
			Reasoning:        "CRO rejected: " + cro.Reasoning,
		}
	}

	// ── LAYER 4: final decision ──
	action := types.ActionOpenLong
	if s.AggregatedSignal == types.SignalShort {
		action = types.ActionOpenShort
	}
	rr := calcRR(s.ConsensusEntry, s.ConsensusSL, s.ConsensusTP)
	posSize := calcPositionSize(s.ModulesAgreeing)

	d := types.TradeDecision{
		Action:           action,
		InstrumentKey:    instrumentKey,
		Entry:            s.ConsensusEntry,
		StopLoss:         s.ConsensusSL,
		TakeProfit:       s.ConsensusTP,
		PositionSizePct:  &posSize,
		RiskRewardRatio:  rr,
		Confidence:       cro.AdjustedConviction,
		ModulesAgreeing:  s.ModulesAgreeing,
		ModulesTotal:     s.ModulesTotal,
		SurvivedCRO:      true,
		CROObjections:    cro.Objections,
		ReflexivityFlags: cro.ReflexivityFlags,
		Reasoning:        s.Reasoning,
	}

	// Final R:R gate.
	if rr != nil && *rr < 1.5 {
		d.Action = types.ActionPass
		d.Reasoning = fmt.Sprintf("R:R %.1f < 1.5, 不满足最低风报比", *rr)
	}
	return d
}

func calcRR(entry, sl, tp *float64) *float64 {
	if entry == nil || sl == nil || tp == nil || *entry == 0 || *sl == 0 || *tp == 0 {
		return nil
	}
	risk := abs(*entry - *sl)
	reward := abs(*tp - *entry)
	if risk == 0 {
		return nil
	}
	v := round1(reward / risk)
	return &v
}

func calcPositionSize(agreeing int) float64 {
	// High consensus = 2% risk, medium = 1%.
	if agreeing >= 4 {
		return 2.0
	}
	if agreeing >= 3 {
		return 1.0
	}
	return 0.5
}

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

func round1(v float64) float64 {
	return math.Round(v*10) / 10
}
