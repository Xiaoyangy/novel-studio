package tools

import (
	"fmt"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/reviewreport"
	"github.com/chenhongyang/novel-studio/internal/store"
)

const (
	defaultRenderOnlyReplanLimit = 3
	minRenderOnlyReplanLimit     = 2
	maxRenderOnlyReplanLimit     = 4
)

// RenderOnlyReplanEscalation explains why repeated whole-draft rendering can
// no longer make useful progress against the current causal plan. Attempts are
// distinct draft hashes after the newest plan/simulation causal boundary;
// retrying the same hash never consumes the budget. A candidate commit happens
// before formal review and therefore cannot reset a plan-owned retry budget. A rerender
// request is authorization, not a causal reset, so repeating --force-rerender
// cannot erase attempts accumulated against the same plan.
type RenderOnlyReplanEscalation struct {
	Required bool
	Attempts int
	Limit    int
	Reason   string
}

// InspectRenderOnlyReplanEscalation upgrades a render-only loop to causal
// replanning after the plan's own review_refinement iteration budget is
// exhausted. Newer builds persist draft-structural-block checkpoints. The
// draft-checkpoint fallback intentionally supports projects that accumulated
// failures before that checkpoint existed: a current, same-hash whole-text
// structural block proves that the uncommitted drafts after the boundary are
// failed render attempts rather than accepted revisions.
func InspectRenderOnlyReplanEscalation(s *store.Store, chapter int) RenderOnlyReplanEscalation {
	result := RenderOnlyReplanEscalation{Limit: renderOnlyReplanLimit(s, chapter)}
	if s == nil || chapter <= 0 {
		return result
	}

	scope := domain.ChapterScope(chapter)
	boundary := renderOnlyCausalBoundary(s, chapter)

	structural := map[string]struct{}{}
	drafts := map[string]struct{}{}
	for _, cp := range s.Checkpoints.All() {
		if cp.Seq <= boundary || !cp.Scope.Matches(scope) || strings.TrimSpace(cp.Digest) == "" {
			continue
		}
		switch cp.Step {
		case "draft-structural-block":
			structural[cp.Digest] = struct{}{}
		case "draft":
			drafts[cp.Digest] = struct{}{}
		}
	}
	result.Attempts = len(structural)
	if result.Attempts >= result.Limit {
		result.Required = true
		result.Reason = fmt.Sprintf("同一因果计划下已有 %d 个不同整章哈希触发 whole-text/segment 结构阻断，达到迭代上限 %d", result.Attempts, result.Limit)
		return result
	}
	// Once this causal epoch contains any explicit structural checkpoint, that
	// journal is authoritative. Mixing its exact failures with the legacy draft-
	// count heuristic would relabel passing drafts and bounded soft edits as
	// structural failures, prematurely exhausting the render budget.
	if len(structural) > 0 {
		return result
	}

	// Compatibility path for runs created before draft-structural-block was
	// introduced. It is used only when this epoch has no explicit structural
	// evidence at all. A current same-hash, advice-complete rerender marker plus
	// an independently reproduced whole-text/segment blocker may then promote
	// the uncommitted draft history to structural attempts.
	if len(drafts) < result.Limit || !currentDraftHasWholeTextStructuralBlock(s, chapter) {
		return result
	}
	result.Attempts = len(drafts)
	result.Required = true
	result.Reason = fmt.Sprintf("同一因果计划下已有 %d 个不同未提交整章稿，当前哈希仍触发 whole-text/segment 结构阻断，达到迭代上限 %d", result.Attempts, result.Limit)
	return result
}

// renderOnlyCausalBoundary is the latest checkpoint that can legitimately
// grant a fresh rendering budget. Only a new plan or world simulation changes
// the causal projection. Candidate commits happen before formal Editor review;
// treating each one as a reset was the source of an unbounded
// render->commit->review->rewrite loop. Explicit rerender requests likewise do
// not qualify because they reuse the existing causal projection.
func renderOnlyCausalBoundary(s *store.Store, chapter int) int64 {
	if s == nil || chapter <= 0 {
		return 0
	}
	scope := domain.ChapterScope(chapter)
	var boundary int64
	for _, step := range []string{"plan", "chapter_world_simulation"} {
		if cp := s.Checkpoints.LatestByStep(scope, step); cp != nil && cp.Seq > boundary {
			boundary = cp.Seq
		}
	}
	return boundary
}

// renderOnlyCausalEpochKey makes structural-attempt idempotence local to the
// current causal cycle. CheckpointStore de-duplicates a digest across all
// history, so the body hash alone would lose a legitimate attempt when the
// same old prose reappears after a genuinely new plan or simulation.
func renderOnlyCausalEpochKey(s *store.Store, chapter int) string {
	if s == nil || chapter <= 0 {
		return "initial"
	}
	boundary := renderOnlyCausalBoundary(s, chapter)
	if boundary == 0 {
		return "initial"
	}
	for _, cp := range s.Checkpoints.All() {
		if cp.Seq != boundary || !cp.Scope.Matches(domain.ChapterScope(chapter)) {
			continue
		}
		if digest := strings.TrimSpace(cp.Digest); digest != "" {
			return cp.Step + ":" + digest
		}
		return fmt.Sprintf("%s:seq:%d", cp.Step, cp.Seq)
	}
	return fmt.Sprintf("seq:%d", boundary)
}

func renderOnlyReplanLimit(s *store.Store, chapter int) int {
	limit := defaultRenderOnlyReplanLimit
	if s != nil && chapter > 0 {
		if plan, err := s.Drafts.LoadChapterPlan(chapter); err == nil && plan != nil && plan.CausalSimulation.ReviewRefinement.IterationLimit > 0 {
			limit = plan.CausalSimulation.ReviewRefinement.IterationLimit
		}
	}
	if limit < minRenderOnlyReplanLimit {
		return minRenderOnlyReplanLimit
	}
	if limit > maxRenderOnlyReplanLimit {
		return maxRenderOnlyReplanLimit
	}
	return limit
}

func currentDraftHasWholeTextStructuralBlock(s *store.Store, chapter int) bool {
	inspection, err := InspectDraftExternalGateWithStore(s, chapter)
	if err != nil || inspection.Status != DraftExternalGateRerenderAuthorized || inspection.Requirement == nil {
		return false
	}
	content, err := s.Drafts.LoadDraft(chapter)
	if err != nil || strings.TrimSpace(content) == "" {
		return false
	}
	report, gate := inspectDraftAIGCGate(s, chapter, content)
	return draftAIGCHasWholeTextStructuralBlock(content, report, gate)
}

// ExplicitRerenderRequestActive is true until a newer draft/edit checkpoint
// consumes the request.
func ExplicitRerenderRequestActive(s *store.Store, chapter int) bool {
	if s == nil || chapter <= 0 {
		return false
	}
	request := s.Checkpoints.LatestByStep(domain.ChapterScope(chapter), "rerender-request")
	if request == nil {
		return false
	}
	body, err := CurrentChapterBodyCheckpoint(s, chapter)
	if err != nil {
		return true // conservative: an unbound/mutated draft cannot consume a request.
	}
	return request.Seq >= body.Seq
}

// ExplicitRerenderReplacementApproved is true after a rerender request has
// been consumed by a newer draft and that exact replacement hash has passed
// the provider-backed whole-draft gate. The Host must finalize this draft
// against the reusable causal plan instead of treating stale rewrite-source
// hashes as a reason to simulate or plan again.
func ExplicitRerenderReplacementApproved(s *store.Store, chapter int) bool {
	if s == nil || chapter <= 0 {
		return false
	}
	scope := domain.ChapterScope(chapter)
	request := s.Checkpoints.LatestByStep(scope, "rerender-request")
	if request == nil {
		return false
	}
	body, err := CurrentChapterBodyCheckpoint(s, chapter)
	if err != nil || body.Seq <= request.Seq {
		return false
	}
	inspection, err := InspectDraftExternalGateWithStore(s, chapter)
	return err == nil && inspection.Status == DraftExternalGateApproved
}

// ReviewRequiresFreshDraft identifies a blocking formal review that still
// describes the exact committed body and whose draft is byte-identical to that
// body. Retrying commit cannot make progress; the next prose action must create
// a different draft hash while reusing the approved causal plan when eligible.
func ReviewRequiresFreshDraft(s *store.Store, chapter int) bool {
	if s == nil || chapter <= 0 {
		return false
	}
	draft, draftErr := s.Drafts.LoadDraft(chapter)
	final, finalErr := s.Drafts.LoadChapterText(chapter)
	if draftErr != nil || finalErr != nil || strings.TrimSpace(draft) == "" || draft != final {
		return false
	}
	return BlockingReviewRejectsBody(s, chapter, final)
}

// BlockingReviewRejectsBody reports whether content exactly reproduces the
// body rejected by the current formal review. A renderer may return to an
// older, externally approved hash; that is still not a fresh rewrite when the
// Editor's blocking verdict names the same hash.
func BlockingReviewRejectsBody(s *store.Store, chapter int, content string) bool {
	if s == nil || chapter <= 0 || strings.TrimSpace(content) == "" {
		return false
	}
	review, err := s.World.LoadReview(chapter)
	if err != nil || review == nil || review.BodySHA256 != reviewreport.BodySHA256(content) {
		return false
	}
	return review.Verdict == "rewrite" || review.Verdict == "polish"
}

// RenderOnlyRerenderReady covers both a human-requested full render and a
// blocking same-hash draft judgment. Both reuse the validated simulation/plan
// and must not fall back to planning merely because source hashes are older.
func RenderOnlyRerenderReady(s *store.Store, chapter int) bool {
	if InspectRenderOnlyReplanEscalation(s, chapter).Required {
		return false
	}
	if ValidateReusableCausalPlanForRerender(s, chapter) != nil {
		return false
	}
	if ExplicitRerenderRequestActive(s, chapter) {
		return true
	}
	inspection, err := InspectDraftExternalGateWithStore(s, chapter)
	if err == nil && inspection.Status == DraftExternalGateRerenderAuthorized {
		return true
	}
	return ReviewRequiresFreshDraft(s, chapter)
}

// ValidateReusableCausalPlanForRerender verifies that an explicit render-only
// request can safely reuse the existing world simulation and POV plan. Only
// the committed prose body's surface identity may be stale because rendering
// and review can happen after planning. The rewrite brief, canonical outcome
// ledger, preserve-fact coverage and live chapter instruction remain causal
// inputs and therefore must still match exactly.
func ValidateReusableCausalPlanForRerender(s *store.Store, chapter int) error {
	if s == nil || chapter <= 0 {
		return fmt.Errorf("invalid chapter %d", chapter)
	}
	if partial, err := s.Drafts.LoadChapterPlanPartial(chapter); err != nil {
		return err
	} else if partial != nil {
		return fmt.Errorf("第 %d 章仍有 plan.partial，不能跳过规划修复", chapter)
	}
	if partial, err := s.LoadChapterWorldSimulationPartial(chapter); err != nil {
		return err
	} else if partial != nil {
		return fmt.Errorf("第 %d 章仍有 world simulation partial，不能跳过推演修复", chapter)
	}
	plan, err := s.Drafts.LoadChapterPlan(chapter)
	if err != nil {
		return err
	}
	if plan == nil || plan.Chapter != chapter || strings.TrimSpace(plan.Title) == "" {
		return fmt.Errorf("第 %d 章缺少可复用的正式 POV plan", chapter)
	}
	if err := ValidateChapterAntiAIExecutionPlanForCurrentRepair(s, *plan, true); err != nil {
		return fmt.Errorf("第 %d 章现有 plan 的反 AIGC 执行合同不完整，不能用 render-only 绕过重规划：%w", chapter, err)
	}
	scope := domain.ChapterScope(chapter)
	// Historical/imported render-only fixtures may predate checkpoint
	// journaling. Preserve that compatibility only when neither side of the
	// simulation->plan epoch has ever been journaled; once either checkpoint
	// exists, require an exact current plan after the latest simulation.
	if s.Checkpoints.LatestByStep(scope, "plan") != nil ||
		s.Checkpoints.LatestByStep(scope, "chapter_world_simulation") != nil {
		if _, err := CurrentChapterPlanCausalCheckpoint(s, chapter); err != nil {
			return fmt.Errorf("第 %d 章现有 plan 尚未消费当前 world simulation epoch，不能用 render-only 绕过重规划：%w", chapter, err)
		}
	}
	if err := ValidateChapterQuantityResultContract(s, *plan); err != nil {
		return fmt.Errorf("第 %d 章现有 plan 的章节数量结果合同已漂移，不能用 render-only 绕过重规划：%w", chapter, err)
	}
	if err := validateRewriteCraftConsumption(s, *plan); err != nil {
		return fmt.Errorf("第 %d 章现有 plan 的 craft receipt 已失效，不能用 render-only 绕过重规划：%w", chapter, err)
	}
	if !ChapterAttractionPlanReadyForProject(s, *plan) {
		return fmt.Errorf("第 %d 章现有 plan 不满足当前项目 attraction contract，不能用 render-only 授权绕过重规划", chapter)
	}
	sim, err := s.LoadChapterWorldSimulation(chapter)
	if err != nil {
		return err
	}
	if sim == nil || sim.Chapter != chapter || strings.TrimSpace(sim.SimulationID) == "" {
		return fmt.Errorf("第 %d 章缺少可复用的全角色世界推演", chapter)
	}
	if err := validateReusableCausalInputs(s, *plan, *sim); err != nil {
		return fmt.Errorf("第 %d 章现有 simulation/plan 的返工因果输入已失效，不能用 render-only 绕过重推演：%w", chapter, err)
	}
	hardGaps := reusableChapterWorldSimulationGaps(s, *sim)
	if len(hardGaps) > 0 {
		return fmt.Errorf("第 %d 章世界推演存在不可忽略缺口：%s", chapter, strings.Join(hardGaps, "；"))
	}
	causal := plan.CausalSimulation
	if strings.TrimSpace(causal.WorldSimulationID) != strings.TrimSpace(sim.SimulationID) {
		return fmt.Errorf("第 %d 章 plan 未绑定当前 world_simulation_id=%s", chapter, sim.SimulationID)
	}
	if strings.TrimSpace(causal.ProtagonistDecision) == "" ||
		strings.TrimSpace(causal.ProtagonistDecision) != effectiveProtagonistDecision(sim.ProtagonistProjection) {
		return fmt.Errorf("第 %d 章 plan 主角决定与世界推演投影不一致", chapter)
	}
	if !contextSourcesContain(causal.ContextSources, sim.SimulationID) ||
		!contextSourcesContain(causal.ContextSources, "chapter_world_simulation") {
		return fmt.Errorf("第 %d 章 plan 缺少世界推演来源绑定", chapter)
	}
	if len(plan.Contract.RequiredBeats) == 0 || len(causal.CausalBeats) == 0 || len(causal.OutcomeShift) == 0 {
		return fmt.Errorf("第 %d 章 plan 缺少结果合同、因果节拍或章末状态变化", chapter)
	}
	return nil
}

// validateReusableCausalInputs narrows the render-only compatibility window:
// a newer prose body is harmless, but every non-prose input that shaped the
// simulation must remain current. This explicit check runs before the legacy
// gap filter below, so that filter cannot turn a canonical-state, fact-coverage
// or instruction drift into permission to render against an obsolete plan.
func validateReusableCausalInputs(s *store.Store, plan domain.ChapterPlan, sim domain.ChapterWorldSimulation) error {
	currentSource, _, _, err := loadChapterRewriteSource(s, plan.Chapter)
	if err != nil {
		return fmt.Errorf("无法读取当前 rewrite source：%w", err)
	}
	if currentSource != nil {
		if sim.RewriteSource == nil {
			return fmt.Errorf("world simulation 缺少当前 rewrite source")
		}
		if err := validateReusableRewriteSource(currentSource, sim.RewriteSource); err != nil {
			return err
		}
		if gaps := rewriteFactCoverageIntegrityGaps(currentSource.PreserveFacts, sim.RewriteFactCoverage); len(gaps) > 0 {
			return fmt.Errorf("rewrite_fact_coverage 与当前 preserve facts 不一致：%s", strings.Join(gaps, "；"))
		}
		covered := make(map[string]domain.ChapterRewriteFactCoverage, len(sim.RewriteFactCoverage))
		for _, item := range sim.RewriteFactCoverage {
			covered[rewriteFactIdentity(item.Fact)] = item
		}
		for _, fact := range currentSource.PreserveFacts {
			item, ok := covered[rewriteFactIdentity(fact)]
			if !ok || len(compactStrings(item.SimulationEvidence)) == 0 {
				return fmt.Errorf("rewrite_fact_coverage 未完整覆盖当前 preserve fact：%s", fact)
			}
		}
	}

	instruction, err := loadChapterPipelineInstruction(s, plan.Chapter)
	if err != nil {
		return fmt.Errorf("当前 chapter_pipeline_instruction 无法校验：%w", err)
	}
	if instruction == nil {
		return nil
	}
	if !sourceTokenPresent(sim.Sources, instruction.Token) {
		return fmt.Errorf("world simulation.sources 缺少当前 chapter_pipeline_instruction binding")
	}
	if !sourceTokenPresent(plan.CausalSimulation.ContextSources, instruction.Token) {
		return fmt.Errorf("plan context_sources 缺少当前 chapter_pipeline_instruction binding")
	}
	return nil
}

// validateReusableRewriteSource deliberately ignores only BodySHA256 and
// WordCount. A body rewritten after the causal plan may still reuse that plan;
// changing any other source identity means the causal premises changed.
func validateReusableRewriteSource(current, simulated *domain.ChapterRewriteSource) error {
	if current == nil || simulated == nil {
		return fmt.Errorf("rewrite source 不完整")
	}
	if strings.TrimSpace(simulated.BodyPath) != strings.TrimSpace(current.BodyPath) {
		return fmt.Errorf("rewrite source body path 已变化：current=%s simulated=%s", current.BodyPath, simulated.BodyPath)
	}
	if strings.TrimSpace(simulated.CanonicalStatePath) != strings.TrimSpace(current.CanonicalStatePath) ||
		strings.TrimSpace(simulated.CanonicalStateSHA256) != strings.TrimSpace(current.CanonicalStateSHA256) {
		return fmt.Errorf("canonical chapter_progress 结果已变化：current=%s#sha256=%s simulated=%s#sha256=%s",
			current.CanonicalStatePath, current.CanonicalStateSHA256,
			simulated.CanonicalStatePath, simulated.CanonicalStateSHA256)
	}
	if !stringSlicesEqual(simulated.PreserveFacts, current.PreserveFacts) {
		return fmt.Errorf("rewrite source preserve facts 已变化，必须重新完成 rewrite_fact_coverage")
	}
	if strings.TrimSpace(simulated.BriefPath) != strings.TrimSpace(current.BriefPath) ||
		strings.TrimSpace(simulated.BriefSHA256) != strings.TrimSpace(current.BriefSHA256) {
		return fmt.Errorf("rewrite brief 已变化：current=%s#sha256=%s simulated=%s#sha256=%s",
			current.BriefPath, current.BriefSHA256, simulated.BriefPath, simulated.BriefSHA256)
	}
	return nil
}

func sourceTokenPresent(sources []string, token string) bool {
	token = strings.TrimSpace(token)
	if token == "" {
		return false
	}
	for _, source := range sources {
		if strings.TrimSpace(source) == token {
			return true
		}
	}
	return false
}

func reusableChapterWorldSimulationGaps(s *store.Store, sim domain.ChapterWorldSimulation) []string {
	var hardGaps []string
	for _, gap := range chapterWorldSimulationGaps(s, sim) {
		if rerenderMayIgnoreSourceVersionGap(gap) {
			continue
		}
		hardGaps = append(hardGaps, gap)
	}
	return hardGaps
}

func rerenderMayIgnoreSourceVersionGap(gap string) bool {
	gap = strings.TrimSpace(gap)
	// validateReusableCausalInputs has already proven that the generic source
	// mismatch is body-only, every current fact is covered, and both artifacts
	// carry the live instruction token. Filter the coarser legacy gap strings
	// here only to avoid rejecting those explicitly validated exceptions.
	return strings.HasPrefix(gap, "rewrite_source does not match current committed body and rewrite brief") ||
		strings.HasPrefix(gap, "rewrite_fact_coverage missing:") ||
		strings.HasPrefix(gap, "chapter_pipeline_instruction source missing:")
}
