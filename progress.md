# trader-evolver migration progress

## Done
- [x] A1. go mod init + dir skeleton + README + prompts copied
- [x] A2. types ported (internal/types/types.go) — build OK
- [x] A3. regime_detector → internal/regime (+ tests passing)

## Next
- [ ] A4. synthesizer → internal/synth (+ tests)

## Stage tracker
- Stage A (skeleton + pure logic): A1✅ A2✅ A3✅ A4 A5 A6
- Stage B (LLM + modules): B1 B2 B3 B4
- Stage C (collectors + store): C1 C2 C3 C4 C5
- Stage D (backtest engine): D1 D2 D3 D4

## Notes
- Source of truth: /Users/zhongyuanzhang/mytradebot/tradex (read-only)
- Codex provider only; no agent loop. Module call = single LLM Call(ctx, system, user).
- Backtest sets funding/OI/LS to null → graceful neutral degradation.
- Go 1.24.1, module name: trader-evolver
