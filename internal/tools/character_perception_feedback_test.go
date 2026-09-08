package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/errs"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/testutil"
)

// Builds both resources before publishing the immutable test input; the failed
// tool call below changes only its candidate, never the frozen baseline.
func perceptionFeedbackToolFixture(t *testing.T) (*store.Store, domain.CharacterActivationSession, domain.CharacterActivationCycle, *store.CharacterAgentStore) {
	t.Helper()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	seed := testutil.CharacterCycle(t, 1, "", nil, 0)
	raw, err := json.Marshal(seed.Evidence.Stimulus.PhysicalState)
	must(err)
	var state domain.WorldPhysicalStateV2
	must(json.Unmarshal(raw, &state))
	remaining := 100.0
	const lightID = "res_0000000000000002"
	state.Resources = append(state.Resources, domain.WorldResourceBalanceV2{ResourceID: lightID, Name: "检修灯照明余量", Unit: "分钟", ActualAmount: &remaining})
	state.Actors[0].Resources = append(state.Actors[0].Resources, domain.CharacterResourceHoldingV2{
		ResourceID: lightID, PerceivedName: "检修灯", PerceivedUnit: "分钟", Access: "exclusive",
		Perception:   domain.ResourcePerceptionV2{Kind: "estimated", EstimateMin: &remaining, EstimateMax: &remaining, AsOfChapter: 0, EvidenceRefs: []string{"opening-light-estimate"}},
		EvidenceRefs: []string{"opening-light-access"},
	})
	cycle := testutil.CharacterCycle(t, 1, "", &state, 0)
	e := cycle.Evidence
	st := store.NewStore(t.TempDir())
	must(st.Init())
	session, err := domain.NewCharacterActivationSession(e.GenerationID, e.Chapter, cycle.ChapterContextDigest, *e.Stimulus.PhysicalState, 0, 4)
	must(err)
	proofs, err := st.CharacterAgents.ForActivationCycle(session)
	must(err)
	must(proofs.SaveRegistrySnapshot(e.GenerationID, e.Chapter, e.Registry))
	must(proofs.SaveStimulus(e.Stimulus))
	must(proofs.SaveObservation(e.Observations[0]))
	must(proofs.SaveActivation(e.Activation))
	must(proofs.SaveProposal(e.Proposals[0], e.Observations[0]))
	return st, session, cycle, proofs
}

func TestWorldToolCombinesPerceptionFailuresAndPreservesSparseReceipt(t *testing.T) {
	st, session, cycle, proofs := perceptionFeedbackToolFixture(t)
	e := cycle.Evidence
	p := e.Proposals[0]
	tool, err := NewResolveCharacterActivationTool(st, session, e.Stimulus, e.Activation, e.Proposals, e.ProtocolDigest, nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	var args map[string]json.RawMessage
	if err := json.Unmarshal(activationArbiterArgs(t, cycle), &args); err != nil {
		t.Fatal(err)
	}
	var resolutions []map[string]json.RawMessage
	if err := json.Unmarshal(args["resolutions"], &resolutions); err != nil {
		t.Fatal(err)
	}
	const fuelID, lightID = "res_0000000000000001", "res_0000000000000002"
	// Reverse the candidate order to check deterministic resource ordering.
	resolutions[0]["post_state"], _ = json.Marshal(map[string]any{
		"location": p.Location,
		"resource_updates": []map[string]any{
			{"resource_id": lightID, "perception": map[string]any{"kind": "estimated", "estimate_min": 100, "estimate_max": 100, "as_of_chapter": 1, "evidence_refs": []string{p.Digest}}},
			{"resource_id": fuelID, "perception": map[string]any{"kind": "last_observed", "amount": 11.8, "as_of_chapter": 1, "evidence_refs": []string{"M_RESOURCE"}}},
		},
	})
	args["resolutions"], _ = json.Marshal(resolutions)
	invalid, _ := json.Marshal(args)
	before := arbitrationReferenceFiles(t, st.Dir())
	_, err = tool.Execute(context.Background(), invalid)
	if !errors.Is(err, errs.ErrToolPrecondition) {
		t.Fatalf("perception failures lost tool precondition classification: %v", err)
	}
	fuelIssue := fmt.Sprintf("post_state[%s].resources[%s].perception.evidence_refs", p.AgentID, fuelID)
	lightIssue := fmt.Sprintf("actor %s resource %s numeric perception lacks an independent estimate, measurement or received report", p.AgentID, lightID)
	fuelIndex, lightIndex := strings.Index(err.Error(), fuelIssue), strings.Index(err.Error(), lightIssue)
	if fuelIndex < 0 || lightIndex < 0 || fuelIndex >= lightIndex {
		t.Fatalf("one rejected call must report both independent errors in resource order: %v", err)
	}
	for _, hint := range []string{"world consumption does not update an actor's knowledge", "omit perception from resource_updates", "proposal digest as evidence, not a mechanism ID"} {
		if !strings.Contains(err.Error(), hint) {
			t.Fatalf("missing actionable repair hint %q: %v", hint, err)
		}
	}
	if !reflect.DeepEqual(before, arbitrationReferenceFiles(t, st.Dir())) {
		t.Fatal("rejected perception candidate changed frozen inputs, arbitration, checkpoint or other files")
	}
	if receipt, loadErr := proofs.LoadArbitration(e.GenerationID, e.Chapter, 1); loadErr != nil || receipt != nil {
		t.Fatalf("rejected candidate persisted an arbitration: receipt=%v err=%v", receipt, loadErr)
	}

	// The existing minimal post_state inherits both old perceptions even though
	// the separate world settlement consumes fuel. No corrected numeric claim.
	resolutions[0]["post_state"], _ = json.Marshal(map[string]any{"location": p.Location})
	args["resolutions"], _ = json.Marshal(resolutions)
	validSparse, _ := json.Marshal(args)
	if _, err := tool.Execute(context.Background(), validSparse); err != nil {
		t.Fatalf("existing sparse post_state was rejected: %v", err)
	}
	receipt, err := proofs.LoadArbitration(e.GenerationID, e.Chapter, 1)
	if err != nil || receipt == nil {
		t.Fatalf("legal sparse arbitration was not persisted: %v", err)
	}
	if !reflect.DeepEqual(receipt.Resolutions[0].PostState.Resources, e.Stimulus.PhysicalState.Actors[0].Resources) {
		t.Fatal("sparse repair refreshed an old perception or rewrote its evidence")
	}
	expected := e.Arbitrations[0]
	expected.GeneratedAt = receipt.GeneratedAt
	expected, err = domain.FinalizeWorldArbitrationReceipt(expected, e.Stimulus, e.Activation, e.Proposals, 1)
	if err != nil || expected.Digest != receipt.Digest {
		t.Fatalf("diagnostic aggregation changed the original legal receipt digest: expected=%s actual=%s err=%v", expected.Digest, receipt.Digest, err)
	}
}
