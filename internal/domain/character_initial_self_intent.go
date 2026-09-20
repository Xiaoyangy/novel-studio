package domain

// This opt-in projection remembers an owner's opening intention as private
// history, not as a command or an immutable future outcome.
const CharacterInitialSelfIntentPolicyV1 = "character-initial-self-intent:history.v1"

func HasCharacterInitialSelfIntentPolicyV1(sources []string) bool {
	return physicalContainsRefV2(sources, CharacterInitialSelfIntentPolicyV1)
}
