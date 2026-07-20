package tools

import (
	"testing"

	"github.com/chenhongyang/novel-studio/internal/rules"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func saveTestProjectContaminationTerms(t *testing.T, st *store.Store, terms ...string) {
	t.Helper()
	if err := st.UserRules.Save(&rules.Snapshot{
		Version: rules.SnapshotVersion,
		Status:  rules.StatusReady,
		Structured: rules.Structured{
			ForbiddenPhrases: terms,
		},
		Sources: []string{"project:test-boundaries.md"},
	}); err != nil {
		t.Fatalf("save project contamination test rules: %v", err)
	}
}

func TestProjectContaminationPolicyUsesOnlyCurrentUserRules(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Characters.Save(nil); err != nil {
		t.Fatal(err)
	}

	const forbidden = "外部项目专名"
	if got := ProjectContaminationViolations(st, forbidden); got != nil {
		t.Fatalf("character cards or empty rules unexpectedly activated policy: %+v", got)
	}
	saveTestProjectContaminationTerms(t, st, forbidden)
	got := ProjectContaminationViolations(st, "正文误用了"+forbidden)
	if len(got) != 1 || got[0].Target != forbidden || got[0].Rule != "project_contamination" {
		t.Fatalf("explicit current-project boundary not enforced: %+v", got)
	}
	if got := ProjectContaminationViolations(st, "当前项目事实"); got != nil {
		t.Fatalf("unconfigured current-project text rejected: %+v", got)
	}
}

func TestSanitizeProjectDiagnosticUsesCurrentRules(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	saveTestProjectContaminationTerms(t, st, "外部项目专名")
	got := sanitizeProjectDiagnosticForPlan(st, "删除外部项目专名，保留当前人物")
	if got != "删除[项目禁用元素]，保留当前人物" {
		t.Fatalf("unexpected sanitized diagnostic: %q", got)
	}
}
