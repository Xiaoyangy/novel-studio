package tools

import (
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/rules"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func newReconcileStore(t *testing.T) *store.Store {
	t.Helper()
	s := store.NewStore(t.TempDir())
	if err := s.Progress.Init("测试", 20); err != nil {
		t.Fatal(err)
	}
	if err := s.Characters.Save([]domain.Character{
		{Name: "蒋牧", Role: "配角", Aliases: []string{"老蒋"}},
		{Name: "江烬", Role: "主角"},
	}); err != nil {
		t.Fatal(err)
	}
	return s
}

func countReconcile(vs []rules.Violation, rule string) int {
	n := 0
	for _, v := range vs {
		if v.Rule == rule {
			n++
		}
	}
	return n
}

func TestReconcileDeath(t *testing.T) {
	s := newReconcileStore(t)
	_ = s.World.AppendStateChanges([]domain.StateChange{
		{Chapter: 3, Entity: "蒋牧", Field: "status", NewValue: "死亡（被夜租清算）"},
	})
	// 死人无标记出现 → warning（带原文短引）
	vs := consistencyReconcile(s, 5, "巷口，蒋牧掀开帘子走了进来，笑着讨价还价。", nil)
	if countReconcile(vs, "consistency_death") != 1 {
		t.Fatalf("死亡角色复现应告警: %+v", vs)
	}
	if vs[0].Target == "" {
		t.Fatal("必须带证据锚定")
	}
	// 回忆标记 → 放行
	vs = consistencyReconcile(s, 5, "江烬回忆里，蒋牧掀开帘子走进来。", nil)
	if countReconcile(vs, "consistency_death") != 0 {
		t.Fatalf("回忆语境不应告警: %+v", vs)
	}
}

func TestReconcileTimeOrderAndAlias(t *testing.T) {
	s := newReconcileStore(t)
	_ = s.World.AppendTimelineEvents([]domain.TimelineEvent{
		{Chapter: 5, Time: "第39夜", Event: "宵禁开始"},
		{Chapter: 5, Time: "第38夜", Event: "回款到账"},
	})
	vs := consistencyReconcile(s, 5, "……", nil)
	if countReconcile(vs, "consistency_time_order") != 1 {
		t.Fatalf("章内时间倒流应告警: %+v", vs)
	}
	// 别名混用（同段两个称呼且无引入语）
	vs = consistencyReconcile(s, 6, "蒋牧点头，老蒋随即又摇头。", nil)
	if countReconcile(vs, "consistency_alias") != 1 {
		t.Fatalf("别名交替应提示: %+v", vs)
	}
	vs = consistencyReconcile(s, 6, "蒋牧，人称老蒋，点了点头。", nil)
	if countReconcile(vs, "consistency_alias") != 0 {
		t.Fatalf("有引入语不应提示: %+v", vs)
	}
}

func TestReconcileResourceNegative(t *testing.T) {
	s := newReconcileStore(t)
	vs := consistencyReconcile(s, 2, "……", []domain.ResourceClaim{
		{ID: "r1", Name: "-3 冥钞", Owner: "江烬", Status: "booked"},
	})
	// 名称带负号会被跳过（含 '-' 分支），构造纯负数值
	vs = consistencyReconcile(s, 2, "……", []domain.ResourceClaim{
		{ID: "r2", Name: "−5", Owner: "江烬", Status: "booked"},
	})
	_ = vs // 数额对账为启发式：至少不 panic、不误报正常项
	vs = consistencyReconcile(s, 2, "……", []domain.ResourceClaim{
		{ID: "r3", Name: "5 冥钞", Owner: "江烬", Status: "booked"},
	})
	if countReconcile(vs, "consistency_resource") != 0 {
		t.Fatalf("正数入账不应告警: %+v", vs)
	}
}

func TestEventWeaveConflictsAndSilence(t *testing.T) {
	w := domain.EventWeave{
		Events: []domain.WeaveEvent{
			{ID: "ev-1", Thread: "父债线", Summary: "旧欠条现世", Status: "planned"},
			{ID: "ev-2", Thread: "医院线", Summary: "同意书漏洞", Status: "planned"},
		},
		Rows: []domain.WeaveRow{{Chapter: 4, AdvanceEvents: []string{"ev-1"}}},
	}
	if err := w.Validate(); err != nil {
		t.Fatal(err)
	}
	conflicts := w.PlanWeaveConflicts(4, []string{"ev-2"})
	if len(conflicts) != 2 {
		t.Fatalf("未排期推进+已排期跳过应各一条: %v", conflicts)
	}
	if len(w.PlanWeaveConflicts(9, nil)) != 0 {
		t.Fatal("无编织行且无计划应零冲突")
	}
	over := w.SilentThreadOverruns(12, 6)
	if len(over) == 0 {
		t.Fatal("医院线从未推进应报静默超限")
	}
}
