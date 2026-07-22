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
	toolRenderCandidateManifestVersion         = "pipeline-render-candidate.v3-effective-style"
	toolRenderCandidatePreStyleManifestVersion = "pipeline-render-candidate.v3-pre-style"
	toolRenderCandidatePreviousManifestVersion = "pipeline-render-candidate.v2"
	toolRenderConvergenceLedgerVersion         = "pipeline-render-convergence.v1"
)

var renderConvergenceLedgerProcessMu sync.Mutex

type toolRenderCandidateManifest struct {
	Version                     string `json:"version"`
	CandidateID                 string `json:"candidate_id"`
	GenerationID                string `json:"generation_id"`
	Chapter                     int    `json:"chapter"`
	PlanDigest                  string `json:"plan_digest"`
	PlanCheckpointSeq           int64  `json:"plan_checkpoint_seq"`
	ProjectedBundleDigest       string `json:"projected_bundle_digest"`
	PromotionReceiptDigest      string `json:"promotion_receipt_digest"`
	PipelineRenderInputDigest   string `json:"pipeline_render_input_digest,omitempty"`
	RenderContextSHA256         string `json:"render_context_sha256,omitempty"`
	EffectiveStyleReceiptDigest string `json:"effective_style_receipt_digest,omitempty"`
	SourceOutputDir             string `json:"source_output_dir"`
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
	Version                     string                        `json:"version"`
	CandidateManifestVersion    string                        `json:"candidate_manifest_version,omitempty"`
	CandidateID                 string                        `json:"candidate_id"`
	GenerationID                string                        `json:"generation_id"`
	Chapter                     int                           `json:"chapter"`
	PlanDigest                  string                        `json:"plan_digest"`
	PlanCheckpointSeq           int64                         `json:"plan_checkpoint_seq"`
	ProjectedBundleDigest       string                        `json:"projected_bundle_digest"`
	PromotionReceiptDigest      string                        `json:"promotion_receipt_digest"`
	PipelineRenderInputDigest   string                        `json:"pipeline_render_input_digest,omitempty"`
	RenderContextSHA256         string                        `json:"render_context_sha256,omitempty"`
	EffectiveStyleReceiptDigest string                        `json:"effective_style_receipt_digest,omitempty"`
	FailureLimit                int                           `json:"failure_limit"`
	Records                     []toolRenderConvergenceRecord `json:"records"`
	UpdatedAt                   string                        `json:"updated_at"`
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
	if (manifest.Version != toolRenderCandidateManifestVersion &&
		manifest.Version != toolRenderCandidatePreStyleManifestVersion &&
		manifest.Version != toolRenderCandidatePreviousManifestVersion) ||
		manifest.Chapter != chapter || manifest.PlanCheckpointSeq <= 0 ||
		strings.TrimSpace(manifest.PlanDigest) == "" ||
		!filepath.IsAbs(manifest.SourceOutputDir) ||
		manifest.SourceOutputDir != filepath.Clean(manifest.SourceOutputDir) ||
		strings.TrimSpace(manifest.CandidateID) == "" ||
		manifest.CandidateID == "." || manifest.CandidateID == ".." ||
		filepath.Base(manifest.CandidateID) != manifest.CandidateID ||
		strings.ContainsAny(manifest.CandidateID, `/\\`) {
		return nil, fmt.Errorf("render candidate convergence identity is invalid")
	}
	if manifest.Version == toolRenderCandidateManifestVersion &&
		(!validToolRenderIdentitySHA256(manifest.PipelineRenderInputDigest) ||
			!validToolRenderIdentitySHA256(manifest.RenderContextSHA256) ||
			!validToolRenderIdentitySHA256(manifest.EffectiveStyleReceiptDigest)) {
		return nil, fmt.Errorf("render candidate convergence effective-style identity is invalid")
	}
	if manifest.Version == toolRenderCandidatePreviousManifestVersion &&
		(strings.TrimSpace(manifest.PipelineRenderInputDigest) != "" ||
			strings.TrimSpace(manifest.RenderContextSHA256) != "" ||
			strings.TrimSpace(manifest.EffectiveStyleReceiptDigest) != "") {
		return nil, fmt.Errorf("render candidate convergence v2 identity contains v3 fields")
	}
	if manifest.Version == toolRenderCandidatePreStyleManifestVersion &&
		(!validToolRenderIdentitySHA256(manifest.PipelineRenderInputDigest) ||
			!validToolRenderIdentitySHA256(manifest.RenderContextSHA256) ||
			strings.TrimSpace(manifest.EffectiveStyleReceiptDigest) != "") {
		return nil, fmt.Errorf("render candidate convergence pre-style identity is incomplete")
	}
	// A promoted live tree retains this manifest only as provenance.
	if st.Dir() == manifest.SourceOutputDir {
		if err := validateToolRenderConvergenceDirectory("source output", manifest.SourceOutputDir); err != nil {
			return nil, err
		}
		return nil, nil
	}
	if _, err := authenticatedToolRenderConvergenceLedgerPath(st, &manifest); err != nil {
		return nil, err
	}
	plan, err := CurrentChapterPlanCheckpoint(st, chapter)
	if err != nil {
		return nil, fmt.Errorf("validate render convergence plan identity: %w", err)
	}
	if plan.Digest != manifest.PlanDigest || plan.Seq != manifest.PlanCheckpointSeq {
		return nil, fmt.Errorf("render convergence plan identity drifted")
	}
	if manifest.Version == toolRenderCandidateManifestVersion {
		_, receipt, _, err := LoadBoundArchivedEffectiveRenderStyleContract(st, chapter, manifest.PlanDigest)
		if err != nil {
			return nil, fmt.Errorf("validate render convergence effective style receipt: %w", err)
		}
		if receipt == nil ||
			receipt.ReceiptDigest != manifest.EffectiveStyleReceiptDigest ||
			receipt.PipelineRenderInputDigest != manifest.PipelineRenderInputDigest ||
			receipt.BaseRenderContextSHA256 != manifest.RenderContextSHA256 {
			return nil, fmt.Errorf("render convergence effective style receipt identity mismatch")
		}
	}
	return &manifest, nil
}

func validToolRenderIdentitySHA256(value string) bool {
	if len(value) != len("sha256:")+64 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	for _, r := range strings.TrimPrefix(value, "sha256:") {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

func authenticatedToolRenderConvergenceLedgerPath(
	st *store.Store,
	manifest *toolRenderCandidateManifest,
) (string, error) {
	if st == nil || manifest == nil ||
		!filepath.IsAbs(manifest.SourceOutputDir) ||
		manifest.SourceOutputDir != filepath.Clean(manifest.SourceOutputDir) ||
		manifest.CandidateID == "" || manifest.CandidateID == "." || manifest.CandidateID == ".." ||
		filepath.Base(manifest.CandidateID) != manifest.CandidateID ||
		strings.ContainsAny(manifest.CandidateID, `/\\`) ||
		!filepath.IsAbs(st.Dir()) || st.Dir() != filepath.Clean(st.Dir()) {
		return "", fmt.Errorf("render convergence paths require a clean absolute source and candidate output")
	}

	sourceOutput := manifest.SourceOutputDir
	namespace := filepath.Join(filepath.Dir(sourceOutput), ".render-candidates")
	container := filepath.Join(namespace, manifest.CandidateID)
	candidateOutput := filepath.Join(container, "output")
	if st.Dir() != candidateOutput {
		return "", fmt.Errorf("render convergence candidate output does not match its source manifest")
	}
	convergenceRoot := filepath.Join(namespace, "convergence")
	convergenceDir := filepath.Join(convergenceRoot, manifest.CandidateID)

	directories := []struct {
		name string
		path string
	}{
		{name: "source output", path: sourceOutput},
		{name: "candidate namespace", path: namespace},
		{name: "candidate container", path: container},
		{name: "candidate output", path: candidateOutput},
		{name: "convergence root", path: convergenceRoot},
		{name: "candidate convergence directory", path: convergenceDir},
	}
	resolved := make(map[string]string, len(directories))
	for _, directory := range directories {
		if err := validateToolRenderConvergenceDirectory(directory.name, directory.path); err != nil {
			return "", err
		}
		path, err := filepath.EvalSymlinks(directory.path)
		if err != nil {
			return "", fmt.Errorf("resolve render convergence %s: %w", directory.name, err)
		}
		resolved[directory.name] = filepath.Clean(path)
	}

	resolvedNamespace := filepath.Join(filepath.Dir(resolved["source output"]), ".render-candidates")
	resolvedContainer := filepath.Join(resolvedNamespace, manifest.CandidateID)
	resolvedConvergenceRoot := filepath.Join(resolvedNamespace, "convergence")
	if resolved["candidate namespace"] != resolvedNamespace ||
		resolved["candidate container"] != resolvedContainer ||
		resolved["candidate output"] != filepath.Join(resolvedContainer, "output") ||
		resolved["convergence root"] != resolvedConvergenceRoot ||
		resolved["candidate convergence directory"] != filepath.Join(resolvedConvergenceRoot, manifest.CandidateID) {
		return "", fmt.Errorf("render convergence paths escape their authenticated source namespace")
	}
	return filepath.Join(convergenceDir, "ledger.json"), nil
}

func validateToolRenderConvergenceDirectory(name, path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect render convergence %s: %w", name, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("render convergence %s must be a real directory", name)
	}
	return nil
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
	if manifest != nil && manifest.Version == toolRenderCandidatePreStyleManifestVersion {
		return nil, fmt.Errorf("pre-style render candidate cannot own a durable convergence ledger")
	}
	path, err := authenticatedToolRenderConvergenceLedgerPath(st, manifest)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &toolRenderConvergenceLedger{
			Version:                     toolRenderConvergenceLedgerVersion,
			CandidateManifestVersion:    manifest.Version,
			CandidateID:                 manifest.CandidateID,
			GenerationID:                manifest.GenerationID,
			Chapter:                     manifest.Chapter,
			PlanDigest:                  manifest.PlanDigest,
			PlanCheckpointSeq:           manifest.PlanCheckpointSeq,
			ProjectedBundleDigest:       manifest.ProjectedBundleDigest,
			PromotionReceiptDigest:      manifest.PromotionReceiptDigest,
			PipelineRenderInputDigest:   manifest.PipelineRenderInputDigest,
			RenderContextSHA256:         manifest.RenderContextSHA256,
			EffectiveStyleReceiptDigest: manifest.EffectiveStyleReceiptDigest,
			FailureLimit:                toolRenderConvergenceLimit(st, manifest.Chapter),
		}, nil
	}
	if err != nil {
		return nil, err
	}
	var ledger toolRenderConvergenceLedger
	if err := json.Unmarshal(raw, &ledger); err != nil {
		return nil, fmt.Errorf("decode plan-owned render convergence ledger: %w", err)
	}
	manifestVersionMatches := ledger.CandidateManifestVersion == manifest.Version ||
		(manifest.Version == toolRenderCandidatePreviousManifestVersion &&
			ledger.CandidateManifestVersion == "")
	if ledger.Version != toolRenderConvergenceLedgerVersion ||
		!manifestVersionMatches ||
		ledger.CandidateID != manifest.CandidateID ||
		ledger.GenerationID != manifest.GenerationID ||
		ledger.Chapter != manifest.Chapter ||
		ledger.PlanDigest != manifest.PlanDigest ||
		ledger.PlanCheckpointSeq != manifest.PlanCheckpointSeq ||
		ledger.ProjectedBundleDigest != manifest.ProjectedBundleDigest ||
		ledger.PromotionReceiptDigest != manifest.PromotionReceiptDigest ||
		ledger.PipelineRenderInputDigest != manifest.PipelineRenderInputDigest ||
		ledger.RenderContextSHA256 != manifest.RenderContextSHA256 ||
		ledger.EffectiveStyleReceiptDigest != manifest.EffectiveStyleReceiptDigest ||
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

func saveToolRenderConvergenceLedger(
	st *store.Store,
	manifest *toolRenderCandidateManifest,
	ledger *toolRenderConvergenceLedger,
) error {
	path, err := authenticatedToolRenderConvergenceLedgerPath(st, manifest)
	if err != nil {
		return err
	}
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
	if err := saveToolRenderConvergenceLedger(st, manifest, ledger); err != nil {
		return err
	}
	return SaveRenderConvergenceGuard(
		st,
		chapter,
		manifest.PlanDigest,
		toolRenderConvergenceFailedHashes(ledger),
	)
}
