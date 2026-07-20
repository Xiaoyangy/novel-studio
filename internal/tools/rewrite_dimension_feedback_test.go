package tools

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/reviewreport"
	"github.com/chenhongyang/novel-studio/internal/store"
)

const rewriteDimensionRuleDiagnostics = "catalog_stuffing：拆散清单；dialogue_conveyor_overuse：加入有效打断；pov_interiority_thin：补足判断变化；semicolon_overuse：改成口语停顿；dramatic_negation_overuse：删除对仗否定；object_response_rhythm_flat：改变回应节拍。"

func TestRefreshRewriteBriefIncludesActionableDimensionComment(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	const body = "第三章\n\n门外的七个地址被她逐个核对。"
	if err := st.Drafts.SaveFinalChapter(3, body); err != nil {
		t.Fatal(err)
	}
	review := domain.ReviewEntry{
		Chapter: 3,
		Scope:   "chapter",
		Verdict: "rewrite",
		Summary: "表达层返工。",
		Issues: []domain.ConsistencyIssue{{
			Type: "ai_voice_detection", Severity: "error", Description: "详见第8维。",
		}},
		Dimensions: []domain.DimensionScore{
			{Dimension: "consistency", Score: 95, Verdict: "pass", Comment: "事实与因果均通过，不得改变七址结果。"},
			{Dimension: "ai_voice_detection", Score: 62, Verdict: "warning", Comment: rewriteDimensionRuleDiagnostics},
		},
	}
	path, err := refreshRewriteBriefFromReview(st, review, "rewrite")
	if err != nil {
		t.Fatal(err)
	}
	brief, err := st.Drafts.LoadRewriteBrief(3)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		path,
		"[ai_voice_detection/error] 详见第8维。",
		"[维度:ai_voice_detection/warning/62] " + rewriteDimensionRuleDiagnostics,
		"catalog_stuffing",
		"object_response_rhythm_flat",
	} {
		if !strings.Contains(path+"\n"+brief, want) {
			t.Fatalf("rewrite brief lost detailed dimension feedback %q:\n%s", want, brief)
		}
	}
	if strings.Contains(brief, "事实与因果均通过，不得改变七址结果") {
		t.Fatalf("passing causal dimension leaked into expression feedback:\n%s", brief)
	}
	corrections := rewriteBriefTopLevelBullets(brief, "必须修正")
	if !sliceContainsSubstring(corrections, "object_response_rhythm_flat") {
		t.Fatalf("dimension feedback was not a top-level planning correction: %#v", corrections)
	}
}

func TestSealedRerenderFeedbackRecoversDimensionCommentMissingFromOldBrief(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	plan := domain.ChapterPlan{Chapter: 3, Title: "冻结计划"}
	if err := st.Drafts.SaveChapterPlan(plan); err != nil {
		t.Fatal(err)
	}
	planCheckpoint, err := st.Checkpoints.AppendArtifactLatest(
		domain.ChapterScope(3), "plan", "drafts/03.plan.json",
	)
	if err != nil {
		t.Fatal(err)
	}
	const body = "第三章\n\n门外的七个地址被她逐个核对。"
	if err := st.Drafts.SaveDraft(3, body); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Checkpoints.AppendArtifactLatestAcross(
		domain.ChapterScope(3), "draft", "drafts/03.draft.md",
		"plan", "rerender-request", "draft", "edit",
	); err != nil {
		t.Fatal(err)
	}
	if err := st.Drafts.SaveFinalChapter(3, body); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init("dimension feedback recovery", 4); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.StartChapter(3); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.MarkChapterComplete(3, len([]rune(body)), "mystery", "delivery"); err != nil {
		t.Fatal(err)
	}
	bodySHA := reviewreport.BodySHA256(body)
	review := domain.ReviewEntry{
		Chapter: 3, Scope: "chapter", BodySHA256: bodySHA,
		Verdict: "rewrite", ContractStatus: "met", Summary: "详见第8维。",
		Issues: []domain.ConsistencyIssue{{
			Type: "ai_voice_detection", Severity: "error", Description: "详见第8维。",
		}},
		Dimensions: []domain.DimensionScore{{
			Dimension: "ai_voice_detection", Score: 62, Verdict: "warning", Comment: rewriteDimensionRuleDiagnostics,
		}},
	}
	if err := st.World.SaveReview(review); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.SetPendingRewritesAndFlow([]int{3}, review.Summary, domain.FlowRewriting); err != nil {
		t.Fatal(err)
	}
	// Model a brief already written by the old implementation: it is exact-body
	// bound but contains only the generic cross-reference.
	oldBrief := "# ch03 rewrite brief\n\n- 待返工正文 SHA-256：`" + bodySHA + "`。\n- 详见第8维。\n"
	if err := st.Drafts.SaveRewriteBrief(3, oldBrief); err != nil {
		t.Fatal(err)
	}

	raw, err := NewContextTool(st, References{}, "default").attachSealedRerenderFeedback(
		json.RawMessage(`{"render_packet":{"chapter":3}}`), 3, planCheckpoint.Digest,
	)
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Feedback struct {
			PlanDigest string                       `json:"plan_digest"`
			Dimensions []rewriteDimensionDiagnostic `json:"blocking_dimension_feedback"`
		} `json:"sealed_rerender_feedback"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Feedback.PlanDigest != planCheckpoint.Digest || len(payload.Feedback.Dimensions) != 1 {
		t.Fatalf("sealed feedback lost plan binding or dimension diagnosis: %+v", payload.Feedback)
	}
	got := payload.Feedback.Dimensions[0]
	if got.Dimension != "ai_voice_detection" || got.Comment != rewriteDimensionRuleDiagnostics {
		t.Fatalf("sealed feedback did not recover exact dimension comment: %+v", got)
	}
}

func sliceContainsSubstring(values []string, needle string) bool {
	for _, value := range values {
		if strings.Contains(value, needle) {
			return true
		}
	}
	return false
}
