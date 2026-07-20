package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/rules"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func TestZeroRelationshipTypeUsesPairEvidenceAndNegation(t *testing.T) {
	characters := []domain.Character{
		{Name: "林澈", Role: "主角"},
		{Name: "沈知遥", Role: "女主", Description: "高颜值御姐，负责专业核验。"},
		{Name: "贺骁", Role: "主角团配角", Description: "本地发小，负责兄弟助攻。"},
		{Name: "叶南栀", Role: "主角团配角", Description: "在男女主暧昧推进中充当旁观者。"},
		{Name: "许牧", Role: "主角团配角", Description: "不能成为第二男主。"},
		{Name: "陈鹿", Role: "工具型伙伴", Description: "不是林澈感情对象，不与男主暧昧。"},
		{Name: "林建国", Role: "主角父亲"},
		{Name: "周曼", Role: "主角母亲"},
	}
	project := zeroInitProject{Characters: characters}
	tests := map[string]string{
		"沈知遥": "恋爱/暧昧潜势",
		"贺骁":  "合作/友谊",
		"叶南栀": "合作/友谊",
		"许牧":  "合作/友谊",
		"陈鹿":  "合作/友谊",
		"林建国": "亲情",
		"周曼":  "亲情",
	}
	for _, c := range characters[1:] {
		got := zeroRelationshipType(project, c)
		if got != tests[c.Name] {
			t.Errorf("%s relationship=%q, want %q", c.Name, got, tests[c.Name])
		}
		potential := zeroRomancePotential(got)
		if c.Name == "沈知遥" && !strings.Contains(potential, "有恋爱") {
			t.Errorf("female lead romance potential missing: %s", potential)
		}
		if c.Name != "沈知遥" && !strings.HasPrefix(potential, "none") {
			t.Errorf("%s received false romance potential: %s", c.Name, potential)
		}
	}
}

func TestZeroOpeningCharacterFactTruncatesFutureAndSecretKnowledge(t *testing.T) {
	c := domain.Character{
		Name:        "沈知遥",
		Role:        "女主",
		Description: "28岁，青山县文旅融合发展中心副主任，后期转任县域品牌公司负责人。她逐步推理系统存在，之后成为唯一知情人。",
	}
	fact := zeroOpeningCharacterFact(c)
	if !strings.Contains(fact, "28岁") || !strings.Contains(fact, "副主任") {
		t.Fatalf("current identity was lost: %s", fact)
	}
	for _, leaked := range []string{"女主", "后期", "负责人", "系统", "唯一知情"} {
		if strings.Contains(fact, leaked) {
			t.Fatalf("future/author metadata %q leaked into opening fact: %s", leaked, fact)
		}
	}

	project := zeroInitProject{
		Characters:    []domain.Character{{Name: "林澈", Role: "主角"}, c},
		FirstMentions: map[string]int{"林澈": 1, "沈知遥": 1},
		FirstCast:     map[string]bool{"林澈": true, "沈知遥": true},
		FirstChapter:  domain.OutlineEntry{Chapter: 1, CoreEvent: "林澈与沈知遥核验现场。", Scenes: []string{"河畔夜市"}},
	}
	dossiers := zeroInitCharacterDossiers(project)
	for _, dossier := range dossiers {
		if dossier.Character != "沈知遥" || len(dossier.PreStoryTimeline) == 0 {
			continue
		}
		past := dossier.PreStoryTimeline[0].Event
		for _, leaked := range []string{"后期", "系统", "唯一知情"} {
			if strings.Contains(past, leaked) {
				t.Fatalf("future knowledge %q leaked into pre-story timeline: %s", leaked, past)
			}
		}
	}
}

func TestZeroCharacterFirstUseIgnoresFutureAuthorNote(t *testing.T) {
	chars := []domain.Character{
		{Name: "林澈", Role: "主角"},
		{Name: "沈知遥", Role: "女主"},
		{Name: "许牧", Role: "主角团配角"},
	}
	outline := []domain.OutlineEntry{
		{Chapter: 1, CoreEvent: "林澈完成第一步；后续许牧入场处理数字化。", Scenes: []string{"林澈在夜市核验付款。"}},
		{Chapter: 3, CoreEvent: "排队问题扩大。", Scenes: []string{"林澈拨给许牧，请他先看现场数据。"}},
		{Chapter: 7, CoreEvent: "关系协作升级。", Scenes: []string{"沈知遥到现场复核。"}},
	}
	got := zeroCharacterFirstMentions(outline, chars)
	if got["林澈"] != 1 || got["许牧"] != 3 || got["沈知遥"] != 7 {
		t.Fatalf("first causal uses=%v, want 林澈1 许牧3 沈知遥7", got)
	}
	project := zeroInitProject{Characters: chars, FirstMentions: got}
	if priority := zeroReturnPriority(project, chars[2], 13); priority != "planned_later" {
		t.Fatalf("explicit later actor priority=%q, want planned_later", priority)
	}
}

func TestZeroGroundedProjectDoesNotGenerateHorrorTemplate(t *testing.T) {
	project := groundedZeroInitTestProject()
	dynamics := zeroInitDynamics(project)
	storycraft := zeroInitStorycraftPlan(project, dynamics)
	background := zeroInitWorldBackgroundPlan(project)
	plan := zeroInitChapterPlan(project, dynamics, zeroInitCrowdPolicy(project), storycraft, background)
	generated := []any{
		zeroInitWorldFoundation(project),
		zeroInitBookWorld(project),
		dynamics,
		storycraft,
		background,
		plan,
	}
	data, err := json.Marshal(generated)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, marker := range []string{"恐怖", "诡异", "黑市", "规则污染", "死亡", "失踪", "异化", "附身", "传送"} {
		if strings.Contains(text, marker) {
			t.Errorf("grounded zero-init generated forbidden horror marker %q", marker)
		}
	}
	if !strings.Contains(text, "真实付款") || !strings.Contains(text, "河畔夜市") {
		t.Fatalf("grounded plan lost project anchors: %s", text)
	}
}

func TestZeroCountyDelayedCharactersStayOffscreenAndUnacquainted(t *testing.T) {
	project := countyDelayedZeroInitTestProject()
	dynamics := zeroInitDynamics(project)
	stages := zeroOffscreenStage(project, dynamics.Characters)
	dossiers := zeroInitCharacterDossiers(project)
	evidence := zeroEvidenceReturnChains(project, dynamics.Characters)
	storycraft := zeroInitStorycraftPlan(project, dynamics)

	var protagonistState, delayedState domain.CharacterSimulationState
	for _, state := range dynamics.Characters {
		switch state.Character {
		case "林澈":
			protagonistState = state
		case "沈知遥":
			delayedState = state
		}
	}
	if len(delayedState.RelationshipContract) != 0 {
		t.Fatalf("delayed character received pre-story relationship contracts: %+v", delayedState.RelationshipContract)
	}
	for _, contract := range protagonistState.RelationshipContract {
		if contract.Counterpart == "沈知遥" {
			t.Fatalf("protagonist prebuilt future relationship contract: %+v", contract)
		}
	}

	for _, stage := range stages {
		switch stage.Character {
		case "林澈", "贺骁":
			if !stage.VisibleInChapter {
				t.Errorf("first-chapter cast %s was hidden: %+v", stage.Character, stage)
			}
		case "沈知遥":
			if stage.VisibleInChapter {
				t.Fatalf("delayed character became visible in chapter one: %+v", stage)
			}
			if !strings.Contains(stage.Location, "离屏/未定") || strings.Contains(stage.Location, "河畔夜市") {
				t.Fatalf("delayed character was placed at the opening scene: %+v", stage)
			}
		}
	}

	for _, dossier := range dossiers {
		if dossier.Character != "沈知遥" {
			continue
		}
		if len(dossier.Relationships) != 0 {
			t.Fatalf("delayed dossier assumed a protagonist relationship: %+v", dossier.Relationships)
		}
		if len(dossier.PreStoryTimeline) == 0 || len(dossier.PreStoryTimeline[0].PeopleMet) != 0 {
			t.Fatalf("delayed dossier assumed people already met: %+v", dossier.PreStoryTimeline)
		}
		if !strings.Contains(dossier.PreStoryTimeline[0].Relationship, "未相识") {
			t.Fatalf("delayed dossier lacks explicit unacquainted boundary: %+v", dossier.PreStoryTimeline[0])
		}
		if !strings.Contains(dossier.CurrentAtStoryStart.Location, "离屏/未定") {
			t.Fatalf("delayed dossier location is not offscreen: %+v", dossier.CurrentAtStoryStart)
		}
	}

	for _, arc := range storycraft.RelationshipArcs {
		if len(arc.Pair) == 2 && arc.Pair[1] == "沈知遥" {
			if arc.RelationshipType != "未建立/待首次互动" || !strings.Contains(arc.IntimacyStage, "未相识") {
				t.Fatalf("delayed relationship arc was prebuilt: %+v", arc)
			}
		}
	}

	workingSets := map[string]any{"dynamics": dynamics, "dossiers": dossiers, "evidence": evidence, "storycraft": storycraft}
	for label, value := range workingSets {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		workingMemory := string(raw)
		for _, leaked := range []string{"后期转任", "县域品牌公司负责人", "逐步推理系统存在", "唯一知情人", "共同保守系统秘密", "未来恋爱关系"} {
			if strings.Contains(workingMemory, leaked) {
				t.Errorf("future/secret character material leaked into %s working memory: %q", label, leaked)
			}
		}
	}
}

func TestZeroTemplatesDeriveFromCurrentAssetsNotPremiseLabels(t *testing.T) {
	base := zeroInitProject{
		Name:       "通用测试项目",
		Premise:    "两名档案员必须在闭馆前找回一份遗失记录。",
		Characters: []domain.Character{{Name: "甲", Role: "主角"}, {Name: "乙", Role: "搭档"}},
		FirstCast:  map[string]bool{"甲": true, "乙": true},
		FirstChapter: domain.OutlineEntry{
			Chapter:   1,
			Title:     "闭馆前",
			CoreEvent: "甲与乙核对失踪档案的最后流转记录。",
			Hook:      "借阅登记出现一条互相矛盾的时间。",
			Scenes:    []string{"市民档案馆阅览室"},
		},
		Outline: []domain.OutlineEntry{{
			Chapter:   1,
			Title:     "闭馆前",
			CoreEvent: "甲与乙核对失踪档案的最后流转记录。",
			Hook:      "借阅登记出现一条互相矛盾的时间。",
			Scenes:    []string{"市民档案馆阅览室"},
		}},
		WorldRules: []domain.WorldRule{{
			Category: "查阅边界",
			Rule:     "角色只能读取自己获准接触的档案。",
			Boundary: "越权信息不得进入角色知识。",
		}},
		BookWorld: &domain.BookWorld{
			Name:   "当代城市",
			Places: []domain.WorldPlace{{Name: "市民档案馆阅览室"}, {Name: "馆外台阶"}},
		},
	}
	labeled := base
	labeled.Premise = "这是一部恐怖小说和规则怪谈，但所有角色、场景、规则与章节事件保持不变。"

	collect := func(project zeroInitProject) []any {
		first := project.FirstChapter
		return []any{
			zeroLongformOpeningDesign(project, first),
			zeroChapterInformationGaps(project),
			zeroChapterCausalBeat(project),
			zeroChapterDecisionPoints(project),
			zeroChapterOutcomeShift(project),
			zeroEnvironmentState(project),
			zeroAssetOpeningPressureName(project),
			zeroInitWorldBackgroundPlan(project),
		}
	}
	baseline, err := json.Marshal(collect(base))
	if err != nil {
		t.Fatal(err)
	}
	withLabel, err := json.Marshal(collect(labeled))
	if err != nil {
		t.Fatal(err)
	}
	if string(baseline) != string(withLabel) {
		t.Fatalf("premise label selected a hidden production template\nbaseline=%s\nlabeled=%s", baseline, withLabel)
	}
	for _, want := range []string{"失踪档案", "市民档案馆阅览室", "获准接触"} {
		if !strings.Contains(string(baseline), want) {
			t.Errorf("current project asset %q did not reach generated plan: %s", want, baseline)
		}
	}
}

func TestZeroPacingContractUsesUserRulesWordRange(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	snap := rules.BuildSnapshot([]rules.Candidate{
		rules.SystemDefaults(),
		{Source: "runtime_update", Structured: rules.Structured{ChapterWords: &rules.WordRange{Min: 2100, Max: 3200}}},
	})
	if err := st.UserRules.Save(&snap); err != nil {
		t.Fatal(err)
	}
	project := groundedZeroInitTestProject()
	project.Dir = dir
	if err := writeZeroMethodologyArtifactsBatch10(dir, project, zeroInitWorldBackgroundPlan(project), true); err != nil {
		t.Fatal(err)
	}
	contract, err := st.Methodology.LoadPacingContract()
	if err != nil || contract == nil {
		t.Fatalf("load pacing contract: %+v %v", contract, err)
	}
	if contract.ChapterWordMin != 2100 || contract.ChapterWordMax != 3200 {
		t.Fatalf("pacing words=%d-%d, want 2100-3200", contract.ChapterWordMin, contract.ChapterWordMax)
	}
}

func TestZeroReadinessDetectsPositiveForbiddenTopicUse(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Outline.SavePremise("轻松县城经营故事。不写诡异、恐怖、黑市或灵异元素。"); err != nil {
		t.Fatal(err)
	}
	snap := rules.BuildSnapshot([]rules.Candidate{rules.SystemDefaults()})
	if err := st.UserRules.Save(&snap); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "meta", "world_background_plan.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"usage_policy":"开场使用恐怖场景与黑市资源"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	issues := strings.Join(zeroCheckForbiddenTopicContamination(dir), "\n")
	if !strings.Contains(issues, "恐怖") || !strings.Contains(issues, "黑市") {
		t.Fatalf("positive forbidden topics were not blocked: %s", issues)
	}
}

func TestPipelineInitialWorldTickOptionsPreserveUserRules(t *testing.T) {
	opts := pipelineInitialWorldTickHeadlessOptions("internal stage")
	if !opts.PreserveUserRules || !opts.StopAfterInitialWorldTick || opts.Prompt != "internal stage" {
		t.Fatalf("unsafe initial world tick options: %+v", opts)
	}
}

func TestPipelineInitialWorldTickExecutionScopeAndUserRulesDigest(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Runtime.AcquirePipelineExecution(domain.PipelineExecutionLock{
		Mode:          domain.PipelineExecutionFoundation,
		TargetChapter: 1,
		Owner:         "zero-init-test",
	}); err != nil {
		t.Fatal(err)
	}
	snap := rules.BuildSnapshot([]rules.Candidate{rules.SystemDefaults()})
	if err := st.UserRules.Save(&snap); err != nil {
		t.Fatal(err)
	}
	before, err := pipelineInitialWorldTickUserRulesDigest(dir)
	if err != nil {
		t.Fatal(err)
	}
	restore, err := pipelineAcquireInitialWorldTickExecution(st)
	if err != nil {
		t.Fatal(err)
	}
	lock, err := st.Runtime.LoadPipelineExecution()
	if err != nil || lock == nil || lock.Mode != domain.PipelineExecutionWorldTick {
		t.Fatalf("world_tick-only lock missing: %+v err=%v", lock, err)
	}
	if err := pipelineVerifyInitialWorldTickUserRulesDigest(dir, before); err != nil {
		t.Fatalf("unchanged user rules rejected: %v", err)
	}
	if err := restore(); err != nil {
		t.Fatal(err)
	}
	lock, err = st.Runtime.LoadPipelineExecution()
	if err != nil || lock == nil || lock.Mode != domain.PipelineExecutionFoundation {
		t.Fatalf("foundation lock was not restored: %+v err=%v", lock, err)
	}

	path := filepath.Join(dir, "meta", "user_rules.json")
	if err := os.WriteFile(path, []byte(`{"mutated":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := pipelineVerifyInitialWorldTickUserRulesDigest(dir, before); err == nil {
		t.Fatal("user_rules digest mutation was not detected")
	}
}

func TestPipelineWorldTickCanonBriefCarriesArcRulesAndFirstVisibility(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveSimulationRestartPolicy(domain.SimulationRestartPolicy{GenerationID: "simulation-test-generation"}); err != nil {
		t.Fatal(err)
	}
	snap := rules.BuildSnapshot([]rules.Candidate{{
		Source: "runtime_update",
		Structured: rules.Structured{
			Genre:        "现实县城经营",
			ChapterWords: &rules.WordRange{Min: 2000, Max: 3300},
		},
		Preferences: "禁止用未来角色替当前冲突解围。",
	}})
	if err := st.UserRules.Save(&snap); err != nil {
		t.Fatal(err)
	}
	if err := st.Outline.SavePremise("林澈必须让每一笔钱形成真实交付。"); err != nil {
		t.Fatal(err)
	}
	if err := st.Outline.SaveLayeredOutline([]domain.VolumeOutline{{
		Index: 1,
		Title: "回县第一步",
		Theme: "从一笔付款建立可信度",
		Arcs: []domain.ArcOutline{{
			Index: 1,
			Title: "夜市试单",
			Goal:  "完成第一笔可核验支出",
			Chapters: []domain.OutlineEntry{
				{Title: "先付一单", CoreEvent: "林澈完成付款；后续沈知遥入场复核项目", Hook: "摊主要求看到账单"},
				{Title: "现场复核", CoreEvent: "沈知遥到夜市核对交付", Hook: "规则压力升级"},
			},
		}},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := st.Characters.Save([]domain.Character{
		{Name: "林澈", Role: "主角", Description: "返乡运营"},
		{Name: "沈知遥", Role: "女主", Description: "后期成为项目负责人"},
	}); err != nil {
		t.Fatal(err)
	}

	brief := pipelineWorldTickCanonBrief(dir)
	for _, want := range []string{
		"simulation-test-generation",
		"现实县城经营",
		"2000—3300",
		"V1A1《夜市试单》",
		"第1章《先付一单》",
		"第2章《现场复核》",
		"沈知遥｜女主｜最早第2章可见",
	} {
		if !strings.Contains(brief, want) {
			t.Fatalf("canon brief missing %q:\n%s", want, brief)
		}
	}
	if strings.Contains(brief, "沈知遥｜女主｜最早第1章可见") {
		t.Fatalf("future author note pulled delayed lead into chapter one:\n%s", brief)
	}
}

func groundedZeroInitTestProject() zeroInitProject {
	outline := []domain.OutlineEntry{{
		Chapter:   1,
		Title:     "夜市第一笔",
		CoreEvent: "林澈在河畔夜市完成第一笔可核验支出，沈知遥只按现场安全和商户收益复核。",
		Hook:      "第二天有更多摊主询问加入条件。",
		Scenes:    []string{"河畔夜市里，林澈先确认价格和责任，再完成真实付款。"},
	}}
	characters := []domain.Character{
		{Name: "林澈", Role: "主角", Description: "27岁，失业返乡的增长运营。", Arc: "从做成第一笔真实改善开始。", Tier: "core"},
		{Name: "沈知遥", Role: "女主", Description: "28岁，当前负责现场规则协调。", Arc: "从专业纠错到有限认可。", Tier: "core"},
	}
	return zeroInitProject{
		Name:          "县城经营测试",
		Premise:       "轻松、现实落地的县城经营爽文，项目必须让普通商户真实受益。",
		Outline:       outline,
		Characters:    characters,
		WorldRules:    []domain.WorldRule{{Category: "经营", Rule: "资金必须形成真实付款、交付和商户收益。", Boundary: "不能只制造围观热度。"}},
		BookWorld:     &domain.BookWorld{Name: "青山县", Places: []domain.WorldPlace{{ID: "night-market", Name: "河畔夜市", Kind: "market"}}},
		FirstChapter:  outline[0],
		FirstCast:     map[string]bool{"林澈": true, "沈知遥": true},
		FirstMentions: map[string]int{"林澈": 1, "沈知遥": 1},
		GeneratedAt:   "2026-07-17T00:00:00+08:00",
		GenerationID:  "test-generation",
	}
}

func countyDelayedZeroInitTestProject() zeroInitProject {
	outline := []domain.OutlineEntry{
		{
			Chapter:   1,
			Title:     "夜市第一笔",
			CoreEvent: "林澈请贺骁帮忙运送灯架，在河畔夜市完成第一笔可核验支出。",
			Hook:      "摊主要求把后续维修责任写清楚。",
			Scenes:    []string{"河畔夜市里，林澈和贺骁核对价格、走线与交付。"},
		},
		{
			Chapter:   3,
			Title:     "现场复核",
			CoreEvent: "沈知遥第一次到场，按商户收益和安全责任复核项目。",
			Scenes:    []string{"沈知遥在河畔夜市查看票据与灯架。"},
		},
	}
	characters := []domain.Character{
		{Name: "林澈", Role: "主角", Description: "27岁，失业返乡的增长运营。", Arc: "后期成为县域品牌公司的负责人。", Tier: "core", Traits: []string{"务实", "嘴硬"}},
		{Name: "贺骁", Role: "发小/汽修店老板", Description: "当前经营汽修店，愿意把车和工时算清楚。", Arc: "未来成为主角事业合伙人。", Tier: "important", Traits: []string{"直爽", "算账清楚"}},
		{Name: "沈知遥", Role: "女主/文旅干部", Description: "28岁，当前负责县内项目合规，后期转任县域品牌公司负责人。她逐步推理系统存在，之后成为唯一知情人。", Arc: "未来恋爱关系确认后与林澈共同保守系统秘密。", Tier: "core", Traits: []string{"专业", "克制"}},
	}
	return zeroInitProject{
		Name:          "只许把钱花在青山县",
		Premise:       "轻松、现实落地的县城经营故事，资金必须形成普通人能看见的真实收益。",
		Outline:       outline,
		Characters:    characters,
		WorldRules:    []domain.WorldRule{{Category: "经营", Rule: "资金必须形成真实付款、交付和商户收益。", Boundary: "不能只制造围观热度。"}},
		BookWorld:     &domain.BookWorld{Name: "青山县", Places: []domain.WorldPlace{{ID: "night-market", Name: "河畔夜市", Kind: "market"}}},
		FirstChapter:  outline[0],
		FirstCast:     map[string]bool{"林澈": true, "贺骁": true},
		FirstMentions: map[string]int{"林澈": 1, "贺骁": 1, "沈知遥": 3},
		GeneratedAt:   "2026-07-17T00:00:00+08:00",
		GenerationID:  "county-delayed-test-generation",
	}
}
