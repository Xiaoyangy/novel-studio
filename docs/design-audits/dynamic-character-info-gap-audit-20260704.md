# 动态人物信息缺口审计（2026-07-04）

## 结论

项目已经有动态人物系统的骨架：写章前有 `causal_simulation.initial_state`，写章中有 `voice_logic` / `causal_beats`，写章后有 `state_changes` / `relationship_changes` / `resource_updates`，章审通过后会把 `character_continuity`、关系台账、资源账本、状态变化等沉淀进 RAG。

当前缺口不是“再加一个角色扮演 prompt”，而是把角色从“当前目标 + 压力 + 资源”的行动推演，升级成可审计的持续系统：

1. 角色为什么这样选。
2. 角色此刻知道什么、误信什么、证据强度多少。
3. 角色和别人之间的债务、信任、承诺、筹码如何变化。
4. 情绪不是标签，而是由事件、目标受损/获益和压抑方式推出。
5. 每次状态更新要有来源章节、置信度和过期风险，方便 RAG 召回。

## 联网参考

- Generative Agents 强调观察、记忆流、反思和计划会共同影响下一步行为；这对应本项目的章节事实、角色台账、审核反馈和下一章推演。
  - https://arxiv.org/abs/2304.03442
- Reflexion 强调把任务反馈转成语言反思并写入记忆，下一轮决策再使用；这对应审核不通过后把结论进入 `review_refinement` 和 RAG，而不是简单重写。
  - https://arxiv.org/abs/2303.11366
- ReAct 强调推理与行动交替，行动结果反过来修正计划；这提示本项目要记录角色的选择依据、被拒选项和行动后果，而不只存 `likely_action`。
  - https://arxiv.org/abs/2210.03629
- IPOCL / narrative planning 强调故事行动要同时满足因果链和角色意图；这提示每个关键行动都应该能追溯到角色自己的目标、承诺或误判。
  - https://jair.org/index.php/jair/article/view/10669
- K. M. Weiland 的角色弧线方法区分 Ghost / Wound / Lie / Weakness，提示当前 `misbeliefs` 还需要和更稳定的创伤、价值轴、内在需要绑定。
  - https://www.helpingwritersbecomeauthors.com/whats-the-difference-your-characters-ghost-vs-wound-vs-lie-vs-weakness/
- Reedsy 的角色弧线指南强调人物变化由障碍和选择体现，提示状态变化要记录“选择如何改变信念/关系/能力”。
  - https://reedsy.com/blog/character-arc/
- OCC 情绪模型的计算化改写把情绪触发条件和强度变量拆开，提示 `emotional_state` 应补充触发源、目标影响和应对策略。
  - https://people.idsia.ch/~steunebrink/Publications/ECAI2008_0337.pdf
- Comme il Faut / Prom Week 系列把社会互动建成可组合的社会状态、规范和互动模型，提示关系台账要比自由文本更结构化。
  - https://cdn.aaai.org/ojs/12454/12454-52-15982-1-2-20201228.pdf

## 当前项目已有字段

`CharacterDynamicsProfile` 已有：

- `current_goal`
- `primary_pressure`
- `resources`
- `relationship_forces`
- `secrets`
- `misbeliefs`
- `action_bias`
- `risk_pressure`
- `emotional_state`
- `physical_state`
- `exposure_level`
- `next_likely_action`
- `conflict_vector`

`CharacterSimulationState` 已有：

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

`CharacterVoiceLogic` 已有：

- 性格来源
- 说话原则
- 场景目标
- 潜台词
- 知识边界
- 关系姿态
- 用词节奏
- 动作拍策略
- 对白功能
- 允许动作 / 禁用偏移 / 自检问题

RAG 侧已经有：

- `meta/character_continuity.md`
- `relationship_state.md`
- `meta/state_changes.json`
- `meta/resource_ledger.md`
- `meta/chapter_progress.md`
- `meta/project_progress.md`
- `reviews` 与 `review_lessons`
- `retrieval_trace`

所以当前系统的方向是对的，缺的是字段精度和审核闭环。

## P0：建议立刻补的动态人物信息

### 1. knowledge_ledger：人物知识账本

问题：现在有 `secrets`、`misbeliefs` 和 `knowledge_boundary`，但缺少结构化的“谁知道什么、证据是什么、置信度多少、什么时候知道的”。

建议字段：

- `known_facts`
- `unknown_facts`
- `suspicions`
- `false_beliefs`
- `evidence_seen`
- `confidence`
- `source_chapter`
- `forbidden_knowledge`

用途：

- 防止人物突然知道未公开信息。
- 让误判不是一次性标签，而是可以被证据改变。
- 让 RAG 召回“这个角色为什么会这么判断”。

### 2. decision_frame：角色决策框架

问题：现在有 `action_tendency` / `likely_action`，但缺少“这个选择为何优于其他选择”的证据链。

建议字段：

- `available_options`
- `rejected_options`
- `decision_rule`
- `tradeoff`
- `cost_paid`
- `risk_accepted`
- `expected_gain`
- `minimum_evidence_required`

用途：

- 避免主角突然降智或突然鲁莽。
- 让推演文章和原有写章逻辑一致：角色选择仍服务章节契约，但选择理由来自角色系统。
- 对《鬼城》的江烬尤其重要：他不是“冷静破解”，而是先核验证据、确认权利边界、再决定是否交易。

### 3. relationship_contract：结构化关系账本

问题：`relationship_forces` 已经能写信任、债务、欺骗、利益绑定，但仍是自由文本，不够适合审查和召回。

建议字段：

- `counterpart`
- `trust`
- `debt`
- `leverage`
- `promise`
- `shared_secret`
- `betrayal_record`
- `dependency`
- `fear_source`
- `alliance_status`
- `betrayal_threshold`
- `help_condition`

用途：

- 判断一个人为什么帮主角、什么时候不帮、什么时候反咬。
- 判断配角回归时是否必须带新信息或偿还旧债。
- 让关系变化能被 `state_changes` 和 `relationship_state` 双向校验。

### 4. emotion_appraisal：情绪评价与应对

问题：`emotional_state` 现在像静态标签，容易写成“他很害怕/很冷静”，但不解释情绪从何而来、如何改变行动。

建议字段：

- `trigger_event`
- `goal_impact`
- `threat_to_value`
- `visible_expression`
- `suppressed_expression`
- `coping_strategy`
- `action_pressure`
- `relationship_effect`

用途：

- 对话更稳定：害怕的人可能嘴硬、转移话题、报账式确认，而不是统一喊口号。
- 审核可以问：情绪是否有触发源，是否真的改变选择、关系或节奏。

### 5. arc_axis：长期人物弧线轴

问题：`misbeliefs` 有误判，但缺少更稳定的“角色会长期变化什么”。

建议字段：

- `want`
- `need`
- `wound_or_ghost`
- `core_lie`
- `value_axis`
- `arc_stage`
- `pressure_test`
- `growth_signal`
- `regression_signal`

用途：

- 避免长篇里人物只随事件移动，没有内在变化。
- 让第一章能确定长期弧线：主角的职业创伤、亲情责任、交易敏感性如何在后续被压力反复测试。

## P1：建议第二步补的动态人物信息

### 6. capability_constraints：能力、限制与冷却

建议字段：

- `competence`
- `limitation`
- `tool_access`
- `injury_effect`
- `resource_lock`
- `rule_permission`
- `cooldown`

用途：避免角色忽然会做以前不会的事，或忘记伤势、权限、资源限制。

### 7. deception_posture：欺骗与暴露姿态

建议字段：

- `lie_to_whom`
- `cover_story`
- `tell`
- `exposure_risk`
- `exposure_trigger`
- `fallback_if_exposed`

用途：让秘密不是列表，而是能进入行动、对白和审核的压力源。

### 8. promise_threads：个人承诺与回收压力

建议字段：

- `promise_made`
- `promise_to`
- `due_chapter`
- `payoff_pressure`
- `callback_condition`
- `if_ignored_cost`

用途：配角回归不靠“该出现了”，而靠未偿还承诺、债务、伏笔和资源压力。

### 9. evidence_freshness：证据新鲜度

建议字段：

- `source_artifact`
- `source_chapter`
- `last_confirmed_chapter`
- `confidence`
- `staleness`
- `contradiction`

用途：让 RAG 召回知道哪些动态人物信息是新事实，哪些只是旧猜测或已被推翻。

## 对《鬼城》的落地口径

江烬第一章和后续章的动态人物信息，至少应该写成这样的系统：

- `arc_axis`：失业后的风控惯性、职业创伤、亲情责任、对交易文本和确认动作的敏感，是长期弧线底座。
- `knowledge_ledger`：他知道账单/合同上的可见条款，不知道冥钞系统真实边界；他只能怀疑，不能提前懂全套规则。
- `decision_frame`：他遇到异常不先“勇敢破解”，而是核验签字、付款、权利、风险隔离和可追责证据。
- `relationship_contract`：江禾不是“软肋标签”，而是责任、债务、行动阈值和风险承受边界。
- `emotion_appraisal`：恐惧表现为报账式确认、延迟签字、转移到条款/凭证/权利边界，而不是纯冷静。
- `capability_constraints`：资源不足、信息不足、权限不足时，主角应先换取核验权/临时权限/交易筹码，而不是直接赢。

## 推荐工程接入顺序

1. 扩展 `CharacterDynamicsProfile` 与 `CharacterSimulationState`：先加 `knowledge_ledger`、`decision_frame`、`relationship_contract`、`emotion_appraisal`、`arc_axis`。
2. 扩展 `plan_chapter` schema：让写章前必须给关键角色填这些结构化信息。
3. 扩展 `commit_chapter` / 写后回填口径：正文如果改变知识、关系契约、情绪评价、承诺或弧线阶段，必须写入 `state_changes` 或关系台账。
4. 扩展 `character_continuity` 渲染与压缩：让下一章 `novel_context` 能直接看到这些字段。
5. 扩展 RAG chunk keywords/facets：把 `knowledge_ledger`、`relationship_contract`、`decision_frame` 作为 character facet 的高优先级召回。
6. 扩展 Editor 审核：新增四个问题：
   - 人物是否知道了他没有证据知道的事？
   - 关键行动是否有决策框架支持？
   - 对话是否符合当前情绪评价和关系契约？
   - 章节改变了动态人物状态却没有回填？

## 不建议做的事

- 不建议把动态人物系统做成“多智能体吵架”。那会增加噪声，不能保证正文稳定。
- 不建议只在 prompt 里写“请保持人物一致”。缺少结构化字段时，审核和 RAG 都抓不住。
- 不建议让 RAG 直接塞完整本地文件。应该继续走 `novel_context` 的 selected memory 和 retrieval trace，召回少量相关事实，并让字段本身有来源和置信度。

## 2026-07-04 接入状态

P0 已接入工程主链路：

- `CharacterDynamicsProfile` 与 `CharacterSimulationState` 已增加 `knowledge_ledger`、`decision_frame`、`relationship_contract`、`emotion_appraisal`、`arc_axis`。
- `plan_chapter` schema 已要求写章前关键角色必填这些字段；`relationship_contract` 没有关键关系时传空数组，避免编造关系债务。
- `meta/character_continuity.*` 的生成与 Markdown 渲染已输出知识账本、决策框架、关系契约、情绪评价和长期弧线轴。
- `novel_context` 的 `character_continuity` usage 与项目记忆 RAG keywords 已同步。
- `commit_chapter.state_changes.field`、Writer prompt 和 Editor prompt 已同步新回填/审核口径。
- 新增 `causal_simulation.crowd_roles` 用于捧场/凑数/群体角色，避免无意义队员污染完整人物动力学和 RAG。
- 覆盖测试：`go test ./...` 通过。
