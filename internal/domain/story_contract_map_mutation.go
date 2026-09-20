package domain

import (
	"fmt"
	"strings"
)

// ValidateStoryContractMapMutationModes checks model-submitted map mutations,
// not ordinary reads of persisted legacy outlines. A legacy omitted mode may
// survive only for the same already-stored contract ID; new references must
// explicitly state their mode. Existing effective modes cannot change (legacy
// omission means payoff), and explicit modes cannot be erased. Source identity,
// placement and coverage remain the responsibility
// of StoryContractSkeletonIssuesForPolicy.
func ValidateStoryContractMapMutationModes(before, after []VolumeOutline, policy string) error {
	if err := ValidateStoryContractEvidencePolicy(policy); err != nil {
		return err
	}
	stored := make(map[string]string)
	for _, volume := range before {
		for _, arc := range volume.Arcs {
			for _, ref := range arc.ContractRefs {
				mode := strings.TrimSpace(ref.EvidenceMode)
				if previous, exists := stored[ref.ID]; exists && previous != mode {
					return fmt.Errorf("contract %q has conflicting stored evidence modes", ref.ID)
				}
				stored[ref.ID] = mode
			}
		}
	}
	for _, volume := range after {
		for _, arc := range volume.Arcs {
			for _, ref := range arc.ContractRefs {
				mode := strings.TrimSpace(ref.EvidenceMode)
				previous, existed := stored[ref.ID]
				if mode == "" {
					if policy != "" || !existed || previous != "" {
						return fmt.Errorf("contract %q must preserve or explicitly submit its evidence mode", ref.ID)
					}
					continue
				}
				if mode != StoryContractEvidencePayoff && mode != StoryContractEvidenceContinuous {
					return fmt.Errorf("contract %q has invalid evidence mode %q", ref.ID, ref.EvidenceMode)
				}
				if existed && StoryContractEvidenceMode(StoryContractRef{EvidenceMode: previous}) != mode {
					return fmt.Errorf("contract %q cannot change its stored effective evidence mode", ref.ID)
				}
			}
		}
	}
	return nil
}
