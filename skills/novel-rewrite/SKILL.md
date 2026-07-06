---
name: novel-rewrite
description: "通过 novel-studio pipeline 的 rewrite 阶段按评审反馈逐章 Writer 重写，会改动章节原文。触发：「按评审改写」「重写这几章」「根据意见润色正文」，在 novel-review 之后据反馈落地修改时使用。"
---
# novel-rewrite：pipeline rewrite 阶段重写

读取已有项目的评审反馈，逐章调用 Writer（或 coordinator 角色）重写章节正文。
**会改动 `chapters/*.md` 原文**，建议先用版本控制或备份。
重写时 Writer 会使用 `human_feel_craft`：把评审中的 AI 腔、解释感、模板情绪问题转成现场物件、主观误判、短对话/动作拍和可复核因果链，而不是只替换形容词。
Writer 同时会使用 `writing_techniques_digest`：把 `data/reference-library/writing-craft` 的逐篇提炼落到目标/阻力/代价/新增信息、钩子接力、事件铺垫/过程/余波、过渡章期待铺垫和标点功能上。结构缺口先补任务和场景, 再做句子级润色。

## 前置条件

- 已完成首次配置（见 `skills/README.md`）。
- 一个已跑过 `novel-review` 的项目（有评审反馈可依据）。**项目根 = 当前工作目录**，不能用
  位置参数传路径，需先 `cd` 进项目目录。

## 执行

```bash
# 先进入项目目录，再按反馈重写
cd ./output/novel
novel-studio --pipeline --stages rewrite

# 只重写第 3–8 章
novel-studio --pipeline --stages rewrite --from 3 --to 8

# 用 coordinator 角色而非默认 writer
novel-studio --pipeline --stages rewrite --role coordinator
```

## 参数

| 参数 | 默认 | 说明 |
|---|---|---|
| `--stages rewrite` | — | 只执行 pipeline 的 rewrite 阶段 |
| `--from <n>` | `0`（自动） | 起始章号（含） |
| `--to <n>` | `0`（自动） | 结束章号（含） |
| `--role <name>` | `writer` | 调用的模型角色：`writer` / `coordinator` |
| `--budget <dur>` | 见 `--help` | 每章 Writer 调用硬时间预算 |

## 产物

- 改写后的 `chapters/*.md`（原文被覆盖）

## 配套流程

`novel-review` → 人工/自动确认意见 → `novel-rewrite`。两者都经 pipeline 按章号范围工作，可分批迭代。
