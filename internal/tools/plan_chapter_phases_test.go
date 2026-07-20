package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/rag"
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

func TestCharacterIdentityGuardAllowsSystemVoiceOnly(t *testing.T) {
	names := map[string]struct{}{"林澈": {}, "系统": {}}
	voicePlan := map[string]any{"causal_simulation": map[string]any{
		"voice_logic": []any{map[string]any{"character": "县城花钱系统"}},
	}}
	if issues := characterFieldIdentityIssues(voicePlan, names, "林澈", false); len(issues) != 0 {
		t.Fatalf("system companion voice should not require a social character dossier: %v", issues)
	}

	emotionalPlan := map[string]any{"causal_simulation": map[string]any{
		"emotional_logic": []any{map[string]any{"character": "县城花钱系统"}},
	}}
	if issues := characterFieldIdentityIssues(emotionalPlan, names, "林澈", false); len(issues) == 0 {
		t.Fatal("system must remain outside social character and emotional simulation fields")
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
	required := stringSliceFromAny(structure["required_beats"])
	if len(required) != 1 || !strings.Contains(required[0], "林澈返乡饭桌受挤兑") {
		t.Fatalf("formal contract must be pinned to the stable outline event: %#v", required)
	}
}

func TestRewriteStructureKeepsSourceBoundGoalAndHookInsteadOfStaleOutline(t *testing.T) {
	st := newPhaseTestStore(t)
	if err := st.Outline.SaveOutline([]domain.OutlineEntry{{
		Chapter:   1,
		Title:     "稳定章名",
		CoreEvent: "旧大纲写成沈知遥先制止错误走线",
		Hook:      "旧大纲把待确认资源写成即将到位",
	}}); err != nil {
		t.Fatal(err)
	}
	structure := map[string]any{
		"title": "模型临时章名",
		"goal":  "林澈先发现风险、叫停并让老丁完成退线，沈知遥随后只复核自纠结果。",
		"hook":  "贺骁接通电话但尚未答复，皮卡继续保持待确认。",
	}
	applyOutlineAnchorsToStructure(st, 1, structure, true)
	if got := structure["title"]; got != "稳定章名" {
		t.Fatalf("rewrite must still inherit the stable outline title: %#v", got)
	}
	if got := structure["goal"]; got != "林澈先发现风险、叫停并让老丁完成退线，沈知遥随后只复核自纠结果。" {
		t.Fatalf("stale outline overwrote the current rewrite goal: %#v", got)
	}
	if got := structure["hook"]; got != "贺骁接通电话但尚未答复，皮卡继续保持待确认。" {
		t.Fatalf("stale outline overwrote the current rewrite hook: %#v", got)
	}

	applyOutlineAnchorsToStructure(st, 1, structure, false)
	if got := structure["goal"]; got != "完整兑现本章大纲核心事件：旧大纲写成沈知遥先制止错误走线" {
		t.Fatalf("new chapter must remain pinned to outline scope: %#v", got)
	}
	if got := structure["hook"]; got != "旧大纲把待确认资源写成即将到位" {
		t.Fatalf("new chapter must remain pinned to outline hook: %#v", got)
	}
}

func TestFinalPlanGoalFollowsWorldSimulationDecision(t *testing.T) {
	st := newPhaseTestStore(t)
	if err := st.Outline.SaveOutline([]domain.OutlineEntry{{
		Chapter: 1, Title: "旧大纲标题", CoreEvent: "当晚扩到十家", Hook: "打开下一步",
	}}); err != nil {
		t.Fatal(err)
	}
	plan := domain.ChapterPlan{Chapter: 1, CausalSimulation: domain.ChapterCausalSimulation{
		WorldSimulationID:   "ch001-test",
		ProtagonistDecision: "扩到十家后守住十家边界，拒绝第十一摊",
	}}
	applyOutlineAnchorsToPlan(st, &plan, false)
	if want := "落实本轮世界模拟后的主角选择：扩到十家后守住十家边界，拒绝第十一摊"; plan.Goal != want {
		t.Fatalf("goal = %q, want %q", plan.Goal, want)
	}
	if plan.Title != "旧大纲标题" || plan.Hook != "打开下一步" {
		t.Fatalf("title and hook should remain outline anchors: %+v", plan)
	}
}

func TestProjectAllPredecessorStateBecomesContinuityAndForbiddenOnly(t *testing.T) {
	plan := domain.ChapterPlan{Contract: domain.ChapterContract{
		RequiredBeats: []string{"推进现场取证"},
	}}
	marker := "[project-all predecessor-state:out-ch011-rescue-complete] 第11章不可逆前态已完成：警方已经救出许知遥并控制两名嫌疑人；本章只能推进由此前态产生的新后果、证据回看或人物反应，不得把同一状态转移重新安排为当前章现场。"
	applyProjectAllOutlineObligations(&plan, []string{marker})
	if len(plan.Contract.RequiredBeats) != 1 || plan.Contract.RequiredBeats[0] != "推进现场取证" {
		t.Fatalf("predecessor state must not become a required beat: %#v", plan.Contract.RequiredBeats)
	}
	if len(plan.Contract.ContinuityChecks) != 1 ||
		!strings.Contains(plan.Contract.ContinuityChecks[0], marker) {
		t.Fatalf("predecessor state missing from continuity checks: %#v", plan.Contract.ContinuityChecks)
	}
	if len(plan.Contract.ForbiddenMoves) != 1 ||
		!strings.Contains(plan.Contract.ForbiddenMoves[0], "不得把 project-all predecessor-state") {
		t.Fatalf("predecessor state missing no-restaging guard: %#v", plan.Contract.ForbiddenMoves)
	}
	applyProjectAllOutlineObligations(&plan, []string{marker})
	if len(plan.Contract.ContinuityChecks) != 1 || len(plan.Contract.ForbiddenMoves) != 1 {
		t.Fatalf("predecessor contract replay was not idempotent: %+v", plan.Contract)
	}
}

func TestFinalRewritePlanKeepsSourceBoundGoalAndHook(t *testing.T) {
	st := newPhaseTestStore(t)
	if err := st.Outline.SaveOutline([]domain.OutlineEntry{{
		Chapter:   1,
		Title:     "稳定标题",
		CoreEvent: "旧大纲错误地让沈知遥先制止",
		Hook:      "旧大纲提前写皮卡已经到位",
	}}); err != nil {
		t.Fatal(err)
	}
	plan := domain.ChapterPlan{
		Chapter: 1,
		Goal:    "林澈先发现风险并主动停扩",
		Hook:    "借车请求仍未获答复",
		CausalSimulation: domain.ChapterCausalSimulation{
			WorldSimulationID:   "ch001-current",
			ProtagonistDecision: "先停扩，再等待现实运力答复",
		},
	}
	applyOutlineAnchorsToPlan(st, &plan, true)
	if plan.Title != "稳定标题" || plan.Goal != "林澈先发现风险并主动停扩" || plan.Hook != "借车请求仍未获答复" {
		t.Fatalf("rewrite finalization replayed stale outline goal/hook: %+v", plan)
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

func TestPlanIdentityAllowsJSONOnlyCompanionSystemPolicy(t *testing.T) {
	st := newPhaseTestStore(t)
	if err := st.Characters.Save([]domain.Character{{Name: "林澈", Role: "主角", Tier: "core"}}); err != nil {
		t.Fatal(err)
	}
	policy := `{
  "version": 1,
  "system_companion": {
    "required": true,
    "companion_voice_beat": "系统短促接话并支持主角",
    "forbidden_comedy": ["不连续抛梗"]
  }
}`
	if err := os.WriteFile(filepath.Join(st.Dir(), "meta", "web_reference_brief.json"), []byte(policy), 0o644); err != nil {
		t.Fatal(err)
	}
	payload := map[string]any{
		"title": "第一章",
		"causal_simulation": map[string]any{
			"voice_logic": []any{
				map[string]any{"character": "林澈", "surface_voice": "正常说话"},
				map[string]any{"character": "系统", "surface_voice": "短促接话"},
			},
		},
	}
	issues := ChapterPlanIdentityIssues(st, 1, payload)
	if joined := strings.Join(issues, "\n"); strings.Contains(joined, `character="系统"`) {
		t.Fatalf("JSON-only companion policy should allow the system speaking entity: %v", issues)
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

func TestPlanDetailsPreserveConstraintBatchesDeduplicateQuoteVariants(t *testing.T) {
	canonical := "只点“少糖”的两碗豆腐脑。"
	merged := map[string]any{
		"review_refinement": map[string]any{
			"preserve_constraints": []any{canonical},
		},
	}
	mergeCausalSimulationPatch(merged, map[string]any{
		"review_refinement": map[string]any{
			"preserve_constraints": []any{"只点'少糖'的两碗豆腐脑。", "模型新增约束。"},
		},
	})
	refinement := merged["review_refinement"].(map[string]any)
	got := stringSliceFromAny(refinement["preserve_constraints"])
	want := []string{canonical, "模型新增约束。"}
	if len(got) != len(want) {
		t.Fatalf("quote-only batch duplicate survived: got=%#v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("batch fact %d mismatch: got=%q want=%q", i, got[i], want[i])
		}
	}
}

func TestPlanDetailsSourceFactsOverrideModelSpellingAndOrder(t *testing.T) {
	st := newPhaseTestStore(t)
	prepareRewriteSourceTest(t, st,
		"第一章\n\n林澈收下十二元。",
		"# brief\n\n## 保留事实\n\n- 只点“少糖”的两碗豆腐脑。\n- 林澈先叫停，沈知遥后到场。\n")
	merged := map[string]any{
		"review_refinement": map[string]any{
			"preserve_constraints": []any{
				"模型新增约束。",
				"只点「少糖」的两碗豆腐脑。",
			},
		},
	}
	applyPlanDetailsSourceAnchors(st, 1, merged, nil, nil)
	refinement := merged["review_refinement"].(map[string]any)
	got := stringSliceFromAny(refinement["preserve_constraints"])
	want := []string{
		"只点“少糖”的两碗豆腐脑。",
		"林澈先叫停，沈知遥后到场。",
		"模型新增约束。",
	}
	if len(got) != len(want) {
		t.Fatalf("source-first facts length mismatch: got=%#v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("source-first fact %d mismatch: got=%q want=%q", i, got[i], want[i])
		}
	}
}

func TestPlanDetailsSourceAnchorsProjectHumanReadableProtagonistDecision(t *testing.T) {
	st := newPhaseTestStore(t)
	merged := map[string]any{
		"protagonist_decision": "模型提交的值会由服务端来源锚点统一",
	}
	simulation := &domain.ChapterWorldSimulation{
		Chapter:      1,
		SimulationID: "sim-1",
		ProtagonistProjection: domain.ProtagonistDecisionProjection{
			ChosenDecision: "保留已自纠的安全部分，继续小范围试点并等待借车答复",
			AvailableOptions: []string{
				"保留已自纠的安全部分，继续小范围试点并等待借车答复",
			},
		},
	}

	if err := applyPlanDetailsSourceAnchors(st, 1, merged, simulation, nil); err != nil {
		t.Fatal(err)
	}
	if got := merged["protagonist_decision"]; got != simulation.ProtagonistProjection.AvailableOptions[0] {
		t.Fatalf("formal plan leaked authority sentinel instead of projected choice: %q", got)
	}
}

func TestPlanDetailsSourceAnchorsRestoreServedCanonicalPlanningSources(t *testing.T) {
	st := newPhaseTestStore(t)
	if err := st.SaveSimulationRestartPolicy(domain.SimulationRestartPolicy{
		Version: 1, Active: true, Mode: "restart", GenerationID: "generation-current",
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveWorldFoundation(domain.WorldFoundation{}); err != nil {
		t.Fatal(err)
	}
	if err := st.Characters.Save([]domain.Character{{Name: "程野", Role: "主角", Tier: "core"}}); err != nil {
		t.Fatal(err)
	}
	if err := st.Outline.SaveOutline([]domain.OutlineEntry{{
		Chapter: 2, Title: "实时核验", CoreEvent: "程野完成实时性核验", Hook: "地点仍未知",
	}}); err != nil {
		t.Fatal(err)
	}
	merged := map[string]any{
		"context_sources": []any{domain.PlanningContextAccessTokenPrefix + strings.Repeat("a", 64)},
	}
	if err := applyPlanDetailsSourceAnchors(st, 2, merged, nil, nil); err != nil {
		t.Fatal(err)
	}
	sources := stringSliceFromAny(merged["context_sources"])
	for _, want := range []string{
		"meta/simulation_restart_policy.md#generation_id=generation-current",
		"meta/world_foundation.md",
		"character_dossiers",
		"outline.json#chapter=2",
		"working_memory.current_chapter_outline#chapter=2",
		"progression/chapter_contract#chapter=2",
	} {
		if !contextSourcesContain(sources, want) {
			t.Fatalf("served canonical planning source %q was not restored: %#v", want, sources)
		}
	}

	before := len(sources)
	if err := applyPlanDetailsSourceAnchors(st, 2, merged, nil, nil); err != nil {
		t.Fatal(err)
	}
	if after := len(stringSliceFromAny(merged["context_sources"])); after != before {
		t.Fatalf("canonical source restoration must be idempotent: before=%d after=%d", before, after)
	}
}

func TestPlanDetailsNormalizesCurrentRAGFactHitAliases(t *testing.T) {
	st, receipt := newPlanDetailsRAGFactReceiptFixture(t, 3)
	alias := func(index int) string {
		return domain.RAGFactReceiptTokenPrefix + receipt.ID + "#hit=" + fmt.Sprint(index)
	}
	merged := map[string]any{
		"external_reference_plan": []any{map[string]any{
			"query_or_need": "三段履约记录", "source_type": "RAG",
			"source_refs": []any{alias(0)}, "retrieved_at": receipt.CreatedAt,
			"freshness_requirement": "当前项目事实", "usable_details": []any{"送达先后不等于距离"},
			"transformation_rule": "转成角色可见的记录错位", "do_not_use": []any{"不复制摘要"},
		}},
		"grounding_details": []any{map[string]any{
			"detail": "观看端时间只形成待校时项", "source_ref": alias(1),
			"transformed_as": "屏幕上的冲突时间戳", "scene_anchor": "桥湾核验",
		}},
		"reality_support_plan": []any{map[string]any{
			"domain": "公共交付", "source_ref": alias(2), "usable_detail": "骑手正常交付即离开",
			"transformed_as": "公共入口交接动作", "chapter_use": "候选点排除",
			"forbidden_direct_use": []any{"不让骑手侦查"},
		}},
	}

	if err := applyPlanDetailsSourceAnchors(st, 1, merged, nil, nil); err != nil {
		t.Fatal(err)
	}
	external := merged["external_reference_plan"].([]any)[0].(map[string]any)
	if got := stringSliceFromAny(external["source_refs"]); len(got) != 1 || got[0] != receipt.Hits[0].Ref {
		t.Fatalf("external alias was not normalized: %#v", got)
	}
	grounding := merged["grounding_details"].([]any)[0].(map[string]any)
	if got := grounding["source_ref"]; got != receipt.Hits[1].Ref {
		t.Fatalf("grounding alias was not normalized: %#v", got)
	}
	support := merged["reality_support_plan"].([]any)[0].(map[string]any)
	if got := support["source_ref"]; got != receipt.Hits[2].Ref {
		t.Fatalf("reality-support alias was not normalized: %#v", got)
	}
}

func TestPlanDetailsRehomesExternalShapedGroundingRAGFacts(t *testing.T) {
	st, receipt := newPlanDetailsRAGFactReceiptFixture(t, 6)
	aliases := make([]any, 0, len(receipt.Hits))
	for index := range receipt.Hits {
		aliases = append(aliases, fmt.Sprintf("%s%s#hit=%d", domain.RAGFactReceiptTokenPrefix, receipt.ID, index))
	}
	merged := map[string]any{
		"grounding_details": []any{map[string]any{
			"source_type": "rag_fact", "source_refs": aliases,
			"usable_details": []any{
				"订单总送达先后不能直接换算成距离",
				"观看端声音时刻与服务端片段顺序是不同证据层",
				"普通骑手完成公共交付即离开",
			},
			"transformation_rule": "转成程野可见的记录错位与保守排除依据",
			"do_not_use":          []any{"不编造延迟秒数", "不让骑手承担侦查风险"},
			"scene_anchor":        "三段履约记录与桥湾观看端声音时间并列核验",
		}},
	}

	if err := applyPlanDetailsSourceAnchors(st, 1, merged, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, exists := merged["grounding_details"]; exists {
		t.Fatalf("external-shaped grounding row was not rehomed: %#v", merged["grounding_details"])
	}
	external, ok := merged["external_reference_plan"].([]any)
	if !ok || len(external) != 1 {
		t.Fatalf("rehome did not create exactly one external fact row: %#v", merged["external_reference_plan"])
	}
	entry := external[0].(map[string]any)
	refs := stringSliceFromAny(entry["source_refs"])
	if len(refs) != len(receipt.Hits) {
		t.Fatalf("rehome lost receipt refs: %#v", refs)
	}
	for index, hit := range receipt.Hits {
		if refs[index] != hit.Ref || strings.Contains(refs[index], "#hit=") {
			t.Fatalf("ref %d was not exact: got=%q want=%q", index, refs[index], hit.Ref)
		}
	}

	// A later batch commonly replaces external_reference_plan with craft-only
	// rows. Preserve the already staged current-receipt fact transformation,
	// while retaining replacement semantics for every older craft row.
	staged := append([]any(nil), external...)
	craftReceipt, craftRow := planDetailsCraftExternalFixture()
	mergeCausalSimulationPatch(merged, map[string]any{
		"external_reference_plan": []any{craftRow},
	})
	if err := applyPlanDetailsSourceAnchors(st, 1, merged, nil, craftReceipt, staged); err != nil {
		t.Fatal(err)
	}
	external = merged["external_reference_plan"].([]any)
	if len(external) != 2 {
		t.Fatalf("craft replacement deleted or duplicated the staged fact row: %#v", external)
	}
	var factRows, craftRows int
	for _, item := range external {
		row := item.(map[string]any)
		switch strings.ToLower(stringFromAny(row["source_type"])) {
		case "rag":
			factRows++
		case craftSourceType:
			craftRows++
		}
	}
	if factRows != 1 || craftRows != 1 {
		t.Fatalf("sequential merge lost fact/craft separation: fact=%d craft=%d rows=%#v", factRows, craftRows, external)
	}

	// Replaying the same staged rows remains idempotent.
	staged = append([]any(nil), external...)
	if err := applyPlanDetailsSourceAnchors(st, 1, merged, nil, craftReceipt, staged); err != nil {
		t.Fatal(err)
	}
	if got := len(merged["external_reference_plan"].([]any)); got != 2 {
		t.Fatalf("RAG fact preservation was not idempotent: got %d rows", got)
	}

	raw, err := json.Marshal(map[string]any{"chapter": 1, "causal_simulation": merged})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := decodeChapterPlanArgs(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateRAGFactPlanCurrent(st, plan); err != nil {
		t.Fatalf("normalized partial did not become a consumable receipt-backed plan: %v", err)
	}
	packet := newDraftRenderPacket(plan)
	if len(packet.FactAnchors) == 0 || packet.FactAnchors[0].Authority != "rag_fact_receipt" {
		t.Fatalf("normalized facts did not project a receipt-backed render anchor: %#v", packet.FactAnchors)
	}
}

func TestPlanDetailsDoesNotPreserveStaleOrIncompleteRAGFactRows(t *testing.T) {
	st, receipt := newPlanDetailsRAGFactReceiptFixture(t, 2)
	craftReceipt, craftRow := planDetailsCraftExternalFixture()
	staleRef := domain.RAGFactReceiptTokenPrefix + strings.Repeat("f", 24) +
		"#chunk=stale#hash=" + strings.Repeat("a", 64)
	complete := func(refs []any) map[string]any {
		return map[string]any{
			"query_or_need": "现场事实", "source_type": "RAG", "source_refs": refs,
			"retrieved_at": receipt.CreatedAt, "freshness_requirement": "当前项目事实",
			"usable_details": []any{"转成可见动作"}, "transformation_rule": "只支撑当前选择",
			"do_not_use": []any{"不复制摘要"},
		}
	}
	incomplete := complete([]any{receipt.Hits[0].Ref})
	delete(incomplete, "transformation_rule")
	staged := []any{
		complete([]any{staleRef}),
		complete([]any{receipt.Hits[0].Ref, staleRef}),
		complete([]any{receipt.SourceToken()}),
		incomplete,
		map[string]any{
			"query_or_need": "旧 craft", "source_type": craftSourceType,
			"source_refs":  []any{craftReceiptSourceToken(strings.Repeat("e", 24)) + "#chunk=old#hash=h"},
			"retrieved_at": "old", "freshness_requirement": "old", "usable_details": []any{"old"},
			"transformation_rule": "old", "do_not_use": []any{"old"},
		},
	}
	merged := map[string]any{
		"external_reference_plan": []any{craftRow},
	}

	if err := applyPlanDetailsSourceAnchors(st, 1, merged, nil, craftReceipt, staged); err != nil {
		t.Fatal(err)
	}
	external := merged["external_reference_plan"].([]any)
	if len(external) != 1 || strings.ToLower(stringFromAny(external[0].(map[string]any)["source_type"])) != craftSourceType {
		t.Fatalf("stale/mixed/source-token/incomplete fact rows or old craft rows survived: %#v", external)
	}
	raw, err := json.Marshal(map[string]any{"chapter": 1, "causal_simulation": merged})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := decodeChapterPlanArgs(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateRAGFactPlanCurrent(st, plan); err == nil || !strings.Contains(err.Error(), "没有通过") {
		t.Fatalf("invalid staged facts did not fail closed as unconsumed: %v", err)
	}
}

func TestPlanDetailsSequentialPatchesPreserveCurrentRAGFactRow(t *testing.T) {
	st, receipt := newPlanDetailsRAGFactReceiptFixture(t, 1)
	if _, err := NewPlanStructureTool(st).Execute(context.Background(), planStructureArgs(1)); err != nil {
		t.Fatal(err)
	}
	first, err := json.Marshal(map[string]any{
		"chapter": 1,
		"causal_simulation": map[string]any{
			"grounding_details": []any{map[string]any{
				"source_type":         "rag_fact",
				"source_refs":         []any{fmt.Sprintf("%s%s#hit=0", domain.RAGFactReceiptTokenPrefix, receipt.ID)},
				"usable_details":      []any{"送达先后不能直接换算成距离"},
				"transformation_rule": "转成角色可见的记录错位",
				"do_not_use":          []any{"不复制摘要"},
				"scene_anchor":        "三段履约记录核验",
			}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewPlanDetailsTool(st).Execute(context.Background(), first); err != nil {
		t.Fatalf("first plan_details rehome: %v", err)
	}
	second, err := json.Marshal(map[string]any{
		"chapter": 1,
		"causal_simulation": map[string]any{
			"external_reference_plan": []any{map[string]any{
				"query_or_need": "项目简报生活纹理", "source_type": "project_web_reference_brief",
				"source_refs": []any{"meta/web_reference_brief.md"}, "retrieved_at": "2026-07-18T00:00:00Z",
				"freshness_requirement": "稳定生活动作", "usable_details": []any{"公共入口交付"},
				"transformation_rule": "只转成现场动作", "do_not_use": []any{"不复制网页摘要"},
			}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewPlanDetailsTool(st).Execute(context.Background(), second); err != nil {
		t.Fatalf("second plan_details replacement: %v", err)
	}
	partial, err := st.Drafts.LoadChapterPlanPartial(1)
	if err != nil || partial == nil {
		t.Fatalf("load sequential partial: partial=%+v err=%v", partial, err)
	}
	merged, _ := partial["causal_simulation"].(map[string]any)
	external, _ := merged["external_reference_plan"].([]any)
	if len(external) != 2 {
		t.Fatalf("PlanDetailsTool replacement lost the staged fact row: %#v", external)
	}
	raw, err := json.Marshal(map[string]any{"chapter": 1, "causal_simulation": merged})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := decodeChapterPlanArgs(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateRAGFactPlanCurrent(st, plan); err != nil {
		t.Fatalf("sequential PlanDetailsTool partial lost current RAG consumption: %v", err)
	}
}

func TestPlanDetailsExternalRAGFactReceiptSetAliasesExpandToSelectedHits(t *testing.T) {
	st, receipt := newPlanDetailsRAGFactReceiptFixture(t, 2)
	for name, setAlias := range map[string]string{
		"canonical source token": receipt.SourceToken(),
		"bare current id":        domain.RAGFactReceiptTokenPrefix + receipt.ID,
	} {
		t.Run(name, func(t *testing.T) {
			merged := map[string]any{
				"external_reference_plan": []any{map[string]any{
					"query_or_need": "本章事实锚点", "source_type": "RAG",
					"source_refs": []any{setAlias}, "retrieved_at": receipt.CreatedAt,
					"freshness_requirement": "当前项目事实", "usable_details": []any{"把选中事实转成可见核验动作"},
					"transformation_rule": "只支撑本章既定选择", "do_not_use": []any{"不复制来源摘要"},
				}},
			}
			if err := applyPlanDetailsSourceAnchors(st, 1, merged, nil, nil); err != nil {
				t.Fatal(err)
			}
			external := merged["external_reference_plan"].([]any)
			refs, ok := strictPartialStringRefs(external[0].(map[string]any)["source_refs"])
			if !ok || len(refs) != len(receipt.Hits) {
				t.Fatalf("receipt set alias was not expanded to selected hits: %#v", refs)
			}
			for i, hit := range receipt.Hits {
				if refs[i] != hit.Ref {
					t.Fatalf("expanded ref[%d] = %q, want %q", i, refs[i], hit.Ref)
				}
			}
		})
	}
}

func TestPlanDetailsMaterializesServerMetadataForExactCurrentRAGFactRow(t *testing.T) {
	st, receipt := newPlanDetailsRAGFactReceiptFixture(t, 1)
	merged := map[string]any{
		"external_reference_plan": []any{map[string]any{
			"query_or_need":         "project-all-methodology",
			"source_type":           "rag_fact",
			"source_refs":           []any{receipt.Hits[0].Ref},
			"retrieved_at":          "2020-01-01T00:00:00Z",
			"freshness_requirement": "稳定写作方法；错误沿用了 craft receipt policy",
			"usable_details": []any{
				"把当前项目事实转成本章角色可见的核验动作",
			},
			"transformation_rule": "只支撑本章既定选择，不复制来源表述",
			"do_not_use":          []any{"不从来源扩写未授权空间"},
		}},
	}

	if err := applyPlanDetailsSourceAnchors(st, 1, merged, nil, nil); err != nil {
		t.Fatal(err)
	}
	rows, ok := merged["external_reference_plan"].([]any)
	if !ok || len(rows) != 1 {
		t.Fatalf("exact receipt-backed row was lost: %#v", merged["external_reference_plan"])
	}
	row := rows[0].(map[string]any)
	if row["query_or_need"] != receipt.Query || row["source_type"] != "RAG" ||
		row["retrieved_at"] != receipt.CreatedAt ||
		row["freshness_requirement"] != "当前项目事实 receipt；selected hits 与当前 RAG index 已由服务端校验" {
		t.Fatalf("server-owned current receipt metadata was not materialized: %#v", row)
	}
	raw, err := json.Marshal(map[string]any{"chapter": 1, "causal_simulation": merged})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := decodeChapterPlanArgs(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateRAGFactPlanCurrent(st, plan); err != nil {
		t.Fatalf("normalized exact current row did not pass the unchanged formal-plan guard: %v", err)
	}
}

func TestPlanDetailsDoesNotMaterializeRAGMetadataWithoutExactCompleteBinding(t *testing.T) {
	st, receipt := newPlanDetailsRAGFactReceiptFixture(t, 1)
	staleRef := domain.RAGFactReceiptTokenPrefix + strings.Repeat("f", 24) +
		"#chunk=stale#hash=" + strings.Repeat("a", 64)
	complete := func(refs []any) map[string]any {
		return map[string]any{
			"source_refs":         refs,
			"usable_details":      []any{"转成本章可见事实"},
			"transformation_rule": "只支撑本章既定选择",
			"do_not_use":          []any{"不复制来源"},
		}
	}
	tests := map[string]map[string]any{
		"stale": complete([]any{staleRef}),
		"mixed": complete([]any{receipt.Hits[0].Ref, staleRef}),
		"explicit non fact authority": func() map[string]any {
			row := complete([]any{receipt.Hits[0].Ref})
			row["source_type"] = craftSourceType
			return row
		}(),
		"incomplete transformation": func() map[string]any {
			row := complete([]any{receipt.Hits[0].Ref})
			delete(row, "do_not_use")
			return row
		}(),
	}
	for name, row := range tests {
		t.Run(name, func(t *testing.T) {
			merged := map[string]any{"external_reference_plan": []any{row}}
			if err := applyPlanDetailsSourceAnchors(st, 1, merged, nil, nil); err != nil {
				t.Fatal(err)
			}
			if strings.TrimSpace(stringFromAny(row["query_or_need"])) != "" ||
				strings.TrimSpace(stringFromAny(row["retrieved_at"])) != "" ||
				strings.TrimSpace(stringFromAny(row["freshness_requirement"])) != "" {
				t.Fatalf("unbound or incomplete row received server metadata: %#v", row)
			}
			raw, err := json.Marshal(map[string]any{"chapter": 1, "causal_simulation": merged})
			if err != nil {
				t.Fatal(err)
			}
			plan, err := decodeChapterPlanArgs(raw)
			if err != nil {
				t.Fatal(err)
			}
			if err := ValidateRAGFactPlanCurrent(st, plan); err == nil {
				t.Fatal("unchanged hard receipt guard accepted an unbound or incomplete row")
			}
		})
	}
}

func TestPlanDetailsRehomesProductionGroundingRAGFactsWithoutMetadata(t *testing.T) {
	st, receipt := newPlanDetailsRAGFactReceiptFixture(t, 3)
	aliases := make([]any, 0, len(receipt.Hits))
	for index := range receipt.Hits {
		aliases = append(aliases, fmt.Sprintf("%s%s#hit=%d", domain.RAGFactReceiptTokenPrefix, receipt.ID, index))
	}
	merged := map[string]any{
		"grounding_details": []any{map[string]any{
			"source_refs":         aliases,
			"usable_details":      []any{"订单时间戳只能形成宽窗", "公共交付后骑手立即离开"},
			"transformation_rule": "转成本章可见的履约记录与安全边界",
			"do_not_use":          []any{"不把预计送达写成实际完成"},
		}},
	}
	if err := applyPlanDetailsSourceAnchors(st, 1, merged, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, exists := merged["grounding_details"]; exists {
		t.Fatalf("production-shaped grounding row was not rehomed: %#v", merged["grounding_details"])
	}
	external, ok := merged["external_reference_plan"].([]any)
	if !ok || len(external) != 1 {
		t.Fatalf("expected one rehomed external row: %#v", merged["external_reference_plan"])
	}
	entry := external[0].(map[string]any)
	if got := stringFromAny(entry["query_or_need"]); got != receipt.Query {
		t.Fatalf("server receipt query metadata = %q", got)
	}
	refs, ok := strictPartialStringRefs(entry["source_refs"])
	if !ok || len(refs) != len(receipt.Hits) {
		t.Fatalf("rehome lost current receipt hits: %#v", refs)
	}
	for i, hit := range receipt.Hits {
		if refs[i] != hit.Ref {
			t.Fatalf("rehomed ref[%d] = %q, want %q", i, refs[i], hit.Ref)
		}
	}
	raw, err := json.Marshal(map[string]any{"chapter": 1, "causal_simulation": merged})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := decodeChapterPlanArgs(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateRAGFactPlanCurrent(st, plan); err != nil {
		t.Fatalf("production-shaped row did not become consumable: %v", err)
	}
}

func TestPlanDetailsRAGFactHitAliasesFailClosed(t *testing.T) {
	st, receipt := newPlanDetailsRAGFactReceiptFixture(t, 2)
	current := fmt.Sprintf("%s%s#hit=0", domain.RAGFactReceiptTokenPrefix, receipt.ID)
	tests := map[string][]any{
		"wrong receipt":      {domain.RAGFactReceiptTokenPrefix + strings.Repeat("f", 24) + "#hit=0"},
		"wrong bare receipt": {domain.RAGFactReceiptTokenPrefix + strings.Repeat("f", 24)},
		"out of range":       {fmt.Sprintf("%s%s#hit=%d", domain.RAGFactReceiptTokenPrefix, receipt.ID, len(receipt.Hits))},
		"negative":           {domain.RAGFactReceiptTokenPrefix + receipt.ID + "#hit=-1"},
		"leading zero":       {domain.RAGFactReceiptTokenPrefix + receipt.ID + "#hit=01"},
		"extra suffix":       {current + "#extra"},
		"mixed":              {current, domain.RAGFactReceiptTokenPrefix + strings.Repeat("e", 24) + "#hit=1"},
	}
	for name, refs := range tests {
		t.Run(name, func(t *testing.T) {
			merged := map[string]any{
				"grounding_details": []any{map[string]any{
					"source_type": "rag_fact", "source_refs": refs,
					"usable_details":      []any{"只能由当前精确命中支撑"},
					"transformation_rule": "转成现场动作", "do_not_use": []any{"不复制摘要"},
					"scene_anchor": "现场核验",
				}},
			}
			if err := applyPlanDetailsSourceAnchors(st, 1, merged, nil, nil); err != nil {
				t.Fatal(err)
			}
			if _, exists := merged["external_reference_plan"]; exists {
				t.Fatalf("invalid or ambiguous aliases were rehomed: %#v", merged["external_reference_plan"])
			}
			if _, exists := merged["grounding_details"]; !exists {
				t.Fatal("invalid aliases were silently discarded")
			}
		})
	}
}

func TestPartialRAGFactRefResolverRepairsUnambiguousLocalAliases(t *testing.T) {
	const receiptID = "0123456789abcdef01234567"
	documentID := strings.Repeat("a", 16)
	hit := domain.RAGFactReceiptHit{
		ChunkID:       "local:" + documentID + ":002",
		ContentSHA256: strings.Repeat("b", 64),
	}
	hit.Ref = domain.RAGFactReceiptHitRef(receiptID, hit)
	receipt := &domain.RAGFactReceipt{ID: receiptID, Hits: []domain.RAGFactReceiptHit{hit}}
	alias := domain.RAGFactReceiptTokenPrefix + receiptID + "#hit=0"
	resolve := partialRAGFactRefResolver(receipt, map[string]string{alias: hit.Ref})

	for name, input := range map[string]string{
		"ordered hit alias": alias,
		"exact chunk id":    hit.ChunkID,
		"local document id": "local:" + documentID + strings.Repeat("c", 48),
		"malformed current receipt chunk": domain.RAGFactReceiptTokenPrefix + receiptID +
			"#chunk=local:" + documentID + strings.Repeat("d", 48) + "#hash=" + strings.Repeat("e", 64),
	} {
		t.Run(name, func(t *testing.T) {
			if got := resolve(input); got != hit.Ref {
				t.Fatalf("unambiguous current-receipt alias was not repaired: got=%q want=%q", got, hit.Ref)
			}
		})
	}
	if got := resolve(domain.RAGFactReceiptTokenPrefix + strings.Repeat("f", 24) + "#chunk=" + hit.ChunkID); got != "" {
		t.Fatalf("wrong receipt alias must fail closed, got %q", got)
	}
	if got := resolve("local:" + documentID + "not-hex"); got != "" {
		t.Fatalf("non-hex local alias must fail closed, got %q", got)
	}
}

func TestPartialRAGFactRefResolverRejectsAmbiguousLocalDocumentAlias(t *testing.T) {
	const receiptID = "0123456789abcdef01234567"
	documentID := strings.Repeat("a", 16)
	hits := []domain.RAGFactReceiptHit{
		{ChunkID: "local:" + documentID + ":001", ContentSHA256: strings.Repeat("b", 64)},
		{ChunkID: "local:" + documentID + ":002", ContentSHA256: strings.Repeat("c", 64)},
	}
	for index := range hits {
		hits[index].Ref = domain.RAGFactReceiptHitRef(receiptID, hits[index])
	}
	receipt := &domain.RAGFactReceipt{ID: receiptID, Hits: hits}
	resolve := partialRAGFactRefResolver(receipt, nil)
	if got := resolve("local:" + documentID + strings.Repeat("d", 48)); got != "" {
		t.Fatalf("document-only alias with multiple current hits must fail closed, got %q", got)
	}
	if got := resolve(hits[1].ChunkID); got != hits[1].Ref {
		t.Fatalf("exact chunk id remains unambiguous: got=%q want=%q", got, hits[1].Ref)
	}
}

func newPlanDetailsRAGFactReceiptFixture(t *testing.T, count int) (*store.Store, domain.RAGFactReceipt) {
	t.Helper()
	st := newPhaseTestStore(t)
	chunks := make([]domain.RAGChunk, 0, count)
	for index := 0; index < count; index++ {
		chunks = append(chunks, rag.NormalizeChunk(domain.RAGChunk{
			ID:         fmt.Sprintf("fact:plan-details-%d", index),
			SourcePath: fmt.Sprintf("summaries/%02d.json", index),
			SourceKind: "chapter_summary_facts",
			Facet:      "plot",
			Summary:    fmt.Sprintf("第 %d 条可用项目事实", index+1),
			Text:       fmt.Sprintf("第 %d 条可用项目事实的完整文本。", index+1),
		}))
	}
	if err := st.RAG.SaveIndexState(domain.RAGIndexState{
		SchemaVersion: domain.CurrentRAGIndexSchemaVersion,
		Config:        domain.RAGIndexConfig{Collection: "local_keyword"},
		Chunks:        chunks,
		UpdatedAt:     "fixture",
	}); err != nil {
		t.Fatal(err)
	}
	hits := make([]domain.RAGFactReceiptHit, 0, len(chunks))
	for index, chunk := range chunks {
		hits = append(hits, domain.RAGFactReceiptHit{
			Rank: index + 1, ChunkID: chunk.ID, ContentSHA256: rag.RehashChunk(chunk).Hash,
			SourcePath: chunk.SourcePath, SourceKind: chunk.SourceKind, Facet: chunk.Facet,
		})
	}
	receipt, err := domain.NewRAGFactReceipt(1, "当前章节项目事实", []string{"当前", "事实"}, "local_keyword", "", hits)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.RAG.SaveRAGFactReceipt(receipt); err != nil {
		t.Fatal(err)
	}
	return st, receipt
}

func planDetailsCraftExternalFixture() (*domain.CraftRecallReceipt, map[string]any) {
	id := "0123456789abcdef01234567"
	ref := craftReceiptSourceToken(id) + "#chunk=craft:scene#hash=" + strings.Repeat("b", 64)
	receipt := &domain.CraftRecallReceipt{
		ID: id, CreatedAt: "2026-07-18T00:00:00Z",
		Attempts: []domain.CraftRecallReceiptAttempt{{
			Need: domain.CraftRecallNeed{ID: "project-all-scene"},
			Hits: []domain.CraftRecallReceiptHit{{
				Ref: ref, SourceKind: "craft_technique",
			}},
		}},
	}
	row := map[string]any{
		"query_or_need": "project-all-scene", "source_type": "craft_technique",
		"source_refs": []any{ref}, "retrieved_at": receipt.CreatedAt,
		"freshness_requirement": "当前 craft receipt", "usable_details": []any{"场景由人物选择改变处境"},
		"transformation_rule": "只迁移场景因果方法", "do_not_use": []any{"不复制来源情节"},
	}
	return receipt, row
}

func TestPlanDetailsRecommendedBatchesPreserveProjectContracts(t *testing.T) {
	guidance := strings.Join(planDetailsRecommendedBatches(), "\n")
	for _, required := range []string{
		"reader_entertainment_plan",
		"trend_language_plan",
		"meta/web_reference_brief",
	} {
		if !strings.Contains(guidance, required) {
			t.Fatalf("recommended batches omit project contract %q: %s", required, guidance)
		}
	}
	if strings.Contains(guidance, "entertainment/longform/trend 均为可选") {
		t.Fatalf("recommended batches still describe project-required contracts as optional: %s", guidance)
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

func TestPlanDetailsRejectsConfiguredProjectContamination(t *testing.T) {
	st := newPhaseTestStore(t)
	if err := st.Characters.Save([]domain.Character{{Name: "顾晴", Role: "主角", Tier: "core"}}); err != nil {
		t.Fatalf("SaveCharacters: %v", err)
	}
	saveTestProjectContaminationTerms(t, st, "外部项目专名")
	structure, _ := json.Marshal(map[string]any{
		"chapter":  1,
		"title":    "测试章",
		"goal":     "顾晴核对发布会资料。",
		"conflict": "顾晴必须在时间压力下保留证据。",
		"hook":     "顾晴发现确认单仍待签。",
	})
	if _, err := NewPlanStructureTool(st).Execute(context.Background(), structure); err != nil {
		t.Fatalf("plan_structure: %v", err)
	}
	args, _ := json.Marshal(map[string]any{
		"chapter": 1,
		"causal_simulation": map[string]any{
			"chapter_function": "顾晴误把外部项目专名写进记录。",
		},
	})
	_, err := NewPlanDetailsTool(st).Execute(context.Background(), args)
	if err == nil || !strings.Contains(err.Error(), "user_rules.forbidden_phrases") {
		t.Fatalf("expected configured project contamination rejection, got %v", err)
	}
	partial, _ := st.Drafts.LoadChapterPlanPartial(1)
	raw, _ := json.Marshal(partial)
	if strings.Contains(string(raw), "外部项目专名") {
		t.Fatalf("contaminated details must not persist: %s", raw)
	}
}
