package agents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/llmcodex"
	"github.com/voocel/agentcore"
)

// Transport-only input: this tests exact serialization and budget dispatch,
// not whether a fictitious story qualifies for a real grounding pass.
func groundingBudgetInput(payload string) domain.PlanGroundingInput {
	return domain.PlanGroundingInput{Policy: domain.PlanGroundingActivationPolicy, Plan: domain.ChapterPlan{Chapter: 1, Goal: "PLAN-BEGIN" + payload + "PLAN-END"},
		POVObservation: domain.CharacterObservationPacket{Character: "甲", CurrentGoal: "OBSERVATION-MUST-REMAIN"},
		Activation:     &domain.PlanGroundingActivationSource{Cycles: []domain.PlanGroundingActivationCycle{{Cycle: 1, POVAfter: domain.PlanGroundingPOVState{Location: "ACTUAL-ENDPOINT"}}}}}
}

func groundingBudgetFakeCLI(t *testing.T) (binary, calls, capture, schemaCapture string) {
	t.Helper()
	dir := t.TempDir()
	binary, calls, capture, schemaCapture = filepath.Join(dir, "fake-codex"), filepath.Join(dir, "calls"), filepath.Join(dir, "prompt"), filepath.Join(dir, "schema")
	t.Setenv("GROUNDING_BUDGET_CALLS", calls)
	t.Setenv("GROUNDING_BUDGET_CAPTURE", capture)
	t.Setenv("GROUNDING_BUDGET_SCHEMA", schemaCapture)
	script := `#!/bin/sh
set -eu
if [ "$1" = 'mcp' ]; then
  printf 'inventory\n' >> "$GROUNDING_BUDGET_CALLS"
  printf '[]'
  exit 0
fi
printf 'exec\n' >> "$GROUNDING_BUDGET_CALLS"
out=''
schema=''
while [ "$#" -gt 0 ]; do
  case "$1" in
    -o|--output-last-message) shift; out="$1" ;;
    --output-schema) shift; schema="$1" ;;
  esac
  shift
done
cat > "$GROUNDING_BUDGET_CAPTURE"
cat "$schema" > "$GROUNDING_BUDGET_SCHEMA"
printf '%s\n' '{"type":"turn.completed","usage":{"input_tokens":1234,"cached_input_tokens":0,"output_tokens":56}}'
printf '%s' '{"action":"tool_call","tool_name":"submit_plan_grounding_verdict","arguments_json":"{\"pass\":true,\"findings\":[]}","text":null}' > "$out"
`
	if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	return
}

func TestActivationGroundingUsesConfiguredExactBudgetThroughSnapshotAndAccounting(t *testing.T) {
	for _, decorated := range []bool{false, true} {
		t.Run(fmt.Sprintf("provider_decorator_%t", decorated), func(t *testing.T) {
			binary, calls, capture, schemaCapture := groundingBudgetFakeCLI(t)
			model := llmcodex.New(binary, "grounding-budget-fixture", "", llmcodex.WithContextWindow(200000))
			// The configured route can be named codex; the actual adapter's
			// ProviderName must survive both the pinned snapshot and WAL wrappers.
			models := &bootstrap.ModelSet{Default: bootstrap.NewSwappableModel("codex", "grounding-budget-fixture", model)}
			starts, skips, records := 0, 0, 0
			record := func(role string, message agentcore.AgentMessage) {
				m, ok := message.(agentcore.Message)
				if !ok || role != "plan_grounding" || m.Usage == nil || m.Usage.Input != 1234 || m.Usage.Output != 56 || m.Metadata["codex_usage_source"] != "reported" {
					t.Fatalf("grounding accounting lost real usage or role: %s %#v", role, message)
				}
				records++
			}
			hooks := DirectUsageLifecycle{StartCall: func(_, role string) error {
				starts++
				if role != "plan_grounding" {
					t.Fatalf("wrong lifecycle role %s", role)
				}
				return nil
			}, SkipCall: func(string) error { skips++; return nil }}
			if decorated {
				models.SetAttemptDecorator(func(ctx context.Context, role, provider, name string, raw agentcore.ChatModel) agentcore.ChatModel {
					return NewAuditedUsageModel(ctx, raw, role, provider, name, "test-host", record, hooks)
				})
			}
			reviewer := NewPlanGroundingReviewer(bootstrap.Config{}, models, record)
			reviewer, err := reviewer.ResolveForSimulation(domain.ChapterWorldSimulation{CharacterActivation: &domain.CharacterActivationSimulationBinding{}})
			if err != nil {
				t.Fatal(err)
			}
			input := groundingBudgetInput(strings.Repeat("source-data ", 10000))
			input.ReviewProtocol = reviewer.Protocol
			raw, _ := json.Marshal(input)
			if utf8.RuneCount(raw) <= 82000 {
				t.Fatal("fixture does not cross the old grounding ceiling")
			}
			ctx := WithDirectUsageLifecycle(context.Background(), hooks)
			verdict, err := reviewer.Review(ctx, input)
			if err != nil || !verdict.Pass || starts != 1 || skips != 0 || records != 1 {
				t.Fatalf("complete configured-window review failed or double charged: starts=%d skips=%d records=%d err=%v", starts, skips, records, err)
			}
			prompt, err := os.ReadFile(capture)
			parameters, _ := json.Marshal(planGroundingToolSpec().Parameters)
			if err != nil || !strings.Contains(string(prompt), string(raw)) || strings.Count(string(prompt), string(raw)) != 1 || !strings.Contains(string(prompt), planGroundingPrompt+activationGroundingPrompt) || !strings.Contains(string(prompt), string(parameters)) || strings.Contains(string(prompt), "Codex 入参压缩") {
				t.Fatalf("judge did not receive exact input/system/tools: %v", err)
			}
			responseSchema, err := os.ReadFile(schemaCapture)
			if err != nil || !json.Valid(responseSchema) || !strings.Contains(string(responseSchema), "arguments_json") || !strings.Contains(string(responseSchema), "additionalProperties") {
				t.Fatal("actual output schema was not sent with the complete packet")
			}
			log, _ := os.ReadFile(calls)
			if strings.Count(string(log), "exec\n") != 1 {
				t.Fatalf("grounding spawned multiple fake provider executions: %s", log)
			}
		})
	}
}

func TestActivationGroundingRealBudgetRejectsBeforeAnyCLIAndSkipsUnstartedUsage(t *testing.T) {
	for _, tc := range []struct {
		name    string
		window  int
		payload string
		message string
	}{
		{"configured small window", 64000, strings.Repeat("a", 160000), "configured operating budget"},
		{"unknown window keeps 90k", 0, strings.Repeat("a", 100000), "90000"},
		{"absolute local bytes", 200000, strings.Repeat("a", planGroundingExactInputByteLimit), "local byte limit"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			binary, calls, _, _ := groundingBudgetFakeCLI(t)
			models := &bootstrap.ModelSet{Default: bootstrap.NewSwappableModel("codex-cli", "grounding-budget-fixture", llmcodex.New(binary, "grounding-budget-fixture", "", llmcodex.WithContextWindow(tc.window)))}
			starts, skips, records := 0, 0, 0
			reviewer := NewPlanGroundingReviewer(bootstrap.Config{}, models, func(string, agentcore.AgentMessage) { records++ })
			reviewer, err := reviewer.ResolveForSimulation(domain.ChapterWorldSimulation{CharacterActivation: &domain.CharacterActivationSimulationBinding{}})
			if err != nil {
				t.Fatal(err)
			}
			input := groundingBudgetInput(tc.payload)
			input.ReviewProtocol = reviewer.Protocol
			ctx := WithDirectUsageLifecycle(context.Background(), DirectUsageLifecycle{StartCall: func(string, string) error { starts++; return nil }, SkipCall: func(string) error { skips++; return nil }})
			_, err = reviewer.Review(ctx, input)
			var budget *PlanGroundingInputBudgetError
			var billed interface {
				error
				LLMUsage() (*agentcore.Usage, string)
			}
			if !errors.As(err, &budget) || errors.As(err, &billed) || !strings.Contains(err.Error(), tc.message) || records != 0 || starts != skips {
				t.Fatalf("budget error lost type or manufactured a charged call: starts=%d skips=%d records=%d error=%v", starts, skips, records, err)
			}
			if _, err := os.Stat(calls); !os.IsNotExist(err) {
				t.Fatal("rejected input launched a CLI process (including inventory)")
			}
		})
	}
}

func TestActivationGroundingUnknownProviderAndLegacyKeepConservativeLimits(t *testing.T) {
	probe := &groundingProbeModel{args: `{"pass":true,"findings":[]}`}
	if _, err := runPlanGroundingReview(context.Background(), probe, agentcore.ThinkingHigh, groundingBudgetInput(strings.Repeat("a", 83000))); err == nil || probe.calls != 0 {
		t.Fatal("activation metadata granted an unknown provider an unlimited window")
	}
	binary, calls, _, _ := groundingBudgetFakeCLI(t)
	model := llmcodex.New(binary, "grounding-budget-fixture", "", llmcodex.WithContextWindow(200000))
	for _, size := range []int{44001, 83000} {
		input := domain.PlanGroundingInput{Policy: domain.PlanGroundingPolicyV1, Plan: domain.ChapterPlan{Goal: strings.Repeat("a", size)}}
		_, err := runPlanGroundingReview(context.Background(), model, agentcore.ThinkingHigh, input)
		var budget *PlanGroundingInputBudgetError
		if !errors.As(err, &budget) {
			t.Fatalf("legacy limit disappeared: %v", err)
		}
	}
	if _, err := os.Stat(calls); !os.IsNotExist(err) {
		t.Fatal("legacy over-budget packet reached provider")
	}
}

type groundingExecutedBudgetLookalike struct{ cause error }

func (e *groundingExecutedBudgetLookalike) Error() string                      { return e.cause.Error() }
func (e *groundingExecutedBudgetLookalike) Unwrap() error                      { return e.cause }
func (*groundingExecutedBudgetLookalike) LLMUsage() (*agentcore.Usage, string) { return nil, "unknown" }

func TestGroundingBudgetClassificationPreservesOriginalErrorsAndExecutedFailures(t *testing.T) {
	model := llmcodex.New("/unused", "fixture", "")
	input := groundingBudgetInput("small")
	cause := errors.New("exact agent packet exceeds configured operating budget: no provider call")
	wrapped := classifyPlanGroundingInputBudgetError(model, input, cause)
	var budget *PlanGroundingInputBudgetError
	if !errors.As(wrapped, &budget) || !errors.Is(wrapped, cause) {
		t.Fatal("local rejection lost its exact cause/type")
	}
	for _, err := range []error{context.Canceled, context.DeadlineExceeded, errors.New("remote context budget exceeded"), &groundingExecutedBudgetLookalike{cause: cause}} {
		classified := classifyPlanGroundingInputBudgetError(model, input, err)
		if classified != err || errors.As(classified, &budget) {
			t.Fatalf("nonlocal/consumed failure was reclassified: %v", classified)
		}
	}
}
