package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/errs"
)

// ResumePartial is a host dispatch convenience, not a new planning capability.
// It uses the ordinary tool and reviewer, once, without resubmitting paid fields.
// The supplied packet must be the successful current ContextTool response.
func (t *PlanDetailsTool) ResumePartial(ctx context.Context, chapter int, packet json.RawMessage) (json.RawMessage, bool, error) {
	var view struct {
		Task struct {
			Mode    string `json:"mode"`
			Chapter int    `json:"chapter"`
		} `json:"active_chapter_task"`
		SourceStatus string `json:"structure_source_status"`
		Access       struct {
			Token string `json:"source_token"`
		} `json:"planning_context_access_receipt"`
	}
	if err := json.Unmarshal(packet, &view); err != nil {
		return nil, false, err
	}
	if view.Task.Mode != "staged_plan_repair" {
		return nil, false, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, true, err
	}
	if view.Task.Chapter != chapter || view.SourceStatus != "ready" {
		return nil, true, fmt.Errorf("partial resume requires the current bound plan structure: %w", errs.ErrToolPrecondition)
	}
	// Check the freshly served token against server state before spending a
	// grounding call. Execute still performs all normal checks and consumes it.
	if err := t.validatePartialResumeAccess(chapter, view.Access.Token); err != nil {
		return nil, true, err
	}
	args, err := json.Marshal(map[string]any{"chapter": chapter, "finalize": true, "causal_simulation": map[string]any{"context_sources": []string{view.Access.Token}}})
	if err != nil {
		return nil, true, err
	}
	result, err := t.Execute(ctx, args)
	return result, true, err
}

func (t *PlanDetailsTool) validatePartialResumeAccess(chapter int, token string) error {
	state, _, err := loadProjectAllStateForExecution(t.store, chapter)
	if err != nil {
		return err
	}
	lock, err := t.store.Runtime.LoadPipelineExecution()
	if err != nil {
		return err
	}
	receipt, err := t.store.Runtime.LoadPlanningContextAccessReceipt(domain.PlanningContextAccessPlan)
	if err != nil {
		return err
	}
	if state == nil || lock == nil || receipt == nil {
		return fmt.Errorf("partial resume requires fresh planning context access: %w", errs.ErrToolPrecondition)
	}
	if err := requireCurrentPipelineExecutionProcess(lock, "partial plan resume"); err != nil {
		return err
	}
	if receipt.GenerationID != state.GenerationID || receipt.Chapter != chapter || receipt.Profile != "planning" || receipt.Phase != domain.PlanningContextAccessPlan || receipt.PlanningContextDigest != state.ContextDigest || receipt.LockMode != lock.Mode || receipt.LockOwner != strings.TrimSpace(lock.Owner) || receipt.LockProcessID != lock.ProcessID || !receipt.LockAcquiredAt.Equal(lock.AcquiredAt) || !receipt.ExpiresAt.Equal(lock.ExpiresAt) || !receipt.ConsumedAt.IsZero() || !time.Now().Before(receipt.ExpiresAt) {
		return fmt.Errorf("partial resume context access has stale execution identity: %w", errs.ErrToolPrecondition)
	}
	_, err = extractPlanningContextAccessToken([]string{token}, receipt.TokenSHA256)
	return err
}

type incompletePlanDetailsError struct{ cause error }

func (e *incompletePlanDetailsError) Error() string { return e.cause.Error() }
func (e *incompletePlanDetailsError) Unwrap() error { return e.cause }

// PlanDetailsResumeNeedsRepair deliberately excludes generic preconditions:
// budget, provider, source-integrity and storage errors must not start a Planner.
func PlanDetailsResumeNeedsRepair(err error) bool {
	var incomplete *incompletePlanDetailsError
	var rejected *planGroundingRejectionError
	return errors.As(err, &incomplete) || errors.As(err, &rejected)
}
