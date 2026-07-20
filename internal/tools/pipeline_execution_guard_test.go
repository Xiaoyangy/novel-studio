package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/rules"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/userrules"
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
		{
			name: "save_user_rules",
			run: func() error {
				svc := userrules.NewService(st, nil, rules.LoadOptions{})
				_, err := NewSaveUserRulesTool(svc, st).Execute(context.Background(), json.RawMessage(`{"text":"每章不少于两千字"}`))
				return err
			},
		},
		{
			name: "reopen_book",
			run: func() error {
				_, err := NewReopenBookTool(st).Execute(context.Background(), json.RawMessage(`{"chapters":[1]}`))
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

func TestCheckConsistencyIsSerializedAndRejectsWrongRenderTarget(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	tool := NewCheckConsistencyTool(st)
	args := json.RawMessage(`{"chapter":3}`)
	if tool.ReadOnly(args) || tool.ConcurrencySafe(args) {
		t.Fatal("check_consistency persists receipts/checkpoints and must be serialized as a write tool")
	}
	if err := st.Runtime.AcquirePipelineExecution(domain.PipelineExecutionLock{
		Mode:          domain.PipelineExecutionRender,
		TargetChapter: 4,
		PlanDigest:    "sha256:locked-plan",
		Owner:         "render-consistency-test",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := tool.Execute(context.Background(), args); err == nil ||
		!strings.Contains(err.Error(), "只授权第 4 章") ||
		!strings.Contains(err.Error(), "check_consistency") {
		t.Fatalf("cross-target consistency check should be rejected before any receipt write: %v", err)
	}
}

func TestRenderConsistencyContextCannotReloadLiveCanon(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if allowed, err := consistencyMayLoadLiveContext(st); err != nil || !allowed {
		t.Fatalf("normal consistency diagnostics should use current canon: allowed=%v err=%v", allowed, err)
	}
	if err := st.Runtime.AcquirePipelineExecution(domain.PipelineExecutionLock{
		Mode:          domain.PipelineExecutionRender,
		TargetChapter: 1,
		PlanDigest:    "sha256:frozen",
		Owner:         "frozen-consistency-test",
	}); err != nil {
		t.Fatal(err)
	}
	if allowed, err := consistencyMayLoadLiveContext(st); err != nil || allowed {
		t.Fatalf("render consistency reloaded live canon: allowed=%v err=%v", allowed, err)
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

func TestPreplanExecutionLockRejectsAllGlobalMutationTools(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Runtime.AcquirePipelineExecution(domain.PipelineExecutionLock{
		Mode:          domain.PipelineExecutionPreplan,
		TargetChapter: 4,
		Owner:         "preplan-global-write-test",
	}); err != nil {
		t.Fatal(err)
	}

	calls := []struct {
		name string
		run  func() error
	}{
		{
			name: "save_user_rules",
			run: func() error {
				svc := userrules.NewService(st, nil, rules.LoadOptions{})
				_, err := NewSaveUserRulesTool(svc, st).Execute(context.Background(), json.RawMessage(`{"text":"每章不少于两千字"}`))
				return err
			},
		},
		{
			name: "reopen_book",
			run: func() error {
				_, err := NewReopenBookTool(st).Execute(context.Background(), json.RawMessage(`{"chapters":[1]}`))
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
			if err == nil ||
				!strings.Contains(err.Error(), "planning execution lock") ||
				!strings.Contains(err.Error(), call.name) {
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

func TestFoundationExecutionLockAllowsOnlyFoundationMutation(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Runtime.AcquirePipelineExecution(domain.PipelineExecutionLock{
		Mode:          domain.PipelineExecutionFoundation,
		TargetChapter: 1,
		Owner:         "foundation-test",
	}); err != nil {
		t.Fatal(err)
	}
	if err := guardPipelineGlobalPlanningExecution(st, "save_foundation"); err != nil {
		t.Fatalf("foundation lock rejected foundation mutation: %v", err)
	}
	if err := guardPipelineGlobalPlanningExecution(st, "reopen_book"); err == nil ||
		!strings.Contains(err.Error(), "不得借此改写 progress") {
		t.Fatalf("foundation lock allowed reopen_book capability escalation: %v", err)
	}
	if err := guardPipelinePlanningExecution(st, 1, "plan_chapter"); err == nil ||
		!strings.Contains(err.Error(), "试图提前规划") {
		t.Fatalf("foundation lock allowed formal chapter planning: %v", err)
	}
	if err := guardPipelineProseExecution(st, 1, "draft_chapter"); err == nil ||
		!strings.Contains(err.Error(), "基础阶段不得") {
		t.Fatalf("foundation lock allowed prose mutation: %v", err)
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

func TestPipelineExecutionPromoteLockRejectsPlanningProseAndGlobalMutation(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Runtime.AcquirePipelineExecution(domain.PipelineExecutionLock{
		Mode:          domain.PipelineExecutionPromote,
		TargetChapter: 1,
		Owner:         "promote-guard-test",
	}); err != nil {
		t.Fatal(err)
	}
	if err := guardPipelinePlanningExecution(st, 1, "plan_chapter"); err == nil || !strings.Contains(err.Error(), "promote execution lock") {
		t.Fatalf("promote lock must reject planning: %v", err)
	}
	if err := guardPipelineProseExecution(st, 1, "draft_chapter"); err == nil || !strings.Contains(err.Error(), "promote execution lock") {
		t.Fatalf("promote lock must reject prose: %v", err)
	}
	if err := guardPipelineGlobalPlanningExecution(st, "save_world_tick"); err == nil || !strings.Contains(err.Error(), "promote execution lock") {
		t.Fatalf("promote lock must reject global mutation: %v", err)
	}
}

func TestPipelineExecutionGuardsRejectAnotherProcessCapability(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Runtime.AcquirePipelineExecution(domain.PipelineExecutionLock{
		Mode:          domain.PipelineExecutionProjectAll,
		TargetChapter: 1,
		Owner:         "owner-process",
	}); err != nil {
		t.Fatal(err)
	}
	lock, err := st.Runtime.LoadPipelineExecution()
	if err != nil || lock == nil {
		t.Fatalf("load execution lock: lock=%+v err=%v", lock, err)
	}
	otherPID := os.Getppid()
	if otherPID <= 0 || otherPID == os.Getpid() {
		t.Skip("test process has no distinct live parent PID")
	}
	lock.ProcessID = otherPID
	raw, err := json.MarshalIndent(lock, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw, '\n')
	lockPath := filepath.Join(st.Dir(), "meta", "runtime", "pipeline_execution.json")
	if err := os.WriteFile(lockPath, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := guardPipelinePlanningExecution(st, 1, "plan_chapter"); err == nil ||
		!strings.Contains(err.Error(), "不得借用该执行能力") {
		t.Fatalf("another process borrowed project-all planning capability: %v", err)
	}

	lock.Mode = domain.PipelineExecutionRender
	lock.PlanDigest = "sha256:other-process-plan"
	raw, err = json.MarshalIndent(lock, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw, '\n')
	if err := os.WriteFile(lockPath, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := guardPipelineProseExecution(st, 1, "draft_chapter"); err == nil ||
		!strings.Contains(err.Error(), "不得借用该执行能力") {
		t.Fatalf("another process borrowed render prose capability: %v", err)
	}
}
