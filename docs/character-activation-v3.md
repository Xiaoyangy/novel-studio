# 角色执行策略 V3：配置与当前边界

`character_agents.protocol: "v2"` 定义角色数据和物理后态合同；`execution_policy: "v3"` 选择章内多周期的执行规则，两者不是同一个版本号。当前默认数据协议为 `v2`，默认执行策略仍为 `v1`；使用 V3 需要显式配置。

在实际使用的配置文件中，将 `character_agents` 设为：

```json
{
  "character_agents": {
    "protocol": "v2",
    "execution_policy": "v3",
    "max_activation_cycles": 8,
    "max_concurrency": 4
  }
}
```

V3 要求 `max_activation_cycles` 大于 1，可配置范围为 2–64；`max_concurrency` 上限为 4。周期数达到上限不代表章节已经完成，仍须通过章节就绪检查。

当前 V3 的支持范围是 `project-all` 的章内多周期执行。普通命令和交互入口尚未完全迁移，不能将此配置理解为所有入口已经统一切到 V3。已有 generation 固定其数据协议、执行策略、周期限额和来源；更改配置不会升级已有证据，同一 generation 不能混用新旧协议。旧 generation 应按冻结版本恢复，切换执行策略需要通过正式流程创建新的 generation。

初始化与生产分开运行。在源码仓库中，先只准备世界、人物、全书导航和零章初态：

```bash
./scripts/run-local.sh pipeline --new-novel --init-only --prompt-file prompt.md
```

初始化退出后，将下列路径替换为刚生成的同一书目目录，继续规划与写作：

```bash
./scripts/run-local.sh pipeline --dir data/runs/书名
```

继续时不带 `--new-novel`、`--init-only` 或 `--restart`。`--init-only` 不进入 `project-all`，初始化完成也不代表 V3 生产验收通过。目录约定见 [README 快速开始](../README.md#快速开始)。

规划中的角色投影和候选产物不属于正式记忆。只有对应真实正文通过接受链后，才按角色实际感知的结果发布正式记忆。完整生产与恢复边界见 [Project-All 按弧架构](project-all-architecture.md)。

截至 2026-09-08，V3 仍在进行真实三章小说验收，尚未宣称端到端生产成功。代码测试和无模型验证通过不能替代真实正文、审核与接受回执形成的完整闭环。
