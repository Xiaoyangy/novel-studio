package agents

import (
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func loadCharacterPhysicalPreState(st *store.Store, chapter int, projected domain.ProjectedPlanningContextV2) (domain.WorldPhysicalStateV2, error) {
	if projected.Version != "" {
		if err := domain.ValidateProjectedPlanningContextV2(projected); err != nil {
			return domain.WorldPhysicalStateV2{}, err
		}
		if projected.NextChapter != chapter {
			return domain.WorldPhysicalStateV2{}, fmt.Errorf("physical state projected chapter mismatch")
		}
		physical, err := domain.ProjectedPhysicalStateV2(projected)
		if err != nil {
			return domain.WorldPhysicalStateV2{}, err
		}
		if physical != nil {
			return *physical, nil
		}
		baseline, err := st.LoadProjectAllAcceptedCharacterBaseline(projected.GenerationID, chapter, projected.StateRoot)
		if err != nil {
			return domain.WorldPhysicalStateV2{}, err
		}
		if baseline != nil {
			if baseline.PhysicalState == nil {
				return domain.WorldPhysicalStateV2{}, fmt.Errorf("accepted physical source snapshot is empty")
			}
			return *baseline.PhysicalState, nil
		}
	}
	if chapter == 1 {
		characters, err := st.Characters.Load()
		if err != nil {
			return domain.WorldPhysicalStateV2{}, err
		}
		registry, err := st.CharacterAgents.LoadRegistry()
		if err != nil {
			return domain.WorldPhysicalStateV2{}, err
		}
		if registry == nil {
			registry = &domain.CharacterAgentRegistry{Version: domain.CharacterAgentRegistryVersion}
		}
		for _, character := range characters {
			if character.InitialState == nil {
				continue
			}
			if _, ok := registry.Resolve(character.Name); ok {
				continue
			}
			updated, _, err := registry.UpsertCharacter(character.Name, character.Aliases, character.Tier, 0, "")
			if err != nil {
				return domain.WorldPhysicalStateV2{}, err
			}
			registry = &updated
		}
		return domain.BuildWorldPhysicalStateFromInitialV2(characters, *registry)
	}
	progress, err := st.Progress.Load()
	if err != nil {
		return domain.WorldPhysicalStateV2{}, err
	}
	if progress == nil || !slices.Contains(progress.CompletedChapters, chapter-1) {
		return domain.WorldPhysicalStateV2{}, fmt.Errorf("character physical state chapter %d lacks exact predecessor post_state; explicit v2 source/new generation is required, never parse StateAfter", chapter)
	}
	cursor, err := st.ProjectedV2().LoadRealizationCursor()
	if err != nil {
		return domain.WorldPhysicalStateV2{}, err
	}
	if cursor != nil && cursor.LastOutcomeReceiptDigest != "" {
		bundle, err := st.LoadAcceptedCharacterAgentBundle(chapter - 1)
		if err != nil {
			return domain.WorldPhysicalStateV2{}, err
		}
		if bundle == nil || bundle.ChapterWorldSimulation.PhysicalState == nil {
			return domain.WorldPhysicalStateV2{}, fmt.Errorf("accepted bundle lacks physical source")
		}
		return *bundle.ChapterWorldSimulation.PhysicalState, nil
	}
	if !st.World.HasAcceptedChapterReview(chapter - 1) {
		return domain.WorldPhysicalStateV2{}, fmt.Errorf("interactive physical predecessor lacks a body-bound accepted chapter review")
	}
	simulation, err := st.LoadChapterWorldSimulation(chapter - 1)
	if err != nil {
		return domain.WorldPhysicalStateV2{}, err
	}
	if simulation == nil || simulation.PhysicalState == nil || simulation.CharacterAgentProtocol == nil || simulation.CharacterAgentProtocol.Version != domain.CharacterAgentDecisionProtocolV2Version {
		return domain.WorldPhysicalStateV2{}, fmt.Errorf("accepted predecessor lacks v2 physical-state evidence; keep old data and establish an explicit new-generation baseline")
	}
	if simulation.CharacterActivation != nil {
		evidence, err := st.LoadCharacterActivationChapterEvidence(simulation.GenerationID, simulation.Chapter)
		if err != nil {
			return domain.WorldPhysicalStateV2{}, err
		}
		if evidence == nil {
			return domain.WorldPhysicalStateV2{}, fmt.Errorf("accepted activation predecessor lacks complete evidence")
		}
		if err := domain.ValidateCharacterActivationSimulation(*simulation, *evidence); err != nil {
			return domain.WorldPhysicalStateV2{}, err
		}
		return *simulation.PhysicalState, nil
	}
	evidence, err := loadCharacterAgentEvidence(st, *simulation)
	if err != nil {
		return domain.WorldPhysicalStateV2{}, err
	}
	if evidence == nil || len(evidence.Arbitrations) == 0 {
		return domain.WorldPhysicalStateV2{}, fmt.Errorf("accepted physical predecessor has no arbitration evidence")
	}
	last := evidence.Arbitrations[len(evidence.Arbitrations)-1]
	proposals, err := st.CharacterAgents.LoadLatestProposals(simulation.GenerationID, simulation.Chapter, last.Round, activeCharacterAgentIDs(evidence.Activation))
	if err != nil {
		return domain.WorldPhysicalStateV2{}, err
	}
	physical, err := domain.ApplyArbitrationPhysicalStateV2(last, evidence.Stimulus, proposals...)
	if err != nil {
		return domain.WorldPhysicalStateV2{}, err
	}
	want, _ := json.Marshal(physical)
	got, _ := json.Marshal(simulation.PhysicalState)
	if string(want) != string(got) {
		return domain.WorldPhysicalStateV2{}, fmt.Errorf("accepted simulation physical state differs from arbitration")
	}
	return physical, nil
}

func applyCharacterPhysicalObservation(observation *domain.CharacterObservationPacket, profile characterAgentProfile, stimulus domain.WorldStimulusPacket) error {
	if stimulus.PhysicalState == nil {
		return fmt.Errorf("v2 character observation lacks host-bound world physical state")
	}
	var actor *domain.CharacterPhysicalStateV2
	for i := range stimulus.PhysicalState.Actors {
		if stimulus.PhysicalState.Actors[i].AgentID == profile.Record.AgentID {
			actor = &stimulus.PhysicalState.Actors[i]
			break
		}
	}
	if actor == nil || actor.Character != profile.Character.Name {
		return fmt.Errorf("v2 physical state is missing character %s", profile.Character.Name)
	}
	views, err := domain.BuildCharacterResourceViewsV2(*stimulus.PhysicalState, profile.Record.AgentID)
	if err != nil {
		return err
	}
	observation.Version = domain.CharacterObservationV2Version
	observation.Location = actor.Location
	observation.ResourceViews = views
	if domain.HasCharacterSelfExperiencePolicyV2(stimulus.Sources) {
		observation.SelfExperiences, observation.TaskProgress, err = domain.BuildCharacterSelfObservationV2(*stimulus.PhysicalState, profile.Record.AgentID)
		if err != nil {
			return err
		}
		observation.Sources = append(observation.Sources, domain.CharacterSelfExperiencePolicyV2)
	}
	if domain.HasCharacterOperationalAvailabilityPolicyV1(stimulus.Sources) {
		observation.OperationalObservations, err = domain.BuildCharacterOperationalObservationsV1(*stimulus.PhysicalState, profile.Record.AgentID)
		if err != nil {
			return err
		}
		observation.Sources = append(observation.Sources, domain.CharacterOperationalAvailabilityPolicyV1)
	}
	if domain.HasCharacterWorkArtifactPolicyV1(stimulus.Sources) {
		observation.ArtifactViews, err = domain.BuildCharacterArtifactViewsV1(*stimulus.PhysicalState, profile.Record.AgentID)
		if err != nil {
			return err
		}
	}
	for _, fact := range actor.ReceivedFacts {
		text := fact.Text
		if fact.SourceType == "communication" {
			text = "收到的" + fact.Kind + "：" + text
			if fact.ReceivedAtDay != nil {
				text = fmt.Sprintf("收到的%s（实际送达于故事开始后 %.12g 分钟）：%s", fact.Kind, *fact.ReceivedAtDay*1440, fact.Text)
			}
		} else {
			text = "读到的文档陈述（不自动等同当前世界真相）：" + text
		}
		observation.KnownFacts = append(observation.KnownFacts, domain.CharacterAgentFact{ID: fact.ID, Kind: "received_" + fact.Kind, Text: text, Source: fact.SourceProposalDigest, Visibility: "private"})
	}
	// Unstructured legacy resources are author material, not automatically
	// owner-safe text. Qualitative permissions/materials use explicit entries
	// with nil amount and an empty unit; no number-stripping heuristics apply.
	observation.Resources = nil // v2 has one typed resource view, not a duplicate textual mirror.
	projectCharacterObservationSourcesV2(observation)
	return nil
}

// BuildCharacterObservationsForProjectedState is a pure, source-validated view
// builder for consumers of a projected context. It shares production builders
// but does not register actors, write memory/evidence, or call any model.
func BuildCharacterObservationsForProjectedState(st *store.Store, generationID string, chapter int, projected domain.ProjectedPlanningContextV2) (map[string]domain.CharacterObservationPacket, error) {
	boundary, err := deriveCharacterAgentArcBoundary(st, chapter)
	if err != nil {
		return nil, err
	}
	profiles, _, err := selectCharacterAgentRoster(st, chapter, boundary)
	if err != nil {
		return nil, err
	}
	registry, err := st.CharacterAgents.LoadRegistry()
	if err != nil {
		return nil, err
	}
	if registry == nil {
		registry = &domain.CharacterAgentRegistry{Version: domain.CharacterAgentRegistryVersion}
	}
	for i := range profiles {
		updated, record, err := registry.UpsertCharacter(profiles[i].Character.Name, profiles[i].Character.Aliases, profiles[i].Character.Tier, chapter, "")
		if err != nil {
			return nil, err
		}
		registry = &updated
		profiles[i].Record = record
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	stimulus, err := buildWorldStimulus(st, generationID, chapter, boundary, projected, nil, now, domain.CharacterAgentDecisionProtocolV2Version)
	if err != nil {
		return nil, err
	}
	result := make(map[string]domain.CharacterObservationPacket, len(profiles))
	for _, profile := range profiles {
		observation, err := buildCharacterObservation(st, generationID, chapter, profile, stimulus, projected, now)
		if err != nil {
			return nil, err
		}
		result[profile.Record.AgentID] = observation
	}
	return result, nil
}
