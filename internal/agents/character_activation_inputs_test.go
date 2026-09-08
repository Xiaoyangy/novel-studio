package agents

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/testutil"
)

func activationInputWithSleepingActor(t *testing.T) domain.CharacterActivationInputSet {
	t.Helper()
	input := testutil.CharacterActivationInputs(t)
	registry, sleeper, err := input.Registry.UpsertCharacter("乙", nil, "important", 1, "")
	if err != nil {
		t.Fatal(err)
	}
	input.Registry = registry
	input.Stimulus.PhysicalState.Actors = append(input.Stimulus.PhysicalState.Actors, domain.CharacterPhysicalStateV2{AgentID: sleeper.AgentID, Character: sleeper.Character, Location: "岸边", Resources: []domain.CharacterResourceHoldingV2{}})
	physical, err := domain.FinalizeWorldPhysicalStateV2(*input.Stimulus.PhysicalState)
	if err != nil {
		t.Fatal(err)
	}
	input.Stimulus.PhysicalState = &physical
	input.Stimulus, err = domain.FinalizeWorldStimulusPacket(input.Stimulus)
	if err != nil {
		t.Fatal(err)
	}
	input.Observations[0].StimulusDigest = input.Stimulus.Digest
	input.Observations[0].PerceivedEvents = []domain.CharacterAgentFact{{ID: "opening-action", Kind: "own_current_action", Text: "开局只执行一次的准备动作", Source: domain.CharacterSourceRefV2(input.Observations[0].AgentID, "opening"), Visibility: "private"}}
	input.Observations[0], err = domain.FinalizeCharacterObservationPacket(input.Observations[0])
	if err != nil {
		t.Fatal(err)
	}
	input.Activation.Entries[0].ObservationDigest = input.Observations[0].Digest
	memory, err := domain.FinalizeCharacterAgentMemory(domain.CharacterAgentMemory{AgentID: sleeper.AgentID, Character: sleeper.Character, State: "projected", GenerationID: input.Stimulus.GenerationID})
	if err != nil {
		t.Fatal(err)
	}
	observation, err := domain.FinalizeCharacterObservationPacket(domain.CharacterObservationPacket{Version: domain.CharacterObservationV2Version, GenerationID: input.Stimulus.GenerationID, Chapter: 1, Round: 1, AgentID: sleeper.AgentID, Character: sleeper.Character, Tier: "important", Location: "岸边", CurrentGoal: "守岸", Pressure: "风雨", StimulusDigest: input.Stimulus.Digest, MemoryRoot: memory.MemoryRoot, Sources: []string{domain.CharacterSourceRefPolicyV2}, KnownFacts: []domain.CharacterAgentFact{{ID: "sleeper-secret", Kind: "known", Text: "乙的私密消息不得出现在甲的输入", Visibility: "private"}}})
	if err != nil {
		t.Fatal(err)
	}
	input.Memories = append(input.Memories, memory)
	input.Observations = append(input.Observations, observation)
	input.Activation.Entries = append(input.Activation.Entries, domain.CharacterAgentActivationEntry{AgentID: sleeper.AgentID, Character: sleeper.Character, Tier: "important", State: domain.CharacterAgentSleeping})
	input, err = finalizeCharacterActivationInputs(input, "")
	if err != nil {
		t.Fatal(err)
	}
	return input
}

func TestCharacterActivationNextInputUsesActualStateAndPrivateMemory(t *testing.T) {
	st := store.NewStore(t.TempDir())
	input := activationInputWithSleepingActor(t)
	fixture := testutil.CharacterCycle(t, 1, "", nil, 0)
	fixture.Evidence.Arbitrations[0].Resolutions[0].StateAfter = "作者侧秘密未来结果，不能成为角色记忆"
	session, err := domain.NewCharacterActivationSession(input.Stimulus.GenerationID, 1, fixture.ChapterContextDigest, *input.Stimulus.PhysicalState, 0, 4)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CreateCharacterActivationSession(session); err != nil {
		t.Fatal(err)
	}
	proofs, err := st.CharacterAgents.ForActivationCycle(session)
	if err != nil {
		t.Fatal(err)
	}
	if err := proofs.PublishActivationInputs(input); err != nil {
		t.Fatal(err)
	}
	// No generation-wide projected or canonical memory exists in this store:
	// subsequent input construction must use only the frozen private snapshot.
	model := &activationExecutionModel{cycle: fixture}
	models := &bootstrap.ModelSet{Default: bootstrap.NewSwappableModel("test", "activation-input", model)}
	cfg := bootstrap.Config{}
	cfg.CharacterAgents.MaxRevisionRounds = 1
	cycle, err := runCharacterActivationCycle(context.Background(), cfg, st, models, session, activationInputsForExecution(input, session))
	if err != nil {
		t.Fatal(err)
	}
	if cycle.InputSetDigest != input.Digest {
		t.Fatal("cycle did not bind its frozen private baseline")
	}
	if _, err := st.AppendCharacterActivationCycle(session.Digest, cycle); err != nil {
		t.Fatal(err)
	}
	session, err = domain.AppendCharacterActivationCycle(session, cycle)
	if err != nil {
		t.Fatal(err)
	}
	readiness := testutil.CycleReadiness(t, cycle, "continue")
	if _, err := st.ApplyCharacterChapterReadiness(session.Digest, readiness); err != nil {
		t.Fatal(err)
	}
	session, err = domain.ApplyCharacterChapterReadiness(session, readiness)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(input)
	next, err := buildNextCharacterActivationInputs(input, cycle, session)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(input)
	if string(before) != string(after) {
		t.Fatal("next input mutated the frozen previous private snapshot")
	}
	if next.Stimulus.StoryClock.CurrentDay != cycle.EndDay || next.Stimulus.Chapter != 1 {
		t.Fatal("next cycle reset the clock or fabricated a chapter")
	}
	root, err := domain.CharacterPhysicalRootForCycle(*next.Stimulus.PhysicalState)
	if err != nil || root != cycle.AfterPhysicalRoot {
		t.Fatal("next cycle did not inherit exact arbitrated state")
	}
	for _, observation := range next.Observations {
		if observation.Character == "甲" {
			raw, _ := json.Marshal(observation)
			for _, secret := range []string{"开局只执行一次", "作者侧秘密", "乙的私密消息"} {
				if strings.Contains(string(raw), secret) {
					t.Fatalf("next private observation leaked/replayed %s", secret)
				}
			}
			if len(observation.Memory) != 1 || observation.Memory[0].Accepted {
				t.Fatal("actual cycle result missing or promoted to accepted memory")
			}
		}
	}
	for _, entry := range next.Activation.Entries {
		if entry.Character == "乙" && (entry.State != domain.CharacterAgentSleeping || len(entry.Reasons) != 0) {
			t.Fatalf("unchanged sleeper was activated: %+v", entry)
		}
	}
	for _, memory := range next.Memories {
		if memory.Character != "乙" {
			continue
		}
		for _, old := range input.Memories {
			if old.AgentID == memory.AgentID && !reflect.DeepEqual(old, memory) {
				t.Fatal("sleeping private memory changed")
			}
		}
	}
	loaded, err := loadOrPrepareCharacterActivationInputs(st, session, ProjectedArcBoundary{}, domain.ProjectedPlanningContextV2{}, []string{"mutable-author-future-secret"})
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Stimulus.Digest != next.Stimulus.Digest || len(loaded.Observations) != 1 {
		t.Fatal("production input loader did not use next-cycle builder/activation selection")
	}
	replayed, err := loadOrPrepareCharacterActivationInputs(st, session, ProjectedArcBoundary{}, domain.ProjectedPlanningContextV2{}, nil)
	if err != nil || replayed.Stimulus.Digest != loaded.Stimulus.Digest || model.characterCalls.Load() != 1 || model.arbiterCalls.Load() != 1 {
		t.Fatal("input recovery regenerated evidence or invoked models")
	}
	model.cycle = testutil.CharacterCycle(t, 2, cycle.Digest, next.Stimulus.PhysicalState, cycle.EndDay)
	second, err := runCharacterActivationCycle(context.Background(), cfg, st, models, session, replayed)
	if err != nil {
		t.Fatal(err)
	}
	if model.characterCalls.Load() != 2 || model.arbiterCalls.Load() != 2 || len(second.Evidence.Proposals) != 1 {
		t.Fatal("actual next-cycle executor invoked an unchanged sleeper")
	}
	thirdSession, err := domain.AppendCharacterActivationCycle(session, second)
	if err != nil {
		t.Fatal(err)
	}
	thirdSession, err = domain.ApplyCharacterChapterReadiness(thirdSession, testutil.CycleReadiness(t, second, "continue"))
	if err != nil {
		t.Fatal(err)
	}
	third, err := buildNextCharacterActivationInputs(next, second, thirdSession)
	if err != nil {
		t.Fatal(err)
	}
	// This minimal fixture has no new owner-visible measurement or task
	// feedback on its identical second action. A new receipt/clock alone must
	// not trigger more calls or duplicate the same memory text.
	if len(activeCharacterAgentIDs(third.Activation)) != 0 {
		t.Fatal("identical feedback/new receipt caused an activation loop")
	}
	for _, memory := range third.Memories {
		if memory.Character == "甲" && len(memory.Facts) != 1 {
			t.Fatal("identical cycle feedback duplicated private memory tokens")
		}
	}
	for _, memory := range next.Memories {
		if global, err := st.CharacterAgents.LoadProjectedMemory(session.GenerationID, memory.AgentID); err != nil || global != nil {
			t.Fatal("cycle input polluted global projected memory")
		}
		if canon, err := st.CharacterAgents.LoadCanonicalMemory(memory.AgentID); err != nil || canon != nil {
			t.Fatal("cycle input polluted accepted memory")
		}
	}
}

func TestCharacterActivationInitialInputUsesProductionWorldAndObservationBuilders(t *testing.T) {
	st := storyClockStore(t)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	quantity := 12.0
	character := domain.Character{Name: "甲", Role: "主角", Tier: "core", InitialState: &domain.CharacterInitialState{
		Location: "船上", CurrentGoal: "检查设备", Pressure: "时间有限", CurrentAction: "检查前准备", KnownFacts: []string{"知道燃油用途"},
		ResourceBalances: []domain.InitialCharacterResourceV2{{ResourceID: "res_0000000000000001", Name: "燃油", Unit: "L", ActualAmount: &quantity, PerceivedName: "燃油", PerceivedLabel: "燃油", PerceivedUnit: "L", Access: "exclusive", Perception: domain.ResourcePerceptionV2{Kind: "last_observed", Amount: &quantity, EvidenceRefs: []string{"初始亲见"}}}},
	}}
	if err := st.Characters.Save([]domain.Character{character}); err != nil {
		t.Fatal(err)
	}
	if err := st.Outline.SaveOutline([]domain.OutlineEntry{{Chapter: 1, Title: "甲检查设备", CoreEvent: "甲可自主选择检查或等待"}}); err != nil {
		t.Fatal(err)
	}
	if err := st.EnsureCharacterAgentCanon(0); err != nil {
		t.Fatal(err)
	}
	boundary := ProjectedArcBoundary{FirstChapter: 1, LastChapter: 1, BookLastChapter: 3}
	stimulus, err := buildWorldStimulus(st, "pg2_initial_cycle", 1, boundary, domain.ProjectedPlanningContextV2{}, nil, "baseline", domain.CharacterAgentDecisionProtocolV2Version)
	if err != nil {
		t.Fatal(err)
	}
	session, err := domain.NewCharacterActivationSession(stimulus.GenerationID, 1, "sha256:"+strings.Repeat("a", 64), *stimulus.PhysicalState, stimulus.StoryClock.CurrentDay, 4)
	if err != nil {
		t.Fatal(err)
	}
	inputs, err := loadOrPrepareCharacterActivationInputs(st, session, boundary, domain.ProjectedPlanningContextV2{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(inputs.Observations) != 1 || !domain.HasCharacterSelfExperiencePolicyV2(inputs.Stimulus.Sources) || inputs.Stimulus.StoryClock.CurrentDay != 0 {
		t.Fatal("initial activation did not use current production policies and actual time")
	}
	proofs, err := st.CharacterAgents.ForActivationCycle(session)
	if err != nil {
		t.Fatal(err)
	}
	frozen, err := proofs.LoadActivationInputs()
	if err != nil || frozen == nil {
		t.Fatalf("initial snapshot missing: %v", err)
	}
	// Reconfiguration after an interrupted input preparation must not change
	// frozen packets or the memory baselines already attached to this cycle.
	character.InitialState.CurrentAction = "不应混入旧输入的新开局行动"
	if err := st.Characters.Save([]domain.Character{character}); err != nil {
		t.Fatal(err)
	}
	replayed, err := loadOrPrepareCharacterActivationInputs(st, session, boundary, domain.ProjectedPlanningContextV2{}, nil)
	if err != nil || replayed.Stimulus.Digest != inputs.Stimulus.Digest {
		t.Fatal("recovery rebuilt initial inputs from changed source files")
	}
	for _, observation := range replayed.Observations {
		raw, _ := json.Marshal(observation)
		if strings.Contains(string(raw), "不应混入") {
			t.Fatal("mutated opening leaked into frozen observation")
		}
	}
}
