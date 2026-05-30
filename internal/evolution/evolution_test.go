package evolution

import (
	"math"
	"testing"
	"time"

	"trader-evolver/internal/types"
)

func f(v float64) *float64 { return &v }

// fakeStore is an in-memory Store for testing pure evolution logic.
type fakeStore struct {
	weights map[string]types.DarwinWeightEntry
	recs    []types.Recommendation
	nextID  int64
}

func newFakeStore() *fakeStore {
	w := map[string]types.DarwinWeightEntry{}
	for _, id := range types.DefaultModuleIDs {
		w[id] = types.DarwinWeightEntry{ModuleID: id, Weight: 1.0}
	}
	return &fakeStore{weights: w}
}

func (s *fakeStore) GetDarwinWeights() []types.DarwinWeightEntry {
	out := []types.DarwinWeightEntry{}
	for _, id := range types.DefaultModuleIDs {
		out = append(out, s.weights[id])
	}
	return out
}
func (s *fakeStore) UpdateDarwinWeight(id string, w float64, sharpe, hit *float64) {
	s.weights[id] = types.DarwinWeightEntry{ModuleID: id, Weight: w, Sharpe30d: sharpe, HitRate30d: hit}
}
func (s *fakeStore) InsertRecommendation(r types.Recommendation) {
	s.nextID++
	r.ID = s.nextID
	s.recs = append(s.recs, r)
}
func (s *fakeStore) GetModuleRecommendations(moduleID string, days int) []types.Recommendation {
	out := []types.Recommendation{}
	for _, r := range s.recs {
		if r.ModuleID == moduleID {
			out = append(out, r)
		}
	}
	return out
}
func (s *fakeStore) GetUnfilledRecommendations(field string, limit int) []types.Recommendation {
	out := []types.Recommendation{}
	for _, r := range s.recs {
		var missing bool
		switch field {
		case "return_1d":
			missing = r.Return1d == nil
		case "return_5d":
			missing = r.Return5d == nil
		case "return_20d":
			missing = r.Return20d == nil
		}
		if missing {
			out = append(out, r)
		}
	}
	return out
}
func (s *fakeStore) UpdateReturn(id int64, field string, value float64) {
	for i := range s.recs {
		if s.recs[i].ID == id {
			switch field {
			case "return_1d":
				s.recs[i].Return1d = &value
			case "return_5d":
				s.recs[i].Return5d = &value
			case "return_20d":
				s.recs[i].Return20d = &value
			}
		}
	}
}

func TestComputeSharpeAndHitRate(t *testing.T) {
	// 3 LONG recs, all positive 5d returns, conviction 100.
	recs := []types.Recommendation{
		{Signal: types.SignalLong, Conviction: 100, Return5d: f(0.01)},
		{Signal: types.SignalLong, Conviction: 100, Return5d: f(0.02)},
		{Signal: types.SignalLong, Conviction: 100, Return5d: f(0.03)},
	}
	sh := computeSharpe(recs)
	if sh <= 0 {
		t.Fatalf("expected positive sharpe, got %v", sh)
	}
	if hr := computeHitRate(recs); hr != 1.0 {
		t.Fatalf("expected hit rate 1.0, got %v", hr)
	}
	// Fewer than 3 -> sharpe 0
	if computeSharpe(recs[:2]) != 0 {
		t.Fatal("expected 0 sharpe for <3 recs")
	}
	// SHORT with positive return = wrong direction.
	short := []types.Recommendation{{Signal: types.SignalShort, Conviction: 100, Return5d: f(0.05)}}
	if computeHitRate(short) != 0 {
		t.Fatal("short with positive ret should be a miss")
	}
}

func TestDarwinUpdateQuartiles(t *testing.T) {
	s := newFakeStore()
	// Give each module 3 recs so they're scored. Vary returns so Sharpe differs.
	// ict best, fundamental worst.
	rets := map[string][]float64{
		"ict_trader":          {0.05, 0.06, 0.05},
		"chanlun_analyst":     {0.02, 0.03, 0.02},
		"wave_analyst":        {0.00, 0.01, 0.00},
		"indicator_analyst":   {-0.01, 0.0, -0.01},
		"fundamental_analyst": {-0.05, -0.06, -0.05},
	}
	for id, rs := range rets {
		for _, r := range rs {
			rec := types.Recommendation{ModuleID: id, Signal: types.SignalLong, Conviction: 100, Return5d: f(r)}
			s.InsertRecommendation(rec)
		}
	}
	u := NewDarwinWeightUpdater(s)
	changes := u.Update(0)
	if len(changes) != 5 {
		t.Fatalf("expected 5 changes, got %d", len(changes))
	}
	// Top module weight should grow (>1.0), bottom should decay (<1.0).
	if w := s.weights["ict_trader"].Weight; w <= 1.0 {
		t.Errorf("top module ict should grow, got %v", w)
	}
	if w := s.weights["fundamental_analyst"].Weight; w >= 1.0 {
		t.Errorf("bottom module should decay, got %v", w)
	}
	// quarter = ceil(5/4) = 2 → top2 grow, bottom2 decay.
	if w := s.weights["ict_trader"].Weight; math.Abs(w-1.05) > 1e-9 {
		t.Errorf("expected 1.05, got %v", w)
	}
}

func TestBackfillReturnsTimeTravel(t *testing.T) {
	s := newFakeStore()
	base := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	s.InsertRecommendation(types.Recommendation{
		ModuleID: "ict_trader", InstrumentKey: "X", Signal: types.SignalLong,
		Conviction: 100, PriceAtRecommendation: 100, RecommendedAt: base.Format(time.RFC3339),
	})
	tr := NewRecommendationTracker(s)
	// Now = base + 6 days, price = 110 → return_1d and return_5d should fill, 20d not.
	now := base.Add(6 * 24 * time.Hour)
	getPrice := func(string) *float64 { return f(110) }
	n := tr.BackfillReturns(getPrice, now)
	if n != 2 {
		t.Fatalf("expected 2 fills (1d+5d), got %d", n)
	}
	if s.recs[0].Return5d == nil || math.Abs(*s.recs[0].Return5d-0.1) > 1e-9 {
		t.Fatalf("expected return5d=0.1, got %v", s.recs[0].Return5d)
	}
	if s.recs[0].Return20d != nil {
		t.Fatal("return20d should still be unfilled at 6 days")
	}
}
