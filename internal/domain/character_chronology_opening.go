package domain

import "fmt"

// ValidateCharacterChronologyOpeningFromSourceV1 authenticates the metadata
// enrichment against a separately verified predecessor. The source must come
// from the prior projected/accepted state or frozen author artifacts, never
// from the opening's own SourcePhysicalRoot or another self-reported before.
// Legacy openings keep their existing validation and byte identities.
func ValidateCharacterChronologyOpeningFromSourceV1(source WorldPhysicalStateV2, opening WorldStimulusPacket) error {
	if !HasCharacterSelfChronologyPolicyV1(opening.Sources) {
		return nil
	}
	if opening.PhysicalState == nil {
		return fmt.Errorf("chronology opening lacks its prepared physical state")
	}
	if err := ValidateCharacterSelfChronologyBaselineTransitionV1(source, *opening.PhysicalState); err != nil {
		return fmt.Errorf("chronology opening differs from its authoritative predecessor: %w", err)
	}
	return nil
}

func validateCharacterChronologyProjectedPredecessorV1(previous, current ProjectedChapterBundle) error {
	opening := current.CharacterOpeningStimulus()
	if opening == nil || !HasCharacterSelfChronologyPolicyV1(opening.Sources) {
		return nil
	}
	if previous.Chapter+1 != current.Chapter || previous.ChapterWorldSimulation.PhysicalState == nil {
		return fmt.Errorf("chronology opening requires the exact preceding projected physical state")
	}
	return ValidateCharacterChronologyOpeningFromSourceV1(*previous.ChapterWorldSimulation.PhysicalState, *opening)
}
