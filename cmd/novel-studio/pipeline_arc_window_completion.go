package main

import (
	"fmt"
	"slices"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

// This is only an in-memory view of a verified proof. Serialization retains
// the real kind and original receipt; it never casts an aggregate to a v1
// single-window receipt or persists the flattened convenience fields.
type pipelineArcCompletionProof struct {
	Kind                      string                                 `json:"kind"`
	SingleWindow              *domain.ArcCompletionReceipt           `json:"single_window,omitempty"`
	WindowAggregate           *domain.ArcWindowAggregateCompletionV1 `json:"window_aggregate,omitempty"`
	ReceiptDigest             string                                 `json:"-"`
	FirstChapter              int                                    `json:"-"`
	LastChapter               int                                    `json:"-"`
	Acceptances               []domain.ArcChapterAcceptanceBinding   `json:"-"`
	FinalOutcomeReceiptDigest string                                 `json:"-"`
	FinalActualPostStateRoot  string                                 `json:"-"`
}

func pipelineSingleWindowCompletionProof(receipt *domain.ArcCompletionReceipt) *pipelineArcCompletionProof {
	return &pipelineArcCompletionProof{Kind: receipt.Version, SingleWindow: receipt, ReceiptDigest: receipt.ReceiptDigest,
		FirstChapter: receipt.FirstChapter, LastChapter: receipt.LastChapter, Acceptances: receipt.Acceptances,
		FinalOutcomeReceiptDigest: receipt.FinalOutcomeReceiptDigest, FinalActualPostStateRoot: receipt.FinalActualPostStateRoot}
}

func pipelineWindowAggregateCompletionProof(receipt *domain.ArcWindowAggregateCompletionV1) *pipelineArcCompletionProof {
	proof := &pipelineArcCompletionProof{Kind: receipt.Version, WindowAggregate: receipt, ReceiptDigest: receipt.ReceiptDigest,
		FirstChapter: receipt.FirstChapter, LastChapter: receipt.LastChapter,
		FinalOutcomeReceiptDigest: receipt.FinalOutcomeReceiptDigest, FinalActualPostStateRoot: receipt.FinalActualPostStateRoot}
	for _, window := range receipt.Windows {
		proof.Acceptances = append(proof.Acceptances, window.Acceptances...)
	}
	return proof
}

func (proof *pipelineArcCompletionProof) description() string {
	if proof.WindowAggregate != nil {
		return proof.Kind + " " + proof.ReceiptDigest
	}
	return proof.ReceiptDigest // Preserve the legacy single-window message.
}

func pipelineArcRequiresWindowAggregate(generation *domain.PlanningGenerationV2) bool {
	return generation != nil && generation.DetailWindow != nil &&
		(generation.FirstProjectedChapter != generation.DetailWindow.ArcFirstChapter || generation.LastProjectedChapter != generation.DetailWindow.ArcLastChapter)
}

func completePipelineArcCycle(st *store.Store, generation *domain.PlanningGenerationV2, cursor *domain.RealizationCursorV2) (*pipelineArcCompletionProof, error) {
	if !pipelineArcRequiresWindowAggregate(generation) {
		receipt, err := completePipelineSingleWindowArcCycle(st, generation, cursor)
		if err != nil {
			return nil, err
		}
		return pipelineSingleWindowCompletionProof(receipt), nil
	}
	if st == nil || cursor == nil || cursor.ActiveGenerationID != generation.GenerationID || cursor.ActivePromotedChapter != 0 ||
		cursor.LastAcceptedChapter != generation.LastProjectedChapter || cursor.NextPromoteChapter != generation.LastProjectedChapter+1 || len(cursor.BlockedByRewrites) != 0 {
		return nil, fmt.Errorf("whole-arc window completion requires the exact fully accepted final-window cursor")
	}
	ids, lastOutcome, err := validatePipelineArcWindowCompletionSources(st, generation)
	if err != nil {
		return nil, err
	}
	if cursor.LastOutcomeReceiptDigest != lastOutcome.ReceiptDigest {
		return nil, fmt.Errorf("whole-arc window completion final outcome differs from realization cursor")
	}
	// Replays may only reuse the exact existing original proof. In particular,
	// ambiguous or damaged evidence is never repaired by writing another file.
	existing, err := st.LoadVerifiedWindowedArcCompletionForGenerationV1(generation.GenerationID)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		published, publishErr := st.CompleteWindowedArcV1(ids, lastOutcome.AcceptedAt)
		if publishErr != nil {
			return nil, publishErr
		}
		existing, err = st.LoadVerifiedWindowedArcCompletionForGenerationV1(generation.GenerationID)
		if err != nil {
			return nil, err
		}
		if existing == nil || existing.ReceiptDigest != published.ReceiptDigest {
			return nil, fmt.Errorf("published whole-arc completion is not the unique original proof")
		}
	}
	if existing.FinalOutcomeReceiptDigest != lastOutcome.ReceiptDigest || existing.FinalActualPostStateRoot != lastOutcome.ActualPostStateRoot {
		return nil, fmt.Errorf("whole-arc aggregate does not bind the exact final actual outcome")
	}
	return pipelineWindowAggregateCompletionProof(existing), nil
}

func requirePipelineArcCompletion(st *store.Store, generation *domain.PlanningGenerationV2) (*pipelineArcCompletionProof, error) {
	if !pipelineArcRequiresWindowAggregate(generation) {
		receipt, err := requirePipelineSingleWindowArcCompletion(st, generation)
		if err != nil {
			return nil, err
		}
		return pipelineSingleWindowCompletionProof(receipt), nil
	}
	if st == nil {
		return nil, fmt.Errorf("window aggregate completion validation requires store")
	}
	_, outcome, err := validatePipelineArcWindowCompletionSources(st, generation)
	if err != nil {
		return nil, err
	}
	receipt, err := st.LoadVerifiedWindowedArcCompletionForGenerationV1(generation.GenerationID)
	if err != nil {
		return nil, err
	}
	if receipt == nil {
		return nil, fmt.Errorf("generation %s requires its original whole-arc window aggregate completion", generation.GenerationID)
	}
	if receipt.FinalOutcomeReceiptDigest != outcome.ReceiptDigest || receipt.FinalActualPostStateRoot != outcome.ActualPostStateRoot {
		return nil, fmt.Errorf("window aggregate completion differs from final accepted outcome/root")
	}
	return pipelineWindowAggregateCompletionProof(receipt), nil
}

func validatePipelineArcWindowCompletionSources(st *store.Store, generation *domain.PlanningGenerationV2) ([]string, *domain.ActualOutcomeReceiptV2, error) {
	if st == nil || generation == nil || generation.DetailWindow == nil || generation.LastProjectedChapter != generation.DetailWindow.ArcLastChapter {
		return nil, nil, fmt.Errorf("an intermediate detail window cannot complete or unlock the whole logical arc")
	}
	var reverse []*domain.PlanningGenerationV2
	seen := map[string]bool{}
	for id := generation.GenerationID; ; {
		if seen[id] {
			return nil, nil, fmt.Errorf("whole-arc window parent chain cycles")
		}
		seen[id] = true
		original, err := st.ProjectedV2().LoadSealedGeneration(id)
		if err != nil {
			return nil, nil, err
		}
		if original == nil || original.DetailWindow == nil || original.ScopeID != generation.ScopeID || original.DetailWindow.ArcFirstChapter != generation.DetailWindow.ArcFirstChapter ||
			original.DetailWindow.ArcLastChapter != generation.DetailWindow.ArcLastChapter || original.BookHorizonChapter != generation.BookHorizonChapter {
			return nil, nil, fmt.Errorf("whole-arc window parent chain changed its original scope")
		}
		if len(reverse) == 0 && original.GenerationDigest != generation.GenerationDigest {
			return nil, nil, fmt.Errorf("whole-arc window completion received a non-original final generation")
		}
		if len(reverse) > 0 && original.LastProjectedChapter != reverse[len(reverse)-1].FirstProjectedChapter-1 {
			return nil, nil, fmt.Errorf("whole-arc window parent chain has a gap or overlap")
		}
		reverse = append(reverse, original)
		if original.FirstProjectedChapter == original.DetailWindow.ArcFirstChapter {
			break
		}
		id = original.ParentGenerationID
	}
	slices.Reverse(reverse)
	var ids []string
	var final *domain.ActualOutcomeReceiptV2
	for _, original := range reverse {
		if _, err := requirePipelineArcPlanningManifest(st, original); err != nil {
			return nil, nil, err
		}
		acceptances, err := st.ArcCycle().ListChapterAcceptanceReceipts(original.GenerationID)
		if err != nil {
			return nil, nil, err
		}
		outcomes, err := validatePipelineArcOutcomeChain(st, original, acceptances)
		if err != nil {
			return nil, nil, err
		}
		if len(outcomes) != original.ExpectedChapterCount {
			return nil, nil, fmt.Errorf("window %s lacks every exact original accepted outcome", original.GenerationID)
		}
		last := outcomes[len(outcomes)-1]
		final = &last
		ids = append(ids, original.GenerationID)
	}
	return ids, final, nil
}
