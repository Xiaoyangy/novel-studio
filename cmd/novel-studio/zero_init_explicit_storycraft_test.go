package main

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

func explicitStorycraftFixture() zeroInitProject {
	first := domain.OutlineEntry{Chapter: 1, Title: "当前线索", CoreEvent: "FUTURE_SCENE_SECRET：后来全体在值班室作出预定选择。", Scenes: []string{"值班室的未来场景", "仓库柜台"}}
	return zeroInitProject{
		Name: "条件性开局", FirstChapter: first, Outline: []domain.OutlineEntry{first},
		FirstCast:     map[string]bool{"林澄": true, "周砚": true, "许岚": true},
		FirstMentions: map[string]int{"林澄": 1, "周砚": 1, "许岚": 1},
		BookWorld: &domain.BookWorld{Places: []domain.WorldPlace{
			{ID: "office", Name: "值班室"}, {ID: "shed", Name: "机修棚"}, {ID: "store", Name: "仓库"},
		}},
		Characters: []domain.Character{
			{Name: "林澄", Role: "主角", Tier: "core", Arc: "FUTURE_ARC_SECRET", InitialState: &domain.CharacterInitialState{
				Location: "仓库", CurrentGoal: "核对手中账页", Pressure: "交接期限临近", KnownFacts: []string{"眼前数字存在覆盖痕迹"},
			}},
			{Name: "周砚", Role: "机修员", Tier: "important", Description: "PRIVATE_DESC_SECRET：领用记录不属于本夜油箱。", Arc: "FUTURE_ARC_SECRET", InitialState: &domain.CharacterInitialState{
				Location: "机修棚", CurrentGoal: "完成检查", Pressure: "足额检查需要工时", KnownFacts: []string{"自己的接收记录为六十升"},
			}},
			{Name: "许岚", Role: "经营者及改账责任人", Tier: "important", Description: "PRIVATE_DESC_SECRET：她将签领六十升改成九十升，偷偷经手原页。", Arc: "FUTURE_ARC_SECRET", InitialState: &domain.CharacterInitialState{
				Location: "store", CurrentGoal: "保住经营", Pressure: "积水威胁存货", KnownFacts: []string{"自己清楚此前改过账"},
			}},
		},
	}
}

func TestExplicitStorycraftUsesOnlyAuthorConfirmedOpeningContact(t *testing.T) {
	project := explicitStorycraftFixture()
	dynamics := zeroInitDynamics(project)
	blueprints := zeroDialogueSceneBlueprints(project, dynamics.Characters)
	if len(blueprints) != 1 {
		t.Fatalf("missing conditional opening aid: %+v", blueprints)
	}
	blueprint := blueprints[0]
	if !strings.HasPrefix(blueprint.LocationAnchor, "仓库；") || blueprint.EntrySpeaker != "" {
		t.Fatalf("invented opening location or first speaker: %+v", blueprint)
	}
	var participants []string
	for _, tactic := range blueprint.ObjectiveTactics {
		participants = append(participants, tactic.Character)
	}
	if !reflect.DeepEqual(participants, []string{"林澄", "许岚"}) {
		t.Fatalf("chapter cast teleported offscreen mechanic into the opening: %v", participants)
	}
	raw, _ := json.Marshal(blueprint)
	for _, forbidden := range []string{"周砚", "FUTURE_SCENE_SECRET", "FUTURE_ARC_SECRET", "PRIVATE_DESC_SECRET", "因此会误读至少一次", "对方保住隐瞒"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("conditional opening aid fabricated %q: %s", forbidden, raw)
		}
	}
	if !strings.Contains(blueprint.FirstSpokenMoment, "实际选择") || !strings.Contains(blueprint.ModeReason, "不能直接复制") {
		t.Fatal("conditional candidates were presented as completed dialogue")
	}
	background := zeroGroundedWorldBackgroundPlan(project)
	if !strings.Contains(background.Layers.PhysicalSpace, "仓库 是第一章行动空间") || strings.Contains(background.Layers.PhysicalSpace, "值班室") {
		t.Fatalf("map insertion order overrode explicit opening location: %s", background.Layers.PhysicalSpace)
	}
	if zeroEnvironmentState(project)[0].Place != "仓库" || zeroGroundingDetails(project)[0].SceneAnchor != "仓库" || zeroRealitySupportPlan(project)[0].ChapterUse != "仓库" {
		t.Fatal("an auxiliary environment/reality template retained the unrelated first map entry")
	}
	stages := zeroOffscreenStage(project, dynamics.Characters)
	if len(stages) != len(project.Characters) {
		t.Fatalf("opening stage lost authored characters: %+v", stages)
	}
	for i, stage := range stages {
		if stage.Location != zeroExplicitOpeningPlace(project, project.Characters[i]) || stage.Evidence != "characters.json:initial_state" {
			t.Fatalf("offscreen template relocated an actor or lost its source: %+v", stage)
		}
		encoded, _ := json.Marshal(stage)
		if strings.Contains(string(encoded), "FUTURE_") || strings.Contains(string(encoded), "PRIVATE_DESC_SECRET") {
			t.Fatalf("offscreen stage copied future/private author description: %s", encoded)
		}
	}

	project.Characters[2].InitialState.Location = "机修棚"
	alone := zeroDialogueSceneBlueprints(project, dynamics.Characters)[0]
	if len(alone.ObjectiveTactics) != 1 || alone.ObjectiveTactics[0].Character != "林澄" || alone.EntrySpeaker != "" {
		t.Fatalf("isolated protagonist acquired an invented interlocutor: %+v", alone)
	}
}

func TestExplicitStorycraftDoesNotInventBeliefsOrExposeDescriptionAsAppearance(t *testing.T) {
	project := explicitStorycraftFixture()
	dynamics := zeroInitDynamics(project)
	before, _ := json.Marshal(project.Characters)
	arcs := zeroCharacterArcTests(project, dynamics.Characters)
	visual := zeroVisualDesign(project, dynamics.Characters)
	voices := dynamics.VoiceLogic
	emotions := zeroEmotionalLogic(project, dynamics.Characters)
	for i, arc := range arcs {
		if arc.Want != project.Characters[i].InitialState.CurrentGoal || arc.PressureTest != project.Characters[i].InitialState.Pressure {
			t.Fatalf("character-specific goals/pressure changed: %+v", arc)
		}
		if !strings.Contains(arc.CoreLie, "未声明错误信念") || !strings.Contains(arc.FirstMistake, "允许角色判断正确") {
			t.Fatalf("source without a false belief acquired a forced mistake: %+v", arc)
		}
	}
	raw, _ := json.Marshal(struct {
		Arcs     []domain.CharacterArcTest
		Visual   []domain.CharacterVisualDesign
		Voices   []domain.CharacterVoiceLogic
		Emotions []domain.CharacterEmotionalLogic
	}{arcs, visual, voices, emotions})
	for _, forbidden := range []string{"FUTURE_ARC_SECRET", "FUTURE_SCENE_SECRET", "PRIVATE_DESC_SECRET", "以为旧经验足以解释", "把异常先误判成普通流程", "偏尖、偏窄或骨感"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("explicit auxiliary design contains invented/secret fact %q: %s", forbidden, raw)
		}
	}
	visible, _ := json.Marshal(visual)
	if strings.Contains(string(visible), "改账责任人") || strings.Contains(string(visible), "六十升改成九十升") {
		t.Fatalf("author guilt became a visible first impression: %s", visible)
	}
	after, _ := json.Marshal(project.Characters)
	if string(after) != string(before) {
		t.Fatal("auxiliary generation rewrote author source")
	}
}

func TestExplicitStorycraftKeepsStructuralGateAndDemandsRealPlannerConcretization(t *testing.T) {
	project := explicitStorycraftFixture()
	dynamics := zeroInitDynamics(project)
	storycraft := zeroInitStorycraftPlan(project, dynamics)
	if !strings.Contains(storycraft.UsagePolicy, "条件性写作辅助") || !strings.Contains(storycraft.UsagePolicy, "不能用于跳过正式计划门禁") {
		t.Fatal("zero-init conditionals were allowed to replace the concrete sealed plan")
	}
	plan := domain.ChapterPlan{CausalSimulation: domain.ChapterCausalSimulation{
		DialogueBlueprints: storycraft.DialogueBlueprints, CharacterArcTests: storycraft.ArcTests, VisualDesign: storycraft.VisualDesign,
	}}
	for _, issue := range zeroValidateChapterPlan(plan) {
		if strings.Contains(issue, "dialogue_scene_blueprints") || strings.Contains(issue, "character_arc_tests") || strings.Contains(issue, "visual_design") {
			t.Fatalf("explicit aid dropped a required structural field: %s", issue)
		}
	}
	plan.CausalSimulation.DialogueBlueprints[0].ModeReason = ""
	issues := strings.Join(zeroValidateChapterPlan(plan), "\n")
	if !strings.Contains(issues, "dialogue_scene_blueprints[0] 未补足") {
		t.Fatal("existing structural dialogue gate was weakened")
	}
}
