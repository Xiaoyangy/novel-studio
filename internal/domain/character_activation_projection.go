package domain

import "strings"

// HasCharacterActivationCycleContract recognizes the host's explicit cycle
// source contract, not a generic v2 or passive-reception policy. Full cycle
// validation still binds this content-addressed token to the exact generation,
// chapter, context, index and predecessor; a model cannot add stimulus sources.
func HasCharacterActivationCycleContract(stimulus WorldStimulusPacket) bool {
	if stimulus.Version != WorldStimulusPacketV2Version || stimulus.PhysicalState == nil || stimulus.StoryClock == nil {
		return false
	}
	count := 0
	for _, source := range stimulus.Sources {
		if !strings.HasPrefix(source, CharacterActivationCycleSourcePrefix) {
			continue
		}
		digest := strings.TrimPrefix(source, CharacterActivationCycleSourcePrefix)
		if digest != strings.TrimSpace(digest) {
			return false
		}
		if err := validatePlanningV2Digest("activation cycle source", digest); err != nil {
			return false
		}
		count++
	}
	return count == 1
}

func emptyCharacterCycleProjection(projection ProtagonistDecisionProjection) bool {
	return projection.Protagonist == "" && projection.ChosenDecision == "" && projection.DecisionReason == "" &&
		len(projection.ObservableEffects) == 0 && len(projection.HiddenPressures) == 0 && len(projection.AvailableOptions) == 0 &&
		len(projection.PlanConstraints) == 0 && len(projection.CausalChain) == 0
}
