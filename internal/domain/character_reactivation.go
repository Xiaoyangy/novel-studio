package domain

import (
	"fmt"
	"reflect"
	"sort"
)

// CharacterReactivationReasons compares only actor-visible semantic state.
// New envelope timestamps, opaque packet digests, or hidden world balances
// never wake an Agent. Initial chapter activation uses the existing roster
// policy; this function governs subsequent activations within the same chapter.
func CharacterReactivationReasons(previous, current CharacterObservationPacket) ([]string, error) {
	if previous.AgentID == "" || previous.AgentID != current.AgentID || previous.Character != current.Character || previous.GenerationID != current.GenerationID || previous.Chapter != current.Chapter {
		return nil, fmt.Errorf("reactivation comparison must name one actor within one chapter/generation")
	}
	var reasons []string
	if previous.Location != current.Location {
		reasons = append(reasons, "location_change")
	}
	if !sameReactivationList(previous.ResourceViews, current.ResourceViews) {
		reasons = append(reasons, "observed_resource_change")
	}
	if !sameReactivationList(previous.Relationships, current.Relationships) {
		reasons = append(reasons, "relationship_change")
	}
	if !sameReactivationList(previous.Commitments, current.Commitments) {
		reasons = append(reasons, "commitment_trigger")
	}
	if !sameReactivationList(previous.SelfExperiences, current.SelfExperiences) || !sameReactivationList(previous.TaskProgress, current.TaskProgress) || !sameReactivationList(previous.OperationalObservations, current.OperationalObservations) {
		reasons = append(reasons, "self_execution_feedback")
	}
	if !sameReactivationList(artifactKnownReactivationViewsV1(previous.ArtifactViews), artifactKnownReactivationViewsV1(current.ArtifactViews)) {
		reasons = append(reasons, "artifact_knowledge_feedback")
	}
	newFacts := func(before, after []CharacterAgentFact) bool {
		known := map[string]CharacterAgentFact{}
		for _, fact := range before {
			known[fact.ID] = fact
		}
		for _, fact := range after {
			if old, ok := known[fact.ID]; !ok || old.Kind != fact.Kind || old.Text != fact.Text {
				return true
			}
		}
		return false
	}
	if newFacts(previous.KnownFacts, current.KnownFacts) || newFacts(previous.PerceivedEvents, current.PerceivedEvents) {
		reasons = append(reasons, "received_information")
	}
	if newFacts(previous.PublicRules, current.PublicRules) || !sameReactivationList(previous.PublicMechanisms, current.PublicMechanisms) {
		reasons = append(reasons, "public_rule_change")
	}
	priorMemory := map[string]CharacterAgentMemoryFact{}
	for _, fact := range previous.Memory {
		priorMemory[fact.ID] = fact
	}
	for _, fact := range current.Memory {
		if old, ok := priorMemory[fact.ID]; !ok || old.Kind != fact.Kind || old.Text != fact.Text {
			reasons = append(reasons, "personal_memory_feedback")
			break
		}
	}
	sort.Strings(reasons)
	return reasons, nil
}

// JSON omitempty round-trips an empty list to nil. That representation change
// carries no event and must not wake sleeping characters (or spend tokens).
func sameReactivationList[T any](a, b []T) bool {
	return len(a) == 0 && len(b) == 0 || reflect.DeepEqual(a, b)
}
