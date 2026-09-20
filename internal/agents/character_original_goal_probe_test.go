package agents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/modelinput"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/voocel/agentcore"
)

const ownerOriginalGoalProbe = "OWNER_ORIGINAL_GOAL：查明家人的错误归责，取得有来源的证据"
const ownerCurrentGoalProbe = "眼前先整理核对问题"
const peerGoalProbe = "PEER_PRIVATE_GOAL：不能让他人获知的本人目标"
const futureGoalProbe = "AUTHOR_FUTURE_OUTLINE：未来才可能发生的交涉"

// Only the model boundary is fake. All observations, source bindings,
// submissions, arbitration and persisted cycle/readiness transitions are real.
type ownerGoalProbeModel struct {
	base         activationV3RuntimeModel
	views        map[int]json.RawMessage
	observations map[int]domain.CharacterObservationPacket
}

func (*ownerGoalProbeModel) SupportsTools() bool { return true }

func (m *ownerGoalProbeModel) Generate(ctx context.Context, messages []agentcore.Message, specs []agentcore.ToolSpec, opts ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	owner := false
	if len(specs) == 1 && specs[0].Name == "submit_character_decision" {
		for _, message := range messages {
			if message.Role != agentcore.RoleUser {
				continue
			}
			_, raw, ok := strings.Cut(message.TextContent(), "<character_observation_packet>\n")
			if !ok {
				continue
			}
			raw, _, ok = strings.Cut(raw, "\n</character_observation_packet>")
			if !ok {
				return nil, fmt.Errorf("probe input was truncated")
			}
			var view modelinput.ScopedReferenceModelView
			if err := json.Unmarshal([]byte(raw), &view); err != nil {
				return nil, err
			}
			var observation domain.CharacterObservationPacket
			if err := json.Unmarshal(view.Body, &observation); err != nil {
				return nil, err
			}
			if observation.Character != "甲" {
				continue
			}
			if observation.CycleContext == nil {
				return nil, fmt.Errorf("probe lost actual cycle identity")
			}
			owner = true
			var system string
			for _, item := range messages {
				if item.Role == agentcore.RoleSystem {
					system += item.TextContent()
				}
			}
			if strings.Contains(system, characterInitialSelfIntentPromptV1) != domain.HasCharacterInitialSelfIntentPolicyV1(observation.Sources) {
				return nil, fmt.Errorf("opening-intent help crossed its frozen producer boundary")
			}
			index := observation.CycleContext.Index
			m.views[index] = append(json.RawMessage(nil), raw...)
			m.observations[index] = observation
			if index == 2 {
				return nil, context.Canceled
			} // Capture O2, do not invent P2.
		}
	}
	response, err := m.base.Generate(ctx, messages, specs, opts...)
	if err != nil || !owner {
		return response, err
	}
	call := response.Message.ToolCalls()[0]
	var args map[string]any
	if err := json.Unmarshal(call.Args, &args); err != nil {
		return nil, err
	}
	args["current_goal"] = ownerCurrentGoalProbe
	// This is an explicit independent fresh decision, not a request to run
	// the original proposal without asking the owner again next cycle.
	delete(args, "work_continuations")
	call.Args, err = json.Marshal(args)
	if err != nil {
		return nil, err
	}
	response.Message.Content = []agentcore.ContentBlock{agentcore.ToolCallBlock(call)}
	return response, nil
}

func (m *ownerGoalProbeModel) GenerateStream(ctx context.Context, messages []agentcore.Message, specs []agentcore.ToolSpec, opts ...agentcore.CallOption) (<-chan agentcore.StreamEvent, error) {
	r, err := m.Generate(ctx, messages, specs, opts...)
	if err != nil {
		return nil, err
	}
	ch := make(chan agentcore.StreamEvent, 1)
	ch <- agentcore.StreamEvent{Type: agentcore.StreamEventDone, Message: r.Message, StopReason: r.Message.StopReason}
	close(ch)
	return ch, nil
}

func TestOriginalOwnerGoalRemainsInActualSecondCycleModelView(t *testing.T) {
	runOriginalOwnerGoalProbe(t, false, "", true)
}

// One-variable control: the existing private fact transport can preserve the
// same opening intent if it is explicitly represented as a self fact.
func TestOriginalOwnerGoalPrivateFactSurvivesActualSecondCycleModelView(t *testing.T) {
	runOriginalOwnerGoalProbe(t, true, characterActivationProtocolV3IncomingReadDigest(), true)
}

func TestOriginalOwnerGoalLegacyProducersRecoverWithoutNewHistory(t *testing.T) {
	for _, producer := range []string{characterActivationProtocolV3IncomingReadDigest(), characterActivationProtocolV3LegacyDigest()} {
		t.Run(producer, func(t *testing.T) { runOriginalOwnerGoalProbe(t, false, producer, false) })
	}
}

func runOriginalOwnerGoalProbe(t *testing.T, seedPrivateFact bool, producer string, wantOriginal bool) {
	t.Helper()
	st, cfg, boundary := activationV3RuntimeFixture(t)
	cfg.CharacterAgents.FrozenActivationProducer = producer
	boundary.FrozenActivationProducer = producer
	characters, err := st.Characters.Load()
	selectionMust(t, err)
	characters[0].InitialState.CurrentGoal = ownerOriginalGoalProbe
	if seedPrivateFact {
		characters[0].InitialState.KnownFacts = append(characters[0].InitialState.KnownFacts, ownerOriginalGoalProbe)
	}
	characters[1].InitialState.CurrentGoal = peerGoalProbe
	selectionMust(t, st.Characters.Save(characters))
	selectionMust(t, st.Outline.SaveOutline([]domain.OutlineEntry{{Chapter: 1, Title: "本人检查", CoreEvent: "甲、乙、丙各自决定本人检查；" + futureGoalProbe}}))
	model := &ownerGoalProbeModel{views: map[int]json.RawMessage{}, observations: map[int]domain.CharacterObservationPacket{}}
	models := &bootstrap.ModelSet{Default: bootstrap.NewSwappableModel("fixture", "owner-goal-probe", model)}
	const generation = "pg2_owner_original_goal_probe"
	_, err = runCharacterActivationChapter(t.Context(), cfg, st, models, generation, 1, boundary, domain.ProjectedPlanningContextV2{}, nil, 4)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("fixture did not reach its O2 model boundary: %v", err)
	}
	if len(model.views[1]) == 0 || len(model.views[2]) == 0 {
		t.Fatal("did not capture both actual model views")
	}
	if model.observations[1].CurrentGoal != ownerOriginalGoalProbe {
		t.Fatal("fixture lost original goal before first decision")
	}
	if model.observations[2].CurrentGoal != ownerCurrentGoalProbe {
		t.Fatal("Host overwrote the owner's autonomous revised current goal")
	}
	for cycle, raw := range model.views {
		if strings.Contains(string(raw), peerGoalProbe) || strings.Contains(string(raw), futureGoalProbe) {
			t.Fatalf("cycle %d exposes another owner's goal or future outline", cycle)
		}
	}
	// Reload the authentic prefix from disk, not the fake model's memory.
	reopened := store.NewStore(st.Dir())
	prefix, err := reopened.LoadVerifiedCharacterActivationPrefix(generation, 1)
	selectionMust(t, err)
	step, ok := prefix.Step(0)
	if !ok {
		t.Fatal("missing actual submitted/arbitrated first cycle")
	}
	ownerProposal := ""
	for _, p := range step.EffectiveProposals() {
		if p.Character == "甲" {
			ownerProposal = p.CurrentGoal
		}
	}
	if ownerProposal != ownerCurrentGoalProbe {
		t.Fatal("short goal was not the actual persisted owner proposal")
	}
	pending, err := reopened.LoadCharacterArbitrationV3(generation, 1)
	selectionMust(t, err)
	if pending == nil {
		t.Fatal("missing actual persisted O2 admission")
	}
	for _, observation := range pending.Input().Observations {
		if observation.Character != "甲" {
			continue
		}
		raw, err := json.Marshal(observation)
		selectionMust(t, err)
		if strings.Contains(string(raw), ownerOriginalGoalProbe) != wantOriginal {
			t.Fatal("persisted O2 opening intention crossed producer boundary")
		}
		if observation.CurrentGoal != model.observations[2].CurrentGoal {
			t.Fatal("model codec changed current goal from its persisted owner observation")
		}
	}
	if strings.Contains(string(model.views[2]), ownerOriginalGoalProbe) != wantOriginal {
		t.Fatalf("ORIGINAL_OWNER_GOAL_LOST: O1 contains original goal, persisted P1 legitimately changed current_goal to %q, but the real scoped O2 model view contains no original owner goal (peer/future remain private)", ownerCurrentGoalProbe)
	}
	// Reopen the Store and dispatch the saved O2 again. No O1 decision,
	// arbitration or readiness is regenerated, and no original intent is
	// retroactively inserted into an old producer's saved observation.
	resumed := &ownerGoalProbeModel{views: map[int]json.RawMessage{}, observations: map[int]domain.CharacterObservationPacket{}}
	models = &bootstrap.ModelSet{Default: bootstrap.NewSwappableModel("fixture", "owner-goal-probe", resumed)}
	_, err = runCharacterActivationChapter(t.Context(), cfg, reopened, models, generation, 1, boundary, domain.ProjectedPlanningContextV2{}, nil, 4)
	if !errors.Is(err, context.Canceled) || resumed.base.actorCalls != 0 || resumed.base.arbiterCalls != 0 {
		t.Fatalf("O2 recovery replayed old work or did not reach model boundary: %v", err)
	}
	if !reflect.DeepEqual(model.views[2], resumed.views[2]) {
		t.Fatal("saved O2 model view changed across Store recovery")
	}
}
