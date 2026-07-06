# short-story-dashboard

`short-story-dashboard` 是 `novel-studio` 内置的短篇项目服务和 HTML 进度看板。短篇只在写作前采用压缩粒度和较短总字数；设计阶段交付物与长篇一致，进入章节写作后，写作、机械审核、复审、返工和解锁逻辑也与长篇一致。

## 启动

```bash
go run ./cmd/novel-studio service start
```

默认地址：`http://127.0.0.1:8765`

健康检查：

```bash
go run ./cmd/novel-studio service status
```

## 数据路径

- 项目数据：`data/generated-output/short_story_service/projects/`
- 进度心跳：`data/generated-output/short_story_service/progress_heartbeat.py`
- 审核脚本：`quality/audit/scripts/`
- HTML / JS / CSS：`services/short-story-dashboard/static/`

## 规则

短篇服务只负责看板、项目状态、章节/审核文件读写和本地指标。新写作规划仍以 `novel-studio` 的统一规则为准：默认单章 2100-3000 字，按预期总字数反推章节数，再逐章推进和审核。每章正文保存后都会补齐 `reviews/NN_ai_gate.json` 与统一审核报告 `reviews/NN.md`；这些机械审核事实和审核报告必须同时通过，章节才允许标记达标并解锁下一章。

统一设计包阶段必须先补齐 `premise.md`、`characters.json/md`、`world_rules.json/md`、`book_world.json/md`、`outline.json/md`、`layered_outline.json/md`、`timeline.json/md`、`relationship_state.json/md`、`foreshadow_ledger.json/md`、`compass.json/md` 和 `故事圣经.md`。短篇的 `layered_outline` 通常是 1 卷 1 弧，不能因为体量短就省掉这些文件。

## 图片生成

终版 `正文.md`、`故事圣经.md` 和通过结论的 `审核报告.md` 就绪后，服务会自动执行图片生成方案，刷新 `图片生成方案.md`、`images/image_jobs.json` 和 `images/*.prompt.txt`。默认环境没有图片模型命令时，图片任务会标记为“待生成”，但提示词和保存路径已完整产出，可直接复制到图片模型。

如需让服务实际出图，设置：

```bash
export NOVEL_STUDIO_IMAGE_GENERATOR_CMD='/path/to/generate-image'
```

该命令会在 `images/` 目录下逐图执行，并通过环境变量接收 `IMAGE_PROMPT`、`IMAGE_NEGATIVE_PROMPT`、`IMAGE_PROMPT_FILE`、`IMAGE_OUTPUT`、`IMAGE_OUTPUT_DIR`、`IMAGE_JOB_ID` 和 `IMAGE_TITLE`。命令成功且 `IMAGE_OUTPUT` 文件存在时，任务会记录为“已生成”。
