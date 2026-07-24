package tools

import (
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func TestBuildInitialWorldTickDispatchContractPrefersLayeredOutline(t *testing.T) {
	st := newInitialWorldTickDispatchTestStore(t)
	if err := st.Outline.SaveOutline([]domain.OutlineEntry{{
		Chapter:   1,
		CoreEvent: "过期扁平事件",
		Hook:      "过期扁平钩子",
	}}); err != nil {
		t.Fatal(err)
	}
	const layeredCore = "林照微在公开风险说明会上发现委托方另有保留条件，并选择当场追问。"
	const layeredHook = "许珩单方冻结委托四十八小时，等待林照微重新选择。"
	if err := st.Outline.SaveLayeredOutline([]domain.VolumeOutline{{
		Index: 1,
		Arcs: []domain.ArcOutline{{
			Index: 1,
			Chapters: []domain.OutlineEntry{{
				Title:     "重逢",
				CoreEvent: layeredCore,
				Hook:      layeredHook,
			}},
		}},
	}}); err != nil {
		t.Fatal(err)
	}

	contract, err := BuildInitialWorldTickDispatchContract(st)
	if err != nil {
		t.Fatalf("build contract: %v", err)
	}
	if contract.CoreEvent != layeredCore || contract.Hook != layeredHook {
		t.Fatalf("layered outline did not win: %+v", contract)
	}
	if strings.Contains(contract.Block, "过期扁平") {
		t.Fatalf("stale flat outline leaked into block: %s", contract.Block)
	}
	for _, want := range []string{
		contract.Marker,
		layeredCore,
		layeredHook,
		InitialWorldTickNoPreemptToken,
		"chapter0 只设置第1章实际发生所需的条件",
		"第1章状态转换必须保持 pending",
		"无需把全部角色塞入 tick",
	} {
		if !strings.Contains(contract.Block, want) {
			t.Fatalf("contract block missing %q:\n%s", want, contract.Block)
		}
	}
	if err := ValidateInitialWorldTickDispatchTask(contract.Block, contract); err != nil {
		t.Fatalf("exact contract block should be a valid dispatch task: %v", err)
	}

	for _, tc := range []struct {
		name  string
		drop  string
		label string
	}{
		{name: "marker", drop: contract.Marker, label: "marker"},
		{name: "core event", drop: layeredCore, label: "exact core_event"},
		{name: "hook", drop: layeredHook, label: "exact hook"},
		{name: "no preempt", drop: InitialWorldTickNoPreemptToken, label: "no-preempt token"},
	} {
		t.Run("rejects task missing "+tc.name, func(t *testing.T) {
			task := strings.Replace(contract.Block, tc.drop, "", 1)
			err := ValidateInitialWorldTickDispatchTask(task, contract)
			if err == nil || !strings.Contains(err.Error(), tc.label) {
				t.Fatalf("missing %s did not fail closed: %v", tc.name, err)
			}
		})
	}

	t.Run("rejects task with anchors but incomplete policy block", func(t *testing.T) {
		const omittedRule = "- 不得新增 exact core_event / exact hook 未授权的证据物、转交、证言、直接接触或场景结果。\n"
		task := strings.Replace(contract.Block, omittedRule, "", 1)
		for _, anchor := range []string{contract.Marker, layeredCore, layeredHook, InitialWorldTickNoPreemptToken} {
			if !strings.Contains(task, anchor) {
				t.Fatalf("fixture accidentally removed required anchor %q", anchor)
			}
		}
		err := ValidateInitialWorldTickDispatchTask(task, contract)
		if err == nil || !strings.Contains(err.Error(), "exact contract block") {
			t.Fatalf("incomplete policy block did not fail closed: %v", err)
		}
	})
}

func TestInitialWorldTickDispatchMarkerDriftsWithExactChapterBoundary(t *testing.T) {
	st := newInitialWorldTickDispatchTestStore(t)
	save := func(coreEvent, hook string) InitialWorldTickDispatchContract {
		t.Helper()
		if err := st.Outline.SaveOutline([]domain.OutlineEntry{{
			Chapter: 1, CoreEvent: coreEvent, Hook: hook,
		}}); err != nil {
			t.Fatal(err)
		}
		contract, err := BuildInitialWorldTickDispatchContract(st)
		if err != nil {
			t.Fatal(err)
		}
		return contract
	}

	base := save("林照微只提交独立风险说明。", "委托继续保持待选择。")
	coreDrift := save("林照微只提交独立风险说明。 ", "委托继续保持待选择。")
	hookDrift := save("林照微只提交独立风险说明。", "委托继续保持待选择。 ")
	if base.Marker == coreDrift.Marker || base.Marker == hookDrift.Marker || coreDrift.Marker == hookDrift.Marker {
		t.Fatalf("exact source drift reused marker: base=%s core=%s hook=%s", base.Marker, coreDrift.Marker, hookDrift.Marker)
	}
	if err := ValidateInitialWorldTickDispatchTask(base.Block, hookDrift); err == nil {
		t.Fatal("stale task unexpectedly validated against drifted chapter boundary")
	}

	tampered := base
	tampered.Marker = hookDrift.Marker
	if err := ValidateInitialWorldTickDispatchTask(tampered.Block, tampered); err == nil || !strings.Contains(err.Error(), "marker drift") {
		t.Fatalf("internally drifted contract did not fail closed: %v", err)
	}
}

func TestBuildInitialWorldTickDispatchContractFailsClosedOnMissingAuthority(t *testing.T) {
	tests := []struct {
		name string
		prep func(t *testing.T, st *store.Store)
		want string
	}{
		{name: "no outline", want: "no authoritative chapter 1"},
		{
			name: "missing core event",
			prep: func(t *testing.T, st *store.Store) {
				t.Helper()
				if err := st.Outline.SaveOutline([]domain.OutlineEntry{{Chapter: 1, Hook: "待选择"}}); err != nil {
					t.Fatal(err)
				}
			},
			want: "core_event is empty",
		},
		{
			name: "missing hook",
			prep: func(t *testing.T, st *store.Store) {
				t.Helper()
				if err := st.Outline.SaveOutline([]domain.OutlineEntry{{Chapter: 1, CoreEvent: "公开说明"}}); err != nil {
					t.Fatal(err)
				}
			},
			want: "hook is empty",
		},
		{
			name: "present layered skeleton cannot fall back to flat",
			prep: func(t *testing.T, st *store.Store) {
				t.Helper()
				if err := st.Outline.SaveOutline([]domain.OutlineEntry{{Chapter: 1, CoreEvent: "旧事件", Hook: "旧钩子"}}); err != nil {
					t.Fatal(err)
				}
				if err := st.Outline.SaveLayeredOutline([]domain.VolumeOutline{{
					Index: 1,
					Arcs: []domain.ArcOutline{{
						Index: 1, EstimatedChapters: 12,
					}},
				}}); err != nil {
					t.Fatal(err)
				}
			},
			want: "authoritative layered_outline has no expanded chapter 1",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st := newInitialWorldTickDispatchTestStore(t)
			if tc.prep != nil {
				tc.prep(t, st)
			}
			_, err := BuildInitialWorldTickDispatchContract(st)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err=%v, want %q", err, tc.want)
			}
		})
	}

	if _, err := BuildInitialWorldTickDispatchContract(nil); err == nil {
		t.Fatal("nil store did not fail closed")
	}
}

func newInitialWorldTickDispatchTestStore(t *testing.T) *store.Store {
	t.Helper()
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	return st
}
