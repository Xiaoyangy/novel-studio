---
name: novel-import
description: "把一本已有小说（Markdown/txt）用 LLM 反推出 foundation（设定/角色/大纲/罗盘），再委派 pipeline 跑 review/rewrite + diag。触发：「导入这本书」「反向解析小说」「把我的书导进来」，当用户只有正文、需要 AI 还原设定时使用。"
---
# novel-import：完整 LLM 反推导入

走完整链路：切分章节 → 反推 foundation → 逐章分析 → pipeline review/rewrite → diag。
适合「只有正文、需要 AI 还原设定」的场景。每阶段都有 stderr 进度日志。

## 前置条件

- 已完成首次配置（见 `skills/README.md`）。`--import` 不支持首次引导。
- 准备好小说文件（`.md` / `.txt`），首行能识别章节（「楔子 / 第N章 / Chapter N」）。

## 执行

```bash
# 完整链路（导入 + 评审 + diag）
novel-studio --import ./novel.md

# 只导入，跳过评审和 diag
novel-studio --import ./novel.md --no-review --no-diag

# 控制评审/改写的硬时间预算
novel-studio --import ./novel.md --review-budget 8m
```

> `--import` 必须在最前、紧跟文件路径；其余 flag 顺序任意。

## 参数

| 参数 | 默认 | 说明 |
|---|---|---|
| `--import <path>` | — | 外部小说文件路径（必填） |
| `--review-budget <dur>` | `8m` | 导入后 pipeline review/rewrite 的每章硬时间预算 |
| `--no-review` | 关 | 跳过 pipeline review/rewrite，只导入 + diag |
| `--no-diag` | 关 | 跳过最后的 diag 报告 |

## 产物

- foundation：premise / characters / outline / compass
- 章节：`output/novel/chapters/*.md`
- 诊断：`meta/diag-export.md`

## 何时改用 import-fast

代理返回 schema 漂移 JSON 导致反推失败，或你已有权威「故事圣经」不需要 AI 推断设定时，
改用 `novel-import-fast`（本地确定性落盘）。
