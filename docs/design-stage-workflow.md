# 设计阶段统一交付物

本文档定义长篇和短篇在写作前必须产出的同名设计交付物。结论：长篇和短篇不应使用两套文件；区别只在每个文件的层级、密度和时间跨度。

## 外部检索结论

- Reedsy 对三幕结构的说明强调故事由 setup / confrontation / resolution 和因果节拍构成，适用于长篇和短篇的共同骨架：https://reedsy.com/blog/guide/story-structure/three-act-structure/
- LitReactor 的短篇写作指南指出短篇仍有角色、冲突和主题，但需要快速高效传达，通常聚焦 2-3 个关键角色、少量事件和单一强主题：https://litreactor.com/columns/how-to-write-a-short-story
- Jami Gold 的 beat sheet 说明指出节拍表用于跟踪转折、缺失节拍和节奏；故事越长，需要越多转折、支线、尝试失败和人物发展：https://jamigold.com/2018/07/how-can-we-use-beat-sheets-with-short-stories/
- Helping Writers Become Authors 关于多线剧情的文章强调，多线不是堆材料，而是要在大纲阶段让多条故事线服务同一高潮和主题共振：https://www.helpingwritersbecomeauthors.com/writing-multiple-plotlines-everything-you-need-to-know/
- E.M. Welsh 的短篇大纲方法强调短篇可从少量核心 plot points 拆到必要 scene，先列出从起点到终点必须发生的场景，再删掉不必要内容：https://www.emwelsh.com/blog/how-to-write-a-short-story-outline
- Buried in Silicon Books 的 plot arc 说明把所有故事共同元素概括为开端、中心冲突、关键高潮事件和结尾；小说长度增加后会加入主事件、次事件、支线、上升/下降张力场景：https://bisbooks.org/2018/10/15/plot-structure/

## 统一文件清单

所有新项目在进入章节写作前必须具备：

| 文件 | 长篇用途 | 短篇用途 |
|---|---|---|
| `premise.md` | 全书前提、终局承诺、目标读者、禁区 | 同样字段，压缩为单一核心效果和终局反转 |
| `characters.json` / `characters.md` | core / important / secondary / decorative 分层角色档案 | 只保留核心和必要功能位，通常 2-3 个关键人物 |
| `world_rules.json` / `world_rules.md` | 力量体系、社会结构、地理/机构硬规则 | 只写会实际影响剧情的背景/规则 |
| `book_world.json` / `book_world.md` | 地点、路线、势力、可复用场景资产 | 只写上屏地点、关键组织和反复使用的物件场 |
| `outline.json` / `outline.md` | 已展开章节的扁平大纲 | 全部章节一次性列完，8-12 章常见 |
| `layered_outline.json` / `layered_outline.md` | 多卷、多弧、滚动展开 | 1 卷 1 弧压缩结构，保持同名文件 |
| `timeline.json` / `timeline.md` | 过去/现在/未来计划事件，支持长线连续性 | 开篇触发、中点转折、终局回收，最好逐章覆盖 |
| `relationship_state.json` / `relationship_state.md` | 人物关系随章节变化的长期状态 | 起点关系、关键转折、终局关系 |
| `foreshadow_ledger.json` / `foreshadow_ledger.md` | 长线伏笔、推进、回收状态 | 短链伏笔，必须在篇内闭合 |
| `compass.json` / `compass.md` | 终局方向、开放长线、估算规模 | 最终情绪落点/反转承诺 |
| `故事圣经.md` | 可读总览和索引 | 可读总览和索引，不能替代结构化文件 |

## 设计差异

短篇设计不是少交付，而是少分叉：

- 主题：短篇单一主题/单一强效果；长篇可有主主题和多个副主题。
- 人物：短篇人物卡强调功能密度，一句话承担多页人物塑造；长篇需要长期弧线、反复选择和阶段变化。
- 大纲：短篇是压缩三幕或少量 plot points 到必要场景；长篇要有卷弧、支线、阶段失败、节奏波峰波谷。
- 时间线：短篇只保留读者必须理解的前史和篇内事件；长篇要记录长期因果、状态变化、伏笔推进和跨卷回收。
- 伏笔：短篇伏笔短链闭合；长篇伏笔可以跨弧/跨卷，但必须登记推进状态。
- 指南针：短篇指南针指向终局反转或情绪落点；长篇指南针指向开放长线和最终收束方向。

## 设计门禁

进入逐章正文前，必须同时满足：

1. 统一文件清单全部存在。
2. JSON 文件可解析，且不是空数组/空对象。
3. `outline.json` 章数与计划章数一致，每章有 `core_event`、`hook`、`scenes`。
4. `timeline.json` 至少覆盖开篇触发、中点转折和终局回收；短篇建议逐章覆盖。
5. `relationship_state`、`foreshadow_ledger` 中的章节号能在 `outline` 和 `timeline` 里找到对应事件。
6. `故事圣经.md` 只做总览索引，并明确“短篇采用 1 卷 1 弧压缩结构”或“长篇采用多卷/滚动弧结构”。
