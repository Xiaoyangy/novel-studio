package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/rag"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/voocel/agentcore/schema"
)

// References 嵌入的参考资料。
type References struct {
	// V0
	ChapterGuide      string
	HookTechniques    string
	QualityChecklist  string
	OutlineTemplate   string
	CharacterTemplate string
	ChapterTemplate   string
	// V1
	Consistency      string
	ContentExpansion string
	DialogueWriting  string
	// V2
	StyleReference          string // 风格补充参考（可为空）
	LongformPlanning        string // 通用长篇规划参考
	Differentiation         string // 通用差异化设计参考
	ArcTemplates            string // 题材弧型模板（按 style 加载，可为空）
	AntiAITone              string // 去 AI 味判据库（writer/editor 共用，全程注入）
	ProductionPlaybook      string // 从 AI-Novel-Writing-Assistant 蒸馏的生产链路边界
	HumanFeelCraft          string // 高人工度样本文沉淀的可迁移写法资产
	CharacterBuilding       string // 人物塑造、动机、压力反应与关系动态参考
	EmotionalNarrativeCraft string // 情感叙事、情绪弧线、动机-反应和场景情绪变化参考
	WritingTechniquesDigest string // refer/写作技巧逐篇压缩后的工程写作规则
	RAGWritingGuidelines    string // RAG 召回在小说写作中的使用边界与 trace 判读
	WebReferenceGuidelines  string // 网络参考、最新资料和热梗进入正文的边界
	LongformAIDetector      string // 3000 字整章 AI 检测与交付门禁口径
}

// ContextTool 组装当前章节所需上下文。
type ContextTool struct {
	store             *store.Store
	refs              References
	style             string
	ragEmbedder       rag.Embedder
	ragVectorSearcher rag.VectorSearcher
}

// NewContextTool 创建上下文工具。
// user_rules 由 buildUserRules 直接读本书快照（meta/user_rules.json）注入，不再依赖加载选项。
func NewContextTool(store *store.Store, refs References, style string) *ContextTool {
	return &ContextTool{store: store, refs: refs, style: style}
}

func (t *ContextTool) WithRAGEmbedder(embedder rag.Embedder) *ContextTool {
	t.ragEmbedder = embedder
	return t
}

func (t *ContextTool) WithRAGVectorSearcher(searcher rag.VectorSearcher) *ContextTool {
	t.ragVectorSearcher = searcher
	return t
}

func (t *ContextTool) Name() string { return "novel_context" }
func (t *ContextTool) Description() string {
	return "获取小说当前状态和创作上下文。" +
		"不传 chapter：返回 progress_status（phase/flow/next_chapter/pending_rewrites 等进度字段）+ 基础设定，用于判断下一步该做什么。" +
		"传 chapter=N：额外返回该章的前情摘要、伏笔、角色状态、风格规则等写作上下文"
}
func (t *ContextTool) Label() string { return "加载上下文" }

// 纯读工具，可被并发调度。
func (t *ContextTool) ReadOnly(_ json.RawMessage) bool        { return true }
func (t *ContextTool) ConcurrencySafe(_ json.RawMessage) bool { return true }

func (t *ContextTool) Schema() map[string]any {
	return schema.Object(
		schema.Property("chapter", schema.Int("章节号。不传则返回进度状态和基础设定（Coordinator 用于判断下一步）；传入则额外返回该章的写作上下文（Writer 用）")),
	)
}

func (t *ContextTool) Execute(_ context.Context, args json.RawMessage) (json.RawMessage, error) {
	var a struct {
		Chapter int `json:"chapter"`
	}
	if err := unmarshalToolArgs(args, &a); err != nil {
		return nil, fmt.Errorf("invalid args: %w", err)
	}

	result := make(map[string]any)
	var warnings []string
	seenWarnings := make(map[string]struct{})
	warn := func(scope string, err error) {
		if err == nil || os.IsNotExist(err) {
			return
		}
		msg := fmt.Sprintf("%s 读取失败: %v", scope, err)
		if _, ok := seenWarnings[msg]; ok {
			return
		}
		seenWarnings[msg] = struct{}{}
		warnings = append(warnings, msg)
	}

	if a.Chapter > 0 {
		// Writer 路径：加载全量基础数据 + 章节上下文
		t.buildBaseContext(result, warn)
		seed := newChapterContextEnvelope()
		state := t.prepareChapterContext(a.Chapter, &seed, warn)
		seed.apply(result)
		t.buildChapterContext(result, state, warn)
		// 数据语义标注（治复读交代）：episodic 是已写入正文的备忘，不是待写素材。
		// 只挂容器内，不进顶层镜像。
		if epi, ok := result["episodic_memory"].(map[string]any); ok && len(epi) > 0 {
			epi["_usage"] = "本容器为已写入正文的事实备忘（供一致性与衔接对照）；在新章正文中原样复述这些内容属于重复缺陷"
		}
	} else {
		// Coordinator/Architect 路径：只返回状态 + 结构化数据，不加载全量原文
		t.buildProgressStatus(result)
		t.buildArchitectContext(result, warn)
	}

	// 注入 working_memory.user_rules（canonical 路径）。架构师路径原本没有 working_memory，
	// 由 buildUserRules 按需新建只装 user_rules 的容器。快照缺失时退到内置默认，
	// 始终输出稳定结构，避免 LLM 看到 user_rules=null 走异常分支。
	if a.Chapter > 0 {
		t.buildSimulationProfile(result, "working_memory", warn)
	} else {
		t.buildSimulationProfile(result, "planning_memory", warn)
	}

	t.buildUserRules(result)

	if len(warnings) > 0 {
		result["_warnings"] = warnings
	}

	// 优先级预算：总大小超过阈值时自动裁剪低优先级数据
	if a.Chapter > 0 {
		trimByBudget(result, 100*1024) // Writer: 100KB
	} else {
		trimByBudget(result, 60*1024) // Coordinator/Architect: 60KB
	}

	result["_loading_summary"] = buildLoadingSummary(result, a.Chapter)
	if a.Chapter > 0 {
		result["_reading_guide"] = contextReadingGuide
	}
	return marshalOrderedContext(result)
}

// buildLoadingSummary 从已组装的 result 中统计各项数据量，生成一行可读摘要。
func buildLoadingSummary(result map[string]any, chapter int) string {
	var parts []string

	if chapter > 0 {
		parts = append(parts, fmt.Sprintf("ch=%d", chapter))
	} else {
		parts = append(parts, "architect")
	}
	if tier, ok := result["planning_tier"].(domain.PlanningTier); ok && tier != "" {
		parts = append(parts, fmt.Sprintf("tier=%s", tier))
	}

	// 卷弧位置
	if pos, ok := result["position"].(map[string]any); ok {
		parts = append(parts, fmt.Sprintf("V%dA%d", pos["volume"], pos["arc"]))
	}

	var items []string
	countSlice := func(key string) int {
		if v, ok := result[key]; ok {
			if s, ok := v.([]domain.Character); ok {
				return len(s)
			}
			// 通用 slice 反射
			return sliceLen(v)
		}
		return 0
	}

	// 角色
	if n := countSlice("character_snapshots"); n > 0 {
		items = append(items, fmt.Sprintf("角色:%d(快照)", n))
	} else if n := countSlice("characters"); n > 0 {
		items = append(items, fmt.Sprintf("角色:%d", n))
	}

	if working, ok := result["working_memory"].(map[string]any); ok && len(working) > 0 {
		items = append(items, fmt.Sprintf("工作记忆:%d", len(working)))
	}
	if episodic, ok := result["episodic_memory"].(map[string]any); ok && len(episodic) > 0 {
		items = append(items, fmt.Sprintf("情节记忆:%d", len(episodic)))
	}
	if planning, ok := result["planning_memory"].(map[string]any); ok && len(planning) > 0 {
		items = append(items, fmt.Sprintf("规划记忆:%d", len(planning)))
	}
	if foundation, ok := result["foundation_memory"].(map[string]any); ok && len(foundation) > 0 {
		items = append(items, fmt.Sprintf("基础记忆:%d", len(foundation)))
	}

	// 分层摘要
	if n := countSlice("volume_summaries"); n > 0 {
		items = append(items, fmt.Sprintf("卷摘要:%d", n))
	}
	if n := countSlice("arc_summaries"); n > 0 {
		items = append(items, fmt.Sprintf("弧摘要:%d", n))
	}
	if n := countSlice("recent_summaries"); n > 0 {
		items = append(items, fmt.Sprintf("章摘要:%d", n))
	}

	// 分层大纲
	if n := countSlice("layered_outline"); n > 0 {
		items = append(items, fmt.Sprintf("分层大纲:%d卷", n))
	}

	// 状态数据
	if n := countSlice("timeline"); n > 0 {
		items = append(items, fmt.Sprintf("时间线:%d", n))
	}
	if n := countSlice("foreshadow_ledger"); n > 0 {
		items = append(items, fmt.Sprintf("伏笔:%d", n))
	}
	if n := countSlice("relationship_state"); n > 0 {
		items = append(items, fmt.Sprintf("关系:%d", n))
	}
	if n := countSlice("recent_state_changes"); n > 0 {
		items = append(items, fmt.Sprintf("状态变化:%d", n))
	}
	if _, ok := result["previous_tail"]; ok {
		items = append(items, "前章尾部:ok")
	}
	if _, ok := result["style_rules"]; ok {
		items = append(items, "风格规则:ok")
	}
	if n := sliceLen(result["related_chapters"]); n > 0 {
		items = append(items, fmt.Sprintf("相关章:%d", n))
	}
	if selected, ok := result["selected_memory"].(map[string]any); ok && len(selected) > 0 {
		if n := sliceLen(selected["story_threads"]); n > 0 {
			items = append(items, fmt.Sprintf("线索召回:%d", n))
		}
		if n := sliceLen(selected["rag_recall"]); n > 0 {
			items = append(items, fmt.Sprintf("RAG:%d", n))
		}
		if n := sliceLen(selected["review_lessons"]); n > 0 {
			items = append(items, fmt.Sprintf("评审召回:%d", n))
		}
	}

	// 参考资料
	if refs, ok := result["references"].(map[string]string); ok && len(refs) > 0 {
		items = append(items, fmt.Sprintf("参考:%d项", len(refs)))
	}
	if pack, ok := result["reference_pack"].(map[string]any); ok && len(pack) > 0 {
		items = append(items, fmt.Sprintf("参考包:%d", len(pack)))
	}
	if _, ok := result["writing_engine"]; ok {
		items = append(items, "写法引擎:ok")
	}
	if _, ok := result["book_world_context"]; ok {
		items = append(items, "本书世界:ok")
	}
	if _, ok := result["resource_audit"]; ok {
		items = append(items, "资源审计:ok")
	}
	if _, ok := result["character_continuity"]; ok {
		items = append(items, "人物续用:ok")
	}
	if _, ok := result["memory_policy"]; ok {
		items = append(items, "记忆策略:ok")
	}
	if _, ok := result["simulation_profile"]; ok {
		items = append(items, "仿写画像:ok")
	}
	if warnings, ok := result["_warnings"].([]string); ok && len(warnings) > 0 {
		items = append(items, fmt.Sprintf("告警:%d", len(warnings)))
	}
	if trimmed, ok := result["_trimmed"].([]string); ok && len(trimmed) > 0 {
		items = append(items, fmt.Sprintf("裁剪:%s", strings.Join(trimmed, ",")))
	}

	if len(items) > 0 {
		parts = append(parts, strings.Join(items, " "))
	}
	return strings.Join(parts, " | ")
}

// sliceLen 对 any 类型尝试取 slice 长度。
func sliceLen(v any) int {
	switch s := v.(type) {
	case []domain.ChapterSummary:
		return len(s)
	case []domain.ArcSummary:
		return len(s)
	case []domain.VolumeSummary:
		return len(s)
	case []domain.CharacterSnapshot:
		return len(s)
	case []domain.TimelineEvent:
		return len(s)
	case []domain.ForeshadowEntry:
		return len(s)
	case []domain.RelationshipEntry:
		return len(s)
	case []domain.StateChange:
		return len(s)
	case []domain.VolumeOutline:
		return len(s)
	case []domain.Character:
		return len(s)
	case []domain.RelatedChapter:
		return len(s)
	case []domain.RecallItem:
		return len(s)
	case []domain.RAGChunk:
		return len(s)
	default:
		return 0
	}
}

// loadFilteredCharacters 按本章参与者和 Tier 过滤角色。
// 有明确参与者时只返回参与者 + 主角兜底；无参与者时退回旧的 Tier 策略。
func (t *ContextTool) loadFilteredCharacters(result map[string]any, chapter int, participants []string, warn func(string, error)) {
	chars, err := t.store.Characters.Load()
	if err != nil {
		warn("characters", err)
		return
	}
	if len(chars) == 0 {
		return
	}

	// 获取当前章节大纲的场景描述，用于匹配次要角色
	entry, err := t.store.Outline.GetChapterOutline(chapter)
	if err != nil {
		warn("current_chapter_outline", err)
		result["characters"] = chars
		annotateCharacterPsych(result, chars)
		return
	}
	sceneText := strings.Join(entry.Scenes, " ") + " " + entry.CoreEvent + " " + entry.Title

	filtered := filterCharactersForChapter(chars, participants, sceneText)
	result["characters"] = filtered
	annotateCharacterPsych(result, filtered)
}

// annotateCharacterPsych 任一注入角色带定量心理画像时，附一行行为化使用指引。
// DNA 分组按 Exposed → Hidden → Latent 的可见时机使用。
func annotateCharacterPsych(result map[string]any, chars []domain.Character) {
	hasPsych := false
	for _, c := range chars {
		if c.Psych != nil {
			hasPsych = true
			break
		}
	}
	if !hasPsych {
		return
	}
	result["psych_usage"] = "角色 psych 画像使用指引：big_five/values/moral_foundations 等分数用行为与决策展示，不要写形容词标签（如高 N 角色遇挫写生理反应与过度解读，不写'她很焦虑'）；dna.exposed 可直接展示，dna.hidden 只做暗示埋线，dna.latent 未到转折点不得明写；attachment 决定亲密关系里的追-逃模式"
}

func filterCharactersForChapter(chars []domain.Character, participants []string, sceneText string) []domain.Character {
	participantSet := make(map[string]struct{}, len(participants))
	for _, p := range participants {
		if p != "" {
			participantSet[p] = struct{}{}
		}
	}
	if len(participantSet) > 0 {
		var filtered []domain.Character
		for _, c := range chars {
			if _, ok := participantSet[c.Name]; ok {
				filtered = append(filtered, c)
				continue
			}
			if strings.Contains(c.Role, "主角") || (c.Tier == "core" && len(filtered) == 0) {
				filtered = append(filtered, c)
			}
		}
		return uniqueCharacters(filtered)
	}

	var filtered []domain.Character
	for _, c := range chars {
		switch c.Tier {
		case "secondary", "decorative":
			if matchCharacter(sceneText, c) {
				filtered = append(filtered, c)
			}
		default: // core, important, 或未设置
			filtered = append(filtered, c)
		}
	}
	return uniqueCharacters(filtered)
}

func uniqueCharacters(chars []domain.Character) []domain.Character {
	seen := make(map[string]struct{}, len(chars))
	var out []domain.Character
	for _, c := range chars {
		if c.Name == "" {
			continue
		}
		if _, ok := seen[c.Name]; ok {
			continue
		}
		seen[c.Name] = struct{}{}
		out = append(out, c)
	}
	return out
}

// matchCharacter 检查场景文本中是否包含角色的正式名或任一别名。
func matchCharacter(text string, c domain.Character) bool {
	if strings.Contains(text, c.Name) {
		return true
	}
	for _, alias := range c.Aliases {
		if strings.Contains(text, alias) {
			return true
		}
	}
	return false
}

// loadLayeredSummaries 分层摘要加载：卷摘要 + 当前卷弧摘要 + 弧内章摘要。
func (t *ContextTool) loadLayeredSummaries(result map[string]any, chapter, summaryWindow int, warn func(string, error)) {
	vol, arc, err := t.store.Outline.LocateChapter(chapter)
	if err != nil {
		warn("layered_outline_position", err)
		// 回退到扁平模式
		if summaries, err := t.store.Summaries.LoadRecentSummaries(chapter, summaryWindow); err == nil && len(summaries) > 0 {
			result["recent_summaries"] = summaries
		} else {
			warn("recent_summaries", err)
		}
		return
	}

	// 1. 已完成卷的卷摘要
	if volSummaries, err := t.store.Summaries.LoadAllVolumeSummaries(); err == nil && len(volSummaries) > 0 {
		result["volume_summaries"] = volSummaries
	} else {
		warn("volume_summaries", err)
	}

	// 2. 当前卷内已完成弧的弧摘要（不含当前弧）
	if arcSummaries, err := t.store.Summaries.LoadArcSummaries(vol); err == nil && len(arcSummaries) > 0 {
		var prior []domain.ArcSummary
		for _, s := range arcSummaries {
			if s.Arc < arc {
				prior = append(prior, s)
			}
		}
		if len(prior) > 0 {
			result["arc_summaries"] = prior
		}
	} else {
		warn("arc_summaries", err)
	}

	// 3. 当前弧内最近 N 章的章摘要
	if summaries, err := t.store.Summaries.LoadRecentSummaries(chapter, summaryWindow); err == nil && len(summaries) > 0 {
		result["recent_summaries"] = summaries
	} else {
		warn("recent_summaries", err)
	}
}

// loadLayeredCharacters Layered 模式下的角色加载：优先用最近快照，回退到原始设定 + Tier 过滤。
func (t *ContextTool) loadLayeredCharacters(result map[string]any, chapter int, participants []string, warn func(string, error)) {
	snapshots, err := t.store.Characters.LoadLatestSnapshots()
	if err == nil && len(snapshots) > 0 {
		result["character_snapshots"] = filterSnapshotsForChapter(snapshots, participants)
		// 同时保留原始设定中的 core/important 角色（快照可能不含新登场角色）
		t.loadFilteredCharacters(result, chapter, participants, warn)
		return
	}
	warn("character_snapshots", err)
	// 无快照时回退到原始设定
	t.loadFilteredCharacters(result, chapter, participants, warn)
}

func filterSnapshotsForChapter(snapshots []domain.CharacterSnapshot, participants []string) []domain.CharacterSnapshot {
	if len(participants) == 0 {
		return snapshots
	}
	set := make(map[string]struct{}, len(participants))
	for _, p := range participants {
		if p != "" {
			set[p] = struct{}{}
		}
	}
	var out []domain.CharacterSnapshot
	for _, snap := range snapshots {
		if _, ok := set[snap.Name]; ok {
			out = append(out, snap)
		}
	}
	return out
}

// writerReferences 返回写作参考资料。章节 1 返回全量，后续章节裁剪掉不再需要的模板。
func (t *ContextTool) writerReferences(chapter int) map[string]string {
	refs := map[string]string{}
	add := func(k, v string) {
		if v != "" {
			refs[k] = v
		}
	}
	// 渐进式加载：始终保留核心参考，前 3 章额外加载完整写作指南
	add("consistency", t.refs.Consistency)
	add("hook_techniques", t.refs.HookTechniques)
	add("quality_checklist", t.refs.QualityChecklist)
	add("anti_ai_tone", t.refs.AntiAITone) // 去 AI 味判据全程注入，不随章节裁剪
	add("production_playbook", t.refs.ProductionPlaybook)
	add("human_feel_craft", t.refs.HumanFeelCraft)
	add("character_building", t.refs.CharacterBuilding)
	add("emotional_narrative_craft", t.refs.EmotionalNarrativeCraft)
	add("writing_techniques_digest", t.refs.WritingTechniquesDigest)
	add("rag_writing_guidelines", t.refs.RAGWritingGuidelines)
	add("web_reference_guidelines", t.refs.WebReferenceGuidelines)
	add("longform_ai_detector", t.refs.LongformAIDetector)
	if chapter <= 3 {
		add("chapter_guide", t.refs.ChapterGuide)
		add("dialogue_writing", t.refs.DialogueWriting)
		add("style_reference", t.refs.StyleReference)
	}

	// 仅首章加载的补充参考
	if chapter <= 1 {
		add("chapter_template", t.refs.ChapterTemplate)
		add("content_expansion", t.refs.ContentExpansion)
	}
	return refs
}

func (t *ContextTool) architectReferences() map[string]string {
	refs := map[string]string{}
	add := func(k, v string) {
		if v != "" {
			refs[k] = v
		}
	}
	add("outline_template", t.refs.OutlineTemplate)
	add("character_template", t.refs.CharacterTemplate)
	add("longform_planning", t.refs.LongformPlanning)
	add("differentiation", t.refs.Differentiation)
	add("style_reference", t.refs.StyleReference)
	add("arc_templates", t.refs.ArcTemplates)
	add("anti_ai_tone", t.refs.AntiAITone) // architect 大纲去 AI 腔；亦兜 editor 走 Chapter=0 路径
	add("production_playbook", t.refs.ProductionPlaybook)
	add("human_feel_craft", t.refs.HumanFeelCraft)
	add("character_building", t.refs.CharacterBuilding)
	add("emotional_narrative_craft", t.refs.EmotionalNarrativeCraft)
	add("writing_techniques_digest", t.refs.WritingTechniquesDigest)
	add("rag_writing_guidelines", t.refs.RAGWritingGuidelines)
	add("web_reference_guidelines", t.refs.WebReferenceGuidelines)
	add("longform_ai_detector", t.refs.LongformAIDetector)
	return refs
}

// foundationStatus 检查基础设定的完备性，返回缺失项列表。
// 与 save_foundation 工具共用 store.FoundationMissing 判定逻辑，保证 LLM 从
// novel_context 看到的 ready/missing 与 save_foundation 返回的 foundation_ready
// 永远一致（长篇 compass 必需项等细节不会漂移）。
func (t *ContextTool) foundationStatus() map[string]any {
	missing := t.store.FoundationMissing()
	status := map[string]any{"ready": len(missing) == 0}
	if len(missing) > 0 {
		status["missing"] = missing
	}
	return status
}

// ContextSummary 返回当前状态的简要摘要（供日志使用）。
func (t *ContextTool) ContextSummary() string {
	var parts []string
	if p, _ := t.store.Outline.LoadPremise(); p != "" {
		parts = append(parts, "premise:ok")
	}
	if o, _ := t.store.Outline.LoadOutline(); o != nil {
		parts = append(parts, fmt.Sprintf("outline:%d chapters", len(o)))
	}
	if c, _ := t.store.Characters.Load(); c != nil {
		parts = append(parts, fmt.Sprintf("characters:%d", len(c)))
	}
	if len(parts) == 0 {
		return "empty"
	}
	return strings.Join(parts, ", ")
}

// trimByBudget 按优先级裁剪 result，使 JSON 总大小不超过 budget 字节。
// 优先级（从低到高）：references < voice_samples < style_anchors < previous_tail < timeline
//
//	< recent_state_changes < foreshadow_ledger < relationship_state < 其余（不裁剪）
//
// 裁剪的 key 会记录到 result["_trimmed"] 供日志排查。
func trimByBudget(result map[string]any, budget int) {
	// 先测量当前大小
	data, err := json.Marshal(result)
	if err != nil || len(data) <= budget {
		return
	}

	// 按优先级从低到高列出可裁剪的 key
	trimOrder := []string{
		"references",
		"voice_samples",
		"style_anchors",
		"style_rules",
		"writing_engine",
		"retrieval_trace",
		"rag_recall",
		"book_world_context",
		"style_stats",
		"previous_tail",
		"timeline",
		"recent_state_changes",
		"foreshadow_ledger",
		"relationship_state",
	}

	var trimmed []string
	for _, key := range trimOrder {
		if _, ok := result[key]; !ok {
			continue
		}
		deleteContextKey(result, key)
		trimmed = append(trimmed, key)
		data, err = json.Marshal(result)
		if err != nil || len(data) <= budget {
			break
		}
	}
	if len(trimmed) > 0 {
		result["_trimmed"] = trimmed
	}
}

func deleteContextKey(result map[string]any, key string) {
	delete(result, key)
	for _, containerKey := range []string{
		"working_memory",
		"episodic_memory",
		"planning_memory",
		"foundation_memory",
		"reference_pack",
		"selected_memory",
	} {
		section, ok := result[containerKey].(map[string]any)
		if !ok {
			continue
		}
		delete(section, key)
	}
}

// buildRelatedChapters 根据结构化数据反查与当前章相关的历史章节。
// 从伏笔、角色出场、状态变化、关系四个维度推荐，去重后最多返回 5 条。
// 所有数据通过参数传入，不做额外 IO。
func (t *ContextTool) buildRelatedChapters(
	chapter int,
	entry *domain.OutlineEntry,
	foreshadow []domain.ForeshadowEntry,
	relationships []domain.RelationshipEntry,
	stateChanges []domain.StateChange,
) []domain.RelatedChapter {
	const recentWindow = 10
	const maxResults = 5

	seen := make(map[int]struct{})
	var results []domain.RelatedChapter
	add := func(ch int, reason string) {
		if ch <= 0 || ch >= chapter {
			return
		}
		// 最近几章太近，不推荐
		if ch > chapter-recentWindow {
			return
		}
		if _, ok := seen[ch]; ok {
			return
		}
		seen[ch] = struct{}{}
		results = append(results, domain.RelatedChapter{Chapter: ch, Reason: reason})
	}

	// 拼接大纲文本用于关键词匹配
	outlineText := entry.Title + " " + entry.CoreEvent
	for _, s := range entry.Scenes {
		outlineText += " " + s
	}

	// 1. 伏笔反查：活跃伏笔的描述是否与当前章大纲相关
	for _, f := range foreshadow {
		if strings.Contains(outlineText, f.ID) || containsAny(outlineText, strings.Fields(f.Description)) {
			add(f.PlantedAt, fmt.Sprintf("伏笔%s(%s)埋设章", f.ID, truncateRunes(f.Description, 15)))
		}
		if len(results) >= maxResults {
			break
		}
	}

	// 2. 角色出场反查：批量单次遍历，IO 从 O(角色数×章节数) 降为 O(章节数)
	chars, _ := t.store.Characters.Load()
	outlineChars := matchOutlineCharacters(outlineText, chars)
	if len(outlineChars) > 0 {
		appearances := t.store.Summaries.FindCharacterAppearances(outlineChars, chapter, recentWindow)
		for _, name := range outlineChars {
			if len(results) >= maxResults {
				break
			}
			if ch, ok := appearances[name]; ok {
				add(ch, fmt.Sprintf("角色'%s'最后出场章", name))
			}
		}
	}

	// 3. 状态变化反查：在已加载的 slice 上操作，零 IO
	for _, name := range outlineChars {
		if len(results) >= maxResults {
			break
		}
		ch := findLastStateChange(stateChanges, name, chapter)
		if ch > 0 && ch <= chapter-recentWindow {
			add(ch, fmt.Sprintf("'%s'状态变化章", name))
		}
	}

	// 4. 关系反查：当前章涉及的角色对之间关系最后变化
	if len(relationships) > 0 && len(outlineChars) >= 2 {
		charSet := make(map[string]struct{}, len(outlineChars))
		for _, c := range outlineChars {
			charSet[c] = struct{}{}
		}
		for _, r := range relationships {
			if len(results) >= maxResults {
				break
			}
			_, aIn := charSet[r.CharacterA]
			_, bIn := charSet[r.CharacterB]
			if aIn && bIn {
				add(r.Chapter, fmt.Sprintf("%s-%s关系变化", r.CharacterA, r.CharacterB))
			}
		}
	}

	return results
}

// findLastStateChange 在已加载的状态变化列表中查找实体最近一次变化的章节号。
func findLastStateChange(changes []domain.StateChange, entity string, currentChapter int) int {
	for i := len(changes) - 1; i >= 0; i-- {
		if changes[i].Entity == entity && changes[i].Chapter < currentChapter {
			return changes[i].Chapter
		}
	}
	return 0
}

// matchOutlineCharacters 从大纲文本中匹配出场角色名。
func matchOutlineCharacters(text string, chars []domain.Character) []string {
	var matched []string
	for _, c := range chars {
		if strings.Contains(text, c.Name) {
			matched = append(matched, c.Name)
			continue
		}
		for _, alias := range c.Aliases {
			if strings.Contains(text, alias) {
				matched = append(matched, c.Name)
				break
			}
		}
	}
	return matched
}

// containsAny 检查 text 是否包含 words 中的任一词（至少 2 字才匹配，避免噪音）。
func containsAny(text string, words []string) bool {
	for _, w := range words {
		if len([]rune(w)) >= 2 && strings.Contains(text, w) {
			return true
		}
	}
	return false
}

func (t *ContextTool) selectStoryThreads(state contextBuildState) []domain.RecallItem {
	if state.currentEntry == nil {
		return nil
	}
	if len(state.foreshadow) < storyThreadRecallThreshold {
		return nil
	}

	const maxThreads = 5
	var items []domain.RecallItem
	seen := make(map[string]struct{})
	picked := make(map[string]struct{}) // 已选中的伏笔 ID，供账龄回填去重
	add := func(item domain.RecallItem) {
		key := item.Kind + "|" + item.Key + "|" + item.Summary
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		picked[item.Key] = struct{}{}
		items = append(items, item)
	}

	// 1. 相关性召回：与当前章 focus 词重叠的伏笔。
	focusTerms := recallFocusTerms(state.currentEntry, state.chapterPlan)
	focusText := strings.Join(focusTerms, " ")
	for _, entry := range state.foreshadow {
		if !matchesRecallTerms(entry.ID+" "+entry.Description, focusTerms) && !strings.Contains(focusText, entry.ID) {
			continue
		}
		add(domain.RecallItem{
			Kind:    "story_thread",
			Key:     entry.ID,
			Chapter: entry.PlantedAt,
			Reason:  "当前章可能需要承接既有伏笔",
			Summary: fmt.Sprintf("伏笔“%s”埋于第%d章：%s", entry.ID, entry.PlantedAt, truncateRunes(entry.Description, 30)),
		})
		if len(items) >= maxThreads {
			return items
		}
	}

	// 2. 账龄回填：与当前章无关、但久挂未回收的伏笔（最旧优先），补足剩余名额。
	//    补的是相关性召回天然的盲区——独自悬挂太久、却没在本章撞上关键词的那根线。
	for _, entry := range agingForeshadow(state.foreshadow, state.chapter, picked) {
		add(domain.RecallItem{
			Kind:    "story_thread",
			Key:     entry.ID,
			Chapter: entry.PlantedAt,
			Reason:  "伏笔久挂未回收，注意适时推进或回收",
			Summary: fmt.Sprintf("伏笔“%s”埋于第%d章，已 %d 章未回收：%s", entry.ID, entry.PlantedAt, state.chapter-entry.PlantedAt, truncateRunes(entry.Description, 30)),
		})
		if len(items) >= maxThreads {
			break
		}
	}

	return items
}

// agingForeshadow 返回账龄 ≥ foreshadowAgingChapters 的未回收伏笔，按最旧优先排序，
// 跳过 picked 中已被相关性召回选中的。入参 all 已是 active（未回收）列表，故无需再过滤状态。
func agingForeshadow(all []domain.ForeshadowEntry, chapter int, picked map[string]struct{}) []domain.ForeshadowEntry {
	var aging []domain.ForeshadowEntry
	for _, e := range all {
		if _, ok := picked[e.ID]; ok {
			continue
		}
		if e.PlantedAt <= 0 || chapter-e.PlantedAt < foreshadowAgingChapters {
			continue
		}
		aging = append(aging, e)
	}
	sort.SliceStable(aging, func(i, j int) bool {
		return aging[i].PlantedAt < aging[j].PlantedAt
	})
	return aging
}

func (t *ContextTool) selectReviewLessons(chapter int, warn func(string, error)) []domain.RecallItem {
	if chapter <= 1 {
		return nil
	}

	var items []domain.RecallItem
	seen := make(map[string]struct{})
	add := func(item domain.RecallItem) {
		key := item.Summary
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		items = append(items, item)
	}

	appendReview := func(review *domain.ReviewEntry) bool {
		if review == nil {
			return false
		}
		for i, miss := range review.ContractMisses {
			add(domain.RecallItem{
				Kind:    "review_lesson",
				Key:     fmt.Sprintf("review-%d-contract-%d", review.Chapter, i),
				Chapter: review.Chapter,
				Reason:  "最近审阅指出 contract 漏项",
				Summary: fmt.Sprintf("第%d章 contract 漏项：%s", review.Chapter, miss),
			})
			if len(items) >= 3 {
				return true
			}
		}
		for i, issue := range review.Issues {
			switch issue.Severity {
			case "", "warning", "error", "critical":
				add(domain.RecallItem{
					Kind:    "review_lesson",
					Key:     fmt.Sprintf("review-%d-issue-%d", review.Chapter, i),
					Chapter: review.Chapter,
					Reason:  "最近审阅指出需要避免重复问题",
					Summary: fmt.Sprintf("第%d章审阅提醒：%s", review.Chapter, truncateRunes(issue.Description, 36)),
				})
			}
			if len(items) >= 3 {
				return true
			}
		}
		return false
	}

	for ch := chapter - 1; ch >= max(chapter-3, 1); ch-- {
		review, err := t.store.World.LoadReview(ch)
		if err != nil {
			warn("review", err)
			continue
		}
		if appendReview(review) {
			return items
		}
	}

	globalReview, err := t.store.World.LoadLastReview(chapter - 1)
	if err != nil {
		warn("global_review", err)
	} else if appendReview(globalReview) {
		return items
	}
	return items
}

func recallFocusTerms(entry *domain.OutlineEntry, plan *domain.ChapterPlan) []string {
	if entry == nil {
		return nil
	}
	var terms []string
	add := func(v string) {
		v = strings.TrimSpace(v)
		if v != "" {
			terms = append(terms, v)
		}
	}

	add(entry.Title)
	add(entry.CoreEvent)
	add(entry.Hook)
	for _, scene := range entry.Scenes {
		add(scene)
	}
	if plan != nil {
		add(plan.Goal)
		add(plan.Hook)
		for _, point := range plan.Contract.PayoffPoints {
			add(point)
		}
		add(plan.Contract.HookGoal)
		for _, anchor := range plan.Contract.SceneAnchors {
			add(anchor)
		}
	}
	return terms
}

func matchesRecallTerms(text string, terms []string) bool {
	for _, term := range terms {
		term = strings.TrimSpace(term)
		if len([]rune(term)) < 2 {
			continue
		}
		if strings.Contains(text, term) || strings.Contains(term, text) {
			return true
		}
		if hasMeaningfulOverlap(term, text) {
			return true
		}
	}
	return false
}

func hasMeaningfulOverlap(a, b string) bool {
	ar := []rune(strings.TrimSpace(a))
	br := []rune(strings.TrimSpace(b))
	if len(ar) < 5 || len(br) < 5 {
		return false
	}
	shorter := len(ar)
	if len(br) < shorter {
		shorter = len(br)
	}
	threshold := 5
	switch {
	case shorter >= 12:
		threshold = 7
	case shorter >= 9:
		threshold = 6
	}
	return longestCommonSubstringRunes(ar, br) >= threshold
}

const storyThreadRecallThreshold = 6
const storyThreadRecallMinSelected = 2

// foreshadowAgingChapters：一条伏笔自埋设起超过这么多章仍未回收，视为"久挂"。
// 这类伏笔即使与当前章关键词无关，也回填进 story_threads，避免长篇里被彻底遗忘
// （相关性召回天然只看见与本章相关的线，看不见独自悬挂太久的那根）。
// 账龄是纯代码派生的事实（当前章 - 埋设章），只陈述"已挂 N 章未回收"，不下指令。
const foreshadowAgingChapters = 30

func longestCommonSubstringRunes(a, b []rune) int {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	prev := make([]int, len(b)+1)
	best := 0
	for i := 1; i <= len(a); i++ {
		curr := make([]int, len(b)+1)
		for j := 1; j <= len(b); j++ {
			if a[i-1] != b[j-1] {
				continue
			}
			curr[j] = prev[j-1] + 1
			if curr[j] > best {
				best = curr[j]
			}
		}
		prev = curr
	}
	return best
}

// truncateRunes 截断字符串到指定 rune 数。
func truncateRunes(s string, maxRunes int) string {
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes]) + "..."
}
