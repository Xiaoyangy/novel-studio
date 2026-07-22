package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/reviewreport"
	"github.com/chenhongyang/novel-studio/internal/store"
	toolspkg "github.com/chenhongyang/novel-studio/internal/tools"
)

const pipelineRenderConvergenceBlockingResponse = `{
	"verdict":"ai_like",
	"risk_level":"high",
	"ai_probability_percent":70,
	"confidence":"high",
	"summary":"对白和段落存在可复核的整章模板化结构。",
	"reasons":["连续十二段均由角色轮流补充流程信息，发言长度和功能高度一致","多个场景都以系统状态变化开头并以抽象结论收束，主角选择没有改变后续动作"],
	"evidence":["连续十二个对白段均在十五字左右并逐项解释下一步","三个场景重复出现页面刷新、角色确认、旁白总结的同序结构"],
	"revision_plan":["压缩重复流程，只保留真正改变主角判断的代表事件","让一次误判产生时间或关系代价，并由该代价改变下一步选择"],
	"dialogue_fix_plan":["删除只负责确认上一句的发言，让沉默或动作承担信息"],
	"author_voice_plan":["保留主角的职业判断，但让判断落到她选择保留或放弃的具体证据"],
	"rag_rules":["多人在场不等于人人发言","同类流程只完整渲染一个改变局面的样本"]
}`

func TestRenderConvergenceBudgetSurvivesCandidateRetirement(t *testing.T) {
	live, frozen, candidate := pipelineRenderConvergenceFixture(t)
	selection := deepseekAIJudgeModelSelection{
		Provider: "deepseek", Model: "deepseek-v4-pro", Explicit: true,
	}

	for index, body := range []string{
		"第一章 冻结计划\n\n林澈先把第一张回执压在桌角，等电话那头说完才抬笔。",
		"第一章 冻结计划\n\n林澈把第二张回执翻到背面，听见楼道脚步后停了两秒。",
		"第一章 冻结计划\n\n林澈没有碰第三张回执。窗外车灯扫过，她先问对方是否安全。",
	} {
		candidateStore := store.NewStore(candidate.OutputDir)
		if err := candidateStore.Drafts.SaveDraft(frozen.Chapter, body); err != nil {
			t.Fatal(err)
		}
		if _, err := candidateStore.Checkpoints.AppendArtifactLatestAcross(
			domain.ChapterScope(frozen.Chapter),
			"draft",
			"drafts/01.draft.md",
			"plan", "rerender-request", "draft", "edit",
		); err != nil {
			t.Fatal(err)
		}
		artifact, err := runDeepSeekAIJudge(
			&reviewCacheModel{response: pipelineRenderConvergenceBlockingResponse}, selection, frozen.Chapter, body, time.Second,
		)
		if err != nil {
			t.Fatal(err)
		}
		if !artifact.Blocking || !artifact.AdviceComplete {
			t.Fatalf("fixture judge is not a complete block: %+v", artifact)
		}
		if err := saveDeepSeekAIJudgeCache(candidate.OutputDir, artifact); err != nil {
			t.Fatal(err)
		}
		if err := saveDraftDeepSeekAIJudge(candidate.OutputDir, artifact); err != nil {
			t.Fatal(err)
		}
		ledger, err := syncPipelineRenderConvergence(candidateStore)
		if err != nil {
			t.Fatal(err)
		}
		if got := pipelineRenderConvergenceFailureCount(ledger); got != index+1 {
			t.Fatalf("failure count after body %d = %d, want %d; ledger=%+v", index, got, index+1, ledger)
		}
		// Re-reading the same exact hash is idempotent.
		ledger, err = syncPipelineRenderConvergence(store.NewStore(candidate.OutputDir))
		if err != nil {
			t.Fatal(err)
		}
		if got := pipelineRenderConvergenceFailureCount(ledger); got != index+1 {
			t.Fatalf("same exact hash consumed another attempt: got=%d want=%d", got, index+1)
		}
		if index == 2 {
			continue
		}
		if err := retirePipelineRenderCandidate(candidate.ContainerDir, "stale"); err != nil {
			t.Fatal(err)
		}
		candidate, err = preparePipelineRenderCandidate(live, frozen)
		if err != nil {
			t.Fatal(err)
		}
	}

	err := requirePipelineRenderConvergenceAvailable(store.NewStore(candidate.OutputDir))
	if err == nil || !pipelineRenderRequiresPlanStage(err) ||
		!strings.Contains(err.Error(), "--stages plan --restart") ||
		!strings.Contains(err.Error(), "原位保留") {
		t.Fatalf("exhausted persistent budget did not return the safe plan action: %v", err)
	}
	if _, statErr := os.Stat(candidate.ContainerDir); statErr != nil {
		t.Fatalf("budget check quarantined or removed the active candidate: %v", statErr)
	}

	// A genuinely new plan identity receives a different ledger namespace.
	newPlan := *frozen
	newPlan.PlanDigest = "sha256:new-plan"
	newPlan.PlanCheckpointSeq++
	if err := requireFrozenPipelineRenderConvergenceAvailable(live, &newPlan); err != nil {
		t.Fatalf("old plan budget leaked into a new plan identity: %v", err)
	}
}

func TestRenderConvergenceRestoresExactHashJudgeWithoutModelCall(t *testing.T) {
	_, frozen, candidate := pipelineRenderConvergenceFixture(t)
	body := "第一章 冻结计划\n\n门外响了一声。林澈没急着回答，先把录音时间抄在纸上。"
	st := store.NewStore(candidate.OutputDir)
	if err := st.Drafts.SaveDraft(frozen.Chapter, body); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Checkpoints.AppendArtifactLatestAcross(
		domain.ChapterScope(frozen.Chapter), "draft", "drafts/01.draft.md",
		"plan", "rerender-request", "draft", "edit",
	); err != nil {
		t.Fatal(err)
	}
	selection := deepseekAIJudgeModelSelection{Provider: "deepseek", Model: "deepseek-v4-pro", Explicit: true}
	artifact, err := runDeepSeekAIJudge(
		&reviewCacheModel{response: pipelineRenderConvergenceBlockingResponse}, selection, frozen.Chapter, body, time.Second,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := saveDeepSeekAIJudgeCache(candidate.OutputDir, artifact); err != nil {
		t.Fatal(err)
	}
	if err := saveDraftDeepSeekAIJudge(candidate.OutputDir, artifact); err != nil {
		t.Fatal(err)
	}
	if _, err := syncPipelineRenderConvergence(st); err != nil {
		t.Fatal(err)
	}

	judgePath := filepath.Join(candidate.OutputDir, "reviews", "drafts", "01_deepseek_ai_judge.json")
	cachePath := reviewExistingCachePath(candidate.OutputDir, deepseekAIJudgeCacheBranch, artifact.CacheKey)
	if err := os.Remove(judgePath); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(cachePath); err != nil {
		t.Fatal(err)
	}
	if err := restorePipelineRenderConvergenceJudgeEvidence(store.NewStore(candidate.OutputDir), frozen.Chapter); err != nil {
		t.Fatal(err)
	}

	model := &reviewCacheModel{response: deepseekCompleteHumanResponse}
	result := loadOrGenerateDeepSeekAIJudge(
		candidate.OutputDir, model, selection, frozen.Chapter, body, time.Second,
	)
	if result.Err != nil || !result.CacheHit || result.Artifact == nil {
		t.Fatalf("restored exact hash was not a cache hit: %+v", result)
	}
	if model.callCount() != 0 {
		t.Fatalf("restored exact hash called the judge model %d times, want 0", model.callCount())
	}
}

func TestRenderConvergenceCountsOneExactHashOnceAcrossAllFailureKinds(t *testing.T) {
	manifest := pipelineRenderCandidateManifest{
		Version:                pipelineRenderCandidatePreviousManifestVersion,
		CandidateID:            "render-ch0001-test",
		GenerationID:           "generation",
		Chapter:                1,
		PlanDigest:             "sha256:plan",
		PlanCheckpointSeq:      7,
		ProjectedBundleDigest:  "sha256:bundle",
		PromotionReceiptDigest: "sha256:promotion",
	}
	ledger := newPipelineRenderConvergenceLedger(manifest, 3)
	record := pipelineRenderConvergenceRecordFor(&ledger, strings.Repeat("a", 64))
	record.ExternalBlocking = true
	record.StructuralBlock = true
	record.SemanticReject = true
	if got := pipelineRenderConvergenceFailureCount(&ledger); got != 1 {
		t.Fatalf("one exact hash with three blockers consumed %d attempts, want 1", got)
	}
}

func TestRenderConvergenceDefersExhaustedSemanticLedgerForCachedRevalidationAndResolves(t *testing.T) {
	manifest := pipelineRenderCandidateManifest{
		Version:                pipelineRenderCandidatePreviousManifestVersion,
		CandidateID:            "render-ch0001-semantic-revalidation",
		GenerationID:           "generation",
		Chapter:                1,
		PlanDigest:             "sha256:plan",
		PlanCheckpointSeq:      7,
		ProjectedBundleDigest:  "sha256:bundle",
		PromotionReceiptDigest: "sha256:promotion",
	}
	ledger := newPipelineRenderConvergenceLedger(manifest, 3)
	for _, marker := range []string{"a", "b", "c"} {
		record := pipelineRenderConvergenceRecordFor(&ledger, strings.Repeat(marker, 64))
		record.SemanticReject = true
	}
	if err := pipelineRenderConvergenceError(&ledger); err == nil {
		t.Fatal("three unresolved formal rejects did not exhaust the strict budget")
	}
	if err := pipelineRenderConvergencePreflightError(&ledger); err != nil {
		t.Fatalf("exhausted semantic ledger blocked cache-only formal revalidation: %v", err)
	}

	resolved := pipelineRenderConvergenceRecordFor(&ledger, strings.Repeat("c", 64))
	resolved.FormalAccepted = true
	if !resolved.SemanticReject {
		t.Fatal("formal acceptance erased historical rejection audit evidence")
	}
	if got := pipelineRenderConvergenceFailureCount(&ledger); got != 2 {
		t.Fatalf("accepted exact hash still consumed prose budget: got=%d want=2", got)
	}
	if err := pipelineRenderConvergenceError(&ledger); err != nil {
		t.Fatalf("resolved exact hash did not unlock the same plan: %v", err)
	}
	for _, failed := range pipelineRenderConvergenceFailedHashes(&ledger) {
		if failed == resolved.BodySHA256 {
			t.Fatalf("resolved exact hash remained in Writer rejection guard: %s", failed)
		}
	}
}

func TestCachedFormalRevalidationIgnoresOnlyObsoleteHistoricalStructuralFlag(t *testing.T) {
	bodySHA := strings.Repeat("a", 64)
	record := pipelineRenderConvergenceRecord{
		BodySHA256: bodySHA, SemanticReject: true, StructuralBlock: true,
	}
	if pipelineRenderRecordNeedsCachedFormalRevalidation(record, bodySHA, toolspkg.DraftExternalGateRejudgePending) {
		t.Fatal("an unresolved current structural gate was treated as historical")
	}
	if !pipelineRenderRecordNeedsCachedFormalRevalidation(record, bodySHA, toolspkg.DraftExternalGateApproved) {
		t.Fatal("an exact-body approved gate did not supersede the obsolete historical structural bit")
	}
	record.ExternalBlocking = true
	if pipelineRenderRecordNeedsCachedFormalRevalidation(record, bodySHA, toolspkg.DraftExternalGateApproved) {
		t.Fatal("an external provider rejection was overridden by formal revalidation")
	}
	record.ExternalBlocking = false
	record.FormalAccepted = true
	if pipelineRenderRecordNeedsCachedFormalRevalidation(record, bodySHA, toolspkg.DraftExternalGateApproved) {
		t.Fatal("an already accepted record was revalidated again")
	}
}

func TestReviewFirstRecoveryRequiresDeepSeekCacheButNotCurrentEditorCache(t *testing.T) {
	dir := t.TempDir()
	body := "第一章 他等的从来不是外卖\n\n林澈把手从门把上收回来，重新核对屏幕里的时间。"
	cfg := bootstrap.Config{
		OutputDir: dir,
		Provider:  "test-openai",
		ModelName: "editor-test",
		Providers: map[string]bootstrap.ProviderConfig{
			"test-openai": {Type: "openai", BaseURL: "http://127.0.0.1:1"},
		},
		Roles: map[string]bootstrap.RoleConfig{
			"reviewer": {Provider: "test-openai", Model: "deepseek-test"},
		},
	}
	selection := deepseekAIJudgeModelSelection{
		Provider: "test-openai", Model: "deepseek-test", Explicit: true,
	}
	artifact, err := runDeepSeekAIJudge(
		&reviewCacheModel{response: deepseekCompleteHumanResponse},
		selection,
		1,
		body,
		time.Second,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := saveDeepSeekAIJudgeCache(dir, artifact); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "reviews", "cache", editorReviewCacheBranch)); !os.IsNotExist(err) {
		t.Fatalf("fixture unexpectedly has a current Editor cache: %v", err)
	}
	if ok, err := pipelineRenderCandidateHasCurrentExactDeepSeekReviewCache(cfg, 1, body); err != nil || !ok {
		t.Fatalf("current exact DeepSeek cache did not authorize review-first Editor refresh: ok=%v err=%v", ok, err)
	}

	cachePath := reviewExistingCachePath(dir, deepseekAIJudgeCacheBranch, artifact.CacheKey)
	if err := os.Remove(cachePath); err != nil {
		t.Fatal(err)
	}
	if ok, err := pipelineRenderCandidateHasCurrentExactDeepSeekReviewCache(cfg, 1, body); err != nil || ok {
		t.Fatalf("missing DeepSeek cache did not fail closed: ok=%v err=%v", ok, err)
	}
}

func TestRenderConvergenceBackfillsExistingSemanticTombstones(t *testing.T) {
	live, frozen, candidate := pipelineRenderConvergenceFixture(t)
	for _, marker := range []string{"d", "e", "f"} {
		bodySHA := strings.Repeat(marker, 64)
		tombstone := pipelineRenderRejectionTombstone{
			Version:                pipelineRenderRejectionTombstoneVersion,
			CandidateID:            candidate.ID,
			GenerationID:           frozen.PlanningGenerationID,
			Chapter:                frozen.Chapter,
			PlanDigest:             frozen.PlanDigest,
			PlanCheckpointSeq:      frozen.PlanCheckpointSeq,
			ProjectedBundleDigest:  frozen.ProjectedBundleDigest,
			PromotionReceiptDigest: frozen.PromotionReceiptDigest,
			BodySHA256:             bodySHA,
			Verdict:                "rewrite",
			Disposition:            "是",
			ReviewArtifacts:        pipelineRenderRequiredReviewArtifacts(frozen.Chapter),
			RejectedAt:             time.Now().UTC().Format(time.RFC3339Nano),
		}
		raw, err := json.Marshal(tombstone)
		if err != nil {
			t.Fatal(err)
		}
		path, err := pipelineRenderRejectionTombstonePath(live, candidate.ID, bodySHA)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, raw, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	ledger, err := syncPipelineRenderConvergence(store.NewStore(candidate.OutputDir))
	if err != nil {
		t.Fatal(err)
	}
	if got := pipelineRenderConvergenceFailureCount(ledger); got != 3 {
		t.Fatalf("existing exact-body tombstones were not backfilled: got=%d ledger=%+v", got, ledger)
	}
	if err := pipelineRenderConvergencePreflightError(ledger); err != nil {
		t.Fatalf("backfilled tombstones blocked their one cache-only normalization pass: %v", err)
	}
}

func TestPublishedLiveRenderManifestIsInertForNextChapterPreflight(t *testing.T) {
	live, frozen, candidate := pipelineRenderConvergenceFixture(t)
	manifest, err := loadPipelineRenderCandidateManifest(candidate.OutputDir)
	if err != nil || manifest == nil {
		t.Fatalf("load candidate manifest: manifest=%+v err=%v", manifest, err)
	}
	ledger := newPipelineRenderConvergenceLedger(*manifest, 3)
	for _, marker := range []string{"7", "8", "9"} {
		record := pipelineRenderConvergenceRecordFor(&ledger, strings.Repeat(marker, 64))
		record.ExternalBlocking = true
	}
	if err := savePipelineRenderConvergenceLedger(live, &ledger); err != nil {
		t.Fatal(err)
	}
	if err := requirePipelineRenderConvergenceAvailable(store.NewStore(candidate.OutputDir)); err == nil {
		t.Fatal("active isolated candidate did not enforce its exhausted ledger")
	}

	// Directory promotion intentionally leaves the manifest in canonical live
	// output as provenance. SourceOutputDir then equals the store containing it.
	source := filepath.Join(candidate.OutputDir, "meta", "planning", "render_candidate.json")
	target := filepath.Join(live, "meta", "planning", "render_candidate.json")
	raw, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	liveStore := store.NewStore(live)
	if synced, err := syncPipelineRenderConvergence(liveStore); err != nil || synced != nil {
		t.Fatalf("published live provenance activated old convergence ledger: ledger=%+v err=%v", synced, err)
	}
	if err := restorePipelineRenderConvergenceJudgeEvidence(liveStore, frozen.Chapter); err != nil {
		t.Fatalf("published live provenance activated judge restoration: %v", err)
	}
	if err := requirePipelineRenderConvergenceAvailable(liveStore); err != nil {
		t.Fatalf("published previous chapter blocked next chapter preflight: %v", err)
	}
}

func TestRenderConvergenceBackfillsEveryHistoricalStructuralCheckpointBody(t *testing.T) {
	live, frozen, candidate := pipelineRenderConvergenceFixture(t)
	manifest, err := loadPipelineRenderCandidateManifest(candidate.OutputDir)
	if err != nil || manifest == nil {
		t.Fatalf("load candidate manifest: manifest=%+v err=%v", manifest, err)
	}
	ledger := newPipelineRenderConvergenceLedger(*manifest, 3)
	semanticSHA := strings.Repeat("1", 64)
	pipelineRenderConvergenceRecordFor(&ledger, semanticSHA).SemanticReject = true
	if err := savePipelineRenderConvergenceLedger(live, &ledger); err != nil {
		t.Fatal(err)
	}

	st := store.NewStore(candidate.OutputDir)
	bodies := []string{
		"第一章 冻结计划\n\n" + strings.Repeat("林澈核完第一项，然后继续下一项。", 120),
		"第一章 冻结计划\n\n" + strings.Repeat("林澈核完第二项，然后继续下一项。", 120),
	}
	for _, body := range bodies {
		if err := st.Drafts.SaveDraft(frozen.Chapter, body); err != nil {
			t.Fatal(err)
		}
		if _, err := st.Checkpoints.AppendArtifactLatestAcross(
			domain.ChapterScope(frozen.Chapter),
			"draft",
			"drafts/01.draft.md",
			"plan", "rerender-request", "draft", "edit",
		); err != nil {
			t.Fatal(err)
		}
		oneWayDigest := "sha256:" + reviewreport.BodySHA256(reviewreport.BodySHA256(body)+"\nplan-epoch")
		if _, err := st.Checkpoints.Append(
			domain.ChapterScope(frozen.Chapter),
			"draft-structural-block",
			"drafts/01.draft.md",
			oneWayDigest,
		); err != nil {
			t.Fatal(err)
		}
	}

	synced, err := syncPipelineRenderConvergence(store.NewStore(candidate.OutputDir))
	if err != nil {
		t.Fatal(err)
	}
	if got := pipelineRenderConvergenceFailureCount(synced); got != 3 {
		t.Fatalf("semantic + two historical structural bodies = %d, want 3; ledger=%+v", got, synced)
	}
	for _, body := range bodies {
		want := reviewreport.BodySHA256(body)
		record := pipelineRenderConvergenceRecordFor(synced, want)
		if !record.StructuralBlock {
			t.Fatalf("preceding exact body %s was not associated with its structural checkpoint", want)
		}
	}
	if err := requirePipelineRenderConvergenceAvailable(store.NewStore(candidate.OutputDir)); err == nil || !pipelineRenderRequiresPlanStage(err) {
		t.Fatalf("restarted candidate did not stop before another draft: %v", err)
	}
}

func pipelineRenderConvergenceFixture(
	t *testing.T,
) (string, *pipelineFrozenPlan, *pipelineRenderCandidate) {
	t.Helper()
	live := pipelineRenderCandidateTestLive(t)
	st := store.NewStore(live)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	plan := domain.ChapterPlan{Chapter: 1, Title: "冻结计划"}
	plan.CausalSimulation.ReviewRefinement.IterationLimit = 3
	if err := st.Drafts.SaveChapterPlan(plan); err != nil {
		t.Fatal(err)
	}
	checkpoint, err := st.Checkpoints.AppendArtifactLatestAcross(
		domain.ChapterScope(1), "plan", "drafts/01.plan.json",
		"plan", "chapter_world_simulation", "review", "draft-structural-block",
	)
	if err != nil {
		t.Fatal(err)
	}
	frozen := pipelineRenderCandidateTestFrozen()
	frozen.PlanDigest = checkpoint.Digest
	frozen.PlanCheckpointSeq = checkpoint.Seq
	candidate, err := preparePipelineRenderCandidate(live, frozen)
	if err != nil {
		t.Fatal(err)
	}
	mustUseLegacyPipelineRenderCandidateForTest(t, candidate, frozen)
	return live, frozen, candidate
}
