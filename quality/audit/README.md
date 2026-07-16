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
# 外部检测器用户人工抽查登记；先冻结并核对实际提交 payload 的 SHA。
PROJECT='/absolute/path/to/output/novel'
EVIDENCE='/absolute/path/to/detector-report.png'
PAYLOAD="$PROJECT/chapters/01.md"
SHA="$(shasum -a 256 "$PAYLOAD" | awk '{print $1}')"
python3 quality/audit/scripts/register_external_detection.py \
  --project "$PROJECT" --chapter 1 \
  --detector zhuque --mode novel-whole-text-single-segment \
  --score 0.86 --score-scale probability --verdict ai_like \
  --result-source user_reported \
  --payload-file chapters/01.md --expected-sha256 "$SHA" \
  --evidence "$EVIDENCE"
```

校准语料与 judge 漂移冻结样本在 `quality/calibration/`（human/llm/mixed 清单 + golden_reviews）。

小说生产门槛由本地 AIGC、当前 SHA 的 DeepSeek 严格 `<4%`、Editor / review 与 exact-body hard consistency 组成；朱雀等人工平台只做用户抽查，不是交付必填项。未抽查 / 未知不阻塞生产；用户报告并登记的当前精确 SHA 达到 `4%` 时，只阻断该版并触发一次整章返工，替换稿不要求用户复测。`--score-scale` 必须显式填写：`0.86 + probability = 86%`，`0.86 + percent = 0.86%`；`--result-source` 也必须显式为 `user_reported` 或 `manual`，脚本不接受浏览器自动化来源。登记脚本要求非空 detector/mode、64 位预期 SHA，并将 `score_percent`、payload 路径、result source 及 evidence SHA 追加到 `meta/external_detection_log.jsonl`；正式稿 payload 必须与 `chapters/NN.md` 精确同字节，候选抽查只接受实际 `drafts/NN.draft.md`、已收口 write intent 和最新精确 draft/edit checkpoint，任意副本不能登记。相同抽查事件重复登记保持幂等；损坏日志会拒绝继续登记，不能被当作生产通过证据。完整流程见 [外部检测器人工抽查与精确登记协议](../../docs/external-detector-protocol.md)。

注册平台结果按 `(detector, mode, body_sha256)` 独立保存，另一平台的低分不会改写原始高分记录。用户报告的当前精确 SHA 分值达到 `4%` 时触发一次整章返工；下一次正常 write / rewrite 会自动把它合入 `pending_rewrites`，不要求用户另跑 review、逐章复测或重复提醒。正文换成新 SHA 后，该事件只保留为历史证据，不创建逐 identity 复测义务。新稿通过本地门禁、当前 SHA 的 DeepSeek 和 hard consistency 后即可 commit / deliver，平台缺失保持 `not_sampled / unknown`，不会进入 `rejudge_pending` 或 named freeze；平台低分也不能替代自动证据。DeepSeek blocking 分支要求返回完整证据与修改建议，建议缺失不会写入有效缓存。`codex-local-aigc-v4` 新增叙事动力检查，覆盖对白传送带、动作开场标签同构、POV 内在体验薄、流程语汇和情绪范围过平。

`scripts/` 和 `references/` 是审核能力的唯一源目录。`skills/review/SKILL.md` 只保留 agent 流程说明；`novel-studio skills export --to <dir>` 会在导出产物里按需装配这些脚本和参考资料。

## 服务集成

进度看板（`services/dashboard/`）为只读服务，不再导入审核脚本；本目录脚本直接命令行调用。环境变量覆盖：

- `NOVEL_STUDIO_AUDIT_SCRIPTS`
- `NOVEL_STUDIO_OUTPUT_ROOT`
- `NOVEL_STUDIO_SHORT_STORY_DATA`
