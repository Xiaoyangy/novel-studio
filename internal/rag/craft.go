package rag

import (
	"path/filepath"
	"sort"
	"strings"

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

// BenchmarkSourceKind 对标素材库（novel_all）chunk 的 source_kind 标记。
// 对标素材只可迁移手法/结构/节奏，禁止照搬情节、人名与专有设定。
const BenchmarkSourceKind = "benchmark_reference"

// IsDesignOnlySourceKind 判断某 source_kind 是否属于"不能作为常规事实召回"的库：
// novel_context 必须排除这些 chunk；若写作/重写要用其中的技法，必须显式调用 craft_recall。
func IsDesignOnlySourceKind(kind string) bool {
	kind = strings.TrimSpace(kind)
	return strings.EqualFold(kind, CraftSourceKind) || strings.EqualFold(kind, BenchmarkSourceKind)
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
	Categories    []string // 命中类目集合（craft_category ∈ Categories）
	Subcategories []string // 可选子类目过滤（空 = 不限）
	SourceKinds   []string // 命中库集合；空 = 仅 craft_technique
	Description   string
	Benchmark     bool // 含对标素材：产出只可迁移手法/结构，须登记 external_reference_plan
}

// craftFieldRecipes 字段 ↔ 检索配方绑定表（确定性路由核心）。
var craftFieldRecipes = map[CraftDesignField]craftFieldRecipe{
	CraftFieldAppearance:  {Categories: []string{"appearance"}, Description: "外貌/五官/服饰描写词库"},
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
	CraftFieldLexicon: {Categories: []string{"素材与描写词汇"}, SourceKinds: []string{BenchmarkSourceKind}, Benchmark: true,
		Description: "描写词汇/替换词/取名/黑话/地名素材"},
	CraftFieldPlotBeats: {Categories: []string{"爽点与剧情钩子"}, SourceKinds: []string{BenchmarkSourceKind}, Benchmark: true,
		Description: "爽点清单/剧情钩子/不卡文剧情点/商战手段"},
	CraftFieldBenchmark: {Categories: []string{"拆文分析"}, SourceKinds: []string{BenchmarkSourceKind}, Benchmark: true,
		Description: "对标作品拆文：结构/节奏/信息释放分析（严禁照搬情节人名）"},
	CraftFieldMarket: {Categories: []string{"运营与平台"}, SourceKinds: []string{BenchmarkSourceKind}, Benchmark: true,
		Description: "投稿渠道/签约模板/拒签原因/平台运营"},
	CraftFieldSceneCraft: {Categories: []string{"场景与情境"}, SourceKinds: []string{BenchmarkSourceKind}, Benchmark: true,
		Description: "场景设计/情境模板"},
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
	Field      CraftDesignField
	Filter     craftFieldRecipe
	Hits       []BM25Hit
	NoMaterial bool
}

// CraftRecall 对 chunk 集执行「字段绑定 filter → 子集内 BM25 排序」的设计检索。
// topic 为空时按字段描述排序（等价于取该类目的代表性材料）。
func CraftRecall(chunks []domain.RAGChunk, field CraftDesignField, topic string, limit int) CraftRecallResult {
	recipe, ok := craftFieldRecipes[field]
	result := CraftRecallResult{Field: field, Filter: recipe}
	if !ok || limit <= 0 {
		result.NoMaterial = true
		return result
	}
	allowedKinds := recipe.SourceKinds
	if len(allowedKinds) == 0 {
		allowedKinds = []string{CraftSourceKind}
	}
	// 1) 确定性 filter：source_kind + craft_category 集合运算
	var subset []domain.RAGChunk
	for _, chunk := range chunks {
		chunk = NormalizeChunk(chunk)
		if !containsFold(allowedKinds, strings.TrimSpace(chunk.SourceKind)) {
			continue
		}
		category, subcategory := chunkCraftCategory(chunk)
		if !containsFold(recipe.Categories, category) {
			continue
		}
		if len(recipe.Subcategories) > 0 && !containsFold(recipe.Subcategories, subcategory) {
			continue
		}
		subset = append(subset, chunk)
	}
	if len(subset) == 0 {
		result.NoMaterial = true
		return result
	}
	// 2) 子集内排序：BM25（topic 为空用配方描述当查询）
	query := strings.TrimSpace(topic)
	if query == "" {
		query = recipe.Description
	}
	hits := BuildBM25Index(subset).Search(query, limit)
	if len(hits) == 0 {
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
