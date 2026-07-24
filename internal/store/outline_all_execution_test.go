package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

func storeOutlineAllReceiptForTest(t *testing.T) domain.OutlineAllExecutionReceipt {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Nanosecond)
	receipt := domain.OutlineAllExecutionReceipt{
		Version:                  domain.OutlineAllExecutionReceiptVersion,
		Mode:                     domain.OutlineAllExecutionMode,
		Status:                   domain.OutlineAllExecutionBuilding,
		BaseCanonChapter:         0,
		GenerationID:             "store-planning-generation",
		WritingMode:              domain.WritingPipelineModeSealedTwoPassV2,
		WritingModeReceiptDigest: domain.PlanningV2DigestPrefix + strings.Repeat("a", 64),
		CompassDigest:            domain.PlanningV2DigestPrefix + strings.Repeat("b", 64),
		EstimatedScale:           "4-6 volumes, 80-120 chapters",
		EndingDirection:          "Close the central promise.",
		NonNegotiables:           []string{"settle the public obligation"},
		MinVolumes:               4,
		MaxVolumes:               6,
		MinChapters:              80,
		MaxChapters:              120,
		TargetVolumes:            5,
		TargetChapters:           100,
		TargetWords:              250_000,
		TargetWordsPerChapter:    2500,
		StoryTimeHint:            "one story year",
		SourceSnapshotRoot:       domain.PlanningV2DigestPrefix + strings.Repeat("c", 64),
		ProtectedCanonRoot:       domain.PlanningV2DigestPrefix + strings.Repeat("d", 64),
		StableProgressRoot:       domain.PlanningV2DigestPrefix + strings.Repeat("1", 64),
		FoundationContextRoot:    domain.PlanningV2DigestPrefix + strings.Repeat("2", 64),
		AttemptID:                "store-outline-all-test",
		CandidateDir:             "/tmp/store-outline-all-test",
		CoordinatorProvider:      "provider-a",
		CoordinatorModel:         "coordinator",
		CoordinatorReasoning:     "high",
		ArchitectProvider:        "provider-b",
		ArchitectModel:           "architect",
		ArchitectReasoning:       "high",
		PromptProtocolDigest:     domain.PlanningV2DigestPrefix + strings.Repeat("e", 64),
		LockVersion:              1,
		LockMode:                 domain.PipelineExecutionOutlineAll,
		LockTargetChapter:        1,
		LockOwner:                "store-outline-all-test",
		LockProcessID:            7,
		LockAcquiredAt:           now,
		LockExpiresAt:            now.Add(time.Hour),
		PendingAction: &domain.OutlineAllPendingAction{
			Type:                domain.OutlineAllActionExpandArc,
			Operation:           1,
			Volume:              1,
			Arc:                 2,
			ExpectedChapterSpan: 12,
			BeforeLayeredDigest: domain.PlanningV2DigestPrefix + strings.Repeat("f", 64),
		},
		StartedAt: now,
		UpdatedAt: now,
	}
	receipt.ModelIdentityDigest, _ = domain.ComputeOutlineAllModelIdentityDigest(receipt.ModelIdentity())
	signed, err := domain.SignOutlineAllExecutionReceipt(receipt)
	if err != nil {
		t.Fatal(err)
	}
	return signed
}

func TestValidateOutlineAllChapterZeroWorkspaceRejectsLiveArtifacts(t *testing.T) {
	for _, rel := range []string{
		"chapters/01.md",
		"drafts/01.plan.json",
		"drafts/01.draft.md",
		"meta/planning/chapters/000001.json",
		"meta/planning/generations/current/chapters/000001.projected.json",
		"meta/planning/current_plan.json",
		"正文.md",
	} {
		t.Run(strings.ReplaceAll(rel, "/", "_"), func(t *testing.T) {
			st := NewStore(t.TempDir())
			if err := st.Init(); err != nil {
				t.Fatal(err)
			}
			if err := st.ValidateOutlineAllChapterZeroWorkspace(); err != nil {
				t.Fatalf("empty workspace: %v", err)
			}
			path := filepath.Join(st.Dir(), filepath.FromSlash(rel))
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("stale"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := st.ValidateOutlineAllChapterZeroWorkspace(); err == nil {
				t.Fatalf("workspace guard accepted %s", rel)
			}
		})
	}
}

func TestOutlineAllExecutionReceiptSaveLoadAndCASUpdate(t *testing.T) {
	st := NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	receipt := storeOutlineAllReceiptForTest(t)
	if err := st.SaveOutlineAllExecutionReceipt(receipt); err != nil {
		t.Fatal(err)
	}
	loaded, err := st.LoadOutlineAllExecutionReceipt()
	if err != nil || loaded == nil || loaded.ReceiptDigest != receipt.ReceiptDigest {
		t.Fatalf("load receipt=%+v err=%v", loaded, err)
	}

	updatedAt := receipt.UpdatedAt.Add(time.Second)
	updated, err := st.UpdateOutlineAllExecutionReceipt(receipt.ReceiptDigest, func(current *domain.OutlineAllExecutionReceipt) error {
		current.PendingAction = nil
		current.CompletedActionCount++
		current.UpdatedAt = updatedAt
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.ReceiptDigest == receipt.ReceiptDigest ||
		updated.PendingAction != nil ||
		updated.CompletedActionCount != 1 {
		t.Fatalf("unexpected updated receipt: %+v", updated)
	}

	if _, err := st.UpdateOutlineAllExecutionReceipt(receipt.ReceiptDigest, func(*domain.OutlineAllExecutionReceipt) error {
		return nil
	}); err == nil || !strings.Contains(err.Error(), "changed before update") {
		t.Fatalf("stale CAS digest must fail: %v", err)
	}
	persisted, err := st.LoadOutlineAllExecutionReceipt()
	if err != nil || persisted.ReceiptDigest != updated.ReceiptDigest {
		t.Fatalf("failed CAS mutated persisted receipt: %+v err=%v", persisted, err)
	}
}
