package domain

import (
	"encoding/json"
	"fmt"
)

const CharacterActivationChapterEvidenceVersion = "character-activation-chapter.v1"

// Complete host-only chapter proof. It keeps every original cycle and input
// snapshot; a ready cursor or the last arbitration alone cannot stand in for
// the whole chapter. This is projected evidence, never accepted prose/canon.
type CharacterActivationChapterEvidence struct {
	Version        string                          `json:"version"`
	Context        CharacterReadinessContext       `json:"context"`
	Session        CharacterActivationSession      `json:"session"`
	Cycles         []CharacterActivationCycle      `json:"cycles"`
	Inputs         []CharacterActivationInputSet   `json:"inputs"`
	Reviews        []CharacterReadinessReviewAudit `json:"reviews"`
	ProtocolDigest string                          `json:"protocol_digest"`
	Digest         string                          `json:"digest"`
}

func FinalizeCharacterActivationChapterEvidence(value CharacterActivationChapterEvidence) (CharacterActivationChapterEvidence, error) {
	value, context, err := prepareCharacterActivationChapterEnvelope(value)
	if err != nil {
		return value, err
	}
	if hasCharacterContinuationCycles(value.Cycles) {
		return finalizeCharacterActivationChapterFromSteps(value, context)
	}
	if value.Cycles[0].Evidence.Stimulus.PhysicalState == nil {
		return value, fmt.Errorf("chapter activation evidence lacks its original physical baseline")
	}
	session, err := NewCharacterActivationSession(context.GenerationID, context.Chapter, context.Digest, *value.Cycles[0].Evidence.Stimulus.PhysicalState, value.Cycles[0].StartDay, value.Session.MaxCycles)
	if err != nil {
		return value, err
	}
	for i, cycle := range value.Cycles {
		if cycle.Evidence.ProtocolDigest != value.ProtocolDigest {
			return value, fmt.Errorf("chapter activation evidence mixes cycle protocols")
		}
		if err := ValidateCharacterActivationInputsForCycle(value.Inputs[i], cycle); err != nil {
			return value, err
		}
		session, err = AppendCharacterActivationCycle(session, cycle)
		if err != nil {
			return value, err
		}
		audit := value.Reviews[i]
		if audit.Receipt.Version != CharacterReadinessReviewedVersion {
			return value, fmt.Errorf("chapter activation requires audited model readiness for every cycle")
		}
		if err := ValidateCharacterReadinessReviewAudit(audit); err != nil {
			return value, err
		}
		expected, err := NewCharacterReadinessReviewInput(context, session, value.Cycles[:i+1], audit.Input.ReviewProtocol)
		if err != nil {
			return value, err
		}
		want, err := CharacterReadinessReviewInputDigest(expected)
		if err != nil {
			return value, err
		}
		got, err := CharacterReadinessReviewInputDigest(audit.Input)
		if err != nil || want != got {
			return value, fmt.Errorf("chapter activation readiness changed its source context/trace")
		}
		session, err = ApplyCharacterChapterReadiness(session, audit.Receipt)
		if err != nil {
			return value, err
		}
	}
	left, _ := json.Marshal(session)
	right, _ := json.Marshal(value.Session)
	if string(left) != string(right) {
		return value, fmt.Errorf("chapter activation final cursor differs from its full evidence chain")
	}
	value.Digest = ""
	value.Digest, err = characterAgentDigest(value)
	return value, err
}

func ValidateCharacterActivationChapterEvidence(value CharacterActivationChapterEvidence) error {
	verified, err := FinalizeCharacterActivationChapterEvidence(value)
	if err != nil {
		return err
	}
	if verified.Digest != value.Digest {
		return fmt.Errorf("chapter activation evidence digest mismatch")
	}
	return nil
}
