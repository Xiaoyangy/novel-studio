package agents

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/modelinput"
	"github.com/voocel/agentcore"
)

func TestProjectAllPlannerExactPacketSurvivesLoopCacheAndFollowup(t *testing.T) {
	prompt := "<host_prefetched_novel_context>" + strings.Repeat("完整的已验证投影前态。", 6000) + "必须保留的末尾约束</host_prefetched_novel_context>"
	base := &outlineAllOperationCaptureModel{responses: []agentcore.Message{projectAllPlannerLoopResponse("partial", "plan_details"), projectAllPlannerLoopResponse("final", "plan_details")}}
	calls, messages := 0, 0
	tool := agentcore.NewFuncTool("plan_details", "test", map[string]any{"type": "object"}, func(context.Context, json.RawMessage) (json.RawMessage, error) {
		calls++
		if calls == 1 {
			return json.RawMessage(`{"staged":"details"}`), nil
		}
		return json.RawMessage(`{"planned":true}`), nil
	})
	err := runProjectAllPlannerLoop(context.Background(), 3, prompt, agentcore.AgentContext{SystemPrompt: "unchanged system", Tools: []agentcore.Tool{tool}}, agentcore.LoopConfig{Model: base, MaxTurns: 4, CacheLastMessage: promptCacheControl, OnMessage: func(raw agentcore.AgentMessage) {
		if raw.GetRole() != agentcore.RoleUser {
			return
		}
		message := raw.(agentcore.Message)
		d, marked, err := modelinput.ParseExactAgentPacketMessage(message)
		if err != nil || !marked || d.Kind != modelinput.KindPlannerContext || message.TextContent() != prompt {
			t.Errorf("Host exact packet lost before provider: %v", err)
		}
		messages++
	}})
	if err != nil || base.calls != 2 || calls != 2 || messages != 1 {
		t.Fatalf("packet changed loop/stop behavior: %v %d/%d/%d", err, base.calls, calls, messages)
	}
	for _, request := range base.requests {
		found := 0
		for _, message := range request {
			d, marked, err := modelinput.ParseExactAgentPacketMessage(message)
			if err != nil {
				t.Fatal(err)
			}
			if marked {
				found++
				if d.Kind != modelinput.KindPlannerContext || message.TextContent() != prompt {
					t.Fatal("long packet bytes or marker changed after cache/sequence conversion")
				}
			}
		}
		if found != 1 {
			t.Fatalf("expected one exact initial packet on every turn, got %d", found)
		}
	}
}

func TestProjectAllPlannerInvalidExactPacketFailsBeforeModel(t *testing.T) {
	for _, prompt := range []string{" ", string([]byte{0xff})} {
		base := &outlineAllOperationCaptureModel{}
		if err := runProjectAllPlannerLoop(context.Background(), 3, prompt, agentcore.AgentContext{}, agentcore.LoopConfig{Model: base}); err == nil {
			t.Fatal("invalid packet accepted")
		}
		if base.calls != 0 {
			t.Fatal("invalid exact packet reached provider")
		}
	}
}
