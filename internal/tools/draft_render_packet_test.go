package tools

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
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
			VisualDesign: []domain.CharacterVisualDesign{
				{Character: "林澈", Silhouette: "瘦高，肩背还留着通勤族的紧", FaceAndHair: "短发被雨压乱", ClothingStyle: "旧衬衫卷到小臂", BodyLanguage: "难堪时先笑", SignatureObject: "磨白的电脑包", FirstImpression: "刚失业还硬撑体面", StatusWear: "袖口起皱", SceneUse: "夹鱼刺时露出手腕勒痕"},
				{Character: "沈知遥", Silhouette: "高挑利落", FaceAndHair: "低马尾，眉眼清冷", ClothingStyle: "浅色衬衫配深色长裤", BodyLanguage: "说话前先看现场", SignatureObject: "工作证", FirstImpression: "旧同学比记忆里更有压迫感", StatusWear: "鞋边沾灰", SceneUse: "快步进夜市时工作证轻撞衣襟"},
			},
			CharacterKit: []domain.CharacterKitEntry{
				{Character: "林澈", FirstAppearance: true, AppearanceRef: "visual_design:林澈"},
				{Character: "沈知遥", FirstAppearance: true, AppearanceRef: "visual_design:沈知遥"},
			},
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
		"user_rules": map[string]any{
			"structured": map[string]any{
				"chapter_words": map[string]any{"min": 2100, "max": 3000},
			},
		},
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
		"selected_memory": map[string]any{
			"review_lessons": []string{"渲染时临时追加的审稿经验"},
			"story_threads":  []string{"未写进正式 plan 的故事线"},
		},
		"episodic_memory": map[string]any{
			"resource_audit":       "实时资源审计",
			"foreshadow_ledger":    "实时伏笔表",
			"recent_state_changes": "实时状态变化",
		},
	}

	applyChapterContextProfile(result, "draft")

	packet, ok := working["render_packet"].(draftRenderPacket)
	if !ok {
		t.Fatalf("render_packet type = %T", working["render_packet"])
	}
	if _, mirrored := result["render_packet"]; mirrored {
		t.Fatal("draft profile duplicated render_packet outside canonical working_memory")
	}
	for _, liveKey := range []string{
		"review_lessons", "story_threads", "resource_audit",
		"foreshadow_ledger", "recent_state_changes",
	} {
		if hasContextKey(result, liveKey) {
			t.Fatalf("draft profile retained live overlay %q outside frozen render packet", liveKey)
		}
	}
	if len(packet.MandatoryBeats) != 1 || len(packet.OptionalStyleBeats) != 0 {
		t.Fatalf("mandatory/optional = %#v / %#v", packet.MandatoryBeats, packet.OptionalStyleBeats)
	}
	if packet.Version != 11 || packet.Heading != "第1章 回县城的第一顿饭" || packet.WordBudget == nil || packet.WordBudget.HardMin != 2100 || packet.WordBudget.HardMax != 3000 ||
		packet.WordBudget.TargetMin != 2400 || packet.WordBudget.TargetMax != 2700 || !packet.WordBudget.ExactBoundary {
		t.Fatalf("exact prose word budget missing from render packet: %#v", packet.WordBudget)
	}
	if !strings.Contains(packet.WordBudget.Unit, "including_title") {
		t.Fatalf("word budget counting unit is ambiguous: %#v", packet.WordBudget)
	}
	if len(packet.ForbiddenMoves) != 2 {
		t.Fatalf("hard project prohibitions must survive render projection: %#v", packet.ForbiddenMoves)
	}
	for _, policy := range []string{
		packet.HardContractPolicy,
		packet.SoftMaterialPolicy,
		packet.SelectionPolicy,
		packet.DialogueTopologyPolicy,
		packet.SystemVoicePolicy,
		packet.CharacterEntrancePolicy,
	} {
		if strings.TrimSpace(policy) == "" {
			t.Fatalf("lean render policy missing: %#v", packet)
		}
	}
	for name, policy := range map[string]string{
		"plan_translation": packet.PlanTranslationPolicy,
		"reader_register":  packet.ReaderRegisterPolicy,
		"interface":        packet.InterfaceCompression,
		"scene_purpose":    packet.ScenePurposePolicy,
		"spoken_language":  packet.SpokenLanguagePolicy,
		"emotional":        packet.EmotionalRenderPolicy,
		"proof_focus":      packet.ProofFocusPolicy,
		"named_role":       packet.NamedRolePolicy,
		"relationship":     packet.RelationshipPriority,
	} {
		if strings.TrimSpace(policy) != "" {
			t.Fatalf("v11 should not duplicate prompt-level %s policy in every packet: %q", name, policy)
		}
	}
	if len(packet.VisualCards) != 2 || !packet.VisualCards[0].FirstAppearance || packet.VisualCards[1].FaceAndHair == "" {
		t.Fatalf("first-appearance visual cards must reach prose context: %#v", packet.VisualCards)
	}
	if !strings.Contains(packet.CharacterEntrancePolicy, "视觉锚点") || !strings.Contains(packet.CharacterEntrancePolicy, "禁止证件照式罗列") {
		t.Fatalf("character entrance policy must require selective POV-grounded appearance: %q", packet.CharacterEntrancePolicy)
	}
	if !strings.Contains(packet.SelectionPolicy, "手续") || !strings.Contains(packet.SelectionPolicy, "压缩或离屏") {
		t.Fatalf("selection policy must keep proof-chain compression without a separate dossier: %q", packet.SelectionPolicy)
	}
	serializedPolicies := strings.Join([]string{
		packet.SelectionPolicy,
		packet.DialogueTopologyPolicy,
		packet.SystemVoicePolicy,
		packet.CharacterEntrancePolicy,
	}, "\n")
	for _, projectSpecific := range []string{"青山县", "县城居民", "林澈", "摊主", "男女主"} {
		if strings.Contains(serializedPolicies, projectSpecific) {
			t.Fatalf("render policy leaked project-specific assumption %q: %s", projectSpecific, serializedPolicies)
		}
	}
	if len(packet.EmotionalLenses) != 1 || packet.EmotionalLenses[0].Character != "林澈" ||
		packet.EmotionalLenses[0].EmotionLedAction != "不炫耀额度，离席验证" {
		t.Fatalf("focalizer emotional causality must reach prose without other minds: %#v", packet.EmotionalLenses)
	}
	if len(packet.ContinuityChecks) != 1 || packet.ContinuityChecks[0] != "沈知遥到场时首单已经发生。" {
		t.Fatalf("factual continuity must survive while render choreography stays outside: %#v", packet.ContinuityChecks)
	}
	if !reflect.DeepEqual(packet.EndingContract, plan.CausalSimulation.EndingContract) || packet.EndingAnchorCandidate != "" {
		t.Fatalf("exact ending consequence contract was lost: %#v / %q", packet.EndingContract, packet.EndingAnchorCandidate)
	}
	if len(packet.DialogueScenes) != 1 || packet.DialogueScenes[0].SceneID != "dinner" || packet.DialogueScenes[0].DialogueObjective != "替主角解围" {
		t.Fatalf("safe scene-level dialogue contract must reach prose: %#v", packet.DialogueScenes)
	}
	raw, err := json.Marshal(packet)
	if err != nil {
		t.Fatal(err)
	}
	serialized := string(raw)
	for _, forbidden := range []string{
		"turn_progression", "action_beat", "proof_on_page", "latent_context", "hidden_pressures", "每句先夹菜",
		"scene_objective", "饭桌受压", "当场炫耀", "先点按钮再付款",
	} {
		if strings.Contains(serialized, forbidden) {
			t.Fatalf("draft render packet leaked %q: %s", forbidden, serialized)
		}
	}
	// The prospective anti-AI contract is now part of the render input rather
	// than a post-draft review-only artifact. Keep a tight cap, but reserve the
	// small explicit allowance needed for those qualitative first-draft rules.
	if len(raw) > 7300 {
		t.Fatalf("prose-facing packet regrew into a planning dossier: %d bytes", len(raw))
	}
	if len(packet.CandidateBeats) != 3 || packet.CandidateBeats[0].Event != "饭桌压力" {
		t.Fatalf("selected reader-facing beats did not reach prose as bounded candidates: %#v", packet.CandidateBeats)
	}
	if _, exists := result["causal_simulation"]; exists {
		t.Fatal("draft profile must hide full causal_simulation")
	}
	world := result["chapter_world_simulation"].(map[string]any)
	if _, exists := world["character_decisions"]; exists {
		t.Fatal("draft profile must hide all-character decisions")
	}
	if _, exists := world["protagonist_projection"]; exists {
		t.Fatal("draft profile must not duplicate the protagonist projection after render_packet absorbed it")
	}
	projection := packet.ProtagonistProjection
	if len(projection.ObservableEffects) != 0 {
		t.Fatalf("duplicate observable effects leaked beside mandatory outcomes: %#v", projection)
	}
	if len(projection.CausalChain) != 0 || len(projection.AvailableOptions) != 0 || len(projection.PlanConstraints) != 0 || projection.DecisionReason != "不想继续被比较" {
		t.Fatalf("draft projection still carries planning choreography: %#v", projection)
	}
	leanPlan := result["chapter_plan"].(map[string]any)
	if _, exists := leanPlan["causal_simulation"]; exists {
		t.Fatal("lean chapter plan must not embed causal_simulation")
	}
	if _, exists := leanPlan["contract"]; exists {
		t.Fatal("draft chapter plan must not duplicate the canonical render_packet contract")
	}
	if hasContextKey(result, "chapter_contract") {
		t.Fatal("draft profile must not retain a duplicate chapter_contract beside render_packet")
	}
}

func TestDraftRenderPacketSelectsVoiceCardsByOnPageMotiveAndKeepsHardVoiceBoundaries(t *testing.T) {
	knowledgeBoundary := "林澈只知道贺骁还在听；不知道沈知遥已经看过票据；也不能推断系统会不会追加条件。"
	forbiddenMoves := []string{
		"不得解释系统原理",
		"不得替沈知遥说出未公开动机",
		"不得把试探说成承诺",
		"不得用同一种短句节奏回答所有人",
	}
	revealBudget := []string{
		"本章只确认借车请求已经提出",
		"不确认贺骁最终是否借车",
		"不解释沈知遥看过哪些票据",
		"不揭露系统追加条件",
		"不提前说明下一章复看结果",
	}
	voice := func(character string) domain.CharacterVoiceLogic {
		return domain.CharacterVoiceLogic{
			Character: character, SpeechPrinciple: character + "按自己的利害开口",
			KnowledgeBoundary: character + "只知道现场公开信息",
			ForbiddenMoves:    []string{"不得替作者解释"},
		}
	}
	voices := []domain.CharacterVoiceLogic{
		voice("离屏长辈"),
		voice("系统"),
		{
			Character: "林澈", SpeechPrinciple: "先确认对方听见了什么，再决定说到哪一步",
			HiddenSubtext:     "既怕被拒绝，也不肯用旧交情逼人",
			KnowledgeBoundary: knowledgeBoundary, RelationshipStance: "请求帮助但不给对方预设答案",
			DictionAndRhythm: "说明用途时完整，碰到承诺边界时收短",
			ForbiddenMoves:   forbiddenMoves,
		},
		voice("贺骁"),
		voice("沈知遥"),
		voice("新摊主"),
		voice("新邻居"),
		voice("未登场供应商"),
	}
	visuals := []domain.CharacterVisualDesign{
		{Character: "新摊主", Silhouette: "围裙外还套着旧夹克"},
		{Character: "新邻居", Silhouette: "抱着纸箱站在门边"},
		{Character: "系统", Silhouette: "手机上的提示框"},
	}
	kits := []domain.CharacterKitEntry{
		{Character: "新摊主", FirstAppearance: true},
		{Character: "新邻居", FirstAppearance: true},
		{Character: "系统", FirstAppearance: true},
	}
	plan := domain.ChapterPlan{CausalSimulation: domain.ChapterCausalSimulation{
		InitialState: []domain.CharacterSimulationState{{Character: "林澈"}},
		VoiceLogic:   voices,
		DialogueBlueprints: []domain.DialogueSceneBlueprint{{
			SceneID: "ending-call", DialogueMode: "phone", Participants: []string{"贺骁", "沈知遥"},
			RelationshipFrame: "朋友请求帮助时仍给彼此留边界",
		}},
		VisualDesign: visuals,
		CharacterKit: kits,
		ReaderRetentionPlan: domain.ReaderRetentionPlan{
			RevealBudget: revealBudget,
		},
	}}

	packet := newDraftRenderPacket(plan)
	gotCharacters := make([]string, 0, len(packet.VoiceCards))
	for _, card := range packet.VoiceCards {
		gotCharacters = append(gotCharacters, card.Character)
	}
	wantCharacters := []string{"林澈", "贺骁", "沈知遥", "新摊主", "新邻居"}
	if !reflect.DeepEqual(gotCharacters, wantCharacters) {
		t.Fatalf("voice selection must follow focalizer, retained dialogue participants, then relevant first appearances: got=%v want=%v", gotCharacters, wantCharacters)
	}
	if packet.VoiceCards[0].KnowledgeBoundary != knowledgeBoundary {
		t.Fatalf("multi-clause knowledge boundary was truncated: %q", packet.VoiceCards[0].KnowledgeBoundary)
	}
	if packet.LiteraryRenderContract.KnowledgeBoundary != knowledgeBoundary {
		t.Fatalf("literary focalization contract truncated the same multi-clause knowledge boundary: %q", packet.LiteraryRenderContract.KnowledgeBoundary)
	}
	if !reflect.DeepEqual(packet.VoiceCards[0].ForbiddenMoves, forbiddenMoves) {
		t.Fatalf("three-or-more hard voice prohibitions were sampled: got=%v want=%v", packet.VoiceCards[0].ForbiddenMoves, forbiddenMoves)
	}
	if !reflect.DeepEqual(packet.RevealBudget, revealBudget) {
		t.Fatalf("full reveal budget was truncated: got=%v want=%v", packet.RevealBudget, revealBudget)
	}
	if slices.Contains(gotCharacters, "离屏长辈") || slices.Contains(gotCharacters, "系统") || slices.Contains(gotCharacters, "未登场供应商") {
		t.Fatalf("source order or an irrelevant system consumed a voice slot: %v", gotCharacters)
	}

	systemRelevant := plan
	systemRelevant.CausalSimulation.DialogueBlueprints = []domain.DialogueSceneBlueprint{{
		SceneID: "specific-system-response", DialogueMode: "stimulus_response",
		Participants: []string{"系统"}, DialogueObjective: "只回应林澈刚提交的具体问题",
	}}
	systemRelevant.CausalSimulation.VisualDesign = nil
	systemRelevant.CausalSimulation.CharacterKit = nil
	relevantPacket := newDraftRenderPacket(systemRelevant)
	relevantCharacters := make([]string, 0, len(relevantPacket.VoiceCards))
	for _, card := range relevantPacket.VoiceCards {
		relevantCharacters = append(relevantCharacters, card.Character)
	}
	if !slices.Contains(relevantCharacters, "系统") {
		t.Fatalf("system voice explicitly retained as an on-page participant was lost: %v", relevantCharacters)
	}
}

func TestDraftProfileHidesRewriteBodyButKeepsImmutableSourceContract(t *testing.T) {
	plan := domain.ChapterPlan{
		Chapter: 1,
		Title:   "回来第一天",
		Contract: domain.ChapterContract{
			RequiredBeats: []string{"林澈完成第一笔真实县内支出"},
		},
	}
	chapter := &domain.ChapterRewriteSource{
		BodyPath:      "chapters/01.md",
		BodySHA256:    "body-sha",
		WordCount:     2311,
		BriefPath:     "reviews/01_rewrite_brief.md",
		BriefSHA256:   "brief-sha",
		PreserveFacts: []string{"首笔支出为4280元"},
	}
	rewrite := map[string]any{
		"chapter":             chapter,
		"current_body":        strings.Repeat("旧稿对白。", 1800),
		"brief_markdown":      strings.Repeat("完整评审示例动作。", 600),
		"required_sources":    []string{"rewrite_source:chapters/01.md#sha256=body-sha"},
		"preservation_policy": "保留金额、关键选择和结果。",
	}
	working := map[string]any{
		"chapter_plan":     &plan,
		"chapter_contract": plan.Contract,
		"rewrite_source":   rewrite,
	}
	result := map[string]any{
		"chapter_plan":     &plan,
		"chapter_contract": plan.Contract,
		"rewrite_source":   rewrite,
		"working_memory":   working,
	}

	applyChapterContextProfile(result, "draft")

	for _, container := range []map[string]any{result, working} {
		compact, ok := container["rewrite_source"].(map[string]any)
		if !ok {
			t.Fatalf("compact rewrite source missing: %#v", container["rewrite_source"])
		}
		if compact["chapter"] == nil || compact["required_sources"] == nil || compact["preservation_policy"] == nil {
			t.Fatalf("immutable rewrite source contract lost: %#v", compact)
		}
		if _, exists := compact["current_body"]; exists {
			t.Fatal("old chapter body leaked into draft profile")
		}
		if _, exists := compact["brief_markdown"]; exists {
			t.Fatal("full review markdown leaked into draft profile")
		}
		if !strings.Contains(compact["source_body_policy"].(string), "按 render_packet 重新讲述") {
			t.Fatalf("rewrite source policy missing: %#v", compact)
		}
	}
	if hasContextKey(result, "chapter_contract") {
		t.Fatal("duplicate chapter contract survived draft projection")
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) >= 12*1024 {
		t.Fatalf("draft profile retained rewrite prose blobs: %d bytes", len(raw))
	}
}

func TestDraftRenderPacketProjectsProseSafeAntiAIContract(t *testing.T) {
	objectTiming := "系统绑定只回应一次；旧债操作后必须留出可感知等待，再给拒绝；首次结算只能由主角在巡查结束后主动查看。"
	rhythmTiming := "饭桌长问短答，风险发现处收短，电话等待各自成段。"
	dialogueFunction := "饭桌对白承担权力博弈，不能让人物轮流说明规则。"
	pollutedObjectTiming := "每3句插一次物件回应；" + objectTiming
	pollutedDialogueFunction := "句长CV低于0.62；" + dialogueFunction
	plan := domain.ChapterPlan{CausalSimulation: domain.ChapterCausalSimulation{
		AntiAIPlan: domain.AntiAIExecutionPlan{
			RiskSignals:          []string{"物件回应等距", "对白传送带"},
			CounterMoves:         []string{"三次反馈由人物行动和场景切换错开"},
			SentenceRhythmPolicy: rhythmTiming,
			ObjectResponseBudget: pollutedObjectTiming,
			DialogueFunctionPlan: pollutedDialogueFunction,
			ReviewChecks:         []string{"旧债前是否真的留下等待空白？"},
		},
	}}

	packet := newDraftRenderPacket(plan)
	contract := packet.AntiAIRenderContract
	if contract == nil ||
		!slices.Contains(contract.RiskSignals, "物件回应等距") ||
		!slices.Contains(contract.RiskSignals, "对白传送带") ||
		!slices.Contains(contract.CounterMoves, "三次反馈由人物行动和场景切换错开") ||
		contract.SentenceRhythmPolicy != rhythmTiming ||
		!slices.Contains(contract.ReviewChecks, "旧债前是否真的留下等待空白？") {
		t.Fatalf("chapter-specific anti-AI contract did not reach prose packet: %#v", contract)
	}
	got := packet.EventTimingSafeguards
	if got == nil ||
		!strings.Contains(got.ObjectResponseBudget, "系统绑定只回应一次") ||
		!strings.Contains(got.ObjectResponseBudget, "旧债操作后必须留出可感知等待") ||
		!strings.Contains(got.ObjectResponseBudget, "首次结算只能由主角在巡查结束后主动查看") ||
		!strings.Contains(got.DialogueFunctionPlan, "饭桌对白承担权力博弈") {
		t.Fatalf("story timing safeguards did not reach prose packet: %#v", got)
	}
	if plan.CausalSimulation.AntiAIPlan.ObjectResponseBudget != pollutedObjectTiming ||
		plan.CausalSimulation.AntiAIPlan.SentenceRhythmPolicy != rhythmTiming ||
		plan.CausalSimulation.AntiAIPlan.DialogueFunctionPlan != pollutedDialogueFunction ||
		len(plan.CausalSimulation.AntiAIPlan.ReviewChecks) != 1 {
		t.Fatalf("formal plan lost anti-AI review authority: %#v", plan.CausalSimulation.AntiAIPlan)
	}
	raw, err := json.Marshal(packet)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"anti_ai_execution_plan", "每3句", "CV", "0.62"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("detector/fixed-cadence recipe %q leaked into prose packet: %s", forbidden, raw)
		}
	}
}

func TestDraftRenderPacketProvidesC6ToC12ProseSafeAntiAIBaselineWhenPlanIsEmpty(t *testing.T) {
	for chapter := 6; chapter <= 12; chapter++ {
		t.Run(fmt.Sprintf("chapter_%02d", chapter), func(t *testing.T) {
			packet := newDraftRenderPacket(domain.ChapterPlan{Chapter: chapter})
			contract := packet.AntiAIRenderContract
			if contract == nil ||
				len(contract.RiskSignals) == 0 ||
				len(contract.CounterMoves) == 0 ||
				strings.TrimSpace(contract.SentenceRhythmPolicy) == "" ||
				strings.TrimSpace(contract.ObjectResponseBudget) == "" ||
				strings.TrimSpace(contract.DialogueFunctionPlan) == "" ||
				len(contract.ReviewChecks) == 0 {
				t.Fatalf("C%d empty formal anti-AI plan did not receive the first-draft baseline: %#v", chapter, contract)
			}
			raw, err := json.Marshal(contract)
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{
				"首稿前执行",
				"防台账",
				"对白传送带",
				"刺激先改判断",
				"硬事实并场",
				"配角对白必须以主动误解、反驳或拒绝改变局面",
				"POV 内省落在判断摇摆、欲望冲突或可感代价",
				"不撒通用身体反应",
				"连续“没有 X，只 Y”式戏剧否定",
				"多源证据只保留 3—5 个功能锚点",
				"其余概括并场",
				"不按来源逐项堆成清单",
				"删掉不承载选择、阻力或后果的弱过渡",
				"真正命中的动作直接承接后果",
				"句段随观察、犹疑、冲突和余波换挡",
				"物件或界面只在改变判断、关系或安全后果时回应",
			} {
				if !strings.Contains(string(raw), want) {
					t.Fatalf("C%d default first-draft anti-AI rule %q is missing: %s", chapter, want, raw)
				}
			}
			for _, forbidden := range []string{
				"CV", "TTR", "0.62", "0.72", "12%", "检测概率", "检测阈值", "低于4%", "每3句", "每五句",
			} {
				if strings.Contains(string(raw), forbidden) {
					t.Fatalf("C%d detector/fixed-cadence recipe %q leaked into default contract: %s", chapter, forbidden, raw)
				}
			}
		})
	}
}

func TestDraftRenderPacketKeepsQualitativeAntiAIRulesAndHidesMetricRecipes(t *testing.T) {
	objectTiming := "旧债拒付后系统静默；人物完成选择后才允许一次结算回应。"
	dialogueFunction := "饭桌对白承担权力博弈，不能让人物轮流说明规则。"
	plan := domain.ChapterPlan{CausalSimulation: domain.ChapterCausalSimulation{
		AntiAIPlan: domain.AntiAIExecutionPlan{
			RiskSignals: []string{
				"句长CV和段长CV偏低——节奏曲线过平",
				"对白传送带压掉了人物迟疑",
				"第三条风险不应进入精简包",
			},
			CounterMoves: []string{
				"每三句插入一次动作并把TTR维持在0.72以上",
				"让主角先误判，再由眼前安全后果迫使他改口",
				"让父亲的护场改变主角随后保密的选择",
			},
			SentenceRhythmPolicy: "旧稿句长CV低于0.62；饭桌保留长问短答，风险发现处自然收短；每五句换一次长度。",
			ObjectResponseBudget: objectTiming,
			DialogueFunctionPlan: dialogueFunction,
			ReviewChecks: []string{
				"全文检测概率是否低于12%阈值",
				"风险发现是否真正改变了人物选择？",
				"父亲护场是否留下后续关系余波？",
				"第四条检查不应进入精简包",
			},
		},
	}}

	packet := newDraftRenderPacket(plan)
	contract := packet.AntiAIRenderContract
	if contract == nil {
		t.Fatal("prose-safe anti-AI render contract is missing")
	}
	contractJSON, err := json.Marshal(contract)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"对白传送带压掉了人物迟疑",
		"让主角先误判，再由眼前安全后果迫使他改口",
		"让父亲的护场改变主角随后保密的选择",
		"饭桌保留长问短答",
		"风险发现处自然收短",
		"风险发现是否真正改变了人物选择",
		"父亲护场是否留下后续关系余波",
	} {
		if !strings.Contains(string(contractJSON), want) {
			t.Fatalf("prose-safe anti-AI rule %q was dropped: %s", want, contractJSON)
		}
	}
	got := packet.EventTimingSafeguards
	raw, err := json.Marshal(packet)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{
		"CV", "TTR", "0.62", "0.72", "12%", "阈值", "每三句", "每五句", "检测概率",
		"anti_ai_execution_plan",
	} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("anti-AI recipe %q leaked into prose-facing packet: %s", forbidden, raw)
		}
	}
	if got == nil || got.ObjectResponseBudget != objectTiming || got.DialogueFunctionPlan != dialogueFunction {
		t.Fatalf("concrete story timing was lost with detector recipe: %#v", got)
	}
}

func TestProseTimingSanitizerKeepsDomainDetectionAndBusinessPercentage(t *testing.T) {
	value := "漏电检测通过后才结算；抽成5%确认后才回应；每3句插一次物件；AIGC检测概率低于4%"
	got := sanitizeProseTimingText(value)
	for _, want := range []string{"漏电检测通过后才结算", "抽成5%确认后才回应"} {
		if !strings.Contains(got, want) {
			t.Fatalf("story timing %q was mistaken for detector recipe: %q", want, got)
		}
	}
	for _, forbidden := range []string{"每3句", "AIGC", "低于4%"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("detector/cadence recipe %q survived timing sanitizer: %q", forbidden, got)
		}
	}
}

func TestDraftRenderPacketKeepsEveryRewritePreserveFact(t *testing.T) {
	facts := []string{
		"已提交状态结果：马玉芬.resource = 获得有限试用设施并新增两碗豆腐脑共12元真实营业收入",
		"贺骁皮卡仍为 pending，尚未答复。",
		"4280 元电子票只证明县内真实商户交易。",
	}
	plan := domain.ChapterPlan{CausalSimulation: domain.ChapterCausalSimulation{
		ReviewRefinement: domain.ReviewRefinementLoop{PreserveConstraints: facts},
	}}

	packet := newDraftRenderPacket(plan)
	if !reflect.DeepEqual(packet.PreserveFacts, facts) {
		t.Fatalf("rewrite preserve facts were sampled, dropped, or changed: got=%#v want=%#v", packet.PreserveFacts, facts)
	}
	if !strings.Contains(packet.HardContractPolicy, "锁定结果") || !strings.Contains(packet.HardContractPolicy, "不得为每项单独分配段落") {
		t.Fatalf("preserve-fact render policy is ambiguous: %q", packet.HardContractPolicy)
	}
}

func TestDraftRenderPacketCarriesPOVSubjectiveCausalityWithoutOtherMind(t *testing.T) {
	target := "两条主观因果链：饭桌难堪到守密选择；顾客受益到发现风险并自纠"
	requirement := "每条都必须落成刺激→具体体验或误判→调节→选择变化→关系或现实余波"
	plan := domain.ChapterPlan{CausalSimulation: domain.ChapterCausalSimulation{
		InitialState: []domain.CharacterSimulationState{{Character: "林澈"}, {Character: "沈知遥"}},
		EmotionalLogic: []domain.CharacterEmotionalLogic{
			{
				Character: "林澈", ImmediateState: "饭桌上压住解释冲动", PrimaryEmotion: "难堪后转为警觉",
				EmotionalTrigger: "亲戚追问和孩子跨过护套", GoalAppraisal: "先守密，再自行消除风险",
				RegulationStrategy: "吞回真话并把短暂满足压成复核", EmotionLedAction: "转开话题；叫停退线",
				EvidenceInScene: []string{"话到嘴边停住", "视线跟着孩子扫到走线"},
			},
			{
				Character: "沈知遥", ImmediateState: "hidden-other-state", GoalAppraisal: "hidden-other-decision",
			},
		},
		LiteraryRendering: &domain.LiteraryRenderingPlan{
			Focalizer: "林澈", NarrativeAccess: domain.LiteraryNarrativeAccessInternal,
			ActiveLenses: []domain.LiteraryRenderingLens{
				{Kind: "focalization-boundary", Target: "全章", Move: "只写林澈可感知信息"},
				{Kind: "emotion-appraisal", Target: target, Move: requirement, Why: "主观体验必须真正改变选择", Avoid: "不能用情绪标签或微动作充数"},
			},
		},
	}}

	packet := newDraftRenderPacket(plan)
	if len(packet.EmotionalLenses) != 2 {
		t.Fatalf("POV emotional logic plus subjective causal requirement must both survive: %#v", packet.EmotionalLenses)
	}
	if packet.EmotionalLenses[0].Character != "林澈" || packet.EmotionalLenses[1].SubjectiveCausalTarget != target ||
		packet.EmotionalLenses[1].SubjectiveCausalRequirement != requirement {
		t.Fatalf("two-chain POV contract was weakened: %#v", packet.EmotionalLenses)
	}
	raw, err := json.Marshal(packet)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "hidden-other-state") || strings.Contains(string(raw), "hidden-other-decision") {
		t.Fatalf("non-focalizer private appraisal leaked into prose packet: %s", raw)
	}
}

func TestDraftRenderPacketPreservesExactEndingAndSafeDialogueContract(t *testing.T) {
	ending := domain.EndingConsequenceContract{
		EndingMode:       "截断——对方尚未答复时切章",
		ConcreteAnchor:   "扳手落入铁盘的金属撞击声来自电话另一端",
		Consequence:      "资源已拨通但仍待确认，倒计时开始而运力没有着落",
		NextChapterPull:  "对方是否借车，以及主角如何赶在复看前凑齐运力",
		WhyNotUI:         "结尾悬在人与人的未完成对话，而不是系统弹窗",
		ForbiddenEndings: []string{"不得答应借车", "不得追问主角位置", "不得用系统主动提示收尾"},
	}
	plan := domain.ChapterPlan{CausalSimulation: domain.ChapterCausalSimulation{
		EndingContract: ending,
		DialogueBlueprints: []domain.DialogueSceneBlueprint{{
			SceneID: "ending-call", DialogueMode: "phone", ScenePressure: "复看倒计时已经开始",
			RelationshipFrame: "朋友之间尚未得到答复", Participants: []string{"林澈", "贺骁"},
			LocationAnchor: "河岸路灯下", DialogueObjective: "说明真实用车目的并请求借车",
			ExitBeat: "林澈说到货物用途时切断", DoNotUse: []string{"不得让贺骁答应"},
			EntryLine:       "hidden-prescribed-line",
			TurnProgression: []domain.DialogueTurnDesign{{Speaker: "贺骁", ActionBeat: "hidden-turn-action"}},
		}},
	}}

	packet := newDraftRenderPacket(plan)
	if !reflect.DeepEqual(packet.EndingContract, ending) {
		t.Fatalf("ending contract must survive exactly: got %#v want %#v", packet.EndingContract, ending)
	}
	if len(packet.DialogueScenes) != 1 || packet.DialogueScenes[0].ExitBeat != "林澈说到货物用途时切断" ||
		packet.DialogueScenes[0].DialogueObjective != "说明真实用车目的并请求借车" {
		t.Fatalf("safe dialogue scene contract missing: %#v", packet.DialogueScenes)
	}
	raw, err := json.Marshal(packet)
	if err != nil {
		t.Fatal(err)
	}
	for _, hidden := range []string{"hidden-prescribed-line", "hidden-turn-action", "turn_progression"} {
		if strings.Contains(string(raw), hidden) {
			t.Fatalf("dialogue choreography leaked %q: %s", hidden, raw)
		}
	}
}

func TestDraftRenderPacketKeepsEveryHardMoveAndFactualContinuity(t *testing.T) {
	forbidden := []string{
		"不得越过第一章范围",
		"不得泄露系统",
		"不得把额度写成存款",
		"不得删除返工后果",
		"不得提前兑现借车",
		"不得展开教程式流程",
		"不得让系统弹窗抢人物反应",
	}
	plan := domain.ChapterPlan{
		Chapter:    1,
		Title:      "回来第一天",
		Goal:       "林澈先做一笔可撤回的小额试验。",
		Conflict:   "钱已经花了，安全隐患仍在。",
		EmotionArc: "难堪——克制——短暂踏实——收紧——愿意承担。",
		Contract: domain.ChapterContract{
			ForbiddenMoves: forbidden,
			ContinuityChecks: []string{
				"专项额度不是个人存款，系统秘密无人知晓。",
				"局部返工源正文 chapters/01.md 的 sha256 必须保持为 old-source。",
			},
			PayoffPoints: []string{"首笔经营结果落到普通顾客身上。"},
			SceneAnchors: []string{"孩子的鞋尖与两碗豆腐脑。"},
		},
	}

	packet := newDraftRenderPacket(plan)

	if !reflect.DeepEqual(packet.ForbiddenMoves, forbidden) {
		t.Fatalf("hard forbidden moves were truncated: %#v", packet.ForbiddenMoves)
	}
	if len(packet.ContinuityChecks) != 1 || packet.ContinuityChecks[0] != plan.Contract.ContinuityChecks[0] {
		t.Fatalf("factual continuity was lost or source metadata leaked: %#v", packet.ContinuityChecks)
	}
	if len(packet.PayoffPoints) != 1 || len(packet.SceneAnchors) != 1 {
		t.Fatalf("reader payoff or concrete scene anchor missing: %#v / %#v", packet.PayoffPoints, packet.SceneAnchors)
	}
	if packet.Goal == "" || packet.Conflict == "" || packet.EmotionArc == "" {
		t.Fatalf("human motive/conflict/emotion contract missing: %#v", packet)
	}
}

func TestDraftRenderPacketProjectsExplicitLiteraryContractWithProvenance(t *testing.T) {
	plan := domain.ChapterPlan{
		Chapter: 7,
		Hook:    "门关上后，杯底那圈冷茶还在桌面慢慢洇开。",
		CausalSimulation: domain.ChapterCausalSimulation{
			LiteraryRendering: &domain.LiteraryRenderingPlan{
				Focalizer:             "许闻溪",
				NarrativeAccess:       domain.LiteraryNarrativeAccessInternal,
				KnowledgeBoundary:     "只写许闻溪亲历、回忆和从会议材料中有依据推断的内容；不得读取梁渡未表达的打算。",
				PerceptualBias:        "她把每次停顿都先误读成对方准备否定自己。",
				SummaryOmissionPolicy: "三周例行复核用一段概述跨过，只把意见第一次分裂的会议留在现场。",
				Afterimage:            "门关后桌面缓慢洇开的冷茶圈。",
				SourceRefs:            []string{"literary-rendering#focalization-boundary", "literary-rendering#scene-summary"},
				SceneModes: []domain.LiterarySceneRenderingMode{{
					Target: "梁渡把签字笔推回来的会议", Mode: domain.LiterarySceneModeScene,
					Distance: domain.LiteraryNarrativeDistanceClose, StateChange: "许闻溪决定不再替旧方案担责",
					RenderMove: "先让她误读停顿，再由笔尖方向迫使她修正判断。",
				}},
				ActiveLenses: []domain.LiteraryRenderingLens{{
					Kind: "free-indirect-discourse", Target: "签字被拒后的三句叙述",
					Move: "去掉她想，让旁白短暂使用她给失败起的内部称呼。",
					Why:  "读者需要先陷入她的误判，随后才能感到修正。", Avoid: "不把梁渡的真实动机写成全知结论。",
					SourceRefs: []string{"literary-rendering#free-indirect-discourse"},
				}},
			},
		},
	}

	packet := newDraftRenderPacket(plan)
	contract := packet.LiteraryRenderContract
	if packet.Version != 11 || contract.Source != "explicit_plan" {
		t.Fatalf("explicit literary contract was not versioned/projected: %#v", contract)
	}
	if contract.Focalizer != "许闻溪" || contract.NarrativeAccess != "internal" || len(contract.SceneModes) != 1 || len(contract.ActiveLenses) != 1 {
		t.Fatalf("literary decisions were lost: %#v", contract)
	}
	joinedRefs := strings.Join(append(append([]string{}, contract.SourceRefs...), contract.ActiveLenses[0].SourceRefs...), "\n")
	for _, want := range []string{"literary-rendering#focalization-boundary", "literary-rendering#scene-summary", "literary-rendering#free-indirect-discourse"} {
		if !strings.Contains(joinedRefs, want) {
			t.Fatalf("literary provenance %q missing: %#v", want, contract)
		}
	}
	if strings.Contains(contract.UsagePolicy, "每章至少") || strings.Contains(contract.UsagePolicy, "固定比例") {
		t.Fatalf("literary contract became a quota checklist: %q", contract.UsagePolicy)
	}
	raw, err := json.Marshal(packet)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "本参考把英文叙事学") {
		t.Fatalf("full research asset leaked into prose packet: %s", raw)
	}
}

func TestDraftRenderPacketProjectsLegacyPlanIntoLiteraryContract(t *testing.T) {
	plan := domain.ChapterPlan{
		Hook: "电话第二声接通，答案还没来。",
		CausalSimulation: domain.ChapterCausalSimulation{
			InitialState: []domain.CharacterSimulationState{{Character: "林澈", Misbeliefs: []string{"做成一件事就能把难堪抹掉"}}},
			VoiceLogic: []domain.CharacterVoiceLogic{{
				Character: "林澈", KnowledgeBoundary: "不知道沈知遥为何到场，也不知道贺骁会不会答应。",
				HiddenSubtext: "不愿再让父母替他接住后果", SubtextStrategy: "被追问时只答能承担的那一半。",
			}},
			EmotionalLogic: []domain.CharacterEmotionalLogic{{
				Character: "林澈", GoalAppraisal: "付款成功不等于没有把风险推给摊主。",
				EmotionLedAction: "咽回辩解，停止扩铺并承担返工。", EvidenceInScene: []string{"legacy-evidence-must-not-leak"},
			}},
			OutcomeShift:        []string{"林澈从把付款成功当作验证，转为先承担安全后果。"},
			ReaderRetentionPlan: domain.ReaderRetentionPlan{CutOrCompress: []string{"采购、开票和安装步骤合并带过"}},
			AntiAIPlan: domain.AntiAIExecutionPlan{
				SentenceRhythmPolicy: "盘问处短促，重新评价时允许句子延迟落点，作出选择后收短。",
				RiskSignals:          []string{"legacy-risk-list-must-not-leak"},
			},
		},
	}

	contract := newDraftRenderPacket(plan).LiteraryRenderContract
	if contract.Source != "legacy_projection" || contract.Focalizer != "林澈" || contract.KnowledgeBoundary == "" {
		t.Fatalf("legacy plan did not receive a useful literary contract: %#v", contract)
	}
	serialized, err := json.Marshal(contract)
	if err != nil {
		t.Fatal(err)
	}
	text := string(serialized)
	for _, want := range []string{"literary-rendering#focalization-boundary", "literary-rendering#emotion-appraisal", "literary-rendering#scene-summary", "literary-rendering#dialogue-subtext"} {
		if !strings.Contains(text, want) {
			t.Fatalf("legacy projection missing source %q: %s", want, text)
		}
	}
	for _, leaked := range []string{"legacy-evidence-must-not-leak", "legacy-risk-list-must-not-leak", "literary-rendering#syntax-rhythm"} {
		if strings.Contains(text, leaked) {
			t.Fatalf("legacy projection leaked planning dossier field %q: %s", leaked, text)
		}
	}
}

func TestCompactDraftAIVoiceRulesExcludesPlanningAdvice(t *testing.T) {
	analysis := domain.AIVoiceAnalysis{RedFlags: []domain.AIVoiceRedFlag{
		{Rule: "chapter_function_repetition", Severity: "warning"}, // legacy artifact
		{Rule: "future_scene_shape", Severity: "info"},
		{Rule: "dialogue_conveyor_overuse", Severity: "warning"},
	}}
	got := compactDraftAIVoiceRules(analysis, 4)
	if len(got) != 1 || got[0] != "dialogue_conveyor_overuse" {
		t.Fatalf("rewrite packet should contain only current-chapter actionable rules, got %#v", got)
	}
}

func TestDraftRenderPacketKeepsSafeDialogueScenesAndDropsTurnChoreography(t *testing.T) {
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
	if len(packet.DialogueScenes) != 2 || packet.DialogueScenes[0].SceneID != "dinner" || packet.DialogueScenes[1].SceneID != "payoff" {
		t.Fatalf("opening/payoff scene-level dialogue contracts should survive: %#v", packet.DialogueScenes)
	}
	if len(packet.RelationshipLenses) != 1 || !strings.Contains(packet.RelationshipLenses[0].CurrentBond, "朋友") {
		t.Fatalf("one relationship-level intention should survive: %#v", packet.RelationshipLenses)
	}
	raw, err := json.Marshal(packet)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "turn_progression") || strings.Contains(string(raw), "action_beat") {
		t.Fatalf("dialogue projection leaked turn choreography: %s", raw)
	}
}

func TestDraftRenderPacketKeepsEightMeaningfulFirstEntrances(t *testing.T) {
	visuals := make([]domain.CharacterVisualDesign, 0, 9)
	kits := make([]domain.CharacterKitEntry, 0, 9)
	for i, name := range []string{"主角", "女主", "父亲", "母亲", "亲戚", "摊主", "年轻亲戚", "师傅", "离屏角色"} {
		visuals = append(visuals, domain.CharacterVisualDesign{
			Character: name, Silhouette: "轮廓", FaceAndHair: "脸发", ClothingStyle: "衣着",
			BodyLanguage: "动作", SignatureObject: "标志物", FirstImpression: "第一印象",
			StatusWear: "状态", SceneUse: "场景用法",
		})
		kits = append(kits, domain.CharacterKitEntry{Character: name, FirstAppearance: i < 8})
	}
	packet := newDraftRenderPacket(domain.ChapterPlan{CausalSimulation: domain.ChapterCausalSimulation{
		VisualDesign: visuals,
		CharacterKit: kits,
	}})
	if len(packet.VisualCards) != 8 {
		t.Fatalf("first chapter entrances were silently dropped: %#v", packet.VisualCards)
	}
	for i, card := range packet.VisualCards {
		if !card.FirstAppearance || card.Character != kits[i].Character {
			t.Fatalf("visual card %d lost first-appearance priority: %#v", i, packet.VisualCards)
		}
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
	if len(packet.CandidateBeats) != 3 ||
		packet.CandidateBeats[0].Event != "opening" ||
		packet.CandidateBeats[1].Event != "obstacle" ||
		packet.CandidateBeats[2].Event != "payoff" {
		t.Fatalf("reader-selected page arc was not sampled across opening/turn/payoff: %#v", packet.CandidateBeats)
	}
}

func TestDraftRenderPacketDropsSurfaceBeatExplicitlyProjectedFromRequiredOutcome(t *testing.T) {
	plan := domain.ChapterPlan{
		Contract: domain.ChapterContract{RequiredBeats: []string{"母子完成两碗豆腐脑合计12元真实付款"}},
		CausalSimulation: domain.ChapterCausalSimulation{
			ReaderRetentionPlan: domain.ReaderRetentionPlan{SurfaceBeats: []domain.RetentionSurfaceBeat{{
				PlanSource: "required_beats[1]",
				MustShow:   "母子买两碗豆腐脑并完成12元付款",
			}}},
		},
	}
	if got := newDraftRenderPacket(plan).CandidateBeats; len(got) != 0 {
		t.Fatalf("surface beat explicitly derived from a hard outcome was duplicated: %#v", got)
	}
}

func TestDraftRenderPacketKeepsHumorPlanOutOfProseObligations(t *testing.T) {
	plan := domain.ChapterPlan{CausalSimulation: domain.ChapterCausalSimulation{
		EntertainmentPlan: domain.ReaderEntertainmentPlan{
			OpeningBeat:          "从人物压力和未说完的话开场",
			HumorBeats:           []string{"opening joke", "front-loaded echo", "closing callback"},
			ImmediatePayoffs:     []string{"普通顾客完成真实采用"},
			ProcedureCompression: "手续只保留改变人物判断的结果",
			CompanionVoiceBeat:   "系统只在主角主动查看时短促接话",
			ForbiddenComedy:      []string{"不得拿失业羞辱主角"},
		},
		LongformOpening: domain.LongformOpeningDesign{
			TargetReader:      "县城经营读者",
			OpeningHook:       "饭桌压力撞上受限额度",
			FirstChapterProof: []string{"能力与限制同章可见"},
			RetentionRisks:    []string{"流程过密"},
		},
	}}

	packet := newDraftRenderPacket(plan)
	if len(packet.EntertainmentPlan.HumorBeats) != 0 {
		t.Fatalf("prescribed jokes must remain optional planning evidence: %#v", packet.EntertainmentPlan.HumorBeats)
	}
	if packet.EntertainmentPlan.OpeningBeat == "" ||
		packet.EntertainmentPlan.ProcedureCompression == "" ||
		len(packet.EntertainmentPlan.ForbiddenComedy) == 0 ||
		packet.LongformOpening.OpeningHook == "" {
		t.Fatalf("prose packet lost non-prescriptive attraction guidance: %+v %+v", packet.EntertainmentPlan, packet.LongformOpening)
	}
}

func TestDraftRenderPacketV7SeparatesHardContractFromSoftMaterials(t *testing.T) {
	plan := domain.ChapterPlan{
		Chapter: 1,
		Contract: domain.ChapterContract{
			RequiredBeats: []string{"林澈获得100万元专项额度，且只能用于青山县内新增真实消费"},
			ForbiddenMoves: []string{
				"不得让未授权角色知道系统存在",
				"不得绕过漏电保护与摊主同意",
			},
			ContinuityChecks: []string{
				"赵航不知道系统存在，沈知遥只知道林澈在改善夜市",
				"未经摊主授权不得施工，安全隐患必须留下真实后果",
			},
			PayoffPoints: []string{"5万元额度必须准确", "价牌变清楚", "摊主改口"},
			SceneAnchors: []string{"鱼刺", "酒杯", "价牌", "护套", "未经授权不得拆除安全护套"},
		},
		CausalSimulation: domain.ChapterCausalSimulation{
			LiteraryRendering: &domain.LiteraryRenderingPlan{
				Focalizer: "林澈", NarrativeAccess: domain.LiteraryNarrativeAccessInternal,
				KnowledgeBoundary: "只写林澈可感知的信息", PerceptualBias: "先把关心误读成审查",
				SummaryOmissionPolicy: "手续省略", Afterimage: "护套压住价牌",
				SourceRefs: []string{"literary-rendering#focalization-boundary"},
				SceneModes: []domain.LiterarySceneRenderingMode{
					{Target: "鱼刺", Mode: domain.LiterarySceneModeScene, Distance: domain.LiteraryNarrativeDistanceClose, StateChange: "离席", RenderMove: "拉近"},
					{Target: "酒杯", Mode: domain.LiterarySceneModePause, Distance: domain.LiteraryNarrativeDistanceMedium, StateChange: "改口", RenderMove: "停顿"},
				},
				ActiveLenses: []domain.LiteraryRenderingLens{
					{Kind: "object", Target: "价牌", Move: "变义", Why: "关系变化", Avoid: "不解释", SourceRefs: []string{"literary-rendering#motif"}},
					{Kind: "afterimage", Target: "护套", Move: "回扣", Why: "余波", Avoid: "不点题", SourceRefs: []string{"literary-rendering#afterimage"}},
				},
			},
			ExternalRefs: []domain.ExternalReferencePlan{{
				QueryOrNeed: "rewrite-methodology", SourceType: craftSourceType,
				SourceRefs:    []string{"craft_recall_receipt:r1#chunk=c1"},
				UsableDetails: []string{"先写鱼刺", "再写酒杯"}, TransformationRule: "改成本章物件",
				DoNotUse: []string{"不复制样例"},
			}},
		},
	}

	packet := newDraftRenderPacket(plan)
	if packet.Version != 11 || len(packet.MandatoryBeats) != 1 || !strings.Contains(packet.MandatoryBeats[0], "100万元") {
		t.Fatalf("hard amount outcome was weakened: %+v", packet)
	}
	if len(packet.ForbiddenMoves) != 2 || len(packet.ContinuityChecks) != 4 ||
		!strings.Contains(packet.HardContractPolicy, "授权边界") || !strings.Contains(packet.HardContractPolicy, "安全后果") {
		t.Fatalf("hard knowledge/authorization/safety contract was weakened: %+v", packet)
	}
	joinedContinuity := strings.Join(packet.ContinuityChecks, "\n")
	for _, hardFact := range []string{"5万元额度必须准确", "未经授权不得拆除安全护套"} {
		if !strings.Contains(joinedContinuity, hardFact) {
			t.Fatalf("hard fact from a legacy soft field was not promoted: %q in %v", hardFact, packet.ContinuityChecks)
		}
	}
	if len(packet.SceneAnchors) != 2 || len(packet.LiteraryRenderContract.SceneModes) != 1 || len(packet.LiteraryRenderContract.ActiveLenses) != 1 {
		t.Fatalf("soft shot dossier was not bounded: anchors=%v literary=%+v", packet.SceneAnchors, packet.LiteraryRenderContract)
	}
	if len(packet.CraftMethods) != 1 || len(packet.CraftMethods[0].Moves) != 1 || !strings.Contains(packet.CraftMethods[0].UsagePolicy, "可省略") {
		t.Fatalf("craft receipt still became a move checklist: %+v", packet.CraftMethods)
	}

	raw, err := json.Marshal(packet)
	if err != nil {
		t.Fatal(err)
	}
	var encoded map[string]any
	if err := json.Unmarshal(raw, &encoded); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"soft_scene_anchors", "soft_payoff_directions", "soft_material_policy"} {
		if _, ok := encoded[key]; !ok {
			t.Fatalf("v7 soft role %q missing: %s", key, raw)
		}
	}
	for _, key := range []string{"scene_anchors", "payoff_points"} {
		if _, ok := encoded[key]; ok {
			t.Fatalf("ambiguous v6 role %q survived: %s", key, raw)
		}
	}
	literary := encoded["literary_render_contract"].(map[string]any)
	for _, key := range []string{"soft_scene_choices", "soft_lens_choices", "soft_afterimage_candidate"} {
		if _, ok := literary[key]; !ok {
			t.Fatalf("literary soft role %q missing: %s", key, raw)
		}
	}
	method := encoded["craft_methods"].([]any)[0].(map[string]any)
	if _, ok := method["candidate_moves"]; !ok {
		t.Fatalf("craft candidate role missing: %s", raw)
	}
	if _, ok := method["moves"]; ok {
		t.Fatalf("ambiguous craft moves survived: %s", raw)
	}
}

func TestChapterPlanScopeLabelsPayoffsAsSoftDirections(t *testing.T) {
	plan := domain.ChapterPlan{Chapter: 1, Contract: domain.ChapterContract{
		RequiredBeats:  []string{"准确金额与授权后果成立"},
		ForbiddenMoves: []string{"不得越权"},
		PayoffPoints:   []string{"5万元额度必须准确", "酒杯边缘留下水印", "价牌被风吹歪"},
		SceneAnchors:   []string{"未经摊主同意不得施工"},
	}}
	scope, _ := chapterPlanScopeCheck(plan, "第一章\n\n准确金额与授权后果成立。")
	if _, ok := scope["soft_payoff_directions"]; !ok {
		t.Fatalf("soft payoff directions missing: %+v", scope)
	}
	if _, ok := scope["payoff_points"]; ok {
		t.Fatalf("payoff candidates still look like hard scope: %+v", scope)
	}
	factual := strings.Join(scope["factual_continuity"].([]string), "\n")
	for _, want := range []string{"5万元额度必须准确", "未经摊主同意不得施工"} {
		if !strings.Contains(factual, want) {
			t.Fatalf("hard fact %q was demoted in consistency scope: %+v", want, scope)
		}
	}
	if !strings.Contains(scope["render_policy"].(string), "required_outcomes") || !strings.Contains(scope["render_policy"].(string), "soft_*") {
		t.Fatalf("hard/soft scope policy is ambiguous: %+v", scope)
	}
}

func TestHardRenderMaterialDoesNotPromoteBroadSubstringFalsePositives(t *testing.T) {
	for _, soft := range []string{
		"她恢复了一点元气，再决定要不要开口",
		"他只是想知道对方为什么忽然沉默",
		"工人把安全帽搁在门边",
		"回到熟人身边才有安全感",
		"老人点头同意孩子把灯移近一点",
		"这场戏的责任是让两人重新说上话",
	} {
		if hardRenderMaterial(soft) {
			t.Fatalf("soft candidate was promoted by a broad substring: %q", soft)
		}
	}
	for _, hard := range []string{
		"首笔支出必须保持为4280元",
		"当前额度是100万元，不得写成存款",
		"当前正文不得泄露系统秘密",
		"未经摊主同意不得拆除护套",
		"线路存在漏电隐患，必须先断电",
		"事故发生后的安全责任由施工方承担",
		"退款比例固定为12.5%",
	} {
		if !hardRenderMaterial(hard) {
			t.Fatalf("hard factual material was not promoted: %q", hard)
		}
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

func TestRenderRequiredOutcomesDeduplicatesWithoutShorteningHardDetails(t *testing.T) {
	detailed := []string{
		"手机向林澈显示县城花钱系统绑定成功，额度一百万元，限定用于青山县内新增、真实、合规、可核验的消费；林澈用旧债等两至三个短动作验证边界，旧债测试被明确拒绝，并保留一次真实迟疑。",
		"系统给出河畔夜市不低于三千元真实改善消费的首笔任务；林澈亲自到场复核入口照明、价目辨识和安全用电缺口。",
		"系统依据真实改善与成交完成阶段核验，解锁五万元夜市小额改善额度，并追加二十四小时内取得十家摊主同意、完成至少五十笔真实交易的目标。",
	}
	plan := domain.ChapterPlan{
		Goal: "完整兑现本章大纲核心事件：林澈返乡饭桌被亲戚阴阳失业",
		Hook: "手机弹出县城花钱系统绑定提示",
		Contract: domain.ChapterContract{RequiredBeats: []string{
			detailed[0],
			detailed[1],
			detailed[2],
			"必须完整兑现大纲核心事件：林澈返乡饭桌被亲戚阴阳失业",
			"必须兑现大纲钩子；若现有章节契约已将其前移，则作为中段转折而非强行改写章末：手机弹出县城花钱系统绑定提示",
			"系统绑定，额度一百万元，只限青山县真实合规消费。",
			"首笔任务为河畔夜市不低于3000元真实改善消费。",
			"系统解锁5万元夜市小额改善额度，追加24小时十家摊主、至少五十笔交易目标。",
		}},
	}

	got := RenderRequiredOutcomes(plan)
	if !reflect.DeepEqual(got, detailed) {
		t.Fatalf("deduplication shortened or reordered hard outcomes: got %#v want %#v", got, detailed)
	}
	joined := strings.Join(got, "\n")
	for _, unwanted := range []string{"必须完整兑现", "若现有章节契约"} {
		if strings.Contains(joined, unwanted) {
			t.Fatalf("duplicate outline wrapper leaked into prose outcomes: %s", joined)
		}
	}
	for _, want := range []string{"两至三个短动作", "旧债测试被明确拒绝", "不低于三千元", "二十四小时", "十家摊主", "至少五十笔"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("hard detail %q missing: %s", want, joined)
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
	if len(got) != 1 || got[0] != "饭桌上亲戚挤兑林澈；林澈最终离席" || strings.Contains(got[0], "呱") {
		t.Fatalf("prose outcome should keep hard events around the optional trend literal: %#v", got)
	}
}

func TestRenderRequiredOutcomesKeepsHardTerminologyBoundary(t *testing.T) {
	hard := "额度必须说成专项额度而不是个人存款"
	got := RenderRequiredOutcomes(domain.ChapterPlan{
		Contract: domain.ChapterContract{RequiredBeats: []string{hard}},
	})
	if !reflect.DeepEqual(got, []string{hard}) {
		t.Fatalf("hard terminology boundary was mistaken for optional style: %#v", got)
	}
}

func TestRenderRequiredOutcomesKeepsEveryDistinctLegacyHardBeat(t *testing.T) {
	want := []string{
		"开场结果", "运输结果", "摊主甲结果", "系统奖励到账并推动关系变化", "逐笔票据核查", "章末结果",
	}
	plan := domain.ChapterPlan{Contract: domain.ChapterContract{RequiredBeats: want}}
	got := RenderRequiredOutcomes(plan)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("legacy hard beats were sampled or reordered: got %#v want %#v", got, want)
	}
}

func TestRenderRequiredOutcomesDoesNotMergeConflictingCountOrOrder(t *testing.T) {
	want := []string{
		"必须完成五个摊位的安全改造",
		"必须完成六个摊位的安全改造",
		"老丁必须在沈知遥到场前完成退线",
		"老丁必须在沈知遥到场后完成退线",
		"不得提前施工",
		"允许提前施工",
	}
	got := RenderRequiredOutcomes(domain.ChapterPlan{
		Contract: domain.ChapterContract{RequiredBeats: want},
	})
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("conflicting quantity/order facts were fuzzy-deduplicated: got=%#v want=%#v", got, want)
	}
}

func TestRenderRequiredOutcomesUnwrapsOnlyExactGoalAndHookDuplicates(t *testing.T) {
	goal := "完整兑现本章大纲核心事件：林澈回到青山县"
	hook := "电话接通，答案还没来"
	plan := domain.ChapterPlan{
		Goal: goal,
		Hook: hook,
		Contract: domain.ChapterContract{RequiredBeats: []string{
			"必须完整兑现大纲核心事件：林澈回到青山县",
			"必须兑现大纲钩子；若现有章节契约已将其前移，则作为中段转折而非强行改写章末：电话接通，答案还没来",
			"必须完整兑现大纲核心事件：林澈回到青山县，并保留4280元电子票",
		}},
	}
	got := RenderRequiredOutcomes(plan)
	if !reflect.DeepEqual(got, []string{"林澈回到青山县，并保留4280元电子票"}) {
		t.Fatalf("wrapped hard suffix was deleted with the exact outline duplicate: %#v", got)
	}
}

func TestRenderContinuityChecksKeepsQuoteConfirmedOrder(t *testing.T) {
	hard := "付款必须位于报价确认后，不能先付后补"
	got := RenderContinuityChecks(domain.ChapterPlan{
		Contract: domain.ChapterContract{ContinuityChecks: []string{hard}},
	})
	if !reflect.DeepEqual(got, []string{hard}) {
		t.Fatalf("hard quote/payment order was mistaken for presentation advice: %#v", got)
	}
}

func TestDraftRenderPacketPreservesFiveHardOutcomesAndCompoundNumbersByteForByte(t *testing.T) {
	hard := []string{
		"先由何骁听完真实用途和条件，再决定是否有条件借车；不得提前答应。",
		"68000元取货款必须继续阻断；只准落地五摊；灯具材料680元、五金360元、老丁人工300元分别准确；往返43公里、油费86元、半日人工180元全部留痕；冷饮支架只允许唯一一次失败复测，不得增加第六套。",
		"沈知遥只核对商户、票据、金额和路径，不得推断系统。",
		"五个摊位利益必须有差异，且真实顾客付款后才算改善成立。",
		"林澈拒绝第六张桌，把可见边界留给下一章。",
	}
	softTrend := "赵航必须原样使用‘呱，……’起头吐槽"
	plan := domain.ChapterPlan{
		Chapter:  2,
		Contract: domain.ChapterContract{RequiredBeats: append(append([]string(nil), hard...), softTrend)},
		CausalSimulation: domain.ChapterCausalSimulation{
			TrendLanguage: []domain.TrendLanguagePlan{{Item: "呱，……", CharacterCarrier: "赵航"}},
		},
	}

	packet := newDraftRenderPacket(plan)
	if !reflect.DeepEqual(packet.MandatoryBeats, hard) {
		t.Fatalf("hard outcomes were shortened, sampled or reordered: got %#v want %#v", packet.MandatoryBeats, hard)
	}
	raw, err := json.Marshal(packet)
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		MandatoryBeats []string `json:"mandatory_beats"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded.MandatoryBeats, hard) {
		t.Fatalf("serialized hard outcomes lost exact bytes or order: got %#v want %#v", decoded.MandatoryBeats, hard)
	}
	serialized := string(raw)
	for _, exact := range []string{"68000元", "五摊", "680元", "360元", "300元", "43公里", "86元", "180元", "唯一一次失败复测"} {
		if !strings.Contains(serialized, exact) {
			t.Fatalf("serialized compound hard outcome lost %q: %s", exact, raw)
		}
	}
	if strings.Contains(serialized, softTrend) || strings.Contains(strings.Join(packet.MandatoryBeats, "\n"), "呱") {
		t.Fatalf("optional trend candidate was promoted into hard outcomes: %s", raw)
	}
	for _, planningDossier := range []string{"chapter_contract", "causal_simulation"} {
		if strings.Contains(serialized, planningDossier) {
			t.Fatalf("hard-outcome fix regrew the full planning dossier %q: %s", planningDossier, raw)
		}
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
		"next_chapter_outline": domain.OutlineEntry{
			Chapter: 3, Title: "下一章", CoreEvent: "保留核心", Hook: "保留钩子", Scenes: []string{"保留完整下一章"},
			ContractRefs: []domain.StoryContractRef{{ID: "root-mirror-receipt", PlannedResolution: "删除结构收据"}},
		},
		"future_outline_window": []domain.OutlineEntry{
			{Chapter: 2, Title: "当前重复", Scenes: []string{"删除"}},
			{Chapter: 3, Title: "下一章重复", Scenes: []string{"删除"}},
			{Chapter: 4, Title: "远期章", CoreEvent: "只保留核心", Scenes: []string{"删除详细场景"}},
		},
		"working_memory": map[string]any{
			"current_chapter_outline": domain.OutlineEntry{Chapter: 2, Title: "当前章"},
			"next_chapter_outline": domain.OutlineEntry{
				Chapter: 3, Title: "下一章", CoreEvent: "保留核心", Hook: "保留钩子", Scenes: []string{"保留完整下一章"},
				ContractRefs: []domain.StoryContractRef{{ID: "working-receipt", PlannedResolution: "删除结构收据"}},
			},
			"future_outline_window": []domain.OutlineEntry{
				{Chapter: 2, Title: "当前重复", Scenes: []string{"删除"}},
				{Chapter: 3, Title: "下一章重复", Scenes: []string{"删除"}},
				{Chapter: 4, Title: "远期章", CoreEvent: "只保留核心", Scenes: []string{"删除详细场景"}},
			},
			"horizon_events":          []string{"已由正式 simulation 消费"},
			"horizon_events_usage":    "删除重复说明",
			"progression_snapshot":    "drop",
			"character_stage_records": "keep",
		},
		"project_all_state": domain.ProjectedPlanningContextV2{
			Version:         domain.ProjectedPlanningContextV2Version,
			CumulativeState: []domain.ProjectedPlanningStateFactV2{{Subject: "重复状态"}},
			RecentTransitions: []domain.ProjectedPlanningTransitionV2{
				{Chapter: 1}, {Chapter: 2}, {Chapter: 3},
			},
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
	if _, exists := working["horizon_events"]; exists {
		t.Fatal("ready planning profile replayed horizon events already consumed by simulation")
	}
	future, ok := working["future_outline_window"].([]domain.OutlineEntry)
	if !ok || len(future) != 1 || future[0].Chapter != 4 || len(future[0].Scenes) != 0 {
		t.Fatalf("planning future outline was not de-duplicated to lean horizon: %#v", working["future_outline_window"])
	}
	if next := working["next_chapter_outline"].(domain.OutlineEntry); len(next.Scenes) != 1 ||
		next.CoreEvent != "保留核心" || next.Hook != "保留钩子" || len(next.ContractRefs) != 0 {
		t.Fatalf("planning profile damaged the adjacent narrative boundary or retained receipts: %#v", next)
	}
	rootNext := result["next_chapter_outline"].(domain.OutlineEntry)
	if len(rootNext.Scenes) != 1 || rootNext.CoreEvent != "保留核心" || rootNext.Hook != "保留钩子" || len(rootNext.ContractRefs) != 0 {
		t.Fatalf("planning root next-outline mirror was not compacted consistently: %#v", rootNext)
	}
	rootFuture, ok := result["future_outline_window"].([]domain.OutlineEntry)
	if !ok || !reflect.DeepEqual(rootFuture, future) {
		t.Fatalf("planning root future-outline mirror diverged from working memory: root=%#v working=%#v", rootFuture, future)
	}
	projected := result["project_all_state"].(map[string]any)
	if _, exists := projected["cumulative_state"]; exists {
		t.Fatalf("project-all state retained cumulative state after simulation consumption: %+v", projected)
	}
	recent := projected["recent_transitions"].([]any)
	if len(recent) != 2 || recent[0].(map[string]any)["chapter"] != 2 {
		t.Fatalf("project-all state did not retain the last two transition receipts: %+v", projected)
	}
	for _, raw := range recent {
		if _, leaked := raw.(map[string]any)["delta"]; leaked {
			t.Fatalf("ready planning replayed a full historical transition delta: %+v", raw)
		}
	}
}

func TestCompactReadyPlanningProjectAllStateKeepsLastTwoReceiptOnlyTransitions(t *testing.T) {
	largeDeltaValue := strings.Repeat("historical-projected-delta-anchor|", 220)
	transitions := make([]domain.ProjectedPlanningTransitionV2, 0, 4)
	for i, digest := range []string{"bundle-1", "bundle-2", "bundle-3", "bundle-4"} {
		transitions = append(transitions, domain.ProjectedPlanningTransitionV2{
			Chapter:                i + 1,
			BundleDigest:           digest,
			ProjectedPostStateRoot: "root-" + digest,
			Delta: domain.ProjectedDelta{
				Version: domain.ProjectedDeltaV2Version,
				CharacterState: []domain.StateMutationV2{{
					StableID: "state", Subject: "角色", Field: "current_action", Operation: "set",
					After: largeDeltaValue, Cause: "历史章节已消费", Evidence: largeDeltaValue,
				}},
			},
		})
	}
	predecessor := &domain.ProjectedPlanningPredecessorContractV2{
		Chapter: 4, OutgoingConsequenceID: "out-4", OutgoingConsequenceText: "保留相邻章后果",
		BundleDigest: "bundle-4", ProjectedPostStateRoot: "root-bundle-4",
	}
	state := domain.ProjectedPlanningContextV2{
		Version:             domain.ProjectedPlanningContextV2Version,
		GenerationID:        "generation-1",
		NextChapter:         5,
		ThroughChapter:      4,
		StateRoot:           "state-root-4",
		ContextDigest:       "context-digest-4",
		PredecessorContract: predecessor,
		CumulativeState: []domain.ProjectedPlanningStateFactV2{{
			Category: "character", StableID: "state", Subject: "角色", Field: "current_action",
			Value: "当前动作", ThroughChapter: 4,
		}},
		RecentTransitions: transitions,
		OpenObligations: []domain.ProjectedPlanningObligationV2{{
			ID: "open-1", Kind: domain.ObligationRevealV2, Contract: "保留开放义务",
			Hardness: domain.ObligationHardV2, ConsumerChapters: []int{5}, DueNow: true,
		}},
	}

	encodedState, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	var mappedState map[string]any
	if err := json.Unmarshal(encodedState, &mappedState); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		value any
	}{
		{name: "typed", value: state},
		{name: "map", value: mappedState},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before, err := json.Marshal(tc.value)
			if err != nil {
				t.Fatal(err)
			}
			result := map[string]any{"project_all_state": tc.value}
			compactReadyPlanningProjectAllState(result)
			after, err := json.Marshal(result["project_all_state"])
			if err != nil {
				t.Fatal(err)
			}
			var compact map[string]any
			if err := json.Unmarshal(after, &compact); err != nil {
				t.Fatal(err)
			}
			if _, exists := compact["cumulative_state"]; exists {
				t.Fatalf("ready-planning transport retained cumulative state: %s", after)
			}
			for key, want := range map[string]any{
				"version":         domain.ProjectedPlanningContextV2Version,
				"generation_id":   "generation-1",
				"next_chapter":    float64(5),
				"through_chapter": float64(4),
				"state_root":      "state-root-4",
				"context_digest":  "context-digest-4",
			} {
				if got := compact[key]; !reflect.DeepEqual(got, want) {
					t.Fatalf("ready-planning transport changed %s: got=%#v want=%#v", key, got, want)
				}
			}
			if predecessor := compact["predecessor_contract"].(map[string]any); predecessor["outgoing_consequence_id"] != "out-4" || predecessor["outgoing_consequence_text"] != "保留相邻章后果" {
				t.Fatalf("predecessor contract changed: %#v", predecessor)
			}
			if obligations := compact["open_obligations"].([]any); len(obligations) != 1 || obligations[0].(map[string]any)["id"] != "open-1" {
				t.Fatalf("open obligations changed: %#v", obligations)
			}
			recent := compact["recent_transitions"].([]any)
			if len(recent) != 2 {
				t.Fatalf("recent transition receipt window = %d, want 2", len(recent))
			}
			for i, raw := range recent {
				receipt := raw.(map[string]any)
				wantChapter := float64(i + 3)
				wantDigest := []string{"bundle-3", "bundle-4"}[i]
				if len(receipt) != 3 || receipt["chapter"] != wantChapter || receipt["bundle_digest"] != wantDigest || receipt["projected_post_state_root"] != "root-"+wantDigest {
					t.Fatalf("transition was not reduced to its exact receipt: %#v", receipt)
				}
				if _, leaked := receipt["delta"]; leaked {
					t.Fatalf("historical delta leaked into ready planning: %#v", receipt)
				}
			}
			saved := len(before) - len(after)
			t.Logf("%s ready-planning project_all_state saves %d transport bytes", tc.name, saved)
			if saved <= 2_480 {
				t.Fatalf("ready-planning compaction saves %d bytes, must exceed the 2,480-byte C12 overflow", saved)
			}
		})
	}
}

func TestReadyPlanningProfileCompactsTerminalOutlineMirrorsBeforeBudget(t *testing.T) {
	const currentBoundary = "00:35—00:37内侧开门；00:37—00:38唯一外侧纠正；00:38—00:40警方控制；程野留在外围；00:40后只转入医疗。"
	refs := make([]domain.StoryContractRef, 0, 22)
	for i := 0; i < 22; i++ {
		refs = append(refs, domain.StoryContractRef{
			ID:                   "terminal-receipt",
			Kind:                 "non_negotiable",
			SourceDigest:         "sha256:terminal",
			PlannedPayoffChapter: 12,
			PlannedResolution:    strings.Repeat("完整收据持久保存在outline.json，当前只传叙事边界。", 12),
		})
	}
	next := domain.OutlineEntry{
		Chapter:      12,
		Title:        "三个月后，镜头没有开",
		CoreEvent:    "00:40后先就医、作证与证据交接；三个月后才回答关系。",
		Hook:         "不追加现场行动。",
		Scenes:       []string{"00:40后只进行医疗接手。", "随后处理证据，三个月后再处理关系。"},
		ContractRefs: refs,
	}
	future := []domain.OutlineEntry{{Chapter: 11, CoreEvent: currentBoundary}, next}
	result := map[string]any{
		"current_chapter_outline": domain.OutlineEntry{Chapter: 11, CoreEvent: currentBoundary},
		"next_chapter_outline":    next,
		"future_outline_window":   future,
		"working_memory": map[string]any{
			"current_chapter_outline": domain.OutlineEntry{Chapter: 11, CoreEvent: currentBoundary},
			"next_chapter_outline":    next,
			"future_outline_window":   future,
			"critical_continuity":     strings.Repeat("w", 20_000),
		},
		"project_all_state": map[string]any{
			"open_obligations": strings.Repeat("o", 13_000),
			"cumulative_state": strings.Repeat("duplicate", 1_000),
		},
		"reference_pack": map[string]any{
			"literary_rendering_cards": strings.Repeat("r", 8_200),
		},
		"chapter_world_simulation": map[string]any{
			"status":                 "ready",
			"simulation_id":          "sim-11",
			"protagonist_projection": domain.ProtagonistDecisionProjection{Protagonist: "程野", ChosenDecision: "留在外围完成唯一纠正"},
		},
	}
	before, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) <= contextBudget(11, "planning") {
		t.Fatalf("fixture must reproduce the ready-planning overflow: %d", len(before))
	}

	raw, err := finalizeContextResult(result, 11, "planning")
	if err != nil {
		t.Fatalf("ready planning context should converge after mirror compaction: %v", err)
	}
	if len(raw) > contextBudget(11, "planning") {
		t.Fatalf("ready planning result exceeded 64 KiB: %d", len(raw))
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	if _, exists := payload["future_outline_window"]; exists {
		t.Fatal("terminal future-outline root mirror survived planning compaction")
	}
	working := payload["working_memory"].(map[string]any)
	if _, exists := working["future_outline_window"]; exists {
		t.Fatal("terminal future-outline working copy survived planning compaction")
	}
	current := working["current_chapter_outline"].(map[string]any)
	if current["core_event"] != currentBoundary {
		t.Fatalf("current climax boundary was damaged: %#v", current)
	}
	adjacent := working["next_chapter_outline"].(map[string]any)
	if _, exists := adjacent["contract_refs"]; exists {
		t.Fatalf("adjacent receipts survived ready planning transport: %#v", adjacent)
	}
	for _, want := range []string{"00:40后先就医", "00:40后只进行医疗接手", "三个月后"} {
		encoded, _ := json.Marshal(adjacent)
		if !strings.Contains(string(encoded), want) {
			t.Fatalf("adjacent narrative boundary lost %q: %s", want, encoded)
		}
	}
}

func TestWorldSimulationProfileKeepsCharacterStateAndDropsWritingMaterial(t *testing.T) {
	result := map[string]any{
		"outline":                  "drop",
		"references":               "drop",
		"literary_rendering_cards": "drop",
		"reference_pack": map[string]any{
			"literary_rendering_cards": "drop",
			"other":                    "keep",
		},
		"working_memory": map[string]any{
			"current_chapter_outline": "keep",
			"next_chapter_outline": domain.OutlineEntry{
				Chapter:   12,
				Title:     "终章",
				CoreEvent: "00:40后只转入医疗与证据交接。",
				Hook:      "三个月后再回答关系。",
				Scenes:    []string{"医疗接手", "证据交接"},
				ContractRefs: []domain.StoryContractRef{{
					ID:                   "ending-receipt",
					Kind:                 "ending",
					SourceDigest:         "sha256:ending",
					PlannedPayoffChapter: 12,
					PlannedResolution:    strings.Repeat("结构收据不属于当前世界事实。", 200),
				}},
			},
			"character_stage_records": "keep",
			"chapter_world_deltas":    "drop",
			"side_character_journeys": "drop",
		},
		"episodic_memory": map[string]any{"characters": "keep"},
		"project_all_state": domain.ProjectedPlanningContextV2{
			Version:         domain.ProjectedPlanningContextV2Version,
			ContextDigest:   "keep-digest",
			CumulativeState: []domain.ProjectedPlanningStateFactV2{{Subject: "角色当前状态的重复累计快照"}},
			RecentTransitions: []domain.ProjectedPlanningTransitionV2{
				{Chapter: 1}, {Chapter: 2}, {Chapter: 3},
			},
		},
	}
	applyChapterContextProfile(result, "world_simulation")
	for _, key := range []string{"outline", "references", "literary_rendering_cards"} {
		if _, exists := result[key]; exists {
			t.Fatalf("world simulation profile kept %s", key)
		}
	}
	pack := result["reference_pack"].(map[string]any)
	if _, exists := pack["literary_rendering_cards"]; exists {
		t.Fatal("world simulation profile kept compact prose craft catalog")
	}
	if pack["other"] != "keep" {
		t.Fatal("world simulation profile removed unrelated reference_pack state")
	}
	working := result["working_memory"].(map[string]any)
	if working["current_chapter_outline"] != "keep" || working["character_stage_records"] != "keep" {
		t.Fatal("world simulation profile lost current chapter or character state")
	}
	next := working["next_chapter_outline"].(domain.OutlineEntry)
	if len(next.ContractRefs) != 0 {
		t.Fatalf("world simulation profile replayed adjacent structural receipts: %#v", next.ContractRefs)
	}
	if next.Title != "终章" || next.CoreEvent != "00:40后只转入医疗与证据交接。" ||
		next.Hook != "三个月后再回答关系。" || !reflect.DeepEqual(next.Scenes, []string{"医疗接手", "证据交接"}) {
		t.Fatalf("world simulation profile damaged adjacent narrative boundary: %#v", next)
	}
	if _, exists := working["chapter_world_deltas"]; exists {
		t.Fatal("world simulation profile kept duplicate chapter deltas")
	}
	projected := result["project_all_state"].(map[string]any)
	if cumulative := projected["cumulative_state"].([]domain.ProjectedPlanningStateFactV2); len(cumulative) != 1 {
		t.Fatalf("world simulation lost the folded direct pre-state: %+v", projected)
	}
	recent := projected["recent_transitions"].([]any)
	if len(recent) != 3 || recent[2].(map[string]any)["chapter"] != 3 || projected["context_digest"] != "keep-digest" {
		t.Fatalf("world simulation project-all transition receipts are incomplete: %+v", projected)
	}
	for _, raw := range recent {
		if _, leaked := raw.(map[string]any)["delta"]; leaked {
			t.Fatalf("historical project-all delta survived receipt compaction: %+v", raw)
		}
	}
}

func TestWorldSimulationProfileDropsAdjacentReceiptsBeforeHardBudget(t *testing.T) {
	const currentBoundary = "00:35—00:37内侧开门；00:37—00:38唯一外侧纠正；00:38—00:40警方控制；程野留在外围；00:40后只转入医疗。"
	refs := make([]domain.StoryContractRef, 0, 22)
	for i := 0; i < 22; i++ {
		refs = append(refs, domain.StoryContractRef{
			ID:                   "terminal-receipt",
			Kind:                 "non_negotiable",
			SourceDigest:         "sha256:terminal",
			PlannedPayoffChapter: 12,
			PlannedResolution:    strings.Repeat("完整收据持久保存在outline.json，当前只传叙事边界。", 12),
		})
	}
	next := &domain.OutlineEntry{
		Chapter:      12,
		Title:        "三个月后，镜头没有开",
		CoreEvent:    "00:40后先就医、作证与证据交接；三个月后才回答关系。",
		Hook:         "不追加现场行动。",
		Scenes:       []string{"00:40后只进行医疗接手。", "随后处理证据，三个月后再处理关系。"},
		ContractRefs: refs,
	}
	result := map[string]any{
		"working_memory": map[string]any{
			"current_chapter_outline": domain.OutlineEntry{Chapter: 11, CoreEvent: currentBoundary},
			"next_chapter_outline":    next,
			"horizon_events":          strings.Repeat("h", 15_500),
			"critical_continuity":     strings.Repeat("w", 42_000),
		},
		"simulation_character_authority": map[string]any{
			"format":  "layered_v1",
			"entries": []any{map[string]any{"exact_current_authority": strings.Repeat("a", 30_000)}},
		},
		"chapter_world_simulation": map[string]any{"status": "missing"},
	}
	before, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) <= contextBudget(11, "world_simulation") {
		t.Fatalf("fixture must reproduce the pre-compaction hard overflow: %d", len(before))
	}

	raw, err := finalizeContextResult(result, 11, "world_simulation")
	if err != nil {
		t.Fatalf("adjacent structural receipts should compact before the hard ceiling: %v", err)
	}
	if len(raw) > contextBudget(11, "world_simulation") {
		t.Fatalf("world simulation result exceeded 96 KiB: %d", len(raw))
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	working := payload["working_memory"].(map[string]any)
	current := working["current_chapter_outline"].(map[string]any)
	if current["core_event"] != currentBoundary {
		t.Fatalf("current climax boundary was damaged: %#v", current)
	}
	adjacent := working["next_chapter_outline"].(map[string]any)
	if _, exists := adjacent["contract_refs"]; exists {
		t.Fatalf("adjacent structural receipts survived focused simulation transport: %#v", adjacent)
	}
	for _, want := range []string{"00:40后先就医", "00:40后只进行医疗接手", "三个月后"} {
		encoded, _ := json.Marshal(adjacent)
		if !strings.Contains(string(encoded), want) {
			t.Fatalf("adjacent narrative boundary lost %q: %s", want, encoded)
		}
	}
}
