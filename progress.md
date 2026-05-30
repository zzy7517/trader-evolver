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

- [x] B2. module_runner → internal/modules (Provider call, JSON parse w/ code-fence strip,
      signal/conviction validation+clamp, LLM-err & parse-fail both fall back to neutral+Error). tests passing.
- [x] B3. adversarial CRO → internal/modules/adversarial.go (ComposeCRO → Provider.Call → parse
      CROOutput; LLM error AND parse failure both fail-safe to Approved=false/RiskLevel=HIGH;
      toBool/toStringSlice/validateRiskLevel/formatThousands helpers; shared toNumber/toStr). tests passing.

- [x] B4. orchestrator → internal/orchestrator (Deps struct of fn fields like tradex OrchestratorDeps;
      L1 regime → L2 5 modules via goroutines+WaitGroup → L3 synth + CRO short-circuit (<3 agree or
      NEUTRAL = PASS) → L4 decision w/ calcRR + posSize + R:R<1.5 gate; running guard; OnComplete;
      uuid for run id). End-to-end MockProvider test passing. STAGE B COMPLETE.
      New dep: github.com/google/uuid v1.6.0.

- [x] C1. internal/store: pure-Go SQLite (modernc.org/sqlite v1.34.4, no CGO, WAL+busy_timeout).
      Schema: candles (PK instrument_key+interval+open_time_ms, WITHOUT ROWID, idempotent upsert),
      daily_macro (PK series+date_ms, for VIX/DXY/SPX), feargreed (PK date_ms). API:
      Open/Close/DB; UpsertCandles/GetCandles(range)/CandleCoverage/LatestCandleTime;
      UpsertDailyMacro/GetDailyMacro/MacroAsOf(<=t); UpsertFearGreed/FearGreedAsOf(<=t)/FearGreedCount.
      As-of lookups (most-recent <= t) feed backtest regime reconstruction (D1). Added types.Candle/
      DailyMacro/FearGreed/Coverage. Tests cover upsert idempotency, range, coverage, as-of, isolation.
      NOTE: repaired a truncated/unterminated store.go from a prior turn (HEAD c9e48d9 did not compile).
      NOTE: a concurrent turn's half-finished C2 collectors (wrong store API) parked under .ralph-wip/.

## Next
- [ ] C2. internal/collectors: Binance /fapi/v1/klines paginated multi-year fetch (rate-limit+retry)
      -> store.UpsertCandles([]types.Candle) for BTC/ETH/SOL. Reuse paging logic in .ralph-wip/.

## Stage tracker
- Stage A (skeleton + pure logic): A1✅ A2✅ A3✅ A4✅ A5✅ A6✅
- Stage B (LLM + modules): B1✅ B2✅ B3✅ B4✅
- Stage C (collectors + store): C1✅ C2 C3 C4 C5
- Stage D (backtest engine): D1 D2 D3 D4

## Reflection (iter 9)
- Done: Stage A (6/6) + Stage B (4/4) + C1. Core logic, LLM layer, orchestrator, SQLite store all
  ported + tested; 8 commits; full suite green.
- Working: layered port order builds on tested foundations; MockProvider runs pipeline offline;
  1:1 fidelity via per-file `git show` checks.
- Friction (resolved): edit churn in internal/modules iter6 (helper sigs shifted); fixed by reverting
  to committed state. Detached-HEAD source reads handled via `git show feat/multi-agent-evolution:...`.
- Adjustment: none. C2-D4 are net-new code (collectors + backtest), lower fidelity risk. Keep network
  calls behind interfaces / injectable HTTP base URLs so tests stay offline.
- Next: C2 Binance -> C3 Yahoo -> C4 F&G -> C5 collect CLI -> Stage D (the payoff: prove Darwin
  weights evolve over historical replay).

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
