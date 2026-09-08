package agents

import (
	"encoding/json"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/modelinput"
	"github.com/voocel/agentcore"
)

// Pure request construction for the actual runner and read-only budget replay.
// It does not write usage or proof artifacts and never invokes a model.
func prepareCharacterArbitrationRequest(inputs characterAgentChapterInputs, proposals []domain.CharacterDecisionProposal, tool agentcore.Tool) (string, string, agentcore.Tool, error) {
	stimulusView, viewErr := characterArbiterStimulusModelView(inputs.Stimulus)
	if viewErr != nil {
		return "", "", nil, viewErr
	}
	var continuations []domain.CharacterWorkContinuationReceiptV1
	if inputs.ContinuationProof != nil {
		continuations = inputs.ContinuationProof.Continuations()
	}
	payload, _ := json.Marshal(struct {
		Stimulus      any                                         `json:"world_stimulus"`
		Activation    domain.CharacterAgentActivation             `json:"activation"`
		Proposals     []domain.CharacterDecisionProposal          `json:"proposals"`
		Continuations []domain.CharacterWorkContinuationReceiptV1 `json:"continuations,omitempty"`
	}{stimulusView, inputs.Activation, proposals, continuations})
	if inputs.ArbitrationV3 != nil {
		scope, err := inputs.ArbitrationV3.Sources(inputs.ArbitrationRoundV3)
		if err != nil {
			return "", "", nil, err
		}
		payload, err = json.Marshal(struct {
			Stimulus      any                                         `json:"world_stimulus"`
			Activation    domain.CharacterAgentActivation             `json:"activation"`
			Proposals     []domain.CharacterDecisionProposal          `json:"proposals"`
			Continuations []domain.CharacterWorkContinuationReceiptV1 `json:"continuations,omitempty"`
			Current       domain.CharacterArbitrationCoordinateV1     `json:"current_arbitration"`
			Sources       []domain.CharacterArbitrationIntentSourceV1 `json:"intent_sources"`
			Previous      []domain.WorldArbitrationReceipt            `json:"previous_arbitrations,omitempty"`
		}{stimulusView, inputs.Activation, proposals, scope.Continuations(), scope.Coordinate(), scope.Sources(), scope.PreviousArbitrations()})
		if err != nil {
			return "", "", nil, err
		}
	}
	arbiterPrompt := worldArbiterSystemPrompt
	if inputs.Stimulus.Version == domain.WorldStimulusPacketV2Version {
		arbiterPrompt = worldArbiterSystemPromptV2
		if domain.HasCharacterSelfExperiencePolicyV2(inputs.Stimulus.Sources) {
			arbiterPrompt += worldArbiterSelfExperiencePromptV2
		}
		if domain.HasCharacterPassiveReceptionPolicyV2(inputs.Stimulus.Sources) {
			arbiterPrompt += worldArbiterPassiveReceptionPromptV2
		}
		if domain.HasCharacterOperationalAvailabilityPolicyV1(inputs.Stimulus.Sources) {
			arbiterPrompt += worldArbiterOperationalAvailabilityPromptV1
		}
		if domain.HasCharacterWorkArtifactPolicyV1(inputs.Stimulus.Sources) {
			arbiterPrompt += worldArbiterWorkArtifactPromptV1
		}
		if domain.HasCharacterResourceObservationTimePolicyV1(inputs.Stimulus.Sources) {
			arbiterPrompt += worldArbiterResourceObservationTimePromptV1
		}
		if domain.HasCharacterSurfaceInspectionPolicyV1(inputs.Stimulus.Sources) {
			arbiterPrompt += worldArbiterSurfaceInspectionPromptV1
		}
	}
	if inputs.CycleSession != nil {
		arbiterPrompt += worldArbiterActivationProjectionPromptV1
	}
	if inputs.ContinuationProof != nil {
		arbiterPrompt += worldArbiterContinuationPromptV2
	}
	if inputs.ArbitrationV3 != nil {
		arbiterPrompt += worldArbiterContinuationPromptV2 + worldArbiterRoundsPromptV3
	}
	var executionTool agentcore.Tool = tool
	if domain.HasCharacterSelfChronologyPolicyV1(inputs.Stimulus.Sources) {
		makeCodec := modelinput.NewScopedReferenceCodec
		if domain.HasCharacterWorkArtifactPolicyV1(inputs.Stimulus.Sources) {
			makeCodec = modelinput.NewScopedArtifactReferenceCodecV1
		}
		codec, err := makeCodec(modelinput.KindWorldArbitration, json.RawMessage(payload))
		if err != nil {
			return "", "", nil, err
		}
		executionTool, err = newScopedReferenceTool(tool, codec)
		if err != nil {
			return "", "", nil, err
		}
		payload, err = json.Marshal(codec.ModelView())
		if err != nil {
			return "", "", nil, err
		}
		arbiterPrompt += scopedReferencePrompt
	}
	return arbiterPrompt, "裁决以下单一世界输入。soft_guidance 只能作为方向，不得覆盖角色选择：\n<world_arbitration_input>\n" + string(payload) + "\n</world_arbitration_input>\n现在只调用 resolve_chapter_world。", executionTool, nil
}
