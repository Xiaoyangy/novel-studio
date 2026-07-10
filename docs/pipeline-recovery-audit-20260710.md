# Pipeline Recovery Audit - 2026-07-10

目标项目：`data/runs/只许把钱花在青山县/output/novel`

核心不变量：单一世界先推进全部实名角色；每个角色基于自己的目标、压力、资源和知识边界做决定并产生蝴蝶效应；主视角 plan 是世界模拟投影；正文只渲染主角可见信息。

## 问题与处理

| 问题 | 根因 | 处理 | 状态 |
| --- | --- | --- | --- |
| 多次修一个错误就重启，始终没有产出 | 没有先完成失败链审计；每次重启重复支付 RAG 和日志成本 | 冻结重跑，先完成契约、恢复、测试和迁移，再只跑一次 | 已修复流程 |
| 轻都市饭桌章被迫填写宇宙观、仪式日历、世界矩阵等二十多组字段 | 全角色世界模拟、写法参考和 POV 章节计划混成一个大对象 | 新增独立 `chapter_world_simulation`；POV plan 只保留因果、声口、情绪、读者奖励和章末承接 | 已实现 |
| 简化计划时一度误删全角色推演 | 把“减少 POV 表格”错误等同于“减少世界模拟” | 恢复并加强全角色硬覆盖；characters/dossiers 中每个实名角色都必须有自主决定 | 已纠正 |
| 配角只是写“无变化/未死亡”，没有自己的选择理由 | 旧 stage 门禁重状态枚举，轻决定因果 | 每个角色必须有可选项、决定、决定理由、行动、现实耗时、完成度和蝴蝶效应 | 已实现 |
| 正文可能偷看离屏角色信息 | 全角色 stage 与正文 plan 共处，渲染边界不清 | 正式模拟生成 `protagonist_projection`；plan 引用 simulation ID；Drafter/Editor 明确 hidden/delayed 不可渲染 | 已实现 |
| 复杂项目与小事件使用同一时间节奏 | 旧计划没有每个行动的现实耗时与完成度 | 模拟决定新增 `action_duration`、`completion_state`，蝴蝶效应记录 `arrival_chapter` | 已实现 |
| 第一章 partial 中马玉芬被错误标记为饭桌可见角色 | 全角色台账被误当成正文出场名单 | 旧 `offscreen_character_stage` 迁移到独立模拟；正文可见名单单独按本章大纲计算 | 已实现，待项目迁移 |
| staged repair 仍反复调用 context/read/craft | Prompt 把对白和情绪检索设为形式任务，工具层没有硬挡 | staged context 改为紧凑状态；read/craft 在 staged 阶段硬拒绝；craft 同章上限改为 3 | 已实现 |
| staged repair 指令排队后被立即清空 | `PendingSteer` 生命周期错误 | 仅在 partial 全部收口或不存在时清除 repair steer | 已修复并有回归测试 |
| Writer 被旧终稿、旧正式 plan 和未来大纲带到第 2 章 | 返工上下文同时暴露多套章节真相 | replanning 隐藏旧终稿/旧 plan/未来窗口；目标标题、核心事件、钩子由当前 outline 锚定 | 已修复并有回归测试 |
| 第一章标题和计划标题漂移 | 计划标题未与 outline 做强一致性校验 | `ChapterPlanIdentityIssues` 强制标题原样匹配 | 已修复 |
| 跨项目人物名和场景进入当前书 | 内置 prompt 曾含项目专属示例 | Writer/Drafter/Editor 项目中立化，并有 assets 回归测试 | 已修复 |
| `novel_context` 近 1 MB，模型窗口被一次调用推满 | 顶层镜像、重复 causal 数据和大参考包重复注入 | 去镜像、去重复、预算裁剪；staged repair 走专用紧凑信封 | 已修复 |
| 本地 GGUF embedding 崩溃/EOF | 上下文、batch、连接复用和并行参数超过稳定范围 | context 8192、batch 512、ubatch 128、并行 1；本地请求禁用陈旧 keep-alive，启动串行并保留日志 | 已修复并有回归测试 |
| HTTP 200 但 embedding/Qdrant JSON 截断 | 只重试连接错误，解析阶段 `unexpected EOF` 直接失败 | 网络 EOF、截断 JSON、429/5xx 统一有界退避；重试前关闭空闲连接 | 已修复并有故障注入测试 |
| RAG 增量回填留下半套索引 | 先保存 index、再删除远端旧点、最后才 embedding | 全部 embedding 先在内存完成；随后幂等替换远端；最后保存 vector_store，并以 index_state 作为提交标记 | 已修复并有事务测试 |
| 一次永久 EOF 后章节事实永久漏入库 | 回填失败只写日志，没有下次补偿状态 | 失败写 `meta/rag/pending_upserts.json`；下一次回填和 pipeline 启动都会合并重放，成功后清除 | 已修复并有恢复测试 |
| Qdrant EOF 导致本章完全没有召回 | 配置了语义后端后，错误分支直接返回空 | Qdrant 错误/空结果依次降级到本地向量和缓存 BM25；embedding 错误直接走 BM25 | 已修复并有降级测试 |
| 每次 pipeline 重启都重嵌入 695 个事实块 | `ensurePipelineRAGReady` 无复用判定 | 核对事实 chunk hash、模型、维度、向量数值、本地向量点和 Qdrant 点数；Qdrant 丢库时从本地向量重放，不再 embedding | 已实现并有回归测试 |
| 每次上下文/手法检索都解析 3.9 万 chunks 并重建 BM25 | RAG state 与 BM25 无只读缓存 | 按原子文件签名缓存只读 state；常规 BM25 与 craft 字段 corpus 按快照惰性复用 | 已修复并有缓存测试 |
| chunk 摘要或 metadata 变化却复用旧向量 | 旧 hash 只覆盖正文等部分字段 | RAG schema v2 的 hash 覆盖 context、summary、keywords、text、metadata；兼容向量原地重映射，语义变化才重嵌入 | 已实现并有迁移测试 |
| 恢复时打印全部历史 ERROR，误以为错误仍在发生 | `ReplayQueue(0)` 每次从序号 0 回放 | pipeline write 恢复设置 `SkipQueueReplay`，只消费新事件 | 已实现并通过测试 |
| plan 和 commit 的全角色语义重复且不一致 | 前者预写、后者回填没有同一模拟 ID | plan 强引用 world simulation；commit 继续全角色回填，并新增决定理由与蝴蝶效应 | 已实现 |

## 新生成链

1. Architect foundation
2. zero-init + opening world tick
3. `simulate_chapter_world` 分批推进全部实名角色并 finalize
4. `plan_structure` 固定当前章骨架
5. `plan_details` 写主角投影与渲染控制并引用 simulation ID
6. Drafter 只渲染主视角可见内容
7. consistency + commit 全角色实际结果回填
8. Editor 同时审核世界模拟完整性、POV 边界和正文质量

## 验证状态

- 定向 Go 包：`cmd/novel-studio`、`internal/tools`、`internal/store`、`internal/domain`、`internal/agents`、`internal/entry/headless`、`assets` 已通过。
- 全库测试：`go test ./... -count=1` 已通过。
- 目标项目迁移与第 1-2 章正式 pipeline：待全库测试通过后执行一次。
