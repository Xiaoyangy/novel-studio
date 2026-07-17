package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/voocel/agentcore/schema"
)

// ReadChapterTool 读取章节原文，让 Agent 能回读自己和前文的文字。
type ReadChapterTool struct {
	store *store.Store
}

type chapterSurfaceWithholding struct {
	stage    string
	trigger  string
	reason   string
	nextStep string
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
	if err := t.guardFrozenRenderRead(&a); err != nil {
		return nil, err
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
		if a.Source == "" || a.Source == "final" || a.Source == "draft" {
			for chapter := a.From; chapter <= a.To; chapter++ {
				withholding, err := t.renderOnlySurfaceWithholding(chapter)
				if err != nil {
					return nil, fmt.Errorf("inspect chapter %d render-only read guard: %w", chapter, err)
				}
				if withholding != nil {
					return json.Marshal(map[string]any{
						"chapter":          chapter,
						"blocked_chapter":  chapter,
						"requested_from":   a.From,
						"requested_to":     a.To,
						"requested_source": normalizedChapterSurfaceSource(a.Source),
						"withheld":         true,
						"stage":            withholding.stage,
						"trigger":          withholding.trigger,
						"reason":           withholding.reason,
						"next_step":        withholding.nextStep,
					})
				}
			}
		}
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
	if a.Source == "" || a.Source == "final" || a.Source == "draft" {
		withholding, err := t.renderOnlySurfaceWithholding(a.Chapter)
		if err != nil {
			return nil, fmt.Errorf("inspect chapter %d render-only read guard: %w", a.Chapter, err)
		}
		if withholding != nil {
			return json.Marshal(map[string]any{
				"chapter":          a.Chapter,
				"requested_source": normalizedChapterSurfaceSource(a.Source),
				"withheld":         true,
				"stage":            withholding.stage,
				"trigger":          withholding.trigger,
				"reason":           withholding.reason,
				"next_step":        withholding.nextStep,
			})
		}
	}
	if (a.Source == "" || a.Source == "final") && t.rewritePlanningBlocksFinalRead(a.Chapter) {
		return json.Marshal(map[string]any{
			"chapter":  a.Chapter,
			"withheld": true,
			"stage":    "rewrite_replanning",
			"reason":   "待返工终稿属于上一轮正文；在新 plan 正式收口前注入全文会把旧事件顺序重新带回规划。",
			"next_step": "使用 novel_context(chapter=N).rewrite_brief、current_chapter_outline 与 chapter_plan_stage 完成 plan_structure/plan_details；" +
				"正式 plan 生成后，Drafter 可再次读取旧终稿作局部借鉴。",
		})
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

// guardFrozenRenderRead prevents read_chapter from becoming a live-canon
// side door after the prose payload was frozen. The only readable surface in a
// render lease is the target chapter's draft produced by that same lease,
// which Drafter/Finalizer must be able to inspect and edit. Prior canon,
// character dialogue samples, ranges, and the superseded final body are
// already represented by the frozen render packet and may not be reloaded.
func (t *ReadChapterTool) guardFrozenRenderRead(a *struct {
	Chapter   int    `json:"chapter"`
	From      int    `json:"from"`
	To        int    `json:"to"`
	Source    string `json:"source"`
	Part      int    `json:"part"`
	Character string `json:"character"`
	MaxRunes  int    `json:"max_runes"`
}) error {
	if t == nil || t.store == nil || a == nil {
		return nil
	}
	lock, err := t.store.Runtime.LoadPipelineExecution()
	if err != nil {
		return fmt.Errorf("read_chapter 读取 pipeline execution lock: %w", err)
	}
	if lock == nil || lock.Mode != domain.PipelineExecutionRender {
		return nil
	}
	if err := requireCurrentPipelineExecutionProcess(lock, "read_chapter"); err != nil {
		return err
	}
	source := strings.TrimSpace(a.Source)
	if a.Chapter <= 0 && a.From <= 0 && a.To <= 0 && strings.TrimSpace(a.Character) == "" {
		a.Chapter = lock.TargetChapter
	}
	if strings.TrimSpace(a.Character) != "" || a.From > 0 || a.To > 0 ||
		a.Chapter != lock.TargetChapter || (source != "draft" && source != "draft_part") {
		return fmt.Errorf(
			"render execution lock 只允许 read_chapter(chapter=%d, source=draft|draft_part) 回读本次执行产生的候选；收到 chapter=%d from=%d to=%d source=%q character=%q。前文、旧终稿、范围与声口样本必须来自冻结 render context",
			lock.TargetChapter,
			a.Chapter,
			a.From,
			a.To,
			a.Source,
			a.Character,
		)
	}
	return nil
}

func normalizedChapterSurfaceSource(source string) string {
	if source == "" {
		return "final"
	}
	return source
}

// renderOnlySurfaceWithholding keeps the superseded prose surface out of a
// clean whole-chapter render. InspectDraftExternalGateWithStore runs first so
// an interrupted but complete replacement draft can recover its checkpoint;
// after recovery the explicit request is consumed and draft_finalizer may read
// the current candidate normally.
func (t *ReadChapterTool) renderOnlySurfaceWithholding(chapter int) (*chapterSurfaceWithholding, error) {
	inspection, err := InspectDraftExternalGateWithStore(t.store, chapter)
	if err != nil {
		return nil, err
	}
	if ExplicitRerenderRequestActive(t.store, chapter) {
		return &chapterSurfaceWithholding{
			stage:    "render_only_fresh_draft",
			trigger:  "explicit_full_rerender",
			reason:   "显式整章重渲染授权尚未被新 draft checkpoint 消费；旧 draft/final 的措辞与段落表面已作废。",
			nextStep: "只使用 novel_context(chapter=N, profile=draft) 提供的净化证据与正式 plan，调用 draft_chapter(mode=write) 产生完整新稿。",
		}, nil
	}
	if inspection.Status == DraftExternalGateRerenderAuthorized {
		return &chapterSurfaceWithholding{
			stage:    "render_only_fresh_draft",
			trigger:  "external_full_rerender",
			reason:   "当前正文已被整章外审阻断并授权重渲染；旧 draft/final 只能作为哈希化失败对象，不能重新注入正文表面。",
			nextStep: "只使用 novel_context(chapter=N, profile=draft) 中净化后的外审证据、rewrite_brief 与正式 plan，调用 draft_chapter(mode=write) 产生完整新稿。",
		}, nil
	}
	if ReviewRequiresFreshDraft(t.store, chapter) {
		return &chapterSurfaceWithholding{
			stage:    "render_only_fresh_draft",
			trigger:  "formal_review_fresh_draft",
			reason:   "正式复审仍拒绝当前 draft/final 的同一正文哈希；回读旧表面只会复现已被拒绝的稿件。",
			nextStep: "只使用 novel_context(chapter=N, profile=draft) 中净化后的 review/rewrite_brief 与可复用正式 plan，调用 draft_chapter(mode=write) 产生不同哈希的新稿。",
		}, nil
	}
	return nil, nil
}

func (t *ReadChapterTool) rewritePlanningBlocksFinalRead(chapter int) bool {
	rewriteTarget, ok := pendingRewriteTarget(t.store)
	if !ok || rewriteTarget != chapter {
		return false
	}
	if partial, err := t.store.Drafts.LoadChapterPlanPartial(chapter); err == nil && partial != nil {
		return true
	}
	plan, err := t.store.Drafts.LoadChapterPlan(chapter)
	if err != nil || plan == nil {
		return true
	}
	return chapterArtifactNotNewerThanFinal(t.store.Dir(), chapter, fmt.Sprintf("drafts/%02d.plan.json", chapter))
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
