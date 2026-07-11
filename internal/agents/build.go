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
	if name == "drafter" || name == "draft_finalizer" || name == "world_simulator" {
		// 渲染阶段与推演阶段共用 writer 角色的模型/推理强度配置。
		return "writer"
	}
	return name
}

func worldSimulatorShouldStopAfterToolResult(toolName string, result json.RawMessage) bool {
	if toolName != "simulate_chapter_world" {
		return false
	}
	var r struct {
		Simulated bool `json:"simulated"`
	}
	_ = json.Unmarshal(result, &r)
	return r.Simulated
}

// plannerShouldStopAfterToolResult 推演阶段收敛信号：章节计划落盘（plan_chapter
// 成功或 plan_details finalize 通过，均返回 planned=true）即结束本轮；plan_details
// 分批中（staged）不停。
func plannerShouldStopAfterToolResult(toolName string, result json.RawMessage) bool {
	if toolName != "plan_chapter" && toolName != "plan_details" {
		return false
	}
	var r struct {
		Planned bool   `json:"planned"`
		Staged  string `json:"staged"`
	}
	_ = json.Unmarshal(result, &r)
	return r.Planned && r.Staged == ""
}

type writingContextProfile struct {
	keepRecentTokens        int
	toolKeepRecent          int
	storeKeepRecentTokens   int
	storeSummaryTokenBudget int
	lightTrim               corecontext.LightTrimConfig
	commitOnProject         bool
}

type roleSelection interface {
	CurrentSelection(role string) (provider, model string, explicit bool)
}

func resolveRoleContextWindow(cfg bootstrap.Config, selections roleSelection, role string, fallback agentcore.ChatModel) (int, bootstrap.ContextWindowSource, string) {
	modelName := ""
	if selections != nil {
		_, modelName, _ = selections.CurrentSelection(role)
	}
	if strings.TrimSpace(modelName) == "" && fallback != nil {
		modelName = bootstrap.ModelName(fallback)
	}
	window, source := cfg.ResolveContextWindow(modelName)
	return window, source, modelName
}

func writingContextProfileFor(agentTag string) writingContextProfile {
	profile := writingContextProfile{
		keepRecentTokens:      32000,
		toolKeepRecent:        8,
		storeKeepRecentTokens: 32000,
	}
	switch agentTag {
	case "writer":
		profile.keepRecentTokens = 16000
		profile.toolKeepRecent = 3
		profile.storeKeepRecentTokens = 16000
		profile.storeSummaryTokenBudget = 5000
		profile.lightTrim = corecontext.LightTrimConfig{KeepRecent: 3, TextThreshold: 3200, PreserveHead: 1000, PreserveTail: 700}
	case "world_simulator":
		profile.keepRecentTokens = 8000
		profile.toolKeepRecent = 2
		profile.storeKeepRecentTokens = 8000
		profile.storeSummaryTokenBudget = 3000
		profile.lightTrim = corecontext.LightTrimConfig{KeepRecent: 2, TextThreshold: 2200, PreserveHead: 700, PreserveTail: 500}
	case "drafter":
		profile.keepRecentTokens = 12000
		profile.toolKeepRecent = 2
		profile.storeKeepRecentTokens = 12000
		profile.storeSummaryTokenBudget = 5000
		profile.lightTrim = corecontext.LightTrimConfig{
			KeepRecent:    2,
			TextThreshold: 2200,
			PreserveHead:  800,
			PreserveTail:  500,
		}
		profile.commitOnProject = true
	}
	return profile
}

func cappedMaxTurns(configured, ceiling int) int {
	if configured <= 0 || configured > ceiling {
		return ceiling
	}
	return configured
}

const worldSimulatorSystemPrompt = `你是全角色世界推演器，只负责在章节 POV plan 之前推进单一世界。

执行协议：
1. 只调用一次 novel_context(chapter=N, profile="world_simulation")，读取 simulation_characters、角色状态、世界状态、当前章大纲、用户规则、rewrite_source 和 gaps。
2. 只调用 simulate_chapter_world。按 gaps 分批补角色，每批最多8名；同名角色不要重复提交。
3. 每个实名角色都基于自己的目标、压力、资源、关系和知识边界列出至少两个可选项，作出决定，写明理由、行动、现实耗时、完成度、即时结果、后续状态和至少一个 butterfly_effect。
4. 复杂经营、装修、审批、施工、招商等按现实时间跨章推进。角色看不到的信息不得提前写进其知识边界。
5. 返工章逐条提交 rewrite_fact_coverage，保留终稿事实链；最后补完整 protagonist_projection 并 finalize=true。
6. 工具返回 simulated=true 后立即停止。不得生成或尝试 plan_structure、plan_details、正文、审阅，也不输出总结。`

// subagentMaxRetries 给所有 SubAgentConfig 与 Coordinator 统一的 LLM retry 上限。
// 退避策略优先服从 server Retry-After。写工具图统一声明为非幂等，因此重试只会
// 发生在尚未产生工具副作用的调用边界，不会在落盘后重放整个 turn。
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

const ThinkingUltra agentcore.ThinkingLevel = "ultra"

// ParseThinkingLevel 把配置字符串转 agentcore.ThinkingLevel。
// "" 合法（= 不覆盖/继承）；其余须是 off/low/medium/high/xhigh/max/ultra 之一，
// 否则返回 error（启动时降级当空并 warn，运行时把 error 回显给用户）。
func ParseThinkingLevel(s string) (agentcore.ThinkingLevel, error) {
	lv := agentcore.NormalizeThinkingLevel(agentcore.ThinkingLevel(s))
	switch lv {
	case "", agentcore.ThinkingOff, agentcore.ThinkingLow, agentcore.ThinkingMedium,
		agentcore.ThinkingHigh, agentcore.ThinkingXHigh, agentcore.ThinkingMax, ThinkingUltra:
		return lv, nil
	default:
		return "", fmt.Errorf("无效推理强度 %q（可选：off/low/medium/high/xhigh/max/ultra）", s)
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

	// 手法库检索（craft_recall）与联网研究（web_research）都是共享实例：
	// 设计时刻（architect）与写作/返工时刻（writer）都能动态调，产出带来源标记，
	// 不与本书事实层混淆。web_research 结果登记 meta/web_research_log.md 可审计。
	craftRecall := tools.NewCraftRecallTool(store)
	webResearch := tools.NewWebResearchTool(store)
	architectTools := []agentcore.Tool{
		contextTool,
		saveFoundation,
		craftRecall,
		// 世界推演（离屏世界 tick）：Architect 在弧边界以 GM 身份裁决镜头外世界变化。
		// 短篇/不 tick 的项目不调用即无副作用。
		tools.NewSaveWorldTickTool(store),
		webResearch,
	}
	// 阶段拆分：推演（planner=writer）与正文渲染（drafter）各自独立上下文，
	// 每阶段只拿本阶段所需上下文——planner 吃全量规划上下文产出完整计划落盘，
	// drafter 起一个干净会话只读已定稿计划 + 精要写法上下文渲染正文。
	// 好处：drafter 不背规划对话的历史 token，长章不再撑爆窗口、注意力集中在正文。
	writerTools := []agentcore.Tool{
		contextTool,
		readChapter,
		craftRecall,
		// 联网研究：推演时按本章需要动态补题材现实支架、生活/职业/平台细节、描写素材。
		webResearch,
		// Host 已先派 world_simulator。POV planner 只保留 staged plan 工具，
		// 避免重复携带 simulate_chapter_world 与单发 plan_chapter 的大 schema。
		tools.NewPlanStructureTool(store),
		tools.NewPlanDetailsTool(store),
	}
	// Dedicated world-simulation repair agent. The model repeatedly ignored a
	// textual "do not call plan_details" instruction, so this role receives a
	// capability-level tool set that makes the invalid transition impossible.
	worldSimulatorTools := []agentcore.Tool{
		contextTool,
		tools.NewSimulateChapterWorldTool(store),
	}
	drafterTools := []agentcore.Tool{
		contextTool,
		readChapter,
		craftRecall,
		webResearch,
		tools.NewDraftChapterTool(store),
		tools.NewDraftChapterPartTool(store),
		tools.NewMergeChapterPartsTool(store),
		tools.NewEditChapterTool(store),
		tools.NewCheckConsistencyTool(store),
		commitChapter,
	}
	draftFinalizerTools := []agentcore.Tool{
		contextTool,
		readChapter,
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
		ToolsAreIdempotent: false,
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
		ToolsAreIdempotent:  false,
		OnMessage:           onMsg,
		StopAfterToolResult: architectLongShouldStopAfterToolResult,
		StopGuardFactory:    architectStopGuardFactory,
	}

	plannerPrompt := bundle.Prompts.Planner

	restore := &ctxpack.WriterRestorePack{}
	restore.Refresh(store)

	// writerContextFactory 推演/渲染两阶段共用的上下文管理器工厂（同角色同窗口）。
	writerContextFactory := func(agentTag string) func(agentcore.ChatModel) agentcore.ContextManager {
		return func(model agentcore.ChatModel) agentcore.ContextManager {
			role := agentToRole(agentTag)
			window, _, _ := resolveRoleContextWindow(cfg, models, role, model)
			profile := writingContextProfileFor(agentTag)
			return newContextManager(contextManagerConfig{
				Model:            model,
				ContextWindow:    window,
				ReserveTokens:    bootstrap.CompactReserveTokens(window),
				KeepRecentTokens: profile.keepRecentTokens,
				Agent:            agentTag,
				CommitOnProject:  profile.commitOnProject,
				ToolMicrocompact: &corecontext.ToolResultMicrocompactConfig{
					IdleThreshold: 5 * time.Minute,
					// 保护承载性工具结果（novel_context 的世界/角色/计划注入等）不被 microcompact
					// 硬删——它是推演/渲染的事实基础。需要缩容时留给 store_summary / full_summary
					// 做"摘要而非丢弃"，保住信息本体不伤质量；可再取的结果（read_chapter/
					// check_consistency/craft_recall 等）才允许激进重写。
					Classifier: loadBearingToolClassifier,
					KeepRecent: profile.toolKeepRecent,
				},
				LightTrim: &profile.lightTrim,
				ExtraStrategies: []corecontext.Strategy{
					ctxpack.NewStoreSummaryCompact(ctxpack.StoreSummaryCompactConfig{
						Store:              store,
						KeepRecentTokens:   profile.storeKeepRecentTokens,
						SummaryTokenBudget: profile.storeSummaryTokenBudget,
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
		}
	}

	// 推演阶段（planner，沿用 name="writer" 保持流程/事件兼容）：只做写前推演，
	// 计划落盘（planned=true）即停，不写正文——drafter 会在干净上下文里渲染。
	writer := subagent.Config{
		Name:                "writer",
		Description:         "章节推演师：把大纲/世界/角色推演成完整的写前计划并落盘",
		Model:               writerModel,
		SystemPrompt:        plannerPrompt,
		Tools:               writerTools,
		MaxTurns:            cappedMaxTurns(cfg.ResolveMaxTurns("writer", 30), 30),
		MaxRetries:          subagentMaxRetries,
		ThinkingLevel:       resolvedRoleThinking(writerModel, cfg, "writer"),
		ToolsAreIdempotent:  false,
		StopAfterToolResult: plannerShouldStopAfterToolResult,
		OnMessage:           onMsg,
		StopGuardFactory: func(_, _ string) agentcore.StopGuard {
			return reminder.NewPlannerStopGuard(store)
		},
		ContextManagerFactory: writerContextFactory("writer"),
	}
	worldSimulator := subagent.Config{
		Name:                "world_simulator",
		Description:         "全角色世界推演修复器：只补角色决定、蝴蝶效应、返工事实覆盖和主视角投影，不生成 POV plan",
		Model:               writerModel,
		SystemPrompt:        worldSimulatorSystemPrompt,
		Tools:               worldSimulatorTools,
		MaxTurns:            cappedMaxTurns(cfg.ResolveMaxTurns("writer", 16), 16),
		MaxRetries:          subagentMaxRetries,
		ThinkingLevel:       resolvedRoleThinking(writerModel, cfg, "writer"),
		ToolsAreIdempotent:  false,
		StopAfterToolResult: worldSimulatorShouldStopAfterToolResult,
		OnMessage:           onMsg,
		StopGuardFactory: func(_, _ string) agentcore.StopGuard {
			return reminder.NewWorldSimulatorStopGuard(store)
		},
		ContextManagerFactory: writerContextFactory("world_simulator"),
	}

	// 正文渲染阶段（drafter）：起干净会话，只读已定稿计划 + 精要写法上下文，
	// 把推演渲染成正文并 commit——不背规划对话历史，长章不撑爆窗口。
	drafter := subagent.Config{
		Name:               "drafter",
		Description:        "正文渲染者：基于已定稿的章节计划写出正文、自审并提交",
		Model:              writerModel,
		SystemPrompt:       bundle.Prompts.Drafter,
		Tools:              drafterTools,
		MaxTurns:           cappedMaxTurns(cfg.ResolveMaxTurns("writer", 80), 80),
		MaxRetries:         subagentMaxRetries,
		ThinkingLevel:      resolvedRoleThinking(writerModel, cfg, "writer"),
		ToolsAreIdempotent: false,
		StopAfterTools:     []string{"commit_chapter"},
		OnMessage:          onMsg,
		StopGuardFactory: func(_, _ string) agentcore.StopGuard {
			return reminder.NewWriterStopGuard(store)
		},
		ContextManagerFactory: writerContextFactory("drafter"),
	}
	draftFinalizer := subagent.Config{
		Name:        "draft_finalizer",
		Description: "草稿验收者：恢复已有且晚于当前 plan 的草稿，只做局部修复、检查和提交，不重新生成整章",
		Model:       writerModel,
		SystemPrompt: bundle.Prompts.Drafter + "\n\n你处于草稿恢复验收阶段。必须先读取 source=draft。" +
			"工具集中没有 draft_chapter；只在发现明确硬伤时用 edit_chapter 做最小替换，然后 check_consistency 并 commit_chapter。",
		Tools:              draftFinalizerTools,
		MaxTurns:           cappedMaxTurns(cfg.ResolveMaxTurns("writer", 30), 30),
		MaxRetries:         subagentMaxRetries,
		ThinkingLevel:      resolvedRoleThinking(writerModel, cfg, "writer"),
		ToolsAreIdempotent: false,
		StopAfterTools:     []string{"commit_chapter"},
		OnMessage:          onMsg,
		StopGuardFactory: func(_, _ string) agentcore.StopGuard {
			return reminder.NewWriterStopGuard(store)
		},
		ContextManagerFactory: writerContextFactory("draft_finalizer"),
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
		ToolsAreIdempotent: false,
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

	subagentTool := subagent.New(architectShort, architectLong, writer, worldSimulator, drafter, draftFinalizer, editor)

	coordinatorEngine := newContextManager(contextManagerConfig{
		Model:            coordinatorModel,
		ContextWindow:    coordinatorContextWindow,
		ReserveTokens:    bootstrap.CompactReserveTokens(coordinatorContextWindow),
		KeepRecentTokens: 30000,
		Agent:            "coordinator",
		CommitOnProject:  true,
		// 同样保护 novel_context 承载性结果不被硬删（Coordinator 裁定也依赖它）。
		ToolMicrocompact: &corecontext.ToolResultMicrocompactConfig{
			Classifier: loadBearingToolClassifier,
			KeepRecent: 8,
		},
	})

	agent := agentcore.NewAgent(
		agentcore.WithModel(coordinatorModel),
		agentcore.WithSystemPrompt(bundle.Prompts.Coordinator),
		agentcore.WithTools(subagentTool, contextTool, tools.NewSaveUserRulesTool(userRulesSvc), tools.NewReopenBookTool(store)),
		agentcore.WithMaxTurns(100_000),
		agentcore.WithOnMessage(coordinatorOnMessage),
		agentcore.WithToolsAreIdempotent(false),
		// subagent 是流程主通道；真实错误应显式返回给 Host，而不是在单次 run 内永久禁用工具。
		agentcore.WithMaxToolErrors(0),
		agentcore.WithMaxRetries(subagentMaxRetries),
		agentcore.WithContextManager(coordinatorEngine),
		agentcore.WithStopGuard(reminder.NewStopGuard(store, nil)),
		agentcore.WithMiddlewares(flowBoundaryMiddleware(onFlowBoundary)),
		// phase=complete 时硬拦截 subagent 派发，防止 Writer 死循环。
		agentcore.WithToolGate(combineToolGates(
			singleSubagentModeGate(),
			completePhaseGate(store),
			writerExpandedChapterGate(store),
			writerZeroInitGate(store),
			expandArcWorldTickGate(store),
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
			if role == "writer" {
				// drafter 与 writer 共用推理强度配置，联动切换。
				subagentTool.SetThinkingLevel("drafter", level)
				subagentTool.SetThinkingLevel("world_simulator", level)
				subagentTool.SetThinkingLevel("draft_finalizer", level)
			}
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

// singleSubagentModeGate protects the project's single-writer state machine.
// Parallel work is performed inside frozen-input components (draft sampling,
// external review), never by generic subagents sharing Progress/Checkpoints.
func singleSubagentModeGate() agentcore.ToolGate {
	return func(_ context.Context, req agentcore.GateRequest) (*agentcore.GateDecision, error) {
		if req.Call.Name != "subagent" {
			return nil, nil
		}
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(req.Call.Args, &raw); err != nil {
			return nil, nil
		}
		for _, forbidden := range []string{"tasks", "chain", "team_name"} {
			if _, exists := raw[forbidden]; exists {
				return &agentcore.GateDecision{Allowed: false, Reason: "novel-studio 只允许 subagent 的单任务 agent+task 模式；并行/链式/team 会破坏单世界状态的单写者顺序"}, nil
			}
		}
		if value, exists := raw["background"]; exists {
			var background bool
			if json.Unmarshal(value, &background) == nil && background {
				return &agentcore.GateDecision{Allowed: false, Reason: "novel-studio 禁止后台 subagent 写共享故事状态；请使用同步 agent+task"}, nil
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
		if err := json.Unmarshal(req.Call.Args, &args); err != nil || !isWritingAgentName(args.Agent) {
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

// writerZeroInitGate 第 1 章硬卡点：零章初始化（--zero-init）未就绪时拒绝派 writer 写第 1 章。
// zero-init 是流水线级命令，Coordinator 会话内无法完成——拒绝理由里明确要求其收工，
// 交还宿主 pipeline 自动执行 zero-init 后续跑（StopGuard 对该场景放行 end_turn）。
// 排在 writerExpandedChapterGate 之后：大纲未展开时优先引导改派 architect。
// HARNESS-METADATA: name=writer_zero_init_gate class=business_logic note=第1章前必须过零章初始化的业务不变量
func writerZeroInitGate(st *store.Store) agentcore.ToolGate {
	return func(_ context.Context, req agentcore.GateRequest) (*agentcore.GateDecision, error) {
		if req.Call.Name != "subagent" {
			return nil, nil
		}
		var args struct {
			Agent string `json:"agent"`
			Task  string `json:"task"`
		}
		if err := json.Unmarshal(req.Call.Args, &args); err != nil || !isWritingAgentName(args.Agent) {
			return nil, nil
		}
		chapter := chapterFromTask(args.Task)
		if chapter <= 0 {
			chapter = writerFallbackChapter(st)
		}
		if chapter != 1 {
			return nil, nil
		}
		// world_codex 先于零章检查：它是会话内可补救的（改派 architect 即可），
		// 不要让 Coordinator 误以为只能收工等宿主。
		if err := tools.EnsureWorldCodexForChapterOne(st); err != nil {
			return &agentcore.GateDecision{Allowed: false, Reason: err.Error()}, nil
		}
		if err := tools.EnsureZeroInitReadyForChapterOne(st); err != nil {
			return &agentcore.GateDecision{
				Allowed: false,
				Reason: err.Error() + "。zero-init 是宿主流水线的命令行步骤，你在会话内无法执行：" +
					"请立即结束本轮运行（不要重试派 writer，也不要改派其他代理补救），宿主会自动执行 zero-init 并继续写第 1 章。",
			}, nil
		}
		// 初始 world_tick：零章就绪后，第 1 章推演前离屏世界必须已生成信息流（会话内可补救）。
		if err := tools.EnsureInitialWorldTickForChapterOne(st); err != nil {
			return &agentcore.GateDecision{Allowed: false, Reason: err.Error()}, nil
		}
		// 放行也留痕：门禁只在不满足时拦截，通过时若无日志，事后审计会误以为没检查。
		slog.Info("第 1 章门禁通过：world_codex / 零章 readiness / 初始 world_tick 均就绪，放行", "module", "agents.gate")
		return nil, nil
	}
}

func isWritingAgentName(name string) bool {
	return name == "writer" || name == "world_simulator" || name == "drafter" || name == "draft_finalizer"
}

// expandArcWorldTickGate 弧/卷边界硬卡点：world_tick 落后正文时拒绝 expand_arc/append_volume。
// 展开下一弧/追加新卷前必须先 save_world_tick 把镜头外世界推进到弧末（会话内可补救）。
// HARNESS-METADATA: name=expand_arc_world_tick_gate class=business_logic note=弧边界世界推演必须先于展开
func expandArcWorldTickGate(st *store.Store) agentcore.ToolGate {
	return func(_ context.Context, req agentcore.GateRequest) (*agentcore.GateDecision, error) {
		if req.Call.Name != "subagent" {
			return nil, nil
		}
		var args struct {
			Agent string `json:"agent"`
			Task  string `json:"task"`
		}
		if err := json.Unmarshal(req.Call.Args, &args); err != nil {
			return nil, nil
		}
		// 只拦"展开下一弧/追加新卷"的 architect 派发；其它 architect 任务（含 save_world_tick
		// 本身）放行，否则会把补救动作也一起挡掉造成死锁。
		if !strings.HasPrefix(args.Agent, "architect") {
			return nil, nil
		}
		if !strings.Contains(args.Task, "expand_arc") && !strings.Contains(args.Task, "append_volume") &&
			!strings.Contains(args.Task, "展开") && !strings.Contains(args.Task, "追加") && !strings.Contains(args.Task, "下一弧") && !strings.Contains(args.Task, "下一卷") {
			return nil, nil
		}
		if err := tools.EnsureWorldTickCurrent(st); err != nil {
			return &agentcore.GateDecision{
				Allowed: false,
				Reason:  err.Error() + "。请先派 architect_long 调用 save_world_tick 把世界推进到弧末，再展开下一弧/追加新卷。",
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
