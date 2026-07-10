# Pipeline 性能与恢复审计（2026-07-11）

## 结论

这次第一章并不是“真正改写了 12 遍”。从 2026-07-10 11:28:31 到 2026-07-11 00:11:40，墙钟 12 小时 43 分；扣除 1 小时 49 分无遥测空档，有效执行约 10 小时 53 分。12 条 review history 只对应 3 个正文 hash，checkpoints 也只有 3 次 durable commit。绝大多数时间花在错章规划、上下文重载、全角色推演/计划 schema 修复和同正文复评。

合并当前与归档遥测，至少有 402 次模型调用；可计量部分约 3155 万 input token、78.6 万 output token、174.75 美元。计划/世界推演约占 60.8% 调用和 83% 已记录成本。保守估计至少 6 小时、本次较合理为 6.5-7.5 小时可避免。

## 原流程的主要失速点

1. 返工目标污染：第 1 章任务曾多次规划成第 2 章，craft recall 也召回过其他项目角色，约 84 分钟没有形成正确章节计划。
2. 上下文放大：Writer 同时携带 54.9KB 总提示词、风格和 anti-AI 附件；233 次 Writer 调用中 132 次只是读取 context/chapter/craft。首轮曾出现约 1MB 单次上下文。
3. 人工串行拆分：plan details 固定 5 批，20 名角色固定每批 5 人；失败后 structure、simulation 和 details 反复重建。
4. 无进展继续派发：旧 Dispatcher 只告警，不熔断。11:28-14:42 共 180 次响应且没有新 checkpoint。
5. 同文复评：12 条评审只有 3 个正文 hash，9 条属于相同正文重复审核；同一正文出现 rewrite/accept/polish 漂移。
6. 恢复证据不完整：rewrite 换入正文后会删除旧 review，崩溃重跑却要求旧 review；commit 又可能在质量门禁和 RAG 前把 Progress 标为完成。
7. 并发边界错误：通用 subagent 暴露 parallel/background/team，但所有 Agent 共享 Progress、Checkpoints 和 RAG，直接并发会破坏单世界事实线。真正可并行的冻结输入任务反而是串行的。

## 新执行图

```text
idea
  -> brainstorm kickoff journal (same input resumes)
  -> Architect (single writer, durable foundation)
  -> zero-init (dependency ordered, no正文)
  -> initial world tick
  -> chapter context snapshot
  -> world simulation (up to 8 roles per patch)
  -> POV plan structure
  -> plan details: causal foundation -> voice/entertainment -> reader contract
  -> draft candidates x3 in parallel on the same frozen plan
  -> deterministic rank + pairwise selection
  -> commit saga: state -> quality_checked -> checkpointed -> rag_indexed
  -> Editor || DeepSeek in parallel on the same frozen body
  -> serial review/RAG commit
  -> rewrite only when the exact body hash is blocking
  -> review cache hit or changed-hash review
  -> deliver
```

单世界、全角色自主决策的原始设计没有改变：世界模拟仍先于 POV plan，正文仍只是对主视角 plan 的渲染。并发只发生在不可变输入上的候选或审核；simulation partial、plan partial、Progress、Checkpoint、正文 swap、review 状态和 RAG 写入保持单写者顺序。

## 已实现的加速与保护

| 环节 | 旧行为 | 新行为 |
|---|---|---|
| Planner prompt | 54,945B Writer 总提示词再追加风格/anti-AI | 专用 Planner 5,593B；渲染规则留给 Drafter |
| Context | Writer 默认保留 32K recent tokens，章节 context 188KB | Planner 16K、world simulator 8K、Drafter 12K；profile 预算 128/160/144KB |
| 世界推演 | 每批最多 5 名 | 每批最多 8 名；20 人通常从 4 批降为 3 批 |
| POV plan | 固定 5 批，voice/style 强制拆开 | 3 个因果边界批次，字段完整校验不变 |
| 草稿候选 | 3 次模型调用串行 | 3 个冻结计划候选并发生成，串行选优 |
| 审核 | Editor 后再 DeepSeek | 两分支并发，产物/RAG 串行提交 |
| 同文复评 | 每次重新调用两个模型 | body/model/prompt/context 指纹独立缓存；失败重跑只补缺失分支 |
| RAG/context | 相同 query 重做 embedding/搜索/trace | 同索引同 query 短期复用；cache hit 不重复写 trace；style stats 按文件指纹复用 |
| Dispatcher | 第 3 次仅告警，继续派发 | 同任务 3 次且 checkpoint/partial 指纹都无变化则中止 |
| Rewrite | 最多 5 次，每次各拿完整 budget | 所有尝试共享一个章节总 deadline；换入正文后可直接恢复 review |
| Commit | completed 可跳过质量/RAG | quality/checkpoint/RAG 分阶段恢复，全部有证据才清 pending |
| Pipeline resume | 只校验输出，prompt 变化仍跳旧阶段 | 创作指令或模型/prompt 协议指纹变化时失效旧完成图 |
| Brainstorm | 共享 `.brainstorm-staging`，完成后崩溃会重跑 | staging 按 input hash 隔离，brainstorm artifact 有 kickoff journal |

## 熔断与预算

- 同一 Agent + task 连续 2 次无 durable progress 先告警，第 3 次中止；正式 checkpoint、world simulation partial、plan partial 和 draft partial 任一变化都会重新计数。
- Planner 最多 30 turns，world simulator 16，Drafter 80，draft finalizer 30；不再让单次 subagent 吞掉数百轮。
- Pipeline write 外层仍保留最多 4 次有界恢复；内部无进展会先被 Dispatcher 截断。
- review 的 Editor/Reviewer 共用同一墙钟预算并行运行；rewrite 的所有重试共享同一章节预算。
- 含写工具的 Agent 不再声明 `ToolsAreIdempotent=true`，避免工具已完成后因流错误重放整个 turn。

## 预期墙钟模型

健康的新章节仍受模型生成速度、正文长度和外部 API 限流影响，不能承诺固定分钟数。但关键路径已经从“角色 4 批 + plan 5 批 + draft 3 串行 + review 2 串行”缩为“角色约 3 批 + plan 3 批 + draft 1 个并发窗口 + review 1 个并发窗口”。相同正文的第二次 review 应为本地 cache hit，不再消耗模型时间。

本次最坏的无 checkpoint 空转会在 3 个无进展 dispatch 后停止，不会再出现 180 个响应、3 小时没有 durable progress 的同类失控。根据本次遥测，已直接消除的重复结构覆盖约 6-7.5 小时浪费来源；这不是把正常 11 小时简单按比例承诺为某个值，而是把失控上界改成可观测、可中止、可恢复。

## 未直接并行的环节

- Architect foundation、zero-init 最终提交、world simulation merge、plan finalize、chapter commit、review ledger 和 RAG/Qdrant 写入必须串行。
- 跨章节正文默认串行，保证蝴蝶效应和上一章提交事实能进入下一章。
- Brainstorm 的市场/RAG/约束三路 fan-out 需要只读 analyst 输出 + 单 reducer 协议；现有写工具尚未具备项目级 lease，不能直接开放通用 subagent parallel。

后续若继续做第二阶段，优先级应是：项目级单写者 lease、usage 增量游标恢复、zero-init 纯构建器 fan-out + 单次提交、角色决策只读 fan-out + reducer，而不是继续增加 Writer 重试次数。

## 验证

- `go test -count=1 ./...`
- `go test -race -count=1 ./internal/writer/sampler ./cmd/novel-studio ./internal/tools ./internal/host/flow`
- `git diff --check`

验证期间没有启动 live pipeline，也没有主动写入小说章节、plan 或 review 产物。
