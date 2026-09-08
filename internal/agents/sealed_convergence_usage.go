package agents

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/tools"
	"github.com/voocel/agentcore"
)

// SealedConvergenceUsageAccounting is a runtime-only capability supplied by
// the validated live controller. It never enters the model's prompt/schema.
type SealedConvergenceUsageAccounting struct {
	ExecutionOutputDir string
	Decorate           bootstrap.ModelAttemptDecorator
	RecordUsage        UsageRecorder
}
type sealedConvergenceUsageKey struct{}

func WithSealedConvergenceUsageAccounting(ctx context.Context, accounting SealedConvergenceUsageAccounting) context.Context {
	return context.WithValue(ctx, sealedConvergenceUsageKey{}, accounting)
}

func bindSealedConvergenceUsage(ctx context.Context, outputDir string, models *bootstrap.ModelSet) (SealedConvergenceUsageAccounting, error) {
	if ctx == nil {
		return SealedConvergenceUsageAccounting{}, fmt.Errorf("sealed convergence usage context missing")
	}
	a, ok := ctx.Value(sealedConvergenceUsageKey{}).(SealedConvergenceUsageAccounting)
	if !ok || a.Decorate == nil || a.RecordUsage == nil || a.ExecutionOutputDir == "" || filepath.Clean(a.ExecutionOutputDir) != filepath.Clean(outputDir) {
		return a, fmt.Errorf("sealed convergence model dispatch requires exact parent-bound live usage accounting")
	}
	models.SetAttemptDecorator(a.Decorate)
	return a, nil
}

// A restricted tool may bind a reviewer to its existing inner PlanDetails,
// but must retain the exact allowlist, call ceiling and metadata externally.
func bindSealedConvergenceGrounding(tool agentcore.Tool, reviewer tools.PlanGroundingReviewer) (agentcore.Tool, error) {
	if details, ok := tool.(*tools.PlanDetailsTool); ok {
		return details.WithGroundingReviewer(reviewer), nil
	}
	if restricted, ok := tool.(interface {
		WithPlanGroundingReviewer(tools.PlanGroundingReviewer) (agentcore.Tool, error)
	}); ok {
		return restricted.WithPlanGroundingReviewer(reviewer)
	}
	return nil, fmt.Errorf("sealed convergence restricted plan_details cannot bind grounding without losing its boundary")
}
