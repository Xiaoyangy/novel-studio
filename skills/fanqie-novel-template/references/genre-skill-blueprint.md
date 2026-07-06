# 新题材写作 Skill 蓝图

本蓝图用于把 `deal-paper-summry` 的聚合结果转成新的番茄写作工具。新 skill 应该是“通用模板的题材扩展”，不要复制通用模板的大段流程。

## 输出位置

默认产出：

```text
skills/<题材>-writing-guide/
└── SKILL.md
```

如需兼容旧 prompt 流程，可额外生成 `skills/<题材>prompt.md`，但新规范优先使用 skill 目录。

## Frontmatter

```yaml
---
name: <题材>-writing-guide
description: <题材>番茄短篇写作工具。用于写、续写、起设定、做细纲、生成正文、审稿和定稿 1.5-2 万字符、10 章以内、终版 AI 味 5% 以下的<题材>短篇。触发场景包括：<关键词 1>、<关键词 2>、<关键词 3>。
---
```

`description` 必须写触发场景，不要只写“某题材指南”。

## 推荐正文结构

```markdown
# <题材>番茄短篇写作工具

本工具继承 `skills/fanqie-novel-template/SKILL.md`，只补<题材>专精方法论。

## 必读材料
1. `skills/fanqie-novel-template/SKILL.md`
2. `skills/fanqie-novel-template/references/fanqie-short-workflow.md`
3. `data/generated-output/<题材>_summaries/` 下的拆书摘要
4. `skills/review/SKILL.md`

## 素材来源
- 《样本 A》：一句话可复用价值
- 《样本 B》：一句话可复用价值

## 赛道选择
<从样本归纳出的 4-8 个细分赛道，每本短篇最多选 1 主 1 副>

## 人设与关系动力
<主角模板、配角功能位、关系推进轨道>

## 情感 / 爽点 / 虐点 / 磕点或燃点引擎
<该题材最核心的留存机制>

## 开篇与章末钩子
<黄金前三行打法、第一章必须完成的事、章末钩子类型>

## 伏笔与意象
<核心意象数量、信物、旧账、证据链、回收节奏>

## 标题、金句、简介
<题材专用标题公式、金句公式、简介公式、标签建议>

## 合规和雷区
<平台敏感表达、题材误区、短篇铺不开的内容>

## AI 味 5% 交付闸门
<继承 fanqie-novel-template；列出本题材最容易触发 AI 味的 5-8 个问题和对应改写动作>

## 一页 SOP
<从开书到终版交付的 8-12 步>
```

## 聚合要求

- 每条方法论尽量挂样本出处，例如“见《X》开篇同框装置”。
- 优先提炼可执行公式，不写空泛审美判断。
- 通用字数、三件套、审核流水线只引用 `fanqie-novel-template`，不要全文复制。
- 新题材 skill 必须继承 AI 味 5% 交付闸门；如果题材有高套路风险，要补充本题材专属降 AI 味动作。
- 如果样本来自长篇，要明确短篇化压缩策略：删支线、减反派、前置设定、压缩误会。
- 产出后运行 skill 校验脚本。
