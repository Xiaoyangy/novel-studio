package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/errs"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/voocel/agentcore/schema"
)

// ReopenBookTool 把已完结的书重新打开进入返工态（仅 Coordinator 持有）。
// 完本后 completePhaseGate 硬拦一切 subagent 派发，用户无法返工已写章节。
// 本工具不是 subagent，complete 期可调：它原子地把 phase 切回 writing、目标章入
// PendingRewrites、flow=rewriting，随后 Flow Router 照既有返工队列派 writer 逐章重写，
// 队列跑完 commit_chapter 自动重新收尾完结。Gate / Router / edit / commit 重逻辑均无需改动。
type ReopenBookTool struct {
	store *store.Store
}

func NewReopenBookTool(s *store.Store) *ReopenBookTool {
	return &ReopenBookTool{store: s}
}

func (t *ReopenBookTool) Name() string  { return "reopen_book" }
func (t *ReopenBookTool) Label() string { return "重开返工" }

func (t *ReopenBookTool) Description() string {
	return "把已完结（phase=complete）的全书重新打开进入返工态，用于用户在完本后要求重写/打磨某几章。" +
		"chapters 是要返工的已完成章节号；调用后这些章进入重写队列，Host 会逐章派 writer 重写，全部改完自动重新完结。" +
		"仅在全书已完结、且用户明确要求修改已写章节时使用；用户要新增剧情/扩展篇幅不属返工，不要用本工具。"
}

// 写工具，禁止并发。
func (t *ReopenBookTool) ReadOnly(_ json.RawMessage) bool        { return false }
func (t *ReopenBookTool) ConcurrencySafe(_ json.RawMessage) bool { return false }

func (t *ReopenBookTool) ActivityDescription(_ json.RawMessage) string {
	return "重新打开全书返工"
}

func (t *ReopenBookTool) Schema() map[string]any {
	return schema.Object(
		schema.Property("chapters", schema.Array("要返工的已完成章节号列表（至少一章）", schema.Int(""))).Required(),
		schema.Property("reason", schema.String("返工原因（可选，如\"清理特殊字符\"）")),
	)
}

func (t *ReopenBookTool) Execute(_ context.Context, args json.RawMessage) (json.RawMessage, error) {
	var a struct {
		Chapters []int  `json:"chapters"`
		Reason   string `json:"reason"`
	}
	if err := unmarshalToolArgs(args, &a); err != nil {
		return nil, fmt.Errorf("invalid args: %w: %w", errs.ErrToolArgs, err)
	}
	if len(a.Chapters) == 0 {
		return nil, fmt.Errorf("chapters 不能为空，需指明要返工的章节: %w", errs.ErrToolArgs)
	}

	progress, err := t.store.Progress.Load()
	if err != nil {
		return nil, fmt.Errorf("load progress: %w: %w", errs.ErrStoreRead, err)
	}
	if progress == nil {
		return nil, fmt.Errorf("progress 未初始化: %w", errs.ErrToolPrecondition)
	}
	// 只能返工已写章；不在已完成集合的章号属续写/越界，明确拒绝引导用户走篇幅调整。
	var invalid []int
	for _, ch := range a.Chapters {
		if !slices.Contains(progress.CompletedChapters, ch) {
			invalid = append(invalid, ch)
		}
	}
	if len(invalid) > 0 {
		return nil, fmt.Errorf("第 %v 章尚未写完，reopen 只能返工已完成章节（新增/扩展剧情请走篇幅调整）: %w", invalid, errs.ErrToolPrecondition)
	}

	// phase 前置校验在 store.Reopen 内兜底（仅 complete 可调）。
	if err := t.store.Progress.Reopen(a.Chapters, a.Reason); err != nil {
		return nil, fmt.Errorf("reopen: %w: %w", errs.ErrStoreWrite, err)
	}

	// checkpoint：与 complete_book 对称（GlobalScope + meta/progress.json）。
	if _, err := t.store.Checkpoints.AppendArtifact(domain.GlobalScope(), "reopen", "meta/progress.json"); err != nil {
		return nil, fmt.Errorf("checkpoint reopen: %w: %w", errs.ErrStoreWrite, err)
	}

	return json.Marshal(map[string]any{
		"reopened":         true,
		"phase":            string(domain.PhaseWriting),
		"pending_rewrites": a.Chapters,
		"next_step":        "已重新打开并把目标章入队。请等待 Host 指令派 writer 逐章返工；全部改完后会自动重新完结。",
	})
}
