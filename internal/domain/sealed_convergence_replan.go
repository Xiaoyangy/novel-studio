package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

const SealedConvergenceReplanReceiptVersion = "sealed-convergence-replan.v1"

// SealedConvergenceReplanReceipt is a narrow successor binding for an active
// promoted chapter whose prose-only retry budget was durably exhausted.  It
// does not replace or mutate the sealed bundle/promotion.  Instead it proves
// that one newer formal plan/checkpoint and render context were produced from
// the same sealed state contract after a bounded set of exact bodies failed.
type SealedConvergenceReplanReceipt struct {
	Version                         string   `json:"version"`
	GenerationID                    string   `json:"generation_id"`
	Chapter                         int      `json:"chapter"`
	BundleDigest                    string   `json:"bundle_digest"`
	PromotionReceiptDigest          string   `json:"promotion_receipt_digest"`
	SealedPlanSemanticDigest        string   `json:"sealed_plan_semantic_digest"`
	PreviousPlanCheckpointDigest    string   `json:"previous_plan_checkpoint_digest"`
	PreviousPlanCheckpointSeq       int64    `json:"previous_plan_checkpoint_seq"`
	PreviousReplanReceiptDigest     string   `json:"previous_replan_receipt_digest,omitempty"`
	ExhaustedCandidateID            string   `json:"exhausted_candidate_id"`
	ExhaustedLedgerSHA256           string   `json:"exhausted_ledger_sha256"`
	FailedBodySHA256                []string `json:"failed_body_sha256"`
	FailureCount                    int      `json:"failure_count"`
	FailureLimit                    int      `json:"failure_limit"`
	ReplacementPlanSemanticDigest   string   `json:"replacement_plan_semantic_digest"`
	ReplacementPlanCheckpointDigest string   `json:"replacement_plan_checkpoint_digest"`
	ReplacementPlanCheckpointSeq    int64    `json:"replacement_plan_checkpoint_seq"`
	ReplacementRenderContextSHA256  string   `json:"replacement_render_context_sha256"`
	StateContractDigest             string   `json:"state_contract_digest"`
	FeedbackDigest                  string   `json:"feedback_digest"`
	CreatedAt                       string   `json:"created_at"`
	ReceiptDigest                   string   `json:"receipt_digest"`
}

func ComputeSealedConvergenceReplanReceiptDigest(receipt SealedConvergenceReplanReceipt) (string, error) {
	receipt.ReceiptDigest = ""
	receipt.FailedBodySHA256 = normalizedSealedReplanHashes(receipt.FailedBodySHA256)
	raw, err := json.Marshal(receipt)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return PlanningV2DigestPrefix + hex.EncodeToString(sum[:]), nil
}

func ValidateSealedConvergenceReplanReceipt(receipt SealedConvergenceReplanReceipt) error {
	if receipt.Version != SealedConvergenceReplanReceiptVersion ||
		!strings.HasPrefix(strings.TrimSpace(receipt.GenerationID), PlanningGenerationIDPrefix) ||
		receipt.Chapter <= 0 {
		return fmt.Errorf("sealed convergence replan receipt: invalid version/generation/chapter")
	}
	for name, value := range map[string]string{
		"bundle_digest":                      receipt.BundleDigest,
		"promotion_receipt_digest":           receipt.PromotionReceiptDigest,
		"sealed_plan_semantic_digest":        receipt.SealedPlanSemanticDigest,
		"previous_plan_checkpoint_digest":    receipt.PreviousPlanCheckpointDigest,
		"exhausted_ledger_sha256":            receipt.ExhaustedLedgerSHA256,
		"replacement_plan_semantic_digest":   receipt.ReplacementPlanSemanticDigest,
		"replacement_plan_checkpoint_digest": receipt.ReplacementPlanCheckpointDigest,
		"replacement_render_context_sha256":  receipt.ReplacementRenderContextSHA256,
		"state_contract_digest":              receipt.StateContractDigest,
		"feedback_digest":                    receipt.FeedbackDigest,
		"receipt_digest":                     receipt.ReceiptDigest,
	} {
		if !sealedReplanPlanningDigest(value) {
			return fmt.Errorf("sealed convergence replan receipt: %s is malformed", name)
		}
	}
	if strings.TrimSpace(receipt.PreviousReplanReceiptDigest) != "" &&
		!sealedReplanPlanningDigest(receipt.PreviousReplanReceiptDigest) {
		return fmt.Errorf("sealed convergence replan receipt: previous_replan_receipt_digest is malformed")
	}
	if receipt.PreviousPlanCheckpointSeq <= 0 || receipt.ReplacementPlanCheckpointSeq <= receipt.PreviousPlanCheckpointSeq {
		return fmt.Errorf("sealed convergence replan receipt: replacement plan is not a successor epoch")
	}
	if strings.TrimSpace(receipt.ExhaustedCandidateID) == "" ||
		strings.ContainsAny(receipt.ExhaustedCandidateID, `/\\`) {
		return fmt.Errorf("sealed convergence replan receipt: exhausted_candidate_id is malformed")
	}
	if receipt.FailureLimit < 2 || receipt.FailureLimit > 4 ||
		receipt.FailureCount < receipt.FailureLimit {
		return fmt.Errorf("sealed convergence replan receipt: convergence budget is not exhausted")
	}
	hashes := normalizedSealedReplanHashes(receipt.FailedBodySHA256)
	if len(hashes) != len(receipt.FailedBodySHA256) || len(hashes) != receipt.FailureCount {
		return fmt.Errorf("sealed convergence replan receipt: failed exact-body set/count mismatch")
	}
	for _, value := range hashes {
		if !sealedReplanBodyHash(value) {
			return fmt.Errorf("sealed convergence replan receipt: failed body hash is malformed")
		}
	}
	if _, err := time.Parse(time.RFC3339Nano, receipt.CreatedAt); err != nil {
		return fmt.Errorf("sealed convergence replan receipt: created_at: %w", err)
	}
	want, err := ComputeSealedConvergenceReplanReceiptDigest(receipt)
	if err != nil {
		return err
	}
	if receipt.ReceiptDigest != want {
		return fmt.Errorf("sealed convergence replan receipt: receipt_digest mismatch")
	}
	return nil
}

func normalizedSealedReplanHashes(values []string) []string {
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
	return out
}

func sealedReplanPlanningDigest(value string) bool {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, PlanningV2DigestPrefix) {
		return false
	}
	decoded, err := hex.DecodeString(strings.TrimPrefix(value, PlanningV2DigestPrefix))
	return err == nil && len(decoded) == sha256.Size
}

func sealedReplanBodyHash(value string) bool {
	decoded, err := hex.DecodeString(strings.TrimSpace(value))
	return err == nil && len(decoded) == sha256.Size
}
