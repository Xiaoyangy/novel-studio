package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/chenhongyang/novel-studio/assets"
	"github.com/chenhongyang/novel-studio/internal/agents"
	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/entry/headless"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/tools"
)

const (
	pipelineSealedConvergenceReplanIntentVersion       = "pipeline-sealed-convergence-replan-intent.v2"
	pipelineSealedConvergenceReplanIntentLegacyVersion = "pipeline-sealed-convergence-replan-intent.v1"
	pipelineSealedConvergenceReplanIntentPath          = "meta/planning/convergence_replan_intent.json"
	pipelineSealedConvergenceReplanReceiptDir          = "meta/planning/convergence_replans"
	pipelineSealedConvergenceReplanFeedbackPath        = "meta/planning/current_convergence_replan_feedback.json"
)

type pipelineSealedConvergenceReplanSnapshot struct {
	ProgressDigest          string            `json:"progress_digest"`
	RealizationCursorDigest string            `json:"realization_cursor_digest"`
	ActiveGenerationDigest  string            `json:"active_generation_digest"`
	CompletedChapterSHA256  map[string]string `json:"completed_chapter_sha256"`
	TargetDraftSHA256       string            `json:"target_draft_sha256,omitempty"`
	TargetChapterSHA256     string            `json:"target_chapter_sha256,omitempty"`
	TargetCommitSeq         int64             `json:"target_commit_seq"`
}

// pipelineSealedConvergenceDiagnostics contains only classifications and
// forward-looking repair advice. Evidence excerpts, quoted lines and failed
// prose bodies are deliberately not representable in this schema.
type pipelineSealedConvergenceDiagnostics struct {
	BlockingDimensions []string `json:"blocking_dimensions"`
	IssueClasses       []string `json:"issue_classes"`
	MechanicalRules    []string `json:"mechanical_rules"`
	RevisionFocus      []string `json:"revision_focus"`
	EvidenceSources    []string `json:"evidence_sources"`
}

type pipelineSealedConvergencePlannerFailure struct {
	PlanDigest string `json:"plan_digest"`
	PlanSeq    int64  `json:"plan_seq"`
	Class      string `json:"class"`
}

const pipelineSealedConvergencePlannerContinuationVersion = "sealed-convergence-planner-continuation.v1"

// pipelineSealedConvergencePlannerContinuation journals a single recovery
// dispatch for an already-paid Planner attempt. The pointer field on the v2
// intent is omitempty, so intents written by older binaries retain their exact
// digest until the controller reserves this recovery under the current lock.
type pipelineSealedConvergencePlannerContinuation struct {
	Version                    string                                          `json:"version"`
	PlannerAttempt             int                                             `json:"planner_attempt"`
	InitialPartialSHA256       string                                          `json:"initial_partial_sha256"`
	SessionIdentity            string                                          `json:"session_identity"`
	Dispatches                 int                                             `json:"dispatches"`
	ReservedAt                 string                                          `json:"reserved_at"`
	DispatchedAt               string                                          `json:"dispatched_at,omitempty"`
	InitialAccessReceiptDigest string                                          `json:"initial_access_receipt_digest,omitempty"`
	InitialPromptSHA256        string                                          `json:"initial_prompt_sha256,omitempty"`
	ZeroSideEffectOutcome      *pipelineSealedConvergenceZeroSideEffectOutcome `json:"zero_side_effect_outcome,omitempty"`
	Replacement                *pipelineSealedConvergenceReplacementDispatch   `json:"replacement,omitempty"`
}

type pipelineSealedConvergenceReplanIntent struct {
	Version               string                                          `json:"version"`
	SourceFrozen          pipelineFrozenPlan                              `json:"source_frozen"`
	ExhaustedCandidate    string                                          `json:"exhausted_candidate_id"`
	ExhaustedLedgerSHA    string                                          `json:"exhausted_ledger_sha256"`
	FailedBodySHA256      []string                                        `json:"failed_body_sha256"`
	FailureCount          int                                             `json:"failure_count"`
	FailureLimit          int                                             `json:"failure_limit"`
	StateContractDigest   string                                          `json:"state_contract_digest"`
	Diagnostics           pipelineSealedConvergenceDiagnostics            `json:"diagnostics"`
	DiagnosticsDigest     string                                          `json:"diagnostics_digest"`
	ChapterWordBounds     tools.SealedShortChapterWordBounds              `json:"chapter_word_bounds"`
	PlannerRepairAttempts int                                             `json:"planner_repair_attempts"`
	PlannerRepairLimit    int                                             `json:"planner_repair_limit"`
	PlannerFailures       []pipelineSealedConvergencePlannerFailure       `json:"planner_failures,omitempty"`
	PlannerContinuation   *pipelineSealedConvergencePlannerContinuation   `json:"planner_continuation,omitempty"`
	HostFinalize          *pipelineSealedConvergenceHostFinalize          `json:"host_finalize,omitempty"`
	ExhaustedSeedFinalize *pipelineSealedConvergenceExhaustedSeedFinalize `json:"exhausted_seed_finalize,omitempty"`
	Snapshot              pipelineSealedConvergenceReplanSnapshot         `json:"snapshot"`
	StartedAt             string                                          `json:"started_at"`
	IntentDigest          string                                          `json:"intent_digest"`
	LegacyIntentDigest    string                                          `json:"-"`
}

// pipelineSealedConvergenceReplanIntentV1 is kept byte-shape compatible with
// the already-persisted v1 journal. Its field order is part of the deterministic
// digest protocol. V1 had already consumed paid Planner attempts, so recovery
// must validate this exact shape before deterministically adding word bounds.
type pipelineSealedConvergenceReplanIntentV1 struct {
	Version               string                                    `json:"version"`
	SourceFrozen          pipelineFrozenPlan                        `json:"source_frozen"`
	ExhaustedCandidate    string                                    `json:"exhausted_candidate_id"`
	ExhaustedLedgerSHA    string                                    `json:"exhausted_ledger_sha256"`
	FailedBodySHA256      []string                                  `json:"failed_body_sha256"`
	FailureCount          int                                       `json:"failure_count"`
	FailureLimit          int                                       `json:"failure_limit"`
	StateContractDigest   string                                    `json:"state_contract_digest"`
	Diagnostics           pipelineSealedConvergenceDiagnostics      `json:"diagnostics"`
	DiagnosticsDigest     string                                    `json:"diagnostics_digest"`
	PlannerRepairAttempts int                                       `json:"planner_repair_attempts"`
	PlannerRepairLimit    int                                       `json:"planner_repair_limit"`
	PlannerFailures       []pipelineSealedConvergencePlannerFailure `json:"planner_failures,omitempty"`
	Snapshot              pipelineSealedConvergenceReplanSnapshot   `json:"snapshot"`
	StartedAt             string                                    `json:"started_at"`
	IntentDigest          string                                    `json:"intent_digest"`
}

type pipelineSealedConvergenceReplanEligibility struct {
	Intent     pipelineSealedConvergenceReplanIntent
	Ledger     pipelineRenderConvergenceLedger
	Binding    pipelineSealedRenderBinding
	Plan       domain.ChapterPlan
	Simulation domain.ChapterWorldSimulation
}

type pipelineSealedConvergenceReplanFeedback struct {
	Version             string                               `json:"version"`
	Chapter             int                                  `json:"chapter"`
	CandidateID         string                               `json:"candidate_id"`
	LedgerSHA256        string                               `json:"ledger_sha256"`
	FailureCount        int                                  `json:"failure_count"`
	FailureLimit        int                                  `json:"failure_limit"`
	FailedBodySHA256    []string                             `json:"failed_body_sha256"`
	StateContractDigest string                               `json:"state_contract_digest"`
	Diagnostics         pipelineSealedConvergenceDiagnostics `json:"diagnostics"`
	DiagnosticsDigest   string                               `json:"diagnostics_digest"`
	ChapterWordBounds   tools.SealedShortChapterWordBounds   `json:"chapter_word_bounds"`
	PlannerTask         string                               `json:"planner_task"`
	Policy              string                               `json:"policy"`
}

// pipelineSealedConvergenceReplanAllowed is deliberately read-only.  The CLI
// entry uses it to distinguish the one safe sealed `plan --restart` operation
// from an ordinary attempt to bypass project-all/seal/promote.
func pipelineSealedConvergenceReplanAllowed(outputDir string, flags pipelineFlags, stages []string) error {
	if !flags.Restart || len(stages) != 1 || stages[0] != "plan" {
		return fmt.Errorf("sealed convergence replan 只允许单独执行 --stages plan --restart")
	}
	if pending, err := loadPipelineSealedConvergenceReplanIntent(outputDir); err != nil {
		return err
	} else if pending != nil {
		_, err := validatePipelineSealedConvergenceReplanIntent(outputDir, *pending)
		return err
	}
	_, err := inspectPipelineSealedConvergenceReplanEligibility(outputDir)
	return err
}

func inspectPipelineSealedConvergenceReplanEligibility(outputDir string) (*pipelineSealedConvergenceReplanEligibility, error) {
	st := store.NewStore(outputDir)
	frozen, _, err := loadAndVerifyPipelineFrozenPlan(outputDir)
	if err != nil {
		return nil, fmt.Errorf("sealed convergence replan 缺少当前可验证 frozen plan: %w", err)
	}
	if frozen.ProjectionBinding != "sealed_v2" {
		return nil, fmt.Errorf("sealed convergence replan 只接受 active sealed_v2 frozen plan")
	}
	if _, committed := pipelineCommittedAfterFrozenBaseline(st, frozen); committed {
		return nil, fmt.Errorf("sealed convergence replan 不得作用于已提交章节")
	}
	binding, err := validatePipelineSealedRenderBinding(st, frozen, false)
	if err != nil {
		return nil, fmt.Errorf("sealed convergence replan active promotion 不可验证: %w", err)
	}
	id, err := pipelineRenderTransactionID(frozen)
	if err != nil {
		return nil, err
	}
	manifest := pipelineRenderCandidateManifest{
		Version:                pipelineRenderCandidateManifestVersion,
		CandidateID:            id,
		GenerationID:           frozen.PlanningGenerationID,
		Chapter:                frozen.Chapter,
		PlanDigest:             frozen.PlanDigest,
		PlanCheckpointSeq:      frozen.PlanCheckpointSeq,
		ProjectedBundleDigest:  frozen.ProjectedBundleDigest,
		PromotionReceiptDigest: frozen.PromotionReceiptDigest,
		SourceOutputDir:        filepath.Clean(outputDir),
	}
	ledgerPath, err := pipelineRenderConvergenceLedgerPath(outputDir, id)
	if err != nil {
		return nil, err
	}
	ledgerRaw, err := os.ReadFile(ledgerPath)
	if err != nil {
		return nil, fmt.Errorf("sealed convergence replan 缺少 durable exhausted ledger: %w", err)
	}
	ledger, err := loadPipelineRenderConvergenceLedger(outputDir, manifest, 3)
	if err != nil {
		return nil, err
	}
	failureCount := pipelineRenderConvergenceFailureCount(ledger)
	if failureCount < ledger.FailureLimit {
		return nil, fmt.Errorf("sealed convergence replan 被拒绝：当前 plan 仅失败 %d/%d，必须继续 bounded render", failureCount, ledger.FailureLimit)
	}
	plan := binding.Bundle.ChapterPlan
	simulation := binding.Bundle.ChapterWorldSimulation
	stateDigest := pipelineSealedConvergenceStateContractDigest(plan, simulation, binding.Bundle.ProjectedDelta)
	diagnostics, err := collectPipelineSealedConvergenceDiagnostics(outputDir, manifest, ledger)
	if err != nil {
		return nil, err
	}
	snapshot, err := capturePipelineSealedConvergenceReplanSnapshot(st, frozen.Chapter)
	if err != nil {
		return nil, err
	}
	wordBounds, err := tools.InspectShortChapterWordBoundsFromAcceptedProse(st, frozen.Chapter)
	if err != nil {
		return nil, fmt.Errorf("sealed convergence replan resolve accepted-prose word budget: %w", err)
	}
	intent := pipelineSealedConvergenceReplanIntent{
		Version:             pipelineSealedConvergenceReplanIntentVersion,
		SourceFrozen:        *frozen,
		ExhaustedCandidate:  id,
		ExhaustedLedgerSHA:  pipelineBytesSHA(ledgerRaw),
		FailedBodySHA256:    pipelineRenderConvergenceFailedHashes(ledger),
		FailureCount:        failureCount,
		FailureLimit:        ledger.FailureLimit,
		StateContractDigest: stateDigest,
		Diagnostics:         diagnostics,
		DiagnosticsDigest:   pipelineProjectAllDigest(diagnostics),
		ChapterWordBounds:   wordBounds,
		PlannerRepairLimit:  2,
		Snapshot:            snapshot,
		StartedAt:           time.Now().UTC().Format(time.RFC3339Nano),
	}
	intent.IntentDigest = pipelineSealedConvergenceReplanIntentDigest(intent)
	return &pipelineSealedConvergenceReplanEligibility{
		Intent: intent, Ledger: *ledger, Binding: *binding, Plan: plan, Simulation: simulation,
	}, nil
}

func pipelineSealedConvergenceReplan(opts cliOptions, flags pipelineFlags) (returnErr error) {
	if !flags.Restart {
		return fmt.Errorf("sealed convergence replan 必须显式使用 --stages plan --restart")
	}
	cfg, promptBundle, err := loadCfgBundle(opts)
	if err != nil {
		return err
	}
	if err := ensurePipelineRAGReady(cfg); err != nil {
		return fmt.Errorf("sealed convergence replan RAG 就绪检查失败: %w", err)
	}

	var eligibility *pipelineSealedConvergenceReplanEligibility
	if pending, loadErr := loadPipelineSealedConvergenceReplanIntent(cfg.OutputDir); loadErr != nil {
		return loadErr
	} else if pending != nil {
		eligibility, err = validatePipelineSealedConvergenceReplanIntent(cfg.OutputDir, *pending)
	} else {
		eligibility, err = inspectPipelineSealedConvergenceReplanEligibility(cfg.OutputDir)
		if err == nil {
			err = savePipelineSealedConvergenceReplanIntent(cfg.OutputDir, eligibility.Intent)
		}
	}
	if err != nil {
		return err
	}
	intent := eligibility.Intent
	chapter := intent.SourceFrozen.Chapter
	st := store.NewStore(cfg.OutputDir)
	owner := pipelineExecutionOwner("convergence-replan", chapter)
	if err := st.Runtime.AcquirePipelineExecution(domain.PipelineExecutionLock{
		Mode:          domain.PipelineExecutionProjectAll,
		TargetChapter: chapter,
		Owner:         owner,
		ExpiresAt:     time.Now().UTC().Add(pipelineExecutionLease),
	}); err != nil {
		return fmt.Errorf("sealed convergence replan 获取 planning execution lock: %w", err)
	}
	defer func() {
		if releaseErr := st.Runtime.ReleasePipelineExecution(owner); releaseErr != nil && returnErr == nil {
			returnErr = fmt.Errorf("sealed convergence replan 释放 execution lock: %w", releaseErr)
		}
	}()
	eligibility, err = validatePipelineSealedConvergenceReplanIntent(cfg.OutputDir, intent)
	if err != nil {
		return err
	}
	intent = eligibility.Intent
	if err := persistPipelineSealedConvergenceReplanIntentMigration(
		cfg.OutputDir,
		&intent,
		owner,
	); err != nil {
		return err
	}
	eligibility.Intent = intent

	// Crash recovery after the successor frozen marker was published only needs
	// to retire the intent. Never pay for a second Planner call in this window.
	if frozen, _, frozenErr := loadAndVerifyPipelineFrozenPlan(cfg.OutputDir); frozenErr == nil &&
		pipelineSealedConvergenceSuccessorAlreadyPublished(frozen, intent) {
		if err := verifyPipelineSealedConvergenceReplanSnapshot(st, chapter, intent.Snapshot); err != nil {
			return err
		}
		return removePipelineSealedConvergenceReplanIntent(cfg.OutputDir)
	}

	if err := savePipelineSealedConvergencePlanningContext(st, eligibility.Binding.Generation, chapter); err != nil {
		return err
	}
	if err := savePipelineSealedConvergenceAuthorityOverlay(cfg.OutputDir, eligibility); err != nil {
		return err
	}
	if err := savePipelineSealedConvergenceReplanFeedback(cfg.OutputDir, eligibility); err != nil {
		return err
	}
	continuedPaidAttempt, err := pipelineSealedConvergenceTryHostFinalize(
		promptBundle,
		cfg.Style,
		st,
		chapter,
		&intent,
		eligibility,
	)
	if err != nil {
		return err
	}
	if !continuedPaidAttempt {
		continuedPaidAttempt, err = pipelineSealedConvergenceTryExhaustedSeedFinalize(
			cfg,
			promptBundle,
			st,
			chapter,
			&intent,
			eligibility,
		)
		if err != nil {
			return err
		}
	}
	// A paid Planner call can persist a valid staged partial and then lose its
	// process before plan_details finalizes the formal successor.  The remaining
	// repair budget is not evidence that this partial belongs to a fresh call:
	// resume the exact paid attempt first, whether it was attempt 1/2 or 2/2.
	// ContinuePaidPlannerAttempt validates the partial's simulation/source
	// identity before reserving its one-shot continuation journal.
	hostFinalizeFailed := intent.HostFinalize != nil && intent.HostFinalize.FailureClass != ""
	if !continuedPaidAttempt && !hostFinalizeFailed && pipelineSealedConvergencePaidAttemptContinuationEligible(intent) {
		candidateCP, checkpointErr := tools.CurrentChapterPlanCausalCheckpoint(st, chapter)
		if checkpointErr != nil && !errors.Is(checkpointErr, os.ErrNotExist) {
			return checkpointErr
		}
		if candidateCP == nil || candidateCP.Seq <= intent.SourceFrozen.PlanCheckpointSeq {
			if partial, partialErr := st.Drafts.LoadChapterPlanPartial(chapter); partialErr != nil {
				return fmt.Errorf("sealed convergence replan inspect paid-attempt partial: %w", partialErr)
			} else if partial != nil {
				continuedPaidAttempt = true
				if err := pipelineSealedConvergenceContinuePaidPlannerAttempt(
					cfg,
					promptBundle,
					st,
					chapter,
					&intent,
					eligibility,
				); err != nil {
					return err
				}
			}
		}
	}
	if !continuedPaidAttempt {
		if err := retirePipelinePlanPartial(st, chapter, "durably exhausted sealed render budget requires a fresh plan-only successor epoch"); err != nil {
			return err
		}
	}

	baselineSeq := intent.SourceFrozen.PlanCheckpointSeq
	var cp *domain.Checkpoint
	var replacementPlan *domain.ChapterPlan
	for {
		st = store.NewStore(cfg.OutputDir)
		candidateCP, checkpointErr := tools.CurrentChapterPlanCausalCheckpoint(st, chapter)
		if checkpointErr == nil && candidateCP != nil && candidateCP.Seq > baselineSeq {
			candidatePlan, loadErr := st.Drafts.LoadChapterPlan(chapter)
			failureClass, validationErr := validatePipelineSealedConvergenceSuccessorPlan(
				st,
				chapter,
				candidateCP,
				candidatePlan,
				intent,
				eligibility,
			)
			if loadErr != nil && validationErr == nil {
				failureClass, validationErr = "plan_unreadable", loadErr
			}
			if validationErr == nil {
				cp, replacementPlan = candidateCP, candidatePlan
				break
			}
			if !pipelineSealedConvergencePlannerFailureRecorded(intent.PlannerFailures, candidateCP.Digest) {
				intent.PlannerFailures = append(intent.PlannerFailures, pipelineSealedConvergencePlannerFailure{
					PlanDigest: candidateCP.Digest,
					PlanSeq:    candidateCP.Seq,
					Class:      failureClass,
				})
				if err := saveUpdatedPipelineSealedConvergenceReplanIntent(cfg.OutputDir, &intent, eligibility); err != nil {
					return err
				}
			}
			if err := retirePipelinePlanPartial(st, chapter, "invalid convergence successor requires bounded Planner repair"); err != nil {
				return err
			}
			if err := retirePipelineFormalPlan(st, chapter, candidateCP.Seq); err != nil {
				return err
			}
			if !pipelineSealedConvergencePlannerAttemptAvailable(intent) {
				return fmt.Errorf("sealed convergence replan Planner repair budget exhausted (%d/%d, last=%s); invalid plan retired, prose/canon/cursor unchanged",
					intent.PlannerRepairAttempts, intent.PlannerRepairLimit, failureClass)
			}
		}

		if !pipelineSealedConvergencePlannerAttemptAvailable(intent) {
			return fmt.Errorf("sealed convergence replan Planner repair budget exhausted (%d/%d); no valid successor was published and prose/canon/cursor remain unchanged",
				intent.PlannerRepairAttempts, intent.PlannerRepairLimit)
		}
		if err := retirePipelinePlanPartial(st, chapter, "durably exhausted sealed render budget requires a fresh plan-only successor epoch"); err != nil {
			return err
		}
		retireSeq := baselineSeq
		if candidateCP != nil {
			retireSeq = candidateCP.Seq
		}
		if err := retirePipelineFormalPlan(st, chapter, retireSeq); err != nil {
			return err
		}
		if _, err := st.Checkpoints.AppendArtifactLatestAcross(
			domain.ChapterScope(chapter),
			"chapter_world_simulation",
			fmt.Sprintf("meta/chapter_simulations/%03d.json", chapter),
			"plan", "chapter_world_simulation", "review", "draft-structural-block",
		); err != nil {
			return fmt.Errorf("sealed convergence replan reassert immutable world simulation: %w", err)
		}
		// Prefetch the one exact, compact planning context before spending the
		// remaining Planner attempt.  This signs the normal server-side access
		// receipt, but prevents a model from retrying a deterministic context
		// budget failure in a loop. A failed prefetch does not burn a paid attempt.
		planningContextRaw, err := tools.NewContextTool(
			st,
			promptBundle.References,
			cfg.Style,
		).Execute(context.Background(), json.RawMessage(fmt.Sprintf(
			`{"chapter":%d,"profile":"planning"}`,
			chapter,
		)))
		if err != nil {
			return fmt.Errorf("sealed convergence replan host-prefetch planning context: %w", err)
		}
		if len(planningContextRaw) == 0 {
			return fmt.Errorf("sealed convergence replan host-prefetch planning context is empty")
		}
		planningContextRaw, err = pipelineSealedConvergencePlanningContextWithWordBounds(
			planningContextRaw,
			intent.ChapterWordBounds,
		)
		if err != nil {
			return fmt.Errorf("sealed convergence replan attach accepted-prose word budget to planning context: %w", err)
		}

		// Persist the paid Planner call immediately before dispatch. A crash or
		// provider timeout cannot silently reopen an unbounded expensive retry.
		intent.PlannerRepairAttempts++
		if err := saveUpdatedPipelineSealedConvergenceReplanIntent(cfg.OutputDir, &intent, eligibility); err != nil {
			return err
		}
		writerSessionIdentity, err := pipelineSealedConvergenceFreshPlannerSessionIdentity(chapter, intent)
		if err != nil {
			return err
		}
		if err := savePipelineSealedConvergenceReplanFeedback(cfg.OutputDir, eligibility); err != nil {
			return err
		}
		steer := pipelineSealedConvergenceReplanTask(eligibility) + fmt.Sprintf(
			"\nHost 已代你完成本次唯一的 novel_context(chapter=%d, profile=planning) 并签发 access receipt；禁止再次调用 novel_context。直接消费下列权威 JSON，把 planning_context_access_receipt.source_token 原样写入 causal_simulation.context_sources，然后调用 plan_structure 与 plan_details：\n<host_prefetched_novel_context>\n%s\n</host_prefetched_novel_context>",
			chapter,
			string(planningContextRaw),
		)
		if err := st.RunMeta.SetPendingSteer(steer); err != nil {
			return fmt.Errorf("sealed convergence replan 写入 Planner 因果任务: %w", err)
		}
		if err := headless.Run(cfg, promptBundle, headless.Options{
			StopAfterPlanChapter:                       chapter,
			StopOnSealedConvergencePreconditionChapter: chapter,
			PreserveUserRules:                          true,
			SkipQueueReplay:                            true,
			WriterSessionIdentity:                      writerSessionIdentity,
		}); err != nil {
			return fmt.Errorf("sealed convergence replan 第 %d 章 Planner 尝试 %d/%d 失败: %w",
				chapter, intent.PlannerRepairAttempts, intent.PlannerRepairLimit, err)
		}
	}

	frozenContext, err := tools.FreezeDraftRenderContext(
		context.Background(),
		tools.NewContextTool(st, promptBundle.References, cfg.Style),
		chapter,
		cp.Digest,
	)
	if err != nil {
		return fmt.Errorf("sealed convergence replan freeze replacement render context: %w", err)
	}
	replacementSemanticDigest, err := domain.ComputeChapterPlanV2Digest(*replacementPlan)
	if err != nil {
		return err
	}
	receipt := domain.SealedConvergenceReplanReceipt{
		Version:                         domain.SealedConvergenceReplanReceiptVersion,
		GenerationID:                    intent.SourceFrozen.PlanningGenerationID,
		Chapter:                         chapter,
		BundleDigest:                    intent.SourceFrozen.ProjectedBundleDigest,
		PromotionReceiptDigest:          intent.SourceFrozen.PromotionReceiptDigest,
		SealedPlanSemanticDigest:        intent.SourceFrozen.ProjectedPlanSHA256,
		PreviousPlanCheckpointDigest:    intent.SourceFrozen.PlanDigest,
		PreviousPlanCheckpointSeq:       intent.SourceFrozen.PlanCheckpointSeq,
		PreviousReplanReceiptDigest:     intent.SourceFrozen.ConvergenceReplanReceiptDigest,
		ExhaustedCandidateID:            intent.ExhaustedCandidate,
		ExhaustedLedgerSHA256:           intent.ExhaustedLedgerSHA,
		FailedBodySHA256:                append([]string(nil), intent.FailedBodySHA256...),
		FailureCount:                    intent.FailureCount,
		FailureLimit:                    intent.FailureLimit,
		ReplacementPlanSemanticDigest:   replacementSemanticDigest,
		ReplacementPlanCheckpointDigest: cp.Digest,
		ReplacementPlanCheckpointSeq:    cp.Seq,
		ReplacementRenderContextSHA256:  frozenContext.PayloadSHA256,
		StateContractDigest:             intent.StateContractDigest,
		FeedbackDigest:                  intent.DiagnosticsDigest,
		CreatedAt:                       intent.StartedAt,
	}
	receipt.ReceiptDigest, err = domain.ComputeSealedConvergenceReplanReceiptDigest(receipt)
	if err != nil {
		return err
	}
	if err := domain.ValidateSealedConvergenceReplanReceipt(receipt); err != nil {
		return err
	}
	if err := savePipelineSealedConvergenceReplanReceipt(cfg.OutputDir, receipt); err != nil {
		return err
	}

	replacementFrozen := intent.SourceFrozen
	replacementFrozen.PlanDigest = cp.Digest
	replacementFrozen.PlanCheckpointSeq = cp.Seq
	replacementFrozen.RenderContextSHA256 = frozenContext.PayloadSHA256
	replacementFrozen.ConvergenceReplanReceiptDigest = receipt.ReceiptDigest
	// A new formal plan epoch is frozen under the already-current render
	// protocol, so it no longer needs the one-time pre-attempt upgrade pointer
	// from its exhausted predecessor. The immutable upgrade receipt remains on
	// disk; clearing only the live pointer prevents that older plan/context
	// identity from invalidating a legitimate later convergence successor.
	replacementFrozen.RenderInputUpgradeID = ""
	replacementFrozen.RenderInputUpgradeReceiptDigest = ""
	replacementFrozen.FrozenAt = time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := writePipelinePlanningJSON(
		filepath.Join(cfg.OutputDir, pipelineFrozenPlanPath),
		replacementFrozen,
	); err != nil {
		return fmt.Errorf("sealed convergence replan publish successor frozen plan: %w", err)
	}
	if _, _, err := loadAndVerifyPipelineFrozenPlan(cfg.OutputDir); err != nil {
		return fmt.Errorf("sealed convergence replan successor frozen plan self-check: %w", err)
	}
	if err := verifyPipelineSealedConvergenceReplanSnapshot(st, chapter, intent.Snapshot); err != nil {
		return err
	}
	_ = st.RunMeta.ClearPendingSteer()
	if err := removePipelineSealedConvergenceReplanIntent(cfg.OutputDir); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "[pipeline:plan] 第 %d 章 sealed render 已从 exhausted candidate %s 建立 plan successor #%d；原 bundle/promotion 与正史游标未变\n", chapter, intent.ExhaustedCandidate, cp.Seq)
	return nil
}

// pipelineSealedConvergenceFreshPlannerSessionIdentity deterministically binds
// one standard-headless Planner dispatch to the paid attempt journal. It is a
// logging identity only: the logical subagent remains writer, while pre-existing
// writer-chNN transcripts stay immutable and cannot be conflated with this run.
func pipelineSealedConvergenceFreshPlannerSessionIdentity(
	chapter int,
	intent pipelineSealedConvergenceReplanIntent,
) (string, error) {
	if chapter <= 0 || chapter != intent.SourceFrozen.Chapter || intent.PlannerRepairAttempts <= 0 {
		return "", fmt.Errorf("sealed convergence fresh Planner session identity is incomplete")
	}
	if intent.IntentDigest == "" || intent.IntentDigest != pipelineSealedConvergenceReplanIntentDigest(intent) {
		return "", fmt.Errorf("sealed convergence fresh Planner session identity requires exact persisted intent digest")
	}
	digest := strings.TrimPrefix(intent.IntentDigest, "sha256:")
	if len(digest) != 64 {
		return "", fmt.Errorf("sealed convergence fresh Planner intent digest is invalid")
	}
	for _, r := range digest {
		if !strings.ContainsRune("0123456789abcdef", r) {
			return "", fmt.Errorf("sealed convergence fresh Planner intent digest is invalid")
		}
	}
	return fmt.Sprintf(
		"convergence_planner_fresh_ch%02d_a%02d_%s",
		chapter,
		intent.PlannerRepairAttempts,
		digest[:16],
	), nil
}

func pipelineSealedConvergenceContinuePaidPlannerAttempt(
	cfg bootstrap.Config,
	promptBundle assets.Bundle,
	st *store.Store,
	chapter int,
	intent *pipelineSealedConvergenceReplanIntent,
	eligibility *pipelineSealedConvergenceReplanEligibility,
) error {
	if st == nil || intent == nil || eligibility == nil || chapter <= 0 {
		return fmt.Errorf("sealed convergence Planner continuation identity is incomplete")
	}
	if !pipelineSealedConvergencePaidAttemptContinuationEligible(*intent) {
		return fmt.Errorf("sealed convergence Planner continuation requires an exact paid attempt without a recorded Planner failure")
	}
	partialRaw, err := os.ReadFile(filepath.Join(st.Dir(), "drafts", fmt.Sprintf("%02d.plan.partial.json", chapter)))
	if err != nil {
		return fmt.Errorf("sealed convergence Planner continuation read exact partial: %w", err)
	}
	var partial map[string]any
	if err := json.Unmarshal(partialRaw, &partial); err != nil {
		return fmt.Errorf("sealed convergence Planner continuation decode exact partial: %w", err)
	}
	if err := validatePipelineSealedConvergenceContinuationPartial(
		st,
		chapter,
		partial,
		eligibility.Simulation,
	); err != nil {
		return err
	}
	partialSHA := pipelineBytesSHA(partialRaw)
	continuation := intent.PlannerContinuation
	if continuation == nil {
		continuation = &pipelineSealedConvergencePlannerContinuation{
			Version:              pipelineSealedConvergencePlannerContinuationVersion,
			PlannerAttempt:       intent.PlannerRepairAttempts,
			InitialPartialSHA256: partialSHA,
			SessionIdentity:      fmt.Sprintf("convergence_planner_continuation-ch%02d", chapter),
			ReservedAt:           time.Now().UTC().Format(time.RFC3339Nano),
		}
		intent.PlannerContinuation = continuation
		if err := saveUpdatedPipelineSealedConvergenceReplanIntent(st.Dir(), intent, eligibility); err != nil {
			return fmt.Errorf("sealed convergence Planner continuation reserve durable slot: %w", err)
		}
	} else if continuation.Version != pipelineSealedConvergencePlannerContinuationVersion ||
		continuation.PlannerAttempt != intent.PlannerRepairAttempts ||
		continuation.SessionIdentity != fmt.Sprintf("convergence_planner_continuation-ch%02d", chapter) {
		return fmt.Errorf("sealed convergence Planner continuation partial/session binding drift")
	}
	if continuation.Dispatches != 0 {
		return pipelineSealedConvergenceDispatchZeroSideEffectReplacement(
			cfg,
			promptBundle,
			st,
			chapter,
			intent,
			eligibility,
			partialSHA,
		)
	}
	if continuation.InitialPartialSHA256 != partialSHA {
		return fmt.Errorf("sealed convergence Planner continuation pre-dispatch partial binding drift")
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
	steer := pipelineSealedConvergenceReplanTask(eligibility) + fmt.Sprintf(
		"\n这是同一已付费 Planner attempt=%d 的唯一 continuation 1/1。drafts/%02d.plan.partial.json（sha256=%s）已由 Host 验证并必须原地续写；禁止 plan_structure，只调用 plan_details。Host 已重新预取本进程唯一 novel_context(chapter=%d, profile=planning)，把新的 planning_context_access_receipt.source_token 写入 causal_simulation.context_sources，按 chapter_plan_stage.gap_summary 补最小缺口并 finalize：\n<host_prefetched_novel_context>\n%s\n</host_prefetched_novel_context>",
		intent.PlannerRepairAttempts,
		chapter,
		partialSHA,
		chapter,
		string(planningContextRaw),
	)
	lock, err := pipelineSealedConvergenceCurrentReplacementLock(st, chapter)
	if err != nil {
		return err
	}
	receipt, err := pipelineSealedConvergenceCurrentUnconsumedPlanReceipt(st, *intent, lock)
	if err != nil {
		return fmt.Errorf("sealed convergence Planner continuation bind planning receipt: %w", err)
	}
	continuation.InitialAccessReceiptDigest = receipt.ReceiptDigest
	continuation.InitialPromptSHA256 = pipelineBytesSHA([]byte(steer))
	continuation.Dispatches = 1
	continuation.DispatchedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if err := saveUpdatedPipelineSealedConvergenceReplanIntent(st.Dir(), intent, eligibility); err != nil {
		return fmt.Errorf("sealed convergence Planner continuation persist dispatch 1/1: %w", err)
	}
	runErr := agents.RunSealedConvergencePlannerContinuation(
		context.Background(),
		cfg,
		promptBundle,
		st.Dir(),
		chapter,
		steer,
	)
	if runErr != nil {
		if agents.SealedConvergencePlannerTimedOutBeforeSideEffects(runErr) {
			if proofErr := pipelineSealedConvergenceRecordTypedTimeoutOutcome(
				st,
				intent,
				eligibility,
				partialSHA,
				runErr,
			); proofErr != nil {
				return fmt.Errorf("sealed convergence typed model-call timeout could not prove zero side effects: %w", proofErr)
			}
			return fmt.Errorf(
				"sealed convergence replan 第 %d 章 continuation model call hit its typed hard timeout before assistant/tool side effects; outcome proof persisted and a fresh-lock replacement 1/1 is now eligible: %w",
				chapter,
				runErr,
			)
		}
		return fmt.Errorf("sealed convergence replan 第 %d 章 paid Planner attempt %d continuation 1/1 failed: %w",
			chapter, intent.PlannerRepairAttempts, runErr)
	}
	cp, err := tools.CurrentChapterPlanCausalCheckpoint(store.NewStore(st.Dir()), chapter)
	if err != nil || cp == nil || cp.Seq <= intent.SourceFrozen.PlanCheckpointSeq {
		return fmt.Errorf("sealed convergence replan 第 %d 章 paid Planner continuation returned without a successor plan: %w",
			chapter, err)
	}
	return nil
}

func pipelineSealedConvergenceContinuationConsumedStoreError(
	st *store.Store,
	intent pipelineSealedConvergenceReplanIntent,
	currentPartialSHA string,
) error {
	base := pipelineSealedConvergenceContinuationConsumedError(intent, currentPartialSHA)
	if st == nil {
		return base
	}
	receipt, err := st.Runtime.LoadPlanningContextAccessReceipt(domain.PlanningContextAccessPlan)
	if err != nil || receipt == nil || receipt.ConsumedAt.IsZero() {
		return base
	}
	planPath := filepath.Join(
		st.Dir(),
		"drafts",
		fmt.Sprintf("%02d.plan.json", intent.SourceFrozen.Chapter),
	)
	planState := "formal plan file absent"
	if raw, readErr := os.ReadFile(planPath); readErr == nil {
		planState = "formal plan retained sha256=" + pipelineBytesSHA(raw)
	}
	return fmt.Errorf(
		"%v; terminal finalize crash window: the single-use planning access receipt was consumed at %s but no successor causal checkpoint exists (%s); partial and attempts remain untouched, manual control-plane recovery is required",
		base,
		receipt.ConsumedAt.UTC().Format(time.RFC3339Nano),
		planState,
	)
}

func pipelineSealedConvergenceContinuationConsumedError(
	intent pipelineSealedConvergenceReplanIntent,
	currentPartialSHA string,
) error {
	dispatches := 0
	initialSHA := ""
	if intent.PlannerContinuation != nil {
		dispatches = intent.PlannerContinuation.Dispatches
		initialSHA = intent.PlannerContinuation.InitialPartialSHA256
	}
	return fmt.Errorf(
		"sealed convergence Planner continuation already dispatched %d/1 for paid attempt %d/%d; fail-closed without a successor plan: attempts are not reset and staged partial is retained (initial=%s current=%s)",
		dispatches,
		intent.PlannerRepairAttempts,
		intent.PlannerRepairLimit,
		initialSHA,
		currentPartialSHA,
	)
}

func validatePipelineSealedConvergenceContinuationPartial(
	st *store.Store,
	chapter int,
	partial map[string]any,
	simulation domain.ChapterWorldSimulation,
) error {
	if st == nil || chapter <= 0 || partial == nil {
		return fmt.Errorf("sealed convergence Planner continuation partial is unavailable")
	}
	structure, ok := partial["structure"].(map[string]any)
	if !ok {
		return fmt.Errorf("sealed convergence Planner continuation requires a staged plan_structure partial")
	}
	if pipelineSealedConvergenceInt(structure["chapter"]) != chapter {
		return fmt.Errorf("sealed convergence Planner continuation partial chapter drift")
	}
	for _, field := range []string{"title", "goal", "conflict", "hook"} {
		if value, _ := structure[field].(string); strings.TrimSpace(value) == "" {
			return fmt.Errorf("sealed convergence Planner continuation partial missing structure.%s", field)
		}
	}
	if bound, _ := structure["_world_simulation_id"].(string); strings.TrimSpace(bound) != simulation.SimulationID {
		return fmt.Errorf("sealed convergence Planner continuation partial world simulation binding drift")
	}
	if err := tools.ValidateStagedChapterPlanSourceBinding(
		st,
		chapter,
		partial,
		simulation.SimulationID,
	); err != nil {
		return fmt.Errorf("sealed convergence Planner continuation partial source binding drift: %w", err)
	}
	if issues := tools.ChapterPlanIdentityIssues(st, chapter, partial); len(issues) > 0 {
		return fmt.Errorf("sealed convergence Planner continuation partial identity mismatch: %s", strings.Join(issues, "；"))
	}
	return nil
}

func pipelineSealedConvergenceInt(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case json.Number:
		parsed, _ := typed.Int64()
		return int(parsed)
	default:
		return 0
	}
}

func pipelineSealedConvergencePrefetchPlanningContext(
	st *store.Store,
	promptBundle assets.Bundle,
	style string,
	chapter int,
	bounds tools.SealedShortChapterWordBounds,
) (json.RawMessage, error) {
	raw, err := tools.NewContextTool(st, promptBundle.References, style).Execute(
		context.Background(),
		json.RawMessage(fmt.Sprintf(`{"chapter":%d,"profile":"planning"}`, chapter)),
	)
	if err != nil {
		return nil, fmt.Errorf("sealed convergence replan host-prefetch planning context: %w", err)
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("sealed convergence replan host-prefetch planning context is empty")
	}
	raw, err = pipelineSealedConvergencePlanningContextWithWordBounds(raw, bounds)
	if err != nil {
		return nil, fmt.Errorf("sealed convergence replan attach accepted-prose word budget to planning context: %w", err)
	}
	return pipelineSealedConvergenceCompactContinuationContext(raw)
}

func pipelineSealedConvergenceSuccessorAlreadyPublished(
	frozen *pipelineFrozenPlan,
	intent pipelineSealedConvergenceReplanIntent,
) bool {
	return frozen != nil &&
		strings.TrimSpace(frozen.ConvergenceReplanReceiptDigest) != "" &&
		frozen.ConvergenceReplanReceiptDigest != intent.SourceFrozen.ConvergenceReplanReceiptDigest &&
		frozen.PlanningGenerationID == intent.SourceFrozen.PlanningGenerationID &&
		frozen.Chapter == intent.SourceFrozen.Chapter &&
		frozen.ProjectedBundleDigest == intent.SourceFrozen.ProjectedBundleDigest &&
		frozen.PromotionReceiptDigest == intent.SourceFrozen.PromotionReceiptDigest
}

func pipelineSealedConvergenceStateContractDigest(
	plan domain.ChapterPlan,
	simulation domain.ChapterWorldSimulation,
	delta domain.ProjectedDelta,
) string {
	type invariantPlan struct {
		Chapter             int                                 `json:"chapter"`
		Title               string                              `json:"title"`
		RequiredBeats       []string                            `json:"required_beats"`
		ForbiddenMoves      []string                            `json:"forbidden_moves"`
		ContinuityChecks    []string                            `json:"continuity_checks"`
		PayoffPoints        []string                            `json:"payoff_points"`
		HookGoal            string                              `json:"hook_goal"`
		WorldSimulationID   string                              `json:"world_simulation_id"`
		ProtagonistDecision string                              `json:"protagonist_decision"`
		ProjectPromise      string                              `json:"project_promise"`
		ChapterFunction     string                              `json:"chapter_function"`
		OutcomeShift        []string                            `json:"outcome_shift"`
		ArcTransition       domain.ArcChapterTransitionContract `json:"arc_transition"`
		EndingContract      domain.EndingConsequenceContract    `json:"ending_contract"`
		RevealBudget        []string                            `json:"reveal_budget"`
		HardRenderContract  domain.HardRenderContractV2         `json:"hard_render_contract"`
	}
	return pipelineProjectAllDigest(invariantPlan{
		Chapter: plan.Chapter, Title: plan.Title,
		RequiredBeats:       append([]string(nil), plan.Contract.RequiredBeats...),
		ForbiddenMoves:      append([]string(nil), plan.Contract.ForbiddenMoves...),
		ContinuityChecks:    append([]string(nil), plan.Contract.ContinuityChecks...),
		PayoffPoints:        append([]string(nil), plan.Contract.PayoffPoints...),
		HookGoal:            plan.Contract.HookGoal,
		WorldSimulationID:   plan.CausalSimulation.WorldSimulationID,
		ProtagonistDecision: plan.CausalSimulation.ProtagonistDecision,
		ProjectPromise:      plan.CausalSimulation.ProjectPromise,
		ChapterFunction:     plan.CausalSimulation.ChapterFunction,
		OutcomeShift:        append([]string(nil), plan.CausalSimulation.OutcomeShift...),
		ArcTransition:       plan.CausalSimulation.ArcTransition,
		EndingContract:      plan.CausalSimulation.EndingContract,
		RevealBudget:        append([]string(nil), plan.CausalSimulation.ReaderRetentionPlan.RevealBudget...),
		HardRenderContract:  pipelineHardRenderContractV2(plan, simulation, delta),
	})
}

func capturePipelineSealedConvergenceReplanSnapshot(st *store.Store, chapter int) (pipelineSealedConvergenceReplanSnapshot, error) {
	var out pipelineSealedConvergenceReplanSnapshot
	progress, err := st.Progress.Load()
	if err != nil || progress == nil {
		return out, fmt.Errorf("sealed convergence replan snapshot progress: %w", err)
	}
	cursor, err := st.ProjectedV2().LoadRealizationCursor()
	if err != nil || cursor == nil {
		return out, fmt.Errorf("sealed convergence replan snapshot cursor: %w", err)
	}
	active, err := st.ProjectedV2().LoadActiveGeneration()
	if err != nil || active == nil {
		return out, fmt.Errorf("sealed convergence replan snapshot active generation: %w", err)
	}
	completed, err := pipelineCompletedChapterSHA256(st.Dir(), progress)
	if err != nil {
		return out, err
	}
	draftSHA, err := pipelineOptionalFileSHA(st.Dir(), fmt.Sprintf("drafts/%02d.draft.md", chapter))
	if err != nil {
		return out, err
	}
	chapterSHA, err := pipelineOptionalFileSHA(st.Dir(), fmt.Sprintf("chapters/%02d.md", chapter))
	if err != nil {
		return out, err
	}
	return pipelineSealedConvergenceReplanSnapshot{
		ProgressDigest:          pipelineProjectAllDigest(progress),
		RealizationCursorDigest: cursor.CursorDigest,
		ActiveGenerationDigest:  active.RecordDigest,
		CompletedChapterSHA256:  completed,
		TargetDraftSHA256:       draftSHA,
		TargetChapterSHA256:     chapterSHA,
		TargetCommitSeq:         latestPipelineChapterCommitSeq(st.Dir(), chapter),
	}, nil
}

func verifyPipelineSealedConvergenceReplanSnapshot(st *store.Store, chapter int, want pipelineSealedConvergenceReplanSnapshot) error {
	got, err := capturePipelineSealedConvergenceReplanSnapshot(st, chapter)
	if err != nil {
		return err
	}
	if got.ProgressDigest != want.ProgressDigest ||
		got.RealizationCursorDigest != want.RealizationCursorDigest ||
		got.ActiveGenerationDigest != want.ActiveGenerationDigest ||
		got.TargetDraftSHA256 != want.TargetDraftSHA256 ||
		got.TargetChapterSHA256 != want.TargetChapterSHA256 ||
		got.TargetCommitSeq != want.TargetCommitSeq ||
		!maps.Equal(got.CompletedChapterSHA256, want.CompletedChapterSHA256) {
		return fmt.Errorf("sealed convergence replan 越权改变了 progress/canon/prose/realization cursor；拒绝发布 successor binding")
	}
	return nil
}

func savePipelineSealedConvergencePlanningContext(st *store.Store, generation domain.PlanningGenerationV2, chapter int) error {
	bundles, err := st.ProjectedV2().LoadProjectedChapterBundles(generation.GenerationID)
	if err != nil {
		return err
	}
	prior := make([]domain.ProjectedChapterBundle, 0, len(bundles))
	for _, bundle := range bundles {
		if bundle.Chapter < chapter {
			prior = append(prior, bundle)
		}
	}
	registry, err := st.ProjectedV2().LoadObligationRegistry(generation.GenerationID)
	if err != nil || registry == nil {
		return fmt.Errorf("sealed convergence replan load obligation registry: %w", err)
	}
	projectedContext, err := domain.DeriveProjectedPlanningContextV2(generation, prior, *registry, chapter)
	if err != nil {
		return err
	}
	return savePipelineProjectAllPlanningContext(st, projectedContext)
}

func savePipelineSealedConvergenceAuthorityOverlay(
	outputDir string,
	eligibility *pipelineSealedConvergenceReplanEligibility,
) error {
	if eligibility == nil {
		return fmt.Errorf("sealed convergence authority overlay eligibility is nil")
	}
	overlay, err := tools.NewSealedConvergenceAuthorityOverlay(
		eligibility.Binding.Bundle,
		eligibility.Intent.SourceFrozen.PromotionReceiptDigest,
		eligibility.Intent.StateContractDigest,
		eligibility.Intent.StartedAt,
	)
	if err != nil {
		return err
	}
	_, err = writePipelinePlanningJSON(
		filepath.Join(outputDir, filepath.FromSlash(tools.SealedConvergenceAuthorityOverlayPath)),
		overlay,
	)
	return err
}

// collectPipelineSealedConvergenceDiagnostics reads only immutable evidence
// snapshots owned by the exhausted exact hashes.  It intentionally selects
// enum-like classifications and forward repair fields; summaries, comments,
// descriptions, evidence excerpts, raw responses and chapter bodies are never
// copied into the successor planning context.
func collectPipelineSealedConvergenceDiagnostics(
	liveOutputDir string,
	manifest pipelineRenderCandidateManifest,
	ledger *pipelineRenderConvergenceLedger,
) (pipelineSealedConvergenceDiagnostics, error) {
	var out pipelineSealedConvergenceDiagnostics
	if ledger == nil {
		return out, fmt.Errorf("sealed convergence diagnostics require exhausted ledger")
	}
	records := append([]pipelineRenderConvergenceRecord(nil), ledger.Records...)
	sort.Slice(records, func(i, j int) bool { return records[i].BodySHA256 < records[j].BodySHA256 })
	failedHashes := pipelineRenderConvergenceFailedHashes(ledger)
	failed := make(map[string]struct{}, len(failedHashes))
	for _, hash := range failedHashes {
		failed[hash] = struct{}{}
	}
	for _, record := range records {
		if _, rejected := failed[record.BodySHA256]; !rejected {
			continue
		}
		if record.ExternalBlocking {
			out.IssueClasses = append(out.IssueClasses, "external_ai:blocking")
		}
		if record.StructuralBlock {
			out.IssueClasses = append(out.IssueClasses, "local_structure:blocking")
		}
		if record.SemanticReject && !record.FormalAccepted {
			out.IssueClasses = append(out.IssueClasses, "editor_semantic:reject")
		}
		evidenceDir, err := pipelineRenderConvergenceEvidenceDir(liveOutputDir, manifest, record.BodySHA256)
		if err != nil {
			return out, err
		}
		addSource := func(rel string) {
			out.EvidenceSources = append(out.EvidenceSources, filepath.ToSlash(filepath.Join("evidence", record.BodySHA256, rel)))
		}

		reviewRel := filepath.Join("reviews", fmt.Sprintf("%02d.json", manifest.Chapter))
		var review domain.ReviewEntry
		if exists, err := readPipelineSealedConvergenceEvidenceJSON(filepath.Join(evidenceDir, reviewRel), &review); err != nil {
			return out, err
		} else if exists {
			if review.Chapter != manifest.Chapter || (review.BodySHA256 != "" && review.BodySHA256 != record.BodySHA256) {
				return out, fmt.Errorf("sealed convergence Editor evidence identity mismatch for %s", record.BodySHA256)
			}
			addSource(reviewRel)
			for _, dimension := range review.Dimensions {
				verdict := pipelineSealedConvergenceToken(dimension.Verdict)
				name := pipelineSealedConvergenceToken(dimension.Dimension)
				if name != "" && verdict != "" && verdict != "pass" {
					out.BlockingDimensions = append(out.BlockingDimensions, fmt.Sprintf("%s:%s:%d", name, verdict, dimension.Score))
				}
			}
			for _, issue := range review.Issues {
				kind := pipelineSealedConvergenceToken(issue.Type)
				severity := pipelineSealedConvergenceToken(issue.Severity)
				if kind != "" && severity != "" {
					out.IssueClasses = append(out.IssueClasses, kind+":"+severity)
				}
				if focus, ok := sanitizePipelineSealedConvergenceAdvice(issue.Suggestion, failedHashes); ok {
					out.RevisionFocus = append(out.RevisionFocus, focus)
				}
			}
		}

		gateRel := filepath.Join("reviews", fmt.Sprintf("%02d_ai_gate.json", manifest.Chapter))
		var gate struct {
			Chapter        int    `json:"chapter"`
			BodySHA256     string `json:"body_sha256"`
			RuleViolations []struct {
				Rule     string `json:"rule"`
				Severity string `json:"severity"`
			} `json:"rule_violations"`
			RewriteFocus  []string `json:"rewrite_focus"`
			AIGCGateCheck struct {
				RewriteFocus []string `json:"rewrite_focus"`
			} `json:"aigc_gate_check"`
		}
		if exists, err := readPipelineSealedConvergenceEvidenceJSON(filepath.Join(evidenceDir, gateRel), &gate); err != nil {
			return out, err
		} else if exists {
			if gate.Chapter != 0 && gate.Chapter != manifest.Chapter || gate.BodySHA256 != "" && gate.BodySHA256 != record.BodySHA256 {
				return out, fmt.Errorf("sealed convergence mechanical evidence identity mismatch for %s", record.BodySHA256)
			}
			addSource(gateRel)
			for _, violation := range gate.RuleViolations {
				rule := pipelineSealedConvergenceToken(violation.Rule)
				severity := pipelineSealedConvergenceToken(violation.Severity)
				if rule != "" {
					if severity == "" {
						severity = "unknown"
					}
					out.MechanicalRules = append(out.MechanicalRules, rule+":"+severity)
				}
			}
			for _, candidate := range append(append([]string(nil), gate.RewriteFocus...), gate.AIGCGateCheck.RewriteFocus...) {
				if focus, ok := sanitizePipelineSealedConvergenceAdvice(candidate, failedHashes); ok {
					out.RevisionFocus = append(out.RevisionFocus, focus)
				}
			}
		}

		judgeRel := filepath.Join("reviews", "drafts", fmt.Sprintf("%02d_deepseek_ai_judge.json", manifest.Chapter))
		var judge deepseekAIJudgeArtifact
		if exists, err := readPipelineSealedConvergenceEvidenceJSON(filepath.Join(evidenceDir, judgeRel), &judge); err != nil {
			return out, err
		} else if exists {
			if judge.Chapter != manifest.Chapter || judge.BodySHA256 != record.BodySHA256 ||
				validateDeepSeekAIJudgeArtifactIdentity(&judge, judge.CachePolicy) != nil {
				return out, fmt.Errorf("sealed convergence DeepSeek evidence identity mismatch for %s", record.BodySHA256)
			}
			addSource(judgeRel)
			if risk := pipelineSealedConvergenceToken(judge.RiskLevel); risk != "" {
				out.IssueClasses = append(out.IssueClasses, "external_ai:"+risk)
			}
			for _, candidate := range append(append(append([]string(nil), judge.RevisionPlan...), judge.DialogueFixPlan...), judge.AuthorVoicePlan...) {
				if focus, ok := sanitizePipelineSealedConvergenceAdvice(candidate, failedHashes); ok {
					out.RevisionFocus = append(out.RevisionFocus, focus)
				}
			}
		}

		requirementRel := filepath.Join("reviews", "drafts", fmt.Sprintf("%02d_full_rerender_required.json", manifest.Chapter))
		var requirement tools.DraftExternalRerenderRequirement
		if exists, err := readPipelineSealedConvergenceEvidenceJSON(filepath.Join(evidenceDir, requirementRel), &requirement); err != nil {
			return out, err
		} else if exists {
			if requirement.Chapter != manifest.Chapter || requirement.EvaluatedBodySHA256 != record.BodySHA256 {
				return out, fmt.Errorf("sealed convergence rerender evidence identity mismatch for %s", record.BodySHA256)
			}
			addSource(requirementRel)
			if source := pipelineSealedConvergenceToken(requirement.Source); source != "" {
				out.IssueClasses = append(out.IssueClasses, "rerender:"+source)
			}
			for _, candidate := range requirement.RevisionPlan {
				if focus, ok := sanitizePipelineSealedConvergenceAdvice(candidate, failedHashes); ok {
					out.RevisionFocus = append(out.RevisionFocus, focus)
				}
			}
		}

		briefRel := filepath.Join("reviews", fmt.Sprintf("%02d_rewrite_brief.md", manifest.Chapter))
		briefRaw, err := os.ReadFile(filepath.Join(evidenceDir, briefRel))
		if err != nil && !os.IsNotExist(err) {
			return out, err
		}
		if err == nil {
			addSource(briefRel)
			briefDimensions, briefFocus := pipelineSealedConvergenceRewriteBriefDiagnostics(string(briefRaw), failedHashes)
			out.BlockingDimensions = append(out.BlockingDimensions, briefDimensions...)
			out.RevisionFocus = append(out.RevisionFocus, briefFocus...)
		}
	}
	// A synchronous local structural block is journaled before the outer
	// candidate snapshotter runs. Its current full-rerender marker therefore
	// lives in the still-preserved active candidate rather than the per-hash
	// evidence directory. Read only that typed marker, after proving the nested
	// candidate manifest is the exact exhausted transaction.
	activeCandidateOutput := filepath.Join(pipelineRenderCandidateRoot(liveOutputDir), manifest.CandidateID, "output")
	if activeManifest, err := loadPipelineRenderCandidateManifest(activeCandidateOutput); err != nil {
		return out, err
	} else if activeManifest != nil {
		if activeManifest.CandidateID != manifest.CandidateID ||
			activeManifest.GenerationID != manifest.GenerationID ||
			activeManifest.Chapter != manifest.Chapter ||
			activeManifest.PlanDigest != manifest.PlanDigest ||
			activeManifest.PlanCheckpointSeq != manifest.PlanCheckpointSeq ||
			activeManifest.ProjectedBundleDigest != manifest.ProjectedBundleDigest ||
			activeManifest.PromotionReceiptDigest != manifest.PromotionReceiptDigest ||
			filepath.Clean(activeManifest.SourceOutputDir) != filepath.Clean(liveOutputDir) {
			return out, fmt.Errorf("sealed convergence active candidate manifest identity mismatch")
		}
		rel := filepath.Join("reviews", "drafts", fmt.Sprintf("%02d_full_rerender_required.json", manifest.Chapter))
		var requirement tools.DraftExternalRerenderRequirement
		if exists, err := readPipelineSealedConvergenceEvidenceJSON(filepath.Join(activeCandidateOutput, rel), &requirement); err != nil {
			return out, err
		} else if exists {
			if requirement.Chapter != manifest.Chapter {
				return out, fmt.Errorf("sealed convergence active structural marker chapter mismatch")
			}
			if _, rejected := failed[requirement.EvaluatedBodySHA256]; !rejected {
				return out, fmt.Errorf("sealed convergence active structural marker is not bound to exhausted exact body")
			}
			out.EvidenceSources = append(out.EvidenceSources, filepath.ToSlash(filepath.Join("active_candidate", rel)))
			if source := pipelineSealedConvergenceToken(requirement.Source); source != "" {
				out.IssueClasses = append(out.IssueClasses, "rerender:"+source)
			}
			for _, candidate := range requirement.RevisionPlan {
				if focus, ok := sanitizePipelineSealedConvergenceAdvice(candidate, failedHashes); ok {
					out.RevisionFocus = append(out.RevisionFocus, focus)
				}
			}
		}
	}
	out.BlockingDimensions = pipelineSealedConvergenceUniqueLimited(out.BlockingDimensions, 24)
	out.IssueClasses = pipelineSealedConvergenceUniqueLimited(out.IssueClasses, 24)
	out.MechanicalRules = pipelineSealedConvergenceUniqueLimited(out.MechanicalRules, 24)
	out.RevisionFocus = pipelineSealedConvergenceUniqueLimited(out.RevisionFocus, 24)
	out.EvidenceSources = pipelineSealedConvergenceUniqueLimited(out.EvidenceSources, 64)
	return out, nil
}

func readPipelineSealedConvergenceEvidenceJSON(path string, target any) (bool, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return false, fmt.Errorf("decode sealed convergence evidence %s: %w", filepath.Base(path), err)
	}
	return true, nil
}

func pipelineSealedConvergenceToken(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" || utf8.RuneCountInString(value) > 64 {
		return ""
	}
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("_-.:/", r) {
			continue
		}
		return ""
	}
	return value
}

func sanitizePipelineSealedConvergenceAdvice(value string, failedHashes []string) (string, bool) {
	value = strings.TrimSpace(value)
	value = strings.TrimSpace(strings.TrimPrefix(value, "修法："))
	value = strings.Join(strings.Fields(value), " ")
	if utf8.RuneCountInString(value) < 4 || utf8.RuneCountInString(value) > 220 ||
		strings.ContainsAny(value, "\n\r`\"'“”‘’「」『』《》") {
		return "", false
	}
	lower := strings.ToLower(value)
	for _, forbidden := range []string{
		"原文", "正文片段", "逐字", "摘录", "引文", "引用内容",
		"raw_response", "evidence", "summary", "chapter_body", "body text",
	} {
		if strings.Contains(lower, forbidden) {
			return "", false
		}
	}
	for _, hash := range failedHashes {
		if strings.Contains(value, hash) {
			return "", false
		}
	}
	return value, true
}

func pipelineSealedConvergenceRewriteBriefDiagnostics(value string, failedHashes []string) ([]string, []string) {
	section := ""
	var dimensions, focus []string
	for _, rawLine := range strings.Split(value, "\n") {
		line := strings.TrimSpace(rawLine)
		if strings.HasPrefix(line, "## ") {
			section = strings.TrimSpace(strings.TrimPrefix(line, "## "))
			continue
		}
		if section != "必须修正" && section != "汇总改写建议" {
			continue
		}
		item := strings.TrimSpace(strings.TrimPrefix(line, "-"))
		if item == line || item == "" {
			continue
		}
		if strings.HasPrefix(item, "[维度:") {
			if end := strings.Index(item, "]"); end > len("[维度:") {
				parts := strings.Split(strings.TrimPrefix(item[:end], "[维度:"), "/")
				if len(parts) >= 2 {
					name := pipelineSealedConvergenceToken(parts[0])
					verdict := pipelineSealedConvergenceToken(parts[1])
					if name != "" && verdict != "" && verdict != "pass" {
						dimensions = append(dimensions, name+":"+verdict)
					}
				}
			}
			continue
		}
		// In 必须修正, issue descriptions and nested evidence may contain
		// chapter language. Only the explicitly forward-looking 修法 field is
		// eligible. A dedicated 汇总改写建议 section is already action-only.
		if section == "必须修正" && !strings.HasPrefix(item, "修法：") {
			continue
		}
		if sanitized, ok := sanitizePipelineSealedConvergenceAdvice(item, failedHashes); ok {
			focus = append(focus, sanitized)
		}
	}
	return dimensions, focus
}

func pipelineSealedConvergenceUniqueLimited(values []string, limit int) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

func pipelineSealedConvergenceReplanTask(eligibility *pipelineSealedConvergenceReplanEligibility) string {
	if eligibility == nil {
		return "Pipeline convergence replan：只重建当前章 plan，禁止正文与正史 mutation。"
	}
	plan := eligibility.Plan
	contract, _ := json.Marshal(map[string]any{
		"title":                       plan.Title,
		"required_beats":              plan.Contract.RequiredBeats,
		"forbidden_moves":             plan.Contract.ForbiddenMoves,
		"continuity_checks":           plan.Contract.ContinuityChecks,
		"payoff_points":               plan.Contract.PayoffPoints,
		"hook_goal":                   plan.Contract.HookGoal,
		"world_simulation_id":         plan.CausalSimulation.WorldSimulationID,
		"protagonist_decision":        plan.CausalSimulation.ProtagonistDecision,
		"project_promise":             plan.CausalSimulation.ProjectPromise,
		"chapter_function":            plan.CausalSimulation.ChapterFunction,
		"outcome_shift":               plan.CausalSimulation.OutcomeShift,
		"arc_transition_contract":     plan.CausalSimulation.ArcTransition,
		"ending_consequence_contract": plan.CausalSimulation.EndingContract,
		"reveal_budget":               plan.CausalSimulation.ReaderRetentionPlan.RevealBudget,
	})
	diagnostics, _ := json.Marshal(eligibility.Intent.Diagnostics)
	plannerFailures, _ := json.Marshal(eligibility.Intent.PlannerFailures)
	wordBudget := ""
	if bounds := eligibility.Intent.ChapterWordBounds; bounds.Active {
		wordBudget = fmt.Sprintf(
			"本章基于已验收实际正文的动态字数合同为 %d-%d 字（此前%d章累计%d字；全书%d-%d字；单章绝对范围%d-%d）。replacement plan 的 causal_simulation.render_capacity.total_target_runes 必须落在 %d-%d，scene_spine 也必须真实承载该区间；这是硬合同，不得仍按旧的较低目标规划。",
			bounds.Min,
			bounds.Max,
			bounds.PriorAcceptedChapters,
			bounds.PriorAcceptedRunes,
			bounds.BookMin,
			bounds.BookMax,
			bounds.ChapterMin,
			bounds.ChapterMax,
			bounds.Min,
			bounds.Max,
		)
	}
	semanticRepair := ""
	if failure := eligibility.Intent.HostFinalize; failure != nil && failure.FailureClass != "" {
		semanticRepair = "Host-only provenance 收口已完成，剩余是正式计划语义门禁，不得再让模型抄 receipt 元数据。"
	}
	return fmt.Sprintf(
		"Pipeline convergence replan：第%d章同一 plan 的 %d 个 exact-body 已达到上限 %d；本次为有界 Planner 尝试 %d/%d，先前无效 successor 仅记录分类=%s。Host 会预取本次唯一 planning context；禁止自行调用 novel_context，只调用 plan_structure、plan_details，让 Planner 重组 causal_beats、decision_points、render_capacity、场景阻力、声口和反 AIGC 执行；禁止 simulate_chapter_world、draft/edit/commit、read_chapter 和任何正史 mutation。%s%s下列 immutable state contract 必须逐字段原样保留，不得同义改写：%s。仅使用下列经过净化、由失败证据归纳出的 plan-owned diagnostics 进行定向重排（digest=%s）：%s。失败 hashes=%s；不得读取、复用或拼接任一失败正文表面，也不得要求取得评审 evidence/summary/引文。",
		plan.Chapter,
		eligibility.Intent.FailureCount,
		eligibility.Intent.FailureLimit,
		eligibility.Intent.PlannerRepairAttempts,
		eligibility.Intent.PlannerRepairLimit,
		string(plannerFailures),
		wordBudget,
		semanticRepair,
		string(contract),
		eligibility.Intent.DiagnosticsDigest,
		string(diagnostics),
		strings.Join(eligibility.Intent.FailedBodySHA256, ","),
	)
}

func pipelineSealedConvergencePlanningContextWithWordBounds(
	raw json.RawMessage,
	bounds tools.SealedShortChapterWordBounds,
) (json.RawMessage, error) {
	if !bounds.Active {
		return raw, nil
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}
	payload["sealed_short_accepted_prose_word_budget"] = map[string]any{
		"chapter":                 bounds.Chapter,
		"required_runes_min":      bounds.Min,
		"required_runes_max":      bounds.Max,
		"prior_accepted_chapters": bounds.PriorAcceptedChapters,
		"prior_accepted_runes":    bounds.PriorAcceptedRunes,
		"book_min":                bounds.BookMin,
		"book_max":                bounds.BookMax,
		"absolute_chapter_min":    bounds.ChapterMin,
		"absolute_chapter_max":    bounds.ChapterMax,
		"render_capacity_policy": fmt.Sprintf(
			"causal_simulation.render_capacity.total_target_runes 必须落在 %d-%d，scene_spine 必须真实承载该区间",
			bounds.Min,
			bounds.Max,
		),
	}
	return json.Marshal(payload)
}

func savePipelineSealedConvergenceReplanFeedback(outputDir string, eligibility *pipelineSealedConvergenceReplanEligibility) error {
	if eligibility == nil {
		return fmt.Errorf("sealed convergence replan feedback is nil")
	}
	payload := pipelineSealedConvergenceReplanFeedback{
		Version:             pipelineSealedConvergenceReplanIntentVersion,
		Chapter:             eligibility.Intent.SourceFrozen.Chapter,
		CandidateID:         eligibility.Intent.ExhaustedCandidate,
		LedgerSHA256:        eligibility.Intent.ExhaustedLedgerSHA,
		FailureCount:        eligibility.Intent.FailureCount,
		FailureLimit:        eligibility.Intent.FailureLimit,
		FailedBodySHA256:    append([]string(nil), eligibility.Intent.FailedBodySHA256...),
		StateContractDigest: eligibility.Intent.StateContractDigest,
		Diagnostics:         eligibility.Intent.Diagnostics,
		DiagnosticsDigest:   eligibility.Intent.DiagnosticsDigest,
		ChapterWordBounds:   eligibility.Intent.ChapterWordBounds,
		PlannerTask:         pipelineSealedConvergenceReplanTask(eligibility),
		Policy:              "failed prose bytes, review evidence, summaries and quotations are not copied into planning; only classifications and sanitized forward repair actions are visible, while the immutable sealed simulation/state contract remains authoritative",
	}
	_, err := writePipelinePlanningJSON(filepath.Join(outputDir, pipelineSealedConvergenceReplanFeedbackPath), payload)
	return err
}

func pipelineSealedConvergenceReplanIntentDigest(intent pipelineSealedConvergenceReplanIntent) string {
	intent.IntentDigest = ""
	intent.FailedBodySHA256 = append([]string(nil), intent.FailedBodySHA256...)
	sort.Strings(intent.FailedBodySHA256)
	return pipelineProjectAllDigest(intent)
}

func pipelineSealedConvergenceReplanIntentV1Digest(intent pipelineSealedConvergenceReplanIntentV1) string {
	intent.IntentDigest = ""
	intent.FailedBodySHA256 = append([]string(nil), intent.FailedBodySHA256...)
	sort.Strings(intent.FailedBodySHA256)
	return pipelineProjectAllDigest(intent)
}

func migratePipelineSealedConvergenceReplanIntentV1(
	legacy pipelineSealedConvergenceReplanIntentV1,
	bounds tools.SealedShortChapterWordBounds,
) pipelineSealedConvergenceReplanIntent {
	intent := pipelineSealedConvergenceReplanIntent{
		Version:               pipelineSealedConvergenceReplanIntentVersion,
		SourceFrozen:          legacy.SourceFrozen,
		ExhaustedCandidate:    legacy.ExhaustedCandidate,
		ExhaustedLedgerSHA:    legacy.ExhaustedLedgerSHA,
		FailedBodySHA256:      append([]string(nil), legacy.FailedBodySHA256...),
		FailureCount:          legacy.FailureCount,
		FailureLimit:          legacy.FailureLimit,
		StateContractDigest:   legacy.StateContractDigest,
		Diagnostics:           legacy.Diagnostics,
		DiagnosticsDigest:     legacy.DiagnosticsDigest,
		ChapterWordBounds:     bounds,
		PlannerRepairAttempts: legacy.PlannerRepairAttempts,
		PlannerRepairLimit:    legacy.PlannerRepairLimit,
		PlannerFailures:       append([]pipelineSealedConvergencePlannerFailure(nil), legacy.PlannerFailures...),
		Snapshot:              legacy.Snapshot,
		StartedAt:             legacy.StartedAt,
		LegacyIntentDigest:    legacy.IntentDigest,
	}
	intent.IntentDigest = pipelineSealedConvergenceReplanIntentDigest(intent)
	return intent
}

func saveUpdatedPipelineSealedConvergenceReplanIntent(
	outputDir string,
	intent *pipelineSealedConvergenceReplanIntent,
	eligibility *pipelineSealedConvergenceReplanEligibility,
) error {
	if intent == nil || eligibility == nil {
		return fmt.Errorf("sealed convergence replan intent update is nil")
	}
	intent.IntentDigest = pipelineSealedConvergenceReplanIntentDigest(*intent)
	if err := savePipelineSealedConvergenceReplanIntent(outputDir, *intent); err != nil {
		return err
	}
	eligibility.Intent = *intent
	return nil
}

func pipelineSealedConvergencePlannerFailureRecorded(
	values []pipelineSealedConvergencePlannerFailure,
	digest string,
) bool {
	for _, value := range values {
		if value.PlanDigest == digest {
			return true
		}
	}
	return false
}

func pipelineSealedConvergencePlannerAttemptAvailable(intent pipelineSealedConvergenceReplanIntent) bool {
	return intent.PlannerRepairLimit == 2 &&
		intent.PlannerRepairAttempts >= 0 &&
		intent.PlannerRepairAttempts < intent.PlannerRepairLimit
}

func pipelineSealedConvergencePaidAttemptContinuationEligible(intent pipelineSealedConvergenceReplanIntent) bool {
	return intent.PlannerRepairLimit == 2 &&
		intent.PlannerRepairAttempts > 0 &&
		intent.PlannerRepairAttempts <= intent.PlannerRepairLimit &&
		len(intent.PlannerFailures) == 0
}

func validatePipelineSealedConvergenceSuccessorPlan(
	st *store.Store,
	chapter int,
	cp *domain.Checkpoint,
	plan *domain.ChapterPlan,
	intent pipelineSealedConvergenceReplanIntent,
	eligibility *pipelineSealedConvergenceReplanEligibility,
) (string, error) {
	if st == nil || cp == nil || plan == nil || eligibility == nil {
		return "plan_unreadable", fmt.Errorf("replacement plan/checkpoint is unavailable")
	}
	if cp.Seq <= intent.SourceFrozen.PlanCheckpointSeq {
		return "checkpoint_not_successor", fmt.Errorf("replacement checkpoint is not newer than baseline")
	}
	if err := verifyPipelineSealedConvergenceReplanSnapshot(st, chapter, intent.Snapshot); err != nil {
		return "control_plane_drift", err
	}
	if got := pipelineSealedConvergenceStateContractDigest(
		*plan,
		eligibility.Simulation,
		eligibility.Binding.Bundle.ProjectedDelta,
	); got != intent.StateContractDigest {
		return "state_contract_drift", fmt.Errorf("immutable state contract digest mismatch")
	}
	if err := tools.ValidateCurrentChapterRenderPlanForExecution(st, chapter); err != nil {
		return "render_plan_invalid", err
	}
	return "", nil
}

func savePipelineSealedConvergenceReplanIntent(outputDir string, intent pipelineSealedConvergenceReplanIntent) error {
	if intent.IntentDigest != pipelineSealedConvergenceReplanIntentDigest(intent) {
		return fmt.Errorf("sealed convergence replan intent digest mismatch")
	}
	_, err := writePipelinePlanningJSON(filepath.Join(outputDir, pipelineSealedConvergenceReplanIntentPath), intent)
	return err
}

func loadPipelineSealedConvergenceReplanIntent(outputDir string) (*pipelineSealedConvergenceReplanIntent, error) {
	path := filepath.Join(outputDir, pipelineSealedConvergenceReplanIntentPath)
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var envelope struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, err
	}
	switch envelope.Version {
	case pipelineSealedConvergenceReplanIntentVersion:
		var intent pipelineSealedConvergenceReplanIntent
		if err := json.Unmarshal(raw, &intent); err != nil {
			return nil, err
		}
		if intent.IntentDigest != pipelineSealedConvergenceReplanIntentDigest(intent) {
			return nil, fmt.Errorf("sealed convergence replan intent is invalid")
		}
		return &intent, nil
	case pipelineSealedConvergenceReplanIntentLegacyVersion:
		var legacy pipelineSealedConvergenceReplanIntentV1
		if err := json.Unmarshal(raw, &legacy); err != nil {
			return nil, err
		}
		if legacy.IntentDigest != pipelineSealedConvergenceReplanIntentV1Digest(legacy) {
			return nil, fmt.Errorf("sealed convergence replan legacy intent is invalid")
		}
		bounds, err := tools.InspectShortChapterWordBoundsFromAcceptedProse(
			store.NewStore(outputDir),
			legacy.SourceFrozen.Chapter,
		)
		if err != nil {
			return nil, fmt.Errorf("sealed convergence replan migrate legacy accepted-prose word budget: %w", err)
		}
		intent := migratePipelineSealedConvergenceReplanIntentV1(legacy, bounds)
		return &intent, nil
	default:
		return nil, fmt.Errorf("sealed convergence replan intent is invalid")
	}
}

func persistPipelineSealedConvergenceReplanIntentMigration(
	outputDir string,
	intent *pipelineSealedConvergenceReplanIntent,
	owner string,
) error {
	if intent == nil || intent.LegacyIntentDigest == "" {
		return nil
	}
	if intent.Version != pipelineSealedConvergenceReplanIntentVersion ||
		intent.IntentDigest != pipelineSealedConvergenceReplanIntentDigest(*intent) {
		return fmt.Errorf("sealed convergence replan migrated intent is invalid")
	}
	lock, err := store.NewStore(outputDir).Runtime.LoadPipelineExecution()
	if err != nil {
		return fmt.Errorf("load convergence migration execution lock: %w", err)
	}
	if lock == nil || lock.Mode != domain.PipelineExecutionProjectAll ||
		lock.TargetChapter != intent.SourceFrozen.Chapter ||
		lock.Owner != owner || lock.ProcessID != os.Getpid() {
		return fmt.Errorf("sealed convergence replan intent migration requires the current process's exact planning lock")
	}
	copy := *intent
	copy.LegacyIntentDigest = ""
	if err := savePipelineSealedConvergenceReplanIntent(outputDir, copy); err != nil {
		return fmt.Errorf("persist sealed convergence replan v1-to-v2 migration: %w", err)
	}
	intent.LegacyIntentDigest = ""
	return nil
}

func validatePipelineSealedConvergenceReplanIntent(outputDir string, intent pipelineSealedConvergenceReplanIntent) (*pipelineSealedConvergenceReplanEligibility, error) {
	if intent.Version != pipelineSealedConvergenceReplanIntentVersion ||
		intent.IntentDigest != pipelineSealedConvergenceReplanIntentDigest(intent) ||
		intent.DiagnosticsDigest != pipelineProjectAllDigest(intent.Diagnostics) ||
		intent.PlannerRepairLimit != 2 || intent.PlannerRepairAttempts < 0 ||
		intent.PlannerRepairAttempts > intent.PlannerRepairLimit ||
		!validPipelineSealedConvergencePlannerJournal(intent) {
		return nil, fmt.Errorf("sealed convergence replan intent identity mismatch")
	}
	st := store.NewStore(outputDir)
	projected := st.ProjectedV2()
	active, err := projected.LoadActiveGeneration()
	if err != nil || active == nil || active.GenerationID != intent.SourceFrozen.PlanningGenerationID {
		return nil, fmt.Errorf("sealed convergence replan active generation drift: %w", err)
	}
	cursor, err := projected.LoadRealizationCursor()
	if err != nil || cursor == nil ||
		cursor.CursorDigest != intent.Snapshot.RealizationCursorDigest ||
		cursor.ActivePromotedChapter != intent.SourceFrozen.Chapter ||
		cursor.ActivePromotionReceiptDigest != intent.SourceFrozen.PromotionReceiptDigest {
		return nil, fmt.Errorf("sealed convergence replan realization cursor drift: %w", err)
	}
	generation, err := projected.LoadSealedGeneration(active.GenerationID)
	if err != nil || generation == nil {
		return nil, fmt.Errorf("sealed convergence replan generation unavailable: %w", err)
	}
	bundle, err := pipelineProjectAllBundleForChapter(projected, active.GenerationID, intent.SourceFrozen.Chapter)
	if err != nil || bundle == nil || bundle.BundleDigest != intent.SourceFrozen.ProjectedBundleDigest {
		return nil, fmt.Errorf("sealed convergence replan exact bundle drift: %w", err)
	}
	promotion, err := projected.LoadPromotionReceipt(active.GenerationID, intent.SourceFrozen.Chapter, intent.SourceFrozen.PromotionReceiptDigest)
	if err != nil || promotion == nil {
		return nil, fmt.Errorf("sealed convergence replan promotion unavailable: %w", err)
	}
	ledgerPath, err := pipelineRenderConvergenceLedgerPath(outputDir, intent.ExhaustedCandidate)
	if err != nil {
		return nil, err
	}
	ledgerRaw, err := os.ReadFile(ledgerPath)
	if err != nil || pipelineBytesSHA(ledgerRaw) != intent.ExhaustedLedgerSHA {
		return nil, fmt.Errorf("sealed convergence replan exhausted ledger drift: %w", err)
	}
	var ledger pipelineRenderConvergenceLedger
	if err := json.Unmarshal(ledgerRaw, &ledger); err != nil {
		return nil, err
	}
	manifest := pipelineRenderCandidateManifest{
		CandidateID:            intent.ExhaustedCandidate,
		GenerationID:           intent.SourceFrozen.PlanningGenerationID,
		Chapter:                intent.SourceFrozen.Chapter,
		PlanDigest:             intent.SourceFrozen.PlanDigest,
		PlanCheckpointSeq:      intent.SourceFrozen.PlanCheckpointSeq,
		ProjectedBundleDigest:  intent.SourceFrozen.ProjectedBundleDigest,
		PromotionReceiptDigest: intent.SourceFrozen.PromotionReceiptDigest,
	}
	if err := validatePipelineRenderConvergenceLedger(&ledger, manifest); err != nil ||
		pipelineRenderConvergenceFailureCount(&ledger) < ledger.FailureLimit ||
		!reflect.DeepEqual(pipelineRenderConvergenceFailedHashes(&ledger), intent.FailedBodySHA256) {
		return nil, fmt.Errorf("sealed convergence replan ledger no longer proves exact exhausted set: %w", err)
	}
	diagnostics, err := collectPipelineSealedConvergenceDiagnostics(outputDir, manifest, &ledger)
	if err != nil {
		return nil, err
	}
	if !reflect.DeepEqual(diagnostics, intent.Diagnostics) ||
		pipelineProjectAllDigest(diagnostics) != intent.DiagnosticsDigest {
		return nil, fmt.Errorf("sealed convergence replan diagnostics drift")
	}
	plan := bundle.ChapterPlan
	simulation := bundle.ChapterWorldSimulation
	if pipelineSealedConvergenceStateContractDigest(plan, simulation, bundle.ProjectedDelta) != intent.StateContractDigest {
		return nil, fmt.Errorf("sealed convergence replan immutable state contract drift")
	}
	if err := verifyPipelineSealedConvergenceReplanSnapshot(st, intent.SourceFrozen.Chapter, intent.Snapshot); err != nil {
		return nil, err
	}
	wordBounds, err := tools.InspectShortChapterWordBoundsFromAcceptedProse(st, intent.SourceFrozen.Chapter)
	if err != nil {
		return nil, fmt.Errorf("sealed convergence replan accepted-prose word budget unavailable: %w", err)
	}
	if !reflect.DeepEqual(wordBounds, intent.ChapterWordBounds) {
		return nil, fmt.Errorf("sealed convergence replan accepted-prose word budget drift")
	}
	return &pipelineSealedConvergenceReplanEligibility{
		Intent: intent,
		Ledger: ledger,
		Binding: pipelineSealedRenderBinding{
			Active: *active, Cursor: *cursor, Generation: *generation, Bundle: *bundle, Promotion: *promotion,
		},
		Plan: plan, Simulation: simulation,
	}, nil
}

func validPipelineSealedConvergencePlannerJournal(intent pipelineSealedConvergenceReplanIntent) bool {
	if len(intent.PlannerFailures) > intent.PlannerRepairAttempts {
		return false
	}
	if continuation := intent.PlannerContinuation; continuation != nil {
		currentAttempt := continuation.PlannerAttempt == intent.PlannerRepairAttempts
		historicalHostAttempt := intent.HostFinalize != nil && intent.HostFinalize.FailureClass != "" &&
			continuation.PlannerAttempt == intent.HostFinalize.PlannerAttempt &&
			intent.PlannerRepairAttempts == continuation.PlannerAttempt+1 &&
			intent.PlannerRepairAttempts == intent.PlannerRepairLimit
		if continuation.Version != pipelineSealedConvergencePlannerContinuationVersion ||
			!pipelineSealedConvergencePaidAttemptContinuationEligible(intent) ||
			len(intent.PlannerFailures) != 0 ||
			(!currentAttempt && !historicalHostAttempt) ||
			continuation.Dispatches < 0 || continuation.Dispatches > 1 ||
			!validPipelineSealedConvergenceDigest(continuation.InitialPartialSHA256) ||
			continuation.SessionIdentity != fmt.Sprintf(
				"convergence_planner_continuation-ch%02d",
				intent.SourceFrozen.Chapter,
			) {
			return false
		}
		if _, err := time.Parse(time.RFC3339Nano, continuation.ReservedAt); err != nil {
			return false
		}
		if continuation.Dispatches == 0 {
			if continuation.DispatchedAt != "" {
				return false
			}
		} else if dispatched, err := time.Parse(time.RFC3339Nano, continuation.DispatchedAt); err != nil ||
			dispatched.Before(mustPipelineSealedConvergenceTime(continuation.ReservedAt)) {
			return false
		}
		if !validPipelineSealedConvergenceContinuationExtension(intent, continuation) {
			return false
		}
	}
	if !validPipelineSealedConvergenceHostFinalizeJournal(intent) {
		return false
	}
	if !validPipelineSealedConvergenceExhaustedSeedJournal(intent) {
		return false
	}
	seen := make(map[string]struct{}, len(intent.PlannerFailures))
	for _, failure := range intent.PlannerFailures {
		if !strings.HasPrefix(failure.PlanDigest, domain.PlanningV2DigestPrefix) ||
			failure.PlanSeq <= intent.SourceFrozen.PlanCheckpointSeq ||
			pipelineSealedConvergenceToken(failure.Class) != failure.Class {
			return false
		}
		if _, duplicate := seen[failure.PlanDigest]; duplicate {
			return false
		}
		seen[failure.PlanDigest] = struct{}{}
	}
	return true
}

func validPipelineSealedConvergenceDigest(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) != len("sha256:")+64 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	for _, r := range strings.TrimPrefix(value, "sha256:") {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

func mustPipelineSealedConvergenceTime(value string) time.Time {
	parsed, _ := time.Parse(time.RFC3339Nano, value)
	return parsed
}

func savePipelineSealedConvergenceReplanReceipt(outputDir string, receipt domain.SealedConvergenceReplanReceipt) error {
	if err := domain.ValidateSealedConvergenceReplanReceipt(receipt); err != nil {
		return err
	}
	path := filepath.Join(outputDir, pipelineSealedConvergenceReplanReceiptDir, receipt.ReceiptDigest+".json")
	if raw, err := os.ReadFile(path); err == nil {
		var existing domain.SealedConvergenceReplanReceipt
		if json.Unmarshal(raw, &existing) == nil && reflect.DeepEqual(existing, receipt) {
			return nil
		}
		return fmt.Errorf("sealed convergence replan receipt digest collision")
	} else if !os.IsNotExist(err) {
		return err
	}
	_, err := writePipelinePlanningJSON(path, receipt)
	return err
}

func loadAndVerifyPipelineSealedConvergenceReplanReceipt(
	outputDir string,
	frozen *pipelineFrozenPlan,
	bundle *domain.ProjectedChapterBundle,
	livePlanSemanticDigest string,
) (*domain.SealedConvergenceReplanReceipt, error) {
	if frozen == nil || bundle == nil || strings.TrimSpace(frozen.ConvergenceReplanReceiptDigest) == "" {
		return nil, fmt.Errorf("convergence successor receipt identity is incomplete")
	}
	digest := strings.TrimSpace(frozen.ConvergenceReplanReceiptDigest)
	if filepath.Base(digest) != digest || strings.ContainsAny(digest, `/\\`) {
		return nil, fmt.Errorf("convergence successor receipt digest is malformed")
	}
	path := filepath.Join(outputDir, pipelineSealedConvergenceReplanReceiptDir, digest+".json")
	var receipt domain.SealedConvergenceReplanReceipt
	if err := readPipelinePlanningJSON(path, &receipt); err != nil {
		return nil, err
	}
	if err := domain.ValidateSealedConvergenceReplanReceipt(receipt); err != nil {
		return nil, err
	}
	sealedPlanDigest, err := domain.ComputeChapterPlanV2Digest(bundle.ChapterPlan)
	if err != nil {
		return nil, err
	}
	livePlan, err := store.NewStore(outputDir).Drafts.LoadChapterPlan(frozen.Chapter)
	if err != nil || livePlan == nil {
		return nil, fmt.Errorf("convergence successor live plan unavailable: %w", err)
	}
	loadedLivePlanSemanticDigest, err := domain.ComputeChapterPlanV2Digest(*livePlan)
	if err != nil {
		return nil, err
	}
	if receipt.ReceiptDigest != digest ||
		receipt.GenerationID != frozen.PlanningGenerationID ||
		receipt.Chapter != frozen.Chapter ||
		receipt.BundleDigest != frozen.ProjectedBundleDigest ||
		receipt.BundleDigest != bundle.BundleDigest ||
		receipt.PromotionReceiptDigest != frozen.PromotionReceiptDigest ||
		receipt.SealedPlanSemanticDigest != frozen.ProjectedPlanSHA256 ||
		receipt.SealedPlanSemanticDigest != sealedPlanDigest ||
		receipt.PreviousReplanReceiptDigest == receipt.ReceiptDigest ||
		receipt.ReplacementPlanSemanticDigest != livePlanSemanticDigest ||
		receipt.ReplacementPlanSemanticDigest != loadedLivePlanSemanticDigest ||
		receipt.ReplacementPlanCheckpointDigest != frozen.PlanDigest ||
		receipt.ReplacementPlanCheckpointSeq != frozen.PlanCheckpointSeq ||
		receipt.ReplacementRenderContextSHA256 != frozen.RenderContextSHA256 ||
		receipt.StateContractDigest != pipelineSealedConvergenceStateContractDigest(
			*livePlan,
			bundle.ChapterWorldSimulation,
			bundle.ProjectedDelta,
		) {
		return nil, fmt.Errorf("convergence successor receipt does not bind exact sealed/current plan state")
	}
	ledgerPath, err := pipelineRenderConvergenceLedgerPath(outputDir, receipt.ExhaustedCandidateID)
	if err != nil {
		return nil, err
	}
	ledgerRaw, err := os.ReadFile(ledgerPath)
	if err != nil || pipelineBytesSHA(ledgerRaw) != receipt.ExhaustedLedgerSHA256 {
		return nil, fmt.Errorf("convergence successor exhausted ledger drift: %w", err)
	}
	var ledger pipelineRenderConvergenceLedger
	if err := json.Unmarshal(ledgerRaw, &ledger); err != nil {
		return nil, fmt.Errorf("decode convergence successor exhausted ledger: %w", err)
	}
	manifest := pipelineRenderCandidateManifest{
		CandidateID:            receipt.ExhaustedCandidateID,
		GenerationID:           receipt.GenerationID,
		Chapter:                receipt.Chapter,
		PlanDigest:             receipt.PreviousPlanCheckpointDigest,
		PlanCheckpointSeq:      receipt.PreviousPlanCheckpointSeq,
		ProjectedBundleDigest:  receipt.BundleDigest,
		PromotionReceiptDigest: receipt.PromotionReceiptDigest,
	}
	if err := validatePipelineRenderConvergenceLedger(&ledger, manifest); err != nil ||
		ledger.FailureLimit != receipt.FailureLimit ||
		pipelineRenderConvergenceFailureCount(&ledger) != receipt.FailureCount ||
		!reflect.DeepEqual(pipelineRenderConvergenceFailedHashes(&ledger), receipt.FailedBodySHA256) {
		return nil, fmt.Errorf("convergence successor exhausted ledger identity mismatch: %w", err)
	}
	var feedback pipelineSealedConvergenceReplanFeedback
	if err := readPipelinePlanningJSON(filepath.Join(outputDir, pipelineSealedConvergenceReplanFeedbackPath), &feedback); err != nil {
		return nil, fmt.Errorf("convergence successor feedback unavailable: %w", err)
	}
	if feedback.Version != pipelineSealedConvergenceReplanIntentVersion ||
		feedback.Chapter != receipt.Chapter ||
		feedback.CandidateID != receipt.ExhaustedCandidateID ||
		feedback.LedgerSHA256 != receipt.ExhaustedLedgerSHA256 ||
		feedback.FailureCount != receipt.FailureCount || feedback.FailureLimit != receipt.FailureLimit ||
		!reflect.DeepEqual(feedback.FailedBodySHA256, receipt.FailedBodySHA256) ||
		feedback.StateContractDigest != receipt.StateContractDigest ||
		feedback.DiagnosticsDigest != receipt.FeedbackDigest ||
		feedback.DiagnosticsDigest != pipelineProjectAllDigest(feedback.Diagnostics) {
		return nil, fmt.Errorf("convergence successor feedback binding mismatch")
	}
	return &receipt, nil
}

func removePipelineSealedConvergenceReplanIntent(outputDir string) error {
	err := os.Remove(filepath.Join(outputDir, pipelineSealedConvergenceReplanIntentPath))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
