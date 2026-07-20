package tools

import (
	"fmt"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/store"
)

// ValidateStagedChapterPlanSourceBinding exposes the same source identity gate
// used by plan_details so a crash-recovery controller can decide whether an
// existing partial is safe to resume. It validates both the current world
// simulation and, for rewrites, the exact rewrite-source hash.
func ValidateStagedChapterPlanSourceBinding(
	st *store.Store,
	chapter int,
	partial map[string]any,
	expectedSimulationID string,
) error {
	if st == nil || chapter <= 0 || partial == nil {
		return fmt.Errorf("staged chapter plan source binding is incomplete")
	}
	simulation, err := ensureChapterWorldSimulationReadyForPlanning(st, chapter)
	if err != nil {
		return err
	}
	if simulation == nil || strings.TrimSpace(simulation.SimulationID) != strings.TrimSpace(expectedSimulationID) {
		return fmt.Errorf("staged chapter plan world simulation identity drift")
	}
	if !planStructureBoundToSources(st, chapter, partial, simulation) {
		return fmt.Errorf("staged chapter plan is not bound to the current world simulation/rewrite source")
	}
	return nil
}
