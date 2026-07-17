package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/chenhongyang/novel-studio/internal/aigc"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/errs"
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

func TestContextToolInjectsLiteraryRenderingReference(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	tool := NewContextTool(s, References{
		LiteraryRendering:      "文学渲染协议：焦点化、目标因果、自由间接话语与适用边界",
		LiteraryRenderingCards: `{"version":1,"cards":[{"id":"focalization-boundary","decision":"谁在感知"}]}`,
	}, "default")
	result, err := tool.Execute(context.Background(), json.RawMessage(`{"chapter":8}`))
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
	if got, ok := refs["literary_rendering"].(string); !ok || !strings.Contains(got, "适用边界") {
		t.Fatalf("expected literary_rendering reference, got %#v", refs["literary_rendering"])
	}
	cards, ok := pack["literary_rendering_cards"].(map[string]any)
	if !ok {
		t.Fatalf("expected compact literary_rendering_cards, got %#v", pack["literary_rendering_cards"])
	}
	items, ok := cards["cards"].([]any)
	if !ok || len(items) != 1 || items[0].(map[string]any)["id"] != "focalization-boundary" {
		t.Fatalf("unexpected compact literary card catalog: %#v", cards)
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
			"literary_rendering_cards": map[string]any{
				"version": 1,
				"cards":   []any{map[string]any{"id": "focalization-boundary"}},
			},
		},
	}

	if trimByBudget(result, 80) {
		t.Fatal("80-byte budget should report that the protected compact catalog cannot safely fit")
	}

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
	if _, ok := pack["literary_rendering_cards"]; !ok {
		t.Fatal("compact literary card catalog must survive full-reference budget trimming")
	}
}

func TestTrimByBudgetPreservesCompactLiteraryCardsAtPlanningBudget(t *testing.T) {
	cardIDs := []string{
		"focalization-boundary",
		"psychic-distance",
		"scene-summary",
		"goal-causality",
		"emotion-appraisal",
		"motif-return",
		"syntax-rhythm",
		"free-indirect-discourse",
		"dialogue-subtext",
	}
	cards := make([]any, 0, len(cardIDs))
	for _, id := range cardIDs {
		cards = append(cards, map[string]any{
			"id":       id,
			"decision": "本章是否需要这项文学决策",
			"move":     "按场景功能选择，不设置次数或比例",
			"avoid":    "不要机械套用",
		})
	}
	fullReferences := map[string]string{
		"literary_rendering": strings.Repeat("完整论文摘要与中文转译边界。", 8000),
		"other_reference":    strings.Repeat("其他低优先级参考。", 2000),
	}
	result := map[string]any{
		"references": fullReferences,
		"reference_pack": map[string]any{
			"references": fullReferences,
			"literary_rendering_cards": map[string]any{
				"version": 1,
				"policy":  "按本章功能选择，不做九项清单",
				"cards":   cards,
			},
		},
		"chapter_contract": map[string]any{
			"chapter": 1,
			"goal":    strings.Repeat("不可裁剪的当前章任务。", 500),
		},
	}

	const planningBudget = 64 * 1024
	if !trimByBudget(result, planningBudget) {
		t.Fatal("expected low-priority full references to make the planning payload fit")
	}

	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if len(encoded) > planningBudget {
		t.Fatalf("trimmed planning context exceeds budget: got=%d budget=%d", len(encoded), planningBudget)
	}
	pack, ok := result["reference_pack"].(map[string]any)
	if !ok {
		t.Fatal("expected reference_pack to survive planning budget trimming")
	}
	if _, ok := pack["references"]; ok {
		t.Fatal("expected full reference essays to be trimmed at planning budget")
	}
	catalog, ok := pack["literary_rendering_cards"].(map[string]any)
	if !ok {
		t.Fatalf("compact literary rendering catalog must survive planning budget trimming: %#v", pack)
	}
	keptCards, ok := catalog["cards"].([]any)
	if !ok || len(keptCards) != len(cardIDs) {
		t.Fatalf("expected all compact literary cards after trimming, got %#v", catalog["cards"])
	}
	if _, ok := result["chapter_contract"]; !ok {
		t.Fatal("current chapter contract must survive reference trimming")
	}
}

func TestTrimByBudgetPreservesStrongestRAGFactInsteadOfReportingPhantomRecall(t *testing.T) {
	items := []domain.RecallItem{
		{Kind: "rag", Key: "rank-1", Reason: "当前章直接相关", Summary: strings.Repeat("最强连续性事实。", 500)},
		{Kind: "rag", Key: "rank-2", Reason: "次相关", Summary: strings.Repeat("次级事实。", 500)},
		{Kind: "rag", Key: "rank-3", Reason: "弱相关", Summary: strings.Repeat("弱事实。", 500)},
	}
	result := map[string]any{
		"selected_memory":     map[string]any{"rag_recall": items},
		"book_world_context":  strings.Repeat("可从当前 plan 重建的宽世界快照。", 900),
		"active_chapter_task": map[string]any{"chapter": 1, "goal": "保留当前任务"},
	}
	if !trimByBudget(result, 20000, 1) {
		t.Fatal("one strongest RAG fact plus critical task should fit after trimming broad snapshots")
	}
	selected := result["selected_memory"].(map[string]any)
	kept, ok := selected["rag_recall"].([]domain.RecallItem)
	if !ok || len(kept) != 1 || kept[0].Key != "rank-1" {
		t.Fatalf("budget trim did not preserve exactly the strongest RAG fact: %#v", selected["rag_recall"])
	}
	trimmed, _ := result["_trimmed"].([]string)
	if !slices.Contains(trimmed, "rag_recall:top1_preserved") {
		t.Fatalf("RAG delivery compaction was not observable: %#v", trimmed)
	}
}

func TestContextToolPlanningProfileFinalPayloadFitsBudgetAndKeepsCards(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	tool := NewContextTool(s, References{
		Consistency:            strings.Repeat("低优先级一致性资料。", 7000),
		LiteraryRendering:      strings.Repeat("完整文学研究论证。", 7000),
		LiteraryRenderingCards: `{"version":1,"policy":"按章选择","cards":[{"id":"focalization-boundary","decision":"谁在感知","move":"限定信息权限","avoid":"跨脑读取"}]}`,
	}, "default")
	raw, err := tool.Execute(context.Background(), json.RawMessage(`{"chapter":1,"profile":"planning"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(raw) > contextBudget(1, "planning") {
		t.Fatalf("final planning payload exceeds budget: got=%d budget=%d", len(raw), contextBudget(1, "planning"))
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if payload["_context_profile"] != "planning" || payload["_reading_guide"] == "" || payload["_loading_summary"] == "" {
		t.Fatalf("final metadata must be counted inside the payload budget: %#v", payload)
	}
	pack, ok := payload["reference_pack"].(map[string]any)
	if !ok {
		t.Fatalf("expected reference_pack after planning trim, got %#v", payload["reference_pack"])
	}
	if _, ok := pack["references"]; ok {
		t.Fatal("full reference essays should be trimmed before compact cards")
	}
	if _, ok := pack["literary_rendering_cards"].(map[string]any); !ok {
		t.Fatalf("compact literary cards must survive final Execute budget: %#v", pack)
	}
	trimmed, ok := payload["_trimmed"].([]any)
	if !ok || len(trimmed) == 0 || !strings.Contains(payload["_loading_summary"].(string), "裁剪:") {
		t.Fatalf("trim history and loading summary must be part of final payload: trimmed=%#v summary=%#v", payload["_trimmed"], payload["_loading_summary"])
	}
}

func TestFinalizeContextResultRejectsOversizedCriticalPayload(t *testing.T) {
	result := map[string]any{
		"active_chapter_task": map[string]any{
			"mode":   "rewrite",
			"policy": strings.Repeat("不可静默删除的当前任务。", 12000),
		},
	}
	raw, err := finalizeContextResult(result, 1, "planning")
	if err == nil || raw != nil {
		t.Fatalf("expected oversized critical payload to fail explicitly, raw=%d err=%v", len(raw), err)
	}
	if !errors.Is(err, errs.ErrToolPrecondition) {
		t.Fatalf("expected ErrToolPrecondition, got %v", err)
	}
	if _, ok := result["active_chapter_task"]; !ok {
		t.Fatal("budget failure must not delete the active chapter task")
	}
}

func TestFinalizeContextResultAllowsOnlyBoundedRewritePlanningOverflow(t *testing.T) {
	t.Run("rewrite planning keeps protected source after preferred trimming", func(t *testing.T) {
		working := map[string]any{
			"rewrite_source": map[string]any{
				"current_body": strings.Repeat("b", 42*1024),
			},
			"rewrite_brief": map[string]any{
				"brief_markdown": strings.Repeat("r", 26*1024),
			},
			"current_chapter_outline": map[string]any{"chapter": 1, "goal": "保留当前章任务"},
		}
		result := map[string]any{
			"working_memory": working,
			"references":     strings.Repeat("低优先级资料", 12000),
		}

		raw, err := finalizeContextResult(result, 1, "planning")
		if err != nil {
			t.Fatalf("bounded rewrite planning payload should fit the hard ceiling: %v", err)
		}
		if len(raw) <= contextBudget(1, "planning") {
			t.Fatalf("fixture did not exercise preferred-budget overflow: got=%d preferred=%d", len(raw), contextBudget(1, "planning"))
		}
		if len(raw) > contextHardBudget(1, "planning") {
			t.Fatalf("rewrite planning payload exceeded hard ceiling: got=%d hard=%d", len(raw), contextHardBudget(1, "planning"))
		}
		var payload map[string]any
		if err := json.Unmarshal(raw, &payload); err != nil {
			t.Fatal(err)
		}
		if _, ok := payload["references"]; ok {
			t.Fatal("bounded overflow must not restore low-priority references")
		}
		budget, ok := payload["_context_budget"].(map[string]any)
		if !ok || budget["mode"] != "rewrite_critical_overflow" {
			t.Fatalf("bounded overflow must be observable: %#v", payload["_context_budget"])
		}
		kept := payload["working_memory"].(map[string]any)
		if _, ok := kept["rewrite_source"]; !ok {
			t.Fatal("rewrite source was silently removed")
		}
		if _, ok := kept["rewrite_brief"]; !ok {
			t.Fatal("rewrite brief was silently removed")
		}
	})

	t.Run("ordinary planning cannot consume the overflow reserve", func(t *testing.T) {
		result := map[string]any{
			"active_chapter_task": map[string]any{
				"mode":   "new_chapter",
				"policy": strings.Repeat("x", 70*1024),
			},
		}
		if raw, err := finalizeContextResult(result, 1, "planning"); err == nil || raw != nil {
			t.Fatalf("ordinary planning must remain capped at the preferred budget, raw=%d err=%v", len(raw), err)
		}
	})

	t.Run("rewrite planning still fails above the hard ceiling", func(t *testing.T) {
		result := map[string]any{
			"working_memory": map[string]any{
				"rewrite_source": map[string]any{
					"current_body": strings.Repeat("x", 100*1024),
				},
			},
		}
		raw, err := finalizeContextResult(result, 1, "planning")
		if err == nil || raw != nil {
			t.Fatalf("rewrite planning above the hard ceiling must fail, raw=%d err=%v", len(raw), err)
		}
		if !errors.Is(err, errs.ErrToolPrecondition) {
			t.Fatalf("expected fail-closed ErrToolPrecondition, got %v", err)
		}
	})
}

func TestWorldSimulationContextCompactsSupersededCausalPayloadAfterStructuralEscalation(t *testing.T) {
	st := newChapterSimulationTestStore(t)
	if err := st.Outline.SaveOutline([]domain.OutlineEntry{{
		Chapter: 1, Title: "旧因果耗尽后重推", CoreEvent: "保留事实结果，重组场景因果",
	}}); err != nil {
		t.Fatal(err)
	}
	source := prepareRewriteSourceTest(t, st,
		"第一章\n\n林澈保住既定结果，但旧场景结构已经耗尽。",
		"# 返工\n\n## 保留事实\n\n- 林澈保住既定结果。\n\n## 必须修正\n\n- 废弃旧场景因果。\n",
	)
	oldAction := strings.Repeat("旧场景动作链只用于预算回归。", 5000)
	lin := simulatedDecision("林澈", "沿用旧选择", true)
	lin.Action = oldAction
	shen := simulatedDecision("沈知遥", "继续旧安排", false)
	shen.Action = oldAction
	old := domain.ChapterWorldSimulation{
		Version: 1, Chapter: 1, TimeWindow: "旧推演时段", RewriteSource: source,
		Sources:            []string{rewriteSourceToken(source), rewriteBriefToken(source)},
		CharacterDecisions: []domain.CharacterWorldDecision{lin, shen},
		ProtagonistProjection: domain.ProtagonistDecisionProjection{
			Protagonist: "林澈", ObservableEffects: []string{"既定结果"}, HiddenPressures: []string{"旧离屏压力"},
			AvailableOptions: []string{"沿用", "改写"}, ChosenDecision: "沿用旧选择", DecisionReason: "旧证据链",
			PlanConstraints: []string{"旧限制"}, CausalChain: []string{"旧场景", "旧选择"},
		},
		RewriteFactCoverage: []domain.ChapterRewriteFactCoverage{{
			Fact: "林澈保住既定结果。", SimulationEvidence: []string{"旧选择承接结果"},
		}},
	}
	old.SimulationID = chapterWorldSimulationID(old)
	if err := st.SaveChapterWorldSimulation(old); err != nil {
		t.Fatal(err)
	}
	if err := ensureChapterWorldSimulationCheckpoint(st, 1); err != nil {
		t.Fatal(err)
	}
	plan := domain.ChapterPlan{Chapter: 1, Title: "旧计划"}
	plan.CausalSimulation.WorldSimulationID = old.SimulationID
	plan.CausalSimulation.ReviewRefinement.IterationLimit = 2
	if err := st.Drafts.SaveChapterPlan(plan); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Checkpoints.AppendArtifactLatest(domain.ChapterScope(1), "plan", "drafts/01.plan.json"); err != nil {
		t.Fatal(err)
	}
	for _, digest := range []string{"sha256:blocked-a", "sha256:blocked-b"} {
		if _, err := st.Checkpoints.Append(domain.ChapterScope(1), "draft-structural-block", "drafts/01.draft.md", digest); err != nil {
			t.Fatal(err)
		}
	}
	if escalation := InspectRenderOnlyReplanEscalation(st, 1); !escalation.Required {
		t.Fatalf("test did not establish structural escalation: %+v", escalation)
	}

	raw, err := NewContextTool(st, References{}, "default").Execute(
		context.Background(), json.RawMessage(`{"chapter":1,"profile":"world_simulation"}`),
	)
	if err != nil {
		t.Fatalf("focused world simulation context must fit without full-profile fallback: %v", err)
	}
	if len(raw) > contextBudget(1, "world_simulation") {
		t.Fatalf("world simulation context exceeded focused budget: got=%d budget=%d", len(raw), contextBudget(1, "world_simulation"))
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	world, ok := payload["chapter_world_simulation"].(map[string]any)
	if !ok || world["status"] != "superseded_for_structural_replan" || world["previous_simulation_id"] != old.SimulationID {
		t.Fatalf("old simulation was not compactly marked superseded: %#v", payload["chapter_world_simulation"])
	}
	for _, leaked := range []string{"character_decisions", "protagonist_projection", "rewrite_fact_coverage"} {
		if _, exists := world[leaked]; exists {
			t.Fatalf("superseded causal surface leaked %s: %#v", leaked, world[leaked])
		}
	}
	working := payload["working_memory"].(map[string]any)
	if _, ok := working["rewrite_source"]; !ok {
		t.Fatal("canonical rewrite source was lost while superseding old simulation")
	}
	if _, ok := working["current_chapter_outline"]; !ok {
		t.Fatal("current chapter outline was lost while superseding old simulation")
	}
	saved, err := st.LoadChapterWorldSimulation(1)
	if err != nil || saved == nil || saved.CharacterDecisions[0].Action != oldAction {
		t.Fatalf("read-only context compaction mutated the formal simulation: saved=%+v err=%v", saved, err)
	}

	// A leftover POV plan partial must not bypass the same structural escalation
	// through the staged-repair fast path.
	if err := st.Drafts.SaveChapterPlanPartial(1, map[string]any{
		"structure":         map[string]any{"chapter": 1, "title": "旧骨架"},
		"causal_simulation": map[string]any{"world_simulation_id": old.SimulationID},
	}); err != nil {
		t.Fatal(err)
	}
	stagedRaw, err := NewContextTool(st, References{}, "default").Execute(
		context.Background(), json.RawMessage(`{"chapter":1,"profile":"world_simulation"}`),
	)
	if err != nil {
		t.Fatalf("staged repair path must also fit the focused world budget: %v", err)
	}
	var staged map[string]any
	if err := json.Unmarshal(stagedRaw, &staged); err != nil {
		t.Fatal(err)
	}
	stagedWorld, _ := staged["chapter_world_simulation"].(map[string]any)
	if stagedWorld["status"] != "superseded_for_structural_replan" {
		t.Fatalf("staged repair replayed the exhausted simulation: %#v", stagedWorld)
	}
	if _, leaked := stagedWorld["protagonist_projection"]; leaked {
		t.Fatalf("staged repair leaked the exhausted protagonist projection: %#v", stagedWorld)
	}
}

func TestPartialWorldSimulationContextReturnsLockedDecisionsNeededForProjection(t *testing.T) {
	st := newChapterSimulationTestStore(t)
	lin := simulatedDecision("林澈", "先叫停并让老丁退线", true)
	shen := simulatedDecision("沈知遥", "只检查已完成的自纠", true)
	he := simulatedDecision("贺骁", "只接通电话，尚未答复", true)
	other := simulatedDecision("旁支角色", "保持背景", false)
	partial := domain.ChapterWorldSimulation{
		Version: 1, Chapter: 1, TimeWindow: "当晚",
		CharacterDecisions: []domain.CharacterWorldDecision{lin, shen, he, other},
		RewriteSource: &domain.ChapterRewriteSource{PreserveFacts: []string{
			"林澈先叫停，沈知遥后检查；贺骁尚未答复。",
		}},
		ProtagonistProjection: domain.ProtagonistDecisionProjection{
			Protagonist: "林澈", ChosenDecision: lin.Decision,
		},
	}
	if err := st.SaveChapterWorldSimulationPartial(partial); err != nil {
		t.Fatal(err)
	}
	result := map[string]any{}
	NewContextTool(st, References{}, "default").buildChapterWorldSimulationContext(result, 1, func(string, error) {})
	world, ok := result["chapter_world_simulation"].(map[string]any)
	if !ok || world["status"] != "partial" {
		t.Fatalf("unexpected partial context: %#v", result["chapter_world_simulation"])
	}
	locked, ok := world["locked_character_decisions"].([]domain.CharacterWorldDecision)
	if !ok {
		t.Fatalf("locked decisions missing from partial context: %#v", world)
	}
	if got := characterDecisionNames(locked); !slices.Equal(got, []string{"林澈", "沈知遥", "贺骁"}) {
		t.Fatalf("preserve-fact decisions were not returned exactly: %v", got)
	}
	if !strings.Contains(world["projection_policy"].(string), "不得用旧稿覆盖") {
		t.Fatalf("projection authority policy missing: %#v", world)
	}
}

func TestCompletePartialWorldSimulationContextIsReadyToFinalizeWithoutResubmission(t *testing.T) {
	st := newChapterSimulationTestStore(t)
	lin := simulatedDecision("林澈", "承认失业", true)
	shen := simulatedDecision("沈知遥", "继续例行工作", false)
	partial := domain.ChapterWorldSimulation{
		Version: 1, Chapter: 1, TimeWindow: "同一天晚饭前后两小时",
		CharacterDecisions: []domain.CharacterWorldDecision{lin, shen},
		ProtagonistProjection: domain.ProtagonistDecisionProjection{
			Protagonist: "林澈", ObservableEffects: []string{"追问落到饭桌"}, HiddenPressures: []string{"场外安排尚未传回"},
			AvailableOptions: []string{"继续隐瞒", "承认失业"}, ChosenDecision: lin.Decision,
			DecisionReason: "继续隐瞒会扩大误解", PlanConstraints: []string{"只写亲见信息"},
			CausalChain: []string{"亲戚追问", "父母护短", "林澈承认失业"},
		},
	}
	if gaps := chapterWorldSimulationGaps(st, partial); len(gaps) != 0 {
		t.Fatalf("test fixture must be complete before context status check: %v", gaps)
	}
	if err := st.SaveChapterWorldSimulationPartial(partial); err != nil {
		t.Fatal(err)
	}
	result := map[string]any{}
	NewContextTool(st, References{}, "default").buildChapterWorldSimulationContext(result, 1, func(string, error) {})
	world := result["chapter_world_simulation"].(map[string]any)
	if world["status"] != "ready_to_finalize" || !strings.Contains(world["policy"].(string), "不得重发") {
		t.Fatalf("complete partial did not receive finalize-only state: %#v", world)
	}
	if _, leaked := result["simulation_character_authority"]; leaked {
		t.Fatal("gap-free partial must not build/replay authority when only finalize=true is legal")
	}
}

func TestStagedPlanRepairGapFreeWorldSimulationIsFinalizeOnly(t *testing.T) {
	st := newChapterSimulationTestStore(t)
	if err := st.Drafts.SaveChapterPlanPartial(1, map[string]any{
		"structure":         map[string]any{"chapter": 1, "title": "旧骨架"},
		"causal_simulation": map[string]any{},
	}); err != nil {
		t.Fatal(err)
	}
	lin := simulatedDecision("林澈", "承认失业", true)
	shen := simulatedDecision("沈知遥", "继续例行工作", false)
	partial := domain.ChapterWorldSimulation{
		Version: 1, Chapter: 1, TimeWindow: "同一天晚饭前后两小时",
		CharacterDecisions: []domain.CharacterWorldDecision{lin, shen},
		ProtagonistProjection: domain.ProtagonistDecisionProjection{
			Protagonist: "林澈", ObservableEffects: []string{"追问落到饭桌"}, HiddenPressures: []string{"场外安排尚未传回"},
			AvailableOptions: []string{"继续隐瞒", "承认失业"}, ChosenDecision: lin.Decision,
			DecisionReason: "继续隐瞒会扩大误解", PlanConstraints: []string{"只写亲见信息"},
			CausalChain: []string{"亲戚追问", "父母护短", "林澈承认失业"},
		},
	}
	if gaps := chapterWorldSimulationGaps(st, partial); len(gaps) != 0 {
		t.Fatalf("fixture must be gap-free: %v", gaps)
	}
	if err := st.SaveChapterWorldSimulationPartial(partial); err != nil {
		t.Fatal(err)
	}

	result, ok, err := NewContextTool(st, References{}, "default").stagedPlanRepairContext(1, 1, false)
	if err != nil || !ok {
		t.Fatalf("stagedPlanRepairContext: ok=%v err=%v", ok, err)
	}
	world, _ := result["chapter_world_simulation"].(map[string]any)
	if world["status"] != "ready_to_finalize" || !strings.Contains(world["policy"].(string), "不得重发") {
		t.Fatalf("gap-free partial was not finalize-only: %#v", world)
	}
	if !strings.Contains(result["next_step"].(string), "finalize=true") || !strings.Contains(result["next_step"].(string), "不得重发") {
		t.Fatalf("staged next step did not force finalize-only: %#v", result["next_step"])
	}
	working := result["working_memory"].(map[string]any)
	stage := working["chapter_plan_stage"].(map[string]any)
	if !strings.Contains(stage["policy"].(string), "只允许 finalize=true") {
		t.Fatalf("staged policy did not preserve finalize-only state: %#v", stage)
	}
	if _, leaked := result["simulation_character_authority"]; leaked {
		t.Fatal("gap-free finalize-only context must not replay the full authority packet")
	}
}

func TestPlanningProfileCompactsReadySimulationAndKeepsBoundEvidence(t *testing.T) {
	result, exactFact, exactDecision := heavyReadyPlanningContextFixture()
	raw, err := finalizeContextResult(result, 1, "planning")
	if err != nil {
		t.Fatalf("ready rewrite planning context must converge: %v", err)
	}
	if len(raw) > contextBudget(1, "planning") {
		t.Fatalf("ready planning context should fit preferred budget after deterministic compaction: got=%d budget=%d", len(raw), contextBudget(1, "planning"))
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	for _, removed := range []string{"simulation_character_authority", "simulation_character_authority_policy", "simulation_characters"} {
		if _, exists := payload[removed]; exists {
			t.Fatalf("ready planning payload replayed %s", removed)
		}
	}
	receipt, _ := payload["simulation_authority_receipt"].(map[string]any)
	if receipt["validation"] != "finalized_and_source_bound" || receipt["required_count"] != float64(19) {
		t.Fatalf("authority receipt lost validation/count: %#v", receipt)
	}
	world, _ := payload["chapter_world_simulation"].(map[string]any)
	if _, leaked := world["character_decisions"]; leaked {
		t.Fatal("planning payload replayed full off-screen character decisions")
	}
	projection, _ := world["protagonist_projection"].(map[string]any)
	if projection["chosen_decision"] != exactDecision {
		t.Fatalf("protagonist decision drifted: %#v", projection)
	}
	coverage, _ := world["rewrite_fact_coverage"].([]any)
	if len(coverage) != 2 {
		t.Fatalf("coverage receipt missing facts: %#v", world["rewrite_fact_coverage"])
	}
	first, _ := coverage[0].(map[string]any)
	if first["fact"] != exactFact || first["evidence_count"] != float64(2) || len(first["evidence_sha256"].(string)) != 64 {
		t.Fatalf("coverage fact/receipt drifted: %#v", first)
	}
	if _, leaked := first["simulation_evidence"]; leaked {
		t.Fatal("verbose simulation evidence was not folded into an auditable digest")
	}
	working, _ := payload["working_memory"].(map[string]any)
	source, _ := working["rewrite_source"].(map[string]any)
	if _, leaked := source["current_body"]; leaked {
		t.Fatal("ready planning payload retained the full old body")
	}
	if _, leaked := source["brief_markdown"]; leaked {
		t.Fatal("ready planning payload retained duplicate full brief markdown")
	}
	chapter, _ := source["chapter"].(map[string]any)
	preserve, _ := chapter["preserve_facts"].([]any)
	if len(preserve) != 2 || preserve[0] != exactFact {
		t.Fatalf("exact preserve facts were lost: %#v", chapter)
	}
	brief, _ := working["rewrite_brief"].(map[string]any)
	for _, key := range []string{"user_requirements", "required_corrections", "whole_text_single_segment_gates", "acceptance_conditions"} {
		if values, _ := brief[key].([]any); len(values) == 0 {
			t.Fatalf("structured rewrite brief lost %s: %#v", key, brief)
		}
	}
	craft, _ := payload["rewrite_craft_pack"].(map[string]any)
	if craft["receipt_id"] != "receipt-current" || craft["source_token"] != "craft_recall:receipt-current" {
		t.Fatalf("RAG craft receipt was lost or changed: %#v", craft)
	}
}

func TestWorldSimulationProfileCompactsAlreadyReadySimulation(t *testing.T) {
	result, _, _ := heavyReadyPlanningContextFixture()
	raw, err := finalizeContextResult(result, 1, "world_simulation")
	if err != nil {
		t.Fatalf("terminal world-simulation view must converge instead of replaying authority: %v", err)
	}
	if len(raw) > contextBudget(1, "world_simulation") {
		t.Fatalf("terminal world-simulation payload exceeded focused budget: %d", len(raw))
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	if _, exists := payload["simulation_character_authority"]; exists {
		t.Fatal("already-ready simulator view replayed the full authority packet")
	}
	world := payload["chapter_world_simulation"].(map[string]any)
	if world["status"] != "ready" {
		t.Fatalf("ready status changed during compaction: %#v", world)
	}
	if _, exists := world["character_decisions"]; exists {
		t.Fatal("already-ready simulator view replayed full decisions")
	}
}

func TestWorldSimulationProfileLayersInvalidNineteenCharacterAuthority(t *testing.T) {
	result := heavyInvalidWorldSimulationContextFixture()
	before, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) <= contextBudget(1, "world_simulation") {
		t.Fatalf("fixture must reproduce a pre-compaction overflow: got=%d", len(before))
	}
	raw, err := finalizeContextResult(result, 1, "world_simulation")
	if err != nil {
		t.Fatalf("invalid simulation repair context must converge without dropping authority: %v", err)
	}
	if len(raw) > contextBudget(1, "world_simulation") {
		t.Fatalf("layered repair context exceeded world budget: got=%d", len(raw))
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	world := payload["chapter_world_simulation"].(map[string]any)
	if world["status"] != "invalid" || len(world["gaps"].([]any)) == 0 {
		t.Fatalf("invalid simulation gaps were lost: %#v", world)
	}
	packet, _ := payload["simulation_character_authority"].(map[string]any)
	if packet["format"] != "layered_v1" {
		t.Fatalf("authority was not delivered as layered packet: %#v", packet)
	}
	entries, _ := packet["entries"].([]any)
	if len(entries) != 19 {
		t.Fatalf("authority roster was capped or dropped: %d", len(entries))
	}
	hold := entries[10].(map[string]any)
	holdContract := hold["hold_baseline_contract"].(map[string]any)
	if hold["authority_mode"] != simulationAuthorityHoldBaseline || holdContract["decision_reason"] != simulationAuthorityMissing || holdContract["character"] != "角色11" {
		t.Fatalf("hold-baseline exact contract drifted: %#v", hold)
	}
	rewriteOnly := entries[18].(map[string]any)
	rewriteContract := rewriteOnly["rewrite_source_only_contract"].(map[string]any)
	if rewriteOnly["authority_mode"] != "rewrite_source_only" || rewriteContract["action"] != "赵航低头回复了一条消息。" {
		t.Fatalf("rewrite-source-only exact action drifted: %#v", rewriteOnly)
	}
	authoritative := entries[0].(map[string]any)
	if authoritative["knowledge_boundary"] != "只知道亲见和合法通信" || authoritative["current_location"] != "林家饭桌" {
		t.Fatalf("authoritative current facts were lost: %#v", authoritative)
	}
	working := payload["working_memory"].(map[string]any)
	source := working["rewrite_source"].(map[string]any)
	if _, leaked := source["current_body"]; leaked {
		t.Fatal("world repair payload retained duplicate old body despite exact blocking contracts")
	}
	brief := working["rewrite_brief"].(map[string]any)
	if gates, _ := brief["whole_text_single_segment_gates"].([]any); len(gates) == 0 {
		t.Fatalf("world repair lost exact whole-text gate: %#v", brief)
	}
}

func TestWorldSimulationProfileKeepsActiveGroundedAuthorityVisibleAfterCodexMiddleClip(t *testing.T) {
	const codexPerMessageTextRuneBudget = 45_000
	const (
		groundedCharacter = "梁广财"
		currentGoal       = "梁广财要以农户合作社代表的立场，在第一章核心事件里做出一个可验证、会留下后果的选择。"
		currentAction     = "由角色卡推出：50岁，山地果蔬乡镇合作社负责人，关心果蔬卖价、损耗和运输，话不多但认结果。直播助农和冷链仓推进中，他代表农户的真实期待和警惕。；务实；谨慎；重承诺；不爱空话；行动时先保留边界，再换取新信息。"
		currentPressure   = "返乡接风饭上，梁广财借关心追问失业补偿、工资和下一份工作。"
		knowledgeBoundary = "只知道角色卡、自己经历、通信中获得的信息；不知道主角内心、后台规则和其他配角未传回的时间线。"
		decisionModel     = "先核验证据，再决定是否承诺、交易、暴露秘密或升级冲突。"
	)

	authority := make([]simulationCharacterAuthority, 0, 16)
	characters := make([]string, 0, 16)
	present := make([]string, 0, 15)
	for i := 1; i <= 15; i++ {
		name := fmt.Sprintf("已落盘角色%02d", i)
		characters = append(characters, name)
		present = append(present, name)
		authority = append(authority, simulationCharacterAuthority{
			Character:        name,
			Role:             "已完成当前章决定的项目角色",
			Tier:             "secondary",
			Aliases:          []string{name + "别名"},
			AuthoritySources: []string{"meta/characters/" + name + "/" + strings.Repeat("source-anchor-", 12)},
			AuthorityMode:    "reuse_saved_decision",
			SimulationStatus: "already_present",
			Blocking:         false,
		})
	}
	characters = append(characters, groundedCharacter)
	authority = append(authority, simulationCharacterAuthority{
		Character:               groundedCharacter,
		Role:                    "农户合作社代表",
		Tier:                    "secondary",
		Aliases:                 []string{"梁叔", "二姨夫"},
		Description:             "50岁，山地果蔬乡镇合作社负责人，关心果蔬卖价、损耗和运输，话不多但认结果。",
		Arc:                     strings.Repeat("未来弧线不得进入当前 grounded 决策。", 2_400),
		CurrentLocation:         "返乡接风饭上",
		CurrentStatus:           simulationAuthorityUnknown,
		CurrentGoal:             currentGoal,
		CurrentAction:           currentAction,
		CurrentPressure:         currentPressure,
		CurrentPressurePolicy:   "outline_authorized_concise",
		Resources:               nil,
		Relationships:           []string{"林澈｜试探/未结盟基线｜未新增正文债务或信任"},
		KnowledgeBoundary:       knowledgeBoundary,
		DecisionModel:           decisionModel,
		VisibleInCurrentChapter: true,
		SimulationStatus:        "required_missing",
		AuthorityMode:           domain.SimulationAuthorityModeGrounded,
		AuthoritySources:        []string{"meta/initial_character_dynamics.json:梁广财"},
		MissingAuthority:        []string{"current_status"},
		Blocking:                false,
		DecisionPolicy:          projectAllGroundedDecisionPolicy,
	})

	result := map[string]any{
		"simulation_characters":          characters,
		"simulation_character_authority": authority,
		// This precedes simulation_character_authority in marshalOrderedContext,
		// reproducing the substantial project state ahead of the real authority
		// roster instead of testing the packet in isolation.
		"project_all_state": map[string]any{
			"continuity_evidence": strings.Repeat("project-state-anchor|", 760),
		},
		"simulation_character_authority_policy": map[string]any{
			"required_count": 16,
			"blocking_count": 0,
		},
		"chapter_world_simulation": map[string]any{
			"status":             "invalid",
			"characters_present": present,
			"gaps":               []string{"missing character decision: 梁广财"},
		},
		"working_memory": map[string]any{
			"current_chapter_outline": map[string]any{
				"chapter": 1,
				"scenes":  []string{"返乡接风饭上，梁广财一边夹菜一边追问下一份工作。"},
			},
		},
		// Unknown top-level context is deliberately retained and ordered after the
		// critical-first packet. It brings the complete tool result into the same
		// 53k+ rune range observed in the production Codex session.
		"zz_realistic_tool_tail": strings.Repeat("tail-context-anchor|", 1_700),
	}

	raw, err := finalizeContextResult(result, 1, "world_simulation")
	if err != nil {
		t.Fatalf("grounded repair context failed to finalize: %v", err)
	}
	if got := utf8.RuneCount(raw); got <= 53_000 {
		t.Fatalf("fixture must reproduce the 53k+ production tool result: got=%d", got)
	}

	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	packet := payload["simulation_character_authority"].(map[string]any)
	entries := packet["entries"].([]any)
	if len(entries) != 16 {
		t.Fatalf("authority roster changed during compaction: %d", len(entries))
	}
	grounded := entries[0].(map[string]any)
	if grounded["character"] != groundedCharacter {
		t.Fatalf("required-missing grounded entry was not moved to the active prefix: %#v", grounded)
	}
	for i, rawEntry := range entries[1:] {
		entry := rawEntry.(map[string]any)
		want := fmt.Sprintf("已落盘角色%02d", i+1)
		if entry["character"] != want {
			t.Fatalf("already-present relative order drifted at %d: want=%q got=%#v", i, want, entry["character"])
		}
	}
	for key, want := range map[string]string{
		"authority_mode":          domain.SimulationAuthorityModeGrounded,
		"current_goal":            currentGoal,
		"current_action":          currentAction,
		"current_pressure":        currentPressure,
		"current_pressure_policy": "outline_authorized_concise",
		"knowledge_boundary":      knowledgeBoundary,
		"decision_model":          decisionModel,
		"decision_policy":         projectAllGroundedDecisionPolicy,
	} {
		if got := strings.TrimSpace(fmt.Sprint(grounded[key])); got != want {
			t.Fatalf("grounded compact entry lost %s:\nwant=%q\ngot=%q", key, want, got)
		}
	}
	if resources, ok := grounded["resources"].([]any); !ok || len(resources) != 0 {
		t.Fatalf("empty grounded resource boundary must remain explicit: %#v", grounded["resources"])
	}
	if locks, ok := grounded["required_knowledge_boundaries"].([]any); !ok || len(locks) != 0 {
		t.Fatalf("empty grounded knowledge-lock set must remain explicit: %#v", grounded["required_knowledge_boundaries"])
	}
	for _, removed := range []string{"arc", "traits", "desires", "boundaries"} {
		if _, leaked := grounded[removed]; leaked {
			t.Fatalf("grounded compact entry retained duplicated/non-current field %q: %#v", removed, grounded)
		}
	}
	modePolicies := packet["mode_policies"].(map[string]any)
	if got := fmt.Sprint(modePolicies[domain.SimulationAuthorityModeGrounded]); got != projectAllGroundedDecisionPolicy {
		t.Fatalf("grounded shared mode policy was lost or changed: %q", got)
	}

	visible := compactCodexTextEquivalentForTest(string(raw), codexPerMessageTextRuneBudget)
	if got := utf8.RuneCountInString(visible); got != codexPerMessageTextRuneBudget {
		t.Fatalf("Codex-equivalent clipping returned %d runes, want %d", got, codexPerMessageTextRuneBudget)
	}
	if !strings.Contains(visible, "Codex 入参压缩") {
		t.Fatal("53k+ fixture did not exercise Codex-equivalent middle clipping")
	}
	encodedGrounded, err := json.Marshal(grounded)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(visible, string(encodedGrounded)) {
		t.Fatalf("complete Liang grounded contract is not model-visible after clipping: %s", encodedGrounded)
	}
}

// compactCodexTextEquivalentForTest intentionally mirrors llmcodex.compactCodexText.
// Keep this local copy exact so the regression tests the transport boundary the
// model actually sees rather than only novel_context's larger byte budget.
func compactCodexTextEquivalentForTest(s string, limit int) string {
	s = strings.TrimSpace(s)
	if limit <= 0 || utf8.RuneCountInString(s) <= limit {
		return s
	}
	if limit < 128 {
		limit = 128
	}
	runes := []rune(s)
	marker := fmt.Sprintf("\n\n[... Codex 入参压缩：省略 %d 字；保留首尾以维持上下文 ...]\n\n", len(runes)-limit)
	markerRunes := []rune(marker)
	available := limit - len(markerRunes)
	if available < 32 {
		available = 32
		markerRunes = []rune("\n\n[...省略...]\n\n")
	}
	head := available / 2
	tail := available - head
	if head+tail > len(runes) {
		return s
	}
	return string(runes[:head]) + string(markerRunes) + string(runes[len(runes)-tail:])
}

func TestRestartedWorldSimulationProfileFitsWithoutDroppingAuthorityOrFactGaps(t *testing.T) {
	result, preserveFacts, corrections, instructionToken := heavyRestartedWorldSimulationContextFixture()
	before, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) <= contextBudget(1, "world_simulation") {
		t.Fatalf("fixture must reproduce restarted-world overflow: got=%d", len(before))
	}
	raw, err := finalizeContextResult(result, 1, "world_simulation")
	if err != nil {
		t.Fatalf("restarted world context must converge without raising 96 KiB: %v", err)
	}
	if len(raw) > contextBudget(1, "world_simulation") {
		t.Fatalf("restarted world context exceeded 96 KiB: got=%d budget=%d", len(raw), contextBudget(1, "world_simulation"))
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	characters := stringSliceFromAny(payload["simulation_characters"])
	if len(characters) != 19 {
		t.Fatalf("full simulation roster was capped: %d %#v", len(characters), characters)
	}
	authority := payload["simulation_character_authority"].(map[string]any)
	entries := authority["entries"].([]any)
	if len(entries) != 19 {
		t.Fatalf("authority entries were capped: %d", len(entries))
	}
	blocking := 0
	for _, rawEntry := range entries {
		entry := rawEntry.(map[string]any)
		isBlocking, _ := entry["blocking"].(bool)
		if !isBlocking {
			if entry["authority_mode"] == "authoritative" && strings.TrimSpace(fmt.Sprint(entry["knowledge_boundary"])) == "" {
				t.Fatalf("authoritative knowledge boundary was lost: %#v", entry)
			}
			continue
		}
		blocking++
		switch entry["authority_mode"] {
		case simulationAuthorityHoldBaseline:
			if _, ok := entry["hold_baseline_contract"].(map[string]any); !ok {
				t.Fatalf("blocking hold contract was lost: %#v", entry)
			}
		case "rewrite_source_only":
			if _, ok := entry["rewrite_source_only_contract"].(map[string]any); !ok {
				t.Fatalf("blocking rewrite-source contract was lost: %#v", entry)
			}
		default:
			t.Fatalf("unknown blocking authority mode lost exact contract: %#v", entry)
		}
	}
	if blocking != 9 {
		t.Fatalf("blocking roster changed: got=%d want=9", blocking)
	}
	working := payload["working_memory"].(map[string]any)
	source := working["rewrite_source"].(map[string]any)
	if _, leaked := source["current_body"]; leaked {
		t.Fatal("restarted world context retained the old chapter body")
	}
	if _, leaked := source["brief_markdown"]; leaked {
		t.Fatal("restarted world context retained full brief markdown")
	}
	chapter := source["chapter"].(map[string]any)
	gotFacts := stringSliceFromAny(chapter["preserve_facts"])
	if !slices.Equal(gotFacts, preserveFacts) {
		t.Fatalf("preserve facts changed or were truncated:\nwant=%#v\ngot=%#v", preserveFacts, gotFacts)
	}
	brief := working["rewrite_brief"].(map[string]any)
	gotCorrections := stringSliceFromAny(brief["required_corrections"])
	if !slices.Equal(gotCorrections, corrections) {
		t.Fatalf("current required corrections changed or were truncated:\nwant=%#v\ngot=%#v", corrections, gotCorrections)
	}
	if _, leaked := brief["ai_voice_redflags"]; leaked {
		t.Fatal("AI voice prose leaked into restarted world context")
	}
	if _, ok := brief["mechanical_gate_receipt"].(map[string]any); !ok {
		t.Fatalf("mechanical dossier was not folded to a receipt: %#v", brief)
	}
	world := payload["chapter_world_simulation"].(map[string]any)
	gaps := stringSliceFromAny(world["gaps"])
	for _, fact := range preserveFacts {
		want := "rewrite_fact_coverage missing: " + fact
		if !slices.Contains(gaps, want) {
			t.Fatalf("exact fact gap was replaced by an index/pointer: %q in %#v", want, gaps)
		}
	}
	if !slices.Contains(gaps, "chapter_pipeline_instruction source missing: "+instructionToken) {
		t.Fatalf("instruction-token gap was lost: %#v", gaps)
	}
	instruction := payload["chapter_pipeline_instruction"].(map[string]any)
	if instruction["source_token"] != instructionToken || !strings.Contains(instruction["instruction"].(string), "完整用户硬合同") {
		t.Fatalf("full instruction/token was changed: %#v", instruction)
	}
}

func TestPlanningProfileWithInvalidSimulationKeepsGapsButDefersFullAuthority(t *testing.T) {
	result := heavyInvalidWorldSimulationContextFixture()
	raw, err := finalizeContextResult(result, 1, "planning")
	if err != nil {
		t.Fatalf("planning view of invalid simulation must converge: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	if _, exists := payload["simulation_character_authority"]; exists {
		t.Fatal("planning must defer full authority to profile=world_simulation")
	}
	receipt := payload["simulation_authority_receipt"].(map[string]any)
	if receipt["validation"] != "not_ready_plan_blocked" || receipt["required_count"] != float64(19) {
		t.Fatalf("planning authority deferral was not auditable: %#v", receipt)
	}
	world := payload["chapter_world_simulation"].(map[string]any)
	if world["status"] != "invalid" || len(world["gaps"].([]any)) == 0 {
		t.Fatalf("planning lost the reason plan_structure is blocked: %#v", world)
	}
	working := payload["working_memory"].(map[string]any)
	if _, leaked := working["rewrite_source"].(map[string]any)["current_body"]; leaked {
		t.Fatal("blocked planning view retained a large old-body blob")
	}
}

func TestStagedPlanningProfileReplaysCurrentRAGCraftReceipt(t *testing.T) {
	st := newRewriteCraftTestStore(t, true)
	structureRaw, err := NewPlanStructureTool(st).Execute(context.Background(), planStructureArgs(1))
	if err != nil {
		t.Fatal(err)
	}
	var structureResult map[string]any
	if err := json.Unmarshal(structureRaw, &structureResult); err != nil {
		t.Fatal(err)
	}
	issued, _ := structureResult["rewrite_craft_pack"].(map[string]any)
	if strings.TrimSpace(fmt.Sprint(issued["receipt_id"])) == "" {
		t.Fatalf("plan_structure did not issue the deterministic craft receipt: %s", structureRaw)
	}
	raw, err := NewContextTool(st, References{}, "default").Execute(
		context.Background(), json.RawMessage(`{"chapter":1,"profile":"planning"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	replayed, _ := payload["rewrite_craft_pack"].(map[string]any)
	if replayed["receipt_id"] != issued["receipt_id"] || replayed["source_token"] != issued["source_token"] {
		t.Fatalf("planning profile lost or changed its bound craft receipt: issued=%#v replayed=%#v", issued, replayed)
	}
	if attempts, _ := replayed["attempts"].([]any); len(attempts) == 0 {
		t.Fatalf("planning profile kept only a receipt label but lost its method attempts: %#v", replayed)
	}
}

func heavyInvalidWorldSimulationContextFixture() map[string]any {
	authority := make([]simulationCharacterAuthority, 0, 19)
	characters := make([]string, 0, 19)
	for i := 0; i < 19; i++ {
		name := fmt.Sprintf("角色%02d", i+1)
		characters = append(characters, name)
		entry := simulationCharacterAuthority{
			Character: name, Role: "配角", Tier: "important", VisibleInCurrentChapter: i < 8,
			SimulationStatus: "required_missing", AuthoritySources: []string{"meta/characters/" + name + "/dossier.json"},
			DecisionPolicy: strings.Repeat("重复的逐角色政策应提升到共享层。", 180),
		}
		switch {
		case i < 10:
			entry.AuthorityMode = "authoritative"
			entry.Description = "当前人物事实"
			entry.Traits = []string{"谨慎", "行动派"}
			entry.Desires = []string{"完成眼前目标"}
			entry.Boundaries = []string{"不知道场外秘密"}
			entry.Arc = strings.Repeat("未来弧线不是当前事实。", 220)
			entry.CurrentLocation = "林家饭桌"
			entry.CurrentStatus = "清醒"
			entry.CurrentAction = "应付眼前事务"
			entry.CurrentPressure = "时间和关系施压"
			entry.NextIndependentMove = "按证据做可逆选择"
			entry.Resources = []string{"手机（available）"}
			entry.Relationships = []string{"与林澈：熟人"}
			entry.KnowledgeBoundary = "只知道亲见和合法通信"
			entry.DecisionModel = "先确认现场证据"
		case i < 18:
			entry.AuthorityMode = simulationAuthorityHoldBaseline
			entry.Blocking = true
			entry.MissingAuthority = []string{"current_location", "knowledge_boundary"}
			entry.HoldBaselineContract = holdBaselineContractPayload(name, 1)
			entry.Description = strings.Repeat("冻结角色不应携带无用档案表面。", 120)
		default:
			entry.AuthorityMode = "rewrite_source_only"
			entry.Blocking = true
			entry.MissingAuthority = []string{"dossier"}
			entry.RewriteSourceEvidence = []string{"赵航低头回复了一条消息。"}
			entry.RewriteSourceOnlyContract = rewriteSourceOnlyContractPayload(name, 1, entry.RewriteSourceEvidence)
		}
		authority = append(authority, entry)
	}
	source := &domain.ChapterRewriteSource{
		BodyPath: "chapters/01.md", BodySHA256: strings.Repeat("a", 64), WordCount: 2800,
		BriefPath: "reviews/01_rewrite_brief.md", BriefSHA256: strings.Repeat("b", 64),
		PreserveFacts: []string{"林澈先叫停，沈知遥只检查已完成的退线。", "皮卡保持待确认。"},
	}
	markdown := "# brief\n\n## 保留事实\n\n- 林澈先叫停，沈知遥只检查已完成的退线。\n- 皮卡保持待确认。\n\n## 必须修正\n\n- 修正知识边界冲突。\n\n## 最新整篇单段门禁（2026-07-16）\n\n- external/whole_text_single_segment 必须严格低于 4%。\n\n## 验收条件\n\n- gaps 清零后才允许 finalize。\n"
	return map[string]any{
		"simulation_characters":                 characters,
		"simulation_character_authority":        authority,
		"simulation_character_authority_policy": map[string]any{"required_count": 19, "blocking_count": 9},
		"chapter_world_simulation": map[string]any{
			"status": "invalid", "simulation_id": "old-invalid", "gaps": []string{"贺骁 knowledge_boundary 冲突", "沈知遥 correction 顺序冲突"},
		},
		"working_memory": map[string]any{
			"current_chapter_outline": map[string]any{"chapter": 1, "core_event": "修正世界推演"},
			"rewrite_source": map[string]any{
				"chapter": source, "current_body": strings.Repeat("待返工旧正文。", 2500), "brief_markdown": markdown,
				"required_sources": []string{rewriteSourceToken(source), rewriteBriefToken(source)}, "preservation_policy": "保留事实。",
			},
			"rewrite_brief": map[string]any{"reason": "世界推演失效", "review_summary": "知识边界冲突", "issues": []domain.ConsistencyIssue{{Type: "canon", Severity: "error", Description: "修正角色知识边界"}}},
			"user_rules":    map[string]any{"pov": "第三人称限知", "hard": strings.Repeat("不可跨脑。", 800)},
		},
		"premise": strings.Repeat("基础世界事实。", 900),
	}
}

func heavyRestartedWorldSimulationContextFixture() (map[string]any, []string, []string, string) {
	result := heavyInvalidWorldSimulationContextFixture()
	preserveFacts := []string{
		"母子返回点两碗豆腐脑，其中一碗少糖，合计12元；真实付款先于风险发现。",
		"林澈先独立发现风险并叫停，老丁完成断电退线后沈知遥才到场复核。",
		"4280元电子票只证明县内真实经营交易；沈知遥不得推理系统存在。",
		"贺骁只问“什么货”；借车、明早到场和执行时间保持未知。",
		"电话只传来贺骁一侧扳手落入铁盘声；贺骁不知道林澈的位置。",
		"系统绑定只回应一次；旧债拒付有等待；首次结算由林澈在复核后主动查看。",
		"正文建立两条分处不同场景的完整主观因果链。",
		"已提交状态结果：林澈.resource = 一百万元青山县专项经营额度，非个人存款",
		"已提交状态结果：林澈.knowledge = 额度不能转入个人账户或偿还旧债",
		"已提交状态结果：马玉芬摊位试点.status = 最外侧灯断电，次日八点待复看",
		"已提交状态结果：马玉芬.resource = 两碗豆腐脑共12元真实营业收入",
		"已提交状态结果：林澈与沈知遥.relationship = 形成次日复看的协作约定",
		"已提交状态结果：贺骁皮卡借用请求.status = pending",
		"已提交资源结果：专项额度status=booked；不得私人偿债、炫富、县外使用。",
		"已提交资源结果：摊位设施status=booked；仅有限授权，不代表整片夜市获准扩铺。",
		"已提交资源结果：皮卡请求status=pending；不得视为车辆已经取得。",
	}
	corrections := []string{
		"保留母子12元消费、自纠、复核的严格先后顺序。",
		"沈知遥和贺骁的知识边界不得扩大。",
		"19名实名角色都按独立目标提交决定或精确 blocking contract。",
		"逐条覆盖全部 preserve facts，不得概括或改写。",
		"主角投影只能包含可观察后果和合法获得的信息。",
		"不得把待确认皮卡写成已同意或即将到位。",
	}
	working := result["working_memory"].(map[string]any)
	source := working["rewrite_source"].(map[string]any)
	chapter := source["chapter"].(*domain.ChapterRewriteSource)
	chapter.PreserveFacts = append([]string(nil), preserveFacts...)
	source["current_body"] = strings.Repeat("旧正文表面与示例对白不得锚定新世界推演。", 1800)
	source["brief_markdown"] = strings.Join([]string{
		"# rewrite brief", "", "## 当前结论", "", "- 开启新的世界模拟 epoch。", "",
		"## 保留事实", "", "- " + strings.Join(preserveFacts, "\n- "), "",
		"## 必须修正", "", "- " + strings.Join(corrections, "\n- "), "",
		"## 最新整篇单段门禁（2026-07-16）", "", "- 新哈希必须重新完成整篇单段检测。", "",
		"## 验收条件", "", "- 全角色、事实覆盖、主角投影和来源 token 全部通过。",
	}, "\n")
	working["rewrite_brief"] = map[string]any{
		"reason":         "连续两次整章结构失败，开启新因果 epoch",
		"review_summary": "旧模拟让关键时序和知识边界回流。",
		"issues": []domain.ConsistencyIssue{
			{Type: "continuity", Severity: "error", Description: "老丁必须在沈知遥到场前完成断电退线。", Evidence: strings.Repeat("旧稿示例证据。", 400), Suggestion: strings.Repeat("示例修法。", 300)},
			{Type: "knowledge", Severity: "error", Description: "沈知遥不得从票据或支付渠道推理系统。", Evidence: strings.Repeat("旧稿引文。", 350), Suggestion: strings.Repeat("替换场景。", 260)},
		},
		"required_corrections":  corrections,
		"current_conclusion":    []string{"开启新的世界模拟 epoch。"},
		"acceptance_conditions": []string{"全角色、事实覆盖、主角投影和来源 token 全部通过。"},
		"mechanical_gate": map[string]any{
			"json_path": "reviews/01_ai_gate.json", "markdown_path": "reviews/01.md", "engine": "codex-local-aigc-v4",
			"detector_dossier": strings.Repeat("概率曲线和示例只供审核，不进入世界模拟。", 650),
			"rule_violations":  []map[string]any{{"rule": "external_aigc_ratio", "severity": "error", "target": strings.Repeat("旧稿送检证据。", 180), "limit": "<4%"}},
		},
		"ai_voice_redflags": map[string]any{"prose": strings.Repeat("AI voice 示例与指标。", 500)},
	}
	working["draft_external_ai_review"] = map[string]any{
		"blocking": true, "summary": "旧稿整篇失败", "reasons": []string{"结构与人物体验同时失控"},
		"evidence": strings.Repeat("旧稿证据引文。", 400), "revision_plan": strings.Repeat("示例场景和示例台词。", 550),
	}
	working["draft_external_ai_review_policy"] = "旧审查完整包"

	world := result["chapter_world_simulation"].(map[string]any)
	world["status"] = "restartable_shell"
	delete(world, "simulation_id")
	gaps := []string{"missing time_window"}
	characters := result["simulation_characters"].([]string)
	for _, name := range characters {
		gaps = append(gaps, "missing character decision: "+name)
	}
	for _, fact := range preserveFacts {
		gaps = append(gaps, "rewrite_fact_coverage missing: "+fact)
	}
	for _, name := range characters[:8] {
		gaps = append(gaps, "rewrite-visible character must remain visible_to_pov: "+name)
	}
	gaps = append(gaps, "incomplete protagonist_projection")
	const instructionToken = "chapter_pipeline_instruction:sha256:restart-heavy"
	gaps = append(gaps, "chapter_pipeline_instruction source missing: "+instructionToken)
	world["gaps"] = gaps
	world["policy"] = "empty restart shell; rebuild all decisions and coverage"
	result["chapter_pipeline_instruction"] = map[string]any{
		"chapter": 1, "instruction": strings.Repeat("完整用户硬合同：金额、数量、知识边界、资源状态和先后顺序不得漂移。", 180),
		"sha256": "restart-heavy", "source": "meta/pipeline.json#prompt", "scope_source": "drafts/01.rerender_request.json",
		"source_token": instructionToken,
		"policy":       "全文和 token 必须保留。",
	}
	return result, preserveFacts, corrections, instructionToken
}

func heavyReadyPlanningContextFixture() (map[string]any, string, string) {
	exactFact := "线缆风险必须由林澈先发现并叫停；老丁在沈知遥到场前完成断电退线。"
	secondFact := "贺骁只接通电话，皮卡和次日运力保持未知、待确认。"
	exactDecision := "先做可撤回的夜市试点，再争取尚未确认的运力"
	briefMarkdown := strings.Join([]string{
		"# ch01 rewrite brief", "", "## 当前结论", "", "- 整章重渲染。", "",
		"## 用户本轮要求", "", "- 整篇作为单段复测，结果严格低于 4%。", "",
		"## 保留事实", "", "- " + exactFact, "- " + secondFact, "",
		"## 必须修正", "", "- 两条主观因果链必须分处不同场景并产生现实余波。", "",
		"## 最新整篇单段门禁（2026-07-16）", "", "- external/whole_text_single_segment 当前为 86%，新 SHA 必须复测。", "",
		"## 验收条件", "", "- 金额、秘密边界、选择结果和章末未知状态不得漂移。",
	}, "\n")
	authority := make([]map[string]any, 0, 19)
	characters := make([]string, 0, 19)
	decisions := make([]map[string]any, 0, 19)
	for i := 0; i < 19; i++ {
		name := fmt.Sprintf("角色%02d", i+1)
		characters = append(characters, name)
		authority = append(authority, map[string]any{
			"character": name, "authority_mode": "authoritative",
			"description": strings.Repeat("完整档案字段只用于预算回归。", 180),
		})
		decisions = append(decisions, map[string]any{
			"character": name, "decision": strings.Repeat("离屏决定已正式落盘。", 100),
		})
	}
	projection := domain.ProtagonistDecisionProjection{
		Protagonist: "林澈", ObservableEffects: []string{"孩子跨线时顿步", "林澈叫停", "老丁先完成退线"},
		HiddenPressures: []string{"贺骁尚未答复"}, AvailableOptions: []string{"继续争取皮卡", "先守住试点"},
		ChosenDecision: exactDecision, DecisionReason: "眼前证据只支持小步验证",
		PlanConstraints: []string{exactFact, secondFact}, CausalChain: []string{"饭桌压力→守密", "夜市风险→自纠→信任"},
	}
	source := &domain.ChapterRewriteSource{
		BodyPath: "chapters/01.md", BodySHA256: strings.Repeat("a", 64), WordCount: 2800,
		BriefPath: "reviews/01_rewrite_brief.md", BriefSHA256: strings.Repeat("b", 64), PreserveFacts: []string{exactFact, secondFact},
	}
	coverage := []domain.ChapterRewriteFactCoverage{
		{Fact: exactFact, SimulationEvidence: []string{strings.Repeat("林澈决定证据。", 650), strings.Repeat("老丁时序证据。", 650)}},
		{Fact: secondFact, SimulationEvidence: []string{strings.Repeat("贺骁未知状态证据。", 650)}},
	}
	working := map[string]any{
		"current_chapter_outline": map[string]any{"chapter": 1, "title": "试钱", "core_event": "完成一次可撤回验证"},
		"rewrite_source": map[string]any{
			"chapter": source, "current_body": strings.Repeat("旧正文表面不应锚定新计划。", 1400), "brief_markdown": briefMarkdown,
			"required_sources":    []string{rewriteSourceToken(source), rewriteBriefToken(source)},
			"preservation_policy": "保留事实但允许删场、并场和换序。",
		},
		"rewrite_brief": map[string]any{
			"reason": "整篇单段门禁失败", "review_summary": "表面过于模板化。",
			"issues": []domain.ConsistencyIssue{{Type: "aigc", Severity: "error", Description: "主观体验需要改变后续选择。"}},
		},
		"user_rules": map[string]any{"pov": "第三人称限知", "hard_rules": strings.Repeat("硬规则。", 1000)},
	}
	return map[string]any{
		"simulation_characters":                 characters,
		"simulation_character_authority":        authority,
		"simulation_character_authority_policy": map[string]any{"required_count": 19, "blocking_count": 0},
		"chapter_world_simulation": map[string]any{
			"status": "ready", "simulation_id": "ch001-current", "character_count": 19,
			"character_decisions": decisions, "protagonist_projection": projection,
			"rewrite_source": source, "rewrite_fact_coverage": coverage,
		},
		"working_memory": working,
		"rewrite_craft_pack": map[string]any{
			"receipt_id": "receipt-current", "source_token": "craft_recall:receipt-current",
			"binding":  map[string]any{"chapter": 1, "rewrite_body_sha256": source.BodySHA256, "rewrite_brief_sha256": source.BriefSHA256},
			"attempts": []any{map[string]any{"need": "主观因果链", "hits": []any{map[string]any{"ref": "hit-1", "summary": strings.Repeat("只迁移写法。", 450)}}}},
		},
	}, exactFact, exactDecision
}

func TestFinalizedChapterOneRealShapeProfilesFitPreferredBudgetAndKeepAuthority(t *testing.T) {
	for _, profile := range []string{"draft", "planning"} {
		t.Run(profile, func(t *testing.T) {
			result, exactFact, knowledgeFact, instructionText := finalizedChapterOneRealShapeFixture()
			working := result["working_memory"].(map[string]any)
			plan := working["chapter_plan"].(*domain.ChapterPlan)
			causalRaw, err := json.Marshal(plan.CausalSimulation)
			if err != nil {
				t.Fatal(err)
			}
			if len(causalRaw) < 60*1024 {
				t.Fatalf("fixture no longer models the 63 KiB formal causal simulation: %d", len(causalRaw))
			}

			raw, err := finalizeContextResult(result, 1, profile)
			if err != nil {
				t.Fatalf("finalized real-shape %s context overflowed: %v", profile, err)
			}
			if len(raw) > contextBudget(1, profile) {
				t.Fatalf("%s context used overflow reserve: got=%d preferred=%d", profile, len(raw), contextBudget(1, profile))
			}
			var payload map[string]any
			if err := json.Unmarshal(raw, &payload); err != nil {
				t.Fatal(err)
			}
			if _, overflow := payload["_context_budget"]; overflow {
				t.Fatalf("%s context must not use rewrite critical overflow: %#v", profile, payload["_context_budget"])
			}
			if hasContextKey(payload, "causal_simulation") || hasContextKey(payload, "chapter_contract") {
				t.Fatalf("%s context replayed formal planning dossiers", profile)
			}

			instruction := payload["chapter_pipeline_instruction"].(map[string]any)
			if instruction["instruction"] != instructionText || !strings.HasPrefix(instruction["source_token"].(string), chapterPipelineInstructionTokenPrefix) {
				t.Fatalf("%s context changed the full chapter instruction or token: %#v", profile, instruction)
			}
			keptWorking := payload["working_memory"].(map[string]any)
			packet := keptWorking["render_packet"].(map[string]any)
			packetJSON, _ := json.Marshal(packet)
			for _, want := range []string{exactFact, "不得泄露系统秘密", "receipt-current", "candidate_moves", "knowledge_boundary"} {
				if !strings.Contains(string(packetJSON), want) {
					t.Fatalf("%s render packet lost hard/RAG/POV authority %q: %s", profile, want, packetJSON)
				}
			}
			if got := len(packet["forbidden_moves"].([]any)); got != 8 {
				t.Fatalf("%s render packet lost full forbidden contract: %d", profile, got)
			}

			receipt := payload["formal_plan_receipt"].(map[string]any)
			if receipt["status"] != "finalized_source_bound" || len(receipt["canonical_content_sha256"].(string)) != 64 {
				t.Fatalf("%s formal plan receipt is not source-bound: %#v", profile, receipt)
			}
			renderReceipt := receipt["render_contract"].(map[string]any)
			if renderReceipt["authority_path"] != "working_memory.render_packet" || renderReceipt["prose_fact_count"] == nil || len(renderReceipt["prose_facts_sha256"].(string)) != 64 {
				t.Fatalf("%s render contract receipt is not canonical: %#v", profile, renderReceipt)
			}
			sources := stringSliceFromAny(receipt["context_sources"])
			for _, source := range []string{"chapter_pipeline_instruction:sha256:instruction-current", "craft_recall_receipt:receipt-current"} {
				if !slices.Contains(sources, source) {
					t.Fatalf("%s formal plan receipt lost source %q: %#v", profile, source, sources)
				}
			}

			source := keptWorking["rewrite_source"].(map[string]any)
			if source["authority_receipt"] != "formal_plan_receipt.rewrite_source" || source["preserve_facts_authority"] != "formal_plan_receipt.rewrite_source.fact_authority" {
				t.Fatalf("%s rewrite source was not folded to the canonical receipt: %#v", profile, source)
			}
			sourceReceipt := receipt["rewrite_source"].(map[string]any)
			if sourceReceipt["brief_path"] != "reviews/01_rewrite_brief.md" || sourceReceipt["preserve_fact_count"] == nil || len(sourceReceipt["preserve_facts_sha256"].(string)) != 64 {
				t.Fatalf("%s rewrite source receipt lost binding/count/digest: %#v", profile, sourceReceipt)
			}
			sourceJSON, _ := json.Marshal(source)
			if strings.Contains(string(sourceJSON), exactFact) {
				t.Fatalf("%s rewrite source repeated canonical fact text: %s", profile, sourceJSON)
			}
			if _, exists := keptWorking["rewrite_brief"]; exists {
				t.Fatalf("%s replayed mutable rewrite brief after formal plan freeze", profile)
			}

			if world, exists := payload["chapter_world_simulation"].(map[string]any); exists {
				if world["rewrite_fact_coverage_receipt"] != "formal_plan_receipt.rewrite_fact_coverage" {
					t.Fatalf("%s world coverage was not folded to the canonical receipt: %#v", profile, world)
				}
				if _, duplicated := world["protagonist_projection"]; duplicated {
					t.Fatalf("%s world simulation duplicated the packet projection: %#v", profile, world)
				}
			}
			coverageReceipt := receipt["rewrite_fact_coverage"].(map[string]any)
			if coverageReceipt["artifact"] != "meta/chapter_simulations/001.json" || coverageReceipt["fact_count"] == nil || len(coverageReceipt["facts_sha256"].(string)) != 64 {
				t.Fatalf("%s coverage receipt lost artifact/count/digest: %#v", profile, coverageReceipt)
			}
			projectionJSON, _ := json.Marshal(packet["protagonist_projection"])
			if !strings.Contains(string(projectionJSON), knowledgeFact) {
				t.Fatalf("%s render packet projection lost the knowledge bound: %s", profile, projectionJSON)
			}
			if hasContextKey(payload, "simulation_restart_policy") || hasContextKey(payload, "premise_sections") {
				t.Fatalf("%s context retained already-consumed broad planning background", profile)
			}
		})
	}
}

func TestFinalizedDraftContextCompactsRepeatedPreserveAuthoritiesUnder64KiB(t *testing.T) {
	quoteFact := "章末贺骁只问“什么货”；借车、到场时间与运力保持未知、待确认。"
	moneyFact := "4280元电子票只证明青山县真实经营交易；沈知遥不得据此推理系统存在。"
	orderFact := "母子两碗豆腐脑（一碗少糖）合计12元真实付款，必须先于林澈发现走线风险。"
	knowledgeFact := "沈知遥不知道系统存在，也不知道林澈的支付渠道与专项额度来源。"
	ledgerFact := "已提交资源结果：贺骁皮卡借用请求；status=pending；不得视为车辆已经取得。"
	canonicalFacts := []string{quoteFact, moneyFact, orderFact, knowledgeFact, ledgerFact}

	// Model a long-running rewrite in which the same immutable facts have been
	// copied into source, coverage and later quote-glyph repair rounds hundreds
	// of times. Only quote typography is equivalent; the overlapping money,
	// order, knowledge and ledger constraints must remain separate.
	var sourceFacts []string
	var coverage []domain.ChapterRewriteFactCoverage
	for i := 0; i < 80; i++ {
		for _, fact := range canonicalFacts {
			sourceFacts = append(sourceFacts, fact)
			coverage = append(coverage, domain.ChapterRewriteFactCoverage{
				Fact:               fact,
				SimulationEvidence: []string{strings.Repeat("已由正式世界推演逐字段验证。", 24)},
			})
		}
	}
	plannedFacts := []string{
		`章末贺骁只问"什么货"；借车、到场时间与运力保持未知、待确认。`,
		"章末贺骁只问「什么货」；借车、到场时间与运力保持未知、待确认。",
		moneyFact, orderFact, knowledgeFact, ledgerFact,
		"4280元电子票只证明青山县真实经营交易；沈知遥不得据此推理系统存在。",
	}
	for i := 0; i < 40; i++ {
		plannedFacts = append(plannedFacts, canonicalFacts...)
	}

	const receiptID = "receipt-budget-regression"
	plan := &domain.ChapterPlan{
		Chapter:  1,
		Title:    "先把线退回来",
		Goal:     "守住一次可撤回试点",
		Conflict: "眼前收益与安全边界同时施压",
		Hook:     "电话接通，但运力仍未确认",
		Contract: domain.ChapterContract{
			RequiredBeats: []string{
				"母子完成两碗豆腐脑合计12元真实付款",
				"林澈独立发现风险并叫停，老丁完成断电退线后沈知遥才到场",
				"林澈主动查看首次结算后再拨通贺骁",
				"电话截在贺骁问“什么货”且尚未同意借车",
			},
			ForbiddenMoves:   []string{"不得漂移4280元或12元", "不得让沈知遥推理系统", "不得倒置断电退线与沈知遥到场"},
			ContinuityChecks: []string{moneyFact, orderFact, knowledgeFact},
		},
		CausalSimulation: domain.ChapterCausalSimulation{
			WorldSimulationID:   "ch001-budget-regression",
			ProtagonistDecision: "先停扩并完成退线",
			ContextSources: []string{
				"chapter_pipeline_instruction:sha256:instruction-budget",
				"rewrite_source:chapters/01.md#sha256=body-budget",
				"craft_recall_receipt:" + receiptID,
			},
			ReviewRefinement: domain.ReviewRefinementLoop{PreserveConstraints: plannedFacts},
			SceneConstraints: []string{knowledgeFact, "贺骁不知道林澈的位置、项目与执行时间"},
			ExternalRefs: []domain.ExternalReferencePlan{
				{
					QueryOrNeed: "rewrite-methodology", SourceType: craftSourceType,
					SourceRefs:         []string{"craft_recall_receipt:" + receiptID + "#chunk=method#hash=aaa"},
					UsableDetails:      []string{"让人物选择承担转场，把手续压成可见后果。"},
					TransformationRule: "只迁移节奏与信息延迟，不迁移事实。",
					DoNotUse:           []string{"不得照抄资料。"},
				},
				{
					QueryOrNeed: "rewrite-dialogue", SourceType: craftSourceType,
					SourceRefs:         []string{"craft_recall_receipt:" + receiptID + "#chunk=dialogue#hash=bbb"},
					UsableDetails:      []string{"让拒绝、沉默和改口改变关系权力。"},
					TransformationRule: "对白只承担当下意图，不补作者说明。",
					DoNotUse:           []string{"不得轮流念规则。"},
				},
			},
			AntiAIPlan: domain.AntiAIExecutionPlan{
				RiskSignals:          []string{"句长CV过低；饭桌对白压住人物犹疑", "对白传送带让人物替作者念流程"},
				CounterMoves:         []string{"每三句插动作；让林澈先误判，再因安全后果改口", "让父亲护场改变林澈随后守密的选择"},
				SentenceRhythmPolicy: "句长CV低于0.62；饭桌保留长问短答，风险发现处自然收短",
				ObjectResponseBudget: "绑定、拒付和主动结算之间必须有不等空白。",
				DialogueFunctionPlan: "饭桌对白只制造关系压力，不轮流说明规则。",
				ReviewChecks:         []string{"检测概率低于4%；风险发现是否改变人物选择", "父亲护场是否留下关系余波"},
			},
			EndingContract: domain.EndingConsequenceContract{
				Consequence:      "请求已经提出但仍未获答复",
				NextChapterPull:  "贺骁听完真实用途后才作有限同意",
				ForbiddenEndings: []string{"不得写贺骁已同意", "不得确认明早到场"},
			},
		},
	}
	source := &domain.ChapterRewriteSource{
		BodyPath: "chapters/01.md", BodySHA256: strings.Repeat("a", 64), WordCount: 2688,
		BriefPath: "reviews/01_rewrite_brief.md", BriefSHA256: strings.Repeat("b", 64),
		CanonicalStatePath: "meta/chapter_progress.json", CanonicalStateSHA256: strings.Repeat("c", 64),
		PreserveFacts: sourceFacts,
	}
	rewriteSource := map[string]any{
		"chapter": source, "current_body": strings.Repeat("旧正文不进入 draft profile。", 900),
		"brief_markdown":      strings.Repeat("完整评审不进入 draft profile。", 500),
		"required_sources":    []string{rewriteSourceToken(source), rewriteBriefToken(source)},
		"preservation_policy": "金额、数量、知识边界、资源状态和因果顺序不可漂移。",
	}
	working := map[string]any{
		"chapter_plan":   plan,
		"rewrite_source": rewriteSource,
		"rewrite_brief": map[string]any{
			"reason": "整篇单段失败", "required_corrections": canonicalFacts,
			"whole_text_single_segment_gates": []string{"整章按单段送检"},
		},
		"current_chapter_outline": map[string]any{"chapter": 1, "title": plan.Title, "detail": strings.Repeat("已由正式计划消费。", 180)},
		"user_rules": map[string]any{
			"structured":  map[string]any{"chapter_words": map[string]any{"min": 2100, "max": 3000}},
			"preferences": strings.Repeat("定性写法偏好。", 480),
		},
	}
	result := map[string]any{
		"working_memory": working,
		"chapter_plan":   plan,
		"rewrite_source": rewriteSource,
		"chapter_world_simulation": map[string]any{
			"status": "ready", "simulation_id": "ch001-budget-regression", "character_count": 8,
			"rewrite_source": source, "rewrite_fact_coverage": coverage,
			"protagonist_projection": domain.ProtagonistDecisionProjection{
				Protagonist: "林澈", ChosenDecision: "先停扩并完成退线", DecisionReason: "眼前证据只支持可撤回试点",
				ObservableEffects: []string{"老丁先断电退线", "沈知遥后到场复核"}, PlanConstraints: []string{knowledgeFact, orderFact},
			},
		},
		"chapter_pipeline_instruction": map[string]any{
			"chapter": 1, "instruction": strings.Repeat("当前用户合同已经由正式计划逐项消费；不得绕过来源哈希。", 170),
			"sha256": "instruction-budget", "source": "meta/pipeline.json#prompt",
			"source_token": "chapter_pipeline_instruction:sha256:instruction-budget",
		},
		"episodic_memory": map[string]any{
			"resource_audit":     strings.Repeat("历史资源审计只作衔接。", 260),
			"relationship_state": strings.Repeat("历史关系状态只作衔接。", 120),
		},
	}

	raw, err := finalizeContextResult(result, 1, "draft")
	if err != nil {
		t.Fatalf("repeated preserve authorities overflowed draft context: %v", err)
	}
	if len(raw) > contextBudget(1, "draft") {
		t.Fatalf("draft context exceeded 64 KiB: got=%d budget=%d", len(raw), contextBudget(1, "draft"))
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	keptWorking := payload["working_memory"].(map[string]any)
	packet := keptWorking["render_packet"].(map[string]any)
	gotFacts := stringSliceFromAny(packet["preserve_facts"])
	gotIdentities := make([]string, 0, len(gotFacts))
	wantIdentities := make([]string, 0, len(canonicalFacts))
	for _, fact := range gotFacts {
		gotIdentities = append(gotIdentities, rewriteFactIdentity(fact))
	}
	for _, fact := range canonicalFacts {
		wantIdentities = append(wantIdentities, rewriteFactIdentity(fact))
	}
	if !slices.Equal(gotIdentities, wantIdentities) {
		t.Fatalf("frozen-plan preserve facts changed or semantically collapsed:\nwant=%#v\ngot=%#v", canonicalFacts, gotFacts)
	}
	if len(gotFacts) == 0 || gotFacts[0] != plannedFacts[0] {
		t.Fatalf("draft must preserve the finalized plan's exact quote spelling, got=%#v", gotFacts)
	}
	seen := map[string]bool{}
	for _, fact := range gotFacts {
		identity := rewriteFactIdentity(fact)
		if seen[identity] {
			t.Fatalf("quote-only duplicate survived in render packet: %q", fact)
		}
		seen[identity] = true
	}
	if got := len(stringSliceFromAny(packet["mandatory_beats"])); got != len(plan.Contract.RequiredBeats) {
		t.Fatalf("hard outcomes were sampled: got=%d want=%d", got, len(plan.Contract.RequiredBeats))
	}
	craft, _ := packet["craft_methods"].([]any)
	if len(craft) != 2 {
		t.Fatalf("complete selected craft methods were lost: %#v", packet["craft_methods"])
	}
	craftJSON, _ := json.Marshal(craft)
	for _, want := range []string{"rewrite-methodology", "rewrite-dialogue", "让人物选择承担转场", "让拒绝、沉默和改口"} {
		if !strings.Contains(string(craftJSON), want) {
			t.Fatalf("craft method %q missing: %s", want, craftJSON)
		}
	}
	timingJSON, _ := json.Marshal(packet["event_timing_safeguards"])
	for _, want := range []string{"绑定、拒付和主动结算", "饭桌对白只制造关系压力"} {
		if !strings.Contains(string(timingJSON), want) {
			t.Fatalf("story timing safeguard %q missing: %s", want, timingJSON)
		}
	}
	packetJSON, _ := json.Marshal(packet)
	for _, forbidden := range []string{"CV", "0.62", "每三句", "检测概率", "4%", "饭桌对白压住人物犹疑", "让林澈先误判", "anti_ai_execution_plan"} {
		if strings.Contains(string(packetJSON), forbidden) {
			t.Fatalf("planning/review recipe %q leaked into prose contract: %s", forbidden, packetJSON)
		}
	}
	receipt := payload["formal_plan_receipt"].(map[string]any)
	for _, name := range []string{"render_contract", "rewrite_source", "rewrite_fact_coverage"} {
		if _, ok := receipt[name].(map[string]any); !ok {
			t.Fatalf("authority receipt %s missing: %#v", name, receipt)
		}
	}
	if sourceReceipt := receipt["rewrite_source"].(map[string]any); sourceReceipt["word_count"] != float64(2688) || sourceReceipt["preserve_fact_count"] != float64(len(canonicalFacts)) {
		t.Fatalf("rewrite source receipt lost identity/count: %#v", sourceReceipt)
	}
	if coverageReceipt := receipt["rewrite_fact_coverage"].(map[string]any); coverageReceipt["fact_count"] != float64(len(canonicalFacts)) || len(coverageReceipt["facts_sha256"].(string)) != 64 {
		t.Fatalf("coverage receipt lost validation count/hash: %#v", coverageReceipt)
	}
	world := payload["chapter_world_simulation"].(map[string]any)
	if _, duplicated := world["protagonist_projection"]; duplicated || world["rewrite_fact_coverage_receipt"] != "formal_plan_receipt.rewrite_fact_coverage" {
		t.Fatalf("world simulation still duplicated packet authority: %#v", world)
	}
	for _, duplicateContainer := range []any{keptWorking["rewrite_source"], world} {
		encoded, _ := json.Marshal(duplicateContainer)
		for _, fact := range canonicalFacts {
			if strings.Contains(string(encoded), fact) {
				t.Fatalf("fact text repeated outside render packet: %q in %s", fact, encoded)
			}
		}
	}
}

func finalizedChapterOneRealShapeFixture() (map[string]any, string, string, string) {
	result, exactFact, _ := heavyReadyPlanningContextFixture()
	knowledgeFact := "贺骁只接通电话，皮卡和次日运力保持未知、待确认。"
	causal := domain.ChapterCausalSimulation{
		WorldSimulationID:   "ch001-current",
		ProtagonistDecision: "先做可撤回试点",
		ProjectPromise:      strings.Repeat("百万字项目承诺只作正式规划证据，不得在正文逐条复述。", 1000),
		ChapterFunction:     strings.Repeat("正式计划已经完成因果推演与审查闭环。", 400),
		ContextSources: []string{
			"rewrite_source:chapters/01.md#sha256=body-current",
			"rewrite_brief:reviews/01_rewrite_brief.md#sha256=brief-current",
			"chapter_pipeline_instruction:sha256:instruction-current",
			"world_simulation:ch001-current",
			"chapter_world_simulation:ch001-current",
			"craft_recall_receipt:receipt-current",
		},
		ExternalRefs: []domain.ExternalReferencePlan{
			{
				QueryOrNeed: "rewrite-methodology", SourceType: "craft_recall",
				SourceRefs:         []string{"craft_recall_receipt:receipt-current#chunk=method-1#hash=method-hash-1"},
				UsableDetails:      []string{"饭桌、走线和章末电话使用不同的段落功能与信息释放方式。"},
				TransformationRule: "把方法转换成饭桌打断、走线自纠与电话截断三个场景动作。",
				DoNotUse:           []string{"不得照搬参考文本。"},
			},
			{
				QueryOrNeed: "rewrite-dialogue", SourceType: "craft_recall",
				SourceRefs:         []string{"craft_recall_receipt:receipt-current#chunk=dialogue-1#hash=dialogue-hash-1"},
				UsableDetails:      []string{"梁广财密集追问，林建国用动作打断，沈知遥只确认已完成的自纠。"},
				TransformationRule: "用权力转移组织话轮，不让旁白替人物总结。",
				DoNotUse:           []string{"不得给角色统一口头禅。"},
			},
		},
		LiteraryRendering: &domain.LiteraryRenderingPlan{
			Focalizer: "林澈", NarrativeAccess: domain.LiteraryNarrativeAccessInternal,
			KnowledgeBoundary:     "第三人称限知；沈知遥不知道系统，贺骁的答复保持未知。",
			PerceptualBias:        "先注意现场风险，再意识到自己的小胜可能伤人。",
			SummaryOmissionPolicy: "安装流程压缩，保留选择后果。", Afterimage: "电话另一端的扳手声。",
			SourceRefs: []string{"craft_recall_receipt:receipt-current"},
		},
		ReviewRefinement: domain.ReviewRefinementLoop{
			FailureModes:        []string{strings.Repeat("整篇单段门禁失败证据仅供正式 plan 消费。", 240)},
			PreserveConstraints: []string{exactFact, knowledgeFact},
			AcceptanceChecks:    []string{"新正文 SHA 必须重新接受整篇单段检测。"},
			StopCondition:       "正式 plan 已通过，下一步只允许渲染正文。",
		},
		SceneConstraints: []string{"第三人称限知", knowledgeFact, "沈知遥不知道系统秘密"},
	}
	plan := &domain.ChapterPlan{
		Chapter: 1, Title: "刚被催着找工作，一百万到账了", Goal: "完成一次可撤回试点",
		Conflict: "面子、秘密和安全在同一选择点对撞", Hook: "电话接通，但借车与到场仍未确认",
		Contract: domain.ChapterContract{
			RequiredBeats: []string{exactFact, "首次真实交易成立", "林澈主动叫停风险", "章末拨通贺骁"},
			ForbiddenMoves: []string{
				"不得泄露系统秘密", "不得把额度写成存款", "不得让沈知遥先纠偏", "不得确认皮卡借出",
				"不得确认次日到场", "不得跨脑读取", "不得照搬 RAG 文本", "不得改写 preserve facts",
			},
			ContinuityChecks: []string{exactFact, knowledgeFact, "沈知遥不知道系统", "4280元为真实交易", "旧债测试被拒", "首次结算后查看", "系统短回应", "电话位置未知", "主观因果链分处不同场景"},
		},
		CausalSimulation: causal,
	}
	working := result["working_memory"].(map[string]any)
	working["chapter_plan"] = plan
	working["chapter_contract"] = plan.Contract
	working["causal_simulation"] = causal
	working["simulation_restart_policy"] = map[string]any{"active": true, "generation_id": "generation-current", "detail": strings.Repeat("正式计划已经消费。", 400)}
	result["chapter_plan"] = plan
	result["chapter_contract"] = plan.Contract
	result["causal_simulation"] = causal
	result["premise_sections"] = strings.Repeat("已经被正式计划消费的全书前提。", 700)
	result["reference_pack"] = map[string]any{"references": strings.Repeat("已消费的完整写作参考。", 1800)}
	instructionText := strings.Repeat("整章必须保留先后顺序、资源状态、知识边界和整篇单段检测硬约束。", 90)
	result["chapter_pipeline_instruction"] = map[string]any{
		"chapter": 1, "instruction": instructionText, "sha256": "instruction-current",
		"source": "meta/chapter_pipeline_instruction.md", "scope_source": "checkpoints.jsonl",
		"source_token": chapterPipelineInstructionTokenPrefix + "instruction-current",
		"policy":       "当前章节用户硬合同必须完整保留。",
	}
	return result, exactFact, knowledgeFact, instructionText
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

	raw, err := NewContextTool(st, References{
		LiteraryRenderingCards: `{"version":1,"cards":[{"id":"focalization-boundary","decision":"谁在感知"}]}`,
	}, "default").Execute(context.Background(), json.RawMessage(`{"chapter":1,"profile":"planning"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(raw) > contextBudget(1, "planning") {
		t.Fatalf("staged plan repair bypassed planning budget: %d", len(raw))
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
	pack, ok := payload["reference_pack"].(map[string]any)
	if !ok {
		t.Fatalf("staged plan repair must keep compact literary cards, got %#v", payload["reference_pack"])
	}
	catalog, ok := pack["literary_rendering_cards"].(map[string]any)
	if !ok {
		t.Fatalf("staged plan repair compact catalog missing, got %#v", pack)
	}
	cards, ok := catalog["cards"].([]any)
	if !ok || len(cards) != 1 || cards[0].(map[string]any)["id"] != "focalization-boundary" {
		t.Fatalf("unexpected staged plan repair cards: %#v", catalog)
	}
	if payload["_context_profile"] != "planning" || payload["_reading_guide"] == "" {
		t.Fatalf("staged plan repair must use the common finalizer: %#v", payload)
	}
	if payload["structure_source_status"] != "ready" {
		t.Fatalf("optional world simulation must not leave staged structure waiting: %#v", payload["structure_source_status"])
	}
	if next, _ := payload["next_step"].(string); !strings.Contains(next, "直接调用 plan_details") || strings.Contains(next, "simulate_chapter_world") {
		t.Fatalf("optional world simulation produced contradictory recovery guidance: %q", next)
	}
	if policy, _ := stage["policy"].(string); strings.Contains(policy, "world simulation") || strings.Contains(policy, "模拟") {
		t.Fatalf("optional world simulation incorrectly rewrote staged policy: %q", policy)
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

func TestPlanningContextHidesPlanSupersededByCurrentWorldSimulationCheckpoint(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Outline.SaveOutline([]domain.OutlineEntry{{Chapter: 1, Title: "目标章", CoreEvent: "扩到十摊"}}); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Save(&domain.Progress{
		NovelName: "test", TotalChapters: 3, CompletedChapters: []int{1}, PendingRewrites: []int{1},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.Drafts.SaveFinalChapter(1, "# 第1章 旧终稿\n\n待返工内容"); err != nil {
		t.Fatal(err)
	}
	oldPlan := domain.ChapterPlan{Chapter: 1, Title: "旧计划", Goal: "维持五摊"}
	oldPlan.CausalSimulation.ProjectPromise = strings.Repeat("这是已被新世界推演越过的旧因果计划。", 5000)
	if err := st.Drafts.SaveChapterPlan(oldPlan); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Checkpoints.AppendArtifact(domain.ChapterScope(1), "plan", "drafts/01.plan.json"); err != nil {
		t.Fatal(err)
	}
	sim := domain.ChapterWorldSimulation{
		Version: 1, SimulationID: "sim-current", Chapter: 1, TimeWindow: "当晚",
		ProtagonistProjection: domain.ProtagonistDecisionProjection{
			Protagonist: "林澈", ObservableEffects: []string{"新增五摊"}, HiddenPressures: []string{"第十一家申请"},
			AvailableOptions: []string{"维持五摊", "扩到十摊"}, ChosenDecision: "扩到十摊", DecisionReason: "真实交易成立",
			PlanConstraints: []string{"不得沿用五摊封顶"}, CausalChain: []string{"新增五摊 -> 十摊营业"},
		},
	}
	if err := st.SaveChapterWorldSimulation(sim); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Checkpoints.AppendArtifact(domain.ChapterScope(1), "chapter_world_simulation", "meta/chapter_simulations/001.json"); err != nil {
		t.Fatal(err)
	}

	raw, err := NewContextTool(st, References{}, "default").Execute(
		context.Background(), json.RawMessage(`{"chapter":1,"profile":"planning"}`),
	)
	if err != nil {
		t.Fatalf("superseded oversized plan must be hidden before budget finalization: %v", err)
	}
	if len(raw) > contextBudget(1, "planning") {
		t.Fatalf("planning context exceeds budget after stale-plan removal: %d", len(raw))
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	working := payload["working_memory"].(map[string]any)
	if _, ok := working["chapter_plan"]; ok {
		t.Fatal("world-simulation-superseded plan leaked into planning context")
	}
	if _, ok := working["causal_simulation"]; ok {
		t.Fatal("superseded causal simulation leaked into planning context")
	}
	stage, _ := working["chapter_plan_stage"].(map[string]any)
	if stage["status"] != "stale_for_rewrite" || !strings.Contains(stage["reason"].(string), "chapter_world_simulation") {
		t.Fatalf("stale plan cause is not auditable: %#v", stage)
	}
}

func TestDraftContextExplicitRerenderReusesValidatedStaleSourcePlan(t *testing.T) {
	st := newChapterSimulationTestStore(t)
	if err := st.Outline.SaveOutline([]domain.OutlineEntry{{Chapter: 1, Title: "夜市试点", CoreEvent: "完成首轮试点"}}); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Save(&domain.Progress{
		NovelName: "test", TotalChapters: 3, Phase: domain.PhaseWriting, Flow: domain.FlowRewriting,
		CompletedChapters: []int{1}, PendingRewrites: []int{1},
	}); err != nil {
		t.Fatal(err)
	}
	body := "# 第1章 夜市试点\n\n林澈和沈知遥在夜市把首轮试点做完。"
	if err := st.Drafts.SaveFinalChapter(1, body); err != nil {
		t.Fatal(err)
	}
	if err := st.Drafts.SaveRewriteBrief(1, "# 返工\n\n只改善正文朗读感，保留既定事实。"); err != nil {
		t.Fatal(err)
	}
	rewriteSource, _, _, err := loadChapterRewriteSource(st, 1)
	if err != nil || rewriteSource == nil {
		t.Fatalf("load rewrite source: source=%+v err=%v", rewriteSource, err)
	}
	decision := "继续完成首轮试点"
	sim := domain.ChapterWorldSimulation{
		Version: 1, SimulationID: "sim-force-1", Chapter: 1, TimeWindow: "当晚两小时",
		CharacterDecisions: []domain.CharacterWorldDecision{
			simulatedDecision("林澈", decision, true),
			simulatedDecision("沈知遥", "守住通道并配合林澈", true),
		},
		ProtagonistProjection: domain.ProtagonistDecisionProjection{
			Protagonist: "林澈", ObservableEffects: []string{"试点完成"}, HiddenPressures: []string{"后续申请增加"},
			AvailableOptions: []string{"继续", "停止"}, ChosenDecision: decision, DecisionReason: "结果可核验",
			PlanConstraints: []string{"只写主视角可见事实"}, CausalChain: []string{"试点完成 -> 申请增加"},
		},
		RewriteSource: rewriteSource,
	}
	if err := st.SaveChapterWorldSimulation(sim); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Checkpoints.AppendArtifact(
		domain.ChapterScope(1),
		"chapter_world_simulation",
		"meta/chapter_simulations/001.json",
	); err != nil {
		t.Fatal(err)
	}
	plan := domain.ChapterPlan{
		Chapter: 1, Title: "夜市试点", Goal: "做完首轮试点", Hook: "更多摊主来问",
		Contract: domain.ChapterContract{RequiredBeats: []string{"首轮试点产生可见结果"}},
		CausalSimulation: domain.ChapterCausalSimulation{
			WorldSimulationID: sim.SimulationID, ProtagonistDecision: decision,
			ContextSources: []string{"chapter_world_simulation:" + sim.SimulationID},
			CausalBeats:    []domain.CausalSimulationBeat{{Cause: "摊主愿意试", CharacterChoice: decision, StoryResult: "试点完成"}},
			OutcomeShift:   []string{"从准备转为真实营业"},
		},
	}
	if err := st.Drafts.SaveChapterPlan(plan); err != nil {
		t.Fatal(err)
	}
	// Rendering/review may replace only the committed prose surface after the
	// simulation and plan were finalized. The rewrite brief and preserve-fact
	// inputs stay unchanged, so this remains a legitimate render-only reuse.
	if err := st.Drafts.SaveFinalChapter(1, "# 第1章 夜市试点\n\n林澈先收好线盘，沈知遥让开通道，两人才重新点亮摊位。 "); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Checkpoints.AppendArtifact(domain.ChapterScope(1), "plan", "drafts/01.plan.json"); err != nil {
		t.Fatal(err)
	}
	if err := st.Drafts.SaveDraft(1, "# 第1章 夜市试点\n\n旧草稿"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Checkpoints.AppendArtifact(domain.ChapterScope(1), "draft", "drafts/01.draft.md"); err != nil {
		t.Fatal(err)
	}
	requestPath := filepath.Join(st.Dir(), "drafts", "01.rerender_request.json")
	if err := os.WriteFile(requestPath, []byte(`{"chapter":1,"reason":"human readability"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Checkpoints.AppendArtifact(domain.ChapterScope(1), "rerender-request", "drafts/01.rerender_request.json"); err != nil {
		t.Fatal(err)
	}
	raw, err := NewContextTool(st, References{}, "default").Execute(context.Background(), json.RawMessage(`{"chapter":1,"profile":"draft"}`))
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	working := payload["working_memory"].(map[string]any)
	if working["explicit_rerender"].(map[string]any)["status"] != "ready" || !strings.Contains(working["next_step"].(string), "draft_chapter") {
		t.Fatalf("explicit rerender directive missing: %#v", working)
	}
	if _, ok := working["render_packet"]; !ok {
		t.Fatalf("validated stale-source plan was hidden from draft profile: %#v", working)
	}
	if working["chapter_draft_stage"].(map[string]any)["status"] != "superseded_for_rerender" {
		t.Fatalf("old draft not marked superseded: %#v", working["chapter_draft_stage"])
	}
	world := payload["chapter_world_simulation"].(map[string]any)
	if world["status"] != "ready" || !strings.Contains(world["source_version_policy"].(string), "不触发重推演") {
		t.Fatalf("world simulation was not safely reused: %#v", world)
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
	probeItems, probeTrace, err := tool.ProbeRAGRecall(context.Background(), 1)
	if err != nil {
		t.Fatalf("ProbeRAGRecall: %v", err)
	}
	if len(probeItems) == 0 || probeItems[0].Key != "chunk:night-rent-craft" {
		t.Fatalf("expected pre-trim RAG probe result, got %+v", probeItems)
	}
	if probeTrace == nil || len(probeTrace.Matches) == 0 {
		t.Fatalf("expected probe trace, got %+v", probeTrace)
	}
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

type contextFactPrefilterSearcher struct {
	ranked  []rag.VectorSearchHit
	options *rag.VectorSearchOptions
}

func (s *contextFactPrefilterSearcher) Search(_ context.Context, _ []float32, limit int) ([]rag.VectorSearchHit, error) {
	return append([]rag.VectorSearchHit(nil), s.ranked[:min(limit, len(s.ranked))]...), nil
}

func (s *contextFactPrefilterSearcher) SearchWithOptions(_ context.Context, _ []float32, limit int, options rag.VectorSearchOptions) ([]rag.VectorSearchHit, error) {
	s.options = &options
	filtered := make([]rag.VectorSearchHit, 0, len(s.ranked))
	for _, hit := range s.ranked {
		if options.ExcludeDesignOnly && rag.IsDesignOnlySourceKind(hit.Point.Chunk.SourceKind) {
			continue
		}
		filtered = append(filtered, hit)
	}
	return append([]rag.VectorSearchHit(nil), filtered[:min(limit, len(filtered))]...), nil
}

func TestContextToolVectorRecallPrefiltersDesignOnlyBeforeTopK(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	ranked := make([]rag.VectorSearchHit, 0, 19)
	for i := range 18 {
		chunk := rag.NormalizeChunk(domain.RAGChunk{
			SourcePath: "deconstruction-library/writing-techniques/craft.md",
			SourceKind: rag.CraftSourceKind,
			Text:       "高相似度写作手法",
		})
		chunk.ID += string(rune('a' + i))
		ranked = append(ranked, rag.VectorSearchHit{
			Point: domain.RAGVectorPoint{ID: chunk.ID, Chunk: chunk},
			Score: 1 - float64(i)/100,
		})
	}
	fact := rag.NormalizeChunk(domain.RAGChunk{
		SourcePath: "meta/chapter_facts.md",
		SourceKind: "chapter_summary_facts",
		Text:       "青山县第一批摊主已经完成签约。",
	})
	ranked = append(ranked, rag.VectorSearchHit{
		Point: domain.RAGVectorPoint{ID: fact.ID, Chunk: fact},
		Score: 0.7,
	})
	searcher := &contextFactPrefilterSearcher{ranked: ranked}
	tool := NewContextTool(s, References{}, "default").
		WithRAGEmbedder(contextTestEmbedder{}).
		WithRAGVectorSearcher(searcher)
	items, trace := tool.selectRAGRecallFresh(context.Background(), contextBuildState{
		chapter: 1,
		currentEntry: &domain.OutlineEntry{
			Chapter: 1, Title: "青山县签约", CoreEvent: "核对第一批摊主签约",
		},
	})
	if searcher.options == nil || !searcher.options.ExcludeDesignOnly {
		t.Fatal("novel_context did not request pre-truncation fact filtering")
	}
	if len(items) != 1 || items[0].Key != fact.ID {
		t.Fatalf("rank-19 fact should be recalled after design-only prefilter: %+v", items)
	}
	if trace == nil || trace.Strategy != "qdrant_vector_engine_v2" {
		t.Fatalf("unexpected retrieval trace: %+v", trace)
	}
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
	qdrantChunk := rag.NormalizeChunk(domain.RAGChunk{
		ID:         "chunk:qdrant-night-rent",
		SourcePath: "meta/rag/qdrant.md",
		SourceKind: "note",
		Facet:      "plot",
		Context:    "夜租商铺 | 资产链",
		Summary:    "租约、账单、资产收益要连续推进。",
		Text:       "语义召回命中夜租商铺的资产链。",
	})
	if err := s.RAG.SaveIndexState(domain.RAGIndexState{
		Config: domain.RAGIndexConfig{Collection: "local_keyword"},
		Chunks: []domain.RAGChunk{
			{
				ID:         "chunk:local-exact-night-rent",
				SourcePath: "meta/local-index.md",
				SourceKind: "note",
				Facet:      "plot",
				Context:    "夜租商铺 | 本地文件",
				Summary:    "本地 index_state 里也有夜租商铺关键词。",
				Text:       "夜租商铺、账单、试营业、第一份租约全部精确命中，但有 Qdrant 时不能混入本地文件召回。",
			},
			qdrantChunk,
		},
	}); err != nil {
		t.Fatalf("SaveIndexState: %v", err)
	}
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
	if err := s.Progress.SetPendingRewrites([]int{2}, "机械审核未通过：aigc_ratio=codex-local-aigc-v4"); err != nil {
		t.Fatalf("SetPendingRewrites: %v", err)
	}
	report := aigc.Report{
		Engine:             "codex-local-aigc-v4",
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
		{Rule: "aigc_ratio", Target: "codex-local-aigc-v4", Limit: "<4%", Actual: 72.5, Severity: rules.SeverityError},
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
