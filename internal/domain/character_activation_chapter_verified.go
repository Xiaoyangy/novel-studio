package domain

import "fmt"

func prepareCharacterActivationChapterEnvelope(value CharacterActivationChapterEvidence) (CharacterActivationChapterEvidence, CharacterReadinessContext, error) {
	var context CharacterReadinessContext
	// Nested validators normalize slices. Never mutate a published proof that
	// may be shared by concurrent readers.
	owned, err := cloneVerifiedActivationValue(value)
	if err != nil {
		return value, context, err
	}
	value = owned
	if value.Version == "" {
		value.Version = CharacterActivationChapterEvidenceVersion
	}
	n := len(value.Cycles)
	if value.Version != CharacterActivationChapterEvidenceVersion || n == 0 || len(value.Inputs) != n || len(value.Reviews) != n || value.Session.Phase != "ready" {
		return value, context, fmt.Errorf("chapter activation evidence is incomplete or not ready")
	}
	context, err = FinalizeCharacterReadinessContext(value.Context)
	if err != nil {
		return value, context, err
	}
	if context.Digest != value.Context.Digest || context.GenerationID != value.Session.GenerationID || context.Chapter != value.Session.Chapter || context.Digest != value.Session.ChapterContextDigest {
		return value, context, fmt.Errorf("chapter activation evidence has a foreign context/session")
	}
	if err := validatePlanningV2Digest("chapter activation protocol", value.ProtocolDigest); err != nil {
		return value, context, err
	}
	if err := ValidateCharacterActivationSession(value.Session); err != nil {
		return value, context, err
	}
	return value, context, nil
}

func hasCharacterContinuationCycles(cycles []CharacterActivationCycle) bool {
	for _, cycle := range cycles {
		if cycle.Version == CharacterActivationCycleV2Version || cycle.Version == CharacterActivationCycleV3Version {
			return true
		}
	}
	return false
}

// Called after the chapter envelope/context checks. The flat input/cycle/audit
// rows are replayed once in order; no serialized badge or recursive ledger can
// replace the source prefix. Audit verification remains mandatory at each step.
func finalizeCharacterActivationChapterFromSteps(value CharacterActivationChapterEvidence, context CharacterReadinessContext) (CharacterActivationChapterEvidence, error) {
	if _, err := rebuildCharacterActivationChapterPrefix(value, context); err != nil {
		return value, err
	}
	value.Digest = ""
	var err error
	value.Digest, err = characterAgentDigest(value)
	return value, err
}

// Rebuild and authenticate all flat sources/audits exactly once. The returned
// steps are runtime capabilities, never JSON badges or self-signed summaries.
func VerifiedStepsForCharacterActivationChapter(evidence CharacterActivationChapterEvidence) ([]VerifiedCharacterActivationStep, error) {
	value, context, err := prepareCharacterActivationChapterEnvelope(evidence)
	if err != nil {
		return nil, err
	}
	prefix, err := rebuildCharacterActivationChapterPrefix(value, context)
	if err != nil {
		return nil, err
	}
	got := value.Digest
	value.Digest = ""
	wanted, err := characterAgentDigest(value)
	if err != nil || got != wanted {
		return nil, fmt.Errorf("chapter activation evidence digest mismatch")
	}
	return prefix.Steps(), nil
}

func rebuildCharacterActivationChapterPrefix(value CharacterActivationChapterEvidence, context CharacterReadinessContext) (VerifiedCharacterActivationPrefix, error) {
	var empty VerifiedCharacterActivationPrefix
	first := value.Inputs[0].Stimulus
	if first.Version != WorldStimulusPacketV2Version || first.PhysicalState == nil || first.StoryClock == nil {
		return empty, fmt.Errorf("activation chapter lacks a typed initial source")
	}
	session, err := NewCharacterActivationSession(context.GenerationID, context.Chapter, context.Digest, *first.PhysicalState, first.StoryClock.CurrentDay, value.Session.MaxCycles)
	if err != nil {
		return empty, err
	}
	prefix, err := NewVerifiedCharacterActivationPrefix(session)
	if err != nil {
		return empty, err
	}
	for i, cycle := range value.Cycles {
		if cycle.Evidence.ProtocolDigest != value.ProtocolDigest {
			return empty, fmt.Errorf("chapter activation evidence mixes cycle protocols")
		}
		_, pending, err := VerifyCharacterActivationStep(prefix, value.Inputs[i], cycle)
		if err != nil {
			return empty, err
		}
		audit := value.Reviews[i]
		if audit.Receipt.Version != CharacterReadinessReviewedVersion {
			return empty, fmt.Errorf("activation chapter requires an audited readiness result for every cycle")
		}
		if err := ValidateCharacterReadinessReviewAudit(audit); err != nil {
			return empty, err
		}
		// These private values are immutable. Avoid deep-copying all complete
		// source inputs again just to produce their compact readiness trace.
		expected, err := NewCharacterReadinessReviewInputFromSteps(context, pending.session, pending.steps, audit.Input.ReviewProtocol)
		if err != nil {
			return empty, err
		}
		want, err := CharacterReadinessReviewInputDigest(expected)
		if err != nil {
			return empty, err
		}
		got, err := CharacterReadinessReviewInputDigest(audit.Input)
		if err != nil || want != got {
			return empty, fmt.Errorf("activation readiness altered its verified input, source trace or actual session")
		}
		prefix, err = ApplyVerifiedCharacterActivationReadiness(pending, audit.Receipt)
		if err != nil {
			return empty, err
		}
	}
	if !samePhysicalValueV2(prefix.session, value.Session) {
		return empty, fmt.Errorf("activation final cursor differs from the complete flat evidence chain")
	}
	return prefix, nil
}
