package tools

import (
	"fmt"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/reviewreport"
	"github.com/chenhongyang/novel-studio/internal/store"
)

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
	latestDraftSeq := int64(0)
	for _, step := range []string{"draft", "edit"} {
		if cp := s.Checkpoints.LatestByStep(domain.ChapterScope(chapter), step); cp != nil && cp.Seq > latestDraftSeq {
			latestDraftSeq = cp.Seq
		}
	}
	return request.Seq >= latestDraftSeq
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
	latestDraftSeq := int64(0)
	for _, step := range []string{"draft", "edit"} {
		if cp := s.Checkpoints.LatestByStep(scope, step); cp != nil && cp.Seq > latestDraftSeq {
			latestDraftSeq = cp.Seq
		}
	}
	if latestDraftSeq <= request.Seq {
		return false
	}
	inspection, err := InspectDraftExternalGate(s.Dir(), chapter)
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
	if ValidateReusableCausalPlanForRerender(s, chapter) != nil {
		return false
	}
	if ExplicitRerenderRequestActive(s, chapter) {
		return true
	}
	inspection, err := InspectDraftExternalGate(s.Dir(), chapter)
	if err == nil && inspection.Status == DraftExternalGateRerenderAuthorized {
		return true
	}
	return ReviewRequiresFreshDraft(s, chapter)
}

// ValidateReusableCausalPlanForRerender verifies that an explicit render-only
// request can safely reuse the existing world simulation and POV plan. Source
// body/brief hashes may be stale because prose review happened after planning;
// all character decisions, the protagonist projection and the result contract
// still have to be complete and mutually bound.
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
	sim, err := s.LoadChapterWorldSimulation(chapter)
	if err != nil {
		return err
	}
	if sim == nil || sim.Chapter != chapter || strings.TrimSpace(sim.SimulationID) == "" {
		return fmt.Errorf("第 %d 章缺少可复用的全角色世界推演", chapter)
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
		strings.TrimSpace(causal.ProtagonistDecision) != strings.TrimSpace(sim.ProtagonistProjection.ChosenDecision) {
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
	return strings.HasPrefix(gap, "rewrite_source does not match current committed body and rewrite brief") ||
		strings.HasPrefix(gap, "rewrite_fact_coverage missing:")
}
