# 外部检测器人工抽检与同稿复测协议

朱雀等检测器是网页工具，**只人工触发、人工登记，不做自动化提交**。外部结果是“某个 detector/mode 对某组精确字节的事件”，不是章节永久属性；没有精确 payload SHA 的结果不能放行或阻断当前正文。

## 标准流程

1. 初次抽检先冻结 `chapters/NN.md` 这份正式正文。整篇单段检测时，不要在计算 SHA 后再删除标题、改变换行或改字。
2. 先计算正式正文 SHA-256，再把同一组字节整篇提交到网页检测器，并保存截图或报告。
3. 用显式分值尺度、detector、mode、payload 和预期 SHA 登记。脚本只登记，不会替你提交正文。
4. 当前 SHA 的任一注册平台结果达到 `4%` 就会使旧 review/delivery 失效；重跑 review，生成 `external_aigc_ratio`、语义 checkpoint 和持久复测合同。
5. 按 review 做整章重渲染。若精确新 SHA 仍被本地确定性 whole-text/segment 门禁判为结构失败，先按有界预算继续整章重渲染或重做因果计划，不浪费一次网页复测；这个本地失败必须绑定当前字节并可重复计算，不能靠手工改 marker 跳过平台。
6. 本地结构门禁干净后，先完成同 SHA 的 DeepSeek 裸正文独立判定；必须 `blocking=false` 且分值严格 `<4%`。这一步先于人工网页复测，避免把仍被异模型阻断的中间稿送进平台。
7. DeepSeek 通过后，再对最终候选 SHA 按合同中的每个 detector/mode 提交精确 payload；另一平台或另一模式的低分不能替代。全部命名 identity 都严格 `<4%` 后，完成同 SHA 的正式 review；只有统一 gate 为 `approved` 才能交付。
8. 全部命名平台已对当前 SHA 通过后进入 named freeze。除新的显式整章重渲染请求或新的确定性 blocking 事件外，只允许继续 consistency / commit / deliver，不得再“顺手润色”并作废复测。

## 当前青山县运行快照（2026-07-16）

第一章 active candidate 是实际文件 `drafts/01.draft.md`，SHA-256 为 `c2c1c36243c086a296aec6d8ca5eef7c35e6072f6b33087f1bd87f9307587a0f`。该同稿在运行时 edit gate 中得到 `raw_local_gate_percent=2.54%`，当前 Python 审计入口独立复算为 `2.75%`，两者都严格 `<4%`；DeepSeek 裸正文结果为 `human_like / low / 2%`、`blocking=false`。这些证据只说明候选已经到达命名平台复测边界，不能替代朱雀。

当前 `zhuque/novel-whole-text-single-segment` 的同哈希结果尚未取得或登记，网页流程停在验证码许可边界。未经用户明确许可，不处理或提交验证码；因此本候选仍为 `rejudge_pending`，不得写成“朱雀已通过”，也不得 commit / deliver。现有 `drafts/01.hard_consistency.json` 仍绑定上一版 `d723...` 失败稿，不能跨 body epoch 复用；只有当前 SHA 的朱雀结果通过并登记后，才能重跑 consistency 生成 `c2c1...` 的 passed receipt。旧第一章 `0.86` 与第二章 `0.83` 只绑定各自旧正式正文 SHA，继续作为返工触发证据，不是这个候选的分数。

## 登记命令

~~~bash
PROJECT='data/runs/只许把钱花在青山县/output/novel'

# 先核对实际提交字节；下面的 expected SHA 只能用于字节完全一致的文件。
shasum -a 256 "$PROJECT/chapters/01.md" "$PROJECT/chapters/02.md"

python3 quality/audit/scripts/register_external_detection.py \
  --project "$PROJECT" --chapter 1 \
  --detector zhuque --mode novel-whole-text-single-segment \
  --score 0.86 --score-scale probability --verdict ai_like \
  --payload-file chapters/01.md \
  --expected-sha256 e3b1ef178aebf1f822a7a15a6a746cec84d28028900b848e9093905c09131399 \
  --note '2026-07-15 用户整篇单段检测 0.86'

python3 quality/audit/scripts/register_external_detection.py \
  --project "$PROJECT" --chapter 2 \
  --detector zhuque --mode novel-whole-text-single-segment \
  --score 0.83 --score-scale probability --verdict ai_like \
  --payload-file chapters/02.md \
  --expected-sha256 b798734cc0c5e932162b4d6d77b6dacc18188f220d3020e868b6225a429fc74c \
  --note '2026-07-15 用户整篇单段检测 0.83'
~~~

正式稿因高分完成整章重渲染后，不要先把候选提交成正式章。平台复测可以直接登记当前候选，但 payload 必须就是实际 `drafts/NN.draft.md`：

~~~bash
DRAFT_SHA=$(shasum -a 256 "$PROJECT/drafts/01.draft.md" | awk '{print $1}')

# 下面的 0.02 只演示登记格式，不是当前候选的朱雀结果；没有网页真实分数与证据时不得运行。
python3 quality/audit/scripts/register_external_detection.py \
  --project "$PROJECT" --chapter 1 \
  --detector zhuque --mode novel-whole-text-single-segment \
  --score 0.02 --score-scale probability --verdict human_like \
  --payload-file drafts/01.draft.md \
  --expected-sha256 "$DRAFT_SHA" \
  --note '格式示例：整章重渲染候选，同一 detector/mode 真实复测'
~~~

候选草稿登记是一个严格桥接，不是任意文件入口。脚本会 fail closed 地同时验证：

- `reviews/drafts/NN_full_rerender_required.json` 是命名平台注册合同，并且明确包含本次完全相同的 detector/mode；
- marker 建议完整、指向旧 SHA，当前状态处于 `rejudge_pending` 边界，而不是旧稿仍可重渲染的 `rerender_authorized`；
- 当前候选由精确 `draft` checkpoint 绑定；若它来自“已通过哈希后仅允许一次”的定点打磨，也可由精确 `edit` checkpoint 绑定。两种情况下，之后都不能再有 `draft-structural-block`；
- `drafts/NN.draft_write_intent.json` 不存在，写入 saga 已完整收口；
- 当前 SHA 尚无该 detector/mode 的登记结果，也没有会把门禁重新切回 `rerender_authorized` 的平台高分。

因此，草稿复制品、手改但没有 checkpoint 的草稿、身份不匹配的 marker、本地仍阻断的中间稿和写入中的草稿都会被拒绝。不要手工伪造或改写 marker/checkpoint 来绕门禁。

有截图或 PDF 时追加 `--evidence <文件路径>`。脚本会记录证据路径和 SHA；不要再把截图路径仅写进 `--note`。正式稿登记仍允许使用提交副本，但副本必须与 `chapters/NN.md` 逐字节一致；候选稿登记只接受实际 `drafts/NN.draft.md` 路径，不接受复制品。若网页必须使用去标题、改换行或其他转换后的文本，应先让那组字节成为受门禁管理的正式正文或候选正文，再按新 SHA 检测；转换副本不能登记成当前正文的硬门禁结果。

`--score-scale` 不可省略：`--score 0.86 --score-scale probability` 表示 `86%`，而 `--score 0.86 --score-scale percent` 表示 `0.86%`。新登记还会拒绝空 detector/mode、非 64 位十六进制 SHA、`human_like >=4%`、`ai_like <4%`、损坏的既有日志和不一致的 `score_percent`。历史无 `score_scale` 行只保留读取兼容，不应继续照旧格式写入。

## 多平台与事件覆盖规则

- 日志按 `(detector, mode)` 分组；同一正文 SHA 下，每组只由该组后续结果覆盖。
- `zhuque/whole=86%` 后追加 `other/paragraph=2%`，朱雀高分仍然阻断。
- 只有同 SHA 的后续 `zhuque/whole <4%` 才能解除该 identity 的当前高分。
- 多个 identity 同时高分时，`required_external_retests` 会全部保留；换稿后缺任意一项复测都不能交付。
- 本地 whole-text/segment 结构阻断只能把注册复测延后到可用候选，不能删除 identity、替代平台分数或批准提交；中间稿一旦不再复现该阻断，所有缺失复测立即恢复为 `rejudge_pending`。
- 正文一旦改变，旧事件保留为返工证据，但不再是新正文分数。

## 抽检与校准

每卷可抽 3—5 章，至少包含 1 章返工稿。登记后运行：

~~~bash
python3 quality/audit/scripts/calibration_report.py \
  --external-log "$PROJECT/meta/external_detection_log.jsonl"
~~~

相关性报告用于发现本地代理漂移，只提供校准建议，不会自动降低当前严格 `<4%` 门槛。

DeepSeek 和每个命名 detector/mode 的运行门槛固定为严格 `<4%`；旧 review、marker 或配置中持久化的 threshold 字段只用于诊断兼容，不能把门槛改成 5%、10% 或其他值。本地 raw whole-text gate 是独立证据，也不会因为 DeepSeek 或平台低分自动降级。

## 平台证据保留

保留 `reviews/`（八维评审与机械门禁）、返工前后 diff、精确 payload、外部检测 evidence、`external_detection_log.jsonl`、checkpoints 和 prompt manifest。平台申诉或复核时，这些文件共同证明创作与返工过程；单独一张分数截图不能证明它对应哪版正文。
