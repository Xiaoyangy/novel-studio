package agents

import (
	"context"
	"encoding/json"
	"errors"
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

// Seven authenticated active owners exceed the three workers; the feeder must
// leave four unstarted jobs when the third worker refuses a deadline boundary.
func deliveryDeadlinePoolFixture(t *testing.T) (*store.Store, bootstrap.Config, domain.CharacterActivationSession, domain.CharacterActivationInputSet) {
	t.Helper()
	st, cfg, boundary := activationV3RuntimeFixture(t)
	characters, err := st.Characters.Load()
	selectionMust(t, err)
	for _, name := range []string{"丁", "戊", "己", "庚"} {
		characters = append(characters, domain.Character{Name: name, Role: "配角", Tier: "important", InitialState: &domain.CharacterInitialState{
			Location: "船上", CurrentGoal: "完成本人检查", Pressure: "时间有限", KnownFacts: []string{name + "知道自己的检查需求"},
		}})
	}
	selectionMust(t, st.Characters.Save(characters))
	selectionMust(t, st.Outline.SaveOutline([]domain.OutlineEntry{{Chapter: 1, Title: "七人分别检查", CoreEvent: "甲、乙、丙、丁、戊、己、庚各自决定本人检查"}}))
	const generation = "pg2_delivery_deadline_pool"
	selectionMust(t, st.EnsureCharacterAgentCanon(0))
	opening, err := buildWorldStimulusDraft(st, generation, 1, boundary, domain.ProjectedPlanningContextV2{}, nil, "2026-09-13T01:00:00Z", domain.CharacterAgentDecisionProtocolV2Version)
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
	_, err = st.PrepareCharacterArbitrationV3(session, nil, characterActivationProtocolForPolicy(characterActivationPolicyForStimulus(input.Stimulus)))
	selectionMust(t, err)
	cfg.CharacterAgents.MaxConcurrency = 3
	if got := len(activeCharacterAgentIDs(input.Activation)); got != 7 {
		t.Fatalf("fixture requires seven real admitted owners, got %d", got)
	}
	return st, cfg, session, input
}

type deliveryDeadlinePoolModel struct {
	activationV3RuntimeModel
	entered    chan string
	canceled   chan string
	release    <-chan struct{}
	thirdError error
	calls      atomic.Int32
}

func (m *deliveryDeadlinePoolModel) Generate(ctx context.Context, messages []agentcore.Message, specs []agentcore.ToolSpec, opts ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	call := m.calls.Add(1)
	group, ok := ctx.Value(characterAccountingGroupKey{}).(domain.CharacterAgentUsage)
	if !ok || group.AgentID == "" {
		return nil, fmt.Errorf("fixture call lacks the actual owner accounting group")
	}
	m.entered <- group.AgentID
	if call == 3 && m.thirdError != nil {
		return nil, m.thirdError
	}
	select {
	case <-ctx.Done():
		m.canceled <- group.AgentID
		return nil, ctx.Err()
	case <-m.release:
	}
	if err := ctx.Err(); err != nil {
		m.canceled <- group.AgentID
		return nil, err
	}
	response, err := m.activationV3RuntimeModel.Generate(ctx, messages, specs, opts...)
	if err == nil {
		response.Message.Usage = &agentcore.Usage{Input: 100, Output: 20, Cost: &agentcore.Cost{Total: .01}}
	}
	return response, err
}

func (m *deliveryDeadlinePoolModel) GenerateStream(ctx context.Context, messages []agentcore.Message, specs []agentcore.ToolSpec, opts ...agentcore.CallOption) (<-chan agentcore.StreamEvent, error) {
	response, err := m.Generate(ctx, messages, specs, opts...)
	if err != nil {
		return nil, err
	}
	events := make(chan agentcore.StreamEvent, 1)
	events <- agentcore.StreamEvent{Type: agentcore.StreamEventDone, Message: response.Message, StopReason: response.Message.StopReason}
	close(events)
	return events, nil
}

func deliveryDeadlineReceive[T any](t *testing.T, ch <-chan T, description string) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s; worker or feeder may be blocked", description)
		var zero T
		return zero
	}
}

func TestCharacterDeliveryDeadlinePoolPreservesInflightProposalsAndUsage(t *testing.T) {
	for _, boundary := range []string{"BeforeAgent", "StartCall"} {
		t.Run(boundary, func(t *testing.T) {
			st, cfg, session, input := deliveryDeadlinePoolFixture(t)
			observations, ids := dispatchViewObservations(input)
			release, allowThird := make(chan struct{}), make(chan struct{})
			var releaseOnce, allowOnce sync.Once
			t.Cleanup(func() { releaseOnce.Do(func() { close(release) }); allowOnce.Do(func() { close(allowThird) }) })
			model := &deliveryDeadlinePoolModel{entered: make(chan string, 7), canceled: make(chan string, 7), release: release}
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			thirdWaiting, denied := make(chan struct{}), make(chan struct{})
			var before, starts, after, skips, fakeSeconds atomic.Int32
			var deniedOnce sync.Once
			deadline := func() error {
				if fakeSeconds.Load() < 1200 {
					return nil
				}
				deniedOnce.Do(func() { close(denied) })
				return fmt.Errorf("injected original chapter wall clock: %w", store.ErrChapterDeliveryDeadline)
			}
			var mu sync.Mutex
			recorded := map[string]agentcore.Message{}
			imported := map[string]domain.CharacterAgentUsage{}
			hooks := ProjectedPlanningAccounting{
				BeforeAgent: func() error {
					if before.Add(1) == 3 {
						close(thirdWaiting)
						<-allowThird
					}
					if boundary == "BeforeAgent" {
						return deadline()
					}
					return nil
				},
				StartCall: func(string, string) error { starts.Add(1); return deadline() },
				SkipCall:  func(string) error { skips.Add(1); return nil },
				AfterAgent: func() error {
					after.Add(1)
					return deadline()
				},
				RecordUsage: func(owner string, raw agentcore.AgentMessage) {
					message, ok := raw.(agentcore.Message)
					if !ok || message.Usage == nil {
						return
					}
					mu.Lock()
					recorded[owner] = message
					mu.Unlock()
				},
				ImportCharacterUsage: func(usage domain.CharacterAgentUsage) error {
					mu.Lock()
					imported[usage.AgentID] = usage
					mu.Unlock()
					return nil
				},
			}
			ctx = context.WithValue(ctx, projectedPlanningAccountingKey{}, hooks)
			finished := make(chan error, 1)
			go func() {
				_, err := runCharacterProposalRoundWithModel(ctx, cfg, st, model, observations, ids, 1, &session)
				finished <- err
			}()
			inflight := []string{deliveryDeadlineReceive(t, model.entered, "first provider admission"), deliveryDeadlineReceive(t, model.entered, "second provider admission")}
			deliveryDeadlineReceive(t, thirdWaiting, "third worker boundary")
			fakeSeconds.Store(1200)
			allowOnce.Do(func() { close(allowThird) })
			deliveryDeadlineReceive(t, denied, "original deadline rejection")
			// The providers remain blocked while the feeder observes the stop.
			// This bounded absence check never advances the injected wall clock.
			select {
			case owner := <-model.canceled:
				t.Fatalf("deadline canceled already-started provider %s", owner)
			case err := <-finished:
				t.Fatalf("pool returned before two in-flight calls settled: %v", err)
			case <-time.After(30 * time.Millisecond):
			}
			releaseOnce.Do(func() { close(release) })
			if err := deliveryDeadlineReceive(t, finished, "deadline pool drain including queued feeder"); !errors.Is(err, store.ErrChapterDeliveryDeadline) {
				t.Fatalf("pool lost typed deadline: %v", err)
			}
			if ctx.Err() != nil || model.calls.Load() != 2 || len(model.canceled) != 0 || before.Load() != 3 {
				t.Fatalf("deadline canceled peers or admitted queued work: ctx=%v calls=%d canceled=%d before=%d", ctx.Err(), model.calls.Load(), len(model.canceled), before.Load())
			}
			wantStarts, wantAfter, wantSkips := int32(2), int32(2), int32(0)
			if boundary == "StartCall" {
				wantStarts, wantAfter, wantSkips = 3, 3, 1
			}
			if starts.Load() != wantStarts || after.Load() != wantAfter || skips.Load() != wantSkips {
				t.Fatalf("usage lifecycle did not settle: starts=%d after=%d skips=%d", starts.Load(), after.Load(), skips.Load())
			}
			proofs, err := store.NewStore(st.Dir()).CharacterAgents.ForActivationCycle(session)
			selectionMust(t, err)
			for _, owner := range ids {
				proposal, err := proofs.LoadProposal(session.GenerationID, session.Chapter, 1, owner)
				selectionMust(t, err)
				started := owner == inflight[0] || owner == inflight[1]
				if started != (proposal != nil) {
					t.Fatalf("owner %s real P persisted=%v; provider started=%v", owner, proposal != nil, started)
				}
				if proposal != nil && (proposal.Digest == "" || proposal.ObservationDigest != observations[owner].Digest) {
					t.Fatalf("owner %s lost exact source-bound P", owner)
				}
			}
			usage, err := store.NewStore(st.Dir()).CharacterAgents.LoadUsage()
			selectionMust(t, err)
			if len(usage) != 2 || len(imported) != 2 || len(recorded) != 2 {
				t.Fatalf("started calls lost durable/imported/provider usage: %d/%d/%d", len(usage), len(imported), len(recorded))
			}
			for _, item := range usage {
				if item.Status != "success" || item.Input != 100 || item.Output != 20 || item.Attempts != 1 || item.CostUSD != .01 || item.UsageID == "" || imported[item.AgentID].UsageID != item.UsageID {
					t.Fatalf("completed provider was not charged as its actual successful proposal: %+v", item)
				}
			}
		})
	}
}

func TestCharacterDeliveryDeadlinePoolStillCancelsPeersForRealErrors(t *testing.T) {
	for _, failure := range []string{"source", "provider"} {
		t.Run(failure, func(t *testing.T) {
			st, cfg, session, input := deliveryDeadlinePoolFixture(t)
			observations, ids := dispatchViewObservations(input)
			release, allowThird := make(chan struct{}), make(chan struct{})
			var releaseOnce, allowOnce sync.Once
			t.Cleanup(func() { releaseOnce.Do(func() { close(release) }); allowOnce.Do(func() { close(allowThird) }) })
			model := &deliveryDeadlinePoolModel{entered: make(chan string, 7), canceled: make(chan string, 7), release: release}
			providerErr := errors.New("injected non-retryable provider failure")
			if failure == "provider" {
				model.thirdError = providerErr
			}
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			var before atomic.Int32
			var sourceErr error
			hooks := ProjectedPlanningAccounting{RecordUsage: func(string, agentcore.AgentMessage) {}, BeforeAgent: func() error {
				if before.Add(1) != 3 {
					return nil
				}
				<-allowThird
				if failure == "source" {
					changed := input
					changed.Digest = "sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
					raw, err := json.Marshal(changed)
					if err == nil {
						err = os.WriteFile(filepath.Join(st.Dir(), "meta/character_agents/activation_sessions", session.GenerationID, "000001/work/000001/proof/inputs.json"), raw, 0o600)
					}
					if err != nil {
						return err
					}
					_, sourceErr = st.LoadCharacterArbitrationV3(session.GenerationID, session.Chapter)
					return sourceErr
				}
				return nil
			}}
			ctx = context.WithValue(ctx, projectedPlanningAccountingKey{}, hooks)
			finished := make(chan error, 1)
			go func() {
				_, err := runCharacterProposalRoundWithModel(ctx, cfg, st, model, observations, ids, 1, &session)
				finished <- err
			}()
			deliveryDeadlineReceive(t, model.entered, "first peer provider")
			deliveryDeadlineReceive(t, model.entered, "second peer provider")
			allowOnce.Do(func() { close(allowThird) })
			deliveryDeadlineReceive(t, model.canceled, "first peer canceled by real error")
			deliveryDeadlineReceive(t, model.canceled, "second peer canceled by real error")
			err := deliveryDeadlineReceive(t, finished, "failed pool and feeder drain")
			if err == nil || errors.Is(err, store.ErrChapterDeliveryDeadline) || (failure == "provider" && !errors.Is(err, providerErr)) || (failure == "source" && sourceErr == nil) {
				t.Fatalf("pool lost real %s failure: %v (source=%v)", failure, err, sourceErr)
			}
			if ctx.Err() != nil || before.Load() != 3 {
				t.Fatalf("pool canceled parent or admitted extra work: ctx=%v before=%d", ctx.Err(), before.Load())
			}
			usage, err := st.CharacterAgents.LoadUsage()
			selectionMust(t, err)
			canceled := 0
			for _, item := range usage {
				if item.Status == "canceled" {
					canceled++
				}
			}
			if canceled != 2 {
				t.Fatalf("canceled real in-flight calls lost durable usage: %+v", usage)
			}
		})
	}
}
