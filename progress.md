# trader-evolver 开发进度

## 阶段总览

| 阶段 | 描述 | 状态 |
|------|------|------|
| A | 骨架 + 纯逻辑移植 | ✅ 完成 (A1-A6) |
| B | LLM + 模块层 | ✅ 完成 (B1-B4) |
| C | 数据收集器 + 存储 | ✅ 完成 (C1-C5) |
| D | 回测引擎 | ✅ 完成 (D1-D4) |
| E | 高级特性 | ✅ 完成 (E1-E3) |
| F | CLI + 集成测试 | ✅ 完成 (F1-F3) |

---

## 已完成

### Stage A — 骨架 + 纯逻辑
- [x] A1. `go mod init` + 目录结构 + README + prompts 复制
- [x] A2. types 移植 (internal/types/types.go)
- [x] A3. regime_detector → internal/regime (+ 测试通过)
- [x] A4. synthesizer → internal/synth (+ 测试通过)
- [x] A5. evolution: darwin_weights + scorecard + recommendation_tracker (+ 测试通过)
- [x] A6. prompt_composer → internal/modules (+ 测试通过) — **阶段 A 完成**

### Stage B — LLM + 模块
- [x] B1. internal/llm: Codex 提供者 (SSE Responses API, 重试/退避, JWT) + MockProvider (+ 测试通过)
- [x] B2. module_runner: LLM 调用 → JSON 解析 → 信号验证/夹紧 → 错误回退中性 (+ 测试通过)
- [x] B3. adversarial CRO: 对抗性风控审查，失败安全 → 不批准/高风险 (+ 测试通过)
- [x] B4. orchestrator: 4 层管线编排，并行模块，CRO 门槛，R:R 过滤 (+ 测试通过) — **阶段 B 完成**

### Stage C — 数据收集器 + 存储
- [x] C1. internal/store: 纯 Go SQLite (WAL 模式，无 CGO)。candles/daily_macro/feargreed 表，
      幂等 upsert，as-of 查询，覆盖率统计 (+ 测试通过)
- [x] C2. internal/collectors/binance.go: Binance USDT-M 期货 klines 收集器。
      分页游标、重试退避、增量续传 (+ 测试通过)
- [x] C3. internal/collectors/yahoo.go: Yahoo Finance 日线收集器。
      股票/指数/VIX/DXY/商品 → UpsertDailyMacro / UpsertCandles。nil-bar 跳过，
      midnight-UTC 归一化，重试退避 (+ 测试通过)
- [x] C4. internal/collectors/feargreed.go: Fear & Greed 指数收集器 (alternative.me)。
      FetchAll(limit=0) 全量历史，FetchRecent 增量，重试退避 (+ 测试通过)
- [x] C5. cmd/evolver: CLI 框架。`evolver collect` 编排所有收集器 — **阶段 C 完成**

### Stage D — 回测引擎
- [x] D1. internal/backtest/engine.go: 时间穿越回测引擎。
      遍历交易日 → as-of 重建市场状态 → 运行完整 4 层管线 → 记录推荐 → 回填前向收益 → 更新 Darwin 权重。
      OnDayComplete 回调 (+ 测试通过)
- [x] D2. 前向收益回填: 集成在 engine.go (backfillReturns)。
      当未来数据可用时自动填充 Return1d/5d/20d
- [x] D3. Darwin 权重进化: 集成在 engine.go (updateDarwinWeights)。
      滚动 Sharpe → 前 25% ×1.05, 后 25% ×0.95, 夹紧 [0.3, 2.5]
- [x] D4. internal/backtest/autoresearch.go: Autoresearch 自我改进循环。
      FindWorstAgent → GenerateModification → EvaluateModification (keep/revert),
      冷却期跟踪 (+ 测试通过) — **阶段 D 完成**

### Stage E — 高级特性 (来自 atlas-gic)
- [x] E1. internal/janus/janus.go: JANUS 元加权层。
      多群体混合 (softmax + 地板/天花板约束)，涌现式市场状态检测
      (NOVEL/HISTORICAL/MIXED)，置信度加权推荐混合 + 分歧惩罚 (+ 测试通过)
- [x] E2. internal/reflexivity/reflexivity.go: Soros 反身性引擎。
      5 条反馈循环: 价格→基本面、盈亏→行为、叙事→资金流、市场→政策、反转检测。
      可配置阈值，严重性分级 (+ 测试通过)
- [x] E3. 报告生成: `evolver report` 命令，数据覆盖 + JSON 输出 — **阶段 E 完成**

### Stage F — CLI + 集成
- [x] F1. `evolver backtest` 命令: 完整历史回放，支持 --mock 离线模式
- [x] F2. `evolver report` 命令: 数据覆盖摘要 + JSON 输出
- [x] F3. 端到端集成测试: store → engine → Darwin → JANUS → reflexivity。
      验证推荐生成、收益回填、权重进化、JANUS 混合、反身性检测 — **阶段 F 完成**

---

## 测试统计

- **97 个测试**，**11 个包**全部通过
- **40 个 Go 源文件**
- 所有测试离线运行（MockProvider，httptest）

## 技术说明

- Go 1.24.1, 模块名: `trader-evolver`
- SQLite: modernc.org/sqlite (纯 Go, 无 CGO, WAL 模式)
- LLM: Codex 提供者 (SSE Responses API); MockProvider 用于离线测试
- 回测中无历史的指标 (funding/OI/LS) → nil → 优雅降级为中性
- 参考项目: [atlas-gic](https://github.com/chrisworsey55/atlas-gic)
