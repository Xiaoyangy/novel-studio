package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

const sealedConvergenceReplanReceiptDir = "meta/planning/convergence_replans"

func sealedConvergenceReplanMarkerCurrent(
	st *store.Store,
	marker sealedV2FrozenPlanMarker,
	checkpoint *domain.Checkpoint,
	sealedPlanSemanticDigest string,
	currentPlanSemanticDigest string,
) bool {
	if st == nil || checkpoint == nil ||
		strings.TrimSpace(marker.ConvergenceReplanReceiptDigest) == "" {
		return false
	}
	digest := strings.TrimSpace(marker.ConvergenceReplanReceiptDigest)
	if filepath.Base(digest) != digest || strings.ContainsAny(digest, `/\\`) {
		return false
	}
	raw, err := os.ReadFile(filepath.Join(
		st.Dir(),
		filepath.FromSlash(sealedConvergenceReplanReceiptDir),
		digest+".json",
	))
	if err != nil {
		return false
	}
	var receipt domain.SealedConvergenceReplanReceipt
	if json.Unmarshal(raw, &receipt) != nil ||
		domain.ValidateSealedConvergenceReplanReceipt(receipt) != nil {
		return false
	}
	return receipt.ReceiptDigest == digest &&
		receipt.GenerationID == marker.PlanningGenerationID &&
		receipt.Chapter == marker.Chapter &&
		receipt.BundleDigest == marker.ProjectedBundleDigest &&
		receipt.PromotionReceiptDigest == marker.PromotionReceiptDigest &&
		receipt.SealedPlanSemanticDigest == sealedPlanSemanticDigest &&
		receipt.ReplacementPlanSemanticDigest == currentPlanSemanticDigest &&
		receipt.ReplacementPlanCheckpointDigest == marker.PlanDigest &&
		receipt.ReplacementPlanCheckpointDigest == checkpoint.Digest &&
		receipt.ReplacementPlanCheckpointSeq == marker.PlanCheckpointSeq &&
		receipt.ReplacementPlanCheckpointSeq == checkpoint.Seq &&
		receipt.ReplacementRenderContextSHA256 == marker.RenderContextSHA256
}

// sealedConvergenceAncestorFailedBodySHA256 projects immutable negative
// exact-body evidence across successor plan epochs. These hashes are guard
// tombstones only: the new plan's durable convergence ledger remains empty and
// therefore receives its own bounded budget.
func sealedConvergenceAncestorFailedBodySHA256(
	st *store.Store,
	chapter int,
	planDigest string,
) ([]string, error) {
	if st == nil || chapter <= 0 || strings.TrimSpace(planDigest) == "" {
		return nil, nil
	}
	raw, err := os.ReadFile(filepath.Join(st.Dir(), filepath.FromSlash(currentFrozenPlanMarkerPath)))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var marker sealedV2FrozenPlanMarker
	if err := json.Unmarshal(raw, &marker); err != nil {
		return nil, err
	}
	if marker.Version != "pipeline-planning.v1" ||
		marker.ProjectionBinding != sealedV2ProjectionBinding ||
		marker.Chapter != chapter || marker.PlanDigest != planDigest ||
		strings.TrimSpace(marker.ConvergenceReplanReceiptDigest) == "" {
		return nil, nil
	}
	plan, err := st.Drafts.LoadChapterPlan(chapter)
	if err != nil || plan == nil {
		return nil, fmt.Errorf("load convergence successor plan: %w", err)
	}
	bundle, sealed := exactSealedV2FrozenBundleForPlan(st, chapter, *plan)
	if !sealed || bundle == nil {
		return nil, fmt.Errorf("convergence successor marker is not current")
	}
	currentPlanSemanticDigest, err := domain.ComputeChapterPlanV2Digest(*plan)
	if err != nil {
		return nil, err
	}
	sealedPlanSemanticDigest, err := domain.ComputeChapterPlanV2Digest(bundle.ChapterPlan)
	if err != nil {
		return nil, err
	}

	digest := strings.TrimSpace(marker.ConvergenceReplanReceiptDigest)
	expectedReplacementPlanDigest := currentPlanSemanticDigest
	expectedReplacementCheckpointDigest := marker.PlanDigest
	expectedReplacementCheckpointSeq := marker.PlanCheckpointSeq
	seenReceipts := map[string]struct{}{}
	var hashes []string
	for depth := 0; digest != ""; depth++ {
		if depth >= 32 {
			return nil, fmt.Errorf("convergence successor receipt chain exceeds bound")
		}
		if _, duplicate := seenReceipts[digest]; duplicate || filepath.Base(digest) != digest || strings.ContainsAny(digest, `/\\`) {
			return nil, fmt.Errorf("convergence successor receipt chain is cyclic or malformed")
		}
		seenReceipts[digest] = struct{}{}
		receiptRaw, err := os.ReadFile(filepath.Join(st.Dir(), filepath.FromSlash(sealedConvergenceReplanReceiptDir), digest+".json"))
		if err != nil {
			return nil, err
		}
		var receipt domain.SealedConvergenceReplanReceipt
		if json.Unmarshal(receiptRaw, &receipt) != nil || domain.ValidateSealedConvergenceReplanReceipt(receipt) != nil {
			return nil, fmt.Errorf("convergence successor receipt %s is invalid", digest)
		}
		if receipt.ReceiptDigest != digest ||
			receipt.GenerationID != marker.PlanningGenerationID ||
			receipt.Chapter != chapter ||
			receipt.BundleDigest != marker.ProjectedBundleDigest ||
			receipt.PromotionReceiptDigest != marker.PromotionReceiptDigest ||
			receipt.SealedPlanSemanticDigest != sealedPlanSemanticDigest ||
			(expectedReplacementPlanDigest != "" && receipt.ReplacementPlanSemanticDigest != expectedReplacementPlanDigest) ||
			receipt.ReplacementPlanCheckpointDigest != expectedReplacementCheckpointDigest ||
			receipt.ReplacementPlanCheckpointSeq != expectedReplacementCheckpointSeq {
			return nil, fmt.Errorf("convergence successor receipt %s breaks the plan chain", digest)
		}
		hashes = append(hashes, receipt.FailedBodySHA256...)
		expectedReplacementPlanDigest = ""
		expectedReplacementCheckpointDigest = receipt.PreviousPlanCheckpointDigest
		expectedReplacementCheckpointSeq = receipt.PreviousPlanCheckpointSeq
		digest = strings.TrimSpace(receipt.PreviousReplanReceiptDigest)
		// The previous receipt's replacement semantic digest is not repeated in
		// the child receipt. Its checkpoint identity is repeated and each receipt
		// is content-addressed, so older semantic values remain audit-only here.
	}
	return hashes, nil
}
