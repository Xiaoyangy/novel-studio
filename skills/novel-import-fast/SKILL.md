---
name: novel-import-fast
description: "从已写好的章节原文 + 故事圣经直接本地落盘 foundation，跳过 LLM 反推（绕开代理 schema 漂移）。触发：「快速导入」「我有故事圣经直接导」「import 失败用本地数据继续」，当设定是已知事实而非待推断时使用。"
---
# novel-import-fast：本地确定性导入

不调 LLM 反推，直接从章节原文（Markdown/txt）+ 可选「故事圣经」本地落盘 foundation。
设计哲学：foundation 是「事实」不是「LLM 推断」，本地手写事实比远端推断更可靠。
评审 / 改写阶段若启用，会委派给 `novel-studio --pipeline` 的 review/rewrite 阶段。

## 前置条件

- 已完成首次配置（见 `skills/README.md`）。
- 章节原文文件，首行能识别章节（「楔子 / 第N章 / Chapter N」）。
- 可选故事圣经：缺省读 `./.ainovel/rules/00-故事圣经.md`，未找到则只写章节。

## 执行

```bash
# 最简：只给章节原文
novel-studio --import-fast ./chapters.md

# 指定故事圣经 + 书名
novel-studio --import-fast ./chapters.md --bible ./bible.md --name "我的书名"

# 跳过评审/diag（纯本地落盘）
novel-studio --import-fast ./chapters.md --no-review --no-diag
```

> `--import-fast` 必须在所有 flag 之前。

## 参数

| 参数 | 默认 | 说明 |
|---|---|---|
| `--import-fast <path>` | — | 章节原文文件路径（必填） |
| `--bible <path>` | `.ainovel/rules/00-故事圣经.md` | 故事圣经路径（可选） |
| `--name <text>` | 文件名 | 书名 |
| `--review-budget <dur>` | `8m` | pipeline review/rewrite 的每章硬时间预算 |
| `--no-review` | 关 | 跳过 pipeline review/rewrite |
| `--no-diag` | 关 | 跳过最后 diag |

## 产物

- foundation：premise / characters / outline / compass（确定性 IO）
- 章节：`output/novel/chapters/*.md`
- 可继续维护的长期资产：`meta/writing_assets.json`、`book_world.json`、`meta/resource_ledger.json`、`meta/rag/*`
