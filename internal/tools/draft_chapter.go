package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"unicode/utf8"

	"github.com/chenhongyang/novel-studio/internal/domain"
	editrules "github.com/chenhongyang/novel-studio/internal/editor/rules"
	"github.com/chenhongyang/novel-studio/internal/errs"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/voocel/agentcore/schema"
)

// DraftChapterTool 写入整章草稿，替代旧的 write_scene + polish_chapter 流水线。
// Agent 自主决定一次写完还是分批续写。
type DraftChapterTool struct {
	store *store.Store
}

func NewDraftChapterTool(store *store.Store) *DraftChapterTool {
	return &DraftChapterTool{store: store}
}

func (t *DraftChapterTool) Name() string { return "draft_chapter" }
func (t *DraftChapterTool) Description() string {
	return "写入章节正文。mode=write 覆盖写入整章，mode=append 追加到现有草稿（续写/修改）"
}
func (t *DraftChapterTool) Label() string { return "写入章节" }

// 写工具，禁止并发（读-改-写竞态）。
func (t *DraftChapterTool) ReadOnly(_ json.RawMessage) bool        { return false }
func (t *DraftChapterTool) ConcurrencySafe(_ json.RawMessage) bool { return false }

func (t *DraftChapterTool) Schema() map[string]any {
	// mode 标 required 是为了兼容 OpenAI strict tool calling——strict 模式
	// 要求所有 properties 都在 required 列表中。原来的"省略 mode 走 write
	// 默认"行为现在需要模型显式传 mode="write"，Execute 的 default 分支不变。
	return schema.Object(
		schema.Property("chapter", schema.Int("章节号")).Required(),
		schema.Property("content", schema.String("章节正文")).Required(),
		schema.Property("mode", schema.Enum("写入模式", "write", "append")).Required(),
	)
}

// StrictSchema 启用 OpenAI 的 strict tool calling，让模型必须严格遵守
// schema：所有 required 字段必填，arguments 不能"提前 EOT"出现空对象。
// litellm 透传 strict 字段；OpenAI / xAI 等支持的后端会强制执行，其他后端
// 按 HTTP/JSON 惯例忽略未知字段。Anthropic/Gemini/Bedrock 走各自的转换链路
// 自然不会看到这个字段。
func (t *DraftChapterTool) StrictSchema() bool { return true }

func (t *DraftChapterTool) Execute(_ context.Context, args json.RawMessage) (json.RawMessage, error) {
	var a struct {
		Chapter  int                    `json:"chapter"`
		Content  string                 `json:"content"`
		Mode     string                 `json:"mode"`
		Sampling *domain.SamplingRecord `json:"sampling"`
	}
	if err := unmarshalToolArgs(args, &a); err != nil {
		return nil, fmt.Errorf("invalid args: %w: %w", errs.ErrToolArgs, err)
	}
	if a.Chapter <= 0 {
		return nil, fmt.Errorf("chapter must be > 0: %w", errs.ErrToolArgs)
	}
	if a.Content == "" {
		return nil, fmt.Errorf("content must not be empty: %w", errs.ErrToolArgs)
	}
	if err := t.store.Progress.ValidateChapterWork(a.Chapter); err != nil {
		return nil, err
	}
	if err := EnsureChapterExpanded(t.store, a.Chapter); err != nil {
		return nil, err
	}
	if t.store.Progress.IsChapterCompleted(a.Chapter) {
		// 打磨/重写路径：章节虽已完成，但仍在 pending_rewrites 中，允许覆盖草稿
		progress, _ := t.store.Progress.Load()
		inRewriteQueue := progress != nil && slices.Contains(progress.PendingRewrites, a.Chapter)
		if !inRewriteQueue {
			return json.Marshal(map[string]any{
				"chapter":   a.Chapter,
				"skipped":   true,
				"completed": true,
				"reason":    fmt.Sprintf("第 %d 章已提交完成，不能覆盖", a.Chapter),
			})
		}
	}
	if latest := t.store.Checkpoints.Latest(domain.ChapterScope(a.Chapter)); latest != nil && latest.Step == "draft" {
		return nil, fmt.Errorf(
			"第 %d 章已有草稿且尚未执行 check_consistency，禁止连续 draft_chapter；请先 read_chapter(source=draft)，再调用 check_consistency，若无硬伤直接 commit_chapter: %w",
			a.Chapter, errs.ErrToolPrecondition,
		)
	}
	if err := t.store.Progress.StartChapter(a.Chapter); err != nil {
		return nil, fmt.Errorf("mark chapter in progress: %w", err)
	}

	switch a.Mode {
	case "append":
		if err := t.store.Drafts.AppendDraft(a.Chapter, a.Content); err != nil {
			return nil, fmt.Errorf("append draft: %w", err)
		}
		full, err := t.store.Drafts.LoadDraft(a.Chapter)
		if err != nil {
			return nil, fmt.Errorf("load draft after append: %w", err)
		}
		if _, err := t.store.Checkpoints.AppendArtifact(
			domain.ChapterScope(a.Chapter), "draft",
			fmt.Sprintf("drafts/%02d.draft.md", a.Chapter),
		); err != nil {
			return nil, fmt.Errorf("checkpoint draft: %w", err)
		}
		analysis, err := t.saveDraftAIVoice(a.Chapter, full, a.Sampling)
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{
			"written":            true,
			"chapter":            a.Chapter,
			"mode":               "append",
			"word_count":         utf8.RuneCountInString(full),
			"ai_voice_score":     analysis.Metrics.AIVoiceScore,
			"figurative_density": analysis.Metrics.FigurativeDensity,
			"dialogue_ratio":     analysis.Metrics.DialogueRatio,
			"next_step":          "草稿已成功保存。不要再次调用 draft_chapter 重写同一章；立即 read_chapter(source=draft) 回读草稿，再调用 check_consistency。若无硬伤，必须调用 commit_chapter 提交终稿。",
		})
	default: // write
		if err := t.store.Drafts.SaveDraft(a.Chapter, a.Content); err != nil {
			return nil, fmt.Errorf("save draft: %w", err)
		}
		if _, err := t.store.Checkpoints.AppendArtifact(
			domain.ChapterScope(a.Chapter), "draft",
			fmt.Sprintf("drafts/%02d.draft.md", a.Chapter),
		); err != nil {
			return nil, fmt.Errorf("checkpoint draft: %w", err)
		}
		analysis, err := t.saveDraftAIVoice(a.Chapter, a.Content, a.Sampling)
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{
			"written":            true,
			"chapter":            a.Chapter,
			"mode":               "write",
			"word_count":         utf8.RuneCountInString(a.Content),
			"ai_voice_score":     analysis.Metrics.AIVoiceScore,
			"figurative_density": analysis.Metrics.FigurativeDensity,
			"dialogue_ratio":     analysis.Metrics.DialogueRatio,
			"next_step":          "草稿已成功保存。不要再次调用 draft_chapter 重写同一章；立即 read_chapter(source=draft) 回读草稿，再调用 check_consistency。若无硬伤，必须调用 commit_chapter 提交终稿。",
		})
	}
}

func (t *DraftChapterTool) saveDraftAIVoice(chapter int, content string, sampling *domain.SamplingRecord) (domain.AIVoiceAnalysis, error) {
	history, _ := t.store.AIVoice.LoadAllChapterMetrics()
	analysis := editrules.AnalyzeChapter(chapter, content, history)
	if err := t.store.AIVoice.SaveChapterMetrics(analysis.Metrics, true); err != nil {
		return analysis, fmt.Errorf("save draft ai voice metrics: %w: %w", errs.ErrStoreWrite, err)
	}
	if sampling != nil {
		sampling.Chapter = chapter
		if err := t.store.AIVoice.SaveSamplingRecord(*sampling); err != nil {
			return analysis, fmt.Errorf("save sampling record: %w: %w", errs.ErrStoreWrite, err)
		}
	}
	return analysis, nil
}
