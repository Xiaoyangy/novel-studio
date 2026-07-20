package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/chenhongyang/novel-studio/assets"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/tools"
)

const pipelineSealedConvergenceHostFinalizeVersion = "sealed-convergence-host-finalize.v1"

// pipelineSealedConvergenceHostFinalize journals one deterministic invocation
// of the real PlanDetailsTool. It is available only after a paid Planner has
// already authored a durable partial. The Host may bind current server-owned
// provenance and consume the current planning access receipt, but it may not
// author any semantic plan field or reopen a model dispatch counter.
type pipelineSealedConvergenceHostFinalize struct {
	Version                       string `json:"version"`
	PlannerAttempt                int    `json:"planner_attempt"`
	InitialPartialSHA256          string `json:"initial_partial_sha256"`
	ReservedAt                    string `json:"reserved_at"`
	PrefetchLockOwner             string `json:"prefetch_lock_owner,omitempty"`
	PrefetchLockProcessID         int    `json:"prefetch_lock_process_id,omitempty"`
	PrefetchStartedAt             string `json:"prefetch_started_at,omitempty"`
	PrefetchBaselineReceiptDigest string `json:"prefetch_baseline_receipt_digest,omitempty"`
	AccessReceiptDigest           string `json:"access_receipt_digest,omitempty"`
	ArgsSHA256                    string `json:"args_sha256,omitempty"`
	ToolDispatches                int    `json:"tool_dispatches"`
	InvokedAt                     string `json:"invoked_at,omitempty"`
	FailureClass                  string `json:"failure_class,omitempty"`
	FailurePartialSHA256          string `json:"failure_partial_sha256,omitempty"`
	FailedAt                      string `json:"failed_at,omitempty"`
}

func validPipelineSealedConvergenceHostFinalizeJournal(intent pipelineSealedConvergenceReplanIntent) bool {
	journal := intent.HostFinalize
	if journal == nil {
		return true
	}
	if journal.Version != pipelineSealedConvergenceHostFinalizeVersion ||
		journal.PlannerAttempt > intent.PlannerRepairAttempts ||
		journal.PlannerAttempt <= 0 || journal.PlannerAttempt > intent.PlannerRepairLimit ||
		!validPipelineSealedConvergenceDigest(journal.InitialPartialSHA256) ||
		journal.ToolDispatches < 0 || journal.ToolDispatches > 1 {
		return false
	}
	reservedAt, err := time.Parse(time.RFC3339Nano, journal.ReservedAt)
	if err != nil {
		return false
	}
	if journal.PrefetchStartedAt == "" {
		return journal.PrefetchLockOwner == "" && journal.PrefetchLockProcessID == 0 &&
			journal.PrefetchBaselineReceiptDigest == "" && journal.AccessReceiptDigest == "" &&
			journal.ArgsSHA256 == "" && journal.ToolDispatches == 0 && journal.InvokedAt == "" &&
			journal.FailureClass == "" && journal.FailurePartialSHA256 == "" && journal.FailedAt == ""
	}
	prefetchAt, err := time.Parse(time.RFC3339Nano, journal.PrefetchStartedAt)
	if err != nil || prefetchAt.Before(reservedAt) || strings.TrimSpace(journal.PrefetchLockOwner) == "" ||
		journal.PrefetchLockProcessID <= 0 {
		return false
	}
	if journal.PrefetchBaselineReceiptDigest != "" &&
		!validPipelineSealedConvergenceDigest(journal.PrefetchBaselineReceiptDigest) {
		return false
	}
	if journal.ToolDispatches == 0 {
		return journal.AccessReceiptDigest == "" && journal.ArgsSHA256 == "" && journal.InvokedAt == "" &&
			journal.FailureClass == "" && journal.FailurePartialSHA256 == "" && journal.FailedAt == ""
	}
	invokedAt, err := time.Parse(time.RFC3339Nano, journal.InvokedAt)
	if err != nil || invokedAt.Before(prefetchAt) ||
		!validPipelineSealedConvergenceDigest(journal.AccessReceiptDigest) ||
		!validPipelineSealedConvergenceDigest(journal.ArgsSHA256) {
		return false
	}
	if journal.FailureClass == "" {
		return journal.PlannerAttempt == intent.PlannerRepairAttempts &&
			journal.FailurePartialSHA256 == "" && journal.FailedAt == ""
	}
	failedAt, err := time.Parse(time.RFC3339Nano, journal.FailedAt)
	return err == nil && !failedAt.Before(invokedAt) &&
		pipelineSealedConvergenceToken(journal.FailureClass) == journal.FailureClass &&
		validPipelineSealedConvergenceDigest(journal.FailurePartialSHA256) &&
		validPipelineSealedConvergenceDigest(journal.AccessReceiptDigest) &&
		validPipelineSealedConvergenceDigest(journal.ArgsSHA256)
}

func pipelineSealedConvergenceTryHostFinalize(
	promptBundle assets.Bundle,
	style string,
	st *store.Store,
	chapter int,
	intent *pipelineSealedConvergenceReplanIntent,
	eligibility *pipelineSealedConvergenceReplanEligibility,
) (bool, error) {
	if st == nil || intent == nil || eligibility == nil || chapter <= 0 {
		return false, fmt.Errorf("sealed convergence Host finalize identity is incomplete")
	}
	// The first durable partial still belongs to the paid Planner attempt and
	// must receive its one normal continuation. Host-only adoption is the
	// terminal schema/provenance lane after that continuation has already used
	// its single dispatch and left a semantically authored partial behind.
	if intent.PlannerContinuation == nil || intent.PlannerContinuation.Dispatches != 1 {
		return false, nil
	}
	if intent.HostFinalize != nil && intent.HostFinalize.FailureClass != "" {
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
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("sealed convergence Host finalize read partial: %w", err)
	}
	var partial map[string]any
	if err := json.Unmarshal(partialRaw, &partial); err != nil {
		return false, fmt.Errorf("sealed convergence Host finalize decode partial: %w", err)
	}
	if err := validatePipelineSealedConvergenceContinuationPartial(
		st, chapter, partial, eligibility.Simulation,
	); err != nil {
		return false, err
	}
	partialSHA := pipelineBytesSHA(partialRaw)
	journal := intent.HostFinalize
	if journal == nil {
		journal = &pipelineSealedConvergenceHostFinalize{
			Version:              pipelineSealedConvergenceHostFinalizeVersion,
			PlannerAttempt:       intent.PlannerRepairAttempts,
			InitialPartialSHA256: partialSHA,
			ReservedAt:           time.Now().UTC().Format(time.RFC3339Nano),
		}
		intent.HostFinalize = journal
		if err := saveUpdatedPipelineSealedConvergenceReplanIntent(st.Dir(), intent, eligibility); err != nil {
			return false, fmt.Errorf("sealed convergence reserve Host finalize: %w", err)
		}
	} else if journal.Version != pipelineSealedConvergenceHostFinalizeVersion ||
		journal.PlannerAttempt != intent.PlannerRepairAttempts ||
		(journal.ToolDispatches == 0 && journal.InitialPartialSHA256 != partialSHA) {
		return false, fmt.Errorf("sealed convergence Host finalize partial/attempt binding drift")
	}
	if journal.ToolDispatches != 0 {
		receipt, loadErr := st.Runtime.LoadPlanningContextAccessReceipt(domain.PlanningContextAccessPlan)
		if loadErr != nil || receipt == nil || receipt.ReceiptDigest != journal.AccessReceiptDigest ||
			!receipt.ConsumedAt.IsZero() || partialSHA == journal.InitialPartialSHA256 {
			return false, fmt.Errorf("sealed convergence Host finalize already invoked %d/1 without an auditable pre-consume validation failure; automatic replay is forbidden", journal.ToolDispatches)
		}
		journal.FailureClass = "semantic_preconsume_validation"
		journal.FailurePartialSHA256 = partialSHA
		journal.FailedAt = time.Now().UTC().Format(time.RFC3339Nano)
		if err := saveUpdatedPipelineSealedConvergenceReplanIntent(st.Dir(), intent, eligibility); err != nil {
			return false, fmt.Errorf("sealed convergence persist Host finalize semantic failure: %w", err)
		}
		fmt.Fprintf(os.Stderr, "[pipeline:plan] 第 %d 章 Host-only metadata 收口已证明剩余缺口属于语义计划；转入剩余 Planner attempt，不重放 Host 工具\n", chapter)
		return false, nil
	}

	lock, err := pipelineSealedConvergenceCurrentReplacementLock(st, chapter)
	if err != nil {
		return false, err
	}
	baselineDigest := ""
	if baseline, loadErr := st.Runtime.LoadPlanningContextAccessReceipt(domain.PlanningContextAccessPlan); loadErr != nil {
		return false, fmt.Errorf("sealed convergence Host finalize load baseline access receipt: %w", loadErr)
	} else if baseline != nil {
		baselineDigest = baseline.ReceiptDigest
	}
	journal.PrefetchLockOwner = lock.Owner
	journal.PrefetchLockProcessID = lock.ProcessID
	journal.PrefetchStartedAt = time.Now().UTC().Format(time.RFC3339Nano)
	journal.PrefetchBaselineReceiptDigest = baselineDigest
	if err := saveUpdatedPipelineSealedConvergenceReplanIntent(st.Dir(), intent, eligibility); err != nil {
		return false, fmt.Errorf("sealed convergence persist Host finalize prefetch write-ahead: %w", err)
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
		return false, fmt.Errorf("sealed convergence Host finalize bind planning receipt: %w", err)
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
	journal.AccessReceiptDigest = receipt.ReceiptDigest
	journal.ArgsSHA256 = pipelineBytesSHA(args)
	journal.ToolDispatches = 1
	journal.InvokedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if err := saveUpdatedPipelineSealedConvergenceReplanIntent(st.Dir(), intent, eligibility); err != nil {
		return false, fmt.Errorf("sealed convergence persist Host finalize invocation 1/1: %w", err)
	}
	if _, err := tools.NewPlanDetailsTool(st).Execute(context.Background(), args); err != nil {
		return false, fmt.Errorf("sealed convergence Host-only plan_details finalize failed; no model retry permitted: %w", err)
	}
	cp, err = tools.CurrentChapterPlanCausalCheckpoint(store.NewStore(st.Dir()), chapter)
	if err != nil || cp == nil || cp.Seq <= intent.SourceFrozen.PlanCheckpointSeq {
		return false, fmt.Errorf("sealed convergence Host finalize returned without a successor checkpoint: %w", err)
	}
	fmt.Fprintf(os.Stderr, "[pipeline:plan] 第 %d 章 durable partial 已由 Host-only PlanDetailsTool 确定性收口；未调用 Planner 模型\n", chapter)
	return true, nil
}
