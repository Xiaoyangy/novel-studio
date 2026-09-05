package store

import (
	"sync"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

func TestEnsureCharacterAgentCanonMigratesAcceptedFactsAndKeepsRenameIdentity(t *testing.T) {
	st := NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Characters.Save([]domain.Character{
		{Name: "林默", Aliases: []string{"小林"}, Role: "主角", Tier: "core"},
		{Name: "收银员", Role: "路人", Tier: "decorative"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.io.WriteJSON(characterContinuityJSON, domain.CharacterContinuityLedger{
		Version: 1, Entries: []domain.CharacterContinuityEntry{{Name: "林默", Aliases: []string{"小林"}, Tier: "core", LastSeenChapter: 4, CurrentFacts: []string{"左手受伤"}}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.EnsureCharacterAgentCanon(4); err != nil {
		t.Fatal(err)
	}
	registry, err := st.CharacterAgents.LoadRegistry()
	if err != nil || registry == nil {
		t.Fatalf("load registry: %v", err)
	}
	if len(registry.Entries) != 1 {
		t.Fatalf("decorative character should not be registered: %+v", registry.Entries)
	}
	first, ok := registry.Resolve("林默")
	if !ok {
		t.Fatal("migrated protagonist is missing")
	}
	memory, err := st.CharacterAgents.LoadCanonicalMemory(first.AgentID)
	if err != nil || memory == nil || len(memory.Facts) != 1 || memory.Facts[0].Text != "左手受伤" || !memory.Facts[0].Accepted {
		t.Fatalf("accepted continuity was not migrated: memory=%+v err=%v", memory, err)
	}

	if err := st.Characters.Save([]domain.Character{{Name: "林川", Aliases: []string{"林默", "小林"}, Role: "主角", Tier: "core"}}); err != nil {
		t.Fatal(err)
	}
	if err := st.EnsureCharacterAgentCanon(8); err != nil {
		t.Fatal(err)
	}
	registry, err = st.CharacterAgents.LoadRegistry()
	if err != nil {
		t.Fatal(err)
	}
	renamed, ok := registry.Resolve("林川")
	if !ok || renamed.AgentID != first.AgentID {
		t.Fatalf("rename changed stable identity: before=%s after=%+v", first.AgentID, renamed)
	}
}

func TestCharacterAgentProposalStorageIsImmutableAndIdempotent(t *testing.T) {
	st := NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	stimulus, _ := domain.FinalizeWorldStimulusPacket(domain.WorldStimulusPacket{Version: domain.WorldStimulusPacketVersion, GenerationID: "pg2_store", Chapter: 2, TimeWindow: "夜"})
	observation, _ := domain.FinalizeCharacterObservationPacket(domain.CharacterObservationPacket{
		Version: domain.CharacterObservationVersion, GenerationID: stimulus.GenerationID, Chapter: 2, Round: 1,
		AgentID: "ca_store", Character: "林默", StimulusDigest: stimulus.Digest,
		KnownFacts: []domain.CharacterAgentFact{{ID: "fact-1", Kind: "known", Text: "灯已灭"}},
	})
	proposal, err := domain.FinalizeCharacterDecisionProposal(domain.CharacterDecisionProposal{
		Version: domain.CharacterDecisionProposalVersion, GenerationID: observation.GenerationID, Chapter: 2, Round: 1,
		AgentID: observation.AgentID, Character: observation.Character, ObservationDigest: observation.Digest,
		Location: "楼梯间", CurrentGoal: "离开", Pressure: "脚步逼近", AvailableOptions: []string{"下楼", "躲藏"},
		Decision: "下楼", DecisionReason: "出口更近", IntendedAction: "沿楼梯下行", ActionDuration: "一分钟", KnowledgeRefs: []string{"fact-1"},
	}, observation)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CharacterAgents.SaveProposal(proposal, observation); err != nil {
		t.Fatal(err)
	}
	if err := st.CharacterAgents.SaveProposal(proposal, observation); err != nil {
		t.Fatalf("identical retry should be idempotent: %v", err)
	}
	var wg sync.WaitGroup
	errCh := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errCh <- st.CharacterAgents.SaveProposal(proposal, observation)
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatalf("concurrent idempotent proposal save failed: %v", err)
		}
	}
	proposal.Decision = "躲藏"
	proposal.Digest = ""
	proposal, err = domain.FinalizeCharacterDecisionProposal(proposal, observation)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CharacterAgents.SaveProposal(proposal, observation); err == nil {
		t.Fatal("different proposal overwrote immutable submission")
	}
}

func TestCharacterAgentSuccessorPlanIsContentAddressedAndRecoverable(t *testing.T) {
	st := NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	plan, err := domain.FinalizeCharacterAgentSuccessorPlan(domain.CharacterAgentSuccessorPlan{
		Version: domain.CharacterAgentSuccessorPlanVersion, ParentGenerationID: "pg2_parent", BaseCanonChapter: 0,
		TriggerChapter: 1, ArcFirstChapter: 1, ArcLastChapter: 2, BookLastChapter: 10,
		ArbitrationDigest: "sha256:arbitration", AcceptedCanonRoot: "sha256:canon", EndingDirection: "完成和解",
		NonNegotiables: []string{"不能复活死者"}, HardContractConflicts: []string{"第二章前必须交付证据"},
		RevisedChapters: []domain.OutlineEntry{
			{Chapter: 1, Title: "拒绝", CoreEvent: "主角拒绝交易", Hook: "改走备用路径", Scenes: []string{"仓库对峙"}},
			{Chapter: 2, Title: "替代", CoreEvent: "盟友交付证据", Hook: "追踪者现身", Scenes: []string{"码头交接"}},
		},
		ArchitectSummary: "保留拒绝交易的选择，用盟友路径兑现交付。",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CharacterAgents.SaveSuccessorPlan(plan, true); err != nil {
		t.Fatal(err)
	}
	if err := st.CharacterAgents.SaveSuccessorPlan(plan, true); err != nil {
		t.Fatalf("idempotent successor save failed: %v", err)
	}
	loaded, err := st.CharacterAgents.LoadCurrentSuccessorPlan()
	if err != nil || loaded == nil || loaded.Digest != plan.Digest {
		t.Fatalf("current successor plan mismatch: plan=%+v err=%v", loaded, err)
	}
}
