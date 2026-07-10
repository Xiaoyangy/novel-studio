package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/rules"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func TestParseRewriteFlagsBriefOnly(t *testing.T) {
	flags, extra, err := parseRewriteFlags([]string{"--from", "1", "--to", "1", "--brief-only"})
	if err != nil {
		t.Fatalf("parseRewriteFlags: %v", err)
	}
	if len(extra) != 0 {
		t.Fatalf("unexpected extra args: %v", extra)
	}
	if !flags.BriefOnly || flags.Start != 1 || flags.End != 1 {
		t.Fatalf("unexpected flags: %+v", flags)
	}
}

func TestMechanicalViolationBriefLabelFormatsNumericLimits(t *testing.T) {
	label := mechanicalViolationBriefLabel(rules.Violation{
		Rule:   "micro_action_overuse",
		Actual: 4,
		Limit:  3,
	})
	if !strings.Contains(label, "actual=4") || !strings.Contains(label, "limit=3") {
		t.Fatalf("label should include formatted numeric actual/limit, got %q", label)
	}
	if strings.Contains(label, "%!") {
		t.Fatalf("label should not contain fmt mismatch marker, got %q", label)
	}
}

func TestBuildRevisionPlanAggregatesRedFlagsAndSuggestions(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "reviews", "01.md"), `# ch01 评审

## 是否需要改写：是
## 主要问题（按严重度排序）
1. 第三段清单灌水，物件没有剧情功能。

## 改写建议（如需要）
- 只保留能入账的三样物件，把其余改成来源待核。
`)
	mustWriteFile(t, filepath.Join(dir, "reviews", "01.json"), `{
  "chapter": 1,
  "scope": "chapter",
  "verdict": "rewrite",
  "issues": [
    {"type": "aesthetic", "severity": "error", "description": "清单灌水", "evidence": "长串物件", "suggestion": "改成交易动作和可见事实。"}
  ],
  "dimensions": [
    {"dimension": "aesthetic", "score": 50, "verdict": "fail", "comment": "堆词破坏读感"}
  ],
  "summary": "必须重写"
}`)
	mustWriteFile(t, filepath.Join(dir, "reviews", "01_ai_gate.json"), `{
  "chapter": 1,
  "rule_violations": [
    {"rule": "aigc_ratio", "severity": "warning", "actual": 7.5, "limit": "5%"}
  ]
}`)

	text := "江烬没接话。桌脚旁还散着竹柄雨伞、裂口搪瓷杯、旧台历夹、粉笔头、桦皮袖扣、蓼蓝布头、荞麦壳、陶埙裂片、绢纱穗、菖蒲根、贝母钮和紫铜铃舌。"
	plan := buildRevisionPlan(dir, 1, text, "")
	if !plan.HasRed {
		t.Fatalf("expected red plan, got %+v", plan)
	}
	for _, want := range []string{"catalog_stuffing", "结构化评审 verdict=rewrite", "机械门禁阻断 warning: aigc_ratio", "AI率目标：≤5%", "禁止注水", "改成交易动作和可见事实"} {
		if !strings.Contains(plan.Brief, want) {
			t.Fatalf("brief missing %q:\n%s", want, plan.Brief)
		}
	}
}

func TestBuildRevisionPlanTreatsAIVoiceAndHighRiskDimensionsAsBlocking(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "reviews", "01_ai_voice_redflags.json"), `{
  "chapter": 1,
  "label": "⚠️ 需打磨",
  "red_flags": [
    {"rule": "supporting_dialogue_ratio", "severity": "warning", "evidence": "对话占比偏低"}
  ]
}`)
	mustWriteFile(t, filepath.Join(dir, "reviews", "01_ai_gate.json"), `{
  "chapter": 1,
  "aigc_report": {
    "engine": "codex-local-aigc-v3",
    "dimensions": {
      "structure_fingerprint": {
        "name": "结构指纹",
        "score": 42,
        "signals": [{"name":"paragraph_start_repeat","evidence":"段首重复偏多"}]
      }
    }
  },
  "rule_violations": [
    {"rule": "isolated_sentence_overuse", "severity": "warning", "actual": 35, "limit": "4"}
  ]
}`)

	plan := buildRevisionPlan(dir, 1, "江烬停住。门牌亮了一下。", "")
	if !plan.HasRed {
		t.Fatalf("expected blocking plan, got %+v", plan)
	}
	for _, want := range []string{"AI味阻断 supporting_dialogue_ratio", "机械门禁阻断 warning: isolated_sentence_overuse", "AI高风险维度阻断"} {
		if !strings.Contains(plan.Brief, want) {
			t.Fatalf("brief missing %q:\n%s", want, plan.Brief)
		}
	}
}

func TestBuildRevisionPlanSeparatesChapterWordsFromAIVoiceRewrite(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "reviews", "01_ai_gate.json"), `{
  "chapter": 1,
  "rule_violations": [
    {"rule": "chapter_words", "severity": "error", "actual": 4114, "limit": "2800-3400"}
  ]
}`)

	plan := buildRevisionPlan(dir, 1, "江烬把欠费单压住。", "")
	if !plan.HasRed {
		t.Fatalf("expected chapter_words error to block by contract, got %+v", plan)
	}
	for _, want := range []string{"篇幅合同 error: chapter_words", "篇幅超标只做局部压缩", "不要整章重写"} {
		if !strings.Contains(plan.Brief, want) {
			t.Fatalf("brief missing %q:\n%s", want, plan.Brief)
		}
	}
	if strings.Contains(plan.Brief, "AI味阻断 chapter_words") {
		t.Fatalf("chapter_words should not be described as AI voice blocking:\n%s", plan.Brief)
	}
}

func TestBuildRevisionPlanKeepsYellowNonBlocking(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "reviews", "02.json"), `{
  "chapter": 2,
  "scope": "chapter",
  "verdict": "polish",
  "issues": [
    {"type": "hook", "severity": "warning", "description": "钩子偏弱", "evidence": "章末收得平", "suggestion": "若不破坏原情绪，可补一个动作余波。"}
  ],
  "summary": "可选打磨"
}`)
	plan := buildRevisionPlan(dir, 2, "江烬把账单压住。周行舟问：“还要核吗？”他说：“先不用。”", "")
	if plan.HasRed {
		t.Fatalf("yellow-only plan should not block rewrite stage: %+v", plan)
	}
	if !plan.HasYellow {
		t.Fatalf("expected yellow suggestion, got %+v", plan)
	}
	if !strings.Contains(plan.Brief, "黄旗只在能提升人物") {
		t.Fatalf("expected quality-first yellow guidance:\n%s", plan.Brief)
	}
}

func TestBuildRevisionPlanIgnoresAcceptedNonActionableWarnings(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "reviews", "01.json"), `{
  "chapter": 1,
  "scope": "chapter",
  "verdict": "accept",
  "issues": [
    {"type": "aesthetic", "severity": "warning", "description": "无严重问题。唯一微小痕迹未达到AI腔阈值，且与整体笔调相容，无需修改。"},
    {"type": "aesthetic", "severity": "warning", "description": "无其他问题。"},
    {"type": "ai_voice_detection", "severity": "warning", "description": "后续章节若高频出现排比式内心独白会触发AI腔模式匹配，建议在下一章故意打断一至两处。"},
    {"type": "aesthetic", "severity": "warning", "description": "定位错位导致评分体系失真：当前是女性职场悬疑长篇，不应按短篇爽文扣分。"}
  ],
  "dimensions": [
    {"dimension": "aesthetic", "score": 100, "verdict": "pass", "comment": "通过"}
  ],
  "summary": "通过"
}`)
	plan := buildRevisionPlan(dir, 1, "", "")
	if plan.HasRed || plan.HasYellow {
		t.Fatalf("accepted non-actionable warnings should not trigger rewrite flags: %+v\n%s", plan, plan.Brief)
	}
	if strings.Contains(plan.Brief, "结构化 issue warning") {
		t.Fatalf("brief should not surface non-actionable accepted warnings:\n%s", plan.Brief)
	}
}

func TestBuildRevisionPlanKeepsAcceptedWarningsAsObservations(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "reviews", "01.json"), `{
  "chapter": 1,
  "scope": "chapter",
  "verdict": "accept",
  "issues": [
    {"type": "hook", "severity": "warning", "description": "章末钩子可更锋利，但当前压力闭环成立。"}
  ],
  "dimensions": [
    {"dimension": "hook", "score": 70, "verdict": "warning", "comment": "倒计时是压力，不是新事件。"}
  ],
  "summary": "通过，可进入下一章"
}`)
	plan := buildRevisionPlan(dir, 1, "", "")
	if plan.HasRed || plan.HasYellow {
		t.Fatalf("accepted warnings should not set rewrite flags: %+v\n%s", plan, plan.Brief)
	}
	for _, want := range []string{"Editor accept 观察: 章末钩子可更锋利", "Editor accept 观察 hook(70)"} {
		if !strings.Contains(plan.Brief, want) {
			t.Fatalf("expected accepted observation %q in brief:\n%s", want, plan.Brief)
		}
	}
	if strings.Contains(plan.Brief, "结构化 issue warning") || strings.Contains(plan.Brief, "八维警告") {
		t.Fatalf("accepted observations should not be labeled as yellow flags:\n%s", plan.Brief)
	}
}

func TestBuildRevisionPlanDowngradesAcceptedWarningOnlyGate(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "reviews", "01.md"), `# 第001章 统一审核

## 是否需要改写：是
## 一句话诊断：AI味/节奏机械门禁未通过。
`)
	mustWriteFile(t, filepath.Join(dir, "reviews", "01.json"), `{
  "chapter": 1,
  "scope": "chapter",
  "verdict": "accept",
  "contract_status": "met",
  "issues": [
    {"type": "aesthetic", "severity": "warning", "description": "配角对话可再加强"}
  ],
  "dimensions": [
    {"dimension": "hook", "score": 100, "verdict": "pass", "comment": "钩子成立"},
    {"dimension": "ai_voice_detection", "score": 80, "verdict": "pass", "comment": "仅余 warning"}
  ],
  "summary": "编辑通过，仅建议打磨"
}`)
	mustWriteFile(t, filepath.Join(dir, "reviews", "01_ai_gate.json"), `{
  "chapter": 1,
  "aigc_report": {"aigc_percent": 4.8, "blended_aigc_percent": 4.8},
  "rule_violations": [
    {"rule": "fatigue_words", "severity": "warning", "actual": 2, "limit": 1}
  ]
}`)
	mustWriteFile(t, filepath.Join(dir, "reviews", "01_ai_voice_redflags.json"), `{
  "chapter": 1,
  "red_flags": [
    {"rule": "supporting_dialogue_ratio", "severity": "warning", "actual": 0.17, "limit": 0.25}
  ]
}`)

	plan := buildRevisionPlan(dir, 1, "江烬把黑卡压在欠费单旁。", "")
	if plan.HasRed {
		t.Fatalf("accepted warning-only gate should not keep forcing rewrite: %+v\n%s", plan, plan.Brief)
	}
	if !plan.HasYellow || !strings.Contains(plan.Brief, "已降级黄旗") {
		t.Fatalf("expected downgraded yellow brief, got %+v\n%s", plan, plan.Brief)
	}
}

func TestPreserveAnchorsKeepsRuleRhyme(t *testing.T) {
	original := `# 第1章

孩子像背错了儿歌：“门开门开，名字来；名字来了，账也来。妈妈不开，小宝不开；1701开过，1702不开，1703半开，1704快开。门认名，名认账；账认门，门认人。不开，不报；不开，不认；不开，不替。这屋不开，那屋不开，叔叔不开，1704不开。”

玻璃杯碎了。许曼哭腔很低：“别念了。”`

	anchors := preserveAnchors(original)
	if len(anchors) == 0 {
		t.Fatal("expected rule rhyme anchor")
	}
	if !strings.Contains(anchors[0], "门开门开") || !strings.Contains(anchors[0], "1704快开") {
		t.Fatalf("unexpected anchors: %#v", anchors)
	}
}

func TestRewritePatchBoundsUseRuntimeWordCount(t *testing.T) {
	original := strings.Repeat("江\n", 1000)
	bounds := rewritePatchBoundsFor("", original)
	if bounds.Original != len([]rune(original)) {
		t.Fatalf("rewrite bounds should use runtime rune word count, got %+v want %d", bounds, len([]rune(original)))
	}
}

func TestRewritePatchBoundsClampToProjectChapterWords(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "meta", "user_rules.json"), `{
  "version": 1,
  "structured": {
    "chapter_words": {"min": 2800, "max": 3400}
  }
}`)
	original := strings.Repeat("江", 3650)
	bounds := rewritePatchBoundsFor(dir, original)
	if bounds.Min != 2800 || bounds.Max != 3400 {
		t.Fatalf("rewrite bounds should clamp to project chapter words, got %+v", bounds)
	}
	if strings.Contains(bounds.Source, "仅作参考") {
		t.Fatalf("chapter_words must be a hard boundary, got source %q", bounds.Source)
	}
	if err := validatePatchRewriteWithBounds(original, strings.Repeat("江", 3481), bounds); err == nil {
		t.Fatal("expected rewrite over project max to be rejected before writeback")
	}
	if err := validatePatchRewriteWithBounds(original, strings.Repeat("江", 3350), bounds); err != nil {
		t.Fatalf("expected rewrite within project bounds to pass, got %v", err)
	}
}

func TestValidateRewriteChapterTitleUsesOutlineTitle(t *testing.T) {
	if err := validateRewriteChapterTitle(1, "失业饭桌", "# 第一章 失业饭桌\n\n正文。"); err != nil {
		t.Fatalf("matching rewrite title rejected: %v", err)
	}
	if err := validateRewriteChapterTitle(1, "失业饭桌", "# 第一章 回乡第一天\n\n正文。"); err == nil || !strings.Contains(err.Error(), "重写标题与大纲不一致") {
		t.Fatalf("rewrite title drift should be blocked, got %v", err)
	}
	if err := validateRewriteChapterTitle(1, "失业饭桌", "林澈推门进屋。\n\n正文。"); err == nil {
		t.Fatal("rewrite without an explicit matching heading should be blocked")
	}
}

func TestValidateRewritePreflightBlocksSecondAlgorithmDeprecatedEngine(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Outline.SavePremise("《她的第二算法》女性职场成长文，主角许闻溪。"); err != nil {
		t.Fatalf("SavePremise: %v", err)
	}
	err := validateRewritePreflight(st, 1, "许闻溪看见日志窗口还开着，工作群里跳出会后纪要。")
	if err == nil {
		t.Fatal("expected deprecated story engine preflight failure")
	}
	got := err.Error()
	for _, want := range []string{"deprecated_story_engine", "日志", "纪要"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected %q in error, got %s", want, got)
		}
	}
}

func TestValidateRewritePreflightAllowsNonBlockingMechanicalWarnings(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	text := "电脑屏幕还亮着，客户表停在最后一行，备注栏里下午没删的批注还在。"
	if err := validateRewritePreflight(st, 1, text); err != nil {
		t.Fatalf("non-blocking mechanical warning should not fail preflight: %v", err)
	}
}

func TestValidateRewritePreflightBlocksAIVoiceWarningsWhenPolishing(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	text := strings.Repeat("许闻溪把确认单压回文件夹，侧台的声音一层一层涌上来。", 80)
	err := validateRewritePreflight(st, 1, text, true)
	if err == nil {
		t.Fatal("expected AI voice warning to fail polish preflight")
	}
	if !strings.Contains(err.Error(), "ai_voice:supporting_dialogue_ratio") {
		t.Fatalf("expected supporting dialogue preflight failure, got %v", err)
	}
}

func TestNormalizeRewriteParagraphingForGateMergesNarrativeIsolatedParagraphs(t *testing.T) {
	text := `第一章 讲稿第一句

侧台的冷气贴着许闻溪的手背往上爬。

台上的溪流助手换了一种漂亮腔调。

导播台有人催主讲准备。

许闻溪抬头看见大屏已经换页。

夏岚踩着倒数走过来。

那张确认单的签名线空着。

梁渡把折过的便签推到讲稿边。

程棠在记录位上把手机扣到桌面。

倒计时归零，黑帘外的光漏进来。`

	normalized, changed := normalizeRewriteParagraphingForGate(text)
	if !changed {
		t.Fatal("expected isolated narrative paragraphs to be merged")
	}
	for _, v := range rules.Lint(normalized) {
		if v.Rule == "isolated_sentence_overuse" {
			t.Fatalf("normalized text should pass isolated_sentence_overuse gate: %+v\n%s", v, normalized)
		}
	}
}

func TestRewriteRetryTargetLeavesUpperMargin(t *testing.T) {
	target := rewriteRetryTargetMax(rewritePatchBounds{Min: 2100, Max: 3000})
	if target >= 3000 || target < 2700 {
		t.Fatalf("retry target should leave useful upper margin, got %d", target)
	}
}

func TestRewriteLengthInstructionMarksCompressionMode(t *testing.T) {
	instruction := rewriteLengthInstruction(rewritePatchBounds{
		Original:   3178,
		Min:        2300,
		Max:        3655,
		ProjectMin: 2100,
		ProjectMax: 3000,
		Source:     "test",
	}, "")
	for _, want := range []string{"不少于 2300", "净增不超过 15%", "更严格口径"} {
		if !strings.Contains(instruction, want) {
			t.Fatalf("instruction missing %q:\n%s", want, instruction)
		}
	}
}

func TestProjectDesignContextUsesCurrentProjectStateAssets(t *testing.T) {
	dir := t.TempDir()
	for _, item := range []struct {
		rel  string
		body string
	}{
		{"world_rules.md", "世界规则：夜租只认门牌、姓名和收据。"},
		{"timeline.md", "时间线：00:00 冥雾压下。"},
		{"layered_outline.md", "动态分卷大纲：第一卷夜租初临。"},
		{"foreshadow_ledger.md", "伏笔账本：江禾小票。"},
		{"characters.md", "角色档案：江烬，金融风控。"},
		{"角色.md", "角色设计：江烬冷静但会被江禾线牵动。"},
		{"relationship_state.md", "关系状态：江烬与周行舟互信建立中。"},
		{"写作手法_历史反馈沉淀.md", "写作手法：普通叙述少用标的。"},
		{"大纲与写作流程_工程版.md", "大纲与写作流程：每章花钱留下资产或后果。"},
		{"meta/chapter_progress.md", "章节推进：第一章夜租入局。"},
		{"meta/first_100_projection.md", "前100章动态推演台账：红伞夜班。"},
		{"meta/resource_ledger.md", "资源账本：1704七分钟宽限权。"},
		{"meta/writing_assets.md", "写法资产库：童谣承载规则。"},
		{"meta/project_progress.md", "项目进度：便利店线推进。"},
		{"meta/character_continuity.md", "人物连续性：江禾线压迫。"},
		{"meta/compass.json", `{"ending_direction":"买下鬼城"}`},
		{"meta/style_rules.json", `{"taboos":["少用标的"]}`},
		{"summaries/01.json", `{"chapter":1,"summary":"首章欠费单"}`},
		{"summaries/02.json", `{"chapter":2,"summary":"七分钟收据"}`},
		{"summaries/arc-v01a01.json", `{"summary":"第一弧"}`},
		{"meta/sampling/27.json", `{"chapter":27}`},
		{"meta/sampling/28.json", `{"chapter":28}`},
		{"meta/snapshots/v01a01.json", `{"volume":1,"arc":1}`},
		{"meta/snapshots/v01a02.json", `{"volume":1,"arc":2}`},
		{"历史人物版_前10章生产总纲.md", "不应作为 rewrite 主上下文"},
		{"前7章发布后_全书过程梳理与8-10重生成任务单.md", "不应作为 rewrite 主上下文"},
	} {
		mustWriteFile(t, filepath.Join(dir, filepath.FromSlash(item.rel)), item.body)
	}

	ctx := projectDesignContext(dir, 1)
	for _, want := range []string{
		"世界规则", "时间线", "动态分卷大纲", "伏笔账本", "角色档案", "角色设计",
		"写作手法：普通叙述少用标的", "前100章动态推演台账", "资源账本",
		"写法资产库", "summaries/01.json", "summaries/02.json", "meta/sampling/28.json", "meta/snapshots/v01a02.json",
	} {
		if !strings.Contains(ctx, want) {
			t.Fatalf("project design context missing %q:\n%s", want, ctx)
		}
	}
	for _, forbidden := range []string{"历史人物版_前10章生产总纲", "前7章发布后", "不应作为 rewrite 主上下文"} {
		if strings.Contains(ctx, forbidden) {
			t.Fatalf("project design context should not include %q:\n%s", forbidden, ctx)
		}
	}
}

func TestProjectDesignContextSurvivesPromptBudgetWithDynamicAssets(t *testing.T) {
	dir := t.TempDir()
	long := func(label string, n int) string {
		return label + "\n" + strings.Repeat(label+" 约束必须进入 rewrite 上下文。\n", n)
	}
	for _, item := range []struct {
		rel  string
		body string
	}{
		{"写作手法_历史反馈沉淀.md", long("写作手法", 220)},
		{"大纲与写作流程_工程版.md", long("大纲与写作流程", 90)},
		{"world_rules.md", long("世界规则", 180)},
		{"timeline.md", long("时间线", 260)},
		{"layered_outline.md", long("动态分卷大纲", 360)},
		{"foreshadow_ledger.md", long("伏笔账本", 140)},
		{"characters.md", long("角色档案", 180)},
		{"角色.md", long("角色设计", 180)},
		{"relationship_state.md", long("人物关系状态", 100)},
		{"meta/character_continuity.md", long("人物连续性", 120)},
		{"summaries/01.json", `{"chapter":1,"summary":"首章欠费单"}`},
		{"summaries/02.json", `{"chapter":2,"summary":"七分钟收据"}`},
		{"summaries/arc-v01a01.json", `{"summary":"第一弧"}`},
		{"meta/sampling/28.json", `{"chapter":28,"note":"采样记录必须保留"}`},
		{"meta/snapshots/v01a02.json", `{"volume":1,"arc":2,"note":"快照必须保留"}`},
		{"meta/chapter_progress.md", long("章节推进台账", 260)},
		{"meta/first_100_projection.md", long("前100章动态推演台账", 220)},
		{"meta/resource_ledger.md", long("资源账本", 180)},
		{"meta/writing_assets.md", long("写法资产库", 180)},
	} {
		mustWriteFile(t, filepath.Join(dir, filepath.FromSlash(item.rel)), item.body)
	}

	ctx := truncateForContext(projectDesignContext(dir, 1), rewriteDesignContextLimit)
	for _, want := range []string{
		"写作手法", "大纲与写作流程", "世界规则", "时间线", "动态分卷大纲",
		"summaries/01.json", "meta/sampling/28.json", "meta/snapshots/v01a02.json",
		"章节推进台账", "前100章动态推演台账", "资源账本", "写法资产库",
	} {
		if !strings.Contains(ctx, want) {
			t.Fatalf("budgeted design context missing %q", want)
		}
	}
}
