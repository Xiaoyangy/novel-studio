package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/voocel/agentcore"
)

func TestCharacterSourceReferencesNeverBecomeModelVisibleAuthorFacts(t *testing.T) {
	const unread = "world_codex.sections[history_timeline].content：R02原记J17及六十升"
	const future = "working_memory.future_outline_window[chapter=3].scenes：未来具体兑现动作"
	const otherSecret = "旁人尚未告知的私密经营转油细节"
	amount := 110.0
	world, err := domain.FinalizeWorldPhysicalStateV2(domain.WorldPhysicalStateV2{Version: domain.WorldPhysicalStateV2Version,
		Resources: []domain.WorldResourceBalanceV2{{ResourceID: "res_0000000000000001", Name: "R02作者原件", ReadableFacts: []domain.ResourceReadableFactV2{{ID: "unread", Text: "未读取的原件正文"}}}, {ResourceID: "res_0000000000000002", Name: "手电", Unit: "分钟", ActualAmount: &amount}},
		Actors: []domain.CharacterPhysicalStateV2{{AgentID: "ca_lin", Character: "林澄", Location: "油料仓库", Resources: []domain.CharacterResourceHoldingV2{
			{ResourceID: "res_0000000000000001", PerceivedName: "外清单列有R02，袋内未读", Access: "none", EvidenceRefs: []string{unread}, Perception: domain.ResourcePerceptionV2{Kind: "unknown", EvidenceRefs: []string{otherSecret}}},
			{ResourceID: "res_0000000000000002", PerceivedName: "本人手电", PerceivedUnit: "分钟", Access: "exclusive", EvidenceRefs: []string{future}, Perception: domain.ResourcePerceptionV2{Kind: "estimated", EstimateMin: &amount, EstimateMax: &amount, EvidenceRefs: []string{"本人已知手电原有110分钟"}}},
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	worldBefore, _ := json.Marshal(world)
	canonicalMemory := []domain.CharacterAgentMemoryFact{{ID: "mem_safe", Chapter: 1, Kind: "accepted_continuity", Text: "只接收过袋外清单", SourceDigest: unread, KnowledgeRefs: []string{future, otherSecret}, Accepted: true}}
	memoryBefore, _ := json.Marshal(canonicalMemory)
	stimulus, err := domain.FinalizeWorldStimulusPacket(domain.WorldStimulusPacket{Version: domain.WorldStimulusPacketV2Version, GenerationID: "pg2_safe_refs", Chapter: 1, TimeWindow: "本轮", PhysicalState: &world, Sources: []string{domain.CharacterSourceRefPolicyV2}})
	if err != nil {
		t.Fatal(err)
	}
	observation := domain.CharacterObservationPacket{Version: domain.CharacterObservationV2Version, GenerationID: "pg2_safe_refs", Chapter: 1, Round: 1, AgentID: "ca_lin", Character: "林澄", CurrentGoal: "核对已知信息", Pressure: "等待真实原件", StimulusDigest: "sha256:" + strings.Repeat("a", 64), MemoryRoot: "sha256:" + strings.Repeat("b", 64), Sources: []string{future}, Memory: canonicalMemory,
		KnownFacts:      []domain.CharacterAgentFact{{ID: "fact-1", Kind: "known", Text: "封袋内尚未读取", Source: unread}},
		PerceivedEvents: []domain.CharacterAgentFact{{ID: "own-event", Kind: "own", Text: "目前在柜台", Source: otherSecret}},
		PublicRules:     []domain.CharacterAgentFact{{ID: "public-rule", Kind: "world_rule_character_view", Text: "接触原件需要实际许可", Source: future, Visibility: "formal"}},
	}
	observation.StimulusDigest = stimulus.Digest
	profile := characterAgentProfile{Character: domain.Character{Name: "林澄"}, Record: domain.CharacterAgentRecord{AgentID: "ca_lin", Character: "林澄"}}
	if err := applyCharacterPhysicalObservation(&observation, profile, stimulus); err != nil {
		t.Fatal(err)
	}
	observation, err = domain.FinalizeCharacterObservationPacket(observation)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(observation)
	for _, forbidden := range []string{unread, "六十升", "future_outline_window", otherSecret, "未读取的原件正文", "actual_amount"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("author provenance leaked into observation: %q", forbidden)
		}
	}
	if observation.ResourceViews[0].Access != "none" || observation.ResourceViews[0].Perception.Kind != "unknown" || observation.ResourceViews[1].Perception.EstimateMin == nil || *observation.ResourceViews[1].Perception.EstimateMin != 110 {
		t.Fatal("opaque provenance changed actual visible knowledge or lawful estimate")
	}
	for _, ref := range append(observation.ResourceViews[0].EvidenceRefs, observation.ResourceViews[0].Perception.EvidenceRefs...) {
		if _, allowed := observation.AllowedFactIDs()[ref]; !allowed || !strings.HasPrefix(ref, "src_") {
			t.Fatal("visible-view provenance cannot be cited")
		}
	}
	if _, allowed := observation.AllowedFactIDs()[unread]; allowed {
		t.Fatal("raw author assertion became an allowed character fact")
	}
	projectCharacterObservationSourcesV2(&observation)
	again, _ := json.Marshal(observation)
	if string(again) != string(encoded) {
		t.Fatal("source view projection is not byte-idempotent")
	}
	worldAfter, _ := json.Marshal(world)
	memoryAfter, _ := json.Marshal(canonicalMemory)
	if string(worldBefore) != string(worldAfter) || string(memoryBefore) != string(memoryAfter) {
		t.Fatal("safe observation rewrote author/canonical sources")
	}

	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.CharacterAgents.SaveObservation(observation); err != nil {
		t.Fatal(err)
	}
	if err := st.CharacterAgents.SaveStimulus(stimulus); err != nil {
		t.Fatal(err)
	}
	model := &characterSourceBoundaryModel{observation: observation, forbidden: []string{unread, "六十升", "future_outline_window", otherSecret, "未读取的原件正文"}}
	if _, err := runCharacterProposalRoundWithModel(context.Background(), bootstrap.Config{}, st, model, map[string]domain.CharacterObservationPacket{observation.AgentID: observation}, []string{observation.AgentID}, 1); err != nil {
		t.Fatal(err)
	}
	if model.calls.Load() == 0 {
		t.Fatal("test did not exercise actual character-model input")
	}
	proposal, err := st.CharacterAgents.LoadProposal(observation.GenerationID, 1, 1, observation.AgentID)
	if err != nil || proposal == nil || proposal.ObservationDigest != observation.Digest {
		t.Fatalf("safe handle proposal was not persisted: %v", err)
	}
	bad := observation
	bad.Sources = []string{domain.CharacterSourceRefPolicyV2, unread}
	bad.Digest, err = domain.ComputeCharacterObservationDigest(bad)
	if err != nil {
		t.Fatal(err)
	}
	beforeCalls := model.calls.Load()
	if _, err := runCharacterProposalRoundWithModel(context.Background(), bootstrap.Config{}, st, model, map[string]domain.CharacterObservationPacket{bad.AgentID: bad}, []string{bad.AgentID}, 1); err == nil || model.calls.Load() != beforeCalls {
		t.Fatal("rehashing a tagged raw source bypassed the pre-model boundary")
	}
	bad.Sources = []string{unread} // Removing the policy cannot opt into historical compatibility.
	bad.Digest, err = domain.ComputeCharacterObservationDigest(bad)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runCharacterProposalRoundWithModel(context.Background(), bootstrap.Config{}, st, model, map[string]domain.CharacterObservationPacket{bad.AgentID: bad}, []string{bad.AgentID}, 1); err == nil || model.calls.Load() != beforeCalls {
		t.Fatal("dropping the source policy bypassed the pre-model stimulus binding")
	}
}

func TestCharacterSourceNewPolicyRejectsOldV2InputsWithoutRewriting(t *testing.T) {
	const oldProtocol = "sha256:3a084bfef87d0004ddb6d620cbb2da8dc7b138ca7064ce26b01320196734368f"
	if CharacterAgentProtocolDigestForVersion(domain.CharacterAgentDecisionProtocolV2Version) == oldProtocol {
		t.Fatal("source projection policy did not change v2 protocol digest")
	}
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	physical, err := domain.FinalizeWorldPhysicalStateV2(domain.WorldPhysicalStateV2{Version: domain.WorldPhysicalStateV2Version, Actors: []domain.CharacterPhysicalStateV2{{AgentID: "ca_old", Character: "角色", Location: "A", Resources: []domain.CharacterResourceHoldingV2{}}}})
	if err != nil {
		t.Fatal(err)
	}
	stimulus, err := domain.FinalizeWorldStimulusPacket(domain.WorldStimulusPacket{Version: domain.WorldStimulusPacketV2Version, GenerationID: "pg2_old_source_policy", Chapter: 1, TimeWindow: "旧轮", PhysicalState: &physical, Sources: []string{"character-agent-protocol:" + oldProtocol}})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CharacterAgents.SaveStimulus(stimulus); err != nil {
		t.Fatal(err)
	}
	before, err := store.DirectoryContentRoot(st.Dir())
	if err != nil {
		t.Fatal(err)
	}
	if err := requireCharacterAgentGenerationProtocol(st, stimulus.GenerationID, 1, domain.CharacterAgentDecisionProtocolV2Version); err == nil || !strings.Contains(err.Error(), "--pipeline") {
		t.Fatalf("old v2 inputs were silently reused: %v", err)
	}
	after, err := store.DirectoryContentRoot(st.Dir())
	if err != nil || before != after {
		t.Fatal("protocol rejection rewrote historical input")
	}
}

// Opt-in real-source audit exercises the production read-only builder without
// a model, receipt writes, or reuse of the contaminated generation's inputs.
func TestCharacterSourceActualReadonlyAudit(t *testing.T) {
	dir := os.Getenv("NOVEL_STUDIO_SOURCE_REF_AUDIT_DIR")
	if dir == "" {
		t.Skip("set explicit readonly source audit directory")
	}
	before, err := store.DirectoryContentRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	observations, err := BuildCharacterObservationsForProjectedState(store.NewStore(dir), "pg2_source_refs_readonly_audit", 1, domain.ProjectedPlanningContextV2{})
	if err != nil {
		t.Fatal(err)
	}
	linFound := false
	for _, observation := range observations {
		raw, _ := json.Marshal(observation)
		if strings.Contains(string(raw), "future_outline_window") || strings.Contains(string(raw), "R02原记J17及六十升") {
			t.Fatal("raw author provenance remains in fresh observation")
		}
		if !domain.HasCharacterSourceRefPolicyV2(observation.Sources) {
			t.Fatal("fresh observation lacks source projection policy")
		}
		if observation.Character == "林澄" {
			linFound = true
			for _, view := range observation.ResourceViews {
				if view.ResourceID == "res_60a6a07ab7da8207" && (view.Access != "none" || view.Perception.Kind != "unknown") {
					t.Fatal("R02 visibility was upgraded by source handle")
				}
			}
		}
	}
	if !linFound || len(observations) != 3 {
		t.Fatal("real source audit did not cover intended three characters")
	}
	after, err := store.DirectoryContentRoot(dir)
	if err != nil || before != after {
		t.Fatal("readonly source audit changed files")
	}
}

type characterSourceBoundaryModel struct {
	observation domain.CharacterObservationPacket
	forbidden   []string
	calls       atomic.Int32
}

func (m *characterSourceBoundaryModel) Generate(_ context.Context, messages []agentcore.Message, _ []agentcore.ToolSpec, _ ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	m.calls.Add(1)
	for _, message := range messages {
		for _, forbidden := range m.forbidden {
			if strings.Contains(message.TextContent(), forbidden) {
				return nil, fmt.Errorf("model received author provenance: %q", forbidden)
			}
		}
	}
	args, _ := json.Marshal(map[string]any{"location": m.observation.Location, "current_goal": "核对已知信息", "pressure": "时间有限", "available_options": []string{"先询问", "等候"}, "decision": "先询问，不假定原件内容", "decision_reason": "只见外清单且袋内未读", "intended_action": "向现场保管人询问核验来源", "action_duration": "一分钟", "knowledge_refs": []string{"fact-1", m.observation.ResourceViews[0].EvidenceRefs[0]}})
	return &agentcore.LLMResponse{Message: agentcore.Message{Role: agentcore.RoleAssistant, StopReason: agentcore.StopReasonToolUse, Content: []agentcore.ContentBlock{agentcore.ToolCallBlock(agentcore.ToolCall{ID: "safe-decision", Name: "submit_character_decision", Args: args})}}}, nil
}
func (m *characterSourceBoundaryModel) GenerateStream(ctx context.Context, messages []agentcore.Message, specs []agentcore.ToolSpec, options ...agentcore.CallOption) (<-chan agentcore.StreamEvent, error) {
	response, err := m.Generate(ctx, messages, specs, options...)
	if err != nil {
		return nil, err
	}
	events := make(chan agentcore.StreamEvent, 1)
	events <- agentcore.StreamEvent{Type: agentcore.StreamEventDone, Message: response.Message, StopReason: response.Message.StopReason}
	close(events)
	return events, nil
}
func (*characterSourceBoundaryModel) SupportsTools() bool { return true }
