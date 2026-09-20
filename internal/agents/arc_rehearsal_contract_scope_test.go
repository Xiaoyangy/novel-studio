package agents

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/voocel/agentcore"
)

type rehearsalScopeCaptureModel struct {
	arcRehearsalFakeModel
	prompts []string
	schemas [][]byte
}

func (m *rehearsalScopeCaptureModel) Generate(ctx context.Context, messages []agentcore.Message, specs []agentcore.ToolSpec, opts ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	var prompt strings.Builder
	for _, message := range messages {
		if message.Role == agentcore.RoleSystem {
			prompt.WriteString(message.TextContent())
		}
	}
	m.prompts = append(m.prompts, prompt.String())
	raw, _ := json.Marshal(specs)
	m.schemas = append(m.schemas, raw)
	return m.arcRehearsalFakeModel.Generate(ctx, messages, specs, opts...)
}

func (m *rehearsalScopeCaptureModel) GenerateStream(ctx context.Context, messages []agentcore.Message, specs []agentcore.ToolSpec, opts ...agentcore.CallOption) (<-chan agentcore.StreamEvent, error) {
	response, err := m.Generate(ctx, messages, specs, opts...)
	if err != nil {
		return nil, err
	}
	events := make(chan agentcore.StreamEvent, 1)
	events <- agentcore.StreamEvent{Type: agentcore.StreamEventDone, Message: response.Message, StopReason: response.Message.StopReason}
	close(events)
	return events, nil
}

func TestArcRehearsalContractScopeReachesBothActualModelCalls(t *testing.T) {
	st, input := arcRehearsalTestInput(t)
	if input.ProtocolDigest == "sha256:44ae6deaede4754b6530f00560e4d7eb1d3e2c6ab072400320eb35413fc23ea2" {
		t.Fatal("new default input silently kept the old assessment protocol")
	}
	model := &rehearsalScopeCaptureModel{}
	models := &bootstrap.ModelSet{Default: bootstrap.NewSwappableModel("test", "fake", model)}
	_, err := RunArcRehearsal(t.Context(), bootstrap.Config{}, models, st, input, ProjectedPlanningAccounting{RecordUsage: func(string, agentcore.AgentMessage) {}})
	selectionMust(t, err)
	if len(model.prompts) != 2 {
		t.Fatalf("expected both actual model calls, got %d", len(model.prompts))
	}
	for i, prompt := range model.prompts {
		for _, required := range []string{"当前整弧的条件性可达性与当前是否违反", "不是全书终态验收", "不得仅因章零没有已接受章节", "不得改写或删除任何硬合同", "必需来源或执行能力确实缺失"} {
			if !strings.Contains(prompt, required) {
				t.Fatalf("CONTRACT_SCOPE_MISSING call=%d role=%s: %q", i, model.roles[i], required)
			}
		}
	}
}

func TestArcRehearsalContractScopePreservesPriorProtocolsAndDraftResume(t *testing.T) {
	previous, err := ReviewDeltaArcRehearsalProtocolDigest()
	selectionMust(t, err)
	if previous != "sha256:44ae6deaede4754b6530f00560e4d7eb1d3e2c6ab072400320eb35413fc23ea2" {
		t.Fatalf("previous delta prompt/schema changed: %s", previous)
	}
	legacy, err := LegacyArcRehearsalProtocolDigest()
	selectionMust(t, err)
	if legacy != "sha256:772e4669ca1b47e3931b333fdd4f8920c7d6a907f5bd25f6696f985492ffa3ee" {
		t.Fatalf("previous full-body prompt/schema changed: %s", legacy)
	}
	current, err := ArcRehearsalProtocolDigest()
	selectionMust(t, err)
	for _, protocol := range []string{legacy, previous, current} {
		t.Run(protocol, func(t *testing.T) {
			st, input := arcRehearsalTestInput(t)
			currentInputDigest := input.InputDigest
			input.ProtocolDigest = protocol
			input, err = domain.FinalizeArcRehearsalInput(input)
			selectionMust(t, err)
			if (protocol == current) != (input.InputDigest == currentInputDigest) {
				t.Fatal("input identity did not bind selected protocol")
			}
			hooks := ProjectedPlanningAccounting{RecordUsage: func(string, agentcore.AgentMessage) {}}
			first := &rehearsalScopeCaptureModel{arcRehearsalFakeModel: arcRehearsalFakeModel{failReview: true}}
			models := &bootstrap.ModelSet{Default: bootstrap.NewSwappableModel("test", "fake", first)}
			if _, err := RunArcRehearsal(t.Context(), bootstrap.Config{}, models, st, input, hooks); !errors.Is(err, context.Canceled) {
				t.Fatalf("expected saved-draft interruption, got %v", err)
			}
			draft, _, err := st.LoadArcRehearsalDraftForInput(input.InputDigest)
			selectionMust(t, err)
			if draft == nil || len(first.prompts) != 2 {
				t.Fatal("did not retain exact first-stage draft")
			}
			original, _ := json.Marshal(draft)
			second := &rehearsalScopeCaptureModel{}
			models = &bootstrap.ModelSet{Default: bootstrap.NewSwappableModel("test", "fake", second)}
			report, err := RunArcRehearsal(t.Context(), bootstrap.Config{}, models, store.NewStore(st.Dir()), input, hooks)
			selectionMust(t, err)
			if report == nil || report.DraftDigest != draft.DraftDigest || !reflect.DeepEqual(second.roles, []string{"world_arbiter"}) || len(second.prompts) != 1 {
				t.Fatal("historical resume reran Architect or changed its draft binding")
			}
			wantArchitect := arcRehearsalPrompt + arcRehearsalCapabilityPromptV1 + arcRehearsalSurfaceCapabilityPromptV1
			wantReview := wantArchitect
			if protocol == legacy {
				wantReview += arcRehearsalReviewPrompt
			} else {
				wantReview += arcRehearsalReviewDeltaPrompt
			}
			if protocol == current {
				wantArchitect += arcRehearsalContractScopePrompt
				wantReview += arcRehearsalContractScopePrompt
			}
			if first.prompts[0] != wantArchitect || first.prompts[1] != wantReview || second.prompts[0] != wantReview || !bytes.Equal(first.schemas[1], second.schemas[0]) {
				t.Fatal("fresh/resumed input selected the wrong prompt or schema")
			}
			after, _, err := st.LoadArcRehearsalDraftForInput(input.InputDigest)
			selectionMust(t, err)
			raw, _ := json.Marshal(after)
			if !bytes.Equal(original, raw) {
				t.Fatal("resume rewrote original draft")
			}
			_, err = RunArcRehearsal(t.Context(), bootstrap.Config{}, models, store.NewStore(st.Dir()), input, hooks)
			selectionMust(t, err)
			if len(second.prompts) != 1 {
				t.Fatal("saved report recovery called a model")
			}
		})
	}
}

func TestArcRehearsalContractScopeKeepsAllContractsAndActualBlockers(t *testing.T) {
	// This checks host semantics and delivered instructions, not a fake model's
	// ability to reason. No host code upgrades an unresolved model assessment.
	for _, mode := range []string{"future-conditional", "missing", "unclear", "unresolved", "infeasible_prediction"} {
		t.Run(mode, func(t *testing.T) {
			st, input := arcRehearsalTestInput(t)
			compass, err := st.Outline.LoadCompass()
			selectionMust(t, err)
			const globalContract = "全书最终须实际完成30万字正文，不得以规划冒充完成"
			compass.NonNegotiables = append(compass.NonNegotiables, globalContract)
			selectionMust(t, st.Outline.SaveCompass(*compass))
			input, err = BuildArcRehearsalInput(st, input)
			selectionMust(t, err)
			original, _ := json.Marshal(input)
			model := &rehearsalScopeCaptureModel{arcRehearsalFakeModel: arcRehearsalFakeModel{bodyFactory: func(input domain.ArcRehearsalInput) domain.ArcRehearsalBody {
				body := arcRehearsalTestBody(input, mode == "missing" || mode == "unclear")
				body.UnresolvedItems = []string{"章零没有已接受章", "未来许可尚未发生，实际执行前仍需取得", "后续弧及全书30万字尚待实际完成，本预演不证明完成"}
				if mode == "unclear" {
					body.MaterialChecks[0].Status = "unclear"
				}
				if mode == "unresolved" || mode == "infeasible_prediction" {
					body.ContractChecks[0].Assessment = mode
					body.ContractChecks[0].Conditions = []string{"本弧必需来源缺失，现有输入无可替代的条件路径"}
				}
				return body
			}}}
			models := &bootstrap.ModelSet{Default: bootstrap.NewSwappableModel("test", "fake", model)}
			report, err := RunArcRehearsal(t.Context(), bootstrap.Config{}, models, st, input, ProjectedPlanningAccounting{RecordUsage: func(string, agentcore.AgentMessage) {}})
			selectionMust(t, err)
			if report.ReadyForDetail != (mode == "future-conditional") {
				t.Fatalf("host changed readiness for %s", mode)
			}
			var got []string
			for _, check := range report.Body.ContractChecks {
				got = append(got, check.Contract)
			}
			if !reflect.DeepEqual(got, input.HardContracts) || !strings.Contains(strings.Join(got, "\n"), globalContract) {
				t.Fatal("new guidance dropped or rewrote an actual global contract")
			}
			after, _ := json.Marshal(input)
			if !bytes.Equal(original, after) {
				t.Fatal("assessment rewrote frozen input")
			}
		})
	}
}
