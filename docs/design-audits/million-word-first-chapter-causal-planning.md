# 百万字长篇第一章推演式生成检查

## 结论

推演式写法是合理的，但第一章不能只推演角色和环境。百万字长篇的第一章还要证明这本书有可持续的连载发动机、奖励循环、长线承诺、揭示预算和留存风险控制。

当前 `causal_simulation` 已覆盖：

- 角色开章状态：目标、压力、边界、可能行动
- 环境信息性：地点/物件/空间承载的信息、规则压力和状态变化
- 世界规则施压：本章实际生效的规则
- 因果节拍：触发事实 -> 角色选择 -> 世界反馈 -> 状态位移
- 信息差、选择点、章末状态变化、场景限制

本次补强后，百万字第一章还需要 `longform_opening`：

- `target_reader`：目标读者与消费期待
- `opening_hook`：第一章继续读的最短理由
- `serial_engine`：支撑百万字的长期发动机
- `reader_reward_loop`：3章、10章、30章、每卷反复给读者的奖励
- `long_range_promises`：第一章种下、后续回收/升级的长线承诺
- `reveal_budget`：第一章只露问题和证据，不提前解释答案
- `first_chapter_proof`：第一章证明长篇可持续的证据
- `retention_risks`：第一章可能流失读者的风险和规避方式

## 第一章生成前的信息是否足够

只具备角色卡、世界背景和章节大纲，不够。

还需要以下信息：

1. **目标读者和平台消费点**
   - 读者追的是恐怖规则、神豪反杀、资产经营、亲情营救、审计追债，还是人物关系。
   - 第一章必须优先兑现最核心的一两个消费点，不能平均铺开。

2. **百万字连载发动机**
   - 主角的方法能不能重复升级。
   - 敌人、地图、资源、规则、组织和关系能不能不断扩展。
   - 如果第一章只有一次性危机，没有后续扩张口，不足以支撑百万字。

3. **读者奖励循环**
   - 3章内给什么小兑现。
   - 10章内给什么阶段爽点。
   - 30章内给什么资产/关系/地图升级。
   - 每卷如何换更大的账单、敌人或经营问题。

4. **长线承诺和回收周期**
   - 第一章埋哪些长线种子。
   - 哪些在3章内回收，哪些在第一卷回收，哪些跨卷升级。
   - 种子必须有可见承载物，不能只靠抽象设定。

5. **揭示预算**
   - 第一章哪些信息必须露出来。
   - 哪些只能露痕迹，不能解释。
   - 哪些绝不能提前出现。

6. **主角方法论**
   - 主角不是“会赢”，而是用什么独特方法赢。
   - 对鬼城来说，是风控、确权、交易内容、权利边界、账单意识，而不是蛮力或系统奖励。

7. **环境信息流**
   - 每个环境元素都要承担功能。
   - 例如门牌、欠费单、黑卡、灰雾、现金退化、门缝渗字都必须传递规则信息或改变选择。

8. **留存风险**
   - 设定解释过多会劝退。
   - 爽点太晚会劝退。
   - 主角太冷但没有人味锚点会劝退。
   - 规则太复杂但没有可见代价会劝退。

## 鬼城第一章当前方向判断

整体方向合理：

- 开局危机具体：夜租欠费单、1703失败、影子被收走。
- 主角方法清楚：不开门、不替人认账、先验证交易边界。
- 世界规则有可见代价：现金失效、人格资产、姓名抵扣。
- 长篇发动机存在：黑卡交易、账单风险、资产确权、阴司审计。
- 第一章留住核心问题：江烬如何付租、名字能不能抵扣、黑卡是不是陷阱。

仍需在正式 plan 阶段补足：

- 第一卷奖励循环：3章内庇护权、10章内便利店入口、30章内七楼安全屋/医院线如何逐级兑现。
- 长线承诺表：黑卡来源、江父旧债、江禾、阴司银行、白骨财神分别在哪个周期露头。
- 揭示预算：第一章不要解释阴司银行、鬼城、白骨财神、江父旧债，只能留黑卡残字和账单空白。
- 目标读者优先级：第一章优先恐怖规则 + 合同反杀，不急着铺商会经营。
- 留存风险规避：规则要靠蒋牧失败、老钱群聊、周行舟电话、欠费单变化证明，少用作者解释。
- 人物声口逻辑：江烬不能只写“冷静”，必须写出他在每组对话中的目的、潜台词、知识边界、语域节奏和动作拍。
- 审核失败重推演：若首章 A/B 或正式审核不通过，必须把审核结论写入 `review_refinement`，再重建因果和声口计划。

## 推荐的第一章 plan 信息最低集

正式生成百万字第一章前，`plan_chapter` 至少应包含：

- `goal`：本章具体目标
- `conflict`：本章核心阻力
- `hook`：章末追读问题
- `scene_anchors`：2-4个可复核环境/物件锚点
- `causal_simulation.context_sources`
- `causal_simulation.longform_opening`
- `causal_simulation.initial_state`
- `causal_simulation.voice_logic`
- `causal_simulation.review_refinement`（返工或对照组生成时必填）
- `causal_simulation.environment_state`
- `causal_simulation.world_rules_in_force`
- `causal_simulation.information_gaps`
- `causal_simulation.causal_beats`
- `causal_simulation.decision_points`
- `causal_simulation.outcome_shift`
- `causal_simulation.scene_constraints`

只有这些信息同时存在，才算“用推演形式生成百万字小说第一章”的信息比较充分。

## 本次网络参考

- Brandon Sanderson 2025 plot lecture notes：承诺、进展、回报；Big P / little p plot；开篇前几页决定读者是否继续。
- Reedsy: How to Start a Novel：开篇需要 premise、POV、读者期待、主要角色、冲突 stakes 和 inciting incident。
- Reedsy: How to Write a Series：连载/系列需要 central plot，使单本/章节与主角线对齐。
- Writer's Digest: Chapter One / Hook Readers：第一章需要 stakes、好奇心、避免不必要 exposition、让读者投入角色旅程。
- Reedsy: How to Write Dialogue / Dialogue Live：对白要有故事功能、角色声口、场景目的和潜台词。
- Self-Refine / Reflexion / Anthropic evaluator-optimizer：失败反馈要回注下一轮生成，并设置验收和停止条件。
- Story bible / series bible 资料：长篇和系列需要持续维护角色、地点、时间线、世界规则和伏笔，防止长线一致性崩坏。
