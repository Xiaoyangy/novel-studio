# audit

本目录是本地审核能力的规范入口，聚合 AIGC、AI 味、重复、内容逻辑和错别字检查。

## 脚本

```bash
python3 quality/audit/scripts/aigc_value.py <正文路径> --target 4
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
# 外部检测器人工抽检登记；先冻结并核对实际提交 payload 的 SHA。
PROJECT='/absolute/path/to/output/novel'
EVIDENCE='/absolute/path/to/detector-report.png'
PAYLOAD="$PROJECT/chapters/01.md"
SHA="$(shasum -a 256 "$PAYLOAD" | awk '{print $1}')"
python3 quality/audit/scripts/register_external_detection.py \
  --project "$PROJECT" --chapter 1 \
  --detector zhuque --mode novel-whole-text-single-segment \
  --score 0.86 --score-scale probability --verdict ai_like \
  --payload-file chapters/01.md --expected-sha256 "$SHA" \
  --evidence "$EVIDENCE"
```

校准语料与 judge 漂移冻结样本在 `quality/calibration/`（human/llm/mixed 清单 + golden_reviews）。

当前小说交付门槛为严格 `<4%`，`4%` 也不通过。`--score-scale` 必须显式填写：`0.86 + probability = 86%`，`0.86 + percent = 0.86%`。登记脚本要求非空 detector/mode、64 位预期 SHA，并将 `score_percent`、payload 路径及 evidence SHA 追加到 `meta/external_detection_log.jsonl`；正式稿 payload 必须与 `chapters/NN.md` 精确同字节。命名平台注册合同触发返工后，候选复测只接受实际 `drafts/NN.draft.md`，并额外核验同 detector/mode、已收口 write intent、精确 draft/edit checkpoint、无更新的结构阻断和真正的 `rejudge_pending`，任意副本不能登记。正式稿同一事件重复登记保持幂等；候选稿一旦该 identity 已有结果就不再处于 pending，重复命令会明确拒绝。损坏日志始终 fail closed。完整流程见 [外部检测器人工抽检与同稿复测协议](../../docs/external-detector-protocol.md)。

注册平台结果按 `(detector, mode)` 独立覆盖：另一平台的低分不能清除当前高分。外部高分触发重渲染后，若精确中间稿仍由本地确定性 whole-text/segment 门禁复现结构失败，pipeline 会先消耗有界整章重渲染预算；注册 identity 只延后、不删除。本地结构门禁干净的候选必须让所有 identity 在该 SHA 上分别复测到 `<4%`，之后还需要当前 SHA 的 DeepSeek 裸正文独立判断和正式 review。DeepSeek blocking 分支要求返回完整证据与修改建议，建议缺失不会写入有效缓存。`codex-local-aigc-v4` 新增叙事动力检查，覆盖对白传送带、动作开场标签同构、POV 内在体验薄、流程语汇和情绪范围过平。

`scripts/` 和 `references/` 是审核能力的唯一源目录。`skills/review/SKILL.md` 只保留 agent 流程说明；`novel-studio skills export --to <dir>` 会在导出产物里按需装配这些脚本和参考资料。

## 服务集成

进度看板（`services/dashboard/`）为只读服务，不再导入审核脚本；本目录脚本直接命令行调用。环境变量覆盖：

- `NOVEL_STUDIO_AUDIT_SCRIPTS`
- `NOVEL_STUDIO_OUTPUT_ROOT`
- `NOVEL_STUDIO_SHORT_STORY_DATA`
