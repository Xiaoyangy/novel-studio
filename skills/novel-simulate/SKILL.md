---
name: novel-simulate
description: "分析 simulate/ 参考语料合成「仿写画像」注入后续创作，或导入此前生成的画像。触发：「学这些范文的写法」「按这批文章的风格写」「导入仿写画像」，想让 Agent 借鉴结构/节奏/钩子手法时使用。"
---
# novel-simulate：仿写画像合成 / 导入

把参考文章放进 cwd 的 `simulate/` 文件夹，用 architect 模型分析语料、合成仿写画像写到
`output/novel/meta/simulation_profile.json`。画像以 compact 形式注入 `novel_context`，
Coordinator / Architect / Writer / Editor 都能读取——只借鉴结构、节奏、钩子和吸引读者手法，
不复制原文表达或专有设定。**需要调用 LLM（architect 角色）**，建议先 `novel-check` 确认可用。

## 前置条件

- 已完成首次配置（见 `skills/README.md`）。
- 合成画像：cwd 下有 `simulate/` 目录，内含 `.txt` / `.md` / `.markdown` 参考文章。

## 执行

```bash
# 分析 simulate/ 语料，合成 / 增量更新仿写画像
novel-studio --simulate

# 导入此前生成的画像 JSON（simulation_profile.v1），按语料指纹合并、重复来源跳过
novel-studio --import-sim ./profile.json

# 仅更新画像，不写 meta/diag-export.md
novel-studio --simulate --no-diag
```

## 行为

- 按 `relative_path + sha256` 跳过未变化文件；无新增/变更时提示"画像已是最新"且不调用 LLM
- 已有画像 + 新增/修改文章时，在原画像基础上继续合成
- `--import-sim` 只接受本功能生成的 `simulation_profile.v1` JSON；只导入可信来源
- 合成 / 导入成功后默认运行同一套 `diag` 收尾并写 `output/novel/meta/diag-export.md`；使用 `--no-diag` 可跳过

## 产物

- `output/novel/meta/simulation_profile.json`
- `output/novel/meta/diag-export.md`（默认生成；`--no-diag` 时跳过）
