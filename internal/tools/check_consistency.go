package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/errs"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/voocel/agentcore/schema"
)

// CheckConsistencyTool 返回章节内容和全部状态数据，供 Agent 自行对照判断。
// 纯 IO 工具：只负责加载数据，不注入指令。
type CheckConsistencyTool struct {
	store *store.Store
}

func NewCheckConsistencyTool(store *store.Store) *CheckConsistencyTool {
	return &CheckConsistencyTool{store: store}
}

func (t *CheckConsistencyTool) Name() string { return "check_consistency" }
func (t *CheckConsistencyTool) Description() string {
	return "加载已写草稿和对照数据（世界规则、伏笔、关系、别名、最近摘要），供你检查一致性。必须在 draft_chapter 之后调用"
}
func (t *CheckConsistencyTool) Label() string { return "一致性检查" }

// 只读工具（仅追加 checkpoint 事件，不改状态），可被并发调度。
func (t *CheckConsistencyTool) ReadOnly(_ json.RawMessage) bool        { return true }
func (t *CheckConsistencyTool) ConcurrencySafe(_ json.RawMessage) bool { return true }

func (t *CheckConsistencyTool) Schema() map[string]any {
	return schema.Object(
		schema.Property("chapter", schema.Int("要检查的章节号")).Required(),
	)
}

func (t *CheckConsistencyTool) Execute(_ context.Context, args json.RawMessage) (json.RawMessage, error) {
	var a struct {
		Chapter int `json:"chapter"`
	}
	if err := unmarshalToolArgs(args, &a); err != nil {
		return nil, fmt.Errorf("invalid args: %w: %w", errs.ErrToolArgs, err)
	}
	if a.Chapter <= 0 {
		return nil, fmt.Errorf("chapter must be > 0: %w", errs.ErrToolArgs)
	}

	result := map[string]any{"chapter": a.Chapter}

	// 章节内容
	content, wordCount, err := t.store.Drafts.LoadChapterContent(a.Chapter)
	if err != nil {
		return nil, fmt.Errorf("load chapter content: %w: %w", errs.ErrStoreRead, err)
	}
	if content == "" {
		return nil, fmt.Errorf("no content found for chapter %d: %w", a.Chapter, errs.ErrToolPrecondition)
	}
	result["content"] = content
	result["word_count"] = wordCount

	// 对照数据：保留全局性的一致性检查数据，避免重复加载 novel_context 已有的窗口数据
	if rules, _ := t.store.World.LoadWorldRules(); len(rules) > 0 {
		result["world_rules"] = rules
	}
	if world, _ := t.store.World.LoadBookWorld(); world != nil {
		result["book_world"] = world
	}
	if foreshadow, _ := t.store.World.LoadActiveForeshadow(); len(foreshadow) > 0 {
		result["foreshadow_ledger"] = foreshadow
	}
	if relationships, _ := t.store.World.LoadRelationships(); len(relationships) > 0 {
		result["relationships"] = relationships
	}
	if chars, _ := t.store.Characters.Load(); len(chars) > 0 {
		aliasMap := make(map[string]string)
		for _, c := range chars {
			for _, alias := range c.Aliases {
				aliasMap[alias] = c.Name
			}
		}
		if len(aliasMap) > 0 {
			result["alias_map"] = aliasMap
		}
	}
	if summaries, _ := t.store.Summaries.LoadRecentSummaries(a.Chapter, 2); len(summaries) > 0 {
		result["recent_summaries"] = summaries
	}
	if participants := participantsFromConsistencyResult(result); len(participants) > 0 {
		if audit, _ := t.store.ResourceLedger.AuditForParticipants(participants); len(audit.Booked) > 0 || len(audit.Pending) > 0 {
			result["resource_audit"] = audit
		}
	}
	if warnings, _ := t.store.ResourceLedger.AuditTextForPendingFacts(content); len(warnings) > 0 {
		result["resource_warnings"] = warnings
	}
	// Task 074：确定性对账结果先行——存亡/位置/资源/时序/别名五类机器筛查，
	// 每条带原文短引证据；你（LLM）的职责是复核这些并补机器看不见的语义矛盾。
	if reconcile := consistencyReconcile(t.store, a.Chapter, content, nil); len(reconcile) > 0 {
		result["machine_reconcile"] = reconcile
		result["machine_reconcile_usage"] = "机器对账（warning 级事实）：逐条对照原文证据确认真伪；确认为真的矛盾必须在返回问题里列出并给修复建议"
	}

	if _, err := t.store.Checkpoints.AppendArtifact(
		domain.ChapterScope(a.Chapter), "consistency_check",
		fmt.Sprintf("drafts/%02d.draft.md", a.Chapter),
	); err != nil {
		return nil, fmt.Errorf("checkpoint consistency check: %w", err)
	}

	return json.Marshal(result)
}

func participantsFromConsistencyResult(result map[string]any) []string {
	var out []string
	if summaries, ok := result["recent_summaries"].([]domain.ChapterSummary); ok && len(summaries) > 0 {
		for _, sum := range summaries {
			out = append(out, sum.Characters...)
		}
	}
	return out
}
