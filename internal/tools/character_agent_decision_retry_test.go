package tools

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func TestSubmitCharacterDecisionRetriesPreserveOriginalReceipt(t *testing.T) {
	st := store.NewStore(t.TempDir())
	observation, err := domain.FinalizeCharacterObservationPacket(domain.CharacterObservationPacket{
		GenerationID: "pg2_retry", Chapter: 1, Round: 1, AgentID: "ca_lin", Character: "林澄",
		KnownFacts: []domain.CharacterAgentFact{{ID: "lamp", Kind: "known", Text: "灯已熄灭"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CharacterAgents.SaveObservation(observation); err != nil {
		t.Fatal(err)
	}
	args := map[string]any{
		"location": "值班室", "current_goal": "保全账本", "pressure": "即将停航",
		"available_options": []string{"带走账本", "留在原处"}, "decision": "带走账本",
		"decision_reason": "防止证据遗失", "intended_action": "把账本放入防水袋",
		"action_duration": "一分钟", "knowledge_refs": []string{"lamp"},
	}
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	tool := NewSubmitCharacterDecisionTool(st, observation)
	first, err := tool.Execute(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := st.CharacterAgents.LoadProposal(observation.GenerationID, observation.Chapter, observation.Round, observation.AgentID)
	if err != nil || stored == nil {
		t.Fatalf("missing saved proposal: %v", err)
	}
	const retries = 12
	var wg sync.WaitGroup
	for i := 0; i < retries; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := tool.Execute(context.Background(), raw)
			if err != nil || string(result) != string(first) {
				t.Errorf("identical retry changed receipt: %s %v", result, err)
			}
		}()
	}
	wg.Wait()
	args["decision"] = "留在原处"
	changed, _ := json.Marshal(args)
	if _, err := tool.Execute(context.Background(), changed); err == nil {
		t.Fatal("different intent was accepted as an idempotent retry")
	}
	after, err := st.CharacterAgents.LoadProposal(observation.GenerationID, observation.Chapter, observation.Round, observation.AgentID)
	if err != nil || after == nil || after.Digest != stored.Digest || after.SubmittedAt != stored.SubmittedAt {
		t.Fatalf("retry changed persisted evidence: %+v, %v", after, err)
	}
}
