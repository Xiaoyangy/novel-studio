package sampler

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/chenhongyang/novel-studio/internal/domain"
	editrules "github.com/chenhongyang/novel-studio/internal/editor/rules"
	"github.com/voocel/agentcore"
	"github.com/voocel/agentcore/llm"
)

const candidateCount = 3

// Model wraps the Writer model and chooses the roughest draft_chapter candidate.
type Model struct {
	// Judge pairwise 终选裁判（Task 067）：reviewer 角色模型，异族裁判防同族自偏。
	// nil = 纯确定性评分（现状行为，老配置零影响）。
	Judge agentcore.ChatModel

	Base agentcore.ChatModel
}

func New(base agentcore.ChatModel) *Model {
	return &Model{Base: base}
}

func (m *Model) SupportsTools() bool {
	return m.Base.SupportsTools()
}

func (m *Model) Info() llm.ModelInfo {
	if info, ok := m.Base.(interface{ Info() llm.ModelInfo }); ok {
		return info.Info()
	}
	return llm.ModelInfo{}
}

func (m *Model) Capabilities() llm.Capabilities {
	if provider, ok := m.Base.(llm.CapabilityProvider); ok {
		return provider.Capabilities()
	}
	return llm.Capabilities{}
}

func (m *Model) Generate(ctx context.Context, messages []agentcore.Message, tools []agentcore.ToolSpec, opts ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	if !shouldAttemptSampling(messages) {
		return m.Base.Generate(ctx, messages, tools, opts...)
	}
	return m.sample(ctx, messages, tools, opts...)
}

func (m *Model) GenerateStream(ctx context.Context, messages []agentcore.Message, tools []agentcore.ToolSpec, opts ...agentcore.CallOption) (<-chan agentcore.StreamEvent, error) {
	if !shouldAttemptSampling(messages) {
		return m.Base.GenerateStream(ctx, messages, tools, opts...)
	}
	resp, err := m.sample(ctx, messages, tools, opts...)
	if err != nil {
		return nil, err
	}
	return streamResponse(resp), nil
}

func (m *Model) sample(ctx context.Context, messages []agentcore.Message, tools []agentcore.ToolSpec, opts ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	type candidate struct {
		response *agentcore.LLMResponse
		call     *agentcore.ToolCall
		args     draftArgs
		score    domain.SamplingCandidate
	}
	candidates := make([]candidate, 0, candidateCount)
	sampleCount := candidateCount
	if isRewriteSamplingRequest(messages) {
		sampleCount = 1
	}
	for i := 1; i <= sampleCount; i++ {
		resp, err := m.Base.Generate(ctx, messages, tools, opts...)
		if err != nil {
			if i == 1 {
				return nil, err
			}
			break
		}
		call, args, ok := findDraftCall(resp.Message)
		if !ok {
			if i == 1 {
				return resp, nil
			}
			continue
		}
		candidates = append(candidates, candidate{
			response: resp,
			call:     call,
			args:     args,
			score:    editrules.CandidateFromText(i, args.Chapter, args.Content),
		})
	}
	if len(candidates) == 0 {
		return nil, errors.New("writer sampler did not receive a draft_chapter candidate")
	}
	// 确定性初筛：按粗糙度排序，淘汰最差；剩余 top2 走 pairwise 终选（Task 067）。
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].score.RoughnessScore > candidates[j].score.RoughnessScore
	})
	best := candidates[0]
	if m.Judge != nil && len(candidates) >= 2 {
		if winner, decided := pairwiseJudge(ctx, m.Judge, candidates[0].args.Content, candidates[1].args.Content); decided && winner == 1 {
			best = candidates[1]
		}
		// 不一致/失败 → 保持确定性分选出的 candidates[0]（回退逻辑）
	}
	record := domain.SamplingRecord{
		Chapter:       best.args.Chapter,
		SelectedIndex: best.score.Index,
		GeneratedAt:   time.Now().Format(time.RFC3339),
	}
	for _, c := range candidates {
		record.Candidates = append(record.Candidates, c.score)
	}
	resp := cloneResponse(best.response)
	attachSamplingRecord(&resp.Message, best.call.ID, record)
	return resp, nil
}

func isRewriteSamplingRequest(messages []agentcore.Message) bool {
	start := max(0, len(messages)-12)
	for i := len(messages) - 1; i >= start; i-- {
		text := messages[i].TextContent()
		if containsAny(text,
			"待返工章节的新推演计划",
			"待返工章节",
			"重写第 ",
			"重写第1", "重写第2", "重写第3", "重写第4", "重写第5",
			`"flow":"rewriting"`, `"flow": "rewriting"`,
		) {
			return true
		}
	}
	return false
}

type draftArgs struct {
	Chapter int    `json:"chapter"`
	Content string `json:"content"`
	Mode    string `json:"mode"`
}

func findDraftCall(msg agentcore.Message) (*agentcore.ToolCall, draftArgs, bool) {
	for i := range msg.Content {
		block := msg.Content[i]
		if block.Type != agentcore.ContentToolCall || block.ToolCall == nil || block.ToolCall.Name != "draft_chapter" {
			continue
		}
		var args draftArgs
		if err := json.Unmarshal(block.ToolCall.Args, &args); err != nil {
			continue
		}
		if args.Chapter <= 0 || args.Content == "" {
			continue
		}
		return block.ToolCall, args, true
	}
	return nil, draftArgs{}, false
}

func attachSamplingRecord(msg *agentcore.Message, toolCallID string, record domain.SamplingRecord) {
	for i := range msg.Content {
		block := &msg.Content[i]
		if block.Type != agentcore.ContentToolCall || block.ToolCall == nil || block.ToolCall.ID != toolCallID {
			continue
		}
		var args map[string]any
		if err := json.Unmarshal(block.ToolCall.Args, &args); err != nil {
			return
		}
		args["sampling"] = record
		raw, err := json.Marshal(args)
		if err != nil {
			return
		}
		block.ToolCall.Args = raw
	}
}

func shouldAttemptSampling(messages []agentcore.Message) bool {
	start := max(0, len(messages)-8)
	for i := len(messages) - 1; i >= start; i-- {
		text := messages[i].TextContent()
		if text == "" {
			continue
		}
		if containsAny(text, "草稿已成功保存", "立即 read_chapter(source=draft)", "check_consistency") {
			return false
		}
		if containsAny(text, "draft_chapter", "写入章节正文", "章节草稿") &&
			containsAny(text, "chapter_plan", "current_chapter_outline", "next_step", "草稿", "正文") {
			return true
		}
	}
	return false
}

func containsAny(s string, needles ...string) bool {
	for _, needle := range needles {
		if needle != "" && strings.Contains(s, needle) {
			return true
		}
	}
	return false
}

func streamResponse(resp *agentcore.LLMResponse) <-chan agentcore.StreamEvent {
	out := make(chan agentcore.StreamEvent, len(resp.Message.Content)+1)
	go func() {
		defer close(out)
		for i := range resp.Message.Content {
			block := resp.Message.Content[i]
			if block.Type == agentcore.ContentToolCall && block.ToolCall != nil {
				out <- agentcore.StreamEvent{Type: agentcore.StreamEventToolCallStart, ContentIndex: i, Message: resp.Message}
				call := *block.ToolCall
				out <- agentcore.StreamEvent{Type: agentcore.StreamEventToolCallEnd, ContentIndex: i, Message: resp.Message, CompletedToolCall: &call}
			}
		}
		out <- agentcore.StreamEvent{Type: agentcore.StreamEventDone, Message: resp.Message, StopReason: resp.Message.StopReason}
	}()
	return out
}

func cloneResponse(resp *agentcore.LLMResponse) *agentcore.LLMResponse {
	if resp == nil {
		return nil
	}
	cloned := *resp
	cloned.Message = cloneMessage(resp.Message)
	return &cloned
}

func cloneMessage(msg agentcore.Message) agentcore.Message {
	msg.Content = append([]agentcore.ContentBlock(nil), msg.Content...)
	for i := range msg.Content {
		if msg.Content[i].ToolCall == nil {
			continue
		}
		tc := *msg.Content[i].ToolCall
		tc.Args = append([]byte(nil), tc.Args...)
		msg.Content[i].ToolCall = &tc
	}
	return msg
}

// pairwiseJudge 对 top2 候选做随机换位、正反两轮的配对比较；两轮一致才裁定
// （返回 0/1 与 decided=true），不一致或失败返回 decided=false 交回确定性分。
func pairwiseJudge(ctx context.Context, judge agentcore.ChatModel, a, b string) (int, bool) {
	first, ok1 := pairwiseOnce(ctx, judge, a, b) // A=a, B=b
	if !ok1 {
		return 0, false
	}
	second, ok2 := pairwiseOnce(ctx, judge, b, a) // 换位
	if !ok2 {
		return 0, false
	}
	// 换位后应互补：第一轮选 A(=a) 且第二轮选 B(=a) → a 胜。
	if first == 0 && second == 1 {
		return 0, true
	}
	if first == 1 && second == 0 {
		return 1, true
	}
	return 0, false // 位置偏好/不一致 → 回退
}

func pairwiseOnce(ctx context.Context, judge agentcore.ChatModel, slotA, slotB string) (int, bool) {
	prompt := "你是小说终审。对比两段候选正文，选更像人类作者、AI 腔更少、叙事更扎实的一段。只回答一个字母：A 或 B。\n\n【A】\n" +
		truncateRunes(slotA, 1800) + "\n\n【B】\n" + truncateRunes(slotB, 1800)
	resp, err := judge.Generate(ctx, []agentcore.Message{agentcore.UserMsg(prompt)}, nil)
	if err != nil || resp == nil {
		return 0, false
	}
	text := strings.ToUpper(strings.TrimSpace(resp.Message.TextContent()))
	switch {
	case strings.HasPrefix(text, "A"):
		return 0, true
	case strings.HasPrefix(text, "B"):
		return 1, true
	}
	return 0, false
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
