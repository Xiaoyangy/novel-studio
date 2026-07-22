# 小说生产流水线与渲染风格审计（2026-07-22）

## 结论

项目的核心推演路线保持不变：全书章纲只负责长程导航，当前弧在隔离环境中完成正式世界模拟、人物决策、POV 知识边界与因果投影，随后封存；正文阶段只能消费已经提升的单章冻结合同。此次优化没有把外部方案中的动态大纲、剧情树搜索、MCTS 或二次情节规划引入渲染层。

本轮修复的中心是把“风格资产存在”升级为“Drafter 与正式 Editor 实际消费同一份、可审计、不可漂移的表达合同”，并让这份合同进入候选、调度许可、正文事务、正式审核和恢复链。

## 端到端流水线

```text
用户规则 / foundation / 人物 / 世界 / 全书章纲
        ↓
architect → outline-all
        ↓
当前弧 preplan
        ↓
project-all：FormalWorldSimulationV2 + POVPlanV2 + ProjectedDelta
        ↓
render-capacity 校验 → seal 当前弧
        ↓
promote 单章（机械提升，不调用模型）
        ↓
冻结 ChapterPlan + frozen render context
        ↓
隔离候选 v3-pre-style
        ↓
编译 surface-only style + accepted-prose serial memory
        ↓
有效风格回执 → 候选升级 v3-effective-style
        ↓
dispatch authorization / 一次性 prose permit
        ↓
Drafter → deterministic gates → commit
        ↓
Editor + DeepSeek exact-body review
        ↓
actual-delta 独立复算
        ↓
目录原子发布 → chapter acceptance → arc completion
```

现有 `project-all → seal → promote → render → exact-body review → actual match → publish` 主链没有改变。`deliver` 仍是本地沉淀与交付包，不是微信公众号、头条等外部平台发布器。

## 不可改动的推演边界

以下数据继续由上游推演独占，风格层没有写权限：

- `FormalWorldSimulationV2` 的初态、角色选择、因果步骤、反事实和终态；
- `POVPlanV2` 的已知/未知信息、人物动机、视角边界和场景因果目的；
- `ProjectedDelta`、义务注册表、hard render contract；
- 章节 causal beats、decision points、事实顺序、人物状态和世界规则；
- sealed generation、promotion receipt 和 frozen render context 原始字节。

风格层只允许改变叙述声音、叙述距离、词汇、语域、句法、节奏、意象、感官、修辞、段落组织、留白和对白表面。风格文件中诸如冲突设计、线索数量、关系推进、能力限制和剧情钩子的条目不会进入 render-only 风格合同。

## 本轮实现

### 1. 选中风格真正进入正文上下文

`assets.Bundle.ResolveStyle` 原子解析有效 style ID 与正文；`Load` 使用同一个有效 ID 选择题材 references。首尾空白、空配置或不存在的 ID 不会再形成“fantasy 正文 + default references”之类的混合输入，等价的 default fallback 也不会无意义地改变 run/project-all/render 身份。run/render digest 只绑定本次实际选中的 style，而不是整个未消费的 style catalog；project-all digest 不再绑定 render-only style ID/body，只绑定它真正消费的 planning prompt 与 references。选中的样式正文及风格协议仍进入 render input digest；真实渲染输入变化会产生新的候选身份，不能借旧缓存继续生成。

`ContextTool.WithConfiguredStyle` 只在 draft/full 表达上下文编译样式，不进入 planning 或 world simulation。Markdown 规则同时检查表层字段 allowlist 与规则正文；未知标签，以及借“叙述声音/节奏”等合法标签夹带新增事件、改写决定、杀死/复活角色、每章强制揭示等语义指令，都会失败闭锁。

### 2. 已验收正文形成独立的 serial style memory

风格统计不再计算后丢弃。目标章之前、已经进入 `CompletedChapters` 的正式正文按章号读取并计算：

- 最近高频短语；
- 跨章重复完整句；
- 过密的固定句式类别；
- 开篇时间模板集中度；
- 短句斩断式结尾集中度。

这份 memory 只是一组可验证的表层复读事实，不是节拍配方。每个来源章的 exact body SHA-256 都进入回执；历史正文漂移会使回执失效。统计不足时执行 replace-or-delete，冻结 base context 里可能存在的旧 memory 不会泄漏到新候选。

统计器会先剥离每章第一个规范标题行（Markdown heading 或 `第N章 标题`），再计算句式、短语、复句、开篇和结尾，标题中的“清晨/黎明”与重复书名不会污染 prose memory。高频短语 stopwords 除人物与 cast 外，还纳入 BookWorld 的世界名、地点、势力/别名，以及 WorldCodex 的书名、能力等级/别名、技能域、种族、武器/装备类别与品级；描述、规则和约束等自由文本不进入 stopwords，避免把真正的写作复读一并过滤。

这次确定性投影变化将 serial compiler 升级为 v2；旧回执不会被误认成新算法的恢复结果。

### 3. Drafter 与 Editor 共用有效风格回执

`effective_render_style_contract.json` 绑定：

- generation、chapter、plan digest、plan checkpoint sequence；
- frozen base render context SHA-256；
- pipeline render input digest；
- projected bundle 和 promotion receipt；
- candidate ID；
- style ID、style asset SHA-256、style protocol；
- canonical `style_contract` 原始 JSON 字节及其 SHA-256；
- serial memory 来源章及正文 SHA-256；
- 回执自身 digest。

外层回执使用 compact JSON 持久化，避免 `json.MarshalIndent` 改写嵌套 `RawMessage` 空白后造成“刚写入就无法通过自身哈希”的问题。

同一 CandidateID 已有回执时恢复是 load-only：修改当前 style asset 不会重编旧候选。新候选可以替换从 live tree 复制来的旧 CandidateID 回执，但必须重新生成并绑定自己的身份。

候选状态明确分为：

```text
v3-pre-style
  - 只允许机械复制和崩溃恢复
  - 不能取得 prose permit
        ↓ 原子绑定 receipt digest
v3-effective-style
  - Drafter / Editor / dispatch / convergence 可消费
  - receipt 缺失、损坏或身份漂移均失败闭锁
```

真正的 v1/v2 历史候选仍可从 frozen packet 读取当时内嵌的 style contract；它们不会叠加当前资产。

为了防止恢复时把已经进入 v3 的候选重新标成 v2，每个 v3 CandidateID 会先在候选目录之外写入一次性、不可变的 `style-epochs/<CandidateID>.json` 意图回执。它绑定 generation、chapter、plan、bundle、promotion、render input 和 render context，采用 `O_EXCL` 创建并同步目录；重复创建只能接受完全相同的 canonical bytes。即使候选 manifest、事务或 frozen plan 的可变字段被删除，命令层和 provider-side prose permit 都会依据这份回执拒绝 v3→v2 降级。

真正的 legacy v1/v2 候选若在恢复时遇到当前输入漂移，只能走不调用任何 provider 的 exact-body 收口路径；需要 Writer、Editor、DeepSeek 或新的 dispatch 时立即失败，不能以旧 CandidateID 消费当前 provider/prompt 配置。

### 4. 风格身份进入正文证据链

正式 v3 候选清单新增：

- `pipeline_render_input_digest`；
- `render_context_sha256`；
- `effective_style_receipt_digest`。

三项同时进入：

- convergence ledger；
- dispatch authorization 与 dispatch ledger；其中还绑定精确的 `source_output_dir`，同一父目录下的另一个真实工程也不能复用授权；
- one-shot prose permit；
- `body_ready` transaction evidence；
- Editor/DeepSeek model provenance；
- formal review artifacts 和 freshness 检查。

任何一处 digest 缺失、部分存在、被篡改或与 receipt 不相等，都会在正文 provider、正式审核或恢复之前失败。

逐章验收协议也完成了显式分代：历史 `chapter-acceptance-receipt.v2` 必须完全不带风格证据；当前 `chapter-acceptance-receipt.v3-effective-style` 必须同时绑定风格归档路径、回执 digest 和归档文件 SHA-256，并精确绑定六项正式审核工件（Editor JSON、报告 Markdown、机械 AI gate、AI 声纹红旗、DeepSeek judge、model provenance），缺项、替换或额外路径都拒绝。最终 render 回执还必须绑定该 acceptance digest，并与正文、outcome、事务 `body_ready` 证据逐项一致。v3 frozen identity 在 manifest 或事务丢失后不允许回落到历史验收协议。

### 5. 正式 Editor 与 Drafter 同源

正式 Editor 不再只依靠硬编码通用提示。它读取和 Drafter 相同的 canonical style bytes，并被明确限制为只评价表达，不得反向提出新增、删除、调序或改写冻结事件、人物决定、事实、因果、状态和知识边界。

`style_contract` 使用独立、不可截断的 JSON 块；其余 plan/结果合同才使用 8 KB 预算。Editor cache policy 对实际送给 provider 的这份 payload 求哈希，不再出现“缓存绑定全量、模型只看到截断版”的身份分叉。

### 6. Editor 解析失败不再自动放行

旧路径在八维表缺失时会合成八个 80 分 `pass`，并可能把明确的“需要改写：是”降级成 accept。现在正式输出必须满足：

- 唯一且章号正确的标题；
- 唯一、合法的 `X / 40` 总分；
- 唯一且合法的改写结论；
- 唯一且非空的一句话诊断；
- 八个不重复的维度、0–5 整数分和正文证据；
- 唯一且非空的主要问题段。

缺行、重复、非法分数或格式损坏都会失败闭锁；无效响应不会进入缓存，也不会到达 `SaveReview`。合法的“需要改写：是”会保持为 `rewrite` 并进入后续反馈/返工链，即使其余维度分数较高也绝不会被软化成 accept。

### 7. 崩溃恢复与证据拓扑收紧

风格归档是 CandidateID 的不可变事实，live tree 中的 `current` 只是工作副本。正常恢复优先读取归档；若在原子发布的极小窗口内只留下 canonical `current`，系统会先验证其 sealed identity、源码字节和自身 digest，再用相同字节重建缺失归档。非 canonical、部分字段、源正文漂移或符号链接均失败闭锁。

convergence 与 dispatch 只接受活动候选的精确拓扑；正式 review 证据可额外读取已经原子退休到 `retired/<CandidateID>-*/output` 的同一候选，但会逐层执行 `Lstat`、`EvalSymlinks` 与 CandidateID 校验。这样既支持发布后的复核，也不会把任意同名目录当成可信候选。

正式 review provenance 协议升级后绑定完整八件审核工件以及 style archive/raw style contract hash。缺工件、混合 CandidateID、归档漂移或旧 provenance 都不能证明当前 v3 exact-body 已审核。

provider 边界还会在读取 frozen context、serial-memory 来源章或写入运行时许可之前，递归检查整个活动候选输出树：目录必须留在候选根内，任何后代符号链接、设备、FIFO 等特殊文件都失败闭锁。检查在上下文组装前、permit arm、availability validation 和 consume 时重复执行，防止许可落盘后再替换证据。v3 permit 在这三个边界还必须重新读取 CandidateID 对应的真实、非 symlink、canonical style-epoch marker，并逐字段重建其 sealed identity；marker 丢失、重编码、换身份或在 arm 后被删除都到不了 provider。

dispatch ledger 从 `reserved → permit_armed → provider_dispatched` 的原子替换会同步父目录；permit 创建和删除也同步 `meta/runtime`。style-epoch namespace/目录、convergence root/候选目录、有效风格 current 的原子 rename 和不可变风格归档也会逐级同步对应父目录。因此进程崩溃或断电后，已经越过 provider 边界的 reservation 不会因目录项未持久化而和旧 permit 一起“复活”。发布态 frozen identity 损坏时，review 会遍历并严格校验候选目录外的 canonical style-epoch 意图，而不是信任 manifest 中可改写的 CandidateID。扫描先按 generation/chapter/plan/checkpoint/bundle/promotion 以及可用的 render-input/context 完整身份匹配，再判断 CandidateID 冲突；同章但不同 generation/plan 的真实 legacy 历史不会被误判为降级。

非 active review 若丢失 owning manifest，或 singleton manifest 已前进到另一章，会把当前 exact body 与所有 canonical v3 intent、完整 `body_ready` transaction sealed identity 交叉验证。仍未进入 `chapter_accepted/completed` 的 v3 正文直接失败，Editor/Reviewer provider 不会按当前配置重编另一份 style bytes；已验收历史章则继续由不可变 model provenance + style archive 证明原审核，保持只读兼容。

章验收、final 与 arc completion 读取正文、六项正式 review、风格归档及其 serial-memory 来源正文时统一走 sealed-evidence reader：逐层 `Lstat`/containment 校验，leaf 使用 `O_NOFOLLOW | O_NONBLOCK`，要求普通文件且 `nlink == 1`，并在读取前后复核 inode、类型与链接数。因此，即使替换内容字节完全相同，leaf/ancestor symlink、hardlink、FIFO 或特殊文件也不能伪装成项目内不可变证据。

这是一次 fail-closed 的 provider 协议切换：升级前仍在途的 `pipeline-render-dispatch-budget.v1` / prose permit v2 缺少精确 source 绑定，不会被原地补字段或继续消费；必须从安全的 sealed 输入重新建立候选和 dispatch。已完成并正式验收的历史正文、legacy v1/v2 候选读取与 acceptance v2 不受这条在途切换规则影响。

表层规则正文门禁同时把 render style contract 升级到 v4。升级前尚未进入 provider 的在途 v3 风格回执需要以原 sealed 输入重新编译；系统不会把旧解析算法的回执冒充成 v4。

## 最新研究的取舍

| 方案 | 本项目取用部分 | 不采用部分 |
|---|---|---|
| [CritiCS, EMNLP 2024](https://aclanthology.org/2024.emnlp-main.1046/) | 表达层批评、有限轮修订、收益停止 | `CrPlan` 或任何重新规划剧情的修订 |
| [Author Writing Sheet, IJCNLP-AACL 2025](https://aclanthology.org/2025.ijcnlp-long.82/) | 有来源的结构化表层风格规则、规则去重与证据化 | plot、冲突、人物弧等语义字段进入渲染层 |
| [StyleVector, ACL 2025](https://aclanthology.org/2025.acl-long.353/) | 作为未来本地开放权重模型实验方向 | 当前闭源 provider 无隐藏层访问，不直接接入 |
| [DOME, NAACL 2025](https://aclanthology.org/2025.naacl-long.63/) | 时间/实体证据检索思想用于 continuity memory | 动态大纲和渲染时重规划 |
| [LongStoryEval, ACL 2025](https://aclanthology.org/2025.acl-long.799/) | 章节级证据与后续聚合优先于整本一次性 judge | 将十万 token 全文一次塞给单一 judge |
| [CheckEval, EMNLP 2025](https://aclanthology.org/2025.emnlp-main.796/) | 八维可验证清单和失败闭锁 | 单一模糊 Likert 总分作为发布门 |
| [SAJA, ACL Industry 2026](https://aclanthology.org/2026.acl-industry.45/) | 保留缓存/provenance，为后续真实编辑偏好校准准备 | 在没有中文人工标注集时伪造校准头 |
| [ConStory-Checker, ACL Findings 2026](https://aclanthology.org/2026.findings-acl.410/) | exact evidence、冲突位置和来源绑定的方向 | 用英语基准阈值直接替代中文项目校准 |
| [SuperWriter, ACL Findings 2026](https://aclanthology.org/2026.findings-acl.428/) | 只参考 refinement 的有限轮思想 | planning、MCTS、层级剧情搜索 |
| [Verbalized Sampling 2025](https://arxiv.org/abs/2510.01171) / [Top-H 2025](https://arxiv.org/abs/2509.02510) | 作为表达候选多样化的后续实验 | 未经内容不变量门直接替换当前生成策略 |

这些选择遵守一条原则：新技术只能增强“怎么写”，不能改变“发生什么、为什么发生、谁知道什么以及人物做了什么决定”。

## 验证矩阵

当前测试覆盖：

- 表层规则进入合同，语义规划标签和藏在合法表层标签后的事件改写指令都被排除；
- 5 章以上 accepted prose 生成 serial memory 和有序 source body hashes；
- 规范章节标题不参与 prose 统计，结构化世界专名不会进入 avoidance memory；
- style ID、style body、题材 references 与各级输入 digest 使用同一次有效解析；
- receipt compact bytes 写入后可原样加载；
- Drafter payload、Editor payload 和 receipt 的 canonical style bytes 完全相等；
- 当前资产变化不能重编同一 CandidateID；
- source body 漂移、receipt 删除/篡改、manifest/permit/ledger 漂移均失败；
- `source_output_dir` 在 authorization、ledger、permit 三层不一致时失败，即使两个 source 共用同一 `.render-candidates` 父命名空间；
- 候选树内后代符号链接在 arm 前或 arm 后都不能越过 provider 边界；
- v3 style-epoch marker 缺失、非 canonical、身份漂移、symlink 或 arm 后删除均不能越过 provider 边界；
- v3-pre-style 可恢复但不能取得 provider permit；
- immutable style epoch 与剩余 exact evidence 会在 manifest v3 字段被清空、frozen identity 丢失或事务恢复等降级场景中阻断 v3→v2 permit、review 与 final fallback，CandidateID 伪造也不能绕过；
- 非 active review 的 manifest 缺失/跨章漂移不能隐藏 exact pending v3 `body_ready`；同章不同 sealed identity 的 legacy 历史不被误伤；
- v2 历史候选保持兼容，但输入漂移时只允许 provider-free exact-body 收口；
- acceptance v2/v3 严格分代，v3 acceptance 精确绑定六项正式审核工件，v3 final 必须闭合 acceptance/body/outcome/style/transaction 证据链；
- acceptance/final/arc completion 拒绝正文、review、风格归档和 style 来源正文的 leaf/ancestor symlink、hardlink、FIFO 与特殊文件；
- 只有 canonical current 幸存时可确定性重建归档，非 canonical current 失败；
- convergence/dispatch 只认 active topology，review 仅接受经验证的 active 或 retired topology；
- Editor 的超大 style contract 不受 8 KB plan 预算截断；
- cache SHA 等于实际 provider context SHA；
- Editor 缺失/重复维度、非法分数会失败；明确 rewrite 会原样进入返工链，不会合成通过；
- candidate retirement、transaction crash replay、actual-mismatch recovery 和 dispatch budget 回归；
- 一条真实 v3 集成测试完整执行 style 编译、permit/dispatch、body-ready、正式 review provenance、actual match、目录发布、outcome、acceptance、final、completed，并证明任一审核工件篡改会使最终验证失败。

最终交付验证已执行：

```bash
go test ./... -count=1
go vet ./...
git diff --check
```

全部包通过；静态检查和补丁空白检查也均通过。

## 后续增强边界

以下增强适合继续放在冻结内容合同之外，不能修改当前推演链：

1. 2–4 个纯表达候选，只允许句法、节奏、意象、对白措辞和叙述距离变化；候选必须逐一通过相同 hard facts 和 actual-delta 门。
2. CritiCS 式 span-level 局部批评与最多两轮最小修订；每轮重新验证 beat 覆盖和事实不变量，失败自动回滚。
3. 只读 arc/book prose audit，用于发现跨章声口漂移、近邻复句、开头/结尾同构和节奏同质化；失败只能进入 expression-only edition，不能触发 Planner。
4. 使用真实中文编辑/目标读者标注训练 SAJA 式校准头，并把低置信样本转人工复核；在数据就绪前不把未经校准的 LLM judge 伪装成客观分数。
5. 若切换到可控的本地开放权重模型，再独立评估 Top-H 或 StyleVector；闭源 API 路线优先使用受合同约束的 verbalized candidates。

这些是渲染质量的增量路线，不是替换世界模拟、角色决策、POV 投影或因果推演的理由。
