package agents

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/voocel/agentcore"
)

func TestProjectedAccountingCoversCharacterLedgerAndObservesCallsOnce(t *testing.T) {
	for _, failed := range []bool{false, true} {
		st, observations, ids := seedCharacterRound(t, 1)
		part := &agentcore.Usage{Provider: "codex-cli", Model: "gpt-6-astra", Input: 150_000}
		model := &characterUsageTestModel{name: "gpt-6-astra", usage: part}
		if failed {
			model.failure = &characterUsageTestError{usage: part, calls: []*agentcore.Usage{part, nil}, source: "partial"}
		}
		covered := ""
		var observed []agentcore.Message
		var ledger []domain.CharacterAgentUsage
		before, after := 0, 0
		ctx := context.WithValue(context.Background(), projectedPlanningAccountingKey{}, ProjectedPlanningAccounting{
			BeforeAgent: func() error { before++; return nil }, AfterAgent: func() error { after++; return nil },
			CoverCharacterUsage: func(id string) error { covered = id; return nil },
			RecordUsage: func(name string, msg agentcore.AgentMessage) {
				message := msg.(agentcore.Message)
				if covered == "" || message.Metadata["usage_group_id"] != covered {
					t.Error("paid call preceded its durable group identity")
				}
				if name != "character_"+ids[0] || message.Metadata["usage_audit_id"] == "" {
					t.Error("call identity missing")
				}
				observed = append(observed, message)
			},
			ImportCharacterUsage: func(row domain.CharacterAgentUsage) error { ledger = append(ledger, row); return nil },
		})
		err := runOneCharacterAgent(ctx, bootstrap.Config{}, st, model, observations[ids[0]])
		if failed && !errors.Is(err, context.Canceled) || !failed && err != nil {
			t.Fatalf("failed=%t err=%v", failed, err)
		}
		if len(observed) != 1 || len(ledger) != 1 || ledger[0].UsageID != covered || before != 1 || after != 1 {
			t.Fatalf("double-counted model event or lost ledger boundary: messages=%d ledger=%+v before=%d after=%d", len(observed), ledger, before, after)
		}
	}
}

func TestProjectedAccountingHardStopPreventsNextPaidCallAndSoftStopWaitsForBoundary(t *testing.T) {
	for _, hard := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		recorded := 0
		hooks := ProjectedPlanningAccounting{
			StartCall: func(string, string) error { return nil }, SkipCall: func(string) error { return nil },
			RecordUsage: func(string, agentcore.AgentMessage) {
				recorded++
				if hard {
					cancel()
				}
			},
			CoverCharacterUsage: func(string) error { return nil },
			AfterAgent: func() error {
				if recorded > 0 {
					cancel()
				}
				return ctx.Err()
			},
		}
		ctx = context.WithValue(ctx, projectedPlanningAccountingKey{}, hooks)
		ctx, _, err := prepareCharacterAccounting(ctx, domain.CharacterAgentUsage{Role: "character", AgentID: "test"})
		if err != nil {
			t.Fatal(err)
		}
		first := agentcore.Message{Role: agentcore.RoleAssistant, Content: []agentcore.ContentBlock{agentcore.TextBlock("continue")}, Usage: &agentcore.Usage{Input: 100, Cost: &agentcore.Cost{Total: .2}}}
		second := outlineAllOperationToolUseResponse("terminal")
		second.Usage = &agentcore.Usage{Input: 100, Cost: &agentcore.Cost{Total: .2}}
		model := &outlineAllOperationCaptureModel{responses: []agentcore.Message{first, second}}
		var saved atomic.Bool
		tool := agentcore.NewFuncTool("save_foundation", "test", map[string]any{"type": "object"}, func(context.Context, json.RawMessage) (json.RawMessage, error) {
			saved.Store(true)
			return json.RawMessage(`{"saved":true}`), nil
		})
		guard := func(context.Context, agentcore.StopInfo) agentcore.StopDecision {
			if saved.Load() {
				return agentcore.StopDecision{Allow: true}
			}
			return agentcore.StopDecision{InjectMessage: "complete the terminal tool"}
		}
		_, runErr := runCharacterAgentTerminalLoop(ctx, model, "test", "test", tool, tool.Name(), 3, "", guard, "test-key")
		if hard {
			if model.calls != 1 || recorded != 1 || !errors.Is(runErr, context.Canceled) {
				t.Fatalf("hard stop allowed extra call: calls=%d recorded=%d err=%v", model.calls, recorded, runErr)
			}
		} else {
			if model.calls != 2 || recorded != 2 || runErr != nil || ctx.Err() != nil {
				t.Fatalf("soft stop interrupted current agent: calls=%d recorded=%d err=%v", model.calls, recorded, runErr)
			}
			if !errors.Is(projectedAccountingAfter(ctx), context.Canceled) {
				t.Fatal("soft stop did not trigger at agent boundary")
			}
		}
	}
}
