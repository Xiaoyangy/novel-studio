package agents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/voocel/agentcore"
)

// The pending boundary is produced by the real V3 actor/arbiter tools and
// appended through the verified Store API. No source badge or receipt is forged.
func readinessTransactionFixture(t *testing.T) (*store.Store, bootstrap.Config, domain.CharacterActivationSession, domain.CharacterActivationSession, domain.CharacterActivationCycle) {
	t.Helper()
	st, cfg, baseline, input, _ := dispatchViewFixture(t)
	model := &activationV3RuntimeModel{}
	models := &bootstrap.ModelSet{Default: bootstrap.NewSwappableModel("test", "readiness-transaction", model)}
	observations, _ := dispatchViewObservations(input)
	cycle, err := runCharacterActivationCycle(context.Background(), cfg, st, models, baseline, characterAgentChapterInputs{Stimulus: input.Stimulus, Activation: input.Activation, Observations: observations})
	selectionMust(t, err)
	pending, err := st.AppendVerifiedCharacterActivationCycle(baseline.Digest, cycle)
	selectionMust(t, err)
	if pending.Phase != "assessing" || len(pending.CycleDigests) != 1 || len(pending.ReadinessDigests) != 0 {
		t.Fatal("fixture did not produce an authentic pending cycle")
	}
	return st, cfg, baseline, *pending, cycle
}

func readinessTransactionCopy(t *testing.T, st *store.Store) *store.Store {
	t.Helper()
	dir := t.TempDir()
	selectionMust(t, os.CopyFS(dir, os.DirFS(st.Dir())))
	return store.NewStore(dir)
}

type readinessTransactionModel struct {
	delegate        activationV2ViewModel
	calls           int
	noSubmission    bool
	inputs, schemas [][]byte
}

func (*readinessTransactionModel) SupportsTools() bool { return true }
func (m *readinessTransactionModel) Generate(ctx context.Context, messages []agentcore.Message, specs []agentcore.ToolSpec, opts ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	m.calls++
	if len(specs) != 1 || specs[0].Name != "submit_chapter_readiness" {
		return nil, fmt.Errorf("unexpected readiness tool")
	}
	input, err := json.Marshal(messages)
	if err != nil {
		return nil, err
	}
	// Only the local message-send wall clock differs between the two calls.
	// Preserve every content/source timestamp and all exact-packet metadata.
	var envelopes []map[string]json.RawMessage
	if err := json.Unmarshal(input, &envelopes); err != nil {
		return nil, err
	}
	for _, envelope := range envelopes {
		delete(envelope, "timestamp")
	}
	input, err = json.Marshal(envelopes)
	if err != nil {
		return nil, err
	}
	schema, err := json.Marshal(specs)
	if err != nil {
		return nil, err
	}
	m.inputs = append(m.inputs, input)
	m.schemas = append(m.schemas, schema)
	if m.noSubmission {
		return &agentcore.LLMResponse{Message: agentcore.Message{Role: agentcore.RoleAssistant, Content: []agentcore.ContentBlock{agentcore.TextBlock("没有提交结构化结果")}, Usage: &agentcore.Usage{Input: 100, Output: 20}}}, nil
	}
	// The delegate reads only the actual grouped model view and returns a
	// schema-valid verdict with its exact local evidence/contract references.
	m.delegate.readiness.Store(1)
	return m.delegate.Generate(ctx, messages, specs, opts...)
}
func (m *readinessTransactionModel) GenerateStream(ctx context.Context, messages []agentcore.Message, specs []agentcore.ToolSpec, opts ...agentcore.CallOption) (<-chan agentcore.StreamEvent, error) {
	r, err := m.Generate(ctx, messages, specs, opts...)
	if err != nil {
		return nil, err
	}
	ch := make(chan agentcore.StreamEvent, 1)
	ch <- agentcore.StreamEvent{Type: agentcore.StreamEventDone, Message: r.Message, StopReason: r.Message.StopReason}
	close(ch)
	return ch, nil
}
func readinessTransactionModels(m *readinessTransactionModel) *bootstrap.ModelSet {
	return &bootstrap.ModelSet{Default: bootstrap.NewSwappableModel("test", "readiness-transaction", m)}
}

func readinessTransactionDriver(cfg bootstrap.Config, st *store.Store, models *bootstrap.ModelSet) ChapterActivationDriver {
	return ChapterActivationDriver{
		VerifyExecutionSources: true,
		ExecuteCycle: func(context.Context, domain.CharacterActivationSession) (domain.CharacterActivationCycle, error) {
			return domain.CharacterActivationCycle{}, fmt.Errorf("completed pending cycle must not be reexecuted")
		},
		AssessPendingCycle: func(ctx context.Context, pending domain.CharacterActivationSession) (domain.CharacterChapterReadiness, *domain.CharacterActivationSession, error) {
			return runVerifiedCharacterChapterReadiness(ctx, cfg, st, models, pending)
		},
	}
}

func TestReadinessTransactionMatchesLegacyInputReceiptAndSingleUsage(t *testing.T) {
	st, cfg, baseline, pending, cycle := readinessTransactionFixture(t)
	legacy := readinessTransactionCopy(t, st)
	oldModel, newModel := &readinessTransactionModel{}, &readinessTransactionModel{}
	oldReceipt, err := runCharacterChapterReadiness(context.Background(), cfg, legacy, readinessTransactionModels(oldModel), pending, cycle)
	selectionMust(t, err)
	oldApplied, err := legacy.ApplyVerifiedCharacterChapterReadiness(pending.Digest, oldReceipt)
	selectionMust(t, err)
	got, err := RunChapterActivationLoop(context.Background(), st, baseline, readinessTransactionDriver(cfg, st, readinessTransactionModels(newModel)))
	selectionMust(t, err)
	if got.Phase != "ready" || got.Digest != oldApplied.Digest || oldModel.calls != 1 || newModel.calls != 1 {
		t.Fatalf("transaction changed transition or paid twice: phase=%s calls=%d/%d", got.Phase, oldModel.calls, newModel.calls)
	}
	if !reflect.DeepEqual(oldModel.inputs, newModel.inputs) || !reflect.DeepEqual(oldModel.schemas, newModel.schemas) {
		for i := 0; i < len(oldModel.inputs[0]) && i < len(newModel.inputs[0]); i++ {
			if oldModel.inputs[0][i] != newModel.inputs[0][i] {
				t.Fatalf("model messages differ at byte %d: old=%q new=%q", i, oldModel.inputs[0][max(0, i-60):min(len(oldModel.inputs[0]), i+100)], newModel.inputs[0][max(0, i-60):min(len(newModel.inputs[0]), i+100)])
			}
		}
		t.Fatal("transaction changed model message length or terminal schema")
	}
	audit, err := st.LoadCharacterReadinessReviewAudit(pending.GenerationID, pending.Chapter, 1)
	selectionMust(t, err)
	if audit == nil || !reflect.DeepEqual(audit.Receipt, oldReceipt) {
		t.Fatal("transaction changed a formerly valid receipt")
	}
	usage, err := st.CharacterAgents.LoadUsage()
	selectionMust(t, err)
	n := 0
	for _, u := range usage {
		if u.Role == "chapter_readiness" {
			n++
			if u.Status != "success" || u.Cycle != 1 || u.Input != 100 || u.Output != 20 {
				t.Fatalf("wrong readiness accounting: %+v", u)
			}
		}
	}
	if n != 1 {
		t.Fatalf("want one readiness usage, got %d", n)
	}
	recovered, err := RunChapterActivationLoop(context.Background(), store.NewStore(st.Dir()), baseline, readinessTransactionDriver(cfg, store.NewStore(st.Dir()), readinessTransactionModels(newModel)))
	selectionMust(t, err)
	if recovered.Digest != got.Digest || newModel.calls != 1 {
		t.Fatal("ready recovery repeated a paid assessment")
	}
}

func TestReadinessTransactionAuditOnlyRecoveryBypassesExpiredDispatch(t *testing.T) {
	st, cfg, baseline, pending, cycle := readinessTransactionFixture(t)
	noAudit := readinessTransactionCopy(t, st)
	paid := &readinessTransactionModel{}
	receipt, err := runCharacterChapterReadiness(context.Background(), cfg, st, readinessTransactionModels(paid), pending, cycle)
	selectionMust(t, err)
	// This is the real historical crash boundary: audit exists, while no
	// readiness receipt/cursor transition has yet been published.
	prefix, err := st.LoadVerifiedCharacterActivationPrefix(pending.GenerationID, pending.Chapter)
	selectionMust(t, err)
	if prefix.Session().Digest != pending.Digest {
		t.Fatal("old audit save unexpectedly advanced cursor")
	}
	denied := errors.New("expired delivery budget")
	guardCalls := 0
	guard := func(context.Context, string) error { guardCalls++; return denied }
	unused := &readinessTransactionModel{}
	driver := readinessTransactionDriver(cfg, st, readinessTransactionModels(unused))
	driver.BeforeDispatch = guard
	driver.AssessPendingCycle = func(ctx context.Context, current domain.CharacterActivationSession) (domain.CharacterChapterReadiness, *domain.CharacterActivationSession, error) {
		return runVerifiedCharacterChapterReadiness(ctx, cfg, st, readinessTransactionModels(unused), current, guard)
	}
	got, err := RunChapterActivationLoop(context.Background(), st, baseline, driver)
	selectionMust(t, err)
	if got.Phase != "ready" || got.ReadinessDigests[0] != receipt.Digest || unused.calls != 0 || guardCalls != 0 {
		t.Fatal("mechanical cached-audit recovery was blocked or started a model")
	}
	_, applied, err := runVerifiedCharacterChapterReadiness(context.Background(), cfg, noAudit, readinessTransactionModels(unused), pending, guard)
	if !errors.Is(err, denied) || applied != nil || unused.calls != 0 || guardCalls != 1 {
		t.Fatalf("expired uncached assessment dispatched: %v", err)
	}
	unchanged, err := noAudit.LoadVerifiedCharacterActivationPrefix(pending.GenerationID, pending.Chapter)
	selectionMust(t, err)
	if unchanged.Session().Digest != pending.Digest {
		t.Fatal("denied dispatch invented readiness")
	}
}

func TestReadinessTransactionNoSubmissionAndBadReturnedCursorFailClosed(t *testing.T) {
	st, cfg, baseline, pending, cycle := readinessTransactionFixture(t)
	t.Run("no_submission", func(t *testing.T) {
		copy := readinessTransactionCopy(t, st)
		model := &readinessTransactionModel{noSubmission: true}
		_, applied, err := runVerifiedCharacterChapterReadiness(context.Background(), cfg, copy, readinessTransactionModels(model), pending)
		if err == nil || applied != nil || model.calls < 1 {
			t.Fatal("unsubmitted response became readiness")
		}
		prefix, err := copy.LoadVerifiedCharacterActivationPrefix(pending.GenerationID, pending.Chapter)
		selectionMust(t, err)
		if prefix.Session().Digest != pending.Digest {
			t.Fatal("unsubmitted response changed cursor")
		}
		audit, err := copy.LoadCharacterReadinessReviewAudit(pending.GenerationID, pending.Chapter, 1)
		selectionMust(t, err)
		if audit != nil {
			t.Fatal("unsubmitted response created a paid verdict")
		}
	})
	t.Run("wrong_cursor", func(t *testing.T) {
		copy := readinessTransactionCopy(t, st)
		model := &readinessTransactionModel{}
		receipt, err := runCharacterChapterReadiness(context.Background(), cfg, copy, readinessTransactionModels(model), pending, cycle)
		selectionMust(t, err)
		driver := readinessTransactionDriver(cfg, copy, readinessTransactionModels(model))
		driver.AssessPendingCycle = func(context.Context, domain.CharacterActivationSession) (domain.CharacterChapterReadiness, *domain.CharacterActivationSession, error) {
			// A valid, but still pending, cursor is not the assessed transition.
			wrong := pending
			return receipt, &wrong, nil
		}
		_, err = RunChapterActivationLoop(context.Background(), copy, baseline, driver)
		if err == nil || !strings.Contains(err.Error(), "different session transition") {
			t.Fatalf("wrong cursor accepted: %v", err)
		}
		prefix, err := copy.LoadVerifiedCharacterActivationPrefix(pending.GenerationID, pending.Chapter)
		selectionMust(t, err)
		if prefix.Session().Digest != pending.Digest {
			t.Fatal("bad driver advanced the real cursor")
		}
	})
}

func TestReadinessTransactionCanceledAndLateToolSubmissionAreRecoverable(t *testing.T) {
	st, cfg, _, pending, _ := readinessTransactionFixture(t)
	model := &readinessTransactionModel{}
	models := readinessTransactionModels(model)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, applied, err := runVerifiedCharacterChapterReadiness(ctx, cfg, st, models, pending)
	if !errors.Is(err, context.Canceled) || applied != nil || model.calls != 0 {
		t.Fatal("canceled dispatch started a model")
	}
	snapshot, err := models.SnapshotForRole("writer")
	selectionMust(t, err)
	thinking, _ := ResolveThinkingForModel(snapshot.Model, roleThinking(cfg, "writer"))
	protocol, err := characterReadinessReviewProtocol(snapshot, thinking, true)
	selectionMust(t, err)
	input, _, err := st.PrepareVerifiedCharacterReadinessReview(pending, protocol)
	selectionMust(t, err)
	tool, err := newSubmitGroupedCharacterReadinessTool(st, input)
	selectionMust(t, err)
	view := tool.codec.ModelView()
	var ref string
	selectionMust(t, json.Unmarshal(view.Trace["final_state_ref"], &ref))
	verdict := domain.CharacterReadinessGroupedVerdictV1{Decision: "ready_for_plan", Reason: "实际已结算检查可进入规划", EvidenceRefs: []string{ref}}
	for _, requirement := range view.Requirements {
		status := "pending"
		if requirement.DueNow {
			status = "satisfied"
		}
		verdict.ContractGroups = append(verdict.ContractGroups, domain.CharacterReadinessContractGroupV1{Status: status, EvidenceRefs: []string{ref}, ContractAliases: []string{requirement.ContractAlias}})
	}
	raw, err := json.Marshal(verdict)
	selectionMust(t, err)
	commit := &characterReadinessCommit{store: st, expected: pending.Digest}
	tool.persist = commit.Save
	// Simulate the already-returned paid tool result arriving after the outer
	// cancellation. It is committed, not dropped, and does not start a model.
	_, err = tool.Execute(ctx, raw)
	selectionMust(t, err)
	audit, done := commit.Result()
	if audit == nil || done == nil || done.Phase != "ready" || model.calls != 0 {
		t.Fatal("late submitted evidence was discarded")
	}
	reloaded, err := store.NewStore(st.Dir()).LoadVerifiedCharacterActivationPrefix(pending.GenerationID, pending.Chapter)
	selectionMust(t, err)
	if reloaded.Session().Digest != done.Digest || reloaded.Session().ReadinessDigests[0] != audit.Receipt.Digest {
		t.Fatal("late paid result was not durable")
	}
}
