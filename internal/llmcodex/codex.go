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
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
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
	codexPromptRuneBudget          = 90_000
	codexPerMessageTextRuneBudget  = 45_000
	codexToolArgsRuneBudget        = 18_000
	codexProsePromptRuneBudget     = 48_000
	codexProsePerMessageRuneBudget = 24_000
	codexExecHardTimeout           = 15 * time.Minute
	proseCacheProtocol             = "codex-prose-cache/v2"
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
	// 正文类工具（draft_chapter 等）特判：把长篇正文塞进通用工具调用的 arguments_json
	// 会让 token 分布过于均匀（weak_lm 曲线过稳）→ segment_risk_floor 飙高、正文更"像 AI"
	// （实测同一份计划：独立正文生成 AIGC 4.8% vs arguments_json 80%）。改用只有 prose
	// 一个字符串字段的专用 schema 重新生成正文，避免 Codex 无 schema 调用长期挂起，
	// 再替换回工具参数，兼顾 tool-calling 框架与正文自然度。
	if err := m.regenerateProseArgs(ctx, messages, &msg, reasoning); err != nil {
		return nil, err
	}
	msg.Usage = m.estimateUsage(prompt, raw)
	return &agentcore.LLMResponse{Message: msg}, nil
}

// proseToolContentField 列出"参数里含长篇正文"的工具及其正文字段名。
var proseToolContentField = map[string]string{
	"draft_chapter": "content",
}

// regenerateProseArgs 若本轮是正文类工具调用，用独立单字段 prose schema 重新生成正文，
// 替换 arguments 里的正文字段，规避结构化输出对正文统计自然度的损伤。
func (m *CodexModel) regenerateProseArgs(ctx context.Context, messages []agentcore.Message, msg *agentcore.Message, reasoning string) error {
	wordContract := inferProseWordContract(messages)
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
		prompt := buildProsePrompt(messages)
		p, cacheHit := loadCachedProse(prompt, m.model, reasoning, wordContract)
		if !cacheHit {
			prose, err := m.runCodexProse(ctx, prompt, reasoning)
			if err != nil {
				return fmt.Errorf("正文 prose schema 渲染失败，未执行占位工具调用: %w", err)
			}
			p = strings.TrimSpace(stripCodeFence(prose))
		}
		if p != "" && wordContract.configured() && !wordContract.accepts(utf8.RuneCountInString(p)) {
			firstCount := utf8.RuneCountInString(p)
			repairStart := time.Now()
			repaired, repairErr := m.runCodexProse(ctx, buildProseRepairPrompt(messages, firstCount, wordContract), reasoning)
			candidate := strings.TrimSpace(stripCodeFence(repaired))
			if repairErr == nil && candidate != "" && wordContract.distance(utf8.RuneCountInString(candidate)) < wordContract.distance(firstCount) {
				p = candidate
			}
			slog.Info("正文触发一次有界字数纠偏",
				"module", "codex", "tool", block.ToolCall.Name,
				"before_runes", firstCount, "after_runes", utf8.RuneCountInString(p),
				"min", wordContract.Min, "max", wordContract.Max,
				"repair_err", repairErr, "elapsed_ms", time.Since(repairStart).Milliseconds())
		}
		if strings.TrimSpace(p) == "" {
			return fmt.Errorf("正文 prose schema 返回空正文，未执行占位工具调用")
		}
		if wordContract.configured() && !wordContract.accepts(utf8.RuneCountInString(p)) {
			return fmt.Errorf("正文 prose schema 返回 %d 字，不满足 %d-%d 字合同，未执行占位工具调用",
				utf8.RuneCountInString(p), wordContract.Min, wordContract.Max)
		}
		if !cacheHit {
			if err := saveCachedProse(prompt, m.model, reasoning, p); err != nil {
				slog.Warn("正文缓存写入失败", "module", "codex", "err", err)
			}
		}
		args[field] = p
		if newArgs, e := json.Marshal(args); e == nil {
			block.ToolCall.Args = newArgs
		}
		slog.Info("正文已用独立 prose schema 重渲染（规避 arguments_json 的 AI 味）",
			"module", "codex", "tool", block.ToolCall.Name, "prose_runes", utf8.RuneCountInString(p), "cache_hit", cacheHit, "elapsed_ms", time.Since(start).Milliseconds())
	}
	return nil
}

type proseCacheEntry struct {
	Protocol  string `json:"protocol"`
	Model     string `json:"model"`
	Reasoning string `json:"reasoning"`
	Prose     string `json:"prose"`
}

func proseCachePath(prompt, model, reasoning string) string {
	root := strings.TrimSpace(os.Getenv("NOVEL_STUDIO_PROSE_CACHE_DIR"))
	if root == "" {
		if userCache, err := os.UserCacheDir(); err == nil {
			root = filepath.Join(userCache, "novel-studio", "prose")
		} else {
			root = filepath.Join(os.TempDir(), "novel-studio-prose")
		}
	}
	sum := sha256.Sum256([]byte(proseCacheProtocol + "\x00" + model + "\x00" + reasoning + "\x00" + prompt))
	return filepath.Join(root, fmt.Sprintf("%x.json", sum[:]))
}

func loadCachedProse(prompt, model, reasoning string, contract proseWordContract) (string, bool) {
	raw, err := os.ReadFile(proseCachePath(prompt, model, reasoning))
	if err != nil {
		return "", false
	}
	var entry proseCacheEntry
	if json.Unmarshal(raw, &entry) != nil || entry.Protocol != proseCacheProtocol || entry.Model != model || entry.Reasoning != reasoning {
		return "", false
	}
	prose := strings.TrimSpace(entry.Prose)
	if prose == "" || (contract.configured() && !contract.accepts(utf8.RuneCountInString(prose))) {
		return "", false
	}
	return prose, true
}

func saveCachedProse(prompt, model, reasoning, prose string) error {
	path := proseCachePath(prompt, model, reasoning)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	raw, err := json.Marshal(proseCacheEntry{Protocol: proseCacheProtocol, Model: model, Reasoning: reasoning, Prose: prose})
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".prose-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

// buildProsePrompt 只把正文真正需要的小型渲染包交给独立 prose 调用。
// 完整推演、旧稿、章节合同和代理执行手册都留在外层；它们若再进入
// prose 上下文，模型很容易把正文写成逐项交付的计划清单。
func buildProsePrompt(messages []agentcore.Message) string {
	var dialog strings.Builder
	for _, msg := range messages {
		role := msg.GetRole()
		if role != agentcore.RoleUser && role != agentcore.RoleTool {
			continue
		}
		if text := strings.TrimSpace(compactProseMessageText(msg, msg.TextContent())); text != "" {
			fmt.Fprintf(&dialog, "[%s]\n%s\n\n", string(role), text)
		}
	}
	suffix := `## 现在写正文
你面对的是连载读者，不是计划审核人。只使用上下文中的 render_packet、人物关系、前章尾声和经净化的外审证据；不要猜测或还原已隐藏的完整章纲、世界推演、旧稿和流程清单。

先找准主角这一章最想要什么、最担心什么，让这个念头贯穿全章。通常只写 2-4 个页面场景；mandatory_beats 是章末必须成立的结果，可以几项合在同一场里，不得一项建一场、一人说一句。无戏的办事过程一句带过，转场可以直接跳时间或地点，不要专写一段因果桥。

群体决定留在模拟层。正文最多完整写一个真正改变主角选择的摊主或申请者，其余只写总结果；禁止用“第一个、另一家、旁边那家、最后一家”给多人逐个分配动机，也不要让每个人各提一个条件。现实项目可以跨半天，但全章最多保留两个具体钟点，中间用午饭、日光、库存或手头工作的变化自然跳过，禁止按八点、九点、十点连续报站。成果兑现只跟一组普通顾客完成看价、付款、拿走东西，其他摊位留在背景里。

自然网文允许平实叙述、普通连接句和一个人连续说完一段话。对白只顺着人物当时最急的事说，可以直白、完整、简短，也可以没有动作标签。不要为了“像人”强加抢话、误会、迟疑、乱码、无用微动作或随机生活事故。多人同场只留真正有话要说的人，完全可以由一人主说、其他人做事。

写每句对白前先确认说话人。称谓、人称和信息必须对得上：人物没有明确表演目的时，不会拿自己的姓名或职务当第三人称说自己。普通词组要能顺口读出来，不造“接骨碟”一类错词，不用看似精确却没人这么搭配的句子。问答若不相接、动作找不到对象、上一句身份与下一句称呼冲突，直接重写，不靠后文解释。

render_packet.visible_characters 是本章唯一可在现场行动、发言或发消息的实名角色。excluded_named_characters 即使出现在角色资料里也不得进入正文；无名摊主继续写“摊主”或职业称呼，绝不能借用亲戚、朋友或离屏角色的名字补位，也不能给已命名人物临时换职业。

人物本章第一次行使职务权力、叫停别人或改变现场时，先给读者一个最省字的身份锚点，例如工作证、熟人称呼或主角认出她；不能因为计划知道她是谁，就让正文里的读者也凭空知道。

叙述和县城普通人对白都用日常话。禁止把计划词泄漏进正文，例如“真实交付、参与范围、统一条件、承载上限、阶段结算、可核查、口径、边界”。不要说“付款与交付形成闭环”，直接让顾客付钱后拎着吃的离开；不要说“按统一条件执行”，可以说“今晚就这五家，明天再排”。让读者看见结果，不让人物解释项目。

重要刺激要留在主视角身上一会儿，并改变他紧接着的选择、说法或对某人的判断。男女主同场时，事业与关系可以是同一条线，但必须服从当前熟悉程度：刚重逢或刚合作，只需让她的一次帮忙、提醒或留步改变他的判断；不要为了“发糖”临时递湿巾、碰手、说机巧对句或硬凑暧昧。真正熟悉后，私下对话才可以自然慢下来。

不要在一段关系对白后补“这句话很重”“话不甜，却让他踏实”“她没问，所以更……”之类判词。若感情真的往前走了，让下一句、下一次站位或主角随后的选择自然证明；没有可见后果就不要替读者宣布。

系统的完整话放在一对【】内，独占一个自然段。它要像支持主角的熟人，接住主角刚才具体的误判或问题，一次只说一件事；一条提示最多两句，不能同时解释拒绝理由、规则和替代方案。直接指出眼前的人或东西，不把忙中疏漏说成主角在“钻空子”；禁止“钱没跑”“陪你换条路”“这笔不行”这类不带具体对象的客服话，也不改成纯数据面板。结算可以报金额或奖励，再带一句短回应，但读者刚看见的因果不要由系统重讲一遍。关系戏里不写“这个好比所有解释都重”“话不甜却让他踏实”这类替读者标注情感重量的旁白。

若 draft_external_ai_review 为 blocking，只吸收其经项目净化后的可读性证据；不沿用旧稿段落顺序做同义改写，不为追分制造缺口。世界模拟中 visible_to_pov=false 的实名角色或具名身份不得出场、发言或发消息；群体结果可写纸上多了名字，不得临时造一个“申请加入的摊主”开口补说明。

正常小说排版：首行必须写成“第N章 标题”，N 使用 render_packet.chapter，标题必须逐字使用 render_packet.title；段落间空一行。只输出完整正文，不要 JSON、Markdown、解释、自检报告或运行环境诊断。外层会负责落盘。`
	if contract := inferProseWordContract(messages); contract.configured() {
		suffix += contract.prompt()
	}
	return assembleBudgetedPromptWithLimit("", dialog.String(), suffix, codexProsePromptRuneBudget)
}

var proseContextKeys = []string{
	"chapter", "title", "premise", "render_packet", "draft_external_ai_review",
	"draft_external_ai_review_policy", "user_rules", "chapter_participants", "relationship_state",
	"character_snapshots", "previous_tail", "rag_recall", "rewrite_brief",
}

func compactProseMessageText(msg agentcore.Message, text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	if string(msg.GetRole()) != string(agentcore.RoleTool) {
		return compactCodexText(text, codexProsePerMessageRuneBudget)
	}
	var payload map[string]any
	if json.Unmarshal([]byte(text), &payload) != nil {
		return compactCodexText(text, codexProsePerMessageRuneBudget)
	}
	selected := make(map[string]any)
	copyProseContextKeys(selected, payload)
	for _, section := range []string{"working_memory", "episodic_memory", "reference_pack", "selected_memory"} {
		rawSection, ok := payload[section].(map[string]any)
		if !ok {
			continue
		}
		kept := make(map[string]any)
		copyProseContextKeys(kept, rawSection)
		if len(kept) > 0 {
			selected[section] = kept
		}
	}
	if len(selected) == 0 {
		return ""
	}
	raw, err := json.Marshal(selected)
	if err != nil {
		return compactCodexText(text, codexProsePerMessageRuneBudget)
	}
	return compactCodexText(string(raw), codexProsePerMessageRuneBudget)
}

func copyProseContextKeys(dst, src map[string]any) {
	for _, key := range proseContextKeys {
		value, ok := src[key]
		if !ok {
			continue
		}
		if key == "draft_external_ai_review" {
			review, keep := proseBlockingReview(value)
			if !keep {
				continue
			}
			value = review
		} else if key == "rewrite_brief" {
			brief, keep := proseRewriteBrief(value)
			if !keep {
				continue
			}
			value = brief
		}
		dst[key] = value
	}
}

func proseRewriteBrief(value any) (map[string]any, bool) {
	brief, ok := value.(map[string]any)
	if !ok {
		return nil, false
	}
	lean := make(map[string]any)
	for _, key := range []string{
		"reason", "review_summary", "contract_misses", "human_acceptance_supplements",
		"human_acceptance_policy", "render_policy", "ai_voice_rules",
	} {
		if item, exists := brief[key]; exists {
			lean[key] = item
		}
	}
	if rawIssues, exists := brief["issues"].([]any); exists {
		issues := make([]map[string]any, 0, min(3, len(rawIssues)))
		for _, raw := range rawIssues {
			if len(issues) >= 3 {
				break
			}
			issue, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			problem, _ := issue["problem"].(string)
			if strings.TrimSpace(problem) == "" {
				continue
			}
			issues = append(issues, map[string]any{
				"type":     issue["type"],
				"severity": issue["severity"],
				"problem":  problem,
			})
		}
		if len(issues) > 0 {
			lean["issues"] = issues
		}
	}
	return lean, len(lean) > 0
}

func proseBlockingReview(value any) (map[string]any, bool) {
	review, ok := value.(map[string]any)
	if !ok {
		return nil, false
	}
	blocking, _ := review["blocking"].(bool)
	if !blocking {
		return nil, false
	}
	lean := map[string]any{
		"blocking":   true,
		"use_policy": "只修正下列旧稿失败证据；修改建议中的示例场景、示例动作和示例台词不是正文指令，不得照搬或换皮复现。",
	}
	for _, key := range []string{"summary", "reasons", "evidence"} {
		if item, exists := review[key]; exists {
			lean[key] = item
		}
	}
	return lean, true
}

type proseWordContract struct {
	Min int
	Max int
}

var (
	chapterWordsObjectPattern = regexp.MustCompile(`(?s)"chapter_words"\s*:\s*\{([^{}]{0,512})\}`)
	chapterWordsMinPattern    = regexp.MustCompile(`"min"\s*:\s*([0-9]{1,7})`)
	chapterWordsMaxPattern    = regexp.MustCompile(`"max"\s*:\s*([0-9]{1,7})`)
	chapterWordsRangePattern  = regexp.MustCompile(`(?i)(?:chapter_words|章节字数)[^0-9]{0,160}([0-9]{2,7})\s*[-—~至到]\s*([0-9]{2,7})`)
)

func inferProseWordContract(messages []agentcore.Message) proseWordContract {
	var result proseWordContract
	for _, message := range messages {
		text := message.TextContent()
		for _, match := range chapterWordsObjectPattern.FindAllStringSubmatch(text, -1) {
			if len(match) < 2 {
				continue
			}
			minMatch := chapterWordsMinPattern.FindStringSubmatch(match[1])
			maxMatch := chapterWordsMaxPattern.FindStringSubmatch(match[1])
			if len(minMatch) < 2 || len(maxMatch) < 2 {
				continue
			}
			minWords, _ := strconv.Atoi(minMatch[1])
			maxWords, _ := strconv.Atoi(maxMatch[1])
			if minWords > 0 && maxWords >= minWords {
				result = proseWordContract{Min: minWords, Max: maxWords}
			}
		}
		if result.configured() {
			continue
		}
		if match := chapterWordsRangePattern.FindStringSubmatch(text); len(match) >= 3 {
			minWords, _ := strconv.Atoi(match[1])
			maxWords, _ := strconv.Atoi(match[2])
			if minWords > 0 && maxWords >= minWords {
				result = proseWordContract{Min: minWords, Max: maxWords}
			}
		}
	}
	return result
}

func (c proseWordContract) configured() bool {
	return c.Min > 0 && c.Max >= c.Min
}

func (c proseWordContract) accepts(count int) bool {
	return !c.configured() || (count >= c.Min && count <= c.Max)
}

func (c proseWordContract) distance(count int) int {
	if count < c.Min {
		return c.Min - count
	}
	if count > c.Max {
		return count - c.Max
	}
	return 0
}

func (c proseWordContract) prompt() string {
	span := c.Max - c.Min
	targetMin := c.Min + span/3
	targetMax := c.Max - span/5
	if targetMax < targetMin {
		targetMin, targetMax = c.Min, c.Max
	}
	return fmt.Sprintf("\n【本章字数硬合同】工具按完整输出（含标题）统计，必须在 %d-%d 字内；建议主动写到 %d-%d 字留出误差。超出或不足都会在覆盖终稿前被拒绝。输出前自行压缩重复解释、流程复述和同义环境描写，但不得删除 required_beats、保留事实、因果转折或章末钩子。", c.Min, c.Max, targetMin, targetMax)
}

func buildProseRepairPrompt(messages []agentcore.Message, previousCount int, contract proseWordContract) string {
	base := buildProsePrompt(messages)
	repair := fmt.Sprintf("\n\n【上一候选已被字数门禁拒绝】上一版按工具统计为 %d 字，不得原样输出，也不要解释。重新从同一计划渲染一次，严格落入 %d-%d 字，优先靠近区间中段；这是唯一一次自动纠偏。", previousCount, contract.Min, contract.Max)
	return compactCodexText(base, codexPromptRuneBudget-utf8.RuneCountInString(repair)) + repair
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
		"不要执行任何 shell 命令、不要读写文件、不要产出多余解释。`--sandbox read-only` 只限制你直接操作文件；" +
		"外层运行时会真正执行下方工具，所以不得把工作区、Qdrant、RAG 或工具挂载误报为阻塞。\n\n")
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
	return assembleBudgetedPromptWithLimit(prefix, dialog, suffix, codexPromptRuneBudget)
}

func assembleBudgetedPromptWithLimit(prefix, dialog, suffix string, limit int) string {
	before := utf8.RuneCountInString(prefix) + utf8.RuneCountInString(dialog) + utf8.RuneCountInString(suffix)
	budget := limit - utf8.RuneCountInString(prefix) - utf8.RuneCountInString(suffix)
	if budget < 12_000 {
		budget = 12_000
	}
	dialog = compactCodexText(dialog, budget)
	after := utf8.RuneCountInString(prefix) + utf8.RuneCountInString(dialog) + utf8.RuneCountInString(suffix)
	if before > after {
		slog.Info("codex prompt 已压缩",
			"module", "codex", "runes_before", before, "runes_after", after, "budget", limit)
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

func buildProseResponseSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"prose": map[string]any{
				"type":        "string",
				"description": "完整小说正文，首行为章名，正文自然分段；不得包含解释或运行状态",
			},
		},
		"required": []string{"prose"},
	}
}

func (m *CodexModel) runCodexProse(ctx context.Context, prompt, reasoning string) (string, error) {
	raw, err := m.runCodexIsolated(ctx, prompt, buildProseResponseSchema(), reasoning)
	if err != nil {
		return "", err
	}
	var envelope struct {
		Prose string `json:"prose"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(stripCodeFence(raw))), &envelope); err != nil {
		return "", fmt.Errorf("解析 Codex prose schema 输出失败: %w", err)
	}
	if strings.TrimSpace(envelope.Prose) == "" {
		return "", fmt.Errorf("Codex prose schema 返回空正文")
	}
	return envelope.Prose, nil
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
	return m.runCodexExec(ctx, prompt, schema, reasoning, false)
}

// runCodexIsolated is for pure prose completion. It keeps the user's auth and
// model configuration, but removes project rules, plugins and multi-agent
// fan-out, and runs in an empty temporary directory. The renderer already has
// all allowed story context in its prompt; shell or collaboration work inside
// this nested call only adds latency and can make it miss the hard timeout.
func (m *CodexModel) runCodexIsolated(ctx context.Context, prompt string, schema map[string]any, reasoning string) (string, error) {
	return m.runCodexExec(ctx, prompt, schema, reasoning, true)
}

func (m *CodexModel) runCodexExec(ctx context.Context, prompt string, schema map[string]any, reasoning string, isolated bool) (string, error) {
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
	if isolated {
		args = append(args,
			"--ephemeral",
			"--ignore-rules",
			"--disable", "multi_agent",
			"--disable", "plugins",
			"--disable", "apps",
			"--disable", "browser_use",
			"--disable", "computer_use",
			"-C", tmp,
		)
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
	runCtx, cancel := boundedCodexExecContext(ctx)
	defer cancel()
	cmd := exec.CommandContext(runCtx, m.binary, args...)
	// 超长章节上下文不能放在 argv 中，使用 stdin 避免触发系统参数长度限制。
	cmd.Stdin = strings.NewReader(prompt)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			return "", fmt.Errorf("codex exec 超过硬时限 %s: %w", codexExecHardTimeout, runCtx.Err())
		}
		return "", fmt.Errorf("codex exec 失败: %w; stderr: %s", err, tailStr(stderr.String(), 800))
	}
	data, err := os.ReadFile(outPath)
	if err != nil {
		return "", fmt.Errorf("读取 codex 输出失败: %w", err)
	}
	return string(data), nil
}

func boundedCodexExecContext(parent context.Context) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	if deadline, ok := parent.Deadline(); ok && time.Until(deadline) <= codexExecHardTimeout {
		return context.WithCancel(parent)
	}
	return context.WithTimeout(parent, codexExecHardTimeout)
}

func tailStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
