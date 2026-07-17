package agents

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/voocel/agentcore"
)

func TestExpandArcWorldTickGateUsesOnlyExactChapterZeroOutlineAllIntent(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	progress := &domain.Progress{
		NovelName:     "Gate fixture",
		Phase:         domain.PhaseOutline,
		TotalChapters: 24,
		Layered:       true,
		GenerationID:  "gate-generation",
	}
	if err := st.Progress.Save(progress); err != nil {
		t.Fatal(err)
	}
	// A deliberately stale sentinel makes the ordinary gate observable even at
	// chapter zero. Production zero-init writes through_chapter=0.
	if err := st.WorldSim.SaveTick(domain.WorldTick{TickID: "stale-test", ThroughChapter: -1}); err != nil {
		t.Fatal(err)
	}
	compass := domain.StoryCompass{
		EndingDirection: "Resolve the terminal contract.",
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
		Owner:         "outline-all-world-tick-gate-test",
	}); err != nil {
		t.Fatal(err)
	}
	lock, err := st.Runtime.LoadPipelineExecution()
	if err != nil || lock == nil {
		t.Fatalf("lock=%+v err=%v", lock, err)
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
	action := domain.OutlineAllPendingAction{
		Type:                domain.OutlineAllActionExpandArc,
		Operation:           1,
		Volume:              1,
		Arc:                 2,
		ExpectedChapterSpan: 12,
		BeforeLayeredDigest: beforeLayeredDigest,
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
		AttemptID:                "outline-all-world-tick-gate-test",
		CandidateDir:             candidateDir,
		CoordinatorProvider:      "provider-a",
		CoordinatorModel:         "coordinator",
		CoordinatorReasoning:     "high",
		ArchitectProvider:        "provider-b",
		ArchitectModel:           "architect",
		ArchitectReasoning:       "high",
		PromptProtocolDigest:     promptProtocolDigest,
		PendingAction:            &action,
		StartedAt:                now,
		UpdatedAt:                now,
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
	intent, err := domain.FormatOutlineAllIntent(action)
	if err != nil {
		t.Fatal(err)
	}
	request := agentcore.GateRequest{Call: agentcore.ToolCall{
		Name: "subagent",
		Args: json.RawMessage(`{"agent":"architect_long","task":` + mustJSONTextForOutlineAllGateTest(t, "expand_arc\n"+intent) + `}`),
	}}
	decision, err := expandArcWorldTickGate(st)(context.Background(), request)
	if err != nil || decision != nil {
		t.Fatalf("exact receipt/intent should bypass stale sentinel: decision=%+v err=%v", decision, err)
	}

	_, err = st.UpdateOutlineAllExecutionReceipt(receipt.ReceiptDigest, func(current *domain.OutlineAllExecutionReceipt) error {
		current.Status = domain.OutlineAllExecutionComplete
		current.PendingAction = nil
		current.FinalLayeredDigest = beforeLayeredDigest
		current.FinalFlatDigest, _ = domain.ComputeFlatOutlineDigest(nil)
		current.ArchitectReadinessJSONDigest = compassDigest
		current.ArchitectReadinessMDDigest = compassDigest
		current.UpdatedAt = current.UpdatedAt.Add(time.Second)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	decision, err = expandArcWorldTickGate(st)(context.Background(), request)
	if err != nil || decision == nil || decision.Allowed {
		t.Fatalf("completed receipt must fall back to rolling gate: decision=%+v err=%v", decision, err)
	}
}

func mustJSONTextForOutlineAllGateTest(t *testing.T, value string) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
