package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/rag"
	"github.com/chenhongyang/novel-studio/internal/store"
)

type projectMemoryRAGArtifact struct {
	RelPath    string
	SourceKind string
	Facet      string
	Context    string
	Summary    string
	Keywords   []string
	MaxRunes   int
}

func upsertProjectMemoryRAG(ctx context.Context, st *store.Store, embedder rag.Embedder, writer rag.VectorWriter, chapter int) (bool, error) {
	chunks, err := projectMemoryRAGChunks(st, chapter)
	if err != nil {
		return false, err
	}
	if len(chunks) == 0 {
		return false, nil
	}
	if err := upsertRAGChunks(ctx, st, embedder, writer, chunks, domain.RAGIndexConfig{}); err != nil {
		return true, err
	}
	return true, nil
}

func projectMemoryRAGChunks(st *store.Store, chapter int) ([]domain.RAGChunk, error) {
	specs := []projectMemoryRAGArtifact{
		{
			// 世界模拟：离屏事件流。让召回能命中"镜头外世界发生过什么"，
			// Writer 写到浮出点、Architect 规划下一弧时可按语义检索离屏事实。
			RelPath:    "meta/world_events.jsonl",
			SourceKind: "ledger",
			Facet:      "world",
			Context:    "离屏世界事件流 | world_events",
			Summary:    "镜头外世界事件：谁在主角看不见的地方做了什么、后果、可见章号与传播路径。",
			Keywords:   []string{"离屏事件", "世界推演", "镜头外", "visibility", "传播路径", "world_tick"},
			MaxRunes:   1400,
		},
		{
			// 世界模拟：离屏角色日程账本。召回配角"此刻在忙什么、进行到哪一步"。
			RelPath:    "meta/offscreen_agenda.json",
			SourceKind: "ledger",
			Facet:      "character",
			Context:    "离屏角色日程账本 | offscreen_agenda",
			Summary:    "离屏角色的自主目标与推进位置：goal、steps、状态与受阻原因。",
			Keywords:   []string{"离屏日程", "角色目标", "agenda", "配角动向", "世界推演"},
			MaxRunes:   1200,
		},
		{
			RelPath:    "meta/chapter_progress.md",
			SourceKind: "ledger",
			Facet:      "progress",
			Context:    "章节推进与人物变化台账 | chapter_progress",
			Summary:    "章节推进、主角变化、下一章动态计划和连续性输入。",
			Keywords:   []string{"章节推进", "人物变化", "主角状态", "next_plan", "动态大纲", "规划推荐"},
			MaxRunes:   1400,
		},
		{
			RelPath:    "meta/character_continuity.md",
			SourceKind: "ledger",
			Facet:      "character",
			Context:    "人物回归与续用规划台账 | character_continuity",
			Summary:    "人物回归、偶发露脸、续用规划、知识边界、决策框架、关系契约、情绪评价、人物状态保留与下一章焦点。",
			Keywords:   []string{"人物回归", "续用规划", "人物状态推荐", "知识账本", "决策框架", "关系契约", "情绪评价", "长期弧线", "next_chapter_focus"},
			MaxRunes:   1400,
		},
		{
			RelPath:    "meta/project_progress.md",
			SourceKind: "ledger",
			Facet:      "planning",
			Context:    "项目进度与规划推荐 | project_progress",
			Summary:    "项目进度、交付口径、动态大纲、资源关注、伏笔优先级和规划动作。",
			Keywords:   []string{"项目进度", "规划推荐", "后续资源关注", "动态大纲推进", "伏笔优先级", "next_chapter_actions"},
			MaxRunes:   1400,
		},
		{
			RelPath:    "meta/evolution_report.md",
			SourceKind: "ledger",
			Facet:      "review",
			Context:    "可审计演化报告 | evolution_report",
			Summary:    "质量模式、候选改动、验证计划和写作风险观察。",
			Keywords:   []string{"演化报告", "质量模式", "候选改动", "验证计划", "写法风险"},
			MaxRunes:   1400,
		},
		{
			RelPath:    "timeline.md",
			SourceKind: "ledger",
			Facet:      "plot",
			Context:    "时间线推进 | timeline",
			Summary:    "已发生事件、时间顺序和人物参与。",
			Keywords:   []string{"时间线推进", "事件顺序", "历史事实"},
			MaxRunes:   1200,
		},
		{
			RelPath:    "foreshadow_ledger.md",
			SourceKind: "ledger",
			Facet:      "plot",
			Context:    "伏笔推进台账 | foreshadow_ledger",
			Summary:    "伏笔埋设、推进、回收状态和后续兑现压力。",
			Keywords:   []string{"伏笔", "线索", "回收", "推进"},
			MaxRunes:   1200,
		},
		{
			RelPath:    "relationship_state.md",
			SourceKind: "ledger",
			Facet:      "character",
			Context:    "人物关系推进 | relationship_state",
			Summary:    "人物关系变化、关系张力和后续互动边界。",
			Keywords:   []string{"人物关系", "关系变化", "关系张力"},
			MaxRunes:   1200,
		},
		{
			RelPath:    "meta/state_changes.json",
			SourceKind: "ledger",
			Facet:      "character",
			Context:    "角色与实体状态推进 | state_changes",
			Summary:    "主角、配角、实体、资源或世界对象的状态变化记录。",
			Keywords:   []string{"主角状态推进", "人物状态", "状态变化"},
			MaxRunes:   1200,
		},
		{
			RelPath:    "meta/resource_ledger.md",
			SourceKind: "ledger",
			Facet:      "resource",
			Context:    "资源账本推进 | resource_ledger",
			Summary:    "已入账资源、待确认资源、风险与资源清账动作。",
			Keywords:   []string{"资源账本", "资源推进", "后续资源关注", "pending", "booked"},
			MaxRunes:   1200,
		},
		{
			RelPath:    "world_rules.md",
			SourceKind: "world",
			Facet:      "world",
			Context:    "世界规则推进 | world_rules",
			Summary:    "本书世界规则、边界和不可违反约束。",
			Keywords:   []string{"世界规则", "规则边界", "设定约束"},
			MaxRunes:   1200,
		},
		{
			RelPath:    "book_world.md",
			SourceKind: "world",
			Facet:      "world",
			Context:    "本书世界资产 | book_world",
			Summary:    "地点、路线、势力、地图注记和可复用世界上下文。",
			Keywords:   []string{"本书世界", "地点", "势力", "路线", "地图"},
			MaxRunes:   1200,
		},
		{
			RelPath:    "outline.md",
			SourceKind: "planning",
			Facet:      "planning",
			Context:    "章节大纲 | outline",
			Summary:    "当前已展开章节大纲、核心事件和章末钩子。",
			Keywords:   []string{"章节大纲", "核心事件", "章末钩子"},
			MaxRunes:   1400,
		},
		{
			RelPath:    "layered_outline.md",
			SourceKind: "planning",
			Facet:      "planning",
			Context:    "分层动态大纲 | layered_outline",
			Summary:    "卷弧结构、当前弧目标、骨架弧和后续规划。",
			Keywords:   []string{"动态大纲", "分层大纲", "卷弧", "后续规划"},
			MaxRunes:   1400,
		},
		{
			RelPath:    "meta/compass.json",
			SourceKind: "planning",
			Facet:      "planning",
			Context:    "终局方向指南针 | compass",
			Summary:    "终局方向、开放长线、预估规模和指南针更新时间。",
			Keywords:   []string{"终局方向", "开放长线", "指南针", "动态大纲推进"},
			MaxRunes:   1200,
		},
		{
			RelPath:    "meta/writing_assets.md",
			SourceKind: "craft",
			Facet:      "craft",
			Context:    "写法资产库与历史反馈沉淀 | writing_assets",
			Summary:    "本书长期写法特征、组合、绑定、主动规则、禁忌和审阅历史反馈。",
			Keywords:   []string{"写法资产库", "历史反馈", "审阅反馈", "review_lesson", "写法引擎", "active_rules", "anti_ai", "taboo"},
			MaxRunes:   1400,
		},
		{
			RelPath:    "meta/zero_chapter_context_manifest.md",
			SourceKind: "planning",
			Facet:      "zero_init",
			Context:    "零章上下文清单 | zero_chapter_context_manifest",
			Summary:    "第一章开写前允许使用的上下文来源、RAG 白名单、禁用来源和动态人物字段要求。",
			Keywords:   []string{"零章初始化", "RAG 白名单", "上下文来源", "第一章", "禁用来源"},
			MaxRunes:   1200,
		},
		{
			RelPath:    "meta/initial_character_dynamics.md",
			SourceKind: "ledger",
			Facet:      "character",
			Context:    "初始人物动态推演 | initial_character_dynamics",
			Summary:    "零章人物目标、压力、资源、关系、秘密、误判、知识账本、决策框架、关系契约、情绪评价、长期弧线和声口逻辑。",
			Keywords:   []string{"初始人物动态", "知识账本", "决策框架", "关系契约", "情绪评价", "长期弧线", "voice_logic"},
			MaxRunes:   1600,
		},
		{
			RelPath:    "relationship_state.initial.md",
			SourceKind: "ledger",
			Facet:      "character",
			Context:    "零章初始关系契约 | relationship_state.initial",
			Summary:    "正文未开始前的信任、债务、恐惧、承诺、依赖、背叛阈值和帮助条件基线。",
			Keywords:   []string{"初始关系", "关系契约", "信任", "债务", "背叛阈值", "帮助条件"},
			MaxRunes:   1200,
		},
		{
			RelPath:    "meta/initial_resource_ledger.md",
			SourceKind: "ledger",
			Facet:      "resource",
			Context:    "零章初始资源账本 | initial_resource_ledger",
			Summary:    "第一章前的资源、证据、能力和 pending/booked 边界。",
			Keywords:   []string{"初始资源", "资源账本", "pending", "booked", "证据", "能力边界"},
			MaxRunes:   1200,
		},
		{
			RelPath:    "foreshadow_ledger.initial.md",
			SourceKind: "ledger",
			Facet:      "plot",
			Context:    "零章初始伏笔账本 | foreshadow_ledger.initial",
			Summary:    "第一章计划埋设的伏笔种子、揭示预算和审核规则。",
			Keywords:   []string{"初始伏笔", "第一章伏笔", "揭示预算", "planned_seed", "审核"},
			MaxRunes:   1200,
		},
		{
			RelPath:    "meta/character_return_plan.md",
			SourceKind: "ledger",
			Facet:      "character",
			Context:    "人物回归与续用规划 | character_return_plan",
			Summary:    "人物必回、近未来、可选、休眠和退场策略，以及升级为长期变量的条件。",
			Keywords:   []string{"人物回归", "续用规划", "退场", "长期变量", "升级条件"},
			MaxRunes:   1200,
		},
		{
			RelPath:    "meta/crowd_role_policy.md",
			SourceKind: "planning",
			Facet:      "character",
			Context:    "捧场类角色策略 | crowd_role_policy",
			Summary:    "围观者、团队凑数成员、后勤组和功能性人群的命名、台词预算、连续性和退出边界。",
			Keywords:   []string{"捧场角色", "凑数角色", "围观者", "团队成员", "台词预算", "退出条件"},
			MaxRunes:   1200,
		},
		{
			RelPath:    "meta/prewrite_storycraft_plan.md",
			SourceKind: "craft",
			Facet:      "zero_init",
			Context:    "写前故事工艺计划 | prewrite_storycraft_plan",
			Summary:    "人物 Want/Lie/Need/Truth、声口卡、首章小胜与新债、前5章奖励阶梯、证据回收链、章末后果契约、休眠角色、现实支撑、情感逻辑、关系情感和视觉设计。",
			Keywords:   []string{"写前故事工艺", "人物弧测试", "声口卡", "读者奖励阶梯", "证据回收链", "章末后果契约", "情感逻辑", "恋爱关系", "视觉设计", "小胜", "新债"},
			MaxRunes:   2400,
		},
		{
			RelPath:    "meta/world_background_plan.md",
			SourceKind: "world",
			Facet:      "zero_init",
			Context:    "写前世界背景计划 | world_background_plan",
			Summary:    "事件背景十层、信息差结构、潜规则、社会情绪/谣言、仪式日历、结构资源、宇宙观规则、矛盾网和叙事张力矩阵。",
			Keywords:   []string{"世界背景层", "信息差", "潜规则", "社会情绪", "流言", "仪式日历", "结构资源", "宇宙观", "矛盾网", "叙事张力", "城市变化"},
			MaxRunes:   2400,
		},
		{
			RelPath:    "meta/ch01_zero_init_plan.md",
			SourceKind: "planning",
			Facet:      "zero_init",
			Context:    "第一章零章推演草案 | ch01_zero_init_plan",
			Summary:    "第一章写前目标、契约、长篇开局、角色初始状态、环境承载和审核失败后的重推演闭环。",
			Keywords:   []string{"第一章", "零章推演", "章节契约", "longform_opening", "review_refinement", "environment_state"},
			MaxRunes:   1800,
		},
		{
			RelPath:    "meta/ch01_prewrite_simulation.md",
			SourceKind: "planning",
			Facet:      "zero_init",
			Context:    "第一章具体写前推演 | ch01_prewrite_simulation",
			Summary:    "第一章具体角色行动系统、因果链、场景承载、捧场角色边界、声口测试、状态回填和审核返工条件。",
			Keywords:   []string{"第一章", "写前推演", "江烬", "知识账本", "决策框架", "关系契约", "声口逻辑", "状态回填"},
			MaxRunes:   2000,
		},
		{
			RelPath:    "meta/initial_review_lessons.md",
			SourceKind: "review",
			Facet:      "review",
			Context:    "初始审核回路 | initial_review_lessons",
			Summary:    "第一章审核失败后要把结论回灌到角色系统、声口逻辑、场景承载和可见事实，而不是只润色句子。",
			Keywords:   []string{"审核回路", "审核失败", "重推演", "voice_logic", "review_refinement", "AI味"},
			MaxRunes:   1000,
		},
	}
	var chunks []domain.RAGChunk
	for _, spec := range specs {
		text, err := readProjectRAGArtifact(st.Dir(), spec.RelPath)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(text) == "" {
			continue
		}
		metadata := map[string]any{
			"source":   "project_memory",
			"chapter":  chapter,
			"artifact": spec.RelPath,
		}
		contextText := spec.Context
		if chapter > 0 {
			contextText = fmt.Sprintf("第 %d 章 accept 后沉淀 | %s", chapter, spec.Context)
		}
		chunks = append(chunks, chunksFromRAGText(
			spec.RelPath,
			spec.SourceKind,
			spec.Facet,
			contextText,
			text,
			spec.Summary,
			spec.Keywords,
			metadata,
			spec.MaxRunes,
		)...)
	}
	return chunks, nil
}

func readProjectRAGArtifact(root, rel string) (string, error) {
	path := filepath.Join(root, filepath.FromSlash(rel))
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("read project RAG artifact %s: %w", rel, err)
	}
	text := strings.TrimSpace(string(raw))
	if text == "" {
		return "", nil
	}
	if strings.EqualFold(filepath.Ext(rel), ".json") {
		var payload any
		if err := json.Unmarshal(raw, &payload); err == nil {
			if pretty, err := json.MarshalIndent(payload, "", "  "); err == nil {
				return string(pretty), nil
			}
		}
	}
	return text, nil
}
