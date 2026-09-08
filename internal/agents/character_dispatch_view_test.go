package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/voocel/agentcore"
)

func dispatchViewFixture(t *testing.T) (*store.Store, bootstrap.Config, domain.CharacterActivationSession, domain.CharacterActivationInputSet, *store.CharacterArbitrationV3) {
	t.Helper()
	st, cfg, boundary := activationV3RuntimeFixture(t)
	const generation = "pg2_dispatch_view"
	selectionMust(t, st.EnsureCharacterAgentCanon(0))
	opening, err := buildWorldStimulusDraft(st, generation, 1, boundary, domain.ProjectedPlanningContextV2{}, nil, time.Now().UTC().Format(time.RFC3339Nano), domain.CharacterAgentDecisionProtocolV2Version)
	selectionMust(t, err)
	physical, err := domain.PrepareCharacterSelfChronologyStateV1(*opening.PhysicalState)
	selectionMust(t, err)
	opening.PhysicalState = &physical
	readiness, err := buildCharacterReadinessContext(st, generation, 1, boundary, domain.ProjectedPlanningContextV2{}, opening)
	selectionMust(t, err)
	selectionMust(t, st.SaveCharacterReadinessContext(readiness))
	session, err := domain.NewCharacterActivationSession(generation, 1, readiness.Digest, physical, opening.StoryClock.CurrentDay, 4)
	selectionMust(t, err)
	selectionMust(t, st.CreateCharacterActivationSession(session))
	input, err := prepareInitialCharacterActivationInputs(st, session, boundary, domain.ProjectedPlanningContextV2{}, nil)
	selectionMust(t, err)
	proofs, err := st.CharacterAgents.ForActivationCycle(session)
	selectionMust(t, err)
	selectionMust(t, proofs.PublishActivationInputs(input))
	view, err := st.PrepareCharacterArbitrationV3(session, nil, characterActivationProtocolForPolicy(characterActivationPolicyForStimulus(input.Stimulus)))
	selectionMust(t, err)
	return st, cfg, session, input, view
}

func dispatchViewObservations(input domain.CharacterActivationInputSet) (map[string]domain.CharacterObservationPacket, []string) {
	observations := map[string]domain.CharacterObservationPacket{}
	for _, o := range input.Observations {
		observations[o.AgentID] = o
	}
	return observations, activeCharacterAgentIDs(input.Activation)
}

func TestCharacterDispatchViewPoolConcurrentResultsAndResume(t *testing.T) {
	st, cfg, session, input, _ := dispatchViewFixture(t)
	cfg.CharacterAgents.MaxConcurrency = 4
	observations, ids := dispatchViewObservations(input)
	baselineDir := t.TempDir()
	selectionMust(t, os.CopyFS(baselineDir, os.DirFS(st.Dir())))
	baselineStore, baselineModel := store.NewStore(baselineDir), &activationV3RuntimeModel{}
	var workers sync.WaitGroup
	errors := make(chan error, len(ids))
	for _, id := range ids {
		workers.Add(1)
		go func(id string) {
			defer workers.Done()
			// The unchanged standalone entry retains the original per-worker
			// complete reload path, providing an independent result comparison.
			errors <- runOneCharacterAgent(context.Background(), cfg, baselineStore, baselineModel, observations[id], &session)
		}(id)
	}
	workers.Wait()
	close(errors)
	for err := range errors {
		selectionMust(t, err)
	}
	baselineProofs, err := baselineStore.CharacterAgents.ForActivationCycle(session)
	selectionMust(t, err)
	baselineProposals, err := baselineProofs.LoadProposals(session.GenerationID, 1, 1, ids)
	selectionMust(t, err)
	model := &activationV3RuntimeModel{}
	proposals, err := runCharacterProposalRoundWithModel(context.Background(), cfg, st, model, observations, ids, 1, &session)
	selectionMust(t, err)
	if len(proposals) != 3 || model.actorCalls != 3 {
		t.Fatalf("wrong concurrent result/calls: %d/%d", len(proposals), model.actorCalls)
	}
	semantic := func(values []domain.CharacterDecisionProposal) string {
		copy := append([]domain.CharacterDecisionProposal(nil), values...)
		for i := range copy {
			copy[i].SubmittedAt, copy[i].Digest = "", ""
		}
		raw, err := json.Marshal(copy)
		selectionMust(t, err)
		return string(raw)
	}
	if baselineModel.actorCalls != 3 || semantic(baselineProposals) != semantic(proposals) {
		t.Fatal("view reuse changed independently submitted choices or source bindings")
	}
	before, err := json.Marshal(proposals)
	selectionMust(t, err)
	resumed, err := runCharacterProposalRoundWithModel(context.Background(), cfg, store.NewStore(st.Dir()), model, observations, ids, 1, &session)
	selectionMust(t, err)
	after, err := json.Marshal(resumed)
	selectionMust(t, err)
	if model.actorCalls != 3 || string(before) != string(after) {
		t.Fatal("resumed dispatch re-ran an owner or changed its exact saved proposal")
	}
	if _, err := st.LoadCharacterArbitrationV3(session.GenerationID, session.Chapter); err != nil {
		t.Fatalf("parallel proposals broke source authority: %v", err)
	}
}

func TestCharacterDispatchViewRejectsForgedOrForeignWorkerScope(t *testing.T) {
	st, cfg, session, input, view := dispatchViewFixture(t)
	o := input.Observations[0]
	bound := func() *characterDispatchViewV3 {
		return &characterDispatchViewV3{view: view, sessionDigest: session.Digest, round: 1, observations: map[string]string{o.AgentID: o.Digest}}
	}
	model := &activationV3RuntimeModel{}
	for _, kind := range []string{"zero view", "JSON authority", "foreign store", "foreign session", "foreign owner", "changed observation", "wrong round"} {
		t.Run(kind, func(t *testing.T) {
			d, target, current, observation := bound(), st, session, o
			switch kind {
			case "zero view":
				d.view = &store.CharacterArbitrationV3{}
			case "JSON authority":
				raw, err := json.Marshal(d)
				selectionMust(t, err)
				d = &characterDispatchViewV3{}
				selectionMust(t, json.Unmarshal(raw, d))
			case "foreign store":
				target = store.NewStore(t.TempDir())
			case "foreign session":
				d.sessionDigest = "not-this-session"
			case "foreign owner":
				d.observations = map[string]string{}
			case "changed observation":
				observation.CurrentGoal += " altered"
			case "wrong round":
				d.round = 2
			}
			if err := runOneCharacterAgentWithDispatchView(context.Background(), cfg, target, model, observation, d, &current); err == nil {
				t.Fatal("worker accepted foreign/unverified dispatch scope")
			}
		})
	}
	if model.actorCalls != 0 {
		t.Fatal("invalid scope reached a model")
	}
}

type dispatchViewHookModel struct {
	activationV3RuntimeModel
	once    sync.Once
	hook    func() error
	hookErr error
	calls   atomic.Int32
}

func (m *dispatchViewHookModel) Generate(ctx context.Context, messages []agentcore.Message, specs []agentcore.ToolSpec, opts ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	m.calls.Add(1)
	m.once.Do(func() { m.hookErr = m.hook() })
	if m.hookErr != nil {
		return nil, m.hookErr
	}
	return m.activationV3RuntimeModel.Generate(ctx, messages, specs, opts...)
}

func (m *dispatchViewHookModel) GenerateStream(ctx context.Context, messages []agentcore.Message, specs []agentcore.ToolSpec, opts ...agentcore.CallOption) (<-chan agentcore.StreamEvent, error) {
	r, err := m.Generate(ctx, messages, specs, opts...)
	if err != nil {
		return nil, err
	}
	ch := make(chan agentcore.StreamEvent, 1)
	ch <- agentcore.StreamEvent{Type: agentcore.StreamEventDone, Message: r.Message, StopReason: r.Message.StopReason}
	close(ch)
	return ch, nil
}

func TestCharacterDispatchViewStillRejectsSourceTamperingBeforeDispatchAndWrite(t *testing.T) {
	for _, when := range []string{"before dispatch", "after model input before write"} {
		t.Run(when, func(t *testing.T) {
			st, cfg, session, input, _ := dispatchViewFixture(t)
			cfg.CharacterAgents.MaxConcurrency = 1
			observations, ids := dispatchViewObservations(input)
			proofRoot := filepath.Join(st.Dir(), "meta", "character_agents", "activation_sessions", session.GenerationID, "000001", "work", "000001", "proof")
			tamper := func() error {
				changed := input
				changed.Digest = "sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
				raw, err := json.Marshal(changed)
				if err != nil {
					return err
				}
				return os.WriteFile(filepath.Join(proofRoot, "inputs.json"), raw, 0600)
			}
			model := &dispatchViewHookModel{hook: func() error { return nil }}
			if when == "before dispatch" {
				selectionMust(t, tamper())
			} else {
				model.hook = tamper
			}
			if _, err := runCharacterProposalRoundWithModel(context.Background(), cfg, st, model, observations, ids, 1, &session); err == nil {
				t.Fatal("source tampering was accepted")
			}
			if model.hookErr != nil {
				t.Fatal(model.hookErr)
			}
			if when == "before dispatch" && model.calls.Load() != 0 {
				t.Fatal("unverified input reached a model")
			}
			if when != "before dispatch" && model.calls.Load() == 0 {
				t.Fatal("write-time test never crossed the dispatch boundary")
			}
			for _, id := range ids {
				path := filepath.Join(proofRoot, "proposals", "round-01", id+".json")
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatal(fmt.Sprintf("tampered source persisted a proposal: %s (%v)", id, err))
				}
			}
		})
	}
}
