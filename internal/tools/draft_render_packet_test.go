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
			ForbiddenMoves: []string{"公开系统", "普通对白不得使用指定设计腔措辞"},
			ContinuityChecks: []string{
				"沈知遥到场时首单已经发生。",
				"章末具体锚点必须是票据、测试记录和九点要求。",
			},
		},
		CausalSimulation: domain.ChapterCausalSimulation{
			EmotionalLogic: []domain.CharacterEmotionalLogic{{
				Character: "林澈", ImmediateState: "被亲戚追问后表面还能笑", PrimaryEmotion: "难堪和警惕",
				EmotionalTrigger: "父母越替他挡话，他越清楚自己让家里担心", GoalAppraisal: "争辩不能恢复体面，必须做出结果",
				RegulationStrategy: "把翻身冲动压成小额试验", EmotionLedAction: "不炫耀额度，离席验证",
				EvidenceInScene: []string{"护住被添满的碗", "没有在饭桌公开系统", "第三条不得泄露"},
			}},
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
			EndingContract: domain.EndingConsequenceContract{
				ConcreteAnchor:  "票据和测试记录",
				Consequence:     "林澈接下明早九点的责任",
				NextChapterPull: "下一家摊主主动求助",
				WhyNotUI:        "不要用系统数字收尾",
			},
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
				ChosenDecision: "离席验证额度", DecisionReason: "不想继续被比较；后续分析不进正文", CausalChain: []string{"饭桌受压", "尝试花钱"},
				AvailableOptions: []string{"当场炫耀", "离席验证"}, PlanConstraints: []string{"先点按钮再付款"},
			},
		},
	}

	applyChapterContextProfile(result, "draft")

	packet, ok := result["render_packet"].(draftRenderPacket)
	if !ok {
		t.Fatalf("render_packet type = %T", result["render_packet"])
	}
	if len(packet.MandatoryBeats) != 1 || len(packet.OptionalStyleBeats) != 0 {
		t.Fatalf("mandatory/optional = %#v / %#v", packet.MandatoryBeats, packet.OptionalStyleBeats)
	}
	if len(packet.ForbiddenMoves) != 2 {
		t.Fatalf("hard project prohibitions must survive render projection: %#v", packet.ForbiddenMoves)
	}
	for _, policy := range []string{
		packet.PlanTranslationPolicy,
		packet.ReaderRegisterPolicy,
		packet.InterfaceCompression,
		packet.ScenePurposePolicy,
		packet.SpokenLanguagePolicy,
		packet.EmotionalRenderPolicy,
	} {
		if strings.TrimSpace(policy) == "" {
			t.Fatalf("render translation policy missing: %#v", packet)
		}
	}
	if len(packet.EmotionalLenses) != 0 {
		t.Fatalf("scene evidence must remain in planning, not prose packet: %#v", packet.EmotionalLenses)
	}
	if len(packet.ContinuityChecks) != 0 {
		t.Fatalf("continuity checklist must remain an outer validation concern: %#v", packet.ContinuityChecks)
	}
	if packet.EndingContract.Consequence != "" || packet.EndingAnchorCandidate != "" {
		t.Fatalf("hook already carries the ending pull; detailed ending choreography must stay out: %#v / %q", packet.EndingContract, packet.EndingAnchorCandidate)
	}
	raw, err := json.Marshal(packet)
	if err != nil {
		t.Fatal(err)
	}
	serialized := string(raw)
	for _, forbidden := range []string{
		"turn_progression", "action_beat", "proof_on_page", "latent_context", "hidden_pressures", "每句先夹菜",
		"scene_objective", "sentence_rhythm_policy", "review_checks", "candidate_beats", "dialogue_scenes",
		"饭桌压力", "被亲戚追问后表面还能笑", "票据和测试记录", "饭桌受压", "当场炫耀", "先点按钮再付款",
	} {
		if strings.Contains(serialized, forbidden) {
			t.Fatalf("draft render packet leaked %q: %s", forbidden, serialized)
		}
	}
	if len(raw) > 5000 {
		t.Fatalf("prose-facing packet regrew into a planning dossier: %d bytes", len(raw))
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
	if len(projection.CausalChain) != 0 || len(projection.AvailableOptions) != 0 || len(projection.PlanConstraints) != 0 || projection.DecisionReason != "不想继续被比较" {
		t.Fatalf("draft projection still carries planning choreography: %#v", projection)
	}
	leanPlan := result["chapter_plan"].(map[string]any)
	if _, exists := leanPlan["causal_simulation"]; exists {
		t.Fatal("lean chapter plan must not embed causal_simulation")
	}
}

func TestDraftRenderPacketKeepsRelationshipLensButDropsDialogueChoreography(t *testing.T) {
	plan := domain.ChapterPlan{CausalSimulation: domain.ChapterCausalSimulation{
		DialogueBlueprints: []domain.DialogueSceneBlueprint{
			{SceneID: "dinner", DialogueMode: "public_confrontation", RelationshipFrame: "亲友争夺体面"},
			{SceneID: "system", DialogueMode: "mediated_exchange", RelationshipFrame: "用户与系统"},
			{SceneID: "phone", DialogueMode: "mediated_exchange", RelationshipFrame: "朋友隔着电话互相试探"},
			{SceneID: "supplier", DialogueMode: "logistics_under_stress", RelationshipFrame: "付款方与供货方"},
			{SceneID: "review", DialogueMode: "status_report", RelationshipFrame: "旧识在职责与关心之间"},
			{SceneID: "settlement", DialogueMode: "mediated_exchange", RelationshipFrame: "系统结算"},
			{SceneID: "payoff", DialogueMode: "private_reconciliation", RelationshipFrame: "朋友终于停止试探", ExitBeat: "对方替主角留了门"},
		},
	}}

	packet := newDraftRenderPacket(plan)
	if len(packet.DialogueScenes) != 0 {
		t.Fatalf("turn-level dialogue blueprints must stay out of prose context: %#v", packet.DialogueScenes)
	}
	if len(packet.RelationshipLenses) != 1 || !strings.Contains(packet.RelationshipLenses[0].CurrentBond, "朋友") {
		t.Fatalf("one relationship-level intention should survive: %#v", packet.RelationshipLenses)
	}
}

func TestDraftRenderPacketSamplesCandidateBeatsAcrossChapterArc(t *testing.T) {
	plan := domain.ChapterPlan{CausalSimulation: domain.ChapterCausalSimulation{
		ReaderRetentionPlan: domain.ReaderRetentionPlan{SurfaceBeats: []domain.RetentionSurfaceBeat{
			{MustShow: "opening"},
			{MustShow: "setup"},
			{MustShow: "obstacle"},
			{MustShow: "turn"},
			{MustShow: "payoff"},
		}},
	}}

	packet := newDraftRenderPacket(plan)
	if len(packet.CandidateBeats) != 0 {
		t.Fatalf("candidate menu duplicates mandatory outcomes and should stay out of prose context: %#v", packet.CandidateBeats)
	}
}

func TestDraftRenderPacketKeepsHumorPlanOutOfProseObligations(t *testing.T) {
	plan := domain.ChapterPlan{CausalSimulation: domain.ChapterCausalSimulation{
		EntertainmentPlan: domain.ReaderEntertainmentPlan{
			HumorBeats: []string{"opening joke", "front-loaded echo", "closing callback"},
		},
	}}

	packet := newDraftRenderPacket(plan)
	if len(packet.EntertainmentPlan.HumorBeats) != 0 {
		t.Fatalf("prescribed jokes must remain optional planning evidence: %#v", packet.EntertainmentPlan.HumorBeats)
	}
}

func TestDraftRenderPacketSynthesizesRelationshipLensFromStrongestDialogue(t *testing.T) {
	plan := domain.ChapterPlan{CausalSimulation: domain.ChapterCausalSimulation{
		DialogueBlueprints: []domain.DialogueSceneBlueprint{
			{
				SceneID: "opening", DialogueMode: "public_confrontation", RelationshipFrame: "三人把时间与车辆边界说清",
				DialogueObjective: "立刻出发", ExitBeat: "众人上车",
			},
			{
				SceneID: "supplier", DialogueMode: "logistics_under_stress", RelationshipFrame: "付款方与供货方",
				DialogueObjective: "核对付款流程", ExitBeat: "完成开票",
			},
			{SceneID: "hall", DialogueMode: "public_confrontation", RelationshipFrame: "父子还在互相防备"},
			{
				SceneID: "door", DialogueMode: "private_reconciliation", RelationshipFrame: "旧友一边试探一边护短",
				DialogueObjective: "确认对方是否还愿意信任自己", ExitBeat: "对方没有追问，却替他留了门",
				TurnProgression: []domain.DialogueTurnDesign{{Speaker: "旧友", ActionBeat: "relationship-action-leak"}},
			},
		},
	}}

	packet := newDraftRenderPacket(plan)
	if len(packet.RelationshipLenses) != 1 {
		t.Fatalf("expected one synthesized relationship lens: %#v", packet.RelationshipLenses)
	}
	lens := packet.RelationshipLenses[0]
	if len(lens.Pair) != 0 || lens.CurrentBond != "旧友一边试探一边护短" ||
		lens.EmotionalWant != "确认对方是否还愿意信任自己" || lens.NextEmotionalBeat != "对方没有追问，却替他留了门" {
		t.Fatalf("relationship lens should use only the strongest dialogue's relationship fields: %#v", lens)
	}
	raw, err := json.Marshal(packet)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "turn_progression") || strings.Contains(string(raw), "relationship-action-leak") {
		t.Fatalf("relationship fallback leaked dialogue choreography: %s", raw)
	}
}

func TestRenderRequiredOutcomesCollapsesProcessRecipesAndOutlineDuplicates(t *testing.T) {
	plan := domain.ChapterPlan{
		Goal: "完整兑现本章大纲核心事件：林澈返乡饭桌被亲戚阴阳失业",
		Hook: "手机弹出县城花钱系统绑定提示",
		Contract: domain.ChapterContract{RequiredBeats: []string{
			"手机向林澈显示县城花钱系统绑定成功，额度一百万元，限定用于青山县内新增、真实、合规、可核验的消费；林澈用旧债等两至三个短动作验证边界，旧债测试被明确拒绝，并保留一次真实迟疑。",
			"系统给出河畔夜市不低于三千元真实改善消费的首笔任务；林澈亲自到场复核入口照明、价目辨识和安全用电缺口。",
			"系统依据真实改善与成交完成阶段核验，解锁五万元夜市小额改善额度，并追加二十四小时内取得十家摊主同意、完成至少五十笔真实交易的目标。",
			"必须完整兑现大纲核心事件：林澈返乡饭桌被亲戚阴阳失业",
			"必须兑现大纲钩子；若现有章节契约已将其前移，则作为中段转折而非强行改写章末：手机弹出县城花钱系统绑定提示",
			"系统绑定，额度一百万元，只限青山县真实合规消费。",
			"首笔任务为河畔夜市不低于3000元真实改善消费。",
			"系统解锁5万元夜市小额改善额度，追加24小时十家摊主、至少五十笔交易目标。",
		}},
	}

	got := RenderRequiredOutcomes(plan)
	if len(got) != 3 {
		t.Fatalf("required outcomes should collapse to 3 result beats, got %d: %#v", len(got), got)
	}
	joined := strings.Join(got, "\n")
	for _, unwanted := range []string{"两至三个短动作", "必须完整兑现", "若现有章节契约"} {
		if strings.Contains(joined, unwanted) {
			t.Fatalf("process/meta instruction leaked into prose outcomes: %s", joined)
		}
	}
	for _, want := range []string{"系统绑定", "首笔任务", "系统解锁"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("result beat %q missing: %s", want, joined)
		}
	}
}

func TestRenderRequiredOutcomesKeepsEventWhenTrendLiteralIsEmbedded(t *testing.T) {
	plan := domain.ChapterPlan{
		Contract: domain.ChapterContract{RequiredBeats: []string{
			"饭桌上亲戚挤兑林澈；赵航以‘呱，’起头吐槽，林澈最终离席。",
		}},
		CausalSimulation: domain.ChapterCausalSimulation{
			TrendLanguage: []domain.TrendLanguagePlan{{Item: "呱，……"}},
		},
	}
	got := RenderRequiredOutcomes(plan)
	if len(got) != 1 || got[0] != "饭桌上亲戚挤兑林澈" || strings.Contains(got[0], "呱") {
		t.Fatalf("prose outcome should keep the event but drop prescribed style/choreography: %#v", got)
	}
}

func TestRenderRequiredOutcomesCapsLegacyChecklistPlans(t *testing.T) {
	plan := domain.ChapterPlan{Contract: domain.ChapterContract{RequiredBeats: []string{
		"开场结果", "运输结果", "摊主甲结果", "摊主乙结果", "关系变化", "章末结果",
	}}}
	got := RenderRequiredOutcomes(plan)
	if len(got) != 4 || got[0] != "开场结果" || got[len(got)-1] != "章末结果" {
		t.Fatalf("legacy checklist should become four spread outcomes: %#v", got)
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

func TestPlanningProfileUsesProjectionWithoutFullCharacterDecisions(t *testing.T) {
	result := map[string]any{
		"outline": []string{"full outline"},
		"working_memory": map[string]any{
			"current_chapter_outline": "keep",
			"progression_snapshot":    "drop",
			"character_stage_records": "keep",
		},
		"chapter_world_simulation": map[string]any{
			"status":                 "ready",
			"simulation_id":          "sim-2",
			"character_decisions":    []domain.CharacterWorldDecision{{Character: "沈知遥"}},
			"protagonist_projection": domain.ProtagonistDecisionProjection{Protagonist: "林澈", ChosenDecision: "继续试点"},
		},
	}
	applyChapterContextProfile(result, "planning")
	if _, exists := result["outline"]; exists {
		t.Fatal("planning profile should use current/future outline window, not the full outline")
	}
	world := result["chapter_world_simulation"].(map[string]any)
	if _, exists := world["character_decisions"]; exists {
		t.Fatal("planning profile leaked full off-screen character decisions")
	}
	if _, exists := world["protagonist_projection"]; !exists {
		t.Fatal("planning profile lost protagonist projection")
	}
	working := result["working_memory"].(map[string]any)
	if _, exists := working["progression_snapshot"]; exists {
		t.Fatal("planning profile kept duplicate project progression")
	}
	if _, exists := working["character_stage_records"]; exists {
		t.Fatal("planning profile should trust the finalized protagonist projection instead of repeating all character stages")
	}
}

func TestWorldSimulationProfileKeepsCharacterStateAndDropsWritingMaterial(t *testing.T) {
	result := map[string]any{
		"outline":    "drop",
		"references": "drop",
		"working_memory": map[string]any{
			"current_chapter_outline": "keep",
			"character_stage_records": "keep",
			"chapter_world_deltas":    "drop",
			"side_character_journeys": "drop",
		},
		"episodic_memory": map[string]any{"characters": "keep"},
	}
	applyChapterContextProfile(result, "world_simulation")
	for _, key := range []string{"outline", "references"} {
		if _, exists := result[key]; exists {
			t.Fatalf("world simulation profile kept %s", key)
		}
	}
	working := result["working_memory"].(map[string]any)
	if working["current_chapter_outline"] != "keep" || working["character_stage_records"] != "keep" {
		t.Fatal("world simulation profile lost current chapter or character state")
	}
	if _, exists := working["chapter_world_deltas"]; exists {
		t.Fatal("world simulation profile kept duplicate chapter deltas")
	}
}
