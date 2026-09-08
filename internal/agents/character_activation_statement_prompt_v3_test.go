package agents

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/voocel/agentcore"
)

// These are pre-clarification identities. The V3 prompt is part of its frozen
// protocol; the historical one-shot and V1/V2 execution paths must not change.
func TestActivationV3StatementProtocolIsolation(t *testing.T) {
	for _, tc := range []struct{ name, got, want string }{
		{"one-shot-v1", CharacterAgentProtocolDigestForVersion(domain.CharacterAgentDecisionProtocolVersion), "sha256:0a031fe80f6a44b477c3c769b9a1e23c2426097df56063b2ad4f02a545f2f135"},
		{"one-shot-v2", CharacterAgentProtocolDigestForVersion(domain.CharacterAgentDecisionProtocolV2Version), "sha256:4db20d580d6828541d6341fbcfc53b34f432747884add86c78026fcfa5e7127e"},
		{"activation-v1", characterActivationProtocolForPolicy(domain.CharacterActivationCyclePolicy), "sha256:dae804415ae589cc5582236f1b0f7d9a6a9774e42be6f04e1d5466e4b977e5b7"},
		{"activation-v2", characterActivationProtocolForPolicy(domain.CharacterActivationCyclePolicyV2), "sha256:7e3b5eacd9df9c19014b2c1da2c829aad6402a1c92d9480e21aa6e39ab4fd953"},
		{"planning-v1", ProjectAllPlanningProtocolWithActivation("statement-boundary", domain.CharacterAgentDecisionProtocolV2Version, 4, domain.CharacterActivationCyclePolicy), "620cf9bff7d0b0433ab4cc115439233663ed254f3588ce703225a3867f05655c"},
		{"planning-v2", ProjectAllPlanningProtocolWithActivation("statement-boundary", domain.CharacterAgentDecisionProtocolV2Version, 4, domain.CharacterActivationCyclePolicyV2), "9f0bfd2d636100c2cc54e8c7ff01bfe666695006646c7802923d27c99539778f"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Fatalf("V3 clarification changed historical identity: got %s, want %s", tc.got, tc.want)
			}
		})
	}
	if got := characterActivationProtocolV3Digest(); got == "" || got == "sha256:2da8fcbf8498909265d660027ae2e33cc3f42ffa2d334b7bf125d3d5c86cb5d1" {
		t.Fatal("V3 clarification reused its old execution identity")
	}
	if got := ProjectAllPlanningProtocolWithActivation("statement-boundary", domain.CharacterAgentDecisionProtocolV2Version, 4, domain.CharacterActivationCyclePolicyV3); got == "" || got == "3c7665b93c7f04dd46f4ae1818107ad62540cf0aea56b14fa8261b40a4fbc35d" {
		t.Fatal("V3 clarification reused its old planning identity")
	}
}

// Checks the actual system prompts sent by the runner. Its scripted model
// exercises transport and isolation only, not the language model's judgment.
type statementPromptBoundaryModelV3 struct {
	activationV3RuntimeModel
	checkedArbiters int
}

func (m *statementPromptBoundaryModelV3) Generate(ctx context.Context, messages []agentcore.Message, specs []agentcore.ToolSpec, opts ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	var system string
	for _, message := range messages {
		if message.Role == agentcore.RoleSystem {
			system += message.TextContent()
		}
	}
	isArbiter := len(specs) == 1 && specs[0].Name == "resolve_chapter_world"
	if isArbiter {
		for _, required := range []string{
			"自主选择合法陈述、承认本人经手事实、否认或保留信息",
			"有来源的reported/待核陈述",
			"不自动使陈述内容成为世界真相、独立证据或已核实结论",
			"不授权裁判禁止知情者说话、要求其改口或为叙事节奏强改意图",
			"仍须拒绝角色使用本人未知的秘密、虚构接收、越过实际位置/时间/权限或其他真实物理与安全条件的结果",
			"哪项已成立的结果、资源、权限或期限冲突使该硬合同的可行履行路径被排除",
			"不得仅因角色合法说话或不符合软情节就上报硬不可能",
			"先测得4，随后耗光至0，本人上次读数仍是4",
			"资源与感知未变化时省略resources/resource_updates",
		} {
			if !strings.Contains(system, required) {
				return nil, fmt.Errorf("V3 arbiter lost statement/verification boundary: %s", required)
			}
		}
		m.checkedArbiters++
	} else if strings.Contains(system, "陈述与核验边界：") || strings.Contains(system, "硬不可能的证明边界：") {
		return nil, fmt.Errorf("V3 arbiter clarification leaked into another role")
	}
	if len(specs) == 1 && specs[0].Name == "submit_character_decision" && !strings.Contains(system, "resource_measurements.task_id绑定本人self_tasks") {
		return nil, fmt.Errorf("V3 character lost the actual measurement task binding")
	}
	return m.activationV3RuntimeModel.Generate(ctx, messages, specs, opts...)
}

func (m *statementPromptBoundaryModelV3) GenerateStream(ctx context.Context, messages []agentcore.Message, specs []agentcore.ToolSpec, opts ...agentcore.CallOption) (<-chan agentcore.StreamEvent, error) {
	response, err := m.Generate(ctx, messages, specs, opts...)
	if err != nil {
		return nil, err
	}
	stream := make(chan agentcore.StreamEvent, 1)
	stream <- agentcore.StreamEvent{Type: agentcore.StreamEventDone, Message: response.Message, StopReason: response.Message.StopReason}
	close(stream)
	return stream, nil
}

func TestActivationV3StatementPromptReachesOnlyActualArbiter(t *testing.T) {
	st, cfg, boundary := activationV3RuntimeFixture(t)
	model := &statementPromptBoundaryModelV3{}
	models := &bootstrap.ModelSet{Default: bootstrap.NewSwappableModel("test", "v3-statement-prompt", model)}
	_, err := runCharacterActivationChapter(context.Background(), cfg, st, models, "pg2_v3_statement_prompt", 1, boundary, domain.ProjectedPlanningContextV2{}, nil, 4)
	selectionMust(t, err)
	if model.checkedArbiters != 4 || model.revisionCalls != 1 || model.actorCalls == 0 || model.readiness.readiness.Load() == 0 {
		t.Fatalf("did not cover R1/R2, continuation, character and readiness prompts: arbiters=%d revisions=%d actors=%d readiness=%d", model.checkedArbiters, model.revisionCalls, model.actorCalls, model.readiness.readiness.Load())
	}
	for _, version := range []string{domain.WorldStimulusPacketVersion, domain.WorldStimulusPacketV2Version} {
		prompt, _, _, err := prepareCharacterArbitrationRequest(characterAgentChapterInputs{Stimulus: domain.WorldStimulusPacket{Version: version}}, nil, nil)
		selectionMust(t, err)
		if strings.Contains(prompt, "陈述与核验边界：") || strings.Contains(prompt, "硬不可能的证明边界：") || strings.Contains(prompt, "数值测量按真实时刻裁决：") {
			t.Fatalf("V3-only clarification leaked into historical request %s", version)
		}
	}
}
