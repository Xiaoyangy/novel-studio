package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/tools"
)

const (
	pipelineRenderInputUpgradeVersion = "pipeline-render-input-upgrade.v1"
	pipelineRenderInputUpgradeDir     = "meta/planning/render_input_upgrades"
)

// pipelineRenderInputUpgradeReceipt makes a deliberate prose-input protocol
// upgrade auditable without pretending that the immutable plan, promotion or
// frozen render context was regenerated. It is only available before the old
// transaction has produced any candidate-side evidence.
type pipelineRenderInputUpgradeReceipt struct {
	Version                string `json:"version"`
	UpgradeID              string `json:"upgrade_id"`
	Chapter                int    `json:"chapter"`
	PlanDigest             string `json:"plan_digest"`
	PlanCheckpointSeq      int64  `json:"plan_checkpoint_seq"`
	ProjectionBinding      string `json:"projection_binding"`
	RenderContextSHA256    string `json:"render_context_sha256"`
	ProjectedBundleDigest  string `json:"projected_bundle_digest"`
	PromotionReceiptDigest string `json:"promotion_receipt_digest"`
	PreviousInputDigest    string `json:"previous_input_digest"`
	CurrentInputDigest     string `json:"current_input_digest"`
	PreviousCandidateID    string `json:"previous_candidate_id"`
	CurrentCandidateID     string `json:"current_candidate_id"`
	Authorization          string `json:"authorization"`
	UpgradedAt             string `json:"upgraded_at"`
	ReceiptDigest          string `json:"receipt_digest"`
}

type pipelineRenderInputUpgradeIdentity struct {
	Version                string `json:"version"`
	Chapter                int    `json:"chapter"`
	PlanDigest             string `json:"plan_digest"`
	PlanCheckpointSeq      int64  `json:"plan_checkpoint_seq"`
	ProjectionBinding      string `json:"projection_binding"`
	RenderContextSHA256    string `json:"render_context_sha256"`
	ProjectedBundleDigest  string `json:"projected_bundle_digest"`
	PromotionReceiptDigest string `json:"promotion_receipt_digest"`
	PreviousInputDigest    string `json:"previous_input_digest"`
	CurrentInputDigest     string `json:"current_input_digest"`
}

func pipelineUpgradeFrozenRenderInput(
	st *store.Store,
	frozen *pipelineFrozenPlan,
	currentInputDigest string,
) (*pipelineFrozenPlan, error) {
	if st == nil || frozen == nil {
		return nil, fmt.Errorf("render input upgrade requires store and frozen plan")
	}
	if frozen.ProjectionBinding != "sealed_v2" {
		return nil, fmt.Errorf("render input upgrade only accepts sealed_v2 plans")
	}
	if !pipelineRenderInputSHA256(frozen.PipelineRunInputDigest) ||
		!pipelineRenderInputSHA256(currentInputDigest) ||
		frozen.PipelineRunInputDigest == currentInputDigest {
		return nil, fmt.Errorf("render input upgrade requires two distinct exact SHA-256 bindings")
	}
	if _, committed := pipelineCommittedAfterFrozenBaseline(st, frozen); committed {
		return nil, fmt.Errorf("render input upgrade is forbidden after chapter commit")
	}
	if _, err := validatePipelineSealedRenderBinding(st, frozen, false); err != nil {
		return nil, fmt.Errorf("render input upgrade sealed binding invalid: %w", err)
	}
	if err := validatePipelineFrozenRenderDependencies(st.Dir(), frozen); err != nil {
		return nil, err
	}
	if err := tools.ValidateCurrentChapterRenderPlanForExecution(st, frozen.Chapter); err != nil {
		return nil, fmt.Errorf("render input upgrade formal plan is not render-fresh: %w", err)
	}

	previousCandidateID, err := pipelineRenderTransactionID(frozen)
	if err != nil {
		return nil, err
	}
	if err := pipelineRequireNoRenderInputUpgradeAttempt(st.Dir(), previousCandidateID, frozen.Chapter); err != nil {
		return nil, err
	}

	upgraded := *frozen
	upgraded.PipelineRunInputDigest = currentInputDigest
	upgraded.RenderInputUpgradeID = ""
	upgraded.RenderInputUpgradeReceiptDigest = ""
	currentCandidateID, err := pipelineRenderTransactionID(&upgraded)
	if err != nil {
		return nil, err
	}
	if err := pipelineRequireNoRenderTransactionEvidence(st.Dir(), currentCandidateID); err != nil {
		return nil, fmt.Errorf("render input upgrade target namespace is not fresh: %w", err)
	}
	identity := pipelineRenderInputUpgradeIdentity{
		Version:                pipelineRenderInputUpgradeVersion,
		Chapter:                frozen.Chapter,
		PlanDigest:             frozen.PlanDigest,
		PlanCheckpointSeq:      frozen.PlanCheckpointSeq,
		ProjectionBinding:      frozen.ProjectionBinding,
		RenderContextSHA256:    frozen.RenderContextSHA256,
		ProjectedBundleDigest:  frozen.ProjectedBundleDigest,
		PromotionReceiptDigest: frozen.PromotionReceiptDigest,
		PreviousInputDigest:    frozen.PipelineRunInputDigest,
		CurrentInputDigest:     currentInputDigest,
	}
	upgradeID := pipelineRenderInputUpgradeID(identity)
	receiptPath := pipelineRenderInputUpgradeReceiptPath(st.Dir(), upgradeID)
	receipt, err := loadOrCreatePipelineRenderInputUpgradeReceipt(
		receiptPath, identity, upgradeID, previousCandidateID, currentCandidateID,
	)
	if err != nil {
		return nil, err
	}
	upgraded.RenderInputUpgradeID = receipt.UpgradeID
	upgraded.RenderInputUpgradeReceiptDigest = receipt.ReceiptDigest
	if _, err := writePipelinePlanningJSON(
		filepath.Join(st.Dir(), pipelineFrozenPlanPath),
		upgraded,
	); err != nil {
		return nil, fmt.Errorf("persist upgraded frozen render input: %w", err)
	}
	if err := validatePipelineRenderInputUpgradeReceipt(st.Dir(), &upgraded); err != nil {
		return nil, fmt.Errorf("verify upgraded frozen render input: %w", err)
	}
	fmt.Fprintf(
		os.Stderr,
		"[pipeline:render] 第 %d 章已显式升级 sealed render input：%s -> %s；plan/context/promotion 未变，回执=%s\n",
		frozen.Chapter,
		previousCandidateID,
		currentCandidateID,
		receipt.ReceiptDigest,
	)
	return &upgraded, nil
}

func pipelineRenderInputUpgradeID(identity pipelineRenderInputUpgradeIdentity) string {
	digest := strings.TrimPrefix(pipelineProjectAllDigest(identity), "sha256:")
	if len(digest) > 24 {
		digest = digest[:24]
	}
	return "render-input-upgrade-" + digest
}

func pipelineRenderInputUpgradeReceiptPath(outputDir, upgradeID string) string {
	return filepath.Join(outputDir, pipelineRenderInputUpgradeDir, upgradeID+".json")
}

func loadOrCreatePipelineRenderInputUpgradeReceipt(
	path string,
	identity pipelineRenderInputUpgradeIdentity,
	upgradeID string,
	previousCandidateID string,
	currentCandidateID string,
) (pipelineRenderInputUpgradeReceipt, error) {
	if _, err := os.Stat(path); err == nil {
		var existing pipelineRenderInputUpgradeReceipt
		if err := readPipelinePlanningJSON(path, &existing); err != nil {
			return existing, fmt.Errorf("read render input upgrade receipt: %w", err)
		}
		if err := validatePipelineRenderInputUpgradeReceiptValue(
			existing, identity, upgradeID, previousCandidateID, currentCandidateID,
		); err != nil {
			return existing, err
		}
		return existing, nil
	} else if !os.IsNotExist(err) {
		return pipelineRenderInputUpgradeReceipt{}, err
	}

	receipt := pipelineRenderInputUpgradeReceipt{
		Version:                identity.Version,
		UpgradeID:              upgradeID,
		Chapter:                identity.Chapter,
		PlanDigest:             identity.PlanDigest,
		PlanCheckpointSeq:      identity.PlanCheckpointSeq,
		ProjectionBinding:      identity.ProjectionBinding,
		RenderContextSHA256:    identity.RenderContextSHA256,
		ProjectedBundleDigest:  identity.ProjectedBundleDigest,
		PromotionReceiptDigest: identity.PromotionReceiptDigest,
		PreviousInputDigest:    identity.PreviousInputDigest,
		CurrentInputDigest:     identity.CurrentInputDigest,
		PreviousCandidateID:    previousCandidateID,
		CurrentCandidateID:     currentCandidateID,
		Authorization:          "explicit pipeline --refresh-render-input",
		UpgradedAt:             time.Now().UTC().Format(time.RFC3339Nano),
	}
	receipt.ReceiptDigest = pipelineRenderInputUpgradeReceiptDigest(receipt)
	if _, err := writePipelinePlanningJSON(path, receipt); err != nil {
		return receipt, fmt.Errorf("write render input upgrade receipt: %w", err)
	}
	return receipt, nil
}

func pipelineRenderInputUpgradeReceiptDigest(receipt pipelineRenderInputUpgradeReceipt) string {
	receipt.ReceiptDigest = ""
	return pipelineProjectAllDigest(receipt)
}

func validatePipelineRenderInputUpgradeReceipt(outputDir string, frozen *pipelineFrozenPlan) error {
	if frozen == nil {
		return fmt.Errorf("render input upgrade frozen plan is nil")
	}
	if frozen.RenderInputUpgradeID == "" && frozen.RenderInputUpgradeReceiptDigest == "" {
		return nil
	}
	if frozen.RenderInputUpgradeID == "" || frozen.RenderInputUpgradeReceiptDigest == "" {
		return fmt.Errorf("render input upgrade binding is incomplete")
	}
	if !pipelineRenderInputUpgradeIDValid(frozen.RenderInputUpgradeID) ||
		!pipelineRenderInputSHA256(frozen.RenderInputUpgradeReceiptDigest) {
		return fmt.Errorf("render input upgrade id or receipt digest is malformed")
	}
	path := pipelineRenderInputUpgradeReceiptPath(outputDir, frozen.RenderInputUpgradeID)
	var receipt pipelineRenderInputUpgradeReceipt
	if err := readPipelinePlanningJSON(path, &receipt); err != nil {
		return fmt.Errorf("read bound render input upgrade receipt: %w", err)
	}
	previous := *frozen
	previous.PipelineRunInputDigest = receipt.PreviousInputDigest
	previous.RenderInputUpgradeID = ""
	previous.RenderInputUpgradeReceiptDigest = ""
	previousCandidateID, err := pipelineRenderTransactionID(&previous)
	if err != nil {
		return err
	}
	current := *frozen
	current.RenderInputUpgradeID = ""
	current.RenderInputUpgradeReceiptDigest = ""
	currentCandidateID, err := pipelineRenderTransactionID(&current)
	if err != nil {
		return err
	}
	identity := pipelineRenderInputUpgradeIdentity{
		Version:                pipelineRenderInputUpgradeVersion,
		Chapter:                frozen.Chapter,
		PlanDigest:             frozen.PlanDigest,
		PlanCheckpointSeq:      frozen.PlanCheckpointSeq,
		ProjectionBinding:      frozen.ProjectionBinding,
		RenderContextSHA256:    frozen.RenderContextSHA256,
		ProjectedBundleDigest:  frozen.ProjectedBundleDigest,
		PromotionReceiptDigest: frozen.PromotionReceiptDigest,
		PreviousInputDigest:    receipt.PreviousInputDigest,
		CurrentInputDigest:     frozen.PipelineRunInputDigest,
	}
	if err := validatePipelineRenderInputUpgradeReceiptValue(
		receipt,
		identity,
		frozen.RenderInputUpgradeID,
		previousCandidateID,
		currentCandidateID,
	); err != nil {
		return err
	}
	if receipt.ReceiptDigest != frozen.RenderInputUpgradeReceiptDigest {
		return fmt.Errorf("render input upgrade receipt digest does not match frozen binding")
	}
	return nil
}

func validatePipelineRenderInputUpgradeReceiptValue(
	receipt pipelineRenderInputUpgradeReceipt,
	identity pipelineRenderInputUpgradeIdentity,
	upgradeID string,
	previousCandidateID string,
	currentCandidateID string,
) error {
	if receipt.Version != pipelineRenderInputUpgradeVersion ||
		receipt.UpgradeID != upgradeID ||
		receipt.UpgradeID != pipelineRenderInputUpgradeID(identity) ||
		receipt.Chapter != identity.Chapter ||
		receipt.PlanDigest != identity.PlanDigest ||
		receipt.PlanCheckpointSeq != identity.PlanCheckpointSeq ||
		receipt.ProjectionBinding != identity.ProjectionBinding ||
		receipt.RenderContextSHA256 != identity.RenderContextSHA256 ||
		receipt.ProjectedBundleDigest != identity.ProjectedBundleDigest ||
		receipt.PromotionReceiptDigest != identity.PromotionReceiptDigest ||
		receipt.PreviousInputDigest != identity.PreviousInputDigest ||
		receipt.CurrentInputDigest != identity.CurrentInputDigest ||
		receipt.PreviousCandidateID != previousCandidateID ||
		receipt.CurrentCandidateID != currentCandidateID ||
		receipt.Authorization != "explicit pipeline --refresh-render-input" ||
		receipt.ReceiptDigest != pipelineRenderInputUpgradeReceiptDigest(receipt) {
		return fmt.Errorf("render input upgrade receipt identity or digest is invalid")
	}
	if _, err := time.Parse(time.RFC3339Nano, receipt.UpgradedAt); err != nil {
		return fmt.Errorf("render input upgrade timestamp is invalid: %w", err)
	}
	return nil
}

func pipelineRequireNoRenderInputUpgradeAttempt(
	outputDir string,
	previousCandidateID string,
	chapter int,
) error {
	if err := pipelineRequireNoRenderTransactionEvidence(outputDir, previousCandidateID); err != nil {
		return err
	}
	for _, path := range []string{
		filepath.Join(outputDir, "drafts", fmt.Sprintf("%02d.draft.md", chapter)),
		filepath.Join(outputDir, "drafts", fmt.Sprintf("%02d.parts", chapter)),
		filepath.Join(outputDir, "chapters", fmt.Sprintf("%02d.md", chapter)),
	} {
		if _, err := os.Lstat(path); err == nil {
			return fmt.Errorf("render input upgrade refused: chapter prose already exists at %s", path)
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func pipelineRequireNoRenderTransactionEvidence(
	outputDir string,
	previousCandidateID string,
) error {
	if strings.TrimSpace(previousCandidateID) == "" || filepath.Base(previousCandidateID) != previousCandidateID {
		return fmt.Errorf("render input upgrade previous candidate id is invalid")
	}
	root := pipelineRenderCandidateRoot(outputDir)
	paths := []string{
		filepath.Join(root, previousCandidateID),
		filepath.Join(root, "convergence", previousCandidateID),
		filepath.Join(root, "rejections", previousCandidateID),
	}
	for _, path := range paths {
		if _, err := os.Lstat(path); err == nil {
			return fmt.Errorf("render input upgrade refused: previous candidate already has durable evidence at %s", path)
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	retiredRoot := filepath.Join(root, "retired")
	if entries, err := os.ReadDir(retiredRoot); err == nil {
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), previousCandidateID+"-") {
				return fmt.Errorf("render input upgrade refused: previous candidate already has retired evidence %s", entry.Name())
			}
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	publishDir := filepath.Join(pipelineRenderTransactionRoot(outputDir), previousCandidateID)
	if _, err := os.Lstat(publishDir); err == nil {
		return fmt.Errorf("render input upgrade refused: previous candidate already has directory-publish evidence at %s", publishDir)
	} else if !os.IsNotExist(err) {
		return err
	}
	publishState, err := store.NewDirectoryPublishStore(
		pipelineRenderTransactionRoot(outputDir),
	).LoadDirectoryPublishState(previousCandidateID)
	if err != nil {
		return err
	}
	if publishState != nil {
		return fmt.Errorf("render input upgrade refused: previous candidate already has directory-publish state %s", publishState.Phase)
	}
	return nil
}

func pipelineRenderInputUpgradeIDValid(value string) bool {
	const prefix = "render-input-upgrade-"
	if filepath.Base(value) != value || !strings.HasPrefix(value, prefix) || len(value) != len(prefix)+24 {
		return false
	}
	for _, r := range strings.TrimPrefix(value, prefix) {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			return false
		}
	}
	return true
}

func pipelineRenderInputSHA256(value string) bool {
	if len(value) != len("sha256:")+64 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	for _, r := range strings.TrimPrefix(value, "sha256:") {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			return false
		}
	}
	return true
}
