package tools

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/modelinput"
)

// This fallback is reached only after the existing context budget rejected the
// payload. Previously successful packets keep their exact shape and bytes.
func (t *ContextTool) compactRejectedPlanningProjection(result map[string]any, chapter int, profile string) (bool, error) {
	if profile != "planning" || chapter <= 0 || chapterWorldSimulationContextStatus(result) != "ready" {
		return false, nil
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return false, err
	}
	ceiling := contextBudget(chapter, profile)
	if _, _, allow := planningCriticalOverflowMode(result, chapter, profile); allow {
		return false, nil // Rewrite/sealed overflow has its existing contract.
	}
	if len(raw) <= ceiling {
		return false, nil
	}
	sim, err := t.store.LoadChapterWorldSimulation(chapter)
	if err != nil {
		return false, err
	}
	if sim == nil || sim.Version != 2 || sim.CharacterActivation == nil || len(sim.CharacterActivation.CycleDigests) < 2 {
		return false, nil
	}
	if gaps := chapterWorldSimulationGaps(t.store, *sim); len(gaps) > 0 {
		return false, fmt.Errorf("planning projection fallback source is invalid: %v", gaps)
	}
	world := result["chapter_world_simulation"].(map[string]any)
	if world["simulation_id"] != sim.SimulationID {
		return false, fmt.Errorf("planning projection fallback source identity changed")
	}
	projected, err := json.Marshal(world["protagonist_projection"])
	if err != nil {
		return false, err
	}
	expected, err := json.Marshal(planningProtagonistProjection(sim.ProtagonistProjection))
	if err != nil {
		return false, err
	}
	var projectedFields, expectedFields map[string]json.RawMessage
	if err := json.Unmarshal(projected, &projectedFields); err != nil {
		return false, err
	}
	if err := json.Unmarshal(expected, &expectedFields); err != nil {
		return false, err
	}
	projected, _ = json.Marshal(projectedFields)
	expected, _ = json.Marshal(expectedFields)
	if !bytes.Equal(projected, expected) {
		return false, fmt.Errorf("planning projection fallback differs from verified source")
	}
	digest, err := domain.DeterministicPlanningHash(*sim)
	if err != nil {
		return false, err
	}
	binding := modelinput.PlanningProjectionSourceBinding{SimulationID: sim.SimulationID, SimulationDigest: "sha256:" + digest}
	compact, changed, err := modelinput.EncodePlanningProjectionModelViewV1(projected, binding)
	if err != nil || !changed {
		return false, err
	}
	decoded, err := modelinput.DecodePlanningProjectionModelViewV1(compact, binding)
	if err != nil {
		return false, err
	}
	if !bytes.Equal(decoded, projected) {
		return false, fmt.Errorf("planning projection fallback failed lossless verification")
	}
	world["protagonist_projection"] = json.RawMessage(compact)
	return true, nil
}
