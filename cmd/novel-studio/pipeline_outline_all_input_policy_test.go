package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chenhongyang/novel-studio/assets"
	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/tools"
)

func completeInputCandidateFixture(t *testing.T, withReceipt bool, repair string) (bootstrap.Config, assets.Bundle, string, string) {
	t.Helper()
	cfg, bundle, source, oldPrepared := outlinePolicyCandidateFixture(t, domain.StoryContractEvidencePolicyNarrationV1, false, repair)
	// Move only this test's pre-execution fixture away from candidate discovery.
	if err := os.Rename(oldPrepared, filepath.Join(t.TempDir(), "old-prepared")); err != nil {
		t.Fatal(err)
	}
	identity, model, prompt, execution, err := pipelineOutlineAllExecutionIdentity(cfg, bundle, domain.StoryContractEvidencePolicyNarrationV1, domain.OutlineAllInputPolicyCompleteFoundationV1)
	if err != nil {
		t.Fatal(err)
	}
	if repair != "" {
		execution += "\noutline-repair=" + repair
	}
	attempt := outlineAllAttemptID(source, execution)
	candidate := pipelineOutlineAllCandidatePath(cfg.OutputDir, attempt)
	if err := preparePipelineOutlineAllCandidate(cfg.OutputDir, candidate, attempt); err != nil {
		t.Fatal(err)
	}
	if withReceipt {
		st := store.NewStore(candidate)
		if err := st.Runtime.AcquirePipelineExecution(domain.PipelineExecutionLock{Mode: domain.PipelineExecutionOutlineAll, TargetChapter: 1, PlanDigest: model, Owner: "full-input-test", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
			t.Fatal(err)
		}
		lock, err := st.Runtime.LoadPipelineExecution()
		if err != nil {
			t.Fatal(err)
		}
		compass := outlineAllGateCompass()
		target, err := domain.ResolveBookScaleTarget(compass.EstimatedScale, 1, 8)
		if err != nil {
			t.Fatal(err)
		}
		protected, err := pipelineOutlineAllProtectedCanonRoot(candidate)
		if err != nil {
			t.Fatal(err)
		}
		stable, err := pipelineOutlineAllStableProgressRoot(candidate)
		if err != nil {
			t.Fatal(err)
		}
		foundation, err := loadPipelineOutlineAllFrozenFoundation(candidate, domain.OutlineAllInputPolicyCompleteFoundationV1)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ensurePipelineOutlineAllReceipt(st, *lock, compass, target, source, protected, stable, foundation.Root, attempt, candidate, identity, model, prompt, domain.StoryContractEvidencePolicyNarrationV1, domain.OutlineAllInputPolicyCompleteFoundationV1); err != nil {
			t.Fatal(err)
		}
		if err := st.Runtime.ReleasePipelineExecution("full-input-test"); err != nil {
			t.Fatal(err)
		}
	}
	return cfg, bundle, source, candidate
}

func TestOutlineAllInputPolicyFreshAndExistingCandidateSelection(t *testing.T) {
	fresh := bootstrap.Config{OutputDir: outlineAllGateLiveDir(t), Provider: "test", ModelName: "architect"}
	selected, err := selectPipelineOutlineAllPolicies(fresh, assets.Bundle{}, outlineAllGateDigest, "")
	if err != nil || selected.Input != domain.OutlineAllInputPolicyCompleteFoundationV1 {
		t.Fatalf("fresh selection: %+v %v", selected, err)
	}
	for _, receipt := range []bool{false, true} {
		for _, repair := range []string{"", outlineAllGateDigest} {
			cfg, bundle, source, candidate := completeInputCandidateFixture(t, receipt, repair)
			before, err := store.DirectoryContentRoot(candidate)
			if err != nil {
				t.Fatal(err)
			}
			selected, err := selectPipelineOutlineAllPolicies(cfg, bundle, source, repair)
			if err != nil || selected.Input != domain.OutlineAllInputPolicyCompleteFoundationV1 || selected.ContractEvidence != domain.StoryContractEvidencePolicyNarrationV1 {
				t.Fatalf("recovery selection: %+v %v", selected, err)
			}
			after, err := store.DirectoryContentRoot(candidate)
			if err != nil || before != after {
				t.Fatal("new-policy candidate selection rewrote evidence")
			}
		}
	}
}

func TestOutlineAllInputPolicyRejectsOldAndNewAttemptAmbiguity(t *testing.T) {
	cfg, bundle, source, old := outlinePolicyCandidateFixture(t, domain.StoryContractEvidencePolicyNarrationV1, true, "")
	before, err := store.DirectoryContentRoot(old)
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, execution, err := pipelineOutlineAllExecutionIdentity(cfg, bundle, domain.StoryContractEvidencePolicyNarrationV1, domain.OutlineAllInputPolicyCompleteFoundationV1)
	if err != nil {
		t.Fatal(err)
	}
	attempt := outlineAllAttemptID(source, execution)
	if err := preparePipelineOutlineAllCandidate(cfg.OutputDir, pipelineOutlineAllCandidatePath(cfg.OutputDir, attempt), attempt); err != nil {
		t.Fatal(err)
	}
	if _, err := selectPipelineOutlineAllPolicies(cfg, bundle, source, ""); err == nil {
		t.Fatal("ambiguous old/new input attempts selected silently")
	}
	after, err := store.DirectoryContentRoot(old)
	if err != nil || before != after {
		t.Fatal("ambiguous selection rewrote old execution evidence")
	}
}

func TestOutlineAllInputPolicyCodexBindingAndLegacyRoot(t *testing.T) {
	cfg, _, _, _ := outlinePolicyCandidateFixture(t, "", false, "")
	path := filepath.Join(cfg.OutputDir, "world_codex.json")
	if err := os.WriteFile(path, []byte(`{"mechanisms":["before"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	legacy, err := loadPipelineOutlineAllFrozenFoundation(cfg.OutputDir)
	if err != nil {
		t.Fatal(err)
	}
	complete, err := loadPipelineOutlineAllFrozenFoundation(cfg.OutputDir, domain.OutlineAllInputPolicyCompleteFoundationV1)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"mechanisms":["after"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	legacyAfter, err := loadPipelineOutlineAllFrozenFoundation(cfg.OutputDir)
	if err != nil {
		t.Fatal(err)
	}
	completeAfter, err := loadPipelineOutlineAllFrozenFoundation(cfg.OutputDir, domain.OutlineAllInputPolicyCompleteFoundationV1)
	if err != nil {
		t.Fatal(err)
	}
	if legacy.Root != legacyAfter.Root || legacy.Authorities["world_codex.json"] != "" {
		t.Fatal("legacy foundation root/wire acquired a new source")
	}
	if complete.Root == completeAfter.Root || completeAfter.Authorities["world_codex.json"] != `{"mechanisms":["after"]}` {
		t.Fatal("new complete input did not bind full codex bytes")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	missing, err := loadPipelineOutlineAllFrozenFoundation(cfg.OutputDir, domain.OutlineAllInputPolicyCompleteFoundationV1)
	if err != nil || missing.Authorities["world_codex.json"] != "" {
		t.Fatalf("missing historical codex fabricated or rejected: %v", err)
	}
	if err := os.Symlink(filepath.Join(cfg.OutputDir, "absent-codex"), path); err != nil {
		t.Fatal(err)
	}
	if _, err := loadPipelineOutlineAllFrozenFoundation(cfg.OutputDir, domain.OutlineAllInputPolicyCompleteFoundationV1); err == nil {
		t.Fatal("existing unreadable codex treated as missing")
	}
}

func TestOutlineAllInputPolicyPreservesLongAuthoritiesAndRejectsOversize(t *testing.T) {
	digest, err := domain.ComputeLayeredOutlineDigest(nil)
	if err != nil {
		t.Fatal(err)
	}
	action := domain.OutlineAllPendingAction{Type: domain.OutlineAllActionPlanStructure, Operation: 1, BeforeLayeredDigest: digest}
	author := `{"original":"` + strings.Repeat("author-original-", 1400) + `TAIL-AUTHOR"}`
	foundation := pipelineOutlineAllFrozenFoundation{Root: "sha256:fixture", Authorities: map[string]string{store.AuthorSourcesPath: author}}
	view, _, _, err := buildPipelineOutlineAllModelVisibleContext(nil, domain.StoryCompass{}, domain.BookScaleTarget{}, action, foundation, tools.References{}, "", domain.OutlineAllInputPolicyCompleteFoundationV1)
	if err != nil || view.Foundation.Authorities[store.AuthorSourcesPath].Text != author || view.Foundation.Authorities[store.AuthorSourcesPath].Truncated {
		t.Fatalf("long original authority lost: %v", err)
	}
	foundation.Premise = strings.Repeat("x", pipelineOutlineAllCompleteContextMaxBytes)
	if _, _, _, err := buildPipelineOutlineAllModelVisibleContext(nil, domain.StoryCompass{}, domain.BookScaleTarget{}, action, foundation, tools.References{}, "", domain.OutlineAllInputPolicyCompleteFoundationV1); err == nil {
		t.Fatal("oversized source was silently cut instead of rejected")
	}
}
