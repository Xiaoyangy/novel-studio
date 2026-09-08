package domain

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestArbiterIntentFeedbackPreservesValidReceipts(t *testing.T) {
	s, a, _, p := validCharacterAgentProtocolForTest(t)
	r, err := FinalizeWorldArbitrationReceipt(validCharacterArbitrationForTest(s, a, p), s, a, []CharacterDecisionProposal{p}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if r.Digest != "sha256:97334a41b7e55e4bbcc1d1245f7d6e0ea5f22d035bfb5ceb301854b26b88e922" {
		t.Fatal("intent feedback changed the legacy valid receipt digest")
	}
	raw, _ := json.Marshal(r)
	replayed, err := FinalizeWorldArbitrationReceipt(r, s, a, []CharacterDecisionProposal{p}, 1)
	if err != nil {
		t.Fatal(err)
	}
	replayedRaw, _ := json.Marshal(replayed)
	if string(raw) != string(replayedRaw) {
		t.Fatal("intent feedback changed valid receipt bytes")
	}
	f := newPhysicalProtocolFixture(t)
	r, err = finalizePhysicalFixture(f)
	if err != nil {
		t.Fatal(err)
	}
	if r.Digest != "sha256:a442ae04c94226ddd9bdd9b07e664db8d8976f8e830b94b1720ba1f744e8cd46" {
		t.Fatal("intent feedback changed the v2 valid receipt digest")
	}
}

func TestArbiterIntentFeedbackNamesOnlyMismatchedFieldsWithExactJSON(t *testing.T) {
	for _, mode := range []string{"decision", "intended_action", "both"} {
		t.Run(mode, func(t *testing.T) {
			s, a, o, p := validCharacterAgentProtocolForTest(t)
			p.Decision = "保留\"原决定\"与\\路径"
			p.IntendedAction = "先核对\n再原样记录\t实际经过"
			p.DecisionReason = "PRIVATE_PROPOSAL_REASONING"
			var err error
			p, err = FinalizeCharacterDecisionProposal(p, o)
			if err != nil {
				t.Fatal(err)
			}
			r := validCharacterArbitrationForTest(s, a, p)
			if mode != "intended_action" {
				r.Resolutions[0].Decision = "PRIVATE_REJECTED_DECISION"
			}
			if mode != "decision" {
				r.Resolutions[0].IntendedAction = "PRIVATE_REJECTED_ACTION"
			}
			before, _ := json.Marshal(r)
			_, err = FinalizeWorldArbitrationReceipt(r, s, a, []CharacterDecisionProposal{p}, 1)
			if err == nil || !strings.HasPrefix(err.Error(), "arbiter rewrote intent for "+p.AgentID+":") {
				t.Fatalf("rewritten intent accepted or generic prefix lost: %v", err)
			}
			characterJSON, _ := json.Marshal(p.Character)
			if !strings.Contains(err.Error(), "character="+string(characterJSON)) {
				t.Fatalf("diagnostic cannot identify the bound character behind an opaque agent id: %v", err)
			}
			for _, field := range []struct {
				name, expected string
				changed        bool
			}{{"decision", p.Decision, mode != "intended_action"}, {"intended_action", p.IntendedAction, mode != "decision"}} {
				encoded, _ := json.Marshal(field.expected)
				hint := fmt.Sprintf("%s must exactly match original proposal.%s; expected_json=%s", field.name, field.name, encoded)
				if strings.Contains(err.Error(), hint) != field.changed {
					t.Fatalf("wrong mismatched-field repair set for %s: %v", mode, err)
				}
			}
			if strings.Contains(err.Error(), "PRIVATE_") {
				t.Fatalf("diagnostic exposed candidate or reasoning: %v", err)
			}
			after, _ := json.Marshal(r)
			if string(before) != string(after) {
				t.Fatal("intent diagnostics silently corrected the candidate")
			}
		})
	}
}

func TestArbiterIntentFeedbackOmitsOverlongValuesInsteadOfTruncating(t *testing.T) {
	s, a, o, p := validCharacterAgentProtocolForTest(t)
	p.Decision = strings.Repeat("长决定", 3000)
	p.IntendedAction = strings.Repeat("长行动", 3000)
	var err error
	p, err = FinalizeCharacterDecisionProposal(p, o)
	if err != nil {
		t.Fatal(err)
	}
	r := validCharacterArbitrationForTest(s, a, p)
	r.Resolutions[0].Decision, r.Resolutions[0].IntendedAction = "候选改写", "候选行动"
	_, err = FinalizeWorldArbitrationReceipt(r, s, a, []CharacterDecisionProposal{p}, 1)
	if err == nil {
		t.Fatal("long rewritten intent was accepted")
	}
	for _, field := range []struct{ name, value string }{{"decision", p.Decision}, {"intended_action", p.IntendedAction}} {
		if !strings.Contains(err.Error(), "original proposal."+field.name) || !strings.Contains(err.Error(), fmt.Sprintf("utf8_bytes=%d, sha256:%x", len(field.value), sha256.Sum256([]byte(field.value)))) {
			t.Fatalf("long original lost its full-field location and fingerprint: %v", err)
		}
	}
	if len(err.Error()) > 4096 || strings.Contains(err.Error(), "expected_json=") || strings.Contains(err.Error(), "长决定") || strings.Contains(err.Error(), "长行动") || strings.Contains(err.Error(), "候选") {
		t.Fatalf("diagnostic returned a partial/candidate value or exceeded its bound: %v", err)
	}
}
