package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/assets"
	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/entry/headless"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func TestPipelineFinalizeConfiguredRunsOneGlobalReviewAndWritesDeterministicManifest(t *testing.T) {
	dir := pipelineFinalizeFixture(t)
	calls := 0
	runner := func(cfg bootstrap.Config, _ assets.Bundle, opts headless.Options) error {
		calls++
		if opts.StopAfterGlobalReviewChapter != 2 {
			t.Fatalf("StopAfterGlobalReviewChapter=%d, want 2", opts.StopAfterGlobalReviewChapter)
		}
		st := store.NewStore(cfg.OutputDir)
		global := acceptedPipelineGlobalReview(2)
		if err := st.World.SaveReview(global); err != nil {
			return err
		}
		if _, err := st.Checkpoints.AppendArtifactLatestAcross(
			domain.ChapterScope(2), "review", "reviews/02-global.json", "review", "commit",
		); err != nil {
			return err
		}
		manuscript, err := buildPipelineMergedManuscript(st, []int{1, 2})
		if err != nil {
			return err
		}
		if err := st.Drafts.SaveMergedManuscript(manuscript); err != nil {
			return err
		}
		return st.Progress.MarkComplete()
	}
	cfg := bootstrap.Config{OutputDir: dir}
	if err := pipelineFinalizeConfigured(cfg, assets.Bundle{}, pipelineFlags{}, runner); err != nil {
		t.Fatalf("pipelineFinalizeConfigured: %v", err)
	}
	if calls != 1 {
		t.Fatalf("global review host calls=%d, want 1", calls)
	}
	for _, rel := range []string{
		pipelineFinalizationJSON, pipelineFinalizationMD,
		pipelinePublicationPackageJSON, pipelinePublicationPackageMD,
		"正文.md", "reviews/02-global.json",
	} {
		if !nonEmptyFile(filepath.Join(dir, filepath.FromSlash(rel))) {
			t.Fatalf("missing finalization artifact %s", rel)
		}
	}
	if err := requirePipelineFinalizedShortBook(dir); err != nil {
		t.Fatalf("requirePipelineFinalizedShortBook: %v", err)
	}
	var manifest pipelineFinalizationManifest
	raw, err := os.ReadFile(filepath.Join(dir, pipelineFinalizationJSON))
	if err != nil || json.Unmarshal(raw, &manifest) != nil {
		t.Fatalf("read finalization manifest: %v", err)
	}
	if manifest.Title != "终审测试" || manifest.TotalRunes != 10 || len(manifest.Chapters) != 2 {
		t.Fatalf("unexpected manifest: %+v", manifest)
	}
	if manifest.Chapters[0].Title != "第一章名" || manifest.Chapters[1].Title != "第二章名" {
		t.Fatalf("chapter titles not frozen from outline: %+v", manifest.Chapters)
	}

	if err := pipelineFinalizeConfigured(cfg, assets.Bundle{}, pipelineFlags{}, func(bootstrap.Config, assets.Bundle, headless.Options) error {
		t.Fatal("idempotent accepted finalization must not call Host again")
		return nil
	}); err != nil {
		t.Fatalf("idempotent finalize: %v", err)
	}
}

func TestPipelineFinalizeConfiguredStopsAfterRejectedGlobalReview(t *testing.T) {
	dir := pipelineFinalizeFixture(t)
	runner := func(cfg bootstrap.Config, _ assets.Bundle, _ headless.Options) error {
		st := store.NewStore(cfg.OutputDir)
		global := acceptedPipelineGlobalReview(2)
		global.Verdict = "rewrite"
		global.AffectedChapters = []int{1}
		global.Summary = "第一章线索需返工。"
		if err := st.World.SaveReview(global); err != nil {
			return err
		}
		_, err := st.Checkpoints.AppendArtifactLatestAcross(
			domain.ChapterScope(2), "review", "reviews/02-global.json", "review", "commit",
		)
		return err
	}
	err := pipelineFinalizeConfigured(bootstrap.Config{OutputDir: dir}, assets.Bundle{}, pipelineFlags{}, runner)
	if err == nil || !strings.Contains(err.Error(), "affected_chapters=[1]") || !strings.Contains(err.Error(), "未启动普通 write/rewrite") {
		t.Fatalf("rejected global review should stop sealed finalization, got %v", err)
	}
	if nonEmptyFile(filepath.Join(dir, pipelineFinalizationJSON)) || nonEmptyFile(filepath.Join(dir, "正文.md")) {
		t.Fatal("rejected global review must not manufacture finalization artifacts")
	}
}

func TestPipelineFinalizeRejectedGlobalReviewResumeNamesExplicitSealedRebaseStateMachine(t *testing.T) {
	dir := pipelineFinalizeFixture(t)
	st := store.NewStore(dir)
	review := acceptedPipelineGlobalReview(2)
	review.Verdict = "rewrite"
	review.AffectedChapters = []int{1, 2}
	review.Summary = "跨章知识边界存在硬伤。"
	if err := st.World.SaveReview(review); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.SetPendingRewritesAndFlow(
		[]int{1, 2}, review.Summary, domain.FlowRewriting,
	); err != nil {
		t.Fatal(err)
	}
	beforeOne, err := os.ReadFile(filepath.Join(dir, "chapters", "01.md"))
	if err != nil {
		t.Fatal(err)
	}
	beforeTwo, err := os.ReadFile(filepath.Join(dir, "chapters", "02.md"))
	if err != nil {
		t.Fatal(err)
	}
	hostCalls := 0
	err = pipelineFinalizeConfigured(
		bootstrap.Config{OutputDir: dir},
		assets.Bundle{},
		pipelineFlags{},
		func(bootstrap.Config, assets.Bundle, headless.Options) error {
			hostCalls++
			return nil
		},
	)
	if err == nil {
		t.Fatal("persisted terminal reject must not resume the global review Host")
	}
	for _, want := range []string{
		"pending_rewrites=[1 2]",
		"--rebase-all-chapters",
		"--stages architect,zero-init,preplan,project-all,seal --restart",
		"--stages promote,render",
		"--stages finalize,deliver",
		"exact-root",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("recovery error missing %q:\n%v", want, err)
		}
	}
	if hostCalls != 0 {
		t.Fatalf("persisted terminal reject reran Host %d times", hostCalls)
	}
	after, loadErr := st.Progress.Load()
	if loadErr != nil || after == nil || after.Flow != domain.FlowRewriting ||
		fmt.Sprint(after.PendingRewrites) != "[1 2]" {
		t.Fatalf("recovery preflight mutated pending state: progress=%+v err=%v", after, loadErr)
	}
	afterOne, _ := os.ReadFile(filepath.Join(dir, "chapters", "01.md"))
	afterTwo, _ := os.ReadFile(filepath.Join(dir, "chapters", "02.md"))
	if string(afterOne) != string(beforeOne) || string(afterTwo) != string(beforeTwo) {
		t.Fatal("recovery guidance edited accepted chapter bytes")
	}
	if nonEmptyFile(filepath.Join(dir, pipelineFinalizationJSON)) || nonEmptyFile(filepath.Join(dir, "正文.md")) {
		t.Fatal("rejected recovery must not manufacture terminal artifacts")
	}
}

func TestRequirePipelineFinalizedShortBookDetectsChapterDrift(t *testing.T) {
	dir := pipelineFinalizeFixture(t)
	st := store.NewStore(dir)
	global := acceptedPipelineGlobalReview(2)
	if err := st.World.SaveReview(global); err != nil {
		t.Fatal(err)
	}
	manuscript, err := buildPipelineMergedManuscript(st, []int{1, 2})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Drafts.SaveMergedManuscript(manuscript); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.MarkComplete(); err != nil {
		t.Fatal(err)
	}
	chapters := []int{1, 2}
	persisted, err := st.World.LoadLastReview(2)
	if err != nil || persisted == nil {
		t.Fatalf("load persisted global review: %+v err=%v", persisted, err)
	}
	if err := writePipelineFinalizationArtifacts(st, dir, chapters, persisted); err != nil {
		t.Fatal(err)
	}
	if err := requirePipelineFinalizedShortBook(dir); err != nil {
		t.Fatalf("fresh finalization: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "chapters", "01.md"), []byte("第一章正文被改动。"), 0o644); err != nil {
		t.Fatal(err)
	}
	err = requirePipelineFinalizedShortBook(dir)
	if err == nil || (!strings.Contains(err.Error(), "全文终审") && !strings.Contains(err.Error(), "不一致")) {
		t.Fatalf("chapter drift should invalidate finalization manifest, got %v", err)
	}
}

func TestValidatePipelineTerminalShortArcProofRequiresExactAcceptancesAndOneCompletion(t *testing.T) {
	st, generation, cursor, _ := arcOutcomeChainTestFixture(t, 2)
	progress := &domain.Progress{Layered: true, TotalChapters: 2}
	meta := &domain.RunMeta{PlanningTier: domain.PlanningTierShort}

	if err := validatePipelineTerminalShortArcProof(st, progress, meta); err == nil || !strings.Contains(err.Error(), "completion receipt") {
		t.Fatalf("missing terminal completion should block finalize, got %v", err)
	}
	if _, err := completePipelineArcCycle(st, generation, cursor); err != nil {
		t.Fatalf("complete terminal arc fixture: %v", err)
	}
	if err := validatePipelineTerminalShortArcProof(st, progress, meta); err != nil {
		t.Fatalf("valid terminal short arc proof rejected: %v", err)
	}
}

func pipelineFinalizeFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init("终审测试", 2); err != nil {
		t.Fatal(err)
	}
	if err := st.RunMeta.SetPlanningTier(domain.PlanningTierShort); err != nil {
		t.Fatal(err)
	}
	if err := st.Outline.SaveOutline([]domain.OutlineEntry{
		{Chapter: 1, Title: "第一章名"},
		{Chapter: 2, Title: "第二章名"},
	}); err != nil {
		t.Fatal(err)
	}
	for chapter, body := range map[int]string{1: "第一章正文", 2: "第二章正文"} {
		if err := st.Drafts.SaveFinalChapter(chapter, body); err != nil {
			t.Fatal(err)
		}
		if err := st.Progress.MarkChapterComplete(chapter, len([]rune(body)), "", ""); err != nil {
			t.Fatal(err)
		}
		mustWriteCurrentReviewArtifacts(t, dir, chapter)
	}
	return dir
}

func acceptedPipelineGlobalReview(chapter int) domain.ReviewEntry {
	dimensions := []domain.DimensionScore{
		{Dimension: "consistency", Score: 90, Verdict: "pass", Comment: "时间线与职业逻辑闭合。"},
		{Dimension: "character", Score: 90, Verdict: "pass", Comment: "人物主动性与关系弧闭合。"},
		{Dimension: "pacing", Score: 90, Verdict: "pass", Comment: "节奏闭合。"},
		{Dimension: "continuity", Score: 90, Verdict: "pass", Comment: "连续性闭合。"},
		{Dimension: "foreshadow", Score: 90, Verdict: "pass", Comment: "线索回收。"},
		{Dimension: "hook", Score: 90, Verdict: "pass", Comment: "收束有效。"},
		{Dimension: "aesthetic", Score: 90, Verdict: "pass", Comment: "表达稳定。"},
		{Dimension: "ai_voice_detection", Score: 90, Verdict: "pass", Comment: "无阻断。"},
	}
	return domain.ReviewEntry{
		Chapter: chapter, Scope: "global", Verdict: "accept", Summary: "短篇全文终审通过。",
		ContractStatus: "met", Dimensions: dimensions,
		Publication: &domain.ShortPublicationPackage{
			PrimaryTitle:     "终审测试",
			AlternateTitles:  []string{"终审之前", "最后一页的证词"},
			HookLead:         "她在终审前收到一份不该存在的证词，只剩一晚确认真假。",
			SpoilerFreeBlurb: "两位旧友因一份来源不明的证词重新并肩。她们必须在有限时间里核对每个细节，也要决定是否再次相信彼此。",
			Tags:             []string{"双女主", "现实悬疑", "限时追查", "旧友重逢", "短篇"},
		},
	}
}
