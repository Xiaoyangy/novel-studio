package agents

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

const characterSelfModelViewPolicyV2 = "bounded-self-history.recent8-allunfinished-placementproofs32-bytes16384.v1"

// Model-only, explicitly labelled view. The tool retains the original full
// stimulus; this object is never persisted or offered as a re-signed source.
func characterArbiterStimulusModelView(stimulus domain.WorldStimulusPacket) (any, error) {
	if !domain.HasCharacterSelfExperiencePolicyV2(stimulus.Sources) || stimulus.PhysicalState == nil {
		return stimulus, nil
	}
	raw, err := json.Marshal(stimulus)
	if err != nil {
		return nil, err
	}
	var view map[string]any
	if err := json.Unmarshal(raw, &view); err != nil {
		return nil, err
	}
	delete(view, "digest")
	view["source_stimulus_digest"] = stimulus.Digest
	view["source_version"] = stimulus.Version
	view["version"] = "world-stimulus-model-view.v1"
	view["view_policy"] = characterSelfModelViewPolicyV2 + "：本人经历仅为有界展示，源摘要对应宿主持有的完整刺激。未展示的旧经历不等于未发生或进度为零；task_progress是宿主从完整历史派生的摘要，不从展示片段重算。只提交新self_executions，不重抄或回存旧历史。"
	physical, ok := view["physical_state"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("arbiter self view lacks physical state")
	}
	physical["source_version"] = physical["version"]
	physical["version"] = "world-physical-state-model-view.v1"
	actors, ok := physical["actors"].([]any)
	if !ok {
		return nil, fmt.Errorf("arbiter self view lacks actor state")
	}
	for _, entry := range actors {
		actor, ok := entry.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("arbiter self view has malformed actor state")
		}
		agentID, _ := actor["agent_id"].(string)
		experiences, progress, err := domain.BuildCharacterSelfObservationV2(*stimulus.PhysicalState, agentID)
		if err != nil {
			return nil, err
		}
		if experiences == nil {
			experiences = []domain.CharacterSelfExperienceV2{}
		}
		if progress == nil {
			progress = []domain.CharacterTaskProgressV2{}
		}
		actor["self_experiences"], actor["task_progress"] = experiences, progress
		if domain.HasCharacterOperationalAvailabilityPolicyV1(stimulus.Sources) {
			operations, err := domain.BuildCharacterOperationalObservationsV1(*stimulus.PhysicalState, agentID)
			if err != nil {
				return nil, err
			}
			if len(operations) > 0 {
				actor["operational_observations"] = operations
			}
		}
	}
	// buildWorldStimulus also makes a host-generated textual current-event
	// mirror of projected physical state. Do not let that encoded full history
	// bypass the bounded self view. Other author events/resources remain.
	if events, ok := view["current_events"].([]any); ok {
		kept := make([]any, 0, len(events))
		for _, entry := range events {
			fact, ok := entry.(map[string]any)
			if ok && fact["kind"] == "projected_state" {
				text, _ := fact["text"].(string)
				if strings.HasPrefix(text, "world的"+domain.WorldPhysicalStateV2Field+"=") {
					continue
				}
			}
			kept = append(kept, entry)
		}
		view["current_events"] = kept
	}
	return view, nil
}
