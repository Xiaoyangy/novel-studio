package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/voocel/agentcore"
)

func TestSealedConvergenceReplacementZeroSideEffectAcceptsExactLiveLegacyShape(t *testing.T) {
	st, intent, partialSHA, _ := sealedConvergenceBinaryFailoverFixture(t)
	outcome, err := pipelineSealedConvergenceCaptureReplacementZeroSideEffect(st, intent, partialSHA)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Classification != pipelineSealedConvergenceOutcomeReplacementZero ||
		outcome.SessionRows != 1 || outcome.UserRows != 1 || outcome.AssistantRows != 0 ||
		outcome.ToolRows != 0 || outcome.ToolCallBlocks != 0 || outcome.FormalPlanPresent ||
		outcome.AccessReceiptConsumed || outcome.SessionIdentity != intent.PlannerContinuation.Replacement.SessionIdentity ||
		outcome.UserPromptSHA256 != intent.PlannerContinuation.Replacement.PromptSHA256 ||
		outcome.AccessReceiptDigest != intent.PlannerContinuation.Replacement.AccessReceiptDigest {
		t.Fatalf("replacement proof did not bind exact live-compatible state: %+v", outcome)
	}
	intent.PlannerContinuation.Replacement.ReplacementZeroSideEffect = outcome
	intent.IntentDigest = pipelineSealedConvergenceReplanIntentDigest(intent)
	if !validPipelineSealedConvergencePlannerJournal(intent) {
		t.Fatal("replacement_zero_side_effect extension was not accepted")
	}
	if err := pipelineSealedConvergenceValidateReplacementZeroSideEffectState(st, intent); err != nil {
		t.Fatal(err)
	}
}

func TestSealedConvergenceReplacementZeroSideEffectFailsClosedOnDrift(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*testing.T, *store.Store, pipelineSealedConvergenceReplanIntent, string)
		want   string
	}{
		{
			name: "assistant or tool side effect",
			mutate: func(t *testing.T, st *store.Store, intent pipelineSealedConvergenceReplanIntent, _ string) {
				appendSealedConvergenceSessionMessage(t, st.Dir(), intent.PlannerContinuation.Replacement.SessionIdentity,
					agentcore.Message{Role: agentcore.RoleAssistant, Content: []agentcore.ContentBlock{agentcore.ToolCallBlock(agentcore.ToolCall{ID: "x", Name: "plan_details"})}})
			},
			want: "not zero-side-effect",
		},
		{
			name: "partial drift",
			mutate: func(t *testing.T, st *store.Store, _ pipelineSealedConvergenceReplanIntent, _ string) {
				if err := os.WriteFile(filepath.Join(st.Dir(), "drafts", "05.plan.partial.json"), []byte(`{"drift":true}`), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			want: "partial drift",
		},
		{
			name: "formal plan",
			mutate: func(t *testing.T, st *store.Store, _ pipelineSealedConvergenceReplanIntent, _ string) {
				if err := os.WriteFile(filepath.Join(st.Dir(), "drafts", "05.plan.json"), []byte(`{}`), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			want: "formal plan is present",
		},
		{
			name: "receipt consumed",
			mutate: func(t *testing.T, st *store.Store, _ pipelineSealedConvergenceReplanIntent, token string) {
				receipt, err := st.Runtime.LoadPlanningContextAccessReceipt(domain.PlanningContextAccessPlan)
				if err != nil || receipt == nil {
					t.Fatalf("receipt: %+v %v", receipt, err)
				}
				if err := st.Runtime.ConsumePlanningContextAccessReceipt(*receipt, token, time.Now().UTC()); err != nil {
					t.Fatal(err)
				}
			},
			want: "identity/consumption drift",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st, intent, partialSHA, token := sealedConvergenceBinaryFailoverFixture(t)
			tc.mutate(t, st, intent, token)
			_, err := pipelineSealedConvergenceCaptureReplacementZeroSideEffect(st, intent, partialSHA)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("drift did not fail closed with %q: %v", tc.want, err)
			}
		})
	}
}

func TestSealedConvergenceBinaryFailoverReauditsOriginalSession(t *testing.T) {
	st, intent, partialSHA, _ := sealedConvergenceBinaryFailoverFixture(t)
	outcome, err := pipelineSealedConvergenceCaptureReplacementZeroSideEffect(st, intent, partialSHA)
	if err != nil {
		t.Fatal(err)
	}
	intent.PlannerContinuation.Replacement.ReplacementZeroSideEffect = outcome
	appendSealedConvergenceSessionMessage(t, st.Dir(), intent.PlannerContinuation.SessionIdentity,
		agentcore.Message{Role: agentcore.RoleAssistant, Content: []agentcore.ContentBlock{agentcore.TextBlock("late drift")}})
	if err := pipelineSealedConvergenceValidateReplacementZeroSideEffectState(st, intent); err == nil ||
		!strings.Contains(err.Error(), "original continuation session drift") {
		t.Fatalf("original session drift did not close binary failover: %v", err)
	}
}

func TestSealedConvergenceBinaryProbeRequiresAbsoluteExecutableAndBindsHash(t *testing.T) {
	t.Setenv("NOVEL_STUDIO_CODEX_BINARY", "relative-codex")
	if _, err := pipelineSealedConvergenceProbeFailoverBinary(); err == nil || !strings.Contains(err.Error(), "absolute executable") {
		t.Fatalf("relative binary passed: %v", err)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "codex")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nprintf 'codex-cli test-v1\\n'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NOVEL_STUDIO_CODEX_BINARY", path)
	if _, err := pipelineSealedConvergenceProbeFailoverBinary(); err == nil || !strings.Contains(err.Error(), "executable regular file") {
		t.Fatalf("non-executable binary passed: %v", err)
	}
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatal(err)
	}
	first, err := pipelineSealedConvergenceProbeFailoverBinary()
	if err != nil {
		t.Fatal(err)
	}
	if first.BinaryPath != path || first.BinaryVersionOutput != "codex-cli test-v1" ||
		!first.BinaryHealthProbeSucceeded || first.BinaryHealthProbeTimeoutMS != 5000 ||
		!validPipelineSealedConvergenceDigest(first.BinaryFileSHA256) {
		t.Fatalf("health probe binding incomplete: %+v", first)
	}
	// Replace the executable atomically. Rewriting an inode immediately after
	// Darwin executed it can be terminated by the platform's executable cache,
	// which makes this drift test flaky even though the probe is correct.
	replacementPath := filepath.Join(dir, "codex-v2")
	if err := os.WriteFile(replacementPath, []byte("#!/bin/sh\nprintf 'codex-cli test-v2\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacementPath, path); err != nil {
		t.Fatal(err)
	}
	second, err := pipelineSealedConvergenceProbeFailoverBinary()
	if err != nil {
		t.Fatal(err)
	}
	if pipelineSealedConvergenceSameBinaryProbe(first, second) || first.BinaryFileSHA256 == second.BinaryFileSHA256 {
		t.Fatal("binary content/version drift was not detected")
	}
}

func TestSealedConvergenceBinaryFailoverJournalCrashWindowsAreBounded(t *testing.T) {
	st, intent, partialSHA, _ := sealedConvergenceBinaryFailoverFixture(t)
	outcome, err := pipelineSealedConvergenceCaptureReplacementZeroSideEffect(st, intent, partialSHA)
	if err != nil {
		t.Fatal(err)
	}
	replacement := intent.PlannerContinuation.Replacement
	replacement.ReplacementZeroSideEffect = outcome
	base, err := time.Parse(time.RFC3339Nano, outcome.RecordedAt)
	if err != nil {
		t.Fatal(err)
	}
	failover := &pipelineSealedConvergenceBinaryFailoverDispatch{
		Version:         pipelineSealedConvergenceBinaryFailoverVersion,
		SessionIdentity: "convergence_planner_continuation_binary_failover-ch05",
		ReservedAt:      base.Add(time.Nanosecond).Format(time.RFC3339Nano),
	}
	replacement.BinaryFailover = failover
	intent.IntentDigest = pipelineSealedConvergenceReplanIntentDigest(intent)
	if !validPipelineSealedConvergencePlannerJournal(intent) {
		t.Fatal("crash after binary failover reservation was not resumable")
	}

	failover.BinaryPath = "/opt/novel-studio/codex-cli"
	failover.BinaryFileSHA256 = "sha256:" + strings.Repeat("e", 64)
	failover.BinaryVersionOutput = "codex-cli 1.2.3"
	failover.BinaryHealthProbeSucceeded = true
	failover.BinaryHealthProbeTimeoutMS = 5000
	failover.BinaryHealthProbedAt = base.Add(2 * time.Nanosecond).Format(time.RFC3339Nano)
	intent.IntentDigest = pipelineSealedConvergenceReplanIntentDigest(intent)
	if !validPipelineSealedConvergencePlannerJournal(intent) {
		t.Fatal("crash after binary health probe was not resumable")
	}

	failover.PrefetchLockOwner = "pipeline-convergence-replan-ch000005-pid99999-new"
	failover.PrefetchLockProcessID = 99999
	failover.PrefetchStartedAt = base.Add(3 * time.Nanosecond).Format(time.RFC3339Nano)
	failover.PrefetchBaselineReceiptDigest = outcome.AccessReceiptDigest
	intent.IntentDigest = pipelineSealedConvergenceReplanIntentDigest(intent)
	if !validPipelineSealedConvergencePlannerJournal(intent) {
		t.Fatal("crash after binary prefetch write-ahead was not resumable")
	}

	failover.AccessReceiptDigest = "sha256:" + strings.Repeat("f", 64)
	failover.PromptSHA256 = "sha256:" + strings.Repeat("1", 64)
	failover.Dispatches = 1
	failover.DispatchedAt = base.Add(4 * time.Nanosecond).Format(time.RFC3339Nano)
	intent.IntentDigest = pipelineSealedConvergenceReplanIntentDigest(intent)
	if !validPipelineSealedConvergencePlannerJournal(intent) {
		t.Fatal("binary failover dispatch 1/1 was not a valid terminal journal")
	}
	failover.Dispatches = 2
	intent.IntentDigest = pipelineSealedConvergenceReplanIntentDigest(intent)
	if validPipelineSealedConvergencePlannerJournal(intent) {
		t.Fatal("a fourth dispatch crossed the durable binary failover ceiling")
	}
}

func sealedConvergenceBinaryFailoverFixture(
	t *testing.T,
) (*store.Store, pipelineSealedConvergenceReplanIntent, string, string) {
	t.Helper()
	st, intent, partialSHA, _ := sealedConvergenceZeroSideEffectFixture(t)
	original, err := pipelineSealedConvergenceCaptureZeroSideEffectOutcome(
		st, intent, partialSHA, "", pipelineSealedConvergenceOutcomeLegacyZero, true,
	)
	if err != nil {
		t.Fatal(err)
	}
	continuation := intent.PlannerContinuation
	continuation.InitialAccessReceiptDigest = original.AccessReceiptDigest
	continuation.InitialPromptSHA256 = original.UserPromptSHA256
	continuation.ZeroSideEffectOutcome = original
	originalRecorded, err := time.Parse(time.RFC3339Nano, original.RecordedAt)
	if err != nil {
		t.Fatal(err)
	}

	replacementPrompt := "host-prefetched replacement prompt"
	replacement := &pipelineSealedConvergenceReplacementDispatch{
		Version:                       pipelineSealedConvergenceReplacementDispatchVersion,
		SessionIdentity:               "convergence_planner_continuation_replacement-ch05",
		Dispatches:                    1,
		ReservedAt:                    originalRecorded.Add(time.Nanosecond).Format(time.RFC3339Nano),
		PrefetchLockOwner:             "released-replacement-lock",
		PrefetchLockProcessID:         88888,
		PrefetchStartedAt:             originalRecorded.Add(2 * time.Nanosecond).Format(time.RFC3339Nano),
		PrefetchBaselineReceiptDigest: original.AccessReceiptDigest,
		PromptSHA256:                  pipelineBytesSHA([]byte(replacementPrompt)),
		DispatchedAt:                  originalRecorded.Add(3 * time.Nanosecond).Format(time.RFC3339Nano),
	}
	continuation.Replacement = replacement
	appendSealedConvergenceSessionMessage(t, st.Dir(), replacement.SessionIdentity, agentcore.UserMsg(replacementPrompt))
	token := domain.PlanningContextAccessTokenPrefix + strings.Repeat("d", 64)
	receipt := saveSealedConvergenceTestReceiptWithToken(t, st, intent, replacement.PrefetchLockOwner, replacement.PrefetchLockProcessID, token)
	replacement.AccessReceiptDigest = receipt.ReceiptDigest
	intent.IntentDigest = pipelineSealedConvergenceReplanIntentDigest(intent)
	if !validPipelineSealedConvergencePlannerJournal(intent) {
		t.Fatal("fixture legacy replacement journal is invalid")
	}
	return st, intent, partialSHA, token
}
