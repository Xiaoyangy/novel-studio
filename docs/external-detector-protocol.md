# 外部检测器人工抽查与精确登记协议

朱雀等检测器是网页工具，**只由用户手工打开、提交检测并告知结果**。系统与助手绝不打开、粘贴、提交或操作朱雀网页，也不识别、处理或提交验证码；任何此前或临时许可都不构成网页自动化授权。系统只校验用户报告的 detector、mode、分值、证据与精确 payload SHA，并把通过校验的报告登记为抽查事件。外部结果不是生产必填项，也不是章节永久属性：未抽查应标记为 `not_sampled / unknown`，不能伪造成“已通过”，但不会阻塞生产；若用户报告并登记的当前精确 SHA 分值达到 `4%`，则只阻断该版正文并触发一次整章返工。替换稿取得新 SHA 后不继承旧分数，也不要求用户复测；它只需通过本地 AIGC、当前 SHA 的 DeepSeek、Editor / review 与 exact-body hard consistency 自动门禁。

## 标准流程

1. 抽查某章时先冻结实际 payload。整篇单段检测不要在计算 SHA 后再删除标题、改变换行或改字；可以抽查正式章 `chapters/NN.md`，也可以抽查受 checkpoint 管理的候选 `drafts/NN.draft.md`。
2. 先计算 payload SHA-256；用户再把同一组字节整篇手工提交到网页检测器，并保存截图或报告。系统与助手不打开或操作该网页。
3. 用户告知结果后，用显式分值尺度、`--result-source user_reported`、detector、mode、payload 和预期 SHA 校验并登记。脚本只处理用户报告，不会访问平台或替用户提交正文。
4. 若用户报告的抽查分值在当前精确 SHA 上达到 `4%`，该事件触发一次整章返工。登记脚本只追加审计事件；下一次正常 `--stages write` 或 `--stages rewrite` 会在目标完成早退之前自动扫描当前正式字节，并把高分章节幂等合入 `pending_rewrites`，不需要用户另跑 review、逐章复测或重复提醒。返工会保留旧事实结果并重做正文；同一触发事件不会反复消耗渲染预算。
5. 正文换成新 SHA 后，旧抽查结果只保留为历史触发证据，不产生 detector/mode 复测义务，也不要求用户再次访问平台。
6. 新 SHA 依靠自动门禁闭环：本地 AIGC、DeepSeek 裸正文严格 `<4%` 且 `blocking=false`、Editor / review，以及 exact-body hard consistency。全部自动证据通过即可 consistency / commit / deliver。
7. 平台结果缺失保持 `not_sampled / unknown`；低分只是一条抽查记录，不能替代自动门禁；身份不齐也不会形成 named freeze 或 `rejudge_pending`。
8. 用户后来若主动抽查新 SHA 并报告新的高分，它是一个新的精确字节事件，可再触发一次整章返工；系统仍只登记，不调用网页。

## 当前青山县运行快照（2026-07-16）

第一章活动候选是实际文件 `drafts/01.draft.md`，SHA-256 为 `c2c1c36243c086a296aec6d8ca5eef7c35e6072f6b33087f1bd87f9307587a0f`。该同稿在运行时 edit gate 中得到 `raw_local_gate_percent=2.54%`，当前 Python 审计入口独立复算为 `2.75%`，两者都严格 `<4%`；DeepSeek 裸正文结果为 `human_like / low / 2%`、`blocking=false`。这些自动证据仍保留为同哈希审计记录，但用户随后报告该精确字节的整篇单段朱雀抽查值为 `0.82`（即 `82%`），该事件已按 `user_reported` 登记，因此只否决 `c2c1...` 这一版并触发一次整章返工，不能继续把它当作待 consistency / commit 的通过候选。

当前 `c2c1...` 的 `zhuque/novel-whole-text-single-segment` 状态是“用户报告 `0.82`，已触发该版一次整章返工”，不是“未抽查 / 未知”，也不是要求后续每个版本继续复测的长期门禁。系统与助手绝不调用朱雀网页或处理验证码，即使此前取得过许可也不自动化；只校验和登记用户主动报告的结果。下一步是生成新 SHA 的替换稿，并仅依靠本地 AIGC、同哈希 DeepSeek、Editor / review 与 exact-body hard consistency 自动闭环；新 SHA 不等待朱雀，也不继承 `0.82`。旧第一章 `0.86` 与第二章 `0.83` 仍只绑定各自旧正式正文 SHA，继续作为各自一次返工触发证据。

## 登记命令

~~~bash
PROJECT='data/runs/只许把钱花在青山县/output/novel'

# 先核对实际提交字节；下面的 expected SHA 只能用于字节完全一致的文件。
shasum -a 256 "$PROJECT/chapters/01.md" "$PROJECT/chapters/02.md"

python3 quality/audit/scripts/register_external_detection.py \
  --project "$PROJECT" --chapter 1 \
  --detector zhuque --mode novel-whole-text-single-segment \
  --score 0.86 --score-scale probability --verdict ai_like \
  --result-source user_reported \
  --payload-file chapters/01.md \
  --expected-sha256 e3b1ef178aebf1f822a7a15a6a746cec84d28028900b848e9093905c09131399 \
  --note '2026-07-15 用户整篇单段检测 0.86'

python3 quality/audit/scripts/register_external_detection.py \
  --project "$PROJECT" --chapter 2 \
  --detector zhuque --mode novel-whole-text-single-segment \
  --score 0.83 --score-scale probability --verdict ai_like \
  --result-source user_reported \
  --payload-file chapters/02.md \
  --expected-sha256 b798734cc0c5e932162b4d6d77b6dacc18188f220d3020e868b6225a429fc74c \
  --note '2026-07-15 用户整篇单段检测 0.83'
~~~

用户也可以主动抽查当前候选；这不是 commit 前置步骤。登记候选时，payload 必须就是实际 `drafts/NN.draft.md`：

~~~bash
DRAFT_SHA=$(shasum -a 256 "$PROJECT/drafts/01.draft.md" | awk '{print $1}')

# 下面的 0.02 只演示登记格式，不是当前候选的朱雀结果；没有用户告知的真实分数与证据时不得运行。
python3 quality/audit/scripts/register_external_detection.py \
  --project "$PROJECT" --chapter 1 \
  --detector zhuque --mode novel-whole-text-single-segment \
  --score 0.02 --score-scale probability --verdict human_like \
  --result-source user_reported \
  --payload-file drafts/01.draft.md \
  --expected-sha256 "$DRAFT_SHA" \
  --note '格式示例：整章重渲染候选，同一 detector/mode 真实抽查'
~~~

候选草稿登记是严格的抽查证据入口，不是任意文件入口。脚本会 fail closed 地验证：

- `--result-source` 必须显式为 `user_reported` 或 `manual`，不会接受浏览器/自动化来源；
- payload 是实际 `drafts/NN.draft.md`，并与登记时计算的 SHA 逐字节一致；
- 当前候选由最新 `draft` 或 `edit` checkpoint 精确绑定；
- `drafts/NN.draft_write_intent.json` 不存在，写入 saga 已完整收口。

它不要求 named marker、`rejudge_pending` 或同 detector/mode 复测合同，也允许抽查仍被本地门禁阻断的受管草稿；抽查结果永远不能覆盖本地、DeepSeek 或 consistency 结论。草稿复制品、手改但没有 checkpoint 的草稿和写入中的草稿仍会被拒绝。

有截图或 PDF 时追加 `--evidence <文件路径>`。脚本会记录证据路径和 SHA；不要再把截图路径仅写进 `--note`。正式稿登记仍允许使用提交副本，但副本必须与 `chapters/NN.md` 逐字节一致；候选稿登记只接受实际 `drafts/NN.draft.md` 路径，不接受复制品。若用户在网页使用了去标题、改换行或其他转换后的文本，那组字节不能登记成受管正文的抽查结果，除非它先成为实际正式正文或候选正文并取得自己的 SHA。

`--score-scale` 不可省略：`--score 0.86 --score-scale probability` 表示 `86%`，而 `--score 0.86 --score-scale percent` 表示 `0.86%`。新登记还会拒绝空 detector/mode、非 64 位十六进制 SHA、`human_like >=4%`、`ai_like <4%`、损坏的既有日志和不一致的 `score_percent`。历史无 `score_scale` 行只保留读取兼容，不应继续照旧格式写入。

## 多平台抽查与事件覆盖规则

- 日志按 `(detector, mode)` 和正文 SHA 保存；另一平台或另一模式的低分不会改写朱雀原始抽查记录。
- 当前精确 SHA 的任一用户报告高分都可触发一次整章返工；同一事件重复登记保持幂等。
- 正文一旦改变，旧事件只保留为返工证据，不再是新正文分数，也不会生成逐 identity 复测义务。
- 单纯缺少某个平台结果、只有部分 identity、或所有平台都未抽查，不会形成阻塞；若已登记当前精确 SHA 的高分，则仍按上条阻断该版并触发一次整章返工。
- 平台低分只表示该次抽查未触发返工，不能把本地或 DeepSeek 的 blocking 结果改成通过。

## 抽查与校准

每卷可抽 3—5 章，至少包含 1 章返工稿。登记后运行：

~~~bash
python3 quality/audit/scripts/calibration_report.py \
  --external-log "$PROJECT/meta/external_detection_log.jsonl"
~~~

相关性报告用于发现本地代理漂移，只提供校准建议，不会自动降低本地或 DeepSeek 的严格 `<4%` 自动门槛。

DeepSeek 的生产门槛固定为严格 `<4%`；本地 raw whole-text gate 是另一份独立自动证据。人工平台用 `4%` 作为用户报告 verdict 的一致性边界和一次性返工触发线，不是逐 detector 的交付门槛；平台低分不会自动降低本地或 DeepSeek 门禁，平台缺失也不会阻塞生产。

## 平台证据保留

保留 `reviews/`（八维评审与机械门禁）、返工前后 diff、精确 payload、外部检测 evidence、`external_detection_log.jsonl`、checkpoints 和 prompt manifest。平台申诉或复核时，这些文件共同证明创作与返工过程；单独一张分数截图不能证明它对应哪版正文。
