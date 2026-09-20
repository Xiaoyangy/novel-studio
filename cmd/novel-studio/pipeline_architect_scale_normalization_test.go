package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/assets"
	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

// This is the real completion seam used immediately after a first Architect
// run produces all foundation files. No headless/provider call is needed.
func pipelineArchitectScaleFixture(t *testing.T, withRequirement ...bool) string {
	t.Helper()
	dir := seedZeroInitProject(t)
	st := store.NewStore(dir)
	if err := st.Progress.Save(&domain.Progress{Phase: domain.PhaseOutline, TotalChapters: 12}); err != nil {
		t.Fatal(err)
	}
	outline, err := st.Outline.LoadOutline()
	if err != nil {
		t.Fatal(err)
	}
	chapters := make([]domain.OutlineEntry, 12)
	for i := range chapters {
		chapters[i] = outline[0]
		chapters[i].Chapter = i + 1
	}
	if err := st.Outline.SaveLayeredOutline([]domain.VolumeOutline{{
		Index: 1, Title: "全书", Arcs: []domain.ArcOutline{{Index: 1, Title: "主弧", Chapters: chapters}},
	}}); err != nil {
		t.Fatal(err)
	}
	compass, err := st.Outline.LoadCompass()
	if err != nil {
		t.Fatal(err)
	}
	compass.EstimatedScale = "单卷12章，正文2.8万—3万字"
	if err := st.Outline.SaveCompass(*compass); err != nil {
		t.Fatal(err)
	}
	if len(withRequirement) == 0 || withRequirement[0] {
		if err := ensurePipelineOutlineAllRequirement(dir); err != nil {
			t.Fatal(err)
		}
	}
	readiness := assessArchitectReadiness(dir)
	if !readiness.Ready {
		t.Fatalf("foundation fixture is not ready: %v / %v", readiness.Missing, readiness.Issues)
	}
	if err := writeArchitectReadiness(dir, readiness); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestPipelineArchitectLegacyCompletionKeepsUnnormalizedSourceBytes(t *testing.T) {
	dir := pipelineArchitectScaleFixture(t, false)
	before := pipelineRenderCandidateTestSnapshot(t, dir)
	if err := pipelineEnsureOrRepairArchitectReadiness(cliOptions{}, bootstrap.Config{OutputDir: dir}, assets.Bundle{}, ""); err != nil {
		t.Fatal(err)
	}
	if after := pipelineRenderCandidateTestSnapshot(t, dir); !reflect.DeepEqual(before, after) {
		t.Fatal("Architect-only completion changed a legacy source without outline-all requirement")
	}
}

func TestPipelineArchitectFirstCompletionNormalizesOutlineAllScale(t *testing.T) {
	dir := pipelineArchitectScaleFixture(t)
	if err := pipelineEnsureOrRepairArchitectReadiness(cliOptions{}, bootstrap.Config{OutputDir: dir}, assets.Bundle{}, ""); err != nil {
		t.Fatalf("first foundation completion: %v", err)
	}
	assertPipelineArchitectScaleNormalized(t, dir)
	before := pipelineRenderCandidateTestSnapshot(t, dir)
	if err := pipelineEnsureOrRepairArchitectReadiness(cliOptions{}, bootstrap.Config{OutputDir: dir}, assets.Bundle{}, ""); err != nil {
		t.Fatalf("idempotent completion: %v", err)
	}
	if after := pipelineRenderCandidateTestSnapshot(t, dir); !reflect.DeepEqual(before, after) {
		t.Fatal("a repeated completion changed normalized foundation or readiness bytes")
	}
}

func TestPipelineArchitectScaleNormalizationRejectsFrozenSourcesWithoutWrites(t *testing.T) {
	for _, kind := range []string{"accepted-canon", "zero-init", "planning-generation", "published-outline-all"} {
		t.Run(kind, func(t *testing.T) {
			dir := pipelineArchitectScaleFixture(t)
			st := store.NewStore(dir)
			switch kind {
			case "accepted-canon":
				if err := st.Progress.Save(&domain.Progress{Phase: domain.PhaseWriting, CurrentChapter: 1, CompletedChapters: []int{1}, TotalWordCount: 20}); err != nil {
					t.Fatal(err)
				}
			case "zero-init":
				mustWriteFile(t, filepath.Join(dir, "meta", "first_chapter_generation_readiness.json"), `{"ready":true}`)
			case "planning-generation":
				mustWriteFile(t, filepath.Join(dir, "meta", "planning", "v2", "generations", "frozen.json"), `{"frozen":true}`)
			case "published-outline-all":
				_, published := publishOutlineAllGateFixture(t, true)
				dir, st = published.Dir(), published
				compass, err := st.Outline.LoadCompass()
				if err != nil {
					t.Fatal(err)
				}
				compass.EstimatedScale = "单卷8章"
				if err := st.Outline.SaveCompass(*compass); err != nil {
					t.Fatal(err)
				}
				if err := ensurePipelineOutlineAllRequirement(dir); err != nil {
					t.Fatal(err)
				}
			}
			before := pipelineRenderCandidateTestSnapshot(t, dir)
			if err := pipelineNormalizeArchitectOutlineAllCompassScale(dir); err == nil {
				t.Fatal("frozen source was normalized")
			}
			if after := pipelineRenderCandidateTestSnapshot(t, dir); !reflect.DeepEqual(before, after) {
				for path, raw := range after {
					if previous, found := before[path]; !found || previous != raw {
						t.Errorf("rejected normalization changed file %s", path)
					}
				}
				for path := range before {
					if _, found := after[path]; !found {
						t.Errorf("rejected normalization removed file %s", path)
					}
				}
				t.Fatal("rejected normalization changed source files")
			}
		})
	}
}

func TestPipelineArchitectScaleNormalizationPreservesReadinessRepairFindings(t *testing.T) {
	dir := pipelineArchitectScaleFixture(t)
	st := store.NewStore(dir)
	rules, err := st.World.LoadWorldRules()
	if err != nil {
		t.Fatal(err)
	}
	rules[0].Rule = ""
	if err := st.World.SaveWorldRules(rules); err != nil {
		t.Fatal(err)
	}
	if err := pipelineNormalizeArchitectOutlineAllCompassScale(dir); err != nil {
		t.Fatalf("normalization blocked the caller's readiness repair path: %v", err)
	}
	assertPipelineArchitectScaleNormalized(t, dir)
	var report architectReadiness
	if err := readPipelinePlanningJSON(filepath.Join(dir, "meta", "architect_readiness.json"), &report); err != nil {
		t.Fatal(err)
	}
	if report.Ready {
		t.Fatal("normalization concealed an unrelated readiness failure")
	}
	target, err := pipelineArchitectReadinessRepairTarget(assessArchitectReadiness(dir))
	if err != nil || target.Type != "world_rules" {
		t.Fatalf("normalization lost the original repair authority: %+v %v", target, err)
	}
}

func TestPipelineArchitectCompletedFingerprintNormalizesBeforeNextStage(t *testing.T) {
	dir := pipelineArchitectScaleFixture(t)
	configPath := filepath.Join(t.TempDir(), "config.json")
	qdrant := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/collections" {
			t.Errorf("unexpected service request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusForbidden)
			return
		}
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	t.Cleanup(qdrant.Close)
	raw, err := json.Marshal(map[string]any{
		"provider": "ollama", "model": "never-called",
		"providers": map[string]any{"ollama": map[string]any{"type": "openai", "base_url": "http://127.0.0.1:1/v1"}},
		"rag":       map[string]any{"qdrant": map[string]any{"url": qdrant.URL}},
	})
	if err != nil {
		t.Fatal(err)
	}
	mustWriteFile(t, configPath, string(raw))
	opts := cliOptions{ConfigPath: configPath, Dir: dir, providerCallGuard: &pipelineProviderCallGuard{check: func() error {
		t.Error("unexpected provider dispatch")
		return fmt.Errorf("test forbids provider dispatch")
	}}}
	// The next stage stops at its manifest parser, before any provider dispatch.
	// Reaching it with a normalized compass proves the completed Architect path
	// repaired its output without replaying Architect.
	badManifest := filepath.Join(t.TempDir(), "stop-before-provider.json")
	mustWriteFile(t, badManifest, "{invalid-json")
	flags := pipelineFlags{OutlineRepairFile: badManifest, OutlineRepairDigest: "test-sentinel"}
	stages := []string{"architect", "outline-all"}
	cfg, bundle, err := loadPipelineDefaultInputsReadOnly(opts)
	if err != nil {
		t.Fatal(err)
	}
	state := &domain.PipelineState{Stages: stages, InputDigest: pipelineRunInputDigest(cfg, bundle), RunIdentity: pipelineRunIdentityDigest(flags)}
	evidence, err := verifyPipelineStage("architect", dir, flags, state, cfg)
	if err != nil {
		t.Fatal(err)
	}
	state.MarkDone("architect", stampPipelineArtifactDigests(dir, evidence))
	if err := savePipelineState(filepath.Join(dir, "meta", "pipeline.json"), state); err != nil {
		t.Fatal(err)
	}
	err = runPipelineWithStages(opts, flags, stages, "", nil)
	if err == nil || !strings.Contains(err.Error(), "invalid character") {
		t.Fatalf("did not stop at the no-provider next-stage boundary: %v", err)
	}
	assertPipelineArchitectScaleNormalized(t, dir)
}

func assertPipelineArchitectScaleNormalized(t *testing.T, dir string) {
	t.Helper()
	compass, err := store.NewStore(dir).Outline.LoadCompass()
	if err != nil {
		t.Fatal(err)
	}
	got, err := domain.ParseBookScaleRange(compass.EstimatedScale)
	want := domain.BookScaleRange{MinVolumes: 1, MaxVolumes: 1, MinChapters: 12, MaxChapters: 12}
	if err != nil || got != want {
		t.Fatalf("first Architect completion left outline-all scale unusable: scale=%q parsed=%+v want=%+v err=%v", compass.EstimatedScale, got, want, err)
	}
}
