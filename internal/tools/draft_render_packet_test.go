package tools

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

func TestDraftProfileBuildsSelectiveRenderPacket(t *testing.T) {
	plan := domain.ChapterPlan{
		Chapter:  1,
		Title:    "回县城的第一顿饭",
		Goal:     "让主角拿到第一笔可用额度",
		Conflict: "旧债不能直接偿还",
		Contract: domain.ChapterContract{
			RequiredBeats: []string{
				"主角确认额度真实并完成第一笔改善消费",
				"赵航必须用“呱，”打断饭桌说教",
			},
			ForbiddenMoves: []string{"公开系统"},
		},
		CausalSimulation: domain.ChapterCausalSimulation{
			ReaderRetentionPlan: domain.ReaderRetentionPlan{
				SurfaceBeats:  []domain.RetentionSurfaceBeat{{MustShow: "饭桌压力"}, {MustShow: "消费见效"}, {MustShow: "项目核验"}},
				LatentContext: []string{"亲戚私下另有打算"},
				CutOrCompress: []string{"安装流程一句带过"},
			},
			TrendLanguage: []domain.TrendLanguagePlan{{Item: "呱，……", CharacterCarrier: "赵航"}},
			VoiceLogic: []domain.CharacterVoiceLogic{{
				Character: "赵航", SpeechPrinciple: "先拆台后护朋友", HiddenSubtext: "替朋友挡话",
				KnowledgeBoundary: "不知道系统", DictionAndRhythm: "短句", SilenceOrAction: "每句先夹菜",
			}},
			DialogueBlueprints: []domain.DialogueSceneBlueprint{{
				SceneID: "dinner", DialogueMode: "group_council", ScenePressure: "失业羞耻", DialogueObjective: "替主角解围",
				TurnProgression: []domain.DialogueTurnDesign{{Speaker: "赵航", SurfaceLineFunction: "反问", ActionBeat: "抬眼夹菜", NextPressure: "亲戚失语"}},
			}},
		},
	}
	working := map[string]any{
		"chapter_plan":             &plan,
		"causal_simulation":        plan.CausalSimulation,
		"causal_simulation_policy": "full",
	}
	result := map[string]any{
		"working_memory":           working,
		"chapter_plan":             &plan,
		"causal_simulation":        plan.CausalSimulation,
		"causal_simulation_policy": "full",
		"chapter_world_simulation": map[string]any{
			"status":              "ready",
			"simulation_id":       "sim-1",
			"character_decisions": []domain.CharacterWorldDecision{{Character: "亲戚"}},
			"protagonist_projection": domain.ProtagonistDecisionProjection{
				Protagonist: "林澈", ObservableEffects: []string{"亲戚当面催工作"}, HiddenPressures: []string{"亲戚私下联络媒人"},
				ChosenDecision: "离席验证额度", DecisionReason: "不想继续被比较", CausalChain: []string{"饭桌受压", "尝试花钱"},
			},
		},
	}

	applyChapterContextProfile(result, "draft")

	packet, ok := result["render_packet"].(draftRenderPacket)
	if !ok {
		t.Fatalf("render_packet type = %T", result["render_packet"])
	}
	if len(packet.MandatoryBeats) != 1 || len(packet.OptionalStyleBeats) != 1 {
		t.Fatalf("mandatory/optional = %#v / %#v", packet.MandatoryBeats, packet.OptionalStyleBeats)
	}
	raw, err := json.Marshal(packet)
	if err != nil {
		t.Fatal(err)
	}
	serialized := string(raw)
	for _, forbidden := range []string{"turn_progression", "action_beat", "proof_on_page", "latent_context", "hidden_pressures", "每句先夹菜"} {
		if strings.Contains(serialized, forbidden) {
			t.Fatalf("draft render packet leaked %q: %s", forbidden, serialized)
		}
	}
	if _, exists := result["causal_simulation"]; exists {
		t.Fatal("draft profile must hide full causal_simulation")
	}
	world := result["chapter_world_simulation"].(map[string]any)
	if _, exists := world["character_decisions"]; exists {
		t.Fatal("draft profile must hide all-character decisions")
	}
	projection := world["protagonist_projection"].(draftProtagonistProjection)
	if len(projection.ObservableEffects) != 1 {
		t.Fatalf("observable effects lost: %#v", projection)
	}
	leanPlan := result["chapter_plan"].(map[string]any)
	if _, exists := leanPlan["causal_simulation"]; exists {
		t.Fatal("lean chapter plan must not embed causal_simulation")
	}
}

func TestNonDraftProfileKeepsPlanningContext(t *testing.T) {
	plan := &domain.ChapterPlan{Chapter: 2, CausalSimulation: domain.ChapterCausalSimulation{ProtagonistDecision: "继续经营"}}
	result := map[string]any{"chapter_plan": plan, "causal_simulation": plan.CausalSimulation}
	applyChapterContextProfile(result, "planning")
	if result["chapter_plan"] != plan {
		t.Fatal("planning profile was unexpectedly rewritten")
	}
	if _, exists := result["causal_simulation"]; !exists {
		t.Fatal("planning profile lost causal_simulation")
	}
}
