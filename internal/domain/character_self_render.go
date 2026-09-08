package domain

import (
	"encoding/json"
	"strings"
)

func planningSelfTruthV2(value any, depth int) bool {
	if depth > 16 {
		return true
	}
	switch node := value.(type) {
	case map[string]any:
		for key, child := range node {
			switch key {
			case "self_tasks", "self_executions", "self_experiences", "task_progress", "known_placement", "operational_observations", "observation_requests", "observation_results":
				return true
			}
			if planningSelfTruthV2(child, depth+1) {
				return true
			}
		}
	case []any:
		for _, child := range node {
			if planningSelfTruthV2(child, depth+1) {
				return true
			}
		}
	case string:
		text := strings.TrimSpace(node)
		var embedded any
		if json.Unmarshal([]byte(text), &embedded) == nil {
			if decoded, isString := embedded.(string); !isString || decoded != node {
				return planningSelfTruthV2(embedded, depth+1)
			}
		}
		if start := strings.IndexByte(text, '{'); start >= 0 && start < len(text)-1 {
			if json.Unmarshal([]byte(text[start:]), &embedded) == nil {
				return planningSelfTruthV2(embedded, depth+1)
			}
		}
	}
	return false
}

func stripPlanningEncodedSelfTruthV2(value any) (any, bool) {
	switch node := value.(type) {
	case map[string]any:
		for key, child := range node {
			cleaned, drop := stripPlanningEncodedSelfTruthV2(child)
			if drop {
				delete(node, key)
			} else {
				node[key] = cleaned
			}
		}
	case []any:
		out := make([]any, 0, len(node))
		for _, child := range node {
			cleaned, drop := stripPlanningEncodedSelfTruthV2(child)
			if !drop {
				out = append(out, cleaned)
			}
		}
		return out, false
	case string:
		if planningSelfTruthV2(node, 0) {
			return nil, true
		}
	}
	return value, false
}
