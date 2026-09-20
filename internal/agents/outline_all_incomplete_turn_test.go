package agents

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/assets"
	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/voocel/agentcore"
)

func TestOutlineAllIncompleteTurnRetriesWithoutInventingMutation(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	model := &outlineAllOperationCaptureModel{responses: []agentcore.Message{
		{Role: agentcore.RoleAssistant, StopReason: agentcore.StopReasonStop, Content: []agentcore.ContentBlock{agentcore.TextBlock("我将展开目标弧。")}},
		outlineAllOperationToolUseResponse("actual-save"),
	}}
	for i := range model.responses {
		model.responses[i].Usage = &agentcore.Usage{Input: 10, Output: 2}
	}
	accountedCalls := 0
	saves := 0
	tool := agentcore.NewFuncTool("save_foundation", "test save", map[string]any{"type": "object"}, func(context.Context, json.RawMessage) (json.RawMessage, error) {
		saves++
		return json.RawMessage(`{"saved":true,"outline_all":true,"type":"expand_arc"}`), nil
	})
	err := runOutlineAllOperationWithModel(t.Context(), bootstrap.Config{}, assets.Bundle{}, st,
		outlineAllOperationTask(t, 29, 3, 3, 14),
		outlineAllOperationModel{ChatModel: model, Provider: "deepseek", Name: "deepseek-v4-flash"}, tool,
		func(_ string, message agentcore.AgentMessage) {
			if m, ok := message.(agentcore.Message); ok && m.Role == agentcore.RoleAssistant && m.Usage != nil {
				accountedCalls++
			}
		})
	if err != nil {
		t.Fatalf("text-only end before the authorized save must get a bounded follow-up: %v", err)
	}
	if model.calls != 2 || saves != 1 {
		t.Fatalf("calls=%d saves=%d; want two calls and exactly one real mutation", model.calls, saves)
	}
	if accountedCalls != 2 {
		t.Fatalf("follow-up usage must be accounted separately: %d", accountedCalls)
	}
	last := model.requests[1][len(model.requests[1])-1].TextContent()
	if !strings.Contains(last, "operation=29") || !strings.Contains(last, "save_foundation") {
		t.Fatalf("follow-up lost the frozen operation: %q", last)
	}
}

func TestOutlineAllIncompleteTurnRemainsBoundedAndRespectsProviderStops(t *testing.T) {
	for _, reason := range []agentcore.StopReason{agentcore.StopReasonStop, agentcore.StopReason("safety"), agentcore.StopReasonAborted, agentcore.StopReasonError} {
		t.Run(string(reason), func(t *testing.T) {
			st := store.NewStore(t.TempDir())
			if err := st.Init(); err != nil {
				t.Fatal(err)
			}
			model := &outlineAllOperationCaptureModel{response: agentcore.Message{
				Role: agentcore.RoleAssistant, StopReason: reason,
				Content: []agentcore.ContentBlock{agentcore.TextBlock("No mutation was executed.")},
			}}
			tool := agentcore.NewFuncTool("save_foundation", "test save", map[string]any{"type": "object"}, func(context.Context, json.RawMessage) (json.RawMessage, error) {
				t.Fatal("host must never fabricate a tool invocation from response text")
				return nil, nil
			})
			err := runOutlineAllOperationWithModel(t.Context(), bootstrap.Config{}, assets.Bundle{}, st,
				outlineAllOperationTask(t, 29, 3, 3, 14),
				outlineAllOperationModel{ChatModel: model, Provider: "deepseek", Name: "deepseek-v4-flash"}, tool)
			if err == nil {
				t.Fatal("text without a successful mutation must fail closed")
			}
			want := 1
			if reason == agentcore.StopReasonStop {
				want = outlineAllOperationMaxTurns
				if !errors.Is(err, agentcore.ErrMaxTurns) {
					t.Fatalf("lost turn cap error: %v", err)
				}
			} else if !strings.Contains(err.Error(), string(reason)) {
				t.Fatalf("lost provider stop reason: %v", err)
			}
			if model.calls != want {
				t.Fatalf("calls=%d, want %d for %s", model.calls, want, reason)
			}
		})
	}
}

func TestOutlineAllIncompleteTurnPreservesLastSaveRejection(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	rejection := errors.New("outline_all V3A3 chapter 167 rejected: missing concrete payoff evidence")
	model := &outlineAllOperationCaptureModel{responses: []agentcore.Message{
		outlineAllOperationToolUseResponse("rejected-save"),
		{Role: agentcore.RoleAssistant, StopReason: agentcore.StopReasonStop, Content: []agentcore.ContentBlock{agentcore.TextBlock("已完成。")}},
	}}
	tool := agentcore.NewFuncTool("save_foundation", "test save", map[string]any{"type": "object"}, func(context.Context, json.RawMessage) (json.RawMessage, error) {
		return nil, rejection
	})
	cfg := bootstrap.Config{Roles: map[string]bootstrap.RoleConfig{"architect": {MaxTurns: 2}}}
	err := runOutlineAllOperationWithModel(t.Context(), cfg, assets.Bundle{}, st,
		outlineAllOperationTask(t, 29, 3, 3, 14),
		outlineAllOperationModel{ChatModel: model, Provider: "deepseek", Name: "deepseek-v4-flash"}, tool)
	if err == nil || !errors.Is(err, rejection) {
		t.Fatalf("terminal error must preserve the actual save rejection, got: %v", err)
	}
	if model.calls != 2 {
		t.Fatalf("must respect the configured two-turn cap, got %d", model.calls)
	}
}
