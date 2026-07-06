# deconstruction-library — 写作方法与拆解成品语料库（统一 RAG 语料源）

本目录只放两类东西：**写作方法**（craft / benchmark 双库）与**拆解好的文档**（书目拆解成品）。长文拆解本身在工程外完成——工程不再内置长篇拆解管道，只负责**读取**放进来的成品并做 RAG 化。语料进入哪条检索通道由代码级准入策略（`internal/rag/policy.go`）决定，不靠自觉：

| 子目录 | 内容 | RAG 通道 | source_kind |
|---|---|---|---|
| `writing-techniques/` | 写作手法库（craft）：描写词库、史料常识、体系分级、创作方法论，**不携带任何对标作品的情节与设定事实** | 设计时刻 `craft_recall`（`rag.craft_library`） | `craft_technique` |
| `novel_all/` | 对标素材库（benchmark）：教程 / 大纲 / 题材 / 人设 / 词汇 / 爽点 / 拆文 / 运营 / 文笔 / 心理 / 场景 11 类归并库 | 设计时刻 `craft_recall`（`rag.benchmark_library`），**只可迁移手法 / 结构 / 节奏，禁止照搬情节、人名与专有设定** | `benchmark_reference` |
| `{书名}/` | 拆解成品文档（每本书一目录，工程外拆解后放入；短篇拆解 `/story-short-analyze` 的产物也落在这里） | **禁入**写作 RAG；经同步进写作项目 `对标/` 供写作流程读取 | — |
| `review-calibration/` | AI 味检测的人类基线语料（高质量 / 普通人类文本 + 校准报告） | 仅离线统计与一次性 prompt：slop 词表生成（`quality/audit/scripts/build_slop_lexicon.py`）以此为 A 组、人味盲测按需取段；**禁入任何索引与持久化上下文** | — |

三条铁律：

1. **设计库不进章节召回** —— `craft_technique` / `benchmark_reference` 只服务零章初始化与"新角色 / 新武器 / 新能力首次出场"的章计划时刻；检索结果立刻实例化为本书事实（dossier / props / world_codex），写作中途的一致性只信本书事实层。
2. **散源禁入** —— 归并前的散目录（`novel_sucai*`、`*.bak` 等）与 `{书名}/` 拆解成品一律不进写作 RAG，防止对标情节污染正文与重复计数。
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

## 放入拆解成品的目录约定

在工程外完成拆解后，按下面的结构放入 `deconstruction-library/{书名}/`；写作流程（`story-long-write` 日更等）按"项目 `对标/{书名}/` 视图优先、回退本库"读取：

```text
deconstruction-library/{书名}/
├── 概要.md
├── 章节/                 # 逐章摘要（*_摘要.md）
├── 角色/
├── 剧情/
│   ├── 故事线.md
│   ├── 节奏.md            # 关键信息推进 / 爽点触动节奏（v12 契约权威轴）
│   └── 情绪模块.md         # 读者需求 / 情绪引擎 / 可复现模块（v12 契约权威轴）
├── 设定/
│   ├── 世界观/            # 按主题拆分的多文件
│   └── 势力/
├── 文风.md               # 句长 / 标点 / 潜台词 / 情绪交替 + 原文范例
└── 拆文报告.md
```

`剧情/情绪模块.md`、`剧情/节奏.md` 与 `文风.md` 是写作侧的权威轴：缺失时日更流程会 fail-fast 并提示补齐成品，而不是 inline 现编。

## 提交规则

全部语料默认不进 git：`.gitignore` 忽略 `deconstruction-library/*`，仅保留本说明。需要保存样例时，放脱敏或自有版权材料，并显式调整忽略规则。
