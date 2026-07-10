package domain

// ChapterPlan 章节写作构思，Writer 自主生成。
// 不再强制场景拆分，Agent 自己决定如何组织内容。
type ChapterPlan struct {
	Chapter          int                     `json:"chapter"`
	Title            string                  `json:"title"`
	Goal             string                  `json:"goal"`
	Conflict         string                  `json:"conflict"`
	Hook             string                  `json:"hook"`
	EmotionArc       string                  `json:"emotion_arc,omitempty"`
	Notes            string                  `json:"notes,omitempty"` // Agent 的自由备忘
	Contract         ChapterContract         `json:"contract,omitempty"`
	CausalSimulation ChapterCausalSimulation `json:"causal_simulation,omitempty"`

	// AdvanceEvents 本章计划推进的事件 id（Task 078 事件编织层；可选）。
	// 与 meta/event_weave.json 编织表冲突时 plan 返回 weave_conflicts 警告，
	// Writer 须在 Notes 里说明改排理由。
	AdvanceEvents []string `json:"advance_events,omitempty"`
}

// ChapterDraftPartIndex 记录分片草稿的可恢复状态。
// 分片只服务写作窗口管理；最终章节仍由 drafts/NN.draft.md -> commit_chapter 提交。
type ChapterDraftPartIndex struct {
	Version   int                `json:"version"`
	Chapter   int                `json:"chapter"`
	UpdatedAt string             `json:"updated_at,omitempty"`
	Parts     []ChapterDraftPart `json:"parts,omitempty"`
}

// ChapterDraftPart 是单个正文片段的索引项。
type ChapterDraftPart struct {
	Part        int    `json:"part"`
	TotalParts  int    `json:"total_parts,omitempty"`
	Title       string `json:"title,omitempty"`
	Focus       string `json:"focus,omitempty"`
	ContentPath string `json:"content_path"`
	RuneCount   int    `json:"rune_count"`
	UpdatedAt   string `json:"updated_at,omitempty"`
}

// ChapterContract 是 Writer 和 Editor 共享的章节验收契约。
// 它定义本章必须完成的推进项、禁止越界项以及审阅关注点。
type ChapterContract struct {
	RequiredBeats    []string `json:"required_beats,omitempty"`    // 本章必须落地的推进项
	ForbiddenMoves   []string `json:"forbidden_moves,omitempty"`   // 本章明确不能发生的推进
	ContinuityChecks []string `json:"continuity_checks,omitempty"` // 本章需特别核对的连续性点
	EvaluationFocus  []string `json:"evaluation_focus,omitempty"`  // Editor 需要重点检查的点
	EmotionTarget    string   `json:"emotion_target,omitempty"`    // 可选：本章希望读者主要感受到的情绪
	PayoffPoints     []string `json:"payoff_points,omitempty"`     // 可选：关键章希望回应的情节点/兑现点
	HookGoal         string   `json:"hook_goal,omitempty"`         // 可选：章末钩子希望驱动的追读欲望
	SceneAnchors     []string `json:"scene_anchors,omitempty"`     // 可选：本章要承载信息/关系/代价的现场物件、痕迹或动作证据
}

// ChapterCausalSimulation 是正文前的角色/世界推演层。
// 它不替代章节契约，而是说明本章事件为什么会由这些人、这些规则自然推出。
type ChapterCausalSimulation struct {
	WorldSimulationID   string                       `json:"world_simulation_id,omitempty"`         // 章前全角色世界模拟的稳定 ID
	ProtagonistDecision string                       `json:"protagonist_decision,omitempty"`        // 从世界模拟投影出的主角选择
	ProjectPromise      string                       `json:"project_promise,omitempty"`             // 本章承接的整本书核心承诺
	ChapterFunction     string                       `json:"chapter_function,omitempty"`            // 本章在全书/卷/弧中的功能
	ContextSources      []string                     `json:"context_sources,omitempty"`             // 本次推演实际使用的上下文来源
	WritingNorms        []WritingNormApplication     `json:"writing_norms_applied,omitempty"`       // 本章写作规范的执行计划
	AntiAIPlan          AntiAIExecutionPlan          `json:"anti_ai_execution_plan,omitempty"`      // AI 味风险的写前阻断计划
	ExternalRefs        []ExternalReferencePlan      `json:"external_reference_plan,omitempty"`     // 网络/RAG/外部资料如何转化为正文细节
	TrendLanguage       []TrendLanguagePlan          `json:"trend_language_plan,omitempty"`         // 热梗/流行语如何受控进入人物声口或生活纹理
	EntertainmentPlan   ReaderEntertainmentPlan      `json:"reader_entertainment_plan,omitempty"`   // 首屏抓力、喜剧节拍、即时兑现和流程压缩
	GroundingDetails    []GroundingDetailPlan        `json:"grounding_details,omitempty"`           // 由外部资料转化出的生活/制度/物件锚点
	OffscreenStage      []CharacterStageRecord       `json:"offscreen_character_stage,omitempty"`   // 本章所有关键角色在正文内外的时间线行动
	LongformOpening     LongformOpeningDesign        `json:"longform_opening,omitempty"`            // 百万字长篇第一章开局设计
	CharacterArcTests   []CharacterArcTest           `json:"character_arc_tests,omitempty"`         // 本章如何测试人物 Want/Lie/Need/Truth 和合理犯错
	ReaderRewardPlan    ReaderRewardPlan             `json:"reader_reward_plan,omitempty"`          // 读者奖励、小胜利、新债务和前几章兑现阶梯
	ReaderRetentionPlan ReaderRetentionPlan          `json:"reader_retention_plan,omitempty"`       // 章节计划的显性/隐性/延后/删压缩筛选，防止把大纲清单全写进正文
	EvidenceChains      []EvidenceReturnChain        `json:"evidence_return_chains,omitempty"`      // 离屏/后台事件如何以证据回到主视角
	EndingContract      EndingConsequenceContract    `json:"ending_consequence_contract,omitempty"` // 章末必须落成的后果契约
	DormantPolicy       []DormantCharacterPolicy     `json:"dormant_character_policy,omitempty"`    // 暂不出场角色的最小推进/静止理由
	RealitySupport      []RealitySupportPlan         `json:"reality_support_plan,omitempty"`        // 现实资料如何支撑生活、职业、交通、交易等细节
	EmotionalLogic      []CharacterEmotionalLogic    `json:"emotional_logic,omitempty"`             // 角色行动背后的身体、情绪、创伤、偏差、意义和元认知
	RelationshipArcs    []RelationshipEmotionArc     `json:"relationship_emotion_arcs,omitempty"`   // 亲情、合作、敌对、恋爱等关系如何由情绪和亲密风险推进
	VisualDesign        []CharacterVisualDesign      `json:"visual_design,omitempty"`               // 人物外貌、发型、服装、轮廓、色彩和状态变化
	CharacterKit        []CharacterKitEntry          `json:"character_kit,omitempty"`               // 角色套件：武器、装备、技能、能力等级；素材来源必须可审计
	WorldLayers         WorldBackgroundLayersPlan    `json:"world_background_layers,omitempty"`     // 本章事件的物理、时间、制度、文化、经济、氛围、宇宙观和叙事层压力
	InformationLedger   []InformationAsymmetryRecord `json:"information_asymmetry,omitempty"`       // 角色/读者/主角分别知道、误以为知道和假装不知道什么
	HiddenRules         []HiddenRulePressure         `json:"hidden_rule_pressure,omitempty"`        // 显规则背后的潜规则、受益者、代价和违反成本
	SocialMoodRumors    []SocialMoodRumor            `json:"social_mood_rumors,omitempty"`          // 社会情绪、流言来源、传播路径、可信度和行动影响
	RitualCalendar      []RitualCalendarWindow       `json:"ritual_calendar,omitempty"`             // 节日、纪念日、仪式、deadline、月相/潮汐等时间窗口
	StructuralResources []StructuralResourcePressure `json:"structural_resources,omitempty"`        // 结构性稀缺资源、控制者、准入方式、黑市/潜规则路径
	CosmologyChecks     []CosmologyRuleCheck         `json:"cosmology_checks,omitempty"`            // 魔法/诡异/科技/因果等元背景规则、代价、边界和例外条件
	ConflictWeb         []ConflictWebNode            `json:"conflict_web,omitempty"`                // 多角色/多势力矛盾网：目标、暗线、资源、信息差、时间压力
	TensionMatrix       NarrativeTensionMatrix       `json:"narrative_tension_matrix,omitempty"`    // 稳定/动荡、显/潜规则、信息差、倒计时和 POV 边界
	InitialState        []CharacterSimulationState   `json:"initial_state,omitempty"`               // 关键角色开章时的欲望、压力、边界
	VoiceLogic          []CharacterVoiceLogic        `json:"voice_logic,omitempty"`                 // 关键角色的说话逻辑和声口自检
	DialogueBlueprints  []DialogueSceneBlueprint     `json:"dialogue_scene_blueprints,omitempty"`   // 关键对白场景的模式、开场策略、角色策略链、潜台词和权力转移设计
	CrowdRoles          []CrowdRoleDesign            `json:"crowd_roles,omitempty"`                 // 捧场/凑数/群体角色的场景功能与退出边界
	ReviewRefinement    ReviewRefinementLoop         `json:"review_refinement,omitempty"`           // 审核失败后的重推演闭环
	EnvironmentState    []EnvironmentSignal          `json:"environment_state,omitempty"`           // 环境如何承载信息、规则压力和状态变化
	WorldRulesInForce   []string                     `json:"world_rules_in_force,omitempty"`        // 本章会实际施压的世界/制度规则
	InformationGaps     []string                     `json:"information_gaps,omitempty"`            // 信息差、误解、隐瞒和未授权内容
	CausalBeats         []CausalSimulationBeat       `json:"causal_beats,omitempty"`                // 行动因果链
	DecisionPoints      []string                     `json:"decision_points,omitempty"`             // 必须落成选择的节点
	OutcomeShift        []string                     `json:"outcome_shift,omitempty"`               // 章末相较开章必须改变的状态
	SceneConstraints    []string                     `json:"scene_constraints,omitempty"`           // 写作限制：视角、证据边界、不能提前解释的内容
}

type WritingNormApplication struct {
	Source             string   `json:"source,omitempty"`
	RuleFocus          []string `json:"rule_focus,omitempty"`
	ChapterApplication string   `json:"chapter_application,omitempty"`
	ProofTargets       []string `json:"proof_targets,omitempty"`
	FailureRisk        string   `json:"failure_risk,omitempty"`
}

type AntiAIExecutionPlan struct {
	RiskSignals          []string `json:"risk_signals,omitempty"`
	CounterMoves         []string `json:"counter_moves,omitempty"`
	SentenceRhythmPolicy string   `json:"sentence_rhythm_policy,omitempty"`
	ObjectResponseBudget string   `json:"object_response_budget,omitempty"`
	DialogueFunctionPlan string   `json:"dialogue_function_plan,omitempty"`
	ReviewChecks         []string `json:"review_checks,omitempty"`
}

type ExternalReferencePlan struct {
	QueryOrNeed          string   `json:"query_or_need,omitempty"`
	SourceType           string   `json:"source_type,omitempty"`
	SourceRefs           []string `json:"source_refs,omitempty"`
	RetrievedAt          string   `json:"retrieved_at,omitempty"`
	FreshnessRequirement string   `json:"freshness_requirement,omitempty"`
	UsableDetails        []string `json:"usable_details,omitempty"`
	TransformationRule   string   `json:"transformation_rule,omitempty"`
	DoNotUse             []string `json:"do_not_use,omitempty"`
}

type TrendLanguagePlan struct {
	Item             string `json:"item,omitempty"`
	SourceContext    string `json:"source_context,omitempty"`
	CharacterCarrier string `json:"character_carrier,omitempty"`
	SceneFunction    string `json:"scene_function,omitempty"`
	UsageBudget      string `json:"usage_budget,omitempty"`
	ForbiddenUsage   string `json:"forbidden_usage,omitempty"`
}

// ReaderEntertainmentPlan 把“抓人、好笑、有爽点”从笼统风格词变成可验收的页面计划。
// 它不替代因果推演，只约束既定事件以什么节奏和人物反应进入主视角正文。
type ReaderEntertainmentPlan struct {
	OpeningBeat          string   `json:"opening_beat,omitempty"`
	HumorBeats           []string `json:"humor_beats,omitempty"`
	ImmediatePayoffs     []string `json:"immediate_payoffs,omitempty"`
	ProcedureCompression string   `json:"procedure_compression,omitempty"`
	CompanionVoiceBeat   string   `json:"companion_voice_beat,omitempty"`
	ForbiddenComedy      []string `json:"forbidden_comedy,omitempty"`
}

type GroundingDetailPlan struct {
	Detail        string `json:"detail,omitempty"`
	SourceRef     string `json:"source_ref,omitempty"`
	TransformedAs string `json:"transformed_as,omitempty"`
	SceneAnchor   string `json:"scene_anchor,omitempty"`
}

type LongformOpeningDesign struct {
	TargetReader      string             `json:"target_reader,omitempty"`
	OpeningHook       string             `json:"opening_hook,omitempty"`
	SerialEngine      string             `json:"serial_engine,omitempty"`
	ReaderRewardLoop  []string           `json:"reader_reward_loop,omitempty"`
	LongRangePromises []LongRangePromise `json:"long_range_promises,omitempty"`
	RevealBudget      []string           `json:"reveal_budget,omitempty"`
	FirstChapterProof []string           `json:"first_chapter_proof,omitempty"`
	RetentionRisks    []string           `json:"retention_risks,omitempty"`
}

type LongRangePromise struct {
	Promise          string `json:"promise,omitempty"`
	FirstChapterSeed string `json:"first_chapter_seed,omitempty"`
	PayoffHorizon    string `json:"payoff_horizon,omitempty"`
}

type CharacterSimulationState struct {
	Character            string                          `json:"character,omitempty"`
	CurrentGoal          string                          `json:"current_goal,omitempty"`
	Pressure             string                          `json:"pressure,omitempty"`
	Resources            []string                        `json:"resources,omitempty"`
	RelationshipForces   []string                        `json:"relationship_forces,omitempty"`
	Secrets              []string                        `json:"secrets,omitempty"`
	Misbeliefs           []string                        `json:"misbeliefs,omitempty"`
	PrivateBoundary      string                          `json:"private_boundary,omitempty"`
	ActionTendency       string                          `json:"action_tendency,omitempty"`
	LikelyAction         string                          `json:"likely_action,omitempty"`
	StateDeltaToTrack    []string                        `json:"state_delta_to_track,omitempty"`
	CompetenceStage      string                          `json:"competence_stage,omitempty"`
	SkillLimits          []string                        `json:"skill_limits,omitempty"`
	PlausibleMistakes    []string                        `json:"plausible_mistakes,omitempty"`
	CorrectionTriggers   []string                        `json:"correction_triggers,omitempty"`
	KnowledgeLedger      CharacterKnowledgeLedger        `json:"knowledge_ledger"`
	DecisionFrame        CharacterDecisionFrame          `json:"decision_frame"`
	RelationshipContract []CharacterRelationshipContract `json:"relationship_contract"`
	EmotionAppraisal     CharacterEmotionAppraisal       `json:"emotion_appraisal"`
	ArcAxis              CharacterArcAxis                `json:"arc_axis"`
}

type CharacterVoiceLogic struct {
	Character          string   `json:"character,omitempty"`
	PersonalitySource  string   `json:"personality_source,omitempty"`
	SpeechPrinciple    string   `json:"speech_principle,omitempty"`
	SceneObjective     string   `json:"scene_objective,omitempty"`
	HiddenSubtext      string   `json:"hidden_subtext,omitempty"`
	KnowledgeBoundary  string   `json:"knowledge_boundary,omitempty"`
	RelationshipStance string   `json:"relationship_stance,omitempty"`
	DictionAndRhythm   string   `json:"diction_and_rhythm,omitempty"`
	SentenceLength     string   `json:"sentence_length,omitempty"`
	PunctuationStyle   string   `json:"punctuation_style,omitempty"`
	LineBreakStyle     string   `json:"line_break_style,omitempty"`
	SubtextStrategy    string   `json:"subtext_strategy,omitempty"`
	SilenceOrAction    string   `json:"silence_or_action_beat,omitempty"`
	VoiceContrast      string   `json:"voice_contrast,omitempty"`
	ActionBeatPolicy   string   `json:"action_beat_policy,omitempty"`
	DialogueFunctions  []string `json:"dialogue_functions,omitempty"`
	TypicalMoves       []string `json:"typical_moves,omitempty"`
	ForbiddenMoves     []string `json:"forbidden_moves,omitempty"`
	DialogueTest       []string `json:"dialogue_test,omitempty"`
}

type DialogueSceneBlueprint struct {
	SceneID                     string                    `json:"scene_id,omitempty"`
	DialogueMode                string                    `json:"dialogue_mode,omitempty"`
	ModeReason                  string                    `json:"mode_reason,omitempty"`
	ScenePressure               string                    `json:"scene_pressure,omitempty"`
	EmotionalTemperature        string                    `json:"emotional_temperature,omitempty"`
	RelationshipFrame           string                    `json:"relationship_frame,omitempty"`
	Medium                      string                    `json:"medium,omitempty"`          // 对话媒介：face_to_face、phone、text_message、letter、through_door、intermediary 等；非面对面时动作拍改为媒介拍
	POVRole                     string                    `json:"pov_role,omitempty"`        // POV 在本场的位置：participant、eavesdropper、bystander；偷听/旁观场必填
	Participants                []string                  `json:"participants,omitempty"`    // 三人以上对话时列出全部说话方或派系
	AudiencePresence            DialogueAudiencePresence  `json:"audience_presence"`         // 第三方观众效应：有人围观时双方各自演给谁看
	InfoAsymmetry               DialogueInfoAsymmetry     `json:"information_asymmetry"`     // 甲/乙/读者三方信息差与本场如何利用
	ValueShift                  DialogueValueShift        `json:"value_shift"`               // 本场必须翻转的价值极性；没有翻转的对话场应删除
	PowerTrajectory             DialoguePowerTrajectory   `json:"power_trajectory"`          // 权力走向：开场谁占上风、何时易手、收场谁占上风
	AddressShift                string                    `json:"address_shift,omitempty"`   // 称谓漂移设计：敬称/直呼/去称谓随压力变化，本身是一条潜台词线
	CoalitionShift              string                    `json:"coalition_shift,omitempty"` // 多人议事场必填：派系联盟在哪一回合、因何翻转
	OpeningStrategy             string                    `json:"opening_strategy,omitempty"`
	FirstSpokenMoment           string                    `json:"first_spoken_moment,omitempty"`
	EntryLine                   string                    `json:"entry_line,omitempty"`
	EntrySpeaker                string                    `json:"entry_speaker,omitempty"`
	LocationAnchor              string                    `json:"location_anchor,omitempty"`
	POVState                    string                    `json:"pov_state,omitempty"`
	InnerQuestion               string                    `json:"inner_question,omitempty"`
	MemoryBridge                string                    `json:"memory_bridge,omitempty"`
	IdentityGrounding           string                    `json:"identity_grounding,omitempty"`
	DialogueObjective           string                    `json:"dialogue_objective,omitempty"`
	InterlocutorAgenda          string                    `json:"interlocutor_agenda,omitempty"`
	ProtagonistResponseStrategy string                    `json:"protagonist_response_strategy,omitempty"`
	ObjectiveTactics            []DialogueObjectiveTactic `json:"objective_tactics,omitempty"`
	TurnProgression             []DialogueTurnDesign      `json:"turn_progression,omitempty"`
	DirectnessPolicy            string                    `json:"directness_policy,omitempty"`
	SubtextSource               string                    `json:"subtext_source,omitempty"`
	EscalationPattern           string                    `json:"escalation_pattern,omitempty"`
	BeatDensity                 string                    `json:"beat_density,omitempty"`
	SilencePolicy               string                    `json:"silence_policy,omitempty"`
	InfoReleasePolicy           string                    `json:"info_release_policy,omitempty"`
	ExpositionBudget            string                    `json:"exposition_budget,omitempty"`
	SubtextAndPowerShift        string                    `json:"subtext_and_power_shift,omitempty"`
	ExitBeat                    string                    `json:"exit_beat,omitempty"`
	DoNotUse                    []string                  `json:"do_not_use,omitempty"`
}

type DialogueObjectiveTactic struct {
	Character          string `json:"character,omitempty"`
	Faction            string `json:"faction,omitempty"` // 多人议事场：此角色所属派系或临时联盟
	ImmediateObjective string `json:"immediate_objective,omitempty"`
	Tactic             string `json:"tactic,omitempty"`
	CounterTactic      string `json:"counter_tactic,omitempty"`
	EmotionalLeak      string `json:"emotional_leak,omitempty"`
	TurnResult         string `json:"turn_result,omitempty"`
}

// DialogueAudiencePresence 描述第三方观众效应：一旦有人围观，双方说的话有一半是演给旁人看的。
type DialogueAudiencePresence struct {
	Present        string `json:"present,omitempty"`         // none 或具体在场第三方：围观宾客、下属、孩子、直播观众等
	PerformanceFor string `json:"performance_for,omitempty"` // 双方各自演给谁看、想在观众面前保住或摧毁什么
	AudienceEffect string `json:"audience_effect,omitempty"` // 观众反应如何反过来改变对话走向：起哄、沉默、倒戈、记录在案
}

// DialogueInfoAsymmetry 描述甲/乙/读者三方信息差；读者比 POV 知道得多是戏剧反讽，知道得少是悬念。
type DialogueInfoAsymmetry struct {
	POVKnows       string `json:"pov_knows,omitempty"`       // POV 此刻掌握、可以打出去的信息
	POVLacks       string `json:"pov_lacks,omitempty"`       // POV 缺失并因此会误读的信息
	OtherHolds     string `json:"other_holds,omitempty"`     // 对手掌握而 POV 不知道的底牌
	ReaderPosition string `json:"reader_position,omitempty"` // reader_ahead(戏剧反讽)、reader_level(同步)、reader_behind(悬念)
	AsymmetryPlay  string `json:"asymmetry_play,omitempty"`  // 信息差在本场如何被利用、暴露或加深
}

// DialogueValueShift 是 McKee 场景律令：每一场戏必须翻转一个价值的极性，否则这场对话应该删除。
type DialogueValueShift struct {
	Value         string `json:"value,omitempty"`          // 被押上的价值：信任、安全、希望、亲密、控制权、名誉等
	OpeningCharge string `json:"opening_charge,omitempty"` // 开场极性(正/负)加一句现场证据
	TurnTrigger   string `json:"turn_trigger,omitempty"`   // 触发翻转的具体台词、动作或信息
	ClosingCharge string `json:"closing_charge,omitempty"` // 收场极性；必须与开场不同
}

// DialoguePowerTrajectory 结构化权力走向；好的对话戏权力至少易手一次。
type DialoguePowerTrajectory struct {
	OpeningHolder string `json:"opening_holder,omitempty"` // 开场谁占上风，凭什么：信息、地位、时间、情感筹码
	FlipBeat      string `json:"flip_beat,omitempty"`      // 权力第一次易手发生在哪一回合、由什么触发
	ClosingHolder string `json:"closing_holder,omitempty"` // 收场谁占上风；允许翻回，但必须经过易手
}

type DialogueTurnDesign struct {
	Speaker             string `json:"speaker,omitempty"`
	SurfaceLineFunction string `json:"surface_line_function,omitempty"`
	HiddenSubtext       string `json:"hidden_subtext,omitempty"`
	NewInformation      string `json:"new_information,omitempty"`
	PowerMove           string `json:"power_move,omitempty"`
	ActionBeat          string `json:"action_beat,omitempty"`
	NextPressure        string `json:"next_pressure,omitempty"`
}

type CharacterArcTest struct {
	Character        string `json:"character,omitempty"`
	Want             string `json:"want,omitempty"`
	CoreLie          string `json:"core_lie,omitempty"`
	Need             string `json:"need,omitempty"`
	Truth            string `json:"truth,omitempty"`
	PressureTest     string `json:"pressure_test,omitempty"`
	FirstMistake     string `json:"first_mistake,omitempty"`
	CorrectionSignal string `json:"correction_signal,omitempty"`
	ChapterEvidence  string `json:"chapter_evidence,omitempty"`
}

type ReaderRewardPlan struct {
	ChapterWindow           string             `json:"chapter_window,omitempty"`
	FirstChapterSmallWin    string             `json:"first_chapter_small_win,omitempty"`
	NewDebtOrCost           string             `json:"new_debt_or_cost,omitempty"`
	PayoffVisibility        string             `json:"payoff_visibility,omitempty"`
	TrafficRisk             string             `json:"traffic_risk,omitempty"`
	RewardLadder            []ReaderRewardStep `json:"reward_ladder,omitempty"`
	ForbiddenRewardPatterns []string           `json:"forbidden_reward_patterns,omitempty"`
}

// ReaderRetentionPlan 把全量章节计划筛成读者会在页面上感到有效的内容。
// 计划是素材池和边界，不是正文清单；未进入 SurfaceBeats 的内容默认不显性展开。
type ReaderRetentionPlan struct {
	SurfaceBeats      []RetentionSurfaceBeat `json:"surface_beats,omitempty"`
	LatentContext     []string               `json:"latent_context,omitempty"`
	RevealBudget      []string               `json:"reveal_budget,omitempty"`
	CutOrCompress     []string               `json:"cut_or_compress,omitempty"`
	PageTurnQuestions []string               `json:"page_turn_questions,omitempty"`
}

type RetentionSurfaceBeat struct {
	PlanSource    string `json:"plan_source,omitempty"`
	MustShow      string `json:"must_show,omitempty"`
	ReaderPayoff  string `json:"reader_payoff,omitempty"`
	SceneVehicle  string `json:"scene_vehicle,omitempty"`
	ProofOnPage   string `json:"proof_on_page,omitempty"`
	FunctionShift string `json:"function_shift,omitempty"`
}

type ReaderRewardStep struct {
	Chapter int    `json:"chapter,omitempty"`
	Reward  string `json:"reward,omitempty"`
	Cost    string `json:"cost,omitempty"`
	Hook    string `json:"hook,omitempty"`
}

type EvidenceReturnChain struct {
	OffscreenCharacter  string `json:"offscreen_character,omitempty"`
	Event               string `json:"event,omitempty"`
	Evidence            string `json:"evidence,omitempty"`
	ProtagonistAccess   string `json:"protagonist_access,omitempty"`
	ReturnTiming        string `json:"return_timing,omitempty"`
	DistortionOrMisread string `json:"distortion_or_misread,omitempty"`
	ChapterToResolve    int    `json:"chapter_to_resolve,omitempty"`
}

type EndingConsequenceContract struct {
	EndingMode       string   `json:"ending_mode,omitempty"`
	ConcreteAnchor   string   `json:"concrete_anchor,omitempty"`
	Consequence      string   `json:"consequence,omitempty"`
	NextChapterPull  string   `json:"next_chapter_pull,omitempty"`
	WhyNotUI         string   `json:"why_not_ui,omitempty"`
	ForbiddenEndings []string `json:"forbidden_endings,omitempty"`
}

type DormantCharacterPolicy struct {
	Character         string `json:"character,omitempty"`
	Status            string `json:"status,omitempty"`
	Location          string `json:"location,omitempty"`
	NoChangeReason    string `json:"no_change_reason,omitempty"`
	TriggerCondition  string `json:"trigger_condition,omitempty"`
	KnowledgeBoundary string `json:"knowledge_boundary,omitempty"`
	NextCheck         string `json:"next_check,omitempty"`
}

type RealitySupportPlan struct {
	Domain             string   `json:"domain,omitempty"`
	SourceRef          string   `json:"source_ref,omitempty"`
	UsableDetail       string   `json:"usable_detail,omitempty"`
	TransformedAs      string   `json:"transformed_as,omitempty"`
	ChapterUse         string   `json:"chapter_use,omitempty"`
	ForbiddenDirectUse []string `json:"forbidden_direct_use,omitempty"`
}

type CharacterEmotionalLogic struct {
	Character               string   `json:"character,omitempty"`
	PhysiologicalState      string   `json:"physiological_state,omitempty"`
	ImmediateState          string   `json:"immediate_state,omitempty"`
	BaselineMood            string   `json:"baseline_mood,omitempty"`
	PrimaryEmotion          string   `json:"primary_emotion,omitempty"`
	CompositeEmotion        string   `json:"composite_emotion,omitempty"`
	EmotionalTrigger        string   `json:"emotional_trigger,omitempty"`
	GoalAppraisal           string   `json:"goal_appraisal,omitempty"`
	BoundaryThreat          string   `json:"boundary_threat,omitempty"`
	RegulationStrategy      string   `json:"regulation_strategy,omitempty"`
	DefenseMechanism        string   `json:"defense_mechanism,omitempty"`
	CognitiveBias           string   `json:"cognitive_bias,omitempty"`
	ApproachAvoidance       string   `json:"approach_avoidance,omitempty"`
	ShortLongTermTension    string   `json:"short_long_term_tension,omitempty"`
	SelfRelationshipTension string   `json:"self_relationship_tension,omitempty"`
	ConsciousReason         string   `json:"conscious_reason,omitempty"`
	HiddenReason            string   `json:"hidden_reason,omitempty"`
	MeaningNeed             string   `json:"meaning_need,omitempty"`
	Metacognition           string   `json:"metacognition,omitempty"`
	EmotionLedAction        string   `json:"emotion_led_action,omitempty"`
	EventCompletionRole     string   `json:"event_completion_role,omitempty"`
	EvidenceInScene         []string `json:"evidence_in_scene,omitempty"`
}

type RelationshipEmotionArc struct {
	Pair                         []string `json:"pair,omitempty"`
	RelationshipType             string   `json:"relationship_type,omitempty"`
	CurrentBond                  string   `json:"current_bond,omitempty"`
	EmotionalWant                string   `json:"emotional_want,omitempty"`
	Fear                         string   `json:"fear,omitempty"`
	PowerBalance                 string   `json:"power_balance,omitempty"`
	IntimacyStage                string   `json:"intimacy_stage,omitempty"`
	TrustDebt                    string   `json:"trust_debt,omitempty"`
	ConflictTrigger              string   `json:"conflict_trigger,omitempty"`
	AttachmentOrLoveLanguage     string   `json:"attachment_or_love_language,omitempty"`
	Boundary                     string   `json:"boundary,omitempty"`
	RomancePotential             string   `json:"romance_potential,omitempty"`
	NextEmotionalBeat            string   `json:"next_emotional_beat,omitempty"`
	ProtagonistKnowledgeBoundary string   `json:"protagonist_knowledge_boundary,omitempty"`
}

type CharacterVisualDesign struct {
	Character       string   `json:"character,omitempty"`
	Silhouette      string   `json:"silhouette,omitempty"`
	FaceAndHair     string   `json:"face_and_hair,omitempty"`
	ClothingStyle   string   `json:"clothing_style,omitempty"`
	ColorPalette    string   `json:"color_palette,omitempty"`
	BodyLanguage    string   `json:"body_language,omitempty"`
	SignatureObject string   `json:"signature_object,omitempty"`
	FirstImpression string   `json:"first_impression,omitempty"`
	StatusWear      string   `json:"status_wear,omitempty"`
	ChangeRule      string   `json:"change_rule,omitempty"`
	SceneUse        string   `json:"scene_use,omitempty"`
	DoNotUse        []string `json:"do_not_use,omitempty"`
	// MaterialSource 素材来源声明：craft_recall 命中的 source_path、book_facts（沿用本书
	// 已实例化事实）或 no_material（检索无料、自行设计）。缺料必须可见，不允许静默编造。
	MaterialSource string `json:"material_source,omitempty"`
}

// CharacterKitItem 角色套件里的一件武器/装备/技能。
type CharacterKitItem struct {
	Name           string `json:"name,omitempty"`
	Category       string `json:"category,omitempty"`        // 类别：长剑/符箓/义体/账本等
	Description    string `json:"description,omitempty"`     // 形制、来历、可见特征
	MaterialSource string `json:"material_source,omitempty"` // craft 素材路径 / book_facts / no_material
	Evidence       string `json:"evidence,omitempty"`        // 首次出场章节或台账证据
}

// CharacterKitAbility 角色能力条目：必须对齐 world_codex 分级，不得越过当前卷上限。
type CharacterKitAbility struct {
	Name           string `json:"name,omitempty"`
	CodexTier      string `json:"codex_tier,omitempty"`      // 对应 world_codex.ability_tiers 的分级名
	CurrentLevel   string `json:"current_level,omitempty"`   // 当前等级/熟练度
	UsageScope     string `json:"usage_scope,omitempty"`     // 本章允许使用范围
	Cost           string `json:"cost,omitempty"`            // 使用代价
	UpgradeTrigger string `json:"upgrade_trigger,omitempty"` // 升级触发条件
	MaterialSource string `json:"material_source,omitempty"`
	Evidence       string `json:"evidence,omitempty"`
}

// CharacterKitEntry 一个角色的设计套件。首次出场角色必须先经 craft_recall 取料
// （或显式 no_material），实例化进本条目；后续章节从本书事实层召回，不再查手法库。
type CharacterKitEntry struct {
	Character       string                `json:"character,omitempty"`
	FirstAppearance bool                  `json:"first_appearance,omitempty"`
	AppearanceRef   string                `json:"appearance_ref,omitempty"` // 对应 visual_design 条目或 dossier
	Weapons         []CharacterKitItem    `json:"weapons,omitempty"`
	Equipment       []CharacterKitItem    `json:"equipment,omitempty"`
	Skills          []CharacterKitItem    `json:"skills,omitempty"`
	Abilities       []CharacterKitAbility `json:"abilities,omitempty"`
	CodexCompliance string                `json:"codex_compliance,omitempty"` // 声明未越过 world_codex/卷上限及依据
}

type WorldBackgroundLayersPlan struct {
	PhysicalSpace       string `json:"physical_space,omitempty"`
	TimeLayer           string `json:"time_layer,omitempty"`
	SocialInstitution   string `json:"social_institution,omitempty"`
	CulturalNorm        string `json:"cultural_norm,omitempty"`
	RelationshipNetwork string `json:"relationship_network,omitempty"`
	EconomicResource    string `json:"economic_resource,omitempty"`
	ConflictTension     string `json:"conflict_tension,omitempty"`
	SocialMood          string `json:"social_mood,omitempty"`
	CosmologyMetaRule   string `json:"cosmology_meta_rule,omitempty"`
	NarrativeMeta       string `json:"narrative_meta,omitempty"`
	EventActivation     string `json:"event_activation,omitempty"`
}

type InformationAsymmetryRecord struct {
	Subject           string   `json:"subject,omitempty"`
	ReaderKnows       []string `json:"reader_knows,omitempty"`
	ProtagonistKnows  []string `json:"protagonist_knows,omitempty"`
	CharacterKnows    []string `json:"character_knows,omitempty"`
	CharacterMistakes []string `json:"character_mistakes,omitempty"`
	CharacterPretends []string `json:"character_pretends,omitempty"`
	HiddenFromReader  []string `json:"hidden_from_reader,omitempty"`
	RevealCondition   string   `json:"reveal_condition,omitempty"`
	TensionFunction   string   `json:"tension_function,omitempty"`
}

type HiddenRulePressure struct {
	Domain        string `json:"domain,omitempty"`
	VisibleRule   string `json:"visible_rule,omitempty"`
	HiddenRule    string `json:"hidden_rule,omitempty"`
	CulturalNorm  string `json:"cultural_norm,omitempty"`
	WhoBenefits   string `json:"who_benefits,omitempty"`
	WhoPays       string `json:"who_pays,omitempty"`
	ViolationCost string `json:"violation_cost,omitempty"`
	SceneEvidence string `json:"scene_evidence,omitempty"`
}

type SocialMoodRumor struct {
	Group             string `json:"group,omitempty"`
	Mood              string `json:"mood,omitempty"`
	Rumor             string `json:"rumor,omitempty"`
	Source            string `json:"source,omitempty"`
	SpreadPath        string `json:"spread_path,omitempty"`
	Reliability       string `json:"reliability,omitempty"`
	BehaviorEffect    string `json:"behavior_effect,omitempty"`
	ProtagonistAccess string `json:"protagonist_access,omitempty"`
}

type RitualCalendarWindow struct {
	Time                string `json:"time,omitempty"`
	CalendarType        string `json:"calendar_type,omitempty"`
	RitualOrDeadline    string `json:"ritual_or_deadline,omitempty"`
	SocialMeaning       string `json:"social_meaning,omitempty"`
	PracticalConstraint string `json:"practical_constraint,omitempty"`
	EmotionalCharge     string `json:"emotional_charge,omitempty"`
	MissedCost          string `json:"missed_cost,omitempty"`
	SceneUse            string `json:"scene_use,omitempty"`
}

type StructuralResourcePressure struct {
	Resource                  string `json:"resource,omitempty"`
	Controller                string `json:"controller,omitempty"`
	ScarcityReason            string `json:"scarcity_reason,omitempty"`
	AccessRule                string `json:"access_rule,omitempty"`
	BlackMarketOrInformalPath string `json:"black_market_or_informal_path,omitempty"`
	PriceOrCost               string `json:"price_or_cost,omitempty"`
	PowerEffect               string `json:"power_effect,omitempty"`
	ChapterPressure           string `json:"chapter_pressure,omitempty"`
}

type CosmologyRuleCheck struct {
	Layer              string `json:"layer,omitempty"`
	Rule               string `json:"rule,omitempty"`
	Cost               string `json:"cost,omitempty"`
	Boundary           string `json:"boundary,omitempty"`
	ExceptionCondition string `json:"exception_condition,omitempty"`
	Evidence           string `json:"evidence,omitempty"`
	FailureMode        string `json:"failure_mode,omitempty"`
}

type ConflictWebNode struct {
	Parties        []string `json:"parties,omitempty"`
	ConflictType   string   `json:"conflict_type,omitempty"`
	OpenGoal       string   `json:"open_goal,omitempty"`
	HiddenAgenda   string   `json:"hidden_agenda,omitempty"`
	ResourceStake  string   `json:"resource_stake,omitempty"`
	InformationGap string   `json:"information_gap,omitempty"`
	TimePressure   string   `json:"time_pressure,omitempty"`
	CurrentBalance string   `json:"current_balance,omitempty"`
	Destabilizer   string   `json:"destabilizer,omitempty"`
	NextEscalation string   `json:"next_escalation,omitempty"`
}

type NarrativeTensionMatrix struct {
	StabilityTurbulence     string `json:"stability_turbulence,omitempty"`
	ExplicitHiddenRules     string `json:"explicit_hidden_rules,omitempty"`
	InformationGap          string `json:"information_gap,omitempty"`
	TimePressurePreparation string `json:"time_pressure_preparation,omitempty"`
	WhyEventNow             string `json:"why_event_now,omitempty"`
	ReaderQuestion          string `json:"reader_question,omitempty"`
	POVBoundary             string `json:"pov_boundary,omitempty"`
}

type CrowdRoleDesign struct {
	GroupName        string `json:"group_name,omitempty"`
	Count            int    `json:"count,omitempty"`
	SceneFunction    string `json:"scene_function,omitempty"`
	ReactionPolicy   string `json:"reaction_policy,omitempty"`
	VoiceBudget      string `json:"voice_budget,omitempty"`
	NamingPolicy     string `json:"naming_policy,omitempty"`
	ContinuityPolicy string `json:"continuity_policy,omitempty"`
	ExitCondition    string `json:"exit_condition,omitempty"`
}

type ReviewRefinementLoop struct {
	TriggerSources      []string `json:"trigger_sources,omitempty"`
	FailureModes        []string `json:"failure_modes,omitempty"`
	LocalizedTargets    []string `json:"localized_targets,omitempty"`
	PreserveConstraints []string `json:"preserve_constraints,omitempty"`
	ReplanningMoves     []string `json:"replanning_moves,omitempty"`
	AcceptanceChecks    []string `json:"acceptance_checks,omitempty"`
	StopCondition       string   `json:"stop_condition,omitempty"`
	IterationLimit      int      `json:"iteration_limit,omitempty"`
}

type EnvironmentSignal struct {
	Place              string `json:"place,omitempty"`
	VisibleState       string `json:"visible_state,omitempty"`
	InformationCarried string `json:"information_carried,omitempty"`
	PressureApplied    string `json:"pressure_applied,omitempty"`
	ExpectedChange     string `json:"expected_change,omitempty"`
}

type CausalSimulationBeat struct {
	Cause           string `json:"cause,omitempty"`
	CharacterChoice string `json:"character_choice,omitempty"`
	WorldResponse   string `json:"world_response,omitempty"`
	StoryResult     string `json:"story_result,omitempty"`
}

// ChapterSummary 章节摘要，供后续章节的上下文窗口使用。
type ChapterSummary struct {
	Chapter       int      `json:"chapter"`
	Summary       string   `json:"summary"`
	Characters    []string `json:"characters"`
	KeyEvents     []string `json:"key_events"`
	OpeningDevice string   `json:"opening_device,omitempty"`
	EndingDevice  string   `json:"ending_device,omitempty"`
}

// ArcSummary 弧级摘要，弧结束时由 Editor 生成。
type ArcSummary struct {
	Volume    int      `json:"volume"`
	Arc       int      `json:"arc"`
	Title     string   `json:"title"`
	Summary   string   `json:"summary"`
	KeyEvents []string `json:"key_events"`
}

// VolumeSummary 卷级摘要，卷结束时生成。
type VolumeSummary struct {
	Volume    int      `json:"volume"`
	Title     string   `json:"title"`
	Summary   string   `json:"summary"`
	KeyEvents []string `json:"key_events"`
}

// CharacterSnapshot 角色状态快照，弧边界时记录。
type CharacterSnapshot struct {
	Volume     int    `json:"volume"`
	Arc        int    `json:"arc"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	Power      string `json:"power,omitempty"`
	Motivation string `json:"motivation"`
	Relations  string `json:"relations,omitempty"`
}

// OutlineFeedback Writer 对大纲的反馈，提交章节时可选。
type OutlineFeedback struct {
	Deviation  string `json:"deviation"`  // 偏离描述
	Suggestion string `json:"suggestion"` // 调整建议
}

// WritingStyleRules 从已写章节中提炼的写作规则，弧边界时由 Editor 生成。
// 取代原文片段（style_anchors / voice_samples），用规则替代搬运原文。
type WritingStyleRules struct {
	Volume    int              `json:"volume"`
	Arc       int              `json:"arc"`
	Prose     []string         `json:"prose"`      // 3-5 条叙述风格规则，每条 ≤50 字
	Dialogue  []CharacterVoice `json:"dialogue"`   // 角色对话风格规则
	Taboos    []string         `json:"taboos"`     // 禁忌清单
	UpdatedAt string           `json:"updated_at"` // ISO8601 时间戳
}

// CharacterVoice 单个角色的对话风格规则。
type CharacterVoice struct {
	Name  string   `json:"name"`
	Rules []string `json:"rules"` // 2-3 条语言特征规则，每条 ≤30 字
}

// RelatedChapter 推荐回读的相关章节。
type RelatedChapter struct {
	Chapter int    `json:"chapter"`
	Reason  string `json:"reason"`
}

// RecallItem 是按当前任务选择性召回的长期信息。
// 它不替代正式工件，只负责把当前轮真正相关的少量历史信息回注给模型。
type RecallItem struct {
	Kind    string `json:"kind"`
	Key     string `json:"key,omitempty"`
	Chapter int    `json:"chapter,omitempty"`
	Reason  string `json:"reason,omitempty"`
	Summary string `json:"summary,omitempty"`
}

// CommitResult 是 commit_chapter 工具的结构化返回值。
// 只包含事实字段；"下一步做什么"由 Reminder 通道基于当前 Progress 自行生成。
type CommitResult struct {
	Chapter        int              `json:"chapter"`
	Committed      bool             `json:"committed"`
	WordCount      int              `json:"word_count"`
	NextChapter    int              `json:"next_chapter"`
	ReviewRequired bool             `json:"review_required"`
	ReviewReason   string           `json:"review_reason,omitempty"`
	HookType       string           `json:"hook_type,omitempty"`
	DominantStrand string           `json:"dominant_strand,omitempty"`
	OpeningDevice  string           `json:"opening_device,omitempty"`
	EndingDevice   string           `json:"ending_device,omitempty"`
	Feedback       *OutlineFeedback `json:"feedback,omitempty"`
	// 长篇分层信号
	ArcEnd         bool `json:"arc_end,omitempty"`
	VolumeEnd      bool `json:"volume_end,omitempty"`
	Volume         int  `json:"volume,omitempty"`
	Arc            int  `json:"arc,omitempty"`
	NeedsExpansion bool `json:"needs_expansion,omitempty"`  // 下一弧是骨架，需要展开章节
	NeedsNewVolume bool `json:"needs_new_volume,omitempty"` // 需要 Architect 创建下一卷
	NextVolume     int  `json:"next_volume,omitempty"`      // 下一弧/卷序号
	NextArc        int  `json:"next_arc,omitempty"`         // 下一弧序号
	// 完成态事实：本次 commit 后是否整本书已完成
	BookComplete bool `json:"book_complete,omitempty"`
	// 当前 Progress.Flow 快照（writing / reviewing / rewriting / polishing）
	Flow string `json:"flow,omitempty"`
	// 滚动大纲策略：进入某卷第一章时敲定下两卷动态大纲；每章 commit 后复查。
	VolumeOutlineDue    string `json:"volume_outline_due,omitempty"`    // 本章是某卷首章：必须派 architect 敲定下两卷
	VolumeOutlineReview string `json:"volume_outline_review,omitempty"` // 每章例行：核对本章 feedback 是否要求修订下两卷
}
