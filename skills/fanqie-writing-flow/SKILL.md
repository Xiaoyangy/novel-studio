---
name: fanqie-writing-flow
description: 番茄小说兼容写作流编排器。用于非 novel-studio 原生项目的番茄短篇批量产出；在 novel-studio 内只能作为方法论参考并转接 `novel-studio --pipeline`。触发场景包括：番茄短篇三件套、CP 营业文生成、百合/双男主/爽文自动路由、断点续写、恢复写作 flow、终版 AI 味 5% 以下。
---

# 番茄写作执行流

本技能是执行器，不替代题材方法论。它接收用户给的题材或设定，自动选择合适的写作技能，建立可恢复状态，然后持续推进到终版三件套、配图包和 AI 味 5% 闸门通过。

## 最高优先级：novel-studio 强制 pipeline

当前目录或任一父目录包含 `cmd/novel-studio` 时，本 skill 只能作为 pipeline 适配器：

- 禁止直接生成、续写或改写正文；禁止直接修改 `正文/`、`chapters/`、`output/novel/chapters/` 等章节产物。
- 无论用户如何描述，包括“根据题材自动生成文章”“继续写”“恢复写作 flow”，只要是 novel-studio 原生项目写作，都必须调用 `novel-studio --pipeline`。
- 番茄题材、AI 味、三件套、图片包等要求应写入 `run-prompts/<书名或任务>.md`，再执行 `novel-studio --pipeline --prompt-file <需求文件>`。
- 本 flow 只保留给 `data/generated-output/...` 兼容批量服务；不得用它绕开 pipeline。

## 必读顺序

1. 读 `skills/fanqie-novel-template/SKILL.md`。
2. 读 `references/routing.md`，选择题材技能。
3. 按选中的方向读对应技能：
   - 百合 / GL / 双女主：`skills/fanqie-baihe-short/SKILL.md`
   - 双男主 / CP 营业 / 他他 / 影帝顶流：`skills/fanqie-shuangnanzhu-short/SKILL.md`
   - 反转 / 爽文 / 复仇 / 打脸 / 维权：`skills/fanqie-shuangwen-short/SKILL.md`
4. 读 `references/stages.md`，按阶段执行。
5. 设计阶段按 `docs/design-stage-workflow.md` 的统一交付物执行；章级和完稿审核读 `skills/review/SKILL.md`，并运行 `quality/audit/scripts/` 下的 AIGC、重复、内容逻辑和错别字脚本；每章都必须留下 `reviews/NN_ai_gate.json` 与统一审核报告 `reviews/NN.md`。

## 启动规则

- 从用户输入里提取：书名、核心设定、人物配置、题材方向、开篇钩子、禁忌要求。
- 如果用户一次给出多个不同题材、书名或核心设定，先拆成多本小说任务。Codex 不会因为状态文件里有 `running` 就自动开 subagent；调度器必须明确写出 `spawn N agents` 或“并行拆给 N 个 agent”，把每本小说显式分派给独立写作 agent。**最多同时运行 5 个 agent**；第 6 本及以后进入 pending 队列，等任一 running agent 完成、阻塞或用户暂停后再补位。
- 每个写作 agent 只处理一本书，并只读取该书目录、该书统一设计包、该书状态文件和当前章相关上下文；禁止把其他小说的设定、正文或审核报告塞进同一个 agent。
- 多本小说可以并发推进，但单本小说内部必须逐章串行：第 N 章写完、审核达标、故事圣经索引和相关设计状态回写、状态标记 `done` 后，才能进入第 N+1 章。
- 主会话只维护轻量调度表：`agent_slot / 书名 / 题材 / 当前阶段 / 当前章 / 最后审核结论 / 下一步`。不要把多本正文同时堆进对话；正文和审核证据都落盘，按需读取，避免上下文压缩。
- 能合理判断方向时直接判断，不要反复问用户。示例《和死对头组CP营业后我们假戏真做了》应路由到双男主，因为有“组 CP 营业、顶流、影帝、他”。
- 只在关键事实无法推断且会影响整本书方向时提问，例如 CP 性别完全不明、用户要求的平台不是番茄、目标体量不是短篇。
- 单本启动或恢复先创建/读取状态文件：`data/generated-output/writing_flows/<书名>/flow_state.json`。多本启动或恢复还必须创建/读取批次状态：`data/generated-output/writing_flows/_batches/<批次名>/batch_state.json`。

## 状态文件

使用脚本管理状态：

```bash
python3 skills/fanqie-writing-flow/scripts/flow_state.py init --title "<书名>" --direction "<方向>" --selected-skill "<skill>" --book-dir "<正文目录>"
python3 skills/fanqie-writing-flow/scripts/flow_state.py show --state "data/generated-output/writing_flows/<书名>/flow_state.json"
python3 skills/fanqie-writing-flow/scripts/flow_state.py complete --state "data/generated-output/writing_flows/<书名>/flow_state.json" --stage "<stage-id>"
python3 skills/fanqie-writing-flow/scripts/flow_state.py chapter --state "data/generated-output/writing_flows/<书名>/flow_state.json" --chapter 1 --status done --path "<章节文件>"
```

恢复时先运行 `show`，从第一个 `pending` 或 `in_progress` 阶段继续，不要从头重写已完成产物。

多本小说使用批次状态管理 5 个并行写作 agent：

```bash
python3 skills/fanqie-writing-flow/scripts/flow_state.py batch-init --batch-title "<批次名>" --state "data/generated-output/writing_flows/<书名A>/flow_state.json" --state "data/generated-output/writing_flows/<书名B>/flow_state.json" --max-parallel-agents 5
python3 skills/fanqie-writing-flow/scripts/flow_state.py batch-sync --state "data/generated-output/writing_flows/_batches/<批次名>/batch_state.json"
python3 skills/fanqie-writing-flow/scripts/flow_state.py batch-show --state "data/generated-output/writing_flows/_batches/<批次名>/batch_state.json"
```

`batch-sync` 会刷新各书 `flow_state.json`，保持最多 5 本处于 `running`。总数超过 5 本时，剩余书保持 `pending`；有 running 书变成 `done` 或 `blocked` 后，自动把下一本 pending 书提升为 running 并分配 `agent_slot`。

## 5 agent 并行调度协议

- 主会话是调度器，不直接写正文；它只负责拆书、建单书状态、建批次状态、显式 `spawn N agents` / “并行拆给 N 个 agent”、收集每本最终结论。只更新 `batch_state.json` 或把书标记为 `running` 不会自动创建 agent。
- 计算本轮可并行数量：`N = min(待处理小说数, 5, 当前可用 agent 槽位数)`。如果 `N > 1`，调度指令必须出现类似 `spawn N agents`、`并行拆给 N 个 agent`、`分别启动 N 个单书写作 agent` 的明确表述；如果只写“并发处理”，视为不合格。
- 每个 running agent 的任务边界必须写清：`agent_slot`、书名、题材方向、选用 skill、`flow_state.json` 路径、正文目录、当前阶段、当前章号、恢复规则和终版闸门。
- 同一 `agent_slot` 同一时间只能绑定一本小说；同一本小说同一时间只能有一个写作 agent。
- 当 running agent 返回“完成/阻塞/需用户输入”时，主会话立刻运行 `batch-sync`，再按新的 `running` 列表显式 spawn/续跑补位 agent。
- 如果当前运行环境不能真正 spawn 子代理，必须降级为主会话按批次顺序轮转执行：一次仍只处理一本书的当前章，状态文件和批次状态照常更新，不得把多本正文混在同一上下文。

写作 agent 提示词骨架：

```text
你是 fanqie-writing-flow 的单书写作 agent。
agent_slot: <1-5>
书名: <书名>
题材方向: <方向>
选用技能: <fanqie-novel-template + 题材 skill + review>
状态文件: <flow_state.json>
正文目录: <book_dir>

只处理这一本小说。先读取状态文件和必要 skill，从当前 pending/in_progress 阶段继续。
单本内部逐章串行：写第 N 章、审核、回写故事圣经、标记章节 done 后，才能写第 N+1 章。
不要读取或引用其他小说目录、正文、故事圣经或审核报告。
只有 final-gate 通过后才返回完成总结；中途受限时返回当前阶段、阻塞原因和下一步恢复命令。
```

## 执行纪律

- 不把“计划”当最终交付。计划写完后继续执行下一阶段。
- 每完成一个阶段，立刻更新 `flow_state.json`。
- 进入正文写作前必须补齐统一设计包：`premise`、`characters`、`world_rules`、`book_world`、`outline`、`layered_outline`、`timeline`、`relationship_state`、`foreshadow_ledger`、`compass` 和 `故事圣经`。短篇只压缩粒度，不减少文件。
- 每章正文必须写入 `{书名}/第NN章_章名.md`。旧项目可兼容 `扩写/第NN章.md` 或合并正文，但新任务一律用书名目录下的单章文件作为主产物。
- 每章写完立刻数 `wc -m`。单章统一按 `novel-studio` 当前 `chapter_words` 预算执行，默认 2100-3000 字；明显越界时优先调整章节承载量、合并或拆分场景，不为卡点删掉必要内容。
- 每章写完立刻审核：字数、题材硬规则、承上启下、AI 味信号、内容硬检、错别字和一致性。先生成本章 `reviews/NN_ai_gate.json` 与统一审核报告 `reviews/NN.md`；机械门禁和审核报告都通过后，本章才能标记 `done` 并进入下一章。
- 每章写完必须回写故事圣经索引和相关设计状态。
- 终版必须通过同一 AI 味 5% 闸门：所有章节 `reviews/` 机械门禁通过，AI 创作度 ≤ 5%，六维合计 ≤ 1，任一单项 ≤ 1，段落级重复为 0。
- 如果一次对话受限而无法写完全书，不能声称完成；必须保留状态文件、说明当前阶段和下一步恢复命令。
- 除非用户明确暂停或外部条件阻塞，持续推进到 `final-gate` 通过。
- 使用本技能实际写作时，只有 `final-gate` 通过后才能给最终交付总结；阶段计划、路由结果、单章草稿都不是最终回答。

## 输出目录

按路由结果选择：

- 双男主：`data/generated-output/nvpin_shuangnanzhu_novels/<书名>/`
- 百合：`data/generated-output/baihe_novels/<书名>/`
- 反转爽文：`data/generated-output/nanpin_shuangwen/<书名>/` 或 `data/generated-output/nvpin_shuangwen/<书名>/`
- 未知新题材：先用 `data/generated-output/<题材>_novels/<书名>/`

最终目录至少包含：

```text
故事圣经_<书名>.md
premise.md
characters.json
characters.md
world_rules.json
world_rules.md
book_world.json
book_world.md
outline.json
outline.md
layered_outline.json
layered_outline.md
timeline.json
timeline.md
relationship_state.json
relationship_state.md
foreshadow_ledger.json
foreshadow_ledger.md
compass.json
compass.md
审核报告_<书名>.md
图片生成方案_<书名>.md
第01章_章名.md
第02章_章名.md
...
reviews/
  01_ai_gate.json
  01.md
正文_<书名>.md   # 可选合并稿，所有单章审核通过后生成
images/          # 可选，保存已生成图片
```

历史爽文目录可以兼容 `正文.md`、`故事圣经.md`、`审核报告.md`，但新执行流优先使用书名目录下的单章文件和带书名的故事圣经/审核报告。
