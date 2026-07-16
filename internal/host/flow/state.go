package flow

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/reviewreport"
	"github.com/chenhongyang/novel-studio/internal/rules"
	storepkg "github.com/chenhongyang/novel-studio/internal/store"
	toolspkg "github.com/chenhongyang/novel-studio/internal/tools"
)

// chapterPlanReadyForDraft 判断某章的写前计划是否已就绪、可直接交 drafter 渲染。
//   - 新章：drafts/NN.plan.json 存在即就绪。
//   - 返工章：计划还必须已纳入 rewrite_brief（context_sources 含 rewrite_brief），
//     否则说明尚未按审阅结论重推演，应先派 planner 重做计划。
func chapterPlanReadyForDraft(store *storepkg.Store, chapter int, isRewrite bool) bool {
	if partial, err := store.Drafts.LoadChapterPlanPartial(chapter); err != nil || partial != nil {
		return false
	}
	if partial, err := store.LoadChapterWorldSimulationPartial(chapter); err != nil || partial != nil {
		return false
	}
	plan, err := store.Drafts.LoadChapterPlan(chapter)
	if err != nil || plan == nil {
		return false
	}
	scope := domain.ChapterScope(chapter)
	_, pipelineErr := os.Stat(filepath.Join(store.Dir(), "meta", "pipeline.json"))
	checkpointStrict := pipelineErr == nil ||
		store.Checkpoints.LatestByStep(scope, "plan") != nil ||
		store.Checkpoints.LatestByStep(scope, "chapter_world_simulation") != nil
	if checkpointStrict {
		if _, err := toolspkg.CurrentChapterPlanCausalCheckpoint(store, chapter); err != nil {
			return false
		}
	}
	worldRequired, worldReady, _ := toolspkg.ChapterWorldSimulationStatus(store, chapter)
	if worldRequired && !worldReady {
		return false
	}
	if worldRequired {
		if simulation, simErr := store.LoadChapterWorldSimulation(chapter); simErr != nil || simulation == nil ||
			strings.TrimSpace(plan.CausalSimulation.WorldSimulationID) != strings.TrimSpace(simulation.SimulationID) {
			return false
		}
	}
	if toolspkg.ValidateChapterQuantityResultContract(store, *plan) != nil {
		return false
	}
	if toolspkg.ValidateChapterAntiAIExecutionPlanForCurrentRepair(store, *plan, isRewrite) != nil {
		return false
	}
	if !toolspkg.ChapterAttractionPlanReadyForProject(store, *plan) {
		return false
	}
	if !isRewrite {
		return true
	}
	if toolspkg.ValidateRewriteCraftPlanCurrent(store, *plan) != nil {
		return false
	}
	if renderOnlyReviewAllowsPlanReuse(store, chapter) {
		return true
	}
	body, err := store.Drafts.LoadChapterText(chapter)
	if err != nil || strings.TrimSpace(body) == "" {
		return false
	}
	briefPath := fmt.Sprintf("reviews/%02d_rewrite_brief.md", chapter)
	brief, err := os.ReadFile(filepath.Join(store.Dir(), filepath.FromSlash(briefPath)))
	if err != nil || len(brief) == 0 {
		return false
	}
	bodySum := sha256.Sum256([]byte(body))
	briefSum := sha256.Sum256(brief)
	bodyToken := fmt.Sprintf("rewrite_source:chapters/%02d.md#sha256=%x", chapter, bodySum)
	briefToken := fmt.Sprintf("rewrite_brief:%s#sha256=%x", briefPath, briefSum)
	return slices.Contains(plan.CausalSimulation.ContextSources, bodyToken) &&
		slices.Contains(plan.CausalSimulation.ContextSources, briefToken)
}

var planPreservingRenderRules = map[string]bool{
	"aigc_ratio":                    true,
	"abstract_system_reassurance":   true,
	"dialogue_semicolon_formality":  true,
	"dramatic_negation_overuse":     true,
	"isolated_sentence_overuse":     true,
	"micro_action_overuse":          true,
	"not_but_overuse":               true,
	"object_response_overuse":       true,
	"object_response_rhythm_flat":   true,
	"paragraph_start_repetition":    true,
	"semicolon_overuse":             true,
	"state_clause_pile":             true,
	"stiff_trade_dialogue":          true,
	"system_message_inline":         true,
	"system_message_overpacked":     true,
	"templated_dialogue_chain":      true,
	"too_many_isolated_short_lines": true,
}

// renderOnlyReviewAllowsPlanReuse identifies rewrites that change expression,
// paragraphing or dialogue presentation without changing the simulated world or
// protagonist decision. Such chapters may go straight to the plan-bound draft
// finalizer; factual, contract or character failures still require replanning.
func renderOnlyReviewAllowsPlanReuse(st *storepkg.Store, chapter int) bool {
	if st == nil || chapter <= 0 {
		return false
	}
	body, err := st.Drafts.LoadChapterText(chapter)
	if err != nil || strings.TrimSpace(body) == "" {
		return false
	}
	bodyHash := reviewreport.BodySHA256(body)
	review, err := st.World.LoadReview(chapter)
	if err != nil || review == nil || review.BodySHA256 != bodyHash || review.ContractStatus != "met" || len(review.ContractMisses) > 0 {
		return false
	}
	for _, dimension := range review.Dimensions {
		switch dimension.Dimension {
		case "consistency", "character", "pacing", "continuity", "foreshadow", "hook":
			if dimension.Verdict != "pass" {
				return false
			}
		}
	}
	for _, issue := range review.Issues {
		if issue.Type != "aesthetic" && issue.Type != "ai_voice_detection" {
			return false
		}
	}

	gate, _, err := reviewreport.LoadMechanicalGate(st.Dir(), chapter)
	if err != nil || gate == nil || gate.BodySHA256 != bodyHash || len(gate.RuleViolations) == 0 {
		return false
	}
	blocking := review.Verdict == "rewrite" || review.Verdict == "polish"
	if !blocking {
		if progress, loadErr := st.Progress.Load(); loadErr == nil && progress != nil &&
			progress.Flow == domain.FlowPolishing && slices.Contains(progress.PendingRewrites, chapter) {
			blocking = true
		}
	}
	for _, violation := range gate.RuleViolations {
		if !planPreservingRenderRules[violation.Rule] {
			return false
		}
		if violation.Severity == rules.SeverityError {
			blocking = true
		}
	}
	return blocking
}

// LoadState 从 Store 读取 Route 所需的全部事实。
// 这是路由的"IO 边界"：所有读取集中在这里，Route 保持纯。
// 读取失败按保守默认填充（has*=false, boundary=nil），让 Router 倾向重派而非跳过。
func LoadState(store *storepkg.Store) State {
	s := State{
		FoundationMissing: store.FoundationMissing(),
	}
	progress, err := store.Progress.Load()
	if err != nil || progress == nil {
		return s
	}
	s.Progress = progress

	if n := len(progress.CompletedChapters); n > 0 {
		s.LastCompleted = progress.CompletedChapters[n-1]
		s.HasChapterReview = store.World.HasAcceptedChapterReview(s.LastCompleted)
		s.UnreviewedChapter = store.World.FirstUnacceptedChapterReview(progress.CompletedChapters)
	}

	// 阶段拆分：判断下一个要处理章节的计划是否已就绪可渲染。
	if len(progress.PendingRewrites) > 0 {
		target := progress.PendingRewrites[0]
		s.NextActionPlanReady = chapterPlanReadyForDraft(store, target, true)
		escalation := toolspkg.InspectRenderOnlyReplanEscalation(store, target)
		s.NextActionStructuralReplanRequired = escalation.Required
		s.NextActionStructuralReplanAttempts = escalation.Attempts
		s.NextActionStructuralReplanLimit = escalation.Limit
		s.NextActionStructuralReplanReason = escalation.Reason
		if escalation.Required {
			s.NextActionPlanReady = false
		}
		s.NextActionExplicitRerender = toolspkg.ExplicitRerenderRequestActive(store, target)
		s.NextActionReviewRerenderRequired = toolspkg.ReviewRequiresFreshDraft(store, target)
		loadDraftExternalGateState(store, target, &s)
		rerenderReplacementApproved := toolspkg.ExplicitRerenderReplacementApproved(store, target)
		if !escalation.Required && !s.NextActionPlanReady && (s.NextActionExplicitRerender || s.NextActionDraftExternalRerenderRequired || rerenderReplacementApproved) && toolspkg.ValidateReusableCausalPlanForRerender(store, target) == nil {
			s.NextActionPlanReady = true
		}
		s.NextActionWorldSimulationRequired, s.NextActionWorldSimulationReady, s.NextActionWorldSimulationGaps = toolspkg.ChapterWorldSimulationStatus(store, target)
		if escalation.Required {
			s.NextActionWorldSimulationRequired = true
			s.NextActionWorldSimulationReady = false
			s.NextActionWorldSimulationGaps = append([]string{"render-only 结构失败已耗尽当前因果预算，必须建立新的 world simulation epoch"}, s.NextActionWorldSimulationGaps...)
		}
		s.NextActionDraftReady = s.NextActionPlanReady && !s.NextActionReviewRerenderRequired && chapterDraftReadyForFinalize(store, target)
		loadNextActionPlanStage(store, target, &s)
		loadNextActionOutline(store, target, &s)
	} else if next := progress.NextChapter(); next > 0 {
		s.NextActionPlanReady = chapterPlanReadyForDraft(store, next, false)
		escalation := toolspkg.InspectRenderOnlyReplanEscalation(store, next)
		s.NextActionStructuralReplanRequired = escalation.Required
		s.NextActionStructuralReplanAttempts = escalation.Attempts
		s.NextActionStructuralReplanLimit = escalation.Limit
		s.NextActionStructuralReplanReason = escalation.Reason
		if escalation.Required {
			s.NextActionPlanReady = false
		}
		s.NextActionWorldSimulationRequired, s.NextActionWorldSimulationReady, s.NextActionWorldSimulationGaps = toolspkg.ChapterWorldSimulationStatus(store, next)
		if escalation.Required {
			s.NextActionWorldSimulationRequired = true
			s.NextActionWorldSimulationReady = false
			s.NextActionWorldSimulationGaps = append([]string{"render-only 结构失败已耗尽当前因果预算，必须建立新的 world simulation epoch"}, s.NextActionWorldSimulationGaps...)
		}
		s.NextActionDraftReady = s.NextActionPlanReady && chapterDraftReadyForFinalize(store, next)
		loadDraftExternalGateState(store, next, &s)
		loadNextActionPlanStage(store, next, &s)
		loadNextActionOutline(store, next, &s)
	}

	s.BookCompleteByChapters = domain.StructurallyComplete(progress)
	if s.BookCompleteByChapters && !progress.Layered {
		meta, _ := store.RunMeta.Load()
		s.NeedsFinalGlobalReview = domain.RequiresFinalGlobalReview(progress, meta)
		s.HasFinalGlobalReview = store.World.HasAcceptedGlobalReview(progress.LatestCompleted())
	}

	// 弧边界仅在分层模式且有已完成章节时才计算
	if progress.Layered && s.LastCompleted > 0 {
		if boundary, berr := store.Outline.CheckArcBoundary(s.LastCompleted); berr == nil && boundary != nil {
			s.ArcBoundary = boundary
			if boundary.IsArcEnd {
				s.HasArcReview = store.World.HasArcReview(s.LastCompleted)
				s.HasArcSummary = store.Summaries.HasArcSummary(boundary.Volume, boundary.Arc)
				if boundary.IsVolumeEnd {
					s.HasVolumeSummary = store.Summaries.HasVolumeSummary(boundary.Volume)
				}
			}
		}
	}

	return s
}

func loadDraftExternalGateState(st *storepkg.Store, chapter int, state *State) {
	inspection, err := toolspkg.InspectDraftExternalGateWithStore(st, chapter)
	if err != nil {
		state.NextActionDraftExternalRejudgePending = true
		return
	}
	state.NextActionDraftExternalRerenderRequired = inspection.Status == toolspkg.DraftExternalGateRerenderAuthorized
	state.NextActionDraftLocalSoftEditPending = inspection.LocalSoftEditPending
	state.NextActionDraftNamedPassFrozen = inspection.Status == toolspkg.DraftExternalGateApproved && inspection.CurrentHashNamedRetestsPassed
	state.NextActionDraftExternalRejudgePending = !inspection.LocalSoftEditPending &&
		(inspection.Status == toolspkg.DraftExternalGateRejudgePending ||
			inspection.Status == toolspkg.DraftExternalGateAdviceIncomplete)
}

func chapterDraftReadyForFinalize(store *storepkg.Store, chapter int) bool {
	if store == nil || chapter <= 0 {
		return false
	}
	draft, err := store.Drafts.LoadDraft(chapter)
	if err != nil || strings.TrimSpace(draft) == "" {
		return false
	}
	plan, err := toolspkg.CurrentChapterPlanCausalCheckpoint(store, chapter)
	if err != nil {
		return false
	}
	scope := domain.ChapterScope(chapter)
	latestPlanSeq := plan.Seq
	if request := store.Checkpoints.LatestByStep(scope, "rerender-request"); request != nil && request.Seq > latestPlanSeq {
		latestPlanSeq = request.Seq
	}
	body, err := toolspkg.CurrentChapterBodyCheckpoint(store, chapter)
	if err != nil {
		return false
	}
	return body.Seq > latestPlanSeq
}

func loadNextActionPlanStage(store *storepkg.Store, chapter int, state *State) {
	if store == nil || state == nil || chapter <= 0 {
		return
	}
	if partial, err := store.Drafts.LoadChapterPlanPartial(chapter); err == nil && partial != nil {
		state.NextActionPlanPartial = true
	}
	if meta, err := store.RunMeta.Load(); err == nil && meta != nil {
		steer := strings.TrimSpace(meta.PendingSteer)
		if strings.HasPrefix(steer, "Pipeline staged-plan repair") || strings.HasPrefix(steer, "Pipeline world-simulation repair") {
			state.NextActionPlanRepairTask = steer
		}
	}
}

func loadNextActionOutline(store *storepkg.Store, chapter int, state *State) {
	if store == nil || state == nil || chapter <= 0 {
		return
	}
	entry, err := store.Outline.GetChapterOutline(chapter)
	if err != nil || entry == nil {
		return
	}
	state.NextActionTitle = entry.Title
	state.NextActionCoreEvent = entry.CoreEvent
	state.NextActionHook = entry.Hook
}
