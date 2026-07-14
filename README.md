# novel-studio：AI 长篇小说自动写作与动态世界推演引擎

[![Release](https://img.shields.io/github/v/release/Xiaoyangy/novel-studio)](https://github.com/Xiaoyangy/novel-studio/releases/latest)
[![License](https://img.shields.io/github/license/Xiaoyangy/novel-studio)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.25-00ADD8?logo=go&logoColor=white)](go.mod)
[![Platform](https://img.shields.io/badge/platform-macOS%20%7C%20Linux%20%7C%20Windows-lightgrey)](https://github.com/Xiaoyangy/novel-studio/releases/latest)

**novel-studio** 是一个开源、自托管、local-first 的 AI 小说生产系统，面向长篇网文、百万字连载、短篇投稿和整书工程化创作。它把 **动态世界推演、多智能体写作、章节规划与渲染、RAG/Qdrant 长程记忆、质量审核返工、AIGC 门禁、断点恢复和进度看板** 放在同一套可验证 pipeline 中。

它不是“把上一段继续写长”的文本生成器。系统先推进角色和世界，再把主角可见的部分投影成章节计划；正文、评审、返工和交付都必须引用同一组落盘事实与正文指纹。

**Search keywords / 检索关键词**：AI 小说写作、AI 网文生成器、长篇小说自动生成、自动写小说、小说创作 Agent、multi-agent novel writing、LLM writing pipeline、dynamic world simulation、local RAG、Qdrant、self-hosted AI writing、AIGC review、GitHub Release、Release 升级、novel-studio update。

## 最新升级：题材文风合同、整章 AIGC 与同稿门禁（2026-07-14）

本轮升级把通用叙事学与项目题材声口同时投影进 `render_packet v6`：规划层选择文学手法，项目规则确定性选择 `genre style profile`，Drafter 从精简 packet 中读取页面事实、`literary_render_contract` 与 `style_contract`。首个专项 profile 覆盖轻松县城经营、系统、单女主关系边界与完整口述气口；未命中的项目不会被注入青山县题材假设。

同时修复了整篇合并成一个检测片段时的相关信号误判、draft 64KB 上下文超限、硬合同在压缩中丢失、同一失败哈希重复提交，以及 Editor、同哈希 DeepSeek 与本地结构 warning 无法严格协调的问题。完整根因、文件范围、兼容边界、青山县第一章实测和验证矩阵见 [README-20260714.md](README-20260714.md)。

> 当前青山县第一章正文 SHA-256 为 `e3b1ef178aebf1f822a7a15a6a746cec84d28028900b848e9093905c09131399`。同稿 DeepSeek 裸正文判定为 `human_like / low / 3%`，本地 Python 诊断值为 `7.32%`；统一审核已采用同哈希 `3.00%`、Editor `accept`，并清空第一章返工队列。这不是腾讯朱雀复测结果，朱雀仍需把当前正文整篇作为一个片段重新提交。

## 效果图

![novel-studio AI 小说创作进度看板总览：pipeline、章节审核、RAG、模型用量和运行队列](docs/assets/dashboard-overview-20260710.png)

*总览：主线下一章、实际工作章、pipeline 阶段、评审门禁、RAG、模型用量和运行队列统一展示。*

![novel-studio 人物页签：角色档案、OCEAN 画像、目标压力、知识边界和关系契约](docs/assets/dashboard-characters-20260710.png)

*人物：角色档案、OCEAN 画像、目标与压力、知识边界、关系契约和长期弧线一屏可查。*

![novel-studio 离屏世界页签：动态世界推演、势力进度钟、社会情绪和角色独立行动](docs/assets/dashboard-offscreen-20260710.png)

*离屏世界：世界推演游标、角色独立行动、势力进度钟、社会情绪和信息传播持续推进。*

## 核心能力

| 能力 | 当前实现 |
|---|---|
| 一句话开书 | `--pipeline --new-novel` 先做 brainstorm，再生成 foundation、zero-init 资产并进入写作 |
| 动态世界 | 每章先运行全角色世界模拟，记录目标、压力、知识边界、选择、耗时、完成度和蝴蝶效应 |
| 主视角边界 | `protagonist_projection` 只暴露主角可见事实，hidden / delayed 信息不能提前进入正文 |
| 规划与正文隔离 | World Simulator、Planner、Drafter 使用独立 prompt、工具集和上下文预算 |
| 题材文风合同 | 项目 style 与 `user_rules` 确定性选择 genre profile；合同随 render packet 进入正文，未命中时不注入题材假设 |
| 候选选优 | 正常草稿基于同一冻结计划并发生成 3 个候选，确定性初筛后可交给异模型 Reviewer 二选一 |
| 质量闭环 | 机械规则、本地 AIGC、Editor、异模型裸正文判定和统一报告共同决定 accept / warning / blocking |
| 可恢复提交 | 章节提交按 state applied → quality checked → checkpointed → RAG indexed 分阶段恢复，不会用半完成状态冒充交付 |
| 长程记忆 | 项目事实 RAG、写法库、对标库和审核校准库分通道路由，支持 embedding、Qdrant、本地向量与 BM25 |
| 运行保护 | 阶段证据指纹、正文 SHA-256、审核缓存、返工来源绑定、预算保险丝和无进展熔断 |
| 可观测性 | 浏览器看板只读聚合全部书目，交叉核对正文、进度、评审、RAG、检查点与运行事件 |

## 快速开始

### 1. 安装

稳定版本从 [GitHub Releases](https://github.com/Xiaoyangy/novel-studio/releases/latest) 下载。Release 使用 GoReleaser 构建 macOS、Linux、Windows 的 x86_64 / arm64 包并附带 SHA-256 checksums。仓库 `main` 可能包含尚未进入最新 Release 的变更。

~~~bash
# macOS / Linux：安装最新 Release
curl -fsSL https://raw.githubusercontent.com/Xiaoyangy/novel-studio/main/scripts/install.sh | sh

# 安装指定版本
curl -fsSL https://raw.githubusercontent.com/Xiaoyangy/novel-studio/main/scripts/install.sh | sh -s -- v0.2.0

# 安装到用户目录；环境变量必须传给管道右侧的 sh
curl -fsSL https://raw.githubusercontent.com/Xiaoyangy/novel-studio/main/scripts/install.sh |
  NOVEL_STUDIO_INSTALL_DIR="$HOME/.local/bin" sh
~~~

Windows 用户从 [Releases](https://github.com/Xiaoyangy/novel-studio/releases) 下载对应 ZIP，解压后将 `novel-studio.exe` 放入 `PATH`。

~~~bash
# 从源码构建
git clone https://github.com/Xiaoyangy/novel-studio.git
cd novel-studio
go build -o novel-studio ./cmd/novel-studio

# Docker：仓库提供 Dockerfile 和本地 compose 配置
docker compose build novel-studio
docker compose run --rm novel-studio --version
~~~

### 2. 首次配置与连通性检查

首次直接运行会启动配置引导；后续无参数运行只打印用法。

~~~bash
novel-studio
novel-studio --check
~~~

配置默认读取 `~/.novel-studio/config.json`，项目目录中的 `./.novel-studio/config.json` 可覆盖全局配置。完整示例见 [config.example.jsonc](config.example.jsonc)。

### 3. 启动完整 pipeline

~~~bash
# 从一句话想法新建小说：brainstorm + 默认完整阶段
novel-studio --pipeline --new-novel \
  --prompt "一个返乡程序员得到只能投资家乡的系统，从夜市改造开始重建县城"

# 更适合长期维护：把完整创作契约写入文件
novel-studio --pipeline --new-novel --prompt-file prompt.md

# 先写到第 10 章暂停
novel-studio --pipeline --prompt-file prompt.md --write-to 10

# 中断后在同一项目目录重跑同一命令，按证据续跑
novel-studio --pipeline --prompt-file prompt.md
~~~

默认阶段是：

~~~text
architect -> zero-init -> write -> review -> rewrite -> deliver
~~~

`--new-novel` 会在此之前增加 brainstorm；`cocreate` 是可选阶段，不在默认阶段列表中。

### 4. 打开进度看板

~~~bash
novel-studio service open
novel-studio service status
~~~

默认地址是 [http://127.0.0.1:8765/](http://127.0.0.1:8765/)。pipeline 会尝试复用或后台启动看板；看板启动失败不会中断正文任务。

## 当前系统架构

~~~mermaid
flowchart TD
    CLI["CLI / Pipeline Stage Runner<br/>参数路由、阶段恢复、证据核验"] --> BS["Brainstorm<br/>新书输入哈希与 kickoff journal"]
    CLI --> HOST["Host<br/>项目生命周期、预算、事件、恢复"]
    HOST --> FLOW["Flow Router + Dispatcher<br/>从 Store 推导下一任务<br/>无进展熔断"]
    FLOW --> CO["Coordinator<br/>接收事实化指令并调度子代理"]

    CO --> AR["Architect<br/>foundation、卷弧、大纲、世界 tick"]
    CO --> WS["World Simulator<br/>全角色单世界推进"]
    CO --> PL["Planner<br/>POV structure + details"]
    CO --> DR["Drafter / Finalizer<br/>冻结 render packet 写正文"]
    CO --> ED["Editor<br/>叙事与契约评审"]

    CLI --> REV["Review Engine<br/>同一正文冻结快照"]
    REV --> EBR["Editor branch"]
    REV --> RBR["Reviewer branch<br/>异模型裸正文判定"]

    AR --> TOOLS["Tools<br/>唯一事实写入口"]
    WS --> TOOLS
    PL --> TOOLS
    DR --> TOOLS
    ED --> TOOLS
    EBR --> TOOLS
    RBR --> TOOLS

    TOOLS --> STORE["Store + Domain<br/>原子文件、Progress、Checkpoint、台账"]
    STORE --> RAG["RAG<br/>BM25 + embedding + local vectors + Qdrant"]
    STORE --> QA["Quality<br/>rules + AIGC + review reports"]
    STORE -.-> DASH["Dashboard<br/>只读扫描 data/runs"]
~~~

### 分层职责

| 层 | 目录 | 职责 |
|---|---|---|
| CLI / 阶段编排 | `cmd/novel-studio/` | 命令路由、pipeline 阶段、review/rewrite/deliver、Release 更新和看板控制 |
| Host / Flow | `internal/host/` | 启动与恢复、事件和预算、根据持久化事实生成下一条指令、重复任务熔断 |
| Agent 装配 | `internal/agents/` | Coordinator、Architect、World Simulator、Planner、Drafter、Editor 的模型、prompt、工具和上下文配置 |
| 工具契约 | `internal/tools/` | 规划、推演、写作、校验、提交、评审和 RAG 写入的前置条件与原子语义 |
| 事实模型 | `internal/domain/` | 章节、世界模拟、角色、计划、审核、pipeline、提交阶段和账本 schema |
| 持久化 | `internal/store/` | 原子文件、progress、checkpoints、signals、RAG、角色与世界状态 |
| 召回 | `internal/rag/` | BM25、embedding、本地 GGUF、Qdrant、向量 fallback、facet/stage 路由和重试 |
| 质量 | `internal/rules/`、`internal/aigc/`、`internal/reviewreport/` | 机械 lint、AI 痕迹信号、门禁合并和统一报告 |
| 可导出能力 | `skills/` | 外部 Agent 使用的单一 skill 源目录；写作型入口统一转入 pipeline |
| 看板 | `services/dashboard/` | Python 标准库只读服务，展示全部小说工程的实时状态 |

## Pipeline 执行模型

### 阶段级生命周期

| 阶段 | 主要工作 | 完成证据 |
|---|---|---|
| `cocreate` | 可选的多轮需求澄清 | 定稿创作指令 |
| `architect` | premise、角色、世界规则、分层大纲和写前 foundation | `meta/architect_readiness.*` 与 foundation 指纹 |
| `zero-init` | 第 0 章世界、初始日程、关系/资源/伏笔台账、第一章准备度 | `meta/first_chapter_generation_readiness.*` |
| `write` | 每章世界推演、POV 计划、候选正文、校验和提交 | 正文、commit checkpoint、Progress 与提交 saga |
| `review` | 本地机械/AIGC + Editor + 异模型 Reviewer | 全部报告绑定当前 `body_sha256` |
| `rewrite` | 只处理当前审核认定的 blocking 正文 | 来源 hash、rewrite brief、备份、新正文和复审 |
| `deliver` | 对账最终正文、审核、台账、RAG 与快照 | 非空交付日志、RAG ready、delivery snapshot |

`meta/pipeline.json` 是阶段索引，不是可以手工勾选的完成事实。每个阶段写入 artifact SHA-256；恢复和最终交付都会重新核验。创作指令、模型选择或 prompt 协议指纹变化时，旧完成图会按依赖关系失效。

### 单章关键路径

~~~mermaid
flowchart LR
    S["World Simulator<br/>全部实名角色分批决策"] --> P["Planner<br/>structure + 3 组 details"]
    P --> RP["Frozen Render Packet<br/>只保留正文所需事实"]
    RP --> C["3 个草稿候选并发生成<br/>rewrite 只生成 1 个"]
    C --> PICK["确定性粗糙度初筛<br/>可选 Reviewer pairwise 终选"]
    PICK --> CHECK["draft -> edit -> consistency"]
    CHECK --> COMMIT["Commit Saga<br/>state_applied -> quality_checked<br/>-> checkpointed -> rag_indexed"]
    COMMIT --> REVIEW["Editor || Reviewer<br/>冻结正文并行审核 + 分支缓存"]
    REVIEW --> G{"当前正文是否 blocking"}
    G -->|是| RW["绑定 body hash 的 rewrite"]
    RW --> REVIEW
    G -->|否| NEXT["accept / deliver / 下一章"]
~~~

世界模拟、计划 partial、Progress、Checkpoint、正文换入、review ledger 和 RAG 写入保持单写者顺序。并发只用于同一冻结输入上的草稿候选或独立审核分支，不能用通用并发工具同时修改同一本书的事实线。

## 最新架构基线

当前主干已把长时间空转、上下文放大、同文复评和半提交恢复纳入代码级控制：

| 变化 | 当前行为 |
|---|---|
| 独立 Planner | 新增专用 Planner prompt；全量规划规则不再塞给 Drafter |
| 分阶段上下文 | Planner、World Simulator、Drafter、Draft Finalizer 使用不同上下文预算与裁剪策略 |
| 世界模拟收敛 | 实名角色仍全覆盖，每批最多 8 个角色；partial 有变化才视为真实进展 |
| 计划收敛 | `plan_structure` 后按因果基础、声口/娱乐性、读者合同 3 组 details 收口 |
| 正文 render packet | Drafter 读取专门的精简渲染包，不再直接吞下整份大型计划对象 |
| 冻结输入并发 | 正常草稿 3 候选并发；Editor 与 Reviewer 对同一正文并发，结果串行落盘 |
| 审核缓存 | cache key 绑定正文、模型、prompt、上下文和审核协议；失败重跑只补缺失分支 |
| 提交 saga | `state_applied`、`quality_checked`、`checkpointed`、`rag_indexed` 分阶段记录，恢复时从缺失阶段继续 |
| 返工恢复 | 正文换入后即使进程中断，也能从 rewrite source 和当前 hash 继续复审 |
| 无进展熔断 | 同一任务在 checkpoint / simulation partial / plan partial 都不变时，第 3 次派发中止 |
| 有界运行 | Planner、Simulator、Drafter、Finalizer 使用独立 turn 上限；rewrite 尝试共享章节总 deadline |
| 写作规则前移 | 字数、开篇吸引力、即时兑现、流程压缩、系统同伴声口和长篇首章承诺在 plan/draft/commit 多处校验 |

详细性能与恢复依据见 [Pipeline 性能与恢复审计](docs/pipeline-performance-audit-20260711.md) 和 [Pipeline Recovery Audit](docs/pipeline-recovery-audit-20260710.md)。

## 动态世界与主视角边界

novel-studio 把“世界发生了什么”和“正文能写什么”拆成两层：

1. `simulate_chapter_world` 推进所有实名角色。每个角色必须基于自己的目标、压力、资源、知识边界和误判给出候选、决定、理由、行动耗时、完成度与蝴蝶效应。
2. 模拟生成唯一 `simulation_id` 和 `protagonist_projection`。POV plan 必须引用它，不能另造一套世界事实。
3. `hidden` / `delayed` 决策只约束后续世界反馈；除非沿合法传播路径越过信息地平线，否则旁白和主角心理都不能提前知道。
4. `commit_chapter` 把实际发生的角色状态、知识、关系、资源、时间线和世界增量回写事实层，下一章模拟从这些结果继续。
5. 弧 / 卷边界由 `save_world_tick` 推进离屏日程、社会情绪、仪式和势力进度钟；世界不会在主角离场后暂停。

这条链保证跨章节因果只有一条来源：

~~~text
世界事实 -> 全角色决策 -> 主角投影 -> POV 计划 -> 正文 -> 提交回写 -> 新世界事实
~~~

## 写作与质量合同

| 合同 | 约束 |
|---|---|
| 章节字数 | `user_rules.chapter_words` 是硬门禁；超出后保留草稿，只允许局部 `edit_chapter`，不能反复整章重抽 |
| 开篇吸引力 | 轻松/爽文项目在 plan 中明确前 200 字冲突、不同机制笑点、即时兑现和流程压缩 |
| 长篇第一章 | `longform_opening` 必须说明开篇钩子、连载发动机、长线承诺、解释预算和第一章页面证据 |
| 热梗与流行语 | 必须来自项目简报并绑定角色、场景和使用预算；它是可选风格素材，不会为了过门禁强塞进每章 |
| 系统/同伴声口 | 若用户定义其会交流、吐槽和支持主角，anti-AI 规则不能把它改写成冷硬菜单机器 |
| 计划范围 | `required_beats` 必须发生，`forbidden_moves` 绝不能发生；计划外重大情节必须退回 Planner |
| 正文渲染 | Render packet 只给 Drafter 页面所需事实；流程说明、离屏心理和 latent context 不得整段搬进正文 |
| 题材声口 | `style_contract` 只约束已命中项目的语域、口述气口、喜剧、成长、关系边界和系统声口；用户最新规则始终优先 |
| 对白与分段 | 禁止“人物：台词”剧本格式、同腔解释对白、连续孤句和无功能文字墙；同章 3 个以上话轮反复用 2—4 汉字句号碎断会触发 `dialogue_micro_period_chain` |
| AIGC / AI 腔 | 机械规则、片段信号、本地 detector、Editor 和异模型判定分层合并；warning 与 blocking 分开处理 |
| 审核新鲜度 | review、AI gate 和外部判定必须绑定当前正文 SHA-256；旧报告不能放行新正文 |
| 返工事实保护 | rewrite source 绑定旧终稿、审核 brief、必须保留事实和当前 plan/simulation 来源，防止越改越偏 |

评审维度不仅看语法，还覆盖 plot、character、continuity、pacing、worldbuilding、style/aesthetic、contract 和 ai_voice_detection。外部 Reviewer 的建议会经过本书设定、系统人格和热梗计划过滤，不能用通用审稿偏好推翻用户硬规则。

## RAG 与长程记忆

### 召回分层

| 通道 | 内容 | 使用边界 |
|---|---|---|
| 项目事实 | 当前书的 prompt、brainstorm、foundation、台账、摘要和已提交事实 | Architect、Planner、Writer、Review 的连续性依据 |
| Craft | 写法技巧与拆文方法 | 只迁移手法，不成为世界事实 |
| Benchmark | 对标素材的结构、节奏和场景方法 | 禁止复制原文、角色和专有设定 |
| Calibration | 审核样本、规则命中和 detector 校准 | 只服务 review / rewrite 判断 |

项目事实索引默认只扫描当前项目的安全来源，并跳过章节原文库、拆文库、对标库、历史输出和其他书目目录。外部资料必须通过明确的 `--add-source` 或配置通道加入；设计素材和项目事实不会混成一条无来源记忆。

### 后端与恢复

- 词法召回使用 BM25。
- 语义召回可使用 OpenAI-compatible embedding、本地 GGUF embedding 或其他配置模型。
- Qdrant 是语义主后端；`meta/rag/vector_store.json` 保留本地向量 fallback。
- Qdrant 错误或空结果时依次降级到本地向量和 BM25，不让一次后端故障清空上下文。
- RAG schema hash 覆盖 context、summary、keywords、text 和 metadata；只有语义内容变化才重嵌入。
- 增量写入失败会保存 `pending_upserts.json`，下次启动或 `--rag-ready` 自动回放。
- Qdrant 丢库时可从本地向量恢复，不必重新计算全部 embedding。

~~~bash
# 构建当前项目事实索引
novel-studio --build-rag --dir output/novel

# 同时生成 embedding、写本地向量并同步 Qdrant
novel-studio --build-rag --dir output/novel --with-embeddings

# 在默认项目来源上显式追加写法资料
novel-studio --build-rag --dir output/novel \
  --add-source deconstruction-library/writing-techniques

# 只迁移 schema、回放 pending、复用向量并恢复 Qdrant；不启动写作
novel-studio --rag-ready --dir output/novel

# 离线验证 RAG/embedding/vector store 工件
novel-studio eval inspect --cases evals/cases/harness
~~~

## 恢复、返工与证据

- `meta/checkpoints.jsonl` 是 step 级只追加日志；相同 scope、step 和 digest 重试时保持幂等。
- `meta/pipeline.json` 保存阶段状态、输入指纹、artifact digest 和 evidence，但完成状态会在恢复时重新验证。
- `PendingCommit` 记录章节提交 saga；正文状态、质量、checkpoint 或 RAG 任一步缺失都能继续补齐。
- `PendingRewrites` 是返工队列；存在返工时，Flow Router 优先处理队首章节，不继续写新章。
- review cache 与正式 review artifact 分离。缓存可以避免同文重复调用，正式报告仍按当前正文和项目规则落盘。
- `--restart` 清空 pipeline 阶段索引，不会把缺失的正文事实凭空补出来；通常先运行 `--diag` 判断是否真要重启。
- 不要手工把阶段改成 `completed`，也不要删除 `.pre-rewrite.md`、rewrite source 或 checkpoint 来“解锁”流程。

常用恢复命令：

~~~bash
novel-studio --diag
novel-studio --architect-check --dir output/novel
novel-studio --zero-init --check --dir output/novel
novel-studio --rag-ready --dir output/novel
novel-studio --pipeline --prompt-file prompt.md
~~~

## 进度看板

`services/dashboard/` 使用 Python 标准库启动只读服务，默认扫描仓库 `data/runs/` 下的全部书目；`NOVEL_STUDIO_RUNS_DIR` 可覆盖扫描根目录。页面交叉核对 `progress.json`、正文目录、章节字数、评审正文 hash、返工队列、检查点、RAG 和运行事件，而不是只相信一个进度数字。

详情页包括：

- **总览**：主线下一章、实际工作章、实时步骤、资产覆盖、RAG、成本与数据一致性
- **设定**：premise、世界地图、地点与路线、势力进度钟、世界规则和时间线
- **人物**：分层画像、目标/压力/情绪、知识账本、能力边界、群众名册和关系契约
- **成长轨迹**：人物生命线、阶段事实、长弧规划和决策流
- **计划**：卷弧骨架、章节细纲、下一章计划、伏笔和时间线
- **离屏世界**：tick、独立日程、社会情绪、信息传播、进度钟和事件流
- **质量**：逐章评审、AI gate、本地 AIGC、异模型判定、版本新鲜度和写作指标
- **运行**：模型选择、reasoning effort、事件队列、近期错误和日志

~~~bash
novel-studio service start
novel-studio service status
novel-studio service open
novel-studio service url

# 直接启动服务
python3 services/dashboard/server.py --host 127.0.0.1 --port 8765
~~~

主要 API：

| 端点 | 内容 |
|---|---|
| `/api/health` | 轻量健康检查 |
| `/api/novels` | 全部书目摘要 |
| `/api/novels/<书名>` | 项目总览 |
| `/api/novels/<书名>/setting` | 设定与世界规则 |
| `/api/novels/<书名>/cast` | 人物与关系 |
| `/api/novels/<书名>/growth` | 成长轨迹 |
| `/api/novels/<书名>/plan` | 卷弧与章节计划 |
| `/api/novels/<书名>/offscreen` | 离屏世界 |
| `/api/novels/<书名>/quality` | 审核与质量 |

## 命令速查

| 命令 | 用途 |
|---|---|
| `novel-studio --pipeline --prompt-file prompt.md` | 运行或恢复默认完整 pipeline |
| `novel-studio --pipeline --new-novel --prompt "..."` | 从一句话 idea 新建书目 |
| `novel-studio --pipeline --dir data/runs/<书名>` | 从已存在书目的持久化证据恢复 |
| `novel-studio --pipeline --stages review --from 1 --to 10` | 只评审指定章节 |
| `novel-studio --pipeline --stages rewrite --max-rewrite-rounds 3` | 按 blocking 反馈返工并复审 |
| `novel-studio --cocreate` | 多轮澄清创作需求 |
| `novel-studio --check` | 验证 provider、model 与 fallback 连通性 |
| `novel-studio --architect-check --dir output/novel` | 核验 foundation 并生成 readiness |
| `novel-studio --zero-init --dir output/novel` | 生成第一章前置推演资产 |
| `novel-studio --rag-ready --dir output/novel` | 单独恢复/验证 RAG |
| `novel-studio --build-rag --dir output/novel` | 重建项目 RAG |
| `novel-studio --diag` | 只读诊断并导出脱敏报告 |
| `novel-studio --refresh-progress --dir output/novel` | 回填推进、人物变化和下一章台账 |
| `novel-studio --writing-assets list` | 查看写法资产 |
| `novel-studio --simulate` / `--import-sim` | 合成或导入仿写画像 |
| `novel-studio --steer "指令"` | 给下一次恢复排队干预 |
| `novel-studio list` | 查看 `data/runs/` 下全部书目 |
| `novel-studio reader-metrics log ...` | 登记真实读者反馈用于校准 |
| `novel-studio eval inspect ...` | 离线检查项目工件与 RAG |
| `novel-studio skills list` | 列出内置 skills |
| `novel-studio skills export --to <dir>` | 导出 skills |
| `novel-studio update [version]` | 更新到 latest 或指定 Release |
| `novel-studio --version` | 查看版本、commit 和构建时间 |

完整参数以本机二进制为准：

~~~bash
novel-studio --help
novel-studio --pipeline --help
novel-studio --build-rag --help
novel-studio --zero-init --help
novel-studio service --help
novel-studio skills --help
~~~

## 配置

novel-studio 支持 OpenAI、Anthropic、Gemini、OpenRouter、DeepSeek、Qwen、GLM、Grok、MiniMax、Ollama、Bedrock 和兼容代理，也支持通过本机 Codex CLI 接入订阅能力。模型调用是否离线取决于你的 provider；只有使用本地生成模型和本地 embedding 时，正文处理才可完全离线。

配置重点：

- `providers`：凭证、协议、base URL、模型列表和 provider 额外请求参数
- `roles`：`coordinator`、`architect`、`writer`、`editor`、`reviewer` 的主模型、fallback 和 reasoning effort
- `context_window` / `context_windows`：真实模型窗口和压缩阈值来源
- `rag.embedding`：远程 embedding 或本地 GGUF embedding
- `rag.qdrant`：URL、collection、本地进程或 Docker 自动启动
- `rag.craft_library` / `benchmark_library` / `calibration_library`：设计与审核素材的隔离通道
- `budget`：单书 USD 告警和硬停止
- `notify`：桌面或自定义通知
- `aigc.real_lm`：外部 detector 的观测/融合配置

不要提交真实 API key。项目级 `.novel-studio/config.json` 会覆盖全局值；若切换默认 provider，必须同时提供同名的 `providers.<name>` 配置。

## 输出结构

新书通常落在 `data/runs/<书名>/`；直接在普通项目目录运行时，默认输出根是 `output/novel/`。

~~~text
data/runs/<书名>/
├── prompt.md
├── brainstorm.md
└── output/novel/
    ├── premise.md                  # 故事前提
    ├── characters.json             # 核心人物档案
    ├── outline.json                # 章节大纲
    ├── layered_outline.json        # 长篇卷弧结构
    ├── world_rules.json            # 世界规则
    ├── book_world.json             # 地点、路线与势力图谱
    ├── chapters/                   # 已提交正文
    ├── drafts/                     # structure、plan details、草稿和 partial
    ├── reviews/                    # Editor、AI gate、Reviewer 与统一报告
    ├── summaries/                  # 章、弧、卷摘要
    └── meta/
        ├── progress.json
        ├── checkpoints.jsonl
        ├── pipeline.json
        ├── architect_readiness.*
        ├── first_chapter_generation_readiness.*
        ├── chapter_simulations/
        ├── character_stage/
        ├── side_character_journeys/
        ├── chapter_world_deltas/
        ├── rag/
        └── delivery_snapshots/
~~~

所有运行事实都应通过工具或维护命令更新。不要把聊天历史当作项目真相，也不要靠手工编辑 `progress.json` 修复章节状态。

## Skills

`skills/` 是可导出能力的唯一源目录。`story-long-write`、`story-short-write`、`story-douban-long-write` 等写作方法 skill 负责整理输入和方法约束，正文生成必须回到 `novel-studio --pipeline`，避免旁路写作绕开世界模拟、字数合同和审核门禁。

~~~bash
novel-studio skills list
novel-studio skills export --to ./exported-skills
python3 scripts/validate_skill_context.py
~~~

## 开发与验证

要求 Go 1.25；看板要求 Python 3；embedding/Qdrant 按配置选用。

~~~bash
go test -count=1 ./...
go test -race -count=1 \
  ./internal/writer/sampler \
  ./cmd/novel-studio \
  ./internal/tools \
  ./internal/host/flow
go vet ./...
go build -o /tmp/novel-studio ./cmd/novel-studio
python3 scripts/validate_skill_context.py
python3 -m unittest services.dashboard.test_server -v
python3 -m py_compile \
  services/dashboard/server.py \
  services/dashboard/test_server.py \
  quality/audit/scripts/aigc_value.py \
  quality/audit/scripts/content_lint.py
git diff --check
~~~

## 文档索引

| 文档 | 内容 |
|---|---|
| [系统架构](docs/architecture.md) | Host、Coordinator、Tools、Store、上下文与 Agent 拓扑 |
| [项目结构](docs/project-structure.md) | 顶层目录与资料归属 |
| [能力清单](docs/capability-inventory.md) | CLI、Agent、Store、RAG、质量和服务能力盘点 |
| [设计阶段工作流](docs/design-stage-workflow.md) | 长短篇规划与写前设计口径 |
| [上下文管理](docs/context-management.md) | 压缩、store summary 和恢复包 |
| [数据生命周期](docs/data-lifecycle-and-progression.md) | 章节、角色、世界与推进台账 |
| [写作审核工作流](docs/writing-review-workflow.md) | 写作、评审、返工与交付 |
| [用户规则运行时](docs/user-rules-runtime.md) | 用户硬规则如何进入工具门禁 |
| [评测系统](docs/evaluation-system.md) | Harness、指标和回归验证 |
| [可观测性](docs/observability.md) | 事件、usage、trace 和诊断 |
| [Pipeline 性能审计](docs/pipeline-performance-audit-20260711.md) | 最新并发边界、缓存、熔断和恢复设计 |
| [Pipeline Recovery Audit](docs/pipeline-recovery-audit-20260710.md) | 全角色模拟、RAG 恢复和返工链问题清单 |
| [工程交付记录](docs/engineering-delivery-20260708.md) | DeepSeek 复审、AIGC 门禁和历史交付证据 |
| [拆文库规范](deconstruction-library/README.md) | RAG 设计素材与拆解工作区约定 |
| [架构总览 HTML](docs/architecture-overview.html) | 浏览器可视化架构说明 |

核心运行时使用 Go 1.25、[agentcore](https://github.com/voocel/agentcore) 和仓内维护的 [litellm fork](third_party/litellm/README.md)；浏览器看板使用 Python 3 标准库；Qdrant 与 llama.cpp/GGUF embedding 均为可配置组件。

## Release 升级

~~~bash
novel-studio --version
novel-studio update
novel-studio update v0.2.0
~~~

长任务运行中不要替换二进制。等待当前 checkpoint 落盘并退出后再升级；升级完成后先运行 `novel-studio --check`，再从原项目目录恢复 pipeline。

## License

[Apache License 2.0](LICENSE)

## 联系

问题反馈优先提交到 [GitHub Issues](https://github.com/Xiaoyangy/novel-studio/issues)。

<img src="docs/assets/wechat-qr.jpg" alt="novel-studio 项目联系二维码" width="180">
