# trader-evolver

一个独立的 Go 项目，用于**多方法交易分析管线的历史回测与进化验证**。

灵感来源于 [atlas-gic](https://github.com/chrisworsey55/atlas-gic) —— 一个自我改进的 AI 交易代理框架。
本项目将其核心架构（4 层管线 + Darwin 进化 + Autoresearch + JANUS 元层 + Soros 反身性）用 Go 重新实现，
并增加了 atlas-gic 所缺少的关键能力：**快速历史回放**，可在数分钟内完成数年数据的进化验证。

## 为什么需要这个项目

在线交易系统每次运行只记录当日信号，前向收益（1d/5d/20d）需要真实时间流逝才能填充。
验证 Darwin 权重进化逻辑需要数月。`trader-evolver` 将多年历史数据加载到本地存储，
重放同一管线，使得 Darwin 权重可以在几分钟内完成跨年进化。

## 核心原则

1. **无 Agent 循环**。分析模块是单次 LLM 调用 (`Call(ctx, system, user) -> (content, tokens, err)`)，JSON 输出，无工具调用。
2. **Codex Provider**。读取 `~/.codex/auth.json` 或 `CODEX_API_KEY`，使用 Responses API。
3. **忠实移植**。评分阈值、Darwin 系数 (1.05 / 0.95，边界 [0.3, 2.5])、Sharpe 计算和加权投票均与原版一致。
4. **优雅降级**。无多年历史的指标（资金费率 / OI / 多空比）在回测中为 `null`；管线回退到中性信号，永不报错。
5. **数据源路由**：加密货币 → Binance；股票/指数/商品/VIX/DXY → Yahoo Finance；Fear & Greed → alternative.me。

## 架构（4 层管线，移植自 atlas-gic）

```
历史数据存储 ──> L1 市场状态检测 (规则) ──> L2 分析模块 x5 (LLM, 并行)
                                                    │
                                                    ▼
               L4 最终决策 <── L3 CRO (对抗性风控) <── L3 综合器 (Darwin 加权投票)
                                                    │
                                    进化: 记录信号 → 回填未来收益 → 更新 Darwin 权重
```

### 详细层级说明

| 层级 | 功能 | 对应 atlas-gic |
|------|------|---------------|
| L1 Regime | 市场状态检测（VIX/DXY/Fear&Greed → 风险偏好/波动率/趋势） | Layer 1 Macro |
| L2 Modules | 5 个分析模块并行调用 LLM（ICT/缠论/波浪/指标/基本面） | Layer 2+3 |
| L3 Synth+CRO | Darwin 加权投票 + 对抗性风控审查 | Layer 4 CRO+CIO |
| L4 Decision | 最终交易决策（开多/开空/观望 + 仓位大小 + R:R 门槛） | Autonomous Execution |

## 高级特性

### Autoresearch 自我改进循环

受 [Karpathy autoresearch](https://github.com/karpathy/autoresearch) 启发：

1. 找到 Sharpe 最低的 agent
2. 生成一个针对性 prompt 修改
3. 运行 5 个交易日
4. Sharpe 改善 → 保留（git commit），否则 → 回退（git revert）

**agent 的 prompt 就是权重，Sharpe 就是损失函数，无需 GPU。**

### JANUS 元层

多个训练群体（cohort）产出不同建议，JANUS 根据近期准确率动态加权：

- 短窗口群体优势 → **新颖市场状态**（NOVEL REGIME）
- 长窗口群体优势 → **历史市场状态**（HISTORICAL REGIME）
- 权重差异即是涌现式的市场状态检测器

### Soros 反身性引擎

5 条反馈循环建模：

1. **价格 → 基本面**：暴跌 >15% → 信用降级、人才流失
2. **盈亏 → 行为**：回撤 >10% → 强制平仓级联
3. **叙事 → 资金流**：3+ 分析师趋同 → 散户跟风
4. **市场 → 政策**：权益回撤 >15% → 央行宽松
5. **反身性反转检测**：循环运行 5+ 轮 → 极端信号，最大共识 = 最大脆弱性

### Darwin 权重进化

每个交易日：
- 表现前 25% 的模块：权重 × 1.05
- 表现后 25% 的模块：权重 × 0.95
- 上限 2.5，下限 0.3，起始 1.0

## 包结构

| 包 | 来源 | 用途 |
|---|---|---|
| `internal/types` | atlas-gic 类型定义 | 共享结构体 |
| `internal/regime` | 市场状态检测器 | L1 确定性规则 |
| `internal/synth` | 综合器 | L3 加权投票 |
| `internal/modules` | 模块运行器 + CRO | L2 LLM 调用 + 对抗性审查 |
| `internal/evolution` | Darwin 权重管理 | 评分卡 + 权重进化 |
| `internal/orchestrator` | 编排器 | 4 层管线接线 |
| `internal/llm` | LLM 提供者 | Codex SSE + Mock（离线测试）|
| `internal/collectors` | 数据收集器 | Binance / Yahoo / Fear&Greed |
| `internal/store` | 存储层 | SQLite（WAL 模式，无 CGO）|
| `internal/backtest` | 回测引擎 | 时间穿越 + Autoresearch |
| `internal/janus` | JANUS 元层 | 多群体混合 + 涌现式状态检测 |
| `internal/reflexivity` | 反身性引擎 | 5 条 Soros 反馈循环 |
| `cmd/evolver` | CLI 入口 | collect / backtest / report |
| `prompts/` | 提示词模板 | 各模块的系统/用户提示 |

## CLI 使用

```bash
# 拉取多年历史数据到本地 SQLite
evolver collect --start 2020-01-01

# 回放管线历史，进化 Darwin 权重（离线模式）
evolver backtest --mock --instrument btc:usdt --start 2024-01-01

# 查看数据覆盖和回测结果
evolver report
```

### 命令选项

```bash
evolver collect [选项]
  --db        SQLite 数据库路径（默认: ./data/evolver.db）
  --start     最早拉取日期（默认: 2020-01-01）
  --incremental  增量更新（默认: true）

evolver backtest [选项]
  --db          数据库路径
  --instrument  回测标的（默认: btc:usdt）
  --start       回测起始日（默认: 2024-01-01）
  --end         回测结束日（默认: 今天）
  --mock        使用 Mock 提供者（无 API 调用）
  --prompts     提示词目录（默认: ./prompts）

evolver report [选项]
  --db          数据库路径
  --instrument  标的
  --json        输出 JSON 格式
```

## 技术栈

- **语言**: Go 1.24.1
- **数据库**: SQLite（modernc.org/sqlite，纯 Go，无 CGO）
- **LLM**: Codex Responses API（SSE 流式）
- **测试**: 97 个测试，11 个包全部通过
- **依赖**: 仅 `modernc.org/sqlite` + `github.com/google/uuid`

## 项目状态

所有阶段已完成。详见 `progress.md`。

| 阶段 | 状态 |
|------|------|
| A - 骨架 + 纯逻辑 | ✅ 完成 |
| B - LLM + 模块 | ✅ 完成 |
| C - 数据收集器 + 存储 | ✅ 完成 |
| D - 回测引擎 | ✅ 完成 |
| E - 高级特性 | ✅ 完成 |
| F - CLI + 集成测试 | ✅ 完成 |
