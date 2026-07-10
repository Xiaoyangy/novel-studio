package tools

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chenhongyang/novel-studio/internal/aigc"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/rag"
	"github.com/chenhongyang/novel-studio/internal/rules"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func TestContextToolInjectsStyleStats(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	progress := &domain.Progress{TotalChapters: 10}
	body := "# 第N章\n他不是迟疑，而是恐惧。沉默了几息。像一道光。\n夜色落下。\n他走了。"
	for ch := 1; ch <= 6; ch++ {
		if err := st.Drafts.SaveFinalChapter(ch, body); err != nil {
			t.Fatalf("SaveFinalChapter: %v", err)
		}
		progress.CompletedChapters = append(progress.CompletedChapters, ch)
	}
	if err := st.Progress.Save(progress); err != nil {
		t.Fatalf("Save progress: %v", err)
	}

	tool := NewContextTool(st, References{}, "default")
	args, _ := json.Marshal(map[string]any{"chapter": 7})
	raw, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var payload struct {
		Episodic map[string]json.RawMessage `json:"episodic_memory"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	statsRaw, ok := payload.Episodic["style_stats"]
	if !ok {
		t.Fatalf("expected episodic_memory.style_stats, got keys %v", keysOf(payload.Episodic))
	}
	var stats struct {
		Chapters int `json:"chapters"`
		Patterns []struct {
			Name  string `json:"name"`
			Total int    `json:"total"`
		} `json:"patterns"`
	}
	if err := json.Unmarshal(statsRaw, &stats); err != nil {
		t.Fatalf("Unmarshal stats: %v", err)
	}
	if stats.Chapters != 6 || len(stats.Patterns) == 0 {
		t.Errorf("stats content: %+v", stats)
	}
	if usage, ok := payload.Episodic["_usage"]; !ok || len(usage) == 0 {
		t.Error("expected episodic_memory._usage annotation")
	}
}

func keysOf(m map[string]json.RawMessage) []string {
	var keys []string
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func TestContextToolInjectsEvolutionReport(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := st.Progress.Init("进化测试", 2); err != nil {
		t.Fatalf("Progress.Init: %v", err)
	}
	if err := st.Progress.MarkChapterComplete(1, 2800, "mystery", "quest"); err != nil {
		t.Fatalf("MarkChapterComplete: %v", err)
	}
	if _, err := st.RefreshEvolutionReport(nil, nil); err != nil {
		t.Fatalf("RefreshEvolutionReport: %v", err)
	}

	tool := NewContextTool(st, References{}, "default")
	args, _ := json.Marshal(map[string]any{"chapter": 2})
	raw, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var payload struct {
		Working map[string]json.RawMessage `json:"working_memory"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	reportRaw, ok := payload.Working["evolution_report"]
	if !ok {
		t.Fatalf("expected working_memory.evolution_report, got keys %v", keysOf(payload.Working))
	}
	var report struct {
		SourceArtifact string `json:"source_artifact"`
		Health         struct {
			Completed int `json:"completed"`
		} `json:"health"`
	}
	if err := json.Unmarshal(reportRaw, &report); err != nil {
		t.Fatalf("Unmarshal report: %v", err)
	}
	if report.SourceArtifact != "meta/evolution_report.md" || report.Health.Completed != 1 {
		t.Fatalf("unexpected evolution report snapshot: %+v", report)
	}
}

func TestContextToolReportsWarningsForCorruptedState(t *testing.T) {
	dir := t.TempDir()
	store := store.NewStore(dir)
	if err := store.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	if err := os.WriteFile(filepath.Join(dir, "outline.json"), []byte("{invalid"), 0o644); err != nil {
		t.Fatalf("write outline.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "meta", "progress.json"), []byte("{invalid"), 0o644); err != nil {
		t.Fatalf("write progress.json: %v", err)
	}

	tool := NewContextTool(store, References{}, "default")
	args, err := json.Marshal(map[string]any{"chapter": 2})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var payload struct {
		Warnings []string `json:"_warnings"`
		Summary  string   `json:"_loading_summary"`
	}
	if err := json.Unmarshal(result, &payload); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(payload.Warnings) == 0 {
		t.Fatal("expected context warnings for corrupted files")
	}
	if !containsWarning(payload.Warnings, "outline") {
		t.Fatalf("expected outline warning, got %v", payload.Warnings)
	}
	if !containsWarning(payload.Warnings, "progress") {
		t.Fatalf("expected progress warning, got %v", payload.Warnings)
	}
	if !strings.Contains(payload.Summary, "告警:") {
		t.Fatalf("expected loading summary to contain warning count, got %q", payload.Summary)
	}
}

func containsWarning(warnings []string, key string) bool {
	for _, warning := range warnings {
		if strings.Contains(warning, key) {
			return true
		}
	}
	return false
}

func TestContextToolChapterModeIncludesWorkingAndReferenceFields(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Outline.SavePremise(`## 题材和基调
少年成长，偏紧张压迫。

## 题材定位
少年升级流

## 核心冲突
主角必须在宗门竞争中活下来。

## 主角目标
进入内门。

## 终局方向
成为真正的执棋者。

## 写作禁区
不提前揭露师尊真相。

## 差异化卖点
弱者逆袭。

## 差异化钩子
每阶段都要用更高代价换成长。

## 核心兑现承诺
持续兑现危机与突破。

## 故事引擎
试炼、资源争夺与身份升级共同推进。

## 中段转折
主角被迫转向另一条修行路线。
`); err != nil {
		t.Fatalf("SavePremise: %v", err)
	}
	if err := s.Outline.SaveOutline([]domain.OutlineEntry{
		{Chapter: 1, Title: "入门", CoreEvent: "主角进入宗门", Scenes: []string{"拜师", "立誓"}},
		{Chapter: 2, Title: "试炼", CoreEvent: "参加外门试炼", Scenes: []string{"集合", "出发"}},
		{Chapter: 3, Title: "分组", CoreEvent: "试炼分组出现争议", Scenes: []string{"名单复核", "队友冲突"}},
		{Chapter: 4, Title: "旧伤", CoreEvent: "旧伤影响第一场胜负", Scenes: []string{"压制伤势", "误判对手"}},
		{Chapter: 5, Title: "内门帖", CoreEvent: "内门试炼邀请露出代价", Scenes: []string{"帖文显字", "拒绝捷径"}},
	}); err != nil {
		t.Fatalf("SaveOutline: %v", err)
	}
	if err := s.Characters.Save([]domain.Character{
		{Name: "林砚", Role: "主角", Description: "少年修士", Arc: "成长", Traits: []string{"冷静"}},
	}); err != nil {
		t.Fatalf("SaveCharacters: %v", err)
	}
	if err := s.World.SaveWorldRules([]domain.WorldRule{
		{Category: "magic", Rule: "灵气可以炼化", Boundary: "凡人不可直接驾驭"},
	}); err != nil {
		t.Fatalf("SaveWorldRules: %v", err)
	}
	if err := s.SaveWorldFoundation(domain.WorldFoundation{
		Version: 1,
		Project: "test",
		StoryStart: domain.StoryStart{
			AbsoluteTime: "第一章开场",
			StoryClock:   "ch001-start",
			Location:     "外门山门",
			Description:  "试炼入口开启",
		},
		IronLaws: []domain.WorldIronLaw{{
			ID:       "law-travel",
			Name:     "移动耗时",
			Rule:     "角色不能瞬移",
			Boundary: "无通行符不得跨场景赶到",
		}},
		KnowledgePolicy: "主角不知道场外时间线",
	}); err != nil {
		t.Fatalf("SaveWorldFoundation: %v", err)
	}
	if err := s.SaveCharacterDossier(domain.CharacterDossier{
		Version:   1,
		Character: "林砚",
		Role:      "主角",
		Profile: domain.CharacterDossierProfile{
			Description: "少年修士",
			Backstory:   "旧伤未愈",
		},
		LifeAnchors: []domain.LifeAnchor{{Kind: "开局位置", Place: "外门山门"}},
		CommunicationBoundary: domain.CommunicationBoundary{
			CanContactProtagonist: true,
			Channels:              []string{"自我行动"},
			InfoAllowed:           "只知道亲历信息",
		},
		KnowledgeBoundary: "不知道执事后台安排",
		CurrentAtStoryStart: domain.CharacterStartState{
			Time:     "第一章末",
			Location: "外门山门",
			Status:   "存活",
		},
	}); err != nil {
		t.Fatalf("SaveCharacterDossier: %v", err)
	}
	if err := s.Progress.Init("test", 2); err != nil {
		t.Fatalf("InitProgress: %v", err)
	}
	if err := s.Summaries.SaveSummary(domain.ChapterSummary{
		Chapter:    1,
		Summary:    "主角拜入宗门，确立目标。",
		Characters: []string{"林砚"},
		KeyEvents:  []string{"拜师"},
	}); err != nil {
		t.Fatalf("SaveSummary: %v", err)
	}
	if err := s.Drafts.SaveFinalChapter(1, "第一章正文结尾，留下试炼悬念。"); err != nil {
		t.Fatalf("SaveFinalChapter: %v", err)
	}
	if err := s.SaveCharacterStageRecords(1, []domain.CharacterStageRecord{{
		Character:           "林砚",
		Time:                "第一章末",
		Location:            "外门山门",
		Environment:         "缺角试炼令被门槛灵纹承认，但登记口即将关闭",
		CurrentAction:       "压住旧伤，保留名册异常不当场解释",
		Pressure:            "守门弟子质疑资格，同门围观",
		Decision:            "先保住入场资格，把名册异常留到第二章复核",
		MistakeOrMisbelief:  "误以为名册异常只是登记口疏漏",
		KnowledgeBoundary:   "不知道内门执事已提前调换分组",
		VisibleInChapter:    true,
		Evidence:            "第一章正文结尾，留下试炼悬念",
		TimelineConsistency: "发生在第二章试炼集合前",
		NextPotential:       "第二章从名册复核自然接入分组争议",
		Tags:                []string{"主角", "试炼"},
	}}); err != nil {
		t.Fatalf("SaveCharacterStageRecords: %v", err)
	}
	if err := s.Drafts.SaveChapterPlan(domain.ChapterPlan{
		Chapter: 2,
		Title:   "试炼",
		Goal:    "通过第一关",
		Contract: domain.ChapterContract{
			RequiredBeats:    []string{"必须让主角通过第一关", "必须埋下内门试炼邀请"},
			ForbiddenMoves:   []string{"不能提前揭露师尊真实身份"},
			ContinuityChecks: []string{"主角左臂旧伤仍未痊愈"},
			EvaluationFocus:  []string{"重点检查试炼节奏是否拖沓"},
			SceneAnchors:     []string{"缺角试炼令", "潮湿门槛"},
		},
		CausalSimulation: domain.ChapterCausalSimulation{
			ProjectPromise:  "修士在门规压迫下主动争取解释权",
			ChapterFunction: "用第一场试炼证明主角不会被宗门流程牵着走",
			ContextSources:  []string{"current_chapter_outline", "chapter_contract", "characters", "world_rules", "recent_summaries", "writing_engine"},
			LongformOpening: domain.LongformOpeningDesign{
				TargetReader:     "喜欢长篇升级和宗门规则博弈的读者",
				OpeningHook:      "缺角试炼令为什么仍被门槛承认",
				SerialEngine:     "宗门规则、试炼关卡和旧伤秘密逐级升级",
				ReaderRewardLoop: []string{"每关发现规则漏洞", "每弧获得新身份和新限制"},
				LongRangePromises: []domain.LongRangePromise{{
					Promise:          "师尊真实身份",
					FirstChapterSeed: "缺角试炼令",
					PayoffHorizon:    "第一卷后段",
				}},
				RevealBudget:      []string{"不解释师尊真实身份"},
				FirstChapterProof: []string{"主角能用观察而非蛮力破局"},
				RetentionRisks:    []string{"宗门规则解释过多，必须落到门槛灵纹"},
			},
			InitialState: []domain.CharacterSimulationState{{
				Character:          "林砚",
				CurrentGoal:        "通过外门试炼",
				Pressure:           "左臂旧伤和同门质疑",
				Resources:          []string{"缺角试炼令"},
				RelationshipForces: []string{"守门弟子质疑其资格"},
				Secrets:            []string{"旧伤来源不能暴露"},
				Misbeliefs:         []string{"同门以为缺角试炼令无效"},
				PrivateBoundary:    "不能暴露旧伤来源",
				ActionTendency:     "先观察规则漏洞再出手",
				LikelyAction:       "先观察规则漏洞再出手",
				StateDeltaToTrack:  []string{"旧伤暴露度", "试炼资格"},
				CompetenceStage:    "刚入门，只能识别低阶门规异常",
				SkillLimits:        []string{"不懂内门试炼暗规", "旧伤限制正面冲突"},
				PlausibleMistakes:  []string{"把名册异常误判成普通登记疏漏"},
				CorrectionTriggers: []string{"门槛灵纹反馈", "登记弟子的异常停顿"},
				KnowledgeLedger: domain.CharacterKnowledgeLedger{
					KnownFacts:         []string{"缺角试炼令仍能触发门槛灵纹"},
					UnknownFacts:       []string{"内门执事已调换分组"},
					Suspicions:         []string{"登记弟子没有说完整规则"},
					FalseBeliefs:       []string{"名册异常只是普通疏漏"},
					EvidenceSeen:       []string{"门槛灵纹响应缺角试炼令"},
					Confidence:         "低到中",
					SourceChapter:      1,
					ForbiddenKnowledge: []string{"师尊真实身份", "内门执事动机"},
				},
				DecisionFrame: domain.CharacterDecisionFrame{
					AvailableOptions:        []string{"争辩", "硬闯", "先核验门槛灵纹"},
					RejectedOptions:         []string{"争辩旧伤来源", "硬闯山门"},
					DecisionRule:            "先用可见规则反馈证明资格",
					Tradeoff:                "少解释能保密，但会延迟处理名册异常",
					CostPaid:                "承担被同门误解的压力",
					RiskAccepted:            "第二章分组可能被动",
					ExpectedGain:            "保住试炼入口",
					MinimumEvidenceRequired: "门槛灵纹承认试炼令",
				},
				RelationshipContract: []domain.CharacterRelationshipContract{{
					Counterpart:       "守门弟子",
					Trust:             "无",
					Debt:              "无",
					Leverage:          "门槛灵纹反馈",
					Promise:           "按宗门流程入试炼",
					FearSource:        "资格被取消",
					AllianceStatus:    "临时流程对手",
					BetrayalThreshold: "守门弟子篡改名册",
					HelpCondition:     "流程证据压过口头质疑",
					SourceChapter:     1,
				}},
				EmotionAppraisal: domain.CharacterEmotionAppraisal{
					TriggerEvent:         "守门弟子质疑缺角试炼令",
					GoalImpact:           "影响试炼资格",
					ThreatToValue:        "旧伤秘密和入门机会同时受压",
					VisibleExpression:    "少说话，先看门槛",
					SuppressedExpression: "不解释旧伤来源",
					CopingStrategy:       "用物证替代辩解",
					ActionPressure:       "尽快通过入口",
					RelationshipEffect:   "与守门弟子保持流程距离",
				},
				ArcAxis: domain.CharacterArcAxis{
					Want:             "通过试炼进入内门",
					Need:             "学会把秘密和规则证据分开处理",
					WoundOrGhost:     "旧伤来源不能公开",
					CoreLie:          "只要不解释就不会暴露",
					ValueAxis:        "资格/隐瞒",
					ArcStage:         "入门受压",
					PressureTest:     "被迫在围观下核验资格",
					GrowthSignal:     "先看证据再行动",
					RegressionSignal: "把异常都归为普通疏漏",
				},
			}},
			VoiceLogic: []domain.CharacterVoiceLogic{{
				Character:          "林砚",
				PersonalitySource:  "characters:沉默、观察、旧伤隐瞒",
				SpeechPrinciple:    "先看门槛证据，再用短句指出规则漏洞",
				SceneObjective:     "让守门弟子承认缺角试炼令仍有效",
				HiddenSubtext:      "隐藏旧伤来源，不暴露师尊线索",
				KnowledgeBoundary:  "只知道门槛灵纹响应缺口，不知道师尊真实身份",
				RelationshipStance: "被质疑的外门弟子对守门流程",
				DictionAndRhythm:   "短句、克制、少解释，先指物再说结论",
				ActionBeatPolicy:   "用碰令牌、停顿和不争辩承载隐瞒",
				DialogueFunctions:  []string{"推进试炼冲突", "暴露隐藏旧伤"},
				TypicalMoves:       []string{"不争辩出身", "用门槛灵纹反问流程漏洞"},
				ForbiddenMoves:     []string{"不能主动解释旧伤来源"},
				DialogueTest:       []string{"删掉说话人后，是否仍能看出林砚在隐藏旧伤"},
			}},
			ReviewRefinement: domain.ReviewRefinementLoop{
				TriggerSources:      []string{"rewrite_brief.issues"},
				FailureModes:        []string{"守门对话解释腔"},
				LocalizedTargets:    []string{"试炼门槛对话"},
				PreserveConstraints: []string{"缺角试炼令仍有效"},
				ReplanningMoves:     []string{"把解释改成令牌动作和门槛反馈"},
				AcceptanceChecks:    []string{"台词不提前解释师尊身份"},
				StopCondition:       "连续两次仍失败时改为局部 edit",
				IterationLimit:      2,
			},
			EnvironmentState: []domain.EnvironmentSignal{{
				Place:              "外门试炼门槛",
				VisibleState:       "门槛潮湿，缺角试炼令贴合灵纹",
				InformationCarried: "入口只认令牌缺口，不认口头解释",
				PressureApplied:    "迫使林砚隐藏旧伤并用观察破局",
				ExpectedChange:     "门槛从阻碍变成通过线索",
			}},
			CausalBeats: []domain.CausalSimulationBeat{{
				Cause:           "试炼令缺角却仍被宗门承认",
				CharacterChoice: "林砚不争辩，先按缺角位置找入口",
				WorldResponse:   "门槛灵纹只认令牌缺口",
				StoryResult:     "林砚获得第一关通过线索",
			}},
		},
	}); err != nil {
		t.Fatalf("SaveChapterPlan: %v", err)
	}
	if err := s.World.SaveStyleRules(domain.WritingStyleRules{
		Volume: 1,
		Arc:    1,
		Prose:  []string{"叙述保持克制"},
	}); err != nil {
		t.Fatalf("SaveStyleRules: %v", err)
	}
	if err := s.RunMeta.SetPlanningTier(domain.PlanningTierLong); err != nil {
		t.Fatalf("SetPlanningTier: %v", err)
	}

	tool := NewContextTool(s, References{
		Consistency:      "一致性检查",
		HookTechniques:   "钩子技巧",
		QualityChecklist: "质量清单",
	}, "default")
	args, err := json.Marshal(map[string]any{"chapter": 2})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(result, &payload); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	for _, key := range []string{
		"premise",
		"premise_sections",
		"premise_structure",
		"outline",
		"world_rules",
		"memory_policy",
		"planning_tier",
		"working_memory",
		"episodic_memory",
		"reference_pack",
		"current_chapter_outline",
		"recent_summaries",
		"chapter_plan",
		"chapter_contract",
		"causal_simulation",
		"previous_tail",
		"style_rules",
		"references",
	} {
		if _, ok := payload[key]; !ok {
			t.Fatalf("expected key %q in chapter context", key)
		}
	}
	working, ok := payload["working_memory"].(map[string]any)
	if !ok {
		t.Fatalf("expected working_memory object, got %#v", payload["working_memory"])
	}
	stages, ok := working["character_stage_records"].([]any)
	if !ok || len(stages) != 1 {
		t.Fatalf("expected character_stage_records in working_memory, got %#v", working["character_stage_records"])
	}
	if _, ok := working["world_foundation"].(map[string]any); !ok {
		t.Fatalf("expected world_foundation in working_memory, got %#v", working["world_foundation"])
	}
	dossiers, ok := working["character_dossiers"].([]any)
	if !ok || len(dossiers) == 0 {
		t.Fatalf("expected character_dossiers in working_memory, got %#v", working["character_dossiers"])
	}
	stage0, ok := stages[0].(map[string]any)
	if !ok || stage0["character"] != "林砚" || stage0["next_potential"] == "" {
		t.Fatalf("expected detailed character stage record, got %#v", stages[0])
	}
	future, ok := working["future_outline_window"].([]any)
	if !ok || len(future) != 4 {
		t.Fatalf("expected 4-entry future_outline_window, got %#v", working["future_outline_window"])
	}
	futureLast, ok := future[3].(map[string]any)
	if !ok || futureLast["chapter"].(float64) != 5 {
		t.Fatalf("expected future window through chapter 5, got %#v", future)
	}
	contract, ok := payload["chapter_contract"].(map[string]any)
	if !ok {
		t.Fatalf("expected chapter_contract object, got %#v", payload["chapter_contract"])
	}
	anchors, ok := contract["scene_anchors"].([]any)
	if !ok || len(anchors) != 2 || anchors[0] != "缺角试炼令" {
		t.Fatalf("expected scene anchors in chapter_contract, got %#v", contract["scene_anchors"])
	}
	causal, ok := payload["causal_simulation"].(map[string]any)
	if !ok {
		t.Fatalf("expected causal_simulation object, got %#v", payload["causal_simulation"])
	}
	if causal["project_promise"] != "修士在门规压迫下主动争取解释权" {
		t.Fatalf("unexpected causal_simulation: %#v", causal)
	}
	initial, ok := causal["initial_state"].([]any)
	if !ok || len(initial) == 0 {
		t.Fatalf("expected initial_state in causal_simulation, got %#v", causal["initial_state"])
	}
	initial0, ok := initial[0].(map[string]any)
	deltas, _ := initial0["state_delta_to_track"].([]any)
	if !ok || initial0["action_tendency"] == "" || len(deltas) == 0 {
		t.Fatalf("expected detailed initial_state in causal_simulation, got %#v", initial[0])
	}
	sources, ok := causal["context_sources"].([]any)
	if !ok || len(sources) == 0 || sources[0] != "current_chapter_outline" {
		t.Fatalf("expected context_sources in causal_simulation, got %#v", causal["context_sources"])
	}
	env, ok := causal["environment_state"].([]any)
	if !ok || len(env) == 0 {
		t.Fatalf("expected environment_state in causal_simulation, got %#v", causal["environment_state"])
	}
	voice, ok := causal["voice_logic"].([]any)
	if !ok || len(voice) == 0 {
		t.Fatalf("expected voice_logic in causal_simulation, got %#v", causal["voice_logic"])
	}
	voice0, ok := voice[0].(map[string]any)
	if !ok || voice0["scene_objective"] == "" || voice0["knowledge_boundary"] == "" {
		t.Fatalf("expected detailed voice_logic in causal_simulation, got %#v", voice[0])
	}
	refine, ok := causal["review_refinement"].(map[string]any)
	if !ok || refine["stop_condition"] == "" {
		t.Fatalf("expected review_refinement in causal_simulation, got %#v", causal["review_refinement"])
	}
	opening, ok := causal["longform_opening"].(map[string]any)
	if !ok || opening["serial_engine"] == "" {
		t.Fatalf("expected longform_opening in causal_simulation, got %#v", causal["longform_opening"])
	}
}

func TestContextToolArchitectModeIncludesPlanningAndFoundation(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Outline.SavePremise(`## 题材和基调
群像冒险，偏冷峻史诗。

## 题材定位
群像长篇冒险

## 核心冲突
众人必须在不断失控的旧秩序中寻找新秩序。

## 主角目标
抵达真相核心。

## 终局方向
揭开古老真相并重建秩序。

## 写作禁区
不靠天降设定收尾。

## 差异化卖点
群像关系推进。

## 差异化钩子
每卷都改变队伍关系结构。

## 核心兑现承诺
持续提供发现、牺牲与选择。

## 故事引擎
旅途推进、真相调查与队伍关系共同驱动。

## 关系/成长主线
队伍从互不信任走向分裂再重组。

## 升级路径
从地方事件走向世界级危机。

## 中期转向
真相并非敌人，而是秩序本身有问题。

## 终局命题
秩序应由谁定义。
`); err != nil {
		t.Fatalf("SavePremise: %v", err)
	}
	if err := s.Outline.SaveOutline([]domain.OutlineEntry{
		{Chapter: 1, Title: "起点", CoreEvent: "旅途开始"},
	}); err != nil {
		t.Fatalf("SaveOutline: %v", err)
	}
	if err := s.Characters.Save([]domain.Character{
		{Name: "沈曜", Role: "主角", Description: "流浪剑客", Arc: "寻找真相", Traits: []string{"敏锐"}},
	}); err != nil {
		t.Fatalf("SaveCharacters: %v", err)
	}
	if err := s.World.SaveWorldRules([]domain.WorldRule{
		{Category: "society", Rule: "城邦林立", Boundary: "皇权不可直辖边地"},
	}); err != nil {
		t.Fatalf("SaveWorldRules: %v", err)
	}
	if err := s.Outline.SaveLayeredOutline([]domain.VolumeOutline{
		{
			Index: 1, Title: "第一卷", Theme: "踏上旅途",
			Arcs: []domain.ArcOutline{
				{Index: 1, Title: "启程", Goal: "建立队伍", Chapters: []domain.OutlineEntry{{Chapter: 1, Title: "起点"}}},
				{Index: 2, Title: "迷雾", Goal: "逼近秘密", EstimatedChapters: 5},
			},
		},
	}); err != nil {
		t.Fatalf("SaveLayeredOutline: %v", err)
	}
	if err := s.Outline.SaveCompass(domain.StoryCompass{
		EndingDirection: "揭开古老真相",
		EstimatedScale:  "预计 3 卷",
	}); err != nil {
		t.Fatalf("SaveCompass: %v", err)
	}
	if err := s.World.SaveStyleRules(domain.WritingStyleRules{
		Volume: 1,
		Arc:    1,
		Prose:  []string{"保持冷峻节制"},
	}); err != nil {
		t.Fatalf("SaveStyleRules: %v", err)
	}
	if err := s.RunMeta.SetPlanningTier(domain.PlanningTierLong); err != nil {
		t.Fatalf("SetPlanningTier: %v", err)
	}

	tool := NewContextTool(s, References{
		OutlineTemplate:   "大纲模板",
		CharacterTemplate: "角色模板",
		LongformPlanning:  "长篇规划",
	}, "default")
	args, err := json.Marshal(map[string]any{})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(result, &payload); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	for _, key := range []string{
		"memory_policy",
		"planning_tier",
		"planning_memory",
		"foundation_memory",
		"reference_pack",
		"premise_sections",
		"premise_structure",
		"characters",
		"layered_outline",
		"skeleton_arcs",
		"compass",
		"style_rules",
		"references",
		"foundation_status",
	} {
		if _, ok := payload[key]; !ok {
			t.Fatalf("expected key %q in architect context", key)
		}
	}
}

func TestContextToolInjectsProductionPlaybookReference(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	tool := NewContextTool(s, References{
		ProductionPlaybook: "生产链路：章节任务单与质量债务",
	}, "default")
	result, err := tool.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(result, &payload); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	pack, ok := payload["reference_pack"].(map[string]any)
	if !ok {
		t.Fatalf("expected reference_pack")
	}
	refs, ok := pack["references"].(map[string]any)
	if !ok {
		t.Fatalf("expected reference_pack.references")
	}
	if got, ok := refs["production_playbook"].(string); !ok || got == "" {
		t.Fatalf("expected production_playbook reference, got %#v", refs["production_playbook"])
	}
}

func TestContextToolInjectsHumanFeelCraftReference(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	tool := NewContextTool(s, References{
		HumanFeelCraft: "人工感写法：物件回扣链与主观误判",
	}, "default")
	result, err := tool.Execute(context.Background(), json.RawMessage(`{"chapter":1}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(result, &payload); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	pack, ok := payload["reference_pack"].(map[string]any)
	if !ok {
		t.Fatalf("expected reference_pack")
	}
	refs, ok := pack["references"].(map[string]any)
	if !ok {
		t.Fatalf("expected reference_pack.references")
	}
	if got, ok := refs["human_feel_craft"].(string); !ok || got == "" {
		t.Fatalf("expected human_feel_craft reference, got %#v", refs["human_feel_craft"])
	}
}

func TestContextToolInjectsCharacterAndEmotionalCraftReferences(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	tool := NewContextTool(s, References{
		CharacterBuilding:       "人物塑造：目标、恐惧、压力反应",
		EmotionalNarrativeCraft: "情感叙事：情绪弧线、动机反应、长循环联网查资料",
	}, "default")
	result, err := tool.Execute(context.Background(), json.RawMessage(`{"chapter":1}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(result, &payload); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	pack, ok := payload["reference_pack"].(map[string]any)
	if !ok {
		t.Fatalf("expected reference_pack")
	}
	refs, ok := pack["references"].(map[string]any)
	if !ok {
		t.Fatalf("expected reference_pack.references")
	}
	if got, ok := refs["character_building"].(string); !ok || got == "" {
		t.Fatalf("expected character_building reference, got %#v", refs["character_building"])
	}
	if got, ok := refs["emotional_narrative_craft"].(string); !ok || got == "" {
		t.Fatalf("expected emotional_narrative_craft reference, got %#v", refs["emotional_narrative_craft"])
	}
}

func TestContextToolInjectsFictionParagraphingReference(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	tool := NewContextTool(s, References{
		FictionParagraphing: "小说分段：换说话人、换焦点、避免文字墙",
	}, "default")
	result, err := tool.Execute(context.Background(), json.RawMessage(`{"chapter":1}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(result, &payload); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	pack, ok := payload["reference_pack"].(map[string]any)
	if !ok {
		t.Fatalf("expected reference_pack")
	}
	refs, ok := pack["references"].(map[string]any)
	if !ok {
		t.Fatalf("expected reference_pack.references")
	}
	if got, ok := refs["fiction_paragraphing"].(string); !ok || got == "" {
		t.Fatalf("expected fiction_paragraphing reference, got %#v", refs["fiction_paragraphing"])
	}
}

func TestContextToolInjectsWritingTechniquesDigestReference(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	tool := NewContextTool(s, References{
		WritingTechniquesDigest: "refer 写作技巧：前台故事、钩子接力、场景余波、中文标点",
	}, "default")
	result, err := tool.Execute(context.Background(), json.RawMessage(`{"chapter":1}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(result, &payload); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	pack, ok := payload["reference_pack"].(map[string]any)
	if !ok {
		t.Fatalf("expected reference_pack")
	}
	refs, ok := pack["references"].(map[string]any)
	if !ok {
		t.Fatalf("expected reference_pack.references")
	}
	if got, ok := refs["writing_techniques_digest"].(string); !ok || got == "" {
		t.Fatalf("expected writing_techniques_digest reference, got %#v", refs["writing_techniques_digest"])
	}
}

func TestContextToolInjectsRAGWritingGuidelinesReference(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	tool := NewContextTool(s, References{
		RAGWritingGuidelines: "RAG 使用：先看 retrieval_trace，弱召回宁可不用",
	}, "default")
	result, err := tool.Execute(context.Background(), json.RawMessage(`{"chapter":1}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(result, &payload); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	pack, ok := payload["reference_pack"].(map[string]any)
	if !ok {
		t.Fatalf("expected reference_pack")
	}
	refs, ok := pack["references"].(map[string]any)
	if !ok {
		t.Fatalf("expected reference_pack.references")
	}
	if got, ok := refs["rag_writing_guidelines"].(string); !ok || got == "" {
		t.Fatalf("expected rag_writing_guidelines reference, got %#v", refs["rag_writing_guidelines"])
	}
}

func TestContextToolInjectsLongformAIDetectorReference(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	tool := NewContextTool(s, References{
		LongformAIDetector: "3000 字整章检测：交付看 effective_gate_percent，不看 blended 平均值",
	}, "default")
	result, err := tool.Execute(context.Background(), json.RawMessage(`{"chapter":1}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(result, &payload); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	pack, ok := payload["reference_pack"].(map[string]any)
	if !ok {
		t.Fatalf("expected reference_pack")
	}
	refs, ok := pack["references"].(map[string]any)
	if !ok {
		t.Fatalf("expected reference_pack.references")
	}
	if got, ok := refs["longform_ai_detector"].(string); !ok || got == "" {
		t.Fatalf("expected longform_ai_detector reference, got %#v", refs["longform_ai_detector"])
	}
}

func TestContextToolInjectsWebReferenceGuidelinesAndProjectBrief(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "meta"), 0o755); err != nil {
		t.Fatalf("mkdir meta: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "meta", "web_reference_brief.md"), []byte("# web brief\nretrieved_at: 2026-07-05\ntrend_language: 小区群半句热梗"), 0o644); err != nil {
		t.Fatalf("write web brief: %v", err)
	}

	tool := NewContextTool(s, References{
		WebReferenceGuidelines: "网络参考：web_reference_brief 与热梗预算",
	}, "default")
	result, err := tool.Execute(context.Background(), json.RawMessage(`{"chapter":1}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(result, &payload); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	pack, ok := payload["reference_pack"].(map[string]any)
	if !ok {
		t.Fatalf("expected reference_pack")
	}
	refs, ok := pack["references"].(map[string]any)
	if !ok {
		t.Fatalf("expected reference_pack.references")
	}
	if got, ok := refs["web_reference_guidelines"].(string); !ok || !strings.Contains(got, "热梗预算") {
		t.Fatalf("expected web_reference_guidelines reference, got %#v", refs["web_reference_guidelines"])
	}
	if got, ok := refs["web_reference_brief"].(string); !ok || !strings.Contains(got, "retrieved_at: 2026-07-05") {
		t.Fatalf("expected project web_reference_brief, got %#v", refs["web_reference_brief"])
	}
}

func TestTrimByBudgetRemovesMirroredMemoryKeys(t *testing.T) {
	result := map[string]any{
		"references": map[string]string{
			"a": strings.Repeat("x", 200),
			"b": strings.Repeat("y", 200),
		},
		"reference_pack": map[string]any{
			"references": map[string]string{
				"a": strings.Repeat("x", 200),
				"b": strings.Repeat("y", 200),
			},
			"style_rules": []string{"克制"},
		},
	}

	trimByBudget(result, 80)

	if _, ok := result["references"]; ok {
		t.Fatal("expected top-level references to be trimmed")
	}
	pack, ok := result["reference_pack"].(map[string]any)
	if !ok {
		t.Fatal("expected reference_pack to remain available")
	}
	if _, ok := pack["references"]; ok {
		t.Fatal("expected mirrored references to be trimmed from reference_pack")
	}
}

func TestTrimByBudgetPreservesCanonicalChapterTaskAndDeduplicatesPlan(t *testing.T) {
	plan := &domain.ChapterPlan{
		Chapter: 1,
		Title:   "旧计划",
		CausalSimulation: domain.ChapterCausalSimulation{
			ProjectPromise: strings.Repeat("旧推演", 200),
		},
	}
	causal := domain.ChapterCausalSimulation{ProjectPromise: strings.Repeat("当前推演", 200)}
	working := map[string]any{
		"current_chapter_outline": map[string]any{"chapter": 1, "title": "目标章"},
		"chapter_plan":            plan,
		"causal_simulation":       causal,
		"character_continuity":    strings.Repeat("历史台账", 400),
	}
	result := map[string]any{
		"working_memory":          working,
		"current_chapter_outline": working["current_chapter_outline"],
		"chapter_plan":            plan,
		"causal_simulation":       causal,
		"character_continuity":    working["character_continuity"],
	}

	trimByBudget(result, 1800)

	if _, ok := result["current_chapter_outline"]; ok {
		t.Fatal("expected top-level mirror to be removed")
	}
	if _, ok := working["current_chapter_outline"]; !ok {
		t.Fatal("expected canonical current_chapter_outline to be preserved")
	}
	trimmedPlan, ok := working["chapter_plan"].(domain.ChapterPlan)
	if !ok {
		t.Fatalf("expected cloned chapter plan, got %T", working["chapter_plan"])
	}
	if hasChapterCausalSimulation(trimmedPlan.CausalSimulation) {
		t.Fatal("expected duplicate causal_simulation inside chapter_plan to be stripped")
	}
	if _, ok := working["causal_simulation"]; !ok {
		t.Fatal("expected canonical causal_simulation to be preserved")
	}
}

func TestContextToolPartialPlanHidesStaleFormalPlan(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := st.Outline.SaveOutline([]domain.OutlineEntry{{Chapter: 1, Title: "新标题", CoreEvent: "新事件"}}); err != nil {
		t.Fatalf("SaveOutline: %v", err)
	}
	if err := st.Progress.Init("test", 2); err != nil {
		t.Fatalf("InitProgress: %v", err)
	}
	if err := st.Drafts.SaveChapterPlan(domain.ChapterPlan{
		Chapter: 1,
		Title:   "旧标题",
		Goal:    "旧目标",
		CausalSimulation: domain.ChapterCausalSimulation{
			ProjectPromise: "旧推演",
		},
	}); err != nil {
		t.Fatalf("SaveChapterPlan: %v", err)
	}
	if err := st.Drafts.SaveChapterPlanPartial(1, map[string]any{
		"structure": map[string]any{"chapter": 1, "title": "新标题"},
		"causal_simulation": map[string]any{
			"project_promise":   "新推演",
			"review_refinement": map[string]any{"stop_condition": "通过"},
		},
	}); err != nil {
		t.Fatalf("SaveChapterPlanPartial: %v", err)
	}

	raw, err := NewContextTool(st, References{}, "default").Execute(context.Background(), json.RawMessage(`{"chapter":1}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	working, ok := payload["working_memory"].(map[string]any)
	if !ok {
		t.Fatal("expected working_memory")
	}
	if _, ok := working["chapter_plan"]; ok {
		t.Fatal("stale formal plan must be hidden while a partial plan exists")
	}
	stage, ok := working["chapter_plan_stage"].(map[string]any)
	if !ok || stage["status"] != "partial" {
		t.Fatalf("expected partial plan stage, got %#v", working["chapter_plan_stage"])
	}
	fields, ok := stage["causal_fields_present"].([]any)
	if !ok || len(fields) != 2 {
		t.Fatalf("expected staged causal fields, got %#v", stage["causal_fields_present"])
	}
}

func TestContextToolHidesStaleRewritePlanAndDraft(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := st.Outline.SaveOutline([]domain.OutlineEntry{{Chapter: 1, Title: "目标章", CoreEvent: "当前事件", Hook: "当前钩子"}}); err != nil {
		t.Fatalf("SaveOutline: %v", err)
	}
	if err := st.Progress.Save(&domain.Progress{
		NovelName:         "test",
		TotalChapters:     2,
		CompletedChapters: []int{1},
		PendingRewrites:   []int{1},
	}); err != nil {
		t.Fatalf("Save progress: %v", err)
	}
	if err := st.Drafts.SaveChapterPlan(domain.ChapterPlan{Chapter: 1, Title: "旧计划", Goal: "旧目标"}); err != nil {
		t.Fatalf("SaveChapterPlan: %v", err)
	}
	if err := st.Drafts.SaveDraft(1, "# 第1章 旧草稿\n\n旧内容"); err != nil {
		t.Fatalf("SaveDraft: %v", err)
	}
	if err := st.Drafts.SaveFinalChapter(1, "# 第1章 旧终稿\n\n待返工内容"); err != nil {
		t.Fatalf("SaveFinalChapter: %v", err)
	}
	oldTime := time.Now().Add(-time.Hour)
	newTime := time.Now()
	for _, rel := range []string{"drafts/01.plan.json", "drafts/01.draft.md"} {
		if err := os.Chtimes(filepath.Join(dir, rel), oldTime, oldTime); err != nil {
			t.Fatalf("Chtimes %s: %v", rel, err)
		}
	}
	if err := os.Chtimes(filepath.Join(dir, "chapters/01.md"), newTime, newTime); err != nil {
		t.Fatalf("Chtimes final: %v", err)
	}

	raw, err := NewContextTool(st, References{}, "default").Execute(context.Background(), json.RawMessage(`{"chapter":1}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	working := payload["working_memory"].(map[string]any)
	if _, ok := working["chapter_plan"]; ok {
		t.Fatal("stale formal plan must not be injected into rewrite planning")
	}
	if _, ok := working["chapter_draft"]; ok {
		t.Fatal("stale draft must not be advertised as the current rewrite draft")
	}
	planStage := working["chapter_plan_stage"].(map[string]any)
	if planStage["status"] != "stale_for_rewrite" {
		t.Fatalf("unexpected plan stage: %#v", planStage)
	}
	draftStage := working["chapter_draft_stage"].(map[string]any)
	if draftStage["status"] != "stale_for_rewrite" {
		t.Fatalf("unexpected draft stage: %#v", draftStage)
	}
}

func TestContextToolSelectedMemoryRecallsStoryThreadsAndReviewLessons(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Outline.SaveOutline([]domain.OutlineEntry{
		{Chapter: 1, Title: "邀约", CoreEvent: "长老暗中给出内门试炼邀请", Scenes: []string{"密谈", "留下试炼令"}},
		{Chapter: 2, Title: "试炼前夜", CoreEvent: "林砚准备回应内门试炼邀请", Hook: "谁在背后推动这场试炼", Scenes: []string{"整理线索", "决定赴约"}},
	}); err != nil {
		t.Fatalf("SaveOutline: %v", err)
	}
	if err := s.Progress.Init("test", 8); err != nil {
		t.Fatalf("InitProgress: %v", err)
	}
	if err := s.World.SaveForeshadowLedger([]domain.ForeshadowEntry{
		{ID: "trial_invite", Description: "内门试炼邀请的真实目的", PlantedAt: 1, Status: "planted"},
		{ID: "trial_mastermind", Description: "谁在背后推动这场试炼", PlantedAt: 1, Status: "planted"},
		{ID: "trial_rules", Description: "试炼规则碑文残卷", PlantedAt: 1, Status: "planted"},
		{ID: "outer_disciple", Description: "外门弟子的旧债纠纷", PlantedAt: 1, Status: "planted"},
		{ID: "elder_token", Description: "长老手中令牌的来历", PlantedAt: 1, Status: "planted"},
		{ID: "hidden_gate", Description: "山门背后的隐藏通道", PlantedAt: 1, Status: "planted"},
		{ID: "trial_bet", Description: "试炼盘口的幕后操盘人", PlantedAt: 1, Status: "planted"},
	}); err != nil {
		t.Fatalf("SaveForeshadowLedger: %v", err)
	}
	if err := s.Drafts.SaveChapterPlan(domain.ChapterPlan{
		Chapter: 2,
		Title:   "试炼前夜",
		Goal:    "决定是否回应邀请",
		Contract: domain.ChapterContract{
			PayoffPoints: []string{"回应内门试炼邀请"},
			HookGoal:     "抛出谁在背后推动试炼",
		},
	}); err != nil {
		t.Fatalf("SaveChapterPlan: %v", err)
	}
	if err := s.World.SaveReview(domain.ReviewEntry{
		Chapter:        1,
		Scope:          "chapter",
		Verdict:        "polish",
		Summary:        "主线启动完成，但伏笔不够明确。",
		ContractStatus: "partial",
		ContractMisses: []string{"未明确埋下内门试炼邀请"},
		Issues: []domain.ConsistencyIssue{
			{Type: "hook", Severity: "warning", Description: "章末钩子不够具体"},
		},
	}); err != nil {
		t.Fatalf("SaveReview: %v", err)
	}

	tool := NewContextTool(s, References{}, "default")
	args, err := json.Marshal(map[string]any{"chapter": 2})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var payload struct {
		Selected struct {
			StoryThreads  []domain.RecallItem `json:"story_threads"`
			ReviewLessons []domain.RecallItem `json:"review_lessons"`
		} `json:"selected_memory"`
		Summary string `json:"_loading_summary"`
	}
	if err := json.Unmarshal(result, &payload); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(payload.Selected.StoryThreads) == 0 {
		t.Fatal("expected story thread recall items")
	}
	if len(payload.Selected.ReviewLessons) == 0 {
		t.Fatal("expected review lesson recall items")
	}
	if !containsRecallSummary(payload.Selected.StoryThreads, "内门试炼邀请") {
		t.Fatalf("expected story thread recall to mention invite, got %+v", payload.Selected.StoryThreads)
	}
	if !containsRecallSummary(payload.Selected.StoryThreads, "推动这场试炼") {
		t.Fatalf("expected story thread recall to mention trial mastermind, got %+v", payload.Selected.StoryThreads)
	}
	if containsRecallSummary(payload.Selected.StoryThreads, "试炼规则碑文残卷") {
		t.Fatalf("expected weak-overlap foreshadow to stay out, got %+v", payload.Selected.StoryThreads)
	}
	if containsRecallSummary(payload.Selected.StoryThreads, "建议回看第") {
		t.Fatalf("expected related_chapters not to be duplicated into story_threads, got %+v", payload.Selected.StoryThreads)
	}
	if !containsRecallSummary(payload.Selected.ReviewLessons, "contract 漏项") {
		t.Fatalf("expected review lesson recall to mention contract miss, got %+v", payload.Selected.ReviewLessons)
	}
	if !strings.Contains(payload.Summary, "线索召回:") || !strings.Contains(payload.Summary, "评审召回:") {
		t.Fatalf("expected loading summary to report selected memory, got %q", payload.Summary)
	}
}

func TestContextToolRAGRecallUsesProjectChunksAndIgnoresReferenceLibraries(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Outline.SaveOutline([]domain.OutlineEntry{
		{Chapter: 1, Title: "夜租商铺", CoreEvent: "林砚用第一份租约打开资产链", Scenes: []string{"欠费单", "试营业"}},
	}); err != nil {
		t.Fatalf("SaveOutline: %v", err)
	}
	if err := s.Progress.Init("test", 8); err != nil {
		t.Fatalf("InitProgress: %v", err)
	}
	if err := s.RAG.SaveIndexState(domain.RAGIndexState{
		Config: domain.RAGIndexConfig{EmbeddingConcurrency: 2, QdrantWriteConcurrency: 2},
		Chunks: []domain.RAGChunk{
			{
				ID:         "chunk:night-rent-craft",
				SourcePath: "meta/writing_assets.md",
				SourceKind: "note",
				Facet:      "craft",
				Context:    "诡异末日神豪 | 夜租商铺 | 资产升级链",
				Summary:    "租约、账单和可见代价要连续推进资产升级。",
				Text:       "用低成本物件承载规则，把收益和风险一起落进场景。",
			},
			{
				ID:         "chunk:unrelated",
				SourcePath: "meta/world_rules.md",
				SourceKind: "note",
				Facet:      "craft",
				Context:    "宗门试炼 | 丹药晋升",
				Summary:    "试炼前先写丹药筹备。",
				Text:       "长老、宗门和擂台构成升级压力。",
			},
			{
				ID:         "chunk:forbidden-reference",
				SourcePath: "拆文库/末日-男频小说/写作要素拆解.md",
				SourceKind: "deconstruction",
				Facet:      "craft",
				Context:    "诡异末日神豪 | 夜租商铺 | 资产升级链",
				Summary:    "这条即使命中也不得召回。",
				Text:       "夜租商铺、租约和账单都匹配本章。",
			},
		},
	}); err != nil {
		t.Fatalf("SaveIndexState: %v", err)
	}

	tool := NewContextTool(s, References{}, "default")
	result, err := tool.Execute(context.Background(), json.RawMessage(`{"chapter":1}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var payload struct {
		Selected struct {
			RAG []domain.RecallItem `json:"rag_recall"`
		} `json:"selected_memory"`
		ReferencePack struct {
			Trace domain.RetrievalTrace `json:"retrieval_trace"`
		} `json:"reference_pack"`
	}
	if err := json.Unmarshal(result, &payload); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(payload.Selected.RAG) == 0 {
		t.Fatal("expected contextual RAG recall")
	}
	if payload.Selected.RAG[0].Key != "chunk:night-rent-craft" {
		t.Fatalf("expected night-rent chunk first, got %+v", payload.Selected.RAG)
	}
	for _, item := range payload.Selected.RAG {
		if item.Key == "chunk:forbidden-reference" {
			t.Fatalf("forbidden reference chunk should not be recalled: %+v", payload.Selected.RAG)
		}
	}
	if payload.ReferencePack.Trace.Strategy != "local_bm25_keyword_hybrid_v1" {
		t.Fatalf("expected retrieval strategy trace, got %+v", payload.ReferencePack.Trace)
	}
	if len(payload.ReferencePack.Trace.QueryTerms) == 0 {
		t.Fatalf("expected query terms in trace, got %+v", payload.ReferencePack.Trace)
	}
	if len(payload.ReferencePack.Trace.Matches) == 0 || payload.ReferencePack.Trace.Matches[0].Context == "" {
		t.Fatalf("expected match context in trace, got %+v", payload.ReferencePack.Trace.Matches)
	}
}

type contextTestEmbedder struct{}

func (contextTestEmbedder) Embed(_ context.Context, _ string) ([]float32, error) {
	return []float32{1, 0}, nil
}

type contextCountingEmbedder struct{ calls *int }

func (e contextCountingEmbedder) Embed(_ context.Context, _ string) ([]float32, error) {
	*e.calls++
	return []float32{1, 0}, nil
}

func TestContextToolCachesIdenticalRAGRecallAndTrace(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	if err := s.Outline.SaveOutline([]domain.OutlineEntry{{Chapter: 1, Title: "返乡开店", CoreEvent: "青山县门店装修"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Progress.Init("test", 8); err != nil {
		t.Fatal(err)
	}
	chunk := rag.NormalizeChunk(domain.RAGChunk{
		SourcePath: "meta/project_progress.md", SourceKind: "ledger", Facet: "progress",
		Summary: "青山县门店装修进入收尾。", Text: "返乡开店，招牌和货架同步推进。",
	})
	if err := s.RAG.SaveIndexState(domain.RAGIndexState{Chunks: []domain.RAGChunk{chunk}, UpdatedAt: "v1"}); err != nil {
		t.Fatal(err)
	}
	calls := 0
	tool := NewContextTool(s, References{}, "default").WithRAGEmbedder(contextCountingEmbedder{calls: &calls})
	for range 2 {
		if _, err := tool.Execute(context.Background(), json.RawMessage(`{"chapter":1,"profile":"planning"}`)); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 1 {
		t.Fatalf("identical recall should embed once, got %d", calls)
	}
	traceBody, err := os.ReadFile(filepath.Join(dir, "meta", "rag", "retrieval_trace.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if lines := strings.Count(strings.TrimSpace(string(traceBody)), "\n") + 1; lines != 1 {
		t.Fatalf("cache hit must not append a duplicate trace, got %d lines", lines)
	}
}

type contextTestSearcher struct {
	hits []rag.VectorSearchHit
}

func (s contextTestSearcher) Search(_ context.Context, _ []float32, _ int) ([]rag.VectorSearchHit, error) {
	return s.hits, nil
}

func TestContextToolRAGRecallPrefersQdrantVectorHits(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Outline.SaveOutline([]domain.OutlineEntry{
		{Chapter: 1, Title: "夜租商铺", CoreEvent: "林砚检查第一份租约", Scenes: []string{"账单", "试营业"}},
	}); err != nil {
		t.Fatalf("SaveOutline: %v", err)
	}
	if err := s.Progress.Init("test", 8); err != nil {
		t.Fatalf("InitProgress: %v", err)
	}
	if err := s.RAG.SaveIndexState(domain.RAGIndexState{
		Config: domain.RAGIndexConfig{Collection: "local_keyword"},
		Chunks: []domain.RAGChunk{{
			ID:         "chunk:local-exact-night-rent",
			SourcePath: "meta/local-index.md",
			SourceKind: "note",
			Facet:      "plot",
			Context:    "夜租商铺 | 本地文件",
			Summary:    "本地 index_state 里也有夜租商铺关键词。",
			Text:       "夜租商铺、账单、试营业、第一份租约全部精确命中，但有 Qdrant 时不能混入本地文件召回。",
		}},
	}); err != nil {
		t.Fatalf("SaveIndexState: %v", err)
	}
	qdrantChunk := rag.NormalizeChunk(domain.RAGChunk{
		ID:         "chunk:qdrant-night-rent",
		SourcePath: "meta/rag/qdrant.md",
		SourceKind: "note",
		Facet:      "plot",
		Context:    "夜租商铺 | 资产链",
		Summary:    "租约、账单、资产收益要连续推进。",
		Text:       "语义召回命中夜租商铺的资产链。",
	})
	tool := NewContextTool(s, References{}, "default").
		WithRAGEmbedder(contextTestEmbedder{}).
		WithRAGVectorSearcher(contextTestSearcher{hits: []rag.VectorSearchHit{{
			Point: domain.RAGVectorPoint{ID: qdrantChunk.ID, Chunk: qdrantChunk},
			Score: 0.92,
		}}})
	result, err := tool.Execute(context.Background(), json.RawMessage(`{"chapter":1}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var payload struct {
		Selected struct {
			RAG []domain.RecallItem `json:"rag_recall"`
		} `json:"selected_memory"`
		ReferencePack struct {
			Trace domain.RetrievalTrace `json:"retrieval_trace"`
		} `json:"reference_pack"`
	}
	if err := json.Unmarshal(result, &payload); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(payload.Selected.RAG) == 0 || payload.Selected.RAG[0].Key != "chunk:qdrant-night-rent" {
		t.Fatalf("expected qdrant hit first, got %+v", payload.Selected.RAG)
	}
	// BM25 混合通道允许本地词法命中加入召回，但语义命中必须保持第一位。
	for _, item := range payload.Selected.RAG[1:] {
		if item.Key == payload.Selected.RAG[0].Key {
			t.Fatalf("duplicate hit in hybrid recall: %+v", payload.Selected.RAG)
		}
	}
	if payload.ReferencePack.Trace.Strategy != "qdrant_bm25_hybrid_v2" {
		t.Fatalf("expected qdrant hybrid strategy, got %+v", payload.ReferencePack.Trace)
	}
}

type contextEOFEmbedder struct{}

func (contextEOFEmbedder) Embed(context.Context, string) ([]float32, error) {
	return nil, io.EOF
}

type contextEOFSearcher struct{}

func (contextEOFSearcher) Search(context.Context, []float32, int) ([]rag.VectorSearchHit, error) {
	return nil, io.ErrUnexpectedEOF
}

func TestContextToolRAGEmbeddingEOFFallsBackToBM25(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Outline.SaveOutline([]domain.OutlineEntry{{Chapter: 1, Title: "返乡开店", CoreEvent: "男主在青山县盘下第一间门店"}}); err != nil {
		t.Fatalf("SaveOutline: %v", err)
	}
	if err := s.Progress.Init("test", 8); err != nil {
		t.Fatalf("InitProgress: %v", err)
	}
	chunk := rag.NormalizeChunk(domain.RAGChunk{
		SourcePath: "outline.md", SourceKind: "planning", Facet: "plot",
		Summary: "返乡后盘下青山县第一间门店。", Text: "开店、装修和第一笔真实营业收入。",
	})
	if err := s.RAG.SaveIndexState(domain.RAGIndexState{Chunks: []domain.RAGChunk{chunk}, UpdatedAt: "v1"}); err != nil {
		t.Fatalf("SaveIndexState: %v", err)
	}
	tool := NewContextTool(s, References{}, "default").WithRAGEmbedder(contextEOFEmbedder{})
	result, err := tool.Execute(context.Background(), json.RawMessage(`{"chapter":1}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var payload struct {
		Selected struct {
			RAG []domain.RecallItem `json:"rag_recall"`
		} `json:"selected_memory"`
		ReferencePack struct {
			Trace domain.RetrievalTrace `json:"retrieval_trace"`
		} `json:"reference_pack"`
	}
	if err := json.Unmarshal(result, &payload); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(payload.Selected.RAG) == 0 || payload.Selected.RAG[0].Key != chunk.ID {
		t.Fatalf("BM25 fallback did not return project chunk: %+v", payload.Selected.RAG)
	}
	if payload.ReferencePack.Trace.Strategy != "embedding_error_bm25_fallback_v2" {
		t.Fatalf("unexpected fallback trace: %+v", payload.ReferencePack.Trace)
	}
}

func TestContextToolRAGQdrantEOFFallsBackToLocalVector(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Outline.SaveOutline([]domain.OutlineEntry{{Chapter: 1, Title: "返乡开店", CoreEvent: "男主在青山县装修门店"}}); err != nil {
		t.Fatalf("SaveOutline: %v", err)
	}
	if err := s.Progress.Init("test", 8); err != nil {
		t.Fatalf("InitProgress: %v", err)
	}
	chunk := rag.NormalizeChunk(domain.RAGChunk{
		SourcePath: "meta/project_progress.md", SourceKind: "ledger", Facet: "progress",
		Summary: "青山县门店装修进入收尾。", Text: "招牌、货架和试营业同步推进。",
	})
	if err := s.RAG.SaveIndexState(domain.RAGIndexState{Chunks: []domain.RAGChunk{chunk}, UpdatedAt: "v1"}); err != nil {
		t.Fatalf("SaveIndexState: %v", err)
	}
	if err := s.RAG.SaveVectorStore(domain.RAGVectorStore{
		Config: domain.RAGIndexConfig{VectorDimension: 2},
		Points: []domain.RAGVectorPoint{{ID: chunk.ID, Hash: chunk.Hash, Vector: []float32{1, 0}, Chunk: chunk}},
	}); err != nil {
		t.Fatalf("SaveVectorStore: %v", err)
	}
	tool := NewContextTool(s, References{}, "default").
		WithRAGEmbedder(contextTestEmbedder{}).
		WithRAGVectorSearcher(contextEOFSearcher{})
	result, err := tool.Execute(context.Background(), json.RawMessage(`{"chapter":1}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var payload struct {
		Selected struct {
			RAG []domain.RecallItem `json:"rag_recall"`
		} `json:"selected_memory"`
		ReferencePack struct {
			Trace domain.RetrievalTrace `json:"retrieval_trace"`
		} `json:"reference_pack"`
	}
	if err := json.Unmarshal(result, &payload); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(payload.Selected.RAG) == 0 || payload.Selected.RAG[0].Key != chunk.ID {
		t.Fatalf("local vector fallback did not return project chunk: %+v", payload.Selected.RAG)
	}
	if payload.ReferencePack.Trace.Strategy != "qdrant_error_local_vector_bm25_fallback_v2" {
		t.Fatalf("unexpected fallback trace: %+v", payload.ReferencePack.Trace)
	}
}

// 久挂未回收的伏笔即使与当前章关键词无关，也应被账龄回填进 story_threads——
// 这正是相关性召回的盲区（独自悬挂太久、却没在本章撞上关键词的那根线）。
// 近期埋下的伏笔（账龄 < 阈值）不应被误标为"未回收"。
func TestContextToolSelectedMemorySurfacesAgingForeshadow(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	// 当前章主题与所有伏笔都不沾边，确保相关性召回为空，只剩账龄回填生效。
	if err := s.Outline.SaveOutline([]domain.OutlineEntry{
		{Chapter: 50, Title: "瘟疫", CoreEvent: "林砚在城南医馆救治瘟疫病患", Scenes: []string{"熬药", "封锁街巷"}},
	}); err != nil {
		t.Fatalf("SaveOutline: %v", err)
	}
	if err := s.Progress.Init("test", 60); err != nil {
		t.Fatalf("InitProgress: %v", err)
	}
	// 6 条满足召回阈值；前两条账龄 ≥30（久挂），后四条账龄 <30（近期）。
	if err := s.World.SaveForeshadowLedger([]domain.ForeshadowEntry{
		{ID: "ancient_seal", Description: "上古封印的裂隙", PlantedAt: 3, Status: "planted"},
		{ID: "lost_bloodline", Description: "主角失落的血脉来历", PlantedAt: 5, Status: "advanced"},
		{ID: "market_feud", Description: "昨夜集市的口角", PlantedAt: 47, Status: "planted"},
		{ID: "rumor_a", Description: "近日传闻甲", PlantedAt: 48, Status: "planted"},
		{ID: "rumor_b", Description: "近日传闻乙", PlantedAt: 48, Status: "planted"},
		{ID: "rumor_c", Description: "近日传闻丙", PlantedAt: 49, Status: "planted"},
	}); err != nil {
		t.Fatalf("SaveForeshadowLedger: %v", err)
	}

	tool := NewContextTool(s, References{}, "default")
	args, err := json.Marshal(map[string]any{"chapter": 50})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var payload struct {
		Selected struct {
			StoryThreads []domain.RecallItem `json:"story_threads"`
		} `json:"selected_memory"`
	}
	if err := json.Unmarshal(result, &payload); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	// 两条久挂伏笔应被回填，且带"未回收"账龄标注。
	if !containsRecallSummary(payload.Selected.StoryThreads, "上古封印的裂隙") {
		t.Fatalf("expected aging foreshadow to surface despite no relevance, got %+v", payload.Selected.StoryThreads)
	}
	if !containsRecallSummary(payload.Selected.StoryThreads, "失落的血脉") {
		t.Fatalf("expected second aging foreshadow to surface, got %+v", payload.Selected.StoryThreads)
	}
	if !containsRecallSummary(payload.Selected.StoryThreads, "未回收") {
		t.Fatalf("expected aging item to carry overdue annotation, got %+v", payload.Selected.StoryThreads)
	}
	// 近期伏笔（账龄 <30 且不相关）不应被回填。
	if containsRecallSummary(payload.Selected.StoryThreads, "昨夜集市的口角") {
		t.Fatalf("recent foreshadow must not be labeled overdue, got %+v", payload.Selected.StoryThreads)
	}
}

func TestContextToolSelectedMemoryIncludesGlobalReviewLessons(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Outline.SaveOutline([]domain.OutlineEntry{
		{Chapter: 1, Title: "开端", CoreEvent: "故事开始"},
		{Chapter: 2, Title: "推进", CoreEvent: "主线继续推进"},
	}); err != nil {
		t.Fatalf("SaveOutline: %v", err)
	}
	if err := s.Progress.Init("test", 6); err != nil {
		t.Fatalf("InitProgress: %v", err)
	}
	if err := s.World.SaveReview(domain.ReviewEntry{
		Chapter: 1,
		Scope:   "global",
		Verdict: "polish",
		Summary: "全局推进合格，但角色目标表达还不够稳定。",
		Issues: []domain.ConsistencyIssue{
			{Type: "character", Severity: "warning", Description: "主角目标表达不够稳定"},
		},
	}); err != nil {
		t.Fatalf("SaveReview(global): %v", err)
	}

	tool := NewContextTool(s, References{}, "default")
	args, err := json.Marshal(map[string]any{"chapter": 2})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var payload struct {
		Selected struct {
			ReviewLessons []domain.RecallItem `json:"review_lessons"`
		} `json:"selected_memory"`
	}
	if err := json.Unmarshal(result, &payload); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !containsRecallSummary(payload.Selected.ReviewLessons, "主角目标表达不够稳定") {
		t.Fatalf("expected global review lesson to be recalled, got %+v", payload.Selected.ReviewLessons)
	}
}

func TestContextToolKeepsFullForeshadowWhenRecallNotTriggered(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Outline.SaveOutline([]domain.OutlineEntry{
		{Chapter: 1, Title: "起势", CoreEvent: "故事起势"},
		{Chapter: 2, Title: "推进", CoreEvent: "继续推进"},
	}); err != nil {
		t.Fatalf("SaveOutline: %v", err)
	}
	if err := s.Progress.Init("test", 4); err != nil {
		t.Fatalf("InitProgress: %v", err)
	}
	if err := s.World.SaveForeshadowLedger([]domain.ForeshadowEntry{
		{ID: "small_1", Description: "第一条小伏笔", PlantedAt: 1, Status: "planted"},
		{ID: "small_2", Description: "第二条小伏笔", PlantedAt: 1, Status: "planted"},
	}); err != nil {
		t.Fatalf("SaveForeshadowLedger: %v", err)
	}

	tool := NewContextTool(s, References{}, "default")
	args, err := json.Marshal(map[string]any{"chapter": 2})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(result, &payload); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if _, ok := payload["foreshadow_ledger"]; !ok {
		t.Fatal("expected full foreshadow ledger to remain when selected recall is not triggered")
	}
	if _, ok := payload["selected_memory"]; ok {
		t.Fatalf("expected no selected_memory for small foreshadow sets, got %+v", payload["selected_memory"])
	}
}

func TestContextToolFallsBackToFullForeshadowWhenSelectionIsTooSparse(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Outline.SaveOutline([]domain.OutlineEntry{
		{Chapter: 1, Title: "邀约", CoreEvent: "长老暗中给出内门试炼邀请"},
		{Chapter: 2, Title: "试炼前夜", CoreEvent: "林砚准备回应内门试炼邀请", Scenes: []string{"整理线索", "决定赴约"}},
	}); err != nil {
		t.Fatalf("SaveOutline: %v", err)
	}
	if err := s.Progress.Init("test", 8); err != nil {
		t.Fatalf("InitProgress: %v", err)
	}
	if err := s.World.SaveForeshadowLedger([]domain.ForeshadowEntry{
		{ID: "trial_invite", Description: "内门试炼邀请的真实目的", PlantedAt: 1, Status: "planted"},
		{ID: "trial_rules", Description: "试炼规则碑文残卷", PlantedAt: 1, Status: "planted"},
		{ID: "outer_disciple", Description: "外门弟子的旧债纠纷", PlantedAt: 1, Status: "planted"},
		{ID: "elder_token", Description: "长老手中令牌的来历", PlantedAt: 1, Status: "planted"},
		{ID: "hidden_gate", Description: "山门背后的隐藏通道", PlantedAt: 1, Status: "planted"},
		{ID: "trial_bet", Description: "试炼盘口的幕后操盘人", PlantedAt: 1, Status: "planted"},
	}); err != nil {
		t.Fatalf("SaveForeshadowLedger: %v", err)
	}

	tool := NewContextTool(s, References{}, "default")
	args, err := json.Marshal(map[string]any{"chapter": 2})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(result, &payload); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if _, ok := payload["foreshadow_ledger"]; !ok {
		t.Fatal("expected full foreshadow ledger when selection is too sparse")
	}
	if selected, ok := payload["selected_memory"].(map[string]any); ok {
		if _, exists := selected["story_threads"]; exists {
			t.Fatalf("expected sparse story_threads to fall back to full ledger, got %+v", selected["story_threads"])
		}
	}
}

func containsRecallSummary(items []domain.RecallItem, want string) bool {
	for _, item := range items {
		if strings.Contains(item.Summary, want) {
			return true
		}
	}
	return false
}

func TestContextToolInjectsRewriteBriefForPendingRewriteChapter(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init("test", 3); err != nil {
		t.Fatalf("InitProgress: %v", err)
	}
	if err := s.Progress.MarkChapterComplete(2, 3000, "", ""); err != nil {
		t.Fatalf("MarkChapterComplete: %v", err)
	}
	if err := s.Progress.SetPendingRewrites([]int{2}, "节奏拖沓，需要压缩前半段"); err != nil {
		t.Fatalf("SetPendingRewrites: %v", err)
	}
	if err := s.World.SaveReview(domain.ReviewEntry{
		Chapter: 2,
		Scope:   "chapter",
		Verdict: "rewrite",
		Summary: "前半段铺垫过长，冲突迟迟不出现。",
		Issues: []domain.ConsistencyIssue{
			{Type: "pacing", Severity: "error", Description: "前 2000 字无推进"},
		},
		ContractMisses: []string{"未兑现试炼开场"},
	}); err != nil {
		t.Fatalf("SaveReview: %v", err)
	}

	tool := NewContextTool(s, References{}, "default")
	args, err := json.Marshal(map[string]any{"chapter": 2})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(result, &payload); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	brief, ok := payload["rewrite_brief"].(map[string]any)
	if !ok {
		t.Fatalf("expected rewrite_brief in chapter context, got %T", payload["rewrite_brief"])
	}
	if got := brief["reason"]; got != "节奏拖沓，需要压缩前半段" {
		t.Fatalf("expected rewrite reason, got %v", got)
	}
	if got, _ := brief["review_summary"].(string); !strings.Contains(got, "铺垫过长") {
		t.Fatalf("expected review summary from chapter review, got %v", brief["review_summary"])
	}
	if issues, _ := brief["issues"].([]any); len(issues) == 0 {
		t.Fatalf("expected review issues in rewrite_brief, got %v", brief["issues"])
	}
	if misses, _ := brief["contract_misses"].([]any); len(misses) == 0 {
		t.Fatalf("expected contract misses in rewrite_brief, got %v", brief["contract_misses"])
	}
}

func TestContextToolInjectsMechanicalGateBriefForPendingRewrite(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init("test", 3); err != nil {
		t.Fatalf("InitProgress: %v", err)
	}
	if err := s.Progress.MarkChapterComplete(2, 3000, "", ""); err != nil {
		t.Fatalf("MarkChapterComplete: %v", err)
	}
	if err := s.Progress.SetPendingRewrites([]int{2}, "机械审核未通过：aigc_ratio=codex-local-aigc-v3"); err != nil {
		t.Fatalf("SetPendingRewrites: %v", err)
	}
	report := aigc.Report{
		Engine:             "codex-local-aigc-v3",
		AIGCPercent:        72.5,
		AIRatioPercent:     72.5,
		BlendedAIGCPercent: 18.2,
		SegmentRiskFloor:   72.5,
		RiskLabel:          "high",
		Confidence:         "high",
		LatestDetectorProxy: aigc.DetectorProxy{
			CompositePercent: 68.4,
			Components: map[string]aigc.Dimension{
				"semantic_smoothing": {
					Name:  "semantic_smoothing",
					Score: 82,
					Signals: []aigc.Signal{
						{Name: "summary_without_scene", Score: 70, Evidence: "抽象判断偏多，动作和物件不足"},
					},
				},
			},
		},
		Dimensions: map[string]aigc.Dimension{
			"structure_fingerprint": {
				Name:  "structure_fingerprint",
				Score: 88,
				Signals: []aigc.Signal{
					{Name: "summary_density", Score: 62, Evidence: "解释归纳标记偏高"},
				},
			},
		},
	}
	violations := []rules.Violation{
		{Rule: "aigc_ratio", Target: "codex-local-aigc-v3", Limit: "5%", Actual: 72.5, Severity: rules.SeverityError},
	}
	if err := NewCommitChapterTool(s).saveAIGCReviewFiles(2, "第二章测试正文", report, violations); err != nil {
		t.Fatalf("saveAIGCReviewFiles: %v", err)
	}

	tool := NewContextTool(s, References{}, "default")
	args, err := json.Marshal(map[string]any{"chapter": 2})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(result, &payload); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	brief, ok := payload["rewrite_brief"].(map[string]any)
	if !ok {
		t.Fatalf("expected rewrite_brief in chapter context, got %T", payload["rewrite_brief"])
	}
	gate, ok := brief["mechanical_gate"].(map[string]any)
	if !ok {
		t.Fatalf("expected mechanical_gate in rewrite_brief, got %+v", brief)
	}
	if got := gate["json_path"]; got != "reviews/02_ai_gate.json" {
		t.Fatalf("expected mechanical gate json path, got %v", got)
	}
	if got := gate["ai_ratio_percent"]; got != 72.5 {
		t.Fatalf("expected ai_ratio_percent 72.5, got %v", got)
	}
	if dims, _ := gate["high_risk_dimensions"].([]any); len(dims) == 0 {
		t.Fatalf("expected high_risk_dimensions, got %+v", gate)
	}
	if violations, _ := gate["rule_violations"].([]any); len(violations) != 1 {
		t.Fatalf("expected compact rule violations, got %+v", gate["rule_violations"])
	}
	if focus, _ := gate["rewrite_focus"].([]any); len(focus) == 0 {
		t.Fatalf("expected rewrite_focus guidance, got %+v", gate)
	}
}

func TestContextToolOmitsRewriteBriefForNormalChapter(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init("test", 3); err != nil {
		t.Fatalf("InitProgress: %v", err)
	}

	tool := NewContextTool(s, References{}, "default")
	args, err := json.Marshal(map[string]any{"chapter": 2})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(result, &payload); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if _, ok := payload["rewrite_brief"]; ok {
		t.Fatal("expected no rewrite_brief for chapter outside PendingRewrites")
	}
}

func TestContextToolDoesNotInjectUserDirectives(t *testing.T) {
	// save_directive 已移除：novel_context 不再注入 working_memory.user_directives，
	// 长期写作要求统一走 user_rules。锁死这条，防止回归。
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init("test", 3); err != nil {
		t.Fatalf("InitProgress: %v", err)
	}

	tool := NewContextTool(s, References{}, "default")
	for name, chapter := range map[string]int{"writer": 1, "architect": 0} {
		args, _ := json.Marshal(map[string]any{"chapter": chapter})
		result, err := tool.Execute(context.Background(), args)
		if err != nil {
			t.Fatalf("[%s] Execute: %v", name, err)
		}
		var payload map[string]any
		if err := json.Unmarshal(result, &payload); err != nil {
			t.Fatalf("[%s] Unmarshal: %v", name, err)
		}
		working, ok := payload["working_memory"].(map[string]any)
		if !ok {
			t.Fatalf("[%s] missing working_memory", name)
		}
		if _, exists := working["user_directives"]; exists {
			t.Errorf("[%s] working_memory 不应再有 user_directives（已统一到 user_rules）", name)
		}
		// user_rules 仍应稳定注入
		if _, ok := working["user_rules"].(map[string]any); !ok {
			t.Errorf("[%s] working_memory.user_rules 应稳定注入", name)
		}
	}
}

func TestContextToolLocksRequestedChapterToPendingRewriteTarget(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init("test", 3); err != nil {
		t.Fatalf("InitProgress: %v", err)
	}
	if err := s.Progress.MarkChapterComplete(1, 2000, "", ""); err != nil {
		t.Fatalf("MarkChapterComplete: %v", err)
	}
	if err := s.Progress.SetPendingRewrites([]int{1}, "返工第一章"); err != nil {
		t.Fatalf("SetPendingRewrites: %v", err)
	}
	if err := s.Outline.SaveOutline([]domain.OutlineEntry{
		{Chapter: 1, Title: "返工目标章"},
		{Chapter: 2, Title: "未来续写章"},
	}); err != nil {
		t.Fatalf("SaveOutline: %v", err)
	}

	tool := NewContextTool(s, References{}, "default")
	args, _ := json.Marshal(map[string]any{"chapter": 2})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(result, &payload); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	active, ok := payload["active_chapter_task"].(map[string]any)
	if !ok {
		t.Fatalf("expected active_chapter_task, got %T", payload["active_chapter_task"])
	}
	if active["chapter"] != float64(1) || active["requested_chapter"] != float64(2) || active["corrected"] != true {
		t.Fatalf("unexpected chapter correction: %+v", active)
	}
	if _, ok := payload["rewrite_brief"]; !ok {
		t.Fatal("corrected context must load the pending rewrite chapter brief")
	}
	current, ok := payload["current_chapter_outline"].(map[string]any)
	if !ok || current["title"] != "返工目标章" {
		t.Fatalf("expected only rewrite target outline, got %+v", payload["current_chapter_outline"])
	}
	for _, hidden := range []string{"outline", "next_chapter_outline", "future_outline_window"} {
		if _, ok := payload[hidden]; ok {
			t.Fatalf("rewrite context must hide %s", hidden)
		}
		if working, ok := payload["working_memory"].(map[string]any); ok {
			if _, ok := working[hidden]; ok {
				t.Fatalf("rewrite working_memory must hide %s", hidden)
			}
		}
	}
}
