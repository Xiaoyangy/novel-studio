package agents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/errs"
	"github.com/voocel/agentcore"
)

func projectAllPlannerLoopResponse(id, tool string) agentcore.Message {
	return agentcore.Message{
		Role: agentcore.RoleAssistant, StopReason: agentcore.StopReasonToolUse,
		Content: []agentcore.ContentBlock{agentcore.ToolCallBlock(agentcore.ToolCall{ID: id, Name: tool, Args: json.RawMessage(`{}`)})},
		Usage:   &agentcore.Usage{Input: 100, Output: 10},
	}
}

func TestProjectAllPlannerLoopStopsAfterPlannedWithoutPaidAcknowledgment(t *testing.T) {
	for _, leading := range []string{"none", "partial", "failure", "other-tool", "planned-but-staged"} {
		t.Run(leading, func(t *testing.T) {
			responses := []agentcore.Message{}
			if leading != "none" {
				tool := "plan_details"
				if leading == "other-tool" {
					tool = "novel_context"
				}
				responses = append(responses, projectAllPlannerLoopResponse("intermediate", tool))
			}
			responses = append(responses, projectAllPlannerLoopResponse("finalize", "plan_details"))
			base := &outlineAllOperationCaptureModel{responses: responses, response: agentcore.Message{
				Role: agentcore.RoleAssistant, StopReason: agentcore.StopReasonStop,
				Content: []agentcore.ContentBlock{agentcore.TextBlock("paid acknowledgment must never be requested")},
				Usage:   &agentcore.Usage{Input: 50171, Output: 58},
			}}
			starts := map[string]bool{}
			records := map[string]agentcore.Message{}
			flushes, committedResults := 0, 0
			ctx := context.WithValue(context.Background(), projectedPlanningAccountingKey{}, ProjectedPlanningAccounting{
				StartCall: func(id, role string) error {
					if role != "project_all_planner" || starts[id] {
						t.Errorf("invalid/duplicate provider start: %s %s", role, id)
					}
					starts[id] = true
					return nil
				},
				RecordUsage: func(_ string, raw agentcore.AgentMessage) {
					msg := raw.(agentcore.Message)
					id, _ := msg.Metadata["usage_audit_id"].(string)
					if !starts[id] || records[id].Usage != nil {
						t.Errorf("unbound or duplicate usage: %q", id)
					}
					records[id] = msg
				},
				AfterAgent: func() error {
					flushes++
					if len(starts) != len(records) || committedResults != len(responses) {
						t.Errorf("flush preceded completed billing/tool results: %d/%d/%d", len(starts), len(records), committedResults)
					}
					return nil
				},
			})
			model, observer := projectedAccountingModel(ctx, base, "project_all_planner", "")
			calls, guards := 0, 0
			plan := agentcore.NewFuncTool("plan_details", "test finalized plan", map[string]any{"type": "object"}, func(context.Context, json.RawMessage) (json.RawMessage, error) {
				calls++
				if calls == 1 && leading != "none" && leading != "other-tool" {
					switch leading {
					case "partial":
						return json.RawMessage(`{"planned":false,"staged":"details"}`), nil
					case "planned-but-staged":
						return json.RawMessage(`{"planned":true,"staged":"details"}`), nil
					case "failure":
						return json.RawMessage(`{"planned":true}`), fmt.Errorf("not persisted: %w", errs.ErrToolPrecondition)
					}
				}
				return json.RawMessage(`{"planned":true}`), nil
			})
			other := agentcore.NewFuncTool("novel_context", "test read", map[string]any{"type": "object"}, func(context.Context, json.RawMessage) (json.RawMessage, error) {
				return json.RawMessage(`{"planned":true}`), nil
			})
			err := runProjectAllPlannerLoop(ctx, 1, "unchanged planner prompt", agentcore.AgentContext{SystemPrompt: "planner boundary", Tools: []agentcore.Tool{plan, other}}, agentcore.LoopConfig{
				Model: model, MaxTurns: 6, MaxRetries: subagentMaxRetries, MaxToolErrors: 0, ToolsAreIdempotent: false,
				ThinkingLevel: agentcore.ThinkingHigh, PromptCacheKey: "project-all-planner-test", CacheLastMessage: promptCacheControl,
				OnMessage: func(msg agentcore.AgentMessage) {
					observer(msg)
					if msg.GetRole() == agentcore.RoleTool {
						committedResults++
					}
				},
				StopGuard: func(_ context.Context, stop agentcore.StopInfo) agentcore.StopDecision {
					guards++
					if stop.Trigger != agentcore.StopTriggerAfterTool {
						t.Errorf("success waited for acknowledgment: %s", stop.Trigger)
					}
					return agentcore.StopDecision{Allow: true}
				},
			})
			if err != nil || base.calls != len(responses) || guards != 1 || flushes != 1 || len(records) != len(responses) {
				t.Fatalf("wrong exit/accounting: err=%v model=%d want=%d guard=%d flush=%d records=%d", err, base.calls, len(responses), guards, flushes, len(records))
			}
			for _, msg := range records {
				if msg.Usage == nil || msg.Usage.Input != 100 || msg.Usage.Output != 10 {
					t.Fatal("lost or invented provider usage")
				}
			}
			for _, config := range base.configs {
				if config.ThinkingLevel != agentcore.ThinkingHigh || config.PromptCacheKey != "project-all-planner-test" {
					t.Fatal("runner lost thinking/cache settings")
				}
			}
		})
	}
}

func TestProjectAllPlannerLoopPreservesStopVetoRepairLimitAndFlushErrors(t *testing.T) {
	t.Run("stop guard veto", func(t *testing.T) {
		base := &outlineAllOperationCaptureModel{responses: []agentcore.Message{projectAllPlannerLoopResponse("first", "plan_details"), projectAllPlannerLoopResponse("second", "plan_details")}}
		tool := agentcore.NewFuncTool("plan_details", "test", map[string]any{"type": "object"}, func(context.Context, json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(`{"planned":true}`), nil
		})
		guards := 0
		err := runProjectAllPlannerLoop(context.Background(), 1, "plan", agentcore.AgentContext{Tools: []agentcore.Tool{tool}}, agentcore.LoopConfig{Model: base, MaxTurns: 4, StopGuard: func(context.Context, agentcore.StopInfo) agentcore.StopDecision {
			guards++
			if guards == 1 {
				return agentcore.StopDecision{InjectMessage: "host still requires validated work"}
			}
			return agentcore.StopDecision{Allow: true}
		}})
		if err != nil || base.calls != 2 || guards != 2 {
			t.Fatalf("guard was bypassed: %v calls=%d guards=%d", err, base.calls, guards)
		}
	})
	t.Run("repair stop drains and flushes", func(t *testing.T) {
		base := &outlineAllOperationCaptureModel{response: projectAllPlannerLoopResponse("attempt", "plan_details")}
		calls, flushes := 0, 0
		flushError := errors.New("usage persistence failed")
		ctx := context.WithValue(context.Background(), projectedPlanningAccountingKey{}, ProjectedPlanningAccounting{AfterAgent: func() error { flushes++; return flushError }})
		tool := agentcore.NewFuncTool("plan_details", "test", map[string]any{"type": "object"}, func(context.Context, json.RawMessage) (json.RawMessage, error) {
			calls++
			return nil, fmt.Errorf("unchanged grounding rejection: %w", errs.ErrToolPrecondition)
		})
		err := runProjectAllPlannerLoop(ctx, 1, "plan", agentcore.AgentContext{Tools: []agentcore.Tool{tool}}, agentcore.LoopConfig{Model: base, MaxTurns: 36})
		var repeated *RepeatedPlanningValidationError
		if !errors.As(err, &repeated) || !errors.Is(err, flushError) || calls != 3 || base.calls != 3 || flushes != 1 {
			t.Fatalf("lost failure/bound/flush: %v tools=%d model=%d flush=%d", err, calls, base.calls, flushes)
		}
	})
}
