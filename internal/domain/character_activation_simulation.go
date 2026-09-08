package domain

import (
	"encoding/json"
	"fmt"
	"sort"
)

type CharacterActivationSimulationBinding struct {
	Version               string   `json:"version"`
	ChapterEvidenceDigest string   `json:"chapter_evidence_digest"`
	ContextDigest         string   `json:"context_digest"`
	SessionDigest         string   `json:"session_digest"`
	CycleDigests          []string `json:"cycle_digests"`
	InputSetDigests       []string `json:"input_set_digests"`
	ReadinessDigests      []string `json:"readiness_digests"`
}

// The complete ordered decisions are not collapsed into a new arbiter intent.
// PostState is omitted from each trace row to avoid repeating full histories;
// its exact source is recoverable via the immutable arbitration/cycle digests.
type CharacterActivationDecisionTrace struct {
	Cycle             int                       `json:"cycle"`
	AgentID           string                    `json:"agent_id"`
	OriginLocation    string                    `json:"origin_location"`
	ProposalDigest    string                    `json:"proposal_digest"`
	ArbitrationDigest string                    `json:"arbitration_digest"`
	StoryTime         *StoryTimeChapterSchedule `json:"story_time"`
	Decision          CharacterWorldDecision    `json:"decision"`
	Continued         bool                      `json:"continued,omitempty"`
}

func BuildCharacterActivationSimulation(evidence CharacterActivationChapterEvidence, baseTickID string, sources []string) (ChapterWorldSimulation, error) {
	verified, err := VerifyCharacterActivationChapter(evidence)
	if err != nil {
		return ChapterWorldSimulation{}, err
	}
	return verified.BuildSimulation(baseTickID, sources)
}

func buildCharacterActivationSimulation(evidence CharacterActivationChapterEvidence, baseTickID string, sources []string, steps []VerifiedCharacterActivationStep) (ChapterWorldSimulation, error) {
	var simulation ChapterWorldSimulation
	first, last := evidence.Cycles[0], evidence.Cycles[len(evidence.Cycles)-1]
	finalReceipt := last.Evidence.Arbitrations[len(last.Evidence.Arbitrations)-1]
	var err error
	var physical WorldPhysicalStateV2
	if len(steps) > 0 {
		physical = steps[len(steps)-1].AfterState()
	} else {
		physical, err = ApplyArbitrationPhysicalStateV2(finalReceipt, last.Evidence.Stimulus, LatestCharacterCycleProposals(last.Evidence)...)
	}
	if err != nil {
		return simulation, err
	}
	binding := &CharacterActivationSimulationBinding{Version: CharacterActivationChapterEvidenceVersion, ChapterEvidenceDigest: evidence.Digest, ContextDigest: evidence.Context.Digest, SessionDigest: evidence.Session.Digest, CycleDigests: append([]string(nil), evidence.Session.CycleDigests...), ReadinessDigests: append([]string(nil), evidence.Session.ReadinessDigests...)}
	latest := map[string]CharacterWorldDecision{}
	latestProposals := map[string]CharacterDecisionProposal{}
	var observations, proposals, memoryRoots, observable []string
	for i, cycle := range evidence.Cycles {
		binding.InputSetDigests = append(binding.InputSetDigests, evidence.Inputs[i].Digest)
		receipt := cycle.Evidence.Arbitrations[len(cycle.Evidence.Arbitrations)-1]
		var choices []CharacterDecisionProposal
		var state WorldPhysicalStateV2
		if len(steps) > 0 {
			choices, state = steps[i].EffectiveProposals(), steps[i].AfterState()
		} else {
			choices = LatestCharacterCycleProposals(cycle.Evidence)
			state, err = ApplyArbitrationPhysicalStateV2(receipt, cycle.Evidence.Stimulus, choices...)
		}
		if err != nil {
			return simulation, err
		}
		decisions, err := receipt.CharacterDecisions(choices, state)
		if err != nil {
			return simulation, err
		}
		for _, choice := range choices {
			latestProposals[choice.AgentID] = choice
			for _, decision := range decisions {
				if decision.Character != choice.Character {
					continue
				}
				latest[choice.AgentID] = decision
				traceDecision := decision
				traceDecision.PostState = nil
				continued := false
				for _, source := range cycle.WorkContinuations {
					continued = continued || (source.AgentID == choice.AgentID && (cycle.Version != CharacterActivationCycleV3Version || source.OriginProposalDigest == choice.Digest))
				}
				simulation.CharacterDecisionTrace = append(simulation.CharacterDecisionTrace, CharacterActivationDecisionTrace{cycle.Index, choice.AgentID, choice.Location, choice.Digest, receipt.Digest, receipt.StoryTime, traceDecision, continued})
			}
			if choice.Character == evidence.Context.POVCharacter {
				for _, resolution := range receipt.Resolutions {
					if resolution.AgentID != choice.AgentID {
						continue
					}
					text, err := CharacterPrivateOutcomeV2(choice, resolution, state, receipt)
					if err != nil {
						return simulation, err
					}
					observable = append(observable, fmt.Sprintf("周期%d（故事开始后%.12g–%.12g分钟）的本人可知结果：%s", cycle.Index, cycle.StartDay*1440, cycle.EndDay*1440, text))
				}
			}
		}
		// Include every initial/revised proposal in the integrity inventory,
		// even though the trace executes only the last proposal of each cycle.
		for _, observation := range cycle.Evidence.Observations {
			observations = append(observations, observation.Digest)
		}
		for _, proposal := range cycle.Evidence.Proposals {
			proposals = append(proposals, proposal.Digest)
		}
		memoryRoots = append(memoryRoots, cycle.Evidence.MemoryRoots...)
	}
	var pov *CharacterDecisionProposal
	for _, actor := range physical.Actors {
		decision, acted := latest[actor.AgentID]
		if acted {
			copy := actor
			decision.PostState, decision.Location = &copy, actor.Location
			views, err := BuildCharacterResourceViewsV2(physical, actor.AgentID)
			if err != nil {
				return simulation, err
			}
			decision.Resources = FormatCharacterResourceViewsV2(views)
			simulation.CharacterDecisions = append(simulation.CharacterDecisions, decision)
		}
		if actor.Character == evidence.Context.POVCharacter {
			if choice, ok := latestProposals[actor.AgentID]; ok {
				copy := choice
				pov = &copy
			}
			for _, fact := range actor.ReceivedFacts {
				if fact.Chapter != evidence.Session.Chapter {
					continue
				}
				label := "读到的文档陈述（不自动等于当前真相）"
				if fact.SourceType == "communication" {
					label = "实际收到的" + fact.Kind + "（不自动确认内容真实）"
				}
				if fact.ReceivedAtDay != nil {
					label += fmt.Sprintf("，实际送达于故事开始后%.12g分钟", *fact.ReceivedAtDay*1440)
				}
				observable = append(observable, label+"："+fact.Text)
			}
		}
	}
	if pov == nil {
		return simulation, fmt.Errorf("chapter activation has no actual POV character choice")
	}
	sort.Slice(simulation.CharacterDecisions, func(i, j int) bool {
		return simulation.CharacterDecisions[i].Character < simulation.CharacterDecisions[j].Character
	})
	simulation.Version, simulation.Chapter, simulation.GenerationID = 2, evidence.Session.Chapter, evidence.Session.GenerationID
	simulation.BaseTickID, simulation.GeneratedAt = baseTickID, finalReceipt.GeneratedAt
	simulation.StoryTime = &StoryTimeChapterSchedule{Chapter: simulation.Chapter, StartDay: first.StartDay, EndDay: last.EndDay}
	simulation.TimeWindow = fmt.Sprintf("故事开始后 %.12g–%.12g 分钟", first.StartDay*1440, last.EndDay*1440)
	simulation.PhysicalState, simulation.CharacterActivation = &physical, binding
	activationPolicy := CharacterActivationCyclePolicy
	if HasCharacterSelfChronologyPolicyV1(first.Evidence.Stimulus.Sources) {
		activationPolicy = CharacterActivationCyclePolicyV2
	}
	if first.Version == CharacterActivationCycleV3Version {
		activationPolicy = CharacterActivationCyclePolicyV3
	}
	simulation.Sources = normalizeV2Strings(append(append(append([]string(nil), first.Evidence.Stimulus.Sources...), sources...), activationPolicy, PlanGroundingActivationPolicy, "character-activation-chapter:"+evidence.Digest))
	simulation.ProtagonistProjection = ProtagonistDecisionProjection{Protagonist: evidence.Context.POVCharacter, ObservableEffects: normalizeV2Strings(observable), AvailableOptions: append([]string(nil), pov.AvailableOptions...), ChosenDecision: pov.Decision, DecisionReason: pov.DecisionReason,
		PlanConstraints: []string{"按character_decision_trace保留每个周期的原始选择和实际顺序；兼容决策列表只代表各角色最近一次选择，不能覆盖早期选择。", "新信息不得倒灌到较早周期；通信报告、持有材料和实际读取/核验必须区分。"},
		CausalChain:     []string{"先独立决定并裁决各周期，再按所有已发生的事件规划整章。"}}
	// Existing fields remain an inventory of real source digests, not a
	// fabricated aggregate arbitration. CharacterActivation is authoritative
	// for the whole-chapter chain and must be verified by new consumers.
	simulation.CharacterAgentProtocol = &CharacterAgentProtocolReceipt{Version: CharacterAgentDecisionProtocolV2Version, RegistryRoot: first.Evidence.Registry.RegistryRoot, StimulusDigest: first.Evidence.Stimulus.Digest, ActivationDigest: first.Evidence.Activation.Digest, ObservationDigests: normalizeV2Strings(observations), ProposalDigests: normalizeV2Strings(proposals), ArbitrationRound: finalReceipt.Round, ArbitrationDigest: finalReceipt.Digest, MemoryRoots: normalizeV2Strings(memoryRoots), ProtocolDigest: evidence.ProtocolDigest}
	simulation.SimulationID = ComputeChapterWorldSimulationID(simulation)
	return simulation, nil
}

func ValidateCharacterActivationSimulation(simulation ChapterWorldSimulation, evidence CharacterActivationChapterEvidence) error {
	verified, err := VerifyCharacterActivationChapter(evidence)
	if err != nil {
		return err
	}
	return verified.ValidateSimulation(simulation)
}

func validateCharacterActivationSimulation(simulation ChapterWorldSimulation, verified VerifiedCharacterActivationChapter) error {
	evidence := verified.evidence
	if simulation.CharacterActivation == nil || simulation.CharacterActivation.ChapterEvidenceDigest != evidence.Digest {
		return fmt.Errorf("simulation lacks its full chapter activation evidence binding")
	}
	expected, err := verified.BuildSimulation(simulation.BaseTickID, simulation.Sources)
	if err != nil {
		return err
	}
	// GeneratedAt is provenance, not part of the established simulation ID.
	expected.GeneratedAt = simulation.GeneratedAt
	left, _ := json.Marshal(expected)
	right, _ := json.Marshal(simulation)
	if string(left) != string(right) {
		return fmt.Errorf("chapter activation simulation differs from its exact original decisions/results")
	}
	return nil
}
