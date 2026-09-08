package agents

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/voocel/agentcore"
)

type groundingProbeModel struct {
	args     string
	calls    int
	messages []agentcore.Message
	specs    []agentcore.ToolSpec
}

func (m *groundingProbeModel) Generate(_ context.Context, messages []agentcore.Message, specs []agentcore.ToolSpec, _ ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	m.calls++
	m.messages = messages
	m.specs = specs
	return &agentcore.LLMResponse{Message: agentcore.Message{Role: agentcore.RoleAssistant, Content: []agentcore.ContentBlock{agentcore.ToolCallBlock(agentcore.ToolCall{ID: "verdict", Name: "submit_plan_grounding_verdict", Args: json.RawMessage(m.args)})}}}, nil
}
func (*groundingProbeModel) GenerateStream(context.Context, []agentcore.Message, []agentcore.ToolSpec, ...agentcore.CallOption) (<-chan agentcore.StreamEvent, error) {
	panic("grounding must use one non-streaming classification")
}
func (*groundingProbeModel) SupportsTools() bool { return true }

func TestPlanGroundingModelHasOneReadOnlyCapabilityAndExactSections(t *testing.T) {
	input := domain.PlanGroundingInput{Policy: domain.PlanGroundingPolicyV1, ReviewProtocol: "sha256:" + strings.Repeat("1", 64), Plan: domain.ChapterPlan{Chapter: 1, Goal: strings.Repeat("长", 20000) + "完整计划尾"}, POVObservation: domain.CharacterObservationPacket{CurrentGoal: strings.Repeat("知", 20000) + "完整观察尾"}}
	m := &groundingProbeModel{args: `{"pass":true,"findings":[]}`}
	if _, err := runPlanGroundingReview(context.Background(), m, agentcore.ThinkingHigh, input); err != nil {
		t.Fatal(err)
	}
	if m.calls != 1 || len(m.specs) != 1 || m.specs[0].Name != "submit_plan_grounding_verdict" {
		t.Fatal("unexpected capabilities/calls")
	}
	if len(m.messages) != 5 || !strings.Contains(m.messages[2].TextContent(), "完整观察尾") || !strings.Contains(m.messages[4].TextContent(), "完整计划尾") {
		t.Fatal("exact source was omitted or truncated")
	}
	input.Plan.Goal = strings.Repeat("大", 44001)
	if _, err := runPlanGroundingReview(context.Background(), m, agentcore.ThinkingHigh, input); err == nil {
		t.Fatal("oversized section silently truncated")
	}
	if m.calls != 1 {
		t.Fatal("over-budget evidence reached provider")
	}
}

func TestPlanGroundingRejectsMalformedAndFabricatedVerdicts(t *testing.T) {
	input := domain.PlanGroundingInput{Policy: domain.PlanGroundingPolicyV1, Plan: domain.ChapterPlan{Goal: "未读材料"}}
	for _, args := range []string{`{}`, `{"pass":true}`, `{"pass":true,"findings":null}`, `{"findings":[]}`, `{"pass":false,"findings":[]}`, `{"pass":true,"findings":[],"thoughts":"private reasoning"}`, `{"pass":true,"findings":[]} {}`, `{"pass":false,"findings":[{"kind":"knowledge","plan_path":"/plan/goal","plan_quote":"未读材料","source_path":"/arbitration/resolutions/9/state_after","source_quote":"编造来源","explanation":"假矛盾"}]}`} {
		m := &groundingProbeModel{args: args}
		if _, err := runPlanGroundingReview(context.Background(), m, agentcore.ThinkingHigh, input); err == nil {
			t.Fatalf("accepted malformed verdict: %s", args)
		}
	}
}
