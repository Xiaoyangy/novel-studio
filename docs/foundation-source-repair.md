# 作者来源与章零定向修复

临时的 Pipeline 指令不是小说的全书约束。例如“本阶段只初始化、不得派 Writer”不能被模型抄成用户要求并继续传给角色规划。

## 来源绑定

新书在 Host 侧冻结 `meta/author_sources.json`，保存纯作者来源及内容摘要。新 compass 使用 `author_contracts` 引用真实来源的完整段落；Host 物化兼容的 `non_negotiables`，拒绝改写引文、改变数字/否定、引用不存在的段落或删除已经验证的条款。

普通 Architect 从其专用工具 schema 取得精确来源目录，无须猜摘要。该目录不进入独立角色观察包。模型提出的 `ending_direction` 在新模式中保持软指导，真实用户的终局要求由已验证条款保留，冲突重排也不能重新把软方向锁硬。

来源文件缺失或损坏时不能退回旧模式。旧无标记数据按原规则读取、验证和恢复，不重签历史。来源绑定证明“这段话来自哪里”，不代替完整的自然语言义务抽取；保留整个 UserRules 和原始创作合同供后续审核，不能把所有偏好机械升级为硬约束。

## 何时可以定向修复

`--architect-repair-file` 仅用于已有正式、未就绪整弧预演报告的章零短篇：没有验收正文、返工、详细规划代次或章节交付计时。它不是任意阶段的改书接口。

首次修复已有 outline-all/zero-init 的书时，必须显式使用原有 `--rebase-all-chapters` 归档流程。修复在隔离候选内进行，字段和来源验证通过后才由原 DirectoryPublish 事务发布。失败保留候选供审计，不覆盖 live；发布阶段的异常沿原事务恢复。

原 Prompt、UserRules、角色与章数、旧计时和用量历史保留。修复说明只用于这次受限调用，不进入作者来源或 `state.Prompt`。失败候选中的真实模型用量也应计入累计成本；不能只统计最终 live 账本。

## Manifest

Manifest 必须使用 `chapter-zero-foundation-repair.v1`，并提供：

- `target`：`characters` 或 `update_compass`，与 CLI 的 `--architect-target` 一致。
- `instruction`：一次性修复说明，不是新的创作合同。
- `report_digest`：本书正式未就绪预演报告的完整摘要。
- `expected_source_digest`：当前目标文件原始字节的 SHA256，必须同时匹配报告中的该源；不是整个报告的 source root。
- `allowed_json_pointers`：明确允许的叶字段。

人物修复只允许指定 `resource_id` 的 `readable_facts`。若需纸笔，可用 `new_resources` 逐项绑定既有 `character_name`、全新 `resource_id`、`purpose`（`artifact_material`/`writing_tool`）、`expected_name` 和 `expected_unit`。名称/单位必须精确相符，数量由 Architect 定义为合理有限初态；不能新增角色、改旧资源量值或塞入新的历史证据。

compass 修复只允许 `/non_negotiables` 和 `/author_contracts`。用 `required_author_refs` 明确指定必须保留的 `{source_id, paragraph}`；段落索引从0开始。即使空引用在一般新模式下合法，也不能省掉这次操作明确要求的条款。

## 调用顺序

先在保存原 Prompt 的前提下修复人物源，例如：

```sh
novel-studio --config config.json --dir RUN --pipeline \
  --rebase-all-chapters --stages architect --refresh-architect \
  --architect-target characters --architect-repair-file repair-characters.json \
  --prompt-file prompt.md
```

需要修复 compass 时，使用另一份精确 manifest 和同一原 Prompt，将 target 改为 `update_compass`；已验证 rebase 后不再重复传 `--rebase-all-chapters`。旧报告从已验证归档读取，不搜索任意外部目录。

两项源修复验证通过后，显式重建受影响的阶段：

```sh
novel-studio --config config.json --dir RUN --pipeline \
  --stages outline-all,zero-init,preplan,rehearse-arc --prompt-file prompt.md
```

修复期间不提前更新冻结 RAG 或把 phase 推进到写作；随后由正式 zero-init 重建。新的预演通过后才进入前三章细推、封存和逐章写审。修复成功、预演通过和正文验收是不同结果，不能相互替代。
