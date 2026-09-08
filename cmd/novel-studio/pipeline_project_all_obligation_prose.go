package main

import (
	"fmt"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

// The registry contract remains the immutable obligation identity. Only its
// authenticated, host-added v2 source suffix is absent from the prose view.
// This is deliberately not a general bracket/number/metadata text scrubber.
func pipelineProjectAllObligationProseContract(st *store.Store, registry domain.ObligationRegistryV2, obligation domain.ObligationV2) (string, bool, error) {
	contract := strings.TrimSpace(obligation.Contract)
	source := obligation.Origin.SourceDigest
	if obligation.Kind != domain.ObligationCharacterV2 || source == pipelineProjectAllDigest(contract) ||
		!strings.HasPrefix(source, "sha256:") || len(source) != len("sha256:")+64 {
		// v1 used hash(contract), including when user prose looked like a tag.
		return contract, false, nil
	}
	suffix := "；因果来源：" + strings.TrimPrefix(source, "sha256:")[:20] + "〕"
	start := strings.LastIndex(contract, "〔角色：")
	if start < 0 || !strings.HasSuffix(contract, suffix) {
		return contract, false, nil
	}
	character := strings.TrimSuffix(contract[start+len("〔角色："):], suffix)
	text := contract[:start]
	if strings.TrimSpace(text) == "" || strings.TrimSpace(character) == "" || strings.ContainsAny(character, "〔〕\n\r") {
		return contract, false, nil
	}
	if err := domain.ValidateObligationRegistryV2(registry); err != nil {
		return "", false, fmt.Errorf("project-all obligation prose registry: %w", err)
	}
	bound := false
	for _, evidence := range obligation.Evidence {
		if evidence.Chapter == obligation.Origin.Chapter && evidence.SourceDigest == source && evidence.Detail == obligation.Contract {
			bound = true
			break
		}
	}
	if !bound {
		return "", false, fmt.Errorf("project-all source-qualified obligation %s lacks exact creation evidence", obligation.ID)
	}
	// A current-arc shadow retains its exact simulations. Cross-arc shadows
	// intentionally do not; in that case the verified carried registry retains
	// the ID, full source digest and exact creation evidence without live reads.
	if st != nil {
		sim, err := st.LoadChapterWorldSimulation(obligation.Origin.Chapter)
		if err != nil {
			return "", false, err
		}
		if sim != nil {
			matched := false
			if sim.GenerationID == obligation.Origin.GenerationID && sim.CharacterAgentProtocol != nil && sim.CharacterAgentProtocol.Version == domain.CharacterAgentDecisionProtocolV2Version {
				for _, decision := range sim.CharacterDecisions {
					for _, effect := range decision.ButterflyEffects {
						candidate, digest := pipelineProjectAllCharacterObligationSource(*sim, decision, effect)
						if candidate == contract && digest == source {
							matched = true
						}
					}
				}
			}
			if !matched {
				return "", false, fmt.Errorf("project-all source-qualified obligation %s differs from its origin simulation", obligation.ID)
			}
		}
	}
	return text, true, nil
}
