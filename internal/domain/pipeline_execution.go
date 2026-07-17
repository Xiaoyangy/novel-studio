package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// PipelineExecutionMode is the mechanical boundary between chapter inference
// and prose rendering. The default (no persisted lock) preserves the legacy
// mixed execution path.
type PipelineExecutionMode string

const (
	PipelineExecutionFoundation PipelineExecutionMode = "foundation"
	PipelineExecutionOutlineAll PipelineExecutionMode = "outline_all"
	PipelineExecutionPreplan    PipelineExecutionMode = "preplan"
	PipelineExecutionProjectAll PipelineExecutionMode = "project_all"
	PipelineExecutionRender     PipelineExecutionMode = "render"
)

const (
	PlanningContextAccessReceiptVersion = 1
	PlanningContextAccessTokenPrefix    = "context-access:"
	WritingPipelineModeReceiptVersion   = 1
	WritingPipelineModeSealedTwoPassV2  = "sealed_two_pass_v2"
)

type WritingPipelineModeReceipt struct {
	Version       int    `json:"version"`
	Mode          string `json:"mode"`
	ActivatedAt   string `json:"activated_at"`
	ReceiptDigest string `json:"receipt_digest"`
}

func ComputeWritingPipelineModeReceiptDigest(
	receipt WritingPipelineModeReceipt,
) (string, error) {
	receipt.ReceiptDigest = ""
	raw, err := json.Marshal(receipt)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return PlanningV2DigestPrefix + hex.EncodeToString(sum[:]), nil
}

func ValidateWritingPipelineModeReceipt(receipt WritingPipelineModeReceipt) error {
	if receipt.Version != WritingPipelineModeReceiptVersion ||
		receipt.Mode != WritingPipelineModeSealedTwoPassV2 ||
		strings.TrimSpace(receipt.ActivatedAt) == "" {
		return fmt.Errorf("writing pipeline mode receipt has invalid identity")
	}
	if _, err := time.Parse(time.RFC3339Nano, receipt.ActivatedAt); err != nil {
		return fmt.Errorf("writing pipeline mode receipt activated_at: %w", err)
	}
	if !strings.HasPrefix(receipt.ReceiptDigest, PlanningV2DigestPrefix) {
		return fmt.Errorf("writing pipeline mode receipt digest is missing")
	}
	want, err := ComputeWritingPipelineModeReceiptDigest(receipt)
	if err != nil {
		return err
	}
	if receipt.ReceiptDigest != want {
		return fmt.Errorf("writing pipeline mode receipt digest mismatch")
	}
	return nil
}

type PlanningContextAccessPhase string

const (
	PlanningContextAccessSimulate PlanningContextAccessPhase = "simulate"
	PlanningContextAccessPlan     PlanningContextAccessPhase = "plan"
)

// PlanningContextAccessReceipt is server-issued proof that novel_context
// successfully returned the exact phase context during the active project-all
// execution. Only a hash of the opaque source token is persisted. A model
// copying token-shaped text therefore cannot manufacture a receipt.
type PlanningContextAccessReceipt struct {
	Version               int                        `json:"version"`
	GenerationID          string                     `json:"generation_id"`
	Chapter               int                        `json:"chapter"`
	Profile               string                     `json:"profile"`
	PlanningContextDigest string                     `json:"planning_context_digest"`
	Phase                 PlanningContextAccessPhase `json:"phase"`
	LockMode              PipelineExecutionMode      `json:"lock_mode"`
	LockOwner             string                     `json:"lock_owner"`
	LockProcessID         int                        `json:"lock_process_id,omitempty"`
	LockAcquiredAt        time.Time                  `json:"lock_acquired_at"`
	IssuedAt              time.Time                  `json:"issued_at"`
	ExpiresAt             time.Time                  `json:"expires_at"`
	TokenSHA256           string                     `json:"token_sha256"`
	ConsumedAt            time.Time                  `json:"consumed_at,omitempty"`
	ReceiptDigest         string                     `json:"receipt_digest"`
}

func PlanningContextAccessTokenSHA256(sourceToken string) (string, error) {
	sourceToken = strings.TrimSpace(sourceToken)
	if !strings.HasPrefix(sourceToken, PlanningContextAccessTokenPrefix) {
		return "", fmt.Errorf("planning context access token has an invalid prefix")
	}
	opaque := strings.TrimPrefix(sourceToken, PlanningContextAccessTokenPrefix)
	decoded, err := hex.DecodeString(opaque)
	if err != nil || len(decoded) != sha256.Size {
		return "", fmt.Errorf("planning context access token has invalid entropy")
	}
	sum := sha256.Sum256([]byte(sourceToken))
	return PlanningV2DigestPrefix + hex.EncodeToString(sum[:]), nil
}

func ComputePlanningContextAccessReceiptDigest(
	receipt PlanningContextAccessReceipt,
) (string, error) {
	receipt.ReceiptDigest = ""
	receipt.ConsumedAt = time.Time{}
	raw, err := json.Marshal(receipt)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return PlanningV2DigestPrefix + hex.EncodeToString(sum[:]), nil
}

func ValidatePlanningContextAccessReceipt(receipt PlanningContextAccessReceipt) error {
	if receipt.Version != PlanningContextAccessReceiptVersion ||
		!strings.HasPrefix(strings.TrimSpace(receipt.GenerationID), PlanningGenerationIDPrefix) ||
		receipt.Chapter <= 0 ||
		receipt.LockMode != PipelineExecutionProjectAll ||
		strings.TrimSpace(receipt.LockOwner) == "" ||
		receipt.LockAcquiredAt.IsZero() ||
		receipt.IssuedAt.IsZero() ||
		receipt.ExpiresAt.IsZero() ||
		!receipt.ExpiresAt.After(receipt.IssuedAt) {
		return fmt.Errorf("planning context access receipt has invalid identity or lifetime")
	}
	switch receipt.Phase {
	case PlanningContextAccessSimulate:
		if receipt.Profile != "world_simulation" {
			return fmt.Errorf("simulate access receipt requires world_simulation profile")
		}
	case PlanningContextAccessPlan:
		if receipt.Profile != "planning" {
			return fmt.Errorf("plan access receipt requires planning profile")
		}
	default:
		return fmt.Errorf("planning context access receipt has unsupported phase")
	}
	if err := validatePlanningV2Digest(
		"planning_context_digest",
		receipt.PlanningContextDigest,
	); err != nil {
		return err
	}
	if err := validatePlanningV2Digest("token_sha256", receipt.TokenSHA256); err != nil {
		return err
	}
	if err := validatePlanningV2Digest("receipt_digest", receipt.ReceiptDigest); err != nil {
		return err
	}
	want, err := ComputePlanningContextAccessReceiptDigest(receipt)
	if err != nil {
		return err
	}
	if receipt.ReceiptDigest != want {
		return fmt.Errorf("planning context access receipt digest mismatch")
	}
	if !receipt.ConsumedAt.IsZero() &&
		(receipt.ConsumedAt.Before(receipt.IssuedAt) || receipt.ConsumedAt.After(receipt.ExpiresAt)) {
		return fmt.Errorf("planning context access receipt has invalid consumption time")
	}
	return nil
}

// PipelineExecutionLock scopes an execution mode to one chapter. It is
// intentionally leased: a crashed pipeline cannot leave planning or rendering
// permanently disabled.
type PipelineExecutionLock struct {
	Version       int                   `json:"version"`
	Mode          PipelineExecutionMode `json:"mode"`
	TargetChapter int                   `json:"target_chapter"`
	PlanDigest    string                `json:"plan_digest,omitempty"`
	Owner         string                `json:"owner"`
	ProcessID     int                   `json:"process_id,omitempty"`
	AcquiredAt    time.Time             `json:"acquired_at"`
	ExpiresAt     time.Time             `json:"expires_at"`
}

// ActiveAt reports whether the lease is still active at the supplied time.
func (l PipelineExecutionLock) ActiveAt(now time.Time) bool {
	return !l.ExpiresAt.IsZero() && now.Before(l.ExpiresAt)
}
