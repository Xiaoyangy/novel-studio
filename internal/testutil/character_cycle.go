// Package testutil contains deterministic, non-model test fixtures.
package testutil

import (
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

func CharacterCycle(t *testing.T, index int, previous string, before *domain.WorldPhysicalStateV2, day float64, contextDigests ...string) domain.CharacterActivationCycle {
	t.Helper()
	const generation = "pg2_cycle_fixture"
	const fuel = "res_0000000000000001"
	contextDigest := "sha256:" + strings.Repeat("a", 64)
	if len(contextDigests) > 0 {
		contextDigest = contextDigests[0]
	}
	registry := domain.CharacterAgentRegistry{}
	registry, actor, err := registry.UpsertCharacter("甲", nil, "core", 1, "")
	if err != nil {
		t.Fatal(err)
	}
	registry, err = domain.FinalizeCharacterAgentRegistry(registry)
	if err != nil {
		t.Fatal(err)
	}
	amount := 12.0
	state := domain.WorldPhysicalStateV2{Version: domain.WorldPhysicalStateV2Version, Resources: []domain.WorldResourceBalanceV2{{ResourceID: fuel, Name: "燃油", Unit: "L", ActualAmount: &amount}}, Actors: []domain.CharacterPhysicalStateV2{{AgentID: actor.AgentID, Character: "甲", Location: "船上", Resources: []domain.CharacterResourceHoldingV2{{ResourceID: fuel, PerceivedName: "燃油", PerceivedUnit: "L", Access: "exclusive", Perception: domain.ResourcePerceptionV2{Kind: "last_observed", Amount: &amount, EvidenceRefs: []string{"known-fuel"}}, EvidenceRefs: []string{"known-fuel"}}}}}}
	if before != nil {
		state = *before
	}
	state, err = domain.FinalizeWorldPhysicalStateV2(state)
	if err != nil {
		t.Fatal(err)
	}
	clock, err := domain.FinalizeStoryClockContext(domain.StoryClockContext{CurrentDay: day, TimeContractCoreDigest: "sha256:" + strings.Repeat("b", 64), DurationDaysMin: 1, DurationDaysMax: 1})
	if err != nil {
		t.Fatal(err)
	}
	token, err := domain.CharacterActivationCycleSourceToken(generation, 1, index, contextDigest, previous)
	if err != nil {
		t.Fatal(err)
	}
	stimulus, err := domain.FinalizeWorldStimulusPacket(domain.WorldStimulusPacket{Version: domain.WorldStimulusPacketV2Version, GenerationID: generation, Chapter: 1, TimeWindow: "本轮一分钟", StoryClock: &clock, PhysicalState: &state, Sources: []string{token}})
	if err != nil {
		t.Fatal(err)
	}
	views, err := domain.BuildCharacterResourceViewsV2(state, actor.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	observation, err := domain.FinalizeCharacterObservationPacket(domain.CharacterObservationPacket{Version: domain.CharacterObservationV2Version, GenerationID: generation, Chapter: 1, Round: 1, AgentID: actor.AgentID, Character: "甲", Tier: "core", Location: "船上", CurrentGoal: "检查设备", Pressure: "时间有限", StimulusDigest: stimulus.Digest, MemoryRoot: "sha256:" + strings.Repeat("c", 64), ResourceViews: views, KnownFacts: []domain.CharacterAgentFact{{ID: "known-fuel", Kind: "known", Text: "知道燃油用途"}}})
	if err != nil {
		t.Fatal(err)
	}
	activation, err := domain.FinalizeCharacterAgentActivation(domain.CharacterAgentActivation{GenerationID: generation, Chapter: 1, RegistryRoot: registry.RegistryRoot, Entries: []domain.CharacterAgentActivationEntry{{AgentID: actor.AgentID, Character: "甲", Tier: "core", State: domain.CharacterAgentActive, Reasons: []string{"task_progress"}, ObservationDigest: observation.Digest}}})
	if err != nil {
		t.Fatal(err)
	}
	proposal, err := domain.FinalizeCharacterDecisionProposal(domain.CharacterDecisionProposal{GenerationID: generation, Chapter: 1, Round: 1, AgentID: actor.AgentID, Character: "甲", Location: "船上", ObservationDigest: observation.Digest, CurrentGoal: "检查设备", Pressure: "时间有限", AvailableOptions: []string{"继续检查", "停止"}, Decision: "继续检查", DecisionReason: "当前条件允许", IntendedAction: "使用少量燃油完成本轮检查", ActionDuration: "一分钟", KnowledgeRefs: []string{"known-fuel"}}, observation)
	if err != nil {
		t.Fatal(err)
	}
	start := *state.Resources[0].ActualAmount
	delta := -.2
	end := start + delta
	post := state.Actors[0]
	for _, candidate := range state.Actors {
		if candidate.AgentID == actor.AgentID {
			post = candidate
			break
		}
	}
	arbitration, err := domain.FinalizeWorldArbitrationReceipt(domain.WorldArbitrationReceipt{Version: domain.WorldArbitrationReceiptV2Version, GenerationID: generation, Chapter: 1, Round: 1, StoryTime: &domain.StoryTimeChapterSchedule{Chapter: 1, StartDay: day, EndDay: day + 1.0/1440}, StimulusDigest: stimulus.Digest, ActivationDigest: activation.Digest, ProposalDigests: []string{proposal.Digest}, Resolutions: []domain.CharacterDecisionResolution{{AgentID: actor.AgentID, Character: "甲", ProposalDigest: proposal.Digest, Decision: proposal.Decision, IntendedAction: proposal.IntendedAction, ActionOrder: 1, Outcome: "success", CompletionState: "completed", ImmediateResult: "完成本轮检查", StateAfter: "本轮检查结果保留", PostState: &post, VisibleToPOV: true, ButterflyEffects: []domain.DecisionButterflyEffect{{Effect: "本轮燃油已经消耗", TransmissionPath: "本人操作", ArrivalChapter: 1, ProtagonistImpact: "后续行动需要保留余量"}}}}, ResourceSettlements: []domain.ResourceSettlementV2{{ResourceID: fuel, Before: &start, Delta: &delta, After: &end, EvidenceRefs: []string{proposal.Digest}}}, HardContractStatus: "feasible", Finalized: true, ProtagonistProjection: domain.ProtagonistDecisionProjection{Protagonist: "甲", ChosenDecision: proposal.Decision, DecisionReason: proposal.DecisionReason, AvailableOptions: proposal.AvailableOptions, PlanConstraints: []string{"不改变角色选择"}, CausalChain: []string{"条件支持检查"}}}, stimulus, activation, []domain.CharacterDecisionProposal{proposal}, 1)
	if err != nil {
		t.Fatal(err)
	}
	evidence, err := domain.FinalizeCharacterAgentEvidenceBundle(domain.CharacterAgentEvidenceBundle{GenerationID: generation, Chapter: 1, Registry: registry, Stimulus: stimulus, Activation: activation, Observations: []domain.CharacterObservationPacket{observation}, Proposals: []domain.CharacterDecisionProposal{proposal}, Arbitrations: []domain.WorldArbitrationReceipt{arbitration}, MemoryRoots: []string{observation.MemoryRoot}, ProtocolDigest: "sha256:" + strings.Repeat("d", 64)})
	if err != nil {
		t.Fatal(err)
	}
	cycle, err := domain.FinalizeCharacterActivationCycle(domain.CharacterActivationCycle{GenerationID: generation, Chapter: 1, Index: index, PreviousDigest: previous, ChapterContextDigest: contextDigest, Evidence: evidence})
	if err != nil {
		t.Fatal(err)
	}
	return cycle
}

func CycleReadiness(t *testing.T, cycle domain.CharacterActivationCycle, decision string) domain.CharacterChapterReadiness {
	t.Helper()
	value := domain.CharacterChapterReadiness{GenerationID: cycle.GenerationID, Chapter: cycle.Chapter, CycleDigest: cycle.Digest, ReviewProtocol: "sha256:" + strings.Repeat("e", 64), Decision: decision, Reason: "根据本轮实际结果作测试判定"}
	if decision == "hard_conflict" {
		value.UnresolvedHardContracts = []string{"当前硬义务无法兑现"}
	}
	readiness, err := domain.FinalizeCharacterChapterReadiness(value)
	if err != nil {
		t.Fatal(err)
	}
	return readiness
}
