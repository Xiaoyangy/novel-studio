package domain

import (
	"strings"
	"testing"
)

func TestPhysicsAxiomsIsEmpty(t *testing.T) {
	if !(PhysicsAxioms{Era: "古代"}).IsEmpty() {
		t.Fatal("只有 Era 的公理集应视为 empty（无可检内容）")
	}
	p := PhysicsAxioms{DistanceSpeed: map[string]SpeedRule{"驿马": {Unit: "里/天", Speed: 80}}}
	if p.IsEmpty() {
		t.Fatal("有速度表的公理集不应为 empty")
	}
}

func TestInfoGraphBuildAndValidate(t *testing.T) {
	ledgers := map[string]CharacterKnowledgeLedger{
		"林昭": {KnownFacts: []string{"A"}, FalseBeliefs: []string{"B 是真相"}, ForbiddenKnowledge: []string{"C"}},
		"反派": {KnownFacts: []string{"B"}},
	}
	g := BuildInfoGraphFromLedgers(12, ledgers)
	if g.Chapter != 12 || len(g.Nodes) != 2 {
		t.Fatalf("派生结果异常: %+v", g)
	}
	if g.Nodes[0].ID != "反派" || g.Nodes[1].ID != "林昭" {
		t.Fatalf("节点应按名字排序保证稳定输出: %+v", g.Nodes)
	}
	if g.Nodes[1].Believes[0] != "B 是真相" || g.Nodes[1].MustNotKnowYet[0] != "C" {
		t.Fatal("false_beliefs/forbidden_knowledge 映射错误")
	}
	g.Nodes = append(g.Nodes, InfoNode{ID: "reader", Type: "reader"})
	g.Edges = []InfoEdge{{From: "林昭", To: "反派", Trust: 0.2, Hiding: true}}
	if err := g.Validate(); err != nil {
		t.Fatalf("合法图不应报错: %v", err)
	}
	g.Edges = append(g.Edges, InfoEdge{From: "不存在", To: "林昭"})
	if err := g.Validate(); err == nil {
		t.Fatal("悬空 edge 引用应报错")
	}
}

func TestSocialMoodValidate(t *testing.T) {
	ok := SocialMood{Mood: "民怨沸腾", Intensity: 0.8, Rumors: []Rumor{{Text: "北境要打仗了", Credibility: 0.4, SpreadRate: 0.9}}}
	if err := ok.Validate(); err != nil {
		t.Fatalf("合法情绪不应报错: %v", err)
	}
	if err := (SocialMood{Intensity: 0.5}).Validate(); err == nil {
		t.Fatal("空 mood 应报错")
	}
	bad := SocialMood{Mood: "x", Intensity: 0.5, Rumors: []Rumor{{Text: "", Credibility: 0.5}}}
	if err := bad.Validate(); err == nil {
		t.Fatal("空谣言文本应报错")
	}
}

func TestRitualCalendarAndCrowdLifeValidate(t *testing.T) {
	cal := RitualCalendar{Annual: []RitualEvent{{Name: "上元灯会", Date: "正月十五", Type: "festive"}}, Lifecycle: []LifecycleEvent{{Name: "及笄", Age: 15}}}
	if err := cal.Validate(); err != nil {
		t.Fatalf("合法日历不应报错: %v", err)
	}
	if err := (RitualCalendar{Annual: []RitualEvent{{Name: "缺日期"}}}).Validate(); err == nil {
		t.Fatal("缺 date 应报错")
	}
	if err := (CrowdLifeEcosystem{NPCs: []NPCSchedule{{NPCID: "客栈老板"}}}).Validate(); err != nil {
		t.Fatal("合法 NPC 生态不应报错")
	}
	if err := (CrowdLifeEcosystem{NPCs: []NPCSchedule{{}}}).Validate(); err == nil {
		t.Fatal("缺 npc_id 应报错")
	}
}

func TestSceneInventoryFunctionRatio(t *testing.T) {
	inv := SceneInventory{Chapter: 3, Objects: []PropFunction{
		{Name: "青铜匕首", Function: "伏笔"},
		{Name: "窗边花瓶", IsDecorative: true},
	}}
	if err := inv.Validate(); err != nil {
		t.Fatalf("合法清单不应报错: %v", err)
	}
	if got := inv.ComputeFunctionRatio(); got != 0.5 {
		t.Fatalf("功能占比期望 0.5，实际 %v", got)
	}
	if (SceneInventory{}).ComputeFunctionRatio() != 0 {
		t.Fatal("空清单占比应为 0")
	}
}

func TestEcologicalMapValidateNested(t *testing.T) {
	m := EcologicalMap{Ecosystems: []Ecosystem{{ID: "plains", Name: "青州平原", SubEcosystems: []Ecosystem{{ID: "marsh", Name: "沼地"}}}}}
	if err := m.Validate(); err != nil {
		t.Fatalf("合法生态图不应报错: %v", err)
	}
	m.Ecosystems[0].SubEcosystems[0].Name = ""
	if err := m.Validate(); err == nil {
		t.Fatal("嵌套子生态缺 name 应报错")
	}
}

func TestCosmologyValidate(t *testing.T) {
	c := Cosmology{Axioms: []CosmologyAxiom{{ID: "a1", Name: "灵气衰减", Rule: "灵气随海拔指数衰减", Category: "physics"}}}
	if err := c.Validate(); err != nil {
		t.Fatalf("合法宇宙观不应报错: %v", err)
	}
	c.Axioms[0].Category = "vibes"
	if err := c.Validate(); err == nil {
		t.Fatal("非法 category 应报错")
	}
}

func TestCulturalFootnotesValidate(t *testing.T) {
	c := CulturalFootnotes{Footnotes: []CulturalFootnote{{Term: "上元灯会", CulturalLoad: "元宵节，全民放灯"}}}
	if err := c.Validate(); err != nil {
		t.Fatalf("合法脚注不应报错: %v", err)
	}
	c.Footnotes[0].CulturalLoad = ""
	if err := c.Validate(); err == nil {
		t.Fatal("缺 cultural_load 应报错")
	}
}

func TestSceneBlockValidateAndSum(t *testing.T) {
	b := SceneBlock{BlockID: "ch23-b1", Chapter: 23, Sequence: 1, Goal: "揭示密信", WordTarget: 1200}
	if err := b.Validate(); err != nil {
		t.Fatalf("合法块不应报错: %v", err)
	}
	if err := (SceneBlock{BlockID: "23-1", Goal: "x"}).Validate(); err == nil {
		t.Fatal("ID 格式错误应报错")
	}
	list := SceneBlockList{Chapter: 23, Blocks: []SceneBlock{b, {BlockID: "ch23-b2", Goal: "y", WordTarget: 1800}}}
	if list.SumWordTargets() != 3000 {
		t.Fatalf("字数目标求和错误: %d", list.SumWordTargets())
	}
}

func TestPacingPresetsAndLint(t *testing.T) {
	for _, name := range PacingPresetNames() {
		p, ok := PacingPreset(name)
		if !ok {
			t.Fatalf("预设 %s 缺失", name)
		}
		if err := p.Validate(); err != nil {
			t.Fatalf("预设 %s 不合法: %v", name, err)
		}
	}
	if _, ok := PacingPreset("unknown"); ok {
		t.Fatal("未知预设不应命中")
	}

	p, _ := PacingPreset("qidian_xuanhuan")
	// 字数越界 + 缺钩子 + 大爽点超期同时触发
	history := []string{"mystery", "desire", "emotion", ""}
	issues := p.LintPacing(4, 5000, "", history)
	if len(issues) != 3 {
		t.Fatalf("期望 3 条违规（字数/钩子/大爽点），实际 %d: %v", len(issues), issues)
	}
	// 合规章零违规
	issues = p.LintPacing(4, 2500, "crisis", []string{"mystery", "desire", "crisis"})
	if len(issues) != 0 {
		t.Fatalf("合规章不应有违规: %v", issues)
	}
}

func TestSceneDynamicsValidateAndTrend(t *testing.T) {
	ok := SceneDynamics{Chapter: 1, ConflictEngine: "value", PressureIndex: 5, InfoReleaseRatio: 0.3, EntropyDelta: 0.1}
	if err := ok.Validate(); err != nil {
		t.Fatalf("合法动力不应报错: %v", err)
	}
	for _, bad := range []SceneDynamics{
		{ConflictEngine: "chaos", PressureIndex: 5},
		{ConflictEngine: "value", PressureIndex: 11},
		{ConflictEngine: "value", PressureIndex: 5, InfoReleaseRatio: 1.2},
		{ConflictEngine: "value", PressureIndex: 5, EntropyDelta: -1.5},
	} {
		if err := bad.Validate(); err == nil {
			t.Fatalf("非法动力应报错: %+v", bad)
		}
	}

	// 连续低压
	low := func(ch int) SceneDynamics {
		return SceneDynamics{Chapter: ch, ConflictEngine: "value", PressureIndex: 2, InfoReleaseRatio: 0.2}
	}
	issues := LintSceneDynamicsTrend(low(3), []SceneDynamics{low(1), low(2)})
	if len(issues) == 0 || !strings.Contains(issues[0], "压力指数") {
		t.Fatalf("连续低压应告警: %v", issues)
	}
	// 信息过载
	overload := SceneDynamics{Chapter: 1, ConflictEngine: "value", PressureIndex: 6, InfoReleaseRatio: 0.9}
	if issues := LintSceneDynamicsTrend(overload, nil); len(issues) != 1 {
		t.Fatalf("信息过载应告警一条: %v", issues)
	}
	// 引擎失衡（近 5 章全 survival）
	var hist []SceneDynamics
	for ch := 1; ch <= 4; ch++ {
		hist = append(hist, SceneDynamics{Chapter: ch, ConflictEngine: "survival", PressureIndex: 6, InfoReleaseRatio: 0.3})
	}
	cur := SceneDynamics{Chapter: 5, ConflictEngine: "survival", PressureIndex: 6, InfoReleaseRatio: 0.3}
	found := false
	for _, issue := range LintSceneDynamicsTrend(cur, hist) {
		if strings.Contains(issue, "占比超 70%") {
			found = true
		}
	}
	if !found {
		t.Fatal("单一引擎失衡应告警")
	}
}

func TestPOVStateCheckPOV(t *testing.T) {
	var nilState *POVState
	if issues := nilState.CheckPOV(3, "林昭"); issues != nil {
		t.Fatal("nil 状态应跳过")
	}
	s := &POVState{
		History: []string{"林昭", "林昭", "林昭"},
		Contract: &POVContract{
			RotationInterval: 3,
			Scope:            []string{"林昭", "沈青"},
		},
		ConvergencePoints: []POVConvergence{{Chapter: 5, POVs: []string{"沈青"}}},
	}
	// 越界 POV
	if issues := s.CheckPOV(4, "路人甲"); len(issues) == 0 {
		t.Fatal("scope 外 POV 应告警")
	}
	// 超期未轮换
	if issues := s.CheckPOV(4, "林昭"); len(issues) == 0 {
		t.Fatal("超过 rotation_interval 未切换应告警")
	}
	// 汇合点 POV 不符
	if issues := s.CheckPOV(5, "林昭"); len(issues) == 0 {
		t.Fatal("汇合章 POV 不符应告警")
	}
	// 合规
	if issues := s.CheckPOV(4, "沈青"); len(issues) != 0 {
		t.Fatalf("合规 POV 不应告警: %v", issues)
	}
}

func TestWorldRuleVisibilityFilter(t *testing.T) {
	rules := []WorldRule{
		{Category: "society", Rule: "帝国律法禁私斗"},
		{Category: "society", Rule: "江湖默认不伤家眷", Visibility: "informal", Source: "江湖"},
		{Category: "magic", Rule: "皇室血脉可闻龙语", Visibility: "secret"},
	}
	if got := len(FilterWorldRules(rules, "formal")); got != 1 {
		t.Fatalf("缺省 visibility 应归 formal，期望 1 实际 %d", got)
	}
	if got := len(FilterWorldRules(rules, "informal")); got != 1 {
		t.Fatalf("informal 过滤错误: %d", got)
	}
	if got := len(FilterWorldRules(rules, "secret")); got != 1 {
		t.Fatalf("secret 过滤错误: %d", got)
	}
}

func TestBookWorldValidateFactionRelations(t *testing.T) {
	w := BookWorld{Factions: []WorldFaction{
		{ID: "court", Name: "朝廷", Stance: "hostile", Relations: []FactionRelation{{Target: "jianghu", Kind: "rival", ConflictType: "权力", ConflictState: "cold_war"}}},
		{ID: "jianghu", Name: "江湖"},
	}}
	if issues := w.ValidateFactionRelations(); len(issues) != 0 {
		t.Fatalf("合法关系不应有警告: %v", issues)
	}
	w.Factions[0].Relations = append(w.Factions[0].Relations, FactionRelation{Target: "不存在的势力", Kind: "ally"})
	if issues := w.ValidateFactionRelations(); len(issues) != 1 {
		t.Fatalf("悬空引用应有一条警告: %v", issues)
	}
}

func TestThematicQuestionIsEmpty(t *testing.T) {
	if !(ThematicQuestion{}).IsEmpty() {
		t.Fatal("零值命题应为 empty")
	}
	if (ThematicQuestion{Question: "复仇之后如何生活?"}).IsEmpty() {
		t.Fatal("有命题不应为 empty")
	}
}

func TestConfidenceReportValidate(t *testing.T) {
	ok := ConfidenceReport{Overall: 0.65, PerField: map[string]float64{"draft": 0.6}, Doubts: []string{"中段对话可能拖节奏"}}
	if err := ok.Validate(); err != nil {
		t.Fatalf("合法报告不应报错: %v", err)
	}
	if err := (ConfidenceReport{Overall: 1.2}).Validate(); err == nil {
		t.Fatal("overall 越界应报错")
	}
	if err := (ConfidenceReport{Overall: 0.5, PerField: map[string]float64{"x": -0.1}}).Validate(); err == nil {
		t.Fatal("per_field 越界应报错")
	}
}
