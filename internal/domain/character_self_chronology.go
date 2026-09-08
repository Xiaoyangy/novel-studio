package domain

import (
	"fmt"
	"sort"
	"strings"
)

// This is opt-in. Legacy receipts keep their exact IDs, derivation and ordering.
const CharacterSelfChronologyPolicyV1 = "character-self-chronology:evaluation.v1"

// The host freezes this context before building the input set. InputSetDigest
// cannot be embedded here (the input set contains this stimulus). The eventual
// fact binds the finalized stimulus and proposal; the cycle binds the input set.
type CharacterSelfEvaluationContextV1 struct {
	Version              string  `json:"version"`
	GenerationID         string  `json:"generation_id"`
	Chapter              int     `json:"chapter"`
	Cycle                int     `json:"cycle"`
	PreviousCycleDigest  string  `json:"previous_cycle_digest,omitempty"`
	ChapterContextDigest string  `json:"chapter_context_digest"`
	SessionDigest        string  `json:"session_digest"`
	BeforePhysicalRoot   string  `json:"before_physical_root"`
	CurrentDay           float64 `json:"current_day"`
	Digest               string  `json:"digest"`
}

// EvaluatedAtDay is the actual evaluation boundary, NOT invented work time.
// All records evaluated in the same cycle share it. Ordinal is host-derived
// from canonical actual execution intervals, then explicit unexecuted tasks.
type CharacterSelfEvaluationV1 struct {
	Version        string  `json:"version"`
	GenerationID   string  `json:"generation_id"`
	Cycle          int     `json:"cycle"`
	ContextDigest  string  `json:"context_digest"`
	StimulusDigest string  `json:"stimulus_digest"`
	EvaluatedAtDay float64 `json:"evaluated_at_day"`
	Ordinal        int     `json:"ordinal"`
}

// An explicit migration anchor, not a rewritten history. The original facts
// remain in SelfExperiences; only their exact digest/count and old aggregate
// are duplicated. A model cannot supply or change this host-owned baseline.
type CharacterSelfChronologyBaselineV1 struct {
	Version            string                    `json:"version"`
	AgentID            string                    `json:"agent_id"`
	SourcePhysicalRoot string                    `json:"source_physical_root"`
	ExperienceDigest   string                    `json:"experience_digest"`
	ExperienceCount    int                       `json:"experience_count"`
	TaskProgress       []CharacterTaskProgressV2 `json:"task_progress,omitempty"`
	Digest             string                    `json:"digest"`
}

func HasCharacterSelfChronologyPolicyV1(sources []string) bool {
	return physicalContainsRefV2(sources, CharacterSelfChronologyPolicyV1)
}

func NewCharacterSelfEvaluationContextV1(session CharacterActivationSession) (*CharacterSelfEvaluationContextV1, error) {
	if err := ValidateCharacterActivationSession(session); err != nil {
		return nil, err
	}
	if session.Phase != "collecting" {
		return nil, fmt.Errorf("self evaluation requires a collecting activation session")
	}
	if len(session.CycleDigests) >= session.MaxCycles {
		return nil, fmt.Errorf("self evaluation exceeds the current session cycle bound")
	}
	context := &CharacterSelfEvaluationContextV1{Version: CharacterSelfChronologyPolicyV1,
		GenerationID: session.GenerationID, Chapter: session.Chapter, Cycle: len(session.CycleDigests) + 1,
		ChapterContextDigest: session.ChapterContextDigest, SessionDigest: session.Digest,
		BeforePhysicalRoot: session.CurrentPhysicalRoot, CurrentDay: session.CurrentDay}
	if len(session.CycleDigests) > 0 {
		context.PreviousCycleDigest = session.CycleDigests[len(session.CycleDigests)-1]
	}
	var err error
	context.Digest, err = selfEvaluationContextDigestV1(*context)
	if err != nil {
		return nil, err
	}
	return context, validateSelfEvaluationContextV1(*context)
}

func ValidateCharacterSelfEvaluationContextAgainstSessionV1(context CharacterSelfEvaluationContextV1, session CharacterActivationSession) error {
	want, err := NewCharacterSelfEvaluationContextV1(session)
	if err != nil {
		return err
	}
	if !samePhysicalValueV2(context, *want) {
		return fmt.Errorf("self evaluation context differs from its current host session")
	}
	return nil
}

func selfEvaluationContextDigestV1(context CharacterSelfEvaluationContextV1) (string, error) {
	context.Digest = ""
	return characterAgentDigest(context)
}

func validateSelfEvaluationContextV1(context CharacterSelfEvaluationContextV1) error {
	if context.Version != CharacterSelfChronologyPolicyV1 || context.Cycle > 64 || !finiteStoryDay(context.CurrentDay) || context.CurrentDay < 0 {
		return fmt.Errorf("invalid self evaluation context")
	}
	if _, err := CharacterActivationCycleSourceToken(context.GenerationID, context.Chapter, context.Cycle, context.ChapterContextDigest, context.PreviousCycleDigest); err != nil {
		return err
	}
	for _, digest := range []string{context.SessionDigest, context.BeforePhysicalRoot} {
		if !characterSourceDigestPatternV2.MatchString(digest) {
			return fmt.Errorf("self evaluation lacks a host source digest")
		}
	}
	want, err := selfEvaluationContextDigestV1(context)
	if err != nil || want != context.Digest {
		return fmt.Errorf("self evaluation context digest mismatch")
	}
	return nil
}

func baselineDigestV1(baseline CharacterSelfChronologyBaselineV1) (string, error) {
	baseline.Digest = ""
	return characterAgentDigest(baseline)
}

func cloneSelfProgressV1(progress []CharacterTaskProgressV2) []CharacterTaskProgressV2 {
	if progress == nil {
		return nil
	}
	out := append([]CharacterTaskProgressV2{}, progress...)
	for i := range out {
		out[i].Target = physicalNumberCopyV2(out[i].Target)
	}
	return out
}

// Prepare only creates an explicit anchor from already valid legacy state.
// Existing anchored actors are preserved, including across new generations.
// Callers must bind the returned state as the new session's initial state.
func PrepareCharacterSelfChronologyStateV1(state WorldPhysicalStateV2) (WorldPhysicalStateV2, error) {
	out, err := PrepareCharacterSelfExperienceStateV2(state)
	if err != nil {
		return out, err
	}
	root, err := CharacterPhysicalRootForCycle(out)
	if err != nil {
		return out, err
	}
	for i := range out.Actors {
		actor := &out.Actors[i]
		if actor.SelfChronologyBaseline != nil {
			continue
		}
		digest, err := characterAgentDigest(actor.SelfExperiences)
		if err != nil {
			return out, err
		}
		baseline := &CharacterSelfChronologyBaselineV1{Version: CharacterSelfChronologyPolicyV1, AgentID: actor.AgentID,
			SourcePhysicalRoot: root, ExperienceDigest: digest, ExperienceCount: len(actor.SelfExperiences), TaskProgress: cloneSelfProgressV1(actor.TaskProgress)}
		baseline.Digest, err = baselineDigestV1(*baseline)
		if err != nil {
			return out, err
		}
		actor.SelfChronologyBaseline = baseline
	}
	return FinalizeWorldPhysicalStateV2(out)
}

func ValidateCharacterSelfChronologyBaselineTransitionV1(before, prepared WorldPhysicalStateV2) error {
	want, err := PrepareCharacterSelfChronologyStateV1(before)
	if err != nil {
		return err
	}
	if !samePhysicalValueV2(want, prepared) {
		return fmt.Errorf("self chronology baseline is not the exact host-prepared source state")
	}
	return nil
}

func validateSelfChronologyStimulusV1(stimulus WorldStimulusPacket) error {
	policy := HasCharacterSelfChronologyPolicyV1(stimulus.Sources)
	if !policy {
		if stimulus.SelfEvaluationContext != nil {
			return fmt.Errorf("self evaluation context requires the explicit chronology policy")
		}
		if stimulus.PhysicalState != nil {
			for _, actor := range stimulus.PhysicalState.Actors {
				if actor.SelfChronologyBaseline != nil {
					return fmt.Errorf("anchored self chronology cannot resume under the legacy policy")
				}
			}
		}
		return nil
	}
	if !HasCharacterSelfExperiencePolicyV2(stimulus.Sources) || stimulus.Version != WorldStimulusPacketV2Version || stimulus.PhysicalState == nil || stimulus.StoryClock == nil || stimulus.SelfEvaluationContext == nil {
		return fmt.Errorf("self chronology requires explicit host context, physical baseline and actual clock")
	}
	context := *stimulus.SelfEvaluationContext
	if err := validateSelfEvaluationContextV1(context); err != nil {
		return err
	}
	token, _ := CharacterActivationCycleSourceToken(context.GenerationID, context.Chapter, context.Cycle, context.ChapterContextDigest, context.PreviousCycleDigest)
	tokens := 0
	for _, source := range stimulus.Sources {
		if strings.HasPrefix(source, CharacterActivationCycleSourcePrefix) {
			tokens++
			if source != token {
				return fmt.Errorf("self evaluation has a foreign cycle source")
			}
		}
	}
	root, err := CharacterPhysicalRootForCycle(*stimulus.PhysicalState)
	if err != nil {
		return err
	}
	if tokens != 1 || stimulus.GenerationID != context.GenerationID || stimulus.Chapter != context.Chapter || root != context.BeforePhysicalRoot || stimulus.StoryClock.CurrentDay != context.CurrentDay {
		return fmt.Errorf("self evaluation differs from the frozen cycle, physical state or actual clock")
	}
	for _, actor := range stimulus.PhysicalState.Actors {
		if actor.SelfChronologyBaseline == nil {
			return fmt.Errorf("self chronology requires an explicit validated baseline for every owner")
		}
		for _, fact := range actor.SelfExperiences {
			if fact.Chapter > context.Chapter || (fact.EndDay != nil && *fact.EndDay > context.CurrentDay+1e-12) {
				return fmt.Errorf("self evaluation precedes an already executed fact")
			}
			if previous := fact.Evaluation; previous != nil {
				if previous.EvaluatedAtDay > context.CurrentDay+1e-12 || (fact.Chapter == context.Chapter && (previous.GenerationID != context.GenerationID || previous.Cycle >= context.Cycle)) {
					return fmt.Errorf("self evaluation does not follow its previous owner evaluations")
				}
			}
		}
	}
	return nil
}

// A strict total order, including unknown execution times. Canonical ordinal
// is checked against actual intervals separately, not trusted as a model hint.
func selfChronologyLessV1(left, right CharacterSelfExperienceV2) bool {
	if (left.Evaluation == nil) != (right.Evaluation == nil) {
		return left.Evaluation == nil // Explicit legacy baseline, not claimed chronology.
	}
	if left.Chapter != right.Chapter {
		return left.Chapter < right.Chapter
	}
	if left.Evaluation != nil {
		if left.Evaluation.Cycle != right.Evaluation.Cycle {
			return left.Evaluation.Cycle < right.Evaluation.Cycle
		}
		if left.Evaluation.Ordinal != right.Evaluation.Ordinal {
			return left.Evaluation.Ordinal < right.Evaluation.Ordinal
		}
	}
	return left.ID < right.ID
}

func sortSelfChronologyV1(experiences []CharacterSelfExperienceV2) {
	sort.Slice(experiences, func(i, j int) bool { return selfChronologyLessV1(experiences[i], experiences[j]) })
}
