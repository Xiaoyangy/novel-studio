package tools

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/testutil"
)

func v3ToolFixture(t *testing.T) (*store.Store, domain.CharacterActivationSession, domain.CharacterActivationInputSet, *store.CharacterArbitrationV3) {
	t.Helper()
	session, input := testutil.CharacterActivationV3Inputs(t)
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
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
	view, err := st.PrepareCharacterArbitrationV3(session, nil, "sha256:"+strings.Repeat("d", 64))
	if err != nil {
		t.Fatal(err)
	}
	return st, session, input, view
}

func v3SubmitToolProposal(t *testing.T, st *store.Store, session domain.CharacterActivationSession, view *store.CharacterArbitrationV3, observation domain.CharacterObservationPacket) {
	t.Helper()
	tool, err := NewSubmitCharacterActivationV3DecisionTool(st, session, observation, view)
	if err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]any{"location": observation.Location, "current_goal": observation.CurrentGoal, "pressure": observation.Pressure,
		"available_options": []string{"检查", "等待"}, "decision": "检查", "decision_reason": "本人选择", "intended_action": "进行一分钟本人工序", "action_duration": "一分钟",
		"knowledge_refs": []string{observation.KnownFacts[0].ID}, "self_tasks": []map[string]any{{"task_id": "inspection", "kind": "work", "action": "本人例行检查", "progress_target": 1, "progress_unit": "minute", "knowledge_refs": []string{observation.KnownFacts[0].ID}}}})
	if _, err := tool.Execute(context.Background(), args); err != nil {
		t.Fatal(err)
	}
	before := arbitrationReferenceFiles(t, st.Dir())
	if _, err := tool.Execute(context.Background(), args); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, arbitrationReferenceFiles(t, st.Dir())) {
		t.Fatal("identical decision retry changed immutable evidence")
	}
}

func v3ArbitrationToolArgs(scope domain.VerifiedCharacterArbitrationSourcesV1, provisional bool) json.RawMessage {
	input := scope.Input()
	p := scope.EffectiveProposals()[0]
	start, end := input.Stimulus.StoryClock.CurrentDay, input.Stimulus.StoryClock.CurrentDay+1.0/1440
	var executions []domain.CharacterSelfExecutionV2
	var conflicts []domain.WorldArbitrationConflict
	if provisional {
		end = start
		conflicts = []domain.WorldArbitrationConflict{{ID: "order-conflict", Kind: "time", AffectedAgentIDs: []string{p.AgentID}, Feedback: "请明确本人顺序"}}
	} else {
		executions = []domain.CharacterSelfExecutionV2{{TaskID: "inspection", Status: "completed", StartDay: &start, EndDay: &end}}
	}
	raw, _ := json.Marshal(map[string]any{"time_window": input.Stimulus.TimeWindow, "story_time": domain.StoryTimeChapterSchedule{Chapter: p.Chapter, StartDay: start, EndDay: end},
		"resolutions": []map[string]any{{"agent_id": p.AgentID, "character": p.Character, "proposal_digest": p.Digest, "decision": p.Decision, "intended_action": p.IntendedAction,
			"action_order": 1, "outcome": "success", "completion_state": "completed", "immediate_result": "本人本次工序结果", "state_after": "阶段结束",
			"post_state": map[string]any{"location": p.Location, "resource_updates": []any{}}, "self_executions": executions,
			"butterfly_effects": []domain.DecisionButterflyEffect{{Effect: "结果影响后续", TransmissionPath: "实际行动", ArrivalChapter: p.Chapter, ProtagonistImpact: "后续有据"}}}},
		"conflicts": conflicts, "hard_contract_status": "feasible", "finalized": !provisional, "resource_settlements": []any{}})
	return raw
}

func TestArbitrationV3ToolsPersistOnlyActualRoundAndRecoverAffectedRevision(t *testing.T) {
	st, session, input, view := v3ToolFixture(t)
	v3SubmitToolProposal(t, st, session, view, input.Observations[0])
	tool, err := NewResolveCharacterArbitrationV3Tool(st, session, view, 1, "sha256:"+strings.Repeat("d", 64))
	if err != nil {
		t.Fatal(err)
	}
	scope, err := view.Sources(1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tool.Execute(context.Background(), v3ArbitrationToolArgs(scope, true)); err != nil {
		t.Fatal(err)
	}
	loaded, err := st.LoadCharacterActivationSession(session.GenerationID, session.Chapter)
	if err != nil || loaded.Digest != session.Digest {
		t.Fatalf("provisional tool advanced the cycle: %v", err)
	}
	r1, err := view.LoadArbitration(1)
	if err != nil || r1 == nil || r1.Finalized {
		t.Fatalf("R1 not durably recoverable: %v", err)
	}
	revision := input.Observations[0]
	revision.Round, revision.ConflictFeedback = 2, []string{"time：请明确本人顺序"}
	revision, err = domain.FinalizeCharacterObservationPacket(revision)
	if err != nil {
		t.Fatal(err)
	}
	if err := view.SaveObservation(revision); err != nil {
		t.Fatal(err)
	}
	v3SubmitToolProposal(t, st, session, view, revision)
	tool, err = NewResolveCharacterArbitrationV3Tool(st, session, view, 2, "sha256:"+strings.Repeat("d", 64))
	if err != nil {
		t.Fatal(err)
	}
	scope, err = view.Sources(2)
	if err != nil {
		t.Fatal(err)
	}
	args := v3ArbitrationToolArgs(scope, false)
	if _, err := tool.Execute(context.Background(), args); err != nil {
		t.Fatal(err)
	}
	before := arbitrationReferenceFiles(t, st.Dir())
	if _, err := tool.Execute(context.Background(), args); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, arbitrationReferenceFiles(t, st.Dir())) {
		t.Fatal("same R2 retry changed saved evidence")
	}
	cycle, err := view.FinalizeCycle(nil)
	if err != nil || cycle.Version != domain.CharacterActivationCycleV3Version || len(cycle.Evidence.Arbitrations) != 2 {
		t.Fatalf("full real two-round cycle missing: %v", err)
	}
	loaded, err = st.LoadCharacterActivationSession(session.GenerationID, session.Chapter)
	if err != nil || len(loaded.CycleDigests) != 0 {
		t.Fatal("tool stole the controller's cycle commit")
	}
	if sim, err := st.LoadChapterWorldSimulation(session.Chapter); err != nil || sim != nil {
		t.Fatal("round tool published a chapter simulation")
	}
}

func TestArbitrationV3ToolRejectsForeignStoreViewBeforeWriting(t *testing.T) {
	st, session, input, view := v3ToolFixture(t)
	foreign := store.NewStore(t.TempDir())
	if err := foreign.Init(); err != nil {
		t.Fatal(err)
	}
	before := arbitrationReferenceFiles(t, st.Dir())
	if _, err := NewSubmitCharacterActivationV3DecisionTool(foreign, session, input.Observations[0], view); err == nil {
		t.Fatal("accepted cross-Store character writer")
	}
	if _, err := NewResolveCharacterArbitrationV3Tool(foreign, session, view, 1, ""); err == nil {
		t.Fatal("accepted cross-Store arbiter writer")
	}
	if !reflect.DeepEqual(before, arbitrationReferenceFiles(t, st.Dir())) {
		t.Fatal("rejected binding wrote files")
	}
}
