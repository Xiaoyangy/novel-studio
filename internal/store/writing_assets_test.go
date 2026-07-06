package store

import (
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

func TestWritingAssetsApplyStyleRulesCompilesFeaturePool(t *testing.T) {
	s := newTestStore(t)
	err := s.WritingAssets.ApplyStyleRules(domain.WritingStyleRules{
		Volume: 1,
		Arc:    2,
		Prose:  []string{"叙述保持克制，少解释因果"},
		Dialogue: []domain.CharacterVoice{
			{Name: "林砚", Rules: []string{"短句多，先问证据"}},
		},
		Taboos: []string{"不要章末强行升华"},
	})
	if err != nil {
		t.Fatalf("ApplyStyleRules: %v", err)
	}

	compiled, err := s.WritingAssets.Compile(4)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if compiled == nil || len(compiled.EnabledFeatures) != 3 {
		t.Fatalf("expected 3 enabled features, got %+v", compiled)
	}
	if len(compiled.ActiveRules) != 3 {
		t.Fatalf("expected 3 active rules, got %+v", compiled.ActiveRules)
	}
}

func TestWritingAssetsSeedDefaultsCreatesCompiledBaseline(t *testing.T) {
	s := newTestStore(t)
	features, presets, bindings, err := s.WritingAssets.SeedDefaults()
	if err != nil {
		t.Fatalf("SeedDefaults: %v", err)
	}
	if features < 6 || presets != 1 || bindings != 1 {
		t.Fatalf("unexpected seed counts: features=%d presets=%d bindings=%d", features, presets, bindings)
	}
	compiled, err := s.WritingAssets.Compile(8)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if compiled == nil || len(compiled.EnabledFeatures) < 6 || len(compiled.ActiveRules) == 0 {
		t.Fatalf("expected compiled defaults, got %+v", compiled)
	}
	if len(compiled.AntiAIRules) == 0 || len(compiled.Taboos) == 0 {
		t.Fatalf("expected anti-ai and taboo defaults, got %+v", compiled)
	}
	if !containsStringLocal(compiled.ActiveRules, "每章写作前确认主角目标、阻力、失败代价和本章新增信息。") {
		t.Fatalf("expected chapter craft rule, got %+v", compiled.ActiveRules)
	}
}

func TestWritingAssetsSeedDefaultsIsIdempotent(t *testing.T) {
	s := newTestStore(t)
	if _, _, _, err := s.WritingAssets.SeedDefaults(); err != nil {
		t.Fatalf("SeedDefaults first: %v", err)
	}
	features, presets, bindings, err := s.WritingAssets.SeedDefaults()
	if err != nil {
		t.Fatalf("SeedDefaults second: %v", err)
	}
	if features != 0 || presets != 0 || bindings != 0 {
		t.Fatalf("expected idempotent seed, got features=%d presets=%d bindings=%d", features, presets, bindings)
	}
}

func TestWritingAssetsApplyReviewFeedbackSedimentsHistory(t *testing.T) {
	s := newTestStore(t)
	feedback, features, err := s.WritingAssets.ApplyReviewFeedback(domain.ReviewEntry{
		Chapter:        4,
		Scope:          "chapter",
		ContractMisses: []string{"没有兑现药盒订单的代价"},
		Issues: []domain.ConsistencyIssue{
			{
				Type:        "aesthetic",
				Severity:    "warning",
				Description: "章末用抽象金句问号假装钩子",
				Evidence:    "那是不是命运给他的答案？",
				Suggestion:  "改成具体物件变化或未完成选择收束。",
			},
			{
				Type:        "ai_voice_detection",
				Severity:    "error",
				Description: "连续抽象判断，缺少现场动作",
				Evidence:    "他意识到一切都不同了。",
				Suggestion:  "把判断拆进动作、物件和角色选择后果。",
			},
		},
		Dimensions: []domain.DimensionScore{
			{Dimension: "aesthetic", Score: 72, Verdict: "warning", Comment: "语言有模板感"},
			{Dimension: "hook", Score: 76, Verdict: "warning", Comment: "章末钩子偏虚"},
		},
		Verdict: "polish",
		Summary: "需要打磨写法。",
	}, "polish", "部分维度需打磨")
	if err != nil {
		t.Fatalf("ApplyReviewFeedback: %v", err)
	}
	if feedback < 5 || features < 5 {
		t.Fatalf("expected feedback and features from review, got feedback=%d features=%d", feedback, features)
	}

	lib, err := s.WritingAssets.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if lib == nil || len(lib.Feedback) < 5 {
		t.Fatalf("expected stored feedback entries, got %+v", lib)
	}
	compiled, err := s.WritingAssets.Compile(8)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if compiled == nil || len(compiled.Feedback) == 0 {
		t.Fatalf("expected compiled feedback, got %+v", compiled)
	}
	if !containsStringLocal(compiled.ActiveRules, "历史审阅反馈：改成具体物件变化或未完成选择收束。") {
		t.Fatalf("expected review suggestion in active rules, got %+v", compiled.ActiveRules)
	}
	data, err := s.WritingAssets.io.ReadFile("meta/writing_assets.md")
	if err != nil {
		t.Fatalf("read writing assets markdown: %v", err)
	}
	text := string(data)
	for _, want := range []string{"历史反馈沉淀", "章末用抽象金句问号", "reviews/04.json"} {
		if !strings.Contains(text, want) {
			t.Fatalf("writing_assets.md missing %q:\n%s", want, text)
		}
	}
}

func TestWritingAssetsCompileForScopeHonorsBindings(t *testing.T) {
	lib := domain.WritingAssetLibrary{
		Version: 1,
		Features: []domain.WritingFeature{
			{ID: "global", Name: "全局", Category: "prose", Enabled: true, Rules: []string{"全局规则"}},
			{ID: "ch2", Name: "第二章", Category: "pacing", Enabled: true, Rules: []string{"第二章规则"}},
			{ID: "arc1", Name: "第一弧", Category: "structure", Enabled: true, Rules: []string{"第一弧规则"}},
		},
		Bindings: []domain.WritingBinding{
			{Scope: "chapter", Chapter: 2, FeatureID: "ch2"},
			{Scope: "arc", Volume: 1, Arc: 1, FeatureID: "arc1"},
		},
	}

	ch1 := CompileWritingAssetsForScope(lib, 4, &domain.WritingBinding{Scope: "chapter", Volume: 1, Arc: 2, Chapter: 1})
	if len(ch1.ActiveRules) != 1 || ch1.ActiveRules[0] != "全局规则" {
		t.Fatalf("expected only global rule for ch1, got %+v", ch1.ActiveRules)
	}

	ch2 := CompileWritingAssetsForScope(lib, 4, &domain.WritingBinding{Scope: "chapter", Volume: 1, Arc: 1, Chapter: 2})
	if len(ch2.ActiveRules) != 3 {
		t.Fatalf("expected global, arc, and chapter rule for ch2, got %+v", ch2.ActiveRules)
	}
}

func containsStringLocal(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestWritingAssetsSaveTrialWritesEditableTask(t *testing.T) {
	s := newTestStore(t)
	rel, err := s.WritingAssets.SaveTrial(
		domain.WritingBinding{Scope: "trial"},
		"写便利店交易场景",
		domain.WritingCompiled{
			ActiveRules: []string{"规则解释必须伴随动作"},
			Trace:       []string{"scope=trial"},
		},
	)
	if err != nil {
		t.Fatalf("SaveTrial: %v", err)
	}
	data, err := s.WritingAssets.io.ReadFile(rel)
	if err != nil {
		t.Fatalf("read trial: %v", err)
	}
	if got := string(data); !strings.Contains(got, "写法试写任务") || !strings.Contains(got, "写便利店交易场景") {
		t.Fatalf("unexpected trial content: %s", got)
	}
}
