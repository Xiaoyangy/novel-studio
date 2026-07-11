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
	"github.com/chenhongyang/novel-studio/internal/rules"
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

func TestPlanStructureNormalizesMaleProjectOutlineAnchors(t *testing.T) {
	st := newPhaseTestStore(t)
	if err := st.Characters.Save([]domain.Character{
		{Name: "林澈", Role: "主角", Tier: "core"},
		{Name: "沈知遥", Role: "女主", Tier: "core"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.Outline.SaveOutline([]domain.OutlineEntry{{
		Chapter: 1, Title: "失业饭桌", CoreEvent: "林澈返乡饭桌受挤兑", Hook: "手机弹出县城花钱系统绑定提示",
	}}); err != nil {
		t.Fatal(err)
	}

	if _, err := NewPlanStructureTool(st).Execute(context.Background(), planStructureArgs(1)); err != nil {
		t.Fatalf("outline anchors should normalize a generic structure: %v", err)
	}
	anchored, err := st.Drafts.LoadChapterPlanPartial(1)
	if err != nil || anchored == nil {
		t.Fatalf("LoadChapterPlanPartial: partial=%v err=%v", anchored, err)
	}
	if got := anchored["structure"].(map[string]any)["title"]; got != "失业饭桌" {
		t.Fatalf("outline title should be injected before validation, got %#v", got)
	}

	args, _ := json.Marshal(map[string]any{
		"chapter":  1,
		"title":    "失业饭桌",
		"goal":     "林澈在返乡饭桌护住父母体面，并直面失业压力。",
		"conflict": "林澈想用玩笑挡住亲戚挤兑，现实账单却让他无法轻松脱身。",
		"hook":     "错误的章末钩子",
	})
	if _, err := NewPlanStructureTool(st).Execute(context.Background(), args); err != nil {
		t.Fatalf("grounded male project plan should pass: %v", err)
	}
	partial, err := st.Drafts.LoadChapterPlanPartial(1)
	if err != nil || partial == nil {
		t.Fatalf("LoadChapterPlanPartial: partial=%v err=%v", partial, err)
	}
	structure := partial["structure"].(map[string]any)
	if got := structure["goal"]; got != "完整兑现本章大纲核心事件：林澈返乡饭桌受挤兑" {
		t.Fatalf("outline goal must pin chapter scope, got %#v", got)
	}
	if got := structure["hook"]; got != "手机弹出县城花钱系统绑定提示" {
		t.Fatalf("outline hook must pin chapter boundary, got %#v", got)
	}
	if got := structure["title"]; got != "失业饭桌" {
		t.Fatalf("outline title must pin chapter name, got %#v", got)
	}
	if required := stringSliceFromAny(structure["required_beats"]); len(required) != 0 {
		t.Fatalf("outline goal/hook must not be duplicated into prose checklist: %#v", required)
	}
}

func TestIdentityGuardUsesExplicitProtagonistGenderAndRecentCast(t *testing.T) {
	st := newPhaseTestStore(t)
	if err := os.WriteFile(filepath.Join(st.Dir(), "premise.md"), []byte("男频单女主小说，男主林澈，女主沈知遥。"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := st.Characters.Save([]domain.Character{
		{Name: "林澈", Role: "主角", Tier: "core"},
		{Name: "沈知遥", Role: "女主", Tier: "core"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.Cast.Save([]domain.CastEntry{
		{Name: "赵航", LastSeenChapter: 1, AppearanceCount: 1},
		{Name: "老丁", LastSeenChapter: 1, AppearanceCount: 1},
	}); err != nil {
		t.Fatal(err)
	}
	if projectHasFemaleProtagonist(st) {
		t.Fatal("单女主只描述感情线配置，不能把男主识别为女性主角")
	}
	names := knownCharacterNameSet(st)
	for _, name := range []string{"林澈", "沈知遥", "赵航", "老丁"} {
		if _, ok := names[name]; !ok {
			t.Fatalf("expected %s in project identity set: %+v", name, names)
		}
	}
}

func TestPlanIdentityAllowsRequestedCompanionSystemWithoutAddingItToCast(t *testing.T) {
	st := newPhaseTestStore(t)
	if err := st.Characters.Save([]domain.Character{{Name: "林澈", Role: "主角", Tier: "core"}}); err != nil {
		t.Fatal(err)
	}
	if err := st.UserRules.Save(&rules.Snapshot{
		Structured:  rules.Structured{Genre: "都市脑洞轻松搞笑爽文"},
		Preferences: "系统会和男主交流解闷，不是一个纯下达任务的机器人，并且始终支持主角。",
	}); err != nil {
		t.Fatal(err)
	}
	payload := map[string]any{
		"title": "第一章",
		"causal_simulation": map[string]any{
			"voice_logic": []any{
				map[string]any{"character": "林澈", "surface_voice": "嘴上镇定"},
				map[string]any{"character": "系统", "surface_voice": "短促接话"},
			},
		},
	}
	issues := ChapterPlanIdentityIssues(st, 1, payload)
	if joined := strings.Join(issues, "\n"); strings.Contains(joined, `character="系统"`) {
		t.Fatalf("requested companion system should be a valid speaking entity: %v", issues)
	}
	if _, ok := knownCharacterNameSet(st)["系统"]; ok {
		t.Fatal("system must not be persisted as a human cast identity")
	}
}

func TestPlanIdentityRejectsUnrequestedSystemEntity(t *testing.T) {
	st := newPhaseTestStore(t)
	if err := st.Characters.Save([]domain.Character{{Name: "林澈", Role: "主角", Tier: "core"}}); err != nil {
		t.Fatal(err)
	}
	payload := map[string]any{
		"title": "第一章",
		"causal_simulation": map[string]any{
			"voice_logic": []any{
				map[string]any{"character": "林澈", "surface_voice": "正常说话"},
				map[string]any{"character": "系统", "surface_voice": "发任务"},
			},
		},
	}
	issues := ChapterPlanIdentityIssues(st, 1, payload)
	if joined := strings.Join(issues, "\n"); !strings.Contains(joined, `character="系统"`) {
		t.Fatalf("unrequested system entity must still be rejected: %v", issues)
	}
}

func TestRewritePlanRejectsVisibleCharacterOutsideCurrentOutline(t *testing.T) {
	st := newPhaseTestStore(t)
	if err := st.Characters.Save([]domain.Character{
		{Name: "林澈", Role: "主角", Tier: "core"},
		{Name: "林建国", Role: "主角父亲", Tier: "important"},
		{Name: "周曼", Role: "主角母亲", Tier: "important"},
		{Name: "沈知遥", Role: "女主", Tier: "core"},
		{Name: "马玉芬", Role: "商户代表", Tier: "secondary"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.Outline.SaveOutline([]domain.OutlineEntry{{
		Chapter:   1,
		Title:     "失业饭桌",
		CoreEvent: "林澈在饭桌被亲戚阴阳失业，父母嘴硬护短。",
		Hook:      "手机弹出系统绑定提示",
	}}); err != nil {
		t.Fatal(err)
	}
	progress, _ := st.Progress.Load()
	progress.CompletedChapters = []int{1}
	progress.PendingRewrites = []int{1}
	if err := st.Progress.Save(progress); err != nil {
		t.Fatal(err)
	}
	payload := map[string]any{
		"structure": map[string]any{
			"chapter":  1,
			"title":    "失业饭桌",
			"goal":     "林澈在饭桌承认失业",
			"conflict": "父母想护住林澈的面子",
			"hook":     "手机弹出系统绑定提示",
		},
		"causal_simulation": map[string]any{
			"offscreen_character_stage": []any{
				map[string]any{"character": "林澈", "visible_in_chapter": true},
				map[string]any{"character": "林建国", "visible_in_chapter": true},
				map[string]any{"character": "周曼", "visible_in_chapter": true},
				map[string]any{"character": "沈知遥", "visible_in_chapter": false},
				map[string]any{"character": "马玉芬", "visible_in_chapter": true},
			},
		},
	}
	issues := ChapterPlanIdentityIssues(st, 1, payload)
	joined := strings.Join(issues, "\n")
	if !strings.Contains(joined, "马玉芬") || !strings.Contains(joined, "未授权该角色出场") {
		t.Fatalf("expected out-of-outline visible character issue, got %v", issues)
	}
	for _, allowed := range []string{"林澈", "林建国", "周曼", "沈知遥"} {
		if strings.Contains(joined, "将 "+allowed+" 标为本章可见") {
			t.Fatalf("allowed/offscreen character %s was rejected: %v", allowed, issues)
		}
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

func TestPlanDetailsAppendsSourceAndReviewArrays(t *testing.T) {
	merged := map[string]any{
		"context_sources": []any{"world_foundation"},
		"review_refinement": map[string]any{
			"trigger_sources":      []any{"reviews/01.md"},
			"preserve_constraints": []any{"保留付款事实"},
		},
	}
	mergeCausalSimulationPatch(merged, map[string]any{
		"context_sources": []any{"rewrite_source:sha256:test", "world_foundation"},
		"review_refinement": map[string]any{
			"trigger_sources":      []any{"rewrite_brief"},
			"preserve_constraints": []any{"保留章末钩子"},
		},
	})
	raw, _ := json.Marshal(merged)
	for _, want := range []string{"world_foundation", "rewrite_source:sha256:test", "reviews/01.md", "rewrite_brief", "保留付款事实", "保留章末钩子"} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("merged patch lost %q: %s", want, raw)
		}
	}
	if strings.Count(string(raw), "world_foundation") != 1 {
		t.Fatalf("source arrays should deduplicate: %s", raw)
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

func TestPlanningFallbackPrefersPendingRewriteTarget(t *testing.T) {
	st := newPhaseTestStore(t)
	if err := st.Progress.MarkChapterComplete(1, 2000, "", ""); err != nil {
		t.Fatalf("MarkChapterComplete: %v", err)
	}
	if err := st.Progress.SetPendingRewrites([]int{1}, "rewrite"); err != nil {
		t.Fatalf("SetPendingRewrites: %v", err)
	}
	if got := inProgressChapterOf(st); got != 1 {
		t.Fatalf("planning fallback must prefer pending rewrite chapter, got %d", got)
	}
	if got := NewPlanDetailsTool(st).inProgressChapter(); got != 1 {
		t.Fatalf("plan_details fallback must prefer pending rewrite chapter, got %d", got)
	}
}

func TestPlanDetailsRejectsSecondAlgorithmCrossProjectContamination(t *testing.T) {
	st := newPhaseTestStore(t)
	if err := st.Characters.Save([]domain.Character{{Name: "许闻溪", Role: "主角", Tier: "core"}}); err != nil {
		t.Fatalf("SaveCharacters: %v", err)
	}
	structure, _ := json.Marshal(map[string]any{
		"chapter":  1,
		"title":    "测试章",
		"goal":     "许闻溪核对发布会资料。",
		"conflict": "许闻溪必须在时间压力下保留证据。",
		"hook":     "许闻溪发现确认单仍待签。",
	})
	if _, err := NewPlanStructureTool(st).Execute(context.Background(), structure); err != nil {
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
