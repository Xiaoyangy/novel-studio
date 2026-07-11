package tools

import (
	"encoding/json"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

// draftRenderPacket is the deliberately small prose-facing view of a chapter
// plan. Planning data remains authoritative on disk, but the Drafter should not
// see turn-by-turn action choreography or hidden world state and mistake it for
// a checklist to transcribe.
type draftRenderPacket struct {
	Version                int                              `json:"version"`
	Chapter                int                              `json:"chapter"`
	Title                  string                           `json:"title,omitempty"`
	Goal                   string                           `json:"goal,omitempty"`
	Conflict               string                           `json:"conflict,omitempty"`
	Hook                   string                           `json:"hook,omitempty"`
	EmotionArc             string                           `json:"emotion_arc,omitempty"`
	MandatoryBeats         []string                         `json:"mandatory_beats,omitempty"`
	OptionalStyleBeats     []string                         `json:"optional_style_beats,omitempty"`
	ForbiddenMoves         []string                         `json:"forbidden_moves,omitempty"`
	ContinuityChecks       []string                         `json:"continuity_checks,omitempty"`
	PayoffPoints           []string                         `json:"payoff_points,omitempty"`
	SceneAnchors           []string                         `json:"scene_anchors,omitempty"`
	CandidateBeats         []draftCandidateBeat             `json:"candidate_beats,omitempty"`
	RevealBudget           []string                         `json:"reveal_budget,omitempty"`
	CutOrCompress          []string                         `json:"cut_or_compress,omitempty"`
	PageTurnQuestions      []string                         `json:"page_turn_questions,omitempty"`
	ProtagonistProjection  draftProtagonistProjection       `json:"protagonist_projection"`
	EntertainmentPlan      draftEntertainmentPlan           `json:"reader_entertainment_plan,omitempty"`
	TrendLanguagePlan      []draftTrendLanguagePlan         `json:"trend_language_plan,omitempty"`
	LongformOpening        draftLongformOpening             `json:"longform_opening,omitempty"`
	AntiAIExecutionPlan    draftAntiAIPlan                  `json:"anti_ai_execution_plan,omitempty"`
	VoiceCards             []draftVoiceCard                 `json:"voice_cards,omitempty"`
	VisualCards            []draftVisualCard                `json:"visual_cards,omitempty"`
	DialogueScenes         []draftDialogueScene             `json:"dialogue_scenes,omitempty"`
	GroundingDetails       []domain.GroundingDetailPlan     `json:"grounding_details,omitempty"`
	EnvironmentSignals     []draftEnvironmentSignal         `json:"environment_signals,omitempty"`
	EndingContract         domain.EndingConsequenceContract `json:"ending_consequence_contract,omitempty"`
	EndingAnchorCandidate  string                           `json:"ending_anchor_candidate,omitempty"`
	SelectionPolicy        string                           `json:"selection_policy"`
	SceneBridgePolicy      string                           `json:"scene_bridge_policy"`
	DialogueTopologyPolicy string                           `json:"dialogue_topology_policy"`
	SystemVoicePolicy      string                           `json:"system_voice_policy"`
	JargonPolicy           string                           `json:"jargon_policy"`
	PlanTranslationPolicy  string                           `json:"plan_translation_policy"`
	ReaderRegisterPolicy   string                           `json:"reader_register_policy"`
	InterfaceCompression   string                           `json:"interface_compression_policy"`
	ScenePurposePolicy     string                           `json:"scene_purpose_policy"`
	SpokenLanguagePolicy   string                           `json:"spoken_language_policy"`
}

type draftEntertainmentPlan struct {
	OpeningBeat          string   `json:"opening_beat,omitempty"`
	HumorBeats           []string `json:"humor_beats,omitempty"`
	ImmediatePayoffs     []string `json:"immediate_payoffs,omitempty"`
	ProcedureCompression string   `json:"procedure_compression,omitempty"`
	CompanionVoiceBeat   string   `json:"companion_voice_beat,omitempty"`
}

type draftTrendLanguagePlan struct {
	Item             string `json:"item,omitempty"`
	CharacterCarrier string `json:"character_carrier,omitempty"`
	SceneFunction    string `json:"scene_function,omitempty"`
	UsageBudget      string `json:"usage_budget,omitempty"`
}

type draftLongformOpening struct {
	TargetReader      string   `json:"target_reader,omitempty"`
	OpeningHook       string   `json:"opening_hook,omitempty"`
	FirstChapterProof []string `json:"first_chapter_proof,omitempty"`
	RetentionRisks    []string `json:"retention_risks,omitempty"`
}

type draftAntiAIPlan struct {
	RiskSignals  []string `json:"risk_signals,omitempty"`
	CounterMoves []string `json:"counter_moves,omitempty"`
}

type draftProtagonistProjection struct {
	Protagonist       string   `json:"protagonist,omitempty"`
	ObservableEffects []string `json:"observable_effects,omitempty"`
	AvailableOptions  []string `json:"available_options,omitempty"`
	ChosenDecision    string   `json:"chosen_decision,omitempty"`
	DecisionReason    string   `json:"decision_reason,omitempty"`
	PlanConstraints   []string `json:"plan_constraints,omitempty"`
	CausalChain       []string `json:"causal_chain,omitempty"`
}

type draftVoiceCard struct {
	Character          string   `json:"character,omitempty"`
	SpeechPrinciple    string   `json:"speech_principle,omitempty"`
	HiddenSubtext      string   `json:"hidden_subtext,omitempty"`
	KnowledgeBoundary  string   `json:"knowledge_boundary,omitempty"`
	RelationshipStance string   `json:"relationship_stance,omitempty"`
	DictionAndRhythm   string   `json:"diction_and_rhythm,omitempty"`
	TypicalMoves       []string `json:"typical_moves,omitempty"`
	ForbiddenMoves     []string `json:"forbidden_moves,omitempty"`
}

type draftCandidateBeat struct {
	Event         string `json:"event,omitempty"`
	ReaderPayoff  string `json:"reader_payoff,omitempty"`
	SceneVehicle  string `json:"scene_vehicle,omitempty"`
	FunctionShift string `json:"function_shift,omitempty"`
}

type draftVisualCard struct {
	Character       string `json:"character,omitempty"`
	Silhouette      string `json:"silhouette,omitempty"`
	FaceAndHair     string `json:"face_and_hair,omitempty"`
	ClothingStyle   string `json:"clothing_style,omitempty"`
	BodyLanguage    string `json:"body_language,omitempty"`
	SignatureObject string `json:"signature_object,omitempty"`
	FirstImpression string `json:"first_impression,omitempty"`
	StatusWear      string `json:"status_wear,omitempty"`
	SceneUse        string `json:"scene_use,omitempty"`
}

type draftDialogueScene struct {
	SceneID           string   `json:"scene_id,omitempty"`
	ScenePressure     string   `json:"scene_pressure,omitempty"`
	RelationshipFrame string   `json:"relationship_frame,omitempty"`
	Participants      []string `json:"participants,omitempty"`
	LocationAnchor    string   `json:"location_anchor,omitempty"`
	DialogueObjective string   `json:"dialogue_objective,omitempty"`
	ExitBeat          string   `json:"exit_beat,omitempty"`
	DoNotUse          []string `json:"do_not_use,omitempty"`
}

type draftEnvironmentSignal struct {
	Place              string `json:"place,omitempty"`
	VisibleState       string `json:"visible_state,omitempty"`
	InformationCarried string `json:"information_carried,omitempty"`
	PressureApplied    string `json:"pressure_applied,omitempty"`
}

func applyChapterContextProfile(result map[string]any, profile string) {
	switch profile {
	case "world_simulation":
		applyWorldSimulationContextProfile(result)
	case "planning":
		applyPlanningContextProfile(result)
	case "draft":
		applyDraftContextProfile(result)
	}
}

func applyWorldSimulationContextProfile(result map[string]any) {
	// The simulator needs every character's current state and the shared world,
	// but not prose craft, prior render packets or project-level progress reports.
	for _, key := range []string{
		"outline", "progression_snapshot", "project_progress", "evolution_report",
		"chapter_world_deltas", "side_character_journeys", "future_outline_window",
		"recent_summaries", "previous_tail", "references", "writing_engine",
		"style_rules", "style_stats", "voice_samples", "rag_recall", "retrieval_trace",
		"chapter_plan", "chapter_contract", "causal_simulation", "next_plan",
	} {
		deleteContextKey(result, key)
	}
	result["world_simulation_context_policy"] = "保留全部实名角色的当前状态、知识边界、关系、资源和本章大纲；写法资料、正文渲染历史与重复项目报告已隐藏。"
}

func applyPlanningContextProfile(result map[string]any) {
	// Once the world simulation is finalized, the POV planner consumes its
	// protagonist projection. Full off-screen decisions stay durable on disk and
	// must not be copied into the POV plan or paid for again in every planning turn.
	for _, key := range []string{
		"outline", "progression_snapshot", "project_progress", "evolution_report",
		"chapter_world_deltas", "character_stage_records", "side_character_journeys",
		"premise", "world_rules", "recent_summaries",
	} {
		deleteContextKey(result, key)
	}
	sanitizePlanningWorldSimulation(result)
	result["planning_context_policy"] = "全角色决定已落盘；本阶段只依据 protagonist_projection、当前章/未来窗口、角色声口与审核约束生成主视角 plan。"
}

func applyDraftContextProfile(result map[string]any) {
	working, _ := result["working_memory"].(map[string]any)
	plan := chapterPlanFromContext(result, working)
	if plan != nil {
		packet := newDraftRenderPacket(*plan)
		if simulation, ok := result["chapter_world_simulation"].(map[string]any); ok {
			if projection, ok := draftProjectionFromAny(simulation["protagonist_projection"]); ok {
				packet.ProtagonistProjection = projection
			}
		}
		result["render_packet"] = packet
		if working != nil {
			working["render_packet"] = packet
		}

		leanContract := plan.Contract
		leanContract.RequiredBeats = append([]string(nil), packet.MandatoryBeats...)
		leanPlan := map[string]any{
			"chapter":       plan.Chapter,
			"title":         plan.Title,
			"goal":          plan.Goal,
			"conflict":      plan.Conflict,
			"hook":          plan.Hook,
			"contract":      leanContract,
			"render_policy": "范围与禁区仍以本对象为准；正文素材只从 render_packet 选择，不展开完整 causal_simulation。",
		}
		result["chapter_plan"] = leanPlan
		if working != nil {
			working["chapter_plan"] = leanPlan
			working["chapter_contract"] = leanContract
		}
		result["chapter_contract"] = leanContract
	}

	for _, key := range []string{"causal_simulation", "causal_simulation_policy"} {
		delete(result, key)
		if working != nil {
			delete(working, key)
		}
	}
	sanitizeDraftWorldSimulation(result)
	for _, key := range []string{
		"outline", "progression_snapshot", "project_progress", "evolution_report",
		"chapter_world_deltas", "character_stage_records", "side_character_journeys",
		"premise", "world_rules", "characters", "character_dossiers",
		"future_outline_window", "next_chapter_outline",
	} {
		deleteContextKey(result, key)
	}
}

func sanitizePlanningWorldSimulation(result map[string]any) {
	sim, ok := result["chapter_world_simulation"].(map[string]any)
	if !ok || sim["status"] != "ready" {
		return
	}
	lean := map[string]any{}
	for _, key := range []string{
		"status", "simulation_id", "base_tick_id", "time_window", "character_count",
		"protagonist_projection", "rewrite_source", "rewrite_fact_coverage",
	} {
		if value, exists := sim[key]; exists {
			lean[key] = value
		}
	}
	lean["planning_policy"] = "完整 character_decisions 已在 meta/chapter_simulations 落盘；POV plan 只能投影 protagonist_projection，不得重写隐藏角色决定。"
	result["chapter_world_simulation"] = lean
}

func chapterPlanFromContext(result map[string]any, working map[string]any) *domain.ChapterPlan {
	values := []any{result["chapter_plan"]}
	if working != nil {
		values = append(values, working["chapter_plan"])
	}
	for _, value := range values {
		switch plan := value.(type) {
		case *domain.ChapterPlan:
			return plan
		case domain.ChapterPlan:
			copy := plan
			return &copy
		}
	}
	return nil
}

func newDraftRenderPacket(plan domain.ChapterPlan) draftRenderPacket {
	sim := plan.CausalSimulation
	mandatoryBeats := RenderRequiredOutcomes(plan)
	_, optionalStyleBeats := splitOptionalStyleBeats(plan.Contract.RequiredBeats, sim.TrendLanguage)
	candidateBeats := make([]draftCandidateBeat, 0, len(sim.ReaderRetentionPlan.SurfaceBeats))
	for _, beat := range sim.ReaderRetentionPlan.SurfaceBeats {
		if optionalStyleText(beat.MustShow+" "+beat.ProofOnPage, sim.TrendLanguage) {
			optionalStyleBeats = append(optionalStyleBeats, beat.MustShow)
			continue
		}
		candidateBeats = append(candidateBeats, draftCandidateBeat{
			Event:         beat.MustShow,
			ReaderPayoff:  beat.ReaderPayoff,
			SceneVehicle:  beat.SceneVehicle,
			FunctionShift: beat.FunctionShift,
		})
	}
	voices := make([]draftVoiceCard, 0, len(sim.VoiceLogic))
	for _, voice := range sim.VoiceLogic {
		voices = append(voices, draftVoiceCard{
			Character:          voice.Character,
			SpeechPrinciple:    firstRenderClause(voice.SpeechPrinciple),
			HiddenSubtext:      firstRenderClause(voice.HiddenSubtext),
			KnowledgeBoundary:  firstRenderClause(voice.KnowledgeBoundary),
			RelationshipStance: voice.RelationshipStance,
			DictionAndRhythm:   firstRenderClause(voice.DictionAndRhythm),
			TypicalMoves:       limitRenderStrings(voice.TypicalMoves, 2),
			ForbiddenMoves:     limitRenderStrings(voice.ForbiddenMoves, 2),
		})
	}
	visuals := make([]draftVisualCard, 0, len(sim.VisualDesign))
	for _, visual := range sim.VisualDesign {
		visuals = append(visuals, draftVisualCard{
			Character:       visual.Character,
			Silhouette:      visual.Silhouette,
			FaceAndHair:     visual.FaceAndHair,
			ClothingStyle:   visual.ClothingStyle,
			BodyLanguage:    visual.BodyLanguage,
			SignatureObject: visual.SignatureObject,
			FirstImpression: visual.FirstImpression,
			StatusWear:      visual.StatusWear,
			SceneUse:        visual.SceneUse,
		})
	}
	dialogueScenes := make([]draftDialogueScene, 0, min(3, len(sim.DialogueBlueprints)))
	for _, scene := range sim.DialogueBlueprints {
		if !keepDraftDialogueScene(scene) || len(dialogueScenes) >= 3 {
			continue
		}
		dialogueScenes = append(dialogueScenes, draftDialogueScene{
			SceneID:           scene.SceneID,
			ScenePressure:     firstRenderClause(scene.ScenePressure),
			RelationshipFrame: firstRenderClause(scene.RelationshipFrame),
			Participants:      limitRenderStrings(scene.Participants, 5),
			LocationAnchor:    firstRenderClause(scene.LocationAnchor),
			DialogueObjective: firstRenderClause(scene.DialogueObjective),
			ExitBeat:          firstRenderClause(scene.ExitBeat),
			DoNotUse:          limitRenderStrings(scene.DoNotUse, 3),
		})
	}
	environment := make([]draftEnvironmentSignal, 0, len(sim.EnvironmentState))
	for _, signal := range sim.EnvironmentState {
		environment = append(environment, draftEnvironmentSignal{
			Place:              signal.Place,
			VisibleState:       signal.VisibleState,
			InformationCarried: signal.InformationCarried,
			PressureApplied:    signal.PressureApplied,
		})
	}
	projection := draftProtagonistProjection{
		Protagonist:     sim.ProtagonistDecision,
		ChosenDecision:  sim.ProtagonistDecision,
		PlanConstraints: sim.SceneConstraints,
	}
	return draftRenderPacket{
		Version:                1,
		Chapter:                plan.Chapter,
		Title:                  plan.Title,
		Goal:                   plan.Goal,
		Conflict:               plan.Conflict,
		Hook:                   plan.Hook,
		EmotionArc:             plan.EmotionArc,
		MandatoryBeats:         mandatoryBeats,
		OptionalStyleBeats:     compactStrings(optionalStyleBeats),
		ForbiddenMoves:         limitRenderStrings(plan.Contract.ForbiddenMoves, 8),
		ContinuityChecks:       RenderContinuityChecks(plan),
		PayoffPoints:           limitRenderStrings(plan.Contract.PayoffPoints, 3),
		SceneAnchors:           renderSceneAnchors(plan.Contract.SceneAnchors),
		CandidateBeats:         limitCandidateBeats(candidateBeats, 2),
		RevealBudget:           limitRenderStrings(sim.ReaderRetentionPlan.RevealBudget, 4),
		CutOrCompress:          limitRenderStrings(sim.ReaderRetentionPlan.CutOrCompress, 4),
		PageTurnQuestions:      limitRenderStrings(sim.ReaderRetentionPlan.PageTurnQuestions, 2),
		ProtagonistProjection:  projection,
		EntertainmentPlan:      leanEntertainmentPlan(sim.EntertainmentPlan),
		TrendLanguagePlan:      leanTrendLanguagePlan(sim.TrendLanguage),
		LongformOpening:        leanLongformOpening(sim.LongformOpening),
		AntiAIExecutionPlan:    leanAntiAIPlan(sim.AntiAIPlan),
		VoiceCards:             limitVoiceCards(voices, 6),
		VisualCards:            limitVisualCards(visuals, 4),
		DialogueScenes:         dialogueScenes,
		GroundingDetails:       limitGroundingDetails(sim.GroundingDetails, 3),
		EnvironmentSignals:     limitEnvironmentSignals(environment, 3),
		EndingContract:         RenderEndingContract(plan),
		EndingAnchorCandidate:  sim.EndingContract.ConcreteAnchor,
		SelectionPolicy:        "mandatory_beats 是本章必须成立的结果，不是动作顺序或句子清单。每个结果只选一个最有戏、最容易看懂的页面证据；同一场景可合并多个结果。candidate_beats 通常只选 2-4 个，其余直接省略，不得用旁白、对白或流程段补交。optional_style_beats 里的热梗、颜文字和指定说法零使用也允许。",
		SceneBridgePolicy:      "每次换场前先让上一场的余波变成主角当下的需要或选择，再进入下一地点；页面至少看得见‘为什么现在去’和‘去了先碰到什么阻力’。仅写锁屏、下楼、到达某地不算因果桥。",
		DialogueTopologyPolicy: "动作拍不是对白轮次的必填项。先用少量具体信息定住人物和空间，进入交锋后允许连续裸对白、打断、漏答、答非所问与无人接话；动作只在改变权力、遮掩信息、打断话头或影响现场结果时出现。",
		SystemVoicePolicy:      "系统先回答主角此刻真正问的事，一次只给一条具体规则或一个可执行提示。首次任务必须让普通读者马上知道：主角能做什么、不能做什么、现在去哪里完成什么；数字、地点、时限和完成条件用日常话说清，不让读者替系统推理。陪伴感来自接住具体情绪、吐槽具体处境和共同做选择；禁用‘钱没跑’‘陪你换条路’‘规矩不撤’‘先喘半口气’等没有对象和后果的客服式安慰。",
		JargonPolicy:           "面向无行业经验的普通读者。专业角色可以知道术语，但台词和叙述必须让读者当场看懂会坏在哪里、谁会吃亏、下一步要做什么；‘补测、核验、用途说明、临时固定’等词若不能由可见后果自然解释，就改成日常说法或删除。",
		PlanTranslationPolicy:  "先把每场计划翻成三个读者问题：此刻最想看什么、最容易看不懂什么、这一场结束后什么发生了变化。再按人物欲望、阻力和结果重组场景。计划中的动作拍、举例、验证路径和句序都可删除、合并、替换或调序；只保留结果事实、因果边界、人物选择和不可改的金额地点。ending_anchor_candidate 只是候选镜头；章末只要兑现 consequence 与 next_chapter_pull，可换成更强的现场人物、动作或结果。禁止按 plan 原句顺序逐句渲染。",
		ReaderRegisterPolicy:   "默认写给没有行业经验的大众类型文读者。优先使用能在饭桌、摊位、街边自然说出口的常用词和短解释；县城普通居民不替作者说工整对仗、验收术语或设计感强的俏皮话。每个新规则只允许一个必要概念，并立刻用人物能得到或失去什么讲明白。",
		InterfaceCompression:   "点击、按钮变灰、改备注、删输入等界面操作本身不是戏。若两次以上试错只证明同一条边界，只保留最能改变人物判断的一次，其余用一句结果带过或直接删除；禁止把 plan 中的验证动作排成‘点击—失败—再点击—再失败’清单。",
		ScenePurposePolicy:     "每场只保留一个主要戏剧问题。授权、询价、安装、测试、开票、付款、检查若都只是在证明事情办妥，压成一次有阻力的交涉、一个时间跳转和一个可见结果；不得让不同配角轮流替计划补齐步骤。首笔兑现的重点是主角敢不敢真花、钱是否真能花出去、结果改变了谁，不是流程是否逐项完成。",
		SpokenLanguagePolicy:   "每句对白先问：这个人眼下在争什么、怕什么、嫌什么。若一句话只是在替作者解释流程或制造漂亮包袱，就删掉或改成当场会脱口而出的半句话。普通居民能说‘往外挪点，晃眼’，就不说工整比喻和‘你是X还是Y’式机智反问；除非人物长期声口和关系语境都明确支持这种说法。",
	}
}

func firstRenderClause(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	for _, separator := range []string{"；", "。", "\n"} {
		if index := strings.Index(text, separator); index >= 0 {
			text = strings.TrimSpace(text[:index])
		}
	}
	runes := []rune(text)
	if len(runes) > 120 {
		text = strings.TrimSpace(string(runes[:120]))
	}
	return text
}

func keepDraftDialogueScene(scene domain.DialogueSceneBlueprint) bool {
	mode := strings.ToLower(strings.TrimSpace(scene.DialogueMode))
	context := scene.SceneID + " " + scene.RelationshipFrame + " " + scene.ScenePressure + " " + scene.DialogueObjective
	relationshipBearing := containsAnyRenderPhrase(context, []string{
		"旧识", "亲友", "父母", "朋友", "同盟", "恋", "暧昧", "信任", "关系", "体面", "羞耻", "控制权",
	})
	if strings.Contains(mode, "mediated") && containsAnyRenderPhrase(context, []string{"系统", "结算", "任务卡", "额度"}) {
		return false
	}
	if !strings.Contains(mode, "logistics") && !strings.Contains(mode, "settlement") &&
		!strings.Contains(mode, "status") && !strings.Contains(mode, "report") && !strings.Contains(mode, "procedure") {
		return true
	}
	return relationshipBearing
}

func renderSceneAnchors(anchors []string) []string {
	out := make([]string, 0, len(anchors))
	for _, anchor := range anchors {
		if containsAnyRenderPhrase(anchor, []string{"材料清单", "采购凭证", "测试记录", "漏保测试", "电子票据"}) {
			continue
		}
		out = append(out, anchor)
	}
	return limitRenderStrings(out, 4)
}

func leanEntertainmentPlan(plan domain.ReaderEntertainmentPlan) draftEntertainmentPlan {
	return draftEntertainmentPlan{
		OpeningBeat:          firstRenderClause(plan.OpeningBeat),
		HumorBeats:           limitRenderStrings(plan.HumorBeats, 2),
		ImmediatePayoffs:     limitRenderStrings(plan.ImmediatePayoffs, 3),
		ProcedureCompression: firstRenderClause(plan.ProcedureCompression),
		CompanionVoiceBeat:   firstRenderClause(plan.CompanionVoiceBeat),
	}
}

func leanTrendLanguagePlan(plans []domain.TrendLanguagePlan) []draftTrendLanguagePlan {
	out := make([]draftTrendLanguagePlan, 0, min(2, len(plans)))
	for _, plan := range plans {
		if len(out) >= 2 {
			break
		}
		out = append(out, draftTrendLanguagePlan{
			Item:             plan.Item,
			CharacterCarrier: firstRenderClause(plan.CharacterCarrier),
			SceneFunction:    firstRenderClause(plan.SceneFunction),
			UsageBudget:      firstRenderClause(plan.UsageBudget),
		})
	}
	return out
}

func leanLongformOpening(opening domain.LongformOpeningDesign) draftLongformOpening {
	return draftLongformOpening{
		TargetReader:      firstRenderClause(opening.TargetReader),
		OpeningHook:       firstRenderClause(opening.OpeningHook),
		FirstChapterProof: limitRenderStrings(opening.FirstChapterProof, 2),
		RetentionRisks:    limitRenderStrings(opening.RetentionRisks, 2),
	}
}

func leanAntiAIPlan(plan domain.AntiAIExecutionPlan) draftAntiAIPlan {
	return draftAntiAIPlan{
		RiskSignals:  limitRenderStrings(plan.RiskSignals, 4),
		CounterMoves: limitRenderStrings(plan.CounterMoves, 4),
	}
}

func limitRenderStrings(values []string, limit int) []string {
	values = compactStrings(values)
	if limit <= 0 || len(values) == 0 {
		return nil
	}
	if len(values) > limit {
		values = values[:limit]
	}
	return append([]string(nil), values...)
}

func containsAnyRenderPhrase(text string, phrases []string) bool {
	for _, phrase := range phrases {
		if strings.Contains(text, phrase) {
			return true
		}
	}
	return false
}

func limitCandidateBeats(values []draftCandidateBeat, limit int) []draftCandidateBeat {
	if limit <= 0 || len(values) == 0 {
		return nil
	}
	if len(values) > limit {
		values = values[:limit]
	}
	return append([]draftCandidateBeat(nil), values...)
}

func limitVoiceCards(values []draftVoiceCard, limit int) []draftVoiceCard {
	if limit <= 0 || len(values) == 0 {
		return nil
	}
	if len(values) > limit {
		values = values[:limit]
	}
	return append([]draftVoiceCard(nil), values...)
}

func limitVisualCards(values []draftVisualCard, limit int) []draftVisualCard {
	if limit <= 0 || len(values) == 0 {
		return nil
	}
	if len(values) > limit {
		values = values[:limit]
	}
	return append([]draftVisualCard(nil), values...)
}

func limitGroundingDetails(values []domain.GroundingDetailPlan, limit int) []domain.GroundingDetailPlan {
	if limit <= 0 || len(values) == 0 {
		return nil
	}
	if len(values) > limit {
		values = values[:limit]
	}
	return append([]domain.GroundingDetailPlan(nil), values...)
}

func limitEnvironmentSignals(values []draftEnvironmentSignal, limit int) []draftEnvironmentSignal {
	if limit <= 0 || len(values) == 0 {
		return nil
	}
	if len(values) > limit {
		values = values[:limit]
	}
	return append([]draftEnvironmentSignal(nil), values...)
}

// RenderRequiredOutcomes projects the full chapter contract into the smallest
// result-level contract needed by prose. The source plan remains untouched and
// keeps every simulation fact; only the Drafter-facing view removes duplicate
// outline anchors and prefers concise outcome statements over process recipes.
func RenderRequiredOutcomes(plan domain.ChapterPlan) []string {
	beats, _ := splitOptionalStyleBeats(plan.Contract.RequiredBeats, plan.CausalSimulation.TrendLanguage)
	out := make([]string, 0, len(beats))
	for _, raw := range beats {
		beat := unwrapRenderOutcome(raw)
		if beat == "" {
			continue
		}
		merged := false
		for i, existing := range out {
			if !renderOutcomesEquivalent(existing, beat) {
				continue
			}
			if preferRenderOutcome(beat, existing) {
				out[i] = beat
			}
			merged = true
			break
		}
		if !merged {
			out = append(out, beat)
		}
	}
	return compactStrings(out)
}

// RenderContinuityChecks keeps factual continuity in the prose packet while
// removing presentation instructions that belong to style or review policy.
func RenderContinuityChecks(plan domain.ChapterPlan) []string {
	out := make([]string, 0, len(plan.Contract.ContinuityChecks))
	for _, check := range plan.Contract.ContinuityChecks {
		if optionalStyleText(check, plan.CausalSimulation.TrendLanguage) || renderOnlyContinuityCheck(check) {
			continue
		}
		out = append(out, check)
	}
	return compactStrings(out)
}

func renderOnlyContinuityCheck(text string) bool {
	for _, marker := range []string{
		"章末具体锚点", "短消息分开发送", "颜文字", "拟声", "吐槽的起头",
		"每次只承担拒绝", "不能连续用界面", "必须位于报价确认后", "台词原句",
	} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

// RenderEndingContract preserves the consequence and forward pull but moves a
// prescribed final prop/shot out of the hard contract. Exact framing belongs
// in EndingAnchorCandidate so the Drafter can replace it with a stronger beat.
func RenderEndingContract(plan domain.ChapterPlan) domain.EndingConsequenceContract {
	contract := plan.CausalSimulation.EndingContract
	contract.ConcreteAnchor = ""
	contract.WhyNotUI = ""
	return contract
}

func unwrapRenderOutcome(text string) string {
	text = strings.TrimSpace(text)
	for _, prefix := range []string{
		"必须完整兑现大纲核心事件：",
		"必须兑现大纲钩子；若现有章节契约已将其前移，则作为中段转折而非强行改写章末：",
	} {
		if strings.HasPrefix(text, prefix) {
			// goal / hook already carry these outline anchors in render_packet.
			return ""
		}
	}
	return text
}

func renderOutcomesEquivalent(a, b string) bool {
	a = normalizeRenderOutcome(a)
	b = normalizeRenderOutcome(b)
	if a == "" || b == "" {
		return false
	}
	shorter, longer := a, b
	if utf8.RuneCountInString(shorter) > utf8.RuneCountInString(longer) {
		shorter, longer = longer, shorter
	}
	if utf8.RuneCountInString(shorter) >= 8 && strings.Contains(longer, shorter) {
		return true
	}
	aPairs := renderOutcomeBigrams(a)
	bPairs := renderOutcomeBigrams(b)
	if len(aPairs) < 6 || len(bPairs) < 6 {
		return false
	}
	intersection := 0
	for pair := range aPairs {
		if _, ok := bPairs[pair]; ok {
			intersection++
		}
	}
	denominator := min(len(aPairs), len(bPairs))
	return intersection >= 6 && float64(intersection)/float64(denominator) >= 0.52
}

func normalizeRenderOutcome(text string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(text) {
		if unicode.Is(unicode.Han, r) || unicode.IsDigit(r) || unicode.IsLetter(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func renderOutcomeBigrams(text string) map[string]struct{} {
	runes := []rune(text)
	out := make(map[string]struct{}, max(0, len(runes)-1))
	for i := 0; i+1 < len(runes); i++ {
		out[string(runes[i:i+2])] = struct{}{}
	}
	return out
}

func preferRenderOutcome(candidate, current string) bool {
	candidateLen := utf8.RuneCountInString(candidate)
	currentLen := utf8.RuneCountInString(current)
	if candidateLen < 8 {
		return false
	}
	if currentLen < 8 {
		return true
	}
	return candidateLen < currentLen
}

func splitOptionalStyleBeats(beats []string, trends []domain.TrendLanguagePlan) ([]string, []string) {
	var mandatory []string
	var optional []string
	for _, beat := range beats {
		if optionalStyleText(beat, trends) {
			optional = append(optional, beat)
			continue
		}
		mandatory = append(mandatory, beat)
	}
	return mandatory, optional
}

func optionalStyleText(text string, trends []domain.TrendLanguagePlan) bool {
	trimmed := strings.TrimSpace(text)
	// A style literal embedded in a compound event must not demote the event's
	// real outcome (for example, "赵航用梗打断；林澈离席"). Only pure style
	// requirements belong in optional_style_beats.
	compound := strings.TrimRight(trimmed, "。！？!?；;")
	if strings.ContainsAny(compound, "；;。") {
		return false
	}
	for _, marker := range []string{"热梗", "颜文字", "台词原句", "原样使用", "必须说成", "句式槽位"} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	trendTokens := make([]string, 0, len(trends)*2)
	for _, trend := range trends {
		item := strings.Trim(strings.TrimSpace(trend.Item), "`'\"“”‘’")
		if item == "" {
			continue
		}
		trendTokens = append(trendTokens, item)
		if strings.HasPrefix(item, "呱") {
			trendTokens = append(trendTokens, "呱")
		}
	}
	if strings.Contains(text, "^_^") && utf8.RuneCountInString(trimmed) <= 60 {
		return true
	}
	for _, token := range trendTokens {
		if token != "" && strings.Contains(text, token) && utf8.RuneCountInString(trimmed) <= 60 &&
			containsRenderStyleMarker(text) {
			return true
		}
	}
	return false
}

func containsRenderStyleMarker(text string) bool {
	for _, marker := range []string{"必须", "原句", "原样", "起头", "说出", "使用"} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

func sanitizeDraftWorldSimulation(result map[string]any) {
	sim, ok := result["chapter_world_simulation"].(map[string]any)
	if !ok {
		return
	}
	lean := map[string]any{}
	for _, key := range []string{"status", "simulation_id", "base_tick_id", "time_window", "character_count", "rewrite_source", "rewrite_fact_coverage"} {
		if value, exists := sim[key]; exists {
			lean[key] = value
		}
	}
	if rawProjection, exists := sim["protagonist_projection"]; exists {
		lean["protagonist_projection"] = sanitizeProtagonistProjection(rawProjection)
	}
	lean["render_policy"] = "仅渲染 protagonist_projection.observable_effects 与主角合法获得的信息；全角色决定和 hidden_pressures 已从 draft profile 隐藏。"
	result["chapter_world_simulation"] = lean
}

func sanitizeProtagonistProjection(value any) any {
	if projection, ok := draftProjectionFromAny(value); ok {
		return projection
	}
	return value
}

func draftProjectionFromAny(value any) (draftProtagonistProjection, bool) {
	switch projection := value.(type) {
	case domain.ProtagonistDecisionProjection:
		return draftProtagonistProjection{
			Protagonist:       projection.Protagonist,
			ObservableEffects: projection.ObservableEffects,
			AvailableOptions:  projection.AvailableOptions,
			ChosenDecision:    projection.ChosenDecision,
			DecisionReason:    projection.DecisionReason,
			PlanConstraints:   projection.PlanConstraints,
			CausalChain:       projection.CausalChain,
		}, true
	case *domain.ProtagonistDecisionProjection:
		if projection == nil {
			return draftProtagonistProjection{}, false
		}
		return draftProjectionFromAny(*projection)
	case map[string]any:
		raw, err := json.Marshal(projection)
		if err != nil {
			return draftProtagonistProjection{}, false
		}
		var lean draftProtagonistProjection
		if err := json.Unmarshal(raw, &lean); err != nil {
			return draftProtagonistProjection{}, false
		}
		return lean, true
	default:
		return draftProtagonistProjection{}, false
	}
}
