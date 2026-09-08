package agents

import (
	"context"
	"fmt"
	"time"

	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

type CharacterActivationNoEventsError struct{ Chapter, Cycle int }

func (e *CharacterActivationNoEventsError) Error() string {
	return fmt.Sprintf("chapter %d cycle %d has no newly perceived event; cannot spin agents or pretend the chapter is ready", e.Chapter, e.Cycle)
}

type CharacterActivationChapterConflictError struct {
	GenerationID    string
	Chapter         int
	ReadinessDigest string
	Conflicts       []string
}

func (e *CharacterActivationChapterConflictError) Error() string {
	return fmt.Sprintf("chapter %d has an audited hard-contract conflict (%s); an Architect successor is required before planning", e.Chapter, e.ReadinessDigest)
}

// A complete chapter-level execution adapter. Publication to ordinary Planner
// and sealed bundles remains an explicit next boundary: this function returns
// projected, fully audited evidence, never a fabricated aggregate arbitration.
func runCharacterActivationChapter(ctx context.Context, cfg bootstrap.Config, st *store.Store, models *bootstrap.ModelSet, generation string, chapter int, boundary ProjectedArcBoundary, projected domain.ProjectedPlanningContextV2, sources []string, maxCycles int) (*domain.CharacterActivationChapterEvidence, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if st == nil || models == nil || maxCycles < 1 || maxCycles > 64 || cfg.CharacterAgents.MaxRevisionRounds < 0 || cfg.CharacterAgents.MaxRevisionRounds > 1 {
		return nil, fmt.Errorf("chapter activation dependencies/limits are invalid")
	}
	if boundary.FrozenActivationProducer == "" {
		boundary.FrozenActivationProducer = cfg.CharacterAgents.FrozenActivationProducer
	}
	producer := CharacterActivationProtocolWithProducer(characterActivationPolicyForBoundary(boundary), boundary.FrozenActivationProducer)
	if producer == "" {
		return nil, fmt.Errorf("chapter activation has an unknown frozen producer")
	}
	if projected.Version != "" {
		if err := domain.ValidateProjectedPlanningContextV2(projected); err != nil {
			return nil, err
		}
		if projected.GenerationID != generation || projected.NextChapter != chapter {
			return nil, fmt.Errorf("chapter activation received a foreign projected context")
		}
	}
	if existing, err := st.LoadCharacterActivationChapterEvidence(generation, chapter); err != nil {
		return nil, err
	} else if existing != nil {
		if existing.ProtocolDigest != producer || existing.Session.MaxCycles != maxCycles {
			return nil, fmt.Errorf("completed chapter activation uses different frozen protocol/limits")
		}
		if existing.Context.ArcLastChapter != boundary.LastChapter || existing.Context.BookLastChapter != boundary.BookLastChapter || existing.Context.ProjectionContextDigest != projected.ContextDigest {
			return nil, fmt.Errorf("completed chapter activation belongs to a different frozen arc/book/projected context")
		}
		return existing, nil
	}
	if err := st.EnsureCharacterAgentCanon(max(0, chapter-1)); err != nil {
		return nil, err
	}
	buildOpening := buildWorldStimulus
	if domain.CharacterActivationUsesVerifiedPrefix(characterActivationPolicyForBoundary(boundary)) {
		buildOpening = buildWorldStimulusDraft
	}
	opening, err := buildOpening(st, generation, chapter, boundary, projected, sources, time.Now().UTC().Format(time.RFC3339Nano), domain.CharacterAgentDecisionProtocolV2Version)
	if err != nil {
		return nil, err
	}
	if opening.PhysicalState == nil || opening.StoryClock == nil {
		return nil, fmt.Errorf("chapter activation requires an actual opening state and clock")
	}
	if domain.CharacterActivationUsesVerifiedPrefix(characterActivationPolicyForBoundary(boundary)) {
		before := *opening.PhysicalState
		prepared, err := domain.PrepareCharacterSelfChronologyStateV1(before)
		if err != nil {
			return nil, err
		}
		if err := domain.ValidateCharacterSelfChronologyBaselineTransitionV1(before, prepared); err != nil {
			return nil, err
		}
		opening.PhysicalState = &prepared
	}
	chapterContext, err := buildCharacterReadinessContext(st, generation, chapter, boundary, projected, opening)
	if err != nil {
		return nil, err
	}
	if err := st.SaveCharacterReadinessContext(chapterContext); err != nil {
		return nil, err
	}
	baseline, err := domain.NewCharacterActivationSession(generation, chapter, chapterContext.Digest, *opening.PhysicalState, opening.StoryClock.CurrentDay, maxCycles)
	if err != nil {
		return nil, err
	}
	driver := ChapterActivationDriver{
		VerifyExecutionSources: domain.CharacterActivationUsesVerifiedPrefix(characterActivationPolicyForBoundary(boundary)),
		BeforeDispatch:         func(ctx context.Context, _ string) error { return projectedAccountingBefore(ctx) },
		ExecuteCycle: func(ctx context.Context, session domain.CharacterActivationSession) (domain.CharacterActivationCycle, error) {
			inputs, err := loadOrPrepareCharacterActivationInputs(st, session, boundary, projected, sources)
			if err != nil {
				return domain.CharacterActivationCycle{}, err
			}
			// Frozen input identifies what is now running, but is not evidence
			// that a role has actually submitted a new decision or result.
			reportDurablePlanningProgress(ctx, DurablePlanningProgress{GenerationID: session.GenerationID, Chapter: session.Chapter, Cycle: len(session.CycleDigests) + 1, Kind: PlanningContextBound, ArtifactDigest: inputs.Activation.Digest})
			if len(activeCharacterAgentIDs(inputs.Activation)) == 0 {
				return domain.CharacterActivationCycle{}, &CharacterActivationNoEventsError{Chapter: session.Chapter, Cycle: len(session.CycleDigests) + 1}
			}
			return runCharacterActivationCycle(ctx, cfg, st, models, session, inputs)
		},
		AssessCycle: func(ctx context.Context, session domain.CharacterActivationSession, cycle domain.CharacterActivationCycle) (domain.CharacterChapterReadiness, error) {
			return runCharacterChapterReadiness(ctx, cfg, st, models, session, cycle)
		},
	}
	session, err := RunChapterActivationLoop(ctx, st, baseline, driver)
	if err != nil {
		return nil, err
	}
	if session.Phase == "hard_conflict" {
		audit, err := st.LoadCharacterReadinessReviewAudit(generation, chapter, len(session.CycleDigests))
		if err != nil {
			return nil, err
		}
		if audit == nil {
			return nil, fmt.Errorf("hard-conflict session lost its readiness evidence")
		}
		return nil, &CharacterActivationChapterConflictError{GenerationID: generation, Chapter: chapter, ReadinessDigest: audit.Receipt.Digest, Conflicts: append([]string(nil), audit.Receipt.UnresolvedHardContracts...)}
	}
	return st.CollectCharacterActivationChapterEvidence(generation, chapter)
}
