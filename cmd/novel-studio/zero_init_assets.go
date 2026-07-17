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

func zeroIsSecondAlgorithmProject(project zeroInitProject) bool {
	// This compatibility profile owns concrete characters, places and opening
	// beats. It must be selected by project identity, never inferred from prose
	// tokens such as a company name that another book may legitimately reuse.
	return strings.TrimSpace(project.Name) == "她的第二算法"
}

func zeroIsCountySpendProject(project zeroInitProject) bool {
	// This is a project-owned compatibility profile, not a genre heuristic.
	// Semantic matching previously injected this book's names and opening scene
	// into any county/system story with similar words.
	return strings.TrimSpace(project.Name) == "只许把钱花在青山县"
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
	desc := zeroFirstNonEmpty(c.Description, "角色卡未写明细节，第一章必须用行动补证。")
	arc := zeroFirstNonEmpty(c.Arc, "从静态设定进入可追踪的行动变化。")
	actionBias := zeroActionBias(c)
	second := zeroIsSecondAlgorithmProject(project)
	firstChapterActive := zeroFirstChapterCharacterActive(project, c)
	// 主角是关系枢纽，应对每个关键配角都有契约；非主角对主角有契约。
	// 旧逻辑只给主角找单个 FirstCast 对手，第一章大纲没点名匹配时主角契约为空。
	counterparts := zeroCounterpartsForCharacter(project, c)
	relationshipForces := []string{"当前章的主要牵引来自现场规则、资源压力和可见证据。"}
	if second {
		relationshipForces = []string{"当前章的主要牵引来自职场处境、岗位资源、关系边界和可见证据。"}
	}
	relationshipContracts := []domain.CharacterRelationshipContract{}
	if len(counterparts) > 0 {
		relationshipForces = relationshipForces[:0]
		for _, cp := range counterparts {
			force := fmt.Sprintf("与%s的信任、债务或信息差必须在行动中体现。", cp)
			debt := "无新增债务，第一章若发生交换必须入账。"
			if second {
				force = fmt.Sprintf("与%s的信任、亏欠、边界或信息差必须在行动中体现。", cp)
				debt = "无新增亏欠或承诺，第一章若发生帮助/交换必须留下机会成本。"
			}
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
				FearSource:        zeroFirstNonEmpty(zeroSecondFearSource(second), "害怕失去目标、资源、身份或关系边界。"),
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
			if second {
				relationshipContracts[i].Debt = "零章不新增亏欠或承诺；首次入场后的实际帮助或交换再入账。"
			} else {
				relationshipContracts[i].Debt = "零章不新增债务；首次入场后的实际交换再入账。"
			}
			relationshipContracts[i].Dependency = "离屏阶段不新增依赖；首次入场前不得凭空建立现场协作。"
			relationshipContracts[i].HelpCondition = "必须等首次联系或入场条件成立，并有可见证据、明确交换或情感压力触发。"
		}
	}
	misbeliefs := []string{"开章时可能误判第一章异常/压力的真实代价，正文需用证据修正。"}
	plausibleMistakes := []string{
		"把异常先误认为普通流程、物业/合同/人情压力",
		"过度依赖旧经验导致判断过窄",
		"在压力下做出一次迟疑、错判或过度自保",
	}
	correctionTriggers := []string{
		"可见物件或规则反馈打破旧经验",
		"他人犯错带来可复核代价",
		"关系/资源损失迫使其修正判断",
	}
	unknownFacts := []string{"第一章世界规则的完整代价", "其他角色的真实意图", "章末钩子背后的答案"}
	if second {
		misbeliefs = []string{"开章时可能误判AI提效或岗位变化的真实代价，正文需用证据修正。"}
		plausibleMistakes = []string{
			"把不舒服先误认为普通流程、上级话术或自己太敏感",
			"过度依赖旧工作经验导致判断过窄",
			"在压力下做出一次迟疑、错判或过度自保",
		}
		correctionTriggers = []string{
			"会议材料、客户反馈或消息截断打破旧经验",
			"他人犯错带来可复核代价",
			"关系/资源损失迫使其修正判断",
		}
		unknownFacts = []string{"AI提效项目的真实取舍", "其他角色的真实意图", "章末后果背后的下一步代价"}
	}
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
			zeroSkillLimitWorldMechanism(second),
			"不能稳定判断所有收益和代价",
			"不能替其他角色提前知道后台秘密",
		},
		PlausibleMistakes:  plausibleMistakes,
		CorrectionTriggers: correctionTriggers,
		KnowledgeLedger: domain.CharacterKnowledgeLedger{
			KnownFacts:         []string{fmt.Sprintf("自己是%s；角色卡基础描述：%s", role, desc)},
			UnknownFacts:       unknownFacts,
			Suspicions:         []string{"现场异常或关系压力不会无代价解除。"},
			FalseBeliefs:       []string{"以为只靠旧经验就能处理第一章问题。"},
			EvidenceSeen:       evidenceSeen,
			Confidence:         "zero_chapter_baseline",
			SourceChapter:      0,
			ForbiddenKnowledge: []string{"未在角色卡、大纲、世界规则或 RAG 召回中出现的谜底与后台设定。"},
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
			RelationshipEffect:   zeroRelationshipEffectLine(second),
		},
		ArcAxis: domain.CharacterArcAxis{
			Want:             zeroFirstNonEmpty(c.Arc, "拿到眼前问题的控制权。"),
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

func zeroCurrentGoal(project zeroInitProject, c domain.Character, role string) string {
	name := zeroFirstNonEmpty(c.Name, role, "该角色")
	if !zeroFirstChapterCharacterActive(project, c) {
		if mention := project.FirstMentions[strings.TrimSpace(c.Name)]; mention > 1 {
			return fmt.Sprintf("%s在第%d章前沿自己的生活线行动，首次入场时以%s的现实目标影响主线，不提前挤进开篇。", name, mention, role)
		}
		return fmt.Sprintf("%s在首次正式入场前维持自己的%s生活线，不得为了人物覆盖提前进入第一章现场。", name, role)
	}
	if zeroIsCountySpendProject(project) {
		switch {
		case zeroCharacterNameIs(c, "林澈"):
			return "顶住返乡饭桌上的难堪，确认异常资金不会连累家人，并在当晚完成一笔真能帮到人的县内消费。"
		case zeroCharacterNameIs(c, "沈知遥"):
			return "守住夜市安全和商户利益，同时判断林澈这笔来路异常、用途却真实的钱到底能不能经得起查。"
		case zeroCharacterNameIs(c, "贺骁"):
			return "先把汽修店的活交稳，再决定要不要借车、出力，帮刚返乡的林澈把第一件事办成。"
		case zeroCharacterNameIs(c, "周曼"):
			return "别让亲戚把儿子逼得下不来台，也要看清林澈是真有下一步，还是只在饭桌上硬撑。"
		case zeroCharacterNameIs(c, "林建国"):
			return "护住儿子的体面又不替他吹牛，等林澈拿一件做成的事回来再表态。"
		case zeroCharacterNameIs(c, "马玉芬"):
			return "不替陌生人的热心买单，先确认灯线安全、坏了有人管，再看摊上能不能真多几单生意。"
		}
	}
	return fmt.Sprintf("%s要以%s的立场，在第一章核心事件里做出一个可验证、会留下后果的选择。", name, role)
}

func zeroSecondFearSource(second bool) string {
	if second {
		return "害怕失去目标、岗位入口、专业价值或关系边界。"
	}
	return ""
}

func zeroSkillLimitWorldMechanism(second bool) string {
	if second {
		return "不知道AI提效和岗位调整的完整取舍"
	}
	return "不知道第一章世界规则完整机制"
}

func zeroRelationshipEffectLine(second bool) string {
	if second {
		return "选择会改变至少一条信任、亏欠、恐惧、依赖或边界记录。"
	}
	return "选择会改变至少一条信任、债务、恐惧或依赖记录。"
}

func zeroInitVoiceLogic(project zeroInitProject, c domain.Character) domain.CharacterVoiceLogic {
	return domain.CharacterVoiceLogic{
		Character:          c.Name,
		PersonalitySource:  zeroFirstNonEmpty(strings.Join(c.Traits, "、"), c.Description, c.Role),
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
	if zeroIsCountySpendProject(project) {
		switch {
		case zeroCharacterNameIs(c, "林澈"):
			return "饭桌上先接住亲戚的难听话，离桌后认真核验一百万的风险；当晚必须把第一笔钱花成摊主和顾客都看得见的改善。"
		case zeroCharacterNameIs(c, "沈知遥"):
			return "到夜市先看走线和商户风险，挡掉不合规的做法；再判断林澈是在撒钱作秀，还是确实能把钱花到实处。"
		case zeroCharacterNameIs(c, "贺骁"):
			return "接到林澈借皮卡的电话时先问清拉什么、谁出油钱，嘴上损两句，行动上给出明早能落地的答复。"
		case zeroCharacterNameIs(c, "周曼"):
			return "在接风饭上给儿子留台阶，不和亲戚正面吵翻；从林澈离桌后的反应判断他是不是又把难受憋回去了。"
		case zeroCharacterNameIs(c, "林建国"):
			return "用最少的话截住亲戚继续比较，既不替林澈编前程，也不让儿子在自家饭桌上被当成笑话。"
		case zeroCharacterNameIs(c, "马玉芬"):
			return "先问坏了找谁、会不会耽误做生意，再亲眼看顾客是否因灯和价牌停下来；没有实际好处就不点头。"
		case zeroCharacterNameIs(c, "梁广财"):
			return "借熟人关心探林澈的失业和去向，话说出口后还想保住自己只是好心的体面。"
		}
	}
	if zeroIsSecondAlgorithmProject(project) {
		switch {
		case zeroIsPrimaryProtagonist(project, c):
			return "先把发布会现场撑住，再确认自己的经验为什么被系统当成可合并资产；章末必须拒绝一个无异议承诺。"
		case zeroCharacterNameIs(c, "梁渡"):
			return "从外部旁观者位置判断这套提效叙事的真实代价；不救场，只留下一个让许闻溪无法继续圆场的问题。"
		case zeroCharacterNameIs(c, "夏岚"):
			return "把发布会事故压回可控范围，同时判断许闻溪还能不能继续承担组织需要她承担的脏活。"
		case zeroCharacterNameIs(c, "傅行简"):
			return "维护AI提效项目的漂亮叙事，把岗位合并说成流程升级，并迫使许闻溪配合现场说法。"
		case zeroCharacterNameIs(c, "程棠"):
			return "确认自己是不是被合并名单推出去的人，并从许闻溪那里等一个不敷衍的态度。"
		case zeroCharacterNameIs(c, "乔安"):
			return "在流程允许范围内保护自己，同时给许闻溪留下一点能被追溯的真实风险。"
		case zeroCharacterNameIs(c, "邱梅"):
			return "用生活化的担心牵住许闻溪，让她看见上一代女性被低价消耗后的旧路。"
		case zeroCharacterNameIs(c, "陆敏"):
			return "用门店现场的真实损耗反驳办公室提效话术，逼许闻溪承认被替代的是具体经验。"
		case zeroCharacterNameIs(c, "韩璐"):
			return "把客服一线被AI压缩后的疲惫带进台面，让工单背后的人不再只是成本项。"
		case zeroCharacterNameIs(c, "陈思予"):
			return "远端观察许闻溪的方法是否能变成可交付服务，并提前计算桥点能承受的合作边界。"
		case zeroCharacterNameIs(c, "周临", "陈砚青"):
			return "稳住高层对外叙事，评估许闻溪的反应会不会影响组织推行岗位压缩。"
		}
	}
	return fmt.Sprintf("%s在“%s”中只按自己的已知信息行动，用一次选择、拒绝或交换留下可追踪后果。", zeroFirstNonEmpty(c.Name, c.Role, "该角色"), event)
}

func zeroSpeechPrinciple(project zeroInitProject, c domain.Character) string {
	roleText := c.Role + " " + strings.Join(c.Traits, " ")
	if zeroIsCountySpendProject(project) {
		switch {
		case zeroCharacterNameIs(c, "林澈"):
			return "嘴上先把场面接住，真到要扛事时少说两句、直接动手；吐槽像当下反应，不拿金句替决定。"
		case zeroCharacterNameIs(c, "沈知遥"):
			return "对外先说边界和后果，转向林澈时语气软半格；不用官样长句，也不靠冷脸人设替代判断。"
		case zeroCharacterNameIs(c, "贺骁"):
			return "熟人话和短句多，损友式补刀之后马上问具体活；笑归笑，答应的事给准时间。"
		case zeroCharacterNameIs(c, "许牧"):
			return "先拆需求和漏洞，再用一句低温吐槽收尾；技术词只说到现场的人听得懂。"
		case zeroCharacterNameIs(c, "叶南栀"):
			return "先接住场面和镜头，再用调侃戳破闺蜜嘴硬；说流量时必须落到谁在看、为什么转。"
		case strings.Contains(c.Role, "父亲"):
			return "不擅长软话，通常先挑毛病再默默补位；真护儿子时只说半句，不做长篇父爱独白。"
		case strings.Contains(c.Role, "母亲"):
			return "从菜、钱和街坊小事说起，担心重时反而把话说轻；唠叨里要有具体生活账。"
		case strings.Contains(c.Role, "商户") || strings.Contains(c.Role, "合作社"):
			return fmt.Sprintf("%s先问价格、责任和能不能真卖出去；县城口语直来直去，不替作者讲经营方法论。", zeroFirstNonEmpty(c.Name, c.Role))
		}
	}
	if zeroIsSecondAlgorithmProject(project) {
		switch {
		case zeroIsPrimaryProtagonist(project, c):
			return "先接住现场，再在过不去的确认上停下来；不讲大道理，用短句和动作暴露她从圆场到拒绝的变化。"
		case zeroCharacterNameIs(c, "梁渡") || strings.Contains(c.Role, "男主"):
			return "先问真实代价，再给判断；话短、准、有边界，不替许闻溪决定出路。"
		case zeroCharacterNameIs(c, "傅行简") || strings.Contains(roleText, "项目负责人"):
			return "把不安包装成项目收益和组织口径；越心虚越使用中性词、流程词和漂亮指标。"
		case zeroCharacterNameIs(c, "夏岚") || strings.Contains(roleText, "上级"):
			return "先压住局面，再留一点人情余地；说话像管理者，也露出被组织消耗过的疲惫。"
		case zeroCharacterNameIs(c, "程棠") || strings.Contains(roleText, "成长对象"):
			return "先用玩笑和快话遮慌，遮不住时直接问以后怎么办；不只等许闻溪拯救。"
		case zeroCharacterNameIs(c, "乔安") || strings.Contains(roleText, "HR"):
			return "先说流程允许的话，再用停顿和半句露出真实提醒；每次帮忙都给自己留退路。"
		case zeroCharacterNameIs(c, "邱梅") || strings.Contains(roleText, "母亲"):
			return "先劝女儿别硬碰，话里藏着上一代女性的忍耐和体面；不直接讲道理。"
		case zeroCharacterNameIs(c, "罗湘", "陆敏") || strings.Contains(roleText, "门店"):
			return "先质疑办公室话术，再拿门店现场反问；语言要有烟火气和被低估后的硬度。"
		case zeroCharacterNameIs(c, "孟嘉仪", "韩璐") || strings.Contains(roleText, "客服"):
			return "先把客户现场和工单压力说清，再露出被工具压缩后的疲惫；不替系统背锅。"
		case zeroCharacterNameIs(c, "周蕴", "陈思予") || strings.Contains(roleText, "课程运营") || strings.Contains(roleText, "社群"):
			return "先谈交付和价格，再谈善意；欣赏许闻溪，但每一句认可都带商业验证条件。"
		case zeroCharacterNameIs(c, "陈砚青", "周临") || strings.Contains(roleText, "高层"):
			return "先看盘子和风险，再决定是否给资源；话里少情绪，多边界和可交换条件。"
		}
	}
	return fmt.Sprintf("%s说话先服从%s的处境和已知信息，再让“%s”的性格从用词与反应速度里露出来。", zeroFirstNonEmpty(c.Name, "该角色"), zeroFirstNonEmpty(c.Role, "当前身份"), zeroFirstNonEmpty(strings.Join(c.Traits, "、"), "既有性格"))
}

func zeroHiddenSubtext(project zeroInitProject, c domain.Character) string {
	roleText := c.Role
	if !zeroFirstChapterCharacterActive(project, c) {
		return fmt.Sprintf("%s在离屏阶段先守住%s的现实利益、关系与日程；未正式入场前不对第一章现场作出反应。", zeroFirstNonEmpty(c.Name, "该角色"), zeroFirstNonEmpty(c.Role, "当前角色"))
	}
	if zeroIsCountySpendProject(project) {
		switch {
		case zeroCharacterNameIs(c, "林澈"):
			return "他怕父母因自己失业被人看低，也怕来得太容易的钱最后让家里替他收拾残局。"
		case zeroCharacterNameIs(c, "沈知遥"):
			return "她对林澈的异常来路已经起疑，却更在意他会不会拿普通商户试错；关心要藏在追问和补位里。"
		case zeroCharacterNameIs(c, "贺骁"):
			return "他担心林澈失业后憋出事，嘴上越损，越说明这趟忙他已经打算帮到底。"
		case zeroCharacterNameIs(c, "周曼"):
			return "她怕儿子在外面受了委屈不肯说，也怕自己护得太急反而让亲戚更看笑话。"
		case zeroCharacterNameIs(c, "林建国"):
			return "他既恼儿子失业，也不允许外人在自家桌上踩儿子；支持会先落在行动上。"
		case zeroCharacterNameIs(c, "马玉芬"):
			return "她不是不想生意变好，只怕好处还没见着，坏灯、赔钱和投诉先落到自己头上。"
		}
	}
	switch {
	case zeroIsSecondAlgorithmProject(project) && zeroIsPrimaryProtagonist(project, c):
		return "她怕一拒绝就失去位置，也怕不拒绝就继续把自己变成好用的人。"
	case zeroCharacterNameIs(c, "梁渡") || strings.Contains(c.Role, "男主"):
		return "他怕自己又把别人当案例，所以宁愿话不好听，也不轻易许诺。"
	case zeroCharacterNameIs(c, "傅行简"):
		return "他要保住项目正确性，不愿承认提效叙事正在吞掉具体的人。"
	case zeroCharacterNameIs(c, "夏岚"):
		return "她既欣赏许闻溪，也要她服从组织口径，疲惫藏在强硬后面。"
	case zeroCharacterNameIs(c, "程棠"):
		return "她怕自己第一个被推出去，又怕许闻溪只是安慰她。"
	case zeroCharacterNameIs(c, "乔安"):
		return "她想做对一点事，但更怕流程风险落到自己身上。"
	case zeroCharacterNameIs(c, "邱梅") || strings.Contains(roleText, "母亲"):
		return "她怕女儿吃亏，也怕自己的忍耐变成女儿的旧路。"
	case zeroCharacterNameIs(c, "陆敏"):
		return "她怕又被办公室一句提效抹掉现场经验，所以说话硬，心里先盘算还能保住谁。"
	case zeroCharacterNameIs(c, "韩璐"):
		return "她怕自己和组员被工单数字吞掉，只敢把疲惫藏进一句看似客气的补充。"
	case zeroCharacterNameIs(c, "陈思予"):
		return "她真心想把混乱的人接住，但也怕桥点的现金流撑不起太多善意。"
	default:
		return fmt.Sprintf("%s想保住自己作为%s的筹码，也不愿暴露“%s”背后的软处；潜台词不能越过已知信息。", zeroFirstNonEmpty(c.Name, "该角色"), zeroFirstNonEmpty(c.Role, "当前角色"), zeroFirstNonEmpty(firstString(c.Traits), "既有性格"))
	}
}

func zeroRelationshipStance(project zeroInitProject, c domain.Character) string {
	roleText := c.Role
	if zeroIsCountySpendProject(project) {
		switch {
		case zeroCharacterNameIs(c, "林澈"):
			return "对家人先报喜不报忧，对朋友肯开口但不白占便宜；面对沈知遥既服她的专业，也会用嘴贫缓解被看穿的紧张。"
		case zeroCharacterNameIs(c, "沈知遥"):
			return "对外强势守规则，对林澈会主动靠近和护短，但每一次柔软都建立在他没有拿别人冒险的前提上。"
		case zeroCharacterNameIs(c, "贺骁"):
			return "兄弟之间可以互损，钱、车和时间却算清楚；帮忙来自多年交情，不替林澈解释秘密。"
		case zeroCharacterNameIs(c, "叶南栀"):
			return "先护沈知遥，再观察林澈值不值得助攻；调侃可以推近关系，不能替两人做决定。"
		case strings.Contains(c.Role, "父亲") || strings.Contains(c.Role, "母亲"):
			return fmt.Sprintf("%s与%s有多年家庭历史，担心会绕着面子和生活细节表达，不能像刚认识的功能角色。", zeroFirstNonEmpty(c.Name, c.Role), zeroFirstNonEmpty(zeroProtagonist(project.Characters).Name, "主角"))
		}
	}
	switch {
	case zeroIsSecondAlgorithmProject(project) && zeroIsPrimaryProtagonist(project, c):
		return "先习惯性负责，再逐步划边界；帮助别人时必须付出可见代价。"
	case zeroCharacterNameIs(c, "夏岚"):
		return "一边欣赏许闻溪，一边把她当组织缓冲垫；每次人情都夹着管理者的回收条件。"
	case zeroCharacterNameIs(c, "傅行简"):
		return "把许闻溪当成项目稳定器；只要她不配合，立刻把关系转成流程压力。"
	case zeroCharacterNameIs(c, "乔安"):
		return "愿意提醒，但不愿替任何人站到台前；帮助必须藏在流程和留痕里。"
	case strings.Contains(roleText, "上级") || strings.Contains(roleText, "项目负责人") || strings.Contains(roleText, "高层"):
		return "权力在手但不能全知；施压、让步和回避都要服务其组织目标。"
	case zeroCharacterNameIs(c, "梁渡") || strings.Contains(roleText, "男主"):
		return "保持低信任试探；帮助来自判断和边界，不来自英雄救场。"
	case zeroCharacterNameIs(c, "陈思予"):
		return "会接住现场情绪，也会提前核算成本；善意必须和现金流、交付边界一起成立。"
	case zeroCharacterNameIs(c, "程棠"):
		return "依赖许闻溪，又不想只做被保护的人；每次求助都带着怕被抛下的急。"
	case zeroCharacterNameIs(c, "陆敏"):
		return "先不信办公室承诺；只有看见实际成本和补偿路径，才会把经验交出来。"
	case zeroCharacterNameIs(c, "韩璐"):
		return "表面配合，心里替组员算退路；信任必须从一次具体保护开始。"
	case strings.Contains(roleText, "母亲"):
		return "亲情已有历史，但表达常绕着体面、担心和不想添麻烦。"
	default:
		return fmt.Sprintf("%s以%s的现实利益和既有情分决定靠近或拒绝；每次态度变化都要有眼前证据。", zeroFirstNonEmpty(c.Name, "该角色"), zeroFirstNonEmpty(c.Role, "当前身份"))
	}
}

func zeroDictionAndRhythm(project zeroInitProject, c domain.Character) string {
	roleText := c.Role + " " + strings.Join(c.Traits, " ")
	if zeroIsCountySpendProject(project) {
		switch {
		case zeroCharacterNameIs(c, "林澈"):
			return "口语短句，能顺手接梗；真正震住时会停一下、重问一遍，确认后才恢复嘴贫。"
		case zeroCharacterNameIs(c, "沈知遥"):
			return "对工作对象句子短而明确，对林澈会放慢半拍，偶尔用一句看似平静的话把人逼得心虚。"
		case zeroCharacterNameIs(c, "贺骁"):
			return "语速快，熟人称呼和现场词多；先笑两声再办事，急起来直接报地点、时间和要带的东西。"
		case zeroCharacterNameIs(c, "许牧"):
			return "话少、信息密，常先指出哪里不对；吐槽落在成本、步骤和返工上，不说玄乎比喻。"
		case zeroCharacterNameIs(c, "叶南栀"):
			return "语气明快，会抛接网络梗，但转到舆情风险时立刻说人话和具体后果。"
		case strings.Contains(c.Role, "父亲"):
			return "短句、停顿多，常省掉主语；重话说到一半收住，随后用拿工具、挪车或添东西补上。"
		case strings.Contains(c.Role, "母亲"):
			return "家常口语，句子会被夹菜、收碗和算账打断；同一担心可能换个生活问题再问一遍。"
		case strings.Contains(c.Role, "商户") || strings.Contains(c.Role, "合作社"):
			return "县城口语直、具体，常把价钱、天气、位置和谁负责挂在一句里；不使用项目汇报腔。"
		}
	}
	switch {
	case zeroIsSecondAlgorithmProject(project) && zeroIsPrimaryProtagonist(project, c):
		return "平时稳、会接话；压力越大句子越短，关键处允许停顿和改口。"
	case zeroCharacterNameIs(c, "傅行简"):
		return "职业词和流程词偏多，语速平稳；被戳破时用更中性的说法缓冲。"
	case zeroCharacterNameIs(c, "程棠"):
		return "快话、口语和半句吐槽多；真正害怕时突然收短。"
	case zeroCharacterNameIs(c, "乔安"):
		return "措辞谨慎，句子常留半截余地；提醒不直接摊牌。"
	case zeroCharacterNameIs(c, "邱梅") || strings.Contains(roleText, "母亲"):
		return "家常话多，常先问吃饭和身体；真正担心时会把重话说轻。"
	case zeroCharacterNameIs(c, "罗湘", "陆敏") || strings.Contains(roleText, "门店"):
		return "句子直，带现场细节和反问；不接漂亮话，常用具体人和具体事压回来。"
	case zeroCharacterNameIs(c, "韩璐"):
		return "声音疲惫但客气，习惯先补充事实；抱怨会藏在工单、排班和客户原话里。"
	case zeroCharacterNameIs(c, "梁渡"):
		return "短句多，先问具体代价；不追着安慰，也不把判断说成鸡血。"
	case zeroCharacterNameIs(c, "陈思予"):
		return "语气更圆，能接人情绪；谈钱和排期时句子会变快、变具体。"
	default:
		return fmt.Sprintf("%s以%s常用的生活或职业词开口，句长随“%s”的性格和压力变化；不复制其他角色的冷静短句。", zeroFirstNonEmpty(c.Name, "该角色"), zeroFirstNonEmpty(c.Role, "当前身份"), zeroFirstNonEmpty(strings.Join(c.Traits, "、"), "既有性格"))
	}
}

func zeroDialogueSceneBlueprints(project zeroInitProject, states []domain.CharacterSimulationState) []domain.DialogueSceneBlueprint {
	protagonist := zeroFirstNonEmpty(zeroProtagonist(project.Characters).Name, "主角")
	counterpart := zeroFirstNonEmpty(zeroFirstNonProtagonistName(project.Characters), zeroOpeningPressureName(project), "第一位施压者")
	for _, state := range states {
		if state.Character != "" && state.Character != protagonist {
			counterpart = state.Character
			break
		}
	}
	scene := zeroFirstNonEmpty(zeroFirstScene(project.FirstChapter), "第一章主场景")
	pressure := zeroFirstNonEmpty(project.FirstChapter.CoreEvent, project.FirstChapter.Hook, project.FirstChapter.Title, "第一章核心压力")
	if zeroIsSecondAlgorithmProject(project) {
		return zeroSecondAlgorithmDialogueSceneBlueprints(protagonist, counterpart, scene, pressure)
	}
	return []domain.DialogueSceneBlueprint{{
		SceneID:              "opening-dialogue-engine",
		DialogueMode:         "pressure_negotiation",
		ModeReason:           "第一章需要让角色在未完全理解规则前围绕风险、任务或交易进行试探；若当前章是告白、审问、闲聊或汇报，正式计划必须改选对应模式。",
		ScenePressure:        fmt.Sprintf("核心压力来自“%s”，同时叠加时间、资源、身份和信息差。", pressure),
		EmotionalTemperature: "开局压住恐惧和尴尬，随后因对方催促、误读或隐瞒升温；不写成全程冷静。",
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
		IdentityGrounding:           fmt.Sprintf("通过称谓、外貌/衣着、职位/门牌/凭证、旧债或权力差说明%s是谁，以及他为什么能对%s施压。", counterpart, protagonist),
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
		BeatDensity:          "高压和恐怖场景使用短动作拍但不能每句一拍；亲密/告白场留空，汇报/制度场用物件或文件换挡。",
		SilencePolicy:        "至少安排一次无人接话、答非所问、物件不回应或刻意停顿，让潜台词承担信息。",
		InfoReleasePolicy:    "信息可以被打断、误读、半句露出或事后才明白；禁止一问一答把规则讲完。",
		ExpositionBudget:     "背景信息只允许夹在对白后的短记忆/身份桥里，每次服务当前一句台词；一段最多解决一个问题。",
		SubtextAndPowerShift: "从对方掌握称呼/任务/时间压力，推进到主角发现一处漏洞或代价；主角可以暂时保住边界，但不能无成本全赢。",
		ExitBeat:             "用具体现场变化退出：对方留下未完成动作、物件状态改变、门口方向变化、账单迟滞或关系冷场；不要用突然一声响、菜单选项或抽象金句收尾。",
		DoNotUse: []string{
			"照抄示例里的现实单位、职务或穿越解释",
			"先整段说明设定再让人物说话",
			"把对白写成系统菜单或作者问答",
			"让主角第一轮就判断全对",
			"用漂亮金句替代犹豫、误判和动作后果",
		},
	}}
}

func zeroSecondAlgorithmDialogueSceneBlueprints(protagonist, counterpart, scene, pressure string) []domain.DialogueSceneBlueprint {
	return []domain.DialogueSceneBlueprint{{
		SceneID:              "opening-dialogue-engine",
		DialogueMode:         "workplace_pressure_negotiation",
		ModeReason:           "第一章需要让角色在尚未完全理解组织取舍前，围绕AI提效、岗位价值、会议话语权或客户反馈试探；若当前章是告白、闲聊、汇报或冲突升级，正式计划必须改选对应模式。",
		ScenePressure:        fmt.Sprintf("核心压力来自“%s”，同时叠加时间、岗位资源、体面和信息差。", pressure),
		EmotionalTemperature: "开局压住尴尬和委屈，随后因对方催促、误读或客气话升温；不写成全程冷静。",
		RelationshipFrame:    fmt.Sprintf("%s和%s存在信息不对称、短期互相需要或权力差，不能像两个设定讲解员。", protagonist, counterpart),
		Medium:               "face_to_face；若正式章节走电话/消息/材料批注/企业微信，动作拍必须换成媒介拍：已读不回、打字又删掉、投屏翻页、纸角折痕、门外脚步。",
		POVRole:              "participant",
		AudiencePresence: domain.DialogueAudiencePresence{
			Present:        "none；若本章有同事、上级、客户或记录者在场，必须改填具体第三方。",
			PerformanceFor: "有观众时写明双方各自演给谁看、想在观众面前保住或遮住什么；无观众写 none。",
			AudienceEffect: "有观众时写明观众反应如何反过来改变对话走向：沉默、打圆场、装没听见、记录在案；无观众写 none。",
		},
		InfoAsymmetry: domain.DialogueInfoAsymmetry{
			POVKnows:       fmt.Sprintf("%s此刻只掌握现场可见证据和自己带进场的旧工作经验。", protagonist),
			POVLacks:       fmt.Sprintf("%s不知道完整组织取舍、对方真实议程和这场对话的机会成本，因此会误读至少一次。", protagonist),
			OtherHolds:     fmt.Sprintf("%s掌握任务/流程/风险的另一半事实，并且有隐瞒动机。", counterpart),
			ReaderPosition: "reader_level；正式计划里明确读者是否比主角先知道底牌(reader_ahead 反讽)或更晚(reader_behind 悬念)。",
			AsymmetryPlay:  "信息差通过半句话、被打断的解释、材料批注或消息截断局部收窄，但收窄的同时必须打开一个新缺口。",
		},
		ValueShift: domain.DialogueValueShift{
			Value:         "主动权与被看见感",
			OpeningCharge: fmt.Sprintf("负：%s被催促、被评价或被默认会配合，处在被动位置。", protagonist),
			TurnTrigger:   fmt.Sprintf("%s抓住对方话术或材料里的一处漏洞，把客气的压力缩成可验证的问题。", protagonist),
			ClosingCharge: "半正：主角换回局部主动权，但留下新的代价或误解；不能无成本全赢。",
		},
		PowerTrajectory: domain.DialoguePowerTrajectory{
			OpeningHolder: fmt.Sprintf("%s，凭职位、流程、时间压力或信息差先占上风。", counterpart),
			FlipBeat:      "第二轮左右：主角用短句核验、沉默或现场证据打断节奏，权力第一次易手。",
			ClosingHolder: "双方各占一半：对方保住一部分隐瞒，主角保住边界；正式计划必须写明收场谁占上风。",
		},
		AddressShift:                "记录双方称呼随压力如何漂移：敬称脱落、直呼其名、去称谓或突然加回客气话，称谓变化本身是一条潜台词线。",
		OpeningStrategy:             "dialogue_first/action_first/object_first/silence_first 任选其一；由当前章压力决定，不把对白先入场当固定模板。",
		FirstSpokenMoment:           fmt.Sprintf("若采用对白先行，%s可以先发话；若采用动作/物件/沉默先行，第一句必须等到%s已有误判或压力后再出现。", counterpart, protagonist),
		EntryLine:                   fmt.Sprintf("可选 dialogue_first 版本：%s先开口，把%s从旧状态拉入“%s”的现场压力；正式写作必须改成本书世界里的真实称呼/话术。", counterpart, protagonist, pressure),
		EntrySpeaker:                counterpart,
		LocationAnchor:              fmt.Sprintf("%s。用一句短定场落下地点、时间、空间边界或可见物件，不先写长篇设定。", scene),
		POVState:                    fmt.Sprintf("%s先有半拍迟滞、误判、身体反应或自我安慰，不能一上来就理解完整取舍。", protagonist),
		InnerQuestion:               "贴近主视角写一个具体困惑，例如这句客气话究竟推给谁、材料为什么改了、她现在该不该补一句；第三人称项目只写短念头，不切换全知解释。",
		MemoryBridge:                "只补读者理解这场对白所需的身份、前一幕、工作/生活处境和关系压力；禁止一次性灌完整背景、履历或行业说明。",
		IdentityGrounding:           fmt.Sprintf("通过称谓、外貌/衣着、职位、材料权限、工牌或权力差说明%s是谁，以及为什么能对%s施压。", counterpart, protagonist),
		DialogueObjective:           "用对白落成本章任务、岗位压力、求助、压制或AI提效第一次露面，并迫使主角下一步行动发生变化。",
		InterlocutorAgenda:          fmt.Sprintf("%s不是等待被剧情收走的人，此刻有自己的事、压力和隐瞒，开口是为了转移风险、索取帮助、完成职责或保住资源。", counterpart),
		ProtagonistResponseStrategy: fmt.Sprintf("%s按当前信息量先核验称呼、材料版本、证据或对方意图；允许误听、停顿、改口或把一句重话吞回去，不写成想明白后再行动。", protagonist),
		ObjectiveTactics: []domain.DialogueObjectiveTactic{
			{
				Character:          counterpart,
				ImmediateObjective: "把任务、风险、请求或责任推到主角面前，同时保住自己的隐瞒。",
				Tactic:             "先用称呼、催促、半句事实、客气话或制度话术占据节奏。",
				CounterTactic:      "主角用短句核验、沉默、转问材料依据或动作迟滞打断节奏。",
				EmotionalLeak:      "对方的焦虑、自利或职责从过快语速、重复称呼、避开关键词或看向屏幕里漏出。",
				TurnResult:         "对方没有完成全额转嫁，只暴露出一处可追问的漏洞。",
			},
			{
				Character:          protagonist,
				ImmediateObjective: "获得最低限度证据，同时避免被客气话、承诺或关系压力绑定。",
				Tactic:             "缩小问题、拒绝全局承诺、问具体对象，必要时改口或先沉默。",
				CounterTactic:      "对方继续施加时间压力、情绪压力、流程压力或人情压力。",
				EmotionalLeak:      "委屈和不服从手停、语气变短、答非所问或把一句话吞回去里出现。",
				TurnResult:         "主角只拿回局部主动权，并留下新的代价或误解。",
			},
		},
		TurnProgression: []domain.DialogueTurnDesign{
			{
				Speaker:             counterpart,
				SurfaceLineFunction: "入场称呼/通知/催促/求助，把场景压力直接递到主角面前。",
				HiddenSubtext:       "把自己的风险、职责或焦虑藏在礼貌、催促、玩笑、命令或半句求助里。",
				NewInformation:      "读者知道当前场景有一条必须回应的关系/制度/岗位压力。",
				PowerMove:           "对方先占据命名权、解释权或时间压力。",
				ActionBeat:          "用投屏翻页、纸面递来、屏幕亮起、椅子声、手指悬停或视线闪避承接第一句。",
				NextPressure:        "主角必须回答、确认、拒绝或继续装作没听懂。",
			},
			{
				Speaker:             protagonist,
				SurfaceLineFunction: "短句核验、改口、敷衍或先保留承诺。",
				HiddenSubtext:       "她怕被绑定责任，也怕不回应导致更坏后果。",
				NewInformation:      "暴露主角此刻不知道完整取舍，只能抓住一个可验证细节。",
				PowerMove:           "主角没有夺回全局主动权，只把对方给出的压力缩小成可确认的问题。",
				ActionBeat:          "让身体反应先于判断：手停住、喉咙发紧、看错字、错按一次或把话吞回去。",
				NextPressure:        "对方继续推进任务/岗位压力/求助，或现场给出第一次反馈。",
			},
		},
		DirectnessPolicy:     "任务、时间、岗位、客户反馈可以局部直说；委屈、羞耻、索取、压制和真实意图优先用潜台词、动作和误读呈现。",
		SubtextSource:        "潜台词来自亏欠感、资源压力、信息差、旧伤、防御机制、身份不平等或文化潜规则。",
		EscalationPattern:    "优先使用 yes-but / no-and / 三次试探 / 让步换代价；不同模式可改成汇报递进、互怼升级、暧昧回避或沉默压迫。",
		BeatDensity:          "高压职场场景使用短动作拍但不能每句一拍；亲密/告白场留空，汇报/制度场用物件或文件换挡。",
		SilencePolicy:        "至少安排一次无人接话、答非所问、消息没回或刻意停顿，让潜台词承担信息。",
		InfoReleasePolicy:    "信息可以被打断、误读、半句露出或事后才明白；禁止一问一答把背景讲完。",
		ExpositionBudget:     "背景信息只允许夹在对白后的短记忆/身份桥里，每次服务当前一句台词；一段最多解决一个问题。",
		SubtextAndPowerShift: "从对方掌握称呼/任务/时间压力，推进到主角发现一处漏洞或代价；主角可以暂时保住边界，但不能无成本全赢。",
		ExitBeat:             "用具体现场变化退出：对方留下未完成动作、讲稿状态改变、屏幕熄灭、门口方向变化、消息迟滞或关系冷场；不要用突然一声响、菜单选项或抽象金句收尾。",
		DoNotUse: []string{
			"照抄示例里的现实单位、职务或行业材料",
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
	policy := "第一章正文只能在这些基线之上新增信任、债务、恐惧、承诺、依赖或背叛记录；新增后必须通过审阅/回填进入正式 relationship_state。"
	if zeroIsSecondAlgorithmProject(project) {
		policy = "第一章正文只能在这些基线之上新增信任、亏欠、恐惧、承诺、依赖、误解或边界变化；新增后必须通过审阅/回填进入正式 relationship_state。"
	}
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
	second := zeroIsSecondAlgorithmProject(project)
	for _, c := range project.Characters {
		firstMention := project.FirstMentions[c.Name]
		priority := zeroReturnPriority(c, firstMention)
		suggestedChapter := firstMention
		if suggestedChapter == 0 && (priority == "required" || priority == "near_future") {
			suggestedChapter = 1
		}
		withInfo := "回归时必须携带新信息、资源压力、关系账或未兑现承诺之一。"
		upgrade := "若做关键选择、建立债务或需要后续回归，升级为关键角色并补全动态字段。"
		if second {
			withInfo = "回归时必须携带新信息、岗位/时间压力、关系余波或未兑现承诺之一。"
			upgrade = "若做关键选择、建立亏欠/承诺或需要后续回归，升级为关键角色并补全动态字段。"
		}
		out[c.Name] = domain.CharacterReturnPlan{
			ReturnPriority:     priority,
			SuggestedChapter:   suggestedChapter,
			DueReason:          zeroReturnDueReason(c, firstMention),
			WithNewInformation: withInfo,
			UpgradePotential:   upgrade,
			RetireReason:       "若只是气氛/捧场/凑数，场景结束即退场，不进入长期台账。",
		}
	}
	return out
}

func zeroInitCrowdPolicy(project zeroInitProject) map[string]any {
	continuity := "不进长期台账；若携带新信息或建立债务，立即升级为关键角色。"
	if zeroIsSecondAlgorithmProject(project) {
		continuity = "不进长期台账；若携带新信息、建立亏欠/承诺或后续会回归，立即升级为关键角色。"
	}
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
	if zeroIsSecondAlgorithmProject(project) {
		usagePolicy = "所有新正文都必须先把本计划转入 plan_chapter.causal_simulation：人物先有职场处境、误判、情绪压抑和可见选择，行动先由能力定价、岗位变化、女性互助/竞争、家庭照护和AI提效压力推出；关系先有边界、试探和缓慢增温，对话先按场景权力和潜台词设计，读者先看到主角被低估后的局部反击与新成本，离屏线先有工单、消息、会议记录或旁人反应回收路径，章末必须落成可追的职业后果。"
	}
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
			Want:             zeroFirstNonEmpty(state.ArcAxis.Want, state.CurrentGoal, c.Arc, "先解决眼前压力。"),
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
	second := zeroIsSecondAlgorithmProject(project)
	for i := 0; i < limit; i++ {
		entry := project.Outline[i]
		reward := zeroFirstNonEmpty(entry.CoreEvent, entry.Title, fmt.Sprintf("第%d章推进一个可见状态变化", i+1))
		cost := "奖励必须带出新代价、新债务、新误解或更深规则压力。"
		hook := zeroFirstNonEmpty(entry.Hook, "章末留下下一章能立刻承接的具体后果。")
		if second {
			cost = "奖励必须带出新机会成本、新误解、新关系压力或更具体的职业风险。"
		}
		if i == 0 {
			reward = "第一章给出一个小胜：主角靠错误后的修正拿到暂缓、证据、收据、门牌变化或下一步权限。"
			cost = "小胜不是免费，必须留下资源、关系、安全感、债务或审核尾巴。"
			if second {
				reward = "第一章给出一个小胜：许闻溪在被低估和被替代的场面里，靠一次不漂亮但有效的应对保住自己的判断权或下一步入口。"
				cost = "小胜不是免费，必须留下被误解、被记名、关系变冷、岗位风险或家庭照护时间被挤压的尾巴。"
			}
		}
		ladder = append(ladder, domain.ReaderRewardStep{
			Chapter: i + 1,
			Reward:  reward,
			Cost:    cost,
			Hook:    hook,
		})
	}
	if second {
		return domain.ReaderRewardPlan{
			ChapterWindow:           "1-5",
			FirstChapterSmallWin:    "第一章不能只写职场委屈，必须让许闻溪在现场做成一件小事：说出被忽略的问题、保住一个客户/同事、拿到一次重新发言或下一步试点机会。",
			NewDebtOrCost:           "任何机会、支持或暂缓都要留下成本：上级不悦、同事误读、家里被打断、情感边界被试探或职业风险被记录。",
			PayoffVisibility:        "奖励必须落在讲稿改动、会议记录、排班表、工单状态、客户反应、同事眼神或一条后续消息上。",
			TrafficRisk:             "只有压抑没有反击会劝退读者；只有爽点没有成本会像职场鸡汤。",
			RewardLadder:            ladder,
			ForbiddenRewardPatterns: []string{"主角全程完美正确", "上司突然欣赏", "男主直接救场", "把女性困境写成口号", "用一段总结代替现场行动"},
		}
	}
	return domain.ReaderRewardPlan{
		ChapterWindow:           "1-5",
		FirstChapterSmallWin:    "第一章不能只把主角推入危机，必须让他在犯错后用一次行动换到可见证据、暂缓或下一步权限。",
		NewDebtOrCost:           "任何暂缓、权限、代付、获救或证据都要留下对价；若对价未知，正文必须让角色显性意识到“拿什么换”。",
		PayoffVisibility:        "奖励必须落在收据、门牌、账单、关系姿态、位置状态或下一步可执行目标上。",
		TrafficRisk:             "只有危机没有小胜会让读者觉得压抑；只有菜单式爽点没有代价会让逻辑崩。",
		RewardLadder:            ladder,
		ForbiddenRewardPatterns: []string{"无限额度提前亮明", "系统菜单摆选项", "免费代付", "主角全程正确", "只靠解释设定当爽点"},
	}
}

func zeroEvidenceReturnChains(project zeroInitProject, states []domain.CharacterSimulationState) []domain.EvidenceReturnChain {
	protagonist := zeroProtagonist(project.Characters).Name
	scene := zeroFirstNonEmpty(zeroFirstScene(project.FirstChapter), "第一章主场景")
	second := zeroIsSecondAlgorithmProject(project)
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
			Evidence:            zeroEvidenceReturnMedium(second),
			ProtagonistAccess:   zeroProtagonistAccessRule(second),
			ReturnTiming:        timing,
			DistortionOrMisread: zeroEvidenceDistortion(second),
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
		event := fmt.Sprintf("按第%d章首次牵引提前保留后台经历；第一章若未展示，也不能在后续凭空随叫随到。", firstMention)
		if c.Description != "" {
			event = "按角色卡后台推进：" + c.Description
		}
		add(c.Name, event, fmt.Sprintf("第%d章或首次出场前以可见证据回收。", firstMention), firstMention)
	}
	if len(out) == 0 {
		out = append(out, domain.EvidenceReturnChain{
			OffscreenCharacter:  "第一章现场外角色/后台压力源",
			Event:               zeroFirstNonEmpty(project.FirstChapter.CoreEvent, "第一章后台压力继续推进"),
			Evidence:            zeroFallbackEvidenceMedium(second),
			ProtagonistAccess:   zeroProtagonistAccessRule(second),
			ReturnTiming:        "第2章或下一次回到" + scene + "时",
			DistortionOrMisread: zeroEvidenceDistortion(second),
			ChapterToResolve:    2,
		})
	}
	return out
}

func zeroEvidenceReturnMedium(second bool) string {
	if second {
		return "后续通过企业微信/短信、会后确认单、工单状态、排班变动、客户反馈、同事转述或现场小物回到主角视角。"
	}
	return "后续通过通信、账单、位置变化、证物、目击者或现场残留传回主角。"
}

func zeroFallbackEvidenceMedium(second bool) string {
	if second {
		return "以会议记录、岗位沟通材料、客户一句反馈、同事表情、后台状态变化或一条迟到的消息回到主角视角。"
	}
	return "以账单、门牌、消息、收据、脚印、监控盲区或他人半句证词回到主角视角。"
}

func zeroProtagonistAccessRule(second bool) string {
	if second {
		return "主角必须通过亲历、消息、文件、同事转述或客户现场反馈获得，不能默认知道后台安排。"
	}
	return "主角必须通过通信/亲见/证据传回/能力授权获得，不能默认知道。"
}

func zeroEvidenceDistortion(second bool) string {
	if second {
		return "传回时可能被话术包装、截图截断、善意隐瞒、权力关系过滤或被主角先误读。"
	}
	return "传回时可能被延迟、遮挡、误读或被角色出于自保隐瞒一部分。"
}

func zeroEndingContract(project zeroInitProject) domain.EndingConsequenceContract {
	anchor := zeroFirstNonEmpty(project.FirstChapter.Hook, zeroFirstScene(project.FirstChapter), project.FirstChapter.CoreEvent, "第一章可见证据")
	if zeroIsSecondAlgorithmProject(project) {
		return domain.EndingConsequenceContract{
			EndingMode:      "具体职场后果落地，不用抽象金句收章。",
			ConcreteAnchor:  anchor,
			Consequence:     "章末必须改变一个可回填状态：岗位入口、会议记录、工单状态、同事关系、客户反馈、家庭照护安排或许闻溪的自我判断至少其一。",
			NextChapterPull: "下一章从该后果继续追问：她为了不被替代要付出什么、谁因此靠近或疏远、AI提效背后伤到谁。",
			WhyNotUI:        "如果出现系统界面或材料，它必须先像真实工作工具，再成为人物选择的压力；不能把按钮或模板当剧情。",
			ForbiddenEndings: []string{
				"突然金句式反问",
				"男主替女主完成关键判断",
				"上级立刻服气",
				"把职场困境总结成口号",
				"主角冷静全知地想明白后停章",
			},
		}
	}
	return domain.EndingConsequenceContract{
		EndingMode:      "具体后果落地，不做读者可见的标准菜单选项。",
		ConcreteAnchor:  anchor,
		Consequence:     "章末必须改变一个可回填状态：门牌/账单/位置/债务/关系/证据/安全边界至少其一。",
		NextChapterPull: "下一章从该后果继续追问代价、回收证据或处理关系变化。",
		WhyNotUI:        "如果出现界面或条款，它必须像诡异物件/合同/烧痕/缺字一样作用于角色，而不是把 A/B 按钮展示给读者。",
		ForbiddenEndings: []string{
			"附加选项式 UI 菜单",
			"突然一个声音作为万能钩子",
			"金句加问号",
			"无限额度或免费代付不交代对价",
			"主角冷静全知地想明白规则后停章",
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
	second := zeroIsSecondAlgorithmProject(project)
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
			TriggerCondition:  zeroFirstNonEmpty(zeroReturnDueReason(c, firstMention), "当大纲、配角线或证据回收需要时再引入。"),
			KnowledgeBoundary: "主角不知道该角色后台经历，除非正文提供通信或证据。",
			NextCheck:         fmt.Sprintf("第%d章前后或该角色首次被提及时检查", zeroFirstNonZero(firstMention, 3)),
		})
	}
	if len(out) == 0 {
		trigger := "新角色做关键选择、建立关系债务、携带信息或后续回归时触发。"
		if second {
			trigger = "新角色做关键选择、建立亏欠/承诺、携带信息或后续回归时触发。"
		}
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
	scene := zeroFirstNonEmpty(zeroFirstScene(project.FirstChapter), "第一章主场景")
	if zeroIsSecondAlgorithmProject(project) {
		return []domain.RealitySupportPlan{
			{
				Domain:        "职场会议/项目协作",
				SourceRef:     "meta/web_reference_brief.md、RAG 或当轮 web_search；若缺失，正式 plan_chapter 前补检索",
				UsableDetail:  "会议室座位、投屏卡顿、讲稿批注、企业微信消息、确认单措辞和谁被安排发言。",
				TransformedAs: "角色在现场改词、停顿、避开承诺、争取一句补充或被上级截断。",
				ChapterUse:    scene,
				ForbiddenDirectUse: []string{
					"把职场流程写成百科",
					"真实公司名称直接搬用",
					"用岗位黑话替代人物情绪",
				},
			},
			{
				Domain:        "AI改变岗位/女性职业转型",
				SourceRef:     "meta/web_reference_brief.md、reference_pack.references 或当轮 web_search",
				UsableDetail:  "AI话术模板、客服/运营岗位被合并、外包替代、培训课报名、绩效口径和转岗沟通材料。",
				TransformedAs: "同事一句玩笑、表格一格空白、客户真实反馈、母亲一句旧经验或主角压住的反问。",
				ChapterUse:    "第一章职业压力与女性成长入口",
				ForbiddenDirectUse: []string{
					"新闻摘要式旁白",
					"把女性困境写成宣言",
					"让角色突然精通全部AI趋势",
				},
			},
			{
				Domain:        "城市生活/家庭照护",
				SourceRef:     "book_world.routes + 稳定现实常识；必要时当轮检索同类城市通勤和照护成本",
				UsableDetail:  "通勤时间、家里电话、复诊提醒、社区商户营业节奏、请假难和下班后的疲惫。",
				TransformedAs: "消息延迟、电话没接、手边小物、临时加班和主角把情绪收回去的动作。",
				ChapterUse:    "offscreen_character_stage.meeting_constraint",
				ForbiddenDirectUse: []string{
					"苦情堆砌",
					"把母亲线当工具催泪",
					"无成本赶场或随叫随到",
				},
			},
		}
	}
	return []domain.RealitySupportPlan{
		{
			Domain:        "居住/物业/邻里",
			SourceRef:     "meta/web_reference_brief.md 或当轮 web_search；若缺失，正式 plan_chapter 前补检索",
			UsableDetail:  "楼道、门禁、缴费通知、物业群、邻居半句反应和夜间生活噪声。",
			TransformedAs: "门牌、通知栏、群聊弹窗、收据、鞋印、排队或门禁失败。",
			ChapterUse:    scene,
			ForbiddenDirectUse: []string{
				"真实小区/城市名称直接搬用",
				"真实敏感事件蹭热点",
				"网页摘要式说明物业流程",
			},
		},
		{
			Domain:        "支付/交易/账单",
			SourceRef:     "meta/web_reference_brief.md、RAG 或当轮 web_search；交易规则以 world_rules 为准",
			UsableDetail:  "支付失败、额度遮挡、费用说明缺失、客服/账单字段、确认按钮和撤销入口。",
			TransformedAs: "角色先找取消/费用说明/对价，再在压力下做有限操作；对价未知时显性留疑问。",
			ChapterUse:    "第一章交易或规则反馈场景",
			ForbiddenDirectUse: []string{
				"免费代付",
				"无脑刷卡解决冲突",
				"把标准 UI 选项当章末钩子",
			},
		},
		{
			Domain:        "交通/城市距离",
			SourceRef:     "book_world.routes + 稳定现实常识；必要时当轮检索同类城市通勤耗时",
			UsableDetail:  "步行、电梯、夜间打车、门禁、楼层距离、堵车或封控造成的现实耗时。",
			TransformedAs: "配角迟到、不能随叫随到、消息延迟和见面限制。",
			ChapterUse:    "offscreen_character_stage.meeting_constraint",
			ForbiddenDirectUse: []string{
				"无授权瞬移",
				"跨城无耗时见面",
				"配角随叫随到",
			},
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
	roleText := c.Role + " " + strings.Join(c.Traits, " ")
	base := zeroEmotionProfile{
		PhysiologicalState: "普通工作日能量基线，压力通过肩颈、手部、语速和视线变化外露。",
		ImmediateState:     "受第一章开场地点、时间窗口和角色职责影响；不预设全程冷静。",
		Primary:            "不安",
		Composite:          name + "的防御性警惕",
		GoalAppraisal:      "把本章事件评估为会改变目标、资源或边界，先出现身体反应，再寻找合理说法。",
		BoundaryThreat:     "安全、尊严、身份叙事、关系义务或资源控制权被威胁。",
		Regulation:         "先把情绪压成可执行的小动作，压力过高时露出停顿、回避或过度解释。",
		Defense:            name + "用职业习惯保护自己：先处理事，再承认情绪。",
		Bias:               "更相信能保护既有叙事的证据，直到现场代价逼其修正。",
		ApproachAvoidance:  "趋近安全和控制感，同时回避失控、亏欠或被看穿。",
		ShortLongTerm:      "短期想止损，长期想守住身份、关系或成长目标。",
		SelfRelationship:   "自我需求会和亲情、亏欠、承诺、群体评价或权力关系冲突。",
		HiddenReason:       "真正原因来自旧伤、责任或不愿承认的依赖。",
		MeaningNeed:        "想证明自己不是任人摆布的工具，并维持能解释痛苦的自我叙事。",
		Metacognition:      "能否意识到自己在防御，要由本章压力决定；开局不默认高自控。",
		Action:             name + "把情绪转成一次可见选择，推动信息、关系或资源发生位移。",
		EventRole:          "本章事件通过该角色的误判、克制或临界选择完成，而不是只被外部事件推着走。",
		Evidence:           []string{"动作停顿", "语速变化", "视线/手部反应", "一次不完全理性的选择"},
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
		base.BoundaryThreat = "自身生活秩序、资源、职业责任或既有关系边界受压，而非第一章现场冲突。"
		base.Regulation = "用离屏行动、日程变化或资源取舍留下状态，避免隔空同步主角情绪。"
		base.Defense = fmt.Sprintf("%s按自己的身份处理离屏压力，不替主角提前解释、担心或救场。", name)
		base.Action = fmt.Sprintf("%s沿独立生活线形成一个首次入场时可回收的目标、代价或资源状态。", name)
		base.EventRole = "暂不进入第一章场景；通过可追踪的离屏状态保证后续出场有来路。"
		base.Evidence = []string{"离屏日程", "职业或家庭压力", "首次入场条件", "可回收的资源或关系状态"}
		return base
	}
	if zeroIsCountySpendProject(project) {
		base.PhysiologicalState = "县城日常节奏里的普通能量基线；压力从夹菜、看手机、收钱、搬东西、语速和站位变化里露出来。"
		base.ImmediateState = "受接风饭、夜市收摊时间和熟人目光牵制；情绪可以被打断、压回去或换成一句家常话。"
		switch {
		case zeroCharacterNameIs(c, "林澈"):
			base.Primary = "难堪"
			base.Composite = "林澈被亲戚戳中后的窝火、失业羞耻和不肯让父母担心的硬撑"
			base.GoalAppraisal = "先把饭桌上的追问当成一顿饭能忍过去的小事，系统到账后才意识到自己真有机会把局面扳回来。"
			base.BoundaryThreat = "失业后的尊严、父母在亲戚面前的体面，以及异常资金会不会反噬家人。"
			base.Regulation = "先笑着顶一句，离桌后反复核验；确认能试时马上去做一笔小而真实的消费。"
			base.Defense = "林澈用嘴贫挡住难堪，真正担心的风险只在独处核验和行动速度里露出来。"
			base.Action = "林澈把被比较的窝火转成当晚去夜市试钱，并完成第一次真实改善。"
			base.EventRole = "他的行动让开篇从失业受气切到系统兑现和县城经营的第一步。"
			base.Evidence = []string{"筷子停一下又继续夹菜", "离桌后重看到账数字", "先问能不能撤回", "确认后立刻联系商户"}
		case zeroCharacterNameIs(c, "沈知遥"):
			base.Primary = "警惕"
			base.Composite = "沈知遥对安全风险的紧绷、对林澈异常资金的怀疑和不愿当众让他难堪的克制"
			base.GoalAppraisal = "先把现场当成需要立即纠正的夜市隐患，再从林澈肯不肯听劝判断这人值不值得继续合作。"
			base.BoundaryThreat = "商户安全、公开规则和她不愿承认的私人关心同时被一次冒进试探。"
			base.Regulation = "对施工问题直接叫停，确认能改后再放低声音问钱和责任；关心藏在补充条件里。"
			base.Defense = "沈知遥用专业和强势保护现场，也借检查掩住自己对林澈格外多看了两眼。"
			base.Action = "沈知遥纠正走线后没有赶走林澈，而是约定次日再看试点。"
			base.EventRole = "她把无脑撒钱的可能性压住，同时为男女主同场经营建立第一条可信合作线。"
			base.Evidence = []string{"先看线再看人", "当众说清责任", "转向林澈时语气放缓", "给出次日时间"}
		case zeroCharacterNameIs(c, "贺骁"):
			base.Primary = "担心"
			base.Composite = "贺骁看兄弟笑话的轻松、怕林澈真撑不住的担心和被叫上后立刻来劲的义气"
			base.Defense = "贺骁用损友笑话遮住关心，先问活和油钱，免得一句安慰把林澈说得更难受。"
			base.Action = "贺骁接下明早借皮卡的活，把兄弟情落成一个具体时间和动作。"
			base.EventRole = "他让主角的下一步不只靠系统，也有县城熟人关系和现实执行力承接。"
		case zeroCharacterNameIs(c, "周曼"):
			base.Primary = "心疼"
			base.Composite = "周曼怕儿子受委屈的焦急、对亲戚眼光的敏感和不敢追问太重的克制"
			base.Defense = "周曼用夹菜、转话题和收拾碗筷护住儿子，不把心疼讲成一段大道理。"
			base.Action = "周曼在亲戚追问时不断给林澈留台阶，也记住他离桌后那点反常。"
			base.EventRole = "她把返乡失业的压力落到一家人的体面和真实担心上。"
		case zeroCharacterNameIs(c, "林建国"):
			base.Primary = "窝火"
			base.Composite = "林建国对儿子失业的焦虑、被亲戚越界后的火气和不会说软话的护短"
			base.Defense = "林建国用一句硬话截住饭桌，再把没说出口的支持留给之后的实际帮忙。"
			base.Action = "林建国不替儿子吹嘘，只阻止亲戚继续拿他比较，让林澈自己去做下一步。"
			base.EventRole = "他的半句保护把家庭压力写实，也给后续父子和解留下空间。"
		case zeroCharacterNameIs(c, "马玉芬"):
			base.Primary = "戒备"
			base.Composite = "马玉芬想多卖几单的期待、怕出事故赔钱的戒备和对外来热心的不信任"
			base.Defense = "马玉芬先问坏了找谁、会不会挡生意，用生意人的具体问题挡住空口好意。"
			base.Action = "马玉芬在确认安全和售后后允许试用，并用真实顾客反应决定是否继续。"
			base.EventRole = "她让系统消费必须接受普通商户的现实判断，首个爽点因此有可信落点。"
		default:
			base.Composite = fmt.Sprintf("%s作为%s面对县城熟人社会时的谨慎、私心和不愿失去现有位置的压力", name, zeroFirstNonEmpty(c.Role, "当事人"))
			base.Defense = fmt.Sprintf("%s先按%s的习惯处理眼前事，把真实情绪藏进条件、称呼和是否出手。", name, zeroFirstNonEmpty(c.Role, "自己的身份"))
			base.Action = fmt.Sprintf("%s把情绪转成一次符合自身利益的靠近、观望或拒绝，不为主角无理由让路。", name)
		}
		return base
	}
	if !zeroIsSecondAlgorithmProject(project) {
		return base
	}
	switch {
	case zeroIsPrimaryProtagonist(project, c):
		base.Primary = "委屈"
		base.Composite = "被复制后的羞窘、责任感和压住怒气"
		base.GoalAppraisal = "她先把发布会失控评估为自己必须圆过去的现场事故，随后意识到这是价值被替代的公开羞辱。"
		base.BoundaryThreat = "她好用、能扛、会补台的旧身份被公司拿来证明岗位可以合并。"
		base.Regulation = "先圆场、接话、看讲稿；真正过不去时在确认单上停住。"
		base.Defense = "许闻溪用负责和体面压住委屈，直到程棠的目光让她无法继续替公司说圆。"
		base.Bias = "习惯相信只要自己补得够快，局面就还能被保住。"
		base.ApproachAvoidance = "趋近团队安全和现场秩序，回避当众承认自己也害怕被替代。"
		base.Action = "许闻溪从本能圆场转向拒签无异议确认。"
		base.EventRole = "她的拒签让第一章从发布会事故变成女主成长线的第一次付代价选择。"
		base.Evidence = []string{"讲稿被捏皱", "看见程棠等她表态", "确认单上的停笔", "把圆场话吞回去"}
	case zeroCharacterNameIs(c, "梁渡") || strings.Contains(c.Role, "男主"):
		base.Primary = "警觉"
		base.Composite = "克制的刺痛和旁观者负罪感"
		base.Defense = "梁渡用冷判断和边界感防止自己再次把别人当案例。"
		base.Action = "梁渡用一句不中听但具体的判断刺破许闻溪的圆场惯性。"
		base.EventRole = "他不救场，只给外部视角，让许闻溪看见自己正在替别人收拾残局。"
		base.Evidence = []string{"短句打断", "不递鸡血建议", "先看确认单再评价", "说完后留出距离"}
	case zeroCharacterNameIs(c, "傅行简"):
		base.Primary = "控制欲"
		base.Composite = "项目焦虑、虚荣和对失控的回避"
		base.Defense = "傅行简把人的不安翻译成流程问题，用指标感遮住心虚。"
		base.Action = "傅行简用发布会口径和权限压力逼许闻溪配合。"
		base.EventRole = "他把提效叙事推到台前，成为许闻溪拒绝粉饰的直接对手。"
		base.Evidence = []string{"改用中性词", "避开岗位替代字眼", "催促签字", "用项目权限施压"}
	case zeroCharacterNameIs(c, "夏岚"):
		base.Primary = "疲惫"
		base.Composite = "管理者的焦躁、欣赏和自保"
		base.Defense = "夏岚用强硬和经验主义保护组织位置，也保护自己不被上层追责。"
		base.Action = "夏岚把许闻溪叫去约谈，要求她把话说得更可控。"
		base.EventRole = "她让女性在组织里执行降本逻辑的镜像压力落地。"
		base.Evidence = []string{"压低声音", "看时间", "把话说成流程要求", "给出有限人情余地"}
	case zeroCharacterNameIs(c, "程棠"):
		base.Primary = "慌张"
		base.Composite = "年轻人的不服气、害怕和被抛下感"
		base.Defense = "程棠先用快话和玩笑遮慌，遮不住时直接问以后怎么办。"
		base.Action = "程棠的目光和追问让许闻溪不能继续把岗位合并说成流程误会。"
		base.EventRole = "她把宏观的 AI 提效压力变成一个具体会失去位置的人。"
		base.Evidence = []string{"消息打到一半又删", "笑得过快", "追问许姐怎么办", "看向确认单"}
	case zeroCharacterNameIs(c, "乔安"):
		base.Primary = "谨慎"
		base.Composite = "职业风险、同情和自保"
		base.Defense = "乔安把提醒藏在流程允许的话里，不把自己推到明处。"
		base.Action = "乔安通过名单、措辞或半句提醒让风险可见，但不给完整承诺。"
		base.EventRole = "她让组织流程的冷和个人迟疑同时存在。"
		base.Evidence = []string{"半句停住", "只发流程内信息", "提醒后撤回视线", "把名单顺序露出一点"}
	case zeroCharacterNameIs(c, "邱梅") || strings.Contains(roleText, "母亲"):
		base.Primary = "担心"
		base.Composite = "旧式体面、心疼和劝忍"
		base.Defense = "邱梅把担心说成别硬碰、别麻烦别人，用生活话遮住心疼。"
		base.Action = "邱梅的语音或物件把上一代女性的忍耐带进许闻溪的选择。"
		base.EventRole = "她提供价值观源头，让许闻溪明白自己不能再复制旧式忍耐。"
		base.Evidence = []string{"语音里压低嗓子", "便当盒或旧工作鞋", "先问吃饭没有", "把难处说得轻"}
	case zeroCharacterNameIs(c, "罗湘", "陆敏") || strings.Contains(roleText, "门店"):
		base.Primary = "不服"
		base.Composite = "被低估后的火气和现实盘算"
		base.Defense = "罗湘用硬话和现场经验挡住办公室话术。"
		base.Action = "罗湘把门店现场的真实代价甩回许闻溪面前。"
		base.EventRole = "她证明被合并的不是抽象岗位，而是一套没被定价的经验。"
		base.Evidence = []string{"反问一句现场怎么办", "提到熟客或门店事", "不接漂亮话", "用账本/货架细节压回去"}
	case zeroCharacterNameIs(c, "陈思予"):
		base.Primary = "清醒"
		base.Composite = "照顾现场的热心、现金流焦虑和产品嗅觉"
		base.Defense = "陈思予把情绪先接住，再把问题拆成名额、排期、成本和交付边界。"
		base.Action = "陈思予把许闻溪的方法看成可合作的雏形，同时提醒她善意也要算成本。"
		base.EventRole = "她让第二算法从个人反抗走向可持续协作的现实问题。"
		base.Evidence = []string{"先安抚当事人", "顺手记排期", "直接问谁来付成本", "把混乱整理成小班名额"}
	}
	return base
}

func zeroRelationshipEmotionArcs(project zeroInitProject, states []domain.CharacterSimulationState) []domain.RelationshipEmotionArc {
	protagonist := zeroProtagonist(project.Characters)
	second := zeroIsSecondAlgorithmProject(project)
	var out []domain.RelationshipEmotionArc
	for _, c := range project.Characters {
		if c.Name == "" || c.Name == protagonist.Name {
			continue
		}
		relType := zeroRelationshipType(c, second)
		emotionalWant := "想从对方那里得到安全感、确认、资源、解释权、赦免或控制感；正式章节需按关系细化。"
		fear := "害怕被拖累、被抛弃、被看穿、被背叛、被债务绑定或失去主导权。"
		trustDebt := "信任从低位开始，任何帮助都要留下债务、亏欠、承诺或边界。"
		conflictTrigger := zeroFirstNonEmpty(project.FirstChapter.CoreEvent, "世界规则第一次施压")
		if second {
			emotionalWant = "想从对方那里得到被看见、被理解、资源支持、专业认可或情绪喘息；正式章节需按关系细化。"
			fear = "害怕被拖累、被抛弃、被看穿、被误解、被迫欠人情或失去主导权。"
			trustDebt = "信任从低位开始，任何帮助都要留下亏欠、承诺、边界、机会成本或情绪余波。"
			conflictTrigger = zeroFirstNonEmpty(project.FirstChapter.CoreEvent, "AI提效和岗位变化第一次施压")
		}
		out = append(out, domain.RelationshipEmotionArc{
			Pair:                         []string{protagonist.Name, c.Name},
			RelationshipType:             relType,
			CurrentBond:                  zeroRelationshipBond(relType, second),
			EmotionalWant:                emotionalWant,
			Fear:                         fear,
			PowerBalance:                 "信息、资源、情绪需求和规则权限不对等。",
			IntimacyStage:                zeroIntimacyStage(relType),
			TrustDebt:                    trustDebt,
			ConflictTrigger:              conflictTrigger,
			AttachmentOrLoveLanguage:     "以行动照顾、风险隔离、资源支持或边界尊重表达情感；不要只靠告白式台词。",
			Boundary:                     "没有通信、证据或共同经历时，关系不能突然亲密或全信。",
			RomancePotential:             zeroRomancePotential(c),
			NextEmotionalBeat:            "下一次推进必须改变信任、亏欠、亲密、嫉妒、保护欲或权力位置之一。",
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

func zeroRelationshipType(c domain.Character, second bool) string {
	text := c.Role + " " + c.Description
	switch {
	case strings.Contains(text, "妹") || strings.Contains(text, "兄") || strings.Contains(text, "姐") || strings.Contains(text, "父") || strings.Contains(text, "母") || strings.Contains(text, "亲"):
		return "亲情"
	case strings.Contains(text, "恋") || strings.Contains(text, "暧昧") || strings.Contains(text, "女主") || strings.Contains(text, "男主") || strings.Contains(text, "伴侣"):
		return "恋爱/暧昧潜势"
	case strings.Contains(text, "反派") || strings.Contains(text, "敌") || strings.Contains(text, "压迫"):
		if second {
			return "职场对抗/价值冲突"
		}
		return "敌对/债务"
	case strings.Contains(text, "朋友") || strings.Contains(text, "搭档") || strings.Contains(text, "后勤") || strings.Contains(text, "合作"):
		return "合作/友谊"
	default:
		if second {
			return "职场关系/潜在亏欠"
		}
		return "社会关系/潜在债务"
	}
}

func zeroRelationshipBond(relType string, second bool) string {
	switch relType {
	case "亲情":
		return "爱与责任并存，保护欲、内疚和控制边界会互相冲突。"
	case "恋爱/暧昧潜势":
		return "吸引尚未等于信任，亲密必须通过共同风险、边界尊重和真实选择推进。"
	case "敌对/债务":
		return "恐惧、贪婪、羞辱或支配欲维系关系。"
	case "职场对抗/价值冲突":
		return "对抗来自话语权、机会分配和价值判断，不来自单纯脸谱化压迫。"
	case "合作/友谊":
		return "互利先于亲密，信任靠可见行动慢慢建立。"
	default:
		if second {
			return "尚未建立稳定情感连接，但可能通过亏欠、目击、共同压力或一次边界尊重变成关系。"
		}
		return "尚未建立稳定情感连接，但可能通过债务、目击或共同风险变成关系。"
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

func zeroRomancePotential(c domain.Character) string {
	text := c.Role + " " + c.Description + " " + c.Arc
	if strings.Contains(text, "恋") || strings.Contains(text, "暧昧") || strings.Contains(text, "女主") || strings.Contains(text, "男主") || strings.Contains(text, "伴侣") {
		return "有恋爱/暧昧潜势：吸引必须来自价值冲突、共同风险、边界尊重和互相看见，不靠强行发糖。"
	}
	return "none；当前关系不以恋爱驱动，若后续出现吸引必须重新建模亲密阶段、阻碍和边界。"
}

func zeroVisualDesign(project zeroInitProject, states []domain.CharacterSimulationState) []domain.CharacterVisualDesign {
	var out []domain.CharacterVisualDesign
	second := zeroIsSecondAlgorithmProject(project)
	for _, c := range project.Characters {
		if c.Name == "" {
			continue
		}
		out = append(out, domain.CharacterVisualDesign{
			Character:       c.Name,
			Silhouette:      zeroVisualSilhouette(c),
			FaceAndHair:     zeroVisualFaceHair(c),
			ClothingStyle:   zeroVisualClothing(c),
			ColorPalette:    zeroVisualPalette(c),
			BodyLanguage:    zeroVisualBodyLanguage(c),
			SignatureObject: zeroVisualObjectForProject(c, second),
			FirstImpression: zeroFirstNonEmpty(c.Role, "能被读者一眼区分的角色"),
			StatusWear:      "第一章或首次出场时要有与处境相关的灰、汗、雨、皱、破损、疲惫或异常痕迹。",
			ChangeRule:      "外观随资源、权力、伤势、亲密关系和世界污染改变；不能每次出场都像静态设定图。",
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

func zeroVisualObjectForProject(c domain.Character, second bool) string {
	if second {
		switch {
		case zeroIsProtagonist(c):
			return "便签本、手机、讲稿批注、旧帆布包或能承载核验习惯和疲惫感的物件。"
		case strings.Contains(c.Role, "反派") || strings.Contains(c.Role, "压迫") || strings.Contains(c.Description, "上级"):
			return "平板、会议激光笔、签字笔、整齐文件夹或过度干净的工牌。"
		default:
			return "与职业、家庭照护或城市生活压力绑定的小物件，不能只做装饰。"
		}
	}
	return zeroVisualObject(c)
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

func zeroVisualPalette(c domain.Character) string {
	if strings.Contains(c.Role, "反派") {
		return "冷白、灰黑、暗金或病态红点缀；避免全员黑灰。"
	}
	if strings.Contains(c.Role, "妹") || strings.Contains(c.Role, "医") {
		return "低饱和浅色与局部冷色，体现脆弱、洁净或压抑。"
	}
	return "低饱和生活色加一处标志色，随资源和污染程度变化。"
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

func zeroVisualObject(c domain.Character) string {
	if zeroIsProtagonist(c) {
		return "便签本、卡片、手机、旧钥匙或能承载核验习惯的物件。"
	}
	if strings.Contains(c.Role, "反派") {
		return "账本、印章、伞、戒指、骨质饰物或过度干净的工具。"
	}
	return "与职业/生活压力绑定的小物件，不能只做装饰。"
}

func zeroSecondAlgorithmWorldBackgroundPlan(project zeroInitProject) zeroWorldBackgroundPlan {
	scene := zeroFirstNonEmpty(zeroFirstScene(project.FirstChapter), "第一章主场景")
	city := zeroFirstNonEmpty(zeroKnownCityName(project), "澄港")
	protagonist := zeroFirstNonEmpty(zeroProtagonist(project.Characters).Name, "许闻溪")
	counterpart := zeroFirstNonEmpty(zeroFirstNonProtagonistName(project.Characters), "开局对手")
	pressure := zeroFirstNonEmpty(project.FirstChapter.CoreEvent, project.FirstChapter.Hook, project.FirstChapter.Title, "第一章职业压力")
	openingPressure := zeroOpeningPressureName(project)
	lifeNode := "桥点职业转型工作室"
	if project.BookWorld != nil {
		for _, place := range project.BookWorld.Places {
			if place.ID != "" && place.Name != "" && place.Name != scene {
				lifeNode = place.Name
				break
			}
		}
	}
	rules := zeroWorldRuleTexts(project.WorldRules, 3)
	ruleText := firstString(rules)
	if ruleText == "" {
		ruleText = "AI提效只改变工作分配，不能替人物承担选择后果。"
	}
	return zeroWorldBackgroundPlan{
		Version:     1,
		Scope:       "reusable_world_background_prewrite",
		Project:     project.Name,
		Chapter:     1,
		GeneratedAt: project.GeneratedAt,
		UsagePolicy: "所有新正文写作前必须先把本计划转入 plan_chapter.causal_simulation：事件背景不能只写成职场流程，必须从城市生活、时间窗口、组织制度、女性处境、关系网络、岗位资源、群体情绪、AI边界和叙事信息差共同推出；每章可根据最新台账和网络资料更新，但不能省略这些层。",
		ResearchBasis: []string{
			"女性成长叙事：处境、误判、小胜和成本要同步出现",
			"职场小说现实支架：会议、协作工具、绩效口径和岗位调整要落到可见动作",
			"AI改变职业背景：技术压力必须转成角色选择、资源重新分配和身份焦虑",
			"人物关系写法：对话要有潜台词、权力差和情绪泄露",
			"信息差 craft：好奇心需要后果和可回收路径",
		},
		Layers: domain.WorldBackgroundLayersPlan{
			PhysicalSpace:       fmt.Sprintf("%s/%s 作为开局空间，必须写清会议桌、投屏、座位距离、门口动线、手机放置和谁能看见谁；空间结构直接决定谁能发话、谁被迫沉默、谁能靠近。", city, scene),
			TimeLayer:           "第一章开场时间必须固定到故事钟：上市前夜、项目验收、下班后照护电话、培训报名截止或会议倒计时会改变角色判断速度；同一选择在白天和下班后不能同义。",
			SocialInstitution:   "显规则来自 world_rules、book_world、项目流程、会后确认单、岗位沟通、排班、预算和客户反馈；正文只展示角色能碰到的制度接口，不把行业背景一次讲完。",
			CulturalNorm:        "潜规则来自职场对年龄、婚育、照护责任、稳定性、服从感、情绪管理和可替代性的默认判断；羞耻、体面和不想麻烦别人会改变角色是否开口。",
			RelationshipNetwork: fmt.Sprintf("%s 与 %s 之间不是工具关系，而是信息差、职位压力、互相试探、同事目光和未说出口的善意组成的临时网络；主角未通信/未见证时不知道其他人时间线。", protagonist, counterpart),
			EconomicResource:    "结构性权力来自岗位入口、发言机会、客户名单、预算、培训名额、排班和时间，不来自口头承诺；资源必须有控制者、准入规则、机会成本和人情路径。",
			ConflictTension:     fmt.Sprintf("%s 不是孤立事件，而是稳定工作日常被AI提效、岗位收缩、家庭照护和关系误读撕开的口子。", pressure),
			SocialMood:          "开局城市和公司里的群体情绪以玩笑、沉默、转发、抢报名、压低声音、过度积极或突然客气表现；流言会随章节推进改变普通人的选择。",
			CosmologyMetaRule:   fmt.Sprintf("元背景规则以“%s”为当前铁律样本；任何AI工具、流程权限或专业判断都必须有成本、边界、例外条件和失败模式。", ruleText),
			NarrativeMeta:       "读者、主角、配角和组织后台的信息量必须分层；正文优先守主角视角，离屏事件只能通过消息、会议记录、工单、客户反馈、旁人反应或后续证据回到主线。",
			EventActivation:     "本章事件由时间窗口逼近、岗位资源不可用、潜规则诱导和信息差同时激活；如果任一层删掉仍能原样发生，正式 plan_chapter 必须重写该层。",
		},
		InformationLedger: []domain.InformationAsymmetryRecord{
			{
				Subject:           openingPressure,
				ReaderKnows:       []string{"第一章会出现可见职业压力，但完整组织取舍不得一次解释。"},
				ProtagonistKnows:  []string{"只能确认自己亲眼看到、听到、收到或能复核的现场证据。"},
				CharacterKnows:    []string{fmt.Sprintf("%s 只知道自己正在承受的局部压力，不能替全书行业背景代言。", counterpart)},
				CharacterMistakes: []string{"有人会误以为努力、忍耐、熟人关系或旧经验仍然足够。"},
				CharacterPretends: []string{"上级、同事或制度接口可能假装只是按流程办事。"},
				HiddenFromReader:  []string{"组织真正的岗位取舍、谁被保护、谁被牺牲、谁在提前准备退路。"},
				RevealCondition:   "只能通过会议措辞变化、消息截断、工单状态、客户反馈、培训名额或付出代价后的局部权限揭示。",
				TensionFunction:   "防止角色突然全知，让读者追问谁知道真实取舍、谁在误判、谁在利用沉默。",
			},
			{
				Subject:           "公司和城市女性群体的情绪",
				ReaderKnows:       []string{"办公室不是静态背景，普通人的反应会随着项目推进改变。"},
				ProtagonistKnows:  []string{"主角只能通过会议室、群聊、电话、客户现场、培训课或同事半句接触片段。"},
				CharacterKnows:    []string{"不同职位、年龄、家庭状态和资源位置的人掌握不同版本传闻。"},
				CharacterMistakes: []string{"角色可能把客气当安全、把沉默当认可、把机会当稳定。"},
				CharacterPretends: []string{"资源控制者可能假装没有听见求助或假装调整是普通优化。"},
				HiddenFromReader:  []string{"流言背后的传播者、真实利益和谁已经开始另找出路。"},
				RevealCondition:   "当流言改变发言顺序、报名名额、排班、客户态度或互助边界时再进入正文。",
				TensionFunction:   "让职场和城市生活随章节推进变形，而不是只在主角身边发生事。",
			},
		},
		HiddenRules: []domain.HiddenRulePressure{
			{
				Domain:        "AI提效/岗位定价/会议话语权",
				VisibleRule:   "表面上只有项目流程、会议材料、岗位沟通、排班或客户反馈。",
				HiddenRule:    "真正有效的是谁能定义问题、谁被要求体面让步、谁承担试错成本；不表态不等于没有后果。",
				CulturalNorm:  "稳定、懂事、情绪管理、照护责任和年龄评价会给女性角色额外压力。",
				WhoBenefits:   openingPressure,
				WhoPays:       protagonist + " 和被系统性压低价值的人",
				ViolationCost: "发言机会被拿走、岗位入口缩窄、同事误解、客户关系受损或个人时间被继续压榨。",
				SceneEvidence: "讲稿批注、投屏页码、表格空格、企业微信提示、会议室沉默、被截断的一句话或客户迟疑。",
			},
			{
				Domain:        "城市生活/家庭照护/职业转型",
				VisibleRule:   "家人、同事、客户、培训机构和社区商户按日常秩序运转。",
				HiddenRule:    "谁掌握时间、信息、名额、客户信任和情绪劳动，谁就能决定别人是否有下一步。",
				CulturalNorm:  "普通人会先保住工作和体面，再找熟人、小圈子或旧经验求证。",
				WhoBenefits:   "掌握资源和消息通道的人",
				WhoPays:       "没有资源、无法请假、被贴上不稳定标签或被孤立的角色",
				ViolationCost: "错过机会、加班补位、失去客户信任、关系降温、家里临时安排被打乱。",
				SceneEvidence: fmt.Sprintf("%s 的消息、课程名单、客户反馈、通勤时间、关门动作和临时安排。", lifeNode),
			},
		},
		SocialMoodRumors: []domain.SocialMoodRumor{{
			Group:             "澄光员工/社区商户/培训课学员",
			Mood:              "不安被压成玩笑、沉默和过度积极。",
			Rumor:             "有人说AI会把基础岗位合并，有人说会用工具的人反而更有机会，也有人说别在会上第一个出头。",
			Source:            "企业微信群、茶水间半句、培训报名页、客户门店里的闲谈或电话断续。",
			SpreadPath:        "同组群聊、会议室、培训课、社区商户、家庭电话和下班通勤。",
			Reliability:       "半真半假；能推动行为，但不能直接当事实。",
			BehaviorEffect:    "普通人抢名额、压低声音、提前准备退路、向熟人打听、对许闻溪更客气或更疏远。",
			ProtagonistAccess: "主角只能接触现场片段或通信片段，不能自动知道所有传闻。",
		}},
		RitualCalendar: []domain.RitualCalendarWindow{{
			Time:                "第一章开场窗口",
			CalendarType:        "项目验收/会议截止/培训报名/下班后照护窗口",
			RitualOrDeadline:    zeroFirstNonEmpty(project.FirstChapter.Hook, pressure),
			SocialMeaning:       "普通工作日被迫进入价值重新排序的时刻，谁先反应、谁先误判都会留下记录。",
			PracticalConstraint: "窗口内发言顺序、材料版本、客户反馈、请假、排班或培训名额都可能受限。",
			EmotionalCharge:     "疲惫、体面、亲情责任、被低估的羞耻和不想示弱会放大错误操作。",
			MissedCost:          "错过窗口会失去发言、失去名额、失去客户信任、让同事误解或把家庭压力继续藏下去。",
			SceneUse:            scene,
		}},
		StructuralResources: []domain.StructuralResourcePressure{
			{
				Resource:                  "发言权/岗位入口/材料版本/客户反馈",
				Controller:                openingPressure,
				ScarcityReason:            "AI提效和组织调整让能被看见的工作机会更稀缺。",
				AccessRule:                "必须通过现场表现、项目凭证、客户反馈、会议记录或上级授权获得。",
				BlackMarketOrInformalPath: "熟人提醒、提前打听、私下求证、替人补位、压下情绪换取机会。",
				PriceOrCost:               "名声风险、时间被占、关系变冷、家庭安排被挤压或后续被重点观察。",
				PowerEffect:               "谁能定义有效工作，谁就能定义谁值得留下。",
				ChapterPressure:           "许闻溪必须判断哪些话该说、哪些证据该拿、哪些体面不能再让。",
			},
			{
				Resource:                  "培训名额/排班时间/社区客户信任",
				Controller:                "公司、培训机构、社区商户、家庭成员或项目负责人",
				ScarcityReason:            "岗位转型、家庭照护、客户流失和同事竞争让普通资源变成权力。",
				AccessRule:                "需要时间、信誉、情绪劳动、可见成果或交换筹码。",
				BlackMarketOrInformalPath: "熟人介绍、下班后补课、帮人救急、交换信息、承担额外工作。",
				PriceOrCost:               "休息时间、个人边界、亲密关系温度、家庭安排或自我怀疑。",
				PowerEffect:               "资源控制者能影响谁能进入下一阶段、谁只能继续原地补位。",
				ChapterPressure:           "配角不能随叫随到，通信、赶路、培训和照护都要写出限制。",
			},
		},
		CosmologyChecks: []domain.CosmologyRuleCheck{{
			Layer:              "AI/组织制度/现实因果",
			Rule:               ruleText,
			Cost:               "每次使用、拒绝或质疑AI工具都必须留下时间、关系、职位、名声或情绪成本。",
			Boundary:           "角色未获得明确权限、材料版本或客户反馈前，不能绕过组织流程。",
			ExceptionCondition: "none；例外只能由后续正文触发、支付代价、审核通过并回填台账。",
			Evidence:           "world_rules.json、world_foundation、book_world 和本章可见工作物/场景后果。",
			FailureMode:        "若没有成本和边界，AI工具、男主判断或主角能力会变成万能解法，长篇中段必崩。",
		}},
		ConflictWeb: []domain.ConflictWebNode{
			{
				Parties:        []string{protagonist, openingPressure},
				ConflictType:   "岗位定价/话语权/职业安全",
				OpenGoal:       protagonist + " 想保住专业判断并获得可执行的下一步。",
				HiddenAgenda:   openingPressure + " 试图让角色在体面、绩效或机会名义下承担更多成本。",
				ResourceStake:  "发言权、材料版本、客户反馈、岗位入口和第一章现场证据。",
				InformationGap: "主角只知道局部证据，压力源知道更多组织取舍，读者只能跟主角推断。",
				TimePressure:   "第一章会议/验收窗口内必须做出有限选择。",
				CurrentBalance: "旧工作秩序尚未完全崩塌，普通人还试图用旧经验处理AI变化。",
				Destabilizer:   pressure,
				NextEscalation: "后续 3-4 章从单点会议扩展到培训名额、客户现场、同事转岗和家庭照护。",
			},
			{
				Parties:        []string{protagonist, counterpart},
				ConflictType:   "关系/试探/互相误读",
				OpenGoal:       "一方想保住体面或获得解释，另一方想不被拖进错误承诺。",
				HiddenAgenda:   "求助、善意、试探、逃避责任或转嫁压力可能混在一起。",
				ResourceStake:  "现场失败样本、通信渠道、信任、情绪边界和后续证据。",
				InformationGap: "配角知道自己的局部困境，主角不知道其完整动机，读者需要证据判断其是否可信。",
				TimePressure:   "会议、客户反馈、培训报名或电话窗口会迅速关闭。",
				CurrentBalance: "双方仍按同事礼貌、上下级规则或陌生人边界试探。",
				Destabilizer:   "AI提效压力把普通互动变成岗位价值与关系边界的测试。",
				NextEscalation: "配角线后续以证据回收、误解升级或资源互换进入主线。",
			},
		},
		TensionMatrix: domain.NarrativeTensionMatrix{
			StabilityTurbulence:     "稳定工作日常被第一章职业压力打破；许闻溪首先是被打破的人，之后才可能成为改变的人。",
			ExplicitHiddenRules:     "显规则是会议、材料、岗位流程和客户反馈，潜规则是谁被要求体面让步、谁的时间被默认征用。",
			InformationGap:          "读者跟随主角只能看见局部；配角、上级、客户和群体流言各自有不同信息量。",
			TimePressurePreparation: "倒计时发生在角色未准备好、疲惫、害怕和资源不足时，不能写成提前准备完美通关。",
			WhyEventNow:             "第一章是新推演线的 canonical start；旧数据只作背景种子，必须在此刻重新激活人物选择和职场压力。",
			ReaderQuestion:          "下一章读者应追问：许闻溪这次争取来的入口要付出什么，谁会靠近她，谁会开始防她。",
			POVBoundary:             "正文不越过主角可见证据；离屏线只以消息、会议记录、工单、客户反馈、目击或后续台账传回。",
		},
	}
}

func zeroInitWorldBackgroundPlan(project zeroInitProject) zeroWorldBackgroundPlan {
	if zeroIsSecondAlgorithmProject(project) {
		return zeroSecondAlgorithmWorldBackgroundPlan(project)
	}
	scene := zeroFirstNonEmpty(zeroFirstScene(project.FirstChapter), "第一章主场景")
	city := zeroFirstNonEmpty(zeroKnownCityName(project), "开局城市")
	protagonist := zeroFirstNonEmpty(zeroProtagonist(project.Characters).Name, "主角")
	counterpart := zeroFirstNonEmpty(zeroFirstNonProtagonistName(project.Characters), "开局压力源")
	pressure := zeroFirstNonEmpty(project.FirstChapter.CoreEvent, project.FirstChapter.Hook, project.FirstChapter.Title, "第一章规则压力")
	openingPressure := zeroOpeningPressureName(project)
	rules := zeroWorldRuleTexts(project.WorldRules, 3)
	ruleText := firstString(rules)
	if ruleText == "" {
		ruleText = "第一章世界规则必须以可见后果施压。"
	}
	lifeNode := "第一章相邻生活节点"
	if project.BookWorld != nil {
		for _, place := range project.BookWorld.Places {
			if place.ID != "" && place.Name != "" && place.Name != scene {
				lifeNode = place.Name
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
		UsagePolicy: "所有新正文写作前必须先把本计划转入 plan_chapter.causal_simulation：事件背景不能只写成气氛，必须从物理空间、时间窗口、制度、文化潜规则、关系网络、资源控制、社会情绪、宇宙观规则和叙事信息差共同推出；每章可根据最新台账和网络资料更新，但不能省略这些层。",
		ResearchBasis: []string{
			"SFWA worldbuilding questions: geography/history/trade/calendar/conflict",
			"Writing Excuses worldbuilding in miniature: every world detail introduces stakes and rules",
			"Brandon Sanderson laws of magic: limitations, costs and boundaries matter more than raw power",
			"Information gap craft: curiosity needs consequence and a reveal path",
			"Multi-antagonist/conflict craft: each conflict node needs its own agenda but must tie back to main conflict",
		},
		Layers: domain.WorldBackgroundLayersPlan{
			PhysicalSpace:       fmt.Sprintf("%s/%s 作为开局空间，必须写清入口、出口、遮挡、光照、天气/雾、楼层或街巷距离，以及角色能否离开；空间结构直接决定谁能逼近、谁能躲、谁能听见。", city, scene),
			TimeLayer:           "第一章开场时间必须固定到故事钟：时辰、夜间/白天、季节、倒计时、账单日或仪式窗口会改变角色判断速度；同一事件在凌晨和白天不能同义。",
			SocialInstitution:   "显规则来自 world_rules、book_world、账单/门牌/登记/执法/交易制度；正文只展示角色能碰到的制度接口，不把世界观一次讲完。",
			CulturalNorm:        "潜规则来自本世界对名字、债务、签字、门牌、承诺、亲缘、面子和求救的默认判断；羞耻、忌讳和默认礼俗会改变角色是否开口。",
			RelationshipNetwork: fmt.Sprintf("%s 与 %s 之间不是工具关系，而是信息、债务、恐惧、求助和互不信任组成的临时网络；主角未通信/未见证时不知道其他人时间线。", protagonist, counterpart),
			EconomicResource:    "结构性权力来自可支付/可确认/可抵押/可通行/可通信的资源，不来自口头身份；资源必须有控制者、价格、准入规则和黑市/潜规则路径。",
			ConflictTension:     fmt.Sprintf("%s 不是孤立怪事，而是稳定日常被规则、资源短缺、信息差和时间压力撕开的口子。", pressure),
			SocialMood:          "开局城市的群体情绪以低声议论、关门、群聊、排队、抢购、沉默或过度热心表现；流言会随章节推进改变普通人的选择。",
			CosmologyMetaRule:   fmt.Sprintf("元背景规则以“%s”为当前铁律样本；任何能力、黑卡、诡异交易或科技都必须有成本、边界、例外条件和失败模式。", ruleText),
			NarrativeMeta:       "读者、主角、配角和后台势力的信息量必须分层；正文优先守主角视角，离屏事件只能通过通信、物件、账单、目击或后续证据回到主线。",
			EventActivation:     "本章事件由时间窗口逼近、资源不可用、潜规则诱导和信息差同时激活；如果任一层删掉仍能原样发生，正式 plan_chapter 必须重写该层。",
		},
		InformationLedger: []domain.InformationAsymmetryRecord{
			{
				Subject:           openingPressure,
				ReaderKnows:       []string{"第一章会出现可见规则压力，但完整机制不得一次解释。"},
				ProtagonistKnows:  []string{"只能确认自己亲眼看到、听到、收到或能复核的现场证据。"},
				CharacterKnows:    []string{fmt.Sprintf("%s 只知道自己正在承受的局部压力，不能替全书规则代言。", counterpart)},
				CharacterMistakes: []string{"有人会误以为旧世界现金、口头承诺、熟人求助或旧经验仍然足够。"},
				CharacterPretends: []string{"受害者/收租方/制度接口可能假装只是按流程办事。"},
				HiddenFromReader:  []string{"幕后控制者、完整账单逻辑、终局代价和未来资产链。"},
				RevealCondition:   "只能通过账单变化、物件痕迹、通信残片、后续回收证据或付出代价后的局部权限揭示。",
				TensionFunction:   "防止角色突然全知，让读者追问谁知道规则、谁误判规则、谁在利用规则。",
			},
			{
				Subject:           "城市社会情绪与流言",
				ReaderKnows:       []string{"城市不是静态背景，普通人的反应会随着时间线改变。"},
				ProtagonistKnows:  []string{"主角只能通过楼道、群聊、电话、店铺、公告或现场人群接触片段。"},
				CharacterKnows:    []string{"不同阶层、职业、地点的人掌握不同版本传闻。"},
				CharacterMistakes: []string{"角色可能把传闻当规则、把官方说法当事实、把沉默当安全。"},
				CharacterPretends: []string{"资源控制者可能假装没有听见谣言或假装谣言是官方通知。"},
				HiddenFromReader:  []string{"流言背后的传播者和操控目的。"},
				RevealCondition:   "当流言改变价格、路线、门禁、抢购、求救或背叛行为时再进入正文。",
				TensionFunction:   "让城市随章节推进变形，而不是只在主角身边发生事。",
			},
		},
		HiddenRules: []domain.HiddenRulePressure{
			{
				Domain:        "交易/债务/身份确认",
				VisibleRule:   "表面上只有条款、账单、门牌、登记或支付接口。",
				HiddenRule:    "真正有效的是谁被迫承认、谁留下证据、谁承担对价；不确认不等于无成本。",
				CulturalNorm:  "名字、签字、承诺、救人和欠债在本世界有强烈羞耻和人情压力。",
				WhoBenefits:   openingPressure,
				WhoPays:       protagonist + " 和被迫求助的普通人",
				ViolationCost: "资源被扣、身份被标记、关系被债务绑定，严重时死亡/失踪/异化。",
				SceneEvidence: "欠费单、门牌、收据、退化现金、沉默人群、避开的称呼或不敢碰的按钮。",
			},
			{
				Domain:        "城市生活/相邻节点",
				VisibleRule:   "居民、商铺、物业、同伴或组织按日常秩序运转。",
				HiddenRule:    "谁掌握补给、门禁、通信、路线和证词，谁就能决定别人是否有下一步。",
				CulturalNorm:  "普通人会先保自己，再找熟人、小圈子或旧经验求证。",
				WhoBenefits:   "掌握资源和消息通道的人",
				WhoPays:       "没有资源、无法通信、位置暴露或被孤立的角色",
				ViolationCost: "被拒绝入内、涨价、断供、延迟救援、被当成危险样本。",
				SceneEvidence: fmt.Sprintf("%s 的关门、短消息、排队、账单、物资和路线限制。", lifeNode),
			},
		},
		SocialMoodRumors: []domain.SocialMoodRumor{{
			Group:             "开局城市普通住户/行人/店员",
			Mood:              "恐慌压低成沉默、侥幸和小声互相试探。",
			Rumor:             "有人说旧规则失效，有人说只要不开口/不签字/不看账单就没事。",
			Source:            "楼道目击、群聊截屏、店门口半句传话、公告残字或电话断续。",
			SpreadPath:        "同楼层、便利店、小超市、物业群、街口队列或角色通信。",
			Reliability:       "半真半假；能推动行为，但不能直接当世界规则。",
			BehaviorEffect:    "普通人关门、抢资源、压低声音、拒绝称呼、抬价或向熟人转嫁风险。",
			ProtagonistAccess: "主角只能接触现场片段或通信片段，不能自动知道全城传闻。",
		}},
		RitualCalendar: []domain.RitualCalendarWindow{{
			Time:                "第一章开场窗口",
			CalendarType:        "开局时刻/deadline/账单日/仪式窗口",
			RitualOrDeadline:    zeroFirstNonEmpty(project.FirstChapter.Hook, pressure),
			SocialMeaning:       "普通生活被迫进入规则确认时刻，谁先反应、谁先误判都会留下记录。",
			PracticalConstraint: "窗口内通信、支付、交通、门禁、身份称呼或资源交换都可能受限。",
			EmotionalCharge:     "疲惫、恐惧、亲情责任、失业/失控羞耻或旧伤会放大错误操作。",
			MissedCost:          "错过窗口会失去证据、被动认账、失去他人、暴露姓名或进入更高成本路线。",
			SceneUse:            scene,
		}},
		StructuralResources: []domain.StructuralResourcePressure{
			{
				Resource:                  "确认权/姓名/签字/账单凭证",
				Controller:                openingPressure,
				ScarcityReason:            "新世界里有效确认比现金、口头解释和旧关系更稀缺。",
				AccessRule:                "必须通过可见条款、凭证、门牌、登记、收据或能力授权获得。",
				BlackMarketOrInformalPath: "熟人求助、恐吓、诱导代缴、冒名、代签或信息遮挡。",
				PriceOrCost:               "姓名暴露、债务绑定、资源扣押、关系破裂或后续审计。",
				PowerEffect:               "谁能定义确认动作，谁就能定义债务、资格和安全边界。",
				ChapterPressure:           "主角必须判断哪些动作等于确认，哪些只是观察证据。",
			},
			{
				Resource:                  "城市生活节点的补给/通信/路线",
				Controller:                "商铺、物业、同伴、交通节点或当地势力",
				ScarcityReason:            "夜间、封锁、规则污染、恐慌抢购或信息中断让普通资源变成权力。",
				AccessRule:                "需要距离、时间、门禁、信任、支付能力或交换筹码。",
				BlackMarketOrInformalPath: "熟人后门、加价、赊账、交换消息、冒险绕路。",
				PriceOrCost:               "时间、暴露位置、欠人情、支付对价或失去其他路线。",
				PowerEffect:               "资源控制者能影响谁能活到下一章、谁能获得证据。",
				ChapterPressure:           "配角不能随叫随到，通信/赶路/补给都要写出限制。",
			},
		},
		CosmologyChecks: []domain.CosmologyRuleCheck{{
			Layer:              "诡异/契约/世界规则",
			Rule:               ruleText,
			Cost:               "每次使用、规避或试探规则都必须留下资源、关系、身份、时间或安全代价。",
			Boundary:           "角色未获得明确能力、凭证或交易权限前，不能绕过该规则。",
			ExceptionCondition: "none；例外只能由后续正文触发、支付代价、审核通过并回填台账。",
			Evidence:           "world_rules.json、world_foundation、book_world 和本章可见物件/场景后果。",
			FailureMode:        "若没有成本和边界，黑卡/能力/主角判断会变成作弊器，长篇中段必崩。",
		}},
		ConflictWeb: []domain.ConflictWebNode{
			{
				Parties:        []string{protagonist, openingPressure},
				ConflictType:   "身份/债务/资源确认",
				OpenGoal:       protagonist + " 想保住安全边界并获得可复核证据。",
				HiddenAgenda:   openingPressure + " 试图让角色在恐惧、善意或旧经验中留下确认。",
				ResourceStake:  "姓名、签字、账单凭证、通行/支付权限和第一章现场证据。",
				InformationGap: "主角只知道局部证据，压力源知道更多确认规则，读者只能跟主角推断。",
				TimePressure:   "第一章开场窗口内必须做出有限选择。",
				CurrentBalance: "旧世界生活秩序尚未完全崩塌，普通人还试图用旧经验处理。",
				Destabilizer:   pressure,
				NextEscalation: "后续 3-4 章从单点规则扩展到相邻地点、资源控制者和城市流言。",
			},
			{
				Parties:        []string{protagonist, counterpart},
				ConflictType:   "关系/求助/转嫁风险",
				OpenGoal:       "一方想活下去或得到解释，另一方想不被拖入未知债务。",
				HiddenAgenda:   "求助、善意、试探、逃避责任或转嫁风险可能混在一起。",
				ResourceStake:  "现场失败样本、通信渠道、信任、人情债和后续证据。",
				InformationGap: "配角知道自己的局部困境，主角不知道其完整动机，读者需要证据判断其是否可信。",
				TimePressure:   "求助/交易/门禁/电话窗口会迅速关闭。",
				CurrentBalance: "双方仍按旧关系或陌生人礼貌试探。",
				Destabilizer:   "规则压力把普通互动变成债务与身份确认。",
				NextEscalation: "配角线后续以证据回收、背叛阈值或资源互换进入主线。",
			},
		},
		TensionMatrix: domain.NarrativeTensionMatrix{
			StabilityTurbulence:     "稳定日常被第一章规则压力打破；主角首先是被打破的人，之后才可能成为催化剂。",
			ExplicitHiddenRules:     "显规则是条款/门禁/账单/制度流程，潜规则是谁留下确认、谁被羞耻或人情逼着认账。",
			InformationGap:          "读者跟随主角只能看见局部；配角、压力源和城市流言各自有不同信息量。",
			TimePressurePreparation: "倒计时发生在角色未准备好、疲惫、害怕和资源不足时，不能写成提前准备完美通关。",
			WhyEventNow:             "第一章是新推演线的 canonical start；旧数据只作背景种子，必须在此刻重新激活规则和人物选择。",
			ReaderQuestion:          "下一章读者应追问：这个选择/凭证/账单的代价是什么，谁在利用信息差，城市其他地方是否也变了。",
			POVBoundary:             "正文不越过主角可见证据；离屏线只以通信、证物、账单、目击或后续台账传回。",
		},
	}
}

func zeroInitChapterPlan(project zeroInitProject, dynamics zeroInitCharacterDynamicsDoc, crowd map[string]any, storycraft zeroPrewriteStorycraftPlan, worldBackground zeroWorldBackgroundPlan) domain.ChapterPlan {
	roleList, _ := crowd["roles"].([]domain.CrowdRoleDesign)
	first := project.FirstChapter
	projectPromise := zeroFirstNonEmpty(project.Premise, "第一章必须证明本书核心承诺可持续。")
	if len([]rune(projectPromise)) > 120 {
		projectPromise = string([]rune(projectPromise)[:120])
	}
	goal := "建立主角初始压力、世界规则第一次露面、第一条可追踪行动目标。"
	conflict := zeroFirstNonEmpty(first.CoreEvent, "角色目标与世界/关系压力第一次相撞。")
	emotionArc := "从旧经验可控，到规则/关系压力失控，再到带代价的主动选择。"
	requiredBeats := []string{
		"证明主角为什么非行动不可",
		"让一条世界规则通过可见后果第一次施压",
		"让至少一个关系/资源状态发生可回填变化",
	}
	forbiddenMoves := []string{
		"提前解释全书谜底",
		"让角色凭空知道未公开信息",
		"用捧场角色完成关键解谜/救场/反杀",
	}
	emotionTarget := "紧张、警惕、被迫选择后的追问感。"
	hookGoal := "让读者想知道这个选择/物件/规则后果下一章会怎样继续收费。"
	sceneAnchors := []string{zeroFirstNonEmpty(zeroFirstScene(first), "第一章主场景"), "可复核的纸面/物件/空间证据", "关系或资源交换动作"}
	chapterFunction := "整本书入口：证明人物系统、世界规则和连载发动机，而不是只触发怪事。"
	if zeroIsSecondAlgorithmProject(project) {
		goal = "建立许闻溪的初始处境、AI提效带来的职业压力、第一条可追踪成长行动目标。"
		conflict = zeroFirstNonEmpty(first.CoreEvent, "许闻溪的职业目标与组织/关系压力第一次相撞。")
		emotionArc = "从旧经验还可控，到被低估和被替代感逼近，再到带代价的主动选择。"
		requiredBeats = []string{
			"证明许闻溪为什么非行动不可",
			"让AI提效/岗位变化通过可见后果第一次施压",
			"让至少一个关系/岗位/客户反馈/家庭照护状态发生可回填变化",
		}
		forbiddenMoves = []string{
			"提前解释完整行业背景",
			"让角色凭空知道未公开信息",
			"用捧场角色或男主完成关键判断/救场/反击",
		}
		emotionTarget = "克制、委屈、被低估后的不服气，以及第一次拿回主动权后的余震。"
		hookGoal = "让读者想知道许闻溪争取来的入口会付出什么，谁会靠近她，谁会开始防她。"
		sceneAnchors = []string{zeroFirstNonEmpty(zeroFirstScene(first), "第一章主场景"), "讲稿/消息/会议记录/客户反馈等可复核证据", "关系、岗位或家庭时间的具体变化"}
		chapterFunction = "整本书入口：证明许闻溪的处境、成长发动机和AI改变职业的现实压力，而不是只写工作流程。"
	}
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
				"premise", "current_chapter_outline", "characters", "world_rules", "book_world",
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
	if zeroIsSecondAlgorithmProject(project) {
		return domain.LongformOpeningDesign{
			TargetReader:     "想看女性在现实职场里从被低估到重新定价自己的读者。",
			OpeningHook:      zeroFirstNonEmpty(first.Hook, first.CoreEvent, first.Title),
			SerialEngine:     "AI提效压缩岗位、女性互助和竞争、家庭照护成本、能力被看见与被误读交替推进，每章让许闻溪多拿回一点主动权。",
			ReaderRewardLoop: []string{"看见她被轻描淡写地否定", "她在现场犯小错或忍下一句", "她用行动保住一个具体结果", "新结果带来更清晰的代价和下一步"},
			LongRangePromises: []domain.LongRangePromise{{
				Promise:          "许闻溪会从替系统补漏洞的人，变成能为自己和身边女性重新设计选择的人。",
				FirstChapterSeed: zeroFirstNonEmpty(first.Hook, first.CoreEvent),
				PayoffHorizon:    "首个小弧兑现一次局部反击，后续跨卷升级为职业与关系双线成长。",
			}},
			RevealBudget:      []string{"第一章只展示AI提效压力和人物处境，不解释完整行业背景", "只让主角知道她亲历、听见或能拿到证据的部分"},
			FirstChapterProof: []string{"许闻溪有非行动不可的现实压力", "她不是完美女强人，而是在不舒服里做出选择", "关系线开始试探但不抢成长主线"},
			RetentionRisks:    []string{"开局像工作汇报", "角色说话过于规范", "女性困境被写成口号", "男主过早解决问题"},
		}
	}
	return domain.LongformOpeningDesign{
		TargetReader:     "需要强入口、可持续规则压力和人物选择代价的类型文读者。",
		OpeningHook:      zeroFirstNonEmpty(first.Hook, first.CoreEvent, first.Title),
		SerialEngine:     "角色目标、规则代价、资源短缺和关系债务互相咬合，每章产生新选择。",
		ReaderRewardLoop: []string{"看见异常/压力", "角色核验证据", "付出代价换取新信息", "章末状态变化带来下一问"},
		LongRangePromises: []domain.LongRangePromise{{
			Promise:          "主角将从被动承压变成能主动利用规则和关系的人。",
			FirstChapterSeed: zeroFirstNonEmpty(first.Hook, first.CoreEvent),
			PayoffHorizon:    "首个小弧内兑现一次局部胜利，长线跨卷升级。",
		}},
		RevealBudget:      []string{"第一章只展示规则后果，不解释完整机制", "只让主角知道自己能复核的部分"},
		FirstChapterProof: []string{"主角有非行动不可的压力", "世界规则会即时反馈", "关系/资源台账可持续推进"},
		RetentionRisks:    []string{"开局只像事件触发而非人物选择", "角色说话像解释设定", "章末钩子落成抽象金句"},
	}
}

func zeroChapterInformationGaps(project zeroInitProject) []string {
	if zeroIsSecondAlgorithmProject(project) {
		return []string{"AI提效项目真实取舍", "许闻溪会不会被边缘化", "梁渡站在哪一边", "同事各自隐忍或自保的原因", "章末后果背后的下一步代价"}
	}
	return []string{"世界规则完整机制", "对方真实意图", "章末钩子答案", "未授权后台秘密"}
}

func zeroChapterCausalBeat(project zeroInitProject) domain.CausalSimulationBeat {
	if zeroIsSecondAlgorithmProject(project) {
		return domain.CausalSimulationBeat{
			Cause:           "第一章现场出现AI提效、岗位调整或公开发言压力。",
			CharacterChoice: "许闻溪先按最低证据标准稳住现场，再决定是否把真实问题说出来。",
			WorldResponse:   "公司流程、上级态度、同事反应或客户反馈给出可见后果。",
			StoryResult:     "许闻溪得到下一步入口，同时付出关系、时间、名声或安全感成本。",
		}
	}
	return domain.CausalSimulationBeat{
		Cause:           "第一章现场出现规则/关系/资源压力。",
		CharacterChoice: "主角按最低证据标准试探，不直接交出承诺或身份边界。",
		WorldResponse:   "世界/制度/关系给出可见后果。",
		StoryResult:     "主角得到下一步目标，同时付出关系、资源或安全感成本。",
	}
}

func zeroChapterDecisionPoints(project zeroInitProject) []string {
	if zeroIsSecondAlgorithmProject(project) {
		return []string{"是否接下被包装成机会的额外工作", "是否在公开场合说出真实问题", "是否接受梁渡的质询", "是否替同事或客户多往前走一步", "是否把家庭照护压力继续藏起来"}
	}
	return []string{"是否相信新信息", "是否交换资源/承诺", "是否暴露私人边界", "是否承担章末代价"}
}

func zeroChapterOutcomeShift(project zeroInitProject) []string {
	if zeroIsSecondAlgorithmProject(project) {
		return []string{"许闻溪的目标更具体", "至少一条岗位/关系/客户反馈/家庭照护状态变化", "章末后果从工作细节变成下一章压力"}
	}
	return []string{"主角目标更具体", "至少一条关系/资源/知识状态变化", "伏笔种子从装饰变成下一章压力"}
}

func zeroWritingNorms(project zeroInitProject) []domain.WritingNormApplication {
	if zeroIsSecondAlgorithmProject(project) {
		return []domain.WritingNormApplication{
			{
				Source:             "writing_engine/user_rules",
				RuleFocus:          []string{"章节契约先定义写什么", "计划是素材池不是正文清单", "第一章必须证明女性成长发动机"},
				ChapterApplication: "把许闻溪的处境、选择、误判和小胜落成 scene_anchors 与 causal_beats，不用规范汇报、解释行业或连环对话代替场景。",
				ProofTargets:       []string{"开场压力", "一次不完美但有效的行动", "章末 outcome_shift"},
				FailureRisk:        "只写工作流程或算法概念，人物没有情绪起伏和主动选择。",
			},
			{
				Source:             "anti_ai_tone/human_feel_craft/writing_techniques_digest",
				RuleFocus:          []string{"少抽象概括", "至少两个生活/职场物件承担新信息", "对白有潜台词和误读", "章尾不用金句问号"},
				ChapterApplication: "用讲稿批注、手机消息、会议室站位、同事停顿、客户反应和许闻溪的身体动作承载压力；连续对话后必须切回动作或沉默。",
				ProofTargets:       []string{"scene_anchors", "voice_logic.dialogue_functions", "environment_state"},
				FailureRisk:        "角色像在开需求会、对白太工整、旁白替人物总结成长意义。",
			},
			{
				Source:             "dialogue_writing",
				RuleFocus:          []string{"先选 dialogue_mode", "按权力关系和情绪压力选择 opening_strategy", "每个角色都有 objective_tactics", "沉默、误读、反制和情绪泄露必须可见"},
				ChapterApplication: "关键对白先判断是汇报、试探、求助、回避、压制、互怼还是暧昧边界；再决定对白/动作/物件/沉默/环境声谁先入场。",
				ProofTargets:       []string{"dialogue_scene_blueprints", "voice_logic", "character_stage_records"},
				FailureRisk:        "所有角色同一种话术，连续问答像审核记录，男主或上级替女主说出答案。",
			},
			{
				Source:             "web_reference_guidelines",
				RuleFocus:          []string{"网络资料只做现实支架", "AI职业变化细节必须有人物载体", "岗位/通勤/照护细节要记录来源与日期"},
				ChapterApplication: "读取项目 web_reference_brief 或当轮 web_search 证据，把AI工具、岗位调整、会议材料、群聊、通勤和照护成本转成角色能看见或听见的现场细节。",
				ProofTargets:       []string{"external_reference_plan", "trend_language_plan", "grounding_details"},
				FailureRisk:        "网页摘要化、热词硬贴、职场细节无来源导致 AI 味和出戏感。",
			},
		}
	}
	return []domain.WritingNormApplication{
		{
			Source:             "writing_engine/user_rules",
			RuleFocus:          []string{"章节契约先定义写什么", "字数和禁用词服从项目规则", "第一章必须证明长篇发动机"},
			ChapterApplication: "把主角压力、规则第一次露面和章末状态变化落成 scene_anchors 与 causal_beats，不用聊天式解释代替场景。",
			ProofTargets:       []string{"开场压力", "规则可见后果", "章末 outcome_shift"},
			FailureRisk:        "只触发怪事但主角没有真实选择，章节像模型按大纲排事件。",
		},
		{
			Source:             "anti_ai_tone/human_feel_craft/writing_techniques_digest",
			RuleFocus:          []string{"少抽象概括", "至少两个物件/痕迹承担新信息", "对白有目的和潜台词", "章尾不用金句问号"},
			ChapterApplication: "用纸面、门牌、群体反应、误判和动作后果承载规则；连续判断后切回物件/动作/对白。",
			ProofTargets:       []string{"scene_anchors", "voice_logic.dialogue_functions", "environment_state"},
			FailureRisk:        "解释段、整齐清单、角色替作者讲规则、章尾抽象悬念。",
		},
		{
			Source:             "dialogue_writing",
			RuleFocus:          []string{"先选 dialogue_mode", "按场景压力和关系权力选择 opening_strategy", "每个角色都有 objective_tactics", "沉默、误读、反制和情绪泄露必须可见"},
			ChapterApplication: "关键对白先判断是谈判、审问、求助、告解、互怼、汇报、回避、沉默压迫还是误会升级；再决定对白/动作/物件/沉默/环境声谁先入场。",
			ProofTargets:       []string{"dialogue_scene_blueprints", "voice_logic", "character_stage_records"},
			FailureRisk:        "所有场景同一种对白结构、配角像工具、对白变成设定问答或系统菜单。",
		},
		{
			Source:             "web_reference_guidelines",
			RuleFocus:          []string{"网络资料只做现实支架", "热梗必须有角色载体和预算", "人物职业/资源/交通细节要记录来源与日期"},
			ChapterApplication: "读取项目 web_reference_brief 或当轮 web_search 证据，把生活、职业、资源、交通和平台细节转成界面、账单、外放声、群聊、路线耗时或角色误判。",
			ProofTargets:       []string{"external_reference_plan", "trend_language_plan", "grounding_details"},
			FailureRisk:        "热梗硬贴、网页摘要化、当代细节无来源、交通耗时无依据导致 AI 味和出戏感。",
		},
	}
}

func zeroAntiAIPlan(project zeroInitProject) domain.AntiAIExecutionPlan {
	if zeroIsSecondAlgorithmProject(project) {
		return domain.AntiAIExecutionPlan{
			RiskSignals: []string{"第一章像项目复盘或规范文章", "许闻溪口吻变成作者总结", "AI/岗位变化解释过整齐", "连续对话每句都在推进信息", "章末抽象金句或问号"},
			CounterMoves: []string{
				"把职场压力拆进讲稿批注、手机震动、座位距离、同事眼神和停顿",
				"让许闻溪先忍一下、误判一下或说半句，再通过动作补回主动权",
				"AI相关信息只露出角色能接触的一小块，不写成行业说明",
				"章末落到新消息、确认单措辞、岗位入口、客户反馈或关系姿态变化",
			},
			SentenceRhythmPolicy: "长短句按现场尴尬、压抑和反击换挡；抽象判断后必须回到动作、物件、声音、对白或选择后果。",
			ObjectResponseBudget: "屏幕/讲稿/消息/表格回应默认不超过4次，至少一次延迟、遮挡或被人打断，避免每句重话都立刻显字。",
			DialogueFunctionPlan: "对白只承担试探、遮掩、压制、让步、反制和关系温度；禁止普通人突然讲懂行业趋势或女主成长主题。",
			ReviewChecks:         []string{"是否有整齐三连/清单感", "是否用物件和动作承载压力", "删掉说话人后角色声口是否仍成立", "章尾是否具体而非金句", "男主是否过早救场"},
		}
	}
	return domain.AntiAIExecutionPlan{
		RiskSignals: []string{"第一章规则解释过整齐", "主角风控口吻变成作者总结", "纸面条款像完整 ToS", "章末抽象金句或问号"},
		CounterMoves: []string{
			"把规则拆进可见物件、动作后果和角色误判",
			"让主角只问交易内容、权利边界和最低证据，不替读者总结世界观",
			"纸面/屏幕信息保留残缺、错位、补字或被打断",
			"章末落到新字、新账、新选择或物件状态变化",
		},
		SentenceRhythmPolicy: "长短句按现场压力换挡；抽象判断后必须回到动作、物件、声音、对话或选择后果。",
		ObjectResponseBudget: "纸面/门牌/屏幕/灯光回应默认不超过4次，至少一次延迟或静默，避免每句重话都立刻显字。",
		DialogueFunctionPlan: "对白只承担试探、拒绝、求救、隐瞒和关系压力；禁止普通人突然讲懂规则。",
		ReviewChecks:         []string{"是否有整齐三连/清单感", "是否用物件和动作承载规则", "删掉说话人后主角声口是否仍成立", "章尾是否具体而非金句"},
	}
}

func zeroExternalReferencePlan(project zeroInitProject) []domain.ExternalReferencePlan {
	if zeroIsSecondAlgorithmProject(project) {
		return []domain.ExternalReferencePlan{{
			QueryOrNeed:          "第一章需要AI改变岗位、女性职场困境、运营/客服/培训协作、城市通勤和家庭照护现实支架时，正式计划必须引用项目 web_reference_brief 的 retrieved_at，或记录当轮 web_search 证据。",
			SourceType:           "project_web_reference_brief",
			SourceRefs:           []string{"reference_pack.references.web_reference_guidelines", "meta/web_reference_brief.md", "selected_memory.rag_recall(若命中相关资料)"},
			RetrievedAt:          "正式计划读取 meta/web_reference_brief.md 后填写具体日期；若简报缺失则先检索并记录当轮日期",
			FreshnessRequirement: "AI工具、就业变化和平台规则优先使用最新资料；稳定职场流程和通勤照护可用近一年资料。",
			UsableDetails:        []string{"会议投屏/讲稿批注/企业微信", "AI话术模板或自动排班界面", "岗位沟通材料和培训报名入口", "通勤、复诊、社区商户和下班后生活细节"},
			TransformationRule:   "转成角色能看见/听见/操作的物件、界面、声音、动作迟疑或误判，不把网页摘要搬进旁白。",
			DoNotUse:             []string{"无来源热词串", "过时AI恐慌梗", "平台政策/价格等未核实事实", "破坏女性成长线的段子化处理"},
		}}
	}
	return []domain.ExternalReferencePlan{{
		QueryOrNeed:          "第一章需要当代生活、平台、物业、支付、角色职业/资源、城市交通或热梗现实支架时，正式计划必须引用项目 web_reference_brief 的 retrieved_at，或记录当轮 web_search 证据。",
		SourceType:           "project_web_reference_brief",
		SourceRefs:           []string{"reference_pack.references.web_reference_guidelines", "meta/web_reference_brief.md", "selected_memory.rag_recall(若命中相关资料)"},
		RetrievedAt:          "正式计划读取 meta/web_reference_brief.md 后填写具体日期；若简报缺失则先检索并记录当轮日期",
		FreshnessRequirement: "最新资料优先；稳定生活流程可用近一年资料；热梗需确认仍在流通",
		UsableDetails:        []string{"物业/小区群/支付失败/账单界面", "手机外放或群聊短句", "排队、门禁、收据、客服话术等生活锚点", "角色职业资源、通勤路线和现实耗时"},
		TransformationRule:   "转成角色能看见/听见/操作的物件、界面、声音、队列或误判，不把网页摘要搬进旁白。",
		DoNotUse:             []string{"无来源热梗串", "过时梗硬贴", "平台政策/价格等未核实事实", "破坏本书恐怖语气的段子"},
	}}
}

func zeroTrendLanguagePlan(project zeroInitProject) []domain.TrendLanguagePlan {
	if zeroIsSecondAlgorithmProject(project) {
		return []domain.TrendLanguagePlan{{
			Item:             "none-or-project-brief-item",
			SourceContext:    "项目 web_reference_brief 的具体条目或正式写作时的最新网络检索结果",
			CharacterCarrier: "默认由同事半句玩笑、群聊消息、培训课现场或客户反馈承载，不由旁白承载",
			SceneFunction:    "只做时代纹理、尴尬感、误判或关系摩擦，不解释AI趋势",
			UsageBudget:      "第一章默认0-1处，最多2处半截反应；禁止热词串",
			ForbiddenUsage:   "主角关键判断、章末钩子、作者旁白、男主评价和成长金句里不用热梗",
		}}
	}
	return []domain.TrendLanguagePlan{{
		Item:             "none-or-project-brief-item",
		SourceContext:    "项目 web_reference_brief 的具体条目或正式写作时的最新网络检索结果",
		CharacterCarrier: "默认由群体角色、手机外放、群聊/物业通知或配角半句反应承载，不由旁白承载",
		SceneFunction:    "只做时代纹理、嘈杂感、误判或关系摩擦，不解释规则",
		UsageBudget:      "第一章默认0-1处，最多2处半截反应；禁止梗串",
		ForbiddenUsage:   "主角关键判断、恐怖规则条款、章末钩子、作者旁白和金句里不用热梗",
	}}
}

func zeroReaderEntertainmentPlan(project zeroInitProject) domain.ReaderEntertainmentPlan {
	if zeroIsSecondAlgorithmProject(project) {
		return domain.ReaderEntertainmentPlan{
			OpeningBeat:          "前200字让许闻溪在具体工作现场遭遇一句轻慢、一次权限卡点或一个结果被夺走，立刻逼出她的临场选择。",
			HumorBeats:           []string{"同事把规范话说得过分顺口，现实小动作当场拆台", "许闻溪用克制的短句反问，对方误以为她在配合，随后被结果反噬"},
			ImmediatePayoffs:     []string{"主角保住一个可见结果或证据", "轻视她的人不得不改变一句话、一个动作或一次资源分配"},
			ProcedureCompression: "会议、审批、培训和工具流程只保留会改变责任、证据或关系的节点，其余一句带过。",
			CompanionVoiceBeat:   "由同事、朋友或男主用有立场的半句反应承载陪伴，不替许闻溪给答案。",
			ForbiddenComedy:      []string{"拿真实困境当笑料", "网络梗串", "配角集体降智", "用金句代替结果"},
		}
	}
	return domain.ReaderEntertainmentPlan{
		OpeningBeat:          "前200字让主角在具体现场遭遇尴尬、冲突、误会或规则反噬，并当场做出不完美但主动的回应。",
		HumorBeats:           []string{"配角的误解或嘴欠反应被现场结果反打", "系统、搭档或朋友用短促接话制造第二层反应，而不是讲规则"},
		ImmediatePayoffs:     []string{"本章中段前出现一次读者能看见的小胜或能力验证", "章末前让钱、结果、面子、关系或权限至少再改变一次"},
		ProcedureCompression: "交易、登记、核验、交通和安装等流程只保留发生拒绝、反转、笑点、代价或关系变化的节点。",
		CompanionVoiceBeat:   "系统、搭档或朋友至少有一次带个人声口的短回应，能接话、吐槽、提醒或撑腰，但不替主角做决定。",
		ForbiddenComedy:      []string{"梗串", "旁白硬贴热词", "配角集体降智", "在章末用热梗代替追读后果"},
	}
}

func zeroGroundingDetails(project zeroInitProject) []domain.GroundingDetailPlan {
	scene := zeroFirstNonEmpty(zeroFirstScene(project.FirstChapter), "第一章主场景")
	if zeroIsSecondAlgorithmProject(project) {
		return []domain.GroundingDetailPlan{
			{
				Detail:        "会议材料、工单、AI话术模板和岗位沟通材料先像真实工作物，再承载压力。",
				SourceRef:     "web_reference_guidelines",
				TransformedAs: "讲稿批注、投屏页码、企业微信消息、表格空格、排班状态或被删改的一句话",
				SceneAnchor:   scene,
			},
			{
				Detail:        "家庭照护和城市生活只做人物选择的现实成本，不抢主线解释。",
				SourceRef:     "web_reference_guidelines",
				TransformedAs: "没接到的电话、复诊提醒、通勤时间、保温杯、包里折起的单子或母亲一句轻描淡写的话",
				SceneAnchor:   "许闻溪能摸到、听见或临时压下去的生活细节",
			},
		}
	}
	return []domain.GroundingDetailPlan{
		{
			Detail:        "现实账单/通知/门禁/支付失败应先像真实载体，再发生异常。",
			SourceRef:     "web_reference_guidelines",
			TransformedAs: "纸面栏位、屏幕弹窗、门牌状态、错位补字或被打断的条款",
			SceneAnchor:   scene,
		},
		{
			Detail:        "当代生活噪声只做背景压力，不抢主角判断。",
			SourceRef:     "web_reference_guidelines",
			TransformedAs: "楼道外放声、小区群短提示、邻居半句反应或物业口吻",
			SceneAnchor:   "可复核的纸面/物件/空间证据",
		},
	}
}

func zeroOffscreenStage(project zeroInitProject, states []domain.CharacterSimulationState) []domain.CharacterStageRecord {
	scene := zeroFirstNonEmpty(zeroFirstScene(project.FirstChapter), "第一章主场景")
	second := zeroIsSecondAlgorithmProject(project)
	var records []domain.CharacterStageRecord
	for i, state := range states {
		if strings.TrimSpace(state.Character) == "" {
			continue
		}
		status := "存活/状态待第一章正文确认"
		environment := zeroFirstNonEmpty(project.FirstChapter.CoreEvent, "第一章规则/关系/资源压力现场")
		deathState := "未死亡/未确认；若死亡、失踪或异化，必须安排传回主角路径"
		notice := "通过正文可见行动、通信、账单、目击者或后续台账传回主角"
		if second {
			status = "工作/生活状态待第一章正文确认"
			environment = zeroFirstNonEmpty(project.FirstChapter.CoreEvent, "第一章AI提效/岗位/关系压力现场")
			deathState = "无极端状态；若发生离职、调岗、缺席、关系降温或家庭照护变化，必须安排传回主角路径"
			notice = "通过正文可见行动、消息、会议记录、工单、客户反馈、目击者或后续台账传回主角"
		}
		records = append(records, domain.CharacterStageRecord{
			Chapter:             1,
			Character:           state.Character,
			Time:                "第一章开场至章末",
			Location:            scene,
			Status:              status,
			Environment:         environment,
			CurrentAction:       zeroFirstNonEmpty(state.LikelyAction, "按当前信息试探并保护自身边界"),
			Pressure:            zeroFirstNonEmpty(state.Pressure, "第一章压力尚未明确"),
			Decision:            "只基于可见证据做有限选择，不提前掌握完整机制。",
			MistakeOrMisbelief:  zeroFirstNonEmpty(strings.Join(state.PlausibleMistakes, "；"), "可能把异常误判成普通流程。"),
			KnowledgeBoundary:   zeroFirstNonEmpty(strings.Join(state.KnowledgeLedger.ForbiddenKnowledge, "；"), "不能知道未公开后台谜底。"),
			VisibleInChapter:    i == 0,
			Evidence:            "zero-init 初始角色动态与第一章大纲",
			Transport:           "按开局地点原地行动；跨地点需 book_world 路线",
			TravelTime:          "未发生跨地点移动时为0；若正文移动，必须补现实耗时",
			MeetingConstraint:   "主角未通信/未见证/无证据时不能知道该角色线；非主角不能随叫随到",
			PersonalityDelta:    "第一章压力会测试其行动倾向，变化需在 commit 后回填",
			DeathState:          deathState,
			ProtagonistNotice:   notice,
			TimelineConsistency: "与第一章主线同步；若正文未展示，后续回归必须承接此处压力和误判。",
			NextPotential:       "携带本章未解决的压力、误判或资源缺口回归。",
			Tags:                []string{"zero_init", "character_stage", state.Character},
		})
	}
	if len(records) == 0 {
		environment := zeroFirstNonEmpty(project.FirstChapter.CoreEvent, "第一章主压力")
		currentAction := "在规则压力下做有限选择。"
		mistake := "可能误判规则代价。"
		knowledge := "不知道完整后台机制。"
		if second {
			currentAction = "在AI提效、岗位变化或关系压力下做有限选择。"
			mistake = "可能误判组织取舍或对方真实意图。"
			knowledge = "不知道完整组织取舍。"
		}
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
	if zeroIsSecondAlgorithmProject(project) {
		return []domain.EnvironmentSignal{{
			Place:              zeroFirstNonEmpty(zeroFirstScene(project.FirstChapter), "第一章主场景"),
			VisibleState:       "有可见工作物、空间边界、消息提示、材料版本或人群反应变化。",
			InformationCarried: "承载AI提效压力、岗位话语权、关系边界或资源短缺。",
			PressureApplied:    "迫使角色核验证据、拒绝/接受承诺，或承担一个可见职业/关系成本。",
			ExpectedChange:     "章末同一地点/材料/消息/关系姿态改变，成为下一章可承接证据。",
		}}
	}
	return []domain.EnvironmentSignal{{
		Place:              zeroFirstNonEmpty(zeroFirstScene(project.FirstChapter), "第一章主场景"),
		VisibleState:       "有可见异常、纸面/物件/空间边界或秩序变化。",
		InformationCarried: "承载世界规则第一次露面、关系压力或资源短缺。",
		PressureApplied:    "迫使角色核验证据、拒绝/接受交换，或承担一个可见代价。",
		ExpectedChange:     "章末同一地点/物件/空间状态改变，成为下一章可承接证据。",
	}}
}
