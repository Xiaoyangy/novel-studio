package domain

import (
	"strings"
	"testing"
)

func TestDependencyFingerprintIsDeterministic(t *testing.T) {
	left, err := NewDependencyFingerprint("generation-1", "canon-root", []PlanningDependency{
		{Kind: "world", ID: "rules", SHA256: "bbb"},
		{Kind: "outline", ID: "book", SHA256: "aaa"},
	})
	if err != nil {
		t.Fatal(err)
	}
	right, err := NewDependencyFingerprint("generation-1", "canon-root", []PlanningDependency{
		{Kind: "outline", ID: "book", SHA256: "aaa"},
		{Kind: "world", ID: "rules", SHA256: "bbb"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if left.RootSHA256 != right.RootSHA256 {
		t.Fatalf("dependency root depends on input order: %s != %s", left.RootSHA256, right.RootSHA256)
	}
	if err := ValidateDependencyFingerprint(left); err != nil {
		t.Fatalf("fingerprint rejected: %v", err)
	}
}

func TestPlanningAuthorityCanNeverClaimCanon(t *testing.T) {
	for _, authority := range []PlanningAuthority{PlanningAuthoritySpeculative, PlanningAuthorityProjected} {
		if authority.IsCanonical() {
			t.Fatalf("%q unexpectedly claims canonical authority", authority)
		}
		if err := ValidatePlanningAuthority(authority); err != nil {
			t.Fatalf("supported authority rejected: %v", err)
		}
	}
	if err := ValidatePlanningAuthority(PlanningAuthority("canonical")); err == nil || !strings.Contains(err.Error(), "non-canonical") {
		t.Fatalf("canonical spoof was not rejected clearly: %v", err)
	}
}

func TestValidateStagedChapterPlanChainRejectsBrokenStateLink(t *testing.T) {
	fingerprint := planningTestFingerprint(t)
	first := planningTestManifest(t, fingerprint, 1, "canon-root")
	second := planningTestManifest(t, fingerprint, 2, first.ProjectedState.PostStateRoot)
	if err := ValidateStagedChapterPlanChain([]StagedChapterPlanManifest{second, first}); err != nil {
		t.Fatalf("valid chain rejected: %v", err)
	}

	broken := planningTestManifest(t, fingerprint, 2, "wrong-predecessor-root")
	err := ValidateStagedChapterPlanChain([]StagedChapterPlanManifest{first, broken})
	if err == nil || !strings.Contains(err.Error(), "does not match chapter 1 post_state_root") {
		t.Fatalf("broken chain was not rejected: %v", err)
	}
}

func TestPlanningEnvelopeRequiresGenerationCanonAndDependencies(t *testing.T) {
	fingerprint := planningTestFingerprint(t)
	manifest := planningTestManifest(t, fingerprint, 1, "canon-root")
	manifest.GenerationID = ""
	if err := ValidateStagedChapterPlanManifest(manifest); err == nil {
		t.Fatal("empty generation_id was accepted")
	}
	manifest = planningTestManifest(t, fingerprint, 1, "canon-root")
	manifest.BaseCanonRoot = ""
	if err := ValidateStagedChapterPlanManifest(manifest); err == nil {
		t.Fatal("empty base_canon_root was accepted")
	}
	manifest = planningTestManifest(t, fingerprint, 1, "canon-root")
	manifest.DependencyFingerprint.RootSHA256 = ""
	if err := ValidateStagedChapterPlanManifest(manifest); err == nil {
		t.Fatal("empty dependency fingerprint was accepted")
	}
}

func planningTestFingerprint(t *testing.T) DependencyFingerprint {
	t.Helper()
	fingerprint, err := NewDependencyFingerprint("generation-1", "canon-root", []PlanningDependency{
		{Kind: "outline", ID: "book", SHA256: "outline-sha"},
		{Kind: "world", ID: "rules", SHA256: "world-sha"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return fingerprint
}

func planningTestManifest(t *testing.T, fingerprint DependencyFingerprint, chapter int, preStateRoot string) StagedChapterPlanManifest {
	t.Helper()
	projectionRoot, err := DeterministicPlanningHash(struct {
		Chapter int `json:"chapter"`
	}{Chapter: chapter})
	if err != nil {
		t.Fatal(err)
	}
	postStateRoot, err := DeriveProjectedStateRoot(
		chapter,
		fingerprint.GenerationID,
		fingerprint.BaseCanonRoot,
		fingerprint.RootSHA256,
		preStateRoot,
		projectionRoot,
	)
	if err != nil {
		t.Fatal(err)
	}
	receipt := ProjectedStateReceipt{
		Version:        PlanningStoreVersion,
		Chapter:        chapter,
		GenerationID:   fingerprint.GenerationID,
		BaseCanonRoot:  fingerprint.BaseCanonRoot,
		DependencyRoot: fingerprint.RootSHA256,
		Authority:      PlanningAuthorityProjected,
		Realization:    PlanningRealizationStaged,
		PreStateRoot:   preStateRoot,
		ProjectionRoot: projectionRoot,
		PostStateRoot:  postStateRoot,
	}
	return StagedChapterPlanManifest{
		Version:               PlanningStoreVersion,
		Chapter:               chapter,
		Volume:                1,
		GenerationID:          fingerprint.GenerationID,
		BaseCanonChapter:      0,
		BaseCanonRoot:         fingerprint.BaseCanonRoot,
		DependencyFingerprint: fingerprint,
		Authority:             PlanningAuthoritySpeculative,
		Realization:           PlanningRealizationStaged,
		PlanPath:              "meta/planning/chapter_payloads/plan.json",
		PlanSHA256:            "plan-sha",
		ProjectedState:        receipt,
	}
}
