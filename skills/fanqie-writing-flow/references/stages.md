# 可恢复写作阶段

每个阶段必须有明确输入、产物和完成条件。完成一个阶段后立刻更新 `flow_state.json`。恢复时从第一个 `pending` 或 `in_progress` 阶段继续。

多本小说时，先为每本书创建独立 `flow_state.json`，再创建批次状态 `batch_state.json`。批次状态只负责 5 个并行写作 agent 的队列和槽位；每本书的真实进度仍以自己的 `flow_state.json` 为准。注意：Codex 不会因为批次状态里有 `running` 自动开 subagent，主会话必须显式写出 `spawn N agents` / “并行拆给 N 个 agent”。

## Batch stage multi-book-scheduler

触发：用户一次给出 2 本及以上小说题材、书名或核心设定。

产物：`data/generated-output/writing_flows/_batches/<批次名>/batch_state.json`。

执行规则：

- `max_parallel_agents` 固定不超过 5；默认 5。
- `books[].agent_status` 只使用 `pending`、`running`、`done`、`blocked`。
- 首批最多 5 本标记为 `running` 并分配 `agent_slot: 1-5`；第 6 本及以后保持 `pending`。
- 每个 `running` 条目都必须显式启动或续跑一个单书写作 agent。调度文本必须写明 `spawn N agents` 或“并行拆给 N 个 agent”，并列出每个 agent 对应的书名、`agent_slot` 和 `flow_state.json`。该 agent 只读取自己的 `flow_state.json`、正文目录和必要 skill。
- 任一 agent 完成、阻塞或用户暂停后，主会话运行 `batch-sync`。同步会读取单书状态，把终态书标记为 `done` / `blocked`，并把下一本 `pending` 书补位到空出的 `agent_slot`。
- 批次全部 `done` 后，主会话再汇总每本三件套、配图包路径和审核结论；不要提前把阶段性草稿当最终交付。

恢复命令：

```bash
python3 skills/fanqie-writing-flow/scripts/flow_state.py batch-sync --state "data/generated-output/writing_flows/_batches/<批次名>/batch_state.json"
```

完成条件：批次内所有小说的单书 `final-gate` 均完成，或剩余条目全部明确 `blocked` 并给出阻塞原因。

## Stage 0 intake

输入：用户给的书名、题材、人设、核心设定、钩子。

产物：单书 `flow_state.json` 初始化，记录原始输入和假设；多本小说时还要把该状态文件加入批次状态。

完成条件：书名、方向、输出目录、选用技能已确定。

## Stage 1 route

输入：`references/routing.md` 与用户设定。

产物：`selected_skills` 写入状态文件。

完成条件：已读通用模板、题材技能、review 技能路径。

## Stage 2 workspace

产物：

- 正文目录
- `输入设定.md`
- `故事圣经_<书名>.md` 初版空壳
- `premise.md`、`characters.json/md`、`world_rules.json/md`、`book_world.json/md`、`outline.json/md`、`layered_outline.json/md`、`timeline.json/md`、`relationship_state.json/md`、`foreshadow_ledger.json/md`、`compass.json/md` 初版空壳
- 单章正文文件位于 `{书名}/第NN章_章名.md`
- `扩写/` 仅作历史兼容或草稿过程目录，新任务不得把它作为唯一正文产物

完成条件：路径写入状态文件。

## Stage 3 design-package

产物：完整统一设计包 v1。`故事圣经_<书名>.md` 是可读总览和索引，不能替代结构化文件。

必须包含：

- `premise.md`：一句话故事核心、作品标签、主角人设、目标读者、核心情绪、终局承诺、AI 味 5% 目标。
- `characters.json/md`：人物卡与人物弧线；短篇只保留核心/重要人物。
- `world_rules.json/md` 与 `book_world.json/md`：背景规则、地点、路线、势力和可复用场景资产。
- `outline.json/md`：完整章节蓝图，每章有核心事件、主场景、关系/爽点推进、章末钩子、目标字符数。
- `layered_outline.json/md`：短篇也必须存在，使用 1 卷 1 弧压缩结构。
- `timeline.json/md`：开篇触发、中点转折、终局回收，最好逐章覆盖。
- `relationship_state.json/md`：人物关系初始态与关键变化章。
- `foreshadow_ledger.json/md`：伏笔埋设、推进、回收章；短篇必须篇内闭合。
- `compass.json/md`：最终情绪落点或反转承诺。

完成条件：统一设计包能支撑逐章写作，JSON 可解析且非空，`outline.json` 章数等于计划章数。

## Stage 4 title-package

产物：书名、作品标签、主角人设、25 字以内金句、100-150 字简介、2-4 个话题标签。

完成条件：按 `fanqie-novel-template/references/work-tag-catalog.md` 从类型、角色、情节、情绪中选好作品标签，写入故事圣经和正文文件头；双男主或双女主作品必须在「角色」标签中补充「双男主」或「双女主」。

## Stage 5 outline-check

产物：对 `outline.json/md`、`layered_outline.json/md`、`timeline.json/md`、`relationship_state.json/md`、`foreshadow_ledger.json/md` 的一致性复核。

每章必须有：功能、主场景、关系/爽点推进、章末钩子、目标字符数。伏笔、关系变化和时间线事件必须能互相对上。

完成条件：总目标字符数落在 15000-20000，且设计包通过后才能进入逐章正文。

## Stage 6 draft-chapters

产物：`{书名}/第NN章_章名.md`、`{书名}/reviews/NN_ai_gate.json`、`{书名}/reviews/NN.md`、单章审核结果和故事圣经逐章回写。

执行规则：

- 一次只写一章。
- 每章写完立刻 `wc -m`，常规目标 1700-2100 字符。
- 低于下限立即增肥，高于上限立即精简。
- 字数达标后立刻做单章审核：题材硬规则、承上启下、AI 味信号、内容硬检、错别字、一致性。先生成并读取本章 `reviews/` 机械审核，再写人工审核报告；任一未过都只修当前章，复审通过后才进入下一章。
- 写完更新状态文件的 `chapters`。

完成条件：所有规划章节状态为 `done`，且每章 `reviews/` 机械门禁与人工审核报告均通过。

## Stage 7 merge

产物：可选的 `正文_<书名>.md` 单文件，由已审核通过的单章文件合并生成。

完成条件：正文文件包含书名、作品标签、主角人设、金句、简介、话题标签、全部章节；章节标题不使用 `##`；合并前确认所有分章 `reviews/` 已通过。

## Stage 8 de-ai-pass

产物：降 AI 味修订后的正文。

处理重点：模板句、整齐排比、抽象心理、解释性旁白、可替换细节、段落复述。

完成条件：自查六维合计预估 ≤ 1。

## Stage 9 typo-first

产物：第一轮错别字修订记录。

命令：

```bash
python3 quality/audit/scripts/typo_scan.py "<正文路径>"
```

完成条件：已人工确认并回写正文。

## Stage 10 audit

产物：`审核报告_<书名>.md`。

命令：

```bash
python3 quality/audit/scripts/aigc_value.py "<正文路径>" --target 5
python3 quality/audit/scripts/text_signals.py "<正文路径>"
python3 quality/audit/scripts/paragraph_dup.py "<正文路径>"
python3 quality/audit/scripts/content_lint.py "<正文路径>"
```

完成条件：报告写明 AI 创作度、六维分、段落级重复、内容硬检、反面信号，并汇总所有分章 `reviews/` 机械门禁结论。

## Stage 11 revise-until-5

产物：通过 AI 味 5% 闸门的正文和更新后的审核报告。

通过条件：

- AI 创作度 ≤ 5%
- 六维合计 ≤ 1
- 任一单项 ≤ 1
- 完全重复段落 / 高度相似段落 / 重复长句均为 0
- 内容硬检无 error，warning 已逐条确认并回写或说明不影响正文

未通过时：按报告定位章节，回正文重写，再回到 Stage 10。

## Stage 12 typo-second

产物：二次错别字复核记录。

完成条件：审核后改写没有引入新错字。

## Stage 13 image-package

产物：`图片生成方案_<书名>.md` 和可选 `images/` 图片文件。

执行规则：

- 输入必须使用终版 `正文_<书名>.md`、最新版故事圣经和审核报告，不根据早期大纲臆造画面。
- 至少生成 1 张封面图方案 + 3 张关键场景图方案，分别覆盖开篇钩子、中段爆点、结尾情绪落点。
- 每张图都要包含章节来源、画面主体、人物造型、情绪、构图、光色、正向提示词、负向提示词、建议文件名。
- 角色外观、服装、标志物必须在所有图片提示词中保持一致。
- 如果当前执行环境能调用图片生成模型，实际生成图片并保存到 `images/`；否则在方案里标记“待生成”，但提示词必须完整可直接复制使用。

完成条件：`图片生成方案_<书名>.md` 包含封面图、关键场景图、角色一致性说明、正向/负向提示词和图片路径 / 待生成标记。

## Stage 14 final-gate

产物：终版三件套 + 配图包。

最终检查：

- `正文_<书名>.md`
- `故事圣经_<书名>.md`
- `审核报告_<书名>.md`
- `图片生成方案_<书名>.md`
- `{书名}/第NN章_章名.md` 单章文件完整存在，且每章状态为 `done`
- 字符数 `[15000, 20000)`
- AI 味 5% 闸门已过
- 配图包已基于终版正文生成
- 状态文件标记 `done`

只有 Stage 14 完成后，才能给用户最终交付总结。
