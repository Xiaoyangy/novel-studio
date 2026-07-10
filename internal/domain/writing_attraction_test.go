package domain

import "testing"

func TestProjectStyleRequests(t *testing.T) {
	if !TrendLanguageRequested("轻松搞笑，热梗可少量点缀") {
		t.Fatal("expected trend language request")
	}
	if TrendLanguageRequested("禁止使用热梗") {
		t.Fatal("forbidden trend language must win")
	}
	if !ReaderEntertainmentRequested("男频轻松搞笑爽文，强情绪兑现") {
		t.Fatal("expected entertainment request")
	}
	if !SystemCompanionVoiceRequested("系统会和男主交流解闷，不是一个纯下达任务的机器人，且始终支持主角") {
		t.Fatal("expected companion-system request")
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
