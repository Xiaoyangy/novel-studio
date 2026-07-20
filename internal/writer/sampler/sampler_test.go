package sampler

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/voocel/agentcore"
)

type fakeModel struct {
	responses []*agentcore.LLMResponse
	calls     int
	thinking  []agentcore.ThinkingLevel
	mu        sync.Mutex
}

func (m *fakeModel) Generate(_ context.Context, _ []agentcore.Message, _ []agentcore.ToolSpec, opts ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
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

func TestSamplerUsesSingleCandidateForRewrite(t *testing.T) {
	base := &fakeModel{responses: []*agentcore.LLMResponse{
		draftResponse("rewrite", 1, "返工后的正文。"),
	}}
	model := New(base)
	_, err := model.Generate(context.Background(), []agentcore.Message{
		{Role: agentcore.RoleUser, Content: []agentcore.ContentBlock{agentcore.TextBlock("重写第 1 章，按 rewrite brief 局部压缩。")}},
		{Role: agentcore.RoleTool, Content: []agentcore.ContentBlock{agentcore.TextBlock("next_step: 请调用 draft_chapter 写章节草稿正文")}},
	}, nil)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if base.calls != 1 {
		t.Fatalf("rewrite sampling calls = %d, want 1", base.calls)
	}
}

func TestRenderSamplerUsesExactlyOneBaseCallAndNoPairwiseJudge(t *testing.T) {
	base := &fakeModel{responses: []*agentcore.LLMResponse{
		draftResponse("render", 1, "冻结渲染正文。"),
	}}
	judge := &scriptedJudge{answers: []string{"A"}}
	model := NewRender(base)
	model.Judge = judge
	resp, err := model.Generate(context.Background(), []agentcore.Message{
		{Role: agentcore.RoleUser, Content: []agentcore.ContentBlock{agentcore.TextBlock("next_step: 请调用 draft_chapter 写章节草稿正文")}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if base.calls != renderCandidateCount {
		t.Fatalf("render base calls=%d want=%d", base.calls, renderCandidateCount)
	}
	if judge.calls != 0 {
		t.Fatalf("render pairwise judge calls=%d want=0", judge.calls)
	}
	if _, _, ok := findDraftCall(resp.Message); !ok {
		t.Fatal("render sampler lost the sole draft response")
	}
}

func TestSamplerProtocolDigestV3IsStable(t *testing.T) {
	const want = "1f4fb223d259fb4526ea47156efed5c740f99cf84b04643743b80b8c3a04466e"
	if got := ProtocolDigest(); got != want {
		t.Fatalf("ProtocolDigest()=%q want=%q", got, want)
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
	if selected := int(sampling["selected_index"].(float64)); selected < 1 || selected > 3 {
		t.Fatalf("selected_index = %v, want 1..3", sampling["selected_index"])
	}
}

type concurrentProbeModel struct {
	mu        sync.Mutex
	active    int
	maxActive int
	nextID    int
}

func (m *concurrentProbeModel) Generate(_ context.Context, _ []agentcore.Message, _ []agentcore.ToolSpec, _ ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	m.mu.Lock()
	m.active++
	if m.active > m.maxActive {
		m.maxActive = m.active
	}
	m.nextID++
	id := m.nextID
	m.mu.Unlock()
	time.Sleep(25 * time.Millisecond)
	m.mu.Lock()
	m.active--
	m.mu.Unlock()
	return draftResponse(string(rune('a'+id-1)), 1, "候选正文。"), nil
}

func (*concurrentProbeModel) GenerateStream(context.Context, []agentcore.Message, []agentcore.ToolSpec, ...agentcore.CallOption) (<-chan agentcore.StreamEvent, error) {
	panic("not used")
}
func (*concurrentProbeModel) SupportsTools() bool { return true }

func TestSamplerGeneratesCandidatesConcurrently(t *testing.T) {
	base := &concurrentProbeModel{}
	model := New(base)
	if _, err := model.Generate(context.Background(), []agentcore.Message{
		{Role: agentcore.RoleUser, Content: []agentcore.ContentBlock{agentcore.TextBlock("next_step: 请调用 draft_chapter 写章节草稿正文")}},
	}, nil); err != nil {
		t.Fatal(err)
	}
	if base.maxActive < 2 {
		t.Fatalf("expected speculative candidates to overlap, maxActive=%d", base.maxActive)
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
	mu      sync.Mutex
}

func (s *scriptedJudge) Generate(_ context.Context, _ []agentcore.Message, _ []agentcore.ToolSpec, _ ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
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
