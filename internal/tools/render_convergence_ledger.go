package tools

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/chenhongyang/novel-studio/internal/reviewreport"
	"github.com/chenhongyang/novel-studio/internal/store"
)

const (
	toolRenderCandidateManifestVersion = "pipeline-render-candidate.v2"
	toolRenderConvergenceLedgerVersion = "pipeline-render-convergence.v1"
)

var renderConvergenceLedgerProcessMu sync.Mutex

type toolRenderCandidateManifest struct {
	Version                string `json:"version"`
	CandidateID            string `json:"candidate_id"`
	GenerationID           string `json:"generation_id"`
	Chapter                int    `json:"chapter"`
	PlanDigest             string `json:"plan_digest"`
	PlanCheckpointSeq      int64  `json:"plan_checkpoint_seq"`
	ProjectedBundleDigest  string `json:"projected_bundle_digest"`
	PromotionReceiptDigest string `json:"promotion_receipt_digest"`
	SourceOutputDir        string `json:"source_output_dir"`
}

type toolRenderConvergenceRecord struct {
	BodySHA256       string `json:"body_sha256"`
	WholeDraft       bool   `json:"whole_draft,omitempty"`
	Edited           bool   `json:"edited,omitempty"`
	ExternalJudged   bool   `json:"external_judged,omitempty"`
	ExternalBlocking bool   `json:"external_blocking,omitempty"`
	StructuralBlock  bool   `json:"structural_block,omitempty"`
	SemanticReject   bool   `json:"semantic_reject,omitempty"`
	FormalAccepted   bool   `json:"formal_accepted,omitempty"`
}

type toolRenderConvergenceLedger struct {
	Version                string                        `json:"version"`
	CandidateID            string                        `json:"candidate_id"`
	GenerationID           string                        `json:"generation_id"`
	Chapter                int                           `json:"chapter"`
	PlanDigest             string                        `json:"plan_digest"`
	PlanCheckpointSeq      int64                         `json:"plan_checkpoint_seq"`
	ProjectedBundleDigest  string                        `json:"projected_bundle_digest"`
	PromotionReceiptDigest string                        `json:"promotion_receipt_digest"`
	FailureLimit           int                           `json:"failure_limit"`
	Records                []toolRenderConvergenceRecord `json:"records"`
	UpdatedAt              string                        `json:"updated_at"`
}

// RenderConvergencePlanStageRequiredError crosses the tools/main package
// boundary without collapsing into an ordinary retryable tool precondition.
// The sealed candidate runner recognizes it and preserves the active tree.
type RenderConvergencePlanStageRequiredError struct {
	Chapter  int
	Attempts int
	Limit    int
}

type RenderConvergenceExhaustion struct {
	Active   bool
	Required bool
	Chapter  int
	Attempts int
	Limit    int
	Reason   string
}

func (e *RenderConvergencePlanStageRequiredError) Error() string {
	if e == nil {
		return "render convergence budget exhausted"
	}
	return fmt.Sprintf(
		"第 %d 章同一冻结 plan 已有 %d 个不同 exact-body 被阻断，达到持久化上限 %d；禁止继续 draft_chapter，当前正文与反馈保持原位。必须执行 --stages plan --restart 建立新 plan epoch",
		e.Chapter,
		e.Attempts,
		e.Limit,
	)
}

func IsRenderConvergencePlanStageRequired(err error) bool {
	var target *RenderConvergencePlanStageRequiredError
	return errors.As(err, &target)
}

func activeToolRenderCandidateManifest(st *store.Store, chapter int) (*toolRenderCandidateManifest, error) {
	if st == nil || chapter <= 0 {
		return nil, nil
	}
	path := filepath.Join(st.Dir(), "meta", "planning", "render_candidate.json")
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var manifest toolRenderCandidateManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return nil, fmt.Errorf("decode render candidate convergence identity: %w", err)
	}
	if manifest.Version != toolRenderCandidateManifestVersion ||
		manifest.Chapter != chapter || manifest.PlanCheckpointSeq <= 0 ||
		strings.TrimSpace(manifest.PlanDigest) == "" ||
		strings.TrimSpace(manifest.SourceOutputDir) == "" ||
		strings.TrimSpace(manifest.CandidateID) == "" ||
		filepath.Base(manifest.CandidateID) != manifest.CandidateID ||
		strings.ContainsAny(manifest.CandidateID, `/\\`) {
		return nil, fmt.Errorf("render candidate convergence identity is invalid")
	}
	// A promoted live tree retains this manifest only as provenance.
	if filepath.Clean(st.Dir()) == filepath.Clean(manifest.SourceOutputDir) {
		return nil, nil
	}
	plan, err := CurrentChapterPlanCheckpoint(st, chapter)
	if err != nil {
		return nil, fmt.Errorf("validate render convergence plan identity: %w", err)
	}
	if plan.Digest != manifest.PlanDigest || plan.Seq != manifest.PlanCheckpointSeq {
		return nil, fmt.Errorf("render convergence plan identity drifted")
	}
	return &manifest, nil
}

func toolRenderConvergenceLedgerPath(manifest *toolRenderCandidateManifest) string {
	return filepath.Join(
		filepath.Dir(filepath.Clean(manifest.SourceOutputDir)),
		".render-candidates",
		"convergence",
		manifest.CandidateID,
		"ledger.json",
	)
}

func toolRenderConvergenceLimit(st *store.Store, chapter int) int {
	limit := 3
	if plan, err := st.Drafts.LoadChapterPlan(chapter); err == nil && plan != nil &&
		plan.CausalSimulation.ReviewRefinement.IterationLimit > 0 {
		limit = plan.CausalSimulation.ReviewRefinement.IterationLimit
	}
	if limit < 2 {
		return 2
	}
	if limit > 4 {
		return 4
	}
	return limit
}

func loadToolRenderConvergenceLedger(
	st *store.Store,
	manifest *toolRenderCandidateManifest,
) (*toolRenderConvergenceLedger, error) {
	path := toolRenderConvergenceLedgerPath(manifest)
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &toolRenderConvergenceLedger{
			Version:                toolRenderConvergenceLedgerVersion,
			CandidateID:            manifest.CandidateID,
			GenerationID:           manifest.GenerationID,
			Chapter:                manifest.Chapter,
			PlanDigest:             manifest.PlanDigest,
			PlanCheckpointSeq:      manifest.PlanCheckpointSeq,
			ProjectedBundleDigest:  manifest.ProjectedBundleDigest,
			PromotionReceiptDigest: manifest.PromotionReceiptDigest,
			FailureLimit:           toolRenderConvergenceLimit(st, manifest.Chapter),
		}, nil
	}
	if err != nil {
		return nil, err
	}
	var ledger toolRenderConvergenceLedger
	if err := json.Unmarshal(raw, &ledger); err != nil {
		return nil, fmt.Errorf("decode plan-owned render convergence ledger: %w", err)
	}
	if ledger.Version != toolRenderConvergenceLedgerVersion ||
		ledger.CandidateID != manifest.CandidateID ||
		ledger.GenerationID != manifest.GenerationID ||
		ledger.Chapter != manifest.Chapter ||
		ledger.PlanDigest != manifest.PlanDigest ||
		ledger.PlanCheckpointSeq != manifest.PlanCheckpointSeq ||
		ledger.ProjectedBundleDigest != manifest.ProjectedBundleDigest ||
		ledger.PromotionReceiptDigest != manifest.PromotionReceiptDigest ||
		ledger.FailureLimit < 2 || ledger.FailureLimit > 4 {
		return nil, fmt.Errorf("plan-owned render convergence ledger identity mismatch")
	}
	seen := map[string]struct{}{}
	for _, record := range ledger.Records {
		if !validExternalBodySHA256(record.BodySHA256) {
			return nil, fmt.Errorf("plan-owned render convergence body hash is malformed")
		}
		if _, ok := seen[record.BodySHA256]; ok {
			return nil, fmt.Errorf("plan-owned render convergence body hash is duplicated")
		}
		seen[record.BodySHA256] = struct{}{}
	}
	return &ledger, nil
}

func saveToolRenderConvergenceLedger(ledger *toolRenderConvergenceLedger, path string) error {
	sort.Slice(ledger.Records, func(i, j int) bool {
		return ledger.Records[i].BodySHA256 < ledger.Records[j].BodySHA256
	})
	ledger.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	raw, err := json.MarshalIndent(ledger, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomicDraftIntent(path, append(raw, '\n'))
}

func toolRenderConvergenceFailureCount(ledger *toolRenderConvergenceLedger) int {
	count := 0
	for _, record := range ledger.Records {
		if record.ExternalBlocking || record.StructuralBlock ||
			(record.SemanticReject && !record.FormalAccepted) {
			count++
		}
	}
	return count
}

func toolRenderConvergenceFailedHashes(ledger *toolRenderConvergenceLedger) []string {
	hashes := make([]string, 0, len(ledger.Records))
	for _, record := range ledger.Records {
		if record.ExternalBlocking || record.StructuralBlock ||
			(record.SemanticReject && !record.FormalAccepted) {
			hashes = append(hashes, record.BodySHA256)
		}
	}
	sort.Strings(hashes)
	return hashes
}

func InspectRenderConvergenceExhaustion(
	st *store.Store,
	chapter int,
) (RenderConvergenceExhaustion, error) {
	result := RenderConvergenceExhaustion{Chapter: chapter}
	manifest, err := activeToolRenderCandidateManifest(st, chapter)
	if err != nil || manifest == nil {
		return result, err
	}
	result.Active = true
	renderConvergenceLedgerProcessMu.Lock()
	defer renderConvergenceLedgerProcessMu.Unlock()
	ledger, err := loadToolRenderConvergenceLedger(st, manifest)
	if err != nil {
		return result, err
	}
	result.Attempts = toolRenderConvergenceFailureCount(ledger)
	result.Limit = ledger.FailureLimit
	if result.Attempts < result.Limit {
		return result, nil
	}
	result.Required = true
	result.Reason = "同一冻结 plan 的 semantic、local structural 与 external exact-body 失败总数已达到持久上限"
	return result, nil
}

func RequireRenderConvergenceAttemptAvailable(st *store.Store, chapter int) error {
	inspection, err := InspectRenderConvergenceExhaustion(st, chapter)
	if err != nil || !inspection.Required {
		return err
	}
	return &RenderConvergencePlanStageRequiredError{
		Chapter: chapter, Attempts: inspection.Attempts, Limit: inspection.Limit,
	}
}

// currentRenderConvergenceAttemptSettledBySemanticReject recognizes the exact
// boundary between two whole-draft attempts. A render attempt may legitimately
// end as draft -> bounded edit(s) -> provider judgment -> formal semantic
// rejection, so the hash carrying WholeDraft is not necessarily the hash that
// is ultimately judged and reviewed. The final reviewed hash settles that
// attempt and authorizes one fresh whole draft; the replacement hash itself is
// still required to pass the ordinary current-hash provider gate before any
// further prose mutation.
//
// Keep this capability deliberately narrow. A plain missing/stale judge, a
// structural failure, or a blocking provider result must not be upgraded into
// semantic-rewrite authority merely because an older body was reviewed.
func currentRenderConvergenceAttemptSettledBySemanticReject(
	st *store.Store,
	chapter int,
) (bool, error) {
	if st == nil || chapter <= 0 || !ReviewRequiresFreshDraft(st, chapter) {
		return false, nil
	}
	manifest, err := activeToolRenderCandidateManifest(st, chapter)
	if err != nil || manifest == nil {
		return false, err
	}
	body, err := st.Drafts.LoadDraft(chapter)
	if err != nil {
		return false, err
	}
	bodySHA := reviewreport.BodySHA256(body)
	tracked, err := pipelineManagedCurrentDraftTracked(st, chapter, bodySHA)
	if err != nil || !tracked {
		return false, err
	}

	renderConvergenceLedgerProcessMu.Lock()
	defer renderConvergenceLedgerProcessMu.Unlock()
	ledger, err := loadToolRenderConvergenceLedger(st, manifest)
	if err != nil {
		return false, err
	}
	for _, record := range ledger.Records {
		if record.BodySHA256 != bodySHA {
			continue
		}
		return record.ExternalJudged && !record.ExternalBlocking &&
			record.SemanticReject && !record.FormalAccepted &&
			!record.StructuralBlock, nil
	}
	return false, nil
}

// recordRenderConvergenceStructuralBlock is called synchronously from the
// local whole-text gate. The plan-owned ledger is replaced atomically first;
// only then is the candidate-local draft guard refreshed. A crash between the
// two cannot lose the authoritative failure, and the next outer sync repairs
// the projection.
func recordRenderConvergenceStructuralBlock(st *store.Store, chapter int, content string) error {
	manifest, err := activeToolRenderCandidateManifest(st, chapter)
	if err != nil || manifest == nil {
		return err
	}
	bodySHA := reviewreport.BodySHA256(content)
	renderConvergenceLedgerProcessMu.Lock()
	defer renderConvergenceLedgerProcessMu.Unlock()
	ledger, err := loadToolRenderConvergenceLedger(st, manifest)
	if err != nil {
		return err
	}
	found := false
	for i := range ledger.Records {
		if ledger.Records[i].BodySHA256 != bodySHA {
			continue
		}
		ledger.Records[i].WholeDraft = true
		ledger.Records[i].StructuralBlock = true
		found = true
		break
	}
	if !found {
		ledger.Records = append(ledger.Records, toolRenderConvergenceRecord{
			BodySHA256: bodySHA, WholeDraft: true, StructuralBlock: true,
		})
	}
	if err := saveToolRenderConvergenceLedger(ledger, toolRenderConvergenceLedgerPath(manifest)); err != nil {
		return err
	}
	return SaveRenderConvergenceGuard(
		st,
		chapter,
		manifest.PlanDigest,
		toolRenderConvergenceFailedHashes(ledger),
	)
}
