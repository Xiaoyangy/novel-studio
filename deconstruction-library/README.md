# deconstruction-library

这里是 `novel-studio` 的长篇小说拆解工作区。

默认约定和内置 `story-long-analyze` skill 保持一致：

```text
deconstruction-library/{书名}/
```

每本被拆解的长篇小说独立一个目录，用来保存原文备份、黄金三章深度拆解、逐章摘要、角色档案、剧情模块、设定、文风和最终拆文报告。

本目录同时是 **AI 味检测的人类基线语料**：slop 词表生成（`quality/audit/scripts/build_slop_lexicon.py`）
与 aigc 阈值校准以此为 A 组做过表达统计，人味盲测判别按需一次性取段。仅限**离线统计与一次性
prompt 使用**，继续禁入任何本书 RAG 索引与持久化上下文。

## 新建一本长篇拆解

```bash
cp -R deconstruction-library/_templates/longform-decon-project "deconstruction-library/书名"
```

然后把原文文件放进：

```text
deconstruction-library/书名/原文/
```

如果在 Codex / Claude Code / OpenCode 中使用内置 skills，可以先导出：

```bash
novel-studio skills export --to .agents/skills
```

再触发 `$story-long-analyze` 或 `/story-long-analyze`，输出目录指定为 `deconstruction-library/书名/`，也可以直接使用默认路径。

## 目录结构

```text
deconstruction-library/{书名}/
├── 原文/
├── 概要.md
├── 快速预览.md
├── 章节/
├── 角色/
├── 剧情/
│   ├── README.md
│   ├── 故事线.md
│   ├── 节奏.md
│   └── 情绪模块.md
├── 设定/
│   ├── 世界观/
│   └── 势力/
├── 文风.md
├── 拆文报告.md
└── _progress.md
```

## 提交规则

真实原文和拆解产物默认不进 git；`.gitignore` 只保留这个说明和 `_templates/` 模板。需要保存样例时，放脱敏或自有版权材料，并显式调整忽略规则。
