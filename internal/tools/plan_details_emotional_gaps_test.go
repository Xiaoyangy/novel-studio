package tools

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

func TestIndependentPlanDetailsReportsEmotionalCoverageBeforeFinalize(t *testing.T) {
	st, sim, token := independentPlanningFixture(t, "林澄")
	if err := st.Characters.Save([]domain.Character{{Name: "林澄", Role: "主角", Tier: "core"}, {Name: "许岚", Role: "配角"}}); err != nil {
		t.Fatal(err)
	}
	cast, err := st.WorldSim.LoadSimulationCast()
	if err != nil || len(cast.Assignments) != 0 || sim.Version != 2 {
		t.Fatal("fixture must use a complete independent v2 receipt with no legacy cast")
	}
	if err := validateStoredCharacterAgentProtocol(st, *sim); err != nil {
		t.Fatal(err)
	}
	structureArgs, _ := json.Marshal(map[string]any{"chapter": 1, "title": "核验", "goal": "林澄核验原件", "conflict": "林澄只能依据实际读到的内容判断", "hook": "林澄保留未核实的问题"})
	if _, err := NewPlanStructureTool(st).Execute(context.Background(), structureArgs); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]any{"chapter": 1, "causal_simulation": map[string]any{"context_sources": []string{token}, "project_promise": "在已核验范围内保留疑点"}})
	result, err := NewPlanDetailsTool(st).Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	var staged map[string]any
	if err := json.Unmarshal(result, &staged); err != nil {
		t.Fatal(err)
	}
	assertEmotionalGap := func(view map[string]any, missing bool) {
		t.Helper()
		gaps := strings.Join(stringSliceFromAny(view["gap_summary"]), " | ")
		batches := strings.Join(stringSliceFromAny(view["recommended_batches"]), " | ")
		if strings.Contains(gaps, "emotional_logic missing characters: 林澄") != missing || strings.Contains(batches, "本章当前必补：emotional_logic missing characters: 林澄") != missing {
			t.Fatalf("unexpected emotional guidance, missing=%t: gaps=%s batches=%s", missing, gaps, batches)
		}
		if strings.Contains(gaps, "emotional_logic missing characters: 许岚") {
			t.Fatal("optional counterpart emotional matrix became mandatory")
		}
	}
	assertEmotionalGap(staged, true)
	view, ok, err := NewContextTool(st, References{}, "").stagedPlanRepairContext(1, 1, false)
	if err != nil || !ok {
		t.Fatalf("staged context: %v", err)
	}
	assertEmotionalGap(view, true)
	partial, err := st.Drafts.LoadChapterPlanPartial(1)
	if err != nil {
		t.Fatal(err)
	}
	merged := partial["causal_simulation"].(map[string]any)
	plan, err := chapterPlanFromPartial(1, partial, merged)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateChapterPrewriteSimulation(st, plan, false); err == nil || !strings.Contains(err.Error(), "causal_simulation.emotional_logic missing characters: 林澄") {
		t.Fatalf("early diagnostic does not match existing final validator: %v", err)
	}
	emotion := domain.CharacterEmotionalLogic{Character: "林澄", ImmediateState: "站在柜台核对", PrimaryEmotion: "担心归责", EmotionalTrigger: "签名旁数量异常", GoalAppraisal: "先核清证据", RegulationStrategy: "压住指控冲动", EmotionLedAction: "请求保留原页", EvidenceInScene: []string{"只指认实际看到的笔画"}}
	args, _ = json.Marshal(map[string]any{"chapter": 1, "causal_simulation": map[string]any{"emotional_logic": []domain.CharacterEmotionalLogic{emotion}}})
	result, err = NewPlanDetailsTool(st).Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(result, &staged); err != nil {
		t.Fatal(err)
	}
	assertEmotionalGap(staged, false)
	view, ok, err = NewContextTool(st, References{}, "").stagedPlanRepairContext(1, 1, false)
	if err != nil || !ok {
		t.Fatalf("filled staged context: %v", err)
	}
	assertEmotionalGap(view, false)
}

func TestPlanDetailsEmotionalCoverageLeavesOptionalProjectsAndRecordsAlone(t *testing.T) {
	st := newPhaseTestStore(t)
	partial := map[string]any{"structure": map[string]any{"chapter": 1}}
	merged := map[string]any{"project_promise": "无已识别主角的规划"}
	before, _ := json.Marshal(merged)
	if got := planEmotionalLogicMissingCharacters(st, nil); len(got) != 0 {
		t.Fatalf("unknown protagonist was invented: %v", got)
	}
	if gaps := strings.Join(planDetailsGapSummary(st, 1, partial, merged), " | "); strings.Contains(gaps, "emotional_logic") {
		t.Fatalf("optional coverage became required: %s", gaps)
	}
	if !reflect.DeepEqual(planDetailsRecommendedBatchesForState(st, 1, partial, merged), planDetailsRecommendedBatches()) {
		t.Fatal("optional project gained a mandatory batch")
	}
	if err := st.Characters.Save([]domain.Character{{Name: "林澄", Role: "主角"}, {Name: "许岚", Role: "配角"}}); err != nil {
		t.Fatal(err)
	}
	if got := planEmotionalLogicMissingCharacters(st, []domain.CharacterEmotionalLogic{{Character: "许岚"}}); !reflect.DeepEqual(got, []string{"林澄"}) {
		t.Fatalf("counterpart record hid the real missing protagonist: %v", got)
	}
	if got := planEmotionalLogicMissingCharacters(st, []domain.CharacterEmotionalLogic{{Character: "林澄"}}); len(got) != 0 {
		t.Fatalf("completed coverage was requested again: %v", got)
	}
	after, _ := json.Marshal(merged)
	if string(before) != string(after) {
		t.Fatal("read-only gap computation changed the staged plan")
	}
}
