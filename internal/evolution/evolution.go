// Package evolution ports tradex/evolution scorecard, darwin_weights, and
// recommendation_tracker. The persistence layer is abstracted behind Store so
// the pure logic is testable without SQLite (the concrete SQLite Store lands in
// internal/store).
package evolution

import (
	"math"
	"sort"
	"time"

	"trader-evolver/internal/types"
)

// Store is the persistence surface the evolution logic needs.
// Method set mirrors tradex/evolution/store.ts.
type Store interface {
	GetDarwinWeights() []types.DarwinWeightEntry
	UpdateDarwinWeight(moduleID string, weight float64, sharpe, hitRate *float64)
	InsertRecommendation(rec types.Recommendation)
	// GetModuleRecommendations returns recs for a module within the last `days`.
	GetModuleRecommendations(moduleID string, days int) []types.Recommendation
	// GetUnfilledRecommendations returns recs missing the given return field.
	// field is one of "return_1d" | "return_5d" | "return_20d".
	GetUnfilledRecommendations(field string, limit int) []types.Recommendation
	UpdateReturn(id int64, field string, value float64)
}

// ----------------------------------------------------------------------------
// Scorecard (scorecard.ts)
// ----------------------------------------------------------------------------

type Scorecard struct{ store Store }

func NewScorecard(s Store) *Scorecard { return &Scorecard{store: s} }

// ComputeAll computes scores for all default modules over `days`.
func (sc *Scorecard) ComputeAll(days int) []types.ModuleScore {
	weights := sc.store.GetDarwinWeights()
	weightMap := make(map[string]types.DarwinWeightEntry, len(weights))
	for _, w := range weights {
		weightMap[w.ModuleID] = w
	}

	now := time.Now().UTC().Format(time.RFC3339)
	out := make([]types.ModuleScore, 0, len(types.DefaultModuleIDs))
	for _, moduleID := range types.DefaultModuleIDs {
		recs := sc.store.GetModuleRecommendations(moduleID, days)
		scored := make([]types.Recommendation, 0, len(recs))
		for _, r := range recs {
			if r.Return5d != nil && r.Signal != types.SignalNeutral {
				scored = append(scored, r)
			}
		}
		weight := types.DefaultDarwinWeight
		if e, ok := weightMap[moduleID]; ok {
			weight = e.Weight
		}
		out = append(out, types.ModuleScore{
			ModuleID:             moduleID,
			DarwinWeight:         weight,
			Sharpe30d:            computeSharpe(scored),
			HitRate30d:           computeHitRate(scored),
			TotalRecommendations: len(recs),
			LastModifiedAt:       nil,
			UpdatedAt:            now,
		})
	}
	return out
}

// computeSharpe: Sharpe of conviction-weighted, direction-adjusted 5d returns.
func computeSharpe(recs []types.Recommendation) float64 {
	if len(recs) < 3 {
		return 0
	}
	returns := make([]float64, len(recs))
	for i, r := range recs {
		ret := *r.Return5d
		weight := r.Conviction / 100
		if r.Signal == types.SignalShort {
			ret = -ret
		}
		returns[i] = ret * weight
	}
	mean := 0.0
	for _, v := range returns {
		mean += v
	}
	mean /= float64(len(returns))
	variance := 0.0
	for _, v := range returns {
		variance += (v - mean) * (v - mean)
	}
	variance /= float64(len(returns) - 1)
	stdDev := math.Sqrt(variance)
	if stdDev == 0 {
		return 0
	}
	return (mean / stdDev) * math.Sqrt(252)
}

// computeHitRate: fraction of correct-direction predictions.
func computeHitRate(recs []types.Recommendation) float64 {
	if len(recs) == 0 {
		return 0
	}
	hits := 0
	for _, r := range recs {
		ret := *r.Return5d
		if r.Signal == types.SignalLong && ret > 0 {
			hits++
		} else if r.Signal == types.SignalShort && ret < 0 {
			hits++
		}
	}
	return float64(hits) / float64(len(recs))
}

// ----------------------------------------------------------------------------
// Darwin weight updater (darwin_weights.ts)
// ----------------------------------------------------------------------------

type DarwinWeightUpdater struct {
	store     Store
	scorecard *Scorecard
}

func NewDarwinWeightUpdater(s Store) *DarwinWeightUpdater {
	return &DarwinWeightUpdater{store: s, scorecard: NewScorecard(s)}
}

// WeightChange reports one module's weight transition.
type WeightChange struct {
	ModuleID  string
	OldWeight float64
	NewWeight float64
	Sharpe    float64
}

// Update runs one daily-style weight update; returns the changes applied.
func (u *DarwinWeightUpdater) Update(minRecommendationsForEval int) []WeightChange {
	scores := u.scorecard.ComputeAll(30)
	changes := []WeightChange{}

	eligible := make([]types.ModuleScore, 0, len(scores))
	for _, s := range scores {
		if s.TotalRecommendations >= minRecommendationsForEval {
			eligible = append(eligible, s)
		}
	}
	if len(eligible) < 2 {
		return changes
	}

	// Sort by Sharpe descending.
	sorted := append([]types.ModuleScore(nil), eligible...)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].Sharpe30d > sorted[j].Sharpe30d
	})
	quarter := int(math.Max(1, math.Ceil(float64(len(sorted))/4)))

	topIDs := map[string]bool{}
	for _, s := range sorted[:quarter] {
		topIDs[s.ModuleID] = true
	}
	bottomIDs := map[string]bool{}
	for _, s := range sorted[len(sorted)-quarter:] {
		bottomIDs[s.ModuleID] = true
	}

	for _, score := range eligible {
		oldWeight := score.DarwinWeight
		newWeight := oldWeight
		if topIDs[score.ModuleID] {
			newWeight = math.Min(types.MaxDarwinWeight, oldWeight*types.WeightGrowthFactor)
		} else if bottomIDs[score.ModuleID] {
			newWeight = math.Max(types.MinDarwinWeight, oldWeight*types.WeightDecayFactor)
		}
		newWeight = math.Round(newWeight*1000) / 1000

		sharpe := score.Sharpe30d
		hitRate := score.HitRate30d
		u.store.UpdateDarwinWeight(score.ModuleID, newWeight, &sharpe, &hitRate)
		changes = append(changes, WeightChange{
			ModuleID: score.ModuleID, OldWeight: oldWeight, NewWeight: newWeight, Sharpe: sharpe,
		})
	}
	return changes
}

// ----------------------------------------------------------------------------
// Recommendation tracker (recommendation_tracker.ts)
// ----------------------------------------------------------------------------

type RecommendationTracker struct{ store Store }

func NewRecommendationTracker(s Store) *RecommendationTracker {
	return &RecommendationTracker{store: s}
}

// RecordFromPipelineRun records all non-errored module outputs from a run.
func (t *RecommendationTracker) RecordFromPipelineRun(results []types.ModuleRunResult, instrumentKey string, currentPrice float64) {
	now := time.Now().UTC().Format(time.RFC3339)
	for _, r := range results {
		if r.Error != nil {
			continue
		}
		t.store.InsertRecommendation(types.Recommendation{
			ModuleID:              r.ModuleID,
			InstrumentKey:         instrumentKey,
			Signal:                r.Output.Signal,
			Conviction:            r.Output.Conviction,
			PriceAtRecommendation: currentPrice,
			RecommendedAt:         now,
		})
	}
}

// PriceAtFn resolves a current/forward price for an instrument; returns nil if unknown.
type PriceAtFn func(instrumentKey string) *float64

// BackfillReturns fills forward returns for recommendations old enough.
// `now` lets the backtest engine drive time-travel; in live use pass time.Now().
func (t *RecommendationTracker) BackfillReturns(getPrice PriceAtFn, now time.Time) int {
	filled := 0
	type window struct {
		field  string
		maxAge time.Duration
	}
	windows := []window{
		{"return_1d", 24 * time.Hour},
		{"return_5d", 5 * 24 * time.Hour},
		{"return_20d", 20 * 24 * time.Hour},
	}
	for _, w := range windows {
		recs := t.store.GetUnfilledRecommendations(w.field, 200)
		for _, rec := range recs {
			recAt, err := time.Parse(time.RFC3339, rec.RecommendedAt)
			if err != nil {
				continue
			}
			if now.Sub(recAt) < w.maxAge {
				continue
			}
			price := getPrice(rec.InstrumentKey)
			if price == nil {
				continue
			}
			ret := (*price - rec.PriceAtRecommendation) / rec.PriceAtRecommendation
			t.store.UpdateReturn(rec.ID, w.field, ret)
			filled++
		}
	}
	return filled
}
