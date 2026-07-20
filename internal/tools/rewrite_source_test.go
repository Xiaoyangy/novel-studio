package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func prepareRewriteSourceTest(t *testing.T, st *store.Store, body, brief string) *domain.ChapterRewriteSource {
	t.Helper()
	if err := st.Progress.MarkChapterComplete(1, len([]rune(body)), "", ""); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.SetPendingRewrites([]int{1}, "局部修复"); err != nil {
		t.Fatal(err)
	}
	if err := st.Drafts.SaveFinalChapter(1, body); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(st.Dir(), "reviews"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(st.Dir(), "reviews", "01_rewrite_brief.md"), []byte(brief), 0o644); err != nil {
		t.Fatal(err)
	}
	source, _, _, err := loadChapterRewriteSource(st, 1)
	if err != nil {
		t.Fatal(err)
	}
	return source
}

func TestRewriteSourceExtractsPreserveFactsAndBodyHash(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init("test", 2); err != nil {
		t.Fatal(err)
	}
	source := prepareRewriteSourceTest(t, st,
		"第一章 失业饭桌\n\n林澈在夜市付款4280元。",
		"# brief\n\n## 保留事实\n\n- 林澈付款4280元。\n- 沈知遥章末要求明早九点带票据。\n\n## 修改目标\n\n- 增加一次犹豫。\n")
	if source.BodySHA256 == "" || source.WordCount == 0 {
		t.Fatalf("rewrite source missing body identity: %+v", source)
	}
	if len(source.PreserveFacts) != 2 || source.PreserveFacts[0] != "林澈付款4280元。" {
		t.Fatalf("preserve facts mismatch: %+v", source.PreserveFacts)
	}
}

func TestRewriteSourceMergesCommittedOutcomeLedgerFacts(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init("test", 2); err != nil {
		t.Fatal(err)
	}
	prepareRewriteSourceTest(t, st,
		"第一章\n\n林澈在夜市完成试点。",
		"# brief\n\n## 保留事实\n\n- 林澈独立发现走线风险。\n")
	ledger := domain.ChapterProgressLedger{Version: 1, Entries: []domain.ChapterProgressEntry{{
		Chapter: 1,
		StateChanges: []domain.StateChange{{
			Chapter: 1, Entity: "马玉芬", Field: "resource",
			NewValue: "获得有限试用设施并新增两碗豆腐脑共12元真实营业收入",
			Reason:   "这段旧原因可能正被评审纠正，不应进入 preserve fact",
		}},
		ResourceChanges: []domain.ResourceClaim{{
			Name: "贺骁皮卡次晨临时借用请求", Status: "pending",
			Risk: "不得视为车辆已经取得。", Evidence: "贺骁尚未答复。",
		}},
	}}}
	raw, err := json.MarshalIndent(ledger, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(st.Dir(), "meta"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(st.Dir(), "meta", "chapter_progress.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}

	source, _, _, err := loadChapterRewriteSource(st, 1)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(source.PreserveFacts, "\n")
	for _, want := range []string{
		"林澈独立发现走线风险。",
		"已提交状态结果：马玉芬.resource = 获得有限试用设施并新增两碗豆腐脑共12元真实营业收入",
		"已提交资源结果：贺骁皮卡次晨临时借用请求；status=pending；边界=不得视为车辆已经取得。；证据=贺骁尚未答复。",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("rewrite source lost %q: %+v", want, source)
		}
	}
	if strings.Contains(joined, "旧原因") {
		t.Fatalf("rewrite source leaked mutable causal reason: %s", joined)
	}
	if source.CanonicalStatePath != "meta/chapter_progress.json" || len(source.CanonicalStateSHA256) != 64 {
		t.Fatalf("canonical state receipt missing: %+v", source)
	}

	ledger.Entries[0].StateChanges[0].NewValue = "改成一碗"
	raw, _ = json.MarshalIndent(ledger, "", "  ")
	if err := os.WriteFile(filepath.Join(st.Dir(), "meta", "chapter_progress.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	changed, _, _, err := loadChapterRewriteSource(st, 1)
	if err != nil {
		t.Fatal(err)
	}
	if rewriteSourceEqual(source, changed) {
		t.Fatal("changed committed outcome ledger did not invalidate rewrite source identity")
	}
}

func TestRewriteBriefPreserveFactsIgnoresEmptyPlaceholder(t *testing.T) {
	brief := "# brief\n\n## 保留事实\n\n- 无额外条目。\n\n## 必须修正\n\n- 调整分号。\n"
	if facts := rewriteBriefPreserveFacts(brief); len(facts) != 0 {
		t.Fatalf("empty placeholder must not become a preserve fact: %v", facts)
	}
}

func TestRewriteBriefRefinementAnchorsSurvivePlanningCompression(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init("test", 2); err != nil {
		t.Fatal(err)
	}
	prepareRewriteSourceTest(t, st,
		"第一章\n\n旧稿。",
		"# brief\n\n## 合同漏项\n\n- 三场结构未建立\n\n## 必须修正\n\n- 冷饮支架作为唯一完整实操主场\n  - 证据：旧稿安装流程分散\n\n```text\n- 不得投影的代码块条目\n```\n\n### 历史证据\n\n- 不得投影的 H3 条目\n\n## 必须修正（已解决）\n\n- 不得投影的历史条目\n\n## 验收条件\n\n- 整篇单段门禁必须通过\n- 说明：这是摘要而非验收项\n\n## 最新整篇单段门禁（2026-07-15）\n\n- 连续对白段不得超过 2\n\n## 最新整篇单段门禁（已解决）\n\n- 不得投影的过期门禁\n")
	plan := domain.ChapterPlan{Chapter: 1}
	normalizeRewriteBriefRefinement(st, &plan)
	loop := plan.CausalSimulation.ReviewRefinement
	if len(loop.FailureModes) != 1 || !strings.Contains(loop.FailureModes[0], "三场") {
		t.Fatalf("contract failures were not anchored: %+v", loop)
	}
	joinedTargets := strings.Join(loop.LocalizedTargets, "\n")
	if !strings.Contains(joinedTargets, "冷饮支架") || !strings.Contains(joinedTargets, "连续对白段") ||
		strings.Contains(joinedTargets, "证据：") || strings.Contains(joinedTargets, "H3") ||
		strings.Contains(joinedTargets, "代码块") || strings.Contains(joinedTargets, "历史条目") || strings.Contains(joinedTargets, "过期门禁") {
		t.Fatalf("rewrite targets were not compactly anchored: %+v", loop.LocalizedTargets)
	}
	if len(loop.AcceptanceChecks) != 1 || !strings.Contains(loop.AcceptanceChecks[0], "整篇单段") {
		t.Fatalf("acceptance checks were not anchored: %+v", loop.AcceptanceChecks)
	}
}

func TestRewriteBriefRefinementRedactsNegativeProjectContamination(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init("test", 2); err != nil {
		t.Fatal(err)
	}
	if err := st.Characters.Save([]domain.Character{{Name: "顾晴", Role: "主角"}}); err != nil {
		t.Fatal(err)
	}
	saveTestProjectContaminationTerms(t, st, "外部项目专名", "过期流程术语")
	prepareRewriteSourceTest(t, st,
		"第一章\n\n顾晴收起手机。",
		"# brief\n\n## 必须修正\n\n- 删除旧稿中的过期流程术语和外部项目专名，回到当前人物线\n")
	plan := domain.ChapterPlan{Chapter: 1, Goal: "顾晴回到当前人物线"}
	plan.CausalSimulation.ReviewRefinement.LocalizedTargets = []string{
		"删除过期流程术语和外部项目专名，回到当前人物线",
	}
	normalizeRewriteBriefRefinement(st, &plan)
	joined := strings.Join(plan.CausalSimulation.ReviewRefinement.LocalizedTargets, "\n")
	if containsProjectContaminationTerm(projectContaminationTerms(st), joined) {
		t.Fatalf("negative diagnosis reintroduced forbidden project terms: %s", joined)
	}
	if strings.Count(joined, "[项目禁用元素]") < 2 {
		t.Fatalf("redacted diagnosis lost its actionable categories: %s", joined)
	}
	if err := validateProjectContaminationFinal(st, "chapter plan", plan); err != nil {
		t.Fatalf("sanitized model diagnosis still blocks plan finalization: %v", err)
	}
}

func TestRewriteVisibleCharactersComeFromCommittedBody(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init("test", 2); err != nil {
		t.Fatal(err)
	}
	if err := st.Characters.Save([]domain.Character{
		{Name: "林澈", Role: "主角"},
		{Name: "沈知遥", Role: "女主"},
		{Name: "马玉芬", Role: "商户代表"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.Outline.SaveOutline([]domain.OutlineEntry{{Chapter: 1, Title: "失业饭桌", CoreEvent: "林澈在饭桌承认失业"}}); err != nil {
		t.Fatal(err)
	}
	prepareRewriteSourceTest(t, st,
		"第一章 失业饭桌\n\n林澈去了夜市。马玉芬收款后，沈知遥检查票据。",
		"# brief\n\n## 保留事实\n\n- 沈知遥章末检查票据。\n")
	names := chapterOutlineCharacterNames(st, 1)
	for _, want := range []string{"林澈", "沈知遥", "马玉芬"} {
		if !containsString(names, want) {
			t.Fatalf("rewrite-visible character %s missing from %v", want, names)
		}
	}
}

func TestStagedRewriteContextCarriesBodyBriefAndRejectsLegacySimulation(t *testing.T) {
	st := newChapterSimulationTestStore(t)
	if err := st.Outline.SaveOutline([]domain.OutlineEntry{{Chapter: 1, Title: "失业饭桌", CoreEvent: "林澈在饭桌承认失业"}}); err != nil {
		t.Fatal(err)
	}
	prepareRewriteSourceTest(t, st,
		"第一章 失业饭桌\n\n林澈付款4280元，沈知遥在章末检查票据。",
		"# brief\n\n## 保留事实\n\n- 林澈付款4280元。\n- 沈知遥章末检查票据。\n")
	legacy := domain.ChapterWorldSimulation{
		Version: 1, Chapter: 1, TimeWindow: "当晚",
		CharacterDecisions: []domain.CharacterWorldDecision{
			simulatedDecision("林澈", "承认失业", true),
			simulatedDecision("沈知遥", "留在办公室", false),
		},
		ProtagonistProjection: domain.ProtagonistDecisionProjection{
			Protagonist: "林澈", ObservableEffects: []string{"饭桌追问"}, HiddenPressures: []string{"办公室安排"},
			AvailableOptions: []string{"隐瞒", "承认"}, ChosenDecision: "承认失业", DecisionReason: "物证出现",
			PlanConstraints: []string{"限知"}, CausalChain: []string{"追问", "承认"},
		},
	}
	legacy.SimulationID = chapterWorldSimulationID(legacy)
	if err := st.SaveChapterWorldSimulation(legacy); err != nil {
		t.Fatal(err)
	}
	if err := st.Drafts.SaveChapterPlanPartial(1, map[string]any{
		"structure":         map[string]any{"chapter": 1, "title": "失业饭桌"},
		"causal_simulation": map[string]any{},
		"rewrite":           true,
	}); err != nil {
		t.Fatal(err)
	}
	raw, err := NewContextTool(st, References{}, "default").Execute(context.Background(), json.RawMessage(`{"chapter":1}`))
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	simulation := payload["chapter_world_simulation"].(map[string]any)
	if simulation["status"] != "invalid" {
		t.Fatalf("legacy simulation must be invalid for current rewrite source: %+v", simulation)
	}
	rewrite := payload["rewrite_source"].(map[string]any)
	if !strings.Contains(rewrite["current_body"].(string), "4280") || !strings.Contains(rewrite["brief_markdown"].(string), "保留事实") {
		t.Fatalf("staged context lost rewrite source: %+v", rewrite)
	}
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
