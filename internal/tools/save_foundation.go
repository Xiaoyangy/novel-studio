package tools

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/errs"
	"github.com/chenhongyang/novel-studio/internal/rag"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/voocel/agentcore/schema"
)

// SaveFoundationTool 保存基础设定（premise/outline/characters），Architect 专用。
type SaveFoundationTool struct {
	store                             *store.Store
	ragEmbedder                       rag.Embedder
	ragVectorWriter                   rag.VectorWriter
	allowChapterZeroFoundationRefresh bool
	allowedFoundationType             string
	recordFoundationRefreshEpoch      bool
	oneShotFoundationRefresh          bool
	foundationRefreshConsumed         atomic.Bool
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

// WithChapterZeroFoundationRefresh grants a process-local capability used only
// by the explicit pipeline Architect refresh. The model cannot set this flag:
// save_foundation still verifies the live foundation execution lease and the
// absence of every chapter-generation artifact before replacing an outline in
// PhaseWriting.
func (t *SaveFoundationTool) WithChapterZeroFoundationRefresh(allow bool) *SaveFoundationTool {
	t.allowChapterZeroFoundationRefresh = allow
	return t
}

// WithFoundationTypeRestriction binds a refresh sidecar to one exact
// save_foundation mutation. Empty preserves the ordinary Architect tool.
func (t *SaveFoundationTool) WithFoundationTypeRestriction(kind string) *SaveFoundationTool {
	t.allowedFoundationType = strings.TrimSpace(kind)
	return t
}

func (t *SaveFoundationTool) WithFoundationRefreshEpoch(allow bool) *SaveFoundationTool {
	t.recordFoundationRefreshEpoch = allow
	return t
}

func (t *SaveFoundationTool) WithOneShotFoundationRefresh(allow bool) *SaveFoundationTool {
	t.oneShotFoundationRefresh = allow
	return t
}

func (t *SaveFoundationTool) Name() string { return "save_foundation" }
func (t *SaveFoundationTool) Description() string {
	return "保存小说基础设定（premise/outline/characters/world_rules/book_world/compass 等）。**这是唯一持久化入口**：未经此工具调用保存的内容不会进入 store，只在消息里输出 Markdown/JSON 等于丢失。参数固定为 {type, content, scale?, volume?, arc?}。type 可选 premise / outline / layered_outline / characters / world_rules / book_world / append_volume / map_contracts / expand_arc / revise_arc / update_compass / complete_book。premise 时 content 必须是 Markdown 字符串；其他类型 content 优先直接传 JSON 数组或对象。map_contracts 只在 outline-all chapter0 receipt 下为完整弧集分配结构化终局/长线回执；expand_arc 展开骨架弧的详细章节（需 volume + arc）；revise_arc 只能在 outline-all chapter0 receipt 下原位替换已展开弧，严禁改变 span；append_volume 追加新卷；update_compass 更新终局方向；complete_book 宣告全书完结。scale 可选，仅允许 short / mid / long。"
}
func (t *SaveFoundationTool) Label() string { return "保存设定" }

// 写工具（跨域更新 Outline/Progress/Characters），禁止并发。
func (t *SaveFoundationTool) ReadOnly(_ json.RawMessage) bool        { return false }
func (t *SaveFoundationTool) ConcurrencySafe(_ json.RawMessage) bool { return false }

func (t *SaveFoundationTool) Schema() map[string]any {
	return schema.Object(
		schema.Property("type", schema.Enum("设定类型", "premise", "outline", "layered_outline", "characters", "world_rules", "book_world", "world_codex", "volume_codex", "append_volume", "map_contracts", "expand_arc", "revise_arc", "update_compass", "complete_book")).Required(),
		// content 语义上必填，但不进 JSON-schema required：长内容被截断时
		// 参数会整体失效为空，schema 层的 InputValidationError 只会说"缺参数"，
		// 模型无从修复。放行到 Execute 由 normalizeFoundationContent 给出
		// 可执行的修复提示（压缩篇幅重发），错误信息可控。
		schema.Property("content", map[string]any{
			"description": "内容（必填）。premise 传 Markdown 字符串；其他类型直接传 JSON 数组或对象即可，也兼容传 JSON 字符串。update_compass 的 estimated_scale 若供 outline-all 消费，必须同时包含 x-y卷与x-y章的显式数字范围，其中 x-y章 必须是全书总章数范围（严禁把“每弧/每卷 8-16 章”这类单元预算写成全书章数范围；如需注明可写“每弧8-16章”，但全书总章数必须另有独立的 x-y章 范围）；固定单卷12章也写成1-1卷、12-12章。layered_outline 若供 outline-all 消费，每个弧必须覆盖 8-16 章；8-16 章短篇应使用一卷一弧，不得拆成多个不足 8 章的短弧。expand_arc 时传章节数组。characters 每项可带 psych 定量心理画像（big_five 五维 0-1 / attachment 依恋 / values 价值观 / moral_foundations / cognitive_biases / abilities / dna 显隐突三组事实）。world_rules 每条可带 visibility（formal 显规则 / informal 潜规则 / secret 隐秘规则）与 source（朝廷/江湖/家族/门派）。book_world 的 faction 必须带 clock（{segments, progress, consequence, pace}，Blades 式势力进度钟），也可带 aliases（后续 save_world_tick 的自然称呼/组织简称必须能落到此处）、stance（对主角立场）/ internal_tension（内部矛盾）/ core_values；relation.target 必须指向已存在 faction 的 id/name/aliases，不得悬空；relation 可带 conflict_type（种族/权力/法律/经济/信仰/资源）与 conflict_state（open_war/cold_war/truce/hidden_hostility/alliance）。book_world 顶层形状严格固定：protagonist_position 是字符串；vision_pillars 是对象 {color_palette:[], signature_elements:[], lighting:\"\", signature_scenes:[]}；world_pillars 是对象 {economic:{base,controlled_by,tension}, cultural:{base,controlled_by,tension}, political:{base,controlled_by,tension}, historical:{base,controlled_by,tension}}；两个 pillars 均不得传数组。",
		}),
		schema.Property("scale", schema.Enum("规划级别", "short", "mid", "long")),
		schema.Property("volume", schema.Int("目标卷序号（expand_arc / revise_arc / outline-all append_volume / volume_codex 时必传）")),
		schema.Property("arc", schema.Int("目标弧序号（expand_arc / revise_arc 时必传）")),
		schema.Property("change_reason", schema.String("world_codex 修订时必传：为什么必须改（正文矛盾/新卷需要/用户指令）")),
		schema.Property("change_evidence", schema.String("world_codex 修订时必传：依据——章节事实、审阅结论或用户原话")),
	)
}

func (t *SaveFoundationTool) Execute(ctx context.Context, args json.RawMessage) (out json.RawMessage, returnErr error) {
	if err := guardPipelineGlobalPlanningExecution(t.store, t.Name()); err != nil {
		return nil, err
	}
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
	if t.allowedFoundationType != "" && a.Type != t.allowedFoundationType {
		return nil, fmt.Errorf(
			"foundation refresh sidecar only allows save_foundation(type=%q); received %q: %w",
			t.allowedFoundationType,
			a.Type,
			errs.ErrToolPrecondition,
		)
	}
	if t.oneShotFoundationRefresh {
		if !t.foundationRefreshConsumed.CompareAndSwap(false, true) {
			return nil, fmt.Errorf("foundation refresh sidecar has already completed its one allowed mutation: %w", errs.ErrToolPrecondition)
		}
		defer func() {
			if returnErr != nil {
				t.foundationRefreshConsumed.Store(false)
			}
		}()
	}
	if err := guardOutlineAllFoundationType(t.store, a.Type, a.Scale); err != nil {
		return nil, err
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
		if t.allowedFoundationType != "" {
			meta, err := t.store.RunMeta.Load()
			if err != nil {
				return nil, fmt.Errorf("load planning tier for exact foundation refresh: %w", err)
			}
			if meta == nil || meta.PlanningTier == "" || meta.PlanningTier != domain.PlanningTier(a.Scale) {
				current := domain.PlanningTier("")
				if meta != nil {
					current = meta.PlanningTier
				}
				return nil, fmt.Errorf(
					"exact foundation refresh cannot change planning tier from %q to %q: %w",
					current,
					a.Scale,
					errs.ErrToolPrecondition,
				)
			}
		}
	}

	result := map[string]any{"saved": true, "type": a.Type, "scale": a.Scale}

	// Any writing/canon evidence makes full outline replacement exceptional.
	// Progress read failures and hidden draft/planning artifacts fail closed.
	if a.Type == "outline" || a.Type == "layered_outline" {
		requiresAuthority, inspectErr := t.outlineReplacementRequiresAuthorization()
		if inspectErr != nil {
			return nil, inspectErr
		}
		if requiresAuthority && !t.chapterZeroOutlineReplacementAuthorized(a.Type) {
			return nil, fmt.Errorf(
				"写作或已有正史证据的项目禁止使用 %s 全量覆盖大纲。请使用 expand_arc/append_volume，或由宿主显式授权章零 refresh/rebase: %w",
				a.Type,
				errs.ErrToolPrecondition,
			)
		}
	}
	if a.Scale != "" && t.allowedFoundationType == "" {
		if err := t.store.RunMeta.SetPlanningTier(domain.PlanningTier(a.Scale)); err != nil {
			return nil, fmt.Errorf("save planning tier: %w: %w", errs.ErrStoreWrite, err)
		}
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
		if err := validateLightheartedOutlineTitles(t.store, entries); err != nil {
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
		if err := validateLightheartedLayeredTitles(t.store, volumes); err != nil {
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
		// LLM（尤其 MiniMax）惯用 name/key 做键的对象表达这些列表，先确定性
		// 归一化成约定数组形状，避免逐字段报错重发 14KB 的收敛循环。
		if err := decodeFoundationJSON("world_codex", normalizeWorldCodexContent(content), &codex); err != nil {
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

	case "map_contracts":
		var assignments []domain.ArcContractAssignment
		if err := decode("map_contracts", &assignments); err != nil {
			return nil, err
		}
		volumes, err := t.store.Outline.LoadLayeredOutline()
		if err != nil {
			return nil, err
		}
		if err := guardOutlineAllFoundationMutation(t.store, domain.OutlineAllPendingAction{
			Type:                domain.OutlineAllActionMapContracts,
			ExpectedChapterSpan: domain.TotalChapters(volumes),
		}); err != nil {
			return nil, err
		}
		if err := validateOutlineAllMapContractsContent(t.store, assignments); err != nil {
			return nil, err
		}
		if err := t.store.MapArcContracts(assignments); err != nil {
			return nil, fmt.Errorf("map arc contracts: %w: %w", errs.ErrStoreWrite, err)
		}
		result["assignments"] = len(assignments)

	case "expand_arc":
		if a.Volume <= 0 || a.Arc <= 0 {
			return nil, fmt.Errorf("expand_arc requires volume and arc parameters: %w", errs.ErrToolArgs)
		}
		var chapters []domain.OutlineEntry
		if err := decode("expand_arc chapters", &chapters); err != nil {
			return nil, err
		}
		if err := validateLightheartedOutlineTitles(t.store, chapters); err != nil {
			return nil, err
		}
		if err := guardOutlineAllFoundationMutation(t.store, domain.OutlineAllPendingAction{
			Type:                domain.OutlineAllActionExpandArc,
			Volume:              a.Volume,
			Arc:                 a.Arc,
			ExpectedChapterSpan: len(chapters),
		}); err != nil {
			return nil, err
		}
		if err := validateOutlineAllArcMutationContent(
			t.store, domain.OutlineAllActionExpandArc, a.Volume, a.Arc, chapters,
		); err != nil {
			return nil, err
		}
		if err := t.store.ExpandArc(a.Volume, a.Arc, chapters); err != nil {
			return nil, fmt.Errorf("expand arc: %w: %w", errs.ErrStoreWrite, err)
		}
		result["volume"] = a.Volume
		result["arc"] = a.Arc
		result["chapters"] = len(chapters)

	case "revise_arc":
		if a.Volume <= 0 || a.Arc <= 0 {
			return nil, fmt.Errorf("revise_arc requires volume and arc parameters: %w", errs.ErrToolArgs)
		}
		var chapters []domain.OutlineEntry
		if err := decode("revise_arc chapters", &chapters); err != nil {
			return nil, err
		}
		if err := validateLightheartedOutlineTitles(t.store, chapters); err != nil {
			return nil, err
		}
		if err := guardOutlineAllFoundationMutation(t.store, domain.OutlineAllPendingAction{
			Type:                domain.OutlineAllActionReviseArc,
			Volume:              a.Volume,
			Arc:                 a.Arc,
			ExpectedChapterSpan: len(chapters),
		}); err != nil {
			return nil, err
		}
		if err := validateOutlineAllArcMutationContent(
			t.store, domain.OutlineAllActionReviseArc, a.Volume, a.Arc, chapters,
		); err != nil {
			return nil, err
		}
		if err := t.store.ReviseArc(a.Volume, a.Arc, chapters); err != nil {
			return nil, fmt.Errorf("revise arc: %w: %w", errs.ErrStoreWrite, err)
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
		chapterSpan := 0
		for _, arc := range vol.Arcs {
			chapterSpan += arc.ChapterSpan()
		}
		if err := guardOutlineAllFoundationMutation(t.store, domain.OutlineAllPendingAction{
			Type:                domain.OutlineAllActionAppendVolume,
			Volume:              vol.Index,
			ExpectedVolumeIndex: vol.Index,
			ExpectedChapterSpan: chapterSpan,
		}); err != nil {
			return nil, err
		}
		outlineAll, err := validateOutlineAllAppendVolumeContent(t.store, vol)
		if err != nil {
			return nil, err
		}
		if err := validateLightheartedLayeredTitles(t.store, []domain.VolumeOutline{vol}); err != nil {
			return nil, err
		}
		if outlineAll {
			err = t.store.AppendVolumeSkeleton(vol)
		} else {
			err = t.store.AppendVolume(vol)
		}
		if err != nil {
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
		} else if outlineAll {
			result["reserved_chapters"] = chapterSpan
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
		return nil, fmt.Errorf("unknown type %q, expected premise/outline/layered_outline/characters/world_rules/book_world/append_volume/map_contracts/expand_arc/revise_arc/update_compass/complete_book: %w", a.Type, errs.ErrToolArgs)
	}

	// checkpoint
	scope := domain.GlobalScope()
	if a.Type == "expand_arc" || a.Type == "revise_arc" {
		scope = domain.ArcScope(a.Volume, a.Arc)
	} else if a.Type == "append_volume" {
		scope = domain.GlobalScope()
	}
	if _, err := t.store.Checkpoints.AppendArtifact(scope, a.Type, foundationArtifact(a.Type)); err != nil {
		return nil, fmt.Errorf("checkpoint foundation %s: %w: %w", a.Type, errs.ErrStoreWrite, err)
	}
	if t.recordFoundationRefreshEpoch {
		digest, err := FoundationRefreshArtifactsDigest(t.store.Dir(), a.Type)
		if err != nil {
			return nil, fmt.Errorf("digest foundation refresh %s: %w: %w", a.Type, errs.ErrStoreRead, err)
		}
		if _, err := t.store.Checkpoints.AppendAlways(
			scope,
			FoundationRefreshCheckpointStep(a.Type),
			strings.Join(foundationRefreshArtifacts(a.Type), "+"),
			digest,
		); err != nil {
			return nil, fmt.Errorf("checkpoint foundation refresh %s: %w: %w", a.Type, errs.ErrStoreWrite, err)
		}
	}
	outlineAllMode, modeErr := outlineAllExecutionModeActive(t.store)
	if modeErr != nil {
		return nil, modeErr
	}
	result["outline_all"] = outlineAllMode
	ragIndexed := false
	if !outlineAllMode {
		var ragErr error
		ragIndexed, ragErr = t.sedimentFoundationRAG(ctx, a.Type, content)
		if ragErr != nil {
			result["rag_error"] = ragErr.Error()
		}
	}
	result["rag_indexed"] = ragIndexed

	// 返回剩余未完成项，引导 Architect 继续或结束；
	// 齐全时一次性把 phase 推进到 writing，避免 Coordinator 再回来派单。
	remaining := FoundationCoreMissing(t.store.Dir())
	ready := len(remaining) == 0
	result["remaining"] = remaining
	result["foundation_ready"] = ready
	if ready && !outlineAllMode {
		if p, _ := t.store.Progress.Load(); p != nil &&
			p.Phase != domain.PhaseWriting && p.Phase != domain.PhaseComplete {
			_ = t.store.Progress.UpdatePhase(domain.PhaseWriting)
			result["phase"] = string(domain.PhaseWriting)
		}
	}
	return json.Marshal(result)
}

func (t *SaveFoundationTool) sedimentFoundationRAG(ctx context.Context, kind, content string) (bool, error) {
	chunks := foundationRAGChunks(kind, content)
	if len(chunks) == 0 {
		return false, nil
	}
	if err := upsertRAGChunks(ctx, t.store, t.ragEmbedder, t.ragVectorWriter, chunks, domain.RAGIndexConfig{}); err != nil {
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
	case "world_codex":
		return "world_codex.json"
	case "update_compass":
		return "meta/compass.json"
	default:
		return ""
	}
}

func FoundationRefreshCheckpointStep(kind string) string {
	return "foundation_refresh:" + strings.TrimSpace(kind)
}

func foundationRefreshArtifacts(kind string) []string {
	switch strings.TrimSpace(kind) {
	case "layered_outline":
		return []string{"layered_outline.json", "outline.json"}
	case "premise", "characters", "world_rules", "book_world", "world_codex", "update_compass":
		if artifact := foundationArtifact(kind); artifact != "" {
			return []string{artifact}
		}
	}
	return nil
}

// FoundationRefreshArtifactsDigest binds a successful refresh epoch to every
// final artifact produced by that exact mutation. layered_outline includes the
// synchronized flat outline, so a partial two-file save cannot satisfy it.
func FoundationRefreshArtifactsDigest(dir, kind string) (string, error) {
	artifacts := foundationRefreshArtifacts(kind)
	if len(artifacts) == 0 {
		return "", fmt.Errorf("unsupported foundation refresh type %q", kind)
	}
	h := sha256.New()
	for _, rel := range artifacts {
		raw, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil {
			return "", err
		}
		_, _ = h.Write([]byte(rel))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write(raw)
		_, _ = h.Write([]byte{0})
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

// decodeFoundationJSON 解析 save_foundation 的 content 字段。严格解析失败时
// 先做一次确定性修复（尾逗号、字符串内裸控制字符——LLM 长 JSON 的高频失误）
// 再试；仍失败才把行列位置和修复提示回给 LLM，让重试能直接定位而不是盲猜。
func decodeFoundationJSON(typeName, content string, out any) error {
	err := json.Unmarshal([]byte(content), out)
	if err == nil {
		return nil
	}
	if repaired, changed := repairLooseJSON(content); changed {
		if json.Unmarshal([]byte(repaired), out) == nil {
			return nil
		}
	}
	// 反射式形状归一化：LLM 高频把对象写成数组、标量写成数组、单元素结构裸写成对象
	// （book_world 的 map_notes/pillars 即此类）。与 plan 路径同一套，能自愈就不必回退
	// 让模型重发、省一次 codex/LLM 往返。仅当 out 是指针时可取目标类型。
	if rv := reflect.ValueOf(out); rv.Kind() == reflect.Pointer && !rv.IsNil() {
		if coerced, changed := coerceJSONShape(json.RawMessage(content), rv.Elem().Type()); changed {
			if json.Unmarshal(coerced, out) == nil {
				return nil
			}
		}
	}
	shape := foundationShapeHint(typeName)
	hint := `常见原因：字符串值中的双引号未转义为 \", 换行未转义为 \n, 或对象字段间漏了逗号。请整段重新生成一次。` + shape
	if se, ok := err.(*json.SyntaxError); ok {
		line, col := offsetToLineCol(content, int(se.Offset))
		return fmt.Errorf("parse %s JSON (line %d col %d): %w — %s", typeName, line, col, err, hint)
	}
	// 类型不匹配单独给字段级提示：默认 hint 只讲转义/逗号，会把重试引向错误方向。
	if te := (*json.UnmarshalTypeError)(nil); errors.As(err, &te) {
		return fmt.Errorf("parse %s JSON: 字段 %q 期望 %s，实际收到 %s — 请只修正该字段的形状（其余保持不变）后整段重发%s",
			typeName, te.Field, jsonShapeName(te.Type), te.Value, shape)
	}
	return fmt.Errorf("parse %s JSON: %w — %s", typeName, err, hint)
}

// worldCodexArrayFields world_codex 里约定为数组的字段 → 元素身份键。
// LLM 高频把它们写成 {身份: {…}} 的对象；归一化时把身份注入回元素。
var worldCodexArrayFields = map[string]string{
	"ability_tiers":        "name",
	"skill_domains":        "name",
	"races":                "name",
	"weapon_categories":    "name",
	"equipment_categories": "name",
	"sections":             "key",
}

// worldCodexListItemFields 元素内约定为 []string 的键；模型常给单个字符串。
var worldCodexListItemFields = map[string]bool{
	"constraints": true, "traits": true, "grades": true,
	"aliases": true, "samples": true, "rules": true,
}

// normalizeWorldCodexContent 在严格解析前对 world_codex 内容做确定性形状归一化：
//  1. 六个列表字段：对象 → 数组（键注入为 name/key，按键排序保证确定性）；
//  2. 元素内 constraints/traits/grades/aliases/samples/rules：字符串 → 单元素数组；
//  3. immutability_policy：非字符串 → 压缩 JSON 字符串。
//
// 任一步解析失败即放弃归一化原样返回，让 decodeFoundationJSON 的错误提示接手。
func normalizeWorldCodexContent(content string) string {
	var root map[string]json.RawMessage
	if json.Unmarshal([]byte(content), &root) != nil {
		return content
	}
	changed := false
	for field, idKey := range worldCodexArrayFields {
		raw, ok := root[field]
		if !ok {
			continue
		}
		normalized, didChange := normalizeCodexListField(raw, idKey)
		if didChange {
			root[field] = normalized
			changed = true
		}
	}
	if raw, ok := root["immutability_policy"]; ok {
		trimmed := bytes.TrimSpace(raw)
		if len(trimmed) > 0 && trimmed[0] != '"' {
			if quoted, err := json.Marshal(string(trimmed)); err == nil {
				root["immutability_policy"] = quoted
				changed = true
			}
		}
	}
	if !changed {
		return content
	}
	out, err := json.Marshal(root)
	if err != nil {
		return content
	}
	return string(out)
}

// normalizeCodexListField 处理单个列表字段：对象转数组 + 元素内 []string 键收敛。
func normalizeCodexListField(raw json.RawMessage, idKey string) (json.RawMessage, bool) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return raw, false
	}
	changed := false
	var items []map[string]json.RawMessage
	switch trimmed[0] {
	case '{':
		var m map[string]json.RawMessage
		if json.Unmarshal(trimmed, &m) != nil {
			return raw, false
		}
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			item := map[string]json.RawMessage{}
			v := bytes.TrimSpace(m[k])
			if len(v) > 0 && v[0] == '{' {
				if json.Unmarshal(v, &item) != nil {
					return raw, false
				}
			} else {
				// 值不是对象（如 "sections":{"key":"一段设定文本"}）：文本落到内容位。
				contentKey := "content"
				if idKey == "name" {
					contentKey = "description"
				}
				item[contentKey] = m[k]
			}
			if _, has := item[idKey]; !has {
				kj, _ := json.Marshal(k)
				item[idKey] = kj
			}
			items = append(items, item)
		}
		changed = true
	case '[':
		var arr []json.RawMessage
		if json.Unmarshal(trimmed, &arr) != nil {
			return raw, false
		}
		for _, el := range arr {
			item := map[string]json.RawMessage{}
			if json.Unmarshal(el, &item) != nil {
				return raw, false // 数组元素不是对象，交给严格解析报错
			}
			items = append(items, item)
		}
	default:
		return raw, false
	}
	for _, item := range items {
		for k, v := range item {
			if !worldCodexListItemFields[k] {
				continue
			}
			vt := bytes.TrimSpace(v)
			if len(vt) > 0 && vt[0] == '"' {
				item[k] = json.RawMessage("[" + string(vt) + "]")
				changed = true
			}
		}
	}
	if !changed {
		return raw, false
	}
	out, err := json.Marshal(items)
	if err != nil {
		return raw, false
	}
	return out, true
}

// foundationShapeHint 给结构复杂、模型高频猜错的类型返回一份紧凑结构模板，
// 附在解析/校验错误后，让模型一次重试就能对齐，而不是每轮收敛一个字段。
func foundationShapeHint(typeName string) string {
	if typeName != "world_codex" {
		return ""
	}
	keys := make([]string, 0, len(domain.RequiredCodexSections))
	for _, sec := range domain.RequiredCodexSections {
		keys = append(keys, sec.Key)
	}
	return "\nworld_codex 结构模板（字段名必须完全一致）：" +
		`{"ability_tiers":[{"order":1,"name":"…","magnitude":"…","limits":"…","promotion":"…","cost":"…"}],` +
		`"skill_domains":[{"name":"…","description":"…","tier_binding":"…","constraints":["…"]}],` +
		`"races":[{"name":"…","description":"…","constraints":["…"]}],` +
		`"weapon_categories":[{"name":"…","description":"…","grades":["低→高"],"tier_binding":"…"}],` +
		`"equipment_categories":[同 weapon_categories 结构],` +
		`"sections":[{"key":"…","content":"…","rules":["…"]} 或 {"key":"…","not_applicable":true,"reason":"…"}],` +
		`"immutability_policy":"…"}` +
		"；sections 是数组且必须覆盖全部 16 个 key：" + strings.Join(keys, ", ")
}

// repairLooseJSON 对 LLM 产出 JSON 的两类高频语法失误做确定性修复：
//  1. 尾逗号：`[..., ]` / `{..., }`（"invalid character ']' / '}' after ..." 的主因）；
//  2. 字符串字面量内的裸控制字符（裸换行/裸 Tab 等，模型忘了转义）。
//
// 只做这两类无歧义修复，不猜缺失的逗号/引号/括号——修错比报错更糟。
// 返回 (修复后文本, 是否有改动)。
func repairLooseJSON(src string) (string, bool) {
	var b strings.Builder
	b.Grow(len(src))
	changed := false
	inString := false
	escaped := false
	for i := 0; i < len(src); i++ {
		c := src[i]
		if inString {
			switch {
			case escaped:
				escaped = false
				b.WriteByte(c)
			case c == '\\':
				escaped = true
				b.WriteByte(c)
			case c == '"':
				inString = false
				b.WriteByte(c)
			case c < 0x20:
				changed = true
				switch c {
				case '\n':
					b.WriteString(`\n`)
				case '\r':
					b.WriteString(`\r`)
				case '\t':
					b.WriteString(`\t`)
				default:
					fmt.Fprintf(&b, `\u%04x`, c)
				}
			default:
				b.WriteByte(c)
			}
			continue
		}
		switch c {
		case '"':
			inString = true
			b.WriteByte(c)
		case ',':
			// 向前看：逗号后只有空白且紧跟 ] 或 } → 尾逗号，丢弃。
			j := i + 1
			for j < len(src) && (src[j] == ' ' || src[j] == '\t' || src[j] == '\n' || src[j] == '\r') {
				j++
			}
			if j < len(src) && (src[j] == ']' || src[j] == '}') {
				changed = true
				continue
			}
			b.WriteByte(c)
		default:
			b.WriteByte(c)
		}
	}
	return b.String(), changed
}

// jsonShapeName 把 Go 目标类型翻译成 LLM 能照做的 JSON 形状说法。
func jsonShapeName(t reflect.Type) string {
	if t == nil {
		return "合法 JSON 值"
	}
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.Struct, reflect.Map:
		return "JSON 对象 {...}"
	case reflect.Slice, reflect.Array:
		return "JSON 数组 [...]"
	case reflect.String:
		return "字符串"
	case reflect.Bool:
		return "布尔值"
	case reflect.Float32, reflect.Float64,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "数字"
	default:
		return t.String()
	}
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
	if len(raw) == 0 || string(raw) == "null" || string(raw) == `""` {
		return "", fmt.Errorf("content 缺失或为空。若上一次调用的参数因内容过长被截断丢弃，请压缩篇幅后重发完整 content"+
			"（characters 可精简描述/psych 字段；不要分多次只发一部分——每次调用都会整体覆盖该类型的文件）: %w", errs.ErrToolArgs)
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

func (t *SaveFoundationTool) outlineReplacementRequiresAuthorization() (bool, error) {
	p, err := t.store.Progress.Load()
	if err != nil {
		return false, fmt.Errorf("load progress before outline replacement: %w: %w", errs.ErrStoreRead, err)
	}
	if p != nil && (p.Phase == domain.PhaseWriting || p.Phase == domain.PhaseComplete ||
		p.LatestCompleted() != 0 || len(p.CompletedChapters) != 0 || p.TotalWordCount != 0 ||
		len(p.ChapterWordCounts) != 0 || p.InProgressChapter > 0 || len(p.CompletedScenes) != 0 ||
		len(p.PendingRewrites) != 0 || strings.TrimSpace(p.GenerationID) != "" ||
		strings.TrimSpace(p.GenerationMode) != "" || p.ReopenedFromComplete ||
		len(p.StrandHistory) != 0 || len(p.HookHistory) != 0) {
		return true, nil
	}
	if err := t.store.ValidateOutlineAllChapterZeroWorkspace(); err != nil {
		return true, nil
	}
	return false, nil
}

func (t *SaveFoundationTool) chapterZeroOutlineReplacementAuthorized(kind string) bool {
	if kind == "layered_outline" && t.chapterZeroFoundationRefreshAuthorized() {
		return true
	}
	return t.chapterZeroRebaseOutlineReplacementAuthorized()
}

func (t *SaveFoundationTool) chapterZeroFoundationRefreshAuthorized() bool {
	if !t.allowChapterZeroFoundationRefresh {
		return false
	}
	if err := RequireChapterZeroFoundationRefreshState(t.store); err != nil {
		return false
	}
	lock, err := t.store.Runtime.LoadPipelineExecution()
	if err != nil || lock == nil || lock.Mode != domain.PipelineExecutionFoundation || lock.TargetChapter != 1 {
		return false
	}
	if err := requireCurrentPipelineExecutionProcess(lock, "save_foundation chapter-zero Architect refresh"); err != nil {
		return false
	}
	return true
}

// RequireChapterZeroFoundationRefreshState is the shared no-partial-write
// preflight for an explicit Architect refresh. The pipeline calls it before
// the first target sidecar; save_foundation repeats it immediately before the
// only exceptional outline replacement.
func RequireChapterZeroFoundationRefreshState(st *store.Store) error {
	if st == nil {
		return fmt.Errorf("chapter-zero foundation refresh requires a store: %w", errs.ErrToolPrecondition)
	}
	p, err := st.Progress.Load()
	if err != nil {
		return fmt.Errorf("load chapter-zero refresh progress: %w", err)
	}
	if p == nil || (p.Phase != domain.PhaseWriting && p.Phase != domain.PhasePremise && p.Phase != domain.PhaseOutline) ||
		p.LatestCompleted() != 0 || len(p.CompletedChapters) != 0 || p.TotalWordCount != 0 || len(p.ChapterWordCounts) != 0 ||
		len(p.PendingRewrites) != 0 || strings.TrimSpace(p.RewriteReason) != "" || strings.TrimSpace(p.GenerationID) != "" ||
		strings.TrimSpace(p.GenerationMode) != "" || p.CurrentChapter < 0 || p.CurrentChapter > 1 || p.InProgressChapter < 0 ||
		p.InProgressChapter > 1 || len(p.CompletedScenes) != 0 || p.ReopenedFromComplete || len(p.StrandHistory) != 0 ||
		len(p.HookHistory) != 0 || (p.Flow != "" && p.Flow != domain.FlowWriting) {
		return fmt.Errorf("foundation refresh requires chapter-zero progress with no canon, generation, rewrite, or history evidence: %w", errs.ErrToolPrecondition)
	}
	if !FoundationCoreComplete(st.Dir()) {
		return fmt.Errorf("foundation refresh requires an existing complete foundation: %w", errs.ErrToolPrecondition)
	}
	if receipt, err := st.LoadOutlineAllExecutionReceipt(); err != nil {
		return fmt.Errorf("load outline-all receipt before foundation refresh: %w", err)
	} else if receipt != nil {
		return fmt.Errorf("foundation refresh refuses a published or in-flight outline-all receipt: %w", errs.ErrToolPrecondition)
	}
	if active, err := st.ProjectedV2().LoadActiveGeneration(); err != nil {
		return fmt.Errorf("load active planning generation before foundation refresh: %w", err)
	} else if active != nil {
		return fmt.Errorf("foundation refresh refuses an active sealed planning generation: %w", errs.ErrToolPrecondition)
	}
	for _, rel := range []string{
		"meta/first_chapter_generation_readiness.json",
		"meta/first_chapter_generation_readiness.md",
	} {
		if _, err := os.Lstat(filepath.Join(st.Dir(), filepath.FromSlash(rel))); err == nil {
			return fmt.Errorf("foundation refresh refuses existing zero-init evidence at %s: %w", rel, errs.ErrToolPrecondition)
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("inspect zero-init evidence %s: %w", rel, err)
		}
	}
	if err := st.ValidateOutlineAllChapterZeroWorkspace(); err != nil {
		return fmt.Errorf("foundation refresh chapter-zero workspace: %w", err)
	}
	return nil
}

func (t *SaveFoundationTool) chapterZeroRebaseOutlineReplacementAuthorized() bool {
	p, err := t.store.Progress.Load()
	if err != nil || p == nil || p.Phase != domain.PhaseWriting || p.LatestCompleted() != 0 || p.TotalWordCount != 0 || strings.TrimSpace(p.GenerationID) == "" {
		return false
	}
	markerRaw, err := os.ReadFile(filepath.Join(t.store.Dir(), "meta", "all_chapter_rebase.json"))
	if err != nil {
		return false
	}
	var marker struct {
		NewGenerationID string `json:"new_generation_id"`
	}
	if json.Unmarshal(markerRaw, &marker) != nil || strings.TrimSpace(marker.NewGenerationID) != strings.TrimSpace(p.GenerationID) {
		return false
	}
	if _, err := os.Stat(filepath.Join(t.store.Dir(), "meta", "first_chapter_generation_readiness.json")); err == nil || !os.IsNotExist(err) {
		return false
	}
	for _, rel := range []string{"chapters", "drafts"} {
		entries, err := os.ReadDir(filepath.Join(t.store.Dir(), rel))
		if err != nil && !os.IsNotExist(err) {
			return false
		}
		for _, entry := range entries {
			if !entry.IsDir() && !strings.HasPrefix(entry.Name(), ".") {
				return false
			}
		}
	}
	return true
}

// worldCodexDraftRel 初建期的增量草稿缓冲。LLM 稳定输出不了一次成型的完整
// 法典（16 维 sections + 五类硬设定动辄 15KB+，常只送到 sections 就断），
// 所以初建走"合并暂存"协议：不完整的提交合并进草稿并暂存，回错只列剩余
// 缺失字段；凑齐后一次性提交正式文件并删除草稿。修订路径（已有正式法典）
// 不走草稿——修订语义是整文替换 + change_log。
const worldCodexDraftRel = "meta/world_codex.draft.json"

func (t *SaveFoundationTool) loadWorldCodexDraft() *domain.WorldCodex {
	data, err := os.ReadFile(filepath.Join(t.store.Dir(), worldCodexDraftRel))
	if err != nil {
		return nil
	}
	var draft domain.WorldCodex
	if json.Unmarshal(data, &draft) != nil {
		return nil
	}
	return &draft
}

func (t *SaveFoundationTool) saveWorldCodexDraft(codex *domain.WorldCodex) {
	data, err := json.MarshalIndent(codex, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(t.store.Dir(), worldCodexDraftRel), data, 0o644)
}

func (t *SaveFoundationTool) dropWorldCodexDraft() {
	_ = os.Remove(filepath.Join(t.store.Dir(), worldCodexDraftRel))
}

// mergeWorldCodex 把本次提交（in）合并到草稿（base）上：整块字段来者非空则覆盖，
// sections 按 key 合并（来者覆盖同 key，新 key 追加）。
func mergeWorldCodex(base, in *domain.WorldCodex) *domain.WorldCodex {
	out := *base
	if len(in.AbilityTiers) > 0 {
		out.AbilityTiers = in.AbilityTiers
	}
	if len(in.SkillDomains) > 0 {
		out.SkillDomains = in.SkillDomains
	}
	if len(in.Races) > 0 {
		out.Races = in.Races
	}
	if len(in.WeaponCategories) > 0 {
		out.WeaponCategories = in.WeaponCategories
	}
	if len(in.EquipmentCategories) > 0 {
		out.EquipmentCategories = in.EquipmentCategories
	}
	if strings.TrimSpace(in.ImmutabilityPolicy) != "" {
		out.ImmutabilityPolicy = in.ImmutabilityPolicy
	}
	if strings.TrimSpace(in.NovelName) != "" {
		out.NovelName = in.NovelName
	}
	if len(in.Sections) > 0 {
		byKey := map[string]int{}
		for i, sec := range out.Sections {
			byKey[strings.TrimSpace(sec.Key)] = i
		}
		for _, sec := range in.Sections {
			key := strings.TrimSpace(sec.Key)
			if i, ok := byKey[key]; ok {
				out.Sections[i] = sec
			} else {
				out.Sections = append(out.Sections, sec)
				byKey[key] = len(out.Sections) - 1
			}
		}
	}
	return &out
}

// saveWorldCodex 校验并保存全局世界法典。已有法典时强制走修订流程：
// 必须带 change_reason + change_evidence，版本自增并落 change_log——世界硬设定
// 不允许随写作漂移。
func (t *SaveFoundationTool) saveWorldCodex(codex *domain.WorldCodex, changeReason, changeEvidence string) error {
	// 初建期增量合并：已有草稿时先并入本次提交（见 worldCodexDraftRel 注释）。
	existingCodex, _ := t.store.LoadWorldCodex()
	if existingCodex != nil && strings.TrimSpace(changeReason) == "" && strings.TrimSpace(changeEvidence) == "" {
		// 法典已定稿且本次不是修订：直接短路，防止重派的 architect 反复重交
		// 不完整 payload 空烧回合（修订必须显式带 change_reason+change_evidence）。
		return fmt.Errorf("world_codex 已存在（v%d，能力分级 %d/种族 %d/维度 %d）且内容完整，无需重复保存。若确需修订请带 change_reason+change_evidence；否则请继续后续任务（如零章初始化由宿主自动执行，直接结束本轮即可）: %w",
			existingCodex.Version, len(existingCodex.AbilityTiers), len(existingCodex.Races), len(existingCodex.Sections), errs.ErrToolPrecondition)
	}
	if existingCodex == nil {
		if draft := t.loadWorldCodexDraft(); draft != nil {
			codex = mergeWorldCodex(draft, codex)
		}
	}
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
		if existingCodex == nil {
			// 初建期：把已收到的部分暂存草稿，后续调用只需补缺失字段。
			t.saveWorldCodexDraft(codex)
			return fmt.Errorf("world_codex 已合并暂存为草稿，仍缺：%s。下次调用 save_foundation(type=world_codex) 只需补发缺失字段（已收到的部分不必重发，会自动合并）: %w%s",
				strings.Join(missing, ", "), errs.ErrToolPrecondition, foundationShapeHint("world_codex"))
		}
		return fmt.Errorf("world_codex 不完整，缺少：%s：世界必须像真实世界一样自洽，每个维度要么设定、要么显式 not_applicable+理由: %w%s",
			strings.Join(missing, ", "), errs.ErrToolPrecondition, foundationShapeHint("world_codex"))
	}

	existing, err := t.store.LoadWorldCodex()
	if err != nil {
		return fmt.Errorf("load world_codex: %w: %w", errs.ErrStoreRead, err)
	}
	t.dropWorldCodexDraft()
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
