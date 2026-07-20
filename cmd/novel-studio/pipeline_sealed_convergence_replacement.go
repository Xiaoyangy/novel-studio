package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/chenhongyang/novel-studio/assets"
	"github.com/chenhongyang/novel-studio/internal/agents"
	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/tools"
)

const (
	pipelineSealedConvergenceZeroSideEffectOutcomeVersion  = "sealed-convergence-zero-side-effect-outcome.v1"
	pipelineSealedConvergenceReplacementDispatchVersion    = "sealed-convergence-replacement-dispatch.v1"
	pipelineSealedConvergenceReplacementZeroOutcomeVersion = "sealed-convergence-replacement-zero-side-effect.v1"
	pipelineSealedConvergenceBinaryFailoverVersion         = "sealed-convergence-binary-failover-dispatch.v1"
	pipelineSealedConvergenceBinaryZeroOutcomeVersion      = "sealed-convergence-binary-zero-side-effect.v1"
	pipelineSealedConvergenceSeededCompactVersion          = "sealed-convergence-seeded-compact-finalize.v1"

	pipelineSealedConvergenceOutcomeTypedTimeout    = "typed_model_call_timeout"
	pipelineSealedConvergenceOutcomeLegacyZero      = "legacy_v1_zero_side_effect_dispatch"
	pipelineSealedConvergenceOutcomeReplacementZero = "replacement_zero_side_effect"
	pipelineSealedConvergenceOutcomeBinaryZero      = "binary_failover_zero_side_effect"
	pipelineSealedConvergenceBinaryProbeTimeout     = 5 * time.Second
	pipelineSealedConvergenceSeededPromptMaxRunes   = 20000
)

// pipelineSealedConvergenceZeroSideEffectOutcome is the atomic durable proof
// that original continuation dispatch 1/1 did not reach any assistant message
// or tool call. The legacy classification is accepted only for the exact v1
// journal shape emitted before typed timeout observations existed.
type pipelineSealedConvergenceZeroSideEffectOutcome struct {
	Version                    string `json:"version"`
	Classification             string `json:"classification"`
	OriginalDispatch           int    `json:"original_dispatch"`
	TypedModelCallTimeout      bool   `json:"typed_model_call_timeout"`
	LegacyV1Inference          bool   `json:"legacy_v1_inference"`
	SessionIdentity            string `json:"session_identity"`
	SessionSHA256              string `json:"session_sha256"`
	SessionRows                int    `json:"session_rows"`
	UserRows                   int    `json:"user_rows"`
	AssistantRows              int    `json:"assistant_rows"`
	ToolRows                   int    `json:"tool_rows"`
	ToolCallBlocks             int    `json:"tool_call_blocks"`
	UserPromptSHA256           string `json:"user_prompt_sha256"`
	PartialSHA256              string `json:"partial_sha256"`
	FormalPlanPresent          bool   `json:"formal_plan_present"`
	AccessReceiptDigest        string `json:"access_receipt_digest"`
	AccessReceiptConsumed      bool   `json:"access_receipt_consumed"`
	AccessReceiptLockOwner     string `json:"access_receipt_lock_owner"`
	AccessReceiptLockProcessID int    `json:"access_receipt_lock_process_id"`
	RecordedAt                 string `json:"recorded_at"`
}

// pipelineSealedConvergenceReplacementDispatch journals the only replacement
// dispatch. PrefetchStarted* is write-ahead state: if the process dies after a
// fresh receipt is issued but before dispatch is committed, the next lock may
// safely overwrite that unconsumed receipt. Once Dispatches becomes 1, no
// state path can issue another replacement.
type pipelineSealedConvergenceReplacementDispatch struct {
	Version                       string                                              `json:"version"`
	SessionIdentity               string                                              `json:"session_identity"`
	Dispatches                    int                                                 `json:"dispatches"`
	ReservedAt                    string                                              `json:"reserved_at"`
	PrefetchLockOwner             string                                              `json:"prefetch_lock_owner,omitempty"`
	PrefetchLockProcessID         int                                                 `json:"prefetch_lock_process_id,omitempty"`
	PrefetchStartedAt             string                                              `json:"prefetch_started_at,omitempty"`
	PrefetchBaselineReceiptDigest string                                              `json:"prefetch_baseline_receipt_digest,omitempty"`
	AccessReceiptDigest           string                                              `json:"access_receipt_digest,omitempty"`
	PromptSHA256                  string                                              `json:"prompt_sha256,omitempty"`
	DispatchedAt                  string                                              `json:"dispatched_at,omitempty"`
	ReplacementZeroSideEffect     *pipelineSealedConvergenceReplacementZeroSideEffect `json:"replacement_zero_side_effect,omitempty"`
	BinaryFailover                *pipelineSealedConvergenceBinaryFailoverDispatch    `json:"binary_failover,omitempty"`
}

// pipelineSealedConvergenceReplacementZeroSideEffect is a separate proof for
// replacement dispatch 1/1. It deliberately cannot be substituted by the
// original continuation outcome.
type pipelineSealedConvergenceReplacementZeroSideEffect struct {
	Version                    string `json:"version"`
	Classification             string `json:"classification"`
	ReplacementDispatch        int    `json:"replacement_dispatch"`
	SessionIdentity            string `json:"session_identity"`
	SessionSHA256              string `json:"session_sha256"`
	SessionRows                int    `json:"session_rows"`
	UserRows                   int    `json:"user_rows"`
	AssistantRows              int    `json:"assistant_rows"`
	ToolRows                   int    `json:"tool_rows"`
	ToolCallBlocks             int    `json:"tool_call_blocks"`
	UserPromptSHA256           string `json:"user_prompt_sha256"`
	PartialSHA256              string `json:"partial_sha256"`
	FormalPlanPresent          bool   `json:"formal_plan_present"`
	AccessReceiptDigest        string `json:"access_receipt_digest"`
	AccessReceiptConsumed      bool   `json:"access_receipt_consumed"`
	AccessReceiptLockOwner     string `json:"access_receipt_lock_owner"`
	AccessReceiptLockProcessID int    `json:"access_receipt_lock_process_id"`
	RecordedAt                 string `json:"recorded_at"`
}

// pipelineSealedConvergenceBinaryFailoverDispatch journals the final and only
// dispatch after both prior CLI invocations were proven to have zero effects.
type pipelineSealedConvergenceBinaryFailoverDispatch struct {
	Version                       string                                          `json:"version"`
	SessionIdentity               string                                          `json:"session_identity"`
	Dispatches                    int                                             `json:"dispatches"`
	ReservedAt                    string                                          `json:"reserved_at"`
	BinaryPath                    string                                          `json:"binary_path,omitempty"`
	BinaryFileSHA256              string                                          `json:"binary_file_sha256,omitempty"`
	BinaryVersionOutput           string                                          `json:"binary_version_output,omitempty"`
	BinaryHealthProbeSucceeded    bool                                            `json:"binary_health_probe_succeeded,omitempty"`
	BinaryHealthProbeTimeoutMS    int                                             `json:"binary_health_probe_timeout_ms,omitempty"`
	BinaryHealthProbedAt          string                                          `json:"binary_health_probed_at,omitempty"`
	PrefetchLockOwner             string                                          `json:"prefetch_lock_owner,omitempty"`
	PrefetchLockProcessID         int                                             `json:"prefetch_lock_process_id,omitempty"`
	PrefetchStartedAt             string                                          `json:"prefetch_started_at,omitempty"`
	PrefetchBaselineReceiptDigest string                                          `json:"prefetch_baseline_receipt_digest,omitempty"`
	AccessReceiptDigest           string                                          `json:"access_receipt_digest,omitempty"`
	PromptSHA256                  string                                          `json:"prompt_sha256,omitempty"`
	DispatchedAt                  string                                          `json:"dispatched_at,omitempty"`
	ZeroSideEffectOutcome         *pipelineSealedConvergenceBinaryZeroSideEffect  `json:"zero_side_effect_outcome,omitempty"`
	SeededCompactFinalize         *pipelineSealedConvergenceSeededCompactFinalize `json:"seeded_compact_finalize,omitempty"`
}

// pipelineSealedConvergenceBinaryZeroSideEffect proves that the health-probed
// binary failover also terminated before producing an assistant row or tool
// call. It is deliberately distinct from the original and replacement proofs;
// none of the three dispatch ceilings can be reset or reused.
type pipelineSealedConvergenceBinaryZeroSideEffect struct {
	Version                    string `json:"version"`
	Classification             string `json:"classification"`
	BinaryDispatch             int    `json:"binary_dispatch"`
	SessionIdentity            string `json:"session_identity"`
	SessionSHA256              string `json:"session_sha256"`
	SessionRows                int    `json:"session_rows"`
	UserRows                   int    `json:"user_rows"`
	AssistantRows              int    `json:"assistant_rows"`
	ToolRows                   int    `json:"tool_rows"`
	ToolCallBlocks             int    `json:"tool_call_blocks"`
	UserPromptSHA256           string `json:"user_prompt_sha256"`
	PartialSHA256              string `json:"partial_sha256"`
	FormalPlanPresent          bool   `json:"formal_plan_present"`
	AccessReceiptDigest        string `json:"access_receipt_digest"`
	AccessReceiptConsumed      bool   `json:"access_receipt_consumed"`
	AccessReceiptLockOwner     string `json:"access_receipt_lock_owner"`
	AccessReceiptLockProcessID int    `json:"access_receipt_lock_process_id"`
	RecordedAt                 string `json:"recorded_at"`
}

// pipelineSealedConvergenceSeededCompactFinalize journals the final bounded
// recovery lane. SeedToolDispatches is a write-ahead reservation for one Host
// invocation of the real plan_details tool with finalize=false. ModelDispatches
// is the independent one-shot compact Planner session; it never reopens any of
// the three prior model dispatch counters.
type pipelineSealedConvergenceSeededCompactFinalize struct {
	Version                       string   `json:"version"`
	SessionIdentity               string   `json:"session_identity"`
	AllowedMutableKeys            []string `json:"allowed_mutable_keys"`
	ReservedAt                    string   `json:"reserved_at"`
	InitialPartialSHA256          string   `json:"initial_partial_sha256"`
	ImmutableSeedDigest           string   `json:"immutable_seed_digest,omitempty"`
	PrefetchLockOwner             string   `json:"prefetch_lock_owner,omitempty"`
	PrefetchLockProcessID         int      `json:"prefetch_lock_process_id,omitempty"`
	PrefetchStartedAt             string   `json:"prefetch_started_at,omitempty"`
	PrefetchBaselineReceiptDigest string   `json:"prefetch_baseline_receipt_digest,omitempty"`
	AccessReceiptDigest           string   `json:"access_receipt_digest,omitempty"`
	SeedArgsSHA256                string   `json:"seed_args_sha256,omitempty"`
	SeedToolDispatches            int      `json:"seed_tool_dispatches"`
	SeedInvokedAt                 string   `json:"seed_invoked_at,omitempty"`
	SeedPartialSHA256             string   `json:"seed_partial_sha256,omitempty"`
	SeededAt                      string   `json:"seeded_at,omitempty"`
	PromptSHA256                  string   `json:"prompt_sha256,omitempty"`
	PromptRunes                   int      `json:"prompt_runes,omitempty"`
	ModelDispatches               int      `json:"model_dispatches"`
	DispatchedAt                  string   `json:"dispatched_at,omitempty"`
}

type pipelineSealedConvergenceSessionAudit struct {
	SHA256           string
	Rows             int
	UserRows         int
	AssistantRows    int
	ToolRows         int
	ToolCallBlocks   int
	UserPromptSHA256 string
}

// pipelineSealedConvergenceRecordTypedTimeoutOutcome persists the proof before
// returning the first typed timeout to the caller. It is deliberately separate
// from replacement dispatch: the current execution lock must be released and
// a later invocation must acquire a fresh lock before replacement prefetch.
func pipelineSealedConvergenceRecordTypedTimeoutOutcome(
	st *store.Store,
	intent *pipelineSealedConvergenceReplanIntent,
	eligibility *pipelineSealedConvergenceReplanEligibility,
	partialSHA string,
	runErr error,
) error {
	if st == nil || intent == nil || eligibility == nil ||
		!agents.SealedConvergencePlannerTimedOutBeforeSideEffects(runErr) {
		return fmt.Errorf("sealed convergence replacement requires a typed zero-side-effect model-call timeout")
	}
	continuation := intent.PlannerContinuation
	if continuation == nil || continuation.Dispatches != 1 ||
		continuation.ZeroSideEffectOutcome != nil || continuation.Replacement != nil {
		return fmt.Errorf("sealed convergence typed timeout journal is not in the original dispatch window")
	}
	outcome, err := pipelineSealedConvergenceCaptureZeroSideEffectOutcome(
		st,
		*intent,
		partialSHA,
		continuation.InitialAccessReceiptDigest,
		pipelineSealedConvergenceOutcomeTypedTimeout,
		false,
	)
	if err != nil {
		return err
	}
	if outcome.UserPromptSHA256 != continuation.InitialPromptSHA256 {
		return fmt.Errorf("sealed convergence typed timeout user prompt binding drift")
	}
	continuation.ZeroSideEffectOutcome = outcome
	return saveUpdatedPipelineSealedConvergenceReplanIntent(st.Dir(), intent, eligibility)
}

// pipelineSealedConvergenceDispatchZeroSideEffectReplacement handles both the
// already-observed typed timeout and the one backward-compatible live v1
// dispatch. It never changes PlannerRepairAttempts or the original Dispatches.
func pipelineSealedConvergenceDispatchZeroSideEffectReplacement(
	cfg bootstrap.Config,
	promptBundle assets.Bundle,
	st *store.Store,
	chapter int,
	intent *pipelineSealedConvergenceReplanIntent,
	eligibility *pipelineSealedConvergenceReplanEligibility,
	partialSHA string,
) error {
	if st == nil || intent == nil || eligibility == nil || chapter <= 0 {
		return fmt.Errorf("sealed convergence replacement identity is incomplete")
	}
	continuation := intent.PlannerContinuation
	if continuation == nil || continuation.Dispatches != 1 ||
		!pipelineSealedConvergenceRecoveryPartialMatches(continuation, partialSHA) {
		return pipelineSealedConvergenceContinuationConsumedStoreError(st, *intent, partialSHA)
	}
	if continuation.Replacement != nil && continuation.Replacement.Dispatches != 0 {
		return pipelineSealedConvergenceDispatchBinaryFailover(
			cfg, promptBundle, st, chapter, intent, eligibility, partialSHA,
		)
	}

	if continuation.ZeroSideEffectOutcome == nil {
		// Backward compatibility is intentionally exact: only a dispatched v1
		// continuation which predates receipt/prompt bindings may use filesystem
		// inference. New journals must carry the typed outcome written by the
		// process that observed the timeout.
		if continuation.Version != pipelineSealedConvergencePlannerContinuationVersion ||
			continuation.InitialAccessReceiptDigest != "" || continuation.InitialPromptSHA256 != "" ||
			continuation.Replacement != nil {
			return pipelineSealedConvergenceContinuationConsumedStoreError(st, *intent, partialSHA)
		}
		outcome, err := pipelineSealedConvergenceCaptureZeroSideEffectOutcome(
			st,
			*intent,
			partialSHA,
			"",
			pipelineSealedConvergenceOutcomeLegacyZero,
			true,
		)
		if err != nil {
			return fmt.Errorf("sealed convergence legacy zero-side-effect inference rejected: %w", err)
		}
		continuation.InitialAccessReceiptDigest = outcome.AccessReceiptDigest
		continuation.InitialPromptSHA256 = outcome.UserPromptSHA256
		continuation.ZeroSideEffectOutcome = outcome
		if err := saveUpdatedPipelineSealedConvergenceReplanIntent(st.Dir(), intent, eligibility); err != nil {
			return fmt.Errorf("sealed convergence persist legacy zero-side-effect outcome: %w", err)
		}
	}
	if err := pipelineSealedConvergenceValidateZeroSideEffectOutcomeState(st, *intent); err != nil {
		return err
	}

	replacement := continuation.Replacement
	if replacement == nil {
		replacement = &pipelineSealedConvergenceReplacementDispatch{
			Version:         pipelineSealedConvergenceReplacementDispatchVersion,
			SessionIdentity: fmt.Sprintf("convergence_planner_continuation_replacement-ch%02d", chapter),
			ReservedAt:      time.Now().UTC().Format(time.RFC3339Nano),
		}
		if err := pipelineSealedConvergenceRequireSessionAbsent(st.Dir(), replacement.SessionIdentity); err != nil {
			return err
		}
		continuation.Replacement = replacement
		if err := saveUpdatedPipelineSealedConvergenceReplanIntent(st.Dir(), intent, eligibility); err != nil {
			return fmt.Errorf("sealed convergence reserve replacement 1/1: %w", err)
		}
	} else if err := pipelineSealedConvergenceRequireSessionAbsent(st.Dir(), replacement.SessionIdentity); err != nil {
		return err
	}

	lock, err := pipelineSealedConvergenceCurrentReplacementLock(st, chapter)
	if err != nil {
		return err
	}
	baselineReceipt, err := st.Runtime.LoadPlanningContextAccessReceipt(domain.PlanningContextAccessPlan)
	if err != nil || baselineReceipt == nil || !baselineReceipt.ConsumedAt.IsZero() ||
		baselineReceipt.Chapter != intent.SourceFrozen.Chapter ||
		baselineReceipt.GenerationID != intent.SourceFrozen.PlanningGenerationID {
		return fmt.Errorf("sealed convergence replacement prefetch baseline receipt is unavailable, consumed, or drifted: %w", err)
	}
	// Write ahead of prefetch. A crash after the receipt write but before the
	// dispatch write leaves Dispatches=0 and the exact issuing lock identity,
	// allowing the next process to verify and replace only that unconsumed receipt.
	replacement.PrefetchLockOwner = lock.Owner
	replacement.PrefetchLockProcessID = lock.ProcessID
	replacement.PrefetchStartedAt = time.Now().UTC().Format(time.RFC3339Nano)
	replacement.PrefetchBaselineReceiptDigest = baselineReceipt.ReceiptDigest
	if err := saveUpdatedPipelineSealedConvergenceReplanIntent(st.Dir(), intent, eligibility); err != nil {
		return fmt.Errorf("sealed convergence persist replacement prefetch write-ahead: %w", err)
	}

	planningContextRaw, err := pipelineSealedConvergencePrefetchPlanningContext(
		st,
		promptBundle,
		cfg.Style,
		chapter,
		intent.ChapterWordBounds,
	)
	if err != nil {
		return err
	}
	if err := pipelineSealedConvergenceValidateStaticZeroSideEffectState(st, *intent); err != nil {
		return fmt.Errorf("sealed convergence replacement prefetch changed protected state: %w", err)
	}
	if err := pipelineSealedConvergenceRequireSessionAbsent(st.Dir(), replacement.SessionIdentity); err != nil {
		return err
	}
	receipt, err := pipelineSealedConvergenceCurrentUnconsumedPlanReceipt(st, *intent, lock)
	if err != nil {
		return fmt.Errorf("sealed convergence replacement planning receipt: %w", err)
	}
	steer := pipelineSealedConvergenceReplanTask(eligibility) + fmt.Sprintf(
		"\n这是原 continuation 经 Host 证明零副作用后的唯一 replacement dispatch 1/1；原 dispatch 与 Planner attempt 计数均不得重置。drafts/%02d.plan.partial.json（sha256=%s）仍是唯一 staged truth。禁止 plan_structure，只调用 plan_details。Host 已在本进程新锁下重新预取唯一 novel_context(chapter=%d, profile=planning)，把新的 planning_context_access_receipt.source_token 写入 causal_simulation.context_sources，按 chapter_plan_stage.gap_summary 补最小缺口并 finalize：\n<host_prefetched_novel_context>\n%s\n</host_prefetched_novel_context>",
		chapter,
		partialSHA,
		chapter,
		string(planningContextRaw),
	)
	replacement.AccessReceiptDigest = receipt.ReceiptDigest
	replacement.PromptSHA256 = pipelineBytesSHA([]byte(steer))
	replacement.Dispatches = 1
	replacement.DispatchedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if err := saveUpdatedPipelineSealedConvergenceReplanIntent(st.Dir(), intent, eligibility); err != nil {
		return fmt.Errorf("sealed convergence persist replacement dispatch 1/1: %w", err)
	}
	if err := agents.RunSealedConvergencePlannerContinuationReplacement(
		context.Background(),
		cfg,
		promptBundle,
		st.Dir(),
		chapter,
		steer,
	); err != nil {
		return fmt.Errorf("sealed convergence replacement dispatch 1/1 failed: %w", err)
	}
	cp, err := tools.CurrentChapterPlanCausalCheckpoint(store.NewStore(st.Dir()), chapter)
	if err != nil || cp == nil || cp.Seq <= intent.SourceFrozen.PlanCheckpointSeq {
		return fmt.Errorf("sealed convergence replacement returned without a successor plan: %w", err)
	}
	return nil
}

// pipelineSealedConvergenceDispatchBinaryFailover is the terminal recovery
// lane. It is reachable only after replacement dispatch 1/1 itself has an
// exact, durable zero-side-effect proof. It never resets either earlier
// dispatch counter or the paid Planner attempt count.
func pipelineSealedConvergenceDispatchBinaryFailover(
	cfg bootstrap.Config,
	promptBundle assets.Bundle,
	st *store.Store,
	chapter int,
	intent *pipelineSealedConvergenceReplanIntent,
	eligibility *pipelineSealedConvergenceReplanEligibility,
	partialSHA string,
) error {
	if st == nil || intent == nil || eligibility == nil || chapter <= 0 || intent.PlannerContinuation == nil {
		return fmt.Errorf("sealed convergence binary failover identity is incomplete")
	}
	continuation := intent.PlannerContinuation
	replacement := continuation.Replacement
	if continuation.Dispatches != 1 || replacement == nil || replacement.Dispatches != 1 ||
		!pipelineSealedConvergenceRecoveryPartialMatches(continuation, partialSHA) {
		return pipelineSealedConvergenceContinuationConsumedStoreError(st, *intent, partialSHA)
	}
	if replacement.BinaryFailover != nil && replacement.BinaryFailover.Dispatches != 0 {
		return pipelineSealedConvergenceSeededCompactFinalizeRecovery(
			cfg, promptBundle, st, chapter, intent, eligibility, partialSHA,
		)
	}

	if replacement.ReplacementZeroSideEffect == nil {
		outcome, err := pipelineSealedConvergenceCaptureReplacementZeroSideEffect(
			st, *intent, partialSHA,
		)
		if err != nil {
			return fmt.Errorf("sealed convergence replacement zero-side-effect proof rejected: %w", err)
		}
		replacement.ReplacementZeroSideEffect = outcome
		if err := saveUpdatedPipelineSealedConvergenceReplanIntent(st.Dir(), intent, eligibility); err != nil {
			return fmt.Errorf("sealed convergence persist replacement_zero_side_effect outcome: %w", err)
		}
	}
	if err := pipelineSealedConvergenceValidateReplacementZeroSideEffectState(st, *intent); err != nil {
		return err
	}

	failover := replacement.BinaryFailover
	if failover == nil {
		failover = &pipelineSealedConvergenceBinaryFailoverDispatch{
			Version:         pipelineSealedConvergenceBinaryFailoverVersion,
			SessionIdentity: fmt.Sprintf("convergence_planner_continuation_binary_failover-ch%02d", chapter),
			ReservedAt:      time.Now().UTC().Format(time.RFC3339Nano),
		}
		if err := pipelineSealedConvergenceRequireSessionAbsent(st.Dir(), failover.SessionIdentity); err != nil {
			return err
		}
		replacement.BinaryFailover = failover
		if err := saveUpdatedPipelineSealedConvergenceReplanIntent(st.Dir(), intent, eligibility); err != nil {
			return fmt.Errorf("sealed convergence reserve binary failover 1/1: %w", err)
		}
	} else if err := pipelineSealedConvergenceRequireSessionAbsent(st.Dir(), failover.SessionIdentity); err != nil {
		return err
	}

	probe, err := pipelineSealedConvergenceProbeFailoverBinary()
	if err != nil {
		return err
	}
	if failover.BinaryPath == "" {
		failover.BinaryPath = probe.BinaryPath
		failover.BinaryFileSHA256 = probe.BinaryFileSHA256
		failover.BinaryVersionOutput = probe.BinaryVersionOutput
		failover.BinaryHealthProbeSucceeded = true
		failover.BinaryHealthProbeTimeoutMS = probe.BinaryHealthProbeTimeoutMS
		failover.BinaryHealthProbedAt = probe.BinaryHealthProbedAt
		if err := saveUpdatedPipelineSealedConvergenceReplanIntent(st.Dir(), intent, eligibility); err != nil {
			return fmt.Errorf("sealed convergence persist binary health probe: %w", err)
		}
	} else if !pipelineSealedConvergenceSameBinaryProbe(failover, probe) {
		return fmt.Errorf("sealed convergence binary failover executable path/hash/version drift")
	}

	lock, err := pipelineSealedConvergenceCurrentReplacementLock(st, chapter)
	if err != nil {
		return err
	}
	baselineReceipt, err := st.Runtime.LoadPlanningContextAccessReceipt(domain.PlanningContextAccessPlan)
	if err != nil || baselineReceipt == nil || !baselineReceipt.ConsumedAt.IsZero() ||
		baselineReceipt.Chapter != intent.SourceFrozen.Chapter ||
		baselineReceipt.GenerationID != intent.SourceFrozen.PlanningGenerationID {
		return fmt.Errorf("sealed convergence binary failover baseline receipt is unavailable, consumed, or drifted: %w", err)
	}
	failover.PrefetchLockOwner = lock.Owner
	failover.PrefetchLockProcessID = lock.ProcessID
	failover.PrefetchStartedAt = time.Now().UTC().Format(time.RFC3339Nano)
	failover.PrefetchBaselineReceiptDigest = baselineReceipt.ReceiptDigest
	if err := saveUpdatedPipelineSealedConvergenceReplanIntent(st.Dir(), intent, eligibility); err != nil {
		return fmt.Errorf("sealed convergence persist binary failover prefetch write-ahead: %w", err)
	}

	planningContextRaw, err := pipelineSealedConvergencePrefetchPlanningContext(
		st, promptBundle, cfg.Style, chapter, intent.ChapterWordBounds,
	)
	if err != nil {
		return err
	}
	if err := pipelineSealedConvergenceValidateReplacementZeroSideEffectState(st, *intent); err != nil {
		return fmt.Errorf("sealed convergence binary failover prefetch changed protected state: %w", err)
	}
	if err := pipelineSealedConvergenceRequireSessionAbsent(st.Dir(), failover.SessionIdentity); err != nil {
		return err
	}
	receipt, err := pipelineSealedConvergenceCurrentUnconsumedPlanReceipt(st, *intent, lock)
	if err != nil {
		return fmt.Errorf("sealed convergence binary failover planning receipt: %w", err)
	}
	steer := pipelineSealedConvergenceReplanTask(eligibility) + fmt.Sprintf(
		"\n这是 original continuation 1/1 与 replacement 1/1 都经 Host 证明零副作用后的最终 binary_failover dispatch 1/1；三者及 Planner attempt 计数均不得重置。drafts/%02d.plan.partial.json（sha256=%s）仍是唯一 staged truth。禁止 plan_structure，只调用 plan_details。Host 已在本进程新锁下重新预取唯一 novel_context(chapter=%d, profile=planning)，把新的 planning_context_access_receipt.source_token 写入 causal_simulation.context_sources，按 chapter_plan_stage.gap_summary 补最小缺口并 finalize：\n<host_prefetched_novel_context>\n%s\n</host_prefetched_novel_context>",
		chapter, partialSHA, chapter, string(planningContextRaw),
	)
	latestProbe, err := pipelineSealedConvergenceProbeFailoverBinary()
	if err != nil || !pipelineSealedConvergenceSameBinaryProbe(failover, latestProbe) {
		return fmt.Errorf("sealed convergence binary failover executable changed before dispatch: %w", err)
	}
	failover.AccessReceiptDigest = receipt.ReceiptDigest
	failover.PromptSHA256 = pipelineBytesSHA([]byte(steer))
	failover.Dispatches = 1
	failover.DispatchedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if err := saveUpdatedPipelineSealedConvergenceReplanIntent(st.Dir(), intent, eligibility); err != nil {
		return fmt.Errorf("sealed convergence persist binary failover dispatch 1/1: %w", err)
	}
	if err := agents.RunSealedConvergencePlannerContinuationBinaryFailover(
		context.Background(), cfg, promptBundle, st.Dir(), chapter, steer, failover.BinaryPath,
	); err != nil {
		return fmt.Errorf("sealed convergence binary failover dispatch 1/1 failed: %w", err)
	}
	cp, err := tools.CurrentChapterPlanCausalCheckpoint(store.NewStore(st.Dir()), chapter)
	if err != nil || cp == nil || cp.Seq <= intent.SourceFrozen.PlanCheckpointSeq {
		return fmt.Errorf("sealed convergence binary failover returned without a successor plan: %w", err)
	}
	return nil
}

func pipelineSealedConvergenceRecoveryPartialMatches(
	continuation *pipelineSealedConvergencePlannerContinuation,
	partialSHA string,
) bool {
	if continuation == nil || !validPipelineSealedConvergenceDigest(partialSHA) {
		return false
	}
	if continuation.InitialPartialSHA256 == partialSHA {
		return true
	}
	if continuation.Replacement == nil || continuation.Replacement.BinaryFailover == nil ||
		continuation.Replacement.BinaryFailover.SeededCompactFinalize == nil {
		return false
	}
	seeded := continuation.Replacement.BinaryFailover.SeededCompactFinalize
	if seeded.SeedPartialSHA256 != "" {
		return seeded.SeedPartialSHA256 == partialSHA
	}
	// The Host seed tool is write-ahead reserved. A crash after its atomic
	// partial write but before SeedPartialSHA256 is journaled leaves a new,
	// intentionally unknown SHA. Admit only that exact audit phase; recovery
	// must validate the full immutable seed and must never execute the tool a
	// second time.
	return seeded.SeedToolDispatches == 1 && seeded.ModelDispatches == 0
}

func pipelineSealedConvergenceCaptureReplacementZeroSideEffect(
	st *store.Store,
	intent pipelineSealedConvergenceReplanIntent,
	partialSHA string,
) (*pipelineSealedConvergenceReplacementZeroSideEffect, error) {
	continuation := intent.PlannerContinuation
	if st == nil || continuation == nil || continuation.Replacement == nil ||
		continuation.Replacement.Dispatches != 1 || continuation.InitialPartialSHA256 != partialSHA {
		return nil, fmt.Errorf("replacement dispatch/partial binding drift")
	}
	if err := pipelineSealedConvergenceValidateStaticZeroSideEffectState(st, intent); err != nil {
		return nil, err
	}
	if err := pipelineSealedConvergenceValidateOriginalZeroSideEffectEvidence(st, intent); err != nil {
		return nil, err
	}
	replacement := continuation.Replacement
	audit, err := pipelineSealedConvergenceAuditSession(st.Dir(), replacement.SessionIdentity)
	if err != nil {
		return nil, err
	}
	if audit.Rows != 1 || audit.UserRows != 1 || audit.AssistantRows != 0 ||
		audit.ToolRows != 0 || audit.ToolCallBlocks != 0 {
		return nil, fmt.Errorf(
			"replacement session is not zero-side-effect (rows=%d user=%d assistant=%d tool=%d tool_calls=%d)",
			audit.Rows, audit.UserRows, audit.AssistantRows, audit.ToolRows, audit.ToolCallBlocks,
		)
	}
	if audit.UserPromptSHA256 != replacement.PromptSHA256 {
		return nil, fmt.Errorf("replacement user prompt binding drift")
	}
	receipt, err := st.Runtime.LoadPlanningContextAccessReceipt(domain.PlanningContextAccessPlan)
	if err != nil || receipt == nil {
		return nil, fmt.Errorf("replacement plan access receipt unavailable: %w", err)
	}
	if receipt.ReceiptDigest != replacement.AccessReceiptDigest || !receipt.ConsumedAt.IsZero() ||
		receipt.Chapter != intent.SourceFrozen.Chapter ||
		receipt.GenerationID != intent.SourceFrozen.PlanningGenerationID {
		return nil, fmt.Errorf("replacement plan access receipt identity/consumption drift")
	}
	lock, err := pipelineSealedConvergenceCurrentReplacementLock(st, intent.SourceFrozen.Chapter)
	if err != nil {
		return nil, err
	}
	if receipt.LockOwner == lock.Owner {
		return nil, fmt.Errorf("binary failover requires replacement lock to be released and a fresh lock acquired")
	}
	return &pipelineSealedConvergenceReplacementZeroSideEffect{
		Version:                    pipelineSealedConvergenceReplacementZeroOutcomeVersion,
		Classification:             pipelineSealedConvergenceOutcomeReplacementZero,
		ReplacementDispatch:        replacement.Dispatches,
		SessionIdentity:            replacement.SessionIdentity,
		SessionSHA256:              audit.SHA256,
		SessionRows:                audit.Rows,
		UserRows:                   audit.UserRows,
		AssistantRows:              audit.AssistantRows,
		ToolRows:                   audit.ToolRows,
		ToolCallBlocks:             audit.ToolCallBlocks,
		UserPromptSHA256:           audit.UserPromptSHA256,
		PartialSHA256:              partialSHA,
		FormalPlanPresent:          false,
		AccessReceiptDigest:        receipt.ReceiptDigest,
		AccessReceiptConsumed:      false,
		AccessReceiptLockOwner:     receipt.LockOwner,
		AccessReceiptLockProcessID: receipt.LockProcessID,
		RecordedAt:                 time.Now().UTC().Format(time.RFC3339Nano),
	}, nil
}

func pipelineSealedConvergenceValidateReplacementZeroSideEffectState(
	st *store.Store,
	intent pipelineSealedConvergenceReplanIntent,
) error {
	if err := pipelineSealedConvergenceValidateStaticZeroSideEffectState(st, intent); err != nil {
		return err
	}
	if err := pipelineSealedConvergenceValidateReplacementZeroSideEffectEvidence(st, intent); err != nil {
		return err
	}
	replacement := intent.PlannerContinuation.Replacement
	outcome := replacement.ReplacementZeroSideEffect
	receipt, err := st.Runtime.LoadPlanningContextAccessReceipt(domain.PlanningContextAccessPlan)
	if err != nil || receipt == nil || !receipt.ConsumedAt.IsZero() {
		return fmt.Errorf("sealed convergence replacement zero-side-effect receipt unavailable or consumed: %w", err)
	}
	if receipt.ReceiptDigest == outcome.AccessReceiptDigest {
		return nil
	}
	failover := replacement.BinaryFailover
	if failover == nil || failover.Dispatches != 0 || failover.PrefetchLockOwner == "" ||
		receipt.Chapter != intent.SourceFrozen.Chapter ||
		receipt.GenerationID != intent.SourceFrozen.PlanningGenerationID {
		return fmt.Errorf("sealed convergence replacement receipt drift outside a persisted binary prefetch crash window")
	}
	if receipt.ReceiptDigest == failover.PrefetchBaselineReceiptDigest {
		return nil
	}
	if receipt.LockOwner != failover.PrefetchLockOwner ||
		receipt.LockProcessID != failover.PrefetchLockProcessID {
		return fmt.Errorf("sealed convergence replacement receipt drift outside a persisted binary prefetch crash window")
	}
	return nil
}

// pipelineSealedConvergenceValidateReplacementZeroSideEffectEvidence checks
// the immutable proof rows without requiring the historical replacement
// receipt to remain current. Later bounded lanes necessarily supersede that
// receipt under a fresh execution lock.
func pipelineSealedConvergenceValidateReplacementZeroSideEffectEvidence(
	st *store.Store,
	intent pipelineSealedConvergenceReplanIntent,
) error {
	if err := pipelineSealedConvergenceValidateOriginalZeroSideEffectEvidence(st, intent); err != nil {
		return err
	}
	continuation := intent.PlannerContinuation
	if continuation == nil || continuation.Replacement == nil ||
		continuation.Replacement.ReplacementZeroSideEffect == nil {
		return fmt.Errorf("sealed convergence replacement_zero_side_effect outcome is absent")
	}
	replacement := continuation.Replacement
	outcome := replacement.ReplacementZeroSideEffect
	if outcome.Version != pipelineSealedConvergenceReplacementZeroOutcomeVersion ||
		outcome.Classification != pipelineSealedConvergenceOutcomeReplacementZero ||
		outcome.ReplacementDispatch != 1 || outcome.SessionIdentity != replacement.SessionIdentity ||
		outcome.UserPromptSHA256 != replacement.PromptSHA256 ||
		outcome.PartialSHA256 != continuation.InitialPartialSHA256 ||
		outcome.AccessReceiptDigest != replacement.AccessReceiptDigest || outcome.FormalPlanPresent ||
		outcome.AccessReceiptConsumed || outcome.SessionRows != 1 || outcome.UserRows != 1 ||
		outcome.AssistantRows != 0 || outcome.ToolRows != 0 || outcome.ToolCallBlocks != 0 {
		return fmt.Errorf("sealed convergence replacement_zero_side_effect journal binding drift")
	}
	audit, err := pipelineSealedConvergenceAuditSession(st.Dir(), outcome.SessionIdentity)
	if err != nil {
		return err
	}
	if audit.SHA256 != outcome.SessionSHA256 || audit.Rows != outcome.SessionRows ||
		audit.UserRows != outcome.UserRows || audit.AssistantRows != outcome.AssistantRows ||
		audit.ToolRows != outcome.ToolRows || audit.ToolCallBlocks != outcome.ToolCallBlocks ||
		audit.UserPromptSHA256 != outcome.UserPromptSHA256 {
		return fmt.Errorf("sealed convergence replacement session drift after zero-side-effect proof")
	}
	return nil
}

// pipelineSealedConvergenceValidateOriginalZeroSideEffectEvidence re-audits
// the original session without requiring its historical receipt to remain the
// current receipt. The replacement dispatch necessarily superseded that
// receipt, but it must never supersede the immutable session proof.
func pipelineSealedConvergenceValidateOriginalZeroSideEffectEvidence(
	st *store.Store,
	intent pipelineSealedConvergenceReplanIntent,
) error {
	continuation := intent.PlannerContinuation
	if st == nil || continuation == nil || continuation.ZeroSideEffectOutcome == nil {
		return fmt.Errorf("sealed convergence original zero-side-effect evidence is absent")
	}
	outcome := continuation.ZeroSideEffectOutcome
	typed := outcome.Classification == pipelineSealedConvergenceOutcomeTypedTimeout &&
		outcome.TypedModelCallTimeout && !outcome.LegacyV1Inference
	legacy := outcome.Classification == pipelineSealedConvergenceOutcomeLegacyZero &&
		!outcome.TypedModelCallTimeout && outcome.LegacyV1Inference
	if outcome.Version != pipelineSealedConvergenceZeroSideEffectOutcomeVersion || typed == legacy ||
		outcome.OriginalDispatch != 1 || outcome.SessionIdentity != continuation.SessionIdentity ||
		outcome.UserPromptSHA256 != continuation.InitialPromptSHA256 ||
		outcome.PartialSHA256 != continuation.InitialPartialSHA256 ||
		outcome.AccessReceiptDigest != continuation.InitialAccessReceiptDigest || outcome.FormalPlanPresent ||
		outcome.AccessReceiptConsumed || outcome.SessionRows != 1 || outcome.UserRows != 1 ||
		outcome.AssistantRows != 0 || outcome.ToolRows != 0 || outcome.ToolCallBlocks != 0 {
		return fmt.Errorf("sealed convergence original zero-side-effect journal binding drift")
	}
	audit, err := pipelineSealedConvergenceAuditSession(st.Dir(), outcome.SessionIdentity)
	if err != nil {
		return err
	}
	if audit.SHA256 != outcome.SessionSHA256 || audit.Rows != outcome.SessionRows ||
		audit.UserRows != outcome.UserRows || audit.AssistantRows != outcome.AssistantRows ||
		audit.ToolRows != outcome.ToolRows || audit.ToolCallBlocks != outcome.ToolCallBlocks ||
		audit.UserPromptSHA256 != outcome.UserPromptSHA256 {
		return fmt.Errorf("sealed convergence original continuation session drift before binary failover")
	}
	return nil
}

func pipelineSealedConvergenceProbeFailoverBinary() (*pipelineSealedConvergenceBinaryFailoverDispatch, error) {
	path := strings.TrimSpace(os.Getenv("NOVEL_STUDIO_CODEX_BINARY"))
	if path == "" || !filepath.IsAbs(path) {
		return nil, fmt.Errorf("NOVEL_STUDIO_CODEX_BINARY must be an absolute executable file for sealed convergence binary failover")
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return nil, fmt.Errorf("NOVEL_STUDIO_CODEX_BINARY is not an executable regular file: %w", err)
	}
	binary, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("hash NOVEL_STUDIO_CODEX_BINARY: %w", err)
	}
	hasher := sha256.New()
	_, hashErr := io.Copy(hasher, binary)
	closeErr := binary.Close()
	if hashErr != nil {
		return nil, fmt.Errorf("hash NOVEL_STUDIO_CODEX_BINARY: %w", hashErr)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close NOVEL_STUDIO_CODEX_BINARY after hash: %w", closeErr)
	}
	binarySHA256 := fmt.Sprintf("sha256:%x", hasher.Sum(nil))
	ctx, cancel := context.WithTimeout(context.Background(), pipelineSealedConvergenceBinaryProbeTimeout)
	defer cancel()
	output, runErr := exec.CommandContext(ctx, path, "--version").CombinedOutput()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return nil, fmt.Errorf("NOVEL_STUDIO_CODEX_BINARY --version health probe exceeded hard 5s limit")
	}
	if runErr != nil {
		return nil, fmt.Errorf("NOVEL_STUDIO_CODEX_BINARY --version health probe failed: %w", runErr)
	}
	versionOutput := strings.TrimSpace(string(output))
	if versionOutput == "" || len(versionOutput) > 4096 {
		return nil, fmt.Errorf("NOVEL_STUDIO_CODEX_BINARY --version health probe returned an empty or oversized result")
	}
	return &pipelineSealedConvergenceBinaryFailoverDispatch{
		BinaryPath:                 path,
		BinaryFileSHA256:           binarySHA256,
		BinaryVersionOutput:        versionOutput,
		BinaryHealthProbeSucceeded: true,
		BinaryHealthProbeTimeoutMS: int(pipelineSealedConvergenceBinaryProbeTimeout / time.Millisecond),
		BinaryHealthProbedAt:       time.Now().UTC().Format(time.RFC3339Nano),
	}, nil
}

func pipelineSealedConvergenceSameBinaryProbe(
	journal *pipelineSealedConvergenceBinaryFailoverDispatch,
	probe *pipelineSealedConvergenceBinaryFailoverDispatch,
) bool {
	return journal != nil && probe != nil && journal.BinaryHealthProbeSucceeded &&
		journal.BinaryHealthProbeTimeoutMS == int(pipelineSealedConvergenceBinaryProbeTimeout/time.Millisecond) &&
		journal.BinaryPath == probe.BinaryPath && journal.BinaryFileSHA256 == probe.BinaryFileSHA256 &&
		journal.BinaryVersionOutput == probe.BinaryVersionOutput
}

func pipelineSealedConvergenceCaptureZeroSideEffectOutcome(
	st *store.Store,
	intent pipelineSealedConvergenceReplanIntent,
	partialSHA string,
	expectedReceiptDigest string,
	classification string,
	requirePriorLock bool,
) (*pipelineSealedConvergenceZeroSideEffectOutcome, error) {
	continuation := intent.PlannerContinuation
	if st == nil || continuation == nil || continuation.Dispatches != 1 ||
		continuation.InitialPartialSHA256 != partialSHA {
		return nil, fmt.Errorf("zero-side-effect outcome original dispatch/partial binding drift")
	}
	if err := pipelineSealedConvergenceValidateStaticZeroSideEffectState(st, intent); err != nil {
		return nil, err
	}
	audit, err := pipelineSealedConvergenceAuditSession(st.Dir(), continuation.SessionIdentity)
	if err != nil {
		return nil, err
	}
	if audit.Rows != 1 || audit.UserRows != 1 || audit.AssistantRows != 0 ||
		audit.ToolRows != 0 || audit.ToolCallBlocks != 0 {
		return nil, fmt.Errorf(
			"continuation session is not zero-side-effect (rows=%d user=%d assistant=%d tool=%d tool_calls=%d)",
			audit.Rows, audit.UserRows, audit.AssistantRows, audit.ToolRows, audit.ToolCallBlocks,
		)
	}
	receipt, err := st.Runtime.LoadPlanningContextAccessReceipt(domain.PlanningContextAccessPlan)
	if err != nil || receipt == nil {
		return nil, fmt.Errorf("zero-side-effect plan access receipt unavailable: %w", err)
	}
	if receipt.Chapter != intent.SourceFrozen.Chapter ||
		receipt.GenerationID != intent.SourceFrozen.PlanningGenerationID ||
		!receipt.ConsumedAt.IsZero() {
		return nil, fmt.Errorf("zero-side-effect plan access receipt identity/consumption drift")
	}
	if expectedReceiptDigest != "" && receipt.ReceiptDigest != expectedReceiptDigest {
		return nil, fmt.Errorf("zero-side-effect plan access receipt digest drift")
	}
	lock, err := pipelineSealedConvergenceCurrentReplacementLock(st, intent.SourceFrozen.Chapter)
	if err != nil {
		return nil, err
	}
	if requirePriorLock {
		if receipt.LockOwner == lock.Owner {
			return nil, fmt.Errorf("legacy zero-side-effect recovery requires the original lock to be released")
		}
	} else if receipt.LockOwner != lock.Owner || receipt.LockProcessID != lock.ProcessID {
		return nil, fmt.Errorf("typed timeout receipt is not bound to the observing execution lock")
	}
	typed := classification == pipelineSealedConvergenceOutcomeTypedTimeout
	legacy := classification == pipelineSealedConvergenceOutcomeLegacyZero
	if typed == legacy {
		return nil, fmt.Errorf("zero-side-effect outcome classification is invalid")
	}
	return &pipelineSealedConvergenceZeroSideEffectOutcome{
		Version:                    pipelineSealedConvergenceZeroSideEffectOutcomeVersion,
		Classification:             classification,
		OriginalDispatch:           continuation.Dispatches,
		TypedModelCallTimeout:      typed,
		LegacyV1Inference:          legacy,
		SessionIdentity:            continuation.SessionIdentity,
		SessionSHA256:              audit.SHA256,
		SessionRows:                audit.Rows,
		UserRows:                   audit.UserRows,
		AssistantRows:              audit.AssistantRows,
		ToolRows:                   audit.ToolRows,
		ToolCallBlocks:             audit.ToolCallBlocks,
		UserPromptSHA256:           audit.UserPromptSHA256,
		PartialSHA256:              partialSHA,
		FormalPlanPresent:          false,
		AccessReceiptDigest:        receipt.ReceiptDigest,
		AccessReceiptConsumed:      false,
		AccessReceiptLockOwner:     receipt.LockOwner,
		AccessReceiptLockProcessID: receipt.LockProcessID,
		RecordedAt:                 time.Now().UTC().Format(time.RFC3339Nano),
	}, nil
}

func pipelineSealedConvergenceValidateZeroSideEffectOutcomeState(
	st *store.Store,
	intent pipelineSealedConvergenceReplanIntent,
) error {
	if err := pipelineSealedConvergenceValidateStaticZeroSideEffectState(st, intent); err != nil {
		return err
	}
	continuation := intent.PlannerContinuation
	if continuation == nil || continuation.ZeroSideEffectOutcome == nil {
		return fmt.Errorf("sealed convergence zero-side-effect outcome is absent")
	}
	outcome := continuation.ZeroSideEffectOutcome
	audit, err := pipelineSealedConvergenceAuditSession(st.Dir(), outcome.SessionIdentity)
	if err != nil {
		return err
	}
	if audit.SHA256 != outcome.SessionSHA256 || audit.Rows != outcome.SessionRows ||
		audit.UserRows != outcome.UserRows || audit.AssistantRows != outcome.AssistantRows ||
		audit.ToolRows != outcome.ToolRows || audit.ToolCallBlocks != outcome.ToolCallBlocks ||
		audit.UserPromptSHA256 != outcome.UserPromptSHA256 {
		return fmt.Errorf("sealed convergence original continuation session drift after zero-side-effect proof")
	}
	receipt, err := st.Runtime.LoadPlanningContextAccessReceipt(domain.PlanningContextAccessPlan)
	if err != nil || receipt == nil || !receipt.ConsumedAt.IsZero() {
		return fmt.Errorf("sealed convergence zero-side-effect receipt unavailable or consumed: %w", err)
	}
	if receipt.ReceiptDigest == outcome.AccessReceiptDigest {
		return nil
	}
	replacement := continuation.Replacement
	if replacement == nil || replacement.Dispatches != 0 || replacement.PrefetchLockOwner == "" ||
		receipt.Chapter != intent.SourceFrozen.Chapter ||
		receipt.GenerationID != intent.SourceFrozen.PlanningGenerationID {
		return fmt.Errorf("sealed convergence zero-side-effect receipt drift outside a persisted prefetch crash window")
	}
	if receipt.ReceiptDigest == replacement.PrefetchBaselineReceiptDigest {
		return nil
	}
	if receipt.LockOwner != replacement.PrefetchLockOwner ||
		receipt.LockProcessID != replacement.PrefetchLockProcessID {
		return fmt.Errorf("sealed convergence zero-side-effect receipt drift outside a persisted prefetch crash window")
	}
	return nil
}

func pipelineSealedConvergenceValidateStaticZeroSideEffectState(
	st *store.Store,
	intent pipelineSealedConvergenceReplanIntent,
) error {
	if st == nil || intent.PlannerContinuation == nil {
		return fmt.Errorf("sealed convergence zero-side-effect state identity is incomplete")
	}
	partialPath := filepath.Join(st.Dir(), "drafts", fmt.Sprintf("%02d.plan.partial.json", intent.SourceFrozen.Chapter))
	partialRaw, err := os.ReadFile(partialPath)
	if err != nil {
		return fmt.Errorf("sealed convergence zero-side-effect partial unavailable: %w", err)
	}
	if got := pipelineBytesSHA(partialRaw); got != intent.PlannerContinuation.InitialPartialSHA256 {
		return fmt.Errorf("sealed convergence zero-side-effect partial drift: got=%s want=%s", got, intent.PlannerContinuation.InitialPartialSHA256)
	}
	formalPath := filepath.Join(st.Dir(), "drafts", fmt.Sprintf("%02d.plan.json", intent.SourceFrozen.Chapter))
	if _, err := os.Stat(formalPath); err == nil {
		return fmt.Errorf("sealed convergence zero-side-effect proof rejected: formal plan is present")
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("sealed convergence inspect formal plan: %w", err)
	}
	return nil
}

func pipelineSealedConvergenceAuditSession(
	outputDir string,
	sessionIdentity string,
) (pipelineSealedConvergenceSessionAudit, error) {
	var out pipelineSealedConvergenceSessionAudit
	if strings.TrimSpace(sessionIdentity) == "" || strings.ContainsAny(sessionIdentity, `/\\`) {
		return out, fmt.Errorf("sealed convergence session identity is invalid")
	}
	path := filepath.Join(outputDir, "meta", "sessions", "agents", sessionIdentity+".jsonl")
	raw, err := os.ReadFile(path)
	if err != nil {
		return out, fmt.Errorf("sealed convergence zero-side-effect session unavailable: %w", err)
	}
	out.SHA256 = pipelineBytesSHA(raw)
	for _, line := range bytes.Split(raw, []byte{'\n'}) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var row struct {
			Role    string `json:"role"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		}
		if err := json.Unmarshal(line, &row); err != nil {
			return out, fmt.Errorf("sealed convergence session row is unreadable: %w", err)
		}
		out.Rows++
		var userText strings.Builder
		switch row.Role {
		case "user":
			out.UserRows++
		case "assistant":
			out.AssistantRows++
		case "tool":
			out.ToolRows++
		default:
			return out, fmt.Errorf("sealed convergence session contains unexpected role %q", row.Role)
		}
		for _, block := range row.Content {
			if block.Type == "toolCall" || block.Type == "tool_call" {
				out.ToolCallBlocks++
			}
			if row.Role == "user" && block.Type == "text" {
				userText.WriteString(block.Text)
			}
		}
		if row.Role == "user" {
			if out.UserRows > 1 {
				return out, fmt.Errorf("sealed convergence session contains multiple user rows")
			}
			out.UserPromptSHA256 = pipelineBytesSHA([]byte(userText.String()))
		}
	}
	return out, nil
}

func pipelineSealedConvergenceRequireSessionAbsent(outputDir, sessionIdentity string) error {
	if strings.TrimSpace(sessionIdentity) == "" || strings.ContainsAny(sessionIdentity, `/\\`) {
		return fmt.Errorf("sealed convergence replacement session identity is invalid")
	}
	path := filepath.Join(outputDir, "meta", "sessions", "agents", sessionIdentity+".jsonl")
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("sealed convergence replacement fresh session already exists; fail-closed before dispatch")
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("sealed convergence inspect replacement session: %w", err)
	}
	return nil
}

func pipelineSealedConvergenceCurrentReplacementLock(
	st *store.Store,
	chapter int,
) (*domain.PipelineExecutionLock, error) {
	if st == nil || chapter <= 0 {
		return nil, fmt.Errorf("sealed convergence replacement lock identity is incomplete")
	}
	lock, err := st.Runtime.LoadPipelineExecution()
	if err != nil {
		return nil, fmt.Errorf("sealed convergence replacement load execution lock: %w", err)
	}
	if lock == nil || lock.Mode != domain.PipelineExecutionProjectAll ||
		lock.TargetChapter != chapter || lock.ProcessID != os.Getpid() ||
		!strings.HasPrefix(lock.Owner, fmt.Sprintf("pipeline-convergence-replan-ch%06d-pid%d-", chapter, os.Getpid())) {
		return nil, fmt.Errorf("sealed convergence replacement requires the current process exact convergence-replan lock")
	}
	return lock, nil
}

func pipelineSealedConvergenceCurrentUnconsumedPlanReceipt(
	st *store.Store,
	intent pipelineSealedConvergenceReplanIntent,
	lock *domain.PipelineExecutionLock,
) (*domain.PlanningContextAccessReceipt, error) {
	if st == nil || lock == nil {
		return nil, fmt.Errorf("planning receipt lock identity is incomplete")
	}
	receipt, err := st.Runtime.LoadPlanningContextAccessReceipt(domain.PlanningContextAccessPlan)
	if err != nil || receipt == nil {
		return nil, fmt.Errorf("planning receipt unavailable: %w", err)
	}
	if !receipt.ConsumedAt.IsZero() || receipt.Chapter != intent.SourceFrozen.Chapter ||
		receipt.GenerationID != intent.SourceFrozen.PlanningGenerationID ||
		receipt.LockOwner != lock.Owner || receipt.LockProcessID != lock.ProcessID {
		return nil, fmt.Errorf("planning receipt is consumed or not bound to the current replacement lock")
	}
	return receipt, nil
}

func validPipelineSealedConvergenceContinuationExtension(
	intent pipelineSealedConvergenceReplanIntent,
	continuation *pipelineSealedConvergencePlannerContinuation,
) bool {
	if continuation == nil {
		return true
	}
	if (continuation.InitialAccessReceiptDigest == "") != (continuation.InitialPromptSHA256 == "") {
		return false
	}
	if continuation.InitialAccessReceiptDigest != "" &&
		(!validPipelineSealedConvergenceDigest(continuation.InitialAccessReceiptDigest) ||
			!validPipelineSealedConvergenceDigest(continuation.InitialPromptSHA256) || continuation.Dispatches != 1) {
		return false
	}
	outcome := continuation.ZeroSideEffectOutcome
	if outcome == nil {
		return continuation.Replacement == nil
	}
	if outcome.Version != pipelineSealedConvergenceZeroSideEffectOutcomeVersion ||
		outcome.OriginalDispatch != 1 || outcome.SessionIdentity != continuation.SessionIdentity ||
		outcome.PartialSHA256 != continuation.InitialPartialSHA256 || outcome.FormalPlanPresent ||
		outcome.AccessReceiptConsumed || outcome.SessionRows != 1 || outcome.UserRows != 1 ||
		outcome.AssistantRows != 0 || outcome.ToolRows != 0 || outcome.ToolCallBlocks != 0 ||
		!validPipelineSealedConvergenceDigest(outcome.SessionSHA256) ||
		!validPipelineSealedConvergenceDigest(outcome.UserPromptSHA256) ||
		!validPipelineSealedConvergenceDigest(outcome.PartialSHA256) ||
		!validPipelineSealedConvergenceDigest(outcome.AccessReceiptDigest) ||
		strings.TrimSpace(outcome.AccessReceiptLockOwner) == "" || outcome.AccessReceiptLockProcessID <= 0 ||
		outcome.AccessReceiptDigest != continuation.InitialAccessReceiptDigest ||
		outcome.UserPromptSHA256 != continuation.InitialPromptSHA256 {
		return false
	}
	typed := outcome.Classification == pipelineSealedConvergenceOutcomeTypedTimeout &&
		outcome.TypedModelCallTimeout && !outcome.LegacyV1Inference
	legacy := outcome.Classification == pipelineSealedConvergenceOutcomeLegacyZero &&
		!outcome.TypedModelCallTimeout && outcome.LegacyV1Inference
	if typed == legacy {
		return false
	}
	recorded, err := time.Parse(time.RFC3339Nano, outcome.RecordedAt)
	if err != nil || recorded.Before(mustPipelineSealedConvergenceTime(continuation.DispatchedAt)) {
		return false
	}
	replacement := continuation.Replacement
	if replacement == nil {
		return true
	}
	if replacement.Version != pipelineSealedConvergenceReplacementDispatchVersion ||
		replacement.SessionIdentity != fmt.Sprintf("convergence_planner_continuation_replacement-ch%02d", intent.SourceFrozen.Chapter) ||
		replacement.Dispatches < 0 || replacement.Dispatches > 1 {
		return false
	}
	reserved, err := time.Parse(time.RFC3339Nano, replacement.ReservedAt)
	if err != nil || reserved.Before(recorded) {
		return false
	}
	if replacement.PrefetchLockOwner == "" {
		if replacement.PrefetchLockProcessID != 0 || replacement.PrefetchStartedAt != "" ||
			replacement.PrefetchBaselineReceiptDigest != "" {
			return false
		}
	} else if replacement.PrefetchLockProcessID <= 0 {
		return false
	} else if !validPipelineSealedConvergenceDigest(replacement.PrefetchBaselineReceiptDigest) {
		return false
	} else if started, err := time.Parse(time.RFC3339Nano, replacement.PrefetchStartedAt); err != nil || started.Before(reserved) {
		return false
	}
	if replacement.Dispatches == 0 {
		return replacement.AccessReceiptDigest == "" && replacement.PromptSHA256 == "" && replacement.DispatchedAt == "" &&
			replacement.ReplacementZeroSideEffect == nil && replacement.BinaryFailover == nil
	}
	if replacement.PrefetchLockOwner == "" ||
		!validPipelineSealedConvergenceDigest(replacement.AccessReceiptDigest) ||
		!validPipelineSealedConvergenceDigest(replacement.PromptSHA256) {
		return false
	}
	dispatched, err := time.Parse(time.RFC3339Nano, replacement.DispatchedAt)
	if err != nil || dispatched.Before(mustPipelineSealedConvergenceTime(replacement.PrefetchStartedAt)) {
		return false
	}
	return validPipelineSealedConvergenceReplacementRecovery(intent, replacement, dispatched)
}

func validPipelineSealedConvergenceReplacementRecovery(
	intent pipelineSealedConvergenceReplanIntent,
	replacement *pipelineSealedConvergenceReplacementDispatch,
	replacementDispatched time.Time,
) bool {
	if replacement == nil {
		return false
	}
	outcome := replacement.ReplacementZeroSideEffect
	if outcome == nil {
		return replacement.BinaryFailover == nil
	}
	if outcome.Version != pipelineSealedConvergenceReplacementZeroOutcomeVersion ||
		outcome.Classification != pipelineSealedConvergenceOutcomeReplacementZero ||
		outcome.ReplacementDispatch != 1 || outcome.SessionIdentity != replacement.SessionIdentity ||
		outcome.PartialSHA256 != intent.PlannerContinuation.InitialPartialSHA256 || outcome.FormalPlanPresent ||
		outcome.AccessReceiptConsumed || outcome.SessionRows != 1 || outcome.UserRows != 1 ||
		outcome.AssistantRows != 0 || outcome.ToolRows != 0 || outcome.ToolCallBlocks != 0 ||
		!validPipelineSealedConvergenceDigest(outcome.SessionSHA256) ||
		!validPipelineSealedConvergenceDigest(outcome.UserPromptSHA256) ||
		!validPipelineSealedConvergenceDigest(outcome.PartialSHA256) ||
		!validPipelineSealedConvergenceDigest(outcome.AccessReceiptDigest) ||
		outcome.AccessReceiptDigest != replacement.AccessReceiptDigest ||
		outcome.UserPromptSHA256 != replacement.PromptSHA256 ||
		strings.TrimSpace(outcome.AccessReceiptLockOwner) == "" || outcome.AccessReceiptLockProcessID <= 0 {
		return false
	}
	recorded, err := time.Parse(time.RFC3339Nano, outcome.RecordedAt)
	if err != nil || recorded.Before(replacementDispatched) {
		return false
	}
	failover := replacement.BinaryFailover
	if failover == nil {
		return true
	}
	if failover.Version != pipelineSealedConvergenceBinaryFailoverVersion ||
		failover.SessionIdentity != fmt.Sprintf("convergence_planner_continuation_binary_failover-ch%02d", intent.SourceFrozen.Chapter) ||
		failover.Dispatches < 0 || failover.Dispatches > 1 {
		return false
	}
	reserved, err := time.Parse(time.RFC3339Nano, failover.ReservedAt)
	if err != nil || reserved.Before(recorded) {
		return false
	}
	if failover.BinaryPath == "" {
		return failover.Dispatches == 0 && failover.BinaryFileSHA256 == "" &&
			failover.BinaryVersionOutput == "" && !failover.BinaryHealthProbeSucceeded &&
			failover.BinaryHealthProbeTimeoutMS == 0 && failover.BinaryHealthProbedAt == "" &&
			failover.PrefetchLockOwner == "" && failover.PrefetchLockProcessID == 0 &&
			failover.PrefetchStartedAt == "" && failover.PrefetchBaselineReceiptDigest == "" &&
			failover.AccessReceiptDigest == "" && failover.PromptSHA256 == "" && failover.DispatchedAt == "" &&
			failover.ZeroSideEffectOutcome == nil && failover.SeededCompactFinalize == nil
	}
	if strings.TrimSpace(failover.BinaryPath) != failover.BinaryPath || !filepath.IsAbs(failover.BinaryPath) ||
		!validPipelineSealedConvergenceDigest(failover.BinaryFileSHA256) ||
		strings.TrimSpace(failover.BinaryVersionOutput) == "" || len(failover.BinaryVersionOutput) > 4096 ||
		!failover.BinaryHealthProbeSucceeded ||
		failover.BinaryHealthProbeTimeoutMS != int(pipelineSealedConvergenceBinaryProbeTimeout/time.Millisecond) {
		return false
	}
	probed, err := time.Parse(time.RFC3339Nano, failover.BinaryHealthProbedAt)
	if err != nil || probed.Before(reserved) {
		return false
	}
	if failover.PrefetchLockOwner == "" {
		if failover.PrefetchLockProcessID != 0 || failover.PrefetchStartedAt != "" ||
			failover.PrefetchBaselineReceiptDigest != "" {
			return false
		}
	} else if failover.PrefetchLockProcessID <= 0 ||
		!validPipelineSealedConvergenceDigest(failover.PrefetchBaselineReceiptDigest) {
		return false
	} else if started, err := time.Parse(time.RFC3339Nano, failover.PrefetchStartedAt); err != nil || started.Before(probed) {
		return false
	}
	if failover.Dispatches == 0 {
		return failover.AccessReceiptDigest == "" && failover.PromptSHA256 == "" && failover.DispatchedAt == "" &&
			failover.ZeroSideEffectOutcome == nil && failover.SeededCompactFinalize == nil
	}
	if failover.PrefetchLockOwner == "" ||
		!validPipelineSealedConvergenceDigest(failover.AccessReceiptDigest) ||
		!validPipelineSealedConvergenceDigest(failover.PromptSHA256) {
		return false
	}
	dispatched, err := time.Parse(time.RFC3339Nano, failover.DispatchedAt)
	if err != nil || dispatched.Before(mustPipelineSealedConvergenceTime(failover.PrefetchStartedAt)) {
		return false
	}
	return validPipelineSealedConvergenceBinaryRecovery(intent, failover, dispatched)
}

func validPipelineSealedConvergenceBinaryRecovery(
	intent pipelineSealedConvergenceReplanIntent,
	failover *pipelineSealedConvergenceBinaryFailoverDispatch,
	failoverDispatched time.Time,
) bool {
	if failover == nil {
		return false
	}
	outcome := failover.ZeroSideEffectOutcome
	if outcome == nil {
		return failover.SeededCompactFinalize == nil
	}
	if outcome.Version != pipelineSealedConvergenceBinaryZeroOutcomeVersion ||
		outcome.Classification != pipelineSealedConvergenceOutcomeBinaryZero ||
		outcome.BinaryDispatch != 1 || outcome.SessionIdentity != failover.SessionIdentity ||
		outcome.PartialSHA256 != intent.PlannerContinuation.InitialPartialSHA256 || outcome.FormalPlanPresent ||
		outcome.AccessReceiptConsumed || outcome.SessionRows != 1 || outcome.UserRows != 1 ||
		outcome.AssistantRows != 0 || outcome.ToolRows != 0 || outcome.ToolCallBlocks != 0 ||
		!validPipelineSealedConvergenceDigest(outcome.SessionSHA256) ||
		!validPipelineSealedConvergenceDigest(outcome.UserPromptSHA256) ||
		!validPipelineSealedConvergenceDigest(outcome.PartialSHA256) ||
		!validPipelineSealedConvergenceDigest(outcome.AccessReceiptDigest) ||
		outcome.AccessReceiptDigest != failover.AccessReceiptDigest ||
		outcome.UserPromptSHA256 != failover.PromptSHA256 ||
		strings.TrimSpace(outcome.AccessReceiptLockOwner) == "" || outcome.AccessReceiptLockProcessID <= 0 {
		return false
	}
	recorded, err := time.Parse(time.RFC3339Nano, outcome.RecordedAt)
	if err != nil || recorded.Before(failoverDispatched) {
		return false
	}
	return validPipelineSealedConvergenceSeededCompactJournal(intent, failover.SeededCompactFinalize, recorded)
}
