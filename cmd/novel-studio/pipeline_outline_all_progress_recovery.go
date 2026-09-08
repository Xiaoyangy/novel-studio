package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

// The old plan_structure tool changed init -> outline after its authorized
// skeleton save. Recover only that exact first-operation host side effect.
// Receipts, proposal bytes, usage, and all other progress/canon remain intact.
// Caller owns the exclusive run-root control and validated candidate namespace.
func recoverPipelineOutlineAllLegacyStructurePhase(live, candidate *store.Store, receipt *domain.OutlineAllExecutionReceipt, protectedRoot, stableRoot string) (bool, error) {
	if receipt == nil || receipt.Status != domain.OutlineAllExecutionBuilding ||
		receipt.CompletedActionCount != 1 || receipt.PendingAction != nil {
		return false, nil
	}
	if err := domain.ValidateOutlineAllExecutionReceipt(*receipt); err != nil {
		return false, err
	}
	if receipt.ProtectedCanonRoot != protectedRoot || receipt.StableProgressRoot != stableRoot {
		return false, fmt.Errorf("outline-all legacy phase recovery baseline does not match receipt")
	}
	readFields := func(st *store.Store) (map[string]json.RawMessage, error) {
		raw, err := os.ReadFile(filepath.Join(st.Dir(), "meta", "progress.json"))
		if err != nil {
			return nil, err
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			return nil, err
		}
		if fields == nil {
			return nil, fmt.Errorf("outline-all recovery requires progress object")
		}
		return fields, nil
	}
	baseline, err := readFields(live)
	if err != nil {
		return false, err
	}
	current, err := readFields(candidate)
	if err != nil {
		return false, err
	}
	var beforePhase, afterPhase domain.Phase
	if err := json.Unmarshal(baseline["phase"], &beforePhase); err != nil {
		return false, err
	}
	if err := json.Unmarshal(current["phase"], &afterPhase); err != nil {
		return false, err
	}
	if beforePhase != domain.PhaseInit || afterPhase != domain.PhaseOutline {
		return false, nil
	}
	current["phase"] = baseline["phase"]
	normalized, err := json.Marshal(current)
	if err != nil {
		return false, err
	}
	if root, err := pipelineOutlineAllStableProgressRawRoot(normalized); err != nil || root != stableRoot {
		return false, fmt.Errorf("outline-all legacy phase recovery refuses other progress drift")
	}
	if root, err := pipelineOutlineAllProtectedCanonRootWithProgress(candidate.Dir(), normalized); err != nil || root != protectedRoot {
		return false, fmt.Errorf("outline-all legacy phase recovery refuses other protected canon drift")
	}
	if err := validatePipelineOutlineAllOperationChain(candidate, receipt); err != nil {
		return false, fmt.Errorf("outline-all legacy phase recovery: %w", err)
	}
	var first pipelineOutlineAllOperationReceipt
	if err := readPipelinePlanningJSON(filepath.Join(candidate.Dir(), filepath.FromSlash(pipelineOutlineAllOperationReceiptPath(1))), &first); err != nil {
		return false, err
	}
	if first.Action.Type != domain.OutlineAllActionPlanStructure {
		return false, fmt.Errorf("outline-all legacy phase recovery requires a completed plan_structure")
	}
	if _, err := writePipelinePlanningJSON(filepath.Join(candidate.Dir(), "meta", "progress.json"), current); err != nil {
		return false, fmt.Errorf("outline-all restore legacy structure phase: %w", err)
	}
	return true, nil
}
