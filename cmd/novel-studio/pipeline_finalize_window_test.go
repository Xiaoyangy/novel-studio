package main

import (
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

func pipelineFinalizeWindowTestFixture(t *testing.T, wholeBook bool) (*store.Store, *domain.PlanningGenerationV2, *domain.PlanningGenerationV2) {
	t.Helper()
	st := pipelineWindowCompletionTestStore(t)
	if wholeBook {
		if err := st.Progress.SetTotalChapters(6); err != nil {
			t.Fatal(err)
		}
		volumes, err := st.Outline.LoadLayeredOutline()
		if err != nil {
			t.Fatal(err)
		}
		volumes[0].Arcs = volumes[0].Arcs[:1]
		if err := st.Outline.SaveLayeredOutline(volumes); err != nil {
			t.Fatal(err)
		}
	}
	volumes, err := st.Outline.LoadLayeredOutline()
	if err != nil {
		t.Fatal(err)
	}
	var flat []domain.OutlineEntry
	for _, volume := range volumes {
		for _, arc := range volume.Arcs {
			flat = append(flat, arc.Chapters...)
		}
	}
	if err := st.Outline.SaveOutline(flat); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.SetLayered(true); err != nil {
		t.Fatal(err)
	}
	if err := st.RunMeta.SetPlanningTier(domain.PlanningTierShort); err != nil {
		t.Fatal(err)
	}
	first := pipelineWindowCompletionTestWindow(t, st, 1, 1, 6, 1, nil, true)
	last := pipelineWindowCompletionTestWindow(t, st, 4, 1, 6, 1, first, true)
	return st, first, last
}

func TestPipelineFinalizeWindowRequiresWholeBookAggregateNotOnlyFinalWindow(t *testing.T) {
	st, first, last := pipelineFinalizeWindowTestFixture(t, true)
	progress, _ := st.Progress.Load()
	meta, _ := st.RunMeta.Load()
	if err := validatePipelineTerminalShortArcProof(st, progress, meta); err == nil {
		t.Fatal("final window alone passed whole-book proof without an aggregate")
	}
	cursor, _ := st.ProjectedV2().LoadRealizationCursor()
	proof, err := completePipelineArcCycle(st, last, cursor)
	if err != nil {
		t.Fatal(err)
	}
	if proof.WindowAggregate == nil || proof.SingleWindow != nil || proof.FirstChapter != 1 || proof.LastChapter != 6 {
		t.Fatal("short finalization did not retain the real whole-book proof kind")
	}
	if err := validatePipelineTerminalShortArcProof(st, progress, meta); err != nil {
		t.Fatalf("two actual three-chapter windows could not prove the six-chapter short book: %v", err)
	}
	if chapters, lastChapter, _, err := validatePipelineFinalizePrerequisites(st, st.Dir(), pipelineFlags{}); err != nil || len(chapters) != 6 || lastChapter != 6 {
		t.Fatalf("whole-book exact-body prerequisites rejected valid aggregate: chapters=%v err=%v", chapters, err)
	}
	t.Run("original-book-word-budget-still-binds", func(t *testing.T) {
		live := outlineAllGateLiveDir(t)
		receipt := writeOutlineAllGateCompleteReceipt(t, live, filepath.Join(t.TempDir(), "budget", "candidate", "output"), outlineAllGateDigest)
		receipt.EstimatedScale = "1-1卷，6-6章，正文1.5万—1.6万字"
		target, err := domain.ResolveBookScaleTarget(receipt.EstimatedScale, 1, 6)
		if err != nil {
			t.Fatal(err)
		}
		receipt.MinChapters, receipt.MaxChapters, receipt.TargetChapters = 6, 6, 6
		receipt.TargetWords, receipt.TargetWordsPerChapter = target.TargetWords, target.TargetWordsPerChapter
		receipt, err = domain.SignOutlineAllExecutionReceipt(receipt)
		if err != nil {
			t.Fatal(err)
		}
		if err := st.SaveOutlineAllExecutionReceipt(receipt); err != nil {
			t.Fatal(err)
		}
		defer func() { _ = os.Remove(filepath.Join(st.Dir(), store.OutlineAllExecutionReceiptPath)) }()
		if _, _, _, err := validatePipelineFinalizePrerequisites(st, st.Dir(), pipelineFlags{}); err == nil || !strings.Contains(err.Error(), "总字数硬门禁") {
			t.Fatalf("aggregate bypassed original whole-book word budget: %v", err)
		}
	})
	for _, mode := range []string{"earlier-acceptance", "earlier-body", "long-tier", "pending-rewrites"} {
		t.Run(mode, func(t *testing.T) {
			switch mode {
			case "earlier-acceptance":
				acceptances, _ := st.ArcCycle().ListChapterAcceptanceReceipts(first.GenerationID)
				path := filepath.Join(st.Dir(), "meta/planning/v3/arc_cycle/acceptances", first.GenerationID, "000001", acceptances[0].ReceiptDigest+".json")
				original, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = os.WriteFile(path, original, 0o644) }()
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			case "earlier-body":
				path := filepath.Join(st.Dir(), "chapters/01.md")
				original, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = os.WriteFile(path, original, 0o644) }()
				if err := os.WriteFile(path, []byte("不能拿旧聚合替改写后的正文通过终审"), 0o644); err != nil {
					t.Fatal(err)
				}
			case "long-tier":
				changed := *meta
				changed.PlanningTier = domain.PlanningTierLong
				if err := validatePipelineTerminalShortArcProof(st, progress, &changed); err == nil {
					t.Fatal("aggregate expanded the explicit short-tier finalization boundary")
				}
				return
			case "pending-rewrites":
				original, err := st.Progress.Load()
				if err != nil {
					t.Fatal(err)
				}
				if err := st.Progress.SetPendingRewritesAndFlow([]int{1}, "全书终审前仍有已标记返工", domain.FlowRewriting); err != nil {
					t.Fatal(err)
				}
				defer func() {
					if err := st.Progress.Save(original); err != nil {
						t.Error(err)
					}
				}()
				if _, _, _, err := validatePipelineFinalizePrerequisites(st, st.Dir(), pipelineFlags{}); err == nil {
					t.Fatal("whole-book aggregate bypassed existing pending-rewrite gate")
				}
				return
			}
			if err := validatePipelineTerminalShortArcProof(st, progress, meta); err == nil {
				t.Fatal("whole-book aggregate ignored drift in an earlier accepted window")
			}
		})
	}
	calls := 0
	if err := pipelineFinalizeConfigured(bootstrap.Config{OutputDir: st.Dir()}, assets.Bundle{}, pipelineFlags{}, func(_ bootstrap.Config, _ assets.Bundle, opts headless.Options) error {
		calls++
		if opts.StopAfterGlobalReviewChapter != 6 {
			t.Fatal("global review did not cover the whole six-chapter short book")
		}
		if err := st.World.SaveReview(acceptedPipelineGlobalReview(6)); err != nil {
			return err
		}
		if _, err := st.Checkpoints.AppendArtifactLatestAcross(domain.ChapterScope(6), "review", "reviews/06-global.json", "review", "commit"); err != nil {
			return err
		}
		manuscript, err := buildPipelineMergedManuscript(st, []int{1, 2, 3, 4, 5, 6})
		if err != nil {
			return err
		}
		if err := st.Drafts.SaveMergedManuscript(manuscript); err != nil {
			return err
		}
		return st.Progress.MarkComplete()
	}); err != nil || calls != 1 {
		t.Fatalf("actual aggregate plus accepted whole-book review could not finalize: calls=%d err=%v", calls, err)
	}
	if err := requirePipelineFinalizedShortBook(st.Dir()); err != nil {
		t.Fatalf("published six-chapter finalization could not verify: %v", err)
	}
	if err := pipelineFinalizeConfigured(bootstrap.Config{OutputDir: st.Dir()}, assets.Bundle{}, pipelineFlags{}, func(bootstrap.Config, assets.Bundle, headless.Options) error {
		t.Fatal("recovered finalized aggregate must not call a model again")
		return nil
	}); err != nil {
		t.Fatalf("accepted aggregate finalization was not idempotent: %v", err)
	}
}

func TestPipelineFinalizeWindowRejectsPartialArcAndStillRequiresGlobalReview(t *testing.T) {
	t.Run("six-of-nine-is-not-full-book", func(t *testing.T) {
		st, _, last := pipelineFinalizeWindowTestFixture(t, false)
		cursor, _ := st.ProjectedV2().LoadRealizationCursor()
		if _, err := completePipelineArcCycle(st, last, cursor); err != nil {
			t.Fatal(err)
		}
		progress, _ := st.Progress.Load()
		meta, _ := st.RunMeta.Load()
		if err := validatePipelineTerminalShortArcProof(st, progress, meta); err == nil || !strings.Contains(err.Error(), "真实全书") {
			t.Fatalf("a legitimate partial-arc aggregate pretended to prove the nine-chapter book: %v", err)
		}
	})
	t.Run("aggregate-is-not-global-review", func(t *testing.T) {
		st, _, last := pipelineFinalizeWindowTestFixture(t, true)
		cursor, _ := st.ProjectedV2().LoadRealizationCursor()
		if _, err := completePipelineArcCycle(st, last, cursor); err != nil {
			t.Fatal(err)
		}
		calls := 0
		err := pipelineFinalizeConfigured(bootstrap.Config{OutputDir: st.Dir()}, assets.Bundle{}, pipelineFlags{}, func(_ bootstrap.Config, _ assets.Bundle, opts headless.Options) error {
			calls++
			if opts.StopAfterGlobalReviewChapter != 6 {
				t.Fatal("global review did not cover all six chapters")
			}
			review := acceptedPipelineGlobalReview(6)
			review.Verdict, review.AffectedChapters = "rewrite", []int{1}
			if err := st.World.SaveReview(review); err != nil {
				return err
			}
			_, err := st.Checkpoints.AppendArtifactLatestAcross(domain.ChapterScope(6), "review", "reviews/06-global.json", "review", "commit")
			return err
		})
		if err == nil || calls != 1 || !strings.Contains(err.Error(), "affected_chapters=[1]") {
			t.Fatalf("aggregate bypassed required global review rejection: calls=%d err=%v", calls, err)
		}
		if nonEmptyFile(filepath.Join(st.Dir(), pipelineFinalizationJSON)) || nonEmptyFile(filepath.Join(st.Dir(), "正文.md")) {
			t.Fatal("aggregate or rejected global review fabricated completed finalization")
		}
	})
}
