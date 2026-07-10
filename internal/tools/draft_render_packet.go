package tools

import (
	"encoding/json"
	"strings"

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
	CandidateBeats         []draftCandidateBeat              `json:"candidate_beats,omitempty"`
	RevealBudget           []string                         `json:"reveal_budget,omitempty"`
	CutOrCompress          []string                         `json:"cut_or_compress,omitempty"`
	PageTurnQuestions      []string                         `json:"page_turn_questions,omitempty"`
	ProtagonistProjection  draftProtagonistProjection       `json:"protagonist_projection"`
	EntertainmentPlan      domain.ReaderEntertainmentPlan   `json:"reader_entertainment_plan,omitempty"`
	TrendLanguagePlan      []domain.TrendLanguagePlan       `json:"trend_language_plan,omitempty"`
	LongformOpening        domain.LongformOpeningDesign     `json:"longform_opening,omitempty"`
	AntiAIExecutionPlan    domain.AntiAIExecutionPlan       `json:"anti_ai_execution_plan,omitempty"`
	VoiceCards             []draftVoiceCard                 `json:"voice_cards,omitempty"`
	VisualCards            []draftVisualCard                `json:"visual_cards,omitempty"`
	DialogueScenes         []draftDialogueScene             `json:"dialogue_scenes,omitempty"`
	GroundingDetails       []domain.GroundingDetailPlan     `json:"grounding_details,omitempty"`
	EnvironmentSignals     []draftEnvironmentSignal         `json:"environment_signals,omitempty"`
	EndingContract         domain.EndingConsequenceContract `json:"ending_consequence_contract,omitempty"`
	SelectionPolicy        string                           `json:"selection_policy"`
	SceneBridgePolicy      string                           `json:"scene_bridge_policy"`
	DialogueTopologyPolicy string                           `json:"dialogue_topology_policy"`
	SystemVoicePolicy      string                           `json:"system_voice_policy"`
	JargonPolicy           string                           `json:"jargon_policy"`
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
	SceneObjective     string   `json:"scene_objective,omitempty"`
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
	SceneID                     string                          `json:"scene_id,omitempty"`
	DialogueMode                string                          `json:"dialogue_mode,omitempty"`
	ScenePressure               string                          `json:"scene_pressure,omitempty"`
	EmotionalTemperature        string                          `json:"emotional_temperature,omitempty"`
	RelationshipFrame           string                          `json:"relationship_frame,omitempty"`
	Medium                      string                          `json:"medium,omitempty"`
	POVRole                     string                          `json:"pov_role,omitempty"`
	Participants                []string                        `json:"participants,omitempty"`
	AudiencePresence            domain.DialogueAudiencePresence `json:"audience_presence"`
	InfoAsymmetry               domain.DialogueInfoAsymmetry    `json:"information_asymmetry"`
	ValueShift                  domain.DialogueValueShift       `json:"value_shift"`
	OpeningStrategy             string                          `json:"opening_strategy,omitempty"`
	LocationAnchor              string                          `json:"location_anchor,omitempty"`
	POVState                    string                          `json:"pov_state,omitempty"`
	DialogueObjective           string                          `json:"dialogue_objective,omitempty"`
	InterlocutorAgenda          string                          `json:"interlocutor_agenda,omitempty"`
	ProtagonistResponseStrategy string                          `json:"protagonist_response_strategy,omitempty"`
	DirectnessPolicy            string                          `json:"directness_policy,omitempty"`
	InfoReleasePolicy           string                          `json:"info_release_policy,omitempty"`
	ExpositionBudget            string                          `json:"exposition_budget,omitempty"`
	ExitBeat                    string                          `json:"exit_beat,omitempty"`
	DoNotUse                    []string                        `json:"do_not_use,omitempty"`
}

type draftEnvironmentSignal struct {
	Place              string `json:"place,omitempty"`
	VisibleState       string `json:"visible_state,omitempty"`
	InformationCarried string `json:"information_carried,omitempty"`
	PressureApplied    string `json:"pressure_applied,omitempty"`
}

func applyChapterContextProfile(result map[string]any, profile string) {
	if profile != "draft" {
		return
	}
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
	mandatoryBeats, optionalStyleBeats := splitOptionalStyleBeats(plan.Contract.RequiredBeats, sim.TrendLanguage)
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
			SpeechPrinciple:    voice.SpeechPrinciple,
			SceneObjective:     voice.SceneObjective,
			HiddenSubtext:      voice.HiddenSubtext,
			KnowledgeBoundary:  voice.KnowledgeBoundary,
			RelationshipStance: voice.RelationshipStance,
			DictionAndRhythm:   voice.DictionAndRhythm,
			TypicalMoves:       voice.TypicalMoves,
			ForbiddenMoves:     voice.ForbiddenMoves,
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
	dialogueScenes := make([]draftDialogueScene, 0, len(sim.DialogueBlueprints))
	for _, scene := range sim.DialogueBlueprints {
		dialogueScenes = append(dialogueScenes, draftDialogueScene{
			SceneID:                     scene.SceneID,
			DialogueMode:                scene.DialogueMode,
			ScenePressure:               scene.ScenePressure,
			EmotionalTemperature:        scene.EmotionalTemperature,
			RelationshipFrame:           scene.RelationshipFrame,
			Medium:                      scene.Medium,
			POVRole:                     scene.POVRole,
			Participants:                scene.Participants,
			AudiencePresence:            scene.AudiencePresence,
			InfoAsymmetry:               scene.InfoAsymmetry,
			ValueShift:                  scene.ValueShift,
			OpeningStrategy:             scene.OpeningStrategy,
			LocationAnchor:              scene.LocationAnchor,
			POVState:                    scene.POVState,
			DialogueObjective:           scene.DialogueObjective,
			InterlocutorAgenda:          scene.InterlocutorAgenda,
			ProtagonistResponseStrategy: scene.ProtagonistResponseStrategy,
			DirectnessPolicy:            scene.DirectnessPolicy,
			InfoReleasePolicy:           scene.InfoReleasePolicy,
			ExpositionBudget:            scene.ExpositionBudget,
			ExitBeat:                    scene.ExitBeat,
			DoNotUse:                    scene.DoNotUse,
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
		ForbiddenMoves:         plan.Contract.ForbiddenMoves,
		ContinuityChecks:       plan.Contract.ContinuityChecks,
		PayoffPoints:           plan.Contract.PayoffPoints,
		SceneAnchors:           plan.Contract.SceneAnchors,
		CandidateBeats:         candidateBeats,
		RevealBudget:           sim.ReaderRetentionPlan.RevealBudget,
		CutOrCompress:          sim.ReaderRetentionPlan.CutOrCompress,
		PageTurnQuestions:      sim.ReaderRetentionPlan.PageTurnQuestions,
		ProtagonistProjection:  projection,
		EntertainmentPlan:      sim.EntertainmentPlan,
		TrendLanguagePlan:      sim.TrendLanguage,
		LongformOpening:        sim.LongformOpening,
		AntiAIExecutionPlan:    sim.AntiAIPlan,
		VoiceCards:             voices,
		VisualCards:            visuals,
		DialogueScenes:         dialogueScenes,
		GroundingDetails:       sim.GroundingDetails,
		EnvironmentSignals:     environment,
		EndingContract:         sim.EndingContract,
		SelectionPolicy:        "mandatory_beats 全部落地；candidate_beats 是素材菜单，不是清单。只选能共同完成本章目标的最少节拍，通常 2-4 个；其余直接省略，不得用旁白补交。optional_style_beats 里的热梗、颜文字和指定说法只在声口与语境自然时选用，零使用也允许。一个场景可合并兑现多个 mandatory beat。",
		SceneBridgePolicy:      "每次换场前先让上一场的余波变成主角当下的需要或选择，再进入下一地点；页面至少看得见‘为什么现在去’和‘去了先碰到什么阻力’。仅写锁屏、下楼、到达某地不算因果桥。",
		DialogueTopologyPolicy: "动作拍不是对白轮次的必填项。先用少量具体信息定住人物和空间，进入交锋后允许连续裸对白、打断、漏答、答非所问与无人接话；动作只在改变权力、遮掩信息、打断话头或影响现场结果时出现。",
		SystemVoicePolicy:      "系统先回答主角此刻真正问的事，一次只给一条具体规则或一个可执行提示。陪伴感来自接住具体情绪、吐槽具体处境和共同做选择；禁用‘钱没跑’‘陪你换条路’‘规矩不撤’‘先喘半口气’等没有对象和后果的客服式安慰。",
		JargonPolicy:           "面向无行业经验的普通读者。专业角色可以知道术语，但台词和叙述必须让读者当场看懂会坏在哪里、谁会吃亏、下一步要做什么；‘补测、核验、用途说明、临时固定’等词若不能由可见后果自然解释，就改成日常说法或删除。",
	}
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
	for _, marker := range []string{"热梗", "颜文字", "台词原句", "原样使用", "必须说成", "句式槽位", "那还说啥了，给你了呗"} {
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
	if strings.Contains(text, "^_^") {
		return true
	}
	for _, token := range trendTokens {
		if token != "" && strings.Contains(text, token) {
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
