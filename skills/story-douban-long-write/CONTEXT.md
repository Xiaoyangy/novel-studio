# Context Recovery

该 skill 的上下文恢复入口。当前仓库内禁止直接生成、续写或改写正文；豆瓣长篇正文产出必须走 `novel-studio --pipeline` 或 `novel-douban-write`。执行时先按 context.json 读取必需文件，再根据当前任务选择条件文件。

## 必读顺序

1. 读 `SKILL.md`，确认触发场景、执行边界和交付物。
2. 读本文件，恢复该 skill 的上下文保护规则。
3. 读 `context.json`，按 `required_files` 与当前任务匹配的 `conditional_files` 补齐材料。
4. 若任务已经开始，优先读执行目录里的 `.skill-context/story-douban-long-write.md`，恢复已读文件、阶段、路径、硬约束和下一步。

## 压缩恢复规则

- 主会话只保留轻量状态：任务名、当前阶段、当前产物路径、已读资料清单、下一步。
- 长正文、拆文结果、审核证据、平台资料和脚本输出必须落盘，恢复时按路径读取。
- 每次完成阶段、写完章节、完成审核或切换 agent 前，更新 `.skill-context/story-douban-long-write.md`。
- 如果上下文压缩后缺少关键信息，不凭记忆续写；先回读 `.skill-context/story-douban-long-write.md` 和相关产物。

## 本 skill 的读取重点

- 必读补充文件：`references/douban-original-guide.md`。
- `references/` 是方法论和模板来源；不要一次性全量加载大目录，按 `context.json` 条件读取。

## 执行期恢复文件模板

```markdown
# story-douban-long-write execution context
- task: <用户任务>
- stage: <当前阶段>
- inputs: <关键输入路径>
- outputs: <关键输出路径>
- files_read: <已读 SKILL / CONTEXT / references / scripts>
- hard_constraints: <不可丢失约束>
- next_step: <恢复后第一步>
```
