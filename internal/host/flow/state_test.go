package flow

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
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
