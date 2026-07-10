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
	"time"

	"github.com/chenhongyang/novel-studio/internal/aigc"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/rag"
	"github.com/chenhongyang/novel-studio/internal/rules"
	"github.com/chenhongyang/novel-studio/internal/stylestat"
)

type contextBuildState struct {
	chapter             int
	profile             domain.ContextProfile
	progress            *domain.Progress
	runMeta             *domain.RunMeta
	currentEntry        *domain.OutlineEntry
	chapterPlan         *domain.ChapterPlan
	chapterParticipants []string
	storyThreads        []domain.RecallItem
	foreshadow          []domain.ForeshadowEntry
	relationships       []domain.RelationshipEntry
	allStateChanges     []domain.StateChange
	styleRules          *domain.WritingStyleRules
	writingEngine       *domain.WritingCompiled
	bookWorld           *domain.BookWorld
}

type chapterContextEnvelope struct {
	Working    map[string]any
	Episodic   map[string]any
	References map[string]any
	Selected   map[string]any
}

type architectContextEnvelope struct {
	Planning   map[string]any
	Foundation map[string]any
	References map[string]any
}

type mechanicalGateReviewPayload struct {
	Chapter        int               `json:"chapter"`
	AIGCReport     aigc.Report       `json:"aigc_report"`
	RuleViolations []rules.Violation `json:"rule_violations"`
	GeneratedAt    string            `json:"generated_at,omitempty"`
}

func newChapterContextEnvelope() chapterContextEnvelope {
	return chapterContextEnvelope{
		Working:    make(map[string]any),
		Episodic:   make(map[string]any),
		References: make(map[string]any),
		Selected:   make(map[string]any),
	}
}

func newArchitectContextEnvelope() architectContextEnvelope {
	return architectContextEnvelope{
		Planning:   make(map[string]any),
		Foundation: make(map[string]any),
		References: make(map[string]any),
	}
}

func (e chapterContextEnvelope) apply(result map[string]any) {
	// 合并而非替换：Execute 的章节路径会先后 apply 两个信封（seed + buildChapterContext），
	// 整体赋值会让第二次 apply 丢弃 seed 的容器内容，working_memory.* 等 canonical
	// 路径随之失效（prompt 指针指向空气，模型只能靠顶层镜像模糊容错）。
	mergeEnvelopeSection(result, "working_memory", e.Working)
	mergeEnvelopeSection(result, "episodic_memory", e.Episodic)
	mergeEnvelopeSection(result, "reference_pack", e.References)
	if len(e.Selected) > 0 {
		mergeEnvelopeSection(result, "selected_memory", e.Selected)
	}
	mergeContextSection(result, e.Working)
	mergeContextSection(result, e.Episodic)
	mergeContextSection(result, e.References)
}

// mergeEnvelopeSection 把 section 合并进 result[key] 的既有容器；容器不存在时直接挂载。
func mergeEnvelopeSection(result map[string]any, key string, section map[string]any) {
	if existing, ok := result[key].(map[string]any); ok {
		for k, v := range section {
			existing[k] = v
		}
		return
	}
	result[key] = section
}

func (e architectContextEnvelope) apply(result map[string]any) {
	result["planning_memory"] = e.Planning
	result["foundation_memory"] = e.Foundation
	result["reference_pack"] = e.References
	mergeContextSection(result, e.Planning)
	mergeContextSection(result, e.Foundation)
	mergeContextSection(result, e.References)
}

func mergeContextSection(result map[string]any, section map[string]any) {
	for key, value := range section {
		result[key] = value
	}
}

// buildProgressStatus 仅在 Coordinator 调用（不传 chapter）时返回进度摘要,
// Writer 不需要这些信息,避免干扰写作。
func (t *ContextTool) buildProgressStatus(result map[string]any) {
	progress, err := t.store.Progress.Load()
	if err != nil || progress == nil {
		return
	}
	status := map[string]any{
		"phase":              string(progress.Phase),
		"flow":               string(progress.Flow),
		"completed_chapters": len(progress.CompletedChapters),
		"total_chapters":     progress.TotalChapters,
		"next_chapter":       progress.NextChapter(),
		"total_word_count":   progress.TotalWordCount,
	}
	if progress.InProgressChapter > 0 {
		status["in_progress_chapter"] = progress.InProgressChapter
	}
	if len(progress.PendingRewrites) > 0 {
		status["pending_rewrites"] = progress.PendingRewrites
		status["rewrite_reason"] = progress.RewriteReason
	}
	if progress.Layered {
		status["layered"] = true
		status["current_volume"] = progress.CurrentVolume
		status["current_arc"] = progress.CurrentArc
	}
	if progress.Phase == domain.PhaseComplete {
		status["finished"] = true
	}
	result["progress_status"] = status
}

// buildUserRules 把合并后的 Bundle 注入 working_memory.user_rules（canonical 路径）。
//
// 单点注入：writer / editor / architect / coordinator 任一路径调用 novel_context
// 都能在 working_memory.user_rules 拿到一致的偏好。architect 路径原本没有 working_memory，
// 由本函数按需新建（仅装 user_rules）；chapter > 0 路径下 working_memory 已存在，直接嵌入。
//
// 即便 Bundle 为空也注入，保持字段稳定，避免 LLM 看到 user_rules=null 而走异常分支。
//
// 注入策略：只给 LLM 看 structured + preferences——这两项才是创作时需要遵循的偏好。
// sources / conflicts 是诊断信息（用户冲突排查），不进 LLM；由 CLI 启动诊断面板按需展示。
func (t *ContextTool) buildUserRules(result map[string]any) {
	snap, err := t.store.UserRules.Load()
	if err != nil || snap == nil {
		// 快照未生成（老书首次/异常）：退到代码内置默认，保证机械底线（字数/禁语/疲劳词）始终存在。
		def := rules.BuildSnapshot([]rules.Candidate{rules.SystemDefaults()})
		snap = &def
	}
	working, ok := result["working_memory"].(map[string]any)
	if !ok {
		working = map[string]any{}
		result["working_memory"] = working
	}
	working["user_rules"] = snap.Payload()
}

func (t *ContextTool) buildSimulationProfile(result map[string]any, sectionKey string, warn func(string, error)) {
	profile, err := t.store.Simulation.Load()
	if err != nil {
		warn("simulation_profile", err)
		return
	}
	compact := domain.CompactSimulationProfile(profile)
	if compact == nil {
		return
	}
	section, ok := result[sectionKey].(map[string]any)
	if !ok {
		section = map[string]any{}
		result[sectionKey] = section
	}
	section["simulation_profile"] = compact
	result["simulation_profile"] = true
}

func (t *ContextTool) buildBaseContext(result map[string]any, warn func(string, error)) {
	if premise, err := t.store.Outline.LoadPremise(); err == nil && premise != "" {
		result["premise"] = premise
		if sections := parsePremiseSections(premise); len(sections) > 0 {
			result["premise_sections"] = sections
		}
		tier := domain.PlanningTier("")
		if meta, err := t.store.RunMeta.Load(); err == nil && meta != nil {
			tier = meta.PlanningTier
		}
		result["premise_structure"] = premiseStructure(premise, tier)
	} else {
		warn("premise", err)
	}
	if outline, err := t.store.Outline.LoadOutline(); err == nil && outline != nil {
		result["outline"] = outline
	} else {
		warn("outline", err)
	}
	if rules, err := t.store.World.LoadWorldRules(); err == nil && len(rules) > 0 {
		result["world_rules"] = orderWorldRulesByVisibility(rules)
		if hasInformalOrSecretRules(rules) {
			result["world_rules_usage"] = "world_rules 按 formal→informal→secret 排序：formal 是制度显规则；informal 是潜规则，应通过人物行为体现、不要旁白直述；secret 仅供你推演，正文不得明写"
		}
	} else {
		warn("world_rules", err)
	}
	if world, err := t.store.World.LoadBookWorld(); err == nil && world != nil {
		result["book_world"] = world
	} else {
		warn("book_world", err)
	}
}

func (t *ContextTool) prepareChapterContext(chapter int, envelope *chapterContextEnvelope, warn func(string, error)) contextBuildState {
	state := contextBuildState{
		chapter: chapter,
		profile: domain.NewContextProfile(0),
	}

	progress, err := t.store.Progress.Load()
	warn("progress", err)
	runMeta, err := t.store.RunMeta.Load()
	warn("run_meta", err)
	state.progress = progress
	state.runMeta = runMeta
	isRewrite := progress != nil && slices.Contains(progress.PendingRewrites, chapter)

	if runMeta != nil && runMeta.PlanningTier != "" {
		envelope.Episodic["planning_tier"] = runMeta.PlanningTier
	}
	if progress != nil && progress.TotalChapters > 0 {
		state.profile = domain.NewContextProfile(progress.TotalChapters)
	}
	if progress == nil || !progress.Layered {
		state.profile.Layered = false
	}

	currentEntry, currentEntryErr := t.store.Outline.GetChapterOutline(chapter)
	if currentEntryErr == nil {
		envelope.Working["current_chapter_outline"] = currentEntry
	} else {
		warn("current_chapter_outline", currentEntryErr)
	}
	state.currentEntry = currentEntry

	partialPlan, partialPlanErr := t.store.Drafts.LoadChapterPlanPartial(chapter)
	if partialPlanErr != nil {
		warn("chapter_plan_partial", partialPlanErr)
	}
	chapterPlan, chapterPlanErr := t.store.Drafts.LoadChapterPlan(chapter)
	if partialPlan != nil {
		// 两阶段规划已开始时，正式 plan 仍是上一轮旧版本。把它注入会让 Writer
		// 同时看到“旧正式计划”和“当前 staged plan”，在返工场景尤其容易误判目标。
		// partial 的完整内容由 plan_structure/plan_details 在工具侧持有，这里只暴露
		// 阶段状态；收口为正式 plan 后下一次 novel_context 会自然恢复完整计划。
		causalFields := make([]string, 0)
		if causal, ok := partialPlan["causal_simulation"].(map[string]any); ok {
			for key := range causal {
				causalFields = append(causalFields, key)
			}
			sort.Strings(causalFields)
		}
		envelope.Working["chapter_plan_stage"] = map[string]any{
			"status":                "partial",
			"chapter":               chapter,
			"causal_fields_present": causalFields,
			"policy":                "当前 staged plan 是唯一规划真相；不要读取或复述上一轮正式 plan。继续 plan_details 补最小缺项并 finalize，禁止转去下一章。",
		}
		chapterPlan = nil
	} else if chapterPlanErr == nil && chapterPlan != nil && isRewrite && chapterArtifactNotNewerThanFinal(t.store.Dir(), chapter, fmt.Sprintf("drafts/%02d.plan.json", chapter)) {
		envelope.Working["chapter_plan_stage"] = map[string]any{
			"status":  "stale_for_rewrite",
			"chapter": chapter,
			"policy":  "旧正式 plan 的修改时间不晚于待返工终稿，不能作为本轮规划事实；请按 current_chapter_outline 与 rewrite_brief 重新 plan_structure。",
		}
		chapterPlan = nil
	} else if chapterPlanErr == nil && chapterPlan != nil {
		envelope.Working["chapter_plan"] = chapterPlan
		if len(chapterPlan.Contract.RequiredBeats) > 0 ||
			len(chapterPlan.Contract.ForbiddenMoves) > 0 ||
			len(chapterPlan.Contract.ContinuityChecks) > 0 ||
			len(chapterPlan.Contract.EvaluationFocus) > 0 ||
			chapterPlan.Contract.EmotionTarget != "" ||
			len(chapterPlan.Contract.PayoffPoints) > 0 ||
			chapterPlan.Contract.HookGoal != "" ||
			len(chapterPlan.Contract.SceneAnchors) > 0 {
			envelope.Working["chapter_contract"] = chapterPlan.Contract
		}
		if hasChapterCausalSimulation(chapterPlan.CausalSimulation) {
			envelope.Working["causal_simulation"] = chapterPlan.CausalSimulation
			envelope.Working["causal_simulation_policy"] = "causal_simulation 是原章节计划的同源增强：必须服从 current_chapter_outline、chapter_contract、progression_snapshot、project_progress、resource_audit、user_rules 和 writing_engine；voice_logic 用于约束人物声口和说话逻辑；review_refinement 用于审核失败后的反馈来源、局部目标、保留约束、验收条件和停止条件；正文仍按原 plan->draft->check->commit 逻辑产出，返工章必须先吸收 rewrite_brief 审核结论再重建推演，不能另起大纲或绕开契约。"
		}
	} else if chapterPlanErr != nil {
		warn("chapter_plan", chapterPlanErr)
	}
	state.chapterPlan = chapterPlan
	state.chapterParticipants = t.detectChapterParticipants(currentEntry, chapterPlan, warn)
	if len(state.chapterParticipants) > 0 {
		envelope.Working["chapter_participants"] = state.chapterParticipants
		envelope.Working["character_context_policy"] = "characters/character_snapshots 已按本章参与者筛选；未进入列表的角色不要塞入本章正文"
	}

	t.buildMethodologyContext(envelope.Working)
	t.buildHorizonEvents(envelope.Working, chapter, warn)

	// 暴露 draft 是否已存在的事实：让 writer 被重派时能自行判断跳过重写还是覆盖。
	// 只暴露 exists + word_count，不注入正文（正文让 writer 按需用 read_chapter 拉）。
	if _, draftWords, draftErr := t.store.Drafts.LoadChapterContent(chapter); draftErr == nil && draftWords > 0 {
		if isRewrite && chapterArtifactNotNewerThanFinal(t.store.Dir(), chapter, fmt.Sprintf("drafts/%02d.draft.md", chapter)) {
			envelope.Working["chapter_draft_stage"] = map[string]any{
				"status":     "stale_for_rewrite",
				"word_count": draftWords,
				"policy":     "旧 draft 的修改时间不晚于待返工终稿；重规划阶段只用 rewrite_brief，正式 plan 收口后才可由 Drafter 按需读取旧终稿，本轮必须生成新 draft 覆盖它。",
			}
		} else {
			envelope.Working["chapter_draft"] = map[string]any{
				"exists":     true,
				"word_count": draftWords,
			}
		}
	} else if draftErr != nil {
		warn("chapter_draft", draftErr)
	}
	if parts, partsErr := t.store.Drafts.LoadDraftPartIndex(chapter); partsErr == nil && parts != nil && len(parts.Parts) > 0 {
		totalParts := 0
		for _, part := range parts.Parts {
			if part.TotalParts > totalParts {
				totalParts = part.TotalParts
			}
			if part.Part > totalParts {
				totalParts = part.Part
			}
		}
		envelope.Working["chapter_draft_parts"] = map[string]any{
			"exists":       true,
			"index":        parts,
			"missing":      missingDraftParts(parts, totalParts),
			"next_step":    "若整章草稿尚未存在，继续补齐 missing 分片；所有分片完成后调用 merge_chapter_parts，再 read_chapter(source=draft) + check_consistency + commit_chapter。",
			"read_policy":  "需要查看某片正文时调用 read_chapter(source=draft_part, chapter=N, part=K)；不要要求 novel_context 注入全文分片。",
			"merge_policy": "分片只降低写作窗口压力，不替代整章审核；合并后必须按完整章节重新自审。",
		}
	} else if partsErr != nil {
		warn("chapter_draft_parts", partsErr)
	}

	if analysis, aiErr := t.store.AIVoice.LoadRedFlags(chapter); aiErr == nil && analysis != nil {
		envelope.Working["ai_voice_redflags"] = analysis
		envelope.Working["ai_voice_redflags_policy"] = "Editor 必须先读此 JSON；ai_voice_detection 维度必须逐项引用其中的数值、阈值和命中句。"
	} else if aiErr != nil {
		warn("ai_voice_redflags", aiErr)
	}
	if metrics, metricErr := t.store.AIVoice.LoadChapterMetrics(chapter); metricErr == nil && metrics != nil {
		envelope.Working["chapter_ai_voice_metrics"] = metrics
	} else if metricErr != nil {
		warn("chapter_ai_voice_metrics", metricErr)
	}

	// 重写时把"为什么改 + 改哪里"交给 writer：理由来自返工队列，具体批评来自本章评审
	// （selectReviewLessons 只召回 chapter-1..chapter-3，恰好漏掉本章本身，writer 又无读评审的工具）。
	// 正文不在此注入——保持"正文按需 read_chapter 拉"的约定不破。
	// Editor 复审回归验证：本章已有上一轮审阅结论（且当前不在重写队列 = 复审语境）时，
	// 把上轮 verdict/issues 注入，要求先逐条验证是否已修复，防"修了 A 换挑 B"的 issue 循环。
	if !isRewrite {
		if prev, prevErr := t.store.World.LoadReview(chapter); prevErr == nil && prev != nil && prev.Scope == "chapter" && prev.Verdict != "accept" {
			prevBrief := map[string]any{"verdict": prev.Verdict}
			if prev.Summary != "" {
				prevBrief["summary"] = prev.Summary
			}
			if len(prev.Issues) > 0 {
				prevBrief["issues"] = prev.Issues
			}
			if rounds := t.store.World.LoadReviewHistory(chapter); len(rounds) > 0 {
				prevBrief["earlier_rounds"] = len(rounds)
			}
			envelope.Working["previous_review"] = prevBrief
			envelope.Working["previous_review_policy"] = "本章为复审：先逐条验证 previous_review.issues 是否已修复并在本轮 review 中给出已修复/未修复结论；不要把同一问题换措辞重复开新 issue；新问题按正常标准判，但标准须与首轮一致，不得逐轮加码"
		}
	}

	if isRewrite {
		brief := map[string]any{"reason": progress.RewriteReason}
		brief["slop_avoidlist"] = aigc.LexiconDigest(6)
		brief["slop_policy"] = "返工时先对照 mechanical_gate 的命中明细逐条清除，再全篇规避 slop_avoidlist 词群；不要用同义改写规避检测（同族变体也计数）"
		if review, reviewErr := t.store.World.LoadReview(chapter); reviewErr == nil && review != nil {
			if review.Summary != "" {
				brief["review_summary"] = review.Summary
			}
			if len(review.Issues) > 0 {
				brief["issues"] = review.Issues
			}
			if len(review.ContractMisses) > 0 {
				brief["contract_misses"] = review.ContractMisses
			}
		} else if reviewErr != nil {
			warn("rewrite_review", reviewErr)
		}
		if gate, gateErr := t.loadMechanicalGateBrief(chapter); gateErr == nil && gate != nil {
			brief["mechanical_gate"] = gate
		} else if gateErr != nil {
			warn("mechanical_gate", gateErr)
		}
		if analysis, aiErr := t.store.AIVoice.LoadRedFlags(chapter); aiErr == nil && analysis != nil {
			brief["ai_voice_redflags"] = analysis
		} else if aiErr != nil {
			warn("rewrite_ai_voice_redflags", aiErr)
		}
		envelope.Working["rewrite_brief"] = brief
		if source, body, markdown, sourceErr := loadChapterRewriteSource(t.store, chapter); sourceErr == nil && source != nil {
			envelope.Working["rewrite_source"] = rewriteSourceContext(source, body, markdown)
		} else if sourceErr != nil {
			warn("rewrite_source", sourceErr)
		}
	}

	foreshadow, foreshadowErr := t.store.World.LoadActiveForeshadow()
	warn("foreshadow_ledger", foreshadowErr)
	state.foreshadow = foreshadow

	relationships, relErr := t.store.World.LoadRelationships()
	warn("relationship_state", relErr)
	if len(relationships) > 0 {
		envelope.Episodic["relationship_state"] = relationships
	}
	state.relationships = relationships

	allStateChanges, scErr := t.store.World.LoadStateChanges()
	warn("recent_state_changes", scErr)
	state.allStateChanges = allStateChanges
	if len(allStateChanges) > 0 {
		start := max(chapter-2, 1)
		var recent []domain.StateChange
		for _, c := range allStateChanges {
			if c.Chapter >= start && c.Chapter < chapter {
				recent = append(recent, c)
			}
		}
		if len(recent) > 0 {
			envelope.Episodic["recent_state_changes"] = recent
		}
	}

	styleRules, styleErr := t.store.World.LoadStyleRules()
	warn("style_rules", styleErr)
	state.styleRules = styleRules
	state.writingEngine = t.loadWritingEngine(styleRules, state.writingBindingScope(), warn)
	if world, err := t.store.World.LoadBookWorld(); err == nil && world != nil {
		state.bookWorld = world
	} else {
		warn("book_world", err)
	}
	state.storyThreads = t.selectStoryThreads(state)
	if len(state.storyThreads) > 0 && len(state.storyThreads) < storyThreadRecallMinSelected {
		state.storyThreads = nil
	}

	return state
}

func chapterArtifactNotNewerThanFinal(dir string, chapter int, artifact string) bool {
	artifactInfo, err := os.Stat(filepath.Join(dir, filepath.FromSlash(artifact)))
	if err != nil {
		return false
	}
	finalInfo, err := os.Stat(filepath.Join(dir, "chapters", fmt.Sprintf("%02d.md", chapter)))
	if err != nil {
		return false
	}
	return !artifactInfo.ModTime().After(finalInfo.ModTime())
}

func hasChapterCausalSimulation(sim domain.ChapterCausalSimulation) bool {
	return sim.WorldSimulationID != "" ||
		sim.ProtagonistDecision != "" ||
		sim.ProjectPromise != "" ||
		sim.ChapterFunction != "" ||
		len(sim.ContextSources) > 0 ||
		len(sim.WritingNorms) > 0 ||
		hasAntiAIExecutionPlan(sim.AntiAIPlan) ||
		len(sim.ExternalRefs) > 0 ||
		len(sim.TrendLanguage) > 0 ||
		domain.CompleteReaderEntertainmentPlan(sim.EntertainmentPlan) ||
		len(sim.GroundingDetails) > 0 ||
		len(sim.OffscreenStage) > 0 ||
		hasLongformOpeningDesign(sim.LongformOpening) ||
		len(sim.CharacterArcTests) > 0 ||
		hasReaderRewardPlan(sim.ReaderRewardPlan) ||
		hasReaderRetentionPlan(sim.ReaderRetentionPlan) ||
		len(sim.EvidenceChains) > 0 ||
		hasEndingConsequenceContract(sim.EndingContract) ||
		len(sim.DormantPolicy) > 0 ||
		len(sim.RealitySupport) > 0 ||
		len(sim.EmotionalLogic) > 0 ||
		len(sim.RelationshipArcs) > 0 ||
		len(sim.VisualDesign) > 0 ||
		hasWorldBackgroundLayers(sim.WorldLayers) ||
		len(sim.InformationLedger) > 0 ||
		len(sim.HiddenRules) > 0 ||
		len(sim.SocialMoodRumors) > 0 ||
		len(sim.RitualCalendar) > 0 ||
		len(sim.StructuralResources) > 0 ||
		len(sim.CosmologyChecks) > 0 ||
		len(sim.ConflictWeb) > 0 ||
		hasNarrativeTensionMatrix(sim.TensionMatrix) ||
		len(sim.InitialState) > 0 ||
		len(sim.VoiceLogic) > 0 ||
		len(sim.DialogueBlueprints) > 0 ||
		len(sim.CrowdRoles) > 0 ||
		hasReviewRefinementLoop(sim.ReviewRefinement) ||
		len(sim.EnvironmentState) > 0 ||
		len(sim.WorldRulesInForce) > 0 ||
		len(sim.InformationGaps) > 0 ||
		len(sim.CausalBeats) > 0 ||
		len(sim.DecisionPoints) > 0 ||
		len(sim.OutcomeShift) > 0 ||
		len(sim.SceneConstraints) > 0
}

func hasAntiAIExecutionPlan(plan domain.AntiAIExecutionPlan) bool {
	return len(plan.RiskSignals) > 0 ||
		len(plan.CounterMoves) > 0 ||
		plan.SentenceRhythmPolicy != "" ||
		plan.ObjectResponseBudget != "" ||
		plan.DialogueFunctionPlan != "" ||
		len(plan.ReviewChecks) > 0
}

func hasReviewRefinementLoop(loop domain.ReviewRefinementLoop) bool {
	return len(loop.TriggerSources) > 0 ||
		len(loop.FailureModes) > 0 ||
		len(loop.LocalizedTargets) > 0 ||
		len(loop.PreserveConstraints) > 0 ||
		len(loop.ReplanningMoves) > 0 ||
		len(loop.AcceptanceChecks) > 0 ||
		loop.StopCondition != "" ||
		loop.IterationLimit > 0
}

func hasLongformOpeningDesign(opening domain.LongformOpeningDesign) bool {
	return opening.TargetReader != "" ||
		opening.OpeningHook != "" ||
		opening.SerialEngine != "" ||
		len(opening.ReaderRewardLoop) > 0 ||
		len(opening.LongRangePromises) > 0 ||
		len(opening.RevealBudget) > 0 ||
		len(opening.FirstChapterProof) > 0 ||
		len(opening.RetentionRisks) > 0
}

func (t *ContextTool) buildChapterContext(ctx context.Context, result map[string]any, state contextBuildState, warn func(string, error)) {
	envelope := newChapterContextEnvelope()
	result["memory_policy"] = domain.NewChapterMemoryPolicy(state.progress, state.profile, state.currentEntry != nil)

	if state.profile.Layered {
		t.loadLayeredCharacters(envelope.Episodic, state.chapter, state.chapterParticipants, warn)
	} else {
		t.loadFilteredCharacters(envelope.Episodic, state.chapter, state.chapterParticipants, warn)
	}

	t.buildChapterEpisodicMemory(&envelope, state, warn)
	t.buildChapterWorkingMemory(&envelope, state, warn)
	t.buildChapterReferencePack(&envelope, state, warn)
	t.buildChapterSelectedMemory(ctx, &envelope, state, warn)
	t.buildStyleStats(&envelope, state)
	envelope.apply(result)
}

// buildStyleStats 对全部已完成章节做全书级风格统计，注入 episodic_memory.style_stats。
// 弧内评审窗口对"章均几十次的句式 tic、章末形态同构、跨章复读"天然失明，只有
// 全书统计能暴露——统计归代码（确定性），裁定归 LLM（editor 在 aesthetic 维度
// 按数字判分，writer 据此自避免）。章数不足时 stylestat 返回 nil，不注入。
func (t *ContextTool) buildStyleStats(envelope *chapterContextEnvelope, state contextBuildState) {
	if state.progress == nil || len(state.progress.CompletedChapters) == 0 {
		return
	}
	completed := slices.Clone(state.progress.CompletedChapters)
	slices.Sort(completed)
	chapters := make([]string, 0, len(completed))
	for _, ch := range completed {
		// 个别章读取失败跳过：统计是 best-effort 事实，不因单章缺失放弃全书视野
		if text, err := t.store.Drafts.LoadChapterText(ch); err == nil && text != "" {
			chapters = append(chapters, text)
		}
	}

	var titles []string
	if outline, err := t.store.Outline.LoadOutline(); err == nil {
		for _, entry := range outline {
			titles = append(titles, entry.Title)
		}
	}

	stats := stylestat.Compute(stylestat.Input{
		Chapters:  chapters,
		Titles:    titles,
		Stopwords: t.styleStopwords(),
	})
	if stats == nil {
		return
	}
	envelope.Episodic["style_stats"] = stats
}

// styleStopwords 收集角色名与别名供短语挖掘过滤——出场人名天然高频，不是文风问题。
func (t *ContextTool) styleStopwords() []string {
	var words []string
	if chars, err := t.store.Characters.Load(); err == nil {
		for _, c := range chars {
			words = append(words, c.Name)
			words = append(words, c.Aliases...)
		}
	}
	if cast, err := t.store.Cast.RecentActive(50); err == nil {
		for _, e := range cast {
			words = append(words, e.Name)
			words = append(words, e.Aliases...)
		}
	}
	return words
}

func (t *ContextTool) buildChapterWorkingMemory(envelope *chapterContextEnvelope, state contextBuildState, warn func(string, error)) {
	restartPolicy, restartErr := t.store.LoadSimulationRestartPolicy()
	if restartErr == nil && restartPolicy != nil && restartPolicy.Active {
		envelope.Working["simulation_restart_policy"] = restartPolicy
		envelope.Working["simulation_restart_policy_note"] = "本书正在按新的 generation_id 从第1章重新推演；旧章节、旧资源账和旧人物经历只能作为背景种子，不能当作新正文已发生事实。"
	} else if restartErr != nil {
		warn("simulation_restart_policy", restartErr)
	}
	suppressLegacyState := shouldSuppressLegacyStateForRestart(restartPolicy, state.progress)
	isRewriteTarget := state.progress != nil && slices.Contains(state.progress.PendingRewrites, state.chapter)
	if suppressLegacyState {
		envelope.Working["legacy_state_policy"] = "检测到 simulation_restart_policy 与 progress.generation_id 不一致；已压制旧 chapter_progress/project_progress/evolution_report/character_continuity，避免旧章节事实污染新推演。正式生成前应先运行 --zero-init --reset-simulation-state。"
	} else {
		if ledger, err := t.store.LoadChapterProgressLedger(); err == nil && ledger != nil {
			snapshot := compactProgressionSnapshot(ledger, state.chapter)
			if isRewriteTarget {
				delete(snapshot, "next_plan")
				delete(snapshot, "next_plan_warning")
				snapshot["active_rewrite_chapter"] = state.chapter
				snapshot["usage"] = "当前任务是返工 active_rewrite_chapter；只用 recent_entries 核对返工前事实。下一新章计划已暂时隐藏，待返工提交并通过审核后再读取。"
			}
			envelope.Working["progression_snapshot"] = snapshot
		} else {
			warn("chapter_progress", err)
		}
		if ledger, err := t.store.LoadProjectProgressLedger(); err == nil && ledger != nil {
			snapshot := compactProjectProgressSnapshot(ledger, state.chapter)
			if isRewriteTarget {
				delete(snapshot, "next_chapter_actions")
				snapshot["active_rewrite_chapter"] = state.chapter
				snapshot["usage"] = "当前任务是返工 active_rewrite_chapter；项目级未来动作暂时隐藏，只保留范围、承诺、资源、伏笔和关系连续性约束。"
			}
			envelope.Working["project_progress"] = snapshot
		} else {
			warn("project_progress", err)
		}
		if report, err := t.store.LoadEvolutionReport(); err == nil && report != nil {
			envelope.Working["evolution_report"] = compactEvolutionReportSnapshot(report)
		} else {
			warn("evolution_report", err)
		}
		if ledger, err := t.store.LoadCharacterContinuityLedger(); err == nil && ledger != nil {
			snapshot := compactCharacterContinuitySnapshot(ledger, state.chapter)
			if isRewriteTarget {
				delete(snapshot, "next_chapter_focus")
				snapshot["active_rewrite_chapter"] = state.chapter
				snapshot["usage"] = "当前任务是返工 active_rewrite_chapter；按既有人物状态与知识边界修复本章，下一新章焦点暂时隐藏。"
			}
			envelope.Working["character_continuity"] = snapshot
		} else {
			warn("character_continuity", err)
		}
	}
	if foundation, err := t.store.LoadWorldFoundation(); err == nil && foundation != nil {
		envelope.Working["world_foundation"] = foundation
		envelope.Working["world_foundation_policy"] = "世界基础规则、开局时间和过去时间线是正文前铁律；角色未获得明确改变规则的能力/凭证前，章节推演必须服从这里的规则、地点基线和信息边界。"
	} else {
		warn("world_foundation", err)
	}
	if codex, err := t.store.LoadWorldCodex(); err == nil && codex != nil {
		envelope.Working["world_codex"] = codex
		envelope.Working["world_codex_policy"] = "世界法典是全局硬设定：能力分级、技能范畴、种族、武器/装备品级和各世界维度不可随写作漂移；character_kit 的 codex_tier 必须引用这里的分级名。修订法典必须走 save_foundation(type=world_codex) 并给出 change_reason/change_evidence。"
		volume := 0
		if state.progress != nil {
			volume = state.progress.CurrentVolume
		}
		if volume > 0 {
			if vc, err := t.store.LoadVolumeCodex(volume); err == nil && vc != nil {
				envelope.Working["volume_codex"] = vc
				envelope.Working["volume_codex_policy"] = "当前卷上限法典：本章出现的能力/武器/装备/技能不得超过卷内上限；卷内未解锁的机制与种族不得提前登场。"
			} else {
				warn("volume_codex", err)
			}
		}
	} else {
		warn("world_codex", err)
	}
	if dossiers, err := t.store.LoadAllCharacterDossiers(); err == nil && len(dossiers) > 0 {
		if len(dossiers) > 18 {
			dossiers = dossiers[:18]
		}
		envelope.Working["character_dossiers"] = dossiers
		envelope.Working["character_dossier_policy"] = "每个角色独立档案记录生活/工作地点、故事前经历、资源、通信边界和关系来源；主角未通信时不能默认知道配角时间线。新角色若由配角线引入，必须补入对应角色档案或后续台账。"
	} else {
		warn("character_dossiers", err)
	}
	if stages, err := t.store.LoadRecentCharacterStageRecords(state.chapter, 5); err == nil && len(stages) > 0 {
		envelope.Working["character_stage_records"] = stages
		envelope.Working["character_stage_policy"] = "角色现场台账记录正文内外角色在同一时间线中的环境、行动、误判和决策；写新章前必须核对，避免角色突然出现或开局全知全能。"
	} else {
		warn("character_stage_records", err)
	}
	if journeys, err := t.store.LoadRecentSideCharacterJourneys(state.chapter, 5); err == nil && len(journeys) > 0 {
		envelope.Working["side_character_journeys"] = journeys
		envelope.Working["side_character_journey_policy"] = "配角动态日志只记录主角之外的人物经历、位置、交通/见面限制、性格变化和死亡/失踪传回计划；正文可只写主角视角，但新章必须尊重这些配角自己的遭遇线。"
	} else {
		warn("side_character_journeys", err)
	}
	if deltas, err := t.store.LoadRecentChapterWorldDeltas(state.chapter, 5); err == nil && len(deltas) > 0 {
		envelope.Working["chapter_world_deltas"] = deltas
		envelope.Working["chapter_world_delta_policy"] = "每章提交后的全角色与世界推进汇总；包含正文可见和主角视角外的推演变化。rewrite 后会覆盖对应章节，后续推演优先按这里恢复角色/世界状态。"
	} else {
		warn("chapter_world_deltas", err)
	}

	if isRewriteTarget {
		envelope.Working["future_outline_policy"] = "当前处于返工阶段，只使用 current_chapter_outline 与 rewrite_brief 重建本章；next_chapter_outline 和 future_outline_window 已隐藏，禁止续写未来章。"
	} else {
		if next, err := t.store.Outline.GetChapterOutline(state.chapter + 1); err == nil && next != nil {
			envelope.Working["next_chapter_outline"] = next
		}
		if future := t.futureOutlineWindow(state.chapter, 4); len(future) > 0 {
			envelope.Working["future_outline_window"] = future
			envelope.Working["future_outline_policy"] = "写正文前必须同时核对当前章及后续3-4章大纲，确保本章选择、伏笔、资源和人物状态服务本卷/本弧后续推进；不得只顾当前单章爽点。"
		}
	}

	if !suppressLegacyState {
		if state.profile.Layered {
			t.loadLayeredSummaries(envelope.Working, state.chapter, state.profile.SummaryWindow, warn)
		} else {
			if summaries, err := t.store.Summaries.LoadRecentSummaries(state.chapter, state.profile.SummaryWindow); err == nil && len(summaries) > 0 {
				envelope.Working["recent_summaries"] = summaries
			} else {
				warn("recent_summaries", err)
			}
		}

		if timeline, err := t.store.World.LoadRecentTimeline(state.chapter, state.profile.TimelineWindow); err == nil && len(timeline) > 0 {
			envelope.Working["timeline"] = timeline
		} else {
			warn("timeline", err)
		}
	}

	if state.progress != nil {
		checkpoint := map[string]any{
			"in_progress_chapter": state.progress.InProgressChapter,
		}
		if len(state.progress.StrandHistory) > 0 {
			checkpoint["strand_history"] = state.progress.StrandHistory
		}
		if len(state.progress.HookHistory) > 0 {
			checkpoint["hook_history"] = state.progress.HookHistory
		}
		envelope.Working["checkpoint"] = checkpoint
	}

	if !suppressLegacyState && state.chapter > 1 {
		if prevText, err := t.store.Drafts.LoadChapterText(state.chapter - 1); err == nil && prevText != "" {
			runes := []rune(prevText)
			if len(runes) > 800 {
				runes = runes[len(runes)-800:]
			}
			envelope.Working["previous_tail"] = string(runes)
		}
	}
}

func shouldSuppressLegacyStateForRestart(policy *domain.SimulationRestartPolicy, progress *domain.Progress) bool {
	if policy == nil || !policy.Active || strings.TrimSpace(policy.GenerationID) == "" {
		return false
	}
	if progress == nil {
		return true
	}
	return strings.TrimSpace(progress.GenerationID) != strings.TrimSpace(policy.GenerationID)
}

// buildMethodologyContext 注入方法论批次的可选写前约束（缺失的工件跳过）：
// 道德天花板、物理公理、地平线事件。均为纯读 + compact 注入，不进控制流。
func (t *ContextTool) buildMethodologyContext(working map[string]any) {
	if mc, err := t.store.Methodology.LoadMoralCeiling(); err == nil && mc != nil && !mc.IsEmpty() {
		working["moral_ceiling"] = mc
		working["moral_ceiling_usage"] = "moral_ceiling 是主角道德硬边界：taboo_zones 绝对不可触碰；kills/necessary_evil 预算超出前应安排代价与动摇"
	}
	if pa, err := t.store.Methodology.LoadPhysicsAxioms(); err == nil && pa != nil && !pa.IsEmpty() {
		working["physics_axioms"] = pa
		working["physics_axioms_usage"] = "physics_axioms 是物理一致性公理：涉及赶路/传信/物价/季节/境界时先对照本表（距离速度、信息传播天数、物价、境界序列、物候、保质期），check_consistency 阶段须复核"
	}
	if cal, err := t.store.WorldSim.LoadStoryCalendar(); err == nil && cal != nil && !cal.IsEmpty() {
		working["story_calendar"] = cal
		working["story_calendar_usage"] = "story_calendar 是故事内时间基线：正文的日期/季节/节令须与之一致；一章约覆盖 days_per_chapter 天，跨章时间跳跃要交代"
	}
	// Task 059 注入（生成链路）：slop 规避清单——Writer 写作时即对照规避，
	// 命中会在提交时被机械检测标红；返工链路在 rewrite brief 里再注入一次。
	aigc.LoadProjectLexicon(t.store.Dir())
	working["slop_avoidlist"] = aigc.LexiconDigest(6)
	working["slop_avoidlist_usage"] = "slop_avoidlist 是 AI 味高频词群（版本 " + aigc.LexiconVersion() + "）：各组给出样例、同族变体一并规避（如'不是A而是B'的全部变形）；用具体动作/感官/口语替代，不要用同义词换皮"

	// Task 056 消费端：信息差图 / 社会情绪 / 仪式日历（零章派生的权威工件）。
	if g, err := t.store.Methodology.LoadInfoGraph(); err == nil && g != nil && len(g.Nodes) > 0 {
		working["info_graph"] = g
		working["info_graph_usage"] = "info_graph 是谁知道什么的基线：reader 节点是读者已知；各角色 knows/believes(误信)/must_not_know_yet 划定台词与视角边界——角色不得说出自己不该知道的信息"
	}
	if m, err := t.store.Methodology.LoadSocialMood(); err == nil && m != nil && m.Mood != "" {
		working["social_mood"] = m
		working["social_mood_usage"] = "social_mood 是场景底色：群体情绪与街头谣言通过路人言行、环境细节渗出，不要旁白直述"
	}
	if cal, err := t.store.Methodology.LoadRitualCalendar(); err == nil && cal != nil && (len(cal.Annual) > 0 || len(cal.Lifecycle) > 0) {
		working["ritual_calendar"] = cal
		working["ritual_calendar_usage"] = "ritual_calendar 是社会时间结构：正文日期落在仪式/截止窗口内时，全民行为与情绪要随之改变，错过的代价要兑现"
	}
	// Task 081 消费端：宇宙观 / NPC 生态 / 生态图 / 文化脚注。
	if cos, err := t.store.Methodology.LoadCosmology(); err == nil && cos != nil && len(cos.Axioms) > 0 {
		working["cosmology"] = cos
		working["cosmology_usage"] = "cosmology 是世界第一性原理：任何超常展开先对照公理与代价，显规则均由此推导，不得让角色无代价违反"
	}
	if crowd, err := t.store.Methodology.LoadCrowdLife(); err == nil && crowd != nil && len(crowd.NPCs) > 0 {
		working["crowd_life"] = crowd
		working["crowd_life_usage"] = "crowd_life 是 NPC 自洽生活循环：路人有自己的日程与目标，不是只为主角配戏；场景里可让其按日程自然出现"
	}
	if eco, err := t.store.Methodology.LoadEcologicalMap(); err == nil && eco != nil && len(eco.Ecosystems) > 0 {
		working["ecological_map"] = eco
	}
	if cf, err := t.store.Methodology.LoadCulturalFootnotes(); err == nil && cf != nil && len(cf.Footnotes) > 0 {
		working["cultural_footnotes"] = cf
		working["cultural_footnotes_usage"] = "cultural_footnotes 是文化负载项：正文用到对应仪式/典故时按脚注语义展开，不要架空其社会含义"
	}
}

// horizonEventWindow 地平线事件的新鲜窗口：事件越过可见章后再保留 N 章供正文消化。
const horizonEventWindow = 5

// buildHorizonEvents 注入"已越过地平线"的离屏世界事件（Writer 路径）。
// 正文只写主角能感知到的世界：未越地平线的事件绝不注入。
func (t *ContextTool) buildHorizonEvents(working map[string]any, chapter int, warn func(string, error)) {
	events, err := t.store.WorldSim.HorizonEvents(chapter, horizonEventWindow)
	if err != nil {
		warn("horizon_events", err)
		return
	}
	if len(events) == 0 {
		return
	}
	working["horizon_events"] = events
	working["horizon_events_usage"] = "horizon_events 是镜头外世界已传到主角感知范围的事件：主角只能通过各事件 visibility_path 描述的渠道（谣言/信使/亲见/官报）得知，感知程度与渠道可信度匹配；事件在正文落地后，用 commit 的 state_changes/knowledge 同步主角认知；未列入本表的离屏事件主角一概不知，不得写入正文"
}

// buildWorldSimulationPlanning 注入世界全景（Architect 规划路径）：tick 游标、
// 日程账本、近期未浮出事件与伏笔提名，供展开下一弧时消费。
func (t *ContextTool) buildWorldSimulationPlanning(planning map[string]any, warn func(string, error)) {
	tick, err := t.store.WorldSim.LoadTick()
	if err != nil {
		warn("world_tick", err)
		return
	}
	agenda, err := t.store.WorldSim.LoadAgendaLedger()
	if err != nil {
		warn("offscreen_agenda", err)
	}
	events, err := t.store.WorldSim.LoadWorldEvents()
	if err != nil {
		warn("world_events", err)
	}
	if tick == nil && len(agenda.Agendas) == 0 && len(events) == 0 {
		return
	}

	current := 0
	if progress, err := t.store.Progress.Load(); err == nil && progress != nil {
		current = progress.LatestCompleted()
	}
	pendingVisible := 0
	for _, e := range events {
		if e.VisibilityChapter > current {
			pendingVisible++
		}
	}

	sim := map[string]any{}
	if tick != nil {
		sim["tick"] = tick
	}
	if cal, err := t.store.WorldSim.LoadStoryCalendar(); err == nil && cal != nil && !cal.IsEmpty() {
		sim["story_calendar"] = cal
	}
	if len(agenda.Agendas) > 0 {
		sim["offscreen_agenda"] = agenda.Agendas
	}
	if pendingVisible > 0 {
		sim["pending_visible_events"] = pendingVisible
	}
	if candidates := domain.NominateForeshadowCandidates(events, current, 3); len(candidates) > 0 {
		sim["foreshadow_candidates_from_world"] = candidates
		sim["foreshadow_candidates_usage"] = "这些离屏事件将来才会撞上主角或已标记回收价值，是天然伏笔素材：可在下一弧大纲中安排浮出点；正式入账仍走 Writer 提交时的 foreshadow_updates"
	}
	sim["_usage"] = "world_simulation 是镜头外世界的推演现状：展开下一弧前应先调 save_world_tick 把世界推进到弧末（推进各角色 agenda、产生离屏事件、更新社会情绪），再基于推演结果规划下一弧"
	planning["world_simulation"] = sim
}

// orderWorldRulesByVisibility 按 formal → informal → secret 稳定排序（Task 011）。
func orderWorldRulesByVisibility(rules []domain.WorldRule) []domain.WorldRule {
	ordered := make([]domain.WorldRule, 0, len(rules))
	for _, vis := range []string{"formal", "informal", "secret"} {
		ordered = append(ordered, domain.FilterWorldRules(rules, vis)...)
	}
	return ordered
}

func hasInformalOrSecretRules(rules []domain.WorldRule) bool {
	for _, r := range rules {
		if v := domain.WorldRuleVisibility(r); v == "informal" || v == "secret" {
			return true
		}
	}
	return false
}

func (t *ContextTool) futureOutlineWindow(chapter, lookahead int) []domain.OutlineEntry {
	if lookahead < 0 {
		lookahead = 0
	}
	var out []domain.OutlineEntry
	for ch := chapter; ch <= chapter+lookahead; ch++ {
		entry, err := t.store.Outline.GetChapterOutline(ch)
		if err != nil || entry == nil {
			continue
		}
		out = append(out, *entry)
	}
	return out
}

func compactProgressionSnapshot(ledger *domain.ChapterProgressLedger, chapter int) map[string]any {
	snapshot := map[string]any{
		"generated_at":       ledger.GeneratedAt,
		"protagonist":        ledger.Protagonist,
		"completed_chapters": ledger.CompletedChapters,
		"total_chapters":     ledger.TotalChapters,
		"current_chapter":    ledger.CurrentChapter,
		"current_volume":     ledger.CurrentVolume,
		"current_arc":        ledger.CurrentArc,
		"source_artifact":    "meta/chapter_progress.md",
		"source_json":        "meta/chapter_progress.json",
		"usage":              "章节通过后的动态进度台账；写本章前优先对照 next_plan 和 recent_entries，避免每章通过后丢失人物/时间线推进。",
	}
	if ledger.NextPlan != nil {
		snapshot["next_plan"] = ledger.NextPlan
		if ledger.NextPlan.Chapter != chapter {
			snapshot["next_plan_warning"] = "next_plan 与当前请求章号不一致，请先核对 progress.current_chapter 和 outline 是否已刷新。"
		}
	}
	var recent []domain.ChapterProgressEntry
	for i := len(ledger.Entries) - 1; i >= 0 && len(recent) < 3; i-- {
		if ledger.Entries[i].Chapter < chapter {
			recent = append(recent, ledger.Entries[i])
		}
	}
	for i, j := 0, len(recent)-1; i < j; i, j = i+1, j-1 {
		recent[i], recent[j] = recent[j], recent[i]
	}
	if len(recent) > 0 {
		snapshot["recent_entries"] = recent
	}
	return snapshot
}

func compactProjectProgressSnapshot(ledger *domain.ProjectProgressLedger, chapter int) map[string]any {
	snapshot := map[string]any{
		"generated_at":         ledger.GeneratedAt,
		"completed_chapters":   ledger.CompletedChapters,
		"total_chapters":       ledger.TotalChapters,
		"delivery_chapters":    ledger.DeliveryChapters,
		"current_chapter":      ledger.CurrentChapter,
		"current_volume":       ledger.CurrentVolume,
		"current_arc":          ledger.CurrentArc,
		"source_artifact":      "meta/project_progress.md",
		"source_json":          "meta/project_progress.json",
		"usage":                "项目级规划仪表盘；写作前用于核对交付口径、卷弧推进、主角变化路线、承诺兑现、钩子疲劳、资源清账、伏笔优先级、关系张力和资产运营。",
		"next_chapter_actions": ledger.NextChapterActions,
	}
	if len(ledger.ScopeWarnings) > 0 {
		snapshot["scope_warnings"] = ledger.ScopeWarnings
	}
	if len(ledger.HookAnalysis.Warnings) > 0 {
		snapshot["hook_warnings"] = ledger.HookAnalysis.Warnings
	}
	if len(ledger.ResourceHygiene.Actions) > 0 {
		snapshot["resource_hygiene_actions"] = ledger.ResourceHygiene.Actions
	}
	if len(ledger.ForeshadowPlan) > 0 {
		limit := min(8, len(ledger.ForeshadowPlan))
		snapshot["foreshadow_priorities"] = ledger.ForeshadowPlan[:limit]
	}
	if len(ledger.RelationshipTension) > 0 {
		limit := min(8, len(ledger.RelationshipTension))
		snapshot["relationship_tension"] = ledger.RelationshipTension[:limit]
	}
	if len(ledger.AssetOperations) > 0 {
		limit := min(8, len(ledger.AssetOperations))
		snapshot["asset_operations"] = ledger.AssetOperations[:limit]
	}
	var currentArc *domain.OutlineArcStatus
	for i := range ledger.OutlineStatus {
		arc := ledger.OutlineStatus[i]
		if arc.StartChapter > 0 && arc.EndChapter > 0 && chapter >= arc.StartChapter && chapter <= arc.EndChapter {
			currentArc = &ledger.OutlineStatus[i]
			break
		}
	}
	if currentArc != nil {
		snapshot["current_arc_status"] = currentArc
	}
	var protagonistArc []domain.ProtagonistArcEntry
	for _, item := range ledger.ProtagonistArc {
		if item.Chapter >= chapter-3 && item.Chapter <= chapter+5 {
			protagonistArc = append(protagonistArc, item)
		}
	}
	if len(protagonistArc) > 0 {
		snapshot["protagonist_arc_window"] = protagonistArc
	}
	var recent []domain.ChapterPromiseEntry
	for i := len(ledger.PromiseEntries) - 1; i >= 0 && len(recent) < 5; i-- {
		if ledger.PromiseEntries[i].Chapter < chapter {
			recent = append(recent, ledger.PromiseEntries[i])
		}
	}
	for i, j := 0, len(recent)-1; i < j; i, j = i+1, j-1 {
		recent[i], recent[j] = recent[j], recent[i]
	}
	if len(recent) > 0 {
		snapshot["recent_promise_entries"] = recent
	}
	return snapshot
}

func compactEvolutionReportSnapshot(report *domain.EvolutionReport) map[string]any {
	snapshot := map[string]any{
		"generated_at":    report.GeneratedAt,
		"current_chapter": report.CurrentChapter,
		"window_chapters": report.WindowChapters,
		"health":          report.Health,
		"source_artifact": "meta/evolution_report.md",
		"source_json":     "meta/evolution_report.json",
		"usage":           "可审计自动进化报告；只记录诊断模式和候选改动，不能当成已采纳规则。写作前优先消化 action/watch 项，但不得绕过大纲、资源账本和审阅门禁。",
	}
	if len(report.Patterns) > 0 {
		limit := min(6, len(report.Patterns))
		snapshot["patterns"] = report.Patterns[:limit]
	}
	if len(report.Candidates) > 0 {
		limit := min(5, len(report.Candidates))
		snapshot["candidates"] = report.Candidates[:limit]
	}
	if len(report.Guardrails) > 0 {
		snapshot["guardrails"] = report.Guardrails
	}
	return snapshot
}

func compactCharacterContinuitySnapshot(ledger *domain.CharacterContinuityLedger, chapter int) map[string]any {
	snapshot := map[string]any{
		"generated_at":    ledger.GeneratedAt,
		"current_chapter": ledger.CurrentChapter,
		"review_policy":   ledger.ReviewPolicy,
		"source_artifact": "meta/character_continuity.md",
		"source_json":     "meta/character_continuity.json",
		"usage":           "角色动力学、知识账本、决策框架、关系契约、情绪评价、长期弧线轴、后续回归和状态保留参考；用于判断人物下一步合理行动、冲突咬合、信息边界与连续性，不强制某个角色出场。",
	}
	if len(ledger.NextChapterFocus) > 0 {
		snapshot["next_chapter_focus"] = ledger.NextChapterFocus
	}
	var active []domain.CharacterContinuityEntry
	for _, entry := range ledger.Entries {
		if len(active) >= 12 {
			break
		}
		nearFuture := false
		for _, use := range entry.FutureUses {
			if use.Chapter == 0 || use.Chapter >= chapter && use.Chapter <= chapter+8 {
				nearFuture = true
				break
			}
		}
		if entry.ReturnMode == "大纲明确回归" || entry.ReturnMode == "核心长线" || nearFuture {
			active = append(active, entry)
		}
	}
	if len(active) > 0 {
		snapshot["active_entries"] = active
	}
	return snapshot
}

func (t *ContextTool) buildChapterSelectedMemory(ctx context.Context, envelope *chapterContextEnvelope, state contextBuildState, warn func(string, error)) {
	if len(state.storyThreads) > 0 {
		envelope.Selected["story_threads"] = state.storyThreads
	}
	if ragItems, trace := t.selectRAGRecall(ctx, state); len(ragItems) > 0 {
		envelope.Selected["rag_recall"] = ragItems
		envelope.References["retrieval_trace"] = trace
		if trace != nil {
			_ = t.store.RAG.AppendTrace(*trace)
		}
	} else if trace != nil && len(trace.Matches) > 0 {
		envelope.References["retrieval_trace"] = trace
		_ = t.store.RAG.AppendTrace(*trace)
	}
	if lessons := t.selectReviewLessons(state.chapter, warn); len(lessons) > 0 {
		envelope.Selected["review_lessons"] = lessons
	}
}

func (t *ContextTool) buildChapterEpisodicMemory(envelope *chapterContextEnvelope, state contextBuildState, warn func(string, error)) {
	if len(state.foreshadow) > 0 && len(state.storyThreads) == 0 {
		envelope.Episodic["foreshadow_ledger"] = state.foreshadow
	}

	if audit, err := t.store.ResourceLedger.AuditForParticipants(state.chapterParticipants); err == nil {
		if len(audit.Booked) > 0 || len(audit.Pending) > 0 {
			envelope.Episodic["resource_audit"] = audit
		}
	} else {
		warn("resource_ledger", err)
	}

	if state.bookWorld != nil {
		if ctx := selectBookWorldContext(*state.bookWorld, stateFocusText(state)); len(ctx) > 0 {
			envelope.Episodic["book_world_context"] = ctx
		}
	}

	// 配角名册：召回最近活跃的次要角色，让 Writer 在引入旧角色时能保持口吻/定位一致
	// 不召回所有条目（长篇会膨胀），只给最近活跃的前 N 个，按 LastSeenChapter 倒序
	if recentCast, err := t.store.Cast.RecentActive(15); err == nil && len(recentCast) > 0 {
		simplified := make([]map[string]any, 0, len(recentCast))
		for _, e := range recentCast {
			item := map[string]any{
				"name":             e.Name,
				"first_seen":       e.FirstSeenChapter,
				"last_seen":        e.LastSeenChapter,
				"appearance_count": e.AppearanceCount,
			}
			if e.BriefRole != "" {
				item["brief_role"] = e.BriefRole
			}
			if len(e.Aliases) > 0 {
				item["aliases"] = e.Aliases
			}
			simplified = append(simplified, item)
		}
		envelope.Episodic["recent_cast"] = simplified
	} else if err != nil {
		warn("recent_cast", err)
	}

	if state.progress != nil && state.progress.TotalChapters > 30 && state.currentEntry != nil {
		if related := t.buildRelatedChapters(
			state.chapter,
			state.currentEntry,
			state.foreshadow,
			state.relationships,
			state.allStateChanges,
		); len(related) > 0 {
			envelope.Episodic["related_chapters"] = related
		}
	}

	if state.profile.Layered && state.progress != nil {
		pos := map[string]any{
			"volume": state.progress.CurrentVolume,
			"arc":    state.progress.CurrentArc,
		}
		if volumes, err := t.store.Outline.LoadLayeredOutline(); err == nil {
			globalCh := 1
			for _, v := range volumes {
				if v.Index == state.progress.CurrentVolume {
					pos["volume_title"] = v.Title
					pos["volume_theme"] = v.Theme
				}
				for _, arc := range v.Arcs {
					if v.Index == state.progress.CurrentVolume && arc.Index == state.progress.CurrentArc {
						pos["arc_title"] = arc.Title
						pos["arc_goal"] = arc.Goal
						if n := len(arc.Chapters); n > 0 {
							pos["arc_total_chapters"] = n
							pos["arc_chapter_index"] = state.chapter - globalCh + 1
						}
					}
					globalCh += len(arc.Chapters)
				}
			}
		} else {
			warn("layered_outline", err)
		}
		envelope.Episodic["position"] = pos
	}
}

func (t *ContextTool) buildChapterReferencePack(envelope *chapterContextEnvelope, state contextBuildState, warn func(string, error)) {
	if state.writingEngine != nil {
		envelope.References["writing_engine"] = state.writingEngine
		if state.styleRules != nil {
			envelope.References["style_rules"] = state.styleRules
		}
	} else if state.styleRules != nil {
		envelope.References["style_rules"] = state.styleRules
	} else {
		var maxCompleted int
		if state.progress != nil {
			maxCompleted = maxCompletedChapter(state.progress.CompletedChapters)
		}
		if anchors := t.store.Drafts.ExtractStyleAnchors(3, maxCompleted); len(anchors) > 0 {
			envelope.References["style_anchors"] = anchors
		}

		if state.currentEntry != nil {
			var voiceSamples []map[string]any
			chars, _ := t.store.Characters.Load()
			for _, c := range chars {
				if c.Tier == "secondary" || c.Tier == "decorative" {
					continue
				}
				samples := t.store.Drafts.ExtractDialogue(c.Name, c.Aliases, 3, maxCompleted)
				if len(samples) > 0 {
					voiceSamples = append(voiceSamples, map[string]any{
						"character": c.Name,
						"samples":   samples,
					})
				}
				if len(voiceSamples) >= 5 {
					break
				}
			}
			if len(voiceSamples) > 0 {
				envelope.References["voice_samples"] = voiceSamples
			}
		}
	}

	refs := t.writerReferences(state.chapter)
	t.addProjectWebReferenceBrief(refs, warn)
	t.addPrewriteStorycraftPlan(refs, warn)
	t.addWorldBackgroundPlan(refs, warn)
	envelope.References["references"] = refs
}

func (t *ContextTool) detectChapterParticipants(entry *domain.OutlineEntry, plan *domain.ChapterPlan, warn func(string, error)) []string {
	chars, err := t.store.Characters.Load()
	if err != nil {
		warn("characters", err)
		return nil
	}
	text := ""
	if entry != nil {
		text += entry.Title + " " + entry.CoreEvent + " " + entry.Hook + " " + strings.Join(entry.Scenes, " ")
	}
	if plan != nil {
		text += " " + plan.Title + " " + plan.Goal + " " + plan.Conflict + " " + plan.Hook + " " + plan.Notes
		text += " " + strings.Join(plan.Contract.RequiredBeats, " ")
		text += " " + strings.Join(plan.Contract.ForbiddenMoves, " ")
		text += " " + strings.Join(plan.Contract.ContinuityChecks, " ")
		text += " " + strings.Join(plan.Contract.EvaluationFocus, " ")
		text += " " + strings.Join(plan.Contract.SceneAnchors, " ")
	}
	return uniqueStrings(matchOutlineCharacters(text, chars))
}

func (state contextBuildState) writingBindingScope() *domain.WritingBinding {
	scope := &domain.WritingBinding{Scope: "chapter", Chapter: state.chapter}
	if state.progress != nil {
		scope.Volume = state.progress.CurrentVolume
		scope.Arc = state.progress.CurrentArc
	}
	return scope
}

func (t *ContextTool) loadMechanicalGateBrief(chapter int) (map[string]any, error) {
	if chapter <= 0 {
		return nil, nil
	}
	type gatePath struct {
		jsonRel     string
		markdownRel string
	}
	candidates := []gatePath{
		{
			jsonRel:     fmt.Sprintf("reviews/%02d_ai_gate.json", chapter),
			markdownRel: fmt.Sprintf("reviews/%02d.md", chapter),
		},
		{
			jsonRel:     fmt.Sprintf("reviews_ai/%02d.json", chapter),
			markdownRel: fmt.Sprintf("reviews_ai/第%03d章_AI味审核.md", chapter),
		},
	}
	var raw []byte
	selected := candidates[0]
	for _, candidate := range candidates {
		var err error
		raw, err = os.ReadFile(filepath.Join(t.store.Dir(), candidate.jsonRel))
		if err == nil {
			selected = candidate
			break
		}
		if !os.IsNotExist(err) {
			return nil, err
		}
	}
	if len(raw) == 0 {
		return nil, nil
	}
	var payload mechanicalGateReviewPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}
	report := payload.AIGCReport
	brief := map[string]any{
		"json_path":                  selected.jsonRel,
		"markdown_path":              selected.markdownRel,
		"engine":                     report.Engine,
		"aigc_percent":               report.AIGCPercent,
		"ai_ratio_percent":           report.AIRatioPercent,
		"effective_gate_percent":     aigc.EffectiveGatePercent(report),
		"blended_aigc_percent":       report.BlendedAIGCPercent,
		"segment_risk_floor_percent": report.SegmentRiskFloor,
		"single_detection_segment":   report.Stats.Hanzi > 0 && report.Stats.Hanzi <= aigc.SingleDetectionSegmentMaxHanzi,
		"gate_basis":                 "EffectiveGatePercent: 短章按整章单检测片段/segment floor 判，不被 blended 平均值稀释",
		"risk_label":                 report.RiskLabel,
		"confidence":                 report.Confidence,
	}
	if report.LatestDetectorProxy.CompositePercent > 0 || len(report.LatestDetectorProxy.Components) > 0 {
		brief["latest_detector_proxy"] = map[string]any{
			"composite_percent": report.LatestDetectorProxy.CompositePercent,
			"top_components":    compactAIGCDimensions(report.LatestDetectorProxy.Components, 4, 2),
		}
	}
	if len(report.Dimensions) > 0 {
		brief["high_risk_dimensions"] = compactAIGCDimensions(report.Dimensions, 4, 2)
	}
	if len(payload.RuleViolations) > 0 {
		brief["rule_violations"] = compactRuleViolations(payload.RuleViolations, 8)
		brief["rewrite_focus"] = mechanicalGateRewriteFocus(payload.RuleViolations, report)
	}
	return brief, nil
}

func compactAIGCDimensions(dimensions map[string]aigc.Dimension, limit, signalLimit int) []map[string]any {
	if len(dimensions) == 0 || limit <= 0 {
		return nil
	}
	items := make([]aigc.Dimension, 0, len(dimensions))
	for _, dim := range dimensions {
		items = append(items, dim)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Score == items[j].Score {
			return items[i].Name < items[j].Name
		}
		return items[i].Score > items[j].Score
	})
	out := make([]map[string]any, 0, min(len(items), limit))
	for _, dim := range items[:min(len(items), limit)] {
		item := map[string]any{
			"name":  dim.Name,
			"score": dim.Score,
		}
		if len(dim.Signals) > 0 && signalLimit > 0 {
			signals := make([]map[string]any, 0, min(len(dim.Signals), signalLimit))
			for _, sig := range dim.Signals[:min(len(dim.Signals), signalLimit)] {
				signals = append(signals, map[string]any{
					"name":     sig.Name,
					"score":    sig.Score,
					"evidence": sig.Evidence,
				})
			}
			item["signals"] = signals
		}
		out = append(out, item)
	}
	return out
}

func compactRuleViolations(violations []rules.Violation, limit int) []map[string]any {
	if len(violations) == 0 || limit <= 0 {
		return nil
	}
	out := make([]map[string]any, 0, min(len(violations), limit))
	for _, violation := range violations[:min(len(violations), limit)] {
		item := map[string]any{
			"rule":     violation.Rule,
			"severity": violation.Severity,
			"actual":   violation.Actual,
		}
		if violation.Target != "" {
			item["target"] = violation.Target
		}
		if rules.HasLimitValue(violation.Limit) {
			item["limit"] = violation.Limit
		}
		out = append(out, item)
	}
	return out
}

func mechanicalGateRewriteFocus(violations []rules.Violation, report aigc.Report) []string {
	var focus []string
	for _, violation := range violations {
		switch violation.Rule {
		case "aigc_ratio":
			gatePercent := aigc.EffectiveGatePercent(report)
			if report.Stats.Hanzi > 0 && report.Stats.Hanzi <= aigc.SingleDetectionSegmentMaxHanzi {
				focus = append(focus, fmt.Sprintf("AIGC 门禁采用值 %.2f%%：本章约 %d 汉字，会被读者整章直接丢进检测器；按单检测片段/segment floor 返工，不能用 blended=%.2f%% 放行。", gatePercent, report.Stats.Hanzi, report.BlendedAIGCPercent))
			} else {
				focus = append(focus, fmt.Sprintf("AIGC 门禁采用值 %.2f%%：按 EffectiveGatePercent 返工，优先看最高风险片段和 latest detector proxy，不随机换词。", gatePercent))
			}
			focus = append(focus, "整章重排段落功能：事故触发、口头争执、私人生活侵入、物件迟到、沉默/缺席、权限后果要轮换出现，避免每 180 字窗口都同样稳定。")
			focus = append(focus, "降低流程句密度：连续出现“保全/导出/权限/说明/审批”时，改成角色怕担责、甩锅、拒签、误按、被私人消息打断等具体压力。")
			focus = append(focus, "增加真实局部不均匀：允许少量由角色压力推动的口语重复、打断、没听清、改口和生活细节，但禁止无信息清单、冷僻词堆砌和脏码。")
		case "chapter_words":
			focus = append(focus, "篇幅超标只做局部压缩：优先删重复规则说明、重复互动问答和同义情绪句；保留已成立的场景、规则链、钩子和人物声口，不要整章重写。")
		case "content_count_mismatch":
			focus = append(focus, "逐条核对正文中的精确数量、清单和实际内容，不确定就改成模糊但准确的表达。")
		case "pending_resource_as_fact":
			focus = append(focus, "把待确认资源改成猜测、提案或谈判状态；确需成为事实时先让 commit_chapter 入账。")
		case "project_contamination":
			focus = append(focus, "删除跨项目污染词，回到本书的许闻溪/澄光生活/溪流助手/岗位合并/桥点职业转型事实；参考素材只能转译成写法，不能搬进正文。")
		case "deprecated_story_engine":
			focus = append(focus, "旧版硬核取证引擎已禁用：不要写系统留痕、原始材料、正式邮件、会务核查或技术追查；改写成女性职场成长压力，如公开羞辱、岗位被合并、同事求助、会后约谈和权限/项目被暂停。")
		case "micro_action_overuse":
			focus = append(focus, "微动作只保留承载道具、伏笔或人物关系的少数几处，其余改成对话摩擦、环境反应、留白或删除。")
		case "dramatic_negation_overuse":
			focus = append(focus, "删掉过量“没有立刻/没急着/没答”式克制声明，直接写角色做了什么，让动作顺序体现克制。")
		case "paragraph_start_repetition":
			focus = append(focus, "连续段首同主语时，换成环境先行、对话先行、宾语前置或旁观者动作进入，制造段落落差。")
		case "not_but_overuse":
			focus = append(focus, "“不是A而是B”每章最多保留一处，其余改成普通陈述、动作后果或物件变化。")
		case "precise_measure_overuse":
			focus = append(focus, "一指/半寸/两寸等精确量词只留给规则、机关或伏笔，其余改成“高了些”“漫上来一点”等模糊感知。")
		case "patch_phrase_overuse":
			focus = append(focus, "修掉“了一下”后不要复读“停了一拍/停了停”；补丁痕迹也要不均匀，能删就删，能换通道就换。")
		case "minor_mistake_overuse":
			focus = append(focus, "刻意小失误每章最多两处；超过后删掉或改成真实代价，不要把“人味”写成新模板。")
		case "isolated_sentence_overuse":
			focus = append(focus, "单行孤句每章最多四个；保留最重的强调，其余并回上下文，并检查章末收束不要和相邻章节同模板。")
		case "supporting_quip_overuse":
			focus = append(focus, "同一配角吐槽每章最多三句；重要节点至少让一句话无人接住，不要每句都被剧情接走。")
		case "vague_quantifier_overuse":
			focus = append(focus, "半/一点/几分等虚量词同字每章最多四次；具体物件保留，抽象虚量能删就删或换成具体状态。")
		case "object_response_overuse":
			focus = append(focus, "屏幕、纸面、门牌、灯光等物件回应主角言行每章最多四次；删掉多余的立刻确认，改成沉默、人物误判或后续代价。")
		case "object_response_rhythm_flat":
			focus = append(focus, "物件回应不能等距；至少补一次延迟、一次缺席/静默，允许一次抢拍，但不要每句重话后立刻显字或亮灯。")
		case "dialogue_aphorism_overuse":
			focus = append(focus, "金句限流扩到主角；连续警句式应答最多三回合，双人对手戏要用语域、句长、利益点和错答区分声口。")
		case "templated_dialogue_chain":
			focus = append(focus, "删掉重复的“点名/叫人→停笔或抬眼→补口径/查字段→第三人追问”三拍对白链；改成目标冲突、误读、拒写、打断、物件承压或信息延迟。")
		case "serial_device_repetition":
			focus = append(focus, "登记每章开头/结尾装置；同一装置连续最多两章，章尾显字三连要改成动作未完成、对话截断、场景余像或物件缺席。")
		case "semicolon_overuse":
			focus = append(focus, "正文分号过多时先朗读；非童谣、非条款的分号改成句号、逗号、破折号或直接拆段，让语气跟人物和场景走。")
		case "form_notice_semicolon_chain":
			focus = append(focus, "纸面、账单、卡面提示不要写成一行分号链；改成真实载体上的错行、涂改、缺字、补字或角色逐行读到。")
		case "dialogue_semicolon_formality":
			focus = append(focus, "普通对白里的分号会显得书面；改成停顿、省略、打断、追问或两句口语。")
		case "stiff_trade_dialogue":
			focus = append(focus, "讲价/互怼对白不能像合同条款或广告口号；改成有停顿、有关系、有算盘的普通人口语。")
		case "bureaucratic_register_overuse":
			focus = append(focus, "制度/纪要/表单词过密时，不要继续补规范说明；把信息拆进人物口语、担责压力、误读、拒写、私人消息打断和具体动作。专业词可以保留在表格/屏幕里，人物说话要短、怕事、有口头反应。")
		case "structured_note_triplet":
			focus = append(focus, "便签和备忘录不要三条工整并列；改成划掉、补字、挤在行尾、写半截、回看物件后暂不下结论的现场痕迹。")
		case "card_tos_block":
			focus = append(focus, "黑卡/系统提示不要完整列 ToS；改成残缺字、糊掉的行、读不全的凸字、空白账单位，让读者自己补规则。")
		case "empty_parallel_chant":
			focus = append(focus, "童谣保留有内容的规则链，删掉空对仗三连；让孩子背岔、卡壳、问妈妈后面是什么，或混入不通顺的数字/童声错位。")
		case "de_fa_adjective_repetition":
			focus = append(focus, "全章只保留一两处最有质感的“X得发Y”，其余换成具体状态、动作或直接删掉，避免同型形容词复现。")
		case "duplicate_dialogue_point":
			focus = append(focus, "相邻对白不要重复同一骂点或同一笑点；删一保一，或让第二句转成新信息、新行动、新代价。")
		case "impossible_body_geometry":
			focus = append(focus, "身体、影子和空间关系必须能成像；遇到肩膀/腰/半身等描述，改成可视方向明确的画面。")
		case "impossible_line_of_sight":
			focus = append(focus, "猫眼、门缝、侧向夹角不能读清背面小字；要么让字渗到主角门内/变大/贴到猫眼上，要么只写主角看不清。")
		case "causal_evidence_order":
			focus = append(focus, "角色点评证据前，证据必须已经在场面里出现；先写昵称/纸面/门牌变化，再让人指着它说话。")
		case "identity_effect_delayed":
			focus = append(focus, "报身份证、报名字、确认身份后的规则后果要紧贴演示；不要在因和果之间插闲聊、吐槽或新支线。")
		case "building_floor_mismatch":
			focus = append(focus, "楼栋、楼层、门牌号要统一；3栋5楼不能写成5栋承租物，除非剧情明确换了楼栋。")
		case "anomalous_phone_unverified":
			focus = append(focus, "异常来电若不是从基站/正常渠道进来，主角先做身份核验，再相信对方声音和信息。")
		case "form_image_mismatch":
			focus = append(focus, "票据、栏位、印章、表格的比喻必须贴合形状；栏位不写成像章，改成拼出来、贴歪或格线不齐。")
		case "card_core_rule_overblurred":
			focus = append(focus, "黑卡可以残缺、糊字、留白，但核心可玩规则要留一两个可读词，如“可确认”，让读者能拿规则参与推理。")
		case "ending_aphorism_question":
			focus = append(focus, "章末不要用抽象金句问号收束，改成具体动作、物件变化、新事实或未完成选择。")
		}
	}
	if len(focus) == 0 && report.AIGCPercent > 5 {
		focus = append(focus, "降低 AIGC 风险时不要随机换词；优先增加场景承载、角色声口差异和段落功能变化。")
	}
	return uniqueFocusItems(focus)
}

func uniqueFocusItems(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func (t *ContextTool) loadWritingEngine(fallback *domain.WritingStyleRules, scope *domain.WritingBinding, warn func(string, error)) *domain.WritingCompiled {
	compiled, err := t.store.WritingAssets.CompileForScope(8, scope)
	if err != nil {
		warn("writing_assets", err)
	}
	if compiled != nil && (len(compiled.EnabledFeatures) > 0 || len(compiled.ActiveRules) > 0 || len(compiled.Samples) > 0) {
		return compiled
	}
	if fallback == nil {
		return nil
	}
	lib := styleRulesAsWritingAssets(*fallback)
	c := compileWritingAssetsLocal(*lib, 8)
	return &c
}

func styleRulesAsWritingAssets(rules domain.WritingStyleRules) *domain.WritingAssetLibrary {
	var features []domain.WritingFeature
	for i, rule := range rules.Prose {
		features = append(features, domain.WritingFeature{
			ID:          fmt.Sprintf("legacy:prose:%d", i+1),
			Name:        rule,
			Category:    "prose",
			Description: rule,
			Enabled:     true,
			Rules:       []string{rule},
			Source:      "meta/style_rules.json",
		})
	}
	for i, voice := range rules.Dialogue {
		for j, rule := range voice.Rules {
			desc := voice.Name + "：" + rule
			features = append(features, domain.WritingFeature{
				ID:          fmt.Sprintf("legacy:dialogue:%d:%d", i+1, j+1),
				Name:        desc,
				Category:    "dialogue",
				Description: desc,
				Enabled:     true,
				Rules:       []string{desc},
				Source:      "meta/style_rules.json",
			})
		}
	}
	for i, taboo := range rules.Taboos {
		features = append(features, domain.WritingFeature{
			ID:          fmt.Sprintf("legacy:taboo:%d", i+1),
			Name:        taboo,
			Category:    "taboo",
			Description: taboo,
			Enabled:     true,
			Rules:       []string{"避免：" + taboo},
			Source:      "meta/style_rules.json",
		})
	}
	return &domain.WritingAssetLibrary{Version: 1, Features: features}
}

func compileWritingAssetsLocal(lib domain.WritingAssetLibrary, sampleLimit int) domain.WritingCompiled {
	if sampleLimit <= 0 {
		sampleLimit = 6
	}
	enabled := make(map[string]struct{})
	var c domain.WritingCompiled
	for _, f := range lib.Features {
		if !f.Enabled {
			continue
		}
		c.EnabledFeatures = append(c.EnabledFeatures, f)
		enabled[f.ID] = struct{}{}
		for _, rule := range f.Rules {
			if rule != "" && !slices.Contains(c.ActiveRules, rule) {
				c.ActiveRules = append(c.ActiveRules, rule)
			}
		}
		if f.Category == "anti_ai" {
			c.AntiAIRules = append(c.AntiAIRules, f.Rules...)
		}
		if f.Category == "taboo" {
			c.Taboos = append(c.Taboos, f.Rules...)
		}
	}
	for _, sample := range lib.Samples {
		if sample.FeatureID != "" {
			if _, ok := enabled[sample.FeatureID]; !ok {
				continue
			}
		}
		c.Samples = append(c.Samples, sample)
		if len(c.Samples) >= sampleLimit {
			break
		}
	}
	c.Trace = []string{
		fmt.Sprintf("enabled_features=%d", len(c.EnabledFeatures)),
		fmt.Sprintf("active_rules=%d", len(c.ActiveRules)),
		fmt.Sprintf("samples=%d", len(c.Samples)),
	}
	return c
}

func selectBookWorldContext(world domain.BookWorld, focus string) map[string]any {
	result := map[string]any{}
	if world.Summary != "" {
		result["summary"] = world.Summary
	}
	if len(world.MapNotes) > 0 {
		result["map_notes"] = world.MapNotes
	}
	var places []domain.WorldPlace
	for _, p := range world.Places {
		if focus == "" || matchesWorldText(focus, p.ID, p.Name, p.Kind, p.Description, strings.Join(p.Tags, " ")) {
			places = append(places, p)
		}
		if len(places) >= 6 {
			break
		}
	}
	if len(places) > 0 {
		result["places"] = places
	}
	var factions []domain.WorldFaction
	for _, f := range world.Factions {
		if focus == "" || matchesWorldText(focus, f.ID, f.Name, f.Goal, strings.Join(f.Tags, " "), strings.Join(f.Resources, " ")) {
			factions = append(factions, f)
		}
		if len(factions) >= 6 {
			break
		}
	}
	if len(factions) > 0 {
		result["faction_graph"] = factions
	}
	var routes []domain.WorldRoute
	for _, r := range world.Routes {
		if focus == "" || matchesWorldText(focus, r.From, r.To, r.Description, r.Risk) {
			routes = append(routes, r)
		}
		if len(routes) >= 5 {
			break
		}
	}
	if len(routes) > 0 {
		result["routes"] = routes
	}
	if len(result) > 0 {
		result["_usage"] = "本容器是本章相关地图/势力上下文；只写角色能感知或剧情正在触碰的部分"
	}
	return result
}

func matchesWorldText(focus string, parts ...string) bool {
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if strings.Contains(focus, p) || containsAny(p, strings.Fields(focus)) {
			return true
		}
	}
	return false
}

func stateFocusText(state contextBuildState) string {
	var parts []string
	if state.currentEntry != nil {
		parts = append(parts, state.currentEntry.Title, state.currentEntry.CoreEvent, state.currentEntry.Hook)
		parts = append(parts, state.currentEntry.Scenes...)
	}
	if state.chapterPlan != nil {
		parts = append(parts, state.chapterPlan.Title, state.chapterPlan.Goal, state.chapterPlan.Conflict, state.chapterPlan.Hook)
		parts = append(parts, state.chapterPlan.Contract.RequiredBeats...)
		parts = append(parts, state.chapterPlan.Contract.ContinuityChecks...)
		parts = append(parts, state.chapterPlan.Contract.SceneAnchors...)
	}
	parts = append(parts, state.chapterParticipants...)
	return strings.Join(parts, " ")
}

func (t *ContextTool) selectRAGRecall(ctx context.Context, state contextBuildState) ([]domain.RecallItem, *domain.RetrievalTrace) {
	focus := stateFocusText(state)
	queryFields := recallFocusTerms(state.currentEntry, state.chapterPlan)
	queryFields = append(queryFields, state.chapterParticipants...)
	if state.currentEntry != nil {
		queryFields = append(queryFields, state.currentEntry.Title, state.currentEntry.CoreEvent)
	}
	terms := rag.QueryTerms(queryFields...)
	if focus == "" && len(terms) == 0 {
		return nil, nil
	}
	facetHints := recallFacetHints(state)
	scoredByID := make(map[string]*ragScored)
	addScore := func(chunk domain.RAGChunk, score float64, reasons ...string) {
		if score <= 0 {
			return
		}
		chunk = rag.NormalizeChunk(chunk)
		if chunk.ID == "" || rag.IsForbiddenChunk(chunk) {
			return
		}
		// 路由隔离：手法库/对标库只服务设计时刻（craft_recall 通道），
		// 常规章节召回只走本书事实层，防止外部材料被当成已发生事实。
		if rag.IsDesignOnlySourceKind(chunk.SourceKind) {
			return
		}
		if existing, ok := scoredByID[chunk.ID]; ok {
			existing.score += score
			existing.reasons = uniqueStrings(append(existing.reasons, reasons...))
			return
		}
		scoredByID[chunk.ID] = &ragScored{chunk: chunk, score: score, reasons: uniqueStrings(reasons)}
	}

	// BM25 词法通道：与向量召回互补（专名、门牌、条款编号等精确词命中）。
	// 向量路径做加性混合，无 embedder 时作为 fallback 的主干排序信号。
	bm25Query := ragQueryEmbeddingText(focus, queryFields, terms)
	ragState, ragStateErr := t.store.RAG.LoadIndexStateReadOnly()
	addBM25 := func() bool {
		if ragStateErr != nil || ragState == nil || len(ragState.Chunks) == 0 {
			return false
		}
		hits := t.cachedProjectBM25(ragState).Search(bm25Query, maxRAGRecallResults*3)
		if len(hits) == 0 {
			return false
		}
		maxScore := hits[0].Score
		for _, hit := range hits {
			addScore(hit.Chunk, 2.0*hit.Score/maxScore, fmt.Sprintf("bm25:%.3f", hit.Score))
		}
		return true
	}

	if t.ragEmbedder != nil {
		embedCtx, cancelEmbed := context.WithTimeout(ctx, 20*time.Second)
		queryVector, embedErr := t.ragEmbedder.Embed(embedCtx, bm25Query)
		cancelEmbed()
		if embedErr == nil && len(queryVector) > 0 {
			qdrantState := "not_configured"
			if t.ragVectorSearcher != nil {
				searchCtx, cancelSearch := context.WithTimeout(ctx, 8*time.Second)
				hits, searchErr := t.ragVectorSearcher.Search(searchCtx, queryVector, maxRAGRecallResults*3)
				cancelSearch()
				if searchErr == nil {
					qdrantState = "empty"
					for _, hit := range hits {
						addScore(hit.Point.Chunk, hit.Score*3.5, fmt.Sprintf("qdrant:%.3f", hit.Score))
					}
					if len(hits) > 0 {
						strategy := "qdrant_vector_engine_v2"
						if addBM25() {
							strategy = "qdrant_bm25_hybrid_v2"
						}
						return finishRAGRecall(scoredByID, focus, terms, strategy)
					}
				} else {
					qdrantState = "error"
				}
			}

			if vectorStore, err := t.store.RAG.LoadVectorStoreReadOnly(); err == nil && vectorStore != nil && len(vectorStore.Points) > 0 {
				localHits := rag.SearchVectorStore(vectorStore, queryVector, maxRAGRecallResults*3)
				for _, hit := range localHits {
					addScore(hit.Point.Chunk, hit.Score*3.0, fmt.Sprintf("vector:%.3f", hit.Score))
				}
				if len(localHits) > 0 {
					strategy := "local_vector_bm25_hybrid_v2"
					if qdrantState == "error" {
						strategy = "qdrant_error_local_vector_bm25_fallback_v2"
					} else if qdrantState == "empty" {
						strategy = "qdrant_empty_local_vector_bm25_fallback_v2"
					}
					_ = addBM25()
					return finishRAGRecall(scoredByID, focus, terms, strategy)
				}
			}
			if addBM25() {
				strategy := "semantic_empty_bm25_fallback_v2"
				if qdrantState == "error" {
					strategy = "qdrant_error_bm25_fallback_v2"
				}
				return finishRAGRecall(scoredByID, focus, terms, strategy)
			}
			return nil, nil
		}
		if addBM25() {
			return finishRAGRecall(scoredByID, focus, terms, "embedding_error_bm25_fallback_v2")
		}
		return nil, nil
	}

	if ragStateErr != nil || ragState == nil || len(ragState.Chunks) == 0 {
		return nil, nil
	}
	for _, chunk := range ragState.Chunks {
		if rag.IsForbiddenChunk(chunk) {
			continue
		}
		chunk = rag.NormalizeChunk(chunk)
		text := rag.SearchText(chunk)
		score := 0.0
		var reasons []string
		matched := false
		for _, phrase := range queryFields {
			phrase = strings.TrimSpace(phrase)
			if len([]rune(phrase)) < 2 {
				continue
			}
			if strings.Contains(text, phrase) {
				score += 2.5
				matched = true
				reasons = append(reasons, "exact:"+truncateRunes(phrase, 14))
			}
		}
		for _, term := range terms {
			if strings.Contains(text, term) {
				score += recallTermWeight(term)
				matched = true
				if len(reasons) < 6 {
					reasons = append(reasons, "term:"+term)
				}
			}
		}
		if chunk.Facet != "" {
			if weight, ok := facetHints[chunk.Facet]; ok {
				score += weight
				reasons = append(reasons, "facet_hint:"+chunk.Facet)
			}
		}
		if chunk.Context != "" && containsAny(chunk.Context, terms) {
			score += 0.75
			matched = true
			reasons = append(reasons, "context_overlap")
		}
		if len(chunk.Keywords) > 0 && containsAny(strings.Join(chunk.Keywords, " "), terms) {
			score += 0.5
			matched = true
			reasons = append(reasons, "keyword_overlap")
		}
		if focus != "" && containsAny(text, terms) {
			score += 0.5
			matched = true
			reasons = append(reasons, "focus_overlap")
		}
		if !matched || score <= 0 {
			continue
		}
		addScore(chunk, score, reasons...)
	}
	strategy := "local_contextual_keyword_fallback_v2"
	if addBM25() {
		strategy = "local_bm25_keyword_hybrid_v1"
	}
	return finishRAGRecall(scoredByID, focus, terms, strategy)
}

func (t *ContextTool) cachedProjectBM25(state *domain.RAGIndexState) *rag.BM25Index {
	if state == nil {
		return rag.BuildBM25Index(nil)
	}
	t.ragBM25Mu.Lock()
	defer t.ragBM25Mu.Unlock()
	if t.ragBM25Index != nil && t.ragBM25State == state {
		return t.ragBM25Index
	}
	factChunks := make([]domain.RAGChunk, 0, len(state.Chunks))
	for _, chunk := range state.Chunks {
		if rag.IsDesignOnlySourceKind(chunk.SourceKind) {
			continue
		}
		factChunks = append(factChunks, chunk)
	}
	t.ragBM25Index = rag.BuildBM25Index(factChunks)
	t.ragBM25State = state
	return t.ragBM25Index
}

type ragScored struct {
	chunk   domain.RAGChunk
	score   float64
	reasons []string
}

func finishRAGRecall(scoredByID map[string]*ragScored, focus string, terms []string, strategy string) ([]domain.RecallItem, *domain.RetrievalTrace) {
	var scoredItems []ragScored
	for _, item := range scoredByID {
		scoredItems = append(scoredItems, *item)
	}
	sort.SliceStable(scoredItems, func(i, j int) bool {
		return scoredItems[i].score > scoredItems[j].score
	})
	if len(scoredItems) > maxRAGRecallResults {
		scoredItems = scoredItems[:maxRAGRecallResults]
	}
	if len(scoredItems) == 0 {
		return nil, nil
	}
	var items []domain.RecallItem
	trace := &domain.RetrievalTrace{
		Query:      focus,
		QueryTerms: limitStrings(terms, 24),
		Strategy:   strategy,
		MaxResults: maxRAGRecallResults,
		CreatedAt:  time.Now().Format(time.RFC3339),
	}
	for _, item := range scoredItems {
		summary := item.chunk.Summary
		if summary == "" {
			summary = truncateRunes(item.chunk.Text, 80)
		}
		items = append(items, domain.RecallItem{
			Kind:    "rag",
			Key:     item.chunk.ID,
			Reason:  strings.Join(item.reasons, "；"),
			Summary: summary,
		})
		trace.Matches = append(trace.Matches, domain.RetrievalTraceHit{
			ChunkID:    item.chunk.ID,
			Score:      item.score,
			Reasons:    item.reasons,
			SourcePath: item.chunk.SourcePath,
			Facet:      item.chunk.Facet,
			SourceKind: item.chunk.SourceKind,
			Context:    item.chunk.Context,
		})
	}
	return items, trace
}

func ragQueryEmbeddingText(focus string, queryFields, terms []string) string {
	var parts []string
	if strings.TrimSpace(focus) != "" {
		parts = append(parts, "focus: "+strings.TrimSpace(focus))
	}
	if len(queryFields) > 0 {
		parts = append(parts, "query_fields: "+strings.Join(limitStrings(queryFields, 24), " "))
	}
	if len(terms) > 0 {
		parts = append(parts, "terms: "+strings.Join(limitStrings(terms, 40), " "))
	}
	return strings.Join(parts, "\n")
}

func recallFacetHints(state contextBuildState) map[string]float64 {
	hints := map[string]float64{
		"craft":    0.35,
		"plot":     0.25,
		"progress": 0.30,
		"planning": 0.25,
		"resource": 0.20,
		"review":   0.15,
	}
	focus := stateFocusText(state)
	if strings.Contains(focus, "角色") || len(state.chapterParticipants) > 0 {
		hints["character"] = 0.35
	}
	if strings.Contains(focus, "地点") || strings.Contains(focus, "势力") || strings.Contains(focus, "规则") || strings.Contains(focus, "世界") {
		hints["world"] = 0.35
	}
	if strings.Contains(focus, "资源") || strings.Contains(focus, "账本") || strings.Contains(focus, "资产") {
		hints["resource"] += 0.35
	}
	if strings.Contains(focus, "大纲") || strings.Contains(focus, "计划") || strings.Contains(focus, "规划") || strings.Contains(focus, "推进") {
		hints["planning"] += 0.25
		hints["progress"] += 0.25
	}
	if strings.Contains(focus, "写法") || strings.Contains(focus, "手法") || strings.Contains(focus, "反馈") || strings.Contains(focus, "审阅") {
		hints["craft"] += 0.35
		hints["review"] += 0.25
	}
	if state.chapterPlan != nil {
		if len(state.chapterPlan.Contract.ContinuityChecks) > 0 {
			hints["world"] += 0.2
			hints["character"] += 0.2
			hints["progress"] += 0.15
		}
		if len(state.chapterPlan.Contract.EvaluationFocus) > 0 {
			hints["craft"] += 0.2
			hints["review"] += 0.15
		}
		if len(state.chapterPlan.Contract.SceneAnchors) > 0 {
			hints["craft"] += 0.15
			hints["world"] += 0.1
			hints["resource"] += 0.1
		}
	}
	return hints
}

func recallTermWeight(term string) float64 {
	switch n := len([]rune(term)); {
	case n >= 6:
		return 0.9
	case n >= 4:
		return 0.65
	default:
		return 0.35
	}
}

func limitStrings(items []string, limit int) []string {
	if limit <= 0 || len(items) <= limit {
		return items
	}
	return items[:limit]
}

const maxRAGRecallResults = 6

func uniqueStrings(items []string) []string {
	seen := make(map[string]struct{}, len(items))
	var out []string
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	return out
}
