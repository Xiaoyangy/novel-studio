package domain

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func detailWindowGenerationFixture(t *testing.T, first, last, arcLast int, status PlanningGenerationStatusV2, previous *PlanningGenerationV2) (PlanningGenerationV2, ObligationRegistryV2) {
	t.Helper()
	parent := ""
	if previous != nil {
		parent = previous.GenerationID
	}
	arcID := DeriveArcCycleID(1, 1, 1, arcLast)
	generation, registry := planningV2ArcGenerationFixture(t, arcID, first, last, arcLast, status, parent)
	window := PlanningDetailWindowV1{
		Version: PlanningDetailWindowVersionV1, ArcID: arcID, ArcFirstChapter: 1, ArcLastChapter: arcLast,
		RehearsalDigest:      planningV2TestDigest(t, "rehearsal-before-"+string(rune('0'+first))),
		RehearsalInputDigest: planningV2TestDigest(t, "rehearsal-input-before-"+string(rune('0'+first))),
	}
	if previous != nil {
		generation.BaseCanonRoot = planningV2TestDigest(t, "accepted-canon-"+previous.GenerationID)
		generation.BaseStateRoot = planningV2TestDigest(t, "accepted-state-"+previous.GenerationID)
		window.AcceptedOutcomeDigest = planningV2TestDigest(t, "accepted-outcome-"+previous.GenerationID)
		window.AcceptedPredecessor = &ProjectedPlanningPredecessorContractV2{
			Chapter: previous.LastProjectedChapter, OutgoingConsequenceID: "carried-choice", OutgoingConsequenceText: "原选择已经造成的下一章压力",
			BundleDigest: previous.ChainTailRoot, ProjectedPostStateRoot: generation.BaseStateRoot,
		}
	}
	var err error
	generation.DetailWindow = &window
	generation.PlanningDependencyRoot, err = PlanningDetailWindowDependencyRootV1(generation.PlanningDependencyRoot, window)
	if err != nil {
		t.Fatal(err)
	}
	generation.GenerationID, err = DerivePlanningGenerationAttemptV2ID(generation.BaseCanonRoot, generation.StableOutlineRoot, generation.PlanningDependencyRoot, generation.RandomSeedContractRoot, generation.AttemptID)
	if err != nil {
		t.Fatal(err)
	}
	registry.GenerationID = generation.GenerationID
	registry.RegistryRoot, _ = ComputeObligationRegistryV2Root(registry)
	generation.ObligationRegistryRoot = registry.RegistryRoot
	generation.GenerationDigest, _ = ComputePlanningGenerationV2Digest(generation)
	if err := ValidatePlanningGenerationV2(generation); err != nil {
		t.Fatal(err)
	}
	return generation, registry
}

func TestPlanningDetailWindowUsesThreeFormalChaptersAndRealArcRemainder(t *testing.T) {
	for _, tc := range []struct{ accepted, arcLast, first, last int }{
		{0, 3, 1, 3}, // The actual three-chapter project remains a full three-chapter transaction.
		{0, 8, 1, 3}, {3, 8, 4, 6}, {6, 8, 7, 8},
	} {
		first, last, err := PlanningDetailWindowRangeV1(tc.accepted, 1, tc.arcLast)
		if err != nil || first != tc.first || last != tc.last {
			t.Fatalf("bad detail window: %d..%d, %v", first, last, err)
		}
	}
	for _, args := range [][3]int{{-1, 1, 8}, {8, 1, 8}, {1, 4, 8}, {0, 0, 8}, {0, 3, 2}} {
		if _, _, err := PlanningDetailWindowRangeV1(args[0], args[1], args[2]); err == nil {
			t.Fatalf("invalid accepted/arc boundary accepted: %v", args)
		}
	}
}

func TestPlanningDetailWindowRejectsScopeRangeAndMissingRehearsal(t *testing.T) {
	generation, _ := detailWindowGenerationFixture(t, 1, 3, 8, PlanningGenerationBuildingV2, nil)
	original, _ := json.Marshal(generation)
	for _, mutate := range []func(*PlanningGenerationV2){
		func(g *PlanningGenerationV2) { g.LastProjectedChapter, g.ExpectedChapterCount = 8, 8 },
		func(g *PlanningGenerationV2) { g.LastProjectedChapter, g.ExpectedChapterCount = 2, 2 },
		func(g *PlanningGenerationV2) { g.FirstProjectedChapter = 2 },
		func(g *PlanningGenerationV2) { g.DetailWindow.RehearsalDigest = "" },
		func(g *PlanningGenerationV2) { g.DetailWindow.Version = "unknown" },
		func(g *PlanningGenerationV2) { g.DetailWindow.ArcID = "different-arc" },
		func(g *PlanningGenerationV2) { g.BookHorizonChapter = 7 },
	} {
		var changed PlanningGenerationV2
		if err := json.Unmarshal(original, &changed); err != nil {
			t.Fatal(err)
		}
		mutate(&changed)
		changed.GenerationDigest, _ = ComputePlanningGenerationV2Digest(changed)
		if err := ValidatePlanningGenerationV2(changed); err == nil {
			t.Fatalf("invalid detail-window generation was accepted: %+v", changed.DetailWindow)
		}
	}
	changed := *generation.DetailWindow
	changed.RehearsalDigest = planningV2TestDigest(t, "revised-whole-arc-rehearsal")
	left, _ := PlanningDetailWindowDependencyRootV1(planningV2TestDigest(t, "base-input"), *generation.DetailWindow)
	right, _ := PlanningDetailWindowDependencyRootV1(planningV2TestDigest(t, "base-input"), changed)
	if left == right {
		t.Fatal("a revised rehearsal reused the old planning dependency identity")
	}
}

func TestPlanningDetailWindowKeepsLegacyNilWireAndFullArcRule(t *testing.T) {
	generation, _ := planningV2ArcGenerationFixture(t, "legacy-arc", 1, 8, 8, PlanningGenerationBuildingV2, "")
	if err := ValidatePlanningGenerationV2(generation); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(generation)
	if strings.Contains(string(raw), "detail_window") || generation.ExpectedChapterCount != 8 {
		t.Fatal("legacy generation acquired a window marker or shortened range")
	}
	manifest := arcCycleDomainTestManifest(t, "pg2_old_manifest")
	raw, _ = json.Marshal(manifest)
	if strings.Contains(string(raw), "detail_window") {
		t.Fatal("old manifest wire format acquired an empty window binding")
	}
}

func TestPlanningDetailWindowCarriesSameScopeWithoutChangingObligationDeadline(t *testing.T) {
	previous, previousRegistry := detailWindowGenerationFixture(t, 1, 3, 8, PlanningGenerationSealedV2, nil)
	future := planningV2ArcObligation(t, previous.GenerationID, 2, 7, ObligationPlannedV2, "第七章用原始回执回应已成立的选择")
	previous, previousRegistry = planningV2ArcBindObligations(t, previous, previousRegistry, future)
	next, _ := detailWindowGenerationFixture(t, 4, 6, 8, PlanningGenerationBuildingV2, &previous)
	rebound, carried, err := CarryForwardArcObligationsV2(previous, previousRegistry, next)
	if err != nil {
		t.Fatal(err)
	}
	if carried.ScopeID != previous.ScopeID || len(carried.Obligations) != 1 || !reflect.DeepEqual(carried.Obligations[0], future) {
		t.Fatal("window cut changed the logical scope, source obligation or original deadline")
	}
	context, err := DeriveProjectedPlanningContextV2(rebound, nil, carried, 4)
	if err != nil || !reflect.DeepEqual(context.PredecessorContract, next.DetailWindow.AcceptedPredecessor) {
		t.Fatalf("next window lost the accepted outgoing consequence: %+v %v", context.PredecessorContract, err)
	}
	context.PredecessorContract.OutgoingConsequenceText = "model-local mutation"
	if next.DetailWindow.AcceptedPredecessor.OutgoingConsequenceText == "model-local mutation" {
		t.Fatal("planning view mutated immutable accepted predecessor data")
	}
	context, err = DeriveProjectedPlanningContextV2(rebound, nil, carried, 4)
	if err != nil {
		t.Fatal(err)
	}
	context.DetailWindow = nil
	context.ContextDigest, _ = ComputeProjectedPlanningContextV2Digest(context)
	if err := ValidateProjectedPlanningContextV2(context); err == nil {
		t.Fatal("dropping the window source marker laundered an accepted predecessor into a legacy context")
	}
	bad := future
	bad.DueWindow, bad.ConsumerChapters = ObligationDueWindowV2{FromChapter: 3, ToChapter: 3}, []int{3}
	dueGeneration, dueRegistry := planningV2ArcBindObligations(t, previous, previousRegistry, bad)
	if _, _, err := CarryForwardArcObligationsV2(dueGeneration, dueRegistry, next); err == nil {
		t.Fatal("unfinished due work crossed the detail-window boundary")
	}
	for _, mode := range []string{"dropped-window", "changed-scope", "foreign-bundle", "changed-actual-state"} {
		raw, _ := json.Marshal(next)
		var changed PlanningGenerationV2
		_ = json.Unmarshal(raw, &changed)
		switch mode {
		case "dropped-window":
			changed.DetailWindow = nil
		case "changed-scope":
			changed.ScopeID, changed.DetailWindow.ArcID = "other-arc", "other-arc"
		case "foreign-bundle":
			changed.DetailWindow.AcceptedPredecessor.BundleDigest = planningV2TestDigest(t, "foreign-bundle")
		case "changed-actual-state":
			changed.DetailWindow.AcceptedPredecessor.ProjectedPostStateRoot = planningV2TestDigest(t, "unaccepted-state")
		}
		changed.GenerationDigest, _ = ComputePlanningGenerationV2Digest(changed)
		if _, _, err := CarryForwardArcObligationsV2(previous, previousRegistry, changed); err == nil {
			t.Fatalf("window carry accepted %s", mode)
		}
	}
}

func TestPlanningDetailWindowManifestCannotPretendToCompleteLogicalArc(t *testing.T) {
	for _, arcLast := range []int{5, 8} {
		manifest := arcCycleDomainTestManifest(t, "pg2_detail_window_manifest") // Three actual chapter bindings, 3..5.
		manifest.DetailWindow = &PlanningDetailWindowV1{
			Version: PlanningDetailWindowVersionV1, ArcID: DeriveArcCycleID(1, 2, 3, arcLast), ArcFirstChapter: 3, ArcLastChapter: arcLast,
			RehearsalDigest:      arcCycleDomainTestDigest("whole-arc-rehearsal"),
			RehearsalInputDigest: arcCycleDomainTestDigest("whole-arc-input"),
		}
		manifest.ArcID = manifest.DetailWindow.ArcID
		var err error
		manifest, err = SignArcPlanningManifest(manifest)
		if err != nil {
			t.Fatal(err)
		}
		var acceptances []ChapterAcceptanceReceipt
		for chapter := 3; chapter <= 5; chapter++ {
			acceptances = append(acceptances, arcCycleDomainTestAcceptance(t, manifest, chapter, "2026-07-17T12:00:00Z"))
		}
		_, err = NewArcCompletionReceipt(manifest, acceptances, arcCycleDomainTestDigest("actual-state"), "2026-07-17T13:00:00Z")
		if (arcLast == 5 && err != nil) || (arcLast == 8 && (err == nil || !strings.Contains(err.Error(), "detail window"))) {
			t.Fatalf("logical arc/window completion confused, arcLast=%d: %v", arcLast, err)
		}
		manifest.Chapters = manifest.Chapters[:2]
		if _, err := SignArcPlanningManifest(manifest); err == nil {
			t.Fatal("coarse/missing third chapter replaced required formal bundle coverage")
		}
	}
}

func TestPlanningDetailWindowSourceSnapshotMustBindSameRehearsal(t *testing.T) {
	generation, _ := detailWindowGenerationFixture(t, 1, 3, 3, PlanningGenerationBuildingV2, nil)
	source := PlanningSourceSnapshotV2{
		Version: PlanningSourceSnapshotV2Version, GenerationID: generation.GenerationID, BaseCanonChapter: generation.BaseCanonChapter,
		BaseCanonRoot: generation.BaseCanonRoot, BaseStateRoot: generation.BaseStateRoot, StableOutlineRoot: generation.StableOutlineRoot,
		PlanningDependencyRoot: generation.PlanningDependencyRoot, RandomSeedContractRoot: generation.RandomSeedContractRoot,
		FoundationSnapshotRoot: planningV2TestDigest(t, "foundation"), RAGSnapshotRoot: planningV2TestDigest(t, "rag"),
		CapturedAt: planningV2TestTime(), DetailWindow: generation.DetailWindow,
	}
	source.SnapshotDigest, _ = ComputePlanningSourceSnapshotV2Digest(source)
	if err := ValidatePlanningSourceSnapshotAgainstGenerationV2(source, generation); err != nil {
		t.Fatal(err)
	}
	source.DetailWindow = nil
	source.SnapshotDigest, _ = ComputePlanningSourceSnapshotV2Digest(source)
	if err := ValidatePlanningSourceSnapshotAgainstGenerationV2(source, generation); err == nil {
		t.Fatal("source snapshot lost its versioned rehearsal binding")
	}
	foundation, rag := planningV2TestDigest(t, "foundation"), planningV2TestDigest(t, "rag")
	got, err := ComputeArcRehearsalSourceRootV1(foundation, rag)
	want, _ := planningV2Digest(struct {
		Version        string `json:"version"`
		FoundationRoot string `json:"foundation_root"`
		RAGRoot        string `json:"rag_root"`
	}{"arc-rehearsal-source.v1", foundation, rag})
	if err != nil || got != want {
		t.Fatalf("rehearsal source root differs from the agreed CLI contract: %s %s %v", got, want, err)
	}
}
