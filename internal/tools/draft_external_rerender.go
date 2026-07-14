package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/errs"
	"github.com/chenhongyang/novel-studio/internal/reviewreport"
)

// DraftExternalRerenderRequirement is written by the external draft judge when
// a whole-chapter result is blocking. It prevents a structural diagnosis from
// being "fixed" through a chain of tiny string replacements.
type DraftExternalRerenderRequirement struct {
	Chapter              int      `json:"chapter"`
	EvaluatedBodySHA256  string   `json:"evaluated_body_sha256"`
	Source               string   `json:"source,omitempty"`
	AIProbabilityPercent int      `json:"ai_probability_percent"`
	PassExclusivePercent int      `json:"pass_exclusive_percent"`
	Summary              string   `json:"summary,omitempty"`
	Evidence             []string `json:"evidence,omitempty"`
	RevisionPlan         []string `json:"revision_plan,omitempty"`
	AdviceComplete       bool     `json:"advice_complete"`
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
	Status              DraftExternalGateStatus
	CurrentBodySHA256   string
	EvaluatedBodySHA256 string
	Requirement         *DraftExternalRerenderRequirement
	ArtifactExists      bool
}

type draftExternalJudgeStatus struct {
	BodySHA256           string `json:"body_sha256"`
	Blocking             bool   `json:"blocking"`
	AdviceComplete       bool   `json:"advice_complete"`
	AIProbabilityPercent int    `json:"ai_probability_percent"`
	PassExclusivePercent int    `json:"pass_exclusive_percent"`
}

func draftExternalRerenderRequirementPath(projectDir string, chapter int) string {
	return filepath.Join(projectDir, "reviews", "drafts", fmt.Sprintf("%02d_full_rerender_required.json", chapter))
}

func SetDraftExternalRerenderRequirement(projectDir string, requirement DraftExternalRerenderRequirement) error {
	if requirement.Chapter <= 0 {
		return fmt.Errorf("chapter must be > 0")
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

// InspectDraftExternalGate treats a blocking judgment as a single-use
// authorization. The judged hash may be replaced once; the replacement hash
// must then be judged before another prose write or commit is allowed.
func InspectDraftExternalGate(projectDir string, chapter int) (DraftExternalGateInspection, error) {
	inspection := DraftExternalGateInspection{Status: DraftExternalGateNotRequired}
	draftPath := filepath.Join(projectDir, "drafts", fmt.Sprintf("%02d.draft.md", chapter))
	if raw, err := os.ReadFile(draftPath); err == nil {
		inspection.CurrentBodySHA256 = reviewreport.BodySHA256(string(raw))
	} else if !os.IsNotExist(err) {
		return inspection, err
	}

	requirement, err := loadDraftExternalRerenderRequirement(projectDir, chapter)
	if err != nil {
		return inspection, err
	}
	inspection.Requirement = requirement
	if requirement != nil {
		inspection.EvaluatedBodySHA256 = strings.TrimSpace(requirement.EvaluatedBodySHA256)
		if !requirement.AdviceComplete || len(requirement.RevisionPlan) == 0 {
			inspection.Status = DraftExternalGateAdviceIncomplete
			return inspection, nil
		}
		if inspection.CurrentBodySHA256 != "" && inspection.CurrentBodySHA256 == inspection.EvaluatedBodySHA256 {
			inspection.Status = DraftExternalGateRerenderAuthorized
			return inspection, nil
		}
		inspection.Status = DraftExternalGateRejudgePending
		return inspection, nil
	}

	status, err := loadDraftExternalJudgeStatus(projectDir, chapter)
	if err != nil {
		return inspection, err
	}
	if status == nil {
		return inspection, nil
	}
	inspection.ArtifactExists = true
	inspection.EvaluatedBodySHA256 = strings.TrimSpace(status.BodySHA256)
	if inspection.CurrentBodySHA256 == "" || inspection.CurrentBodySHA256 != inspection.EvaluatedBodySHA256 {
		inspection.Status = DraftExternalGateRejudgePending
		return inspection, nil
	}
	if status.Blocking || status.AIProbabilityPercent >= status.PassExclusivePercent {
		// A crash may leave the result but not its marker. Re-running the cached
		// judge restores the single-use authorization without regenerating prose.
		inspection.Status = DraftExternalGateRejudgePending
		return inspection, nil
	}
	inspection.Status = DraftExternalGateApproved
	return inspection, nil
}

// DraftExternalRerenderRequired exposes only the routing fact; prose agents get
// the full evidence through novel_context.
func DraftExternalRerenderRequired(projectDir string, chapter int) (bool, error) {
	inspection, err := InspectDraftExternalGate(projectDir, chapter)
	return inspection.Status == DraftExternalGateRerenderAuthorized, err
}

func RequireDraftExternalApproval(projectDir string, chapter int) error {
	inspection, err := InspectDraftExternalGate(projectDir, chapter)
	if err != nil {
		return fmt.Errorf("inspect draft external gate: %w: %w", err, errs.ErrStoreRead)
	}
	switch inspection.Status {
	case DraftExternalGateNotRequired, DraftExternalGateApproved:
		return nil
	case DraftExternalGateRerenderAuthorized:
		return fmt.Errorf("第 %d 章当前草稿仍是外判阻断版本，必须先按完整修改建议整章重渲染: %w", chapter, errs.ErrToolPrecondition)
	case DraftExternalGateAdviceIncomplete:
		return fmt.Errorf("第 %d 章外判没有返回完整修改建议，禁止重渲染和提交，必须先重新外判: %w", chapter, errs.ErrToolPrecondition)
	default:
		return fmt.Errorf("第 %d 章草稿已产生新哈希，尚未完成该哈希的外部复判，禁止继续修改或提交: %w", chapter, errs.ErrToolPrecondition)
	}
}

func draftExternalRerenderInstruction(requirement *DraftExternalRerenderRequirement) string {
	if requirement == nil {
		return ""
	}
	source := "草稿外审"
	if requirement.Source == "local_mechanical_gate" {
		source = "本地整章机械门禁"
	}
	return fmt.Sprintf(
		"第 %d 章%s为 %d%%（要求 <%d%%），且判定需要结构级重渲染。禁止继续 edit_chapter 局部贴补；请保留批准 plan 与事实，用 draft_chapter(mode=write) 整章覆盖，落实 draft_external_ai_review 与 rewrite_brief 后再检查",
		requirement.Chapter, source, requirement.AIProbabilityPercent, requirement.PassExclusivePercent,
	)
}
