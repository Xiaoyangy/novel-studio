# 说话逻辑与审核失败重推演设计

## 结论

`causal_simulation` 需要从“人物会怎么说”升级为“人物为什么在此刻这样说”。声口不是性格标签，而是场景目的、潜台词、知识边界、关系姿态、语域节奏和动作反应的合成结果。

审核失败后的返工也不能是随机润色。它应当是 evaluator-optimizer 式闭环：审核给出反馈，Writer 把反馈转成新的推演约束，局部改写，再按验收条件检查；若同一失败原因重复出现，就停止整章重写，收窄为局部 edit 或上游重规划。

## 网络参考

- Reedsy 的对白写作指南强调对白要服务故事功能，而不只是听起来自然；本项目把它落为 `dialogue_functions`。
  - https://blog.reedsy.com/how-to-write-dialogue/
- Reedsy Live 的对白课程强调角色声口、场景目的和潜台词；本项目把它落为 `scene_objective`、`hidden_subtext`、`diction_and_rhythm`。
  - https://reedsy.com/live/writing-dialogue-adina-edelman/
- Self-Refine 论文提出“生成 -> 反馈 -> 修正”的迭代流程，且不需要额外训练；本项目把它落为 `review_refinement.trigger_sources`、`failure_modes`、`replanning_moves`、`acceptance_checks`。
  - https://arxiv.org/abs/2303.17651
- Reflexion 论文提出用语言反馈/反思记忆帮助下一轮尝试；本项目把审核结论通过 `rewrite_brief` 和 `review_refinement` 回注给下一轮推演。
  - https://arxiv.org/abs/2303.11366
- Anthropic 的 evaluator-optimizer agent pattern 把评估器反馈交给优化器反复改进，并要求有明确停止边界；本项目把它落为 `iteration_limit` 和 `stop_condition`。
  - https://www.anthropic.com/engineering/building-effective-agents

## 声口字段

`CharacterVoiceLogic` 现在包含：

- `personality_source`：来自人物卡、关系状态、前文声口或本章压力的证据。
- `speech_principle`：此角色在本章说话的底层逻辑。
- `scene_objective`：此角色在关键对话里想拿到什么。
- `hidden_subtext`：没有明说但正在隐瞒、试探、转嫁、索取或保护什么。
- `knowledge_boundary`：此刻知道/不知道什么，避免台词越界剧透。
- `relationship_stance`：面对对方时的权力位置、亲疏和情绪姿态。
- `diction_and_rhythm`：词汇、句长、停顿、职业语域和情绪节奏。
- `action_beat_policy`：对白之间用什么动作、沉默、误判或物件反应承载潜台词。
- `dialogue_functions`：对白承担的故事功能。
- `typical_moves` / `forbidden_moves` / `dialogue_test`：生成和审核共用的可执行自检。

对鬼城第一章，江烬的关键不是“冷静短句”，而是：

1. 先看证据。
2. 再问这笔买的是什么。
3. 再判断谁确认、谁付出什么。
4. 最后才决定是否开口。

因此江烬不能提前解释阴司银行，不能替蒋牧确认，不能对普通人堆“人格资产/标的”等术语，也不能用漂亮金句代替交易边界判断。

## 重推演字段

`ReviewRefinementLoop` 现在包含：

- `trigger_sources`：触发返工的审核来源。
- `failure_modes`：从审核中归纳出的失败类型。
- `localized_targets`：需要改的场景、段落、台词组、物件或章尾。
- `preserve_constraints`：返工时必须保留的已通过资产。
- `replanning_moves`：根据失败类型重新推演的动作。
- `acceptance_checks`：本轮返工完成后的验收问题。
- `stop_condition`：停止继续整章重写的条件。
- `iteration_limit`：同一失败原因允许连续重推演/重写的上限。

## 生产规则

Writer 返工时必须按这个顺序：

`rewrite_brief -> review_refinement -> voice_logic 重检 -> 局部/整章改写 -> check_consistency -> commit_chapter`

Editor 审核时必须检查：

- `context_sources` 是否覆盖原生成链路。
- `voice_logic` 是否真的落到台词目的、潜台词、知识边界和语域节奏。
- `review_refinement.trigger_sources` 中的审核问题是否被改动。
- `preserve_constraints` 是否被破坏。
- `acceptance_checks` 是否能从正文直接证明。

如果同一失败模式重复出现，不应继续放任整章随机重写，应改为局部 `edit_chapter`，或回到上游调整章节契约/大纲。
