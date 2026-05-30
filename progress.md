# trader-evolver migration progress

## Done
- [x] A1. go mod init + dir skeleton + README + prompts copied
- [x] A2. types ported (internal/types/types.go) — build OK
- [x] A3. regime_detector → internal/regime (+ tests passing)
- [x] A4. synthesizer → internal/synth (+ tests passing)
- [x] A5. evolution: darwin_weights + scorecard + recommendation_tracker (+ tests passing)
- [x] A6. prompt_composer → internal/modules (+ tests passing) — STAGE A COMPLETE

- [x] B1. internal/llm: Codex provider (SSE Responses API, retry/backoff, ~/.codex/auth.json or
      CODEX_API_KEY, JWT account-id) + deterministic MockProvider (+ tests passing).
      Provider iface: Call(ctx, system, user) (content string, tokens int, err error) + Name().

## Next
- [ ] B2. module_runner → internal/modules (call Provider, parse JSON, fallback neutral on parse fail)

## Stage tracker
- Stage A (skeleton + pure logic): A1✅ A2✅ A3✅ A4✅ A5✅ A6✅
- Stage B (LLM + modules): B1✅ B2 B3 B4
- Stage C (collectors + store): C1 C2 C3 C4 C5
- Stage D (backtest engine): D1 D2 D3 D4

## Notes
- IMPORTANT: the mytradebot worktree HEAD is detached at origin/main, which does NOT
  contain tradex/pipeline|evolution|data_feeds. Read source via:
    git -C /Users/zhongyuanzhang/mytradebot show feat/multi-agent-evolution:tradex/pipeline/<file>.ts
  (branch feat/multi-agent-evolution holds the pipeline/evolution code). The `read` tool
  on those paths will fail; use `git show` through bash instead.
- Source of truth: /Users/zhongyuanzhang/mytradebot/tradex (read-only, via git show on the branch)
- Codex provider only; no agent loop. Module call = single LLM Call(ctx, system, user).
- Backtest sets funding/OI/LS to null → graceful neutral degradation.
- Go 1.24.1, module name: trader-evolver
