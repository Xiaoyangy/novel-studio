package host

import (
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

// newFlagTestHost 造一个最小 Host，只够驱动 cocreating 标记状态机与并发守卫。
// emitEvent 用 recover + 非阻塞 select，缓冲 events 通道即可，无需 coordinator/observer。
// PauseForCoCreate 的运行态分支会调 coordinator.Abort（复用已验证的 Esc 暂停路径），
// 不在此单测；这里只覆盖不依赖 coordinator 的非运行态与标记/守卫逻辑。
func newFlagTestHost(lc lifecycle, cocreating bool) *Host {
	return &Host{
		lifecycle:  lc,
		cocreating: cocreating,
		events:     make(chan Event, 16),
	}
}

func TestPauseForCoCreate_NonRunningSetsFlag(t *testing.T) {
	h := newFlagTestHost(lifecycleIdle, false)
	if !h.PauseForCoCreate() {
		t.Fatal("idle 态应允许进入阶段共创")
	}
	if !h.cocreating {
		t.Error("进入后 cocreating 应为 true")
	}
	if h.lifecycle != lifecycleIdle {
		t.Errorf("非运行态进入不应改 lifecycle，得 %s", h.lifecycle)
	}
}

func TestPauseForCoCreate_RejectsCompleted(t *testing.T) {
	h := newFlagTestHost(lifecycleCompleted, false)
	if h.PauseForCoCreate() {
		t.Error("全书完成后不应允许进入阶段共创")
	}
	if h.cocreating {
		t.Error("拒绝后不应置位 cocreating")
	}
}

func TestPauseForCoCreate_RejectsReentrant(t *testing.T) {
	h := newFlagTestHost(lifecyclePaused, true)
	if h.PauseForCoCreate() {
		t.Error("已在共创中应拒绝重入")
	}
}

func TestCancelCoCreate_ClearsFlag(t *testing.T) {
	h := newFlagTestHost(lifecyclePaused, true)
	h.CancelCoCreate()
	if h.cocreating {
		t.Error("取消后 cocreating 应清空")
	}
	if h.lifecycle != lifecyclePaused {
		t.Errorf("取消不应改 lifecycle，得 %s", h.lifecycle)
	}
}

func TestCancelCoCreate_NoopWhenNotCocreating(t *testing.T) {
	h := newFlagTestHost(lifecycleRunning, false)
	h.CancelCoCreate() // 不应 panic，不应改状态
	if h.cocreating || h.lifecycle != lifecycleRunning {
		t.Error("非共创态 CancelCoCreate 应为 no-op")
	}
}

func TestResumeFromCoCreate_RejectsEmptyDraft(t *testing.T) {
	h := newFlagTestHost(lifecyclePaused, true)
	if err := h.ResumeFromCoCreate("   "); err == nil {
		t.Fatal("空 draft 应报错")
	}
	if !h.cocreating {
		t.Error("空 draft 在清标记前返回，cocreating 应保持 true")
	}
}

func TestResumeFromCoCreate_RejectsWhenNotCocreating(t *testing.T) {
	h := newFlagTestHost(lifecyclePaused, false)
	err := h.ResumeFromCoCreate("## 后续走向\n- 进入第二卷")
	if err == nil || !strings.Contains(err.Error(), "not in co-create") {
		t.Fatalf("非共创态应报 not in co-create，得 %v", err)
	}
}

func TestGuardExclusive(t *testing.T) {
	cases := []struct {
		name       string
		lc         lifecycle
		cocreating bool
		wantErr    string // 空=期望放行
	}{
		{"running", lifecycleRunning, false, "运行中"},
		{"cocreating", lifecyclePaused, true, "阶段共创"},
		{"idle free", lifecycleIdle, false, ""},
		{"paused free", lifecyclePaused, false, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newFlagTestHost(c.lc, c.cocreating)
			err := h.guardExclusive("导入")
			if c.wantErr == "" {
				if err != nil {
					t.Fatalf("应放行，得 %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Fatalf("应含 %q，得 %v", c.wantErr, err)
			}
			if !strings.Contains(err.Error(), "导入") {
				t.Errorf("错误文案应带 action %q，得 %v", "导入", err)
			}
		})
	}
}

// TestStageCoCreate_OccupancyBlocksConcurrentEntries 验证共创窗口内独占性入口全部被堵：
// start/resume/continue 在 cocreating 期间都应被拒，补上 paused 期只查 ==running 的缺口。
func TestStageCoCreate_OccupancyBlocksConcurrentEntries(t *testing.T) {
	h := newFlagTestHost(lifecycleIdle, false)
	if !h.PauseForCoCreate() {
		t.Fatal("进入阶段共创失败")
	}

	if err := h.StartPrepared("写个新故事"); err == nil {
		t.Error("共创窗口内 StartPrepared 应被拒")
	}
	if _, err := h.Resume(); err == nil {
		t.Error("共创窗口内 Resume 应被拒")
	}
	if err := h.Continue("继续写"); err == nil {
		t.Error("共创窗口内 Continue 应被拒")
	}

	// 退出共创后占用解除（这里走 Cancel；Resume 注入路径需 coordinator，归集成验证）
	h.CancelCoCreate()
	if h.cocreating {
		t.Fatal("退出后占用标记应解除")
	}
}

func TestEnsureStartProgressInitializedPreservesSimulationRestartProgress(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	want := &domain.Progress{
		NovelName:       "她的第二算法",
		Phase:           domain.PhaseInit,
		TotalChapters:   70,
		Layered:         true,
		CurrentVolume:   1,
		CurrentArc:      1,
		GenerationID:    "simulation-test",
		GenerationMode:  "simulation_restart_from_seed",
		CurrentChapter:  0,
		TotalWordCount:  0,
		CompletedScenes: nil,
	}
	if err := st.Progress.Save(want); err != nil {
		t.Fatal(err)
	}
	h := &Host{store: st}
	if err := h.ensureStartProgressInitialized(); err != nil {
		t.Fatalf("ensureStartProgressInitialized: %v", err)
	}
	got, err := st.Progress.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.GenerationID != want.GenerationID || got.TotalChapters != want.TotalChapters || !got.Layered || got.CurrentVolume != 1 || got.CurrentArc != 1 {
		t.Fatalf("progress should be preserved, got %+v", got)
	}
}

func TestEnsureStartProgressInitializedCreatesMissingProgress(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	h := &Host{store: st}
	if err := h.ensureStartProgressInitialized(); err != nil {
		t.Fatalf("ensureStartProgressInitialized: %v", err)
	}
	got, err := st.Progress.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.Phase != domain.PhaseInit {
		t.Fatalf("expected init progress, got %+v", got)
	}
}

func TestResetStartRuntimeStateClearsQueueAndCheckpoints(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Runtime.AppendQueue(domain.RuntimeQueueItem{Category: "TOOL", Summary: "old loop"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Checkpoints.Append(domain.GlobalScope(), "old", "old", "digest"); err != nil {
		t.Fatal(err)
	}
	h := &Host{store: st}
	if err := h.resetStartRuntimeState(); err != nil {
		t.Fatalf("resetStartRuntimeState: %v", err)
	}
	if items, err := st.Runtime.LoadQueue(); err != nil || len(items) != 0 {
		t.Fatalf("runtime queue should be empty: %+v err=%v", items, err)
	}
	if cps := st.Checkpoints.All(); len(cps) != 0 {
		t.Fatalf("checkpoints should be empty: %+v", cps)
	}
}

func TestResetStartRuntimeStatePreservesCheckpointsWhenConfigured(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Runtime.AppendQueue(domain.RuntimeQueueItem{Category: "TOOL", Summary: "old loop"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Checkpoints.Append(domain.GlobalScope(), "premise", "premise.md", "digest-1"); err != nil {
		t.Fatal(err)
	}
	h := &Host{store: st, preserveCheckpointsOnStart: true}
	if err := h.resetStartRuntimeState(); err != nil {
		t.Fatalf("resetStartRuntimeState: %v", err)
	}
	if items, err := st.Runtime.LoadQueue(); err != nil || len(items) != 0 {
		t.Fatalf("runtime queue should still reset: %+v err=%v", items, err)
	}
	if _, err := st.Checkpoints.Append(domain.GlobalScope(), "characters", "characters.json", "digest-2"); err != nil {
		t.Fatal(err)
	}

	second := store.NewStore(dir)
	if err := second.Init(); err != nil {
		t.Fatal(err)
	}
	if err := (&Host{store: second, preserveCheckpointsOnStart: true}).resetStartRuntimeState(); err != nil {
		t.Fatal(err)
	}
	if _, err := second.Checkpoints.Append(domain.GlobalScope(), "world_rules", "world_rules.json", "digest-3"); err != nil {
		t.Fatal(err)
	}
	cps := store.NewStore(dir).Checkpoints.All()
	if len(cps) != 3 || cps[0].Seq != 1 || cps[1].Seq != 2 || cps[2].Seq != 3 {
		t.Fatalf("checkpoint journal was reset or lost sequence across refresh hosts: %+v", cps)
	}
}

func TestBuildStoryStateSummary_NilStore(t *testing.T) {
	if got := buildStoryStateSummary(nil); got != "" {
		t.Errorf("nil store 应返回空串，得 %q", got)
	}
}

func TestBuildStoryStateSummary_Populated(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init("影之诗", 100); err != nil {
		t.Fatal(err)
	}
	p, _ := st.Progress.Load()
	p.CompletedChapters = []int{1, 2, 3}
	p.TotalWordCount = 12000
	if err := st.Progress.Save(p); err != nil {
		t.Fatal(err)
	}
	if err := st.Outline.SaveCompass(domain.StoryCompass{
		EndingDirection: "主角登临绝巅",
		OpenThreads:     []string{"师门血仇未报"},
		EstimatedScale:  "预计 4-6 卷",
	}); err != nil {
		t.Fatal(err)
	}

	got := buildStoryStateSummary(st)
	for _, want := range []string{"影之诗", "已完成 3 章", "下一章为第 4 章", "主角登临绝巅", "师门血仇未报", "预计 4-6 卷"} {
		if !strings.Contains(got, want) {
			t.Errorf("摘要应含 %q，实际:\n%s", want, got)
		}
	}
}
