<div align="center">

<img src="docs/assets/novel-studio-hero.jpg" alt="novel-studio：开源、本地优先的 AI 长篇小说创作引擎，使用世界模拟、多智能体规划与 RAG 长程记忆" width="100%">

# novel-studio — 开源、本地优先的 AI 长篇小说创作引擎

**先推演世界，再规划弧线，最后把主角真正看见的因果写成正文。**

Open-source, local-first AI novel generator with self-hosted orchestration for long-form fiction, web novels and story production.

[![GitHub Stars](https://img.shields.io/github/stars/Xiaoyangy/novel-studio?style=flat&logo=github&color=E3B341)](https://github.com/Xiaoyangy/novel-studio)
[![Release](https://img.shields.io/github/v/release/Xiaoyangy/novel-studio?logo=github)](https://github.com/Xiaoyangy/novel-studio/releases/latest)
[![Go](https://img.shields.io/badge/Go-1.25.5-00ADD8?logo=go&logoColor=white)](go.mod)
[![Platform](https://img.shields.io/badge/macOS%20%7C%20Linux%20%7C%20WSL2-supported-555)](#运行要求)
[![License](https://img.shields.io/github/license/Xiaoyangy/novel-studio)](LICENSE)

[简体中文](README.md) · [English](README_EN.md)

[快速开始](#快速开始) · [为什么是 novel-studio](#为什么是-novel-studio) · [效果预览](#效果预览) · [工作流](#从世界到正文) · [渲染风格](#渲染风格只改变怎么写) · [RAG](#rag-不是装饰) · [文档](#文档与社区)

</div>

---

novel-studio 是一个面向**长篇小说、网文连载、短篇整书和故事工作室**的开源 AI 写作系统。它把通常散落在聊天记录里的大纲、人物、世界状态、RAG 记忆、正文审核和返工过程，变成可落盘、可验证、可恢复的生产流水线。

它不是“把上一段继续写长”的聊天壳。系统先冻结全书导航，再按弧推演所有角色的选择与后果；一弧规划完整并封存后，才逐章渲染、逐章审核。正文只看到主视角有权知道的事实，隐藏世界状态不会为了方便推进而直接泄漏。

## 为什么是 novel-studio

普通 AI 写作很擅长生成一段“像小说”的文字，真正困难的是让几十万字以后的人物仍然知道自己为什么行动、拥有什么、错过了什么，以及一个离屏决定如何改变下一章。

novel-studio 把这些问题拆成独立且可审计的工程边界：

| 长篇创作难题 | novel-studio 的处理方式 |
|---|---|
| 角色围着主角静止，或提前知道秘密 | 每名角色拥有自己的目标、压力、资源、知识与离屏行动；完整世界状态与 POV 可见事实严格分层 |
| 大纲写完，正文仍然自由发挥 | 先冻结全书卷—弧—章导航，再完整推演并封存当前弧；正文只能消费已封存的章节合同 |
| RAG 召回很多，正文却像没用 | 命中必须转换成带来源的事实锚点或写法方法，再进入当前章的不可变 render packet |
| 连载越长，声口越漂、句式越重复 | 配置风格与已验收正文的防复读记忆合成同一份不可变合同；风格只能改变表达，不能改写剧情事实 |
| 文字像报告，检测指标反客为主 | 三采样与读者体验分优先判断现场、对白、节奏、POV 和章末推力；反 AI 腔与检测器只作护栏 |
| 返工后审核的不是同一稿 | 计划、正文、审核、状态变化与交付绑定同一个 exact-body SHA-256 |
| 长任务中断后只能重跑，重试成本失控 | pipeline、弧规划、候选正文、审核与发布都有 checkpoint、租约、恢复回执和持久调用预算 |
| 生产过程是黑箱，项目被锁在服务商里 | 世界、章节、索引与凭证保存在本地项目目录；看板展示规划、正文、RAG、调用、成本与错误 |

## 效果预览

![novel-studio AI 小说生产进度看板：章节、按弧规划、审核、RAG、模型用量与运行状态](docs/assets/dashboard-overview-20260720.jpg)

<details>
<summary><strong>展开人物与离屏世界视图</strong></summary>

![novel-studio 人物模拟：角色档案、目标压力、知识边界、关系与成长轨迹](docs/assets/dashboard-characters-20260710.webp)

![novel-studio 离屏世界模拟：角色独立行动、势力进度钟、社会情绪与信息传播](docs/assets/dashboard-offscreen-20260710.webp)

</details>

看板只读交叉核对正文、进度、弧规划、评审、RAG、checkpoint 和运行事件。冻结的全书大纲、当前弧正式 plan、正在渲染的章节和已验收正文分别统计，不会把“有章纲”误报成“已经写完”。

## 快速开始

### 运行要求

- macOS 或 Linux；Windows 请使用 WSL2，不要使用旧 Release 中的原生 Windows ZIP。
- 使用 Release 安装无需本地 Go 工具链；从源码构建需要 Go 1.25.5。
- 至少配置一个可用的文本模型 provider；完整 sealed pipeline 要求 `roles.reviewer` 显式指向 DeepSeek，独立的 `--draft-ai-judge` 命令也会强制校验这一点。
- 是否完全离线还取决于 provider、embedding、向量服务，以及本次流程是否调用 `web_research` 等联网能力。
- 进度看板使用 Python 3，当前需从源码 checkout 的项目根目录启动；Release 一键安装目前只安装 CLI 二进制。RAG embedding 与 Qdrant 按配置启用。

### 1. 安装

本页描述当前 `main`。想体验这里介绍的最新生产合同与看板，请选择源码构建；GitHub Release 更适合只需要稳定 CLI 的用户，但可能晚于主干能力。下面两种方式二选一。

```bash
# 方式 A：当前 main + 完整看板
git clone https://github.com/Xiaoyangy/novel-studio.git
cd novel-studio
mkdir -p "$HOME/.local/bin"
go build -o "$HOME/.local/bin/novel-studio" ./cmd/novel-studio
export PATH="$HOME/.local/bin:$PATH"
```

```bash
# 方式 B：稳定 Release，仅安装 CLI；写入用户目录，不触发 sudo
mkdir -p "$HOME/.local/bin"
curl -fsSL https://raw.githubusercontent.com/Xiaoyangy/novel-studio/main/scripts/install.sh \
  | NOVEL_STUDIO_INSTALL_DIR="$HOME/.local/bin" sh
export PATH="$HOME/.local/bin:$PATH"
```

### 2. 配置并检查模型

```bash
novel-studio
novel-studio --check
```

全局配置位于 `~/.novel-studio/config.json`；项目中的 `./.novel-studio/config.json` 可以覆盖它。首次引导只配置默认模型，不会自动建立独立的角色路由；运行完整 sealed pipeline 前，请按 [配置示例](config.example.jsonc) 补充 `providers.deepseek` 与 `roles.reviewer`。`--check` 验证已配置路由的连通性，pipeline 还会在正文审核前拒绝非 DeepSeek reviewer。

### 3. 开始一本新书

```bash
novel-studio --pipeline --new-novel \
  --prompt "写一部 12 章完结的双女主都市悬疑短篇；每章 2000—2500 中文字；人物边界和结局回收必须先在章纲中冻结"
```

长期项目建议把完整创作契约放进文件：

```bash
novel-studio --pipeline --new-novel --prompt-file prompt.md
```

这条命令会创建书目并进入**有界、可恢复**的生产流程，不会在一个无限上下文里盲写整本书。默认每次只推进当前合法阶段，并最多验收下一章正文。

### 4. 恢复下一步

```bash
novel-studio --pipeline --dir data/runs/<书名>
```

重复同一条命令，直到所有弧和章节都验收完成。pipeline 会按落盘证据幂等继续：身份完整的候选稿可以接着补齐审核与发布回执，不会仅因进程崩溃再次调用 Drafter；摘要漂移、证据缺失或路径异常会在 provider 调用前失败闭锁。

不要手改 `progress.json`、`.render-candidates/`、`.render-transactions/`、`meta/runtime/` 或 `reviews/`，也不要同时为同一本书启动两条写作 pipeline。

> **升级兼容：**已经正式验收的 legacy v1/v2 正文与 acceptance v2 保持只读兼容；升级前仍在途的旧 dispatch、permit 或风格协议不会靠手工补字段继续复用。请保留 sealed 输入，让 pipeline 建立新的 candidate / dispatch 证据链。

### 5. 终审并交付短篇

默认阶段不包含全文终审。短篇末章和终弧回执都已落盘后，显式执行：

```bash
novel-studio --pipeline --dir data/runs/<书名> \
  --stages finalize,deliver
```

满足短篇全局终审合同的项目会生成 `output/novel/正文.md`、全文终审与出版包；长篇的当前终态是全书章级验收链完成，不冒充全书 exact-book 终审。

### 6. 打开进度看板（源码 checkout）

```bash
novel-studio service open
```

`service open` 会在需要时后台启动看板并打开浏览器，默认地址是 [http://127.0.0.1:8765/](http://127.0.0.1:8765/)。若要在前台查看服务日志，请在单独终端运行 `novel-studio service start`。

> **路径速记：**pipeline 与 `--diag` 的 `--dir` 指向 `data/runs/<书名>`；RAG 命令指向 `data/runs/<书名>/output/novel`；看板命令需在源码 checkout 根目录运行。

## 从世界到正文

```mermaid
flowchart LR
    P["Idea / Prompt"] --> B["Brainstorm"]
    B --> A["Architect"]
    A --> O["冻结全书章纲"]
    O --> Z["Zero-init"]
    Z --> AP["当前弧全章推演<br/>World + POV + Capacity"]
    AP --> S["Seal 当前弧"]
    S --> R["逐章 Render"]
    R --> Q["逐章 Exact-body Review"]
    Q -->|弧内仍有章节| R
    Q -->|弧内全部通过| C["Arc Completion"]
    C -->|还有下一弧| AP
    C -->|长篇终卷完成| F["全书章级回执完成"]
    C -->|符合短篇终审范围| SF["Finalize + Deliver"]
```

这里最关键的边界是：

1. **全书章纲先冻结**：提供全局方向与章节位置，但它不等于各章已经正式规划。
2. **推演一弧，渲染一弧**：当前弧全部章节的角色决定、跨章因果、POV 信息边界和正文承载力完成后，才允许 seal。
3. **渲染仍然逐章**：每次只提升下一份不可变 chapter bundle，生成隔离候选正文，并对该章最终 body 做审核。
4. **审核通过才进入正史**：候选正文、实际状态变化与 sealed plan 一致后才原子发布；失败稿保留诊断但不污染 live canon。
5. **一弧结束再进入下一弧**：弧内缺章、缺 acceptance receipt 或正文 SHA 漂移，都会阻止下一弧启动。当前全书 exact-book finalize / publication package 仅用于满足短篇终审合同的项目，不能冒充长篇全书终审。

### 为什么按弧，而不是全书一次推演？

全书章纲适合固定方向，单章计划适合执行，但真正决定故事是否完整的是“这一段因果如何跨越多章并收束”。按弧推演让角色选择、伏笔、资源变化与章节钩子在一个联合窗口内互相校验；按章渲染又把正文质量和返工成本限制在可控范围内。

完整的 generation、bundle、obligation registry、promotion、actual outcome 和恢复协议见 [Project-All 按弧架构](docs/project-all-architecture.md)。

## 渲染风格只改变“怎么写”

在 `~/.novel-studio/config.json` 或项目级 `./.novel-studio/config.json` 中选择风格：

```json
{
  "style": "suspense"
}
```

内置 `default`、`suspense`、`fantasy` 和 `romance`。风格合同只允许调整叙述声口、距离、用词、句法、节奏、意象、感官、段落与对白质感；它不能新增、删除或调序事件，也不能改写人物决定、事实、因果、状态或 POV 知识边界。

渲染前，系统会把选中的配置风格与**已验收正文**编译成有效风格合同。后者形成 serial style memory，用来识别跨章复现的非必要短语、逐字句和同构开收尾；章节标题与正史专名会被排除，避免为了“防重复”机械改名或破坏连续性。

```text
frozen render packet + selected style + accepted-prose surface stats
                              ↓
             immutable effective-style receipt
                       ↙             ↘
                  Drafter           Editor
                 （同一份 canonical bytes + digest）
```

Render 阶段不会临时重做世界推演，也不会读取 live RAG。风格回执会归档并绑定候选、审核与 acceptance，因此恢复后仍能证明 Drafter 和正式 Editor 使用的是同一份合同。协议、恢复与兼容细节见 [渲染风格流水线审计](docs/design-audits/render-style-pipeline-audit-20260722.md)。

## 正文质量闭环

```text
sealed chapter plan + exact frozen render context
                    ↓
     effective style + accepted-prose memory
                    ↓
 immutable style receipt + typed preflight + one-shot permit
                    ↓
          isolated draft by Drafter
                    ↓
   deterministic gates + hard consistency + commit
                    ↓
 exact-body local checks + Reviewer + Editor + DeepSeek judge
                    ↓
 actual-delta match + atomic publish + acceptance receipt
```

每章都要回答四个问题：

- **事实对不对**：金额、数量、时间、地点、授权、知识边界与因果顺序是否符合 sealed plan。
- **故事好不好看**：目标、阻力、行动、转折、关系位移、读者回报和章末钩子是否成立。
- **文字像不像人写的小说**：是否出现流程报告、同构节奏、过度解释、对白传送带或元数据泄漏。
- **审核的是不是同一稿**：plan、正文、Reviewer、Editor、consistency、commit 和交付是否绑定同一个 SHA。

当前 acceptance 直接绑定六项正式审核工件：Editor JSON、统一评审报告、机械 AI gate、AI 声纹红旗、DeepSeek judge JSON 与模型来源证明；provenance 还会继续绑定模型缓存和 DeepSeek Markdown。正式路径集合出现缺项、替换或额外项，或者正文发生漂移，都会阻止验收。

正文是给读者看的，不是给检测器过的。系统会算一个确定性的**读者体验分**（现场具体度、对白活性、句长节奏起伏、主视角在场、章末前推力，越高越好读），三采样据此选出更好读的一稿，评审报告与看板都会展示它。它刻意保持为软信号：只把正文推向读者，而反 AI 腔与外部检测始终是底线护栏——达标只是及格，真正决定一章成败的是读者愿不愿意读下去。

外部人工检测属于用户可选抽查。novel-studio 不自动操作第三方检测网站，也不会因为用户没有逐章上报外部得分而阻塞生产。完整边界见 [外部检测协议](docs/external-detector-protocol.md)。

## RAG 不是装饰

novel-studio 的检索增强生成面向长篇小说的“可追溯使用”，而不是把一堆相似文本塞进正文上下文：

```text
BM25 / embedding / Qdrant 命中
              ↓
 exact source ref + content-addressed receipt
              ↓
 Planner 转换成当前章事实锚点或写法方法
              ↓
 sealed render_packet
              ↓
 Drafter 只消费最小、可见、已转化的输入
```

| RAG 通道 | 用途 |
|---|---|
| 项目事实 | 世界规则、人物状态、章节事实、资源、关系和伏笔 |
| 写法资料 | 对话、场景、节奏、类型文技巧与方法卡 |
| 对标素材 | 隔离处理后的结构样本与参考作品拆解 |
| 审核校准 | 可读性、AIGC、平台反馈和历史修改建议 |

每次当前弧推演都会冻结独立的 `rag_snapshot_root`。Drafter 看不到 raw hits，也不会在 render 阶段临时连接 live Qdrant；真正进入正文执行层的是已经有来源、有用途、有边界的最小输入。

这条证据链能证明资料被检索、转化并受控注入规划，不会机械声称每个软性事实锚点或写法建议都已经改变最终正文。

```bash
# 构建或刷新项目索引
novel-studio --build-rag --dir data/runs/<书名>/output/novel

# 修复并验证 RAG / embedding / vector store 状态
novel-studio --rag-ready --dir data/runs/<书名>/output/novel
```

## 模型与部署

novel-studio 可以按角色选择不同 provider、model 和 reasoning effort。当前适配包括 OpenAI、Anthropic、Gemini、OpenRouter、DeepSeek、Qwen、GLM、Grok、MiniMax、Mimo、Ollama、Bedrock、OpenAI-compatible 代理，以及本机 Codex CLI。适配器存在不等于所有模型版本都已在每个生产角色上完成验证。

| 配置 | 作用 |
|---|---|
| `providers` | API key、协议、base URL、模型和附加参数 |
| `roles` | Coordinator、Architect、Writer（World Simulator / Planner 共用）、Drafter、Editor、Reviewer 的模型分工 |
| `context_window` | 真实上下文窗口与压缩依据 |
| `rag.embedding` | 远程 embedding 或本地 GGUF embedding |
| `rag.qdrant` | Qdrant 地址、collection 与自动启动方式 |
| `budget` | 单书成本告警与硬停止 |
| `notify` | 桌面或自定义通知 |

**Local-first / 自托管编排不等于默认完全离线或完全私密。** 项目文件与状态保存在本机；文本是否离线生成，取决于你选择 Ollama、本地兼容服务还是远程 API。完整 sealed pipeline 的裸正文审核要求 `reviewer` 显式指向 DeepSeek，其他生产角色仍可独立路由。即使模型、embedding 与 Qdrant 都在本地，brainstorm 或返工阶段调用 `web_research` 时仍会联网。不要把真实 API key 提交到仓库。

## 适合谁

- 想写几十章到数百章网文、长篇小说或系列故事的作者。
- 需要人物状态、知识边界、关系、伏笔和资源长期一致的创作团队。
- 想自托管 AI 写作流程，并掌控模型、RAG、成本和项目文件的开发者。
- 在研究多智能体写作、世界模拟、长上下文治理与可恢复 Agent pipeline 的工程师。
- 需要把短篇生产拆成规划、渲染、审核、全文终审和交付包的内容工作室。

它目前不是拖拽式桌面写作软件，也不承诺“一条提示词无人值守产出完美百万字成书”。百万字级项目是架构目标，不代表已经完成百万字成书质量验证；最终质量仍取决于创作契约、模型能力、RAG 资料、审核标准、预算和作者抽查。

## 常用命令

下表中的 `<RUN>` 表示 `data/runs/<书名>`。

| 命令 | 用途 |
|---|---|
| `novel-studio --pipeline --new-novel --prompt "..."` | 新建书目并启动生产流程 |
| `novel-studio --pipeline --dir <RUN>` | 从可信证据恢复下一步 |
| `novel-studio --pipeline --dir <RUN> --stages preplan,project-all,seal` | 只完成当前弧全部章节的正式推演与封存，不写正文 |
| `novel-studio --pipeline --dir <RUN> --stages preplan,project-all,seal,promote,render` | 复核 sealed arc，并渲染、审核下一章 |
| `novel-studio --pipeline --dir <RUN> --stages render --refresh-render-input` | 仅为尚未开始且没有 durable candidate evidence 的 sealed 章节刷新 model / provider / prompt 绑定 |
| `novel-studio --pipeline --dir <RUN> --stages finalize,deliver` | 仅对满足全局终审范围的短篇，在逐章通过后执行 exact-book 终审并生成出版包 |
| `novel-studio --build-rag --dir <RUN>/output/novel` | 构建项目 RAG 索引 |
| `novel-studio --rag-ready --dir <RUN>/output/novel` | 验证 embedding 与向量状态 |
| `novel-studio service open` | 打开进度看板 |
| `novel-studio --diag --dir <RUN>` | 生成诊断报告，不推进小说生产状态 |
| `novel-studio --check` | 检查 provider、model 与 fallback 配置 |

高级 rebase、outline repair、successor generation、慢章诊断、完整输出树和 execution receipt 说明集中在 [生产与运维参考](README-TECHNICAL.md)，避免产品 README 退化成长篇变更日志。

## 项目数据

```text
data/runs/<书名>/
├── brainstorm.md
├── prompt.md                       # 可选的稳定创作契约
├── archives/                       # rebase 前精确归档
└── output/
    ├── .render-candidates/         # 候选目录、拒稿与独立 style-epoch 意图
    ├── .render-transactions/       # 不可变阶段回执
    └── novel/
        ├── premise.md
        ├── characters.json
        ├── layered_outline.json
        ├── world_rules.json
        ├── chapters/               # 已验收正文
        ├── reviews/                # 六项 acceptance 正式证据及其 provenance 依赖
        ├── 正文.md                 # 短篇 finalize 后的全文
        └── meta/                   # 进度、世界状态、RAG、规划、凭证与交付包
```

项目真相以落盘工件为准，不以聊天历史、模型自述或单个进度数字为准。

## 文档与社区

| 文档 | 内容 |
|---|---|
| [English README](README_EN.md) | English overview, quick start and architecture |
| [生产与运维参考](README-TECHNICAL.md) | execution lock、receipt、恢复、rebase、outline repair、命令和完整输出结构 |
| [系统架构](docs/architecture.md) | Host、Agent、Tools、Store 与上下文拓扑 |
| [Project-All 按弧架构](docs/project-all-architecture.md) | 全书定位、当前弧推演/seal、逐章验收与下一弧解锁 |
| [设计阶段工作流](docs/design-stage-workflow.md) | Architect、outline-all 与 zero-init |
| [上下文管理](docs/context-management.md) | 阶段化压缩、收据与恢复包 |
| [数据生命周期](docs/data-lifecycle-and-progression.md) | 章节、角色、世界和推进台账 |
| [写作审核工作流](docs/writing-review-workflow.md) | draft、review、rewrite、commit 与 deliver |
| [RAG Pipeline Audit](docs/design-audits/harness-rag-pipeline-audit.md) | RAG、Harness 与 pipeline 审计 |
| [渲染风格流水线审计](docs/design-audits/render-style-pipeline-audit-20260722.md) | surface-only style、serial memory、Drafter / Editor 同源合同、v3 证据链与恢复边界 |
| [评测系统](docs/evaluation-system.md) | 测试案例、指标与回归 |
| [可观测性](docs/observability.md) | 事件、usage、trace 和诊断 |

发现问题或有功能建议，请提交 [GitHub Issue](https://github.com/Xiaoyangy/novel-studio/issues)。代码贡献欢迎先说明使用场景、当前行为和期望边界；涉及 pipeline 的改动请同时附上回归测试。

### Roadmap

- 更轻量的新手模板与示例书目。
- 可公开复现的长篇连续性、RAG grounding 与正文质量 benchmark。
- 更完整的英文文档和跨平台安装体验。
- 看板中的运行诊断与人工确认工作流。

## FAQ

<details>
<summary><strong>novel-studio 是 AI 小说生成器还是写作助手？</strong></summary>

两者都是，但更准确地说，它是一个 AI 小说生产引擎：从 brainstorm、世界设定、全书章纲、按弧角色推演，到逐章正文和审核都由同一套可恢复数据合同连接；满足短篇终审合同的项目还可执行全文终审与交付。

</details>

<details>
<summary><strong>它能一键写完一本百万字小说吗？</strong></summary>

不能把它理解成“点击一次，自动交付百万字成书”。系统为长周期项目设计，通过多次有界调用逐弧、逐章推进；目前没有宣称已完成一部百万字成书的生产级质量验证，质量、速度和成本仍取决于模型、题材、创作契约、RAG 与审核要求。

</details>

<details>
<summary><strong>它真的使用 RAG 吗？</strong></summary>

使用。项目支持 BM25、embedding、本地向量与 Qdrant，并要求召回命中经过 exact ref、receipt 和 Planner 转换后才能进入 sealed render packet。正文模型不会直接看到 raw RAG 命中。

</details>

<details>
<summary><strong>可以使用本地模型或完全离线运行吗？</strong></summary>

可以配置 Ollama、本地 OpenAI-compatible 服务、本地 GGUF embedding 和自托管 Qdrant。只有所有角色与检索组件都在本地，并且本次流程没有调用 `web_research` 或其他联网安装/拉取动作时，才能称为完全离线。

</details>

<details>
<summary><strong>为什么要绑定正文 SHA？</strong></summary>

因为“审核通过”只有在审核对象与最终发布正文逐字相同时才有意义。novel-studio 使用 exact body SHA 把候选、Review、Editor、consistency、commit、acceptance 和最终交付串成同一证据链。

</details>

<details>
<summary><strong>切换写作风格会改变剧情规划吗？</strong></summary>

不会。风格只控制已经冻结内容的表达方式；事件、人物决定、事实顺序、因果、状态与 POV 知识边界仍以 sealed plan 和 render packet 为准。若配置风格试图注入新的剧情语义，系统会在进入正文模型前拒绝它。

</details>

## 开发与验证

```bash
go test -count=1 ./...
go vet ./...
go build -o /tmp/novel-studio ./cmd/novel-studio

python3 scripts/validate_skill_context.py
python3 -m unittest discover -s quality/audit/scripts -p 'test_*.py' -v
python3 -m unittest services.dashboard.test_server -v

git diff --check
```

## License

[Apache License 2.0](LICENSE)

<div align="center">

如果 novel-studio 对你的 AI 写作、长篇小说或 Agent 工程有帮助，欢迎 [⭐ Star](https://github.com/Xiaoyangy/novel-studio) · [提交 Issue](https://github.com/Xiaoyangy/novel-studio/issues) · 分享你的真实生产经验。

</div>
