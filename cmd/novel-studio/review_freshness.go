package main

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/aigc"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/reviewreport"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/tools"
)

type currentChapterReviewEvidence struct {
	Chapter                      int
	BodySHA256                   string
	Verdict                      string
	Disposition                  string
	Artifacts                    []string
	Issues                       []string
	EditorCacheKey               string
	DeepSeekCacheKey             string
	EditorContextSHA256          string
	EffectiveStyleReceiptPath    string
	EffectiveStyleReceiptHash    string
	EffectiveStyleArtifactSHA256 string
}

const reviewModelProvenanceVersion = "review-model-provenance.v2-artifact-set"

type reviewModelArtifactBinding struct {
	Role   string `json:"role"`
	Branch string `json:"branch"`
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

type reviewModelProvenance struct {
	Version                      string                       `json:"version"`
	Chapter                      int                          `json:"chapter"`
	BodySHA256                   string                       `json:"body_sha256"`
	EditorCacheKey               string                       `json:"editor_cache_key"`
	EditorCachePolicy            reviewExistingCachePolicy    `json:"editor_cache_policy"`
	DeepSeekCacheKey             string                       `json:"deepseek_cache_key"`
	DeepSeekCachePolicy          reviewExistingCachePolicy    `json:"deepseek_cache_policy"`
	EffectiveStyleReceiptPath    string                       `json:"effective_style_receipt_path,omitempty"`
	EffectiveStyleReceiptDigest  string                       `json:"effective_style_receipt_digest,omitempty"`
	EffectiveStyleArtifactSHA256 string                       `json:"effective_style_artifact_sha256,omitempty"`
	StyleContractSHA256          string                       `json:"style_contract_sha256,omitempty"`
	Artifacts                    []reviewModelArtifactBinding `json:"artifacts"`
}

type reviewEffectiveStyleRequirement struct {
	ManifestVersion string
	PlanDigest      string
	Required        bool
	ReceiptDigest   string
}

func loadReviewEffectiveStyleRequirement(st *store.Store, chapter int) (reviewEffectiveStyleRequirement, error) {
	var requirement reviewEffectiveStyleRequirement
	if st == nil || chapter <= 0 {
		return requirement, fmt.Errorf("review effective style requirement needs store and chapter")
	}
	lock, err := st.Runtime.LoadPipelineExecution()
	if err != nil {
		return requirement, fmt.Errorf("load render execution for review style requirement: %w", err)
	}
	activeRender := lock != nil && lock.Mode == domain.PipelineExecutionRender && lock.TargetChapter == chapter
	path := filepath.Join(st.Dir(), "meta", "planning", "render_candidate.json")
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		if activeRender {
			return requirement, fmt.Errorf("active render is missing render_candidate.json")
		}
		if err := validateReviewStyleRequirementWithoutOwningManifest(
			st, chapter, "render_candidate.json is missing",
		); err != nil {
			return requirement, err
		}
		return requirement, nil
	}
	if err != nil {
		return requirement, fmt.Errorf("read render candidate for review style requirement: %w", err)
	}
	var manifest pipelineRenderCandidateManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return requirement, fmt.Errorf("decode render candidate for review style requirement: %w", err)
	}
	// A published project can retain the latest candidate manifest while older
	// chapters are inspected for delivery. Only the manifest that owns this
	// chapter is authoritative for its style protocol.
	if manifest.Chapter != chapter {
		if activeRender {
			return requirement, fmt.Errorf("active render candidate belongs to chapter %d, want %d", manifest.Chapter, chapter)
		}
		if err := validateReviewStyleRequirementWithoutOwningManifest(
			st,
			chapter,
			fmt.Sprintf("render_candidate.json belongs to chapter %d", manifest.Chapter),
		); err != nil {
			return requirement, err
		}
		return requirement, nil
	}
	requirement.ManifestVersion = strings.TrimSpace(manifest.Version)
	requirement.PlanDigest = strings.TrimSpace(manifest.PlanDigest)
	if activeRender && requirement.PlanDigest != strings.TrimSpace(lock.PlanDigest) {
		return requirement, fmt.Errorf("active render candidate plan does not match execution lock")
	}
	required, digest, err := tools.EffectiveRenderStyleContractRequired(
		st, chapter, requirement.PlanDigest,
	)
	if err != nil {
		return requirement, err
	}
	requirement.Required = required
	requirement.ReceiptDigest = digest
	trackedStyleDigest, tracked, err := reviewTrackedBodyEffectiveStyleDigest(st, &manifest)
	if err != nil {
		return requirement, err
	}
	if tracked {
		if trackedStyleDigest != "" && (!required || trackedStyleDigest != digest) {
			return requirement, fmt.Errorf("v3 body_ready style receipt was downgraded or replaced by the candidate manifest")
		}
		if trackedStyleDigest == "" && required {
			return requirement, fmt.Errorf("legacy body_ready transaction was relabeled as a v3 style candidate")
		}
	}
	return requirement, nil
}

// validateReviewStyleRequirementWithoutOwningManifest preserves the historical
// live-project fallback only when no still-open v3 body transaction proves
// that this exact chapter body consumed an immutable effective-style receipt.
// A completed chapter remains readable through its immutable review
// provenance; an in-flight body must retain its owning manifest before any
// Editor/Reviewer provider can be reached.
func validateReviewStyleRequirementWithoutOwningManifest(
	st *store.Store,
	chapter int,
	reason string,
) error {
	if st == nil || chapter <= 0 {
		return fmt.Errorf("review style manifest fallback requires store and chapter")
	}
	outputDir, err := filepath.Abs(st.Dir())
	if err != nil {
		return fmt.Errorf("resolve review style fallback output: %w", err)
	}
	outputDir = filepath.Clean(outputDir)
	if reviewOutputUsesRenderCandidateTopology(outputDir) {
		return fmt.Errorf("isolated render candidate cannot review without its owning chapter manifest: %s", reason)
	}
	bodyPath := filepath.Join(outputDir, "chapters", fmt.Sprintf("%02d.md", chapter))
	info, err := os.Lstat(bodyPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect chapter body for review style fallback: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("chapter body for review style fallback must be a real file")
	}
	body, err := os.ReadFile(bodyPath)
	if err != nil {
		return fmt.Errorf("read chapter body for review style fallback: %w", err)
	}
	evidence, err := inspectPipelineRenderV3BodyStyleEvidence(outputDir, chapter, body)
	if err != nil {
		return fmt.Errorf("inspect durable v3 review style evidence: %w", err)
	}
	if evidence == nil || evidence.ChapterAccepted {
		return nil
	}
	return fmt.Errorf(
		"%s, but exact body_ready for CandidateID %s durably requires v3 style receipt %s",
		reason,
		evidence.CandidateID,
		evidence.ReceiptDigest,
	)
}

func reviewOutputUsesRenderCandidateTopology(outputDir string) bool {
	if filepath.Base(outputDir) != "output" {
		return false
	}
	container := filepath.Dir(outputDir)
	parent := filepath.Dir(container)
	if filepath.Base(parent) == ".render-candidates" {
		return true
	}
	return filepath.Base(parent) == "retired" &&
		filepath.Base(filepath.Dir(parent)) == ".render-candidates"
}

func reviewTrackedBodyEffectiveStyleDigest(
	st *store.Store,
	manifest *pipelineRenderCandidateManifest,
) (string, bool, error) {
	if st == nil || manifest == nil || manifest.Chapter <= 0 {
		return "", false, nil
	}
	sourceOutputDir, isolated, err := resolvePipelineRenderCandidateSourceOutput(st.Dir(), manifest)
	if err != nil {
		return "", false, err
	}
	if isolated {
		topologyManifest := *manifest
		topologyManifest.SourceOutputDir = sourceOutputDir
		if _, err := validateReviewPipelineRenderCandidateTopology(st.Dir(), &topologyManifest); err != nil {
			return "", false, fmt.Errorf("validate review candidate topology: %w", err)
		}
	}
	var frozen *pipelineFrozenPlan
	loadedFrozen, _, frozenErr := loadAndVerifyPipelineFrozenPlan(st.Dir())
	if frozenErr == nil {
		frozen = loadedFrozen
		if frozen.Chapter != manifest.Chapter || frozen.PlanDigest != manifest.PlanDigest ||
			frozen.PlanningGenerationID != manifest.GenerationID ||
			frozen.ProjectedBundleDigest != manifest.ProjectedBundleDigest ||
			frozen.PromotionReceiptDigest != manifest.PromotionReceiptDigest {
			return "", false, fmt.Errorf("review frozen sealed identity differs from candidate manifest")
		}
		expectedCandidateID, idErr := pipelineRenderTransactionID(frozen)
		if idErr != nil {
			return "", false, idErr
		}
		if manifest.CandidateID != expectedCandidateID {
			return "", false, fmt.Errorf("review candidate CandidateID differs from deterministic sealed identity")
		}
	} else if isolated {
		return "", false, fmt.Errorf("load isolated frozen identity for review: %w", frozenErr)
	}
	v3StyleEpoch := false
	if frozen != nil {
		v3StyleEpoch, err = pipelineRenderV3StyleEpochDeclared(sourceOutputDir, frozen)
	} else {
		var intent *pipelineRenderStyleEpochIntent
		intent, err = inspectPipelineRenderV3StyleEpochIntentForManifest(sourceOutputDir, manifest)
		v3StyleEpoch = intent != nil
	}
	if err != nil {
		return "", false, fmt.Errorf("inspect immutable review style epoch: %w", err)
	}
	if v3StyleEpoch && strings.TrimSpace(manifest.Version) != pipelineRenderCandidateManifestVersion {
		return "", false, fmt.Errorf("immutable v3 style epoch intent forbids legacy review protocol")
	}
	txnStore := store.NewChapterRenderTransactionStore(sourceOutputDir)
	info, err := os.Lstat(txnStore.Root())
	if os.IsNotExist(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", false, fmt.Errorf("review chapter render transaction root is not a real directory")
	}
	if frozen == nil {
		frozen, _, err = loadAndVerifyPipelineFrozenPlan(st.Dir())
		if err != nil {
			return "", false, fmt.Errorf("load frozen identity for review BodyReady: %w", err)
		}
	}
	if frozen.Chapter != manifest.Chapter || frozen.PlanDigest != manifest.PlanDigest {
		return "", false, fmt.Errorf("review BodyReady frozen identity differs from candidate manifest")
	}
	planIdentity, err := pipelineChapterRenderPlanIdentity(frozen)
	if err != nil {
		return "", false, err
	}
	bodySHA, err := pipelineOptionalFileSHA(
		st.Dir(), fmt.Sprintf("chapters/%02d.md", manifest.Chapter),
	)
	if err != nil {
		return "", false, err
	}
	if bodySHA == "" {
		return "", false, nil
	}
	bodyIdentity, err := domain.NewChapterRenderBodyIdentity(planIdentity, bodySHA)
	if err != nil {
		return "", false, err
	}
	receipts, err := txnStore.LoadReceipts(bodyIdentity)
	if err != nil {
		return "", false, err
	}
	bodyReady := pipelineChapterRenderReceiptForPhase(receipts, domain.ChapterRenderPhaseBodyReady)
	if bodyReady == nil {
		return "", false, nil
	}
	return bodyReady.Evidence.EffectiveStyleReceiptDigest, true, nil
}

// validateReviewPipelineRenderCandidateTopology accepts the two immutable
// locations from which exact review evidence can legitimately be inspected:
// the active CandidateID container and a retired snapshot whose basename is
// still prefixed by that exact CandidateID. Convergence and dispatch continue
// to use the stricter active-only validator; this exception exists solely for
// crash recovery after the candidate has already been atomically retired.
func validateReviewPipelineRenderCandidateTopology(
	candidateOutputDir string,
	manifest *pipelineRenderCandidateManifest,
) (string, error) {
	if manifest == nil {
		return "", fmt.Errorf("review render candidate manifest is nil")
	}
	candidateOutputDir = strings.TrimSpace(candidateOutputDir)
	liveOutputDir := strings.TrimSpace(manifest.SourceOutputDir)
	if candidateOutputDir == "" || liveOutputDir == "" ||
		!filepath.IsAbs(candidateOutputDir) || !filepath.IsAbs(liveOutputDir) ||
		candidateOutputDir != filepath.Clean(candidateOutputDir) ||
		liveOutputDir != filepath.Clean(liveOutputDir) {
		return "", fmt.Errorf("review render candidate and source paths must be clean absolute paths")
	}
	if _, err := pipelineRenderConvergenceDir(liveOutputDir, manifest.CandidateID); err != nil {
		return "", err
	}

	namespace := pipelineRenderCandidateRoot(liveOutputDir)
	container := filepath.Dir(candidateOutputDir)
	if filepath.Base(candidateOutputDir) != "output" {
		return "", fmt.Errorf("review render candidate output has an invalid basename")
	}
	activeContainer := filepath.Join(namespace, manifest.CandidateID)
	retiredRoot := filepath.Join(namespace, "retired")
	retired := filepath.Dir(container) == retiredRoot &&
		strings.HasPrefix(filepath.Base(container), manifest.CandidateID+"-")
	if container != activeContainer && !retired {
		return "", fmt.Errorf("review render candidate path is outside its authenticated source namespace")
	}

	items := []struct {
		name string
		path string
	}{
		{name: "source output", path: liveOutputDir},
		{name: "candidate namespace", path: namespace},
	}
	if retired {
		items = append(items, struct {
			name string
			path string
		}{name: "retired root", path: retiredRoot})
	}
	items = append(items,
		struct {
			name string
			path string
		}{name: "candidate container", path: container},
		struct {
			name string
			path string
		}{name: "candidate output", path: candidateOutputDir},
	)
	for _, item := range items {
		info, err := os.Lstat(item.path)
		if err != nil {
			return "", fmt.Errorf("inspect review render %s: %w", item.name, err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return "", fmt.Errorf("review render %s must be a real directory", item.name)
		}
	}

	resolvedLive, err := filepath.EvalSymlinks(liveOutputDir)
	if err != nil {
		return "", fmt.Errorf("resolve review render source output: %w", err)
	}
	resolvedCandidate, err := filepath.EvalSymlinks(candidateOutputDir)
	if err != nil {
		return "", fmt.Errorf("resolve review render candidate output: %w", err)
	}
	resolvedNamespace := pipelineRenderCandidateRoot(resolvedLive)
	resolvedExpected := filepath.Join(resolvedNamespace, manifest.CandidateID, "output")
	if retired {
		resolvedExpected = filepath.Join(resolvedNamespace, "retired", filepath.Base(container), "output")
	}
	if filepath.Clean(resolvedCandidate) != filepath.Clean(resolvedExpected) {
		return "", fmt.Errorf("resolved review render candidate escapes its authenticated source namespace")
	}
	liveInfo, err := os.Stat(resolvedLive)
	if err != nil {
		return "", fmt.Errorf("stat review render source output: %w", err)
	}
	candidateInfo, err := os.Stat(resolvedCandidate)
	if err != nil {
		return "", fmt.Errorf("stat review render candidate output: %w", err)
	}
	if os.SameFile(liveInfo, candidateInfo) {
		return "", fmt.Errorf("review render candidate aliases live canon")
	}
	if _, err := validatePipelineRenderConvergenceControlDir(liveOutputDir, manifest.CandidateID, false); err != nil {
		return "", err
	}
	return liveOutputDir, nil
}

func expectedReviewModelArtifactBindings(chapter int, editorCacheKey, deepSeekCacheKey string) []reviewModelArtifactBinding {
	return []reviewModelArtifactBinding{
		{Role: "editor_model_cache", Branch: editorReviewCacheBranch, Path: filepath.ToSlash(filepath.Join("reviews", reviewExistingCacheDirectoryName, editorReviewCacheBranch, editorCacheKey+".json"))},
		{Role: "deepseek_model_cache", Branch: deepseekAIJudgeCacheBranch, Path: filepath.ToSlash(filepath.Join("reviews", reviewExistingCacheDirectoryName, deepseekAIJudgeCacheBranch, deepSeekCacheKey+".json"))},
		{Role: "mechanical_gate", Branch: "deterministic", Path: fmt.Sprintf("reviews/%02d_ai_gate.json", chapter)},
		{Role: "ai_voice_redflags", Branch: "deterministic", Path: fmt.Sprintf("reviews/%02d_ai_voice_redflags.json", chapter)},
		{Role: "editor_final", Branch: editorReviewCacheBranch, Path: fmt.Sprintf("reviews/%02d.json", chapter)},
		{Role: "deepseek_final_json", Branch: deepseekAIJudgeCacheBranch, Path: fmt.Sprintf("reviews/%02d_deepseek_ai_judge.json", chapter)},
		{Role: "deepseek_final_markdown", Branch: deepseekAIJudgeCacheBranch, Path: fmt.Sprintf("reviews/%02d_deepseek_ai_judge.md", chapter)},
		{Role: "unified_review", Branch: "reconciled", Path: fmt.Sprintf("reviews/%02d.md", chapter)},
	}
}

func reviewModelArtifactSHA256(projectDir, rel string) (string, error) {
	nativeRel := filepath.FromSlash(rel)
	if strings.TrimSpace(rel) == "" || strings.Contains(rel, `\`) || filepath.IsAbs(nativeRel) ||
		filepath.Clean(nativeRel) != nativeRel || nativeRel == ".." ||
		strings.HasPrefix(nativeRel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("review artifact path %q is not canonical and relative", rel)
	}
	path := filepath.Join(projectDir, nativeRel)
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("review artifact %s is not a regular file", rel)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if len(raw) == 0 {
		return "", fmt.Errorf("review artifact %s is empty", rel)
	}
	return reviewExistingSHA256(string(raw)), nil
}

func validateReviewModelCachePolicies(
	chapter int,
	bodySHA256, editorCacheKey string,
	editor reviewExistingCachePolicy,
	deepSeekCacheKey string,
	deepSeek reviewExistingCachePolicy,
	requireCurrentProtocol bool,
) error {
	if chapter <= 0 || strings.TrimSpace(bodySHA256) == "" ||
		editor.Chapter != chapter || editor.BodySHA256 != bodySHA256 ||
		deepSeek.Chapter != chapter || deepSeek.BodySHA256 != bodySHA256 ||
		editorCacheKey != reviewExistingCacheKey(editor) ||
		deepSeekCacheKey != reviewExistingCacheKey(deepSeek) {
		return fmt.Errorf("cache policy does not match exact chapter body")
	}
	if editor.Branch != editorReviewCacheBranch ||
		strings.TrimSpace(editor.ReviewProtocolVersion) == "" ||
		strings.TrimSpace(editor.UserPayloadKind) == "" ||
		!validReviewModelSHA256(editor.SystemPromptSHA256) ||
		strings.TrimSpace(editor.Provider) == "" || strings.TrimSpace(editor.Model) == "" {
		return fmt.Errorf("Editor branch/protocol/payload identity is stale")
	}
	if deepSeek.Branch != deepseekAIJudgeCacheBranch ||
		strings.TrimSpace(deepSeek.ReviewProtocolVersion) == "" ||
		deepSeek.UserPayloadKind != "chapter_body_only" ||
		!validReviewModelSHA256(deepSeek.SystemPromptSHA256) ||
		strings.TrimSpace(deepSeek.ReasoningEffort) == "" ||
		strings.TrimSpace(deepSeek.Provider) == "" || strings.TrimSpace(deepSeek.Model) == "" {
		return fmt.Errorf("DeepSeek branch/protocol/payload identity is stale")
	}
	if requireCurrentProtocol && (editor.ReviewProtocolVersion != editorReviewProtocolVersion ||
		editor.UserPayloadKind != editorReviewUserPayloadKind ||
		editor.SystemPromptSHA256 != reviewExistingSHA256(editorSystemPrompt) ||
		deepSeek.ReviewProtocolVersion != deepseekAIJudgeReviewProtocolVersion ||
		deepSeek.SystemPromptSHA256 != reviewExistingSHA256(deepseekAIJudgeSystemPrompt) ||
		deepSeek.ReasoningEffort != string(deepseekAIJudgeReasoningEffort)) {
		return fmt.Errorf("review provenance was not produced by the current protocol")
	}
	return nil
}

func validReviewModelSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, r := range value {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

func reviewProvenanceHasAnyStyleEvidence(provenance reviewModelProvenance) bool {
	return strings.TrimSpace(provenance.EffectiveStyleReceiptPath) != "" ||
		strings.TrimSpace(provenance.EffectiveStyleReceiptDigest) != "" ||
		strings.TrimSpace(provenance.EffectiveStyleArtifactSHA256) != "" ||
		strings.TrimSpace(provenance.StyleContractSHA256) != ""
}

func loadReviewProvenanceStyleArchive(
	st *store.Store,
	provenance reviewModelProvenance,
	chapter int,
	expectedReceiptDigest string,
	expectedPlanDigest string,
) (*tools.EffectiveRenderStyleContractReceipt, error) {
	if st == nil || chapter <= 0 ||
		strings.TrimSpace(provenance.EffectiveStyleReceiptPath) == "" ||
		strings.TrimSpace(provenance.EffectiveStyleReceiptDigest) == "" ||
		strings.TrimSpace(provenance.EffectiveStyleArtifactSHA256) == "" ||
		strings.TrimSpace(provenance.StyleContractSHA256) == "" {
		return nil, fmt.Errorf("effective style archive evidence is incomplete")
	}
	if expectedReceiptDigest = strings.TrimSpace(expectedReceiptDigest); expectedReceiptDigest != "" &&
		provenance.EffectiveStyleReceiptDigest != expectedReceiptDigest {
		return nil, fmt.Errorf("effective style receipt digest differs from active candidate")
	}
	receipt, err := tools.LoadArchivedEffectiveRenderStyleContract(
		st,
		provenance.EffectiveStyleReceiptPath,
		chapter,
		provenance.EffectiveStyleReceiptDigest,
	)
	if err != nil {
		return nil, err
	}
	if expectedPlanDigest = strings.TrimSpace(expectedPlanDigest); expectedPlanDigest != "" &&
		receipt.PlanDigest != expectedPlanDigest {
		return nil, fmt.Errorf("effective style archive plan differs from active candidate")
	}
	if receipt.StyleContractSHA256 != provenance.StyleContractSHA256 {
		return nil, fmt.Errorf("effective style contract digest mismatch")
	}
	artifactSHA, err := pipelineRequiredFileSHA(st.Dir(), provenance.EffectiveStyleReceiptPath)
	if err != nil {
		return nil, err
	}
	if artifactSHA != provenance.EffectiveStyleArtifactSHA256 {
		return nil, fmt.Errorf("effective style archive artifact sha256 mismatch")
	}
	return receipt, nil
}

func validateReviewModelArtifactBindings(
	projectDir string,
	provenance reviewModelProvenance,
	finalJudge *deepseekAIJudgeArtifact,
) []string {
	expected := expectedReviewModelArtifactBindings(
		provenance.Chapter,
		provenance.EditorCacheKey,
		provenance.DeepSeekCacheKey,
	)
	var issues []string
	if len(provenance.Artifacts) != len(expected) {
		issues = append(issues, fmt.Sprintf("artifact binding count=%d, want %d", len(provenance.Artifacts), len(expected)))
	}
	byRole := make(map[string]reviewModelArtifactBinding, len(provenance.Artifacts))
	for _, binding := range provenance.Artifacts {
		if _, duplicate := byRole[binding.Role]; duplicate {
			issues = append(issues, fmt.Sprintf("duplicate artifact role %q", binding.Role))
			continue
		}
		byRole[binding.Role] = binding
	}
	for _, want := range expected {
		binding, ok := byRole[want.Role]
		if !ok {
			issues = append(issues, fmt.Sprintf("%s binding missing", want.Role))
			continue
		}
		if binding.Path != want.Path || binding.Branch != want.Branch {
			issues = append(issues, fmt.Sprintf("%s path/branch identity invalid", want.Role))
			continue
		}
		digest, err := reviewModelArtifactSHA256(projectDir, binding.Path)
		if err != nil {
			issues = append(issues, fmt.Sprintf("%s (%v)", binding.Path, err))
			continue
		}
		if binding.SHA256 != digest {
			issues = append(issues, fmt.Sprintf("%s (sha256 mismatch)", binding.Path))
		}
	}

	// Digest bindings catch interrupted/mixed writes. Decode the two model
	// caches as well so a self-consistent but wrong branch or cache payload
	// cannot be treated as the response named by the provenance policies.
	editorCachePath := reviewExistingCachePath(projectDir, editorReviewCacheBranch, provenance.EditorCacheKey)
	if raw, err := os.ReadFile(editorCachePath); err == nil {
		var cache editorReviewCacheArtifact
		if err := json.Unmarshal(raw, &cache); err != nil ||
			validateEditorReviewCacheArtifact(&cache, provenance.EditorCachePolicy) != nil {
			issues = append(issues, filepath.ToSlash(strings.TrimPrefix(editorCachePath, projectDir+string(filepath.Separator)))+" (invalid Editor cache artifact)")
		}
	}
	deepSeekCachePath := reviewExistingCachePath(projectDir, deepseekAIJudgeCacheBranch, provenance.DeepSeekCacheKey)
	if raw, err := os.ReadFile(deepSeekCachePath); err == nil {
		var cache deepseekAIJudgeArtifact
		if err := json.Unmarshal(raw, &cache); err != nil ||
			validateDeepSeekAIJudgeCacheArtifact(&cache, provenance.DeepSeekCachePolicy) != nil {
			issues = append(issues, filepath.ToSlash(strings.TrimPrefix(deepSeekCachePath, projectDir+string(filepath.Separator)))+" (invalid DeepSeek cache artifact)")
		}
	}
	if finalJudge == nil || finalJudge.CacheKey != provenance.DeepSeekCacheKey ||
		finalJudge.CachePolicy != provenance.DeepSeekCachePolicy {
		issues = append(issues, fmt.Sprintf("reviews/%02d_deepseek_ai_judge.json (mixed model identity)", provenance.Chapter))
	}
	return issues
}

// inspectCurrentChapterReview verifies that every durable review component was
// produced for the current chapter bytes. Existence alone is not completion:
// direct edits and interrupted rewrites can leave a complete-looking stale set.
func inspectCurrentChapterReview(projectDir string, chapter int) currentChapterReviewEvidence {
	result := currentChapterReviewEvidence{Chapter: chapter}
	chapterRel := fmt.Sprintf("chapters/%02d.md", chapter)
	body, err := os.ReadFile(filepath.Join(projectDir, filepath.FromSlash(chapterRel)))
	if err != nil || strings.TrimSpace(string(body)) == "" {
		result.Issues = append(result.Issues, chapterRel+" (missing or empty)")
		return result
	}
	result.BodySHA256 = reviewreport.BodySHA256(string(body))

	readJSON := func(rel string, dst any) bool {
		raw, readErr := os.ReadFile(filepath.Join(projectDir, filepath.FromSlash(rel)))
		if readErr != nil || len(strings.TrimSpace(string(raw))) == 0 {
			result.Issues = append(result.Issues, rel+" (missing or empty)")
			return false
		}
		if unmarshalErr := json.Unmarshal(raw, dst); unmarshalErr != nil {
			result.Issues = append(result.Issues, rel+" (invalid JSON)")
			return false
		}
		result.Artifacts = append(result.Artifacts, rel)
		return true
	}
	checkIdentity := func(rel string, artifactChapter int, bodyHash string) {
		if artifactChapter != chapter {
			result.Issues = append(result.Issues, fmt.Sprintf("%s (chapter=%d, want %d)", rel, artifactChapter, chapter))
		}
		if strings.TrimSpace(bodyHash) == "" {
			result.Issues = append(result.Issues, rel+" (body_sha256 missing)")
		} else if bodyHash != result.BodySHA256 {
			result.Issues = append(result.Issues, rel+" (body_sha256 stale)")
		}
	}

	mechanicalRel := fmt.Sprintf("reviews/%02d_ai_gate.json", chapter)
	var mechanical reviewreport.MechanicalGatePayload
	mechanicalCurrent := readJSON(mechanicalRel, &mechanical)
	if mechanicalCurrent {
		checkIdentity(mechanicalRel, mechanical.Chapter, mechanical.BodySHA256)
	}

	voiceRel := fmt.Sprintf("reviews/%02d_ai_voice_redflags.json", chapter)
	var voice domain.AIVoiceAnalysis
	if readJSON(voiceRel, &voice) {
		checkIdentity(voiceRel, voice.Chapter, voice.BodySHA256)
	}

	editorRel := fmt.Sprintf("reviews/%02d.json", chapter)
	var editor domain.ReviewEntry
	if readJSON(editorRel, &editor) {
		checkIdentity(editorRel, editor.Chapter, editor.BodySHA256)
		if editor.Scope != "chapter" {
			result.Issues = append(result.Issues, editorRel+" (scope is not chapter)")
		}
		result.Verdict = editor.Verdict
	}

	judgeRel := fmt.Sprintf("reviews/%02d_deepseek_ai_judge.json", chapter)
	var judge deepseekAIJudgeArtifact
	if readJSON(judgeRel, &judge) {
		checkIdentity(judgeRel, judge.Chapter, judge.BodySHA256)
		if !judge.RawBodyOnly || judge.UserPayloadKind != "chapter_body_only" {
			result.Issues = append(result.Issues, judgeRel+" (not a raw-body-only judgment)")
		}
	}

	// The manifest declares whether style evidence is required. Never infer a
	// protocol upgrade from the mutable singleton receipt: it may belong to a
	// different chapter, and a missing v3 receipt must remain a hard failure.
	styleRequirement, styleRequirementErr := loadReviewEffectiveStyleRequirement(store.NewStore(projectDir), chapter)
	styleReceiptRequired := styleRequirement.Required
	styleReceiptDigest := ""
	styleContractSHA := ""
	if styleRequirementErr != nil {
		result.Issues = append(result.Issues, fmt.Sprintf(
			"meta/planning/render_candidate.json (effective style requirement invalid) (%v)",
			styleRequirementErr,
		))
	} else if styleReceiptRequired {
		_, verified, _, verifyErr := tools.LoadBoundArchivedEffectiveRenderStyleContract(
			store.NewStore(projectDir), chapter, styleRequirement.PlanDigest,
		)
		if verifyErr != nil || verified == nil || verified.ReceiptDigest != styleRequirement.ReceiptDigest {
			result.Issues = append(result.Issues, tools.EffectiveRenderStyleContractPath+" (required receipt missing or invalid)")
		} else {
			styleReceiptDigest = verified.ReceiptDigest
			styleContractSHA = verified.StyleContractSHA256
		}
	}

	provenanceRel := fmt.Sprintf("reviews/%02d_model_provenance.json", chapter)
	var provenance reviewModelProvenance
	provenanceRaw, provenanceErr := os.ReadFile(filepath.Join(projectDir, filepath.FromSlash(provenanceRel)))
	if provenanceErr == nil && len(strings.TrimSpace(string(provenanceRaw))) > 0 {
		if json.Unmarshal(provenanceRaw, &provenance) != nil {
			result.Issues = append(result.Issues, provenanceRel+" (invalid JSON)")
		} else {
			styleArchiveVerified := false
			result.Artifacts = append(result.Artifacts, provenanceRel)
			if provenance.Version != reviewModelProvenanceVersion || provenance.Chapter != chapter ||
				provenance.BodySHA256 != result.BodySHA256 ||
				validateReviewModelCachePolicies(
					chapter,
					result.BodySHA256,
					provenance.EditorCacheKey,
					provenance.EditorCachePolicy,
					provenance.DeepSeekCacheKey,
					provenance.DeepSeekCachePolicy,
					false,
				) != nil {
				result.Issues = append(result.Issues, provenanceRel+" (identity or cache key invalid)")
			}
			styleFieldsPresent := reviewProvenanceHasAnyStyleEvidence(provenance)
			explicitLegacy := styleRequirementErr == nil &&
				(styleRequirement.ManifestVersion == pipelineRenderCandidatePreviousManifestVersion ||
					styleRequirement.ManifestVersion == pipelineRenderCandidateLegacyManifestVersion)
			switch {
			case styleReceiptRequired:
				archived, archiveErr := loadReviewProvenanceStyleArchive(
					store.NewStore(projectDir),
					provenance,
					chapter,
					styleRequirement.ReceiptDigest,
					styleRequirement.PlanDigest,
				)
				if archiveErr != nil || archived == nil ||
					(styleReceiptDigest != "" && provenance.EffectiveStyleReceiptDigest != styleReceiptDigest) ||
					(styleContractSHA != "" && provenance.StyleContractSHA256 != styleContractSHA) ||
					(archived != nil && provenance.EditorCachePolicy.StyleContractSHA256 != archived.StyleContractSHA256) {
					result.Issues = append(result.Issues, provenanceRel+" (effective style archive mismatch)")
				} else {
					styleArchiveVerified = true
				}
			case explicitLegacy:
				if styleFieldsPresent {
					result.Issues = append(result.Issues, provenanceRel+" (legacy candidate was relabeled with v3 style evidence)")
				}
			case styleRequirementErr == nil && styleFieldsPresent:
				// Once a newer chapter owns the mutable candidate manifest, an
				// accepted historical chapter authenticates its original v3 style
				// receipt exclusively through this immutable archive binding.
				if archived, archiveErr := loadReviewProvenanceStyleArchive(
					store.NewStore(projectDir), provenance, chapter, "", "",
				); archiveErr != nil || archived == nil ||
					(archived != nil && provenance.EditorCachePolicy.StyleContractSHA256 != archived.StyleContractSHA256) {
					result.Issues = append(result.Issues, provenanceRel+" (historical effective style archive invalid)")
				} else {
					styleArchiveVerified = true
				}
			}
			for _, issue := range validateReviewModelArtifactBindings(projectDir, provenance, &judge) {
				result.Issues = append(result.Issues, provenanceRel+" ("+issue+")")
			}
			result.EditorCacheKey = provenance.EditorCacheKey
			result.DeepSeekCacheKey = provenance.DeepSeekCacheKey
			result.EditorContextSHA256 = provenance.EditorCachePolicy.ChapterReviewContextSHA256
			if styleArchiveVerified {
				result.EffectiveStyleReceiptPath = provenance.EffectiveStyleReceiptPath
				result.EffectiveStyleReceiptHash = provenance.EffectiveStyleReceiptDigest
				result.EffectiveStyleArtifactSHA256 = provenance.EffectiveStyleArtifactSHA256
			}
		}
	} else if styleReceiptRequired {
		result.Issues = append(result.Issues, provenanceRel+" (missing for effective style receipt)")
	} else if provenanceErr != nil && !os.IsNotExist(provenanceErr) {
		result.Issues = append(result.Issues, provenanceRel+" (unreadable)")
	}

	reportRel := fmt.Sprintf("reviews/%02d.md", chapter)
	reportRaw, reportErr := os.ReadFile(filepath.Join(projectDir, filepath.FromSlash(reportRel)))
	if reportErr != nil || len(strings.TrimSpace(string(reportRaw))) == 0 {
		result.Issues = append(result.Issues, reportRel+" (missing or empty)")
	} else {
		result.Artifacts = append(result.Artifacts, reportRel)
		if !strings.Contains(string(reportRaw), "sha256="+result.BodySHA256) {
			result.Issues = append(result.Issues, reportRel+" (current body fingerprint missing)")
		}
	}

	// A user-reported high sample can be registered after an otherwise current
	// review was produced. Bind every readable, still-blocking identity to the
	// mechanical gate, checkpoint journal and unified report so a low result from
	// another identity cannot revive that review. The sampling journal is not a
	// production dependency: if it is unreadable, registration remains fail
	// closed but chapter review freshness continues on automated evidence.
	registered, registeredErr := reviewreport.LatestRegisteredExternalDetections(projectDir, chapter, result.BodySHA256)
	if registeredErr == nil {
		checkpoints := store.NewStore(projectDir).Checkpoints.All()
		for _, detection := range registered {
			if detection.NormalizedScorePercent < aigc.PassExclusivePercent {
				continue
			}
			identity := registeredExternalDetectionIdentity(detection)
			if !mechanicalCurrent || !mechanicalHasRegisteredExternalDetection(&mechanical, detection) {
				result.Issues = append(result.Issues, fmt.Sprintf(
					"%s (current registered external detection %s %.2f%% missing)",
					mechanicalRel, identity, detection.NormalizedScorePercent,
				))
			}
			if !hasRegisteredExternalDetectionCheckpoint(checkpoints, chapter, detection) {
				result.Issues = append(result.Issues, fmt.Sprintf(
					"meta/checkpoints.jsonl (current registered external detection %s not reviewed)", identity,
				))
			}
			reportNeedle := fmt.Sprintf("external_aigc_ratio｜actual=%v｜limit=<4%%｜target=%s",
				detection.NormalizedScorePercent, registeredExternalDetectionTarget(detection))
			if reportErr == nil && !strings.Contains(string(reportRaw), reportNeedle) {
				result.Issues = append(result.Issues, fmt.Sprintf(
					"%s (current registered external detection %s missing)", reportRel, identity,
				))
			}
		}
	}
	if len(result.Issues) == 0 {
		result.Disposition = reviewreport.RewriteDisposition(
			&mechanical,
			&voice,
			deepSeekExternalAIJudge(&judge),
			&editor,
		)
		if result.Disposition == "是" && editor.Verdict != "rewrite" {
			result.Issues = append(result.Issues, editorRel+" (verdict contradicts blocking unified review)")
		}
	}

	return result
}

func registeredExternalDetectionIdentity(detection reviewreport.RegisteredExternalDetection) string {
	detector := strings.TrimSpace(detection.Detector)
	mode := strings.TrimSpace(detection.Mode)
	if mode == "" {
		return detector
	}
	return detector + "/" + mode
}

func registeredExternalDetectionTarget(detection reviewreport.RegisteredExternalDetection) string {
	return registeredExternalDetectionIdentity(detection)
}

func mechanicalHasRegisteredExternalDetection(mechanical *reviewreport.MechanicalGatePayload, detection reviewreport.RegisteredExternalDetection) bool {
	if mechanical == nil {
		return false
	}
	wantTarget := registeredExternalDetectionTarget(detection)
	for _, violation := range mechanical.RuleViolations {
		if strings.TrimSpace(violation.Rule) != "external_aigc_ratio" ||
			!strings.EqualFold(strings.TrimSpace(violation.Target), wantTarget) {
			continue
		}
		actual, err := strconv.ParseFloat(strings.TrimSpace(fmt.Sprint(violation.Actual)), 64)
		if err == nil && math.Abs(actual-detection.NormalizedScorePercent) <= 0.0001 {
			return true
		}
	}
	return false
}

func hasRegisteredExternalDetectionCheckpoint(checkpoints []domain.Checkpoint, chapter int, detection reviewreport.RegisteredExternalDetection) bool {
	wantDigest := reviewreport.RegisteredExternalDetectionDigest(detection)
	wantScope := domain.ChapterScope(chapter)
	for i := len(checkpoints) - 1; i >= 0; i-- {
		checkpoint := checkpoints[i]
		if checkpoint.Scope.Matches(wantScope) &&
			checkpoint.Step == "registered-external-detection" &&
			checkpoint.Digest == wantDigest {
			return true
		}
	}
	return false
}

// currentRegisteredExternalDeliveryIssues is deliberately delivery-only. A
// user-reported high result bound to the exact current hash requires a rewrite;
// absence of a spot-check result never blocks. Missing identities can only come
// from an explicitly configured automated external gate.
func currentRegisteredExternalDeliveryIssues(projectDir string, chapter int) []string {
	chapterPath := filepath.Join(projectDir, "chapters", fmt.Sprintf("%02d.md", chapter))
	body, readErr := os.ReadFile(chapterPath)
	if readErr != nil || strings.TrimSpace(string(body)) == "" {
		return []string{fmt.Sprintf("chapters/%02d.md missing or empty", chapter)}
	}
	inspection, err := tools.InspectRegisteredExternalRetestsForBody(
		projectDir, chapter, reviewreport.BodySHA256(string(body)),
	)
	if err != nil {
		return []string{fmt.Sprintf("reviews/drafts/%02d external gate unreadable", chapter)}
	}
	if !inspection.Required || inspection.Approved {
		return nil
	}
	if len(inspection.Blocking) > 0 {
		return []string{fmt.Sprintf(
			"reviews/drafts/%02d current exact-hash external sampling result requires rewrite (%s)",
			chapter, strings.Join(inspection.Blocking, "; "),
		)}
	}
	details := make([]string, 0, len(inspection.Missing))
	if len(inspection.Missing) > 0 {
		details = append(details, "missing="+strings.Join(inspection.Missing, ","))
	}
	return []string{fmt.Sprintf(
		"reviews/drafts/%02d explicit automated external gate unresolved (required exact-payload retest: %s)",
		chapter, strings.Join(details, "; "),
	)}
}

func inspectReviewSummaryCurrent(projectDir string, chapters []int, hashes map[int]string) (string, []string) {
	rel := "meta/review-summary.md"
	raw, err := os.ReadFile(filepath.Join(projectDir, filepath.FromSlash(rel)))
	if err != nil || len(strings.TrimSpace(string(raw))) == 0 {
		return "", []string{rel + " (missing or empty)"}
	}
	body := string(raw)
	var issues []string
	for _, chapter := range chapters {
		if !strings.Contains(body, fmt.Sprintf("**ch%02d**", chapter)) {
			issues = append(issues, fmt.Sprintf("%s (ch%02d row missing)", rel, chapter))
			continue
		}
		if hash := hashes[chapter]; hash == "" || !strings.Contains(body, "body_sha256="+hash) {
			issues = append(issues, fmt.Sprintf("%s (ch%02d current body fingerprint missing)", rel, chapter))
		}
	}
	return rel, issues
}

func currentChapterReviewError(projectDir string, chapter int) error {
	evidence := inspectCurrentChapterReview(projectDir, chapter)
	if len(evidence.Issues) == 0 {
		return nil
	}
	return fmt.Errorf("第 %d 章审核产物不是当前正文版本：%s", chapter, strings.Join(evidence.Issues, ", "))
}
