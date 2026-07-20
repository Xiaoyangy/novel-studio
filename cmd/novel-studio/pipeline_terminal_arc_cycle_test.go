package main

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func TestResetCompletedSealedPipelineCycleIgnoresOrdinaryRenderState(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	state := &domain.PipelineState{Stages: []string{"render"}}
	state.MarkDone("render", domain.PipelineStageEvidence{Stage: "render", Status: "complete"})

	reset, err := resetCompletedSealedPipelineCycle(st.Dir(), state)
	if err != nil || reset {
		t.Fatalf("ordinary render entered sealed completion: reset=%v err=%v", reset, err)
	}
	if !state.Done("render") {
		t.Fatal("ordinary render stage was cleared by sealed completion logic")
	}
}

func TestTerminalArcCycleResetIsIdempotentAndDoesNotOpenSuccessor(t *testing.T) {
	opts, st, identity := projectAllCmdTestInstallThreeChapterCLIProjection(t)
	if err := pipelineSeal(opts, pipelineFlags{}); err != nil {
		t.Fatalf("seal terminal arc fixture: %v", err)
	}

	projected := st.ProjectedV2()
	generation, err := projected.LoadSealedGeneration(identity.Generation.GenerationID)
	if err != nil || generation == nil {
		t.Fatalf("load terminal sealed generation: generation=%+v err=%v", generation, err)
	}
	if generation.LastProjectedChapter != generation.BookHorizonChapter {
		t.Fatalf(
			"fixture is not terminal: arc ends at %d but book ends at %d",
			generation.LastProjectedChapter,
			generation.BookHorizonChapter,
		)
	}
	bundles, err := projected.LoadProjectedChapterBundles(generation.GenerationID)
	if err != nil {
		t.Fatal(err)
	}
	if len(bundles) != generation.ExpectedChapterCount {
		t.Fatalf("terminal fixture bundles=%d want=%d", len(bundles), generation.ExpectedChapterCount)
	}

	var finalOutcome domain.ActualOutcomeReceiptV2
	for _, bundle := range bundles {
		cursor, err := projected.LoadRealizationCursor()
		if err != nil || cursor == nil {
			t.Fatalf("chapter %d load realization cursor: cursor=%+v err=%v", bundle.Chapter, cursor, err)
		}
		planDigest, err := domain.ComputeChapterPlanV2Digest(bundle.ChapterPlan)
		if err != nil {
			t.Fatal(err)
		}
		promotion := domain.PromotionReceiptV2{
			Version:               domain.PromotionReceiptV2Version,
			GenerationID:          generation.GenerationID,
			Chapter:               bundle.Chapter,
			BundleDigest:          bundle.BundleDigest,
			ActualPreStateRoot:    bundle.ProjectedPreStateRoot,
			ProjectedPreStateRoot: bundle.ProjectedPreStateRoot,
			RenderDependencyRoot:  projectAllCmdTestDigest(fmt.Sprintf("terminal-render-dependencies-%d", bundle.Chapter)),
			FrozenPlanDigest:      planDigest,
			Mode:                  domain.ExactPromotionModeV2,
			PromotedAt:            fmt.Sprintf("2026-07-17T00:00:0%dZ", bundle.Chapter),
		}
		promotion.ReceiptDigest, err = domain.ComputePromotionReceiptV2Digest(promotion)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := projected.Promote(*cursor, promotion); err != nil {
			t.Fatalf("chapter %d promote fixture: %v", bundle.Chapter, err)
		}

		body := fmt.Sprintf("第%d章\n\n", bundle.Chapter)
		for len([]rune(body)) < 2100 {
			body += fmt.Sprintf(
				"主角完成第%d次验证，逐项核对票据、时限与对方回应，并让每一步选择留下可复核的行动后果。\n",
				bundle.Chapter,
			)
		}
		body = string([]rune(body)[:2100])
		chapterRel := fmt.Sprintf("chapters/%02d.md", bundle.Chapter)
		projectAllCmdTestWriteFile(t, filepath.Join(st.Dir(), filepath.FromSlash(chapterRel)), body)
		bodySHA, err := pipelineRequiredFileSHA(st.Dir(), chapterRel)
		if err != nil {
			t.Fatal(err)
		}
		commit, err := st.Checkpoints.Append(
			domain.ChapterScope(bundle.Chapter),
			"commit",
			chapterRel,
			bodySHA,
		)
		if err != nil {
			t.Fatal(err)
		}
		if err := st.Progress.MarkChapterComplete(bundle.Chapter, len([]rune(body)), "", ""); err != nil {
			t.Fatal(err)
		}
		progress, err := st.Progress.Load()
		if err != nil || progress == nil {
			t.Fatalf("chapter %d load progress: progress=%+v err=%v", bundle.Chapter, progress, err)
		}
		actualCanonRoot, err := pipelineProjectAllLiveCanonRoot(st.Dir(), progress)
		if err != nil {
			t.Fatal(err)
		}
		outcome := domain.ActualOutcomeReceiptV2{
			Version:                domain.ActualOutcomeReceiptV2Version,
			GenerationID:           generation.GenerationID,
			Chapter:                bundle.Chapter,
			PromotionReceiptDigest: promotion.ReceiptDigest,
			ChapterBodySHA256:      bodySHA,
			CommitCheckpointSeq:    commit.Seq,
			ActualDelta:            bundle.ProjectedDelta,
			ActualPreStateRoot:     bundle.ProjectedPreStateRoot,
			ActualPostStateRoot:    bundle.ProjectedPostStateRoot,
			ActualCanonRoot:        actualCanonRoot,
			ProjectedPostStateRoot: bundle.ProjectedPostStateRoot,
			ObligationsSatisfied:   append([]string(nil), bundle.ObligationsConsumed...),
			ProjectionMatch:        true,
			AcceptedAt:             fmt.Sprintf("2026-07-17T00:01:0%dZ", bundle.Chapter),
		}
		outcome.ReceiptDigest, err = domain.ComputeActualOutcomeReceiptV2Digest(outcome)
		if err != nil {
			t.Fatal(err)
		}
		promotedCursor, err := projected.LoadRealizationCursor()
		if err != nil || promotedCursor == nil {
			t.Fatalf("chapter %d load promoted cursor: cursor=%+v err=%v", bundle.Chapter, promotedCursor, err)
		}
		if _, err := projected.AcceptOutcome(*promotedCursor, outcome); err != nil {
			t.Fatalf("chapter %d accept outcome fixture: %v", bundle.Chapter, err)
		}
		mustWriteCurrentReviewArtifacts(t, st.Dir(), bundle.Chapter)
		if _, err := savePipelineChapterAcceptance(
			st.Dir(),
			st,
			generation,
			bundle.Chapter,
			bodySHA,
			&outcome,
		); err != nil {
			t.Fatalf("chapter %d save exact-body acceptance: %v", bundle.Chapter, err)
		}
		finalOutcome = outcome
	}
	if err := st.Progress.MarkComplete(); err != nil {
		t.Fatal(err)
	}
	if _, err := writePipelinePlanningJSON(
		filepath.Join(st.Dir(), pipelineRenderReceiptPath),
		pipelineRenderReceipt{
			Version:                pipelinePlanningSchema,
			Chapter:                generation.LastProjectedChapter,
			PlanningGenerationID:   generation.GenerationID,
			OutcomeReceiptDigest:   finalOutcome.ReceiptDigest,
			ProjectedBundleDigest:  bundles[len(bundles)-1].BundleDigest,
			PromotionReceiptDigest: finalOutcome.PromotionReceiptDigest,
		},
	); err != nil {
		t.Fatal(err)
	}

	// A terminal convergence successor is rendered in a standalone `render`
	// invocation after `plan --restart`; there is intentionally no completed
	// promote stage in that new pipeline state.  Missing exact-body acceptance
	// must still fail closed, then restoring it must publish exactly one
	// idempotent arc completion receipt.
	renderOnlyState := &domain.PipelineState{Stages: []string{"render"}}
	renderOnlyState.MarkDone("render", domain.PipelineStageEvidence{Stage: "render", Status: "complete"})
	acceptances, err := st.ArcCycle().ListChapterAcceptanceReceipts(generation.GenerationID)
	if err != nil || len(acceptances) != generation.ExpectedChapterCount {
		t.Fatalf("load terminal acceptances: receipts=%+v err=%v", acceptances, err)
	}
	lastAcceptance := acceptances[len(acceptances)-1]
	lastAcceptancePath := filepath.Join(
		st.Dir(),
		"meta", "planning", "v3", "arc_cycle", "acceptances",
		generation.GenerationID,
		fmt.Sprintf("%06d", lastAcceptance.Chapter),
		lastAcceptance.ReceiptDigest+".json",
	)
	if err := os.Remove(lastAcceptancePath); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Dir(lastAcceptancePath)); err != nil {
		t.Fatal(err)
	}
	if reset, err := resetCompletedSealedPipelineCycle(st.Dir(), renderOnlyState); err == nil || reset ||
		!strings.Contains(err.Error(), "逐章审核回执") {
		t.Fatalf("terminal render-only completed without exact acceptance: reset=%v err=%v", reset, err)
	}
	if completions, err := st.ArcCycle().ListArcCompletionReceipts(generation.GenerationID); err != nil || len(completions) != 0 {
		t.Fatalf("missing acceptance published terminal completion: receipts=%+v err=%v", completions, err)
	}
	if _, err := st.ArcCycle().SaveChapterAcceptanceReceipt(lastAcceptance); err != nil {
		t.Fatalf("restore terminal acceptance: %v", err)
	}
	for invocation := 1; invocation <= 2; invocation++ {
		reset, err := resetCompletedSealedPipelineCycle(st.Dir(), renderOnlyState)
		if err != nil || reset {
			t.Fatalf("terminal render-only completion %d: reset=%v err=%v", invocation, reset, err)
		}
		if !renderOnlyState.Done("render") {
			t.Fatal("terminal render-only completion cleared the durable render stage")
		}
		completions, err := st.ArcCycle().ListArcCompletionReceipts(generation.GenerationID)
		if err != nil || len(completions) != 1 {
			t.Fatalf("terminal render-only completion %d published %+v: err=%v", invocation, completions, err)
		}
	}

	state := &domain.PipelineState{Stages: []string{"preplan", "project-all", "seal", "promote", "render"}}
	for _, stage := range state.Stages {
		state.MarkDone(stage, domain.PipelineStageEvidence{Stage: stage, Status: "complete"})
	}
	wantCompleted := append([]string(nil), state.Completed...)
	beforeCursor, err := projected.LoadRealizationCursor()
	if err != nil || beforeCursor == nil {
		t.Fatalf("load terminal cursor: cursor=%+v err=%v", beforeCursor, err)
	}
	beforeActive, err := projected.LoadActiveGeneration()
	if err != nil || beforeActive == nil {
		t.Fatalf("load terminal active generation: active=%+v err=%v", beforeActive, err)
	}

	resetCalls := []struct {
		name string
		call func() (bool, error)
	}{
		{name: "sealed-first", call: func() (bool, error) { return resetCompletedSealedPipelineCycle(st.Dir(), state) }},
		{name: "sealed-repeat", call: func() (bool, error) { return resetCompletedSealedPipelineCycle(st.Dir(), state) }},
		{name: "split-first", call: func() (bool, error) { return resetCompletedSplitPipelineCycle(st.Dir(), state) }},
		{name: "split-repeat", call: func() (bool, error) { return resetCompletedSplitPipelineCycle(st.Dir(), state) }},
	}
	for _, tc := range resetCalls {
		t.Run(tc.name, func(t *testing.T) {
			reset, err := tc.call()
			if err != nil {
				t.Fatalf("terminal reset check failed: %v", err)
			}
			if reset {
				t.Fatal("terminal arc opened a nonexistent successor cycle")
			}
			if !slices.Equal(state.Completed, wantCompleted) {
				t.Fatalf("terminal invocation cleared completed stages: got=%v want=%v", state.Completed, wantCompleted)
			}
			for _, stage := range state.Stages {
				if !state.Done(stage) || state.Evidence[stage].Status != "complete" {
					t.Fatalf("terminal invocation mutated stage %s: completed=%v evidence=%+v", stage, state.Done(stage), state.Evidence[stage])
				}
			}
		})
	}

	afterCursor, err := projected.LoadRealizationCursor()
	if err != nil || afterCursor == nil {
		t.Fatalf("reload terminal cursor: cursor=%+v err=%v", afterCursor, err)
	}
	afterActive, err := projected.LoadActiveGeneration()
	if err != nil || afterActive == nil {
		t.Fatalf("reload terminal active generation: active=%+v err=%v", afterActive, err)
	}
	if afterCursor.CursorDigest != beforeCursor.CursorDigest ||
		afterActive.RecordDigest != beforeActive.RecordDigest ||
		afterActive.GenerationID != generation.GenerationID {
		t.Fatalf("terminal invocation attempted successor control state: cursor=%+v active=%+v", afterCursor, afterActive)
	}
	completions, err := st.ArcCycle().ListArcCompletionReceipts(generation.GenerationID)
	if err != nil {
		t.Fatal(err)
	}
	if len(completions) != 1 {
		t.Fatalf("repeated terminal checks published %d completion receipts, want exactly one", len(completions))
	}
	if completion, err := requirePipelineArcCompletion(st, generation); err != nil {
		t.Fatalf("terminal completion no longer validates after repeated checks: %v", err)
	} else if completion.ReceiptDigest != completions[0].ReceiptDigest ||
		completion.FinalOutcomeReceiptDigest != finalOutcome.ReceiptDigest {
		t.Fatalf("terminal completion drifted after repeated checks: %+v", completion)
	}

	// Exercise the real persisted pipeline replay path without invoking a model.
	// project-all/seal are already complete and therefore must pass stored digest
	// checks plus their current stage verifiers on every invocation. The reset
	// assertions above cover the terminal promote/render branch that deliberately
	// keeps those stages complete instead of opening a nonexistent successor.
	replayStages := []string{"project-all", "seal"}
	replayFlags := pipelineFlags{Stages: "project-all,seal"}
	cfg, bundle, err := loadCfgBundle(opts)
	if err != nil {
		t.Fatal(err)
	}
	replayState := &domain.PipelineState{
		Stages:      append([]string(nil), replayStages...),
		InputDigest: pipelineRunInputDigest(cfg, bundle),
		RunIdentity: pipelineRunIdentityDigest(replayFlags),
	}
	wantArtifactDigests := make(map[string]map[string]string, len(replayStages))
	for _, stage := range replayStages {
		evidence, verifyErr := verifyPipelineStage(stage, st.Dir(), replayFlags, replayState)
		if verifyErr != nil {
			t.Fatalf("prepare %s replay evidence: %v", stage, verifyErr)
		}
		evidence = stampPipelineArtifactDigests(st.Dir(), evidence)
		replayState.MarkDone(stage, evidence)
		wantArtifactDigests[stage] = maps.Clone(evidence.ArtifactDigests)
	}
	statePath := filepath.Join(st.Dir(), "meta", "pipeline.json")
	if err := savePipelineState(statePath, replayState); err != nil {
		t.Fatal(err)
	}
	for invocation := 1; invocation <= 2; invocation++ {
		if err := runPipelineWithStages(opts, replayFlags, replayStages, "", nil); err != nil {
			t.Fatalf("terminal no-model pipeline replay %d: %v", invocation, err)
		}
	}
	replayedState, err := loadOrInitPipelineState(
		statePath,
		replayStages,
		"",
		pipelineRunInputDigest(cfg, bundle),
		pipelineRunIdentityDigest(replayFlags),
		false,
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, stage := range replayStages {
		if !replayedState.Done(stage) || replayedState.Evidence[stage].Status != "verified" {
			t.Fatalf("terminal replay lost completed %s evidence: %+v", stage, replayedState.Evidence[stage])
		}
		if !maps.Equal(replayedState.Evidence[stage].ArtifactDigests, wantArtifactDigests[stage]) {
			t.Fatalf(
				"terminal replay changed %s artifact digests: got=%v want=%v",
				stage,
				replayedState.Evidence[stage].ArtifactDigests,
				wantArtifactDigests[stage],
			)
		}
	}
	afterReplayCompletions, err := st.ArcCycle().ListArcCompletionReceipts(generation.GenerationID)
	if err != nil {
		t.Fatal(err)
	}
	if len(afterReplayCompletions) != 1 || afterReplayCompletions[0].ReceiptDigest != completions[0].ReceiptDigest {
		t.Fatalf("terminal replay duplicated or changed arc completion: %+v", afterReplayCompletions)
	}
}
