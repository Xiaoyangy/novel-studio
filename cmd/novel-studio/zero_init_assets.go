package main

import (
	"fmt"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

func zeroCharacterNameIs(c domain.Character, names ...string) bool {
	name := strings.TrimSpace(c.Name)
	for _, want := range names {
		want = strings.TrimSpace(want)
		if want == "" {
			continue
		}
		if name == want {
			return true
		}
		for _, alias := range c.Aliases {
			if strings.TrimSpace(alias) == want {
				return true
			}
		}
	}
	return false
}

func zeroAssetOpeningPressureName(project zeroInitProject) string {
	if title := strings.TrimSpace(project.FirstChapter.Title); title != "" {
		return title + "中的现实压力"
	}
	return "第一章目标、资源与执行压力"
}

func zeroFirstActiveNonProtagonistName(project zeroInitProject) string {
	for _, c := range project.Characters {
		if !zeroIsPrimaryProtagonist(project, c) && zeroFirstChapterCharacterActive(project, c) {
			if name := strings.TrimSpace(c.Name); name != "" {
				return name
			}
		}
	}
	return ""
}

func zeroFirstChapterCharacterActive(project zeroInitProject, c domain.Character) bool {
	if zeroIsPrimaryProtagonist(project, c) {
		return true
	}
	return project.FirstCast[strings.TrimSpace(c.Name)]
}

func zeroIsPrimaryProtagonist(project zeroInitProject, c domain.Character) bool {
	primary := zeroProtagonist(project.Characters)
	return strings.TrimSpace(primary.Name) != "" && strings.TrimSpace(primary.Name) == strings.TrimSpace(c.Name)
}

func zeroInitDynamics(project zeroInitProject) zeroInitCharacterDynamicsDoc {
	chars := zeroInitialCharacters(project)
	var states []domain.CharacterSimulationState
	var voices []domain.CharacterVoiceLogic
	for _, c := range chars {
		states = append(states, zeroInitCharacterState(project, c))
		voices = append(voices, zeroInitVoiceLogic(project, c))
	}
	return zeroInitCharacterDynamicsDoc{
		Version:        1,
		Scope:          "zero_chapter",
		Chapter:        1,
		GeneratedAt:    project.GeneratedAt,
		RequiredFields: zeroRequiredDynamicFields(),
		Characters:     states,
		VoiceLogic:     voices,
	}
}

func zeroInitCharacterState(project zeroInitProject, c domain.Character) domain.CharacterSimulationState {
	role := zeroFirstNonEmpty(c.Role, "关键角色")
	arc := zeroOpeningArcBaseline(c)
	actionBias := zeroOpeningActionBias(c)
	firstChapterActive := zeroFirstChapterCharacterActive(project, c)
	// 主角是关系枢纽，应对每个关键配角都有契约；非主角对主角有契约。
	// 旧逻辑只给主角找单个 FirstCast 对手，第一章大纲没点名匹配时主角契约为空。
	counterparts := zeroCurrentCounterparts(project, c)
	relationshipForces := []string{"当前章的主要牵引来自现场规则、资源压力和可见证据。"}
	relationshipContracts := []domain.CharacterRelationshipContract{}
	if len(counterparts) > 0 {
		relationshipForces = relationshipForces[:0]
		for _, cp := range counterparts {
			force := fmt.Sprintf("与%s的信任、债务、亏欠、边界或信息差必须在行动中体现。", cp)
			debt := "无新增债务、亏欠或承诺；第一章若发生帮助/交换必须入账并留下机会成本。"
			relationshipForces = append(relationshipForces, force)
			relationshipContracts = append(relationshipContracts, domain.CharacterRelationshipContract{
				Counterpart:       cp,
				Trust:             "零章基线：未因正文事件新增信任。",
				Debt:              debt,
				Leverage:          "信息差和现场资源是主要筹码。",
				Promise:           "不默认承诺长期协作。",
				SharedSecret:      "无正文确认的共同秘密。",
				BetrayalRecord:    "无正文确认的背叛记录。",
				Dependency:        "第一章仅允许低强度依赖，不能瞬间绝对信任。",
				FearSource:        "害怕失去目标、资源、身份或关系边界。",
				AllianceStatus:    "未结盟/试探期。",
				BetrayalThreshold: "对方要求无证据承诺、隐瞒关键代价或夺走核心资源。",
				HelpCondition:     "必须有可见证据、明确交换或情感压力触发。",
				SourceChapter:     0,
			})
		}
	}
	if !firstChapterActive {
		relationshipForces = []string{"离屏阶段只承受既有关系、日程和资源牵引；未获联系或见面条件时不得跨场景介入第一章。"}
		for i := range relationshipContracts {
			relationshipContracts[i].Debt = "零章不新增债务、亏欠或承诺；首次入场后的实际帮助或交换再入账。"
			relationshipContracts[i].Dependency = "离屏阶段不新增依赖；首次入场前不得凭空建立现场协作。"
			relationshipContracts[i].HelpCondition = "必须等首次联系或入场条件成立，并有可见证据、明确交换或情感压力触发。"
		}
	}
	misbeliefs := []string{"开章时可能误判第一章异常/压力的真实代价，正文需用证据修正。"}
	plausibleMistakes := []string{
		"把当前压力误认为旧经验足以处理的普通问题",
		"过度依赖旧经验导致判断过窄",
		"在压力下做出一次迟疑、错判或过度自保",
	}
	correctionTriggers := []string{
		"可见物件或规则反馈打破旧经验",
		"他人犯错带来可复核代价",
		"关系/资源损失迫使其修正判断",
	}
	unknownFacts := []string{"第一章世界规则的完整代价", "其他角色的真实意图", "章末钩子背后的答案"}
	pressure := fmt.Sprintf("第一章核心事件“%s”会检验其性格、资源和关系边界。", zeroFirstNonEmpty(project.FirstChapter.CoreEvent, project.FirstChapter.Title))
	resources := []string{"角色卡既有经验", "第一章可见事实", "可复核的关系/资源台账"}
	evidenceSeen := []string{"premise", "characters", "current_chapter_outline", "world_rules/book_world"}
	likelyAction := "先按最低证据标准试探局面，再用可见行动换取新信息或资源。"
	competenceStage := "开局阶段：只能使用角色卡里的经验和现场证据，不能预装最终答案。"
	triggerEvent := fmt.Sprintf("第一章核心事件触发：%s。", zeroFirstNonEmpty(project.FirstChapter.CoreEvent, project.FirstChapter.Title))
	actionPressure := "必须在章内做出一个能改变状态的选择。"
	pressureTest := "第一章用规则、关系或资源短缺测试其行动倾向。"
	growthSignal := "承认一个新信息，并为下一章留下主动目标。"
	if !firstChapterActive {
		mention := project.FirstMentions[strings.TrimSpace(c.Name)]
		entryWindow := "首次正式入场前"
		if mention > 1 {
			entryWindow = fmt.Sprintf("第%d章正式入场前", mention)
		}
		pressure = entryWindow + "承受自己的生活、资源与关系压力；不得为了补齐人物表提前进入第一章现场。"
		resources = []string{"角色卡既有经验", "离屏日程与职业资源", "首次入场前已授权的关系台账"}
		evidenceSeen = []string{"premise", "characters", "first_mention_outline_anchor", "world_rules/book_world"}
		likelyAction = "沿自己的离屏生活线行动并留下可回收状态；未满足联系或入场条件时不介入第一章。"
		competenceStage = entryWindow + "的离屏基线：只保留角色卡经验、日程和可授权关系，不预装第一章现场证据。"
		triggerEvent = entryWindow + "的个人生活线持续推进，与第一章事件没有现场接触。"
		actionPressure = "维持一条可回收的离屏行动或资源变化，首次入场时再与主线发生因果接触。"
		pressureTest = entryWindow + "由其自身目标、日程和资源约束施压，不借第一章事件抢戏。"
		growthSignal = "首次入场时带着离屏期间形成的具体状态、目标或代价，而不是空降为功能角色。"
		misbeliefs = []string{"对主线只保留角色卡允许的间接判断；未进入现场前不得知道第一章具体事件。"}
		unknownFacts = []string{"第一章现场发生的具体过程", "主角未公开的选择与秘密", "首次入场前无法接触的主线证据"}
		plausibleMistakes = []string{"按自己的生活线误判主线影响", "受既有关系或资源压力限制而延迟介入", "首次入场时携带尚未修正的局部判断"}
		correctionTriggers = []string{"首次接触主线后的可见证据", "关系对象主动披露且已获授权的信息", "角色自身付出代价后得到的新事实"}
	}
	return domain.CharacterSimulationState{
		Character:          c.Name,
		CurrentGoal:        zeroCurrentGoal(project, c, role),
		Pressure:           pressure,
		Resources:          resources,
		RelationshipForces: relationshipForces,
		Secrets:            []string{"零章只允许保留角色卡或大纲已授权的秘密，未授权秘密写入 information_gaps。"},
		Misbeliefs:         misbeliefs,
		PrivateBoundary:    "不说出自己无法知道的信息；不为完成大纲突然解释、救场或转性。",
		ActionTendency:     actionBias,
		LikelyAction:       likelyAction,
		StateDeltaToTrack:  []string{"goal", "pressure", "resource", "relationship_contract", "knowledge", "emotion", "arc_axis"},
		CompetenceStage:    competenceStage,
		SkillLimits: []string{
			zeroSkillLimitWorldMechanism(),
			"不能稳定判断所有收益和代价",
			"不能替其他角色提前知道后台秘密",
		},
		PlausibleMistakes:  plausibleMistakes,
		CorrectionTriggers: correctionTriggers,
		KnowledgeLedger: domain.CharacterKnowledgeLedger{
			KnownFacts:    []string{zeroOpeningCharacterFact(c)},
			UnknownFacts:  unknownFacts,
			Suspicions:    []string{"现场异常或关系压力不会无代价解除。"},
			FalseBeliefs:  []string{"以为只靠旧经验就能处理第一章问题。"},
			EvidenceSeen:  evidenceSeen,
			Confidence:    "zero_chapter_baseline",
			SourceChapter: 0,
			ForbiddenKnowledge: []string{
				"未在角色卡、大纲、世界规则或 RAG 召回中出现的谜底与后台设定。",
				"中后期角色弧、尚未发生的关系进展、秘密机制及未来秘密归属不属于零章知识。",
			},
		},
		DecisionFrame: domain.CharacterDecisionFrame{
			AvailableOptions:        []string{"观察并核验证据", "与关键关系对象交换信息", "暂时拒绝高风险承诺"},
			RejectedOptions:         []string{"凭空知道答案", "为了推进剧情突然相信陌生信息", "无代价解决核心冲突"},
			DecisionRule:            "先核验证据，再决定是否承诺、交易、暴露秘密或升级冲突。",
			Tradeoff:                "越快行动越可能抢到信息，越慢越能保住边界但会付出时机成本。",
			CostPaid:                "至少付出时间、信任、资源或安全感之一。",
			RiskAccepted:            "接受局部风险，不接受无法复核的全局解释。",
			ExpectedGain:            "获得下一步行动线索或改变一个关系/资源状态。",
			MinimumEvidenceRequired: "正文中必须出现可被读者复核的物件、台词、规则反应或关系动作。",
		},
		RelationshipContract: relationshipContracts,
		EmotionAppraisal: domain.CharacterEmotionAppraisal{
			TriggerEvent:         triggerEvent,
			GoalImpact:           "迫使角色把静态目标转化成可见选择。",
			ThreatToValue:        "威胁其安全、责任、尊严、资源或关系边界。",
			VisibleExpression:    "用动作、停顿、询问、避让或交易呈现，不直接贴情绪标签。",
			SuppressedExpression: "隐瞒自己还不确定、害怕或想要交换的部分。",
			CopingStrategy:       "先缩小问题、核验证据、保留退路。",
			ActionPressure:       actionPressure,
			RelationshipEffect:   zeroRelationshipEffectLine(),
		},
		ArcAxis: domain.CharacterArcAxis{
			Want:             zeroFirstNonEmpty(zeroCurrentGoal(project, c, role), "拿到眼前问题的控制权。"),
			Need:             "学会用行动和证据承担选择后果，而不是停留在人设标签。",
			WoundOrGhost:     "角色卡中的创伤、执念或责任必须通过具体反应出现。",
			CoreLie:          "以为旧经验足以解释第一章的新压力。",
			ValueAxis:        arc,
			ArcStage:         "zero_chapter_baseline",
			PressureTest:     pressureTest,
			GrowthSignal:     growthSignal,
			RegressionSignal: "用漂亮判断、沉默或逃避替代真实选择。",
		},
	}
}

func zeroCurrentCounterparts(project zeroInitProject, c domain.Character) []string {
	if !zeroFirstChapterCharacterActive(project, c) {
		return nil
	}
	var out []string
	for _, name := range zeroCounterpartsForCharacter(project, c) {
		for _, other := range project.Characters {
			if zeroCharacterNameIs(other, name) && zeroFirstChapterCharacterActive(project, other) {
				out = append(out, strings.TrimSpace(other.Name))
				break
			}
		}
	}
	return out
}

func zeroOpeningCharacterDescription(c domain.Character) string {
	desc := strings.TrimSpace(c.Description)
	if desc == "" {
		return ""
	}
	if idx := strings.IndexAny(desc, "。；！？\n"); idx >= 0 {
		desc = desc[:idx]
	}
	for _, marker := range []string{
		"早期", "前期", "中期", "后期", "终局", "最终", "未来", "逐步", "后来",
		"确认关系时", "之后成为", "将成为", "转任", "升任", "绑定只能", "绑定系统", "系统秘密",
		"推理系统存在", "发现系统", "知道系统", "不知系统", "唯一知情", "任务限制", "惩罚触发",
	} {
		if idx := strings.Index(desc, marker); idx >= 0 {
			desc = desc[:idx]
		}
	}
	return strings.Trim(strings.TrimSpace(desc), "，,；;：:")
}

func zeroOpeningArcBaseline(c domain.Character) string {
	role := zeroFirstNonEmpty(strings.TrimSpace(c.Role), "当前身份")
	if traits := strings.TrimSpace(strings.Join(c.Traits, "、")); traits != "" {
		return fmt.Sprintf("开局只验证%s以%s应对眼前压力；完整角色弧不载入零章工作记忆。", role, traits)
	}
	return fmt.Sprintf("开局只验证%s如何应对眼前压力；完整角色弧不载入零章工作记忆。", role)
}

func zeroOpeningActionBias(c domain.Character) string {
	parts := append([]string{}, c.Traits...)
	if baseline := zeroOpeningCharacterDescription(c); baseline != "" {
		parts = append(parts, baseline)
	}
	if len(parts) == 0 {
		return "先观察、再试探、最后用可见证据做选择。"
	}
	src := strings.Join(parts, "；")
	if len([]rune(src)) > 80 {
		src = string([]rune(src)[:80])
	}
	return "由当下角色基线推出：" + src + "；行动时先保留边界，再换取新信息。"
}

func zeroOpeningCharacterFact(c domain.Character) string {
	name := zeroFirstNonEmpty(strings.TrimSpace(c.Name), "该角色")
	desc := zeroOpeningCharacterDescription(c)
	if desc == "" {
		return fmt.Sprintf("自己名为%s；故事开始前其他事实必须由正文、角色当下证据或台账确认。", name)
	}
	return fmt.Sprintf("自己名为%s；故事开始前可确认的角色资料：%s。", name, desc)
}

func zeroCurrentGoal(project zeroInitProject, c domain.Character, role string) string {
	name := zeroFirstNonEmpty(c.Name, role, "该角色")
	if !zeroFirstChapterCharacterActive(project, c) {
		if mention := project.FirstMentions[strings.TrimSpace(c.Name)]; mention > 1 {
			return fmt.Sprintf("%s在第%d章前沿自己的生活线行动，首次入场时以%s的现实目标影响主线，不提前挤进开篇。", name, mention, role)
		}
		return fmt.Sprintf("%s在首次正式入场前维持自己的%s生活线，不得为了人物覆盖提前进入第一章现场。", name, role)
	}
	event := zeroFirstNonEmpty(project.FirstChapter.CoreEvent, project.FirstChapter.Title, "第一章核心事件")
	baseline := zeroFirstNonEmpty(zeroOpeningCharacterDescription(c), strings.Join(c.Traits, "、"), role)
	return fmt.Sprintf("%s以%s的身份和“%s”的当下基线进入“%s”，做出一个可验证且会留下后果的选择。", name, role, baseline, event)
}

func zeroSkillLimitWorldMechanism() string {
	return "不知道第一章世界规则与其他角色选择的完整机制"
}

func zeroRelationshipEffectLine() string {
	return "选择会改变至少一条信任、债务、亏欠、恐惧、依赖或边界记录。"
}

func zeroInitVoiceLogic(project zeroInitProject, c domain.Character) domain.CharacterVoiceLogic {
	return domain.CharacterVoiceLogic{
		Character:          c.Name,
		PersonalitySource:  zeroFirstNonEmpty(strings.Join(c.Traits, "、"), zeroOpeningCharacterDescription(c), c.Role),
		SpeechPrinciple:    zeroSpeechPrinciple(project, c),
		SceneObjective:     zeroSceneObjective(project, c),
		HiddenSubtext:      zeroHiddenSubtext(project, c),
		KnowledgeBoundary:  "只说自己能从角色卡、现场证据和已授权关系里推出来的话。",
		RelationshipStance: zeroRelationshipStance(project, c),
		DictionAndRhythm:   zeroDictionAndRhythm(project, c),
		SentenceLength:     "平稳时中短句核验事实；受惊、撒谎、被逼问时允许断句、改口和半句停住。",
		PunctuationStyle:   "问号只用于真实核验或失控反应；避免连续反问、整齐顿号清单和每句都句号斩断。",
		LineBreakStyle:     "只有选择前犹豫、物件反馈、关系冷场或情绪压不住时单独断行。",
		SubtextStrategy:    "把恐惧、索取、隐瞒和试探藏进条件、费用、时间、称呼和动作里，不直接自我剖白。",
		SilenceOrAction:    "关键节点至少用一次沉默、手部动作、视线偏移或物件细节代替解释。",
		VoiceContrast:      "与同场角色用词、反应速度和信息处理习惯拉开；不要所有人都像同一个冷静旁白。",
		ActionBeatPolicy:   "对白前后必须有动作拍、物件反应或关系后果承接。",
		DialogueFunctions:  []string{"推进冲突", "暴露知识边界", "交换或隐藏信息", "改变关系状态"},
		TypicalMoves:       []string{"追问证据", "回避未授权秘密", "用具体条件换承诺"},
		ForbiddenMoves:     []string{"突然懂完整规则", "替作者解释设定", "无证据相信陌生信息", "只用金句收束情绪"},
		DialogueTest:       []string{"删掉说话人后仍能看出场景目标和知识边界", "每句对白至少改变信息、关系或压力之一"},
	}
}

func zeroSceneObjective(project zeroInitProject, c domain.Character) string {
	event := zeroFirstNonEmpty(project.FirstChapter.CoreEvent, project.FirstChapter.Title, "第一章事件")
	if !zeroFirstChapterCharacterActive(project, c) {
		if mention := project.FirstMentions[strings.TrimSpace(c.Name)]; mention > 1 {
			return fmt.Sprintf("第%d章前保持自己的离屏生活线；首次入场时让%s的目标、资源或顾虑真实改变现场，不提前抢进第一章。", mention, zeroFirstNonEmpty(c.Name, c.Role, "该角色"))
		}
		return fmt.Sprintf("首次正式入场前保持%s的离屏生活线；等联系、见面或事件条件成立后再影响主线。", zeroFirstNonEmpty(c.Name, c.Role, "该角色"))
	}
	role := zeroFirstNonEmpty(c.Role, "当前身份")
	return fmt.Sprintf("%s在“%s”中只按%s能知道的信息行动，用一次选择、拒绝或交换留下可追踪后果。", zeroFirstNonEmpty(c.Name, role, "该角色"), event, role)
}

func zeroSpeechPrinciple(project zeroInitProject, c domain.Character) string {
	if !zeroFirstChapterCharacterActive(project, c) {
		return fmt.Sprintf("%s首次入场前只保留由%s与当下性格推出的个人声口；不预写与主角的熟稔、暧昧、默契或共同秘密。", zeroFirstNonEmpty(c.Name, "该角色"), zeroFirstNonEmpty(c.Role, "当前身份"))
	}
	return fmt.Sprintf("%s说话先服从%s的处境和已知信息；用词、句长与反应速度由角色卡中的“%s”推出，不能套用固定人物声口。", zeroFirstNonEmpty(c.Name, "该角色"), zeroFirstNonEmpty(c.Role, "当前身份"), zeroFirstNonEmpty(strings.Join(c.Traits, "、"), zeroOpeningCharacterDescription(c), "既有性格"))
}

func zeroHiddenSubtext(project zeroInitProject, c domain.Character) string {
	if !zeroFirstChapterCharacterActive(project, c) {
		return fmt.Sprintf("%s在离屏阶段先守住%s的现实利益、关系与日程；未正式入场前不对第一章现场作出反应。", zeroFirstNonEmpty(c.Name, "该角色"), zeroFirstNonEmpty(c.Role, "当前角色"))
	}
	seed := zeroFirstNonEmpty(c.Arc, zeroOpeningCharacterDescription(c), strings.Join(c.Traits, "、"), c.Role)
	return fmt.Sprintf("%s表面按%s职责行动，未明说的需要与恐惧只能从角色卡基线“%s”和现场选择推出；不得预填历史项目的秘密。", zeroFirstNonEmpty(c.Name, "该角色"), zeroFirstNonEmpty(c.Role, "当前角色"), seed)
}

func zeroRelationshipStance(project zeroInitProject, c domain.Character) string {
	if !zeroFirstChapterCharacterActive(project, c) {
		return fmt.Sprintf("%s未进入第一章时不预建亲密默契；与主角的关系只保留角色卡已写明的基线，首次联系/见面后再按证据更新。", zeroFirstNonEmpty(c.Name, "该角色"))
	}
	counterparts := zeroCurrentCounterparts(project, c)
	target := "其他在场角色"
	if len(counterparts) > 0 {
		target = strings.Join(counterparts, "、")
	}
	return fmt.Sprintf("%s先以%s的身份面对%s；信任、帮助、依赖和对抗都由角色卡基线、现场交换与可见后果升级。", zeroFirstNonEmpty(c.Name, "该角色"), zeroFirstNonEmpty(c.Role, "当前角色"), target)
}

func zeroDictionAndRhythm(project zeroInitProject, c domain.Character) string {
	if !zeroFirstChapterCharacterActive(project, c) {
		return fmt.Sprintf("离屏阶段不预写对白；首次入场时从%s的职业词、生活词和性格节奏建立声口，不能沿用主角或其他角色模板。", zeroFirstNonEmpty(c.Role, "当前身份"))
	}
	return fmt.Sprintf("以%s的职业词、生活词和角色卡特征“%s”为底，句长随压力变化；不复制其他角色或历史项目的口头禅。", zeroFirstNonEmpty(c.Role, "当前身份"), zeroFirstNonEmpty(strings.Join(c.Traits, "、"), zeroOpeningCharacterDescription(c), "待正文验证"))
}

func zeroDialogueSceneBlueprints(project zeroInitProject, states []domain.CharacterSimulationState) []domain.DialogueSceneBlueprint {
	protagonist := zeroFirstNonEmpty(zeroProtagonist(project.Characters).Name, "主角")
	counterpart := zeroFirstNonEmpty(zeroFirstActiveNonProtagonistName(project), zeroAssetOpeningPressureName(project), "第一位施压者")
	for _, state := range states {
		if state.Character != "" && state.Character != protagonist {
			counterpart = state.Character
			break
		}
	}
	scene := zeroFirstNonEmpty(zeroFirstSceneForProject(project), "第一章主场景")
	pressure := zeroFirstNonEmpty(project.FirstChapter.CoreEvent, project.FirstChapter.Hook, project.FirstChapter.Title, "第一章核心压力")
	modeReason := "第一章需要让角色围绕目标、责任、代价、执行条件或合作边界进行真实协商；若当前场景功能不同，正式计划必须据此改选对白模式。"
	emotionalTemperature := "开局压住难堪、怀疑或不服气，随后因催促、误读、观望或责任分歧升温；不写成全程冷静。"
	identityGrounding := fmt.Sprintf("通过称谓、外貌/衣着、职责、现场位置、既有关系或可见凭证说明%s是谁，以及他为什么能影响%s的选择。", counterpart, protagonist)
	beatDensity := "高压协商使用短动作拍但不能每句一拍；关系缓和处留出呼吸，流程信息用现场物件、他人反应或结果变化换挡。"
	exitBeat := "用具体现场变化退出：物件或位置改变、有人加入/离开、一次交换成立、关系姿态变化；不用菜单选项或抽象金句收尾。"
	return []domain.DialogueSceneBlueprint{{
		SceneID:              "opening-dialogue-engine",
		DialogueMode:         "pressure_negotiation",
		ModeReason:           modeReason,
		ScenePressure:        fmt.Sprintf("核心压力来自“%s”，同时叠加时间、资源、身份和信息差。", pressure),
		EmotionalTemperature: emotionalTemperature,
		RelationshipFrame:    fmt.Sprintf("%s和%s存在信息不对称、短期互相需要或权力差，不能像两个设定讲解员。", protagonist, counterpart),
		Medium:               "face_to_face；若正式章节走电话/消息/纸条/门缝/传话，动作拍必须换成媒介拍：已读不回、打字又删掉、纸面折痕、门后脚步。",
		POVRole:              "participant",
		AudiencePresence: domain.DialogueAudiencePresence{
			Present:        "none；若本章有围观者、下属或记录者在场，必须改填具体第三方。",
			PerformanceFor: "有观众时写明双方各自演给谁看、想在观众面前保住或摧毁什么；无观众写 none。",
			AudienceEffect: "有观众时写明观众反应如何反过来改变对话走向：起哄、沉默、倒戈、记录在案；无观众写 none。",
		},
		InfoAsymmetry: domain.DialogueInfoAsymmetry{
			POVKnows:       fmt.Sprintf("%s此刻只掌握现场可见证据和自己带进场的旧信息。", protagonist),
			POVLacks:       fmt.Sprintf("%s不知道完整规则、对方真实议程和这场交换的真实代价，因此会误读至少一次。", protagonist),
			OtherHolds:     fmt.Sprintf("%s掌握任务/规则/风险的另一半事实，并且有隐瞒动机。", counterpart),
			ReaderPosition: "reader_level；正式计划里明确读者是否比主角先知道底牌(reader_ahead 反讽)或更晚(reader_behind 悬念)。",
			AsymmetryPlay:  "信息差通过半句话、被打断的解释或物件证据局部收窄，但收窄的同时必须打开一个新缺口。",
		},
		ValueShift: domain.DialogueValueShift{
			Value:         "主动权与安全感",
			OpeningCharge: fmt.Sprintf("负：%s被催促、被命名、不明规则，处在被动位置。", protagonist),
			TurnTrigger:   fmt.Sprintf("%s抓住对方话术或名册/凭证里的一处漏洞，把催促缩成可验证的问题。", protagonist),
			ClosingCharge: "半正：主角换回局部主动权，但欠下新的代价或误解；不能无成本全赢。",
		},
		PowerTrajectory: domain.DialoguePowerTrajectory{
			OpeningHolder: fmt.Sprintf("%s，凭称呼权、时间压力和信息差先占上风。", counterpart),
			FlipBeat:      "第二轮左右：主角用短句核验或沉默打断节奏，权力第一次易手。",
			ClosingHolder: "双方各占一半：对方保住隐瞒，主角保住边界；正式计划必须写明收场谁占上风。",
		},
		AddressShift:                "记录双方称呼随压力如何漂移：敬称脱落、直呼其名、去称谓或突然加回客气话，称谓变化本身是一条潜台词线。",
		OpeningStrategy:             "dialogue_first/action_first/object_first/silence_first 任选其一；由当前章压力决定，不把对白先入场当固定模板。",
		FirstSpokenMoment:           fmt.Sprintf("若采用对白先行，%s可以先发话；若采用动作/物件/沉默先行，第一句必须等到%s已有误判或压力后再出现。", counterpart, protagonist),
		EntryLine:                   fmt.Sprintf("可选 dialogue_first 版本：%s先开口，把%s从旧状态拉入“%s”的现场压力；正式写作必须改成本书世界里的真实称呼/话术。", counterpart, protagonist, pressure),
		EntrySpeaker:                counterpart,
		LocationAnchor:              fmt.Sprintf("%s。用一句短定场落下地点、时辰、空间边界或可见物件，不先写长篇设定。", scene),
		POVState:                    fmt.Sprintf("%s先有半拍迟滞、误判、身体反应或自我安慰，不能一上来就理解完整规则。", protagonist),
		InnerQuestion:               "贴近主视角写一个具体困惑，例如自己为什么在这里、这句话指向谁、这笔账买的是什么；第三人称项目只写短念头，不切换全知解释。",
		MemoryBridge:                "只补读者理解这场对白所需的身份、前一幕、工作/生活处境和关系压力；禁止一次性灌完整背景、履历或世界观。",
		IdentityGrounding:           identityGrounding,
		DialogueObjective:           "用对白落成本章任务、交易、威胁、求助或规则第一次露面，并迫使主角下一步行动发生变化。",
		InterlocutorAgenda:          fmt.Sprintf("%s不是等待被剧情收走的人，他此刻有自己的事、压力和隐瞒，开口是为了转移风险、索取帮助、完成职责或保住资源。", counterpart),
		ProtagonistResponseStrategy: fmt.Sprintf("%s按当前信息量先核验称呼、费用、证据或对方意图；允许误听、按错、停顿、绕开姓名，不写成想明白后再行动。", protagonist),
		ObjectiveTactics: []domain.DialogueObjectiveTactic{
			{
				Character:          counterpart,
				ImmediateObjective: "把任务、风险、请求或责任推到主角面前，同时保住自己的隐瞒。",
				Tactic:             "先用称呼、催促、求助、半句事实或制度话术占据节奏。",
				CounterTactic:      "主角用短句核验、沉默、转问交易内容或动作迟滞打断节奏。",
				EmotionalLeak:      "对方的恐惧、自利或职责从过快语速、重复称呼、避开关键词或看向物件里漏出。",
				TurnResult:         "对方没有完成全额转嫁，只暴露出一处可追问的漏洞。",
			},
			{
				Character:          protagonist,
				ImmediateObjective: "获得最低限度证据，同时避免被姓名、承诺、交易或关系债绑定。",
				Tactic:             "缩小问题、拒绝全局承诺、问具体对象，必要时误按或改口。",
				CounterTactic:      "对方继续施加时间压力、情绪压力、规则压力或人情压力。",
				EmotionalLeak:      "恐惧从手停、语气变短、答非所问或把一句话吞回去里出现。",
				TurnResult:         "主角只拿回局部主动权，并留下新的代价或误解。",
			},
		},
		TurnProgression: []domain.DialogueTurnDesign{
			{
				Speaker:             counterpart,
				SurfaceLineFunction: "入场称呼/通知/催促/求助，把场景压力直接递到主角面前。",
				HiddenSubtext:       "把自己的风险、职责或恐惧藏在礼貌、催促、玩笑、命令或半句求助里。",
				NewInformation:      "读者知道当前场景有一条必须回应的关系/制度/交易压力。",
				PowerMove:           "对方先占据命名权、解释权或时间压力。",
				ActionBeat:          "用门口停顿、纸面递来、屏幕亮起、椅子声、手指悬停或视线闪避承接第一句。",
				NextPressure:        "主角必须回答、确认、拒绝或继续装作没听懂。",
			},
			{
				Speaker:             protagonist,
				SurfaceLineFunction: "短句核验、改口、敷衍或先保留承诺。",
				HiddenSubtext:       "他怕被绑定责任，也怕不回应导致更坏后果。",
				NewInformation:      "暴露主角此刻不知道完整规则，只能抓住一个可验证细节。",
				PowerMove:           "主角没有夺回全局主动权，只把对方给出的压力缩小成可确认的问题。",
				ActionBeat:          "让身体反应先于判断：手停住、喉咙发紧、看错字、错按一次或把话吞回去。",
				NextPressure:        "对方继续推进任务/交易/威胁，或环境给出第一次规则反馈。",
			},
		},
		DirectnessPolicy:     "任务、时间、价格、危险可以局部直说；恐惧、羞耻、索取、背叛和真实意图优先用潜台词、动作和误读呈现。",
		SubtextSource:        "潜台词来自关系债、资源压力、信息差、旧伤、防御机制、身份不平等或文化潜规则。",
		EscalationPattern:    "优先使用 yes-but / no-and / 三次试探 / 让步换代价；不同模式可改成审问递进、互怼升级、暧昧回避或沉默压迫。",
		BeatDensity:          beatDensity,
		SilencePolicy:        "至少安排一次无人接话、答非所问、物件不回应或刻意停顿，让潜台词承担信息。",
		InfoReleasePolicy:    "信息可以被打断、误读、半句露出或事后才明白；禁止一问一答把规则讲完。",
		ExpositionBudget:     "背景信息只允许夹在对白后的短记忆/身份桥里，每次服务当前一句台词；一段最多解决一个问题。",
		SubtextAndPowerShift: "从对方掌握称呼/任务/时间压力，推进到主角发现一处漏洞或代价；主角可以暂时保住边界，但不能无成本全赢。",
		ExitBeat:             exitBeat,
		DoNotUse: []string{
			"照抄示例里的现实单位、职务或穿越解释",
			"先整段说明设定再让人物说话",
			"把对白写成系统菜单或作者问答",
			"让主角第一轮就判断全对",
			"用漂亮金句替代犹豫、误判和动作后果",
		},
	}}
}

func zeroInitResourceLedger(project zeroInitProject) domain.ResourceLedger {
	protagonist := zeroProtagonist(project.Characters)
	return domain.ResourceLedger{
		Version: 1,
		Claims: []domain.ResourceClaim{
			{
				ID:        "zero-opening-evidence",
				Name:      "第一章现场证据",
				Owner:     zeroFirstNonEmpty(protagonist.Name, "主角"),
				Kind:      "evidence",
				Status:    "pending",
				Risk:      "未被正文验证前不得写成既成事实。",
				Evidence:  "current_chapter_outline + world_rules/book_world",
				Chapter:   0,
				UpdatedAt: project.GeneratedAt,
			},
			{
				ID:        "zero-character-experience",
				Name:      "角色既有经验",
				Owner:     zeroFirstNonEmpty(protagonist.Name, "主角"),
				Kind:      "competence",
				Status:    "pending",
				Risk:      "只能解释判断倾向，不能直接解决第一章核心冲突。",
				Evidence:  "characters",
				Chapter:   0,
				UpdatedAt: project.GeneratedAt,
			},
		},
	}
}

func zeroInitRelationshipState(project zeroInitProject, states []domain.CharacterSimulationState) map[string]any {
	var contracts []domain.CharacterRelationshipContract
	for _, state := range states {
		contracts = append(contracts, state.RelationshipContract...)
	}
	policy := "第一章正文只能在这些基线之上新增信任、债务、亏欠、恐惧、承诺、依赖、误解或边界变化；新增后必须通过审阅/回填进入正式 relationship_state。"
	return map[string]any{
		"version":      1,
		"scope":        "zero_chapter_initial",
		"chapter":      0,
		"generated_at": project.GeneratedAt,
		"contracts":    contracts,
		"policy":       policy,
	}
}

func zeroInitForeshadow(project zeroInitProject) map[string]any {
	return map[string]any{
		"version":      1,
		"scope":        "zero_chapter_initial",
		"chapter":      0,
		"generated_at": project.GeneratedAt,
		"seeds": []map[string]any{
			{
				"id":             "ch01-opening-hook",
				"description":    zeroFirstNonEmpty(project.FirstChapter.Hook, project.FirstChapter.CoreEvent, project.FirstChapter.Title),
				"status":         "planned_seed",
				"planting_rule":  "第一章只能埋可被现场证据承载的种子，不提前解释答案。",
				"payoff_horizon": "第2章以后按大纲和审阅回填推进。",
				"source_chapter": 0,
				"target_chapter": 1,
				"required_audit": "commit 后由 review 检查是否成为有效伏笔或只是装饰。",
			},
		},
	}
}

func zeroInitReturnPlan(project zeroInitProject) map[string]domain.CharacterReturnPlan {
	out := map[string]domain.CharacterReturnPlan{}
	for _, c := range project.Characters {
		firstMention := project.FirstMentions[c.Name]
		priority := zeroReturnPriority(project, c, firstMention)
		suggestedChapter := firstMention
		if suggestedChapter == 0 && zeroIsPrimaryProtagonist(project, c) {
			suggestedChapter = 1
		}
		withInfo := "回归时必须携带新信息、资源或时间压力、关系余波或未兑现承诺之一。"
		upgrade := "若做关键选择、建立债务/亏欠/承诺或需要后续回归，升级为关键角色并补全动态字段。"
		out[c.Name] = domain.CharacterReturnPlan{
			ReturnPriority:     priority,
			SuggestedChapter:   suggestedChapter,
			DueReason:          zeroReturnDueReason(project, c, firstMention),
			WithNewInformation: withInfo,
			UpgradePotential:   upgrade,
			RetireReason:       "若只是气氛/捧场/凑数，场景结束即退场，不进入长期台账。",
		}
	}
	return out
}

func zeroInitCrowdPolicy(project zeroInitProject) map[string]any {
	continuity := "不进长期台账；若携带新信息、建立债务/亏欠/承诺或后续会回归，立即升级为关键角色。"
	return map[string]any{
		"version": 1,
		"policy":  "捧场类角色是场景压力与社会反馈，不是免费关键角色。默认不命名、不入长期人物台账、不承担解谜/救场/反杀。",
		"roles": []domain.CrowdRoleDesign{
			{
				GroupName:        "现场反应人群",
				Count:            2,
				SceneFunction:    "放大规则压力、提供噪声和社会评价，让主角选择显得有代价。",
				ReactionPolicy:   "只给短反应、迟疑、退让、抱怨或误解，不提供关键答案。",
				VoiceBudget:      "单人最多一句短台词；群体反应优先用动作和空间变化呈现。",
				NamingPolicy:     "默认不命名，用职责/位置/外观短锚点称呼。",
				ContinuityPolicy: continuity,
				ExitCondition:    "主角做出选择或规则后果落地后退场。",
			},
			{
				GroupName:        "功能性团队成员",
				Count:            1,
				SceneFunction:    "补足团队人数、分担操作或制造生活化摩擦。",
				ReactionPolicy:   "只承接具体任务，不替主角判断核心规则。",
				VoiceBudget:      "只在任务交接、错误提醒或情绪反应时开口。",
				NamingPolicy:     "需要重复出场才给名字；否则使用岗位称呼。",
				ContinuityPolicy: "完成场景功能即退场；连续两章出现必须补角色卡与动态字段。",
				ExitCondition:    "任务完成、信息交接结束或压力转回关键角色。",
			},
		},
	}
}

func zeroInitStorycraftPlan(project zeroInitProject, dynamics zeroInitCharacterDynamicsDoc) zeroPrewriteStorycraftPlan {
	usagePolicy := "所有新正文都必须先把本计划转入 plan_chapter.causal_simulation：人物先有 Want/Lie/Need/Truth 和合理犯错，行动先有身体/情绪/关系/创伤/偏差/意义驱动，关系先有亲密阶段与边界，人物先有可识别外观，对话先按角色、场景、压力、情绪和关系选择 dialogue_mode、opening_strategy 与 objective_tactics，读者先有小胜与新债，离屏线先有证据回收路径，章末先有具体后果契约；正式写作可根据当轮网络检索和最新台账更新字段，但不能省略同类字段。"
	return zeroPrewriteStorycraftPlan{
		Version:            2,
		Scope:              "reusable_prewrite_storycraft",
		Project:            project.Name,
		Chapter:            1,
		GeneratedAt:        project.GeneratedAt,
		UsagePolicy:        usagePolicy,
		ArcTests:           zeroCharacterArcTests(project, dynamics.Characters),
		VoiceCards:         dynamics.VoiceLogic,
		DialogueBlueprints: zeroDialogueSceneBlueprints(project, dynamics.Characters),
		ReaderReward:       zeroReaderRewardPlan(project),
		EvidenceChains:     zeroEvidenceReturnChains(project, dynamics.Characters),
		EndingContract:     zeroEndingContract(project),
		DormantPolicy:      zeroDormantCharacterPolicy(project, dynamics.Characters),
		RealitySupport:     zeroRealitySupportPlan(project),
		EmotionalLogic:     zeroEmotionalLogic(project, dynamics.Characters),
		RelationshipArcs:   zeroRelationshipEmotionArcs(project, dynamics.Characters),
		VisualDesign:       zeroVisualDesign(project, dynamics.Characters),
	}
}

func zeroCharacterArcTests(project zeroInitProject, states []domain.CharacterSimulationState) []domain.CharacterArcTest {
	var out []domain.CharacterArcTest
	stateByName := map[string]domain.CharacterSimulationState{}
	for _, state := range states {
		if state.Character != "" {
			stateByName[state.Character] = state
		}
	}
	for _, c := range project.Characters {
		if strings.TrimSpace(c.Name) == "" {
			continue
		}
		state := stateByName[c.Name]
		coreLie := zeroFirstNonEmpty(state.ArcAxis.CoreLie, firstString(state.Misbeliefs), "以为旧经验足以解释第一章新压力。")
		out = append(out, domain.CharacterArcTest{
			Character:        c.Name,
			Want:             zeroFirstNonEmpty(state.ArcAxis.Want, state.CurrentGoal, "先解决眼前压力。"),
			CoreLie:          coreLie,
			Need:             zeroFirstNonEmpty(state.ArcAxis.Need, "承认新世界需要新的证据和代价。"),
			Truth:            "真正有效的选择必须同时改变信息、关系或资源状态，不能只在脑内想明白。",
			PressureTest:     zeroFirstNonEmpty(state.ArcAxis.PressureTest, state.Pressure, project.FirstChapter.CoreEvent, "第一章规则压力测试角色旧反应。"),
			FirstMistake:     zeroFirstNonEmpty(firstString(state.PlausibleMistakes), "把异常先误判成普通流程或可拖延事项。"),
			CorrectionSignal: zeroFirstNonEmpty(firstString(state.CorrectionTriggers), "现场证据、代价或他人行动迫使其修正。"),
			ChapterEvidence:  "正文必须用选择、误操作、物件状态或关系后果证明这条弧线被测试。",
		})
	}
	if len(out) == 0 {
		out = append(out, domain.CharacterArcTest{
			Character:        zeroFirstNonEmpty(zeroProtagonist(project.Characters).Name, "主角"),
			Want:             "解决第一章眼前压力。",
			CoreLie:          "以为旧经验足以处理新世界。",
			Need:             "用证据和行动承担选择后果。",
			Truth:            "这个世界的规则会按代价反馈，不按主角想明白与否停止。",
			PressureTest:     zeroFirstNonEmpty(project.FirstChapter.CoreEvent, project.FirstChapter.Title),
			FirstMistake:     "先错判一次信息、按钮、称呼、规则或对方意图。",
			CorrectionSignal: "可见后果迫使其修正。",
			ChapterEvidence:  "第一章正文必须落成一次错误后的修正。",
		})
	}
	return out
}

func zeroReaderRewardPlan(project zeroInitProject) domain.ReaderRewardPlan {
	ladder := make([]domain.ReaderRewardStep, 0, 5)
	limit := min(len(project.Outline), 5)
	if limit == 0 {
		limit = 1
	}
	for i := 0; i < limit; i++ {
		entry := project.Outline[i]
		reward := zeroFirstNonEmpty(entry.CoreEvent, entry.Title, fmt.Sprintf("第%d章推进一个可见状态变化", i+1))
		cost := "奖励必须带出新代价、新债务、新误解或更深规则压力。"
		hook := zeroFirstNonEmpty(entry.Hook, "章末留下下一章能立刻承接的具体后果。")
		if i == 0 {
			reward = "第一章给出一个小胜：主角靠一次不完美但有效的行动完成可核验结果、守住边界或拿到下一步执行入口。"
			cost = "小胜不是免费，必须留下时间、资源、关系、返工、公开评价或下一步责任。"
		}
		ladder = append(ladder, domain.ReaderRewardStep{
			Chapter: i + 1,
			Reward:  reward,
			Cost:    cost,
			Hook:    hook,
		})
	}
	return domain.ReaderRewardPlan{
		ChapterWindow:        "1-5",
		FirstChapterSmallWin: "第一章不能只写受气或规则说明，必须让主角用一次现场行动换到真实结果、有限认可或下一步执行入口。",
		NewDebtOrCost:        "任何资源、协助、认可或暂缓都要留下时间、返工、责任、关系或机会成本。",
		PayoffVisibility:     "奖励必须落在交换结果、位置/物件变化、参与者态度、关系姿态或下一步可执行目标上。",
		TrafficRisk:          "只有受阻没有小胜会压抑；只有热闹没有结果与成本会显得悬浮。",
		RewardLadder:         ladder,
		ForbiddenRewardPatterns: []string{
			"主角全程正确", "围观者无条件服气", "只用数值替代结果", "免费资源随叫随到", "用总结替代现场行动",
		},
	}
}

func zeroEvidenceReturnChains(project zeroInitProject, states []domain.CharacterSimulationState) []domain.EvidenceReturnChain {
	protagonist := zeroProtagonist(project.Characters).Name
	scene := zeroFirstNonEmpty(zeroFirstSceneForProject(project), "第一章主场景")
	var out []domain.EvidenceReturnChain
	seen := map[string]bool{}
	add := func(name, event, timing string, resolve int) {
		name = strings.TrimSpace(name)
		if name == "" || name == protagonist || seen[name] {
			return
		}
		out = append(out, domain.EvidenceReturnChain{
			OffscreenCharacter:  name,
			Event:               event,
			Evidence:            zeroEvidenceReturnMedium(project),
			ProtagonistAccess:   zeroProtagonistAccessRule(project),
			ReturnTiming:        timing,
			DistortionOrMisread: zeroEvidenceDistortion(project),
			ChapterToResolve:    resolve,
		})
		seen[name] = true
	}
	for _, state := range states {
		if state.Character == "" || state.Character == protagonist {
			continue
		}
		add(state.Character, "在第一章同一时间线里承受自己的压力、误判或资源限制；正文可不展示。", "第2-5章或该角色首次回归时检查并回收。", zeroFirstNonZero(project.FirstMentions[state.Character], 3))
	}
	for _, c := range project.Characters {
		firstMention := project.FirstMentions[c.Name]
		if firstMention <= 0 || firstMention > 5 {
			continue
		}
		event := fmt.Sprintf("按第%d章首次牵引保留当下生活、日程与资源基线；第一章不预装其后期身份、秘密或未来关系，也不能在后续凭空随叫随到。", firstMention)
		if baseline := zeroOpeningCharacterDescription(c); baseline != "" {
			event = fmt.Sprintf("按第%d章首次牵引保留当下基线：%s；完整角色弧和后期信息不进入零章工作记忆。", firstMention, baseline)
		}
		add(c.Name, event, fmt.Sprintf("第%d章或首次出场前以可见证据回收。", firstMention), firstMention)
	}
	if len(out) == 0 {
		out = append(out, domain.EvidenceReturnChain{
			OffscreenCharacter:  "第一章现场外角色/后台压力源",
			Event:               zeroFirstNonEmpty(project.FirstChapter.CoreEvent, "第一章后台压力继续推进"),
			Evidence:            zeroFallbackEvidenceMedium(project),
			ProtagonistAccess:   zeroProtagonistAccessRule(project),
			ReturnTiming:        "第2章或下一次回到" + scene + "时",
			DistortionOrMisread: zeroEvidenceDistortion(project),
			ChapterToResolve:    2,
		})
	}
	return out
}

func zeroEvidenceReturnMedium(project zeroInitProject) string {
	return "后续通过通信、交换记录、现场状态变化、参与者转述、公开反馈或可复核物件回到主角视角。"
}

func zeroFallbackEvidenceMedium(project zeroInitProject) string {
	return "以交换记录、通信片段、参与者反馈、现场物件变化、位置状态或一条迟到的消息回到主角视角。"
}

func zeroProtagonistAccessRule(project zeroInitProject) string {
	return "主角必须通过亲历、通信、可复核记录、参与者转述或现场结果获得，不能默认知道离屏安排。"
}

func zeroEvidenceDistortion(project zeroInitProject) string {
	return "传回时可能被延迟、遮挡、截断、话术包装、权力关系过滤或被角色出于自保隐瞒一部分。"
}

func zeroEndingContract(project zeroInitProject) domain.EndingConsequenceContract {
	anchor := zeroFirstNonEmpty(project.FirstChapter.Hook, zeroFirstSceneForProject(project), project.FirstChapter.CoreEvent, "第一章可见证据")
	return domain.EndingConsequenceContract{
		EndingMode:      "具体现场后果落地，不用抽象金句或标准菜单收章。",
		ConcreteAnchor:  anchor,
		Consequence:     "章末必须改变一个可回填状态：交换结果、位置或物件、责任边界、参与者态度、关系或下一步执行目标至少其一。",
		NextChapterPull: "下一章直接消费该后果：结果能否复制、谁会加入或阻拦、谁需要承担新增责任。",
		WhyNotUI:        "如果出现系统界面、表格或条款，它必须先作用于人物选择和现场结果，不能把按钮或提示文字本身当剧情。",
		ForbiddenEndings: []string{
			"附加选项式 UI 菜单", "突然一个声音作为万能钩子", "金句加问号", "只报数值不写结果", "主角总结完道理后停章",
		},
	}
}

func zeroDormantCharacterPolicy(project zeroInitProject, states []domain.CharacterSimulationState) []domain.DormantCharacterPolicy {
	active := map[string]bool{}
	for _, state := range states {
		if state.Character != "" {
			active[state.Character] = true
		}
	}
	var out []domain.DormantCharacterPolicy
	for _, c := range project.Characters {
		if active[c.Name] {
			continue
		}
		firstMention := project.FirstMentions[c.Name]
		status := "未进入第一章主视角；按角色生活/工作地点和资源边界保持后台状态。"
		if firstMention > 0 && firstMention <= 5 {
			status = fmt.Sprintf("第%d章附近可能进入主线；第一章只保留位置和信息边界。", firstMention)
		}
		out = append(out, domain.DormantCharacterPolicy{
			Character:         c.Name,
			Status:            status,
			Location:          "角色卡默认生活/工作地点；若未设定，首次正式引入前补全。",
			NoChangeReason:    "第一章没有通信、目击、证据或交通路径把其拉入主角视角。",
			TriggerCondition:  zeroFirstNonEmpty(zeroReturnDueReason(project, c, firstMention), "当大纲、配角线或证据回收需要时再引入。"),
			KnowledgeBoundary: "主角不知道该角色后台经历，除非正文提供通信或证据。",
			NextCheck:         fmt.Sprintf("第%d章前后或该角色首次被提及时检查", zeroFirstNonZero(firstMention, 3)),
		})
	}
	if len(out) == 0 {
		trigger := "新角色做关键选择、建立债务/亏欠/承诺、携带信息或后续回归时触发。"
		out = append(out, domain.DormantCharacterPolicy{
			Character:         "none",
			Status:            "当前零章未识别休眠角色。",
			Location:          "none",
			NoChangeReason:    "所有已识别角色都进入第一章初始动态推演；后续新角色引入时必须补独立 dossier。",
			TriggerCondition:  trigger,
			KnowledgeBoundary: "主角默认不知道未引入角色。",
			NextCheck:         "每次 plan_chapter 前检查新角色需求。",
		})
	}
	return out
}

func zeroRealitySupportPlan(project zeroInitProject) []domain.RealitySupportPlan {
	scene := zeroFirstNonEmpty(zeroFirstSceneForProject(project), "第一章主场景")
	return []domain.RealitySupportPlan{
		{
			Domain:             "场景环境与现实执行",
			SourceRef:          "meta/web_reference_brief.md、RAG 或当轮 web_search；具体规则以当前项目 foundation、world_rules 和用户规则为准",
			UsableDetail:       "从当前章纲提取地点、时段、角色职责、可用资源、行动限制与参与者反馈；缺失项在正式 plan_chapter 前检索。",
			TransformedAs:      "角色通过与当前场景相符的动作、交流和选择推进目标，并让结果改变现场状态。",
			ChapterUse:         scene,
			ForbiddenDirectUse: []string{"百科或行业教程式旁白", "真实敏感单位直接搬用", "用流程清单替代人物选择"},
		},
		{
			Domain:             "资源、权限与结果核验",
			SourceRef:          "world_rules + current_chapter_outline + meta/web_reference_brief.md；必要时补当轮检索",
			UsableDetail:       "由当前项目决定关键资源、权限、证据、时间或关系承诺的状态、退出边界与可复核载体。",
			TransformedAs:      "先确认当前目标、责任和退出边界，再通过行动改变信息、关系或资源状态。",
			ChapterUse:         "第一章资源使用与结果变化",
			ForbiddenDirectUse: []string{"无成本解决冲突", "只报数值不写结果", "标准 UI 选项充当章末钩子"},
		},
		{
			Domain:             "空间、距离与角色日程",
			SourceRef:          "book_world.routes + 当前角色 dossier；必要时检索当前地点与行动方式的现实耗时",
			UsableDetail:       "地点距离、行动方式、出入条件、故事时段和个人职责造成的现实耗时。",
			TransformedAs:      "角色不能随叫随到；到场、通信、移动与返回都产生当前设定支持的成本。",
			ChapterUse:         "offscreen_character_stage.meeting_constraint",
			ForbiddenDirectUse: []string{"跨地点无耗时见面", "配角放下本职责任瞬间救场", "复杂行动一句话无成本完成"},
		},
	}
}

func zeroEmotionalLogic(project zeroInitProject, states []domain.CharacterSimulationState) []domain.CharacterEmotionalLogic {
	stateByName := map[string]domain.CharacterSimulationState{}
	for _, state := range states {
		if state.Character != "" {
			stateByName[state.Character] = state
		}
	}
	var out []domain.CharacterEmotionalLogic
	for _, c := range project.Characters {
		state := stateByName[c.Name]
		pressure := zeroFirstNonEmpty(state.Pressure, project.FirstChapter.CoreEvent, "正文开始前的世界/关系压力")
		emotion := zeroCharacterEmotionProfile(project, c)
		out = append(out, domain.CharacterEmotionalLogic{
			Character:               c.Name,
			PhysiologicalState:      emotion.PhysiologicalState,
			ImmediateState:          emotion.ImmediateState,
			BaselineMood:            zeroFirstNonEmpty(firstString(c.Traits), "紧绷"),
			PrimaryEmotion:          emotion.Primary,
			CompositeEmotion:        emotion.Composite,
			EmotionalTrigger:        pressure,
			GoalAppraisal:           emotion.GoalAppraisal,
			BoundaryThreat:          emotion.BoundaryThreat,
			RegulationStrategy:      emotion.Regulation,
			DefenseMechanism:        emotion.Defense,
			CognitiveBias:           emotion.Bias,
			ApproachAvoidance:       emotion.ApproachAvoidance,
			ShortLongTermTension:    emotion.ShortLongTerm,
			SelfRelationshipTension: emotion.SelfRelationship,
			ConsciousReason:         zeroFirstNonEmpty(state.DecisionFrame.DecisionRule, "我是在做理性选择。"),
			HiddenReason:            zeroFirstNonEmpty(state.ArcAxis.WoundOrGhost, emotion.HiddenReason),
			MeaningNeed:             emotion.MeaningNeed,
			Metacognition:           emotion.Metacognition,
			EmotionLedAction:        emotion.Action,
			EventCompletionRole:     emotion.EventRole,
			EvidenceInScene:         emotion.Evidence,
		})
	}
	if len(out) == 0 {
		out = append(out, domain.CharacterEmotionalLogic{
			Character:               "主角",
			PhysiologicalState:      "待补",
			ImmediateState:          "待补",
			BaselineMood:            "待补",
			PrimaryEmotion:          "恐惧",
			CompositeEmotion:        "羞耻和警惕",
			EmotionalTrigger:        zeroFirstNonEmpty(project.FirstChapter.CoreEvent, project.FirstChapter.Title),
			GoalAppraisal:           "事件威胁目标和边界。",
			BoundaryThreat:          "安全和身份边界。",
			RegulationStrategy:      "压抑后行动。",
			DefenseMechanism:        "合理化。",
			CognitiveBias:           "损失厌恶。",
			ApproachAvoidance:       "趋近安全，回避失控。",
			ShortLongTermTension:    "短期止损，长期成长。",
			SelfRelationshipTension: "自我与关系冲突。",
			ConsciousReason:         "理性选择。",
			HiddenReason:            "旧伤和恐惧。",
			MeaningNeed:             "证明自己能活下去。",
			Metacognition:           "有限自控。",
			EmotionLedAction:        "由恐惧推动试探。",
			EventCompletionRole:     "情绪完成事件。",
			EvidenceInScene:         []string{"动作停顿"},
		})
	}
	return out
}

type zeroEmotionProfile struct {
	PhysiologicalState string
	ImmediateState     string
	Primary            string
	Composite          string
	GoalAppraisal      string
	BoundaryThreat     string
	Regulation         string
	Defense            string
	Bias               string
	ApproachAvoidance  string
	ShortLongTerm      string
	SelfRelationship   string
	HiddenReason       string
	MeaningNeed        string
	Metacognition      string
	Action             string
	EventRole          string
	Evidence           []string
}

func zeroCharacterEmotionProfile(project zeroInitProject, c domain.Character) zeroEmotionProfile {
	name := zeroFirstNonEmpty(c.Name, "角色")
	role := zeroFirstNonEmpty(c.Role, "当前身份")
	traits := zeroFirstNonEmpty(strings.Join(c.Traits, "、"), zeroOpeningCharacterDescription(c), "既有性格")
	event := zeroFirstNonEmpty(project.FirstChapter.CoreEvent, project.FirstChapter.Title, "第一章核心事件")
	base := zeroEmotionProfile{
		PhysiologicalState: "普通生活或工作节奏下的能量基线；压力通过肩颈、手部、语速、呼吸和视线变化外露。",
		ImmediateState:     fmt.Sprintf("受“%s”的地点、时间窗口、职责和关系位置影响；不预设全程冷静。", event),
		Primary:            "不安",
		Composite:          fmt.Sprintf("%s以%s和“%s”应对新压力时的警惕、欲望与防御", name, role, traits),
		GoalAppraisal:      "把本章事件评估为会改变目标、资源或边界，先出现身体反应，再寻找符合角色卡的解释。",
		BoundaryThreat:     "安全、尊严、身份叙事、关系义务或资源控制权被威胁。",
		Regulation:         "先把情绪压成可执行的小动作，压力过高时露出停顿、回避或过度解释。",
		Defense:            fmt.Sprintf("%s用%s的习惯保护自己：先处理事，再承认情绪。", name, role),
		Bias:               "更相信能保护既有叙事的证据，直到现场代价逼其修正。",
		ApproachAvoidance:  "趋近安全和控制感，同时回避失控、亏欠或被看穿。",
		ShortLongTerm:      "短期想止损，长期想守住身份、关系或成长目标。",
		SelfRelationship:   "自我需求会和亲情、亏欠、承诺、群体评价或权力关系冲突。",
		HiddenReason:       fmt.Sprintf("真正原因必须从角色卡的弧线与基线“%s”推出，不从历史作品模板预填。", zeroFirstNonEmpty(c.Arc, traits)),
		MeaningNeed:        "想证明自己的选择仍有价值，并维持能解释眼前代价的自我叙事。",
		Metacognition:      "能否意识到自己在防御，要由本章压力决定；开局不默认高自控。",
		Action:             fmt.Sprintf("%s把情绪转成一次可见选择，推动信息、关系或资源发生位移。", name),
		EventRole:          "本章事件通过该角色的误判、克制或临界选择完成，而不是只被外部事件推着走。",
		Evidence:           []string{"动作或语速变化", "知识边界内的误判", "一次会留下后果的选择"},
	}
	if !zeroFirstChapterCharacterActive(project, c) {
		mention := project.FirstMentions[strings.TrimSpace(c.Name)]
		entryWindow := "首次正式入场前"
		if mention > 1 {
			entryWindow = fmt.Sprintf("第%d章正式入场前", mention)
		}
		base.ImmediateState = entryWindow + "处于自己的离屏生活、工作与关系节奏中；不承接第一章现场的即时情绪。"
		base.Primary = "蓄压"
		base.Composite = fmt.Sprintf("%s在离屏生活线中积累的现实压力与尚未介入主线的边界感", name)
		base.GoalAppraisal = "先按自己的日程、利益和关系判断眼前生活；只有联系、见面或事件条件成立后才评估主线。"
		base.Regulation = "用离屏行动、日程变化或资源取舍留下状态，避免隔空同步主角情绪。"
		base.Action = fmt.Sprintf("%s沿独立生活线形成一个首次入场时可回收的目标、代价或资源状态。", name)
		base.EventRole = "暂不进入第一章场景；通过可追踪的离屏状态保证后续出场有来路。"
	}
	return base
}

func zeroRelationshipEmotionArcs(project zeroInitProject, states []domain.CharacterSimulationState) []domain.RelationshipEmotionArc {
	protagonist := zeroProtagonist(project.Characters)
	var out []domain.RelationshipEmotionArc
	for _, c := range project.Characters {
		if c.Name == "" || c.Name == protagonist.Name {
			continue
		}
		firstChapterActive := zeroFirstChapterCharacterActive(project, c)
		relType := zeroRelationshipType(project, c)
		emotionalWant := "想从对方那里得到安全感、确认、资源、解释权、赦免或控制感；正式章节需按关系细化。"
		fear := "害怕被拖累、被抛弃、被看穿、被背叛、被债务绑定或失去主导权。"
		trustDebt := "信任从低位开始，任何帮助都要留下债务、亏欠、承诺或边界。"
		conflictTrigger := zeroFirstNonEmpty(project.FirstChapter.CoreEvent, "世界规则第一次施压")
		currentBond := zeroRelationshipBond(relType)
		intimacyStage := zeroIntimacyStage(relType)
		romancePotential := zeroRomancePotential(relType)
		nextBeat := "下一次推进必须改变信任、亏欠、亲密、嫉妒、保护欲或权力位置之一。"
		if !firstChapterActive {
			relType = "未建立/待首次互动"
			currentBond = "尚未相识、联系或共同经历；零章不预设互信、亏欠、默契、暧昧或敌意。"
			emotionalWant = "首次互动前不预写向对方索取的情感位置；只保留各自生活线的现实目标。"
			fear = "首次互动前只保留角色自身的现实顾虑，不把未来关系结果倒灌为当下情绪。"
			trustDebt = "none；首次可见互动后再根据行动建账。"
			conflictTrigger = "首次联系、见面或共同结果尚未发生。"
			intimacyStage = "未相识/未建立关系"
			romancePotential = "none；零章不加载未来关系方向。"
			nextBeat = "等首次互动发生后，再依据可见选择建立关系类型、信任与边界。"
		}
		out = append(out, domain.RelationshipEmotionArc{
			Pair:                         []string{protagonist.Name, c.Name},
			RelationshipType:             relType,
			CurrentBond:                  currentBond,
			EmotionalWant:                emotionalWant,
			Fear:                         fear,
			PowerBalance:                 "信息、资源、情绪需求和规则权限不对等。",
			IntimacyStage:                intimacyStage,
			TrustDebt:                    trustDebt,
			ConflictTrigger:              conflictTrigger,
			AttachmentOrLoveLanguage:     "以行动照顾、风险隔离、资源支持或边界尊重表达情感；不要只靠告白式台词。",
			Boundary:                     "没有通信、证据或共同经历时，关系不能突然亲密或全信。",
			RomancePotential:             romancePotential,
			NextEmotionalBeat:            nextBeat,
			ProtagonistKnowledgeBoundary: "主角只能知道正文中亲见、通信或证据传回的关系信息。",
		})
	}
	if len(out) == 0 {
		out = append(out, domain.RelationshipEmotionArc{
			Pair:                         []string{zeroFirstNonEmpty(protagonist.Name, "主角"), "none"},
			RelationshipType:             "none",
			CurrentBond:                  "当前未识别关键关系；新角色出现时必须补关系情绪弧。",
			EmotionalWant:                "待补",
			Fear:                         "待补",
			PowerBalance:                 "待补",
			IntimacyStage:                "陌生",
			TrustDebt:                    "none",
			ConflictTrigger:              "新关系引入",
			AttachmentOrLoveLanguage:     "待补",
			Boundary:                     "不能突然亲密",
			RomancePotential:             "none；未识别恋爱关系",
			NextEmotionalBeat:            "首次互动后补齐",
			ProtagonistKnowledgeBoundary: "主角不知道未引入关系",
		})
	}
	return out
}

func zeroRelationshipType(project zeroInitProject, c domain.Character) string {
	role := strings.TrimSpace(c.Role)
	text := strings.Join([]string{role, zeroOpeningCharacterDescription(c)}, " ")
	switch {
	case zeroContainsAny(role, "父亲", "母亲", "哥哥", "弟弟", "姐姐", "妹妹", "兄长", "家人", "亲属"):
		return "亲情"
	case zeroHighConfidenceRomanceRole(role) || zeroDirectedRomanceEvidence(project, c):
		return "恋爱/暧昧潜势"
	case strings.Contains(text, "反派") || strings.Contains(text, "敌") || strings.Contains(text, "压迫"):
		return "敌对/价值冲突"
	case zeroContainsAny(text, "朋友", "搭档", "后勤", "合作", "主角团", "伙伴", "盟友", "发小", "室友", "闺蜜", "同事"):
		return "合作/友谊"
	default:
		return "社会关系/潜在债务或亏欠"
	}
}

func zeroHighConfidenceRomanceRole(role string) bool {
	for _, part := range strings.FieldsFunc(role, func(r rune) bool {
		return r == '/' || r == '／' || r == '|' || r == '｜' || r == '、' || r == '，' || r == ','
	}) {
		switch strings.TrimSpace(part) {
		case "男主", "女主", "恋爱对象", "感情对象", "伴侣":
			return true
		}
	}
	return false
}

func zeroDirectedRomanceEvidence(project zeroInitProject, c domain.Character) bool {
	primary := strings.TrimSpace(zeroProtagonist(project.Characters).Name)
	if primary == "" {
		return false
	}
	text := zeroOpeningCharacterDescription(c)
	for _, clause := range strings.FieldsFunc(text, func(r rune) bool {
		return strings.ContainsRune("。；！？!?\n", r)
	}) {
		clause = strings.TrimSpace(clause)
		if clause == "" || !strings.Contains(clause, primary) || !zeroContainsAny(clause, "恋爱", "感情", "暧昧", "伴侣", "相爱") {
			continue
		}
		if zeroContainsAny(clause, "不是", "并非", "不与", "不会与", "不能成为", "不承担", "无暧昧", "不是感情") {
			continue
		}
		if zeroContainsAny(clause,
			"与"+primary, "和"+primary, primary+"的恋爱", primary+"的感情", primary+"的暧昧", primary+"的伴侣",
		) {
			return true
		}
	}
	return false
}

func zeroContainsAny(text string, candidates ...string) bool {
	for _, candidate := range candidates {
		if candidate != "" && strings.Contains(text, candidate) {
			return true
		}
	}
	return false
}

func zeroRelationshipBond(relType string) string {
	switch relType {
	case "亲情":
		return "爱与责任并存，保护欲、内疚和控制边界会互相冲突。"
	case "恋爱/暧昧潜势":
		return "吸引尚未等于信任，亲密必须通过共同风险、边界尊重和真实选择推进。"
	case "敌对/债务", "敌对/价值冲突":
		return "恐惧、贪婪、羞辱或支配欲维系关系。"
	case "职场对抗/价值冲突":
		return "对抗来自话语权、机会分配和价值判断，不来自单纯脸谱化压迫。"
	case "合作/友谊":
		return "互利先于亲密，信任靠可见行动慢慢建立。"
	default:
		return "尚未建立稳定情感连接，但可能通过债务、亏欠、目击、共同压力或一次边界尊重变成关系。"
	}
}

func zeroIntimacyStage(relType string) string {
	if relType == "恋爱/暧昧潜势" {
		return "陌生/试探；不得跳到承诺或亲密无间。"
	}
	if relType == "亲情" {
		return "已有历史亲密，但当前会受危机、隐瞒和责任压力扭曲。"
	}
	return "低信任试探"
}

func zeroRomancePotential(relationshipType string) string {
	if relationshipType == "恋爱/暧昧潜势" {
		return "有恋爱/暧昧潜势：吸引必须来自价值冲突、共同风险、边界尊重和互相看见，不靠强行发糖。"
	}
	return "none；当前关系不以恋爱驱动，若后续出现吸引必须重新建模亲密阶段、阻碍和边界。"
}

func zeroVisualDesign(project zeroInitProject, states []domain.CharacterSimulationState) []domain.CharacterVisualDesign {
	var out []domain.CharacterVisualDesign
	for _, c := range project.Characters {
		if c.Name == "" {
			continue
		}
		statusWear := "第一章或首次出场时要有与处境相关的灰、汗、雨、皱、破损或疲惫痕迹。"
		changeRule := "外观随资源、权力、工作强度、亲密关系和生活阶段改变；不能每次出场都像静态设定图。"
		out = append(out, domain.CharacterVisualDesign{
			Character:       c.Name,
			Silhouette:      zeroVisualSilhouette(c),
			FaceAndHair:     zeroVisualFaceHair(c),
			ClothingStyle:   zeroVisualClothing(c),
			ColorPalette:    zeroVisualPalette(project, c),
			BodyLanguage:    zeroVisualBodyLanguage(c),
			SignatureObject: zeroVisualObjectForProject(project, c),
			FirstImpression: zeroFirstNonEmpty(c.Role, "能被读者一眼区分的角色"),
			StatusWear:      statusWear,
			ChangeRule:      changeRule,
			SceneUse:        "首次入场至少一个视觉细节必须推动识别、情绪、关系或规则判断。",
			DoNotUse:        []string{"空泛帅/美/普通", "真实品牌堆砌", "与世界背景不合的服装", "所有角色同一黑衣冷脸"},
		})
	}
	if len(out) == 0 {
		out = append(out, domain.CharacterVisualDesign{
			Character:       "主角",
			Silhouette:      "待补但必须可识别",
			FaceAndHair:     "待补",
			ClothingStyle:   "待补",
			ColorPalette:    "待补",
			BodyLanguage:    "待补",
			SignatureObject: "待补",
			FirstImpression: "待补",
			StatusWear:      "待补",
			ChangeRule:      "随章节改变",
			SceneUse:        "首次入场使用",
			DoNotUse:        []string{"空白外貌"},
		})
	}
	return out
}

func zeroVisualObjectForProject(project zeroInitProject, c domain.Character) string {
	if zeroIsProtagonist(c) {
		return "与当前行动、生活责任或核验习惯绑定的日常物件。"
	}
	if strings.Contains(c.Role, "反派") {
		return "体现既有渠道、资源位置或控制欲的工作物件。"
	}
	return "与职业、生活责任或首次入场任务绑定的小物件，不能只做装饰。"
}

func zeroVisualSilhouette(c domain.Character) string {
	text := c.Role + " " + strings.Join(c.Traits, " ")
	switch {
	case strings.Contains(text, "反派") || strings.Contains(text, "危险") || strings.Contains(text, "贪"):
		return "偏尖、偏窄或骨感的轮廓，给人压迫和不稳定感。"
	case strings.Contains(text, "后勤") || strings.Contains(text, "父") || strings.Contains(text, "守"):
		return "方形或厚重轮廓，强调承担、笨拙或可靠。"
	case strings.Contains(text, "妹") || strings.Contains(text, "温") || strings.Contains(text, "医"):
		return "较软的圆线条和轻薄轮廓，但要保留紧绷/病弱/防备的细节。"
	default:
		return "中等偏收的轮廓，肩颈和手部动作承担心理压力。"
	}
}

func zeroVisualFaceHair(c domain.Character) string {
	if zeroIsProtagonist(c) {
		return "短发或便于行动的发型，脸部疲惫但眼神常在核验细节；不写成完美冷峻。"
	}
	if strings.Contains(c.Role, "反派") {
		return "脸部特征要有可记忆的不对称、过度整洁或骨感阴影；发型服务压迫感。"
	}
	return "发型、发质和修整程度要反映生活状态、职业资源和情绪防御。"
}

func zeroVisualClothing(c domain.Character) string {
	if zeroIsProtagonist(c) {
		return "旧外套/衬衫/便于行动的普通衣物，口袋、袖口、鞋底能承载贫穷、失业或奔波痕迹。"
	}
	if strings.Contains(c.Role, "反派") {
		return "过度干净、礼服化、制服化或与环境不合的衣物，制造不适感。"
	}
	return "衣服要显示职业、阶层、资源和当下处境；首次出场前可结合网络资料细化现实穿搭。"
}

func zeroVisualPalette(project zeroInitProject, c domain.Character) string {
	if strings.Contains(c.Role, "反派") {
		return "整洁克制的深色与一处过分讲究的亮点，体现其资源位置和控制欲。"
	}
	return "低饱和生活色加一处职业或性格标志色，随资源与生活阶段变化。"
}

func zeroVisualBodyLanguage(c domain.Character) string {
	if zeroIsProtagonist(c) {
		return "先收手、看字、避开承诺性动作；压力大时动作会慢半拍或突然停住。"
	}
	if strings.Contains(c.Role, "反派") {
		return "动作少而稳，距离感和俯视/停顿制造压迫。"
	}
	return "用站位、手部保护动作、眼神逃避或靠近距离显示关系和情绪。"
}

func zeroGroundedWorldBackgroundPlan(project zeroInitProject) zeroWorldBackgroundPlan {
	scene := zeroFirstNonEmpty(zeroFirstSceneForProject(project), "第一章主场景")
	region := zeroFirstNonEmpty(zeroKnownCityName(project), "开局区域")
	protagonist := zeroFirstNonEmpty(zeroProtagonist(project.Characters).Name, "主角")
	counterpart := zeroFirstNonEmpty(zeroFirstActiveNonProtagonistName(project), "开局关系对象")
	pressure := zeroFirstNonEmpty(project.FirstChapter.CoreEvent, project.FirstChapter.Hook, project.FirstChapter.Title, "第一章核心压力")
	ruleText := firstString(zeroWorldRuleTexts(project.WorldRules, 3))
	if ruleText == "" {
		ruleText = "人物的行动必须遵守当前项目已声明的边界，并留下可见原因与后果。"
	}
	adjacentNode := "第一章相邻地点"
	if project.BookWorld != nil {
		for _, place := range project.BookWorld.Places {
			if name := strings.TrimSpace(place.Name); name != "" && name != strings.TrimSpace(scene) {
				adjacentNode = name
				break
			}
		}
	}
	return zeroWorldBackgroundPlan{
		Version:     1,
		Scope:       "reusable_world_background_prewrite",
		Project:     project.Name,
		Chapter:     1,
		GeneratedAt: project.GeneratedAt,
		UsagePolicy: "写作前把当前项目的地点、时间、角色、规则、资源和信息边界转入章节因果模拟；只采用当前输入能支持、且会改变人物选择或结果的细节。",
		ResearchBasis: []string{
			"空间与时间必须由当前 book_world、章节大纲或可核验资料支持",
			"每个参与者都按角色卡拥有自己的目标、风险和退出条件",
			"信息差由主角可接触的证据与明确回收路径承载",
			"能力、制度或世界机制的边界与代价只能来自当前 world_rules",
			"关系变化必须来自共同经历、现实选择与可见后果",
		},
		Layers: domain.WorldBackgroundLayersPlan{
			PhysicalSpace:       fmt.Sprintf("%s/%s 是第一章行动空间；入口、出口、动线、距离、遮挡和可交互对象必须从当前地点资料或章节事件推出。", region, scene),
			TimeLayer:           "第一章必须固定到具体故事时段；行动窗口、移动耗时、角色日程和截止条件由当前大纲、世界资料或检索证据决定。",
			SocialInstitution:   "显规则只来自当前 world_rules、book_world、用户规则和场景中可见的制度边界；正文用人物选择与后果展示。",
			CulturalNorm:        "群体习惯、礼节、身份差和关系压力只能从当前世界设定、角色卡或现实资料推出，不能预填地域与行业模板。",
			RelationshipNetwork: fmt.Sprintf("%s 与 %s 的目标、职责、信息和风险差异以当前角色卡为准；没有同场证据、通信或共同结果时，不升级关系。", protagonist, counterpart),
			EconomicResource:    "结构性资源使用当前项目已经声明的资金、权限、物件、能力、人手、时间、渠道或信誉；每项资源都要写明控制者、条件与成本。",
			ConflictTension:     fmt.Sprintf("“%s”必须落实为人物目标与当前阻力的碰撞；成本类型由大纲和规则决定，不预设行业、地点或题材机制。", pressure),
			SocialMood:          "群体情绪只通过当前场景确有的参与者、媒介和传播路径扩散；传闻可以施压，不能替代事实。",
			CosmologyMetaRule:   fmt.Sprintf("当前核心规则样本为“%s”；能力、制度、技术或超自然机制都必须按当前项目写明范围、成本、证据与失败方式。", ruleText),
			NarrativeMeta:       "读者、主角、配角与离屏角色的信息量分层；人物只能依据亲见、通信、当前记录和获准传回的证据行动。",
			EventActivation:     "第一章事件由当前目标、时间窗口、资源约束、关系阻力和信息差共同激活；不能用预置类型模板代替因果。",
		},
		InformationLedger: []domain.InformationAsymmetryRecord{{
			Subject:           pressure,
			ReaderKnows:       []string{"读者只获得当前章节入口与可见压力，不一次性获得完整机制。"},
			ProtagonistKnows:  []string{"主角只知道亲历、收到、复核或由可信渠道提供的事实。"},
			CharacterKnows:    []string{fmt.Sprintf("%s 只掌握角色卡职责、既有关系与现场观察范围内的信息。", counterpart)},
			CharacterMistakes: []string{"角色可能把旧经验、口头承诺或局部迹象误当成完整事实。"},
			CharacterPretends: []string{"参与者可能因自身目标淡化成本、夸大把握或隐去底线。"},
			HiddenFromReader:  []string{"当前输入尚未揭示的机制、底线、动机与未来关系变化。"},
			RevealCondition:   "只能通过当前项目允许的行动结果、通信、记录、物件变化或角色付出代价后的新证据逐步揭示。",
			TensionFunction:   "防止人物突然全知，让读者持续判断哪条信息可信、什么状态已经改变。",
		}},
		HiddenRules: []domain.HiddenRulePressure{{
			Domain:        "当前场景的资源、责任与结果确认",
			VisibleRule:   "表层条件从当前章节事件、world_rules 和场景资料提取。",
			HiddenRule:    "真正的合作或对抗取决于谁承担失败、谁保留选择权、谁能证明结果。",
			CulturalNorm:  "未由当前项目定义的地域习惯、行业规矩和社会偏见一律不预填。",
			WhoBenefits:   "掌握当前项目关键资源、信息或权限的人",
			WhoPays:       protagonist + " 与实际受选择影响的人",
			ViolationCost: "由当前规则声明的时间、资源、关系、身份、安全或机会成本。",
			SceneEvidence: "从当前场景可见的动作、物件、空间变化、交流记录或当事人反应中选择。",
		}},
		SocialMoodRumors: []domain.SocialMoodRumor{{
			Group:             "当前场景明确存在的群体角色",
			Mood:              "立场随可见结果变化；具体情绪由章节目标、风险与角色卡推出。",
			Rumor:             "只允许传播当前人物能够观察、误读或转述的信息。",
			Source:            "当前场景已建立的目击、通信、公开记录或角色转述。",
			SpreadPath:        "由 book_world、角色关系和场景媒介支持的实际路径。",
			Reliability:       "需要正文证据核验，不能直接作为事实。",
			BehaviorEffect:    "至少改变一个人的行动、判断、站位、关系或资源选择。",
			ProtagonistAccess: "主角只接触亲见或经允许传回的片段，不自动知道群体真实动机。",
		}},
		RitualCalendar: []domain.RitualCalendarWindow{{
			Time:                "第一章开场窗口",
			CalendarType:        "由当前章节事件与世界资料定义的时段或截止窗口",
			RitualOrDeadline:    zeroFirstNonEmpty(project.FirstChapter.Hook, pressure),
			SocialMeaning:       "这个窗口为何迫使当前人物现在选择，必须由角色目标与世界规则说明。",
			PracticalConstraint: "可用时间、地点、资源、权限和沟通机会以当前项目证据为准。",
			EmotionalCharge:     "角色卡中的欲望、恐惧、责任与关系压力会放大误判。",
			MissedCost:          "错过窗口的后果必须由当前大纲或规则明确，不预设固定损失。",
			SceneUse:            scene,
		}},
		StructuralResources: []domain.StructuralResourcePressure{{
			Resource:                  "当前章节完成目标所需的关键资源",
			Controller:                "由角色卡、world_rules 或 book_world 指定的控制者",
			ScarcityReason:            "稀缺原因必须来自当前目标、时限、权限、地点或关系条件。",
			AccessRule:                "满足当前项目已声明的边界，并留下可复核的获得过程。",
			BlackMarketOrInformalPath: "none；只有当前设定明确存在替代路径时才加入。",
			PriceOrCost:               "从当前规则或角色处境选择时间、资源、关系、身份、安全或机会成本。",
			PowerEffect:               "资源的获得、失去或拒绝必须改变人物选择与下一步关系。",
			ChapterPressure:           fmt.Sprintf("%s 必须在“%s”的窗口内作出会改变状态的选择。", protagonist, pressure),
		}},
		CosmologyChecks: []domain.CosmologyRuleCheck{{
			Layer:              "核心机制/现实因果/规则边界",
			Rule:               ruleText,
			Cost:               "任何关键资源、能力、权限或制度效果都必须使用当前项目声明的代价；未声明时标记待规划，不擅自补写。",
			Boundary:           "角色未获得明确资源、权限、证据或他人同意前，不能绕过当前项目的执行条件。",
			ExceptionCondition: "none；例外只能由后续正文触发、支付代价、审核通过并回填台账。",
			Evidence:           "当前 world_rules、world_foundation、book_world、章节大纲和可见行动结果。",
			FailureMode:        "若规则没有边界、失败和结果，正式计划必须先补齐机制而非使用通用模板。",
		}},
		ConflictWeb: []domain.ConflictWebNode{
			{
				Parties:        []string{protagonist, "第一章当前阻力"},
				ConflictType:   "由当前目标、角色关系与规则边界共同定义",
				OpenGoal:       protagonist + " 要推进第一章已声明的目标。",
				HiddenAgenda:   "阻力方的隐藏议程只能来自其角色卡、当前处境或大纲证据。",
				ResourceStake:  "当前事件涉及的资源、权限、关系、身份、信息或安全边界。",
				InformationGap: "各方只掌握各自可见证据，读者跟随当前视角逐步核验。",
				TimePressure:   "在第一章明确窗口内作出有限选择并承担后果。",
				CurrentBalance: "权力与资源分布使用当前角色状态，不预设主角天然弱势或优势。",
				Destabilizer:   pressure,
				NextEscalation: "由本章真实结果产生下一项任务、阻力、关系变化或规则追问。",
			},
			{
				Parties:        []string{protagonist, counterpart},
				ConflictType:   "目标差异/关系试探/边界协商",
				OpenGoal:       "双方各自推进角色卡允许的当前目标。",
				HiddenAgenda:   "认可、怀疑、善意、自保或对抗只能由现有角色动机支持。",
				ResourceStake:  "当前场景证据、信息、信任、时间与协作资格。",
				InformationGap: "双方掌握的事实和职责不同，不能用默契跳过说明与选择。",
				TimePressure:   "问题必须在当前场景窗口内得到阶段回应。",
				CurrentBalance: "关系保持在当前角色卡与关系台账定义的阶段。",
				Destabilizer:   "一次可见结果或守边界的选择会改变双方判断。",
				NextEscalation: "关系只能由后续共同经历、选择与结果逐级变化。",
			},
		},
		TensionMatrix: domain.NarrativeTensionMatrix{
			StabilityTurbulence:     "当前日常被第一章目标与限制打破；主角通过行动改变局部状态，并承担当前规则允许的后果。",
			ExplicitHiddenRules:     "显规则来自当前 world_rules 与场景条件，潜规则只来自角色利益、关系和已支持的社会资料。",
			InformationGap:          "读者跟随当前视角只看见局部；其他角色按各自证据与动机行动。",
			TimePressurePreparation: "倒计时发生在角色准备、资源和关系的当前状态下，不能预装完美通关能力。",
			WhyEventNow:             fmt.Sprintf("当前大纲把“%s”设为第一章发动事件；其必要性必须在正式计划中可验证。", pressure),
			ReaderQuestion:          "下一章应承接本章具体结果、未解信息、关系变化或新增代价。",
			POVBoundary:             fmt.Sprintf("正文不越过主角可见证据；离屏线只能通过当前项目允许的路径传回。相邻地点“%s”不自动与主场景共享信息。", adjacentNode),
		},
	}
}

func zeroInitWorldBackgroundPlan(project zeroInitProject) zeroWorldBackgroundPlan {
	return zeroGroundedWorldBackgroundPlan(project)
}

func zeroInitChapterPlan(project zeroInitProject, dynamics zeroInitCharacterDynamicsDoc, crowd map[string]any, storycraft zeroPrewriteStorycraftPlan, worldBackground zeroWorldBackgroundPlan) domain.ChapterPlan {
	roleList, _ := crowd["roles"].([]domain.CrowdRoleDesign)
	first := project.FirstChapter
	projectPromise := zeroFirstNonEmpty(project.Premise, "第一章必须证明本书核心承诺可持续。")
	if len([]rune(projectPromise)) > 120 {
		projectPromise = string([]rune(projectPromise)[:120])
	}
	goal := "建立主角初始处境、第一项可核验行动和一条能持续推进的目标。"
	conflict := zeroFirstNonEmpty(first.CoreEvent, "角色目标与世界/关系压力第一次相撞。")
	emotionArc := "从难堪、怀疑或资源不足，到主动承担一次选择，再由可见结果换来有限认可和新责任。"
	requiredBeats := []string{
		"证明主角为什么必须现在行动",
		"让当前阻力迫使主角作出不可回避的选择",
		"让交换结果、参与者态度、关系或资源状态发生可回填变化",
	}
	forbiddenMoves := []string{
		"提前解释全书谜底",
		"让角色凭空知道未公开信息",
		"用捧场角色完成关键解谜/救场/反杀",
	}
	emotionTarget := "难堪、不服、试探和做成第一件事后的短暂松气；小胜必须带着下一步责任。"
	hookGoal := "让读者想知道这次结果能否复制、谁会加入或阻拦、下一步要由谁承担成本。"
	sceneAnchors := []string{zeroFirstNonEmpty(zeroFirstSceneForProject(project), "第一章主场景"), "由本章事件产生的可复核证据", "参与者选择与关系变化"}
	chapterFunction := "整本书入口：证明主角行动、当前阻力、可见结果与关系推进能形成长期连载发动机。"
	return domain.ChapterPlan{
		Chapter:    1,
		Title:      zeroFirstNonEmpty(first.Title, "第一章"),
		Goal:       goal,
		Conflict:   conflict,
		Hook:       zeroFirstNonEmpty(first.Hook, "章末留下一个具体物件、选择或新事实作为下一章追问。"),
		EmotionArc: emotionArc,
		Notes:      "这是 zero-init 生成的写前推演草案，不是正式 plan_chapter 落盘。Writer 仍需在 --pipeline 内生成或核对正式 drafts/01.plan.json。",
		Contract: domain.ChapterContract{
			RequiredBeats:    requiredBeats,
			ForbiddenMoves:   forbiddenMoves,
			ContinuityChecks: []string{"零章角色知识边界", "初始关系契约", "资源 pending/booked 状态", "第一章揭示预算"},
			EvaluationFocus:  []string{"人物行动是否由目标和压力推出", "对白是否符合 voice_logic 与 dialogue_scene_blueprints", "审核失败后是否用 review_refinement 重推演"},
			EmotionTarget:    emotionTarget,
			PayoffPoints:     []string{"第一章现场证据改变主角下一步", "章末状态相较开章发生可记录变化"},
			HookGoal:         hookGoal,
			SceneAnchors:     sceneAnchors,
		},
		CausalSimulation: domain.ChapterCausalSimulation{
			ProjectPromise:  projectPromise,
			ChapterFunction: chapterFunction,
			ContextSources: []string{
				"premise", "current_chapter_outline", "characters", "world_rules", "book_world", "meta/user_rules.json",
				"simulation_restart_policy", "world_foundation", "character_dossiers",
				"meta/story_time_contract.json", "meta/story_calendar.json",
				"meta/initial_character_dynamics", "relationship_state.initial", "meta/initial_resource_ledger",
				"foreshadow_ledger.initial", "meta/crowd_role_policy", "meta/prewrite_storycraft_plan", "meta/world_background_plan", "meta/initial_review_lessons",
				"reference_pack.references", "dialogue_writing", "web_reference_guidelines", "meta/web_reference_brief.md 或当轮 web_search 证据",
			},
			WritingNorms:        zeroWritingNorms(project),
			AntiAIPlan:          zeroAntiAIPlan(project),
			ExternalRefs:        zeroExternalReferencePlan(project),
			TrendLanguage:       zeroTrendLanguagePlan(project),
			EntertainmentPlan:   zeroReaderEntertainmentPlan(project),
			GroundingDetails:    zeroGroundingDetails(project),
			OffscreenStage:      zeroOffscreenStage(project, dynamics.Characters),
			LongformOpening:     zeroLongformOpeningDesign(project, first),
			CharacterArcTests:   storycraft.ArcTests,
			ReaderRewardPlan:    storycraft.ReaderReward,
			EvidenceChains:      storycraft.EvidenceChains,
			EndingContract:      storycraft.EndingContract,
			DormantPolicy:       storycraft.DormantPolicy,
			RealitySupport:      storycraft.RealitySupport,
			EmotionalLogic:      storycraft.EmotionalLogic,
			RelationshipArcs:    storycraft.RelationshipArcs,
			VisualDesign:        storycraft.VisualDesign,
			WorldLayers:         worldBackground.Layers,
			InformationLedger:   worldBackground.InformationLedger,
			HiddenRules:         worldBackground.HiddenRules,
			SocialMoodRumors:    worldBackground.SocialMoodRumors,
			RitualCalendar:      worldBackground.RitualCalendar,
			StructuralResources: worldBackground.StructuralResources,
			CosmologyChecks:     worldBackground.CosmologyChecks,
			ConflictWeb:         worldBackground.ConflictWeb,
			TensionMatrix:       worldBackground.TensionMatrix,
			InitialState:        dynamics.Characters,
			VoiceLogic:          dynamics.VoiceLogic,
			DialogueBlueprints:  storycraft.DialogueBlueprints,
			CrowdRoles:          roleList,
			ReviewRefinement:    zeroReviewRefinement(),
			EnvironmentState:    zeroEnvironmentState(project),
			WorldRulesInForce:   zeroWorldRuleTexts(project.WorldRules, 4),
			InformationGaps:     zeroChapterInformationGaps(project),
			CausalBeats:         []domain.CausalSimulationBeat{zeroChapterCausalBeat(project)},
			DecisionPoints:      zeroChapterDecisionPoints(project),
			OutcomeShift:        zeroChapterOutcomeShift(project),
			SceneConstraints:    []string{"第三方/群体角色不能替主角完成关键判断", "环境信息必须通过可见物件、动作或规则后果呈现", "第一章不提前解释大谜底"},
		},
	}
}

func zeroLongformOpeningDesign(project zeroInitProject, first domain.OutlineEntry) domain.LongformOpeningDesign {
	protagonist := zeroFirstNonEmpty(zeroProtagonist(project.Characters).Name, "主角")
	return domain.LongformOpeningDesign{
		TargetReader:     "想看人物在明确约束里推进具体目标，并由选择后果持续打开人物、关系与世界的读者。",
		OpeningHook:      zeroFirstNonEmpty(first.Hook, first.CoreEvent, first.Title),
		SerialEngine:     "角色目标、执行条件、资源缺口、参与者利益和可见结果互相推动；每章完成一个可核验变化，同时产生下一步责任。",
		ReaderRewardLoop: []string{"看见具体难题与当前阻力", "角色判断不全但开始行动", "信息、资源、环境或关系出现可见变化", "结果带来新参与者、新责任或下一步机会"},
		LongRangePromises: []domain.LongRangePromise{{
			Promise:          fmt.Sprintf("%s会从只能处理眼前难题，成长为能稳定组织资源、承担后果并让成果持续的人。", protagonist),
			FirstChapterSeed: zeroFirstNonEmpty(first.Hook, first.CoreEvent),
			PayoffHorizon:    "首个小弧兑现一次可复核的小成果，后续通过规模、协作和责任升级长线目标。",
		}},
		RevealBudget:      []string{"第一章只交代当前行动必需的身份、目标、责任、限制与阻力", "未亲见、未通信、未形成记录的信息不进入主角视角"},
		FirstChapterProof: []string{"主角有必须处理的当前目标", "至少一次不完美但有效的行动改变现场", "结果与成本都能被参与者和读者复核"},
		RetentionRisks:    []string{"开局只讲设定或方法", "配角无条件配合", "抽象数值替代真实结果", "章末只做总结不产生下一步责任"},
	}

}

func zeroChapterInformationGaps(project zeroInitProject) []string {
	return []string{"当前目标的完整执行条件", "各参与者尚未说出的现实顾虑", "资源和责任最终由谁承担", "本章结果能否持续或复制", "下一步机会附带的具体成本"}

}

func zeroChapterCausalBeat(project zeroInitProject) domain.CausalSimulationBeat {
	return domain.CausalSimulationBeat{
		Cause:           zeroFirstNonEmpty(project.FirstChapter.CoreEvent, project.FirstChapter.Title, "第一章出现一个必须处理的目标与行动阻力。"),
		CharacterChoice: "主角先核对目标、责任、证据和可执行条件，再做一次不完美但能落地的选择。",
		WorldResponse:   "参与者、环境、规则载体或公开反馈给出当前项目支持的可见反应。",
		StoryResult:     "主角完成一个可复核变化，同时承担时间、返工、关系、资源或下一步责任。",
	}

}

func zeroChapterDecisionPoints(project zeroInitProject) []string {
	return []string{"是否先做一项可核验的小行动", "是否接受当前责任、资源与行动边界", "是否向参与者公开尚未解决的风险", "是否为结果承担当前规则支持的成本", "是否把本章结果转成下一步明确任务"}

}

func zeroChapterOutcomeShift(project zeroInitProject) []string {
	return []string{"主角的下一步目标与责任更具体", "至少一项信息、环境、资源、权限或关系状态发生可复核变化", "本章结果带来下一位参与者、下一项任务或新的明确成本"}

}

func zeroWritingNorms(project zeroInitProject) []domain.WritingNormApplication {
	return []domain.WritingNormApplication{
		{
			Source:             "writing_engine/user_rules",
			RuleFocus:          []string{"章节契约先定义写什么", "字数和禁用项服从项目规则", "第一章必须证明长期行动引擎"},
			ChapterApplication: "把主角目标、主动阻力、不可回避的选择和现场结果落成 scene_anchors 与 causal_beats；手续只保留会改变责任、关系或结果的节点。",
			ProofTargets:       []string{"开场现实压力", "一次主动选择", "可见结果与 outcome_shift"},
			FailureRisk:        "只写流程、金额或旁人吹捧，主角没有真实选择，结果也无法核验。",
		},
		{
			Source:             "anti_ai_tone/human_feel_craft/writing_techniques_digest",
			RuleFocus:          []string{"少抽象概括", "具体物件和现场反应承担新信息", "对白有目的和潜台词", "章尾不用金句问号"},
			ChapterApplication: "用付款、物料、位置、人流、动作失误、商户/顾客反应承载压力；连续判断或对白后必须切回可见行动。",
			ProofTargets:       []string{"scene_anchors", "voice_logic.dialogue_functions", "environment_state"},
			FailureRisk:        "解释段、整齐清单、汇报腔和同声同气的对白制造明显模板感。",
		},
		{
			Source:             "dialogue_writing",
			RuleFocus:          []string{"先选 dialogue_mode", "每个角色都有 objective_tactics", "权力和信息差会变化", "沉默、误读、反制和情绪泄露可见"},
			ChapterApplication: "关键对白先明确谁要什么、谁承担风险、什么证据会改变立场，再决定对白/动作/物件/沉默谁先入场。",
			ProofTargets:       []string{"dialogue_scene_blueprints", "voice_logic", "character_stage_records"},
			FailureRisk:        "所有角色像同一个项目经理，对话变成政策、教程或审核记录。",
		},
		{
			Source:             "web_reference_guidelines",
			RuleFocus:          []string{"网络资料只做现实支架", "经营和地方生活细节需可追溯", "热梗必须有角色载体与预算"},
			ChapterApplication: "把营业、支付、运输、安装、顾客等待、地方传播和角色职业细节转成现场动作与选择成本，不搬运网页摘要。",
			ProofTargets:       []string{"external_reference_plan", "trend_language_plan", "grounding_details"},
			FailureRisk:        "行业教程、热词硬贴、交通与交付无耗时，导致悬浮和 AI 味。",
		},
	}

}

func zeroAntiAIPlan(project zeroInitProject) domain.AntiAIExecutionPlan {
	return domain.AntiAIExecutionPlan{
		RiskSignals: []string{"第一章像项目汇报或教程", "角色口吻变成作者总结", "金额与流程解释过整齐", "连续对话每句都推进信息", "章末抽象金句或问号"},
		CounterMoves: []string{
			"把压力拆进付款、物料、位置、人流、手机消息、顾客去留和角色手上动作",
			"让角色先误判、犹豫或承担一次损失，再通过可见行动修正",
			"经营信息只露出会改变当下选择的一小段，不写成行业说明",
			"章末落到真实结果、责任变化、参与者态度或下一步执行条件",
		},
		SentenceRhythmPolicy: "长短句按难堪、催促、执行和结果反馈换挡；抽象判断后必须回到动作、物件、声音、对白或选择后果。",
		ObjectResponseBudget: "屏幕/票据/物料/现场标识回应默认不超过4次，至少一次延迟、遮挡或被人物行动打断。",
		DialogueFunctionPlan: "对白只承担试探、议价、拒绝、让步、提醒、撑腰和关系温度；禁止人物突然讲懂整套经营方法。",
		ReviewChecks:         []string{"是否有整齐三连/清单感", "是否用现场动作承载压力", "删掉说话人后角色声口是否仍成立", "章尾是否具体而非金句", "结果是否真实可核验"},
	}

}

func zeroExternalReferencePlan(project zeroInitProject) []domain.ExternalReferencePlan {
	return []domain.ExternalReferencePlan{{
		QueryOrNeed:          "第一章需要地方经营、支付、场地、运输、顾客行为、角色职业或本地传播现实支架时，正式计划必须引用项目 web_reference_brief 的 retrieved_at，或记录当轮 web_search 证据。",
		SourceType:           "project_web_reference_brief",
		SourceRefs:           []string{"reference_pack.references.web_reference_guidelines", "meta/web_reference_brief.md", "selected_memory.rag_recall(若命中相关资料)"},
		RetrievedAt:          "正式计划读取 meta/web_reference_brief.md 后填写具体日期；若简报缺失则先检索并记录当轮日期",
		FreshnessRequirement: "平台、价格和地方经营规则优先使用最新资料；稳定生活与运输常识可使用近一年资料。",
		UsableDetails:        []string{"营业/收摊与排队节奏", "支付、退款、价格和交付凭证", "运输、安装与本职工作耗时", "地方群聊、短视频和熟人转述"},
		TransformationRule:   "转成角色能看见、听见、操作和为之付费的动作、物件、声音、队列或误判，不把网页摘要搬进旁白。",
		DoNotUse:             []string{"无来源热梗串", "过时梗硬贴", "平台政策/价格等未核实事实", "把经营写成教程或新闻稿"},
	}}

}

func zeroTrendLanguagePlan(project zeroInitProject) []domain.TrendLanguagePlan {
	return []domain.TrendLanguagePlan{{
		Item:             "none-or-project-brief-item",
		SourceContext:    "项目 web_reference_brief 的具体条目或正式写作时的最新网络检索结果",
		CharacterCarrier: "默认由顾客、商户、朋友、群聊、短视频评论或配角半句反应承载，不由旁白承载",
		SceneFunction:    "只做时代纹理、笑点、误判或关系摩擦，不解释经营机制",
		UsageBudget:      "第一章默认0-1处，最多2处半截反应；禁止梗串",
		ForbiddenUsage:   "主角关键判断、系统保密信息、章末钩子、作者旁白和总结金句里不用热梗",
	}}

}

func zeroReaderEntertainmentPlan(project zeroInitProject) domain.ReaderEntertainmentPlan {
	protagonist := zeroFirstNonEmpty(zeroProtagonist(project.Characters).Name, "主角")
	event := zeroFirstNonEmpty(project.FirstChapter.CoreEvent, project.FirstChapter.Title, "第一章核心事件")
	return domain.ReaderEntertainmentPlan{
		OpeningBeat:          fmt.Sprintf("前200字让%s在“%s”的具体现场遭遇阻力，并当场做出不完美但主动的回应。", protagonist, event),
		HumorBeats:           []string{"幽默只能来自角色卡允许的偏见、误读或关系反应", "笑点必须改变当下关系、判断或行动，不能靠通用小动作填充"},
		ImmediatePayoffs:     []string{"本章中段前出现一次读者可见的小胜或能力验证", "章末前让信息、资源、面子、关系或权限至少再改变一次"},
		ProcedureCompression: "流程只保留会改变责任、证据、选择或关系的节点，其余一句带过。",
		CompanionVoiceBeat:   "若角色卡或用户规则定义了搭档/陪伴角色，由其用有立场的短回应承载陪伴，但不替主角做决定。",
		ForbiddenComedy:      []string{"梗串", "旁白硬贴热词", "配角集体降智", "在章末用热梗代替追读后果"},
	}
}

func zeroGroundingDetails(project zeroInitProject) []domain.GroundingDetailPlan {
	scene := zeroFirstNonEmpty(zeroFirstSceneForProject(project), "第一章主场景")
	return []domain.GroundingDetailPlan{
		{
			Detail:        "付款、物料、位置、安装和顾客等待先像真实经营现场，再承担剧情压力。",
			SourceRef:     "web_reference_guidelines",
			TransformedAs: "收款提示、价牌、线材/桌面、车辆装卸、排队去留、商户手上动作和明确交接",
			SceneAnchor:   scene,
		},
		{
			Detail:        "县城生活与熟人传播只做人物选择的现实成本，不抢主线解释。",
			SourceRef:     "web_reference_guidelines",
			TransformedAs: "亲友群消息、熟人半句打听、摊主观望、顾客改口、收摊时间和往返交通",
			SceneAnchor:   "主角能亲见、听见、收到或复核的生活细节",
		},
	}

}

func zeroOffscreenStage(project zeroInitProject, states []domain.CharacterSimulationState) []domain.CharacterStageRecord {
	scene := zeroFirstNonEmpty(zeroFirstSceneForProject(project), "第一章主场景")
	var records []domain.CharacterStageRecord
	for _, state := range states {
		if strings.TrimSpace(state.Character) == "" {
			continue
		}
		firstChapterActive := false
		firstMention := project.FirstMentions[strings.TrimSpace(state.Character)]
		for _, c := range project.Characters {
			if zeroCharacterNameIs(c, state.Character) {
				firstChapterActive = zeroFirstChapterCharacterActive(project, c)
				break
			}
		}
		location := scene
		status := "角色状态待第一章正文确认"
		environment := zeroFirstNonEmpty(project.FirstChapter.CoreEvent, "第一章行动/关系/资源压力现场")
		deathState := "无预设极端状态；若发生缺席、状态变化、关系降温或资源损失，必须安排传回主角路径"
		notice := "通过正文可见行动、通信、可复核记录、现场结果、目击者或后续台账传回主角"
		transport := "按开局地点原地行动；跨地点需 book_world 路线"
		travelTime := "未发生跨地点移动时为0；若正文移动，必须补现实耗时"
		meetingConstraint := "主角未通信/未见证/无证据时不能知道该角色线；非主角不能随叫随到"
		timelineConsistency := "与第一章主线同步；若正文未展示，后续回归必须承接此处压力和误判。"
		if !firstChapterActive {
			location = "离屏/未定；未进入第一章现场"
			if firstMention > 1 {
				location = fmt.Sprintf("离屏/未定；第%d章首次入场前不得占用第一章现场", firstMention)
			}
			environment = "第一章同时段的个人生活、工作、日程与资源环境；不共享主角现场信息"
			transport = "未前往第一章现场；首次入场前若跨地点，必须另补路线与现实耗时"
			travelTime = "离屏阶段待定；不得用零耗时把角色送入第一章现场"
			meetingConstraint = "本章未相识/未联系/未同场；只有首次出场边界到达且正文建立渠道后才能接触主角"
			timelineConsistency = "与第一章处在同一时间轴但保持离屏；后续首次入场只承接个人基线，不得倒灌第一章现场知识。"
		}
		records = append(records, domain.CharacterStageRecord{
			Chapter:             1,
			Character:           state.Character,
			Time:                "第一章开场至章末",
			Location:            location,
			Status:              status,
			Environment:         environment,
			CurrentAction:       zeroFirstNonEmpty(state.LikelyAction, "按当前信息试探并保护自身边界"),
			Pressure:            zeroFirstNonEmpty(state.Pressure, "第一章压力尚未明确"),
			Decision:            "只基于可见证据做有限选择，不提前掌握完整机制。",
			MistakeOrMisbelief:  zeroFirstNonEmpty(strings.Join(state.PlausibleMistakes, "；"), "可能把异常误判成普通流程。"),
			KnowledgeBoundary:   zeroFirstNonEmpty(strings.Join(state.KnowledgeLedger.ForbiddenKnowledge, "；"), "不能知道未公开后台谜底。"),
			VisibleInChapter:    firstChapterActive,
			Evidence:            "zero-init 初始角色动态与第一章大纲",
			Transport:           transport,
			TravelTime:          travelTime,
			MeetingConstraint:   meetingConstraint,
			PersonalityDelta:    "第一章压力会测试其行动倾向，变化需在 commit 后回填",
			DeathState:          deathState,
			ProtagonistNotice:   notice,
			TimelineConsistency: timelineConsistency,
			NextPotential:       "携带本章未解决的压力、误判或资源缺口回归。",
			Tags:                []string{"zero_init", "character_stage", state.Character},
		})
	}
	if len(records) == 0 {
		environment := zeroFirstNonEmpty(project.FirstChapter.CoreEvent, "第一章主压力")
		currentAction := "在规则压力下做有限选择。"
		mistake := "可能误判规则代价。"
		knowledge := "不知道完整后台机制。"
		records = append(records, domain.CharacterStageRecord{
			Chapter:             1,
			Character:           "关键角色",
			Time:                "第一章开场至章末",
			Location:            scene,
			Environment:         environment,
			CurrentAction:       currentAction,
			Pressure:            "第一章核心事件",
			Decision:            "不预装答案，只按证据修正。",
			MistakeOrMisbelief:  mistake,
			KnowledgeBoundary:   knowledge,
			Transport:           "原地/待补",
			TravelTime:          "待补",
			MeetingConstraint:   "待补；正式 plan_chapter 必须写清",
			PersonalityDelta:    "待补",
			DeathState:          "待确认",
			ProtagonistNotice:   "待补",
			TimelineConsistency: "作为后续正式 plan_chapter 的占位，需由 Writer 补成具体角色。",
			NextPotential:       "正式写作时补全。",
			Tags:                []string{"zero_init", "placeholder"},
		})
	}
	return records
}

func zeroReviewRefinement() domain.ReviewRefinementLoop {
	return domain.ReviewRefinementLoop{
		TriggerSources:      []string{"reviews/01.md", "reviews_ai/01.json", "check_consistency", "commit_chapter.aigc_report"},
		FailureModes:        []string{"角色行动不由目标/压力推出", "对白不符合 voice_logic", "RAG/context_sources 不足", "捧场角色越权", "环境只做氛围不承载信息"},
		LocalizedTargets:    []string{"知识账本", "决策框架", "关系契约", "声口逻辑", "场景承载物"},
		PreserveConstraints: []string{"章节大纲核心事件", "已成立的资源/关系基线", "用户规则", "写法资产中已启用禁忌"},
		ReplanningMoves:     []string{"按审核结论重建 initial_state", "调整 voice_logic 的知识边界和对白功能", "把抽象问题改成具体物件/动作/关系代价"},
		AcceptanceChecks:    []string{"每个关键选择都有证据来源", "每个关键对白有场景目的", "章末状态变化可回填"},
		StopCondition:       "同一失败原因连续两轮仍未解决时，停止整章凭感觉重写，转为局部 edit 或上游规划修正。",
		IterationLimit:      2,
	}
}

func zeroEnvironmentState(project zeroInitProject) []domain.EnvironmentSignal {
	return []domain.EnvironmentSignal{{
		Place:              zeroFirstNonEmpty(zeroFirstSceneForProject(project), "第一章主场景"),
		VisibleState:       "价格、票据、物料、位置、排队、交接、人员动作或消息记录中至少一项可见且会变化。",
		InformationCarried: "承载当前目标的用途、执行条件、责任边界、参与者顾虑与资源缺口。",
		PressureApplied:    "迫使角色在有限时间和资源下作出一次可执行选择，并明确谁承担成本。",
		ExpectedChange:     "章末同一地点的付款、交付、物料、位置、人员态度或下一步安排发生可复核变化。",
	}}

}
