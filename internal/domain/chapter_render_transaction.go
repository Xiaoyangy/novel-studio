package domain

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

const (
	ChapterRenderPlanIdentityVersion = "chapter-render-plan-identity.v1"
	ChapterRenderBodyIdentityVersion = "chapter-render-body-identity.v1"
	ChapterRenderPhaseReceiptVersion = "chapter-render-phase-receipt.v1"
)

// ChapterRenderPlanIdentity is the first content-addressed level of a sealed
// render. It names an exact promoted plan and every immutable input consumed
// before prose exists. One plan attempt may legitimately produce several body
// transactions after bounded semantic rejection.
type ChapterRenderPlanIdentity struct {
	Version                        string `json:"version"`
	ProtocolVersion                string `json:"protocol_version"`
	GenerationID                   string `json:"generation_id"`
	Chapter                        int    `json:"chapter"`
	PlanDigest                     string `json:"plan_digest"`
	PlanCheckpointSeq              int64  `json:"plan_checkpoint_seq"`
	ProjectedBundleDigest          string `json:"projected_bundle_digest"`
	PromotionReceiptDigest         string `json:"promotion_receipt_digest"`
	PipelineRunInputDigest         string `json:"pipeline_run_input_digest"`
	RenderContextSHA256            string `json:"render_context_sha256"`
	ConvergenceReplanReceiptDigest string `json:"convergence_replan_receipt_digest,omitempty"`
}

// ChapterRenderBodyIdentity is the second content-addressed level. The body
// transaction ID transitively binds all plan inputs through PlanAttemptID and
// additionally binds the exact UTF-8 chapter bytes.
type ChapterRenderBodyIdentity struct {
	Version       string                    `json:"version"`
	Plan          ChapterRenderPlanIdentity `json:"plan"`
	PlanAttemptID string                    `json:"plan_attempt_id"`
	BodySHA256    string                    `json:"body_sha256"`
	TransactionID string                    `json:"transaction_id"`
}

type ChapterRenderPhase string

const (
	ChapterRenderPhaseBodyReady           ChapterRenderPhase = "body_ready"
	ChapterRenderPhaseCommitted           ChapterRenderPhase = "committed"
	ChapterRenderPhaseStructurallyBlocked ChapterRenderPhase = "structurally_blocked"
	ChapterRenderPhaseFormalAccepted      ChapterRenderPhase = "formal_accepted"
	ChapterRenderPhaseFormalRejected      ChapterRenderPhase = "formal_rejected"
	ChapterRenderPhaseActualMatched       ChapterRenderPhase = "actual_matched"
	ChapterRenderPhasePublished           ChapterRenderPhase = "published"
	ChapterRenderPhaseOutcomeAccepted     ChapterRenderPhase = "outcome_accepted"
	ChapterRenderPhaseChapterAccepted     ChapterRenderPhase = "chapter_accepted"
	ChapterRenderPhaseCompleted           ChapterRenderPhase = "completed"
)

type ChapterRenderArtifactBinding struct {
	Path   string `json:"path"`
	Digest string `json:"digest"`
}

// ChapterRenderPhaseEvidence is a typed union. Each phase requires only its
// own fields; later receipts are chained to earlier evidence by
// PreviousReceiptDigest rather than copying a mutable cumulative snapshot.
type ChapterRenderPhaseEvidence struct {
	BodyCheckpointSeq           int64  `json:"body_checkpoint_seq,omitempty"`
	BodyCheckpointDigest        string `json:"body_checkpoint_digest,omitempty"`
	EffectiveStyleReceiptDigest string `json:"effective_style_receipt_digest,omitempty"`
	CommitCheckpointSeq         int64  `json:"commit_checkpoint_seq,omitempty"`
	CommitDigest                string `json:"commit_digest,omitempty"`
	// CandidateRoot is the deterministic canon root at commit time. It binds
	// chapter bodies and every other canonical artifact while excluding
	// operational namespaces such as meta/runtime.
	CandidateRoot           string                         `json:"candidate_root,omitempty"`
	ReviewVerdict           string                         `json:"review_verdict,omitempty"`
	ReviewDisposition       string                         `json:"review_disposition,omitempty"`
	ReviewArtifacts         []ChapterRenderArtifactBinding `json:"review_artifacts,omitempty"`
	EditorCacheKey          string                         `json:"editor_cache_key,omitempty"`
	DeepSeekCacheKey        string                         `json:"deepseek_cache_key,omitempty"`
	ActualMatchDigest       string                         `json:"actual_match_digest,omitempty"`
	DirectoryPublishID      string                         `json:"directory_publish_id,omitempty"`
	DirectoryPublishDigest  string                         `json:"directory_publish_digest,omitempty"`
	OutcomeReceiptDigest    string                         `json:"outcome_receipt_digest,omitempty"`
	ChapterAcceptanceDigest string                         `json:"chapter_acceptance_digest,omitempty"`
	RenderReceiptDigest     string                         `json:"render_receipt_digest,omitempty"`
	Reason                  string                         `json:"reason,omitempty"`
}

// ChapterRenderPhaseReceipt is an immutable link in one body transaction.
// Fixed phase file names and PreviousReceiptDigest make the latest valid phase
// reconstructible without a mutable HEAD pointer.
type ChapterRenderPhaseReceipt struct {
	Version               string                     `json:"version"`
	Identity              ChapterRenderBodyIdentity  `json:"identity"`
	Phase                 ChapterRenderPhase         `json:"phase"`
	PreviousReceiptDigest string                     `json:"previous_receipt_digest,omitempty"`
	Evidence              ChapterRenderPhaseEvidence `json:"evidence"`
	CreatedAt             string                     `json:"created_at"`
	ReceiptDigest         string                     `json:"receipt_digest"`
}

func ComputeChapterRenderBodySHA256(body []byte) string {
	return ComputeArcArtifactSHA256(body)
}

func ComputeChapterRenderPlanAttemptID(identity ChapterRenderPlanIdentity) (string, error) {
	if err := ValidateChapterRenderPlanIdentity(identity); err != nil {
		return "", err
	}
	digest, err := planningV2Digest(struct {
		Version  string                    `json:"version"`
		Identity ChapterRenderPlanIdentity `json:"identity"`
	}{Version: "chapter-render-plan-address.v1", Identity: identity})
	if err != nil {
		return "", err
	}
	return "render-plan-" + strings.TrimPrefix(digest, PlanningV2DigestPrefix), nil
}

func NewChapterRenderBodyIdentity(
	plan ChapterRenderPlanIdentity,
	bodySHA256 string,
) (ChapterRenderBodyIdentity, error) {
	identity, err := deriveChapterRenderBodyIdentity(plan, bodySHA256)
	if err != nil {
		return ChapterRenderBodyIdentity{}, err
	}
	if err := ValidateChapterRenderBodyIdentity(identity); err != nil {
		return ChapterRenderBodyIdentity{}, err
	}
	return identity, nil
}

func ValidateChapterRenderPlanIdentity(identity ChapterRenderPlanIdentity) error {
	const prefix = "chapter render plan identity"
	if identity.Version != ChapterRenderPlanIdentityVersion {
		return fmt.Errorf("%s: unsupported version %q", prefix, identity.Version)
	}
	for name, value := range map[string]string{
		"protocol_version": identity.ProtocolVersion,
		"generation_id":    identity.GenerationID,
	} {
		if strings.TrimSpace(value) == "" || strings.TrimSpace(value) != value {
			return fmt.Errorf("%s: %s is empty or not canonical", prefix, name)
		}
	}
	if identity.Chapter <= 0 || identity.PlanCheckpointSeq <= 0 {
		return fmt.Errorf("%s: chapter and plan_checkpoint_seq must be > 0", prefix)
	}
	for name, digest := range map[string]string{
		"plan_digest":               identity.PlanDigest,
		"projected_bundle_digest":   identity.ProjectedBundleDigest,
		"promotion_receipt_digest":  identity.PromotionReceiptDigest,
		"pipeline_run_input_digest": identity.PipelineRunInputDigest,
		"render_context_sha256":     identity.RenderContextSHA256,
	} {
		if err := validatePlanningV2Digest(name, digest); err != nil {
			return fmt.Errorf("%s: %w", prefix, err)
		}
	}
	if identity.ConvergenceReplanReceiptDigest != "" {
		if err := validatePlanningV2Digest("convergence_replan_receipt_digest", identity.ConvergenceReplanReceiptDigest); err != nil {
			return fmt.Errorf("%s: %w", prefix, err)
		}
	}
	return nil
}

func ValidateChapterRenderBodyIdentity(identity ChapterRenderBodyIdentity) error {
	const prefix = "chapter render body identity"
	if identity.Version != ChapterRenderBodyIdentityVersion {
		return fmt.Errorf("%s: unsupported version %q", prefix, identity.Version)
	}
	if err := ValidateChapterRenderPlanIdentity(identity.Plan); err != nil {
		return fmt.Errorf("%s: %w", prefix, err)
	}
	wantPlanID, err := ComputeChapterRenderPlanAttemptID(identity.Plan)
	if err != nil {
		return err
	}
	if identity.PlanAttemptID != wantPlanID {
		return fmt.Errorf("%s: plan_attempt_id mismatch", prefix)
	}
	if err := validatePlanningV2Digest("body_sha256", identity.BodySHA256); err != nil {
		return fmt.Errorf("%s: %w", prefix, err)
	}
	want, err := deriveChapterRenderBodyIdentity(identity.Plan, identity.BodySHA256)
	if err != nil {
		return err
	}
	if identity.TransactionID != want.TransactionID {
		return fmt.Errorf("%s: transaction_id mismatch", prefix)
	}
	return nil
}

// deriveChapterRenderBodyIdentity computes the address without invoking the
// public validator recursively.
func deriveChapterRenderBodyIdentity(
	plan ChapterRenderPlanIdentity,
	bodySHA256 string,
) (ChapterRenderBodyIdentity, error) {
	planID, err := ComputeChapterRenderPlanAttemptID(plan)
	if err != nil {
		return ChapterRenderBodyIdentity{}, err
	}
	bodySHA256 = strings.TrimSpace(bodySHA256)
	if err := validatePlanningV2Digest("body_sha256", bodySHA256); err != nil {
		return ChapterRenderBodyIdentity{}, err
	}
	digest, err := planningV2Digest(struct {
		Version       string `json:"version"`
		PlanAttemptID string `json:"plan_attempt_id"`
		BodySHA256    string `json:"body_sha256"`
	}{Version: "chapter-render-body-address.v1", PlanAttemptID: planID, BodySHA256: bodySHA256})
	if err != nil {
		return ChapterRenderBodyIdentity{}, err
	}
	return ChapterRenderBodyIdentity{
		Version:       ChapterRenderBodyIdentityVersion,
		Plan:          plan,
		PlanAttemptID: planID,
		BodySHA256:    bodySHA256,
		TransactionID: "render-body-" + strings.TrimPrefix(digest, PlanningV2DigestPrefix),
	}, nil
}

func CanonicalChapterRenderArtifacts(
	artifacts []ChapterRenderArtifactBinding,
) []ChapterRenderArtifactBinding {
	out := append([]ChapterRenderArtifactBinding(nil), artifacts...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].Digest < out[j].Digest
	})
	return out
}

func ChapterRenderPhaseOrdinal(phase ChapterRenderPhase) int {
	switch phase {
	case ChapterRenderPhaseBodyReady:
		return 1
	case ChapterRenderPhaseCommitted, ChapterRenderPhaseStructurallyBlocked:
		if phase == ChapterRenderPhaseCommitted {
			return 2
		}
		return 3
	case ChapterRenderPhaseFormalRejected:
		return 3
	case ChapterRenderPhaseFormalAccepted:
		return 4
	case ChapterRenderPhaseActualMatched:
		return 5
	case ChapterRenderPhasePublished:
		return 6
	case ChapterRenderPhaseOutcomeAccepted:
		return 7
	case ChapterRenderPhaseChapterAccepted:
		return 8
	case ChapterRenderPhaseCompleted:
		return 9
	default:
		return 0
	}
}

func ChapterRenderPhaseTerminal(phase ChapterRenderPhase) bool {
	return phase == ChapterRenderPhaseStructurallyBlocked ||
		phase == ChapterRenderPhaseCompleted
}

func ValidateChapterRenderPhaseTransition(previous, next ChapterRenderPhase) error {
	if previous == "" {
		if next != ChapterRenderPhaseBodyReady {
			return fmt.Errorf("chapter render transaction must start at %s", ChapterRenderPhaseBodyReady)
		}
		return nil
	}
	if ChapterRenderPhaseTerminal(previous) {
		return fmt.Errorf("chapter render phase %s is terminal", previous)
	}
	allowed := map[ChapterRenderPhase][]ChapterRenderPhase{
		ChapterRenderPhaseBodyReady: {
			ChapterRenderPhaseCommitted,
			ChapterRenderPhaseStructurallyBlocked,
		},
		ChapterRenderPhaseCommitted: {
			ChapterRenderPhaseFormalAccepted,
			ChapterRenderPhaseFormalRejected,
			ChapterRenderPhaseStructurallyBlocked,
		},
		ChapterRenderPhaseFormalAccepted: {ChapterRenderPhaseActualMatched},
		// A semantic rejection remains an immutable negative receipt. The only
		// legal escape is the existing cache-only/current-policy revalidation
		// protocol, represented by a later accepted receipt rather than
		// overwriting the rejected phase or pretending it never happened.
		ChapterRenderPhaseFormalRejected:  {ChapterRenderPhaseFormalAccepted},
		ChapterRenderPhaseActualMatched:   {ChapterRenderPhasePublished},
		ChapterRenderPhasePublished:       {ChapterRenderPhaseOutcomeAccepted},
		ChapterRenderPhaseOutcomeAccepted: {ChapterRenderPhaseChapterAccepted},
		ChapterRenderPhaseChapterAccepted: {ChapterRenderPhaseCompleted},
	}
	for _, candidate := range allowed[previous] {
		if candidate == next {
			return nil
		}
	}
	return fmt.Errorf("chapter render phase cannot advance from %s to %s", previous, next)
}

func ComputeChapterRenderPhaseReceiptDigest(
	receipt ChapterRenderPhaseReceipt,
) (string, error) {
	receipt.ReceiptDigest = ""
	receipt.Evidence.ReviewArtifacts = CanonicalChapterRenderArtifacts(receipt.Evidence.ReviewArtifacts)
	return planningV2Digest(receipt)
}

func SignChapterRenderPhaseReceipt(
	receipt ChapterRenderPhaseReceipt,
) (ChapterRenderPhaseReceipt, error) {
	receipt.Evidence.ReviewArtifacts = CanonicalChapterRenderArtifacts(receipt.Evidence.ReviewArtifacts)
	digest, err := ComputeChapterRenderPhaseReceiptDigest(receipt)
	if err != nil {
		return receipt, err
	}
	receipt.ReceiptDigest = digest
	if err := ValidateChapterRenderPhaseReceipt(receipt); err != nil {
		return receipt, err
	}
	return receipt, nil
}

func ValidateChapterRenderPhaseReceipt(receipt ChapterRenderPhaseReceipt) error {
	const prefix = "chapter render phase receipt"
	if receipt.Version != ChapterRenderPhaseReceiptVersion {
		return fmt.Errorf("%s: unsupported version %q", prefix, receipt.Version)
	}
	if err := ValidateChapterRenderBodyIdentity(receipt.Identity); err != nil {
		return fmt.Errorf("%s: %w", prefix, err)
	}
	if ChapterRenderPhaseOrdinal(receipt.Phase) == 0 {
		return fmt.Errorf("%s: unsupported phase %q", prefix, receipt.Phase)
	}
	if receipt.Phase == ChapterRenderPhaseBodyReady {
		if receipt.PreviousReceiptDigest != "" {
			return fmt.Errorf("%s: body_ready must not have a previous receipt", prefix)
		}
	} else if err := validatePlanningV2Digest("previous_receipt_digest", receipt.PreviousReceiptDigest); err != nil {
		return fmt.Errorf("%s: %w", prefix, err)
	}
	if err := validateChapterRenderPhaseEvidence(receipt.Phase, receipt.Identity, receipt.Evidence); err != nil {
		return fmt.Errorf("%s: %w", prefix, err)
	}
	if err := validatePlanningV2Time("created_at", receipt.CreatedAt); err != nil {
		return fmt.Errorf("%s: %w", prefix, err)
	}
	if err := validatePlanningV2Digest("receipt_digest", receipt.ReceiptDigest); err != nil {
		return fmt.Errorf("%s: %w", prefix, err)
	}
	want, err := ComputeChapterRenderPhaseReceiptDigest(receipt)
	if err != nil {
		return err
	}
	if receipt.ReceiptDigest != want {
		return fmt.Errorf("%s: receipt_digest mismatch", prefix)
	}
	return nil
}

func validateChapterRenderPhaseEvidence(
	phase ChapterRenderPhase,
	identity ChapterRenderBodyIdentity,
	evidence ChapterRenderPhaseEvidence,
) error {
	for i, artifact := range evidence.ReviewArtifacts {
		if artifact != CanonicalChapterRenderArtifacts(evidence.ReviewArtifacts)[i] {
			return fmt.Errorf("review_artifacts must be in canonical order")
		}
		clean := filepath.ToSlash(filepath.Clean(strings.TrimSpace(artifact.Path)))
		if clean == "." || clean != strings.TrimSpace(artifact.Path) || strings.HasPrefix(clean, "../") || filepath.IsAbs(clean) {
			return fmt.Errorf("review_artifacts[%d].path is unsafe", i)
		}
		if err := validatePlanningV2Digest(fmt.Sprintf("review_artifacts[%d].digest", i), artifact.Digest); err != nil {
			return err
		}
		if i > 0 && evidence.ReviewArtifacts[i-1].Path == artifact.Path {
			return fmt.Errorf("review artifact path %q is duplicated", artifact.Path)
		}
	}
	requireDigest := func(name, value string) error {
		if err := validatePlanningV2Digest(name, value); err != nil {
			return err
		}
		return nil
	}
	if evidence.EffectiveStyleReceiptDigest != "" {
		if err := requireDigest("effective_style_receipt_digest", evidence.EffectiveStyleReceiptDigest); err != nil {
			return err
		}
	}
	switch phase {
	case ChapterRenderPhaseBodyReady:
		if evidence.BodyCheckpointSeq <= 0 || evidence.BodyCheckpointDigest != identity.BodySHA256 {
			return fmt.Errorf("body_ready must bind the exact body checkpoint")
		}
	case ChapterRenderPhaseCommitted:
		if evidence.CommitCheckpointSeq <= 0 || evidence.CommitDigest != identity.BodySHA256 {
			return fmt.Errorf("committed must bind the exact body commit checkpoint")
		}
		if err := requireDigest("candidate_root", evidence.CandidateRoot); err != nil {
			return err
		}
	case ChapterRenderPhaseStructurallyBlocked:
		if strings.TrimSpace(evidence.Reason) == "" {
			return fmt.Errorf("structurally_blocked requires a reason")
		}
	case ChapterRenderPhaseFormalAccepted:
		if strings.TrimSpace(evidence.ReviewVerdict) != "accept" || strings.TrimSpace(evidence.ReviewDisposition) == "" || len(evidence.ReviewArtifacts) == 0 {
			return fmt.Errorf("formal_accepted requires accept verdict, disposition and exact artifacts")
		}
	case ChapterRenderPhaseFormalRejected:
		if verdict := strings.TrimSpace(evidence.ReviewVerdict); verdict == "" || verdict == "accept" || len(evidence.ReviewArtifacts) == 0 {
			return fmt.Errorf("formal_rejected requires a non-accept verdict and exact artifacts")
		}
	case ChapterRenderPhaseActualMatched:
		return requireDigest("actual_match_digest", evidence.ActualMatchDigest)
	case ChapterRenderPhasePublished:
		if strings.TrimSpace(evidence.DirectoryPublishID) == "" {
			return fmt.Errorf("published requires directory_publish_id")
		}
		return requireDigest("directory_publish_digest", evidence.DirectoryPublishDigest)
	case ChapterRenderPhaseOutcomeAccepted:
		return requireDigest("outcome_receipt_digest", evidence.OutcomeReceiptDigest)
	case ChapterRenderPhaseChapterAccepted:
		return requireDigest("chapter_acceptance_digest", evidence.ChapterAcceptanceDigest)
	case ChapterRenderPhaseCompleted:
		return requireDigest("render_receipt_digest", evidence.RenderReceiptDigest)
	default:
		return fmt.Errorf("unsupported phase %q", phase)
	}
	return nil
}
