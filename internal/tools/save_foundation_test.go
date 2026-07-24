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
)

func TestSaveFoundationAllowsRebasedChapterZeroOutlineReplacement(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	progress := &domain.Progress{
		NovelName:         "测试",
		Phase:             domain.PhaseWriting,
		CurrentChapter:    1,
		InProgressChapter: 1,
		TotalChapters:     12,
		GenerationID:      "generation-rebased",
	}
	if err := s.Progress.Save(progress); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "meta"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "meta", "all_chapter_rebase.json"), []byte(`{"new_generation_id":"generation-rebased"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]any{
		"type":  "layered_outline",
		"scale": "short",
		"content": []map[string]any{{
			"index": 1, "title": "第一卷", "theme": "主题",
			"arcs": []map[string]any{{
				"index": 1, "title": "第一弧", "goal": "目标",
				"chapters": []map[string]any{{"chapter": 1, "title": "第一章", "core_event": "开局", "hook": "继续"}},
			}},
		}},
	})
	if _, err := NewSaveFoundationTool(s).Execute(context.Background(), args); err != nil {
		t.Fatalf("rebased chapter-zero outline replacement rejected: %v", err)
	}
}

func TestSaveFoundationStillBlocksWritingOutlineReplacementWithoutRebaseAuthority(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	if err := s.Progress.Save(&domain.Progress{Phase: domain.PhaseWriting, CurrentChapter: 1, GenerationID: "ordinary"}); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]any{
		"type":    "outline",
		"scale":   "short",
		"content": []map[string]any{{"chapter": 1, "title": "第一章", "core_event": "开局", "hook": "继续"}},
	})
	if _, err := NewSaveFoundationTool(s).Execute(context.Background(), args); err == nil || !strings.Contains(err.Error(), "禁止使用") {
		t.Fatalf("ordinary writing outline replacement was not blocked: %v", err)
	}
}

func TestSaveFoundationAllowsExplicitLockedChapterZeroArchitectRefresh(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	if err := s.Progress.Save(&domain.Progress{
		Phase:             domain.PhaseWriting,
		CurrentChapter:    1,
		InProgressChapter: 1,
		TotalChapters:     12,
	}); err != nil {
		t.Fatal(err)
	}
	seedChapterZeroRefreshFoundation(t, s)
	const owner = "architect-refresh-test"
	if err := s.Runtime.AcquirePipelineExecution(domain.PipelineExecutionLock{
		Mode: domain.PipelineExecutionFoundation, TargetChapter: 1, Owner: owner,
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Runtime.ReleasePipelineExecution(owner) })
	args, _ := json.Marshal(map[string]any{
		"type":  "layered_outline",
		"scale": "short",
		"content": []map[string]any{{
			"index": 1, "title": "第一卷", "theme": "主题",
			"arcs": []map[string]any{{
				"index": 1, "title": "第一弧", "goal": "目标",
				"chapters": []map[string]any{{"chapter": 1, "title": "第一章", "core_event": "开局", "hook": "继续"}},
			}},
		}},
	})
	tool := NewSaveFoundationTool(s).
		WithChapterZeroFoundationRefresh(true).
		WithFoundationTypeRestriction("layered_outline").
		WithFoundationRefreshEpoch(true).
		WithOneShotFoundationRefresh(true)
	if _, err := tool.Execute(context.Background(), args); err != nil {
		t.Fatalf("explicit locked chapter-zero refresh rejected: %v", err)
	}
	firstEpoch := s.Checkpoints.LatestByStep(domain.GlobalScope(), FoundationRefreshCheckpointStep("layered_outline"))
	if firstEpoch == nil {
		t.Fatal("explicit refresh did not append its causal epoch")
	}
	wantDigest, err := FoundationRefreshArtifactsDigest(s.Dir(), "layered_outline")
	if err != nil || firstEpoch.Digest != wantDigest {
		t.Fatalf("refresh epoch did not bind layered+flat artifacts: checkpoint=%+v want=%q err=%v", firstEpoch, wantDigest, err)
	}
	if _, err := tool.Execute(context.Background(), args); err == nil || !strings.Contains(err.Error(), "already completed") {
		t.Fatalf("same sidecar performed a second mutation: %v", err)
	}
	tool = NewSaveFoundationTool(s).
		WithChapterZeroFoundationRefresh(true).
		WithFoundationTypeRestriction("layered_outline").
		WithFoundationRefreshEpoch(true).
		WithOneShotFoundationRefresh(true)
	if _, err := tool.Execute(context.Background(), args); err != nil {
		t.Fatalf("same-body retry in a fresh sidecar rejected: %v", err)
	}
	secondEpoch := s.Checkpoints.LatestByStep(domain.GlobalScope(), FoundationRefreshCheckpointStep("layered_outline"))
	if secondEpoch == nil || secondEpoch.Seq <= firstEpoch.Seq || secondEpoch.Digest != firstEpoch.Digest {
		t.Fatalf("same-body refresh did not append a new causal epoch: first=%+v second=%+v", firstEpoch, secondEpoch)
	}
}

func TestSaveFoundationChapterZeroRefreshCapabilityFailsClosed(t *testing.T) {
	newStore := func(t *testing.T) *store.Store {
		t.Helper()
		s := store.NewStore(t.TempDir())
		if err := s.Init(); err != nil {
			t.Fatal(err)
		}
		if err := s.Progress.Save(&domain.Progress{
			Phase: domain.PhaseWriting, CurrentChapter: 1, TotalChapters: 12,
		}); err != nil {
			t.Fatal(err)
		}
		seedChapterZeroRefreshFoundation(t, s)
		return s
	}
	flatArgs, _ := json.Marshal(map[string]any{
		"type": "outline", "scale": "short",
		"content": []map[string]any{{"chapter": 1, "title": "第一章", "core_event": "开局", "hook": "继续"}},
	})
	layeredArgs, _ := json.Marshal(map[string]any{
		"type": "layered_outline", "scale": "short",
		"content": []map[string]any{{"index": 1, "title": "卷", "arcs": []map[string]any{{
			"index": 1, "title": "弧", "chapters": []map[string]any{{"chapter": 1, "title": "章", "core_event": "事", "hook": "钩"}},
		}}}},
	})

	t.Run("capability without foundation lock", func(t *testing.T) {
		s := newStore(t)
		if _, err := NewSaveFoundationTool(s).WithChapterZeroFoundationRefresh(true).Execute(context.Background(), layeredArgs); err == nil || !strings.Contains(err.Error(), "禁止使用") {
			t.Fatalf("refresh capability escaped without a live foundation lock: %v", err)
		}
	})

	t.Run("foundation lock without capability", func(t *testing.T) {
		s := newStore(t)
		const owner = "foundation-lock-without-refresh-capability"
		if err := s.Runtime.AcquirePipelineExecution(domain.PipelineExecutionLock{
			Mode: domain.PipelineExecutionFoundation, TargetChapter: 1, Owner: owner,
		}); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = s.Runtime.ReleasePipelineExecution(owner) })
		if _, err := NewSaveFoundationTool(s).Execute(context.Background(), layeredArgs); err == nil || !strings.Contains(err.Error(), "禁止使用") {
			t.Fatalf("foundation lock escaped without the process-local refresh capability: %v", err)
		}
	})

	t.Run("capability does not authorize flat outline", func(t *testing.T) {
		s := newStore(t)
		const owner = "foundation-refresh-flat-outline"
		if err := s.Runtime.AcquirePipelineExecution(domain.PipelineExecutionLock{
			Mode: domain.PipelineExecutionFoundation, TargetChapter: 1, Owner: owner,
		}); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = s.Runtime.ReleasePipelineExecution(owner) })
		if err := s.RunMeta.SetPlanningTier(domain.PlanningTierLong); err != nil {
			t.Fatal(err)
		}
		if _, err := NewSaveFoundationTool(s).WithChapterZeroFoundationRefresh(true).Execute(context.Background(), flatArgs); err == nil || !strings.Contains(err.Error(), "禁止使用") {
			t.Fatalf("refresh capability authorized a flat outline: %v", err)
		}
		meta, err := s.RunMeta.Load()
		if err != nil || meta == nil || meta.PlanningTier != domain.PlanningTierLong {
			t.Fatalf("rejected outline changed planning tier: %+v err=%v", meta, err)
		}
	})

	t.Run("zero-init evidence blocks capability", func(t *testing.T) {
		s := newStore(t)
		const owner = "foundation-refresh-after-zero-init"
		if err := s.Runtime.AcquirePipelineExecution(domain.PipelineExecutionLock{
			Mode: domain.PipelineExecutionFoundation, TargetChapter: 1, Owner: owner,
		}); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = s.Runtime.ReleasePipelineExecution(owner) })
		if err := os.WriteFile(filepath.Join(s.Dir(), "meta", "first_chapter_generation_readiness.json"), []byte(`{"ready":true}`), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := NewSaveFoundationTool(s).WithChapterZeroFoundationRefresh(true).Execute(context.Background(), layeredArgs); err == nil || !strings.Contains(err.Error(), "禁止使用") {
			t.Fatalf("refresh capability escaped after zero-init: %v", err)
		}
	})

	t.Run("nested draft evidence blocks capability", func(t *testing.T) {
		s := newStore(t)
		const owner = "foundation-refresh-with-nested-draft"
		if err := s.Runtime.AcquirePipelineExecution(domain.PipelineExecutionLock{
			Mode: domain.PipelineExecutionFoundation, TargetChapter: 1, Owner: owner,
		}); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = s.Runtime.ReleasePipelineExecution(owner) })
		path := filepath.Join(s.Dir(), "drafts", "nested", "01.plan.json")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(`{"chapter":1}`), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := NewSaveFoundationTool(s).WithChapterZeroFoundationRefresh(true).Execute(context.Background(), layeredArgs); err == nil || !strings.Contains(err.Error(), "禁止使用") {
			t.Fatalf("refresh capability escaped nested draft evidence: %v", err)
		}
	})

	t.Run("exact type restriction rejects another foundation mutation", func(t *testing.T) {
		s := newStore(t)
		const owner = "foundation-refresh-exact-type"
		if err := s.Runtime.AcquirePipelineExecution(domain.PipelineExecutionLock{
			Mode: domain.PipelineExecutionFoundation, TargetChapter: 1, Owner: owner,
		}); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = s.Runtime.ReleasePipelineExecution(owner) })
		tool := NewSaveFoundationTool(s).
			WithChapterZeroFoundationRefresh(true).
			WithFoundationTypeRestriction("characters")
		if _, err := tool.Execute(context.Background(), layeredArgs); err == nil || !strings.Contains(err.Error(), "only allows") {
			t.Fatalf("exact foundation restriction accepted another type: %v", err)
		}
	})

	t.Run("exact target cannot change planning tier", func(t *testing.T) {
		s := newStore(t)
		const owner = "foundation-refresh-scale-guard"
		if err := s.Runtime.AcquirePipelineExecution(domain.PipelineExecutionLock{
			Mode: domain.PipelineExecutionFoundation, TargetChapter: 1, Owner: owner,
		}); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = s.Runtime.ReleasePipelineExecution(owner) })
		var payload map[string]any
		if err := json.Unmarshal(layeredArgs, &payload); err != nil {
			t.Fatal(err)
		}
		payload["scale"] = "long"
		longArgs, _ := json.Marshal(payload)
		tool := NewSaveFoundationTool(s).
			WithChapterZeroFoundationRefresh(true).
			WithFoundationTypeRestriction("layered_outline")
		if _, err := tool.Execute(context.Background(), longArgs); err == nil || !strings.Contains(err.Error(), "cannot change planning tier") {
			t.Fatalf("exact target changed planning tier: %v", err)
		}
		meta, err := s.RunMeta.Load()
		if err != nil || meta == nil || meta.PlanningTier != domain.PlanningTierShort {
			t.Fatalf("rejected target changed planning tier: %+v err=%v", meta, err)
		}
	})
}

func seedChapterZeroRefreshFoundation(t *testing.T, st *store.Store) {
	t.Helper()
	if err := st.RunMeta.SetPlanningTier(domain.PlanningTierShort); err != nil {
		t.Fatal(err)
	}
	for rel, body := range map[string]string{
		"premise.md":           "# 测试",
		"characters.json":      `[{"name":"主角"}]`,
		"world_rules.json":     `[{"name":"规则"}]`,
		"book_world.json":      `{"name":"世界"}`,
		"world_codex.json":     `{"version":1}`,
		"meta/compass.json":    `{"ending_direction":"HE"}`,
		"layered_outline.json": `[]`,
	} {
		path := filepath.Join(st.Dir(), filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSaveFoundationOutlineReplacementFailsClosedOnCorruptOrHiddenCanonState(t *testing.T) {
	outlineArgs, _ := json.Marshal(map[string]any{
		"type": "outline", "scale": "short",
		"content": []map[string]any{{"chapter": 1, "title": "章", "core_event": "事", "hook": "钩"}},
	})
	t.Run("corrupt progress", func(t *testing.T) {
		st := store.NewStore(t.TempDir())
		if err := st.Init(); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(st.Dir(), "meta", "progress.json"), []byte(`{"phase":`), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := NewSaveFoundationTool(st).Execute(context.Background(), outlineArgs); err == nil || !strings.Contains(err.Error(), "load progress before outline replacement") {
			t.Fatalf("corrupt progress failed open: %v", err)
		}
	})
	t.Run("planning phase with nested draft", func(t *testing.T) {
		st := store.NewStore(t.TempDir())
		if err := st.Init(); err != nil {
			t.Fatal(err)
		}
		if err := st.Progress.Save(&domain.Progress{Phase: domain.PhaseOutline, CurrentChapter: 0}); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(st.Dir(), "drafts", "nested", "01.draft.md")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("canon-like draft"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := NewSaveFoundationTool(st).Execute(context.Background(), outlineArgs); err == nil || !strings.Contains(err.Error(), "禁止") {
			t.Fatalf("planning phase with hidden draft failed open: %v", err)
		}
	})
}

func TestSaveFoundationPersistsPlanningTier(t *testing.T) {
	dir := t.TempDir()
	store := store.NewStore(dir)
	if err := store.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	tool := NewSaveFoundationTool(store)
	args, err := json.Marshal(map[string]any{
		"type":    "premise",
		"content": "# 测试书名\n\n## 题材和基调\n测试",
		"scale":   "long",
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	if _, err := tool.Execute(context.Background(), args); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	meta, err := store.RunMeta.Load()
	if err != nil {
		t.Fatalf("LoadRunMeta: %v", err)
	}
	if meta == nil {
		t.Fatal("expected run meta to exist")
	}
	if meta.PlanningTier != domain.PlanningTierLong {
		t.Fatalf("expected planning tier %q, got %q", domain.PlanningTierLong, meta.PlanningTier)
	}
}

func TestSaveFoundationPremiseSetsNovelName(t *testing.T) {
	dir := t.TempDir()
	store := store.NewStore(dir)
	if err := store.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := store.Progress.Init("novel", 0); err != nil {
		t.Fatalf("Init progress: %v", err)
	}

	tool := NewSaveFoundationTool(store)
	args, err := json.Marshal(map[string]any{
		"type": "premise",
		"content": `# 长夜燃灯

## 题材和基调
东方玄幻，冷硬求生。`,
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	if _, err := tool.Execute(context.Background(), args); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	progress, err := store.Progress.Load()
	if err != nil {
		t.Fatalf("LoadProgress: %v", err)
	}
	if progress == nil {
		t.Fatal("expected progress")
	}
	if progress.NovelName != "长夜燃灯" {
		t.Fatalf("expected novel name set, got %q", progress.NovelName)
	}
}

func TestSaveFoundationOutlineClearsLayeredStateWhenDowngrading(t *testing.T) {
	dir := t.TempDir()
	store := store.NewStore(dir)
	if err := store.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := store.Progress.Init("test", 0); err != nil {
		t.Fatalf("InitProgress: %v", err)
	}

	tool := NewSaveFoundationTool(store)

	layeredArgs, err := json.Marshal(map[string]any{
		"type":    "layered_outline",
		"content": `[{"index":1,"title":"第一卷","theme":"主题","arcs":[{"index":1,"title":"第一弧","goal":"目标","chapters":[{"chapter":1,"title":"第一章","core_event":"开局","hook":"继续"}]}]}]`,
		"scale":   "long",
	})
	if err != nil {
		t.Fatalf("Marshal layered args: %v", err)
	}
	if _, err := tool.Execute(context.Background(), layeredArgs); err != nil {
		t.Fatalf("Execute layered outline: %v", err)
	}

	outlineArgs, err := json.Marshal(map[string]any{
		"type":    "outline",
		"content": `[{"chapter":1,"title":"第一章","core_event":"改为中篇","hook":"继续"}]`,
		"scale":   "mid",
	})
	if err != nil {
		t.Fatalf("Marshal outline args: %v", err)
	}
	if _, err := tool.Execute(context.Background(), outlineArgs); err != nil {
		t.Fatalf("Execute outline: %v", err)
	}

	progress, err := store.Progress.Load()
	if err != nil {
		t.Fatalf("LoadProgress: %v", err)
	}
	if progress == nil {
		t.Fatal("expected progress to exist")
	}
	if progress.Layered {
		t.Fatal("expected layered mode to be disabled")
	}
	if progress.CurrentVolume != 0 || progress.CurrentArc != 0 {
		t.Fatalf("expected volume/arc reset, got volume=%d arc=%d", progress.CurrentVolume, progress.CurrentArc)
	}

	volumes, err := store.Outline.LoadLayeredOutline()
	if err != nil {
		t.Fatalf("LoadLayeredOutline: %v", err)
	}
	if len(volumes) != 0 {
		t.Fatalf("expected layered outline cleared, got %d volumes", len(volumes))
	}

	meta, err := store.RunMeta.Load()
	if err != nil {
		t.Fatalf("LoadRunMeta: %v", err)
	}
	if meta == nil {
		t.Fatal("expected run meta to exist")
	}
	if meta.PlanningTier != domain.PlanningTierMid {
		t.Fatalf("expected planning tier %q, got %q", domain.PlanningTierMid, meta.PlanningTier)
	}
}

func TestSaveFoundationRejectsCompactProcessTitlesForExplicitLightheartedProject(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.UserRules.Save(&rules.Snapshot{
		Version:     rules.SnapshotVersion,
		Status:      rules.StatusReady,
		Preferences: "整本保持轻松欢快；卷名、弧名和章节标题必须有吸引力，不能像工作日志。",
	}); err != nil {
		t.Fatalf("Save user rules: %v", err)
	}

	tool := NewSaveFoundationTool(s)
	bad, _ := json.Marshal(map[string]any{
		"type": "outline",
		"content": []map[string]any{{
			"chapter": 1, "title": "第一张清单", "core_event": "系统筛掉无效消费", "hook": "女主到场",
		}},
	})
	if _, err := tool.Execute(context.Background(), bad); err == nil {
		t.Fatal("expected compact process title to be rejected")
	}

	good, _ := json.Marshal(map[string]any{
		"type": "outline",
		"content": []map[string]any{{
			"chapter": 1, "title": "系统不要排面，只要实用", "core_event": "系统筛掉无效消费", "hook": "女主到场",
		}},
	})
	if _, err := tool.Execute(context.Background(), good); err != nil {
		t.Fatalf("expected contrast-driven title to pass: %v", err)
	}
}

func TestSaveFoundationAppendVolume(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init("test", 0); err != nil {
		t.Fatalf("InitProgress: %v", err)
	}

	tool := NewSaveFoundationTool(s)

	// 先创建初始 layered_outline（卷1）
	layeredArgs, _ := json.Marshal(map[string]any{
		"type": "layered_outline",
		"content": []map[string]any{{
			"index": 1, "title": "第一卷", "theme": "起步",
			"arcs": []map[string]any{{
				"index": 1, "title": "首弧", "goal": "目标",
				"chapters": []map[string]any{{"title": "第一章", "core_event": "开局", "hook": "继续"}},
			}},
		}},
		"scale": "long",
	})
	if _, err := tool.Execute(context.Background(), layeredArgs); err != nil {
		t.Fatalf("Execute layered: %v", err)
	}

	// append_volume：追加卷2
	appendArgs, _ := json.Marshal(map[string]any{
		"type": "append_volume",
		"content": map[string]any{
			"index": 2, "title": "第二卷", "theme": "升级",
			"arcs": []map[string]any{{
				"index": 1, "title": "弧一", "goal": "目标",
				"chapters": []map[string]any{{"title": "新章", "core_event": "推进", "hook": "钩子"}},
			}},
		},
	})
	res, err := tool.Execute(context.Background(), appendArgs)
	if err != nil {
		t.Fatalf("Execute append_volume: %v", err)
	}
	var result map[string]any
	json.Unmarshal(res, &result)
	if result["volume"] != float64(2) {
		t.Fatalf("expected volume=2, got %v", result["volume"])
	}

	// 验证大纲有 2 卷
	volumes, _ := s.Outline.LoadLayeredOutline()
	if len(volumes) != 2 {
		t.Fatalf("expected 2 volumes, got %d", len(volumes))
	}
	if volumes[1].Title != "第二卷" {
		t.Fatalf("expected title '第二卷', got %q", volumes[1].Title)
	}
}

func TestSaveFoundationExpandArcRejectsChapterCountThatWouldShiftLaterArc(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := st.Progress.Init("test", 7); err != nil {
		t.Fatalf("Progress.Init: %v", err)
	}
	volumes := []domain.VolumeOutline{{
		Index: 1,
		Arcs: []domain.ArcOutline{
			{
				Index: 1,
				Chapters: []domain.OutlineEntry{
					{Title: "前一"},
					{Title: "前二"},
				},
			},
			{Index: 2, EstimatedChapters: 3},
			{
				Index: 3,
				Chapters: []domain.OutlineEntry{
					{Title: "后一"},
					{Title: "后二"},
				},
			},
		},
	}}
	if err := st.Outline.SaveLayeredOutline(volumes); err != nil {
		t.Fatalf("SaveLayeredOutline: %v", err)
	}
	if err := st.Outline.SaveOutline(domain.FlattenOutline(volumes)); err != nil {
		t.Fatalf("SaveOutline: %v", err)
	}

	args, err := json.Marshal(map[string]any{
		"type":   "expand_arc",
		"volume": 1,
		"arc":    2,
		"content": []map[string]any{
			{"title": "中一"},
			{"title": "中二"},
		},
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if _, err := NewSaveFoundationTool(st).Execute(context.Background(), args); err == nil ||
		!strings.Contains(err.Error(), "must equal reserved estimated_chapters 3") {
		t.Fatalf("expected stable chapter reservation guard, got %v", err)
	}

	flat, err := st.Outline.LoadOutline()
	if err != nil {
		t.Fatalf("LoadOutline: %v", err)
	}
	if len(flat) != 4 || flat[2].Chapter != 6 || flat[2].Title != "后一" {
		t.Fatalf("rejected tool call changed later arc mapping: %+v", flat)
	}
}

func TestSaveFoundationAppendVolumeValidation(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init("test", 0); err != nil {
		t.Fatalf("InitProgress: %v", err)
	}

	tool := NewSaveFoundationTool(s)

	// 初始卷
	layeredArgs, _ := json.Marshal(map[string]any{
		"type": "layered_outline",
		"content": []map[string]any{{
			"index": 1, "title": "第一卷", "theme": "起步",
			"arcs": []map[string]any{{
				"index": 1, "title": "首弧", "goal": "目标",
				"chapters": []map[string]any{{"title": "第一章", "core_event": "开局", "hook": "继续"}},
			}},
		}},
		"scale": "long",
	})
	tool.Execute(context.Background(), layeredArgs)

	// Index 不递增 → 应失败（结构性校验）
	appendArgs, _ := json.Marshal(map[string]any{
		"type": "append_volume",
		"content": map[string]any{
			"index": 1, "title": "重复 Index", "theme": "x",
			"arcs": []map[string]any{{
				"index": 1, "title": "弧一", "goal": "目标",
				"chapters": []map[string]any{{"title": "章", "core_event": "事件", "hook": "钩子"}},
			}},
		},
	})
	_, err := tool.Execute(context.Background(), appendArgs)
	if err == nil {
		t.Fatal("expected error when appending volume with non-increasing index")
	}
}

// TestSaveFoundationAppendVolumeRejectsAfterComplete 验证 Phase=Complete 后不允许 append_volume。
// 取代旧的"Final 卷拒绝追加"语义（Final 字段已删除）。
func TestSaveFoundationAppendVolumeRejectsAfterComplete(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init("test", 0); err != nil {
		t.Fatalf("InitProgress: %v", err)
	}
	if err := s.Progress.MarkComplete(); err != nil {
		t.Fatalf("MarkComplete: %v", err)
	}

	tool := NewSaveFoundationTool(s)
	appendArgs, _ := json.Marshal(map[string]any{
		"type": "append_volume",
		"content": map[string]any{
			"index": 1, "title": "尝试续写", "theme": "x",
			"arcs": []map[string]any{{
				"index": 1, "title": "弧", "goal": "g",
				"chapters": []map[string]any{{"title": "章", "core_event": "e", "hook": "h"}},
			}},
		},
	})
	if _, err := tool.Execute(context.Background(), appendArgs); err == nil {
		t.Fatal("expected error when appending after Phase=Complete")
	}
}

func TestSaveFoundationUpdateCompass(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	tool := NewSaveFoundationTool(s)
	args, _ := json.Marshal(map[string]any{
		"type": "update_compass",
		"content": map[string]any{
			"ending_direction": "主角面对最终抉择",
			"open_threads":     []string{"线索A", "关系B"},
			"estimated_scale":  "预计 4-6 卷",
		},
	})
	_, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute update_compass: %v", err)
	}

	compass, err := s.Outline.LoadCompass()
	if err != nil {
		t.Fatalf("LoadCompass: %v", err)
	}
	if compass == nil || compass.EndingDirection != "主角面对最终抉择" {
		t.Fatalf("unexpected compass: %+v", compass)
	}
	if len(compass.OpenThreads) != 2 {
		t.Fatalf("expected 2 open threads, got %d", len(compass.OpenThreads))
	}
}

func TestSaveFoundationUpdateCompassOverridesLastUpdated(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Save(&domain.Progress{
		NovelName:         "光斑",
		Phase:             domain.PhaseWriting,
		CompletedChapters: []int{1, 2, 3, 5, 4}, // 乱序，验证取 max 而非 len
	}); err != nil {
		t.Fatalf("Save progress: %v", err)
	}

	tool := NewSaveFoundationTool(s)
	args, _ := json.Marshal(map[string]any{
		"type": "update_compass",
		"content": map[string]any{
			"ending_direction": "主角面对最终抉择",
			"open_threads":     []string{"线索A"},
			"last_updated":     0, // LLM 通常忘填或留 0
		},
	})
	if _, err := tool.Execute(context.Background(), args); err != nil {
		t.Fatalf("Execute update_compass: %v", err)
	}

	compass, err := s.Outline.LoadCompass()
	if err != nil {
		t.Fatalf("LoadCompass: %v", err)
	}
	if compass.LastUpdated != 5 {
		t.Fatalf("expected LastUpdated=5 (max of CompletedChapters), got %d", compass.LastUpdated)
	}
}

func TestSaveFoundationUpdateCompassRequiresDirection(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	tool := NewSaveFoundationTool(s)
	args, _ := json.Marshal(map[string]any{
		"type":    "update_compass",
		"content": map[string]any{"estimated_scale": "3 卷"},
	})
	_, err := tool.Execute(context.Background(), args)
	if err == nil {
		t.Fatal("expected error when ending_direction is empty")
	}
}

func TestSaveFoundationAcceptsDirectJSONArrayContent(t *testing.T) {
	dir := t.TempDir()
	store := store.NewStore(dir)
	if err := store.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	tool := NewSaveFoundationTool(store)
	args, err := json.Marshal(map[string]any{
		"type": "outline",
		"content": []map[string]any{
			{
				"chapter":    1,
				"title":      "第一章",
				"core_event": "主角登场",
				"hook":       "继续",
				"scenes":     []string{"场景一", "场景二"},
			},
		},
		"scale": "short",
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	if _, err := tool.Execute(context.Background(), args); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	outline, err := store.Outline.LoadOutline()
	if err != nil {
		t.Fatalf("LoadOutline: %v", err)
	}
	if len(outline) != 1 || outline[0].Title != "第一章" {
		t.Fatalf("unexpected outline: %+v", outline)
	}
}

func TestSaveFoundationBookWorld(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	tool := NewSaveFoundationTool(s)
	args, _ := json.Marshal(map[string]any{
		"type": "book_world",
		"content": map[string]any{
			"name":     "灰雾城",
			"summary":  "鬼市、便利店与医院构成前期地图。",
			"places":   []map[string]any{{"id": "store", "name": "鬼便利店", "kind": "shop"}},
			"factions": []map[string]any{{"id": "bank", "name": "阴司银行", "goal": "回收欠账"}},
		},
	})
	if _, err := tool.Execute(context.Background(), args); err != nil {
		t.Fatalf("Execute book_world: %v", err)
	}
	world, err := s.World.LoadBookWorld()
	if err != nil {
		t.Fatalf("LoadBookWorld: %v", err)
	}
	if world == nil || len(world.Places) != 1 || len(world.Factions) != 1 {
		t.Fatalf("unexpected book_world: %+v", world)
	}
}

// completeBookSetup 建一份处于 writing 阶段的最小 Store，用于 complete_book 系列测试。
// complete_book 不校验 layered_outline 章节齐全（判定责任在 LLM 的"完结判定清单"），
// 工具层只校验 PendingRewrites 为空、progress 已初始化。
func completeBookSetup(t *testing.T) *store.Store {
	t.Helper()
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init("test", 0); err != nil {
		t.Fatalf("InitProgress: %v", err)
	}
	_ = s.Progress.UpdatePhase(domain.PhaseWriting)
	return s
}

func TestSaveFoundationCompleteBookPushesPhaseComplete(t *testing.T) {
	s := completeBookSetup(t)
	tool := NewSaveFoundationTool(s)
	args, _ := json.Marshal(map[string]any{
		"type": "complete_book", "content": map[string]any{},
	})
	res, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute complete_book: %v", err)
	}
	var result map[string]any
	_ = json.Unmarshal(res, &result)
	if result["book_complete"] != true {
		t.Fatalf("expected book_complete=true, got %+v", result)
	}
	if result["phase"] != string(domain.PhaseComplete) {
		t.Fatalf("expected phase=complete, got %v", result["phase"])
	}
	progress, _ := s.Progress.Load()
	if progress.Phase != domain.PhaseComplete {
		t.Fatalf("expected progress.Phase=complete, got %s", progress.Phase)
	}
}

func TestSaveFoundationCompleteBookRejectsBeforeWriting(t *testing.T) {
	// 规划阶段误调 complete_book 必须被拒，否则会直接跳过整本写作。
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init("test", 0); err != nil {
		t.Fatalf("InitProgress: %v", err)
	}
	_ = s.Progress.UpdatePhase(domain.PhasePremise)
	_ = s.Progress.UpdatePhase(domain.PhaseOutline)
	tool := NewSaveFoundationTool(s)
	args, _ := json.Marshal(map[string]any{
		"type": "complete_book", "content": map[string]any{},
	})
	if _, err := tool.Execute(context.Background(), args); err == nil {
		t.Fatal("expected error when phase != writing")
	}
	progress, _ := s.Progress.Load()
	if progress.Phase != domain.PhaseOutline {
		t.Fatalf("phase should remain outline, got %s", progress.Phase)
	}
}

func TestSaveFoundationCompleteBookRejectsWithPendingRewrites(t *testing.T) {
	s := completeBookSetup(t)
	if err := s.Progress.MarkChapterComplete(2, 3000, "", ""); err != nil {
		t.Fatalf("MarkChapterComplete: %v", err)
	}
	if err := s.Progress.SetPendingRewrites([]int{2}, "尾章节奏过快"); err != nil {
		t.Fatalf("SetPendingRewrites: %v", err)
	}
	tool := NewSaveFoundationTool(s)
	args, _ := json.Marshal(map[string]any{
		"type": "complete_book", "content": map[string]any{},
	})
	if _, err := tool.Execute(context.Background(), args); err == nil {
		t.Fatal("expected error when PendingRewrites non-empty")
	}
	progress, _ := s.Progress.Load()
	if progress.Phase == domain.PhaseComplete {
		t.Fatalf("phase should not be Complete with PendingRewrites: %s", progress.Phase)
	}
}

func TestSaveFoundationCompleteBookRejectsUnreviewedChapter(t *testing.T) {
	s := completeBookSetup(t)
	if err := s.Progress.MarkChapterComplete(1, 3000, "", ""); err != nil {
		t.Fatalf("MarkChapterComplete: %v", err)
	}
	tool := NewSaveFoundationTool(s)
	args, _ := json.Marshal(map[string]any{
		"type": "complete_book", "content": map[string]any{},
	})
	if _, err := tool.Execute(context.Background(), args); err == nil {
		t.Fatal("expected error when completed chapter lacks accepted review")
	}
	progress, _ := s.Progress.Load()
	if progress.Phase == domain.PhaseComplete {
		t.Fatalf("phase should not be Complete with unreviewed chapter: %s", progress.Phase)
	}
}

func TestSaveFoundationCompleteBookRejectsShortWithoutGlobalReview(t *testing.T) {
	s := completeBookSetup(t)
	if err := s.RunMeta.SetPlanningTier(domain.PlanningTierShort); err != nil {
		t.Fatalf("SetPlanningTier: %v", err)
	}
	if err := s.Progress.SetTotalChapters(1); err != nil {
		t.Fatalf("SetTotalChapters: %v", err)
	}
	if err := s.Progress.MarkChapterComplete(1, 3000, "", ""); err != nil {
		t.Fatalf("MarkChapterComplete: %v", err)
	}
	if err := s.World.SaveReview(domain.ReviewEntry{Chapter: 1, Scope: "chapter", Verdict: "accept"}); err != nil {
		t.Fatalf("SaveReview chapter: %v", err)
	}
	tool := NewSaveFoundationTool(s)
	args, _ := json.Marshal(map[string]any{
		"type": "complete_book", "content": map[string]any{},
	})
	if _, err := tool.Execute(context.Background(), args); err == nil {
		t.Fatal("expected error when short project lacks global final review")
	}
	progress, _ := s.Progress.Load()
	if progress.Phase == domain.PhaseComplete {
		t.Fatalf("phase should not be Complete without global review: %s", progress.Phase)
	}
}
