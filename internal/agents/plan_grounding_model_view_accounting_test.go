package agents

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/llmcodex"
	"github.com/voocel/agentcore"
)

func groundingModelViewOversizedFixture() domain.PlanGroundingInput {
	return groundingBudgetInput("待核查计划；" + strings.Repeat("重复原文必须完整保留，不能改写；\n", 16000))
}

func TestGroundingModelViewFallbackAccountsOnlyExecutedAttempt(t *testing.T) {
	for _, decorated := range []bool{false, true} {
		t.Run(fmt.Sprint(decorated), func(t *testing.T) {
			binary, calls, capture, _ := groundingBudgetFakeCLI(t)
			base := llmcodex.New(binary, "grounding-model-view", "high", llmcodex.WithContextWindow(272000))
			models := &bootstrap.ModelSet{Default: bootstrap.NewSwappableModel("codex", "grounding-model-view", base)}
			starts, skips, records := 0, 0, 0
			hooks := DirectUsageLifecycle{StartCall: func(_, role string) error {
				if role != "plan_grounding" {
					t.Fatalf("wrong role: %s", role)
				}
				starts++
				return nil
			}, SkipCall: func(string) error { skips++; return nil }}
			record := func(role string, msg agentcore.AgentMessage) {
				message, ok := msg.(agentcore.Message)
				if !ok || role != "plan_grounding" || message.Usage == nil || message.Usage.Input != 1234 || message.Usage.Output != 56 || message.Metadata["codex_usage_source"] != "reported" {
					t.Fatalf("incorrect reported usage: role=%s message=%#v", role, msg)
				}
				records++
			}
			if decorated {
				models.SetAttemptDecorator(func(ctx context.Context, role, provider, name string, model agentcore.ChatModel) agentcore.ChatModel {
					return NewAuditedUsageModel(ctx, model, role, provider, name, "test", record, hooks)
				})
			}
			reviewer := NewPlanGroundingReviewer(bootstrap.Config{}, models, record)
			reviewer, err := reviewer.ResolveForSimulation(domain.ChapterWorldSimulation{CharacterActivation: &domain.CharacterActivationSimulationBinding{}})
			if err != nil {
				t.Fatal(err)
			}
			input := groundingModelViewOversizedFixture()
			input.ReviewProtocol = reviewer.Protocol
			before, _ := json.Marshal(input)
			ctx := WithDirectUsageLifecycle(t.Context(), hooks)
			verdict, err := reviewer.Review(ctx, input)
			if err != nil || !verdict.Pass {
				t.Fatalf("lossless fallback did not reach verdict: %v", err)
			}
			if starts != 2 || skips != 1 || records != 1 {
				t.Fatalf("local rejection was billed or executed attempt double recorded: starts=%d skips=%d records=%d", starts, skips, records)
			}
			log, _ := os.ReadFile(calls)
			if strings.Count(string(log), "exec\n") != 1 || strings.Count(string(log), "inventory\n") != 1 {
				t.Fatalf("unexpected real CLI attempts: %s", log)
			}
			prompt, err := os.ReadFile(capture)
			parameters, _ := json.Marshal(planGroundingToolSpec().Parameters)
			if err != nil || !bytes.Contains(prompt, []byte("plan-grounding-exact-shared.v1")) || !bytes.Contains(prompt, []byte(planGroundingPrompt+activationGroundingPrompt)) || !bytes.Contains(prompt, parameters) {
				t.Fatalf("fallback lost its self-contained view, original rules or tool schema: %v", err)
			}
			after, _ := json.Marshal(input)
			if !bytes.Equal(before, after) {
				t.Fatal("model transport changed canonical input")
			}
		})
	}
}

type groundingModelViewFailure struct {
	groundingProbeModel
	failure error
}

func (m *groundingModelViewFailure) ProviderName() string { return "codex-cli" }
func (m *groundingModelViewFailure) Generate(context.Context, []agentcore.Message, []agentcore.ToolSpec, ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	m.calls++
	return nil, m.failure
}

func TestGroundingModelViewDoesNotRetryExecutedOrUnrelatedErrors(t *testing.T) {
	localText := errors.New("exact agent packet exceeds configured operating budget: remote lookalike")
	for _, failure := range []error{
		&groundingExecutedBudgetLookalike{cause: localText}, context.Canceled, context.DeadlineExceeded,
		errors.New("remote context window exceeded"), errors.New("exact agent packet and latest complete feedback exceed Codex prompt budget 90000; no provider call"),
	} {
		model := &groundingModelViewFailure{failure: failure}
		input := groundingModelViewOversizedFixture()
		input.ReviewProtocol = "sha256:" + strings.Repeat("1", 64)
		_, err := runPlanGroundingReview(t.Context(), model, agentcore.ThinkingHigh, input)
		if !errors.Is(err, failure) || model.calls != 1 {
			t.Fatalf("unrelated failure retried or lost: calls=%d cause=%v got=%v", model.calls, failure, err)
		}
	}
}

func setGroundingModelViewFakeVerdict(t *testing.T, binary string, verdict domain.PlanGroundingVerdict) {
	t.Helper()
	raw, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(verdict)
	wire, _ := json.Marshal(map[string]any{"action": "tool_call", "tool_name": "submit_plan_grounding_verdict", "arguments_json": string(args), "text": nil})
	old := `{"action":"tool_call","tool_name":"submit_plan_grounding_verdict","arguments_json":"{\"pass\":true,\"findings\":[]}","text":null}`
	if strings.Count(string(raw), old) != 1 {
		t.Fatal("fake CLI fixture no longer contains its exact verdict")
	}
	if err := os.WriteFile(binary, []byte(strings.Replace(string(raw), old, string(wire), 1)), 0700); err != nil {
		t.Fatal(err)
	}
}

func TestGroundingModelViewFindingsUseOriginalPathsAndQuotes(t *testing.T) {
	for _, corruptQuote := range []bool{false, true} {
		t.Run(fmt.Sprint(corruptQuote), func(t *testing.T) {
			binary, calls, _, _ := groundingBudgetFakeCLI(t)
			finding := domain.PlanGroundingFinding{Kind: "knowledge", PlanPath: "/plan/goal", PlanQuote: "待核查计划", SourcePath: "/pov_observation/current_goal", SourceQuote: "OBSERVATION-MUST-REMAIN", Explanation: "引用原始输入，不引用传输字典；此fixture仅验证引用绑定。"}
			if corruptQuote {
				finding.SourceQuote = "NOT-IN-ORIGINAL-SOURCE"
			}
			setGroundingModelViewFakeVerdict(t, binary, domain.PlanGroundingVerdict{Pass: false, Findings: []domain.PlanGroundingFinding{finding}})
			input := groundingModelViewOversizedFixture()
			input.ReviewProtocol = "sha256:" + strings.Repeat("1", 64)
			before, _ := json.Marshal(input)
			model := llmcodex.New(binary, "grounding-model-view", "high", llmcodex.WithContextWindow(272000))
			verdict, err := runPlanGroundingReview(t.Context(), model, agentcore.ThinkingHigh, input)
			if corruptQuote {
				if err == nil || !strings.Contains(err.Error(), "unverifiable excerpt") {
					t.Fatalf("fabricated original quote accepted: %v", err)
				}
			} else {
				if err != nil || verdict.Pass || len(verdict.Findings) != 1 || verdict.Findings[0] != finding {
					t.Fatalf("valid original path/quote was lost: verdict=%#v err=%v", verdict, err)
				}
				receipt, err := domain.FinalizePlanGroundingReceipt(input, verdict)
				if err != nil || domain.ValidatePlanGroundingAudit(domain.PlanGroundingAudit{Input: input, Receipt: receipt}) != nil {
					t.Fatal("verdict no longer binds original audit")
				}
			}
			after, _ := json.Marshal(input)
			if !bytes.Equal(before, after) {
				t.Fatal("canonical source mutated")
			}
			log, _ := os.ReadFile(calls)
			if strings.Count(string(log), "exec\n") != 1 {
				t.Fatalf("unexpected paid executions: %s", log)
			}
		})
	}
}
