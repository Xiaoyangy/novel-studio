package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/chenhongyang/novel-studio/assets"
	"github.com/chenhongyang/novel-studio/internal/agents"
	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/tools"
	"github.com/voocel/agentcore"
)

var pipelineSealedConvergenceSeededMutableKeys = []string{
	"anti_ai_execution_plan",
	"causal_beats",
	"decision_points",
	"emotional_logic",
	"literary_rendering_plan",
	"render_capacity",
	"voice_logic",
}

var pipelineSealedConvergenceImmutableSeedKeys = []string{
	"arc_transition_contract",
	"chapter_function",
	"context_sources",
	"ending_consequence_contract",
	"external_reference_plan",
	"initial_state",
	"outcome_shift",
	"project_promise",
	"reader_retention_plan",
}

// validPipelineSealedConvergenceSeededCompactJournal validates every durable
// write-ahead phase independently. In particular, a reserved Host seed or
// compact model dispatch can never be interpreted as a fresh retry budget.
func validPipelineSealedConvergenceSeededCompactJournal(
	intent pipelineSealedConvergenceReplanIntent,
	recovery *pipelineSealedConvergenceSeededCompactFinalize,
	binaryZeroRecorded time.Time,
) bool {
	if recovery == nil {
		return true
	}
	if recovery.Version != pipelineSealedConvergenceSeededCompactVersion ||
		recovery.SessionIdentity != fmt.Sprintf("convergence_planner_seeded_compact_finalize-ch%02d", intent.SourceFrozen.Chapter) ||
		!reflect.DeepEqual(recovery.AllowedMutableKeys, pipelineSealedConvergenceSeededMutableKeys) ||
		recovery.InitialPartialSHA256 != intent.PlannerContinuation.InitialPartialSHA256 ||
		!validPipelineSealedConvergenceDigest(recovery.InitialPartialSHA256) ||
		recovery.SeedToolDispatches < 0 || recovery.SeedToolDispatches > 1 ||
		recovery.ModelDispatches < 0 || recovery.ModelDispatches > 1 {
		return false
	}
	reserved, err := time.Parse(time.RFC3339Nano, recovery.ReservedAt)
	if err != nil || reserved.Before(binaryZeroRecorded) {
		return false
	}
	if recovery.PrefetchLockOwner == "" {
		return recovery.PrefetchLockProcessID == 0 && recovery.PrefetchStartedAt == "" &&
			recovery.PrefetchBaselineReceiptDigest == "" && recovery.AccessReceiptDigest == "" &&
			recovery.ImmutableSeedDigest == "" && recovery.SeedArgsSHA256 == "" &&
			recovery.SeedToolDispatches == 0 && recovery.SeedInvokedAt == "" &&
			recovery.SeedPartialSHA256 == "" && recovery.SeededAt == "" &&
			recovery.PromptSHA256 == "" && recovery.PromptRunes == 0 &&
			recovery.ModelDispatches == 0 && recovery.DispatchedAt == ""
	}
	if recovery.PrefetchLockProcessID <= 0 ||
		!validPipelineSealedConvergenceDigest(recovery.PrefetchBaselineReceiptDigest) {
		return false
	}
	prefetchStarted, err := time.Parse(time.RFC3339Nano, recovery.PrefetchStartedAt)
	if err != nil || prefetchStarted.Before(reserved) {
		return false
	}
	if recovery.SeedToolDispatches == 0 {
		return recovery.AccessReceiptDigest == "" && recovery.ImmutableSeedDigest == "" &&
			recovery.SeedArgsSHA256 == "" && recovery.SeedInvokedAt == "" &&
			recovery.SeedPartialSHA256 == "" && recovery.SeededAt == "" &&
			recovery.PromptSHA256 == "" && recovery.PromptRunes == 0 &&
			recovery.ModelDispatches == 0 && recovery.DispatchedAt == ""
	}
	if !validPipelineSealedConvergenceDigest(recovery.AccessReceiptDigest) ||
		!validPipelineSealedConvergenceDigest(recovery.ImmutableSeedDigest) ||
		!validPipelineSealedConvergenceDigest(recovery.SeedArgsSHA256) {
		return false
	}
	seedInvoked, err := time.Parse(time.RFC3339Nano, recovery.SeedInvokedAt)
	if err != nil || seedInvoked.Before(prefetchStarted) {
		return false
	}
	if recovery.SeedPartialSHA256 == "" {
		return recovery.SeededAt == "" && recovery.PromptSHA256 == "" && recovery.PromptRunes == 0 &&
			recovery.ModelDispatches == 0 && recovery.DispatchedAt == ""
	}
	if !validPipelineSealedConvergenceDigest(recovery.SeedPartialSHA256) {
		return false
	}
	seeded, err := time.Parse(time.RFC3339Nano, recovery.SeededAt)
	if err != nil || seeded.Before(seedInvoked) {
		return false
	}
	if recovery.ModelDispatches == 0 {
		return recovery.PromptSHA256 == "" && recovery.PromptRunes == 0 && recovery.DispatchedAt == ""
	}
	if !validPipelineSealedConvergenceDigest(recovery.PromptSHA256) || recovery.PromptRunes <= 0 ||
		recovery.PromptRunes > pipelineSealedConvergenceSeededPromptMaxRunes {
		return false
	}
	dispatched, err := time.Parse(time.RFC3339Nano, recovery.DispatchedAt)
	return err == nil && !dispatched.Before(seeded)
}

// pipelineSealedConvergenceSeededCompactFinalizeRecovery is reachable only
// after all three earlier model sessions have an exact terminal zero-effect
// proof. The Host first seeds only sealed state/grounding through the formal
// plan_details tool, then gives one compact, allowlisted Planner session the
// remaining mutable design work.
func pipelineSealedConvergenceSeededCompactFinalizeRecovery(
	cfg bootstrap.Config,
	promptBundle assets.Bundle,
	st *store.Store,
	chapter int,
	intent *pipelineSealedConvergenceReplanIntent,
	eligibility *pipelineSealedConvergenceReplanEligibility,
	partialSHA string,
) error {
	if st == nil || intent == nil || eligibility == nil || chapter <= 0 ||
		intent.PlannerContinuation == nil || intent.PlannerContinuation.Replacement == nil ||
		intent.PlannerContinuation.Replacement.BinaryFailover == nil {
		return fmt.Errorf("sealed convergence seeded compact recovery identity is incomplete")
	}
	failover := intent.PlannerContinuation.Replacement.BinaryFailover
	if failover.Dispatches != 1 {
		return fmt.Errorf("sealed convergence seeded compact recovery requires binary failover dispatch 1/1")
	}

	if failover.ZeroSideEffectOutcome == nil {
		outcome, err := pipelineSealedConvergenceCaptureBinaryZeroSideEffect(st, *intent, partialSHA)
		if err != nil {
			return fmt.Errorf("sealed convergence binary zero-side-effect proof rejected: %w", err)
		}
		failover.ZeroSideEffectOutcome = outcome
		if err := saveUpdatedPipelineSealedConvergenceReplanIntent(st.Dir(), intent, eligibility); err != nil {
			return fmt.Errorf("sealed convergence persist binary zero-side-effect outcome: %w", err)
		}
	}
	if err := pipelineSealedConvergenceValidateBinaryZeroSideEffectEvidence(st, *intent); err != nil {
		return err
	}

	recovery := failover.SeededCompactFinalize
	if recovery == nil {
		recovery = &pipelineSealedConvergenceSeededCompactFinalize{
			Version:              pipelineSealedConvergenceSeededCompactVersion,
			SessionIdentity:      fmt.Sprintf("convergence_planner_seeded_compact_finalize-ch%02d", chapter),
			AllowedMutableKeys:   append([]string(nil), pipelineSealedConvergenceSeededMutableKeys...),
			ReservedAt:           time.Now().UTC().Format(time.RFC3339Nano),
			InitialPartialSHA256: intent.PlannerContinuation.InitialPartialSHA256,
		}
		failover.SeededCompactFinalize = recovery
		if err := saveUpdatedPipelineSealedConvergenceReplanIntent(st.Dir(), intent, eligibility); err != nil {
			return fmt.Errorf("sealed convergence reserve seeded compact finalize: %w", err)
		}
	}
	if recovery.ModelDispatches != 0 {
		return fmt.Errorf("sealed convergence seeded compact finalize already dispatched %d/1; no further model dispatch is permitted", recovery.ModelDispatches)
	}

	currentRaw, err := os.ReadFile(filepath.Join(st.Dir(), "drafts", fmt.Sprintf("%02d.plan.partial.json", chapter)))
	if err != nil {
		return fmt.Errorf("sealed convergence read seeded compact partial: %w", err)
	}
	currentSHA := pipelineBytesSHA(currentRaw)
	if recovery.SeedPartialSHA256 == "" {
		switch {
		case currentSHA == recovery.InitialPartialSHA256 && recovery.SeedToolDispatches == 0:
			if err := pipelineSealedConvergenceApplyImmutableSeed(
				promptBundle, cfg.Style, st, chapter, intent, eligibility, recovery,
			); err != nil {
				return err
			}
			currentRaw, err = os.ReadFile(filepath.Join(st.Dir(), "drafts", fmt.Sprintf("%02d.plan.partial.json", chapter)))
			if err != nil {
				return err
			}
			currentSHA = pipelineBytesSHA(currentRaw)
		case currentSHA == recovery.InitialPartialSHA256:
			return fmt.Errorf("sealed convergence immutable seed dispatch 1/1 was reserved but has no auditable partial side effect; automatic replay is forbidden")
		case recovery.SeedToolDispatches != 1:
			return fmt.Errorf("sealed convergence seeded compact partial changed without a reserved Host seed dispatch")
		default:
			// Crash recovery for: Execute saved the exact partial, then the
			// process died before SeedPartialSHA256 was journaled. Audit the
			// complete side effect and only seal its existing SHA; never replay.
			if err := pipelineSealedConvergenceRequireFormalPlanAbsent(st, chapter); err != nil {
				return err
			}
			if err := pipelineSealedConvergenceValidateSeededPartial(st, *intent, eligibility, recovery); err != nil {
				return fmt.Errorf("sealed convergence unsealed Host seed side-effect audit failed: %w", err)
			}
		}
		if currentSHA == recovery.InitialPartialSHA256 {
			return fmt.Errorf("sealed convergence immutable seed completed without changing the partial")
		}
		recovery.SeedPartialSHA256 = currentSHA
		recovery.SeededAt = time.Now().UTC().Format(time.RFC3339Nano)
		if err := saveUpdatedPipelineSealedConvergenceReplanIntent(st.Dir(), intent, eligibility); err != nil {
			return fmt.Errorf("sealed convergence persist seeded partial binding: %w", err)
		}
	}
	if currentSHA != recovery.SeedPartialSHA256 {
		return fmt.Errorf("sealed convergence seeded compact partial SHA drift")
	}
	if err := pipelineSealedConvergenceRequireFormalPlanAbsent(st, chapter); err != nil {
		return err
	}
	if err := pipelineSealedConvergenceValidateSeededPartial(st, *intent, eligibility, recovery); err != nil {
		return err
	}

	prompt, err := pipelineSealedConvergenceBuildSeededCompactPrompt(
		currentRaw, *intent, eligibility, recovery,
	)
	if err != nil {
		return err
	}
	if err := pipelineSealedConvergenceRequireSessionAbsent(st.Dir(), recovery.SessionIdentity); err != nil {
		return err
	}
	latestProbe, err := pipelineSealedConvergenceProbeFailoverBinary()
	if err != nil {
		return fmt.Errorf("sealed convergence seeded compact executable changed before dispatch: %w", err)
	}
	if !pipelineSealedConvergenceSameBinaryProbe(failover, latestProbe) {
		return fmt.Errorf("sealed convergence seeded compact executable path/hash/version drift before dispatch")
	}
	recovery.PromptSHA256 = pipelineBytesSHA([]byte(prompt))
	recovery.PromptRunes = utf8.RuneCountInString(prompt)
	recovery.ModelDispatches = 1
	recovery.DispatchedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if err := saveUpdatedPipelineSealedConvergenceReplanIntent(st.Dir(), intent, eligibility); err != nil {
		return fmt.Errorf("sealed convergence persist seeded compact model dispatch 1/1: %w", err)
	}

	allowlisted := newPipelineSealedConvergenceMutablePlanDetailsTool(
		tools.NewPlanDetailsTool(st), chapter, recovery.AllowedMutableKeys,
	)
	if err := agents.RunSealedConvergencePlannerSeededCompactFinalize(
		context.Background(), cfg, promptBundle, st.Dir(), chapter, prompt,
		failover.BinaryPath, allowlisted,
	); err != nil {
		return fmt.Errorf("sealed convergence seeded compact finalize dispatch 1/1 failed: %w", err)
	}
	cp, err := tools.CurrentChapterPlanCausalCheckpoint(store.NewStore(st.Dir()), chapter)
	if err != nil || cp == nil || cp.Seq <= intent.SourceFrozen.PlanCheckpointSeq {
		return fmt.Errorf("sealed convergence seeded compact finalize returned without a successor plan: %w", err)
	}
	return nil
}

func pipelineSealedConvergenceRequireFormalPlanAbsent(st *store.Store, chapter int) error {
	if st == nil || chapter <= 0 {
		return fmt.Errorf("sealed convergence formal-plan absence check identity is incomplete")
	}
	path := filepath.Join(st.Dir(), "drafts", fmt.Sprintf("%02d.plan.json", chapter))
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("sealed convergence seeded recovery rejected: formal plan is present")
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("sealed convergence inspect seeded formal plan: %w", err)
	}
	return nil
}

func pipelineSealedConvergenceCaptureBinaryZeroSideEffect(
	st *store.Store,
	intent pipelineSealedConvergenceReplanIntent,
	partialSHA string,
) (*pipelineSealedConvergenceBinaryZeroSideEffect, error) {
	if err := pipelineSealedConvergenceValidateStaticZeroSideEffectState(st, intent); err != nil {
		return nil, err
	}
	if err := pipelineSealedConvergenceValidateReplacementZeroSideEffectEvidence(st, intent); err != nil {
		return nil, err
	}
	failover := intent.PlannerContinuation.Replacement.BinaryFailover
	if failover == nil || failover.Dispatches != 1 || partialSHA != intent.PlannerContinuation.InitialPartialSHA256 {
		return nil, fmt.Errorf("binary failover dispatch/partial binding drift")
	}
	audit, err := pipelineSealedConvergenceAuditSession(st.Dir(), failover.SessionIdentity)
	if err != nil {
		return nil, err
	}
	if audit.Rows != 1 || audit.UserRows != 1 || audit.AssistantRows != 0 ||
		audit.ToolRows != 0 || audit.ToolCallBlocks != 0 || audit.UserPromptSHA256 != failover.PromptSHA256 {
		return nil, fmt.Errorf("binary failover session is not exact zero-side-effect")
	}
	receipt, err := st.Runtime.LoadPlanningContextAccessReceipt(domain.PlanningContextAccessPlan)
	if err != nil || receipt == nil || !receipt.ConsumedAt.IsZero() ||
		receipt.ReceiptDigest != failover.AccessReceiptDigest ||
		receipt.Chapter != intent.SourceFrozen.Chapter ||
		receipt.GenerationID != intent.SourceFrozen.PlanningGenerationID {
		return nil, fmt.Errorf("binary failover access receipt identity/consumption drift: %w", err)
	}
	lock, err := pipelineSealedConvergenceCurrentReplacementLock(st, intent.SourceFrozen.Chapter)
	if err != nil {
		return nil, err
	}
	if receipt.LockOwner == lock.Owner || receipt.LockProcessID == lock.ProcessID {
		return nil, fmt.Errorf("seeded compact recovery requires the binary failover lock to be released and a fresh lock acquired")
	}
	return &pipelineSealedConvergenceBinaryZeroSideEffect{
		Version: pipelineSealedConvergenceBinaryZeroOutcomeVersion, Classification: pipelineSealedConvergenceOutcomeBinaryZero,
		BinaryDispatch: failover.Dispatches, SessionIdentity: failover.SessionIdentity,
		SessionSHA256: audit.SHA256, SessionRows: audit.Rows, UserRows: audit.UserRows,
		AssistantRows: audit.AssistantRows, ToolRows: audit.ToolRows, ToolCallBlocks: audit.ToolCallBlocks,
		UserPromptSHA256: audit.UserPromptSHA256, PartialSHA256: partialSHA, FormalPlanPresent: false,
		AccessReceiptDigest: receipt.ReceiptDigest, AccessReceiptConsumed: false,
		AccessReceiptLockOwner: receipt.LockOwner, AccessReceiptLockProcessID: receipt.LockProcessID,
		RecordedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}, nil
}

func pipelineSealedConvergenceValidateBinaryZeroSideEffectEvidence(
	st *store.Store,
	intent pipelineSealedConvergenceReplanIntent,
) error {
	if err := pipelineSealedConvergenceValidateOriginalZeroSideEffectEvidence(st, intent); err != nil {
		return err
	}
	if err := pipelineSealedConvergenceValidateReplacementZeroSideEffectEvidence(st, intent); err != nil {
		return err
	}
	failover := intent.PlannerContinuation.Replacement.BinaryFailover
	if failover == nil || failover.ZeroSideEffectOutcome == nil {
		return fmt.Errorf("sealed convergence binary zero-side-effect outcome is absent")
	}
	outcome := failover.ZeroSideEffectOutcome
	if outcome.Version != pipelineSealedConvergenceBinaryZeroOutcomeVersion ||
		outcome.Classification != pipelineSealedConvergenceOutcomeBinaryZero || outcome.BinaryDispatch != 1 ||
		outcome.SessionIdentity != failover.SessionIdentity || outcome.UserPromptSHA256 != failover.PromptSHA256 ||
		outcome.PartialSHA256 != intent.PlannerContinuation.InitialPartialSHA256 || outcome.FormalPlanPresent ||
		outcome.AccessReceiptConsumed || outcome.AccessReceiptDigest != failover.AccessReceiptDigest ||
		outcome.SessionRows != 1 || outcome.UserRows != 1 || outcome.AssistantRows != 0 ||
		outcome.ToolRows != 0 || outcome.ToolCallBlocks != 0 {
		return fmt.Errorf("sealed convergence binary zero-side-effect journal binding drift")
	}
	audit, err := pipelineSealedConvergenceAuditSession(st.Dir(), outcome.SessionIdentity)
	if err != nil {
		return err
	}
	if audit.SHA256 != outcome.SessionSHA256 || audit.Rows != outcome.SessionRows ||
		audit.UserRows != outcome.UserRows || audit.AssistantRows != outcome.AssistantRows ||
		audit.ToolRows != outcome.ToolRows || audit.ToolCallBlocks != outcome.ToolCallBlocks ||
		audit.UserPromptSHA256 != outcome.UserPromptSHA256 {
		return fmt.Errorf("sealed convergence binary failover session drift after zero-side-effect proof")
	}
	return nil
}

func pipelineSealedConvergenceApplyImmutableSeed(
	promptBundle assets.Bundle,
	style string,
	st *store.Store,
	chapter int,
	intent *pipelineSealedConvergenceReplanIntent,
	eligibility *pipelineSealedConvergenceReplanEligibility,
	recovery *pipelineSealedConvergenceSeededCompactFinalize,
) error {
	lock, err := pipelineSealedConvergenceCurrentReplacementLock(st, chapter)
	if err != nil {
		return err
	}
	baseline, err := st.Runtime.LoadPlanningContextAccessReceipt(domain.PlanningContextAccessPlan)
	if err != nil || baseline == nil || !baseline.ConsumedAt.IsZero() ||
		baseline.Chapter != intent.SourceFrozen.Chapter ||
		baseline.GenerationID != intent.SourceFrozen.PlanningGenerationID {
		return fmt.Errorf("sealed convergence seeded compact baseline receipt unavailable: %w", err)
	}
	recovery.PrefetchLockOwner = lock.Owner
	recovery.PrefetchLockProcessID = lock.ProcessID
	recovery.PrefetchStartedAt = time.Now().UTC().Format(time.RFC3339Nano)
	recovery.PrefetchBaselineReceiptDigest = baseline.ReceiptDigest
	if err := saveUpdatedPipelineSealedConvergenceReplanIntent(st.Dir(), intent, eligibility); err != nil {
		return fmt.Errorf("sealed convergence persist seeded compact prefetch write-ahead: %w", err)
	}

	contextRaw, err := pipelineSealedConvergencePrefetchPlanningContext(
		st, promptBundle, style, chapter, intent.ChapterWordBounds,
	)
	if err != nil {
		return err
	}
	accessToken, err := pipelineSealedConvergencePlanningAccessToken(contextRaw)
	if err != nil {
		return err
	}
	receipt, err := pipelineSealedConvergenceCurrentUnconsumedPlanReceipt(st, *intent, lock)
	if err != nil {
		return err
	}
	sealedRAGReceipt, sealedRAGDigest, err := pipelineSealedConvergenceRebindSealedRAGReceipt(st, eligibility)
	if err != nil {
		return err
	}
	seed, err := pipelineSealedConvergenceImmutableSeed(st, eligibility, accessToken)
	if err != nil {
		return err
	}
	if err := pipelineSealedConvergenceValidateImmutableSeedRoot(seed); err != nil {
		return err
	}
	seedArgs, err := json.Marshal(map[string]any{
		"chapter": chapter, "causal_simulation": seed, "finalize": false,
	})
	if err != nil {
		return err
	}
	recovery.ImmutableSeedDigest = pipelineSealedConvergenceImmutableSeedDigest(
		seed, sealedRAGReceipt.ID, sealedRAGDigest,
	)
	recovery.AccessReceiptDigest = receipt.ReceiptDigest
	recovery.SeedArgsSHA256 = pipelineBytesSHA(seedArgs)
	recovery.SeedToolDispatches = 1
	recovery.SeedInvokedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if err := saveUpdatedPipelineSealedConvergenceReplanIntent(st.Dir(), intent, eligibility); err != nil {
		return fmt.Errorf("sealed convergence persist immutable seed dispatch 1/1: %w", err)
	}
	result, err := tools.NewPlanDetailsTool(st).Execute(context.Background(), seedArgs)
	if err != nil {
		return fmt.Errorf("sealed convergence immutable seed plan_details failed: %w", err)
	}
	if strings.Contains(string(result), `"planned":true`) {
		return fmt.Errorf("sealed convergence immutable seed unexpectedly finalized a plan")
	}
	return nil
}

func pipelineSealedConvergencePlanningAccessToken(raw json.RawMessage) (string, error) {
	var packet struct {
		Receipt struct {
			SourceToken string `json:"source_token"`
		} `json:"planning_context_access_receipt"`
	}
	if err := json.Unmarshal(raw, &packet); err != nil {
		return "", err
	}
	if _, err := domain.PlanningContextAccessTokenSHA256(packet.Receipt.SourceToken); err != nil {
		return "", fmt.Errorf("sealed convergence seeded compact planning token is invalid: %w", err)
	}
	return packet.Receipt.SourceToken, nil
}

func pipelineSealedConvergenceImmutableSeed(
	st *store.Store,
	eligibility *pipelineSealedConvergenceReplanEligibility,
	accessToken string,
) (map[string]any, error) {
	if st == nil || eligibility == nil {
		return nil, fmt.Errorf("sealed convergence immutable seed source is incomplete")
	}
	sealed := eligibility.Plan.CausalSimulation
	sealedReceipt, sealedDigest, err := pipelineSealedConvergenceSealedRAGReceipt(eligibility)
	if err != nil {
		return nil, err
	}
	receipt, err := st.RAG.LoadLatestRAGFactReceipt(eligibility.Plan.Chapter)
	if err != nil {
		return nil, err
	}
	if receipt == nil {
		return nil, fmt.Errorf("sealed convergence exact sealed RAG receipt is not latest after formal rebind")
	}
	latestDigest, err := domain.RAGFactReceiptDigestV2(*receipt)
	if err != nil || receipt.ID != sealedReceipt.ID || latestDigest != sealedDigest {
		return nil, fmt.Errorf("sealed convergence latest RAG receipt does not match the exact sealed bundle: %w", err)
	}
	validRefs := make(map[string]struct{}, len(receipt.Hits))
	for _, hit := range receipt.Hits {
		validRefs[hit.Ref] = struct{}{}
	}
	externalRefs := make([]domain.ExternalReferencePlan, 0, len(sealed.ExternalRefs))
	for _, row := range sealed.ExternalRefs {
		if !strings.Contains(strings.ToLower(strings.TrimSpace(row.SourceType)), "rag") || len(row.SourceRefs) == 0 {
			continue
		}
		valid := true
		for _, ref := range row.SourceRefs {
			if _, ok := validRefs[strings.TrimSpace(ref)]; !ok {
				valid = false
				break
			}
		}
		if valid {
			externalRefs = append(externalRefs, row)
		}
	}
	if !receipt.NoMaterial && len(externalRefs) == 0 {
		return nil, fmt.Errorf("sealed convergence exact sealed RAG material has no authorized external_reference_plan row")
	}
	seed := pipelineSealedConvergenceImmutableSeedMap(sealed, accessToken, externalRefs)
	if err := pipelineSealedConvergenceValidateImmutableSeedRoot(seed); err != nil {
		return nil, err
	}
	return seed, nil
}

func pipelineSealedConvergenceImmutableSeedMap(
	sealed domain.ChapterCausalSimulation,
	accessToken string,
	externalRefs []domain.ExternalReferencePlan,
) map[string]any {
	return map[string]any{
		"project_promise":             sealed.ProjectPromise,
		"chapter_function":            sealed.ChapterFunction,
		"context_sources":             []string{accessToken},
		"initial_state":               append([]domain.CharacterSimulationState{}, sealed.InitialState...),
		"reader_retention_plan":       map[string]any{"reveal_budget": append([]string{}, sealed.ReaderRetentionPlan.RevealBudget...)},
		"arc_transition_contract":     sealed.ArcTransition,
		"ending_consequence_contract": sealed.EndingContract,
		"outcome_shift":               append([]string{}, sealed.OutcomeShift...),
		"external_reference_plan":     append([]domain.ExternalReferencePlan{}, externalRefs...),
	}
}

func pipelineSealedConvergenceValidateImmutableSeedRoot(seed map[string]any) error {
	if len(seed) != len(pipelineSealedConvergenceImmutableSeedKeys) {
		return fmt.Errorf("sealed convergence immutable seed root widened: got %d keys want %d", len(seed), len(pipelineSealedConvergenceImmutableSeedKeys))
	}
	for _, key := range pipelineSealedConvergenceImmutableSeedKeys {
		if _, ok := seed[key]; !ok {
			return fmt.Errorf("sealed convergence immutable seed root is missing authorized key %s", key)
		}
	}
	for key := range seed {
		if pipelineSealedConvergenceStringContains(pipelineSealedConvergenceSeededMutableKeys, key) {
			return fmt.Errorf("sealed convergence Host seed crossed mutable key %s", key)
		}
	}
	return nil
}

func pipelineSealedConvergenceStringContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func pipelineSealedConvergenceImmutableSeedDigest(seed map[string]any, ragReceiptID, ragReceiptDigest string) string {
	withoutAccess := make(map[string]any, len(seed)-1)
	for key, value := range seed {
		if key != "context_sources" {
			withoutAccess[key] = value
		}
	}
	return pipelineProjectAllDigest(struct {
		Seed             map[string]any `json:"seed"`
		RAGReceiptID     string         `json:"rag_receipt_id"`
		RAGReceiptDigest string         `json:"rag_receipt_digest"`
	}{withoutAccess, ragReceiptID, ragReceiptDigest})
}

func pipelineSealedConvergenceSealedRAGReceipt(
	eligibility *pipelineSealedConvergenceReplanEligibility,
) (*domain.RAGFactReceipt, string, error) {
	if eligibility == nil {
		return nil, "", fmt.Errorf("sealed convergence RAG bundle identity is incomplete")
	}
	bundle := eligibility.Binding.Bundle
	if err := domain.ValidateProjectedChapterBundle(bundle); err != nil {
		return nil, "", fmt.Errorf("sealed convergence exact projected bundle is invalid: %w", err)
	}
	if eligibility.Intent.SourceFrozen.ProjectedBundleDigest != "" &&
		bundle.BundleDigest != eligibility.Intent.SourceFrozen.ProjectedBundleDigest {
		return nil, "", fmt.Errorf("sealed convergence exact projected bundle digest drift")
	}
	if bundle.RAGFactReceipt == nil || bundle.Chapter != eligibility.Plan.Chapter {
		return nil, "", fmt.Errorf("sealed convergence exact projected bundle has no chapter RAG receipt")
	}
	bundlePlanDigest, bundlePlanErr := domain.ComputeChapterPlanV2Digest(bundle.ChapterPlan)
	eligibilityPlanDigest, eligibilityPlanErr := domain.ComputeChapterPlanV2Digest(eligibility.Plan)
	if bundlePlanErr != nil || eligibilityPlanErr != nil || bundlePlanDigest != eligibilityPlanDigest {
		return nil, "", fmt.Errorf("sealed convergence eligibility plan does not match its exact projected bundle")
	}
	digest, err := domain.RAGFactReceiptDigestV2(*bundle.RAGFactReceipt)
	if err != nil || digest != bundle.RAGFactReceiptDigest {
		return nil, "", fmt.Errorf("sealed convergence exact projected bundle RAG digest drift: %w", err)
	}
	return bundle.RAGFactReceipt, digest, nil
}

func pipelineSealedConvergenceRebindSealedRAGReceipt(
	st *store.Store,
	eligibility *pipelineSealedConvergenceReplanEligibility,
) (*domain.RAGFactReceipt, string, error) {
	if st == nil {
		return nil, "", fmt.Errorf("sealed convergence RAG rebind store is unavailable")
	}
	receipt, digest, err := pipelineSealedConvergenceSealedRAGReceipt(eligibility)
	if err != nil {
		return nil, "", err
	}
	// Validate the exact sealed plan/receipt transformations against the
	// current index before advancing latest. This admits additive retrieval
	// drift but rejects deleted, rerouted, or changed chunks.
	if err := tools.ValidateRAGFactPlanCurrent(st, eligibility.Plan); err != nil {
		return nil, "", fmt.Errorf("sealed convergence exact sealed RAG receipt is not current-index valid: %w", err)
	}
	if err := st.RAG.SaveRAGFactReceipt(*receipt); err != nil {
		return nil, "", fmt.Errorf("sealed convergence atomically rebind exact sealed RAG receipt: %w", err)
	}
	latest, err := st.RAG.LoadLatestRAGFactReceipt(receipt.Chapter)
	if err != nil || latest == nil {
		return nil, "", fmt.Errorf("sealed convergence verify exact sealed RAG latest rebind: %w", err)
	}
	latestDigest, err := domain.RAGFactReceiptDigestV2(*latest)
	if err != nil || latest.ID != receipt.ID || latestDigest != digest {
		return nil, "", fmt.Errorf("sealed convergence exact sealed RAG latest rebind drift: %w", err)
	}
	return receipt, digest, nil
}

func pipelineSealedConvergenceValidateSeededPartial(
	st *store.Store,
	intent pipelineSealedConvergenceReplanIntent,
	eligibility *pipelineSealedConvergenceReplanEligibility,
	recovery *pipelineSealedConvergenceSeededCompactFinalize,
) error {
	if err := pipelineSealedConvergenceRequireFormalPlanAbsent(st, intent.SourceFrozen.Chapter); err != nil {
		return err
	}
	partial, err := st.Drafts.LoadChapterPlanPartial(intent.SourceFrozen.Chapter)
	if err != nil || partial == nil {
		return fmt.Errorf("sealed convergence seeded partial unavailable: %w", err)
	}
	merged, _ := partial["causal_simulation"].(map[string]any)
	if merged == nil {
		return fmt.Errorf("sealed convergence seeded partial has no causal_simulation")
	}
	expectedRoots := append([]string(nil), pipelineSealedConvergenceImmutableSeedKeys...)
	expectedRoots = append(expectedRoots, "world_simulation_id", "protagonist_decision")
	if len(merged) != len(expectedRoots) {
		return fmt.Errorf("sealed convergence seeded partial root widened: got %d keys want %d", len(merged), len(expectedRoots))
	}
	for _, key := range expectedRoots {
		if _, present := merged[key]; !present {
			return fmt.Errorf("sealed convergence seeded partial is missing authorized root %s", key)
		}
	}
	retention, ok := merged["reader_retention_plan"].(map[string]any)
	if !ok || len(retention) != 1 || retention["reveal_budget"] == nil {
		return fmt.Errorf("sealed convergence seeded partial retention root must contain reveal_budget only")
	}
	for _, key := range recovery.AllowedMutableKeys {
		if _, present := merged[key]; present {
			return fmt.Errorf("sealed convergence Host seed crossed mutable key %s", key)
		}
	}
	raw, _ := json.Marshal(merged)
	var got domain.ChapterCausalSimulation
	if err := json.Unmarshal(raw, &got); err != nil {
		return err
	}
	receipt, err := st.Runtime.LoadPlanningContextAccessReceipt(domain.PlanningContextAccessPlan)
	if err != nil || receipt == nil || receipt.ReceiptDigest != recovery.AccessReceiptDigest || !receipt.ConsumedAt.IsZero() ||
		receipt.Chapter != intent.SourceFrozen.Chapter || receipt.GenerationID != intent.SourceFrozen.PlanningGenerationID ||
		receipt.LockOwner != recovery.PrefetchLockOwner || receipt.LockProcessID != recovery.PrefetchLockProcessID {
		return fmt.Errorf("sealed convergence seeded receipt unavailable or consumed: %w", err)
	}
	accessCount := 0
	accessToken := ""
	sealedRAGReceipt, sealedRAGDigest, err := pipelineSealedConvergenceSealedRAGReceipt(eligibility)
	if err != nil {
		return err
	}
	ragCount := 0
	for _, source := range got.ContextSources {
		source = strings.TrimSpace(source)
		if strings.HasPrefix(source, domain.RAGFactReceiptTokenPrefix) {
			ragCount++
			if source != sealedRAGReceipt.SourceToken() {
				return fmt.Errorf("sealed convergence seeded RAG source token drift")
			}
		}
		if strings.HasPrefix(source, domain.PlanningContextAccessTokenPrefix) {
			accessCount++
			accessToken = source
			sha, hashErr := domain.PlanningContextAccessTokenSHA256(source)
			if hashErr != nil || sha != receipt.TokenSHA256 {
				return fmt.Errorf("sealed convergence seeded access token drift")
			}
		}
	}
	if accessCount != 1 {
		return fmt.Errorf("sealed convergence seeded partial must contain exactly one fresh access token")
	}
	if ragCount != 1 {
		return fmt.Errorf("sealed convergence seeded partial must contain exactly one exact sealed RAG token")
	}
	expectedSeed, err := pipelineSealedConvergenceImmutableSeed(st, eligibility, accessToken)
	if err != nil {
		return err
	}
	expectedRaw, err := json.Marshal(expectedSeed)
	if err != nil {
		return err
	}
	var want domain.ChapterCausalSimulation
	if err := json.Unmarshal(expectedRaw, &want); err != nil {
		return err
	}
	sealed := eligibility.Plan.CausalSimulation
	if got.WorldSimulationID != eligibility.Simulation.SimulationID ||
		got.ProtagonistDecision != eligibility.Simulation.ProtagonistProjection.ChosenDecision ||
		got.ProjectPromise != sealed.ProjectPromise || got.ChapterFunction != sealed.ChapterFunction ||
		!reflect.DeepEqual(got.InitialState, want.InitialState) ||
		!reflect.DeepEqual(got.OutcomeShift, want.OutcomeShift) ||
		!reflect.DeepEqual(got.ArcTransition, want.ArcTransition) ||
		!reflect.DeepEqual(got.EndingContract, want.EndingContract) ||
		!reflect.DeepEqual(got.ReaderRetentionPlan.RevealBudget, want.ReaderRetentionPlan.RevealBudget) ||
		!reflect.DeepEqual(got.ExternalRefs, want.ExternalRefs) {
		return fmt.Errorf("sealed convergence immutable/grounding seed drift")
	}
	if got := pipelineSealedConvergenceImmutableSeedDigest(
		expectedSeed, sealedRAGReceipt.ID, sealedRAGDigest,
	); got != recovery.ImmutableSeedDigest {
		return fmt.Errorf("sealed convergence immutable seed digest drift")
	}
	seedArgs, err := json.Marshal(map[string]any{
		"chapter": intent.SourceFrozen.Chapter, "causal_simulation": expectedSeed, "finalize": false,
	})
	if err != nil {
		return err
	}
	if got := pipelineBytesSHA(seedArgs); got != recovery.SeedArgsSHA256 {
		return fmt.Errorf("sealed convergence immutable seed args digest drift")
	}
	return nil
}

func pipelineSealedConvergenceBuildSeededCompactPrompt(
	partialRaw []byte,
	intent pipelineSealedConvergenceReplanIntent,
	eligibility *pipelineSealedConvergenceReplanEligibility,
	recovery *pipelineSealedConvergenceSeededCompactFinalize,
) (string, error) {
	var partial map[string]any
	if err := json.Unmarshal(partialRaw, &partial); err != nil {
		return "", err
	}
	structure, _ := partial["structure"].(map[string]any)
	compactStructure := make(map[string]any, len(structure))
	for key, value := range structure {
		if key != "notes" && !strings.HasPrefix(key, "_") {
			compactStructure[key] = value
		}
	}
	protagonist := strings.TrimSpace(eligibility.Simulation.ProtagonistProjection.Protagonist)
	if protagonist == "" {
		protagonist = "主角"
	}
	payload := map[string]any{
		"chapter":                          intent.SourceFrozen.Chapter,
		"seeded_partial_sha256":            recovery.SeedPartialSHA256,
		"allowed_mutable_keys":             recovery.AllowedMutableKeys,
		"structure":                        compactStructure,
		"sanitized_diagnostics":            intent.Diagnostics,
		"chapter_word_bounds":              intent.ChapterWordBounds,
		"protagonist_projection":           eligibility.Simulation.ProtagonistProjection,
		"required_exact_consumed_by_cause": eligibility.Plan.CausalSimulation.ArcTransition.ConsumedByCause,
		"hard_rules": []string{
			"只调用 plan_details；chapter 必须精确，finalize=true；causal_simulation 只能包含 allowed_mutable_keys。",
			"initial_state、context_sources、world_simulation_id、protagonist_decision、project_promise、chapter_function、outcome_shift、arc/ending/reveal及RAG grounding均已seed，禁止重发。",
			"causal_beats第一项 cause 必须逐字等于 required_exact_consumed_by_cause；最多4项，并按顺序覆盖 protagonist_projection.causal_chain 与 sanitized_diagnostics 要求的可见因果，不得补入项目外固定桥段。",
			"render_capacity总量落在 chapter_word_bounds.min-max；3-6场，每场300-1400且至少3个具体动作；structure 或 diagnostics 中声明的顺序、位置窗口和后续独立后果必须映射到 scene_spine。",
			fmt.Sprintf("voice_logic和emotional_logic必须覆盖当前主角%s并填满工具schema必填子字段；anti_ai_execution_plan六组字段齐全；literary_rendering_plan须有scene_modes与带source_refs的active_lenses。", protagonist),
		},
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	prompt := fmt.Sprintf("完成已由Host精确seed的sealed第%d章计划。不要解释，不要输出计划文字；只提交一次允许的mutable plan_details patch并finalize。\n<seeded_compact_packet>\n%s\n</seeded_compact_packet>", intent.SourceFrozen.Chapter, encoded)
	if runes := utf8.RuneCountInString(prompt); runes > pipelineSealedConvergenceSeededPromptMaxRunes {
		return "", fmt.Errorf("sealed convergence seeded compact prompt exceeds hard %d-rune limit: %d", pipelineSealedConvergenceSeededPromptMaxRunes, runes)
	}
	return prompt, nil
}

type pipelineSealedConvergenceMutablePlanDetailsTool struct {
	inner   agentcore.Tool
	chapter int
	allowed map[string]struct{}
	mu      sync.Mutex
	calls   int
}

func newPipelineSealedConvergenceMutablePlanDetailsTool(inner agentcore.Tool, chapter int, keys []string) agentcore.Tool {
	allowed := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		allowed[key] = struct{}{}
	}
	return &pipelineSealedConvergenceMutablePlanDetailsTool{inner: inner, chapter: chapter, allowed: allowed}
}

func (t *pipelineSealedConvergenceMutablePlanDetailsTool) Name() string { return "plan_details" }
func (t *pipelineSealedConvergenceMutablePlanDetailsTool) Description() string {
	return "只补Host允许的mutable causal_simulation字段并以finalize=true收口；禁止覆盖任何seed字段。"
}
func (t *pipelineSealedConvergenceMutablePlanDetailsTool) Schema() map[string]any {
	if t == nil || t.inner == nil {
		return map[string]any{"type": "object"}
	}
	cloneRaw, _ := json.Marshal(t.inner.Schema())
	var clone map[string]any
	_ = json.Unmarshal(cloneRaw, &clone)
	properties, _ := clone["properties"].(map[string]any)
	causal, _ := properties["causal_simulation"].(map[string]any)
	causalProperties, _ := causal["properties"].(map[string]any)
	for key := range causalProperties {
		if _, ok := t.allowed[key]; !ok {
			delete(causalProperties, key)
		}
	}
	if required, ok := causal["required"].([]any); ok {
		kept := required[:0]
		for _, value := range required {
			if key, ok := value.(string); ok {
				if _, allowed := t.allowed[key]; allowed {
					kept = append(kept, key)
				}
			}
		}
		causal["required"] = kept
	}
	return clone
}
func (t *pipelineSealedConvergenceMutablePlanDetailsTool) Execute(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
	if t == nil || t.inner == nil {
		return nil, fmt.Errorf("seeded compact plan_details wrapper is unavailable")
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(args, &envelope); err != nil {
		return nil, fmt.Errorf("seeded compact plan_details args invalid: %w", err)
	}
	for key := range envelope {
		switch key {
		case "chapter", "causal_simulation", "finalize":
		default:
			return nil, fmt.Errorf("seeded compact plan_details forbids top-level key %s", key)
		}
	}
	var request struct {
		Chapter          int            `json:"chapter"`
		CausalSimulation map[string]any `json:"causal_simulation"`
		Finalize         bool           `json:"finalize"`
	}
	if err := json.Unmarshal(args, &request); err != nil {
		return nil, fmt.Errorf("seeded compact plan_details args invalid: %w", err)
	}
	if request.Chapter != t.chapter || !request.Finalize || len(request.CausalSimulation) == 0 {
		return nil, fmt.Errorf("seeded compact plan_details requires exact chapter, nonempty mutable patch and finalize=true")
	}
	for key := range request.CausalSimulation {
		if _, ok := t.allowed[key]; !ok {
			return nil, fmt.Errorf("seeded compact plan_details forbids seeded/non-allowlisted key %s", key)
		}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.calls >= 2 {
		return nil, fmt.Errorf("seeded compact plan_details exceeded its two-turn repair ceiling")
	}
	t.calls++
	return t.inner.Execute(ctx, args)
}

func sortedPipelineSealedConvergenceMutableKeys() []string {
	keys := append([]string(nil), pipelineSealedConvergenceSeededMutableKeys...)
	sort.Strings(keys)
	return keys
}
