package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/testutil"
)

func activationToolFixture(t *testing.T, includeProposal bool) (*store.Store, domain.CharacterActivationSession, domain.CharacterActivationCycle, *store.CharacterAgentStore) {
	t.Helper()
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	cycle := testutil.CharacterCycle(t, 1, "", nil, 0)
	e := cycle.Evidence
	session, err := domain.NewCharacterActivationSession(e.GenerationID, e.Chapter, cycle.ChapterContextDigest, *e.Stimulus.PhysicalState, 0, 4)
	if err != nil {
		t.Fatal(err)
	}
	proof, err := st.CharacterAgents.ForActivationCycle(session)
	if err != nil {
		t.Fatal(err)
	}
	for _, save := range []func() error{
		func() error { return proof.SaveRegistrySnapshot(e.GenerationID, e.Chapter, e.Registry) },
		func() error { return proof.SaveStimulus(e.Stimulus) },
		func() error { return proof.SaveObservation(e.Observations[0]) },
		func() error { return proof.SaveActivation(e.Activation) },
	} {
		if err := save(); err != nil {
			t.Fatal(err)
		}
	}
	if includeProposal {
		if err := proof.SaveProposal(e.Proposals[0], e.Observations[0]); err != nil {
			t.Fatal(err)
		}
	}
	return st, session, cycle, proof
}

func activationArbiterArgs(t *testing.T, cycle domain.CharacterActivationCycle) json.RawMessage {
	t.Helper()
	r := cycle.Evidence.Arbitrations[0]
	raw, err := json.Marshal(map[string]any{
		"time_window": cycle.Evidence.Stimulus.TimeWindow, "story_time": r.StoryTime,
		"resolutions": r.Resolutions, "conflicts": r.Conflicts, "hard_contract_status": r.HardContractStatus,
		"hard_contract_conflicts": r.HardContractConflicts, "protagonist_projection": r.ProtagonistProjection,
		"finalized": r.Finalized, "resource_settlements": r.ResourceSettlements, "resource_deliveries": r.ResourceDeliveries,
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func nonCycleFiles(t *testing.T, root string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if filepath.ToSlash(rel) == "meta/character_agents/activation_sessions" {
				return filepath.SkipDir
			}
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files[rel] = string(raw)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func TestResolveCharacterActivationDoesNotPublishChapterAndIsIdempotent(t *testing.T) {
	st, session, cycle, proof := activationToolFixture(t, true)
	e := cycle.Evidence
	if err := st.Drafts.SaveChapterPlanPartial(1, map[string]any{"chapter": 1, "preserve": "paid partial"}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveChapterWorldSimulationPartial(domain.ChapterWorldSimulation{Version: 1, Chapter: 1, TimeWindow: "旧章内进度"}); err != nil {
		t.Fatal(err)
	}
	before := nonCycleFiles(t, st.Dir())
	tool, err := NewResolveCharacterActivationTool(st, session, e.Stimulus, e.Activation, e.Proposals, e.ProtocolDigest, nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	raw := activationArbiterArgs(t, cycle)
	first, err := tool.Execute(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err := json.Unmarshal(first, &result); err != nil {
		t.Fatal(err)
	}
	if result["resolved"] != true || result["cycle_closed"] != true || result["simulated"] != nil || result["simulation_id"] != nil {
		t.Fatalf("cycle pretends to finish chapter: %s", first)
	}
	for range 3 {
		retry, err := tool.Execute(context.Background(), raw)
		if err != nil || string(retry) != string(first) {
			t.Fatalf("retry changed paid receipt: %s %v", retry, err)
		}
	}
	receipt, err := proof.LoadArbitration(e.GenerationID, e.Chapter, 1)
	if err != nil || receipt == nil || !receipt.Finalized || receipt.Resolutions[0].Decision != e.Proposals[0].Decision {
		t.Fatalf("missing/rewritten cycle receipt: %+v %v", receipt, err)
	}
	if !reflect.DeepEqual(before, nonCycleFiles(t, st.Dir())) {
		t.Fatal("cycle altered chapter progress, memory, or legacy evidence")
	}
	if simulation, err := st.LoadChapterWorldSimulation(1); err != nil || simulation != nil {
		t.Fatal("cycle published a chapter simulation")
	}
	// The legacy constructor cannot accidentally materialize cycle evidence.
	legacy := NewResolveChapterWorldTool(st, e.Stimulus, e.Activation, e.Proposals, e.ProtocolDigest, nil, 1)
	if _, err := legacy.Execute(context.Background(), raw); err == nil {
		t.Fatal("legacy arbiter accepted a cycle")
	}
}

func TestSubmitCharacterActivationPersistsOnlyBoundCycle(t *testing.T) {
	st, session, cycle, proof := activationToolFixture(t, false)
	e := cycle.Evidence
	tool, err := NewSubmitCharacterActivationDecisionTool(st, session, e.Observations[0])
	if err != nil {
		t.Fatal(err)
	}
	p := e.Proposals[0]
	raw, _ := json.Marshal(map[string]any{
		"location": p.Location, "current_goal": p.CurrentGoal, "pressure": p.Pressure,
		"available_options": p.AvailableOptions, "decision": p.Decision, "decision_reason": p.DecisionReason,
		"intended_action": p.IntendedAction, "action_duration": p.ActionDuration, "knowledge_refs": p.KnowledgeRefs,
	})
	first, err := tool.Execute(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	second, err := tool.Execute(context.Background(), raw)
	if err != nil || string(first) != string(second) {
		t.Fatalf("cycle decision retry is not idempotent: %v", err)
	}
	stored, err := proof.LoadProposal(e.GenerationID, 1, 1, p.AgentID)
	if err != nil || stored == nil || stored.Decision != p.Decision {
		t.Fatalf("missing cycle proposal: %v", err)
	}
	if legacy, err := st.CharacterAgents.LoadProposal(e.GenerationID, 1, 1, p.AgentID); err != nil || legacy != nil {
		t.Fatal("proposal escaped cycle namespace")
	}
	bad := e.Observations[0]
	bad.Location = "未到达的地点"
	if _, err := NewSubmitCharacterActivationDecisionTool(st, session, bad); err == nil {
		t.Fatal("unbound observation accepted by constructor")
	}
}

func TestResolveCharacterActivationRejectsAlteredIntentBeforePersistence(t *testing.T) {
	st, session, cycle, proof := activationToolFixture(t, true)
	e := cycle.Evidence
	tool, err := NewResolveCharacterActivationTool(st, session, e.Stimulus, e.Activation, e.Proposals, e.ProtocolDigest, nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	raw := activationArbiterArgs(t, cycle)
	bad := strings.Replace(string(raw), `"decision":"继续检查"`, `"decision":"交出燃油"`, 1)
	if _, err := tool.Execute(context.Background(), json.RawMessage(bad)); err == nil {
		t.Fatal("arbiter rewrote actor intent")
	}
	if receipt, err := proof.LoadArbitration(e.GenerationID, 1, 1); err != nil || receipt != nil {
		t.Fatal("invalid cycle receipt persisted")
	}
	altered := e.Proposals[0]
	altered.Decision = "改为离开"
	if _, err := NewResolveCharacterActivationTool(st, session, e.Stimulus, e.Activation, []domain.CharacterDecisionProposal{altered}, e.ProtocolDigest, nil, 1); err == nil {
		t.Fatal("unbound proposal accepted by constructor")
	}
}
