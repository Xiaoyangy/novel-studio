---
name: novel-pipeline
description: "把各功能串成一条可恢复的流水线：（共创→）写作→评审→重写→交付，按阶段执行、断点续跑。触发：「一条龙写完整本书」「从头跑完整个流程」「中断后接着跑」，想要无人值守端到端、且能中断恢复时使用。"
---
# novel-pipeline：可恢复流水线

按阶段顺序跑完整流程，状态存 `output/novel/meta/pipeline.json`，**已完成的阶段重跑时自动跳过**，
从断点继续。流水线只做阶段编排；每个阶段复用对应子命令逻辑，阶段内部还有更细的恢复
（write 走 checkpoint、review/rewrite 按章号），两层恢复叠加。

## 阶段

`cocreate`（可选，多轮澄清）→ `write`（创作）→ `review`（评审）→ `rewrite`（重写）→ `deliver`（交付沉淀：推进台账 + RAG 事实入库 + 交付快照）

默认序列：`write,review,rewrite,deliver`（假设已有创作指令）。

## 前置条件

- 已完成首次配置（见 `skills/README.md`），LLM 可用（先 `novel-check`）。
- 含 `cocreate` 阶段时需要交互终端。

## 执行

```bash
# 标准：给创作指令，跑写作→评审→重写→交付
novel-studio --pipeline --prompt "写一本东方玄幻长篇，主角从边陲小城起步"

# 从文件读创作指令
novel-studio --pipeline --prompt-file ./requirement.md

# 先共创澄清再一条龙（首阶段交互，之后无人值守）
novel-studio --pipeline --stages cocreate,write,review,rewrite,deliver

# 自定义阶段子集
novel-studio --pipeline --prompt "..." --stages write,deliver

# 单阶段评审 / 重写也走 pipeline，可指定章号范围
novel-studio --pipeline --stages review --from 3 --to 8 --budget 5m
novel-studio --pipeline --stages rewrite --from 3 --to 8 --role coordinator

# 中断后再次运行同一命令即从断点继续；要从头重跑用 --restart
novel-studio --pipeline --prompt "..."
novel-studio --pipeline --prompt "..." --restart
```

## 参数

| 参数 | 默认 | 说明 |
|---|---|---|
| `--prompt <text>` / `--prompt-file <path>` | — | 创作指令（`write` 阶段用；`-` 表示 stdin） |
| `--stages a,b,c` | `write,review,rewrite,deliver` | 阶段子集与顺序 |
| `--write-to <n>` | `0` | `write` 阶段写到指定章节后暂停；0 表示写到全书完结 |
| `--from <n>` / `--to <n>` | `0` | `review` / `rewrite` 阶段章号范围 |
| `--budget <dur>` | 阶段默认 | `review` / `rewrite` 阶段每章 LLM 调用预算 |
| `--role <name>` | `writer` | `rewrite` 阶段模型角色 |
| `--restart` | 关 | 清空已保存状态，从头重跑 |

## 恢复语义

- 每个阶段成功返回后还要通过证据校验，才写入 `pipeline.json` 的 `completed` 与 `evidence`；失败或证据不足的阶段不标记完成，下次重跑从它继续
- `write` 阶段：本书已完结则跳过；已有进度则恢复创作；全新项目用创作指令起新书
- 已标记完成的阶段重跑时会先复核证据；若产物被删或 checkpoint 缺失，会清掉完成标记并重跑该阶段
- 证据口径：`write` 看 `progress.phase=complete`、章节文件和 `commit_chapter` checkpoint；`review` 看 `reviews/*.md` 与 `meta/review-summary.md`；`rewrite` 看章节文件和 `.pre-rewrite.md` 备份；`deliver` 看 `meta/delivery_log.jsonl` / `meta/delivery_log.md`
- `novel-studio --diag` 会只读报告 `meta/pipeline.json` 的证据漂移；真正清标记和重跑只发生在再次执行 `--pipeline` 时
- 改了 `--stages` 列表会重置进度（保留已捕获的创作指令）
- 状态文件用临时文件 + rename 原子写入，写一半崩溃不损坏
- `--headless --prompt`、`--review-existing`、`--rewrite-existing` 只保留为兼容别名，新任务应直接调用 `--pipeline`

## 长期资产

流水线过程中会持续读写 `meta/writing_assets.json`、`book_world.json`、`meta/resource_ledger.json`
和 `meta/rag/*`。这些文件是可编辑、可复用的创作资产；不要把写法、世界观或资源状态只写在
一次性 prompt 里。
