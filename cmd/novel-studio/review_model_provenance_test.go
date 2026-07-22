package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/reviewreport"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/tools"
)

func TestReviewModelProvenanceIsNotWrittenBeforeFinalArtifactSet(t *testing.T) {
	dir, st, editorCache, finalEditor, deepSeek := newReviewModelProvenanceFixture(t)
	reportPath := filepath.Join(dir, "reviews", "01.md")
	if err := os.Remove(reportPath); err != nil {
		t.Fatal(err)
	}
	err := persistReviewModelProvenance(
		st, 1, reviewreport.BodySHA256("第一章正文"), editorCache, finalEditor, deepSeek,
	)
	if err == nil || !strings.Contains(err.Error(), "reviews/01.md") {
		t.Fatalf("incomplete final artifact set was sealed: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "reviews", "01_model_provenance.json")); !os.IsNotExist(statErr) {
		t.Fatalf("crash-window provenance should not exist: %v", statErr)
	}
}

func TestReviewFreshnessRejectsTamperedFinalArtifactBoundByProvenance(t *testing.T) {
	dir, st, editorCache, finalEditor, deepSeek := newReviewModelProvenanceFixture(t)
	if err := persistReviewModelProvenance(
		st, 1, reviewreport.BodySHA256("第一章正文"), editorCache, finalEditor, deepSeek,
	); err != nil {
		t.Fatal(err)
	}
	if current := inspectCurrentChapterReview(dir, 1); len(current.Issues) != 0 {
		t.Fatalf("fresh provenance fixture rejected: %+v", current.Issues)
	}

	path := filepath.Join(dir, "reviews", "01_deepseek_ai_judge.json")
	var mixed deepseekAIJudgeArtifact
	readJSONFileForFreshness(t, path, &mixed)
	mixed.Summary = "来自另一轮但仍沿用相同 body/cache identity 的结果"
	mustWriteJSONFile(t, path, mixed)
	issues := strings.Join(inspectCurrentChapterReview(dir, 1).Issues, "\n")
	if !strings.Contains(issues, "01_deepseek_ai_judge.json (sha256 mismatch)") {
		t.Fatalf("mixed final artifact was not rejected: %s", issues)
	}
}

func TestReviewFreshnessRejectsMissingBoundModelCache(t *testing.T) {
	dir, st, editorCache, finalEditor, deepSeek := newReviewModelProvenanceFixture(t)
	if err := persistReviewModelProvenance(
		st, 1, reviewreport.BodySHA256("第一章正文"), editorCache, finalEditor, deepSeek,
	); err != nil {
		t.Fatal(err)
	}
	cachePath := reviewExistingCachePath(dir, editorReviewCacheBranch, editorCache.CacheKey)
	if err := os.Remove(cachePath); err != nil {
		t.Fatal(err)
	}
	issues := strings.Join(inspectCurrentChapterReview(dir, 1).Issues, "\n")
	if !strings.Contains(issues, filepath.ToSlash(strings.TrimPrefix(cachePath, dir+string(filepath.Separator)))) {
		t.Fatalf("missing Editor model cache was not rejected: %s", issues)
	}
}

func TestReviewFreshnessDerivesV3StyleRequirementFromManifest(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "chapters", "01.md"), "第一章正文")
	mustWriteCurrentReviewArtifacts(t, dir, 1)
	manifest := reviewStyleRequirementTestManifest(pipelineRenderCandidateManifestVersion)
	manifest.EffectiveStyleReceiptDigest = pipelineBytesSHA([]byte("required style receipt"))
	mustWriteJSONFile(t, filepath.Join(dir, "meta", "planning", "render_candidate.json"), manifest)

	issues := strings.Join(inspectCurrentChapterReview(dir, 1).Issues, "\n")
	if !strings.Contains(issues, tools.EffectiveRenderStyleContractPath+" (required receipt missing or invalid)") {
		t.Fatalf("v3 manifest without its receipt was downgraded to legacy freshness: %s", issues)
	}
}

func TestReviewFreshnessExplicitV2IgnoresUnboundStyleSingleton(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "chapters", "01.md"), "第一章正文")
	mustWriteCurrentReviewArtifacts(t, dir, 1)
	manifest := reviewStyleRequirementTestManifest(pipelineRenderCandidatePreviousManifestVersion)
	manifest.PipelineRenderInputDigest = ""
	manifest.RenderContextSHA256 = ""
	manifest.EffectiveStyleReceiptDigest = ""
	mustWriteJSONFile(t, filepath.Join(dir, "meta", "planning", "render_candidate.json"), manifest)
	mustWriteFile(t, filepath.Join(dir, filepath.FromSlash(tools.EffectiveRenderStyleContractPath)), "{definitely-not-a-receipt")

	if current := inspectCurrentChapterReview(dir, 1); len(current.Issues) != 0 {
		t.Fatalf("explicit v2 review consulted an unbound singleton receipt: %+v", current.Issues)
	}
}

func TestReviewFreshnessCannotHideV3BodyReadyWithSelfPointingLegacyManifest(t *testing.T) {
	live, frozen, plan, candidate, snapshot := newPipelineChapterRenderTransactionFixture(t)
	frozen.EffectiveStyleProtocol = pipelineRenderCandidateManifestVersion
	for _, outputDir := range []string{live, candidate.OutputDir} {
		if _, err := writePipelinePlanningJSON(
			filepath.Join(outputDir, filepath.FromSlash(pipelineFrozenPlanPath)), frozen,
		); err != nil {
			t.Fatal(err)
		}
	}
	manifestPath := filepath.Join(candidate.OutputDir, "meta", "planning", "render_candidate.json")
	var manifest pipelineRenderCandidateManifest
	readJSONFileForFreshness(t, manifestPath, &manifest)
	manifest.Version = pipelineRenderCandidatePreStyleManifestVersion
	manifest.PipelineRenderInputDigest = frozen.PipelineRunInputDigest
	manifest.RenderContextSHA256 = frozen.RenderContextSHA256
	manifest.EffectiveStyleReceiptDigest = ""
	manifest.SourceOutputDir = live
	mustWriteJSONFile(t, manifestPath, manifest)
	if _, err := ensurePipelineRenderV3StyleEpochIntent(live, frozen, candidate.ID); err != nil {
		t.Fatal(err)
	}
	styleReceipt, err := tools.PublishEffectiveRenderStyleContract(
		store.NewStore(candidate.OutputDir),
		tools.EffectiveRenderStyleContractIdentity{
			GenerationID:              frozen.PlanningGenerationID,
			Chapter:                   frozen.Chapter,
			PlanDigest:                frozen.PlanDigest,
			PlanCheckpointSeq:         frozen.PlanCheckpointSeq,
			BaseRenderContextSHA256:   frozen.RenderContextSHA256,
			PipelineRenderInputDigest: frozen.PipelineRunInputDigest,
			ProjectedBundleDigest:     frozen.ProjectedBundleDigest,
			PromotionReceiptDigest:    frozen.PromotionReceiptDigest,
			CandidateID:               candidate.ID,
		},
		"review-body-ready-style",
		"# 审核风格\n- 叙述声音：克制。",
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := bindPipelineRenderCandidateEffectiveStyle(candidate.OutputDir, frozen, styleReceipt); err != nil {
		t.Fatal(err)
	}
	if _, err := pipelineEnsureChapterRenderCommitted(
		live, candidate.OutputDir, frozen, plan, snapshot,
	); err != nil {
		t.Fatal(err)
	}

	readJSONFileForFreshness(t, manifestPath, &manifest)
	manifest.Version = pipelineRenderCandidatePreviousManifestVersion
	manifest.PipelineRenderInputDigest = ""
	manifest.RenderContextSHA256 = ""
	manifest.EffectiveStyleReceiptDigest = ""
	manifest.SourceOutputDir = candidate.OutputDir
	mustWriteJSONFile(t, manifestPath, manifest)
	if _, err := loadReviewEffectiveStyleRequirement(store.NewStore(candidate.OutputDir), 1); err == nil ||
		!strings.Contains(err.Error(), "source output conflicts with directory topology") {
		t.Fatalf("self-pointing v2 manifest hid live v3 BodyReady evidence: %v", err)
	}
}

func TestReviewFreshnessMissingManifestCannotHidePendingV3BodyReady(t *testing.T) {
	live, _, _ := reviewFreshnessPendingV3BodyReadyFixture(t)
	if _, err := loadReviewEffectiveStyleRequirement(store.NewStore(live), 1); err == nil ||
		!strings.Contains(err.Error(), "render_candidate.json is missing") ||
		!strings.Contains(err.Error(), "durably requires v3 style receipt") {
		t.Fatalf("missing non-active manifest hid exact pending v3 BodyReady evidence: %v", err)
	}
}

func TestReviewFreshnessChapterDriftCannotHidePendingV3BodyReady(t *testing.T) {
	live, manifest, _ := reviewFreshnessPendingV3BodyReadyFixture(t)
	manifest.Chapter = 2
	mustWriteJSONFile(t, filepath.Join(live, "meta", "planning", "render_candidate.json"), manifest)
	if _, err := loadReviewEffectiveStyleRequirement(store.NewStore(live), 1); err == nil ||
		!strings.Contains(err.Error(), "belongs to chapter 2") ||
		!strings.Contains(err.Error(), "durably requires v3 style receipt") {
		t.Fatalf("non-active manifest chapter drift hid exact pending v3 BodyReady evidence: %v", err)
	}
}

func TestReviewFreshnessMissingManifestKeepsAcceptedV3HistoryReadable(t *testing.T) {
	live, _, identity := reviewFreshnessPendingV3BodyReadyFixture(t)
	transactions := store.NewChapterRenderTransactionStore(live)
	advance := func(phase domain.ChapterRenderPhase, evidence domain.ChapterRenderPhaseEvidence) {
		t.Helper()
		if _, err := transactions.Advance(identity, phase, evidence); err != nil {
			t.Fatalf("advance accepted-history fixture to %s: %v", phase, err)
		}
	}
	advance(domain.ChapterRenderPhaseFormalAccepted, domain.ChapterRenderPhaseEvidence{
		ReviewVerdict:     "accept",
		ReviewDisposition: "accepted",
		ReviewArtifacts: []domain.ChapterRenderArtifactBinding{{
			Path: "reviews/01.json", Digest: pipelineBytesSHA([]byte("accepted review")),
		}},
	})
	advance(domain.ChapterRenderPhaseActualMatched, domain.ChapterRenderPhaseEvidence{
		ActualMatchDigest: pipelineBytesSHA([]byte("accepted actual match")),
	})
	advance(domain.ChapterRenderPhasePublished, domain.ChapterRenderPhaseEvidence{
		DirectoryPublishID:     "accepted-history-publish",
		DirectoryPublishDigest: pipelineBytesSHA([]byte("accepted publish")),
	})
	advance(domain.ChapterRenderPhaseOutcomeAccepted, domain.ChapterRenderPhaseEvidence{
		OutcomeReceiptDigest: pipelineBytesSHA([]byte("accepted outcome")),
	})
	advance(domain.ChapterRenderPhaseChapterAccepted, domain.ChapterRenderPhaseEvidence{
		ChapterAcceptanceDigest: pipelineBytesSHA([]byte("accepted chapter")),
	})

	requirement, err := loadReviewEffectiveStyleRequirement(store.NewStore(live), 1)
	if err != nil || requirement.Required || requirement.ManifestVersion != "" {
		t.Fatalf("accepted v3 history lost manifest-free provenance compatibility: requirement=%+v err=%v", requirement, err)
	}
}

func reviewFreshnessPendingV3BodyReadyFixture(
	t *testing.T,
) (string, pipelineRenderCandidateManifest, domain.ChapterRenderBodyIdentity) {
	t.Helper()
	live, frozen, plan, candidate, snapshot := newPipelineChapterRenderTransactionFixture(t)
	frozen.EffectiveStyleProtocol = pipelineRenderCandidateManifestVersion
	for _, outputDir := range []string{live, candidate.OutputDir} {
		if _, err := writePipelinePlanningJSON(
			filepath.Join(outputDir, filepath.FromSlash(pipelineFrozenPlanPath)), frozen,
		); err != nil {
			t.Fatal(err)
		}
	}
	manifestPath := filepath.Join(candidate.OutputDir, "meta", "planning", "render_candidate.json")
	var manifest pipelineRenderCandidateManifest
	readJSONFileForFreshness(t, manifestPath, &manifest)
	manifest.Version = pipelineRenderCandidatePreStyleManifestVersion
	manifest.PipelineRenderInputDigest = frozen.PipelineRunInputDigest
	manifest.RenderContextSHA256 = frozen.RenderContextSHA256
	manifest.EffectiveStyleReceiptDigest = ""
	manifest.SourceOutputDir = live
	mustWriteJSONFile(t, manifestPath, manifest)
	if _, err := ensurePipelineRenderV3StyleEpochIntent(live, frozen, candidate.ID); err != nil {
		t.Fatal(err)
	}
	styleReceipt, err := tools.PublishEffectiveRenderStyleContract(
		store.NewStore(candidate.OutputDir),
		tools.EffectiveRenderStyleContractIdentity{
			GenerationID:              frozen.PlanningGenerationID,
			Chapter:                   frozen.Chapter,
			PlanDigest:                frozen.PlanDigest,
			PlanCheckpointSeq:         frozen.PlanCheckpointSeq,
			BaseRenderContextSHA256:   frozen.RenderContextSHA256,
			PipelineRenderInputDigest: frozen.PipelineRunInputDigest,
			ProjectedBundleDigest:     frozen.ProjectedBundleDigest,
			PromotionReceiptDigest:    frozen.PromotionReceiptDigest,
			CandidateID:               candidate.ID,
		},
		"review-manifest-fallback-style",
		"# 审核风格\n- 叙述声音：克制。",
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := bindPipelineRenderCandidateEffectiveStyle(candidate.OutputDir, frozen, styleReceipt); err != nil {
		t.Fatal(err)
	}
	identity, err := pipelineEnsureChapterRenderCommitted(
		live, candidate.OutputDir, frozen, plan, snapshot,
	)
	if err != nil {
		t.Fatal(err)
	}
	chapterRaw, err := os.ReadFile(filepath.Join(candidate.OutputDir, "chapters", "01.md"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(live, "chapters", "01.md"), chapterRaw, 0o644); err != nil {
		t.Fatal(err)
	}
	readJSONFileForFreshness(t, manifestPath, &manifest)
	return live, manifest, identity
}

func TestReviewFreshnessActiveRenderCannotLoseCandidateManifest(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	mustWriteFile(t, filepath.Join(dir, "chapters", "01.md"), "第一章正文")
	mustWriteCurrentReviewArtifacts(t, dir, 1)
	if err := st.Runtime.AcquirePipelineExecution(domain.PipelineExecutionLock{
		Mode:          domain.PipelineExecutionRender,
		TargetChapter: 1,
		PlanDigest:    pipelineBytesSHA([]byte("active render plan")),
		Owner:         "review-provenance-missing-manifest-test",
		ExpiresAt:     time.Now().UTC().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	issues := strings.Join(inspectCurrentChapterReview(dir, 1).Issues, "\n")
	if !strings.Contains(issues, "render_candidate.json (effective style requirement invalid)") {
		t.Fatalf("active render without its candidate manifest was treated as legacy: %s", issues)
	}
}

func TestReviewFreshnessKeepsHistoricallyBoundProtocolReadableAfterUpgrade(t *testing.T) {
	dir, st, editorCache, finalEditor, deepSeek := newReviewModelProvenanceFixture(t)
	if err := persistReviewModelProvenance(
		st, 1, reviewreport.BodySHA256("第一章正文"), editorCache, finalEditor, deepSeek,
	); err != nil {
		t.Fatal(err)
	}
	provenancePath := filepath.Join(dir, "reviews", "01_model_provenance.json")
	var provenance reviewModelProvenance
	readJSONFileForFreshness(t, provenancePath, &provenance)
	oldEditorPath := reviewExistingCachePath(dir, editorReviewCacheBranch, provenance.EditorCacheKey)
	var historicalCache editorReviewCacheArtifact
	readJSONFileForFreshness(t, oldEditorPath, &historicalCache)
	historicalCache.CachePolicy.ReviewProtocolVersion = "review-existing/editor/historical"
	historicalCache.CachePolicy.UserPayloadKind = "chapter_context_historical"
	historicalCache.CachePolicy.SystemPromptSHA256 = reviewExistingSHA256("historical Editor prompt")
	historicalCache.CacheKey = reviewExistingCacheKey(historicalCache.CachePolicy)
	if err := saveEditorReviewCache(dir, &historicalCache); err != nil {
		t.Fatal(err)
	}
	provenance.EditorCacheKey = historicalCache.CacheKey
	provenance.EditorCachePolicy = historicalCache.CachePolicy
	provenance.Artifacts = expectedReviewModelArtifactBindings(1, provenance.EditorCacheKey, provenance.DeepSeekCacheKey)
	for i := range provenance.Artifacts {
		digest, err := reviewModelArtifactSHA256(dir, provenance.Artifacts[i].Path)
		if err != nil {
			t.Fatal(err)
		}
		provenance.Artifacts[i].SHA256 = digest
	}
	mustWriteJSONFile(t, provenancePath, provenance)

	if current := inspectCurrentChapterReview(dir, 1); len(current.Issues) != 0 {
		t.Fatalf("a self-consistent immutable historical protocol was bricked by current prompt versions: %+v", current.Issues)
	}
}

func newReviewModelProvenanceFixture(
	t *testing.T,
) (string, *store.Store, *editorReviewCacheArtifact, *domain.ReviewEntry, *deepseekAIJudgeArtifact) {
	t.Helper()
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	const body = "第一章正文"
	mustWriteFile(t, filepath.Join(dir, "chapters", "01.md"), body)
	mustWriteCurrentReviewArtifacts(t, dir, 1)

	editorPolicy := newEditorReviewCachePolicy(
		"test-openai", "editor-test", "premise", "rules", "chapter-context",
		1, body, "ai-voice-context",
	)
	editorCache := &editorReviewCacheArtifact{
		Chapter:     1,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		CacheKey:    reviewExistingCacheKey(editorPolicy),
		CachePolicy: editorPolicy,
		Markdown:    editorCacheTestMarkdown,
	}
	if err := saveEditorReviewCache(dir, editorCache); err != nil {
		t.Fatal(err)
	}

	selection := deepseekAIJudgeModelSelection{Provider: "deepseek", Model: "deepseek-test", Explicit: true}
	deepSeekPolicy := newDeepSeekAIJudgeCachePolicy(selection, 1, body)
	deepSeek := &deepseekAIJudgeArtifact{
		Chapter:              1,
		GeneratedAt:          time.Now().UTC().Format(time.RFC3339),
		CacheKey:             reviewExistingCacheKey(deepSeekPolicy),
		CachePolicy:          deepSeekPolicy,
		Provider:             selection.Provider,
		Model:                selection.Model,
		ReviewerExplicit:     selection.Explicit,
		ReasoningEffort:      string(deepseekAIJudgeReasoningEffort),
		RawBodyOnly:          true,
		UserPayloadKind:      "chapter_body_only",
		BodySHA256:           reviewreport.BodySHA256(body),
		Verdict:              "human_like",
		RiskLevel:            "low",
		AIProbabilityPercent: 2,
		PassExclusivePercent: deepseekAIJudgePassExclusive,
		Confidence:           "high",
		AdviceComplete:       true,
		AttemptCount:         1,
		Summary:              "裸正文外审通过。",
		RawResponse:          deepseekCompleteHumanResponse,
		ModelSelection:       selection,
	}
	if err := saveDeepSeekAIJudgeCache(dir, deepSeek); err != nil {
		t.Fatal(err)
	}
	if err := saveDeepSeekAIJudge(dir, deepSeek); err != nil {
		t.Fatal(err)
	}
	if err := appendDeepSeekAIJudgeToUnifiedMarkdown(dir, deepSeek); err != nil {
		t.Fatal(err)
	}
	var finalEditor domain.ReviewEntry
	readJSONFileForFreshness(t, filepath.Join(dir, "reviews", "01.json"), &finalEditor)
	return dir, st, editorCache, &finalEditor, deepSeek
}

func reviewStyleRequirementTestManifest(version string) pipelineRenderCandidateManifest {
	return pipelineRenderCandidateManifest{
		Version:                     version,
		CandidateID:                 "render-ch0001-review-style-requirement",
		GenerationID:                "pg2_review_style_requirement",
		Chapter:                     1,
		PlanDigest:                  pipelineBytesSHA([]byte("plan")),
		PlanCheckpointSeq:           1,
		ProjectedBundleDigest:       pipelineBytesSHA([]byte("bundle")),
		PromotionReceiptDigest:      pipelineBytesSHA([]byte("promotion")),
		PipelineRenderInputDigest:   pipelineBytesSHA([]byte("render input")),
		RenderContextSHA256:         pipelineBytesSHA([]byte("render context")),
		EffectiveStyleReceiptDigest: pipelineBytesSHA([]byte("style receipt")),
	}
}

func TestReviewModelProvenanceJSONBindsArtifactPathsAndDigests(t *testing.T) {
	dir, st, editorCache, finalEditor, deepSeek := newReviewModelProvenanceFixture(t)
	if err := persistReviewModelProvenance(
		st, 1, reviewreport.BodySHA256("第一章正文"), editorCache, finalEditor, deepSeek,
	); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "reviews", "01_model_provenance.json"))
	if err != nil {
		t.Fatal(err)
	}
	var provenance reviewModelProvenance
	if err := json.Unmarshal(raw, &provenance); err != nil {
		t.Fatal(err)
	}
	if provenance.Version != reviewModelProvenanceVersion || len(provenance.Artifacts) != 8 {
		t.Fatalf("provenance artifact set incomplete: %+v", provenance)
	}
	for _, binding := range provenance.Artifacts {
		if binding.Path == "" || binding.SHA256 == "" || binding.Branch == "" {
			t.Fatalf("incomplete artifact binding: %+v", binding)
		}
	}
}
