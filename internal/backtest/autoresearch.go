// autoresearch.go implements the self-improving loop:
//
//  1. Identify worst-performing agent by rolling Sharpe
//  2. Generate ONE targeted prompt modification (via LLM)
//  3. Run for 5 trading days with modified prompt
//  4. Check if agent's Sharpe improved
//  5. Keep (commit) or revert
//
// This is the Go port of atlas-gic's autoresearch.py loop.
// The key insight: agent prompts ARE the weights being optimised,
// Sharpe ratio IS the loss function, git IS the optimizer.
package backtest

import (
	"context"
	"fmt"
	"time"

	"trader-evolver/internal/llm"
	"trader-evolver/internal/types"
)

// AutoresearchConfig configures the autoresearch loop.
type AutoresearchConfig struct {
	// EvalWindowDays: how many days to run before evaluating a modification.
	EvalWindowDays int
	// CooldownDays: minimum days between modifications to the same agent.
	CooldownDays int
	// MinSharpeImprovement: minimum Sharpe improvement to keep a modification.
	MinSharpeImprovement float64
	// MaxModifications: stop after this many total modifications attempted.
	MaxModifications int
}

// DefaultAutoresearchConfig returns sensible defaults matching atlas-gic.
func DefaultAutoresearchConfig() AutoresearchConfig {
	return AutoresearchConfig{
		EvalWindowDays:       5,
		CooldownDays:         5,
		MinSharpeImprovement: 0.0, // any improvement counts
		MaxModifications:     100,
	}
}

// PromptModification records one autoresearch attempt.
type PromptModification struct {
	Day          int
	Date         string
	ModuleID     string
	Description  string
	BeforeSharpe float64
	AfterSharpe  float64
	Kept         bool
}

// AutoresearchState tracks the autoresearch loop state within a backtest.
type AutoresearchState struct {
	Config          AutoresearchConfig
	Modifications   []PromptModification
	LastModifiedDay map[string]int // moduleID → last modification day
	Provider        llm.Provider
}

// NewAutoresearchState creates initial state.
func NewAutoresearchState(cfg AutoresearchConfig, provider llm.Provider) *AutoresearchState {
	return &AutoresearchState{
		Config:          cfg,
		LastModifiedDay: make(map[string]int),
		Provider:        provider,
	}
}

// ShouldTrigger returns true if autoresearch should run on this day.
// Triggers every EvalWindowDays and after cooldown periods.
func (a *AutoresearchState) ShouldTrigger(dayNum int) bool {
	if len(a.Modifications) >= a.Config.MaxModifications {
		return false
	}
	return dayNum > 0 && dayNum%a.Config.EvalWindowDays == 0
}

// FindWorstAgent identifies the module with the lowest rolling Sharpe.
// Returns the moduleID and its Sharpe, or ("", 0) if none qualifies.
func (a *AutoresearchState) FindWorstAgent(dayNum int, recs []types.Recommendation) (string, float64) {
	// Calculate Sharpe for each module
	sharpes := make(map[string]float64)
	for _, id := range types.DefaultModuleIDs {
		// Check cooldown
		if lastDay, ok := a.LastModifiedDay[id]; ok {
			if dayNum-lastDay < a.Config.CooldownDays {
				continue // still in cooldown
			}
		}
		s := calcSharpeForModule(id, recs)
		sharpes[id] = s
	}

	if len(sharpes) == 0 {
		return "", 0
	}

	// Find the worst
	worstID := ""
	worstSharpe := 999.0
	for id, s := range sharpes {
		if s < worstSharpe {
			worstSharpe = s
			worstID = id
		}
	}
	return worstID, worstSharpe
}

// GenerateModification asks the LLM to propose a targeted prompt modification
// for the worst-performing agent.
func (a *AutoresearchState) GenerateModification(ctx context.Context, moduleID string, recs []types.Recommendation) (string, error) {
	// Collect recent recommendations for this module
	var recentRecs []string
	count := 0
	for i := len(recs) - 1; i >= 0 && count < 10; i-- {
		r := recs[i]
		if r.ModuleID != moduleID {
			continue
		}
		ret5d := "pending"
		if r.Return5d != nil {
			ret5d = fmt.Sprintf("%.2f%%", *r.Return5d*100)
		}
		recentRecs = append(recentRecs, fmt.Sprintf(
			"  %s: %s %s conv=%0.f → 5d return: %s",
			r.RecommendedAt, r.Signal, r.InstrumentKey, r.Conviction, ret5d,
		))
		count++
	}

	system := `You are an autoresearch agent for a trading system. Your job is to analyze 
a trading module's recent performance and propose ONE specific, targeted modification 
to its analysis prompt that would improve its Sharpe ratio.

Rules:
- Propose exactly ONE change (not multiple)
- The change should address a specific pattern of failure
- Be concrete: "Add momentum filter to prevent longs during sector weakness"
- NOT vague: "Be more careful" or "Improve accuracy"
- Return ONLY the modification description as a single sentence.`

	user := fmt.Sprintf(`Module: %s

Recent recommendations and outcomes:
%s

This module has the worst Sharpe ratio among all modules. 
Analyze the pattern of failures and propose ONE targeted prompt modification.

Respond with a single sentence describing the modification.`,
		moduleID, joinStrings(recentRecs))

	content, _, err := a.Provider.Call(ctx, system, user)
	if err != nil {
		return fmt.Sprintf("Add validation filter to %s recommendations", moduleID), nil
	}
	if content == "" {
		return fmt.Sprintf("Add momentum confirmation for %s signals", moduleID), nil
	}
	return content, nil
}

// EvaluateModification compares Sharpe before and after a modification period.
func (a *AutoresearchState) EvaluateModification(
	dayNum int,
	dateStr string,
	moduleID string,
	beforeSharpe float64,
	afterSharpe float64,
) PromptModification {
	kept := afterSharpe > beforeSharpe+a.Config.MinSharpeImprovement

	mod := PromptModification{
		Day:          dayNum,
		Date:         dateStr,
		ModuleID:     moduleID,
		BeforeSharpe: beforeSharpe,
		AfterSharpe:  afterSharpe,
		Kept:         kept,
	}

	a.Modifications = append(a.Modifications, mod)
	a.LastModifiedDay[moduleID] = dayNum

	return mod
}

// Stats returns summary statistics.
func (a *AutoresearchState) Stats() (total, kept, reverted int) {
	total = len(a.Modifications)
	for _, m := range a.Modifications {
		if m.Kept {
			kept++
		} else {
			reverted++
		}
	}
	return total, kept, total - kept
}

// ─── helpers ───

func calcSharpeForModule(moduleID string, recs []types.Recommendation) float64 {
	var returns []float64
	for _, rec := range recs {
		if rec.ModuleID != moduleID || rec.Return5d == nil {
			continue
		}
		convW := rec.Conviction / 100.0
		ret := *rec.Return5d * convW
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
		d := r - mean
		variance += d * d
	}
	variance /= float64(len(returns) - 1)

	if variance <= 0 {
		return 0
	}
	stdDev := sqrt(variance)
	return mean / stdDev
}

func joinStrings(ss []string) string {
	result := ""
	for i, s := range ss {
		if i > 0 {
			result += "\n"
		}
		result += s
	}
	return result
}

// Ensure time is used (for potential future use in date formatting)
var _ = time.Now
