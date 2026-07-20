package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/reviewreport"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/tools"
)

const pipelineRenderConvergenceLedgerVersion = "pipeline-render-convergence.v1"

// pipelineRenderConvergenceRecord is keyed by the exact chapter bytes.  One
// body can fail more than one gate, but it consumes only one plan attempt.
// Keeping this journal outside the disposable candidate directory makes the
// budget survive process restarts and candidate retirement/reconstruction.
type pipelineRenderConvergenceRecord struct {
	BodySHA256       string `json:"body_sha256"`
	WholeDraft       bool   `json:"whole_draft,omitempty"`
	Edited           bool   `json:"edited,omitempty"`
	ExternalJudged   bool   `json:"external_judged,omitempty"`
	ExternalBlocking bool   `json:"external_blocking,omitempty"`
	StructuralBlock  bool   `json:"structural_block,omitempty"`
	SemanticReject   bool   `json:"semantic_reject,omitempty"`
	// FormalAccepted never erases the historical reject. It records that the
	// same immutable body was subsequently re-normalized from its exact cached
	// Editor/DeepSeek responses and is now the authoritative formal decision.
	FormalAccepted bool `json:"formal_accepted,omitempty"`
}

type pipelineRenderConvergenceLedger struct {
	Version                string                            `json:"version"`
	CandidateID            string                            `json:"candidate_id"`
	GenerationID           string                            `json:"generation_id"`
	Chapter                int                               `json:"chapter"`
	PlanDigest             string                            `json:"plan_digest"`
	PlanCheckpointSeq      int64                             `json:"plan_checkpoint_seq"`
	ProjectedBundleDigest  string                            `json:"projected_bundle_digest"`
	PromotionReceiptDigest string                            `json:"promotion_receipt_digest"`
	FailureLimit           int                               `json:"failure_limit"`
	Records                []pipelineRenderConvergenceRecord `json:"records"`
	UpdatedAt              string                            `json:"updated_at"`
}

// pipelineRenderPlanStageRequiredError is intentionally typed.  The sealed
// candidate runner must retain the active tree when this condition is reached:
// the next safe mutation is a new plan epoch, not quarantine or another prose
// attempt.
type pipelineRenderPlanStageRequiredError struct {
	Chapter  int
	Attempts int
	Limit    int
	Reason   string
}

func (e *pipelineRenderPlanStageRequiredError) Error() string {
	if e == nil {
		return "render convergence budget exhausted"
	}
	return fmt.Sprintf(
		"第 %d 章同一冻结 plan 已有 %d 个不同 exact-body 稿件被 DeepSeek/本地结构门/正式 Editor 阻断，达到持久化上限 %d；当前候选、旧稿和全部反馈均原位保留，禁止 quarantine 或继续整章生成。下一步必须单独执行 --stages plan --restart 建立新 plan epoch 后再 render：%s",
		e.Chapter,
		e.Attempts,
		e.Limit,
		strings.TrimSpace(e.Reason),
	)
}

func pipelineRenderRequiresPlanStage(err error) bool {
	var target *pipelineRenderPlanStageRequiredError
	return errors.As(err, &target) ||
		pipelineRenderDispatchBudgetExhausted(err) ||
		tools.IsRenderConvergencePlanStageRequired(err)
}

func pipelineRenderConvergenceDir(liveOutputDir, candidateID string) (string, error) {
	if strings.TrimSpace(candidateID) == "" || filepath.Base(candidateID) != candidateID ||
		strings.ContainsAny(candidateID, `/\\`) {
		return "", fmt.Errorf("render convergence candidate id is malformed")
	}
	return filepath.Join(pipelineRenderCandidateRoot(liveOutputDir), "convergence", candidateID), nil
}

func pipelineRenderConvergenceLedgerPath(liveOutputDir, candidateID string) (string, error) {
	dir, err := pipelineRenderConvergenceDir(liveOutputDir, candidateID)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "ledger.json"), nil
}

// validatePipelineRenderConvergenceControlDir authenticates every directory
// component used by convergence and dispatch ledgers. In particular,
// .render-candidates/convergence must never be a symlink: it is a sibling
// control plane derived from the mutable candidate manifest and otherwise
// could redirect lock/ledger writes outside the live project's namespace.
func validatePipelineRenderConvergenceControlDir(
	liveOutputDir string,
	candidateID string,
	requireCandidateDir bool,
) (string, error) {
	dir, err := pipelineRenderConvergenceDir(liveOutputDir, candidateID)
	if err != nil {
		return "", err
	}
	namespace := pipelineRenderCandidateRoot(liveOutputDir)
	root := filepath.Dir(dir)
	for _, item := range []struct {
		name     string
		path     string
		required bool
	}{
		{name: "source output", path: liveOutputDir, required: true},
		{name: "candidate namespace", path: namespace, required: true},
		{name: "convergence root", path: root, required: requireCandidateDir},
		{name: "candidate convergence directory", path: dir, required: requireCandidateDir},
	} {
		info, statErr := os.Lstat(item.path)
		if os.IsNotExist(statErr) && !item.required {
			continue
		}
		if statErr != nil {
			return "", fmt.Errorf("inspect render %s: %w", item.name, statErr)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return "", fmt.Errorf("render %s must be a real directory", item.name)
		}
	}

	resolvedLive, err := filepath.EvalSymlinks(liveOutputDir)
	if err != nil {
		return "", fmt.Errorf("resolve render source output: %w", err)
	}
	expectedRoot := filepath.Join(pipelineRenderCandidateRoot(resolvedLive), "convergence")
	if _, err := os.Lstat(root); err == nil {
		resolvedRoot, resolveErr := filepath.EvalSymlinks(root)
		if resolveErr != nil {
			return "", fmt.Errorf("resolve render convergence root: %w", resolveErr)
		}
		if filepath.Clean(resolvedRoot) != filepath.Clean(expectedRoot) {
			return "", fmt.Errorf("render convergence root escapes its authenticated source namespace")
		}
	}
	if _, err := os.Lstat(dir); err == nil {
		resolvedDir, resolveErr := filepath.EvalSymlinks(dir)
		if resolveErr != nil {
			return "", fmt.Errorf("resolve render candidate convergence directory: %w", resolveErr)
		}
		expectedDir := filepath.Join(expectedRoot, candidateID)
		if filepath.Clean(resolvedDir) != filepath.Clean(expectedDir) {
			return "", fmt.Errorf("render candidate convergence directory escapes its authenticated source namespace")
		}
	}
	return dir, nil
}

func ensurePipelineRenderConvergenceControlDir(liveOutputDir, candidateID string) (string, error) {
	dir, err := validatePipelineRenderConvergenceControlDir(liveOutputDir, candidateID, false)
	if err != nil {
		return "", err
	}
	root := filepath.Dir(dir)
	if err := os.Mkdir(root, 0o755); err != nil && !os.IsExist(err) {
		return "", fmt.Errorf("create render convergence root: %w", err)
	}
	if err := os.Mkdir(dir, 0o755); err != nil && !os.IsExist(err) {
		return "", fmt.Errorf("create render candidate convergence directory: %w", err)
	}
	return validatePipelineRenderConvergenceControlDir(liveOutputDir, candidateID, true)
}

func pipelineRenderConvergenceLimit(st *store.Store, chapter int) int {
	limit := 3
	if st != nil && chapter > 0 {
		if plan, err := st.Drafts.LoadChapterPlan(chapter); err == nil && plan != nil &&
			plan.CausalSimulation.ReviewRefinement.IterationLimit > 0 {
			limit = plan.CausalSimulation.ReviewRefinement.IterationLimit
		}
	}
	if limit < 2 {
		return 2
	}
	if limit > 4 {
		return 4
	}
	return limit
}

func newPipelineRenderConvergenceLedger(
	manifest pipelineRenderCandidateManifest,
	limit int,
) pipelineRenderConvergenceLedger {
	return pipelineRenderConvergenceLedger{
		Version:                pipelineRenderConvergenceLedgerVersion,
		CandidateID:            manifest.CandidateID,
		GenerationID:           manifest.GenerationID,
		Chapter:                manifest.Chapter,
		PlanDigest:             manifest.PlanDigest,
		PlanCheckpointSeq:      manifest.PlanCheckpointSeq,
		ProjectedBundleDigest:  manifest.ProjectedBundleDigest,
		PromotionReceiptDigest: manifest.PromotionReceiptDigest,
		FailureLimit:           limit,
	}
}

func validatePipelineRenderConvergenceLedger(
	ledger *pipelineRenderConvergenceLedger,
	manifest pipelineRenderCandidateManifest,
) error {
	if ledger == nil || ledger.Version != pipelineRenderConvergenceLedgerVersion ||
		ledger.CandidateID != manifest.CandidateID ||
		ledger.GenerationID != manifest.GenerationID ||
		ledger.Chapter != manifest.Chapter ||
		ledger.PlanDigest != manifest.PlanDigest ||
		ledger.PlanCheckpointSeq != manifest.PlanCheckpointSeq ||
		ledger.ProjectedBundleDigest != manifest.ProjectedBundleDigest ||
		ledger.PromotionReceiptDigest != manifest.PromotionReceiptDigest ||
		ledger.FailureLimit < 2 || ledger.FailureLimit > 4 {
		return fmt.Errorf("render convergence ledger identity mismatch")
	}
	seen := map[string]struct{}{}
	for _, record := range ledger.Records {
		if !pipelineRenderExactBodySHA256(record.BodySHA256) {
			return fmt.Errorf("render convergence record body hash is malformed")
		}
		if _, ok := seen[record.BodySHA256]; ok {
			return fmt.Errorf("render convergence record body hash is duplicated")
		}
		seen[record.BodySHA256] = struct{}{}
	}
	return nil
}

func loadPipelineRenderConvergenceLedger(
	liveOutputDir string,
	manifest pipelineRenderCandidateManifest,
	limit int,
) (*pipelineRenderConvergenceLedger, error) {
	if _, err := validatePipelineRenderConvergenceControlDir(liveOutputDir, manifest.CandidateID, true); err != nil {
		return nil, err
	}
	path, err := pipelineRenderConvergenceLedgerPath(liveOutputDir, manifest.CandidateID)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		ledger := newPipelineRenderConvergenceLedger(manifest, limit)
		return &ledger, nil
	}
	if err != nil {
		return nil, err
	}
	var ledger pipelineRenderConvergenceLedger
	if err := json.Unmarshal(raw, &ledger); err != nil {
		return nil, fmt.Errorf("decode render convergence ledger: %w", err)
	}
	if err := validatePipelineRenderConvergenceLedger(&ledger, manifest); err != nil {
		return nil, err
	}
	return &ledger, nil
}

func savePipelineRenderConvergenceLedger(
	liveOutputDir string,
	ledger *pipelineRenderConvergenceLedger,
) error {
	if ledger == nil {
		return fmt.Errorf("render convergence ledger is nil")
	}
	if _, err := ensurePipelineRenderConvergenceControlDir(liveOutputDir, ledger.CandidateID); err != nil {
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
	raw = append(raw, '\n')
	path, err := pipelineRenderConvergenceLedgerPath(liveOutputDir, ledger.CandidateID)
	if err != nil {
		return err
	}
	return atomicWriteRewriteFile(path, raw, 0o644)
}

func pipelineRenderConvergenceRecordFor(
	ledger *pipelineRenderConvergenceLedger,
	bodySHA256 string,
) *pipelineRenderConvergenceRecord {
	for i := range ledger.Records {
		if ledger.Records[i].BodySHA256 == bodySHA256 {
			return &ledger.Records[i]
		}
	}
	ledger.Records = append(ledger.Records, pipelineRenderConvergenceRecord{BodySHA256: bodySHA256})
	return &ledger.Records[len(ledger.Records)-1]
}

func pipelineRenderConvergenceFailureCount(ledger *pipelineRenderConvergenceLedger) int {
	if ledger == nil {
		return 0
	}
	count := 0
	for _, record := range ledger.Records {
		if record.ExternalBlocking || record.StructuralBlock ||
			(record.SemanticReject && !record.FormalAccepted) {
			count++
		}
	}
	return count
}

func pipelineRenderConvergenceFailedHashes(ledger *pipelineRenderConvergenceLedger) []string {
	if ledger == nil {
		return nil
	}
	var hashes []string
	for _, record := range ledger.Records {
		if record.ExternalBlocking || record.StructuralBlock ||
			(record.SemanticReject && !record.FormalAccepted) {
			hashes = append(hashes, record.BodySHA256)
		}
	}
	sort.Strings(hashes)
	return hashes
}

// pipelineRenderBodyHasDurableConvergenceRejection reads the sibling control
// plane from the authenticated frozen identity, not from a candidate's mutable
// filesystem location. Retired candidates deliberately no longer satisfy the
// active-candidate topology contract, but their exact-body failure record must
// still quarantine those bytes during crash recovery.
//
// A missing ledger means that this older candidate has no convergence evidence.
// An existing malformed or redirected control plane fails closed: recovery must
// never turn an unreadable durable rejection into permission to replay prose.
func pipelineRenderBodyHasDurableConvergenceRejection(
	liveOutputDir string,
	frozen *pipelineFrozenPlan,
	candidateID string,
	bodySHA256 string,
) (bool, error) {
	if frozen == nil || !pipelineRenderExactBodySHA256(bodySHA256) {
		return false, fmt.Errorf("durable render rejection requires frozen identity and exact body hash")
	}
	wantID, err := pipelineRenderTransactionID(frozen)
	if err != nil {
		return false, err
	}
	if candidateID != wantID {
		return false, fmt.Errorf("durable render rejection candidate identity mismatch")
	}
	if _, err := validatePipelineRenderConvergenceControlDir(
		liveOutputDir,
		candidateID,
		false,
	); err != nil {
		return false, err
	}
	path, err := pipelineRenderConvergenceLedgerPath(liveOutputDir, candidateID)
	if err != nil {
		return false, err
	}
	if _, err := os.Lstat(path); os.IsNotExist(err) {
		return false, nil
	} else if err != nil {
		return false, fmt.Errorf("inspect exact render convergence ledger: %w", err)
	}
	manifest := pipelineRenderCandidateManifest{
		Version:                pipelineRenderCandidateManifestVersion,
		CandidateID:            candidateID,
		GenerationID:           frozen.PlanningGenerationID,
		Chapter:                frozen.Chapter,
		PlanDigest:             frozen.PlanDigest,
		PlanCheckpointSeq:      frozen.PlanCheckpointSeq,
		ProjectedBundleDigest:  frozen.ProjectedBundleDigest,
		PromotionReceiptDigest: frozen.PromotionReceiptDigest,
		SourceOutputDir:        filepath.Clean(liveOutputDir),
	}
	ledger, err := loadPipelineRenderConvergenceLedger(liveOutputDir, manifest, 3)
	if err != nil {
		return false, fmt.Errorf("load exact render convergence rejection: %w", err)
	}
	for _, record := range ledger.Records {
		if record.BodySHA256 != bodySHA256 {
			continue
		}
		return record.ExternalBlocking || record.StructuralBlock ||
			(record.SemanticReject && !record.FormalAccepted), nil
	}
	return false, nil
}

func pipelineRenderConvergenceHasPendingFormalRevalidation(
	ledger *pipelineRenderConvergenceLedger,
) bool {
	if ledger == nil {
		return false
	}
	for _, record := range ledger.Records {
		if record.SemanticReject && !record.FormalAccepted &&
			!record.ExternalBlocking && !record.StructuralBlock {
			return true
		}
	}
	return false
}

// pipelineRenderConvergencePreflightError permits only the cache-only formal
// revalidation window. The strict check still runs inside the candidate before
// Writer, so a missing cache or a repeated reject cannot reopen prose budget.
func pipelineRenderConvergencePreflightError(ledger *pipelineRenderConvergenceLedger) error {
	err := pipelineRenderConvergenceError(ledger)
	if err != nil && pipelineRenderConvergenceHasPendingFormalRevalidation(ledger) {
		return nil
	}
	return err
}

func pipelineRenderConvergenceError(ledger *pipelineRenderConvergenceLedger) error {
	attempts := pipelineRenderConvergenceFailureCount(ledger)
	if ledger == nil || attempts < ledger.FailureLimit {
		return nil
	}
	return &pipelineRenderPlanStageRequiredError{
		Chapter:  ledger.Chapter,
		Attempts: attempts,
		Limit:    ledger.FailureLimit,
		Reason:   "旧 plan 的表达层整章返工已不再收敛；质量阈值保持不变，由 plan 阶段重组场景因果与表达资源",
	}
}

func loadPipelineRenderCandidateManifest(outputDir string) (*pipelineRenderCandidateManifest, error) {
	path := filepath.Join(outputDir, "meta", "planning", "render_candidate.json")
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var manifest pipelineRenderCandidateManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return nil, fmt.Errorf("decode render candidate manifest for convergence: %w", err)
	}
	if manifest.Version != pipelineRenderCandidateManifestVersion ||
		manifest.Chapter <= 0 || manifest.PlanCheckpointSeq <= 0 ||
		strings.TrimSpace(manifest.SourceOutputDir) == "" ||
		!filepath.IsAbs(manifest.SourceOutputDir) ||
		manifest.SourceOutputDir != filepath.Clean(manifest.SourceOutputDir) {
		return nil, fmt.Errorf("render candidate manifest cannot own a convergence ledger")
	}
	if filepath.Clean(outputDir) != filepath.Clean(manifest.SourceOutputDir) {
		if _, err := validateActivePipelineRenderCandidateTopology(outputDir, &manifest); err != nil {
			return nil, err
		}
	}
	return &manifest, nil
}

// validateActivePipelineRenderCandidateTopology authenticates the filesystem
// namespace before any convergence or dispatch ledger path is derived from the
// mutable candidate manifest. Published-live manifests are handled by the
// caller and never enter this active-candidate path.
func validateActivePipelineRenderCandidateTopology(
	candidateOutputDir string,
	manifest *pipelineRenderCandidateManifest,
) (string, error) {
	if manifest == nil {
		return "", fmt.Errorf("active render candidate manifest is nil")
	}
	candidateOutputDir = strings.TrimSpace(candidateOutputDir)
	liveOutputDir := strings.TrimSpace(manifest.SourceOutputDir)
	if candidateOutputDir == "" || liveOutputDir == "" ||
		!filepath.IsAbs(candidateOutputDir) || !filepath.IsAbs(liveOutputDir) ||
		candidateOutputDir != filepath.Clean(candidateOutputDir) ||
		liveOutputDir != filepath.Clean(liveOutputDir) {
		return "", fmt.Errorf("active render candidate and source paths must be clean absolute paths")
	}
	if _, err := pipelineRenderConvergenceDir(liveOutputDir, manifest.CandidateID); err != nil {
		return "", err
	}
	expectedCandidate := filepath.Join(
		pipelineRenderCandidateRoot(liveOutputDir),
		manifest.CandidateID,
		"output",
	)
	if candidateOutputDir != expectedCandidate {
		return "", fmt.Errorf("active render candidate path is outside its authenticated source namespace")
	}

	namespace := pipelineRenderCandidateRoot(liveOutputDir)
	for _, item := range []struct {
		name string
		path string
	}{
		{name: "source output", path: liveOutputDir},
		{name: "candidate namespace", path: namespace},
		{name: "candidate container", path: filepath.Join(namespace, manifest.CandidateID)},
		{name: "candidate output", path: candidateOutputDir},
	} {
		info, err := os.Lstat(item.path)
		if err != nil {
			return "", fmt.Errorf("inspect active render %s: %w", item.name, err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return "", fmt.Errorf("active render %s must be a real directory", item.name)
		}
	}

	resolvedLive, err := filepath.EvalSymlinks(liveOutputDir)
	if err != nil {
		return "", fmt.Errorf("resolve active render source output: %w", err)
	}
	resolvedCandidate, err := filepath.EvalSymlinks(candidateOutputDir)
	if err != nil {
		return "", fmt.Errorf("resolve active render candidate output: %w", err)
	}
	resolvedExpected := filepath.Join(
		pipelineRenderCandidateRoot(resolvedLive),
		manifest.CandidateID,
		"output",
	)
	if filepath.Clean(resolvedCandidate) != filepath.Clean(resolvedExpected) {
		return "", fmt.Errorf("resolved active render candidate escapes its authenticated source namespace")
	}
	liveInfo, err := os.Stat(resolvedLive)
	if err != nil {
		return "", fmt.Errorf("stat active render source output: %w", err)
	}
	candidateInfo, err := os.Stat(resolvedCandidate)
	if err != nil {
		return "", fmt.Errorf("stat active render candidate output: %w", err)
	}
	if os.SameFile(liveInfo, candidateInfo) {
		return "", fmt.Errorf("active render candidate aliases live canon")
	}
	if _, err := validatePipelineRenderConvergenceControlDir(liveOutputDir, manifest.CandidateID, false); err != nil {
		return "", err
	}
	return liveOutputDir, nil
}

// A promoted candidate keeps render_candidate.json as provenance in the live
// tree. It is not an active isolated candidate: SourceOutputDir now names the
// store that contains the manifest. Treating this residue as active makes the
// next chapter inherit the previous chapter's convergence ledger.
func pipelineRenderConvergenceManifestIsPublishedLive(
	st *store.Store,
	manifest *pipelineRenderCandidateManifest,
) bool {
	return st != nil && manifest != nil &&
		filepath.Clean(st.Dir()) == filepath.Clean(manifest.SourceOutputDir)
}

// syncPipelineRenderConvergence records only durable exact-body facts.  It is
// safe to call before and after every Host turn; repeated calls for one hash
// only OR flags into the same record.
func syncPipelineRenderConvergence(st *store.Store) (*pipelineRenderConvergenceLedger, error) {
	if st == nil {
		return nil, nil
	}
	manifest, err := loadPipelineRenderCandidateManifest(st.Dir())
	if err != nil || manifest == nil {
		return nil, err
	}
	if pipelineRenderConvergenceManifestIsPublishedLive(st, manifest) {
		return nil, nil
	}
	liveOutputDir := filepath.Clean(manifest.SourceOutputDir)
	if _, err := ensurePipelineRenderConvergenceControlDir(liveOutputDir, manifest.CandidateID); err != nil {
		return nil, err
	}
	limit := pipelineRenderConvergenceLimit(st, manifest.Chapter)
	ledger, err := loadPipelineRenderConvergenceLedger(liveOutputDir, *manifest, limit)
	if err != nil {
		return nil, err
	}
	changed, err := backfillPipelineRenderSemanticRejections(liveOutputDir, *manifest, ledger)
	if err != nil {
		return nil, err
	}
	scope := domain.ChapterScope(manifest.Chapter)
	latestExactBodySHA := ""
	for _, cp := range st.Checkpoints.All() {
		if cp.Seq <= manifest.PlanCheckpointSeq || !cp.Scope.Matches(scope) {
			continue
		}
		switch cp.Step {
		case "draft", "edit":
			bodySHA := strings.TrimPrefix(strings.TrimSpace(cp.Digest), domain.PlanningV2DigestPrefix)
			if !pipelineRenderExactBodySHA256(bodySHA) {
				latestExactBodySHA = ""
				continue
			}
			latestExactBodySHA = bodySHA
			record := pipelineRenderConvergenceRecordFor(ledger, bodySHA)
			if cp.Step == "draft" && !record.WholeDraft {
				record.WholeDraft = true
				changed = true
			}
			if cp.Step == "edit" && !record.Edited {
				record.Edited = true
				changed = true
			}
		case "draft-structural-block":
			// Current builds bind this checkpoint digest to bodyHash+planEpoch,
			// which is intentionally one-way. Its journal position is the durable
			// association: it rejects the nearest preceding exact draft/edit body
			// in the same chapter scope and frozen plan epoch.
			if !pipelineRenderExactBodySHA256(latestExactBodySHA) {
				continue
			}
			record := pipelineRenderConvergenceRecordFor(ledger, latestExactBodySHA)
			if !record.StructuralBlock {
				record.StructuralBlock = true
				changed = true
			}
		}
	}

	body, bodyErr := st.Drafts.LoadDraft(manifest.Chapter)
	if bodyErr == nil && strings.TrimSpace(body) != "" {
		bodySHA := reviewreport.BodySHA256(body)
		record := pipelineRenderConvergenceRecordFor(ledger, bodySHA)
		if tools.CurrentDraftHasLocalStructuralBlock(st, manifest.Chapter) && !record.StructuralBlock {
			record.StructuralBlock = true
			changed = true
		}
		artifact, artifactErr := loadPipelineCurrentDraftJudgeArtifact(st.Dir(), manifest.Chapter, bodySHA)
		if artifactErr != nil {
			return nil, artifactErr
		}
		if artifact != nil {
			if !record.ExternalJudged {
				record.ExternalJudged = true
				changed = true
			}
			blocking := artifact.Blocking || artifact.AIProbabilityPercent >= deepseekAIJudgePassExclusive
			if blocking && !record.ExternalBlocking {
				record.ExternalBlocking = true
				changed = true
			}
			if err := snapshotPipelineRenderJudgeEvidence(liveOutputDir, *manifest, st.Dir(), bodySHA, artifact); err != nil {
				return nil, err
			}
		}
		if record.SemanticReject && !record.FormalAccepted &&
			requirePipelineAcceptedExactReview(st.Dir(), manifest.Chapter) == nil {
			record.FormalAccepted = true
			changed = true
		}
	}
	if changed {
		if err := savePipelineRenderConvergenceLedger(liveOutputDir, ledger); err != nil {
			return nil, err
		}
	}
	if err := tools.SaveRenderConvergenceGuard(
		st,
		manifest.Chapter,
		manifest.PlanDigest,
		pipelineRenderConvergenceFailedHashes(ledger),
	); err != nil {
		return nil, err
	}
	return ledger, nil
}

func backfillPipelineRenderSemanticRejections(
	liveOutputDir string,
	manifest pipelineRenderCandidateManifest,
	ledger *pipelineRenderConvergenceLedger,
) (bool, error) {
	dir := filepath.Join(
		pipelineRenderCandidateRoot(liveOutputDir),
		"rejections",
		manifest.CandidateID,
	)
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	changed := false
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".json") {
			continue
		}
		bodySHA := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
		if !pipelineRenderExactBodySHA256(bodySHA) {
			return false, fmt.Errorf("render semantic rejection filename is malformed: %s", entry.Name())
		}
		raw, readErr := os.ReadFile(filepath.Join(dir, entry.Name()))
		if readErr != nil {
			return false, readErr
		}
		var tombstone pipelineRenderRejectionTombstone
		if json.Unmarshal(raw, &tombstone) != nil ||
			tombstone.Version != pipelineRenderRejectionTombstoneVersion ||
			tombstone.CandidateID != manifest.CandidateID ||
			tombstone.GenerationID != manifest.GenerationID ||
			tombstone.Chapter != manifest.Chapter ||
			tombstone.PlanDigest != manifest.PlanDigest ||
			tombstone.PlanCheckpointSeq != manifest.PlanCheckpointSeq ||
			tombstone.ProjectedBundleDigest != manifest.ProjectedBundleDigest ||
			tombstone.PromotionReceiptDigest != manifest.PromotionReceiptDigest ||
			tombstone.BodySHA256 != bodySHA ||
			strings.TrimSpace(tombstone.Verdict) == "" ||
			strings.TrimSpace(tombstone.Disposition) == "" ||
			pipelineReviewAcceptedForProjection(tombstone.Verdict, tombstone.Disposition) ||
			!pipelineRenderHasCompleteReviewArtifacts(tombstone.Chapter, tombstone.ReviewArtifacts) {
			return false, fmt.Errorf("render semantic rejection tombstone identity mismatch: %s", entry.Name())
		}
		if _, parseErr := time.Parse(time.RFC3339Nano, tombstone.RejectedAt); parseErr != nil {
			return false, fmt.Errorf("render semantic rejection timestamp is invalid: %w", parseErr)
		}
		record := pipelineRenderConvergenceRecordFor(ledger, bodySHA)
		if !record.SemanticReject {
			record.SemanticReject = true
			changed = true
		}
	}
	return changed, nil
}

func loadPipelineCurrentDraftJudgeArtifact(
	outputDir string,
	chapter int,
	bodySHA256 string,
) (*deepseekAIJudgeArtifact, error) {
	path := filepath.Join(outputDir, "reviews", "drafts", fmt.Sprintf("%02d_deepseek_ai_judge.json", chapter))
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var artifact deepseekAIJudgeArtifact
	if err := json.Unmarshal(raw, &artifact); err != nil {
		return nil, fmt.Errorf("decode current draft judge for convergence: %w", err)
	}
	if artifact.BodySHA256 != bodySHA256 {
		return nil, nil
	}
	if err := validateDeepSeekAIJudgeArtifactIdentity(&artifact, artifact.CachePolicy); err != nil {
		return nil, fmt.Errorf("validate current draft judge for convergence: %w", err)
	}
	if !artifact.AdviceComplete {
		return nil, nil
	}
	return &artifact, nil
}

func pipelineRenderConvergenceEvidenceDir(
	liveOutputDir string,
	manifest pipelineRenderCandidateManifest,
	bodySHA256 string,
) (string, error) {
	if !pipelineRenderExactBodySHA256(bodySHA256) {
		return "", fmt.Errorf("render convergence evidence body hash is malformed")
	}
	dir, err := pipelineRenderConvergenceDir(liveOutputDir, manifest.CandidateID)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "evidence", bodySHA256), nil
}

func snapshotPipelineRenderJudgeEvidence(
	liveOutputDir string,
	manifest pipelineRenderCandidateManifest,
	outputDir string,
	bodySHA256 string,
	artifact *deepseekAIJudgeArtifact,
) error {
	if artifact == nil || artifact.BodySHA256 != bodySHA256 {
		return nil
	}
	dir, err := pipelineRenderConvergenceEvidenceDir(liveOutputDir, manifest, bodySHA256)
	if err != nil {
		return err
	}
	files := []string{
		filepath.Join("reviews", "drafts", fmt.Sprintf("%02d_deepseek_ai_judge.json", manifest.Chapter)),
		filepath.Join("reviews", "drafts", fmt.Sprintf("%02d_deepseek_ai_judge.md", manifest.Chapter)),
		filepath.Join("reviews", "drafts", fmt.Sprintf("%02d_full_rerender_required.json", manifest.Chapter)),
	}
	for _, rel := range files {
		if err := copyPipelineRenderConvergenceFileIfPresent(
			filepath.Join(outputDir, rel), filepath.Join(dir, rel),
		); err != nil {
			return err
		}
	}
	cacheDir := filepath.Join(outputDir, "reviews", "cache", deepseekAIJudgeCacheBranch)
	entries, readErr := os.ReadDir(cacheDir)
	if readErr != nil && !os.IsNotExist(readErr) {
		return readErr
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".json") {
			continue
		}
		raw, readErr := os.ReadFile(filepath.Join(cacheDir, entry.Name()))
		if readErr != nil {
			return readErr
		}
		var cached deepseekAIJudgeArtifact
		if json.Unmarshal(raw, &cached) != nil || cached.BodySHA256 != bodySHA256 ||
			validateDeepSeekAIJudgeCacheArtifact(&cached, cached.CachePolicy) != nil {
			continue
		}
		if err := atomicWriteRewriteFile(
			filepath.Join(dir, "reviews", "cache", deepseekAIJudgeCacheBranch, entry.Name()),
			raw,
			0o644,
		); err != nil {
			return err
		}
	}
	return nil
}

func copyPipelineRenderConvergenceFileIfPresent(source, target string) error {
	raw, err := os.ReadFile(source)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	return atomicWriteRewriteFile(target, raw, 0o644)
}

// restorePipelineRenderConvergenceJudgeEvidence makes an old exact hash a
// cache hit if a model later reproduces it.  The response and its one-shot
// marker are exact-body evidence; no quality threshold is relaxed.
func restorePipelineRenderConvergenceJudgeEvidence(st *store.Store, chapter int) error {
	if st == nil || chapter <= 0 {
		return nil
	}
	manifest, err := loadPipelineRenderCandidateManifest(st.Dir())
	if err != nil || manifest == nil || manifest.Chapter != chapter {
		return err
	}
	if pipelineRenderConvergenceManifestIsPublishedLive(st, manifest) {
		return nil
	}
	body, err := st.Drafts.LoadDraft(chapter)
	if err != nil || strings.TrimSpace(body) == "" {
		return err
	}
	bodySHA := reviewreport.BodySHA256(body)
	dir, err := pipelineRenderConvergenceEvidenceDir(manifest.SourceOutputDir, *manifest, bodySHA)
	if err != nil {
		return err
	}
	judgeRel := filepath.Join("reviews", "drafts", fmt.Sprintf("%02d_deepseek_ai_judge.json", chapter))
	judgeRaw, err := os.ReadFile(filepath.Join(dir, judgeRel))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var artifact deepseekAIJudgeArtifact
	if err := json.Unmarshal(judgeRaw, &artifact); err != nil || artifact.BodySHA256 != bodySHA ||
		validateDeepSeekAIJudgeArtifactIdentity(&artifact, artifact.CachePolicy) != nil ||
		!artifact.AdviceComplete {
		return fmt.Errorf("render convergence exact-body judge evidence is invalid")
	}
	if err := atomicWriteRewriteFile(filepath.Join(st.Dir(), judgeRel), judgeRaw, 0o644); err != nil {
		return err
	}
	for _, rel := range []string{
		filepath.Join("reviews", "drafts", fmt.Sprintf("%02d_deepseek_ai_judge.md", chapter)),
		filepath.Join("reviews", "drafts", fmt.Sprintf("%02d_full_rerender_required.json", chapter)),
	} {
		if err := copyPipelineRenderConvergenceFileIfPresent(filepath.Join(dir, rel), filepath.Join(st.Dir(), rel)); err != nil {
			return err
		}
	}
	cacheDir := filepath.Join(dir, "reviews", "cache", deepseekAIJudgeCacheBranch)
	entries, readErr := os.ReadDir(cacheDir)
	if readErr != nil && !os.IsNotExist(readErr) {
		return readErr
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".json") {
			continue
		}
		raw, readErr := os.ReadFile(filepath.Join(cacheDir, entry.Name()))
		if readErr != nil {
			return readErr
		}
		var cached deepseekAIJudgeArtifact
		if json.Unmarshal(raw, &cached) != nil || cached.BodySHA256 != bodySHA ||
			validateDeepSeekAIJudgeCacheArtifact(&cached, cached.CachePolicy) != nil {
			return fmt.Errorf("render convergence exact-body judge cache is invalid")
		}
		if err := atomicWriteRewriteFile(
			filepath.Join(st.Dir(), "reviews", "cache", deepseekAIJudgeCacheBranch, entry.Name()),
			raw,
			0o644,
		); err != nil {
			return err
		}
	}
	return nil
}

func recordPipelineRenderSemanticRejection(
	liveOutputDir string,
	candidateOutputDir string,
	frozen *pipelineFrozenPlan,
	bodySHA256 string,
) (*pipelineRenderConvergenceLedger, error) {
	st := store.NewStore(candidateOutputDir)
	ledger, err := syncPipelineRenderConvergence(st)
	if err != nil {
		return nil, err
	}
	if ledger == nil || frozen == nil || ledger.PlanDigest != frozen.PlanDigest ||
		ledger.PlanCheckpointSeq != frozen.PlanCheckpointSeq {
		return nil, fmt.Errorf("semantic rejection convergence identity mismatch")
	}
	record := pipelineRenderConvergenceRecordFor(ledger, bodySHA256)
	if !record.SemanticReject || record.FormalAccepted {
		record.SemanticReject = true
		record.FormalAccepted = false
		if err := savePipelineRenderConvergenceLedger(liveOutputDir, ledger); err != nil {
			return nil, err
		}
		if err := tools.SaveRenderConvergenceGuard(
			st,
			ledger.Chapter,
			ledger.PlanDigest,
			pipelineRenderConvergenceFailedHashes(ledger),
		); err != nil {
			return nil, err
		}
	}
	// Copy the complete formal feedback set into the plan-owned evidence store.
	// This is a backup/reuse source, never a replacement for fresh exact-body
	// validation.
	manifest, err := loadPipelineRenderCandidateManifest(candidateOutputDir)
	if err != nil || manifest == nil {
		return nil, err
	}
	evidenceDir, err := pipelineRenderConvergenceEvidenceDir(liveOutputDir, *manifest, bodySHA256)
	if err != nil {
		return nil, err
	}
	for _, rel := range append(
		pipelineRenderRequiredReviewArtifacts(frozen.Chapter),
		fmt.Sprintf("reviews/%02d_rewrite_brief.md", frozen.Chapter),
	) {
		if err := copyPipelineRenderConvergenceFileIfPresent(
			filepath.Join(candidateOutputDir, filepath.FromSlash(rel)),
			filepath.Join(evidenceDir, filepath.FromSlash(rel)),
		); err != nil {
			return nil, err
		}
	}
	return ledger, nil
}

func requirePipelineRenderConvergenceAvailable(st *store.Store) error {
	if st == nil {
		return nil
	}
	if err := restorePipelineRenderConvergenceJudgeEvidence(st, func() int {
		manifest, _ := loadPipelineRenderCandidateManifest(st.Dir())
		if manifest == nil {
			return 0
		}
		return manifest.Chapter
	}()); err != nil {
		return err
	}
	ledger, err := syncPipelineRenderConvergence(st)
	if err != nil {
		return err
	}
	return pipelineRenderConvergenceError(ledger)
}

func requireFrozenPipelineRenderConvergenceAvailable(
	liveOutputDir string,
	frozen *pipelineFrozenPlan,
) error {
	if frozen == nil || frozen.ProjectionBinding != "sealed_v2" {
		return nil
	}
	id, err := pipelineRenderTransactionID(frozen)
	if err != nil {
		return err
	}
	manifest := pipelineRenderCandidateManifest{
		Version:                pipelineRenderCandidateManifestVersion,
		CandidateID:            id,
		GenerationID:           frozen.PlanningGenerationID,
		Chapter:                frozen.Chapter,
		PlanDigest:             frozen.PlanDigest,
		PlanCheckpointSeq:      frozen.PlanCheckpointSeq,
		ProjectedBundleDigest:  frozen.ProjectedBundleDigest,
		PromotionReceiptDigest: frozen.PromotionReceiptDigest,
		SourceOutputDir:        filepath.Clean(liveOutputDir),
	}
	path, err := pipelineRenderConvergenceLedgerPath(liveOutputDir, id)
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	ledger, err := loadPipelineRenderConvergenceLedger(liveOutputDir, manifest, 3)
	if err != nil {
		return err
	}
	return pipelineRenderConvergencePreflightError(ledger)
}

// pipelineRenderBodyHasEffectiveSemanticRejection keeps immutable tombstones
// as audit evidence while allowing a later exact-body cached formal accept to
// supersede their operational effect during crash recovery.
func pipelineRenderBodyHasEffectiveSemanticRejection(
	liveOutputDir string,
	frozen *pipelineFrozenPlan,
	candidateID string,
	bodySHA256 string,
) (bool, error) {
	rejected, err := pipelineRenderBodyHasSemanticRejection(
		liveOutputDir,
		frozen,
		candidateID,
		bodySHA256,
	)
	if err != nil || !rejected {
		return rejected, err
	}
	path, err := pipelineRenderConvergenceLedgerPath(liveOutputDir, candidateID)
	if err != nil {
		return false, err
	}
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	var ledger pipelineRenderConvergenceLedger
	if err := json.Unmarshal(raw, &ledger); err != nil {
		return false, fmt.Errorf("decode render convergence resolution: %w", err)
	}
	if frozen == nil || ledger.Version != pipelineRenderConvergenceLedgerVersion ||
		ledger.CandidateID != candidateID ||
		ledger.GenerationID != frozen.PlanningGenerationID ||
		ledger.Chapter != frozen.Chapter ||
		ledger.PlanDigest != frozen.PlanDigest ||
		ledger.PlanCheckpointSeq != frozen.PlanCheckpointSeq ||
		ledger.ProjectedBundleDigest != frozen.ProjectedBundleDigest ||
		ledger.PromotionReceiptDigest != frozen.PromotionReceiptDigest {
		return false, fmt.Errorf("render convergence resolution identity mismatch")
	}
	for _, record := range ledger.Records {
		if record.BodySHA256 == bodySHA256 && record.SemanticReject && record.FormalAccepted {
			return false, nil
		}
	}
	return true, nil
}
