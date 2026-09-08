package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

import "github.com/chenhongyang/novel-studio/internal/domain"

// These are two real three-chapter windows, with chapter 1's original hard
// obligation carried across the boundary and actually consumed in chapter 5.
func arcWindowAggregateFixture(t *testing.T) (*Store, []string) {
	t.Helper()
	f := newPlanningWindowStoreFixture(t, 6, true)
	obligation := projectedStoreV2Obligation(t, f.generation.GenerationID)
	obligation.Origin.Chapter = 1
	obligation.ID, _ = domain.DeriveObligationIDV2(obligation.Kind, 1, obligation.Contract)
	obligation.DueWindow.FromChapter = 5
	f.registry.Obligations = []domain.ObligationV2{obligation}
	planningWindowStoreRebind(t, &f)
	arcWindowAggregateAcceptWindow(t, f)
	boundary, err := f.st.ProjectedV2().LoadAcceptedPlanningWindowBoundaryV1(f.generation.GenerationID)
	if err != nil {
		t.Fatal(err)
	}
	next := f
	next.generation.ParentGenerationID = f.generation.GenerationID
	next.generation.BaseCanonChapter, next.generation.FirstProjectedChapter, next.generation.LastProjectedChapter = 3, 4, 6
	next.generation.BaseCanonRoot, next.generation.BaseStateRoot = boundary.LastOutcome.ActualCanonRoot, boundary.LastOutcome.ActualPostStateRoot
	next.source.BaseCanonChapter, next.source.BaseCanonRoot, next.source.BaseStateRoot = 3, next.generation.BaseCanonRoot, next.generation.BaseStateRoot
	next.input, next.report = planningWindowStoreSeedRehearsal(t, next.st, next.source, 1, 6, true)
	next.generation.DetailWindow = &domain.PlanningDetailWindowV1{Version: domain.PlanningDetailWindowVersionV1, ArcID: f.generation.ScopeID,
		ArcFirstChapter: 1, ArcLastChapter: 6, RehearsalDigest: next.report.ReportDigest, RehearsalInputDigest: next.input.InputDigest,
		AcceptedOutcomeDigest: boundary.LastOutcome.ReceiptDigest, AcceptedPredecessor: &boundary.Predecessor}
	planningWindowStoreRebind(t, &next)
	arcWindowAggregateAcceptWindow(t, next)
	return f.st, []string{f.generation.GenerationID, next.generation.GenerationID}
}

func arcWindowAggregateAcceptWindow(t *testing.T, f planningWindowStoreFixture) {
	t.Helper()
	p := f.st.ProjectedV2()
	if err := p.CreateBuildingGeneration(f.generation, f.source, f.registry); err != nil {
		t.Fatal(err)
	}
	if f.generation.FirstProjectedChapter > 1 {
		if err := p.ResetProjectionCursorForRestart(f.generation.GenerationID); err != nil {
			t.Fatal(err)
		}
	}
	cursor, err := p.InitializeProjectionCursor(f.generation.GenerationID)
	if err != nil {
		t.Fatal(err)
	}
	templates := planningWindowStoreBundles(t, f)
	var bundles []domain.ProjectedChapterBundle
	generation, registry := f.generation, f.registry
	previous, _ := domain.DeriveProjectedChainGenesisV2(generation)
	state := generation.BaseStateRoot
	for _, bundle := range templates {
		context, err := domain.DeriveProjectedPlanningContextV2(f.generation, bundles, registry, bundle.Chapter)
		if err != nil {
			t.Fatal(err)
		}
		capacity := bundle.ChapterPlan.CausalSimulation.RenderCapacity
		bundle = projectedStoreV2Bundle(t, generation.GenerationID, bundle.Chapter, previous, state, context.ContextDigest, context.PredecessorContract, false)
		bundle.ChapterPlan.CausalSimulation.RenderCapacity = capacity
		if len(registry.Obligations) != 0 && (bundle.Chapter == 1 || bundle.Chapter == 5) {
			obligation := registry.Obligations[0]
			operation, before, after := "create", "", "planned"
			bundle.ObligationsCreated = []string{obligation.ID}
			if bundle.Chapter == 5 {
				operation, before, after = "consume", "planned", "satisfied"
				bundle.ObligationsCreated, bundle.ObligationsConsumed = nil, []string{obligation.ID}
				registry.Obligations = append([]domain.ObligationV2(nil), registry.Obligations...)
				registry.Obligations[0].State = domain.ObligationSatisfiedV2
				registry.Obligations[0].Evidence = []domain.ObligationEvidenceV2{{Chapter: 5, SourceDigest: projectedStoreV2Digest("actual-choice-5"), Detail: "第五章实际行动兑现第一章的资源代价"}}
				registry.RegistryRoot, _ = domain.ComputeObligationRegistryV2Root(registry)
			}
			bundle.ProjectedDelta.Obligations = []domain.StateMutationV2{{StableID: "obligation:" + obligation.ID, Subject: obligation.ID, Field: "state", Operation: operation, Before: before, After: after, Cause: "本章实际行动的可验证后果"}}
		}
		bundle.ProjectedPostStateRoot, _ = domain.DeriveProjectedPostStateRootV2(bundle.ProjectedPreStateRoot, bundle.ProjectedDelta)
		projectedStoreV2RebindRenderContext(t, &bundle)
		bundle.BundleDigest, _ = domain.ComputeProjectedChapterBundleDigest(bundle)
		cursor, err = p.ProjectChapterAndAdvance(generation.GenerationDigest, generation.ChainTailRoot, generation.ObligationRegistryRoot, *cursor, bundle, registry)
		if err != nil {
			t.Fatalf("project actual fixture chapter %d: %v", bundle.Chapter, err)
		}
		loaded, err := p.LoadBuildingGeneration(generation.GenerationID)
		if err != nil {
			t.Fatal(err)
		}
		generation, previous, state = *loaded, bundle.BundleDigest, bundle.ProjectedPostStateRoot
		bundles = append(bundles, bundle)
	}
	if _, err := p.SealGeneration(generation.GenerationID); err != nil {
		t.Fatal(err)
	}
	manifest := arcCycleStoreTestManifest(t, generation.GenerationID)
	manifest.ArcID, manifest.DetailWindow, manifest.BookLastChapter, manifest.FullOutlineDigest = generation.ScopeID, generation.DetailWindow, generation.BookHorizonChapter, generation.StableOutlineRoot
	manifest.FirstChapter, manifest.LastChapter = generation.FirstProjectedChapter, generation.LastProjectedChapter
	manifest.Chapters, manifest.CausalLinks = nil, nil
	for i, bundle := range bundles {
		capacity, _ := json.Marshal(bundle.ChapterPlan.CausalSimulation.RenderCapacity)
		manifest.Chapters = append(manifest.Chapters, domain.ArcChapterPlanningBinding{Chapter: bundle.Chapter, BundleDigest: bundle.BundleDigest, CapacityDigest: domain.ComputeArcArtifactSHA256(capacity)})
		if i > 0 {
			manifest.CausalLinks = append(manifest.CausalLinks, domain.ArcCausalLink{ID: fmt.Sprintf("cause-%d", bundle.Chapter), FromChapter: bundle.Chapter - 1, ToChapter: bundle.Chapter, Cause: "上一章的实际代价", Effect: "本章据此作出选择"})
		}
	}
	manifest.Turns = []domain.ArcNarrativeMarker{{ID: "window-turn", Chapter: generation.FirstProjectedChapter + 1, Summary: "实际行动改变处境"}}
	manifest.Payoffs = []domain.ArcNarrativeMarker{{ID: "window-payoff", Chapter: generation.LastProjectedChapter, Summary: "阶段承诺在真实后果中兑现"}}
	manifest.CarriedObligations, err = domain.ArcWindowCarriedObligationsV1(registry)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err = domain.SignArcPlanningManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.st.ArcCycle().SaveArcPlanningManifest(manifest); err != nil {
		t.Fatal(err)
	}
	if _, _, err := p.ActivateSealedGeneration(generation.GenerationID, nil); err != nil {
		t.Fatal(err)
	}
	for _, bundle := range bundles {
		evidence := arcCycleStoreWriteEvidence(t, f.st.Dir(), bundle.Chapter)
		promotion := projectedStoreV2Promotion(t, bundle)
		if _, err := p.SavePromotionReceipt(promotion); err != nil {
			t.Fatal(err)
		}
		outcome := projectedStoreV2Outcome(t, bundle, promotion)
		outcome.ChapterBodySHA256 = domain.ComputeArcChapterBodySHA256(evidence.body)
		outcome.ObligationsSatisfied = append([]string(nil), bundle.ObligationsConsumed...)
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
	}
}

func TestArcWindowAggregateCompletesSixAcceptedChaptersAndReloadsWithoutCursors(t *testing.T) {
	st, ids := arcWindowAggregateFixture(t)
	before := map[string][]byte{}
	for chapter := 1; chapter <= 6; chapter++ {
		path := filepath.Join(st.Dir(), fmt.Sprintf("chapters/%02d.md", chapter))
		before[path], _ = os.ReadFile(path)
	}
	receipt, err := st.CompleteWindowedArcV1(ids, projectedStoreV2Time())
	if err != nil {
		t.Fatal(err)
	}
	if receipt.FirstChapter != 1 || receipt.LastChapter != 6 || len(receipt.Windows) != 2 || len(receipt.Windows[0].Acceptances) != 3 || len(receipt.Windows[1].Acceptances) != 3 {
		t.Fatalf("aggregate is not the full original two-window six-chapter arc: %+v", receipt)
	}
	again, err := st.CompleteWindowedArcV1(ids, projectedStoreV2Time())
	if err != nil || !reflect.DeepEqual(receipt, again) {
		t.Fatalf("idempotent aggregate publication: %v", err)
	}
	for _, rel := range []string{projectedProjectionCursorPath, projectedRealizationCursorPath, projectedActiveGenerationPath} {
		if err := os.Remove(filepath.Join(st.Dir(), rel)); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
	loaded, err := NewStore(st.Dir()).LoadVerifiedWindowedArcCompletionV1(ids[1], receipt.ReceiptDigest)
	if err != nil || !reflect.DeepEqual(receipt, loaded) {
		t.Fatalf("aggregate recovery depends on mutable cursor state: %v", err)
	}
	for path, original := range before {
		actual, err := os.ReadFile(path)
		if err != nil || string(actual) != string(original) {
			t.Fatalf("completion changed accepted prose %s: %v", path, err)
		}
	}
	if old, err := st.ArcCycle().ListArcCompletionReceipts(ids[1]); err != nil || len(old) != 0 {
		t.Fatalf("aggregate leaked into the unchanged v1 completion collection: %v", err)
	}
}

func TestArcWindowAggregateRejectsOmittedDuplicateReorderedAndFinalOnlySelections(t *testing.T) {
	st, ids := arcWindowAggregateFixture(t)
	for name, selected := range map[string][]string{"none": nil, "first-only": ids[:1], "final-only": ids[1:], "duplicate": {ids[0], ids[0], ids[1]}, "reordered": {ids[1], ids[0]}, "foreign": {"pg2_foreign", ids[1]}} {
		t.Run(name, func(t *testing.T) {
			if _, err := st.CompleteWindowedArcV1(selected, projectedStoreV2Time()); err == nil {
				t.Fatal("incomplete or foreign selection proved whole-arc completion")
			}
		})
	}
	if _, err := os.Stat(filepath.Join(st.Dir(), arcWindowAggregateDirV1)); !os.IsNotExist(err) {
		t.Fatal("rejected completion published an aggregate")
	}
}

func TestArcWindowAggregateReloadRejectsTamperedOriginalEvidence(t *testing.T) {
	st, ids := arcWindowAggregateFixture(t)
	receipt, err := st.CompleteWindowedArcV1(ids, projectedStoreV2Time())
	if err != nil {
		t.Fatal(err)
	}
	windows, err := withProjectedReadResult(st.ProjectedV2(), func() ([]domain.ArcWindowCompletionEvidenceV1, error) {
		return st.ProjectedV2().loadArcWindowAggregateEvidenceUnlocked(ids)
	})
	if err != nil {
		t.Fatal(err)
	}
	first := windows[0]
	reportPath, _ := arcRehearsalPath("reports", first.Generation.DetailWindow.RehearsalDigest)
	paths := map[string]string{
		"first-body": "chapters/01.md", "first-review": first.Acceptances[0].ReviewArtifacts[0].Path,
		"first-manifest":   arcCycleManifestPath(ids[0], first.Manifest.ManifestDigest),
		"first-acceptance": arcCycleAcceptancePath(ids[0], 1, first.Acceptances[0].ReceiptDigest),
		"first-outcome":    projectedActualOutcomePath(ids[0], 1, first.Outcomes[0].ReceiptDigest),
		"first-bundle":     projectedBundlePath(projectedSealedGenerationPath(ids[0]), 1),
		"first-registry":   filepath.Join(projectedSealedGenerationPath(ids[0]), projectedObligationRegistryFile),
		"first-rehearsal":  reportPath,
		"final-parent":     filepath.Join(projectedSealedGenerationPath(ids[1]), projectedGenerationManifestFile),
		"aggregate":        arcWindowAggregatePathV1(ids[1], receipt.ReceiptDigest),
	}
	for name, rel := range paths {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(st.Dir(), rel)
			original, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = os.WriteFile(path, original, 0o644) }()
			if err := os.WriteFile(path, []byte("tampered original evidence"), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := NewStore(st.Dir()).LoadVerifiedWindowedArcCompletionV1(ids[1], receipt.ReceiptDigest); err == nil {
				t.Fatal("aggregate trusted a stale receipt after original evidence changed")
			}
		})
	}
	if _, err := NewStore(st.Dir()).LoadVerifiedWindowedArcCompletionV1(ids[1], receipt.ReceiptDigest); err != nil {
		t.Fatalf("fixture restoration did not recover original proof: %v", err)
	}
}

func TestArcWindowAggregateDomainRejectsReSignedFalseClaimsAndLostObligations(t *testing.T) {
	st, ids := arcWindowAggregateFixture(t)
	p := st.ProjectedV2()
	windows, err := withProjectedReadResult(p, func() ([]domain.ArcWindowCompletionEvidenceV1, error) {
		return p.loadArcWindowAggregateEvidenceUnlocked(ids)
	})
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := domain.NewArcWindowAggregateCompletionV1(windows, projectedStoreV2Time())
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"range", "actual-root", "manifest", "acceptance", "registry"} {
		t.Run(name, func(t *testing.T) {
			raw, _ := json.Marshal(receipt)
			var fake domain.ArcWindowAggregateCompletionV1
			_ = json.Unmarshal(raw, &fake)
			switch name {
			case "range":
				fake.FirstChapter = 4
			case "actual-root":
				fake.FinalActualPostStateRoot = projectedStoreV2Digest("forged-root")
			case "manifest":
				fake.Windows[0].ManifestDigest = projectedStoreV2Digest("forged-manifest")
			case "acceptance":
				fake.Windows[0].Acceptances[0].AcceptanceReceiptDigest = projectedStoreV2Digest("forged-acceptance")
			case "registry":
				fake.Windows[0].RegistryRoot = projectedStoreV2Digest("forged-registry")
			}
			fake.ReceiptDigest, _ = domain.ComputeArcWindowAggregateCompletionDigestV1(fake)
			if err := domain.ValidateArcWindowAggregateCompletionAgainstWindowsV1(fake, windows); err == nil {
				t.Fatal("re-signing a false aggregate replaced original evidence")
			}
		})
	}
	for _, mode := range []string{"drop", "contract", "deadline", "hardness", "origin", "consumer"} {
		t.Run("inherited-"+mode, func(t *testing.T) {
			raw, _ := json.Marshal(windows)
			var changed []domain.ArcWindowCompletionEvidenceV1
			_ = json.Unmarshal(raw, &changed)
			obligation := &changed[1].Registry.Obligations[0]
			switch mode {
			case "drop":
				changed[1].Registry.Obligations = nil
			case "contract":
				obligation.Contract += "（偷偷改写）"
			case "deadline":
				obligation.DueWindow.ToChapter = 6
			case "hardness":
				obligation.Hardness = domain.ObligationSoftV2
			case "origin":
				obligation.Origin.SourceDigest = projectedStoreV2Digest("forged-origin")
			case "consumer":
				obligation.ConsumerChapters = []int{6}
			}
			// Original registry content is evidence, not a caller-supplied summary.
			if err := domain.ValidateArcWindowAggregateCompletionAgainstWindowsV1(receipt, changed); err == nil {
				t.Fatal("changed inherited obligation was accepted")
			}
		})
	}
	t.Run("actual-satisfaction-not-merely-registry-state", func(t *testing.T) {
		raw, _ := json.Marshal(windows)
		var changed []domain.ArcWindowCompletionEvidenceV1
		_ = json.Unmarshal(raw, &changed)
		outcome := &changed[1].Outcomes[1] // Actual chapter 5 consumer.
		outcome.ObligationsSatisfied = nil
		outcome.ReceiptDigest, _ = domain.ComputeActualOutcomeReceiptV2Digest(*outcome)
		acceptance := &changed[1].Acceptances[1]
		acceptance.OutcomeReceiptDigest = outcome.ReceiptDigest
		*acceptance, err = domain.SignChapterAcceptanceReceipt(*acceptance)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := domain.NewArcWindowAggregateCompletionV1(changed, projectedStoreV2Time()); err == nil || !strings.Contains(err.Error(), "obligation evidence") {
			t.Fatalf("satisfied registry tag replaced actual consumer proof: %v", err)
		}
	})
	if _, err := domain.NewArcCompletionReceipt(windows[1].Manifest, windows[1].Acceptances, receipt.FinalActualPostStateRoot, projectedStoreV2Time()); err == nil || !strings.Contains(err.Error(), "detail window") {
		t.Fatalf("final window alone became an old v1 whole-arc completion: %v", err)
	}
}

func TestArcWindowAggregateLeavesOriginalThreeChapterCompletionPathUnchanged(t *testing.T) {
	f := newPlanningWindowStoreFixture(t, 3, true)
	manifest, acceptances := planningWindowStoreAcceptThreeChapters(t, f)
	boundary, err := f.st.ProjectedV2().LoadAcceptedPlanningWindowBoundaryV1(f.generation.GenerationID)
	if err != nil {
		t.Fatal(err)
	}
	completion, err := domain.NewArcCompletionReceipt(manifest, acceptances, boundary.LastOutcome.ActualPostStateRoot, projectedStoreV2Time())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.st.ArcCycle().SaveArcCompletionReceipt(completion); err != nil {
		t.Fatal(err)
	}
	if err := f.st.ArcCycle().ValidateArcCompletion(f.generation.GenerationID, completion.ReceiptDigest); err != nil {
		t.Fatal(err)
	}
	if _, err := f.st.CompleteWindowedArcV1([]string{f.generation.GenerationID}, projectedStoreV2Time()); err == nil {
		t.Fatal("single three-chapter arc silently switched completion versions")
	}
}
