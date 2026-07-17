package tools

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/rules"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func toolsTestRenderCapacity(targets ...int) *domain.ChapterRenderCapacity {
	scenes := make([]domain.ChapterRenderSceneUnit, 0, len(targets))
	total := 0
	for i, target := range targets {
		total += target
		scenes = append(scenes, domain.ChapterRenderSceneUnit{
			SceneID:          "scene-" + string(rune('a'+i)),
			TargetRunes:      target,
			POVObjective:     "林澈要拿到对方可以执行的明确答复",
			ActiveOpposition: "合同截止时间与对方的撤回动作同时施压",
			Turn:             "沈知遥指出回执日期与合同正文冲突",
			ExitConsequence:  "对方收不回回执，只能约到下一场重谈",
			ConcreteActionBeats: []string{
				"林澈把合同翻到盖章页",
				"沈知遥压住回执上的日期",
				"对方伸手抽文件却停在半空",
				"第四条完整计划证据不应进入精炼正文包",
			},
		})
	}
	return &domain.ChapterRenderCapacity{
		TotalTargetRunes:  total,
		SceneUnits:        scenes,
		AntiPaddingPolicy: "重复手续一笔带过；只加深选择、阻力、人物反应和可见后果，禁止同义复述。",
	}
}

func TestFocusedCausalSimulationSchemaIncludesRenderCapacity(t *testing.T) {
	causal := focusedCausalSimulationSchema()
	properties := causal["properties"].(map[string]any)
	renderCapacity, ok := properties["render_capacity"].(map[string]any)
	if !ok {
		t.Fatal("focused causal schema missing render_capacity")
	}
	required := renderCapacity["required"].([]string)
	for _, field := range []string{"total_target_runes", "scene_units", "anti_padding_policy"} {
		if !renderCapacityContainsString(required, field) {
			t.Fatalf("render_capacity schema does not require %s: %v", field, required)
		}
	}
	scenes := renderCapacity["properties"].(map[string]any)["scene_units"].(map[string]any)
	scene := scenes["items"].(map[string]any)
	sceneRequired := scene["required"].([]string)
	for _, field := range []string{
		"scene_id", "target_runes", "pov_objective", "active_opposition",
		"turn", "exit_consequence", "concrete_action_beats",
	} {
		if !renderCapacityContainsString(sceneRequired, field) {
			t.Fatalf("scene unit schema does not require %s: %v", field, sceneRequired)
		}
	}
	description := scene["properties"].(map[string]any)["target_runes"].(map[string]any)["description"].(string)
	if !strings.Contains(description, "300-1400") {
		t.Fatalf("scene target range is not visible to planner: %q", description)
	}
}

func TestFocusedAndLegacySchemasExposeExplicitArcTransitionContract(t *testing.T) {
	for name, causal := range map[string]map[string]any{
		"focused": focusedCausalSimulationSchema(),
		"legacy":  legacyCausalSimulationSchema(false),
	} {
		properties := causal["properties"].(map[string]any)
		transition, ok := properties["arc_transition_contract"].(map[string]any)
		if !ok {
			t.Fatalf("%s causal schema missing arc_transition_contract", name)
		}
		fields := transition["properties"].(map[string]any)
		for _, field := range []string{
			"incoming_consequence_id",
			"incoming_consequence_text",
			"consumed_by_cause",
			"outgoing_consequence_id",
			"outgoing_consequence_text",
		} {
			if _, exists := fields[field]; !exists {
				t.Fatalf("%s transition schema missing %s", name, field)
			}
		}
		required := transition["required"].([]string)
		for _, field := range []string{"outgoing_consequence_id", "outgoing_consequence_text"} {
			if !renderCapacityContainsString(required, field) {
				t.Fatalf("%s transition schema does not require %s: %v", name, field, required)
			}
		}
	}
}

func TestProjectAllRenderCapacityHardGateUsesUserRules(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.UserRules.Save(&rules.Snapshot{Structured: rules.Structured{
		ChapterWords: &rules.WordRange{Min: 2000, Max: 3300},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := st.Runtime.AcquirePipelineExecution(domain.PipelineExecutionLock{
		Mode:          domain.PipelineExecutionProjectAll,
		TargetChapter: 1,
		Owner:         "render-capacity-test",
	}); err != nil {
		t.Fatal(err)
	}

	plan := domain.ChapterPlan{Chapter: 1}
	if err := validateProjectAllRenderCapacity(st, plan); err == nil || !strings.Contains(err.Error(), "缺少 causal_simulation.render_capacity") {
		t.Fatalf("project-all accepted missing capacity: %v", err)
	}
	plan.CausalSimulation.RenderCapacity = toolsTestRenderCapacity(700, 700, 700)
	if err := validateProjectAllRenderCapacity(st, plan); err != nil {
		t.Fatalf("valid project-all capacity rejected: %v", err)
	}
	plan.CausalSimulation.RenderCapacity = toolsTestRenderCapacity(600, 600, 600)
	if err := validateProjectAllRenderCapacity(st, plan); err == nil || !strings.Contains(err.Error(), "user_rules.chapter_words=2000-3300") {
		t.Fatalf("under-budget capacity accepted: %v", err)
	}

	if err := st.Runtime.ReleasePipelineExecution("render-capacity-test"); err != nil {
		t.Fatal(err)
	}
	if err := validateProjectAllRenderCapacity(st, domain.ChapterPlan{Chapter: 1}); err != nil {
		t.Fatalf("legacy/non-project plan was made retroactively invalid: %v", err)
	}
}

func TestDraftRenderPacketCarriesOnlyLeanRenderCapacitySpine(t *testing.T) {
	packet := newDraftRenderPacket(domain.ChapterPlan{
		Chapter: 1,
		Title:   "合同翻到盖章页",
		CausalSimulation: domain.ChapterCausalSimulation{
			RenderCapacity: toolsTestRenderCapacity(800, 800, 800),
		},
	})
	if packet.RenderCapacity == nil || packet.RenderCapacity.TotalTargetRunes != 2400 || len(packet.RenderCapacity.SceneSpine) != 3 {
		t.Fatalf("draft packet lost render capacity: %+v", packet.RenderCapacity)
	}
	for _, scene := range packet.RenderCapacity.SceneSpine {
		if len(scene.ConcreteActionBeats) != domain.MinConcreteActionBeatsScene {
			t.Fatalf("scene spine is not compact: %+v", scene)
		}
	}

	raw, err := json.Marshal(packet.RenderCapacity)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "第四条完整计划证据") {
		t.Fatalf("draft packet leaked nonessential planning beat: %s", raw)
	}
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		t.Fatal(err)
	}
	if got, want := renderCapacitySortedMapKeys(root), []string{"anti_padding_policy", "scene_spine", "total_target_runes"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("render capacity leaked fields: got=%v want=%v", got, want)
	}
	first := root["scene_spine"].([]any)[0].(map[string]any)
	if got, want := renderCapacitySortedMapKeys(first), []string{
		"active_opposition", "concrete_action_beats", "exit_consequence", "pov_objective", "scene_id", "target_runes", "turn",
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("scene spine leaked fields: got=%v want=%v", got, want)
	}
}

func renderCapacityContainsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func renderCapacitySortedMapKeys(value map[string]any) []string {
	keys := make([]string, 0, len(value))
	for key := range value {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}
