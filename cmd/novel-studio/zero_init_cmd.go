package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

type zeroInitFlags struct {
	Dir                  string
	Overwrite            bool
	Check                bool
	RebuildRAG           bool
	ResetSimulationState bool
	GenerationID         string
	MaxChunk             int
	MaxFiles             int
	WithEmbeddings       bool
	EmbeddingProvider    string
	EmbeddingModel       string
	EmbeddingBaseURL     string
	EmbeddingAPIKey      string
	EmbeddingAPIKeyEnv   string
}

type zeroInitProject struct {
	Dir           string
	Name          string
	GenerationID  string
	Premise       string
	Outline       []domain.OutlineEntry
	Characters    []domain.Character
	WorldRules    []domain.WorldRule
	BookWorld     *domain.BookWorld
	FirstChapter  domain.OutlineEntry
	FirstCast     map[string]bool
	FirstMentions map[string]int
	GeneratedAt   string
}

type zeroInitCharacterDynamicsDoc struct {
	Version        int                               `json:"version"`
	Scope          string                            `json:"scope"`
	Chapter        int                               `json:"chapter"`
	GeneratedAt    string                            `json:"generated_at,omitempty"`
	RequiredFields []string                          `json:"required_fields"`
	Characters     []domain.CharacterSimulationState `json:"characters"`
	VoiceLogic     []domain.CharacterVoiceLogic      `json:"voice_logic"`
}

type zeroPrewriteStorycraftPlan struct {
	Version            int                              `json:"version"`
	Scope              string                           `json:"scope"`
	Project            string                           `json:"project"`
	Chapter            int                              `json:"chapter"`
	GeneratedAt        string                           `json:"generated_at,omitempty"`
	UsagePolicy        string                           `json:"usage_policy"`
	ArcTests           []domain.CharacterArcTest        `json:"character_arc_tests"`
	VoiceCards         []domain.CharacterVoiceLogic     `json:"voice_cards"`
	DialogueBlueprints []domain.DialogueSceneBlueprint  `json:"dialogue_scene_blueprints"`
	ReaderReward       domain.ReaderRewardPlan          `json:"reader_reward_plan"`
	EvidenceChains     []domain.EvidenceReturnChain     `json:"evidence_return_chains"`
	EndingContract     domain.EndingConsequenceContract `json:"ending_consequence_contract"`
	DormantPolicy      []domain.DormantCharacterPolicy  `json:"dormant_character_policy"`
	RealitySupport     []domain.RealitySupportPlan      `json:"reality_support_plan"`
	EmotionalLogic     []domain.CharacterEmotionalLogic `json:"emotional_logic"`
	RelationshipArcs   []domain.RelationshipEmotionArc  `json:"relationship_emotion_arcs"`
	VisualDesign       []domain.CharacterVisualDesign   `json:"visual_design"`

	// ThematicQuestion 全书核心命题 + 每卷变奏（零章确定，防 100 章后主题散掉）。可选。
	ThematicQuestion domain.ThematicQuestion `json:"thematic_question,omitzero"`
}

type zeroWorldBackgroundPlan struct {
	Version             int                                 `json:"version"`
	Scope               string                              `json:"scope"`
	Project             string                              `json:"project"`
	Chapter             int                                 `json:"chapter"`
	GeneratedAt         string                              `json:"generated_at,omitempty"`
	UsagePolicy         string                              `json:"usage_policy"`
	ResearchBasis       []string                            `json:"research_basis,omitempty"`
	Layers              domain.WorldBackgroundLayersPlan    `json:"world_background_layers"`
	InformationLedger   []domain.InformationAsymmetryRecord `json:"information_asymmetry"`
	HiddenRules         []domain.HiddenRulePressure         `json:"hidden_rule_pressure"`
	SocialMoodRumors    []domain.SocialMoodRumor            `json:"social_mood_rumors"`
	RitualCalendar      []domain.RitualCalendarWindow       `json:"ritual_calendar"`
	StructuralResources []domain.StructuralResourcePressure `json:"structural_resources"`
	CosmologyChecks     []domain.CosmologyRuleCheck         `json:"cosmology_checks"`
	ConflictWeb         []domain.ConflictWebNode            `json:"conflict_web"`
	TensionMatrix       domain.NarrativeTensionMatrix       `json:"narrative_tension_matrix"`
}

type zeroInitRAGStats struct {
	Enabled        bool   `json:"enabled"`
	IndexPath      string `json:"index_path,omitempty"`
	Files          int    `json:"files,omitempty"`
	Chunks         int    `json:"chunks,omitempty"`
	SkippedDup     int    `json:"skipped_dup,omitempty"`
	VectorEnabled  bool   `json:"vector_enabled,omitempty"`
	VectorEmbedded int    `json:"vector_embedded,omitempty"`
	VectorWritten  int    `json:"vector_written,omitempty"`
}

// zeroReadinessSchemaVersion readiness 文件的 schema 版本。消费方（pipeline 写前检查、
// 外部 agent）读到缺 schema_version 或 < 当前值的 readiness 一律视为 not ready——
// 防止旧版生成器的 ready:true 被误信（清跑项目事故根因）。
const zeroReadinessSchemaVersion = 2

type zeroInitReadiness struct {
	Ready            bool             `json:"ready"`
	SchemaVersion    int              `json:"schema_version,omitempty"`
	GeneratorVersion string           `json:"generator_version,omitempty"`
	Missing          []string         `json:"missing,omitempty"`
	Issues           []string         `json:"issues,omitempty"`
	Warnings         []string         `json:"warnings,omitempty"`
	RAG              zeroInitRAGStats `json:"rag,omitempty"`
	GeneratedAt      string           `json:"generated_at,omitempty"`
	Path             string           `json:"path,omitempty"`
}

func hasZeroInitFlag(argv []string) bool {
	return slices.Contains(argv, "--zero-init")
}

func parseZeroInitFlags(argv []string) (zeroInitFlags, []string, error) {
	fs := flag.NewFlagSet("zero-init", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "用法: novel-studio --zero-init [--dir <output/novel>] [--check] [--overwrite] [--rebuild-rag=false]\n\n")
		fmt.Fprintf(os.Stderr, "为一本还未开写正文的新书生成零章写前梳理资产：本书世界、初始人物动态、资源/关系/伏笔台账、捧场角色策略、第一章推演草案和 RAG 白名单索引。正文仍必须通过 --pipeline 产出。\n\n选项：\n")
		fs.PrintDefaults()
	}
	var f zeroInitFlags
	f.MaxChunk = 900
	f.MaxFiles = 2000
	f.RebuildRAG = true
	fs.StringVar(&f.Dir, "dir", "", "小说输出目录；为空时使用配置中的 OutputDir，仍为空则使用 output/novel")
	fs.BoolVar(&f.Overwrite, "overwrite", false, "覆盖已存在的零章资产；默认只补缺失文件")
	fs.BoolVar(&f.Check, "check", false, "只检查零章资产是否齐全，不生成文件")
	fs.BoolVar(&f.RebuildRAG, "rebuild-rag", f.RebuildRAG, "生成/更新零章白名单 RAG 索引；只索引当前项目设定和零章资产")
	fs.BoolVar(&f.ResetSimulationState, "reset-simulation-state", false, "将活动 progress 切换为新的推演线；不删除旧章节文件，旧数据只保留为背景种子")
	fs.StringVar(&f.GenerationID, "generation-id", "", "指定本轮推演 generation_id；为空时按生成时间自动创建")
	fs.IntVar(&f.MaxChunk, "max-chunk-runes", f.MaxChunk, "单个 RAG chunk 的最大字符数")
	fs.IntVar(&f.MaxFiles, "max-files", f.MaxFiles, "最多索引文件数")
	fs.BoolVar(&f.WithEmbeddings, "with-embeddings", false, "同时写入 embeddings/Qdrant；未指定时只在配置已启用 embedding 时执行")
	fs.StringVar(&f.EmbeddingProvider, "embedding-provider", "", "embedding provider；为空时使用 rag.embedding.provider 或顶层 provider")
	fs.StringVar(&f.EmbeddingModel, "embedding-model", "", "embedding 模型；为空时使用 rag.embedding.model 或 text-embedding-3-small")
	fs.StringVar(&f.EmbeddingBaseURL, "embedding-base-url", "", "embedding OpenAI 兼容 base_url；为空时继承 provider.base_url")
	fs.StringVar(&f.EmbeddingAPIKey, "embedding-api-key", "", "embedding API key；为空时继承 provider.api_key 或 --embedding-api-key-env")
	fs.StringVar(&f.EmbeddingAPIKeyEnv, "embedding-api-key-env", "", "从环境变量读取 embedding API key")
	if err := fs.Parse(argv); err != nil {
		return f, nil, err
	}
	return f, fs.Args(), nil
}

func zeroInitPipeline(opts cliOptions, args []string) error {
	if hasHelpToken(args) {
		_, _, _ = parseZeroInitFlags([]string{"--help"})
		return nil
	}
	flags, extra, err := parseZeroInitFlags(args)
	if err != nil {
		return err
	}
	if len(extra) > 0 {
		return fmt.Errorf("--zero-init 不接受额外参数：%v", extra)
	}
	if flags.MaxChunk < 200 {
		return fmt.Errorf("--max-chunk-runes 不能小于 200")
	}
	if flags.MaxFiles <= 0 {
		return fmt.Errorf("--max-files 必须大于 0")
	}
	dir, err := resolveZeroInitDir(opts, flags.Dir)
	if err != nil {
		return err
	}
	if flags.Check {
		readiness := assessZeroInitReadiness(dir, zeroInitRAGStats{})
		if !readiness.Ready {
			return fmt.Errorf("零章初始化未就绪：missing=%v issues=%v", readiness.Missing, readiness.Issues)
		}
		fmt.Fprintf(os.Stdout, "%s\n", filepath.Join(dir, "meta", "first_chapter_generation_readiness.md"))
		return nil
	}

	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		return err
	}
	project, err := loadZeroInitProject(st, dir)
	if err != nil {
		return err
	}
	if flags.GenerationID != "" {
		project.GenerationID = strings.TrimSpace(flags.GenerationID)
	} else if existingPolicy, perr := st.LoadSimulationRestartPolicy(); perr == nil && existingPolicy != nil && strings.TrimSpace(existingPolicy.GenerationID) != "" {
		project.GenerationID = strings.TrimSpace(existingPolicy.GenerationID)
	}
	if err := writeZeroInitArtifacts(dir, &project, flags.Overwrite); err != nil {
		return err
	}
	if flags.ResetSimulationState {
		if err := applyZeroInitSimulationRestartState(dir, &project); err != nil {
			return err
		}
	}

	ragStats := zeroInitRAGStats{Enabled: flags.RebuildRAG}
	if flags.RebuildRAG {
		ragStats, err = rebuildZeroInitRAG(opts, dir, flags)
		if err != nil {
			return err
		}
	}
	readiness := assessZeroInitReadiness(dir, ragStats)
	if err := writeZeroInitReadiness(dir, readiness, flags.Overwrite); err != nil {
		return err
	}
	if !readiness.Ready {
		return fmt.Errorf("零章资产已生成但未就绪：missing=%v issues=%v", readiness.Missing, readiness.Issues)
	}
	fmt.Fprintf(os.Stderr, "[zero-init] 已生成零章写前梳理：%s\n", filepath.Join(dir, "meta", "ch01_zero_init_plan.md"))
	if ragStats.Enabled {
		fmt.Fprintf(os.Stderr, "[zero-init] RAG 来源=%d chunks=%d index=%s\n", ragStats.Files, ragStats.Chunks, ragStats.IndexPath)
	}
	fmt.Fprintf(os.Stdout, "%s\n", filepath.Join(dir, "meta", "first_chapter_generation_readiness.md"))
	return nil
}

func resolveZeroInitDir(opts cliOptions, explicit string) (string, error) {
	dir := strings.TrimSpace(explicit)
	if dir == "" {
		if cfg, _, err := loadCfgBundle(opts); err == nil {
			dir = strings.TrimSpace(cfg.OutputDir)
		}
	}
	if dir == "" {
		dir = filepath.Join("output", "novel")
	}
	return filepath.Abs(dir)
}

func loadZeroInitProject(st *store.Store, dir string) (zeroInitProject, error) {
	missing := st.FoundationMissing()
	if len(missing) > 0 {
		return zeroInitProject{}, fmt.Errorf("零章初始化缺少基础设定：%s；请先用 --pipeline 让 architect/save_foundation 落盘 foundation", strings.Join(missing, ", "))
	}
	premise, err := st.Outline.LoadPremise()
	if err != nil {
		return zeroInitProject{}, err
	}
	outline, err := st.Outline.LoadOutline()
	if err != nil {
		return zeroInitProject{}, err
	}
	if len(outline) == 0 {
		if layered, err := st.Outline.LoadLayeredOutline(); err == nil && len(layered) > 0 {
			outline = domain.FlattenOutline(layered)
		} else if err != nil {
			return zeroInitProject{}, err
		}
	}
	if len(outline) == 0 {
		return zeroInitProject{}, fmt.Errorf("零章初始化需要至少 1 章大纲")
	}
	chars, err := st.Characters.Load()
	if err != nil {
		return zeroInitProject{}, err
	}
	sortZeroInitCharacters(chars)
	rules, err := st.World.LoadWorldRules()
	if err != nil {
		return zeroInitProject{}, err
	}
	world, err := st.World.LoadBookWorld()
	if err != nil {
		return zeroInitProject{}, err
	}
	firstCast := zeroFirstChapterCast(outline[0], chars)
	firstMentions := zeroCharacterFirstMentions(outline, chars)
	generatedAt := time.Now().Format(time.RFC3339)
	return zeroInitProject{
		Dir:           dir,
		Name:          zeroInitProjectName(dir),
		GenerationID:  zeroSimulationGenerationID(generatedAt),
		Premise:       strings.TrimSpace(premise),
		Outline:       outline,
		Characters:    chars,
		WorldRules:    rules,
		BookWorld:     world,
		FirstChapter:  outline[0],
		FirstCast:     firstCast,
		FirstMentions: firstMentions,
		GeneratedAt:   generatedAt,
	}, nil
}

func writeZeroInitArtifacts(dir string, project *zeroInitProject, overwrite bool) error {
	if project.BookWorld == nil || overwrite {
		world := zeroInitBookWorld(*project)
		project.BookWorld = &world
		if err := writeZeroJSON(filepath.Join(dir, "book_world.json"), world, overwrite); err != nil {
			return err
		}
		if err := writeZeroText(filepath.Join(dir, "book_world.md"), renderZeroBookWorld(world), overwrite); err != nil {
			return err
		}
	}

	restartPolicy := zeroInitSimulationRestartPolicy(*project)
	refreshPolicy := overwrite || zeroShouldWriteArtifact(dir, false, "meta/simulation_restart_policy.json", "meta/simulation_restart_policy.md") || !zeroExistingRestartPolicyMatches(dir, project.GenerationID)
	if refreshPolicy {
		if err := store.NewStore(dir).SaveSimulationRestartPolicy(restartPolicy); err != nil {
			return err
		}
	}

	worldFoundation := zeroInitWorldFoundation(*project)
	if zeroShouldWriteArtifact(dir, overwrite, "meta/world_foundation.json", "meta/world_foundation.md") {
		if err := store.NewStore(dir).SaveWorldFoundation(worldFoundation); err != nil {
			return err
		}
	}
	for _, dossier := range zeroInitCharacterDossiers(*project) {
		if zeroShouldWriteArtifact(dir, overwrite, characterDossierRel(dossier.Character, "dossier.json"), characterDossierRel(dossier.Character, "dossier.md")) {
			if err := store.NewStore(dir).SaveCharacterDossier(dossier); err != nil {
				return err
			}
		}
	}

	dynamics := zeroInitDynamics(*project)
	if err := writeZeroJSON(filepath.Join(dir, "meta", "initial_character_dynamics.json"), dynamics, overwrite); err != nil {
		return err
	}
	if err := writeZeroText(filepath.Join(dir, "meta", "initial_character_dynamics.md"), renderZeroDynamics(dynamics), overwrite); err != nil {
		return err
	}
	resourceLedger := zeroInitResourceLedger(*project)
	if err := writeZeroJSON(filepath.Join(dir, "meta", "initial_resource_ledger.json"), resourceLedger, overwrite); err != nil {
		return err
	}
	if err := writeZeroText(filepath.Join(dir, "meta", "initial_resource_ledger.md"), renderZeroResourceLedger(resourceLedger), overwrite); err != nil {
		return err
	}
	relationship := zeroInitRelationshipState(*project, dynamics.Characters)
	if err := writeZeroJSON(filepath.Join(dir, "relationship_state.initial.json"), relationship, overwrite); err != nil {
		return err
	}
	if err := writeZeroText(filepath.Join(dir, "relationship_state.initial.md"), renderZeroGenericDoc("零章初始关系契约", relationship), overwrite); err != nil {
		return err
	}
	foreshadow := zeroInitForeshadow(*project)
	if err := writeZeroJSON(filepath.Join(dir, "foreshadow_ledger.initial.json"), foreshadow, overwrite); err != nil {
		return err
	}
	if err := writeZeroText(filepath.Join(dir, "foreshadow_ledger.initial.md"), renderZeroGenericDoc("零章初始伏笔账本", foreshadow), overwrite); err != nil {
		return err
	}
	if err := writeZeroText(filepath.Join(dir, "meta", "initial_review_lessons.md"), renderZeroReviewLessons(), overwrite); err != nil {
		return err
	}
	returnPlan := zeroInitReturnPlan(*project)
	if err := writeZeroJSON(filepath.Join(dir, "meta", "character_return_plan.json"), returnPlan, overwrite); err != nil {
		return err
	}
	if err := writeZeroText(filepath.Join(dir, "meta", "character_return_plan.md"), renderZeroGenericDoc("人物回归与续用规划", returnPlan), overwrite); err != nil {
		return err
	}
	crowdPolicy := zeroInitCrowdPolicy()
	if err := writeZeroJSON(filepath.Join(dir, "meta", "crowd_role_policy.json"), crowdPolicy, overwrite); err != nil {
		return err
	}
	if err := writeZeroText(filepath.Join(dir, "meta", "crowd_role_policy.md"), renderZeroGenericDoc("捧场类角色策略", crowdPolicy), overwrite); err != nil {
		return err
	}
	storycraft := zeroInitStorycraftPlan(*project, dynamics)
	refreshStorycraft := overwrite || zeroShouldWriteArtifact(dir, false, "meta/prewrite_storycraft_plan.json", "meta/prewrite_storycraft_plan.md") || !zeroExistingStorycraftReady(dir)
	if err := writeZeroJSON(filepath.Join(dir, "meta", "prewrite_storycraft_plan.json"), storycraft, refreshStorycraft); err != nil {
		return err
	}
	if err := writeZeroText(filepath.Join(dir, "meta", "prewrite_storycraft_plan.md"), renderZeroStorycraftPlan(storycraft), refreshStorycraft); err != nil {
		return err
	}
	worldBackground := zeroInitWorldBackgroundPlan(*project)
	refreshWorldBackground := overwrite || zeroShouldWriteArtifact(dir, false, "meta/world_background_plan.json", "meta/world_background_plan.md") || !zeroExistingWorldBackgroundReady(dir)
	if err := writeZeroJSON(filepath.Join(dir, "meta", "world_background_plan.json"), worldBackground, refreshWorldBackground); err != nil {
		return err
	}
	if err := writeZeroText(filepath.Join(dir, "meta", "world_background_plan.md"), renderZeroWorldBackgroundPlan(worldBackground), refreshWorldBackground); err != nil {
		return err
	}
	plan := zeroInitChapterPlan(*project, dynamics, crowdPolicy, storycraft, worldBackground)
	refreshPlan := overwrite || zeroShouldWriteArtifact(dir, false, "drafts/01.zero_init.plan.json") || !zeroExistingPlanReady(dir)
	if err := writeZeroJSON(filepath.Join(dir, "drafts", "01.zero_init.plan.json"), plan, refreshPlan); err != nil {
		return err
	}
	if err := writeZeroText(filepath.Join(dir, "meta", "ch01_zero_init_plan.md"), renderZeroChapterPlan(plan), refreshPlan); err != nil {
		return err
	}
	if err := writeZeroWorldSimAssets(dir, *project, overwrite); err != nil {
		return err
	}
	if err := writeZeroMethodologyArtifacts(dir, *project, worldBackground, overwrite); err != nil {
		return err
	}
	if err := writeZeroMethodologyArtifactsBatch10(dir, *project, worldBackground, overwrite); err != nil {
		return err
	}
	manifest := zeroInitManifest(*project)
	refreshManifest := overwrite || zeroShouldWriteArtifact(dir, false, "meta/zero_chapter_context_manifest.json") || !zeroExistingManifestReady(dir)
	if err := writeZeroJSON(filepath.Join(dir, "meta", "zero_chapter_context_manifest.json"), manifest, refreshManifest); err != nil {
		return err
	}
	if err := writeZeroText(filepath.Join(dir, "meta", "zero_chapter_context_manifest.md"), renderZeroGenericDoc("零章上下文清单", manifest), refreshManifest); err != nil {
		return err
	}
	return nil
}

func rebuildZeroInitRAG(opts cliOptions, dir string, flags zeroInitFlags) (zeroInitRAGStats, error) {
	cfg, _, err := loadCfgBundle(opts)
	if err != nil {
		cfg = bootstrap.Config{}
	}
	refreshAutoRAGCollectionForOutputDir(&cfg, dir, hasConfiguredRAGQdrantCollection(opts))
	// 配置了写作手法库时随零章索引一并入库，重建不丢 craft chunk。
	sources := appendConfiguredSharedLibraries(zeroInitRAGSources(dir), cfg)
	result, err := buildLocalRAGIndex(dir, sources, flags.MaxChunk, flags.MaxFiles)
	if err != nil {
		return zeroInitRAGStats{}, err
	}
	st := store.NewStore(dir)
	if err := st.RAG.SaveIndexState(result.State); err != nil {
		return zeroInitRAGStats{}, err
	}
	stats := zeroInitRAGStats{
		Enabled:    true,
		IndexPath:  filepath.Join(dir, "meta", "rag", "index_state.json"),
		Files:      result.Files,
		Chunks:     len(result.State.Chunks),
		SkippedDup: result.SkippedDup,
	}
	override := buildRAGEmbeddingOverride(buildRAGFlags{
		WithEmbeddings:     flags.WithEmbeddings,
		EmbeddingProvider:  flags.EmbeddingProvider,
		EmbeddingModel:     flags.EmbeddingModel,
		EmbeddingBaseURL:   flags.EmbeddingBaseURL,
		EmbeddingAPIKey:    flags.EmbeddingAPIKey,
		EmbeddingAPIKeyEnv: flags.EmbeddingAPIKeyEnv,
	})
	if _, enabled := bootstrap.ResolveRAGEmbeddingConfig(cfg, override); enabled {
		embeddingResult, vectorStore, err := buildRAGEmbeddings(context.Background(), cfg, override, result.State.Chunks)
		if err != nil {
			return zeroInitRAGStats{}, err
		}
		if err := st.RAG.SaveIndexState(embeddingResult.State); err != nil {
			return zeroInitRAGStats{}, err
		}
		if err := st.RAG.SaveVectorStore(vectorStore); err != nil {
			return zeroInitRAGStats{}, err
		}
		stats.VectorEnabled = true
		stats.VectorEmbedded = embeddingResult.Embedded
		stats.VectorWritten = embeddingResult.Written
	}
	resetRAGTrace(dir)
	return stats, nil
}

func zeroInitRAGSources(outputDir string) []string {
	outputDir = cleanAbsRAGPath(outputDir)
	projectRoot := ragProjectRoot(outputDir)
	candidates := []string{
		filepath.Join(projectRoot, "prompt.md"),
		filepath.Join(projectRoot, "input"),
		filepath.Join(outputDir, "premise.md"),
		filepath.Join(outputDir, "outline.md"),
		filepath.Join(outputDir, "layered_outline.md"),
		filepath.Join(outputDir, "characters.md"),
		filepath.Join(outputDir, "world_rules.md"),
		filepath.Join(outputDir, "world_codex.md"),
		filepath.Join(outputDir, "book_world.md"),
		filepath.Join(outputDir, "compass.md"),
		filepath.Join(outputDir, "meta", "volume_codex"),
		filepath.Join(outputDir, "meta", "simulation_restart_policy.md"),
		filepath.Join(outputDir, "meta", "world_foundation.md"),
		filepath.Join(outputDir, "meta", "characters"),
		filepath.Join(outputDir, "meta", "zero_chapter_context_manifest.md"),
		filepath.Join(outputDir, "meta", "initial_character_dynamics.md"),
		filepath.Join(outputDir, "meta", "initial_resource_ledger.md"),
		filepath.Join(outputDir, "meta", "initial_review_lessons.md"),
		filepath.Join(outputDir, "meta", "character_return_plan.md"),
		filepath.Join(outputDir, "meta", "crowd_role_policy.md"),
		filepath.Join(outputDir, "meta", "prewrite_storycraft_plan.md"),
		filepath.Join(outputDir, "meta", "world_background_plan.md"),
		filepath.Join(outputDir, "meta", "ch01_zero_init_plan.md"),
		filepath.Join(outputDir, "relationship_state.initial.md"),
		filepath.Join(outputDir, "foreshadow_ledger.initial.md"),
	}
	var out []string
	for _, path := range candidates {
		if _, err := os.Stat(path); err == nil && !containsPath(out, path) {
			out = append(out, path)
		}
	}
	return out
}

func zeroInitManifest(project zeroInitProject) map[string]any {
	return map[string]any{
		"version":                        1,
		"scope":                          "zero_chapter_prewrite",
		"project":                        project.Name,
		"generated_at":                   project.GeneratedAt,
		"principle":                      "零章资产只提供写前推演和检索证据；正文仍由 novel-studio --pipeline 中的 plan_chapter -> draft_chapter -> check_consistency -> commit_chapter 产出。",
		"generation_id":                  project.GenerationID,
		"simulation_restart_policy":      "旧章节、旧计划、旧资源/人物经历只允许作为背景种子；新正文事实必须由本 generation_id 的推演重新生成、入账并审核。",
		"required_dynamic_fields":        zeroRequiredDynamicFields(),
		"required_side_character_fields": []string{"status", "transport", "travel_time", "meeting_constraint", "personality_delta", "death_state", "protagonist_notice"},
		"world_foundation_rule":          "world_foundation 是正文开始前铁律、开局时间和过去时间线；角色未获得改变规则的明确能力/凭证前不得突破。",
		"character_dossier_rule":         "每个角色必须有独立 dossier；主角未通信/未见证/无证据时不能知道配角档案和时间线。",
		"context_sources": []string{
			"premise", "outline/current_chapter_outline", "characters", "world_rules/book_world", "simulation_restart_policy",
			"world_foundation", "character_dossiers", "initial_character_dynamics", "relationship_state.initial", "initial_resource_ledger",
			"foreshadow_ledger.initial", "crowd_role_policy", "prewrite_storycraft_plan", "world_background_plan", "dialogue_writing", "initial_review_lessons",
		},
		"rag_source_policy": map[string]any{
			"allowed":               zeroInitDisplaySources(project.Dir),
			"forbidden_dir_markers": []string{"chapters", "drafts", "summaries", "reviews", "reviews_ai", "meta/rag", "meta/runtime", "meta/sessions", "source_project", "experiments", "拆文库", "deconstruction-library", "对标", "meta/resource_ledger", "meta/state_changes", "meta/project_progress", "meta/character_continuity"},
			"reason":                "第一章开写前只能召回本项目 foundation 和零章推演资产，旧正文、旧资源账、旧人物连续性、审稿、备份、实验稿或参考库都不能当成新推演事实。",
		},
		"review_refinement_required": true,
		"crowd_role_rule":            "捧场/凑数角色默认不命名、不入关键人物台账、不承担关键解谜、救场或反杀；一旦携带新信息、做关键选择、建立债务或后续要回归，必须升级为关键角色并补齐动态字段。",
	}
}

func zeroInitSimulationRestartPolicy(project zeroInitProject) domain.SimulationRestartPolicy {
	return domain.SimulationRestartPolicy{
		Version:              1,
		Project:              project.Name,
		Active:               true,
		Mode:                 "restart_from_seed",
		GenerationID:         project.GenerationID,
		GeneratedAt:          project.GeneratedAt,
		CanonicalStart:       "chapter-001-start",
		LegacyUse:            "旧数据只用于抽取题材基调、世界候选、人物原型、爽点/雷点和写法经验；旧章节中发生过的事件、资源变化、人物经历、债务、死亡和关系进展默认不进入新 canon。",
		StoryStatePolicy:     "新正文事实从 world_foundation.story_start 开始，由 plan_chapter 的因果推演、draft_chapter 正文证据、check_consistency 审核和 commit_chapter 台账回填共同确认。",
		CharacterStatePolicy: "每个角色的具体经历、资源、关系、位置和性格变化随本 generation_id 的章节推演重新生成；旧角色卡只提供背景种子，不能直接继承旧章节经历。",
		ResourcePolicy:       "旧 resource_ledger/state_changes 只可作为风险清单参考；新资源必须在 initial_resource_ledger、resource_ledger 或章节 commit 中重新入账，pending 与已确认分开。",
		KnowledgePolicy:      "主角只能知道新正文里亲见、听见、通信收到、证据传回或能力授权的信息；旧章节信息不能成为主角默认记忆。",
		AllowedSeedSources: []string{
			"prompt.md", "input/", "premise.md", "outline.md", "layered_outline.md", "characters.md",
			"world_rules.md", "book_world.md", "meta/simulation_restart_policy.md", "meta/world_foundation.md",
			"meta/characters/*/dossier.md", "meta/zero_chapter_context_manifest.md", "meta/initial_character_dynamics.md",
			"meta/initial_resource_ledger.md", "meta/prewrite_storycraft_plan.md", "meta/world_background_plan.md", "relationship_state.initial.md", "foreshadow_ledger.initial.md",
		},
		ForbiddenFactSources: []string{
			"chapters/", "drafts/", "summaries/", "reviews/", "reviews_ai/", "meta/rag/", "meta/runtime/", "meta/sessions/",
			"source_project/", "experiments/", "meta/resource_ledger.*", "meta/state_changes.*", "meta/project_progress.*",
			"meta/character_continuity.*", "meta/chapter_progress.*", "旧章节 delivery_snapshots",
		},
		CanonicalStateRoots: []string{
			"meta/world_foundation.*", "meta/characters/*/dossier.*", "meta/character_stage/", "meta/side_character_journeys/",
			"meta/initial_resource_ledger.*", "meta/prewrite_storycraft_plan.*", "meta/world_background_plan.*", "relationship_state.initial.*", "foreshadow_ledger.initial.*",
			"chapters/ 与 drafts/ 中由本 generation_id 重新生成并 commit 的章节",
		},
		ResetTargets: []string{
			"meta/progress.json completed_chapters/word_count/pending_rewrites/current_chapter",
			"meta/pipeline.json 已完成阶段",
			"meta/rag/index_state.json 与 vector_store 中的旧正文/旧台账召回",
		},
		RAGPolicy: "零章和第一章推演只索引 allowed seed sources；旧正文事实、旧资源账、旧人物连续性不得进入 RAG 召回。新章节 commit 后再把新 generation 的事实沉淀回 RAG。",
		Sources:   []string{"用户重启口径", "zero-init", "world_foundation", "character_dossiers"},
	}
}

func applyZeroInitSimulationRestartState(dir string, project *zeroInitProject) error {
	st := store.NewStore(dir)
	total := len(project.Outline)
	if total <= 0 {
		total = 0
	}
	if progress, err := st.Progress.Load(); err == nil && progress != nil && progress.TotalChapters > total {
		total = progress.TotalChapters
	}
	if err := st.Progress.ResetForSimulationRestart(project.Name, total, project.GenerationID); err != nil {
		return err
	}
	state := map[string]any{
		"version":          1,
		"generation_id":    project.GenerationID,
		"mode":             "simulation_restart_from_seed",
		"applied_at":       time.Now().Format(time.RFC3339),
		"progress_policy":  "已清空活动 completed_chapters/word_count/pending_rewrites/current_chapter；旧章节文件未删除，只能作为背景种子。",
		"next_required":    "正式写作必须从第 1 章重新 plan_chapter -> draft_chapter -> check_consistency -> commit_chapter。",
		"legacy_material":  []string{"chapters/", "drafts/", "summaries/", "旧 meta 台账"},
		"canonical_source": []string{"meta/simulation_restart_policy.*", "meta/world_foundation.*", "meta/characters/*/dossier.*"},
	}
	if err := writeZeroJSON(filepath.Join(dir, "meta", "simulation_restart_state.json"), state, true); err != nil {
		return err
	}
	return writeZeroText(filepath.Join(dir, "meta", "simulation_restart_state.md"), renderZeroGenericDoc("推演重启状态", state), true)
}

func zeroInitBookWorld(project zeroInitProject) domain.BookWorld {
	first := project.FirstChapter
	cityName := zeroFirstNonEmpty(zeroKnownCityName(project), "开局城市")
	openingName := zeroFirstNonEmpty(zeroFirstScene(first), "第一章主场景")
	places := []domain.WorldPlace{
		{
			ID:          "city-core",
			Name:        cityName,
			Kind:        "city",
			Description: "本书开局城市底图；后续地点、势力、交通和规则状态都要挂到这张动态图上推进。",
			Rules:       zeroWorldRuleTexts(project.WorldRules, 4),
			Factions:    []string{"protagonist-side", "opening-pressure"},
			Tags:        []string{"zero_init", "city", "dynamic_world"},
		},
		{
			ID:          "opening-place",
			Name:        openingName,
			Kind:        "opening",
			Description: "第一章用于证明世界规则会真实施压的现场；章末必须记录地点状态变化。",
			Rules:       zeroWorldRuleTexts(project.WorldRules, 3),
			Factions:    []string{"protagonist-side", "opening-pressure"},
			Tags:        []string{"zero_init", "opening", "must_track_state_change"},
		},
		{
			ID:          "nearby-life-node",
			Name:        "第一章相邻生活节点",
			Kind:        "support",
			Description: "开局现场周边可被正文触碰的现实生活节点，如便利店、物业、小超市、医院、车站或同楼层住户；只在剧情需要时命名细化。",
			Rules:       []string{"未在正文引入前只作为路线和生活支架，不提前变成既成设定。"},
			Tags:        []string{"zero_init", "adjacent", "life_detail"},
		},
	}
	routes := []domain.WorldRoute{
		{
			From:        "city-core",
			To:          "opening-place",
			Description: "开局城市到第一章现场的现实路线；交通工具、步行、电梯、门禁和夜间限制必须按当前世界阶段计算耗时。",
			Risk:        "除非角色已获得快速移动、附身或规则授权能力，否则移动时间应接近现实世界，不允许配角随叫随到。",
		},
		{
			From:        "opening-place",
			To:          "nearby-life-node",
			Description: "第一章现场到相邻生活节点的短程路线，用于承载补给、求助、消息传递或配角迟到/缺席的时间成本。",
			Risk:        "路线状态会随章节推进改变；关闭、污染、被占用或新增规则都要回填 book_world/timeline。",
		},
	}
	factions := []domain.WorldFaction{{
		ID:        "protagonist-side",
		Name:      zeroFirstNonEmpty(zeroProtagonist(project.Characters).Name, "主角方"),
		Goal:      "在第一章建立可持续行动目标，并让读者看见其最低证据标准和行动边界。",
		Resources: []string{"角色经验", "有限信息", "第一章现场证据"},
		Tags:      []string{"zero_init"},
	}}
	factions = append(factions, domain.WorldFaction{
		ID:        "opening-pressure",
		Name:      zeroOpeningPressureName(project),
		Goal:      "在第一章通过可见规则、物件或空间边界迫使主角做选择。",
		Resources: []string{"现场压力", "规则反馈", "信息差"},
		Tags:      []string{"zero_init", "pressure"},
	})
	return domain.BookWorld{
		Version:  1,
		Name:     project.Name,
		Summary:  "由 premise、world_rules、characters 和第一章大纲自动生成的零章动态世界资产；需在 architect 或人工确认后继续细化城市地图、地点状态、路线耗时和势力变化。",
		Places:   places,
		Routes:   routes,
		Factions: factions,
		MapNotes: []string{
			"第一章只展示会改变选择的规则和环境证据，不提前解释全书谜底。",
			"地点/物件必须承担信息、压力或状态变化，不能只做氛围背景。",
			"所有角色在正文引入后都要绑定当前位置、行动目标和可用交通；跨地点见面必须服从路线与耗时。",
			"新地点、新势力、路线封锁、角色死亡/失踪/转移都应在 commit 或审核后同步回填 book_world、timeline、character_stage 或 resource_ledger。",
		},
		LastSyncedAt: project.GeneratedAt,
	}
}

func zeroKnownCityName(project zeroInitProject) string {
	text := strings.Join([]string{
		project.Name,
		project.Premise,
		project.FirstChapter.Title,
		project.FirstChapter.CoreEvent,
		project.FirstChapter.Hook,
		strings.Join(project.FirstChapter.Scenes, "\n"),
	}, "\n")
	for _, rule := range project.WorldRules {
		text += "\n" + rule.Category + "\n" + rule.Rule + "\n" + rule.Boundary
	}
	for _, candidate := range []string{"北城", "南城", "东城", "西城", "旧城", "新城", "江城", "海城", "澄港"} {
		if strings.Contains(text, candidate) {
			return candidate
		}
	}
	return ""
}

func zeroInitWorldFoundation(project zeroInitProject) domain.WorldFoundation {
	scene := zeroFirstNonEmpty(zeroFirstScene(project.FirstChapter), "第一章主场景")
	laws := make([]domain.WorldIronLaw, 0, len(project.WorldRules)+5)
	for i, rule := range project.WorldRules {
		name := zeroFirstNonEmpty(rule.Category, fmt.Sprintf("world-rule-%02d", i+1))
		laws = append(laws, domain.WorldIronLaw{
			ID:        fmt.Sprintf("foundation-rule-%02d", i+1),
			Name:      name,
			Rule:      zeroFirstNonEmpty(rule.Rule, "世界规则未写明，正式写作前需补足。"),
			Boundary:  zeroFirstNonEmpty(rule.Boundary, "没有正文证据和台账更新前不得突破。"),
			Evidence:  "world_rules.json",
			AppliesTo: []string{"all_characters", "all_locations"},
		})
	}
	laws = append(laws,
		domain.WorldIronLaw{
			ID:        "foundation-knowledge-boundary",
			Name:      "主角信息边界",
			Rule:      "正文主视角只能呈现主角看见、听见、接到通信或通过证据推断的信息；配角时间线不等于主角已知事实。",
			Boundary:  "没有通信、证据、目击者、账单、台账传回或明确能力时，主角不能知道配角经历。",
			Evidence:  "zero-init policy",
			AppliesTo: []string{"protagonist", "side_characters"},
		},
		domain.WorldIronLaw{
			ID:        "foundation-travel-time",
			Name:      "现实交通耗时",
			Rule:      "角色跨地点移动默认按 book_world 路线和现实城市耗时计算。",
			Boundary:  "除非角色获得快速移动、附身、传送凭证或规则授权，不得瞬移、随叫随到或无成本赶场。",
			Evidence:  "book_world.routes",
			AppliesTo: []string{"all_characters"},
		},
		domain.WorldIronLaw{
			ID:        "foundation-independent-npc",
			Name:      "非主角独立推进",
			Rule:      "非主角有自己的生活、工作、资源、恐惧和判断，章节未描写时仍按其位置和压力推进。",
			Boundary:  "配角不能只作为推动主角事件的工具；新角色若在配角线出现，必须记录相识来源和后续台账。",
			Evidence:  "character_dossiers + side_character_journeys",
			AppliesTo: []string{"side_characters"},
		},
		domain.WorldIronLaw{
			ID:        "foundation-resource-ledger",
			Name:      "资源入账",
			Rule:      "资产、欠条、能力、交通凭证、通信能力和关键证据必须先入账或标记 pending，正文才能当事实使用。",
			Boundary:  "未入账资源不得解决冲突；pending 资源必须保留对价、风险或审核尾巴。",
			Evidence:  "resource_ledger",
			AppliesTo: []string{"resources", "transactions"},
		},
		domain.WorldIronLaw{
			ID:        "foundation-rule-change",
			Name:      "规则改变必须有证据",
			Rule:      "所有人的决策都能改变世界状态，但只能改变已触发、已支付代价、已获得权限或已被台账确认的局部状态。",
			Boundary:  "没有明确触发、代价、权限和回填目标时，不能改写世界铁律。",
			Evidence:  "timeline + state_changes + book_world",
			AppliesTo: []string{"all_characters", "world_rules"},
		},
	)
	var baselines []domain.LocationBaseline
	if project.BookWorld != nil {
		for _, place := range project.BookWorld.Places {
			baselines = append(baselines, domain.LocationBaseline{
				ID:            place.ID,
				Name:          place.Name,
				StatusAtStart: zeroFirstNonEmpty(place.Description, "故事开始前状态未细化，首次进入正文前需补足。"),
				OpenQuestions: place.Rules,
				UpdatePolicy:  "地点状态、规则封锁、死亡/失踪、资产确权或交通变化必须回填 book_world/timeline/character_stage。",
			})
		}
	}
	if len(baselines) == 0 {
		baselines = append(baselines, domain.LocationBaseline{
			ID:            "opening-place",
			Name:          scene,
			StatusAtStart: "第一章开局地点；状态变化必须在章末回填。",
			UpdatePolicy:  "每章若地点状态改变，必须更新 book_world 或 timeline。",
		})
	}
	past := []domain.PastTimelineEvent{
		{
			Time:             "故事开始前",
			Event:            "世界按 foundation 规则运行，角色只拥有角色卡和台账中已经确认的资源与信息。",
			Locations:        []string{scene},
			Participants:     zeroCharacterNames(project.Characters),
			Consequences:     []string{"未写入正文的配角经历仍受位置、资源、通信和交通限制", "主角默认不知道配角线"},
			ProtagonistKnows: false,
			Source:           "zero-init",
		},
	}
	return domain.WorldFoundation{
		Version: 1,
		Project: project.Name,
		StoryStart: domain.StoryStart{
			AbsoluteTime: "第一章开场；若项目有具体年月日/时刻，正式推演前补写为绝对时间。",
			StoryClock:   "chapter-001-start",
			Location:     scene,
			Description:  zeroFirstNonEmpty(project.FirstChapter.CoreEvent, project.FirstChapter.Title, "故事从第一章核心事件开始。"),
		},
		IronLaws:             laws,
		RuleChangeConditions: zeroRuleChangeConditions(laws),
		PastTimeline:         past,
		CityBaseline:         baselines,
		KnowledgePolicy:      "主角视角不是世界全知视角；RAG 可召回世界后台和配角档案，但正文只能通过通信、证据、目击、账单或能力把信息传给主角。",
		GeneratedAt:          project.GeneratedAt,
		Sources:              []string{"premise.md", "world_rules.json", "book_world.json", "characters.json", "outline.json"},
	}
}

func zeroRuleChangeConditions(laws []domain.WorldIronLaw) []domain.RuleChangeCondition {
	var out []domain.RuleChangeCondition
	for _, law := range laws {
		out = append(out, domain.RuleChangeCondition{
			RuleID:        law.ID,
			AllowedBy:     []string{"明确能力", "交易凭证", "账单/合同确认", "章节事件后果", "审阅通过后的状态回填"},
			ProofNeeded:   "正文可见触发 + 资源/关系/状态代价 + 对应 ledger 更新",
			UpdateTargets: []string{"world_foundation(仅新增例外说明)", "book_world", "timeline", "state_changes", "resource_ledger", "character_dossiers", "side_character_journeys"},
		})
	}
	return out
}

func zeroInitCharacterDossiers(project zeroInitProject) []domain.CharacterDossier {
	var out []domain.CharacterDossier
	protagonist := zeroProtagonist(project.Characters)
	for _, c := range project.Characters {
		name := strings.TrimSpace(c.Name)
		if name == "" {
			continue
		}
		firstMention := project.FirstMentions[name]
		isProtagonist := strings.TrimSpace(protagonist.Name) == name
		location := zeroFirstNonEmpty(zeroFirstScene(project.FirstChapter), "故事开局地点待补")
		if !project.FirstCast[name] && !isProtagonist {
			location = "故事开始前未进入主角视角；正式出场前按角色卡和交通规则补足位置"
		}
		counterpart := zeroCounterpartForCharacter(project, c)
		relationships := []domain.CharacterRelationNote{}
		if counterpart != "" {
			relationships = append(relationships, domain.CharacterRelationNote{
				Other:              counterpart,
				HowMet:             "零章根据角色卡/大纲推导，正式正文若建立新关系必须补相识场景。",
				CurrentTie:         "试探/未结盟基线",
				DebtOrTrust:        "未新增正文债务或信任",
				KnownToProtagonist: isProtagonist,
			})
		}
		out = append(out, domain.CharacterDossier{
			Version:   1,
			Character: name,
			Role:      c.Role,
			Tier:      c.Tier,
			Aliases:   c.Aliases,
			Profile: domain.CharacterDossierProfile{
				Description: zeroFirstNonEmpty(c.Description, "角色卡未写明，正式引入前必须补足。"),
				Backstory:   "故事开始前背景只允许来自角色卡、大纲、世界规则或后续补档；未知处标记为信息缺口，不直接编成正文事实。",
				Traits:      c.Traits,
				Arc:         zeroFirstNonEmpty(c.Arc, "从静态身份进入可追踪选择。"),
				Desires:     []string{"在开局压力中保住自己的目标、资源或关系边界。"},
				Fears:       []string{"失去关键资源、身份、关系或安全边界。"},
				Boundaries:  []string{"不能知道未通信/未见证/未入账的信息", "不能为推动主角而瞬间到场"},
			},
			LifeAnchors: []domain.LifeAnchor{
				{
					Kind:        "开局位置",
					Place:       location,
					Schedule:    "故事开始时",
					Obligation:  zeroFirstNonEmpty(c.Role, "按自身身份承担开局压力"),
					TravelNotes: "跨地点行动必须服从 book_world 路线与现实耗时。",
				},
			},
			PreStoryTimeline: []domain.CharacterPastEvent{
				{
					Time:               "故事开始前",
					Event:              zeroFirstNonEmpty(c.Description, c.Role, "角色背景待补"),
					Location:           location,
					PeopleMet:          zeroOptionalPeople(counterpart),
					Relationship:       "零章基线；正文发生新相识必须写入角色档案或台账。",
					Consequence:        "形成开局目标、压力和知识边界。",
					KnownToProtagonist: isProtagonist,
				},
			},
			Resources: []domain.CharacterResource{
				{
					ID:       "baseline-experience",
					Name:     "故事开始前经验/身份资源",
					Kind:     "experience",
					Status:   "baseline",
					Risk:     "只能解释行动倾向，不能无代价解决本章冲突。",
					Evidence: "characters.json",
				},
			},
			Relationships: relationships,
			CommunicationBoundary: domain.CommunicationBoundary{
				CanContactProtagonist: isProtagonist || project.FirstCast[name],
				Channels:              []string{"同场对话", "电话/消息(若世界阶段允许)", "物件/账单/目击者传回"},
				Delay:                 "默认存在现实延迟；无通信或证据时主角不知道该角色线。",
				FailureModes:          []string{"无信号", "规则干扰", "角色被困", "交通耗时", "信息未传回"},
				InfoAllowed:           "只能传递该角色知道且有渠道传出的信息。",
			},
			KnowledgeBoundary: "只知道角色卡、自己经历、通信中获得的信息；不知道主角内心、后台规则和其他配角未传回的时间线。",
			DecisionModel:     "按自身目标、恐惧、资源、关系和现场证据选择，不为主角工具化。",
			CurrentAtStoryStart: domain.CharacterStartState{
				Time:                "第一章开场",
				Location:            location,
				Status:              "存活/状态待正文确认",
				CurrentAction:       "按自身开局目标行动或被自身场景压力困住。",
				Pressure:            zeroFirstNonEmpty(project.FirstChapter.CoreEvent, "第一章开局压力"),
				NextIndependentMove: zeroNextIndependentMove(c, firstMention, project),
			},
			RAGHints: []string{
				"检索该角色时优先读取本 dossier、character_stage、side_character_journeys 和 relationship/resource ledger。",
				"主角未通信时不能把本档案内容直接写成主角已知。",
			},
			GeneratedAt: project.GeneratedAt,
			Sources:     []string{"characters.json", "outline.json", "book_world.json", "world_rules.json"},
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Character < out[j].Character })
	return out
}

func zeroCharacterNames(chars []domain.Character) []string {
	var out []string
	for _, c := range chars {
		if strings.TrimSpace(c.Name) != "" {
			out = append(out, strings.TrimSpace(c.Name))
		}
	}
	return out
}

func zeroOptionalPeople(name string) []string {
	if strings.TrimSpace(name) == "" {
		return nil
	}
	return []string{strings.TrimSpace(name)}
}

func zeroNextIndependentMove(c domain.Character, firstMention int, project zeroInitProject) string {
	if zeroIsProtagonist(c) || firstMention == 1 {
		return "进入第一章现场选择，并在章末产生可回填状态变化。"
	}
	if firstMention > 1 {
		return fmt.Sprintf("第%d章前保持自身位置/职责/风险推进，不提前抢主线。", firstMention)
	}
	return "保持背景职责和资源压力；正式引入前补位置、资源、关系和通信边界。"
}

// writeZeroWorldSimAssets 生成世界模拟（离屏推演）的初始化四件套：
// LOD 分层名单、离屏角色初始日程、故事日历骨架、世界 tick 零点。
// 补齐 Generative Agents 三支柱中"世界几点了"与"大家都在忙什么"两根：
// 种子记忆（initial_character_dynamics）已有，此处补日程与时钟基线。
func writeZeroWorldSimAssets(dir string, project zeroInitProject, overwrite bool) error {
	st := store.NewStore(dir)

	// LOD 分层：按角色卡 Tier 初始指派（写作推进后由世界 tick 升降级）。
	if zeroShouldWriteArtifact(dir, overwrite, "meta/simulation_tiers.json") && len(project.Characters) > 0 {
		var cast domain.SimulationCast
		for _, c := range project.Characters {
			cast = cast.Upsert(domain.TierAssignment{
				Name:   c.Name,
				Tier:   domain.SuggestSimulationTier(c, 0),
				Reason: "zero-init 按角色卡 tier 初始指派",
			})
		}
		if err := st.WorldSim.SaveSimulationCast(cast); err != nil {
			return err
		}
	}

	// 离屏日程：非主角圈角色开局就有自己的事做（目标从其独立动线派生），
	// 首次世界推演时由 GM 校准，避免第一弧之前离屏世界空白。
	if zeroShouldWriteArtifact(dir, overwrite, "meta/offscreen_agenda.json") && len(project.Characters) > 0 {
		var ledger domain.OffscreenAgendaLedger
		for _, c := range project.Characters {
			tier := domain.SuggestSimulationTier(c, 0)
			if tier == domain.TierProtagonistCircle {
				continue
			}
			goal := strings.TrimSpace(zeroNextIndependentMove(c, project.FirstMentions[c.Name], project))
			if goal == "" {
				goal = "按其人物弧线推进自身事务"
			}
			ledger = ledger.Upsert(domain.CharacterAgenda{
				Name:        c.Name,
				Tier:        string(tier),
				CurrentGoal: goal,
				Motivation:  "zero-init 由人物卡派生；首次世界推演（save_world_tick）时校准",
				Status:      "active",
			})
		}
		if len(ledger.Agendas) > 0 {
			if err := st.WorldSim.SaveAgendaLedger(ledger); err != nil {
				return err
			}
		}
	}

	// 故事日历骨架：世界时钟基线。era/start_date/season 由 Architect 初始规划校准。
	if zeroShouldWriteArtifact(dir, overwrite, "meta/story_calendar.json") {
		if err := st.WorldSim.SaveStoryCalendar(domain.StoryCalendar{
			DaysPerChapter: 2,
			Notes: []string{
				"zero-init 默认骨架：era / start_date / season_at_start 由 Architect 初始规划时校准",
				"days_per_chapter 默认 2 天/章，按题材节奏调整（战斗弧更密、旅途弧更疏）",
			},
		}); err != nil {
			return err
		}
	}

	// 事件编织骨架（Task 078）：agenda 目标各立一事件（线索=角色线），编织行留给
	// Architect 卷边界排期；工件缺失时 plan/commit 消费方零影响。
	if zeroShouldWriteArtifact(dir, overwrite, "meta/event_weave.json") {
		if ledger, err := st.WorldSim.LoadAgendaLedger(); err == nil && len(ledger.Agendas) > 0 {
			var weave domain.EventWeave
			for i, a := range ledger.Agendas {
				weave.Events = append(weave.Events, domain.WeaveEvent{
					ID:           fmt.Sprintf("ev-%03d", i+1),
					Thread:       a.Name + "线",
					Summary:      a.CurrentGoal,
					Participants: []string{a.Name},
					WindowFrom:   1,
					WindowTo:     12,
					Status:       "planned",
				})
			}
			if err := st.WorldSim.SaveEventWeave(weave); err != nil {
				return err
			}
		}
	}

	// 世界 tick 零点：让首次弧边界推演有游标基准。
	if zeroShouldWriteArtifact(dir, overwrite, "meta/world_tick.json") {
		if err := st.WorldSim.SaveTick(domain.WorldTick{TickID: "v0-a0", ThroughChapter: 0}); err != nil {
			return err
		}
	}
	return nil
}

// writeZeroMethodologyArtifacts Task 056：把 world_background_plan 里的世界背景层
// 派生为权威方法论工件（第一批 5 个），生产/消费两头打通——此前这些数据只活在
// 计划文档里，MethodologyStore 的 Save* 无任何调用方。
// 派生不出的字段留零值（omitempty），不编造。
// batch-10（Task 081）补齐剩余 5 个：cosmology / crowd_life / ecological_map /
// cultural_footnotes / pacing_contract（见 writeZeroMethodologyArtifactsBatch10）。
func writeZeroMethodologyArtifacts(dir string, project zeroInitProject, plan zeroWorldBackgroundPlan, overwrite bool) error {
	st := store.NewStore(dir)

	// info_graph ← 信息差层（reader/主角/角色三视角，零章基线快照）。
	if zeroShouldWriteArtifact(dir, overwrite, "meta/info_graph.json") && len(plan.InformationLedger) > 0 {
		protagonist := strings.TrimSpace(zeroProtagonist(project.Characters).Name)
		graph := domain.InfoGraph{Chapter: 0}
		seen := map[string]int{}
		addNode := func(id, kind string) int {
			if idx, ok := seen[id]; ok {
				return idx
			}
			graph.Nodes = append(graph.Nodes, domain.InfoNode{ID: id, Type: kind})
			seen[id] = len(graph.Nodes) - 1
			return len(graph.Nodes) - 1
		}
		reader := addNode("reader", "reader")
		var protagonistIdx = -1
		if protagonist != "" {
			protagonistIdx = addNode(protagonist, "character")
		}
		for _, rec := range plan.InformationLedger {
			graph.Nodes[reader].Knows = append(graph.Nodes[reader].Knows, rec.ReaderKnows...)
			if protagonistIdx >= 0 {
				graph.Nodes[protagonistIdx].Knows = append(graph.Nodes[protagonistIdx].Knows, rec.ProtagonistKnows...)
				graph.Nodes[protagonistIdx].MustNotKnowYet = append(graph.Nodes[protagonistIdx].MustNotKnowYet, rec.HiddenFromReader...)
			}
			subject := strings.TrimSpace(rec.Subject)
			if subject == "" || subject == protagonist {
				continue
			}
			idx := addNode(subject, "character")
			graph.Nodes[idx].Knows = append(graph.Nodes[idx].Knows, rec.CharacterKnows...)
			graph.Nodes[idx].Believes = append(graph.Nodes[idx].Believes, rec.CharacterMistakes...)
		}
		if err := st.Methodology.SaveInfoGraph(graph); err != nil {
			return err
		}
	}

	// social_mood ← 谣言/社会情绪层。数值字段无法定量，留零值不编造。
	if zeroShouldWriteArtifact(dir, overwrite, "meta/social_mood.json") && len(plan.SocialMoodRumors) > 0 {
		mood := domain.SocialMood{Mood: strings.TrimSpace(plan.SocialMoodRumors[0].Mood)}
		for _, r := range plan.SocialMoodRumors {
			text := strings.TrimSpace(r.Rumor)
			if text == "" {
				continue
			}
			mood.Rumors = append(mood.Rumors, domain.Rumor{Text: text, SourceFaction: strings.TrimSpace(r.Source)})
		}
		if mood.Mood != "" {
			if err := st.Methodology.SaveSocialMood(mood); err != nil {
				return err
			}
		}
	}

	// ritual_calendar ← 仪式日历层。
	if zeroShouldWriteArtifact(dir, overwrite, "meta/ritual_calendar.json") && len(plan.RitualCalendar) > 0 {
		var cal domain.RitualCalendar
		for _, w := range plan.RitualCalendar {
			name := strings.TrimSpace(w.RitualOrDeadline)
			date := strings.TrimSpace(w.Time)
			if name == "" || date == "" {
				continue
			}
			cal.Annual = append(cal.Annual, domain.RitualEvent{
				Name:           name,
				Date:           date,
				BehaviorChange: strings.TrimSpace(w.PracticalConstraint),
				NarrativeUse:   compactNonEmpty(w.SceneUse, w.MissedCost),
			})
		}
		if len(cal.Annual) > 0 {
			if err := st.Methodology.SaveRitualCalendar(cal); err != nil {
				return err
			}
		}
	}

	// physics_axioms ← 宇宙观/物理规则层（与 world_rules 硬规则对齐，先落 Notes）。
	if zeroShouldWriteArtifact(dir, overwrite, "meta/physics_axioms.json") && len(plan.CosmologyChecks) > 0 {
		var axioms domain.PhysicsAxioms
		for _, c := range plan.CosmologyChecks {
			rule := strings.TrimSpace(c.Rule)
			if rule == "" {
				continue
			}
			note := rule
			if cost := strings.TrimSpace(c.Cost); cost != "" {
				note += "；代价：" + cost
			}
			if boundary := strings.TrimSpace(c.Boundary); boundary != "" {
				note += "；边界：" + boundary
			}
			axioms.Notes = append(axioms.Notes, note)
		}
		if !axioms.IsEmpty() {
			if err := st.Methodology.SavePhysicsAxioms(axioms); err != nil {
				return err
			}
		}
	}

	// moral_ceiling ← premise 禁区段 + world_rules 道德边界。
	if zeroShouldWriteArtifact(dir, overwrite, "meta/moral_ceiling.json") {
		var taboo []string
		seen := map[string]bool{}
		addTaboo := func(s string) {
			s = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(s), "-*• "))
			if s == "" || seen[s] || len(taboo) >= 8 {
				return
			}
			seen[s] = true
			taboo = append(taboo, s)
		}
		inTaboo := false
		for line := range strings.SplitSeq(project.Premise, "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "#") {
				inTaboo = strings.Contains(trimmed, "禁区")
				continue
			}
			if inTaboo && trimmed != "" {
				addTaboo(trimmed)
			}
		}
		for _, r := range project.WorldRules {
			if strings.TrimSpace(r.Boundary) != "" && (r.Category == "society" || r.Category == "other" || strings.Contains(r.Boundary, "不")) {
				addTaboo(r.Boundary)
			}
		}
		if len(taboo) > 0 {
			if err := st.Methodology.SaveMoralCeiling(domain.MoralCeiling{TabooZones: taboo}); err != nil {
				return err
			}
		}
	}
	return nil
}

// compactNonEmpty 收集非空字符串（派生辅助）。
func compactNonEmpty(values ...string) []string {
	var out []string
	for _, v := range values {
		if s := strings.TrimSpace(v); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// writeZeroMethodologyArtifactsBatch10 Task 081：补齐 08 遗留的 5 个方法论工件。
// 派生不出的字段留零值，不编造；尊重 overwrite 语义。
func writeZeroMethodologyArtifactsBatch10(dir string, project zeroInitProject, plan zeroWorldBackgroundPlan, overwrite bool) error {
	st := store.NewStore(dir)

	// cosmology ← 宇宙观规则层（layer 文本映射 category）。
	if zeroShouldWriteArtifact(dir, overwrite, "meta/cosmology.json") && len(plan.CosmologyChecks) > 0 {
		var cos domain.Cosmology
		for i, c := range plan.CosmologyChecks {
			rule := strings.TrimSpace(c.Rule)
			if rule == "" {
				continue
			}
			cat := "physics"
			switch {
			case strings.Contains(c.Layer, "因果"):
				cat = "causality"
			case strings.Contains(c.Layer, "存在") || strings.Contains(c.Layer, "灵界") || strings.Contains(c.Layer, "冥"):
				cat = "existence"
			case strings.Contains(c.Layer, "命运"):
				cat = "fate"
			case strings.Contains(c.Layer, "知识") || strings.Contains(c.Layer, "垄断"):
				cat = "knowledge"
			}
			cos.Axioms = append(cos.Axioms, domain.CosmologyAxiom{
				ID:       fmt.Sprintf("ax-%02d", i+1),
				Name:     compactProgressTextZero(rule, 20),
				Rule:     rule,
				Category: cat,
				Note:     strings.TrimSpace(c.Cost),
			})
		}
		if len(cos.Axioms) > 0 {
			if err := st.Methodology.SaveCosmology(cos); err != nil {
				return err
			}
		}
	}

	// crowd_life ← 离屏日程账本（07 已生成）派生 NPC 生活循环骨架。
	if zeroShouldWriteArtifact(dir, overwrite, "meta/crowd_life.json") {
		if ledger, err := st.WorldSim.LoadAgendaLedger(); err == nil && len(ledger.Agendas) > 0 {
			var eco domain.CrowdLifeEcosystem
			for _, a := range ledger.Agendas {
				eco.NPCs = append(eco.NPCs, domain.NPCSchedule{NPCID: a.Name, Goals: []string{a.CurrentGoal}})
			}
			if err := st.Methodology.SaveCrowdLife(eco); err != nil {
				return err
			}
		}
	}

	// ecological_map ← book_world places 浅派生（kind 即生境提示，细节留空）。
	if zeroShouldWriteArtifact(dir, overwrite, "meta/ecological_map.json") && project.BookWorld != nil && len(project.BookWorld.Places) > 0 {
		var m domain.EcologicalMap
		for _, pl := range project.BookWorld.Places {
			if pl.ID == "" || pl.Name == "" {
				continue
			}
			m.Ecosystems = append(m.Ecosystems, domain.Ecosystem{ID: pl.ID, Name: pl.Name, Climate: strings.TrimSpace(pl.Kind)})
		}
		if len(m.Ecosystems) > 0 {
			if err := st.Methodology.SaveEcologicalMap(m); err != nil {
				return err
			}
		}
	}

	// cultural_footnotes ← 仪式日历层的社会含义。
	if zeroShouldWriteArtifact(dir, overwrite, "meta/cultural_footnotes.json") && len(plan.RitualCalendar) > 0 {
		var cf domain.CulturalFootnotes
		for _, w := range plan.RitualCalendar {
			term := strings.TrimSpace(w.RitualOrDeadline)
			load := strings.TrimSpace(w.SocialMeaning)
			if term == "" || load == "" {
				continue
			}
			cf.Footnotes = append(cf.Footnotes, domain.CulturalFootnote{Term: term, CulturalLoad: load})
		}
		if len(cf.Footnotes) > 0 {
			if err := st.Methodology.SaveCulturalFootnotes(cf); err != nil {
				return err
			}
		}
	}

	// pacing_contract ← premise 题材启发选内置预设（网文口径默认 qidian_dushi）。
	if zeroShouldWriteArtifact(dir, overwrite, "meta/pacing_contract.json") {
		preset := "qidian_dushi"
		switch {
		case strings.Contains(project.Premise, "玄幻") || strings.Contains(project.Premise, "修仙"):
			preset = "qidian_xuanhuan"
		case strings.Contains(project.Premise, "晋江") || strings.Contains(project.Premise, "言情"):
			preset = "jinjiang"
		case strings.Contains(project.Premise, "豆瓣"):
			preset = "douban"
		}
		if contract, ok := domain.PacingPreset(preset); ok {
			if err := st.Methodology.SavePacingContract(contract); err != nil {
				return err
			}
		}
	}
	return nil
}

// compactProgressTextZero 截断辅助（zero-init 侧）。
func compactProgressTextZero(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
