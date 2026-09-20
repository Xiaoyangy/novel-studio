package domain

import (
	"strings"
	"testing"
)

func TestIncomingReadMixedModeExplainsExactFieldConflict(t *testing.T) {
	observation := CharacterObservationPacket{Sources: []string{
		CharacterIncomingMaterialReadPolicyV1, CharacterWorkArtifactPolicyV1, CharacterSelfExperiencePolicyV2,
	}}
	proposal := CharacterDecisionProposal{Character: "乙",
		SelfTasks: []CharacterSelfTaskV2{{TaskID: "read", Kind: "work"}},
		ResourceReads: []ResourceReadRequestV2{
			{ResourceID: "res_0000000000000001"},
			{ResourceID: "res_0000000000000002", TaskID: "read"},
		},
	}
	err := validateIncomingMaterialReadIntentV1(proposal, observation)
	if err == nil {
		t.Fatal("mixed ordinary and incoming read modes must remain invalid")
	}
	for _, part := range []string{"resource_reads[1]", "resource_id", "task_id", "ordinary"} {
		if !strings.Contains(err.Error(), part) {
			t.Fatalf("feedback must name the exact conflicting mode, missing %q: %v", part, err)
		}
	}
	proposal.ResourceReads[1].TaskID = ""
	if err := validateIncomingMaterialReadIntentV1(proposal, observation); err != nil {
		t.Fatalf("ordinary reads must remain valid without an incoming task: %v", err)
	}
	proposal.ResourceReads[1] = ResourceReadRequestV2{IncomingDeliveryFrom: "甲", TaskID: "read"}
	if err := validateIncomingMaterialReadIntentV1(proposal, observation); err != nil {
		t.Fatalf("explicit incoming read must still bind the receiver's own work: %v", err)
	}
}
