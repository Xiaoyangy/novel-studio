# novel-studio 功能入口 skills

本目录是 **novel-studio 全部 skill 的唯一源目录**，同时包含 novel-studio 原生命令入口、oh-story 工具箱、番茄短篇兼容流程和本地审核入口。TUI 移除后，原生写作能力统一经 `novel-studio --pipeline` 执行；每个 `SKILL.md` 描述一个功能：触发场景、前置条件、要执行的命令、参数与产物。每个 skill 同时带有 `CONTEXT.md` 和 `context.json`，用于声明必读资料、条件资料和压缩恢复规则。

外部 agent（Claude Code / Codex / OpenCode 等）读取对应 skill 后，直接拼出命令行调用二进制即可。

## 上下文读取协议

执行任何 skill 前，按 [`CONTEXT_PROTOCOL.md`](CONTEXT_PROTOCOL.md) 的顺序读取：

1. `skills/CONTEXT_PROTOCOL.md`
2. `SKILL.md`
3. 同目录 `CONTEXT.md`
4. 同目录 `context.json`
5. `context.json` 中的 `required_files`
6. 与当前任务匹配的 `conditional_files`

长任务或多阶段任务必须在执行目录维护 `.skill-context/<skill>.md`，记录已读文件、当前阶段、关键输入输出、硬约束和下一步。上下文压缩后先读这个恢复文件，再继续执行。

如果执行器需要一次性拿到可恢复上下文全文，而不是只拿路径清单，使用：

```bash
novel-studio skills context <skill> --content
novel-studio skills context <skill> --content --include-conditional
novel-studio skills context <skill> --content --state-dir <执行目录>
```

第二条会把当前 skill 声明的条件资料和脚本也物化出来，只在任务确实需要对应分支时使用。第三条会把执行目录里已经存在的 `.skill-context/<skill>.md`、`追踪/上下文.md`、`_progress.md` 等 manifest 声明状态文件纳入恢复包，并报告仍缺失的状态文件。

## 入口一览

| skill | 功能 | 底层命令 |
|---|---|---|
| `novel-check` | LLM 连通性自检（创作前先确认能用） | `novel-studio --check` |
| `novel-cocreate` | 多轮对话澄清需求，定稿创作指令 | `novel-studio --cocreate` |
| `novel-write` | 单句需求跑一次创作 | `novel-studio --pipeline --prompt <text>` |
| `novel-douban-write` | 豆瓣阅读原创长篇专项：15W-30W + 默认评审链 | `novel-studio --pipeline --prompt-file <file>` |
| `novel-pipeline` | 可恢复流水线：写作→评审→重写→导出 | `novel-studio --pipeline` |
| `novel-import` | 完整 LLM 反推导入（设定+评审+diag） | `novel-studio --import <novel.md>` |
| `novel-import-fast` | 本地确定性导入（不反推设定） | `novel-studio --import-fast <chapter.md>` |
| `novel-review` | 逐章 Editor 评审（不改原文） | `novel-studio --pipeline --stages review` |
| `novel-rewrite` | 按评审反馈逐章 Writer 重写 | `novel-studio --pipeline --stages rewrite` |
| `novel-export` | 合并导出已完成章节（TXT/EPUB） | `novel-studio --export` |
| `novel-diag` | 诊断当前项目产物 | `novel-studio --diag` |
| `novel-simulate` | 仿写画像合成 / 导入 | `novel-studio --simulate` / `--import-sim` |
| `novel-steer` | 排队干预，下次启动生效 | `novel-studio --steer "<指令>"` |
| `novel-writing-assets` | 查看/初始化/启停/组合/绑定/试写/重编译本书写法特征池 | `novel-studio --writing-assets ...` |

## 已归并的旧工作流 skills

| skill | 功能 | 现在的工程入口 |
|---|---|---|
| `fanqie-writing-flow` | 番茄短篇可恢复阶段流、多书队列、逐章审核、final-gate | `skills/fanqie-writing-flow/SKILL.md` + `data/generated-output/writing_flows/` |
| `fanqie-novel-template` | 番茄短篇三件套、标签、故事圣经和交付骨架 | `skills/fanqie-novel-template/SKILL.md` |
| `fanqie-baihe-short` | 百合 / GL / 双女主短篇方法论 | `skills/fanqie-baihe-short/SKILL.md` |
| `fanqie-shuangnanzhu-short` | 双男主 / CP 营业短篇方法论 | `skills/fanqie-shuangnanzhu-short/SKILL.md` |
| `fanqie-shuangwen-short` | 反转爽文 / 复仇打脸短篇方法论 | `skills/fanqie-shuangwen-short/SKILL.md` |
| `fanqie-western-fantasy-short` | 哥伦布计划 / 西方幻想短篇方法论 | `skills/fanqie-western-fantasy-short/SKILL.md` |
| `review` | 本地 AIGC / AI 味 / 重复 / 内容逻辑 / 错别字审核逻辑 | `quality/audit/` + `skills/review/SKILL.md` |
| `deal-paper-summry` | 批量拆书、摘要聚合、题材写作 skill 生成 | `skills/deal-paper-summry/SKILL.md` |
| `fanqie-legacy-prompts` | 旧版题材 prompt 与故事圣经模板归档 | `skills/fanqie-legacy-prompts/SKILL.md` |

这些 skill 可用 `novel-studio skills context <name>` 展开压缩恢复读取清单，用 `novel-studio skills context <name> --content` 物化必读上下文，用 `--state-dir <执行目录>` 把本次任务状态一起带回，也可用 `novel-studio skills export --to <dir>` 从本目录直接导出。导出时，`quality/audit/` 的审核脚本会装配进导出产物；源仓库不再保留第二份脚本副本。

## 共享副本防漂移

部分 story 工具箱 skill 为了保持导出后自包含，保留真实文件副本，不使用软链接。同步关系集中写在 [`../scripts/shared_skill_files.json`](../scripts/shared_skill_files.json)，覆盖去 AI 资料、禁用词表、共用写作方法论和本地检查脚本等内容。`python3 scripts/validate_skill_context.py` 会逐组按 SHA-256 校验，任一副本缺失、变成软链接或内容漂移都会失败。

## 推荐工作流

- **从零写一本（无人值守）**：`novel-check` → `novel-pipeline --prompt "..."`（自动写作→评审→重写→导出，可断点续跑）
- **豆瓣原创长篇**：`novel-check` → `novel-douban-write`（准备豆瓣专项 prompt）→ `novel-pipeline --prompt-file ./豆瓣长篇需求.md`
- **需求模糊先聊清楚**：`novel-cocreate`（定稿创作指令）→ `novel-pipeline --stages cocreate,write,review,rewrite,export`
- **已有小说返工**：`novel-import` / `novel-import-fast` → `novel-review` → `novel-rewrite` → `novel-export`
- **随时**：`novel-diag` 诊断、`novel-steer` 排队下一轮修改意见
- **短篇看板**：`novel-studio service start` → 浏览器打开 `http://127.0.0.1:8765`
- **本地审核**：`python3 quality/audit/scripts/aigc_value.py <正文路径> --target 5` → `python3 quality/audit/scripts/text_signals.py <正文路径>` → `python3 quality/audit/scripts/paragraph_dup.py <正文路径>`

## 长期资产

新书和导入项目会逐步形成可复用资产，而不是只依赖 prompt 文本：

- 写法资产：`output/novel/meta/writing_assets.json` / `.md`，保存可启用、停用、组合、绑定、试写的写法特征、原文样本和当前编译结果；`novel-studio --writing-assets seed-defaults` 可为新项目注入人工感、单章可读性和去 AI 味基线；默认参考 `human_feel_craft`，把《同桌是只假装高冷的猫》的物件回扣、主观误判、短对话和现实支架作为人工感标尺；默认参考 `writing_techniques_digest`，把 `data/reference-library/writing-craft` 19 篇逐篇提炼的全书、大纲、人物、单章、改文和中文标点规则作为写作总纲。
- 本书世界：`output/novel/book_world.json` / `.md`，保存地图、地点、路线和势力图谱，章节上下文会按本章相关性裁剪注入。
- RAG 状态：`output/novel/meta/rag/index_state.json` 与 `retrieval_trace.jsonl`，记录 chunk hash 去重、facets 和召回命中原因。
- 资源账本：`output/novel/meta/resource_ledger.json` / `.md`，区分 `booked` 已入账事实与 `pending` 待确认提案。

## 公共前置条件

- 已安装 `novel-studio` 二进制（或 `go run ./cmd/novel-studio`）。
- 已完成首次配置：在交互终端运行一次 `novel-studio` 走 stdin 引导，或手写
  `~/.ainovel/config.json`（项目内可用 `./.ainovel/config.json` 覆盖）。headless / 子命令
  **不支持**首次引导。
- 章节产物默认写到 `output/novel/chapters/*.md`（可在配置里改 `OutputDir`）。
