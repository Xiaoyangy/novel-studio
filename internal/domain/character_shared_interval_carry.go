package domain

import "fmt"

// Opt-in physical interpretation only. The owner still submits every task and
// the arbiter still supplies every actual execution; no active generation is
// upgraded by the presence of this constant in a newer binary.
const CharacterSharedIntervalCarryPolicyV1 = "character-carry:shared-interval.v1"

func HasCharacterSharedIntervalCarryPolicyV1(sources []string) bool {
	return physicalContainsRefV2(sources, CharacterSharedIntervalCarryPolicyV1)
}

// All executions have already passed the ordinary shape, owner/task and
// story-window checks. An identical positive interval denotes one co-carried
// load, not several inferred trips. Each task keeps its own experience ID;
// every resource may belong to that completed load only once.
func validateSharedIntervalCarryExecutionsV1(executions []CharacterSelfExecutionV2, tasks map[string]CharacterSelfTaskV2) error {
	var interval *CharacterSelfExecutionV2
	seenTasks, seenResources := map[string]bool{}, map[string]bool{}
	for _, execution := range executions {
		task := tasks[execution.TaskID]
		if task.Kind != "carry" || execution.Status != "completed" {
			continue
		}
		if interval == nil {
			copy := execution
			interval = &copy
		} else if !samePhysicalNumberV2(interval.StartDay, execution.StartDay) || !samePhysicalNumberV2(interval.EndDay, execution.EndDay) {
			return fmt.Errorf("completed carry tasks must share one exact actual interval; intermediate or separate transports require another explicit cycle")
		}
		if seenTasks[task.TaskID] {
			return fmt.Errorf("one shared-interval carry cannot repeat a completed task")
		}
		seenTasks[task.TaskID] = true
		for _, resourceID := range task.ResourceIDs {
			if seenResources[resourceID] {
				return fmt.Errorf("shared-interval carry repeats the same resource across completed tasks")
			}
			seenResources[resourceID] = true
		}
	}
	return nil
}
