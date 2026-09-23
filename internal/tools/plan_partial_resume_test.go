package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/errs"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func resumePartialFixture(t *testing.T) (*store.Store, map[string]any, json.RawMessage) {
	t.Helper()
	st := newPhaseTestStore(t)
	saveRewriteCraftIndex(t, st, "partial-resume")
	if err := st.Outline.SaveOutline([]domain.OutlineEntry{{Chapter: 1, Title: "测试章", CoreEvent: "推进剧情", Hook: "留下悬念"}}); err != nil {
		t.Fatal(err)
	}
	_, stateToken := installPlanningContextAccessProjectAll(t, st, 1, "resume-owner")
	oldToken := readPlanningContextAccessToken(t, st, 1, "planning")
	var full map[string]any
	if err := json.Unmarshal(planArgs(1), &full); err != nil {
		t.Fatal(err)
	}
	causal := full["causal_simulation"].(map[string]any)
	delete(full, "causal_simulation")
	full["goal"] = "完整兑现本章大纲核心事件：推进剧情"
	causal["render_capacity"] = toolsTestRenderCapacity(700, 700, 700)
	causal["arc_transition_contract"] = map[string]any{"outgoing_consequence_id": "resume-1", "outgoing_consequence_text": "当前选择留下下一章必须处理的后果"}
	causal["context_sources"] = append(stringSliceFromAny(causal["context_sources"]), stateToken, oldToken)
	partial := map[string]any{"structure": full, "causal_simulation": causal, "rewrite": false}
	if err := st.Drafts.SaveChapterPlanPartial(1, partial); err != nil {
		t.Fatal(err)
	}
	raw, err := NewContextTool(st, References{}, "").Execute(context.Background(), json.RawMessage(`{"chapter":1,"profile":"planning"}`))
	if err != nil {
		t.Fatal(err)
	}
	return st, partial, raw
}

func TestPlanDetailsResumeCompletePartialWithoutResubmission(t *testing.T) {
	st, partial, raw := resumePartialFixture(t)
	full := partial["structure"].(map[string]any)
	causal := partial["causal_simulation"].(map[string]any)
	before := full["goal"]
	expected, err := chapterPlanFromPartial(1, partial, causal)
	if err != nil {
		t.Fatal(err)
	}
	result, attempted, err := NewPlanDetailsTool(st).ResumePartial(context.Background(), 1, raw)
	if err != nil || !attempted {
		t.Fatalf("resume did not finalize existing fields: attempted=%v err=%v", attempted, err)
	}
	var got struct {
		Planned bool `json:"planned"`
	}
	if err := json.Unmarshal(result, &got); err != nil || !got.Planned {
		t.Fatalf("not a real finalized result: %s %v", result, err)
	}
	plan, err := st.Drafts.LoadChapterPlan(1)
	if err != nil || plan == nil || plan.Goal != before {
		t.Fatalf("saved work was lost: %v %v", plan, err)
	}
	if !reflect.DeepEqual(plan.CausalSimulation.CausalBeats, expected.CausalSimulation.CausalBeats) {
		t.Fatal("resume reauthored causal beats")
	}
	if partial, _ := st.Drafts.LoadChapterPlanPartial(1); partial != nil {
		t.Fatal("successful plan remained partial")
	}
	if cp := st.Checkpoints.LatestByStep(domain.ChapterScope(1), "plan"); cp == nil {
		t.Fatal("claimed completion without checkpoint")
	}
}

func TestPlanDetailsResumeReviewerFailurePreservesPaidFields(t *testing.T) {
	for _, cause := range []error{context.Canceled, errors.New("provider unavailable"), fmt.Errorf("exact grounding budget: %w", errs.ErrToolPrecondition)} {
		st, partial, raw := resumePartialFixture(t)
		// A legacy planning source suffices to exercise reviewer dispatch: the
		// resolver fails before a provider/source input is needed. No fake pass.
		if err := st.SaveChapterWorldSimulation(domain.ChapterWorldSimulation{Chapter: 1, Sources: []string{domain.PlanGroundingPolicyV1}}); err != nil {
			t.Fatal(err)
		}
		calls := 0
		reviewer := PlanGroundingReviewer{ResolveForSimulation: func(domain.ChapterWorldSimulation) (PlanGroundingReviewer, error) {
			calls++
			return PlanGroundingReviewer{}, cause
		}}
		_, attempted, err := NewPlanDetailsTool(st).WithGroundingReviewer(reviewer).ResumePartial(context.Background(), 1, raw)
		if !attempted || !errors.Is(err, cause) || PlanDetailsResumeNeedsRepair(err) || calls != 1 {
			t.Fatalf("review failure was hidden/retried: attempted=%v calls=%d err=%v", attempted, calls, err)
		}
		after, _ := st.Drafts.LoadChapterPlanPartial(1)
		if !reflect.DeepEqual(after["structure"], partial["structure"]) {
			t.Fatal("failed review overwrote structure")
		}
		beforePlan, _ := chapterPlanFromPartial(1, partial, partial["causal_simulation"].(map[string]any))
		afterPlan, _ := chapterPlanFromPartial(1, after, after["causal_simulation"].(map[string]any))
		if !reflect.DeepEqual(beforePlan.CausalSimulation.CausalBeats, afterPlan.CausalSimulation.CausalBeats) {
			t.Fatal("failed review overwrote causal beats")
		}
		if plan, _ := st.Drafts.LoadChapterPlan(1); plan != nil {
			t.Fatal("failed review claimed formal plan")
		}
		if cp := st.Checkpoints.LatestByStep(domain.ChapterScope(1), "plan"); cp != nil {
			t.Fatal("failed review advanced checkpoint")
		}
		receipt, _ := st.Runtime.LoadPlanningContextAccessReceipt(domain.PlanningContextAccessPlan)
		if receipt == nil || !receipt.ConsumedAt.IsZero() {
			t.Fatal("failed review consumed context receipt")
		}
	}
}

func TestPlanDetailsResumeIncompleteRetainsPartialWithoutReviewer(t *testing.T) {
	st, partial, raw := resumePartialFixture(t)
	delete(partial["causal_simulation"].(map[string]any), "initial_state")
	if err := st.Drafts.SaveChapterPlanPartial(1, partial); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveChapterWorldSimulation(domain.ChapterWorldSimulation{Chapter: 1, Sources: []string{domain.PlanGroundingPolicyV1}}); err != nil {
		t.Fatal(err)
	}
	calls := 0
	_, attempted, err := NewPlanDetailsTool(st).WithGroundingReviewer(PlanGroundingReviewer{ResolveForSimulation: func(domain.ChapterWorldSimulation) (PlanGroundingReviewer, error) {
		calls++
		return PlanGroundingReviewer{}, errors.New("must not review missing fields")
	}}).ResumePartial(context.Background(), 1, raw)
	if !attempted || !PlanDetailsResumeNeedsRepair(err) || !strings.Contains(err.Error(), "initial_state") || calls != 0 {
		t.Fatalf("missing fields did not route to bounded repair: %v calls=%d", err, calls)
	}
	if after, _ := st.Drafts.LoadChapterPlanPartial(1); after == nil {
		t.Fatal("incomplete attempt lost paid partial")
	}
	if plan, _ := st.Drafts.LoadChapterPlan(1); plan != nil {
		t.Fatal("incomplete attempt created plan")
	}
}

func TestPlanDetailsResumeMissingFieldsAreRepairableButInfrastructureIsNot(t *testing.T) {
	st := newPhaseTestStore(t)
	err := validateChapterPrewriteSimulation(st, domain.ChapterPlan{Chapter: 1}, false)
	if err == nil || !PlanDetailsResumeNeedsRepair(err) {
		t.Fatalf("deterministic missing fields need a partial repair: %v", err)
	}
	for _, cause := range []error{context.Canceled, errors.New("provider unavailable"), fmt.Errorf("budget exceeded: %w", errs.ErrToolPrecondition), fmt.Errorf("missing source: %w", errs.ErrStoreRead)} {
		wrapped := planDetailsFinalizeRepairError(1, map[string]any{"project_promise": "keep"}, cause)
		if PlanDetailsResumeNeedsRepair(wrapped) || !errors.Is(wrapped, cause) {
			t.Fatalf("infrastructure failure became repairable: %v", wrapped)
		}
	}
	if !PlanDetailsResumeNeedsRepair(planDetailsFinalizeRepairError(1, nil, &planGroundingRejectionError{cause: errors.New("actual verified finding")})) {
		t.Fatal("verified grounding finding lost repair classification")
	}
}

func TestPlanDetailsResumeRejectsPreviousProcessReceiptBeforeChangingPartial(t *testing.T) {
	st := newPhaseTestStore(t)
	_, _ = installPlanningContextAccessProjectAll(t, st, 1, "resume-identity")
	oldToken := readPlanningContextAccessToken(t, st, 1, "planning")
	receipt, err := st.Runtime.LoadPlanningContextAccessReceipt(domain.PlanningContextAccessPlan)
	if err != nil {
		t.Fatal(err)
	}
	receipt.LockProcessID = os.Getpid() + 100000
	receipt.ReceiptDigest, err = domain.ComputePlanningContextAccessReceiptDigest(*receipt)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Runtime.SavePlanningContextAccessReceipt(*receipt); err != nil {
		t.Fatal(err)
	}
	partial := map[string]any{"structure": map[string]any{"chapter": 1, "goal": "keep"}, "causal_simulation": map[string]any{"context_sources": []string{oldToken}}}
	if err := st.Drafts.SaveChapterPlanPartial(1, partial); err != nil {
		t.Fatal(err)
	}
	packet, _ := json.Marshal(map[string]any{"active_chapter_task": map[string]any{"mode": "staged_plan_repair", "chapter": 1}, "structure_source_status": "ready", "planning_context_access_receipt": map[string]any{"source_token": oldToken}})
	_, attempted, err := NewPlanDetailsTool(st).ResumePartial(context.Background(), 1, packet)
	if !attempted || err == nil || !strings.Contains(err.Error(), "stale execution identity") {
		t.Fatalf("old process receipt accepted: %v %v", attempted, err)
	}
	before, _ := json.Marshal(partial)
	afterPartial, _ := st.Drafts.LoadChapterPlanPartial(1)
	after, _ := json.Marshal(afterPartial)
	if string(before) != string(after) {
		t.Fatal("receipt failure mutated partial")
	}
	// A new successful context read issues the current process identity; old
	// echoed tokens cannot authorize it but also do not invalidate its proof.
	freshToken := readPlanningContextAccessToken(t, st, 1, "planning")
	if freshToken == oldToken {
		t.Fatal("test did not issue a fresh receipt")
	}
	if err := consumePlanningContextAccessReceipt(st, 1, domain.PlanningContextAccessPlan, []string{oldToken}); err != nil {
		t.Fatalf("fresh server identity failed with old echo: %v", err)
	}
}

func TestPlanDetailsResumeFreshChapterAndCancellationCannotFinalize(t *testing.T) {
	st := newPhaseTestStore(t)
	result, attempted, err := NewPlanDetailsTool(st).ResumePartial(context.Background(), 1, json.RawMessage(`{"chapter_world_simulation":{"status":"ready"}}`))
	if result != nil || attempted || err != nil {
		t.Fatalf("fresh chapter changed route: %s %v %v", result, attempted, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, attempted, err = NewPlanDetailsTool(st).ResumePartial(ctx, 1, json.RawMessage(`{"active_chapter_task":{"mode":"staged_plan_repair","chapter":1},"structure_source_status":"ready"}`))
	if !attempted || !errors.Is(err, context.Canceled) || PlanDetailsResumeNeedsRepair(err) {
		t.Fatalf("cancellation changed to planner repair: %v", err)
	}
	if p, _ := st.Drafts.LoadChapterPlan(1); p != nil {
		t.Fatal("non-success generated plan")
	}
}
