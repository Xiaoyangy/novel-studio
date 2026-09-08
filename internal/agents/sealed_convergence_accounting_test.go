package agents_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/chenhongyang/novel-studio/assets"
	"github.com/chenhongyang/novel-studio/internal/agents"
	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/host"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/tools"
	"github.com/voocel/agentcore"
)

type sealedUsageFakeModel struct{ judge bool }

func (m *sealedUsageFakeModel) SupportsTools() bool  { return true }
func (m *sealedUsageFakeModel) ProviderName() string { return "codex-cli" }
func (m *sealedUsageFakeModel) Generate(context.Context, []agentcore.Message, []agentcore.ToolSpec, ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	msg := agentcore.Message{Role: agentcore.RoleAssistant, Content: []agentcore.ContentBlock{agentcore.TextBlock("bounded sidecar response")}, Usage: &agentcore.Usage{Input: 10, Output: 2, Cost: &agentcore.Cost{Total: .25}}}
	if m.judge {
		msg.Content = []agentcore.ContentBlock{agentcore.ToolCallBlock(agentcore.ToolCall{ID: "verdict", Name: "submit_plan_grounding_verdict", Args: json.RawMessage(`{"pass":true,"findings":[]}`)})}
	}
	return &agentcore.LLMResponse{Message: msg}, nil
}
func (m *sealedUsageFakeModel) GenerateStream(ctx context.Context, messages []agentcore.Message, specs []agentcore.ToolSpec, opts ...agentcore.CallOption) (<-chan agentcore.StreamEvent, error) {
	r, err := m.Generate(ctx, messages, specs, opts...)
	if err != nil {
		return nil, err
	}
	ch := make(chan agentcore.StreamEvent, 1)
	ch <- agentcore.StreamEvent{Type: agentcore.StreamEventDone, Message: r.Message}
	close(ch)
	return ch, nil
}

type sealedUsageRestrictedTool struct{ reviewer tools.PlanGroundingReviewer }

func (t *sealedUsageRestrictedTool) Name() string           { return "plan_details" }
func (t *sealedUsageRestrictedTool) Description() string    { return "restricted test tool" }
func (t *sealedUsageRestrictedTool) Schema() map[string]any { return map[string]any{"type": "object"} }
func (t *sealedUsageRestrictedTool) Execute(context.Context, json.RawMessage) (json.RawMessage, error) {
	return json.RawMessage(`{"planned":true}`), nil
}
func (t *sealedUsageRestrictedTool) WithPlanGroundingReviewer(r tools.PlanGroundingReviewer) (agentcore.Tool, error) {
	t.reviewer = r
	return t, nil
}

func TestSealedConvergenceFivePaidLanesShareScopedWALAndPreserveSessions(t *testing.T) {
	for _, lane := range []string{"continuation", "replacement", "binary_failover", "seeded_compact_finalize", "exhausted_compact_finalize"} {
		t.Run(lane, func(t *testing.T) {
			st := store.NewStore(t.TempDir())
			if err := st.Init(); err != nil {
				t.Fatal(err)
			}
			owner := fmt.Sprintf("pipeline-convergence-replan-ch000001-pid%d-123", os.Getpid())
			if err := st.Runtime.AcquirePipelineExecution(domain.PipelineExecutionLock{Mode: domain.PipelineExecutionProjectAll, TargetChapter: 1, Owner: owner, ExpiresAt: time.Now().Add(time.Minute)}); err != nil {
				t.Fatal(err)
			}
			cfg := bootstrap.Config{OutputDir: st.Dir(), Provider: "local", ModelName: "writer-test", Providers: map[string]bootstrap.ProviderConfig{"local": {Type: "codex-cli", BaseURL: "/never/run/unbound"}}, Roles: map[string]bootstrap.RoleConfig{"world_arbiter": {Provider: "local", Model: "judge-test"}}}
			scope, err := host.NewScopedUsageAccounting(context.Background(), st, "sealed-generation", bootstrap.BudgetConfig{})
			if err != nil {
				t.Fatal(err)
			}
			var mu sync.Mutex
			var targets []string
			ctx := agents.WithSealedConvergenceUsageAccounting(scope.Context(), agents.SealedConvergenceUsageAccounting{ExecutionOutputDir: st.Dir(), RecordUsage: scope.Record, Decorate: func(ctx context.Context, role, provider, name string, raw agentcore.ChatModel) agentcore.ChatModel {
				mu.Lock()
				targets = append(targets, role+":"+name)
				mu.Unlock()
				return scope.Decorate(ctx, role, provider, name, &sealedUsageFakeModel{judge: name == "judge-test"})
			}})
			tool := &sealedUsageRestrictedTool{}
			switch lane {
			case "continuation":
				err = agents.RunSealedConvergencePlannerContinuation(ctx, cfg, assets.Bundle{}, st.Dir(), 1, "bounded prompt")
			case "replacement":
				err = agents.RunSealedConvergencePlannerContinuationReplacement(ctx, cfg, assets.Bundle{}, st.Dir(), 1, "bounded prompt")
			case "binary_failover":
				err = agents.RunSealedConvergencePlannerContinuationBinaryFailover(ctx, cfg, assets.Bundle{}, st.Dir(), 1, "bounded prompt", "/usr/bin/true")
			case "seeded_compact_finalize":
				err = agents.RunSealedConvergencePlannerSeededCompactFinalize(ctx, cfg, assets.Bundle{}, st.Dir(), 1, "bounded prompt", "/usr/bin/true", tool)
			case "exhausted_compact_finalize":
				err = agents.RunSealedConvergencePlannerExhaustedCompactFinalize(ctx, cfg, assets.Bundle{}, st.Dir(), 1, "bounded prompt", "/usr/bin/true", tool)
			}
			if err != nil {
				t.Fatal(err)
			}
			if tool.reviewer.Review != nil {
				verdict, err := tool.reviewer.Review(ctx, domain.PlanGroundingInput{Policy: domain.PlanGroundingPolicyV1, ReviewProtocol: tool.reviewer.Protocol, Plan: domain.ChapterPlan{Chapter: 1}})
				if err != nil || !verdict.Pass {
					t.Fatalf("scoped judge did not complete: %+v %v", verdict, err)
				}
			}
			if err := scope.Finish(nil); err != nil {
				t.Fatal(err)
			}
			meter, err := host.NewDurableUsageMeter(store.NewStore(st.Dir()))
			if err != nil {
				t.Fatal(err)
			}
			state := meter.Tracker().Snapshot()
			want := 10
			if tool.reviewer.Review != nil {
				want = 20
				if state.PerAgent["world_arbiter"].Input != 10 {
					t.Fatal("judge was not charged independently")
				}
			}
			if state.Overall.Input != want || state.Overall.Cost != float64(want)/40 || state.PerAgent["writer"].Input != 10 || len(state.PendingUsageCalls) != 0 {
				t.Fatalf("scoped caller lost/doubled usage: %+v", state)
			}
			name := "convergence_planner_" + lane
			if lane == "replacement" || lane == "binary_failover" {
				name = "convergence_planner_continuation_" + lane
			}
			if _, err := os.Stat(filepath.Join(st.Dir(), "meta", "sessions", "agents", name+"-ch01.jsonl")); err != nil {
				t.Fatal(err)
			}
			if len(targets) != (want/10) || targets[0] != "writer:writer-test" {
				t.Fatalf("model routing or dispatch count changed: %v", targets)
			}
		})
	}
}

func TestSealedConvergencePaidRunnerRejectsMissingOrWrongAccountingBeforeProvider(t *testing.T) {
	cfg := bootstrap.Config{Provider: "local", ModelName: "test", Providers: map[string]bootstrap.ProviderConfig{"local": {Type: "codex-cli", BaseURL: "/never-execute"}}}
	dir := t.TempDir()
	for _, ctx := range []context.Context{context.Background(), agents.WithSealedConvergenceUsageAccounting(context.Background(), agents.SealedConvergenceUsageAccounting{ExecutionOutputDir: dir + "-candidate", Decorate: func(context.Context, string, string, string, agentcore.ChatModel) agentcore.ChatModel {
		t.Fatal("unbound provider reached")
		return nil
	}, RecordUsage: func(string, agentcore.AgentMessage) { t.Fatal("unbound usage charged") }})} {
		if err := agents.RunSealedConvergencePlannerContinuation(ctx, cfg, assets.Bundle{}, dir, 1, "test"); err == nil {
			t.Fatal("runner accepted absent/mismatched accounting")
		}
	}
}
