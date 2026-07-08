# 鬼城产出中断根因分析（2026-07-07）

## 结论一句话
产出反复中断的根因不是 novel-studio 代码缺陷，而是**当前唯一可用的 LLM（MiniMax-M3）在两个维度触到能力天花板**：①对 ~130K 上下文的流式请求反复卡流（stream idle）；②无法可靠产出完整、结构正确的 ~50 字段 `causal_simulation` 章节计划。今晚已把工程侧所有可修的点修完，把失败模式从"归零死循环"改善到"累积到 32-51/~50 字段、卡在收口"，但最后一步受限于模型能力，非代码可解。

## 现象时间线（鬼城第 1 章）
- planner 上下文涨到 143K 附近 → MiniMax **stream idle timeout**，7/7 重试全挂 → subagent 硬失败。
- 换 160K 窗口 + 2min 超时后卡流减少，但基座上下文 ~130K（见下）仍贴着卡流区。
- planner 累积到 32→51 字段，但 `dialogue_scene_blueprints`（string 写成 struct）、`writing_norms_applied`、`external_reference_plan.collected_source`、`trend_language_plan` 反复缺失/形状错，**始终无法 finalize**。

## 两个根因

### 1. 上下文基座过大（~130K），贴着 MiniMax 卡流区
planner 的 novel_context 注入被方法论前置的大工件主导：

| 来源 | 体积 |
|---|---|
| meta/prewrite_storycraft_plan.json | 152 KB |
| meta/initial_character_dynamics.json | 136 KB |
| 15 个角色 dossier | 合计 236 KB |
| world_foundation / characters / book_world | 32 / 24 / 20 KB |

这些构成 ~130K token 的地板，任何 plan 批次都把请求推进 MiniMax 的卡流区（~140K+）。

### 2. 计划复杂度超出 MiniMax 可靠产出能力
完整 `causal_simulation` 有 ~50 个字段，多个含深层嵌套子结构（对话蓝图约 15 个必填子字段、证据回收链、情绪逻辑…）。MiniMax 能产出其中大部分，但对最复杂的几个字段反复：写错形状（数组元素写成裸对象/字符串）、漏子字段。校验（正确地）拒收，于是收不了口。

## 今晚已落地的工程缓解（全部构建+测试通过）
- context_window 1M→**160K**（compact≈136K，把请求压在卡流区下沿）；streamIdleTimeout 5min→**2min**（早重试）。
- **推演/渲染阶段拆分**（planner↔drafter 独立上下文）：drafter 起干净小上下文渲染，不背规划历史。
- **plan_structure 保留已累积 causal_simulation**（旧逻辑每次清空→卡流后归零死循环）。
- **plan_chapter partial-merge** + **plan_details causal_simulation 改可选**：planner 单发/两阶段混用、想 finalize 已攒字段时不再被迫重发整个巨型 payload（05:51 死循环根因）。
- 反射式 JSON 形状容错 `coerceJSONShape`：对象↔数组、单 struct 裸写成对象（environment_state 等）都能自愈；string→struct 仍无法自动纠正（歧义）。
- world_tick 双硬卡点 + 故事时钟锚定 + accept 后进 RAG；主角关系契约 bug 修复。

## 根治路径（二选一，均需 provider 侧动作）
1. **换能力更强的 provider 跑推演+渲染**（首选）：GPT-5.5 / Claude 对 130K 上下文 + 50 字段结构化计划游刃有余，本类失败整体消失，今晚所有代码改动继续生效。当前障碍：OpenAI key 是占位符、Anthropic 欠费、Codex/Claude 为订阅制（OAuth，非 HTTP API）——见订阅接入方案。
2. **降低计划的硬性复杂度以适配 MiniMax**：把 MiniMax 反复搞不定的少数字段（对话蓝图深层子字段、collected_source 等）从 finalize 硬阻塞降为 warning，保留其余 ~45 个字段的丰富度。属质量取舍，需用户拍板。

## 订阅接入的技术现实（供决策）
- novel-studio 经 litellm 走 **HTTP completion + function-calling**。
- Codex 订阅：`auth_mode=chatgpt`（OAuth）。`codex exec-server` 是 **ws/stdio** 协议、`codex exec` 是**带自有工具的 agent**（非 completion API）——都不能直接插进 litellm；要用需较大改造（把 writer/architect 从"LLM 调我方工具"改成"codex exec 按 output-schema 生成结构化计划、我方解析"）。
- Claude 订阅：`claude -p` 同理是 agent。
- 既有桥：cc-switch 的本地代理（:15721）把订阅桥接成 anthropic 式 HTTP，但当前未监听且此前报 base_url 配置问题。
- 落地建议：短期启用/修好 cc-switch 代理把 gpt-5.5 暴露成 HTTP 给 novel-studio 用；长期在 novel-studio 里加"codex-exec 结构化生成"provider 作为原生订阅支持。

## 补充：provider 可用性实测（决策关键）
- **OpenAI**：config 里 api_key 是占位符 `REPLACE_..._KEY`，无有效 key。
- **Anthropic**：欠费（credit too low）。
- **Codex 订阅**：`codex exec` 实测**可headless运行、订阅鉴权正常**（结构化 --output-schema 可用），但**当前触发用量上限**："try again at 9:08 PM"（今晚重置）。→ 证明订阅接入技术可行；但今晚 21:08 前无可用 capable provider。
- **MiniMax**：可用，但产不出完整计划（见根因）。
- 结论：**在 Codex 用量重置(21:08)或用户提供有效 API key 之前，无法用 capable 模型产出正文**。工程侧应把系统建完整、就绪，等 provider 恢复即可跑。
