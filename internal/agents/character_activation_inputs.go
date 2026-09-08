package agents

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func activationInputsForExecution(input domain.CharacterActivationInputSet, session domain.CharacterActivationSession) characterAgentChapterInputs {
	observations := map[string]domain.CharacterObservationPacket{}
	active := map[string]bool{}
	for _, id := range activeCharacterAgentIDs(input.Activation) {
		active[id] = true
	}
	for _, observation := range input.Observations {
		if active[observation.AgentID] {
			observations[observation.AgentID] = observation
		}
	}
	return characterAgentChapterInputs{Stimulus: input.Stimulus, Activation: input.Activation, Observations: observations, Sources: input.Stimulus.Sources, CycleSession: &session}
}

func loadOrPrepareCharacterActivationInputs(st *store.Store, session domain.CharacterActivationSession, boundary ProjectedArcBoundary, projected domain.ProjectedPlanningContextV2, sources []string) (characterAgentChapterInputs, error) {
	var empty characterAgentChapterInputs
	proofs, err := st.CharacterAgents.ForActivationCycle(session)
	if err != nil {
		return empty, err
	}
	policy := characterActivationPolicyForBoundary(boundary)
	if frozen, err := proofs.LoadActivationInputs(); err != nil {
		return empty, err
	} else if frozen != nil {
		if err := validateCharacterActivationInputPolicy(*frozen, session, policy); err != nil {
			return empty, err
		}
	}
	if frozen, err := proofs.RecoverActivationInputs(); err != nil {
		return empty, err
	} else if frozen != nil {
		return activationInputsForExecution(*frozen, session), nil
	}
	var input domain.CharacterActivationInputSet
	if len(session.CycleDigests) == 0 {
		input, err = prepareInitialCharacterActivationInputs(st, session, boundary, projected, sources)
	} else if domain.CharacterActivationUsesVerifiedPrefix(policy) {
		prefix, loadErr := st.LoadVerifiedCharacterActivationPrefix(session.GenerationID, session.Chapter)
		if loadErr != nil {
			return empty, loadErr
		}
		if prefix == nil || !sameCharacterCycleValue(prefix.Session(), session) {
			return empty, fmt.Errorf("activation is missing its exact verified source prefix")
		}
		steps := prefix.Steps()
		if len(steps) != len(session.CycleDigests) {
			return empty, fmt.Errorf("activation source prefix has an incomplete committed step set")
		}
		last := steps[len(steps)-1]
		input, err = buildNextCharacterActivationInputsFromStep(last.Input(), last, session)
	} else {
		previous, loadErr := st.LoadCharacterActivationCycle(session.GenerationID, session.Chapter, len(session.CycleDigests))
		if loadErr != nil {
			return empty, loadErr
		}
		if previous == nil {
			return empty, fmt.Errorf("activation is missing its previous committed cycle")
		}
		frozen, loadErr := st.CharacterAgents.LoadActivationInputsForCycle(*previous)
		if loadErr != nil {
			return empty, loadErr
		}
		input, err = buildNextCharacterActivationInputs(*frozen, *previous, session)
	}
	if err != nil {
		return empty, err
	}
	if err := validateCharacterActivationInputPolicy(input, session, policy); err != nil {
		return empty, err
	}
	if err := proofs.PublishActivationInputs(input); err != nil {
		return empty, err
	}
	return activationInputsForExecution(input, session), nil
}

func prepareInitialCharacterActivationInputs(st *store.Store, session domain.CharacterActivationSession, boundary ProjectedArcBoundary, projected domain.ProjectedPlanningContextV2, sources []string) (domain.CharacterActivationInputSet, error) {
	var input domain.CharacterActivationInputSet
	if len(session.CycleDigests) != 0 {
		return input, fmt.Errorf("initial character input cannot restart a progressed chapter")
	}
	if err := st.EnsureCharacterAgentCanon(max(0, session.Chapter-1)); err != nil {
		return input, err
	}
	profiles, protagonist, err := selectCharacterAgentRoster(st, session.Chapter, boundary)
	if err != nil {
		return input, err
	}
	registry, err := st.CharacterAgents.LoadRegistry()
	if err != nil {
		return input, err
	}
	if registry == nil {
		return input, fmt.Errorf("initial activation has no registered character")
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for i := range profiles {
		updated, record, err := registry.UpsertCharacter(profiles[i].Character.Name, profiles[i].Character.Aliases, profiles[i].Character.Tier, session.Chapter, now)
		if err != nil {
			return input, err
		}
		registry = &updated
		profiles[i].Record = record
		if err := ensureCharacterAgentMemory(st, session.GenerationID, profiles[i], session.Chapter, now); err != nil {
			return input, err
		}
	}
	// Only stable identity registration is book-wide. Cycle-specific status and
	// memory below live in the immutable projected input, not accepted canon.
	if err := st.CharacterAgents.SaveRegistry(*registry); err != nil {
		return input, err
	}
	token, err := domain.CharacterActivationCycleSourceToken(session.GenerationID, session.Chapter, 1, session.ChapterContextDigest, "")
	if err != nil {
		return input, err
	}
	var cycleSources []string
	for _, source := range sources {
		if !strings.HasPrefix(source, "character-agent-protocol:") {
			cycleSources = append(cycleSources, source)
		}
	}
	policy := characterActivationPolicyForBoundary(boundary)
	sources = append(cycleSources, token, domain.CharacterPassiveReceptionPolicyV2, domain.CharacterOperationalAvailabilityPolicyV1, "character-agent-protocol:"+characterActivationProtocolForPolicy(policy))
	buildStimulus := buildWorldStimulus
	if domain.CharacterActivationUsesVerifiedPrefix(policy) {
		buildStimulus = buildWorldStimulusDraft
	}
	stimulus, err := buildStimulus(st, session.GenerationID, session.Chapter, boundary, projected, sources, now, domain.CharacterAgentDecisionProtocolV2Version)
	if err != nil {
		return input, err
	}
	if domain.CharacterActivationUsesVerifiedPrefix(policy) {
		if err := prepareCharacterActivationChronology(&stimulus, session, policy); err != nil {
			return input, err
		}
		stimulus, err = domain.FinalizeWorldStimulusPacket(stimulus)
		if err != nil {
			return input, err
		}
	}
	input.Stimulus = stimulus
	input.Activation = domain.CharacterAgentActivation{GenerationID: session.GenerationID, Chapter: session.Chapter, GeneratedAt: now}
	for _, profile := range profiles {
		observation, err := buildCharacterActivationObservation(st, session, profile, stimulus, projected, now)
		if err != nil {
			return input, err
		}
		memory, err := st.CharacterAgents.LoadProjectedMemory(session.GenerationID, profile.Record.AgentID)
		if err != nil {
			return input, err
		}
		if memory == nil {
			return input, fmt.Errorf("initial activation lacks private memory")
		}
		reasons := characterActivationReasons(st, profile, protagonist, session.Chapter, projected)
		entry := domain.CharacterAgentActivationEntry{AgentID: profile.Record.AgentID, Character: profile.Character.Name, Tier: profile.Character.Tier, State: domain.CharacterAgentSleeping, Reasons: reasons}
		if len(reasons) > 0 {
			entry.State, entry.ObservationDigest = domain.CharacterAgentActive, observation.Digest
		}
		input.Observations = append(input.Observations, observation)
		input.Memories = append(input.Memories, *memory)
		input.Activation.Entries = append(input.Activation.Entries, entry)
	}
	input.Registry = *registry
	return finalizeCharacterActivationInputs(input, now)
}

func buildCharacterActivationObservation(st *store.Store, session domain.CharacterActivationSession, profile characterAgentProfile, stimulus domain.WorldStimulusPacket, projected domain.ProjectedPlanningContextV2, now string) (domain.CharacterObservationPacket, error) {
	build := buildCharacterObservation
	if domain.HasCharacterSelfChronologyPolicyV1(stimulus.Sources) {
		if stimulus.SelfEvaluationContext == nil {
			return domain.CharacterObservationPacket{}, fmt.Errorf("activation observation lacks its host evaluation context")
		}
		if err := domain.ValidateCharacterSelfEvaluationContextAgainstSessionV1(*stimulus.SelfEvaluationContext, session); err != nil {
			return domain.CharacterObservationPacket{}, err
		}
		build = buildCharacterObservationDraft
	}
	observation, err := build(st, session.GenerationID, session.Chapter, profile, stimulus, projected, now)
	if err != nil {
		return observation, err
	}
	observation.CycleContext, err = domain.NewCharacterObservationCycleContext(session)
	if err != nil {
		return observation, err
	}
	return domain.FinalizeCharacterObservationPacket(observation)
}

// This builder deliberately does not read mutable dossiers, outline text or
// global projected memory. Everything comes from the previous frozen private
// baselines and the exact closed arbitration; readiness cannot steer actors.
func buildNextCharacterActivationInputs(previous domain.CharacterActivationInputSet, cycle domain.CharacterActivationCycle, session domain.CharacterActivationSession) (domain.CharacterActivationInputSet, error) {
	var input domain.CharacterActivationInputSet
	if err := domain.ValidateCharacterActivationInputsForCycle(previous, cycle); err != nil {
		return input, err
	}
	if err := validateNextCharacterActivationBoundary(previous, cycle, session); err != nil {
		return input, err
	}
	receipt := cycle.Evidence.Arbitrations[len(cycle.Evidence.Arbitrations)-1]
	proposals := domain.LatestCharacterCycleProposals(cycle.Evidence)
	physical, err := domain.ApplyArbitrationPhysicalStateV2(receipt, cycle.Evidence.Stimulus, proposals...)
	if err != nil {
		return input, err
	}
	memories, err := projectCharacterActivationMemories(previous.Memories, cycle)
	if err != nil {
		return input, err
	}
	return buildNextCharacterActivationInputsResolved(previous, cycle, session, proposals, physical, memories)
}

func validateNextCharacterActivationBoundary(previous domain.CharacterActivationInputSet, cycle domain.CharacterActivationCycle, session domain.CharacterActivationSession) error {
	if err := domain.ValidateCharacterActivationSession(session); err != nil {
		return err
	}
	if session.Phase != "collecting" || len(session.CycleDigests) != cycle.Index || session.CycleDigests[len(session.CycleDigests)-1] != cycle.Digest || session.GenerationID != cycle.GenerationID || session.Chapter != cycle.Chapter || session.ChapterContextDigest != cycle.ChapterContextDigest || session.CurrentPhysicalRoot != cycle.AfterPhysicalRoot || session.CurrentDay != cycle.EndDay || cycle.Index >= session.MaxCycles {
		return fmt.Errorf("next activation does not follow the exact closed cycle")
	}
	if !domain.HasCharacterSourceRefPolicyV2(previous.Stimulus.Sources) {
		return fmt.Errorf("next activation requires the frozen opaque source policy; do not mix historical inputs")
	}
	return nil
}

// Both source-aware and historical entry points reach the same private
// projection only after their own proof validators have supplied the complete
// effective choices, after-state and projected memories.
func buildNextCharacterActivationInputsResolved(previous domain.CharacterActivationInputSet, cycle domain.CharacterActivationCycle, session domain.CharacterActivationSession, proposals []domain.CharacterDecisionProposal, physical domain.WorldPhysicalStateV2, memories []domain.CharacterAgentMemory) (domain.CharacterActivationInputSet, error) {
	var input domain.CharacterActivationInputSet
	raw, err := json.Marshal(previous)
	if err != nil {
		return input, err
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return input, err
	}
	receipt := cycle.Evidence.Arbitrations[len(cycle.Evidence.Arbitrations)-1]
	input.Memories = memories
	token, err := domain.CharacterActivationCycleSourceToken(session.GenerationID, session.Chapter, cycle.Index+1, session.ChapterContextDigest, cycle.Digest)
	if err != nil {
		return input, err
	}
	input.Stimulus.PhysicalState = &physical
	clock := *input.Stimulus.StoryClock
	clock.CurrentDay = cycle.EndDay
	clock, err = domain.FinalizeStoryClockContext(clock)
	if err != nil {
		return input, err
	}
	input.Stimulus.StoryClock = &clock
	input.Stimulus.TimeWindow = characterStoryClockText(cycle.EndDay)
	input.Stimulus.GeneratedAt = receipt.GeneratedAt
	var sources []string
	for _, source := range input.Stimulus.Sources {
		if !strings.HasPrefix(source, domain.CharacterActivationCycleSourcePrefix) {
			sources = append(sources, source)
		}
	}
	input.Stimulus.Sources = append(sources, token)
	if domain.HasCharacterSelfChronologyPolicyV1(input.Stimulus.Sources) {
		input.Stimulus.SelfEvaluationContext, err = domain.NewCharacterSelfEvaluationContextV1(session)
		if err != nil {
			return input, err
		}
	}
	// Keep non-physical/social history, but never present an opening snapshot
	// as a new event or let its encoded physical mirror override the new state.
	var events []domain.CharacterAgentFact
	for _, fact := range input.Stimulus.CurrentEvents {
		if fact.Kind == "projected_state" {
			if strings.HasPrefix(fact.Text, "world的"+domain.WorldPhysicalStateV2Field+"=") || strings.HasPrefix(fact.Text, "world的story_day=") {
				continue
			}
			fact.Kind = "chapter_opening_state"
			fact.Text = "本章开局的历史快照（不是新事件，不覆盖本轮实际状态）：" + fact.Text
		}
		events = append(events, fact)
	}
	input.Stimulus.CurrentEvents = events
	input.Stimulus, err = domain.FinalizeWorldStimulusPacket(input.Stimulus)
	if err != nil {
		return input, err
	}
	byMemory := map[string]domain.CharacterAgentMemory{}
	for _, memory := range input.Memories {
		byMemory[memory.AgentID] = memory
	}
	byProposal := map[string]domain.CharacterDecisionProposal{}
	for _, proposal := range proposals {
		byProposal[proposal.AgentID] = proposal
	}
	input.Activation.Entries = nil
	for i := range input.Observations {
		observation := &input.Observations[i]
		before := previous.Observations[i]
		observation.CycleContext, err = domain.NewCharacterObservationCycleContext(session)
		if err != nil {
			return input, err
		}
		observation.Round, observation.ConflictFeedback = 1, nil
		observation.StimulusDigest, observation.TimeWindow, observation.GeneratedAt = input.Stimulus.Digest, input.Stimulus.TimeWindow, receipt.GeneratedAt
		observation.Sources = append([]string{"characters.json"}, input.Stimulus.Sources...)
		var events []domain.CharacterAgentFact
		for _, fact := range observation.PerceivedEvents {
			if fact.Kind != "own_current_action" {
				events = append(events, fact)
			}
		}
		observation.PerceivedEvents = events
		var known []domain.CharacterAgentFact
		for _, fact := range observation.KnownFacts {
			if !strings.HasPrefix(fact.Kind, "received_") {
				known = append(known, fact)
			}
		}
		observation.KnownFacts = known // Rebuild received facts once from the actual owner state.
		if proposal, active := byProposal[observation.AgentID]; active {
			observation.CurrentGoal, observation.Pressure = proposal.CurrentGoal, proposal.Pressure
		}
		memory := byMemory[observation.AgentID]
		previousRaw := map[string]bool{}
		for _, old := range previous.Memories {
			if old.AgentID == observation.AgentID {
				for _, fact := range old.Facts {
					previousRaw[fact.ID] = true
				}
			}
		}
		previousVisible := map[string]bool{}
		for _, fact := range before.Memory {
			previousVisible[fact.ID] = true
		}
		observation.Memory = nil
		for _, fact := range memory.Facts {
			if previousVisible[fact.ID] || !previousRaw[fact.ID] {
				observation.Memory = append(observation.Memory, fact)
			}
		}
		observation.MemoryRoot = memory.MemoryRoot
		profile := characterAgentProfile{Record: domain.CharacterAgentRecord{AgentID: observation.AgentID}, Character: domain.Character{Name: observation.Character}}
		if err := applyCharacterPhysicalObservation(observation, profile, input.Stimulus); err != nil {
			return input, err
		}
		*observation, err = domain.FinalizeCharacterObservationPacket(*observation)
		if err != nil {
			return input, err
		}
		reasons, err := domain.CharacterReactivationReasons(before, *observation)
		if err != nil {
			return input, err
		}
		entry := domain.CharacterAgentActivationEntry{AgentID: observation.AgentID, Character: observation.Character, Tier: observation.Tier, State: domain.CharacterAgentSleeping, Reasons: reasons}
		if len(reasons) > 0 {
			entry.State, entry.ObservationDigest = domain.CharacterAgentActive, observation.Digest
		}
		input.Activation.Entries = append(input.Activation.Entries, entry)
	}
	return finalizeCharacterActivationInputs(input, receipt.GeneratedAt)
}

func finalizeCharacterActivationInputs(input domain.CharacterActivationInputSet, now string) (domain.CharacterActivationInputSet, error) {
	for _, entry := range input.Activation.Entries {
		for i := range input.Registry.Entries {
			record := &input.Registry.Entries[i]
			if record.AgentID != entry.AgentID {
				continue
			}
			record.Status = entry.State
			if entry.State == domain.CharacterAgentActive {
				record.LastActivatedChapter, record.UpdatedAt = input.Stimulus.Chapter, now
			}
		}
	}
	var err error
	input.Registry, err = domain.FinalizeCharacterAgentRegistry(input.Registry)
	if err != nil {
		return input, err
	}
	input.Activation.RegistryRoot, input.Activation.GeneratedAt = input.Registry.RegistryRoot, now
	input.Activation, err = domain.FinalizeCharacterAgentActivation(input.Activation)
	if err != nil {
		return input, err
	}
	return domain.FinalizeCharacterActivationInputSet(input)
}
