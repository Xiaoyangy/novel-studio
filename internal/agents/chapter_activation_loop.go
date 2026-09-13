package agents

import (
	"context"
	"fmt"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

// ChapterActivationDriver separates actual world execution from the Planner's
// readiness assessment. Runtime adapters must return complete immutable proof;
// the controller never fabricates choices, physical changes, or assessments.
type ChapterActivationDriver struct {
	VerifyExecutionSources bool
	ExecuteCycle           func(context.Context, domain.CharacterActivationSession) (domain.CharacterActivationCycle, error)
	AssessCycle            func(context.Context, domain.CharacterActivationSession, domain.CharacterActivationCycle) (domain.CharacterChapterReadiness, error)
	BeforeDispatch         func(context.Context, string) error
}

type ChapterActivationLimitError struct{ Chapter, Cycles int }

func (e *ChapterActivationLimitError) Error() string {
	return fmt.Sprintf("chapter %d reached %d activation cycles without readiness; evidence retained, chapter is not complete", e.Chapter, e.Cycles)
}

// RunChapterActivationLoop is resumable at both expensive boundaries: a paid
// world cycle and its readiness assessment. It is deliberately not wired to
// the production planner until the world-cycle adapters and bundle aggregation
// are complete. The old one-round protocol must not silently pretend to loop.
func RunChapterActivationLoop(ctx context.Context, st *store.Store, baseline domain.CharacterActivationSession, driver ChapterActivationDriver) (*domain.CharacterActivationSession, error) {
	if st == nil || driver.ExecuteCycle == nil || driver.AssessCycle == nil {
		return nil, fmt.Errorf("chapter activation driver is incomplete")
	}
	if err := domain.ValidateCharacterActivationSession(baseline); err != nil {
		return nil, err
	}
	if len(baseline.CycleDigests) != 0 || baseline.Phase != "collecting" {
		return nil, fmt.Errorf("activation runner requires the original empty chapter baseline")
	}
	var result *domain.CharacterActivationSession
	recoverSession := st.RecoverCharacterActivationSession
	appendCycle := st.AppendCharacterActivationCycle
	applyReadiness := st.ApplyCharacterChapterReadiness
	if driver.VerifyExecutionSources {
		recoverSession = st.RecoverVerifiedCharacterActivationSession
		appendCycle = st.AppendVerifiedCharacterActivationCycle
		applyReadiness = st.ApplyVerifiedCharacterChapterReadiness
	}
	err := st.WithCharacterActivationExecution(ctx, baseline.GenerationID, baseline.Chapter, func() error {
		current, err := recoverSession(baseline.GenerationID, baseline.Chapter)
		if err != nil {
			return err
		}
		if current == nil {
			if err := st.CreateCharacterActivationSession(baseline); err != nil {
				return err
			}
			copy := baseline
			current = &copy
		}
		if current.ChapterContextDigest != baseline.ChapterContextDigest || current.InitialPhysicalRoot != baseline.InitialPhysicalRoot || current.InitialDay != baseline.InitialDay || current.MaxCycles != baseline.MaxCycles {
			return fmt.Errorf("activation runner baseline/limits differ from its persisted session")
		}
		for {
			result = current
			if err := ctx.Err(); err != nil {
				return err
			}
			switch current.Phase {
			case "ready", "hard_conflict":
				return nil
			case "collecting":
				if len(current.CycleDigests) >= current.MaxCycles {
					return &ChapterActivationLimitError{Chapter: current.Chapter, Cycles: len(current.CycleDigests)}
				}
				if driver.BeforeDispatch != nil {
					if err := driver.BeforeDispatch(ctx, "character_activation"); err != nil {
						return err
					}
				}
				if err := ctx.Err(); err != nil {
					return err
				}
				cycle, err := driver.ExecuteCycle(ctx, *current)
				if err != nil {
					return err
				}
				// Preserve a completed paid result even if cancellation arrived
				// while it was returning. No next model runs after that cancel.
				current, err = appendCycle(current.Digest, cycle)
				if err != nil {
					return err
				}
				reportDurablePlanningProgress(ctx, DurablePlanningProgress{GenerationID: cycle.GenerationID, Chapter: cycle.Chapter, Cycle: cycle.Index, Kind: PlanningCycleCommitted, ArtifactDigest: cycle.Digest})
			case "assessing":
				var cycle *domain.CharacterActivationCycle
				var err error
				if driver.VerifyExecutionSources {
					prefix, loadErr := st.LoadVerifiedCharacterActivationPrefix(current.GenerationID, current.Chapter)
					err = loadErr
					if err == nil && prefix != nil && prefix.Session().Digest == current.Digest {
						// Source verification above covers the complete prefix; this
						// assessment only needs its final detached cycle.
						if step, ok := prefix.Step(len(current.CycleDigests) - 1); ok {
							value := step.Cycle()
							cycle = &value
						}
					}
				} else {
					cycle, err = st.LoadCharacterActivationCycle(current.GenerationID, current.Chapter, len(current.CycleDigests))
				}
				if err != nil {
					return err
				}
				if cycle == nil {
					return fmt.Errorf("pending activation cycle evidence is missing")
				}
				if driver.BeforeDispatch != nil {
					if err := driver.BeforeDispatch(ctx, "chapter_readiness"); err != nil {
						return err
					}
				}
				if err := ctx.Err(); err != nil {
					return err
				}
				readiness, err := driver.AssessCycle(ctx, *current, *cycle)
				if err != nil {
					return err
				}
				current, err = applyReadiness(current.Digest, readiness)
				if err != nil {
					return err
				}
				// Same ID as the audit notification: recovering its cursor cannot
				// manufacture fresh progress by rereading an already paid verdict.
				reportDurablePlanningProgress(ctx, DurablePlanningProgress{GenerationID: cycle.GenerationID, Chapter: cycle.Chapter, Cycle: cycle.Index, Kind: PlanningReadinessCommitted, ArtifactDigest: readiness.Digest})
			default:
				return fmt.Errorf("unsupported activation session phase")
			}
		}
	})
	return result, err
}
