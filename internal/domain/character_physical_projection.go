package domain

import (
	"encoding/json"
	"fmt"
	"strings"
)

func ValidateProjectedPhysicalStateV2(simulation ChapterWorldSimulation, delta ProjectedDelta) error {
	var states, amounts []StateMutationV2
	for _, mutation := range delta.CharacterState {
		if mutation.Field == WorldPhysicalStateV2Field {
			states = append(states, mutation)
		}
	}
	for _, mutation := range delta.Resources {
		if mutation.Field == WorldResourceActualAmountV2Field {
			amounts = append(amounts, mutation)
		}
	}
	if simulation.PhysicalState == nil {
		for _, decision := range simulation.CharacterDecisions {
			if decision.PostState != nil {
				return fmt.Errorf("post_state requires simulation physical_state")
			}
		}
		if len(states)+len(amounts) > 0 {
			return fmt.Errorf("physical state projection requires simulation physical_state")
		}
		return nil
	}
	encoded, err := EncodeWorldPhysicalStateV2(*simulation.PhysicalState)
	if err != nil {
		return err
	}
	if len(states) != 1 || states[0].Subject != "world" || states[0].Object != "" || states[0].After != encoded {
		return fmt.Errorf("physical state projection must contain one exact canonical world physical_state_v2")
	}
	catalog := map[string]WorldResourceBalanceV2{}
	for _, resource := range simulation.PhysicalState.Resources {
		catalog[resource.ResourceID] = resource
	}
	if len(amounts) != len(catalog) {
		return fmt.Errorf("physical state projection requires one global actual_amount_v2 per resource")
	}
	seen := map[string]bool{}
	for _, mutation := range amounts {
		resource, exists := catalog[mutation.Object]
		if !exists || seen[mutation.Object] || mutation.Subject != "world" {
			return fmt.Errorf("physical resource amount is unknown, duplicated or copied per actor")
		}
		seen[mutation.Object] = true
		amount, err := json.Marshal(resource.ActualAmount)
		if err != nil {
			return err
		}
		if mutation.After != string(amount) {
			return fmt.Errorf("physical resource %s amount projection differs from world catalog", mutation.Object)
		}
	}
	actors := map[string]CharacterPhysicalStateV2{}
	for _, actor := range simulation.PhysicalState.Actors {
		actors[actor.AgentID] = actor
	}
	for _, decision := range simulation.CharacterDecisions {
		if decision.PostState == nil {
			return fmt.Errorf("physical simulation decision requires post_state")
		}
		actor, exists := actors[decision.PostState.AgentID]
		if !exists || actor.Character != decision.Character || actor.Location != decision.Location || !samePhysicalValueV2(actor, *decision.PostState) {
			return fmt.Errorf("physical decision differs from complete world post_state")
		}
		locations := 0
		for _, mutation := range delta.Locations {
			if mutation.Subject == decision.Character && (mutation.Field == "location" || mutation.Field == "current_location") {
				locations++
				if mutation.After != actor.Location || mutation.Object != "" {
					return fmt.Errorf("physical location projection differs from actor post_state")
				}
			}
		}
		if locations != 1 {
			return fmt.Errorf("physical location projection requires one current location per decided actor")
		}
	}
	return nil
}

func ProjectedPhysicalStateV2(context ProjectedPlanningContextV2) (*WorldPhysicalStateV2, error) {
	if err := ValidateProjectedPlanningContextV2(context); err != nil {
		return nil, err
	}
	var result *WorldPhysicalStateV2
	for _, fact := range context.CumulativeState {
		if fact.Field != WorldPhysicalStateV2Field {
			continue
		}
		if result != nil || fact.Category != "character_state" || fact.Subject != "world" || fact.Object != "" || fact.ThroughChapter != context.ThroughChapter {
			return nil, fmt.Errorf("projected physical state must be unique and come from the immediately preceding chapter")
		}
		state, err := DecodeWorldPhysicalStateV2(fact.Value)
		if err != nil {
			return nil, err
		}
		result = &state
	}
	return result, nil
}

// Recognize server-only structured data, including JSON encoded in strings.
// A numeric value is never a secrecy marker: lawful estimates/reports may be
// numerically identical to a hidden balance.
func planningPhysicalTruthV2(value any, depth int) bool {
	if depth > 16 {
		return false
	}
	switch node := value.(type) {
	case map[string]any:
		if version, _ := node["version"].(string); version == WorldPhysicalStateV2Version {
			return true
		}
		if field, _ := node["field"].(string); field == WorldPhysicalStateV2Field || field == WorldResourceActualAmountV2Field {
			return true
		}
		for key, child := range node {
			if physicalServerKeyV2(key) {
				return true
			}
			if planningPhysicalTruthV2(child, depth+1) {
				return true
			}
		}
	case []any:
		for _, child := range node {
			if planningPhysicalTruthV2(child, depth+1) {
				return true
			}
		}
	case string:
		text := strings.TrimSpace(node)
		if strings.Contains(text, WorldPhysicalStateV2Field+"=") || strings.Contains(text, WorldResourceActualAmountV2Field+"=") {
			return true
		}
		var embedded any
		if json.Unmarshal([]byte(text), &embedded) == nil {
			if _, sameString := embedded.(string); !sameString || embedded != node {
				return planningPhysicalTruthV2(embedded, depth+1)
			}
		}
		if start := strings.IndexByte(text, '{'); start >= 0 && start < len(text)-1 {
			if json.Unmarshal([]byte(text[start:]), &embedded) == nil {
				return planningPhysicalTruthV2(embedded, depth+1)
			}
		}
	}
	return false
}

func stripPlanningPhysicalTruthV2(value any) (any, bool) {
	switch node := value.(type) {
	case map[string]any:
		if version, _ := node["version"].(string); version == WorldPhysicalStateV2Version {
			return nil, true
		}
		if field, _ := node["field"].(string); field == WorldPhysicalStateV2Field || field == WorldResourceActualAmountV2Field {
			return nil, true
		}
		for key, child := range node {
			if physicalServerKeyV2(key) {
				delete(node, key)
				continue
			}
			cleaned, drop := stripPlanningPhysicalTruthV2(child)
			if drop {
				delete(node, key)
			} else {
				node[key] = cleaned
			}
		}
		return node, false
	case []any:
		out := make([]any, 0, len(node))
		for _, child := range node {
			cleaned, drop := stripPlanningPhysicalTruthV2(child)
			if !drop {
				out = append(out, cleaned)
			}
		}
		return out, false
	case string:
		if planningPhysicalTruthV2(node, 0) {
			return nil, true
		}
	}
	return value, false
}

func physicalServerKeyV2(key string) bool {
	switch key {
	case "physical_state", "final_physical_state", "resource_settlements", "resource_deliveries", "passive_receptions", "character_activation", "character_activation_evidence", "character_decision_trace", "readable_facts", "access_requires_any", "actual_amount", "post_state":
		return true
	}
	return false
}
