// Package modules ports tradex/pipeline module_runner, prompt_composer, and
// adversarial (CRO). This file ports prompt_composer.ts.
package modules

import (
	"os"
	"path/filepath"
	"strings"
	"sync"

	"trader-evolver/internal/types"
)

type ComposedPrompt struct {
	SystemPrompt string
	UserPrompt   string
}

type ModulePromptInput struct {
	ModuleID          string
	InstrumentKey     string
	Regime            types.RegimeSignal
	CandleData        string // formatted OHLCV text
	AdditionalContext string // e.g. funding rate, OI for fundamental analyst
}

// PromptComposer assembles system + user prompts from the prompts/ tree.
type PromptComposer struct {
	mu             sync.Mutex
	promptDir      string
	personaCache   map[string]string
	regimeCache    map[string]string
	executionRules string
}

// NewPromptComposer resolves the prompts directory and loads execution rules.
// If promptDir is empty, it auto-discovers a directory named "prompts" starting
// from cwd and walking up parents.
func NewPromptComposer(promptDir string) *PromptComposer {
	dir := promptDir
	if dir == "" {
		dir = discoverPromptDir()
	}
	pc := &PromptComposer{
		promptDir:    dir,
		personaCache: map[string]string{},
		regimeCache:  map[string]string{},
	}
	pc.executionRules = pc.loadFile("meta/execution_rules.md")
	return pc
}

func discoverPromptDir() string {
	cwd, err := os.Getwd()
	if err != nil {
		return "prompts"
	}
	d := cwd
	for {
		cand := filepath.Join(d, "prompts")
		if info, err := os.Stat(cand); err == nil && info.IsDir() {
			return cand
		}
		parent := filepath.Dir(d)
		if parent == d {
			break
		}
		d = parent
	}
	return filepath.Join(cwd, "prompts")
}

// Compose builds the full prompt for a single analysis module.
func (pc *PromptComposer) Compose(in ModulePromptInput) ComposedPrompt {
	persona := pc.getPersona(in.ModuleID)
	regimeRules := pc.getRegimeRules(in.Regime)

	systemPrompt := strings.Join([]string{
		persona, "", "---", "", regimeRules, "", "---", "", pc.executionRules,
	}, "\n")

	return ComposedPrompt{
		SystemPrompt: systemPrompt,
		UserPrompt:   pc.buildUserPrompt(in),
	}
}

// ComposeCRO returns the risk-officer prompt.
func (pc *PromptComposer) ComposeCRO(candidateDecision, context string) ComposedPrompt {
	persona := pc.getPersona("risk_officer")
	return ComposedPrompt{
		SystemPrompt: persona,
		UserPrompt:   "## 候选交易决策\n\n" + candidateDecision + "\n\n## 市场上下文\n\n" + context + "\n\n请按照输出格式审查此决策。",
	}
}

// ComposeSynthesis returns the synthesis prompt (unused by the pure synthesizer,
// kept for parity with tradex).
func (pc *PromptComposer) ComposeSynthesis(moduleOutputs, regimeInfo string) ComposedPrompt {
	synthesis := pc.loadFile("meta/synthesis.md")
	return ComposedPrompt{
		SystemPrompt: synthesis,
		UserPrompt:   "## 当前 Regime\n\n" + regimeInfo + "\n\n## 各模块输出\n\n" + moduleOutputs + "\n\n请综合以上模块输出，给出最终候选决策。",
	}
}

func (pc *PromptComposer) getPersona(moduleID string) string {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	if v, ok := pc.personaCache[moduleID]; ok {
		return v
	}
	content := pc.loadFile("personas/" + moduleID + ".md")
	pc.personaCache[moduleID] = content
	return content
}

func (pc *PromptComposer) getRegimeRules(regime types.RegimeSignal) string {
	key := regimeToFile(regime)
	pc.mu.Lock()
	defer pc.mu.Unlock()
	if v, ok := pc.regimeCache[key]; ok {
		return v
	}
	content := pc.loadFile("regimes/" + key + ".md")
	pc.regimeCache[key] = content
	return content
}

func regimeToFile(regime types.RegimeSignal) string {
	if regime.Volatility == types.VolExtreme || regime.Volatility == types.VolHigh {
		return "volatile"
	}
	if regime.Trend == types.TrendRange {
		return "ranging"
	}
	return "trending"
}

func (pc *PromptComposer) buildUserPrompt(in ModulePromptInput) string {
	parts := []string{
		"## 分析目标: " + in.InstrumentKey,
		"",
		"## 当前 Regime",
		"- Market: " + string(in.Regime.Market),
		"- Volatility: " + string(in.Regime.Volatility),
		"- Trend: " + string(in.Regime.Trend),
		"",
		"## K线数据",
		in.CandleData,
	}
	if in.AdditionalContext != "" {
		parts = append(parts, "", "## 附加数据", in.AdditionalContext)
	}
	parts = append(parts, "", "请严格按照输出格式 (JSON) 给出你的分析结果。只输出JSON，不要其他文字。")
	return strings.Join(parts, "\n")
}

func (pc *PromptComposer) loadFile(relativePath string) string {
	full := filepath.Join(pc.promptDir, filepath.FromSlash(relativePath))
	if b, err := os.ReadFile(full); err == nil {
		return string(b)
	}
	return "[Prompt file not found: " + relativePath + "]"
}

// Reload clears caches and re-reads execution rules (for autoresearch parity).
func (pc *PromptComposer) Reload() {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	pc.personaCache = map[string]string{}
	pc.regimeCache = map[string]string{}
	pc.executionRules = pc.loadFile("meta/execution_rules.md")
}
