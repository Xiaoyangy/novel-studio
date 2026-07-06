# Harness 与 RAG Pipeline 接线审计

日期：2026-07-04

## 结论

当前 `novel-studio` 的 harness 和 RAG 能力已经接入工程主流程，不是孤立脚本：

- Harness 通过 `novel-studio eval` / `novel-studio eval inspect` 复用 `diag`、`stylestat`、RAG collection collector 和 case contract，能检查真实 pipeline 产物。
- Pipeline 写作阶段会在 `pipelineWrite` 前执行 `ensurePipelineRAGReady`，保证项目 RAG index 存在，并在 embedding 启用时写入 Qdrant 和本地 `vector_store.json` fallback。
- Writer/Editor/Architect 通过 `novel_context` 获取 `selected_memory.rag_recall`、`reference_pack.retrieval_trace`、项目推进台账和角色连续性台账。
- `save_foundation`、`commit_chapter`、`save_review`、`review-existing`、`rewrite-existing` 的事实沉淀统一走 `UpsertRAGChunks`，按 `source_path` 替换旧 chunk，并过滤参考库污染。

本次审计发现并修复了一个边缘风险：手动 `--build-rag` 过去默认只重建项目资料索引，已完成章节的 `chapter_summary_facts` 需要额外传 `--backfill-chapters`。这会让手动重建或恢复性操作丢失章节事实 chunk，和 harness/pipeline 的 RAG 契约不一致。现在 `--build-rag` 默认回填章节事实，显式 `--backfill-chapters=false` 才关闭。

## Harness 使用情况

入口：

- `cmd/novel-studio/main.go`：`eval` 子命令在常规 flag 解析前拦截。
- `internal/eval/eval.go`：负责 single / A-B / repeat 编排、variant prompt 覆盖、报告输出。
- `internal/eval/inspect.go`：对既有 `output/novel` 产物离线检查，不启动模型生成。
- `internal/eval/collect.go`：采集 progress、checkpoints、reviews、usage、tool calls、stylestat、RAG index、vector_store 和 Qdrant count。
- `internal/eval/grade.go`：把 diag finding、case contract、RAG contract、delta gate 映射为 PASS/WARN/FAIL。

现有 harness case：

- `evals/cases/harness/ghostcity_rag_progression.json`
- `evals/cases/harness/second_algorithm_rag_progression.json`

它们覆盖的关键项：

- pipeline 产物：`progress.json`、`checkpoints.jsonl`、章节推进、角色连续性、项目进度、演化报告、资源账本、状态变化、写法资产。
- RAG 产物：`meta/rag/index_state.json`、`meta/rag/vector_store.json`。
- RAG 质量：最小 chunk 数、facet 覆盖、`source_kind=chapter_summary_facts/note`、关键 `source_path`、禁入来源、Qdrant 健康和 point 数。

本次验证：

```bash
./novel-studio eval inspect --cases evals/cases/harness --out workspace/evals/codex-harness-rag-audit-20260704-after-fix
```

结果：2 cases PASS，0 hard fails，0 warnings。

## RAG 使用情况

写前准备：

- `runPipelineWithStages` 启动时调用 `bootstrap.EnsureRAGQdrant`。
- `pipelineWrite` 调用 `ensurePipelineRAGReady`。
- `ensurePipelineRAGReady` 先保证 `index_state`，embedding 启用时再写 Qdrant 和 `vector_store.json`。

运行时注入：

- `agents.BuildCoordinator` 把 Qdrant searcher 接入 `ContextTool.WithRAGVectorSearcher`。
- 同处把 embedder 接入 `ContextTool.WithRAGEmbedder`。
- 同处把 embedder/vector writer 接入 `commit_chapter`、`save_foundation`、`save_review`。
- `ContextTool.selectRAGRecall` 的召回顺序是 Qdrant vector 优先，本地 `vector_store.json` fallback，再走本地关键词/上下文 hybrid。
- 每次有效召回会写 `meta/rag/retrieval_trace.jsonl`。

事实沉淀：

- `commit_chapter` 写 `summaries/NN.json` 对应的 `chapter_summary_facts`。
- `save_review(accept)` 刷新 chapter/project/evolution/character ledgers，并通过项目记忆 sink upsert。
- `pipeline` export settle 会补交付事实 chunk。
- `UpsertRAGChunks` 统一做 source replacement、禁入来源过滤、向量点删除和重写。

本次探针：

```bash
./novel-studio --build-rag --dir data/runs/鬼城/output/novel --probe-chapter 29
```

结果：

- `chunks=1430`
- `chapter_summary_facts=28`
- `note=1402`
- `vector_points=1430`
- `probe chapter 29 rag_recall=6`
- 最新 trace strategy 为 `qdrant_vector_engine_v1`，命中项的 reason 均来自 `qdrant:*`，说明 runtime RAG 召回走 Qdrant 引擎而不是本地关键词扫描；命中来源包括 `outline.md`、`meta/chapter_progress.md`、`meta/character_continuity.md`。

## 修复项

修改：

- `cmd/novel-studio/rag_cmd.go`
  - `parseBuildRAGFlags` 默认 `BackfillChapters=true`。
  - `--backfill-chapters=false` 仍可显式关闭。
- `cmd/novel-studio/rag_cmd_test.go`
  - 新增默认开启回填测试。
  - 新增显式关闭回填测试。
- `docs/data-lifecycle-and-progression.md`
  - 写作前检查增加手动 build-rag 默认回填说明。
- `docs/context-management.md`
  - RAG 策略增加手动 build-rag 默认回填说明。

## 验证命令

```bash
go test ./...
go build -o novel-studio ./cmd/novel-studio
python3 scripts/validate_skill_context.py
./novel-studio --build-rag --dir data/runs/鬼城/output/novel --probe-chapter 29
./novel-studio eval inspect --cases evals/cases/harness --out workspace/evals/codex-harness-rag-audit-20260704-after-fix
```

全部通过。

## 剩余建议

- 将 `eval inspect --cases evals/cases/harness` 加入日常大改后的 proof set，尤其是改 RAG、progression、review、pipeline 时。
- 若后续希望 harness 不只验产物，还验“每次 Writer 上下文里必含 RAG 召回”，可以扩展 `internal/eval/collect.go` 读取 `meta/rag/retrieval_trace.jsonl`，把最近一次 trace 的 strategy/matches/source_path 纳入 case contract。
