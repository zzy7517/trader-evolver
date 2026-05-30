// Package janus implements the JANUS meta-weighting layer from atlas-gic.
//
// JANUS sits above multiple agent "cohorts" (trained on different market periods)
// and dynamically weights their recommendations based on recent accuracy.
//
// Key insight: the weight differential between cohorts is an EMERGENT regime detector:
//   - Short-window cohort outperforms → NOVEL REGIME
//   - Long-window cohort outperforms → HISTORICAL REGIME
//   - Roughly equal → MIXED
//
// We didn't build a regime detector. It emerged from tracking which cohort gets things right.
package janus

import (
	"math"

	"trader-evolver/internal/types"
)

// Regime represents the market regime detected by JANUS.
type Regime string

const (
	RegimeNovel      Regime = "NOVEL_REGIME"
	RegimeHistorical Regime = "HISTORICAL_REGIME"
	RegimeMixed      Regime = "MIXED"
)

// Config holds JANUS configuration.
type Config struct {
	// MinWeight: no cohort drops below this influence (default 0.2).
	MinWeight float64
	// MaxWeight: no cohort dominates above this (default 0.8).
	MaxWeight float64
	// RollingWindow: days for rolling accuracy calculation.
	RollingWindow int
	// RegimeThreshold: weight diff threshold for regime signals.
	RegimeThreshold float64
}

// DefaultConfig returns sensible defaults matching atlas-gic's janus.py.
func DefaultConfig() Config {
	return Config{
		MinWeight:       0.2,
		MaxWeight:       0.8,
		RollingWindow:   30,
		RegimeThreshold: 0.15,
	}
}

// Cohort represents one trained agent cohort.
type Cohort struct {
	Name    string
	HitRate float64 // 0-1
	Sharpe  float64
}

// CohortRecommendation is a single recommendation from a cohort.
type CohortRecommendation struct {
	CohortName string
	Ticker     string
	Direction  types.SignalDirection
	Conviction float64 // 0-100
	Agents     []string
}

// BlendedRecommendation is the weighted output from JANUS.
type BlendedRecommendation struct {
	Ticker          string
	Direction       types.SignalDirection
	Conviction      float64 // weighted conviction
	Contested       bool    // cohorts disagree on direction
	CohortBreakdown map[string]CohortView
}

// CohortView shows one cohort's contribution to a blended recommendation.
type CohortView struct {
	Direction  types.SignalDirection
	Conviction float64
	Weight     float64
}

// Output is the daily JANUS result.
type Output struct {
	CohortWeights  map[string]float64
	Regime         Regime
	Blended        []BlendedRecommendation
	ContestedCount int
}

// Layer implements the JANUS meta-weighting algorithm.
type Layer struct {
	Config  Config
	Cohorts []string
	Weights map[string]float64
}

// New creates a JANUS layer with equal initial weights.
func New(cohorts []string, cfg Config) *Layer {
	weights := make(map[string]float64)
	equalWeight := 1.0 / float64(len(cohorts))
	for _, c := range cohorts {
		weights[c] = equalWeight
	}
	return &Layer{
		Config:  cfg,
		Cohorts: cohorts,
		Weights: weights,
	}
}

// UpdateWeights recalculates cohort weights based on recent accuracy metrics.
func (j *Layer) UpdateWeights(metrics map[string]Cohort) {
	if len(metrics) == 0 {
		return
	}

	// Calculate raw scores: 50% hit rate + 50% normalized Sharpe
	rawScores := make(map[string]float64)
	for _, name := range j.Cohorts {
		m, ok := metrics[name]
		if !ok {
			rawScores[name] = 0.5 // default neutral
			continue
		}
		// Normalize Sharpe to roughly 0-1 range
		normSharpe := math.Max(0, math.Min(1, (m.Sharpe+1)/2))
		rawScores[name] = 0.5*m.HitRate + 0.5*normSharpe
	}

	// Apply softmax with constraints
	j.Weights = j.softmaxConstrained(rawScores)
}

// softmaxConstrained applies softmax with min/max weight constraints.
func (j *Layer) softmaxConstrained(scores map[string]float64) map[string]float64 {
	if len(scores) == 0 {
		return j.Weights
	}

	// Find max for numerical stability
	maxScore := -math.MaxFloat64
	for _, v := range scores {
		if v > maxScore {
			maxScore = v
		}
	}

	// Compute exp(score - max) for each cohort
	expScores := make(map[string]float64)
	total := 0.0
	for name, score := range scores {
		exp := math.Exp(score - maxScore)
		expScores[name] = exp
		total += exp
	}

	// Normalize
	weights := make(map[string]float64)
	for name, exp := range expScores {
		weights[name] = exp / total
	}

	// Apply floor constraint
	for name := range weights {
		if weights[name] < j.Config.MinWeight {
			weights[name] = j.Config.MinWeight
		}
	}

	// Renormalize
	total = 0
	for _, w := range weights {
		total += w
	}
	for name := range weights {
		weights[name] /= total
	}

	// Apply ceiling constraint
	for name := range weights {
		if weights[name] > j.Config.MaxWeight {
			weights[name] = j.Config.MaxWeight
		}
	}

	// Final renormalization
	total = 0
	for _, w := range weights {
		total += w
	}
	for name := range weights {
		weights[name] /= total
	}

	return weights
}

// DetectRegime determines the market regime from cohort weight differentials.
// Assumes cohorts are ordered [short-window, long-window].
func (j *Layer) DetectRegime() Regime {
	if len(j.Cohorts) < 2 {
		return RegimeMixed
	}

	shortCohort := j.Cohorts[0]
	longCohort := j.Cohorts[len(j.Cohorts)-1]

	shortWeight := j.Weights[shortCohort]
	longWeight := j.Weights[longCohort]

	diff := shortWeight - longWeight

	if diff > j.Config.RegimeThreshold {
		return RegimeNovel
	} else if diff < -j.Config.RegimeThreshold {
		return RegimeHistorical
	}
	return RegimeMixed
}

// BlendRecommendations merges recommendations from all cohorts using current weights.
func (j *Layer) BlendRecommendations(recs []CohortRecommendation) []BlendedRecommendation {
	// Group by ticker
	type tickerEntry struct {
		longs  []CohortRecommendation
		shorts []CohortRecommendation
	}
	byTicker := make(map[string]*tickerEntry)

	for _, rec := range recs {
		if _, ok := byTicker[rec.Ticker]; !ok {
			byTicker[rec.Ticker] = &tickerEntry{}
		}
		if rec.Direction == types.SignalLong {
			byTicker[rec.Ticker].longs = append(byTicker[rec.Ticker].longs, rec)
		} else if rec.Direction == types.SignalShort {
			byTicker[rec.Ticker].shorts = append(byTicker[rec.Ticker].shorts, rec)
		}
	}

	var blended []BlendedRecommendation

	for ticker, entry := range byTicker {
		// Calculate weighted conviction for each direction
		longWeighted := 0.0
		for _, r := range entry.longs {
			w := j.Weights[r.CohortName]
			longWeighted += r.Conviction * w
		}
		shortWeighted := 0.0
		for _, r := range entry.shorts {
			w := j.Weights[r.CohortName]
			shortWeighted += r.Conviction * w
		}

		contested := len(entry.longs) > 0 && len(entry.shorts) > 0

		var direction types.SignalDirection
		var baseConviction, opposingConviction float64

		if longWeighted >= shortWeighted {
			direction = types.SignalLong
			baseConviction = longWeighted
			opposingConviction = shortWeighted
		} else {
			direction = types.SignalShort
			baseConviction = shortWeighted
			opposingConviction = longWeighted
		}

		// Reduce conviction by disagreement
		finalConviction := baseConviction
		if contested {
			penalty := opposingConviction * 0.5
			finalConviction = math.Max(0, baseConviction-penalty)
		}

		// Build cohort breakdown
		breakdown := make(map[string]CohortView)
		for _, r := range append(entry.longs, entry.shorts...) {
			breakdown[r.CohortName] = CohortView{
				Direction:  r.Direction,
				Conviction: r.Conviction,
				Weight:     j.Weights[r.CohortName],
			}
		}

		blended = append(blended, BlendedRecommendation{
			Ticker:          ticker,
			Direction:       direction,
			Conviction:      finalConviction,
			Contested:       contested,
			CohortBreakdown: breakdown,
		})
	}

	// Sort by conviction descending
	sortBlended(blended)
	return blended
}

// Run executes the full JANUS daily cycle.
func (j *Layer) Run(metrics map[string]Cohort, recs []CohortRecommendation) Output {
	j.UpdateWeights(metrics)

	blended := j.BlendRecommendations(recs)
	regime := j.DetectRegime()

	contested := 0
	for _, b := range blended {
		if b.Contested {
			contested++
		}
	}

	return Output{
		CohortWeights:  copyMap(j.Weights),
		Regime:         regime,
		Blended:        blended,
		ContestedCount: contested,
	}
}

// ─── helpers ───

func copyMap(m map[string]float64) map[string]float64 {
	out := make(map[string]float64, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func sortBlended(s []BlendedRecommendation) {
	// Insertion sort by conviction descending
	for i := 1; i < len(s); i++ {
		key := s[i]
		j := i - 1
		for j >= 0 && s[j].Conviction < key.Conviction {
			s[j+1] = s[j]
			j--
		}
		s[j+1] = key
	}
}
