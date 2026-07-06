---
name: novel-check
description: "LLM 连通性自检：对默认模型与各角色模型做一次最小真实调用，逐一报告 provider/model 是否真的可用。触发：「检查 LLM 能不能用」「测试模型连通」「创作前先确认配置」「为什么一调用就报错」，排查代理未启动 / key 失效 / base_url 写错时使用。"
---
# novel-check：LLM 连通性自检

加载配置，为默认模型与各角色模型（coordinator / architect / writer / editor）做一次最小真实
`Generate` 往返，逐一报告是否可用。**创作前先跑它**，把「配置看起来对、但一调用就崩」的问题前置。

## 前置条件

- 已完成首次配置（见 `skills/README.md`）。

## 执行

```bash
# 自检当前配置的所有模型目标（按 provider/model 去重，每个只 ping 一次）
novel-studio --check

# 调长单次超时（默认 30s）
novel-studio --check --timeout 60s

# 只验证某个备用 provider（不改配置），确认它能用作 fallback
novel-studio --check --provider minimax --model MiniMax-M3
```

## 参数

| 参数 | 默认 | 说明 |
|---|---|---|
| `--timeout <dur>` | `30s` | 单次连通性调用的超时 |
| `--provider <name>` | — | 只测指定 provider（配置里 `providers` 的 key 名），需配 `--model` |
| `--model <model>` | — | 配合 `--provider` 指定要测的模型名 |

## 输出与退出码

- 逐目标：`✓ provider/model [角色(主/兜底)] 耗时` 或 `✗ ... 错误原因`（主模型与配置的兜底都会测）
- 按角色汇总：`✓ 主可用` / `⚠ 主挂但走兜底` / `✗ 主与兜底都挂`
- 退出码：每个具名角色都至少有一条可用路径 → 0（含"降级可用"，即主挂走兜底）；
  某具名角色主与兜底全挂 → 非 0，并提示常见原因（代理未启动 / api_key 失效 / base_url 写错）

> 配了 `roles.<role>.fallbacks` 兜底时，自检会一并验证兜底是否真的可用，
> 让你在主 provider 挂掉时确认"还能不能降级创作"。

## 典型失败

- `connection refused` → 该 provider 的 `base_url` 指向的服务（如本地代理）没启动
- `401 / invalid api key` → `api_key` 失效或写错
- `模型返回空响应` → 连接通但模型名 / 代理路由有问题
