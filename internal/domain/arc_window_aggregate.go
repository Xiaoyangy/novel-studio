package domain

import (
	"fmt"
	"sort"
)

const ArcWindowAggregateCompletionVersionV1 = "arc-window-aggregate-completion.v1"

// ArcWindowAggregateBindingV1 names original immutable evidence; it is not a
// replacement manifest and cannot promote speculative rehearsal into a plan.
type ArcWindowAggregateBindingV1 struct {
	GenerationID     string                        `json:"generation_id"`
	GenerationDigest string                        `json:"generation_digest"`
	ManifestDigest   string                        `json:"manifest_digest"`
	RegistryRoot     string                        `json:"registry_root"`
	FirstChapter     int                           `json:"first_chapter"`
	LastChapter      int                           `json:"last_chapter"`
	Acceptances      []ArcChapterAcceptanceBinding `json:"acceptances"`
}

// ArcWindowAggregateCompletionV1 is separate from the unchanged single-window
// ArcCompletionReceipt. Its authority requires reloading every original window.
type ArcWindowAggregateCompletionV1 struct {
	Version                   string                        `json:"version"`
	ArcID                     string                        `json:"arc_id"`
	FirstChapter              int                           `json:"first_chapter"`
	LastChapter               int                           `json:"last_chapter"`
	Windows                   []ArcWindowAggregateBindingV1 `json:"windows"`
	FinalGenerationID         string                        `json:"final_generation_id"`
	FinalOutcomeReceiptDigest string                        `json:"final_outcome_receipt_digest"`
	FinalActualPostStateRoot  string                        `json:"final_actual_post_state_root"`
	CarriedObligationRoot     string                        `json:"carried_obligation_root"`
	CompletedAt               string                        `json:"completed_at"`
	ReceiptDigest             string                        `json:"receipt_digest"`
}

// ArcWindowCompletionEvidenceV1 is transient validation input, never persisted
// as another copy of prose. Store additionally authenticates rehearsal sources,
// promotions, and the exact accepted body/review bytes before using it.
type ArcWindowCompletionEvidenceV1 struct {
	Generation  PlanningGenerationV2
	Manifest    ArcPlanningManifest
	Registry    ObligationRegistryV2
	Bundles     []ProjectedChapterBundle
	Acceptances []ChapterAcceptanceReceipt
	Outcomes    []ActualOutcomeReceiptV2
}

func ComputeArcWindowAggregateCompletionDigestV1(receipt ArcWindowAggregateCompletionV1) (string, error) {
	receipt.ReceiptDigest = ""
	return planningV2Digest(receipt)
}

func ValidateArcWindowAggregateCompletionV1(receipt ArcWindowAggregateCompletionV1) error {
	if receipt.Version != ArcWindowAggregateCompletionVersionV1 || len(receipt.Windows) < 2 || receipt.ArcID == "" || receipt.FirstChapter <= 0 || receipt.LastChapter < receipt.FirstChapter {
		return fmt.Errorf("window aggregate requires at least two windows of a complete logical arc")
	}
	if err := validatePlanningV2Time("completed_at", receipt.CompletedAt); err != nil {
		return err
	}
	next := receipt.FirstChapter
	seen := map[string]bool{}
	for _, window := range receipt.Windows {
		if window.GenerationID == "" || seen[window.GenerationID] || window.FirstChapter != next || window.LastChapter < next || window.LastChapter > receipt.LastChapter || len(window.Acceptances) != window.LastChapter-window.FirstChapter+1 {
			return fmt.Errorf("window aggregate has duplicate, missing or unordered coverage")
		}
		seen[window.GenerationID] = true
		for name, digest := range map[string]string{"generation_digest": window.GenerationDigest, "manifest_digest": window.ManifestDigest, "registry_root": window.RegistryRoot} {
			if err := validatePlanningV2Digest(name, digest); err != nil {
				return err
			}
		}
		for i, acceptance := range window.Acceptances {
			if acceptance.Chapter != next+i {
				return fmt.Errorf("window aggregate acceptance coverage differs from window")
			}
			if err := validatePlanningV2Digest("acceptance_receipt_digest", acceptance.AcceptanceReceiptDigest); err != nil {
				return err
			}
		}
		next = window.LastChapter + 1
	}
	if next != receipt.LastChapter+1 || receipt.FinalGenerationID != receipt.Windows[len(receipt.Windows)-1].GenerationID {
		return fmt.Errorf("window aggregate lacks its exact final window")
	}
	for name, digest := range map[string]string{"final_outcome_receipt_digest": receipt.FinalOutcomeReceiptDigest, "final_actual_post_state_root": receipt.FinalActualPostStateRoot, "carried_obligation_root": receipt.CarriedObligationRoot, "receipt_digest": receipt.ReceiptDigest} {
		if err := validatePlanningV2Digest(name, digest); err != nil {
			return err
		}
	}
	digest, err := ComputeArcWindowAggregateCompletionDigestV1(receipt)
	if err != nil || digest != receipt.ReceiptDigest {
		return fmt.Errorf("window aggregate receipt digest mismatch: %w", err)
	}
	return nil
}

func NewArcWindowAggregateCompletionV1(windows []ArcWindowCompletionEvidenceV1, completedAt string) (ArcWindowAggregateCompletionV1, error) {
	var receipt ArcWindowAggregateCompletionV1
	if len(windows) < 2 {
		return receipt, fmt.Errorf("window aggregate requires multiple complete windows; use the original completion path for one window")
	}
	first := windows[0].Generation.DetailWindow
	if first == nil {
		return receipt, fmt.Errorf("window aggregate requires a bound whole-arc rehearsal")
	}
	// The caller's selected range is never authority for logical arc coverage.
	receipt = ArcWindowAggregateCompletionV1{Version: ArcWindowAggregateCompletionVersionV1, ArcID: first.ArcID,
		FirstChapter: first.ArcFirstChapter, LastChapter: first.ArcLastChapter, CompletedAt: completedAt}
	nextChapter := receipt.FirstChapter
	seen := map[string]bool{}
	for index, evidence := range windows {
		g, manifest := evidence.Generation, evidence.Manifest
		if err := ValidatePlanningGenerationV2(g); err != nil {
			return receipt, err
		}
		if g.Status != PlanningGenerationSealedV2 || g.DetailWindow == nil || seen[g.GenerationID] ||
			g.ScopeID != receipt.ArcID || g.DetailWindow.ArcFirstChapter != receipt.FirstChapter || g.DetailWindow.ArcLastChapter != receipt.LastChapter ||
			g.FirstProjectedChapter != nextChapter {
			return receipt, fmt.Errorf("window aggregate requires distinct contiguous sealed windows of the original full arc")
		}
		seen[g.GenerationID] = true
		if err := ValidateArcPlanningManifest(manifest); err != nil {
			return receipt, err
		}
		if manifest.GenerationID != g.GenerationID || manifest.ArcID != g.ScopeID || manifest.FirstChapter != g.FirstProjectedChapter || manifest.LastChapter != g.LastProjectedChapter ||
			manifest.BookLastChapter != g.BookHorizonChapter || manifest.FullOutlineDigest != g.StableOutlineRoot || !samePhysicalValueV2(manifest.DetailWindow, g.DetailWindow) {
			return receipt, fmt.Errorf("window aggregate manifest differs from original generation")
		}
		if err := ValidateProjectedChapterBundleChain(g, evidence.Bundles, evidence.Registry); err != nil {
			return receipt, err
		}
		if index > 0 {
			previous := windows[index-1]
			outcome := previous.Outcomes[len(previous.Outcomes)-1]
			bundle := previous.Bundles[len(previous.Bundles)-1]
			contract := bundle.ChapterPlan.CausalSimulation.ArcTransition
			predecessor := ProjectedPlanningPredecessorContractV2{Chapter: bundle.Chapter, BundleDigest: bundle.BundleDigest, ProjectedPostStateRoot: outcome.ActualPostStateRoot,
				OutgoingConsequenceID: contract.OutgoingConsequenceID, OutgoingConsequenceText: contract.OutgoingConsequenceText}
			if g.ParentGenerationID != previous.Generation.GenerationID || g.BaseCanonRoot != outcome.ActualCanonRoot || g.BaseStateRoot != outcome.ActualPostStateRoot ||
				g.DetailWindow.AcceptedOutcomeDigest != outcome.ReceiptDigest || !samePhysicalValueV2(g.DetailWindow.AcceptedPredecessor, &predecessor) ||
				manifest.Volume != previous.Manifest.Volume || manifest.Arc != previous.Manifest.Arc || g.BookHorizonChapter != previous.Generation.BookHorizonChapter {
				return receipt, fmt.Errorf("window aggregate parent or actual accepted boundary differs")
			}
			if err := validateArcWindowInheritedObligationsV1(previous, evidence); err != nil {
				return receipt, err
			}
		}
		if len(evidence.Acceptances) != g.ExpectedChapterCount || len(evidence.Outcomes) != g.ExpectedChapterCount {
			return receipt, fmt.Errorf("window aggregate lacks original chapter acceptance/outcome evidence")
		}
		binding := ArcWindowAggregateBindingV1{GenerationID: g.GenerationID, GenerationDigest: g.GenerationDigest, ManifestDigest: manifest.ManifestDigest,
			RegistryRoot: evidence.Registry.RegistryRoot, FirstChapter: g.FirstProjectedChapter, LastChapter: g.LastProjectedChapter}
		preState := g.BaseStateRoot
		consumed := map[string]bool{}
		for i, acceptance := range evidence.Acceptances {
			bundle, outcome := evidence.Bundles[i], evidence.Outcomes[i]
			if acceptance.Chapter != g.FirstProjectedChapter+i || bundle.Chapter != acceptance.Chapter || outcome.Chapter != acceptance.Chapter {
				return receipt, fmt.Errorf("window aggregate chapter sources are not in exact order")
			}
			if err := ValidateChapterAcceptanceReceiptAgainstManifest(acceptance, manifest); err != nil {
				return receipt, err
			}
			if err := ValidateActualOutcomeReceiptV2(outcome); err != nil {
				return receipt, err
			}
			if manifest.Chapters[i].BundleDigest != bundle.BundleDigest || outcome.GenerationID != g.GenerationID || acceptance.OutcomeReceiptDigest != outcome.ReceiptDigest ||
				acceptance.ChapterBodySHA256 != outcome.ChapterBodySHA256 || !outcome.ProjectionMatch || outcome.ActualPreStateRoot != preState ||
				outcome.ActualPostStateRoot != bundle.ProjectedPostStateRoot || outcome.ProjectedPostStateRoot != bundle.ProjectedPostStateRoot ||
				!samePhysicalValueV2(normalizeV2Strings(outcome.ObligationsSatisfied), normalizeV2Strings(bundle.ObligationsConsumed)) {
				return receipt, fmt.Errorf("window aggregate chapter %d actual body/state/obligation evidence differs", bundle.Chapter)
			}
			for _, id := range outcome.ObligationsSatisfied {
				consumed[id] = true
			}
			preState = outcome.ActualPostStateRoot
			binding.Acceptances = append(binding.Acceptances, ArcChapterAcceptanceBinding{Chapter: acceptance.Chapter, AcceptanceReceiptDigest: acceptance.ReceiptDigest})
		}
		for _, obligation := range evidence.Registry.Obligations {
			if obligation.State == ObligationSatisfiedV2 && !consumed[obligation.ID] {
				return receipt, fmt.Errorf("window aggregate obligation %s has no actual accepted satisfaction", obligation.ID)
			}
		}
		carried, err := ArcWindowCarriedObligationsV1(evidence.Registry)
		if err != nil || !samePhysicalValueV2(carried, append([]ArcCarriedObligation(nil), manifest.CarriedObligations...)) {
			return receipt, fmt.Errorf("window aggregate manifest lost or changed original carried obligations: %w", err)
		}
		receipt.Windows = append(receipt.Windows, binding)
		nextChapter = g.LastProjectedChapter + 1
	}
	if nextChapter != receipt.LastChapter+1 {
		return receipt, fmt.Errorf("window aggregate does not cover the entire original logical arc")
	}
	last := windows[len(windows)-1]
	outcome := last.Outcomes[len(last.Outcomes)-1]
	receipt.FinalGenerationID, receipt.FinalOutcomeReceiptDigest, receipt.FinalActualPostStateRoot = last.Generation.GenerationID, outcome.ReceiptDigest, outcome.ActualPostStateRoot
	var err error
	receipt.CarriedObligationRoot, err = ComputeArcCarriedObligationRoot(last.Manifest.CarriedObligations)
	if err != nil {
		return receipt, err
	}
	receipt.ReceiptDigest, err = ComputeArcWindowAggregateCompletionDigestV1(receipt)
	if err != nil {
		return receipt, err
	}
	return receipt, ValidateArcWindowAggregateCompletionV1(receipt)
}

func ValidateArcWindowAggregateCompletionAgainstWindowsV1(receipt ArcWindowAggregateCompletionV1, windows []ArcWindowCompletionEvidenceV1) error {
	if err := ValidateArcWindowAggregateCompletionV1(receipt); err != nil {
		return err
	}
	expected, err := NewArcWindowAggregateCompletionV1(windows, receipt.CompletedAt)
	if err != nil {
		return err
	}
	if !samePhysicalValueV2(receipt, expected) {
		return fmt.Errorf("window aggregate differs from its original accepted window evidence")
	}
	return nil
}

func validateArcWindowInheritedObligationsV1(previous, current ArcWindowCompletionEvidenceV1) error {
	initial := current.Generation
	initial.Status, initial.ProjectedChapterCount = PlanningGenerationBuildingV2, 0
	initial.ChainHeadRoot, initial.ChainTailRoot, initial.SealedAt = "", "", ""
	initial.GenerationDigest, _ = ComputePlanningGenerationV2Digest(initial)
	_, inherited, err := CarryForwardArcObligationsV2(previous.Generation, previous.Registry, initial)
	if err != nil {
		return err
	}
	byID := map[string]ObligationV2{}
	for _, obligation := range current.Registry.Obligations {
		byID[obligation.ID] = obligation
	}
	for _, original := range inherited.Obligations {
		actual, exists := byID[original.ID]
		if !exists || actual.Kind != original.Kind || actual.Contract != original.Contract || actual.Hardness != original.Hardness ||
			!samePhysicalValueV2(actual.Origin, original.Origin) || !samePhysicalValueV2(actual.DueWindow, original.DueWindow) || !samePhysicalValueV2(actual.ConsumerChapters, original.ConsumerChapters) {
			return fmt.Errorf("window aggregate changed or dropped original obligation %s", original.ID)
		}
	}
	return nil
}

// ArcWindowCarriedObligationsV1 derives the manifest's existing carry format
// from the original registry, including its complete obligation digest.
func ArcWindowCarriedObligationsV1(registry ObligationRegistryV2) ([]ArcCarriedObligation, error) {
	var carried []ArcCarriedObligation
	for _, obligation := range registry.Obligations {
		if obligation.State != ObligationOpenV2 && obligation.State != ObligationPlannedV2 {
			continue
		}
		digest, err := planningV2Digest(obligation)
		if err != nil {
			return nil, err
		}
		carried = append(carried, ArcCarriedObligation{ObligationID: obligation.ID, OriginChapter: obligation.Origin.Chapter, DueChapter: obligation.DueWindow.ToChapter, ObligationDigest: digest})
	}
	sort.Slice(carried, func(i, j int) bool { return carried[i].ObligationID < carried[j].ObligationID })
	return carried, nil
}
