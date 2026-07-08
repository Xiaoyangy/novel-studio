package main

import (
	"fmt"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

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
	// 主角是关系枢纽，应对每个关键配角都有契约；非主角对主角有契约。
	// 旧逻辑只给主角找单个 FirstCast 对手，第一章大纲没点名匹配时主角契约为空。
	counterparts := zeroCounterpartsForCharacter(project, c)
	relationshipForces := []string{"当前章的主要牵引来自现场规则、资源压力和可见证据。"}
	relationshipContracts := []domain.CharacterRelationshipContract{}
	if len(counterparts) > 0 {
		relationshipForces = relationshipForces[:0]
		for _, cp := range counterparts {
			relationshipForces = append(relationshipForces, fmt.Sprintf("与%s的信任、债务或信息差必须在行动中体现。", cp))
			relationshipContracts = append(relationshipContracts, domain.CharacterRelationshipContract{
				Counterpart:       cp,
				Trust:             "零章基线：未因正文事件新增信任。",
				Debt:              "无新增债务，第一章若发生交换必须入账。",
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
	return domain.CharacterSimulationState{
		Character:          c.Name,
		CurrentGoal:        fmt.Sprintf("以“%s”的身份在第一章拿到一个可验证的下一步目标。", role),
		Pressure:           fmt.Sprintf("第一章核心事件“%s”会检验其性格、资源和关系边界。", zeroFirstNonEmpty(project.FirstChapter.CoreEvent, project.FirstChapter.Title)),
		Resources:          []string{"角色卡既有经验", "第一章可见证据", "可被审计的关系/资源台账"},
		RelationshipForces: relationshipForces,
		Secrets:            []string{"零章只允许保留角色卡或大纲已授权的秘密，未授权秘密写入 information_gaps。"},
		Misbeliefs:         []string{"开章时可能误判第一章异常/压力的真实代价，正文需用证据修正。"},
		PrivateBoundary:    "不说出自己无法知道的信息；不为完成大纲突然解释、救场或转性。",
		ActionTendency:     actionBias,
		LikelyAction:       "先按最低证据标准试探局面，再用可见行动换取新信息或资源。",
		StateDeltaToTrack:  []string{"goal", "pressure", "resource", "relationship_contract", "knowledge", "emotion", "arc_axis"},
		CompetenceStage:    "开局阶段：只能使用角色卡里的经验和现场证据，不能预装最终答案。",
		SkillLimits: []string{
			"不知道第一章世界规则完整机制",
			"不能稳定判断所有收益和代价",
			"不能替其他角色提前知道后台秘密",
		},
		PlausibleMistakes: []string{
			"把异常先误认为普通流程、物业/合同/人情压力",
			"过度依赖旧经验导致判断过窄",
			"在压力下做出一次迟疑、错判或过度自保",
		},
		CorrectionTriggers: []string{
			"可见物件或规则反馈打破旧经验",
			"他人犯错带来可复核代价",
			"关系/资源损失迫使其修正判断",
		},
		KnowledgeLedger: domain.CharacterKnowledgeLedger{
			KnownFacts:         []string{fmt.Sprintf("自己是%s；角色卡基础描述：%s", role, desc)},
			UnknownFacts:       []string{"第一章世界规则的完整代价", "其他角色的真实意图", "章末钩子背后的答案"},
			Suspicions:         []string{"现场异常或关系压力不会无代价解除。"},
			FalseBeliefs:       []string{"以为只靠旧经验就能处理第一章问题。"},
			EvidenceSeen:       []string{"premise", "characters", "current_chapter_outline", "world_rules/book_world"},
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
			RiskAccepted:            "接受局部风险，不接受不可审计的全局解释。",
			ExpectedGain:            "获得下一步行动线索或改变一个关系/资源状态。",
			MinimumEvidenceRequired: "正文中必须出现可被读者复核的物件、台词、规则反应或关系动作。",
		},
		RelationshipContract: relationshipContracts,
		EmotionAppraisal: domain.CharacterEmotionAppraisal{
			TriggerEvent:         fmt.Sprintf("第一章核心事件触发：%s。", zeroFirstNonEmpty(project.FirstChapter.CoreEvent, project.FirstChapter.Title)),
			GoalImpact:           "迫使角色把静态目标转化成可见选择。",
			ThreatToValue:        "威胁其安全、责任、尊严、资源或关系边界。",
			VisibleExpression:    "用动作、停顿、询问、避让或交易呈现，不直接贴情绪标签。",
			SuppressedExpression: "隐瞒自己还不确定、害怕或想要交换的部分。",
			CopingStrategy:       "先缩小问题、核验证据、保留退路。",
			ActionPressure:       "必须在章内做出一个能改变状态的选择。",
			RelationshipEffect:   "选择会改变至少一条信任、债务、恐惧或依赖记录。",
		},
		ArcAxis: domain.CharacterArcAxis{
			Want:             zeroFirstNonEmpty(c.Arc, "拿到眼前问题的控制权。"),
			Need:             "学会用行动和证据承担选择后果，而不是停留在人设标签。",
			WoundOrGhost:     "角色卡中的创伤、执念或责任必须通过具体反应出现。",
			CoreLie:          "以为旧经验足以解释第一章的新压力。",
			ValueAxis:        arc,
			ArcStage:         "zero_chapter_baseline",
			PressureTest:     "第一章用规则、关系或资源短缺测试其行动倾向。",
			GrowthSignal:     "承认一个新信息，并为下一章留下主动目标。",
			RegressionSignal: "用漂亮判断、沉默或逃避替代真实选择。",
		},
	}
}

func zeroInitVoiceLogic(project zeroInitProject, c domain.Character) domain.CharacterVoiceLogic {
	return domain.CharacterVoiceLogic{
		Character:          c.Name,
		PersonalitySource:  zeroFirstNonEmpty(strings.Join(c.Traits, "、"), c.Description, c.Role),
		SpeechPrinciple:    "先服务本场目标和知识边界，再体现性格；不为喂设定突然全知。",
		SceneObjective:     fmt.Sprintf("围绕“%s”拿到信息、资源或关系位移。", zeroFirstNonEmpty(project.FirstChapter.CoreEvent, project.FirstChapter.Title)),
		HiddenSubtext:      "想保护自己的边界、筹码或弱点。",
		KnowledgeBoundary:  "只说自己能从角色卡、现场证据和已授权关系里推出来的话。",
		RelationshipStance: "试探多于交心；帮助、拒绝或讽刺都要有交易/情感/风险依据。",
		DictionAndRhythm:   "句长和停顿随压力变化；避免连续漂亮短句、金句反问和同质化冷静判断。",
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
	return map[string]any{
		"version":      1,
		"scope":        "zero_chapter_initial",
		"chapter":      0,
		"generated_at": project.GeneratedAt,
		"contracts":    contracts,
		"policy":       "第一章正文只能在这些基线之上新增信任、债务、恐惧、承诺、依赖或背叛记录；新增后必须通过审阅/回填进入正式 relationship_state。",
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
		priority := zeroReturnPriority(c, firstMention)
		suggestedChapter := firstMention
		if suggestedChapter == 0 && (priority == "required" || priority == "near_future") {
			suggestedChapter = 1
		}
		out[c.Name] = domain.CharacterReturnPlan{
			ReturnPriority:     priority,
			SuggestedChapter:   suggestedChapter,
			DueReason:          zeroReturnDueReason(c, firstMention),
			WithNewInformation: "回归时必须携带新信息、资源压力、关系账或未兑现承诺之一。",
			UpgradePotential:   "若做关键选择、建立债务或需要后续回归，升级为关键角色并补全动态字段。",
			RetireReason:       "若只是气氛/捧场/凑数，场景结束即退场，不进入长期台账。",
		}
	}
	return out
}

func zeroInitCrowdPolicy() map[string]any {
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
				ContinuityPolicy: "不进长期台账；若携带新信息或建立债务，立即升级为关键角色。",
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
	return zeroPrewriteStorycraftPlan{
		Version:            2,
		Scope:              "reusable_prewrite_storycraft",
		Project:            project.Name,
		Chapter:            1,
		GeneratedAt:        project.GeneratedAt,
		UsagePolicy:        "所有新正文都必须先把本计划转入 plan_chapter.causal_simulation：人物先有 Want/Lie/Need/Truth 和合理犯错，行动先有身体/情绪/关系/创伤/偏差/意义驱动，关系先有亲密阶段与边界，人物先有可识别外观，对话先按角色、场景、压力、情绪和关系选择 dialogue_mode、opening_strategy 与 objective_tactics，读者先有小胜与新债，离屏线先有证据回收路径，章末先有具体后果契约；正式写作可根据当轮网络检索和最新台账更新字段，但不能省略同类字段。",
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
	for i := 0; i < limit; i++ {
		entry := project.Outline[i]
		reward := zeroFirstNonEmpty(entry.CoreEvent, entry.Title, fmt.Sprintf("第%d章推进一个可见状态变化", i+1))
		cost := "奖励必须带出新代价、新债务、新误解或更深规则压力。"
		hook := zeroFirstNonEmpty(entry.Hook, "章末留下下一章能立刻承接的具体后果。")
		if i == 0 {
			reward = "第一章给出一个小胜：主角靠错误后的修正拿到暂缓、证据、收据、门牌变化或下一步权限。"
			cost = "小胜不是免费，必须留下资源、关系、安全感、债务或审核尾巴。"
		}
		ladder = append(ladder, domain.ReaderRewardStep{
			Chapter: i + 1,
			Reward:  reward,
			Cost:    cost,
			Hook:    hook,
		})
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
			Evidence:            "后续通过通信、账单、位置变化、证物、目击者或现场残留传回主角。",
			ProtagonistAccess:   "主角必须通过通信/亲见/证据传回/能力授权获得，不能默认知道。",
			ReturnTiming:        timing,
			DistortionOrMisread: "传回时可能被延迟、遮挡、误读或被角色出于自保隐瞒一部分。",
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
			Evidence:            "以账单、门牌、消息、收据、脚印、监控盲区或他人半句证词回到主角视角。",
			ProtagonistAccess:   "主角只能通过现场证据或后续通信得知。",
			ReturnTiming:        "第2章或下一次回到" + scene + "时",
			DistortionOrMisread: "证据先出现局部、错位或被误读，避免一次讲完规则。",
			ChapterToResolve:    2,
		})
	}
	return out
}

func zeroEndingContract(project zeroInitProject) domain.EndingConsequenceContract {
	anchor := zeroFirstNonEmpty(project.FirstChapter.Hook, zeroFirstScene(project.FirstChapter), project.FirstChapter.CoreEvent, "第一章可见证据")
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
		out = append(out, domain.DormantCharacterPolicy{
			Character:         "none",
			Status:            "当前零章未识别休眠角色。",
			Location:          "none",
			NoChangeReason:    "所有已识别角色都进入第一章初始动态推演；后续新角色引入时必须补独立 dossier。",
			TriggerCondition:  "新角色做关键选择、建立关系债务、携带信息或后续回归时触发。",
			KnowledgeBoundary: "主角默认不知道未引入角色。",
			NextCheck:         "每次 plan_chapter 前检查新角色需求。",
		})
	}
	return out
}

func zeroRealitySupportPlan(project zeroInitProject) []domain.RealitySupportPlan {
	scene := zeroFirstNonEmpty(zeroFirstScene(project.FirstChapter), "第一章主场景")
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
		primary := "恐惧"
		composite := "羞耻和警惕"
		if strings.Contains(c.Role, "反派") || strings.Contains(c.Role, "敌") {
			primary = "愤怒"
			composite = "贪婪、轻蔑和控制欲"
		} else if strings.Contains(c.Role, "妹") || strings.Contains(c.Role, "亲") || strings.Contains(c.Role, "父") || strings.Contains(c.Role, "母") {
			primary = "爱"
			composite = "担忧、内疚和依赖"
		}
		out = append(out, domain.CharacterEmotionalLogic{
			Character:               c.Name,
			PhysiologicalState:      "默认处于普通人能量与安全感基线；若本章发生饥饿、疲惫、疼痛、受伤或欲望刺激，正式 plan_chapter 必须改写。",
			ImmediateState:          "受故事开局时间、地点、最近一次强化和注意力焦点影响；凌晨/惊吓/等待/通话会改变决策速度。",
			BaselineMood:            zeroFirstNonEmpty(firstString(c.Traits), "紧绷"),
			PrimaryEmotion:          primary,
			CompositeEmotion:        composite,
			EmotionalTrigger:        pressure,
			GoalAppraisal:           "此事件被角色评估为会改变目标、预期、资源或边界，因而先引发情绪反应再推动行动。",
			BoundaryThreat:          "安全、尊严、身份叙事、关系义务或资源控制权被威胁。",
			RegulationStrategy:      "先压住情绪并转成可执行的小动作；压力过高时会出现转移、沉默、攻击或合理化。",
			DefenseMechanism:        "合理化/压抑/投射三选一按场景触发；正式章节需落到可见话术或动作。",
			CognitiveBias:           "损失厌恶与确认偏差优先；角色会更相信能保护既有叙事的证据。",
			ApproachAvoidance:       "趋近想要的安全/亲密/控制感，同时回避失控、羞耻、被抛弃或被吞并。",
			ShortLongTermTension:    "短期想止损，长期想守住身份、关系或成长目标。",
			SelfRelationshipTension: "自我需求会和亲情、债务、承诺、群体评价或权力关系冲突。",
			ConsciousReason:         zeroFirstNonEmpty(state.DecisionFrame.DecisionRule, "我是在做理性选择。"),
			HiddenReason:            zeroFirstNonEmpty(state.ArcAxis.WoundOrGhost, "真正原因来自旧伤、恐惧、爱、羞耻或不愿承认的依赖。"),
			MeaningNeed:             "想证明自己不是任人摆布的工具，并维持一套能解释痛苦的自我叙事。",
			Metacognition:           "能否意识到自己在冲动，要由本章压力决定；开局不默认高自控。",
			EmotionLedAction:        "行动先由情绪和关系牵引，再被认知包装成规则、交易或任务。",
			EventCompletionRole:     "本章事件必须通过该角色情绪失衡、压抑、误判或反向克制来完成，而不是只被外部事件推着走。",
			EvidenceInScene:         []string{"动作停顿", "语速变化", "视线/手部反应", "一次不完全理性的选择"},
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

func zeroRelationshipEmotionArcs(project zeroInitProject, states []domain.CharacterSimulationState) []domain.RelationshipEmotionArc {
	protagonist := zeroProtagonist(project.Characters)
	var out []domain.RelationshipEmotionArc
	for _, c := range project.Characters {
		if c.Name == "" || c.Name == protagonist.Name {
			continue
		}
		relType := zeroRelationshipType(c)
		out = append(out, domain.RelationshipEmotionArc{
			Pair:                         []string{protagonist.Name, c.Name},
			RelationshipType:             relType,
			CurrentBond:                  zeroRelationshipBond(relType),
			EmotionalWant:                "想从对方那里得到安全感、确认、资源、解释权、赦免或控制感；正式章节需按关系细化。",
			Fear:                         "害怕被拖累、被抛弃、被看穿、被背叛、被债务绑定或失去主导权。",
			PowerBalance:                 "信息、资源、情绪需求和规则权限不对等。",
			IntimacyStage:                zeroIntimacyStage(relType),
			TrustDebt:                    "信任从低位开始，任何帮助都要留下债务、亏欠、承诺或边界。",
			ConflictTrigger:              zeroFirstNonEmpty(project.FirstChapter.CoreEvent, "世界规则第一次施压"),
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

func zeroRelationshipType(c domain.Character) string {
	text := c.Role + " " + c.Description
	switch {
	case strings.Contains(text, "妹") || strings.Contains(text, "兄") || strings.Contains(text, "姐") || strings.Contains(text, "父") || strings.Contains(text, "母") || strings.Contains(text, "亲"):
		return "亲情"
	case strings.Contains(text, "恋") || strings.Contains(text, "暧昧") || strings.Contains(text, "女主") || strings.Contains(text, "男主") || strings.Contains(text, "伴侣"):
		return "恋爱/暧昧潜势"
	case strings.Contains(text, "反派") || strings.Contains(text, "敌") || strings.Contains(text, "压迫"):
		return "敌对/债务"
	case strings.Contains(text, "朋友") || strings.Contains(text, "搭档") || strings.Contains(text, "后勤") || strings.Contains(text, "合作"):
		return "合作/友谊"
	default:
		return "社会关系/潜在债务"
	}
}

func zeroRelationshipBond(relType string) string {
	switch relType {
	case "亲情":
		return "爱与责任并存，保护欲、内疚和控制边界会互相冲突。"
	case "恋爱/暧昧潜势":
		return "吸引尚未等于信任，亲密必须通过共同风险、边界尊重和真实选择推进。"
	case "敌对/债务":
		return "恐惧、贪婪、羞辱或支配欲维系关系。"
	case "合作/友谊":
		return "互利先于亲密，信任靠可见行动慢慢建立。"
	default:
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
			SignatureObject: zeroVisualObject(c),
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

func zeroInitWorldBackgroundPlan(project zeroInitProject) zeroWorldBackgroundPlan {
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
	return domain.ChapterPlan{
		Chapter:    1,
		Title:      zeroFirstNonEmpty(first.Title, "第一章"),
		Goal:       "建立主角初始压力、世界规则第一次露面、第一条可追踪行动目标。",
		Conflict:   zeroFirstNonEmpty(first.CoreEvent, "角色目标与世界/关系压力第一次相撞。"),
		Hook:       zeroFirstNonEmpty(first.Hook, "章末留下一个具体物件、选择或新事实作为下一章追问。"),
		EmotionArc: "从旧经验可控，到规则/关系压力失控，再到带代价的主动选择。",
		Notes:      "这是 zero-init 生成的写前推演草案，不是正式 plan_chapter 落盘。Writer 仍需在 --pipeline 内生成或核对正式 drafts/01.plan.json。",
		Contract: domain.ChapterContract{
			RequiredBeats: []string{
				"证明主角为什么非行动不可",
				"让一条世界规则通过可见后果第一次施压",
				"让至少一个关系/资源状态发生可回填变化",
			},
			ForbiddenMoves: []string{
				"提前解释全书谜底",
				"让角色凭空知道未公开信息",
				"用捧场角色完成关键解谜/救场/反杀",
			},
			ContinuityChecks: []string{"零章角色知识边界", "初始关系契约", "资源 pending/booked 状态", "第一章揭示预算"},
			EvaluationFocus:  []string{"人物行动是否由目标和压力推出", "对白是否符合 voice_logic 与 dialogue_scene_blueprints", "审核失败后是否用 review_refinement 重推演"},
			EmotionTarget:    "紧张、警惕、被迫选择后的追问感。",
			PayoffPoints:     []string{"第一章现场证据改变主角下一步", "章末状态相较开章发生可记录变化"},
			HookGoal:         "让读者想知道这个选择/物件/规则后果下一章会怎样继续收费。",
			SceneAnchors:     []string{zeroFirstNonEmpty(zeroFirstScene(first), "第一章主场景"), "可复核的纸面/物件/空间证据", "关系或资源交换动作"},
		},
		CausalSimulation: domain.ChapterCausalSimulation{
			ProjectPromise:  projectPromise,
			ChapterFunction: "整本书入口：证明人物系统、世界规则和连载发动机，而不是只触发怪事。",
			ContextSources: []string{
				"premise", "current_chapter_outline", "characters", "world_rules", "book_world",
				"simulation_restart_policy", "world_foundation", "character_dossiers",
				"meta/initial_character_dynamics", "relationship_state.initial", "meta/initial_resource_ledger",
				"foreshadow_ledger.initial", "meta/crowd_role_policy", "meta/prewrite_storycraft_plan", "meta/world_background_plan", "meta/initial_review_lessons",
				"reference_pack.references", "dialogue_writing", "web_reference_guidelines", "meta/web_reference_brief.md 或当轮 web_search 证据",
			},
			WritingNorms:     zeroWritingNorms(),
			AntiAIPlan:       zeroAntiAIPlan(),
			ExternalRefs:     zeroExternalReferencePlan(),
			TrendLanguage:    zeroTrendLanguagePlan(),
			GroundingDetails: zeroGroundingDetails(project),
			OffscreenStage:   zeroOffscreenStage(project, dynamics.Characters),
			LongformOpening: domain.LongformOpeningDesign{
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
			},
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
			InformationGaps:     []string{"世界规则完整机制", "对方真实意图", "章末钩子答案", "未授权后台秘密"},
			CausalBeats: []domain.CausalSimulationBeat{{
				Cause:           "第一章现场出现规则/关系/资源压力。",
				CharacterChoice: "主角按最低证据标准试探，不直接交出承诺或身份边界。",
				WorldResponse:   "世界/制度/关系给出可见后果。",
				StoryResult:     "主角得到下一步目标，同时付出关系、资源或安全感成本。",
			}},
			DecisionPoints:   []string{"是否相信新信息", "是否交换资源/承诺", "是否暴露私人边界", "是否承担章末代价"},
			OutcomeShift:     []string{"主角目标更具体", "至少一条关系/资源/知识状态变化", "伏笔种子从装饰变成下一章压力"},
			SceneConstraints: []string{"第三方/群体角色不能替主角完成关键判断", "环境信息必须通过可见物件、动作或规则后果呈现", "第一章不提前解释大谜底"},
		},
	}
}

func zeroWritingNorms() []domain.WritingNormApplication {
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

func zeroAntiAIPlan() domain.AntiAIExecutionPlan {
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

func zeroExternalReferencePlan() []domain.ExternalReferencePlan {
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

func zeroTrendLanguagePlan() []domain.TrendLanguagePlan {
	return []domain.TrendLanguagePlan{{
		Item:             "none-or-project-brief-item",
		SourceContext:    "项目 web_reference_brief 的具体条目或正式写作时的最新网络检索结果",
		CharacterCarrier: "默认由群体角色、手机外放、群聊/物业通知或配角半句反应承载，不由旁白承载",
		SceneFunction:    "只做时代纹理、嘈杂感、误判或关系摩擦，不解释规则",
		UsageBudget:      "第一章默认0-1处，最多2处半截反应；禁止梗串",
		ForbiddenUsage:   "主角关键判断、恐怖规则条款、章末钩子、作者旁白和金句里不用热梗",
	}}
}

func zeroGroundingDetails(project zeroInitProject) []domain.GroundingDetailPlan {
	scene := zeroFirstNonEmpty(zeroFirstScene(project.FirstChapter), "第一章主场景")
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
	var records []domain.CharacterStageRecord
	for i, state := range states {
		if strings.TrimSpace(state.Character) == "" {
			continue
		}
		records = append(records, domain.CharacterStageRecord{
			Chapter:             1,
			Character:           state.Character,
			Time:                "第一章开场至章末",
			Location:            scene,
			Status:              "存活/状态待第一章正文确认",
			Environment:         zeroFirstNonEmpty(project.FirstChapter.CoreEvent, "第一章规则/关系/资源压力现场"),
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
			DeathState:          "未死亡/未确认；若死亡、失踪或异化，必须安排传回主角路径",
			ProtagonistNotice:   "通过正文可见行动、通信、账单、目击者或后续台账传回主角",
			TimelineConsistency: "与第一章主线同步；若正文未展示，后续回归必须承接此处压力和误判。",
			NextPotential:       "携带本章未解决的压力、误判或资源缺口回归。",
			Tags:                []string{"zero_init", "character_stage", state.Character},
		})
	}
	if len(records) == 0 {
		records = append(records, domain.CharacterStageRecord{
			Chapter:             1,
			Character:           "关键角色",
			Time:                "第一章开场至章末",
			Location:            scene,
			Environment:         zeroFirstNonEmpty(project.FirstChapter.CoreEvent, "第一章主压力"),
			CurrentAction:       "在规则压力下做有限选择。",
			Pressure:            "第一章核心事件",
			Decision:            "不预装答案，只按证据修正。",
			MistakeOrMisbelief:  "可能误判规则代价。",
			KnowledgeBoundary:   "不知道完整后台机制。",
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
		Place:              zeroFirstNonEmpty(zeroFirstScene(project.FirstChapter), "第一章主场景"),
		VisibleState:       "有可见异常、纸面/物件/空间边界或秩序变化。",
		InformationCarried: "承载世界规则第一次露面、关系压力或资源短缺。",
		PressureApplied:    "迫使角色核验证据、拒绝/接受交换，或承担一个可见代价。",
		ExpectedChange:     "章末同一地点/物件/空间状态改变，成为下一章可承接证据。",
	}}
}
