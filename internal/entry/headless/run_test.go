package headless

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/host"
	"github.com/chenhongyang/novel-studio/internal/rules"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/tools"
)

func TestSealedConvergenceDeterministicPreconditionStopsOnFirstFailure(t *testing.T) {
	events := []host.Event{
		{Category: "ERROR", Agent: "writer", Detail: `writer → novel_context: novel_context profile="planning" 的关键上下文无法安全收敛：hard=65536 actual=88205`},
		{Category: "ERROR", Agent: "writer", Detail: "writer → novel_context: same immutable failure repeated"},
		{Category: "ERROR", Agent: "writer", Detail: "writer → novel_context: same immutable failure repeated"},
	}
	observedCalls := 0
	for _, event := range events {
		observedCalls++
		if err := sealedConvergenceDeterministicPreconditionError(event, 5); err != nil {
			break
		}
	}
	if observedCalls != 1 {
		t.Fatalf("deterministic convergence precondition repeated %d times, want exactly 1", observedCalls)
	}
	ordinary := host.Event{Category: "ERROR", Agent: "writer", Detail: "plan_details 缺少一个可由 Planner 补齐的字段"}
	if err := sealedConvergenceDeterministicPreconditionError(ordinary, 5); err != nil {
		t.Fatalf("ordinary repairable planner error was made fatal: %v", err)
	}
	authority := host.Event{Category: "ERROR", Agent: "writer", Detail: "stored project-all authority receipt invalid: active project-all authority binding no longer matches receipt"}
	if err := sealedConvergenceDeterministicPreconditionError(authority, 5); err == nil {
		t.Fatal("immutable authority binding failure did not stop immediately")
	}
	projectedState := host.Event{Category: "ERROR", Agent: "writer", Detail: "plan_details 缺少 novel_context 返回的 exact project-all-state authoritative source token"}
	if err := sealedConvergenceDeterministicPreconditionError(projectedState, 5); err == nil {
		t.Fatal("missing immutable project-all state binding did not stop immediately")
	}
}

func TestInspectRenderOnlyReplanStopRejectsExhaustedCausalEpoch(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Checkpoints.Append(domain.ChapterScope(1), "plan", "drafts/01.plan.json", "plan-epoch"); err != nil {
		t.Fatal(err)
	}
	if err := inspectRenderOnlyReplanStop(dir, 1); err != nil {
		t.Fatalf("fresh plan should still have render attempts: %v", err)
	}
	for _, digest := range []string{"body-a", "body-b", "body-c"} {
		if _, err := st.Checkpoints.Append(domain.ChapterScope(1), "draft-structural-block", "drafts/01.draft.md", digest); err != nil {
			t.Fatal(err)
		}
	}
	err := inspectRenderOnlyReplanStop(dir, 1)
	if err == nil || !strings.Contains(err.Error(), "禁止自动回到 World Simulator/Planner") {
		t.Fatalf("exhausted render epoch did not request immediate stop: %v", err)
	}
	if cp := st.Checkpoints.LatestByStep(domain.ChapterScope(1), "plan"); cp == nil || cp.Digest != "plan-epoch" {
		t.Fatalf("stop inspection mutated frozen plan: %+v", cp)
	}
}

func TestInspectRenderOnlyReplanStopAbortsBeforeThirdProjectionOnCombinedLedger(t *testing.T) {
	root := t.TempDir()
	sourceOutputDir := filepath.Join(root, "output", "novel")
	candidateID := "render-ch0001-headless-event"
	dir := filepath.Join(filepath.Dir(sourceOutputDir), ".render-candidates", candidateID, "output")
	if err := os.MkdirAll(sourceOutputDir, 0o755); err != nil {
		t.Fatal(err)
	}
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Drafts.SaveChapterPlan(domain.ChapterPlan{Chapter: 1, Title: "冻结"}); err != nil {
		t.Fatal(err)
	}
	plan, err := st.Checkpoints.AppendArtifact(domain.ChapterScope(1), "plan", "drafts/01.plan.json")
	if err != nil {
		t.Fatal(err)
	}
	manifest := map[string]any{
		"version": "pipeline-render-candidate.v2", "candidate_id": candidateID,
		"generation_id": "generation", "chapter": 1,
		"plan_digest": plan.Digest, "plan_checkpoint_seq": plan.Seq,
		"projected_bundle_digest":  "sha256:bundle",
		"promotion_receipt_digest": "sha256:promotion",
		"source_output_dir":        sourceOutputDir,
	}
	ledger := map[string]any{
		"version": "pipeline-render-convergence.v1", "candidate_id": candidateID,
		"generation_id": "generation", "chapter": 1,
		"plan_digest": plan.Digest, "plan_checkpoint_seq": plan.Seq,
		"projected_bundle_digest":  "sha256:bundle",
		"promotion_receipt_digest": "sha256:promotion", "failure_limit": 3,
		"records": []map[string]any{
			{"body_sha256": strings.Repeat("a", 64), "semantic_reject": true},
			{"body_sha256": strings.Repeat("b", 64), "structural_block": true},
			{"body_sha256": strings.Repeat("c", 64), "structural_block": true},
		},
	}
	writeJSON := func(path string, value any) {
		t.Helper()
		raw, marshalErr := json.Marshal(value)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		if mkdirErr := os.MkdirAll(filepath.Dir(path), 0o755); mkdirErr != nil {
			t.Fatal(mkdirErr)
		}
		if writeErr := os.WriteFile(path, raw, 0o644); writeErr != nil {
			t.Fatal(writeErr)
		}
	}
	writeJSON(filepath.Join(dir, "meta", "planning", "render_candidate.json"), manifest)
	writeJSON(filepath.Join(
		filepath.Dir(sourceOutputDir), ".render-candidates", "convergence", candidateID, "ledger.json",
	), ledger)

	thirdProjectionCalls := 0
	err = inspectRenderOnlyReplanStop(dir, 1)
	if err == nil {
		thirdProjectionCalls++
	}
	if !tools.IsRenderConvergencePlanStageRequired(err) {
		t.Fatalf("post-tool event did not return typed combined-ledger stop: %v", err)
	}
	if thirdProjectionCalls != 0 {
		t.Fatalf("headless event boundary dispatched a third prose projection: %d", thirdProjectionCalls)
	}
}

func TestShouldStopAfterChapterDraftUsesNewWholeDraftCheckpointOnly(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Checkpoints.Append(domain.ChapterScope(5), "draft", "drafts/05.draft.md", "old-draft"); err != nil {
		t.Fatal(err)
	}
	baseline := latestChapterDraftSeq(dir, 5)
	if shouldStopAfterChapterDraft(dir, 5, baseline) {
		t.Fatal("an old recovered draft checkpoint stopped the new Host turn")
	}
	if _, err := st.Checkpoints.Append(domain.ChapterScope(5), "edit", "drafts/05.draft.md", "edited-draft"); err != nil {
		t.Fatal(err)
	}
	if shouldStopAfterChapterDraft(dir, 5, baseline) {
		t.Fatal("an edit checkpoint was mistaken for a new whole-body render")
	}
	if _, err := st.Checkpoints.Append(domain.ChapterScope(5), "draft", "drafts/05.draft.md", "new-draft"); err != nil {
		t.Fatal(err)
	}
	if !shouldStopAfterChapterDraft(dir, 5, baseline) {
		t.Fatal("new whole-body draft checkpoint did not stop frozen render Host")
	}
	if shouldStopAfterChapterDraft(dir, 4, baseline) || shouldStopAfterChapterDraft(dir, 0, baseline) {
		t.Fatal("wrong/disabled chapter inherited the render draft stop")
	}
}

func TestShouldStopAfterChapterWaitsForPendingRewrite(t *testing.T) {
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

	if shouldStopAfterChapter(dir, 5) {
		t.Fatal("expected stop-after to wait while target-range rewrites are pending")
	}

	if err := st.Progress.ClearPendingRewrites(); err != nil {
		t.Fatal(err)
	}
	if !shouldStopAfterChapter(dir, 5) {
		t.Fatal("expected stop-after to fire after pending rewrites are drained")
	}
}

func TestShouldStopAfterInitialWorldTickRequiresSubstantiveEvents(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init("长篇", 120); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.SetLayered(true); err != nil {
		t.Fatal(err)
	}
	if err := st.Outline.SavePremise("县城商户在开篇前形成现实经营压力。"); err != nil {
		t.Fatal(err)
	}
	if err := st.Outline.SaveLayeredOutline([]domain.VolumeOutline{{
		Index: 1,
		Title: "第一卷",
		Arcs: []domain.ArcOutline{{
			Index: 1,
			Title: "第一弧",
			Chapters: []domain.OutlineEntry{{
				Chapter:   1,
				Title:     "开张",
				CoreEvent: "县城商户形成第一笔可见交易",
			}},
		}},
	}}); err != nil {
		t.Fatal(err)
	}
	snapshot := rules.BuildSnapshot([]rules.Candidate{rules.SystemDefaults()})
	if err := st.UserRules.Save(&snapshot); err != nil {
		t.Fatal(err)
	}
	if err := st.WorldSim.SaveTick(domain.WorldTick{TickID: "v0-a0", ThroughChapter: 0}); err != nil {
		t.Fatal(err)
	}
	if shouldStopAfterInitialWorldTickReady(dir) {
		t.Fatal("empty zero-init world_tick baseline must not stop the stage")
	}
	if err := st.Characters.Save([]domain.Character{{Name: "县城商户", Role: "开篇群体角色"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.WorldSim.AppendWorldEvents([]domain.WorldEvent{{
		TickID:            "v1-a1",
		Chapter:           0,
		Actors:            []string{"县城商户"},
		Summary:           "青山县商户在开局前形成第一条离屏价格波动。",
		VisibilityChapter: 1,
		VisibilityPath:    "收据和街面闲聊",
	}}); err != nil {
		t.Fatal(err)
	}
	if shouldStopAfterInitialWorldTickReady(dir) {
		t.Fatal("events without a substantive world_tick cursor must not stop the stage")
	}
	if err := st.WorldSim.SaveTick(domain.WorldTick{TickID: "v1-a1", Volume: 1, Arc: 1, ThroughChapter: 0, EventCount: 1}); err != nil {
		t.Fatal(err)
	}
	if !shouldStopAfterInitialWorldTickReady(dir) {
		t.Fatal("substantive world events should stop the stage")
	}
}

func TestShouldStopAfterFoundationChangedRequiresDigestChange(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "meta"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"premise.md", "characters.json", "world_rules.json", "book_world.json", "world_codex.json", filepath.Join("meta", "compass.json")} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(`{"ready":true}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"outline.json", "layered_outline.json"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(`{"version":1}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	initial := foundationRevisionDigest(dir)
	if initial == "" {
		t.Fatal("expected initial foundation digest")
	}
	if shouldStopAfterFoundationChanged(dir, initial) {
		t.Fatal("unchanged foundation should not stop")
	}
	if err := os.WriteFile(filepath.Join(dir, "outline.json"), []byte(`{"version":2}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if !shouldStopAfterFoundationChanged(dir, initial) {
		t.Fatal("changed foundation should stop")
	}
}

func TestShouldStopAfterChapterCommitRequiresNewCheckpoint(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Drafts.SaveFinalChapter(1, "第一版正文"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Checkpoints.AppendArtifact(domain.ChapterScope(1), "commit", "chapters/01.md"); err != nil {
		t.Fatal(err)
	}
	initial := latestChapterCommitSeq(dir, 1)
	if shouldStopAfterChapterCommit(dir, 1, initial) {
		t.Fatal("existing commit must not stop a resumed rewrite")
	}
	if err := st.Drafts.SaveFinalChapter(1, "第二版正文"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Checkpoints.AppendArtifact(domain.ChapterScope(1), "commit", "chapters/01.md"); err != nil {
		t.Fatal(err)
	}
	if !shouldStopAfterChapterCommit(dir, 1, initial) {
		t.Fatal("new commit checkpoint should return control to pipeline review")
	}
}

func TestShouldStopAfterGlobalReviewRequiresNewGlobalCheckpoint(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.World.SaveReview(domain.ReviewEntry{
		Chapter: 2, Scope: "global", Verdict: "rewrite", Summary: "旧全文终审",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Checkpoints.AppendArtifactLatestAcross(
		domain.ChapterScope(2), "review", "reviews/02-global.json", "review", "commit",
	); err != nil {
		t.Fatal(err)
	}
	initial := latestGlobalReviewSeq(dir, 2)
	if shouldStopAfterGlobalReview(dir, 2, initial) {
		t.Fatal("an existing global review must not stop a resumed finalizer")
	}
	if _, err := st.Checkpoints.Append(domain.ChapterScope(2), "commit", "chapters/02.md", "new-body"); err != nil {
		t.Fatal(err)
	}
	if err := st.World.SaveReview(domain.ReviewEntry{
		Chapter: 2, Scope: "global", Verdict: "accept", Summary: "新全文终审",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Checkpoints.AppendArtifactLatestAcross(
		domain.ChapterScope(2), "review", "reviews/02-global.json", "review", "commit",
	); err != nil {
		t.Fatal(err)
	}
	if !shouldStopAfterGlobalReview(dir, 2, initial) {
		t.Fatal("a new global review checkpoint should return control to pipeline finalization")
	}
}

func TestShouldStopAfterChapterPlanRequiresNewFormalPlanCheckpoint(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Drafts.SaveChapterPlan(domain.ChapterPlan{Chapter: 3, Title: "旧计划"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Checkpoints.AppendArtifact(domain.ChapterScope(3), "plan", "drafts/03.plan.json"); err != nil {
		t.Fatal(err)
	}
	initial := latestChapterPlanSeq(dir, 3)
	if shouldStopAfterChapterPlan(dir, 3, initial) {
		t.Fatal("existing formal plan checkpoint must not stop a resumed planner")
	}
	if err := st.Drafts.SaveChapterPlan(domain.ChapterPlan{Chapter: 3, Title: "新计划"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Checkpoints.AppendArtifactLatest(domain.ChapterScope(3), "plan", "drafts/03.plan.json"); err != nil {
		t.Fatal(err)
	}
	if !shouldStopAfterChapterPlan(dir, 3, initial) {
		t.Fatal("new formal plan checkpoint should return control to the pipeline")
	}
}

func TestShouldStopAfterInitialWorldTickUsesLayeredOutlineWhenProgressUninitialized(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Outline.SaveLayeredOutline([]domain.VolumeOutline{{
		Index: 1,
		Title: "第一卷",
		Arcs: []domain.ArcOutline{{
			Index: 1,
			Title: "第一弧",
			Chapters: []domain.OutlineEntry{
				{Chapter: 1, Title: "一"},
				{Chapter: 2, Title: "二"},
			},
		}},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := st.WorldSim.SaveTick(domain.WorldTick{TickID: "v0-a0", ThroughChapter: 0}); err != nil {
		t.Fatal(err)
	}
	if shouldStopAfterInitialWorldTickReady(dir) {
		t.Fatal("layered outline without progress.layered must still reject empty world_tick baseline")
	}
}
