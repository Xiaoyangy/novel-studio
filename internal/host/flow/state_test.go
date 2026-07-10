package flow

import (
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
