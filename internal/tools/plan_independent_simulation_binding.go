package tools

import (
	"errors"
	"fmt"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/errs"
	"github.com/chenhongyang/novel-studio/internal/store"
)

var ErrIndependentPlanNeedsReplan = errors.New("independent plan needs_replan")

func validateIndependentPlanCausalBindings(merged map[string]any, simulation *domain.ChapterWorldSimulation) error {
	if simulation == nil || !independentChapterSimulation(*simulation) {
		return nil
	}
	for key, expected := range map[string]string{"world_simulation_id": simulation.SimulationID, "protagonist_decision": effectiveProtagonistDecision(simulation.ProtagonistProjection)} {
		if value, exists := merged[key]; exists {
			if text, ok := value.(string); !ok || text != expected {
				return fmt.Errorf("independent plan %s differs from current simulation (expected %q); refusing to overwrite an existing binding: %w", key, expected, errs.ErrToolPrecondition)
			}
		}
	}
	return nil
}

func validateIndependentPlanContextSources(st *store.Store, chapter int, simulation *domain.ChapterWorldSimulation, partialSources []string, requireContext bool) error {
	context, token, err := LoadProjectAllStateForExecution(st, chapter)
	if err != nil {
		return fmt.Errorf("independent planning context: %w: %w", err, errs.ErrToolPrecondition)
	}
	if context == nil {
		if requireContext {
			return fmt.Errorf("independent plan stamp recovery requires a current-process project-all lease: %w", errs.ErrToolPrecondition)
		}
		return nil
	}
	if token == "" || context.GenerationID != simulation.GenerationID || simulation.Chapter != chapter {
		return fmt.Errorf("independent planning simulation differs from the current generation/context: %w", errs.ErrToolPrecondition)
	}
	groups := [][]string{simulation.Sources}
	if partialSources != nil {
		groups = append(groups, partialSources)
	}
	for _, sources := range groups {
		matched := false
		for _, source := range sources {
			if strings.HasPrefix(strings.TrimSpace(source), "project-all-state:") {
				if source != token {
					return fmt.Errorf("independent plan binding refuses a different context source: %w", errs.ErrToolPrecondition)
				}
				matched = true
			}
		}
		if !matched {
			return fmt.Errorf("independent plan binding requires the exact context token in partial and simulation: %w", errs.ErrToolPrecondition)
		}
	}
	return nil
}

// A same-context token proves provenance, not that a plan consumed the actual
// simulation. Missing stamps from the old empty-cast loader therefore require
// replanning; never relabel paid but potentially contradictory prose as valid.
// The existing call shape is retained, but this function never writes stamps.
func recoverIndependentPlanSourceStamp(st *store.Store, chapter int, partial map[string]any, simulation *domain.ChapterWorldSimulation, _ bool) (bool, error) {
	if simulation == nil || !independentChapterSimulation(*simulation) || partial == nil {
		return false, nil
	}
	structure, ok := partial["structure"].(map[string]any)
	if !ok || intFromAny(structure["chapter"]) != chapter || simulation.Chapter != chapter {
		return false, fmt.Errorf("independent plan partial does not bind the current chapter: %w", errs.ErrToolPrecondition)
	}
	rewriteSource, _, _, err := loadChapterRewriteSource(st, chapter)
	if err != nil {
		return false, err
	}
	if stamp, exists := structure[planStructureRewriteSHAKey]; exists {
		if value, ok := stamp.(string); !ok || rewriteSource == nil || value != rewriteSource.BodySHA256 {
			return false, fmt.Errorf("independent plan partial has a different rewrite source; refusing stamp recovery: %w", errs.ErrToolPrecondition)
		}
	} else if rewriteSource != nil {
		return false, fmt.Errorf("independent plan partial lacks its current rewrite source binding: %w", errs.ErrToolPrecondition)
	}
	merged, _ := partial["causal_simulation"].(map[string]any)
	if err := validateIndependentPlanCausalBindings(merged, simulation); err != nil {
		return false, err
	}
	if stamp, exists := structure[planStructureWorldSimulationKey]; exists {
		if value, ok := stamp.(string); !ok || value != simulation.SimulationID {
			return false, fmt.Errorf("independent plan_structure has a different simulation stamp; refusing to replace it: %w", errs.ErrToolPrecondition)
		}
		if sources := stringSliceFromAny(merged["context_sources"]); len(sources) > 0 {
			if err := validateIndependentPlanContextSources(st, chapter, simulation, sources, false); err != nil {
				return false, err
			}
		}
		return false, nil
	}
	if err := validateStoredCharacterAgentProtocol(st, *simulation); err != nil {
		return false, fmt.Errorf("independent plan stamp recovery requires verified arbitration: %w: %w", err, errs.ErrToolPrecondition)
	}
	sources := stringSliceFromAny(merged["context_sources"])
	if sources == nil {
		sources = []string{}
	}
	if err := validateIndependentPlanContextSources(st, chapter, simulation, sources, true); err != nil {
		return false, err
	}
	return false, fmt.Errorf("%w: 缺失宿主 simulation stamp 的旧 partial 未证明已消费真实裁决；同一 context token 不能证明其场景/结果正确。原 partial 保留审计，请通过正式恢复流程基于真实裁决重新规划，不得只补 stamp 后 finalize: %w", ErrIndependentPlanNeedsReplan, errs.ErrToolPrecondition)
}
