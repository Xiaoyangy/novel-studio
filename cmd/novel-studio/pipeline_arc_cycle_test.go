package main

import (
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func TestLocatePipelineArcScopeAndRequireBoundary(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	volumes := []domain.VolumeOutline{{
		Index: 1,
		Title: "第一卷",
		Theme: "县城重新开门",
		Arcs: []domain.ArcOutline{
			{Index: 1, Title: "桥先堵住", Goal: "从个人解围走到公共选择", Chapters: makeArcEntries(3)},
			{Index: 2, Title: "摊主自己接单", Goal: "把代办能力交还给普通人", Chapters: makeArcEntries(2)},
		},
	}}
	if err := st.Outline.SaveLayeredOutline(volumes); err != nil {
		t.Fatal(err)
	}
	scope, err := locatePipelineArcScope(st, 2)
	if err != nil {
		t.Fatal(err)
	}
	if scope.Volume != 1 || scope.Arc != 1 || scope.FirstChapter != 1 ||
		scope.LastChapter != 3 || scope.BookLastChapter != 5 {
		t.Fatalf("scope=%+v", scope)
	}
	if _, err := requirePipelineArcStart(st, 1); err == nil {
		t.Fatal("mid-arc base unexpectedly unlocked a new arc projection")
	}
	if next, err := requirePipelineArcStart(st, 3); err != nil || next.Arc != 2 {
		t.Fatalf("arc boundary did not unlock V1A2: scope=%+v err=%v", next, err)
	}
	if err := requirePipelineCompletedArcBoundary(st, 2); err == nil {
		t.Fatal("non-terminal chapter accepted as completed arc boundary")
	}
	if err := requirePipelineCompletedArcBoundary(st, 3); err != nil {
		t.Fatal(err)
	}
}

func TestLocatePipelineArcScopeRejectsSkeletonArc(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Outline.SaveLayeredOutline([]domain.VolumeOutline{{
		Index: 1,
		Title: "第一卷",
		Theme: "主题",
		Arcs: []domain.ArcOutline{{
			Index: 1, Title: "未展开", Goal: "不能正式推演", EstimatedChapters: 3,
		}},
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := locatePipelineArcScope(st, 1); err == nil {
		t.Fatal("skeleton arc unexpectedly accepted as formal planning scope")
	}
}

func makeArcEntries(count int) []domain.OutlineEntry {
	out := make([]domain.OutlineEntry, count)
	for i := range out {
		out[i] = domain.OutlineEntry{
			Title:     "章节",
			CoreEvent: "人物在具体阻力下作出选择并承担后果",
			Hook:      "新的具体行动开始",
			Scenes:    []string{"现场冲突发生并改变人物选择"},
		}
	}
	return out
}
