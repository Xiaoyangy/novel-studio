package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/chenhongyang/novel-studio/assets"
	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/reviewreport"
	"github.com/chenhongyang/novel-studio/internal/rules"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/tools"
)

func TestPipelineReconcilesExplicitPromptChapterWordsForDownstreamRecovery(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.UserRules.Save(&rules.Snapshot{
		Sources: []string{"system_defaults", "startup_prompt"},
		Structured: rules.Structured{
			ChapterWords: &rules.WordRange{Min: 2000, Max: 3300},
		},
		Uncertain: []string{
			"未设置 chapter_words：只给出了全书总字数和章数",
			"第三人称视角要求未提升到 structured",
		},
	}); err != nil {
		t.Fatal(err)
	}

	changed, err := pipelineReconcileExplicitPromptChapterWords(
		st,
		"正文严格控制在2.8万—3万字，共12章，单章约 2200—2600 字。",
	)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("explicit downstream prompt range should replace the stale default")
	}
	snapshot, err := st.UserRules.Load()
	if err != nil {
		t.Fatal(err)
	}
	if snapshot == nil || snapshot.Structured.ChapterWords == nil ||
		snapshot.Structured.ChapterWords.Min != 2200 ||
		snapshot.Structured.ChapterWords.Max != 2600 {
		t.Fatalf("chapter_words not reconciled: %+v", snapshot)
	}
	if !slices.Contains(snapshot.Sources, "pipeline_prompt_explicit") {
		t.Fatalf("explicit prompt source missing: %+v", snapshot.Sources)
	}
	if len(snapshot.Uncertain) != 1 || strings.Contains(snapshot.Uncertain[0], "chapter_words") {
		t.Fatalf("stale chapter_words uncertainty survived: %+v", snapshot.Uncertain)
	}

	changed, err = pipelineReconcileExplicitPromptChapterWords(st, "单章约2200—2600字")
	if err != nil || changed {
		t.Fatalf("same explicit range should be idempotent: changed=%v err=%v", changed, err)
	}
}

func TestPipelineDraftNeedsExternalJudgeIncludesFirstUnjudgedHash(t *testing.T) {
	for _, test := range []struct {
		name       string
		inspection tools.DraftExternalGateInspection
		want       bool
	}{
		{
			name: "new draft without artifact",
			inspection: tools.DraftExternalGateInspection{
				Status:            tools.DraftExternalGateNotRequired,
				CurrentBodySHA256: "new-body",
			},
			want: true,
		},
		{
			name:       "no draft yet",
			inspection: tools.DraftExternalGateInspection{Status: tools.DraftExternalGateNotRequired},
		},
		{
			name: "approved hash",
			inspection: tools.DraftExternalGateInspection{
				Status:            tools.DraftExternalGateApproved,
				CurrentBodySHA256: "approved-body",
				ArtifactExists:    true,
			},
		},
		{
			name:       "changed hash",
			inspection: tools.DraftExternalGateInspection{Status: tools.DraftExternalGateRejudgePending},
			want:       true,
		},
		{
			name: "registered retest waits for operator",
			inspection: tools.DraftExternalGateInspection{
				Status:                   tools.DraftExternalGateRejudgePending,
				RequiresRegisteredRetest: true,
			},
		},
		{
			name: "DeepSeek-passing local soft failure goes to one edit",
			inspection: tools.DraftExternalGateInspection{
				Status:               tools.DraftExternalGateRejudgePending,
				LocalSoftEditPending: true,
			},
		},
		{
			name: "blocking hash authorized for rerender",
			inspection: tools.DraftExternalGateInspection{
				Status:            tools.DraftExternalGateRerenderAuthorized,
				CurrentBodySHA256: "blocking-body",
				Requirement:       &tools.DraftExternalRerenderRequirement{Chapter: 3},
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := pipelineDraftNeedsExternalJudge(test.inspection); got != test.want {
				t.Fatalf("pipelineDraftNeedsExternalJudge() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestPipelineExplicitRerenderSkipsJudgeForSupersededDraft(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Drafts.SaveDraft(2, "第二章\n\n已被人工否掉的旧候选。"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Checkpoints.AppendArtifact(domain.ChapterScope(2), "draft", "drafts/02.draft.md"); err != nil {
		t.Fatal(err)
	}
	requestPath := filepath.Join(dir, "drafts", "02.rerender_request.json")
	if err := os.WriteFile(requestPath, []byte(`{"chapter":2}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Checkpoints.AppendArtifact(domain.ChapterScope(2), "rerender-request", "drafts/02.rerender_request.json"); err != nil {
		t.Fatal(err)
	}
	inspection := tools.DraftExternalGateInspection{
		Status: tools.DraftExternalGateRejudgePending, CurrentBodySHA256: "superseded",
	}
	if pipelineDraftNeedsExternalJudgeForChapter(dir, 2, inspection) {
		t.Fatal("newer explicit rerender request should skip judging the superseded draft")
	}
}

func TestPipelineJudgePendingDraftHashSkipsNamedRetestForExplicitlySupersededDraft(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	body := "第二章\n\n这是已经被显式整章重渲染请求作废的旧候选。"
	if err := st.Drafts.SaveDraft(2, body); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Checkpoints.AppendArtifact(domain.ChapterScope(2), "draft", "drafts/02.draft.md"); err != nil {
		t.Fatal(err)
	}
	if err := tools.SetRegisteredExternalRerenderRequirement(dir, reviewreport.RegisteredExternalDetection{
		Chapter: 2, Detector: "zhuque", Mode: "novel-whole-text-single-segment",
		BodySHA256: reviewreport.BodySHA256(body), NormalizedScorePercent: 83,
	}); err != nil {
		t.Fatal(err)
	}
	requestPath := filepath.Join(dir, "drafts", "02.rerender_request.json")
	if err := os.WriteFile(requestPath, []byte(`{"chapter":2}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Checkpoints.AppendArtifact(domain.ChapterScope(2), "rerender-request", "drafts/02.rerender_request.json"); err != nil {
		t.Fatal(err)
	}

	judged, err := pipelineJudgePendingDraftHash(cliOptions{}, dir, &domain.Progress{PendingRewrites: []int{2}})
	if err != nil || judged {
		t.Fatalf("explicit rerender should bypass all old-hash preflight: judged=%v err=%v", judged, err)
	}
	inspection, err := tools.InspectDraftExternalGateWithStore(st, 2)
	if err != nil {
		t.Fatal(err)
	}
	if tools.RequiresRegisteredExternalRetest(inspection.Requirement) ||
		!slices.Contains(tools.RegisteredExternalRetestLabels(inspection.Requirement), "zhuque/novel-whole-text-single-segment") {
		t.Fatalf("explicit rerender lost sampling provenance or created a hard obligation: %+v", inspection.Requirement)
	}
}

func TestSampleTriggeredRewriteSurvivesQueueAndBecomesNextDeepSeekGate(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init("queue", 2); err != nil {
		t.Fatal(err)
	}
	for chapter := 1; chapter <= 2; chapter++ {
		if err := st.Progress.MarkChapterComplete(chapter, 20, "", ""); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.Progress.SetPendingRewritesAndFlow([]int{1, 2}, "registered retest queue", domain.FlowRewriting); err != nil {
		t.Fatal(err)
	}

	oldBody := "第二章\n\n旧平台送检正文。"
	newBody := "第二章\n\n替换稿先交给当前哈希的 DeepSeek 判断。"
	if err := st.Drafts.SaveDraft(2, oldBody); err != nil {
		t.Fatal(err)
	}
	if err := tools.SetRegisteredExternalRerenderRequirement(dir, reviewreport.RegisteredExternalDetection{
		Chapter: 2, Detector: "zhuque", Mode: "novel-whole-text-single-segment",
		BodySHA256: reviewreport.BodySHA256(oldBody), NormalizedScorePercent: 83,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.Drafts.SaveDraft(2, newBody); err != nil {
		t.Fatal(err)
	}

	before, err := tools.InspectDraftExternalGateWithStore(st, 2)
	if err != nil || before.Status != tools.DraftExternalGateRejudgePending || before.RegisteredRetestDeferred || before.RequiresRegisteredRetest {
		t.Fatalf("chapter 2 should wait for DeepSeek without a platform obligation while chapter 1 leads: inspection=%+v err=%v", before, err)
	}
	if err := st.Progress.CompleteRewrite(1); err != nil {
		t.Fatal(err)
	}
	progress, err := st.Progress.Load()
	if err != nil || len(progress.PendingRewrites) != 1 || progress.PendingRewrites[0] != 2 {
		t.Fatalf("chapter 2 did not become queue head after chapter 1 completed: progress=%+v err=%v", progress, err)
	}
	after, err := tools.InspectDraftExternalGateWithStore(st, progress.PendingRewrites[0])
	if err != nil || after.RegisteredRetestDeferred || tools.RequiresRegisteredExternalRetest(after.Requirement) ||
		!pipelineDraftNeedsExternalJudgeForChapterWithStore(st, 2, after) {
		t.Fatalf("chapter 2 failed to enter DeepSeek-first sampling recovery: inspection=%+v err=%v", after, err)
	}
}

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

func TestPipelineFinalizeStagedPlanKeepsRepairSteerOnFailure(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Save(&domain.Progress{
		NovelName:         "test",
		TotalChapters:     2,
		CompletedChapters: []int{1},
		PendingRewrites:   []int{1},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.Drafts.SaveChapterPlanPartial(1, map[string]any{
		"structure": map[string]any{
			"chapter":  1,
			"title":    "测试章",
			"goal":     "推进测试事件",
			"conflict": "测试冲突",
			"hook":     "测试钩子",
		},
		"causal_simulation": map[string]any{
			"chapter_function": "只填一个字段，确保 finalize 失败",
		},
	}); err != nil {
		t.Fatal(err)
	}

	if err := pipelineFinalizeStagedPlans(dir, 2); err != nil {
		t.Fatalf("pipelineFinalizeStagedPlans: %v", err)
	}
	meta, err := st.RunMeta.Load()
	if err != nil {
		t.Fatal(err)
	}
	if meta == nil || !strings.HasPrefix(meta.PendingSteer, "Pipeline staged-plan repair") {
		t.Fatalf("failed staged plan must retain exact repair steer, got %+v", meta)
	}
	if !strings.Contains(meta.PendingSteer, "chapter_function") {
		t.Fatalf("repair steer should carry finalize diagnostics, got %q", meta.PendingSteer)
	}

	if err := pipelineFinalizeStagedPlans(dir, 2); err != nil {
		t.Fatalf("second pipelineFinalizeStagedPlans: %v", err)
	}
	meta, _ = st.RunMeta.Load()
	if meta == nil || meta.PendingSteer == "" {
		t.Fatal("a repeated failed preflight must not clear the pending repair steer")
	}
}

func TestPipelineWorldSimulationFailureQueuesSimulationOnlyRepair(t *testing.T) {
	steer := pipelineWorldSimulationRepairSteer(1, "全角色世界推演不完整：missing character decision: 老丁")
	for _, want := range []string{
		"Pipeline world-simulation repair", "只允许调用 simulate_chapter_world", "严禁调用 plan_structure、plan_details", "simulated=true",
	} {
		if !strings.Contains(steer, want) {
			t.Fatalf("world simulation repair steer missing %q: %s", want, steer)
		}
	}
	if !pipelineFailureNeedsWorldSimulation("chapter_world_simulation invalid") {
		t.Fatal("world simulation failures must use the dedicated repair route")
	}
	if pipelineFailureNeedsWorldSimulation("missing causal_simulation.voice_logic") {
		t.Fatal("ordinary plan gaps must stay on staged plan repair")
	}
}

func TestPipelineFinalizeStagedPlansClearsRepairSteerWhenNoPartialRemains(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.RunMeta.SetPendingSteer("Pipeline staged-plan repair：旧指令"); err != nil {
		t.Fatal(err)
	}
	if err := pipelineFinalizeStagedPlans(dir, 2); err != nil {
		t.Fatal(err)
	}
	meta, err := st.RunMeta.Load()
	if err != nil {
		t.Fatal(err)
	}
	if meta != nil && meta.PendingSteer != "" {
		t.Fatalf("stale repair steer should clear after all partials are gone, got %q", meta.PendingSteer)
	}
}

func TestPipelineAutoRepairBookWorldStructureAddsOnlyDanglingFaction(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.World.SaveBookWorld(domain.BookWorld{
		Version: 1,
		Factions: []domain.WorldFaction{{
			ID:      "project_office",
			Name:    "项目办公室",
			Aliases: []string{"项目组"},
			Relations: []domain.FactionRelation{{
				Target: "field_team",
				Kind:   "frontline_partner",
				Note:   "依赖现场执行与反馈。",
			}},
			Clock: &domain.FactionClock{Segments: 6, Progress: 1, Consequence: "项目办公室完成一次资源调整"},
		}, {
			ID:      "outside_partner",
			Name:    "外部合作方",
			Aliases: []string{"合作机构"},
			Clock:   &domain.FactionClock{Segments: 6, Progress: 0, Consequence: "合作方进入正式选择"},
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
	var fieldTeam *domain.WorldFaction
	for i := range world.Factions {
		if world.Factions[i].ID == "field_team" {
			fieldTeam = &world.Factions[i]
		}
	}
	if fieldTeam == nil || fieldTeam.Clock == nil || fieldTeam.Name != "field team" {
		t.Fatalf("faction derived from relation target should be added: %+v", world.Factions)
	}
	if len(fieldTeam.Aliases) != 0 {
		t.Fatalf("auto repair must not invent project-specific aliases: %+v", fieldTeam.Aliases)
	}
	if got := strings.Join(world.Factions[1].Aliases, " "); got != "合作机构" {
		t.Fatalf("authored aliases should remain unchanged, got %+v", world.Factions[1].Aliases)
	}
}

func TestPipelineWorldTickForbiddenTopicsUsesStructuredUserRules(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Outline.SavePremise("当前项目正文，不通过书名或题材关键词选择禁用词表。"); err != nil {
		t.Fatal(err)
	}
	if got := pipelineWorldTickForbiddenTopics(dir); got != nil {
		t.Fatalf("premise text must not activate a hidden production profile: %v", got)
	}
	if err := st.UserRules.Save(&rules.Snapshot{
		Version: rules.SnapshotVersion,
		Status:  rules.StatusReady,
		Structured: rules.Structured{
			ForbiddenPhrases: []string{"外部设定", "过期流程", "外部设定"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if got, want := pipelineWorldTickForbiddenTopics(dir), []string{"外部设定", "过期流程"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("forbidden topics = %v, want structured user rules %v", got, want)
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

	mustWriteCurrentReviewArtifacts(t, dir, 1)
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
	mustWriteCurrentReviewArtifacts(t, dir, 2)

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

func TestVerifyPipelineReviewStageRejectsStaleBodyHash(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "chapters", "01.md"), "第一版正文")
	mustWriteCurrentReviewArtifacts(t, dir, 1)
	mustWriteFile(t, filepath.Join(dir, "chapters", "01.md"), "第二版正文")

	_, err := verifyPipelineStage("review", dir, pipelineFlags{}, &domain.PipelineState{})
	if err == nil || !strings.Contains(err.Error(), "body_sha256 stale") {
		t.Fatalf("stale review should be rejected after body change, got %v", err)
	}
}

func TestRebuildReviewSummaryPreservesAllChapterRows(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "chapters", "01.md"), "第一章正文")
	mustWriteFile(t, filepath.Join(dir, "chapters", "02.md"), "第二章正文")
	mustWriteCurrentReviewArtifacts(t, dir, 1)
	mustWriteCurrentReviewArtifacts(t, dir, 2)

	raw, err := os.ReadFile(filepath.Join(dir, "meta", "review-summary.md"))
	if err != nil {
		t.Fatal(err)
	}
	summary := string(raw)
	for _, want := range []string{"**ch01**", "**ch02**", "body_sha256=" + reviewreport.BodySHA256("第一章正文"), "body_sha256=" + reviewreport.BodySHA256("第二章正文")} {
		if !strings.Contains(summary, want) {
			t.Fatalf("rebuilt summary missing %q:\n%s", want, summary)
		}
	}
}

func TestVerifyPipelineRewriteStageRequiresBackups(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "chapters", "01.md"), "重写后正文")
	mustWriteCurrentReviewArtifacts(t, dir, 1, domain.AIVoiceRedFlag{
		Rule: "catalog_stuffing", Severity: "error", Evidence: "竹柄雨伞、裂口搪瓷杯、旧台历夹、粉笔头、桦皮袖扣、蓼蓝布头、荞麦壳、陶埙裂片、绢纱穗、菖蒲根",
	})

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
	mustWriteCurrentReviewArtifacts(t, dir, 1)

	evidence, err := verifyPipelineStage("rewrite", dir, pipelineFlags{}, &domain.PipelineState{})
	if err != nil {
		t.Fatalf("clean chapter should not require rewrite backup: %v", err)
	}
	if got := strings.Join(evidence.Checkpoints, " "); !strings.Contains(got, "rewrite-not-needed") {
		t.Fatalf("expected rewrite-not-needed evidence, got %+v", evidence.Checkpoints)
	}
}

func TestVerifyPipelineRewriteStageAcceptsResolvedRewriteBackup(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "chapters", "01.md"), "江烬把账单压在桌上。周行舟问：“还要核哪一项？”他指了指押金条：“先核这个，别添别的。”")
	mustWriteFile(t, filepath.Join(dir, "chapters", "01.md.pre-rewrite.md"), "改写前正文")
	mustWriteCurrentReviewArtifacts(t, dir, 1)

	evidence, err := verifyPipelineStage("rewrite", dir, pipelineFlags{}, &domain.PipelineState{})
	if err != nil {
		t.Fatalf("resolved rewrite should accept pre-rewrite backup: %v", err)
	}
	if got := strings.Join(evidence.Artifacts, " "); !strings.Contains(got, "chapters/01.md.pre-rewrite.md") {
		t.Fatalf("expected pre-rewrite artifact, got %+v", evidence.Artifacts)
	}
	if got := strings.Join(evidence.Checkpoints, " "); strings.Contains(got, "rewrite-not-needed") {
		t.Fatalf("resolved rewrite should not claim rewrite-not-needed without checkpoint: %+v", evidence.Checkpoints)
	}
}

func TestVerifyPipelineRewriteStageBriefOnlyRequiresBriefNotBackup(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "chapters", "01.md"), "江烬把账单压在桌上。")
	mustWriteCurrentReviewArtifacts(t, dir, 1)

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
	mustWriteCurrentReviewArtifacts(t, dir, 1, domain.AIVoiceRedFlag{
		Rule: "ending_hook_uniformity", Severity: "warning", Evidence: "章末用新名词制造悬念", Suggestion: "换成行动压力或未读消息。",
	})

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
	mustWriteCurrentReviewArtifacts(t, dir, 3)
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

func TestLoadPipelineStateInvalidatesExplicitPromptChange(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pipeline.json")
	previous := &domain.PipelineState{
		Stages:      []string{"architect", "zero-init"},
		Completed:   []string{"architect"},
		Prompt:      "旧创作指令",
		InputDigest: "sha256:runtime-a",
	}
	if err := savePipelineState(path, previous); err != nil {
		t.Fatal(err)
	}
	state, err := loadOrInitPipelineState(
		path, previous.Stages, "新创作指令", "sha256:runtime-a", "sha256:run-a", false,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Completed) != 0 || state.Prompt != "新创作指令" {
		t.Fatalf("prompt drift must reset completed stages: %+v", state)
	}
}

func TestLoadPipelineStateInvalidatesRuntimePromptFingerprintChange(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pipeline.json")
	previous := &domain.PipelineState{
		Stages:      []string{"architect", "zero-init"},
		Completed:   []string{"architect"},
		Prompt:      "创作指令",
		InputDigest: "sha256:runtime-a",
	}
	if err := savePipelineState(path, previous); err != nil {
		t.Fatal(err)
	}
	state, err := loadOrInitPipelineState(
		path, previous.Stages, "", "sha256:runtime-b", "sha256:run-a", false,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Completed) != 0 || state.Prompt != previous.Prompt || state.InputDigest != "sha256:runtime-b" {
		t.Fatalf("runtime drift must reset while preserving prompt: %+v", state)
	}
}

func TestLoadPipelineStateInvalidatesRunIdentityChange(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pipeline.json")
	previous := &domain.PipelineState{
		Stages:      []string{"preplan", "plan", "render"},
		Completed:   []string{"preplan", "plan", "render"},
		Prompt:      "创作指令",
		InputDigest: "sha256:runtime-a",
		RunIdentity: pipelineRunIdentityDigest(pipelineFlags{Start: 1, End: 1, Budget: time.Minute}),
	}
	if err := savePipelineState(path, previous); err != nil {
		t.Fatal(err)
	}
	nextIdentity := pipelineRunIdentityDigest(pipelineFlags{Start: 2, End: 2, Budget: 2 * time.Minute})
	state, err := loadOrInitPipelineState(
		path, previous.Stages, "", previous.InputDigest, nextIdentity, false,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Completed) != 0 || state.Prompt != previous.Prompt || state.RunIdentity != nextIdentity {
		t.Fatalf("run identity drift must reset completed stages while preserving prompt: %+v", state)
	}
}

func TestPipelineRunIdentityBindsFromToAndBudget(t *testing.T) {
	base := pipelineRunIdentityDigest(pipelineFlags{Start: 1, End: 3, Budget: time.Minute})
	for name, flags := range map[string]pipelineFlags{
		"from":             {Start: 2, End: 3, Budget: time.Minute},
		"to":               {Start: 1, End: 4, Budget: time.Minute},
		"budget":           {Start: 1, End: 3, Budget: 2 * time.Minute},
		"architect-target": {Start: 1, End: 3, Budget: time.Minute, ArchitectTarget: "book_world"},
	} {
		if got := pipelineRunIdentityDigest(flags); got == base {
			t.Fatalf("%s change did not alter pipeline run identity", name)
		}
	}
}

func TestPipelineRunInputDigestBindsBrainstormArtifact(t *testing.T) {
	project := t.TempDir()
	outputDir := filepath.Join(project, "output", "novel")
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		t.Fatal(err)
	}
	brainstormPath := filepath.Join(project, "brainstorm.md")
	if err := os.WriteFile(brainstormPath, []byte("第一版脑爆"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := bootstrap.Config{OutputDir: outputDir, Provider: "openai", ModelName: "gpt-5.6-sol", Style: "default"}
	first := pipelineRunInputDigest(cfg, assets.Load("default"))
	if err := os.WriteFile(brainstormPath, []byte("第二版脑爆"), 0o644); err != nil {
		t.Fatal(err)
	}
	second := pipelineRunInputDigest(cfg, assets.Load("default"))
	if first == second {
		t.Fatal("brainstorm drift must invalidate the pipeline input digest")
	}
}

func TestPipelineRunInputDigestBindsReferencesAndEffectiveStyle(t *testing.T) {
	cfg := bootstrap.Config{
		OutputDir: filepath.Join(t.TempDir(), "output", "novel"),
		Provider:  "openai",
		ModelName: "gpt-5.6-sol",
		Style:     "default",
	}
	baseBundle := assets.Load("default")
	baseDigest := pipelineRunInputDigest(cfg, baseBundle)

	t.Run("references", func(t *testing.T) {
		drifted := baseBundle
		drifted.References.AntiAITone += "\n仅用于验证 reference 漂移会失效运行摘要。"
		if got := pipelineRunInputDigest(cfg, drifted); got == baseDigest {
			t.Fatal("reference drift must invalidate the pipeline input digest")
		}
	})

	t.Run("styles", func(t *testing.T) {
		drifted := baseBundle
		drifted.Styles = make(map[string]string, len(baseBundle.Styles))
		for name, body := range baseBundle.Styles {
			drifted.Styles[name] = body
		}
		drifted.Styles["default"] += "\n仅用于验证 style 漂移会失效运行摘要。"
		if got := pipelineRunInputDigest(cfg, drifted); got == baseDigest {
			t.Fatal("style drift must invalidate the pipeline input digest")
		}
	})

	t.Run("unselected styles are not consumed", func(t *testing.T) {
		drifted := baseBundle
		drifted.Styles = make(map[string]string, len(baseBundle.Styles))
		for name, body := range baseBundle.Styles {
			drifted.Styles[name] = body
		}
		drifted.Styles["fantasy"] += "\n未被本次运行选择。"
		if got := pipelineRunInputDigest(cfg, drifted); got != baseDigest {
			t.Fatal("unselected style drift changed pipeline input digest")
		}
	})
}

func TestPipelineRunInputDigestCanonicalizesEffectiveStyleID(t *testing.T) {
	cfg := bootstrap.Config{
		OutputDir: filepath.Join(t.TempDir(), "output", "novel"),
		Provider:  "openai",
		ModelName: "gpt-5.6-sol",
		Style:     "default",
	}
	bundle := assets.Load("default")
	want := pipelineRunInputDigest(cfg, bundle)
	for _, equivalent := range []string{"", "  default  ", "missing-style"} {
		candidate := cfg
		candidate.Style = equivalent
		if got := pipelineRunInputDigest(candidate, bundle); got != want {
			t.Fatalf("equivalent style %q changed pipeline identity: got=%s want=%s", equivalent, got, want)
		}
	}
}

func TestVerifyStoredPipelineArtifactDigestsRejectsDrift(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "chapters", "01.md"), "第一版")
	evidence := stampPipelineArtifactDigests(dir, domain.PipelineStageEvidence{
		Stage: "write", Artifacts: []string{"chapters/01.md"},
	})
	if err := verifyStoredPipelineArtifactDigests(dir, evidence); err != nil {
		t.Fatalf("fresh digest rejected: %v", err)
	}
	mustWriteFile(t, filepath.Join(dir, "chapters", "01.md"), "第二版")
	if err := verifyStoredPipelineArtifactDigests(dir, evidence); err == nil || !strings.Contains(err.Error(), "指纹漂移") {
		t.Fatalf("digest drift should be rejected, got %v", err)
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

func TestParsePipelineFlagsSupportsOpeningRefresh(t *testing.T) {
	flags, extra, err := parsePipelineFlags([]string{"--refresh-architect", "--architect-target", "book_world", "--refresh-zero-init", "--force-rerender", "--stages", "architect,zero-init"})
	if err != nil {
		t.Fatalf("parsePipelineFlags: %v", err)
	}
	if len(extra) != 0 || !flags.RefreshArchitect || flags.ArchitectTarget != "book_world" || !flags.RefreshZeroInit || !flags.ForceRerender {
		t.Fatalf("unexpected refresh flags: flags=%+v extra=%v", flags, extra)
	}
}

func TestPipelineRequestFullRerenderInvalidatesExistingDraftWithoutChangingPlan(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Characters.Save([]domain.Character{{Name: "主角", Role: "主角", Tier: "core"}}); err != nil {
		t.Fatal(err)
	}
	decision := domain.CharacterWorldDecision{
		Character: "主角", Location: "夜市", CurrentGoal: "完成试点", Pressure: "时间有限",
		KnowledgeBoundary: "只知道现场事实", AvailableOptions: []string{"继续", "停止"},
		Decision: "继续", DecisionReason: "结果可核验", Action: "完成试点", ActionDuration: "半天",
		CompletionState: "completed", ImmediateResult: "试点完成", StateAfter: "准备复盘",
		ButterflyEffects: []domain.DecisionButterflyEffect{{
			Effect: "下一步扩展", TransmissionPath: "现场结果", ArrivalChapter: 2,
			Visibility: "visible", ProtagonistImpact: "形成新选择",
		}},
	}
	sim := domain.ChapterWorldSimulation{
		Version: 1, SimulationID: "sim-2", Chapter: 2, TimeWindow: "次日上午至傍晚",
		CharacterDecisions: []domain.CharacterWorldDecision{decision},
		ProtagonistProjection: domain.ProtagonistDecisionProjection{
			Protagonist: "主角", ObservableEffects: []string{"试点完成"}, HiddenPressures: []string{"后续申请"},
			AvailableOptions: []string{"继续", "停止"}, ChosenDecision: "继续", DecisionReason: "结果可核验",
			PlanConstraints: []string{"只写主视角可见事实"}, CausalChain: []string{"试点完成 -> 申请增加"},
		},
	}
	if err := st.SaveChapterWorldSimulation(sim); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Checkpoints.AppendArtifact(
		domain.ChapterScope(2),
		"chapter_world_simulation",
		"meta/chapter_simulations/002.json",
	); err != nil {
		t.Fatal(err)
	}
	plan := domain.ChapterPlan{
		Chapter: 2, Title: "皮卡一到，五个摊主点头了",
		Contract: domain.ChapterContract{RequiredBeats: []string{"完成试点"}},
		CausalSimulation: domain.ChapterCausalSimulation{
			WorldSimulationID: "sim-2", ProtagonistDecision: "继续",
			ContextSources: []string{"chapter_world_simulation:sim-2"},
			CausalBeats:    []domain.CausalSimulationBeat{{Cause: "需求出现", CharacterChoice: "继续", StoryResult: "试点完成"}},
			OutcomeShift:   []string{"试点完成"},
		},
	}
	if err := st.Drafts.SaveChapterPlan(plan); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Checkpoints.AppendArtifact(domain.ChapterScope(2), "plan", "drafts/02.plan.json"); err != nil {
		t.Fatal(err)
	}
	if err := st.Drafts.SaveDraft(2, "第二章\n\n旧草稿"); err != nil {
		t.Fatal(err)
	}
	draftCheckpoint, err := st.Checkpoints.AppendArtifact(domain.ChapterScope(2), "draft", "drafts/02.draft.md")
	if err != nil {
		t.Fatal(err)
	}

	requested, err := pipelineRequestFullRerender(st, []int{2}, "读起来像流程播报")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(requested, []int{2}) {
		t.Fatalf("requested = %v", requested)
	}
	requestCheckpoint := st.Checkpoints.LatestByStep(domain.ChapterScope(2), "rerender-request")
	if requestCheckpoint == nil || requestCheckpoint.Seq <= draftCheckpoint.Seq {
		t.Fatalf("rerender checkpoint = %+v, draft=%+v", requestCheckpoint, draftCheckpoint)
	}
	var request pipelineRerenderRequest
	raw, err := os.ReadFile(filepath.Join(st.Dir(), "drafts", "02.rerender_request.json"))
	if err != nil || json.Unmarshal(raw, &request) != nil {
		t.Fatalf("rerender request unreadable: err=%v body=%s", err, raw)
	}
	if request.Chapter != 2 || request.PlanSHA256 == "" || request.SupersededDraftSHA256 == "" ||
		request.Instruction != "读起来像流程播报" || request.InstructionSHA256 == "" {
		t.Fatalf("rerender request incomplete: %+v", request)
	}
	loadedPlan, err := st.Drafts.LoadChapterPlan(2)
	if err != nil || loadedPlan == nil || loadedPlan.Title != plan.Title {
		t.Fatalf("force rerender changed plan: plan=%+v err=%v", loadedPlan, err)
	}

	// A rerender request is authorization to reuse this plan, not a fresh
	// causal budget. Repeating --force-rerender must not jump over exhausted
	// structural attempts by writing a newer request checkpoint.
	for i := 0; i < 3; i++ {
		if _, err := st.Checkpoints.Append(
			domain.ChapterScope(2), "draft-structural-block", "drafts/02.draft.md",
			fmt.Sprintf("sha256:blocked-%d", i),
		); err != nil {
			t.Fatal(err)
		}
	}
	before := st.Checkpoints.LatestByStep(domain.ChapterScope(2), "rerender-request")
	if _, err := pipelineRequestFullRerender(st, []int{2}, "再来一次"); err == nil || !strings.Contains(err.Error(), "必须先重做") {
		t.Fatalf("repeated force rerender should reject an exhausted plan, got %v", err)
	}
	after := st.Checkpoints.LatestByStep(domain.ChapterScope(2), "rerender-request")
	if before == nil || after == nil || before.Seq != after.Seq {
		t.Fatalf("rejected force rerender wrote a new boundary: before=%+v after=%+v", before, after)
	}
}

func TestPipelineForceRerenderTargetsCompletedRangeWithoutPendingQueue(t *testing.T) {
	progress := &domain.Progress{CompletedChapters: []int{1, 2, 3, 4}}
	targets, err := pipelineForceRerenderTargets(progress, 1, 3)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(targets, []int{1, 2, 3}) {
		t.Fatalf("targets = %v", targets)
	}

	if _, err := pipelineForceRerenderTargets(progress, 7, 8); err == nil {
		t.Fatal("missing completed range must fail instead of reporting a successful no-op")
	}
}

func TestMergePendingRewriteChaptersPreservesOutOfRangeFailures(t *testing.T) {
	got := mergePendingRewriteChapters([]int{1, 2}, []int{1})
	if !slices.Equal(got, []int{1, 2}) {
		t.Fatalf("scoped rerender erased an unrelated pending chapter: %v", got)
	}

	got = mergePendingRewriteChapters([]int{2, 4}, []int{3, 2, 1})
	if !slices.Equal(got, []int{1, 2, 3, 4}) {
		t.Fatalf("merged queue = %v", got)
	}
}

func TestPipelineQueueCurrentExternalSamplingFailuresQueuesExactHighIdempotently(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init("抽查自动返工", 2); err != nil {
		t.Fatal(err)
	}
	const body = "第一章当前正式正文"
	if err := st.Drafts.SaveFinalChapter(1, body); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.MarkChapterComplete(1, len([]rune(body)), "", ""); err != nil {
		t.Fatal(err)
	}
	appendRegisteredExternalFreshnessRow(t, dir, reviewreport.RegisteredExternalDetection{
		Chapter: 1, Detector: "zhuque", Mode: "novel-whole-text-single-segment",
		Score: 0.86, ScoreScale: "probability", Verdict: "ai_like",
		BodySHA256: reviewreport.BodySHA256(body), NormalizedScorePercent: 86,
		CheckedAt: "2026-07-16T10:00:00+08:00",
	})

	queued, err := pipelineQueueCurrentExternalSamplingFailures(st, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(queued, []int{1}) {
		t.Fatalf("queued = %v, want [1]", queued)
	}
	progress, err := st.Progress.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(progress.PendingRewrites, []int{1}) ||
		progress.Flow != domain.FlowRewriting ||
		progress.RewriteReason != pipelineExternalSamplingRewriteReason {
		t.Fatalf("unexpected reconciled progress: %+v", progress)
	}
	if pipelineWriteGoalReached(progress, 1) {
		t.Fatal("write goal must not return early while an exact-hash sampling failure is queued")
	}

	progressPath := filepath.Join(dir, "meta", "progress.json")
	before, err := os.ReadFile(progressPath)
	if err != nil {
		t.Fatal(err)
	}
	queued, err = pipelineQueueCurrentExternalSamplingFailures(st, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(progressPath)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(queued, []int{1}) {
		t.Fatalf("second reconciliation queued = %v, want [1]", queued)
	}
	if string(after) != string(before) {
		t.Fatalf("idempotent reconciliation rewrote progress:\nbefore=%s\nafter=%s", before, after)
	}
}

func TestPipelineQueueCurrentExternalSamplingFailuresIgnoresOldHashAndUnknown(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init("抽查非阻塞", 3); err != nil {
		t.Fatal(err)
	}
	for chapter, body := range map[int]string{
		1: "第一章当前正式正文",
		2: "第二章未抽查正文",
		3: "第三章显式自动门禁正文",
	} {
		if err := st.Drafts.SaveFinalChapter(chapter, body); err != nil {
			t.Fatal(err)
		}
		if err := st.Progress.MarkChapterComplete(chapter, len([]rune(body)), "", ""); err != nil {
			t.Fatal(err)
		}
	}
	appendRegisteredExternalFreshnessRow(t, dir, reviewreport.RegisteredExternalDetection{
		Chapter: 1, Detector: "zhuque", Mode: "novel-whole-text-single-segment",
		Score: 0.83, ScoreScale: "probability", Verdict: "ai_like",
		BodySHA256: reviewreport.BodySHA256("第一章已经被替换的旧正文"), NormalizedScorePercent: 83,
		CheckedAt: "2026-07-16T10:00:00+08:00",
	})
	chapter3SHA := reviewreport.BodySHA256("第三章显式自动门禁正文")
	if err := tools.SetDraftExternalRerenderRequirement(dir, tools.DraftExternalRerenderRequirement{
		Chapter:              3,
		EvaluatedBodySHA256:  chapter3SHA,
		ExternalRetestPolicy: tools.DraftExternalRetestPolicyAutomatedHard,
		RequiredExternalRetests: []tools.DraftExternalRetestIdentity{{
			Detector: "internal-detector", Mode: "whole", TriggerBodySHA256: chapter3SHA,
		}},
		AdviceComplete: true,
	}); err != nil {
		t.Fatal(err)
	}

	queued, err := pipelineQueueCurrentExternalSamplingFailures(st, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(queued) != 0 {
		t.Fatalf("old hash, unknown sampling, and automated_hard missing must not enter rewrite queue: %v", queued)
	}
	progress, err := st.Progress.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(progress.PendingRewrites) != 0 {
		t.Fatalf("pending rewrites = %v, want empty", progress.PendingRewrites)
	}
}

func TestPipelineArchitectRefreshPromptLocksGoldenThreeAndStageBoundary(t *testing.T) {
	prompt, err := pipelineArchitectRefreshPrompt(t.TempDir(), "整章朱雀分数过高，重做黄金三章")
	if err != nil {
		t.Fatalf("pipelineArchitectRefreshPrompt: %v", err)
	}
	for _, want := range []string{"architect_long", "移动阅读留存闭环", "每条【系统消息】独立成段", "没有此类设定时严禁凭空新增", "严禁 plan_chapter", "整章朱雀分数过高"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("refresh prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestPipelineArchitectRefreshPromptReplansWholeShortBookAtChapterZero(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Save(&domain.Progress{TotalChapters: 12}); err != nil {
		t.Fatal(err)
	}
	prompt, err := pipelineArchitectRefreshPrompt(dir, "重做12章现实悬疑短篇，保留双女主")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"章零短篇全书刷新", "premise", "characters", "world_rules", "book_world", "world_codex", "compass", "重做完整12章", "每轮只调用一次 save_foundation",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("short refresh prompt missing %q:\n%s", want, prompt)
		}
	}
	if strings.Contains(prompt, "只重做前三章") {
		t.Fatalf("chapter-zero short refresh must not be restricted to three chapters:\n%s", prompt)
	}
}

func TestPipelineArchitectShortRefreshRunPromptPinsOneFoundationType(t *testing.T) {
	for run, want := range []string{
		"premise", "characters", "world_rules", "book_world", "world_codex", "update_compass", "layered_outline",
	} {
		prompt := pipelineArchitectShortRefreshRunPrompt("BASE", run+1)
		if !strings.Contains(prompt, `save_foundation(type="`+want+`")`) ||
			!strings.Contains(prompt, `subagent(agent="architect_long"`) ||
			!strings.Contains(prompt, "Coordinator 自己禁止调用 novel_context") ||
			!strings.Contains(prompt, "novel_context(chapter=1, profile=planning)") ||
			!strings.Contains(prompt, "BASE") {
			t.Fatalf("run %d did not pin %s:\n%s", run+1, want, prompt)
		}
	}
}

func TestPipelineArchitectShortRefreshTargetsStayProjectAgnostic(t *testing.T) {
	forbidden := []string{
		"数据灰产",
		"女性地址",
		"路线数据",
		"两次指定反转",
		"旧案统一引擎",
	}
	seen := map[string]bool{}
	for _, target := range pipelineArchitectShortRefreshTargets {
		if seen[target.Type] {
			t.Fatalf("duplicate short refresh target %q", target.Type)
		}
		seen[target.Type] = true
		if strings.TrimSpace(target.Description) == "" {
			t.Fatalf("short refresh target %s has an empty description", target.Type)
		}
		for _, fragment := range forbidden {
			if strings.Contains(target.Description, fragment) {
				t.Fatalf("short refresh target %s leaked project-specific fragment %q: %s", target.Type, fragment, target.Description)
			}
		}
	}
	if len(seen) != 7 {
		t.Fatalf("short refresh target inventory=%v, want 7 unique foundation targets", seen)
	}
}

func TestPipelineArchitectRefreshHeadlessOptionsAreStageBound(t *testing.T) {
	artifacts := []string{"premise.md"}
	opts := pipelineArchitectRefreshHeadlessOptions("refresh", artifacts, "premise", false)
	if opts.Prompt != "refresh" || !opts.PreserveUserRules || !opts.PreserveCheckpointsOnStart ||
		!opts.DisableFlowRouter || opts.AllowChapterZeroFoundationRefresh || opts.FoundationRefreshTarget != "premise" ||
		!opts.RecordFoundationRefreshEpoch || !opts.OneShotFoundationRefresh || !opts.StopAfterFoundationChange ||
		opts.FoundationChangeCheckpointStep != tools.FoundationRefreshCheckpointStep("premise") ||
		!slices.Equal(opts.FoundationChangeArtifacts, artifacts) {
		t.Fatalf("Architect refresh headless options lost a safety boundary: %+v", opts)
	}
	artifacts[0] = "mutated.md"
	if opts.FoundationChangeArtifacts[0] != "premise.md" {
		t.Fatal("Architect refresh options retained a mutable artifact slice alias")
	}
	for _, target := range pipelineArchitectShortRefreshTargets {
		targetOpts := pipelineArchitectRefreshHeadlessOptions("refresh", target.Artifacts, target.Type, target.Type == "layered_outline")
		wantCapability := target.Type == "layered_outline"
		if targetOpts.AllowChapterZeroFoundationRefresh != wantCapability || targetOpts.FoundationRefreshTarget != target.Type {
			t.Fatalf("target %s options lost exact binding: %+v", target.Type, targetOpts)
		}
	}
}

func TestPipelineArchitectInitialHeadlessOptionsDisableWritingRouter(t *testing.T) {
	opts := pipelineArchitectInitialHeadlessOptions("initialize foundation")
	if opts.Prompt != "initialize foundation" || !opts.StopAfterFoundation || !opts.DisableFlowRouter {
		t.Fatalf("initial Architect headless options lost the foundation-only boundary: %+v", opts)
	}
	if opts.StopAfterFoundationChange || opts.AllowChapterZeroFoundationRefresh ||
		opts.FoundationRefreshTarget != "" || opts.OneShotFoundationRefresh {
		t.Fatalf("initial Architect options unexpectedly acquired refresh capabilities: %+v", opts)
	}
}

func TestInvalidateExplicitArchitectRefreshClearsDownstreamEvidence(t *testing.T) {
	state := &domain.PipelineState{Stages: []string{"zero-init", "cocreate", "architect", "outline-all"}}
	for _, stage := range state.Stages {
		state.MarkDone(stage, domain.PipelineStageEvidence{Stage: stage, Status: "ok"})
	}
	if !invalidateExplicitArchitectRefresh(state, true) {
		t.Fatal("explicit Architect refresh did not invalidate completed evidence")
	}
	if !state.Done("cocreate") {
		t.Fatal("Architect refresh invalidated an upstream cocreate stage")
	}
	for _, stage := range []string{"architect", "outline-all", "zero-init"} {
		if state.Done(stage) || state.Evidence[stage].Status != "forced_refresh" {
			t.Fatalf("stage %s retained stale evidence: %+v", stage, state.Evidence[stage])
		}
	}
}

func TestArchitectRefreshRejectsMissingStageAndCorruptProgress(t *testing.T) {
	if err := runPipelineWithStages(cliOptions{}, pipelineFlags{RefreshArchitect: true}, []string{"zero-init"}, "prompt", nil); err == nil || !strings.Contains(err.Error(), "包含 architect") {
		t.Fatalf("refresh without architect stage was not rejected before setup: %v", err)
	}
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "meta", "progress.json"), []byte(`{"phase":`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := pipelineArchitectShortChapterZero(dir); err == nil {
		t.Fatal("corrupt progress silently downgraded into a broader Architect refresh mode")
	}
}

func TestPipelineArchitectShortTargetSelectionAndRevision(t *testing.T) {
	targets, err := pipelineArchitectShortSelectedTargets("book_world")
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 || targets[0].Type != "book_world" || !slices.Equal(targets[0].Artifacts, []string{"book_world.json"}) {
		t.Fatalf("unexpected selected targets: %+v", targets)
	}
	if _, err := pipelineArchitectShortSelectedTargets("not-a-foundation"); err == nil || !strings.Contains(err.Error(), "未知 --architect-target") {
		t.Fatalf("unknown target accepted: %v", err)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "book_world.json")
	if err := os.WriteFile(path, []byte(`{"version":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	before, err := pipelineArchitectShortTargetRevision(dir, targets[0])
	if err != nil {
		t.Fatal(err)
	}
	stamp := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	after, err := pipelineArchitectShortTargetRevision(dir, targets[0])
	if err != nil {
		t.Fatal(err)
	}
	if after == before {
		t.Fatal("same-content target rewrite must still change the revision proof")
	}
}

func TestPipelineArchitectPromptLocksOutlineAllArcSpanForShortFiction(t *testing.T) {
	prompt, err := pipelineArchitectPrompt(t.TempDir(), "写一篇12章现实悬疑短篇")
	if err != nil {
		t.Fatalf("pipelineArchitectPrompt: %v", err)
	}
	for _, want := range []string{"每个弧占 8—16 章", "一卷一弧 12 章", "不得拆成三个 4 章弧"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("architect prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestPipelineBookWordTotalIsHardAtDelivery(t *testing.T) {
	target := domain.BookScaleTarget{MinWords: 28000, MaxWords: 30000}
	if err := validatePipelineBookWordTotal(target, 29000); err != nil {
		t.Fatalf("in-range total rejected: %v", err)
	}
	for _, total := range []int{27999, 30001} {
		if err := validatePipelineBookWordTotal(target, total); err == nil || !strings.Contains(err.Error(), "全书正文总字数硬门禁") {
			t.Fatalf("out-of-range total %d accepted: %v", total, err)
		}
	}
}

func TestPipelineNormalizeOutlineAllArcSpansLeavesModelAllocatedArcs(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	// Under model-allocated structure, arc length is a story decision with no
	// upper bound and a one-chapter floor. A short 4-chapter arc and a long
	// 30-chapter arc are both valid and must survive the delivery normalizer
	// untouched (it only repairs genuinely invalid, sub-one-chapter spans).
	chapter := 0
	makeArc := func(index, span int) domain.ArcOutline {
		var chapters []domain.OutlineEntry
		for i := 1; i <= span; i++ {
			chapter++
			chapters = append(chapters, domain.OutlineEntry{
				Chapter: chapter, Title: fmt.Sprintf("第%d章", chapter),
				CoreEvent: fmt.Sprintf("事件%d", chapter), Hook: fmt.Sprintf("钩子%d", chapter),
				Scenes: []string{fmt.Sprintf("场景%d", chapter)},
			})
		}
		return domain.ArcOutline{Index: index, Title: fmt.Sprintf("弧%d", index), Goal: fmt.Sprintf("目标%d", index), Chapters: chapters}
	}
	arcs := []domain.ArcOutline{makeArc(1, 4), makeArc(2, 30)}
	before := domain.FlattenOutline([]domain.VolumeOutline{{Index: 1, Title: "全书", Arcs: arcs}})
	if err := st.Outline.SaveLayeredOutline([]domain.VolumeOutline{{Index: 1, Title: "全书", Arcs: arcs}}); err != nil {
		t.Fatal(err)
	}

	changed, err := pipelineNormalizeOutlineAllArcSpans(dir)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("model-allocated arc spans (4 and 30 chapters) must not be repartitioned")
	}
	afterVolumes, err := st.Outline.LoadLayeredOutline()
	if err != nil {
		t.Fatal(err)
	}
	if len(afterVolumes) != 1 || len(afterVolumes[0].Arcs) != 2 ||
		afterVolumes[0].Arcs[0].ChapterSpan() != 4 || afterVolumes[0].Arcs[1].ChapterSpan() != 30 {
		t.Fatalf("normalizer changed model-allocated arcs = %+v", afterVolumes)
	}
	after := domain.FlattenOutline(afterVolumes)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("chapter content changed:\nbefore=%+v\nafter=%+v", before, after)
	}
}

func TestPipelineNormalizeOutlineAllCompassScaleAddsExplicitExactRanges(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	chapters := make([]domain.OutlineEntry, 12)
	for i := range chapters {
		chapters[i] = domain.OutlineEntry{Chapter: i + 1, Title: fmt.Sprintf("第%d章", i+1)}
	}
	if err := st.Outline.SaveLayeredOutline([]domain.VolumeOutline{{
		Index: 1, Title: "全书", Arcs: []domain.ArcOutline{{Index: 1, Title: "主弧", Chapters: chapters}},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := st.Outline.SaveCompass(domain.StoryCompass{
		EndingDirection: "闭环", EstimatedScale: "单卷12章，正文2.8万—3万字",
	}); err != nil {
		t.Fatal(err)
	}

	changed, err := pipelineNormalizeOutlineAllCompassScale(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected natural-language exact scale to be normalized")
	}
	compass, err := st.Outline.LoadCompass()
	if err != nil {
		t.Fatal(err)
	}
	got, err := domain.ParseBookScaleRange(compass.EstimatedScale)
	if err != nil {
		t.Fatal(err)
	}
	want := domain.BookScaleRange{MinVolumes: 1, MaxVolumes: 1, MinChapters: 12, MaxChapters: 12}
	if got != want || !strings.Contains(compass.EstimatedScale, "正文2.8万—3万字") {
		t.Fatalf("normalized scale=%q parsed=%+v want=%+v", compass.EstimatedScale, got, want)
	}
}

func TestVerifyPipelineZeroInitStageKeepsEvidenceAfterChapterOne(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init("她的第二算法", 70); err != nil {
		t.Fatal(err)
	}
	if err := st.Drafts.SaveFinalChapter(1, "许闻溪把讲稿翻到最后一页。"); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.MarkChapterComplete(1, 13, "crisis", "quest"); err != nil {
		t.Fatal(err)
	}
	generatedAt := time.Now().Add(10 * time.Minute).Format(time.RFC3339)
	mustWriteFile(t, filepath.Join(dir, "meta", "first_chapter_generation_readiness.json"), fmt.Sprintf(`{"ready":true,"generated_at":%q}`, generatedAt))
	mustWriteFile(t, filepath.Join(dir, "meta", "first_chapter_generation_readiness.md"), "ready")
	mustWriteFile(t, filepath.Join(dir, "meta", "ch01_zero_init_plan.md"), "plan")
	if _, err := st.WorldSim.AppendWorldEvents([]domain.WorldEvent{{
		TickID:            "v1-a1",
		Chapter:           0,
		Actors:            []string{"AI提效项目组"},
		Summary:           "AI提效项目组把溪流助手列入运营中心试点。",
		VisibilityChapter: 1,
	}}); err != nil {
		t.Fatalf("AppendWorldEvents: %v", err)
	}
	if err := st.WorldSim.SaveTick(domain.WorldTick{TickID: "v1-a1", Volume: 1, Arc: 1, ThroughChapter: 0, EventCount: 1}); err != nil {
		t.Fatalf("SaveTick: %v", err)
	}

	evidence, err := verifyPipelineStage("zero-init", dir, pipelineFlags{}, &domain.PipelineState{})
	if err != nil {
		t.Fatalf("verify zero-init: %v", err)
	}
	if !strings.Contains(evidence.Message, "completed before chapter one") {
		t.Fatalf("message=%q, want immutable historical readiness note", evidence.Message)
	}
	for _, want := range []string{
		"meta/first_chapter_generation_readiness.json",
		"meta/first_chapter_generation_readiness.md",
		"meta/ch01_zero_init_plan.md",
	} {
		if !slices.Contains(evidence.Artifacts, want) {
			t.Fatalf("zero-init evidence missing %s: %+v", want, evidence.Artifacts)
		}
	}
	for _, mutable := range []string{"meta/world_tick.json", "meta/world_events.jsonl"} {
		if slices.Contains(evidence.Artifacts, mutable) {
			t.Fatalf("zero-init immutable evidence retained live ledger %s: %+v", mutable, evidence.Artifacts)
		}
	}
}

func TestSettlePipelineDeliveryRefreshesLedgersAndRAGFacts(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init("城市更新", 3); err != nil {
		t.Fatal(err)
	}
	// This fixture exercises incremental delivery sedimentation, not the
	// short-book whole-manuscript finalization gate.
	if err := st.RunMeta.SetPlanningTier(domain.PlanningTierLong); err != nil {
		t.Fatal(err)
	}
	chapterText := "正文原文不应该进入 RAG。顾晴把现场授权单压住。"
	if err := st.Drafts.SaveFinalChapter(1, chapterText); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.MarkChapterComplete(1, len([]rune(chapterText)), "mystery", "quest"); err != nil {
		t.Fatal(err)
	}
	if err := st.Summaries.SaveSummary(domain.ChapterSummary{
		Chapter:    1,
		Summary:    "顾晴拿到临时通行凭证，确认不能替未登记人员签收。",
		Characters: []string{"顾晴", "陆宁"},
		KeyEvents:  []string{"临时通行凭证送达", "代领签收规则成立"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.World.AppendTimelineEvents([]domain.TimelineEvent{{
		Chapter:    1,
		Time:       "上午09:17",
		Event:      "施工区收到临时通行凭证，顾晴确认不能替未登记人员签收。",
		Characters: []string{"顾晴"},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := st.World.AppendStateChanges([]domain.StateChange{{
		Chapter:  1,
		Entity:   "顾晴",
		Field:    "授权状态",
		NewValue: "收到施工区临时通行凭证",
		Reason:   "现场负责人完成登记",
	}}); err != nil {
		t.Fatal(err)
	}
	if err := st.ResourceLedger.MergeClaims(1, []domain.ResourceClaim{{
		ID:      "temporary-pass",
		Name:    "施工区临时通行凭证",
		Owner:   "顾晴",
		Kind:    "access_permission",
		Status:  "booked",
		Chapter: 1,
	}}, nil); err != nil {
		t.Fatal(err)
	}
	if err := st.World.SaveWorldRules([]domain.WorldRule{{
		Category: "现场权限",
		Rule:     "未登记人员进入施工区必须持临时通行凭证。",
		Boundary: "口头同意无效，代领必须完成签收确认。",
	}}); err != nil {
		t.Fatal(err)
	}
	if err := st.World.SaveReview(domain.ReviewEntry{Chapter: 1, Scope: "chapter", Verdict: "accept", Summary: "章级审核通过"}); err != nil {
		t.Fatal(err)
	}
	mustWriteCurrentReviewArtifacts(t, dir, 1)

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

func TestWorldRuleHitsDerivesTermsFromCurrentRule(t *testing.T) {
	rule := domain.WorldRule{
		Category: "现场权限",
		Rule:     "未登记人员进入施工区必须持蓝色通行凭证。",
		Boundary: "代领必须完成签收确认。",
	}
	hits := worldRuleHits("顾晴拿到蓝色通行凭证，并完成签收确认。", rule)
	if len(hits) == 0 || !strings.Contains(strings.Join(hits, " "), "通行") {
		t.Fatalf("current rule evidence was not derived dynamically: %v", hits)
	}
	if hits := worldRuleHits("顾晴回到办公室喝水。", rule); len(hits) != 0 {
		t.Fatalf("unrelated corpus matched rule evidence: %v", hits)
	}
}

func TestSettlePipelineDeliveryRejectsUnifiedBlockingGate(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "chapters", "01.md"), "第一章正文")
	mustWriteCurrentReviewArtifactsWithVerdict(t, dir, 1, "accept", domain.AIVoiceRedFlag{
		Rule: "ending_hook_uniformity", Severity: "warning", Evidence: "章末装置重复", Suggestion: "改成行动压力。",
	})

	err := settlePipelineDelivery(dir, pipelineFlags{Start: 1, End: 1})
	if err == nil || (!strings.Contains(err.Error(), "统一审核裁决") && !strings.Contains(err.Error(), "blocking unified review")) {
		t.Fatalf("blocking unified gate should reject delivery, got %v", err)
	}
}

func TestPipelineCausalRewritePendingKeepsChapterOrder(t *testing.T) {
	progress := &domain.Progress{PendingRewrites: []int{3, 1, 2, 2}}
	pending, err := pipelineCausalRewritePending(progress, 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(pending, []int{1, 2}) {
		t.Fatalf("unexpected pending range: %v", pending)
	}
	if _, err := pipelineCausalRewritePending(progress, 2, 3); err == nil || !strings.Contains(err.Error(), "不能从 --from=2 跳过") {
		t.Fatalf("causal rewrite must not skip an earlier pending chapter, got %v", err)
	}
}

func TestPipelineCausalRewriteResumesAtReviewAfterCommit(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	mustWriteFile(t, filepath.Join(dir, "chapters", "01.md"), "第一章 旧正文")
	mustWriteCurrentReviewArtifactsWithVerdict(t, dir, 1, "rewrite")
	if _, err := st.Checkpoints.AppendArtifact(domain.ChapterScope(1), "review", "reviews/01.json"); err != nil {
		t.Fatal(err)
	}
	mustWriteFile(t, filepath.Join(dir, "chapters", "01.md"), "第一章 新正文")
	if _, err := st.Checkpoints.AppendArtifact(domain.ChapterScope(1), "commit", "chapters/01.md"); err != nil {
		t.Fatal(err)
	}
	if !pipelineCausalRewriteAwaitingReview(st, []int{1}) {
		t.Fatal("newer commit should resume directly at review")
	}
	// Simulate reviewExistingPipeline writing through another Store instance.
	// The original checkpoint cache stays stale, but current review artifacts
	// must stop recovery from reviewing the same body forever.
	mustWriteCurrentReviewArtifactsWithVerdict(t, dir, 1, "rewrite")
	other := store.NewStore(dir)
	if _, err := other.Checkpoints.AppendArtifact(domain.ChapterScope(1), "review", "reviews/01.json"); err != nil {
		t.Fatal(err)
	}
	if pipelineCausalRewriteAwaitingReview(st, []int{1}) {
		t.Fatal("current review artifacts should require normal pending-rewrite routing")
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

func mustWriteCurrentReviewArtifacts(t *testing.T, dir string, chapter int, redFlags ...domain.AIVoiceRedFlag) {
	verdict := "accept"
	if len(redFlags) > 0 {
		verdict = "rewrite"
	}
	mustWriteCurrentReviewArtifactsWithVerdict(t, dir, chapter, verdict, redFlags...)
}

func mustWriteCurrentReviewArtifactsWithVerdict(t *testing.T, dir string, chapter int, verdict string, redFlags ...domain.AIVoiceRedFlag) {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(dir, "chapters", fmt.Sprintf("%02d.md", chapter)))
	if err != nil {
		t.Fatal(err)
	}
	bodyHash := reviewreport.BodySHA256(string(body))
	mechanical := reviewreport.MechanicalGatePayload{Chapter: chapter, BodySHA256: bodyHash}
	voice := domain.AIVoiceAnalysis{Chapter: chapter, BodySHA256: bodyHash, Label: "可通过", RedFlags: redFlags}
	editor := domain.ReviewEntry{Chapter: chapter, BodySHA256: bodyHash, Scope: "chapter", Verdict: verdict, Summary: "章级审核通过"}
	if verdict == "rewrite" || verdict == "polish" {
		editor.AffectedChapters = []int{chapter}
	}
	judge := deepseekAIJudgeArtifact{
		Chapter: chapter, BodySHA256: bodyHash, RawBodyOnly: true, UserPayloadKind: "chapter_body_only", Verdict: "human_like", RiskLevel: "low",
		AIProbabilityPercent: 3, PassExclusivePercent: 4, AdviceComplete: true,
	}
	mustWriteJSONFile(t, filepath.Join(dir, "reviews", fmt.Sprintf("%02d_ai_gate.json", chapter)), mechanical)
	mustWriteJSONFile(t, filepath.Join(dir, "reviews", fmt.Sprintf("%02d_ai_voice_redflags.json", chapter)), voice)
	mustWriteJSONFile(t, filepath.Join(dir, "reviews", fmt.Sprintf("%02d.json", chapter)), editor)
	mustWriteJSONFile(t, filepath.Join(dir, "reviews", fmt.Sprintf("%02d_deepseek_ai_judge.json", chapter)), judge)
	if err := reviewreport.WriteUnifiedMarkdown(dir, reviewreport.UnifiedMarkdownInput{
		Chapter: chapter, Mechanical: &mechanical, AIVoice: &voice, Editor: &editor,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := rebuildReviewSummary(dir); err != nil {
		t.Fatal(err)
	}
}

func mustWriteJSONFile(t *testing.T, path string, value any) {
	t.Helper()
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	mustWriteFile(t, path, string(raw))
}
