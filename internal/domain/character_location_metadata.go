package domain

import "fmt"

// A static source-selected projection policy, not a changing claim about
// what the actor knows. Canonical locations remain world truth. Names learned
// in communications, read statements and the actor's own prose stay intact.
const CharacterHostLocationMetadataPolicyV1 = "character-host-location-metadata:opaque.v1"

func HasCharacterHostLocationMetadataPolicyV1(sources []string) bool {
	return physicalContainsRefV2(sources, CharacterHostLocationMetadataPolicyV1)
}

func validateHostLocationMetadataStimulusV1(stimulus WorldStimulusPacket) error {
	if stimulus.PhysicalState == nil {
		return nil
	}
	for _, actor := range stimulus.PhysicalState.Actors {
		if actor.HostLocationMetadataPolicy != "" && !HasCharacterHostLocationMetadataPolicyV1(stimulus.Sources) {
			return fmt.Errorf("explicit unknown opening location requires the new host-location producer; old generations cannot silently reinterpret it")
		}
	}
	return nil
}

// Formatting copies only: IDs and canonical receipt fields are not recomputed
// or persisted. All actual actor-authored/received text stays byte-identical.
func privateLocationResourceViewsV1(views []CharacterResourceViewV2) []CharacterResourceViewV2 {
	out := append([]CharacterResourceViewV2(nil), views...)
	for i := range out {
		if out[i].KnownPlacement != nil {
			placement := *out[i].KnownPlacement
			placement.Location = fmt.Sprintf("第%d章该资源本人执行记录对应的地点", placement.AsOfChapter)
			out[i].KnownPlacement = &placement
		}
	}
	return out
}
