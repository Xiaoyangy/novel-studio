# deconstruction-library — 统一 RAG 语料源与拆解工作区

`novel-studio` 所有**要 RAG 化的语料**统一放在本目录。它同时承担四个角色，进入哪条检索通道由代码级准入策略（`internal/rag/policy.go`）决定，不靠自觉：

| 子目录 | 角色 | RAG 通道 | source_kind |
|---|---|---|---|
| `{书名}/` | 长篇拆解工作区（每本一目录） | **禁入**写作 RAG；产物经同步进写作项目 `对标/` 供 skill 流程读取 | — |
| `writing-techniques/` | 写作手法库（craft）：描写词库、史料常识、体系分级、创作方法论，**不携带任何对标作品的情节与设定事实** | 设计时刻 `craft_recall`（`rag.craft_library`） | `craft_technique` |
| `novel_all/` | 对标素材库（benchmark）：教程 / 大纲 / 题材 / 人设 / 词汇 / 爽点 / 拆文 / 运营 / 文笔 / 心理 / 场景 11 类归并库 | 设计时刻 `craft_recall`（`rag.benchmark_library`），**只可迁移手法 / 结构 / 节奏，禁止照搬情节、人名与专有设定** | `benchmark_reference` |
| `review-calibration/` | AI 味检测的人类基线语料（高质量 / 普通人类文本 + 校准报告） | 仅离线统计与一次性 prompt：slop 词表生成（`quality/audit/scripts/build_slop_lexicon.py`）以此为 A 组、人味盲测按需取段；**禁入任何索引与持久化上下文** | — |

三条铁律：

1. **设计库不进章节召回** —— `craft_technique` / `benchmark_reference` 只服务零章初始化与"新角色 / 新武器 / 新能力首次出场"的章计划时刻；检索结果立刻实例化为本书事实（dossier / props / world_codex），写作中途的一致性只信本书事实层。
2. **散源禁入** —— 归并前的散目录（`novel_sucai*`、`*.bak` 等）与 `{书名}/` 拆解产物一律不进写作 RAG，防止对标情节污染正文与重复计数。
3. **人类基线只做统计** —— `review-calibration/` 参与词表与阈值校准，绝不作为写作素材召回。

## 接线方式

```jsonc
// ~/.novel-studio/config.json 或 ./.novel-studio/config.json
"rag": {
  "craft_library":     "deconstruction-library/writing-techniques",
  "benchmark_library": "deconstruction-library/novel_all"
}
```

`--build-rag` / `--zero-init` 重建索引时自动附带双库；类目路由是确定性的（每个设计字段绑定固定类目 filter，见 `writing-techniques/INDEX.md` 的字段绑定表与 `novel_all/INDEX.md`），BM25 只在命中子集内排序，查不到返回显式 `no_material`。

## 新建一本长篇拆解

```bash
cp -R deconstruction-library/_templates/longform-decon-project "deconstruction-library/书名"
# 原文放进 deconstruction-library/书名/原文/ ，然后触发拆解：
# Claude Code / OpenCode 用 /story-long-analyze，Codex 用 $story-long-analyze
# （需要先 novel-studio skills export --to <目标目录> 部署 skills）
```

拆解产物（黄金三章、逐章摘要、角色、剧情模块、设定、文风、拆文报告）留在本目录作为数据源；写作项目通过同步把 `deconstruction-library/{书名}/` 复制为项目内 `对标/{书名}/`，`story-long-write` 日更流程按"项目对标视图优先、回退本库"读取文风与情绪 / 节奏模块。

## 每本书的目录结构

```text
deconstruction-library/{书名}/
├── 原文/                 # 原文备份（默认不提交）
├── 概要.md
├── 快速预览.md            # 黄金三章拆完的早期判断快照
├── 章节/                 # 逐章摘要（*_摘要.md）
├── 角色/
├── 剧情/
│   ├── 故事线.md
│   ├── 节奏.md            # 关键信息推进 / 爽点触动节奏（v12 契约权威轴）
│   └── 情绪模块.md         # 读者需求 / 情绪引擎 / 可复现模块（v12 契约权威轴）
├── 设定/
│   ├── 世界观/            # 按主题拆分的多文件
│   └── 势力/
├── 文风.md               # Stage 6 产出：句长 / 标点 / 潜台词 / 情绪交替 + 原文范例
├── 拆文报告.md
└── _progress.md          # 拆解断点（paused_after_stage1 可续跑）
```

## 提交规则

真实原文与拆解产物默认不进 git：`.gitignore` 忽略 `deconstruction-library/*`，仅保留本说明与 `_templates/`。`writing-techniques/`、`novel_all/`、`review-calibration/` 属本地语料，同样不提交；需要保存样例时，放脱敏或自有版权材料，并显式调整忽略规则。
