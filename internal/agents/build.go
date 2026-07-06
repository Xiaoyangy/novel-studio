package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/chenhongyang/novel-studio/assets"
	"github.com/chenhongyang/novel-studio/internal/agents/ctxpack"
	"github.com/chenhongyang/novel-studio/internal/aigc"
	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/host/reminder"
	"github.com/chenhongyang/novel-studio/internal/rules"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/tools"
	"github.com/chenhongyang/novel-studio/internal/userrules"
	writerprompts "github.com/chenhongyang/novel-studio/internal/writer/prompts"
	writersampler "github.com/chenhongyang/novel-studio/internal/writer/sampler"
	"github.com/voocel/agentcore"
	corecontext "github.com/voocel/agentcore/context"
	"github.com/voocel/agentcore/llm"
	"github.com/voocel/agentcore/subagent"
)

// agentToRole 把 subagent name 归一为 ModelSet 认得的 role 名。
// architect_short / architect_long 都共用同一个 architect role 配置。
// 跟 host.agentRoleName 同义，因为 build 与 host 互不依赖故各持一份。
func agentToRole(name string) string {
	if strings.HasPrefix(name, "architect_") {
		return "architect"
	}
	return name
}

// subagentMaxRetries 给所有 SubAgentConfig 与 Coordinator 统一的 LLM retry 上限。
// 退避策略：指数退避（受 maxDelay 上限约束），优先服从 server Retry-After。
// 配合 ToolsAreIdempotent=true 让 stream-idle / 503 / 短暂网络抖动这类 retryable
// 错误能在 subagent 层就近重试，而不是把整个 subagent 抛回 coordinator 重派发。
// 项目铁律一保证写类工具走 checkpoint+digest 幂等，重试是安全的。
const subagentMaxRetries = 7

// UsageRecorder 是 BuildCoordinator 可选的用量回调；签名与 OnMessage 一致，
// 每条 agent 消息都会调一次，由 Host 层负责聚合。nil 表示不追踪。
type UsageRecorder func(agentName string, msg agentcore.AgentMessage)

// FlowBoundaryHook runs synchronously after a Coordinator tool that advances
// the durable story state succeeds. Host uses it to queue the next flow
// instruction before the Coordinator gets another LLM turn.
type FlowBoundaryHook func(toolName string)

// ApplyThinking 把某具体角色的推理强度应用到 live agent（运行时 /model 调整用）。
// coordinator → Agent.SetThinkingLevel；architect → 两个 architect_* 子代理；
// writer/editor → 对应子代理。空 level = 沿用模型/provider 默认。其它 role 名忽略。
type ApplyThinking func(role string, level agentcore.ThinkingLevel)

// ParseThinkingLevel 把配置字符串转 agentcore.ThinkingLevel。
// "" 合法（= 不覆盖/继承）；其余须是 off/low/medium/high/xhigh/max 之一，
// 否则返回 error（启动时降级当空并 warn，运行时把 error 回显给用户）。
func ParseThinkingLevel(s string) (agentcore.ThinkingLevel, error) {
	lv := agentcore.NormalizeThinkingLevel(agentcore.ThinkingLevel(s))
	switch lv {
	case "", agentcore.ThinkingOff, agentcore.ThinkingLow, agentcore.ThinkingMedium,
		agentcore.ThinkingHigh, agentcore.ThinkingXHigh, agentcore.ThinkingMax:
		return lv, nil
	default:
		return "", fmt.Errorf("无效推理强度 %q（可选：off/low/medium/high/xhigh/max）", s)
	}
}

func ResolveThinkingForModel(model agentcore.ChatModel, level agentcore.ThinkingLevel) (agentcore.ThinkingLevel, bool) {
	return llm.ThinkingPolicyFor(model).Resolve(level)
}

func AvailableThinkingForModel(model agentcore.ChatModel) []agentcore.ThinkingLevel {
	return llm.ThinkingPolicyFor(model).Available
}

// roleThinking 解析某角色生效的推理强度；非法值降级为空（不覆盖）并 warn。
func roleThinking(cfg bootstrap.Config, role string) agentcore.ThinkingLevel {
	lv, err := ParseThinkingLevel(cfg.ResolveReasoningEffort(role))
	if err != nil {
		slog.Warn("忽略无效推理强度配置", "module", "agent", "role", role, "err", err)
		return ""
	}
	return lv
}

func resolvedRoleThinking(model agentcore.ChatModel, cfg bootstrap.Config, role string) agentcore.ThinkingLevel {
	resolved, _ := ResolveThinkingForModel(model, roleThinking(cfg, role))
	return resolved
}

// BuildCoordinator 组装 Coordinator Agent 及其 SubAgent。
// 返回 Agent、AskUserTool、WriterRestorePack、Coordinator 的 ContextEngine 引用，
// 以及 ApplyThinking 闭包——Host 层 /model 切换时需要直接调 SetContextWindow +
// SetReserveTokens 联动新模型的窗口（writer/architect/editor 走 ContextManagerFactory
// 自动重建，不需要 ref；只有常驻的 coordinator 需要），并通过 ApplyThinking 联动各角色
// 推理强度。Host 层通过 Agent.Subscribe 获取事件流,不再需要 emit 回调。
func BuildCoordinator(
	cfg bootstrap.Config,
	store *store.Store,
	models *bootstrap.ModelSet,
	bundle assets.Bundle,
	recordUsage UsageRecorder,
	onFlowBoundary FlowBoundaryHook,
) (*agentcore.Agent, *tools.AskUserTool, *ctxpack.WriterRestorePack, *corecontext.ContextEngine, ApplyThinking) {
	// 共享工具
	// Task 072：real_lm 维度配置注入（未配置时 ai_gate 报告无痕）。
	aigc.SetRealLMRuntimeConfig(cfg.AIGC.RealLM.Endpoint, cfg.AIGC.RealLM.Model, cfg.AIGC.RealLM.Weight)
	contextTool := tools.NewContextTool(store, bundle.References, cfg.Style)
	commitChapter := tools.NewCommitChapterTool(store)
	saveFoundation := tools.NewSaveFoundationTool(store)
	saveReview := tools.NewSaveReviewTool(store)
	if qdrantClient, enabled, err := bootstrap.NewRAGQdrantClient(cfg, false); err != nil {
		slog.Warn("Qdrant 初始化失败，将回退本地 RAG", "module", "rag", "err", err)
	} else if enabled {
		contextTool.WithRAGVectorSearcher(qdrantClient)
		commitChapter.WithRAGVectorWriter(qdrantClient)
		saveFoundation.WithRAGVectorWriter(qdrantClient)
		saveReview.WithRAGVectorWriter(qdrantClient)
	}
	if embedder, enabled, err := bootstrap.NewRAGEmbedder(cfg); err != nil {
		slog.Warn("RAG embedding 初始化失败，将回退本地关键词召回", "module", "rag", "err", err)
	} else if enabled {
		contextTool.WithRAGEmbedder(embedder)
		commitChapter.WithRAGEmbedder(embedder)
		saveFoundation.WithRAGEmbedder(embedder)
		saveReview.WithRAGEmbedder(embedder)
	}
	// 用户规则服务：归一化各来源 → 确定性合并 → 落盘本书快照。Coordinator 的
	// save_user_rules 工具复用它做运行中更新；归一化用 Default 模型（与 Host 开书侧一致）。
	userRulesSvc := userrules.NewService(store, models.Default, rules.DefaultOptions())
	readChapter := tools.NewReadChapterTool(store)
	askUser := tools.NewAskUserTool()

	// 设计时刻的手法库检索：字段绑定确定性配方，产出立刻实例化为本书事实。
	craftRecall := tools.NewCraftRecallTool(store)
	architectTools := []agentcore.Tool{
		contextTool,
		saveFoundation,
		craftRecall,
		// 世界推演（离屏世界 tick）：Architect 在弧边界以 GM 身份裁决镜头外世界变化。
		// 短篇/不 tick 的项目不调用即无副作用。
		tools.NewSaveWorldTickTool(store),
	}
	writerTools := []agentcore.Tool{
		contextTool,
		readChapter,
		craftRecall,
		tools.NewPlanChapterTool(store),
		// 两阶段规划：plan_structure 先落核心骨架，plan_details 分批补 causal_simulation；
		// 与单发 plan_chapter 同一校验口径，长章/大 plan 用它降低单次输出压力。
		tools.NewPlanStructureTool(store),
		tools.NewPlanDetailsTool(store),
		tools.NewDraftChapterTool(store),
		tools.NewEditChapterTool(store),
		tools.NewCheckConsistencyTool(store),
		commitChapter,
	}
	editorTools := []agentcore.Tool{
		contextTool,
		readChapter,
		saveReview,
		tools.NewSaveArcSummaryTool(store),
		tools.NewSaveVolumeSummaryTool(store),
	}

	// Provider failover 只记日志,不通知宿主
	reportFailover := func(ev bootstrap.FailoverEvent) {
		slog.Warn("provider 切换",
			"module", "agent",
			"role", ev.Role,
			"reason", ev.Reason,
			"from", fmt.Sprintf("%s/%s", ev.FromProvider, ev.FromModel),
			"to", fmt.Sprintf("%s/%s", ev.ToProvider, ev.ToModel),
			"err", ev.Err,
		)
	}

	architectModel := models.ForRoleWithFailover("architect", reportFailover)
	writerModel := writersampler.New(models.ForRoleWithFailover("writer", reportFailover))
	// Task 067：三采样 pairwise 终选用 reviewer 角色（异族裁判；未配置回落 editor）。
	writerModel.Judge = models.ForRoleWithFailover("reviewer", reportFailover)
	editorModel := models.ForRoleWithFailover("editor", reportFailover)
	coordinatorModel := models.ForRoleWithFailover("coordinator", reportFailover)

	// Coordinator 的 ContextManager 在 Agent 构造时一次性生成，按启动模型解析。
	// 运行中 /model 切换到更小窗口的模型时，建议用户显式配置 context_window 兜底。
	_, coordinatorModelName, _ := models.CurrentSelection("coordinator")
	coordinatorContextWindow, coordinatorSource := cfg.ResolveContextWindow(coordinatorModelName)
	// Writer 的 ContextManager 由工厂每次调用重建，窗口随模型 swap 动态跟随（见下方工厂）。
	_, writerModelName, _ := models.CurrentSelection("writer")
	writerContextWindow, writerSource := cfg.ResolveContextWindow(writerModelName)
	bootstrap.LogContextWindowChoice("coordinator", coordinatorModelName, coordinatorContextWindow, coordinatorSource)
	bootstrap.LogContextWindowChoice("writer", writerModelName, writerContextWindow, writerSource)

	// modelLookup 写入 session 时给每条 assistant 消息附 _meta:{provider,model}，
	// 让 replay 不再依赖"当前 ModelSet"来反推历史 cost，运行中切换模型也能精确算。
	modelLookup := func(agentName string) (string, string) {
		role := agentToRole(agentName)
		provider, name, _ := models.CurrentSelection(role)
		return provider, name
	}
	baseOnMsg := store.Sessions.SubAgentLogger(modelLookup)
	onMsg := func(agentName, task string, msg agentcore.AgentMessage) {
		baseOnMsg(agentName, task, msg)
		if recordUsage != nil {
			recordUsage(agentName, msg)
		}
	}
	baseCoordinatorLog := store.Sessions.CoordinatorLogger(modelLookup)
	coordinatorOnMessage := func(msg agentcore.AgentMessage) {
		baseCoordinatorLog(msg)
		if recordUsage != nil {
			recordUsage("coordinator", msg)
		}
	}

	architectStopGuardFactory := func(_, _ string) agentcore.StopGuard {
		return reminder.NewArchitectStopGuard(store)
	}
	architectThinking, _ := ResolveThinkingForModel(architectModel, roleThinking(cfg, "architect"))
	architectShort := subagent.Config{
		Name:               "architect_short",
		Description:        "短篇规划师：为单卷、单冲突、高密度故事生成紧凑设定与扁平大纲",
		Model:              architectModel,
		SystemPrompt:       bundle.Prompts.ArchitectShort,
		Tools:              architectTools,
		MaxTurns:           cfg.ResolveMaxTurns("architect", 15),
		MaxRetries:         subagentMaxRetries,
		ThinkingLevel:      architectThinking,
		ToolsAreIdempotent: true,
		OnMessage:          onMsg,
		StopAfterToolResult: func(toolName string, result json.RawMessage) bool {
			r := decodeSaveFoundationResult(toolName, result)
			return r.Type == "outline" && r.FoundationReady
		},
		StopGuardFactory: architectStopGuardFactory,
	}
	architectLong := subagent.Config{
		Name:                "architect_long",
		Description:         "长篇规划师：为连载型、可持续升级的故事生成分层设定与卷弧大纲",
		Model:               architectModel,
		SystemPrompt:        bundle.Prompts.ArchitectLong,
		Tools:               architectTools,
		MaxTurns:            cfg.ResolveMaxTurns("architect", 20),
		MaxRetries:          subagentMaxRetries,
		ThinkingLevel:       architectThinking,
		ToolsAreIdempotent:  true,
		OnMessage:           onMsg,
		StopAfterToolResult: architectLongShouldStopAfterToolResult,
		StopGuardFactory:    architectStopGuardFactory,
	}

	writerPrompt := bundle.Prompts.Writer
	if style, ok := bundle.Styles[cfg.Style]; ok {
		writerPrompt += "\n\n" + style
	}
	writerPrompt += "\n\n" + writerprompts.AntiAIVoice()

	restore := &ctxpack.WriterRestorePack{}
	restore.Refresh(store)

	writer := subagent.Config{
		Name:         "writer",
		Description:  "创作者：自主完成一章的构思、写作、自审和提交",
		Model:        writerModel,
		SystemPrompt: writerPrompt,
		Tools:        writerTools,
		// writer 的收敛依赖 StopAfterTools(commit_chapter)+StopGuard+预算护栏，而不是回合数；
		// 30 的旧上限会把长章（plan 拆阶段+多轮自审+返修）在半途砍断，默认放到 300。
		MaxTurns:           cfg.ResolveMaxTurns("writer", 300),
		MaxRetries:         subagentMaxRetries,
		ThinkingLevel:      resolvedRoleThinking(writerModel, cfg, "writer"),
		ToolsAreIdempotent: true,
		StopAfterTools:     []string{"commit_chapter"},
		OnMessage:          onMsg,
		StopGuardFactory: func(_, _ string) agentcore.StopGuard {
			return reminder.NewWriterStopGuard(store)
		},
		ContextManagerFactory: func(model agentcore.ChatModel) agentcore.ContextManager {
			// 每次 subagent(writer) 调用都会重建，从当前 runModel 读取最新模型名。
			// /model 切换 writer 后下一章自动用新窗口。
			window, _ := cfg.ResolveContextWindow(bootstrap.ModelName(model))
			return newContextManager(contextManagerConfig{
				Model:            model,
				ContextWindow:    window,
				ReserveTokens:    bootstrap.CompactReserveTokens(window),
				KeepRecentTokens: 20000,
				Agent:            "writer",
				ToolMicrocompact: &corecontext.ToolResultMicrocompactConfig{
					IdleThreshold: 5 * time.Minute,
				},
				ExtraStrategies: []corecontext.Strategy{
					ctxpack.NewStoreSummaryCompact(ctxpack.StoreSummaryCompactConfig{
						Store:            store,
						KeepRecentTokens: 20000,
					}),
				},
				Summary: &corecontext.FullSummaryConfig{
					PostSummaryHooks:    []corecontext.PostSummaryHook{restore.Hook()},
					SystemPrompt:        ctxpack.WriterSummarySystemPrompt,
					SummaryPrompt:       ctxpack.WriterSummaryPrompt,
					UpdateSummaryPrompt: ctxpack.WriterUpdateSummaryPrompt,
					TurnPrefixPrompt:    ctxpack.WriterTurnPrefixPrompt,
				},
			})
		},
	}

	editor := subagent.Config{
		Name:               "editor",
		Description:        "审阅者：阅读原文，从结构和审美两个层面发现问题",
		Model:              editorModel,
		SystemPrompt:       bundle.Prompts.Editor,
		Tools:              editorTools,
		MaxTurns:           cfg.ResolveMaxTurns("editor", 20),
		MaxRetries:         subagentMaxRetries,
		ThinkingLevel:      resolvedRoleThinking(editorModel, cfg, "editor"),
		ToolsAreIdempotent: true,
		OnMessage:          onMsg,
		// 仅摘要类终态产物命中即停；save_review 不再硬停——StopAfterTool 退出会绕过
		// StopGuard（agentcore loop.go），若 save_review 硬停，"被派生成弧摘要却先复核"
		// 的 editor 会在 save_review 处被砍断、够不到 save_arc_summary。评审/摘要任务的
		// 收尾改由任务感知的 NewEditorStopGuard 把关。
		StopAfterToolResult: func(toolName string, _ json.RawMessage) bool {
			return toolName == "save_arc_summary" || toolName == "save_volume_summary"
		},
		StopGuardFactory: func(_, task string) agentcore.StopGuard {
			return reminder.NewEditorStopGuard(store, task)
		},
	}

	subagentTool := subagent.New(architectShort, architectLong, writer, editor)

	coordinatorEngine := newContextManager(contextManagerConfig{
		Model:            coordinatorModel,
		ContextWindow:    coordinatorContextWindow,
		ReserveTokens:    bootstrap.CompactReserveTokens(coordinatorContextWindow),
		KeepRecentTokens: 30000,
		Agent:            "coordinator",
		CommitOnProject:  true,
	})

	agent := agentcore.NewAgent(
		agentcore.WithModel(coordinatorModel),
		agentcore.WithSystemPrompt(bundle.Prompts.Coordinator),
		agentcore.WithTools(subagentTool, contextTool, tools.NewSaveUserRulesTool(userRulesSvc), tools.NewReopenBookTool(store)),
		agentcore.WithMaxTurns(100_000),
		agentcore.WithOnMessage(coordinatorOnMessage),
		agentcore.WithToolsAreIdempotent(true),
		// subagent 是流程主通道；真实错误应显式返回给 Host，而不是在单次 run 内永久禁用工具。
		agentcore.WithMaxToolErrors(0),
		agentcore.WithMaxRetries(subagentMaxRetries),
		agentcore.WithContextManager(coordinatorEngine),
		agentcore.WithStopGuard(reminder.NewStopGuard(store, nil)),
		agentcore.WithMiddlewares(flowBoundaryMiddleware(onFlowBoundary)),
		// phase=complete 时硬拦截 subagent 派发，防止 Writer 死循环。
		agentcore.WithToolGate(combineToolGates(
			completePhaseGate(store),
			writerExpandedChapterGate(store),
		)),
	)
	// Coordinator 推理强度：无条件应用解析结果。未配置时为空（不发 thinking，用 provider
	// 默认），与各子代理（Config.ThinkingLevel 默认空）一致——避免覆盖 agentcore 默认
	// ThinkingLow 而对所有 provider 强制发 low（含会被强制开思考的 GLM/Ollama）。
	coordinatorThinking, _ := ResolveThinkingForModel(models.ForRole("coordinator"), roleThinking(cfg, "coordinator"))
	agent.SetThinkingLevel(coordinatorThinking)

	// 运行时联动各角色推理强度：coordinator 走 Agent，子代理走 subagentTool override。
	applyThinking := func(role string, level agentcore.ThinkingLevel) {
		switch role {
		case "coordinator":
			level, _ = ResolveThinkingForModel(models.ForRole("coordinator"), level)
			agent.SetThinkingLevel(level)
		case "architect":
			level, _ = ResolveThinkingForModel(models.ForRole("architect"), level)
			subagentTool.SetThinkingLevel("architect_short", level)
			subagentTool.SetThinkingLevel("architect_long", level)
		case "writer", "editor":
			level, _ = ResolveThinkingForModel(models.ForRole(role), level)
			subagentTool.SetThinkingLevel(role, level)
		}
	}

	return agent, askUser, restore, coordinatorEngine, applyThinking
}

func flowBoundaryMiddleware(onBoundary FlowBoundaryHook) agentcore.ToolMiddleware {
	return func(ctx context.Context, call agentcore.ToolCall, next agentcore.ToolExecuteFunc) (json.RawMessage, error) {
		out, err := next(ctx, call.Args)
		if err == nil && onBoundary != nil && isFlowBoundaryTool(call.Name) {
			onBoundary(call.Name)
		}
		return out, err
	}
}

func isFlowBoundaryTool(name string) bool {
	return name == "subagent" || name == "reopen_book"
}

// completePhaseGate 返回一个 ToolGate：phase=complete 时拒绝所有 subagent 派发。
// 防止 Coordinator LLM 在书完成后仍调用 Writer/Architect 导致死循环。
// HARNESS-METADATA: name=tool_gate_complete_phase class=business_logic
func completePhaseGate(st *store.Store) agentcore.ToolGate {
	return func(_ context.Context, req agentcore.GateRequest) (*agentcore.GateDecision, error) {
		if req.Call.Name != "subagent" {
			return nil, nil
		}
		// fail-open：Load 出错或 progress 为空时一律放行，不因瞬时读错误卡死正常派发。
		// 唯一代价是 complete 期恰逢读失败时死锁可能复现（概率极低，可接受）。
		progress, _ := st.Progress.Load()
		if progress != nil && progress.Phase == domain.PhaseComplete {
			return &agentcore.GateDecision{
				Allowed: false,
				Reason:  "全书已完成（phase=complete），不能直接派子代理。若用户要返工已写章节，请先调用 reopen_book(chapters=[...]) 把书重新打开进入返工态（之后会自动派 writer 重写）；若用户要新增剧情，告知需新建项目。",
			}, nil
		}
		return nil, nil
	}
}

func combineToolGates(gates ...agentcore.ToolGate) agentcore.ToolGate {
	return func(ctx context.Context, req agentcore.GateRequest) (*agentcore.GateDecision, error) {
		for _, gate := range gates {
			if gate == nil {
				continue
			}
			decision, err := gate(ctx, req)
			if err != nil {
				return nil, err
			}
			if decision != nil && !decision.Allowed {
				return decision, nil
			}
		}
		return nil, nil
	}
}

// HARNESS-METADATA: name=writer_expanded_chapter_gate class=business_logic
func writerExpandedChapterGate(st *store.Store) agentcore.ToolGate {
	return func(_ context.Context, req agentcore.GateRequest) (*agentcore.GateDecision, error) {
		if req.Call.Name != "subagent" {
			return nil, nil
		}
		var args struct {
			Agent string `json:"agent"`
			Task  string `json:"task"`
		}
		if err := json.Unmarshal(req.Call.Args, &args); err != nil || args.Agent != "writer" {
			return nil, nil
		}
		chapter := chapterFromTask(args.Task)
		if chapter <= 0 {
			chapter = writerFallbackChapter(st)
		}
		if chapter <= 0 {
			return nil, nil
		}
		if err := tools.EnsureChapterExpanded(st, chapter); err != nil {
			return &agentcore.GateDecision{
				Allowed: false,
				Reason:  err.Error() + "。请改派 architect_long，调用 save_foundation(type=expand_arc) 展开下一弧，或 type=append_volume 追加并展开下一卷后再派 writer。",
			}, nil
		}
		return nil, nil
	}
}

func writerFallbackChapter(st *store.Store) int {
	if st == nil {
		return 0
	}
	progress, err := st.Progress.Load()
	if err != nil || progress == nil {
		return 0
	}
	if len(progress.PendingRewrites) > 0 {
		return progress.PendingRewrites[0]
	}
	return progress.NextChapter()
}

var chapterTaskRe = regexp.MustCompile(`第\s*(\d+)\s*章`)

func chapterFromTask(task string) int {
	m := chapterTaskRe.FindStringSubmatch(task)
	if len(m) < 2 {
		return 0
	}
	n, _ := strconv.Atoi(m[1])
	return n
}

type saveFoundationResult struct {
	Type            string `json:"type"`
	FoundationReady bool   `json:"foundation_ready"`
}

func decodeSaveFoundationResult(toolName string, result json.RawMessage) saveFoundationResult {
	if toolName != "save_foundation" {
		return saveFoundationResult{}
	}
	var r saveFoundationResult
	_ = json.Unmarshal(result, &r)
	return r
}

func architectLongShouldStopAfterToolResult(toolName string, result json.RawMessage) bool {
	r := decodeSaveFoundationResult(toolName, result)
	switch r.Type {
	case "expand_arc", "complete_book":
		return true
	default:
		return false
	}
}
