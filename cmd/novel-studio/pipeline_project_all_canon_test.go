package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

func TestProjectAllFirstPromoteRejectsLiveCanonDrift(t *testing.T) {
	opts, st, _ := projectAllCmdTestInstallThreeChapterCLIProjection(t)
	if err := pipelineSeal(opts, pipelineFlags{}); err != nil {
		t.Fatal(err)
	}
	projected := st.ProjectedV2()
	active, err := projected.LoadActiveGeneration()
	if err != nil || active == nil {
		t.Fatalf("load active generation: active=%+v err=%v", active, err)
	}
	generation, err := projected.LoadSealedGeneration(active.GenerationID)
	if err != nil || generation == nil {
		t.Fatalf("load sealed generation: generation=%+v err=%v", generation, err)
	}
	cursor, err := projected.LoadRealizationCursor()
	if err != nil || cursor == nil {
		t.Fatalf("load realization cursor: cursor=%+v err=%v", cursor, err)
	}
	progress, err := st.Progress.Load()
	if err != nil || progress == nil {
		t.Fatal(err)
	}
	if err := validatePipelineProjectAllLiveCanonForPromotion(
		st.Dir(),
		progress,
		projected,
		cursor,
		generation,
	); err != nil {
		t.Fatalf("fresh base canon rejected: %v", err)
	}
	writeProjectAllCanonTestFile(t, st.Dir(), "meta/unplanned_canon_change.json", `{"changed":true}`)
	progress, _ = st.Progress.Load()
	err = validatePipelineProjectAllLiveCanonForPromotion(
		st.Dir(),
		progress,
		projected,
		cursor,
		generation,
	)
	if err == nil || !strings.Contains(err.Error(), "live canon root drift") {
		t.Fatalf("first promote accepted live canon drift: %v", err)
	}
	err = pipelinePromote(opts, pipelineFlags{Start: 1, End: 1})
	if err == nil ||
		!strings.Contains(err.Error(), "promote 锁内第 1 章正史边界失效") ||
		!strings.Contains(err.Error(), "live canon root drift") {
		t.Fatalf("promote execution path accepted live canon drift: %v", err)
	}
}

func TestProjectAllPromoteRerunRecoversAfterStartChapter(t *testing.T) {
	opts, st, _ := projectAllCmdTestInstallThreeChapterCLIProjection(t)
	if err := pipelineSeal(opts, pipelineFlags{}); err != nil {
		t.Fatal(err)
	}
	if err := pipelinePromote(opts, pipelineFlags{Start: 1, End: 1}); err != nil {
		t.Fatalf("first promote: %v", err)
	}
	progress, err := st.Progress.Load()
	if err != nil || progress == nil || progress.InProgressChapter != 1 {
		t.Fatalf("first promote did not reach StartChapter: progress=%+v err=%v", progress, err)
	}
	// Models a crash after the promotion cursor and StartChapter were durable
	// but before pipeline.json could mark the stage complete. The retry must
	// finish the same active promotion instead of treating transient progress
	// fields as an unknown canon mutation.
	if err := pipelinePromote(opts, pipelineFlags{Start: 1, End: 1}); err != nil {
		t.Fatalf("idempotent promote recovery after StartChapter: %v", err)
	}
	cursor, err := st.ProjectedV2().LoadRealizationCursor()
	if err != nil || cursor == nil ||
		cursor.ActivePromotedChapter != 1 ||
		cursor.NextPromoteChapter != 1 {
		t.Fatalf("promote recovery changed realization order: cursor=%+v err=%v", cursor, err)
	}
}

func TestPipelinePromoteIsActiveReceiptRecovery(t *testing.T) {
	tests := []struct {
		name   string
		cursor *domain.RealizationCursorV2
		want   bool
	}{
		{
			name: "exact active next chapter receipt",
			cursor: &domain.RealizationCursorV2{
				NextPromoteChapter:           2,
				ActivePromotedChapter:        2,
				ActivePromotionReceiptDigest: "sha256:active",
			},
			want: true,
		},
		{
			name: "different active chapter",
			cursor: &domain.RealizationCursorV2{
				NextPromoteChapter:           2,
				ActivePromotedChapter:        3,
				ActivePromotionReceiptDigest: "sha256:active",
			},
		},
		{
			name: "missing active receipt",
			cursor: &domain.RealizationCursorV2{
				NextPromoteChapter:    2,
				ActivePromotedChapter: 2,
			},
		},
		{name: "nil cursor"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := pipelinePromoteIsActiveReceiptRecovery(tt.cursor); got != tt.want {
				t.Fatalf("pipelinePromoteIsActiveReceiptRecovery()=%v want %v", got, tt.want)
			}
		})
	}
}

func TestProjectAllPromoteQuarantinesLegacyDraftSurfaces(t *testing.T) {
	opts, st, _ := projectAllCmdTestInstallThreeChapterCLIProjection(t)
	for rel, body := range map[string]string{
		"drafts/01.draft.md":              "旧草稿措辞，不得进入 sealed render。\n",
		"drafts/01.parts/index.json":      `{"chapter":1,"parts":[]}`,
		"drafts/01.parts/part-01.md":      "旧分片措辞。\n",
		"drafts/01.manual_candidate.md":   "旧人工候选。\n",
		"drafts/01.candidate_a.md":        "旧候选 A。\n",
		"drafts/01.hard_consistency.json": `{"old":true}`,
	} {
		writeProjectAllCanonTestFile(t, st.Dir(), rel, body)
	}
	if err := pipelineSeal(opts, pipelineFlags{}); err != nil {
		t.Fatal(err)
	}
	if err := pipelinePromote(opts, pipelineFlags{Start: 1, End: 1}); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{
		"drafts/01.draft.md",
		"drafts/01.parts",
		"drafts/01.manual_candidate.md",
		"drafts/01.candidate_a.md",
		"drafts/01.hard_consistency.json",
	} {
		if _, err := os.Stat(filepath.Join(st.Dir(), filepath.FromSlash(rel))); !os.IsNotExist(err) {
			t.Fatalf("legacy prose surface %s survived sealed promotion: %v", rel, err)
		}
	}
	manifests, err := filepath.Glob(filepath.Join(
		st.Dir(), "meta", "quarantine", "sealed_promotion", "*", "ch0001", "*", "manifest.json",
	))
	if err != nil || len(manifests) != 1 {
		t.Fatalf("sealed promotion did not retain one audit manifest: manifests=%v err=%v", manifests, err)
	}
	archivedParts, err := filepath.Glob(filepath.Join(filepath.Dir(manifests[0]), "01.parts", "part-01.md"))
	if err != nil || len(archivedParts) != 1 {
		t.Fatalf("legacy draft parts were not preserved in quarantine: matches=%v err=%v", archivedParts, err)
	}
}

func TestProjectAllNextPromoteUsesLastAcceptedActualCanonRoot(t *testing.T) {
	opts, st, _ := projectAllCmdTestInstallThreeChapterCLIProjection(t)
	if err := pipelineSeal(opts, pipelineFlags{}); err != nil {
		t.Fatal(err)
	}
	if err := pipelinePromote(opts, pipelineFlags{Start: 1, End: 1}); err != nil {
		t.Fatal(err)
	}
	frozen, _, err := loadAndVerifyPipelineFrozenPlan(st.Dir())
	if err != nil {
		t.Fatal(err)
	}
	binding, err := validatePipelineSealedRenderBinding(st, frozen, false)
	if err != nil {
		t.Fatal(err)
	}
	chapterBody := "第一章\n\n角色完成了可复核的小额验证。\n"
	writeProjectAllCanonTestFile(t, st.Dir(), "chapters/01.md", chapterBody)
	if err := st.Progress.MarkChapterComplete(1, len([]rune(chapterBody)), "choice", "main"); err != nil {
		t.Fatal(err)
	}
	progress, err := st.Progress.Load()
	if err != nil || progress == nil {
		t.Fatal(err)
	}
	actualCanonRoot, err := pipelineProjectAllLiveCanonRoot(st.Dir(), progress)
	if err != nil {
		t.Fatal(err)
	}
	bodySHA, err := pipelineRequiredFileSHA(st.Dir(), "chapters/01.md")
	if err != nil {
		t.Fatal(err)
	}
	commit, err := st.Checkpoints.Append(
		domain.ChapterScope(1),
		"commit",
		"chapters/01.md",
		bodySHA,
	)
	if err != nil {
		t.Fatal(err)
	}
	actualMatch := &pipelineSealedActualDeltaMatch{
		ActualDelta:          binding.Bundle.ProjectedDelta,
		ProjectionMatch:      true,
		Complete:             true,
		ObligationsSatisfied: append([]string(nil), binding.Bundle.ObligationsConsumed...),
	}
	outcome, err := acceptPipelineSealedRenderOutcome(
		st,
		binding,
		commit,
		bodySHA,
		actualCanonRoot,
		actualMatch,
	)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.ActualCanonRoot != actualCanonRoot {
		t.Fatalf("outcome did not persist actual canon root: %+v", outcome)
	}
	projected := st.ProjectedV2()
	cursor, err := projected.LoadRealizationCursor()
	if err != nil || cursor == nil {
		t.Fatal(err)
	}
	generation, err := projected.LoadSealedGeneration(cursor.ActiveGenerationID)
	if err != nil || generation == nil {
		t.Fatal(err)
	}
	loaded, err := projected.LoadActualOutcomeReceipt(
		generation.GenerationID,
		1,
		cursor.LastOutcomeReceiptDigest,
	)
	if err != nil || loaded == nil || loaded.ActualCanonRoot != actualCanonRoot {
		t.Fatalf("durable outcome lost actual canon root: outcome=%+v err=%v", loaded, err)
	}
	progress, _ = st.Progress.Load()
	if err := validatePipelineProjectAllLiveCanonForPromotion(
		st.Dir(),
		progress,
		projected,
		cursor,
		generation,
	); err != nil {
		t.Fatalf("next promote rejected exact last accepted canon root: %v", err)
	}

	for rel, body := range map[string]string{
		"meta/runtime/pipeline_execution.json":      `{"runtime":"changed"}`,
		"meta/planning/current_render_receipt.json": `{"review_receipt":"changed"}`,
		"meta/chapter_metrics/01.json":              `{"review_metric":"changed"}`,
		"reviews/01.json":                           `{"verdict":"accept"}`,
	} {
		writeProjectAllCanonTestFile(t, st.Dir(), rel, body)
	}
	writeProjectAllCanonTestFile(
		t,
		filepath.Dir(st.Dir()),
		".render-publish/tx/finalized_receipt.json",
		`{"phase":"finalized"}`,
	)
	progress, _ = st.Progress.Load()
	if err := validatePipelineProjectAllLiveCanonForPromotion(
		st.Dir(),
		progress,
		projected,
		cursor,
		generation,
	); err != nil {
		t.Fatalf("runtime/review/directory receipts changed causal canon root: %v", err)
	}

	writeProjectAllCanonTestFile(t, st.Dir(), "meta/canon_ledger_drift.json", `{"canon":"drift"}`)
	progress, _ = st.Progress.Load()
	err = validatePipelineProjectAllLiveCanonForPromotion(
		st.Dir(),
		progress,
		projected,
		cursor,
		generation,
	)
	if err == nil || !strings.Contains(err.Error(), "live canon root drift") {
		t.Fatalf("next promote accepted canon drift after actual outcome: %v", err)
	}
}

func writeProjectAllCanonTestFile(t *testing.T, root, rel, body string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
