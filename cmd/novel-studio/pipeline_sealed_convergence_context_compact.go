package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

const (
	pipelineSealedConvergenceContinuationContextVersion    = "sealed-convergence-continuation-context.v1"
	pipelineSealedConvergenceContinuationContextRuneBudget = 16_000
)

// pipelineSealedConvergenceCompactContinuationContext projects the normal
// host-prefetched planning packet into the only fields a fresh plan_details-only
// continuation can consume. The complete source remains durable in the store;
// this is a model-facing projection, not a new authority object.
func pipelineSealedConvergenceCompactContinuationContext(raw json.RawMessage) (json.RawMessage, error) {
	var source map[string]json.RawMessage
	if err := json.Unmarshal(raw, &source); err != nil {
		return nil, fmt.Errorf("decode sealed convergence continuation context: %w", err)
	}

	required := []string{
		"planning_context_access_receipt",
		"project_all_state",
		"project_all_state_source_token",
		"sealed_convergence_replan_context",
		"chapter_world_simulation",
		"structure",
		"gap_summary",
	}
	for _, key := range required {
		if len(source[key]) == 0 || string(source[key]) == "null" {
			return nil, fmt.Errorf("sealed convergence continuation context missing %s", key)
		}
	}
	if err := validatePipelineSealedConvergenceCompactBindings(source); err != nil {
		return nil, err
	}

	keep := []string{
		"active_chapter_task",
		"chapter_pipeline_instruction",
		"planning_context_access_receipt",
		"project_all_state",
		"project_all_state_source_token",
		"sealed_convergence_replan_context",
		"sealed_short_accepted_prose_word_budget",
		"chapter_world_simulation",
		"simulation_authority_receipt",
		"current_chapter_outline",
		"structure",
		"structure_source_status",
		"saved_core",
		"fields_present",
		"gap_summary",
		"simulation_characters",
		"visible_characters",
		"rewrite_craft_pack",
		"rewrite_craft_status",
	}
	out := make(map[string]any, len(keep)+3)
	for _, key := range keep {
		if value, ok := source[key]; ok {
			var decoded any
			if err := json.Unmarshal(value, &decoded); err != nil {
				return nil, fmt.Errorf("decode sealed convergence continuation field %s: %w", key, err)
			}
			switch key {
			case "sealed_convergence_replan_context":
				decoded = compactPipelineSealedConvergenceOverlay(decoded)
			case "chapter_world_simulation":
				decoded = compactPipelineSealedConvergenceSimulation(decoded)
			case "current_chapter_outline":
				decoded = compactPipelineSealedConvergenceOutline(decoded)
			case "structure":
				decoded = compactPipelineSealedConvergenceStructure(decoded)
			}
			out[key] = decoded
		}
	}

	if workingRaw := source["working_memory"]; len(workingRaw) > 0 {
		var working map[string]any
		if err := json.Unmarshal(workingRaw, &working); err != nil {
			return nil, fmt.Errorf("decode sealed convergence continuation working_memory: %w", err)
		}
		compact := make(map[string]any, 3)
		for _, key := range []string{"chapter_plan_stage", "current_chapter_outline", "user_rules"} {
			if value, ok := working[key]; ok {
				compact[key] = value
			}
		}
		if len(compact) > 0 {
			out["working_memory"] = compact
		}
	}

	if referenceRaw := source["reference_pack"]; len(referenceRaw) > 0 {
		var pack map[string]any
		if err := json.Unmarshal(referenceRaw, &pack); err != nil {
			return nil, fmt.Errorf("decode sealed convergence continuation reference_pack: %w", err)
		}
		compact := make(map[string]any, 1)
		if value, ok := pack["rag_fact_receipt"]; ok {
			compact["rag_fact_receipt"] = compactPipelineSealedConvergenceRAGFactReceipt(value)
		}
		if len(compact) > 0 {
			out["reference_pack"] = compact
		}
	}

	sum := sha256.Sum256(raw)
	out["sealed_convergence_continuation_packet"] = map[string]any{
		"version":               pipelineSealedConvergenceContinuationContextVersion,
		"source_context_sha256": "sha256:" + hex.EncodeToString(sum[:]),
		"policy":                "plan_details-only；exact partial/gaps/authority/receipts only",
	}

	encoded, err := json.Marshal(out)
	if err != nil {
		return nil, err
	}
	if runes := utf8.RuneCount(encoded); runes > pipelineSealedConvergenceContinuationContextRuneBudget {
		return nil, fmt.Errorf(
			"sealed convergence continuation compact context exceeds %d runes: %d",
			pipelineSealedConvergenceContinuationContextRuneBudget,
			runes,
		)
	}
	return encoded, nil
}

func compactPipelineSealedConvergenceRAGFactReceipt(value any) any {
	receipt, ok := value.(map[string]any)
	if !ok {
		return value
	}
	compact := make(map[string]any, 8)
	for _, key := range []string{
		"chapter", "receipt_id", "source_token", "selected_facts_sha256",
		"retrieval_policy", "no_material", "hits", "usage_policy",
	} {
		if field, exists := receipt[key]; exists {
			compact[key] = field
		}
	}
	return compact
}

func compactPipelineSealedConvergenceOverlay(value any) any {
	overlay, ok := value.(map[string]any)
	if !ok {
		return value
	}
	compact := make(map[string]any, 14)
	for _, key := range []string{
		"version", "generation_id", "chapter", "planning_context_digest",
		"projected_pre_state_root", "bundle_digest", "promotion_receipt_digest",
		"simulation_id", "simulation_digest", "authority_receipt_digest",
		"immutable_state_contract_sha256", "diagnostics_digest",
	} {
		if field, exists := overlay[key]; exists {
			compact[key] = field
		}
	}
	return compact
}

func compactPipelineSealedConvergenceSimulation(value any) any {
	simulation, ok := value.(map[string]any)
	if !ok {
		return value
	}
	compact := make(map[string]any, 5)
	for _, key := range []string{
		"status", "simulation_id", "base_tick_id", "protagonist_projection",
		"rewrite_source", "rewrite_fact_coverage",
	} {
		if field, exists := simulation[key]; exists {
			compact[key] = field
		}
	}
	return compact
}

func compactPipelineSealedConvergenceOutline(value any) any {
	outline, ok := value.(map[string]any)
	if !ok {
		return value
	}
	compact := make(map[string]any, 4)
	for _, key := range []string{"chapter", "title", "core_event", "hook"} {
		if field, exists := outline[key]; exists {
			compact[key] = field
		}
	}
	return compact
}

func compactPipelineSealedConvergenceStructure(value any) any {
	structure, ok := value.(map[string]any)
	if !ok {
		return value
	}
	compact := make(map[string]any, len(structure))
	for key, field := range structure {
		if key != "notes" {
			compact[key] = field
		}
	}
	return compact
}

func validatePipelineSealedConvergenceCompactBindings(source map[string]json.RawMessage) error {
	var access struct {
		Phase       string `json:"phase"`
		SourceToken string `json:"source_token"`
	}
	if err := json.Unmarshal(source["planning_context_access_receipt"], &access); err != nil ||
		access.Phase != string(domain.PlanningContextAccessPlan) {
		return fmt.Errorf("sealed convergence continuation planning access binding is invalid")
	}
	if _, err := domain.PlanningContextAccessTokenSHA256(access.SourceToken); err != nil {
		return fmt.Errorf("sealed convergence continuation planning access binding is invalid: %w", err)
	}
	var state struct {
		GenerationID  string `json:"generation_id"`
		NextChapter   int    `json:"next_chapter"`
		StateRoot     string `json:"state_root"`
		ContextDigest string `json:"context_digest"`
	}
	if err := json.Unmarshal(source["project_all_state"], &state); err != nil {
		return fmt.Errorf("decode sealed convergence continuation project_all_state: %w", err)
	}
	wantStateToken, err := domain.ProjectedPlanningContextSourceTokenV2(state.ContextDigest)
	if err != nil {
		return fmt.Errorf("sealed convergence continuation project-all digest is invalid: %w", err)
	}
	var gotStateToken string
	if err := json.Unmarshal(source["project_all_state_source_token"], &gotStateToken); err != nil ||
		strings.TrimSpace(gotStateToken) != wantStateToken {
		return fmt.Errorf("sealed convergence continuation project-all source token drift")
	}
	var overlay struct {
		GenerationID          string `json:"generation_id"`
		Chapter               int    `json:"chapter"`
		PlanningContextDigest string `json:"planning_context_digest"`
		ProjectedPreStateRoot string `json:"projected_pre_state_root"`
		SimulationID          string `json:"simulation_id"`
	}
	if err := json.Unmarshal(source["sealed_convergence_replan_context"], &overlay); err != nil {
		return fmt.Errorf("decode sealed convergence continuation authority overlay: %w", err)
	}
	var simulation struct {
		Status       string `json:"status"`
		SimulationID string `json:"simulation_id"`
	}
	if err := json.Unmarshal(source["chapter_world_simulation"], &simulation); err != nil {
		return fmt.Errorf("decode sealed convergence continuation simulation: %w", err)
	}
	if state.NextChapter <= 0 || overlay.Chapter != state.NextChapter ||
		overlay.GenerationID != state.GenerationID ||
		overlay.PlanningContextDigest != state.ContextDigest ||
		overlay.ProjectedPreStateRoot != state.StateRoot ||
		overlay.SimulationID == "" || overlay.SimulationID != simulation.SimulationID ||
		simulation.Status != "ready" {
		return fmt.Errorf("sealed convergence continuation exact authority binding drift")
	}
	return nil
}
