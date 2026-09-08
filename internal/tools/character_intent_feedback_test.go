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

func TestWorldToolIntentFeedbackRejectsWithoutWritingAndPreservesReceipt(t *testing.T) {
	for _, mode := range []string{"decision", "intended_action", "both"} {
		t.Run(mode, func(t *testing.T) {
			st, session, cycle, proofs := activationToolFixture(t, true)
			e := cycle.Evidence
			p := e.Proposals[0]
			tool, err := NewResolveCharacterActivationTool(st, session, e.Stimulus, e.Activation, e.Proposals, e.ProtocolDigest, nil, 1)
			if err != nil {
				t.Fatal(err)
			}
			valid := activationArbiterArgs(t, cycle)
			var args map[string]json.RawMessage
			if err := json.Unmarshal(valid, &args); err != nil {
				t.Fatal(err)
			}
			var resolutions []map[string]json.RawMessage
			if err := json.Unmarshal(args["resolutions"], &resolutions); err != nil {
				t.Fatal(err)
			}
			if mode != "intended_action" {
				resolutions[0]["decision"], _ = json.Marshal("PRIVATE_CANDIDATE_DECISION")
			}
			if mode != "decision" {
				resolutions[0]["intended_action"], _ = json.Marshal("PRIVATE_CANDIDATE_ACTION")
			}
			args["resolutions"], _ = json.Marshal(resolutions)
			raw, _ := json.Marshal(args)
			before := arbitrationReferenceFiles(t, st.Dir())
			_, err = tool.Execute(context.Background(), raw)
			if !errors.Is(err, errs.ErrToolPrecondition) || !strings.Contains(err.Error(), "arbiter rewrote intent for "+p.AgentID) {
				t.Fatalf("rewritten intent lost fail-closed classification: %v", err)
			}
			characterJSON, _ := json.Marshal(p.Character)
			if !strings.Contains(err.Error(), "character="+string(characterJSON)) {
				t.Fatalf("bound character name missing from tool diagnostic: %v", err)
			}
			decisionJSON, _ := json.Marshal(p.Decision)
			actionJSON, _ := json.Marshal(p.IntendedAction)
			if mode != "intended_action" && !strings.Contains(err.Error(), "decision must exactly match original proposal.decision; expected_json="+string(decisionJSON)) {
				t.Fatalf("missing exact decision repair: %v", err)
			}
			if mode != "decision" && !strings.Contains(err.Error(), "intended_action must exactly match original proposal.intended_action; expected_json="+string(actionJSON)) {
				t.Fatalf("missing exact intended_action repair: %v", err)
			}
			if strings.Contains(err.Error(), "PRIVATE_CANDIDATE") {
				t.Fatalf("tool feedback exposed rejected candidate: %v", err)
			}
			if !reflect.DeepEqual(before, arbitrationReferenceFiles(t, st.Dir())) {
				t.Fatal("intent rejection wrote or replaced source/receipt files")
			}
			if receipt, loadErr := proofs.LoadArbitration(e.GenerationID, e.Chapter, 1); loadErr != nil || receipt != nil {
				t.Fatalf("rejected intent produced an arbitration: %v", loadErr)
			}
			if _, err := tool.Execute(context.Background(), valid); err != nil {
				t.Fatalf("unchanged original intent no longer succeeds: %v", err)
			}
			receipt, err := proofs.LoadArbitration(e.GenerationID, e.Chapter, 1)
			if err != nil || receipt == nil {
				t.Fatalf("legal arbitration missing: %v", err)
			}
			expected := e.Arbitrations[0]
			expected.GeneratedAt = receipt.GeneratedAt
			expected, err = domain.FinalizeWorldArbitrationReceipt(expected, e.Stimulus, e.Activation, e.Proposals, 1)
			if err != nil {
				t.Fatal(err)
			}
			expectedJSON, _ := json.Marshal(expected)
			actualJSON, _ := json.Marshal(receipt)
			if expected.Digest != receipt.Digest || string(expectedJSON) != string(actualJSON) {
				t.Fatal("intent diagnostics changed original legal receipt bytes/digest")
			}
		})
	}
}
