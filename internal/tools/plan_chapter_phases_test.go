package tools

import (
	"context"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func newPhaseTestStore(t *testing.T) *store.Store {
	t.Helper()
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := st.Progress.Init("test", 3); err != nil {
		t.Fatalf("Progress.Init: %v", err)
	}
	return st
}

func planStructureArgs(chapter int) json.RawMessage {
	b, _ := json.Marshal(map[string]any{
		"chapter":  chapter,
		"title":    "测试章",
		"goal":     "推进剧情",
		"conflict": "外部阻力",
		"hook":     "留下悬念",
	})
	return b
}

func enableFemaleIdentityGuard(t *testing.T, st *store.Store) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(st.Dir(), "premise.md"), []byte("女频女性职场成长文，主角许闻溪。"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := st.Characters.Save([]domain.Character{
		{Name: "许闻溪", Role: "主角", Tier: "core"},
		{Name: "梁渡", Role: "重要配角 / 感情线男主", Tier: "core"},
	}); err != nil {
		t.Fatal(err)
	}
}

func TestPlanStructureRejectsGenericIdentityAnchors(t *testing.T) {
	st := newPhaseTestStore(t)
	enableFemaleIdentityGuard(t, st)
	if _, err := NewPlanStructureTool(st).Execute(context.Background(), planStructureArgs(1)); err == nil || !strings.Contains(err.Error(), "身份锚点") {
		t.Fatalf("expected identity anchor rejection, got %v", err)
	}

	args, _ := json.Marshal(map[string]any{
		"chapter":  1,
		"title":    "第一章",
		"goal":     "让许闻溪在发布会后台看见溪流助手复制自己的劳动。",
		"conflict": "许闻溪想按流程撑完发布会，傅行简推动岗位合并建议上屏。",
		"hook":     "会后确认单推送到许闻溪工位，确认栏已经待签。",
	})
	if _, err := NewPlanStructureTool(st).Execute(context.Background(), args); err != nil {
		t.Fatalf("named plan_structure should pass identity guard: %v", err)
	}
}

func TestPlanDetailsRejectsGenericCharacterPlaceholder(t *testing.T) {
	st := newPhaseTestStore(t)
	enableFemaleIdentityGuard(t, st)
	args, _ := json.Marshal(map[string]any{
		"chapter":  1,
		"title":    "第一章",
		"goal":     "让许闻溪在发布会后台看见溪流助手复制自己的劳动。",
		"conflict": "许闻溪想按流程撑完发布会，傅行简推动岗位合并建议上屏。",
		"hook":     "会后确认单推送到许闻溪工位，确认栏已经待签。",
	})
	if _, err := NewPlanStructureTool(st).Execute(context.Background(), args); err != nil {
		t.Fatalf("plan_structure: %v", err)
	}
	details, _ := json.Marshal(map[string]any{
		"chapter": 1,
		"causal_simulation": map[string]any{
			"initial_state": []map[string]any{{
				"character":     "主角",
				"current_goal":  "稳住发布会。",
				"pressure":      "岗位合并建议上屏。",
				"likely_action": "先按流程确认。",
			}},
		},
	})
	if _, err := NewPlanDetailsTool(st).Execute(context.Background(), details); err == nil || !strings.Contains(err.Error(), "模板占位") {
		t.Fatalf("expected placeholder rejection, got %v", err)
	}
	partial, _ := st.Drafts.LoadChapterPlanPartial(1)
	raw, _ := json.Marshal(partial)
	if strings.Contains(string(raw), `"character":"主角"`) {
		t.Fatalf("rejected placeholder must not persist: %s", raw)
	}
}

func TestPlanDetailsRejectsEmptyPatch(t *testing.T) {
	st := newPhaseTestStore(t)
	if _, err := NewPlanStructureTool(st).Execute(context.Background(), planStructureArgs(1)); err != nil {
		t.Fatalf("plan_structure: %v", err)
	}
	args, _ := json.Marshal(map[string]any{"chapter": 1})
	_, err := NewPlanDetailsTool(st).Execute(context.Background(), args)
	if err == nil || !strings.Contains(err.Error(), "空提交无效") {
		t.Fatalf("expected empty patch rejection, got %v", err)
	}
}

func TestPlanDetailsMergesCharacterArraysByName(t *testing.T) {
	st := newPhaseTestStore(t)
	if _, err := NewPlanStructureTool(st).Execute(context.Background(), planStructureArgs(1)); err != nil {
		t.Fatalf("plan_structure: %v", err)
	}
	first, _ := json.Marshal(map[string]any{
		"chapter": 1,
		"causal_simulation": map[string]any{
			"initial_state": []map[string]any{{
				"character":    "许闻溪",
				"current_goal": "保住发布会口径",
				"pressure":     "倒计时",
			}},
		},
	})
	if _, err := NewPlanDetailsTool(st).Execute(context.Background(), first); err != nil {
		t.Fatalf("plan_details first: %v", err)
	}
	second, _ := json.Marshal(map[string]any{
		"chapter": 1,
		"causal_simulation": map[string]any{
			"initial_state": []map[string]any{{
				"character":       "梁渡",
				"action_tendency": "先留日志",
			}},
		},
	})
	if _, err := NewPlanDetailsTool(st).Execute(context.Background(), second); err != nil {
		t.Fatalf("plan_details second: %v", err)
	}
	partial, _ := st.Drafts.LoadChapterPlanPartial(1)
	sim := partial["causal_simulation"].(map[string]any)
	states := sim["initial_state"].([]any)
	if len(states) != 2 {
		t.Fatalf("expected merged states for both characters, got %#v", states)
	}
	raw, _ := json.Marshal(states)
	if !strings.Contains(string(raw), "许闻溪") || !strings.Contains(string(raw), "梁渡") {
		t.Fatalf("merged states lost a character: %s", raw)
	}
}

// TestPlanPhasesTwoStageEqualsSingleShot 两阶段（structure + 分批 details + finalize）
// 与单发 plan_chapter 产物一致：plan 文件落盘、章节 in_progress、中间态清理。
func TestPlanPhasesTwoStageEqualsSingleShot(t *testing.T) {
	st := newPhaseTestStore(t)
	structureTool := NewPlanStructureTool(st)
	detailsTool := NewPlanDetailsTool(st)

	if _, err := structureTool.Execute(context.Background(), planStructureArgs(1)); err != nil {
		t.Fatalf("plan_structure: %v", err)
	}
	if partial, err := st.Drafts.LoadChapterPlanPartial(1); err != nil || partial == nil {
		t.Fatalf("expected partial saved, got partial=%v err=%v", partial, err)
	}

	// 把完整 causal_simulation 拆两批提交
	sim := testCausalSimulation(false)
	keys := sortedKeys(sim)
	batch1 := map[string]any{}
	batch2 := map[string]any{}
	for i, k := range keys {
		if i%2 == 0 {
			batch1[k] = sim[k]
		} else {
			batch2[k] = sim[k]
		}
	}
	args1, _ := json.Marshal(map[string]any{"chapter": 1, "causal_simulation": batch1})
	raw, err := detailsTool.Execute(context.Background(), args1)
	if err != nil {
		t.Fatalf("plan_details batch1: %v", err)
	}
	var staged struct {
		Staged        string   `json:"staged"`
		FieldsPresent []string `json:"fields_present"`
	}
	if err := json.Unmarshal(raw, &staged); err != nil || staged.Staged != "details" {
		t.Fatalf("expected staged details, got %s err=%v", raw, err)
	}
	if len(staged.FieldsPresent) != len(batch1) {
		t.Fatalf("expected %d fields present, got %d", len(batch1), len(staged.FieldsPresent))
	}

	args2, _ := json.Marshal(map[string]any{"chapter": 1, "causal_simulation": batch2, "finalize": true})
	raw, err = detailsTool.Execute(context.Background(), args2)
	if err != nil {
		t.Fatalf("plan_details finalize: %v", err)
	}
	var final struct {
		Planned bool `json:"planned"`
	}
	if err := json.Unmarshal(raw, &final); err != nil || !final.Planned {
		t.Fatalf("expected planned=true, got %s err=%v", raw, err)
	}

	plan, err := st.Drafts.LoadChapterPlan(1)
	if err != nil || plan == nil {
		t.Fatalf("expected final plan saved, got plan=%v err=%v", plan, err)
	}
	if plan.Title != "测试章" || plan.Hook != "留下悬念" {
		t.Fatalf("structure fields lost in merge: %+v", plan)
	}
	if len(plan.CausalSimulation.ContextSources) == 0 {
		t.Fatalf("causal_simulation lost in merge")
	}
	if partial, _ := st.Drafts.LoadChapterPlanPartial(1); partial != nil {
		t.Fatalf("expected partial cleaned after finalize")
	}
	progress, _ := st.Progress.Load()
	if progress == nil || progress.InProgressChapter != 1 {
		t.Fatalf("expected chapter 1 in progress, got %+v", progress)
	}
}

// TestPlanDetailsAutoFinalizesCompletePlanWithoutFlag 防止模型已提交完整 details
// 却忘记 finalize=true 时停在 partial。
func TestPlanDetailsAutoFinalizesCompletePlanWithoutFlag(t *testing.T) {
	st := newPhaseTestStore(t)
	if _, err := NewPlanStructureTool(st).Execute(context.Background(), planStructureArgs(1)); err != nil {
		t.Fatalf("plan_structure: %v", err)
	}
	args, _ := json.Marshal(map[string]any{"chapter": 1, "causal_simulation": testCausalSimulation(false)})
	raw, err := NewPlanDetailsTool(st).Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("plan_details auto finalize: %v", err)
	}
	var final struct {
		Planned bool `json:"planned"`
	}
	if err := json.Unmarshal(raw, &final); err != nil || !final.Planned {
		t.Fatalf("expected planned=true, got %s err=%v", raw, err)
	}
	if plan, err := st.Drafts.LoadChapterPlan(1); err != nil || plan == nil {
		t.Fatalf("expected final plan saved, got plan=%v err=%v", plan, err)
	}
	if partial, _ := st.Drafts.LoadChapterPlanPartial(1); partial != nil {
		t.Fatalf("expected partial cleaned after auto finalize")
	}
}

// TestPlanDetailsFinalizeIncompleteListsMissing finalize 时字段不全必须报缺失且不落正式 plan。
func TestPlanDetailsFinalizeIncompleteListsMissing(t *testing.T) {
	st := newPhaseTestStore(t)
	if _, err := NewPlanStructureTool(st).Execute(context.Background(), planStructureArgs(1)); err != nil {
		t.Fatalf("plan_structure: %v", err)
	}
	sim := testCausalSimulation(false)
	partialSim := map[string]any{}
	maps.Copy(partialSim, sim)
	delete(partialSim, "initial_state")
	args, _ := json.Marshal(map[string]any{"chapter": 1, "causal_simulation": partialSim, "finalize": true})
	_, err := NewPlanDetailsTool(st).Execute(context.Background(), args)
	if err == nil || !strings.Contains(err.Error(), "initial_state") {
		t.Fatalf("expected missing initial_state error, got %v", err)
	}
	if plan, _ := st.Drafts.LoadChapterPlan(1); plan != nil {
		t.Fatalf("incomplete finalize must not persist final plan")
	}
	// 中间态保留，补批后可重试
	if partial, _ := st.Drafts.LoadChapterPlanPartial(1); partial == nil {
		t.Fatalf("partial must survive failed finalize")
	}
}

// TestPlanDetailsWithoutStructureRejected 未建中间态直接 plan_details 必须报错。
func TestPlanDetailsWithoutStructureRejected(t *testing.T) {
	st := newPhaseTestStore(t)
	args, _ := json.Marshal(map[string]any{"chapter": 1, "causal_simulation": map[string]any{}})
	_, err := NewPlanDetailsTool(st).Execute(context.Background(), args)
	if err == nil || !strings.Contains(err.Error(), "plan_structure") {
		t.Fatalf("expected precondition error mentioning plan_structure, got %v", err)
	}
}

// TestPlanStructureRejectsMissingCore 缺核心字段直接拒绝。
func TestPlanStructureRejectsMissingCore(t *testing.T) {
	st := newPhaseTestStore(t)
	b, _ := json.Marshal(map[string]any{"chapter": 1, "title": "只有标题"})
	_, err := NewPlanStructureTool(st).Execute(context.Background(), b)
	if err == nil || !strings.Contains(err.Error(), "goal") {
		t.Fatalf("expected missing goal error, got %v", err)
	}
}

func TestPlanDetailsRejectsSecondAlgorithmCrossProjectContamination(t *testing.T) {
	st := newPhaseTestStore(t)
	if err := st.Characters.Save([]domain.Character{{Name: "许闻溪", Role: "主角", Tier: "core"}}); err != nil {
		t.Fatalf("SaveCharacters: %v", err)
	}
	if _, err := NewPlanStructureTool(st).Execute(context.Background(), planStructureArgs(1)); err != nil {
		t.Fatalf("plan_structure: %v", err)
	}
	args, _ := json.Marshal(map[string]any{
		"chapter": 1,
		"causal_simulation": map[string]any{
			"chapter_function": "江烬收到欠费单，进入鬼城规则。",
		},
	})
	_, err := NewPlanDetailsTool(st).Execute(context.Background(), args)
	if err == nil || !strings.Contains(err.Error(), "跨项目污染") {
		t.Fatalf("expected cross-project contamination rejection, got %v", err)
	}
	partial, _ := st.Drafts.LoadChapterPlanPartial(1)
	raw, _ := json.Marshal(partial)
	if strings.Contains(string(raw), "江烬") || strings.Contains(string(raw), "欠费单") {
		t.Fatalf("contaminated details must not persist: %s", raw)
	}
}
