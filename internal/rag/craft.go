package rag

import (
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

// craft 检索通道：写作手法库服务两类场景。
// 1. 设计取料：零章初始化、新角色/新武器/新能力首次出场的章计划。检索结果立刻
//    实例化成本书事实（dossier、props、visual_design、world_codex）。
// 2. 写法取料：草稿/重写阶段检索场景处理、对白摩擦、信息延迟、段落节奏和留存手法。
//    这类结果只能当表达方法，不能覆盖角色现状、资源状态和本书事实。
//
// novel_context 的常规事实召回仍排除 craft/benchmark chunk，避免方法库被误写成 canon；
// 写作阶段要通过 craft_recall 显式调用并留下审计日志。
//
// 路由是确定性的：每个设计字段绑定固定的类目 filter（集合运算，命中与否确定），
// BM25 只负责在命中子集内排序。查不到 = 显式 no_material，可见、可审计。

// CraftSourceKind 写作手法库 chunk 的 source_kind 标记。
const CraftSourceKind = "craft_technique"

// CalibrationSourceKind 审核校准库（review-calibration）chunk 的 source_kind 标记。
const CalibrationSourceKind = "calibration_reference"

// BenchmarkSourceKind 对标素材库（novel_all）chunk 的 source_kind 标记。
// 对标素材只可迁移手法/结构/节奏，禁止照搬情节、人名与专有设定。
const BenchmarkSourceKind = "benchmark_reference"

// IsDesignOnlySourceKind 判断某 source_kind 是否属于"不能作为常规事实召回"的库：
// novel_context 必须排除这些 chunk；若写作/重写要用其中的技法，必须显式调用 craft_recall。
func IsDesignOnlySourceKind(kind string) bool {
	kind = strings.TrimSpace(kind)
	return strings.EqualFold(kind, CraftSourceKind) ||
		strings.EqualFold(kind, BenchmarkSourceKind) ||
		strings.EqualFold(kind, CalibrationSourceKind)
}

// BenchmarkCategory 从 novel_all 路径推导类目（剥掉编号前缀）。
// 例：deconstruction-library/novel_all/03-题材与套路/xx.md → "题材与套路"。
func BenchmarkCategory(path string) string {
	clean := filepath.ToSlash(strings.TrimSpace(path))
	segments := strings.Split(clean, "/")
	for i, segment := range segments {
		if !strings.EqualFold(strings.TrimSpace(segment), benchmarkLibrarySegment) {
			continue
		}
		if i+1 >= len(segments)-1 { // 库根下的散文件（如 INDEX.md）
			return "总索引"
		}
		return normalizeBenchmarkCategory(segments[i+1])
	}
	return ""
}

func normalizeBenchmarkCategory(dir string) string {
	dir = strings.TrimSpace(dir)
	if idx := strings.Index(dir, "-"); idx >= 0 && idx <= 3 {
		dir = dir[idx+1:]
	}
	return dir
}

// CraftCategory 从路径推导手法库类目与子类目。
// 返回 ("", "") 表示不是手法库路径。
// 例：deconstruction-library/writing-techniques/appearance/eyes/描写.md → ("appearance", "eyes")。
func CraftCategory(path string) (category, subcategory string) {
	clean := filepath.ToSlash(strings.TrimSpace(path))
	segments := strings.Split(clean, "/")
	for i, segment := range segments {
		if !strings.EqualFold(strings.TrimSpace(segment), craftTechniqueSegment) {
			continue
		}
		if i+1 < len(segments)-1 { // 下一段是目录（不是文件名）
			category = strings.ToLower(strings.TrimSpace(segments[i+1]))
		}
		if i+2 < len(segments)-1 {
			subcategory = strings.ToLower(strings.TrimSpace(segments[i+2]))
		}
		return category, subcategory
	}
	return "", ""
}

// CraftDesignField 设计字段（plan/零章的产出槽位）。字段与检索类目的绑定见
// craftFieldRecipes——绑定关系是引擎级契约，不允许自由文本路由。
type CraftDesignField string

const (
	CraftFieldAppearance  CraftDesignField = "appearance"  // 外貌/穿着 → visual_design
	CraftFieldDialogue    CraftDesignField = "dialogue"    // 对白/交涉 → dialogue_scene_blueprints
	CraftFieldWeapon      CraftDesignField = "weapon"      // 武器 → character_kit.weapons / props
	CraftFieldEquipment   CraftDesignField = "equipment"   // 装备/法宝 → character_kit.equipment
	CraftFieldAbility     CraftDesignField = "ability"     // 能力分级/体系 → world_codex.ability_tiers
	CraftFieldSkill       CraftDesignField = "skill"       // 技能/法术/阵法 → character_kit.skills
	CraftFieldInstitution CraftDesignField = "institution" // 制度/官职/物价/民俗 → world_codex/grounding
	CraftFieldTechnology  CraftDesignField = "technology"  // 科幻科技/星际 → world_codex.technology
	CraftFieldCosmology   CraftDesignField = "cosmology"   // 世界构成/位面 → world_codex.morphology
	CraftFieldMethodology CraftDesignField = "methodology" // 章节技法/创作方法论 → plan craft 参考

	// benchmark（novel_all 对标素材库）侧字段：只迁移手法/结构，禁止照搬情节与人名。
	CraftFieldOutlineSample CraftDesignField = "outline_sample"     // 大纲模板与示例
	CraftFieldTrope         CraftDesignField = "trope"              // 题材与套路
	CraftFieldPersona       CraftDesignField = "persona"            // 人设与角色
	CraftFieldLexicon       CraftDesignField = "lexicon"            // 素材与描写词汇
	CraftFieldPlotBeats     CraftDesignField = "plot_beats"         // 爽点与剧情钩子
	CraftFieldBenchmark     CraftDesignField = "benchmark_analysis" // 拆文分析（严禁照搬情节）
	CraftFieldMarket        CraftDesignField = "market"             // 运营与平台
	CraftFieldSceneCraft    CraftDesignField = "scene_situation"    // 场景与情境
)

// craftFieldRecipe 一个设计字段的确定性检索配方。
type craftFieldRecipe struct {
	Categories    []string     // 命中类目集合（craft_category ∈ Categories，目录级）
	Facets        []CraftFacet // 命中内容级细分面集合（craft_facet ∈ Facets，跨目录内容级）
	Subcategories []string     // 可选子类目过滤（空 = 不限）
	SourceKinds   []string     // 命中库集合；空 = 仅 craft_technique
	Description   string
	Benchmark     bool // 含对标素材：产出只可迁移手法/结构，须登记 external_reference_plan
}

// craftFieldRecipes 字段 ↔ 检索配方绑定表（确定性路由核心）。
var craftFieldRecipes = map[CraftDesignField]craftFieldRecipe{
	CraftFieldAppearance: {Categories: []string{"appearance"}, Facets: []CraftFacet{FacetAppearance, FacetEmotion},
		SourceKinds: []string{CraftSourceKind, BenchmarkSourceKind}, Benchmark: true,
		Description: "外貌/五官/神态/服饰/发型/心理/动作描写词库（跨库按内容取）"},
	// dialogue：原库无对白目录，靠内容级 craft_facet 跨库检索对白/交涉/台词技法。
	CraftFieldDialogue: {Facets: []CraftFacet{FacetDialogue},
		SourceKinds: []string{CraftSourceKind, BenchmarkSourceKind}, Benchmark: true,
		Description: "对白/对话/交涉/台词/信息博弈技法"},
	CraftFieldWeapon:      {Categories: []string{"weapons", "ancient-history"}, Description: "冷兵器/名刀名剑/法宝神器/古代武器"},
	CraftFieldEquipment:   {Categories: []string{"weapons", "magic-arts"}, Description: "法宝/装备/炼金产物"},
	CraftFieldAbility:     {Categories: []string{"fantasy", "magic-arts", "scifi"}, Description: "阶位划分/神格/超凡体系分级"},
	CraftFieldSkill:       {Categories: []string{"magic-arts"}, Description: "法术/阵法/方术/雷法/炼金"},
	CraftFieldInstitution: {Categories: []string{"ancient-history"}, Description: "官职/爵位/兵制/物价/婚嫁/民俗"},
	CraftFieldTechnology:  {Categories: []string{"scifi"}, Description: "星际武器/太空作战/科幻分类"},
	CraftFieldCosmology: {Categories: []string{"fantasy", "scifi", "magic-arts", "心理学与世界观"},
		SourceKinds: []string{CraftSourceKind, BenchmarkSourceKind}, Benchmark: true,
		Description: "世界构成/位面/晶壁系/宇宙观/角色心理学"},
	CraftFieldMethodology: {Categories: []string{"novel-craft-methodology", "教程方法论", "文笔提升与书单"},
		SourceKinds: []string{CraftSourceKind, BenchmarkSourceKind}, Benchmark: true,
		Description: "角色塑造/事件冲突/叙事节奏/世界构建方法论/网文教程/文笔提升"},

	// benchmark（novel_all）侧配方
	CraftFieldOutlineSample: {Categories: []string{"大纲模板与示例"}, SourceKinds: []string{BenchmarkSourceKind}, Benchmark: true,
		Description: "大纲模板/填表法/编辑视角的好大纲/细纲示例"},
	CraftFieldTrope: {Categories: []string{"题材与套路"}, SourceKinds: []string{BenchmarkSourceKind}, Benchmark: true,
		Description: "题材选型/流派套路/桥段模式"},
	CraftFieldPersona: {Categories: []string{"人设与角色"}, SourceKinds: []string{BenchmarkSourceKind}, Benchmark: true,
		Description: "人设模板/角色小传/反派设定"},
	CraftFieldLexicon: {Categories: []string{"素材与描写词汇"}, Facets: []CraftFacet{FacetLexicon},
		SourceKinds: []string{BenchmarkSourceKind}, Benchmark: true,
		Description: "描写词汇/替换词/取名/黑话/地名素材"},
	CraftFieldPlotBeats: {Categories: []string{"爽点与剧情钩子"}, SourceKinds: []string{BenchmarkSourceKind}, Benchmark: true,
		Description: "爽点清单/剧情钩子/不卡文剧情点/商战手段"},
	CraftFieldBenchmark: {Categories: []string{"拆文分析"}, SourceKinds: []string{BenchmarkSourceKind}, Benchmark: true,
		Description: "对标作品拆文：结构/节奏/信息释放分析（严禁照搬情节人名）"},
	CraftFieldMarket: {Categories: []string{"运营与平台"}, SourceKinds: []string{BenchmarkSourceKind}, Benchmark: true,
		Description: "投稿渠道/签约模板/拒签原因/平台运营"},
	CraftFieldSceneCraft: {Categories: []string{"场景与情境"}, Facets: []CraftFacet{FacetScene},
		SourceKinds: []string{CraftSourceKind, BenchmarkSourceKind}, Benchmark: true,
		Description: "场景/环境/景色/情境设计（跨库按内容取）"},
}

// CraftFieldNames 返回全部设计字段名（schema enum 用），字典序稳定。
func CraftFieldNames() []string {
	names := make([]string, 0, len(craftFieldRecipes))
	for field := range craftFieldRecipes {
		names = append(names, string(field))
	}
	sort.Strings(names)
	return names
}

// CraftRecipeDescription 返回字段配方说明；未知字段返回空串。
func CraftRecipeDescription(field CraftDesignField) string {
	return craftFieldRecipes[field].Description
}

// CraftRecallResult 一次设计检索的结果。NoMaterial=true 表示 filter 命中集为空
// 或排序后无有效材料——这是一个可审计事件，调用方必须把它写进产物
// （material_source: no_material），不允许静默让 LLM 自行编造。
type CraftRecallResult struct {
	Field          CraftDesignField
	Filter         craftFieldRecipe
	Hits           []BM25Hit
	NoMaterial     bool
	FilteredCount  int
	FilteredReason map[string]int
}

// CraftCatalog caches the deterministic field filter and BM25 corpus lazily.
// A tool instance can reuse it across several design-time recalls without
// rebuilding an index over the same large technique library each time.
type CraftCatalog struct {
	chunks  []domain.RAGChunk
	mu      sync.Mutex
	corpora map[string]*craftCorpus
}

type craftCorpus struct {
	subset         []domain.RAGChunk
	index          *BM25Index
	filteredCount  int
	filteredReason map[string]int
}

func NewCraftCatalog(chunks []domain.RAGChunk) *CraftCatalog {
	return &CraftCatalog{
		chunks:  append([]domain.RAGChunk(nil), chunks...),
		corpora: make(map[string]*craftCorpus),
	}
}

// CraftRecallOptions narrows automatic rewrite retrieval without changing the
// broad, explicit design-time tool contract used by Architect.
type CraftRecallOptions struct {
	Stage           string
	RequireRelevant bool
	SafeRewrite     bool
}

// CraftRecall 对 chunk 集执行「字段绑定 filter → 子集内 BM25 排序」的设计检索。
// topic 为空时按字段描述排序（等价于取该类目的代表性材料）。
func CraftRecall(chunks []domain.RAGChunk, field CraftDesignField, topic string, limit int) CraftRecallResult {
	return NewCraftCatalog(chunks).Recall(field, topic, limit)
}

func (c *CraftCatalog) Recall(field CraftDesignField, topic string, limit int) CraftRecallResult {
	return c.RecallWithOptions(field, topic, limit, CraftRecallOptions{})
}

func (c *CraftCatalog) RecallWithOptions(field CraftDesignField, topic string, limit int, options CraftRecallOptions) CraftRecallResult {
	recipe, ok := craftFieldRecipes[field]
	result := CraftRecallResult{Field: field, Filter: recipe}
	if c == nil || !ok || limit <= 0 {
		result.NoMaterial = true
		return result
	}
	if options.SafeRewrite && !safeRewriteCraftField(field) {
		result.NoMaterial = true
		return result
	}
	corpus := c.corpus(field, recipe, options)
	subset := corpus.subset
	result.FilteredCount = corpus.filteredCount
	result.FilteredReason = cloneCraftFilterReasons(corpus.filteredReason)
	if len(subset) == 0 {
		result.NoMaterial = true
		return result
	}
	query := strings.TrimSpace(topic)
	if query == "" {
		query = recipe.Description
	}
	hits := corpus.index.Search(query, limit)
	if options.SafeRewrite {
		hits = searchSafeRewriteMethodCards(subset, query, limit, field)
	}
	if options.RequireRelevant {
		minimumOverlap := minimumCraftQueryOverlap(query)
		relevant := make([]domain.RAGChunk, 0, len(subset))
		for _, chunk := range subset {
			searchText := SearchText(chunk)
			if options.SafeRewrite {
				searchText = safeRewriteMethodSearchText(chunk)
			}
			if craftQueryTermOverlap(query, searchText) < minimumOverlap {
				result.FilteredCount++
				incrementCraftFilterReason(result.FilteredReason, "low_query_overlap")
				continue
			}
			relevant = append(relevant, chunk)
		}
		if options.SafeRewrite {
			hits = searchSafeRewriteMethodCards(relevant, query, limit, field)
		} else {
			hits = BuildBM25Index(relevant).Search(query, limit)
		}
	} else if len(hits) == 0 {
		// filter 命中但词法排序无得分：退化为子集头部，保证"命中即有料"。
		for i, chunk := range subset {
			if i >= limit {
				break
			}
			hits = append(hits, BM25Hit{Chunk: chunk})
		}
	}
	result.Hits = hits
	result.NoMaterial = len(hits) == 0
	return result
}

func safeRewriteMethodSearchText(chunk domain.RAGChunk) string {
	category, _ := chunkCraftCategory(chunk)
	return strings.Join([]string{
		strings.TrimSpace(chunk.Summary),
		strings.TrimSpace(category),
		string(chunkCraftFacet(chunk)),
	}, " ")
}

func searchSafeRewriteMethodCards(chunks []domain.RAGChunk, query string, limit int, field CraftDesignField) []BM25Hit {
	if len(chunks) == 0 || limit <= 0 {
		return nil
	}
	originals := make(map[string]domain.RAGChunk, len(chunks))
	sanitized := make([]domain.RAGChunk, 0, len(chunks))
	seenMethods := make(map[string]struct{}, len(chunks))
	for _, chunk := range chunks {
		if !safeRewritePrimaryMethodSupportsField(chunk.Summary, field) {
			continue
		}
		// Rebuilt indexes can contain many source chunks that resolve to the same
		// closed-vocabulary operation. Deduplicate by the primary mechanism/action,
		// not only by the whole string: secondary tags must not let three variants
		// of the same operation occupy every Top-N slot.
		methodKey := safeRewriteMethodDedupKey(chunk.Summary)
		if methodKey == "" {
			continue
		}
		if _, exists := seenMethods[methodKey]; exists {
			continue
		}
		seenMethods[methodKey] = struct{}{}
		originals[chunk.ID] = chunk
		clone := chunk
		clone.SourcePath = ""
		clone.SourceKind = ""
		clone.ParentID = ""
		clone.Context = ""
		clone.Keywords = nil
		clone.Text = ""
		clone.Metadata = nil
		clone.Summary = safeRewriteMethodSearchText(chunk)
		sanitized = append(sanitized, clone)
	}
	hits := BuildBM25Index(sanitized).Search(query, limit)
	for i := range hits {
		if original, ok := originals[hits[i].Chunk.ID]; ok {
			hits[i].Chunk = original
		}
	}
	return hits
}

func safeRewritePrimaryMethodSupportsField(summary string, field CraftDesignField) bool {
	if field == CraftFieldMethodology {
		return true
	}
	primary := safeRewritePrimaryMethodTag(summary)
	if primary == "" {
		// Hand-curated cards may predate the structured tag field. Relevance and
		// the existing category/facet filter still apply to those summaries.
		return true
	}
	switch field {
	case CraftFieldDialogue:
		switch primary {
		case "漏答", "打断", "潜台词", "声口差异", "权力位移", "信息延迟", "信息释放", "行动反应", "关系位移":
			return true
		}
	case CraftFieldSceneCraft:
		switch primary {
		case "场景目标", "阻力对抗", "选择取舍", "行动反应", "场景后果", "证据物件", "转折改道", "冲突升级", "节奏张弛", "空间调度", "感官锚点", "关系位移", "情绪转向", "过场压缩":
			return true
		}
	default:
		return true
	}
	return false
}

func safeRewritePrimaryMethodTag(summary string) string {
	for _, field := range strings.Split(summary, "；") {
		field = strings.TrimSpace(field)
		if !strings.HasPrefix(field, "技法标签=") {
			continue
		}
		labels := strings.Split(strings.TrimPrefix(field, "技法标签="), "、")
		if len(labels) > 0 {
			return strings.TrimSpace(labels[0])
		}
	}
	return ""
}

func safeRewriteMethodDedupKey(summary string) string {
	normalized := strings.ToLower(strings.Join(strings.Fields(summary), " "))
	if normalized == "" {
		return ""
	}
	var mechanism, action string
	for _, field := range strings.Split(normalized, "；") {
		field = strings.TrimSpace(field)
		switch {
		case strings.HasPrefix(field, "机制="):
			mechanism = strings.TrimSpace(strings.TrimPrefix(field, "机制="))
		case strings.HasPrefix(field, "动作="):
			action = strings.TrimSpace(strings.TrimPrefix(field, "动作="))
		}
	}
	if mechanism != "" || action != "" {
		return "operation:" + mechanism + "\x00" + action
	}
	if primaryTag := safeRewritePrimaryMethodTag(summary); primaryTag != "" {
		return "primary-tag:" + strings.ToLower(primaryTag)
	}
	return "summary:" + normalized
}

func (c *CraftCatalog) corpus(field CraftDesignField, recipe craftFieldRecipe, options CraftRecallOptions) *craftCorpus {
	c.mu.Lock()
	defer c.mu.Unlock()
	key := strings.Join([]string{string(field), strings.ToLower(strings.TrimSpace(options.Stage)), fmtBool(options.SafeRewrite)}, "|")
	if existing := c.corpora[key]; existing != nil {
		return existing
	}
	allowedKinds := append([]string(nil), recipe.SourceKinds...)
	if len(allowedKinds) == 0 {
		allowedKinds = []string{CraftSourceKind}
	}
	if options.SafeRewrite && !containsFold(allowedKinds, CalibrationSourceKind) {
		allowedKinds = append(allowedKinds, CalibrationSourceKind)
	}
	// 1) 确定性 filter：source_kind + craft_category 集合运算
	var subset []domain.RAGChunk
	filteredReason := map[string]int{}
	filteredCount := 0
	filter := func(reason string) {
		filteredCount++
		incrementCraftFilterReason(filteredReason, reason)
	}
	for _, chunk := range c.chunks {
		chunk = NormalizeChunk(chunk)
		if !containsFold(allowedKinds, strings.TrimSpace(chunk.SourceKind)) {
			filter("source_kind")
			continue
		}
		if !validCraftKindPath(chunk) {
			filter("kind_path_mismatch")
			continue
		}
		if !craftChunkSupportsStage(chunk, options.Stage) {
			filter("usage_stage")
			continue
		}
		category, subcategory := chunkCraftCategory(chunk)
		// 命中判定：目录类目命中 或 内容级 craft_facet 命中（两者取并集，让"素材与描写词汇"
		// 这类混合目录里被内容判为 dialogue/appearance/scene 的文件也能被对应字段取到）。
		categoryHit := containsFold(recipe.Categories, category)
		facetHit := len(recipe.Facets) > 0 && containsCraftFacet(recipe.Facets, chunkCraftFacet(chunk))
		// The curated automatic-rewrite corpus is deliberately stored as
		// methodology cards.  A card can carry dialogue/scene techniques in its
		// controlled summary while its single persisted craft_facet remains
		// "methodology".  For safe automatic recalls, let dialogue and scene
		// needs search that method-card category as well; RequireRelevant still
		// demands real query overlap before a hit is returned.  This bridge is
		// intentionally unavailable to broad/explicit craft recall, so it cannot
		// turn an unrelated query into the historical zero-score fallback.
		safeMethodCardHit := options.SafeRewrite &&
			(field == CraftFieldDialogue || field == CraftFieldSceneCraft) &&
			strings.EqualFold(category, "novel-craft-methodology")
		if !categoryHit && !facetHit && !safeMethodCardHit {
			filter("category_or_facet")
			continue
		}
		// 子类目过滤仅在靠目录类目命中时生效（facet 命中的跨目录文件不受子类目约束）。
		if categoryHit && len(recipe.Subcategories) > 0 && !containsFold(recipe.Subcategories, subcategory) {
			filter("subcategory")
			continue
		}
		if options.SafeRewrite && strings.TrimSpace(chunk.Summary) == "" {
			filter("missing_summary")
			continue
		}
		if options.SafeRewrite && !safeRewriteSummaryProvenance(chunk) {
			filter("unsafe_summary_origin")
			continue
		}
		if options.SafeRewrite && !safeRewriteCraftChunk(chunk) {
			filter("unsafe_rewrite_category")
			continue
		}
		subset = append(subset, chunk)
	}
	corpus := &craftCorpus{
		subset:         subset,
		index:          BuildBM25Index(subset),
		filteredCount:  filteredCount,
		filteredReason: filteredReason,
	}
	c.corpora[key] = corpus
	return corpus
}

func cloneCraftFilterReasons(source map[string]int) map[string]int {
	clone := make(map[string]int, len(source))
	for reason, count := range source {
		clone[reason] = count
	}
	return clone
}

func incrementCraftFilterReason(reasons map[string]int, reason string) {
	if strings.TrimSpace(reason) == "" {
		return
	}
	reasons[reason]++
}

func fmtBool(value bool) string {
	if value {
		return "1"
	}
	return "0"
}

func safeRewriteCraftField(field CraftDesignField) bool {
	return field == CraftFieldDialogue || field == CraftFieldMethodology || field == CraftFieldSceneCraft
}

// validCraftKindPath prevents a stale/manually edited index from turning an
// active-project fact chunk into design material merely by spoofing source_kind.
func validCraftKindPath(chunk domain.RAGChunk) bool {
	switch strings.ToLower(strings.TrimSpace(chunk.SourceKind)) {
	case CraftSourceKind:
		return IsCraftTechniquePath(chunk.SourcePath)
	case BenchmarkSourceKind:
		return IsBenchmarkLibraryPath(chunk.SourcePath)
	case CalibrationSourceKind:
		return IsCalibrationPath(chunk.SourcePath)
	default:
		return false
	}
}

func safeRewriteCraftChunk(chunk domain.RAGChunk) bool {
	if strings.EqualFold(chunk.SourceKind, CraftSourceKind) {
		category, _ := CraftCategory(chunk.SourcePath)
		return strings.EqualFold(category, "novel-craft-methodology")
	}
	if strings.EqualFold(chunk.SourceKind, CalibrationSourceKind) {
		return isCuratedRewriteMethodPath(chunk.SourcePath)
	}
	// Benchmark and broad writing-techniques libraries contain story excerpts,
	// names, settings and even instruction-like strings. Automatic prose repair
	// does not consume them until they have been curated into the dedicated
	// method path. Explicit Architect craft_recall remains unchanged.
	return false
}

// IsSafeRewriteMethodChunk reports whether a chunk is allowed into the
// automatic prose-repair corpus.  Keep this predicate shared with receipt
// identity calculation: a receipt must be invalidated by changes to the exact
// corpus that can affect automatic recall, rather than by a cached index-level
// sanitization marker.
func IsSafeRewriteMethodChunk(chunk domain.RAGChunk) bool {
	return strings.TrimSpace(chunk.Summary) != "" &&
		safeRewriteSummaryProvenance(chunk) &&
		IsSafeRewriteMethodSource(chunk)
}

// IsSafeRewriteMethodSource reports whether a source is eligible for the
// automatic rewrite method corpus before summary provenance is established.
// Schema migration uses this narrower predicate so upgrading structured cards
// does not rewrite or re-embed the much larger benchmark/reference libraries.
func IsSafeRewriteMethodSource(chunk domain.RAGChunk) bool {
	return validCraftKindPath(chunk) &&
		safeRewriteCraftChunk(chunk) &&
		craftChunkSupportsStage(chunk, StagePlan)
}

func isCuratedRewriteMethodPath(path string) bool {
	clean := strings.ToLower(filepath.ToSlash(strings.TrimSpace(path)))
	return strings.Contains(clean, "/review-calibration/novel-craft-methodology/") ||
		strings.HasPrefix(clean, "deconstruction-library/review-calibration/novel-craft-methodology/")
}

func craftChunkSupportsStage(chunk domain.RAGChunk, stage string) bool {
	stage = strings.ToLower(strings.TrimSpace(stage))
	if stage == "" {
		return true
	}
	var stages []string
	if chunk.Metadata != nil {
		if raw, ok := chunk.Metadata["usage_stage"].(string); ok {
			for _, item := range strings.Split(raw, ",") {
				if item = strings.ToLower(strings.TrimSpace(item)); item != "" {
					stages = append(stages, item)
				}
			}
		}
	}
	if len(stages) == 0 {
		stages = UsageStagesForFacet(chunkCraftFacet(chunk))
	}
	return containsFold(stages, stage)
}

func craftQueryTermOverlap(query, text string) int {
	wanted := map[string]struct{}{}
	for _, token := range TokenizeForBM25(query) {
		wanted[token] = struct{}{}
	}
	seen := map[string]struct{}{}
	for _, token := range TokenizeForBM25(text) {
		if _, ok := wanted[token]; ok {
			seen[token] = struct{}{}
		}
	}
	return len(seen)
}

func minimumCraftQueryOverlap(query string) int {
	unique := map[string]struct{}{}
	for _, token := range TokenizeForBM25(query) {
		unique[token] = struct{}{}
	}
	if len(unique) <= 1 {
		return len(unique)
	}
	return 2
}

func chunkCraftCategory(chunk domain.RAGChunk) (category, subcategory string) {
	if chunk.Metadata != nil {
		if v, ok := chunk.Metadata["craft_category"].(string); ok {
			category = strings.ToLower(strings.TrimSpace(v))
		}
		if v, ok := chunk.Metadata["craft_subcategory"].(string); ok {
			subcategory = strings.ToLower(strings.TrimSpace(v))
		}
	}
	if category == "" {
		category, subcategory = CraftCategory(chunk.SourcePath)
	}
	if category == "" {
		category = BenchmarkCategory(chunk.SourcePath)
	}
	if category == "" && isCuratedRewriteMethodPath(chunk.SourcePath) {
		category = "novel-craft-methodology"
	}
	return category, subcategory
}

// IsBenchmarkField 该设计字段是否会命中对标素材（结果只可迁移手法/结构）。
func IsBenchmarkField(field CraftDesignField) bool {
	return craftFieldRecipes[field].Benchmark
}

func containsFold(list []string, target string) bool {
	for _, item := range list {
		if strings.EqualFold(item, target) {
			return true
		}
	}
	return false
}
