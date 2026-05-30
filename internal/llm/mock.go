package llm

import (
	"context"
	"encoding/json"
	"hash/fnv"
	"strings"
)

// MockProvider returns deterministic, rule-based JSON without any network call.
// It lets the full pipeline + backtest run offline and produces module signals
// that vary by module + prompt content, so Darwin weights can evolve during a
// backtest. Output shape matches what module_runner / CRO parse.
type MockProvider struct {
	// CallFn, if set, fully overrides behavior (handy for targeted tests).
	CallFn func(ctx context.Context, system, user string) (string, int, error)
}

func NewMockProvider() *MockProvider { return &MockProvider{} }

// Name implements Provider.
func (m *MockProvider) Name() string { return "mock" }

func (m *MockProvider) Call(ctx context.Context, system, user string) (string, int, error) {
	if m.CallFn != nil {
		return m.CallFn(ctx, system, user)
	}
	// CRO prompt? (risk officer persona / decision review). Approve by default.
	if strings.Contains(user, "候选交易决策") {
		out := map[string]any{
			"approved":           true,
			"objections":         []string{},
			"reflexivityFlags":   []string{},
			"riskLevel":          "MEDIUM",
			"adjustedConviction": 60,
			"reasoning":          "mock CRO: no blocking objections",
		}
		b, _ := json.Marshal(out)
		return string(b), 10, nil
	}

	// Analysis module: derive a deterministic signal from system+user hash so
	// different modules / market states yield different but stable outputs.
	h := fnv.New32a()
	_, _ = h.Write([]byte(system))
	_, _ = h.Write([]byte(user))
	seed := h.Sum32()

	var signal string
	switch seed % 3 {
	case 0:
		signal = "LONG"
	case 1:
		signal = "SHORT"
	default:
		signal = "NEUTRAL"
	}
	conviction := 40 + int(seed%50) // 40-89

	out := map[string]any{
		"moduleId":   "mock",
		"signal":     signal,
		"conviction": conviction,
		"entry":      nil,
		"stopLoss":   nil,
		"takeProfit": nil,
		"keyLevels":  map[string]any{"support": []float64{}, "resistance": []float64{}},
		"reasoning":  "mock deterministic output",
	}
	b, _ := json.Marshal(out)
	return string(b), 10, nil
}
