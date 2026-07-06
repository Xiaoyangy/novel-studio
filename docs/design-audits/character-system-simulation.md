# 角色系统推演设计

## 结论

角色推演不是让模型“扮演某个人”，而是把角色当成会持续变化的系统：目标、压力、资源、关系、秘密、误判、知识账本、决策框架、关系契约、情绪评价、长期弧线、行动倾向、信任/债务/伤势/暴露度都会随章节推进更新。

本项目现在把角色系统分成四个环节：

1. 写章前：`causal_simulation.initial_state` 记录角色当前系统状态和合理下一步。
2. 写章中：`voice_logic` 和 `causal_beats` 让行动/对白从状态中长出来。
3. 写章后：`commit_chapter.state_changes`、`relationship_changes`、`resource_updates` 回填目标、压力、资源、关系、秘密、误判、知识边界、决策框架、关系契约、情绪评价、长期弧线、行动倾向等变化。
4. 下一章前：`meta/character_continuity.*` 重新生成 `dynamics`、`return_plan` 和 `consistency_checks`，再通过 `novel_context` 召回。

## 网络参考

- K. M. Weiland 的角色弧线方法强调角色的目标、需要、内在谎言和变化轨迹；本项目把它落成 `current_goal`、`misbeliefs`、`state_delta_to_track`。
  - https://www.helpingwritersbecomeauthors.com/write-character-arcs/
- Reedsy 的角色弧线指南强调人物要在冲突中变化，而不是只完成情节任务；本项目把它落成写后 `state_changes` 回填。
  - https://blog.reedsy.com/character-arc/
- Generative Agents 提出观察、反思、计划三段式记忆架构；本项目对应为章节事实/台账观察、`character_continuity.dynamics` 反思、下一章 `initial_state` 计划。
  - https://arxiv.org/abs/2304.03442
- Reflexion 提出用语言反馈和记忆改进下一轮行为；本项目把审核结论、台账和 RAG 召回回注到下一章角色推演。
  - https://arxiv.org/abs/2303.11366

## 工程落点

### 写章前角色状态推演

`CharacterSimulationState` 现在包含：

- `current_goal`
- `pressure`
- `resources`
- `relationship_forces`
- `secrets`
- `misbeliefs`
- `private_boundary`
- `action_tendency`
- `likely_action`
- `state_delta_to_track`
- `knowledge_ledger`
- `decision_frame`
- `relationship_contract`
- `emotion_appraisal`
- `arc_axis`

这些字段要求 Writer 在 `plan_chapter` 时回答：这个角色现在想要什么、被什么压迫、手上有什么、欠谁什么、误以为什么、不能暴露什么、知道/不知道什么、为什么选择这一步、情绪从何而来、长期弧线被怎样测试、遇到风险会先做什么、这一章结束后哪些状态必须回填。

### 写章后角色状态回填

`commit_chapter.state_changes.field` 的推荐口径扩展为：

`goal / pressure / resource / relationship / secret / misbelief / action_tendency / emotion / trust / debt / injury / exposure / status / knowledge / decision_frame / relationship_contract / emotion_appraisal / arc_axis`

如果正文让角色恐惧、信任、债务、资源、伤势、秘密暴露度或行动倾向发生变化，就必须回填对应字段；否则下一章只能读到旧角色。

### 人物回归与续用规划

`CharacterContinuityEntry` 新增：

- `dynamics`：当前目标、压力、资源、关系牵引、秘密、误判、行动倾向、风险、下一步合理行动、冲突咬合点、知识账本、决策框架、关系契约、情绪评价和长期弧线轴。
- `return_plan`：required / near_future / optional / dormant，以及回归时必须携带的新信息。
- `consistency_checks`：写此人物前必须核对的行为一致性问题。

人物回归不再只是“多久没出场”，而是判断：他是否还有未兑现功能、状态是否已经变化、回归能否带新信息、是否可以从事件工具人升级为长期变量。

### 角色行为一致性审查

Editor 需要检查：

- 行动是否能从 `dynamics` 或 `initial_state` 推出。
- 人物是否突然知道 `knowledge_ledger` 未授权的信息。
- 关键行动是否有 `decision_frame` 支持，而不是只因大纲需要发生。
- 关系互动是否符合 `relationship_contract` 中的信任、债务、承诺和背叛阈值。
- 情绪是否有 `emotion_appraisal` 的触发源和行动后果。
- 长期变化是否落在 `arc_axis` 上，而不是突然降智、突然转性、突然忘记债务/伤势/秘密。
- 可选或休眠配角是否没有新信息却强行露脸。
- 正文改变了角色状态但没有回填到提交参数。

### 捧场/凑数角色设计

团队里只用于凑数、围观、烘托、后勤搬运或制造规模感的人，不应该进入完整人物动力学系统。否则每个“队员甲”都会被 RAG 当成可回归变量，后续章节会被无意义角色污染。

本项目用 `causal_simulation.crowd_roles` 处理这类角色：

- `group_name`：群体/职能名，例如调查队其余队员、围观租户、后勤组三人。
- `count`：人数。
- `scene_function`：本章功能，例如证明团队规模、提供反应、制造众目睽睽、承接恐怖样本、后勤搬运。
- `reaction_policy`：反应方式，例如集体后退、低声附和、沉默、互看、服从指令。
- `voice_budget`：台词预算，默认 0-2 句短反应，不用来解释设定。
- `naming_policy`：默认不命名；需要区分时用职能称呼，不引入无用正式名。
- `continuity_policy`：默认不进长期人物台账；若携带新信息、做关键选择或建立关系债务，必须升级为关键角色。
- `exit_condition`：何时退场或回到背景。

判断标准很简单：如果他只是证明“这里有一队人”，就是 crowd role；如果他改变了主角选择、掌握独占信息、欠债/背叛/承诺、后续要回归，就不是捧场角色，必须补完整 `initial_state` 五组必填字段。

## 鬼城口径

江烬不是“遇到规则就冷静破解”的静态标签。他当前系统包含：

- 失业后的风控惯性。
- 对江禾的责任。
- 不乱签字的职业创伤。
- 对交易文本、确认动作和权利边界的敏感。
- 资源不足但警惕性高。

所以他每章的合理行动应该是：先核验证据，再判断交易内容和权利边界，最后决定是否付出资源；如果江禾或活人客户受压，他也不会直接圣母救人，而是先把救援变成可确认、可收费、可审计或可隔离风险的交易。
