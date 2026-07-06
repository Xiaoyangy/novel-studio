---
name: novel-cocreate
description: "共创规划：与 AI 多轮对话澄清需求，逐轮累积出一份整本书的创作指令，定稿后可直接进入创作。触发：「还没想清楚写什么」「帮我把想法理清楚」「一起策划一本书」「共创」，冷启动、需求模糊时用它而非一次性 --prompt。"
---
# novel-cocreate：共创规划（多轮澄清）

与 AI 多轮对话澄清需求：AI 主动追问 + 给 1-3 条引导建议，逐轮把模糊想法收敛成一份高质量的
"整本书创作指令"草稿。定稿后落盘 `meta/cocreate-prompt.txt`，可直接进入 pipeline 创作。
等价于把一次性 `--pipeline --prompt` 的"一句话"升级成"聊出来的完整指令"。

## 前置条件

- 已完成首次配置（见 `skills/README.md`），且 LLM 可用（先 `novel-check`）。
- **需要交互终端（stdin）**：这是多轮 REPL，不适合纯管道。

## 执行

```bash
# 给个初始想法开始（也可不带，进入后再输入）
novel-studio --cocreate "我想写一个赛博朋克背景、主角是义体黑客的悬疑长篇"

# 定稿后立即进入创作
novel-studio --cocreate "..." --start
```

## 对话中的命令

| 输入 | 作用 |
|---|---|
| 直接回复 | 回答 AI 的追问，推进澄清 |
| `/draft` | 查看当前累积的创作指令草稿 |
| `/done` | 草稿成形后定稿（落盘 + 退出 / 进入创作） |
| `/quit` | 放弃本次共创 |

## 产物与衔接

- 创作指令草稿：`output/novel/meta/cocreate-prompt.txt`
- 不带 `--start` 时，可稍后用 `novel-studio --pipeline --prompt-file output/novel/meta/cocreate-prompt.txt` 创作
- 也可作为 `--pipeline` 的首阶段：`novel-studio --pipeline --stages cocreate,write,review,rewrite,export`
