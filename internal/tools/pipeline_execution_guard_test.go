package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func TestRenderExecutionLockRejectsAllPlanningToolsForTargetChapter(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init("执行锁测试", 10); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.StartChapter(4); err != nil {
		t.Fatal(err)
	}
	if err := st.Runtime.AcquirePipelineExecution(domain.PipelineExecutionLock{
		Mode:          domain.PipelineExecutionRender,
		TargetChapter: 4,
		PlanDigest:    "sha256:locked-plan",
		Owner:         "render-test",
	}); err != nil {
		t.Fatal(err)
	}

	calls := []struct {
		name string
		run  func() error
	}{
		{
			name: "plan_chapter",
			run: func() error {
				_, err := NewPlanChapterTool(st).Execute(context.Background(), json.RawMessage(`{"chapter":4}`))
				return err
			},
		},
		{
			name: "plan_structure",
			run: func() error {
				_, err := NewPlanStructureTool(st).Execute(context.Background(), json.RawMessage(`{"chapter":4}`))
				return err
			},
		},
		{
			name: "plan_details",
			run: func() error {
				_, err := NewPlanDetailsTool(st).Execute(context.Background(), json.RawMessage(`{"chapter":4}`))
				return err
			},
		},
		{
			name: "simulate_chapter_world",
			run: func() error {
				_, err := NewSimulateChapterWorldTool(st).Execute(context.Background(), json.RawMessage(`{"chapter":4}`))
				return err
			},
		},
		{
			name: "save_foundation",
			run: func() error {
				_, err := NewSaveFoundationTool(st).Execute(context.Background(), json.RawMessage(`{"type":"expand_arc"}`))
				return err
			},
		},
		{
			name: "save_world_tick",
			run: func() error {
				_, err := NewSaveWorldTickTool(st).Execute(context.Background(), json.RawMessage(`{"through_chapter":4}`))
				return err
			},
		},
	}
	for _, call := range calls {
		t.Run(call.name, func(t *testing.T) {
			err := call.run()
			if err == nil || !strings.Contains(err.Error(), "render execution lock") || !strings.Contains(err.Error(), call.name) {
				t.Fatalf("%s should be blocked by render execution lock: %v", call.name, err)
			}
		})
	}
	if _, err := NewPlanStructureTool(st).Execute(context.Background(), json.RawMessage(`{}`)); err == nil ||
		!strings.Contains(err.Error(), "render execution lock") ||
		!strings.Contains(err.Error(), "plan_structure") {
		t.Fatalf("plan_structure with inferred target chapter should be blocked: %v", err)
	}
}

func TestPreplanExecutionLockRejectsAllProseMutationToolsForTargetChapter(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Runtime.AcquirePipelineExecution(domain.PipelineExecutionLock{
		Mode:          domain.PipelineExecutionPreplan,
		TargetChapter: 4,
		Owner:         "preplan-test",
	}); err != nil {
		t.Fatal(err)
	}

	calls := []struct {
		name string
		run  func() error
	}{
		{
			name: "draft_chapter",
			run: func() error {
				_, err := NewDraftChapterTool(st).Execute(context.Background(), json.RawMessage(`{"chapter":4,"content":"待写正文","mode":"write"}`))
				return err
			},
		},
		{
			name: "edit_chapter",
			run: func() error {
				_, err := NewEditChapterTool(st).Execute(context.Background(), json.RawMessage(`{"chapter":4,"old_string":"旧","new_string":"新"}`))
				return err
			},
		},
		{
			name: "commit_chapter",
			run: func() error {
				_, err := NewCommitChapterTool(st).Execute(context.Background(), json.RawMessage(`{"chapter":4}`))
				return err
			},
		},
		{
			name: "draft_chapter_part",
			run: func() error {
				_, err := NewDraftChapterPartTool(st).Execute(context.Background(), json.RawMessage(`{"chapter":4}`))
				return err
			},
		},
		{
			name: "merge_chapter_parts",
			run: func() error {
				_, err := NewMergeChapterPartsTool(st).Execute(context.Background(), json.RawMessage(`{"chapter":4}`))
				return err
			},
		},
	}
	for _, call := range calls {
		t.Run(call.name, func(t *testing.T) {
			err := call.run()
			if err == nil || !strings.Contains(err.Error(), "preplan execution lock") || !strings.Contains(err.Error(), call.name) {
				t.Fatalf("%s should be blocked by preplan execution lock: %v", call.name, err)
			}
		})
	}
}

func TestPipelineExecutionLockIsGlobalPhaseBarrier(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Runtime.AcquirePipelineExecution(domain.PipelineExecutionLock{
		Mode:          domain.PipelineExecutionPreplan,
		TargetChapter: 8,
		Owner:         "scope-test",
	}); err != nil {
		t.Fatal(err)
	}
	if err := guardPipelineProseExecution(st, 7, "draft_chapter"); err == nil || !strings.Contains(err.Error(), "推演阶段不得") {
		t.Fatalf("preplan must block prose for every chapter: %v", err)
	}
	if err := guardPipelinePlanningExecution(st, 8, "plan_chapter"); err != nil {
		t.Fatalf("preplan target should allow planning: %v", err)
	}
	if err := guardPipelinePlanningExecution(st, 7, "plan_chapter"); err != nil {
		t.Fatalf("preplan may deliberately plan another chapter: %v", err)
	}

	if err := st.Runtime.ReleasePipelineExecution("scope-test"); err != nil {
		t.Fatal(err)
	}
	if err := st.Drafts.SaveChapterPlan(domain.ChapterPlan{Chapter: 8, Title: "锁定计划"}); err != nil {
		t.Fatal(err)
	}
	cp, err := st.Checkpoints.AppendArtifactLatest(domain.ChapterScope(8), "plan", "drafts/08.plan.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Runtime.AcquirePipelineExecution(domain.PipelineExecutionLock{
		Mode:          domain.PipelineExecutionRender,
		TargetChapter: 8,
		PlanDigest:    cp.Digest,
		Owner:         "render-scope-test",
	}); err != nil {
		t.Fatal(err)
	}
	if err := guardPipelinePlanningExecution(st, 7, "plan_chapter"); err == nil || !strings.Contains(err.Error(), "不得推演") {
		t.Fatalf("render must block planning for every chapter: %v", err)
	}
	if err := guardPipelineProseExecution(st, 7, "draft_chapter"); err == nil || !strings.Contains(err.Error(), "只授权第 8 章") {
		t.Fatalf("render must block prose outside its exact target: %v", err)
	}
}

func TestRenderExecutionBindsProseToExactFormalPlanDigest(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Drafts.SaveChapterPlan(domain.ChapterPlan{Chapter: 2, Title: "锁定计划"}); err != nil {
		t.Fatal(err)
	}
	cp, err := st.Checkpoints.AppendArtifactLatest(domain.ChapterScope(2), "plan", "drafts/02.plan.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Runtime.AcquirePipelineExecution(domain.PipelineExecutionLock{
		Mode:          domain.PipelineExecutionRender,
		TargetChapter: 2,
		PlanDigest:    cp.Digest,
		Owner:         "render-digest-test",
	}); err != nil {
		t.Fatal(err)
	}
	if err := guardPipelineProseExecution(st, 2, "draft_chapter"); err != nil {
		t.Fatalf("exact locked plan should allow prose stage: %v", err)
	}

	if err := st.Drafts.SaveChapterPlan(domain.ChapterPlan{Chapter: 2, Title: "计划被改写"}); err != nil {
		t.Fatal(err)
	}
	if err := guardPipelineProseExecution(st, 2, "draft_chapter"); err == nil || !strings.Contains(err.Error(), "无法验证正式 plan") {
		t.Fatalf("mutated formal plan must invalidate render lock: %v", err)
	}
}

func TestPipelineExecutionNoLockPreservesDefaultBehavior(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := guardPipelinePlanningExecution(st, 1, "plan_chapter"); err != nil {
		t.Fatalf("no-lock planning changed behavior: %v", err)
	}
	if err := guardPipelineProseExecution(st, 1, "draft_chapter"); err != nil {
		t.Fatalf("no-lock prose changed behavior: %v", err)
	}
}
