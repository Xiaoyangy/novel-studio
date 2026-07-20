package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/chenhongyang/novel-studio/assets"
	"github.com/chenhongyang/novel-studio/internal/agents"
	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/errs"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/tools"
)

const (
	pipelineSealedConvergenceExhaustedSeedVersion       = "sealed-convergence-exhausted-compact-finalize.v2"
	pipelineSealedConvergencePollutedSessionMinBytes    = 196608
	pipelineSealedConvergencePollutedSessionCompactMark = "[session_compact:"
	pipelineSealedConvergenceExhaustedSeedFailureClass  = "auto_finalize_semantic_rejection"
)

// pipelineSealedConvergenceExhaustedSeedFinalize is the final bounded lane for
// a paid attempt that accumulated and compacted a large generic writer audit
// stream. Host first refreshes only immutable/source fields through the real
// plan_details tool, then one distinct two-turn/one-tool compact session may
// replace allowlisted mutable fields and finalize. Neither prior transcripts
// nor retired partials are deleted or reused.
type pipelineSealedConvergenceExhaustedSeedFinalize struct {
	Version                       string                                         `json:"version"`
	PlannerAttempt                int                                            `json:"planner_attempt"`
	SourcePartialSHA256           string                                         `json:"source_partial_sha256"`
	AllowedSeedKeys               []string                                       `json:"allowed_seed_keys"`
	AllowedMutableKeys            []string                                       `json:"allowed_mutable_keys"`
	WriterSessionSHA256           string                                         `json:"writer_session_sha256"`
	WriterSessionBytes            int64                                          `json:"writer_session_bytes"`
	SessionCompactionCount        int                                            `json:"session_compaction_count"`
	SessionIdentity               string                                         `json:"session_identity"`
	BinaryPath                    string                                         `json:"binary_path"`
	BinaryFileSHA256              string                                         `json:"binary_file_sha256"`
	BinaryVersionOutput           string                                         `json:"binary_version_output"`
	ReservedAt                    string                                         `json:"reserved_at"`
	PrefetchLockOwner             string                                         `json:"prefetch_lock_owner,omitempty"`
	PrefetchLockProcessID         int                                            `json:"prefetch_lock_process_id,omitempty"`
	PrefetchStartedAt             string                                         `json:"prefetch_started_at,omitempty"`
	PrefetchBaselineReceiptDigest string                                         `json:"prefetch_baseline_receipt_digest,omitempty"`
	AccessReceiptDigest           string                                         `json:"access_receipt_digest,omitempty"`
	SeedDigest                    string                                         `json:"seed_digest,omitempty"`
	SeedArgsSHA256                string                                         `json:"seed_args_sha256,omitempty"`
	SeedToolDispatches            int                                            `json:"seed_tool_dispatches"`
	SeedInvokedAt                 string                                         `json:"seed_invoked_at,omitempty"`
	SeedPartialSHA256             string                                         `json:"seed_partial_sha256,omitempty"`
	SeedFailureClass              string                                         `json:"seed_failure_class,omitempty"`
	SeededAt                      string                                         `json:"seeded_at,omitempty"`
	PromptSHA256                  string                                         `json:"prompt_sha256,omitempty"`
	PromptRunes                   int                                            `json:"prompt_runes,omitempty"`
	ModelDispatches               int                                            `json:"model_dispatches"`
	DispatchedAt                  string                                         `json:"dispatched_at,omitempty"`
	DisabledToolRecovery          *pipelineSealedConvergenceDisabledToolRecovery `json:"disabled_tool_recovery,omitempty"`
}

const pipelineSealedConvergenceDisabledToolRecoveryVersion = "sealed-convergence-disabled-tool-recovery.v1"

// pipelineSealedConvergenceDisabledToolRecovery binds the exact second tool
// call that agentcore refused before execution after the first semantic error.
// Host refreshes the single-use source receipt, then executes those exact
// durable args once through the ordinary PlanDetailsTool; no model is recalled.
type pipelineSealedConvergenceDisabledToolRecovery struct {
	Version                       string                                          `json:"version"`
	SessionSHA256                 string                                          `json:"session_sha256"`
	ToolCallID                    string                                          `json:"tool_call_id"`
	ArgsSHA256                    string                                          `json:"args_sha256"`
	InitialPartialSHA256          string                                          `json:"initial_partial_sha256"`
	ReservedAt                    string                                          `json:"reserved_at"`
	PrefetchLockOwner             string                                          `json:"prefetch_lock_owner,omitempty"`
	PrefetchLockProcessID         int                                             `json:"prefetch_lock_process_id,omitempty"`
	PrefetchStartedAt             string                                          `json:"prefetch_started_at,omitempty"`
	PrefetchBaselineReceiptDigest string                                          `json:"prefetch_baseline_receipt_digest,omitempty"`
	AccessReceiptDigest           string                                          `json:"access_receipt_digest,omitempty"`
	SourceArgsSHA256              string                                          `json:"source_args_sha256,omitempty"`
	SourceToolDispatches          int                                             `json:"source_tool_dispatches"`
	SourceInvokedAt               string                                          `json:"source_invoked_at,omitempty"`
	SourcePartialSHA256           string                                          `json:"source_partial_sha256,omitempty"`
	SourceSeededAt                string                                          `json:"source_seeded_at,omitempty"`
	PatchToolDispatches           int                                             `json:"patch_tool_dispatches"`
	PatchInvokedAt                string                                          `json:"patch_invoked_at,omitempty"`
	HostFinalize                  *pipelineSealedConvergenceRecoveredHostFinalize `json:"host_finalize,omitempty"`
}

type pipelineSealedConvergenceRecoveredHostFinalize struct {
	SourcePartialSHA256           string                                               `json:"source_partial_sha256"`
	ReservedAt                    string                                               `json:"reserved_at"`
	PrefetchLockOwner             string                                               `json:"prefetch_lock_owner,omitempty"`
	PrefetchLockProcessID         int                                                  `json:"prefetch_lock_process_id,omitempty"`
	PrefetchStartedAt             string                                               `json:"prefetch_started_at,omitempty"`
	PrefetchBaselineReceiptDigest string                                               `json:"prefetch_baseline_receipt_digest,omitempty"`
	AccessReceiptDigest           string                                               `json:"access_receipt_digest,omitempty"`
	ArgsSHA256                    string                                               `json:"args_sha256,omitempty"`
	ToolDispatches                int                                                  `json:"tool_dispatches"`
	InvokedAt                     string                                               `json:"invoked_at,omitempty"`
	UpgradeRetry                  *pipelineSealedConvergenceRecoveredHostFinalizeRetry `json:"upgrade_retry,omitempty"`
}

type pipelineSealedConvergenceRecoveredHostFinalizeRetry struct {
	SourcePartialSHA256           string `json:"source_partial_sha256"`
	ReservedAt                    string `json:"reserved_at"`
	PrefetchLockOwner             string `json:"prefetch_lock_owner,omitempty"`
	PrefetchLockProcessID         int    `json:"prefetch_lock_process_id,omitempty"`
	PrefetchStartedAt             string `json:"prefetch_started_at,omitempty"`
	PrefetchBaselineReceiptDigest string `json:"prefetch_baseline_receipt_digest,omitempty"`
	AccessReceiptDigest           string `json:"access_receipt_digest,omitempty"`
	ArgsSHA256                    string `json:"args_sha256,omitempty"`
	ToolDispatches                int    `json:"tool_dispatches"`
	InvokedAt                     string `json:"invoked_at,omitempty"`
}

func validPipelineSealedConvergenceExhaustedSeedJournal(intent pipelineSealedConvergenceReplanIntent) bool {
	journal := intent.ExhaustedSeedFinalize
	if journal == nil {
		return true
	}
	if journal.Version != pipelineSealedConvergenceExhaustedSeedVersion ||
		journal.PlannerAttempt != intent.PlannerRepairLimit ||
		journal.PlannerAttempt != intent.PlannerRepairAttempts ||
		!validPipelineSealedConvergenceDigest(journal.SourcePartialSHA256) ||
		!reflect.DeepEqual(journal.AllowedSeedKeys, pipelineSealedConvergenceImmutableSeedKeys) ||
		!reflect.DeepEqual(journal.AllowedMutableKeys, pipelineSealedConvergenceSeededMutableKeys) ||
		!validPipelineSealedConvergenceDigest(journal.WriterSessionSHA256) ||
		journal.WriterSessionBytes < pipelineSealedConvergencePollutedSessionMinBytes ||
		journal.SessionCompactionCount <= 0 ||
		journal.SessionIdentity != fmt.Sprintf("convergence_planner_exhausted_compact_finalize-ch%02d", intent.SourceFrozen.Chapter) ||
		strings.TrimSpace(journal.BinaryPath) != journal.BinaryPath || !filepath.IsAbs(journal.BinaryPath) ||
		!validPipelineSealedConvergenceDigest(journal.BinaryFileSHA256) ||
		strings.TrimSpace(journal.BinaryVersionOutput) == "" ||
		journal.SeedToolDispatches < 0 || journal.SeedToolDispatches > 1 ||
		journal.ModelDispatches < 0 || journal.ModelDispatches > 1 {
		return false
	}
	reservedAt, err := time.Parse(time.RFC3339Nano, journal.ReservedAt)
	if err != nil {
		return false
	}
	if journal.PrefetchStartedAt == "" {
		return journal.PrefetchLockOwner == "" && journal.PrefetchLockProcessID == 0 &&
			journal.PrefetchBaselineReceiptDigest == "" && journal.AccessReceiptDigest == "" &&
			journal.SeedDigest == "" && journal.SeedArgsSHA256 == "" &&
			journal.SeedToolDispatches == 0 && journal.SeedInvokedAt == "" &&
			journal.SeedPartialSHA256 == "" && journal.SeedFailureClass == "" && journal.SeededAt == "" &&
			journal.PromptSHA256 == "" && journal.PromptRunes == 0 &&
			journal.ModelDispatches == 0 && journal.DispatchedAt == ""
	}
	prefetchAt, err := time.Parse(time.RFC3339Nano, journal.PrefetchStartedAt)
	if err != nil || prefetchAt.Before(reservedAt) || strings.TrimSpace(journal.PrefetchLockOwner) == "" ||
		journal.PrefetchLockProcessID <= 0 ||
		!validPipelineSealedConvergenceDigest(journal.PrefetchBaselineReceiptDigest) {
		return false
	}
	if journal.SeedToolDispatches == 0 {
		return journal.AccessReceiptDigest == "" && journal.SeedDigest == "" &&
			journal.SeedArgsSHA256 == "" && journal.SeedInvokedAt == "" &&
			journal.SeedPartialSHA256 == "" && journal.SeedFailureClass == "" && journal.SeededAt == "" &&
			journal.PromptSHA256 == "" && journal.PromptRunes == 0 &&
			journal.ModelDispatches == 0 && journal.DispatchedAt == ""
	}
	seedInvokedAt, err := time.Parse(time.RFC3339Nano, journal.SeedInvokedAt)
	if err != nil || seedInvokedAt.Before(prefetchAt) ||
		!validPipelineSealedConvergenceDigest(journal.AccessReceiptDigest) ||
		!validPipelineSealedConvergenceDigest(journal.SeedDigest) ||
		!validPipelineSealedConvergenceDigest(journal.SeedArgsSHA256) {
		return false
	}
	if journal.SeedPartialSHA256 == "" {
		return journal.SeedFailureClass == "" && journal.SeededAt == "" &&
			journal.PromptSHA256 == "" && journal.PromptRunes == 0 &&
			journal.ModelDispatches == 0 && journal.DispatchedAt == ""
	}
	seededAt, err := time.Parse(time.RFC3339Nano, journal.SeededAt)
	if err != nil || seededAt.Before(seedInvokedAt) ||
		!validPipelineSealedConvergenceDigest(journal.SeedPartialSHA256) ||
		(journal.SeedFailureClass != "" && journal.SeedFailureClass != pipelineSealedConvergenceExhaustedSeedFailureClass) {
		return false
	}
	if journal.ModelDispatches == 0 {
		return journal.PromptSHA256 == "" && journal.PromptRunes == 0 && journal.DispatchedAt == ""
	}
	dispatchedAt, err := time.Parse(time.RFC3339Nano, journal.DispatchedAt)
	return err == nil && !dispatchedAt.Before(seededAt) &&
		validPipelineSealedConvergenceDigest(journal.PromptSHA256) &&
		journal.PromptRunes > 0 && journal.PromptRunes <= pipelineSealedConvergenceSeededPromptMaxRunes &&
		validPipelineSealedConvergenceDisabledToolRecovery(journal.DisabledToolRecovery, dispatchedAt)
}

func validPipelineSealedConvergenceDisabledToolRecovery(
	recovery *pipelineSealedConvergenceDisabledToolRecovery,
	modelDispatchedAt time.Time,
) bool {
	if recovery == nil {
		return true
	}
	if recovery.Version != pipelineSealedConvergenceDisabledToolRecoveryVersion ||
		!validPipelineSealedConvergenceDigest(recovery.SessionSHA256) ||
		strings.TrimSpace(recovery.ToolCallID) == "" ||
		!validPipelineSealedConvergenceDigest(recovery.ArgsSHA256) ||
		!validPipelineSealedConvergenceDigest(recovery.InitialPartialSHA256) ||
		recovery.SourceToolDispatches < 0 || recovery.SourceToolDispatches > 1 ||
		recovery.PatchToolDispatches < 0 || recovery.PatchToolDispatches > 1 {
		return false
	}
	reservedAt, err := time.Parse(time.RFC3339Nano, recovery.ReservedAt)
	if err != nil || reservedAt.Before(modelDispatchedAt) {
		return false
	}
	if recovery.PrefetchStartedAt == "" {
		return recovery.PrefetchLockOwner == "" && recovery.PrefetchLockProcessID == 0 &&
			recovery.PrefetchBaselineReceiptDigest == "" && recovery.AccessReceiptDigest == "" &&
			recovery.SourceArgsSHA256 == "" && recovery.SourceToolDispatches == 0 &&
			recovery.SourceInvokedAt == "" && recovery.SourcePartialSHA256 == "" && recovery.SourceSeededAt == "" &&
			recovery.PatchToolDispatches == 0 && recovery.PatchInvokedAt == "" && recovery.HostFinalize == nil
	}
	prefetchAt, err := time.Parse(time.RFC3339Nano, recovery.PrefetchStartedAt)
	if err != nil || prefetchAt.Before(reservedAt) || strings.TrimSpace(recovery.PrefetchLockOwner) == "" ||
		recovery.PrefetchLockProcessID <= 0 ||
		!validPipelineSealedConvergenceDigest(recovery.PrefetchBaselineReceiptDigest) {
		return false
	}
	if recovery.SourceToolDispatches == 0 {
		return recovery.AccessReceiptDigest == "" && recovery.SourceArgsSHA256 == "" &&
			recovery.SourceInvokedAt == "" && recovery.SourcePartialSHA256 == "" && recovery.SourceSeededAt == "" &&
			recovery.PatchToolDispatches == 0 && recovery.PatchInvokedAt == "" && recovery.HostFinalize == nil
	}
	sourceInvokedAt, err := time.Parse(time.RFC3339Nano, recovery.SourceInvokedAt)
	if err != nil || sourceInvokedAt.Before(prefetchAt) ||
		!validPipelineSealedConvergenceDigest(recovery.AccessReceiptDigest) ||
		!validPipelineSealedConvergenceDigest(recovery.SourceArgsSHA256) {
		return false
	}
	if recovery.SourcePartialSHA256 == "" {
		return recovery.SourceSeededAt == "" && recovery.PatchToolDispatches == 0 &&
			recovery.PatchInvokedAt == "" && recovery.HostFinalize == nil
	}
	sourceSeededAt, err := time.Parse(time.RFC3339Nano, recovery.SourceSeededAt)
	if err != nil || sourceSeededAt.Before(sourceInvokedAt) ||
		!validPipelineSealedConvergenceDigest(recovery.SourcePartialSHA256) {
		return false
	}
	if recovery.PatchToolDispatches == 0 {
		return recovery.PatchInvokedAt == "" && recovery.HostFinalize == nil
	}
	patchInvokedAt, err := time.Parse(time.RFC3339Nano, recovery.PatchInvokedAt)
	return err == nil && !patchInvokedAt.Before(sourceSeededAt) &&
		validPipelineSealedConvergenceRecoveredHostFinalize(recovery.HostFinalize, patchInvokedAt)
}

func validPipelineSealedConvergenceRecoveredHostFinalize(
	finalize *pipelineSealedConvergenceRecoveredHostFinalize,
	patchInvokedAt time.Time,
) bool {
	if finalize == nil {
		return true
	}
	if !validPipelineSealedConvergenceDigest(finalize.SourcePartialSHA256) ||
		finalize.ToolDispatches < 0 || finalize.ToolDispatches > 1 {
		return false
	}
	reservedAt, err := time.Parse(time.RFC3339Nano, finalize.ReservedAt)
	if err != nil || reservedAt.Before(patchInvokedAt) {
		return false
	}
	if finalize.PrefetchStartedAt == "" {
		return finalize.PrefetchLockOwner == "" && finalize.PrefetchLockProcessID == 0 &&
			finalize.PrefetchBaselineReceiptDigest == "" && finalize.AccessReceiptDigest == "" &&
			finalize.ArgsSHA256 == "" && finalize.ToolDispatches == 0 && finalize.InvokedAt == "" &&
			finalize.UpgradeRetry == nil
	}
	prefetchAt, err := time.Parse(time.RFC3339Nano, finalize.PrefetchStartedAt)
	if err != nil || prefetchAt.Before(reservedAt) || strings.TrimSpace(finalize.PrefetchLockOwner) == "" ||
		finalize.PrefetchLockProcessID <= 0 ||
		!validPipelineSealedConvergenceDigest(finalize.PrefetchBaselineReceiptDigest) {
		return false
	}
	if finalize.ToolDispatches == 0 {
		return finalize.AccessReceiptDigest == "" && finalize.ArgsSHA256 == "" && finalize.InvokedAt == "" &&
			finalize.UpgradeRetry == nil
	}
	invokedAt, err := time.Parse(time.RFC3339Nano, finalize.InvokedAt)
	return err == nil && !invokedAt.Before(prefetchAt) &&
		validPipelineSealedConvergenceDigest(finalize.AccessReceiptDigest) &&
		validPipelineSealedConvergenceDigest(finalize.ArgsSHA256) &&
		validPipelineSealedConvergenceRecoveredHostFinalizeRetry(finalize.UpgradeRetry, invokedAt)
}

func validPipelineSealedConvergenceRecoveredHostFinalizeRetry(
	retry *pipelineSealedConvergenceRecoveredHostFinalizeRetry,
	priorInvokedAt time.Time,
) bool {
	if retry == nil {
		return true
	}
	if !validPipelineSealedConvergenceDigest(retry.SourcePartialSHA256) ||
		retry.ToolDispatches < 0 || retry.ToolDispatches > 1 {
		return false
	}
	reservedAt, err := time.Parse(time.RFC3339Nano, retry.ReservedAt)
	if err != nil || reservedAt.Before(priorInvokedAt) {
		return false
	}
	if retry.PrefetchStartedAt == "" {
		return retry.PrefetchLockOwner == "" && retry.PrefetchLockProcessID == 0 &&
			retry.PrefetchBaselineReceiptDigest == "" && retry.AccessReceiptDigest == "" &&
			retry.ArgsSHA256 == "" && retry.ToolDispatches == 0 && retry.InvokedAt == ""
	}
	prefetchAt, err := time.Parse(time.RFC3339Nano, retry.PrefetchStartedAt)
	if err != nil || prefetchAt.Before(reservedAt) || strings.TrimSpace(retry.PrefetchLockOwner) == "" ||
		retry.PrefetchLockProcessID <= 0 ||
		!validPipelineSealedConvergenceDigest(retry.PrefetchBaselineReceiptDigest) {
		return false
	}
	if retry.ToolDispatches == 0 {
		return retry.AccessReceiptDigest == "" && retry.ArgsSHA256 == "" && retry.InvokedAt == ""
	}
	invokedAt, err := time.Parse(time.RFC3339Nano, retry.InvokedAt)
	return err == nil && !invokedAt.Before(prefetchAt) &&
		validPipelineSealedConvergenceDigest(retry.AccessReceiptDigest) &&
		validPipelineSealedConvergenceDigest(retry.ArgsSHA256)
}

func pipelineSealedConvergenceTryExhaustedSeedFinalize(
	cfg bootstrap.Config,
	promptBundle assets.Bundle,
	st *store.Store,
	chapter int,
	intent *pipelineSealedConvergenceReplanIntent,
	eligibility *pipelineSealedConvergenceReplanEligibility,
) (bool, error) {
	if st == nil || intent == nil || eligibility == nil || chapter <= 0 {
		return false, fmt.Errorf("sealed convergence exhausted seed identity is incomplete")
	}
	if intent.PlannerRepairAttempts != intent.PlannerRepairLimit || intent.PlannerRepairLimit != 2 ||
		intent.HostFinalize == nil || intent.HostFinalize.FailureClass == "" {
		return false, nil
	}
	cp, err := tools.CurrentChapterPlanCausalCheckpoint(st, chapter)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	if cp != nil && cp.Seq > intent.SourceFrozen.PlanCheckpointSeq {
		return true, nil
	}
	partialPath := filepath.Join(st.Dir(), "drafts", fmt.Sprintf("%02d.plan.partial.json", chapter))
	partialRaw, err := os.ReadFile(partialPath)
	if err != nil {
		return false, fmt.Errorf("sealed convergence exhausted seed read partial: %w", err)
	}
	var partial map[string]any
	if err := json.Unmarshal(partialRaw, &partial); err != nil {
		return false, fmt.Errorf("sealed convergence exhausted seed decode partial: %w", err)
	}
	if err := validatePipelineSealedConvergenceContinuationPartial(
		st, chapter, partial, eligibility.Simulation,
	); err != nil {
		return false, err
	}
	partialSHA := pipelineBytesSHA(partialRaw)
	sessionRaw, sessionSHA, compactions, err := pipelineSealedConvergencePollutedWriterSession(st.Dir(), chapter)
	if err != nil {
		return false, err
	}
	probe, err := pipelineSealedConvergenceProbeFailoverBinary()
	if err != nil {
		return false, fmt.Errorf("sealed convergence exhausted compact executable: %w", err)
	}
	journal := intent.ExhaustedSeedFinalize
	if journal == nil {
		sessionIdentity := fmt.Sprintf("convergence_planner_exhausted_compact_finalize-ch%02d", chapter)
		if err := pipelineSealedConvergenceRequireSessionAbsent(st.Dir(), sessionIdentity); err != nil {
			return false, err
		}
		journal = &pipelineSealedConvergenceExhaustedSeedFinalize{
			Version:                pipelineSealedConvergenceExhaustedSeedVersion,
			PlannerAttempt:         intent.PlannerRepairAttempts,
			SourcePartialSHA256:    partialSHA,
			AllowedSeedKeys:        append([]string(nil), pipelineSealedConvergenceImmutableSeedKeys...),
			AllowedMutableKeys:     append([]string(nil), pipelineSealedConvergenceSeededMutableKeys...),
			WriterSessionSHA256:    sessionSHA,
			WriterSessionBytes:     int64(len(sessionRaw)),
			SessionCompactionCount: compactions,
			SessionIdentity:        sessionIdentity,
			BinaryPath:             probe.BinaryPath,
			BinaryFileSHA256:       probe.BinaryFileSHA256,
			BinaryVersionOutput:    probe.BinaryVersionOutput,
			ReservedAt:             time.Now().UTC().Format(time.RFC3339Nano),
		}
		intent.ExhaustedSeedFinalize = journal
		if err := saveUpdatedPipelineSealedConvergenceReplanIntent(st.Dir(), intent, eligibility); err != nil {
			return false, fmt.Errorf("sealed convergence reserve exhausted seed finalize: %w", err)
		}
	} else if journal.WriterSessionSHA256 != sessionSHA || journal.WriterSessionBytes != int64(len(sessionRaw)) ||
		journal.SessionCompactionCount != compactions || journal.BinaryPath != probe.BinaryPath ||
		journal.BinaryFileSHA256 != probe.BinaryFileSHA256 || journal.BinaryVersionOutput != probe.BinaryVersionOutput {
		return false, fmt.Errorf("sealed convergence exhausted seed partial/session binding drift")
	}
	if journal.ModelDispatches != 0 {
		return pipelineSealedConvergenceRecoverDisabledToolCall(
			promptBundle, cfg.Style, st, chapter, intent, eligibility, journal,
			partialRaw, partialSHA,
		)
	}

	if journal.SeedPartialSHA256 == "" {
		if journal.SeedToolDispatches != 0 {
			if partialSHA == journal.SourcePartialSHA256 {
				return false, fmt.Errorf("sealed convergence exhausted seed dispatch 1/1 was reserved without an auditable partial side effect; replay forbidden")
			}
			if err := pipelineSealedConvergenceRequireFormalPlanAbsent(st, chapter); err != nil {
				return false, err
			}
			journal.SeedPartialSHA256 = partialSHA
			journal.SeededAt = time.Now().UTC().Format(time.RFC3339Nano)
			if err := saveUpdatedPipelineSealedConvergenceReplanIntent(st.Dir(), intent, eligibility); err != nil {
				return false, err
			}
		} else {
			seeded, finished, seedErr := pipelineSealedConvergenceApplyExhaustedSeed(
				promptBundle, cfg.Style, st, chapter, intent, eligibility, journal, partial,
			)
			if seedErr != nil || finished {
				return finished, seedErr
			}
			partialRaw = seeded
			partialSHA = pipelineBytesSHA(partialRaw)
		}
	}
	if partialSHA != journal.SeedPartialSHA256 {
		return false, fmt.Errorf("sealed convergence exhausted seeded partial SHA drift")
	}
	if err := pipelineSealedConvergenceRequireFormalPlanAbsent(st, chapter); err != nil {
		return false, err
	}

	seededRecovery := &pipelineSealedConvergenceSeededCompactFinalize{
		AllowedMutableKeys: append([]string(nil), journal.AllowedMutableKeys...),
		SeedPartialSHA256:  journal.SeedPartialSHA256,
	}
	prompt, err := pipelineSealedConvergenceBuildSeededCompactPrompt(
		partialRaw, *intent, eligibility, seededRecovery,
	)
	if err != nil {
		return false, err
	}
	if err := pipelineSealedConvergenceRequireSessionAbsent(st.Dir(), journal.SessionIdentity); err != nil {
		return false, err
	}
	journal.PromptSHA256 = pipelineBytesSHA([]byte(prompt))
	journal.PromptRunes = utf8.RuneCountInString(prompt)
	journal.ModelDispatches = 1
	journal.DispatchedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if err := saveUpdatedPipelineSealedConvergenceReplanIntent(st.Dir(), intent, eligibility); err != nil {
		return false, fmt.Errorf("sealed convergence persist exhausted compact model dispatch 1/1: %w", err)
	}
	allowlisted := newPipelineSealedConvergenceMutablePlanDetailsTool(
		tools.NewPlanDetailsTool(st), chapter, journal.AllowedMutableKeys,
	)
	if err := agents.RunSealedConvergencePlannerExhaustedCompactFinalize(
		context.Background(), cfg, promptBundle, st.Dir(), chapter, prompt,
		journal.BinaryPath, allowlisted,
	); err != nil {
		return false, fmt.Errorf("sealed convergence exhausted compact finalize dispatch 1/1 failed: %w", err)
	}
	cp, err = tools.CurrentChapterPlanCausalCheckpoint(store.NewStore(st.Dir()), chapter)
	if err != nil || cp == nil || cp.Seq <= intent.SourceFrozen.PlanCheckpointSeq {
		return false, fmt.Errorf("sealed convergence exhausted compact finalize returned without successor checkpoint: %w", err)
	}
	fmt.Fprintf(os.Stderr, "[pipeline:plan] 第 %d 章污染 writer 日志已隔离；Host seed + 极简 allowlisted Planner 已通过正式 PlanDetailsTool 收口\n", chapter)
	return true, nil
}

func pipelineSealedConvergenceApplyExhaustedSeed(
	promptBundle assets.Bundle,
	style string,
	st *store.Store,
	chapter int,
	intent *pipelineSealedConvergenceReplanIntent,
	eligibility *pipelineSealedConvergenceReplanEligibility,
	journal *pipelineSealedConvergenceExhaustedSeedFinalize,
	partial map[string]any,
) ([]byte, bool, error) {
	lock, err := pipelineSealedConvergenceCurrentReplacementLock(st, chapter)
	if err != nil {
		return nil, false, err
	}
	baseline, err := st.Runtime.LoadPlanningContextAccessReceipt(domain.PlanningContextAccessPlan)
	if err != nil || baseline == nil || !validPipelineSealedConvergenceDigest(baseline.ReceiptDigest) {
		return nil, false, fmt.Errorf("sealed convergence exhausted compact baseline receipt unavailable: %w", err)
	}
	journal.PrefetchLockOwner = lock.Owner
	journal.PrefetchLockProcessID = lock.ProcessID
	journal.PrefetchStartedAt = time.Now().UTC().Format(time.RFC3339Nano)
	journal.PrefetchBaselineReceiptDigest = baseline.ReceiptDigest
	if err := saveUpdatedPipelineSealedConvergenceReplanIntent(st.Dir(), intent, eligibility); err != nil {
		return nil, false, fmt.Errorf("sealed convergence persist exhausted compact prefetch: %w", err)
	}
	contextRaw, err := pipelineSealedConvergencePrefetchPlanningContext(
		st, promptBundle, style, chapter, intent.ChapterWordBounds,
	)
	if err != nil {
		return nil, false, err
	}
	accessToken, err := pipelineSealedConvergencePlanningAccessToken(contextRaw)
	if err != nil {
		return nil, false, err
	}
	receipt, err := pipelineSealedConvergenceCurrentUnconsumedPlanReceipt(st, *intent, lock)
	if err != nil {
		return nil, false, fmt.Errorf("sealed convergence exhausted compact bind planning receipt: %w", err)
	}
	seed, err := pipelineSealedConvergenceExhaustedImmutableSeed(st, eligibility, partial, accessToken)
	if err != nil {
		return nil, false, err
	}
	args, err := json.Marshal(map[string]any{
		"chapter": chapter, "causal_simulation": seed, "finalize": false,
	})
	if err != nil {
		return nil, false, err
	}
	journal.AccessReceiptDigest = receipt.ReceiptDigest
	journal.SeedDigest = pipelineProjectAllDigest(seed)
	journal.SeedArgsSHA256 = pipelineBytesSHA(args)
	journal.SeedToolDispatches = 1
	journal.SeedInvokedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if err := saveUpdatedPipelineSealedConvergenceReplanIntent(st.Dir(), intent, eligibility); err != nil {
		return nil, false, fmt.Errorf("sealed convergence persist exhausted compact seed 1/1: %w", err)
	}
	_, executeErr := tools.NewPlanDetailsTool(st).Execute(context.Background(), args)
	if cp, cpErr := tools.CurrentChapterPlanCausalCheckpoint(store.NewStore(st.Dir()), chapter); cpErr == nil && cp != nil && cp.Seq > intent.SourceFrozen.PlanCheckpointSeq {
		return nil, true, nil
	}
	partialPath := filepath.Join(st.Dir(), "drafts", fmt.Sprintf("%02d.plan.partial.json", chapter))
	seededRaw, readErr := os.ReadFile(partialPath)
	if readErr != nil {
		return nil, false, fmt.Errorf("sealed convergence read exhausted seeded partial: %w", readErr)
	}
	seededSHA := pipelineBytesSHA(seededRaw)
	if seededSHA == journal.SourcePartialSHA256 {
		return nil, false, fmt.Errorf("sealed convergence exhausted seed produced no auditable partial side effect: %w", executeErr)
	}
	if err := pipelineSealedConvergenceRequireFormalPlanAbsent(st, chapter); err != nil {
		return nil, false, err
	}
	if executeErr != nil {
		if !errors.Is(executeErr, errs.ErrToolPrecondition) {
			return nil, false, fmt.Errorf("sealed convergence exhausted Host seed failed before semantic auto-finalize: %w", executeErr)
		}
		journal.SeedFailureClass = pipelineSealedConvergenceExhaustedSeedFailureClass
	}
	journal.SeedPartialSHA256 = seededSHA
	journal.SeededAt = time.Now().UTC().Format(time.RFC3339Nano)
	if err := saveUpdatedPipelineSealedConvergenceReplanIntent(st.Dir(), intent, eligibility); err != nil {
		return nil, false, fmt.Errorf("sealed convergence persist exhausted seeded partial: %w", err)
	}
	return seededRaw, false, nil
}

type pipelineSealedConvergenceDisabledToolCall struct {
	ID   string
	Args json.RawMessage
}

func pipelineSealedConvergenceRecoverDisabledToolCall(
	promptBundle assets.Bundle,
	style string,
	st *store.Store,
	chapter int,
	intent *pipelineSealedConvergenceReplanIntent,
	eligibility *pipelineSealedConvergenceReplanEligibility,
	journal *pipelineSealedConvergenceExhaustedSeedFinalize,
	currentRaw []byte,
	currentSHA string,
) (bool, error) {
	if journal == nil || journal.ModelDispatches != 1 {
		return false, fmt.Errorf("sealed convergence disabled-tool recovery requires model dispatch 1/1")
	}
	call, sessionSHA, err := pipelineSealedConvergenceExtractDisabledToolCall(
		st.Dir(), chapter, journal.SessionIdentity, journal.AllowedMutableKeys,
	)
	if err != nil {
		return false, err
	}
	recovery := journal.DisabledToolRecovery
	if recovery == nil {
		recovery = &pipelineSealedConvergenceDisabledToolRecovery{
			Version:              pipelineSealedConvergenceDisabledToolRecoveryVersion,
			SessionSHA256:        sessionSHA,
			ToolCallID:           call.ID,
			ArgsSHA256:           pipelineBytesSHA(call.Args),
			InitialPartialSHA256: currentSHA,
			ReservedAt:           time.Now().UTC().Format(time.RFC3339Nano),
		}
		journal.DisabledToolRecovery = recovery
		if err := saveUpdatedPipelineSealedConvergenceReplanIntent(st.Dir(), intent, eligibility); err != nil {
			return false, fmt.Errorf("sealed convergence reserve disabled-tool recovery: %w", err)
		}
	} else if recovery.SessionSHA256 != sessionSHA || recovery.ToolCallID != call.ID ||
		recovery.ArgsSHA256 != pipelineBytesSHA(call.Args) {
		return false, fmt.Errorf("sealed convergence disabled-tool recovery session/args drift")
	}
	if recovery.PatchToolDispatches != 0 {
		return pipelineSealedConvergenceFinalizeRecoveredPartial(
			promptBundle, style, st, chapter, intent, eligibility, recovery, currentSHA,
		)
	}

	if recovery.SourcePartialSHA256 == "" {
		if recovery.SourceToolDispatches != 0 {
			if currentSHA == recovery.InitialPartialSHA256 {
				return false, fmt.Errorf("sealed convergence recovery source refresh was reserved without an auditable side effect; replay forbidden")
			}
			if err := pipelineSealedConvergenceRequireFormalPlanAbsent(st, chapter); err != nil {
				return false, err
			}
			recovery.SourcePartialSHA256 = currentSHA
			recovery.SourceSeededAt = time.Now().UTC().Format(time.RFC3339Nano)
			if err := saveUpdatedPipelineSealedConvergenceReplanIntent(st.Dir(), intent, eligibility); err != nil {
				return false, err
			}
		} else {
			refreshedRaw, finished, refreshErr := pipelineSealedConvergenceRefreshDisabledToolSources(
				promptBundle, style, st, chapter, intent, eligibility, recovery,
			)
			if refreshErr != nil || finished {
				return finished, refreshErr
			}
			currentRaw = refreshedRaw
			currentSHA = pipelineBytesSHA(refreshedRaw)
		}
	}
	if currentSHA != recovery.SourcePartialSHA256 {
		return false, fmt.Errorf("sealed convergence disabled-tool source partial SHA drift")
	}
	if err := pipelineSealedConvergenceRequireFormalPlanAbsent(st, chapter); err != nil {
		return false, err
	}

	recovery.PatchToolDispatches = 1
	recovery.PatchInvokedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if err := saveUpdatedPipelineSealedConvergenceReplanIntent(st.Dir(), intent, eligibility); err != nil {
		return false, fmt.Errorf("sealed convergence persist recovered tool dispatch 1/1: %w", err)
	}
	if _, err := tools.NewPlanDetailsTool(st).Execute(context.Background(), call.Args); err != nil {
		return false, fmt.Errorf("sealed convergence exact disabled tool call failed: %w", err)
	}
	cp, err := tools.CurrentChapterPlanCausalCheckpoint(store.NewStore(st.Dir()), chapter)
	if err != nil || cp == nil || cp.Seq <= intent.SourceFrozen.PlanCheckpointSeq {
		return false, fmt.Errorf("sealed convergence exact disabled tool call returned without successor checkpoint: %w", err)
	}
	_ = currentRaw
	fmt.Fprintf(os.Stderr, "[pipeline:plan] 第 %d 章已执行被 agentcore 错误上限拦截的精确第二次 patch；未重新调用模型\n", chapter)
	return true, nil
}

func pipelineSealedConvergenceRefreshDisabledToolSources(
	promptBundle assets.Bundle,
	style string,
	st *store.Store,
	chapter int,
	intent *pipelineSealedConvergenceReplanIntent,
	eligibility *pipelineSealedConvergenceReplanEligibility,
	recovery *pipelineSealedConvergenceDisabledToolRecovery,
) ([]byte, bool, error) {
	lock, err := pipelineSealedConvergenceCurrentReplacementLock(st, chapter)
	if err != nil {
		return nil, false, err
	}
	baseline, err := st.Runtime.LoadPlanningContextAccessReceipt(domain.PlanningContextAccessPlan)
	if err != nil || baseline == nil || !validPipelineSealedConvergenceDigest(baseline.ReceiptDigest) {
		return nil, false, fmt.Errorf("sealed convergence disabled-tool baseline receipt unavailable: %w", err)
	}
	recovery.PrefetchLockOwner = lock.Owner
	recovery.PrefetchLockProcessID = lock.ProcessID
	recovery.PrefetchStartedAt = time.Now().UTC().Format(time.RFC3339Nano)
	recovery.PrefetchBaselineReceiptDigest = baseline.ReceiptDigest
	if err := saveUpdatedPipelineSealedConvergenceReplanIntent(st.Dir(), intent, eligibility); err != nil {
		return nil, false, fmt.Errorf("sealed convergence persist disabled-tool prefetch: %w", err)
	}
	contextRaw, err := pipelineSealedConvergencePrefetchPlanningContext(
		st, promptBundle, style, chapter, intent.ChapterWordBounds,
	)
	if err != nil {
		return nil, false, err
	}
	accessToken, err := pipelineSealedConvergencePlanningAccessToken(contextRaw)
	if err != nil {
		return nil, false, err
	}
	receipt, err := pipelineSealedConvergenceCurrentUnconsumedPlanReceipt(st, *intent, lock)
	if err != nil {
		return nil, false, err
	}
	sourceArgs, err := json.Marshal(map[string]any{
		"chapter": chapter,
		"causal_simulation": map[string]any{
			"context_sources": []string{accessToken},
		},
		"finalize": false,
	})
	if err != nil {
		return nil, false, err
	}
	recovery.AccessReceiptDigest = receipt.ReceiptDigest
	recovery.SourceArgsSHA256 = pipelineBytesSHA(sourceArgs)
	recovery.SourceToolDispatches = 1
	recovery.SourceInvokedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if err := saveUpdatedPipelineSealedConvergenceReplanIntent(st.Dir(), intent, eligibility); err != nil {
		return nil, false, fmt.Errorf("sealed convergence persist disabled-tool source refresh 1/1: %w", err)
	}
	_, executeErr := tools.NewPlanDetailsTool(st).Execute(context.Background(), sourceArgs)
	if cp, cpErr := tools.CurrentChapterPlanCausalCheckpoint(store.NewStore(st.Dir()), chapter); cpErr == nil && cp != nil && cp.Seq > intent.SourceFrozen.PlanCheckpointSeq {
		return nil, true, nil
	}
	partialPath := filepath.Join(st.Dir(), "drafts", fmt.Sprintf("%02d.plan.partial.json", chapter))
	refreshedRaw, readErr := os.ReadFile(partialPath)
	if readErr != nil {
		return nil, false, readErr
	}
	refreshedSHA := pipelineBytesSHA(refreshedRaw)
	if refreshedSHA == recovery.InitialPartialSHA256 {
		return nil, false, fmt.Errorf("sealed convergence disabled-tool source refresh produced no partial side effect: %w", executeErr)
	}
	if executeErr != nil && !errors.Is(executeErr, errs.ErrToolPrecondition) {
		return nil, false, fmt.Errorf("sealed convergence disabled-tool source refresh failed before semantic gate: %w", executeErr)
	}
	recovery.SourcePartialSHA256 = refreshedSHA
	recovery.SourceSeededAt = time.Now().UTC().Format(time.RFC3339Nano)
	if err := saveUpdatedPipelineSealedConvergenceReplanIntent(st.Dir(), intent, eligibility); err != nil {
		return nil, false, err
	}
	return refreshedRaw, false, nil
}

func pipelineSealedConvergenceFinalizeRecoveredPartial(
	promptBundle assets.Bundle,
	style string,
	st *store.Store,
	chapter int,
	intent *pipelineSealedConvergenceReplanIntent,
	eligibility *pipelineSealedConvergenceReplanEligibility,
	recovery *pipelineSealedConvergenceDisabledToolRecovery,
	currentSHA string,
) (bool, error) {
	if recovery == nil || recovery.PatchToolDispatches != 1 ||
		!validPipelineSealedConvergenceDigest(currentSHA) {
		return false, fmt.Errorf("sealed convergence recovered Host finalize identity is incomplete")
	}
	finalize := recovery.HostFinalize
	if finalize == nil {
		if currentSHA == recovery.SourcePartialSHA256 {
			return false, fmt.Errorf("sealed convergence recovered patch has no auditable partial side effect")
		}
		if err := pipelineSealedConvergenceRequireFormalPlanAbsent(st, chapter); err != nil {
			return false, err
		}
		finalize = &pipelineSealedConvergenceRecoveredHostFinalize{
			SourcePartialSHA256: currentSHA,
			ReservedAt:          time.Now().UTC().Format(time.RFC3339Nano),
		}
		recovery.HostFinalize = finalize
		if err := saveUpdatedPipelineSealedConvergenceReplanIntent(st.Dir(), intent, eligibility); err != nil {
			return false, fmt.Errorf("sealed convergence reserve recovered Host finalize: %w", err)
		}
	} else if finalize.ToolDispatches == 0 && finalize.SourcePartialSHA256 != currentSHA {
		return false, fmt.Errorf("sealed convergence recovered Host finalize partial SHA drift")
	}
	if finalize.ToolDispatches != 0 {
		return pipelineSealedConvergenceRetryRecoveredHostFinalize(
			promptBundle, style, st, chapter, intent, eligibility, finalize, currentSHA,
		)
	}
	lock, err := pipelineSealedConvergenceCurrentReplacementLock(st, chapter)
	if err != nil {
		return false, err
	}
	baseline, err := st.Runtime.LoadPlanningContextAccessReceipt(domain.PlanningContextAccessPlan)
	if err != nil || baseline == nil || !validPipelineSealedConvergenceDigest(baseline.ReceiptDigest) {
		return false, fmt.Errorf("sealed convergence recovered Host finalize baseline receipt unavailable: %w", err)
	}
	finalize.PrefetchLockOwner = lock.Owner
	finalize.PrefetchLockProcessID = lock.ProcessID
	finalize.PrefetchStartedAt = time.Now().UTC().Format(time.RFC3339Nano)
	finalize.PrefetchBaselineReceiptDigest = baseline.ReceiptDigest
	if err := saveUpdatedPipelineSealedConvergenceReplanIntent(st.Dir(), intent, eligibility); err != nil {
		return false, fmt.Errorf("sealed convergence persist recovered Host finalize prefetch: %w", err)
	}
	contextRaw, err := pipelineSealedConvergencePrefetchPlanningContext(
		st, promptBundle, style, chapter, intent.ChapterWordBounds,
	)
	if err != nil {
		return false, err
	}
	accessToken, err := pipelineSealedConvergencePlanningAccessToken(contextRaw)
	if err != nil {
		return false, err
	}
	receipt, err := pipelineSealedConvergenceCurrentUnconsumedPlanReceipt(st, *intent, lock)
	if err != nil {
		return false, err
	}
	args, err := json.Marshal(map[string]any{
		"chapter": chapter,
		"causal_simulation": map[string]any{
			"context_sources": []string{accessToken},
		},
		"finalize": true,
	})
	if err != nil {
		return false, err
	}
	finalize.AccessReceiptDigest = receipt.ReceiptDigest
	finalize.ArgsSHA256 = pipelineBytesSHA(args)
	finalize.ToolDispatches = 1
	finalize.InvokedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if err := saveUpdatedPipelineSealedConvergenceReplanIntent(st.Dir(), intent, eligibility); err != nil {
		return false, fmt.Errorf("sealed convergence persist recovered Host finalize 1/1: %w", err)
	}
	if _, err := tools.NewPlanDetailsTool(st).Execute(context.Background(), args); err != nil {
		return false, fmt.Errorf("sealed convergence recovered Host finalize failed: %w", err)
	}
	cp, err := tools.CurrentChapterPlanCausalCheckpoint(store.NewStore(st.Dir()), chapter)
	if err != nil || cp == nil || cp.Seq <= intent.SourceFrozen.PlanCheckpointSeq {
		return false, fmt.Errorf("sealed convergence recovered Host finalize returned without successor checkpoint: %w", err)
	}
	fmt.Fprintf(os.Stderr, "[pipeline:plan] 第 %d 章已用精确第二次 patch 的持久化 partial 完成 Host finalize；未重新调用模型\n", chapter)
	return true, nil
}

// pipelineSealedConvergenceRetryRecoveredHostFinalize is one final Host-only
// upgrade for a partial that the first finalize durably normalized before a
// newer semantic contract rejected it. It refreshes the single-use planning
// receipt and replays only finalize=true; the model-authored patch is never
// regenerated and this journal permits exactly one dispatch.
func pipelineSealedConvergenceRetryRecoveredHostFinalize(
	promptBundle assets.Bundle,
	style string,
	st *store.Store,
	chapter int,
	intent *pipelineSealedConvergenceReplanIntent,
	eligibility *pipelineSealedConvergenceReplanEligibility,
	finalize *pipelineSealedConvergenceRecoveredHostFinalize,
	currentSHA string,
) (bool, error) {
	if finalize == nil || finalize.ToolDispatches != 1 ||
		!validPipelineSealedConvergenceDigest(currentSHA) {
		return false, fmt.Errorf("sealed convergence recovered Host finalize upgrade identity is incomplete")
	}
	if err := pipelineSealedConvergenceRequireFormalPlanAbsent(st, chapter); err != nil {
		return false, err
	}
	retry := finalize.UpgradeRetry
	if retry == nil {
		if currentSHA == finalize.SourcePartialSHA256 {
			return false, fmt.Errorf("sealed convergence recovered Host finalize upgrade has no auditable partial side effect")
		}
		retry = &pipelineSealedConvergenceRecoveredHostFinalizeRetry{
			SourcePartialSHA256: currentSHA,
			ReservedAt:          time.Now().UTC().Format(time.RFC3339Nano),
		}
		finalize.UpgradeRetry = retry
		if err := saveUpdatedPipelineSealedConvergenceReplanIntent(st.Dir(), intent, eligibility); err != nil {
			return false, fmt.Errorf("sealed convergence reserve recovered Host finalize upgrade: %w", err)
		}
	} else if retry.SourcePartialSHA256 != currentSHA {
		return false, fmt.Errorf("sealed convergence recovered Host finalize upgrade partial SHA drift")
	}
	if retry.ToolDispatches != 0 {
		return false, fmt.Errorf("sealed convergence recovered Host finalize upgrade already dispatched %d/1 without a successor checkpoint", retry.ToolDispatches)
	}

	lock, err := pipelineSealedConvergenceCurrentReplacementLock(st, chapter)
	if err != nil {
		return false, err
	}
	baseline, err := st.Runtime.LoadPlanningContextAccessReceipt(domain.PlanningContextAccessPlan)
	if err != nil || baseline == nil || !validPipelineSealedConvergenceDigest(baseline.ReceiptDigest) {
		return false, fmt.Errorf("sealed convergence recovered Host finalize upgrade baseline receipt unavailable: %w", err)
	}
	retry.PrefetchLockOwner = lock.Owner
	retry.PrefetchLockProcessID = lock.ProcessID
	retry.PrefetchStartedAt = time.Now().UTC().Format(time.RFC3339Nano)
	retry.PrefetchBaselineReceiptDigest = baseline.ReceiptDigest
	if err := saveUpdatedPipelineSealedConvergenceReplanIntent(st.Dir(), intent, eligibility); err != nil {
		return false, fmt.Errorf("sealed convergence persist recovered Host finalize upgrade prefetch: %w", err)
	}
	contextRaw, err := pipelineSealedConvergencePrefetchPlanningContext(
		st, promptBundle, style, chapter, intent.ChapterWordBounds,
	)
	if err != nil {
		return false, err
	}
	accessToken, err := pipelineSealedConvergencePlanningAccessToken(contextRaw)
	if err != nil {
		return false, err
	}
	receipt, err := pipelineSealedConvergenceCurrentUnconsumedPlanReceipt(st, *intent, lock)
	if err != nil {
		return false, err
	}
	args, err := json.Marshal(map[string]any{
		"chapter": chapter,
		"causal_simulation": map[string]any{
			"context_sources": []string{accessToken},
		},
		"finalize": true,
	})
	if err != nil {
		return false, err
	}
	retry.AccessReceiptDigest = receipt.ReceiptDigest
	retry.ArgsSHA256 = pipelineBytesSHA(args)
	retry.ToolDispatches = 1
	retry.InvokedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if err := saveUpdatedPipelineSealedConvergenceReplanIntent(st.Dir(), intent, eligibility); err != nil {
		return false, fmt.Errorf("sealed convergence persist recovered Host finalize upgrade 1/1: %w", err)
	}
	if _, err := tools.NewPlanDetailsTool(st).Execute(context.Background(), args); err != nil {
		return false, fmt.Errorf("sealed convergence recovered Host finalize upgrade failed: %w", err)
	}
	cp, err := tools.CurrentChapterPlanCausalCheckpoint(store.NewStore(st.Dir()), chapter)
	if err != nil || cp == nil || cp.Seq <= intent.SourceFrozen.PlanCheckpointSeq {
		return false, fmt.Errorf("sealed convergence recovered Host finalize upgrade returned without successor checkpoint: %w", err)
	}
	fmt.Fprintf(os.Stderr, "[pipeline:plan] 第 %d 章已完成唯一一次 Host finalize 升级；未重新调用模型\n", chapter)
	return true, nil
}

func pipelineSealedConvergenceExtractDisabledToolCall(
	outputDir string,
	chapter int,
	sessionIdentity string,
	allowedMutableKeys []string,
) (pipelineSealedConvergenceDisabledToolCall, string, error) {
	var out pipelineSealedConvergenceDisabledToolCall
	path := filepath.Join(outputDir, "meta", "sessions", "agents", sessionIdentity+".jsonl")
	raw, err := os.ReadFile(path)
	if err != nil {
		return out, "", fmt.Errorf("read exhausted compact session: %w", err)
	}
	audit, err := pipelineSealedConvergenceAuditSession(outputDir, sessionIdentity)
	if err != nil {
		return out, "", err
	}
	if audit.Rows != 5 || audit.UserRows != 1 || audit.AssistantRows != 2 ||
		audit.ToolRows != 2 || audit.ToolCallBlocks != 2 {
		return out, "", fmt.Errorf("exhausted compact session is not the exact two-turn disabled-tool shape")
	}
	type contentBlock struct {
		Type     string `json:"type"`
		Text     string `json:"text"`
		ToolCall *struct {
			ID   string          `json:"id"`
			Name string          `json:"name"`
			Args json.RawMessage `json:"args"`
		} `json:"tool_call"`
	}
	type row struct {
		Role     string         `json:"role"`
		Content  []contentBlock `json:"content"`
		Metadata struct {
			IsError    bool   `json:"is_error"`
			ToolCallID string `json:"tool_call_id"`
			ToolName   string `json:"tool_name"`
		} `json:"metadata"`
	}
	var rows []row
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var decoded row
		if err := json.Unmarshal(line, &decoded); err != nil {
			return out, "", fmt.Errorf("decode exhausted compact session row: %w", err)
		}
		rows = append(rows, decoded)
	}
	if err := scanner.Err(); err != nil {
		return out, "", err
	}
	if len(rows) != 5 || rows[3].Role != "assistant" || rows[4].Role != "tool" ||
		!rows[4].Metadata.IsError ||
		(rows[4].Metadata.ToolName != "" && rows[4].Metadata.ToolName != "plan_details") {
		return out, "", fmt.Errorf("exhausted compact session tail is not an unexecuted disabled plan_details call")
	}
	var disabledText string
	for _, block := range rows[4].Content {
		disabledText += block.Text
	}
	disabledText = strings.ReplaceAll(disabledText, `\"`, `"`)
	if !strings.Contains(disabledText, `tool "plan_details" disabled after 1 consecutive errors`) {
		return out, "", fmt.Errorf("exhausted compact session tail lacks the exact agentcore disabled-tool proof")
	}
	for _, block := range rows[3].Content {
		if block.Type != "toolCall" || block.ToolCall == nil || block.ToolCall.Name != "plan_details" {
			continue
		}
		if out.ID != "" {
			return out, "", fmt.Errorf("disabled assistant row contains multiple plan_details calls")
		}
		out.ID = strings.TrimSpace(block.ToolCall.ID)
		out.Args = append(json.RawMessage(nil), block.ToolCall.Args...)
	}
	if out.ID == "" || out.ID != rows[4].Metadata.ToolCallID {
		return out, "", fmt.Errorf("disabled plan_details tool_call identity mismatch")
	}
	if err := pipelineSealedConvergenceValidateRecoveredPatchArgs(out.Args, chapter, allowedMutableKeys); err != nil {
		return out, "", err
	}
	return out, pipelineBytesSHA(raw), nil
}

func pipelineSealedConvergenceValidateRecoveredPatchArgs(
	args json.RawMessage,
	chapter int,
	allowedMutableKeys []string,
) error {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(args, &envelope); err != nil {
		return err
	}
	for key := range envelope {
		switch key {
		case "chapter", "causal_simulation", "finalize":
		default:
			return fmt.Errorf("disabled plan_details patch widened top-level key %s", key)
		}
	}
	var request struct {
		Chapter          int            `json:"chapter"`
		CausalSimulation map[string]any `json:"causal_simulation"`
		Finalize         bool           `json:"finalize"`
	}
	if err := json.Unmarshal(args, &request); err != nil {
		return err
	}
	if request.Chapter != chapter || request.Finalize || len(request.CausalSimulation) == 0 {
		return fmt.Errorf("disabled plan_details recovery requires exact chapter, nonempty patch and the model's original finalize=false")
	}
	allowed := make(map[string]struct{}, len(allowedMutableKeys))
	for _, key := range allowedMutableKeys {
		allowed[key] = struct{}{}
	}
	for key := range request.CausalSimulation {
		if _, ok := allowed[key]; !ok {
			return fmt.Errorf("disabled plan_details recovery forbids non-mutable key %s", key)
		}
	}
	return nil
}

func pipelineSealedConvergencePollutedWriterSession(outputDir string, chapter int) ([]byte, string, int, error) {
	path := filepath.Join(outputDir, "meta", "sessions", "agents", fmt.Sprintf("writer-ch%02d.jsonl", chapter))
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, "", 0, fmt.Errorf("sealed convergence inspect reused writer session: %w", err)
	}
	compactions := strings.Count(string(raw), pipelineSealedConvergencePollutedSessionCompactMark)
	if len(raw) < pipelineSealedConvergencePollutedSessionMinBytes || compactions == 0 {
		return nil, "", 0, fmt.Errorf("sealed convergence exhausted seed requires proof of a large compacted generic writer session")
	}
	return raw, pipelineBytesSHA(raw), compactions, nil
}

func pipelineSealedConvergenceExhaustedImmutableSeed(
	st *store.Store,
	eligibility *pipelineSealedConvergenceReplanEligibility,
	partial map[string]any,
	accessToken string,
) (map[string]any, error) {
	if st == nil || eligibility == nil || partial == nil {
		return nil, fmt.Errorf("sealed convergence exhausted immutable seed source is incomplete")
	}
	merged, ok := partial["causal_simulation"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("sealed convergence exhausted partial has no causal_simulation")
	}
	raw, err := json.Marshal(merged["external_reference_plan"])
	if err != nil {
		return nil, err
	}
	var externalRefs []domain.ExternalReferencePlan
	if err := json.Unmarshal(raw, &externalRefs); err != nil {
		return nil, fmt.Errorf("sealed convergence exhausted current external refs are invalid: %w", err)
	}
	if len(externalRefs) == 0 {
		return nil, fmt.Errorf("sealed convergence exhausted current external refs are empty")
	}
	seed := pipelineSealedConvergenceImmutableSeedMap(
		eligibility.Plan.CausalSimulation,
		accessToken,
		externalRefs,
	)
	if err := pipelineSealedConvergenceValidateImmutableSeedRoot(seed); err != nil {
		return nil, err
	}
	return seed, nil
}
