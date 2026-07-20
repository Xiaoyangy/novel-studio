package agents

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chenhongyang/novel-studio/assets"
	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/voocel/agentcore"
)

func TestSealedConvergenceContinuationRunsFourBatchesAndStopsOnPlanned(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	oldSessionPath := filepath.Join(st.Dir(), "meta", "sessions", "agents", "writer-ch05.jsonl")
	if err := os.MkdirAll(filepath.Dir(oldSessionPath), 0o755); err != nil {
		t.Fatal(err)
	}
	oldSession := []byte("poisoned-large-failed-writer-session-must-not-be-resumed\n")
	if err := os.WriteFile(oldSessionPath, oldSession, 0o644); err != nil {
		t.Fatal(err)
	}
	responses := make([]agentcore.Message, sealedConvergenceContinuationMaxTurns)
	for i := range responses {
		responses[i] = agentcore.Message{
			Role:       agentcore.RoleAssistant,
			StopReason: agentcore.StopReasonToolUse,
			Content: []agentcore.ContentBlock{agentcore.ToolCallBlock(agentcore.ToolCall{
				ID:   "details-" + string(rune('a'+i)),
				Name: "plan_details",
				Args: json.RawMessage(`{"chapter":5,"causal_simulation":{"context_sources":["context-access:test"]}}`),
			})},
		}
	}
	model := &outlineAllOperationCaptureModel{responses: responses}
	executions := 0
	planDetails := agentcore.NewFuncTool(
		"plan_details",
		"test details",
		map[string]any{"type": "object"},
		func(_ context.Context, _ json.RawMessage) (json.RawMessage, error) {
			executions++
			if executions < sealedConvergenceContinuationMaxTurns {
				return json.RawMessage(`{"staged":"details"}`), nil
			}
			if _, err := st.Checkpoints.Append(
				domain.ChapterScope(5),
				"plan",
				"drafts/05.plan.json",
				"continuation-plan",
			); err != nil {
				return nil, err
			}
			return json.RawMessage(`{"planned":true}`), nil
		},
	)
	cfg := bootstrap.Config{Roles: map[string]bootstrap.RoleConfig{
		"writer": {MaxTurns: 30},
	}}
	err := runSealedConvergencePlannerContinuationWithModel(
		context.Background(),
		cfg,
		assets.Bundle{Prompts: assets.Prompts{Planner: "PLANNER"}},
		st,
		5,
		"HOST PREFETCHED CONTEXT",
		model,
		"writer-provider",
		"writer-model",
		planDetails,
	)
	if err != nil {
		t.Fatal(err)
	}
	if model.calls != sealedConvergenceContinuationMaxTurns || executions != sealedConvergenceContinuationMaxTurns {
		t.Fatalf("four-batch ceiling/terminal stop drifted: model=%d tools=%d", model.calls, executions)
	}
	for _, spec := range model.tools {
		if spec.Name != "plan_details" {
			t.Fatalf("continuation exposed capability %q", spec.Name)
		}
	}
	sessionPath := filepath.Join(st.Dir(), "meta", "sessions", "agents", "convergence_planner_continuation-ch05.jsonl")
	if _, err := os.Stat(sessionPath); err != nil {
		t.Fatalf("fresh continuation sidecar session was not persisted: %v", err)
	}
	if after, err := os.ReadFile(oldSessionPath); err != nil || string(after) != string(oldSession) {
		t.Fatalf("continuation touched the failed writer transcript: after=%q err=%v", after, err)
	}
}

func TestSealedConvergenceContinuationDeterministicStateBindingFailsFast(t *testing.T) {
	for _, message := range []string{
		"plan_details 缺少 novel_context 返回的 exact project-all-state authoritative source token",
		"sealed convergence authority overlay rejected plan_details project-all-state injection",
		"stored project-all authority receipt invalid: active project-all authority binding no longer matches receipt",
	} {
		if !sealedConvergenceContinuationDeterministicError(errors.New(message)) {
			t.Fatalf("deterministic binding error was not classified: %s", message)
		}
	}
	if sealedConvergenceContinuationDeterministicError(errors.New("plan_details missing render_capacity")) {
		t.Fatal("repairable plan gap was made fatal")
	}
	if sealedConvergenceContinuationMaxTurns != 4 ||
		sealedConvergenceContinuationMaxRetries != 1 ||
		sealedConvergenceContinuationMaxErrors != 2 ||
		sealedConvergenceContinuationTimeout.String() != "8m0s" ||
		sealedConvergenceContinuationCallTimeout.String() != "3m0s" {
		t.Fatal("continuation liveness ceilings drifted")
	}
	if strings.Contains(sealedConvergenceContinuationBoundary, "plan_structure 保存") ||
		!strings.Contains(sealedConvergenceContinuationBoundary, "禁止调用或重做 plan_structure") {
		t.Fatal("continuation boundary reopened plan_structure")
	}
}

func TestSealedConvergenceReplacementEligibilityRequiresTypedTimeoutAndNoObservedActivity(t *testing.T) {
	typed := &ModelCallTimeoutError{
		Role: "convergence_planner_continuation", Timeout: 3 * time.Minute, Cause: context.DeadlineExceeded,
	}
	zero := &sealedConvergencePlannerRunError{cause: typed}
	if !SealedConvergencePlannerTimedOutBeforeSideEffects(zero) {
		t.Fatal("typed timeout with an empty activity trace was rejected")
	}
	for _, activity := range []SealedConvergencePlannerRunActivity{
		{AssistantMessageStarts: 1},
		{ToolMessageStarts: 1},
		{ToolExecutions: 1},
	} {
		err := &sealedConvergencePlannerRunError{cause: typed, activity: activity}
		if SealedConvergencePlannerTimedOutBeforeSideEffects(err) {
			t.Fatalf("observed assistant/tool activity was classified zero-side-effect: %+v", activity)
		}
	}
	if SealedConvergencePlannerTimedOutBeforeSideEffects(
		&sealedConvergencePlannerRunError{cause: errors.New("model call timeout")},
	) {
		t.Fatal("timeout-shaped text acquired replacement authority")
	}
}

func TestSealedConvergenceContinuationAbortsOnFirstDeterministicToolError(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	responses := make([]agentcore.Message, sealedConvergenceContinuationMaxTurns)
	for i := range responses {
		responses[i] = agentcore.Message{
			Role:       agentcore.RoleAssistant,
			StopReason: agentcore.StopReasonToolUse,
			Content: []agentcore.ContentBlock{agentcore.ToolCallBlock(agentcore.ToolCall{
				ID:   "binding-" + string(rune('a'+i)),
				Name: "plan_details",
				Args: json.RawMessage(`{"chapter":5}`),
			})},
		}
	}
	model := &outlineAllOperationCaptureModel{responses: responses}
	executions := 0
	planDetails := agentcore.NewFuncTool(
		"plan_details",
		"test details",
		map[string]any{"type": "object"},
		func(_ context.Context, _ json.RawMessage) (json.RawMessage, error) {
			executions++
			return nil, errors.New("plan_details 缺少 novel_context 返回的 exact project-all-state authoritative source token")
		},
	)
	started := time.Now()
	err := runSealedConvergencePlannerContinuationWithModel(
		context.Background(),
		bootstrap.Config{},
		assets.Bundle{Prompts: assets.Prompts{Planner: "PLANNER"}},
		st,
		5,
		"HOST PREFETCHED CONTEXT",
		model,
		"writer-provider",
		"writer-model",
		planDetails,
	)
	if err == nil || !strings.Contains(err.Error(), "deterministic precondition") {
		t.Fatalf("first binding failure did not return terminal error: %v", err)
	}
	if model.calls != 1 || executions != 1 {
		t.Fatalf("deterministic binding failure repeated: model=%d tools=%d", model.calls, executions)
	}
	if time.Since(started) > time.Second {
		t.Fatalf("local deterministic failure did not return promptly: %s", time.Since(started))
	}
}
