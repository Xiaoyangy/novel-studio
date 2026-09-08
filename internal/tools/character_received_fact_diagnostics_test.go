package tools

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/errs"
)

func TestReceivedFactDiagnosticKernelFailureWritesNothing(t *testing.T) {
	st, proposal, _ := newPhysicalArbitratedStoreForTest(t)
	stimulus, err := st.CharacterAgents.LoadStimulus(proposal.GenerationID, proposal.Chapter)
	if err != nil || stimulus == nil {
		t.Fatalf("stimulus: %v", err)
	}
	activation, err := st.CharacterAgents.LoadActivation(proposal.GenerationID, proposal.Chapter)
	if err != nil || activation == nil {
		t.Fatalf("activation: %v", err)
	}
	receipt, err := st.CharacterAgents.LoadArbitration(proposal.GenerationID, proposal.Chapter, proposal.Round)
	if err != nil || receipt == nil {
		t.Fatalf("receipt: %v", err)
	}
	// The accepted fixture read the exact document. A different attempted
	// receipt cannot retain that read while claiming its reader was blocked.
	resolutions := append([]domain.CharacterDecisionResolution(nil), receipt.Resolutions...)
	resolutions[0].CompletionState = "blocked"
	raw, err := json.Marshal(map[string]any{
		"time_window": stimulus.TimeWindow, "story_time": receipt.StoryTime,
		"resolutions": resolutions, "conflicts": receipt.Conflicts, "hard_contract_status": receipt.HardContractStatus,
		"hard_contract_conflicts": receipt.HardContractConflicts, "protagonist_projection": receipt.ProtagonistProjection,
		"finalized": receipt.Finalized, "resource_settlements": receipt.ResourceSettlements, "resource_deliveries": receipt.ResourceDeliveries,
	})
	if err != nil {
		t.Fatal(err)
	}
	tool := NewResolveChapterWorldTool(st, *stimulus, *activation, []domain.CharacterDecisionProposal{proposal}, "sha256:"+strings.Repeat("a", 64), nil, 1)
	before := arbitrationReferenceFiles(t, st.Dir())
	_, err = tool.Execute(context.Background(), raw)
	if !errors.Is(err, errs.ErrToolPrecondition) || !strings.Contains(err.Error(), "code=reader_completion_blocked") || !strings.Contains(err.Error(), "lacks an exact delivered communication or authorized document read") {
		t.Fatalf("kernel gate/classification changed: %v", err)
	}
	if strings.Contains(err.Error(), "SECRET") || strings.Contains(err.Error(), "原始经营用油记录") {
		t.Fatal("private content escaped through diagnostic")
	}
	if !reflect.DeepEqual(before, arbitrationReferenceFiles(t, st.Dir())) {
		t.Fatal("failed kernel validation wrote receipt/plan/checkpoint/source data")
	}
	again, err := domain.FinalizeWorldArbitrationReceipt(*receipt, *stimulus, *activation, []domain.CharacterDecisionProposal{proposal}, 1)
	if err != nil || again.Digest != receipt.Digest {
		t.Fatalf("original valid receipt/hash changed: %v", err)
	}
}
