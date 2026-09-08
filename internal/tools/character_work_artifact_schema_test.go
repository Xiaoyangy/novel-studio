package tools

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

func TestWorkArtifactToolSchemaIsExplicitAndHostDerivedFieldsAreNotWritable(t *testing.T) {
	base := []string{domain.CharacterSelfExperiencePolicyV2, domain.CharacterSelfChronologyPolicyV1, domain.CharacterWorkContinuationPolicyV1, domain.CharacterArbitrationRoundSourcesPolicyV1, domain.CharacterActivationCyclePolicyV3}
	observation := domain.CharacterObservationPacket{Version: domain.CharacterObservationV2Version, Sources: base}
	legacy := NewSubmitCharacterDecisionTool(nil, observation).Schema()
	legacyJSON, _ := json.Marshal(legacy)
	if strings.Contains(string(legacyJSON), "output_requests") || strings.Contains(string(legacyJSON), "artifact_reads") {
		t.Fatal("artifact input became available without its explicit policy")
	}
	observation.Sources = append(append([]string(nil), base...), domain.CharacterWorkArtifactPolicyV1)
	current := NewSubmitCharacterDecisionTool(nil, observation).Schema()
	props := current["properties"].(map[string]any)
	taskProps := props["self_tasks"].(map[string]any)["items"].(map[string]any)["properties"].(map[string]any)
	request := taskProps["output_requests"].(map[string]any)["items"].(map[string]any)["properties"].(map[string]any)
	claim := request["claims"].(map[string]any)["items"].(map[string]any)["properties"].(map[string]any)
	for _, key := range []string{"lineage_roots", "version_digest", "creator_agent_id", "placement", "signature_digest"} {
		if claim[key] != nil || request[key] != nil {
			t.Fatalf("Host field %s exposed to the actor", key)
		}
	}
	reads := props["artifact_reads"].(map[string]any)["items"].(map[string]any)["properties"].(map[string]any)
	if reads["at_day"] != nil {
		t.Fatal("actor must not predeclare an actual read timestamp")
	}
	if props["artifact_signs"] == nil {
		t.Fatal("independent signature intent is unavailable")
	}
	for _, key := range []string{"artifact_reads", "artifact_signs"} {
		item := props[key].(map[string]any)["items"].(map[string]any)
		if item["properties"].(map[string]any)["task_id"] == nil || !slices.Contains(item["required"].([]string), "task_id") {
			t.Fatalf("%s intent must bind an actual self task", key)
		}
	}
	stimulus := domain.WorldStimulusPacket{Version: domain.WorldStimulusPacketV2Version, Sources: observation.Sources}
	arbiter := NewResolveChapterWorldTool(nil, stimulus, domain.CharacterAgentActivation{}, nil, "", nil, 1).Schema()
	resolution := arbiter["properties"].(map[string]any)["resolutions"].(map[string]any)["items"].(map[string]any)["properties"].(map[string]any)
	exec := resolution["self_executions"].(map[string]any)["items"].(map[string]any)["properties"].(map[string]any)
	output := exec["output_results"].(map[string]any)["items"].(map[string]any)["properties"].(map[string]any)
	for _, key := range []string{"text", "claims", "lineage_roots", "resource_id", "version_digest", "placement"} {
		if output[key] != nil {
			t.Fatalf("arbiter can replace declared/Host output field %s", key)
		}
	}
	if output["claim_ids"] == nil || output["at_day"] == nil {
		t.Fatal("actual output lacks declaration/time binding")
	}
	signature := resolution["artifact_signatures"].(map[string]any)["items"].(map[string]any)["properties"].(map[string]any)
	if signature["signer_agent_id"] != nil || signature["signature_digest"] != nil || signature["at_day"] == nil {
		t.Fatal("signature result lost Host-bound signer or actual time")
	}
	for _, key := range []string{"artifact_read_results", "artifact_signatures"} {
		item := resolution[key].(map[string]any)["items"].(map[string]any)
		if item["properties"].(map[string]any)["task_id"] != nil {
			t.Fatalf("%s lets arbiter replace the actor's task binding", key)
		}
	}
	// Building the opted-in schema must not mutate the old caller's schema.
	again, _ := json.Marshal(NewSubmitCharacterDecisionTool(nil, domain.CharacterObservationPacket{Version: domain.CharacterObservationV2Version, Sources: base}).Schema())
	if string(legacyJSON) != string(again) {
		t.Fatal("new schema mutated legacy defaults")
	}
}
