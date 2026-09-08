package domain

// These selectors do not grant trust. Callers still validate the whole bundle;
// they only avoid treating multi-cycle proof as an unrelated single round.
func (bundle ProjectedChapterBundle) HasCharacterEvidence() bool {
	return bundle.CharacterAgentEvidence != nil || bundle.CharacterActivationEvidence != nil
}

func (bundle ProjectedChapterBundle) CharacterOpeningStimulus() *WorldStimulusPacket {
	if bundle.CharacterActivationEvidence != nil {
		if len(bundle.CharacterActivationEvidence.Cycles) == 0 {
			return nil
		}
		return &bundle.CharacterActivationEvidence.Cycles[0].Evidence.Stimulus
	}
	if bundle.CharacterAgentEvidence != nil {
		return &bundle.CharacterAgentEvidence.Stimulus
	}
	return nil
}

func (bundle ProjectedChapterBundle) HasCharacterSelfExperienceEvidence() bool {
	stimulus := bundle.CharacterOpeningStimulus()
	return stimulus != nil && HasCharacterSelfExperiencePolicyV2(stimulus.Sources)
}
