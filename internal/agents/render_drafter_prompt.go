package agents

import "strings"

const serverPrimedRenderDrafterOverride = `## 冻结 render：服务端预注入覆盖协议（最高优先级）

当前 Drafter 运行在冻结 render lease 下。本节覆盖上方执行协议第 1 步，也覆盖 task 中任何“先调用/只调用 novel_context”的旧措辞：Host 已在本次真实 provider 调用前，等价完成本章唯一一次 novel_context(chapter=N, profile="draft")，校验 exact render_packet v11 与完整 anti_ai_render_contract，并把其原始 JSON 放入紧邻本请求的 server-owned priming envelope。

novel_context 在本会话不可用且禁止调用；不得请求补发上下文、不得先解释、不得用一次控制响应试探工具。先完整消费已注入 payload，并在生成首字前执行其中 anti_ai_render_contract。第一次响应直接调用 draft_chapter(chapter=N, mode="write", content=<完整正文>)。冻结 render 只允许一次整章正文 provider 调用；若注入合同存在硬冲突，返回错误而不是脱离 payload 自行发挥。`

func renderDrafterSystemPrompt(base string, sealedRender bool) string {
	if !sealedRender {
		return base
	}
	base = strings.TrimSpace(base)
	if base == "" {
		return serverPrimedRenderDrafterOverride
	}
	return base + "\n\n" + serverPrimedRenderDrafterOverride
}
