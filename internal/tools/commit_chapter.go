package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/chenhongyang/novel-studio/internal/aigc"
	"github.com/chenhongyang/novel-studio/internal/domain"
	editrules "github.com/chenhongyang/novel-studio/internal/editor/rules"
	"github.com/chenhongyang/novel-studio/internal/errs"
	"github.com/chenhongyang/novel-studio/internal/rag"
	"github.com/chenhongyang/novel-studio/internal/reviewreport"
	"github.com/chenhongyang/novel-studio/internal/rules"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/voocel/agentcore/schema"
)

const maxMechanicalGateCommitAttempts = 3 // 原提交 + 最多两次机械门禁打磨。

// CommitChapterTool 提交章节：加载正文 → 保存终稿 → 生成摘要 → 更新状态 → 更新进度。
type CommitChapterTool struct {
	store           *store.Store
	ragEmbedder     rag.Embedder
	ragVectorWriter rag.VectorWriter
}

func NewCommitChapterTool(store *store.Store) *CommitChapterTool {
	return &CommitChapterTool{store: store}
}

func (t *CommitChapterTool) WithRAGEmbedder(embedder rag.Embedder) *CommitChapterTool {
	t.ragEmbedder = embedder
	return t
}

func (t *CommitChapterTool) WithRAGVectorWriter(writer rag.VectorWriter) *CommitChapterTool {
	t.ragVectorWriter = writer
	return t
}

// commitOutput 在 domain.CommitResult 之上嵌入扩展字段，保持 domain 包不依赖 rules。
// 由于嵌入字段会被 JSON marshaler 提升（promoted），序列化结果等同于扁平结构。
type commitOutput struct {
	domain.CommitResult
	RuleViolations []rules.Violation       `json:"rule_violations,omitempty"`
	AIGCReport     *aigc.Report            `json:"aigc_report,omitempty"`
	AIVoice        *domain.AIVoiceAnalysis `json:"ai_voice,omitempty"`
	ResourceAudit  *domain.ResourceAudit   `json:"resource_audit,omitempty"`
	RAGIndexed     bool                    `json:"rag_indexed,omitempty"`
	RAGError       string                  `json:"rag_error,omitempty"`
}

func (t *CommitChapterTool) Name() string { return "commit_chapter" }
func (t *CommitChapterTool) Description() string {
	return "提交章节终稿。加载草稿正文保存为终稿，更新时间线、伏笔、关系、角色状态和进度。" +
		"提交时必须沉淀全角色 character_stage_records；系统会同步保存 side_character_journeys 和 chapter_world_deltas。rewrite 阶段若正文改变角色/世界事实，也必须同步提交新版角色台账。" +
		"返回结构化事实：next_chapter / review_required / arc_end / volume_end / needs_expansion / book_complete / flow 等"
}
func (t *CommitChapterTool) Label() string { return "提交章节" }

// 写工具（跨域原子操作：草稿→终稿→摘要→进度→checkpoint），禁止并发。
func (t *CommitChapterTool) ReadOnly(_ json.RawMessage) bool        { return false }
func (t *CommitChapterTool) ConcurrencySafe(_ json.RawMessage) bool { return false }

func (t *CommitChapterTool) Schema() map[string]any {
	timelineSchema := schema.Object(
		schema.Property("time", schema.String("故事内时间")).Required(),
		schema.Property("event", schema.String("事件描述")).Required(),
		schema.Property("characters", schema.Array("涉及角色", schema.String(""))),
	)
	foreshadowSchema := schema.Object(
		schema.Property("id", schema.String("伏笔 ID")).Required(),
		schema.Property("action", schema.Enum("操作", "plant", "advance", "resolve")).Required(),
		schema.Property("description", schema.String("伏笔描述（仅 plant 时必需）")),
	)
	relationshipSchema := schema.Object(
		schema.Property("character_a", schema.String("角色 A")).Required(),
		schema.Property("character_b", schema.String("角色 B")).Required(),
		schema.Property("relation", schema.String("当前关系描述")).Required(),
	)
	stateChangeSchema := schema.Object(
		schema.Property("entity", schema.String("角色名或实体名")).Required(),
		schema.Property("field", schema.String("变化属性；优先使用 goal/pressure/resource/relationship/secret/misbelief/action_tendency/emotion/trust/debt/injury/exposure/status/knowledge/decision_frame/relationship_contract/emotion_appraisal/arc_axis 等能支撑角色持续推演的字段")).Required(),
		schema.Property("old_value", schema.String("变化前的值")),
		schema.Property("new_value", schema.String("变化后的值")).Required(),
		schema.Property("reason", schema.String("变化原因")),
	)
	characterStageSchema := schema.Object(
		schema.Property("character", schema.String("角色名")).Required(),
		schema.Property("time", schema.String("故事内时间点或相对时间")),
		schema.Property("location", schema.String("该角色此刻所处位置")).Required(),
		schema.Property("status", schema.String("此刻状态：存活/受伤/失踪/异化/死亡/待确认等；不能含糊跳过")),
		schema.Property("environment", schema.String("该角色正在承受的现场环境、规则压力或社会压力")).Required(),
		schema.Property("current_action", schema.String("该角色此刻正在做什么；正文可不展示，但时间线必须成立")).Required(),
		schema.Property("pressure", schema.String("推动此角色行动的具体压力")).Required(),
		schema.Property("decision", schema.String("此阶段做出的选择或暂时不做的选择")).Required(),
		schema.Property("decision_reason", schema.String("为何此人会这样选择；锚定目标、压力、资源、关系或误判")),
		schema.Property("butterfly_effects", schema.Array("该决定对世界和主角后续选项造成的影响", schema.String(""))),
		schema.Property("mistake_or_misbelief", schema.String("合理误判、错误操作、信息缺口或过度反应")),
		schema.Property("knowledge_boundary", schema.String("此角色此刻知道/不知道什么，后续出场不能越界")).Required(),
		schema.Property("visible_in_chapter", schema.Bool("本章正文是否直接展示")),
		schema.Property("evidence", schema.String("正文或台账中支撑该记录的证据")),
		schema.Property("transport", schema.String("交通工具/移动方式；不能默认瞬移，若未移动写原地/被困/无")),
		schema.Property("travel_time", schema.String("按 book_world 或现实距离估算的移动耗时；未移动也写为什么为0")),
		schema.Property("meeting_constraint", schema.String("本章能否与主角相见、为何不能随叫随到、需要什么交通/凭证/能力")),
		schema.Property("personality_delta", schema.String("本章经历造成的性格、信任、恐惧、价值取向或决策习惯变化")),
		schema.Property("death_state", schema.String("若死亡/失踪/异化/重伤，记录确认程度；否则写存活/未确认/无")),
		schema.Property("protagonist_notice", schema.String("该状态何时、通过谁或什么证据传回主角；若主角已知也说明证据")),
		schema.Property("timeline_consistency", schema.String("如何与主线时间线同步，避免后续突然出现")).Required(),
		schema.Property("next_potential", schema.String("后续可回归时携带的新压力、新信息或新误判")),
		schema.Property("tags", schema.Array("检索标签", schema.String(""))),
	)
	resourceClaimSchema := schema.Object(
		schema.Property("id", schema.String("稳定 ID，可用 owner-kind-name")).Required(),
		schema.Property("name", schema.String("资源名")).Required(),
		schema.Property("owner", schema.String("当前归属角色/势力")),
		schema.Property("kind", schema.String("资源类型，如 asset/skill/item/place/debt")),
		schema.Property("risk", schema.String("高风险原因或审计备注")),
		schema.Property("evidence", schema.String("入账证据或提案依据")),
		schema.Property("participants", schema.Array("本资源相关参与者", schema.String(""))),
	)
	feedbackSchema := schema.Object(
		schema.Property("deviation", schema.String("偏离大纲的描述")).Required(),
		schema.Property("suggestion", schema.String("对后续大纲的调整建议")).Required(),
	)
	feedbackSchema["description"] = "对后续大纲的建议对象；必须直接传 JSON object，不要传字符串化 JSON"
	return schema.Object(
		schema.Property("chapter", schema.Int("章节号")).Required(),
		schema.Property("summary", schema.String("本章内容摘要（200字以内）")).Required(),
		schema.Property("characters", schema.Array("本章出场角色名", schema.String(""))).Required(),
		schema.Property("key_events", schema.Array("本章关键事件", schema.String(""))).Required(),
		schema.Property("timeline_events", schema.Array("本章时间线事件", timelineSchema)),
		schema.Property("foreshadow_updates", schema.Array("伏笔操作", foreshadowSchema)),
		schema.Property("relationship_changes", schema.Array("关系变化", relationshipSchema)),
		schema.Property("state_changes", schema.Array("角色/实体状态变化", stateChangeSchema)),
		schema.Property("character_stage_records", schema.Array("本章所有角色在同一时间线中的环境、行动、误判和决策；已有独立 dossier 的角色必须全部覆盖，正文未展示的主角视角外经历也只在这里推演记录；rewrite 时若正文变动也要同步提交新版记录", characterStageSchema)),
		schema.Property("resource_updates", schema.Array("本章已经入账、正文可以当事实使用的资源变化", resourceClaimSchema)),
		schema.Property("resource_proposals", schema.Array("待确认资源提案；不得在正文写成已经拥有或已入账", resourceClaimSchema)),
		schema.Property("cast_intros", schema.Array("本章首次引入且后续可能再出现的次要角色简介（不含主角及 characters.json 已有角色）", schema.Object(
			schema.Property("name", schema.String("角色名")).Required(),
			schema.Property("brief_role", schema.String("一句话定位（如：客栈老板/赌坊打手）")).Required(),
		))),
		schema.Property("hook_type", schema.Enum("章末钩子类型", "crisis", "mystery", "desire", "emotion", "choice")),
		schema.Property("dominant_strand", schema.Enum("本章主导叙事线", "quest", "fire", "constellation")),
		schema.Property("opening_device", schema.String("本章开头装置类型，如凶兆物微动/纸面显字/屏幕显字/对话截断/动作未完成/场景余像/无")),
		schema.Property("ending_device", schema.String("本章结尾装置类型，如凶兆物微动/纸面显字/屏幕显字/对话截断/动作未完成/场景余像/无")),
		schema.Property("scene_dynamics", schema.Object(
			schema.Property("conflict_engine", schema.Enum("本章主导冲突引擎", "value", "interest", "emotion", "survival")).Required(),
			schema.Property("pressure_index", schema.Int("本章紧张度 1-10")).Required(),
			schema.Property("info_release_ratio", schema.Number("新信息占比 0-1（>0.8 会警告读者过载）")).Required(),
			schema.Property("entropy_delta", schema.Number("本章混乱度增减 -1 到 1（负值=收束/喘息）")).Required(),
		)),
		schema.Property("pov", schema.String("本章 POV 角色名（多视角项目必报，用于契约轮换检查）")),
		schema.Property("confidence", schema.Object(
			schema.Property("overall", schema.Number("对本章质量的自报置信度 0-1；仅观测不阻塞")).Required(),
			schema.Property("doubts", schema.Array("具体疑点（哪里没把握、为什么），比分数更重要", schema.String(""))),
		)),
		schema.Property("character_expression_check", schema.Array("本章主要角色情绪表现强度自评（有大五画像的角色）", schema.Object(
			schema.Property("name", schema.String("角色名")).Required(),
			schema.Property("emotion_intensity", schema.Number("本章该角色情绪表现强度 0-1")).Required(),
		))),
		schema.Property("feedback", feedbackSchema),
	)
}

func (t *CommitChapterTool) Execute(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
	var a struct {
		Chapter             int                           `json:"chapter"`
		Summary             string                        `json:"summary"`
		Characters          []string                      `json:"characters"`
		KeyEvents           []string                      `json:"key_events"`
		TimelineEvents      []domain.TimelineEvent        `json:"timeline_events"`
		ForeshadowUpdates   []domain.ForeshadowUpdate     `json:"foreshadow_updates"`
		RelationshipChanges []domain.RelationshipEntry    `json:"relationship_changes"`
		StateChanges        []domain.StateChange          `json:"state_changes"`
		CharacterStage      []domain.CharacterStageRecord `json:"character_stage_records"`
		ResourceUpdates     []domain.ResourceClaim        `json:"resource_updates"`
		ResourceProposals   []domain.ResourceClaim        `json:"resource_proposals"`
		CastIntros          []domain.CastIntro            `json:"cast_intros"`
		HookType            string                        `json:"hook_type"`
		DominantStrand      string                        `json:"dominant_strand"`
		OpeningDevice       string                        `json:"opening_device"`
		EndingDevice        string                        `json:"ending_device"`
		Feedback            *domain.OutlineFeedback       `json:"feedback"`
		methodologyCommitExtras
	}
	if err := unmarshalToolArgs(args, &a); err != nil {
		return nil, fmt.Errorf("invalid args: %w: %w", errs.ErrToolArgs, err)
	}
	if a.Chapter <= 0 {
		return nil, fmt.Errorf("chapter must be > 0: %w", errs.ErrToolArgs)
	}
	if t.store.Progress.IsChapterCompleted(a.Chapter) {
		// 清理可能残留的 PendingCommit（崩溃发生在 ProgressMarked 之后、ClearPendingCommit 之前）
		if pending, _ := t.store.Signals.LoadPendingCommit(); pending != nil && pending.Chapter == a.Chapter {
			if err := t.appendCommitCheckpoint(a.Chapter); err != nil {
				return nil, fmt.Errorf("checkpoint commit: %w: %w", errs.ErrStoreWrite, err)
			}
			_ = t.store.Signals.ClearPendingCommit()
		}
		// 打磨/重写路径：章节虽已完成，但仍在 pending_rewrites 中，允许覆盖并 drain 队列
		progress, _ := t.store.Progress.Load()
		if progress != nil && slices.Contains(progress.PendingRewrites, a.Chapter) {
			return t.executeRewriteCommit(ctx, a.Chapter, a.Summary, a.Characters, a.KeyEvents,
				a.TimelineEvents, a.ForeshadowUpdates, a.RelationshipChanges, a.StateChanges,
				a.CharacterStage, a.ResourceUpdates, a.ResourceProposals,
				a.HookType, a.DominantStrand, a.OpeningDevice, a.EndingDevice, progress)
		}
		return t.buildSkipResult(a.Chapter, progress)
	}
	existingPending, err := t.store.Signals.LoadPendingCommit()
	if err != nil {
		return nil, fmt.Errorf("load pending commit: %w: %w", errs.ErrStoreRead, err)
	}
	if existingPending != nil && existingPending.Chapter != a.Chapter {
		return nil, fmt.Errorf("存在未恢复的章节提交：第 %d 章（阶段 %s），请先恢复或重新提交该章: %w", existingPending.Chapter, existingPending.Stage, errs.ErrToolConflict)
	}
	if err := t.store.Progress.ValidateChapterWork(a.Chapter); err != nil {
		// 队列冲突保持原样（已带 ErrToolConflict 分类）；其他 IO 错误归 Precondition。
		if errors.Is(err, errs.ErrToolConflict) {
			return nil, err
		}
		return nil, fmt.Errorf("章节当前不允许提交: %w: %w", errs.ErrToolPrecondition, err)
	}
	if err := validateCommitCharacterStage(t.store, a.Chapter, a.CharacterStage); err != nil {
		return nil, err
	}

	// 分层模式越界拦截：必须先于任何写操作，否则越界 commit 会把章节文件、摘要、
	// Progress 都改坏。boundary 复用给下方第 6b 步算弧/卷信号。
	var boundary *store.ArcBoundary
	if progress, perr := t.store.Progress.Load(); perr == nil && progress != nil && progress.Layered {
		b, bErr := t.store.Outline.CheckArcBoundary(a.Chapter)
		if bErr != nil {
			return nil, fmt.Errorf("弧边界检测失败 chapter=%d: %w: %w", a.Chapter, errs.ErrStoreRead, bErr)
		}
		if b == nil {
			return nil, fmt.Errorf(
				"第 %d 章不在分层大纲范围内：写作必须先 expand_arc 扩展弧或 append_volume 追加卷；若全书已完结请调 save_foundation type=complete_book: %w",
				a.Chapter, errs.ErrToolPrecondition)
		}
		boundary = b
	}

	// 1. 加载章节正文
	content, wordCount, err := t.store.Drafts.LoadChapterContent(a.Chapter)
	if err != nil {
		return nil, fmt.Errorf("load chapter content: %w: %w", errs.ErrStoreRead, err)
	}
	if content == "" {
		return nil, fmt.Errorf("no content found for chapter %d: %w", a.Chapter, errs.ErrToolPrecondition)
	}
	if err := requireCurrentDraftConsistency(t.store, a.Chapter, content); err != nil {
		return nil, err
	}
	if err := requireDraftPlanTitleMatch(t.store, a.Chapter, content); err != nil {
		return nil, err
	}
	if err := requireChapterWordContract(t.store, a.Chapter, content); err != nil {
		return nil, err
	}
	if err := requireChapterAttractionContent(t.store, a.Chapter, content); err != nil {
		return nil, err
	}

	now := time.Now().Format(time.RFC3339)
	pending := domain.PendingCommit{
		Chapter:        a.Chapter,
		Stage:          domain.CommitStageStarted,
		Summary:        a.Summary,
		HookType:       a.HookType,
		DominantStrand: a.DominantStrand,
		StartedAt:      now,
		UpdatedAt:      now,
	}
	if err := t.store.Signals.SavePendingCommit(pending); err != nil {
		return nil, fmt.Errorf("save pending commit: %w: %w", errs.ErrStoreWrite, err)
	}

	// 2. 保存终稿
	if err := t.store.Drafts.SaveFinalChapter(a.Chapter, content); err != nil {
		return nil, fmt.Errorf("save final chapter: %w: %w", errs.ErrStoreWrite, err)
	}
	if err := t.store.World.ClearChapterReview(a.Chapter); err != nil {
		return nil, fmt.Errorf("clear stale review: %w: %w", errs.ErrStoreWrite, err)
	}

	// 3. 保存摘要
	summary := domain.ChapterSummary{
		Chapter:       a.Chapter,
		Summary:       a.Summary,
		Characters:    a.Characters,
		KeyEvents:     a.KeyEvents,
		OpeningDevice: a.OpeningDevice,
		EndingDevice:  a.EndingDevice,
	}
	if err := t.store.Summaries.SaveSummary(summary); err != nil {
		return nil, fmt.Errorf("save summary: %w: %w", errs.ErrStoreWrite, err)
	}

	// 4. 更新状态增量
	if len(a.TimelineEvents) > 0 {
		for i := range a.TimelineEvents {
			a.TimelineEvents[i].Chapter = a.Chapter
		}
		if err := t.store.World.AppendTimelineEvents(a.TimelineEvents); err != nil {
			return nil, fmt.Errorf("append timeline: %w: %w", errs.ErrStoreWrite, err)
		}
	}
	if len(a.ForeshadowUpdates) > 0 {
		if err := t.store.World.UpdateForeshadow(a.Chapter, a.ForeshadowUpdates); err != nil {
			return nil, fmt.Errorf("update foreshadow: %w: %w", errs.ErrStoreWrite, err)
		}
	}
	if len(a.RelationshipChanges) > 0 {
		for i := range a.RelationshipChanges {
			a.RelationshipChanges[i].Chapter = a.Chapter
		}
		if err := t.store.World.UpdateRelationships(a.RelationshipChanges); err != nil {
			return nil, fmt.Errorf("update relationships: %w: %w", errs.ErrStoreWrite, err)
		}
	}
	if len(a.StateChanges) > 0 {
		for i := range a.StateChanges {
			a.StateChanges[i].Chapter = a.Chapter
		}
		if err := t.store.World.AppendStateChanges(a.StateChanges); err != nil {
			return nil, fmt.Errorf("append state changes: %w: %w", errs.ErrStoreWrite, err)
		}
	}
	if len(a.CharacterStage) > 0 {
		if err := t.store.SaveCharacterStageRecords(a.Chapter, a.CharacterStage); err != nil {
			return nil, fmt.Errorf("save character stage records: %w: %w", errs.ErrStoreWrite, err)
		}
		if err := t.store.SaveSideCharacterJourneys(a.Chapter, a.CharacterStage); err != nil {
			return nil, fmt.Errorf("save side character journeys: %w: %w", errs.ErrStoreWrite, err)
		}
	}
	if len(a.ResourceUpdates) > 0 || len(a.ResourceProposals) > 0 {
		if err := t.store.ResourceLedger.MergeClaims(a.Chapter, a.ResourceUpdates, a.ResourceProposals); err != nil {
			return nil, fmt.Errorf("merge resource ledger: %w: %w", errs.ErrStoreWrite, err)
		}
	}
	if err := t.saveChapterWorldDelta(
		a.Chapter, false, a.Summary, a.CharacterStage,
		a.TimelineEvents, a.ForeshadowUpdates, a.RelationshipChanges, a.StateChanges,
		a.ResourceUpdates, a.ResourceProposals,
	); err != nil {
		return nil, fmt.Errorf("save chapter world delta: %w: %w", errs.ErrStoreWrite, err)
	}

	// 4b. 累加配角名册：本章出场的非核心角色进 cast_ledger，供 novel_context 召回。
	// 失败时只 warn 不阻断 commit——名册是次要数据，可通过下一章 commit 自愈。
	if len(a.Characters) > 0 {
		coreNames := loadCoreCharacterNameSet(t.store)
		if err := t.store.Cast.MergeAppearances(a.Chapter, a.Characters, a.CastIntros, coreNames); err != nil {
			slog.Warn("配角名册累加失败，跳过", "module", "commit", "chapter", a.Chapter, "err", err)
		}
	}

	pending.Stage = domain.CommitStageStateApplied
	pending.UpdatedAt = time.Now().Format(time.RFC3339)
	if err := t.store.Signals.SavePendingCommit(pending); err != nil {
		return nil, fmt.Errorf("update pending commit stage: %w: %w", errs.ErrStoreWrite, err)
	}

	// 5. 更新进度
	if err := t.store.Progress.MarkChapterComplete(a.Chapter, wordCount, a.HookType, a.DominantStrand); err != nil {
		return nil, fmt.Errorf("mark chapter complete: %w: %w", errs.ErrStoreWrite, err)
	}

	// 6. 判断是否需要审阅
	progress, err := t.store.Progress.Load()
	if err != nil {
		return nil, fmt.Errorf("load progress: %w: %w", errs.ErrStoreRead, err)
	}
	completedCount := 0
	if progress != nil {
		completedCount = len(progress.CompletedChapters)
	}

	// 6b. 长篇模式弧/卷信号：boundary 已在入口前置校验，Layered 时保证非 nil
	var arcEnd, volumeEnd, needsExpansion, needsNewVolume bool
	var vol, arc, nextVol, nextArc int
	if progress != nil && progress.Layered && boundary != nil {
		arcEnd = boundary.IsArcEnd
		volumeEnd = boundary.IsVolumeEnd
		vol = boundary.Volume
		arc = boundary.Arc
		needsExpansion = boundary.NeedsExpansion
		needsNewVolume = boundary.NeedsNewVolume
		nextVol = boundary.NextVolume
		nextArc = boundary.NextArc
		_ = t.store.Progress.UpdateVolumeArc(vol, arc)
	}

	reviewRequired := true
	reviewReason := fmt.Sprintf("第 %d 章终稿已提交，必须先通过章级审阅", a.Chapter)
	if progress != nil && progress.Layered && (arcEnd || volumeEnd) {
		_, arcReason := domain.ShouldArcReview(arcEnd, volumeEnd, vol, arc)
		if arcReason != "" {
			reviewReason = reviewReason + "；" + arcReason
		}
	} else if !arcEnd && completedCount > 0 && completedCount%domain.ReviewInterval == 0 {
		reviewReason = reviewReason + fmt.Sprintf("；已完成 %d 章，可在章审后做阶段性审阅", completedCount)
	}

	// 7. 构造结构化信号
	result := domain.CommitResult{
		Chapter:        a.Chapter,
		Committed:      true,
		WordCount:      wordCount,
		NextChapter:    a.Chapter + 1,
		ReviewRequired: reviewRequired,
		ReviewReason:   reviewReason,
		HookType:       a.HookType,
		DominantStrand: a.DominantStrand,
		OpeningDevice:  a.OpeningDevice,
		EndingDevice:   a.EndingDevice,
		Feedback:       a.Feedback,
		ArcEnd:         arcEnd,
		VolumeEnd:      volumeEnd,
		Volume:         vol,
		Arc:            arc,
		NeedsExpansion: needsExpansion,
		NeedsNewVolume: needsNewVolume,
		NextVolume:     nextVol,
		NextArc:        nextArc,
	}

	// 8. 机械门禁先于完成态判定。章节未通过 AI/规则 error 时，进入打磨队列，
	// Router 会优先处理 PendingRewrites，不允许续写下一章。
	aiVoiceAnalysis, err := t.saveFinalAIVoice(a.Chapter, content, "")
	if err != nil {
		return nil, err
	}
	aigcReport := aigc.Analyze(content)
	violations := append(t.checkRules(content, wordCount), t.projectContaminationViolations(content)...)
	violations = append(violations, aigcViolation(aigcReport)...)
	violations = append(violations, t.resourcePendingViolations(content)...)
	a.methodologyCommitExtras.ChapterContent = content
	violations = append(violations, t.methodologyViolations(a.Chapter, wordCount, a.HookType, a.methodologyCommitExtras)...)
	violations = append(violations, consistencyReconcile(t.store, a.Chapter, content, a.ResourceUpdates)...)
	if err := t.saveAIGCReviewFiles(a.Chapter, content, aigcReport, violations); err != nil {
		return nil, fmt.Errorf("save aigc review: %w: %w", errs.ErrStoreWrite, err)
	}
	if reason := blockingViolationReason(violations); reason != "" {
		result.ReviewRequired = true
		result.ReviewReason = reason
		if err := t.enqueueMechanicalGateFailure(a.Chapter, reason); err != nil {
			return nil, fmt.Errorf("enqueue mechanical gate failure: %w: %w", errs.ErrStoreWrite, err)
		}
	}
	t.clearStaleFinalGlobalReview(progress)
	if p, _ := t.store.Progress.Load(); p != nil {
		result.Flow = string(p.Flow)
	}
	result.VolumeOutlineDue, result.VolumeOutlineReview = t.rollingVolumeOutlineSignals(a.Chapter)

	pending.Stage = domain.CommitStageProgressMarked
	pending.Result = &result
	pending.UpdatedAt = time.Now().Format(time.RFC3339)
	if err := t.store.Signals.SavePendingCommit(pending); err != nil {
		return nil, fmt.Errorf("update pending commit result: %w: %w", errs.ErrStoreWrite, err)
	}

	// 9. 追加 checkpoint。必须先于清除 pending_commit，确保重启后可见的
	// pending_commit 总能驱动重跑补齐缺失 checkpoint。
	if err := t.appendCommitCheckpoint(a.Chapter); err != nil {
		return nil, fmt.Errorf("checkpoint commit: %w: %w", errs.ErrStoreWrite, err)
	}

	// 10. 清除进度中间状态
	if err := t.store.Progress.ClearInProgress(); err != nil {
		return nil, fmt.Errorf("clear in-progress: %w: %w", errs.ErrStoreWrite, err)
	}
	if err := t.store.Signals.ClearPendingCommit(); err != nil {
		return nil, fmt.Errorf("clear pending commit: %w: %w", errs.ErrStoreWrite, err)
	}

	ragIndexed, ragErr := t.sedimentChapterRAG(ctx, chapterRAGFacts{
		Chapter:             a.Chapter,
		Summary:             a.Summary,
		Characters:          a.Characters,
		KeyEvents:           a.KeyEvents,
		TimelineEvents:      a.TimelineEvents,
		ForeshadowUpdates:   a.ForeshadowUpdates,
		RelationshipChanges: a.RelationshipChanges,
		StateChanges:        a.StateChanges,
		ResourceUpdates:     a.ResourceUpdates,
		ResourceProposals:   a.ResourceProposals,
		HookType:            a.HookType,
		DominantStrand:      a.DominantStrand,
		OpeningDevice:       a.OpeningDevice,
		EndingDevice:        a.EndingDevice,
		WordCount:           wordCount,
	})
	if ragErr != nil {
		slog.Warn("章节 RAG 沉淀失败，跳过", "module", "commit", "chapter", a.Chapter, "err", ragErr)
	}

	// 11. 机械规则检查结果随返回透出，供 editor 和日志解释。
	var audit *domain.ResourceAudit
	if ra, err := t.store.ResourceLedger.AuditForParticipants(a.Characters); err == nil && (len(ra.Booked) > 0 || len(ra.Pending) > 0) {
		audit = &ra
	}
	return json.Marshal(commitOutput{CommitResult: result, RuleViolations: violations, AIGCReport: &aigcReport, AIVoice: &aiVoiceAnalysis, ResourceAudit: audit, RAGIndexed: ragIndexed, RAGError: errorString(ragErr)})
}

func (t *CommitChapterTool) appendCommitCheckpoint(chapter int) error {
	_, err := t.store.Checkpoints.AppendArtifact(
		domain.ChapterScope(chapter), "commit",
		fmt.Sprintf("chapters/%02d.md", chapter),
	)
	return err
}

func (t *CommitChapterTool) saveChapterWorldDelta(
	chapter int,
	rewrite bool,
	summary string,
	stages []domain.CharacterStageRecord,
	timeline []domain.TimelineEvent,
	foreshadow []domain.ForeshadowUpdate,
	relationships []domain.RelationshipEntry,
	stateChanges []domain.StateChange,
	resources []domain.ResourceClaim,
	resourceProposals []domain.ResourceClaim,
) error {
	delta := domain.ChapterWorldDelta{
		Version:      1,
		Chapter:      chapter,
		GenerationID: currentGenerationID(t.store),
		Rewrite:      rewrite,
		Summary:      summary,
		GeneratedAt:  time.Now().Format(time.RFC3339),
		Sources:      []string{"commit_chapter", "character_stage_records", "timeline/resource/relationship/state deltas"},
	}
	for _, stage := range stages {
		if strings.TrimSpace(stage.Character) == "" {
			continue
		}
		delta.CharacterDeltas = append(delta.CharacterDeltas, domain.CharacterChapterDelta{
			Character:           stage.Character,
			Location:            stage.Location,
			Status:              stage.Status,
			VisibleInChapter:    stage.VisibleInChapter,
			CurrentAction:       stage.CurrentAction,
			Decision:            stage.Decision,
			DecisionReason:      stage.DecisionReason,
			ButterflyEffects:    append([]string(nil), stage.ButterflyEffects...),
			MistakeOrMisbelief:  stage.MistakeOrMisbelief,
			KnowledgeBoundary:   stage.KnowledgeBoundary,
			PersonalityDelta:    stage.PersonalityDelta,
			DeathState:          stage.DeathState,
			ProtagonistNotice:   stage.ProtagonistNotice,
			WorldImpact:         characterWorldImpact(stage),
			NextPotential:       stage.NextPotential,
			TimelineConsistency: stage.TimelineConsistency,
		})
		if !stage.VisibleInChapter && strings.TrimSpace(stage.CurrentAction) != "" {
			delta.WorldDeltas = append(delta.WorldDeltas, domain.WorldChapterDelta{
				Kind:                 "offscreen_character",
				Entity:               stage.Character,
				Change:               stage.CurrentAction + "；决策：" + stage.Decision,
				Evidence:             stage.Evidence,
				VisibleToProtagonist: false,
			})
		}
	}
	for _, event := range timeline {
		change := strings.TrimSpace(event.Event)
		if event.Time != "" {
			change = event.Time + "，" + change
		}
		delta.WorldDeltas = append(delta.WorldDeltas, domain.WorldChapterDelta{
			Kind:                 "timeline",
			Entity:               strings.Join(event.Characters, "、"),
			Change:               change,
			VisibleToProtagonist: true,
		})
	}
	for _, item := range foreshadow {
		delta.WorldDeltas = append(delta.WorldDeltas, domain.WorldChapterDelta{
			Kind:                 "foreshadow",
			Entity:               item.ID,
			Change:               strings.TrimSpace(strings.Join(cleanNonEmptyStrings([]string{item.Action, item.Description}), "：")),
			VisibleToProtagonist: true,
		})
	}
	for _, rel := range relationships {
		delta.WorldDeltas = append(delta.WorldDeltas, domain.WorldChapterDelta{
			Kind:                 "relationship",
			Entity:               rel.CharacterA + "-" + rel.CharacterB,
			Change:               rel.Relation,
			VisibleToProtagonist: true,
		})
	}
	for _, change := range stateChanges {
		delta.WorldDeltas = append(delta.WorldDeltas, domain.WorldChapterDelta{
			Kind:                 "state",
			Entity:               change.Entity + "." + change.Field,
			Change:               strings.TrimSpace(change.OldValue + " -> " + change.NewValue),
			Evidence:             change.Reason,
			VisibleToProtagonist: true,
		})
	}
	addResourceDeltas := func(kind string, claims []domain.ResourceClaim) {
		for _, claim := range claims {
			delta.WorldDeltas = append(delta.WorldDeltas, domain.WorldChapterDelta{
				Kind:                 kind,
				Entity:               claim.Name,
				Change:               strings.TrimSpace(strings.Join(cleanNonEmptyStrings([]string{claim.Owner, claim.Kind, claim.Status, claim.Risk}), "，")),
				Evidence:             claim.Evidence,
				VisibleToProtagonist: true,
			})
		}
	}
	addResourceDeltas("resource_booked", resources)
	addResourceDeltas("resource_pending", resourceProposals)
	return t.store.SaveChapterWorldDelta(delta)
}

func currentGenerationID(s *store.Store) string {
	if progress, err := s.Progress.Load(); err == nil && progress != nil && strings.TrimSpace(progress.GenerationID) != "" {
		return strings.TrimSpace(progress.GenerationID)
	}
	if policy, err := s.LoadSimulationRestartPolicy(); err == nil && policy != nil {
		return strings.TrimSpace(policy.GenerationID)
	}
	return ""
}

func characterWorldImpact(stage domain.CharacterStageRecord) string {
	parts := cleanNonEmptyStrings([]string{
		stage.PersonalityDelta,
		stage.DeathState,
		stage.NextPotential,
		stage.ProtagonistNotice,
	})
	return strings.Join(parts, "；")
}

// checkRules 对章节正文做机械检查：内置产品底线 Lint（机制残留，始终执行）
// + 用户规则 Check（读本书快照的 structured；快照缺失退到内置默认，保证机械底线始终在）。
func (t *CommitChapterTool) checkRules(text string, wordCount int) []rules.Violation {
	aigc.LoadProjectLexicon(t.store.Dir()) // Task 059：项目级 slop 词表覆盖（幂等）
	violations := rules.Lint(text)
	structured := rules.SystemDefaults().Structured
	if snap, err := t.store.UserRules.Load(); err == nil && snap != nil {
		structured = snap.Structured
	}
	return append(violations, rules.Check(text, wordCount, structured)...)
}

type chapterRAGFacts struct {
	Chapter             int
	Summary             string
	Characters          []string
	KeyEvents           []string
	TimelineEvents      []domain.TimelineEvent
	ForeshadowUpdates   []domain.ForeshadowUpdate
	RelationshipChanges []domain.RelationshipEntry
	StateChanges        []domain.StateChange
	ResourceUpdates     []domain.ResourceClaim
	ResourceProposals   []domain.ResourceClaim
	HookType            string
	DominantStrand      string
	OpeningDevice       string
	EndingDevice        string
	WordCount           int
}

func (t *CommitChapterTool) sedimentChapterRAG(ctx context.Context, f chapterRAGFacts) (bool, error) {
	chunks := buildChapterRAGChunks(f)
	if len(chunks) == 0 {
		return false, nil
	}
	if err := upsertRAGChunks(ctx, t.store, t.ragEmbedder, t.ragVectorWriter, chunks, domain.RAGIndexConfig{}); err != nil {
		return true, err
	}
	return true, nil
}

func buildChapterRAGChunks(f chapterRAGFacts) []domain.RAGChunk {
	text := chapterRAGText(f)
	if strings.TrimSpace(text) == "" {
		return nil
	}
	sourcePath := chapterRAGSourcePath(f.Chapter)
	keywords := append([]string(nil), f.Characters...)
	keywords = append(keywords, f.KeyEvents...)
	return []domain.RAGChunk{{
		ID:         fmt.Sprintf("chapter:%03d:commit_facts", f.Chapter),
		SourcePath: sourcePath,
		SourceKind: "chapter_summary_facts",
		Facet:      "plot",
		Context:    fmt.Sprintf("第 %d 章终稿沉淀 | commit_chapter", f.Chapter),
		Text:       text,
		Summary:    truncateRunes(strings.TrimSpace(f.Summary), 120),
		Keywords:   keywords,
		Metadata: map[string]any{
			"chapter":       f.Chapter,
			"word_count":    f.WordCount,
			"hook_type":     f.HookType,
			"dominant_line": f.DominantStrand,
			"source":        "commit_chapter",
		},
	}}
}

func chapterRAGText(f chapterRAGFacts) string {
	var b strings.Builder
	writeLine := func(label, value string) {
		value = strings.TrimSpace(value)
		if value != "" {
			fmt.Fprintf(&b, "%s：%s\n", label, value)
		}
	}
	writeList := func(label string, values []string) {
		values = cleanNonEmptyStrings(values)
		if len(values) > 0 {
			writeLine(label, strings.Join(values, "；"))
		}
	}
	fmt.Fprintf(&b, "# 第 %d 章终稿沉淀\n", f.Chapter)
	writeLine("摘要", f.Summary)
	writeList("出场人物", f.Characters)
	writeList("关键事件", f.KeyEvents)
	writeLine("章末钩子类型", f.HookType)
	writeLine("主导叙事线", f.DominantStrand)
	writeLine("开头装置", f.OpeningDevice)
	writeLine("结尾装置", f.EndingDevice)
	if f.WordCount > 0 {
		writeLine("终稿字数", fmt.Sprintf("%d", f.WordCount))
	}
	for _, event := range f.TimelineEvents {
		line := strings.TrimSpace(event.Event)
		if line == "" {
			continue
		}
		if event.Time != "" {
			line = event.Time + "，" + line
		}
		if len(event.Characters) > 0 {
			line += "（" + strings.Join(cleanNonEmptyStrings(event.Characters), "、") + "）"
		}
		writeLine("时间线", line)
	}
	for _, item := range f.ForeshadowUpdates {
		line := strings.TrimSpace(strings.Join(cleanNonEmptyStrings([]string{item.ID, item.Action, item.Description}), "，"))
		writeLine("伏笔变化", line)
	}
	for _, rel := range f.RelationshipChanges {
		line := strings.TrimSpace(fmt.Sprintf("%s-%s：%s", rel.CharacterA, rel.CharacterB, rel.Relation))
		writeLine("关系变化", line)
	}
	for _, change := range f.StateChanges {
		line := strings.TrimSpace(fmt.Sprintf("%s.%s：%s -> %s", change.Entity, change.Field, change.OldValue, change.NewValue))
		if change.Reason != "" {
			line += "；原因：" + change.Reason
		}
		writeLine("状态变化", line)
	}
	writeResourceClaims := func(label string, claims []domain.ResourceClaim) {
		for _, claim := range claims {
			line := strings.TrimSpace(strings.Join(cleanNonEmptyStrings([]string{claim.Name, claim.Owner, claim.Kind, claim.Status, claim.Evidence}), "，"))
			writeLine(label, line)
		}
	}
	writeResourceClaims("资源入账", f.ResourceUpdates)
	writeResourceClaims("资源提案", f.ResourceProposals)
	return strings.TrimSpace(b.String())
}

func chapterRAGSourcePath(chapter int) string {
	return fmt.Sprintf("summaries/%02d.json", chapter)
}

func rebuildRAGChunkHashes(chunks []domain.RAGChunk) []string {
	seen := map[string]struct{}{}
	for _, chunk := range chunks {
		chunk = rag.NormalizeChunk(chunk)
		if chunk.Hash != "" {
			seen[chunk.Hash] = struct{}{}
		}
	}
	hashes := make([]string, 0, len(seen))
	for hash := range seen {
		hashes = append(hashes, hash)
	}
	sort.Strings(hashes)
	return hashes
}

func cleanNonEmptyStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// executeRewriteCommit 处理打磨/重写章节的提交：覆盖终稿与摘要、更新字数、drain 队列。
// 跳过所有世界状态追加（timeline / foreshadow / relationship / state_changes）与弧边界检测，
// 这些已在章节原始提交时应用。
func (t *CommitChapterTool) executeRewriteCommit(
	ctx context.Context,
	chapter int,
	summary string,
	characters, keyEvents []string,
	timelineEvents []domain.TimelineEvent,
	foreshadowUpdates []domain.ForeshadowUpdate,
	relationshipChanges []domain.RelationshipEntry,
	stateChanges []domain.StateChange,
	characterStage []domain.CharacterStageRecord,
	resourceUpdates []domain.ResourceClaim,
	resourceProposals []domain.ResourceClaim,
	hookType, dominantStrand string,
	openingDevice, endingDevice string,
	progress *domain.Progress,
) (json.RawMessage, error) {
	// 1. 加载打磨后的正文
	content, wordCount, err := t.store.Drafts.LoadChapterContent(chapter)
	if err != nil {
		return nil, fmt.Errorf("rewrite: load chapter content: %w: %w", errs.ErrStoreRead, err)
	}
	if content == "" {
		return nil, fmt.Errorf("no content found for chapter %d: %w", chapter, errs.ErrToolPrecondition)
	}
	if err := requireCurrentDraftConsistency(t.store, chapter, content); err != nil {
		return nil, err
	}
	if err := requireDraftPlanTitleMatch(t.store, chapter, content); err != nil {
		return nil, err
	}

	// 2. 硬校验：drafts 与现终稿完全相同 → 判定为未真正打磨/重写（writer 跳过了 draft_chapter）
	// 拒绝 commit，强制 writer 先调 draft_chapter(mode=write) 写入新版本。
	existingFinal, _ := t.store.Drafts.LoadChapterText(chapter)
	if existingFinal != "" && existingFinal == content {
		mode := "重写"
		if progress != nil && progress.Flow == domain.FlowPolishing {
			mode = "打磨"
		}
		return nil, fmt.Errorf("第 %d 章 drafts 与 chapters 内容完全相同，未检测到%s改动。请先调 draft_chapter(mode=write, chapter=%d) 写入%s后的新正文，再 commit_chapter: %w",
			chapter, mode, chapter, mode, errs.ErrToolPrecondition)
	}
	if err := requireChapterWordContract(t.store, chapter, content); err != nil {
		return nil, err
	}
	if err := requireChapterAttractionContent(t.store, chapter, content); err != nil {
		return nil, err
	}
	characterStage = mergeRewriteCharacterStage(t.store, chapter, characterStage)
	if len(characterStage) > 0 || len(requiredDossierCharacterNames(t.store, chapter)) > 0 {
		if err := validateCommitCharacterStage(t.store, chapter, characterStage); err != nil {
			return nil, err
		}
	}

	// 3. 覆盖终稿
	if err := t.store.Drafts.SaveFinalChapter(chapter, content); err != nil {
		return nil, fmt.Errorf("rewrite: save final chapter: %w: %w", errs.ErrStoreWrite, err)
	}
	if err := t.store.World.ClearChapterReview(chapter); err != nil {
		return nil, fmt.Errorf("rewrite: clear stale review: %w: %w", errs.ErrStoreWrite, err)
	}

	// 3. 覆盖摘要
	if err := t.store.Summaries.SaveSummary(domain.ChapterSummary{
		Chapter:       chapter,
		Summary:       summary,
		Characters:    characters,
		KeyEvents:     keyEvents,
		OpeningDevice: openingDevice,
		EndingDevice:  endingDevice,
	}); err != nil {
		return nil, fmt.Errorf("rewrite: save summary: %w: %w", errs.ErrStoreWrite, err)
	}

	for i := range timelineEvents {
		timelineEvents[i].Chapter = chapter
	}
	if err := t.store.World.ReplaceTimelineEventsForChapter(chapter, timelineEvents); err != nil {
		return nil, fmt.Errorf("rewrite: replace timeline: %w: %w", errs.ErrStoreWrite, err)
	}
	for i := range relationshipChanges {
		relationshipChanges[i].Chapter = chapter
	}
	if len(relationshipChanges) > 0 {
		if err := t.store.World.UpdateRelationships(relationshipChanges); err != nil {
			return nil, fmt.Errorf("rewrite: update relationships: %w: %w", errs.ErrStoreWrite, err)
		}
	}
	for i := range stateChanges {
		stateChanges[i].Chapter = chapter
	}
	if err := t.store.World.ReplaceStateChangesForChapter(chapter, stateChanges); err != nil {
		return nil, fmt.Errorf("rewrite: replace state changes: %w: %w", errs.ErrStoreWrite, err)
	}
	if len(foreshadowUpdates) > 0 {
		if err := t.store.World.UpdateForeshadow(chapter, foreshadowUpdates); err != nil {
			return nil, fmt.Errorf("rewrite: update foreshadow: %w: %w", errs.ErrStoreWrite, err)
		}
	}
	if err := t.store.SaveCharacterStageRecords(chapter, characterStage); err != nil {
		return nil, fmt.Errorf("rewrite: save character stage records: %w: %w", errs.ErrStoreWrite, err)
	}
	if err := t.store.SaveSideCharacterJourneys(chapter, characterStage); err != nil {
		return nil, fmt.Errorf("rewrite: save side character journeys: %w: %w", errs.ErrStoreWrite, err)
	}
	if err := t.store.ResourceLedger.ReplaceChapterClaims(chapter, resourceUpdates, resourceProposals); err != nil {
		return nil, fmt.Errorf("rewrite: replace resource ledger: %w: %w", errs.ErrStoreWrite, err)
	}
	if err := t.saveChapterWorldDelta(
		chapter, true, summary, characterStage,
		timelineEvents, foreshadowUpdates, relationshipChanges, stateChanges,
		resourceUpdates, resourceProposals,
	); err != nil {
		return nil, fmt.Errorf("rewrite: save chapter world delta: %w: %w", errs.ErrStoreWrite, err)
	}

	// 4. 更新字数（MarkChapterComplete 对已完成章节是幂等的：replaces word count, slice.Contains 防止重复入队）
	if err := t.store.Progress.MarkChapterComplete(chapter, wordCount, hookType, dominantStrand); err != nil {
		return nil, fmt.Errorf("rewrite: update word count: %w: %w", errs.ErrStoreWrite, err)
	}

	// 5. Drain 待处理队列；队列空时 CompleteRewrite 会自动把 flow 切回 writing
	if err := t.store.Progress.CompleteRewrite(chapter); err != nil {
		return nil, fmt.Errorf("rewrite: complete rewrite: %w: %w", errs.ErrStoreWrite, err)
	}

	// 6. Checkpoint
	if _, err := t.store.Checkpoints.AppendArtifact(
		domain.ChapterScope(chapter), "commit",
		fmt.Sprintf("chapters/%02d.md", chapter),
	); err != nil {
		return nil, fmt.Errorf("rewrite: checkpoint commit: %w: %w", errs.ErrStoreWrite, err)
	}

	// 7. 机械检查先跑。返工后仍未通过时，把当前章重新压回队列，不能向后续章节放行。
	// 若同一章已经经历"原提交 + 两次打磨"仍被启发式检测卡住，则保留报告，
	// 交给后续章级审阅判定，避免机械指标无限返工。
	aiVoiceAnalysis, err := t.saveFinalAIVoice(chapter, content, existingFinal)
	if err != nil {
		return nil, err
	}
	aigcReport := aigc.Analyze(content)
	violations := append(t.checkRules(content, wordCount), t.projectContaminationViolations(content)...)
	violations = append(violations, aigcViolation(aigcReport)...)
	violations = append(violations, t.resourcePendingViolations(content)...)
	gateBlocked := false
	if reason := blockingViolationReason(violations); reason != "" {
		commitAttempts := t.chapterCommitCheckpointCount(chapter)
		if commitAttempts >= maxMechanicalGateCommitAttempts {
			violations = append(violations, rules.Violation{
				Rule:     "mechanical_gate_retry_limit",
				Target:   reason,
				Limit:    fmt.Sprintf("%d commit attempts", maxMechanicalGateCommitAttempts),
				Actual:   commitAttempts,
				Severity: rules.SeverityWarning,
			})
		} else {
			gateBlocked = true
		}
	}
	if err := t.saveAIGCReviewFiles(chapter, content, aigcReport, violations); err != nil {
		return nil, fmt.Errorf("rewrite: save aigc review: %w: %w", errs.ErrStoreWrite, err)
	}
	if gateBlocked {
		reason := blockingViolationReason(violations)
		if err := t.enqueueMechanicalGateFailure(chapter, reason); err != nil {
			return nil, fmt.Errorf("rewrite: enqueue mechanical gate failure: %w: %w", errs.ErrStoreWrite, err)
		}
	}

	// 8. 读取 drain / gate 后的 Progress 快照，作为事实返回
	mode := "rewrite"
	if progress.Flow == domain.FlowPolishing {
		mode = "polish"
	}
	latest, _ := t.store.Progress.Load()
	remaining := []int{}
	nextChapter := chapter + 1
	flow := string(domain.FlowWriting)
	if latest != nil {
		remaining = append(remaining, latest.PendingRewrites...)
		nextChapter = latest.NextChapter()
		flow = string(latest.Flow)
	}
	drained := len(remaining) == 0

	if latest != nil {
		t.clearStaleFinalGlobalReview(latest)
	}
	ragIndexed, ragErr := t.sedimentChapterRAG(ctx, chapterRAGFacts{
		Chapter:             chapter,
		Summary:             summary,
		Characters:          characters,
		KeyEvents:           keyEvents,
		TimelineEvents:      timelineEvents,
		ForeshadowUpdates:   foreshadowUpdates,
		RelationshipChanges: relationshipChanges,
		StateChanges:        stateChanges,
		ResourceUpdates:     resourceUpdates,
		ResourceProposals:   resourceProposals,
		HookType:            hookType,
		DominantStrand:      dominantStrand,
		OpeningDevice:       openingDevice,
		EndingDevice:        endingDevice,
		WordCount:           wordCount,
	})
	if ragErr != nil {
		slog.Warn("返工章节 RAG 沉淀失败，跳过", "module", "commit", "chapter", chapter, "err", ragErr)
	}

	return json.Marshal(map[string]any{
		"chapter":         chapter,
		"rewritten":       true,
		"mode":            mode,
		"word_count":      wordCount,
		"remaining_queue": remaining,
		"queue_drained":   drained,
		"next_chapter":    nextChapter,
		"flow":            flow,
		"opening_device":  openingDevice,
		"ending_device":   endingDevice,
		"review_required": true,
		"review_reason":   fmt.Sprintf("第 %d 章返工终稿已提交，必须重新通过章级审阅", chapter),
		"rule_violations": violations,
		"aigc_report":     aigcReport,
		"ai_voice":        aiVoiceAnalysis,
		"rag_indexed":     ragIndexed,
		"rag_error":       errorString(ragErr),
	})
}

func mergeRewriteCharacterStage(st *store.Store, chapter int, submitted []domain.CharacterStageRecord) []domain.CharacterStageRecord {
	existing, err := st.LoadCharacterStageRecords(chapter)
	if err != nil || len(existing) == 0 {
		return submitted
	}
	if len(submitted) == 0 {
		return existing
	}
	out := append([]domain.CharacterStageRecord(nil), existing...)
	index := make(map[string]int, len(out))
	for i := range out {
		if name := strings.TrimSpace(out[i].Character); name != "" {
			index[name] = i
		}
	}
	for _, record := range submitted {
		name := strings.TrimSpace(record.Character)
		if name == "" {
			continue
		}
		record.Character = name
		if i, ok := index[name]; ok {
			out[i] = record
			continue
		}
		index[name] = len(out)
		out = append(out, record)
	}
	return out
}

func (t *CommitChapterTool) chapterCommitCheckpointCount(chapter int) int {
	scope := domain.ChapterScope(chapter)
	count := 0
	for _, cp := range t.store.Checkpoints.All() {
		if cp.Step == "commit" && cp.Scope.Matches(scope) {
			count++
		}
	}
	return count
}

func (t *CommitChapterTool) resourcePendingViolations(text string) []rules.Violation {
	warnings, err := t.store.ResourceLedger.AuditTextForPendingFacts(text)
	if err != nil || len(warnings) == 0 {
		return nil
	}
	out := make([]rules.Violation, 0, len(warnings))
	for _, warning := range warnings {
		out = append(out, rules.Violation{
			Rule:     "pending_resource_as_fact",
			Target:   warning,
			Actual:   1,
			Severity: rules.SeverityWarning,
		})
	}
	return out
}

func (t *CommitChapterTool) projectContaminationViolations(text string) []rules.Violation {
	return SecondAlgorithmProjectContaminationViolations(t.store, text)
}

func (t *CommitChapterTool) saveFinalAIVoice(chapter int, content string, previousText string) (domain.AIVoiceAnalysis, error) {
	history, _ := t.store.AIVoice.LoadAllChapterMetrics()
	previousMetrics, _ := t.store.AIVoice.LoadChapterMetrics(chapter)
	analysis := editrules.AnalyzeChapter(chapter, content, history)
	if previousMetrics != nil && previousText != "" {
		analysis.Metrics.RevisionRound = previousMetrics.RevisionRound + 1
		analysis.Metrics.BeforeAfterDiff = summarizeBeforeAfter(previousText, content)
		analysis.Metrics.AIVoiceScoreHistory = append([]domain.AIVoiceScorePoint(nil), previousMetrics.AIVoiceScoreHistory...)
		analysis.Metrics.AIVoiceScoreHistory = append(analysis.Metrics.AIVoiceScoreHistory, domain.AIVoiceScorePoint{
			Round:  analysis.Metrics.RevisionRound,
			Source: "rules",
			Score:  analysis.Metrics.AIVoiceScore,
			At:     analysis.Metrics.GeneratedAt,
		})
		analysis.Metrics.GeneratedAt = time.Now().Format(time.RFC3339)
		analysis.GeneratedAt = analysis.Metrics.GeneratedAt
		analysis.Metrics.Chapter = chapter
		analysis.Metrics.BeforeAfterDiff = summarizeBeforeAfter(previousText, content)
	} else if previousMetrics != nil {
		analysis.Metrics.RevisionRound = previousMetrics.RevisionRound
		analysis.Metrics.BeforeAfterDiff = previousMetrics.BeforeAfterDiff
		analysis.Metrics.AIVoiceScoreHistory = append([]domain.AIVoiceScorePoint(nil), previousMetrics.AIVoiceScoreHistory...)
		analysis.Metrics.AIVoiceScoreHistory = append(analysis.Metrics.AIVoiceScoreHistory, domain.AIVoiceScorePoint{
			Round:  analysis.Metrics.RevisionRound,
			Source: "rules",
			Score:  analysis.Metrics.AIVoiceScore,
			At:     analysis.Metrics.GeneratedAt,
		})
	} else if analysis.Metrics.RevisionRound == 0 && len(analysis.Metrics.AIVoiceScoreHistory) == 0 {
		analysis.Metrics.AIVoiceScoreHistory = []domain.AIVoiceScorePoint{{
			Round:  0,
			Source: "rules",
			Score:  analysis.Metrics.AIVoiceScore,
			At:     analysis.Metrics.GeneratedAt,
		}}
	}
	analysis.Metrics.GeneratedAt = time.Now().Format(time.RFC3339)
	analysis.GeneratedAt = analysis.Metrics.GeneratedAt
	analysis.BodySHA256 = reviewreport.BodySHA256(content)
	if err := t.store.AIVoice.SaveChapterMetrics(analysis.Metrics, false); err != nil {
		return analysis, fmt.Errorf("save ai voice metrics: %w: %w", errs.ErrStoreWrite, err)
	}
	if err := t.store.AIVoice.SaveRedFlags(analysis); err != nil {
		return analysis, fmt.Errorf("save ai voice redflags: %w: %w", errs.ErrStoreWrite, err)
	}
	return analysis, nil
}

func summarizeBeforeAfter(before, after string) string {
	if before == "" {
		return ""
	}
	beforeRunes := []rune(before)
	afterRunes := []rune(after)
	prefix := 0
	for prefix < len(beforeRunes) && prefix < len(afterRunes) && beforeRunes[prefix] == afterRunes[prefix] {
		prefix++
	}
	suffix := 0
	for suffix < len(beforeRunes)-prefix && suffix < len(afterRunes)-prefix &&
		beforeRunes[len(beforeRunes)-1-suffix] == afterRunes[len(afterRunes)-1-suffix] {
		suffix++
	}
	return fmt.Sprintf("字数 %d→%d；公共前缀 %d 字，公共后缀 %d 字", len(beforeRunes), len(afterRunes), prefix, suffix)
}

// requireCurrentDraftConsistency closes the draft -> consistency -> commit
// contract mechanically. Legacy/imported drafts may have no draft checkpoint;
// once draft_chapter has emitted one, commit only accepts a consistency check
// over those exact bytes.
func requireCurrentDraftConsistency(st *store.Store, chapter int, content string) error {
	scope := domain.ChapterScope(chapter)
	draftCheckpoint := st.Checkpoints.LatestByStep(scope, "draft")
	if draftCheckpoint == nil {
		return nil
	}
	wantDigest := "sha256:" + reviewreport.BodySHA256(content)
	consistencyCheckpoint := st.Checkpoints.LatestByStep(scope, "consistency_check")
	// edit_chapter 会有意修改 draft checkpoint 之后的字节。只要随后确实对当前
	// 精确字节重新执行过 check_consistency，它就应成为新的提交凭证；过去先比较
	// draft digest，导致 edit -> check -> commit 永远无法通过并循环重发提交参数。
	if consistencyCheckpoint != nil && consistencyCheckpoint.Digest == wantDigest {
		return nil
	}
	if draftCheckpoint.Digest != wantDigest {
		return fmt.Errorf("第 %d 章草稿在 draft checkpoint 后发生变化；请重新调用 draft_chapter 或 check_consistency 后再提交: %w", chapter, errs.ErrToolPrecondition)
	}
	return fmt.Errorf("第 %d 章当前草稿尚未通过 check_consistency（或检查后正文又被修改）；请回读 drafts/%02d.draft.md 并重新检查后再 commit_chapter: %w", chapter, chapter, errs.ErrToolPrecondition)
}

func requireDraftPlanTitleMatch(st *store.Store, chapter int, content string) error {
	plan, err := st.Drafts.LoadChapterPlan(chapter)
	if err != nil {
		return fmt.Errorf("读取第 %d 章计划用于标题校验: %w", chapter, err)
	}
	if plan == nil || strings.TrimSpace(plan.Title) == "" {
		return nil
	}
	heading := firstChapterHeading(content)
	if heading == "" || !chapterTitleEquivalent(heading, plan.Title) {
		return fmt.Errorf("第 %d 章正文标题与计划标题不一致：正文首行=%q，plan.title=%q；请修正正文标题并重新 check_consistency: %w", chapter, heading, plan.Title, errs.ErrToolPrecondition)
	}
	return nil
}

func (t *CommitChapterTool) saveAIGCReviewFiles(chapter int, body string, report aigc.Report, violations []rules.Violation) error {
	dir := filepath.Join(t.store.Dir(), "reviews")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	payload := reviewreport.MechanicalGatePayload{
		Chapter:        chapter,
		BodySHA256:     reviewreport.BodySHA256(body),
		AIGCReport:     report,
		RuleViolations: violations,
		GeneratedAt:    time.Now().Format(time.RFC3339),
	}
	raw, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("%02d_ai_gate.json", chapter)), raw, 0o644); err != nil {
		return err
	}
	if err := reviewreport.WriteUnifiedMarkdown(t.store.Dir(), reviewreport.UnifiedMarkdownInput{
		Chapter:     chapter,
		GeneratedAt: payload.GeneratedAt,
		Mechanical:  &payload,
	}); err != nil {
		return err
	}
	return reviewreport.RemoveLegacyMarkdown(t.store.Dir(), chapter)
}

func (t *CommitChapterTool) clearStaleFinalGlobalReview(progress *domain.Progress) {
	if progress == nil || progress.Layered || progress.TotalChapters <= 0 {
		return
	}
	_ = t.store.World.ClearGlobalReview(progress.TotalChapters)
}

// blockingViolationReason 决定哪些机械项直接打回返工（重新入队）。高置信 AI
// 风险和命中即改的写法模板在提交阶段硬拦；其余 warning 仍交给章级审阅判定，
// 避免慢速逐轮下机械指标无限返工。
func blockingViolationReason(violations []rules.Violation) string {
	for _, v := range violations {
		if immediateMechanicalGateFailure(v) {
			target := v.Target
			if target == "" {
				target = fmt.Sprint(v.Actual)
			}
			return fmt.Sprintf("机械审核未通过：%s=%s", v.Rule, target)
		}
	}
	return ""
}

func immediateMechanicalGateFailure(v rules.Violation) bool {
	switch v.Rule {
	case "aigc_ratio":
		return v.Severity == rules.SeverityError
	case "templated_dialogue_chain":
		return true
	case "project_contamination", "deprecated_story_engine":
		return v.Severity == rules.SeverityError
	default:
		return false
	}
}

func (t *CommitChapterTool) enqueueMechanicalGateFailure(chapter int, reason string) error {
	progress, err := t.store.Progress.Load()
	if err != nil {
		return err
	}
	queue := []int{chapter}
	if progress != nil {
		for _, ch := range progress.PendingRewrites {
			if ch != chapter {
				queue = append(queue, ch)
			}
		}
	}
	if err := t.store.Progress.SetPendingRewrites(queue, reason); err != nil {
		return err
	}
	latest, err := t.store.Progress.Load()
	if err != nil || latest == nil {
		return err
	}
	switch latest.Flow {
	case domain.FlowPolishing, domain.FlowRewriting:
		return nil
	default:
		return t.store.Progress.SetFlow(domain.FlowPolishing)
	}
}

func aigcViolation(report aigc.Report) []rules.Violation {
	gatePercent := aigcGatePercent(report)
	if gatePercent <= 5 {
		return nil
	}
	severity := rules.SeverityWarning
	if gatePercent >= 35 {
		severity = rules.SeverityError
	}
	return []rules.Violation{
		{
			Rule:      "aigc_ratio",
			Target:    report.Engine,
			Limit:     "5%",
			Actual:    gatePercent,
			Deviation: gatePercent / 100,
			Severity:  severity,
		},
	}
}

func aigcGatePercent(report aigc.Report) float64 {
	// 与章级审阅门禁共用同一口径（短章按 segment floor 真高判，不被 blended 稀释）。
	return aigc.EffectiveGatePercent(report)
}

// buildSkipResult 为"章节已完成的重复提交"构造与正常 commit 对齐的事实返回。
// 协调者据此做后续决策（writer/editor/architect 派发），而不会因为拿到 prose 提示而幻觉。
func (t *CommitChapterTool) buildSkipResult(chapter int, progress *domain.Progress) (json.RawMessage, error) {
	_, wordCount, _ := t.store.Drafts.LoadChapterContent(chapter)

	result := domain.CommitResult{
		Chapter:     chapter,
		Committed:   true,
		WordCount:   wordCount,
		NextChapter: chapter + 1,
	}

	if progress != nil && progress.Layered {
		if boundary, _ := t.store.Outline.CheckArcBoundary(chapter); boundary != nil {
			result.ArcEnd = boundary.IsArcEnd
			result.VolumeEnd = boundary.IsVolumeEnd
			result.Volume = boundary.Volume
			result.Arc = boundary.Arc
			result.NeedsExpansion = boundary.NeedsExpansion
			result.NeedsNewVolume = boundary.NeedsNewVolume
			result.NextVolume = boundary.NextVolume
			result.NextArc = boundary.NextArc
		}
	}
	result.ReviewRequired = !t.store.World.HasAcceptedChapterReview(chapter)
	if result.ReviewRequired {
		result.ReviewReason = fmt.Sprintf("第 %d 章终稿已提交，必须先通过章级审阅", chapter)
	}

	if progress != nil {
		if progress.Phase == domain.PhaseComplete {
			result.BookComplete = true
		}
		result.Flow = string(progress.Flow)
	}

	return json.Marshal(result)
}

// loadCoreCharacterNameSet 加载 characters.json 中已有的角色名集合（含别名）。
// 用作 cast_ledger 的"已知核心"过滤集——核心角色不进次要名册。
// 加载失败时返回 nil（merge 时所有 characters 都进 ledger，可接受）。
func loadCoreCharacterNameSet(s *store.Store) map[string]bool {
	chars, err := s.Characters.Load()
	if err != nil || len(chars) == 0 {
		return nil
	}
	set := make(map[string]bool, len(chars)*2)
	for _, c := range chars {
		if c.Name != "" {
			set[c.Name] = true
		}
		for _, alias := range c.Aliases {
			if alias != "" {
				set[alias] = true
			}
		}
	}
	return set
}

func validateCommitCharacterStage(s *store.Store, chapter int, records []domain.CharacterStageRecord) error {
	if len(records) == 0 {
		return fmt.Errorf("第 %d 章提交缺少 character_stage_records：必须沉淀主角和关键配角的场景、行动、位置、交通/见面限制和状态变化: %w", chapter, errs.ErrToolPrecondition)
	}
	protagonist := inferCommitProtagonist(s)
	simulationRequired := chapterWorldSimulationRequired(s)
	hasSideCharacter := false
	var missing []string
	for i, r := range records {
		prefix := fmt.Sprintf("character_stage_records[%d]", i)
		name := strings.TrimSpace(r.Character)
		require := func(ok bool, field string) {
			if !ok {
				missing = append(missing, prefix+"."+field)
			}
		}
		require(name != "", "character")
		require(strings.TrimSpace(r.Location) != "", "location")
		require(strings.TrimSpace(r.Environment) != "", "environment")
		require(strings.TrimSpace(r.CurrentAction) != "", "current_action")
		require(strings.TrimSpace(r.Pressure) != "", "pressure")
		require(strings.TrimSpace(r.Decision) != "", "decision")
		if simulationRequired {
			require(strings.TrimSpace(r.DecisionReason) != "", "decision_reason")
			require(len(r.ButterflyEffects) > 0, "butterfly_effects")
		}
		require(strings.TrimSpace(r.KnowledgeBoundary) != "", "knowledge_boundary")
		require(strings.TrimSpace(r.TimelineConsistency) != "", "timeline_consistency")
		if name != "" && (protagonist == "" || name != protagonist) {
			hasSideCharacter = true
		}
	}
	if !hasSideCharacter {
		missing = append(missing, "side_character_record(non_protagonist)")
	}
	if missingCharacters := missingStageCoverage(requiredDossierCharacterNames(s, chapter), records); len(missingCharacters) > 0 {
		missing = append(missing, formatMissingCharacterCoverage("character_stage_records", missingCharacters))
	}
	if len(missing) > 0 {
		return fmt.Errorf("第 %d 章角色动态台账不完整，缺少：%s: %w", chapter, strings.Join(missing, ", "), errs.ErrToolPrecondition)
	}
	return nil
}

func inferCommitProtagonist(s *store.Store) string {
	chars, err := s.Characters.Load()
	if err != nil {
		return ""
	}
	for _, c := range chars {
		role := strings.ToLower(c.Role)
		if c.Tier == "core" || strings.Contains(c.Role, "主角") || strings.Contains(role, "protagonist") {
			return c.Name
		}
	}
	if len(chars) > 0 {
		return chars[0].Name
	}
	return ""
}

func hasMovementConstraint(r domain.CharacterStageRecord) bool {
	return strings.TrimSpace(r.Transport) != "" ||
		strings.TrimSpace(r.TravelTime) != "" ||
		strings.TrimSpace(r.MeetingConstraint) != ""
}

// rollingVolumeOutlineSignals 实现滚动大纲策略的提交侧信号：
//   - 本章是某卷第一章 → volume_outline_due：必须派 architect 敲定下两卷动态大纲
//     （含各自 volume_codex 卷级上限）；已详细展开的卷只需复核。
//   - 每章例行 → volume_outline_review：结合本章 feedback 判断是否要修订下两卷。
func (t *CommitChapterTool) rollingVolumeOutlineSignals(chapter int) (due string, review string) {
	volumes, err := t.store.Outline.LoadLayeredOutline()
	if err != nil || len(volumes) == 0 {
		return "", ""
	}
	currentIdx := -1
	firstChapter := 0
	for i, vol := range volumes {
		lo, hi := volumeChapterRange(vol)
		if lo == 0 {
			continue
		}
		if chapter >= lo && chapter <= hi {
			currentIdx = i
			firstChapter = lo
			break
		}
	}
	if currentIdx < 0 {
		return "", ""
	}
	// 下两卷的状态描述
	nextTwoStatus := func() string {
		var parts []string
		for offset := 1; offset <= 2; offset++ {
			idx := currentIdx + offset
			if idx >= len(volumes) {
				parts = append(parts, fmt.Sprintf("第%d卷：尚未创建（需 append_volume）", volumes[currentIdx].Index+offset))
				continue
			}
			vol := volumes[idx]
			detailed := 0
			total := 0
			for _, arc := range vol.Arcs {
				total++
				if len(arc.Chapters) > 0 {
					detailed++
				}
			}
			codexNote := "卷级上限未生成"
			if vc, err := t.store.LoadVolumeCodex(vol.Index); err == nil && vc != nil {
				codexNote = "卷级上限已生成"
			}
			parts = append(parts, fmt.Sprintf("第%d卷《%s》：%d/%d 弧已详细展开，%s", vol.Index, vol.Title, detailed, total, codexNote))
		}
		return strings.Join(parts, "；")
	}
	review = "每章例行：若本章 feedback 偏离当前卷走向，同步评估是否修订下两卷动态大纲（" + nextTwoStatus() + "）。"
	if chapter == firstChapter {
		due = fmt.Sprintf("已进入第%d卷第一章：必须派 architect 敲定下两卷动态大纲并生成各自 volume_codex 卷级上限。当前状态：%s", volumes[currentIdx].Index, nextTwoStatus())
	}
	return due, review
}

// volumeChapterRange 返回一卷覆盖的章号区间；卷未详细展开时返回 (0,0)。
func volumeChapterRange(vol domain.VolumeOutline) (lo, hi int) {
	for _, arc := range vol.Arcs {
		for _, ch := range arc.Chapters {
			if ch.Chapter <= 0 {
				continue
			}
			if lo == 0 || ch.Chapter < lo {
				lo = ch.Chapter
			}
			if ch.Chapter > hi {
				hi = ch.Chapter
			}
		}
	}
	return lo, hi
}
