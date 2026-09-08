package main

import (
	"fmt"
	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
	"os"
	"path/filepath"
	"strings"
)

// Defaults apply to new attempts, not to an already pinned arc. Explicit
// restart/successor changes its attempt identity and therefore selects fresh
// configuration. Source/model drift is still checked by the normal identity.
func pinPipelineActivationForExistingAttempt(cfg *bootstrap.Config, st *store.Store, base, first, last int, attempt string) error {
	generation, err := pipelineGenerationForActivationAttempt(st, base, first, last, attempt)
	if err != nil {
		return err
	}
	if generation == nil || generation.CharacterAgentProtocol != cfg.CharacterAgentsProtocolVersion() {
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

// A generation is durably created before its derived cursor or first actor
// input. Recover that crash window from validated generation envelopes too.
func pipelineGenerationForActivationAttempt(st *store.Store, base, first, last int, attempt string) (*domain.PlanningGenerationV2, error) {
	matches := func(g *domain.PlanningGenerationV2) bool {
		return g != nil && g.BaseCanonChapter == base && g.FirstProjectedChapter == first && g.LastProjectedChapter == last && g.AttemptID == attempt
	}
	p := st.ProjectedV2()
	if cursor, err := p.LoadProjectionCursor(); err != nil {
		return nil, err
	} else if cursor != nil {
		g, err := p.LoadBuildingGeneration(cursor.GenerationID)
		if err != nil {
			return nil, err
		}
		if g == nil {
			g, err = p.LoadSealedGeneration(cursor.GenerationID)
		}
		if err != nil {
			return nil, err
		}
		if matches(g) {
			return g, nil
		}
	}
	var found *domain.PlanningGenerationV2
	for _, dir := range []string{".building", "generations"} {
		entries, err := os.ReadDir(filepath.Join(st.Dir(), "meta", "planning", "v2", dir))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
				continue
			}
			load := p.LoadBuildingGeneration
			if dir == "generations" {
				load = p.LoadSealedGeneration
			}
			g, err := load(entry.Name())
			if err != nil {
				return nil, err
			}
			if !matches(g) {
				continue
			}
			if found != nil && found.GenerationID != g.GenerationID {
				return nil, fmt.Errorf("project-all attempt has multiple frozen generations; explicit recovery is required")
			}
			found = g
		}
	}
	return found, nil
}
