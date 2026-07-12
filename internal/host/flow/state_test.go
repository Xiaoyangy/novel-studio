package flow

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/reviewreport"
	"github.com/chenhongyang/novel-studio/internal/rules"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func TestChapterPlanReadyForDraftRejectsStaleAttractionPlan(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	if err := s.Progress.Init("test", 128); err != nil {
		t.Fatal(err)
	}
	if err := s.UserRules.Save(&rules.Snapshot{
		Structured:  rules.Structured{Genre: "都市脑洞轻松搞笑爽文"},
		Preferences: "长篇连载，热梗可少量点缀",
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Drafts.SaveChapterPlan(domain.ChapterPlan{Chapter: 1, Title: "失业饭桌"}); err != nil {
		t.Fatal(err)
	}
	if chapterPlanReadyForDraft(s, 1, false) {
		t.Fatal("stale plan without attraction contract must route back to planner")
	}
}

func TestRenderOnlyReviewReusesCurrentCausalPlan(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	body := "第一章正文。\n\n【额度和限制与任务全挤在这里。】"
	if err := s.Drafts.SaveFinalChapter(1, body); err != nil {
		t.Fatal(err)
	}
	if err := s.Drafts.SaveChapterPlan(domain.ChapterPlan{Chapter: 1, Title: "第一章"}); err != nil {
		t.Fatal(err)
	}
	bodyHash := reviewreport.BodySHA256(body)
	review := domain.ReviewEntry{
		Chapter: 1, BodySHA256: bodyHash, Scope: "chapter", ContractStatus: "met", Verdict: "rewrite",
		Dimensions: []domain.DimensionScore{
			{Dimension: "consistency", Verdict: "pass"},
			{Dimension: "character", Verdict: "pass"},
			{Dimension: "pacing", Verdict: "pass"},
			{Dimension: "continuity", Verdict: "pass"},
			{Dimension: "foreshadow", Verdict: "pass"},
			{Dimension: "hook", Verdict: "pass"},
		},
	}
	if err := s.World.SaveReview(review); err != nil {
		t.Fatal(err)
	}
	writeGate := func(rule string) {
		t.Helper()
		payload := reviewreport.MechanicalGatePayload{
			Chapter: 1, BodySHA256: bodyHash,
			RuleViolations: []rules.Violation{{Rule: rule, Severity: rules.SeverityError}},
		}
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(s.Dir(), "reviews", "01_ai_gate.json")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, raw, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	writeGate("system_message_overpacked")
	if !renderOnlyReviewAllowsPlanReuse(s, 1) || !chapterPlanReadyForDraft(s, 1, true) {
		t.Fatal("render-only system dialogue fix should reuse the current causal plan")
	}
	writeGate("semicolon_overuse")
	if !renderOnlyReviewAllowsPlanReuse(s, 1) || !chapterPlanReadyForDraft(s, 1, true) {
		t.Fatal("render-only punctuation fix should reuse the current causal plan")
	}
	writeGate("pending_resource_as_fact")
	if renderOnlyReviewAllowsPlanReuse(s, 1) || chapterPlanReadyForDraft(s, 1, true) {
		t.Fatal("resource/fact failures must return to causal replanning")
	}
}

func TestChapterPlanReadyForDraftRejectsStaleRewriteBrief(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	body := "第二章正文。"
	if err := s.Drafts.SaveFinalChapter(2, body); err != nil {
		t.Fatal(err)
	}
	briefPath := filepath.Join(s.Dir(), "reviews", "02_rewrite_brief.md")
	if err := os.MkdirAll(filepath.Dir(briefPath), 0o755); err != nil {
		t.Fatal(err)
	}
	brief := []byte("# rewrite\n\n旧要求")
	if err := os.WriteFile(briefPath, brief, 0o644); err != nil {
		t.Fatal(err)
	}
	bodySum := sha256.Sum256([]byte(body))
	briefSum := sha256.Sum256(brief)
	plan := domain.ChapterPlan{Chapter: 2, Title: "第二章", CausalSimulation: domain.ChapterCausalSimulation{
		ContextSources: []string{
			fmt.Sprintf("rewrite_source:chapters/02.md#sha256=%x", bodySum),
			fmt.Sprintf("rewrite_brief:reviews/02_rewrite_brief.md#sha256=%x", briefSum),
		},
	}}
	if err := s.Drafts.SaveChapterPlan(plan); err != nil {
		t.Fatal(err)
	}
	if !chapterPlanReadyForDraft(s, 2, true) {
		t.Fatal("plan bound to current body and brief should be ready")
	}
	if err := os.WriteFile(briefPath, []byte("# rewrite\n\n用户新增要求"), 0o644); err != nil {
		t.Fatal(err)
	}
	if chapterPlanReadyForDraft(s, 2, true) {
		t.Fatal("changed rewrite brief must invalidate the old plan")
	}
}

func TestChapterDraftReadyForFinalizeHonorsExplicitRerenderRequest(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	if err := s.Drafts.SaveChapterPlan(domain.ChapterPlan{Chapter: 2, Title: "第二章"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Checkpoints.AppendArtifact(domain.ChapterScope(2), "plan", "drafts/02.plan.json"); err != nil {
		t.Fatal(err)
	}
	if err := s.Drafts.SaveDraft(2, "第二章\n\n旧草稿"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Checkpoints.AppendArtifact(domain.ChapterScope(2), "draft", "drafts/02.draft.md"); err != nil {
		t.Fatal(err)
	}
	if !chapterDraftReadyForFinalize(s, 2) {
		t.Fatal("draft newer than plan should be ready")
	}
	requestPath := filepath.Join(s.Dir(), "drafts", "02.rerender_request.json")
	if err := os.WriteFile(requestPath, []byte(`{"chapter":2,"reason":"human readability"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Checkpoints.AppendArtifact(domain.ChapterScope(2), "rerender-request", "drafts/02.rerender_request.json"); err != nil {
		t.Fatal(err)
	}
	if chapterDraftReadyForFinalize(s, 2) {
		t.Fatal("explicit rerender request must make the old draft stale")
	}
	if err := s.Drafts.SaveDraft(2, "第二章\n\n新草稿"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Checkpoints.AppendArtifact(domain.ChapterScope(2), "draft", "drafts/02.draft.md"); err != nil {
		t.Fatal(err)
	}
	if !chapterDraftReadyForFinalize(s, 2) {
		t.Fatal("new draft after rerender request should be ready")
	}
}
