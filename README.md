# trader-evolver

A standalone Go project for **historical backtesting and evolution validation** of a
multi-method trading-analysis pipeline. It is the research/backtest sibling of
[`mytradebot`](../../../mytradebot) (`tradex`): it reimplements the `pipeline` +
`evolution` logic in Go and adds the one capability `tradex` lacks — replaying many
years of history to "fast-forward time" and verify whether the regime detection,
multi-module analysis, and Darwinian module-weight evolution actually work.

> This project never modifies `tradex`. The TS logic in `tradex/pipeline`,
> `tradex/evolution`, and `tradex/data_feeds` is the reference being ported.

## Why it exists

`tradex` runs the pipeline **online**: every run records module signals, and forward
returns are filled in only as real time passes (1d / 5d / 20d). Validating the
evolution logic that way takes months. `trader-evolver` instead loads multi-year
history into a local store and replays the same pipeline over it, so the Darwin
weights can evolve across years of data in minutes.

## Core principles

1. **No agent loop.** Analysis modules are single LLM calls
   (`Call(ctx, system, user) -> (content, tokens, err)`), JSON output, no tools.
2. **Codex provider only.** Reads `~/.codex/auth.json` (or `CODEX_API_KEY`) and uses
   the Responses API. No Anthropic, no base-url override.
3. **Faithful to tradex.** Scoring thresholds, Darwin coefficients (1.05 / 0.95,
   bounds [0.3, 2.5]), Sharpe, and weighted voting match the TS implementation.
4. **Graceful degradation.** Indicators with no multi-year history (funding / OI /
   long-short ratio) are `null` in backtests; the pipeline falls back to neutral and
   never errors.
5. **Source routing by asset type:** crypto → Binance; stocks/indices/commodities/
   VIX/DXY → Yahoo Finance; Fear & Greed → alternative.me (`limit=0`, full history);
   pre-IPO (`vntl:*`) has no history and is excluded.

## Architecture (4-layer pipeline, ported from ATLAS-GIC / tradex)

```
History store ──> L1 Regime (pure rules) ──> L2 Modules x5 (LLM, parallel)
                                                   │
                                                   ▼
              L4 Decision <── L3 CRO (LLM) <── L3 Synthesizer (Darwin-weighted vote)
                                                   │
                                  Evolution: record signals, backfill returns from
                                  future bars in the store, update Darwin weights.
```

## Package layout

| Package | Ported from | Purpose |
|---|---|---|
| `internal/types` | `pipeline/types.ts`, `evolution/types.ts` | shared structs |
| `internal/regime` | `regime_detector.ts` | L1 deterministic regime |
| `internal/synth` | `synthesizer.ts` | L3 weighted voting |
| `internal/modules` | `module_runner.ts`, `prompt_composer.ts`, `adversarial.ts` | L2 + CRO |
| `internal/evolution` | `darwin_weights.ts`, `scorecard.ts`, `recommendation_tracker.ts` | weight evolution |
| `internal/orchestrator` | `orchestrator.ts` | 4-layer wiring |
| `internal/llm` | (new) | Codex provider + mock provider |
| `internal/collectors` | (new) | Binance / Yahoo / Fear&Greed history fetchers |
| `internal/store` | (new) | SQLite history + backtest results |
| `internal/backtest` | (new) | time-travel replay engine + reports |
| `prompts/` | `tradex/prompts/` | copied verbatim |

## CLI

```bash
evolver collect   # fetch multi-year history into the store
evolver backtest  # replay the pipeline over history, evolve Darwin weights
evolver report    # render backtest results
```

## Status

Under active migration. See `progress.md` for the live checklist.
