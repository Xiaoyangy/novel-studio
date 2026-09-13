package main

import (
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

// A runtime callback, never model data or serialized CLI configuration.
type pipelineProviderCallGuard struct {
	check func() error
	mu    sync.Mutex
	err   error
}

func (g *pipelineProviderCallGuard) Check() error {
	if g == nil {
		return nil
	}
	g.mu.Lock()
	previous := g.err
	g.mu.Unlock()
	if previous != nil {
		return previous
	}
	if err := g.check(); err != nil {
		g.mu.Lock()
		if g.err == nil {
			g.err = err
		}
		g.mu.Unlock()
		return err
	}
	return nil
}

func (g *pipelineProviderCallGuard) Err() error {
	if g == nil {
		return nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.err
}

func pipelineGenerationDeliveryGuard(st *store.Store, generation domain.PlanningGenerationV2, requiredChapters ...int) *pipelineProviderCallGuard {
	if generation.ChapterDeliveryBudget == nil {
		return nil
	}
	chapters := append([]int(nil), requiredChapters...)
	return &pipelineProviderCallGuard{check: func() error {
		for _, chapter := range chapters {
			if err := requirePipelineChapterDeliveryStarted(st, generation, chapter); err != nil {
				return err
			}
		}
		return st.CheckChapterDeliveryBudgetNow(generation.GenerationID)
	}}
}

// Render and dependent calls must consume the original detail start, never
// create it. The generation-wide guard also serves new, only-armed planning.
func requirePipelineChapterDeliveryStarted(st *store.Store, generation domain.PlanningGenerationV2, chapter int) error {
	if generation.ChapterDeliveryBudget == nil {
		return nil
	}
	if st == nil || chapter < generation.FirstProjectedChapter || chapter > generation.LastProjectedChapter {
		return fmt.Errorf("chapter delivery start requires the exact generation chapter")
	}
	timing, err := st.LoadChapterDeliveryTiming(generation.GenerationID, chapter)
	if err != nil {
		return err
	}
	if timing == nil || timing.StartedAt.IsZero() || timing.DeadlineAt.IsZero() || timing.GenerationID != generation.GenerationID || timing.Chapter != chapter || timing.Policy != generation.ChapterDeliveryBudget.Policy || timing.LimitSeconds != generation.ChapterDeliveryBudget.LimitSeconds {
		return fmt.Errorf("chapter %d delivery budget lacks its original first-detail start; cannot dispatch render or backfill timing", chapter)
	}
	return nil
}

func resolvePipelineChapterDeliveryBudget(seconds int, existing *domain.PlanningGenerationV2) (*domain.ChapterDeliveryBudgetV1, error) {
	if seconds < 0 || seconds > 86400 {
		return nil, fmt.Errorf("chapter delivery seconds must be 0 or 1..86400")
	}
	if existing != nil {
		if existing.ChapterDeliveryBudget == nil {
			if seconds != 0 {
				return nil, fmt.Errorf("existing generation has no frozen chapter delivery budget; cannot retrofit or invent its first dispatch time")
			}
			return nil, nil
		}
		budget := *existing.ChapterDeliveryBudget
		if err := domain.ValidateChapterDeliveryBudgetV1(&budget); err != nil {
			return nil, err
		}
		if seconds != 0 && seconds != budget.LimitSeconds {
			return nil, fmt.Errorf("chapter delivery limit differs from its frozen generation")
		}
		return &budget, nil
	}
	if seconds == 0 {
		return nil, nil
	}
	return &domain.ChapterDeliveryBudgetV1{Policy: domain.ChapterDeliveryBudgetPolicyV1, LimitSeconds: seconds}, nil
}

func samePipelineChapterDeliveryBudget(left, right *domain.ChapterDeliveryBudgetV1) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func pipelineChapterDeliveryDependencyRoot(root string, budget *domain.ChapterDeliveryBudgetV1) string {
	if budget == nil {
		return root
	}
	return pipelineProjectAllDigest(struct {
		Base   string
		Budget domain.ChapterDeliveryBudgetV1
	}{root, *budget})
}

func reportPipelineChapterDeliveryTiming(timing *store.ChapterDeliveryTimingV1) {
	if timing == nil || timing.ClosedAt.IsZero() {
		return
	}
	met := !timing.TimingUnknown && !timing.Recovered && timing.ClosedAt.Before(timing.DeadlineAt)
	fmt.Fprintf(os.Stderr, "[pipeline:chapter-delivery] chapter=%d elapsed=%s limit_seconds=%d timing_unknown=%t within_budget=%t acceptance=%s\n", timing.Chapter, timing.ClosedAt.Sub(timing.StartedAt).Round(time.Millisecond), timing.LimitSeconds, timing.TimingUnknown, met, timing.AcceptanceReceiptDigest)
}
