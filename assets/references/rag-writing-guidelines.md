# RAG 写作使用规则

RAG 召回只服务于当前小说项目的“找证据、找约束、找已沉淀事实”，不是参考库、拆文库或正文素材库。写作和审稿时按以下顺序使用：

1. 先看 `reference_pack.retrieval_trace.strategy/query_terms/matches`，判断召回为什么命中。
2. 命中理由只有短词重叠、来源不明、facet 与本章职责不一致时，视为弱召回，宁可不用。
3. `selected_memory.rag_recall.summary` 只能作为本书已存在的人物、场景、规则、代价和物件的提醒，不得照抄来源表达。
4. RAG 不得引用 `拆文库/`、`对标/`、`data/reference-library/` 或任何外部参考正文；这些材料如果需要使用，也只能先由项目规划阶段转写成本书自己的设定、任务单或账本。
5. 多条召回冲突时，以 `working_memory.chapter_contract`、`episodic_memory`、`book_world_context`、`resource_audit` 和用户规则为准。
6. 如果召回帮助解决本章问题，正文必须落到可见动作、物件、对白、规则代价或选择后果，不能写成资料说明。
7. 审稿发现 RAG 来源污染正文时，按 aesthetic 或 continuity 出 issue：指出越权来源、被污染段落和应改成本书事实的方式。
