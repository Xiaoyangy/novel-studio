package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/errs"
	"github.com/chenhongyang/novel-studio/internal/rag"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/voocel/agentcore/schema"
)

// SaveFoundationTool 保存基础设定（premise/outline/characters），Architect 专用。
type SaveFoundationTool struct {
	store           *store.Store
	ragEmbedder     rag.Embedder
	ragVectorWriter rag.VectorWriter
}

func NewSaveFoundationTool(store *store.Store) *SaveFoundationTool {
	return &SaveFoundationTool{store: store}
}

func (t *SaveFoundationTool) WithRAGEmbedder(embedder rag.Embedder) *SaveFoundationTool {
	t.ragEmbedder = embedder
	return t
}

func (t *SaveFoundationTool) WithRAGVectorWriter(writer rag.VectorWriter) *SaveFoundationTool {
	t.ragVectorWriter = writer
	return t
}

func (t *SaveFoundationTool) Name() string { return "save_foundation" }
func (t *SaveFoundationTool) Description() string {
	return "保存小说基础设定（premise/outline/characters/world_rules/book_world/compass 等）。**这是唯一持久化入口**：未经此工具调用保存的内容不会进入 store，只在消息里输出 Markdown/JSON 等于丢失。参数固定为 {type, content, scale?, volume?, arc?}。type 可选 premise / outline / layered_outline / characters / world_rules / book_world / expand_arc / append_volume / update_compass / complete_book。premise 时 content 必须是 Markdown 字符串；其他类型 content 优先直接传 JSON 数组或对象。book_world 保存地图、地点、路线和势力图谱；expand_arc 展开骨架弧的详细章节（需 volume + arc）；append_volume 追加新卷（content 为完整 VolumeOutline JSON，含弧结构）；update_compass 更新终局方向（content 为 StoryCompass JSON）；complete_book 宣告全书完结（content 传空对象 {}，直接推 Phase=Complete；调用前必须先通过终卷判定清单，且无返工队列）。scale 可选，仅允许 short / mid / long。"
}
func (t *SaveFoundationTool) Label() string { return "保存设定" }

// 写工具（跨域更新 Outline/Progress/Characters），禁止并发。
func (t *SaveFoundationTool) ReadOnly(_ json.RawMessage) bool        { return false }
func (t *SaveFoundationTool) ConcurrencySafe(_ json.RawMessage) bool { return false }

func (t *SaveFoundationTool) Schema() map[string]any {
	return schema.Object(
		schema.Property("type", schema.Enum("设定类型", "premise", "outline", "layered_outline", "characters", "world_rules", "book_world", "world_codex", "volume_codex", "expand_arc", "append_volume", "update_compass", "complete_book")).Required(),
		schema.Property("content", map[string]any{
			"description": "内容。premise 传 Markdown 字符串；其他类型直接传 JSON 数组或对象即可，也兼容传 JSON 字符串。expand_arc 时传章节数组。characters 每项可带 psych 定量心理画像（big_five 五维 0-1 / attachment 依恋 / values 价值观 / moral_foundations / cognitive_biases / abilities / dna 显隐突三组事实）。world_rules 每条可带 visibility（formal 显规则 / informal 潜规则 / secret 隐秘规则）与 source（朝廷/江湖/家族/门派）。book_world 的 faction 可带 stance（对主角立场）/ internal_tension（内部矛盾）/ core_values，relation 可带 conflict_type（种族/权力/法律/经济/信仰/资源）与 conflict_state（open_war/cold_war/truce/hidden_hostility/alliance），顶层可带 protagonist_position（主角在矛盾网中的位置）与 vision_pillars / world_pillars（视觉核心与世界运作核心分层）。",
		}).Required(),
		schema.Property("scale", schema.Enum("规划级别", "short", "mid", "long")),
		schema.Property("volume", schema.Int("目标卷序号（expand_arc / volume_codex 时必传）")),
		schema.Property("arc", schema.Int("目标弧序号（仅 expand_arc 时必传）")),
		schema.Property("change_reason", schema.String("world_codex 修订时必传：为什么必须改（正文矛盾/新卷需要/用户指令）")),
		schema.Property("change_evidence", schema.String("world_codex 修订时必传：依据——章节事实、审阅结论或用户原话")),
	)
}

func (t *SaveFoundationTool) Execute(_ context.Context, args json.RawMessage) (json.RawMessage, error) {
	var a struct {
		Type           string          `json:"type"`
		Content        json.RawMessage `json:"content"`
		Scale          string          `json:"scale"`
		Volume         int             `json:"volume"`
		Arc            int             `json:"arc"`
		ChangeReason   string          `json:"change_reason"`
		ChangeEvidence string          `json:"change_evidence"`
	}
	if err := unmarshalToolArgs(args, &a); err != nil {
		return nil, fmt.Errorf("invalid args: %w: %w", errs.ErrToolArgs, err)
	}
	content, err := normalizeFoundationContent(a.Content)
	if err != nil {
		return nil, err
	}
	if a.Scale != "" {
		switch domain.PlanningTier(a.Scale) {
		case domain.PlanningTierShort, domain.PlanningTierMid, domain.PlanningTierLong:
		default:
			return nil, fmt.Errorf("invalid scale %q, expected short/mid/long: %w", a.Scale, errs.ErrToolArgs)
		}
		if err := t.store.RunMeta.SetPlanningTier(domain.PlanningTier(a.Scale)); err != nil {
			return nil, fmt.Errorf("save planning tier: %w: %w", errs.ErrStoreWrite, err)
		}
	}

	result := map[string]any{"saved": true, "type": a.Type, "scale": a.Scale}

	// 写作阶段禁止全量覆盖大纲，只允许增量操作（expand_arc / append_volume）
	if (a.Type == "outline" || a.Type == "layered_outline") && t.isWriting() {
		return nil, fmt.Errorf(
			"写作阶段禁止使用 %s 全量覆盖大纲。请使用 expand_arc 展开骨架弧，或 append_volume 追加新卷: %w", a.Type, errs.ErrToolPrecondition)
	}

	decode := func(typeName string, out any) error {
		return decodeFoundationJSON(typeName, content, out)
	}

	switch a.Type {
	case "premise":
		name := domain.ExtractNovelNameFromPremise(content)
		if err := t.store.Outline.SavePremise(content); err != nil {
			return nil, fmt.Errorf("save premise: %w: %w", errs.ErrStoreWrite, err)
		}
		if name != "" {
			_ = t.store.Progress.SetNovelName(name)
			result["novel_name"] = name
		}
		_ = t.store.Progress.UpdatePhase(domain.PhasePremise)

	case "outline":
		var entries []domain.OutlineEntry
		if err := decode("outline", &entries); err != nil {
			return nil, err
		}
		if err := t.store.Outline.SaveOutline(entries); err != nil {
			return nil, fmt.Errorf("save outline: %w: %w", errs.ErrStoreWrite, err)
		}
		_ = t.store.Progress.UpdatePhase(domain.PhaseOutline)
		_ = t.store.Progress.SetTotalChapters(len(entries))
		if domain.PlanningTier(a.Scale) != domain.PlanningTierLong {
			_ = t.store.Progress.SetLayered(false)
			_ = t.store.Progress.UpdateVolumeArc(0, 0)
			_ = t.store.Outline.ClearLayeredOutline()
		}
		result["chapters"] = len(entries)

	case "layered_outline":
		var volumes []domain.VolumeOutline
		if err := decode("layered_outline", &volumes); err != nil {
			return nil, err
		}
		if err := t.store.Outline.SaveLayeredOutline(volumes); err != nil {
			return nil, fmt.Errorf("save layered_outline: %w: %w", errs.ErrStoreWrite, err)
		}
		flat := domain.FlattenOutline(volumes)
		if err := t.store.Outline.SaveOutline(flat); err != nil {
			return nil, fmt.Errorf("save flattened outline: %w: %w", errs.ErrStoreWrite, err)
		}
		total := domain.TotalChapters(volumes)
		_ = t.store.Progress.UpdatePhase(domain.PhaseOutline)
		_ = t.store.Progress.SetTotalChapters(total)
		_ = t.store.Progress.SetLayered(true)
		if len(volumes) > 0 && len(volumes[0].Arcs) > 0 {
			_ = t.store.Progress.UpdateVolumeArc(volumes[0].Index, volumes[0].Arcs[0].Index)
		}
		result["volumes"] = len(volumes)
		result["chapters"] = total

	case "characters":
		var chars []domain.Character
		if err := decode("characters", &chars); err != nil {
			return nil, err
		}
		if err := t.store.Characters.Save(chars); err != nil {
			return nil, fmt.Errorf("save characters: %w: %w", errs.ErrStoreWrite, err)
		}
		result["count"] = len(chars)

	case "world_rules":
		var rules []domain.WorldRule
		if err := decode("world_rules", &rules); err != nil {
			return nil, err
		}
		if err := t.store.World.SaveWorldRules(rules); err != nil {
			return nil, fmt.Errorf("save world_rules: %w: %w", errs.ErrStoreWrite, err)
		}
		result["count"] = len(rules)

	case "book_world":
		var world domain.BookWorld
		if err := decode("book_world", &world); err != nil {
			return nil, err
		}
		if err := t.store.World.SaveBookWorld(world); err != nil {
			return nil, fmt.Errorf("save book_world: %w: %w", errs.ErrStoreWrite, err)
		}
		result["places"] = len(world.Places)
		result["factions"] = len(world.Factions)

	case "world_codex":
		var codex domain.WorldCodex
		if err := decode("world_codex", &codex); err != nil {
			return nil, err
		}
		if err := t.saveWorldCodex(&codex, a.ChangeReason, a.ChangeEvidence); err != nil {
			return nil, err
		}
		result["version"] = codex.Version
		result["ability_tiers"] = len(codex.AbilityTiers)
		result["sections"] = len(codex.Sections)

	case "volume_codex":
		var vc domain.VolumeCodex
		if err := decode("volume_codex", &vc); err != nil {
			return nil, err
		}
		if a.Volume > 0 && vc.Volume == 0 {
			vc.Volume = a.Volume
		}
		if err := t.saveVolumeCodex(&vc); err != nil {
			return nil, err
		}
		result["volume"] = vc.Volume
		result["tier_ceiling"] = vc.TierCeiling

	case "expand_arc":
		if a.Volume <= 0 || a.Arc <= 0 {
			return nil, fmt.Errorf("expand_arc requires volume and arc parameters: %w", errs.ErrToolArgs)
		}
		var chapters []domain.OutlineEntry
		if err := decode("expand_arc chapters", &chapters); err != nil {
			return nil, err
		}
		if err := t.store.ExpandArc(a.Volume, a.Arc, chapters); err != nil {
			return nil, fmt.Errorf("expand arc: %w: %w", errs.ErrStoreWrite, err)
		}
		result["volume"] = a.Volume
		result["arc"] = a.Arc
		result["chapters"] = len(chapters)

	case "append_volume":
		if p, _ := t.store.Progress.Load(); p != nil && p.Phase == domain.PhaseComplete {
			return nil, fmt.Errorf("全书已完结（phase=complete），不允许追加新卷: %w", errs.ErrToolPrecondition)
		}
		var vol domain.VolumeOutline
		if err := decode("append_volume", &vol); err != nil {
			return nil, err
		}
		if err := t.store.AppendVolume(vol); err != nil {
			return nil, fmt.Errorf("append volume: %w: %w", errs.ErrStoreWrite, err)
		}
		result["volume"] = vol.Index
		result["arcs"] = len(vol.Arcs)
		chCount := 0
		for _, arc := range vol.Arcs {
			chCount += len(arc.Chapters)
		}
		if chCount > 0 {
			result["chapters"] = chCount
		}

	case "complete_book":
		// 全书完结的唯一入口：直接推 Phase=Complete。
		// 仅 Writing 阶段允许，防止规划阶段误调跳过整本写作。
		// 拒绝有返工队列时调用——保证 PendingRewrites 跑完才能结束。
		progress, perr := t.store.Progress.Load()
		if perr != nil {
			return nil, fmt.Errorf("load progress: %w: %w", errs.ErrStoreRead, perr)
		}
		if progress == nil {
			return nil, fmt.Errorf("progress 未初始化: %w", errs.ErrToolPrecondition)
		}
		if progress.Phase != domain.PhaseWriting {
			return nil, fmt.Errorf("complete_book 仅在 writing 阶段可调用（当前 phase=%s）: %w", progress.Phase, errs.ErrToolPrecondition)
		}
		if len(progress.PendingRewrites) > 0 {
			return nil, fmt.Errorf("还有 %d 章在返工队列中，处理完再调 complete_book: %w", len(progress.PendingRewrites), errs.ErrToolPrecondition)
		}
		if unreviewed := t.store.World.FirstUnacceptedChapterReview(progress.CompletedChapters); unreviewed > 0 {
			return nil, fmt.Errorf("第 %d 章尚未通过章级审阅，不能 complete_book: %w", unreviewed, errs.ErrToolPrecondition)
		}
		if !progress.Layered {
			meta, _ := t.store.RunMeta.Load()
			if domain.RequiresFinalGlobalReview(progress, meta) &&
				!t.store.World.HasAcceptedGlobalReview(progress.LatestCompleted()) {
				return nil, fmt.Errorf("短篇/三万字内项目必须先通过 scope=global 的全文终审，不能直接 complete_book: %w", errs.ErrToolPrecondition)
			}
		}
		if err := t.store.Progress.MarkComplete(); err != nil {
			return nil, fmt.Errorf("mark complete: %w: %w", errs.ErrStoreWrite, err)
		}
		result["book_complete"] = true
		result["phase"] = string(domain.PhaseComplete)

	case "update_compass":
		var compass domain.StoryCompass
		if err := decode("compass", &compass); err != nil {
			return nil, err
		}
		// 工具层强制覆盖 LastUpdated 为当前已完成章节数，不信任 LLM 自填。
		// LLM 通常忘填或留 0，会让 diag.CompassDrift 误报、Router 路由失真。
		if p, _ := t.store.Progress.Load(); p != nil {
			compass.LastUpdated = p.LatestCompleted()
		}
		if err := t.store.Outline.SaveCompass(compass); err != nil {
			return nil, fmt.Errorf("save compass: %w: %w", errs.ErrStoreWrite, err)
		}
		result["ending_direction"] = compass.EndingDirection
		result["last_updated"] = compass.LastUpdated

	default:
		return nil, fmt.Errorf("unknown type %q, expected premise/outline/layered_outline/characters/world_rules/book_world/expand_arc/append_volume/update_compass/complete_book: %w", a.Type, errs.ErrToolArgs)
	}

	// checkpoint
	scope := domain.GlobalScope()
	if a.Type == "expand_arc" {
		scope = domain.ArcScope(a.Volume, a.Arc)
	} else if a.Type == "append_volume" {
		scope = domain.GlobalScope()
	}
	if _, err := t.store.Checkpoints.AppendArtifact(scope, a.Type, foundationArtifact(a.Type)); err != nil {
		return nil, fmt.Errorf("checkpoint foundation %s: %w: %w", a.Type, errs.ErrStoreWrite, err)
	}
	ragIndexed, ragErr := t.sedimentFoundationRAG(a.Type, content)
	if ragErr != nil {
		result["rag_error"] = ragErr.Error()
	}
	result["rag_indexed"] = ragIndexed

	// 返回剩余未完成项，引导 Architect 继续或结束；
	// 齐全时一次性把 phase 推进到 writing，避免 Coordinator 再回来派单。
	remaining := t.store.FoundationMissing()
	ready := len(remaining) == 0
	result["remaining"] = remaining
	result["foundation_ready"] = ready
	if ready {
		if p, _ := t.store.Progress.Load(); p != nil &&
			p.Phase != domain.PhaseWriting && p.Phase != domain.PhaseComplete {
			_ = t.store.Progress.UpdatePhase(domain.PhaseWriting)
			result["phase"] = string(domain.PhaseWriting)
		}
	}
	return json.Marshal(result)
}

func (t *SaveFoundationTool) sedimentFoundationRAG(kind, content string) (bool, error) {
	chunks := foundationRAGChunks(kind, content)
	if len(chunks) == 0 {
		return false, nil
	}
	if err := upsertRAGChunks(context.Background(), t.store, t.ragEmbedder, t.ragVectorWriter, chunks, domain.RAGIndexConfig{}); err != nil {
		return true, err
	}
	return true, nil
}

func foundationRAGChunks(kind, content string) []domain.RAGChunk {
	sourcePath := foundationRAGSourcePath(kind)
	if sourcePath == "" || strings.TrimSpace(content) == "" {
		return nil
	}
	facet := "plot"
	sourceKind := "foundation"
	switch kind {
	case "characters":
		facet = "character"
	case "world_rules", "book_world":
		facet = "world"
	case "update_compass":
		facet = "plot"
	case "outline", "layered_outline", "expand_arc", "append_volume":
		facet = "plot"
	}
	return chunksFromRAGText(
		sourcePath,
		sourceKind,
		facet,
		"foundation | "+kind,
		content,
		content,
		[]string{kind},
		map[string]any{"source": "save_foundation", "foundation_type": kind},
		1200,
	)
}

func foundationRAGSourcePath(kind string) string {
	switch kind {
	case "expand_arc", "append_volume", "update_compass":
		return "meta/rag/foundation/" + kind + ".json"
	default:
		return foundationArtifact(kind)
	}
}

func foundationArtifact(t string) string {
	switch t {
	case "premise":
		return "premise.md"
	case "outline":
		return "outline.json"
	case "layered_outline", "expand_arc", "append_volume":
		return "layered_outline.json"
	case "complete_book":
		return "meta/progress.json"
	case "characters":
		return "characters.json"
	case "world_rules":
		return "world_rules.json"
	case "book_world":
		return "book_world.json"
	case "update_compass":
		return "meta/compass.json"
	default:
		return ""
	}
}

// decodeFoundationJSON 解析 save_foundation 的 content 字段，失败时附上行列位置
// 和最常见的修复提示，让 LLM 下一次重试能直接定位而不是盲猜。
func decodeFoundationJSON(typeName, content string, out any) error {
	err := json.Unmarshal([]byte(content), out)
	if err == nil {
		return nil
	}
	hint := `常见原因：字符串值中的双引号未转义为 \", 换行未转义为 \n, 或对象字段间漏了逗号。请整段重新生成一次。`
	if se, ok := err.(*json.SyntaxError); ok {
		line, col := offsetToLineCol(content, int(se.Offset))
		return fmt.Errorf("parse %s JSON (line %d col %d): %w — %s", typeName, line, col, err, hint)
	}
	return fmt.Errorf("parse %s JSON: %w — %s", typeName, err, hint)
}

func offsetToLineCol(s string, offset int) (int, int) {
	if offset < 0 {
		offset = 0
	}
	if offset > len(s) {
		offset = len(s)
	}
	line, col := 1, 1
	for i := 0; i < offset; i++ {
		if s[i] == '\n' {
			line++
			col = 1
		} else {
			col++
		}
	}
	return line, col
}

func normalizeFoundationContent(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", fmt.Errorf("content is required: %w", errs.ErrToolArgs)
	}

	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text, nil
	}

	if !json.Valid(raw) {
		return "", fmt.Errorf("invalid content: expected Markdown string or valid JSON value: %w", errs.ErrToolArgs)
	}
	return string(raw), nil
}

func (t *SaveFoundationTool) isWriting() bool {
	p, _ := t.store.Progress.Load()
	return p != nil && p.Phase == domain.PhaseWriting
}

// saveWorldCodex 校验并保存全局世界法典。已有法典时强制走修订流程：
// 必须带 change_reason + change_evidence，版本自增并落 change_log——世界硬设定
// 不允许随写作漂移。
func (t *SaveFoundationTool) saveWorldCodex(codex *domain.WorldCodex, changeReason, changeEvidence string) error {
	var missing []string
	require := func(ok bool, field string) {
		if !ok {
			missing = append(missing, field)
		}
	}
	require(len(codex.AbilityTiers) > 0, "ability_tiers")
	for i, tier := range codex.AbilityTiers {
		prefix := fmt.Sprintf("ability_tiers[%d]", i)
		require(strings.TrimSpace(tier.Name) != "", prefix+".name")
		require(strings.TrimSpace(tier.Magnitude) != "", prefix+".magnitude")
		require(strings.TrimSpace(tier.Limits) != "", prefix+".limits")
		require(strings.TrimSpace(tier.Promotion) != "", prefix+".promotion")
	}
	require(len(codex.SkillDomains) > 0, "skill_domains")
	require(len(codex.Races) > 0, "races（现代/单种族题材至少登记「人类」并写明约束）")
	require(len(codex.WeaponCategories) > 0, "weapon_categories（现代题材至少登记现实器械类并说明对诡异/超凡无效的边界）")
	require(len(codex.EquipmentCategories) > 0, "equipment_categories")
	require(strings.TrimSpace(codex.ImmutabilityPolicy) != "", "immutability_policy")

	// 覆盖清单：每个世界维度要么有内容，要么显式 not_applicable + 理由。
	sectionByKey := map[string]domain.CodexSection{}
	for _, sec := range codex.Sections {
		sectionByKey[strings.TrimSpace(sec.Key)] = sec
	}
	for _, want := range domain.RequiredCodexSections {
		sec, ok := sectionByKey[want.Key]
		if !ok {
			missing = append(missing, "sections."+want.Key+"（"+want.Title+"）")
			continue
		}
		if sec.NotApplicable {
			require(strings.TrimSpace(sec.Reason) != "", "sections."+want.Key+".reason(not_applicable 必须给理由)")
			continue
		}
		require(strings.TrimSpace(sec.Content) != "" || len(sec.Rules) > 0, "sections."+want.Key+".content/rules")
	}
	if len(missing) > 0 {
		return fmt.Errorf("world_codex 不完整，缺少：%s：世界必须像真实世界一样自洽，每个维度要么设定、要么显式 not_applicable+理由: %w",
			strings.Join(missing, ", "), errs.ErrToolPrecondition)
	}

	existing, err := t.store.LoadWorldCodex()
	if err != nil {
		return fmt.Errorf("load world_codex: %w: %w", errs.ErrStoreRead, err)
	}
	now := time.Now().Format(time.RFC3339)
	if existing == nil {
		codex.Version = 1
	} else {
		if strings.TrimSpace(changeReason) == "" || strings.TrimSpace(changeEvidence) == "" {
			return fmt.Errorf("world_codex 已存在（v%d）且不可随意更改：修订必须提供 change_reason 和 change_evidence（正文矛盾/新卷需要/用户指令 + 具体依据）: %w",
				existing.Version, errs.ErrToolPrecondition)
		}
		codex.Version = existing.Version + 1
		codex.ChangeLog = append(append([]domain.CodexChange(nil), existing.ChangeLog...), domain.CodexChange{
			At:       now,
			Version:  codex.Version,
			Reason:   changeReason,
			Evidence: changeEvidence,
			Fields:   diffWorldCodexFields(existing, codex),
		})
	}
	codex.GeneratedAt = now
	if err := t.store.SaveWorldCodex(*codex); err != nil {
		return fmt.Errorf("save world_codex: %w: %w", errs.ErrStoreWrite, err)
	}
	return nil
}

// saveVolumeCodex 校验并保存卷级上限；分级/门类名必须能在全局法典中找到。
func (t *SaveFoundationTool) saveVolumeCodex(vc *domain.VolumeCodex) error {
	if vc.Volume <= 0 {
		return fmt.Errorf("volume_codex 必须指定 volume: %w", errs.ErrToolArgs)
	}
	if strings.TrimSpace(vc.TierCeiling) == "" || strings.TrimSpace(vc.ProtagonistCeiling) == "" {
		return fmt.Errorf("volume_codex 必须写明 tier_ceiling 与 protagonist_ceiling: %w", errs.ErrToolPrecondition)
	}
	codex, err := t.store.LoadWorldCodex()
	if err != nil {
		return fmt.Errorf("load world_codex: %w: %w", errs.ErrStoreRead, err)
	}
	if codex == nil {
		return fmt.Errorf("volume_codex 依赖全局 world_codex：请先用 save_foundation type=world_codex 敲定全局能力分级: %w", errs.ErrToolPrecondition)
	}
	tierNames := map[string]bool{}
	for _, tier := range codex.AbilityTiers {
		tierNames[strings.TrimSpace(tier.Name)] = true
	}
	if !tierNames[strings.TrimSpace(vc.TierCeiling)] {
		return fmt.Errorf("volume_codex.tier_ceiling %q 不在 world_codex.ability_tiers 中；卷上限必须引用全局分级名: %w", vc.TierCeiling, errs.ErrToolPrecondition)
	}
	if !tierNames[strings.TrimSpace(vc.ProtagonistCeiling)] {
		return fmt.Errorf("volume_codex.protagonist_ceiling %q 不在 world_codex.ability_tiers 中: %w", vc.ProtagonistCeiling, errs.ErrToolPrecondition)
	}
	domainNames := map[string]bool{}
	for _, d := range codex.SkillDomains {
		domainNames[strings.TrimSpace(d.Name)] = true
	}
	for _, name := range vc.AllowedSkillDomains {
		if !domainNames[strings.TrimSpace(name)] {
			return fmt.Errorf("volume_codex.allowed_skill_domains 含未登记门类 %q；先在 world_codex.skill_domains 补登记: %w", name, errs.ErrToolPrecondition)
		}
	}
	vc.GeneratedAt = time.Now().Format(time.RFC3339)
	if err := t.store.SaveVolumeCodex(*vc); err != nil {
		return fmt.Errorf("save volume_codex: %w: %w", errs.ErrStoreWrite, err)
	}
	return nil
}

// diffWorldCodexFields 粗粒度列出两版法典的差异部分（change_log 用）。
func diffWorldCodexFields(old *domain.WorldCodex, next *domain.WorldCodex) []string {
	var fields []string
	add := func(name string, changed bool) {
		if changed {
			fields = append(fields, name)
		}
	}
	marshal := func(v any) string {
		data, _ := json.Marshal(v)
		return string(data)
	}
	add("ability_tiers", marshal(old.AbilityTiers) != marshal(next.AbilityTiers))
	add("skill_domains", marshal(old.SkillDomains) != marshal(next.SkillDomains))
	add("races", marshal(old.Races) != marshal(next.Races))
	add("weapon_categories", marshal(old.WeaponCategories) != marshal(next.WeaponCategories))
	add("equipment_categories", marshal(old.EquipmentCategories) != marshal(next.EquipmentCategories))
	add("sections", marshal(old.Sections) != marshal(next.Sections))
	if len(fields) == 0 {
		fields = append(fields, "metadata_only")
	}
	return fields
}
