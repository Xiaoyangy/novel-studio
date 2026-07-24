package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func newPipelineOutlineAllGenerationStore(t *testing.T) *store.Store {
	t.Helper()
	st := store.NewStore(filepath.Join(t.TempDir(), "output", "novel"))
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Save(&domain.Progress{
		NovelName:     "chapter-zero-generation",
		Phase:         domain.PhaseWriting,
		TotalChapters: 12,
	}); err != nil {
		t.Fatal(err)
	}
	return st
}

func TestEnsurePipelineOutlineAllGenerationSeedsOnce(t *testing.T) {
	st := newPipelineOutlineAllGenerationStore(t)
	generation, created, err := ensurePipelineOutlineAllGeneration(st, "generation-1")
	if err != nil || !created || generation != "generation-1" {
		t.Fatalf("seed outline-all generation: generation=%q created=%v err=%v", generation, created, err)
	}
	progressPath := filepath.Join(st.Dir(), "meta", "progress.json")
	afterFirst, err := os.ReadFile(progressPath)
	if err != nil {
		t.Fatal(err)
	}
	generation, created, err = ensurePipelineOutlineAllGeneration(st, "generation-2")
	if err != nil || created || generation != "generation-1" {
		t.Fatalf("reuse outline-all generation: generation=%q created=%v err=%v", generation, created, err)
	}
	afterSecond, err := os.ReadFile(progressPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(afterFirst, afterSecond) {
		t.Fatal("outline-all generation retry rewrote progress")
	}
	progress, err := st.Progress.Load()
	if err != nil || progress == nil ||
		progress.GenerationMode != domain.GenerationModeSimulationRestartFromSeed {
		t.Fatalf("outline-all generation mode drifted: progress=%+v err=%v", progress, err)
	}
}

func TestEnsurePipelineOutlineAllGenerationPreservesRebaseLineage(t *testing.T) {
	st := newPipelineOutlineAllGenerationStore(t)
	if err := st.Progress.ResetForSimulationRestart("rebased", 12, "rebase-generation"); err != nil {
		t.Fatal(err)
	}
	generation, created, err := ensurePipelineOutlineAllGeneration(st, "replacement")
	if err != nil || created || generation != "rebase-generation" {
		t.Fatalf("existing rebase generation was not preserved: generation=%q created=%v err=%v", generation, created, err)
	}
}

func TestEnsurePipelineOutlineAllGenerationFailsClosedOnChapterEvidence(t *testing.T) {
	tests := []struct {
		name  string
		dirty func(*testing.T, *store.Store)
	}{
		{
			name: "chapter body",
			dirty: func(t *testing.T, st *store.Store) {
				path := filepath.Join(st.Dir(), "chapters", "01.md")
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("started"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "chapter checkpoint",
			dirty: func(t *testing.T, st *store.Store) {
				if _, err := st.Checkpoints.Append(domain.ChapterScope(1), "draft", "drafts/01.draft.md", "sha256:test"); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "corrupt checkpoint journal",
			dirty: func(t *testing.T, st *store.Store) {
				path := filepath.Join(st.Dir(), "meta", "checkpoints.jsonl")
				if err := os.WriteFile(path, []byte("{truncated\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st := newPipelineOutlineAllGenerationStore(t)
			tc.dirty(t, st)
			progressPath := filepath.Join(st.Dir(), "meta", "progress.json")
			before, err := os.ReadFile(progressPath)
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := ensurePipelineOutlineAllGeneration(st, "forbidden"); err == nil {
				t.Fatal("outline-all generation accepted durable chapter evidence")
			}
			after, err := os.ReadFile(progressPath)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before, after) {
				t.Fatal("rejected outline-all generation mutated progress")
			}
		})
	}
}

func TestPipelineOutlineAllCandidateAllowsCurrentAttemptArcCheckpoint(t *testing.T) {
	st := newPipelineOutlineAllGenerationStore(t)
	if _, err := st.Checkpoints.Append(
		domain.ArcScope(1, 1),
		"outline-all:revise_arc",
		"layered_outline.json+outline.json",
		"sha256:current-attempt",
	); err != nil {
		t.Fatal(err)
	}
	if err := validatePipelineOutlineAllEntry(st); err != nil {
		t.Fatalf("candidate validator rejected a legal current-attempt arc checkpoint: %v", err)
	}
	if err := validatePipelineOutlineAllLiveEntry(st); err == nil {
		t.Fatal("live pre-attempt validator accepted an unexplained arc checkpoint")
	}
}
