package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func TestProjectAllFoundationAndPlanningDependenciesSealStoryTimeContract(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Outline.SaveOutline([]domain.OutlineEntry{{
		Chapter: 1, Title: "开端", CoreEvent: "主角作出第一个不可撤销选择。",
	}}); err != nil {
		t.Fatal(err)
	}
	if err := st.World.SaveWorldRules([]domain.WorldRule{{Category: "time", Rule: "时间单向推进。"}}); err != nil {
		t.Fatal(err)
	}
	if err := st.Outline.SaveCompass(domain.StoryCompass{EndingDirection: "完成时间合同"}); err != nil {
		t.Fatal(err)
	}
	contract := domain.StoryTimeContract{
		Source:                domain.StoryTimeSourceOutlineAll,
		TargetChapters:        4,
		DurationDaysMin:       7,
		DurationDaysMax:       9,
		NominalDaysPerChapter: 2,
	}
	if err := st.WorldSim.SaveStoryTimeContract(contract); err != nil {
		t.Fatal(err)
	}
	if err := st.WorldSim.SaveStoryCalendar(domain.StoryCalendar{DaysPerChapter: 2}); err != nil {
		t.Fatal(err)
	}

	before, err := pipelineProjectAllFoundationSnapshotRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	contract.ArcSchedule = []domain.StoryTimeArcSchedule{{
		Volume: 1, Arc: 1, StartChapter: 1, EndChapter: 4, StartDay: 0, EndDay: 8,
	}}
	if err := st.WorldSim.SaveStoryTimeContract(contract); err != nil {
		t.Fatal(err)
	}
	after, err := pipelineProjectAllFoundationSnapshotRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	if before == after {
		t.Fatal("project-all foundation root did not bind story_time_contract bytes")
	}

	dependencies, _, err := pipelinePlanningDependencies(dir)
	if err != nil {
		t.Fatal(err)
	}
	hasContract := false
	hasCalendar := false
	for _, dependency := range dependencies {
		hasContract = hasContract || dependency.ID == "meta/story_time_contract.json"
		hasCalendar = hasCalendar || dependency.ID == "meta/story_calendar.json"
	}
	if !hasContract || !hasCalendar {
		t.Fatalf("planning dependencies omitted time artifacts: %+v", dependencies)
	}

	workspace := filepath.Join(t.TempDir(), "workspace")
	if err := copyProjectAllWorkspace(dir, workspace); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"meta/story_time_contract.json", "meta/story_calendar.json"} {
		if _, err := os.Stat(filepath.Join(workspace, filepath.FromSlash(rel))); err != nil {
			t.Fatalf("project-all workspace omitted %s: %v", rel, err)
		}
	}
}
