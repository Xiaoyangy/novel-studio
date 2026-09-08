package domain

import (
	"encoding/json"
	"testing"
)

func TestArcWindowAggregateInheritedObligationsRetainOriginalTermsAfterResigning(t *testing.T) {
	previous, registry := detailWindowGenerationFixture(t, 1, 3, 6, PlanningGenerationSealedV2, nil)
	obligation := planningV2ArcObligation(t, previous.GenerationID, 1, 5, ObligationPlannedV2, "第五章兑现第一章既有资源代价")
	previous, registry = planningV2ArcBindObligations(t, previous, registry, obligation)
	next, _ := detailWindowGenerationFixture(t, 4, 6, 6, PlanningGenerationBuildingV2, &previous)
	next, carried, err := CarryForwardArcObligationsV2(previous, registry, next)
	if err != nil {
		t.Fatal(err)
	}
	left := ArcWindowCompletionEvidenceV1{Generation: previous, Registry: registry}
	right := ArcWindowCompletionEvidenceV1{Generation: next, Registry: carried}
	if err := validateArcWindowInheritedObligationsV1(left, right); err != nil {
		t.Fatalf("original carried obligation was rejected: %v", err)
	}
	for _, mode := range []string{"drop", "kind", "contract", "hardness", "origin-generation", "origin-chapter", "origin-source", "due-start", "due-end", "terminal", "consumer"} {
		t.Run(mode, func(t *testing.T) {
			raw, _ := json.Marshal(right)
			var changed ArcWindowCompletionEvidenceV1
			_ = json.Unmarshal(raw, &changed)
			o := &changed.Registry.Obligations[0]
			switch mode {
			case "drop":
				changed.Registry.Obligations = nil
			case "kind":
				o.Kind = ObligationResourceV2
			case "contract":
				o.Contract += "（改写原义务）"
			case "hardness":
				o.Hardness = ObligationSoftV2
			case "origin-generation":
				o.Origin.GenerationID = next.GenerationID
			case "origin-chapter":
				o.Origin.Chapter = 2
			case "origin-source":
				o.Origin.SourceDigest = planningV2TestDigest(t, "forged-origin")
			case "due-start":
				o.DueWindow.FromChapter = 4
			case "due-end":
				o.DueWindow.ToChapter = 6
			case "terminal":
				o.DueWindow.TerminalResolution = !o.DueWindow.TerminalResolution
			case "consumer":
				o.ConsumerChapters = []int{6}
			}
			changed.Registry.RegistryRoot, _ = ComputeObligationRegistryV2Root(changed.Registry)
			changed.Generation.ObligationRegistryRoot = changed.Registry.RegistryRoot
			changed.Generation.GenerationDigest, _ = ComputePlanningGenerationV2Digest(changed.Generation)
			if err := validateArcWindowInheritedObligationsV1(left, changed); err == nil {
				t.Fatal("re-signing changed or dropped original obligation terms")
			}
		})
	}
	// State/evidence can advance, but only the aggregate's separately verified
	// actual consumer bundle/outcome may establish satisfaction.
	right.Registry.Obligations[0].State = ObligationSatisfiedV2
	right.Registry.Obligations[0].Evidence = []ObligationEvidenceV2{{Chapter: 5, SourceDigest: planningV2TestDigest(t, "actual-consumer"), Detail: "实际消费"}}
	if err := validateArcWindowInheritedObligationsV1(left, right); err != nil {
		t.Fatalf("legitimate satisfaction changed immutable terms: %v", err)
	}
}

func TestArcWindowAggregateUsesOriginalDueStartAndConsumerCarryBoundary(t *testing.T) {
	for _, mode := range []string{"due-start", "consumer"} {
		t.Run(mode, func(t *testing.T) {
			previous, registry := detailWindowGenerationFixture(t, 1, 3, 6, PlanningGenerationSealedV2, nil)
			obligation := planningV2ArcObligation(t, previous.GenerationID, 1, 5, ObligationPlannedV2, "原始到期义务不能被窗口末章跳过")
			if mode == "due-start" {
				obligation.DueWindow.FromChapter = 3 // ToChapter alone still says 5.
			} else {
				obligation.ConsumerChapters = []int{3, 5}
			}
			previous, registry = planningV2ArcBindObligations(t, previous, registry, obligation)
			next, nextRegistry := detailWindowGenerationFixture(t, 4, 6, 6, PlanningGenerationBuildingV2, &previous)
			if err := validateArcWindowInheritedObligationsV1(ArcWindowCompletionEvidenceV1{Generation: previous, Registry: registry}, ArcWindowCompletionEvidenceV1{Generation: next, Registry: nextRegistry}); err == nil {
				t.Fatal("already due unresolved obligation crossed window boundary")
			}
		})
	}
}
