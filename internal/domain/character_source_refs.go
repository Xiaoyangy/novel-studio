package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
)

// This fixed policy label is not author provenance. It is included in new
// stimulus/observation Sources and in the host protocol digest; historical
// evidence without it retains its original byte-level verification contract.
const CharacterSourceRefPolicyV2 = "character-source-refs:opaque.v1"

var characterSourceRefPatternV2 = regexp.MustCompile(`^src_[0-9a-f]{64}$`)
var characterSourceDigestPatternV2 = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

func IsCharacterSourceRefV2(ref string) bool { return characterSourceRefPatternV2.MatchString(ref) }

// CharacterSourceRefV2 attests only the actor's visible access/perception
// context. It grants no knowledge of the underlying author text, and there is
// deliberately no exported reverse lookup. Already opaque references survive
// subsequent chapters unchanged; possession is checked against the actor's
// actual observation, never merely against this token's syntax.
func CharacterSourceRefV2(agentID, raw string) string {
	if raw == "" || IsCharacterSourceRefV2(raw) {
		return raw
	}
	sum := sha256.Sum256([]byte("character-source-ref.v1\x00" + agentID + "\x00" + raw))
	return "src_" + hex.EncodeToString(sum[:])
}

func CharacterSourceRefsV2(agentID string, raw []string) []string {
	var refs []string
	for _, ref := range raw {
		if strings.TrimSpace(ref) != "" {
			refs = append(refs, CharacterSourceRefV2(agentID, ref))
		}
	}
	return normalizeV2Strings(refs)
}

func HasCharacterSourceRefPolicyV2(sources []string) bool {
	for _, source := range sources {
		if strings.TrimSpace(source) == CharacterSourceRefPolicyV2 {
			return true
		}
	}
	return false
}

func validateCharacterObservationSourceRefsV2(observation CharacterObservationPacket) error {
	if !HasCharacterSourceRefPolicyV2(observation.Sources) {
		return nil
	}
	require := func(ref string, optional bool, field string) error {
		if (optional && ref == "") || IsCharacterSourceRefV2(ref) {
			return nil
		}
		// Never echo rejected author provenance into a model-visible error.
		return fmt.Errorf("opaque character source policy rejects raw %s", field)
	}
	for _, ref := range observation.Sources {
		if ref != CharacterWorkContinuationHistoryPolicyV1 && ref != CharacterResourceObservationTimePolicyV1 && ref != CharacterSourceRefPolicyV2 && ref != CharacterSelfExperiencePolicyV2 && ref != CharacterOperationalAvailabilityPolicyV1 && ref != CharacterSelfChronologyPolicyV1 && ref != CharacterWorkContinuationPolicyV1 && ref != CharacterActivationCyclePolicyV3 && ref != CharacterArbitrationRoundSourcesPolicyV1 && ref != CharacterWorkArtifactPolicyV1 && ref != CharacterRevisionFeedbackPolicyV1 {
			if err := require(ref, false, "sources"); err != nil {
				return err
			}
		}
	}
	for _, view := range observation.ResourceViews {
		for _, ref := range view.EvidenceRefs {
			if err := require(ref, false, "resource_views.evidence_refs"); err != nil {
				return err
			}
		}
		for _, ref := range view.Perception.EvidenceRefs {
			if err := require(ref, false, "resource_views.perception.evidence_refs"); err != nil {
				return err
			}
		}
	}
	for _, facts := range [][]CharacterAgentFact{observation.KnownFacts, observation.PerceivedEvents, observation.PublicRules} {
		for _, fact := range facts {
			if err := require(fact.Source, true, "fact.source"); err != nil {
				return err
			}
		}
	}
	for _, memory := range observation.Memory {
		if memory.SourceDigest != "" && !IsCharacterSourceRefV2(memory.SourceDigest) && !characterSourceDigestPatternV2.MatchString(memory.SourceDigest) {
			return fmt.Errorf("opaque character source policy rejects raw memory.source_digest")
		}
		for _, ref := range memory.KnowledgeRefs {
			if err := require(ref, false, "memory.knowledge_refs"); err != nil {
				return err
			}
		}
	}
	return nil
}

func sameCharacterResourcePerceptionV2(agentID string, before, after ResourcePerceptionV2) bool {
	before.EvidenceRefs = CharacterSourceRefsV2(agentID, before.EvidenceRefs)
	after.EvidenceRefs = CharacterSourceRefsV2(agentID, after.EvidenceRefs)
	return samePhysicalValueV2(before, after)
}

func characterContainsAllSourceRefsV2(agentID string, refs, wanted []string) bool {
	return physicalContainsAllRefsV2(CharacterSourceRefsV2(agentID, refs), CharacterSourceRefsV2(agentID, wanted))
}

// ValidateCharacterResourceViewsAgainstStimulusV2 is also the pre-model cache
// boundary. A re-signed observation must not drop the host source policy and
// reach a model before the final evidence bundle notices the mismatch.
func ValidateCharacterResourceViewsAgainstStimulusV2(stimulus WorldStimulusPacket, observation CharacterObservationPacket) error {
	if HasCharacterWorkArtifactPolicyV1(stimulus.Sources) != HasCharacterWorkArtifactPolicyV1(observation.Sources) {
		return fmt.Errorf("artifact observation policy differs from its world source")
	}
	if HasCharacterWorkArtifactPolicyV1(stimulus.Sources) && stimulus.PhysicalState != nil {
		views, err := BuildCharacterArtifactViewsV1(*stimulus.PhysicalState, observation.AgentID)
		if err != nil {
			return err
		}
		if !samePhysicalValueV2(views, observation.ArtifactViews) && !(len(views) == 0 && len(observation.ArtifactViews) == 0) {
			return fmt.Errorf("artifact observation differs from the owner's actual known versions")
		}
	}
	if stimulus.Version != WorldStimulusPacketV2Version || observation.Version != CharacterObservationV2Version || stimulus.PhysicalState == nil {
		return fmt.Errorf("v2 resource-view binding requires v2 stimulus, physical state and observation")
	}
	if observation.GenerationID != stimulus.GenerationID || observation.Chapter != stimulus.Chapter || observation.StimulusDigest != stimulus.Digest {
		return fmt.Errorf("resource-view observation is not bound to this stimulus")
	}
	if err := validateCharacterObservationSourceRefsV2(observation); err != nil {
		return err
	}
	return validateCharacterResourceViewsAgainstStimulusV2(stimulus, observation)
}

func validateCharacterResourceViewsAgainstStimulusV2(stimulus WorldStimulusPacket, observation CharacterObservationPacket) error {
	if HasCharacterWorkContinuationHistoryPolicyV1(stimulus.Sources) && (!HasCharacterWorkContinuationPolicyV1(stimulus.Sources) || !HasCharacterSelfChronologyPolicyV1(stimulus.Sources) || !physicalContainsRefV2(stimulus.Sources, CharacterActivationCyclePolicyV3)) {
		return fmt.Errorf("full-owner continuation history requires the v3 continuation and chronology policies")
	}
	for _, policy := range []string{CharacterWorkContinuationHistoryPolicyV1, CharacterResourceObservationTimePolicyV1, CharacterActivationCyclePolicyV3, CharacterArbitrationRoundSourcesPolicyV1, CharacterRevisionFeedbackPolicyV1} {
		if physicalContainsRefV2(stimulus.Sources, policy) != physicalContainsRefV2(observation.Sources, policy) {
			return fmt.Errorf("character observation round-source policy differs from its stimulus")
		}
	}
	if err := validateSelfChronologyStimulusV1(stimulus); err != nil {
		return err
	}
	if HasCharacterSelfChronologyPolicyV1(stimulus.Sources) != HasCharacterSelfChronologyPolicyV1(observation.Sources) {
		return fmt.Errorf("character observation self chronology policy differs from its stimulus")
	}
	if err := validateCharacterOperationalObservationBindingV1(stimulus, observation); err != nil {
		return err
	}
	if err := validateCharacterObservationCycleBinding(stimulus, observation); err != nil {
		return err
	}
	if HasCharacterSelfExperiencePolicyV2(stimulus.Sources) != HasCharacterSelfExperiencePolicyV2(observation.Sources) {
		return fmt.Errorf("character observation self-experience policy differs from its stimulus")
	}
	if HasCharacterSelfExperiencePolicyV2(stimulus.Sources) {
		for _, actor := range stimulus.PhysicalState.Actors {
			for _, holding := range actor.Resources {
				if holding.Perception.Kind != "unaware" && strings.TrimSpace(holding.PerceivedLabel) == "" {
					return fmt.Errorf("self-experience stimulus lacks a safe static resource label")
				}
			}
		}
		experiences, progress, err := BuildCharacterSelfObservationV2(*stimulus.PhysicalState, observation.AgentID)
		if err != nil {
			return err
		}
		if (!samePhysicalValueV2(experiences, observation.SelfExperiences) && !(len(experiences) == 0 && len(observation.SelfExperiences) == 0)) || (!samePhysicalValueV2(progress, observation.TaskProgress) && !(len(progress) == 0 && len(observation.TaskProgress) == 0)) {
			return fmt.Errorf("self experience observation differs from the exact owner's confirmed state")
		}
	}
	opaque := HasCharacterSourceRefPolicyV2(stimulus.Sources)
	if opaque != HasCharacterSourceRefPolicyV2(observation.Sources) {
		return fmt.Errorf("character observation source policy differs from its stimulus")
	}
	views, err := BuildCharacterResourceViewsV2(*stimulus.PhysicalState, observation.AgentID)
	if err != nil {
		return err
	}
	if samePhysicalValueV2(views, observation.ResourceViews) || (len(views) == 0 && len(observation.ResourceViews) == 0) {
		return nil
	}
	if !opaque {
		legacy, err := buildCharacterResourceViewsV2(*stimulus.PhysicalState, observation.AgentID, false)
		if err != nil {
			return err
		}
		if samePhysicalValueV2(legacy, observation.ResourceViews) {
			return nil
		}
	}
	return fmt.Errorf("v2 observation resource_views differ from the actor's physical perception")
}
