package domain

import (
	"strings"
	"testing"
	"time"
)

func validOutlineAllReceiptForTest(t *testing.T) OutlineAllExecutionReceipt {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Nanosecond)
	receipt := OutlineAllExecutionReceipt{
		Version:                  OutlineAllExecutionReceiptVersion,
		Mode:                     OutlineAllExecutionMode,
		Status:                   OutlineAllExecutionBuilding,
		BaseCanonChapter:         0,
		GenerationID:             "planning-generation",
		WritingMode:              WritingPipelineModeSealedTwoPassV2,
		WritingModeReceiptDigest: PlanningV2DigestPrefix + strings.Repeat("1", 64),
		CompassDigest:            PlanningV2DigestPrefix + strings.Repeat("2", 64),
		EstimatedScale:           "8-10 volumes, 360-480 chapters",
		EndingDirection:          "The terminal social contract is fulfilled.",
		NonNegotiables:           []string{"resolve the founding promise"},
		MinVolumes:               8,
		MaxVolumes:               10,
		MinChapters:              360,
		MaxChapters:              480,
		TargetVolumes:            8,
		TargetChapters:           400,
		TargetWords:              1_040_000,
		TargetWordsPerChapter:    2600,
		StoryTimeHint:            "about four story years",
		SourceSnapshotRoot:       PlanningV2DigestPrefix + strings.Repeat("3", 64),
		ProtectedCanonRoot:       PlanningV2DigestPrefix + strings.Repeat("4", 64),
		StableProgressRoot:       PlanningV2DigestPrefix + strings.Repeat("7", 64),
		FoundationContextRoot:    PlanningV2DigestPrefix + strings.Repeat("8", 64),
		AttemptID:                "outline-all-domain-test",
		CandidateDir:             "/tmp/outline-all-domain-test",
		CoordinatorProvider:      "provider-a",
		CoordinatorModel:         "coordinator-model",
		CoordinatorReasoning:     "high",
		ArchitectProvider:        "provider-b",
		ArchitectModel:           "architect-model",
		ArchitectReasoning:       "high",
		PromptProtocolDigest:     PlanningV2DigestPrefix + strings.Repeat("5", 64),
		LockVersion:              1,
		LockMode:                 PipelineExecutionOutlineAll,
		LockTargetChapter:        1,
		LockOwner:                "outline-all-test",
		LockProcessID:            42,
		LockAcquiredAt:           now,
		LockExpiresAt:            now.Add(time.Hour),
		PendingAction: &OutlineAllPendingAction{
			Type:                OutlineAllActionAppendVolume,
			Operation:           1,
			Volume:              3,
			ExpectedVolumeIndex: 3,
			ExpectedChapterSpan: 48,
			ExpectedArcSpans:    "16,16,16",
			BeforeLayeredDigest: PlanningV2DigestPrefix + strings.Repeat("6", 64),
		},
		StartedAt: now,
		UpdatedAt: now,
	}
	receipt.ModelIdentityDigest, _ = ComputeOutlineAllModelIdentityDigest(receipt.ModelIdentity())
	signed, err := SignOutlineAllExecutionReceipt(receipt)
	if err != nil {
		t.Fatal(err)
	}
	return signed
}

func TestOutlineAllExecutionReceiptDigestRejectsTampering(t *testing.T) {
	receipt := validOutlineAllReceiptForTest(t)
	if err := ValidateOutlineAllExecutionReceipt(receipt); err != nil {
		t.Fatalf("valid receipt: %v", err)
	}
	receipt.MaxChapters++
	if err := ValidateOutlineAllExecutionReceipt(receipt); err == nil ||
		!strings.Contains(err.Error(), "digest mismatch") {
		t.Fatalf("tampered bounds must invalidate receipt: %v", err)
	}
}

func TestOutlineAllExecutionReceiptRequiresBoundedSingleAction(t *testing.T) {
	receipt := validOutlineAllReceiptForTest(t)
	receipt.PendingAction = &OutlineAllPendingAction{
		Type:                OutlineAllActionExpandArc,
		Operation:           2,
		Volume:              2,
		Arc:                 4,
		ExpectedChapterSpan: 12,
		BeforeLayeredDigest: PlanningV2DigestPrefix + strings.Repeat("7", 64),
	}
	receipt, err := SignOutlineAllExecutionReceipt(receipt)
	if err != nil {
		t.Fatalf("sign expand action: %v", err)
	}
	if err := ValidateOutlineAllExecutionReceipt(receipt); err != nil {
		t.Fatal(err)
	}

	receipt.Status = OutlineAllExecutionComplete
	receipt.FinalLayeredDigest = PlanningV2DigestPrefix + strings.Repeat("8", 64)
	receipt.FinalFlatDigest = PlanningV2DigestPrefix + strings.Repeat("9", 64)
	if _, err := SignOutlineAllExecutionReceipt(receipt); err == nil ||
		!strings.Contains(err.Error(), "cannot retain a pending action") {
		t.Fatalf("complete receipt retained capability: %v", err)
	}
}

func TestOutlineAllIntentRoundTripIsExact(t *testing.T) {
	action := OutlineAllPendingAction{
		Type:                OutlineAllActionExpandArc,
		Operation:           3,
		Volume:              4,
		Arc:                 2,
		ExpectedChapterSpan: 14,
		BeforeLayeredDigest: PlanningV2DigestPrefix + strings.Repeat("a", 64),
	}
	marker, err := FormatOutlineAllIntent(action)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseOutlineAllIntent("single mutation only\n" + marker)
	if err != nil {
		t.Fatal(err)
	}
	if parsed == nil || !OutlineAllPendingActionEqual(*parsed, action) {
		t.Fatalf("parsed action=%+v want=%+v", parsed, action)
	}
	if _, err := ParseOutlineAllIntent(marker + "\n" + marker); err == nil {
		t.Fatal("multiple capability markers must be rejected")
	}
}

func TestOutlineAllExecutionLockBindingIsExact(t *testing.T) {
	receipt := validOutlineAllReceiptForTest(t)
	lock := PipelineExecutionLock{
		Version:       receipt.LockVersion,
		Mode:          receipt.LockMode,
		TargetChapter: receipt.LockTargetChapter,
		PlanDigest:    receipt.LockPlanDigest,
		Owner:         receipt.LockOwner,
		ProcessID:     receipt.LockProcessID,
		AcquiredAt:    receipt.LockAcquiredAt,
		ExpiresAt:     receipt.LockExpiresAt,
	}
	if err := ValidateOutlineAllExecutionLockBinding(receipt, lock); err != nil {
		t.Fatal(err)
	}
	lock.ExpiresAt = lock.ExpiresAt.Add(time.Second)
	if err := ValidateOutlineAllExecutionLockBinding(receipt, lock); err == nil {
		t.Fatal("a refreshed/replaced lease must require an explicit receipt rebind")
	}
}

func TestValidateOutlineAllChapterZeroProgress(t *testing.T) {
	receipt := validOutlineAllReceiptForTest(t)
	progress := &Progress{GenerationID: receipt.GenerationID, TotalChapters: 128}
	if err := ValidateOutlineAllChapterZeroProgress(progress, receipt); err != nil {
		t.Fatal(err)
	}
	progress.CompletedChapters = []int{1}
	if err := ValidateOutlineAllChapterZeroProgress(progress, receipt); err == nil {
		t.Fatal("completed canon must close the chapter-zero execution window")
	}
}
