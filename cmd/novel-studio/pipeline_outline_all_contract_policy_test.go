package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/chenhongyang/novel-studio/assets"
	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func outlinePolicyCandidateFixture(t *testing.T, policy string, withReceipt bool, repairDigest string) (bootstrap.Config, assets.Bundle, string, string) {
	t.Helper()
	live := outlineAllGateLiveDir(t)
	writeOutlineAllGateCompleteReceipt(t, live, pipelineOutlineAllCandidatePath(live, outlineAllGateAttemptID()), outlineAllGateDigest)
	// This fixture starts before outline-all. Retain the actual foundation and
	// mode, not the helper's unrelated synthetic completed receipt.
	if err := os.Remove(filepath.Join(live, store.OutlineAllExecutionReceiptPath)); err != nil {
		t.Fatal(err)
	}
	if err := ensurePipelineOutlineAllRequirement(live); err != nil {
		t.Fatal(err)
	}
	cfg := bootstrap.Config{OutputDir: live, Provider: "test", ModelName: "architect"}
	bundle := assets.Bundle{Prompts: assets.Prompts{ArchitectLong: "frozen architect test prompt"}}
	sourceRoot, err := pipelineOutlineAllSourceSnapshotRoot(live)
	if err != nil {
		t.Fatal(err)
	}
	identity, modelDigest, promptDigest, execution, err := pipelineOutlineAllExecutionIdentity(cfg, bundle, policy)
	if err != nil {
		t.Fatal(err)
	}
	if repairDigest != "" {
		execution += "\noutline-repair=" + repairDigest
	}
	attempt := outlineAllAttemptID(sourceRoot, execution)
	candidate := pipelineOutlineAllCandidatePath(live, attempt)
	if err := preparePipelineOutlineAllCandidate(live, candidate, attempt); err != nil {
		t.Fatal(err)
	}
	if withReceipt {
		st := store.NewStore(candidate)
		if err := st.Runtime.AcquirePipelineExecution(domain.PipelineExecutionLock{Mode: domain.PipelineExecutionOutlineAll, TargetChapter: 1, PlanDigest: modelDigest, Owner: "policy-resume-test", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
			t.Fatal(err)
		}
		lock, err := st.Runtime.LoadPipelineExecution()
		if err != nil || lock == nil {
			t.Fatalf("lock: %v", err)
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
		foundation, err := loadPipelineOutlineAllFrozenFoundation(candidate)
		if err != nil {
			t.Fatal(err)
		}
		receipt, err := ensurePipelineOutlineAllReceipt(st, *lock, compass, target, sourceRoot, protected, stable, foundation.Root, attempt, candidate, identity, modelDigest, promptDigest, policy)
		if err != nil {
			t.Fatal(err)
		}
		if receipt.ContractEvidencePolicy != policy || receipt.PromptProtocolDigest != promptDigest {
			t.Fatal("host did not freeze chosen policy/prompt")
		}
		if err := st.Runtime.ReleasePipelineExecution("policy-resume-test"); err != nil {
			t.Fatal(err)
		}
	}
	return cfg, bundle, sourceRoot, candidate
}

func TestOutlineAllContractPolicyFreshDefaultAndPromptBinding(t *testing.T) {
	cfg := bootstrap.Config{OutputDir: outlineAllGateLiveDir(t), Provider: "test", ModelName: "architect"}
	bundle := assets.Bundle{Prompts: assets.Prompts{ArchitectLong: "frozen architect test prompt"}}
	policy, err := selectPipelineOutlineAllContractPolicy(cfg, bundle, outlineAllGateDigest, "")
	if err != nil || policy != domain.StoryContractEvidencePolicyNarrationV1 {
		t.Fatalf("fresh policy=%q err=%v", policy, err)
	}
	_, oldModel, oldPrompt, oldIdentity, err := pipelineOutlineAllExecutionIdentity(cfg, bundle)
	if err != nil {
		t.Fatal(err)
	}
	_, newModel, newPrompt, newIdentity, err := pipelineOutlineAllExecutionIdentity(cfg, bundle, policy)
	if err != nil {
		t.Fatal(err)
	}
	if oldModel != newModel || oldPrompt == newPrompt || oldIdentity == newIdentity {
		t.Fatal("policy must change prompt identity without changing model identity")
	}
}

func TestOutlineAllContractPolicyResumesExactPreparedAndUnfinishedAttempt(t *testing.T) {
	for _, policy := range []string{"", domain.StoryContractEvidencePolicyNarrationV1} {
		for _, withReceipt := range []bool{false, true} {
			for _, repair := range []string{"", outlineAllGateDigest} {
				t.Run(policy+"/receipt="+map[bool]string{false: "false", true: "true"}[withReceipt]+"/repair="+repair, func(t *testing.T) {
					cfg, bundle, source, candidate := outlinePolicyCandidateFixture(t, policy, withReceipt, repair)
					before, err := store.DirectoryContentRoot(candidate)
					if err != nil {
						t.Fatal(err)
					}
					selected, err := selectPipelineOutlineAllContractPolicy(cfg, bundle, source, repair)
					if err != nil || selected != policy {
						t.Fatalf("resume selected policy %q instead of %q: %v", selected, policy, err)
					}
					_, _, _, execution, err := pipelineOutlineAllExecutionIdentity(cfg, bundle, selected)
					if err != nil {
						t.Fatal(err)
					}
					if repair != "" {
						execution += "\noutline-repair=" + repair
					}
					if got := pipelineOutlineAllCandidatePath(cfg.OutputDir, outlineAllAttemptID(source, execution)); got != candidate {
						t.Fatalf("resume selected a different attempt: %s", got)
					}
					if after, err := store.DirectoryContentRoot(candidate); err != nil || after != before {
						t.Fatalf("policy selection rewrote prior candidate: %v", err)
					}
				})
			}
		}
	}
}

func TestOutlineAllContractPolicyPublishedLegacyVerifiesWithoutResigning(t *testing.T) {
	live, st := publishOutlineAllGateFixture(t, true)
	path := filepath.Join(live, store.OutlineAllExecutionReceiptPath)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := st.LoadOutlineAllExecutionReceipt()
	if err != nil || receipt == nil || receipt.ContractEvidencePolicy != "" {
		t.Fatalf("not a legacy receipt: %v", err)
	}
	for i := 0; i < 2; i++ {
		if _, err := verifyPipelineOutlineAllReceiptAndArtifacts(live); err != nil {
			t.Fatalf("legacy published verification failed: %v", err)
		}
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("legacy verification re-signed historical receipt: %v", err)
	}
}

func TestOutlineAllContractPolicyRejectsAmbiguousAttempts(t *testing.T) {
	cfg, bundle, source, _ := outlinePolicyCandidateFixture(t, "", true, "")
	_, _, _, execution, err := pipelineOutlineAllExecutionIdentity(cfg, bundle, domain.StoryContractEvidencePolicyNarrationV1)
	if err != nil {
		t.Fatal(err)
	}
	attempt := outlineAllAttemptID(source, execution)
	if err := preparePipelineOutlineAllCandidate(cfg.OutputDir, pipelineOutlineAllCandidatePath(cfg.OutputDir, attempt), attempt); err != nil {
		t.Fatal(err)
	}
	if _, err := selectPipelineOutlineAllContractPolicy(cfg, bundle, source, ""); err == nil {
		t.Fatal("ambiguous old/new attempts were silently selected")
	}
}

func TestOutlineAllContractPolicyAllowsExistingInterruptedCopyRecovery(t *testing.T) {
	for _, policy := range []string{"", domain.StoryContractEvidencePolicyNarrationV1} {
		t.Run(policy, func(t *testing.T) {
			cfg := bootstrap.Config{OutputDir: outlineAllGateLiveDir(t), Provider: "test", ModelName: "architect"}
			writeOutlineAllGateCompleteReceipt(t, cfg.OutputDir, pipelineOutlineAllCandidatePath(cfg.OutputDir, outlineAllGateAttemptID()), outlineAllGateDigest)
			if err := os.Remove(filepath.Join(cfg.OutputDir, store.OutlineAllExecutionReceiptPath)); err != nil {
				t.Fatal(err)
			}
			if err := ensurePipelineOutlineAllRequirement(cfg.OutputDir); err != nil {
				t.Fatal(err)
			}
			bundle := assets.Bundle{}
			source, err := pipelineOutlineAllSourceSnapshotRoot(cfg.OutputDir)
			if err != nil {
				t.Fatal(err)
			}
			_, _, _, identity, err := pipelineOutlineAllExecutionIdentity(cfg, bundle, policy)
			if err != nil {
				t.Fatal(err)
			}
			attempt := outlineAllAttemptID(source, identity)
			candidate := pipelineOutlineAllCandidatePath(cfg.OutputDir, attempt)
			if err := os.MkdirAll(candidate, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(candidate, "partial-copy.txt"), []byte("interrupted copy"), 0o644); err != nil {
				t.Fatal(err)
			}
			selected, err := selectPipelineOutlineAllContractPolicy(cfg, bundle, source, "")
			if err != nil || selected != policy {
				t.Fatalf("policy selection blocked existing interrupted-copy recovery: policy=%q err=%v", selected, err)
			}
			if err := preparePipelineOutlineAllCandidate(cfg.OutputDir, candidate, attempt); err != nil {
				t.Fatal(err)
			}
			if err := validatePipelineOutlineAllCandidatePrepareReceipt(cfg.OutputDir, candidate, attempt); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(candidate, "partial-copy.txt")); !os.IsNotExist(err) {
				t.Fatalf("interrupted copy was not rebuilt: %v", err)
			}
		})
	}
}

func TestOutlineAllContractPolicyNeverRebuildsStartedCandidateMissingPrepareReceipt(t *testing.T) {
	cfg, bundle, source, candidate := outlinePolicyCandidateFixture(t, "", true, "")
	if err := os.Remove(filepath.Join(candidate, "meta/planning/outline_all_candidate_prepare.json")); err != nil {
		t.Fatal(err)
	}
	before, err := store.DirectoryContentRoot(candidate)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := selectPipelineOutlineAllContractPolicy(cfg, bundle, source, ""); err == nil {
		t.Fatal("started candidate with missing prepare proof was treated as disposable interrupted copy")
	}
	if after, err := store.DirectoryContentRoot(candidate); err != nil || before != after {
		t.Fatalf("rejected recovery changed execution evidence: %v", err)
	}
}
