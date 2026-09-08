package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func pipelineWindowCompletionTestStore(t *testing.T) *store.Store {
	t.Helper()
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init("two-window actual acceptance fixture", 9); err != nil {
		t.Fatal(err)
	}
	var arcs []domain.ArcOutline
	for index, span := range []struct{ first, last int }{{1, 6}, {7, 9}} {
		arc := domain.ArcOutline{Index: index + 1, Title: fmt.Sprintf("弧%d", index+1), Goal: "只按实际行动和已接受后果推进"}
		for chapter := span.first; chapter <= span.last; chapter++ {
			arc.Chapters = append(arc.Chapters, domain.OutlineEntry{Chapter: chapter, Title: fmt.Sprintf("章%d", chapter), CoreEvent: "实际选择产生可核对后果"})
		}
		arcs = append(arcs, arc)
	}
	if err := st.Outline.SaveLayeredOutline([]domain.VolumeOutline{{Index: 1, Title: "第一卷", Arcs: arcs}}); err != nil {
		t.Fatal(err)
	}
	return st
}

func pipelineWindowCompletionTestRehearsal(t *testing.T, st *store.Store, source domain.PlanningSourceSnapshotV2, arcFirst, arcLast, arcIndex int, previous *domain.PlanningGenerationV2) (domain.ArcRehearsalInput, domain.ArcRehearsalReport) {
	t.Helper()
	const author = "作者原始规则：未来只能作条件预测，不可冒充已发生事实。"
	projectAllCmdTestWriteFile(t, filepath.Join(st.Dir(), "arc-window-author.txt"), author)
	root, err := domain.ComputeArcRehearsalSourceRootV1(source.FoundationSnapshotRoot, source.RAGSnapshotRoot)
	if err != nil {
		t.Fatal(err)
	}
	world, err := domain.FinalizeWorldPhysicalStateV2(domain.WorldPhysicalStateV2{Resources: []domain.WorldResourceBalanceV2{}, Actors: []domain.CharacterPhysicalStateV2{{AgentID: "ca_observer", Character: "观察者", Location: "A", Resources: []domain.CharacterResourceHoldingV2{}}}})
	if err != nil {
		t.Fatal(err)
	}
	observation, err := domain.FinalizeCharacterObservationPacket(domain.CharacterObservationPacket{
		Version: domain.CharacterObservationV2Version, GenerationID: "pg2_arc_window_rehearsal", Chapter: source.BaseCanonChapter + 1, Round: 1,
		AgentID: "ca_observer", Character: "观察者", CurrentGoal: "观察条件", Location: "A", TimeWindow: "current", Pressure: "需按可见情况决定",
		StimulusDigest: projectAllCmdTestDigest("stimulus"), ResourceViews: []domain.CharacterResourceViewV2{},
	})
	if err != nil {
		t.Fatal(err)
	}
	input := domain.ArcRehearsalInput{ArcID: domain.DeriveArcCycleID(1, arcIndex, arcFirst, arcLast), ArcFirstChapter: arcFirst, ArcLastChapter: arcLast,
		BaseCanonChapter: source.BaseCanonChapter, BaseCanonRoot: source.BaseCanonRoot, SourceRoot: root, WorldState: &world,
		CharacterObservations: []domain.CharacterObservationPacket{observation}, HardContracts: []string{author},
		SourceFiles: map[string]string{"arc-window-author.txt": domain.ComputeArcArtifactSHA256([]byte(author))}}
	if source.BaseCanonChapter >= arcFirst {
		acceptances, err := st.ArcCycle().ListChapterAcceptanceReceipts(previous.GenerationID)
		if err != nil {
			t.Fatal(err)
		}
		input.AcceptedEvidence = map[int]string{}
		for _, acceptance := range acceptances {
			rel := fmt.Sprintf("chapters/%02d.md", acceptance.Chapter)
			body, err := os.ReadFile(filepath.Join(st.Dir(), rel))
			if err != nil {
				t.Fatal(err)
			}
			input.AcceptedEvidence[acceptance.Chapter] = acceptance.ReceiptDigest
			input.AcceptedSummaries = append(input.AcceptedSummaries, domain.ChapterSummary{Chapter: acceptance.Chapter, Summary: string(body)})
			input.SourceFiles[rel] = domain.ComputeArcArtifactSHA256(body)
		}
	}
	body := domain.ArcRehearsalBody{Summary: "未来章节只有条件预期，详细窗口仍需实际推进",
		CharacterConflicts: []domain.ArcRehearsalCharacterConflict{{Character: "观察者", CurrentGoal: "观察条件", Conflicts: []string{"条件可能变化"}, ConditionalChoices: []string{"若条件允许则观察"}}},
		ContractChecks:     []domain.ArcRehearsalContractCheck{{Contract: author, Assessment: "conditional", Conditions: []string{"以实际观察为准"}}},
		MaterialChecks:     []domain.ArcRehearsalMaterialCheck{{Operation: "目视观察", Status: "not_required", Explanation: "本测试操作不读取文书"}}}
	for chapter := arcFirst; chapter <= arcLast; chapter++ {
		input.Outline = append(input.Outline, domain.OutlineEntry{Chapter: chapter, Title: fmt.Sprintf("章%d", chapter), CoreEvent: "条件允许才观察"})
		forecast := domain.ArcRehearsalChapter{Chapter: chapter, ConditionalForecast: "若条件维持则继续观察", Assumptions: []string{"条件尚待确认"}, CausalLinks: []string{"承接已接受后果"}, TimeResourceChecks: []string{"按实际时间裁决"}}
		for _, summary := range input.AcceptedSummaries {
			if summary.Chapter == chapter {
				forecast.ConditionalForecast, forecast.AcceptedSourceDigest = summary.Summary, input.AcceptedEvidence[chapter]
			}
		}
		body.Chapters = append(body.Chapters, forecast)
	}
	input, err = domain.FinalizeArcRehearsalInput(input)
	if err != nil {
		t.Fatal(err)
	}
	call := func(role string) domain.ArcRehearsalCall {
		return domain.ArcRehearsalCall{Role: role, Provider: "offline-test", Model: "fixture", UsageIDs: []string{"usage-" + role}, ToolCallID: "call-" + role, ResponseDigest: projectAllCmdTestDigest(role)}
	}
	draft, err := domain.FinalizeArcRehearsalDraft(input, domain.ArcRehearsalDraft{Body: body, Call: call("architect")})
	if err != nil {
		t.Fatal(err)
	}
	report, err := domain.FinalizeArcRehearsalReport(input, draft, domain.ArcRehearsalReport{Body: body, Call: call("world_arbiter")})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SaveArcRehearsalReport(input, draft, report); err != nil {
		t.Fatal(err)
	}
	return input, report
}

// Each call creates exactly three complete formal chapters. No fixture turns
// a coarse forecast into a sealed bundle or rewrites an already accepted body.
func pipelineWindowCompletionTestWindow(t *testing.T, st *store.Store, first, arcFirst, arcLast, arcIndex int, previous *domain.PlanningGenerationV2, accept bool) *domain.PlanningGenerationV2 {
	t.Helper()
	generation, registry := projectAllCmdTestGenerationAndRegistry(t, 3)
	progress, err := st.Progress.Load()
	if err != nil || progress == nil {
		t.Fatalf("load explicit fixture book horizon: %v", err)
	}
	bookLast := progress.TotalChapters
	generation.ProjectionScope, generation.ScopeID, generation.BookHorizonChapter = domain.PlanningProjectionScopeArcV2, domain.DeriveArcCycleID(1, arcIndex, arcFirst, arcLast), bookLast
	generation.BaseCanonChapter, generation.FirstProjectedChapter, generation.LastProjectedChapter = first-1, first, first+2
	var boundary *store.AcceptedPlanningWindowBoundaryV1
	if previous != nil {
		boundary, err = st.ProjectedV2().LoadAcceptedPlanningWindowBoundaryV1(previous.GenerationID)
		if err != nil {
			t.Fatal(err)
		}
		generation.ParentGenerationID, generation.BaseCanonRoot, generation.BaseStateRoot = previous.GenerationID, boundary.LastOutcome.ActualCanonRoot, boundary.LastOutcome.ActualPostStateRoot
	}
	source := domain.PlanningSourceSnapshotV2{Version: domain.PlanningSourceSnapshotV2Version, BaseCanonChapter: generation.BaseCanonChapter, BaseCanonRoot: generation.BaseCanonRoot,
		BaseStateRoot: generation.BaseStateRoot, StableOutlineRoot: generation.StableOutlineRoot, RandomSeedContractRoot: generation.RandomSeedContractRoot,
		FoundationSnapshotRoot: projectAllCmdTestDigest("window-foundation"), RAGSnapshotRoot: projectAllCmdTestDigest("window-rag"), CapturedAt: generation.CreatedAt}
	input, report := pipelineWindowCompletionTestRehearsal(t, st, source, arcFirst, arcLast, arcIndex, previous)
	generation.DetailWindow = &domain.PlanningDetailWindowV1{Version: domain.PlanningDetailWindowVersionV1, ArcID: generation.ScopeID, ArcFirstChapter: arcFirst, ArcLastChapter: arcLast,
		RehearsalDigest: report.ReportDigest, RehearsalInputDigest: input.InputDigest}
	if first != arcFirst {
		generation.DetailWindow.AcceptedPredecessor, generation.DetailWindow.AcceptedOutcomeDigest = &boundary.Predecessor, boundary.LastOutcome.ReceiptDigest
	}
	generation.PlanningDependencyRoot, _ = domain.PlanningDetailWindowDependencyRootV1(generation.PlanningDependencyRoot, *generation.DetailWindow)
	generation.GenerationID, _ = domain.DerivePlanningGenerationAttemptV2ID(generation.BaseCanonRoot, generation.StableOutlineRoot, generation.PlanningDependencyRoot, generation.RandomSeedContractRoot, generation.AttemptID)
	registry.GenerationID, registry.ProjectionScope, registry.ScopeID, registry.BookHorizonChapter = generation.GenerationID, generation.ProjectionScope, generation.ScopeID, bookLast
	registry.FirstChapter, registry.LastChapter = first, first+2
	registry.RegistryRoot, _ = domain.ComputeObligationRegistryV2Root(registry)
	generation.ObligationRegistryRoot = registry.RegistryRoot
	generation.GenerationDigest, _ = domain.ComputePlanningGenerationV2Digest(generation)
	source.GenerationID, source.PlanningDependencyRoot, source.DetailWindow = generation.GenerationID, generation.PlanningDependencyRoot, generation.DetailWindow
	source.SnapshotDigest, _ = domain.ComputePlanningSourceSnapshotV2Digest(source)
	p := st.ProjectedV2()
	if err := p.CreateBuildingGeneration(generation, source, registry); err != nil {
		t.Fatal(err)
	}
	if previous != nil {
		if err := p.ResetProjectionCursorForRestart(generation.GenerationID); err != nil {
			t.Fatal(err)
		}
	}
	cursor, err := p.InitializeProjectionCursor(generation.GenerationID)
	if err != nil {
		t.Fatal(err)
	}
	var bundles []domain.ProjectedChapterBundle
	current := generation
	prior, preState, _ := pipelineProjectAllTail(current, nil)
	for chapter := first; chapter <= first+2; chapter++ {
		artifacts, outline := projectAllCmdTestArtifacts(t, generation.GenerationID, chapter)
		projectAllCmdTestBindPlanningContext(t, artifacts, current, bundles, registry, chapter)
		bundle, nextRegistry, err := buildPipelineProjectedChapterBundle(current, outline, prior, preState, artifacts, registry)
		if err != nil {
			t.Fatal(err)
		}
		cursor, err = p.ProjectChapterAndAdvance(current.GenerationDigest, current.ChainTailRoot, current.ObligationRegistryRoot, *cursor, bundle, nextRegistry)
		if err != nil {
			t.Fatal(err)
		}
		loaded, err := p.LoadBuildingGeneration(generation.GenerationID)
		if err != nil {
			t.Fatal(err)
		}
		current, registry, prior, preState = *loaded, nextRegistry, bundle.BundleDigest, bundle.ProjectedPostStateRoot
		bundles = append(bundles, bundle)
	}
	if _, err := p.SealGeneration(generation.GenerationID); err != nil {
		t.Fatal(err)
	}
	sealed, err := p.LoadSealedGeneration(generation.GenerationID)
	if err != nil {
		t.Fatal(err)
	}
	links, turns, payoffs, err := pipelineArcNarrativeEvidence(bundles, sealed.DetailWindow.AcceptedPredecessor)
	if err != nil {
		t.Fatal(err)
	}
	manifest := domain.ArcPlanningManifest{Version: domain.ArcPlanningManifestVersion, DetailWindow: sealed.DetailWindow, ArcID: sealed.ScopeID, GenerationID: sealed.GenerationID,
		Volume: 1, Arc: arcIndex, FirstChapter: first, LastChapter: first + 2, BookLastChapter: bookLast, FullOutlineDigest: sealed.StableOutlineRoot,
		ChapterBodyRunes: domain.ArcChapterBodyRuneContract{MinRunes: 2000, MaxRunes: 3300, SourceUserRulesDigest: projectAllCmdTestDigest("author-runes")},
		CausalLinks:      links, Turns: turns, Payoffs: payoffs, CreatedAt: sealed.CreatedAt}
	for _, bundle := range bundles {
		manifest.Chapters = append(manifest.Chapters, domain.ArcChapterPlanningBinding{Chapter: bundle.Chapter, BundleDigest: bundle.BundleDigest, CapacityDigest: pipelineProjectAllDigest(*bundle.ChapterPlan.CausalSimulation.RenderCapacity)})
	}
	manifest.CarriedObligations, _ = domain.ArcWindowCarriedObligationsV1(registry)
	manifest, err = domain.SignArcPlanningManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.ArcCycle().SaveArcPlanningManifest(manifest); err != nil {
		t.Fatal(err)
	}
	if _, _, err := p.ActivateSealedGeneration(sealed.GenerationID, nil); err != nil {
		t.Fatal(err)
	}
	if !accept {
		return sealed
	}
	for _, bundle := range bundles {
		cursor, err := p.LoadRealizationCursor()
		if err != nil {
			t.Fatal(err)
		}
		promotion := projectAllCmdTestPromotion(t, bundle)
		if _, err := p.Promote(*cursor, promotion); err != nil {
			t.Fatal(err)
		}
		body := strings.Repeat("可核对实际行动。", 300)
		body = string([]rune(body)[:2100])
		projectAllCmdTestWriteFile(t, filepath.Join(st.Dir(), fmt.Sprintf("chapters/%02d.md", bundle.Chapter)), body)
		if err := st.Progress.MarkChapterComplete(bundle.Chapter, 2100, "", ""); err != nil {
			t.Fatal(err)
		}
		outcome := projectAllCmdTestOutcome(t, bundle, promotion, bundle.Chapter)
		outcome.ChapterBodySHA256 = domain.ComputeArcChapterBodySHA256([]byte(body))
		progress, _ := st.Progress.Load()
		outcome.ActualCanonRoot, err = pipelineProjectAllLiveCanonRoot(st.Dir(), progress)
		if err != nil {
			t.Fatal(err)
		}
		outcome.ObligationsSatisfied = append([]string(nil), bundle.ObligationsConsumed...)
		outcome.ReceiptDigest, _ = domain.ComputeActualOutcomeReceiptV2Digest(outcome)
		cursor, _ = p.LoadRealizationCursor()
		if _, err := p.AcceptOutcome(*cursor, outcome); err != nil {
			t.Fatal(err)
		}
		mustWriteCurrentReviewArtifacts(t, st.Dir(), bundle.Chapter)
		inspection := inspectCurrentChapterReview(st.Dir(), bundle.Chapter)
		var reviews []domain.ChapterReviewArtifactBinding
		for _, rel := range inspection.Artifacts {
			digest, err := pipelineRequiredFileSHA(st.Dir(), rel)
			if err != nil {
				t.Fatal(err)
			}
			reviews = append(reviews, domain.ChapterReviewArtifactBinding{Path: rel, Digest: digest})
		}
		acceptance, err := domain.SignChapterAcceptanceReceipt(domain.ChapterAcceptanceReceipt{Version: domain.ChapterAcceptanceReceiptLegacyVersion,
			ArcID: manifest.ArcID, ArcManifestDigest: manifest.ManifestDigest, GenerationID: sealed.GenerationID, Chapter: bundle.Chapter,
			ChapterBodySHA256: outcome.ChapterBodySHA256, ChapterBodyRunes: 2100, ReviewArtifacts: reviews, OutcomeReceiptDigest: outcome.ReceiptDigest, AcceptedAt: outcome.AcceptedAt})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := st.ArcCycle().SaveChapterAcceptanceReceipt(acceptance); err != nil {
			t.Fatal(err)
		}
		if _, err := writePipelinePlanningJSON(filepath.Join(st.Dir(), pipelineRenderReceiptPath), pipelineRenderReceipt{Version: pipelinePlanningSchema, Chapter: bundle.Chapter,
			PlanningGenerationID: sealed.GenerationID, OutcomeReceiptDigest: outcome.ReceiptDigest, ProjectedBundleDigest: bundle.BundleDigest, PromotionReceiptDigest: promotion.ReceiptDigest}); err != nil {
			t.Fatal(err)
		}
	}
	return sealed
}

func pipelineWindowCompletionTestDoneState() *domain.PipelineState {
	state := &domain.PipelineState{Stages: []string{"preplan", "rehearse-arc", "project-all", "seal", "promote", "render"}}
	for _, stage := range state.Stages {
		state.MarkDone(stage, domain.PipelineStageEvidence{Stage: stage, Status: "complete"})
	}
	return state
}

func TestPipelineWindowCompletionAggregatesSixChaptersThenUnlocksNextArc(t *testing.T) {
	st := pipelineWindowCompletionTestStore(t)
	first := pipelineWindowCompletionTestWindow(t, st, 1, 1, 6, 1, nil, true)
	state := pipelineWindowCompletionTestDoneState()
	if reset, err := resetCompletedSealedPipelineCycle(st.Dir(), state); err != nil || !reset || state.Evidence["render"].Status != "next_window" {
		t.Fatalf("first three accepted chapters did not stop at next_window: reset=%v err=%v", reset, err)
	}
	if _, err := requirePipelineArcCompletion(st, first); err == nil {
		t.Fatal("first detail window pretended to complete the entire arc")
	}
	last := pipelineWindowCompletionTestWindow(t, st, 4, 1, 6, 1, first, true)
	if err := requirePipelinePreviousArcFullyRealized(st, 6); err == nil {
		t.Fatal("a completed final window unlocked the next arc without an aggregate")
	}
	state = pipelineWindowCompletionTestDoneState()
	if reset, err := resetCompletedSealedPipelineCycle(st.Dir(), state); err != nil || !reset || state.Evidence["render"].Status != "next_arc" {
		t.Fatalf("complete six-chapter aggregate did not unlock next_arc: reset=%v err=%v", reset, err)
	}
	proof, err := requirePipelineArcCompletion(st, last)
	if err != nil {
		t.Fatal(err)
	}
	if proof.SingleWindow != nil || proof.WindowAggregate == nil || proof.Kind != domain.ArcWindowAggregateCompletionVersionV1 || proof.FirstChapter != 1 || proof.LastChapter != 6 || len(proof.Acceptances) != 6 {
		t.Fatalf("CLI laundered aggregate into a v1 single-window receipt: %+v", proof)
	}
	if !strings.Contains(state.Evidence["render"].Message, proof.Kind+" "+proof.ReceiptDigest) {
		t.Fatal("next_arc diagnostic lost the actual proof kind/digest")
	}
	raw, _ := json.Marshal(proof)
	if strings.Contains(string(raw), "single_window") || !strings.Contains(string(raw), "window_aggregate") || !strings.Contains(string(raw), proof.ReceiptDigest) {
		t.Fatal("serialized CLI view lost the original typed aggregate")
	}
	cursor, _ := st.ProjectedV2().LoadRealizationCursor()
	again, err := completePipelineArcCycle(store.NewStore(st.Dir()), last, cursor)
	if err != nil || !reflect.DeepEqual(proof, again) {
		t.Fatalf("recovery did not reuse the same original aggregate: %v", err)
	}
	if err := requirePipelinePreviousArcFullyRealized(st, 6); err != nil {
		t.Fatalf("verified whole arc did not unlock successor: %v", err)
	}
	scope, err := requirePipelineArcStart(st, 6)
	if err != nil || scope.FirstChapter != 7 || scope.LastChapter != 9 {
		t.Fatalf("next arc boundary is not exactly chapters 7..9: %+v %v", scope, err)
	}
	next := pipelineWindowCompletionTestWindow(t, st, 7, 7, 9, 2, last, false)
	nextCursor, _ := st.ProjectedV2().LoadRealizationCursor()
	canonRoot, err := pipelineProjectAllExpectedCanonRoot(st.ProjectedV2(), nextCursor, next)
	if err != nil || canonRoot != next.BaseCanonRoot || next.BaseStateRoot != proof.FinalActualPostStateRoot || nextCursor.LastOutcomeReceiptDigest != proof.FinalOutcomeReceiptDigest {
		t.Fatalf("successor generation lost actual aggregate boundary: %v", err)
	}
}

func TestPipelineWindowCompletionRejectsEarlierEvidenceDriftAndFalseFinalRoot(t *testing.T) {
	st := pipelineWindowCompletionTestStore(t)
	first := pipelineWindowCompletionTestWindow(t, st, 1, 1, 6, 1, nil, true)
	last := pipelineWindowCompletionTestWindow(t, st, 4, 1, 6, 1, first, true)
	cursor, _ := st.ProjectedV2().LoadRealizationCursor()
	proof, err := completePipelineArcCycle(st, last, cursor)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"missing-first-acceptance", "first-review", "first-body", "false-root", "ambiguous-proof"} {
		t.Run(mode, func(t *testing.T) {
			var path string
			var replacement []byte
			missing := false
			switch mode {
			case "missing-first-acceptance":
				acceptances, _ := st.ArcCycle().ListChapterAcceptanceReceipts(first.GenerationID)
				path = filepath.Join(st.Dir(), "meta/planning/v3/arc_cycle/acceptances", first.GenerationID, "000001", acceptances[0].ReceiptDigest+".json")
				missing = true
			case "first-review":
				path, replacement = filepath.Join(st.Dir(), "reviews/01.json"), []byte(`{"chapter":1,"verdict":"reject"}`)
			case "first-body":
				path, replacement = filepath.Join(st.Dir(), "chapters/01.md"), []byte("不得悄悄改写已接受正文")
			case "false-root":
				originalPath := filepath.Join(st.Dir(), "meta/planning/v3/arc_cycle/window_aggregates", last.GenerationID, proof.ReceiptDigest+".json")
				backup := filepath.Join(t.TempDir(), "original-aggregate.json")
				fake := *proof.WindowAggregate
				fake.FinalActualPostStateRoot = projectAllCmdTestDigest("forged-actual-root")
				fake.ReceiptDigest, _ = domain.ComputeArcWindowAggregateCompletionDigestV1(fake)
				replacement, _ = json.Marshal(fake)
				path = filepath.Join(filepath.Dir(originalPath), fake.ReceiptDigest+".json")
				if err := os.Rename(originalPath, backup); err != nil {
					t.Fatal(err)
				}
				defer func() { _ = os.Rename(backup, originalPath); _ = os.Remove(path) }()
				if err := os.WriteFile(path, replacement, 0o644); err != nil {
					t.Fatal(err)
				}
			case "ambiguous-proof":
				second, err := st.CompleteWindowedArcV1([]string{first.GenerationID, last.GenerationID}, "2026-07-18T00:00:00Z")
				if err != nil {
					t.Fatal(err)
				}
				path = filepath.Join(st.Dir(), "meta/planning/v3/arc_cycle/window_aggregates", last.GenerationID, second.ReceiptDigest+".json")
				defer func() { _ = os.Remove(path) }()
			}
			if mode != "ambiguous-proof" && mode != "false-root" {
				original, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = os.WriteFile(path, original, 0o644) }()
				if missing {
					if err := os.Remove(path); err != nil {
						t.Fatal(err)
					}
				} else if err := os.WriteFile(path, replacement, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if err := requirePipelinePreviousArcFullyRealized(st, 6); err == nil {
				t.Fatal("next arc unlocked without one intact whole-arc proof")
			}
			if _, err := completePipelineArcCycle(st, last, cursor); err == nil {
				t.Fatal("completion recovery repaired or ignored invalid original evidence")
			}
		})
	}
}
