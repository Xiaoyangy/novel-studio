package domain

import "fmt"

// VerifiedCharacterActivationChapter is an operation-local authority over a
// complete, detached chapter proof. It has no serializable authority fields:
// callers must read and verify their actual sources again for a new operation.
// It is not a cache keyed by the evidence's self-reported digest.
type VerifiedCharacterActivationChapter struct {
	evidence CharacterActivationChapterEvidence
	steps    []VerifiedCharacterActivationStep
	verified bool
}

// VerifyCharacterActivationChapter authenticates every input, cycle, readiness
// audit and final cursor once. Subsequent consumers share only private source
// authority; neither caller-owned values nor returned projections alias it.
func VerifyCharacterActivationChapter(evidence CharacterActivationChapterEvidence) (VerifiedCharacterActivationChapter, error) {
	var result VerifiedCharacterActivationChapter
	owned, err := cloneVerifiedActivationValue(evidence)
	if err != nil {
		return result, err
	}
	if !hasCharacterContinuationCycles(owned.Cycles) {
		// Preserve the established legacy validator and its normalization/hash.
		if err := ValidateCharacterActivationChapterEvidence(owned); err != nil {
			return result, err
		}
	} else {
		value, context, err := prepareCharacterActivationChapterEnvelope(owned)
		if err != nil {
			return result, err
		}
		prefix, err := rebuildCharacterActivationChapterPrefix(value, context)
		if err != nil {
			return result, err
		}
		got := value.Digest
		value.Digest = ""
		wanted, err := characterAgentDigest(value)
		if err != nil || got != wanted {
			return result, fmt.Errorf("chapter activation evidence digest mismatch")
		}
		// Prefix owns these immutable runtime values. No public getter or JSON
		// copy is needed just to pass authority to another internal consumer.
		result.steps = prefix.steps
	}
	result.evidence, result.verified = owned, true
	return result, nil
}

// Evidence returns a detached copy, not a way to transfer verification status.
func (v VerifiedCharacterActivationChapter) Evidence() CharacterActivationChapterEvidence {
	if !v.verified {
		return CharacterActivationChapterEvidence{}
	}
	value, _ := cloneVerifiedActivationValue(v.evidence)
	return value
}

func (v VerifiedCharacterActivationChapter) BuildSimulation(baseTickID string, sources []string) (ChapterWorldSimulation, error) {
	if !v.verified {
		return ChapterWorldSimulation{}, fmt.Errorf("activation simulation requires verified chapter source authority")
	}
	// Older nested projection helpers normalize slices. Give them a working
	// copy, and detach their output so repeated/concurrent consumers are safe.
	simulation, err := buildCharacterActivationSimulation(v.Evidence(), baseTickID, sources, v.steps)
	if err != nil {
		return simulation, err
	}
	return cloneVerifiedActivationValue(simulation)
}

func (v VerifiedCharacterActivationChapter) ValidateSimulation(simulation ChapterWorldSimulation) error {
	if !v.verified {
		return fmt.Errorf("activation simulation requires verified chapter source authority")
	}
	return validateCharacterActivationSimulation(simulation, v)
}

func (v VerifiedCharacterActivationChapter) NewPlanGroundingInput(plan ChapterPlan, simulation ChapterWorldSimulation, protocol string) (PlanGroundingInput, error) {
	if err := v.ValidateSimulation(simulation); err != nil {
		return PlanGroundingInput{}, err
	}
	input, err := newActivationPlanGroundingInput(plan, simulation, v.Evidence(), protocol, v.steps)
	if err != nil {
		return input, err
	}
	return cloneVerifiedActivationValue(input)
}
