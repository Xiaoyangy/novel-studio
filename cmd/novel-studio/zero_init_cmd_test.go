package main

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/tools"
)

func TestZeroInitPipelineScaffoldsRequiredDynamicAssets(t *testing.T) {
	dir := seedZeroInitProject(t)

	if err := zeroInitPipeline(cliOptions{}, []string{"--dir", dir, "--rebuild-rag=false"}); err != nil {
		t.Fatalf("zeroInitPipeline: %v", err)
	}

	required := []string{
		"meta/zero_chapter_context_manifest.json",
		"book_world.json",
		"meta/initial_character_dynamics.json",
		"meta/initial_resource_ledger.json",
		"relationship_state.initial.json",
		"foreshadow_ledger.initial.json",
		"meta/character_return_plan.json",
		"meta/crowd_role_policy.json",
		"meta/prewrite_storycraft_plan.json",
		"meta/prewrite_storycraft_plan.md",
		"meta/world_background_plan.json",
		"meta/world_background_plan.md",
		"drafts/01.zero_init.plan.json",
		"meta/ch01_zero_init_plan.md",
		"meta/first_chapter_generation_readiness.md",
		"meta/story_time_contract.json",
		"meta/story_calendar.json",
	}
	for _, rel := range required {
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			t.Fatalf("expected %s: %v", rel, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "drafts", "01.plan.json")); !os.IsNotExist(err) {
		t.Fatalf("zero-init should not create official writer plan, stat err=%v", err)
	}

	var plan domain.ChapterPlan
	data, err := os.ReadFile(filepath.Join(dir, "drafts", "01.zero_init.plan.json"))
	if err != nil {
		t.Fatalf("read plan: %v", err)
	}
	if err := json.Unmarshal(data, &plan); err != nil {
		t.Fatalf("plan JSON: %v", err)
	}
	if issues := zeroValidateChapterPlan(plan); len(issues) > 0 {
		t.Fatalf("expected complete causal simulation, got issues=%v", issues)
	}
	var rawPlan map[string]any
	if err := json.Unmarshal(data, &rawPlan); err != nil {
		t.Fatalf("raw plan JSON: %v", err)
	}
	rawSim := rawPlan["causal_simulation"].(map[string]any)
	rawStates := rawSim["initial_state"].([]any)
	if rawStates[0].(map[string]any)["relationship_contract"] == nil {
		t.Fatalf("relationship_contract should be an empty array when no key relation, not null")
	}
	// Task 051 契约：覆盖 主角 ∪ 第一章点名 ∪ 全部 core/important（tier 空按 important）。
	// 夹具 3 个角色均无 tier（默认 important），应全员覆盖。
	if got := len(plan.CausalSimulation.InitialState); got != 3 {
		t.Fatalf("expected all core/important character states, got %d: %+v", got, plan.CausalSimulation.InitialState)
	}
	if len(plan.CausalSimulation.WritingNorms) == 0 ||
		plan.CausalSimulation.AntiAIPlan.ObjectResponseBudget == "" ||
		len(plan.CausalSimulation.ExternalRefs) == 0 ||
		len(plan.CausalSimulation.TrendLanguage) == 0 ||
		len(plan.CausalSimulation.GroundingDetails) == 0 ||
		len(plan.CausalSimulation.CharacterArcTests) == 0 ||
		len(plan.CausalSimulation.ReaderRewardPlan.RewardLadder) == 0 ||
		len(plan.CausalSimulation.EvidenceChains) == 0 ||
		plan.CausalSimulation.EndingContract.Consequence == "" ||
		len(plan.CausalSimulation.DormantPolicy) == 0 ||
		len(plan.CausalSimulation.RealitySupport) == 0 ||
		len(plan.CausalSimulation.EmotionalLogic) == 0 ||
		len(plan.CausalSimulation.RelationshipArcs) == 0 ||
		len(plan.CausalSimulation.VisualDesign) == 0 ||
		len(plan.CausalSimulation.DialogueBlueprints) == 0 ||
		plan.CausalSimulation.WorldLayers.EventActivation == "" ||
		len(plan.CausalSimulation.InformationLedger) == 0 ||
		len(plan.CausalSimulation.HiddenRules) == 0 ||
		len(plan.CausalSimulation.SocialMoodRumors) == 0 ||
		len(plan.CausalSimulation.RitualCalendar) == 0 ||
		len(plan.CausalSimulation.StructuralResources) == 0 ||
		len(plan.CausalSimulation.CosmologyChecks) == 0 ||
		len(plan.CausalSimulation.ConflictWeb) == 0 ||
		plan.CausalSimulation.TensionMatrix.ReaderQuestion == "" {
		t.Fatalf("expected zero-init causal plan to include storycraft fields: %+v", plan.CausalSimulation)
	}
	if plan.CausalSimulation.InitialState[0].Character != "江烬" {
		t.Fatalf("expected protagonist first even when other core roles exist, got %s", plan.CausalSimulation.InitialState[0].Character)
	}
	if plan.CausalSimulation.DialogueBlueprints[0].DialogueMode == "" ||
		plan.CausalSimulation.DialogueBlueprints[0].OpeningStrategy == "" ||
		len(plan.CausalSimulation.DialogueBlueprints[0].ObjectiveTactics) == 0 {
		t.Fatalf("expected dialogue mode blueprint in zero-init plan: %+v", plan.CausalSimulation.DialogueBlueprints)
	}
	for _, state := range plan.CausalSimulation.InitialState {
		if state.KnowledgeLedger.Confidence == "" ||
			state.DecisionFrame.DecisionRule == "" || state.EmotionAppraisal.TriggerEvent == "" ||
			state.ArcAxis.PressureTest == "" {
			t.Fatalf("state missing required dynamic fields: %+v", state)
		}
	}
	var returnPlan map[string]domain.CharacterReturnPlan
	data, err = os.ReadFile(filepath.Join(dir, "meta", "character_return_plan.json"))
	if err != nil {
		t.Fatalf("read return plan: %v", err)
	}
	if err := json.Unmarshal(data, &returnPlan); err != nil {
		t.Fatalf("return plan JSON: %v", err)
	}
	if returnPlan["白骨财神"].ReturnPriority == "required" || returnPlan["白骨财神"].SuggestedChapter == 1 {
		t.Fatalf("future antagonist should not be required for chapter 1: %+v", returnPlan["白骨财神"])
	}
	worldData, err := os.ReadFile(filepath.Join(dir, "book_world.json"))
	if err != nil {
		t.Fatalf("read book_world: %v", err)
	}
	if strings.Contains(string(worldData), "白骨财神") || strings.Contains(string(worldData), "江禾") {
		t.Fatalf("zero-init book_world should not promote future characters as first-chapter pressure: %s", string(worldData))
	}
}

func TestZeroInitDerives420ChapterStoryTimeContractAndCalendar(t *testing.T) {
	dir := seedZeroInitProject(t)
	st := store.NewStore(dir)
	compass, err := st.Outline.LoadCompass()
	if err != nil || compass == nil {
		t.Fatalf("LoadCompass: %+v err=%v", compass, err)
	}
	compass.EstimatedScale = "预计100-130万字，约8-10卷，360-480章；主线时间跨度约3年半到4年"
	if err := st.Outline.SaveCompass(*compass); err != nil {
		t.Fatal(err)
	}
	if err := st.Outline.SaveLayeredOutline([]domain.VolumeOutline{{
		Index: 1,
		Title: "全书最终规划",
		Arcs: []domain.ArcOutline{{
			Index:             1,
			Title:             "全书章位合同",
			Goal:              "测试最终目标总章数",
			EstimatedChapters: 420,
		}},
	}}); err != nil {
		t.Fatal(err)
	}
	architectReadiness := assessArchitectReadiness(dir)
	if !architectReadiness.Ready {
		t.Fatalf("architect readiness after final outline: missing=%v issues=%v", architectReadiness.Missing, architectReadiness.Issues)
	}
	if err := writeArchitectReadiness(dir, architectReadiness); err != nil {
		t.Fatal(err)
	}

	if err := zeroInitPipeline(cliOptions{}, []string{"--dir", dir, "--rebuild-rag=false"}); err != nil {
		t.Fatalf("zeroInitPipeline: %v", err)
	}
	contract, err := st.WorldSim.LoadStoryTimeContract()
	if err != nil || contract == nil {
		t.Fatalf("LoadStoryTimeContract: %+v err=%v", contract, err)
	}
	wantNominal := 3.75 * domain.StoryDaysPerYear / 420
	if contract.TargetChapters != 420 || math.Abs(contract.NominalDaysPerChapter-wantNominal) > 1e-9 {
		t.Fatalf("contract = %+v want target=420 nominal=%.9f", contract, wantNominal)
	}
	calendar, err := st.WorldSim.LoadStoryCalendar()
	if err != nil || calendar == nil || math.Abs(calendar.DaysPerChapter-wantNominal) > 1e-9 {
		t.Fatalf("calendar must derive nominal from contract: %+v err=%v", calendar, err)
	}
	readiness := assessZeroInitReadiness(dir, zeroInitRAGStats{})
	if !readiness.Ready || !readiness.StoryTime.Validated || !readiness.StoryTime.CalendarSynced ||
		readiness.StoryTime.TargetChapters != 420 || readiness.StoryTime.CoreDigest == "" ||
		readiness.StoryTime.ScheduleDigest == "" {
		t.Fatalf("readiness must carry validated story-time evidence: %+v", readiness)
	}
}

func TestZeroInitPipelinePreservesFoundationExecutionLock(t *testing.T) {
	dir := seedZeroInitProject(t)
	if err := activatePipelineSealedTwoPassModeAtOutput(dir); err != nil {
		t.Fatalf("activate sealed mode: %v", err)
	}
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	const owner = "zero-init-lock-regression"
	if err := st.Runtime.AcquirePipelineExecution(domain.PipelineExecutionLock{
		Mode:          domain.PipelineExecutionFoundation,
		TargetChapter: 1,
		Owner:         owner,
	}); err != nil {
		t.Fatalf("acquire foundation lock: %v", err)
	}
	t.Cleanup(func() { _ = st.Runtime.ReleasePipelineExecution(owner) })

	if err := zeroInitPipeline(cliOptions{}, []string{
		"--dir", dir,
		"--reset-simulation-state",
		"--rebuild-rag=false",
	}); err != nil {
		t.Fatalf("zeroInitPipeline: %v", err)
	}
	lock, err := st.Runtime.LoadPipelineExecution()
	if err != nil {
		t.Fatalf("load foundation lock after zero-init: %v", err)
	}
	if lock == nil || lock.Owner != owner || lock.Mode != domain.PipelineExecutionFoundation {
		t.Fatalf("zero-init removed or replaced foundation lock: %+v", lock)
	}
}

func TestZeroInitRefreshOpeningPlanPreservesActiveLedgers(t *testing.T) {
	dir := seedZeroInitProject(t)
	if err := zeroInitPipeline(cliOptions{}, []string{"--dir", dir, "--rebuild-rag=false"}); err != nil {
		t.Fatalf("initial zeroInitPipeline: %v", err)
	}
	sentinel := []byte(`{"active":"keep-current-chapter-state"}`)
	sentinelPaths := []string{
		filepath.Join(dir, "meta", "resource_ledger.json"),
		filepath.Join(dir, "relationship_state.json"),
		filepath.Join(dir, "foreshadow_ledger.json"),
	}
	for _, path := range sentinelPaths {
		if err := os.WriteFile(path, sentinel, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Remove(filepath.Join(dir, "meta", "initial_review_lessons.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "meta", "initial_character_dynamics.json"), []byte(`{"stale":"发布会现场"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	outline, err := store.NewStore(dir).Outline.LoadOutline()
	if err != nil || len(outline) == 0 {
		t.Fatalf("load outline: %v %+v", err, outline)
	}
	outline[0].Title = "刷新后的开篇标题"
	if err := store.NewStore(dir).Outline.SaveOutline(outline); err != nil {
		t.Fatal(err)
	}
	if err := zeroInitPipeline(cliOptions{}, []string{"--dir", dir, "--refresh-opening-plan", "--rebuild-rag=false"}); err != nil {
		t.Fatalf("refresh zeroInitPipeline: %v", err)
	}

	planRaw, err := os.ReadFile(filepath.Join(dir, "drafts", "01.zero_init.plan.json"))
	if err != nil || !strings.Contains(string(planRaw), "刷新后的开篇标题") {
		t.Fatalf("opening plan was not refreshed: err=%v body=%s", err, planRaw)
	}
	for _, path := range sentinelPaths {
		gotSentinel, readErr := os.ReadFile(path)
		if readErr != nil || string(gotSentinel) != string(sentinel) {
			t.Fatalf("active ledger changed at %s: err=%v got=%s", path, readErr, gotSentinel)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "meta", "initial_review_lessons.md")); err != nil {
		t.Fatalf("refresh should restore review lessons: %v", err)
	}
	dynamicsRaw, err := os.ReadFile(filepath.Join(dir, "meta", "initial_character_dynamics.json"))
	if err != nil || strings.Contains(string(dynamicsRaw), "发布会现场") {
		t.Fatalf("refresh should replace stale opening dynamics: err=%v body=%s", err, dynamicsRaw)
	}
}

func TestZeroInitCountySpendVoicesSeparateLeadsAndFriends(t *testing.T) {
	project := zeroInitProject{
		Name:    "只许把钱花在青山县",
		Premise: "林澈失业返乡，绑定只能在青山县花钱的县城系统，从河畔夜市开始经营。",
		FirstChapter: domain.OutlineEntry{
			Chapter:   1,
			Title:     "刚被催着找工作，一百万到账了",
			CoreEvent: "林澈在接风饭后核验系统，当晚为夜市完成首笔真实改善消费。",
		},
		Characters: []domain.Character{
			{Name: "林澈", Role: "主角", Tier: "core", Traits: []string{"嘴贫", "有担当"}},
			{Name: "沈知遥", Role: "女主", Tier: "core", Traits: []string{"强势", "护短"}},
			{Name: "贺骁", Role: "主角团配角", Tier: "core", Traits: []string{"嘴碎", "仗义"}},
		},
		FirstCast: map[string]bool{"林澈": true, "沈知遥": true, "贺骁": true},
	}
	dynamics := zeroInitDynamics(project)
	voices := map[string]domain.CharacterVoiceLogic{}
	for _, voice := range dynamics.VoiceLogic {
		voices[voice.Character] = voice
	}
	if voices["林澈"].SpeechPrinciple == voices["沈知遥"].SpeechPrinciple ||
		voices["沈知遥"].DictionAndRhythm == voices["贺骁"].DictionAndRhythm {
		t.Fatalf("county cast voices must be distinct: %+v", voices)
	}
	raw, err := json.Marshal(dynamics)
	if err != nil {
		t.Fatal(err)
	}
	for _, stale := range []string{"发布会", "讲稿", "许闻溪", "可合并资产"} {
		if strings.Contains(string(raw), stale) {
			t.Fatalf("county zero-init contains stale workplace residue %q: %s", stale, raw)
		}
	}
}

func TestCountySpendCompatibilityProfileIsExactProjectOnly(t *testing.T) {
	lookalike := zeroInitProject{
		Name:    "县城消费系统",
		Premise: "主角绑定一百万系统，在青山县夜市帮助商户。",
		FirstChapter: domain.OutlineEntry{
			Title: "县城第一夜", CoreEvent: "夜市完成第一笔消费。",
		},
	}
	if zeroIsCountySpendProject(lookalike) {
		t.Fatal("semantic lookalike inherited another book's project-owned character profile")
	}
	lookalike.Name = "只许把钱花在青山县"
	if !zeroIsCountySpendProject(lookalike) {
		t.Fatal("exact project key did not enable its compatibility profile")
	}
}

func TestSecondAlgorithmCompatibilityProfileIsExactProjectOnly(t *testing.T) {
	lookalike := zeroInitProject{
		Name:    "澄光余晖",
		Premise: "另一座城市也有一家名为澄光的公司，但人物与故事均无关。",
		FirstChapter: domain.OutlineEntry{
			Title: "新同事", CoreEvent: "林青第一次参加澄光的部门例会。",
		},
		Characters: []domain.Character{
			{Name: "林青", Role: "主角", Tier: "core", Traits: []string{"谨慎"}},
			{Name: "周野", Role: "男主", Tier: "core", Traits: []string{"直接"}},
		},
	}
	if zeroIsSecondAlgorithmProject(lookalike) {
		t.Fatal("a different project containing 澄光 inherited the second-algorithm profile")
	}
	for _, character := range lookalike.Characters {
		principle := zeroSpeechPrinciple(lookalike, character)
		for _, foreignName := range []string{"许闻溪", "梁渡", "程棠", "傅行简", "夏岚"} {
			if strings.Contains(principle, foreignName) {
				t.Fatalf("ordinary project speech principle contains foreign name %q: %s", foreignName, principle)
			}
		}
	}

	lookalike.Name = "她的第二算法"
	if !zeroIsSecondAlgorithmProject(lookalike) {
		t.Fatal("exact project identity did not enable the second-algorithm profile")
	}
}

func TestZeroInitDelayedCharacterKeepsOffscreenStateUntilFirstMention(t *testing.T) {
	project := zeroInitProject{
		Name:    "只许把钱花在青山县",
		Premise: "林澈失业返乡，在青山县经营县城生活。",
		FirstChapter: domain.OutlineEntry{
			Chapter: 1, Title: "一百万到账了", CoreEvent: "林澈离开接风饭后去河畔夜市核验第一笔消费。",
		},
		Characters: []domain.Character{
			{Name: "林澈", Role: "主角", Tier: "core"},
			{Name: "叶南栀", Role: "主角团配角", Tier: "core"},
		},
		FirstCast:     map[string]bool{"林澈": true},
		FirstMentions: map[string]int{"林澈": 1, "叶南栀": 7},
	}
	dynamics := zeroInitDynamics(project)
	var state domain.CharacterSimulationState
	var voice domain.CharacterVoiceLogic
	for _, candidate := range dynamics.Characters {
		if candidate.Character == "叶南栀" {
			state = candidate
		}
	}
	for _, candidate := range dynamics.VoiceLogic {
		if candidate.Character == "叶南栀" {
			voice = candidate
		}
	}
	if !strings.Contains(state.CurrentGoal, "第7章前") || !strings.Contains(voice.SceneObjective, "第7章前") {
		t.Fatalf("delayed character lost its offscreen entry boundary: state=%+v voice=%+v", state, voice)
	}
	joined := strings.Join([]string{
		state.Pressure,
		state.EmotionAppraisal.TriggerEvent,
		state.EmotionAppraisal.ActionPressure,
		state.ArcAxis.PressureTest,
		voice.SceneObjective,
		voice.HiddenSubtext,
	}, "\n")
	for _, leaked := range []string{"接风饭", "夜市收摊时间", "第一章核心事件触发"} {
		if strings.Contains(joined, leaked) {
			t.Fatalf("delayed character inherited opening-only state %q: %s", leaked, joined)
		}
	}
	emotion := zeroCharacterEmotionProfile(project, project.Characters[1])
	if !strings.Contains(emotion.ImmediateState, "第7章正式入场前") || strings.Contains(emotion.ImmediateState, "接风饭") || strings.Contains(emotion.ImmediateState, "夜市收摊") {
		t.Fatalf("delayed emotion profile is not offscreen-scoped: %+v", emotion)
	}
}

func TestZeroInitOverwriteRepairsWorldTickFromExistingEvents(t *testing.T) {
	dir := seedZeroInitProject(t)
	st := store.NewStore(dir)
	if _, err := st.WorldSim.AppendWorldEvents([]domain.WorldEvent{{
		TickID:            "v1-a1",
		Chapter:           0,
		Actors:            []string{"AI提效项目组"},
		Summary:           "AI提效项目组把溪流助手列为运营中心试点。",
		VisibilityChapter: 1,
	}}); err != nil {
		t.Fatalf("AppendWorldEvents: %v", err)
	}
	if err := st.WorldSim.SaveTick(domain.WorldTick{TickID: "v0-a0", ThroughChapter: 0}); err != nil {
		t.Fatalf("SaveTick: %v", err)
	}

	if err := zeroInitPipeline(cliOptions{}, []string{"--dir", dir, "--overwrite", "--rebuild-rag=false"}); err != nil {
		t.Fatalf("zeroInitPipeline overwrite: %v", err)
	}

	tick, err := st.WorldSim.LoadTick()
	if err != nil {
		t.Fatalf("LoadTick: %v", err)
	}
	if tick == nil || tick.TickID != "v1-a1" || tick.EventCount != 1 {
		t.Fatalf("world_tick should be repaired from existing events, got %+v", tick)
	}
	events, err := st.WorldSim.LoadWorldEvents()
	if err != nil {
		t.Fatalf("LoadWorldEvents: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("world events should be preserved, got %d", len(events))
	}
}

func TestZeroInitPipelineBuildsCleanProjectRAG(t *testing.T) {
	dir := seedZeroInitProject(t)
	mustWriteZeroInitTestFile(t, filepath.Join(dir, "chapters", "01.md"), "# 旧正文\n\n旧正文不得进入零章 RAG。")
	mustWriteZeroInitTestFile(t, filepath.Join(dir, "reviews", "01.md"), "# 审稿\n\n审稿不得进入零章 RAG。")
	mustWriteZeroInitTestFile(t, filepath.Join(dir, "experiments", "draft.md"), "# 实验稿\n\n实验稿不得进入零章 RAG。")
	mustWriteZeroInitTestFile(t, filepath.Join(dir, "timeline.md"), "# 旧时间线\n\n旧章节事实不得进入零章 RAG。")
	mustWriteZeroInitTestFile(t, filepath.Join(dir, "relationship_state.md"), "# 旧关系\n\n旧关系状态不得进入零章 RAG。")
	mustWriteZeroInitTestFile(t, filepath.Join(dir, "foreshadow_ledger.md"), "# 旧伏笔\n\n旧伏笔状态不得进入零章 RAG。")

	if err := zeroInitPipeline(cliOptions{}, []string{"--dir", dir, "--no-embeddings"}); err != nil {
		t.Fatalf("zeroInitPipeline: %v", err)
	}

	st := store.NewStore(dir)
	state, err := st.RAG.LoadIndexState()
	if err != nil {
		t.Fatalf("LoadIndexState: %v", err)
	}
	if state == nil || len(state.Chunks) == 0 {
		t.Fatal("expected zero-init RAG chunks")
	}
	var hasDynamics, hasPlan, hasStorycraft, hasWorldBackground bool
	for _, chunk := range state.Chunks {
		if strings.Contains(chunk.SourcePath, "chapters/") || strings.Contains(chunk.SourcePath, "reviews/") || strings.Contains(chunk.SourcePath, "experiments/") ||
			strings.Contains(chunk.SourcePath, "timeline.md") || strings.Contains(chunk.SourcePath, "relationship_state.md") || strings.Contains(chunk.SourcePath, "foreshadow_ledger.md") {
			t.Fatalf("forbidden zero-init RAG source indexed: %+v", chunk)
		}
		if strings.Contains(chunk.SourcePath, "initial_character_dynamics.md") {
			hasDynamics = true
		}
		if strings.Contains(chunk.SourcePath, "prewrite_storycraft_plan.md") {
			hasStorycraft = true
		}
		if strings.Contains(chunk.SourcePath, "world_background_plan.md") {
			hasWorldBackground = true
		}
		if strings.Contains(chunk.SourcePath, "ch01_zero_init_plan.md") {
			hasPlan = true
		}
	}
	if !hasDynamics || !hasStorycraft || !hasWorldBackground || !hasPlan {
		t.Fatalf("expected zero-init RAG sources, dynamics=%v storycraft=%v world_background=%v plan=%v", hasDynamics, hasStorycraft, hasWorldBackground, hasPlan)
	}
}

func TestZeroInitSecondAlgorithmArtifactsStayWorkplaceBounded(t *testing.T) {
	dir := seedSecondAlgorithmProject(t)

	if err := zeroInitPipeline(cliOptions{}, []string{"--dir", dir, "--rebuild-rag=false"}); err != nil {
		t.Fatalf("zeroInitPipeline: %v", err)
	}

	files := []string{
		"meta/initial_character_dynamics.json",
		"relationship_state.initial.json",
		"meta/character_return_plan.json",
		"meta/crowd_role_policy.json",
		"meta/prewrite_storycraft_plan.json",
		"meta/world_background_plan.json",
		"meta/world_foundation.json",
		"drafts/01.zero_init.plan.json",
		"meta/ch01_zero_init_plan.md",
	}
	forbidden := []string{
		"江烬", "江禾", "白骨财神", "鬼城", "欠费单", "门牌", "黑卡", "阴司", "冥钞",
		"债务", "关系债", "账单", "死亡/失踪", "异化", "收费",
	}
	var combined strings.Builder
	for _, rel := range files {
		data, err := os.ReadFile(filepath.Join(dir, rel))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		text := string(data)
		combined.WriteString(text)
		for _, bad := range forbidden {
			if strings.Contains(text, bad) {
				t.Fatalf("%s contains cross-project or hard-genre residue %q: %s", rel, bad, text)
			}
		}
	}
	if !strings.Contains(combined.String(), "许闻溪") {
		t.Fatalf("zero-init artifacts should stay anchored to current project protagonist")
	}
}

func TestZeroInitSimulationRestartUsesLayeredTotalAndClearsWorldSim(t *testing.T) {
	dir := seedSecondAlgorithmProject(t)
	st := store.NewStore(dir)
	if err := st.Outline.SaveLayeredOutline([]domain.VolumeOutline{{
		Index: 1,
		Title: "第一卷：重新定价",
		Theme: "AI替代焦虑下的女性职场自救",
		Arcs: []domain.ArcOutline{
			{Index: 1, Title: "讲稿背面", Goal: "许闻溪意识到自己被默认可替代", EstimatedChapters: 16},
			{Index: 2, Title: "小组雏形", Goal: "女性互助从吐槽变成可交付能力", EstimatedChapters: 14},
		},
	}, {
		Index: 2,
		Title: "第二卷：可见价值",
		Theme: "把隐形劳动变成可谈判的职业资产",
		Arcs: []domain.ArcOutline{
			{Index: 1, Title: "桥点试水", Goal: "许闻溪走出澄光的单一评价体系", EstimatedChapters: 20},
			{Index: 2, Title: "关系定价", Goal: "感情线和事业线都进入真实选择", EstimatedChapters: 20},
		},
	}}); err != nil {
		t.Fatalf("SaveLayeredOutline: %v", err)
	}
	if err := st.Progress.Init("她的第二算法", 16); err != nil {
		t.Fatalf("Progress.Init: %v", err)
	}
	if _, err := st.WorldSim.AppendWorldEvents([]domain.WorldEvent{{
		TickID:            "v1-a1",
		Chapter:           0,
		Actors:            []string{"澄光AI提效项目组"},
		Summary:           "旧 generation 的世界事件不应进入新 canon。",
		VisibilityChapter: 1,
	}}); err != nil {
		t.Fatalf("AppendWorldEvents: %v", err)
	}
	if err := st.WorldSim.SaveTick(domain.WorldTick{TickID: "v1-a1", Volume: 1, Arc: 1, ThroughChapter: 0, EventCount: 1}); err != nil {
		t.Fatalf("SaveTick: %v", err)
	}
	outline, err := st.Outline.LoadOutline()
	if err != nil {
		t.Fatalf("LoadOutline: %v", err)
	}
	project := zeroInitProject{Name: "她的第二算法", GenerationID: "simulation-test", Outline: outline}
	if err := applyZeroInitSimulationRestartState(dir, &project); err != nil {
		t.Fatalf("applyZeroInitSimulationRestartState: %v", err)
	}
	progress, err := st.Progress.Load()
	if err != nil || progress == nil {
		t.Fatalf("Load progress: %+v err=%v", progress, err)
	}
	if progress.TotalChapters != 70 || !progress.Layered || progress.CurrentVolume != 1 || progress.CurrentArc != 1 {
		t.Fatalf("progress should follow layered outline total 70, got %+v", progress)
	}
	events, err := st.WorldSim.LoadWorldEvents()
	if err != nil || len(events) != 0 {
		t.Fatalf("world events should be cleared on restart: %+v err=%v", events, err)
	}
	tick, err := st.WorldSim.LoadTick()
	if err != nil || tick == nil || tick.TickID != "v0-a0" || tick.EventCount != 0 {
		t.Fatalf("world tick should reset to baseline: %+v err=%v", tick, err)
	}
}

func TestZeroInitialCharactersKeepsLeadsAndProtagonistGroupSeparate(t *testing.T) {
	project := zeroInitProject{Characters: []domain.Character{
		{Name: "叶南栀", Role: "主角团配角", Tier: "core"},
		{Name: "林澈", Role: "主角", Tier: "core"},
		{Name: "沈知遥", Role: "女主", Tier: "core"},
	}}
	if zeroIsProtagonist(project.Characters[0]) {
		t.Fatal("主角团配角不应被判定为 protagonist")
	}
	if got := zeroProtagonist(project.Characters).Name; got != "林澈" {
		t.Fatalf("expected 林澈 as protagonist, got %s", got)
	}
	got := zeroInitialCharacters(project)
	seen := map[string]bool{}
	for _, c := range got {
		seen[c.Name] = true
	}
	for _, name := range []string{"林澈", "沈知遥", "叶南栀"} {
		if !seen[name] {
			t.Fatalf("expected %s in initial characters, got %+v", name, got)
		}
	}
}

func TestZeroInitialCharactersIncludesSecondaryActorReservedByFutureOutline(t *testing.T) {
	project := zeroInitProject{
		Characters: []domain.Character{
			{Name: "林澈", Role: "主角", Tier: "core"},
			{Name: "罗成海", Role: "公开部门配角", Tier: "secondary"},
			{Name: "路人甲", Role: "临时路人", Tier: "secondary"},
		},
		FirstMentions: map[string]int{
			"林澈":  1,
			"罗成海": 12,
		},
	}
	got := zeroInitialCharacters(project)
	var names []string
	for _, character := range got {
		names = append(names, character.Name)
	}
	if !slices.Contains(names, "罗成海") {
		t.Fatalf("future outline actor was omitted from zero-chapter dynamics: %v", names)
	}
	if slices.Contains(names, "路人甲") {
		t.Fatalf("unreserved secondary actor should not inflate the simulation seed: %v", names)
	}
}

func TestPipelineZeroInitRefreshesFutureActorMissingBehindReadyReceipt(t *testing.T) {
	dir := seedZeroInitProject(t)
	st := store.NewStore(dir)

	characters, err := st.Characters.Load()
	if err != nil {
		t.Fatal(err)
	}
	characters = append(characters, domain.Character{
		Name:        "罗成海",
		Role:        "公开部门配角",
		Description: "负责核验后续公开记录的基层工作人员。",
		Arc:         "从按表办事到愿意留下可复核证据。",
		Traits:      []string{"谨慎", "守流程"},
		Tier:        "secondary",
		Psych:       zeroInitTestPsych("审慎"),
	})
	if err := st.Characters.Save(characters); err != nil {
		t.Fatal(err)
	}
	outline, err := st.Outline.LoadOutline()
	if err != nil {
		t.Fatal(err)
	}
	outline = append(outline, domain.OutlineEntry{
		Chapter:   2,
		Title:     "公开记录",
		CoreEvent: "罗成海按流程核验公开记录，并给江烬留下一条可复核回执。",
		Hook:      "回执编号指向另一份欠费单。",
		Scenes:    []string{"公开窗口"},
	})
	if err := st.Outline.SaveOutline(outline); err != nil {
		t.Fatal(err)
	}
	architect := assessArchitectReadiness(dir)
	if !architect.Ready {
		t.Fatalf("expanded fixture architect readiness: missing=%v issues=%v", architect.Missing, architect.Issues)
	}
	if err := writeArchitectReadiness(dir, architect); err != nil {
		t.Fatal(err)
	}
	if err := zeroInitPipeline(cliOptions{}, []string{
		"--dir", dir,
		"--reset-simulation-state",
		"--rebuild-rag=false",
	}); err != nil {
		t.Fatalf("seed zero-init: %v", err)
	}
	if ok, reason := pipelineCurrentZeroInitReadinessState(dir); !ok {
		t.Fatalf("fresh zero-init rejected: %s", reason)
	}

	dynamicsPath := filepath.Join(dir, "meta", "initial_character_dynamics.json")
	data, err := os.ReadFile(dynamicsPath)
	if err != nil {
		t.Fatal(err)
	}
	var dynamics zeroInitCharacterDynamicsDoc
	if err := json.Unmarshal(data, &dynamics); err != nil {
		t.Fatal(err)
	}
	dynamics.Characters = slices.DeleteFunc(dynamics.Characters, func(state domain.CharacterSimulationState) bool {
		return state.Character == "罗成海"
	})
	dynamics.VoiceLogic = slices.DeleteFunc(dynamics.VoiceLogic, func(voice domain.CharacterVoiceLogic) bool {
		return voice.Character == "罗成海"
	})
	data, err = json.MarshalIndent(dynamics, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dynamicsPath, append(data, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}

	// This reproduces the production regression: the durable receipt and its
	// foundation timestamps are still valid, but the newly required actor is
	// absent from the already-generated dynamics.
	if ok, reason := tools.ZeroInitReadinessState(dir); !ok {
		t.Fatalf("fixture must retain the old ready receipt: %s", reason)
	}
	if ok, reason := pipelineCurrentZeroInitReadinessState(dir); ok || !strings.Contains(reason, "罗成海") {
		t.Fatalf("current semantic coverage did not invalidate stale dynamics: ok=%v reason=%q", ok, reason)
	}

	args := append(pipelineZeroInitRegenerationArgs(dir), "--rebuild-rag=false")
	if !slices.Contains(args, "--overwrite") {
		t.Fatalf("chapter-zero regeneration must overwrite derived zero-init assets: %v", args)
	}
	if err := zeroInitPipeline(cliOptions{}, args); err != nil {
		t.Fatalf("regenerate zero-init: %v", err)
	}
	if ok, reason := pipelineCurrentZeroInitReadinessState(dir); !ok {
		t.Fatalf("regenerated zero-init rejected: %s", reason)
	}
	data, err = os.ReadFile(dynamicsPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &dynamics); err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(dynamics.Characters, func(state domain.CharacterSimulationState) bool {
		return state.Character == "罗成海"
	}) {
		t.Fatalf("future actor still missing after chapter-zero regeneration: %+v", dynamics.Characters)
	}
}

func TestPipelineZeroInitDoesNotRewritePublishedProject(t *testing.T) {
	dir := seedZeroInitProject(t)
	st := store.NewStore(dir)
	if err := st.Progress.Init("已出版测试书", 2); err != nil {
		t.Fatal(err)
	}
	if err := st.Drafts.SaveFinalChapter(1, "第一章已经出版。"); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.MarkChapterComplete(1, 8, "published", "main"); err != nil {
		t.Fatal(err)
	}
	dynamicsPath := filepath.Join(dir, "meta", "initial_character_dynamics.json")
	const publishedDynamics = `{"published_history":"must-not-be-rewritten"}`
	if err := os.MkdirAll(filepath.Dir(dynamicsPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dynamicsPath, []byte(publishedDynamics), 0o644); err != nil {
		t.Fatal(err)
	}

	runRoot := filepath.Dir(filepath.Dir(dir))
	configPath := filepath.Join(runRoot, "config.json")
	if err := os.WriteFile(configPath, []byte(`{
  "provider": "ollama",
  "model": "zero-init-published-test",
  "providers": {
    "ollama": {
      "type": "openai",
      "base_url": "http://127.0.0.1:11434/v1"
    }
  }
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := pipelineZeroInit(
		cliOptions{ConfigPath: configPath, Dir: runRoot},
		pipelineFlags{},
		&domain.PipelineState{},
	); err != nil {
		t.Fatalf("published zero-init stage: %v", err)
	}
	data, err := os.ReadFile(dynamicsPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != publishedDynamics {
		t.Fatalf("published initial dynamics changed: got=%q want=%q", data, publishedDynamics)
	}
}

func TestZeroWorldSimDoesNotRewritePublishedStoryTimeArtifacts(t *testing.T) {
	dir := seedZeroInitProject(t)
	st := store.NewStore(dir)
	if err := st.Progress.Init("已出版时间合同测试", 2); err != nil {
		t.Fatal(err)
	}
	if err := st.Drafts.SaveFinalChapter(1, "第一章已经出版。"); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.MarkChapterComplete(1, 8, "published", "main"); err != nil {
		t.Fatal(err)
	}
	if err := st.WorldSim.SaveStoryTimeContract(domain.StoryTimeContract{
		Source:                domain.StoryTimeSourceExplicit,
		TargetChapters:        2,
		DurationDaysMin:       5,
		DurationDaysMax:       7,
		NominalDaysPerChapter: 3,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.WorldSim.SaveStoryCalendar(domain.StoryCalendar{
		Era:            "出版后纪年不得改",
		DaysPerChapter: 1.25,
		Notes:          []string{"出版正文使用的既有时间轴"},
	}); err != nil {
		t.Fatal(err)
	}
	contractPath := filepath.Join(dir, "meta", "story_time_contract.json")
	calendarPath := filepath.Join(dir, "meta", "story_calendar.json")
	beforeContract, err := os.ReadFile(contractPath)
	if err != nil {
		t.Fatal(err)
	}
	beforeCalendar, err := os.ReadFile(calendarPath)
	if err != nil {
		t.Fatal(err)
	}
	project, err := loadZeroInitProject(st, dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeZeroWorldSimAssets(dir, project, true); err != nil {
		t.Fatalf("writeZeroWorldSimAssets: %v", err)
	}
	afterContract, err := os.ReadFile(contractPath)
	if err != nil {
		t.Fatal(err)
	}
	afterCalendar, err := os.ReadFile(calendarPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(afterContract) != string(beforeContract) || string(afterCalendar) != string(beforeCalendar) {
		t.Fatalf("published time artifacts changed:\ncontract before=%s after=%s\ncalendar before=%s after=%s",
			beforeContract, afterContract, beforeCalendar, afterCalendar)
	}
}

func seedZeroInitProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "output", "novel")
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := st.Outline.SavePremise("失业风控员江烬进入一座按契约和账单运转的鬼城，为妹妹江禾寻找活路。"); err != nil {
		t.Fatalf("SavePremise: %v", err)
	}
	if err := st.Outline.SaveOutline([]domain.OutlineEntry{{
		Chapter:   1,
		Title:     "午夜欠费单",
		CoreEvent: "江烬收到鬼城入住欠费单，被迫核验第一条规则。",
		Hook:      "欠费单上的妹妹姓名多出一笔红账。",
		Scenes:    []string{"老小区楼道", "便利店收银台"},
	}}); err != nil {
		t.Fatalf("SaveOutline: %v", err)
	}
	if err := st.Characters.Save([]domain.Character{
		{
			Name:        "江烬",
			Role:        "主角",
			Description: "失业后的风控员，习惯先核验条款和签字风险，对妹妹江禾有责任压力。",
			Arc:         "从只会规避风险到学会用规则承担代价。",
			Traits:      []string{"警惕", "不乱签字", "对交易文本敏感"},
			Tier:        "core",
			Psych:       zeroInitTestPsych("紧张"),
		},
		{
			Name:        "江禾",
			Role:        "妹妹",
			Description: "江烬的责任牵引，也是鬼城账单压力的情感来源。",
			Arc:         "从被保护对象变成能反向提供线索的人。",
			Traits:      []string{"敏感", "忍耐", "会藏住害怕"},
			Tier:        "important",
			Psych:       zeroInitTestPsych("担心"),
		},
		{
			Name:        "白骨财神",
			Role:        "反派",
			Description: "账单规则的阶段性压迫源。",
			Arc:         "持续把债务压力推向主角。",
			Traits:      []string{"贪婪", "精算"},
			Tier:        "core",
			Psych:       zeroInitTestPsych("贪婪"),
		},
	}); err != nil {
		t.Fatalf("SaveCharacters: %v", err)
	}
	if err := st.World.SaveWorldRules([]domain.WorldRule{
		{Category: "contract", Rule: "鬼城承认签名、欠费、门牌和收据，口头解释不具备免债效力。", Boundary: "角色不能无证据免除账单。"},
		{Category: "society", Rule: "住户之间的帮助会形成债务，债务必须在后续台账中回填。", Boundary: "信任不能瞬间满格。"},
	}); err != nil {
		t.Fatalf("SaveWorldRules: %v", err)
	}
	if err := st.World.SaveBookWorld(domain.BookWorld{
		Version: 1,
		Name:    "鬼城",
		Summary: "按契约、欠费单和门牌运转的夜间城市。",
		Places: []domain.WorldPlace{
			{ID: "old_block", Name: "老小区楼道", Kind: "residential", Description: "江烬收到欠费单的开场地点。"},
			{ID: "corner_store", Name: "便利店", Kind: "shop", Description: "规则第一次具象化的公共场所。"},
		},
		Factions: []domain.WorldFaction{
			{ID: "debt_rule", Name: "账单规则", Goal: "让住户承认债务并持续付出代价。", Clock: &domain.FactionClock{Segments: 6, Progress: 1, Consequence: "规则压力扩大到整栋楼", Pace: "每弧 1 段"}},
			{ID: "residents", Name: "住户", Goal: "在互相欠债的规则里求生。", Clock: &domain.FactionClock{Segments: 6, Progress: 0, Consequence: "住户形成临时互助或互害秩序", Pace: "每弧 1 段"}},
		},
	}); err != nil {
		t.Fatalf("SaveBookWorld: %v", err)
	}
	if err := st.SaveWorldCodex(zeroInitTestWorldCodex()); err != nil {
		t.Fatalf("SaveWorldCodex: %v", err)
	}
	if err := st.Outline.SaveCompass(domain.StoryCompass{
		EndingDirection: "江烬学会在规则内承担代价，最终为江禾和普通住户争取可呼吸的活路。",
		OpenThreads:     []string{"江禾红账来源", "鬼城债务规则真相", "江烬如何承担而非逃避"},
		EstimatedScale:  "长篇多卷",
	}); err != nil {
		t.Fatalf("SaveCompass: %v", err)
	}
	readiness := assessArchitectReadiness(dir)
	if !readiness.Ready {
		t.Fatalf("architect readiness fixture invalid: missing=%v issues=%v warnings=%v", readiness.Missing, readiness.Issues, readiness.Warnings)
	}
	if err := writeArchitectReadiness(dir, readiness); err != nil {
		t.Fatalf("writeArchitectReadiness: %v", err)
	}
	return dir
}

func seedSecondAlgorithmProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "她的第二算法", "output", "novel")
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := st.Outline.SavePremise("AI改变职业的都市女性成长小说。澄光科技上市前夜，运营产品经理许闻溪在AI提效项目里被默认可替代，她要重新给自己的能力定价，并和梁渡在缓慢试探中建立信任。"); err != nil {
		t.Fatalf("SavePremise: %v", err)
	}
	if err := st.Outline.SaveOutline([]domain.OutlineEntry{{
		Chapter:   1,
		Title:     "讲稿背面",
		CoreEvent: "许闻溪在澄光AI提效项目会前发现客户反馈被讲稿删掉，夏岚要求她只讲演示效果，梁渡旁听时追问真实成本。",
		Hook:      "会议结束前，许闻溪把被删掉的一句客户原话补回讲稿背面。",
		Scenes:    []string{"澄光生活运营中心会议室", "下班后的电梯间"},
	}}); err != nil {
		t.Fatalf("SaveOutline: %v", err)
	}
	if err := st.Characters.Save([]domain.Character{
		{
			Name:        "许闻溪",
			Role:        "女主/运营产品经理",
			Description: "三十岁左右，有文学素养的程序员型产品经理，习惯先核验证据，也习惯把委屈压进工作动作里。",
			Arc:         "从替系统补漏洞的人，变成能为自己和身边女性重新设计选择的人。",
			Traits:      []string{"克制", "敏感", "证据意识强"},
			Tier:        "core",
			Psych:       zeroInitTestPsych("克制"),
		},
		{
			Name:        "梁渡",
			Role:        "男主/外部审阅顾问",
			Description: "克制敏锐，先问具体代价，不急着安慰人。",
			Arc:         "从旁观审阅到愿意尊重许闻溪的边界并并肩承担风险。",
			Traits:      []string{"冷静", "边界感", "观察细"},
			Tier:        "core",
			Psych:       zeroInitTestPsych("审慎"),
		},
		{
			Name:        "夏岚",
			Role:        "上级/阶段性压力源",
			Description: "懂组织取舍，擅长把要求说成机会。",
			Arc:         "持续测试许闻溪的边界和职业定价。",
			Traits:      []string{"体面", "强势", "会留余地"},
			Tier:        "important",
			Psych:       zeroInitTestPsych("压抑"),
		},
		{
			Name:        "程棠",
			Role:        "闺蜜/内容策划",
			Description: "爱吐槽，怕被淘汰又不想只做被保护的人。",
			Arc:         "从求助到能和许闻溪一起拆解自己的新能力。",
			Traits:      []string{"口语化", "敏感", "外热内慌"},
			Tier:        "important",
			Psych:       zeroInitTestPsych("不安"),
		},
		{
			Name:        "邱梅",
			Role:        "母亲",
			Description: "上一代女性的忍耐和体面来源，常把心疼说得很轻。",
			Arc:         "让许闻溪看见自己不能再复制旧式忍耐。",
			Traits:      []string{"节省", "体面", "心疼"},
			Tier:        "important",
			Psych:       zeroInitTestPsych("担心"),
		},
	}); err != nil {
		t.Fatalf("SaveCharacters: %v", err)
	}
	if err := st.World.SaveWorldRules([]domain.WorldRule{
		{Category: "ai_work", Rule: "AI工具只能改变工作分配，不能替人物承担选择后果。", Boundary: "没有现场证据、客户反馈或明确权限，不能让工具直接解决人物困境。"},
		{Category: "workplace", Rule: "岗位价值必须通过可见行动、客户反馈、协作成本和关系后果呈现。", Boundary: "不能用作者总结替代场景变化。"},
	}); err != nil {
		t.Fatalf("SaveWorldRules: %v", err)
	}
	if err := st.World.SaveBookWorld(domain.BookWorld{
		Version: 1,
		Name:    "澄港",
		Summary: "AI改变职业背景下的都市职场与女性互助网络。",
		Places: []domain.WorldPlace{
			{ID: "chenguang_meeting", Name: "澄光生活运营中心会议室", Kind: "office", Description: "第一章公开发言与材料改动的现场。"},
			{ID: "bridgepoint", Name: "桥点职业转型工作室", Kind: "training", Description: "后续女性职业转型和互助的现实节点。"},
			{ID: "community_business", Name: "澄港社区商户街", Kind: "community", Description: "客户反馈和现实生意压力落地的地方。"},
		},
		Factions: []domain.WorldFaction{
			{ID: "chenguang_ai", Name: "澄光AI提效项目组", Goal: "在上市前证明效率提升。", Clock: &domain.FactionClock{Segments: 6, Progress: 1, Consequence: "推动第二轮岗位合并方案", Pace: "每弧 1 段"}},
			{ID: "ops_women", Name: "女性职业转型小组", Goal: "在岗位变化里重新定价自己的能力。", Clock: &domain.FactionClock{Segments: 8, Progress: 0, Consequence: "从临时互助变成可交付小组", Pace: "每弧 1 段"}},
			{ID: "bridgepoint", Name: "桥点职业转型工作室", Goal: "把转型焦虑变成可交付服务。", Clock: &domain.FactionClock{Segments: 6, Progress: 1, Consequence: "决定是否接纳许闻溪的项目", Pace: "每弧 1 段"}},
		},
	}); err != nil {
		t.Fatalf("SaveBookWorld: %v", err)
	}
	if err := st.SaveWorldCodex(zeroInitSecondAlgorithmWorldCodex()); err != nil {
		t.Fatalf("SaveWorldCodex: %v", err)
	}
	if err := st.Outline.SaveCompass(domain.StoryCompass{
		EndingDirection: "许闻溪建立自己的第二算法：不再只被系统评价，而是能为自己和身边女性设计可持续选择。",
		OpenThreads:     []string{"许闻溪如何重新定价能力", "梁渡和许闻溪如何建立信任", "程棠等女性如何从被替代感里找到新入口"},
		EstimatedScale:  "长篇多卷",
	}); err != nil {
		t.Fatalf("SaveCompass: %v", err)
	}
	readiness := assessArchitectReadiness(dir)
	if !readiness.Ready {
		t.Fatalf("architect readiness fixture invalid: missing=%v issues=%v warnings=%v", readiness.Missing, readiness.Issues, readiness.Warnings)
	}
	if err := writeArchitectReadiness(dir, readiness); err != nil {
		t.Fatalf("writeArchitectReadiness: %v", err)
	}
	return dir
}

func zeroInitSecondAlgorithmWorldCodex() domain.WorldCodex {
	sections := make([]domain.CodexSection, 0, len(domain.RequiredCodexSections))
	for _, sec := range domain.RequiredCodexSections {
		sections = append(sections, domain.CodexSection{
			Key:     sec.Key,
			Title:   sec.Title,
			Content: "职场女性成长设定：" + sec.Title,
			Rules:   []string{"设定必须通过人物选择、职场物件和关系后果进入正文"},
		})
	}
	return domain.WorldCodex{
		Version: 1,
		AbilityTiers: []domain.CodexAbilityTier{
			{Order: 1, Name: "岗位执行者", Magnitude: "能完成既有流程和局部判断", Limits: "不能改变资源分配", Promotion: "拿到一次可见成果"},
			{Order: 2, Name: "问题定义者", Magnitude: "能重新定义问题和交付边界", Limits: "必须付出关系或时间成本", Promotion: "获得客户和同事双重反馈"},
		},
		SkillDomains:        []domain.CodexDomainEntry{{Name: "职业定价", Description: "把经验、客户反馈和AI工具转成可被看见的价值", TierBinding: "问题定义者"}},
		Races:               []domain.CodexRace{{Name: "现实职场人", Description: "都市职场中的普通人", Constraints: []string{"受时间、岗位和关系限制"}}},
		WeaponCategories:    []domain.CodexGradedCategory{{Name: "工作物件", Description: "讲稿、手机、工单、会议记录等普通物件", Grades: []string{"普通", "关键证据"}}},
		EquipmentCategories: []domain.CodexGradedCategory{{Name: "职业资源", Description: "发言机会、客户反馈、培训名额、排班时间", Grades: []string{"临时", "可用", "已回填"}}},
		Sections:            sections,
		ImmutabilityPolicy:  "修订必须提供 change_reason 与 change_evidence。",
	}
}

func zeroInitTestPsych(label string) *domain.CharacterPsychProfile {
	return &domain.CharacterPsychProfile{
		BigFive: &domain.BigFive{
			Openness:          0.55,
			Conscientiousness: 0.68,
			Extraversion:      0.42,
			Agreeableness:     0.53,
			Neuroticism:       0.61,
		},
		EmotionVector: &domain.EmotionVector{
			Valence:      -0.35,
			Arousal:      0.45,
			Intensity:    0.62,
			PrimaryLabel: label,
			Granularity:  0.5,
		},
		Values: &domain.ValuesProfile{
			Values: domain.SchwartzValues{
				SelfDirection: 0.62,
				Stimulation:   0.38,
				Hedonism:      0.2,
				Achievement:   0.55,
				Power:         0.32,
				Security:      0.7,
				Tradition:     0.45,
				Conformity:    0.48,
				Benevolence:   0.58,
				Universalism:  0.5,
			},
			PrimaryDriver: "security + self_direction",
		},
		MoralFoundations: &domain.MoralFoundations{
			HarmCare:            0.62,
			FairnessCheating:    0.66,
			LoyaltyBetrayal:     0.42,
			AuthoritySubversion: 0.36,
			SanctityDegradation: 0.2,
			LibertyOppression:   0.64,
			PrimaryMorality:     "fairness + liberty",
		},
	}
}

func zeroInitTestWorldCodex() domain.WorldCodex {
	sections := make([]domain.CodexSection, 0, len(domain.RequiredCodexSections))
	for _, sec := range domain.RequiredCodexSections {
		sections = append(sections, domain.CodexSection{
			Key:     sec.Key,
			Title:   sec.Title,
			Content: "测试设定：" + sec.Title,
			Rules:   []string{"测试规则必须可被 zero-init 读取"},
		})
	}
	return domain.WorldCodex{
		Version: 1,
		AbilityTiers: []domain.CodexAbilityTier{
			{Order: 1, Name: "普通住户", Magnitude: "只能遵守账单规则求生", Limits: "不能改写欠费单", Promotion: "完成首次规则核验"},
			{Order: 2, Name: "核账人", Magnitude: "能识别条款漏洞并延缓债务", Limits: "不能凭空免债", Promotion: "拿到有效收据"},
		},
		SkillDomains:        []domain.CodexDomainEntry{{Name: "契约核验", Description: "识别账单条款、签名和收据漏洞", TierBinding: "核账人"}},
		Races:               []domain.CodexRace{{Name: "人类", Description: "鬼城中的普通住户", Constraints: []string{"必须遵守已签规则"}}},
		WeaponCategories:    []domain.CodexGradedCategory{{Name: "现实器物", Description: "手电、钥匙、票据等普通物件", Grades: []string{"普通", "规则承认"}}},
		EquipmentCategories: []domain.CodexGradedCategory{{Name: "契约凭证", Description: "欠费单、收据、门牌", Grades: []string{"临时", "有效", "确权"}}},
		Sections:            sections,
		ImmutabilityPolicy:  "修订必须提供 change_reason 与 change_evidence。",
	}
}

func mustWriteZeroInitTestFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
