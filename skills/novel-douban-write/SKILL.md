---
name: novel-douban-write
description: "用 novel-studio 生成豆瓣阅读原创长篇小说的专项入口。适用于 15W-30W 字原创长篇，默认把豆瓣写作契约写入 prompt，并串联 review/rewrite/export；触发：「豆瓣长篇」「按豆瓣要求写」「生成豆瓣阅读原创小说」。"
---
# novel-douban-write：豆瓣原创长篇专项入口

用 `novel-studio` 生成一部面向豆瓣阅读的 15W-30W 字原创长篇。该入口是命令层工作流；具体写作标准见 embedded skill `story-douban-long-write`。

## 前置条件

- 已完成首次配置（见 `skills/README.md`）。
- 先跑 `novel-check`，确认 writer / architect / editor 模型可用。
- 创作需求必须写成 prompt 文件，避免一句话丢失豆瓣约束。

## Prompt 模板

把需求写入 `豆瓣长篇需求.md`，至少包含：

```md
目标平台：豆瓣阅读原创
目标字数：15W-30W
专项规则：使用 story-douban-long-write；写作中默认引入 story-review，前3章、每5章、完稿后都要审查。

作品方向：
- 作者追问：
- 主题：
- 主类型/副类型：
- 目标读者：
- 一句话梗概：
- 叙述人称与语气：
- 不写什么：
```

如果用户只给了题材，没有主题，先用 `novel-cocreate` 澄清“为什么写”，再进入流水线。

## 执行

```bash
novel-studio --check
novel-studio --pipeline --prompt-file ./豆瓣长篇需求.md --stages write,review,rewrite,deliver
```

不要拆成旧的 headless / review / rewrite 直达入口。命令层虽保留兼容别名，但豆瓣专项必须
直接使用 pipeline，让写作、评审、重写和导出共享同一份 `meta/pipeline.json` 证据。

## 产物要求

- 正文：`output/novel/chapters/*.md`，总字数 15W-30W。
- 评审：`reviews/` 中保留逐章 review；S1/S2 不应残留到最终导出。
- 导出：TXT/EPUB 成品。
- 另需在项目资料中保留豆瓣提交包：书名候选、标签与简介、提交自检。

## 质量闸门

不要只使用 `novel-write` 单独写完。豆瓣专项默认流程是：

`novel-check` -> `novel-cocreate` 或 prompt 文件 -> `novel-pipeline(write,review,rewrite,deliver)` -> 最终人工/agent 检查包装材料。
