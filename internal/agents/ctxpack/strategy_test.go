package ctxpack

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/chenhongyang/novel-studio/internal/domain"
	storepkg "github.com/chenhongyang/novel-studio/internal/store"
	"github.com/voocel/agentcore"
	corecontext "github.com/voocel/agentcore/context"
)

func TestTruncateJSONToTokensKeepsCJKPayloadValidAndWithinBudget(t *testing.T) {
	source, err := json.Marshal(map[string]any{
		"角色状态": strings.Repeat("林岚在雨夜追查仓库线索。", 800),
		"线索":   []string{"旧录音", "码头目击", "未兑现承诺"},
	})
	if err != nil {
		t.Fatal(err)
	}

	const budget = 180
	got := truncateJSONToTokens(source, budget)
	if !utf8.ValidString(got) {
		t.Fatal("truncated restore JSON split a UTF-8 rune")
	}
	if !json.Valid([]byte(got)) {
		t.Fatalf("truncated restore section is not valid JSON: %q", got)
	}
	if tokens := corecontext.EstimateTokens(agentcore.UserMsg(got)); tokens > budget {
		t.Fatalf("CJK restore section exceeded token budget: got=%d budget=%d", tokens, budget)
	}
	var envelope struct {
		Truncated bool   `json:"_truncated"`
		Preview   string `json:"preview"`
	}
	if err := json.Unmarshal([]byte(got), &envelope); err != nil {
		t.Fatal(err)
	}
	if !envelope.Truncated || !strings.Contains(envelope.Preview, "角色状态") {
		t.Fatalf("expected an explicit, useful truncation preview: %#v", envelope)
	}
}

func TestTruncateJSONToTokensBoundsMixedUnicodeAndPreservesFittingEvidence(t *testing.T) {
	for _, sample := range []string{
		"receipt: signed; actor: Lin; ",
		"角色只知道已经发生的事实。",
		"🔒证据\"line\nnext\t<&>\\",
		strings.Repeat("contract ", 60) + strings.Repeat("中文证据", 90),
	} {
		source, err := json.Marshal(map[string]string{"evidence": strings.Repeat(sample, 300)})
		if err != nil {
			t.Fatal(err)
		}
		for _, budget := range []int{60, 180, 600} {
			text := truncateJSONToTokens(source, budget)
			if !utf8.ValidString(text) || !json.Valid([]byte(text)) {
				t.Fatalf("invalid UTF-8 or JSON at budget %d", budget)
			}
			if used := corecontext.EstimateTokens(agentcore.UserMsg(text)); used > budget {
				t.Fatalf("mixed-script evidence uses %d tokens, budget=%d", used, budget)
			}
		}
		budget := corecontext.EstimateTokens(agentcore.UserMsg(string(source)))
		if got := truncateJSONToTokens(source, budget); got != string(source) {
			t.Fatal("fitting evidence changed during truncation")
		}
	}
}

func TestAppendJSONSectionAccountsForHeadingAndTruncationSuffix(t *testing.T) {
	const budget = 220
	remaining := budget
	var parts []string
	stopped := appendJSONSection(&parts, "角色快照", map[string]any{
		"状态": strings.Repeat("雨夜追查仍在继续。", 1000),
	}, &remaining)
	if !stopped || len(parts) != 1 {
		t.Fatalf("expected one truncated terminal section, stopped=%v parts=%d", stopped, len(parts))
	}
	if used := corecontext.EstimateTokens(agentcore.UserMsg(parts[0])); used > budget {
		t.Fatalf("rendered section exceeded total budget: got=%d budget=%d", used, budget)
	}
	if remaining < 0 {
		t.Fatalf("remaining budget became negative: %d", remaining)
	}
}

func TestWriterContextBuildersAccountForWholeMessageBudget(t *testing.T) {
	s := seededWriterStore(t)
	if err := s.Drafts.SaveChapterPlan(domain.ChapterPlan{
		Chapter: 3, Title: "第三章", Goal: strings.Repeat("必须保持角色的知识边界与实际发生的因果。", 3000),
	}); err != nil {
		t.Fatal(err)
	}
	for _, budget := range []int{180, 600, restoreBudgetTokens} {
		for _, tc := range []struct {
			name  string
			build func(*storepkg.Store, int) (string, bool, error)
		}{
			{"restore", buildWriterRestoreText},
			{"summary", buildWriterStoreSummaryText},
		} {
			text, ok, err := tc.build(s, budget)
			if err != nil || !ok {
				t.Fatalf("%s budget=%d: ok=%v err=%v", tc.name, budget, ok, err)
			}
			if used := corecontext.EstimateTokens(agentcore.UserMsg(text)); used > budget {
				t.Errorf("%s whole message uses %d tokens, budget=%d", tc.name, used, budget)
			}
		}
	}
	pack := &WriterRestorePack{}
	pack.Refresh(s)
	if _, ok := pack.buildMessage(restoreBudgetTokens); !ok {
		t.Fatal("a full restore pack was silently discarded after adding its envelope")
	}
}

func BenchmarkTruncateJSONToTokensLargeCJK(b *testing.B) {
	source, err := json.Marshal(map[string]string{"角色状态": strings.Repeat("林岚在雨夜追查仓库线索。", 10000)})
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.SetBytes(int64(len(source)))
	b.ResetTimer()
	for b.Loop() {
		_ = truncateJSONToTokens(source, 600)
	}
}

func TestStoreSummaryCompactApplyUsesPersistentStoreData(t *testing.T) {
	s := seededWriterStore(t)
	strategy := NewStoreSummaryCompact(StoreSummaryCompactConfig{
		Store:              s,
		KeepRecentTokens:   80,
		SummaryTokenBudget: 2000,
	})

	msgs := []agentcore.AgentMessage{
		agentcore.UserMsg(strings.Repeat("旧上下文", 500)),
		agentcore.Message{
			Role:    agentcore.RoleAssistant,
			Content: []agentcore.ContentBlock{agentcore.TextBlock(strings.Repeat("旧回复", 500))},
		},
		agentcore.UserMsg("继续写第三章，注意承接第二章结尾。"),
		agentcore.Message{
			Role:    agentcore.RoleAssistant,
			Content: []agentcore.ContentBlock{agentcore.TextBlock("收到，我先梳理当前场景。")},
		},
	}

	out, result, err := strategy.Apply(context.Background(), msgs, msgs, corecontext.Budget{
		Tokens:    corecontext.EstimateTotal(msgs),
		Window:    128,
		Threshold: 32,
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !result.Applied {
		t.Fatal("expected store summary strategy to apply")
	}
	if result.Name != storeSummaryStrategyName {
		t.Fatalf("unexpected strategy name: %q", result.Name)
	}
	if len(out) < 2 {
		t.Fatalf("expected summary + kept messages, got %d", len(out))
	}
	summary, ok := out[0].(corecontext.ContextSummary)
	if !ok {
		t.Fatalf("expected ContextSummary, got %T", out[0])
	}
	if !strings.Contains(summary.Summary, "最近章节摘要") {
		t.Fatalf("expected persistent summaries in checkpoint, got %q", summary.Summary)
	}
	if !strings.Contains(summary.Summary, "当前章节计划") {
		t.Fatalf("expected chapter plan in checkpoint, got %q", summary.Summary)
	}
	if !strings.Contains(summary.Summary, "活跃伏笔") {
		t.Fatalf("expected foreshadow data in checkpoint, got %q", summary.Summary)
	}
	if !strings.Contains(summary.Summary, "待修审稿问题") {
		t.Fatalf("expected pending review section in checkpoint, got %q", summary.Summary)
	}
	if !strings.Contains(summary.Summary, "仓库线索需要再蓄压一拍") {
		t.Fatalf("expected pending review details in checkpoint, got %q", summary.Summary)
	}
	if !strings.Contains(summary.Summary, "写法引擎") {
		t.Fatalf("expected writing engine section in checkpoint, got %q", summary.Summary)
	}
	if !strings.Contains(summary.Summary, "单章四问") || !strings.Contains(summary.Summary, "每章写作前确认主角目标") {
		t.Fatalf("expected writing engine baseline rules in checkpoint, got %q", summary.Summary)
	}
	if result.Info == nil || result.Info.CompactedCount <= 0 {
		t.Fatalf("expected compaction info, got %+v", result.Info)
	}
}

func TestStoreSummaryCompactApplyFallsBackWhenStoreDataInsufficient(t *testing.T) {
	dir := t.TempDir()
	s := storepkg.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Save(&domain.Progress{
		Phase:             domain.PhaseWriting,
		CurrentChapter:    1,
		TotalChapters:     3,
		CompletedChapters: nil,
	}); err != nil {
		t.Fatalf("Save progress: %v", err)
	}

	strategy := NewStoreSummaryCompact(StoreSummaryCompactConfig{Store: s, KeepRecentTokens: 20})
	msgs := []agentcore.AgentMessage{
		agentcore.UserMsg(strings.Repeat("旧上下文", 40)),
		agentcore.Message{
			Role:    agentcore.RoleAssistant,
			Content: []agentcore.ContentBlock{agentcore.TextBlock(strings.Repeat("旧回复", 40))},
		},
	}

	out, result, err := strategy.Apply(context.Background(), msgs, msgs, corecontext.Budget{
		Tokens:    corecontext.EstimateTotal(msgs),
		Window:    64,
		Threshold: 16,
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if result.Applied {
		t.Fatal("expected no-op when persistent memory is insufficient")
	}
	if len(out) != len(msgs) {
		t.Fatalf("expected messages unchanged, got %d", len(out))
	}
}

func TestWriterRestorePackRefreshReusesStoreBuilder(t *testing.T) {
	s := seededWriterStore(t)
	pack := &WriterRestorePack{}
	pack.Refresh(s)

	msg, ok := pack.buildMessage(restoreBudgetTokens)
	if !ok {
		t.Fatal("expected restore pack message")
	}
	text := msg.TextContent()
	if !strings.Contains(text, "<post-compact-context>") {
		t.Fatalf("expected wrapped restore context, got %q", text)
	}
	if !strings.Contains(text, "待修审稿问题") {
		t.Fatalf("expected pending review section, got %q", text)
	}
	if !strings.Contains(text, "当前章节计划") {
		t.Fatalf("expected chapter plan section, got %q", text)
	}
	if !strings.Contains(text, "写法引擎") {
		t.Fatalf("expected writing engine section, got %q", text)
	}
	if strings.Contains(text, `"samples"`) {
		t.Fatalf("restore pack should not carry writing samples, got %q", text)
	}
}

func seededWriterStore(t *testing.T) *storepkg.Store {
	t.Helper()

	s := storepkg.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Save(&domain.Progress{
		Phase:             domain.PhaseWriting,
		CurrentChapter:    3,
		TotalChapters:     6,
		CompletedChapters: []int{1, 2},
		Flow:              domain.FlowWriting,
	}); err != nil {
		t.Fatalf("Save progress: %v", err)
	}
	if err := s.Outline.SaveOutline([]domain.OutlineEntry{
		{Chapter: 1, Title: "第一章", CoreEvent: "开场"},
		{Chapter: 2, Title: "第二章", CoreEvent: "冲突升级"},
		{Chapter: 3, Title: "第三章", CoreEvent: "追查线索", Scenes: []string{"主角追查失踪案", "发现旧仓库线索"}},
	}); err != nil {
		t.Fatalf("SaveOutline: %v", err)
	}
	if err := s.Drafts.SaveChapterPlan(domain.ChapterPlan{
		Chapter:    3,
		Title:      "第三章",
		Goal:       "推进失踪案调查",
		Conflict:   "主角与搭档对调查方向分歧",
		Hook:       "仓库中发现可疑录音",
		EmotionArc: "怀疑到紧张",
	}); err != nil {
		t.Fatalf("SaveChapterPlan: %v", err)
	}
	if err := s.Summaries.SaveSummary(domain.ChapterSummary{
		Chapter:    1,
		Summary:    "主角接下委托，发现失踪案并不简单。",
		Characters: []string{"林岚", "周策"},
		KeyEvents:  []string{"委托成立"},
	}); err != nil {
		t.Fatalf("SaveSummary 1: %v", err)
	}
	if err := s.Summaries.SaveSummary(domain.ChapterSummary{
		Chapter:    2,
		Summary:    "两人追查旧码头，线索指向废弃仓库。",
		Characters: []string{"林岚", "周策", "沈叔"},
		KeyEvents:  []string{"旧码头冲突", "仓库线索出现"},
	}); err != nil {
		t.Fatalf("SaveSummary 2: %v", err)
	}
	if err := s.World.SaveForeshadowLedger([]domain.ForeshadowEntry{
		{ID: "tape", Description: "失踪者留下的录音带", PlantedAt: 2, Status: "planted"},
	}); err != nil {
		t.Fatalf("SaveForeshadowLedger: %v", err)
	}
	if err := s.World.SaveTimeline([]domain.TimelineEvent{
		{Chapter: 2, Time: "夜晚", Event: "旧码头交锋", Characters: []string{"林岚", "周策"}},
	}); err != nil {
		t.Fatalf("SaveTimeline: %v", err)
	}
	if err := s.World.SaveStyleRules(domain.WritingStyleRules{
		Prose:  []string{"句子偏短，保持压迫感"},
		Taboos: []string{"避免直白解释谜团"},
	}); err != nil {
		t.Fatalf("SaveStyleRules: %v", err)
	}
	if _, _, _, err := s.WritingAssets.SeedDefaults(); err != nil {
		t.Fatalf("Seed writing assets: %v", err)
	}
	if err := s.World.SaveReview(domain.ReviewEntry{
		Chapter: 2,
		Scope:   "chapter",
		Verdict: "polish",
		Summary: "第二章结尾铺垫偏急，需要补一拍仓库前的压迫感。",
		Issues: []domain.ConsistencyIssue{
			{
				Type:        "pacing",
				Severity:    "warning",
				Description: "仓库线索出现过快，悬疑蓄压不够。",
				Suggestion:  "在进入仓库前增加一段迟疑与环境压迫描写。",
			},
		},
		ContractMisses: []string{"章末钩子不够强"},
	}); err != nil {
		t.Fatalf("Save chapter review: %v", err)
	}
	if err := s.World.SaveReview(domain.ReviewEntry{
		Chapter: 2,
		Scope:   "global",
		Verdict: "polish",
		Summary: "第二章尾声节奏偏快，仓库线索需要再蓄压一拍。",
	}); err != nil {
		t.Fatalf("SaveReview: %v", err)
	}
	return s
}
