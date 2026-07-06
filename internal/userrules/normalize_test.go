package userrules

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/voocel/agentcore"
)

func TestExtractJSON_StripsCodeFences(t *testing.T) {
	cases := []struct{ in, wantHas string }{
		{"```json\n{\"a\":1}\n```", `"a":1`},
		{"```\n{\"a\":1}\n```", `"a":1`},
		{"前缀解释\n{\"a\":1}\n后缀", `"a":1`},
		{"{\"a\":1}", `"a":1`},
	}
	for _, c := range cases {
		got := extractJSON(c.in)
		if got == "" {
			t.Fatalf("extractJSON(%q) 返回空", c.in)
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(got), &m); err != nil {
			t.Fatalf("extractJSON(%q)=%q 不是合法 JSON: %v", c.in, got, err)
		}
	}
	if extractJSON("没有任何 JSON") != "" {
		t.Fatal("无 JSON 时应返回空串")
	}
}

func TestCoerceUncertain_HandlesAllDriftForms(t *testing.T) {
	// 原型实测：uncertain 时而字符串、时而 []string、时而 [{item,reason}]。
	cases := []struct {
		name string
		raw  string
		want int // 期望条目数（>0 即可，验证不丢）
	}{
		{"array_of_string", `["少用比喻：无阈值"]`, 1},
		{"plain_string", `"chapter_words 太模糊未提升"`, 1},
		{"array_of_object", `[{"item":"少用比喻","reason":"无明确阈值"}]`, 1},
		{"array_of_field_object", `[{"field":"chapter_words.min","reason":"未给下限"}]`, 1},
		{"empty_array", `[]`, 0},
		{"empty_string", `""`, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := coerceUncertain(json.RawMessage(c.raw))
			if len(got) != c.want {
				t.Fatalf("coerceUncertain(%s)=%v，期望 %d 条", c.raw, got, c.want)
			}
		})
	}
}

func TestParseNormalizerJSON_FullOutput(t *testing.T) {
	raw := "```json\n" + `{
  "structured": {"chapter_words": {"min": 1200, "max": 1600}, "forbidden_phrases": ["某种程度上"]},
  "preferences": "主角冷静克制",
  "uncertain": [{"item": "少用比喻", "reason": "无阈值"}]
}` + "\n```"
	out, ok := parseNormalizerJSON(raw)
	if !ok {
		t.Fatal("应解析成功")
	}
	if out.Structured.ChapterWords == nil || out.Structured.ChapterWords.Min != 1200 {
		t.Fatalf("chapter_words 解析错误：%+v", out.Structured.ChapterWords)
	}
	if len(out.Structured.ForbiddenPhrases) != 1 || out.Structured.ForbiddenPhrases[0] != "某种程度上" {
		t.Fatalf("forbidden_phrases 解析错误：%v", out.Structured.ForbiddenPhrases)
	}
	if out.Preferences != "主角冷静克制" {
		t.Fatalf("preferences 解析错误：%q", out.Preferences)
	}
	if got := coerceUncertain(out.Uncertain); len(got) != 1 {
		t.Fatalf("uncertain 应有 1 条，得到 %v", got)
	}
}

func TestParseNormalizerJSON_GarbageFails(t *testing.T) {
	if _, ok := parseNormalizerJSON("模型只回了一句话，没有 JSON"); ok {
		t.Fatal("无 JSON 应解析失败（触发降级）")
	}
	if _, ok := parseNormalizerJSON("{ 不完整"); ok {
		t.Fatal("残缺 JSON 应解析失败")
	}
}

func TestNormalize_NilModelDegrades(t *testing.T) {
	// 无模型可用：整体降级为 raw preferences，不产 structured，永不 panic/返错。
	var n *Normalizer = NewNormalizer(nil)
	cand := n.Normalize(t.Context(), "startup_prompt", "每章1200字，主角冷静")
	if !cand.Degraded {
		t.Fatal("无模型应降级")
	}
	if cand.Preferences == "" {
		t.Fatal("降级应保留原文为 preferences")
	}
	if cand.Structured.ChapterWords != nil {
		t.Fatal("降级不应产出 structured")
	}
}

// scriptedModel 是最小 fake ChatModel：按调用次序吐预设回复，并记录最后一轮收到的
// messages，供断言反馈式重试是否把纠正提示并入了下一轮对话。回复用尽后重复最后一条。
type scriptedModel struct {
	replies  []string
	calls    int
	lastMsgs []agentcore.Message
	lastCfg  agentcore.CallConfig
}

func (m *scriptedModel) Generate(_ context.Context, messages []agentcore.Message, _ []agentcore.ToolSpec, opts ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	var cfg agentcore.CallConfig
	for _, o := range opts {
		o(&cfg)
	}
	m.lastCfg = cfg
	m.lastMsgs = messages
	i := m.calls
	m.calls++
	if i >= len(m.replies) {
		i = len(m.replies) - 1
	}
	return &agentcore.LLMResponse{Message: agentcore.Message{
		Role:    agentcore.RoleAssistant,
		Content: []agentcore.ContentBlock{agentcore.TextBlock(m.replies[i])},
	}}, nil
}

func (m *scriptedModel) GenerateStream(context.Context, []agentcore.Message, []agentcore.ToolSpec, ...agentcore.CallOption) (<-chan agentcore.StreamEvent, error) {
	return nil, nil
}

func (m *scriptedModel) SupportsTools() bool { return false }

// 反馈式重试：首轮吐坏 JSON、次轮才合法。Normalize 应成功，且次轮对话里带上了上一轮的
// 坏输出与纠正提示（反馈式，而非原样盲重试）。
func TestNormalize_FeedbackRetryRecovers(t *testing.T) {
	model := &scriptedModel{replies: []string{
		"这不是 JSON",
		`{"structured":{"chapter_words":{"min":1200,"max":1600}},"preferences":"","uncertain":[]}`,
	}}
	n := NewNormalizer(model)

	cand := n.Normalize(t.Context(), "startup_prompt", "每章1200到1600字")
	if cand.Degraded {
		t.Fatal("次轮已返回合法 JSON，不应降级")
	}
	if cand.Structured.ChapterWords == nil || cand.Structured.ChapterWords.Min != 1200 {
		t.Fatalf("应解析出 chapter_words，got %+v", cand.Structured)
	}
	if model.calls != 2 {
		t.Fatalf("应在第 2 次成功，实际调用 %d 次", model.calls)
	}

	var sawBad, sawHint bool
	for _, msg := range model.lastMsgs {
		switch msg.TextContent() {
		case "这不是 JSON":
			sawBad = true
		case normalizerRetryHint:
			sawHint = true
		}
	}
	if !sawBad || !sawHint {
		t.Errorf("次轮应并入上一轮坏输出与纠正提示，sawBad=%v sawHint=%v", sawBad, sawHint)
	}
}

// 归一化是机械抽取：对支持关闭思考的模型应显式关思考，并把 max_tokens 留足给 JSON。
// scriptedModel 未实现 CapabilityProvider → 思考策略默认允许 off → 应 Resolve 成 off。
func TestNormalize_DisablesThinkingAndReservesTokens(t *testing.T) {
	model := &scriptedModel{replies: []string{`{"preferences":"x"}`}}
	n := NewNormalizer(model)

	_ = n.Normalize(t.Context(), "startup_prompt", "随便一条规则")
	if model.lastCfg.ThinkingLevel != agentcore.ThinkingOff {
		t.Errorf("应对可关闭思考的模型关思考，got %q", model.lastCfg.ThinkingLevel)
	}
	if model.lastCfg.MaxTokens != normalizeMaxTokens {
		t.Errorf("max_tokens 应为 %d，got %d", normalizeMaxTokens, model.lastCfg.MaxTokens)
	}
}

// 全程坏 JSON：重试耗尽后降级，且恰好尝试 normalizeMaxAttempts 次。
func TestNormalize_FeedbackRetryExhaustsThenDegrades(t *testing.T) {
	model := &scriptedModel{replies: []string{"坏"}}
	n := NewNormalizer(model)

	cand := n.Normalize(t.Context(), "startup_prompt", "每章1200字")
	if !cand.Degraded {
		t.Fatal("全程坏 JSON 应降级")
	}
	if model.calls != normalizeMaxAttempts {
		t.Fatalf("应尝试 %d 次，实际 %d", normalizeMaxAttempts, model.calls)
	}
}
