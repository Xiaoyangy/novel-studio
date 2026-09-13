package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/modelinput"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/tools"
	"github.com/voocel/agentcore"
)

type authorSourceSuccessorModel struct {
	newMode      bool
	ending       string
	hard         []string
	calls        int
	legacySchema []byte
}

func (*authorSourceSuccessorModel) SupportsTools() bool { return true }
func (m *authorSourceSuccessorModel) Generate(_ context.Context, messages []agentcore.Message, specs []agentcore.ToolSpec, _ ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	m.calls++
	if len(specs) != 1 || specs[0].Name != "submit_character_agent_successor_plan" {
		return nil, fmt.Errorf("unexpected successor tool")
	}
	rawSchema, err := json.Marshal(specs[0].Parameters)
	if err != nil {
		return nil, err
	}
	if string(rawSchema) != string(m.legacySchema) {
		return nil, fmt.Errorf("source mode changed the old submission shape")
	}
	seenPrompt, seenInput := false, false
	for _, message := range messages {
		if message.Role == agentcore.RoleTool && message.Metadata["is_error"] == true {
			return nil, fmt.Errorf("successor tool rejected fake submission")
		}
		if message.Role == agentcore.RoleSystem {
			want := characterAgentSuccessorArchitectPrompt
			if m.newMode {
				want = characterAgentAuthorSourceSuccessorPrompt
			}
			seenPrompt = strings.Contains(message.TextContent(), want)
			if m.newMode && strings.Contains(message.TextContent(), characterAgentSuccessorArchitectPrompt) {
				return nil, fmt.Errorf("old preserve-soft-ending instruction leaked")
			}
		}
		if message.Role != agentcore.RoleUser {
			continue
		}
		_, raw, found := strings.Cut(message.TextContent(), "<successor_input>\n")
		if !found {
			continue
		}
		d, marked, err := modelinput.ParseExactAgentPacketMessage(message)
		if err != nil || !marked || d.Kind != modelinput.KindCharacterSuccessor {
			return nil, fmt.Errorf("lost exact successor packet")
		}
		raw, _, found = strings.Cut(raw, "\n</successor_input>")
		if !found {
			return nil, fmt.Errorf("truncated successor packet")
		}
		var input struct {
			Immutable map[string]json.RawMessage `json:"immutable_constraints"`
			Soft      map[string]string          `json:"soft_guidance"`
		}
		if err := json.Unmarshal([]byte(raw), &input); err != nil {
			return nil, err
		}
		var hard []string
		if err := json.Unmarshal(input.Immutable["non_negotiables"], &hard); err != nil || !reflect.DeepEqual(hard, m.hard) {
			return nil, fmt.Errorf("actual user hard contracts changed")
		}
		if m.newMode {
			if _, exists := input.Immutable["ending_direction"]; exists {
				return nil, fmt.Errorf("soft ending remains immutable")
			}
			if input.Soft["ending_direction"] != m.ending {
				return nil, fmt.Errorf("soft hint lost")
			}
			var policy string
			if err := json.Unmarshal(input.Immutable["author_contract_policy"], &policy); err != nil || policy != domain.AuthorSourcesPolicyV1 {
				return nil, fmt.Errorf("soft meaning not bound")
			}
			if strings.Contains(specs[0].Description, "不改结局") {
				return nil, fmt.Errorf("tool still hardens model ending")
			}
		} else {
			var ending string
			if err := json.Unmarshal(input.Immutable["ending_direction"], &ending); err != nil || ending != m.ending || input.Soft != nil {
				return nil, fmt.Errorf("legacy payload changed")
			}
			if _, exists := input.Immutable["author_contract_policy"]; exists {
				return nil, fmt.Errorf("legacy mode got a marker")
			}
		}
		seenInput = true
	}
	if !seenPrompt || !seenInput {
		return nil, fmt.Errorf("missing original prompt/input")
	}
	args, _ := json.Marshal(map[string]any{"architect_summary": "保留用户要求，仅重排模型原软路线。", "revised_chapters": []domain.OutlineEntry{{Chapter: 1, Title: "原地核验", CoreEvent: "以现场原始材料核验，软方向不预定离泊", Hook: "等待实际回应", Scenes: []string{"停泊地点核验"}}}})
	return &agentcore.LLMResponse{Message: agentcore.Message{Role: agentcore.RoleAssistant, StopReason: agentcore.StopReasonToolUse, Content: []agentcore.ContentBlock{agentcore.ToolCallBlock(agentcore.ToolCall{ID: "source-successor-1", Name: specs[0].Name, Args: args})}, Usage: &agentcore.Usage{Input: 100, Output: 20}}}, nil
}
func (m *authorSourceSuccessorModel) GenerateStream(ctx context.Context, messages []agentcore.Message, specs []agentcore.ToolSpec, opts ...agentcore.CallOption) (<-chan agentcore.StreamEvent, error) {
	r, err := m.Generate(ctx, messages, specs, opts...)
	if err != nil {
		return nil, err
	}
	ch := make(chan agentcore.StreamEvent, 1)
	ch <- agentcore.StreamEvent{Type: agentcore.StreamEventDone, Message: r.Message, StopReason: r.Message.StopReason}
	close(ch)
	return ch, nil
}

func TestCharacterSuccessorAuthorSourceModeKeepsOnlyRealContractsImmutable(t *testing.T) {
	for _, newMode := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy", true: "author_sources"}[newMode], func(t *testing.T) {
			st := store.NewStore(t.TempDir())
			const ending = "模型原软方案：必须随船交付"
			const hard = "用户要求第三章解除错误归责，不强制离泊；保留实际角色自主选择。"
			compass := domain.StoryCompass{EndingDirection: ending, NonNegotiables: []string{hard}}
			if newMode {
				catalog, err := domain.FinalizeAuthorSourcesV1(domain.AuthorSourcesV1{Sources: []domain.AuthorSourceV1{{ID: "startup_prompt", Text: hard}}})
				selectionMust(t, err)
				selectionMust(t, st.SaveAuthorSources(catalog))
				compass.AuthorContracts = &domain.CompassAuthorContractsV1{Policy: domain.AuthorSourcesPolicyV1, SourcesDigest: catalog.Digest, Refs: []domain.AuthorSourceParagraphRefV1{{SourceID: "startup_prompt", Paragraph: 0}}}
			}
			selectionMust(t, st.Outline.SaveCompass(compass))
			selectionMust(t, st.Outline.SaveOutline([]domain.OutlineEntry{{Chapter: 1, Title: "旧章", CoreEvent: "旧软方向"}}))
			model := &authorSourceSuccessorModel{newMode: newMode, ending: ending, hard: []string{hard}}
			model.legacySchema, _ = json.Marshal(tools.NewSubmitCharacterAgentSuccessorPlanTool(nil, domain.CharacterAgentSuccessorPlan{}, nil).Schema())
			models := &bootstrap.ModelSet{Default: bootstrap.NewSwappableModel("test", "source-successor", model)}
			boundary := ProjectedArcBoundary{FirstChapter: 1, LastChapter: 1, BookLastChapter: 3}
			receipt := domain.WorldArbitrationReceipt{GenerationID: "pg2_source_successor", Chapter: 1, Digest: "sha256:" + strings.Repeat("a", 64), HardContractStatus: "infeasible", HardContractConflicts: []string{"当前路线无法兑现已验证用户要求"}}
			plan, err := runCharacterAgentSuccessorArchitectForCause(t.Context(), bootstrap.Config{}, st, models, boundary, characterAgentChapterInputs{}, nil, receipt, nil)
			selectionMust(t, err)
			if model.calls != 1 || plan == nil || !reflect.DeepEqual(plan.NonNegotiables, []string{hard}) || plan.EndingDirection != ending {
				t.Fatal("source successor lost real constraints or original soft provenance")
			}
			if (plan.AuthorContractPolicy == domain.AuthorSourcesPolicyV1) != newMode {
				t.Fatal("persisted soft/legacy semantics changed")
			}
			reopened := store.NewStore(st.Dir())
			got, err := runCharacterAgentSuccessorArchitectForCause(t.Context(), bootstrap.Config{}, reopened, models, boundary, characterAgentChapterInputs{}, nil, receipt, nil)
			selectionMust(t, err)
			if got.Digest != plan.Digest || got.AuthorContractPolicy != plan.AuthorContractPolicy || model.calls != 1 {
				t.Fatal("cached successor lost semantics or paid twice")
			}
			if !newMode {
				// A host migration must not reuse an old cached plan while
				// silently changing the meaning of its immutable ending field.
				catalog, err := domain.FinalizeAuthorSourcesV1(domain.AuthorSourcesV1{Sources: []domain.AuthorSourceV1{{ID: "startup_prompt", Text: hard}}})
				selectionMust(t, err)
				selectionMust(t, st.SaveAuthorSources(catalog))
				_, err = runCharacterAgentSuccessorArchitectForCause(t.Context(), bootstrap.Config{}, reopened, models, boundary, characterAgentChapterInputs{}, nil, receipt, nil)
				if err == nil || !strings.Contains(err.Error(), "semantics differ") || model.calls != 1 {
					t.Fatalf("legacy cached ending silently reinterpreted: %v", err)
				}
			}
		})
	}
}
