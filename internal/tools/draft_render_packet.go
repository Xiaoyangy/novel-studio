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
	Version                 int                              `json:"version"`
	Chapter                 int                              `json:"chapter"`
	Title                   string                           `json:"title,omitempty"`
	Goal                    string                           `json:"goal,omitempty"`
	Conflict                string                           `json:"conflict,omitempty"`
	Hook                    string                           `json:"hook,omitempty"`
	EmotionArc              string                           `json:"emotion_arc,omitempty"`
	MandatoryBeats          []string                         `json:"mandatory_beats,omitempty"`
	OptionalStyleBeats      []string                         `json:"optional_style_beats,omitempty"`
	ForbiddenMoves          []string                         `json:"forbidden_moves,omitempty"`
	ContinuityChecks        []string                         `json:"continuity_checks,omitempty"`
	PayoffPoints            []string                         `json:"payoff_points,omitempty"`
	SceneAnchors            []string                         `json:"scene_anchors,omitempty"`
	CandidateBeats          []draftCandidateBeat             `json:"candidate_beats,omitempty"`
	RevealBudget            []string                         `json:"reveal_budget,omitempty"`
	CutOrCompress           []string                         `json:"cut_or_compress,omitempty"`
	PageTurnQuestions       []string                         `json:"page_turn_questions,omitempty"`
	ProtagonistProjection   draftProtagonistProjection       `json:"protagonist_projection"`
	EntertainmentPlan       draftEntertainmentPlan           `json:"reader_entertainment_plan,omitempty"`
	TrendLanguagePlan       []draftTrendLanguagePlan         `json:"trend_language_plan,omitempty"`
	LongformOpening         draftLongformOpening             `json:"longform_opening,omitempty"`
	AntiAIExecutionPlan     draftAntiAIPlan                  `json:"anti_ai_execution_plan,omitempty"`
	VoiceCards              []draftVoiceCard                 `json:"voice_cards,omitempty"`
	VisualCards             []draftVisualCard                `json:"visual_cards,omitempty"`
	DialogueScenes          []draftDialogueScene             `json:"dialogue_scenes,omitempty"`
	EmotionalLenses         []draftEmotionalLens             `json:"emotional_lenses,omitempty"`
	RelationshipLenses      []draftRelationshipLens          `json:"relationship_lenses,omitempty"`
	VisibleCharacters       []string                         `json:"visible_characters,omitempty"`
	ExcludedNamedCharacters []string                         `json:"excluded_named_characters,omitempty"`
	GroundingDetails        []domain.GroundingDetailPlan     `json:"grounding_details,omitempty"`
	EnvironmentSignals      []draftEnvironmentSignal         `json:"environment_signals,omitempty"`
	EndingContract          domain.EndingConsequenceContract `json:"ending_consequence_contract,omitempty"`
	EndingAnchorCandidate   string                           `json:"ending_anchor_candidate,omitempty"`
	SelectionPolicy         string                           `json:"selection_policy"`
	SceneBridgePolicy       string                           `json:"scene_bridge_policy"`
	DialogueTopologyPolicy  string                           `json:"dialogue_topology_policy"`
	SystemVoicePolicy       string                           `json:"system_voice_policy"`
	JargonPolicy            string                           `json:"jargon_policy"`
	PlanTranslationPolicy   string                           `json:"plan_translation_policy"`
	ReaderRegisterPolicy    string                           `json:"reader_register_policy"`
	InterfaceCompression    string                           `json:"interface_compression_policy"`
	ScenePurposePolicy      string                           `json:"scene_purpose_policy"`
	SpokenLanguagePolicy    string                           `json:"spoken_language_policy"`
	EmotionalRenderPolicy   string                           `json:"emotional_render_policy"`
	GroupCompressionPolicy  string                           `json:"group_compression_policy"`
	ChronologyPolicy        string                           `json:"chronology_policy"`
	ProofFocusPolicy        string                           `json:"proof_focus_policy"`
	NamedRolePolicy         string                           `json:"named_role_policy"`
	RelationshipPriority    string                           `json:"relationship_priority_policy"`
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

type draftEmotionalLens struct {
	Character        string   `json:"character,omitempty"`
	ImmediateState   string   `json:"immediate_state,omitempty"`
	PrimaryEmotion   string   `json:"primary_emotion,omitempty"`
	Trigger          string   `json:"trigger,omitempty"`
	GoalAppraisal    string   `json:"goal_appraisal,omitempty"`
	Regulation       string   `json:"regulation,omitempty"`
	EmotionLedAction string   `json:"emotion_led_action,omitempty"`
	SceneEvidence    []string `json:"scene_evidence,omitempty"`
}

type draftRelationshipLens struct {
	Pair              []string `json:"pair,omitempty"`
	CurrentBond       string   `json:"current_bond,omitempty"`
	EmotionalWant     string   `json:"emotional_want,omitempty"`
	Fear              string   `json:"fear,omitempty"`
	Boundary          string   `json:"boundary,omitempty"`
	NextEmotionalBeat string   `json:"next_emotional_beat,omitempty"`
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
	compactDraftRewriteBrief(result)
	if working != nil {
		compactDraftRewriteBrief(working)
	}
	plan := chapterPlanFromContext(result, working)
	if plan != nil {
		packet := newDraftRenderPacket(*plan)
		if simulation, ok := result["chapter_world_simulation"].(map[string]any); ok {
			if projection, ok := draftProjectionFromAny(simulation["protagonist_projection"]); ok {
				packet.ProtagonistProjection = leanDraftProjection(projection)
			}
			packet.VisibleCharacters, packet.ExcludedNamedCharacters = draftVisibilityFromSimulation(simulation)
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

func compactDraftRewriteBrief(container map[string]any) {
	if container == nil {
		return
	}
	brief, ok := container["rewrite_brief"].(map[string]any)
	if !ok || brief == nil {
		return
	}
	compact := map[string]any{
		"render_policy": "只吸收问题、人工硬约束和必须保留的结果；评审证据里的示例动作、示例台词与指标补丁不是剧情指令，禁止照搬或换皮。",
	}
	for _, key := range []string{
		"reason", "review_summary", "contract_misses", "human_acceptance_supplements", "human_acceptance_policy",
	} {
		if value, exists := brief[key]; exists {
			compact[key] = value
		}
	}
	if issues := compactDraftReviewIssues(brief["issues"], 3); len(issues) > 0 {
		compact["issues"] = issues
	}
	if rules := compactDraftAIVoiceRules(brief["ai_voice_redflags"], 4); len(rules) > 0 {
		compact["ai_voice_rules"] = rules
	}
	container["rewrite_brief"] = compact
}

func compactDraftReviewIssues(raw any, limit int) []map[string]string {
	if limit <= 0 || raw == nil {
		return nil
	}
	var issues []domain.ConsistencyIssue
	switch values := raw.(type) {
	case []domain.ConsistencyIssue:
		issues = values
	default:
		encoded, err := json.Marshal(values)
		if err != nil || json.Unmarshal(encoded, &issues) != nil {
			return nil
		}
	}
	out := make([]map[string]string, 0, min(limit, len(issues)))
	for _, issue := range issues {
		if len(out) >= limit {
			break
		}
		problem := firstRenderClause(issue.Description)
		if problem == "" {
			continue
		}
		out = append(out, map[string]string{
			"type":     strings.TrimSpace(issue.Type),
			"severity": strings.TrimSpace(issue.Severity),
			"problem":  problem,
		})
	}
	return out
}

func compactDraftAIVoiceRules(raw any, limit int) []string {
	if limit <= 0 || raw == nil {
		return nil
	}
	var analysis domain.AIVoiceAnalysis
	switch value := raw.(type) {
	case *domain.AIVoiceAnalysis:
		if value == nil {
			return nil
		}
		analysis = *value
	case domain.AIVoiceAnalysis:
		analysis = value
	default:
		encoded, err := json.Marshal(value)
		if err != nil || json.Unmarshal(encoded, &analysis) != nil {
			return nil
		}
	}
	rules := make([]string, 0, min(limit, len(analysis.RedFlags)))
	for _, flag := range analysis.RedFlags {
		if len(rules) >= limit {
			break
		}
		rule := strings.TrimSpace(flag.Rule)
		if rule != "" {
			rules = append(rules, rule)
		}
	}
	return compactStrings(rules)
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
	voices := make([]draftVoiceCard, 0, len(sim.VoiceLogic))
	for _, voice := range sim.VoiceLogic {
		voices = append(voices, draftVoiceCard{
			Character:         voice.Character,
			KnowledgeBoundary: firstRenderClause(voice.KnowledgeBoundary),
			DictionAndRhythm:  firstRenderClause(voice.DictionAndRhythm),
			ForbiddenMoves:    limitRenderStrings(voice.ForbiddenMoves, 1),
		})
	}
	relationshipLenses := make([]draftRelationshipLens, 0, min(2, len(sim.RelationshipArcs)))
	for _, arc := range sim.RelationshipArcs {
		if len(relationshipLenses) >= 2 {
			break
		}
		relationshipLenses = append(relationshipLenses, draftRelationshipLens{
			Pair:              limitRenderStrings(arc.Pair, 2),
			CurrentBond:       firstRenderClause(arc.CurrentBond),
			EmotionalWant:     firstRenderClause(arc.EmotionalWant),
			Fear:              firstRenderClause(arc.Fear),
			Boundary:          firstRenderClause(arc.Boundary),
			NextEmotionalBeat: firstRenderClause(arc.NextEmotionalBeat),
		})
	}
	if len(sim.RelationshipArcs) == 0 {
		if lens, ok := strongestDraftDialogueRelationshipLens(sim.DialogueBlueprints); ok {
			relationshipLenses = append(relationshipLenses, lens)
		}
	}
	protagonist := ""
	if len(sim.InitialState) > 0 {
		protagonist = sim.InitialState[0].Character
	}
	projection := leanDraftProjection(draftProtagonistProjection{
		Protagonist:       protagonist,
		ChosenDecision:    sim.ProtagonistDecision,
		DecisionReason:    plan.Goal,
		PlanConstraints:   sim.SceneConstraints,
		ObservableEffects: append([]string(nil), plan.Contract.RequiredBeats...),
	})
	return draftRenderPacket{
		Version:                3,
		Chapter:                plan.Chapter,
		Title:                  plan.Title,
		Hook:                   plan.Hook,
		MandatoryBeats:         mandatoryBeats,
		ForbiddenMoves:         limitRenderStrings(plan.Contract.ForbiddenMoves, 5),
		ProtagonistProjection:  projection,
		VoiceCards:             selectEssentialVoiceCards(voices),
		RelationshipLenses:     sampleRenderValues(relationshipLenses, 1),
		SelectionPolicy:        "通常只写2-4场；几个结果可以在一场里同时成立，离屏台账不写。",
		SceneBridgePolicy:      "可以直接跳时间或地点，不为转场另写解释。",
		DialogueTopologyPolicy: "谁眼下真有话谁说；允许一人连续说完，删掉替计划报步骤的台词。",
		SystemVoicePolicy:      "系统像支持主角的熟人，只接眼前具体的人或事，不说客服套话。",
		JargonPolicy:           "不用计划、审核和项目管理词；用普通人当场会说的话。",
		PlanTranslationPolicy:  "只保留欲望、风险、选择和结果，不复刻计划句序、动作或验证路径。",
		ReaderRegisterPolicy:   "面向大众读者，县城居民说日常话，不替作者总结方法。",
		InterfaceCompression:   "界面操作只留会改变选择的一次，其余直接略过。",
		ScenePurposePolicy:     "每场只让一个局面真正变化，办妥的过程可以略过。",
		SpokenLanguagePolicy:   "对白先服从说话人的身份、关系和眼前目的；朗读不顺就删或重说。",
		EmotionalRenderPolicy:  "只跟一条个人牵挂；情绪必须改变紧接着的选择，不写替读者标重的总结。",
		GroupCompressionPolicy: "多人做同类决定时只展开一个真正改变主角选择的代表；其余只落总结果，禁止按‘第一个、另一家、最后一家’逐人分配理由。",
		ChronologyPolicy:       "全章最多保留两个具体钟点；现实耗时用午饭、日光、库存或工作状态跨越，禁止按时间戳报站。",
		ProofFocusPolicy:       "小胜只跟一组顾客完整看价、付款并拿走东西；其他摊位退成背景，禁止并排举三个成功案例。",
		NamedRolePolicy:        "无名摊主保持无名；不得拿角色册里的亲戚或离屏人物给泛化摊主补名字，更不得改变已命名人物的职业和当日行踪。",
		RelationshipPriority:   "正事中留一处男女主的判断互相改变下一步选择；不靠票据问答替代关系推进，也不硬凑肢体暧昧。",
	}
}

func draftVisibilityFromSimulation(simulation map[string]any) (visible, excluded []string) {
	raw, ok := simulation["character_decisions"]
	if !ok {
		return nil, nil
	}
	var decisions []domain.CharacterWorldDecision
	switch values := raw.(type) {
	case []domain.CharacterWorldDecision:
		decisions = values
	default:
		encoded, err := json.Marshal(values)
		if err != nil || json.Unmarshal(encoded, &decisions) != nil {
			return nil, nil
		}
	}
	for _, decision := range decisions {
		name := strings.TrimSpace(decision.Character)
		if name == "" {
			continue
		}
		if decision.VisibleToPOV {
			visible = append(visible, name)
		} else {
			excluded = append(excluded, name)
		}
	}
	return compactStrings(visible), compactStrings(excluded)
}

func selectEssentialVoiceCards(values []draftVoiceCard) []draftVoiceCard {
	if len(values) == 0 {
		return nil
	}
	out := make([]draftVoiceCard, 0, 3)
	add := func(value draftVoiceCard) {
		for _, existing := range out {
			if existing.Character == value.Character {
				return
			}
		}
		out = append(out, value)
	}
	add(values[0])
	if len(values) > 1 {
		add(values[1])
	}
	for _, value := range values {
		if strings.Contains(value.Character, "系统") {
			add(value)
			break
		}
	}
	if len(out) > 3 {
		out = out[:3]
	}
	return out
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

func strongestDraftDialogueRelationshipLens(scenes []domain.DialogueSceneBlueprint) (draftRelationshipLens, bool) {
	bestIndex := -1
	bestStrength := 0
	for i, scene := range scenes {
		if !keepDraftDialogueScene(scene) {
			continue
		}
		context := scene.RelationshipFrame + " " + scene.ScenePressure + " " + scene.DialogueObjective + " " + scene.ExitBeat
		signalStrength := draftDialogueRelationshipSignalStrength(context)
		if signalStrength == 0 {
			continue
		}
		strength := signalStrength * 10
		if strings.TrimSpace(scene.RelationshipFrame) != "" {
			strength += 3
		}
		if strings.TrimSpace(scene.DialogueObjective) != "" {
			strength += 2
		}
		if strings.TrimSpace(scene.ExitBeat) != "" {
			strength++
		}
		if strength > bestStrength {
			bestIndex = i
			bestStrength = strength
		}
	}
	if bestIndex < 0 {
		return draftRelationshipLens{}, false
	}

	scene := scenes[bestIndex]
	return draftRelationshipLens{
		Pair:              limitRenderStrings(scene.Participants, 2),
		CurrentBond:       firstRenderClause(scene.RelationshipFrame),
		EmotionalWant:     firstRenderClause(scene.DialogueObjective),
		NextEmotionalBeat: firstRenderClause(scene.ExitBeat),
	}, true
}

func draftDialogueRelationshipSignalStrength(context string) int {
	strength := 0
	for _, phrase := range []string{
		"旧识", "亲友", "父母", "父子", "父女", "母子", "母女", "兄弟", "姐妹", "朋友", "同盟",
		"恋", "暧昧", "信任", "关系", "亲密", "疏远", "背叛", "关心", "护短", "试探", "体面", "羞耻", "控制权",
		"秘密", "怀疑", "信息差", "没有追问", "共同", "搭档",
	} {
		if strings.Contains(context, phrase) {
			strength++
		}
	}
	return strength
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
		HumorBeats:           sampleRenderStrings(plan.HumorBeats, 1),
		ImmediatePayoffs:     limitRenderStrings(plan.ImmediatePayoffs, 1),
		ProcedureCompression: firstRenderClause(plan.ProcedureCompression),
		CompanionVoiceBeat:   firstRenderClause(plan.CompanionVoiceBeat),
	}
}

func leanTrendLanguagePlan(plans []domain.TrendLanguagePlan) []draftTrendLanguagePlan {
	out := make([]draftTrendLanguagePlan, 0, min(1, len(plans)))
	for _, plan := range plans {
		if len(out) >= 1 {
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

func sampleRenderStrings(values []string, limit int) []string {
	return sampleRenderValues(compactStrings(values), limit)
}

// sampleRenderValues preserves the opening and payoff while spreading any
// remaining packet budget across the ordered material between them.
func sampleRenderValues[T any](values []T, limit int) []T {
	if limit <= 0 || len(values) == 0 {
		return nil
	}
	if len(values) <= limit {
		return append([]T(nil), values...)
	}
	if limit == 1 {
		return append([]T(nil), values[0])
	}

	out := make([]T, 0, limit)
	span := len(values) - 1
	steps := limit - 1
	for i := 0; i < limit; i++ {
		index := (i*span + steps/2) / steps
		out = append(out, values[index])
	}
	return out
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
	return sampleRenderValues(values, limit)
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
		beat = firstRenderClause(beat)
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
	// Old rewrite plans may predate the 2-4 outcome contract. Keep their full
	// simulation on disk, but do not feed more than four visible obligations to
	// prose or review; the first, middle shifts and final consequence are sampled.
	return sampleRenderStrings(compactStrings(out), 4)
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
	for _, key := range []string{"status", "simulation_id", "base_tick_id", "time_window", "character_count", "rewrite_source", "rewrite_fact_coverage", "source_version_policy"} {
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
		return leanDraftProjection(projection)
	}
	return value
}

// leanDraftProjection keeps the protagonist's reason for acting and a few
// visible consequences. Options, full causal chains and planning constraints
// remain on disk: when prose sees them, it tends to serialize each item as a
// scene or a line of dialogue even though mandatory_beats already define scope.
func leanDraftProjection(projection draftProtagonistProjection) draftProtagonistProjection {
	effects := make([]string, 0, len(projection.ObservableEffects))
	for _, effect := range projection.ObservableEffects {
		if clause := firstRenderClause(effect); clause != "" {
			effects = append(effects, clause)
		}
	}
	return draftProtagonistProjection{
		Protagonist:       strings.TrimSpace(projection.Protagonist),
		ObservableEffects: sampleRenderStrings(effects, 3),
		ChosenDecision:    firstRenderClause(projection.ChosenDecision),
		DecisionReason:    firstRenderClause(projection.DecisionReason),
	}
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
