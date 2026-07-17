package tools

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func authorizeOutlineAllForTest(t *testing.T, st *store.Store) domain.OutlineAllExecutionReceipt {
	t.Helper()
	progress := &domain.Progress{
		NovelName:     "Generic fixture",
		Phase:         domain.PhaseOutline,
		TotalChapters: 12,
		Layered:       true,
		GenerationID:  "generation-outline-all-test",
	}
	if err := st.Progress.Save(progress); err != nil {
		t.Fatal(err)
	}
	compass := domain.StoryCompass{
		EndingDirection: "The founding promise becomes an irreversible outcome.",
		NonNegotiables:  []string{"settle the public obligation"},
		EstimatedScale:  "4-5 volumes, 80-100 chapters",
	}
	if err := st.Outline.SaveCompass(compass); err != nil {
		t.Fatal(err)
	}
	writingMode := domain.WritingPipelineModeReceipt{
		Version:     domain.WritingPipelineModeReceiptVersion,
		Mode:        domain.WritingPipelineModeSealedTwoPassV2,
		ActivatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	var err error
	writingMode.ReceiptDigest, err = domain.ComputeWritingPipelineModeReceiptDigest(writingMode)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SaveWritingPipelineMode(writingMode); err != nil {
		t.Fatal(err)
	}
	if err := st.Runtime.AcquirePipelineExecution(domain.PipelineExecutionLock{
		Mode:          domain.PipelineExecutionOutlineAll,
		TargetChapter: 1,
		Owner:         "outline-all-authorization-test",
	}); err != nil {
		t.Fatal(err)
	}
	lock, err := st.Runtime.LoadPipelineExecution()
	if err != nil || lock == nil {
		t.Fatalf("load execution lock: %+v err=%v", lock, err)
	}
	compassDigest, err := domain.ComputeStoryCompassDigest(compass)
	if err != nil {
		t.Fatal(err)
	}
	layered, err := st.Outline.LoadLayeredOutline()
	if err != nil {
		t.Fatal(err)
	}
	beforeLayeredDigest, err := domain.ComputeLayeredOutlineDigest(layered)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	promptProtocolDigest, err := domain.ComputeOutlineAllPromptProtocolDigest("single mutation protocol v1")
	if err != nil {
		t.Fatal(err)
	}
	candidateDir, err := filepath.Abs(st.Dir())
	if err != nil {
		t.Fatal(err)
	}
	receipt := domain.OutlineAllExecutionReceipt{
		Version:                  domain.OutlineAllExecutionReceiptVersion,
		Mode:                     domain.OutlineAllExecutionMode,
		Status:                   domain.OutlineAllExecutionBuilding,
		BaseCanonChapter:         0,
		GenerationID:             progress.GenerationID,
		WritingMode:              writingMode.Mode,
		WritingModeReceiptDigest: writingMode.ReceiptDigest,
		CompassDigest:            compassDigest,
		EstimatedScale:           compass.EstimatedScale,
		EndingDirection:          compass.EndingDirection,
		NonNegotiables:           compass.NonNegotiables,
		MinVolumes:               4,
		MaxVolumes:               5,
		MinChapters:              80,
		MaxChapters:              100,
		TargetVolumes:            4,
		TargetChapters:           90,
		TargetWords:              225_000,
		TargetWordsPerChapter:    2500,
		StoryTimeHint:            "one story year",
		SourceSnapshotRoot:       compassDigest,
		ProtectedCanonRoot:       compassDigest,
		StableProgressRoot:       compassDigest,
		FoundationContextRoot:    compassDigest,
		AttemptID:                "outline-all-authorization-test",
		CandidateDir:             candidateDir,
		CoordinatorProvider:      "provider-a",
		CoordinatorModel:         "coordinator",
		CoordinatorReasoning:     "high",
		ArchitectProvider:        "provider-b",
		ArchitectModel:           "architect",
		ArchitectReasoning:       "high",
		PromptProtocolDigest:     promptProtocolDigest,
		PendingAction: &domain.OutlineAllPendingAction{
			Type:                domain.OutlineAllActionAppendVolume,
			Operation:           1,
			ExpectedVolumeIndex: 2,
			Volume:              2,
			ExpectedChapterSpan: 24,
			ExpectedArcSpans:    "12,12",
			BeforeLayeredDigest: beforeLayeredDigest,
		},
		StartedAt: now,
		UpdatedAt: now,
	}
	receipt.ModelIdentityDigest, err = domain.ComputeOutlineAllModelIdentityDigest(receipt.ModelIdentity())
	if err != nil {
		t.Fatal(err)
	}
	if err := domain.BindOutlineAllExecutionLock(&receipt, *lock); err != nil {
		t.Fatal(err)
	}
	receipt, err = domain.SignOutlineAllExecutionReceipt(receipt)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SaveOutlineAllExecutionReceipt(receipt); err != nil {
		t.Fatal(err)
	}
	return receipt
}

func TestChapterZeroOutlineAllWorldTickBypassRequiresExactCapability(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	receipt := authorizeOutlineAllForTest(t, st)
	authorized, err := ChapterZeroOutlineAllWorldTickBypassAuthorized(st, receipt.PendingAction)
	if err != nil || !authorized {
		t.Fatalf("expected exact chapter-zero capability, authorized=%v err=%v", authorized, err)
	}

	progress, err := st.Progress.Load()
	if err != nil {
		t.Fatal(err)
	}
	progress.CurrentChapter = 1
	if err := st.Progress.Save(progress); err != nil {
		t.Fatal(err)
	}
	authorized, err = ChapterZeroOutlineAllWorldTickBypassAuthorized(st, receipt.PendingAction)
	if err != nil || authorized {
		t.Fatalf("chapter 1 must close bypass, authorized=%v err=%v", authorized, err)
	}
	progress.CurrentChapter = 0
	if err := st.Progress.Save(progress); err != nil {
		t.Fatal(err)
	}

	_, err = st.UpdateOutlineAllExecutionReceipt(receipt.ReceiptDigest, func(current *domain.OutlineAllExecutionReceipt) error {
		current.LockOwner += "-copied"
		current.UpdatedAt = current.UpdatedAt.Add(time.Second)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	authorized, err = ChapterZeroOutlineAllWorldTickBypassAuthorized(st, receipt.PendingAction)
	if err != nil || authorized {
		t.Fatalf("copied/mismatched lock must not authorize, authorized=%v err=%v", authorized, err)
	}
}

func TestOutlineAllExecutionAllowsOnlyExactFoundationMutation(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	receipt := authorizeOutlineAllForTest(t, st)
	if err := guardOutlineAllFoundationType(st, "update_compass", ""); err == nil {
		t.Fatal("outline_all lock must reject non-pending foundation types")
	}
	if err := guardPipelineGlobalPlanningExecution(st, "save_world_tick"); err == nil {
		t.Fatal("outline_all lock must reject world_tick mutation")
	}
	exact := domain.OutlineAllPendingAction{
		Type:                domain.OutlineAllActionAppendVolume,
		Volume:              2,
		ExpectedVolumeIndex: 2,
		ExpectedChapterSpan: 24,
	}
	if err := guardOutlineAllFoundationMutation(st, exact); err != nil {
		t.Fatalf("exact decoded mutation: %v", err)
	}
	exact.ExpectedChapterSpan--
	if err := guardOutlineAllFoundationMutation(st, exact); err == nil {
		t.Fatal("wrong append span must not consume pending capability")
	}
	if receipt.PendingAction.Operation != 1 {
		t.Fatalf("unexpected operation fixture: %+v", receipt.PendingAction)
	}
}

func TestChapterZeroOutlineAllWorldTickBypassRejectsCompleteReceipt(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	receipt := authorizeOutlineAllForTest(t, st)
	finalFlatDigest, err := domain.ComputeFlatOutlineDigest(nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = st.UpdateOutlineAllExecutionReceipt(receipt.ReceiptDigest, func(current *domain.OutlineAllExecutionReceipt) error {
		current.Status = domain.OutlineAllExecutionComplete
		current.PendingAction = nil
		current.FinalLayeredDigest = receipt.PendingAction.BeforeLayeredDigest
		current.FinalFlatDigest = finalFlatDigest
		current.ArchitectReadinessJSONDigest = receipt.CompassDigest
		current.ArchitectReadinessMDDigest = receipt.CompassDigest
		current.UpdatedAt = current.UpdatedAt.Add(time.Second)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	authorized, err := ChapterZeroOutlineAllWorldTickBypassAuthorized(st, receipt.PendingAction)
	if err != nil || authorized {
		t.Fatalf("completed receipt must revoke bypass, authorized=%v err=%v", authorized, err)
	}
}
