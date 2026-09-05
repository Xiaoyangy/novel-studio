package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func TestSubmitCharacterAgentSuccessorPlanOnlyRewritesSoftFields(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	original := []domain.OutlineEntry{
		{Chapter: 3, Title: "旧三", CoreEvent: "旧事件三", Hook: "旧钩子三", Scenes: []string{"旧场景三"}, ContractRefs: []domain.StoryContractRef{{ID: "hard-3"}}},
		{Chapter: 4, Title: "旧四", CoreEvent: "旧事件四", Hook: "旧钩子四", Scenes: []string{"旧场景四"}, ContractRefs: []domain.StoryContractRef{{ID: "hard-4"}}},
	}
	base := domain.CharacterAgentSuccessorPlan{
		Version: domain.CharacterAgentSuccessorPlanVersion, ParentGenerationID: "pg2_failed", BaseCanonChapter: 0,
		TriggerChapter: 3, ArcFirstChapter: 1, ArcLastChapter: 4, BookLastChapter: 12,
		ArbitrationDigest: "sha256:arbiter", AcceptedCanonRoot: "sha256:canon", EndingDirection: "真相公开",
		NonNegotiables: []string{"保留结局"}, HardContractConflicts: []string{"第四章前交付证据"},
	}
	tool := NewSubmitCharacterAgentSuccessorPlanTool(st, base, original)
	args, _ := json.Marshal(map[string]any{
		"architect_summary": "保留拒绝交易的选择，改由备用路线交付。",
		"revised_chapters": []map[string]any{
			{"chapter": 3, "title": "新三", "core_event": "选择备用路线", "hook": "追兵抵达", "scenes": []string{"码头改道"}},
			{"chapter": 4, "title": "新四", "core_event": "证据按时送达", "hook": "新线索出现", "scenes": []string{"完成交付"}},
		},
	})
	if _, err := tool.Execute(context.Background(), args); err != nil {
		t.Fatal(err)
	}
	plan, err := st.CharacterAgents.LoadCurrentSuccessorPlan()
	if err != nil || plan == nil {
		t.Fatalf("load successor plan: %+v %v", plan, err)
	}
	if len(plan.RevisedChapters) != 2 || len(plan.RevisedChapters[0].ContractRefs) != 1 || plan.RevisedChapters[0].ContractRefs[0].ID != "hard-3" {
		t.Fatalf("host-bound contract refs were not preserved: %+v", plan.RevisedChapters)
	}

	badArgs, _ := json.Marshal(map[string]any{
		"architect_summary": "试图插章",
		"revised_chapters":  []map[string]any{{"chapter": 99, "title": "插章", "core_event": "越权", "hook": "越权", "scenes": []string{"越权"}}},
	})
	if _, err := tool.Execute(context.Background(), badArgs); err == nil {
		t.Fatal("successor tool accepted an added/renumbered chapter")
	}
}
