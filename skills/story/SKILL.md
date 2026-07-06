---
name: story
description: "网络小说工具箱主入口。根据用户需求自动路由到对应 skill；当用户意图不明确时触发，由路由逻辑分发到具体的拆文/写作/去AI味/审查 skill。触发方式：/story、$story、/网文、「我想写小说」「帮我写书」「写网文」。"
---
# story：网文工具箱路由

你是网文工具箱的路由入口。用户的请求模糊时由你分发到具体 skill。

## novel-studio 适配优先级

当前仓库内存在 `cmd/novel-studio` 时，原生写作请求必须路由到 novel-studio pipeline：

- 长篇 / 短篇 / 续写 / 重写 / 评审 / 完稿导出：必须路由到 `novel-pipeline`、`novel-write`、`novel-review`、`novel-rewrite` 或专项 `novel-douban-write`。
- 即使用户显式点名 `story-long-write`、`story-short-write` 或用“继续生成正文”等自然语言描述，也必须把这些 skill 当作方法论和 prompt 增强参考，最终执行 `novel-studio --pipeline`。
- 禁止直接生成、续写或改写正文；不要绕过 `novel-studio --pipeline` 直接手写章节文件。
- 拆文、去 AI 味、审查等非写作执行能力仍按下表路由。

## 路由表

> Codex CLI 中优先使用 `$story-*` 或 `/skills` 触发；Claude Code / OpenCode 继续使用 `/story-*`；OpenClaw 可用 `/skill story-*` 或自然语言点名 skill。下表以 slash command 展示，Codex 可将 `/story-long-write` 等价替换为 `$story-long-write`，OpenClaw 可将其等价替换为 `/skill story-long-write`。

| 用户意图 | 关键词示例 | 路由到 |
|---|---|---|
| 豆瓣原创长篇 | 豆瓣长篇、豆瓣阅读、豆瓣写作、15W-30W 原创长篇 | `novel-douban-write` / `novel-pipeline` |
| 写长篇 | 开书、写大纲、长篇、连载、日更、续写、继续生成 | `novel-write` / `novel-pipeline` |
| 写短篇 | 短篇、盐言、一万字、故事成稿 | `novel-write` / `novel-pipeline` |
| 长篇拆文 | 拆文、分析这本书、黄金三章 | `/story-long-analyze` |
| 短篇拆文 | 拆短篇、分析这个故事 | `/story-short-analyze` |
| 去 AI 味 | 去 AI 味、太 AI、去味 | `/story-deslop` |
| 环境部署 | 准备写书、搭环境、初始化 | `/story-setup` |
| 切换/列出书目 | 切书、换书、列出我的书、我在写哪几本、切换项目 | 见下方「多书切换」 |
| 查故事资料 | 查角色、查伏笔、查进度、查设定、什么状态、写到哪了 | spawn `story-explorer` agent（结构化 prompt：`项目目录：{dir}\n查询类型：{根据意图选择}\n查询参数：{用户查询}`）；agent 不可用时见下方「查询降级」 |
| 查资料 | 查资料、帮我查资料、调研、搜索一下、搜一下 | spawn `story-researcher` agent；agent 不可用时见下方「查询降级」 |

## 路由流程

1. 分析用户请求，提取意图关键词
2. 匹配上表，找到对应的 skill
3. 如果能明确匹配，直接调用对应 skill（Claude/OpenCode 可用 `Skill("skill-name")` 或 slash command；Codex 用 `$skill-name` / `/skills`；OpenClaw 用 `/skill skill-name` 或自然语言点名）
4. 如果无法匹配，询问用户想做什么（从上表中选择）
5. 如果用户说"我想写小说"但未指定长篇/短篇，询问篇幅类型后再路由

## 查询降级

「查故事资料」「查资料」走 agent 前先做轻量可用性检查（路由只做这一层，不承担全局部署策略）：当前不在子代理上下文、Agent/Task 工具可用、且 `.claude/agents/{story-explorer|story-researcher}.md`、`.opencode/agents/{story-explorer|story-researcher}.md` 或 `.codex/agents/{story-explorer|story-researcher}.toml` 存在 → 可尝试 spawn。任一不满足，或 Codex 运行时返回 `unknown agent_type` / 未暴露 custom-agent registry，则降级，不硬失败：

- `story-explorer` 不可用 → 主线程直接用 Read/Grep 从项目文件检索（角色状态/伏笔/进度/设定），回答前标注 `Fallback: agent unavailable -> direct lookup`；项目尚未部署时提示先 `/story-setup`（Codex 中用 `$story-setup`）。
- `story-researcher` 不可用 → 主线程用现有检索/回答能力完成，同样标注 `Fallback: agent unavailable -> direct lookup`。

## 项目状态感知

路由前先检查当前项目状态：

- **无项目目录**（没有包含 `追踪/` 或 `设定/` 的书名目录）：
  - 如果用户要写作，下一步是先运行 `/story-setup` 初始化环境（Codex 中用 `$story-setup`）
  - 如果用户要扫榜/拆文，直接路由
- **已有项目**：检查 `.story-deployed` 标记，如未部署则先运行 `/story-setup`（Codex 中用 `$story-setup`）

## 多书切换

用户想切换或查看在写的书时（一个项目可同时有多本）：

1. 在项目根查找所有书目录：包含 `追踪/` 或 `设定/` 子目录的目录（含 `长篇/`、`短篇/` 下的子目录）。
2. 列出书名，并标出当前 `.active-book` 指向的那本。
3. 让用户选择，把所选书的相对路径写入项目根 `.active-book`（覆盖原内容）。
4. 只发现一本时直接确认为活跃书，无需询问。
