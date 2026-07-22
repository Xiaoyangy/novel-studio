package domain

import "testing"

func TestProjectStyleRequests(t *testing.T) {
	if !TrendLanguageRequested("轻松搞笑，热梗可少量点缀") {
		t.Fatal("expected trend language request")
	}
	if TrendLanguageRequested("禁止使用热梗") {
		t.Fatal("forbidden trend language must win")
	}
	if TrendLanguageRequested("标题允许口语，但不堆网络梗、不连续套同一句型") {
		t.Fatal("an anti-stacking ceiling must not become a mandatory trend-language request")
	}
	if !TrendLanguageRequested("每章最多用一个热梗，但不堆网络梗") {
		t.Fatal("an anti-stacking ceiling must not cancel a separate explicit trend-language request")
	}
	if !ReaderEntertainmentRequested("男频轻松搞笑爽文，强情绪兑现") {
		t.Fatal("expected entertainment request")
	}
	if !SystemCompanionVoiceRequested("系统会和男主交流解闷，不是一个纯下达任务的机器人，且始终支持主角") {
		t.Fatal("expected companion-system request")
	}
	if !SystemCompanionVoiceForbidden("本书系统不能聊天，纯任务机器人即可。") {
		t.Fatal("expected explicit companion-system prohibition")
	}
}

func TestTrendLanguageAvoidanceIsNotARequest(t *testing.T) {
	// A contract that lists 网络热梗 among things to avoid must NOT activate the
	// trend-language requirement (previously a bare substring hit on 热梗 made
	// every chapter's project-all demand a trend_language_plan and fail).
	avoiding := []string{
		"避免滥用唯美修辞、网络热梗、说教对白、同义抒情反复、百科搬运、流程报告和证据清单式叙述",
		"全书克制、具象，远离网络热梗与说教对白",
		"整体成年克制，杜绝网络热梗",
	}
	for _, text := range avoiding {
		if TrendLanguageRequested(text) {
			t.Fatalf("avoidance wording must not be read as a trend-language request: %q", text)
		}
	}
	// Genuine requests must still register.
	for _, text := range []string{"需要网络热梗融入", "可以适当加入轻梗调节气氛", "每章一个热梗，但不堆网络梗"} {
		if !TrendLanguageRequested(text) {
			t.Fatalf("explicit trend-language request must register: %q", text)
		}
	}
}

func TestSystemCompanionVoiceRequestedFromWorldRule(t *testing.T) {
	if !SystemCompanionVoiceRequested("系统是主角的稳定吐槽搭子和情绪支持者，会聊天，也会提醒风险。") {
		t.Fatal("world-rule companion wording should enable the system voice contract")
	}
}

func TestSystemCompanionFeedbackContradiction(t *testing.T) {
	for _, text := range []string{
		"系统口吻偏暖，建议强化系统冷硬感。",
		"系统不予回应，只做冷硬的规则重申，减少系统拟人化玩笑。",
		"建议系统发送一条乱码或重复提示，制造断联感。",
		"调整系统提示语的语气，保持冷感，如去掉'^_^'，改用纯文本进度条式通知。",
		"系统类信息必须绑定界面/载体，禁止以【】作为独立叙事段。",
		"系统以【】直接嵌入叙事，缺乏视角锚定。",
		"系统对白只能以状态报告、数据更新、错误日志等形式出现。",
		"将系统提示改为纯数据反馈，只提供事实，不提供感悟。",
		"系统消息避免添加解释性后半句，如‘回来拿筷子的那位不扣你’。",
	} {
		if !SystemCompanionFeedbackContradicts(text) {
			t.Fatalf("expected contradiction: %q", text)
		}
	}
	for _, text := range []string{
		"系统保持短促接话，但不要连续抛三个梗。",
		"不能把系统写成冷硬静默的任务机器人。",
		"沈知遥的问话可以少一句解释。",
	} {
		if SystemCompanionFeedbackContradicts(text) {
			t.Fatalf("unexpected contradiction: %q", text)
		}
	}
}

func TestSystemCompanionFeedbackContradictsQuotedBracketEvidenceWithoutSystemWord(t *testing.T) {
	for _, text := range []string{
		"AI证据：‘【这一笔先别记完。】’突然出现，未交代是界面文字还是意识流，打断叙述视角。",
		"【奖励到账。】缺乏视角锚定。",
	} {
		if !SystemCompanionFeedbackContradicts(text) {
			t.Fatalf("quoted bracket-interface contradiction not detected: %q", text)
		}
	}
	if SystemCompanionFeedbackContradicts("【奖励到账。】这句很简洁。") {
		t.Fatal("ordinary bracket-line feedback must not be rejected")
	}
}

func TestTrendLanguagePlanRejectsGuaAsSoundEffect(t *testing.T) {
	bad := []TrendLanguagePlan{{
		Item: "呱，", CharacterCarrier: "赵航", SceneFunction: "用一拍突兀的拟声制造停顿，随后反击", UsageBudget: "一次",
	}}
	if problems := TrendLanguagePlanProblems(bad); len(problems) == 0 {
		t.Fatal("呱， must not be planned as a sound effect")
	}
	good := []TrendLanguagePlan{{
		Item: "呱，", CharacterCarrier: "赵航以网络语气词起手", SceneFunction: "后接完整吐槽，打断亲戚说教，禁止写成拟声动作", UsageBudget: "一次",
	}}
	if problems := TrendLanguagePlanProblems(good); len(problems) != 0 {
		t.Fatalf("valid discourse opener was rejected: %v", problems)
	}
}

func TestChapterAttractionPlanReady(t *testing.T) {
	plan := ChapterPlan{Chapter: 1, CausalSimulation: ChapterCausalSimulation{
		TrendLanguage: []TrendLanguagePlan{{
			Item: "呱", SourceContext: "朋友口语", CharacterCarrier: "赵航",
			SceneFunction: "误会反应", UsageBudget: "1次", ForbiddenUsage: "不用在章末",
		}},
		EntertainmentPlan: ReaderEntertainmentPlan{
			OpeningBeat:          "饭桌当众点破失业",
			HumorBeats:           []string{"赵航误会后被瞪", "系统接住林澈自嘲"},
			ImmediatePayoffs:     []string{"系统验证", "第一笔消费见效"},
			ProcedureCompression: "压缩询价和安装",
			CompanionVoiceBeat:   "系统短促撑腰",
			ForbiddenComedy:      []string{"不串梗"},
		},
		LongformOpening: LongformOpeningDesign{
			TargetReader: "大众男频读者", OpeningHook: "失业当场绑定系统", SerialEngine: "县城项目逐级扩张",
			ReaderRewardLoop:  []string{"小项目即时见效"},
			LongRangePromises: []LongRangePromise{{Promise: "县城翻新", FirstChapterSeed: "夜市灯", PayoffHorizon: "全书"}},
			RevealBudget:      []string{"不解释系统来源"}, FirstChapterProof: []string{"第一笔消费见效"}, RetentionRisks: []string{"流程过长"},
		},
	}}
	plan.CausalSimulation.EntertainmentPlan.CompanionVoiceBeat = "系统会接话吐槽，也始终支持主角"
	if !ChapterAttractionPlanReady(plan, true, true, true, true) {
		t.Fatal("complete attraction plan should pass")
	}
	plan.CausalSimulation.EntertainmentPlan.HumorBeats = []string{"只有一个笑点"}
	if ChapterAttractionPlanReady(plan, true, true, true, true) {
		t.Fatal("single humor beat must not pass")
	}
	plan.CausalSimulation.EntertainmentPlan.HumorBeats = []string{"笑点一", "笑点二"}
	plan.CausalSimulation.EntertainmentPlan.ForbiddenComedy = []string{"把系统写成会吐槽的聊天伙伴"}
	if ChapterAttractionPlanReady(plan, true, true, true, true) {
		t.Fatal("plan that contradicts the requested system voice must fail")
	}
}
