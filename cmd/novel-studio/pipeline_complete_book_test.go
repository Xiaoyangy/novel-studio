package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func completeBookStageFixture(t *testing.T, withPlanningAncestor ...bool) (cliOptions, *store.Store) {
	t.Helper()
	st := completeBookPublishedFoundation(t)
	if err := st.RunMeta.SetPlanningTier(domain.PlanningTierLong); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.UpdatePhase(domain.PhaseWriting); err != nil {
		t.Fatal(err)
	}
	var previous *domain.PlanningGenerationV2
	var planningParents []string
	if len(withPlanningAncestor) > 0 && withPlanningAncestor[0] {
		old := pipelineWindowCompletionTestWindow(t, st, 1, 1, 6, 1, nil, false)
		planningParents = []string{old.GenerationID}
	}
	for arc := 1; arc <= 2; arc++ {
		first, last := (arc-1)*6+1, arc*6
		previous = pipelineWindowCompletionTestWindow(t, st, first, first, last, arc, previous, true, planningParents...)
		planningParents = nil
		previous = pipelineWindowCompletionTestWindow(t, st, first+3, first, last, arc, previous, true)
		cursor, err := st.ProjectedV2().LoadRealizationCursor()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := completePipelineArcCycle(st, previous, cursor); err != nil {
			t.Fatal(err)
		}
	}
	configPath := filepath.Join(t.TempDir(), "config.json")
	config := fmt.Sprintf(`{"provider":"ollama","model":"no-model-may-be-called","output_dir":%q,"providers":{"ollama":{"type":"openai","base_url":"http://127.0.0.1:1/v1"}}}`, st.Dir())
	if err := os.WriteFile(configPath, []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	return cliOptions{ConfigPath: configPath, Dir: st.Dir()}, st
}

func TestCompleteBookAllowsVerifiedCanonZeroPlanningAncestor(t *testing.T) {
	opts, st := completeBookStageFixture(t, true)
	if err := runPipelineStage("complete-book", opts, pipelineFlags{}, &domain.PipelineState{}, nil); err != nil {
		t.Fatalf("legitimate canon-zero restart planning parent blocked complete book: %v", err)
	}
	progress, err := st.Progress.Load()
	if err != nil || progress.Phase != domain.PhaseComplete {
		t.Fatal("restarted book did not complete", err)
	}
	active, err := st.ProjectedV2().LoadActiveGeneration()
	if err != nil {
		t.Fatal(err)
	}
	current, err := st.ProjectedV2().LoadSealedGeneration(active.GenerationID)
	if err != nil {
		t.Fatal(err)
	}
	for current.BaseCanonChapter > 0 {
		current, err = st.ProjectedV2().LoadSealedGeneration(current.ParentGenerationID)
		if err != nil {
			t.Fatal(err)
		}
	}
	if current.ParentGenerationID == "" {
		t.Fatal("fixture did not retain actual planning ancestor")
	}
	path := filepath.Join(st.Dir(), "meta/planning/v2/generations", current.ParentGenerationID, "generation.json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.WriteFile(path, before, 0o644) }()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := verifyPipelineStage("complete-book", st.Dir(), pipelineFlags{}, &domain.PipelineState{}); err == nil {
		t.Fatal("missing planning ancestor was accepted as a verified parent")
	}
}

func TestCompleteBookExistingMergerDoesNotRepeatHundredthHeading(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init("合并测试", 100); err != nil {
		t.Fatal(err)
	}
	var chapters []int
	var outline []domain.OutlineEntry
	for chapter := 1; chapter <= 100; chapter++ {
		chapters = append(chapters, chapter)
		outline = append(outline, domain.OutlineEntry{Chapter: chapter, Title: "场景"})
		heading := fmt.Sprintf("第%d章 场景", chapter)
		if chapter == 100 {
			heading = "第一百章 场景"
		}
		if err := st.Drafts.SaveFinalChapter(chapter, heading+"\n\n正文保留原样。"); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.Outline.SaveOutline(outline); err != nil {
		t.Fatal(err)
	}
	merged, err := buildPipelineMergedManuscript(st, chapters)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(merged, "第一百章") || strings.Count(merged, "## 第 100 章 场景") != 1 || strings.Count(merged, "正文保留原样。") != 100 {
		t.Fatal("whole-book builder duplicated headings or lost body")
	}
}

func completeBookPublishedFoundation(t *testing.T) *store.Store {
	t.Helper()
	liveDir := filepath.Join(t.TempDir(), "output", "novel")
	if err := os.MkdirAll(liveDir, 0o755); err != nil {
		t.Fatal(err)
	}
	before, err := store.DirectoryContentRoot(liveDir)
	if err != nil {
		t.Fatal(err)
	}
	attempt := outlineAllGateAttemptID()
	candidate := pipelineOutlineAllCandidatePath(liveDir, attempt)
	r := writeOutlineAllGateCompleteReceipt(t, candidate, candidate, before, domain.StoryContractEvidencePolicyNarrationV1)
	st := store.NewStore(candidate)
	compass, err := st.Outline.LoadCompass()
	if err != nil {
		t.Fatal(err)
	}
	compass.EstimatedScale = "1-1卷，12-12章，正文25200-25200字"
	if err := st.Outline.SaveCompass(*compass); err != nil {
		t.Fatal(err)
	}
	_, base := outlineAllGateOutlines()
	refs := append([]domain.StoryContractRef(nil), base[len(base)-1].ContractRefs...)
	for i := range refs {
		refs[i].PlannedPayoffChapter = 12
	}
	volumes := []domain.VolumeOutline{{Index: 1, Title: "第一卷", Theme: "实际证据完整收束"}}
	for arc := 1; arc <= 2; arc++ {
		item := domain.ArcOutline{Index: arc, Title: fmt.Sprintf("弧%d", arc), Goal: "实际选择产生可核对后果并完成本段叙事任务"}
		for chapter := (arc-1)*6 + 1; chapter <= arc*6; chapter++ {
			entry := base[(chapter-1)%len(base)]
			entry.Chapter, entry.Title, entry.ContractRefs = chapter, fmt.Sprintf("第%d次现场核验", chapter), nil
			entry.CoreEvent = fmt.Sprintf("林澈在第%d处现场遭遇商会阻断后重排证据顺序并让一笔冻结款项恢复可追踪状态", chapter)
			entry.Hook = fmt.Sprintf("第%d份新账单迫使居民在下一次会议前执行具体补偿", chapter)
			entry.Scenes = append([]string(nil), entry.Scenes...)
			if chapter == 12 {
				entry.ContractRefs = refs
				entry.CoreEvent += "；" + refs[0].PlannedResolution
				entry.Scenes = append(entry.Scenes, refs[1].PlannedResolution, refs[2].PlannedResolution)
			}
			item.Chapters = append(item.Chapters, entry)
		}
		if arc == 2 {
			item.ContractRefs = refs
		}
		volumes[0].Arcs = append(volumes[0].Arcs, item)
	}
	flat := domain.FlattenOutline(volumes)
	if err := st.Outline.SaveLayeredOutline(volumes); err != nil {
		t.Fatal(err)
	}
	if err := st.Outline.SaveOutline(flat); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.SetTotalChapters(12); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.SetLayered(true); err != nil {
		t.Fatal(err)
	}
	ready := assessArchitectReadiness(candidate)
	if !ready.Ready {
		t.Fatalf("published fixture foundation not ready: %+v", ready)
	}
	if err := writeArchitectReadiness(candidate, ready); err != nil {
		t.Fatal(err)
	}
	r.EstimatedScale = compass.EstimatedScale
	r.MinChapters, r.MaxChapters, r.TargetChapters = 12, 12, 12
	r.TargetWords, r.TargetWordsPerChapter = 25200, 2100
	r.CompassDigest, err = domain.ComputeStoryCompassDigest(*compass)
	if err != nil {
		t.Fatal(err)
	}
	r.FinalLayeredDigest, err = domain.ComputeLayeredOutlineDigest(volumes)
	if err != nil {
		t.Fatal(err)
	}
	r.FinalFlatDigest, err = domain.ComputeFlatOutlineDigest(flat)
	if err != nil {
		t.Fatal(err)
	}
	r.ArchitectReadinessJSONDigest, err = pipelineRequiredFileSHA(candidate, "meta/architect_readiness.json")
	if err != nil {
		t.Fatal(err)
	}
	r.ArchitectReadinessMDDigest, err = pipelineRequiredFileSHA(candidate, "meta/architect_readiness.md")
	if err != nil {
		t.Fatal(err)
	}
	r.ProtectedCanonRoot, err = pipelineOutlineAllProtectedCanonRoot(candidate)
	if err != nil {
		t.Fatal(err)
	}
	r.StableProgressRoot, err = pipelineOutlineAllStableProgressRoot(candidate)
	if err != nil {
		t.Fatal(err)
	}
	foundation, err := loadPipelineOutlineAllFrozenFoundation(candidate)
	if err != nil {
		t.Fatal(err)
	}
	r.FoundationContextRoot = foundation.Root
	r, err = domain.SignOutlineAllExecutionReceipt(r)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SaveOutlineAllExecutionReceipt(r); err != nil {
		t.Fatal(err)
	}
	publisher := store.NewDirectoryPublishStore(pipelineOutlineAllPublishRoot(liveDir))
	published, err := publisher.PublishDirectory(store.PublishDirectoryRequest{TransactionID: attempt, LiveDir: liveDir, CandidateDir: candidate, ExpectedLiveRoot: before})
	if err != nil {
		t.Fatal(err)
	}
	if err := publisher.FinalizeDirectoryPublish(attempt); err != nil {
		t.Fatal(err)
	}
	st = store.NewStore(liveDir)
	if _, err := st.UpdateOutlineAllExecutionReceipt(r.ReceiptDigest, func(receipt *domain.OutlineAllExecutionReceipt) error {
		receipt.PublishedCandidateRoot = published.CandidateRoot
		receipt.DirectoryPublishReceiptDigest = published.ReceiptDigest
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := RequirePublishedOutlineAllIfPresent(liveDir); err != nil {
		t.Fatal(err)
	}
	// Same post-zero marker seam used by published-gate tests; actual chapter
	// acceptance authority is constructed through Store transitions below.
	projectAllCmdTestWriteFile(t, filepath.Join(liveDir, "meta/ch01_zero_init_plan.md"), "fixture entered post-zero production\n")
	return st
}

func TestCompleteBookActualStageExportsVerifiedLongManuscript(t *testing.T) {
	opts, st := completeBookStageFixture(t)
	contractBefore, err := os.ReadFile(filepath.Join(st.Dir(), store.OutlineAllExecutionReceiptPath))
	if err != nil {
		t.Fatal(err)
	}
	state := &domain.PipelineState{Stages: []string{"complete-book"}}
	if err := runPipelineStage("complete-book", opts, pipelineFlags{}, state, map[string][]string{}); err != nil {
		t.Fatalf("LONG_BOOK_DELIVERY_ENTRY_MISSING: real accepted twelve-chapter/four-window book has no executable complete-book export stage: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(st.Dir(), "正文.md"))
	if err != nil || len(got) == 0 {
		t.Fatalf("complete-book did not produce deliverable manuscript: %v", err)
	}
	want, err := buildPipelineMergedManuscript(st, []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12})
	if err != nil || string(got) != want {
		t.Fatal("delivered manuscript differs from actual ordered chapters", err)
	}
	p, err := st.Progress.Load()
	if err != nil || p.Phase != domain.PhaseComplete {
		t.Fatal("book was not completed after manuscript write", err)
	}
	if stages, err := resolveStages("complete-book,deliver"); err != nil || len(stages) != 2 {
		t.Fatal("CLI cannot select complete-book,deliver", err)
	}
	if pipelineStagesNeedQdrant([]string{"complete-book"}) {
		t.Fatal("mechanical completion started retrieval/model infrastructure")
	}
	evidence, err := verifyPipelineStage("complete-book", st.Dir(), pipelineFlags{}, state)
	if err != nil || !strings.Contains(evidence.Message, "no global LLM review") {
		t.Fatal("completion stage lost its exact mechanical evidence", err)
	}
	progressBytes, err := os.ReadFile(filepath.Join(st.Dir(), "meta/progress.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := runPipelineStage("complete-book", opts, pipelineFlags{}, state, nil); err != nil {
		t.Fatal("idempotent verified resume", err)
	}
	after, err := os.ReadFile(filepath.Join(st.Dir(), "meta/progress.json"))
	if err != nil || !bytes.Equal(progressBytes, after) {
		t.Fatal("idempotent resume rewrote completion", err)
	}
	contractAfter, err := os.ReadFile(filepath.Join(st.Dir(), store.OutlineAllExecutionReceiptPath))
	if err != nil || !bytes.Equal(contractBefore, contractAfter) {
		t.Fatal("completion changed frozen receipt or ContractEvidencePolicy", err)
	}
	// A complete phase is not proof that the current exported manuscript is
	// still the deterministic view of the accepted chapter chain.
	if err := st.Drafts.SaveMergedManuscript("原有效稿，不得自动覆盖。"); err != nil {
		t.Fatal(err)
	}
	if _, err := verifyPipelineStage("complete-book", st.Dir(), pipelineFlags{}, state); err == nil {
		t.Fatal("verifier trusted only phase=complete")
	}
	if err := runPipelineStage("complete-book", opts, pipelineFlags{}, state, nil); err == nil || !strings.Contains(err.Error(), "retained unchanged") {
		t.Fatalf("different existing manuscript was not protected: %v", err)
	}
	retained, err := os.ReadFile(filepath.Join(st.Dir(), "正文.md"))
	if err != nil || string(retained) != "原有效稿，不得自动覆盖。" {
		t.Fatal("failed export overwrote existing manuscript", err)
	}
}

func TestCompleteBookRejectsIncompleteProofAndPreservesOriginalManuscript(t *testing.T) {
	opts, st := completeBookStageFixture(t)
	active, err := st.ProjectedV2().LoadActiveGeneration()
	if err != nil {
		t.Fatal(err)
	}
	var chain []*domain.PlanningGenerationV2
	for id := active.GenerationID; id != ""; {
		g, err := st.ProjectedV2().LoadSealedGeneration(id)
		if err != nil || g == nil {
			t.Fatal(err)
		}
		chain = append(chain, g)
		id = g.ParentGenerationID
	}
	if len(chain) != 4 {
		t.Fatal("fixture must contain four authentic accepted windows")
	}
	early, err := st.LoadVerifiedWindowedArcCompletionForGenerationV1(chain[2].GenerationID)
	if err != nil || early == nil {
		t.Fatal(err)
	}
	firstAcceptances, err := st.ArcCycle().ListChapterAcceptanceReceipts(chain[3].GenerationID)
	if err != nil {
		t.Fatal(err)
	}
	want, err := buildPipelineMergedManuscript(st, []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Drafts.SaveMergedManuscript(want); err != nil {
		t.Fatal(err)
	}
	progressPath := filepath.Join(st.Dir(), "meta/progress.json")
	baselineProgress, err := os.ReadFile(progressPath)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, path string
		replace    []byte
	}{
		{"earlier-arc", filepath.Join("meta/planning/v3/arc_cycle/window_aggregates", chain[2].GenerationID, early.ReceiptDigest+".json"), nil},
		{"middle-window", filepath.Join("meta/planning/v2/generations", chain[1].GenerationID, "generation.json"), nil},
		{"first-acceptance", filepath.Join("meta/planning/v3/arc_cycle/acceptances", chain[3].GenerationID, "000001", firstAcceptances[0].ReceiptDigest+".json"), nil},
		{"changed-body", "chapters/01.md", []byte(strings.Repeat("改", 2100))},
		{"changed-review", "reviews/01.json", []byte(`{"chapter":1,"scope":"chapter","verdict":"rewrite"}`)},
		{"missing-contract", store.OutlineAllExecutionReceiptPath, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(st.Dir(), tc.path)
			original, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := os.WriteFile(path, original, 0o644); err != nil {
					t.Error(err)
				}
			}()
			if tc.replace == nil {
				err = os.Remove(path)
			} else {
				err = os.WriteFile(path, tc.replace, 0o644)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := runPipelineStage("complete-book", opts, pipelineFlags{}, &domain.PipelineState{}, nil); err == nil {
				t.Fatal("incomplete source chain was completed")
			}
			body, err := os.ReadFile(filepath.Join(st.Dir(), "正文.md"))
			if err != nil || string(body) != want {
				t.Fatal("failure overwrote original manuscript", err)
			}
			progress, err := os.ReadFile(progressPath)
			if err != nil || !bytes.Equal(baselineProgress, progress) {
				t.Fatal("failure advanced progress phase", err)
			}
		})
	}
	t.Run("only-final-window-in-progress", func(t *testing.T) {
		var progress domain.Progress
		if err := json.Unmarshal(baselineProgress, &progress); err != nil {
			t.Fatal(err)
		}
		progress.CompletedChapters = []int{10, 11, 12}
		if err := st.Progress.Save(&progress); err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := os.WriteFile(progressPath, baselineProgress, 0o644); err != nil {
				t.Error(err)
			}
		}()
		if err := runPipelineStage("complete-book", opts, pipelineFlags{}, &domain.PipelineState{}, nil); err == nil {
			t.Fatal("only final window impersonated whole book")
		}
		after, err := st.Progress.Load()
		if err != nil || after.Phase != domain.PhaseWriting {
			t.Fatal("partial completion changed phase", err)
		}
	})
	// A prior atomic export followed by a crash before MarkComplete is an
	// ordinary recovery, not permission to rewrite the manuscript or source.
	if err := runPipelineStage("complete-book", opts, pipelineFlags{}, &domain.PipelineState{}, nil); err != nil {
		t.Fatal("could not close verified pre-existing manuscript", err)
	}
}

func TestCompleteBookWriteFailureDoesNotComplete(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses filesystem write permissions")
	}
	opts, st := completeBookStageFixture(t)
	if err := os.Chmod(st.Dir(), 0o500); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(st.Dir(), 0o755) }()
	err := runPipelineStage("complete-book", opts, pipelineFlags{}, &domain.PipelineState{}, nil)
	if err == nil || !strings.Contains(err.Error(), "write verified whole-book manuscript") {
		t.Fatalf("did not exercise actual atomic writer failure: %v", err)
	}
	progress, err := st.Progress.Load()
	if err != nil || progress.Phase != domain.PhaseWriting {
		t.Fatal("failed write marked book complete", err)
	}
	if _, err := os.Stat(filepath.Join(st.Dir(), "正文.md")); !os.IsNotExist(err) {
		t.Fatal("failed write published manuscript", err)
	}
}
