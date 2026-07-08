package rag

import (
	"path/filepath"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

// 设计库（craft/benchmark）内容级细粒度标签。目录分类太粗（如"素材与描写词汇"里混着
// 外貌、对白、场景、结构、平台多种内容），故按文件名+正文内容再判一个 craft_facet，
// 并派生 usage_stage（哪个流水线阶段能用），让 Architect / plan / writing 各取所需。

// CraftFacet 内容级细分面。
type CraftFacet string

const (
	FacetAppearance    CraftFacet = "appearance"         // 外貌/肖像/神态/服饰/发型/心理/动作描写词库
	FacetDialogue      CraftFacet = "dialogue"           // 对白/对话/台词/交涉/吵架技法（原库缺失，本次补齐）
	FacetScene         CraftFacet = "scene"              // 场景/环境/景色/情境/天气描写
	FacetEmotion       CraftFacet = "emotion"            // 情绪/心理/心情描写
	FacetLexicon       CraftFacet = "lexicon"            // 取名/地名/黑话/词汇素材
	FacetWeapon        CraftFacet = "weapon"             // 武器/兵器/法宝
	FacetSkillAbility  CraftFacet = "skill_ability"      // 功法/技能/法术/职业/体系/阶位
	FacetInstitution   CraftFacet = "institution"        // 制度/官职/物价/民俗/现实支架
	FacetOutline       CraftFacet = "outline"            // 大纲模板/细纲/结构填表
	FacetTrope         CraftFacet = "trope"              // 题材/套路/流派/桥段
	FacetPersona       CraftFacet = "persona"            // 人设/角色设计/小传/反派
	FacetPlot          CraftFacet = "plot"               // 爽点/剧情钩子/情节/戏剧结构/冲突
	FacetBenchmark     CraftFacet = "benchmark_analysis" // 拆文/拆书/对标作品结构分析
	FacetWorldview     CraftFacet = "worldview"          // 世界观/宇宙观/心理学
	FacetMethodology   CraftFacet = "methodology"        // 创作方法论/教程/入门/文笔提升
	FacetMarket        CraftFacet = "market"             // 运营/平台/投稿/签约/基金
	FacetCalibration   CraftFacet = "calibration"        // AI 检测校准/人工样本共通点/审核报告
	FacetUncategorized CraftFacet = "uncategorized"
)

// usage stage 常量。
const (
	StageArchitect = "architect"
	StagePlan      = "plan"
	StageWriting   = "writing"
	StageReview    = "review"
)

// facetRule 一条内容分类规则：命中任一关键词即计一次分（文件名权重更高）。
type facetRule struct {
	facet    CraftFacet
	keywords []string
}

// facetRules 关键词分类表，顺序即优先级（并列时靠前者胜）。规则来自逐目录抽样的真实文件名。
var facetRules = []facetRule{
	{FacetCalibration, []string{"校准", "人工样本", "共通点", "审核优化", "aigc", "ai味", "ai 味", "检测报告"}},
	{FacetBenchmark, []string{"拆文", "拆书", "拆解-", "短篇-", "逐章拆", "对标"}},
	{FacetDialogue, []string{"对白", "对话", "台词", "交涉", "吵架", "争吵", "唇枪", "说话", "口语化", "对骂"}},
	{FacetAppearance, []string{"外貌", "肖像", "长相", "容貌", "神态", "神情", "美女", "美男", "俊美", "发鬓", "发型", "服饰", "穿着", "衣着", "形容男", "形容女", "描写男", "描写女", "接吻", "品貌", "动作细节", "动作描写", "描写动作", "人物描写", "人物动作", "人物外貌"}},
	{FacetEmotion, []string{"心情", "心理描写", "情绪", "描写人物心理", "描写心情", "描写神情", "心理活动"}},
	{FacetScene, []string{"环境描写", "景色", "自然环境", "场景描写", "情境", "天气", "风景", "描写景", "描写自然"}},
	{FacetOutline, []string{"大纲", "细纲", "分卷", "填表", "架构指南", "结构模板", "剧情大纲"}},
	{FacetPlot, []string{"爽点", "钩子", "剧情钩", "情节", "戏剧结构", "冲突设计", "反转", "桥段", "诡计", "不卡文"}},
	{FacetPersona, []string{"人设", "角色设定", "人物设定", "角色小传", "反派设定", "主角设定", "配角"}},
	{FacetWeapon, []string{"武器", "兵器", "名刀", "名剑", "法宝", "神器", "冷兵器"}},
	{FacetSkillAbility, []string{"功法", "技能", "法术", "阵法", "职业设定", "体系", "阶位", "神格", "能力分级", "修炼", "雷法", "炼金"}},
	{FacetInstitution, []string{"官职", "爵位", "兵制", "物价", "婚嫁", "民俗", "制度", "急救", "医学", "现实"}},
	{FacetTrope, []string{"题材", "套路", "流派", "网游", "悬疑", "言情", "玄幻", "历史题材", "侦探", "女频", "男频", "架空"}},
	{FacetWorldview, []string{"世界观", "宇宙观", "心理学", "位面", "晶壁"}},
	{FacetMarket, []string{"平台", "运营", "投稿", "签约", "拒签", "基金", "协会", "申请表", "评定标准", "上架", "编辑"}},
	{FacetMethodology, []string{"教程", "方法论", "指南", "写作技巧", "入门", "写作指导", "文笔", "提升", "怎么写", "如何写", "创作"}},
	{FacetLexicon, []string{"词汇", "词语", "成语", "取名", "名字", "地名", "黑话", "素材", "替换词", "俗语", "谚语"}},
}

// CraftContentFacet 按文件名（权重高）+ 正文样本（权重低）给设计库文件判一个细分面。
// path 用于文件名判定；sample 是正文前若干字（可空）。命中不到时回落到目录类目推断。
func CraftContentFacet(path, sample string) CraftFacet {
	// 审核校准库：人工文笔样本是叙事正文（按内容会误判成 appearance/scene），校准报告是
	// 元分析——它们都是"什么是好的人类文笔 / AI 味校准"的参照，统一归 calibration。
	// 唯独 review-calibration 下的 novel-craft-methodology 是创作方法论，仍按关键词判 methodology。
	lowerPath := strings.ToLower(filepath.ToSlash(path))
	if strings.Contains(lowerPath, "review-calibration") && !strings.Contains(lowerPath, "novel-craft-methodology") {
		return FacetCalibration
	}
	name := strings.ToLower(filepath.Base(path))
	body := strings.ToLower(sample)
	best := FacetUncategorized
	bestScore := 0
	for _, rule := range facetRules {
		if len(rule.keywords) == 0 {
			continue
		}
		score := 0
		for _, kw := range rule.keywords {
			if strings.Contains(name, kw) {
				score += 3 // 文件名命中权重高
			}
			if body != "" && strings.Contains(body, kw) {
				score++
			}
		}
		if score > bestScore {
			bestScore = score
			best = rule.facet
		}
	}
	if bestScore == 0 {
		return facetFromCategory(path)
	}
	return best
}

// facetFromCategory 关键词判不出时，用目录类目做兜底映射。
func facetFromCategory(path string) CraftFacet {
	if cat, _ := CraftCategory(path); cat != "" {
		switch cat {
		case "appearance":
			return FacetAppearance
		case "weapons":
			return FacetWeapon
		case "magic-arts", "fantasy", "scifi":
			return FacetSkillAbility
		case "ancient-history":
			return FacetInstitution
		case "novel-craft-methodology":
			return FacetMethodology
		}
	}
	switch BenchmarkCategory(path) {
	case "大纲模板与示例":
		return FacetOutline
	case "题材与套路":
		return FacetTrope
	case "人设与角色":
		return FacetPersona
	case "素材与描写词汇":
		return FacetLexicon
	case "爽点与剧情钩子":
		return FacetPlot
	case "拆文分析":
		return FacetBenchmark
	case "运营与平台":
		return FacetMarket
	case "场景与情境":
		return FacetScene
	case "教程方法论", "文笔提升与书单":
		return FacetMethodology
	case "心理学与世界观":
		return FacetWorldview
	}
	return FacetUncategorized
}

// facetStages 面 → 可用流水线阶段。Architect=世界/大纲/设定/故事性；plan/writing=章节级取料。
var facetStages = map[CraftFacet][]string{
	FacetAppearance:   {StagePlan, StageWriting},
	FacetDialogue:     {StagePlan, StageWriting},
	FacetScene:        {StagePlan, StageWriting},
	FacetEmotion:      {StagePlan, StageWriting},
	FacetLexicon:      {StagePlan, StageWriting},
	FacetWeapon:       {StageArchitect, StagePlan, StageWriting},
	FacetSkillAbility: {StageArchitect, StagePlan},
	FacetInstitution:  {StageArchitect, StagePlan, StageWriting},
	FacetOutline:      {StageArchitect},
	FacetTrope:        {StageArchitect},
	FacetPersona:      {StageArchitect, StagePlan},
	FacetPlot:         {StageArchitect, StagePlan},
	FacetBenchmark:    {StageArchitect, StagePlan},
	FacetWorldview:    {StageArchitect},
	FacetMethodology:  {StageArchitect, StagePlan, StageWriting},
	FacetMarket:       {StageArchitect},
	FacetCalibration:  {StageReview},
}

// UsageStagesForFacet 返回该面可服务的流水线阶段（稳定顺序）。
func UsageStagesForFacet(facet CraftFacet) []string {
	if stages, ok := facetStages[facet]; ok {
		return stages
	}
	return []string{StagePlan, StageWriting}
}

// chunkCraftFacet 读 chunk 上落盘的内容级细分面（craft_facet）；缺失时按路径内容兜底判定。
func chunkCraftFacet(chunk domain.RAGChunk) CraftFacet {
	if chunk.Metadata != nil {
		if v, ok := chunk.Metadata["craft_facet"].(string); ok && strings.TrimSpace(v) != "" {
			return CraftFacet(strings.ToLower(strings.TrimSpace(v)))
		}
	}
	return CraftContentFacet(chunk.SourcePath, "")
}

// containsCraftFacet 判断 facet 是否在集合内（大小写不敏感）。
func containsCraftFacet(set []CraftFacet, facet CraftFacet) bool {
	for _, f := range set {
		if strings.EqualFold(string(f), string(facet)) {
			return true
		}
	}
	return false
}
