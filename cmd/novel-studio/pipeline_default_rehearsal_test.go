package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func saveDefaultMigrationState(t *testing.T, opts cliOptions, st *store.Store, stages []string) *domain.PipelineState {
	t.Helper()
	cfg, bundle, err := loadPipelineDefaultInputsReadOnly(opts)
	if err != nil {
		t.Fatal(err)
	}
	state := &domain.PipelineState{Stages: append([]string(nil), stages...), InputDigest: pipelineRunInputDigest(cfg, bundle), RunIdentity: pipelineRunIdentityDigest(pipelineFlags{})}
	for _, stage := range []string{"architect", "outline-all", "zero-init", "preplan"} {
		state.MarkDone(stage, domain.PipelineStageEvidence{Stage: stage, Status: "verified"})
	}
	if err := savePipelineState(filepath.Join(st.Dir(), "meta/pipeline.json"), state); err != nil {
		t.Fatal(err)
	}
	return state
}

func TestDefaultRehearsalMigrationPreservesOriginalBuildingAndOldGraph(t *testing.T) {
	opts, st, old := continuationProducerCLIFixture(t)
	if err := os.Remove(filepath.Join(st.Dir(), "meta/planning/v2/projection_cursor.json")); err != nil {
		t.Fatal(err)
	}
	previous := saveDefaultMigrationState(t, opts, st, legacyDefaultPipelineStages)
	// This unrelated report-era directory cannot upgrade the existing attempt.
	if err := os.MkdirAll(filepath.Join(st.Dir(), store.ArcRehearsalRoot), 0o755); err != nil {
		t.Fatal(err)
	}
	before, err := store.DirectoryContentRoot(st.Dir())
	if err != nil {
		t.Fatal(err)
	}
	plan, err := preparePipelineDefaultResume(opts, pipelineFlags{}, defaultPipelineStages, "")
	if err != nil {
		t.Fatal(err)
	}
	if plan == nil || !plan.Preserve || plan.Generation.GenerationID != old.Generation.GenerationID || plan.Generation.DetailWindow != nil || !slices.Equal(plan.Stages, legacyDefaultPipelineStages) {
		t.Fatalf("default request upgraded or discarded the existing legacy attempt: %+v", plan)
	}
	state, err := loadPipelineStateWithDefaultResume(filepath.Join(st.Dir(), "meta/pipeline.json"), plan.Stages, "", plan.InputDigest, pipelineRunIdentityDigest(pipelineFlags{}), false, plan)
	if err != nil {
		t.Fatal(err)
	}
	if !state.Done("preplan") || !reflect.DeepEqual(state.Completed, previous.Completed) || !reflect.DeepEqual(state.Evidence, previous.Evidence) || slices.Contains(state.Stages, "rehearse-arc") {
		t.Fatal("pure default graph migration erased the valid old completed graph")
	}
	after, err := store.DirectoryContentRoot(st.Dir())
	if err != nil || after != before {
		t.Fatalf("read-only resume modified source/cursor/state: %v", err)
	}
	if cursor, err := st.ProjectedV2().LoadProjectionCursor(); err != nil || cursor != nil {
		t.Fatalf("read-only preflight created or moved the missing cursor: %+v %v", cursor, err)
	}
}

func TestDefaultRehearsalMigrationRefusesInputDriftBeforeAnyOuterWrite(t *testing.T) {
	for _, mode := range []string{"global-fingerprint", "model", "source", "missing-scope", "missing-progress", "empty-progress", "explicit-author-prompt"} {
		t.Run(mode, func(t *testing.T) {
			opts, st, _ := continuationProducerCLIFixture(t)
			state := saveDefaultMigrationState(t, opts, st, legacyDefaultPipelineStages)
			prompt := ""
			switch mode {
			case "global-fingerprint":
				state.InputDigest = projectAllCmdTestDigest("unknown-old-components")
				if err := savePipelineState(filepath.Join(st.Dir(), "meta/pipeline.json"), state); err != nil {
					t.Fatal(err)
				}
			case "model":
				raw, err := os.ReadFile(opts.ConfigPath)
				if err != nil {
					t.Fatal(err)
				}
				var cfg map[string]any
				if err := json.Unmarshal(raw, &cfg); err != nil {
					t.Fatal(err)
				}
				cfg["model"] = "different-model-must-not-run"
				raw, _ = json.Marshal(cfg)
				if err := os.WriteFile(opts.ConfigPath, raw, 0o600); err != nil {
					t.Fatal(err)
				}
			case "source":
				path := filepath.Join(st.Dir(), "world_rules.json")
				raw, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, append(raw, '\n'), 0o644); err != nil {
					t.Fatal(err)
				}
			case "missing-scope":
				if err := os.WriteFile(filepath.Join(st.Dir(), "layered_outline.json"), []byte("{broken-original-scope"), 0o644); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(filepath.Join(st.Dir(), "meta/planning/v2/projection_cursor.json")); err != nil {
					t.Fatal(err)
				}
			case "missing-progress":
				if err := os.Remove(filepath.Join(st.Dir(), "meta/progress.json")); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(filepath.Join(st.Dir(), "meta/planning/v2/projection_cursor.json")); err != nil {
					t.Fatal(err)
				}
			case "empty-progress":
				if err := os.WriteFile(filepath.Join(st.Dir(), "meta/progress.json"), []byte("{}"), 0o644); err != nil {
					t.Fatal(err)
				}
			case "explicit-author-prompt":
				prompt = "修改旧generation既有作者合同"
			}
			before, err := store.DirectoryContentRoot(st.Dir())
			if err != nil {
				t.Fatal(err)
			}
			err = runPipelineWithStages(opts, pipelineFlags{}, defaultPipelineStages, prompt, nil)
			if err == nil {
				t.Fatal("changed inputs reached default execution")
			}
			if mode == "global-fingerprint" && (!strings.Contains(err.Error(), "stage 图可兼容") || !strings.Contains(err.Error(), "fingerprint")) {
				t.Fatalf("graph migration was confused with unverifiable input compatibility: %v", err)
			}
			after, rootErr := store.DirectoryContentRoot(st.Dir())
			if rootErr != nil || before != after {
				t.Fatalf("default rejection wrote a manifest, mode marker, source or pipeline state: %v / %v", err, rootErr)
			}
		})
	}
}

func TestDefaultRehearsalMigrationDoesNotShrinkIndependentSixChapterLegacyAttempt(t *testing.T) {
	st := store.NewStore(t.TempDir())
	generation, registry := projectAllCmdTestGenerationAndRegistry(t, 6)
	arc := pipelineArcScope{Volume: 1, Arc: 1, FirstChapter: 1, LastChapter: 6, BookLastChapter: 6}
	generation.ProjectionScope, generation.ScopeID, generation.BookHorizonChapter = domain.PlanningProjectionScopeArcV2, domain.DeriveArcCycleID(1, 1, 1, 6), 6
	registry.ProjectionScope, registry.ScopeID, registry.BookHorizonChapter = generation.ProjectionScope, generation.ScopeID, 6
	registry.RegistryRoot, _ = domain.ComputeObligationRegistryV2Root(registry)
	generation.ObligationRegistryRoot = registry.RegistryRoot
	generation.GenerationDigest, _ = domain.ComputePlanningGenerationV2Digest(generation)
	source := domain.PlanningSourceSnapshotV2{Version: domain.PlanningSourceSnapshotV2Version, GenerationID: generation.GenerationID,
		BaseCanonRoot: generation.BaseCanonRoot, BaseStateRoot: generation.BaseStateRoot, StableOutlineRoot: generation.StableOutlineRoot,
		PlanningDependencyRoot: generation.PlanningDependencyRoot, RandomSeedContractRoot: generation.RandomSeedContractRoot,
		FoundationSnapshotRoot: projectAllCmdTestDigest("six-chapter-foundation"), RAGSnapshotRoot: projectAllCmdTestDigest("six-chapter-rag"), CapturedAt: generation.CreatedAt}
	source.SnapshotDigest, _ = domain.ComputePlanningSourceSnapshotV2Digest(source)
	if err := st.ProjectedV2().CreateBuildingGeneration(generation, source, registry); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(st.Dir(), store.ArcRehearsalRoot), 0o755); err != nil {
		t.Fatal(err)
	}
	found, err := pipelineExistingAttemptBeforeDetailWindow(st, 0, arc)
	if err != nil || found == nil || found.GenerationID != generation.GenerationID || found.LastProjectedChapter != 6 || found.ExpectedChapterCount != 6 || found.DetailWindow != nil {
		t.Fatalf("old six-chapter source was silently replaced by a new three-chapter window: %+v %v", found, err)
	}
}

func TestDefaultRehearsalMigrationPreservesSealedPrefixAndExplicitStages(t *testing.T) {
	opts, st, identity := projectAllCmdTestInstallThreeChapterCLIProjection(t)
	if err := pipelineSeal(opts, pipelineFlags{}); err != nil {
		t.Fatal(err)
	}
	previous := saveDefaultMigrationState(t, opts, st, []string{"project-all", "seal"})
	previous.MarkDone("project-all", domain.PipelineStageEvidence{Stage: "project-all", Status: "verified"})
	previous.MarkDone("seal", domain.PipelineStageEvidence{Stage: "seal", Status: "verified"})
	if err := savePipelineState(filepath.Join(st.Dir(), "meta/pipeline.json"), previous); err != nil {
		t.Fatal(err)
	}
	plan, err := preparePipelineDefaultResume(opts, pipelineFlags{}, defaultPipelineStages, "")
	if err != nil {
		t.Fatal(err)
	}
	if plan == nil || !plan.Preserve || plan.Generation.GenerationID != identity.Generation.GenerationID || slices.Contains(plan.Stages, "preplan") || slices.Contains(plan.Stages, "rehearse-arc") {
		t.Fatalf("default resumed a sealed generation by restarting its preplan: %+v", plan)
	}
	if !slices.Equal(plan.Stages, []string{"promote", "render"}) {
		t.Fatalf("unexpected original sealed continuation route: %v", plan.Stages)
	}
	// Advance one real accepted chapter, leaving two sealed chapters pending.
	// Recovery must authenticate its old base, not rebuild preplan at base 1.
	p := st.ProjectedV2()
	bundles, err := p.LoadProjectedChapterBundles(identity.Generation.GenerationID)
	if err != nil {
		t.Fatal(err)
	}
	cursor, _ := p.LoadRealizationCursor()
	promotion := projectAllCmdTestPromotion(t, bundles[0])
	if _, err := p.Promote(*cursor, promotion); err != nil {
		t.Fatal(err)
	}
	body := strings.Repeat("章", 2100)
	projectAllCmdTestWriteFile(t, filepath.Join(st.Dir(), "chapters/01.md"), body)
	if err := st.Progress.MarkChapterComplete(1, 2100, "", ""); err != nil {
		t.Fatal(err)
	}
	progress, _ := st.Progress.Load()
	outcome := projectAllCmdTestOutcome(t, bundles[0], promotion, 1)
	outcome.ChapterBodySHA256 = domain.ComputeArcChapterBodySHA256([]byte(body))
	outcome.ActualCanonRoot, err = pipelineProjectAllLiveCanonRoot(st.Dir(), progress)
	if err != nil {
		t.Fatal(err)
	}
	outcome.ReceiptDigest, _ = domain.ComputeActualOutcomeReceiptV2Digest(outcome)
	cursor, _ = p.LoadRealizationCursor()
	if _, err := p.AcceptOutcome(*cursor, outcome); err != nil {
		t.Fatal(err)
	}
	mustWriteCurrentReviewArtifacts(t, st.Dir(), 1)
	inspection := inspectCurrentChapterReview(st.Dir(), 1)
	var reviews []domain.ChapterReviewArtifactBinding
	for _, rel := range inspection.Artifacts {
		digest, err := pipelineRequiredFileSHA(st.Dir(), rel)
		if err != nil {
			t.Fatal(err)
		}
		reviews = append(reviews, domain.ChapterReviewArtifactBinding{Path: rel, Digest: digest})
	}
	manifests, _ := st.ArcCycle().ListArcPlanningManifests(identity.Generation.GenerationID)
	acceptance, err := domain.SignChapterAcceptanceReceipt(domain.ChapterAcceptanceReceipt{Version: domain.ChapterAcceptanceReceiptLegacyVersion,
		ArcID: manifests[0].ArcID, ArcManifestDigest: manifests[0].ManifestDigest, GenerationID: identity.Generation.GenerationID, Chapter: 1,
		ChapterBodySHA256: outcome.ChapterBodySHA256, ChapterBodyRunes: 2100, ReviewArtifacts: reviews, OutcomeReceiptDigest: outcome.ReceiptDigest, AcceptedAt: outcome.AcceptedAt})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.ArcCycle().SaveChapterAcceptanceReceipt(acceptance); err != nil {
		t.Fatal(err)
	}
	plan, err = preparePipelineDefaultResume(opts, pipelineFlags{}, defaultPipelineStages, "")
	if err != nil || plan.Generation.BaseCanonChapter != 0 || plan.Generation.LastProjectedChapter != 3 || !slices.Equal(plan.Stages, []string{"promote", "render"}) {
		t.Fatalf("accepted 1/3 prefix was replanned or forced to initialize: %+v %v", plan, err)
	}
	explicit := []string{"project-all", "seal"}
	if got, err := preparePipelineDefaultResume(opts, pipelineFlags{Stages: "project-all,seal"}, explicit, ""); err != nil || got != nil {
		t.Fatalf("explicit production stage list entered default migration: %+v %v", got, err)
	}
}

func TestDefaultRehearsalGraphAndInitializationOnlyBoundaries(t *testing.T) {
	stages, err := resolveStages("")
	if err != nil || !slices.Equal(stages, []string{"architect", "outline-all", "zero-init", "preplan", "rehearse-arc", "project-all", "seal", "promote", "render"}) {
		t.Fatalf("new default order: %v %v", stages, err)
	}
	flags, _, err := parsePipelineFlags([]string{"--init-only"})
	if err != nil || flags.Stages != "architect,outline-all,zero-init" {
		t.Fatalf("init-only changed: %+v %v", flags, err)
	}
	if plan, err := preparePipelineDefaultResume(cliOptions{}, flags, []string{"architect", "outline-all", "zero-init"}, ""); err != nil || plan != nil {
		t.Fatalf("init-only entered rehearsal/config preflight: %+v %v", plan, err)
	}
	old := &domain.PipelineState{Stages: legacyDefaultPipelineStages, InputDigest: "same-input", RunIdentity: "same-run", Completed: []string{"architect", "outline-all", "zero-init"}}
	plan := &pipelineDefaultResume{Stages: stages, Previous: old, InputDigest: "same-input"}
	next, err := loadPipelineStateWithDefaultResume("unused-read-only-path", stages, "", "same-input", "same-run", false, plan)
	if err != nil || !next.Done("zero-init") || !slices.Contains(next.Stages, "rehearse-arc") || next.Done("rehearse-arc") {
		t.Fatalf("new-project graph migration invalidated initialization or invented rehearsal: %+v %v", next, err)
	}
}

func TestDefaultRehearsalWindowCompletionRequiresUpdatedForecast(t *testing.T) {
	for _, accepted := range []bool{false, true} {
		t.Run(map[bool]string{false: "frozen-window", true: "next-window"}[accepted], func(t *testing.T) {
			st := pipelineWindowCompletionTestStore(t)
			generation := pipelineWindowCompletionTestWindow(t, st, 1, 1, 6, 1, nil, accepted)
			_, err := verifyPipelineArcRehearsalStage(st.Dir(), domain.PipelineStageEvidence{Stage: "rehearse-arc"})
			if !accepted {
				if err != nil {
					t.Fatalf("unfinished sealed window lost its immutable rehearsal: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("previous window's rehearsal authorized a new window without an updated report")
			}
			state := pipelineWindowCompletionTestDoneState()
			if reset, err := resetCompletedSealedPipelineCycle(st.Dir(), state); err != nil || !reset {
				t.Fatalf("accepted window could not reset: %v", err)
			}
			if state.Evidence["render"].Status != "next_window" || state.Done("preplan") || state.Done("rehearse-arc") || state.Done("project-all") {
				t.Fatal("window acceptance did not require fresh preplan/rehearsal before its next three chapters")
			}
			if _, err := requirePipelineArcCompletion(st, generation); err == nil {
				t.Fatal("one accepted window completed the six-chapter arc")
			}
		})
	}
}
