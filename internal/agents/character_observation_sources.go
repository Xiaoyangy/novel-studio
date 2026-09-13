package agents

import (
	"encoding/hex"
	"sort"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

// Source metadata attests only the already visible view/fact. It must not
// carry author notes, paths or hidden contents into the character prompt.
// Canonical memory and world sources remain unchanged, including their roots.
func projectCharacterObservationSourcesV2(observation *domain.CharacterObservationPacket) {
	if observation == nil || observation.Version != domain.CharacterObservationV2Version {
		return
	}
	agentID := observation.AgentID
	selfPolicy := domain.HasCharacterSelfExperiencePolicyV2(observation.Sources)
	operationalPolicy := domain.HasCharacterOperationalAvailabilityPolicyV1(observation.Sources)
	chronologyPolicy := domain.HasCharacterSelfChronologyPolicyV1(observation.Sources)
	continuationPolicy := domain.HasCharacterWorkContinuationPolicyV1(observation.Sources)
	newPolicies := map[string]bool{}
	sources := make([]string, 0, len(observation.Sources)+1)
	for _, source := range observation.Sources {
		if source == domain.CharacterIncomingMaterialReadPolicyV1 || source == domain.CharacterSurfaceInspectionPolicyV1 || source == domain.CharacterSelfCompletionViewPolicyV1 || source == domain.CharacterWorkContinuationHistoryPolicyV1 || source == domain.CharacterResourceObservationTimePolicyV1 || source == domain.CharacterActivationCyclePolicyV3 || source == domain.CharacterArbitrationRoundSourcesPolicyV1 || source == domain.CharacterWorkArtifactPolicyV1 || source == domain.CharacterRevisionFeedbackPolicyV1 {
			newPolicies[source] = true
			continue
		}
		if source != domain.CharacterSourceRefPolicyV2 && source != domain.CharacterSelfExperiencePolicyV2 && source != domain.CharacterOperationalAvailabilityPolicyV1 && source != domain.CharacterSelfChronologyPolicyV1 && source != domain.CharacterWorkContinuationPolicyV1 {
			sources = append(sources, source)
		}
	}
	observation.Sources = append(domain.CharacterSourceRefsV2(agentID, sources), domain.CharacterSourceRefPolicyV2)
	if selfPolicy {
		observation.Sources = append(observation.Sources, domain.CharacterSelfExperiencePolicyV2)
	}
	if operationalPolicy {
		observation.Sources = append(observation.Sources, domain.CharacterOperationalAvailabilityPolicyV1)
	}
	if chronologyPolicy {
		observation.Sources = append(observation.Sources, domain.CharacterSelfChronologyPolicyV1)
	}
	if continuationPolicy {
		observation.Sources = append(observation.Sources, domain.CharacterWorkContinuationPolicyV1)
	}
	for policy := range newPolicies {
		observation.Sources = append(observation.Sources, policy)
	}
	sort.Strings(observation.Sources)
	projectFacts := func(facts []domain.CharacterAgentFact) []domain.CharacterAgentFact {
		out := append([]domain.CharacterAgentFact(nil), facts...)
		for i := range out {
			if out[i].Source != "" {
				out[i].Source = domain.CharacterSourceRefV2(agentID, out[i].Source)
			}
		}
		return out
	}
	observation.KnownFacts = projectFacts(observation.KnownFacts)
	observation.PerceivedEvents = projectFacts(observation.PerceivedEvents)
	observation.PublicRules = projectFacts(observation.PublicRules)
	memory := append([]domain.CharacterAgentMemoryFact(nil), observation.Memory...)
	for i := range memory {
		memory[i].KnowledgeRefs = domain.CharacterSourceRefsV2(agentID, memory[i].KnowledgeRefs)
		// Proper digests are already opaque, but historical validators allowed
		// arbitrary nonempty source_digest strings. Do not expose those notes.
		if memory[i].SourceDigest != "" && !characterObservationDigestOpaque(memory[i].SourceDigest) {
			memory[i].SourceDigest = domain.CharacterSourceRefV2(agentID, memory[i].SourceDigest)
		}
	}
	observation.Memory = memory
}

func characterObservationDigestOpaque(value string) bool {
	if !strings.HasPrefix(value, "sha256:") || len(value) != len("sha256:")+64 || value != strings.ToLower(value) {
		return false
	}
	raw, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil && len(raw) == 32
}
