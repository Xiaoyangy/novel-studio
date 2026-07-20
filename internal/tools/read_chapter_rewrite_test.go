package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/reviewreport"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func TestReadChapterWithholdsOldFinalDuringRewriteReplanning(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Save(&domain.Progress{
		TotalChapters:     2,
		CompletedChapters: []int{1},
		PendingRewrites:   []int{1},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.Drafts.SaveChapterPlan(domain.ChapterPlan{Chapter: 1, Title: "旧计划"}); err != nil {
		t.Fatal(err)
	}
	if err := st.Drafts.SaveFinalChapter(1, "# 第1章 旧终稿\n\n旧事件"); err != nil {
		t.Fatal(err)
	}
	oldTime := time.Now().Add(-time.Hour)
	finalTime := time.Now()
	if err := os.Chtimes(filepath.Join(dir, "drafts/01.plan.json"), oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filepath.Join(dir, "chapters/01.md"), finalTime, finalTime); err != nil {
		t.Fatal(err)
	}

	tool := NewReadChapterTool(st)
	raw, err := tool.Execute(context.Background(), json.RawMessage(`{"chapter":1,"source":"final"}`))
	if err != nil {
		t.Fatal(err)
	}
	var blocked map[string]any
	if err := json.Unmarshal(raw, &blocked); err != nil {
		t.Fatal(err)
	}
	if blocked["withheld"] != true || blocked["stage"] != "rewrite_replanning" {
		t.Fatalf("expected planning-stage withholding, got %s", raw)
	}
	if _, ok := blocked["content"]; ok {
		t.Fatal("old final content must not enter rewrite planning context")
	}

	newTime := finalTime.Add(time.Hour)
	if err := os.Chtimes(filepath.Join(dir, "drafts/01.plan.json"), newTime, newTime); err != nil {
		t.Fatal(err)
	}
	raw, err = tool.Execute(context.Background(), json.RawMessage(`{"chapter":1,"source":"final"}`))
	if err != nil {
		t.Fatal(err)
	}
	var allowed map[string]any
	if err := json.Unmarshal(raw, &allowed); err != nil {
		t.Fatal(err)
	}
	if allowed["content"] == nil || allowed["withheld"] == true {
		t.Fatalf("expected final read after fresh plan, got %s", raw)
	}
}

func TestReadChapterAcceptedFinalBypassesLegacyDraftQuotaInspection(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	body := "# 第1章 已验收终稿\n\n报警记录和连续录屏分别封存，正文保持不变。"
	if err := st.Drafts.SaveChapterPlan(domain.ChapterPlan{Chapter: 1, Title: "冻结计划"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Checkpoints.AppendArtifact(domain.ChapterScope(1), "plan", "drafts/01.plan.json"); err != nil {
		t.Fatal(err)
	}
	if err := st.Drafts.SaveDraft(1, body); err != nil {
		t.Fatal(err)
	}
	// Historical C2-style journals used edit (not draft) as the first body
	// checkpoint after plan, so the local-soft quota has no eligible seed.
	if _, err := st.Checkpoints.AppendArtifact(domain.ChapterScope(1), "edit", "drafts/01.draft.md"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := draftLocalSoftEditQuotaIdentity(st, 1); err == nil {
		t.Fatal("fixture must reproduce the missing formal-review/initial-draft seed")
	}
	if err := st.Drafts.SaveFinalChapter(1, body); err != nil {
		t.Fatal(err)
	}
	if err := st.World.SaveReview(domain.ReviewEntry{
		Chapter: 1, Scope: "chapter", BodySHA256: reviewreport.BodySHA256(body),
		Verdict: "accept", ContractStatus: "met",
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Save(&domain.Progress{
		TotalChapters: 1, CurrentChapter: 2, CompletedChapters: []int{1},
	}); err != nil {
		t.Fatal(err)
	}

	tool := NewReadChapterTool(st)
	for _, args := range []string{
		`{"chapter":1,"source":"final"}`,
		`{"from":1,"to":1,"source":"final","max_runes":40000}`,
	} {
		raw, err := tool.Execute(context.Background(), json.RawMessage(args))
		if err != nil {
			t.Fatalf("accepted final read failed for %s: %v", args, err)
		}
		if !strings.Contains(string(raw), "报警记录和连续录屏") || strings.Contains(string(raw), "withheld") {
			t.Fatalf("accepted exact-body final was not returned for %s: %s", args, raw)
		}
	}
	progress, err := st.Progress.Load()
	if err != nil {
		t.Fatal(err)
	}
	progress.InProgressChapter = 1
	if err := st.Progress.Save(progress); err != nil {
		t.Fatal(err)
	}
	if stable, err := tool.acceptedFinalSurfaceStable(1); err != nil || stable {
		t.Fatalf("in-progress chapter entered accepted-final fast path: stable=%v err=%v", stable, err)
	}
	progress.InProgressChapter = 0
	if err := st.Progress.Save(progress); err != nil {
		t.Fatal(err)
	}
	if err := st.Signals.SavePendingCommit(domain.PendingCommit{Chapter: 1, Stage: domain.CommitStageStarted}); err != nil {
		t.Fatal(err)
	}
	if stable, err := tool.acceptedFinalSurfaceStable(1); err != nil || stable {
		t.Fatalf("pending commit entered accepted-final fast path: stable=%v err=%v", stable, err)
	}
}

func TestReadChapterAcceptedFinalDoesNotBypassNewExplicitRerender(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	body := "# 第1章 已验收但被重新抽查\n\n当前终稿。"
	if err := st.Drafts.SaveDraft(1, body); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Checkpoints.AppendArtifact(domain.ChapterScope(1), "draft", "drafts/01.draft.md"); err != nil {
		t.Fatal(err)
	}
	if err := st.Drafts.SaveFinalChapter(1, body); err != nil {
		t.Fatal(err)
	}
	if err := st.World.SaveReview(domain.ReviewEntry{
		Chapter: 1, Scope: "chapter", BodySHA256: reviewreport.BodySHA256(body),
		Verdict: "accept", ContractStatus: "met",
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Save(&domain.Progress{TotalChapters: 1, CurrentChapter: 2, CompletedChapters: []int{1}}); err != nil {
		t.Fatal(err)
	}
	requestPath := filepath.Join(st.Dir(), "drafts", "01.rerender_request.json")
	if err := os.WriteFile(requestPath, []byte(`{"chapter":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Checkpoints.AppendArtifact(domain.ChapterScope(1), "rerender-request", "drafts/01.rerender_request.json"); err != nil {
		t.Fatal(err)
	}
	raw, err := NewReadChapterTool(st).Execute(context.Background(), json.RawMessage(`{"chapter":1,"source":"final"}`))
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got["withheld"] != true || got["trigger"] != "explicit_full_rerender" {
		t.Fatalf("accepted final bypassed a newer explicit rerender: %s", raw)
	}
}

func TestReadChapterWithholdsSupersededSurfaceDuringRenderOnlyFreshDraft(t *testing.T) {
	tests := []struct {
		name    string
		trigger string
		setup   func(*testing.T, *store.Store, string)
	}{
		{
			name:    "explicit full rerender",
			trigger: "explicit_full_rerender",
			setup: func(t *testing.T, st *store.Store, _ string) {
				t.Helper()
				if _, err := st.Checkpoints.AppendArtifact(domain.ChapterScope(1), "draft", "drafts/01.draft.md"); err != nil {
					t.Fatal(err)
				}
				requestPath := filepath.Join(st.Dir(), "drafts", "01.rerender_request.json")
				if err := os.WriteFile(requestPath, []byte(`{"chapter":1}`), 0o644); err != nil {
					t.Fatal(err)
				}
				if _, err := st.Checkpoints.AppendArtifact(domain.ChapterScope(1), "rerender-request", "drafts/01.rerender_request.json"); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name:    "external whole chapter rerender",
			trigger: "external_full_rerender",
			setup: func(t *testing.T, st *store.Store, body string) {
				t.Helper()
				if err := SetDraftExternalRerenderRequirement(st.Dir(), DraftExternalRerenderRequirement{
					Chapter: 1, EvaluatedBodySHA256: reviewreport.BodySHA256(body),
					AIProbabilityPercent: 86, PassExclusivePercent: 4,
					RevisionPlan: []string{"整章重组人物选择链与段落功能。"}, AdviceComplete: true,
				}); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name:    "formal review fresh draft",
			trigger: "formal_review_fresh_draft",
			setup: func(t *testing.T, st *store.Store, body string) {
				t.Helper()
				if err := st.World.SaveReview(domain.ReviewEntry{
					Chapter: 1, BodySHA256: reviewreport.BodySHA256(body), Verdict: "rewrite", ContractStatus: "met",
				}); err != nil {
					t.Fatal(err)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := store.NewStore(t.TempDir())
			if err := st.Init(); err != nil {
				t.Fatal(err)
			}
			body := "# 第1章 旧正文\n\n这段旧措辞不能进入整章重渲染上下文。"
			if err := st.Drafts.SaveDraft(1, body); err != nil {
				t.Fatal(err)
			}
			if err := st.Drafts.SaveFinalChapter(1, body); err != nil {
				t.Fatal(err)
			}
			tt.setup(t, st, body)

			tool := NewReadChapterTool(st)
			for _, source := range []string{"draft", "final"} {
				raw, err := tool.Execute(context.Background(), json.RawMessage(`{"chapter":1,"source":"`+source+`"}`))
				if err != nil {
					t.Fatal(err)
				}
				var got map[string]any
				if err := json.Unmarshal(raw, &got); err != nil {
					t.Fatal(err)
				}
				if got["withheld"] != true || got["stage"] != "render_only_fresh_draft" || got["trigger"] != tt.trigger {
					t.Fatalf("source=%s did not fail closed: %s", source, raw)
				}
				if _, ok := got["content"]; ok {
					t.Fatalf("source=%s leaked superseded prose: %s", source, raw)
				}
			}
		})
	}
}

func TestReadChapterAllowsNewCandidateAfterExplicitRerenderConsumed(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	oldBody := "# 第1章 旧正文\n\n旧候选。"
	if err := st.Drafts.SaveDraft(1, oldBody); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Checkpoints.AppendArtifact(domain.ChapterScope(1), "draft", "drafts/01.draft.md"); err != nil {
		t.Fatal(err)
	}
	requestPath := filepath.Join(st.Dir(), "drafts", "01.rerender_request.json")
	if err := os.WriteFile(requestPath, []byte(`{"chapter":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Checkpoints.AppendArtifact(domain.ChapterScope(1), "rerender-request", "drafts/01.rerender_request.json"); err != nil {
		t.Fatal(err)
	}
	if err := SetDraftExternalRerenderRequirement(st.Dir(), DraftExternalRerenderRequirement{
		Chapter: 1, EvaluatedBodySHA256: reviewreport.BodySHA256(oldBody),
		AIProbabilityPercent: 86, PassExclusivePercent: 4,
		RevisionPlan: []string{"重渲染完整新稿后复判。"}, AdviceComplete: true,
	}); err != nil {
		t.Fatal(err)
	}

	newBody := "# 第1章 新正文\n\n这是晚于整章重渲染请求的当前候选。"
	if err := st.Drafts.SaveDraft(1, newBody); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Checkpoints.AppendArtifact(domain.ChapterScope(1), "draft", "drafts/01.draft.md"); err != nil {
		t.Fatal(err)
	}
	raw, err := NewReadChapterTool(st).Execute(context.Background(), json.RawMessage(`{"chapter":1,"source":"draft"}`))
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got["withheld"] == true || got["content"] != newBody {
		t.Fatalf("current candidate reread was blocked after request consumption: %s", raw)
	}
}

func TestReadChapterRenderLockOnlyAllowsCurrentCandidateSurface(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Drafts.SaveDraft(1, "# 当前候选\n\n只允许回读这一份。"); err != nil {
		t.Fatal(err)
	}
	if err := st.Drafts.SaveFinalChapter(1, "# 旧终稿\n\n不允许作为动态输入。"); err != nil {
		t.Fatal(err)
	}
	if err := st.Runtime.AcquirePipelineExecution(domain.PipelineExecutionLock{
		Mode:          domain.PipelineExecutionRender,
		TargetChapter: 1,
		PlanDigest:    "sha256:frozen",
		Owner:         "read-surface-test",
	}); err != nil {
		t.Fatal(err)
	}
	tool := NewReadChapterTool(st)
	raw, err := tool.Execute(context.Background(), json.RawMessage(`{"chapter":1,"source":"draft"}`))
	if err != nil || !strings.Contains(string(raw), "当前候选") {
		t.Fatalf("current render candidate should remain readable: raw=%s err=%v", raw, err)
	}
	for _, args := range []string{
		`{"chapter":1,"source":"final"}`,
		`{"chapter":2,"source":"draft"}`,
		`{"from":1,"to":2,"source":"draft"}`,
		`{"character":"林澈","source":"draft"}`,
	} {
		if _, err := tool.Execute(context.Background(), json.RawMessage(args)); err == nil ||
			!strings.Contains(err.Error(), "render execution lock") {
			t.Fatalf("render lock accepted live chapter surface args=%s err=%v", args, err)
		}
	}
}

func TestReadChapterRangeWithholdsWhenItWouldIncludeRenderOnlyTarget(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	for chapter := 1; chapter <= 2; chapter++ {
		if err := st.Drafts.SaveFinalChapter(chapter, "# 旧终稿\n\n范围读取不应泄漏重渲染目标。"); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.Drafts.SaveDraft(1, "# 旧草稿\n\n范围读取同样不能绕过重渲染门禁。"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Checkpoints.AppendArtifact(domain.ChapterScope(1), "draft", "drafts/01.draft.md"); err != nil {
		t.Fatal(err)
	}
	requestPath := filepath.Join(st.Dir(), "drafts", "01.rerender_request.json")
	if err := os.WriteFile(requestPath, []byte(`{"chapter":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Checkpoints.AppendArtifact(domain.ChapterScope(1), "rerender-request", "drafts/01.rerender_request.json"); err != nil {
		t.Fatal(err)
	}
	raw, err := NewReadChapterTool(st).Execute(context.Background(), json.RawMessage(`{"from":1,"to":2,"source":"final"}`))
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got["withheld"] != true || got["blocked_chapter"] != float64(1) {
		t.Fatalf("range read leaked a render-only target: %s", raw)
	}
	if _, ok := got["chapters"]; ok {
		t.Fatalf("withheld range must not contain prose: %s", raw)
	}
}
