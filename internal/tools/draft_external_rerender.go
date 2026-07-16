package tools

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/aigc"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/errs"
	"github.com/chenhongyang/novel-studio/internal/reviewreport"
	"github.com/chenhongyang/novel-studio/internal/store"
)

const draftExternalEvaluatorRegistered = "registered_external_detector"

const draftRerenderAuthorizationSource = "render_only_authorization"

// DraftExternalRerenderRequirement is written by the external draft judge when
// a whole-chapter result is blocking. It prevents a structural diagnosis from
// being "fixed" through a chain of tiny string replacements.
type DraftExternalRerenderRequirement struct {
	Chapter                 int                           `json:"chapter"`
	EvaluatedBodySHA256     string                        `json:"evaluated_body_sha256"`
	Source                  string                        `json:"source,omitempty"`
	Evaluator               string                        `json:"evaluator,omitempty"`
	RequiredDetector        string                        `json:"required_detector,omitempty"`
	RequiredMode            string                        `json:"required_mode,omitempty"`
	RequiredExternalRetests []DraftExternalRetestIdentity `json:"required_external_retests,omitempty"`
	InitialDraftBodySHA256  string                        `json:"initial_draft_body_sha256,omitempty"`
	AIProbabilityPercent    int                           `json:"ai_probability_percent"`
	PassExclusivePercent    int                           `json:"pass_exclusive_percent"`
	Summary                 string                        `json:"summary,omitempty"`
	Evidence                []string                      `json:"evidence,omitempty"`
	RevisionPlan            []string                      `json:"revision_plan,omitempty"`
	AdviceComplete          bool                          `json:"advice_complete"`
}

// DraftExternalRetestIdentity is one durable named-platform obligation. More
// than one detector/mode may independently block the same body; a passing row
// only clears its own identity.
type DraftExternalRetestIdentity struct {
	Detector          string `json:"detector"`
	Mode              string `json:"mode"`
	TriggerBodySHA256 string `json:"trigger_body_sha256,omitempty"`
}

type DraftExternalGateStatus string

const (
	DraftExternalGateNotRequired        DraftExternalGateStatus = "not_required"
	DraftExternalGateApproved           DraftExternalGateStatus = "approved"
	DraftExternalGateRerenderAuthorized DraftExternalGateStatus = "rerender_authorized"
	DraftExternalGateRejudgePending     DraftExternalGateStatus = "rejudge_pending"
	DraftExternalGateAdviceIncomplete   DraftExternalGateStatus = "advice_incomplete"
)

type DraftExternalGateInspection struct {
	Status                   DraftExternalGateStatus
	CurrentBodySHA256        string
	FinalBodySHA256          string
	EvaluatedBodySHA256      string
	Requirement              *DraftExternalRerenderRequirement
	ArtifactExists           bool
	RegisteredArtifactExists bool
	RequiresRegisteredRetest bool
	RegisteredRetestDeferred bool
	// LocalSoftEditPending means the exact current hash has already passed the
	// independent DeepSeek judge, but the effective local gate still has a
	// non-whole-text failure. Keep Status=rejudge_pending so commit remains
	// fail-closed; edit_chapter may consume exactly one local repair before the
	// changed hash returns to the independent judge. Named-platform retests stay
	// deferred until both earlier stages pass.
	LocalSoftEditPending bool
	// CurrentHashNamedRetestsPassed is true only when Status=approved and every
	// registered detector/mode required by Requirement has a result bound to the
	// exact CurrentBodySHA256 whose normalized score is strictly below the fixed
	// 4% platform gate. Prose tools treat that payload as frozen; commit remains
	// allowed. A newer explicit full-rerender request or a new blocking result
	// moves routing out of this terminal prose state.
	CurrentHashNamedRetestsPassed bool
	RegisteredDetection           *reviewreport.RegisteredExternalDetection
}

type draftExternalJudgeStatus struct {
	BodySHA256           string   `json:"body_sha256"`
	Blocking             bool     `json:"blocking"`
	AdviceComplete       bool     `json:"advice_complete"`
	AIProbabilityPercent int      `json:"ai_probability_percent"`
	PassExclusivePercent int      `json:"pass_exclusive_percent"`
	Summary              string   `json:"summary,omitempty"`
	Evidence             []string `json:"evidence,omitempty"`
	RevisionPlan         []string `json:"revision_plan,omitempty"`
}

func draftExternalRerenderRequirementPath(projectDir string, chapter int) string {
	return filepath.Join(projectDir, "reviews", "drafts", fmt.Sprintf("%02d_full_rerender_required.json", chapter))
}

func SetDraftExternalRerenderRequirement(projectDir string, requirement DraftExternalRerenderRequirement) error {
	if requirement.Chapter <= 0 {
		return fmt.Errorf("chapter must be > 0")
	}
	// A named platform retest is a durable obligation across every subsequent
	// rewrite. DeepSeek or a local whole-text gate may add a newer structural
	// reason, but neither is allowed to downgrade the required detector/mode to
	// an independent-model-only marker.
	incomingRegistered := RequiresRegisteredExternalRetest(&requirement)
	if existing, err := loadDraftExternalRerenderRequirement(projectDir, requirement.Chapter); err != nil {
		return err
	} else if RequiresRegisteredExternalRetest(existing) {
		for _, identity := range registeredExternalRetestIdentities(existing) {
			requirement.RequiredExternalRetests = appendRegisteredExternalRetestIdentity(requirement.RequiredExternalRetests, identity)
		}
		if !incomingRegistered {
			requirement.RequiredDetector = existing.RequiredDetector
			requirement.RequiredMode = existing.RequiredMode
		}
		if requirement.InitialDraftBodySHA256 == "" {
			requirement.InitialDraftBodySHA256 = existing.InitialDraftBodySHA256
		}
		requirement.Evidence = append(requirement.Evidence,
			fmt.Sprintf("registered_external_retest_required:%s", strings.Join(RegisteredExternalRetestLabels(existing), ",")),
		)
	}
	path := draftExternalRerenderRequirementPath(projectDir, requirement.Chapter)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(requirement, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0o644)
}

func ClearDraftExternalRerenderRequirement(projectDir string, chapter int) error {
	err := os.Remove(draftExternalRerenderRequirementPath(projectDir, chapter))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// SetRenderOnlyRejudgeRequirement turns an explicit/review-driven one-shot
// render authorization into a durable pre-write contract. The marker is
// written before replacing prose: after a successful write (or saga recovery)
// its evaluated hash differs from the current draft, so Inspector returns
// rejudge_pending and commit cannot mistake the clean replacement for an
// unjudged but finalizable draft.
func SetRenderOnlyRejudgeRequirement(st *store.Store, chapter int, evaluatedBodySHA256 string) (*DraftExternalRerenderRequirement, error) {
	if st == nil || chapter <= 0 || !validExternalBodySHA256(evaluatedBodySHA256) {
		return nil, fmt.Errorf("render-only rejudge contract requires store, chapter and a 64-hex evaluated body SHA")
	}
	requirement := DraftExternalRerenderRequirement{
		Chapter:                chapter,
		EvaluatedBodySHA256:    strings.TrimSpace(evaluatedBodySHA256),
		InitialDraftBodySHA256: strings.TrimSpace(evaluatedBodySHA256),
		Source:                 draftRerenderAuthorizationSource,
		PassExclusivePercent:   int(aigc.PassExclusivePercent),
		Summary:                "显式整章重渲染或当前正式 review 已要求换稿；替换稿必须冻结新哈希并完成 provider-backed whole-draft 复判后才能提交。",
		RevisionPlan: []string{
			"复用已验证的因果计划完整重渲染，不得用 append 或局部同义替换消费授权。",
			"新哈希落盘后立即停笔，等待外层 pipeline 完成整章复判；复判前禁止 edit、check 或 commit。",
		},
		AdviceComplete: true,
	}
	if err := SetDraftExternalRerenderRequirement(st.Dir(), requirement); err != nil {
		return nil, err
	}
	return &requirement, nil
}

func loadDraftExternalRerenderRequirement(projectDir string, chapter int) (*DraftExternalRerenderRequirement, error) {
	path := draftExternalRerenderRequirementPath(projectDir, chapter)
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var requirement DraftExternalRerenderRequirement
	if err := json.Unmarshal(raw, &requirement); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if requirement.Chapter != chapter {
		return nil, fmt.Errorf("marker chapter=%d, want %d", requirement.Chapter, chapter)
	}
	return &requirement, nil
}

func loadDraftExternalJudgeStatus(projectDir string, chapter int) (*draftExternalJudgeStatus, error) {
	path := filepath.Join(projectDir, "reviews", "drafts", fmt.Sprintf("%02d_deepseek_ai_judge.json", chapter))
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var status draftExternalJudgeStatus
	if err := json.Unmarshal(raw, &status); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &status, nil
}

// pipelineManagedCurrentDraftNeedsDeepSeekJudge promotes only prose produced by
// the managed writing journal. Imported/legacy drafts without a draft/edit
// checkpoint keep their historical compatibility, while a pipeline-authored
// exact body can never treat a missing or stale DeepSeek artifact as
// NotRequired.
func pipelineManagedCurrentDraftNeedsDeepSeekJudge(st *store.Store, chapter int, bodySHA256 string) (bool, error) {
	if !pipelineWritingManaged(st) || st == nil || chapter <= 0 || strings.TrimSpace(bodySHA256) == "" {
		return false, nil
	}
	scope := domain.ChapterScope(chapter)
	hasBodyCheckpoint := false
	for _, cp := range st.Checkpoints.All() {
		if cp.Scope.Matches(scope) && (cp.Step == "draft" || cp.Step == "edit") {
			hasBodyCheckpoint = true
			break
		}
	}
	if !hasBodyCheckpoint {
		return false, nil
	}
	bodyCheckpoint, err := CurrentChapterBodyCheckpoint(st, chapter)
	if err != nil {
		return false, fmt.Errorf("validate managed current draft checkpoint: %w", err)
	}
	wantDigest := "sha256:" + strings.ToLower(strings.TrimSpace(bodySHA256))
	if bodyCheckpoint.Digest != wantDigest {
		return false, fmt.Errorf("managed current draft checkpoint digest=%s, want %s", bodyCheckpoint.Digest, wantDigest)
	}
	status, err := loadDraftExternalJudgeStatus(st.Dir(), chapter)
	if err != nil {
		return false, err
	}
	if status == nil || strings.TrimSpace(status.BodySHA256) != strings.TrimSpace(bodySHA256) {
		return true, nil
	}
	// The persisted threshold is historical/diagnostic metadata. Pipeline
	// approval is a fixed protocol boundary and must never be relaxed by a
	// legacy artifact that recorded (for example) 10%.
	return !status.AdviceComplete || status.Blocking ||
		float64(status.AIProbabilityPercent) >= aigc.PassExclusivePercent, nil
}

// InspectDraftExternalGate treats a blocking judgment as a single-use
// authorization. Callers that already own a Store should use
// InspectDraftExternalGateWithStore so crash recovery updates their live
// checkpoint cache as well as the append-only journal.
func InspectDraftExternalGate(projectDir string, chapter int) (DraftExternalGateInspection, error) {
	return InspectDraftExternalGateWithStore(store.NewStore(projectDir), chapter)
}

// InspectDraftExternalGateWithStore treats a blocking judgment as a single-use
// authorization. The judged hash may be replaced once; the replacement hash
// must then be judged before another prose write or commit is allowed.
//
// Recovery must append checkpoints through st. CheckpointStore reads use an
// in-memory mirror, so recovering through a second Store would leave the
// caller unable to observe the recovered draft epoch until process restart.
func InspectDraftExternalGateWithStore(st *store.Store, chapter int) (DraftExternalGateInspection, error) {
	inspection := DraftExternalGateInspection{Status: DraftExternalGateNotRequired}
	if st == nil {
		return inspection, fmt.Errorf("inspect draft external gate: nil store")
	}
	projectDir := st.Dir()
	// Recovery is part of inspection because pipeline startup asks for gate state
	// before it can know whether to dispatch a writer or finalizer. Waiting until
	// the next prose write would strand a clean crash-written candidate without
	// its draft checkpoint, or even cause it to be overwritten after a valid
	// current-hash retest. The intent makes this mutation exact and idempotent.
	if _, statErr := os.Stat(draftWriteIntentPath(projectDir, chapter)); statErr == nil {
		if err := NewDraftChapterTool(st).recoverDraftWriteIntent(chapter); err != nil {
			return inspection, fmt.Errorf("recover pending draft write intent: %w", err)
		}
	} else if !os.IsNotExist(statErr) {
		return inspection, fmt.Errorf("inspect draft write intent: %w", statErr)
	}
	draftPath := filepath.Join(projectDir, "drafts", fmt.Sprintf("%02d.draft.md", chapter))
	finalPath := filepath.Join(projectDir, "chapters", fmt.Sprintf("%02d.md", chapter))
	if finalRaw, finalErr := os.ReadFile(finalPath); finalErr == nil {
		inspection.FinalBodySHA256 = reviewreport.BodySHA256(string(finalRaw))
	} else if !os.IsNotExist(finalErr) {
		return inspection, finalErr
	}
	if raw, err := os.ReadFile(draftPath); err == nil {
		inspection.CurrentBodySHA256 = reviewreport.BodySHA256(string(raw))
	} else if !os.IsNotExist(err) {
		return inspection, err
	} else if inspection.FinalBodySHA256 != "" {
		// Imported and historical projects may have a committed chapter but no
		// retained draft. Use the final bytes only to authorize creating the first
		// replacement draft; commit still requires an actual drafts/NN.draft.md.
		inspection.CurrentBodySHA256 = inspection.FinalBodySHA256
	}

	requirement, err := loadDraftExternalRerenderRequirement(projectDir, chapter)
	if err != nil {
		return inspection, err
	}
	// A retained draft may predate the committed chapter that was actually sent
	// to a platform. Inspect the formal final SHA as well as the draft SHA so a
	// stale drafts/NN.draft.md cannot hide a registered blocker when the marker
	// has not yet been materialized (or was lost before the first rerender).
	if inspection.FinalBodySHA256 != "" && inspection.FinalBodySHA256 != inspection.CurrentBodySHA256 {
		finalRows, finalRowsErr := reviewreport.LatestRegisteredExternalDetections(projectDir, chapter, inspection.FinalBodySHA256)
		if finalRowsErr != nil {
			return inspection, finalRowsErr
		}
		if len(finalRows) > 0 {
			inspection.RegisteredArtifactExists = true
		}
		for i := range finalRows {
			registered := finalRows[i]
			if registered.NormalizedScorePercent < aigc.PassExclusivePercent {
				continue
			}
			inspection.RegisteredDetection = &registered
			requirement = registeredExternalRerenderRequirement(registered, requirement)
			if strings.TrimSpace(requirement.InitialDraftBodySHA256) == "" {
				requirement.InitialDraftBodySHA256 = inspection.CurrentBodySHA256
			}
			inspection.EvaluatedBodySHA256 = inspection.FinalBodySHA256
		}
	}
	registeredRows, err := reviewreport.LatestRegisteredExternalDetections(projectDir, chapter, inspection.CurrentBodySHA256)
	if err != nil {
		return inspection, err
	}
	if len(registeredRows) > 0 {
		inspection.RegisteredArtifactExists = true
	}
	currentRegisteredHigh := false
	for i := range registeredRows {
		registered := registeredRows[i]
		if registered.NormalizedScorePercent < aigc.PassExclusivePercent {
			continue
		}
		// A current-hash platform blocker always upgrades an existing DeepSeek or
		// local marker to a named detector contract. Otherwise the next rewrite
		// could pass only DeepSeek and silently lose the platform retest.
		inspection.RegisteredDetection = &registered
		requirement = registeredExternalRerenderRequirement(registered, requirement)
		inspection.Requirement = requirement
		inspection.EvaluatedBodySHA256 = inspection.CurrentBodySHA256
		inspection.Status = DraftExternalGateRerenderAuthorized
		currentRegisteredHigh = true
	}
	inspection.Requirement = requirement
	if currentRegisteredHigh {
		return inspection, nil
	}
	requiresRegisteredRetest := RequiresRegisteredExternalRetest(requirement)
	if requirement != nil {
		inspection.EvaluatedBodySHA256 = strings.TrimSpace(requirement.EvaluatedBodySHA256)
		if !requirement.AdviceComplete || len(requirement.RevisionPlan) == 0 {
			inspection.Status = DraftExternalGateAdviceIncomplete
			inspection.RegisteredRetestDeferred = requiresRegisteredRetest
			return inspection, nil
		}
		// A blocking marker bound to the current bytes is itself the one-shot
		// replacement authorization. Registered identities are carried to the next
		// hash; they are not spent on bytes already known to be blocking. The one
		// exception is a registered-platform marker whose same detector/mode later
		// appended a passing result for these exact bytes: that newer event clears
		// the old platform blocker and lets the remaining stages decide the hash.
		registeredMarkerCleared := (requirement.Source == "registered_external_detection" || requirement.Evaluator == draftExternalEvaluatorRegistered) &&
			registeredExternalRetestsStrictlyPassedInRows(requirement, registeredRows)
		if inspection.CurrentBodySHA256 != "" && inspection.CurrentBodySHA256 == inspection.EvaluatedBodySHA256 {
			if !registeredMarkerCleared {
				inspection.Status = DraftExternalGateRerenderAuthorized
				inspection.RegisteredRetestDeferred = requiresRegisteredRetest
				return inspection, nil
			}
		}
		if requiresRegisteredRetest {
			// Stage 1: never spend a named-platform retest on an intermediate hash
			// that the deterministic whole-text/segment gate still rejects. The
			// exact local marker authorizes one bounded full render while every
			// detector identity remains durable for the eventual candidate.
			if localRequirement, blocked := currentDraftLocalStructuralRerenderRequirement(st, chapter, requirement); blocked {
				inspection.Requirement = localRequirement
				inspection.EvaluatedBodySHA256 = inspection.CurrentBodySHA256
				inspection.Status = DraftExternalGateRerenderAuthorized
				inspection.RegisteredRetestDeferred = true
				return inspection, nil
			}
			if initialDraft := strings.TrimSpace(requirement.InitialDraftBodySHA256); initialDraft != "" &&
				inspection.CurrentBodySHA256 == initialDraft && inspection.CurrentBodySHA256 != inspection.EvaluatedBodySHA256 &&
				inspection.FinalBodySHA256 == inspection.EvaluatedBodySHA256 {
				inspection.Status = DraftExternalGateRerenderAuthorized
				inspection.RegisteredRetestDeferred = true
				return inspection, nil
			}
		}
	}

	// Stage 2: the independent current-hash DeepSeek judgment always precedes a
	// named-platform retest. A stale or missing artifact keeps every registered
	// identity deferred; it must never make the pipeline ask a human-operated
	// detector to score bytes that DeepSeek may immediately reject.
	status, err := loadDraftExternalJudgeStatus(projectDir, chapter)
	if err != nil {
		return inspection, err
	}
	if status == nil {
		managedPending, pendingErr := pipelineManagedCurrentDraftNeedsDeepSeekJudge(st, chapter, inspection.CurrentBodySHA256)
		if pendingErr != nil {
			return inspection, pendingErr
		}
		if requirement != nil || managedPending {
			inspection.Status = DraftExternalGateRejudgePending
		}
		inspection.RegisteredRetestDeferred = requiresRegisteredRetest
		return inspection, nil
	}
	inspection.ArtifactExists = true
	inspection.EvaluatedBodySHA256 = strings.TrimSpace(status.BodySHA256)
	if inspection.CurrentBodySHA256 == "" || inspection.CurrentBodySHA256 != inspection.EvaluatedBodySHA256 {
		inspection.Status = DraftExternalGateRejudgePending
		inspection.RegisteredRetestDeferred = requiresRegisteredRetest
		return inspection, nil
	}
	if !status.AdviceComplete {
		inspection.Status = DraftExternalGateAdviceIncomplete
		inspection.RegisteredRetestDeferred = requiresRegisteredRetest
		return inspection, nil
	}
	if status.Blocking || float64(status.AIProbabilityPercent) >= aigc.PassExclusivePercent {
		if requiresRegisteredRetest {
			clone := *requirement
			clone.EvaluatedBodySHA256 = inspection.CurrentBodySHA256
			clone.Source = "deepseek_ai_judge"
			clone.Evaluator = "deepseek"
			clone.AIProbabilityPercent = status.AIProbabilityPercent
			clone.Summary = status.Summary
			clone.Evidence = append([]string(nil), status.Evidence...)
			clone.RevisionPlan = append([]string(nil), status.RevisionPlan...)
			clone.AdviceComplete = status.AdviceComplete
			inspection.Requirement = &clone
			if clone.AdviceComplete && len(clone.RevisionPlan) > 0 {
				inspection.Status = DraftExternalGateRerenderAuthorized
			} else {
				inspection.Status = DraftExternalGateAdviceIncomplete
			}
			inspection.RegisteredRetestDeferred = true
			return inspection, nil
		}
		// A crash may leave the result but not its marker. Re-running the cached
		// judge restores the single-use authorization without regenerating prose.
		inspection.Status = DraftExternalGateRejudgePending
		return inspection, nil
	}
	if requiresRegisteredRetest {
		// DeepSeek passed the exact current hash. Re-run the effective local gate
		// with that corroboration before scheduling a named detector. Structural
		// failures still consume the bounded full-render budget; a remaining soft
		// failure permits one hash-changing edit while commit stays fail-closed.
		content, loadErr := st.Drafts.LoadDraft(chapter)
		if loadErr != nil {
			return inspection, loadErr
		}
		report, gate := inspectDraftAIGCGate(st, chapter, content)
		localStructural, localSoft := draftExternalLocalGateDisposition(content, report, gate)
		if localStructural {
			if localRequirement, blocked := currentDraftLocalStructuralRerenderRequirement(st, chapter, requirement); blocked {
				inspection.Requirement = localRequirement
				inspection.EvaluatedBodySHA256 = inspection.CurrentBodySHA256
				inspection.Status = DraftExternalGateRerenderAuthorized
				inspection.RegisteredRetestDeferred = true
				return inspection, nil
			}
		}
		if localSoft {
			inspection.Status = DraftExternalGateRejudgePending
			inspection.LocalSoftEditPending = true
			inspection.RegisteredRetestDeferred = true
			return inspection, nil
		}

		// Stage 3: only a current hash that passed both the deterministic local
		// gate and DeepSeek may consume the named-platform retest obligation.
		missingRegistered := false
		for _, identity := range registeredExternalRetestIdentities(requirement) {
			registered, registeredErr := reviewreport.LatestRegisteredExternalDetection(
				projectDir, chapter, inspection.CurrentBodySHA256,
				identity.Detector, identity.Mode,
			)
			if registeredErr != nil {
				return inspection, registeredErr
			}
			if registered == nil {
				missingRegistered = true
				continue
			}
			inspection.RegisteredDetection = registered
			inspection.RegisteredArtifactExists = true
			if registered.NormalizedScorePercent >= aigc.PassExclusivePercent {
				inspection.Status = DraftExternalGateRerenderAuthorized
				inspection.Requirement = registeredExternalRerenderRequirement(*registered, requirement)
				inspection.EvaluatedBodySHA256 = inspection.CurrentBodySHA256
				return inspection, nil
			}
		}
		if missingRegistered {
			inspection.Status = DraftExternalGateRejudgePending
			inspection.RequiresRegisteredRetest = true
			return inspection, nil
		}
		inspection.CurrentHashNamedRetestsPassed = registeredExternalRetestsStrictlyPassedInRows(requirement, registeredRows)
	}
	inspection.Status = DraftExternalGateApproved
	return inspection, nil
}

func draftExternalLocalGateDisposition(content string, report aigc.Report, gate draftAIGCGateResult) (structural, soft bool) {
	rawGate := draftAIGCRawLocalGateResult(report, gate)
	if rawGate.Passed {
		return false, false
	}
	structural = draftAIGCHasWholeTextStructuralBlock(content, report, rawGate)
	return structural, rawGate.Enforced && !structural
}

// RequiresRegisteredExternalRetest distinguishes a named human-triggered
// detector requirement from the independent model-judge requirement.
func RequiresRegisteredExternalRetest(requirement *DraftExternalRerenderRequirement) bool {
	return requirement != nil && (requirement.Evaluator == draftExternalEvaluatorRegistered || len(requirement.RequiredExternalRetests) > 0)
}

func registeredExternalRetestIdentities(requirement *DraftExternalRerenderRequirement) []DraftExternalRetestIdentity {
	if requirement == nil {
		return nil
	}
	identities := append([]DraftExternalRetestIdentity(nil), requirement.RequiredExternalRetests...)
	if detector, mode := strings.TrimSpace(requirement.RequiredDetector), strings.TrimSpace(requirement.RequiredMode); detector != "" && mode != "" {
		identities = appendRegisteredExternalRetestIdentity(identities, DraftExternalRetestIdentity{
			Detector: detector, Mode: mode, TriggerBodySHA256: strings.TrimSpace(requirement.EvaluatedBodySHA256),
		})
	}
	return identities
}

// RegisteredExternalRetestLabels returns stable human-readable identities for
// pipeline errors and audit reports.
func RegisteredExternalRetestLabels(requirement *DraftExternalRerenderRequirement) []string {
	identities := registeredExternalRetestIdentities(requirement)
	labels := make([]string, 0, len(identities))
	for _, identity := range identities {
		labels = append(labels, strings.TrimSpace(identity.Detector)+"/"+strings.TrimSpace(identity.Mode))
	}
	return labels
}

// registeredExternalRetestsStrictlyPassedInRows is deliberately independent
// of a persisted marker's pass_exclusive_percent. Named-platform acceptance is
// a fixed protocol boundary: every required detector/mode must score <4% for
// the exact body. A malformed or legacy marker with a looser threshold must
// never freeze prose as if it had met the current contract.
func registeredExternalRetestsStrictlyPassedInRows(requirement *DraftExternalRerenderRequirement, rows []reviewreport.RegisteredExternalDetection) bool {
	identities := registeredExternalRetestIdentities(requirement)
	if len(identities) == 0 {
		return false
	}
	for _, identity := range identities {
		found := false
		for _, row := range rows {
			if !strings.EqualFold(strings.TrimSpace(row.Detector), strings.TrimSpace(identity.Detector)) ||
				!strings.EqualFold(strings.TrimSpace(row.Mode), strings.TrimSpace(identity.Mode)) {
				continue
			}
			found = true
			if row.NormalizedScorePercent >= aigc.PassExclusivePercent {
				return false
			}
			break
		}
		if !found {
			return false
		}
	}
	return true
}

func draftCurrentHashNamedPassFrozen(inspection DraftExternalGateInspection) bool {
	return inspection.Status == DraftExternalGateApproved && inspection.CurrentHashNamedRetestsPassed
}

type RegisteredExternalBodyGateInspection struct {
	Required    bool
	Approved    bool
	Requirement *DraftExternalRerenderRequirement
	Passed      []string
	Missing     []string
	Blocking    []string
}

// InspectRegisteredExternalRetestsForBody verifies named-platform obligations
// against an explicitly selected body hash. Delivery must pass the committed
// chapter hash here; draft routing uses InspectDraftExternalGate instead.
func InspectRegisteredExternalRetestsForBody(projectDir string, chapter int, bodySHA256 string) (RegisteredExternalBodyGateInspection, error) {
	result := RegisteredExternalBodyGateInspection{Approved: true}
	requirement, err := loadDraftExternalRerenderRequirement(projectDir, chapter)
	if err != nil {
		return result, err
	}
	result.Requirement = requirement
	identities := registeredExternalRetestIdentities(requirement)
	rows, err := reviewreport.LatestRegisteredExternalDetections(projectDir, chapter, bodySHA256)
	if err != nil {
		return result, err
	}
	rowByIdentity := make(map[string]reviewreport.RegisteredExternalDetection, len(rows))
	identityKey := func(detector, mode string) string {
		return strings.ToLower(strings.TrimSpace(detector)) + "\x00" + strings.ToLower(strings.TrimSpace(mode))
	}
	for _, row := range rows {
		rowByIdentity[identityKey(row.Detector, row.Mode)] = row
		if row.NormalizedScorePercent >= aigc.PassExclusivePercent {
			identities = appendRegisteredExternalRetestIdentity(identities, DraftExternalRetestIdentity{
				Detector: row.Detector, Mode: row.Mode, TriggerBodySHA256: row.BodySHA256,
			})
		}
	}
	result.Required = len(identities) > 0
	for _, identity := range identities {
		label := strings.TrimSpace(identity.Detector) + "/" + strings.TrimSpace(identity.Mode)
		row, ok := rowByIdentity[identityKey(identity.Detector, identity.Mode)]
		if !ok {
			result.Missing = append(result.Missing, label)
			continue
		}
		if row.NormalizedScorePercent >= aigc.PassExclusivePercent {
			result.Blocking = append(result.Blocking, fmt.Sprintf("%s=%.2f%%", label, row.NormalizedScorePercent))
			continue
		}
		result.Passed = append(result.Passed, fmt.Sprintf("%s=%.2f%%", label, row.NormalizedScorePercent))
	}
	result.Approved = len(result.Missing) == 0 && len(result.Blocking) == 0
	return result, nil
}

func appendRegisteredExternalRetestIdentity(identities []DraftExternalRetestIdentity, candidate DraftExternalRetestIdentity) []DraftExternalRetestIdentity {
	candidate.Detector = strings.TrimSpace(candidate.Detector)
	candidate.Mode = strings.TrimSpace(candidate.Mode)
	candidate.TriggerBodySHA256 = strings.ToLower(strings.TrimSpace(candidate.TriggerBodySHA256))
	if candidate.Detector == "" || candidate.Mode == "" {
		return identities
	}
	for i := range identities {
		if strings.EqualFold(strings.TrimSpace(identities[i].Detector), candidate.Detector) &&
			strings.EqualFold(strings.TrimSpace(identities[i].Mode), candidate.Mode) {
			if candidate.TriggerBodySHA256 != "" {
				identities[i].TriggerBodySHA256 = candidate.TriggerBodySHA256
			}
			return identities
		}
	}
	return append(identities, candidate)
}

func validExternalBodySHA256(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func registeredExternalRerenderRequirement(detection reviewreport.RegisteredExternalDetection, prior *DraftExternalRerenderRequirement) *DraftExternalRerenderRequirement {
	requirement := DraftExternalRerenderRequirement{
		Chapter: detection.Chapter, EvaluatedBodySHA256: strings.TrimSpace(detection.BodySHA256),
		Source: "registered_external_detection", Evaluator: draftExternalEvaluatorRegistered,
		RequiredDetector: strings.TrimSpace(detection.Detector), RequiredMode: strings.TrimSpace(detection.Mode),
		AIProbabilityPercent: int(math.Round(detection.NormalizedScorePercent)),
		PassExclusivePercent: int(aigc.PassExclusivePercent),
		Summary: fmt.Sprintf("%s/%s 整篇检测 %.2f%%，当前正文必须整章重渲染并以同 detector/mode 复测新哈希。",
			strings.TrimSpace(detection.Detector), strings.TrimSpace(detection.Mode), detection.NormalizedScorePercent),
		Evidence: []string{
			fmt.Sprintf("registered_external_detection:%s/%s", strings.TrimSpace(detection.Detector), strings.TrimSpace(detection.Mode)),
			"body_sha256:" + strings.TrimSpace(detection.BodySHA256),
		},
		RevisionPlan: []string{
			"保留结果事实，重新设计主视角选择链、场景承压和段落功能，禁止局部同义替换。",
			"让对白通过漏答、打断、动作和权力转移发生，不按问答模板传递信息。",
			"整章覆盖后冻结新正文，并用同一 detector/mode 对精确 payload 复测；严格低于 4% 才可提交。",
		},
		AdviceComplete: true,
	}
	if prior != nil {
		requirement.InitialDraftBodySHA256 = strings.TrimSpace(prior.InitialDraftBodySHA256)
	}
	requirement.RequiredExternalRetests = registeredExternalRetestIdentities(prior)
	requirement.RequiredExternalRetests = appendRegisteredExternalRetestIdentity(requirement.RequiredExternalRetests, DraftExternalRetestIdentity{
		Detector: detection.Detector, Mode: detection.Mode, TriggerBodySHA256: detection.BodySHA256,
	})
	if prior != nil && len(prior.RevisionPlan) > 0 {
		requirement.RevisionPlan = append([]string(nil), prior.RevisionPlan...)
	}
	return &requirement
}

// SetRegisteredExternalRerenderRequirement persists the named-detector
// contract so changing the prose hash cannot silently fall back to the
// independent model judge.
func SetRegisteredExternalRerenderRequirement(projectDir string, detection reviewreport.RegisteredExternalDetection) error {
	if detection.Chapter <= 0 || strings.TrimSpace(detection.Detector) == "" || strings.TrimSpace(detection.Mode) == "" || !validExternalBodySHA256(detection.BodySHA256) {
		return fmt.Errorf("registered external detection requires chapter, non-empty detector/mode, and a 64-hex body SHA")
	}
	requirement := registeredExternalRerenderRequirement(detection, nil)
	if existing, err := loadDraftExternalRerenderRequirement(projectDir, detection.Chapter); err != nil {
		return err
	} else if existing != nil {
		requirement = registeredExternalRerenderRequirement(detection, existing)
	}
	if requirement.InitialDraftBodySHA256 == "" {
		if raw, err := os.ReadFile(filepath.Join(projectDir, "drafts", fmt.Sprintf("%02d.draft.md", detection.Chapter))); err == nil {
			requirement.InitialDraftBodySHA256 = reviewreport.BodySHA256(string(raw))
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	return SetDraftExternalRerenderRequirement(projectDir, *requirement)
}

// DraftExternalRerenderRequired exposes only the routing fact; prose agents get
// the full evidence through novel_context.
func DraftExternalRerenderRequired(projectDir string, chapter int) (bool, error) {
	inspection, err := InspectDraftExternalGate(projectDir, chapter)
	return inspection.Status == DraftExternalGateRerenderAuthorized, err
}

func RequireDraftExternalApproval(projectDir string, chapter int) error {
	return RequireDraftExternalApprovalWithStore(store.NewStore(projectDir), chapter)
}

func RequireDraftExternalApprovalWithStore(st *store.Store, chapter int) error {
	inspection, err := InspectDraftExternalGateWithStore(st, chapter)
	if err != nil {
		return fmt.Errorf("inspect draft external gate: %w: %w", err, errs.ErrStoreRead)
	}
	switch inspection.Status {
	case DraftExternalGateNotRequired:
		managedPending, pendingErr := pipelineManagedCurrentDraftNeedsDeepSeekJudge(st, chapter, inspection.CurrentBodySHA256)
		if pendingErr != nil {
			return fmt.Errorf("inspect managed draft DeepSeek gate: %w: %w", pendingErr, errs.ErrStoreRead)
		}
		if managedPending {
			return fmt.Errorf("第 %d 章 pipeline 草稿尚未完成当前哈希的 DeepSeek 裸正文复判，禁止提交: %w", chapter, errs.ErrToolPrecondition)
		}
		return nil
	case DraftExternalGateApproved:
		if RequiresRegisteredExternalRetest(inspection.Requirement) && !inspection.CurrentHashNamedRetestsPassed {
			return fmt.Errorf("第 %d 章命名外部检测义务尚未对当前精确哈希全部达到严格 <%.0f%%；Approved 状态本身不能替代 fixed-threshold named receipt，禁止提交: %w",
				chapter, aigc.PassExclusivePercent, errs.ErrToolPrecondition)
		}
		return nil
	case DraftExternalGateRerenderAuthorized:
		return fmt.Errorf("第 %d 章当前草稿仍是外判阻断版本，必须先按完整修改建议整章重渲染: %w", chapter, errs.ErrToolPrecondition)
	case DraftExternalGateAdviceIncomplete:
		return fmt.Errorf("第 %d 章外判没有返回完整修改建议，禁止重渲染和提交，必须先重新外判: %w", chapter, errs.ErrToolPrecondition)
	default:
		if inspection.LocalSoftEditPending {
			return fmt.Errorf("第 %d 章当前哈希已通过 DeepSeek，但本地非结构性 AIGC 门禁仍未通过；只允许按 rewrite_focus 定向 edit_chapter 一次，提交继续保持关闭，编辑后必须复判新哈希: %w", chapter, errs.ErrToolPrecondition)
		}
		if inspection.RequiresRegisteredRetest && inspection.Requirement != nil {
			return fmt.Errorf("第 %d 章草稿已产生新哈希，尚未完成 %s 的同 payload 外部复测，禁止继续修改或提交: %w",
				chapter, strings.Join(RegisteredExternalRetestLabels(inspection.Requirement), ", "), errs.ErrToolPrecondition)
		}
		managedPending, pendingErr := pipelineManagedCurrentDraftNeedsDeepSeekJudge(st, chapter, inspection.CurrentBodySHA256)
		if pendingErr != nil {
			return fmt.Errorf("inspect managed draft DeepSeek gate: %w: %w", pendingErr, errs.ErrStoreRead)
		}
		if managedPending {
			return fmt.Errorf("第 %d 章 pipeline 草稿尚未完成当前哈希的 DeepSeek 裸正文复判，禁止继续修改或提交: %w", chapter, errs.ErrToolPrecondition)
		}
		return fmt.Errorf("第 %d 章草稿已产生新哈希，尚未完成该哈希的外部复判，禁止继续修改或提交: %w", chapter, errs.ErrToolPrecondition)
	}
}

func draftExternalRerenderInstruction(requirement *DraftExternalRerenderRequirement) string {
	if requirement == nil {
		return ""
	}
	if requirement.Source == draftRerenderAuthorizationSource {
		return fmt.Sprintf("第 %d 章已有显式或正式 review 触发的整章重渲染授权；必须复用批准 plan 用 draft_chapter(mode=write) 产生不同的完整正文，新哈希随后等待外层 whole-draft 复判", requirement.Chapter)
	}
	source := "草稿外审"
	if requirement.Source == "local_mechanical_gate" {
		source = "本地整章机械门禁"
	} else if requirement.Source == "deepseek_ai_judge" {
		source = "DeepSeek 当前哈希外判"
	} else if RequiresRegisteredExternalRetest(requirement) {
		source = fmt.Sprintf("注册外部检测 %s", strings.Join(RegisteredExternalRetestLabels(requirement), ", "))
	}
	instruction := fmt.Sprintf(
		"第 %d 章%s为 %d%%（要求 <%d%%），且判定需要结构级重渲染。禁止继续 edit_chapter 局部贴补；请保留批准 plan 与事实，用 draft_chapter(mode=write) 整章覆盖，落实 draft_external_ai_review 与 rewrite_brief 后再检查",
		requirement.Chapter, source, requirement.AIProbabilityPercent, requirement.PassExclusivePercent,
	)
	if requirement.Source == "deepseek_ai_judge" && RequiresRegisteredExternalRetest(requirement) {
		instruction += fmt.Sprintf("；%s 的注册平台复测义务仍保留，但继续暂缓到新稿通过本地门禁与 DeepSeek 当前哈希判断后", strings.Join(RegisteredExternalRetestLabels(requirement), ", "))
	}
	return instruction
}
