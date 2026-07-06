# 项目结构

`novel-studio` 按运行时、服务、数据、审核、写作指导和历史研究资料分层。历史文件已实际迁入规范目录，不保留软链。

## 顶层分区

| 目录 | 职责 |
|---|---|
| `cmd/` | Go CLI 入口。 |
| `internal/` | Go 运行时、代理、规则、存储、诊断和工具实现。 |
| `assets/` | 编译进二进制的 prompts、references 和样式资源。 |
| `skills/` | 给外部 agent 读取和 CLI 导出的唯一 skill 源目录；包含原生 CLI skill、story 工具箱和旧工作流兼容 skill。 |
| `services/dashboard/` | 浏览器进度看板：统一读取 `data/runs/` 下全部书目工程，实时展示各书进度、章节审核、用量与运行日志（只读）。 |
| `quality/audit/` | 本地 AIGC、AI 味、重复、内容逻辑和错别字审核聚合入口。 |
| `data/generated-output/` | 历史短篇产出、服务项目、工作流状态、审核报告和图片方案。 |
| `data/reference-library/` | 历史参考库、题材样本、拆书材料和写作技巧源材料。 |
| `output/novel/` | 当前长篇项目的运行产物。 |
| `deconstruction-library/` | 长篇拆文、题材研究和方法论工作区。 |
| `docs/` | 工程说明、架构说明和整合清单。 |
| `scripts/` | 工程维护脚本。 |

新代码、新文档和历史状态文件都应直接使用规范入口，不再依赖 `data/generated-output/`、`data/reference-library/` 或 `short_story_service/` 顶层路径。

## 审核聚合

`quality/audit/scripts/` 聚合审核脚本：

- `aigc_value.py`
- `text_signals.py`
- `paragraph_dup.py`
- `content_lint.py`
- `typo_scan.py`

`skills/review/SKILL.md` 是 agent 流程入口；脚本和参考资料只维护在 `quality/audit/`。服务端默认也从 `quality/audit/scripts/` 导入审核能力。

## 写作指导资料

写作指导不再维护镜像目录：

- 通用运行时参考只在 `assets/references/` 维护。
- 题材、流程和历史 prompt 只在对应 `skills/*/references/` 维护。
- `novel-studio skills export --to <dir>` 从 `skills/` 单一源目录导出 skill 包。
