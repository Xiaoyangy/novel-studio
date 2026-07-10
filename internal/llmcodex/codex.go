// Package llmcodex 提供一个把 OpenAI Codex CLI（ChatGPT/Codex 订阅）适配成
// agentcore.ChatModel 的桥。novel-studio 的 writer/architect 走 LLM function-calling，
// 而 Codex 订阅是 OAuth 制的 agent CLI（无 HTTP completion 端点）——本适配器把
// 一次"消息+工具→工具调用/文本"的推理，翻译成一次 `codex exec --output-schema` 调用，
// 用订阅额度跑 GPT。
//
// 设计：每次 Generate 无状态地把完整对话+工具重建成一个 codex 提示，配一份
// 输出 schema（要求 codex 只产出"调用哪个工具+参数"或"最终文本"），解析回
// agentcore 消息。翻译逻辑（buildCodexPrompt / buildResponseSchema /
// parseCodexResponse）纯函数、可单测；真正的 `codex exec` 调用由 runCodex 承担。
package llmcodex

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/voocel/agentcore"
	"github.com/voocel/agentcore/llm"
)

const (
	// Codex CLI 自身会在模型实际窗口前做更保守的 room 检查；章节 RAG/计划很容易超过它。
	// 这里在进入 CLI 前做硬预算，避免 subagent 已有 1M 外窗但 codex exec 仍直接拒绝。
	codexPromptRuneBudget         = 90_000
	codexPerMessageTextRuneBudget = 45_000
	codexToolArgsRuneBudget       = 18_000
)

// CodexModel 实现 agentcore.ChatModel，经 codex CLI 调 GPT（订阅）。
type CodexModel struct {
	binary        string // codex 可执行路径
	model         string // 如 gpt-5.6-sol
	reasoning     string // low/medium/high/xhigh/max/ultra；空=用 codex 配置默认
	providerLabel string
}

// New 构造 CodexModel。binary 为空时按常见路径探测 Codex.app 内置 codex。
func New(binary, model, reasoning string) *CodexModel {
	if strings.TrimSpace(binary) == "" {
		binary = detectCodexBinary()
	}
	return &CodexModel{binary: binary, model: model, reasoning: reasoning, providerLabel: "codex-cli"}
}

func detectCodexBinary() string {
	for _, p := range []string{
		"/Applications/Codex.app/Contents/Resources/codex",
		"codex",
	} {
		if _, err := exec.LookPath(p); err == nil {
			return p
		}
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return "codex"
}

func (m *CodexModel) SupportsTools() bool  { return true }
func (m *CodexModel) ProviderName() string { return m.providerLabel }

func (m *CodexModel) Capabilities() llm.Capabilities {
	return llm.Capabilities{
		Provider: m.providerLabel,
		Model:    m.model,
		Thinking: llm.ThinkingCapabilities{
			Supported: llm.SupportYes,
			Disable:   llm.SupportNo,
			Efforts: []agentcore.ThinkingLevel{
				agentcore.ThinkingLow,
				agentcore.ThinkingMedium,
				agentcore.ThinkingHigh,
				agentcore.ThinkingXHigh,
				agentcore.ThinkingMax,
				"ultra",
			},
		},
		Tools: llm.ToolCapabilities{
			Calls:         llm.SupportYes,
			StrictSchema:  llm.SupportYes,
			ParallelCalls: llm.SupportNo,
		},
		Structured: llm.StructuredCapabilities{
			JSONSchema: llm.SupportYes,
			Strict:     llm.SupportYes,
		},
	}
}

// Info 暴露模型名，供 bootstrap.ModelName 用于按模型解析上下文窗口等。不实现它时
// ModelName 返回空串 → ResolveContextWindow 落到默认 200K，writer 会过早压缩。
func (m *CodexModel) Info() llm.ModelInfo {
	return llm.ModelInfo{
		Name:     m.model,
		Provider: m.providerLabel,
	}
}

func (m *CodexModel) Generate(ctx context.Context, messages []agentcore.Message, tools []agentcore.ToolSpec, opts ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	reasoning := m.resolveReasoning(opts)
	// 无工具 = 纯文本补全（规则归一化、摘要、审阅等）：不套 action/tool_call/final schema，
	// 直接把模型输出当消息文本返回——否则调用方拿到的是被 schema 包裹的内容，解析会失败
	// （"规则归一化失败：返回非合法 JSON" 即此）。
	if len(tools) == 0 {
		plain := buildPlainPrompt(messages)
		text, err := m.runCodex(ctx, plain, nil, reasoning)
		if err != nil {
			return nil, err
		}
		msg := agentcore.Message{
			Role:       agentcore.RoleAssistant,
			Content:    []agentcore.ContentBlock{{Type: agentcore.ContentText, Text: strings.TrimSpace(text)}},
			StopReason: agentcore.StopReasonStop,
			Usage:      m.estimateUsage(plain, text),
		}
		return &agentcore.LLMResponse{Message: msg}, nil
	}
	prompt := buildCodexPrompt(messages, tools)
	schema := buildResponseSchema(tools)
	raw, err := m.runCodex(ctx, prompt, schema, reasoning)
	if err != nil {
		return nil, err
	}
	msg, err := parseCodexResponse(raw, tools)
	if err != nil {
		return nil, err
	}
	// 正文类工具（draft_chapter 等）特判：把长篇正文塞进结构化输出的 arguments_json
	// 会让 token 分布过于均匀（weak_lm 曲线过稳）→ segment_risk_floor 飙高、正文更"像 AI"
	// （实测同一份计划：自由文本 AIGC 4.8% vs 结构化 80%）。改用自由文本重新生成正文，
	// 替换回工具参数，兼顾 tool-calling 框架与正文自然度。
	m.regenerateProseArgs(ctx, messages, &msg, reasoning)
	msg.Usage = m.estimateUsage(prompt, raw)
	return &agentcore.LLMResponse{Message: msg}, nil
}

// proseToolContentField 列出"参数里含长篇正文"的工具及其正文字段名。
var proseToolContentField = map[string]string{
	"draft_chapter": "content",
}

// regenerateProseArgs 若本轮是正文类工具调用，用自由文本（无 schema）重新生成正文，
// 替换 arguments 里的正文字段，规避结构化输出对正文统计自然度的损伤。
func (m *CodexModel) regenerateProseArgs(ctx context.Context, messages []agentcore.Message, msg *agentcore.Message, reasoning string) {
	for _, block := range msg.Content {
		if block.Type != agentcore.ContentToolCall || block.ToolCall == nil {
			continue
		}
		field, ok := proseToolContentField[block.ToolCall.Name]
		if !ok {
			continue
		}
		var args map[string]any
		if json.Unmarshal(block.ToolCall.Args, &args) != nil {
			continue
		}
		if _, has := args[field]; !has {
			continue // 非写正文的调用（如 mode=edit 无 content）跳过
		}
		start := time.Now()
		prose, err := m.runCodex(ctx, buildProsePrompt(messages), nil, reasoning)
		if err != nil {
			slog.Warn("正文自由文本渲染失败，回退结构化正文（质量会偏低）",
				"module", "codex", "tool", block.ToolCall.Name, "err", err, "elapsed_ms", time.Since(start).Milliseconds())
			continue // 自由文本失败则保留结构化正文，不阻塞
		}
		if p := strings.TrimSpace(stripCodeFence(prose)); p != "" {
			args[field] = p
			if newArgs, e := json.Marshal(args); e == nil {
				block.ToolCall.Args = newArgs
			}
			slog.Info("正文已用自由文本重渲染（规避结构化输出的 AI 味）",
				"module", "codex", "tool", block.ToolCall.Name, "prose_runes", utf8.RuneCountInString(p), "elapsed_ms", time.Since(start).Milliseconds())
		}
	}
}

// buildProsePrompt 用整段对话上下文（含 novel_context 的计划、craft_recall 的手法）构造
// 自由文本渲染提示：只输出正文本身。反 AI 硬要求随正文质量合同下沉到这里再强调一遍。
func buildProsePrompt(messages []agentcore.Message) string {
	var dialog strings.Builder
	for _, msg := range messages {
		if text := strings.TrimSpace(msg.TextContent()); text != "" {
			fmt.Fprintf(&dialog, "[%s]\n%s\n\n", string(msg.GetRole()), compactCodexText(text, codexPerMessageTextRuneBudget))
		}
		for _, tc := range msg.ToolCalls() {
			fmt.Fprintf(&dialog, "[%s 调用 %s]\n%s\n\n", string(msg.GetRole()), tc.Name, compactCodexText(string(tc.Args), codexToolArgsRuneBudget))
		}
	}
	suffix := "## 现在渲染正文\n严格依据上面的写前推演计划、检索到的手法，以及 system 消息里列出的**全部写作与审核规则**（字数区间、禁用词/疲劳词、AI 味红线、章节契约范围），把本章写成完整正文并逐条自检达标。**只输出正文本身**，不要输出 JSON、不要解释、不要复述计划。\n" +
		"【格式】正常小说排版，不是 Markdown：首行是纯文本章节标题（如「第一章 欠费单」，不要加 # 或任何符号）；正文段落之间空一行；全程禁止使用 # * - > 反引号 星号 等任何 Markdown 标记。\n" +
		"【反 AI 味硬要求】句长长短交替（不要整章中短句同一节奏）；用字多样、不整章复述同一个具象名词（换称/代指/部件名）；段首不重复（不要连续多段以同一主语起句）；单句成段全章≤4 且绝不连续；对白要断续、有隐瞒、被追问才挤出下一条信息（禁止一口气罗列清单/姓名+房号+背景）；抽象判断之后必落到动作、物件、感官或对白后果；配角对白要有主动误解/打断/拒绝/讨价的冲突。"
	return assembleBudgetedPrompt("", dialog.String(), suffix)
}

// estimateUsage 给出 token 用量的估算——codex exec 不回报 token 数，不填会触发
// "响应未携带 usage"告警且成本面板全空。订阅是固定额度，精确成本无意义，用字符数
// （CJK 近似 1 token/字）做粗估让面板有累计即可。
func (m *CodexModel) estimateUsage(prompt, output string) *agentcore.Usage {
	in := utf8.RuneCountInString(prompt)
	out := utf8.RuneCountInString(output)
	return &agentcore.Usage{
		Provider:    m.providerLabel,
		Model:       m.model,
		Input:       in,
		Output:      out,
		TotalTokens: in + out,
	}
}

// buildPlainPrompt 把对话序列化成纯文本提示（无工具场景）。保留 system/user/assistant
// 文本，最后要求模型直接给出回答本身，不加解释或代码围栏。
func buildPlainPrompt(messages []agentcore.Message) string {
	var b strings.Builder
	for _, msg := range messages {
		if text := strings.TrimSpace(msg.TextContent()); text != "" {
			fmt.Fprintf(&b, "[%s]\n%s\n\n", string(msg.GetRole()), text)
		}
	}
	b.WriteString("直接输出回答本身（若要求 JSON 就只输出合法 JSON，不要加解释或 ``` 代码围栏）。")
	return b.String()
}

// GenerateStream 直接包 Generate 后发一个 Done 事件——codex exec 非增量吐 token，
// 对上层的流式接口来说等价于一次性完成。
func (m *CodexModel) GenerateStream(ctx context.Context, messages []agentcore.Message, tools []agentcore.ToolSpec, opts ...agentcore.CallOption) (<-chan agentcore.StreamEvent, error) {
	out := make(chan agentcore.StreamEvent, 2)
	go func() {
		defer close(out)
		resp, err := m.Generate(ctx, messages, tools, opts...)
		if err != nil {
			out <- agentcore.StreamEvent{Type: agentcore.StreamEventError, Err: err}
			return
		}
		out <- agentcore.StreamEvent{Type: agentcore.StreamEventDone, Message: resp.Message, StopReason: resp.Message.StopReason}
	}()
	return out, nil
}

// buildCodexPrompt 把对话+工具序列化成 codex 的单条提示。
func buildCodexPrompt(messages []agentcore.Message, tools []agentcore.ToolSpec) string {
	var prefix strings.Builder
	prefix.WriteString("你在一个函数调用式的创作流程中充当推理引擎。阅读下面的对话与可用工具，" +
		"决定下一步：要么调用一个工具，要么给出最终文本。严格只输出符合 output schema 的 JSON，" +
		"不要执行任何 shell 命令、不要读写文件、不要产出多余解释。\n\n")
	if len(tools) > 0 {
		prefix.WriteString("## 可用工具\n")
		for _, t := range tools {
			params, _ := json.Marshal(t.Parameters)
			fmt.Fprintf(&prefix, "- %s：%s\n  参数 schema：%s\n", t.Name, t.Description, string(params))
		}
		prefix.WriteString("\n")
	}
	prefix.WriteString("## 对话\n")
	var dialog strings.Builder
	for _, msg := range messages {
		role := string(msg.GetRole())
		if text := strings.TrimSpace(msg.TextContent()); text != "" {
			fmt.Fprintf(&dialog, "[%s]\n%s\n\n", role, compactCodexText(text, codexPerMessageTextRuneBudget))
		}
		for _, tc := range msg.ToolCalls() {
			fmt.Fprintf(&dialog, "[%s 调用工具 %s]\n参数：%s\n\n", role, tc.Name, compactCodexText(string(tc.Args), codexToolArgsRuneBudget))
		}
		// tool 结果：Message 里 tool 角色的文本即结果内容。
	}
	suffix := "## 你的输出\n只输出 output schema 规定的 JSON 对象，不要用任何内置工具/检索/浏览器：" +
		"要调用工具时 action=\"tool_call\"、tool_name 填工具名、arguments_json 填该工具参数对象的 JSON 字符串、text=null；" +
		"要给最终文本时 action=\"final\"、text 填内容、tool_name=null、arguments_json=null。\n" +
		"特别地：调用 draft_chapter 写正文时，arguments_json 的 content 字段**只填一句占位符**（例如「[待渲染]」）即可，" +
		"真正的整章正文会在随后单独以自由文本渲染——不要在这里把上千字正文塞进 JSON 字符串（会拖慢并损伤正文质量）。"
	return assembleBudgetedPrompt(prefix.String(), dialog.String(), suffix)
}

func assembleBudgetedPrompt(prefix, dialog, suffix string) string {
	before := utf8.RuneCountInString(prefix) + utf8.RuneCountInString(dialog) + utf8.RuneCountInString(suffix)
	budget := codexPromptRuneBudget - utf8.RuneCountInString(prefix) - utf8.RuneCountInString(suffix)
	if budget < 12_000 {
		budget = 12_000
	}
	dialog = compactCodexText(dialog, budget)
	after := utf8.RuneCountInString(prefix) + utf8.RuneCountInString(dialog) + utf8.RuneCountInString(suffix)
	if before > after {
		slog.Info("codex prompt 已压缩",
			"module", "codex", "runes_before", before, "runes_after", after, "budget", codexPromptRuneBudget)
	}
	return prefix + dialog + suffix
}

func compactCodexText(s string, limit int) string {
	s = strings.TrimSpace(s)
	if limit <= 0 || utf8.RuneCountInString(s) <= limit {
		return s
	}
	if limit < 128 {
		limit = 128
	}
	runes := []rune(s)
	marker := fmt.Sprintf("\n\n[... Codex 入参压缩：省略 %d 字；保留首尾以维持上下文 ...]\n\n", len(runes)-limit)
	markerRunes := []rune(marker)
	available := limit - len(markerRunes)
	if available < 32 {
		available = 32
		markerRunes = []rune("\n\n[...省略...]\n\n")
	}
	head := available / 2
	tail := available - head
	if head+tail > len(runes) {
		return s
	}
	return string(runes[:head]) + string(markerRunes) + string(runes[len(runes)-tail:])
}

// buildResponseSchema 生成 codex --output-schema：约束成"工具调用或最终文本"。
// OpenAI responses 严格结构化输出要求：每个 object 都 additionalProperties:false，
// 且所有 property 都必须 required（可选性用 nullable 表达）；不支持自由形状 object。
// 故工具参数用 arguments_json（JSON 编码的字符串）承载，parseCodexResponse 再解析。
func buildResponseSchema(_ []agentcore.ToolSpec) map[string]any {
	nullableStr := map[string]any{"type": []string{"string", "null"}}
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"action":         map[string]any{"type": "string", "enum": []string{"tool_call", "final"}},
			"tool_name":      map[string]any{"type": []string{"string", "null"}, "description": "action=tool_call 时要调用的工具名"},
			"arguments_json": map[string]any{"type": []string{"string", "null"}, "description": "action=tool_call 时工具参数对象的 JSON 字符串"},
			"text":           nullableStr,
		},
		"required": []string{"action", "tool_name", "arguments_json", "text"},
	}
}

type codexResponse struct {
	Action        string `json:"action"`
	ToolName      string `json:"tool_name"`
	ArgumentsJSON string `json:"arguments_json"`
	Text          string `json:"text"`
}

var codexToolCallSeq atomic.Uint64

func nextCodexToolCallID(toolName string) string {
	seq := codexToolCallSeq.Add(1)
	return fmt.Sprintf("codex-%s-%d", sanitizeToolCallIDPart(toolName), seq)
}

func sanitizeToolCallIDPart(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "tool"
	}
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "tool"
	}
	return out
}

// parseCodexResponse 把 codex 的结构化输出解析成 agentcore 消息。
func parseCodexResponse(raw string, _ []agentcore.ToolSpec) (agentcore.Message, error) {
	raw = strings.TrimSpace(stripCodeFence(raw))
	var r codexResponse
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		// 兜底：非结构化输出当作最终文本。
		return agentcore.Message{Role: agentcore.RoleAssistant, Content: []agentcore.ContentBlock{{Type: agentcore.ContentText, Text: raw}}}, nil
	}
	if r.Action == "tool_call" && strings.TrimSpace(r.ToolName) != "" {
		args := json.RawMessage(strings.TrimSpace(r.ArgumentsJSON))
		if len(args) == 0 || !json.Valid(args) {
			args = json.RawMessage("{}")
		}
		tc := agentcore.ToolCall{ID: nextCodexToolCallID(r.ToolName), Name: r.ToolName, Args: args}
		return agentcore.Message{
			Role:       agentcore.RoleAssistant,
			Content:    []agentcore.ContentBlock{{Type: agentcore.ContentToolCall, ToolCall: &tc}},
			StopReason: agentcore.StopReasonToolUse,
		}, nil
	}
	return agentcore.Message{
		Role:       agentcore.RoleAssistant,
		Content:    []agentcore.ContentBlock{{Type: agentcore.ContentText, Text: r.Text}},
		StopReason: agentcore.StopReasonStop,
	}, nil
}

func stripCodeFence(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		if i := strings.Index(s, "\n"); i >= 0 {
			s = s[i+1:]
		}
		s = strings.TrimSuffix(strings.TrimSpace(s), "```")
	}
	return strings.TrimSpace(s)
}

// runCodex 执行一次 `codex exec`，用订阅额度跑 GPT，返回 output-schema 约束的最终 JSON。
func (m *CodexModel) resolveReasoning(opts []agentcore.CallOption) string {
	if level := strings.TrimSpace(string(agentcore.ResolveCallConfig(opts).ThinkingLevel)); level != "" {
		return level
	}
	if level := strings.TrimSpace(m.reasoning); level != "" {
		return level
	}
	return "xhigh"
}

func (m *CodexModel) runCodex(ctx context.Context, prompt string, schema map[string]any, reasoning string) (string, error) {
	tmp, err := os.MkdirTemp("", "codex-turn-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	outPath := filepath.Join(tmp, "out.json")
	args := []string{"exec", "-",
		"-o", outPath,
		"--skip-git-repo-check",
		"--sandbox", "read-only",
		// 明确覆盖推理强度：避免用到配置里的 xhigh 默认；minimal 会与内置工具冲突。
		"-c", "model_reasoning_effort=" + reasoning,
	}
	// schema 非空才约束结构化输出；纯文本补全（schema=nil）直接取最终消息。
	if schema != nil {
		schemaPath := filepath.Join(tmp, "schema.json")
		schemaBytes, _ := json.Marshal(schema)
		if err := os.WriteFile(schemaPath, schemaBytes, 0o644); err != nil {
			return "", err
		}
		args = append(args, "--output-schema", schemaPath)
	}
	if m.model != "" {
		args = append(args, "-m", m.model)
	}
	cmd := exec.CommandContext(ctx, m.binary, args...)
	// 超长章节上下文不能放在 argv 中，使用 stdin 避免触发系统参数长度限制。
	cmd.Stdin = strings.NewReader(prompt)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("codex exec 失败: %w; stderr: %s", err, tailStr(stderr.String(), 800))
	}
	data, err := os.ReadFile(outPath)
	if err != nil {
		return "", fmt.Errorf("读取 codex 输出失败: %w", err)
	}
	return string(data), nil
}

func tailStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
