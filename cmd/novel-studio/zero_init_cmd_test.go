package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
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

func TestZeroInitPipelineBuildsCleanProjectRAG(t *testing.T) {
	dir := seedZeroInitProject(t)
	mustWriteZeroInitTestFile(t, filepath.Join(dir, "chapters", "01.md"), "# 旧正文\n\n旧正文不得进入零章 RAG。")
	mustWriteZeroInitTestFile(t, filepath.Join(dir, "reviews", "01.md"), "# 审稿\n\n审稿不得进入零章 RAG。")
	mustWriteZeroInitTestFile(t, filepath.Join(dir, "experiments", "draft.md"), "# 实验稿\n\n实验稿不得进入零章 RAG。")
	mustWriteZeroInitTestFile(t, filepath.Join(dir, "timeline.md"), "# 旧时间线\n\n旧章节事实不得进入零章 RAG。")
	mustWriteZeroInitTestFile(t, filepath.Join(dir, "relationship_state.md"), "# 旧关系\n\n旧关系状态不得进入零章 RAG。")
	mustWriteZeroInitTestFile(t, filepath.Join(dir, "foreshadow_ledger.md"), "# 旧伏笔\n\n旧伏笔状态不得进入零章 RAG。")

	if err := zeroInitPipeline(cliOptions{}, []string{"--dir", dir}); err != nil {
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
		},
		{
			Name:        "江禾",
			Role:        "妹妹",
			Description: "江烬的责任牵引，也是鬼城账单压力的情感来源。",
			Arc:         "从被保护对象变成能反向提供线索的人。",
			Traits:      []string{"敏感", "忍耐", "会藏住害怕"},
			Tier:        "important",
		},
		{
			Name:        "白骨财神",
			Role:        "反派",
			Description: "账单规则的阶段性压迫源。",
			Arc:         "持续把债务压力推向主角。",
			Traits:      []string{"贪婪", "精算"},
			Tier:        "core",
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
	return dir
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
