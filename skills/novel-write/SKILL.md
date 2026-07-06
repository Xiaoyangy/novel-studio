---
name: novel-write
description: "用一句创作需求驱动 novel-studio 走可恢复 pipeline 完整创作（写作→评审→重写→导出）。触发：「帮我写本小说」「按这个设定写」「跑一次创作」，已有明确创作要求且希望无人值守端到端产出章节时使用。"
---
# novel-write：单句需求跑一次 pipeline 创作

把用户的创作要求作为 prompt 交给 `novel-studio --pipeline`，由 pipeline 编排写作、评审、
重写和导出。章节落盘到 `output/novel/chapters/*.md`。无 TTY，可在 CI / 远程 agent 中运行。

> `--headless --prompt` 只保留为兼容别名，命令层会转入 pipeline。新任务一律直接调用
> `--pipeline`，不要绕过流水线。

## 前置条件

- 已完成首次配置（见 `skills/README.md`）。headless 不支持首次引导。

## 执行

```bash
# 直接传文本
novel-studio --pipeline --prompt "写一个赛博朋克背景、主角是义体黑客的长篇开篇"

# 从文件读 prompt（长需求/含换行时推荐）
novel-studio --pipeline --prompt-file ./requirement.md

# 从 stdin 读
echo "需求文本" | novel-studio --pipeline --prompt-file -
```

## 参数

| 参数 | 说明 |
|---|---|
| `--prompt <text>` | 单句创作需求；与 `--prompt-file` 互斥 |
| `--prompt-file <path>` | 从文件读需求，`-` 表示 stdin |
| `--stages <list>` | 可选，默认 `write,review,rewrite,export` |
| `--config <path>` | 可选，指定配置文件 |

## 产物

- 章节：`output/novel/chapters/*.md`
- 写法资产：`output/novel/meta/writing_assets.json`（可编辑、复用、由弧摘要持续沉淀）
- 人工感写法参考：内置 `assets/references/human-feel-craft.md` 会随 `reference_pack.references.human_feel_craft` 注入，迁移《同桌是只假装高冷的猫》的物件回扣、主观误判、短对话和现实支架，避免正文只靠解释和模板情绪推进。
- reference-library 写作技巧总纲：内置 `assets/references/refer-writing-techniques-digest.md` 会随 `reference_pack.references.writing_techniques_digest` 注入，来自 `data/reference-library/writing-craft` 19 篇逐篇提炼，约束全书设计、大纲、人物、单章目标/阻力/代价/新增信息、事件余波和中文标点功能。
- 本书世界：`output/novel/book_world.json`（地图、地点、路线、势力图谱）
- 资源账本：`output/novel/meta/resource_ledger.json`（已入账事实与待确认提案分离）
- RAG trace：`output/novel/meta/rag/retrieval_trace.jsonl`（召回命中原因）
- 运行日志：项目目录下 `headless.log`

## 失败排查

- 报「首次启动需要先在交互终端运行一次 novel-studio 完成配置引导」→ 先在交互终端跑一次
  `novel-studio` 完成 stdin 配置，或手写 `~/.ainovel/config.json`。
