# RAG 全生命周期与存量数据复审（2026-09-05）

## 结论

RAG 的创建、增量沉淀、混合检索、Qdrant 恢复和历史快照已经按同一套内容寻址口径收拢。正式项目以 `meta/rag/index_state.json` 为检索权威，以 `vector_store.json` 为可恢复向量源；Qdrant 只承担可重建的在线缓存。共享写法资料与本书事实严格分层。

本次对 `data/runs` 做了全量扫描：共 123 个 RAG 目录，其中 6 个正式目录、117 个 project-all / render candidate / outline-all / rebase / archive / staging 快照。逻辑 RAG 数据约 6.93 GiB，累计 1,921,764 个 chunk、54,051 个向量点。修复后 6 个正式目录均为 schema v4、pending 为 0，事实 chunk 与本地向量一一对应且无阻断问题；全部 123 个目录仍可读取。

正式数据处理中，3 份旧 schema 索引迁移到 v4；两个已标记废弃的正式目录共移除 15 个命中本书禁入规则的事实 chunk 及对应向量。共享设计语料不按单书宽泛禁词做存储期删除，而是在 `craft_recall` 时按项目过滤，避免诸如题材词或角色职业词误删整批可复用方法卡。

历史快照不改写，只对字节完全一致、时间戳/权限/文件系统均一致的 `index_state.json` 与 `vector_store.json` 使用硬链接去重。当前 240 个大文件引用归并为 79 个唯一 inode，约 4.17 GB 重复逻辑数据不再重复占盘；修复前版本保留在每本书的 `meta/rag-maintenance-backups/`，可按 manifest 校验回滚。

## 数据分层与权威边界

| 层 | 内容 | 检索方式 | 权威性 |
|---|---|---|---|
| 本书事实 | 世界规则、角色状态、关系、资源、伏笔、章节摘要 | Qdrant / 本地向量与 BM25 混合 | `index_state.json` 中当前 ID + hash 才有效 |
| 共享设计 | 写作手法、对标拆解、审核校准 | `craft_recall` 的确定性 BM25 路由 | 只能迁移方法，不能成为 canon |
| 本地向量 | 本书事实的 embedding 与 payload | Qdrant 失效时本地检索，或用于重放 | `vector_store.json` 是恢复源 |
| 在线向量 | 当前项目 collection | 低延迟语义召回 | Qdrant 是缓存，不可反向覆盖本地权威 |
| 检索审计 | query、strategy、命中、理由、receipt | JSONL / JSON | 重建时归档，不删除历史 |

## 创建与更新路径

1. `--build-rag` 收集当前项目白名单文件，并追加配置中明确允许的三类共享设计库。
2. 路径被规范成稳定的 `project/...` 或 `shared/...` 标识，避免工作目录或机器绝对路径改变 chunk ID。
3. 文档按章节标题和字符上限切块，补全 context、summary、keywords、facet，规范化后以内容 hash 去重。
4. 已接受章节从 `summaries/` 回填为 `chapter_summary_facts`；未完成的增量写入通过 `pending_upserts.json` 恢复。
5. 共享设计 chunk 留在词法层；只有本书事实生成 embedding。
6. 全量构建先在内存完成索引、回填、pending 合并与 embedding，再先发布 `vector_store.json`，最后以 `index_state.json` 作为提交标记。失败不会提前暴露半成品索引。
7. `save_foundation`、`commit_chapter`、`save_review` 等增量入口按 `source_path` 替换旧版本，向量成功后再提交本地状态。

## 检索与防串库

`novel_context` 先构造章节目标、契约、出场角色和伏笔查询。正常顺序是 Qdrant + BM25、随后本地向量 + BM25、最后纯 BM25。无论远端还是本地向量命中，都必须同时满足：

- 非设计库、非禁入 chunk；
- `chunk_id` 存在于当前本地索引；
- 命中的内容 hash 与当前本地索引完全一致。

这使错误 collection、同点数串库、过期 payload 或 Qdrant 在启动后被外部改写时都无法把外书事实注入当前项目。写作前 `--rag-ready` 还会通过 scroll 分页核验 collection 全集；只要存在缺失、额外点或 hash 不同，就以本地向量完整重放。

## 全量数据维护

```bash
# 不修改任何数据，扫描正式与历史快照
go run ./cmd/novel-studio rag audit --root data/runs

# 修正式目录并对完全相同的大快照做物理去重
go run ./cmd/novel-studio rag maintain --root data/runs --apply

# 单本书恢复/验证 Qdrant，不启动写作
go run ./cmd/novel-studio --rag-ready --dir data/runs/<书名>/output/novel
```

`maintain` 会先为即将改变的正式数据创建带 SHA-256 manifest 的备份，然后处理 schema、chunk hash 清单、内容去重、禁入事实、孤儿/缺失/重复向量、向量维度与数值、payload 和粘连 JSON 对象流。它不会修订 archive、candidate 或废弃 generation；这些目录的问题只进入报告。

报告写入 `data/runs/rag-maintenance-report.json`。每个正式项目另写 `meta/rag/health.json`，Dashboard 只读取这份轻量摘要，不加载几十 MB 的索引。

本地 Qdrant 子进程现在以配置的 `storage_dir` 作为工作目录，运行标记、日志和持久数据不会再落进仓库根目录。

## 本次正式项目结果

| 项目 | chunks | 事实向量 | 状态 |
|---|---:|---:|---|
| 她的第二算法_20260709_reboot | 38,721 | 458 | 健康；Qdrant 内容核验通过 |
| 只许把钱花在青山县 | 16,316 | 1,322 | 健康；Qdrant 内容核验通过 |
| 我在直播间给凶手点外卖 | 15,467 | 473 | 健康；Qdrant 内容核验通过 |
| 旧纹有姓名 | 15,256 | 308 | 健康；Qdrant 内容核验通过 |
| 废弃-旧 | 38,807 | 431 | 健康；Qdrant 内容核验通过 |
| 废弃-鬼城 | 38,901 | 539 | 健康；Qdrant 内容核验通过 |

Qdrant 中另有一个没有任何本地快照引用的旧 collection（12 points），复核 payload 后已作为可重建孤儿缓存移除；当前服务只保留上述 6 个项目 collection。

历史/投影快照仍报告旧 schema、绝对路径、陈旧 hash、缺失或孤儿向量等遗留统计。这是刻意保留的不可变历史，不影响正式项目健康状态，也不会被在线召回读取。

## 依据与后续边界

Qdrant 的 scroll 接口用于分页遍历整个 collection，collection alias 可作为未来零停机切换手段，snapshot 可用于服务级灾备。当前实现采用“本地内容寻址状态 + collection 全量核验 + 可重放缓存”，避免把 Qdrant 本身误当成唯一真相：

- <https://qdrant.tech/documentation/manage-data/points/>
- <https://api.qdrant.tech/api-reference/points/scroll-points>
- <https://qdrant.tech/documentation/manage-data/collections/>
- <https://qdrant.tech/documentation/operations/snapshots/>
