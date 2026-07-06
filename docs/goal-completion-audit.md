# 目标完成审计：项目结构、架构、执行逻辑与优化

审计日期：2026-07-02

## 目标拆解

用户目标：

- 通读 `novel-studio` 的项目设计、架构和执行逻辑。
- 对项目进行实际优化，而不是只给建议。
- 可查阅最新设计思路、写作参考资料和人味感 / AI 文本识别资料。
- 最终目标状态需要能被正确更新。

本文件作为完成证据索引，记录已检查的当前状态、已落地优化、外部资料结论和验证命令。

## 当前结构结论

项目已经按“运行时、skill、审核、数据、写作资产、服务和文档”分层：

| 区域 | 当前职责 | 审计结论 |
|---|---|---|
| `cmd/novel-studio/` | CLI 入口：pipeline、import、diag、review、rewrite、export、skills、service 等 | 入口清晰，长任务通过子命令复用内部 runtime。 |
| `internal/` | Go 运行时：agents、host、flow、tools、store、diag、rules、rag、aigc、eval | 核心依赖方向是 Host/Agents/Tools/Store/Domain，诊断只读。 |
| `skills/` | 全部 skill 的唯一源目录，可导出给外部 agent | 已补齐上下文读取协议和共享副本防漂移。 |
| `quality/audit/` | AIGC、AI 味、重复、内容逻辑、错别字审核入口 | 已是审核能力规范目录，和 `skills/review` 保持分工。 |
| `assets/` | 编译进二进制的 prompts、references、styles | README 已说明新增内容归属和三处接线要求。 |
| `docs/` | 架构、上下文、观测、结构、整合说明 | 已修正过期依赖口径，并新增本审计文件。 |
| `scripts/` | 维护脚本与校验 | 已扩展为 skill 上下文、共享副本、文档漂移的统一校验入口。 |

结构说明的权威文件：

- `docs/project-structure.md`
- `docs/integration-inventory.md`
- `README.md`
- `assets/README.md`
- `skills/README.md`

## 架构和执行逻辑结论

### 长篇创作主链路

当前主链路是：

`Entry/CLI -> Host -> Coordinator -> SubAgent -> Tools -> Store -> Domain`

关键设计：

- Host 只做启动、恢复、事件投影和 Flow Router 指令注入。
- Coordinator 保留语义裁定和用户干预处理。
- Flow Router 负责查表型流程路由。
- Tools 是事实层唯一写入口，写类工具必须落盘 artifact、推进 progress、追加 checkpoint。
- Store 只做文件系统事实持久化。
- Diag 只读输出 Finding，不自动修复、不改流程。

已核对文件：

- `docs/architecture.md`
- `cmd/novel-studio/pipeline_cmd.go`
- `internal/host/flow/router.go`
- `internal/host/flow/dispatcher.go`
- `internal/tools/commit_chapter.go`
- `internal/domain/transitions.go`
- `internal/diag/rules_flow.go`
- `internal/diag/export.go`

### 上下文和压缩恢复链路

当前上下文链路是：

`github.com/voocel/agentcore/context -> internal/tools/novel_context -> internal/agents/ctxpack -> writerRestorePack`

关键设计：

- Writer 压缩顺序：`ToolResultMicrocompact -> LightTrim -> StoreSummaryCompact -> FullSummary`。
- StoreSummaryCompact 优先使用落盘结构化记忆，减少 LLM 摘要漂移。
- FullSummary 后追加 writer restore pack，恢复当前章节计划、大纲、角色和写法引擎。
- `skills/CONTEXT_PROTOCOL.md` 是所有 skill 执行前的共享恢复协议。

已核对文件：

- `docs/context-management.md`
- `internal/agents/context_manager.go`
- `internal/agents/build.go`
- `internal/agents/ctxpack/builder.go`
- `internal/agents/ctxpack/restore.go`
- `internal/tools/novel_context.go`
- `skills/CONTEXT_PROTOCOL.md`
- `skills/bundle.go`

## 已落地优化

### 1. 共享 skill 副本防漂移

新增：

- `scripts/shared_skill_files.json`

扩展：

- `scripts/validate_skill_context.py`

效果：

- 登记 34 组、94 个真实文件副本。
- 按 SHA-256 校验每组副本是否与 canonical 一致。
- 明确禁止共享副本变成软链接。
- 覆盖去 AI 资料、禁用词、共用写作方法论、短篇输出契约和本地检查脚本。

这是对“不要软链接，实际文件迁移 / 整合共性功能”的直接收口：文件仍是真实副本，导出后自包含，但由校验防止同步漂移。

### 2. skill 上下文读取顺序修正

修正：

- `skills/README.md`

效果：

- 执行任何 skill 前先读 `skills/CONTEXT_PROTOCOL.md`。
- 再读对应 `SKILL.md`、`CONTEXT.md`、`context.json`、required / conditional files。
- `novel-studio skills context --content --state-dir` 的恢复语义保持一致。

### 3. 架构文档依赖口径修正

修正：

- `docs/architecture.md`
- `docs/context-management.md`

效果：

- `agentcore` 不再被描述成本地兄弟目录。
- 当前权威口径是 `go.mod` 依赖：`github.com/voocel/agentcore`。
- `github.com/voocel/litellm` 也按模块依赖描述。

同时扩展 `scripts/validate_skill_context.py`，若关键架构文档再次出现旧的本地兄弟目录依赖口径，会直接校验失败。

### 4. 旧共享脚本引用清理

修正：

- `skills/story-short-analyze/references/output-contract.md`
- `skills/story-short-write/references/output-contract.md`
- `skills/story-short-analyze/references/material-decomposition.md`
- `anti-ai-writing.md` / `banned-words.md` 的共享副本头部说明

效果：

- 源 `skills/` 和 `scripts/` 不再引用已废弃的旧共享校验脚本名。
- 同步守卫统一到 `python3 scripts/validate_skill_context.py`。

## 外部资料结论

本轮查阅重点不是“找一个检测器分数”，而是把最新研究转成可执行工程原则。

### AI 文本识别

外部资料支持当前本地审核方向：

- 公开研究继续强调 stylometry + psycholinguistic / cognitive feature mapping，而不是单一表面词表。
- 短样本 stylometry 可以使用词汇、语法、句法、标点模式识别人机差异，但结论依赖文本类型边界。
- RAID 等鲁棒性基准显示检测器会受模型、领域、采样策略和 adversarial attacks 影响，不能把单个分数当成身份裁决。
- 创意写作研究显示人类文本更异质，LLM 输出更容易形成模型内聚类和风格统一。

工程化结论：

- 本地 `quality/audit` 保留 `aigc_value.py`、`text_signals.py`、`stylestat`、分片代理、概率曲率代理、布局 humanizer 指纹和人工锚点校准是合理方向。
- 审核报告必须解释风险来源，而不是只输出一个 AI 百分比。
- 写作修复应优先保持剧情、人物选择、场景承载和证据链，不能用乱码、随机断句或拟声噪声去扰动检测曲线。

来源：

- https://arxiv.org/abs/2505.01800
- https://arxiv.org/abs/2507.00838
- https://arxiv.org/html/2405.07940v1
- https://www.nature.com/articles/s41599-025-05986-3

### 人味感写作

结合本地 `assets/references/human-feel-craft.md`、`skills/*/references/human-feel-craft.md` 和外部 stylometry 结论，当前“人味感”不应理解为随机化，而应落到：

- 场景承担信息：物件、动作、对白、规则代价和选择后果承载剧情。
- 视角带偏差：限知、误判、改口、自嘲和短链修正。
- 节奏有非均匀性：句长、段长、对话密度、解释密度按场景功能变化。
- 证据链可回看：强动作、误会、反转、关系改变都有前文可复核线索。
- 现实支架具体：账单、门禁、路线、工牌、收据、药盒、价签等低成本物件持续回扣。

## 完成证据矩阵

| 要求 | 证据 | 状态 |
|---|---|---|
| 通读项目结构 | `docs/project-structure.md`、`find . -maxdepth 2`、`find internal cmd docs skills quality assets scripts` | 已完成 |
| 通读架构设计 | `docs/architecture.md`、`README.md`、`internal/host/flow/*`、`internal/domain/transitions.go` | 已完成 |
| 通读执行逻辑 | `cmd/novel-studio/*`、`pipeline_cmd.go`、`skills/*/context.json`、`skills/bundle.go` | 已完成 |
| 整合共性功能 | `scripts/shared_skill_files.json` + `validate_shared_skill_files()` | 已完成 |
| 不使用软链接 | `validate_shared_skill_files()` 对共享路径检查 `is_symlink()`；`find skills -type l` 验证 | 已完成 |
| 防止上下文压缩丢 skill 内容 | `skills/CONTEXT_PROTOCOL.md`、`skills/README.md`、`skills context --content` 校验 | 已完成 |
| 查阅最新资料 | 本文件“外部资料结论”记录来源和工程化结论 | 已完成 |
| 验证当前实现 | `python3 scripts/validate_skill_context.py`、`go test ./...`、`go build -o novel-studio ./cmd/novel-studio` | 已完成 |

## 最终验证命令

目标完成前必须全部通过：

```bash
python3 scripts/validate_skill_context.py
go test ./...
go build -o novel-studio ./cmd/novel-studio
find skills -type l -print
rg -n "旧本地依赖口径|旧共享校验脚本名" docs README.md skills scripts
```

通过后，本目标可以标记为完成。

## 2026-07-02 最终验证结果

已通过：

- `python3 scripts/validate_skill_context.py`：validated 37 skill context manifests。
- `go test ./...`：全部 Go package 通过。
- `go build -o novel-studio ./cmd/novel-studio`：构建成功。
- `find skills -type l -print`：无输出，确认 `skills/` 下没有软链接。
- 旧本地依赖口径和旧共享校验脚本名检查：无残留。
