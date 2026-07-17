package domain

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

const (
	OutlineAllExecutionReceiptVersion = 1
	OutlineAllExecutionMode           = "sealed_full_book_outline_v1"
	OutlineAllIntentMarker            = "OUTLINE_ALL_INTENT "

	OutlineAllExecutionBuilding = "building"
	OutlineAllExecutionComplete = "complete"
)

// FormatOutlineAllIntent emits the host-controlled, single-line action marker
// consumed by the Architect dispatch gate. Natural-language task text is not
// accepted as execution authority.
func FormatOutlineAllIntent(action OutlineAllPendingAction) (string, error) {
	if err := ValidateOutlineAllPendingAction(action); err != nil {
		return "", err
	}
	raw, err := json.Marshal(action)
	if err != nil {
		return "", err
	}
	return OutlineAllIntentMarker + string(raw), nil
}

func ParseOutlineAllIntent(task string) (*OutlineAllPendingAction, error) {
	var result *OutlineAllPendingAction
	for _, line := range strings.Split(strings.ReplaceAll(task, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, OutlineAllIntentMarker) {
			continue
		}
		if result != nil {
			return nil, fmt.Errorf("outline-all task contains multiple intent markers")
		}
		var action OutlineAllPendingAction
		if err := json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, OutlineAllIntentMarker))), &action); err != nil {
			return nil, fmt.Errorf("parse outline-all intent: %w", err)
		}
		if err := ValidateOutlineAllPendingAction(action); err != nil {
			return nil, err
		}
		result = &action
	}
	if result == nil {
		return nil, fmt.Errorf("outline-all task is missing its intent marker")
	}
	return result, nil
}

func OutlineAllPendingActionEqual(a, b OutlineAllPendingAction) bool {
	return a == b
}

type OutlineAllActionType string

const (
	OutlineAllActionAppendVolume OutlineAllActionType = "append_volume"
	OutlineAllActionMapContracts OutlineAllActionType = "map_contracts"
	OutlineAllActionExpandArc    OutlineAllActionType = "expand_arc"
	OutlineAllActionReviseArc    OutlineAllActionType = "revise_arc"
)

// OutlineAllPendingAction is the single structural mutation authorized for
// the next resumable Architect invocation. A receipt never authorizes an
// open-ended foundation session.
type OutlineAllPendingAction struct {
	Type                OutlineAllActionType `json:"type"`
	Operation           int                  `json:"operation"`
	Volume              int                  `json:"volume,omitempty"`
	Arc                 int                  `json:"arc,omitempty"`
	ExpectedVolumeIndex int                  `json:"expected_volume_index,omitempty"`
	ExpectedChapterSpan int                  `json:"expected_chapter_span"`
	ExpectedArcSpans    string               `json:"expected_arc_spans,omitempty"`
	BeforeLayeredDigest string               `json:"before_layered_digest"`
	FinalSkeleton       bool                 `json:"final_skeleton,omitempty"`
}

type OutlineAllModelIdentity struct {
	CoordinatorProvider  string `json:"coordinator_provider"`
	CoordinatorModel     string `json:"coordinator_model"`
	CoordinatorReasoning string `json:"coordinator_reasoning"`
	ArchitectProvider    string `json:"architect_provider"`
	ArchitectModel       string `json:"architect_model"`
	ArchitectReasoning   string `json:"architect_reasoning"`
}

// OutlineAllExecutionReceipt is host-issued evidence for the exceptional
// chapter-zero planning window in which a full-book skeleton may be appended
// and expanded before rolling canon/world ticks exist. It is deliberately
// bound to the exact active OutlineAll lease and the exact compass/writing
// mode receipts, so copying it into another run grants no capability.
type OutlineAllExecutionReceipt struct {
	Version                       int                      `json:"version"`
	Mode                          string                   `json:"mode"`
	Status                        string                   `json:"status"`
	BaseCanonChapter              int                      `json:"base_canon_chapter"`
	GenerationID                  string                   `json:"generation_id,omitempty"`
	WritingMode                   string                   `json:"writing_mode"`
	WritingModeReceiptDigest      string                   `json:"writing_mode_receipt_digest"`
	CompassDigest                 string                   `json:"compass_digest"`
	EstimatedScale                string                   `json:"estimated_scale"`
	EndingDirection               string                   `json:"ending_direction"`
	NonNegotiables                []string                 `json:"non_negotiables,omitempty"`
	MinVolumes                    int                      `json:"min_volumes"`
	MaxVolumes                    int                      `json:"max_volumes"`
	MinChapters                   int                      `json:"min_chapters"`
	MaxChapters                   int                      `json:"max_chapters"`
	TargetVolumes                 int                      `json:"target_volumes"`
	TargetChapters                int                      `json:"target_chapters"`
	TargetWords                   int                      `json:"target_words"`
	TargetWordsPerChapter         int                      `json:"target_words_per_chapter"`
	StoryTimeHint                 string                   `json:"story_time_hint"`
	SourceSnapshotRoot            string                   `json:"source_snapshot_root"`
	ProtectedCanonRoot            string                   `json:"protected_canon_root"`
	StableProgressRoot            string                   `json:"stable_progress_root"`
	FoundationContextRoot         string                   `json:"foundation_context_root"`
	AttemptID                     string                   `json:"attempt_id"`
	CandidateDir                  string                   `json:"candidate_dir"`
	CoordinatorProvider           string                   `json:"coordinator_provider"`
	CoordinatorModel              string                   `json:"coordinator_model"`
	CoordinatorReasoning          string                   `json:"coordinator_reasoning"`
	ArchitectProvider             string                   `json:"architect_provider"`
	ArchitectModel                string                   `json:"architect_model"`
	ArchitectReasoning            string                   `json:"architect_reasoning"`
	ModelIdentityDigest           string                   `json:"model_identity_digest"`
	PromptProtocolDigest          string                   `json:"prompt_protocol_digest"`
	FinalLayeredDigest            string                   `json:"final_layered_digest,omitempty"`
	FinalFlatDigest               string                   `json:"final_flat_digest,omitempty"`
	ArchitectReadinessJSONDigest  string                   `json:"architect_readiness_json_digest,omitempty"`
	ArchitectReadinessMDDigest    string                   `json:"architect_readiness_md_digest,omitempty"`
	ExpectedLiveDirectoryRoot     string                   `json:"expected_live_directory_root,omitempty"`
	PublishedCandidateRoot        string                   `json:"published_candidate_root,omitempty"`
	DirectoryPublishReceiptDigest string                   `json:"directory_publish_receipt_digest,omitempty"`
	LockVersion                   int                      `json:"lock_version"`
	LockMode                      PipelineExecutionMode    `json:"lock_mode"`
	LockTargetChapter             int                      `json:"lock_target_chapter"`
	LockPlanDigest                string                   `json:"lock_plan_digest,omitempty"`
	LockOwner                     string                   `json:"lock_owner"`
	LockProcessID                 int                      `json:"lock_process_id"`
	LockAcquiredAt                time.Time                `json:"lock_acquired_at"`
	LockExpiresAt                 time.Time                `json:"lock_expires_at"`
	PendingAction                 *OutlineAllPendingAction `json:"pending_action,omitempty"`
	CompletedActionCount          int                      `json:"completed_action_count,omitempty"`
	StartedAt                     time.Time                `json:"started_at"`
	UpdatedAt                     time.Time                `json:"updated_at"`
	ReceiptDigest                 string                   `json:"receipt_digest"`
}

func (receipt OutlineAllExecutionReceipt) ModelIdentity() OutlineAllModelIdentity {
	return OutlineAllModelIdentity{
		CoordinatorProvider:  receipt.CoordinatorProvider,
		CoordinatorModel:     receipt.CoordinatorModel,
		CoordinatorReasoning: receipt.CoordinatorReasoning,
		ArchitectProvider:    receipt.ArchitectProvider,
		ArchitectModel:       receipt.ArchitectModel,
		ArchitectReasoning:   receipt.ArchitectReasoning,
	}
}

func ComputeOutlineAllModelIdentityDigest(identity OutlineAllModelIdentity) (string, error) {
	return planningV2Digest(identity)
}

func ComputeOutlineAllPromptProtocolDigest(protocol string) (string, error) {
	protocol = strings.TrimSpace(protocol)
	if protocol == "" {
		return "", fmt.Errorf("outline-all prompt protocol is empty")
	}
	return planningV2Digest(protocol)
}

// ComputeStoryCompassDigest gives outline-all a stable binding to every
// compass field, including estimated_scale and non_negotiables.
func ComputeStoryCompassDigest(compass StoryCompass) (string, error) {
	return planningV2Digest(compass)
}

func ComputeLayeredOutlineDigest(volumes []VolumeOutline) (string, error) {
	return planningV2Digest(volumes)
}

func ComputeFlatOutlineDigest(entries []OutlineEntry) (string, error) {
	return planningV2Digest(entries)
}

func ComputeOutlineAllExecutionReceiptDigest(
	receipt OutlineAllExecutionReceipt,
) (string, error) {
	receipt.ReceiptDigest = ""
	raw, err := json.Marshal(receipt)
	if err != nil {
		return "", err
	}
	return planningV2Digest(json.RawMessage(raw))
}

// SignOutlineAllExecutionReceipt recomputes the content-addressed receipt
// digest after a host-side rebind or checkpoint update.
func SignOutlineAllExecutionReceipt(
	receipt OutlineAllExecutionReceipt,
) (OutlineAllExecutionReceipt, error) {
	digest, err := ComputeOutlineAllExecutionReceiptDigest(receipt)
	if err != nil {
		return receipt, err
	}
	receipt.ReceiptDigest = digest
	if err := ValidateOutlineAllExecutionReceipt(receipt); err != nil {
		return receipt, err
	}
	return receipt, nil
}

// BindOutlineAllExecutionLock binds a receipt to every persisted field of the
// active OutlineAll lease. A recovered run must explicitly rebind and resign
// the receipt after acquiring its own lease.
func BindOutlineAllExecutionLock(
	receipt *OutlineAllExecutionReceipt,
	lock PipelineExecutionLock,
) error {
	if receipt == nil {
		return fmt.Errorf("outline-all execution receipt is nil")
	}
	if lock.Mode != PipelineExecutionOutlineAll ||
		lock.TargetChapter <= 0 ||
		strings.TrimSpace(lock.Owner) == "" ||
		lock.ProcessID <= 0 ||
		lock.AcquiredAt.IsZero() ||
		lock.ExpiresAt.IsZero() ||
		!lock.ExpiresAt.After(lock.AcquiredAt) {
		return fmt.Errorf("outline-all requires a valid dedicated execution lock")
	}
	receipt.LockVersion = lock.Version
	receipt.LockMode = lock.Mode
	receipt.LockTargetChapter = lock.TargetChapter
	receipt.LockPlanDigest = lock.PlanDigest
	receipt.LockOwner = lock.Owner
	receipt.LockProcessID = lock.ProcessID
	receipt.LockAcquiredAt = lock.AcquiredAt
	receipt.LockExpiresAt = lock.ExpiresAt
	return nil
}

func ValidateOutlineAllExecutionLockBinding(
	receipt OutlineAllExecutionReceipt,
	lock PipelineExecutionLock,
) error {
	if receipt.LockVersion != lock.Version ||
		receipt.LockMode != lock.Mode ||
		receipt.LockTargetChapter != lock.TargetChapter ||
		receipt.LockPlanDigest != lock.PlanDigest ||
		receipt.LockOwner != lock.Owner ||
		receipt.LockProcessID != lock.ProcessID ||
		!receipt.LockAcquiredAt.Equal(lock.AcquiredAt) ||
		!receipt.LockExpiresAt.Equal(lock.ExpiresAt) {
		return fmt.Errorf("outline-all execution receipt does not match the active dedicated lock")
	}
	return nil
}

func ValidateOutlineAllExecutionReceipt(receipt OutlineAllExecutionReceipt) error {
	if receipt.Version != OutlineAllExecutionReceiptVersion ||
		receipt.Mode != OutlineAllExecutionMode ||
		receipt.BaseCanonChapter != 0 ||
		receipt.WritingMode != WritingPipelineModeSealedTwoPassV2 {
		return fmt.Errorf("outline-all execution receipt has invalid identity")
	}
	if receipt.Status != OutlineAllExecutionBuilding &&
		receipt.Status != OutlineAllExecutionComplete {
		return fmt.Errorf("outline-all execution receipt has invalid status %q", receipt.Status)
	}
	if err := validatePlanningV2Digest("writing_mode_receipt_digest", receipt.WritingModeReceiptDigest); err != nil {
		return err
	}
	if err := validatePlanningV2Digest("compass_digest", receipt.CompassDigest); err != nil {
		return err
	}
	if strings.TrimSpace(receipt.EstimatedScale) == "" ||
		strings.TrimSpace(receipt.EndingDirection) == "" {
		return fmt.Errorf("outline-all execution receipt requires estimated_scale and ending_direction")
	}
	if receipt.MinVolumes <= 0 || receipt.MaxVolumes < receipt.MinVolumes ||
		receipt.MinChapters <= 0 || receipt.MaxChapters < receipt.MinChapters {
		return fmt.Errorf("outline-all execution receipt has invalid scale bounds")
	}
	if receipt.TargetVolumes < receipt.MinVolumes || receipt.TargetVolumes > receipt.MaxVolumes ||
		receipt.TargetChapters < receipt.MinChapters || receipt.TargetChapters > receipt.MaxChapters {
		return fmt.Errorf("outline-all execution receipt has invalid deterministic targets")
	}
	if (receipt.TargetWords == 0) != (receipt.TargetWordsPerChapter == 0) ||
		receipt.TargetWords < 0 || receipt.TargetWordsPerChapter < 0 {
		return fmt.Errorf("outline-all optional word target must be absent or complete")
	}
	for name, digest := range map[string]string{
		"source_snapshot_root":    receipt.SourceSnapshotRoot,
		"protected_canon_root":    receipt.ProtectedCanonRoot,
		"stable_progress_root":    receipt.StableProgressRoot,
		"foundation_context_root": receipt.FoundationContextRoot,
		"model_identity_digest":   receipt.ModelIdentityDigest,
		"prompt_protocol_digest":  receipt.PromptProtocolDigest,
	} {
		if err := validatePlanningV2Digest(name, digest); err != nil {
			return err
		}
	}
	if strings.TrimSpace(receipt.AttemptID) == "" ||
		strings.TrimSpace(receipt.CandidateDir) == "" ||
		!filepath.IsAbs(receipt.CandidateDir) {
		return fmt.Errorf("outline-all execution receipt requires attempt_id and an absolute candidate_dir")
	}
	identity := receipt.ModelIdentity()
	if strings.TrimSpace(identity.CoordinatorProvider) == "" ||
		strings.TrimSpace(identity.CoordinatorModel) == "" ||
		strings.TrimSpace(identity.CoordinatorReasoning) == "" ||
		strings.TrimSpace(identity.ArchitectProvider) == "" ||
		strings.TrimSpace(identity.ArchitectModel) == "" ||
		strings.TrimSpace(identity.ArchitectReasoning) == "" {
		return fmt.Errorf("outline-all execution receipt requires complete coordinator/architect model identity")
	}
	wantModelDigest, err := ComputeOutlineAllModelIdentityDigest(identity)
	if err != nil {
		return err
	}
	if receipt.ModelIdentityDigest != wantModelDigest {
		return fmt.Errorf("outline-all model identity digest mismatch")
	}
	if len(receipt.NonNegotiables) == 0 {
		return fmt.Errorf("outline-all execution receipt requires at least one terminal non_negotiable")
	}
	seenContracts := make(map[string]struct{}, len(receipt.NonNegotiables))
	for _, contract := range receipt.NonNegotiables {
		contract = strings.TrimSpace(contract)
		if contract == "" {
			return fmt.Errorf("outline-all execution receipt has an empty non_negotiable")
		}
		if _, exists := seenContracts[contract]; exists {
			return fmt.Errorf("outline-all execution receipt has duplicate non_negotiable %q", contract)
		}
		seenContracts[contract] = struct{}{}
	}
	if receipt.LockVersion <= 0 ||
		receipt.LockMode != PipelineExecutionOutlineAll ||
		receipt.LockTargetChapter <= 0 ||
		strings.TrimSpace(receipt.LockOwner) == "" ||
		receipt.LockProcessID <= 0 ||
		receipt.LockAcquiredAt.IsZero() ||
		receipt.LockExpiresAt.IsZero() ||
		!receipt.LockExpiresAt.After(receipt.LockAcquiredAt) {
		return fmt.Errorf("outline-all execution receipt has invalid dedicated lock binding")
	}
	if receipt.CompletedActionCount < 0 ||
		receipt.StartedAt.IsZero() ||
		receipt.UpdatedAt.IsZero() ||
		receipt.UpdatedAt.Before(receipt.StartedAt) {
		return fmt.Errorf("outline-all execution receipt has invalid checkpoint metadata")
	}
	if receipt.Status == OutlineAllExecutionComplete && receipt.PendingAction != nil {
		return fmt.Errorf("completed outline-all execution cannot retain a pending action")
	}
	if receipt.Status == OutlineAllExecutionComplete {
		if err := validatePlanningV2Digest("final_layered_digest", receipt.FinalLayeredDigest); err != nil {
			return err
		}
		if err := validatePlanningV2Digest("final_flat_digest", receipt.FinalFlatDigest); err != nil {
			return err
		}
		if err := validatePlanningV2Digest("architect_readiness_json_digest", receipt.ArchitectReadinessJSONDigest); err != nil {
			return err
		}
		if err := validatePlanningV2Digest("architect_readiness_md_digest", receipt.ArchitectReadinessMDDigest); err != nil {
			return err
		}
	} else {
		for name, digest := range map[string]string{
			"final_layered_digest":            receipt.FinalLayeredDigest,
			"final_flat_digest":               receipt.FinalFlatDigest,
			"architect_readiness_json_digest": receipt.ArchitectReadinessJSONDigest,
			"architect_readiness_md_digest":   receipt.ArchitectReadinessMDDigest,
			"expected_live_directory_root":    receipt.ExpectedLiveDirectoryRoot,
		} {
			if digest != "" {
				if err := validatePlanningV2Digest(name, digest); err != nil {
					return err
				}
			}
		}
	}
	if receipt.ExpectedLiveDirectoryRoot != "" {
		if err := validatePlanningV2Digest("expected_live_directory_root", receipt.ExpectedLiveDirectoryRoot); err != nil {
			return err
		}
	}
	if (receipt.PublishedCandidateRoot == "") != (receipt.DirectoryPublishReceiptDigest == "") {
		return fmt.Errorf("outline-all publish roots must be recorded together")
	}
	if receipt.PublishedCandidateRoot != "" {
		if err := validatePlanningV2Digest("published_candidate_root", receipt.PublishedCandidateRoot); err != nil {
			return err
		}
		if err := validatePlanningV2Digest("directory_publish_receipt_digest", receipt.DirectoryPublishReceiptDigest); err != nil {
			return err
		}
	}
	if receipt.PendingAction != nil {
		if err := ValidateOutlineAllPendingAction(*receipt.PendingAction); err != nil {
			return err
		}
	}
	if err := validatePlanningV2Digest("receipt_digest", receipt.ReceiptDigest); err != nil {
		return err
	}
	want, err := ComputeOutlineAllExecutionReceiptDigest(receipt)
	if err != nil {
		return err
	}
	if receipt.ReceiptDigest != want {
		return fmt.Errorf("outline-all execution receipt digest mismatch")
	}
	return nil
}

func ValidateOutlineAllPendingAction(action OutlineAllPendingAction) error {
	if action.Operation <= 0 {
		return fmt.Errorf("outline-all pending action requires a positive operation number")
	}
	if err := validatePlanningV2Digest("before_layered_digest", action.BeforeLayeredDigest); err != nil {
		return err
	}
	switch action.Type {
	case OutlineAllActionAppendVolume:
		if action.Volume <= 0 || action.ExpectedVolumeIndex <= 0 ||
			action.Volume != action.ExpectedVolumeIndex || action.Arc != 0 ||
			action.ExpectedChapterSpan <= 0 {
			return fmt.Errorf("append_volume pending action requires the exact next volume and no arc")
		}
		spans, err := RecommendedOutlineAllArcSpans(action.ExpectedChapterSpan)
		if err != nil {
			return err
		}
		if action.ExpectedArcSpans != FormatOutlineAllArcSpans(spans) {
			return fmt.Errorf("append_volume pending action expected_arc_spans=%q want %q", action.ExpectedArcSpans, FormatOutlineAllArcSpans(spans))
		}
	case OutlineAllActionMapContracts:
		if action.Volume != 0 || action.Arc != 0 || action.ExpectedVolumeIndex != 0 ||
			action.ExpectedChapterSpan <= 0 || action.ExpectedArcSpans != "" || !action.FinalSkeleton {
			return fmt.Errorf("map_contracts pending action requires the frozen full-book span")
		}
	case OutlineAllActionExpandArc, OutlineAllActionReviseArc:
		if action.Volume <= 0 || action.Arc <= 0 ||
			action.ExpectedVolumeIndex != 0 || action.ExpectedChapterSpan <= 0 || action.ExpectedArcSpans != "" || action.FinalSkeleton {
			return fmt.Errorf("%s pending action requires an exact volume/arc", action.Type)
		}
	default:
		return fmt.Errorf("unsupported outline-all pending action %q", action.Type)
	}
	return nil
}

// ValidateOutlineAllChapterZeroProgress proves that the active generation has
// no progress-visible canon yet. The store-level workspace guard separately
// requires the live/candidate tree to contain no chapter, draft, or formal-plan
// artifacts; progress alone is never treated as sufficient evidence.
func ValidateOutlineAllChapterZeroProgress(
	progress *Progress,
	receipt OutlineAllExecutionReceipt,
) error {
	if progress == nil {
		return fmt.Errorf("outline-all requires initialized progress")
	}
	if progress.GenerationID != receipt.GenerationID {
		return fmt.Errorf("outline-all execution receipt generation_id does not match progress")
	}
	if progress.CurrentChapter != 0 ||
		progress.InProgressChapter != 0 ||
		progress.LatestCompleted() != 0 ||
		len(progress.CompletedChapters) != 0 ||
		len(progress.CompletedScenes) != 0 ||
		progress.TotalWordCount != 0 ||
		len(progress.PendingRewrites) != 0 ||
		progress.ReopenedFromComplete ||
		progress.Phase == PhaseComplete {
		return fmt.Errorf("outline-all is authorized only before chapter 1 canon begins")
	}
	for chapter, count := range progress.ChapterWordCounts {
		if count != 0 {
			return fmt.Errorf("outline-all found canon word count for chapter %d", chapter)
		}
	}
	return nil
}
