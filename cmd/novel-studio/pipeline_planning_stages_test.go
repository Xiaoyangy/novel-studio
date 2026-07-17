package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/tools"
)

func freezeTestDraftRenderContext(t *testing.T, st *store.Store, chapter int, planDigest string) *tools.FrozenDraftRenderContext {
	t.Helper()
	frozen, err := tools.FreezeDraftRenderContext(
		context.Background(),
		tools.NewContextTool(st, tools.References{}, "default"),
		chapter,
		planDigest,
	)
	if err != nil {
		t.Fatalf("freeze test draft render context: %v", err)
	}
	return frozen
}

func TestRequirePipelineRenderBindingForSealedWritingMode(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	legacy := &pipelineFrozenPlan{ProjectionBinding: "detailed_projection"}
	if err := requirePipelineRenderBindingForWritingMode(st, legacy); err != nil {
		t.Fatalf("legacy project without sealed mode should remain compatible: %v", err)
	}

	receipt := domain.WritingPipelineModeReceipt{
		Version:     domain.WritingPipelineModeReceiptVersion,
		Mode:        domain.WritingPipelineModeSealedTwoPassV2,
		ActivatedAt: "2026-07-16T00:00:00Z",
	}
	digest, err := domain.ComputeWritingPipelineModeReceiptDigest(receipt)
	if err != nil {
		t.Fatal(err)
	}
	receipt.ReceiptDigest = digest
	if err := st.SaveWritingPipelineMode(receipt); err != nil {
		t.Fatal(err)
	}
	if err := requirePipelineRenderBindingForWritingMode(st, legacy); err == nil ||
		!strings.Contains(err.Error(), "只能消费 promote 发布的 sealed_v2") {
		t.Fatalf("sealed mode accepted a compatibility frozen plan: %v", err)
	}
	sealed := &pipelineFrozenPlan{ProjectionBinding: "sealed_v2"}
	if err := requirePipelineRenderBindingForWritingMode(st, sealed); err != nil {
		t.Fatalf("sealed mode rejected a mechanically promoted binding: %v", err)
	}
	if err := st.RunMeta.SetPendingSteer("临时改变本章真相"); err != nil {
		t.Fatal(err)
	}
	if err := requirePipelineRenderBindingForWritingMode(st, sealed); err == nil ||
		!strings.Contains(err.Error(), "禁止消费未封存的 pending steer") {
		t.Fatalf("sealed render consumed an unsealed steer: %v", err)
	}
}

func TestPipelineRequireRenderAttemptAvailableStopsBeforeQuarantine(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Checkpoints.Append(
		domain.ChapterScope(1),
		"plan",
		"drafts/01.plan.json",
		"plan-epoch",
	); err != nil {
		t.Fatal(err)
	}
	for _, digest := range []string{"body-a", "body-b", "body-c"} {
		if _, err := st.Checkpoints.Append(
			domain.ChapterScope(1),
			"draft-structural-block",
			"drafts/01.draft.md",
			digest,
		); err != nil {
			t.Fatal(err)
		}
	}
	if err := pipelineRequireRenderAttemptAvailable(st, 1); err == nil ||
		!strings.Contains(err.Error(), "冻结计划和 world simulation 保持不变") {
		t.Fatalf("exhausted render epoch must stop before quarantine: %v", err)
	}
	if cp := st.Checkpoints.LatestByStep(domain.ChapterScope(1), "plan"); cp == nil || cp.Digest != "plan-epoch" {
		t.Fatalf("render preflight mutated the frozen plan checkpoint: %+v", cp)
	}
}

func TestReloadPipelineStoreSeesHeadlessCommitCheckpoint(t *testing.T) {
	dir := t.TempDir()
	stale := store.NewStore(dir)
	if err := stale.Init(); err != nil {
		t.Fatal(err)
	}
	writer := store.NewStore(dir)
	if _, err := writer.Checkpoints.Append(
		domain.ChapterScope(1),
		"commit",
		"chapters/01.md",
		"headless-commit",
	); err != nil {
		t.Fatal(err)
	}
	if cp := stale.Checkpoints.LatestByStep(domain.ChapterScope(1), "commit"); cp != nil {
		t.Fatalf("fixture no longer models the stale Store cache: %+v", cp)
	}
	fresh := reloadPipelineStore(dir)
	if cp := fresh.Checkpoints.LatestByStep(domain.ChapterScope(1), "commit"); cp == nil || cp.Digest != "headless-commit" {
		t.Fatalf("render Store reload did not see headless commit: %+v", cp)
	}
}

func TestPipelineRequiredFileSHAMatchesCommitCheckpointDigest(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Drafts.SaveFinalChapter(1, "已提交正文"); err != nil {
		t.Fatal(err)
	}
	cp, err := st.Checkpoints.AppendArtifact(domain.ChapterScope(1), "commit", "chapters/01.md")
	if err != nil {
		t.Fatal(err)
	}
	bodySHA, err := pipelineRequiredFileSHA(dir, "chapters/01.md")
	if err != nil {
		t.Fatal(err)
	}
	if cp.Digest != bodySHA || !strings.HasPrefix(bodySHA, "sha256:") {
		t.Fatalf("commit and file digest domains differ: commit=%s file=%s", cp.Digest, bodySHA)
	}
}

func TestPipelineRenderDetectsPostCommitFinalizeRecovery(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	baseline := []int{1, 2, 3}
	baselineDigest, err := domain.DeterministicPlanningHash(baseline)
	if err != nil {
		t.Fatal(err)
	}
	frozen := &pipelineFrozenPlan{
		Chapter:                 1,
		BaselineCommitSeq:       0,
		BaselineCompletedDigest: baselineDigest,
	}
	if _, err := st.Checkpoints.Append(
		domain.ChapterScope(1),
		"commit",
		"chapters/01.md",
		"sha256:durable-body",
	); err != nil {
		t.Fatal(err)
	}
	if _, recovery := pipelineCommittedAfterFrozenBaseline(st, frozen); !recovery {
		t.Fatal("durable commit without render receipt was not detected as finalize recovery")
	}
	if _, err := os.Stat(filepath.Join(dir, pipelineRenderReceiptPath)); !os.IsNotExist(err) {
		t.Fatalf("fixture unexpectedly has a render receipt: %v", err)
	}
	if err := validatePipelinePostCommitProgressBoundary(&domain.Progress{
		CompletedChapters: baseline,
	}, frozen); err != nil {
		t.Fatalf("rewrite commit should preserve completed set: %v", err)
	}

	newChapterFrozen := *frozen
	newChapterFrozen.Chapter = 4
	if err := validatePipelinePostCommitProgressBoundary(&domain.Progress{
		CompletedChapters: []int{1, 2, 3, 4},
	}, &newChapterFrozen); err != nil {
		t.Fatalf("new chapter commit should advance only its own completed slot: %v", err)
	}
	if err := validatePipelinePostCommitProgressBoundary(&domain.Progress{
		CompletedChapters: []int{1, 2, 3, 4, 5},
	}, &newChapterFrozen); err == nil {
		t.Fatal("recovery accepted unrelated completed-chapter drift")
	}
}

func TestPipelinePostCommitRecoveryRejectsOtherChapterDrift(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Drafts.SaveFinalChapter(1, "第一章基线"); err != nil {
		t.Fatal(err)
	}
	if err := st.Drafts.SaveFinalChapter(2, "第二章基线"); err != nil {
		t.Fatal(err)
	}
	progress := &domain.Progress{CompletedChapters: []int{1, 2}}
	baseline, err := pipelineCompletedChapterSHA256(dir, progress)
	if err != nil {
		t.Fatal(err)
	}
	frozen := &pipelineFrozenPlan{Chapter: 1, BaselineChapterSHA256: baseline}
	if err := st.Drafts.SaveFinalChapter(1, "第一章已按冻结计划提交"); err != nil {
		t.Fatal(err)
	}
	if err := validatePipelinePostCommitChapterBoundary(dir, frozen); err != nil {
		t.Fatalf("target chapter commit should be the only allowed body drift: %v", err)
	}
	if err := st.Drafts.SaveFinalChapter(2, "第二章被意外改动"); err != nil {
		t.Fatal(err)
	}
	if err := validatePipelinePostCommitChapterBoundary(dir, frozen); err == nil ||
		!strings.Contains(err.Error(), "非目标章节") {
		t.Fatalf("recovery accepted simultaneous N-1 chapter drift: %v", err)
	}
}

func TestFirstChapterPostCommitRecoveryAcceptsExplicitEmptyBaseline(t *testing.T) {
	dir := t.TempDir()
	frozen := &pipelineFrozenPlan{
		Chapter:               1,
		BaselineChapterSHA256: map[string]string{},
	}
	if err := validatePipelinePostCommitChapterBoundary(dir, frozen); err != nil {
		t.Fatalf("first chapter has a legitimate zero-chapter freeze baseline: %v", err)
	}
	frozen.BaselineChapterSHA256 = nil
	if err := validatePipelinePostCommitChapterBoundary(dir, frozen); err == nil {
		t.Fatal("legacy/missing chapter baseline must remain distinguishable from an explicit empty map")
	}
}

func TestResolvePipelinePlanningStageAliasesAndPreservesDefault(t *testing.T) {
	got, err := resolveStages("")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, []string{
		"architect", "outline-all", "zero-init", "preplan", "project-all", "seal", "promote", "render",
	}) {
		t.Fatalf("default pipeline changed: %v", got)
	}
	got, err = resolveStages("batch-plan,batch_plan,pre-plan,plan,render")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, []string{"preplan", "preplan", "preplan", "plan", "render"}) {
		t.Fatalf("planning aliases = %v", got)
	}
}

func TestStandalonePreplanDoesNotRequireQdrant(t *testing.T) {
	if pipelineStagesNeedQdrant([]string{"outline-all"}) {
		t.Fatal("standalone outline-all must use only its frozen visible context and never initialize live Qdrant")
	}
	if pipelineStagesNeedQdrant([]string{"preplan"}) {
		t.Fatal("standalone preplan must remain local and Qdrant-independent")
	}
	if pipelineStagesNeedQdrant([]string{"batch-plan", "pre-plan"}) {
		t.Fatal("preplan aliases must remain Qdrant-independent")
	}
	if pipelineStagesNeedQdrant([]string{"render"}) {
		t.Fatal("frozen render must not perform live retrieval or require Qdrant")
	}
	for _, stages := range [][]string{
		{"plan"},
		{"preplan", "plan"},
		defaultPipelineStages,
	} {
		if !pipelineStagesNeedQdrant(stages) {
			t.Fatalf("stages %v must preserve Qdrant startup", stages)
		}
	}
}

func TestPipelineCausalSkeletonReservesEveryStableSlotAndSkipsV0Shell(t *testing.T) {
	volumes := []domain.VolumeOutline{
		{Index: 0},
		{
			Index: 1,
			Title: "卷一",
			Theme: "立足",
			Arcs: []domain.ArcOutline{
				{
					Index: 1,
					Title: "开局",
					Goal:  "取得第一份筹码",
					Chapters: []domain.OutlineEntry{
						{Title: "一", CoreEvent: "找到入口", Hook: "门后有人"},
						{Title: "二", CoreEvent: "付出代价", Hook: "账本出现"},
					},
				},
				{Index: 2, Title: "扩张", Goal: "站稳脚跟", EstimatedChapters: 3},
			},
		},
		{
			Index: 2,
			Title: "卷二",
			Theme: "反攻",
			Arcs: []domain.ArcOutline{{
				Index: 1,
				Title: "回收",
				Goal:  "兑现前债",
				Chapters: []domain.OutlineEntry{
					{Title: "六", CoreEvent: "收回线索", Hook: "旧债翻面"},
					{Title: "七", CoreEvent: "完成闭环", Hook: "新门打开"},
				},
			}},
		},
	}
	book, volumeSkeletons, projections, total, err := pipelineBuildCausalSkeletons(volumes, nil)
	if err != nil {
		t.Fatal(err)
	}
	if total != 7 || len(book) != 2 || len(volumeSkeletons) != 2 {
		t.Fatalf("total/book/volumes = %d/%d/%d", total, len(book), len(volumeSkeletons))
	}
	for chapter := 1; chapter <= total; chapter++ {
		if _, ok := projections[chapter]; !ok {
			t.Fatalf("stable chapter slot %d missing", chapter)
		}
	}
	if projections[3].Level != pipelineProjectionCoarse || projections[5].Level != pipelineProjectionCoarse {
		t.Fatalf("skeleton slots not marked coarse: %#v %#v", projections[3], projections[5])
	}
	if projections[6].Level != pipelineProjectionExpanded || projections[6].Entry.Title != "六" {
		t.Fatalf("later expanded arc lost stable numbering: %#v", projections[6])
	}
	if volumeSkeletons[0].ChapterFrom != 1 || volumeSkeletons[0].ChapterTo != 5 ||
		volumeSkeletons[1].ChapterFrom != 6 || volumeSkeletons[1].ChapterTo != 7 {
		t.Fatalf("volume ranges = %#v", volumeSkeletons)
	}
}

func TestPipelineSyncStableFlatOutlineMigratesLaterExpandedChapterNumbers(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	volumes := []domain.VolumeOutline{{
		Index: 1,
		Arcs: []domain.ArcOutline{
			{Index: 1, Chapters: []domain.OutlineEntry{{Title: "一"}, {Title: "二"}}},
			{Index: 2, EstimatedChapters: 3},
			{Index: 3, Chapters: []domain.OutlineEntry{{Title: "六"}, {Title: "七"}}},
		},
	}}
	existing := []domain.OutlineEntry{
		{Chapter: 1, Title: "一"},
		{Chapter: 2, Title: "二"},
		{Chapter: 3, Title: "六"},
		{Chapter: 4, Title: "七"},
	}
	if err := st.Outline.SaveOutline(existing); err != nil {
		t.Fatal(err)
	}
	stable, err := pipelineSyncStableFlatOutline(st, volumes, existing)
	if err != nil {
		t.Fatal(err)
	}
	gotNumbers := []int{stable[0].Chapter, stable[1].Chapter, stable[2].Chapter, stable[3].Chapter}
	if !slices.Equal(gotNumbers, []int{1, 2, 6, 7}) {
		t.Fatalf("stable numbers = %v", gotNumbers)
	}
	reloaded, err := st.Outline.LoadOutline()
	if err != nil {
		t.Fatal(err)
	}
	if reloaded[2].Chapter != 6 || reloaded[3].Chapter != 7 {
		t.Fatalf("flat outline was not persisted: %#v", reloaded)
	}
}

func TestLoadAndVerifyFrozenPlanRejectsMissingAndDriftedPlan(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := loadAndVerifyPipelineFrozenPlan(dir); err == nil || !strings.Contains(err.Error(), "缺少冻结计划") {
		t.Fatalf("missing freeze error = %v", err)
	}
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Drafts.SaveChapterPlan(domain.ChapterPlan{Chapter: 1, Title: "冻结计划"}); err != nil {
		t.Fatal(err)
	}
	cp, err := st.Checkpoints.AppendArtifact(domain.ChapterScope(1), "plan", "drafts/01.plan.json")
	if err != nil {
		t.Fatal(err)
	}
	renderDependencies, err := capturePipelineFrozenRenderDependencies(dir)
	if err != nil {
		t.Fatal(err)
	}
	renderContext := freezeTestDraftRenderContext(t, st, 1, cp.Digest)
	frozen := pipelineFrozenPlan{
		Version:                pipelinePlanningSchema,
		Chapter:                1,
		PlanPath:               "drafts/01.plan.json",
		PlanDigest:             cp.Digest,
		PlanCheckpointSeq:      cp.Seq,
		RenderDependencySHA256: renderDependencies,
		RenderContextPath:      tools.FrozenDraftRenderContextPath,
		RenderContextSHA256:    renderContext.PayloadSHA256,
	}
	if _, err := writePipelinePlanningJSON(filepath.Join(dir, pipelineFrozenPlanPath), frozen); err != nil {
		t.Fatal(err)
	}
	if _, _, err := loadAndVerifyPipelineFrozenPlan(dir); err != nil {
		t.Fatalf("valid frozen plan: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "drafts", "01.plan.json"), []byte(`{"chapter":1,"title":"漂移"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := loadAndVerifyPipelineFrozenPlan(dir); err == nil || !strings.Contains(err.Error(), "失效") {
		t.Fatalf("drifted plan error = %v", err)
	}
}

func TestExplicitPlanRestartArchivesStagedPartialBeforeFreshEpoch(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	partialPath := filepath.Join(dir, "drafts", "01.plan.partial.json")
	partial := []byte(`{"structure":{"chapter":1,"goal":"旧提示生成的目标"},"causal_simulation":{"context_sources":["old"]}}`)
	if err := os.WriteFile(partialPath, partial, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := retirePipelinePlanPartial(st, 1, "test explicit restart"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(partialPath); !os.IsNotExist(err) {
		t.Fatalf("retired staged partial still active: %v", err)
	}
	matches, err := filepath.Glob(filepath.Join(
		dir, "meta", "planning", "retired_formal_plans", "ch000001", "*-plan-partial.json",
	))
	if err != nil || len(matches) != 1 {
		t.Fatalf("staged partial archive missing: matches=%v err=%v", matches, err)
	}
	got, err := os.ReadFile(matches[0])
	if err != nil || !slices.Equal(got, partial) {
		t.Fatalf("staged partial archive changed bytes: got=%q err=%v", got, err)
	}
	metaMatches, err := filepath.Glob(strings.TrimSuffix(matches[0], ".json") + ".retirement.json")
	if err != nil || len(metaMatches) != 1 {
		t.Fatalf("staged partial retirement receipt missing: matches=%v err=%v", metaMatches, err)
	}
}

func TestFrozenPlanRejectsUserRulesChapterWordsDrift(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Drafts.SaveChapterPlan(domain.ChapterPlan{Chapter: 1, Title: "已冻结计划"}); err != nil {
		t.Fatal(err)
	}
	cp, err := st.Checkpoints.AppendArtifact(domain.ChapterScope(1), "plan", "drafts/01.plan.json")
	if err != nil {
		t.Fatal(err)
	}
	userRulesPath := filepath.Join(dir, "meta", "user_rules.json")
	if err := os.WriteFile(userRulesPath, []byte(`{"structured":{"chapter_words":{"min":2200,"max":2600}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	dependencies, err := capturePipelineFrozenRenderDependencies(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, rel := range pipelineFrozenRenderDependencyPaths {
		if _, ok := dependencies[rel]; !ok {
			t.Fatalf("frozen dependencies missing %s: %#v", rel, dependencies)
		}
	}
	renderContext := freezeTestDraftRenderContext(t, st, 1, cp.Digest)
	frozen := pipelineFrozenPlan{
		Version:                pipelinePlanningSchema,
		Chapter:                1,
		PlanPath:               "drafts/01.plan.json",
		PlanDigest:             cp.Digest,
		PlanCheckpointSeq:      cp.Seq,
		RenderDependencySHA256: dependencies,
		RenderContextPath:      tools.FrozenDraftRenderContextPath,
		RenderContextSHA256:    renderContext.PayloadSHA256,
	}
	if _, err := writePipelinePlanningJSON(filepath.Join(dir, pipelineFrozenPlanPath), frozen); err != nil {
		t.Fatal(err)
	}
	if _, _, err := loadAndVerifyPipelineFrozenPlan(dir); err != nil {
		t.Fatalf("unchanged frozen plan identity should verify: %v", err)
	}
	if err := validatePipelineFrozenRenderDependencies(dir, &frozen); err != nil {
		t.Fatalf("unchanged hard render dependencies should verify before prose: %v", err)
	}
	if err := os.WriteFile(userRulesPath, []byte(`{"structured":{"chapter_words":{"min":2600,"max":3200}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := loadAndVerifyPipelineFrozenPlan(dir); err != nil {
		t.Fatalf("post-commit plan identity checks must not require mutable review assets to stay byte-identical: %v", err)
	}
	if err := validatePipelineFrozenRenderDependencies(dir, &frozen); err == nil ||
		!strings.Contains(err.Error(), "meta/user_rules.json") ||
		!strings.Contains(err.Error(), "硬渲染依赖") {
		t.Fatalf("changed chapter_words should invalidate frozen render dependencies: %v", err)
	}
}

func TestPlanProseSnapshotAllowsProgressStartButRejectsDraftMutation(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	progress := &domain.Progress{Phase: domain.PhaseWriting, TotalChapters: 3}
	if err := st.Progress.Save(progress); err != nil {
		t.Fatal(err)
	}
	before, err := capturePipelinePlanProseSnapshot(st, progress, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.StartChapter(1); err != nil {
		t.Fatal(err)
	}
	if err := verifyPipelinePlanDidNotWriteProse(st, progress, 1, before); err != nil {
		t.Fatalf("StartChapter is planning state, not prose completion: %v", err)
	}
	if err := st.Drafts.SaveDraft(1, "越权正文"); err != nil {
		t.Fatal(err)
	}
	if err := verifyPipelinePlanDidNotWriteProse(st, progress, 1, before); err == nil || !strings.Contains(err.Error(), "正文草稿") {
		t.Fatalf("draft mutation error = %v", err)
	}
}

func TestPipelineReviewAcceptedForProjectionMatchesDeliveryDisposition(t *testing.T) {
	for _, disposition := range []string{"否", "可选"} {
		if !pipelineReviewAcceptedForProjection("accept", disposition) {
			t.Fatalf("accept/%s should realize projection", disposition)
		}
	}
	for _, disposition := range []string{"是", "待定", ""} {
		if pipelineReviewAcceptedForProjection("accept", disposition) {
			t.Fatalf("accept/%s must not realize projection", disposition)
		}
	}
	if pipelineReviewAcceptedForProjection("rewrite", "否") {
		t.Fatal("rewrite verdict must not realize projection")
	}
}

func TestProjectedPayloadAndFrozenStateRootsAreBoundExactly(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	payload := pipelineProjectedChapterPayload{
		Version:         pipelinePlanningSchema,
		GenerationID:    "generation-1",
		Chapter:         4,
		Volume:          1,
		Arc:             1,
		Formal:          false,
		Authority:       "speculative",
		ProjectionLevel: pipelineProjectionExpanded,
		Outline: domain.OutlineEntry{
			Chapter:   4,
			Title:     "第四章",
			CoreEvent: "兑现前因",
			Hook:      "留下后果",
		},
		Notice: "非正史",
	}
	planRel := "meta/planning/generations/test/chapters/000004.projected.json"
	planSHA, err := writePipelinePlanningJSON(filepath.Join(dir, filepath.FromSlash(planRel)), payload)
	if err != nil {
		t.Fatal(err)
	}
	projectionRoot, err := domain.DeterministicPlanningHash(payload)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint, err := domain.NewDependencyFingerprint("generation-1", "canon-root", []domain.PlanningDependency{{
		Kind: "outline", ID: "layered_outline.json", SHA256: "outline-sha",
	}})
	if err != nil {
		t.Fatal(err)
	}
	postRoot, err := domain.DeriveProjectedStateRoot(
		4,
		"generation-1",
		"canon-root",
		fingerprint.RootSHA256,
		"canon-root",
		projectionRoot,
	)
	if err != nil {
		t.Fatal(err)
	}
	manifest := domain.StagedChapterPlanManifest{
		Version:               domain.PlanningStoreVersion,
		Chapter:               4,
		Volume:                1,
		GenerationID:          "generation-1",
		BaseCanonChapter:      3,
		BaseCanonRoot:         "canon-root",
		DependencyFingerprint: fingerprint,
		Authority:             domain.PlanningAuthoritySpeculative,
		Realization:           domain.PlanningRealizationStaged,
		PlanPath:              planRel,
		PlanSHA256:            planSHA,
		ProjectedState: domain.ProjectedStateReceipt{
			Version:        domain.PlanningStoreVersion,
			Chapter:        4,
			GenerationID:   "generation-1",
			BaseCanonRoot:  "canon-root",
			DependencyRoot: fingerprint.RootSHA256,
			Authority:      domain.PlanningAuthorityProjected,
			Realization:    domain.PlanningRealizationStaged,
			PreStateRoot:   "canon-root",
			ProjectionRoot: projectionRoot,
			PostStateRoot:  postRoot,
		},
	}
	if err := st.Planning.ReplaceStagedChapterPlanManifests([]domain.StagedChapterPlanManifest{manifest}); err != nil {
		t.Fatal(err)
	}
	loaded, err := st.Planning.LoadStagedChapterPlanManifest(4)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := loadAndVerifyPipelineProjectedPayload(dir, loaded); err != nil {
		t.Fatalf("valid projected payload binding: %v", err)
	}
	frozen := &pipelineFrozenPlan{
		ProjectionBinding:      "detailed_projection",
		ProjectedPlanSHA256:    planSHA,
		ProjectedPreStateRoot:  "canon-root",
		ProjectedPostStateRoot: postRoot,
	}
	if err := validatePipelineFrozenProjectionBinding(frozen, loaded); err != nil {
		t.Fatalf("valid frozen projection binding: %v", err)
	}
	driftedFrozen := *frozen
	driftedFrozen.ProjectedPostStateRoot = "drifted-post-root"
	if err := validatePipelineFrozenProjectionBinding(&driftedFrozen, loaded); err == nil ||
		!strings.Contains(err.Error(), "pre-state/post-state") {
		t.Fatalf("drifted frozen state root should fail: %v", err)
	}

	payload.Outline.Hook = "payload changed without updating projection root"
	newSHA, err := writePipelinePlanningJSON(filepath.Join(dir, filepath.FromSlash(planRel)), payload)
	if err != nil {
		t.Fatal(err)
	}
	tampered := *loaded
	tampered.PlanSHA256 = newSHA
	if _, err := loadAndVerifyPipelineProjectedPayload(dir, &tampered); err == nil ||
		!strings.Contains(err.Error(), "payload root") {
		t.Fatalf("payload/projection root drift should fail: %v", err)
	}
}

func TestRebaseBoundaryAllowsRemainingCanonicalRewritesButBlocksFuture(t *testing.T) {
	receipt := pipelinePreplanReceipt{
		BaseCanonChapter:           3,
		RebaseRequiredBeforeFuture: true,
	}
	for _, chapter := range []int{1, 2, 3} {
		if err := validatePipelineRebaseBoundary(receipt, chapter); err != nil {
			t.Fatalf("canonical rewrite chapter %d should remain actionable: %v", chapter, err)
		}
	}
	if err := validatePipelineRebaseBoundary(receipt, 4); err == nil ||
		!strings.Contains(err.Error(), "重跑 preplan") {
		t.Fatalf("future chapter must wait for rebase: %v", err)
	}
	refreshed := receipt
	refreshed.BaseCanonChapter = 3
	refreshed.RebaseRequiredBeforeFuture = false
	if err := validatePipelineRebaseBoundary(refreshed, 4); err != nil {
		t.Fatalf("fresh preplan should clear rebase boundary: %v", err)
	}
}

func TestSplitPipelineCycleRebaseClearsStageCursorAndAdvancesActionableChapter(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Save(&domain.Progress{
		Phase:             domain.PhaseWriting,
		TotalChapters:     3,
		CompletedChapters: []int{1},
	}); err != nil {
		t.Fatal(err)
	}
	receipt := pipelinePreplanReceipt{
		Version:                    pipelinePlanningSchema,
		GenerationID:               "generation-1",
		BaseCanonRoot:              "sha256:base",
		CurrentCanonRoot:           "sha256:after-chapter-1",
		DependencyRoot:             "sha256:dependencies",
		RebaseRequiredBeforeFuture: true,
	}
	if _, err := writePipelinePlanningJSON(filepath.Join(dir, pipelinePlanningReceiptPath), receipt); err != nil {
		t.Fatal(err)
	}
	state := &domain.PipelineState{
		Stages:    []string{"preplan", "plan", "render"},
		Completed: []string{"preplan", "plan", "render"},
	}
	for _, stage := range state.Completed {
		state.MarkDone(stage, domain.PipelineStageEvidence{Stage: stage, Status: "verified"})
	}
	reset, err := resetCompletedSplitPipelineCycle(dir, state)
	if err != nil {
		t.Fatal(err)
	}
	if !reset {
		t.Fatal("render rebase flag did not open a new split-stage cycle")
	}
	for _, stage := range []string{"preplan", "plan", "render"} {
		if state.Done(stage) {
			t.Fatalf("%s remained completed across chapter cycle boundary", stage)
		}
		if got := state.Evidence[stage].Status; got != "next_cycle" {
			t.Fatalf("%s evidence status = %q, want next_cycle", stage, got)
		}
	}
	actionable, _, err := pipelineCurrentActionableChapter(st, pipelineFlags{})
	if err != nil {
		t.Fatal(err)
	}
	if actionable != 2 {
		t.Fatalf("next actionable chapter = %d, want 2", actionable)
	}
	if _, err := verifyPipelinePreplanStage(dir, domain.PipelineStageEvidence{Stage: "preplan"}); !errors.Is(err, errPipelinePreplanRebaseRequired) {
		t.Fatalf("rebase-required preplan evidence should be stale: %v", err)
	}
}

func TestSplitPipelineCycleDoesNotOpenAfterBookCompletion(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Save(&domain.Progress{
		Phase:             domain.PhaseComplete,
		TotalChapters:     1,
		CompletedChapters: []int{1},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := writePipelinePlanningJSON(filepath.Join(dir, pipelinePlanningReceiptPath), pipelinePreplanReceipt{
		Version:                    pipelinePlanningSchema,
		GenerationID:               "generation-1",
		BaseCanonRoot:              "sha256:base",
		CurrentCanonRoot:           "sha256:after-final-chapter",
		DependencyRoot:             "sha256:dependencies",
		RebaseRequiredBeforeFuture: true,
	}); err != nil {
		t.Fatal(err)
	}
	state := &domain.PipelineState{
		Stages:    []string{"preplan", "plan", "render"},
		Completed: []string{"preplan", "plan", "render"},
	}
	reset, err := resetCompletedSplitPipelineCycle(dir, state)
	if err != nil {
		t.Fatal(err)
	}
	if reset {
		t.Fatal("completed book opened a split-stage cycle with no actionable chapter")
	}
	for _, stage := range state.Stages {
		if !state.Done(stage) {
			t.Fatalf("%s completion was cleared after the final chapter", stage)
		}
	}
}

func TestSplitPipelinePreservesOldCycleUntilPostCommitRenderReceiptCloses(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init("recovery", 2); err != nil {
		t.Fatal(err)
	}
	if err := st.Drafts.SaveChapterPlan(domain.ChapterPlan{Chapter: 1, Title: "冻结计划"}); err != nil {
		t.Fatal(err)
	}
	planCP, err := st.Checkpoints.AppendArtifact(domain.ChapterScope(1), "plan", "drafts/01.plan.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Checkpoints.Append(domain.ChapterScope(1), "commit", "chapters/01.md", "sha256:committed"); err != nil {
		t.Fatal(err)
	}
	renderContext := freezeTestDraftRenderContext(t, st, 1, planCP.Digest)
	frozen := pipelineFrozenPlan{
		Version:                pipelinePlanningSchema,
		Chapter:                1,
		PlanPath:               "drafts/01.plan.json",
		PlanDigest:             planCP.Digest,
		PlanCheckpointSeq:      planCP.Seq,
		BaselineCommitSeq:      0,
		BaselineChapterSHA256:  map[string]string{},
		RenderDependencySHA256: map[string]string{"meta/user_rules.json": pipelineMissingDependency},
		PlanningGenerationID:   "generation-1",
		PlanningDependencyRoot: "dependency-1",
		PipelineRunInputDigest: "sha256:run-input",
		RenderContextPath:      tools.FrozenDraftRenderContextPath,
		RenderContextSHA256:    renderContext.PayloadSHA256,
		ProjectionBinding:      "canonical_rewrite_rebase_required",
	}
	if _, err := writePipelinePlanningJSON(filepath.Join(dir, pipelineFrozenPlanPath), frozen); err != nil {
		t.Fatal(err)
	}
	if _, err := writePipelinePlanningJSON(filepath.Join(dir, pipelinePlanningReceiptPath), pipelinePreplanReceipt{
		Version:                    pipelinePlanningSchema,
		GenerationID:               "generation-1",
		BaseCanonRoot:              "base",
		CurrentCanonRoot:           "after-commit",
		DependencyRoot:             "dependency-1",
		RebaseRequiredBeforeFuture: false,
	}); err != nil {
		t.Fatal(err)
	}
	state := &domain.PipelineState{
		Stages:    []string{"preplan", "plan", "render"},
		Completed: []string{"preplan", "plan"},
	}
	pending, err := splitPipelineRenderRecoveryPending(dir, state)
	if err != nil || !pending {
		t.Fatalf("commit without render receipt must preserve the old cycle: pending=%v err=%v", pending, err)
	}
	if reset, err := resetCompletedSplitPipelineCycle(dir, state); err != nil || reset {
		t.Fatalf("preplan/plan cursor cleared before render recovery: reset=%v err=%v", reset, err)
	}
	if !state.Done("preplan") || !state.Done("plan") {
		t.Fatalf("old cycle evidence was cleared before recovery: %+v", state.Completed)
	}
	if _, err := writePipelinePlanningJSON(filepath.Join(dir, pipelinePlanningReceiptPath), pipelinePreplanReceipt{
		Version:                    pipelinePlanningSchema,
		GenerationID:               "generation-1",
		BaseCanonRoot:              "base",
		CurrentCanonRoot:           "after-commit",
		DependencyRoot:             "dependency-1",
		RebaseRequiredBeforeFuture: true,
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := writePipelinePlanningJSON(filepath.Join(dir, pipelineRenderReceiptPath), pipelineRenderReceipt{
		Version:           pipelinePlanningSchema,
		Chapter:           1,
		PlanDigest:        planCP.Digest,
		PlanCheckpointSeq: planCP.Seq,
	}); err != nil {
		t.Fatal(err)
	}
	pending, err = splitPipelineRenderRecoveryPending(dir, state)
	if err != nil || !pending {
		t.Fatalf("receipt without pipeline MarkDone must remain recoverable: pending=%v err=%v", pending, err)
	}
	state.MarkDone("render", domain.PipelineStageEvidence{Stage: "render", Status: "verified"})
	pending, err = splitPipelineRenderRecoveryPending(dir, state)
	if err != nil || pending {
		t.Fatalf("render MarkDone did not close recovery: pending=%v err=%v", pending, err)
	}
	if reset, err := resetCompletedSplitPipelineCycle(dir, state); err != nil || !reset {
		t.Fatalf("closed render cycle did not advance to rebase: reset=%v err=%v", reset, err)
	}
}

func TestSplitPipelineRenderRecoveryAllowsFreshCycleWithoutFrozenPlan(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	state := &domain.PipelineState{
		Stages: []string{"preplan", "plan", "render"},
	}

	pending, err := splitPipelineRenderRecoveryPending(dir, state)
	if err != nil {
		t.Fatalf("fresh split pipeline must not require a frozen render plan: %v", err)
	}
	if pending {
		t.Fatal("fresh split pipeline without a committed render cannot be in recovery")
	}
}

func TestExplicitRestartNeverAdoptsUnfrozenFormalPlan(t *testing.T) {
	t.Parallel()

	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	cp, recovered, err := recoverPipelineUnfrozenFormalPlan(st, 1, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	if recovered || cp != nil {
		t.Fatalf("--restart adopted a previous formal plan: recovered=%v checkpoint=%+v", recovered, cp)
	}
}

func TestPlanFreezeReloadsCheckpointStoreAfterHeadlessRun(t *testing.T) {
	dir := t.TempDir()
	stale := store.NewStore(dir)
	if err := stale.Init(); err != nil {
		t.Fatal(err)
	}
	if err := stale.Drafts.SaveChapterPlan(domain.ChapterPlan{Chapter: 1, Title: "旧计划"}); err != nil {
		t.Fatal(err)
	}
	old, err := stale.Checkpoints.AppendArtifact(domain.ChapterScope(1), "plan", "drafts/01.plan.json")
	if err != nil {
		t.Fatal(err)
	}

	headlessStore := store.NewStore(dir)
	if err := headlessStore.SaveChapterWorldSimulation(domain.ChapterWorldSimulation{
		Version:      1,
		SimulationID: "new-simulation",
		Chapter:      1,
		TimeWindow:   "当天",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := headlessStore.Checkpoints.AppendArtifact(
		domain.ChapterScope(1),
		"chapter_world_simulation",
		"meta/chapter_simulations/001.json",
	); err != nil {
		t.Fatal(err)
	}
	if err := headlessStore.Drafts.SaveChapterPlan(domain.ChapterPlan{Chapter: 1, Title: "新计划"}); err != nil {
		t.Fatal(err)
	}
	newCheckpoint, err := headlessStore.Checkpoints.AppendArtifact(domain.ChapterScope(1), "plan", "drafts/01.plan.json")
	if err != nil {
		t.Fatal(err)
	}
	if newCheckpoint.Seq <= old.Seq || newCheckpoint.Digest == old.Digest {
		t.Fatalf("test setup did not create a new plan epoch: old=%+v new=%+v", old, newCheckpoint)
	}

	reloaded, current, err := loadPipelineCurrentFormalPlanAfterHeadless(dir, 1)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded == stale || current.Seq != newCheckpoint.Seq || current.Digest != newCheckpoint.Digest {
		t.Fatalf("freeze saw stale checkpoint: current=%+v want=%+v", current, newCheckpoint)
	}
	if !pipelinePlanCheckpointAfterLatestBoundary(reloaded.Checkpoints.All(), 1, newCheckpoint.Seq, 0) {
		t.Fatalf("new plan checkpoint should be recoverable after a newer simulation boundary")
	}
	if pipelinePlanCheckpointAfterLatestBoundary(reloaded.Checkpoints.All(), 1, old.Seq, 0) {
		t.Fatalf("old plan checkpoint must not be recoverable before the newer simulation boundary")
	}
}
