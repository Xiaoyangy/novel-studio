# 工程能力清单

生成时间：2026-07-06 21:51:48

## 工程能力总览

- `services/short-story-dashboard/`：短篇项目服务与 HTML 进度看板；使用 `novel-studio service start` 启动。
- `data/generated-output/`：历史短篇正文、服务项目、工作流状态、配图方案和审核报告。
- `data/reference-library/`：题材参考库、写作技巧源材料和拆书样本。
- `quality/audit/`：本地 AIGC / AI 味 / 重复 / 内容逻辑 / 错别字审核脚本与参考。
- `skills/`：novel-studio 原生命令、story 工具箱、审核等 skill 的唯一源目录，可通过 `novel-studio skills export --to <dir>` 导出。
- `assets/references/`：运行时通用写作技巧摘要、人工感标尺、生产链路、去 AI 味规则和通用规划资料。

## 统一规划口径

- 长篇和短篇共用 `novel-studio` 规划逻辑。
- 默认单章字数预算为 2100-3000 字；用户或本书规则覆盖时，以覆盖值为准。
- 用户给预期总字数时，先按当前单章预算反推大致章数，再设计卷弧与章节承载量。
- 字数是节奏预算，不为卡点牺牲必要剧情、人物选择、铺垫、读者读感或章节钩子。

## 快速命令

```bash
go run ./cmd/novel-studio service start
go run ./cmd/novel-studio service status
go run ./cmd/novel-studio skills list
python3 scripts/index_workspace_assets.py
```

## 数据索引

- [data/generated-output/INDEX.md](../data/generated-output/INDEX.md)
- [data/reference-library/INDEX.md](../data/reference-library/INDEX.md)
