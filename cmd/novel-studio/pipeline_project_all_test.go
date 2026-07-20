package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/chenhongyang/novel-studio/assets"
	"github.com/chenhongyang/novel-studio/internal/agents"
	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/rules"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/tools"
)

func TestPipelineProjectAllWorldMutationVisibilityDoesNotLeakHiddenState(t *testing.T) {
	simulation := &domain.ChapterWorldSimulation{
		ProtagonistProjection: domain.ProtagonistDecisionProjection{
			Protagonist: "程野",
			ObservableEffects: []string{
				"画外声音提前说出南栈订单，但真实藏匿地仍未确认",
				"姜岚启动平台保全",
			},
		},
	}
	tests := []struct {
		name     string
		kind     string
		mutation domain.StateMutationV2
		want     bool
	}{
		{
			name:     "protagonist owns her state",
			kind:     "state",
			mutation: domain.StateMutationV2{Subject: "程野", After: "留在安全停车位"},
			want:     true,
		},
		{
			name:     "hidden antagonist state",
			kind:     "state",
			mutation: domain.StateMutationV2{Subject: "贺铎", After: "准备在00:40转移许知遥"},
			want:     false,
		},
		{
			name:     "hidden victim location",
			kind:     "location",
			mutation: domain.StateMutationV2{Subject: "许知遥", After: "南栈影创园"},
			want:     false,
		},
		{
			name:     "explicitly observable external effect",
			kind:     "state",
			mutation: domain.StateMutationV2{Subject: "姜岚", After: "姜岚启动平台保全"},
			want:     true,
		},
		{
			name:     "obligation is never protagonist knowledge",
			kind:     "obligation",
			mutation: domain.StateMutationV2{Subject: "obl-1", After: "planned"},
			want:     false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := pipelineProjectAllWorldMutationVisibleToProtagonist(
				tt.kind,
				tt.mutation,
				simulation,
			)
			if got != tt.want {
				t.Fatalf("visibility = %t, want %t", got, tt.want)
			}
		})
	}
}

func TestRepairPipelineProjectAllWorldDeltaVisibilityOnResume(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init("visibility-repair", 1); err != nil {
		t.Fatal(err)
	}
	const generationID = "pg2_visibility_repair"
	if err := st.SaveChapterWorldSimulation(domain.ChapterWorldSimulation{
		Chapter: 1,
		ProtagonistProjection: domain.ProtagonistDecisionProjection{
			Protagonist:       "程野",
			ObservableEffects: []string{"姜岚启动平台保全"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveChapterWorldDelta(domain.ChapterWorldDelta{
		Version:      1,
		Chapter:      1,
		GenerationID: generationID,
		WorldDeltas: []domain.WorldChapterDelta{
			{Kind: "state", Entity: "程野", Change: "留在安全停车位", VisibleToProtagonist: true},
			{Kind: "state", Entity: "贺铎", Change: "准备在00:40转移许知遥", VisibleToProtagonist: true},
			{Kind: "location", Entity: "许知遥", Change: "南栈影创园", VisibleToProtagonist: true},
			{Kind: "state", Entity: "姜岚", Change: "姜岚启动平台保全", VisibleToProtagonist: true},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.MarkChapterComplete(1, 0, "", "projected_non_canon"); err != nil {
		t.Fatal(err)
	}
	if err := repairPipelineProjectAllWorldDeltaVisibility(st, generationID, 0); err != nil {
		t.Fatal(err)
	}
	delta, err := st.LoadChapterWorldDelta(1)
	if err != nil {
		t.Fatal(err)
	}
	if delta == nil || len(delta.WorldDeltas) != 4 {
		t.Fatalf("repaired delta missing: %#v", delta)
	}
	want := []bool{true, false, false, true}
	for i, item := range delta.WorldDeltas {
		if item.VisibleToProtagonist != want[i] {
			t.Fatalf("world delta %d visibility = %t, want %t: %#v", i, item.VisibleToProtagonist, want[i], item)
		}
	}
}

func TestApplyPipelineProjectAllPredecessorStateToShadowOutlineIsIdempotent(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Outline.SaveLayeredOutline([]domain.VolumeOutline{{
		Index: 1,
		Title: "第一卷",
		Arcs: []domain.ArcOutline{{
			Index: 1,
			Title: "营救弧",
			Chapters: []domain.OutlineEntry{
				{Chapter: 1, Title: "开门", Scenes: []string{"完成救援"}},
				{Chapter: 2, Title: "后果", Scenes: []string{"进入医疗与取证"}},
			},
		}},
	}}); err != nil {
		t.Fatal(err)
	}
	predecessor := &domain.ProjectedPlanningPredecessorContractV2{
		Chapter:                 1,
		OutgoingConsequenceID:   "out-ch001-rescue-complete",
		OutgoingConsequenceText: "警方已经救出许知遥并控制两名嫌疑人",
	}
	for i := 0; i < 2; i++ {
		entry, err := applyPipelineProjectAllObligationsToOutline(
			st,
			domain.ObligationRegistryV2{},
			predecessor,
			2,
		)
		if err != nil {
			t.Fatal(err)
		}
		if entry == nil || entry.Chapter != 2 {
			t.Fatalf("materialized chapter = %#v", entry)
		}
	}
	volumes, err := st.Outline.LoadLayeredOutline()
	if err != nil {
		t.Fatal(err)
	}
	if len(volumes) != 1 || len(volumes[0].Arcs) != 1 ||
		len(volumes[0].Arcs[0].Chapters) != 2 {
		t.Fatalf("layered outline shape changed: %#v", volumes)
	}
	entry := &volumes[0].Arcs[0].Chapters[1]
	count := 0
	for _, scene := range entry.Scenes {
		if strings.HasPrefix(scene, "[project-all predecessor-state:out-ch001-rescue-complete]") {
			count++
			if !strings.Contains(scene, "不得把同一状态转移重新安排为当前章现场") {
				t.Fatalf("predecessor guard lost no-restaging rule: %q", scene)
			}
		}
	}
	if count != 1 {
		t.Fatalf("predecessor guard count = %d, want 1; scenes=%#v", count, entry.Scenes)
	}
	first := &volumes[0].Arcs[0].Chapters[0]
	for _, scene := range first.Scenes {
		if strings.Contains(scene, "project-all predecessor-state") {
			t.Fatalf("predecessor guard leaked into predecessor chapter: %#v", first.Scenes)
		}
	}
}

func TestPlanningDependenciesBindProjectAllFoundationCorpus(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"layered_outline.json":                    `[{"index":1}]`,
		"outline.json":                            `[{"chapter":1}]`,
		"world_rules.json":                        `[{"rule":"边界"}]`,
		"meta/compass.json":                       `{"ending_direction":"收束"}`,
		"characters.json":                         `[{"name":"林澈"}]`,
		"book_world.json":                         `{"locations":["青山县"]}`,
		"meta/world_foundation.json":              `{"iron_laws":["只许在本县花钱"]}`,
		"meta/initial_character_dynamics.json":    `{"characters":[{"character":"林澈"}]}`,
		"meta/initial_resource_ledger.json":       `{"claims":[]}`,
		"relationship_state.initial.json":         `[]`,
		"foreshadow_ledger.initial.json":          `[]`,
		"meta/characters/林澈/dossier.json":         `{"character":"林澈"}`,
		"meta/volume_codex/v01.json":              `{"volume":1}`,
		"meta/rag/index_state.json":               `{"chunks":[]}`,
		"meta/rag/vector_store.json":              `{"points":[]}`,
		"meta/prewrite_storycraft_plan.json":      `{"version":1}`,
		"meta/prewrite_storycraft_plan.md":        `# 写前工艺`,
		"meta/zero_chapter_context_manifest.json": `{"version":1}`,
		"meta/simulation_restart_policy.json":     `{"active":true}`,
		"meta/web_reference_brief.json":           `{"facts":[]}`,
		"meta/web_reference_brief.md":             `# 联网简报`,
	}
	for rel, body := range files {
		projectAllCmdTestWriteFile(t, filepath.Join(root, filepath.FromSlash(rel)), body)
	}
	dependencies, sourceArtifacts, err := pipelinePlanningDependencies(root)
	if err != nil {
		t.Fatal(err)
	}
	sourceSet := make(map[string]bool, len(sourceArtifacts))
	for _, rel := range sourceArtifacts {
		sourceSet[rel] = true
	}
	for _, rel := range []string{
		"characters.json",
		"book_world.json",
		"meta/world_foundation.json",
		"meta/initial_character_dynamics.json",
		"meta/initial_resource_ledger.json",
		"relationship_state.initial.json",
		"foreshadow_ledger.initial.json",
		"meta/characters/林澈/dossier.json",
		"meta/volume_codex/v01.json",
		"meta/prewrite_storycraft_plan.md",
		"meta/web_reference_brief.md",
		"meta/rag/index_state.json",
		"meta/rag/vector_store.json",
	} {
		if !sourceSet[rel] {
			t.Fatalf("project-all consumed foundation artifact missing from preplan audit set: %s", rel)
		}
	}
	before, err := domain.NewDependencyFingerprint("foundation-test", "canon-root", dependencies)
	if err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{
		"characters.json",
		"meta/world_foundation.json",
		"meta/initial_character_dynamics.json",
		"meta/initial_resource_ledger.json",
		"relationship_state.initial.json",
		"meta/characters/林澈/dossier.json",
	} {
		path := filepath.Join(root, filepath.FromSlash(rel))
		original, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		projectAllCmdTestWriteFile(t, path, string(original)+"\n")
		changed, _, err := pipelinePlanningDependencies(root)
		if err != nil {
			t.Fatal(err)
		}
		after, err := domain.NewDependencyFingerprint("foundation-test", "canon-root", changed)
		if err != nil {
			t.Fatal(err)
		}
		if after.RootSHA256 == before.RootSHA256 {
			t.Fatalf("foundation drift did not change dependency root: %s", rel)
		}
		if err := os.WriteFile(path, original, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestProjectAllRequiresPreservedNonemptyRAGSnapshot(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := requirePipelineProjectAllRAGSnapshot(st); err == nil ||
		!strings.Contains(err.Error(), "chunks>0") {
		t.Fatalf("missing RAG snapshot did not fail project-all entry: %v", err)
	}
	if err := st.RAG.SaveIndexState(domain.RAGIndexState{Chunks: []domain.RAGChunk{}}); err != nil {
		t.Fatal(err)
	}
	if err := requirePipelineProjectAllRAGSnapshot(st); err == nil ||
		!strings.Contains(err.Error(), "chunks>0") {
		t.Fatalf("empty RAG snapshot did not fail project-all entry: %v", err)
	}
	if err := st.RAG.SaveIndexState(domain.RAGIndexState{Chunks: []domain.RAGChunk{{
		ID:         "preserved-craft-index",
		SourcePath: "deconstruction-library/writing-techniques/method.md",
		SourceKind: "craft_method",
		Text:       "人物选择改变信息释放顺序。",
	}}}); err != nil {
		t.Fatal(err)
	}
	if err := requirePipelineProjectAllRAGSnapshot(st); err != nil {
		t.Fatalf("preserved nonempty RAG snapshot was not reusable: %v", err)
	}
}

func TestProjectAllCapturedSourceRootsSeparateCanonRAGAndShadowOutputs(t *testing.T) {
	root := t.TempDir()
	for rel, body := range map[string]string{
		"characters.json":            `[{"name":"林澈"}]`,
		"meta/resource_ledger.json":  `{"claims":[]}`,
		"meta/rag/index_state.json":  `{"chunks":[{"id":"foundation"}]}`,
		"meta/rag/vector_store.json": `{"points":[{"id":"foundation"}]}`,
	} {
		projectAllCmdTestWriteFile(t, filepath.Join(root, filepath.FromSlash(rel)), body)
	}
	progress := &domain.Progress{GenerationID: "source-root-test"}
	canonBefore, err := pipelineCanonRoot(root, progress)
	if err != nil {
		t.Fatal(err)
	}
	foundationBefore, err := pipelineProjectAllFoundationSnapshotRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	ragBefore, err := pipelineProjectAllRAGSnapshotRoot(root)
	if err != nil {
		t.Fatal(err)
	}

	projectAllCmdTestWriteFile(t, filepath.Join(root, "meta", "rag", "index_state.json"), `{"chunks":[{"id":"grown"}]}`)
	projectAllCmdTestWriteFile(t, filepath.Join(root, "meta", "rag", "vector_store.json"), `{"points":[{"id":"grown"}]}`)
	canonAfterRAG, err := pipelineCanonRoot(root, progress)
	if err != nil {
		t.Fatal(err)
	}
	foundationAfterRAG, err := pipelineProjectAllFoundationSnapshotRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	ragAfter, err := pipelineProjectAllRAGSnapshotRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	if canonAfterRAG != canonBefore {
		t.Fatal("derived live-growing RAG changed canonical story root")
	}
	if foundationAfterRAG != foundationBefore {
		t.Fatal("RAG leaked into the separately captured foundation root")
	}
	if ragAfter == ragBefore {
		t.Fatal("exact project-all RAG snapshot root ignored index/vector drift")
	}

	projectAllCmdTestWriteFile(t, filepath.Join(root, "meta", "resource_ledger.json"), `{"claims":[{"id":"seed"}]}`)
	foundationAfterLedger, err := pipelineProjectAllFoundationSnapshotRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	if foundationAfterLedger == foundationBefore {
		t.Fatal("consumed baseline ledger drift did not change captured foundation root")
	}

	for _, rel := range []string{
		"drafts/01.plan.json",
		"meta/chapter_simulations/001.json",
		"meta/planning/current_frozen_plan.json",
	} {
		projectAllCmdTestWriteFile(t, filepath.Join(root, filepath.FromSlash(rel)), `{"generated":true}`)
	}
	foundationAfterGenerated, err := pipelineProjectAllFoundationSnapshotRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	if foundationAfterGenerated != foundationAfterLedger {
		t.Fatal("mutable shadow/generated inference output entered captured foundation root")
	}
}

func TestProjectAllDependencyUsesCapturedSourcesAfterLiveStateGrows(t *testing.T) {
	root := t.TempDir()
	projectAllCmdTestWriteFile(t, filepath.Join(root, "characters.json"), `[{"name":"林澈"}]`)
	projectAllCmdTestWriteFile(t, filepath.Join(root, "meta", "resource_ledger.json"), `{"claims":[]}`)
	projectAllCmdTestWriteFile(t, filepath.Join(root, "meta", "characters", "林澈", "dossier.json"), `{"character":"林澈","status":"seed"}`)
	projectAllCmdTestWriteFile(t, filepath.Join(root, "meta", "rag", "index_state.json"), `{"chunks":[{"id":"seed"}]}`)
	projectAllCmdTestWriteFile(t, filepath.Join(root, "meta", "rag", "vector_store.json"), `{"points":[{"id":"seed"}]}`)
	cfg := bootstrap.Config{OutputDir: root}
	receipt := pipelinePreplanReceipt{
		DependencyRoot:  projectAllCmdTestDigest("preplan"),
		SourceArtifacts: []string{"characters.json", "meta/rag/index_state.json", "meta/rag/vector_store.json"},
	}
	foundationRoot, err := pipelineProjectAllFoundationSnapshotRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	ragRoot, err := pipelineProjectAllRAGSnapshotRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	before, err := pipelineProjectAllDependencyRootWithSourceRoots(
		cfg,
		assets.Bundle{},
		receipt,
		foundationRoot,
		ragRoot,
	)
	if err != nil {
		t.Fatal(err)
	}

	projectAllCmdTestWriteFile(t, filepath.Join(root, "meta", "rag", "index_state.json"), `{"chunks":[{"id":"accepted-chapter"}]}`)
	projectAllCmdTestWriteFile(t, filepath.Join(root, "meta", "rag", "vector_store.json"), `{"points":[{"id":"accepted-chapter"}]}`)
	projectAllCmdTestWriteFile(t, filepath.Join(root, "meta", "resource_ledger.json"), `{"claims":[{"id":"accepted-chapter"}]}`)
	projectAllCmdTestWriteFile(t, filepath.Join(root, "meta", "characters", "林澈", "dossier.json"), `{"character":"林澈","status":"advanced"}`)
	capturedAgain, err := pipelineProjectAllDependencyRootWithSourceRoots(
		cfg,
		assets.Bundle{},
		receipt,
		foundationRoot,
		ragRoot,
	)
	if err != nil {
		t.Fatal(err)
	}
	if capturedAgain != before {
		t.Fatal("post-render live ledger/dossier/RAG growth invalidated captured sealed planning dependency")
	}
	current, err := pipelineProjectAllDependencyRoot(cfg, assets.Bundle{}, receipt)
	if err != nil {
		t.Fatal(err)
	}
	if current == before {
		t.Fatal("fresh project-all identity failed to observe the new live RAG snapshot")
	}
}

func TestProjectAllCLIStageAliasesAndQdrantBoundary(t *testing.T) {
	got, err := resolveStages("projectall,project_all,all-plan,all_plan,seal,promote,render")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"project-all", "project-all", "project-all", "project-all",
		"seal", "promote", "render",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("project-all CLI stage aliases = %v want %v", got, want)
	}
	for _, stages := range [][]string{
		{"project-all"},
		{"projectall", "seal", "promote", "render"},
		{"preplan", "project_all", "seal", "promote", "render"},
	} {
		if pipelineStagesNeedQdrant(stages) {
			t.Fatalf("sealed two-pass stages must never initialize live Qdrant: %v", stages)
		}
	}
	for _, stages := range [][]string{
		{"project-all", "plan"},
		{"promote", "review"},
	} {
		if !pipelineStagesNeedQdrant(stages) {
			t.Fatalf("RAG-aware stage was incorrectly hidden by project-all boundary: %v", stages)
		}
	}
}

func TestCopyProjectAllWorkspaceDoesNotHardLinkMutableCanon(t *testing.T) {
	live := t.TempDir()
	shadow := t.TempDir()
	mutable := map[string]string{
		"meta/world_events.jsonl":       "{\"chapter\":1,\"event\":\"live\"}\n",
		"meta/state_changes.json":       "[{\"chapter\":1,\"field\":\"status\"}]\n",
		"meta/relationships.json":       "[{\"chapter\":1,\"relation\":\"ally\"}]\n",
		"summaries/01.md":               "live summary\n",
		"chapters/01.md":                "live chapter\n",
		"meta/rag/receipts/000001.json": "{\"id\":\"receipt-live\"}\n",
	}
	for rel, body := range mutable {
		projectAllCmdTestWriteFile(t, filepath.Join(live, filepath.FromSlash(rel)), body)
	}
	for rel, body := range map[string]string{
		"meta/rag/index_state.json":  "{\"version\":1}\n",
		"meta/rag/vector_store.json": "{\"vectors\":[]}\n",
	} {
		projectAllCmdTestWriteFile(t, filepath.Join(live, filepath.FromSlash(rel)), body)
	}
	projectAllCmdTestWriteFile(t, filepath.Join(live, "meta", "planning", "must-not-copy.json"), "{}\n")
	projectAllCmdTestWriteFile(t, filepath.Join(live, "meta", "runtime", "must-not-copy.json"), "{}\n")
	projectAllCmdTestWriteFile(t, filepath.Join(live, "meta", "sessions", "agents", "writer-ch01.jsonl"), "{\"stale\":true}\n")
	projectAllCmdTestWriteFile(t, filepath.Join(live, "sessions", "must-not-copy.json"), "{}\n")

	if err := copyProjectAllWorkspace(live, shadow); err != nil {
		t.Fatal(err)
	}

	for rel, want := range mutable {
		livePath := filepath.Join(live, filepath.FromSlash(rel))
		shadowPath := filepath.Join(shadow, filepath.FromSlash(rel))
		liveInfo, err := os.Stat(livePath)
		if err != nil {
			t.Fatal(err)
		}
		shadowInfo, err := os.Stat(shadowPath)
		if err != nil {
			t.Fatal(err)
		}
		if os.SameFile(liveInfo, shadowInfo) {
			t.Fatalf("mutable canon file was hard-linked into shadow workspace: %s", rel)
		}
		f, err := os.OpenFile(shadowPath, os.O_WRONLY|os.O_APPEND, 0)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.WriteString("shadow-only mutation\n"); err != nil {
			_ = f.Close()
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(livePath)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Fatalf("shadow append contaminated live canon %s: got %q want %q", rel, got, want)
		}
	}

	for _, rel := range []string{
		"meta/planning/must-not-copy.json",
		"meta/runtime/must-not-copy.json",
		"meta/sessions/agents/writer-ch01.jsonl",
		"sessions/must-not-copy.json",
	} {
		if _, err := os.Stat(filepath.Join(shadow, filepath.FromSlash(rel))); !os.IsNotExist(err) {
			t.Fatalf("runtime/planning artifact crossed isolation boundary: %s err=%v", rel, err)
		}
	}
}

func TestSanitizeProjectAllWorkspaceRemovesOldFutureInferenceArtifacts(t *testing.T) {
	workspace := t.TempDir()
	preserved := map[string]string{
		"chapters/01.md":       "committed canon remains\n",
		"summaries/01.md":      "committed summary remains\n",
		"layered_outline.json": "[]\n",
		"world_rules.json":     "[]\n",
	}
	for rel, body := range preserved {
		projectAllCmdTestWriteFile(t, filepath.Join(workspace, filepath.FromSlash(rel)), body)
	}
	oldFutureArtifacts := []string{
		"drafts/02.plan.json",
		"drafts/02.plan.partial.json",
		"drafts/02.draft.md",
		"reviews/drafts/02.json",
		"meta/chapter_simulations/002.json",
		"meta/checkpoints.jsonl",
		"meta/pending_commit.json",
		"meta/rag/fact_receipts/receipt-old.json",
		"meta/planning/current_frozen_plan.json",
		"meta/runtime/pipeline_execution.json",
		"meta/sessions/agents/writer-ch02.jsonl",
		"meta/chapter_metrics/02.json",
		"meta/sampling/02.json",
		"meta/scene_dynamics/02.json",
		"meta/delivery_snapshots/ch02.json",
		"meta/rewrite_recovery/ch02.json",
	}
	for _, rel := range oldFutureArtifacts {
		projectAllCmdTestWriteFile(
			t,
			filepath.Join(workspace, filepath.FromSlash(rel)),
			"{\"stale\":true}\n",
		)
	}

	if err := sanitizePipelineProjectAllWorkspace(workspace); err != nil {
		t.Fatal(err)
	}
	for _, rel := range oldFutureArtifacts {
		if _, err := os.Stat(filepath.Join(workspace, filepath.FromSlash(rel))); !os.IsNotExist(err) {
			t.Fatalf("old future inference artifact survived shadow sanitizer: %s err=%v", rel, err)
		}
	}
	for _, rel := range []string{"drafts", "meta/chapter_simulations"} {
		entries, err := os.ReadDir(filepath.Join(workspace, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("sanitizer did not recreate empty %s: %v", rel, err)
		}
		if len(entries) != 0 {
			t.Fatalf("sanitizer recreated non-empty %s: %+v", rel, entries)
		}
	}
	for rel, want := range preserved {
		raw, err := os.ReadFile(filepath.Join(workspace, filepath.FromSlash(rel)))
		if err != nil || string(raw) != want {
			t.Fatalf("sanitizer damaged canonical input %s: body=%q err=%v", rel, raw, err)
		}
	}
}

func TestProjectAllStateIsInjectedOnlyForExactLockedChapter(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Save(&domain.Progress{
		NovelName:      "project-all state injection",
		Phase:          domain.PhaseWriting,
		Flow:           domain.FlowWriting,
		CurrentChapter: 1,
		TotalChapters:  2,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.Outline.SaveOutline([]domain.OutlineEntry{
		{Chapter: 1, Title: "第一章", CoreEvent: "验证", Hook: "留下后果"},
		{Chapter: 2, Title: "第二章", CoreEvent: "承担后果", Hook: "完成阶段"},
	}); err != nil {
		t.Fatal(err)
	}
	generation, registry := projectAllCmdTestGenerationAndRegistry(t, 2)
	planningContext, err := domain.DeriveProjectedPlanningContextV2(
		generation,
		nil,
		registry,
		1,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := savePipelineProjectAllPlanningContext(st, planningContext); err != nil {
		t.Fatal(err)
	}
	const owner = "project-all-state-context-test"
	if err := st.Runtime.AcquirePipelineExecution(domain.PipelineExecutionLock{
		Mode:          domain.PipelineExecutionProjectAll,
		TargetChapter: 1,
		Owner:         owner,
		ExpiresAt:     time.Now().UTC().Add(time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	tool := tools.NewContextTool(st, tools.References{}, "")
	raw, err := tool.Execute(
		context.Background(),
		json.RawMessage(`{"chapter":1,"profile":"world_simulation"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]json.RawMessage
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	var injected domain.ProjectedPlanningContextV2
	if err := json.Unmarshal(result["project_all_state"], &injected); err != nil {
		t.Fatalf("decode injected project_all_state: %v body=%s", err, result["project_all_state"])
	}
	if injected.ContextDigest != planningContext.ContextDigest ||
		injected.StateRoot != planningContext.StateRoot ||
		injected.NextChapter != 1 {
		t.Fatalf("novel_context injected a different projected state: got=%+v want=%+v", injected, planningContext)
	}
	var policy string
	if err := json.Unmarshal(result["project_all_state_policy"], &policy); err != nil ||
		!strings.Contains(policy, "唯一 projected 前态") {
		t.Fatalf("project-all precedence policy missing: policy=%q err=%v", policy, err)
	}
	if _, err := tool.Execute(
		context.Background(),
		json.RawMessage(`{"chapter":2,"profile":"planning"}`),
	); err == nil || !strings.Contains(err.Error(), "targets chapter 1") {
		t.Fatalf("project-all state leaked to a different chapter: %v", err)
	}
	if err := st.Runtime.ReleasePipelineExecution(owner); err != nil {
		t.Fatal(err)
	}
	raw, err = tool.Execute(
		context.Background(),
		json.RawMessage(`{"chapter":1,"profile":"planning"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	result = nil
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	if _, exists := result["project_all_state"]; exists {
		t.Fatal("project_all_state was injected outside a project-all execution lease")
	}
}

func TestProjectAllAuthorityNoOpNeverEntersProjectedState(t *testing.T) {
	hold := domain.CharacterWorldDecision{
		Character:         "离屏角色",
		Location:          "unknown",
		CurrentGoal:       "hold_baseline",
		Pressure:          "authority_missing",
		KnowledgeBoundary: "authority_missing",
		AvailableOptions:  []string{"hold_baseline", "wait_for_authoritative_state"},
		Decision:          "hold_baseline",
		DecisionReason:    "authority_missing",
		Action:            "hold_baseline",
		ActionDuration:    "not_applicable",
		CompletionState:   "blocked",
		ImmediateResult:   "no_chapter_effect",
		StateAfter:        "unchanged_authoritative_baseline",
		VisibleToPOV:      false,
		ButterflyEffects: []domain.DecisionButterflyEffect{{
			Effect:            "transmission_blocked",
			TransmissionPath:  "authority_missing",
			ArrivalChapter:    1,
			Visibility:        "hidden",
			ProtagonistImpact: "none",
		}},
	}
	active := domain.CharacterWorldDecision{
		Character:         "林澈",
		Location:          "河畔夜市",
		CurrentGoal:       "核验专项额度",
		Pressure:          "不能连累家人",
		KnowledgeBoundary: "只知道本人看见的系统提示",
		AvailableOptions:  []string{"暂缓", "小额试钱"},
		Decision:          "小额试钱",
		DecisionReason:    "结果可撤回并可现场核验",
		Action:            "购买安全照明和防绊护套",
		ActionDuration:    "一晚",
		CompletionState:   "completed",
		ImmediateResult:   "顾客安全停步并完成消费",
		StateAfter:        "首笔真实县内支出已验证",
		VisibleToPOV:      true,
	}
	sim := domain.ChapterWorldSimulation{
		Chapter:            1,
		CharacterDecisions: []domain.CharacterWorldDecision{hold, active},
	}
	plan := domain.ChapterPlan{
		Chapter: 1,
		Goal:    "完成首笔核验",
		CausalSimulation: domain.ChapterCausalSimulation{
			OutcomeShift: []string{"专项额度完成一次真实核验"},
		},
	}

	if !pipelineProjectAllAuthorityNoOp(hold) || pipelineProjectAllAuthorityNoOp(active) {
		t.Fatal("authority no-op classifier did not distinguish frozen and active decisions")
	}
	delta := pipelineProjectAllDelta(1, sim, plan, nil, nil, nil)
	for _, mutations := range [][]domain.StateMutationV2{
		delta.CharacterState,
		delta.Locations,
		delta.Knowledge,
	} {
		for _, mutation := range mutations {
			if mutation.Subject == hold.Character ||
				strings.Contains(mutation.After, "hold_baseline") ||
				strings.Contains(mutation.After, "authority_missing") ||
				strings.Contains(mutation.After, "unchanged_authoritative_baseline") {
				t.Fatalf("authority no-op leaked into projected delta: %+v", mutation)
			}
		}
	}
	if len(delta.CharacterState) != 1 || delta.CharacterState[0].Subject != active.Character {
		t.Fatalf("active character transition was lost while filtering no-op: %+v", delta.CharacterState)
	}
}

func TestPrepareProjectAllWorkspaceMaterializesCoarseSlotsAndResetsShadowProgress(t *testing.T) {
	runRoot := t.TempDir()
	live := filepath.Join(runRoot, "output", "novel")
	liveStore := store.NewStore(live)
	if err := liveStore.Init(); err != nil {
		t.Fatal(err)
	}
	volumes := []domain.VolumeOutline{{
		Index: 1,
		Title: "第一卷",
		Arcs: []domain.ArcOutline{
			{
				Index: 1,
				Title: "已成正史",
				Chapters: []domain.OutlineEntry{{
					Chapter: 1, Title: "第一章", CoreEvent: "正史发生", Hook: "进入未来",
				}},
			},
			{Index: 2, Title: "预留章位", EstimatedChapters: 2},
		},
	}}
	if err := liveStore.Outline.SaveLayeredOutline(volumes); err != nil {
		t.Fatal(err)
	}
	if err := liveStore.Progress.Save(&domain.Progress{
		NovelName:            "隔离测试",
		Phase:                domain.PhaseComplete,
		Flow:                 domain.FlowRewriting,
		CurrentChapter:       3,
		TotalChapters:        3,
		CompletedChapters:    []int{1, 2},
		PendingRewrites:      []int{1, 2},
		RewriteReason:        "live-only",
		InProgressChapter:    2,
		CompletedScenes:      []int{1, 2},
		ReopenedFromComplete: true,
	}); err != nil {
		t.Fatal(err)
	}
	projected := []domain.OutlineEntry{
		{Chapter: 2, Title: "第二章", CoreEvent: "角色作出选择", Hook: "选择留下代价"},
		{Chapter: 3, Title: "第三章", CoreEvent: "代价抵达现场", Hook: "必须再次选择"},
	}
	projectAllCmdTestInstallCoarseManifests(t, liveStore, projected, 1)

	liveBefore := projectAllCmdTestSnapshotFiles(t, live)
	shadow, err := preparePipelineProjectAllWorkspace(live, "pg2_workspace_test", 1, false)
	if err != nil {
		t.Fatal(err)
	}
	wantShadow := filepath.Join(runRoot, ".project-all", "pg2_workspace_test", "output", "novel")
	if shadow != wantShadow || strings.HasPrefix(shadow, live+string(filepath.Separator)) {
		t.Fatalf("shadow workspace must be a sibling of output, not recursively nested: got %s want %s", shadow, wantShadow)
	}
	liveAfter := projectAllCmdTestSnapshotFiles(t, live)
	if !reflect.DeepEqual(liveBefore, liveAfter) {
		t.Fatalf("project-all workspace preparation mutated live files:\nbefore=%v\nafter=%v", liveBefore, liveAfter)
	}

	shadowStore := store.NewStore(shadow)
	gotVolumes, err := shadowStore.Outline.LoadLayeredOutline()
	if err != nil {
		t.Fatal(err)
	}
	if len(gotVolumes) != 1 || len(gotVolumes[0].Arcs) != 2 {
		t.Fatalf("materialized outline shape = %+v", gotVolumes)
	}
	coarseArc := gotVolumes[0].Arcs[1]
	if coarseArc.EstimatedChapters != 0 || !reflect.DeepEqual(coarseArc.Chapters, projected) {
		t.Fatalf("coarse slots were not materialized exactly: %+v", coarseArc)
	}
	progress, err := shadowStore.Progress.Load()
	if err != nil {
		t.Fatal(err)
	}
	if progress == nil ||
		!reflect.DeepEqual(progress.CompletedChapters, []int{1}) ||
		progress.CurrentChapter != 2 ||
		progress.Phase != domain.PhaseWriting ||
		progress.Flow != domain.FlowWriting ||
		len(progress.PendingRewrites) != 0 ||
		progress.RewriteReason != "" ||
		progress.ReopenedFromComplete ||
		progress.InProgressChapter != 0 ||
		len(progress.CompletedScenes) != 0 {
		t.Fatalf("shadow progress did not reset to the base-canon boundary: %+v", progress)
	}

	generation := domain.PlanningGenerationV2{
		GenerationID:         "pg2_workspace_test",
		LastProjectedChapter: 3,
	}
	registry := domain.ObligationRegistryV2{
		Version:      domain.ObligationRegistryV2Version,
		GenerationID: generation.GenerationID,
		FirstChapter: 2,
		LastChapter:  3,
	}
	if _, _, err := buildPipelineProjectedChapterBundle(
		generation,
		projected[0],
		projectAllCmdTestDigest("previous"),
		projectAllCmdTestDigest("state"),
		nil,
		registry,
	); err == nil || !strings.Contains(err.Error(), "full simulation and plan") {
		t.Fatalf("materialized coarse slot was allowed to masquerade as a formal bundle: %v", err)
	}
}

func TestBuildPipelineProjectedChapterBundleBindsExactRenderContext(t *testing.T) {
	generation, registry := projectAllCmdTestGenerationAndRegistry(t, 1)
	genesis, err := domain.DeriveProjectedChainGenesisV2(generation)
	if err != nil {
		t.Fatal(err)
	}
	artifacts, outline := projectAllCmdTestArtifacts(t, generation.GenerationID, 1)
	projectAllCmdTestBindPlanningContext(t, artifacts, generation, nil, registry, 1)
	bundle, _, err := buildPipelineProjectedChapterBundle(
		generation,
		outline,
		genesis,
		generation.BaseStateRoot,
		artifacts,
		registry,
	)
	if err != nil {
		t.Fatal(err)
	}
	wantRenderDigest, err := domain.ComputePlanningV2JSONDigest(bundle.RenderContext)
	if err != nil {
		t.Fatal(err)
	}
	if bundle.RenderContextSHA256 != wantRenderDigest {
		t.Fatalf("render context digest = %s want %s", bundle.RenderContextSHA256, wantRenderDigest)
	}
	var boundContext map[string]any
	if err := json.Unmarshal(bundle.RenderContext, &boundContext); err != nil {
		t.Fatal(err)
	}
	if _, ok := boundContext["sealed_projection_contract"].(map[string]any); !ok {
		t.Fatal("render context did not expose the exact sealed projection contract")
	}
	if err := domain.ValidateProjectedChapterBundle(bundle); err != nil {
		t.Fatalf("builder emitted invalid exact render binding: %v", err)
	}

	var tamperedContext map[string]any
	if err := json.Unmarshal(bundle.RenderContext, &tamperedContext); err != nil {
		t.Fatal(err)
	}
	sealed := tamperedContext["sealed_projection_contract"].(map[string]any)
	sealed["chapter_plan_digest"] = projectAllCmdTestDigest("偷偷替换后的计划")
	bundle.RenderContext, err = json.Marshal(tamperedContext)
	if err != nil {
		t.Fatal(err)
	}
	bundle.RenderContextSHA256, err = domain.ComputePlanningV2JSONDigest(bundle.RenderContext)
	if err != nil {
		t.Fatal(err)
	}
	bundle.BundleDigest, err = domain.ComputeProjectedChapterBundleDigest(bundle)
	if err != nil {
		t.Fatal(err)
	}
	if err := domain.ValidateProjectedChapterBundle(bundle); err == nil ||
		!strings.Contains(err.Error(), "sealed plan/simulation digest mismatch") {
		t.Fatalf("render context drift was not rejected after recomputing outer hashes: %v", err)
	}
}

func TestBuildPipelineProjectedChapterBundleBindsMaterialRAGReceiptIdentity(t *testing.T) {
	generation, registry := projectAllCmdTestGenerationAndRegistry(t, 1)
	genesis, err := domain.DeriveProjectedChainGenesisV2(generation)
	if err != nil {
		t.Fatal(err)
	}
	artifacts, outline := projectAllCmdTestArtifacts(t, generation.GenerationID, 1)
	projectAllCmdTestBindPlanningContext(t, artifacts, generation, nil, registry, 1)
	receipt, err := domain.NewRAGFactReceipt(
		1,
		"县城票据复核",
		[]string{"票据", "复核"},
		"project_facts_exact_v1",
		strings.Repeat("b", 64),
		[]domain.RAGFactReceiptHit{{
			Rank:          1,
			ChunkID:       "chunk-material-1",
			ContentSHA256: strings.Repeat("a", 64),
			SourcePath:    "meta/writing_assets.json",
			SourceKind:    "project_fact",
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	artifacts.RAGFactReceipt = &receipt
	artifacts.Plan.CausalSimulation.ContextSources = append(
		artifacts.Plan.CausalSimulation.ContextSources,
		receipt.SourceToken(),
	)
	artifacts.Plan.CausalSimulation.ExternalRefs = append(
		artifacts.Plan.CausalSimulation.ExternalRefs,
		domain.ExternalReferencePlan{
			QueryOrNeed:        "票据如何形成可复核动作",
			SourceType:         "RAG",
			SourceRefs:         []string{receipt.Hits[0].Ref},
			UsableDetails:      []string{"票据由当事人当面核对"},
			TransformationRule: "转成角色逐项核对金额与时间的动作",
			DoNotUse:           []string{"不复制召回原句"},
		},
	)
	bundle, _, err := buildPipelineProjectedChapterBundle(
		generation,
		outline,
		genesis,
		generation.BaseStateRoot,
		artifacts,
		registry,
	)
	if err != nil {
		t.Fatalf("material RAG hit made formal bundle impossible: %v", err)
	}
	wantReceiptDigest, err := domain.RAGFactReceiptDigestV2(receipt)
	if err != nil {
		t.Fatal(err)
	}
	foundIdentity := false
	foundTransformation := false
	for _, binding := range bundle.SourceBindings {
		if binding.Kind == "rag_fact_receipt" &&
			binding.SourceDigest == wantReceiptDigest &&
			slices.Contains(binding.ExactReferences, receipt.SourceToken()) {
			foundIdentity = true
		}
		if slices.Contains(binding.ExactReferences, receipt.Hits[0].Ref) &&
			len(binding.UsableFacts) > 0 {
			foundTransformation = true
		}
	}
	if !foundIdentity || !foundTransformation {
		t.Fatalf("material receipt identity/transformation bindings incomplete: %+v", bundle.SourceBindings)
	}
}

func TestBuildPipelineProjectedChapterBundleRejectsLateGenerationRewrite(t *testing.T) {
	generation, registry := projectAllCmdTestGenerationAndRegistry(t, 1)
	genesis, err := domain.DeriveProjectedChainGenesisV2(generation)
	if err != nil {
		t.Fatal(err)
	}
	artifacts, outline := projectAllCmdTestArtifacts(t, generation.GenerationID, 1)
	projectAllCmdTestBindPlanningContext(t, artifacts, generation, nil, registry, 1)
	artifacts.WorldSimulation.GenerationID = "simulation-seed-from-progress"

	_, _, err = buildPipelineProjectedChapterBundle(
		generation,
		outline,
		genesis,
		generation.BaseStateRoot,
		artifacts,
		registry,
	)
	if err == nil || !strings.Contains(err.Error(), "generation identity mismatch") {
		t.Fatalf("bundle builder silently rewrote simulation generation after simulation_id was computed: %v", err)
	}
}

func TestBuildPipelineProjectedChapterBundleRejectsOpaqueRevealBudget(t *testing.T) {
	generation, registry := projectAllCmdTestGenerationAndRegistry(t, 1)
	genesis, err := domain.DeriveProjectedChainGenesisV2(generation)
	if err != nil {
		t.Fatal(err)
	}
	artifacts, outline := projectAllCmdTestArtifacts(t, generation.GenerationID, 1)
	artifacts.Plan.CausalSimulation.ReaderRetentionPlan.RevealBudget =
		[]string{"只控制信息揭示程度"}
	projectAllCmdTestBindPlanningContext(t, artifacts, generation, nil, registry, 1)
	_, _, err = buildPipelineProjectedChapterBundle(
		generation,
		outline,
		genesis,
		generation.BaseStateRoot,
		artifacts,
		registry,
	)
	if err == nil || !strings.Contains(err.Error(), "must make every clause an explicit") {
		t.Fatalf("opaque positive reveal budget entered sealed generation: %v", err)
	}
}

func TestBuildPipelineProjectedChapterBundleRejectsShortRevealBudgetProbe(t *testing.T) {
	generation, registry := projectAllCmdTestGenerationAndRegistry(t, 1)
	genesis, err := domain.DeriveProjectedChainGenesisV2(generation)
	if err != nil {
		t.Fatal(err)
	}
	artifacts, outline := projectAllCmdTestArtifacts(t, generation.GenerationID, 1)
	artifacts.Plan.CausalSimulation.ReaderRetentionPlan.RevealBudget = []string{"不解释"}
	projectAllCmdTestBindPlanningContext(t, artifacts, generation, nil, registry, 1)
	_, _, err = buildPipelineProjectedChapterBundle(
		generation,
		outline,
		genesis,
		generation.BaseStateRoot,
		artifacts,
		registry,
	)
	if err == nil || !strings.Contains(err.Error(), "mechanically locatable forbidden fact") {
		t.Fatalf("empty negative reveal probe entered sealed generation: %v", err)
	}
}

func TestBuildPipelineProjectedChapterBundleMaintainsThreeChapterStateChain(t *testing.T) {
	generation, registry := projectAllCmdTestGenerationAndRegistry(t, 3)
	genesis, err := domain.DeriveProjectedChainGenesisV2(generation)
	if err != nil {
		t.Fatal(err)
	}
	previous := genesis
	preState := generation.BaseStateRoot
	bundles := make([]domain.ProjectedChapterBundle, 0, 3)
	for chapter := 1; chapter <= 3; chapter++ {
		artifacts, outline := projectAllCmdTestArtifacts(t, generation.GenerationID, chapter)
		projectAllCmdTestBindPlanningContext(t, artifacts, generation, bundles, registry, chapter)
		bundle, nextRegistry, err := buildPipelineProjectedChapterBundle(
			generation,
			outline,
			previous,
			preState,
			artifacts,
			registry,
		)
		if err != nil {
			t.Fatalf("build chapter %d: %v", chapter, err)
		}
		if chapter > 1 {
			prior := bundles[len(bundles)-1]
			if bundle.PreviousBundleDigest != prior.BundleDigest ||
				bundle.ProjectedPreStateRoot != prior.ProjectedPostStateRoot {
				t.Fatalf("chapter %d does not consume chapter %d exact projected roots", chapter, chapter-1)
			}
		}
		bundles = append(bundles, bundle)
		registry = nextRegistry
		previous = bundle.BundleDigest
		preState = bundle.ProjectedPostStateRoot
	}
	registry.RegistryRoot, err = domain.ComputeObligationRegistryV2Root(registry)
	if err != nil {
		t.Fatal(err)
	}
	generation.Status = domain.PlanningGenerationSealedV2
	generation.ProjectedChapterCount = len(bundles)
	generation.ChainHeadRoot = bundles[0].BundleDigest
	generation.ChainTailRoot = bundles[len(bundles)-1].BundleDigest
	generation.ObligationRegistryRoot = registry.RegistryRoot
	generation.SealedAt = "2026-07-17T00:00:00Z"
	generation.GenerationDigest, err = domain.ComputePlanningGenerationV2Digest(generation)
	if err != nil {
		t.Fatal(err)
	}
	if err := domain.ValidateProjectedChapterBundleChain(generation, bundles, registry); err != nil {
		t.Fatalf("three-chapter builder chain rejected: %v", err)
	}

	broken := append([]domain.ProjectedChapterBundle(nil), bundles...)
	broken[1].ProjectedPreStateRoot = projectAllCmdTestDigest("wrong-pre-state")
	broken[1].ProjectedPostStateRoot, err = domain.DeriveProjectedPostStateRootV2(
		broken[1].ProjectedPreStateRoot,
		broken[1].ProjectedDelta,
	)
	if err != nil {
		t.Fatal(err)
	}
	broken[1].RenderContext, err = augmentPipelineProjectAllRenderContext(
		broken[1].RenderContext,
		broken[1],
	)
	if err != nil {
		t.Fatal(err)
	}
	broken[1].RenderContextSHA256, err = domain.ComputePlanningV2JSONDigest(
		broken[1].RenderContext,
	)
	if err != nil {
		t.Fatal(err)
	}
	broken[1].BundleDigest, err = domain.ComputeProjectedChapterBundleDigest(broken[1])
	if err != nil {
		t.Fatal(err)
	}
	if err := domain.ValidateProjectedChapterBundleChain(generation, broken, registry); err == nil ||
		!strings.Contains(err.Error(), "pre-state") {
		t.Fatalf("individually valid but discontinuous projected state chain was accepted: %v", err)
	}
}

func TestPipelineProjectAllRejectsFutureObligationBeyondTerminalChapter(t *testing.T) {
	generation, registry := projectAllCmdTestGenerationAndRegistry(t, 1)
	artifacts, _ := projectAllCmdTestArtifacts(t, generation.GenerationID, 1)
	artifacts.WorldSimulation.CharacterDecisions[0].ButterflyEffects = []domain.DecisionButterflyEffect{{
		Effect:            "终章之后才抵达的场外后果",
		Targets:           []string{"主角"},
		TransmissionPath:  "书外消息",
		ArrivalChapter:    2,
		Visibility:        "hidden",
		ProtagonistImpact: "书内无法兑现",
	}}
	if _, _, err := pipelineProjectAllCreateObligations(
		generation,
		*artifacts.WorldSimulation,
		*artifacts.Plan,
		registry,
	); err == nil || !strings.Contains(err.Error(), "outside terminal chapter") {
		t.Fatalf("terminal chapter silently clamped an impossible future obligation: %v", err)
	}
}

func TestBuildPipelineProjectedChapterBundleSignsNextObligationRegistry(t *testing.T) {
	generation, registry := projectAllCmdTestGenerationAndRegistry(t, 2)
	genesis, err := domain.DeriveProjectedChainGenesisV2(generation)
	if err != nil {
		t.Fatal(err)
	}
	artifacts, outline := projectAllCmdTestArtifacts(t, generation.GenerationID, 1)
	artifacts.WorldSimulation.CharacterDecisions[0].ButterflyEffects = []domain.DecisionButterflyEffect{{
		Effect:            "第二章必须回收的可见后果",
		Targets:           []string{"主角"},
		TransmissionPath:  "下一次当面核验",
		ArrivalChapter:    2,
		Visibility:        "visible",
		ProtagonistImpact: "主角必须作出新的选择",
	}}
	projectAllCmdTestBindPlanningContext(t, artifacts, generation, nil, registry, 1)
	_, nextRegistry, err := buildPipelineProjectedChapterBundle(
		generation,
		outline,
		genesis,
		generation.BaseStateRoot,
		artifacts,
		registry,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(nextRegistry.Obligations) != 1 {
		t.Fatalf("next registry obligations=%d, want 1", len(nextRegistry.Obligations))
	}
	if nextRegistry.RegistryRoot == registry.RegistryRoot {
		t.Fatal("next registry retained the predecessor root after adding an obligation")
	}
	if err := domain.ValidateObligationRegistryV2(nextRegistry); err != nil {
		t.Fatalf("builder returned unsigned next registry: %v", err)
	}
}

func TestProjectAllSealPromoteOutcomeRecoveryAndCycleReset(t *testing.T) {
	opts, st, identity := projectAllCmdTestInstallThreeChapterCLIProjection(t)

	projectAllEvidence, err := verifyPipelineProjectAllStage(
		st.Dir(),
		domain.PipelineStageEvidence{Stage: "project-all"},
	)
	if err != nil {
		t.Fatalf("verify project-all: %v", err)
	}
	if !strings.Contains(projectAllEvidence.Message, "3/3 formal projected bundles") {
		t.Fatalf("project-all evidence does not prove complete formal chain: %+v", projectAllEvidence)
	}
	if err := pipelineSeal(opts, pipelineFlags{}); err != nil {
		t.Fatalf("seal pure-file projection: %v", err)
	}
	sealEvidence, err := verifyPipelineSealStage(
		st.Dir(),
		domain.PipelineStageEvidence{Stage: "seal"},
	)
	if err != nil {
		t.Fatalf("verify seal: %v", err)
	}
	if slices.Contains(sealEvidence.Artifacts, "meta/planning/v2/realization_cursor.json") {
		t.Fatal("seal evidence hashed the mutable realization cursor; chapter recovery would stale the immutable arc stage")
	}
	if err := pipelinePromote(opts, pipelineFlags{Start: 1, End: 1}); err != nil {
		t.Fatalf("promote exact chapter-one bundle without Planner: %v", err)
	}
	if _, err := verifyPipelinePromoteStage(
		st.Dir(),
		domain.PipelineStageEvidence{Stage: "promote"},
	); err != nil {
		t.Fatalf("verify promote: %v", err)
	}

	projected := st.ProjectedV2()
	cursor, err := projected.LoadRealizationCursor()
	if err != nil || cursor == nil {
		t.Fatalf("load promoted cursor: cursor=%+v err=%v", cursor, err)
	}
	if cursor.ActiveGenerationID != identity.Generation.GenerationID ||
		cursor.NextPromoteChapter != 1 ||
		cursor.ActivePromotedChapter != 1 ||
		cursor.LastAcceptedChapter != 0 {
		t.Fatalf("promotion advanced the wrong control fields: %+v", cursor)
	}
	frozen, _, err := loadAndVerifyPipelineFrozenPlan(st.Dir())
	if err != nil {
		t.Fatal(err)
	}
	binding, err := validatePipelineSealedRenderBinding(st, frozen, false)
	if err != nil {
		t.Fatalf("sealed render binding before outcome: %v", err)
	}
	chapterBody := "第一章\n\n主角在截止前完成小额验证，并把可复核票据收进衣袋。\n"
	chapterBody = string([]rune(chapterBody + strings.Repeat("县", 2100))[:2100])
	projectAllCmdTestWriteFile(t, filepath.Join(st.Dir(), "chapters", "01.md"), chapterBody)
	bodySHA, err := pipelineRequiredFileSHA(st.Dir(), "chapters/01.md")
	if err != nil {
		t.Fatal(err)
	}
	commit, err := st.Checkpoints.Append(
		domain.ChapterScope(1),
		"commit",
		"chapters/01.md",
		bodySHA,
	)
	if err != nil {
		t.Fatal(err)
	}
	actualMatch := &pipelineSealedActualDeltaMatch{
		ActualDelta:          binding.Bundle.ProjectedDelta,
		ProjectionMatch:      true,
		Complete:             true,
		ObligationsSatisfied: append([]string(nil), binding.Bundle.ObligationsConsumed...),
	}
	actualCanonRoot := projectAllCmdTestDigest("accepted chapter one canon")
	outcome, err := acceptPipelineSealedRenderOutcome(
		st,
		binding,
		commit,
		bodySHA,
		actualCanonRoot,
		actualMatch,
	)
	if err != nil {
		t.Fatalf("accept matching sealed render outcome: %v", err)
	}
	if outcome.ActualCanonRoot != actualCanonRoot {
		t.Fatalf("accepted outcome lost actual canon root: %+v", outcome)
	}
	mustWriteCurrentReviewArtifacts(t, st.Dir(), 1)
	if _, err := savePipelineChapterAcceptance(
		st.Dir(),
		st,
		&binding.Generation,
		1,
		bodySHA,
		outcome,
	); err != nil {
		t.Fatalf("save exact-body chapter acceptance: %v", err)
	}
	acceptedCursor, err := projected.LoadRealizationCursor()
	if err != nil || acceptedCursor == nil {
		t.Fatalf("load accepted cursor: cursor=%+v err=%v", acceptedCursor, err)
	}
	if acceptedCursor.ActivePromotedChapter != 0 ||
		acceptedCursor.LastAcceptedChapter != 1 ||
		acceptedCursor.NextPromoteChapter != 2 ||
		acceptedCursor.LastOutcomeReceiptDigest != outcome.ReceiptDigest {
		t.Fatalf("matching outcome did not advance exactly one realization step: %+v", acceptedCursor)
	}

	crashState := &domain.PipelineState{
		Stages: []string{"preplan", "project-all", "seal", "promote", "render"},
	}
	for _, stage := range []string{"preplan", "project-all", "seal", "promote"} {
		crashState.MarkDone(stage, domain.PipelineStageEvidence{Stage: stage, Status: "complete"})
	}
	if pending, err := splitPipelineRenderRecoveryPending(st.Dir(), crashState); err != nil || !pending {
		t.Fatalf("accepted outcome without pipeline render receipt must enter crash recovery: pending=%v err=%v", pending, err)
	}
	recoveredBinding, err := validatePipelineSealedRenderBinding(st, frozen, true)
	if err != nil {
		t.Fatalf("recover sealed binding from durable outcome receipt: %v", err)
	}
	if recoveredBinding.Outcome == nil ||
		recoveredBinding.Outcome.ReceiptDigest != outcome.ReceiptDigest {
		t.Fatalf("recovery did not load exact durable outcome: %+v", recoveredBinding.Outcome)
	}
	replayed, err := acceptPipelineSealedRenderOutcome(
		st,
		recoveredBinding,
		commit,
		bodySHA,
		actualCanonRoot,
		actualMatch,
	)
	if err != nil {
		t.Fatalf("replay durable outcome after crash: %v", err)
	}
	afterReplay, err := projected.LoadRealizationCursor()
	if err != nil {
		t.Fatal(err)
	}
	if replayed.ReceiptDigest != outcome.ReceiptDigest ||
		afterReplay.CursorDigest != acceptedCursor.CursorDigest {
		t.Fatalf("outcome recovery was not idempotent: replay=%+v cursor=%+v", replayed, afterReplay)
	}

	if _, err := writePipelinePlanningJSON(
		filepath.Join(st.Dir(), pipelineRenderReceiptPath),
		pipelineRenderReceipt{
			Version:                pipelinePlanningSchema,
			Chapter:                1,
			PlanningGenerationID:   identity.Generation.GenerationID,
			OutcomeReceiptDigest:   outcome.ReceiptDigest,
			ProjectedBundleDigest:  binding.Bundle.BundleDigest,
			PromotionReceiptDigest: binding.Promotion.ReceiptDigest,
		},
	); err != nil {
		t.Fatal(err)
	}
	crashState.MarkDone("render", domain.PipelineStageEvidence{Stage: "render", Status: "complete"})
	acceptances, err := st.ArcCycle().ListChapterAcceptanceReceipts(identity.Generation.GenerationID)
	if err != nil || len(acceptances) != 1 {
		t.Fatalf("load exact-body acceptance fixture: receipts=%+v err=%v", acceptances, err)
	}
	acceptanceDir := filepath.Join(
		st.Dir(),
		"meta", "planning", "v3", "arc_cycle", "acceptances",
		identity.Generation.GenerationID,
	)
	if err := os.RemoveAll(acceptanceDir); err != nil {
		t.Fatal(err)
	}
	if reset, err := resetCompletedSplitPipelineCycle(st.Dir(), crashState); err == nil || reset ||
		!strings.Contains(err.Error(), "逐章审核回执") {
		t.Fatalf("sealed cycle advanced without chapter acceptance: reset=%v err=%v", reset, err)
	}
	if _, err := st.ArcCycle().SaveChapterAcceptanceReceipt(acceptances[0]); err != nil {
		t.Fatalf("restore exact-body acceptance fixture: %v", err)
	}
	renderOnlyState := &domain.PipelineState{Stages: []string{"render"}}
	renderOnlyState.MarkDone("render", domain.PipelineStageEvidence{Stage: "render", Status: "complete"})
	if reset, err := resetCompletedSealedPipelineCycle(st.Dir(), renderOnlyState); err != nil || reset {
		t.Fatalf("non-terminal render-only invocation advanced sealed cycle: reset=%v err=%v", reset, err)
	}
	if !renderOnlyState.Done("render") {
		t.Fatal("non-terminal render-only invocation cleared its completed render stage")
	}
	if completions, err := st.ArcCycle().ListArcCompletionReceipts(identity.Generation.GenerationID); err != nil || len(completions) != 0 {
		t.Fatalf("non-terminal render-only invocation published arc completion: receipts=%+v err=%v", completions, err)
	}
	reset, err := resetCompletedSplitPipelineCycle(st.Dir(), crashState)
	if err != nil || !reset {
		t.Fatalf("sealed cycle did not advance after durable render receipt: reset=%v err=%v", reset, err)
	}
	for _, preserved := range []string{"preplan", "project-all", "seal"} {
		if !crashState.Done(preserved) {
			t.Fatalf("sealed cycle reset cleared immutable stage %s: %+v", preserved, crashState.Completed)
		}
	}
	for _, cleared := range []string{"promote", "render"} {
		if crashState.Done(cleared) {
			t.Fatalf("sealed cycle reset retained chapter-scoped stage %s: %+v", cleared, crashState.Completed)
		}
		if crashState.Evidence[cleared].Status != "next_cycle" {
			t.Fatalf("cleared stage %s lost next-cycle evidence: %+v", cleared, crashState.Evidence[cleared])
		}
	}
}

func TestProjectAllV2ThreeChapterSealBeforeSequentialRealization(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	projected := st.ProjectedV2()
	generation, registry := projectAllCmdTestGenerationAndRegistry(t, 3)
	source := domain.PlanningSourceSnapshotV2{
		Version:                domain.PlanningSourceSnapshotV2Version,
		GenerationID:           generation.GenerationID,
		BaseCanonChapter:       generation.BaseCanonChapter,
		BaseCanonRoot:          generation.BaseCanonRoot,
		BaseStateRoot:          generation.BaseStateRoot,
		StableOutlineRoot:      generation.StableOutlineRoot,
		PlanningDependencyRoot: generation.PlanningDependencyRoot,
		RandomSeedContractRoot: generation.RandomSeedContractRoot,
		FoundationSnapshotRoot: projectAllCmdTestDigest("three-chapter-foundation-snapshot"),
		RAGSnapshotRoot:        projectAllCmdTestDigest("three-chapter-rag-snapshot"),
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

	currentGeneration := generation
	currentRegistry := registry
	previous, preState, err := pipelineProjectAllTail(currentGeneration, nil)
	if err != nil {
		t.Fatal(err)
	}
	bundles := make([]domain.ProjectedChapterBundle, 0, 3)
	for chapter := 1; chapter <= 3; chapter++ {
		artifacts, outline := projectAllCmdTestArtifacts(
			t,
			generation.GenerationID,
			chapter,
		)
		artifacts.RenderContext, err = json.Marshal(map[string]any{
			"_context_profile": "draft",
			"draft_packet": map[string]any{
				"chapter": chapter,
				"policy":  "只渲染可见行动，不暴露幕后状态",
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		if chapter == 1 {
			projectAllCmdTestAttachMaterialRAG(t, artifacts)
			artifacts.WorldSimulation.ProtagonistProjection.HiddenPressures = append(
				artifacts.WorldSimulation.ProtagonistProjection.HiddenPressures,
				"账房老周已经扣住备用收据，但主角尚未收到消息",
			)
			artifacts.WorldSimulation.CharacterDecisions = append(
				artifacts.WorldSimulation.CharacterDecisions,
				domain.CharacterWorldDecision{
					Character:         "账房老周",
					Time:              "同日上午",
					Location:          "旧街账房后屋",
					CurrentGoal:       "先核对账目再决定是否交出备用收据",
					Pressure:          "账面差额必须在午前解释",
					KnowledgeBoundary: "知道备用收据被扣住，不知道主角的完整验证计划",
					AvailableOptions:  []string{"暂扣收据", "立即送出"},
					Decision:          "暂扣收据",
					DecisionReason:    "账面差额尚未核清",
					Action:            "把备用收据压在账册下面继续核账",
					ActionDuration:    "两小时",
					CompletionState:   "completed_offscreen",
					ImmediateResult:   "主角暂时拿不到第二份核对材料",
					StateAfter:        "备用收据仍被扣住且消息尚未外传",
					ButterflyEffects: []domain.DecisionButterflyEffect{
						{
							Effect:            "第二章账房仍未主动送出备用收据",
							Targets:           []string{"主角"},
							TransmissionPath:  "场外账房继续扣留",
							ArrivalChapter:    2,
							Visibility:        "hidden",
							ProtagonistImpact: "主角只能从票据缺口感到阻力",
						},
						{
							Effect:            "第三章备用收据必须通过可见行动交到主角手里",
							Targets:           []string{"主角", "账房老周"},
							TransmissionPath:  "账房老周当面交付",
							ArrivalChapter:    3,
							Visibility:        "visible",
							ProtagonistImpact: "主角获得可复核的第二份证据",
						},
					},
				},
			)
		}
		projectAllCmdTestBindPlanningContext(
			t,
			artifacts,
			currentGeneration,
			bundles,
			currentRegistry,
			chapter,
		)
		bundle, nextRegistry, err := buildPipelineProjectedChapterBundle(
			currentGeneration,
			outline,
			previous,
			preState,
			artifacts,
			currentRegistry,
		)
		if err != nil {
			t.Fatalf("build formal chapter %d: %v", chapter, err)
		}
		nextRegistry.RegistryRoot, err = domain.ComputeObligationRegistryV2Root(nextRegistry)
		if err != nil {
			t.Fatalf("bind obligation registry after chapter %d: %v", chapter, err)
		}
		cursor, err = projected.ProjectChapterAndAdvance(
			currentGeneration.GenerationDigest,
			currentGeneration.ChainTailRoot,
			currentGeneration.ObligationRegistryRoot,
			*cursor,
			bundle,
			nextRegistry,
		)
		if err != nil {
			t.Fatalf("persist formal chapter %d: %v", chapter, err)
		}
		persisted, err := projected.LoadProjectedChapterBundles(generation.GenerationID)
		if err != nil {
			t.Fatalf("reload formal prefix after chapter %d: %v", chapter, err)
		}
		if len(persisted) != chapter ||
			persisted[len(persisted)-1].BundleDigest != bundle.BundleDigest {
			t.Fatalf("formal prefix was not durable after chapter %d: %+v", chapter, persisted)
		}
		bundles = append(bundles, bundle)
		currentRegistry = nextRegistry
		loaded, err := projected.LoadBuildingGeneration(generation.GenerationID)
		if err != nil || loaded == nil {
			t.Fatalf("reload building generation after chapter %d: %+v err=%v", chapter, loaded, err)
		}
		currentGeneration = *loaded
		previous = bundle.BundleDigest
		preState = bundle.ProjectedPostStateRoot

		if chapter == 2 {
			if _, err := projected.SealGeneration(generation.GenerationID); err == nil {
				t.Fatal("two-of-three projected chapters were allowed to seal")
			}
			if _, _, err := projected.ActivateSealedGeneration(
				generation.GenerationID,
				nil,
			); err == nil {
				t.Fatal("building generation was activated before all chapters sealed")
			}
			earlyPromotion := projectAllCmdTestPromotion(t, bundles[0])
			if _, err := projected.Promote(
				domain.RealizationCursorV2{},
				earlyPromotion,
			); err == nil {
				t.Fatal("chapter prose promotion was allowed before all chapters sealed")
			}
			if realization, err := projected.LoadRealizationCursor(); err != nil ||
				realization != nil {
				t.Fatalf("pre-seal path created a realization cursor: %+v err=%v", realization, err)
			}
			projectAllCmdTestRequireNoChapterBodies(t, st.Dir(), 3)
		}
	}

	projectAllCmdTestAssertIntegratedProjection(t, bundles, currentRegistry)
	beforeSealDigests := projectAllCmdTestBundleDigests(bundles)
	seal, err := projected.SealGeneration(generation.GenerationID)
	if err != nil {
		t.Fatalf("seal complete three-chapter generation: %v", err)
	}
	if strings.TrimSpace(seal.ReceiptDigest) == "" {
		t.Fatal("complete seal did not return a receipt digest")
	}
	active, realization, err := projected.ActivateSealedGeneration(
		generation.GenerationID,
		nil,
	)
	if err != nil {
		t.Fatalf("activate complete sealed generation: %v", err)
	}
	if active.GenerationID != generation.GenerationID ||
		realization.NextPromoteChapter != 1 {
		t.Fatalf("sealed activation started from the wrong chapter: active=%+v cursor=%+v", active, realization)
	}
	if building, err := projected.LoadBuildingGeneration(generation.GenerationID); err != nil {
		t.Fatal(err)
	} else if building != nil {
		t.Fatalf("building generation survived successful seal: %+v", building)
	}
	if err := projected.SaveProjectedChapterBundle(bundles[0]); err == nil {
		t.Fatal("sealed bundle accepted a replanning write")
	}

	for chapter := 1; chapter <= 3; chapter++ {
		cursorBefore, err := projected.LoadRealizationCursor()
		if err != nil || cursorBefore == nil {
			t.Fatalf("load realization cursor before chapter %d: %+v err=%v", chapter, cursorBefore, err)
		}
		if cursorBefore.NextPromoteChapter != chapter ||
			cursorBefore.ActivePromotedChapter != 0 {
			t.Fatalf("realization order drifted before chapter %d: %+v", chapter, cursorBefore)
		}
		projectAllCmdTestRequireNoFutureChapterBodies(t, st.Dir(), chapter, 3)
		if chapter < 3 {
			jump := projectAllCmdTestPromotion(t, bundles[chapter])
			if _, err := projected.Promote(*cursorBefore, jump); err == nil {
				t.Fatalf("realization skipped chapter %d and promoted chapter %d", chapter, chapter+1)
			}
		}

		promotion := projectAllCmdTestPromotion(t, bundles[chapter-1])
		if _, err := projected.Promote(*cursorBefore, promotion); err != nil {
			t.Fatalf("promote chapter %d: %v", chapter, err)
		}
		projectAllCmdTestRequireNoFutureChapterBodies(t, st.Dir(), chapter+1, 3)
		if chapter < 3 {
			promotedCursor, err := projected.LoadRealizationCursor()
			if err != nil || promotedCursor == nil {
				t.Fatalf("load active promotion for chapter %d: %+v err=%v", chapter, promotedCursor, err)
			}
			jump := projectAllCmdTestPromotion(t, bundles[chapter])
			if _, err := projected.Promote(*promotedCursor, jump); err == nil {
				t.Fatalf("chapter %d+1 promoted before chapter %d outcome closed", chapter, chapter)
			}
		}

		body := fmt.Sprintf("第%d章正文：角色只通过现场行动推进已封存的因果结果。", chapter)
		projectAllCmdTestWriteFile(
			t,
			filepath.Join(st.Dir(), "chapters", fmt.Sprintf("%02d.md", chapter)),
			body,
		)
		promotedCursor, err := projected.LoadRealizationCursor()
		if err != nil || promotedCursor == nil {
			t.Fatalf("load promoted cursor for chapter %d outcome: %+v err=%v", chapter, promotedCursor, err)
		}
		outcome := projectAllCmdTestOutcome(
			t,
			bundles[chapter-1],
			promotion,
			chapter,
		)
		if _, err := projected.AcceptOutcome(*promotedCursor, outcome); err != nil {
			t.Fatalf("accept chapter %d outcome: %v", chapter, err)
		}
		after, err := projected.LoadRealizationCursor()
		if err != nil || after == nil {
			t.Fatalf("load cursor after chapter %d outcome: %+v err=%v", chapter, after, err)
		}
		if after.LastAcceptedChapter != chapter ||
			after.NextPromoteChapter != chapter+1 ||
			after.ActivePromotedChapter != 0 {
			t.Fatalf("chapter %d outcome did not advance exactly one step: %+v", chapter, after)
		}
		sealedBundles, err := projected.LoadProjectedChapterBundles(generation.GenerationID)
		if err != nil {
			t.Fatalf("reload sealed bundles after chapter %d: %v", chapter, err)
		}
		if got := projectAllCmdTestBundleDigests(sealedBundles); !reflect.DeepEqual(got, beforeSealDigests) {
			t.Fatalf("chapter %d realization replanned sealed suffix: got=%v want=%v", chapter, got, beforeSealDigests)
		}
	}

	for chapter := 1; chapter <= 3; chapter++ {
		if _, err := os.Stat(filepath.Join(
			st.Dir(),
			"chapters",
			fmt.Sprintf("%02d.md", chapter),
		)); err != nil {
			t.Fatalf("sequential realization did not publish chapter %d: %v", chapter, err)
		}
	}
}

func projectAllCmdTestAttachMaterialRAG(
	t *testing.T,
	artifacts *agents.ProjectedChapterArtifacts,
) {
	t.Helper()
	receipt, err := domain.NewRAGFactReceipt(
		1,
		"青山县票据复核动作",
		[]string{"票据", "复核"},
		"project_facts_exact_v1",
		strings.Repeat("b", 64),
		[]domain.RAGFactReceiptHit{{
			Rank:          1,
			ChunkID:       "chunk-three-chapter-material",
			ContentSHA256: strings.Repeat("a", 64),
			SourcePath:    "meta/writing_assets.json",
			SourceKind:    "project_fact",
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	artifacts.RAGFactReceipt = &receipt
	artifacts.Plan.CausalSimulation.ContextSources = append(
		artifacts.Plan.CausalSimulation.ContextSources,
		receipt.SourceToken(),
	)
	artifacts.Plan.CausalSimulation.ExternalRefs = append(
		artifacts.Plan.CausalSimulation.ExternalRefs,
		domain.ExternalReferencePlan{
			QueryOrNeed:        "票据怎样形成可复核的现场动作",
			SourceType:         "RAG",
			SourceRefs:         []string{receipt.Hits[0].Ref},
			UsableDetails:      []string{"当事人逐项核对票据金额与时间"},
			TransformationRule: "转化成角色现场逐项核对的动作",
			DoNotUse:           []string{"不得复制召回原句"},
		},
	)
}

func projectAllCmdTestAssertIntegratedProjection(
	t *testing.T,
	bundles []domain.ProjectedChapterBundle,
	registry domain.ObligationRegistryV2,
) {
	t.Helper()
	if len(bundles) != 3 {
		t.Fatalf("integrated fixture has %d bundles, want 3", len(bundles))
	}
	if bundles[0].RAGFactReceipt == nil ||
		len(bundles[0].RAGFactReceipt.Hits) == 0 {
		t.Fatal("chapter one lost its material RAG hit")
	}
	ragDigest, err := domain.RAGFactReceiptDigestV2(*bundles[0].RAGFactReceipt)
	if err != nil {
		t.Fatal(err)
	}
	foundRAGBinding := false
	for _, binding := range bundles[0].SourceBindings {
		if binding.Kind == "rag_fact_receipt" &&
			binding.SourceDigest == ragDigest {
			foundRAGBinding = true
			break
		}
	}
	if !foundRAGBinding {
		t.Fatalf("chapter one did not bind material RAG receipt: %+v", bundles[0].SourceBindings)
	}

	hiddenState := false
	for _, mutation := range bundles[0].ProjectedDelta.CharacterState {
		if mutation.Subject == "账房老周" &&
			strings.Contains(mutation.After, "备用收据仍被扣住") {
			hiddenState = true
			break
		}
	}
	if !hiddenState {
		t.Fatalf("offscreen character state was not projected: %+v", bundles[0].ProjectedDelta.CharacterState)
	}
	renderText := string(bundles[0].RenderContext)
	for _, hidden := range []string{"账房老周", "备用收据仍被扣住"} {
		if strings.Contains(renderText, hidden) {
			t.Fatalf("server-only hidden state leaked into prose render context: %q", hidden)
		}
	}
	var renderPayload map[string]json.RawMessage
	if err := json.Unmarshal(bundles[0].RenderContext, &renderPayload); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{
		"formal_world_simulation",
		"pov_plan",
		"projected_delta",
		"obligation_registry",
		"source_bindings",
	} {
		if _, exists := renderPayload[key]; exists {
			t.Fatalf("server-only field %q leaked into prose render context", key)
		}
	}

	var softID, hardID string
	for _, obligation := range registry.Obligations {
		switch obligation.Hardness {
		case domain.ObligationSoftV2:
			softID = obligation.ID
			if obligation.State != domain.ObligationSatisfiedV2 ||
				!reflect.DeepEqual(obligation.ConsumerChapters, []int{2}) {
				t.Fatalf("soft cross-chapter obligation was not satisfied in chapter two: %+v", obligation)
			}
		case domain.ObligationHardV2:
			hardID = obligation.ID
			if obligation.State != domain.ObligationSatisfiedV2 ||
				!reflect.DeepEqual(obligation.ConsumerChapters, []int{3}) {
				t.Fatalf("hard cross-chapter obligation was not satisfied in chapter three: %+v", obligation)
			}
		}
	}
	if softID == "" || hardID == "" {
		t.Fatalf("expected one hard and one soft obligation, got %+v", registry.Obligations)
	}
	if !slices.Contains(bundles[0].ObligationsCreated, softID) ||
		!slices.Contains(bundles[0].ObligationsCreated, hardID) ||
		!slices.Contains(bundles[1].ObligationsConsumed, softID) ||
		!slices.Contains(bundles[1].ObligationsCarried, hardID) ||
		!slices.Contains(bundles[2].ObligationsConsumed, hardID) {
		t.Fatalf(
			"obligation flow mismatch: chapter1=%+v chapter2=%+v chapter3=%+v",
			bundles[0].ProjectedDelta.Obligations,
			bundles[1].ProjectedDelta.Obligations,
			bundles[2].ProjectedDelta.Obligations,
		)
	}
}

func projectAllCmdTestPromotion(
	t *testing.T,
	bundle domain.ProjectedChapterBundle,
) domain.PromotionReceiptV2 {
	t.Helper()
	planDigest, err := domain.ComputeChapterPlanV2Digest(bundle.ChapterPlan)
	if err != nil {
		t.Fatal(err)
	}
	receipt := domain.PromotionReceiptV2{
		Version:               domain.PromotionReceiptV2Version,
		GenerationID:          bundle.GenerationID,
		Chapter:               bundle.Chapter,
		BundleDigest:          bundle.BundleDigest,
		ActualPreStateRoot:    bundle.ProjectedPreStateRoot,
		ProjectedPreStateRoot: bundle.ProjectedPreStateRoot,
		RenderDependencyRoot:  projectAllCmdTestDigest("three-chapter-render-dependencies"),
		FrozenPlanDigest:      planDigest,
		Mode:                  domain.ExactPromotionModeV2,
		PromotedAt:            "2026-07-17T00:00:00Z",
	}
	receipt.ReceiptDigest, err = domain.ComputePromotionReceiptV2Digest(receipt)
	if err != nil {
		t.Fatal(err)
	}
	return receipt
}

func projectAllCmdTestOutcome(
	t *testing.T,
	bundle domain.ProjectedChapterBundle,
	promotion domain.PromotionReceiptV2,
	chapter int,
) domain.ActualOutcomeReceiptV2 {
	t.Helper()
	receipt := domain.ActualOutcomeReceiptV2{
		Version:                     domain.ActualOutcomeReceiptV2Version,
		GenerationID:                bundle.GenerationID,
		Chapter:                     bundle.Chapter,
		PromotionReceiptDigest:      promotion.ReceiptDigest,
		ChapterBodySHA256:           projectAllCmdTestDigest(fmt.Sprintf("chapter-%d-body", chapter)),
		CommitCheckpointSeq:         int64(chapter),
		ActualDelta:                 bundle.ProjectedDelta,
		ActualPreStateRoot:          bundle.ProjectedPreStateRoot,
		ActualPostStateRoot:         bundle.ProjectedPostStateRoot,
		ActualCanonRoot:             projectAllCmdTestDigest(fmt.Sprintf("chapter-%d-canon", chapter)),
		ProjectedPostStateRoot:      bundle.ProjectedPostStateRoot,
		ObligationsSatisfied:        append([]string(nil), bundle.ObligationsConsumed...),
		ObligationsCreatedUnplanned: []string{},
		ProjectionMatch:             true,
		AcceptedAt:                  "2026-07-17T00:00:00Z",
	}
	var err error
	receipt.ReceiptDigest, err = domain.ComputeActualOutcomeReceiptV2Digest(receipt)
	if err != nil {
		t.Fatal(err)
	}
	return receipt
}

func projectAllCmdTestRequireNoChapterBodies(t *testing.T, root string, count int) {
	t.Helper()
	projectAllCmdTestRequireNoFutureChapterBodies(t, root, 1, count)
}

func projectAllCmdTestRequireNoFutureChapterBodies(
	t *testing.T,
	root string,
	first int,
	last int,
) {
	t.Helper()
	for chapter := first; chapter <= last; chapter++ {
		path := filepath.Join(root, "chapters", fmt.Sprintf("%02d.md", chapter))
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("chapter %d body exists before its exact promotion: %v", chapter, err)
		}
	}
}

func projectAllCmdTestBundleDigests(
	bundles []domain.ProjectedChapterBundle,
) []string {
	out := make([]string, 0, len(bundles))
	for _, bundle := range bundles {
		out = append(out, bundle.BundleDigest)
	}
	return out
}

func projectAllCmdTestInstallCoarseManifests(
	t *testing.T,
	st *store.Store,
	outlines []domain.OutlineEntry,
	baseChapter int,
) {
	t.Helper()
	const generationID = "coarse-generation"
	const baseCanonRoot = "coarse-canon-root"
	fingerprint, err := domain.NewDependencyFingerprint(
		generationID,
		baseCanonRoot,
		[]domain.PlanningDependency{{
			Kind: "outline", ID: "layered_outline.json", SHA256: "outline-sha",
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	preState := baseCanonRoot
	manifests := make([]domain.StagedChapterPlanManifest, 0, len(outlines))
	for _, outline := range outlines {
		payload := pipelineProjectedChapterPayload{
			Version:         pipelinePlanningSchema,
			GenerationID:    generationID,
			Chapter:         outline.Chapter,
			Volume:          1,
			Arc:             2,
			Formal:          false,
			Authority:       "speculative",
			ProjectionLevel: pipelineProjectionCoarse,
			Outline:         outline,
			Notice:          "非正史粗章位",
		}
		planRel := filepath.ToSlash(filepath.Join(
			"meta", "planning", "generations", generationID, "chapters",
			projectAllCmdTestChapterFile(outline.Chapter),
		))
		planSHA, err := writePipelinePlanningJSON(
			filepath.Join(st.Dir(), filepath.FromSlash(planRel)),
			payload,
		)
		if err != nil {
			t.Fatal(err)
		}
		projectionRoot, err := domain.DeterministicPlanningHash(payload)
		if err != nil {
			t.Fatal(err)
		}
		postState, err := domain.DeriveProjectedStateRoot(
			outline.Chapter,
			generationID,
			baseCanonRoot,
			fingerprint.RootSHA256,
			preState,
			projectionRoot,
		)
		if err != nil {
			t.Fatal(err)
		}
		manifests = append(manifests, domain.StagedChapterPlanManifest{
			Version:               domain.PlanningStoreVersion,
			Chapter:               outline.Chapter,
			Volume:                1,
			GenerationID:          generationID,
			BaseCanonChapter:      baseChapter,
			BaseCanonRoot:         baseCanonRoot,
			DependencyFingerprint: fingerprint,
			Authority:             domain.PlanningAuthoritySpeculative,
			Realization:           domain.PlanningRealizationStaged,
			PlanPath:              planRel,
			PlanSHA256:            planSHA,
			ProjectedState: domain.ProjectedStateReceipt{
				Version:        domain.PlanningStoreVersion,
				Chapter:        outline.Chapter,
				GenerationID:   generationID,
				BaseCanonRoot:  baseCanonRoot,
				DependencyRoot: fingerprint.RootSHA256,
				Authority:      domain.PlanningAuthorityProjected,
				Realization:    domain.PlanningRealizationStaged,
				PreStateRoot:   preState,
				ProjectionRoot: projectionRoot,
				PostStateRoot:  postState,
			},
		})
		preState = postState
	}
	if err := st.Planning.ReplaceStagedChapterPlanManifests(manifests); err != nil {
		t.Fatal(err)
	}
}

func projectAllCmdTestInstallThreeChapterCLIProjection(
	t *testing.T,
) (cliOptions, *store.Store, pipelineProjectAllIdentity) {
	t.Helper()
	runRoot := t.TempDir()
	configPath := filepath.Join(runRoot, "config.json")
	projectAllCmdTestWriteFile(t, configPath, `{
  "provider": "ollama",
  "model": "project-all-test-model",
  "providers": {
    "ollama": {
      "type": "openai",
      "base_url": "http://127.0.0.1:11434/v1"
    }
  }
}
`)
	opts := cliOptions{ConfigPath: configPath, Dir: runRoot}
	cfg, promptBundle, err := loadCfgBundle(opts)
	if err != nil {
		t.Fatal(err)
	}
	st := store.NewStore(cfg.OutputDir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	defaultRules := rules.BuildSnapshot([]rules.Candidate{rules.SystemDefaults()})
	if err := st.UserRules.Save(&defaultRules); err != nil {
		t.Fatal(err)
	}
	outlines := []domain.OutlineEntry{
		{Chapter: 1, Title: "第一章", CoreEvent: "第一次验证规则", Hook: "票据留下疑点"},
		{Chapter: 2, Title: "第二章", CoreEvent: "疑点形成现实压力", Hook: "必须作第二次选择"},
		{Chapter: 3, Title: "第三章", CoreEvent: "选择改变人物位置", Hook: "阶段性结果落定"},
	}
	if err := st.Outline.SaveOutline(outlines); err != nil {
		t.Fatal(err)
	}
	if err := st.Outline.SaveLayeredOutline([]domain.VolumeOutline{{
		Index: 1,
		Title: "第一卷",
		Theme: "先验证再承担",
		Arcs: []domain.ArcOutline{{
			Index:    1,
			Title:    "验证弧",
			Goal:     "取得可复核证据",
			Chapters: outlines,
		}},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := st.Outline.SaveCompass(domain.StoryCompass{
		EndingDirection: "角色承担自己选择造成的可见后果",
		OpenThreads:     []string{"票据来源"},
		EstimatedScale:  "三章测试",
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.World.SaveWorldRules([]domain.WorldRule{{
		Category: "society",
		Rule:     "交易必须留下双方可复核票据",
		Boundary: "票据不能替代角色亲历",
	}}); err != nil {
		t.Fatal(err)
	}
	if err := st.Characters.Save([]domain.Character{{
		Name: "主角", Role: "主角", Tier: "core",
	}}); err != nil {
		t.Fatal(err)
	}
	progress := &domain.Progress{
		NovelName:      "三章机械提升测试",
		Phase:          domain.PhaseWriting,
		Flow:           domain.FlowWriting,
		CurrentChapter: 1,
		TotalChapters:  3,
		GenerationID:   "cli-project-all-test",
	}
	if err := st.Progress.Save(progress); err != nil {
		t.Fatal(err)
	}
	baseCanonRoot, err := pipelineCanonRoot(st.Dir(), progress)
	if err != nil {
		t.Fatal(err)
	}
	dependencies, sourceArtifacts, err := pipelinePlanningDependencies(st.Dir())
	if err != nil {
		t.Fatal(err)
	}
	fingerprint, err := domain.NewDependencyFingerprint(
		progress.GenerationID,
		baseCanonRoot,
		dependencies,
	)
	if err != nil {
		t.Fatal(err)
	}
	projectAllCmdTestInstallPreplanManifests(
		t,
		st,
		outlines,
		progress.GenerationID,
		baseCanonRoot,
		fingerprint,
	)
	preplan := pipelinePreplanReceipt{
		Version:          pipelinePlanningSchema,
		GenerationID:     progress.GenerationID,
		BaseCanonChapter: 0,
		BaseCanonRoot:    baseCanonRoot,
		CurrentCanonRoot: baseCanonRoot,
		DependencyRoot:   fingerprint.RootSHA256,
		TotalChapters:    3,
		VolumeIndices:    []int{1},
		StagedChapters:   []int{1, 2, 3},
		DetailedChapters: []int{1, 2, 3},
		CreatedAt:        "2026-07-17T00:00:00Z",
		SourceArtifacts:  sourceArtifacts,
	}
	if _, err := writePipelinePlanningJSON(
		filepath.Join(st.Dir(), pipelinePlanningReceiptPath),
		preplan,
	); err != nil {
		t.Fatal(err)
	}
	identity, err := buildPipelineProjectAllIdentity(cfg, promptBundle, st, progress)
	if err != nil {
		t.Fatal(err)
	}
	projected := st.ProjectedV2()
	if err := projected.CreateBuildingGeneration(
		identity.Generation,
		identity.Source,
		identity.Registry,
	); err != nil {
		t.Fatal(err)
	}
	cursor, err := projected.InitializeProjectionCursor(identity.Generation.GenerationID)
	if err != nil {
		t.Fatal(err)
	}
	currentGeneration := identity.Generation
	currentRegistry := identity.Registry
	previous, preState, err := pipelineProjectAllTail(currentGeneration, nil)
	if err != nil {
		t.Fatal(err)
	}
	for chapter := 1; chapter <= 3; chapter++ {
		artifacts, outline := projectAllCmdTestArtifacts(t, identity.Generation.GenerationID, chapter)
		priorBundles, err := projected.LoadProjectedChapterBundles(identity.Generation.GenerationID)
		if err != nil {
			t.Fatalf("load prior CLI fixture bundles for chapter %d: %v", chapter, err)
		}
		projectAllCmdTestBindPlanningContext(
			t,
			artifacts,
			currentGeneration,
			priorBundles,
			currentRegistry,
			chapter,
		)
		bundle, nextRegistry, err := buildPipelineProjectedChapterBundle(
			currentGeneration,
			outline,
			previous,
			preState,
			artifacts,
			currentRegistry,
		)
		if err != nil {
			t.Fatalf("build CLI fixture chapter %d: %v", chapter, err)
		}
		cursor, err = projected.ProjectChapterAndAdvance(
			currentGeneration.GenerationDigest,
			currentGeneration.ChainTailRoot,
			currentGeneration.ObligationRegistryRoot,
			*cursor,
			bundle,
			nextRegistry,
		)
		if err != nil {
			t.Fatalf("persist CLI fixture chapter %d: %v", chapter, err)
		}
		loaded, err := projected.LoadBuildingGeneration(identity.Generation.GenerationID)
		if err != nil || loaded == nil {
			t.Fatalf("reload CLI fixture generation chapter %d: generation=%+v err=%v", chapter, loaded, err)
		}
		currentGeneration = *loaded
		currentRegistry = nextRegistry
		previous = bundle.BundleDigest
		preState = bundle.ProjectedPostStateRoot
	}
	return opts, st, identity
}

func projectAllCmdTestInstallPreplanManifests(
	t *testing.T,
	st *store.Store,
	outlines []domain.OutlineEntry,
	generationID string,
	baseCanonRoot string,
	fingerprint domain.DependencyFingerprint,
) {
	t.Helper()
	preState := baseCanonRoot
	manifests := make([]domain.StagedChapterPlanManifest, 0, len(outlines))
	for _, outline := range outlines {
		payload := pipelineProjectedChapterPayload{
			Version:         pipelinePlanningSchema,
			GenerationID:    generationID,
			Chapter:         outline.Chapter,
			Volume:          1,
			Arc:             1,
			Formal:          false,
			Authority:       "speculative",
			ProjectionLevel: pipelineProjectionExpanded,
			Outline:         outline,
			Notice:          "非正史稳定章位",
		}
		planRel := filepath.ToSlash(filepath.Join(
			"meta", "planning", "generations", generationID, "chapters",
			projectAllCmdTestChapterFile(outline.Chapter),
		))
		planSHA, err := writePipelinePlanningJSON(
			filepath.Join(st.Dir(), filepath.FromSlash(planRel)),
			payload,
		)
		if err != nil {
			t.Fatal(err)
		}
		projectionRoot, err := domain.DeterministicPlanningHash(payload)
		if err != nil {
			t.Fatal(err)
		}
		postState, err := domain.DeriveProjectedStateRoot(
			outline.Chapter,
			generationID,
			baseCanonRoot,
			fingerprint.RootSHA256,
			preState,
			projectionRoot,
		)
		if err != nil {
			t.Fatal(err)
		}
		manifests = append(manifests, domain.StagedChapterPlanManifest{
			Version:               domain.PlanningStoreVersion,
			Chapter:               outline.Chapter,
			Volume:                1,
			GenerationID:          generationID,
			BaseCanonChapter:      0,
			BaseCanonRoot:         baseCanonRoot,
			DependencyFingerprint: fingerprint,
			Authority:             domain.PlanningAuthoritySpeculative,
			Realization:           domain.PlanningRealizationStaged,
			PlanPath:              planRel,
			PlanSHA256:            planSHA,
			ProjectedState: domain.ProjectedStateReceipt{
				Version:        domain.PlanningStoreVersion,
				Chapter:        outline.Chapter,
				GenerationID:   generationID,
				BaseCanonRoot:  baseCanonRoot,
				DependencyRoot: fingerprint.RootSHA256,
				Authority:      domain.PlanningAuthorityProjected,
				Realization:    domain.PlanningRealizationStaged,
				PreStateRoot:   preState,
				ProjectionRoot: projectionRoot,
				PostStateRoot:  postState,
			},
		})
		preState = postState
	}
	if err := st.Planning.ReplaceStagedChapterPlanManifests(manifests); err != nil {
		t.Fatal(err)
	}
}

func projectAllCmdTestGenerationAndRegistry(
	t *testing.T,
	count int,
) (domain.PlanningGenerationV2, domain.ObligationRegistryV2) {
	t.Helper()
	baseCanonRoot := projectAllCmdTestDigest("canon")
	baseStateRoot := projectAllCmdTestDigest("state")
	outlineRoot := projectAllCmdTestDigest("outline")
	dependencyRoot := projectAllCmdTestDigest("dependencies")
	seedRoot, err := domain.ComputePlanningSeedContractRootV2("deterministic project-all test seed")
	if err != nil {
		t.Fatal(err)
	}
	generationID, err := domain.DerivePlanningGenerationAttemptV2ID(
		baseCanonRoot,
		outlineRoot,
		dependencyRoot,
		seedRoot,
		"",
	)
	if err != nil {
		t.Fatal(err)
	}
	registry := domain.ObligationRegistryV2{
		Version:      domain.ObligationRegistryV2Version,
		GenerationID: generationID,
		FirstChapter: 1,
		LastChapter:  count,
		Obligations:  []domain.ObligationV2{},
	}
	registry.RegistryRoot, err = domain.ComputeObligationRegistryV2Root(registry)
	if err != nil {
		t.Fatal(err)
	}
	generation := domain.PlanningGenerationV2{
		Version:                domain.PlanningGenerationV2Version,
		GenerationID:           generationID,
		Status:                 domain.PlanningGenerationBuildingV2,
		BaseCanonChapter:       0,
		BaseCanonRoot:          baseCanonRoot,
		BaseStateRoot:          baseStateRoot,
		StableOutlineRoot:      outlineRoot,
		PlanningDependencyRoot: dependencyRoot,
		RandomSeedContractRoot: seedRoot,
		FirstProjectedChapter:  1,
		LastProjectedChapter:   count,
		ExpectedChapterCount:   count,
		ProjectedChapterCount:  0,
		ObligationRegistryRoot: registry.RegistryRoot,
		CreatedAt:              "2026-07-17T00:00:00Z",
	}
	generation.GenerationDigest, err = domain.ComputePlanningGenerationV2Digest(generation)
	if err != nil {
		t.Fatal(err)
	}
	return generation, registry
}

func projectAllCmdTestArtifacts(
	t *testing.T,
	generationID string,
	chapter int,
) (*agents.ProjectedChapterArtifacts, domain.OutlineEntry) {
	t.Helper()
	simulationID := fmt.Sprintf("sim-project-all-%06d", chapter)
	simulation := domain.ChapterWorldSimulation{
		Version:      1,
		SimulationID: simulationID,
		Chapter:      chapter,
		GenerationID: generationID,
		TimeWindow:   "上午八点到中午",
		CharacterDecisions: []domain.CharacterWorldDecision{{
			Character:         "主角",
			Time:              "上午",
			Location:          "青山县旧街",
			CurrentGoal:       "先验证规则再扩大行动",
			Pressure:          "必须在午前拿到可复核结果",
			KnowledgeBoundary: "只知道自己亲历和收到的票据",
			AvailableOptions:  []string{"小额验证", "暂时放弃"},
			Decision:          "小额验证",
			DecisionReason:    "可以控制损失并留下证据",
			Action:            "完成一次可撤回的小额交易",
			ActionDuration:    "两小时",
			CompletionState:   "completed",
			ImmediateResult:   "拿到可复核票据",
			StateAfter:        "从猜测转为有限确认",
		}},
		ProtagonistProjection: domain.ProtagonistDecisionProjection{
			Protagonist:       "主角",
			ObservableEffects: []string{"交易完成且票据可见"},
			HiddenPressures:   []string{"后台仍有未抵达主角的信息"},
			AvailableOptions:  []string{"小额验证", "暂时放弃"},
			ChosenDecision:    "小额验证",
			DecisionReason:    "可以控制损失并留下证据",
			PlanConstraints:   []string{"只写主角可知事实"},
			CausalChain:       []string{"压力出现 -> 主角选择 -> 票据形成"},
		},
	}
	plan := domain.ChapterPlan{
		Chapter:  chapter,
		Title:    fmt.Sprintf("第%d章", chapter),
		Goal:     "用行动验证当前规则",
		Conflict: "时间与可支配资源同时受压",
		Hook:     "票据上的细节迫使主角作出下一次选择",
		Contract: domain.ChapterContract{
			RequiredBeats:    []string{"主角完成小额验证并拿到票据"},
			ForbiddenMoves:   []string{"不得泄露主角尚未知的后台信息"},
			ContinuityChecks: []string{"交易前后资源数量必须连续"},
			PayoffPoints:     []string{"小额验证留下可复核票据"},
		},
		CausalSimulation: domain.ChapterCausalSimulation{
			WorldSimulationID:   simulationID,
			ProtagonistDecision: "小额验证",
			ProjectPromise:      "每次获得能力都必须通过人物选择和可见代价兑现",
			ChapterFunction:     "用一次可撤回行动建立规则可信度并留下下一章压力",
			RenderCapacity: &domain.ChapterRenderCapacity{
				TotalTargetRunes:  2100,
				AntiPaddingPolicy: "每一场都必须改变证据、选择或代价；禁止靠复述、回忆和同义对话凑字数",
				SceneUnits: []domain.ChapterRenderSceneUnit{
					{
						SceneID: "pressure", TargetRunes: 700,
						POVObjective: "在截止前确认交易条件", ActiveOpposition: "对方回避留下书面凭据",
						Turn: "主角用撤回交易迫使对方明确条件", ExitConsequence: "双方进入必须当面验票的交易",
						ConcreteActionBeats: []string{"主角核对墙上时钟", "对方收起空白票据", "主角把零钱重新放回口袋"},
					},
					{
						SceneID: "transaction", TargetRunes: 700,
						POVObjective: "完成可撤回的小额验证", ActiveOpposition: "金额与时间窗口同时收紧",
						Turn: "交易完成却出现一处不合常理的编号", ExitConsequence: "主角拿到可复核但带疑点的票据",
						ConcreteActionBeats: []string{"主角逐张数清零钱", "对方在票据上落笔", "主角当面比对编号"},
					},
					{
						SceneID: "verification", TargetRunes: 700,
						POVObjective: "确认票据能否支持当前判断", ActiveOpposition: "编号疑点让结论无法直接成立",
						Turn: "主角发现疑点可被下一步行动验证", ExitConsequence: "有限确认转化为下一章的现实选择",
						ConcreteActionBeats: []string{"主角迎光检查纸面压痕", "他把金额记入随身本", "他圈出需要追查的编号"},
					},
				},
			},
			ContextSources: []string{
				"chapter_world_simulation:" + simulationID,
				"world_foundation:test-rules",
				"character_dossiers:主角",
				fmt.Sprintf("current_chapter_outline:%d", chapter),
				"progression:chapter_contract",
			},
			InitialState: []domain.CharacterSimulationState{{
				Character:      "主角",
				CurrentGoal:    "在午前验证规则",
				Pressure:       "可支配资源有限",
				ActionTendency: "先做可撤回的小额试验",
				LikelyAction:   "要求交易留下票据",
			}},
			VoiceLogic: []domain.CharacterVoiceLogic{{
				Character:         "主角",
				SceneObjective:    "确认交易能否复核",
				HiddenSubtext:     "不愿暴露自己仍不确定",
				KnowledgeBoundary: "只谈亲历和票据",
				DictionAndRhythm:  "短句，先问具体条件",
				DialogueFunctions: []string{"试探规则", "推动交易"},
				ForbiddenMoves:    []string{"替场景总结道理"},
			}},
			EmotionalLogic: []domain.CharacterEmotionalLogic{{
				Character:          "主角",
				ImmediateState:     "警惕但愿意试一次",
				PrimaryEmotion:     "克制的焦虑",
				EmotionalTrigger:   "午前截止逼近",
				GoalAppraisal:      "小额损失仍可承担",
				RegulationStrategy: "把不安转成逐项核对",
				EmotionLedAction:   "要求对方当面写清票据",
				EvidenceInScene:    []string{"反复核对时间和金额"},
			}},
			CausalBeats: []domain.CausalSimulationBeat{{
				Cause:           "午前截止压力出现",
				CharacterChoice: "主角选择小额验证",
				WorldResponse:   "交易留下可复核票据",
				StoryResult:     "主角获得有限确认",
			}},
			DecisionPoints:   []string{"是否承担一次可撤回的小额损失"},
			OutcomeShift:     []string{"主角从猜测转为拥有一份可复核证据"},
			SceneConstraints: []string{"主视角不得越过票据和亲历信息"},
		},
	}
	renderContext, err := json.Marshal(map[string]any{
		"_context_profile":         "draft",
		"chapter_plan":             plan,
		"chapter_world_simulation": simulation,
	})
	if err != nil {
		t.Fatal(err)
	}
	return &agents.ProjectedChapterArtifacts{
			WorldSimulation: &simulation,
			Plan:            &plan,
			RenderContext:   renderContext,
		}, domain.OutlineEntry{
			Chapter:   chapter,
			Title:     plan.Title,
			CoreEvent: "主角完成小额验证并获得证据",
			Hook:      plan.Hook,
		}
}

func projectAllCmdTestBindPlanningContext(
	t *testing.T,
	artifacts *agents.ProjectedChapterArtifacts,
	generation domain.PlanningGenerationV2,
	prior []domain.ProjectedChapterBundle,
	registry domain.ObligationRegistryV2,
	chapter int,
) {
	t.Helper()
	if artifacts == nil {
		t.Fatal("cannot bind planning context to nil projected artifacts")
	}
	planningContext, err := domain.DeriveProjectedPlanningContextV2(
		generation,
		prior,
		registry,
		chapter,
	)
	if err != nil {
		t.Fatalf("derive projected planning context for chapter %d: %v", chapter, err)
	}
	artifacts.PlanningContextDigest = planningContext.ContextDigest
	transition := domain.ArcChapterTransitionContract{
		OutgoingConsequenceID:   fmt.Sprintf("fixture-consequence-%06d", chapter),
		OutgoingConsequenceText: fmt.Sprintf("第%d章的行动留下第%d章必须处理的可复核后果", chapter, chapter+1),
	}
	if predecessor := planningContext.PredecessorContract; predecessor != nil {
		transition.IncomingConsequenceID = predecessor.OutgoingConsequenceID
		transition.IncomingConsequenceText = predecessor.OutgoingConsequenceText
		if len(artifacts.Plan.CausalSimulation.CausalBeats) == 0 {
			t.Fatalf("chapter %d fixture lacks causal beat for predecessor consumption", chapter)
		}
		transition.ConsumedByCause = artifacts.Plan.CausalSimulation.CausalBeats[0].Cause
	}
	artifacts.Plan.CausalSimulation.ArcTransition = transition
	token, err := domain.ProjectedPlanningContextSourceTokenV2(planningContext.ContextDigest)
	if err != nil {
		t.Fatalf("derive projected planning context token for chapter %d: %v", chapter, err)
	}
	artifacts.WorldSimulation.Sources = append(artifacts.WorldSimulation.Sources, token)
	if artifacts.RAGFactReceipt == nil {
		receipt, receiptErr := domain.NewRAGFactReceipt(
			chapter,
			"project-all chapter fixture",
			[]string{"fixture"},
			"no_material_v1",
			"",
			nil,
		)
		if receiptErr != nil {
			t.Fatalf("build no-material fact receipt for chapter %d: %v", chapter, receiptErr)
		}
		artifacts.RAGFactReceipt = &receipt
	}
	if artifacts.CraftRecallReceipt == nil {
		receipt := projectAllCmdTestNoMaterialCraftReceipt(
			t,
			generation.GenerationID,
			chapter,
			planningContext.ContextDigest,
		)
		artifacts.CraftRecallReceipt = &receipt
	}
	artifacts.Plan.CausalSimulation.ContextSources = append(
		artifacts.Plan.CausalSimulation.ContextSources,
		token,
		artifacts.RAGFactReceipt.SourceToken(),
		domain.CraftRecallReceiptSourceTokenV2(*artifacts.CraftRecallReceipt),
	)
}

func projectAllCmdTestNoMaterialCraftReceipt(
	t *testing.T,
	generationID string,
	chapter int,
	planningContextDigest string,
) domain.CraftRecallReceipt {
	t.Helper()
	receipt := domain.CraftRecallReceipt{
		Version:               1,
		ID:                    fmt.Sprintf("%024x", chapter),
		Chapter:               chapter,
		Stage:                 domain.ProjectAllCraftReceiptStage,
		GenerationID:          generationID,
		PlanningContextDigest: planningContextDigest,
		IndexIdentity:         "fixture-index",
		Enforcement:           true,
		CreatedAt:             "2026-07-17T00:00:00Z",
		Attempts: []domain.CraftRecallReceiptAttempt{
			{
				Need:       domain.CraftRecallNeed{ID: "project-all-methodology", Field: "methodology", Topic: "fixture methodology"},
				NoMaterial: true,
			},
			{
				Need:       domain.CraftRecallNeed{ID: "project-all-scene", Field: "scene_situation", Topic: "fixture scene"},
				NoMaterial: true,
			},
		},
	}
	receipt.PayloadSHA256 = domain.ComputeCraftRecallReceiptPayloadSHA256(receipt)
	if err := domain.ValidateProjectAllCraftRecallReceipt(receipt); err != nil {
		t.Fatalf("build no-material craft receipt for chapter %d: %v", chapter, err)
	}
	return receipt
}

func TestProjectAllRestartAttemptPersistsRecoveryIntent(t *testing.T) {
	outputDir := t.TempDir()
	if err := rotatePipelineProjectAllAttempt(outputDir); err != nil {
		t.Fatal(err)
	}
	nonce, err := loadPipelineProjectAllAttemptNonce(outputDir)
	if err != nil || strings.TrimSpace(nonce) == "" {
		t.Fatalf("rotated attempt nonce missing: nonce=%q err=%v", nonce, err)
	}
	if pending, err := pipelineProjectAllRestartPending(outputDir); err != nil || !pending {
		t.Fatalf("restart crash intent was not durable: pending=%v err=%v", pending, err)
	}
	if err := completePipelineProjectAllRestart(outputDir); err != nil {
		t.Fatal(err)
	}
	if pending, err := pipelineProjectAllRestartPending(outputDir); err != nil || pending {
		t.Fatalf("completed restart intent remained pending: pending=%v err=%v", pending, err)
	}
	after, err := loadPipelineProjectAllAttemptNonce(outputDir)
	if err != nil || after != nonce {
		t.Fatalf("finalizing restart changed generation nonce: before=%s after=%s err=%v", nonce, after, err)
	}
}

func TestBuildProjectAllIdentityBindsRotatedAttemptNonce(t *testing.T) {
	opts, st, _ := projectAllCmdTestInstallThreeChapterCLIProjection(t)
	if err := rotatePipelineProjectAllAttempt(st.Dir()); err != nil {
		t.Fatal(err)
	}
	nonce, err := loadPipelineProjectAllAttemptNonce(st.Dir())
	if err != nil || strings.TrimSpace(nonce) == "" {
		t.Fatalf("rotated attempt nonce missing: nonce=%q err=%v", nonce, err)
	}
	cfg, promptBundle, err := loadCfgBundle(opts)
	if err != nil {
		t.Fatal(err)
	}
	progress, err := st.Progress.Load()
	if err != nil || progress == nil {
		t.Fatalf("load progress: %+v err=%v", progress, err)
	}
	identity, err := buildPipelineProjectAllIdentity(cfg, promptBundle, st, progress)
	if err != nil {
		t.Fatalf("non-empty attempt made generation identity invalid: %v", err)
	}
	if identity.Generation.AttemptID != nonce {
		t.Fatalf("generation attempt_id=%q want %q", identity.Generation.AttemptID, nonce)
	}
	wantID, err := domain.DerivePlanningGenerationAttemptV2ID(
		identity.Generation.BaseCanonRoot,
		identity.Generation.StableOutlineRoot,
		identity.Generation.PlanningDependencyRoot,
		identity.Generation.RandomSeedContractRoot,
		nonce,
	)
	if err != nil {
		t.Fatal(err)
	}
	if identity.Generation.GenerationID != wantID {
		t.Fatalf("generation_id=%s want %s", identity.Generation.GenerationID, wantID)
	}
}

func TestBuildProjectAllIdentityFailsClosedOnFoundationDriftBeforeSeal(t *testing.T) {
	opts, st, _ := projectAllCmdTestInstallThreeChapterCLIProjection(t)
	projectAllCmdTestWriteFile(
		t,
		filepath.Join(st.Dir(), "meta", "world_foundation.json"),
		`{"iron_laws":["seal 前被改写"]}`,
	)
	cfg, promptBundle, err := loadCfgBundle(opts)
	if err != nil {
		t.Fatal(err)
	}
	progress, err := st.Progress.Load()
	if err != nil || progress == nil {
		t.Fatalf("load progress: %+v err=%v", progress, err)
	}
	if _, err := buildPipelineProjectAllIdentity(cfg, promptBundle, st, progress); err == nil ||
		!strings.Contains(err.Error(), "preplan") {
		t.Fatalf("foundation drift before seal did not fail closed through preplan/source verification: %v", err)
	}
}

func TestFreshProjectAllIdentityChangesWhenRAGSnapshotDrifts(t *testing.T) {
	opts, st, before := projectAllCmdTestInstallThreeChapterCLIProjection(t)
	projectAllCmdTestWriteFile(
		t,
		filepath.Join(st.Dir(), "meta", "rag", "index_state.json"),
		`{"chunks":[{"id":"new-planning-fact"}]}`,
	)
	projectAllCmdTestWriteFile(
		t,
		filepath.Join(st.Dir(), "meta", "rag", "vector_store.json"),
		`{"points":[{"id":"new-planning-fact"}]}`,
	)
	cfg, promptBundle, err := loadCfgBundle(opts)
	if err != nil {
		t.Fatal(err)
	}
	progress, err := st.Progress.Load()
	if err != nil || progress == nil {
		t.Fatalf("load progress: %+v err=%v", progress, err)
	}
	after, err := buildPipelineProjectAllIdentity(cfg, promptBundle, st, progress)
	if err != nil {
		t.Fatal(err)
	}
	if after.Generation.BaseCanonRoot != before.Generation.BaseCanonRoot {
		t.Fatal("derived RAG drift incorrectly changed canonical story root")
	}
	if after.Generation.PlanningDependencyRoot == before.Generation.PlanningDependencyRoot ||
		after.Generation.GenerationID == before.Generation.GenerationID {
		t.Fatal("fresh project-all generation identity ignored exact RAG snapshot drift")
	}
}

func projectAllCmdTestSnapshotFiles(t *testing.T, root string) map[string]string {
	t.Helper()
	out := make(map[string]string)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out[filepath.ToSlash(rel)] = string(raw)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func projectAllCmdTestWriteFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func projectAllCmdTestDigest(seed string) string {
	hash, _ := domain.DeterministicPlanningHash(seed)
	return domain.PlanningV2DigestPrefix + strings.TrimPrefix(hash, domain.PlanningV2DigestPrefix)
}

func projectAllCmdTestChapterFile(chapter int) string {
	return fmt.Sprintf("%06d.projected.json", chapter)
}
