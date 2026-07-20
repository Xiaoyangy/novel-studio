package tools

import (
	"crypto/sha256"
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

const draftLocalSoftEditConsumedStep = "draft-local-soft-edit-consumed"

const draftLocalSoftEditTokenVersion = 1

// draftLocalSoftEditToken is the durable half of the one-shot local repair
// capability.  The checkpoint proves consumption order; this artifact binds
// that checkpoint to the exact bytes which were present before edit_chapter
// attempted its atomic replacement.  Keeping the pre-edit hash out of the
// quota identity is intentional: the quota remains spent after a failed write
// and therefore cannot be minted again for the same plan/render seed.
type draftLocalSoftEditToken struct {
	Version           int    `json:"version"`
	Chapter           int    `json:"chapter"`
	QuotaDigest       string `json:"quota_digest"`
	SeedCheckpointSeq int64  `json:"seed_checkpoint_seq"`
	PreEditBodySHA256 string `json:"pre_edit_body_sha256"`
}

type draftLocalSoftEditConsumption struct {
	Token      *draftLocalSoftEditToken
	Checkpoint *domain.Checkpoint
	Legacy     bool
}

type DraftExternalRetestPolicy string

const (
	DraftExternalRetestPolicySamplingOptional DraftExternalRetestPolicy = "sampling_optional"
	DraftExternalRetestPolicyAutomatedHard    DraftExternalRetestPolicy = "automated_hard"
)

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
	// ExternalRetestPolicy makes the production semantics explicit in every new
	// marker. Legacy markers without this field are observational sampling even
	// if they contain block_until_external_retest=true: only the explicit new
	// enum can opt a deployment into an automated hard detector contract.
	ExternalRetestPolicy DraftExternalRetestPolicy `json:"external_retest_policy"`
	// BlockUntilExternalRetest is retained as a legacy compatibility alias.
	// New readers use ExternalRetestPolicy; new hard markers write both fields so
	// older readers still fail closed.
	BlockUntilExternalRetest bool     `json:"block_until_external_retest,omitempty"`
	InitialDraftBodySHA256   string   `json:"initial_draft_body_sha256,omitempty"`
	AIProbabilityPercent     int      `json:"ai_probability_percent"`
	PassExclusivePercent     int      `json:"pass_exclusive_percent"`
	Summary                  string   `json:"summary,omitempty"`
	Evidence                 []string `json:"evidence,omitempty"`
	RevisionPlan             []string `json:"revision_plan,omitempty"`
	AdviceComplete           bool     `json:"advice_complete"`
}

// DraftExternalRetestIdentity preserves which detector/mode produced a sampled
// result. It is provenance by default, not a per-chapter production obligation.
// Only an explicitly opted-in BlockUntilExternalRetest marker treats these
// identities as hard requirements.
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
	// LocalSoftEditPending is retained for a non-probability, non-whole-text
	// deterministic failure.  A fresh managed draft may consume its single edit
	// before the provider call so only the final edited hash is judged; older
	// flows may still discover the same state after an exact-hash DeepSeek pass.
	// A pure local probability disagreement never enters this edit loop.
	// Named-platform retests stay deferred until every real blocker passes.
	LocalSoftEditPending bool
	// LocalSoftEditBeforeJudge distinguishes the latency-saving pre-judge route
	// from recovery of an older provider-passing hash.  Both routes permit the
	// same single edit and require DeepSeek on the resulting exact hash.
	LocalSoftEditBeforeJudge bool
	// LocalSoftEditConsumed records that the current exact body is already the
	// result of a bounded edit and has subsequently passed DeepSeek. A remaining
	// non-whole local proxy is diagnostic at that point; it may not open an
	// unbounded edit/rejudge loop.
	LocalSoftEditConsumed bool
	// LocalSoftEditFailedClosed means the plan/seed quota was consumed but there
	// is no later exact-body edit checkpoint proving that different bytes landed.
	// This is the expected crash/save-failure state: it may never mint a second
	// edit or waive the original deterministic blocker. A new plan epoch is
	// required before prose mutation can resume.
	LocalSoftEditFailedClosed bool
	// CurrentHashNamedRetestsPassed is retained for the explicit opt-in hard
	// external-detector policy. Human-operated sampling never sets it and never
	// freezes or blocks an otherwise approved replacement hash.
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
	Reasons              []string `json:"reasons,omitempty"`
	Evidence             []string `json:"evidence,omitempty"`
	RevisionPlan         []string `json:"revision_plan,omitempty"`
	DialogueFixPlan      []string `json:"dialogue_fix_plan,omitempty"`
	AuthorVoicePlan      []string `json:"author_voice_plan,omitempty"`
	RAGRules             []string `json:"rag_rules,omitempty"`
}

func draftExternalRerenderRequirementPath(projectDir string, chapter int) string {
	return filepath.Join(projectDir, "reviews", "drafts", fmt.Sprintf("%02d_full_rerender_required.json", chapter))
}

func SetDraftExternalRerenderRequirement(projectDir string, requirement DraftExternalRerenderRequirement) error {
	if requirement.Chapter <= 0 {
		return fmt.Errorf("chapter must be > 0")
	}
	if err := normalizeDraftExternalRetestPolicy(&requirement); err != nil {
		return err
	}
	// Only an explicitly opted-in external retest is durable across rewrites.
	// User-reported sampling markers are one-shot rewrite triggers and may be
	// replaced by the current local/DeepSeek reason after the body hash changes.
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
		requirement.ExternalRetestPolicy = DraftExternalRetestPolicyAutomatedHard
		requirement.BlockUntilExternalRetest = true
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
	if err := normalizeDraftExternalRetestPolicy(&requirement); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
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
	tracked, err := pipelineManagedCurrentDraftTracked(st, chapter, bodySHA256)
	if err != nil || !tracked {
		return false, err
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

func pipelineManagedCurrentDraftTracked(st *store.Store, chapter int, bodySHA256 string) (bool, error) {
	if st == nil || !pipelineWritingManaged(st) || chapter <= 0 || strings.TrimSpace(bodySHA256) == "" {
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
	return true, nil
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
		finalRows, finalRowsErr := latestRegisteredExternalDetectionsForDraftGate(projectDir, chapter, inspection.FinalBodySHA256, requirement)
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
	registeredRows, err := latestRegisteredExternalDetectionsForDraftGate(projectDir, chapter, inspection.CurrentBodySHA256, requirement)
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
	registeredSamplingTrigger := isRegisteredExternalSamplingTrigger(requirement)
	status, err := loadDraftExternalJudgeStatus(projectDir, chapter)
	if err != nil {
		return inspection, err
	}
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
			registeredExternalMarkerClearedInRows(requirement, inspection.CurrentBodySHA256, registeredRows)
		if inspection.CurrentBodySHA256 != "" && inspection.CurrentBodySHA256 == inspection.EvaluatedBodySHA256 {
			localProxyProviderRouted := false
			if requirement.Source == "local_mechanical_gate" && !requiresRegisteredRetest {
				content, loadErr := st.Drafts.LoadDraft(chapter)
				if loadErr != nil {
					return inspection, loadErr
				}
				report, gate := inspectDraftAIGCGate(st, chapter, content)
				// Upgrade recovery for markers written by builds which merged a
				// calibratable whole-text probability proxy with statistical prose
				// warnings. Missing/stale/incomplete provider state must fall through
				// to RejudgePending; an exact pass resolves only the probability
				// component and leaves the warnings for LocalSoftEditPending.
				if routed, pendingErr := draftAIGCLocalMarkerProviderRouted(st, chapter, content, report, gate); pendingErr != nil {
					return inspection, pendingErr
				} else {
					localProxyProviderRouted = routed
				}
			}
			if !registeredMarkerCleared && !localProxyProviderRouted {
				inspection.Status = DraftExternalGateRerenderAuthorized
				inspection.RegisteredRetestDeferred = requiresRegisteredRetest
				return inspection, nil
			}
		}
		if requiresRegisteredRetest || registeredSamplingTrigger {
			// Stage 1: never spend a named-platform retest on an intermediate hash
			// that the deterministic whole-text/segment gate still rejects. The
			// exact local marker authorizes one bounded full render while every
			// detector identity remains durable for the eventual candidate.
			if localRequirement, blocked := currentDraftLocalStructuralRerenderRequirement(st, chapter, requirement); blocked {
				inspection.Requirement = localRequirement
				inspection.EvaluatedBodySHA256 = inspection.CurrentBodySHA256
				inspection.Status = DraftExternalGateRerenderAuthorized
				inspection.RegisteredRetestDeferred = requiresRegisteredRetest
				return inspection, nil
			}
			if initialDraft := strings.TrimSpace(requirement.InitialDraftBodySHA256); initialDraft != "" &&
				inspection.CurrentBodySHA256 == initialDraft && inspection.CurrentBodySHA256 != inspection.EvaluatedBodySHA256 &&
				inspection.FinalBodySHA256 == inspection.EvaluatedBodySHA256 {
				inspection.Status = DraftExternalGateRerenderAuthorized
				inspection.RegisteredRetestDeferred = requiresRegisteredRetest
				return inspection, nil
			}
		}
	}

	// Stage 2: resolve deterministic local-soft text defects before paying for a
	// provider call, but only once and only when they are not whole-text/segment
	// failures.  This changes no approval authority: the edited exact hash still
	// requires DeepSeek below.  Probability-only local disagreement skips this
	// path because draftAIGCExternalCurrentBodyBlockers excludes aigc_ratio.
	managedCurrent, managedErr := pipelineManagedCurrentDraftTracked(st, chapter, inspection.CurrentBodySHA256)
	if managedErr != nil {
		return inspection, managedErr
	}
	exactJudgeExists := status != nil && inspection.CurrentBodySHA256 != "" &&
		inspection.CurrentBodySHA256 == strings.TrimSpace(status.BodySHA256)
	if !exactJudgeExists && draftCurrentHashNeedsLocalGateRouting(requirement, managedCurrent) {
		content, loadErr := st.Drafts.LoadDraft(chapter)
		if loadErr != nil {
			return inspection, loadErr
		}
		report, gate := inspectDraftAIGCGate(st, chapter, content)
		localStructural, localSoft := draftExternalLocalGateDisposition(content, report, gate)
		if requiresRegisteredRetest && draftAIGCHasWholeTextStructuralBlock(content, report, draftAIGCRawLocalGateResult(report, gate)) {
			localStructural, localSoft = true, false
		}
		if draftPreJudgeLocalSoftEditEligible(st, chapter, content, report, localStructural, localSoft) {
			if status != nil {
				inspection.ArtifactExists = true
				inspection.EvaluatedBodySHA256 = strings.TrimSpace(status.BodySHA256)
			}
			inspection.Status = DraftExternalGateRejudgePending
			inspection.LocalSoftEditPending = true
			inspection.LocalSoftEditBeforeJudge = true
			inspection.RegisteredRetestDeferred = requiresRegisteredRetest
			return inspection, nil
		}
	}

	// The independent current-hash DeepSeek judgment always precedes any named
	// platform retest. A stale or missing artifact keeps every registered
	// identity deferred; it must never make the pipeline ask a human-operated
	// detector to score bytes that DeepSeek may immediately reject.
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
		if requirement != nil {
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
			inspection.RegisteredRetestDeferred = requiresRegisteredRetest
			return inspection, nil
		}
		// A crash may leave the result but not its marker. Re-running the cached
		// judge restores the single-use authorization without regenerating prose.
		inspection.Status = DraftExternalGateRejudgePending
		return inspection, nil
	}
	if draftCurrentHashNeedsLocalGateRouting(requirement, managedCurrent) {
		// DeepSeek passed the exact current hash. Re-run the effective local gate
		// with that corroboration before approval. Structural failures still
		// consume the bounded full-render budget; a remaining soft failure permits
		// one hash-changing edit while commit stays fail-closed. This applies to
		// sampled external failures too, even though no follow-up sample is needed.
		content, loadErr := st.Drafts.LoadDraft(chapter)
		if loadErr != nil {
			return inspection, loadErr
		}
		report, gate := inspectDraftAIGCGate(st, chapter, content)
		localStructural, localSoft := draftExternalLocalGateDisposition(content, report, gate)
		if localStructural {
			// A prior blocking marker already grants a one-shot replacement and can
			// be upgraded immediately. For a first managed draft, check_consistency
			// persists the exact-body local marker; inspection alone remains
			// read-mostly and must not turn a provider pass into a new write grant.
			if requirement != nil {
				if localRequirement, blocked := currentDraftLocalStructuralRerenderRequirement(st, chapter, requirement); blocked {
					inspection.Requirement = localRequirement
					inspection.EvaluatedBodySHA256 = inspection.CurrentBodySHA256
					inspection.Status = DraftExternalGateRerenderAuthorized
					inspection.RegisteredRetestDeferred = requiresRegisteredRetest
					return inspection, nil
				}
			}
		}
		if localSoft {
			if draftAIGCLocalSoftSatisfiedAfterBoundedEdit(st, chapter, content, report, gate) {
				inspection.LocalSoftEditConsumed = true
			} else {
				consumed, consumedErr := draftLocalSoftEditQuotaConsumed(st, chapter)
				if consumedErr != nil {
					return inspection, consumedErr
				}
				if consumed {
					// The token is written before prose mutation. If no later exact
					// edit checkpoint binds different bytes, the write crashed or
					// failed. Keep both edit and commit closed; only a new plan/seed
					// may create another bounded repair capability.
					inspection.Status = DraftExternalGateAdviceIncomplete
					inspection.LocalSoftEditConsumed = true
					inspection.LocalSoftEditFailedClosed = true
					inspection.RegisteredRetestDeferred = requiresRegisteredRetest
					return inspection, nil
				}
				inspection.Status = DraftExternalGateRejudgePending
				inspection.LocalSoftEditPending = true
				inspection.RegisteredRetestDeferred = requiresRegisteredRetest
				return inspection, nil
			}
		}
		if requirement == nil {
			inspection.Status = DraftExternalGateApproved
			return inspection, nil
		}

		// Stage 3 exists only for an explicitly opted-in automated external gate.
		// Human-operated detector results are sampling signals: once the replacement
		// hash passes the local gate and DeepSeek, production continues without a
		// follow-up result. A later user-reported high score on that exact hash will
		// independently trigger another bounded rewrite.
		if !requiresRegisteredRetest {
			inspection.Status = DraftExternalGateApproved
			return inspection, nil
		}
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

func draftCurrentHashNeedsLocalGateRouting(requirement *DraftExternalRerenderRequirement, managedCurrent bool) bool {
	return requirement != nil || managedCurrent
}

func draftAIGCLocalMarkerProviderRouted(
	st *store.Store,
	chapter int,
	content string,
	report aigc.Report,
	gate draftAIGCGateResult,
) (bool, error) {
	if draftAIGCExternalProbabilityComponentSatisfied(content, report, gate) {
		return true, nil
	}
	return draftAIGCManagedProviderPendingWholeText(st, chapter, content, report, gate)
}

func draftExternalLocalGateDisposition(content string, report aigc.Report, gate draftAIGCGateResult) (structural, soft bool) {
	rawGate := draftAIGCRawLocalGateResult(report, gate)
	if rawGate.Passed {
		return false, false
	}
	if draftAIGCExternalProbabilityComponentSatisfied(content, report, gate) {
		// The exact-body provider resolved the stochastic probability proxy.
		// Concrete statistical/mechanical warnings keep one bounded local-soft
		// repair; they can no longer be recombined with the resolved probability
		// signal into a whole-draft rerender authorization.
		return false, len(draftAIGCExternalCurrentBodyBlockers(content)) > 0
	}
	structural = draftAIGCHasWholeTextStructuralBlock(content, report, rawGate)
	if structural {
		return true, false
	}
	return structural, rawGate.Enforced && !structural
}

func draftExternalJudgeStrictlyPassesBody(status *draftExternalJudgeStatus, bodySHA256 string) bool {
	return status != nil && status.AdviceComplete && !status.Blocking &&
		float64(status.AIProbabilityPercent) < aigc.PassExclusivePercent &&
		strings.TrimSpace(status.BodySHA256) == strings.TrimSpace(bodySHA256)
}

// draftPreJudgeLocalSoftEditEligible is deliberately narrower than the
// post-judge soft path. Before an independent probability exists, we may edit
// only concrete deterministic text violations; a high local probability proxy,
// whole-text/segment risk, or content-integrity floor must go straight to
// DeepSeek. The one-edit quota is owned by the causal plan plus its formal
// review/initial-render seed, not by the current body hash. A later whole render
// under the same seed therefore cannot reopen the edit loop.
func draftPreJudgeLocalSoftEditEligible(
	st *store.Store,
	chapter int,
	content string,
	report aigc.Report,
	localStructural bool,
	localSoft bool,
) bool {
	if st == nil || chapter <= 0 || strings.TrimSpace(content) == "" || localStructural ||
		report.ContentIntegrityFloor > 0 || !localSoft ||
		len(draftAIGCExternalCurrentBodyBlockers(content)) == 0 {
		return false
	}
	consumed, err := draftLocalSoftEditQuotaConsumed(st, chapter)
	return err == nil && !consumed
}

// draftLocalSoftEditQuotaIdentity binds the bounded local repair to the current
// formal plan and the event that seeded this render cycle. A blocking formal
// review is the seed when one exists after the plan; otherwise the first draft
// after the plan is the initial-render seed. Subsequent draft hashes do not
// change this identity. Including sequence numbers preserves legitimate A-B-A
// epochs even when artifact bytes repeat.
func draftLocalSoftEditQuotaIdentity(st *store.Store, chapter int) (digest string, seedSeq int64, err error) {
	plan, err := CurrentChapterPlanCheckpoint(st, chapter)
	if err != nil {
		return "", 0, err
	}
	scope := domain.ChapterScope(chapter)
	body, err := CurrentChapterBodyCheckpoint(st, chapter)
	if err != nil {
		return "", 0, err
	}
	// A formal review starts a new render/edit quota only after prose actually
	// succeeds it. A review written for the current exact body is an outcome of
	// the existing cycle; using it as the seed would re-key an already-consumed
	// token after commit and falsely mint another local-soft edit. Select the
	// newest review strictly between the plan and current body checkpoints.
	var seed *domain.Checkpoint
	for _, checkpoint := range st.Checkpoints.All() {
		if !checkpoint.Scope.Matches(scope) || checkpoint.Step != "review" ||
			checkpoint.Seq <= plan.Seq || checkpoint.Seq >= body.Seq {
			continue
		}
		if seed == nil || checkpoint.Seq > seed.Seq {
			copy := checkpoint
			seed = &copy
		}
	}
	if seed == nil {
		for _, checkpoint := range st.Checkpoints.All() {
			if checkpoint.Seq <= plan.Seq || checkpoint.Seq > body.Seq ||
				!checkpoint.Scope.Matches(scope) || checkpoint.Step != "draft" {
				continue
			}
			copy := checkpoint
			seed = &copy
			break
		}
	}
	if seed == nil || seed.Seq <= plan.Seq || strings.TrimSpace(seed.Digest) == "" {
		return "", 0, fmt.Errorf("第 %d 章当前 plan 后缺少 formal review 或 initial draft seed: %w", chapter, errs.ErrToolPrecondition)
	}
	digest, err = draftLocalSoftEditQuotaDigest(chapter, plan, seed)
	if err != nil {
		return "", 0, err
	}
	return digest, seed.Seq, nil
}

func draftLocalSoftEditQuotaDigest(
	chapter int,
	plan *domain.Checkpoint,
	seed *domain.Checkpoint,
) (string, error) {
	if chapter <= 0 || plan == nil || seed == nil || plan.Seq <= 0 ||
		seed.Seq <= plan.Seq || strings.TrimSpace(plan.Digest) == "" ||
		strings.TrimSpace(seed.Step) == "" || strings.TrimSpace(seed.Digest) == "" {
		return "", fmt.Errorf("local-soft edit quota identity is incomplete: %w", errs.ErrToolPrecondition)
	}
	payload := fmt.Sprintf(
		"draft-local-soft-edit/v1\nchapter=%d\nplan_seq=%d\nplan_digest=%s\nseed_step=%s\nseed_seq=%d\nseed_digest=%s\n",
		chapter, plan.Seq, strings.TrimSpace(plan.Digest), seed.Step, seed.Seq, strings.TrimSpace(seed.Digest),
	)
	sum := sha256.Sum256([]byte(payload))
	return fmt.Sprintf("sha256:%x", sum), nil
}

func draftLocalSoftEditTokenArtifact(chapter int, quotaDigest string) string {
	key := strings.TrimPrefix(strings.TrimSpace(quotaDigest), "sha256:")
	return filepath.ToSlash(filepath.Join(
		"meta", "runtime", "draft_local_soft_edit",
		fmt.Sprintf("ch%03d", chapter), key+".json",
	))
}

func loadDraftLocalSoftEditConsumption(
	st *store.Store,
	chapter int,
	quotaDigest string,
) (*draftLocalSoftEditConsumption, error) {
	if st == nil || chapter <= 0 || strings.TrimSpace(quotaDigest) == "" {
		return nil, nil
	}
	scope := domain.ChapterScope(chapter)
	expectedArtifact := draftLocalSoftEditTokenArtifact(chapter, quotaDigest)
	for _, checkpoint := range st.Checkpoints.All() {
		if !checkpoint.Scope.Matches(scope) || checkpoint.Step != draftLocalSoftEditConsumedStep {
			continue
		}
		// Builds predating the token artifact stored the quota identity directly
		// as checkpoint digest. It remains consumed, but without pre-edit identity
		// it can never be used as acceptance evidence.
		if checkpoint.Digest == quotaDigest &&
			!strings.HasPrefix(filepath.ToSlash(checkpoint.Artifact), "meta/runtime/draft_local_soft_edit/") {
			copy := checkpoint
			return &draftLocalSoftEditConsumption{Checkpoint: &copy, Legacy: true}, nil
		}
		if filepath.ToSlash(strings.TrimSpace(checkpoint.Artifact)) != expectedArtifact {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(st.Dir(), filepath.FromSlash(checkpoint.Artifact)))
		if err != nil {
			return nil, fmt.Errorf("read local-soft edit token %s: %w", checkpoint.Artifact, err)
		}
		if checkpoint.Digest != "sha256:"+reviewreport.BodySHA256(string(raw)) {
			return nil, fmt.Errorf("local-soft edit token checkpoint digest mismatch")
		}
		var token draftLocalSoftEditToken
		if err := json.Unmarshal(raw, &token); err != nil {
			return nil, fmt.Errorf("parse local-soft edit token %s: %w", checkpoint.Artifact, err)
		}
		if token.QuotaDigest != quotaDigest {
			continue
		}
		if token.Version != draftLocalSoftEditTokenVersion || token.Chapter != chapter ||
			token.SeedCheckpointSeq <= 0 || !validExternalBodySHA256(token.PreEditBodySHA256) {
			return nil, fmt.Errorf("invalid local-soft edit token for chapter %d", chapter)
		}
		copy := checkpoint
		return &draftLocalSoftEditConsumption{Token: &token, Checkpoint: &copy}, nil
	}
	return nil, nil
}

// draftLocalSoftEditQuotaConsumed checks the explicit persistent token first.
// For projects created before the token existed, any edit checkpoint after the
// same seed is treated conservatively as already consumed. That migration path
// prevents a process upgrade from granting an extra edit to an in-flight cycle.
func draftLocalSoftEditQuotaConsumed(st *store.Store, chapter int) (bool, error) {
	digest, seedSeq, err := draftLocalSoftEditQuotaIdentity(st, chapter)
	if err != nil {
		return false, err
	}
	consumption, err := loadDraftLocalSoftEditConsumption(st, chapter, digest)
	if err != nil {
		return false, err
	}
	if consumption != nil {
		return true, nil
	}
	scope := domain.ChapterScope(chapter)
	for _, checkpoint := range st.Checkpoints.All() {
		if !checkpoint.Scope.Matches(scope) {
			continue
		}
		if checkpoint.Seq > seedSeq && checkpoint.Step == "edit" {
			return true, nil
		}
	}
	return false, nil
}

// consumeDraftLocalSoftEditQuota persists the at-most-once capability before
// prose bytes are mutated. If the later file write fails, the quota remains
// consumed (fail closed) instead of risking a second edit after restart.
func consumeDraftLocalSoftEditQuota(st *store.Store, chapter int) error {
	digest, seedSeq, err := draftLocalSoftEditQuotaIdentity(st, chapter)
	if err != nil {
		return err
	}
	consumed, err := draftLocalSoftEditQuotaConsumed(st, chapter)
	if err != nil {
		return err
	}
	if consumed {
		return fmt.Errorf("第 %d 章当前 plan/render seed 的唯一本地软修配额已消费: %w", chapter, errs.ErrToolPrecondition)
	}
	content, err := st.Drafts.LoadDraft(chapter)
	if err != nil {
		return fmt.Errorf("load pre-edit body for local-soft token: %w", err)
	}
	if strings.TrimSpace(content) == "" {
		return fmt.Errorf("local-soft token requires a non-empty pre-edit body: %w", errs.ErrToolPrecondition)
	}
	preEditSHA := reviewreport.BodySHA256(content)
	bodyCheckpoint, err := CurrentChapterBodyCheckpoint(st, chapter)
	if err != nil {
		return fmt.Errorf("bind local-soft token to current body checkpoint: %w", err)
	}
	if bodyCheckpoint.Digest != "sha256:"+preEditSHA {
		return fmt.Errorf("local-soft token pre-edit body/checkpoint mismatch: %w", errs.ErrToolPrecondition)
	}
	token := draftLocalSoftEditToken{
		Version:           draftLocalSoftEditTokenVersion,
		Chapter:           chapter,
		QuotaDigest:       digest,
		SeedCheckpointSeq: seedSeq,
		PreEditBodySHA256: preEditSHA,
	}
	raw, err := json.MarshalIndent(token, "", "  ")
	if err != nil {
		return err
	}
	artifact := draftLocalSoftEditTokenArtifact(chapter, digest)
	if err := writeAtomicDraftIntent(filepath.Join(st.Dir(), filepath.FromSlash(artifact)), raw); err != nil {
		return fmt.Errorf("persist local-soft edit token: %w", err)
	}
	_, err = st.Checkpoints.AppendArtifact(
		domain.ChapterScope(chapter),
		draftLocalSoftEditConsumedStep,
		artifact,
	)
	return err
}

// draftAIGCLocalSoftSatisfiedAfterBoundedEdit closes the local soft loop only
// after all three facts are durable: the current body checkpoint is an edit,
// the independent DeepSeek artifact binds these exact bytes and strictly
// passes, and the local failure is non-whole-text. Whole-text/segment failures
// remain structural and can never be waived by this bounded-edit rule.
func draftAIGCLocalSoftSatisfiedAfterBoundedEdit(
	st *store.Store,
	chapter int,
	content string,
	report aigc.Report,
	gate draftAIGCGateResult,
) bool {
	if st == nil || chapter <= 0 || strings.TrimSpace(content) == "" {
		return false
	}
	structural, soft := draftExternalLocalGateDisposition(content, report, gate)
	if structural || !soft {
		return false
	}
	quotaDigest, _, err := draftLocalSoftEditQuotaIdentity(st, chapter)
	if err != nil {
		return false
	}
	consumption, err := loadDraftLocalSoftEditConsumption(st, chapter, quotaDigest)
	if err != nil || consumption == nil || consumption.Legacy || consumption.Token == nil ||
		consumption.Checkpoint == nil {
		return false
	}
	bodyCheckpoint, err := CurrentChapterBodyCheckpoint(st, chapter)
	if err != nil || bodyCheckpoint.Step != "edit" ||
		bodyCheckpoint.Seq <= consumption.Checkpoint.Seq {
		return false
	}
	currentSHA := reviewreport.BodySHA256(content)
	if bodyCheckpoint.Digest != "sha256:"+currentSHA ||
		currentSHA == consumption.Token.PreEditBodySHA256 {
		return false
	}
	status, err := loadDraftExternalJudgeStatus(st.Dir(), chapter)
	if err != nil || status == nil || !status.AdviceComplete || status.Blocking ||
		float64(status.AIProbabilityPercent) >= aigc.PassExclusivePercent ||
		strings.TrimSpace(status.BodySHA256) != currentSHA {
		return false
	}
	return true
}

func normalizeDraftExternalRetestPolicy(requirement *DraftExternalRerenderRequirement) error {
	if requirement == nil {
		return nil
	}
	switch requirement.ExternalRetestPolicy {
	case "":
		requirement.ExternalRetestPolicy = DraftExternalRetestPolicySamplingOptional
		requirement.BlockUntilExternalRetest = false
	case DraftExternalRetestPolicySamplingOptional:
		// The explicit policy wins over a contradictory legacy alias.
		requirement.BlockUntilExternalRetest = false
	case DraftExternalRetestPolicyAutomatedHard:
		requirement.BlockUntilExternalRetest = true
	default:
		return fmt.Errorf("unsupported external_retest_policy %q", requirement.ExternalRetestPolicy)
	}
	if requirement.ExternalRetestPolicy == DraftExternalRetestPolicyAutomatedHard {
		if err := validateAutomatedHardExternalRetestIdentities(requirement); err != nil {
			return err
		}
	}
	return nil
}

func validateAutomatedHardExternalRetestIdentities(requirement *DraftExternalRerenderRequirement) error {
	if requirement == nil {
		return fmt.Errorf("automated_hard external retest policy requires a marker")
	}
	legacyDetector := strings.TrimSpace(requirement.RequiredDetector)
	legacyMode := strings.TrimSpace(requirement.RequiredMode)
	if (legacyDetector == "") != (legacyMode == "") {
		return fmt.Errorf("automated_hard external retest policy requires a complete required_detector/required_mode pair")
	}
	identityCount := 0
	if legacyDetector != "" {
		identityCount++
	}
	for i, identity := range requirement.RequiredExternalRetests {
		detector := strings.TrimSpace(identity.Detector)
		mode := strings.TrimSpace(identity.Mode)
		if detector == "" || mode == "" {
			return fmt.Errorf("automated_hard external retest identity %d requires non-empty detector and mode", i)
		}
		if triggerSHA := strings.TrimSpace(identity.TriggerBodySHA256); triggerSHA != "" && !validExternalBodySHA256(triggerSHA) {
			return fmt.Errorf("automated_hard external retest identity %d has invalid trigger_body_sha256", i)
		}
		identityCount++
	}
	if identityCount == 0 {
		return fmt.Errorf("automated_hard external retest policy requires at least one detector/mode identity")
	}
	return nil
}

func effectiveDraftExternalRetestPolicy(requirement *DraftExternalRerenderRequirement) DraftExternalRetestPolicy {
	if requirement == nil {
		return DraftExternalRetestPolicySamplingOptional
	}
	switch requirement.ExternalRetestPolicy {
	case DraftExternalRetestPolicySamplingOptional, DraftExternalRetestPolicyAutomatedHard:
		return requirement.ExternalRetestPolicy
	default:
		// Legacy block_until_external_retest is deliberately not sufficient to
		// opt into an automated release dependency. Older markers may have been
		// produced from a user-operated detector and must remain sampling-only.
		return DraftExternalRetestPolicySamplingOptional
	}
}

// RequiresRegisteredExternalRetest is deliberately opt-in. Human-triggered
// detectors are sampled by the user and therefore never become an implicit
// per-chapter production dependency merely because a marker names them.
func RequiresRegisteredExternalRetest(requirement *DraftExternalRerenderRequirement) bool {
	return requirement != nil &&
		effectiveDraftExternalRetestPolicy(requirement) == DraftExternalRetestPolicyAutomatedHard &&
		len(registeredExternalRetestIdentities(requirement)) > 0
}

func isRegisteredExternalSamplingTrigger(requirement *DraftExternalRerenderRequirement) bool {
	return requirement != nil && (requirement.Source == "registered_external_detection" || requirement.Evaluator == draftExternalEvaluatorRegistered)
}

// latestRegisteredExternalDetectionsForDraftGate keeps a human-operated
// sampling journal observational. A missing or malformed optional journal is
// equivalent to no usable sample and must not become a production dependency.
// An explicitly configured automated-hard marker still fails closed because
// that deployment has made the journal part of its release contract.
func latestRegisteredExternalDetectionsForDraftGate(projectDir string, chapter int, bodySHA256 string, requirement *DraftExternalRerenderRequirement) ([]reviewreport.RegisteredExternalDetection, error) {
	rows, err := reviewreport.LatestRegisteredExternalDetections(projectDir, chapter, bodySHA256)
	if err != nil {
		if RequiresRegisteredExternalRetest(requirement) {
			return nil, err
		}
		return nil, nil
	}
	return rows, nil
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

func registeredExternalIdentitiesStrictlyPassedInRows(identities []DraftExternalRetestIdentity, rows []reviewreport.RegisteredExternalDetection) bool {
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

// registeredExternalRetestsStrictlyPassedInRows is deliberately independent
// of a persisted marker's pass_exclusive_percent. Automated-hard acceptance is
// a fixed protocol boundary: every required detector/mode must score <4% for
// the exact body. A malformed or legacy marker with a looser threshold must
// never freeze prose as if it had met the current contract.
func registeredExternalRetestsStrictlyPassedInRows(requirement *DraftExternalRerenderRequirement, rows []reviewreport.RegisteredExternalDetection) bool {
	return registeredExternalIdentitiesStrictlyPassedInRows(registeredExternalRetestIdentities(requirement), rows)
}

// registeredExternalMarkerClearedInRows distinguishes a same-payload sampling
// correction from an automated-hard retest contract. Optional sampling markers
// may contain identities accumulated by an older build; only identities bound
// to the marker's current body may participate in clearing it. Historical-body
// identities can therefore never turn a missing user sample into a production
// dependency.
func registeredExternalMarkerClearedInRows(requirement *DraftExternalRerenderRequirement, bodySHA256 string, rows []reviewreport.RegisteredExternalDetection) bool {
	if requirement == nil {
		return false
	}
	if RequiresRegisteredExternalRetest(requirement) {
		return registeredExternalRetestsStrictlyPassedInRows(requirement, rows)
	}
	bodySHA256 = strings.ToLower(strings.TrimSpace(bodySHA256))
	if bodySHA256 == "" || bodySHA256 != strings.ToLower(strings.TrimSpace(requirement.EvaluatedBodySHA256)) {
		return false
	}
	var exactBodyIdentities []DraftExternalRetestIdentity
	for _, identity := range registeredExternalRetestIdentities(requirement) {
		triggerSHA := strings.ToLower(strings.TrimSpace(identity.TriggerBodySHA256))
		if triggerSHA == bodySHA256 {
			exactBodyIdentities = appendRegisteredExternalRetestIdentity(exactBodyIdentities, identity)
			continue
		}
		if triggerSHA != "" {
			continue
		}
		// Some legacy markers omitted trigger_body_sha256. Treat such an identity
		// as current only when an exact-body row for it actually exists; never
		// manufacture a missing obligation from the absent hash.
		for _, row := range rows {
			if strings.EqualFold(strings.TrimSpace(row.Detector), strings.TrimSpace(identity.Detector)) &&
				strings.EqualFold(strings.TrimSpace(row.Mode), strings.TrimSpace(identity.Mode)) {
				identity.TriggerBodySHA256 = bodySHA256
				exactBodyIdentities = appendRegisteredExternalRetestIdentity(exactBodyIdentities, identity)
				break
			}
		}
	}
	return registeredExternalIdentitiesStrictlyPassedInRows(exactBodyIdentities, rows)
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
	var identities []DraftExternalRetestIdentity
	if RequiresRegisteredExternalRetest(requirement) {
		identities = registeredExternalRetestIdentities(requirement)
	}
	rows, err := latestRegisteredExternalDetectionsForDraftGate(projectDir, chapter, bodySHA256, requirement)
	if err != nil {
		return result, err
	}
	// A persisted optional marker is itself durable evidence that this exact
	// payload was sampled high. Losing or corrupting the observational journal
	// must not turn the already-known rejected bytes into a delivery pass. A
	// later same-identity low row on the same SHA may still clear the marker; an
	// old-body marker never affects a replacement body.
	if requirement != nil && !RequiresRegisteredExternalRetest(requirement) &&
		isRegisteredExternalSamplingTrigger(requirement) &&
		strings.EqualFold(strings.TrimSpace(requirement.EvaluatedBodySHA256), strings.TrimSpace(bodySHA256)) &&
		float64(requirement.AIProbabilityPercent) >= aigc.PassExclusivePercent &&
		!registeredExternalMarkerClearedInRows(requirement, bodySHA256, rows) {
		labels := RegisteredExternalRetestLabels(requirement)
		label := "registered-sampling-marker"
		if len(labels) > 0 {
			label = strings.Join(labels, ",")
		}
		result.Required = true
		result.Approved = false
		result.Blocking = []string{fmt.Sprintf("%s=marker %.2f%%", label, float64(requirement.AIProbabilityPercent))}
		return result, nil
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
		ExternalRetestPolicy: DraftExternalRetestPolicySamplingOptional,
		AIProbabilityPercent: int(math.Round(detection.NormalizedScorePercent)),
		PassExclusivePercent: int(aigc.PassExclusivePercent),
		Summary: fmt.Sprintf("%s/%s 抽查整篇检测 %.2f%%，当前正文必须整章重渲染；替换稿通过本地门禁、DeepSeek 与一致性检查后即可继续生产，不等待人工复测。",
			strings.TrimSpace(detection.Detector), strings.TrimSpace(detection.Mode), detection.NormalizedScorePercent),
		Evidence: []string{
			fmt.Sprintf("registered_external_detection:%s/%s", strings.TrimSpace(detection.Detector), strings.TrimSpace(detection.Mode)),
			"body_sha256:" + strings.TrimSpace(detection.BodySHA256),
		},
		RevisionPlan: []string{
			"保留结果事实，重新设计主视角选择链、场景承压和段落功能，禁止局部同义替换。",
			"让对白通过漏答、打断、动作和权力转移发生，不按问答模板传递信息。",
			"整章覆盖后冻结新正文，依次通过本地 AIGC 门禁、DeepSeek 裸正文判定和事实一致性检查；外部平台仅由用户抽查，缺少复测结果不得阻塞提交。",
		},
		AdviceComplete: true,
	}
	priorIsAutomatedHard := RequiresRegisteredExternalRetest(prior)
	if prior != nil {
		requirement.InitialDraftBodySHA256 = strings.TrimSpace(prior.InitialDraftBodySHA256)
	}
	if priorIsAutomatedHard {
		requirement.ExternalRetestPolicy = DraftExternalRetestPolicyAutomatedHard
		requirement.BlockUntilExternalRetest = true
		requirement.RequiredDetector = strings.TrimSpace(prior.RequiredDetector)
		requirement.RequiredMode = strings.TrimSpace(prior.RequiredMode)
		requirement.RequiredExternalRetests = registeredExternalRetestIdentities(prior)
		if len(prior.RevisionPlan) > 0 {
			requirement.RevisionPlan = append([]string(nil), prior.RevisionPlan...)
		}
	}
	// Optional user sampling is exact-payload provenance, not a chapter-lifetime
	// contract. Start a fresh identity set for each sampled body. The append-only
	// detection log retains history without making an old detector/mode or its
	// revision wording a requirement for the replacement hash.
	if !priorIsAutomatedHard {
		// This function is the user-sampling bridge. A sampled detector remains
		// provenance only and must never be promoted into an unrelated deployment's
		// explicit automated_hard identity set.
		requirement.RequiredExternalRetests = appendRegisteredExternalRetestIdentity(requirement.RequiredExternalRetests, DraftExternalRetestIdentity{
			Detector: detection.Detector, Mode: detection.Mode, TriggerBodySHA256: detection.BodySHA256,
		})
	}
	return &requirement
}

// SetRegisteredExternalRerenderRequirement persists one user-reported sampling
// failure as a rewrite trigger. The detector identity remains auditable, but
// the replacement hash is released by automated local/DeepSeek/consistency
// gates and does not wait for another human-operated platform result.
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
			return fmt.Errorf("第 %d 章显式配置的自动外部门禁尚未对当前精确哈希全部达到严格 <%.0f%%；用户手工抽查不会创建该义务，禁止提交: %w",
				chapter, aigc.PassExclusivePercent, errs.ErrToolPrecondition)
		}
		return nil
	case DraftExternalGateRerenderAuthorized:
		return fmt.Errorf("第 %d 章当前草稿仍是外判阻断版本，必须先按完整修改建议整章重渲染: %w", chapter, errs.ErrToolPrecondition)
	case DraftExternalGateAdviceIncomplete:
		if inspection.LocalSoftEditFailedClosed {
			return fmt.Errorf("第 %d 章当前 plan/render seed 的本地软修 token 已消费，但没有更晚、不同哈希的 exact-body edit checkpoint；疑似编辑写入失败或中断，禁止再次编辑或提交，必须先建立新 plan epoch: %w", chapter, errs.ErrToolPrecondition)
		}
		return fmt.Errorf("第 %d 章外判没有返回完整修改建议，禁止重渲染和提交，必须先重新外判: %w", chapter, errs.ErrToolPrecondition)
	default:
		if inspection.LocalSoftEditPending {
			return fmt.Errorf("第 %d 章当前哈希已通过 DeepSeek，但本地非结构性 AIGC 门禁仍未通过；只允许按 rewrite_focus 定向 edit_chapter 一次，提交继续保持关闭，编辑后必须复判新哈希: %w", chapter, errs.ErrToolPrecondition)
		}
		if inspection.RequiresRegisteredRetest && inspection.Requirement != nil {
			return fmt.Errorf("第 %d 章草稿已产生新哈希，显式自动外部门禁尚未完成 %s 的同 payload 复测，禁止继续修改或提交: %w",
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
	} else if isRegisteredExternalSamplingTrigger(requirement) {
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
