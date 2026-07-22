package tools

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func TestActiveToolRenderCandidateManifestPreservesV2Compatibility(t *testing.T) {
	st, _ := renderConvergenceV2ManifestFixture(t)

	loaded, err := activeToolRenderCandidateManifest(st, 1)
	if err != nil || loaded == nil || loaded.Version != toolRenderCandidatePreviousManifestVersion {
		t.Fatalf("v2 render candidate compatibility failed: manifest=%+v err=%v", loaded, err)
	}
	ledger, err := loadToolRenderConvergenceLedger(st, loaded)
	if err != nil {
		t.Fatal(err)
	}
	if ledger.PipelineRenderInputDigest != "" || ledger.RenderContextSHA256 != "" ||
		ledger.EffectiveStyleReceiptDigest != "" {
		t.Fatalf("v2 ledger acquired synthetic v3 identity: %+v", ledger)
	}
}

func TestActiveToolRenderCandidateManifestValidatesV3EffectiveStyleIdentity(t *testing.T) {
	st, valid := renderConvergenceV3ManifestFixture(t)
	loaded, err := activeToolRenderCandidateManifest(st, valid.Chapter)
	if err != nil || loaded == nil {
		t.Fatalf("valid v3 render candidate was rejected: manifest=%+v err=%v", loaded, err)
	}

	otherDigest := renderConvergenceIdentityDigest("other sealed identity")
	tests := []struct {
		name   string
		mutate func(*toolRenderCandidateManifest)
	}{
		{
			name: "missing canonical pipeline render input digest",
			mutate: func(manifest *toolRenderCandidateManifest) {
				manifest.PipelineRenderInputDigest = ""
			},
		},
		{
			name: "malformed render context digest",
			mutate: func(manifest *toolRenderCandidateManifest) {
				manifest.RenderContextSHA256 = "sha256:short"
			},
		},
		{
			name: "malformed style receipt digest",
			mutate: func(manifest *toolRenderCandidateManifest) {
				manifest.EffectiveStyleReceiptDigest = "sha256:NOT-HEX"
			},
		},
		{
			name: "pipeline render input receipt drift",
			mutate: func(manifest *toolRenderCandidateManifest) {
				manifest.PipelineRenderInputDigest = otherDigest
			},
		},
		{
			name: "base render context receipt drift",
			mutate: func(manifest *toolRenderCandidateManifest) {
				manifest.RenderContextSHA256 = otherDigest
			},
		},
		{
			name: "effective style receipt digest drift",
			mutate: func(manifest *toolRenderCandidateManifest) {
				manifest.EffectiveStyleReceiptDigest = otherDigest
			},
		},
		{
			name: "version downgrade retaining v3 identity",
			mutate: func(manifest *toolRenderCandidateManifest) {
				manifest.Version = toolRenderCandidatePreviousManifestVersion
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mutated := valid
			test.mutate(&mutated)
			renderConvergenceWriteManifest(t, st, mutated)
			if loaded, err := activeToolRenderCandidateManifest(st, mutated.Chapter); err == nil || loaded != nil {
				t.Fatalf("v3 identity mutation was accepted: manifest=%+v loaded=%+v err=%v", mutated, loaded, err)
			}
		})
	}

	t.Run("legacy pipeline run input key is not an alias", func(t *testing.T) {
		raw, err := json.Marshal(valid)
		if err != nil {
			t.Fatal(err)
		}
		var object map[string]any
		if err := json.Unmarshal(raw, &object); err != nil {
			t.Fatal(err)
		}
		delete(object, "pipeline_render_input_digest")
		object["pipeline_run_input_digest"] = valid.PipelineRenderInputDigest
		renderConvergenceWriteJSON(
			t,
			filepath.Join(st.Dir(), "meta", "planning", "render_candidate.json"),
			object,
		)
		if loaded, err := activeToolRenderCandidateManifest(st, valid.Chapter); err == nil || loaded != nil {
			t.Fatalf("v3 legacy input-digest alias was accepted: loaded=%+v err=%v", loaded, err)
		}
	})
}

func TestActiveToolRenderCandidateManifestRejectsV2WithAnyV3EffectiveStyleField(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*toolRenderCandidateManifest)
	}{
		{
			name: "pipeline render input digest",
			mutate: func(manifest *toolRenderCandidateManifest) {
				manifest.PipelineRenderInputDigest = renderConvergenceIdentityDigest("downgraded render input")
			},
		},
		{
			name: "render context digest",
			mutate: func(manifest *toolRenderCandidateManifest) {
				manifest.RenderContextSHA256 = renderConvergenceIdentityDigest("downgraded render context")
			},
		},
		{
			name: "effective style receipt digest",
			mutate: func(manifest *toolRenderCandidateManifest) {
				manifest.EffectiveStyleReceiptDigest = renderConvergenceIdentityDigest("downgraded style receipt")
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			st, manifest := renderConvergenceV3ManifestFixture(t)
			manifest.Version = toolRenderCandidatePreviousManifestVersion
			manifest.PipelineRenderInputDigest = ""
			manifest.RenderContextSHA256 = ""
			manifest.EffectiveStyleReceiptDigest = ""
			test.mutate(&manifest)
			renderConvergenceWriteManifest(t, st, manifest)

			loaded, err := activeToolRenderCandidateManifest(st, manifest.Chapter)
			if err == nil || loaded != nil || !strings.Contains(err.Error(), "v2 identity contains v3 fields") {
				t.Fatalf("v2 candidate retained %s: loaded=%+v err=%v", test.name, loaded, err)
			}
		})
	}
}

func TestToolRenderConvergenceLedgerBindsV3EffectiveStyleDigests(t *testing.T) {
	st, manifest := renderConvergenceV3ManifestFixture(t)
	ledger, err := loadToolRenderConvergenceLedger(st, &manifest)
	if err != nil {
		t.Fatal(err)
	}
	if ledger.PipelineRenderInputDigest != manifest.PipelineRenderInputDigest ||
		ledger.RenderContextSHA256 != manifest.RenderContextSHA256 ||
		ledger.EffectiveStyleReceiptDigest != manifest.EffectiveStyleReceiptDigest {
		t.Fatalf("new v3 ledger omitted effective-style identity: %+v", ledger)
	}
	if err := saveToolRenderConvergenceLedger(st, &manifest, ledger); err != nil {
		t.Fatal(err)
	}

	drifted := manifest
	drifted.EffectiveStyleReceiptDigest = renderConvergenceIdentityDigest("different style receipt")
	if loaded, err := loadToolRenderConvergenceLedger(st, &drifted); err == nil || loaded != nil {
		t.Fatalf("persisted ledger accepted v3 identity drift: ledger=%+v err=%v", loaded, err)
	}
	downgraded := manifest
	downgraded.Version = toolRenderCandidatePreviousManifestVersion
	downgraded.PipelineRenderInputDigest = ""
	downgraded.RenderContextSHA256 = ""
	downgraded.EffectiveStyleReceiptDigest = ""
	if loaded, err := loadToolRenderConvergenceLedger(st, &downgraded); err == nil || loaded != nil {
		t.Fatalf("persisted v3 ledger accepted a v2 manifest downgrade: ledger=%+v err=%v", loaded, err)
	}
}

func TestActiveToolRenderCandidateManifestAuthenticatesPathTopology(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*testing.T, *store.Store, *toolRenderCandidateManifest)
	}{
		{
			name: "relative source output",
			mutate: func(_ *testing.T, _ *store.Store, manifest *toolRenderCandidateManifest) {
				manifest.SourceOutputDir = filepath.Join("relative", "output")
			},
		},
		{
			name: "unclean absolute source output",
			mutate: func(_ *testing.T, _ *store.Store, manifest *toolRenderCandidateManifest) {
				manifest.SourceOutputDir += string(os.PathSeparator) + "."
			},
		},
		{
			name: "external source output",
			mutate: func(t *testing.T, _ *store.Store, manifest *toolRenderCandidateManifest) {
				external := filepath.Join(t.TempDir(), "external-output")
				if err := os.MkdirAll(external, 0o755); err != nil {
					t.Fatal(err)
				}
				manifest.SourceOutputDir = external
			},
		},
		{
			name: "wrong candidate id",
			mutate: func(_ *testing.T, _ *store.Store, manifest *toolRenderCandidateManifest) {
				manifest.CandidateID = "render-ch0001-wrong-candidate"
			},
		},
		{
			name: "symlink source output",
			mutate: func(t *testing.T, _ *store.Store, manifest *toolRenderCandidateManifest) {
				external := filepath.Join(t.TempDir(), "external-output")
				if err := os.MkdirAll(external, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(manifest.SourceOutputDir); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(external, manifest.SourceOutputDir); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "symlink candidate namespace",
			mutate: func(t *testing.T, _ *store.Store, manifest *toolRenderCandidateManifest) {
				namespace := filepath.Join(filepath.Dir(manifest.SourceOutputDir), ".render-candidates")
				relocated := filepath.Join(filepath.Dir(namespace), ".render-candidates-relocated")
				if err := os.Rename(namespace, relocated); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(relocated, namespace); err != nil {
					t.Fatal(err)
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			st, manifest := renderConvergenceV2ManifestFixture(t)
			test.mutate(t, st, &manifest)
			renderConvergenceWriteManifest(t, st, manifest)
			if loaded, err := activeToolRenderCandidateManifest(st, manifest.Chapter); err == nil || loaded != nil {
				t.Fatalf("unsafe render convergence topology was accepted: loaded=%+v err=%v", loaded, err)
			}
		})
	}
}

func TestToolRenderConvergenceLedgerReadWriteReauthenticateTopology(t *testing.T) {
	st, manifest := renderConvergenceV2ManifestFixture(t)
	ledger, err := loadToolRenderConvergenceLedger(st, &manifest)
	if err != nil {
		t.Fatal(err)
	}

	namespace := filepath.Join(filepath.Dir(manifest.SourceOutputDir), ".render-candidates")
	relocated := filepath.Join(filepath.Dir(namespace), ".render-candidates-relocated")
	if err := os.Rename(namespace, relocated); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(relocated, namespace); err != nil {
		t.Fatal(err)
	}
	if loaded, err := loadToolRenderConvergenceLedger(st, &manifest); err == nil || loaded != nil {
		t.Fatalf("ledger read followed a replaced namespace: ledger=%+v err=%v", loaded, err)
	}
	if err := saveToolRenderConvergenceLedger(st, &manifest, ledger); err == nil {
		t.Fatal("ledger write followed a replaced namespace")
	}
}

func renderConvergenceV2ManifestFixture(t *testing.T) (*store.Store, toolRenderCandidateManifest) {
	t.Helper()
	root := t.TempDir()
	candidateID := "render-ch0001-v2-compatibility"
	sourceOutput := filepath.Join(root, "live", "output")
	candidateOutput := filepath.Join(
		filepath.Dir(sourceOutput), ".render-candidates", candidateID, "output",
	)
	if err := os.MkdirAll(sourceOutput, 0o755); err != nil {
		t.Fatal(err)
	}
	st := store.NewStore(candidateOutput)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(
		filepath.Dir(sourceOutput), ".render-candidates", "convergence", candidateID,
	), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := st.Drafts.SaveChapterPlan(domain.ChapterPlan{Chapter: 1, Title: "v2 compatibility"}); err != nil {
		t.Fatal(err)
	}
	checkpoint, err := st.Checkpoints.AppendArtifactLatest(
		domain.ChapterScope(1), "plan", "drafts/01.plan.json",
	)
	if err != nil {
		t.Fatal(err)
	}
	manifest := toolRenderCandidateManifest{
		Version:                toolRenderCandidatePreviousManifestVersion,
		CandidateID:            candidateID,
		GenerationID:           "generation-v2",
		Chapter:                1,
		PlanDigest:             checkpoint.Digest,
		PlanCheckpointSeq:      checkpoint.Seq,
		ProjectedBundleDigest:  "legacy-bundle-digest",
		PromotionReceiptDigest: "legacy-promotion-digest",
		SourceOutputDir:        sourceOutput,
	}
	renderConvergenceWriteManifest(t, st, manifest)
	return st, manifest
}

func renderConvergenceV3ManifestFixture(t *testing.T) (*store.Store, toolRenderCandidateManifest) {
	t.Helper()
	root := t.TempDir()
	candidateID := "render-ch0001-v3-effective-style"
	sourceOutput := filepath.Join(root, "live", "output")
	if err := os.MkdirAll(sourceOutput, 0o755); err != nil {
		t.Fatal(err)
	}
	st := store.NewStore(filepath.Join(
		filepath.Dir(sourceOutput), ".render-candidates", candidateID, "output",
	))
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(
		filepath.Dir(sourceOutput), ".render-candidates", "convergence", candidateID,
	), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := st.Drafts.SaveChapterPlan(domain.ChapterPlan{Chapter: 1, Title: "v3 effective style"}); err != nil {
		t.Fatal(err)
	}
	checkpoint, err := st.Checkpoints.AppendArtifactLatest(
		domain.ChapterScope(1), "plan", "drafts/01.plan.json",
	)
	if err != nil {
		t.Fatal(err)
	}
	baseContext := json.RawMessage(`{
		"_context_profile":"draft",
		"working_memory":{"render_packet":{"version":11,"chapter":1,"style_contract":{"version":3,"usage_policy":"surface only"}}}
	}`)
	frozenContext, err := PublishFrozenDraftRenderContext(st, 1, checkpoint.Digest, baseContext)
	if err != nil {
		t.Fatal(err)
	}
	inputDigest := renderConvergenceIdentityDigest("pipeline render input")
	bundleDigest := renderConvergenceIdentityDigest("projected bundle")
	promotionDigest := renderConvergenceIdentityDigest("promotion receipt")
	manifest := toolRenderCandidateManifest{
		Version:                   toolRenderCandidateManifestVersion,
		CandidateID:               candidateID,
		GenerationID:              "generation-v3",
		Chapter:                   1,
		PlanDigest:                checkpoint.Digest,
		PlanCheckpointSeq:         checkpoint.Seq,
		ProjectedBundleDigest:     bundleDigest,
		PromotionReceiptDigest:    promotionDigest,
		PipelineRenderInputDigest: inputDigest,
		RenderContextSHA256:       frozenContext.PayloadSHA256,
		SourceOutputDir:           sourceOutput,
	}
	renderConvergenceWriteManifest(t, st, manifest)
	renderConvergenceWriteJSON(t, filepath.Join(st.Dir(), filepath.FromSlash(currentFrozenPlanMarkerPath)), map[string]any{
		"version":                   "pipeline-planning.v1",
		"chapter":                   manifest.Chapter,
		"plan_digest":               manifest.PlanDigest,
		"plan_checkpoint_seq":       manifest.PlanCheckpointSeq,
		"planning_generation_id":    manifest.GenerationID,
		"render_context_sha256":     manifest.RenderContextSHA256,
		"pipeline_run_input_digest": manifest.PipelineRenderInputDigest,
		"projected_bundle_digest":   manifest.ProjectedBundleDigest,
		"promotion_receipt_digest":  manifest.PromotionReceiptDigest,
	})
	receipt, err := PublishEffectiveRenderStyleContract(st, EffectiveRenderStyleContractIdentity{
		GenerationID:              manifest.GenerationID,
		Chapter:                   manifest.Chapter,
		PlanDigest:                manifest.PlanDigest,
		PlanCheckpointSeq:         manifest.PlanCheckpointSeq,
		BaseRenderContextSHA256:   manifest.RenderContextSHA256,
		PipelineRenderInputDigest: manifest.PipelineRenderInputDigest,
		ProjectedBundleDigest:     manifest.ProjectedBundleDigest,
		PromotionReceiptDigest:    manifest.PromotionReceiptDigest,
		CandidateID:               manifest.CandidateID,
	}, "default", "")
	if err != nil {
		t.Fatal(err)
	}
	manifest.EffectiveStyleReceiptDigest = receipt.ReceiptDigest
	renderConvergenceWriteManifest(t, st, manifest)
	return st, manifest
}

func renderConvergenceWriteManifest(t *testing.T, st *store.Store, manifest toolRenderCandidateManifest) {
	t.Helper()
	renderConvergenceWriteJSON(
		t,
		filepath.Join(st.Dir(), "meta", "planning", "render_candidate.json"),
		manifest,
	)
}

func renderConvergenceWriteJSON(t *testing.T, path string, value any) {
	t.Helper()
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(raw, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

func renderConvergenceIdentityDigest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return "sha256:" + hex.EncodeToString(sum[:])
}
