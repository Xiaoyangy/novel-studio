---
name: novel-diag
description: "诊断当前项目的 output 产物，从流程/质量/规划/上下文四维给出可执行发现，并写出脱敏报告。触发：「诊断这本书」「为什么卡住了」「检查创作有没有问题」「生成 diag 报告贴 issue」。"
---
# novel-diag：项目产物诊断

对当前项目（cwd 的 `output` 产物）做静态分析，在终端打印本地 Findings，产出可执行的
发现和改进建议，并写出一份**已脱敏**的 `meta/diag-export.md`（移除正文，仅留行为骨架），
适合贴到 GitHub issue。

## 前置条件

- 已完成首次配置（见 `skills/README.md`）。
- 在项目目录下运行（含 `output/novel/`）。

## 执行

```bash
novel-studio --diag
```

## 诊断维度

- **流程** — 改写循环卡顿、未消费的转向指令、阶段/流程状态异常、章节跳号、流水线证据漂移
- **质量** — 评审维度持续低分、合同履约率、改写率、章节字数异常
- **规划** — 伏笔停滞、指南针过时、大纲耗尽、摘要缺失
- **上下文** — 角色消失、时间线缺口、关系数据停滞

## 产物

- 终端报告：本地可读 Findings（可能包含剧情/伏笔/流程细节，不适合直接贴 issue）
- `meta/diag-export.md`（脱敏行为骨架）

`diag` 会只读检查 `meta/pipeline.json`：已完成阶段若缺少 `evidence`、证据状态不是
`verified`、或证据指向的 artifact/checkpoint 已不存在，会在 Findings 中报告；它不会
自动清除阶段标记或重跑流水线。

> 随时对已有项目单独诊断。
