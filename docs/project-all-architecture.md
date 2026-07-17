# 全书章纲先冻结、再按弧推演与渲染

> 状态：当前默认 pipeline 保留 `project-all` 这个兼容阶段名，但它的实际作用域是
> **当前整弧**，不是“从当前章一次推演到全书末章”。一弧规划封存后才能逐章渲染；
> 每章仍使用该章最终 exact body 独立审核，弧内全章通过才解锁下一弧。

## 1. 为什么改成按弧生产

全书章纲解决的是长程导航：全书有多少卷、每卷有哪些弧、每弧包含哪些章，以及核心因果和收束方向。它不等于可直接渲染的逐章正式 plan。

一次性推演所有剩余章节会让远端规划过早固化，也会忽略已渲染正文带来的真实状态结果。只在渲染前规划单章，又无法保证铺垫、转折、付清和人物变化在整弧内完整闭合。

因此当前边界是：

1. 先冻结全书章纲。
2. 只推演当前一弧的全部章节。
3. 逐章验证每个 plan 能否自然承载 `2000–3300` 中文字。
4. 把当前弧的所有规划封存成不可变工件。
5. 按章提升、渲染并对最终 exact body 独立审核。
6. 弧内全章通过、不可变回执齐全后，才以真实结果为起点推演下一弧。

按弧规划是为了跨章内容更完整，不是把正文审核改成弧级审核。

## 2. 唯一合法的阶段顺序

```text
Architect / outline-all
        ↓
冻结全书卷—弧—章纲
        ↓
当前弧 preplan
        ↓
project-all（当前弧全部章节）
        ↓
每章 render capacity 校验
        ↓
seal 当前弧
        ↓
promote 章 N → render 章 N → exact-body review 章 N → acceptance receipt
        ↓
弧内下一章，重复上一行
        ↓
弧内全章回执齐全 → arc completion receipt
        ↓
下一弧 preplan
```

弧内任一章的规划、承载力、正文、exact-body 审核或 actual outcome 未通过，都不能跳到下一弧。

## 3. 阶段职责

| 阶段 | 读取 | 产物 | 禁止事项 |
|---|---|---|---|
| `architect` / `outline-all` | premise、世界规则、人物与题材契约 | 冻结的全书卷—弧—章骨架 | 不得把粗章位冒充为可渲染 plan |
| `preplan` | 全书冻结章纲、当前 accepted canon、上一弧完成回执 | 当前弧范围、目标、弧内章位、承接义务 | 不得跨过未完成的上一弧 |
| `project-all` | 当前弧输入、冻结 canon/foundation/RAG snapshot | 弧内每章 simulation、plan、projected delta、capacity、render context | 不写正文；不连 live RAG；不推进 live canon |
| `seal` | 当前弧全部 chapter bundles 与 registry | 不可变 arc planning manifest 与 sealed generation | 任一章缺失、容量不足或因果链不连续时不得封存 |
| `promote` | 已封存的下一章 bundle、realization cursor | live frozen plan、promotion receipt | 不调用模型；不改 plan；不跳章 |
| `render` | 当前 promotion 冻结的最小渲染合同 | 候选正文、commit、本章 exact-body review、actual outcome | 不重规划；不做 live RAG；不读取未提升未来章 |
| 章验收 | 当前最终正文 SHA 与对应审核/actual 证据 | `ChapterAcceptanceReceipt` | 不得继承旧 SHA 的审核；不得以弧级审核代替 |
| 弧完成 | 弧内有序的全部章验收回执 | `ArcCompletionReceipt`、下一弧基准 | 缺章、跳章、SHA 漂移或 generation 串线时不得解锁下一弧 |

## 4. 全书章纲与弧计划的层级

系统必须明确区分三类工件：

1. **全书章纲**：冻结卷弧、章位、长程目标与收束方向，覆盖全书，但不是正式章 plan。
2. **弧规划**：从当前已验收 canon 出发，将当前弧的所有章节联合推演并封存。
3. **章渲染**：每次只消费弧规划中的一章，生成一份最终正文并独立审核。

全书章纲可以告诉第六卷某个矛盾必须收束，但第六卷的正式章 plan 不会在第一弧就被封存。当运行到该弧时，系统必须使用前一弧的真实 actual post state，而不是早期预测的假定状态。

## 5. 弧内联合推演

`project-all` 不循环调用 live `plan`。它在隔离的 ProjectedStore 中对当前弧按章号串行建立状态链：

```text
accepted actual state at arc start
        ↓
chapter N projected pre-state
        ↓
world simulation + character decisions + POV projection
        ↓
chapter N projected delta / post-state / obligations
        ↓
chapter N+1 projected pre-state
```

每个 chapter bundle 至少要绑定：

- arc scope、chapter number 和 generation identity；
- 全书章纲中对应的弧与章位；
- projected pre/post state 和前一章 digest；
- 显式 `arc_transition_contract`：上一章 outgoing consequence ID/文本、下一章逐字承接，以及精确消费它的 `causal_beats[].cause`；
- 角色目标、压力、知识边界与真实决定；
- POV 可见事实、不可见事实、必保事实和揭示预算；
- 跨章 obligation 的生成、消费、延期或跨弧 carry；
- 结构化 projected delta；
- 正式 chapter plan、render capacity 和冻结 render context；
- 所有可用外部事实与写法素材的 content-addressed receipt。

弧内任一章的前态不等于前一章后态、相邻章的显式后果没有被下一章因果拍消费，或义务的生产与消费无法闭合，都必须在 seal 之前失败。`goal`、`hook` 和语义相似度不能代替这条显式证明；turn 只来自模型写出的 `outcome_shift`，payoff 只来自 `payoff_points` 或 `ending_consequence_contract.consequence`，系统不得自动补造。

## 6. 每章 `2000–3300` 字的渲染承载力

字数不是渲染阶段的“补长度”指令，而是规划阶段的内容容量合同。每章正式 plan 都必须包含 `ChapterRenderCapacity`，其目标总字数必须落在项目硬规则 `2000–3300` 内。

每个 scene unit 需要明确：

- 场景 ID 与合理的目标字数；
- POV 当下目标；
- 正在发力的反作用或对抗；
- 至少三个可视、可写、不重复的具体行动拍；
- 场景中发生的转折；
- 退场后必须留下的后果。

高质量容量来自事件发展，而不是对同一信息的换句话重复。下列方式不能被计为有效承载力：

- 反复解释已经清楚的规则、动机或结论；
- 让人物为了凑字数继续不产生新信息的对话；
- 同义复述、总结式内心独白或流程报告；
- 与当前行动无因果关系的景物、设定或背景填充；
- 把本该由事件表现的变化改成旁白宣布。

如果章计划无法靠行动、对抗、发现、选择和后果自然达到下限，该章必须回到规划层增加真实事件，不得把空白留给 Drafter 注水。

## 7. 弧规划封存

seal 的对象是当前弧，而不是全书所有未写章节。`ArcPlanningManifest` 应聚合并绑定：

- volume/arc identity 与精确起止章；
- generation identity 与冻结全书章纲 digest；
- 当前章级字数区间与来源 `user_rules` 文件 digest；
- 弧内有序 chapter bundle digests；
- 每章 render capacity digest；
- 弧内显式后果 ID → 下一章消费因果拍、转折与付清标记；
- obligation registry 及需要跨弧携带的未结义务。

base canon、foundation、RAG snapshot、prompt/model/seed 等生成身份由 manifest 所绑定的 generation 继续承载，不在 manifest 中复制一份可漂移数据。

封存后任何字段都不得原地修改。若当前弧尚未验收的规划必须改变，应显式生成 successor generation；若要否定已验收的正史前缀，必须进入受控 rebase，不能偷改旧 bundle。

## 8. 逐章渲染与逐章审核

promote 只将 realization cursor 指向的下一章 sealed bundle 安装为 live frozen plan。它不调用 Planner 或 World Simulator，也不能跳过弧内早先章节。

render 在隔离候选工作区中执行：

1. 从冻结 render packet 生成本章正文。
2. 执行 deterministic 与 hard-consistency 前置门禁。
3. 对候选正文提交 commit。
4. 对这一章的最终 exact body 执行 fresh review。
5. 独立验证 actual delta 是否实现 sealed plan。
6. 通过后原子发布 live canon，写入本章不可变验收回执。

`ChapterAcceptanceReceipt` 必须绑定 arc ID、arc manifest digest、generation、chapter number、final body SHA、独立复算的 Unicode rune 数、fresh review artifact paths/digests 和 actual outcome receipt digest。arc manifest 再间接绑定该章 bundle、capacity 与封存字数区间。保存回执、恢复重放和弧完成都会重新读取 `chapters/NN.md`，同时校验 SHA 与实际 rune 数；正文任何字节变化、短于下限或长于上限都会使旧回执失效。已经被 acceptance 绑定的章级审核文件也不能再由 standalone `--review-existing` 原地覆盖。

不存在“弧级正文评分”取代章级审核的路径。弧级只负责验证章级验收证据是否齐全、有序且未漂移。

## 9. 弧完成与下一弧交接

只有同时满足以下条件，才能写入 `ArcCompletionReceipt`：

- 弧内每个章号都存在唯一的 `ChapterAcceptanceReceipt`；
- 回执的 arc/generation 与当前 sealed manifest 一致；
- 回执按章号连续，无缺章和跳章；
- 每个 final body SHA 仍等于 live canon 中的精确正文；
- 每个 fresh review 和 actual outcome 仍绑定同一稿；
- realization cursor 已到达弧末；
- 末章 actual post state 可作为下一弧的 base state；
- 跨弧 obligation 已按 content digest 携带，未被截断或伪造解决。

下一弧的 generation 必须绑定该完成回执、前一弧最后的 actual post state 与 carried obligation registry。这保证弧与弧之间使用的是已经真实发生的正文结果，而不是上一弧早期投影的理想化预测。

## 10. RAG 边界

RAG 只在当前弧的规划快照中使用。弧推演启动时，系统分别计算：

- `base_canon_root`：弧起点已验收的正史；
- `foundation_snapshot_root`：世界、人物、规则和规划可读台账；
- `rag_snapshot_root`：弧规划开始时复制并指纹化的本地检索工件。

project-all 可以读取这份冻结 RAG snapshot，并把命中转化成带来源回执的 `fact_anchors` 或 `craft_methods`。raw hits、召回摘要和主角不可见信息不得直接进入正文会话。

render 必须 `DisableLiveRAG`：

- 不启动 embedding 或 Qdrant；
- 不连接 live vector store；
- 不调用 `craft_recall` 或其他现场检索工具；
- 不因 live index 增长而改变 sealed plan；
- 只消费已在规划阶段转化、有回执、已封存的最小渲染输入。

因此“RAG 参与规划”不等于“Drafter 自由检索”。

## 11. 朱雀与人工外部抽查

朱雀等网页检测不属于自动生产依赖：

- 系统不调用、提交或操作朱雀；
- 只有用户主动抽查后才记录外部结果；
- 结果必须绑定被检测章节的 exact body SHA；
- 未抽查应保持 `not_sampled / unknown`，不能伪造为通过；
- 缺失朱雀分数不阻塞规划、seal、render、章验收或弧完成；
- 旧 SHA 的外部分数不得继承到新 SHA。

自动 pipeline 的硬边界是内部章级 exact-body review 与 actual-delta consistency，不是等待用户逐章手工复测。

## 12. 隔离、不可变性与恢复

下列写入必须隔离：

- ProjectedStore 不能写 live `chapters/`、progress 或 canon ledger；
- building arc generation 只能通过 CAS 逐章追加；
- sealed generation 只读；
- promotion、actual outcome、chapter acceptance 和 arc completion 回执使用内容寻址或严格唯一性；
- render candidate 在隔离目录完成 commit、review 和 actual match 之前不替换 live；
- 发布通过 journaled transaction 保证在 rename 窗口崩溃后可恢复。

恢复时不以“看起来已经写过”为依据，而是重新验证 arc/generation、bundle digest、promotion、final body SHA、fresh review、actual outcome、chapter acceptance、arc completion 与 cursor 的完整链。已提交的同一稿可以幂等补回执，不得因崩溃而重复生成正文。

## 13. restart 与 rebase

当前弧尚未验收的 plan 必须改变时，使用显式 `--restart` 建立 successor generation。这个 successor 仍只覆盖当前弧的合法范围，不会把未来所有弧一次性正式推演。

已验收正史也必须否定时，使用 `--rebase-all-chapters`：

1. 精确归档旧 live 工程并验证 archive root；
2. 在隔离候选中回到 chapter zero；
3. 保留或重新确认全书章纲；
4. 从第一弧开始 `preplan → project-all → capacity → seal`；
5. 仍按章渲染和审核，弧完成后才进下一弧。

rebase 不等于“重新封存全书正式 plan”。它改变正史基准，但不改变按弧生产边界。

## 14. 验收清单

### 全书章纲

- 所有卷、弧、章位和章号连续且已冻结。
- 每弧的目标、起止章和下一弧承接明确。
- 粗骨架未被冒充为正式 chapter bundle。

### 当前弧 seal

- arc scope 与全书章纲一致。
- 弧内每章都有完整 simulation、plan、delta、capacity 和 render context。
- 每章承载力支撑 `2000–3300` 中文字，不靠注水。
- 弧内 state/digest chain 连续。
- 本弧完成的 obligation 有明确消费者，跨弧 obligation 有可追踪 carry。
- RAG 输入来自当前弧冻结 snapshot，而非 live 检索。

### 单章 accept

- 渲染输入与 sealed bundle/promotion 一致。
- 本章最终 exact body SHA 与 commit、fresh review、actual outcome 完全一致。
- render 期间没有 live RAG、临时召回或偷偷重规划。
- 候选失败时 live canon 未被污染。

### 弧完成

- 弧内每章都有章级不可变验收回执。
- 没有以弧级正文审核替代任一章的审核。
- 所有回执仍绑定 live 中同一份 exact body。
- `ArcCompletionReceipt` 绑定弧规划、章验收链、末章 actual state 和 carried obligations。
- 只有上述项目全部通过后，下一弧才能开始。

## 15. 可以对外声称的能力边界

当上述工件真实落盘并通过校验后，可以准确说：

- 全书卷弧章骨架已冻结；
- 当前弧的所有章节已联合推演、通过承载力校验并封存；
- 正文按章渲染，每章独立完成 exact-body review 和 actual-delta 验收；
- 弧内全章通过后才会推演下一弧；
- RAG 只在弧规划冻结快照中使用，渲染不使用 live RAG；
- 朱雀只是用户可选抽查，系统不调用，缺失分数不阻塞生产。

不得声称“全书所有正式章 plan 已一次性 project-all 并 seal”，也不得声称“整弧只做一次合并正文审核”。
