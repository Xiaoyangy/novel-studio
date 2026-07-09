package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/voocel/agentcore/schema"
)

// ReadChapterTool 读取章节原文，让 Agent 能回读自己和前文的文字。
type ReadChapterTool struct {
	store *store.Store
}

func NewReadChapterTool(store *store.Store) *ReadChapterTool {
	return &ReadChapterTool{store: store}
}

func (t *ReadChapterTool) Name() string { return "read_chapter" }
func (t *ReadChapterTool) Description() string {
	return "读取章节原文。可读终稿、整章草稿、分片草稿，或提取角色对话片段"
}
func (t *ReadChapterTool) Label() string { return "读取章节" }

// 纯读工具，可被并发调度（editor 审阅时常一次读多章）。
func (t *ReadChapterTool) ReadOnly(_ json.RawMessage) bool        { return true }
func (t *ReadChapterTool) ConcurrencySafe(_ json.RawMessage) bool { return true }

func (t *ReadChapterTool) Schema() map[string]any {
	return schema.Object(
		schema.Property("chapter", schema.Int("章节号（读单章时可省略；省略时默认当前写作章）")),
		schema.Property("from", schema.Int("起始章节号（读范围时使用）")),
		schema.Property("to", schema.Int("结束章节号（读范围时使用）")),
		schema.Property("source", schema.Enum("来源", "final", "draft", "draft_part")).Required(),
		schema.Property("part", schema.Int("分片序号（source=draft_part 时使用；不填则返回分片索引）")),
		schema.Property("character", schema.String("角色名（提取对话片段时使用）")),
		schema.Property("max_runes", schema.Int("每章最大字符数（范围读取时截取，默认 2000）")),
	)
}

func (t *ReadChapterTool) Execute(_ context.Context, args json.RawMessage) (json.RawMessage, error) {
	var a struct {
		Chapter   int    `json:"chapter"`
		From      int    `json:"from"`
		To        int    `json:"to"`
		Source    string `json:"source"`
		Part      int    `json:"part"`
		Character string `json:"character"`
		MaxRunes  int    `json:"max_runes"`
	}
	if err := unmarshalToolArgs(args, &a); err != nil {
		return nil, fmt.Errorf("invalid args: %w", err)
	}

	// 模式 1：提取角色对话
	if a.Character != "" {
		chars, _ := t.store.Characters.Load()
		var aliases []string
		for _, c := range chars {
			if c.Name == a.Character {
				aliases = c.Aliases
				break
			}
		}
		var maxCompleted int
		if p, _ := t.store.Progress.Load(); p != nil {
			maxCompleted = maxCompletedChapter(p.CompletedChapters)
		}
		samples := t.store.Drafts.ExtractDialogue(a.Character, aliases, 8, maxCompleted)
		result := map[string]any{
			"character": a.Character,
			"samples":   samples,
		}
		if len(samples) == 0 {
			result["hint"] = "该角色暂无对话样本，无需重试，直接进入下一步"
		}
		return json.Marshal(result)
	}

	// 模式 2：范围读取
	if a.From > 0 && a.To > 0 {
		maxRunes := a.MaxRunes
		if maxRunes <= 0 {
			maxRunes = 2000
		}
		texts, err := t.store.Drafts.LoadChapterRange(a.From, a.To, maxRunes)
		if err != nil {
			return nil, fmt.Errorf("load chapter range: %w", err)
		}
		return json.Marshal(map[string]any{
			"chapters": texts,
			"from":     a.From,
			"to":       a.To,
		})
	}

	// 模式 3：单章读取
	if a.Chapter <= 0 {
		resolved, err := t.defaultReadChapter()
		if err != nil {
			return nil, err
		}
		a.Chapter = resolved
	}

	if a.Source == "draft_part" {
		if a.Part <= 0 {
			index, err := t.store.Drafts.LoadDraftPartIndex(a.Chapter)
			if err != nil {
				return nil, fmt.Errorf("read draft part index for chapter %d: %w", a.Chapter, err)
			}
			if index == nil {
				return json.Marshal(map[string]any{
					"chapter": a.Chapter,
					"exists":  false,
					"hint":    "该章节尚未写入分片草稿；如需分片写作请先调用 draft_chapter_part",
				})
			}
			return json.Marshal(map[string]any{
				"chapter": a.Chapter,
				"exists":  true,
				"index":   index,
			})
		}
		content, err := t.store.Drafts.LoadDraftPartContent(a.Chapter, a.Part)
		if err != nil {
			return nil, fmt.Errorf("read draft part %d.%d: %w", a.Chapter, a.Part, err)
		}
		if content == "" {
			return json.Marshal(map[string]any{
				"chapter": a.Chapter,
				"part":    a.Part,
				"exists":  false,
				"hint":    "该分片尚未写入；调用 draft_chapter_part 补写后再继续",
			})
		}
		return json.Marshal(map[string]any{
			"chapter":    a.Chapter,
			"part":       a.Part,
			"content":    content,
			"word_count": len([]rune(content)),
		})
	}

	var content string
	var err error
	switch a.Source {
	case "draft":
		content, err = t.store.Drafts.LoadDraft(a.Chapter)
	default: // final
		content, err = t.store.Drafts.LoadChapterText(a.Chapter)
		if err == nil && content == "" {
			slog.Warn("read_chapter 读取终稿为空，回退到草稿", "module", "tool", "chapter", a.Chapter)
			content, err = t.store.Drafts.LoadDraft(a.Chapter)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("read chapter %d: %w", a.Chapter, err)
	}
	if content == "" {
		return json.Marshal(map[string]any{
			"chapter": a.Chapter,
			"exists":  false,
			"hint":    "该章节尚未写入，如需写作请先调用 draft_chapter",
		})
	}

	return json.Marshal(map[string]any{
		"chapter":    a.Chapter,
		"content":    content,
		"word_count": len([]rune(content)),
	})
}

func (t *ReadChapterTool) defaultReadChapter() (int, error) {
	p, err := t.store.Progress.Load()
	if err != nil {
		return 0, fmt.Errorf("load progress for default read_chapter: %w", err)
	}
	if p == nil {
		return 0, fmt.Errorf("chapter is required")
	}
	if p.InProgressChapter > 0 {
		return p.InProgressChapter, nil
	}
	if p.CurrentChapter > 0 {
		return p.CurrentChapter, nil
	}
	if ch := maxCompletedChapter(p.CompletedChapters); ch > 0 {
		return ch, nil
	}
	return 0, fmt.Errorf("chapter is required")
}

// maxCompletedChapter 返回已完成章节列表中的最大章节号。
func maxCompletedChapter(completed []int) int {
	m := 0
	for _, ch := range completed {
		if ch > m {
			m = ch
		}
	}
	return m
}
