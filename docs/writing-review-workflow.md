# 写作审核一体化执行方案

本文档定义 `novel-studio` 后续创作任务的默认工作流：用户给题材、大纲、人物设定或直接说“继续开始创作”时，系统如何启动看板、恢复进度、写作、审核、返工和交付。

## 目标

- 写作和审核合成一条闭环，不把审核只放在完本之后。
- 长篇和短篇只在写作前的规划粒度上不同；设计阶段交付物必须一致，进入章节写作后，写作和审核逻辑也必须一致。
- 每次开始创作默认提供 HTML 进度看板。
- 已有看板服务或其他并行任务时，不重复启动服务，只刷新数据。
- 所有恢复判断以落盘事实为准，不凭聊天记忆判断进度。
- 每章必须经过提交、机械审核和编辑审核；未过门禁不进入下一章。

## 默认入口

当用户说“继续开始创作”或提供新的题材大纲时，按以下顺序执行：

1. 确保进度看板可用。
2. 判断这是新书、续写、返工，还是多项目并行中的一个任务。
3. 读取当前事实源：`meta/progress.json`、`meta/pipeline.json`、`meta/checkpoints.jsonl`、`reviews/`、`meta/rag/`。
4. 如果已有未完成写作或返工队列，优先恢复，不重新开书。
5. pipeline 启动时确保本机 Qdrant；进入写作前刷新当前项目 RAG，embedding 启用时必须写入 Qdrant 和本地 vector fallback。
6. 如果没有有效进度，再按用户给的题材/大纲创建创作指令并启动流水线。
7. 新书进入逐章写作前，先检查设计包：`premise`、`characters`、`world_rules`、`book_world`、`outline`、`layered_outline`、`timeline`、`relationship_state`、`foreshadow_ledger`、`compass` 和 `故事圣经.md` 必须齐全。

## 看板服务策略

HTML 看板由 `services/dashboard/` 提供（统一读取 `data/runs/`），入口命令是：

```bash
go run ./cmd/novel-studio service status
go run ./cmd/novel-studio service start
go run ./cmd/novel-studio service open
```

默认地址：

```text
http://127.0.0.1:8765/novel.html
```

`/novel.html` 是长篇/当前工程项目看板，会自动发现当前 workspace 的 `output/novel`、`data/runs/*/output/novel` 以及 `NOVEL_STUDIO_NOVEL_DIR` / `NOVEL_STUDIO_NOVEL_DIRS` 指定目录。它动态刷新 `progress.json`、`pipeline.json`、章节、草稿、审核、AI 审核、摘要、日志、导出文件和 meta 下的全部资料。`/index.html` 仍保留短篇项目服务看板。

启动规则：

- 先跑 `service status`。
- 如果服务健康，只打开或刷新现有页面；不再启动第二个服务。
- 如果服务未启动，再启动服务。
- `service start` 已做幂等处理：同端口已有健康服务时直接返回 `service: ok`。
- 看板页面自身每 3.5 秒轮询刷新一次；手动刷新只调用页面的刷新按钮或重新请求 API，不重启服务。
- `--pipeline` 启动时会尽力后台拉起或复用看板，并在终端打印小说项目看板 URL；看板启动失败只告警，不阻断创作。

后台启动参考：

```bash
mkdir -p output/logs
nohup go run ./cmd/novel-studio service start --host 127.0.0.1 --port 8765 > output/logs/dashboard.log 2>&1 &
```

注意：看板服务只负责展示、项目状态、章节/审核文件读写和本地指标；它不是写作调度器。写作恢复和章级推进仍由 `novel-studio` CLI、Store 和 checkpoint 决定。

## 事实源

长篇或当前主项目以 `output/novel/` 为权威目录：

| 工件 | 用途 |
|---|---|
| `meta/progress.json` | 当前 phase、flow、章节总数、已完成章节、当前章、返工队列 |
| `meta/pipeline.json` | `--pipeline` 阶段完成状态和证据 |
| `meta/checkpoints.jsonl` | 每个写入步骤的断点恢复证据 |
| `chapters/NN.md` | 已提交终稿章节 |
| `drafts/NN.draft.md` | 当前草稿或返工稿 |
| `reviews/NN_ai_gate.json` | 机械审核结构化结果 |
| `reviews/NN.md` | 统一审核报告：机械门禁、AI 味信号和 Editor 八维章级评审 |
| `meta/review-summary.md` | 批量评审汇总 |
| `meta/chapter_progress.json/md` | 章节通过后沉淀的主线推进、主角变化、资源/时间线和下一章动态计划 |
| `meta/character_continuity.json/md` | 人物回归、偶发露脸、后续大纲用途和状态保留建议；只指导写作，不作为审核通过条件 |
| `meta/project_progress.json/md` | 项目级规划仪表盘：交付口径、卷弧推进、主角变化路线图、逐章承诺兑现、钩子节奏、资源清账、伏笔优先级、关系张力和资产运营动作 |
| `meta/evolution_report.json/md` | 可审计自动进化报告：观察近章问题、诊断模式、提出 proposed 候选改动和验证计划；不自动采纳规则或代码 |
| `meta/rag/index_state.json/md` | 当前项目 RAG chunk、facet、source 和 hash 状态；只索引本书事实，不索引deconstruction-library/对标库 |
| `meta/rag/vector_store.json/md` | embedding 启用时的本地向量 fallback；Qdrant 失效时仍可做本地向量召回 |
| `meta/rag/retrieval_trace.jsonl` | `novel_context` 每次召回的 query、strategy、命中来源、分数和 reason |
| Qdrant collection | 本机向量库，pipeline 启动时确保可用；只做召回加速，不是唯一事实源 |

短篇/批量项目以看板服务的 `DATA_ROOT` 为事实源；默认是 `data/generated-output/short_story_service/projects/`，也可能被 `NOVEL_STUDIO_OUTPUT_ROOT` 或 `NOVEL_STUDIO_SHORT_STORY_DATA` 覆盖。具体路径以 `/api/stages` 返回的 `output_root` 和 `project.json` 中的 `book_dir` 为准。短篇章节同样必须生成 `reviews/NN_ai_gate.json` 与统一审核报告 `reviews/NN.md`，看板质量门禁优先读取这些机械审核事实。

设计阶段统一事实源见 [`design-stage-workflow.md`](design-stage-workflow.md)。短篇不再只用 `故事圣经.md` 承载设计，必须同时生成与长篇同名的结构化文件；短篇的差异是 `layered_outline` 压缩为 1 卷 1 弧、时间线短链闭合、伏笔篇内回收，而不是减少交付物。

## 创作主线

新书默认走可恢复流水线：

```bash
go run ./cmd/novel-studio --pipeline --prompt-file run-prompts/<book>.md
```

已有进度时，不传新 prompt，按 Store 断点恢复：

```bash
go run ./cmd/novel-studio --pipeline
```

完整流水线阶段：

```text
write -> review -> rewrite -> export
```

需要先共创澄清时显式加入：

```text
cocreate -> write -> review -> rewrite -> export
```

写作阶段内部固定顺序：

```text
novel_context -> read_chapter -> plan_chapter -> draft_chapter -> check_consistency -> commit_chapter
```

`commit_chapter` 是章节完成的唯一交接点。只写出草稿不算完成；只有 `chapters/NN.md`、progress 和 checkpoint 都写好，才算本章进入审核。

数据沉淀、RAG upsert、Qdrant 和状态推进的完整说明见 [`data-lifecycle-and-progression.md`](data-lifecycle-and-progression.md)。

## 章级审核闭环

每章提交后立即进入门禁：

1. `commit_chapter` 自动运行机械审核，写入 `reviews/NN_ai_gate.json`，并先生成机械门禁版 `reviews/NN.md`。
2. 如果命中阻断级规则，章节进入 `pending_rewrites`，flow 切到 `polishing` 或 `rewriting`。
3. Editor 章级评审通过 `save_review` 回写同一个 `reviews/NN.md`，补全 AI 味信号、八维评审和改写建议，裁定 `accept`、`polish` 或 `rewrite`；审阅中的 issue、contract miss、低分维度和门禁升级原因会同步沉淀到 `meta/writing_assets.json/md` 的历史反馈区。
4. Writer 只处理被入队的章节。若 `rewrite_brief` 存在，先用 `plan_chapter` 重建本章 `causal_simulation.review_refinement` 和 `voice_logic`：把审核来源、失败类型、局部目标、保留约束、重规划动作、验收条件和停止条件写入计划，再改写正文。返工后再次 `check_consistency -> commit_chapter`。
5. 机械审核和 Editor 审核都通过后，才允许继续下一章或进入弧/卷/完本处理。
6. 章级 `accept` 后刷新 `meta/chapter_progress.*`、`meta/character_continuity.*`、`meta/project_progress.*` 与 `meta/evolution_report.*`，把本章造成的人物目标/压力/资源/关系/秘密/误判/行动倾向、时间线、资源、关系、大纲续用建议、从第1章到交付线的主角变化路线、项目级规划动作、写法历史反馈和可审计进化候选沉淀为下一章写作输入。

默认硬指标：

- 自研 AIGC / AI 占比目标：`<= 5%`。
- `aigc_ratio >= 35%` 属于阻断级机械门禁，自动压入返工。
- `5% < aigc_ratio < 35%` 至少作为警告处理；若用户要求每章 5% 以下，则必须返工到达标。
- 段落级重复必须为 0。
- 内容完整性不得出现字符汤、无语义噪声、数词事实错误、库存明喻硬贴、顺序词悬空等问题。
- 本地 AI 味风险分建议 `<= 35/100`。
- 人物是否在本章回归、是否安排偶发露脸，不是审核硬门槛；只在出现人物状态前后矛盾、声口偏离或误用已沉淀事实时进入 `character` / `continuity` 问题。

机械审核脚本入口：

```bash
python3 quality/audit/scripts/aigc_value.py <章节或正文路径> --target 5
python3 quality/audit/scripts/text_signals.py <章节或正文路径>
python3 quality/audit/scripts/paragraph_dup.py <章节或正文路径>
python3 quality/audit/scripts/content_lint.py <章节或正文路径>
python3 quality/audit/scripts/typo_scan.py <章节或正文路径>
```

文学评审入口：

```bash
go run ./cmd/novel-studio --pipeline --stages review --from 1 --to 5
go run ./cmd/novel-studio --pipeline --stages rewrite --from 1 --to 5
```

## 返工优先级

返工不按“最新章节”优先，而按风险优先：

1. 机械审核阻断：AIGC 高风险、内容完整性、硬规则错误。
2. Editor `rewrite`：结构性错误、章节契约漏项、人物/设定严重偏移。
3. Editor `polish`：局部表达、节奏、审美品质、标点功能、AI 腔警告。
4. 用户明确指出的具体问题。

返工方式：

- 小范围问题用 `edit_chapter`。
- 大幅结构问题用 `draft_chapter(mode="write")` 整章覆盖。
- 不允许 Writer 跳过修改直接再次 commit。
- 返工后必须重新跑机械审核和 Editor 复审。

## 续写判断

收到“继续开始创作”时，先判断以下状态：

| 状态 | 动作 |
|---|---|
| `pending_rewrites` 非空 | 先处理返工队列 |
| `phase=writing` 且有 `in_progress_chapter` | 恢复当前章 |
| `completed_chapters` 少于 `total_chapters` | 写下一章 |
| `phase=complete` 但 review/export 未完成 | 跑 `review -> rewrite -> export` |
| `meta/pipeline.json` 有未完成阶段 | 从未完成阶段续跑 |
| checkpoint 与 progress 不一致 | 先跑 `--diag`，修复事实源后再继续 |

诊断命令：

```bash
go run ./cmd/novel-studio --diag
```

## 多任务并行

并行原则：

- 看板服务全局只启动一个。
- 每本书的写作事实源必须隔离，不能多个写作进程同时写同一个 `output/novel/`。
- 批量短篇由看板项目的 `agent.status` 区分 `running`、`pending`、`blocked`、`done`。
- 没有用户明确要求，不自动扩张并行 writer 数量。
- 如果已有并行任务正在跑，只刷新看板数据，当前任务复用已有服务。

并行任务状态检查：

```bash
curl -s http://127.0.0.1:8765/api/projects
```

## 完结与交付

短篇或三万字以内项目只在完本收束上多一步全文合并：

1. 所有章节达标。
2. 合并 `正文.md`。
3. 跑全文 AIGC、重复、内容、错字审核。
4. 生成 `审核报告.md`、`降AI味记录.md`、`交付清单.md`。
5. 看板 final 阶段质量门禁通过后交付。

长篇项目：

1. 最后一章完成章级审核。
2. 弧/卷摘要齐全。
3. `progress.phase=complete`。
4. 跑 `--pipeline --stages deliver` 做交付沉淀（推进台账 + RAG 事实 + 交付快照）。
5. 保留 `reviews/`、`diag-export.md` 作为复盘证据。

## 后续执行口径

用户以后只要给出题材、大纲、人物或一句“继续开始创作”，默认执行：

```text
确保/复用 HTML 看板 -> 读取事实源 -> 判断新建或恢复 -> 写作 -> 机械审核 -> Editor 评审 -> 返工复审 -> 更新看板/进度 -> 继续下一章或交付
```

除非用户明确要求暂停、只做设定或只看内容，否则不把流程停在大纲、草稿或未审核章节。
