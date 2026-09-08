package domain

import (
	"fmt"
	"strings"
)

const PlanningDetailWindowVersionV1 = "planning-detail-window.v1"
const PlanningDetailWindowChapterLimitV1 = 3

// PlanningDetailWindowV1 separates a complete formal planning transaction
// from the logical arc covered by its immutable speculative rehearsal.
// Absence preserves the historical whole-arc generation contract.
type PlanningDetailWindowV1 struct {
	Version               string                                  `json:"version"`
	ArcID                 string                                  `json:"arc_id"`
	ArcFirstChapter       int                                     `json:"arc_first_chapter"`
	ArcLastChapter        int                                     `json:"arc_last_chapter"`
	RehearsalDigest       string                                  `json:"rehearsal_digest"`
	RehearsalInputDigest  string                                  `json:"rehearsal_input_digest"`
	AcceptedOutcomeDigest string                                  `json:"accepted_outcome_digest,omitempty"`
	AcceptedPredecessor   *ProjectedPlanningPredecessorContractV2 `json:"accepted_predecessor,omitempty"`
}

func PlanningDetailWindowRangeV1(acceptedThrough, arcFirst, arcLast int) (int, int, error) {
	if arcFirst <= 0 || arcLast < arcFirst || acceptedThrough < arcFirst-1 || acceptedThrough >= arcLast {
		return 0, 0, fmt.Errorf("detail window requires an accepted prefix inside the logical arc")
	}
	first := acceptedThrough + 1
	last := first + min(PlanningDetailWindowChapterLimitV1-1, arcLast-first)
	return first, last, nil
}

func validatePlanningDetailWindowShapeV1(window PlanningDetailWindowV1) error {
	if window.Version != PlanningDetailWindowVersionV1 || window.ArcID == "" || strings.TrimSpace(window.ArcID) != window.ArcID ||
		window.ArcFirstChapter <= 0 || window.ArcLastChapter < window.ArcFirstChapter {
		return fmt.Errorf("detail window has an invalid version or logical arc identity/range")
	}
	if err := validatePlanningV2Digest("detail window rehearsal_digest", window.RehearsalDigest); err != nil {
		return err
	}
	if err := validatePlanningV2Digest("detail window rehearsal_input_digest", window.RehearsalInputDigest); err != nil {
		return err
	}
	if (window.AcceptedPredecessor == nil) != (window.AcceptedOutcomeDigest == "") {
		return fmt.Errorf("detail window accepted predecessor requires its exact outcome receipt")
	}
	if predecessor := window.AcceptedPredecessor; predecessor != nil {
		if predecessor.Chapter < window.ArcFirstChapter || predecessor.Chapter >= window.ArcLastChapter ||
			strings.TrimSpace(predecessor.OutgoingConsequenceID) == "" || strings.TrimSpace(predecessor.OutgoingConsequenceText) == "" ||
			predecessor.OutgoingConsequenceID != strings.TrimSpace(predecessor.OutgoingConsequenceID) ||
			predecessor.OutgoingConsequenceText != strings.TrimSpace(predecessor.OutgoingConsequenceText) {
			return fmt.Errorf("detail window accepted predecessor has no exact in-arc outgoing consequence")
		}
		for name, digest := range map[string]string{
			"accepted_outcome_digest":     window.AcceptedOutcomeDigest,
			"predecessor_bundle_digest":   predecessor.BundleDigest,
			"predecessor_post_state_root": predecessor.ProjectedPostStateRoot,
		} {
			if err := validatePlanningV2Digest(name, digest); err != nil {
				return err
			}
		}
	}
	return nil
}

func ValidatePlanningDetailWindowV1(generation PlanningGenerationV2) error {
	window := generation.DetailWindow
	if window == nil {
		return nil
	}
	if err := validatePlanningDetailWindowShapeV1(*window); err != nil {
		return err
	}
	if !IsArcPlanningGenerationV2(generation) || generation.ScopeID != window.ArcID || generation.BookHorizonChapter < window.ArcLastChapter {
		return fmt.Errorf("detail window differs from the logical arc scope or book horizon")
	}
	first, last, err := PlanningDetailWindowRangeV1(generation.BaseCanonChapter, window.ArcFirstChapter, window.ArcLastChapter)
	if err != nil {
		return err
	}
	if generation.FirstProjectedChapter != first || generation.LastProjectedChapter != last || generation.ExpectedChapterCount != last-first+1 {
		return fmt.Errorf("detail window must cover exactly chapters %d..%d after accepted chapter %d", first, last, generation.BaseCanonChapter)
	}
	if first == window.ArcFirstChapter {
		if window.AcceptedPredecessor != nil {
			return fmt.Errorf("logical arc opening cannot claim an in-arc accepted predecessor")
		}
		return nil
	}
	predecessor := window.AcceptedPredecessor
	if generation.ParentGenerationID == "" || predecessor == nil || predecessor.Chapter != generation.BaseCanonChapter || predecessor.ProjectedPostStateRoot != generation.BaseStateRoot {
		return fmt.Errorf("detail window must bind the immediately preceding accepted chapter and actual state")
	}
	return nil
}

// The CLI uses this before deriving the new generation ID. An updated coarse
// rehearsal or accepted predecessor therefore selects a new immutable attempt.
func PlanningDetailWindowDependencyRootV1(baseDependencyRoot string, window PlanningDetailWindowV1) (string, error) {
	if err := validatePlanningV2Digest("base planning dependency root", baseDependencyRoot); err != nil {
		return "", err
	}
	if err := validatePlanningDetailWindowShapeV1(window); err != nil {
		return "", err
	}
	return planningV2Digest(struct {
		BaseDependencyRoot string                 `json:"base_dependency_root"`
		DetailWindow       PlanningDetailWindowV1 `json:"detail_window"`
	}{baseDependencyRoot, window})
}

func ComputeArcRehearsalSourceRootV1(foundationRoot, ragRoot string) (string, error) {
	if err := validatePlanningV2Digest("rehearsal foundation_root", foundationRoot); err != nil {
		return "", err
	}
	if err := validatePlanningV2Digest("rehearsal rag_root", ragRoot); err != nil {
		return "", err
	}
	return planningV2Digest(struct {
		Version        string `json:"version"`
		FoundationRoot string `json:"foundation_root"`
		RAGRoot        string `json:"rag_root"`
	}{"arc-rehearsal-source.v1", foundationRoot, ragRoot})
}

func samePlanningDetailWindowV1(left, right *PlanningDetailWindowV1) bool {
	return samePhysicalValueV2(left, right)
}

func sameLogicalArcDetailWindowsV1(previous, next PlanningGenerationV2) bool {
	left, right := previous.DetailWindow, next.DetailWindow
	return left != nil && right != nil && left.ArcID == right.ArcID &&
		left.ArcFirstChapter == right.ArcFirstChapter && left.ArcLastChapter == right.ArcLastChapter &&
		previous.LastProjectedChapter < left.ArcLastChapter && next.FirstProjectedChapter == previous.LastProjectedChapter+1 &&
		right.AcceptedPredecessor != nil && right.AcceptedPredecessor.Chapter == previous.LastProjectedChapter &&
		right.AcceptedPredecessor.BundleDigest == previous.ChainTailRoot
}

func validateArcManifestDetailWindowV1(manifest ArcPlanningManifest) error {
	window := manifest.DetailWindow
	if window == nil {
		return nil
	}
	if err := validatePlanningDetailWindowShapeV1(*window); err != nil {
		return err
	}
	first, last, err := PlanningDetailWindowRangeV1(manifest.FirstChapter-1, window.ArcFirstChapter, window.ArcLastChapter)
	if err != nil || first != manifest.FirstChapter || last != manifest.LastChapter || manifest.ArcID != window.ArcID || manifest.BookLastChapter < window.ArcLastChapter {
		return fmt.Errorf("arc planning manifest does not cover its exact formal detail window")
	}
	if first == window.ArcFirstChapter {
		if window.AcceptedPredecessor != nil {
			return fmt.Errorf("logical arc opening manifest cannot claim an in-arc predecessor")
		}
	} else if window.AcceptedPredecessor == nil || window.AcceptedPredecessor.Chapter != first-1 {
		return fmt.Errorf("detail window manifest lacks its immediately preceding accepted chapter")
	}
	return nil
}

func validatePlanningDetailWindowContextV1(context ProjectedPlanningContextV2) error {
	window := context.DetailWindow
	if window == nil {
		return nil
	}
	if err := validatePlanningDetailWindowShapeV1(*window); err != nil {
		return err
	}
	base := window.ArcFirstChapter - 1
	if window.AcceptedPredecessor != nil {
		base = window.AcceptedPredecessor.Chapter
	}
	first, last, err := PlanningDetailWindowRangeV1(base, window.ArcFirstChapter, window.ArcLastChapter)
	if err != nil || context.NextChapter < first || context.NextChapter > last {
		return fmt.Errorf("projected planning context is outside its bound detail window")
	}
	if context.NextChapter == first && window.AcceptedPredecessor != nil && !planningDetailWindowHasAcceptedPredecessorV1(context) {
		return fmt.Errorf("projected planning context lost its source-bound accepted predecessor")
	}
	return nil
}

func planningDetailWindowHasAcceptedPredecessorV1(context ProjectedPlanningContextV2) bool {
	window := context.DetailWindow
	return window != nil && window.AcceptedPredecessor != nil && context.NextChapter == window.AcceptedPredecessor.Chapter+1 &&
		window.AcceptedOutcomeDigest != "" && samePhysicalValueV2(context.PredecessorContract, window.AcceptedPredecessor)
}
