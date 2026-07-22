package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/errs"
	"github.com/chenhongyang/novel-studio/internal/rag"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/stylestat"
	"github.com/voocel/agentcore/schema"
)

// References 嵌入的参考资料。
type References struct {
	// V0
	ChapterGuide      string
	HookTechniques    string
	QualityChecklist  string
	OutlineTemplate   string
	CharacterTemplate string
	ChapterTemplate   string
	// V1
	Consistency      string
	ContentExpansion string
	DialogueWriting  string
	// V2
	StyleReference          string // 风格补充参考（可为空）
	LongformPlanning        string // 通用长篇规划参考
	Differentiation         string // 通用差异化设计参考
	ArcTemplates            string // 题材弧型模板（按 style 加载，可为空）
	AntiAITone              string // 去 AI 味判据库（writer/editor 共用，全程注入）
	ProductionPlaybook      string // 从 AI-Novel-Writing-Assistant 蒸馏的生产链路边界
	HumanFeelCraft          string // 高人工度样本文沉淀的可迁移写法资产
	CharacterBuilding       string // 人物塑造、动机、压力反应与关系动态参考
	EmotionalNarrativeCraft string // 情感叙事、情绪弧线、动机-反应和场景情绪变化参考
	FictionParagraphing     string // 小说正文分段、对话换段、移动阅读文字墙规避参考
	WritingTechniquesDigest string // refer/写作技巧逐篇压缩后的工程写作规则
	RAGWritingGuidelines    string // RAG 召回在小说写作中的使用边界与 trace 判读
	WebReferenceGuidelines  string // 网络参考、最新资料和热梗进入正文的边界
	LongformAIDetector      string // 3000 字整章 AI 检测与交付门禁口径
	LiteraryRendering       string // 权威叙事学转化的文学渲染协议与适用边界
	LiteraryRenderingCards  string // 结构化文学渲染卡目录；完整 references 被预算裁剪时仍保留
	GenreStyleCraft         string // 题材专项写法总则；只在 user_rules/style 命中 profile 时注入
	GenreStyleProfiles      string // 可确定性匹配的题材专项 profile 目录
}

// ContextTool 组装当前章节所需上下文。
type ContextTool struct {
	store             *store.Store
	refs              References
	style             string
	configuredStyle   string
	ragEmbedder       rag.Embedder
	ragVectorSearcher rag.VectorSearcher
	ragBM25Mu         sync.Mutex
	ragBM25State      *domain.RAGIndexState
	ragBM25Index      *rag.BM25Index
	ragRecallMu       sync.Mutex
	ragRecallCache    map[string]ragRecallCacheEntry
	styleStatsMu      sync.Mutex
	styleStatsKey     string
	styleStatsCache   *stylestat.Stats
}

type ragRecallCacheEntry struct {
	items     []domain.RecallItem
	trace     *domain.RetrievalTrace
	expiresAt time.Time
}

// NewContextTool 创建上下文工具。
// user_rules 由 buildUserRules 直接读本书快照（meta/user_rules.json）注入，不再依赖加载选项。
func NewContextTool(store *store.Store, refs References, style string) *ContextTool {
	return &ContextTool{store: store, refs: refs, style: style}
}

// WithConfiguredStyle binds the selected assets/styles/<style>.md body to the
// prose and review surfaces.  It is deliberately separate from References:
// project-all hashes/consumes planning references, while this guide is a
// render-only input and must not invalidate or steer the sealed world/causal
// simulation.
func (t *ContextTool) WithConfiguredStyle(raw string) *ContextTool {
	if t != nil {
		t.configuredStyle = strings.TrimSpace(raw)
	}
	return t
}

func (t *ContextTool) WithRAGEmbedder(embedder rag.Embedder) *ContextTool {
	t.ragEmbedder = embedder
	return t
}

func (t *ContextTool) WithRAGVectorSearcher(searcher rag.VectorSearcher) *ContextTool {
	t.ragVectorSearcher = searcher
	return t
}

func (t *ContextTool) Name() string { return "novel_context" }
func (t *ContextTool) Description() string {
	return "获取小说当前状态和创作上下文。" +
		"不传 chapter：返回 progress_status（phase/flow/next_chapter/pending_rewrites 等进度字段）+ 基础设定，用于判断下一步该做什么。" +
		"传 chapter=N：额外返回该章的前情摘要、伏笔、角色状态、风格规则等写作上下文；profile 可按 planning/world_simulation/draft 缩小只读预算"
}
func (t *ContextTool) Label() string { return "加载上下文" }

// Phase-scoped project-all reads issue a durable server-side access receipt,
// so those calls are serialized as writes. Other context profiles remain pure
// reads and retain concurrent scheduling.
func (t *ContextTool) ReadOnly(args json.RawMessage) bool {
	return !planningContextAccessArgsMayWrite(args)
}
func (t *ContextTool) ConcurrencySafe(args json.RawMessage) bool {
	return !planningContextAccessArgsMayWrite(args)
}

func (t *ContextTool) Schema() map[string]any {
	return schema.Object(
		schema.Property("chapter", schema.Int("章节号。不传则返回进度状态和基础设定（Coordinator 用于判断下一步）；传入则额外返回该章的写作上下文（Writer 用）")),
		schema.Property("profile", schema.Enum("章节上下文预算；planning=章节计划，world_simulation=全角色推演，draft=正文渲染，full=兼容全量", "full", "planning", "world_simulation", "draft")),
	)
}

func (t *ContextTool) Execute(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
	if err := guardOutlineAllDynamicMaterialExecution(t.store, t.Name()); err != nil {
		return nil, err
	}
	var a struct {
		Chapter int    `json:"chapter"`
		Profile string `json:"profile"`
	}
	if err := unmarshalToolArgs(args, &a); err != nil {
		return nil, fmt.Errorf("invalid args: %w", err)
	}
	if err := t.store.Runtime.ValidatePipelineRenderCandidateEvidenceTree(); err != nil {
		return nil, fmt.Errorf("novel_context 校验冻结渲染候选文件树: %w", err)
	}
	lock, err := t.store.Runtime.LoadPipelineExecution()
	if err != nil {
		return nil, fmt.Errorf("novel_context 读取 pipeline execution lock: %w", err)
	}
	if lock != nil {
		if err := requireCurrentPipelineExecutionProcess(lock, "novel_context"); err != nil {
			return nil, err
		}
	}
	if lock != nil && lock.Mode == domain.PipelineExecutionRender {
		if a.Chapter != lock.TargetChapter || strings.TrimSpace(a.Profile) != "draft" {
			return nil, fmt.Errorf(
				"render execution lock 只允许 novel_context(chapter=%d, profile=draft)；收到 chapter=%d profile=%q。冻结渲染禁止 full/planning/world_simulation 与其他章节实时上下文",
				lock.TargetChapter,
				a.Chapter,
				a.Profile,
			)
		}
		raw, _, err := LoadFrozenDraftRenderContext(t.store, lock.TargetChapter, lock.PlanDigest)
		if err != nil {
			return nil, fmt.Errorf("render 加载冻结正文上下文失败: %w", err)
		}
		raw, err = t.attachSealedShortRenderWordBudget(raw, lock.TargetChapter)
		if err != nil {
			return nil, fmt.Errorf("render 注入 sealed 短篇动态字数合同失败: %w", err)
		}
		raw, err = t.attachSealedRerenderFeedback(raw, lock.TargetChapter, lock.PlanDigest)
		if err != nil {
			return nil, err
		}
		raw, err = applyProseRenderCompatibilityOverlay(raw)
		if err != nil {
			return nil, err
		}
		// Sealed candidates publish the exact effective style contract before
		// Drafter can run. Both Drafter and formal Editor then consume this
		// content-addressed receipt. Legacy contexts without a receipt keep the
		// style_contract embedded in their frozen bytes; never rebuild them from
		// current config during recovery.
		required, _, err := EffectiveRenderStyleContractRequired(
			t.store,
			lock.TargetChapter,
			lock.PlanDigest,
		)
		if err != nil {
			return nil, err
		}
		if !required {
			return raw, nil
		}
		effective, err := ApplyEffectiveRenderStyleContract(
			raw,
			t.store,
			lock.TargetChapter,
			lock.PlanDigest,
		)
		if err != nil {
			return nil, fmt.Errorf("load required sealed render style receipt: %w", err)
		}
		return effective, nil
	}

	requestedChapter := a.Chapter
	rewriteTarget, hasRewriteTarget := pendingRewriteTarget(t.store)
	if requestedChapter > 0 && hasRewriteTarget {
		a.Chapter = rewriteTarget
	}
	if a.Chapter > 0 {
		if staged, ok, err := t.stagedPlanRepairContext(a.Chapter, requestedChapter, hasRewriteTarget); ok || err != nil {
			if err != nil {
				return nil, err
			}
			if err := t.addChapterPipelineInstructionContext(staged, a.Chapter); err != nil {
				return nil, fmt.Errorf("load chapter pipeline instruction: %w", err)
			}
			if err := t.addProjectAllStateContext(staged, a.Chapter); err != nil {
				return nil, err
			}
			sanitizeExternalSamplingPolicyContext(t.store, a.Chapter, staged)
			return t.finalizeContextWithAccessReceipt(staged, a.Chapter, a.Profile)
		}
	}

	result := make(map[string]any)
	if hasRewriteTarget && requestedChapter > 0 {
		result["active_chapter_task"] = map[string]any{
			"mode":              "rewrite",
			"chapter":           rewriteTarget,
			"requested_chapter": requestedChapter,
			"corrected":         requestedChapter != rewriteTarget,
			"policy":            "pending_rewrites 队首章是本轮唯一目标；next_plan 和未来大纲只供后续承接，不得改写本轮章号",
		}
	}
	var warnings []string
	seenWarnings := make(map[string]struct{})
	warn := func(scope string, err error) {
		if err == nil || os.IsNotExist(err) {
			return
		}
		msg := fmt.Sprintf("%s 读取失败: %v", scope, err)
		if _, ok := seenWarnings[msg]; ok {
			return
		}
		seenWarnings[msg] = struct{}{}
		warnings = append(warnings, msg)
	}

	if a.Chapter > 0 {
		// Writer 路径：加载全量基础数据 + 章节上下文
		t.buildBaseContext(result, warn)
		seed := newChapterContextEnvelope()
		state := t.prepareChapterContext(a.Chapter, a.Profile, &seed, warn)
		seed.apply(result)
		if err := t.buildChapterContext(ctx, result, state, warn); err != nil {
			return nil, err
		}
		t.buildChapterWorldSimulationContext(result, a.Chapter, warn)
		if err := t.addChapterPipelineInstructionContext(result, a.Chapter); err != nil {
			return nil, fmt.Errorf("load chapter pipeline instruction: %w", err)
		}
		if err := t.addProjectAllStateContext(result, a.Chapter); err != nil {
			return nil, err
		}
		if hasRewriteTarget {
			// 返工只需要当前章契约、原文与 review brief。完整 outline 和未来章窗口会让
			// planner 把已完成的目标章误当作“上一章”，继而规划 next chapter。
			delete(result, "outline")
			result["rewrite_outline_scope"] = map[string]any{
				"chapter": rewriteTarget,
				"policy":  "返工阶段只允许使用 current_chapter_outline；完整大纲与未来章窗口已隐藏，待本章提交并通过审核后恢复",
			}
		}
		// 数据语义标注（治复读交代）：episodic 是已写入正文的备忘，不是待写素材。
		// 只挂容器内，不进顶层镜像。
		if epi, ok := result["episodic_memory"].(map[string]any); ok && len(epi) > 0 {
			epi["_usage"] = "本容器为已写入正文的事实备忘（供一致性与衔接对照）；在新章正文中原样复述这些内容属于重复缺陷"
		}
	} else {
		// Coordinator/Architect 路径：只返回状态 + 结构化数据，不加载全量原文
		t.buildProgressStatus(result)
		t.buildArchitectContext(result, warn)
	}

	// 注入 working_memory.user_rules（canonical 路径）。架构师路径原本没有 working_memory，
	// 由 buildUserRules 按需新建只装 user_rules 的容器。快照缺失时退到内置默认，
	// 始终输出稳定结构，避免 LLM 看到 user_rules=null 走异常分支。
	if a.Chapter > 0 {
		t.buildSimulationProfile(result, "working_memory", warn)
	} else {
		t.buildSimulationProfile(result, "planning_memory", warn)
	}

	t.buildUserRules(result)
	sanitizeExternalSamplingPolicyContext(t.store, a.Chapter, result)
	if a.Profile == "draft" && a.Chapter > 0 {
		plan, err := t.store.Drafts.LoadChapterPlan(a.Chapter)
		if err != nil {
			return nil, fmt.Errorf("load chapter plan before draft context: %w", err)
		}
		if plan != nil {
			if err := validateRewriteCraftConsumption(t.store, *plan); err != nil {
				return nil, fmt.Errorf("第 %d 章 draft render_packet 的 craft receipt 已失效，必须重新规划：%w", a.Chapter, err)
			}
			if err := ValidateRAGFactPlanCurrent(t.store, *plan); err != nil {
				return nil, fmt.Errorf("第 %d 章 draft render_packet 的普通事实 RAG receipt 已失效，必须重新规划：%w", a.Chapter, err)
			}
		}
	}

	if len(warnings) > 0 {
		result["_warnings"] = warnings
	}
	return t.finalizeContextWithAccessReceipt(result, a.Chapter, a.Profile)
}

// attachSealedRerenderFeedback is the sole post-freeze prose overlay.  It is
// enabled when the current formal review rejects the exact committed body that
// is still the current draft, or when a later exact replacement hash has itself
// received a complete blocking DeepSeek judgment. This keeps the immutable POV
// plan authoritative while allowing the next one-shot render to see the newest
// bounded feedback without exposing either rejected prose body.
func (t *ContextTool) attachSealedRerenderFeedback(
	raw json.RawMessage,
	chapter int,
	planDigest string,
) (json.RawMessage, error) {
	if t == nil || t.store == nil {
		return raw, nil
	}
	freshFormalSeed := ReviewRequiresFreshDraft(t.store, chapter)
	externalRejectedReplacement := false
	if formalPlan, loadErr := t.store.Drafts.LoadChapterPlan(chapter); loadErr == nil && formalPlan != nil {
		externalRejectedReplacement = expressionOnlyReviewPlanReusePhase(t.store, chapter, *formalPlan) ==
			expressionOnlyReviewReuseRejectedReplacement
	}
	if !freshFormalSeed && !externalRejectedReplacement {
		return raw, nil
	}
	progress, err := t.store.Progress.Load()
	if err != nil || progress == nil || !slices.Contains(progress.PendingRewrites, chapter) {
		return raw, err
	}
	plan, err := CurrentChapterPlanCheckpoint(t.store, chapter)
	if err != nil || plan == nil || plan.Digest != strings.TrimSpace(planDigest) {
		return nil, fmt.Errorf("sealed rerender feedback plan binding mismatch: plan=%v err=%v", plan, err)
	}
	review, err := t.store.World.LoadReview(chapter)
	if err != nil {
		return nil, fmt.Errorf("sealed rerender feedback load exact review: %w", err)
	}
	if review == nil {
		return nil, fmt.Errorf("sealed rerender feedback exact review is missing")
	}
	briefPath := filepath.Join(t.store.Dir(), "reviews", fmt.Sprintf("%02d_rewrite_brief.md", chapter))
	brief, err := os.ReadFile(briefPath)
	if err != nil {
		return nil, fmt.Errorf("sealed rerender feedback missing exact rewrite brief: %w", err)
	}
	if strings.TrimSpace(string(brief)) == "" || !strings.Contains(string(brief), review.BodySHA256) {
		return nil, fmt.Errorf("sealed rerender feedback rewrite brief is not bound to body %s", review.BodySHA256)
	}
	feedback := map[string]any{
		"chapter":                     chapter,
		"plan_digest":                 plan.Digest,
		"body_sha256":                 review.BodySHA256,
		"formal_rejected_body_sha256": review.BodySHA256,
		"verdict":                     review.Verdict,
		"summary":                     review.Summary,
		"issues":                      compactDraftReviewIssues(review.Issues, 4),
		"rewrite_brief":               string(brief),
		"policy":                      "只修复 exact-body 正式拒稿与最新 exact-body DeepSeek 阻断列出的具体问题；复用冻结 world simulation、POV plan、事实结果与知识边界，不得重规划、照抄示例修法或复用任一旧稿表面。",
	}
	if diagnostics := actionableRewriteDimensionDiagnostics(review, review.Verdict); len(diagnostics) > 0 {
		// This structured copy makes recovery of an already-written rewrite brief
		// safe: old briefs may only say “详见第8维”, but the exact structured
		// review still carries the rule-by-rule comment. It is an expression-only
		// overlay and does not mutate or supersede the frozen plan.
		feedback["blocking_dimension_feedback"] = diagnostics
	}
	if gate, gateErr := t.loadMechanicalGateBriefForBody(chapter, review.BodySHA256); gateErr == nil && gate != nil {
		feedback["mechanical_gate"] = gate
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("decode frozen context for semantic rerender overlay: %w", err)
	}
	if externalRejectedReplacement {
		externalFeedback, externalErr := loadDraftExternalJudgeContextWithStore(t.store, chapter)
		if externalErr != nil {
			return nil, fmt.Errorf("sealed rerender feedback load exact external review: %w", externalErr)
		}
		if externalFeedback == nil || externalFeedback["evaluated_body_sha256"] == "" {
			return nil, fmt.Errorf("sealed rerender feedback exact external review is missing")
		}
		feedback["external_rejected_body_sha256"] = externalFeedback["evaluated_body_sha256"]
		feedback["external_ai_review"] = externalFeedback
		payload["draft_external_ai_review"] = externalFeedback
		payload["draft_external_ai_review_policy"] = "这是最新 exact-body 拒稿的净化诊断；只吸收具体证据与修改计划，不得读取、拼贴或换皮复现被拒正文。"
	}
	payload["sealed_rerender_feedback"] = feedback
	return json.Marshal(payload)
}

func contextBudget(chapter int, profile string) int {
	if chapter <= 0 {
		return 60 * 1024
	}
	switch profile {
	case "planning":
		// The arc's project_all_state accumulates as chapters are projected, so
		// later chapters carry a larger planning context. 64 KiB overflowed by
		// chapter 3 on a normal book even though the writer model has a 272K-token
		// window; use a budget that reflects that capacity.
		return 160 * 1024
	case "draft":
		return 64 * 1024
	case "world_simulation":
		return 96 * 1024
	default:
		return 188 * 1024
	}
}

// contextHardBudget is a fail-closed ceiling, not the normal delivery target.
// Rewrite planning is the one profile that must sometimes carry both the old
// chapter body and its full review brief so the new causal plan can preserve
// facts without guessing. Keep trimming toward 64 KiB first; only the protected
// remainder may use the same 96 KiB ceiling as world simulation.
func contextHardBudget(chapter int, profile string) int {
	if chapter > 0 && profile == "planning" {
		return 224 * 1024
	}
	return contextBudget(chapter, profile)
}

func planningCriticalOverflowMode(result map[string]any, chapter int, profile string) (string, string, bool) {
	if chapter <= 0 || profile != "planning" {
		return "", "", false
	}
	if _, sealed := result["sealed_convergence_replan_context"]; sealed {
		return "sealed_convergence_critical_overflow",
			"已删除未来大纲、全量 projected 历史、角色 authority 包和失败正文表面；仅当前章 outline、精确 protagonist projection、immutable state contract binding、open obligations、receipts、user rules 与净化 diagnostics 可使用 planning 硬上限。",
			true
	}
	if hasContextKey(result, "rewrite_source") {
		return "rewrite_critical_overflow",
			"已按首选预算删除镜像、宽快照与低优先级资料；仅受保护的返工正文、brief 和当前任务使用硬上限。",
			true
	}
	return "", "", false
}

func finalizeContextResult(result map[string]any, chapter int, profile string) (json.RawMessage, error) {
	applyChapterContextProfile(result, profile)
	if chapter > 0 && profile != "" && profile != "full" {
		result["_context_profile"] = profile
	}
	result["_loading_summary"] = buildLoadingSummary(result, chapter)
	if chapter > 0 {
		result["_reading_guide"] = contextReadingGuide
	}

	preferredBudget := contextBudget(chapter, profile)
	finalBudget := preferredBudget
	if !trimByBudget(result, preferredBudget, chapter) {
		data, _ := json.Marshal(result)
		hardBudget := preferredBudget
		overflowMode, overflowPolicy, allowOverflow := planningCriticalOverflowMode(result, chapter, profile)
		if allowOverflow {
			hardBudget = contextHardBudget(chapter, profile)
		}
		if hardBudget <= preferredBudget || len(data) > hardBudget {
			return nil, fmt.Errorf("novel_context profile=%q 的关键上下文无法安全收敛：preferred=%d hard=%d actual=%d critical=%s；请压缩上游 plan，不会静默删除当前任务: %w", profile, preferredBudget, hardBudget, len(data), largestContextKeySizes(result, 8), errs.ErrToolPrecondition)
		}
		result["_context_budget"] = map[string]any{
			"mode":             overflowMode,
			"preferred_bytes":  preferredBudget,
			"hard_limit_bytes": hardBudget,
			"policy":           overflowPolicy,
		}
		if !trimByBudget(result, hardBudget, chapter) {
			data, _ = json.Marshal(result)
			return nil, fmt.Errorf("novel_context profile=%q 的关键上下文超过硬上限：mode=%s preferred=%d hard=%d actual=%d critical=%s: %w", profile, overflowMode, preferredBudget, hardBudget, len(data), largestContextKeySizes(result, 8), errs.ErrToolPrecondition)
		}
		finalBudget = hardBudget
	}
	raw, err := marshalOrderedContext(result)
	if err != nil {
		return nil, err
	}
	if len(raw) > finalBudget {
		return nil, fmt.Errorf("novel_context 最终序列化结果超过预算：profile=%q budget=%d actual=%d: %w", profile, finalBudget, len(raw), errs.ErrToolPrecondition)
	}
	return raw, nil
}

func largestContextKeySizes(result map[string]any, limit int) string {
	type sizedKey struct {
		key  string
		size int
	}
	items := make([]sizedKey, 0, len(result))
	for key, value := range result {
		data, err := json.Marshal(value)
		if err == nil {
			items = append(items, sizedKey{key: key, size: len(data)})
		}
		if section, ok := value.(map[string]any); ok {
			for childKey, childValue := range section {
				childData, childErr := json.Marshal(childValue)
				if childErr == nil {
					items = append(items, sizedKey{key: key + "." + childKey, size: len(childData)})
				}
			}
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].size == items[j].size {
			return items[i].key < items[j].key
		}
		return items[i].size > items[j].size
	})
	if limit > len(items) {
		limit = len(items)
	}
	parts := make([]string, 0, limit)
	for _, item := range items[:limit] {
		parts = append(parts, fmt.Sprintf("%s=%d", item.key, item.size))
	}
	return strings.Join(parts, ",")
}

// ProbeRAGRecall runs the chapter retrieval path without building and trimming
// the full novel_context envelope. A full context may legitimately trim
// selected_memory.rag_recall to stay within its byte budget; probes must report
// the pre-trim retrieval result instead of turning that budget decision into a
// false zero-recall failure.
func (t *ContextTool) ProbeRAGRecall(ctx context.Context, chapter int) ([]domain.RecallItem, *domain.RetrievalTrace, error) {
	if chapter <= 0 {
		return nil, nil, fmt.Errorf("RAG probe chapter 必须大于 0")
	}
	seed := newChapterContextEnvelope()
	state := t.prepareChapterContext(chapter, "planning", &seed, func(string, error) {})
	if state.currentEntry == nil && state.chapterPlan == nil {
		return nil, nil, fmt.Errorf("RAG probe chapter %d 缺少大纲或章节计划", chapter)
	}
	items, trace, cacheHit := t.selectRAGRecall(ctx, state)
	if trace != nil && !cacheHit {
		if err := t.store.RAG.AppendTrace(*trace); err != nil {
			return nil, nil, err
		}
	}
	return items, trace, nil
}

func (t *ContextTool) buildChapterWorldSimulationContext(result map[string]any, chapter int, warn func(string, error)) {
	if !chapterWorldSimulationRequired(t.store) {
		return
	}
	escalation := InspectRenderOnlyReplanEscalation(t.store, chapter)
	result["simulation_characters"] = requiredDossierCharacterNames(t.store, chapter)
	result["visible_characters"] = chapterOutlineCharacterNames(t.store, chapter)
	addAuthority := func() {
		if _, exists := result["simulation_character_authority"]; exists {
			return
		}
		authority := buildSimulationCharacterAuthority(t.store, chapter)
		result["simulation_character_authority"] = authority
		result["simulation_character_authority_policy"] = simulationCharacterAuthorityPolicy(authority)
	}
	if partial, err := t.store.LoadChapterWorldSimulationPartial(chapter); err != nil {
		warn("chapter_world_simulation_partial", err)
	} else if partial != nil {
		status := "partial"
		policy := "复用已落盘的角色决定，只补 gaps 中的最小缺口，不重发已完成角色。"
		gaps := chapterWorldSimulationGaps(t.store, *partial)
		if escalation.Required && !chapterWorldSimulationHasCausalWork(*partial) {
			status = "restartable_shell"
			policy = "该 partial 没有任何角色决定、保留事实覆盖或主角投影；下一次有效 simulate_chapter_world 提交会先丢弃这个空壳，再从当前上下文重建。"
		} else if len(gaps) == 0 {
			status = "ready_to_finalize"
			policy = "全部角色决定、保留事实覆盖、主角投影和来源已通过校验。只调用 simulate_chapter_world(chapter=N, sources=[本轮 planning_context_access_receipt.source_token], finalize=true) 原子转正式；不得重发 character_decisions、protagonist_projection、rewrite_fact_coverage、旧 sources 或 time_window。"
		}
		if status != "ready_to_finalize" {
			addAuthority()
		}
		lockedDecisions := protectedPartialCharacterDecisions(t.store, *partial)
		result["chapter_world_simulation"] = map[string]any{
			"status":                     status,
			"base_tick_id":               partial.BaseTickID,
			"time_window":                partial.TimeWindow,
			"characters_present":         characterDecisionNames(partial.CharacterDecisions),
			"locked_character_decisions": lockedDecisions,
			"projection_seed":            partial.ProtagonistProjection,
			"rewrite_fact_coverage":      partial.RewriteFactCoverage,
			"gaps":                       gaps,
			"policy":                     policy,
			"projection_policy":          "locked_character_decisions 是已落盘、禁止覆盖的权威证据。protagonist_projection 只能由这些决定及本轮新增决定派生；project_all_grounded 只由服务端精确绑定主角 chosen_decision，模型仍必须一次完整提交决定当下真正可用的 available_options、基于可见证据的 decision_reason、observable_effects、hidden_pressures、plan_constraints 和 causal_chain，不得把已失败或已过期动作当作当前选项。rewrite_source 正文可能正是待修旧稿，不得用旧稿覆盖锁定决定。hidden_pressures 也不得把 preserve_facts 明令禁止的推测改写成‘是否/可能’后重新引入。",
		}
		return
	}
	if sim, err := t.store.LoadChapterWorldSimulation(chapter); err != nil {
		warn("chapter_world_simulation", err)
	} else if sim != nil {
		if escalation.Required {
			// The checkpointed simulation is still valuable provenance, but its
			// character decisions and protagonist projection are precisely the
			// exhausted causal surface the Router ordered us to replace. Replaying
			// all of it both breaches the focused profile budget and anchors the new
			// simulator on the failed scene logic. Canonical facts remain available
			// through rewrite_source/rewrite_brief, current outline, dossiers and
			// world state in working_memory.
			addAuthority()
			result["chapter_world_simulation"] = supersededWorldSimulationContext(*sim, escalation)
			return
		}
		gaps := chapterWorldSimulationGaps(t.store, *sim)
		renderOnlyRerender := RenderOnlyRerenderReady(t.store, chapter)
		if renderOnlyRerender {
			gaps = reusableChapterWorldSimulationGaps(t.store, *sim)
		}
		if len(gaps) > 0 {
			addAuthority()
			result["chapter_world_simulation"] = map[string]any{
				"status":             "invalid",
				"simulation_id":      sim.SimulationID,
				"characters_present": characterDecisionNames(sim.CharacterDecisions),
				"gaps":               gaps,
				"next_step":          "当前正式模拟已因返工源或角色可见性变化失效；重新分批调用 simulate_chapter_world 并 finalize。",
			}
			return
		}
		ready := map[string]any{
			"status":                 "ready",
			"simulation_id":          sim.SimulationID,
			"base_tick_id":           sim.BaseTickID,
			"time_window":            sim.TimeWindow,
			"character_count":        len(sim.CharacterDecisions),
			"character_decisions":    sim.CharacterDecisions,
			"protagonist_projection": planningProtagonistProjection(sim.ProtagonistProjection),
			"rewrite_source":         sim.RewriteSource,
			"rewrite_fact_coverage":  sim.RewriteFactCoverage,
			"render_policy":          "character_decisions 仅用于全角色连续性与 commit 回填；正文只能渲染 protagonist_projection.observable_effects 和主角合法获得的信息，hidden/delayed 不得泄露。",
		}
		if renderOnlyRerender {
			ready["source_version_policy"] = "显式 render-only 已校验世界推演和 POV plan；旧正文/brief hash 只表示版本差，不触发重推演。"
		}
		result["chapter_world_simulation"] = ready
		return
	}
	addAuthority()
	result["chapter_world_simulation"] = map[string]any{
		"status":    "missing",
		"next_step": "先调用 simulate_chapter_world 推演全部实名角色并 finalize，再开始 plan_structure。",
	}
}

// protectedPartialCharacterDecisions returns the already-persisted decisions
// that a resumed simulator must use to build rewrite coverage and the POV
// projection. Returning names alone forced the model to reconstruct those
// decisions from the stale rewrite body—the exact artifact being corrected.
// On rewrites, keep the protagonist plus every saved actor named by a preserve
// fact; for non-rewrite partials, retain all saved decisions.
func protectedPartialCharacterDecisions(st *store.Store, partial domain.ChapterWorldSimulation) []domain.CharacterWorldDecision {
	if len(partial.CharacterDecisions) == 0 {
		return nil
	}
	protagonist := strings.TrimSpace(inferCommitProtagonist(st))
	var preserveText string
	if partial.RewriteSource != nil {
		preserveText = strings.Join(partial.RewriteSource.PreserveFacts, "\n")
	}
	if strings.TrimSpace(preserveText) == "" {
		return append([]domain.CharacterWorldDecision(nil), partial.CharacterDecisions...)
	}
	locked := make([]domain.CharacterWorldDecision, 0, len(partial.CharacterDecisions))
	for _, decision := range partial.CharacterDecisions {
		name := strings.TrimSpace(decision.Character)
		if name == "" || (name != protagonist && !strings.Contains(preserveText, name)) {
			continue
		}
		locked = append(locked, decision)
	}
	return locked
}

func supersededWorldSimulationContext(sim domain.ChapterWorldSimulation, escalation RenderOnlyReplanEscalation) map[string]any {
	return map[string]any{
		"status":                   "superseded_for_structural_replan",
		"previous_simulation_id":   sim.SimulationID,
		"previous_base_tick_id":    sim.BaseTickID,
		"previous_character_count": len(sim.CharacterDecisions),
		"previous_rewrite_source":  sim.RewriteSource,
		"reason":                   escalation.Reason,
		"attempts":                 escalation.Attempts,
		"limit":                    escalation.Limit,
		"policy":                   "旧 character_decisions/protagonist_projection 已因连续整章结构失败隐藏；以 current_chapter_outline、rewrite_source.preserve_facts、rewrite_brief、当前角色档案和世界状态建立新因果 epoch，不得沿用旧场景与对白投影。",
		"next_step":                "分批调用 simulate_chapter_world 重建全部实名角色决定和 protagonist_projection，finalize 后才可重建 plan_structure。",
	}
}

func (t *ContextTool) stagedPlanRepairContext(chapter, requestedChapter int, rewrite bool) (map[string]any, bool, error) {
	partial, err := t.store.Drafts.LoadChapterPlanPartial(chapter)
	if err != nil || partial == nil {
		return nil, false, err
	}
	structure, _ := partial["structure"].(map[string]any)
	merged, _ := partial["causal_simulation"].(map[string]any)
	if merged == nil {
		merged = map[string]any{}
	}
	fields := sortedKeys(merged)
	savedCore := map[string]any{}
	for _, field := range []string{
		"world_simulation_id", "protagonist_decision", "project_promise", "chapter_function", "context_sources", "initial_state",
		"environment_state", "causal_beats", "decision_points", "outcome_shift",
	} {
		if value, ok := merged[field]; ok {
			savedCore[field] = value
		}
	}
	stage := map[string]any{
		"status":                "partial",
		"chapter":               chapter,
		"causal_fields_present": fields,
		"policy":                "当前 staged plan 是唯一规划真相；只用 plan_details 补缺并收口，不重新检索、不重做骨架、不写正文。",
	}
	nextStep := "直接调用 plan_details，只补 gap_summary 最靠前的一组；禁止 craft_recall/read_chapter，禁止重新 plan_structure。"
	var simulationStage any
	var readySimulation *domain.ChapterWorldSimulation
	simulationAuthorityNeeded := false
	simulationFinalizationOnly := false
	worldSimulationRequired := chapterWorldSimulationRequired(t.store)
	escalation := InspectRenderOnlyReplanEscalation(t.store, chapter)
	if worldSimulationRequired {
		if partialSim, partialErr := t.store.LoadChapterWorldSimulationPartial(chapter); partialErr == nil && partialSim != nil {
			status := "partial"
			policy := "复用已落盘角色决定，只补 gaps 中的最小缺口。"
			gaps := chapterWorldSimulationGaps(t.store, *partialSim)
			if escalation.Required && !chapterWorldSimulationHasCausalWork(*partialSim) {
				status = "restartable_shell"
				policy = "本 partial 只是无因果证据空壳；下一次带真实角色决定、保留事实覆盖或主角投影的 simulate_chapter_world 提交会自动从当前上下文重建。"
				simulationAuthorityNeeded = true
			} else if len(gaps) == 0 {
				status = "ready_to_finalize"
				policy = "全部角色决定、保留事实覆盖、主角投影和来源已通过校验。只调用 simulate_chapter_world(chapter=N, sources=[本轮 planning_context_access_receipt.source_token], finalize=true) 原子转正式；不得重发 character_decisions、protagonist_projection、rewrite_fact_coverage、旧 sources 或 time_window。"
				simulationFinalizationOnly = true
			} else {
				simulationAuthorityNeeded = true
			}
			simulationStage = map[string]any{
				"status":             status,
				"characters_present": characterDecisionNames(partialSim.CharacterDecisions),
				"gaps":               gaps,
				"policy":             policy,
			}
			if simulationFinalizationOnly {
				nextStep = "只调用 simulate_chapter_world(chapter=N, sources=[本轮 planning_context_access_receipt.source_token], finalize=true) 原子转正式；不得重发任何已落盘字段或旧 sources。成功后重新调用 novel_context(profile=planning) 并按新 simulation 重建 plan_structure。"
			} else {
				nextStep = "先继续分批调用 simulate_chapter_world，完成全角色决定、理由、蝴蝶效应和 protagonist_projection 并 finalize；此前禁止 plan_details、craft_recall/read_chapter。"
			}
		} else if final, loadErr := t.store.LoadChapterWorldSimulation(chapter); loadErr == nil && final != nil {
			if escalation.Required {
				simulationStage = supersededWorldSimulationContext(*final, escalation)
				nextStep = "旧 world simulation 已因连续整章结构失败失效；重新分批调用 simulate_chapter_world 并 finalize，随后重建 plan_structure。"
				simulationAuthorityNeeded = true
			} else if gaps := chapterWorldSimulationGaps(t.store, *final); len(gaps) > 0 {
				simulationStage = map[string]any{
					"status":             "invalid",
					"simulation_id":      final.SimulationID,
					"characters_present": characterDecisionNames(final.CharacterDecisions),
					"gaps":               gaps,
				}
				nextStep = "当前正式模拟未绑定返工源或未覆盖保留事实；重新分批调用 simulate_chapter_world 并 finalize，随后重新 plan_structure。"
				simulationAuthorityNeeded = true
			} else {
				readySimulation = final
				simulationStage = map[string]any{
					"status":                 "ready",
					"simulation_id":          final.SimulationID,
					"character_count":        len(final.CharacterDecisions),
					"protagonist_projection": planningProtagonistProjection(final.ProtagonistProjection),
					"rewrite_source":         final.RewriteSource,
					"rewrite_fact_coverage":  final.RewriteFactCoverage,
				}
			}
		} else {
			simulationStage = map[string]any{"status": "missing"}
			nextStep = "先调用 simulate_chapter_world 完成全角色世界推演并 finalize；此前禁止 plan_details、craft_recall/read_chapter。"
			simulationAuthorityNeeded = true
		}
	}
	structureStatus := "waiting_for_simulation"
	if !worldSimulationRequired {
		if planStructureBoundToSources(t.store, chapter, partial, nil) {
			structureStatus = "ready"
		} else {
			structureStatus = "stale"
			nextStep = "当前 plan_structure 未绑定最新 rewrite_source；先重新调用 plan_structure，再用 plan_details 补缺并 finalize。"
		}
	} else if readySimulation != nil {
		if planStructureBoundToSources(t.store, chapter, partial, readySimulation) {
			structureStatus = "ready"
		} else {
			structureStatus = "stale"
			nextStep = "当前 plan_structure 未绑定最新 world_simulation/rewrite_source；先重新调用 plan_structure，再用 plan_details 补缺并 finalize。"
		}
	}
	switch structureStatus {
	case "stale":
		stage["policy"] = "当前骨架绑定的是旧 world_simulation/rewrite_source，必须先重新 plan_structure；新骨架提交后再用 plan_details 补缺，不检索、不写正文。"
	case "waiting_for_simulation":
		if simulationFinalizationOnly {
			stage["policy"] = "world simulation 的 gaps 已清零；当前只允许 finalize=true 原子收口。正式 simulation 落盘后必须重新 plan_structure，再用 plan_details 收口。不检索、不写正文。"
		} else {
			stage["policy"] = "先完成或重建 world simulation；模拟 finalize 后重新 plan_structure，再用 plan_details 收口。不检索、不写正文。"
		}
	}
	result := map[string]any{
		"active_chapter_task": map[string]any{
			"mode":              "staged_plan_repair",
			"chapter":           chapter,
			"requested_chapter": requestedChapter,
			"corrected":         requestedChapter > 0 && requestedChapter != chapter,
		},
		"current_chapter_outline": func() any {
			entry, _ := t.store.Outline.GetChapterOutline(chapter)
			return entry
		}(),
		"structure":               structure,
		"structure_source_status": structureStatus,
		"saved_core":              savedCore,
		"fields_present":          fields,
		"gap_summary":             planDetailsGapSummary(t.store, chapter, partial, merged),
		"recommended_batches":     planDetailsRecommendedBatches(),
		"simulation_characters":   requiredDossierCharacterNames(t.store, chapter),
		"visible_characters":      chapterOutlineCharacterNames(t.store, chapter),
		"working_memory": map[string]any{
			"chapter_plan_stage": stage,
		},
		"next_step":        nextStep,
		"_loading_summary": fmt.Sprintf("ch=%d | staged_plan_repair | fields=%d", chapter, len(fields)),
	}
	if worldSimulationRequired && simulationAuthorityNeeded {
		authority := buildSimulationCharacterAuthority(t.store, chapter)
		result["simulation_character_authority"] = authority
		result["simulation_character_authority_policy"] = simulationCharacterAuthorityPolicy(authority)
	}
	if receiptID, _ := partial[planCraftReceiptKey].(string); strings.TrimSpace(receiptID) != "" {
		if receipt, loadErr := t.store.RAG.LoadCraftRecallReceipt(receiptID); loadErr != nil {
			result["_warnings"] = []string{fmt.Sprintf("rewrite craft receipt 读取失败: %v", loadErr)}
		} else if receipt != nil {
			current, currentErr := rewriteCraftReceiptIsCurrent(t.store, chapter, receipt)
			if currentErr != nil {
				result["_warnings"] = []string{fmt.Sprintf("rewrite craft receipt 当前绑定复验失败: %v", currentErr)}
			} else if current {
				result["rewrite_craft_pack"] = craftReceiptContext(receipt)
			} else {
				warnings, _ := result["_warnings"].([]string)
				result["_warnings"] = append(warnings, "staged rewrite craft receipt 已因正文/brief/index/triggers/generation 变化而失效；未向 Planner 回放旧方法包")
				result["rewrite_craft_status"] = "stale_refresh_in_plan_details"
				result["next_step"] = "当前 staged craft receipt 已失效；调用 plan_details 提交下一批时会自动刷新 receipt 并剔除旧 refs，刷新前不要按旧 pack 规划或写正文。"
			}
		}
	} else if rewrite {
		needs := deriveRewriteCraftNeeds(t.store, chapter)
		if len(needs) > 0 {
			result["rewrite_craft_status"] = map[string]any{
				"status": "pending_deterministic_receipt",
				"needs":  needs,
				"policy": "不得凭空补写 RAG 手法，也不得直接 craft_recall。先完成当前 world simulation；随后下一次合法的 plan_structure（骨架 stale）或 plan_details（旧中间态恢复）会原子生成 receipt 并返回 rewrite_craft_pack，之后均按 receipt_id 回放同一方法包。",
			}
		} else {
			result["rewrite_craft_status"] = map[string]any{
				"status": "not_required",
				"policy": "当前结构化 review 未派生 rewrite craft need；不得为了形式自行引用 RAG 素材。",
			}
		}
	}
	factReceiptContext, factReceiptErr := currentRAGFactReceiptContext(t.store, chapter)
	if factReceiptErr != nil {
		return nil, true, fmt.Errorf("staged plan repair current RAG fact receipt: %w", factReceiptErr)
	}
	if factReceiptContext != nil {
		pack, _ := result["reference_pack"].(map[string]any)
		if pack == nil {
			pack = map[string]any{}
		}
		pack["rag_fact_receipt"] = factReceiptContext
		result["reference_pack"] = pack
	}
	if cards, decodeErr := decodeLiteraryRenderingCards(t.refs.LiteraryRenderingCards); decodeErr != nil {
		warnings, _ := result["_warnings"].([]string)
		result["_warnings"] = append(warnings, fmt.Sprintf("literary_rendering_cards 读取失败: %v", decodeErr))
	} else if cards != nil {
		pack, _ := result["reference_pack"].(map[string]any)
		if pack == nil {
			pack = map[string]any{}
		}
		pack["literary_rendering_cards"] = cards
		result["reference_pack"] = pack
	}
	if simulationStage != nil {
		result["chapter_world_simulation"] = simulationStage
	}
	if rewrite {
		workingBodySHA := currentChapterContextBodySHA(t.store, chapter)
		brief := map[string]any{}
		if progress, loadErr := t.store.Progress.Load(); loadErr == nil && progress != nil && strings.TrimSpace(progress.RewriteReason) != "" {
			brief["reason"] = progress.RewriteReason
		}
		if review, loadErr := t.store.World.LoadReview(chapter); loadErr == nil && review != nil {
			brief["review_summary"] = review.Summary
			brief["issues"] = review.Issues
			brief["contract_misses"] = review.ContractMisses
		}
		if gate, gateErr := t.loadMechanicalGateBriefForBody(chapter, workingBodySHA); gateErr == nil && gate != nil {
			brief["mechanical_gate"] = gate
		}
		if analysis, aiErr := t.loadAIVoiceRedFlagsForBody(chapter, workingBodySHA); aiErr == nil && analysis != nil {
			if actionable := domain.ActionableAIVoiceAnalysis(analysis); actionable != nil {
				brief["ai_voice_redflags"] = actionable
			}
		}
		if supplements := loadRewriteBriefHumanSupplements(t.store.Dir(), chapter); len(supplements) > 0 {
			brief["human_acceptance_supplements"] = supplements
			brief["human_acceptance_policy"] = "人工验收补充优先于概率代理和评审示例；其中禁止项是硬约束，示例只能说明问题，不得照搬或换皮。"
		}
		if len(brief) > 0 {
			result["rewrite_brief"] = brief
		}
		source, body, markdown, loadErr := loadChapterRewriteSource(t.store, chapter)
		if loadErr != nil {
			return nil, true, loadErr
		}
		if source != nil {
			result["rewrite_source"] = rewriteSourceContext(source, body, markdown)
		}
	}
	if issues := ChapterPlanIdentityIssues(t.store, chapter, partial); len(issues) > 0 {
		result["scope_warnings"] = issues
	}
	return result, true, nil
}

// buildLoadingSummary 从已组装的 result 中统计各项数据量，生成一行可读摘要。
func buildLoadingSummary(result map[string]any, chapter int) string {
	var parts []string

	if chapter > 0 {
		parts = append(parts, fmt.Sprintf("ch=%d", chapter))
	} else {
		parts = append(parts, "architect")
	}
	if tier, ok := result["planning_tier"].(domain.PlanningTier); ok && tier != "" {
		parts = append(parts, fmt.Sprintf("tier=%s", tier))
	}

	// 卷弧位置
	if pos, ok := result["position"].(map[string]any); ok {
		parts = append(parts, fmt.Sprintf("V%dA%d", pos["volume"], pos["arc"]))
	}

	var items []string
	countSlice := func(key string) int {
		if v, ok := result[key]; ok {
			if s, ok := v.([]domain.Character); ok {
				return len(s)
			}
			// 通用 slice 反射
			return sliceLen(v)
		}
		return 0
	}

	// 角色
	if n := countSlice("character_snapshots"); n > 0 {
		items = append(items, fmt.Sprintf("角色:%d(快照)", n))
	} else if n := countSlice("characters"); n > 0 {
		items = append(items, fmt.Sprintf("角色:%d", n))
	}

	if working, ok := result["working_memory"].(map[string]any); ok && len(working) > 0 {
		items = append(items, fmt.Sprintf("工作记忆:%d", len(working)))
	}
	if episodic, ok := result["episodic_memory"].(map[string]any); ok && len(episodic) > 0 {
		items = append(items, fmt.Sprintf("情节记忆:%d", len(episodic)))
	}
	if planning, ok := result["planning_memory"].(map[string]any); ok && len(planning) > 0 {
		items = append(items, fmt.Sprintf("规划记忆:%d", len(planning)))
	}
	if foundation, ok := result["foundation_memory"].(map[string]any); ok && len(foundation) > 0 {
		items = append(items, fmt.Sprintf("基础记忆:%d", len(foundation)))
	}

	// 分层摘要
	if n := countSlice("volume_summaries"); n > 0 {
		items = append(items, fmt.Sprintf("卷摘要:%d", n))
	}
	if n := countSlice("arc_summaries"); n > 0 {
		items = append(items, fmt.Sprintf("弧摘要:%d", n))
	}
	if n := countSlice("recent_summaries"); n > 0 {
		items = append(items, fmt.Sprintf("章摘要:%d", n))
	}

	// 分层大纲
	if n := countSlice("layered_outline"); n > 0 {
		items = append(items, fmt.Sprintf("分层大纲:%d卷", n))
	}

	// 状态数据
	if n := countSlice("timeline"); n > 0 {
		items = append(items, fmt.Sprintf("时间线:%d", n))
	}
	if n := countSlice("foreshadow_ledger"); n > 0 {
		items = append(items, fmt.Sprintf("伏笔:%d", n))
	}
	if n := countSlice("relationship_state"); n > 0 {
		items = append(items, fmt.Sprintf("关系:%d", n))
	}
	if n := countSlice("recent_state_changes"); n > 0 {
		items = append(items, fmt.Sprintf("状态变化:%d", n))
	}
	if _, ok := result["previous_tail"]; ok {
		items = append(items, "前章尾部:ok")
	}
	if _, ok := result["style_rules"]; ok {
		items = append(items, "风格规则:ok")
	}
	if n := sliceLen(result["related_chapters"]); n > 0 {
		items = append(items, fmt.Sprintf("相关章:%d", n))
	}
	if selected, ok := result["selected_memory"].(map[string]any); ok && len(selected) > 0 {
		if n := sliceLen(selected["story_threads"]); n > 0 {
			items = append(items, fmt.Sprintf("线索召回:%d", n))
		}
		if n := sliceLen(selected["rag_recall"]); n > 0 {
			items = append(items, fmt.Sprintf("RAG:%d", n))
		}
		if n := sliceLen(selected["review_lessons"]); n > 0 {
			items = append(items, fmt.Sprintf("评审召回:%d", n))
		}
	}

	// 参考资料
	if refs, ok := result["references"].(map[string]string); ok && len(refs) > 0 {
		items = append(items, fmt.Sprintf("参考:%d项", len(refs)))
	}
	if pack, ok := result["reference_pack"].(map[string]any); ok && len(pack) > 0 {
		items = append(items, fmt.Sprintf("参考包:%d", len(pack)))
	}
	if _, ok := result["writing_engine"]; ok {
		items = append(items, "写法引擎:ok")
	}
	if _, ok := result["book_world_context"]; ok {
		items = append(items, "本书世界:ok")
	}
	if _, ok := result["resource_audit"]; ok {
		items = append(items, "资源审计:ok")
	}
	if _, ok := result["character_continuity"]; ok {
		items = append(items, "人物续用:ok")
	}
	if _, ok := result["memory_policy"]; ok {
		items = append(items, "记忆策略:ok")
	}
	if _, ok := result["simulation_profile"]; ok {
		items = append(items, "仿写画像:ok")
	}
	if warnings, ok := result["_warnings"].([]string); ok && len(warnings) > 0 {
		items = append(items, fmt.Sprintf("告警:%d", len(warnings)))
	}
	if trimmed, ok := result["_trimmed"].([]string); ok && len(trimmed) > 0 {
		items = append(items, fmt.Sprintf("裁剪:%s", strings.Join(trimmed, ",")))
	}

	if len(items) > 0 {
		parts = append(parts, strings.Join(items, " "))
	}
	return strings.Join(parts, " | ")
}

// sliceLen 对 any 类型尝试取 slice 长度。
func sliceLen(v any) int {
	switch s := v.(type) {
	case []domain.ChapterSummary:
		return len(s)
	case []domain.ArcSummary:
		return len(s)
	case []domain.VolumeSummary:
		return len(s)
	case []domain.CharacterSnapshot:
		return len(s)
	case []domain.TimelineEvent:
		return len(s)
	case []domain.ForeshadowEntry:
		return len(s)
	case []domain.RelationshipEntry:
		return len(s)
	case []domain.StateChange:
		return len(s)
	case []domain.VolumeOutline:
		return len(s)
	case []domain.Character:
		return len(s)
	case []domain.RelatedChapter:
		return len(s)
	case []domain.RecallItem:
		return len(s)
	case []domain.RAGChunk:
		return len(s)
	default:
		return 0
	}
}

// loadFilteredCharacters 按本章参与者和 Tier 过滤角色。
// 有明确参与者时只返回参与者 + 主角兜底；无参与者时退回旧的 Tier 策略。
func (t *ContextTool) loadFilteredCharacters(result map[string]any, chapter int, participants []string, warn func(string, error)) {
	chars, err := t.store.Characters.Load()
	if err != nil {
		warn("characters", err)
		return
	}
	if len(chars) == 0 {
		return
	}

	// 获取当前章节大纲的场景描述，用于匹配次要角色
	entry, err := t.store.Outline.GetChapterOutline(chapter)
	if err != nil {
		warn("current_chapter_outline", err)
		result["characters"] = chars
		annotateCharacterPsych(result, chars)
		return
	}
	sceneText := strings.Join(entry.Scenes, " ") + " " + entry.CoreEvent + " " + entry.Title

	filtered := filterCharactersForChapter(chars, participants, sceneText)
	result["characters"] = filtered
	annotateCharacterPsych(result, filtered)
}

// annotateCharacterPsych 任一注入角色带定量心理画像时，附一行行为化使用指引。
// DNA 分组按 Exposed → Hidden → Latent 的可见时机使用。
func annotateCharacterPsych(result map[string]any, chars []domain.Character) {
	hasPsych := false
	for _, c := range chars {
		if c.Psych != nil {
			hasPsych = true
			break
		}
	}
	if !hasPsych {
		return
	}
	result["psych_usage"] = "角色 psych 画像使用指引：big_five/values/moral_foundations 等分数用行为与决策展示，不要写形容词标签（如高 N 角色遇挫写生理反应与过度解读，不写'她很焦虑'）；dna.exposed 可直接展示，dna.hidden 只做暗示埋线，dna.latent 未到转折点不得明写；attachment 决定亲密关系里的追-逃模式"
}

func filterCharactersForChapter(chars []domain.Character, participants []string, sceneText string) []domain.Character {
	participantSet := make(map[string]struct{}, len(participants))
	for _, p := range participants {
		if p != "" {
			participantSet[p] = struct{}{}
		}
	}
	if len(participantSet) > 0 {
		var filtered []domain.Character
		for _, c := range chars {
			if _, ok := participantSet[c.Name]; ok {
				filtered = append(filtered, c)
				continue
			}
			if strings.Contains(c.Role, "主角") || (c.Tier == "core" && len(filtered) == 0) {
				filtered = append(filtered, c)
			}
		}
		return uniqueCharacters(filtered)
	}

	var filtered []domain.Character
	for _, c := range chars {
		switch c.Tier {
		case "secondary", "decorative":
			if matchCharacter(sceneText, c) {
				filtered = append(filtered, c)
			}
		default: // core, important, 或未设置
			filtered = append(filtered, c)
		}
	}
	return uniqueCharacters(filtered)
}

func uniqueCharacters(chars []domain.Character) []domain.Character {
	seen := make(map[string]struct{}, len(chars))
	var out []domain.Character
	for _, c := range chars {
		if c.Name == "" {
			continue
		}
		if _, ok := seen[c.Name]; ok {
			continue
		}
		seen[c.Name] = struct{}{}
		out = append(out, c)
	}
	return out
}

// matchCharacter 检查场景文本中是否包含角色的正式名或任一别名。
func matchCharacter(text string, c domain.Character) bool {
	if strings.Contains(text, c.Name) {
		return true
	}
	for _, alias := range c.Aliases {
		if strings.Contains(text, alias) {
			return true
		}
	}
	return false
}

// loadLayeredSummaries 分层摘要加载：卷摘要 + 当前卷弧摘要 + 弧内章摘要。
func (t *ContextTool) loadLayeredSummaries(result map[string]any, chapter, summaryWindow int, warn func(string, error)) {
	vol, arc, err := t.store.Outline.LocateChapter(chapter)
	if err != nil {
		warn("layered_outline_position", err)
		// 回退到扁平模式
		if summaries, err := t.store.Summaries.LoadRecentSummaries(chapter, summaryWindow); err == nil && len(summaries) > 0 {
			result["recent_summaries"] = summaries
		} else {
			warn("recent_summaries", err)
		}
		return
	}

	// 1. 已完成卷的卷摘要
	if volSummaries, err := t.store.Summaries.LoadAllVolumeSummaries(); err == nil && len(volSummaries) > 0 {
		result["volume_summaries"] = volSummaries
	} else {
		warn("volume_summaries", err)
	}

	// 2. 当前卷内已完成弧的弧摘要（不含当前弧）
	if arcSummaries, err := t.store.Summaries.LoadArcSummaries(vol); err == nil && len(arcSummaries) > 0 {
		var prior []domain.ArcSummary
		for _, s := range arcSummaries {
			if s.Arc < arc {
				prior = append(prior, s)
			}
		}
		if len(prior) > 0 {
			result["arc_summaries"] = prior
		}
	} else {
		warn("arc_summaries", err)
	}

	// 3. 当前弧内最近 N 章的章摘要
	if summaries, err := t.store.Summaries.LoadRecentSummaries(chapter, summaryWindow); err == nil && len(summaries) > 0 {
		result["recent_summaries"] = summaries
	} else {
		warn("recent_summaries", err)
	}
}

// loadLayeredCharacters Layered 模式下的角色加载：优先用最近快照，回退到原始设定 + Tier 过滤。
func (t *ContextTool) loadLayeredCharacters(result map[string]any, chapter int, participants []string, warn func(string, error)) {
	snapshots, err := t.store.Characters.LoadLatestSnapshots()
	if err == nil && len(snapshots) > 0 {
		result["character_snapshots"] = filterSnapshotsForChapter(snapshots, participants)
		// 同时保留原始设定中的 core/important 角色（快照可能不含新登场角色）
		t.loadFilteredCharacters(result, chapter, participants, warn)
		return
	}
	warn("character_snapshots", err)
	// 无快照时回退到原始设定
	t.loadFilteredCharacters(result, chapter, participants, warn)
}

func filterSnapshotsForChapter(snapshots []domain.CharacterSnapshot, participants []string) []domain.CharacterSnapshot {
	if len(participants) == 0 {
		return snapshots
	}
	set := make(map[string]struct{}, len(participants))
	for _, p := range participants {
		if p != "" {
			set[p] = struct{}{}
		}
	}
	var out []domain.CharacterSnapshot
	for _, snap := range snapshots {
		if _, ok := set[snap.Name]; ok {
			out = append(out, snap)
		}
	}
	return out
}

// writerReferences 返回写作参考资料。章节 1 返回全量，后续章节裁剪掉不再需要的模板。
func (t *ContextTool) writerReferences(chapter int) map[string]string {
	refs := map[string]string{}
	add := func(k, v string) {
		if v != "" {
			refs[k] = v
		}
	}
	// 渐进式加载：始终保留核心参考，前 3 章额外加载完整写作指南
	add("consistency", t.refs.Consistency)
	add("hook_techniques", t.refs.HookTechniques)
	add("quality_checklist", t.refs.QualityChecklist)
	add("anti_ai_tone", t.refs.AntiAITone) // 去 AI 味判据全程注入，不随章节裁剪
	add("production_playbook", t.refs.ProductionPlaybook)
	add("human_feel_craft", t.refs.HumanFeelCraft)
	add("character_building", t.refs.CharacterBuilding)
	add("emotional_narrative_craft", t.refs.EmotionalNarrativeCraft)
	add("fiction_paragraphing", t.refs.FictionParagraphing)
	add("writing_techniques_digest", t.refs.WritingTechniquesDigest)
	add("rag_writing_guidelines", t.refs.RAGWritingGuidelines)
	add("web_reference_guidelines", t.refs.WebReferenceGuidelines)
	add("longform_ai_detector", t.refs.LongformAIDetector)
	add("literary_rendering", t.refs.LiteraryRendering)
	if chapter <= 3 {
		add("chapter_guide", t.refs.ChapterGuide)
		add("dialogue_writing", t.refs.DialogueWriting)
		add("style_reference", t.refs.StyleReference)
	}

	// 仅首章加载的补充参考
	if chapter <= 1 {
		add("chapter_template", t.refs.ChapterTemplate)
		add("content_expansion", t.refs.ContentExpansion)
	}
	return refs
}

func (t *ContextTool) architectReferences() map[string]string {
	refs := map[string]string{}
	add := func(k, v string) {
		if v != "" {
			refs[k] = v
		}
	}
	add("outline_template", t.refs.OutlineTemplate)
	add("character_template", t.refs.CharacterTemplate)
	add("longform_planning", t.refs.LongformPlanning)
	add("differentiation", t.refs.Differentiation)
	add("style_reference", t.refs.StyleReference)
	add("arc_templates", t.refs.ArcTemplates)
	add("anti_ai_tone", t.refs.AntiAITone) // architect 大纲去 AI 腔；亦兜 editor 走 Chapter=0 路径
	add("production_playbook", t.refs.ProductionPlaybook)
	add("human_feel_craft", t.refs.HumanFeelCraft)
	add("character_building", t.refs.CharacterBuilding)
	add("emotional_narrative_craft", t.refs.EmotionalNarrativeCraft)
	add("fiction_paragraphing", t.refs.FictionParagraphing)
	add("writing_techniques_digest", t.refs.WritingTechniquesDigest)
	add("rag_writing_guidelines", t.refs.RAGWritingGuidelines)
	add("web_reference_guidelines", t.refs.WebReferenceGuidelines)
	add("longform_ai_detector", t.refs.LongformAIDetector)
	return refs
}

// foundationStatus 检查基础设定的完备性，返回缺失项列表。
// 与 save_foundation 工具共用 store.FoundationMissing 判定逻辑，保证 LLM 从
// novel_context 看到的 ready/missing 与 save_foundation 返回的 foundation_ready
// 永远一致（长篇 compass 必需项等细节不会漂移）。
func (t *ContextTool) foundationStatus() map[string]any {
	missing := FoundationCoreMissing(t.store.Dir())
	status := map[string]any{"ready": len(missing) == 0}
	if len(missing) > 0 {
		status["missing"] = missing
	}
	return status
}

// ContextSummary 返回当前状态的简要摘要（供日志使用）。
func (t *ContextTool) ContextSummary() string {
	var parts []string
	if p, _ := t.store.Outline.LoadPremise(); p != "" {
		parts = append(parts, "premise:ok")
	}
	if o, _ := t.store.Outline.LoadOutline(); o != nil {
		parts = append(parts, fmt.Sprintf("outline:%d chapters", len(o)))
	}
	if c, _ := t.store.Characters.Load(); c != nil {
		parts = append(parts, fmt.Sprintf("characters:%d", len(c)))
	}
	if len(parts) == 0 {
		return "empty"
	}
	return strings.Join(parts, ", ")
}

// trimByBudget 按优先级裁剪 result，使 JSON 总大小不超过 budget 字节。
// 优先级（从低到高）：references < voice_samples < style_anchors < previous_tail < timeline
//
//	< recent_state_changes < foreshadow_ledger < relationship_state < 其余（不裁剪）
//
// 裁剪的 key 会记录到 result["_trimmed"] 供日志排查。返回值只在包含
// `_trimmed` 后的最终 JSON 已不超过 budget 时为 true；关键上下文不会被静默删除。
func trimByBudget(result map[string]any, budget int, chapter ...int) bool {
	var trimmed []string
	seenTrimmed := make(map[string]struct{})
	if existing, ok := result["_trimmed"].([]string); ok {
		for _, key := range existing {
			if _, seen := seenTrimmed[key]; seen {
				continue
			}
			seenTrimmed[key] = struct{}{}
			trimmed = append(trimmed, key)
		}
	}
	recordTrimmed := func(key string) {
		if _, seen := seenTrimmed[key]; seen {
			return
		}
		seenTrimmed[key] = struct{}{}
		trimmed = append(trimmed, key)
	}
	fits := func() bool {
		if len(trimmed) > 0 {
			result["_trimmed"] = append([]string(nil), trimmed...)
		} else {
			delete(result, "_trimmed")
		}
		if len(chapter) > 0 {
			if _, hasSummary := result["_loading_summary"]; hasSummary {
				result["_loading_summary"] = buildLoadingSummary(result, chapter[0])
			}
		}
		data, err := json.Marshal(result)
		return err == nil && len(data) <= budget
	}
	if fits() {
		return true
	}

	// chapterContextEnvelope 同时保留 canonical 容器与旧版顶层镜像。上下文较大时
	// 先删镜像，模型仍可从 working_memory/episodic_memory/reference_pack/
	// selected_memory 读取同一份数据，且不会为完全相同的内容支付两次窗口成本。
	for _, containerKey := range []string{"working_memory", "episodic_memory", "reference_pack", "selected_memory"} {
		section, ok := result[containerKey].(map[string]any)
		if !ok {
			continue
		}
		for key := range section {
			if _, mirrored := result[key]; !mirrored {
				continue
			}
			delete(result, key)
			recordTrimmed("mirror:" + key)
		}
	}

	// chapter_plan 已内含 causal_simulation，而 working_memory 还提供同级的
	// causal_simulation。保留同级版本供 Drafter 使用，把计划内副本清空即可。
	if working, ok := result["working_memory"].(map[string]any); ok {
		if _, hasCausal := working["causal_simulation"]; hasCausal {
			if stripChapterPlanCausalDuplicate(working) {
				recordTrimmed("chapter_plan.causal_simulation")
			}
		}
	}

	if fits() {
		return true
	}

	// 按优先级从低到高列出可裁剪的 key。后半段是大项目的阶段性历史快照：
	// 当前章已有完整 causal plan 时，它们与计划中的人物/世界推演高度重复；新章
	// 尚无 plan 时则通常会在达到这些项之前收敛，因此仍能拿到角色档案。
	trimOrder := []string{
		"references",
		"voice_samples",
		"style_anchors",
		"style_rules",
		"writing_engine",
		"retrieval_trace",
		"rag_recall",
		"book_world_context",
		"style_stats",
		"previous_tail",
		"timeline",
		"recent_state_changes",
		"foreshadow_ledger",
		"relationship_state",
		"character_continuity",
		"evolution_report",
		"project_progress",
		"world_codex",
		"book_world",
		"world_foundation",
		"premise_sections",
		"character_dossiers",
		"characters",
		"world_rules",
		"premise",
	}

	for _, key := range trimOrder {
		if !hasContextKey(result, key) {
			continue
		}
		if key == "rag_recall" {
			// RAG is executable context, not merely a pre-trim diagnostic. Keep
			// the highest-ranked fact even under pressure, then trim broader
			// snapshots around it. If the critical payload plus one fact cannot
			// fit, fail explicitly instead of reporting recall success while
			// silently delivering zero hits to the Writer.
			if retainStrongestRAGRecall(result) {
				recordTrimmed("rag_recall:top1_preserved")
			}
			if fits() {
				return true
			}
			continue
		}
		deleteContextKey(result, key)
		recordTrimmed(key)
		if fits() {
			return true
		}
	}
	return fits()
}

func retainStrongestRAGRecall(result map[string]any) bool {
	changed := false
	retain := func(section map[string]any) {
		if section == nil {
			return
		}
		switch items := section["rag_recall"].(type) {
		case []domain.RecallItem:
			if len(items) > 1 {
				section["rag_recall"] = append([]domain.RecallItem(nil), items[0])
				changed = true
			}
		case []any:
			if len(items) > 1 {
				section["rag_recall"] = append([]any(nil), items[0])
				changed = true
			}
		}
	}
	retain(result)
	if selected, ok := result["selected_memory"].(map[string]any); ok {
		retain(selected)
	}
	return changed
}

func hasContextKey(result map[string]any, key string) bool {
	if _, ok := result[key]; ok {
		return true
	}
	for _, containerKey := range []string{
		"working_memory",
		"episodic_memory",
		"planning_memory",
		"foundation_memory",
		"reference_pack",
		"selected_memory",
	} {
		section, ok := result[containerKey].(map[string]any)
		if !ok {
			continue
		}
		if _, ok := section[key]; ok {
			return true
		}
	}
	return false
}

func stripChapterPlanCausalDuplicate(working map[string]any) bool {
	switch plan := working["chapter_plan"].(type) {
	case *domain.ChapterPlan:
		if plan == nil || !hasChapterCausalSimulation(plan.CausalSimulation) {
			return false
		}
		clone := *plan
		clone.CausalSimulation = domain.ChapterCausalSimulation{}
		working["chapter_plan"] = clone
		return true
	case domain.ChapterPlan:
		if !hasChapterCausalSimulation(plan.CausalSimulation) {
			return false
		}
		plan.CausalSimulation = domain.ChapterCausalSimulation{}
		working["chapter_plan"] = plan
		return true
	case map[string]any:
		if _, ok := plan["causal_simulation"]; !ok {
			return false
		}
		clone := make(map[string]any, len(plan)-1)
		for key, value := range plan {
			if key != "causal_simulation" {
				clone[key] = value
			}
		}
		working["chapter_plan"] = clone
		return true
	default:
		return false
	}
}

func deleteContextKey(result map[string]any, key string) {
	delete(result, key)
	for _, containerKey := range []string{
		"working_memory",
		"episodic_memory",
		"planning_memory",
		"foundation_memory",
		"reference_pack",
		"selected_memory",
	} {
		section, ok := result[containerKey].(map[string]any)
		if !ok {
			continue
		}
		delete(section, key)
	}
}

// buildRelatedChapters 根据结构化数据反查与当前章相关的历史章节。
// 从伏笔、角色出场、状态变化、关系四个维度推荐，去重后最多返回 5 条。
// 所有数据通过参数传入，不做额外 IO。
func (t *ContextTool) buildRelatedChapters(
	chapter int,
	entry *domain.OutlineEntry,
	foreshadow []domain.ForeshadowEntry,
	relationships []domain.RelationshipEntry,
	stateChanges []domain.StateChange,
) []domain.RelatedChapter {
	const recentWindow = 10
	const maxResults = 5

	seen := make(map[int]struct{})
	var results []domain.RelatedChapter
	add := func(ch int, reason string) {
		if ch <= 0 || ch >= chapter {
			return
		}
		// 最近几章太近，不推荐
		if ch > chapter-recentWindow {
			return
		}
		if _, ok := seen[ch]; ok {
			return
		}
		seen[ch] = struct{}{}
		results = append(results, domain.RelatedChapter{Chapter: ch, Reason: reason})
	}

	// 拼接大纲文本用于关键词匹配
	outlineText := entry.Title + " " + entry.CoreEvent
	for _, s := range entry.Scenes {
		outlineText += " " + s
	}

	// 1. 伏笔反查：活跃伏笔的描述是否与当前章大纲相关
	for _, f := range foreshadow {
		if strings.Contains(outlineText, f.ID) || containsAny(outlineText, strings.Fields(f.Description)) {
			add(f.PlantedAt, fmt.Sprintf("伏笔%s(%s)埋设章", f.ID, truncateRunes(f.Description, 15)))
		}
		if len(results) >= maxResults {
			break
		}
	}

	// 2. 角色出场反查：批量单次遍历，IO 从 O(角色数×章节数) 降为 O(章节数)
	chars, _ := t.store.Characters.Load()
	outlineChars := matchOutlineCharacters(outlineText, chars)
	if len(outlineChars) > 0 {
		appearances := t.store.Summaries.FindCharacterAppearances(outlineChars, chapter, recentWindow)
		for _, name := range outlineChars {
			if len(results) >= maxResults {
				break
			}
			if ch, ok := appearances[name]; ok {
				add(ch, fmt.Sprintf("角色'%s'最后出场章", name))
			}
		}
	}

	// 3. 状态变化反查：在已加载的 slice 上操作，零 IO
	for _, name := range outlineChars {
		if len(results) >= maxResults {
			break
		}
		ch := findLastStateChange(stateChanges, name, chapter)
		if ch > 0 && ch <= chapter-recentWindow {
			add(ch, fmt.Sprintf("'%s'状态变化章", name))
		}
	}

	// 4. 关系反查：当前章涉及的角色对之间关系最后变化
	if len(relationships) > 0 && len(outlineChars) >= 2 {
		charSet := make(map[string]struct{}, len(outlineChars))
		for _, c := range outlineChars {
			charSet[c] = struct{}{}
		}
		for _, r := range relationships {
			if len(results) >= maxResults {
				break
			}
			_, aIn := charSet[r.CharacterA]
			_, bIn := charSet[r.CharacterB]
			if aIn && bIn {
				add(r.Chapter, fmt.Sprintf("%s-%s关系变化", r.CharacterA, r.CharacterB))
			}
		}
	}

	return results
}

// findLastStateChange 在已加载的状态变化列表中查找实体最近一次变化的章节号。
func findLastStateChange(changes []domain.StateChange, entity string, currentChapter int) int {
	for i := len(changes) - 1; i >= 0; i-- {
		if changes[i].Entity == entity && changes[i].Chapter < currentChapter {
			return changes[i].Chapter
		}
	}
	return 0
}

// matchOutlineCharacters 从大纲文本中匹配出场角色名。
func matchOutlineCharacters(text string, chars []domain.Character) []string {
	var matched []string
	for _, c := range chars {
		if strings.Contains(text, c.Name) {
			matched = append(matched, c.Name)
			continue
		}
		for _, alias := range c.Aliases {
			if strings.Contains(text, alias) {
				matched = append(matched, c.Name)
				break
			}
		}
	}
	return matched
}

// containsAny 检查 text 是否包含 words 中的任一词（至少 2 字才匹配，避免噪音）。
func containsAny(text string, words []string) bool {
	for _, w := range words {
		if len([]rune(w)) >= 2 && strings.Contains(text, w) {
			return true
		}
	}
	return false
}

func (t *ContextTool) selectStoryThreads(state contextBuildState) []domain.RecallItem {
	if state.currentEntry == nil {
		return nil
	}
	if len(state.foreshadow) < storyThreadRecallThreshold {
		return nil
	}

	const maxThreads = 5
	var items []domain.RecallItem
	seen := make(map[string]struct{})
	picked := make(map[string]struct{}) // 已选中的伏笔 ID，供账龄回填去重
	add := func(item domain.RecallItem) {
		key := item.Kind + "|" + item.Key + "|" + item.Summary
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		picked[item.Key] = struct{}{}
		items = append(items, item)
	}

	// 1. 相关性召回：与当前章 focus 词重叠的伏笔。
	focusTerms := recallFocusTerms(state.currentEntry, state.chapterPlan)
	focusText := strings.Join(focusTerms, " ")
	for _, entry := range state.foreshadow {
		if !matchesRecallTerms(entry.ID+" "+entry.Description, focusTerms) && !strings.Contains(focusText, entry.ID) {
			continue
		}
		add(domain.RecallItem{
			Kind:    "story_thread",
			Key:     entry.ID,
			Chapter: entry.PlantedAt,
			Reason:  "当前章可能需要承接既有伏笔",
			Summary: fmt.Sprintf("伏笔“%s”埋于第%d章：%s", entry.ID, entry.PlantedAt, truncateRunes(entry.Description, 30)),
		})
		if len(items) >= maxThreads {
			return items
		}
	}

	// 2. 账龄回填：与当前章无关、但久挂未回收的伏笔（最旧优先），补足剩余名额。
	//    补的是相关性召回天然的盲区——独自悬挂太久、却没在本章撞上关键词的那根线。
	for _, entry := range agingForeshadow(state.foreshadow, state.chapter, picked) {
		add(domain.RecallItem{
			Kind:    "story_thread",
			Key:     entry.ID,
			Chapter: entry.PlantedAt,
			Reason:  "伏笔久挂未回收，注意适时推进或回收",
			Summary: fmt.Sprintf("伏笔“%s”埋于第%d章，已 %d 章未回收：%s", entry.ID, entry.PlantedAt, state.chapter-entry.PlantedAt, truncateRunes(entry.Description, 30)),
		})
		if len(items) >= maxThreads {
			break
		}
	}

	return items
}

// agingForeshadow 返回账龄 ≥ foreshadowAgingChapters 的未回收伏笔，按最旧优先排序，
// 跳过 picked 中已被相关性召回选中的。入参 all 已是 active（未回收）列表，故无需再过滤状态。
func agingForeshadow(all []domain.ForeshadowEntry, chapter int, picked map[string]struct{}) []domain.ForeshadowEntry {
	var aging []domain.ForeshadowEntry
	for _, e := range all {
		if _, ok := picked[e.ID]; ok {
			continue
		}
		if e.PlantedAt <= 0 || chapter-e.PlantedAt < foreshadowAgingChapters {
			continue
		}
		aging = append(aging, e)
	}
	sort.SliceStable(aging, func(i, j int) bool {
		return aging[i].PlantedAt < aging[j].PlantedAt
	})
	return aging
}

func (t *ContextTool) selectReviewLessons(chapter int, warn func(string, error)) []domain.RecallItem {
	if chapter <= 1 {
		return nil
	}

	var items []domain.RecallItem
	seen := make(map[string]struct{})
	add := func(item domain.RecallItem) {
		key := item.Summary
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		items = append(items, item)
	}

	appendReview := func(review *domain.ReviewEntry) bool {
		if review == nil {
			return false
		}
		for i, miss := range review.ContractMisses {
			add(domain.RecallItem{
				Kind:    "review_lesson",
				Key:     fmt.Sprintf("review-%d-contract-%d", review.Chapter, i),
				Chapter: review.Chapter,
				Reason:  "最近审阅指出 contract 漏项",
				Summary: fmt.Sprintf("第%d章 contract 漏项：%s", review.Chapter, miss),
			})
			if len(items) >= 3 {
				return true
			}
		}
		for i, issue := range review.Issues {
			switch issue.Severity {
			case "", "warning", "error", "critical":
				add(domain.RecallItem{
					Kind:    "review_lesson",
					Key:     fmt.Sprintf("review-%d-issue-%d", review.Chapter, i),
					Chapter: review.Chapter,
					Reason:  "最近审阅指出需要避免重复问题",
					Summary: fmt.Sprintf("第%d章审阅提醒：%s", review.Chapter, truncateRunes(issue.Description, 36)),
				})
			}
			if len(items) >= 3 {
				return true
			}
		}
		return false
	}

	for ch := chapter - 1; ch >= max(chapter-3, 1); ch-- {
		review, err := t.store.World.LoadReview(ch)
		if err != nil {
			warn("review", err)
			continue
		}
		if appendReview(review) {
			return items
		}
	}

	globalReview, err := t.store.World.LoadLastReview(chapter - 1)
	if err != nil {
		warn("global_review", err)
	} else if appendReview(globalReview) {
		return items
	}
	return items
}

func recallFocusTerms(entry *domain.OutlineEntry, plan *domain.ChapterPlan) []string {
	if entry == nil {
		return nil
	}
	var terms []string
	add := func(v string) {
		v = strings.TrimSpace(v)
		if v != "" {
			terms = append(terms, v)
		}
	}

	add(entry.Title)
	add(entry.CoreEvent)
	add(entry.Hook)
	for _, scene := range entry.Scenes {
		add(scene)
	}
	if plan != nil {
		add(plan.Goal)
		add(plan.Hook)
		for _, point := range plan.Contract.PayoffPoints {
			add(point)
		}
		add(plan.Contract.HookGoal)
		for _, anchor := range plan.Contract.SceneAnchors {
			add(anchor)
		}
	}
	return terms
}

func matchesRecallTerms(text string, terms []string) bool {
	for _, term := range terms {
		term = strings.TrimSpace(term)
		if len([]rune(term)) < 2 {
			continue
		}
		if strings.Contains(text, term) || strings.Contains(term, text) {
			return true
		}
		if hasMeaningfulOverlap(term, text) {
			return true
		}
	}
	return false
}

func hasMeaningfulOverlap(a, b string) bool {
	ar := []rune(strings.TrimSpace(a))
	br := []rune(strings.TrimSpace(b))
	if len(ar) < 5 || len(br) < 5 {
		return false
	}
	shorter := len(ar)
	if len(br) < shorter {
		shorter = len(br)
	}
	threshold := 5
	switch {
	case shorter >= 12:
		threshold = 7
	case shorter >= 9:
		threshold = 6
	}
	return longestCommonSubstringRunes(ar, br) >= threshold
}

const storyThreadRecallThreshold = 6
const storyThreadRecallMinSelected = 2

// foreshadowAgingChapters：一条伏笔自埋设起超过这么多章仍未回收，视为"久挂"。
// 这类伏笔即使与当前章关键词无关，也回填进 story_threads，避免长篇里被彻底遗忘
// （相关性召回天然只看见与本章相关的线，看不见独自悬挂太久的那根）。
// 账龄是纯代码派生的事实（当前章 - 埋设章），只陈述"已挂 N 章未回收"，不下指令。
const foreshadowAgingChapters = 30

func longestCommonSubstringRunes(a, b []rune) int {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	prev := make([]int, len(b)+1)
	best := 0
	for i := 1; i <= len(a); i++ {
		curr := make([]int, len(b)+1)
		for j := 1; j <= len(b); j++ {
			if a[i-1] != b[j-1] {
				continue
			}
			curr[j] = prev[j-1] + 1
			if curr[j] > best {
				best = curr[j]
			}
		}
		prev = curr
	}
	return best
}

// truncateRunes 截断字符串到指定 rune 数。
func truncateRunes(s string, maxRunes int) string {
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes]) + "..."
}
