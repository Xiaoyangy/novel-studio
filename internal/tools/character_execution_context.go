package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

// CharacterAgentExecutionContext is Host-only orchestration authority, not a
// model-facing novel_context profile. Each character receives its separately
// validated private observation, and the Arbiter receives its bounded view.
type CharacterAgentExecutionContext struct {
	AccessSourceToken     string
	ProjectAllSourceToken string
	ProjectAllState       *domain.ProjectedPlanningContextV2
}

// PrepareCharacterAgentExecutionContext avoids constructing the legacy
// Simulator's prose/RAG/outline packet just to extract a receipt. In particular,
// complete physical history is retained as typed Host authority rather than
// squeezed into the legacy model profile's 96KB budget or silently truncated.
func (t *ContextTool) PrepareCharacterAgentExecutionContext(ctx context.Context, chapter int) (CharacterAgentExecutionContext, error) {
	var out CharacterAgentExecutionContext
	if t == nil || t.store == nil || chapter <= 0 {
		return out, fmt.Errorf("invalid character-agent execution context")
	}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if err := t.store.Runtime.ValidatePipelineRenderCandidateEvidenceTree(); err != nil {
		return out, err
	}
	lock, err := t.store.Runtime.InspectPipelineExecution()
	if err != nil {
		return out, err
	}
	if lock == nil {
		if _, err := os.Lstat(filepath.Join(t.store.Dir(), "meta/runtime/pipeline_execution.json")); err == nil {
			return out, fmt.Errorf("character-agent context found an inactive execution lease; reacquire it before preparation")
		} else if !os.IsNotExist(err) {
			return out, err
		}
	}
	if lock != nil {
		if err := requireCurrentPipelineExecutionProcess(lock, "character-agent context"); err != nil {
			return out, err
		}
		if lock.Mode == domain.PipelineExecutionRender {
			return out, fmt.Errorf("frozen render cannot prepare live character-agent context")
		}
		if lock.TargetChapter != chapter {
			return out, fmt.Errorf("character-agent context chapter differs from execution lease")
		}
	}
	if err := guardOutlineAllDynamicMaterialExecution(t.store, t.Name()); err != nil {
		return out, err
	}
	if target, pending := pendingRewriteTarget(t.store); pending && target != chapter {
		return out, fmt.Errorf("character-agent context must target pending rewrite chapter %d", target)
	}
	if _, err := t.store.Drafts.LoadChapterPlanPartial(chapter); err != nil {
		return out, fmt.Errorf("load existing plan partial before character simulation: %w", err)
	}
	state, token, err := LoadProjectAllStateForExecution(t.store, chapter)
	if err != nil {
		return out, err
	}
	// These loaders validate source/checkpoint integrity even though their
	// display fields are not inputs to the independent-character orchestrator.
	validation := map[string]any{}
	if err := t.addChapterPipelineInstructionContext(validation, chapter); err != nil {
		return out, err
	}
	if err := t.attachSealedConvergencePlanningContext(validation, chapter, "world_simulation"); err != nil {
		return out, err
	}
	receipt, access, err := t.preparePlanningContextAccessReceipt(chapter, "world_simulation")
	if err != nil {
		return out, err
	}
	if receipt != nil {
		if state == nil || receipt.GenerationID != state.GenerationID || receipt.PlanningContextDigest != state.ContextDigest {
			return out, fmt.Errorf("character-agent authority changed before access receipt")
		}
		// Re-read identity immediately before publication. Neither a released
		// lease nor a changed canonical source can acquire an old receipt.
		current, currentToken, err := LoadProjectAllStateForExecution(t.store, chapter)
		if err != nil {
			return out, err
		}
		if current == nil || currentToken != token {
			return out, fmt.Errorf("character-agent authority changed while preparing context")
		}
		currentLock, err := t.store.Runtime.InspectPipelineExecution()
		if err != nil {
			return out, err
		}
		if currentLock == nil || currentLock.Owner != receipt.LockOwner || currentLock.ProcessID != receipt.LockProcessID || !currentLock.AcquiredAt.Equal(receipt.LockAcquiredAt) {
			return out, fmt.Errorf("character-agent execution lease changed before receipt publication")
		}
		if err := ctx.Err(); err != nil {
			return out, err
		}
		if err := t.store.Runtime.SavePlanningContextAccessReceipt(*receipt); err != nil {
			return out, err
		}
	}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	return CharacterAgentExecutionContext{AccessSourceToken: access, ProjectAllSourceToken: token, ProjectAllState: state}, nil
}
