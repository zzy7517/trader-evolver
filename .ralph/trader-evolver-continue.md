# Continue trader-evolver Project

Complete the remaining stages of the trader-evolver project, a Go rewrite of atlas-gic's 
multi-agent trading pipeline with historical backtesting capabilities.

## Current State
- Stage A (skeleton + pure logic): COMPLETE (A1-A6)
- Stage B (LLM + modules): COMPLETE (B1-B4)
- Stage C (collectors + store): C1✅ C2✅, C3-C5 remaining
- Stage D (backtest engine): Not started

## Goals
1. Complete Stage C collectors (Yahoo Finance, Fear&Greed, CLI)
2. Build Stage D backtest engine (the core payoff — historical replay + Darwin evolution)
3. Add atlas-gic features: JANUS meta-layer, autoresearch loop, reflexivity signals
4. Wire up CLI commands (collect, backtest, report)

## Checklist

### Stage C - Collectors
- [x] C3: Yahoo Finance daily collector (stocks/indices/VIX/DXY/commodities) → store.UpsertDailyMacro + UpsertCandles
- [x] C4: Fear & Greed Index collector (alternative.me, full history) → store.UpsertFearGreed  
- [x] C5: `evolver collect` CLI command — orchestrate all collectors with config

### Stage D - Backtest Engine  
- [x] D1: Backtest time-travel engine — iterate historical days, reconstruct regime from store as-of lookups
- [x] D2: Forward-return backfill — after each simulated day, fill Return1d/5d/20d from actual prices (integrated in engine.go)
- [x] D3: Darwin weight evolution loop — score agents by rolling Sharpe, apply 1.05/0.95 daily updates (integrated in engine.go)
- [ ] D4: Autoresearch integration — identify worst agent, generate prompt mod, 5-day eval, keep/revert

### Stage E - Advanced Features (from atlas-gic)
- [ ] E1: JANUS meta-layer — multi-cohort blending with emergent regime detection
- [ ] E2: Reflexivity engine — model feedback loops (price→fundamentals, P&L→behavior, etc.)
- [ ] E3: Report generation — equity curve, agent weights over time, modification log

### Stage F - CLI & Integration
- [ ] F1: `evolver backtest` command — run full historical replay
- [ ] F2: `evolver report` command — generate backtest summaries
- [ ] F3: End-to-end integration test with mock data

## Verification
- All tests passing: `go test ./...`
- Each new package has corresponding _test.go
- Code compiles cleanly with no warnings

## Notes
- Go 1.24.1, module: trader-evolver
- SQLite via modernc.org/sqlite (no CGO)
- LLM: Codex provider only (SSE Responses API)
- Backtest indicators with no history (funding/OI/LS) → null → neutral fallback
- Reference: /data/zzy/atlas-gic for architecture patterns
- PATH: export PATH="/usr/local/go/bin:$PATH"
