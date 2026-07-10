package sampler

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/voocel/agentcore"
)

type fakeModel struct {
	responses []*agentcore.LLMResponse
	calls     int
	thinking  []agentcore.ThinkingLevel
}

func (m *fakeModel) Generate(_ context.Context, _ []agentcore.Message, _ []agentcore.ToolSpec, opts ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	m.thinking = append(m.thinking, agentcore.ResolveCallConfig(opts).ThinkingLevel)
	resp := m.responses[m.calls]
	m.calls++
	return resp, nil
}

func TestSamplerPreservesUltraThinking(t *testing.T) {
	base := &fakeModel{responses: []*agentcore.LLMResponse{
		draftResponse("a", 1, "第一份正文。"),
		draftResponse("b", 1, "第二份正文。"),
		draftResponse("c", 1, "第三份正文。"),
	}}
	model := New(base)
	_, err := model.Generate(context.Background(), []agentcore.Message{
		{Role: agentcore.RoleUser, Content: []agentcore.ContentBlock{agentcore.TextBlock("next_step: 请调用 draft_chapter 写章节草稿正文")}},
	}, nil, agentcore.WithThinking(agentcore.ThinkingLevel("ultra")))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(base.thinking) != 3 {
		t.Fatalf("thinking calls = %d, want 3", len(base.thinking))
	}
	for i, level := range base.thinking {
		if level != "ultra" {
			t.Fatalf("thinking[%d] = %q, want ultra", i, level)
		}
	}
}

func (m *fakeModel) GenerateStream(context.Context, []agentcore.Message, []agentcore.ToolSpec, ...agentcore.CallOption) (<-chan agentcore.StreamEvent, error) {
	panic("not used")
}

func (m *fakeModel) SupportsTools() bool { return true }

func TestSamplerSelectsRoughestDraft(t *testing.T) {
	base := &fakeModel{responses: []*agentcore.LLMResponse{
		draftResponse("a", 1, "她说：“我要撕开真相。”\n\n月光像刀，雾像网。她没有犹豫。"),
		draftResponse("b", 1, "卡莱尔问：“你怕吗？”\n\n她抬起手，又放下。“怕。你先开门。”"),
		draftResponse("c", 1, "风像灰线一样落下。\n\n她说：“我从未这样清醒。”"),
	}}
	model := New(base)
	resp, err := model.Generate(context.Background(), []agentcore.Message{
		{Role: agentcore.RoleUser, Content: []agentcore.ContentBlock{agentcore.TextBlock("next_step: 请调用 draft_chapter 写章节草稿正文")}},
	}, nil)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if base.calls != 3 {
		t.Fatalf("calls = %d, want 3", base.calls)
	}
	call, _, ok := findDraftCall(resp.Message)
	if !ok {
		t.Fatalf("expected draft_chapter call")
	}
	var args map[string]any
	if err := json.Unmarshal(call.Args, &args); err != nil {
		t.Fatalf("unmarshal args: %v", err)
	}
	sampling, ok := args["sampling"].(map[string]any)
	if !ok {
		t.Fatalf("missing sampling metadata: %+v", args)
	}
	if int(sampling["selected_index"].(float64)) != 2 {
		t.Fatalf("selected_index = %v, want 2", sampling["selected_index"])
	}
}

func draftResponse(id string, chapter int, content string) *agentcore.LLMResponse {
	args, _ := json.Marshal(map[string]any{
		"chapter": chapter,
		"mode":    "write",
		"content": content,
	})
	return &agentcore.LLMResponse{Message: agentcore.Message{
		Role: agentcore.RoleAssistant,
		Content: []agentcore.ContentBlock{agentcore.ToolCallBlock(agentcore.ToolCall{
			ID:   id,
			Name: "draft_chapter",
			Args: args,
		})},
	}}
}

type scriptedJudge struct {
	answers []string
	calls   int
}

func (s *scriptedJudge) Generate(_ context.Context, _ []agentcore.Message, _ []agentcore.ToolSpec, _ ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	a := s.answers[s.calls%len(s.answers)]
	s.calls++
	msg := agentcore.Message{Role: agentcore.RoleAssistant, Content: []agentcore.ContentBlock{{Type: agentcore.ContentText, Text: a}}}
	return &agentcore.LLMResponse{Message: msg}, nil
}
func (s *scriptedJudge) GenerateStream(context.Context, []agentcore.Message, []agentcore.ToolSpec, ...agentcore.CallOption) (<-chan agentcore.StreamEvent, error) {
	return nil, nil
}
func (s *scriptedJudge) SupportsTools() bool { return false }

func TestPairwiseJudgeSwapAndFallback(t *testing.T) {
	// 两轮一致（换位后互补）→ 裁定 b 胜：第一轮选 B(=b)，第二轮换位选 A(=b)。
	j := &scriptedJudge{answers: []string{"B", "A"}}
	if w, ok := pairwiseJudge(context.Background(), j, "aaa", "bbb"); !ok || w != 1 {
		t.Fatalf("换位一致应裁定 b 胜: w=%d ok=%v", w, ok)
	}
	// 位置偏好（两轮都选 A 槽）→ 不一致 → 回退确定性分。
	j2 := &scriptedJudge{answers: []string{"A", "A"}}
	if _, ok := pairwiseJudge(context.Background(), j2, "aaa", "bbb"); ok {
		t.Fatal("位置偏好应判不一致并回退")
	}
	// 无法解析 → 回退。
	j3 := &scriptedJudge{answers: []string{"两个都好"}}
	if _, ok := pairwiseJudge(context.Background(), j3, "aaa", "bbb"); ok {
		t.Fatal("不可解析应回退")
	}
}
