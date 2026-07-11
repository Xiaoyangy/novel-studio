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

func TestRewriteBriefPreserveFactsIgnoresEmptyPlaceholder(t *testing.T) {
	brief := "# brief\n\n## 保留事实\n\n- 无额外条目。\n\n## 必须修正\n\n- 调整分号。\n"
	if facts := rewriteBriefPreserveFacts(brief); len(facts) != 0 {
		t.Fatalf("empty placeholder must not become a preserve fact: %v", facts)
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
