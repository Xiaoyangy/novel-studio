package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func TestVerifyPipelineWriteStageRequiresProgressChapterAndCheckpoint(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init("证据小说", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.Drafts.SaveFinalChapter(1, "第一章正文"); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.MarkChapterComplete(1, 5, "", ""); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.MarkComplete(); err != nil {
		t.Fatal(err)
	}

	_, err := verifyPipelineStage("write", dir, pipelineFlags{}, &domain.PipelineState{})
	if err == nil || !strings.Contains(err.Error(), "checkpoint:chapter:1:commit_chapter") {
		t.Fatalf("expected missing commit checkpoint error, got %v", err)
	}

	if _, err := st.Checkpoints.AppendArtifact(domain.ChapterScope(1), "commit_chapter", "chapters/01.md"); err != nil {
		t.Fatal(err)
	}
	evidence, err := verifyPipelineStage("write", dir, pipelineFlags{}, &domain.PipelineState{})
	if err != nil {
		t.Fatalf("verify write: %v", err)
	}
	if evidence.CompletedChapters != 1 {
		t.Fatalf("completed chapters = %d, want 1", evidence.CompletedChapters)
	}
	if len(evidence.Checkpoints) != 1 {
		t.Fatalf("expected one checkpoint evidence, got %+v", evidence.Checkpoints)
	}
}

func TestChapterFromPlanPartialPath(t *testing.T) {
	chapter, ok := chapterFromPlanPartialPath(filepath.Join("drafts", "01.plan.partial.json"))
	if !ok || chapter != 1 {
		t.Fatalf("expected chapter 1, got chapter=%d ok=%v", chapter, ok)
	}
	if _, ok := chapterFromPlanPartialPath(filepath.Join("drafts", "01.plan.json")); ok {
		t.Fatal("final plan file must not parse as partial")
	}
}

func TestPipelineHasWritingProgressIgnoresFreshRestartSeed(t *testing.T) {
	fresh := &domain.Progress{
		NovelName:      "她的第二算法",
		Phase:          domain.PhaseInit,
		TotalChapters:  70,
		Layered:        true,
		GenerationID:   "simulation-test",
		GenerationMode: "simulation_restart_from_seed",
	}
	if pipelineHasWritingProgress(fresh) {
		t.Fatalf("fresh simulation restart seed should start a new writing session, got progress=%+v", fresh)
	}
	for _, p := range []*domain.Progress{
		{Phase: domain.PhaseWriting},
		{CurrentChapter: 1},
		{InProgressChapter: 1},
		{CompletedChapters: []int{1}},
		{PendingRewrites: []int{1}},
	} {
		if !pipelineHasWritingProgress(p) {
			t.Fatalf("expected writing progress for %+v", p)
		}
	}
}

func TestPipelinePrepareFreshWritingSessionPreservesSeedAndClearsRuntime(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.ResetForSimulationRestart("她的第二算法", 70, "simulation-test"); err != nil {
		t.Fatal(err)
	}
	progress, err := st.Progress.Load()
	if err != nil {
		t.Fatal(err)
	}
	progress.Layered = true
	progress.CurrentVolume = 1
	progress.CurrentArc = 1
	if err := st.Progress.Save(progress); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Runtime.AppendQueue(domain.RuntimeQueueItem{
		Kind:     domain.RuntimeQueueControl,
		Priority: domain.RuntimePriorityControl,
		Summary:  "stale architect dispatch",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Checkpoints.Append(domain.GlobalScope(), "architect", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := st.RunMeta.SetPendingSteer("Pipeline staged-plan repair：旧计划修复指令"); err != nil {
		t.Fatal(err)
	}
	mustWriteFile(t, filepath.Join(dir, "meta", "sessions", "agents", "writer-ch01.jsonl"), `{"role":"assistant","content":"stale"}`)
	mustWriteFile(t, filepath.Join(dir, "drafts", "01.plan.partial.json"), `{"chapter":1,"stale":true}`)
	mustWriteFile(t, filepath.Join(dir, "drafts", "01.plan.json"), `{"chapter":1}`)
	mustWriteFile(t, filepath.Join(dir, "drafts", "01.draft.md"), "旧草稿")

	if err := pipelinePrepareFreshWritingSession(dir, 1); err != nil {
		t.Fatalf("prepare fresh writing session: %v", err)
	}
	progress, err = st.Progress.Load()
	if err != nil {
		t.Fatal(err)
	}
	if progress.Phase != domain.PhaseWriting || progress.CurrentChapter != 1 || progress.InProgressChapter != 1 {
		t.Fatalf("progress not moved to chapter 1 writing: %+v", progress)
	}
	if progress.GenerationID != "simulation-test" || progress.TotalChapters != 70 || !progress.Layered {
		t.Fatalf("seed metadata should be preserved: %+v", progress)
	}
	queue, err := st.Runtime.LoadQueue()
	if err != nil {
		t.Fatal(err)
	}
	if len(queue) != 0 {
		t.Fatalf("runtime queue should be reset, got %+v", queue)
	}
	if got := store.NewStore(dir).Checkpoints.All(); len(got) != 0 {
		t.Fatalf("checkpoints should be reset, got %+v", got)
	}
	meta, err := st.RunMeta.Load()
	if err != nil {
		t.Fatal(err)
	}
	if meta != nil && meta.PendingSteer != "" {
		t.Fatalf("pipeline staged-plan pending steer should be cleared, got %q", meta.PendingSteer)
	}
	for _, rel := range []string{
		filepath.Join("meta", "sessions", "agents", "writer-ch01.jsonl"),
		filepath.Join("drafts", "01.plan.partial.json"),
		filepath.Join("drafts", "01.plan.json"),
		filepath.Join("drafts", "01.draft.md"),
	} {
		if _, err := os.Stat(filepath.Join(dir, rel)); !os.IsNotExist(err) {
			t.Fatalf("expected stale artifact %s removed, err=%v", rel, err)
		}
	}
}

func TestPipelineNeedsFreshWritingSessionHandlesEmptyWritingShell(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.ResetForSimulationRestart("她的第二算法", 70, "simulation-test"); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.StartChapter(1); err != nil {
		t.Fatal(err)
	}
	progress, err := st.Progress.Load()
	if err != nil {
		t.Fatal(err)
	}
	needsFresh, err := pipelineNeedsFreshWritingSession(dir, progress)
	if err != nil {
		t.Fatal(err)
	}
	if !needsFresh {
		t.Fatalf("empty first-chapter writing shell should restart fresh: %+v", progress)
	}

	mustWriteFile(t, filepath.Join(dir, "drafts", "01.plan.partial.json"), `{"chapter":1}`)
	needsFresh, err = pipelineNeedsFreshWritingSession(dir, progress)
	if err != nil {
		t.Fatal(err)
	}
	if needsFresh {
		t.Fatal("staged plan artifact should be treated as resumable progress")
	}
}

func TestPipelineNeedsFreshWritingSessionResetsInvalidStagedPlan(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.ResetForSimulationRestart("她的第二算法", 70, "simulation-test"); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.StartChapter(1); err != nil {
		t.Fatal(err)
	}
	if err := st.Characters.Save([]domain.Character{
		{Name: "许闻溪", Role: "主角", Tier: "core"},
		{Name: "梁渡", Role: "重要配角 / 感情线男主", Tier: "core"},
	}); err != nil {
		t.Fatal(err)
	}
	mustWriteFile(t, filepath.Join(dir, "premise.md"), "女频女性职场成长文，主角许闻溪。")
	mustWriteFile(t, filepath.Join(dir, "drafts", "01.plan.partial.json"), `{"structure":{"chapter":1,"goal":"推进主角面对异常现场","conflict":"外部阻力","hook":"留下悬念"},"causal_simulation":{"initial_state":[{"character":"主角","current_goal":"稳住"}]}}`)
	progress, err := st.Progress.Load()
	if err != nil {
		t.Fatal(err)
	}
	needsFresh, err := pipelineNeedsFreshWritingSession(dir, progress)
	if err != nil {
		t.Fatal(err)
	}
	if !needsFresh {
		t.Fatal("invalid staged plan should be cleared by fresh writing session")
	}
}

func TestPipelineAutoRepairBookWorldStructureAddsDanglingFactionAndAliases(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.World.SaveBookWorld(domain.BookWorld{
		Version: 1,
		Factions: []domain.WorldFaction{{
			ID:   "operations_center",
			Name: "运营中心",
			Relations: []domain.FactionRelation{{
				Target: "store_ops",
				Kind:   "frontline_partner",
				Note:   "依赖门店执行与反馈。",
			}},
			Clock: &domain.FactionClock{Segments: 6, Progress: 1, Consequence: "运营中心完成一次人员缩编"},
		}, {
			ID:    "bridgepoint",
			Name:  "桥点职业转型工作室",
			Clock: &domain.FactionClock{Segments: 6, Progress: 0, Consequence: "桥点进入正式合作选择"},
		}},
	}); err != nil {
		t.Fatal(err)
	}

	repaired, err := pipelineAutoRepairBookWorldStructure(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !repaired {
		t.Fatal("expected auto repair to change book_world")
	}
	world, err := st.World.LoadBookWorld()
	if err != nil {
		t.Fatal(err)
	}
	if issues := world.ValidateFactionRelations(); len(issues) != 0 {
		t.Fatalf("relations should be valid after repair, got %v", issues)
	}
	var storeOps *domain.WorldFaction
	for i := range world.Factions {
		if world.Factions[i].ID == "store_ops" {
			storeOps = &world.Factions[i]
		}
	}
	if storeOps == nil || storeOps.Clock == nil {
		t.Fatalf("store_ops faction with clock should be added: %+v", world.Factions)
	}
	if got := strings.Join(storeOps.Aliases, " "); !strings.Contains(got, "门店运营组") {
		t.Fatalf("store_ops aliases should include 门店运营组, got %+v", storeOps.Aliases)
	}
	if got := strings.Join(world.Factions[1].Aliases, " "); !strings.Contains(got, "桥点工作室") {
		t.Fatalf("bridgepoint aliases should include 桥点工作室, got %+v", world.Factions[1].Aliases)
	}
}

func TestVerifyPipelineWriteStageAcceptsCommitCheckpointStep(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init("证据小说", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.Drafts.SaveFinalChapter(1, "第一章正文"); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.MarkChapterComplete(1, 5, "", ""); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.MarkComplete(); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Checkpoints.AppendArtifact(domain.ChapterScope(1), "commit", "chapters/01.md"); err != nil {
		t.Fatal(err)
	}

	evidence, err := verifyPipelineStage("write", dir, pipelineFlags{}, &domain.PipelineState{})
	if err != nil {
		t.Fatalf("verify write: %v", err)
	}
	if got := strings.Join(evidence.Checkpoints, " "); !strings.Contains(got, "chapter:1:commit#") {
		t.Fatalf("checkpoint evidence = %+v", evidence.Checkpoints)
	}
}

func TestVerifyPipelineWriteStageWriteToAllowsPartialWritingRun(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init("分段写作小说", 67); err != nil {
		t.Fatal(err)
	}
	for ch := 1; ch <= 3; ch++ {
		if err := st.Drafts.SaveFinalChapter(ch, "第几章正文"); err != nil {
			t.Fatal(err)
		}
		if err := st.Progress.MarkChapterComplete(ch, 5, "", ""); err != nil {
			t.Fatal(err)
		}
		if _, err := st.Checkpoints.AppendArtifact(domain.ChapterScope(ch), "commit", filepath.ToSlash(filepath.Join("chapters", fmt.Sprintf("%02d.md", ch)))); err != nil {
			t.Fatal(err)
		}
	}

	evidence, err := verifyPipelineStage("write", dir, pipelineFlags{WriteTo: 3}, &domain.PipelineState{})
	if err != nil {
		t.Fatalf("verify partial write: %v", err)
	}
	if evidence.ProgressPhase == string(domain.PhaseComplete) {
		t.Fatalf("partial write test should stay in writing phase, got %s", evidence.ProgressPhase)
	}
	if !strings.Contains(evidence.Message, "chapter 3") {
		t.Fatalf("expected write-to evidence message, got %q", evidence.Message)
	}

	_, err = verifyPipelineStage("write", dir, pipelineFlags{WriteTo: 4}, &domain.PipelineState{})
	if err == nil || !strings.Contains(err.Error(), "write-to=4") {
		t.Fatalf("expected write-to missing error, got %v", err)
	}
}

func TestVerifyPipelineWriteStageWriteToAllowsLegacyCompletedChapters(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init("老项目续写", 20); err != nil {
		t.Fatal(err)
	}
	for ch := 1; ch <= 3; ch++ {
		if err := st.Drafts.SaveFinalChapter(ch, "已有正文"); err != nil {
			t.Fatal(err)
		}
		if err := st.Progress.MarkChapterComplete(ch, 5, "", ""); err != nil {
			t.Fatal(err)
		}
	}

	evidence, err := verifyPipelineStage("write", dir, pipelineFlags{WriteTo: 3}, &domain.PipelineState{})
	if err != nil {
		t.Fatalf("verify partial write with legacy chapters: %v", err)
	}
	if got := strings.Join(evidence.Checkpoints, " "); !strings.Contains(got, "chapter:1:legacy-file") {
		t.Fatalf("expected legacy checkpoint evidence, got %+v", evidence.Checkpoints)
	}
}

func TestVerifyPipelineWriteStageWriteToWaitsForPendingRewrite(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init("返工小说", 10); err != nil {
		t.Fatal(err)
	}
	for ch := 1; ch <= 5; ch++ {
		if err := st.Drafts.SaveFinalChapter(ch, "已有正文"); err != nil {
			t.Fatal(err)
		}
		if err := st.Progress.MarkChapterComplete(ch, 5, "", ""); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.Progress.SetPendingRewrites([]int{2, 5}, "重建前五章"); err != nil {
		t.Fatal(err)
	}

	_, err := verifyPipelineStage("write", dir, pipelineFlags{WriteTo: 5}, &domain.PipelineState{})
	if err == nil || !strings.Contains(err.Error(), "pending_rewrites") {
		t.Fatalf("expected pending rewrite evidence error, got %v", err)
	}

	if err := st.Progress.ClearPendingRewrites(); err != nil {
		t.Fatal(err)
	}
	evidence, err := verifyPipelineStage("write", dir, pipelineFlags{WriteTo: 5}, &domain.PipelineState{})
	if err != nil {
		t.Fatalf("verify after clearing rewrites: %v", err)
	}
	if !strings.Contains(evidence.Message, "chapter 5") {
		t.Fatalf("expected write-to evidence, got %q", evidence.Message)
	}
}

func TestVerifyPipelineReviewStageRequiresReviewArtifacts(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "chapters", "01.md"), "第一章正文")

	_, err := verifyPipelineStage("review", dir, pipelineFlags{}, &domain.PipelineState{})
	if err == nil || !strings.Contains(err.Error(), "reviews/01.md") {
		t.Fatalf("expected missing review artifact error, got %v", err)
	}

	mustWriteFile(t, filepath.Join(dir, "reviews", "01.md"), "# ch01 评审\n")
	mustWriteFile(t, filepath.Join(dir, "meta", "review-summary.md"), "# review-summary\n")
	evidence, err := verifyPipelineStage("review", dir, pipelineFlags{}, &domain.PipelineState{})
	if err != nil {
		t.Fatalf("verify review: %v", err)
	}
	if evidence.CompletedChapters != 1 {
		t.Fatalf("reviewed chapters = %d, want 1", evidence.CompletedChapters)
	}
}

func TestVerifyPipelineReviewStageHonorsChapterRange(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "chapters", "01.md"), "第一章正文")
	mustWriteFile(t, filepath.Join(dir, "chapters", "02.md"), "第二章正文")
	mustWriteFile(t, filepath.Join(dir, "reviews", "02.md"), "# ch02 评审\n")
	mustWriteFile(t, filepath.Join(dir, "meta", "review-summary.md"), "# review-summary\n")

	evidence, err := verifyPipelineStage("review", dir, pipelineFlags{Start: 2, End: 2}, &domain.PipelineState{})
	if err != nil {
		t.Fatalf("verify ranged review: %v", err)
	}
	if evidence.CompletedChapters != 1 {
		t.Fatalf("reviewed chapters = %d, want 1", evidence.CompletedChapters)
	}
	if got := strings.Join(evidence.Artifacts, " "); !strings.Contains(got, "reviews/02.md") || strings.Contains(got, "reviews/01.md") {
		t.Fatalf("review artifacts = %+v", evidence.Artifacts)
	}
}

func TestVerifyPipelineRewriteStageRequiresBackups(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "chapters", "01.md"), "重写后正文")
	mustWriteFile(t, filepath.Join(dir, "reviews", "01_ai_voice_redflags.json"), `{
  "chapter": 1,
  "label": "❌ 需返工",
  "red_flags": [
    {"rule": "catalog_stuffing", "severity": "error", "evidence": "竹柄雨伞、裂口搪瓷杯、旧台历夹、粉笔头、桦皮袖扣、蓼蓝布头、荞麦壳、陶埙裂片、绢纱穗、菖蒲根"}
  ]
}`)

	_, err := verifyPipelineStage("rewrite", dir, pipelineFlags{}, &domain.PipelineState{})
	if err == nil || !strings.Contains(err.Error(), "chapters/01.md.pre-rewrite.md") {
		t.Fatalf("expected missing rewrite backup error, got %v", err)
	}

	mustWriteFile(t, filepath.Join(dir, "chapters", "01.md.pre-rewrite.md"), "原始正文")
	evidence, err := verifyPipelineStage("rewrite", dir, pipelineFlags{}, &domain.PipelineState{})
	if err != nil {
		t.Fatalf("verify rewrite: %v", err)
	}
	if evidence.CompletedChapters != 1 {
		t.Fatalf("rewritten chapters = %d, want 1", evidence.CompletedChapters)
	}
}

func TestVerifyPipelineRewriteStageAllowsCleanChaptersWithoutBackup(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "chapters", "01.md"), "江烬把账单压在桌上。周行舟问：“还要核哪一项？”他指了指押金条：“先核这个，别添别的。”")

	evidence, err := verifyPipelineStage("rewrite", dir, pipelineFlags{}, &domain.PipelineState{})
	if err != nil {
		t.Fatalf("clean chapter should not require rewrite backup: %v", err)
	}
	if got := strings.Join(evidence.Checkpoints, " "); !strings.Contains(got, "rewrite-not-needed") {
		t.Fatalf("expected rewrite-not-needed evidence, got %+v", evidence.Checkpoints)
	}
}

func TestVerifyPipelineRewriteStageBriefOnlyRequiresBriefNotBackup(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "chapters", "01.md"), "江烬把账单压在桌上。")

	_, err := verifyPipelineStage("rewrite", dir, pipelineFlags{RewriteBriefOnly: true}, &domain.PipelineState{})
	if err == nil || !strings.Contains(err.Error(), "reviews/01_rewrite_brief.md") {
		t.Fatalf("expected missing rewrite brief error, got %v", err)
	}

	mustWriteFile(t, filepath.Join(dir, "reviews", "01_rewrite_brief.md"), "# ch01 rewrite brief\n")
	evidence, err := verifyPipelineStage("rewrite", dir, pipelineFlags{RewriteBriefOnly: true}, &domain.PipelineState{})
	if err != nil {
		t.Fatalf("verify brief-only rewrite stage: %v", err)
	}
	if got := strings.Join(evidence.Artifacts, " "); !strings.Contains(got, "reviews/01_rewrite_brief.md") || strings.Contains(got, "pre-rewrite") {
		t.Fatalf("brief-only artifacts = %+v", evidence.Artifacts)
	}
	if got := strings.Join(evidence.Checkpoints, " "); !strings.Contains(got, "rewrite-brief-only") {
		t.Fatalf("brief-only checkpoints = %+v", evidence.Checkpoints)
	}
}

func TestVerifyPipelineRewriteStageRequiresBackupForBlockingAIVoiceWarnings(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "chapters", "01.md"), "江烬把账单压在桌上。周行舟问：“还要核哪一项？”他指了指押金条：“先核这个。”")
	mustWriteFile(t, filepath.Join(dir, "reviews", "01_ai_voice_redflags.json"), `{
  "chapter": 1,
  "label": "⚠️ 需打磨",
  "red_flags": [
    {"rule": "ending_hook_uniformity", "severity": "warning", "evidence": "章末用新名词制造悬念", "suggestion": "换成行动压力或未读消息。"}
  ]
}`)

	_, err := verifyPipelineStage("rewrite", dir, pipelineFlags{}, &domain.PipelineState{})
	if err == nil || !strings.Contains(err.Error(), "chapters/01.md.pre-rewrite.md") {
		t.Fatalf("expected blocking AI warning to require backup evidence, got %v", err)
	}

	mustWriteFile(t, filepath.Join(dir, "chapters", "01.md.pre-rewrite.md"), "原始正文")
	evidence, err := verifyPipelineStage("rewrite", dir, pipelineFlags{}, &domain.PipelineState{})
	if err != nil {
		t.Fatalf("verify blocking AI rewrite: %v", err)
	}
	if evidence.CompletedChapters != 1 {
		t.Fatalf("rewritten chapters = %d, want 1", evidence.CompletedChapters)
	}
}

func TestCompleteResolvedRewritesClearsAcceptedPending(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init("鬼城", 10); err != nil {
		t.Fatal(err)
	}
	text := "江烬把收据压在黑卡旁。许曼问：“七天后呢？”他说：“账上另算。”"
	mustWriteFile(t, filepath.Join(dir, "chapters", "03.md"), text)
	mustWriteFile(t, filepath.Join(dir, "reviews", "03.json"), `{"chapter":3,"scope":"chapter","verdict":"accept","summary":"复审通过"}`)
	if err := st.Progress.MarkChapterComplete(3, len([]rune(text)), "crisis", "quest"); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.SetPendingRewrites([]int{3}, "复审前待返工"); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.SetFlow(domain.FlowPolishing); err != nil {
		t.Fatal(err)
	}

	if err := completeResolvedRewrites(dir, 1, 5); err != nil {
		t.Fatalf("complete resolved rewrites: %v", err)
	}
	progress, err := st.Progress.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(progress.PendingRewrites) != 0 {
		t.Fatalf("pending rewrites = %+v, want empty", progress.PendingRewrites)
	}
	if progress.Flow != domain.FlowWriting {
		t.Fatalf("flow = %s, want writing", progress.Flow)
	}
}

func TestPipelineStateDoneEvidenceCanBeCleared(t *testing.T) {
	state := &domain.PipelineState{}
	state.MarkDone("review", domain.PipelineStageEvidence{Stage: "review", Status: "verified"})
	if !state.Done("review") {
		t.Fatal("stage should be marked done")
	}
	state.ClearDone("review", domain.PipelineStageEvidence{Stage: "review", Status: "stale", Message: "missing reviews/01.md"})
	if state.Done("review") {
		t.Fatal("stage should be cleared")
	}
	if got := state.Evidence["review"].Status; got != "stale" {
		t.Fatalf("evidence status = %s, want stale", got)
	}
}

func TestResolvePipelinePromptUsesGlobalPromptConsumedByTopParser(t *testing.T) {
	opts, args, err := parseCLIOptions([]string{"--pipeline", "--prompt", "写一本东方玄幻长篇"})
	if err != nil {
		t.Fatalf("parseCLIOptions: %v", err)
	}
	if !hasPipelineFlag(args) {
		t.Fatalf("expected pipeline route token to remain, got %v", args)
	}
	prompt, err := resolvePipelinePrompt(pipelineFlags{}, opts)
	if err != nil {
		t.Fatalf("resolvePipelinePrompt: %v", err)
	}
	if prompt != "写一本东方玄幻长篇" {
		t.Fatalf("prompt = %q", prompt)
	}
}

func TestResolvePipelinePromptRejectsDuplicatePromptSources(t *testing.T) {
	opts := cliOptions{Prompt: "全局 prompt"}
	_, err := resolvePipelinePrompt(pipelineFlags{Prompt: "pipeline prompt"}, opts)
	if err == nil || !strings.Contains(err.Error(), "只能指定一次") {
		t.Fatalf("expected duplicate prompt error, got %v", err)
	}
}

func TestPipelineStageArgsPassesReviewRewriteOptions(t *testing.T) {
	flags := pipelineFlags{Start: 3, End: 8, Budget: 5 * time.Minute, Role: "coordinator", MaxRewriteRounds: 4, PolishWarnings: true, RewriteBriefOnly: true}
	args := pipelineStageArgs(flags)
	if got := strings.Join(args["review"], " "); got != "--from 3 --to 8 --budget 5m0s" {
		t.Fatalf("review args = %q", got)
	}
	if got := strings.Join(args["rewrite"], " "); got != "--from 3 --to 8 --budget 5m0s --role coordinator --max-rounds 4 --polish-warnings --brief-only" {
		t.Fatalf("rewrite args = %q", got)
	}
}

func TestSettlePipelineDeliveryRefreshesLedgersAndRAGFacts(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init("鬼城", 3); err != nil {
		t.Fatal(err)
	}
	chapterText := "正文原文不应该进入 RAG。江烬把欠费单压住。"
	if err := st.Drafts.SaveFinalChapter(1, chapterText); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.MarkChapterComplete(1, len([]rune(chapterText)), "mystery", "quest"); err != nil {
		t.Fatal(err)
	}
	if err := st.Summaries.SaveSummary(domain.ChapterSummary{
		Chapter:    1,
		Summary:    "江烬收到夜租欠费单，确认不能替人认账。",
		Characters: []string{"江烬", "周行舟"},
		KeyEvents:  []string{"夜租欠费单送达", "代缴确认规则成立"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.World.AppendTimelineEvents([]domain.TimelineEvent{{
		Chapter:    1,
		Time:       "午夜00:17",
		Event:      "1704收到夜租欠费单，江烬确认不能替人认账。",
		Characters: []string{"江烬"},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := st.World.AppendStateChanges([]domain.StateChange{{
		Chapter:  1,
		Entity:   "江烬",
		Field:    "夜租状态",
		NewValue: "收到1704夜租欠费单",
		Reason:   "收租鬼上门",
	}}); err != nil {
		t.Fatal(err)
	}
	if err := st.ResourceLedger.MergeClaims(1, []domain.ResourceClaim{{
		ID:      "black-card",
		Name:    "冥府黑卡虚拟卡面",
		Owner:   "江烬",
		Kind:    "payment_permission",
		Status:  "booked",
		Chapter: 1,
	}}, nil); err != nil {
		t.Fatal(err)
	}
	if err := st.World.SaveWorldRules([]domain.WorldRule{{
		Category: "夜租规则",
		Rule:     "活人被视为暂住阳间的租客，必须缴夜租。",
		Boundary: "普通现金无效，代缴必须被契约确认。",
	}}); err != nil {
		t.Fatal(err)
	}
	if err := st.World.SaveReview(domain.ReviewEntry{Chapter: 1, Scope: "chapter", Verdict: "accept", Summary: "章级审核通过"}); err != nil {
		t.Fatal(err)
	}

	if err := settlePipelineDelivery(dir, pipelineFlags{Start: 1, End: 1}); err != nil {
		t.Fatalf("settlePipelineDelivery: %v", err)
	}
	for _, rel := range []string{
		"meta/chapter_progress.json",
		"meta/project_progress.json",
		"meta/evolution_report.json",
		"meta/delivery_log.jsonl",
		"meta/delivery_log.md",
	} {
		if !nonEmptyFile(filepath.Join(dir, filepath.FromSlash(rel))) {
			t.Fatalf("expected delivery artifact %s", rel)
		}
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "meta", "delivery_snapshots", "ch01_*.json"))
	if len(matches) != 1 {
		t.Fatalf("expected one delivery snapshot, got %v", matches)
	}
	var snap pipelineDeliverySnapshot
	data, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &snap); err != nil {
		t.Fatal(err)
	}
	if snap.Completion == nil {
		t.Fatalf("expected completion package in snapshot: %+v", snap)
	}
	if snap.Completion.Summary == nil || snap.Completion.Summary.Summary == "" {
		t.Fatalf("expected summary sedimentation: %+v", snap.Completion)
	}
	if len(snap.Completion.TimelineProgress) == 0 {
		t.Fatalf("expected timeline progress in completion: %+v", snap.Completion)
	}
	if len(snap.Completion.ProtagonistChanges) == 0 {
		t.Fatalf("expected protagonist state changes in completion: %+v", snap.Completion)
	}
	if len(snap.Completion.ResourceLedgerUpdates) == 0 {
		t.Fatalf("expected resource ledger updates in completion: %+v", snap.Completion)
	}
	if len(snap.Completion.WorldRuleProgress) == 0 {
		t.Fatalf("expected world rule progress in completion: %+v", snap.Completion)
	}
	if snap.Completion.RAG.SourcePath != "summaries/01.json" || snap.Completion.RAG.UsesBody {
		t.Fatalf("RAG completion should use summary facts only: %+v", snap.Completion.RAG)
	}
	state, err := st.RAG.LoadIndexState()
	if err != nil || state == nil || len(state.Chunks) == 0 {
		t.Fatalf("expected RAG fact state, state=%+v err=%v", state, err)
	}
	if state.Chunks[0].SourcePath != "summaries/01.json" {
		t.Fatalf("RAG should be sourced from summary facts, got %+v", state.Chunks[0])
	}
	if strings.Contains(state.Chunks[0].Text, "正文原文不应该进入 RAG") {
		t.Fatalf("RAG chunk should not contain chapter body: %s", state.Chunks[0].Text)
	}
}

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
