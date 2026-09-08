package domain

import (
	"reflect"
	"strings"
	"testing"
)

func TestRevisionFeedbackPolicyKeepsOriginalAndOnlyOwnerConstraintCategories(t *testing.T) {
	r := WorldArbitrationReceipt{Conflicts: []WorldArbitrationConflict{{ID: "private-secret", Kind: "time", AffectedAgentIDs: []string{"owner"}, Feedback: "peer plans secret action and owns 11.8"}, {Kind: "malicious_kind_secret", AffectedAgentIDs: []string{"owner"}, Feedback: "another secret"}, {Kind: "resource", AffectedAgentIDs: []string{"peer"}, Feedback: "not delivered"}}}
	before := continuationCloneV1(r)
	got := CharacterArbitrationFeedbackForOwnerV1(r, "owner", []string{CharacterRevisionFeedbackPolicyV1})
	if len(got) != 2 || strings.Contains(strings.Join(got, " "), "secret") || strings.Contains(strings.Join(got, " "), "11.8") || strings.Contains(strings.Join(got, " "), "peer") {
		t.Fatal("feedback leaked arbitrary text or peer information")
	}
	if len(CharacterArbitrationFeedbackForOwnerV1(r, "sleeping", []string{CharacterRevisionFeedbackPolicyV1})) != 0 {
		t.Fatal("unaffected owner got a revision")
	}
	if !reflect.DeepEqual(before, r) {
		t.Fatal("safe projection rewrote original receipt")
	}
	legacy := CharacterArbitrationFeedbackForOwnerV1(r, "owner", nil)
	if !reflect.DeepEqual(legacy, []string{"time：peer plans secret action and owns 11.8", "malicious_kind_secret：another secret"}) {
		t.Fatal("legacy feedback wire changed")
	}
}
