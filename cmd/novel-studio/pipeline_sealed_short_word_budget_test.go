package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/rules"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/tools"
)

func TestPipelineZeroCostWordGateUsesSealedShortAcceptedCumulative(t *testing.T) {
	live := outlineAllGateLiveDir(t)
	receiptCandidate := filepath.Join(t.TempDir(), "word-budget-attempt", "candidate", "output")
	receipt := writeOutlineAllGateCompleteReceipt(t, live, receiptCandidate, outlineAllGateDigest)
	receipt.EstimatedScale = "1-1卷，12-12章；正文2.8万—3万字"
	receipt.MinVolumes = 1
	receipt.MaxVolumes = 1
	receipt.MinChapters = 12
	receipt.MaxChapters = 12
	receipt.TargetVolumes = 1
	receipt.TargetChapters = 12
	receipt.TargetWords = 29000
	receipt.TargetWordsPerChapter = 2417
	var err error
	receipt, err = domain.SignOutlineAllExecutionReceipt(receipt)
	if err != nil {
		t.Fatal(err)
	}

	liveStore := store.NewStore(live)
	if err := liveStore.SaveOutlineAllExecutionReceipt(receipt); err != nil {
		t.Fatal(err)
	}
	if err := liveStore.RunMeta.Save(domain.RunMeta{PlanningTier: domain.PlanningTierShort}); err != nil {
		t.Fatal(err)
	}
	if err := liveStore.UserRules.Save(&rules.Snapshot{Structured: rules.Structured{
		ChapterWords: &rules.WordRange{Min: 2200, Max: 2600},
	}}); err != nil {
		t.Fatal(err)
	}
	counts := map[int]int{1: 2350, 2: 2350, 3: 2350, 4: 2348}
	for chapter := 1; chapter <= 4; chapter++ {
		if err := liveStore.Drafts.SaveFinalChapter(chapter, strings.Repeat("她", counts[chapter])); err != nil {
			t.Fatal(err)
		}
	}
	if err := liveStore.Progress.Save(&domain.Progress{
		NovelName:         "零成本动态字数门禁",
		Phase:             domain.PhaseWriting,
		CurrentChapter:    5,
		TotalChapters:     12,
		CompletedChapters: []int{1, 2, 3, 4},
		TotalWordCount:    9398,
		ChapterWordCounts: counts,
	}); err != nil {
		t.Fatal(err)
	}
	plan := domain.ChapterPlan{Chapter: 5, Title: "旧账翻到第五页"}
	if err := liveStore.Drafts.SaveChapterPlan(plan); err != nil {
		t.Fatal(err)
	}
	checkpoint, err := liveStore.Checkpoints.AppendArtifactLatest(
		domain.ChapterScope(5),
		"plan",
		"drafts/05.plan.json",
	)
	if err != nil {
		t.Fatal(err)
	}
	frozen := &pipelineFrozenPlan{
		Version:                pipelinePlanningSchema,
		Chapter:                5,
		PlanDigest:             checkpoint.Digest,
		PlanCheckpointSeq:      checkpoint.Seq,
		RenderContextSHA256:    "sha256:word-budget-context",
		PlanningGenerationID:   "short-word-generation",
		ProjectionBinding:      "sealed_v2",
		ProjectedPlanSHA256:    "sha256:word-budget-plan",
		ProjectedBundleDigest:  "sha256:word-budget-bundle",
		PromotionReceiptDigest: "sha256:word-budget-promotion",
	}
	if _, err := writePipelinePlanningJSON(
		filepath.Join(live, filepath.FromSlash(pipelineFrozenPlanPath)),
		frozen,
	); err != nil {
		t.Fatal(err)
	}
	candidate, err := preparePipelineRenderCandidate(live, frozen)
	if err != nil {
		t.Fatal(err)
	}
	// This test exercises the historical word-budget overlay, not the v3 style
	// receipt boundary. Keep its deliberately non-canonical digest fixture on
	// the legacy v2 candidate protocol.
	manifestPath := filepath.Join(candidate.OutputDir, "meta", "planning", "render_candidate.json")
	manifestRaw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifest pipelineRenderCandidateManifest
	if err := json.Unmarshal(manifestRaw, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.Version = pipelineRenderCandidatePreviousManifestVersion
	manifest.PipelineRenderInputDigest = ""
	manifest.RenderContextSHA256 = ""
	manifest.EffectiveStyleReceiptDigest = ""
	manifestRaw, err = json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, append(manifestRaw, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ensurePipelineRenderConvergenceControlDir(live, manifest.CandidateID); err != nil {
		t.Fatal(err)
	}
	candidateStore := store.NewStore(candidate.OutputDir)
	liveBounds, err := tools.InspectShortChapterWordBoundsFromAcceptedProse(liveStore, 5)
	if err != nil || !liveBounds.Active || liveBounds.Min != 2444 || liveBounds.Max != 2600 {
		t.Fatalf("live convergence planning bounds=%+v err=%v, want 2444-2600", liveBounds, err)
	}
	eligibility := &pipelineSealedConvergenceReplanEligibility{
		Intent: pipelineSealedConvergenceReplanIntent{
			FailureCount: 3, FailureLimit: 3,
			PlannerRepairAttempts: 1, PlannerRepairLimit: 2,
			ChapterWordBounds: liveBounds,
		},
		Plan: plan,
	}
	task := pipelineSealedConvergenceReplanTask(eligibility)
	for _, want := range []string{
		"动态字数合同为 2444-2600 字",
		"causal_simulation.render_capacity.total_target_runes 必须落在 2444-2600",
	} {
		if !strings.Contains(task, want) {
			t.Fatalf("convergence Planner task missing %q: %s", want, task)
		}
	}
	prefetched, err := pipelineSealedConvergencePlanningContextWithWordBounds(
		[]byte(`{"planning_context_access_receipt":{"source_token":"test-token"}}`),
		liveBounds,
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"required_runes_min":2444`,
		`"required_runes_max":2600`,
		`total_target_runes 必须落在 2444-2600`,
		`"source_token":"test-token"`,
	} {
		if !strings.Contains(string(prefetched), want) {
			t.Fatalf("prefetched convergence context missing %q: %s", want, prefetched)
		}
	}
	legacy := pipelineSealedConvergenceReplanIntentV1{
		Version:               pipelineSealedConvergenceReplanIntentLegacyVersion,
		SourceFrozen:          *frozen,
		FailedBodySHA256:      []string{strings.Repeat("a", 64)},
		FailureCount:          3,
		FailureLimit:          3,
		PlannerRepairAttempts: 1,
		PlannerRepairLimit:    2,
		PlannerFailures: []pipelineSealedConvergencePlannerFailure{{
			PlanDigest: checkpoint.Digest,
			PlanSeq:    checkpoint.Seq + 1,
			Class:      "render_plan_invalid",
		}},
		StartedAt: "2026-07-19T08:00:00Z",
	}
	legacy.IntentDigest = pipelineSealedConvergenceReplanIntentV1Digest(legacy)
	legacyPath := filepath.Join(live, filepath.FromSlash(pipelineSealedConvergenceReplanIntentPath))
	if _, err := writePipelinePlanningJSON(legacyPath, legacy); err != nil {
		t.Fatal(err)
	}
	migrated, err := loadPipelineSealedConvergenceReplanIntent(live)
	if err != nil || migrated == nil {
		t.Fatalf("load legacy convergence intent: intent=%+v err=%v", migrated, err)
	}
	if migrated.Version != pipelineSealedConvergenceReplanIntentVersion ||
		migrated.LegacyIntentDigest != legacy.IntentDigest ||
		migrated.PlannerRepairAttempts != 1 ||
		len(migrated.PlannerFailures) != 1 ||
		migrated.StartedAt != legacy.StartedAt ||
		migrated.ChapterWordBounds.Min != 2444 || migrated.ChapterWordBounds.Max != 2600 {
		t.Fatalf("legacy convergence intent was not deterministically backfilled: %+v", migrated)
	}
	legacyRaw, err := os.ReadFile(legacyPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(legacyRaw), "chapter_word_bounds") ||
		!strings.Contains(string(legacyRaw), pipelineSealedConvergenceReplanIntentLegacyVersion) {
		t.Fatalf("legacy load persisted before full validation: %s", legacyRaw)
	}
	migrationOwner := pipelineExecutionOwner("convergence-replan-migration-test", 5)
	if err := liveStore.Runtime.AcquirePipelineExecution(domain.PipelineExecutionLock{
		Mode:          domain.PipelineExecutionProjectAll,
		TargetChapter: 5,
		Owner:         migrationOwner,
	}); err != nil {
		t.Fatal(err)
	}
	if err := persistPipelineSealedConvergenceReplanIntentMigration(live, migrated, migrationOwner); err != nil {
		t.Fatal(err)
	}
	if err := liveStore.Runtime.ReleasePipelineExecution(migrationOwner); err != nil {
		t.Fatal(err)
	}
	upgradedRaw, err := os.ReadFile(legacyPath)
	if err != nil {
		t.Fatal(err)
	}
	var upgraded map[string]any
	if err := json.Unmarshal(upgradedRaw, &upgraded); err != nil {
		t.Fatal(err)
	}
	wordBounds, _ := upgraded["chapter_word_bounds"].(map[string]any)
	if upgraded["version"] != pipelineSealedConvergenceReplanIntentVersion ||
		upgraded["planner_repair_attempts"] != float64(1) ||
		wordBounds["min"] != float64(2444) || wordBounds["max"] != float64(2600) {
		t.Fatalf("legacy convergence intent upgrade lost paid state/bounds: %s", upgradedRaw)
	}

	prefix := "第五章 旧账翻到第五页\n\n"
	body := prefix + strings.Repeat("她", 2400-utf8.RuneCountInString(prefix))
	reason, err := pipelineManagedDraftWordRangeReason(candidateStore, 5, body)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"sealed short cumulative word count out of range",
		"prior_accepted=9398",
		"book=28000-30000",
		"absolute_chapter=2200-2600",
		"actual=2400 required=2444-2600",
	} {
		if !strings.Contains(reason, want) {
			t.Fatalf("zero-cost word reason missing %q: %s", want, reason)
		}
	}

	if err := candidateStore.Drafts.SaveDraft(5, body); err != nil {
		t.Fatal(err)
	}
	canonPath := filepath.Join(candidate.OutputDir, "meta", "chapter_progress.json")
	cursorPath := filepath.Join(candidate.OutputDir, "meta", "planning", "realization_cursor.json")
	if err := os.WriteFile(canonPath, []byte("canon-sentinel\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cursorPath, []byte("cursor-sentinel\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	liveBefore := pipelineRenderCandidateTestSnapshot(t, live)
	manifestRel, err := pipelineQuarantineStaleCausalCandidate(
		candidateStore,
		5,
		[]byte(body),
		reason,
		false,
		false,
	)
	if err != nil {
		t.Fatal(err)
	}
	if manifestRel == "" {
		t.Fatal("zero-cost rejection did not persist its lossless quarantine reason")
	}
	if got, err := os.ReadFile(canonPath); err != nil || string(got) != "canon-sentinel\n" {
		t.Fatalf("zero-cost word rejection changed canon: body=%q err=%v", got, err)
	}
	if got, err := os.ReadFile(cursorPath); err != nil || string(got) != "cursor-sentinel\n" {
		t.Fatalf("zero-cost word rejection changed realization cursor: body=%q err=%v", got, err)
	}
	if _, err := os.Stat(filepath.Join(candidate.OutputDir, "drafts", "05.plan.json")); err != nil {
		t.Fatalf("zero-cost word rejection removed the reusable plan: %v", err)
	}
	if _, err := os.Stat(filepath.Join(candidate.OutputDir, "chapters", "05.md")); !os.IsNotExist(err) {
		t.Fatalf("zero-cost word rejection published prose: %v", err)
	}
	if got := pipelineRenderCandidateTestSnapshot(t, live); len(got) != len(liveBefore) {
		t.Fatalf("isolated zero-cost rejection changed live file count: before=%d after=%d", len(liveBefore), len(got))
	} else {
		for rel, body := range liveBefore {
			if got[rel] != body {
				t.Fatalf("isolated zero-cost rejection changed live %s", rel)
			}
		}
	}
}
