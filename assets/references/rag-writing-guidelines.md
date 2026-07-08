# RAG 写作使用规则

RAG 召回只服务于当前小说项目的“找证据、找约束、找已沉淀事实”，不是参考库、拆文库或正文素材库。写作和审稿时按以下顺序使用：

1. 先看 `reference_pack.retrieval_trace.strategy/query_terms/matches`，判断召回为什么命中。
2. 命中理由只有短词重叠、来源不明、facet 与本章职责不一致时，视为弱召回，宁可不用。
3. `selected_memory.rag_recall.summary` 只能作为本书已存在的人物、场景、规则、代价和物件的提醒，不得照抄来源表达。
4. RAG 不得引用 `拆文库/`、`对标/`、`data/reference-library/` 或任何外部参考正文；这些材料如果需要使用，也只能先由项目规划阶段转写成本书自己的设定、任务单或账本。
5. 多条召回冲突时，以 `working_memory.chapter_contract`、`episodic_memory`、`book_world_context`、`resource_audit` 和用户规则为准。
6. 如果召回帮助解决本章问题，正文必须落到可见动作、物件、对白、规则代价或选择后果，不能写成资料说明。
7. 审稿发现 RAG 来源污染正文时，按 aesthetic 或 continuity 出 issue：指出越权来源、被污染段落和应改成本书事实的方式。

## 长循环处理

同一章连续两轮以上围绕相似问题循环时，停止凭感觉改写：

1. 先判断循环类型：人物刻画、情感叙事、对白摩擦、信息密度、AI 检测误判、外部平台口径或现实细节不足。
2. 对人物/情感/对白/节奏问题，必须用 `craft_recall(dialogue|methodology|scene_situation)` 取手法；主题词包含人物刻画、情感叙事、情绪弧线、动机反应、潜台词、信息差中的至少两个。对白工整或信息倾倒优先走 `dialogue`，人物和情绪不落地优先走 `methodology`。
3. 如果 craft 召回弱、无料，或问题涉及检测平台/平台规则/最新语境，必须 `web_research` 查资料，转写成 `meta/writing-techniques/`、`meta/web_reference_brief.*` 或 review/RAG 规则后再改。
4. 每轮只改 1-3 个有证据的局部，保护已经通过的剧情资产、人物声口和物件回扣；不要整章磨平。
