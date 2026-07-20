package domain

import (
	"strings"
	"testing"
	"time"
)

func chapterRenderTestDigest(seed string) string {
	return ComputeArcArtifactSHA256([]byte(seed))
}

func chapterRenderTestPlanIdentity() ChapterRenderPlanIdentity {
	return ChapterRenderPlanIdentity{
		Version:                ChapterRenderPlanIdentityVersion,
		ProtocolVersion:        "sealed-chapter-render.v1",
		GenerationID:           "pg2_generation-1",
		Chapter:                4,
		PlanDigest:             chapterRenderTestDigest("plan"),
		PlanCheckpointSeq:      12,
		ProjectedBundleDigest:  chapterRenderTestDigest("bundle"),
		PromotionReceiptDigest: chapterRenderTestDigest("promotion"),
		PipelineRunInputDigest: chapterRenderTestDigest("run-input"),
		RenderContextSHA256:    chapterRenderTestDigest("render-context"),
	}
}

func TestChapterRenderIdentityHasTwoContentAddressedLevels(t *testing.T) {
	plan := chapterRenderTestPlanIdentity()
	firstPlanID, err := ComputeChapterRenderPlanAttemptID(plan)
	if err != nil {
		t.Fatal(err)
	}
	secondPlanID, err := ComputeChapterRenderPlanAttemptID(plan)
	if err != nil || secondPlanID != firstPlanID {
		t.Fatalf("plan address is not deterministic: first=%q second=%q err=%v", firstPlanID, secondPlanID, err)
	}
	if !strings.HasPrefix(firstPlanID, "render-plan-") || len(firstPlanID) != len("render-plan-")+64 {
		t.Fatalf("unexpected plan address %q", firstPlanID)
	}

	bodyA := chapterRenderTestDigest("body-a")
	identityA, err := NewChapterRenderBodyIdentity(plan, bodyA)
	if err != nil {
		t.Fatal(err)
	}
	identityAAgain, err := NewChapterRenderBodyIdentity(plan, bodyA)
	if err != nil || identityAAgain != identityA {
		t.Fatalf("body address is not deterministic: first=%+v second=%+v err=%v", identityA, identityAAgain, err)
	}
	identityB, err := NewChapterRenderBodyIdentity(plan, chapterRenderTestDigest("body-b"))
	if err != nil {
		t.Fatal(err)
	}
	if identityA.PlanAttemptID != identityB.PlanAttemptID || identityA.TransactionID == identityB.TransactionID {
		t.Fatalf("same plan/different body did not preserve two-level identity: A=%+v B=%+v", identityA, identityB)
	}

	changedProtocol := plan
	changedProtocol.ProtocolVersion = "sealed-chapter-render.v2"
	identityProtocol, err := NewChapterRenderBodyIdentity(changedProtocol, bodyA)
	if err != nil {
		t.Fatal(err)
	}
	if identityProtocol.PlanAttemptID == identityA.PlanAttemptID || identityProtocol.TransactionID == identityA.TransactionID {
		t.Fatal("protocol drift did not invalidate both content addresses")
	}
	changedContext := plan
	changedContext.RenderContextSHA256 = chapterRenderTestDigest("other-context")
	identityContext, err := NewChapterRenderBodyIdentity(changedContext, bodyA)
	if err != nil {
		t.Fatal(err)
	}
	if identityContext.PlanAttemptID == identityA.PlanAttemptID {
		t.Fatal("render context drift did not invalidate plan address")
	}

	tampered := identityA
	tampered.TransactionID = identityB.TransactionID
	if err := ValidateChapterRenderBodyIdentity(tampered); err == nil || !strings.Contains(err.Error(), "transaction_id mismatch") {
		t.Fatalf("tampered body address validated: %v", err)
	}
}

func TestChapterRenderPhaseTransitionsAreFixedAndMonotonic(t *testing.T) {
	acceptedPath := []ChapterRenderPhase{
		ChapterRenderPhaseBodyReady,
		ChapterRenderPhaseCommitted,
		ChapterRenderPhaseFormalAccepted,
		ChapterRenderPhaseActualMatched,
		ChapterRenderPhasePublished,
		ChapterRenderPhaseOutcomeAccepted,
		ChapterRenderPhaseChapterAccepted,
		ChapterRenderPhaseCompleted,
	}
	previous := ChapterRenderPhase("")
	for _, next := range acceptedPath {
		if err := ValidateChapterRenderPhaseTransition(previous, next); err != nil {
			t.Fatalf("valid transition %q -> %q failed: %v", previous, next, err)
		}
		previous = next
	}
	if err := ValidateChapterRenderPhaseTransition(ChapterRenderPhaseBodyReady, ChapterRenderPhaseFormalAccepted); err == nil {
		t.Fatal("phase skipping was accepted")
	}
	if err := ValidateChapterRenderPhaseTransition(ChapterRenderPhaseCommitted, ChapterRenderPhaseBodyReady); err == nil {
		t.Fatal("phase rollback was accepted")
	}
	if err := ValidateChapterRenderPhaseTransition(ChapterRenderPhaseCompleted, ChapterRenderPhaseCompleted); err == nil {
		t.Fatal("terminal phase advanced")
	}
	if err := ValidateChapterRenderPhaseTransition(ChapterRenderPhaseBodyReady, ChapterRenderPhaseStructurallyBlocked); err != nil {
		t.Fatalf("body structural terminal transition failed: %v", err)
	}
	if err := ValidateChapterRenderPhaseTransition(ChapterRenderPhaseCommitted, ChapterRenderPhaseStructurallyBlocked); err != nil {
		t.Fatalf("committed structural terminal transition failed: %v", err)
	}
	if err := ValidateChapterRenderPhaseTransition(ChapterRenderPhaseCommitted, ChapterRenderPhaseFormalRejected); err != nil {
		t.Fatalf("formal rejection transition failed: %v", err)
	}
	if err := ValidateChapterRenderPhaseTransition(ChapterRenderPhaseFormalRejected, ChapterRenderPhaseActualMatched); err == nil {
		t.Fatal("formal rejection advanced directly to actual match")
	}
	if err := ValidateChapterRenderPhaseTransition(ChapterRenderPhaseFormalRejected, ChapterRenderPhaseFormalAccepted); err != nil {
		t.Fatalf("same-body formal revalidation transition failed: %v", err)
	}
	if err := ValidateChapterRenderPhaseTransition(ChapterRenderPhaseFormalAccepted, ChapterRenderPhaseActualMatched); err != nil {
		t.Fatalf("revalidated accept did not enable actual match: %v", err)
	}
}

func TestChapterRenderPhaseReceiptBindsDigestChainAndEvidence(t *testing.T) {
	plan := chapterRenderTestPlanIdentity()
	body := []byte("# 第四章\n\n她把最后一张回执压在灯下。")
	identity, err := NewChapterRenderBodyIdentity(plan, ComputeChapterRenderBodySHA256(body))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	bodyReceipt, err := SignChapterRenderPhaseReceipt(ChapterRenderPhaseReceipt{
		Version:   ChapterRenderPhaseReceiptVersion,
		Identity:  identity,
		Phase:     ChapterRenderPhaseBodyReady,
		Evidence:  ChapterRenderPhaseEvidence{BodyCheckpointSeq: 13, BodyCheckpointDigest: identity.BodySHA256},
		CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	commitReceipt, err := SignChapterRenderPhaseReceipt(ChapterRenderPhaseReceipt{
		Version:               ChapterRenderPhaseReceiptVersion,
		Identity:              identity,
		Phase:                 ChapterRenderPhaseCommitted,
		PreviousReceiptDigest: bodyReceipt.ReceiptDigest,
		Evidence: ChapterRenderPhaseEvidence{
			CommitCheckpointSeq: 14,
			CommitDigest:        identity.BodySHA256,
			CandidateRoot:       chapterRenderTestDigest("candidate-root"),
		},
		CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateChapterRenderPhaseReceipt(commitReceipt); err != nil {
		t.Fatal(err)
	}

	tampered := commitReceipt
	tampered.Evidence.CandidateRoot = chapterRenderTestDigest("tampered-root")
	if err := ValidateChapterRenderPhaseReceipt(tampered); err == nil || !strings.Contains(err.Error(), "receipt_digest mismatch") {
		t.Fatalf("tampered phase evidence validated: %v", err)
	}
	missingPrevious := commitReceipt
	missingPrevious.PreviousReceiptDigest = ""
	missingPrevious.ReceiptDigest, _ = ComputeChapterRenderPhaseReceiptDigest(missingPrevious)
	if err := ValidateChapterRenderPhaseReceipt(missingPrevious); err == nil || !strings.Contains(err.Error(), "previous_receipt_digest") {
		t.Fatalf("missing predecessor validated: %v", err)
	}
}
