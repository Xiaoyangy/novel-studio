# audit

本目录是本地审核能力的规范入口，聚合 AIGC、AI 味、重复、内容逻辑和错别字检查。

## 脚本

```bash
python3 quality/audit/scripts/aigc_value.py <正文路径> --target 5
python3 quality/audit/scripts/text_signals.py <正文路径>
python3 quality/audit/scripts/paragraph_dup.py <正文路径>
python3 quality/audit/scripts/content_lint.py <正文路径>
python3 quality/audit/scripts/typo_scan.py <正文路径>
```

09 批次（审核与 AI 味补强）新增：

```bash
# 语料驱动 slop 词表生成（A=deconstruction-library人类基线，B=自产章节；候选人工复核后进 groups）
python3 quality/audit/scripts/build_slop_lexicon.py --human deconstruction-library --llm "data/runs/*/output/novel/chapters" --out meta/slop_lexicon.json
# aigc 阈值校准报告（三组语料分布/ROC/FPR≤5% 阈值建议——proposed 语义，不自动改阈值）
python3 quality/audit/scripts/calibration_report.py --out docs/aigc-calibration-report.md
# 外部检测器人工抽检登记（朱雀小说版；流程见 docs/external-detector-protocol.md）
python3 quality/audit/scripts/register_external_detection.py --project <output/novel> --chapter N --detector zhuque --mode novel --score X --verdict human_like
```

校准语料与 judge 漂移冻结样本在 `quality/calibration/`（human/llm/mixed 清单 + golden_reviews）。

`scripts/` 和 `references/` 是审核能力的唯一源目录。`skills/review/SKILL.md` 只保留 agent 流程说明；`novel-studio skills export --to <dir>` 会在导出产物里按需装配这些脚本和参考资料。

## 服务集成

`services/short-story-dashboard/server.py` 默认从本目录导入审核脚本。可用环境变量覆盖：

- `NOVEL_STUDIO_AUDIT_SCRIPTS`
- `NOVEL_STUDIO_OUTPUT_ROOT`
- `NOVEL_STUDIO_SHORT_STORY_DATA`
