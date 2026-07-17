package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/errs"
	"github.com/chenhongyang/novel-studio/internal/rules"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/userrules"
	"github.com/voocel/agentcore/schema"
)

// SaveUserRulesTool 持久化用户的长效"写作风格/质量"要求（仅 Coordinator 持有）。
//
// 它是长期写作规则的统一入口：任何时候都成立、约束 writer 笔法的风格/质量规则（如"每章1500字"
// "少用比喻""禁止出现'某种程度上'""对话占比高一点""主角整体冷静克制"）经 LLM 归一化为结构化
// 约束写入本书快照 meta/user_rules.json，novel_context 注入 working_memory.user_rules、
// commit_chapter 据此机械检查。剧情/结构/人物/阶段调整走 architect，已写章节返工先走 editor 入队，
// 后续由 Host 派 writer 改写。
//
// 归一化失败不报错（降级为 raw preferences），只有落盘失败才返回 tool error——
// 技术细节不应抛回 Coordinator 当流程错误。
type SaveUserRulesTool struct {
	svc   *userrules.Service
	store *store.Store
}

func NewSaveUserRulesTool(svc *userrules.Service, st *store.Store) *SaveUserRulesTool {
	return &SaveUserRulesTool{svc: svc, store: st}
}

func (t *SaveUserRulesTool) Name() string  { return "save_user_rules" }
func (t *SaveUserRulesTool) Label() string { return "保存写作规则" }

func (t *SaveUserRulesTool) Description() string {
	return "把用户的长效写作风格/质量要求归一化为本书的结构化规则并持久化" +
		"（如\"每章1500字左右\"\"少用比喻和排比\"\"禁止出现'某种程度上'\"）。" +
		"保存后所有子代理每章都会在 working_memory.user_rules 看到，writer 据此写作、commit_chapter 据此自检，跨重启生效。" +
		"text 必填，原样转述用户的要求即可，结构化提炼由系统完成。" +
		"返回本次理解到的结构化约束与当前全量生效约束——请把它回显给用户确认是否理解正确。" +
		"只存\"任何时候都成立\"的写作风格/质量规则；剧情/结构/人物走向、阶段篇幅调整（如\"增加10章\"\"这一卷多写战斗\"）走 architect，已写章节返工走 editor，都不要存这里。"
}

// 写工具，禁止并发。
func (t *SaveUserRulesTool) ReadOnly(_ json.RawMessage) bool        { return false }
func (t *SaveUserRulesTool) ConcurrencySafe(_ json.RawMessage) bool { return false }

func (t *SaveUserRulesTool) ActivityDescription(_ json.RawMessage) string {
	return "保存写作规则"
}

func (t *SaveUserRulesTool) Schema() map[string]any {
	return schema.Object(
		schema.Property("text", schema.String("用户的长效写作要求（原样转述，可适当凝练），系统会归一化为结构化约束")).Required(),
	)
}

func (t *SaveUserRulesTool) Execute(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
	var a struct {
		Text string `json:"text"`
	}
	if err := unmarshalToolArgs(args, &a); err != nil {
		return nil, fmt.Errorf("invalid args: %w: %w", errs.ErrToolArgs, err)
	}
	text := strings.TrimSpace(a.Text)
	if text == "" {
		return nil, fmt.Errorf("text 不能为空: %w", errs.ErrToolArgs)
	}
	if err := guardPipelineGlobalPlanningExecution(t.store, t.Name()); err != nil {
		return nil, err
	}

	// 归一化失败只会把该条降级为 raw preferences（不报错）；只有落盘失败才返回 tool error。
	snap, cand, err := t.svc.AddRuntimeRule(ctx, text)
	if err != nil {
		return nil, fmt.Errorf("保存写作规则失败: %w", err)
	}

	return json.Marshal(map[string]any{
		"saved":      true,
		"status":     snap.Status,
		"understood": userRuleUnderstanding(cand), // 本次理解，供回显确认
		"in_effect":  snap.Payload(),              // 当前全量生效约束
	})
}

// userRuleUnderstanding 把本次归一化候选转成给 LLM 的事实视图——
// Coordinator 据此向用户回显"我把你这句理解成了什么"，便于及时纠偏。
func userRuleUnderstanding(c rules.Candidate) map[string]any {
	m := map[string]any{"degraded": c.Degraded}
	if !c.Structured.IsEmpty() {
		m["structured"] = c.Structured
	}
	if p := strings.TrimSpace(c.Preferences); p != "" {
		m["preferences"] = p
	}
	if len(c.Uncertain) > 0 {
		m["uncertain"] = c.Uncertain // 故意未提升为硬性检查的项 + 原因
	}
	return m
}
