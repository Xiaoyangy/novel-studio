package domain

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	CharacterActivationSessionVersion    = "character-activation-session.v1"
	CharacterActivationCycleVersion      = "character-activation-cycle.v1"
	CharacterActivationCycleV2Version    = "character-activation-cycle.v2"
	CharacterActivationCyclePolicy       = "chapter-activation-cycles.v1"
	CharacterActivationCyclePolicyV2     = "chapter-activation-cycles.v2"
	CharacterActivationCycleSourcePrefix = "character-activation-cycle:"
)

// A cycle is not a chapter or a successor generation. Every packet still names
// the real chapter and generation; its source token additionally binds this
// activation and the previous cycle. Existing v1/v2 evidence stays unchanged.
type CharacterActivationCycle struct {
	Version              string                       `json:"version"`
	GenerationID         string                       `json:"generation_id"`
	Chapter              int                          `json:"chapter"`
	Index                int                          `json:"index"`
	PreviousDigest       string                       `json:"previous_digest"`
	ChapterContextDigest string                       `json:"chapter_context_digest"`
	InputSetDigest       string                       `json:"input_set_digest,omitempty"`
	BeforePhysicalRoot   string                       `json:"before_physical_root"`
	AfterPhysicalRoot    string                       `json:"after_physical_root"`
	StartDay             float64                      `json:"start_day"`
	EndDay               float64                      `json:"end_day"`
	Evidence             CharacterAgentEvidenceBundle `json:"evidence"`
	// A v2 execution distinguishes unchanged past intent from fresh proposals.
	// These fields are absent from all historical cycle digest payloads.
	// In V3 WorkContinuations records R1 admission; R2 may replace an admitted
	// owner with a genuine fresh proposal. Entries bind only actual continuers.
	WorkContinuations        []CharacterWorkContinuationReceiptV1 `json:"work_continuations,omitempty"`
	ContinuationEntryDigests []string                             `json:"continuation_entry_digests,omitempty"`
	// Ordered R1/R2 source roots, always recomputed by the V3 Host verifier.
	RoundSourceDigests []string `json:"round_source_digests,omitempty"`
	Digest             string   `json:"digest"`
}

// Chapter readiness is separate from world arbitration. It may request another
// event-driven activation, never change an actor's choice or invent outcomes.
// The initial implementation stores only concise structured assessment, not
// the assessor's private reasoning or a proposed replacement plot.
type CharacterChapterReadiness struct {
	Version                 string                            `json:"version"`
	GenerationID            string                            `json:"generation_id"`
	Chapter                 int                               `json:"chapter"`
	CycleDigest             string                            `json:"cycle_digest"`
	ReviewProtocol          string                            `json:"review_protocol"`
	InputDigest             string                            `json:"input_digest,omitempty"`
	EvidenceRefs            []string                          `json:"evidence_refs,omitempty"`
	ContractChecks          []CharacterReadinessContractCheck `json:"contract_checks,omitempty"`
	Decision                string                            `json:"decision"` // continue / ready_for_plan / hard_conflict
	Reason                  string                            `json:"reason"`
	UnresolvedHardContracts []string                          `json:"unresolved_hard_contracts,omitempty"`
	Digest                  string                            `json:"digest"`
}

// Session state is reconstructible from immutable cycles and assessments. A
// safety limit is not completion; reaching it leaves the session resumable.
type CharacterActivationSession struct {
	Version              string   `json:"version"`
	GenerationID         string   `json:"generation_id"`
	Chapter              int      `json:"chapter"`
	ChapterContextDigest string   `json:"chapter_context_digest"`
	InitialPhysicalRoot  string   `json:"initial_physical_root"`
	InitialDay           float64  `json:"initial_day"`
	MaxCycles            int      `json:"max_cycles"`
	CycleDigests         []string `json:"cycle_digests"`
	ReadinessDigests     []string `json:"readiness_digests"`
	Phase                string   `json:"phase"` // collecting / assessing / ready / hard_conflict
	CurrentPhysicalRoot  string   `json:"current_physical_root"`
	CurrentDay           float64  `json:"current_day"`
	PendingHardConflict  bool     `json:"pending_hard_conflict,omitempty"`
	Digest               string   `json:"digest"`
}

func CharacterActivationCycleSourceToken(generation string, chapter, index int, chapterContext, previousDigest string) (string, error) {
	if !strings.HasPrefix(generation, PlanningGenerationIDPrefix) || chapter <= 0 || index <= 0 {
		return "", fmt.Errorf("activation cycle identity is incomplete")
	}
	if err := validatePlanningV2Digest("chapter_context_digest", chapterContext); err != nil {
		return "", err
	}
	if index == 1 {
		if previousDigest != "" {
			return "", fmt.Errorf("first activation cycle cannot have a predecessor")
		}
	} else if err := validatePlanningV2Digest("previous_cycle_digest", previousDigest); err != nil {
		return "", err
	}
	digest, err := characterAgentDigest(struct {
		Generation        string
		Chapter, Index    int
		Context, Previous string
	}{generation, chapter, index, chapterContext, previousDigest})
	return CharacterActivationCycleSourcePrefix + digest, err
}

func CharacterPhysicalRootForCycle(state WorldPhysicalStateV2) (string, error) {
	if err := ValidateWorldPhysicalStateV2(state); err != nil {
		return "", err
	}
	return characterAgentDigest(state)
}

func LatestCharacterCycleProposals(evidence CharacterAgentEvidenceBundle) []CharacterDecisionProposal {
	latest := map[string]CharacterDecisionProposal{}
	for _, proposal := range evidence.Proposals {
		if old, ok := latest[proposal.AgentID]; !ok || proposal.Round > old.Round {
			latest[proposal.AgentID] = proposal
		}
	}
	ids := make([]string, 0, len(latest))
	for id := range latest {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	proposals := make([]CharacterDecisionProposal, 0, len(ids))
	for _, id := range ids {
		proposals = append(proposals, latest[id])
	}
	return proposals
}

func FinalizeCharacterActivationCycle(cycle CharacterActivationCycle) (CharacterActivationCycle, error) {
	if cycle.InputSetDigest != "" {
		if err := validatePlanningV2Digest("activation_input_set_digest", cycle.InputSetDigest); err != nil {
			return cycle, err
		}
	}
	if cycle.Version == "" {
		cycle.Version = CharacterActivationCycleVersion
	}
	if cycle.Version != CharacterActivationCycleVersion {
		return cycle, fmt.Errorf("unsupported activation cycle version")
	}
	if len(cycle.WorkContinuations)+len(cycle.ContinuationEntryDigests)+len(cycle.RoundSourceDigests) != 0 {
		return cycle, fmt.Errorf("legacy activation cycle cannot contain continuation authority")
	}
	token, err := CharacterActivationCycleSourceToken(cycle.GenerationID, cycle.Chapter, cycle.Index, cycle.ChapterContextDigest, cycle.PreviousDigest)
	if err != nil {
		return cycle, err
	}
	evidence := cycle.Evidence
	if physicalContainsRefV2(evidence.Stimulus.Sources, CharacterActivationCyclePolicyV3) {
		return cycle, fmt.Errorf("legacy activation cycle cannot claim v3 execution policy")
	}
	if evidence.GenerationID != cycle.GenerationID || evidence.Chapter != cycle.Chapter {
		return cycle, fmt.Errorf("activation cycle contains foreign chapter/generation evidence")
	}
	if !planningV2ContainsExactString(evidence.Stimulus.Sources, token) {
		return cycle, fmt.Errorf("activation cycle evidence lacks its exact cycle source token")
	}
	validateEvidence := ValidateCharacterAgentEvidenceBundle
	if evidence.Version == CharacterHardConflictEvidenceVersion {
		validateEvidence = ValidateCharacterHardConflictEvidenceBundle
	}
	if err := validateEvidence(evidence); err != nil {
		return cycle, fmt.Errorf("activation cycle evidence: %w", err)
	}
	if evidence.Stimulus.Version != WorldStimulusPacketV2Version || evidence.Stimulus.PhysicalState == nil || evidence.Stimulus.StoryClock == nil {
		return cycle, fmt.Errorf("activation cycle requires typed physical state and actual clock")
	}
	final := evidence.Arbitrations[len(evidence.Arbitrations)-1]
	if (!final.Finalized && final.HardContractStatus != "infeasible") || final.StoryTime == nil {
		return cycle, fmt.Errorf("activation cycle must finish its bounded arbitration/revision protocol")
	}
	if final.StoryTime.StartDay != evidence.Stimulus.StoryClock.CurrentDay || final.StoryTime.EndDay < final.StoryTime.StartDay {
		return cycle, fmt.Errorf("activation cycle clock is not exact")
	}
	before, err := CharacterPhysicalRootForCycle(*evidence.Stimulus.PhysicalState)
	if err != nil {
		return cycle, err
	}
	after, err := ApplyArbitrationPhysicalStateV2(final, evidence.Stimulus, LatestCharacterCycleProposals(evidence)...)
	if err != nil {
		return cycle, err
	}
	afterRoot, err := CharacterPhysicalRootForCycle(after)
	if err != nil {
		return cycle, err
	}
	// Derived roots and time cannot be supplied as claims by a caller.
	cycle.BeforePhysicalRoot, cycle.AfterPhysicalRoot = before, afterRoot
	cycle.StartDay, cycle.EndDay = final.StoryTime.StartDay, final.StoryTime.EndDay
	cycle.Digest = ""
	cycle.Digest, err = characterAgentDigest(cycle)
	return cycle, err
}

func ValidateCharacterActivationCycle(cycle CharacterActivationCycle) error {
	want, err := FinalizeCharacterActivationCycle(cycle)
	if err != nil {
		return err
	}
	wantHash, err := characterAgentDigest(want)
	if err != nil {
		return err
	}
	gotHash, err := characterAgentDigest(cycle)
	if err != nil || wantHash != gotHash {
		return fmt.Errorf("activation cycle derived state/time/digest mismatch")
	}
	return nil
}

func FinalizeCharacterChapterReadiness(readiness CharacterChapterReadiness) (CharacterChapterReadiness, error) {
	if readiness.Version == "" {
		readiness.Version = "character-chapter-readiness.v1"
	}
	if (readiness.Version != "character-chapter-readiness.v1" && readiness.Version != CharacterReadinessReviewedVersion) || !strings.HasPrefix(readiness.GenerationID, PlanningGenerationIDPrefix) || readiness.Chapter <= 0 {
		return readiness, fmt.Errorf("chapter readiness identity is incomplete")
	}
	if readiness.Version == CharacterReadinessReviewedVersion {
		if err := validatePlanningV2Digest("readiness input_digest", readiness.InputDigest); err != nil {
			return readiness, err
		}
		if len(readiness.EvidenceRefs) == 0 {
			return readiness, fmt.Errorf("reviewed readiness must cite its exact evidence")
		}
	} else if readiness.InputDigest != "" || len(readiness.EvidenceRefs)+len(readiness.ContractChecks) > 0 {
		return readiness, fmt.Errorf("legacy readiness cannot mix reviewed receipt fields")
	}
	for key, value := range map[string]string{"cycle_digest": readiness.CycleDigest, "review_protocol": readiness.ReviewProtocol} {
		if err := validatePlanningV2Digest(key, value); err != nil {
			return readiness, err
		}
	}
	if strings.TrimSpace(readiness.Reason) == "" || utf8.RuneCountInString(readiness.Reason) > 1000 {
		return readiness, fmt.Errorf("chapter readiness needs a bounded reason")
	}
	switch readiness.Decision {
	case "continue":
	case "ready_for_plan":
		if len(readiness.UnresolvedHardContracts) != 0 {
			return readiness, fmt.Errorf("unresolved hard contracts cannot authorize chapter planning")
		}
	case "hard_conflict":
		if len(readiness.UnresolvedHardContracts) == 0 {
			return readiness, fmt.Errorf("hard conflict must identify unresolved obligations")
		}
	default:
		return readiness, fmt.Errorf("unsupported chapter readiness decision")
	}
	readiness.Digest = ""
	var err error
	readiness.Digest, err = characterAgentDigest(readiness)
	return readiness, err
}

func NewCharacterActivationSession(generation string, chapter int, contextDigest string, before WorldPhysicalStateV2, day float64, maxCycles int) (CharacterActivationSession, error) {
	root, err := CharacterPhysicalRootForCycle(before)
	if err != nil {
		return CharacterActivationSession{}, err
	}
	session := CharacterActivationSession{Version: CharacterActivationSessionVersion, GenerationID: generation, Chapter: chapter, ChapterContextDigest: contextDigest, InitialPhysicalRoot: root, InitialDay: day, MaxCycles: maxCycles, Phase: "collecting", CurrentPhysicalRoot: root, CurrentDay: day}
	return finalizeCharacterActivationSession(session)
}

func finalizeCharacterActivationSession(session CharacterActivationSession) (CharacterActivationSession, error) {
	if session.Version != CharacterActivationSessionVersion || !strings.HasPrefix(session.GenerationID, PlanningGenerationIDPrefix) || session.Chapter <= 0 || session.MaxCycles <= 0 || session.MaxCycles > 64 || !finiteStoryDay(session.InitialDay) || session.InitialDay < 0 || !finiteStoryDay(session.CurrentDay) || session.CurrentDay < session.InitialDay {
		return session, fmt.Errorf("invalid chapter activation session identity/bounds")
	}
	for key, value := range map[string]string{"context": session.ChapterContextDigest, "initial_physical_root": session.InitialPhysicalRoot, "current_physical_root": session.CurrentPhysicalRoot} {
		if err := validatePlanningV2Digest(key, value); err != nil {
			return session, err
		}
	}
	if len(session.CycleDigests) > session.MaxCycles || len(session.ReadinessDigests) > len(session.CycleDigests) {
		return session, fmt.Errorf("activation session exceeds its cycle/readiness bounds")
	}
	if len(session.CycleDigests) == 0 && (session.Phase != "collecting" || session.CurrentPhysicalRoot != session.InitialPhysicalRoot || session.CurrentDay != session.InitialDay) {
		return session, fmt.Errorf("empty activation session cannot claim advanced state or time")
	}
	for _, list := range [][]string{session.CycleDigests, session.ReadinessDigests} {
		for _, digest := range list {
			if err := validatePlanningV2Digest("activation session link", digest); err != nil {
				return session, err
			}
		}
	}
	switch session.Phase {
	case "collecting", "ready", "hard_conflict":
		if len(session.CycleDigests) != len(session.ReadinessDigests) {
			return session, fmt.Errorf("activation session is missing a readiness assessment")
		}
		if session.PendingHardConflict {
			return session, fmt.Errorf("pending hard conflict requires assessment phase")
		}
		if session.Phase != "collecting" && len(session.CycleDigests) == 0 {
			return session, fmt.Errorf("activation session cannot finish without a cycle")
		}
	case "assessing":
		if len(session.CycleDigests) != len(session.ReadinessDigests)+1 {
			return session, fmt.Errorf("activation session assessment cursor mismatch")
		}
	default:
		return session, fmt.Errorf("invalid activation session phase")
	}
	session.Digest = ""
	var err error
	session.Digest, err = characterAgentDigest(session)
	return session, err
}

func ValidateCharacterActivationSession(session CharacterActivationSession) error {
	want, err := finalizeCharacterActivationSession(session)
	if err != nil {
		return err
	}
	if want.Digest != session.Digest {
		return fmt.Errorf("activation session digest mismatch")
	}
	return nil
}

func AppendCharacterActivationCycle(session CharacterActivationSession, cycle CharacterActivationCycle) (CharacterActivationSession, error) {
	if err := ValidateCharacterActivationSession(session); err != nil {
		return session, err
	}
	if err := ValidateCharacterActivationCycle(cycle); err != nil {
		return session, err
	}
	return appendValidatedCharacterActivationCycle(session, cycle)
}

// Only domain verifiers call this after authenticating the complete cycle.
// Keeping it private does not turn a self-signed v2 envelope into authority.
func appendValidatedCharacterActivationCycle(session CharacterActivationSession, cycle CharacterActivationCycle) (CharacterActivationSession, error) {
	if session.Phase != "collecting" {
		return session, fmt.Errorf("activation session is not ready for another cycle")
	}
	if len(session.CycleDigests) >= session.MaxCycles {
		return session, fmt.Errorf("chapter activation cycle limit reached; chapter is not complete")
	}
	previous := ""
	if len(session.CycleDigests) > 0 {
		previous = session.CycleDigests[len(session.CycleDigests)-1]
	}
	if cycle.GenerationID != session.GenerationID || cycle.Chapter != session.Chapter || cycle.ChapterContextDigest != session.ChapterContextDigest || cycle.Index != len(session.CycleDigests)+1 || cycle.PreviousDigest != previous || cycle.BeforePhysicalRoot != session.CurrentPhysicalRoot || cycle.StartDay != session.CurrentDay {
		return session, fmt.Errorf("activation cycle does not continue exact chapter state/clock")
	}
	if HasCharacterSelfChronologyPolicyV1(cycle.Evidence.Stimulus.Sources) {
		if cycle.Evidence.Stimulus.SelfEvaluationContext == nil {
			return session, fmt.Errorf("chronological cycle lacks its evaluation context")
		}
		if err := ValidateCharacterSelfEvaluationContextAgainstSessionV1(*cycle.Evidence.Stimulus.SelfEvaluationContext, session); err != nil {
			return session, err
		}
	}
	if cycle.EndDay == cycle.StartDay && cycle.AfterPhysicalRoot == cycle.BeforePhysicalRoot && cycle.Evidence.Arbitrations[len(cycle.Evidence.Arbitrations)-1].HardContractStatus != "infeasible" {
		return session, fmt.Errorf("activation cycle made no world progress; do not spin another model call")
	}
	session.CycleDigests = append(append([]string(nil), session.CycleDigests...), cycle.Digest)
	session.CurrentPhysicalRoot, session.CurrentDay = cycle.AfterPhysicalRoot, cycle.EndDay
	session.Phase = "assessing"
	session.PendingHardConflict = cycle.Evidence.Arbitrations[len(cycle.Evidence.Arbitrations)-1].HardContractStatus == "infeasible"
	return finalizeCharacterActivationSession(session)
}

func ApplyCharacterChapterReadiness(session CharacterActivationSession, readiness CharacterChapterReadiness) (CharacterActivationSession, error) {
	if err := ValidateCharacterActivationSession(session); err != nil {
		return session, err
	}
	want, err := FinalizeCharacterChapterReadiness(readiness)
	if err != nil {
		return session, err
	}
	if want.Digest != readiness.Digest || session.Phase != "assessing" || readiness.GenerationID != session.GenerationID || readiness.Chapter != session.Chapter || readiness.CycleDigest != session.CycleDigests[len(session.CycleDigests)-1] {
		return session, fmt.Errorf("chapter readiness does not bind the pending exact cycle")
	}
	if session.PendingHardConflict && readiness.Decision != "hard_conflict" {
		return session, fmt.Errorf("readiness cannot override an arbitrated hard conflict")
	}
	session.ReadinessDigests = append(append([]string(nil), session.ReadinessDigests...), readiness.Digest)
	session.Phase = map[string]string{"continue": "collecting", "ready_for_plan": "ready", "hard_conflict": "hard_conflict"}[readiness.Decision]
	session.PendingHardConflict = false
	return finalizeCharacterActivationSession(session)
}
