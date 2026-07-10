# 上下文管理说明

本文档说明 `novel-studio` 当前的上下文管理体系，包括：

- 为什么要做上下文管理
- 上下文从哪里来
- 运行时如何压缩、恢复、交接
- 每个策略的价值、触发条件与适用场景
- 出问题时应该先看哪里

目标不是介绍抽象概念，而是让后续维护者打开这一份文档，就能快速理解当前实现和排障入口。

## 1. 设计目标

本项目的上下文管理不是通用聊天场景，而是面向小说创作场景。它要同时解决几类问题：

1. 长对话会超出模型上下文窗口。
2. 小说创作需要保留的不是“聊天历史本身”，而是结构化叙事记忆。
3. Writer 在压缩后不能丢掉角色状态、伏笔、章节计划、写法引擎、风格约束、审稿待修项。
4. 恢复写作时不能假设模型还“记得之前聊过什么”，必须优先依赖持久化工件。

因此我们采用的是一套“分层记忆”方案：

- 短期记忆：最近保留的消息尾部
- 中期记忆：压缩生成的 `ContextSummary`
- 长期记忆：项目 store 中的结构化工件
- 恢复记忆：resume prompt / restore pack / novel_context

## 2. 整体架构

### 2.1 主要分层

当前上下文管理分成四层：

1. `github.com/voocel/agentcore/context`
   负责通用的上下文预算、策略管线、压缩/恢复框架。

2. `internal/tools/novel_context`
   负责把小说项目中的结构化数据装配成当前轮可用上下文。

3. `internal/agents/ctxpack`
   负责 Writer 专用的 store-based 快速压缩。

4. `internal/agents/ctxpack/restore.go`
   负责在 `FullSummary` 之后追加一份压缩后恢复包，确保 Writer 能继续写。

### 2.2 数据流

运行时主要有两条上下文路径：

1. 正常工作路径
   - Agent 调用 `novel_context`
   - `novel_context` 从 store 读取章节摘要、计划、角色、时间线等数据
   - 这些数据进入当前轮 prompt

2. 上下文过长路径
   - `ContextManager` 检测到 token 压力
   - 按策略顺序压缩
   - 优先尝试轻量压缩和 store-based 压缩
   - 还不够时才走 LLM `FullSummary`
   - `FullSummary` 后注入 restore pack

## 3. 关键文件

### 3.1 通用上下文引擎

- `github.com/voocel/agentcore/context` 模块中的 `strategy.go`
- `github.com/voocel/agentcore/context` 模块中的 `engine.go`
- `github.com/voocel/agentcore/context` 模块中的 `strategy_tool.go`
- `github.com/voocel/agentcore/context` 模块中的 `strategy_trim.go`
- `github.com/voocel/agentcore/context` 模块中的 `strategy_summary.go`
- `github.com/voocel/agentcore/context` 模块中的 `message.go`
- `github.com/voocel/agentcore/context` 模块中的 `summary_run.go`

作用：

- 定义 `Strategy` / `ForceCompactionStrategy`
- 负责基于预算执行策略链
- 负责 `ContextSummary` 的表示与 LLM 转换
- 负责 `FullSummary` 的 LLM 摘要压缩

### 3.2 项目侧接线

- `internal/agents/build.go`
- `internal/agents/context_manager.go`

作用：

- 组装 Writer / Coordinator 的 `ContextManager`
- 给 Writer 注入额外的 `StoreSummaryCompact`
- 给 Writer 配置小说定制的 `FullSummary` prompt
- 给 Writer 配置 `writerRestorePack`

### 3.3 项目侧压缩与恢复

- `internal/agents/ctxpack/strategy.go`
- `internal/agents/ctxpack/builder.go`
- `internal/agents/ctxpack/restore.go`

作用：

- 在 LLM 摘要之前，优先使用 store 数据做快速压缩
- 统一构建 Writer 压缩与恢复所需的结构化上下文
- 在 `FullSummary` 后追加一份纯内存 restore message
- 压缩/恢复时携带轻量 `writing_engine`（启用特征、active_rules、anti_ai_rules、taboos、trace），但不携带样本文本，避免恢复消息膨胀

### 3.4 结构化上下文装配

- `internal/tools/novel_context.go`
- `internal/tools/novel_context_builders.go`
- `internal/domain/runtime.go`

作用：

- 定义 `ContextProfile` / `MemoryPolicy`
- 决定加载多少章节摘要、多少时间线、是否启用分层摘要
- 把 store 中的章节、角色、伏笔、时间线、审稿经验等装配出来

### 3.5 恢复提示与状态标签

- `internal/host/resume.go`
- `internal/host/host_snapshot.go`
- `internal/domain/runtime.go`

作用：

- 根据进度、checkpoint 和记忆策略生成恢复提示
- 在 `UISnapshot.RecoveryLabel` 暴露当前可恢复状态
- 通过 `memory_policy.handoff_preferred` 表示长篇/分层摘要场景更应依赖结构化工件，而不是聊天历史

### 3.6 可观测性

- `internal/host/observer_*.go`（事件投影）
- `internal/host/host_snapshot.go`（状态快照聚合）

作用：

- 记录上下文重写事件
- 输出策略名称、token 变化、消息保留量
- 让上层入口能看到当前上下文是 `projected` 还是 `compacted`（经 `Snapshot()`）

## 4. ContextManager 是怎么组装的

Writer 和 Coordinator 都走 `newContextManager`，但配置不同。

当前 `contextManagerConfig` 的关键参数：

- `ContextWindow`
  模型总上下文窗口。

- `ReserveTokens`
  给模型输出预留的 token。

- `KeepRecentTokens`
  压缩时尽量保留的最近消息尾部预算。

- `ToolMicrocompact`
  工具结果微压缩配置。

- `ExtraStrategies`
  项目侧额外压缩策略。当前 Writer 用来挂 `StoreSummaryCompact`。

- `Summary`
  `FullSummary` 的配置，包括自定义 prompt 和 post-summary hook。

当前实际配置值：

| 参数 | Writer | Coordinator |
|------|--------|-------------|
| ReserveTokens | 16,384 | 32,000 |
| KeepRecentTokens | 20,000 | 30,000 |
| CommitOnProject | false | true |
| IdleThreshold | 5min | 无 |
| ExtraStrategies | StoreSummaryCompact | 无 |
| 自定义 Summary Prompt | 小说叙事版 | 默认(代码助手版) |

压缩触发阈值 = `ContextWindow - ReserveTokens`。例如窗口 128K 时，Writer 在 ~112K 触发，Coordinator 在 ~96K 触发。

当前 Writer 的策略管线顺序是：

1. `ToolResultMicrocompact`
2. `LightTrim`
3. `StoreSummaryCompact`
4. `FullSummary`

这个顺序有明确含义：

- 先用最便宜的办法清理工具噪音
- 再裁剪超长文本块
- 如果 store 数据够，直接做零 LLM 的结构化压缩
- 最后才退到 LLM 摘要

## 5. 每个策略的作用

### 5.1 ToolResultMicrocompact

实现位置：

- `github.com/voocel/agentcore/context` 模块中的 `strategy_tool.go`

作用：

- 清理历史 `tool_result`
- 给旧工具结果替换成简短占位文本

价值：

- 工具返回内容通常体积大、信息密度低
- 很多旧工具结果只是“过程噪音”，不是小说记忆

当前 Writer 的配置特点：

- 设置了 `IdleThreshold = 5m`

这意味着：

- 如果最近 assistant 消息已经闲置超过阈值
- 会更激进地减少保留的旧工具结果数量

适用场景：

- 多轮 `novel_context`
- 多轮 read / check / draft 工具之后

### 5.2 LightTrim

实现位置：

- `github.com/voocel/agentcore/context` 模块中的 `strategy_trim.go`

作用：

- 截断非常长的文本块
- 保留头部和尾部，中间用占位符替代

价值：

- 保住消息结构不变
- 代价低
- 很适合处理超长章节原文或大段输出

适用场景：

- 单条消息过长，但还不需要整段历史做 summary

### 5.3 StoreSummaryCompact

实现位置：

- `internal/agents/ctxpack/strategy.go`
- `internal/agents/ctxpack/builder.go`

作用：

- 当 Writer 上下文过长时
- 优先使用持久化 store 中的结构化记忆来替换旧消息
- 不调用 LLM

它不是对话摘要，而是“结构化记忆替换”。

当前保留的核心数据包括：

- 当前进度
- 最近章节摘要
- 当前章节计划
- 当前章节大纲
- 当前弧摘要
- 当前卷摘要
- 角色快照
- 活跃伏笔
- 待修审稿问题
- 最近时间线
- 风格规则

触发前提：

- 当前章节大于 1
- store 中已经有足够的历史摘要
- 且当前章至少有工作态数据
  - `chapter_plan` 或 `current_outline`

价值：

- 降低 LLM 压缩次数
- 避免小说关键信息在摘要时漂移
- 让长期记忆优先依赖落盘事实，而不是聊天历史

为什么只给 Writer 用：

- 这是小说业务策略，不是通用框架策略
- Coordinator / Editor 的上下文模式不同
- 先在最需要连续创作记忆的 Writer 上验证最合理

### 5.4 FullSummary

实现位置：

- `github.com/voocel/agentcore/context` 模块中的 `strategy_summary.go`
- `github.com/voocel/agentcore/context` 模块中的 `summary_run.go`

作用：

- 当上面几层还不够时，使用模型生成 `ContextSummary`
- 保留最近消息尾部
- 把更早的上下文变成结构化 checkpoint

Writer 与默认代码助手不同的地方：

- Writer 使用了自定义 summary prompt
- 摘要内容明确要求保留：
  - 当前进度
  - 角色即时状态
  - 活跃伏笔与线索
  - 审稿反馈与待修问题
  - 风格与节奏
  - 关键决策
  - 下一步
  - 关键上下文

价值：

- 是最终兜底策略
- 即使 store 数据不足，也仍然可以通过 LLM 维持连续性

### 5.5 熔断器（Circuit Breaker）

实现位置：

- `github.com/voocel/agentcore/context` 模块中的 `engine.go`

作用：

- 当压缩连续失败达到阈值（默认 3 次）时，跳过当前轮压缩
- 跳过时仍然发出 `RewriteEvent`（`Reason = “circuit_breaker”`）
- 快照中 scope 会显示为”熔断跳过”
- 采用半开模式：跳过一轮后下次会重试，成功则复位，再失败再跳过

为什么需要：

- LLM 摘要可能因网络、模型拒绝等原因连续失败
- 没有熔断的话，每轮 Project 都会尝试并失败，浪费 API 调用
- 长篇写作会话中这个浪费会累积

排障：

- 如果快照持续显示”熔断跳过”，说明 LLM 摘要路径有问题
- 检查 slog 中 `reason=circuit_breaker` 的上下文重写事件
- 熔断不影响 `StoreSummaryCompact`（它不调 LLM）

### 5.6 Token 估算（CJK 感知）

实现位置：

- `github.com/voocel/agentcore/context` 模块中的 `usage.go`

作用：

- 所有预算控制、压缩触发时机都依赖 token 估算
- `estimateTextTokens` 自动检测文本是否以 CJK 字符为主
- CJK 主导文本：`runes × 1.5`
- ASCII 主导文本：`bytes / 4`

为什么不能用标准 `bytes/4`：

- 中文 UTF-8 一个字 = 3 bytes
- `bytes/4` 会把一个中文字估为 0.75 token，实际约 1.5 token
- 低估 2 倍会导致压缩触发严重滞后

影响范围：

- `EstimateTokens`（单条消息）
- `EstimateTotal`（消息列表）
- `EstimateContextTokens`（混合估算：LLM 上报 Usage + 尾部消息估算）
- `ctxpack/builder.go` 中的预算裁剪

注意：ToolCall 的 args 是 JSON（ASCII 主导），仍使用 `bytes/4`，不受 CJK 调整影响。

## 6. Writer 为什么有两套”压缩后记忆”

当前 Writer 有两条看起来相近、但职责不同的链路：

### 6.1 StoreSummaryCompact

职责：

- 在压缩过程中直接替换旧消息

特点：

- 发生在 `FullSummary` 之前
- 零 LLM
- 用 store 替换更早历史

### 6.2 writerRestorePack

实现位置：

- `internal/agents/ctxpack/restore.go`

职责：

- 在 `FullSummary` 之后追加一条 restore message

特点：

- 发生在 LLM 压缩之后
- 通过 `PostSummaryHook` 注入
- 用于补充 Writer 恢复继续创作时必须看到的结构化信息
- 刷新时机包括 Host 启动构建、Resume/Continue，以及 Flow Router 派发 writer
  指令后（章节已预标记为 `in_progress`，restore pack 能读到最新章节）

为什么两者都需要：

- `StoreSummaryCompact` 不是总能命中
  - 比如第一章或 store 数据不够时
- `FullSummary` 即使做得再好，也可能遗漏 store 中的精确信息
- 所以 restore pack 作为最后一道保险

现在这两者已经共用 `ctxpack/builder.go`，避免口径漂移。写法资产只以编译后的轻量 `writing_engine` 进入压缩恢复包，样本文本仍由常规 `novel_context` 按需选择。

## 7. novel_context 的作用

实现位置：

- `internal/tools/novel_context.go`
- `internal/tools/novel_context_builders.go`

`novel_context` 不是压缩策略，它是运行时的“结构化上下文装配器”。

它把 store 中的数据分成几类：

- `working_memory`
  - 当前章节计划
  - 当前章节大纲
  - 最近章节摘要
  - 时间线
  - checkpoint
  - previous tail

- `episodic_memory`
  - 角色状态
  - 关系状态
  - 最近状态变化
  - 伏笔

- `reference_pack`
  - 更稳定的设定和参考数据

- `selected_memory`
  - 按当前任务挑选出来的少量重要记忆

### 7.1 RAG 写作召回

实现位置：

- `internal/rag/indexer.go`
- `internal/rag/qdrant.go`
- `internal/rag/vector_store.go`
- `internal/tools/rag_sink.go`
- `internal/tools/novel_context_builders.go`
- `assets/references/rag-writing-guidelines.md`

当前策略：

- 索引时不再只 embedding 裸 chunk 文本，而是用 `context + facet + summary + keywords + text` 作为 embedding 输入。
- `RAGChunk.context` 用于记录来源作品、章节、小节、题材、标签等局部上下文；老索引缺失该字段时会按 `source_path/source_kind/facet/metadata` 自动推导。
- `RAGChunk.keywords` 用于本地轻量中文召回；老索引缺失时会从 context、summary、metadata 中自动提取。
- Writer 路径的 `selected_memory.rag_recall` 使用混合降级链：Qdrant + BM25；Qdrant EOF/空结果时转本地 `vector_store.json` + BM25；embedding 失败时直接使用 BM25。trace strategy 使用 `*_v2` 标明实际路径。章节大纲、章节任务单、出场角色共同组成查询，短中文词和长短语同时参与打分。
- `--pipeline` 启动时会确保本机 Qdrant；`write`/`deliver` 前运行 `ensurePipelineRAGReady`。chunk、模型、维度和本地向量一致时直接复用；Qdrant 丢库时从 `vector_store.json` 重放，不重新 embedding。
- 手动 `--build-rag` 默认从 `summaries/` + `chapters/` 回填已完成章节事实包；显式 `--backfill-chapters=false` 时才只重建项目资料索引。
- 第一章正文未开写、foundation 已落盘时，可先运行 `--zero-init` 生成写前资产和更窄的白名单 RAG；它只索引 foundation 与零章推演资产，不回填旧章节、审稿或实验稿。
- `save_foundation`、`commit_chapter`、`save_review`、`review-existing` 和 `rewrite-existing` 都通过 `UpsertRAGChunks` 增量替换同 `source_path` 的旧 chunk。相同 hash 来源直接 no-op；变化来源先完整 staging embedding，再删旧 point、批量写新 point，最后提交本地状态。失败写入 `meta/rag/pending_upserts.json` 等待下次重放。
- `save_review` 会把历史审阅反馈同步进 `meta/writing_assets.json/md`：完整问题仍在 `reviews/`，可复用的写法建议会进入 `writing_engine.feedback` 和 `active_rules`。
- `save_review(verdict=accept, scope=chapter)` 还会沉淀项目记忆：`chapter_progress`、`character_continuity`、`project_progress`、`evolution_report`、时间线、伏笔、关系、状态变化、资源账本、世界规则、本书世界、动态大纲、指南针、写法资产库和历史反馈都会作为 RAG artifact 更新；零章初始化资产存在时也会进入项目记忆，供第一章/后续审核追溯角色为什么这么判断。
- RAG 只允许当前项目内来源：`prompt.md`、`input/`、`output/novel` 的设定/大纲/账本/摘要；`deconstruction-library/`、`对标/`、`data/reference-library/` 等参考库在构建和召回时都会被排除。
- `reference_pack.retrieval_trace` 输出 `strategy/query_terms/max_results/matches`，让 writer/editor 判断召回强弱和来源边界。
- `rag_writing_guidelines` 是 RAG 使用边界：召回只提供本书已有证据、约束和沉淀事实，不得把弱召回写成本书事实。

如果后续接托管 file search、BM25、rerank 或图谱召回，应保持同一语义：

- 查询仍由章节目标、契约、角色、伏笔和本书世界共同构造。
- 检索结果必须保留 source/facet/context/reason trace。
- 新召回层只能提升候选质量，不能绕过本书 store 中的章节契约、资源账本和用户规则。

价值：

- 它决定了每一轮真正“喂给模型”的结构化小说上下文
- `StoreSummaryCompact` 不是调用它本身，但和它复用同类数据来源与装配思路
- 数据沉淀、Qdrant 和推进状态的完整口径见 [`data-lifecycle-and-progression.md`](data-lifecycle-and-progression.md)

## 8. ContextProfile 与 MemoryPolicy

实现位置：

- `internal/domain/runtime.go`

### 8.1 ContextProfile

作用：

- 按总章节数决定加载窗口大小

当前规则：

- `<= 15` 章
  - 最近 `10` 章摘要
  - 最近 `10` 章时间线

- `<= 50` 章
  - 最近 `5` 章摘要
  - 最近 `8` 章时间线

- `> 50` 章
  - 最近 `3` 章摘要
  - 最近 `5` 章时间线
  - 启用分层摘要

价值：

- 控制上下文规模
- 避免长篇时把所有历史都塞进 prompt

### 8.2 MemoryPolicy

作用：

- 把当前上下文使用策略显式写出来
- 供 `novel_context` 输出
- 供恢复提示、reminder 和诊断逻辑使用

关键字段：

- `SummaryWindow`
- `TimelineWindow`
- `LayeredSummaries`
- `SummaryStrategy`
- `HandoffPreferred`
- `ReadOnlyThreshold`

价值：

- 把“当前系统应该如何使用记忆”从隐式逻辑变成显式运行时策略

## 9. 恢复提示与 memory_policy 的作用

实现位置：

- `internal/host/resume.go`
- `internal/host/host_snapshot.go`
- `internal/domain/runtime.go`

当作品进入更长、更复杂、更依赖结构化工件的阶段时，`memory_policy.handoff_preferred`
会提醒上层入口和恢复逻辑优先依赖 store 工件，而不是聊天历史。

恢复提示与状态标签会记录或展示：

- 当前阶段与 flow
- 下一章位置
- 最近提交
- 最近审阅
- 最近摘要
- 当前 memory policy
- 恢复指导语或恢复状态标签

价值：

- 中断恢复时不依赖聊天历史
- 在返工、审阅、长篇场景中优先依赖结构化工件

## 10. 可观测性与排障

### 10.1 上下文重写事件

实现位置：

- `internal/agents/context_manager.go`

每次上下文重写都会通过 `contextRewriteCallback` 写入 `slog`：

- `reason`
- `strategy`
- `committed`
- `tokens_before`
- `tokens_after`
- `msgs_before`
- `msgs_after`
- `compacted`
- `kept`
- `duration_ms`

`novel-studio --diag` 会从日志尾部聚合这些结构字段，写入脱敏 `diag-export.md`
的运行时信号；若 `reason=circuit_breaker`，本地 Findings 会报告
`ContextCompactionCircuitBreaker`。上层 UI 主要通过 `Snapshot()` 暴露上下文健康度和恢复标签。

### 10.2 快照里能看到什么

`Snapshot()` 暴露以下上下文字段（供上层入口展示或日志记录，实现见 `internal/host/host_snapshot.go`）：

- 当前上下文 token（含健康度档位）
- context window
- 当前上下文 scope（含"熔断跳过"）
- 当前最后一次策略名称
- summary 数量

上下文百分比的健康度档位：

| 档位 | 条件 | 含义 |
|------|------|------|
| 充裕 | < 70% | 远离压缩阈值 |
| 接近 | 70-85% | 接近压缩阈值 |
| 紧张 | > 85% | 即将或正在压缩 |

Scope 的中文标签：

| Scope | 显示 | 含义 |
|-------|------|------|
| baseline | 基线 | 正常状态 |
| projected | 投影 | 临时压缩预览 |
| compacted | 已提交 | 压缩已生效 |
| recovered | 恢复 | 溢出后恢复 |
| skipped | 熔断跳过 | 压缩被熔断器跳过 |

价值：

- 能快速判断当前上下文健康度
- 黄色/红色时可以预期即将发生压缩
- 看到"熔断跳过"说明 LLM 摘要路径有问题

### 10.3 出问题先看哪里

#### 场景 1：Writer 压缩后丢章节计划

先看：

- `novel_context` 是否稳定注入 `chapter_plan`
- `ctxpack/builder.go` 是否拿到 `chapterPlan`
- `writerRestorePack` 是否刷新

重点文件：

- `internal/tools/novel_context_builders.go`
- `internal/agents/ctxpack/builder.go`
- `internal/agents/ctxpack/restore.go`
- `internal/agents/context_manager.go`

#### 场景 2：压缩后丢角色状态/伏笔

先看：

- `LoadLatestSnapshots`
- `LoadActiveForeshadow`
- `ctxpack/builder.go`
- Writer summary prompt 是否被覆盖

#### 场景 3：压缩后丢写法引擎 / 人工感规则

先看：

- `output/novel/meta/writing_assets.json` 是否存在且有 enabled features
- `novel_context(chapter=N)` 的 `reference_pack.writing_engine` 是否有 active_rules
- `ctxpack/builder.go` 是否把 `writingEngine` 加入 store summary 和 writer restore sections
- `internal/agents/ctxpack/strategy_test.go` 的恢复包测试是否仍覆盖 `写法引擎`

#### 场景 4：压缩频繁但总是不命中 store_summary

先看：

- 当前章节是不是 `<= 1`
- 是否已有 recent summaries / arc / volume summary
- 是否存在 `chapter_plan` 或 `current_outline`
- `writer.Context.Strategy` 最终记录的是不是 `full_summary`

#### 场景 5：恢复后上下文不够

先看：

- resume prompt 是否生成
- restore pack 是否刷新；正常 writer 派发时应由 `Dispatcher.SetOnWriterDispatch`
  触发，且刷新发生在 `Progress.InProgressChapter` 更新之后
- `memory_policy` 是否显示当前应依赖分层摘要/结构化恢复

#### 场景 6：工具结果太多导致上下文膨胀

先看：

- `ToolResultMicrocompact` 是否命中
- `IdleThreshold` 是否生效
- `diag-export.md` 的「上下文重写」是否长期只有 `full_summary` 或出现
  `reason=circuit_breaker`

## 11. 当前实现的取舍

### 已明确坚持的方向

1. 不把小说业务逻辑塞进 `agentcore`
2. 优先依赖结构化 store，而不是聊天历史
3. Writer 使用专门的小说摘要 prompt
4. 压缩与恢复尽量共用 builder，避免口径漂移

### 当前仍然有意保留的限制

1. `StoreSummaryCompact` 只给 Writer 用
2. 第一章不会命中 store-based compact
3. store 数据不足时仍然回退到 `FullSummary`
4. `writerRestorePack` 是追加式补偿，不替代 `FullSummary`

这些限制不是缺陷，而是当前阶段为了控制复杂度做的边界。

## 12. 一句话总结

本项目的上下文管理不是“把长对话压短”这么简单，而是：

`优先用结构化小说记忆维持连续性，在必要时才让 LLM 去摘要对话；并且在压缩、恢复、交接三个环节都尽量依赖同一套持久化工件。`

如果你后续要改这套系统，优先守住下面三条：

1. 不要让 Writer 的关键记忆再次只依赖聊天历史。
2. 不要让 `store_summary` 和 `writer_restore` 口径分叉。
3. 出现连续性问题时，先查结构化工件有没有进入上下文，再决定是否改 prompt。
