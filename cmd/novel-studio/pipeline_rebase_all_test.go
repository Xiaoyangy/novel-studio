package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func TestResetPipelineAllChapterCandidatePreservesChapterZeroSeedsAndIsIdempotent(t *testing.T) {
	outputDir := filepath.Join(t.TempDir(), "output", "novel")
	preserved := map[string]string{
		"premise.md":                       "不可删除的故事前提\n",
		"outline.json":                     `[{"chapter":1,"title":"第一章"}]`,
		"characters.json":                  `[{"name":"甲"}]`,
		"world_rules.json":                 `[{"rule":"规则"}]`,
		"meta/initial_review_lessons.md":   "用户明确保留的 RAG 素材\n",
		"meta/rag/index_state.json":        `{"chunks":[{"id":"frozen-foundation"}]}`,
		"meta/rag/index_state.md":          "# Frozen index\n\n15484 chunks.\n",
		"meta/rag/vector_store.json":       `{"points":[{"id":"frozen-foundation"}]}`,
		"meta/rag/vector_store.md":         "# Frozen vectors\n\n490 points.\n",
		"meta/rag/foundation/premise.json": `{"immutable":true}`,
	}
	for rel, body := range preserved {
		rebaseAllTestWriteFile(t, outputDir, rel, body)
	}
	removed := map[string]string{
		"chapters/01.md":                               "旧第一章正文",
		"chapters/02.md":                               "旧第二章正文",
		"drafts/01.draft.md":                           "旧草稿",
		"summaries/01.md":                              "旧摘要",
		"reviews/01.json":                              `{"verdict":"accept"}`,
		"meta/planning/current_plan.json":              `{"old":true}`,
		"meta/runtime/pipeline_execution.json":         `{"old":true}`,
		"meta/quarantine/old.md":                       "旧隔离稿",
		"meta/chapter_world_deltas/01.json":            `{"old":true}`,
		"meta/character_stage/甲.json":                  `{"old":true}`,
		"meta/side_character_journeys/甲.json":          `{"old":true}`,
		"meta/rag/receipts/000001.json":                `{"generation":"old"}`,
		"meta/rag/fact_receipts/fact-old.json":         `{"generation":"old"}`,
		"meta/rag/craft_receipts/craft-old.json":       `{"generation":"old"}`,
		"meta/rag/traces/trace-old.jsonl":              `{"generation":"old"}`,
		"meta/rag/logs/retrieval-old.jsonl":            `{"generation":"old"}`,
		"meta/rag/craft_recall_log.jsonl":              `{"generation":"old"}`,
		"meta/rag/retrieval_trace.jsonl":               `{"generation":"old"}`,
		"meta/rag/pending_upserts.json":                `[{"generation":"old"}]`,
		"meta/checkpoints.jsonl":                       `{"old":true}`,
		"meta/state_changes.json":                      `{"old":true}`,
		"meta/pipeline.json":                           `{"old":true}`,
		"meta/chapter_progress.json":                   `{"old":true}`,
		"meta/cast_ledger.json":                        `{"old":true}`,
		"meta/sessions/agents/writer-ch01.jsonl":       `{"old":true}`,
		"meta/chapter_simulations/001.json":            `{"old":true}`,
		"meta/chapter_metrics/01.json":                 `{"old":true}`,
		"meta/sampling/01.json":                        `{"old":true}`,
		"meta/scene_dynamics/01.json":                  `{"old":true}`,
		"meta/delivery_snapshots/ch01.json":            `{"old":true}`,
		"meta/rewrite_recovery/ch01.json":              `{"old":true}`,
		"relationship_state.initial.json":              `{"initial_relationship":"old-zero-init"}`,
		"foreshadow_ledger.initial.json":               `{"initial_foreshadow":"old-zero-init"}`,
		"meta/initial_resource_ledger.json":            `{"initial_resource":"old-zero-init"}`,
		"meta/prewrite_storycraft_plan.json":           `{"old":true}`,
		"meta/world_background_plan.json":              `{"old":true}`,
		"meta/characters/甲/dossier.json":               `{"old":true}`,
		"meta/story_time_contract.json":                `{"old":true}`,
		"meta/story_time_contract.md":                  "旧时间合同\n",
		"meta/story_calendar.json":                     `{"old":true}`,
		"meta/world_tick.json":                         `{"old":true}`,
		"meta/world_events.jsonl":                      `{"old":true}`,
		"meta/simulation_restart_policy.json":          `{"old":true}`,
		"meta/simulation_restart_state.json":           `{"old":true}`,
		"meta/zero_chapter_context_manifest.json":      `{"old":true}`,
		"meta/first_chapter_generation_readiness.json": `{"old":true}`,
	}
	for rel, body := range removed {
		rebaseAllTestWriteFile(t, outputDir, rel, body)
	}
	wantRAGRoot, err := pipelineRebaseRAGAuthorityRoot(outputDir)
	if err != nil {
		t.Fatal(err)
	}

	if err := resetPipelineAllChapterCandidate(outputDir, &zeroInitProject{}); err != nil {
		t.Fatal(err)
	}
	for rel, want := range preserved {
		got, err := os.ReadFile(filepath.Join(outputDir, filepath.FromSlash(rel)))
		if err != nil || string(got) != want {
			t.Fatalf("chapter-zero seed %s changed: got=%q err=%v", rel, got, err)
		}
	}
	for rel := range removed {
		if _, err := os.Stat(filepath.Join(outputDir, filepath.FromSlash(rel))); !os.IsNotExist(err) {
			t.Fatalf("active history %s survived reset: %v", rel, err)
		}
	}
	gotRAGRoot, err := pipelineRebaseRAGAuthorityRoot(outputDir)
	if err != nil {
		t.Fatal(err)
	}
	if gotRAGRoot != wantRAGRoot {
		t.Fatalf("chapter-zero reset changed frozen RAG authority: got=%s want=%s", gotRAGRoot, wantRAGRoot)
	}
	firstRoot, err := store.DirectoryContentRoot(outputDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := resetPipelineAllChapterCandidate(outputDir, &zeroInitProject{}); err != nil {
		t.Fatalf("second reset: %v", err)
	}
	secondRoot, err := store.DirectoryContentRoot(outputDir)
	if err != nil {
		t.Fatal(err)
	}
	if secondRoot != firstRoot {
		t.Fatalf("repeated chapter-zero reset changed candidate root: first=%s second=%s", firstRoot, secondRoot)
	}
}

func TestPipelineRebaseAllChaptersPreservesFrozenRAGAndClearsOldGenerationReceipts(t *testing.T) {
	live := seedZeroInitProject(t)
	st := store.NewStore(live)
	if err := st.Progress.Init("rag-preserving-rebase", 1); err != nil {
		t.Fatal(err)
	}
	progress, err := st.Progress.Load()
	if err != nil || progress == nil {
		t.Fatalf("load old progress: progress=%+v err=%v", progress, err)
	}
	progress.GenerationID = "old-rag-generation"
	if err := st.Progress.Save(progress); err != nil {
		t.Fatal(err)
	}
	rebaseAllTestWriteFile(t, live, "chapters/01.md", "旧 generation 正文。\n")
	if err := st.Progress.MarkChapterComplete(1, 16, "旧回执", "main"); err != nil {
		t.Fatal(err)
	}
	authority := map[string]string{
		"meta/rag/index_state.json":  `{"chunks":[{"id":"foundation-15484"}]}`,
		"meta/rag/index_state.md":    "# Index\n\n15484 chunks.\n",
		"meta/rag/vector_store.json": `{"points":[{"id":"foundation-490"}]}`,
		"meta/rag/vector_store.md":   "# Vector store\n\n490 points.\n",
	}
	for rel, body := range authority {
		rebaseAllTestWriteFile(t, live, rel, body)
	}
	oldGenerationArtifacts := map[string]string{
		"meta/rag/receipts/000001.json":          `{"generation":"old-rag-generation"}`,
		"meta/rag/fact_receipts/fact-old.json":   `{"generation":"old-rag-generation"}`,
		"meta/rag/craft_receipts/craft-old.json": `{"generation":"old-rag-generation"}`,
		"meta/rag/craft_recall_log.jsonl":        `{"generation":"old-rag-generation"}` + "\n",
		"meta/rag/retrieval_trace.jsonl":         `{"generation":"old-rag-generation"}` + "\n",
		"meta/rag/pending_upserts.json":          `[{"generation":"old-rag-generation"}]`,
	}
	for rel, body := range oldGenerationArtifacts {
		rebaseAllTestWriteFile(t, live, rel, body)
	}
	wantRAGRoot, err := pipelineRebaseRAGAuthorityRoot(live)
	if err != nil {
		t.Fatal(err)
	}

	if err := pipelineRebaseAllChapters(
		rebaseAllTestOptions(t, pipelineRebaseRunRoot(live)),
	); err != nil {
		t.Fatal(err)
	}

	rebasedProgress, err := store.NewStore(live).Progress.Load()
	if err != nil || rebasedProgress == nil {
		t.Fatalf("load rebased progress: progress=%+v err=%v", rebasedProgress, err)
	}
	if rebasedProgress.GenerationID == "" || rebasedProgress.GenerationID == "old-rag-generation" {
		t.Fatalf("rebase did not create a new generation: %+v", rebasedProgress)
	}
	gotRAGRoot, err := pipelineRebaseRAGAuthorityRoot(live)
	if err != nil {
		t.Fatal(err)
	}
	if gotRAGRoot != wantRAGRoot {
		t.Fatalf("new generation changed frozen RAG authority: got=%s want=%s", gotRAGRoot, wantRAGRoot)
	}
	for rel, want := range authority {
		got, err := os.ReadFile(filepath.Join(live, filepath.FromSlash(rel)))
		if err != nil || string(got) != want {
			t.Fatalf("new generation lost immutable RAG artifact %s: got=%q err=%v", rel, got, err)
		}
	}
	for rel := range oldGenerationArtifacts {
		if _, err := os.Lstat(filepath.Join(live, filepath.FromSlash(rel))); !os.IsNotExist(err) {
			t.Fatalf("old generation RAG artifact survived rebase: %s err=%v", rel, err)
		}
	}
	var receipt pipelineAllChapterRebaseReceipt
	if err := readPipelinePlanningJSON(
		filepath.Join(live, "meta", "all_chapter_rebase.json"),
		&receipt,
	); err != nil {
		t.Fatal(err)
	}
	if receipt.SourceRAGAuthorityRoot != wantRAGRoot ||
		receipt.ArchiveRAGAuthorityRoot != wantRAGRoot ||
		receipt.RebasedRAGAuthorityRoot != wantRAGRoot {
		t.Fatalf("rebase receipt did not bind one immutable RAG root: %+v want=%s", receipt, wantRAGRoot)
	}
	archiveRAGRoot, err := pipelineRebaseRAGAuthorityRoot(receipt.ArchiveOutput)
	if err != nil || archiveRAGRoot != wantRAGRoot {
		t.Fatalf("archive lost immutable RAG authority: got=%s err=%v", archiveRAGRoot, err)
	}
	for rel, want := range oldGenerationArtifacts {
		got, err := os.ReadFile(filepath.Join(receipt.ArchiveOutput, filepath.FromSlash(rel)))
		if err != nil || string(got) != want {
			t.Fatalf("archive lost old generation evidence %s: got=%q err=%v", rel, got, err)
		}
	}
}

func TestPipelineRebaseAllChaptersArchivesLegacyChapterOneCursorBeforeReset(t *testing.T) {
	live := seedZeroInitProject(t)
	st := store.NewStore(live)
	progress := &domain.Progress{
		NovelName:         "legacy-router-cursor",
		Phase:             domain.PhaseWriting,
		Flow:              domain.FlowWriting,
		CurrentChapter:    1,
		InProgressChapter: 1,
		TotalChapters:     1,
	}
	if err := st.Progress.Save(progress); err != nil {
		t.Fatal(err)
	}
	if err := validatePipelineOutlineAllEntry(st); err == nil {
		t.Fatal("ordinary outline-all accepted an ambiguous chapter-one cursor")
	}

	foundation := make(map[string][]byte)
	for _, rel := range []string{
		"premise.md",
		"outline.json",
		"characters.json",
		"world_rules.json",
		"book_world.json",
		"world_codex.json",
		"meta/compass.json",
	} {
		body, err := os.ReadFile(filepath.Join(live, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("read foundation %s: %v", rel, err)
		}
		foundation[rel] = body
	}
	progressBefore, err := os.ReadFile(filepath.Join(live, "meta", "progress.json"))
	if err != nil {
		t.Fatal(err)
	}

	if err := pipelineRebaseAllChapters(
		rebaseAllTestOptions(t, pipelineRebaseRunRoot(live)),
	); err != nil {
		t.Fatal(err)
	}

	var receipt pipelineAllChapterRebaseReceipt
	if err := readPipelinePlanningJSON(
		filepath.Join(live, "meta", "all_chapter_rebase.json"),
		&receipt,
	); err != nil {
		t.Fatal(err)
	}
	archivedProgress, err := os.ReadFile(receipt.PreviousProgress)
	if err != nil {
		t.Fatal(err)
	}
	if string(archivedProgress) != string(progressBefore) {
		t.Fatalf("rebase archive changed the ambiguous progress bytes:\narchive=%s\nsource=%s", archivedProgress, progressBefore)
	}
	archiveRoot, err := store.DirectoryContentRoot(receipt.ArchiveOutput)
	if err != nil || archiveRoot != receipt.SourceRoot || archiveRoot != receipt.ArchiveRoot {
		t.Fatalf("rebase archive is not the exact source tree: source=%s receipt=%s actual=%s err=%v",
			receipt.SourceRoot, receipt.ArchiveRoot, archiveRoot, err)
	}

	rebased, err := store.NewStore(live).Progress.Load()
	if err != nil || rebased == nil {
		t.Fatalf("load rebased progress: progress=%+v err=%v", rebased, err)
	}
	if rebased.CurrentChapter != 0 || rebased.InProgressChapter != 0 ||
		rebased.Phase != domain.PhaseInit || rebased.Flow != "" ||
		rebased.NovelName != progress.NovelName ||
		strings.TrimSpace(rebased.GenerationID) == "" || rebased.GenerationID != receipt.NewGenerationID {
		t.Fatalf("rebase did not publish an unambiguous chapter-zero generation: progress=%+v receipt=%+v", rebased, receipt)
	}
	if err := validatePipelineOutlineAllEntry(store.NewStore(live)); err != nil {
		t.Fatalf("rebased cursor is not a valid outline-all entry: %v", err)
	}
	for rel, want := range foundation {
		got, err := os.ReadFile(filepath.Join(live, filepath.FromSlash(rel)))
		if err != nil || string(got) != string(want) {
			t.Fatalf("rebase changed foundation %s: err=%v", rel, err)
		}
	}
}

func TestExplicitRebaseCandidateLoaderUsesPremiseTitleOutsideCanonicalOutputPath(t *testing.T) {
	live := seedZeroInitProject(t)
	st := store.NewStore(live)
	if err := st.Outline.SavePremise("# 《旧纹有姓名》\n\n完整故事前提。\n"); err != nil {
		t.Fatal(err)
	}
	candidate := filepath.Join(t.TempDir(), ".canon-rebase", "rebase-test", "output")
	if err := copyPipelineRenderCandidateTree(live, candidate); err != nil {
		t.Fatal(err)
	}
	project, err := loadZeroInitProjectForExplicitRebaseCandidate(store.NewStore(candidate), candidate)
	if err != nil {
		t.Fatal(err)
	}
	if project.Name != "旧纹有姓名" {
		t.Fatalf("rebase candidate derived project name %q from its temporary path", project.Name)
	}
}

func TestPipelineRebaseAllChaptersAllowsCompassNewerThanArchitectReadiness(t *testing.T) {
	live := seedZeroInitProject(t)
	st := store.NewStore(live)
	if err := st.Progress.Init("stale-architect-readiness-rebase", 1); err != nil {
		t.Fatal(err)
	}
	rebaseAllTestWriteFile(t, live, "chapters/01.md", "旧正史第一章。\n")
	if err := st.Progress.MarkChapterComplete(1, 8, "old-receipt", "main"); err != nil {
		t.Fatal(err)
	}

	readinessRaw, err := os.ReadFile(filepath.Join(live, "meta", "architect_readiness.json"))
	if err != nil {
		t.Fatal(err)
	}
	var readiness architectReadiness
	if err := json.Unmarshal(readinessRaw, &readiness); err != nil {
		t.Fatal(err)
	}
	generatedAt, err := time.Parse(time.RFC3339, readiness.GeneratedAt)
	if err != nil {
		t.Fatal(err)
	}
	compassPath := filepath.Join(live, "meta", "compass.json")
	compassUpdatedAt := generatedAt.Add(architectFreshnessGrace + time.Second)
	if err := os.Chtimes(compassPath, compassUpdatedAt, compassUpdatedAt); err != nil {
		t.Fatal(err)
	}

	if ok, reason := architectReadinessState(live); ok || !strings.Contains(reason, "compass.json") {
		t.Fatalf("normal readiness must fail closed after compass update: ok=%v reason=%q", ok, reason)
	}
	if _, err := loadZeroInitProject(st, live); err == nil || !strings.Contains(err.Error(), "compass.json") {
		t.Fatalf("normal zero-init loader accepted stale readiness: %v", err)
	}
	if err := pipelineRebaseAllChapters(
		rebaseAllTestOptions(t, pipelineRebaseRunRoot(live)),
	); err != nil {
		t.Fatalf("explicit rebase must not be blocked before same-run architect refresh: %v", err)
	}
	rebased, err := store.NewStore(live).Progress.Load()
	if err != nil || rebased == nil || rebased.LatestCompleted() != 0 ||
		len(rebased.CompletedChapters) != 0 || rebased.TotalWordCount != 0 {
		t.Fatalf("rebase did not publish chapter-zero candidate: progress=%+v err=%v", rebased, err)
	}
}

func TestPipelineRebaseAllChaptersAllowsFailedArchitectReadinessBeforeRefresh(t *testing.T) {
	live := seedZeroInitProject(t)
	st := store.NewStore(live)
	if err := st.Progress.Init("failed-architect-readiness-rebase", 1); err != nil {
		t.Fatal(err)
	}
	rebaseAllTestWriteFile(t, live, "chapters/01.md", "旧正史第一章。\n")
	if err := st.Progress.MarkChapterComplete(1, 8, "old-receipt", "main"); err != nil {
		t.Fatal(err)
	}

	readinessRaw, err := os.ReadFile(filepath.Join(live, "meta", "architect_readiness.json"))
	if err != nil {
		t.Fatal(err)
	}
	var readiness architectReadiness
	if err := json.Unmarshal(readinessRaw, &readiness); err != nil {
		t.Fatal(err)
	}
	readiness.Ready = false
	readiness.Issues = []string{"等待同次 architect 刷新"}
	if err := writeArchitectReadiness(live, readiness); err != nil {
		t.Fatal(err)
	}
	if _, err := loadZeroInitProject(st, live); err == nil || !strings.Contains(err.Error(), "Architect 未通过") {
		t.Fatalf("normal zero-init loader accepted failed readiness: %v", err)
	}

	if err := pipelineRebaseAllChapters(
		rebaseAllTestOptions(t, pipelineRebaseRunRoot(live)),
	); err != nil {
		t.Fatalf("explicit rebase must allow same-run architect to repair failed readiness: %v", err)
	}
	rebased, err := store.NewStore(live).Progress.Load()
	if err != nil || rebased == nil || rebased.LatestCompleted() != 0 ||
		len(rebased.CompletedChapters) != 0 || rebased.TotalWordCount != 0 {
		t.Fatalf("rebase did not publish chapter-zero candidate: progress=%+v err=%v", rebased, err)
	}
}

func TestExplicitRebaseCandidateLoaderRequiresCompleteFoundation(t *testing.T) {
	live := seedZeroInitProject(t)
	if err := os.Remove(filepath.Join(live, "world_codex.json")); err != nil {
		t.Fatal(err)
	}
	_, err := loadZeroInitProjectForExplicitRebaseCandidate(store.NewStore(live), live)
	if err == nil || !strings.Contains(err.Error(), "world_codex.json") {
		t.Fatalf("explicit rebase loader accepted incomplete foundation: %v", err)
	}
}

func TestApplyPipelineAllChapterZeroProgressResetUsesLayeredOutlineWithoutSeedingWorld(t *testing.T) {
	outputDir := filepath.Join(t.TempDir(), "output", "novel")
	st := store.NewStore(outputDir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	chapters := []domain.OutlineEntry{
		{Chapter: 1, Title: "一"},
		{Chapter: 2, Title: "二"},
		{Chapter: 3, Title: "三"},
	}
	if err := st.Outline.SaveLayeredOutline([]domain.VolumeOutline{{
		Index: 1,
		Title: "第一卷",
		Arcs:  []domain.ArcOutline{{Index: 1, Title: "第一弧", Chapters: chapters}},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Save(&domain.Progress{
		NovelName:         "旧进度",
		Phase:             domain.PhaseWriting,
		TotalChapters:     99,
		CompletedChapters: []int{1, 2},
		TotalWordCount:    5000,
		GenerationID:      "old-generation",
	}); err != nil {
		t.Fatal(err)
	}
	rebaseAllTestWriteFile(t, outputDir, "meta/world_tick.json", `{"tick_id":"old"}`)
	rebaseAllTestWriteFile(t, outputDir, "meta/world_events.jsonl", `{"id":"old"}`+"\n")
	project := zeroInitProject{
		Name:         "新进度",
		GenerationID: "rebase-generation",
		Outline:      chapters[:1],
	}
	if err := applyPipelineAllChapterZeroProgressReset(outputDir, &project); err != nil {
		t.Fatal(err)
	}
	progress, err := st.Progress.Load()
	if err != nil || progress == nil {
		t.Fatalf("load reset progress: progress=%+v err=%v", progress, err)
	}
	if progress.TotalChapters != 3 ||
		progress.GenerationID != project.GenerationID ||
		progress.Phase != domain.PhaseInit ||
		!progress.Layered ||
		progress.CurrentVolume != 1 ||
		progress.CurrentArc != 1 ||
		len(progress.CompletedChapters) != 0 ||
		progress.TotalWordCount != 0 {
		t.Fatalf("layered chapter-zero progress reset drifted: %+v", progress)
	}
	for _, rel := range []string{
		"meta/world_tick.json",
		"meta/world_events.jsonl",
		"meta/simulation_restart_policy.json",
		"meta/simulation_restart_state.json",
	} {
		if _, err := os.Lstat(filepath.Join(outputDir, filepath.FromSlash(rel))); !os.IsNotExist(err) {
			t.Fatalf("pure progress reset wrote or retained %s: %v", rel, err)
		}
	}
}

func TestPipelineRebaseAllChaptersArchivesExactCanonAndPublishesChapterZero(t *testing.T) {
	live := seedZeroInitProject(t)
	st := store.NewStore(live)
	outline, err := st.Outline.LoadOutline()
	if err != nil || len(outline) != 1 {
		t.Fatalf("load seed outline: outline=%+v err=%v", outline, err)
	}
	outline = append(outline, domain.OutlineEntry{
		Chapter:   2,
		Title:     "红账追索",
		CoreEvent: "江烬沿欠费单继续核验红账来源。",
		Hook:      "收据背面出现新的门牌号。",
		Scenes:    []string{"便利店后门", "旧楼门厅"},
	})
	if err := st.Outline.SaveOutline(outline); err != nil {
		t.Fatal(err)
	}
	readiness := assessArchitectReadiness(live)
	if !readiness.Ready {
		t.Fatalf("refresh architect readiness after outline expansion: missing=%v issues=%v warnings=%v",
			readiness.Missing, readiness.Issues, readiness.Warnings)
	}
	if err := writeArchitectReadiness(live, readiness); err != nil {
		t.Fatalf("write refreshed architect readiness: %v", err)
	}
	if err := zeroInitPipeline(cliOptions{}, []string{
		"--dir", live,
		"--reset-simulation-state",
		"--rebuild-rag=false",
	}); err != nil {
		t.Fatalf("seed chapter-zero artifacts: %v", err)
	}

	oldChapterBodies := map[string]string{
		"chapters/01.md":         "旧正史第一章：午夜欠费单。\n",
		"chapters/02.md":         "旧正史第二章：红账已经推进。\n",
		"drafts/02.draft.md":     "旧第二章草稿。\n",
		"summaries/01.md":        "旧第一章摘要。\n",
		"reviews/02.json":        `{"chapter":2,"verdict":"accept"}`,
		"meta/planning/old.json": `{"generation":"old"}`,
	}
	for rel, body := range oldChapterBodies {
		rebaseAllTestWriteFile(t, live, rel, body)
	}
	activeLedgerBodies := map[string]string{
		"relationship_state.json":   `{"active_relationship":"old-chapter-two"}`,
		"foreshadow_ledger.json":    `{"active_foreshadow":"old-chapter-two"}`,
		"meta/resource_ledger.json": `{"active_resource":"old-chapter-two"}`,
	}
	for rel, body := range activeLedgerBodies {
		rebaseAllTestWriteFile(t, live, rel, body)
	}
	if err := st.Progress.MarkChapterComplete(1, 1200, "receipt", "main"); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.MarkChapterComplete(2, 1300, "doorplate", "main"); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.SetPendingRewrites([]int{2}, "旧第二章待返工"); err != nil {
		t.Fatal(err)
	}

	runRoot := filepath.Dir(filepath.Dir(live))
	configPath := filepath.Join(runRoot, "config.json")
	rebaseAllTestWriteFile(t, runRoot, "config.json", `{
  "provider": "ollama",
  "model": "rebase-all-test-model",
  "providers": {
    "ollama": {
      "type": "openai",
      "base_url": "http://127.0.0.1:11434/v1"
    }
  }
}`)
	opts := cliOptions{ConfigPath: configPath, Dir: runRoot}
	cfg, _, err := loadCfgBundle(opts)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Clean(cfg.OutputDir) != filepath.Clean(live) {
		t.Fatalf("test config resolved output=%s want=%s", cfg.OutputDir, live)
	}
	// Rebase checks the cross-process execution lease before taking its source
	// snapshot. That check durably creates the advisory guard file, so exercise
	// the same boundary before independently calculating the expected root.
	if lock, err := st.Runtime.LoadPipelineExecution(); err != nil || lock != nil {
		t.Fatalf("stabilize execution guard: lock=%+v err=%v", lock, err)
	}
	sourceRoot, err := store.DirectoryContentRoot(live)
	if err != nil {
		t.Fatal(err)
	}
	if err := pipelineRebaseAllChapters(opts); err != nil {
		t.Fatal(err)
	}

	var receipt pipelineAllChapterRebaseReceipt
	if err := readPipelinePlanningJSON(
		filepath.Join(live, "meta", "all_chapter_rebase.json"),
		&receipt,
	); err != nil {
		t.Fatalf("read rebase receipt: %v", err)
	}
	if receipt.Version != "pipeline-all-chapter-rebase.v1" ||
		receipt.SourceRoot != sourceRoot ||
		receipt.ArchiveRoot != sourceRoot ||
		filepath.Clean(receipt.SourceOutput) != filepath.Clean(live) {
		t.Fatalf("rebase receipt did not bind exact source/archive root: %+v want_root=%s", receipt, sourceRoot)
	}
	archiveRoot, err := store.DirectoryContentRoot(receipt.ArchiveOutput)
	if err != nil {
		t.Fatalf("explicit archive was not retained: %v", err)
	}
	if archiveRoot != sourceRoot || archiveRoot != receipt.ArchiveRoot {
		t.Fatalf("archive root drift: actual=%s receipt=%s source=%s", archiveRoot, receipt.ArchiveRoot, sourceRoot)
	}
	for rel, want := range oldChapterBodies {
		got, err := os.ReadFile(filepath.Join(receipt.ArchiveOutput, filepath.FromSlash(rel)))
		if err != nil || string(got) != want {
			t.Fatalf("archive lost %s: got=%q err=%v", rel, got, err)
		}
	}
	for rel, want := range activeLedgerBodies {
		got, err := os.ReadFile(filepath.Join(receipt.ArchiveOutput, filepath.FromSlash(rel)))
		if err != nil || string(got) != want {
			t.Fatalf("archive lost active ledger %s: got=%q err=%v", rel, got, err)
		}
	}
	var previous domain.Progress
	previousRaw, err := os.ReadFile(receipt.PreviousProgress)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(previousRaw, &previous); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(previous.CompletedChapters, []int{1, 2}) ||
		!reflect.DeepEqual(previous.PendingRewrites, []int{2}) ||
		previous.TotalWordCount != 2500 {
		t.Fatalf("archive did not preserve old active progress: %+v", previous)
	}

	progress, err := store.NewStore(live).Progress.Load()
	if err != nil || progress == nil {
		t.Fatalf("load rebased progress: %+v err=%v", progress, err)
	}
	mode, err := store.NewStore(live).LoadWritingPipelineMode()
	if err != nil || mode == nil || mode.Mode != domain.WritingPipelineModeSealedTwoPassV2 {
		t.Fatalf("rebased chapter-zero canon did not persist sealed route intent: mode=%+v err=%v", mode, err)
	}
	if progress.LatestCompleted() != 0 ||
		len(progress.CompletedChapters) != 0 ||
		len(progress.PendingRewrites) != 0 ||
		len(progress.ChapterWordCounts) != 0 ||
		progress.TotalWordCount != 0 ||
		progress.CurrentChapter != 0 ||
		progress.InProgressChapter != 0 ||
		progress.Phase != domain.PhaseInit ||
		progress.GenerationID != receipt.NewGenerationID {
		t.Fatalf("active progress did not return to chapter zero: %+v receipt=%+v", progress, receipt)
	}
	if err := store.NewStore(live).ValidateOutlineAllChapterZeroWorkspace(); err != nil {
		t.Fatalf("rebased live tree is not a valid outline-all chapter-zero workspace: %v", err)
	}
	for rel := range oldChapterBodies {
		if strings.HasPrefix(rel, "chapters/") {
			if _, err := os.Stat(filepath.Join(live, filepath.FromSlash(rel))); !os.IsNotExist(err) {
				t.Fatalf("old chapter %s remained in live canon: %v", rel, err)
			}
		}
	}
	for _, rel := range pipelineRebaseForbiddenDerivedArtifactsForTest() {
		if _, err := os.Lstat(filepath.Join(live, filepath.FromSlash(rel))); !os.IsNotExist(err) {
			t.Fatalf("old chapter/zero-init derived artifact %s survived rebase: %v", rel, err)
		}
	}
	if events, err := store.NewStore(live).World.LoadTimeline(); err != nil || len(events) != 0 {
		t.Fatalf("rebased timeline is not empty: events=%+v err=%v", events, err)
	}

	// A successful directory swap intentionally removes the old runtime tree.
	// Stabilize the new live directory at the same execution-guard boundary
	// that every subsequent rebase invocation crosses before comparing roots.
	if lock, err := store.NewStore(live).Runtime.LoadPipelineExecution(); err != nil || lock != nil {
		t.Fatalf("stabilize rebased execution guard: lock=%+v err=%v", lock, err)
	}
	firstLiveRoot, err := store.DirectoryContentRoot(live)
	if err != nil {
		t.Fatal(err)
	}
	archiveParent := filepath.Dir(filepath.Dir(filepath.Dir(receipt.ArchiveOutput)))
	beforeArchiveEntries, err := os.ReadDir(archiveParent)
	if err != nil {
		t.Fatal(err)
	}
	if err := pipelineRebaseAllChapters(opts); err != nil {
		t.Fatalf("repeated chapter-zero rebase: %v", err)
	}
	secondLiveRoot, err := store.DirectoryContentRoot(live)
	if err != nil {
		t.Fatal(err)
	}
	afterArchiveEntries, err := os.ReadDir(archiveParent)
	if err != nil {
		t.Fatal(err)
	}
	if secondLiveRoot != firstLiveRoot {
		t.Fatalf("repeated chapter-zero rebase changed live root: first=%s second=%s", firstLiveRoot, secondLiveRoot)
	}
	if len(afterArchiveEntries) != len(beforeArchiveEntries) {
		t.Fatalf("repeated chapter-zero rebase created another archive: before=%d after=%d",
			len(beforeArchiveEntries), len(afterArchiveEntries))
	}
	if retainedRoot, err := store.DirectoryContentRoot(receipt.ArchiveOutput); err != nil ||
		retainedRoot != sourceRoot {
		t.Fatalf("repeated chapter-zero rebase damaged retained archive: root=%s err=%v", retainedRoot, err)
	}
}

func pipelineRebaseForbiddenDerivedArtifactsForTest() []string {
	return []string{
		"chapters",
		"drafts",
		"正文.md",
		"relationship_state.json",
		"relationship_state.md",
		"relationship_state.initial.json",
		"relationship_state.initial.md",
		"foreshadow_ledger.json",
		"foreshadow_ledger.md",
		"foreshadow_ledger.initial.json",
		"foreshadow_ledger.initial.md",
		"meta/planning/v2",
		"meta/planning/preplan_receipt.json",
		"meta/characters",
		"meta/resource_ledger.json",
		"meta/resource_ledger.md",
		"meta/initial_resource_ledger.json",
		"meta/initial_resource_ledger.md",
		"meta/simulation_restart_policy.json",
		"meta/simulation_restart_policy.md",
		"meta/simulation_restart_state.json",
		"meta/simulation_restart_state.md",
		"meta/world_foundation.json",
		"meta/world_foundation.md",
		"meta/initial_character_dynamics.json",
		"meta/initial_character_dynamics.md",
		"meta/character_return_plan.json",
		"meta/character_return_plan.md",
		"meta/crowd_role_policy.json",
		"meta/crowd_role_policy.md",
		"meta/prewrite_storycraft_plan.json",
		"meta/prewrite_storycraft_plan.md",
		"meta/world_background_plan.json",
		"meta/world_background_plan.md",
		"meta/ch01_zero_init_plan.md",
		"meta/zero_chapter_context_manifest.json",
		"meta/zero_chapter_context_manifest.md",
		"meta/story_time_contract.json",
		"meta/story_time_contract.md",
		"meta/story_calendar.json",
		"meta/world_events.jsonl",
		"meta/world_tick.json",
		"meta/simulation_tiers.json",
		"meta/offscreen_agenda.json",
		"meta/event_weave.json",
		"meta/info_graph.json",
		"meta/social_mood.json",
		"meta/ritual_calendar.json",
		"meta/physics_axioms.json",
		"meta/moral_ceiling.json",
		"meta/cosmology.json",
		"meta/crowd_life.json",
		"meta/ecological_map.json",
		"meta/cultural_footnotes.json",
		"meta/pacing_contract.json",
		"meta/first_chapter_generation_readiness.json",
		"meta/first_chapter_generation_readiness.md",
	}
}

func TestRebasePublishedOutlineAllThenZeroInitKeepsGenerationID(t *testing.T) {
	live := seedZeroInitProject(t)
	st := store.NewStore(live)
	if err := st.Progress.Init("rebase-outline-zero-init-generation", 1); err != nil {
		t.Fatal(err)
	}
	rebaseAllTestWriteFile(t, live, "chapters/01.md", "旧正史第一章。\n")
	if err := st.Progress.MarkChapterComplete(1, 8, "旧钩子", "main"); err != nil {
		t.Fatal(err)
	}
	runRoot := pipelineRebaseRunRoot(live)
	if err := pipelineRebaseAllChapters(rebaseAllTestOptions(t, runRoot)); err != nil {
		t.Fatal(err)
	}
	rebasedProgress, err := store.NewStore(live).Progress.Load()
	if err != nil || rebasedProgress == nil || strings.TrimSpace(rebasedProgress.GenerationID) == "" {
		t.Fatalf("load rebased generation: progress=%+v err=%v", rebasedProgress, err)
	}
	wantGeneration := rebasedProgress.GenerationID

	expectedLiveRoot, err := store.DirectoryContentRoot(live)
	if err != nil {
		t.Fatal(err)
	}
	attemptID := outlineAllGateAttemptID()
	candidateDir := pipelineOutlineAllCandidatePath(live, attemptID)
	if err := copyPipelineRenderCandidateTree(live, candidateDir); err != nil {
		t.Fatal(err)
	}
	writeOutlineAllGateCompleteReceipt(t, candidateDir, candidateDir, expectedLiveRoot)
	candidateStore := store.NewStore(candidateDir)
	outlineProgress, err := candidateStore.Progress.Load()
	if err != nil || outlineProgress == nil {
		t.Fatalf("load outline-all candidate progress: progress=%+v err=%v", outlineProgress, err)
	}
	outlineProgress.GenerationID = wantGeneration
	outlineProgress.GenerationMode = rebasedProgress.GenerationMode
	outlineProgress.Phase = domain.PhaseInit
	if err := candidateStore.Progress.Save(outlineProgress); err != nil {
		t.Fatal(err)
	}
	compass, err := candidateStore.Outline.LoadCompass()
	if err != nil || compass == nil {
		t.Fatalf("load outline-all candidate compass: compass=%+v err=%v", compass, err)
	}
	compassDigest, err := domain.ComputeStoryCompassDigest(*compass)
	if err != nil {
		t.Fatal(err)
	}
	architectReadiness := assessArchitectReadiness(candidateDir)
	if !architectReadiness.Ready {
		t.Fatalf("outline-all candidate architect readiness: missing=%v issues=%v warnings=%v",
			architectReadiness.Missing, architectReadiness.Issues, architectReadiness.Warnings)
	}
	if err := writeArchitectReadiness(candidateDir, architectReadiness); err != nil {
		t.Fatal(err)
	}
	protectedRoot, err := pipelineOutlineAllProtectedCanonRoot(candidateDir)
	if err != nil {
		t.Fatal(err)
	}
	stableRoot, err := pipelineOutlineAllStableProgressRoot(candidateDir)
	if err != nil {
		t.Fatal(err)
	}
	foundation, err := loadPipelineOutlineAllFrozenFoundation(candidateDir)
	if err != nil {
		t.Fatal(err)
	}
	readinessJSON, err := pipelineRequiredFileSHA(candidateDir, "meta/architect_readiness.json")
	if err != nil {
		t.Fatal(err)
	}
	readinessMD, err := pipelineRequiredFileSHA(candidateDir, "meta/architect_readiness.md")
	if err != nil {
		t.Fatal(err)
	}
	outlineReceipt, err := candidateStore.LoadOutlineAllExecutionReceipt()
	if err != nil || outlineReceipt == nil {
		t.Fatalf("load candidate outline receipt: receipt=%+v err=%v", outlineReceipt, err)
	}
	if _, err := candidateStore.UpdateOutlineAllExecutionReceipt(
		outlineReceipt.ReceiptDigest,
		func(current *domain.OutlineAllExecutionReceipt) error {
			current.GenerationID = wantGeneration
			current.CompassDigest = compassDigest
			current.ProtectedCanonRoot = protectedRoot
			current.StableProgressRoot = stableRoot
			current.FoundationContextRoot = foundation.Root
			current.ArchitectReadinessJSONDigest = readinessJSON
			current.ArchitectReadinessMDDigest = readinessMD
			current.UpdatedAt = time.Now().UTC()
			return nil
		},
	); err != nil {
		t.Fatal(err)
	}

	publisher := store.NewDirectoryPublishStore(pipelineOutlineAllPublishRoot(live))
	published, err := publisher.PublishDirectory(store.PublishDirectoryRequest{
		TransactionID:    attemptID,
		LiveDir:          live,
		CandidateDir:     candidateDir,
		ExpectedLiveRoot: expectedLiveRoot,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := publisher.FinalizeDirectoryPublish(attemptID); err != nil {
		t.Fatal(err)
	}
	liveStore := store.NewStore(live)
	outlineReceipt, err = liveStore.LoadOutlineAllExecutionReceipt()
	if err != nil || outlineReceipt == nil {
		t.Fatalf("load promoted outline receipt: receipt=%+v err=%v", outlineReceipt, err)
	}
	if _, err := liveStore.UpdateOutlineAllExecutionReceipt(
		outlineReceipt.ReceiptDigest,
		func(current *domain.OutlineAllExecutionReceipt) error {
			current.PublishedCandidateRoot = published.CandidateRoot
			current.DirectoryPublishReceiptDigest = published.ReceiptDigest
			current.UpdatedAt = time.Now().UTC()
			return nil
		},
	); err != nil {
		t.Fatal(err)
	}
	beforeZero, err := liveStore.Progress.Load()
	if err != nil || beforeZero == nil || beforeZero.GenerationID != wantGeneration ||
		outlineReceipt.GenerationID != wantGeneration {
		latestReceipt, _ := liveStore.LoadOutlineAllExecutionReceipt()
		t.Fatalf(
			"published outline-all did not preserve rebase generation before zero-init: want=%s progress=%+v stale_receipt=%+v latest_receipt=%+v err=%v",
			wantGeneration,
			beforeZero,
			outlineReceipt,
			latestReceipt,
			err,
		)
	}

	if err := zeroInitPipeline(cliOptions{}, []string{
		"--dir", live,
		"--reset-simulation-state",
		"--rebuild-rag=false",
	}); err != nil {
		t.Fatalf("zero-init after published outline-all: %v", err)
	}
	after, err := liveStore.Progress.Load()
	if err != nil || after == nil || after.GenerationID != wantGeneration {
		t.Fatalf("zero-init changed generation: want=%s progress=%+v err=%v", wantGeneration, after, err)
	}
	policy, err := liveStore.LoadSimulationRestartPolicy()
	if err != nil || policy == nil || policy.GenerationID != wantGeneration {
		t.Fatalf("zero-init policy changed generation: want=%s policy=%+v err=%v", wantGeneration, policy, err)
	}
	outlineReceipt, err = liveStore.LoadOutlineAllExecutionReceipt()
	if err != nil || outlineReceipt == nil || outlineReceipt.GenerationID != wantGeneration {
		t.Fatalf("outline receipt generation drifted: want=%s receipt=%+v err=%v", wantGeneration, outlineReceipt, err)
	}
	if got, err := resolveZeroInitGenerationID(liveStore, wantGeneration, "generated"); err != nil || got != wantGeneration {
		t.Fatalf("matching explicit generation was rejected: got=%s err=%v", got, err)
	}
	if _, err := resolveZeroInitGenerationID(liveStore, "different-generation", "generated"); err == nil || !strings.Contains(err.Error(), "cannot override published outline-all generation") {
		t.Fatalf("mismatched explicit generation overrode published outline-all: %v", err)
	}
}

func TestPipelineRebaseAllChaptersRefreshesDirtyChapterZeroEpoch(t *testing.T) {
	live := seedZeroInitProject(t)
	if err := store.NewStore(live).Progress.Init("dirty-chapter-zero", 1); err != nil {
		t.Fatal(err)
	}
	runRoot := pipelineRebaseRunRoot(live)
	opts := rebaseAllTestOptions(t, runRoot)
	preserved := make(map[string]string)
	for _, rel := range []string{
		"premise.md",
		"outline.json",
		"characters.json",
		"world_rules.json",
		"book_world.json",
	} {
		raw, err := os.ReadFile(filepath.Join(live, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		preserved[rel] = string(raw)
	}
	preserved["meta/initial_review_lessons.md"] = "RAG source lesson\n"
	rebaseAllTestWriteFile(
		t,
		live,
		"meta/initial_review_lessons.md",
		preserved["meta/initial_review_lessons.md"],
	)
	rebaseAllTestWriteFile(t, runRoot, "prompt.md", "project RAG source survives\n")
	stale := map[string]string{
		"meta/planning/v2/.building/pg2_stale/generation.json": `{"stale":true}`,
		"meta/planning/v2/projection_cursor.json":              `{"stale":true}`,
		"meta/planning/preplan_receipt.json":                   `{"stale":true}`,
		"meta/pipeline.json":                                   `{"stale":true}`,
		"meta/first_chapter_generation_readiness.json":         `{"ready":false}`,
		"meta/sessions/agents/project-all-ch01.jsonl":          `{"stale":true}`,
	}
	for rel, body := range stale {
		rebaseAllTestWriteFile(t, live, rel, body)
	}
	workspaceFile := filepath.Join(
		runRoot,
		".project-all",
		"pg2_stale",
		"output",
		"novel",
		"meta",
		"project_all_state.json",
	)
	rebaseAllTestWriteFile(t, filepath.Dir(workspaceFile), filepath.Base(workspaceFile), `{"stale":true}`)

	if err := pipelineRebaseAllChapters(opts); err != nil {
		t.Fatal(err)
	}
	var receipt pipelineAllChapterRebaseReceipt
	if err := readPipelinePlanningJSON(
		filepath.Join(live, "meta", "all_chapter_rebase.json"),
		&receipt,
	); err != nil {
		t.Fatal(err)
	}
	if receipt.ArchivedProjectAllWorkspaces == "" {
		t.Fatalf("chapter-zero rebase did not archive stale project-all workspace: %+v", receipt)
	}
	if _, err := os.Stat(filepath.Join(receipt.ArchivedProjectAllWorkspaces, "pg2_stale")); err != nil {
		t.Fatalf("stale project-all workspace archive missing: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(runRoot, ".project-all")); !os.IsNotExist(err) {
		t.Fatalf("stale live project-all workspace survived rebase: %v", err)
	}
	for rel := range stale {
		if _, err := os.Lstat(filepath.Join(live, filepath.FromSlash(rel))); !os.IsNotExist(err) {
			t.Fatalf("chapter-zero failed epoch artifact %s survived: %v", rel, err)
		}
	}
	for rel, want := range preserved {
		got, err := os.ReadFile(filepath.Join(live, filepath.FromSlash(rel)))
		if err != nil || string(got) != want {
			t.Fatalf("foundation/RAG source %s changed: got=%q err=%v", rel, got, err)
		}
	}
	if got, err := os.ReadFile(filepath.Join(runRoot, "prompt.md")); err != nil ||
		string(got) != "project RAG source survives\n" {
		t.Fatalf("project-level RAG source changed: got=%q err=%v", got, err)
	}
	progress, err := store.NewStore(live).Progress.Load()
	if err != nil || progress == nil ||
		progress.LatestCompleted() != 0 ||
		len(progress.CompletedChapters) != 0 ||
		len(progress.PendingRewrites) != 0 {
		t.Fatalf("dirty chapter-zero refresh changed canon progress: progress=%+v err=%v", progress, err)
	}
}

func TestPipelineRebaseAllChaptersExplicitlyArchivesActiveSealedGeneration(t *testing.T) {
	live := seedZeroInitProject(t)
	if err := store.NewStore(live).Progress.Init("active-sealed-chapter-zero", 1); err != nil {
		t.Fatal(err)
	}
	activeID := rebaseAllTestActivateOneChapterGeneration(t, live)
	runRoot := pipelineRebaseRunRoot(live)
	opts := rebaseAllTestOptions(t, runRoot)

	if err := pipelineRebaseAllChapters(opts); err != nil {
		t.Fatal(err)
	}
	var receipt pipelineAllChapterRebaseReceipt
	if err := readPipelinePlanningJSON(
		filepath.Join(live, "meta", "all_chapter_rebase.json"),
		&receipt,
	); err != nil {
		t.Fatal(err)
	}
	if receipt.ArchivedPlanningGenerationID != activeID ||
		receipt.PlanningGenerationArchiveReceipt == "" {
		t.Fatalf("active sealed generation was not explicitly archived: %+v", receipt)
	}
	archivedProjected := store.NewStore(receipt.ArchiveOutput).ProjectedV2()
	archives, err := archivedProjected.LoadGenerationArchiveReceipts(activeID)
	if err != nil || len(archives) != 1 ||
		archives[0].ReceiptDigest != receipt.PlanningGenerationArchiveReceipt {
		t.Fatalf("generation lifecycle receipt missing from exact output archive: archives=%+v err=%v", archives, err)
	}
	if _, err := archivedProjected.LoadSealedGeneration(activeID); err != nil {
		t.Fatalf("immutable sealed generation was not retained in output archive: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(live, "meta", "planning", "v2")); !os.IsNotExist(err) {
		t.Fatalf("archived active planning controls survived in new chapter-zero live tree: %v", err)
	}
}

func rebaseAllTestActivateOneChapterGeneration(t *testing.T, outputDir string) string {
	t.Helper()
	projected := store.NewStore(outputDir).ProjectedV2()
	generation, registry := projectAllCmdTestGenerationAndRegistry(t, 1)
	source := domain.PlanningSourceSnapshotV2{
		Version:                domain.PlanningSourceSnapshotV2Version,
		GenerationID:           generation.GenerationID,
		BaseCanonChapter:       generation.BaseCanonChapter,
		BaseCanonRoot:          generation.BaseCanonRoot,
		BaseStateRoot:          generation.BaseStateRoot,
		StableOutlineRoot:      generation.StableOutlineRoot,
		PlanningDependencyRoot: generation.PlanningDependencyRoot,
		RandomSeedContractRoot: generation.RandomSeedContractRoot,
		FoundationSnapshotRoot: projectAllCmdTestDigest("rebase-active-foundation"),
		RAGSnapshotRoot:        projectAllCmdTestDigest("rebase-active-rag"),
		CapturedAt:             "2026-07-17T00:00:00Z",
	}
	var err error
	source.SnapshotDigest, err = domain.ComputePlanningSourceSnapshotV2Digest(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := projected.CreateBuildingGeneration(generation, source, registry); err != nil {
		t.Fatal(err)
	}
	cursor, err := projected.InitializeProjectionCursor(generation.GenerationID)
	if err != nil {
		t.Fatal(err)
	}
	artifacts, outline := projectAllCmdTestArtifacts(t, generation.GenerationID, 1)
	projectAllCmdTestBindPlanningContext(t, artifacts, generation, nil, registry, 1)
	previous, preState, err := pipelineProjectAllTail(generation, nil)
	if err != nil {
		t.Fatal(err)
	}
	bundle, nextRegistry, err := buildPipelineProjectedChapterBundle(
		generation,
		outline,
		previous,
		preState,
		artifacts,
		registry,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := projected.ProjectChapterAndAdvance(
		generation.GenerationDigest,
		generation.ChainTailRoot,
		generation.ObligationRegistryRoot,
		*cursor,
		bundle,
		nextRegistry,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := projected.SealGeneration(generation.GenerationID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := projected.ActivateSealedGeneration(generation.GenerationID, nil); err != nil {
		t.Fatal(err)
	}
	return generation.GenerationID
}

func TestRecoverPipelineRebasePublishesBeforeLoadFinalizesEveryCrashWindow(t *testing.T) {
	for _, phase := range []store.DirectoryPublishPhase{
		store.DirectoryPublishLiveArchived,
		store.DirectoryPublishCandidatePromoted,
		store.DirectoryPublishReceiptWritten,
	} {
		t.Run(string(phase), func(t *testing.T) {
			fixture := newPipelineRebaseRecoveryFixture(t, phase)
			if err := recoverPipelineRebasePublishesBeforeLoad(fixture.opts); err != nil {
				t.Fatalf("recover %s: %v", phase, err)
			}
			state, err := fixture.publisher.LoadDirectoryPublishState(fixture.transactionID)
			if err != nil {
				t.Fatal(err)
			}
			if state == nil ||
				state.Phase != store.DirectoryPublishFinalized ||
				state.Intent != nil ||
				state.Receipt == nil ||
				state.Receipt.CandidateRoot != fixture.candidateRoot ||
				state.Receipt.CommittedLiveRoot != fixture.candidateRoot {
				t.Fatalf("recovered state=%+v, want finalized candidate root %s", state, fixture.candidateRoot)
			}
			liveRoot, err := store.DirectoryContentRoot(fixture.live)
			if err != nil {
				t.Fatal(err)
			}
			if liveRoot != fixture.candidateRoot {
				t.Fatalf("recovered live root=%s want=%s", liveRoot, fixture.candidateRoot)
			}
			if _, err := os.Lstat(fixture.candidate); !os.IsNotExist(err) {
				t.Fatalf("finalized recovery retained candidate: %v", err)
			}
			if _, err := os.Lstat(fixture.archive); !os.IsNotExist(err) {
				t.Fatalf("finalized recovery retained rollback archive: %v", err)
			}
			firstProtocolRoot, err := store.DirectoryContentRoot(
				pipelineRebaseTransactionRoot(fixture.live),
			)
			if err != nil {
				t.Fatal(err)
			}
			if err := recoverPipelineRebasePublishesBeforeLoad(fixture.opts); err != nil {
				t.Fatalf("repeat recover %s: %v", phase, err)
			}
			secondProtocolRoot, err := store.DirectoryContentRoot(
				pipelineRebaseTransactionRoot(fixture.live),
			)
			if err != nil {
				t.Fatal(err)
			}
			if secondProtocolRoot != firstProtocolRoot {
				t.Fatalf(
					"repeat recovery rewrote protocol state: first=%s second=%s",
					firstProtocolRoot,
					secondProtocolRoot,
				)
			}
		})
	}
}

func TestPipelineRebaseAllAcquiresRunRootExclusiveBeforeRecoveryAndConfigLoad(t *testing.T) {
	runRoot := t.TempDir()
	live := filepath.Join(runRoot, "output", "novel")
	if err := os.MkdirAll(live, 0o755); err != nil {
		t.Fatal(err)
	}
	opts := rebaseAllTestOptions(t, runRoot)
	releaseShared, err := acquirePipelineOutlineAllControl(live, false)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = releaseShared() }()
	err = pipelineRebaseAllChapters(opts)
	if err == nil || !strings.Contains(err.Error(), "already held shared") {
		t.Fatalf("rebase did not stop before recovery/load under downstream SH: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(live, "meta", "prompt_manifest.json")); !os.IsNotExist(statErr) {
		t.Fatalf("config loader ran before rebase EX: %v", statErr)
	}
}

func TestPipelineRebaseAllRecoveryFinalizesReceiptBeforeChapterZeroEarlyReturn(t *testing.T) {
	live := seedZeroInitProject(t)
	if err := store.NewStore(live).Progress.Init("chapter-zero-recovery", 1); err != nil {
		t.Fatal(err)
	}
	runRoot := pipelineRebaseRunRoot(live)
	opts := rebaseAllTestOptions(t, runRoot)
	candidate := filepath.Join(
		pipelineRebaseCandidateRoot(live),
		"rebase-chapter-zero-early-return",
		"output",
	)
	if err := copyPipelineRenderCandidateTree(live, candidate); err != nil {
		t.Fatal(err)
	}
	beforeRoot, err := store.DirectoryContentRoot(live)
	if err != nil {
		t.Fatal(err)
	}
	publisher := store.NewDirectoryPublishStore(pipelineRebaseTransactionRoot(live))
	receipt, err := publisher.PublishDirectory(store.PublishDirectoryRequest{
		TransactionID:    "canon-rebase-chapter-zero-early-return",
		LiveDir:          live,
		CandidateDir:     candidate,
		ExpectedLiveRoot: beforeRoot,
	})
	if err != nil {
		t.Fatal(err)
	}
	if state, err := publisher.LoadDirectoryPublishState(receipt.TransactionID); err != nil ||
		state == nil ||
		state.Phase != store.DirectoryPublishReceiptWritten {
		t.Fatalf("precondition state=%+v err=%v, want receipt_written", state, err)
	}

	// The promoted candidate is already at chapter zero. Recovery must still
	// finalize before pipelineRebaseAllChapters observes that state and returns.
	if err := pipelineRebaseAllChapters(opts); err != nil {
		t.Fatalf("chapter-zero early return: %v", err)
	}
	state, err := publisher.LoadDirectoryPublishState(receipt.TransactionID)
	if err != nil {
		t.Fatal(err)
	}
	if state == nil ||
		state.Phase != store.DirectoryPublishFinalized ||
		state.Intent != nil ||
		state.Receipt == nil {
		t.Fatalf("chapter-zero early return left transaction unfinished: %+v", state)
	}
	if _, err := os.Lstat(receipt.ArchiveDir); !os.IsNotExist(err) {
		t.Fatalf("chapter-zero early return retained rollback archive: %v", err)
	}
	if _, err := os.Lstat(candidate); !os.IsNotExist(err) {
		t.Fatalf("chapter-zero early return retained candidate: %v", err)
	}
}

func TestRecoverPipelineRebasePublishesBeforeLoadRejectsForeignProjectIntent(t *testing.T) {
	base := t.TempDir()
	runRoot := filepath.Join(base, "project-a")
	projectLive := filepath.Join(runRoot, "output", "novel")
	rebaseAllTestWriteFile(t, projectLive, "meta/progress.json", `{"current_chapter":0}`)
	opts := rebaseAllTestOptions(t, runRoot)

	foreignRunRoot := filepath.Join(base, "project-b")
	foreignLive := filepath.Join(foreignRunRoot, "output", "novel")
	foreignCandidate := filepath.Join(
		foreignRunRoot,
		".canon-rebase",
		"rebase-foreign",
		"output",
	)
	rebaseAllTestWriteFile(t, foreignLive, "canon.txt", "foreign old canon\n")
	rebaseAllTestWriteFile(t, foreignCandidate, "canon.txt", "foreign promoted canon\n")
	beforeRoot, err := store.DirectoryContentRoot(foreignLive)
	if err != nil {
		t.Fatal(err)
	}
	publisher := store.NewDirectoryPublishStore(pipelineRebaseTransactionRoot(projectLive))
	receipt, err := publisher.PublishDirectory(store.PublishDirectoryRequest{
		TransactionID:    "canon-rebase-foreign-project",
		LiveDir:          foreignLive,
		CandidateDir:     foreignCandidate,
		ExpectedLiveRoot: beforeRoot,
	})
	if err != nil {
		t.Fatal(err)
	}
	foreignRootBeforeRecovery, err := store.DirectoryContentRoot(foreignLive)
	if err != nil {
		t.Fatal(err)
	}

	err = recoverPipelineRebasePublishesBeforeLoad(opts)
	if err == nil || !strings.Contains(err.Error(), "not bound to this live/candidate root") {
		t.Fatalf("foreign transaction recovery error=%v", err)
	}
	foreignRootAfterRecovery, rootErr := store.DirectoryContentRoot(foreignLive)
	if rootErr != nil {
		t.Fatal(rootErr)
	}
	if foreignRootAfterRecovery != foreignRootBeforeRecovery {
		t.Fatalf(
			"foreign live changed during rejected recovery: before=%s after=%s",
			foreignRootBeforeRecovery,
			foreignRootAfterRecovery,
		)
	}
	state, stateErr := publisher.LoadDirectoryPublishState(receipt.TransactionID)
	if stateErr != nil {
		t.Fatal(stateErr)
	}
	if state == nil || state.Phase != store.DirectoryPublishReceiptWritten {
		t.Fatalf("foreign transaction was mutated: %+v", state)
	}
}

type pipelineRebaseRecoveryFixture struct {
	opts          cliOptions
	live          string
	candidate     string
	archive       string
	transactionID string
	candidateRoot string
	publisher     *store.DirectoryPublishStore
}

func newPipelineRebaseRecoveryFixture(
	t *testing.T,
	phase store.DirectoryPublishPhase,
) pipelineRebaseRecoveryFixture {
	t.Helper()
	runRoot := filepath.Join(t.TempDir(), "run")
	live := filepath.Join(runRoot, "output", "novel")
	candidate := filepath.Join(
		pipelineRebaseCandidateRoot(live),
		"rebase-"+string(phase),
		"output",
	)
	rebaseAllTestWriteFile(t, live, "canon.txt", "old canon\n")
	rebaseAllTestWriteFile(t, candidate, "canon.txt", "chapter zero canon\n")
	beforeRoot, err := store.DirectoryContentRoot(live)
	if err != nil {
		t.Fatal(err)
	}
	candidateRoot, err := store.DirectoryContentRoot(candidate)
	if err != nil {
		t.Fatal(err)
	}
	transactionID := "canon-rebase-recovery-" + string(phase)
	publisher := store.NewDirectoryPublishStore(pipelineRebaseTransactionRoot(live))
	receipt, err := publisher.PublishDirectory(store.PublishDirectoryRequest{
		TransactionID:    transactionID,
		LiveDir:          live,
		CandidateDir:     candidate,
		ExpectedLiveRoot: beforeRoot,
	})
	if err != nil {
		t.Fatal(err)
	}
	receiptPath := filepath.Join(
		pipelineRebaseTransactionRoot(live),
		transactionID,
		"receipt.json",
	)
	switch phase {
	case store.DirectoryPublishReceiptWritten:
	case store.DirectoryPublishCandidatePromoted:
		if err := os.Remove(receiptPath); err != nil {
			t.Fatal(err)
		}
	case store.DirectoryPublishLiveArchived:
		if err := os.Remove(receiptPath); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(candidate), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(live, candidate); err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatalf("unsupported crash phase %s", phase)
	}
	state, err := publisher.LoadDirectoryPublishState(transactionID)
	if err != nil {
		t.Fatal(err)
	}
	if state == nil || state.Phase != phase {
		t.Fatalf("fixture phase=%+v want=%s", state, phase)
	}
	return pipelineRebaseRecoveryFixture{
		opts:          rebaseAllTestOptions(t, runRoot),
		live:          live,
		candidate:     candidate,
		archive:       receipt.ArchiveDir,
		transactionID: transactionID,
		candidateRoot: candidateRoot,
		publisher:     publisher,
	}
}

func rebaseAllTestOptions(t *testing.T, runRoot string) cliOptions {
	t.Helper()
	rebaseAllTestWriteFile(t, runRoot, "config.json", `{
  "provider": "ollama",
  "model": "rebase-recovery-test-model",
  "providers": {
    "ollama": {
      "type": "openai",
      "base_url": "http://127.0.0.1:11434/v1"
    }
  }
}`)
	return cliOptions{
		ConfigPath: filepath.Join(runRoot, "config.json"),
		Dir:        runRoot,
	}
}

func rebaseAllTestWriteFile(t *testing.T, root, rel, body string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
