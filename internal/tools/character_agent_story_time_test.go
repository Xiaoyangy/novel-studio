package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func newStoryClockArbitrationTool(t *testing.T, withClock bool) (*ResolveChapterWorldTool, map[string]any) {
	t.Helper()
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init("时钟测试", 3); err != nil {
		t.Fatal(err)
	}
	registry, err := domain.FinalizeCharacterAgentRegistry(domain.CharacterAgentRegistry{Entries: []domain.CharacterAgentRecord{{
		AgentID: "ca_clock", Character: "林默", Tier: "core", Status: domain.CharacterAgentActive, MemoryVersion: 1,
	}}})
	if err != nil {
		t.Fatal(err)
	}
	stimulus := domain.WorldStimulusPacket{GenerationID: "pg2_story_clock", Chapter: 3, TimeWindow: "chapter-000003"}
	if withClock {
		contract, err := domain.DeriveStoryTimeContract("3章，90分钟", 3)
		if err != nil {
			t.Fatal(err)
		}
		clock, err := domain.FinalizeStoryClockContext(domain.StoryClockContext{
			CurrentDay: 61.0 / 86400, TimeContractCoreDigest: contract.CoreDigest,
			DurationDaysMin: contract.DurationDaysMin, DurationDaysMax: contract.DurationDaysMax,
		})
		if err != nil {
			t.Fatal(err)
		}
		stimulus.StoryClock = &clock
	}
	stimulus, err = domain.FinalizeWorldStimulusPacket(stimulus)
	if err != nil {
		t.Fatal(err)
	}
	observation, err := domain.FinalizeCharacterObservationPacket(domain.CharacterObservationPacket{
		GenerationID: stimulus.GenerationID, Chapter: stimulus.Chapter, Round: 1, AgentID: "ca_clock", Character: "林默",
		StimulusDigest: stimulus.Digest, CurrentGoal: "保全证据", Pressure: "闭馆时间将至",
		KnownFacts: []domain.CharacterAgentFact{{ID: "door", Kind: "known", Text: "门尚未关闭"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	activation, err := domain.FinalizeCharacterAgentActivation(domain.CharacterAgentActivation{
		GenerationID: stimulus.GenerationID, Chapter: stimulus.Chapter, RegistryRoot: registry.RegistryRoot,
		Entries: []domain.CharacterAgentActivationEntry{{AgentID: observation.AgentID, Character: observation.Character, Tier: "core", State: domain.CharacterAgentActive, Reasons: []string{"protagonist"}, ObservationDigest: observation.Digest}},
	})
	if err != nil {
		t.Fatal(err)
	}
	proposal, err := domain.FinalizeCharacterDecisionProposal(domain.CharacterDecisionProposal{
		GenerationID: stimulus.GenerationID, Chapter: stimulus.Chapter, Round: 1, AgentID: observation.AgentID, Character: observation.Character,
		ObservationDigest: observation.Digest, Location: "档案室", CurrentGoal: "保全证据", Pressure: "闭馆时间将至",
		AvailableOptions: []string{"带走账本", "原地等候"}, Decision: "带走账本", DecisionReason: "门尚未关闭，账本可以携带",
		IntendedAction: "把账本放入防水袋", ActionDuration: "六十一秒", KnowledgeRefs: []string{"door"},
	}, observation)
	if err != nil {
		t.Fatal(err)
	}
	for _, save := range []func() error{
		func() error {
			return st.CharacterAgents.SaveRegistrySnapshot(stimulus.GenerationID, stimulus.Chapter, registry)
		},
		func() error { return st.CharacterAgents.SaveStimulus(stimulus) },
		func() error { return st.CharacterAgents.SaveActivation(activation) },
		func() error { return st.CharacterAgents.SaveObservation(observation) },
		func() error { return st.CharacterAgents.SaveProposal(proposal, observation) },
	} {
		if err := save(); err != nil {
			t.Fatal(err)
		}
	}
	args := map[string]any{
		"time_window": "", "hard_contract_status": "feasible", "finalized": true,
		"resolutions": []domain.CharacterDecisionResolution{{
			AgentID: proposal.AgentID, Character: proposal.Character, ProposalDigest: proposal.Digest,
			Decision: proposal.Decision, IntendedAction: proposal.IntendedAction, ActionOrder: 1,
			Outcome: "success", CompletionState: "completed", ImmediateResult: "账本已经装袋", StateAfter: "持有装袋账本",
			ButterflyEffects: []domain.DecisionButterflyEffect{{Effect: "留下空抽屉", TransmissionPath: "管理员巡查", ArrivalChapter: 3, Visibility: "delayed", ProtagonistImpact: "有人发现取走账本"}},
		}},
		"protagonist_projection": domain.ProtagonistDecisionProjection{
			Protagonist: proposal.Character, AvailableOptions: proposal.AvailableOptions, ChosenDecision: proposal.Decision,
			DecisionReason: proposal.DecisionReason, PlanConstraints: []string{"保留装袋动作"}, CausalChain: []string{"装袋后才能携带账本"},
		},
	}
	return NewResolveChapterWorldTool(st, stimulus, activation, []domain.CharacterDecisionProposal{proposal}, "sha256:clock-protocol", nil, 1), args
}

func TestResolveChapterWorldClockPersistsActualTimeAndRejectsReceiptDrift(t *testing.T) {
	tool, args := newStoryClockArbitrationTool(t, true)
	want := domain.StoryTimeChapterSchedule{Chapter: 3, StartDay: 61.0 / 86400, EndDay: 88.0 / 1440}
	args["story_time"] = want
	// The numeric receipt is authoritative even if the optional display label
	// accidentally repeats an obsolete multi-day plan.
	args["time_window"] = "已经过去六天"
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tool.Execute(context.Background(), raw); err != nil {
		t.Fatal(err)
	}
	sim, err := tool.store.LoadChapterWorldSimulation(3)
	if err != nil || sim == nil || !domain.SameStoryTime(sim.StoryTime, &want) {
		t.Fatalf("actual story clock was lost in simulation: %+v, %v", sim, err)
	}
	if strings.HasPrefix(sim.TimeWindow, "chapter-") || !strings.Contains(sim.TimeWindow, "88") || strings.Contains(sim.TimeWindow, "六天") {
		t.Fatalf("numeric clock fell back to chapter label: %q", sim.TimeWindow)
	}
	receipt, err := tool.store.CharacterAgents.LoadArbitration(tool.stimulus.GenerationID, 3, 1)
	if err != nil || receipt == nil || !domain.SameStoryTime(receipt.StoryTime, sim.StoryTime) {
		t.Fatalf("receipt did not persist the exact clock: %+v, %v", receipt, err)
	}
	if sim.CharacterDecisions[0].Decision != tool.proposals[0].Decision || sim.CharacterDecisions[0].Action != tool.proposals[0].IntendedAction {
		t.Fatal("clock materialization rewrote character intent")
	}
	changed := want
	changed.EndDay += 1.0 / 86400
	sim.StoryTime = &changed
	sim.SimulationID = chapterWorldSimulationID(*sim)
	if err := validateStoredCharacterAgentProtocol(tool.store, *sim); err == nil || !strings.Contains(err.Error(), "story_time differs") {
		t.Fatalf("refreshed simulation hash hid receipt clock drift: %v", err)
	}
}

func TestResolveChapterWorldClockRejectsMissingOrInvalidTimeBeforePersistence(t *testing.T) {
	for _, tc := range []struct {
		name string
		time any
	}{
		{"missing", nil},
		{"incomplete", map[string]any{"chapter": 3, "end_day": 0.01}},
		{"wrong chapter", domain.StoryTimeChapterSchedule{Chapter: 2, StartDay: 61.0 / 86400, EndDay: 0.01}},
		{"nominal start", domain.StoryTimeChapterSchedule{Chapter: 3, StartDay: 60.0 / 1440, EndDay: 88.0 / 1440}},
		{"over deadline", domain.StoryTimeChapterSchedule{Chapter: 3, StartDay: 61.0 / 86400, EndDay: 91.0 / 1440}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tool, args := newStoryClockArbitrationTool(t, true)
			if tc.time != nil {
				args["story_time"] = tc.time
			}
			raw, _ := json.Marshal(args)
			if _, err := tool.Execute(context.Background(), raw); err == nil || !strings.Contains(err.Error(), "story_time") {
				t.Fatalf("invalid actual time was accepted: %v", err)
			}
			if receipt, err := tool.store.CharacterAgents.LoadArbitration(tool.stimulus.GenerationID, 3, 1); err != nil || receipt != nil {
				t.Fatalf("invalid time left persisted arbitration: %+v, %v", receipt, err)
			}
		})
	}
}

func TestResolveChapterWorldStoryTimeSchemaIsRequiredOnlyWithClock(t *testing.T) {
	for _, withClock := range []bool{false, true} {
		t.Run(fmt.Sprint(withClock), func(t *testing.T) {
			tool, args := newStoryClockArbitrationTool(t, withClock)
			raw, _ := json.Marshal(tool.Schema())
			var definition struct {
				Required []string `json:"required"`
			}
			if err := json.Unmarshal(raw, &definition); err != nil {
				t.Fatal(err)
			}
			required := false
			for _, field := range definition.Required {
				required = required || field == "story_time"
			}
			if required != withClock {
				t.Fatalf("story_time required=%t, clock=%t", required, withClock)
			}
			if !withClock {
				raw, _ := json.Marshal(args)
				if _, err := tool.Execute(context.Background(), raw); err != nil {
					t.Fatalf("legacy clockless resolve was rejected: %v", err)
				}
			}
		})
	}
}
