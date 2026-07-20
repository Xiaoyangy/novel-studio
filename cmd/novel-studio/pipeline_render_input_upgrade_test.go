package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderInputUpgradeReceiptBindsOldAndNewCandidateIdentity(t *testing.T) {
	dir := t.TempDir()
	oldDigest := "sha256:" + strings.Repeat("1", 64)
	newDigest := "sha256:" + strings.Repeat("2", 64)
	frozen := pipelineRenderInputUpgradeTestFrozen(oldDigest)
	oldCandidate, err := pipelineRenderTransactionID(&frozen)
	if err != nil {
		t.Fatal(err)
	}
	upgraded := frozen
	upgraded.PipelineRunInputDigest = newDigest
	newCandidate, err := pipelineRenderTransactionID(&upgraded)
	if err != nil {
		t.Fatal(err)
	}
	if oldCandidate == newCandidate {
		t.Fatal("render input upgrade did not create a fresh candidate identity")
	}
	identity := pipelineRenderInputUpgradeIdentity{
		Version:                pipelineRenderInputUpgradeVersion,
		Chapter:                frozen.Chapter,
		PlanDigest:             frozen.PlanDigest,
		PlanCheckpointSeq:      frozen.PlanCheckpointSeq,
		ProjectionBinding:      frozen.ProjectionBinding,
		RenderContextSHA256:    frozen.RenderContextSHA256,
		ProjectedBundleDigest:  frozen.ProjectedBundleDigest,
		PromotionReceiptDigest: frozen.PromotionReceiptDigest,
		PreviousInputDigest:    oldDigest,
		CurrentInputDigest:     newDigest,
	}
	upgradeID := pipelineRenderInputUpgradeID(identity)
	path := pipelineRenderInputUpgradeReceiptPath(dir, upgradeID)
	receipt, err := loadOrCreatePipelineRenderInputUpgradeReceipt(
		path, identity, upgradeID, oldCandidate, newCandidate,
	)
	if err != nil {
		t.Fatal(err)
	}
	upgraded.RenderInputUpgradeID = receipt.UpgradeID
	upgraded.RenderInputUpgradeReceiptDigest = receipt.ReceiptDigest
	if err := validatePipelineRenderInputUpgradeReceipt(dir, &upgraded); err != nil {
		t.Fatalf("valid upgrade receipt was rejected: %v", err)
	}

	reused, err := loadOrCreatePipelineRenderInputUpgradeReceipt(
		path, identity, upgradeID, oldCandidate, newCandidate,
	)
	if err != nil || reused.ReceiptDigest != receipt.ReceiptDigest || reused.UpgradedAt != receipt.UpgradedAt {
		t.Fatalf("crash-safe receipt reuse failed: receipt=%+v err=%v", reused, err)
	}
	tampered := receipt
	tampered.CurrentCandidateID += "-tampered"
	if err := validatePipelineRenderInputUpgradeReceiptValue(
		tampered, identity, upgradeID, oldCandidate, newCandidate,
	); err == nil {
		t.Fatal("tampered render input upgrade receipt was accepted")
	}
}

func TestRenderInputUpgradeRefusesAnyPriorCandidateEvidence(t *testing.T) {
	outputDir := filepath.Join(t.TempDir(), "output", "novel")
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		t.Fatal(err)
	}
	candidateID := "render-ch0005-0123456789abcdef01234567"
	if err := pipelineRequireNoRenderInputUpgradeAttempt(outputDir, candidateID, 5); err != nil {
		t.Fatalf("fresh candidate was rejected: %v", err)
	}
	evidenceDir := filepath.Join(pipelineRenderCandidateRoot(outputDir), "convergence", candidateID)
	if err := os.MkdirAll(evidenceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := pipelineRequireNoRenderInputUpgradeAttempt(outputDir, candidateID, 5); err == nil {
		t.Fatal("candidate with convergence evidence was allowed to rebind")
	}
}

func TestRenderInputUpgradeRejectsTraversalAndRealDraftArtifacts(t *testing.T) {
	dir := t.TempDir()
	frozen := pipelineRenderInputUpgradeTestFrozen("sha256:" + strings.Repeat("2", 64))
	frozen.RenderInputUpgradeID = "../render-input-upgrade-" + strings.Repeat("a", 24)
	frozen.RenderInputUpgradeReceiptDigest = "sha256:" + strings.Repeat("b", 64)
	if err := validatePipelineRenderInputUpgradeReceipt(dir, &frozen); err == nil {
		t.Fatal("path-traversing render input upgrade id was accepted")
	}

	outputDir := filepath.Join(t.TempDir(), "output", "novel")
	if err := os.MkdirAll(filepath.Join(outputDir, "drafts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outputDir, "drafts", "05.draft.md"), []byte("started"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := pipelineRequireNoRenderInputUpgradeAttempt(
		outputDir,
		"render-ch0005-0123456789abcdef01234567",
		5,
	); err == nil {
		t.Fatal("existing drafts/05.draft.md was allowed to rebind")
	}
}

func TestPipelineParsesExplicitRenderInputUpgradeFlag(t *testing.T) {
	flags, extra, err := parsePipelineFlags([]string{
		"--stages", "render", "--refresh-render-input",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !flags.RefreshRenderInput || len(extra) != 0 {
		t.Fatalf("explicit render input upgrade flag was not parsed: flags=%+v extra=%v", flags, extra)
	}
}

func pipelineRenderInputUpgradeTestFrozen(inputDigest string) pipelineFrozenPlan {
	return pipelineFrozenPlan{
		Version:                pipelinePlanningSchema,
		Chapter:                5,
		PlanPath:               "drafts/05.plan.json",
		PlanDigest:             "sha256:" + strings.Repeat("3", 64),
		PlanCheckpointSeq:      140,
		PipelineRunInputDigest: inputDigest,
		RenderContextPath:      "meta/planning/current_render_context.json",
		RenderContextSHA256:    "sha256:" + strings.Repeat("4", 64),
		PlanningGenerationID:   "pg2_test",
		PlanningDependencyRoot: "sha256:" + strings.Repeat("5", 64),
		ProjectionBinding:      "sealed_v2",
		ProjectedPlanSHA256:    "sha256:" + strings.Repeat("6", 64),
		ProjectedPreStateRoot:  "sha256:" + strings.Repeat("7", 64),
		ProjectedPostStateRoot: "sha256:" + strings.Repeat("8", 64),
		ProjectedBundleDigest:  "sha256:" + strings.Repeat("9", 64),
		PromotionReceiptDigest: "sha256:" + strings.Repeat("a", 64),
	}
}
