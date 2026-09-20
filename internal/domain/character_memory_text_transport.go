package domain

// This marker opts a new, frozen producer into lossless model-only encoding.
// It does not change canonical memory, actor knowledge, or action authority.
const CharacterMemoryTextTransportPolicyV1 = "character-memory-text-transport:shared.v1"

func HasCharacterMemoryTextTransportPolicyV1(sources []string) bool {
	return physicalContainsRefV2(sources, CharacterMemoryTextTransportPolicyV1)
}
