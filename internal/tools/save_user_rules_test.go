package tools

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/errs"
	"github.com/chenhongyang/novel-studio/internal/rules"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/userrules"
)

// nil 模型 → 归一化降级；空 LoadOptions → 不读真实磁盘。
func newDegradedTool(t *testing.T) (*SaveUserRulesTool, *store.Store) {
	t.Helper()
	st := store.NewStore(t.TempDir())
	svc := userrules.NewService(st, nil, rules.LoadOptions{})
	return NewSaveUserRulesTool(svc, st), st
}

// 核心契约：归一化失败（技术细节）绝不抛回 Coordinator，只降级 + 返回事实 + 落盘。
func TestSaveUserRulesTool_DegradeReturnsFactsNotError(t *testing.T) {
	tool, st := newDegradedTool(t)

	out, err := tool.Execute(t.Context(), json.RawMessage(`{"text":"每章1500字，少用比喻"}`))
	if err != nil {
		t.Fatalf("归一化降级不应作为 tool error 抛出：%v", err)
	}

	var res struct {
		Saved      bool   `json:"saved"`
		Status     string `json:"status"`
		Understood struct {
			Degraded    bool   `json:"degraded"`
			Preferences string `json:"preferences"`
		} `json:"understood"`
		InEffect map[string]any `json:"in_effect"`
	}
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("结果应为合法 JSON：%v", err)
	}
	if !res.Saved {
		t.Fatal("saved 应为 true")
	}
	if res.Status != string(rules.StatusDegraded) {
		t.Fatalf("无模型应 degraded，got %q", res.Status)
	}
	if !res.Understood.Degraded || res.Understood.Preferences != "每章1500字，少用比喻" {
		t.Fatalf("回显应含降级标记与原文，got %+v", res.Understood)
	}
	if res.InEffect == nil {
		t.Fatal("应返回当前全量生效约束供回显")
	}
	// 已落盘。
	if cur, _ := st.UserRules.Load(); cur == nil {
		t.Fatal("规则应已持久化")
	}
}

func TestSaveUserRulesTool_EmptyTextErrors(t *testing.T) {
	tool, _ := newDegradedTool(t)
	if _, err := tool.Execute(t.Context(), json.RawMessage(`{"text":"  "}`)); !errors.Is(err, errs.ErrToolArgs) {
		t.Fatalf("空 text 应返回 ErrToolArgs，got %v", err)
	}
}
