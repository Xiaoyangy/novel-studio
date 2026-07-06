---
name: novel-review
description: "通过 novel-studio pipeline 的 review 阶段对已有项目逐章跑 Editor 评审，只产出审阅意见、不改原文。触发：「评审我的小说」「逐章审一遍」「给章节挑问题」，需要质量诊断但要保留原文时使用。"
---
# novel-review：pipeline review 阶段评审（不改原文）

对一个已有 novel-studio 项目通过 `novel-studio --pipeline --stages review` 逐章调用 Editor 评审，
输出审阅意见到 `reviews/`，不改动原文。
Editor 会读取内置 `human_feel_craft` 作为人工感正向标尺：检查现场异常、物件/痕迹回扣、主观误判、短对话/动作拍、现实支架和可复核因果链，避免只用禁用词和 AI 腔做负向判断。
Editor 也会读取内置 `writing_techniques_digest`：检查前台故事、目标/阻力/失败代价/新增信息、钩子接力、事件铺垫/过程/余波、过渡章期待铺垫和中文标点功能。

## 前置条件

- 已完成首次配置（见 `skills/README.md`）。
- 一个已有项目目录（含 `chapters/` 等产物）。**项目根 = 当前工作目录**，不能用位置参数
  传路径（会被当成多余参数报错），需先 `cd` 进项目目录。

## 执行

```bash
# 先进入项目目录，再评审全部章节
cd ./output/novel
novel-studio --pipeline --stages review

# 只评审第 3–8 章
novel-studio --pipeline --stages review --from 3 --to 8

# 调整每章 Editor 的硬时间预算（默认 90s）
novel-studio --pipeline --stages review --budget 5m
```

## 参数

| 参数 | 默认 | 说明 |
|---|---|---|
| `--stages review` | — | 只执行 pipeline 的 review 阶段 |
| `--from <n>` | `0`（自动） | 起始章号（含） |
| `--to <n>` | `0`（自动） | 结束章号（含） |
| `--budget <dur>` | `90s` | 每章 Editor 调用硬时间预算 |

## 产物

- 审阅意见：项目 `reviews/` 目录
- 不改动 `chapters/*.md` 原文
- 人工感问题会归入审美/节奏/因果建议，修法应具体到补哪个物件、哪处动作、哪条证据链或哪句对话偏差。
- refer 技巧问题会归入结构/节奏/连贯/审美建议，修法优先补章节任务和事件余波，不用句子润色掩盖结构缺口。

## 后续

拿到评审意见后想据此改写，使用 `novel-rewrite`（`--pipeline --stages rewrite`）。
