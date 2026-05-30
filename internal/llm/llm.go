// Package llm provides a minimal single-call LLM abstraction for trader-evolver.
//
// Unlike tradex's agent loop, modules here only need a single, stateless call:
//
//	content, tokens, err := provider.Call(ctx, systemPrompt, userPrompt)
//
// JSON-out, no tools, no multi-turn. Two implementations are provided:
//   - CodexProvider: reads ~/.codex/auth.json (or CODEX_API_KEY) and calls the
//     OpenAI Codex Responses API once over SSE.
//   - MockProvider: deterministic rule-based stub for offline tests/backtests.
package llm

import "context"

// Provider is the single seam every module uses to talk to an LLM.
//
// Call performs one stateless request and returns the assistant text content,
// the total token count (input+output) when available, and an error.
// Implementations MUST be safe for concurrent use (modules run in parallel).
type Provider interface {
	Call(ctx context.Context, systemPrompt, userPrompt string) (content string, tokens int, err error)
	// Name returns a short identifier for logging/reporting.
	Name() string
}
