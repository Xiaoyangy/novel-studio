package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

type planningWindowStoreFixture struct {
	st         *Store
	generation domain.PlanningGenerationV2
	source     domain.PlanningSourceSnapshotV2
	registry   domain.ObligationRegistryV2
	report     domain.ArcRehearsalReport
	input      domain.ArcRehearsalInput
}

func planningWindowStoreSeedRehearsal(t *testing.T, st *Store, source domain.PlanningSourceSnapshotV2, arcFirst, arcLast int, ready bool) (domain.ArcRehearsalInput, domain.ArcRehearsalReport) {
	t.Helper()
	const author = "explicit original author source"
	if err := os.WriteFile(filepath.Join(st.Dir(), "characters.json"), []byte(author), 0o644); err != nil {
		t.Fatal(err)
	}
	root, err := domain.ComputeArcRehearsalSourceRootV1(source.FoundationSnapshotRoot, source.RAGSnapshotRoot)
	if err != nil {
		t.Fatal(err)
	}
	world, err := domain.FinalizeWorldPhysicalStateV2(domain.WorldPhysicalStateV2{
		Resources: []domain.WorldResourceBalanceV2{},
		Actors:    []domain.CharacterPhysicalStateV2{{AgentID: "ca_observer", Character: "观察者", Location: "A", Resources: []domain.CharacterResourceHoldingV2{}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	observation, err := domain.FinalizeCharacterObservationPacket(domain.CharacterObservationPacket{
		Version: domain.CharacterObservationV2Version, GenerationID: "pg2_rehearsal_fixture", Chapter: source.BaseCanonChapter + 1, Round: 1,
		AgentID: "ca_observer", Character: "观察者", CurrentGoal: "观察天气", Location: "A", TimeWindow: "current", Pressure: "等待实际观察",
		StimulusDigest: projectedStoreV2Digest("rehearsal-stimulus"), ResourceViews: []domain.CharacterResourceViewV2{},
	})
	if err != nil {
		t.Fatal(err)
	}
	input := domain.ArcRehearsalInput{
		ArcID: domain.DeriveArcCycleID(1, 1, arcFirst, arcLast), ArcFirstChapter: arcFirst, ArcLastChapter: arcLast,
		BaseCanonChapter: source.BaseCanonChapter, BaseCanonRoot: source.BaseCanonRoot, SourceRoot: root,
		SourceFiles:           map[string]string{"characters.json": domain.ComputeArcArtifactSHA256([]byte(author))},
		CharacterObservations: []domain.CharacterObservationPacket{observation},
		WorldState:            &world, HardContracts: []string{"按实际可见后果回应本弧目标"},
	}
	if source.BaseCanonChapter >= arcFirst {
		acceptances, err := st.ArcCycle().ListChapterAcceptanceReceipts(source.GenerationID)
		if err != nil {
			t.Fatal(err)
		}
		input.AcceptedEvidence = map[int]string{}
		for _, acceptance := range acceptances {
			path := fmt.Sprintf("chapters/%02d.md", acceptance.Chapter)
			raw, err := os.ReadFile(filepath.Join(st.Dir(), path))
			if err != nil {
				t.Fatal(err)
			}
			input.AcceptedEvidence[acceptance.Chapter] = acceptance.ReceiptDigest
			input.AcceptedSummaries = append(input.AcceptedSummaries, domain.ChapterSummary{Chapter: acceptance.Chapter, Summary: string(raw)})
			input.SourceFiles[path] = domain.ComputeArcArtifactSHA256(raw)
		}
	}
	body := domain.ArcRehearsalBody{
		Summary:            "整弧条件性预演，尚无角色实际执行结果",
		CharacterConflicts: []domain.ArcRehearsalCharacterConflict{{Character: "观察者", CurrentGoal: "观察天气", Conflicts: []string{"观察条件尚未确认"}, ConditionalChoices: []string{"若条件允许则继续观察"}}},
		ContractChecks:     []domain.ArcRehearsalContractCheck{{Contract: input.HardContracts[0], Assessment: "conditional", Conditions: []string{"以真实观察结果为准"}}},
		MaterialChecks:     []domain.ArcRehearsalMaterialCheck{{Operation: "目视天气", Status: "not_required", Explanation: "此fixture操作不需要读取文书"}},
	}
	for chapter := arcFirst; chapter <= arcLast; chapter++ {
		input.Outline = append(input.Outline, domain.OutlineEntry{Chapter: chapter, Title: fmt.Sprintf("章%d", chapter), CoreEvent: "按实际条件回应后果"})
		body.Chapters = append(body.Chapters, domain.ArcRehearsalChapter{Chapter: chapter, ConditionalForecast: "如果既有条件维持则继续观察", Assumptions: []string{"条件尚未执行确认"}, CausalLinks: []string{"承接上一章实际结果"}, TimeResourceChecks: []string{"时间与资源需实际裁决"}})
		for _, summary := range input.AcceptedSummaries {
			if summary.Chapter == chapter {
				body.Chapters[len(body.Chapters)-1].ConditionalForecast = summary.Summary
				body.Chapters[len(body.Chapters)-1].AcceptedSourceDigest = input.AcceptedEvidence[chapter]
			}
		}
	}
	input, err = domain.FinalizeArcRehearsalInput(input)
	if err != nil {
		t.Fatal(err)
	}
	call := func(role string) domain.ArcRehearsalCall {
		return domain.ArcRehearsalCall{Role: role, Provider: "test", Model: "offline-fixture", UsageIDs: []string{"test-" + role}, ToolCallID: "test-call-" + role, ResponseDigest: projectedStoreV2Digest("test-response-" + role)}
	}
	draft, err := domain.FinalizeArcRehearsalDraft(input, domain.ArcRehearsalDraft{Body: body, Call: call("architect")})
	if err != nil {
		t.Fatal(err)
	}
	if !ready {
		body.MaterialChecks = append([]domain.ArcRehearsalMaterialCheck(nil), body.MaterialChecks...)
		body.MaterialChecks[0].Status = "unclear"
		body.MaterialChecks[0].Explanation = "存在阻断的来源缺口"
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

func planningWindowStoreRebind(t *testing.T, f *planningWindowStoreFixture) {
	t.Helper()
	g := &f.generation
	var err error
	g.PlanningDependencyRoot, err = domain.PlanningDetailWindowDependencyRootV1(projectedStoreV2Digest("window-base-dependencies"), *g.DetailWindow)
	if err != nil {
		t.Fatal(err)
	}
	g.GenerationID, err = domain.DerivePlanningGenerationAttemptV2ID(g.BaseCanonRoot, g.StableOutlineRoot, g.PlanningDependencyRoot, g.RandomSeedContractRoot, g.AttemptID)
	if err != nil {
		t.Fatal(err)
	}
	f.registry.GenerationID, f.registry.ScopeID = g.GenerationID, g.ScopeID
	f.registry.FirstChapter, f.registry.LastChapter = g.FirstProjectedChapter, g.LastProjectedChapter
	f.registry.RegistryRoot, _ = domain.ComputeObligationRegistryV2Root(f.registry)
	g.ObligationRegistryRoot = f.registry.RegistryRoot
	g.GenerationDigest, _ = domain.ComputePlanningGenerationV2Digest(*g)
	f.source.GenerationID, f.source.PlanningDependencyRoot, f.source.DetailWindow = g.GenerationID, g.PlanningDependencyRoot, g.DetailWindow
	f.source.BaseCanonChapter, f.source.BaseCanonRoot, f.source.BaseStateRoot = g.BaseCanonChapter, g.BaseCanonRoot, g.BaseStateRoot
	f.source.SnapshotDigest, _ = domain.ComputePlanningSourceSnapshotV2Digest(f.source)
}

func newPlanningWindowStoreFixture(t *testing.T, arcLast int, ready bool) planningWindowStoreFixture {
	t.Helper()
	f := planningWindowStoreFixture{st: NewStore(t.TempDir())}
	f.generation, f.source, f.registry, _ = projectedStoreV2ArcFixture(t, domain.DeriveArcCycleID(1, 1, 1, arcLast), 3, arcLast)
	f.generation.BaseCanonChapter, f.source.BaseCanonChapter = 0, 0
	f.generation.FirstProjectedChapter, f.generation.LastProjectedChapter = 1, 3
	f.input, f.report = planningWindowStoreSeedRehearsal(t, f.st, f.source, 1, arcLast, ready)
	f.generation.DetailWindow = &domain.PlanningDetailWindowV1{
		Version: domain.PlanningDetailWindowVersionV1, ArcID: f.generation.ScopeID, ArcFirstChapter: 1, ArcLastChapter: arcLast,
		RehearsalDigest: f.report.ReportDigest, RehearsalInputDigest: f.input.InputDigest,
	}
	planningWindowStoreRebind(t, &f)
	return f
}

func TestPlanningDetailWindowStoreRequiresActualReadyRehearsalAndFreshSource(t *testing.T) {
	for _, mode := range []string{"ready", "missing-report", "blocked-report", "foreign-input", "source-root", "source-bytes", "source-binding"} {
		t.Run(mode, func(t *testing.T) {
			f := newPlanningWindowStoreFixture(t, 8, mode != "blocked-report")
			switch mode {
			case "missing-report":
				f.generation.DetailWindow.RehearsalDigest = projectedStoreV2Digest("not-persisted")
			case "foreign-input":
				f.generation.DetailWindow.RehearsalInputDigest = projectedStoreV2Digest("foreign-input")
			case "source-root":
				f.source.FoundationSnapshotRoot = projectedStoreV2Digest("changed-foundation")
			case "source-bytes":
				if err := os.WriteFile(filepath.Join(f.st.Dir(), "characters.json"), []byte("changed author source"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			planningWindowStoreRebind(t, &f)
			if mode == "source-binding" {
				f.source.DetailWindow = nil
				f.source.SnapshotDigest, _ = domain.ComputePlanningSourceSnapshotV2Digest(f.source)
			}
			err := f.st.ProjectedV2().CreateBuildingGeneration(f.generation, f.source, f.registry)
			if mode == "ready" {
				if err != nil {
					t.Fatal(err)
				}
				loaded, err := NewStore(f.st.Dir()).ProjectedV2().LoadBuildingGeneration(f.generation.GenerationID)
				if err != nil || loaded == nil || !reflect.DeepEqual(*loaded, f.generation) {
					t.Fatalf("independent Store reload lost the original window binding: %v", err)
				}
			} else {
				if err == nil {
					t.Fatalf("invalid %s rehearsal bound a formal generation", mode)
				}
				if _, err := os.Stat(filepath.Join(f.st.Dir(), projectedBuildingGenerationPath(f.generation.GenerationID))); !os.IsNotExist(err) {
					t.Fatal("rejected rehearsal published a partial formal generation")
				}
			}
		})
	}
}

func planningWindowStoreBundles(t *testing.T, f planningWindowStoreFixture) []domain.ProjectedChapterBundle {
	t.Helper()
	previous, err := domain.DeriveProjectedChainGenesisV2(f.generation)
	if err != nil {
		t.Fatal(err)
	}
	state := f.generation.BaseStateRoot
	var bundles []domain.ProjectedChapterBundle
	for chapter := f.generation.FirstProjectedChapter; chapter <= f.generation.LastProjectedChapter; chapter++ {
		context, err := domain.DeriveProjectedPlanningContextV2(f.generation, bundles, f.registry, chapter)
		if err != nil {
			t.Fatal(err)
		}
		bundle := projectedStoreV2Bundle(t, f.generation.GenerationID, chapter, previous, state, context.ContextDigest, context.PredecessorContract, false)
		capacity := domain.ChapterRenderCapacity{TotalTargetRunes: 900, AntiPaddingPolicy: "仅用实际因果与可见选择承载正文"}
		for i := 0; i < 3; i++ {
			capacity.SceneUnits = append(capacity.SceneUnits, domain.ChapterRenderSceneUnit{SceneID: fmt.Sprintf("scene-%d", i), TargetRunes: 300,
				POVObjective: "观察实际情况", ActiveOpposition: "条件尚未明确", Turn: "实际结果改变原判断", ExitConsequence: "按真实结果继续选择",
				ConcreteActionBeats: []string{"人物站到可以观察的位置", "人物确认眼前已经发生的变化", "人物据此调整自己下一步选择"}})
		}
		bundle.ChapterPlan.CausalSimulation.RenderCapacity = &capacity
		projectedStoreV2RebindRenderContext(t, &bundle)
		bundle.BundleDigest, _ = domain.ComputeProjectedChapterBundleDigest(bundle)
		if err := domain.ValidateProjectedChapterBundle(bundle); err != nil {
			t.Fatal(err)
		}
		bundles = append(bundles, bundle)
		previous, state = bundle.BundleDigest, bundle.ProjectedPostStateRoot
	}
	return bundles
}

func planningWindowStorePublish(t *testing.T, f planningWindowStoreFixture, bundles []domain.ProjectedChapterBundle) {
	t.Helper()
	p := f.st.ProjectedV2()
	if err := p.CreateBuildingGeneration(f.generation, f.source, f.registry); err != nil {
		t.Fatal(err)
	}
	cursor, err := p.InitializeProjectionCursor(f.generation.GenerationID)
	if err != nil {
		t.Fatal(err)
	}
	generation := f.generation
	for _, bundle := range bundles {
		cursor, err = p.ProjectBundleAndAdvance(generation.GenerationDigest, generation.ChainTailRoot, *cursor, bundle)
		if err != nil {
			t.Fatal(err)
		}
		loaded, err := p.LoadBuildingGeneration(generation.GenerationID)
		if err != nil {
			t.Fatal(err)
		}
		generation = *loaded
	}
}

func TestPlanningDetailWindowStoreSealsOnlyThreeFormalBundlesAndRetainsHistoricalRehearsal(t *testing.T) {
	f := newPlanningWindowStoreFixture(t, 8, true)
	bundles := planningWindowStoreBundles(t, f)
	planningWindowStorePublish(t, f, bundles[:2])
	p := f.st.ProjectedV2()
	if _, err := p.SealGeneration(f.generation.GenerationID); err == nil || !strings.Contains(err.Error(), "projected 2 of 3") {
		t.Fatalf("coarse future chapter replaced missing third formal bundle: %v", err)
	}
	recovered, cursor, err := NewStore(f.st.Dir()).ProjectedV2().RecoverBuildingProjection(f.generation.GenerationID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.ProjectBundleAndAdvance(recovered.GenerationDigest, recovered.ChainTailRoot, *cursor, bundles[2]); err != nil {
		t.Fatal(err)
	}
	if _, err := p.SealGeneration(f.generation.GenerationID); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.st.Dir(), "characters.json"), []byte("later accepted source"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := NewStore(f.st.Dir()).ProjectedV2().LoadSealedGeneration(f.generation.GenerationID); err != nil {
		t.Fatalf("ordinary later source evolution invalidated immutable sealed history: %v", err)
	}
	path, err := arcRehearsalPath("reports", f.report.ReportDigest)
	if err != nil {
		t.Fatal(err)
	}
	f.report.Body.Summary = "tampered original rehearsal"
	raw, _ := json.Marshal(f.report)
	if err := os.WriteFile(filepath.Join(f.st.Dir(), path), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := NewStore(f.st.Dir()).ProjectedV2().LoadSealedGeneration(f.generation.GenerationID); err == nil {
		t.Fatal("sealed window ignored tampering of its original rehearsal report")
	}
}

func planningWindowStoreAcceptThreeChapters(t *testing.T, f planningWindowStoreFixture) (domain.ArcPlanningManifest, []domain.ChapterAcceptanceReceipt) {
	t.Helper()
	bundles := planningWindowStoreBundles(t, f)
	planningWindowStorePublish(t, f, bundles)
	p := f.st.ProjectedV2()
	if _, err := p.SealGeneration(f.generation.GenerationID); err != nil {
		t.Fatal(err)
	}
	manifest := arcCycleStoreTestManifest(t, f.generation.GenerationID)
	manifest.ArcID, manifest.BookLastChapter, manifest.FullOutlineDigest = f.generation.ScopeID, f.generation.BookHorizonChapter, f.generation.StableOutlineRoot
	manifest.DetailWindow = f.generation.DetailWindow
	manifest.CarriedObligations = nil
	for i, bundle := range bundles {
		capacity, _ := json.Marshal(bundle.ChapterPlan.CausalSimulation.RenderCapacity)
		manifest.Chapters[i].BundleDigest = bundle.BundleDigest
		manifest.Chapters[i].CapacityDigest = domain.ComputeArcArtifactSHA256(capacity)
	}
	var err error
	manifest, err = domain.SignArcPlanningManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.st.ArcCycle().SaveArcPlanningManifest(manifest); err != nil {
		t.Fatal(err)
	}
	if _, _, err := p.ActivateSealedGeneration(f.generation.GenerationID, nil); err != nil {
		t.Fatal(err)
	}
	var acceptances []domain.ChapterAcceptanceReceipt
	for _, bundle := range bundles {
		evidence := arcCycleStoreWriteEvidence(t, f.st.Dir(), bundle.Chapter)
		promotion := projectedStoreV2Promotion(t, bundle)
		if _, err := p.SavePromotionReceipt(promotion); err != nil {
			t.Fatal(err)
		}
		outcome := projectedStoreV2Outcome(t, bundle, promotion)
		outcome.ChapterBodySHA256 = domain.ComputeArcChapterBodySHA256(evidence.body)
		outcome.ReceiptDigest, _ = domain.ComputeActualOutcomeReceiptV2Digest(outcome)
		if _, err := p.SaveActualOutcomeReceipt(outcome); err != nil {
			t.Fatal(err)
		}
		acceptance := arcCycleStoreTestAcceptance(t, manifest, bundle.Chapter, evidence)
		acceptance.OutcomeReceiptDigest = outcome.ReceiptDigest
		acceptance, err = domain.SignChapterAcceptanceReceipt(acceptance)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.st.ArcCycle().SaveChapterAcceptanceReceipt(acceptance); err != nil {
			t.Fatal(err)
		}
		acceptances = append(acceptances, acceptance)
	}
	return manifest, acceptances
}

func TestAcceptedPlanningWindowBoundaryUsesCompleteOriginalAcceptanceAndSurvivesCursorLoss(t *testing.T) {
	f := newPlanningWindowStoreFixture(t, 8, true)
	manifest, acceptances := planningWindowStoreAcceptThreeChapters(t, f)
	p := NewStore(f.st.Dir()).ProjectedV2()
	before, _ := DirectoryContentRoot(f.st.Dir())
	boundary, err := p.LoadAcceptedPlanningWindowBoundaryV1(f.generation.GenerationID)
	if err != nil {
		t.Fatal(err)
	}
	if boundary.Generation.ExpectedChapterCount != 3 || boundary.LastBundle.Chapter != 3 || boundary.Predecessor.Chapter != 3 ||
		boundary.LastOutcome.ReceiptDigest != acceptances[2].OutcomeReceiptDigest || boundary.Predecessor.BundleDigest != boundary.LastBundle.BundleDigest ||
		boundary.Predecessor.ProjectedPostStateRoot != boundary.LastOutcome.ActualPostStateRoot {
		t.Fatalf("boundary was not derived from the three actual accepted chapters: %+v", boundary.Predecessor)
	}
	after, _ := DirectoryContentRoot(f.st.Dir())
	if before != after {
		t.Fatal("read-only acceptance boundary changed the stored source tree")
	}
	if _, err := domain.NewArcCompletionReceipt(manifest, acceptances, boundary.LastOutcome.ActualPostStateRoot, projectedStoreV2Time()); err == nil {
		t.Fatal("three accepted chapters pretended to complete the eight-chapter logical arc")
	}
	cursorPath := filepath.Join(f.st.Dir(), projectedRealizationCursorPath)
	cursorBackup := filepath.Join(t.TempDir(), "realization-cursor.json")
	if err := os.Rename(cursorPath, cursorBackup); err != nil {
		t.Fatal(err)
	}
	reloaded, err := NewStore(f.st.Dir()).ProjectedV2().LoadAcceptedPlanningWindowBoundaryV1(f.generation.GenerationID)
	if err != nil || !reflect.DeepEqual(reloaded, boundary) {
		t.Fatalf("a mutable cursor replaced immutable accepted proof: %v", err)
	}
	if err := os.Rename(cursorBackup, cursorPath); err != nil {
		t.Fatal(err)
	}
	last := acceptances[2]
	acceptancePath := filepath.Join(f.st.Dir(), arcCycleAcceptancePath(last.GenerationID, last.Chapter, last.ReceiptDigest))
	acceptanceBackup := filepath.Join(t.TempDir(), "acceptance.json")
	if err := os.Rename(acceptancePath, acceptanceBackup); err != nil {
		t.Fatal(err)
	}
	if _, err := p.LoadAcceptedPlanningWindowBoundaryV1(f.generation.GenerationID); err == nil {
		t.Fatal("last outcome/cursor alone replaced the missing third acceptance")
	}
	if err := os.Rename(acceptanceBackup, acceptancePath); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.st.Dir(), "chapters/02.md"), []byte("changed accepted body"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := p.LoadAcceptedPlanningWindowBoundaryV1(f.generation.GenerationID); err == nil {
		t.Fatal("tampered accepted body was used as the next window source")
	}
}

func TestPlanningDetailWindowStoreNextWindowRequiresAcceptedOriginalPredecessor(t *testing.T) {
	f := newPlanningWindowStoreFixture(t, 8, true)
	planningWindowStoreAcceptThreeChapters(t, f)
	boundary, err := f.st.ProjectedV2().LoadAcceptedPlanningWindowBoundaryV1(f.generation.GenerationID)
	if err != nil {
		t.Fatal(err)
	}
	next := f
	next.generation.ParentGenerationID = f.generation.GenerationID
	next.generation.BaseCanonChapter, next.generation.FirstProjectedChapter, next.generation.LastProjectedChapter = 3, 4, 6
	next.generation.BaseCanonRoot = boundary.LastOutcome.ActualCanonRoot
	next.generation.BaseStateRoot = boundary.LastOutcome.ActualPostStateRoot
	next.source.BaseCanonChapter, next.source.BaseCanonRoot, next.source.BaseStateRoot = 3, next.generation.BaseCanonRoot, next.generation.BaseStateRoot
	next.input, next.report = planningWindowStoreSeedRehearsal(t, next.st, next.source, 1, 8, true)
	next.generation.DetailWindow = &domain.PlanningDetailWindowV1{
		Version: domain.PlanningDetailWindowVersionV1, ArcID: f.generation.ScopeID, ArcFirstChapter: 1, ArcLastChapter: 8,
		RehearsalDigest: next.report.ReportDigest, RehearsalInputDigest: next.input.InputDigest,
		AcceptedOutcomeDigest: boundary.LastOutcome.ReceiptDigest, AcceptedPredecessor: &boundary.Predecessor,
	}
	planningWindowStoreRebind(t, &next)
	if err := next.st.ProjectedV2().CreateBuildingGeneration(next.generation, next.source, next.registry); err != nil {
		t.Fatal(err)
	}
	loaded, err := NewStore(next.st.Dir()).ProjectedV2().LoadBuildingGeneration(next.generation.GenerationID)
	if err != nil || loaded == nil || loaded.ScopeID != f.generation.ScopeID || loaded.FirstProjectedChapter != 4 || loaded.LastProjectedChapter != 6 {
		t.Fatalf("same logical arc could not resume its next three-chapter window: %v", err)
	}
	context, err := domain.DeriveProjectedPlanningContextV2(*loaded, nil, next.registry, 4)
	if err != nil || !reflect.DeepEqual(context.PredecessorContract, &boundary.Predecessor) {
		t.Fatalf("next window lost actual accepted incoming consequence: %v", err)
	}
	// A real next-window registry-first crash must remain recoverable. The
	// accepted-source checker cannot require the generation's old registry
	// root/count to have advanced before replaying its durable chapter intent.
	p := next.st.ProjectedV2()
	if err := p.ResetProjectionCursorForRestart(next.generation.GenerationID); err != nil {
		t.Fatal(err)
	}
	cursor, err := p.InitializeProjectionCursor(next.generation.GenerationID)
	if err != nil {
		t.Fatal(err)
	}
	nextRegistry := next.registry
	obligation := projectedStoreV2Obligation(t, next.generation.GenerationID)
	nextRegistry.Obligations = []domain.ObligationV2{obligation}
	nextRegistry.RegistryRoot, _ = domain.ComputeObligationRegistryV2Root(nextRegistry)
	bundle := planningWindowStoreBundles(t, next)[0]
	bundle.ObligationsCreated = []string{obligation.ID}
	bundle.ProjectedDelta.Obligations = []domain.StateMutationV2{{StableID: "obligation:" + obligation.ID, Subject: obligation.ID, Field: "state", Operation: "create", After: "planned", Cause: "本章实际选择产生后续义务"}}
	bundle.ProjectedPostStateRoot, _ = domain.DeriveProjectedPostStateRootV2(bundle.ProjectedPreStateRoot, bundle.ProjectedDelta)
	projectedStoreV2RebindRenderContext(t, &bundle)
	bundle.BundleDigest, _ = domain.ComputeProjectedChapterBundleDigest(bundle)
	fired := false
	p.testFault = func(stage string) error {
		if stage == "registry_written" && !fired {
			fired = true
			return fmt.Errorf("injected registry-first interruption")
		}
		return nil
	}
	if _, err := p.ProjectChapterAndAdvance(next.generation.GenerationDigest, next.generation.ChainTailRoot, next.registry.RegistryRoot, *cursor, bundle, nextRegistry); err == nil || !fired {
		t.Fatalf("next-window registry-first boundary did not execute: %v", err)
	}
	p.testFault = nil
	recovered, recoveredCursor, err := NewStore(next.st.Dir()).ProjectedV2().RecoverBuildingProjection(next.generation.GenerationID)
	if err != nil || recovered == nil || recoveredCursor == nil || recovered.ProjectedChapterCount != 1 || recoveredCursor.LastProjectedChapter != 4 || recovered.ObligationRegistryRoot != nextRegistry.RegistryRoot {
		t.Fatalf("accepted predecessor checks blocked valid window intent recovery: %v", err)
	}
	next.generation.DetailWindow.AcceptedOutcomeDigest = projectedStoreV2Digest("unaccepted-outcome")
	planningWindowStoreRebind(t, &next)
	if err := next.st.ProjectedV2().CreateBuildingGeneration(next.generation, next.source, next.registry); err == nil {
		t.Fatal("a forged accepted-outcome reference opened another formal window")
	}
}
