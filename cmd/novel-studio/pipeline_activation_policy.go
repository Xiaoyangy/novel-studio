package main

import (
	"fmt"
	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

// Defaults apply to new attempts, not to an already pinned arc. Explicit
// restart/successor changes its attempt identity and therefore selects fresh
// configuration. Source/model drift is still checked by the normal identity.
func pinPipelineActivationForExistingAttempt(cfg *bootstrap.Config, st *store.Store, base, first, last int, attempt string) error {
	cursor, err := st.ProjectedV2().LoadProjectionCursor()
	if err != nil || cursor == nil {
		return err
	}
	generation, err := st.ProjectedV2().LoadBuildingGeneration(cursor.GenerationID)
	if err != nil {
		return err
	}
	if generation == nil {
		generation, err = st.ProjectedV2().LoadSealedGeneration(cursor.GenerationID)
		if err != nil {
			return err
		}
	}
	if generation == nil || generation.BaseCanonChapter != base || generation.FirstProjectedChapter != first || generation.LastProjectedChapter != last || generation.AttemptID != attempt || generation.CharacterAgentProtocol != cfg.CharacterAgentsProtocolVersion() {
		return nil
	}
	if cfg.CharacterAgents.ExecutionPolicy == "v3" && generation.CharacterActivationPolicy != domain.CharacterActivationCyclePolicyV3 {
		return fmt.Errorf("execution_policy=v3 requires a new generation; this attempt is frozen to %s; restart or create a successor without rewriting its evidence", generation.CharacterActivationPolicy)
	}
	cfg.CharacterAgents.MaxActivationCycles = 1
	cfg.CharacterAgents.ExecutionPolicy = "v1"
	switch generation.CharacterActivationPolicy {
	case domain.CharacterActivationCyclePolicy:
		cfg.CharacterAgents.MaxActivationCycles = generation.MaxCharacterActivationCycles
	case domain.CharacterActivationCyclePolicyV2:
		cfg.CharacterAgents.ExecutionPolicy = "v2"
		cfg.CharacterAgents.MaxActivationCycles = generation.MaxCharacterActivationCycles
	case domain.CharacterActivationCyclePolicyV3:
		cfg.CharacterAgents.ExecutionPolicy = "v3"
		cfg.CharacterAgents.MaxActivationCycles = generation.MaxCharacterActivationCycles
	}
	return nil
}
