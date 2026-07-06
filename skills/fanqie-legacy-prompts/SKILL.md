---
name: fanqie-legacy-prompts
description: 保留旧版短篇小说题材提示词与故事圣经模板。用于需要复查或迁移旧百合、双男主、通用小说 prompt 时作为参考；新创作优先走 fanqie-writing-flow、fanqie-novel-template 和 novel-studio 统一规划逻辑。
---

# 番茄旧 Prompt 参考包

本 skill 只作为历史 prompt 与题材模板的归档入口，不直接替代当前工程写作流程。

## 内容

- `references/小说写作提示词模板.md`：早期通用小说提示词。
- `references/baiheprompt.md`：百合短篇方向提示词。
- `references/百合故事圣经模板.md`：百合故事圣经模板。
- `references/nvpin_shuangnanzhuprompt.md`：女频双男主方向提示词。

## 使用规则

1. 新任务先走 `fanqie-writing-flow` 或 `novel-studio --pipeline`。
2. 只从本包抽取题材偏好、人物关系、标签和交付格式经验。
3. 字数、章数、审核、服务看板和数据落盘以 `novel-studio` 当前工程规则为准。
