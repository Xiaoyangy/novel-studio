package domain

import (
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

const (
	PipelineRenderDispatchLedgerVersion  = "pipeline-render-dispatch-budget.v2-source-output"
	PipelineRenderWholeBodyDispatchLimit = 3
)

type PipelineRenderDispatchReservation struct {
	AuthorizationDigest  string `json:"authorization_digest"`
	Attempt              int    `json:"attempt"`
	Status               string `json:"status"`
	BodySHA256           string `json:"body_sha256,omitempty"`
	BodyCheckpointSeq    int64  `json:"body_checkpoint_seq,omitempty"`
	ReservedAt           string `json:"reserved_at"`
	PermitArmedAt        string `json:"permit_armed_at,omitempty"`
	ProviderDispatchedAt string `json:"provider_dispatched_at,omitempty"`
	FinishedAt           string `json:"finished_at,omitempty"`
}

type PipelineRenderDispatchLedger struct {
	Version                     string                              `json:"version"`
	CandidateID                 string                              `json:"candidate_id"`
	SourceOutputDir             string                              `json:"source_output_dir"`
	GenerationID                string                              `json:"generation_id"`
	Chapter                     int                                 `json:"chapter"`
	PlanDigest                  string                              `json:"plan_digest"`
	PlanCheckpointSeq           int64                               `json:"plan_checkpoint_seq"`
	ProjectedBundleDigest       string                              `json:"projected_bundle_digest"`
	PromotionReceiptDigest      string                              `json:"promotion_receipt_digest"`
	PipelineRenderInputDigest   string                              `json:"pipeline_render_input_digest,omitempty"`
	RenderContextSHA256         string                              `json:"render_context_sha256,omitempty"`
	EffectiveStyleReceiptDigest string                              `json:"effective_style_receipt_digest,omitempty"`
	Limit                       int                                 `json:"limit"`
	Reservations                []PipelineRenderDispatchReservation `json:"reservations"`
	UpdatedAt                   string                              `json:"updated_at"`
}

// ValidatePipelineRenderDispatchLedger validates the complete shared ledger
// schema. Both the orchestration budget and the provider-side permit boundary
// use this validator, so neither can accept evidence the other would reject.
func ValidatePipelineRenderDispatchLedger(ledger *PipelineRenderDispatchLedger) error {
	if ledger == nil || ledger.Version != PipelineRenderDispatchLedgerVersion ||
		strings.TrimSpace(ledger.CandidateID) == "" || ledger.CandidateID == "." || ledger.CandidateID == ".." || strings.ContainsAny(ledger.CandidateID, `/\\`) ||
		strings.TrimSpace(ledger.SourceOutputDir) == "" || !filepath.IsAbs(ledger.SourceOutputDir) || filepath.Clean(ledger.SourceOutputDir) != ledger.SourceOutputDir ||
		strings.TrimSpace(ledger.GenerationID) == "" || ledger.Chapter <= 0 ||
		!validPlanningSHA256(ledger.PlanDigest) || ledger.PlanCheckpointSeq <= 0 ||
		!validPlanningSHA256(ledger.ProjectedBundleDigest) || !validPlanningSHA256(ledger.PromotionReceiptDigest) ||
		ledger.Limit != PipelineRenderWholeBodyDispatchLimit || len(ledger.Reservations) > ledger.Limit {
		return fmt.Errorf("render dispatch ledger identity or limit is malformed")
	}
	styleBindingCount := 0
	for _, digest := range []string{
		ledger.PipelineRenderInputDigest,
		ledger.RenderContextSHA256,
		ledger.EffectiveStyleReceiptDigest,
	} {
		if strings.TrimSpace(digest) != "" {
			styleBindingCount++
			if !validPlanningSHA256(digest) {
				return fmt.Errorf("render dispatch ledger effective-style binding is malformed")
			}
		}
	}
	if styleBindingCount != 0 && styleBindingCount != 3 {
		return fmt.Errorf("render dispatch ledger effective-style binding is incomplete")
	}
	if _, err := time.Parse(time.RFC3339Nano, ledger.UpdatedAt); err != nil {
		return fmt.Errorf("render dispatch ledger updated_at is invalid: %w", err)
	}
	seen := make(map[string]struct{}, len(ledger.Reservations))
	for index, reservation := range ledger.Reservations {
		if !validPlanningSHA256(reservation.AuthorizationDigest) || reservation.Attempt != index+1 ||
			reservation.Attempt > ledger.Limit {
			return fmt.Errorf("render dispatch reservation %d identity is malformed", index+1)
		}
		if _, exists := seen[reservation.AuthorizationDigest]; exists {
			return fmt.Errorf("render dispatch authorization is duplicated")
		}
		seen[reservation.AuthorizationDigest] = struct{}{}
		reservedAt, err := time.Parse(time.RFC3339Nano, reservation.ReservedAt)
		if err != nil {
			return fmt.Errorf("render dispatch reserved_at is invalid: %w", err)
		}
		armedAt, err := optionalRenderDispatchTime("permit_armed_at", reservation.PermitArmedAt)
		if err != nil {
			return err
		}
		dispatchedAt, err := optionalRenderDispatchTime("provider_dispatched_at", reservation.ProviderDispatchedAt)
		if err != nil {
			return err
		}
		finishedAt, err := optionalRenderDispatchTime("finished_at", reservation.FinishedAt)
		if err != nil {
			return err
		}
		if !armedAt.IsZero() && armedAt.Before(reservedAt) ||
			!dispatchedAt.IsZero() && (armedAt.IsZero() || dispatchedAt.Before(armedAt)) ||
			!finishedAt.IsZero() && (finishedAt.Before(reservedAt) ||
				!armedAt.IsZero() && finishedAt.Before(armedAt) ||
				!dispatchedAt.IsZero() && finishedAt.Before(dispatchedAt)) {
			return fmt.Errorf("render dispatch reservation timestamps are not monotonic")
		}
		switch reservation.Status {
		case "reserved":
			if !armedAt.IsZero() || !dispatchedAt.IsZero() || !finishedAt.IsZero() || reservation.BodySHA256 != "" || reservation.BodyCheckpointSeq != 0 {
				return fmt.Errorf("reserved render dispatch contains later-phase evidence")
			}
		case "permit_armed":
			if armedAt.IsZero() || !dispatchedAt.IsZero() || !finishedAt.IsZero() || reservation.BodySHA256 != "" || reservation.BodyCheckpointSeq != 0 {
				return fmt.Errorf("armed render dispatch evidence is inconsistent")
			}
		case "provider_dispatched":
			if armedAt.IsZero() || dispatchedAt.IsZero() || !finishedAt.IsZero() || reservation.BodySHA256 != "" || reservation.BodyCheckpointSeq != 0 {
				return fmt.Errorf("provider-dispatched render evidence is inconsistent")
			}
		case "body_ready":
			if armedAt.IsZero() || dispatchedAt.IsZero() || finishedAt.IsZero() || !validPlanningSHA256(reservation.BodySHA256) || reservation.BodyCheckpointSeq <= 0 {
				return fmt.Errorf("body-ready render dispatch evidence is inconsistent")
			}
		case "no_durable_body", "provider_or_host_error":
			if armedAt.IsZero() || finishedAt.IsZero() || reservation.BodySHA256 != "" || reservation.BodyCheckpointSeq != 0 {
				return fmt.Errorf("finished render dispatch evidence is inconsistent")
			}
		default:
			return fmt.Errorf("render dispatch reservation status %q is unsupported", reservation.Status)
		}
	}
	return nil
}

func optionalRenderDispatchTime(name, value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("render dispatch %s is invalid: %w", name, err)
	}
	return parsed, nil
}

func validPlanningSHA256(value string) bool {
	if !strings.HasPrefix(value, PlanningV2DigestPrefix) || len(value) != len(PlanningV2DigestPrefix)+64 {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, PlanningV2DigestPrefix))
	return err == nil
}
