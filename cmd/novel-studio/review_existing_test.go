package main

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/reviewreport"
	"github.com/voocel/agentcore"
)

const editorCacheTestMarkdown = `# ch01 评审

## 总体评分：35 / 40
## 是否需要改写：否
## 一句话诊断：正文通过。`

type reviewCacheModel struct {
	response string
	calls    atomic.Int32
}

func (m *reviewCacheModel) Generate(_ context.Context, _ []agentcore.Message, _ []agentcore.ToolSpec, _ ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	m.calls.Add(1)
	return &agentcore.LLMResponse{Message: agentcore.Message{
		Role:    agentcore.RoleAssistant,
		Content: []agentcore.ContentBlock{agentcore.TextBlock(m.response)},
	}}, nil
}

func (m *reviewCacheModel) GenerateStream(context.Context, []agentcore.Message, []agentcore.ToolSpec, ...agentcore.CallOption) (<-chan agentcore.StreamEvent, error) {
	ch := make(chan agentcore.StreamEvent)
	close(ch)
	return ch, nil
}

func (m *reviewCacheModel) SupportsTools() bool { return false }

func (m *reviewCacheModel) callCount() int { return int(m.calls.Load()) }

func TestEditorReviewCacheHitSkipsModelCallAndUsesDedicatedArtifact(t *testing.T) {
	dir := t.TempDir()
	body := "第一章\n\n林澈把手机翻过来。"
	analysis := editorCacheTestAnalysis(body, "2026-07-11T10:00:00+08:00")
	model := &reviewCacheModel{response: editorCacheTestMarkdown}

	first := loadOrGenerateEditorReview(
		dir, model, "openai", "editor-v1", "premise", "rules", "chapter-context",
		1, body, analysis, time.Second,
	)
	if first.Err != nil || first.CacheHit || first.CacheArtifact == nil {
		t.Fatalf("first editor branch = %+v", first)
	}
	if err := saveEditorReviewCache(dir, first.CacheArtifact); err != nil {
		t.Fatalf("saveEditorReviewCache: %v", err)
	}

	// Generated timestamps are persistence metadata, not review context. A fresh
	// deterministic analysis for the same body must still hit the same cache.
	secondAnalysis := editorCacheTestAnalysis(body, "2026-07-11T10:05:00+08:00")
	second := loadOrGenerateEditorReview(
		dir, model, "openai", "editor-v1", "premise", "rules", "chapter-context",
		1, body, secondAnalysis, time.Second,
	)
	if second.Err != nil || !second.CacheHit {
		t.Fatalf("second editor branch = %+v", second)
	}
	if model.callCount() != 1 {
		t.Fatalf("Editor Generate calls = %d, want 1", model.callCount())
	}
	cachePath := reviewExistingCachePath(dir, editorReviewCacheBranch, first.CacheArtifact.CacheKey)
	if _, err := os.Stat(cachePath); err != nil {
		t.Fatalf("dedicated Editor cache artifact missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "reviews", "01.json")); !os.IsNotExist(err) {
		t.Fatalf("Editor cache must not reuse or create final ReviewEntry, stat err=%v", err)
	}
}

func TestEditorReviewCacheDriftCausesModelMiss(t *testing.T) {
	dir := t.TempDir()
	body := "第一章\n\n林澈停在门口。"
	analysis := editorCacheTestAnalysis(body, "2026-07-11T10:00:00+08:00")
	baseModel := &reviewCacheModel{response: editorCacheTestMarkdown}
	base := loadOrGenerateEditorReview(
		dir, baseModel, "openai", "editor-v1", "premise", "rules", "chapter-context",
		1, body, analysis, time.Second,
	)
	if base.Err != nil || base.CacheArtifact == nil {
		t.Fatalf("base editor branch = %+v", base)
	}
	if err := saveEditorReviewCache(dir, base.CacheArtifact); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name           string
		provider       string
		model          string
		premise        string
		userRules      string
		chapterContext string
		body           string
	}{
		{name: "body", provider: "openai", model: "editor-v1", premise: "premise", userRules: "rules", chapterContext: "chapter-context", body: body + "\n新一句。"},
		{name: "provider", provider: "anthropic", model: "editor-v1", premise: "premise", userRules: "rules", chapterContext: "chapter-context", body: body},
		{name: "model", provider: "openai", model: "editor-v2", premise: "premise", userRules: "rules", chapterContext: "chapter-context", body: body},
		{name: "premise", provider: "openai", model: "editor-v1", premise: "premise drift", userRules: "rules", chapterContext: "chapter-context", body: body},
		{name: "user rules", provider: "openai", model: "editor-v1", premise: "premise", userRules: "rules drift", chapterContext: "chapter-context", body: body},
		{name: "chapter context", provider: "openai", model: "editor-v1", premise: "premise", userRules: "rules", chapterContext: "chapter-context drift", body: body},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			model := &reviewCacheModel{response: editorCacheTestMarkdown}
			caseAnalysis := editorCacheTestAnalysis(tt.body, "2026-07-11T10:10:00+08:00")
			result := loadOrGenerateEditorReview(
				dir, model, tt.provider, tt.model, tt.premise, tt.userRules, tt.chapterContext,
				1, tt.body, caseAnalysis, time.Second,
			)
			if result.Err != nil || result.CacheHit {
				t.Fatalf("drifted editor branch = %+v", result)
			}
			if model.callCount() != 1 {
				t.Fatalf("Editor Generate calls = %d, want 1", model.callCount())
			}
		})
	}

	driftedPolicy := base.CacheArtifact.CachePolicy
	driftedPolicy.ReviewProtocolVersion += "-drift"
	if reviewExistingCacheKey(driftedPolicy) == base.CacheArtifact.CacheKey {
		t.Fatal("review protocol drift must change the cache key")
	}
	if cached, err := loadEditorReviewCache(dir, driftedPolicy); err != nil || cached != nil {
		t.Fatalf("protocol-drifted cache load = artifact:%+v err:%v, want miss", cached, err)
	}
}

func TestReviewBranchCachesAreIndependent(t *testing.T) {
	dir := t.TempDir()
	body := "第一章\n\n林澈把门推开。"
	analysis := editorCacheTestAnalysis(body, "2026-07-11T10:00:00+08:00")
	seedModel := &reviewCacheModel{response: editorCacheTestMarkdown}
	seed := loadOrGenerateEditorReview(
		dir, seedModel, "openai", "editor-v1", "premise", "rules", "chapter-context",
		1, body, analysis, time.Second,
	)
	if seed.Err != nil || seed.CacheArtifact == nil {
		t.Fatalf("seed editor branch = %+v", seed)
	}
	if err := saveEditorReviewCache(dir, seed.CacheArtifact); err != nil {
		t.Fatal(err)
	}

	editorModel := &reviewCacheModel{response: editorCacheTestMarkdown}
	deepseekModel := &reviewCacheModel{response: `{"verdict":"human_like","risk_level":"low","ai_probability_percent":5,"confidence":"high","summary":"自然"}`}
	editorResult, deepseekResult := runReviewExistingBranchesConcurrently(
		func() editorReviewBranchResult {
			return loadOrGenerateEditorReview(
				dir, editorModel, "openai", "editor-v1", "premise", "rules", "chapter-context",
				1, body, analysis, time.Second,
			)
		},
		func() deepseekAIJudgeBranchResult {
			return loadOrGenerateDeepSeekAIJudge(
				dir, deepseekModel,
				deepseekAIJudgeModelSelection{Provider: "deepseek", Model: "deepseek-v4-pro", Explicit: true},
				1, body, time.Second,
			)
		},
	)
	if editorResult.Err != nil || !editorResult.CacheHit || editorModel.callCount() != 0 {
		t.Fatalf("Editor independent hit failed: result=%+v calls=%d", editorResult, editorModel.callCount())
	}
	if deepseekResult.Err != nil || deepseekResult.CacheHit || deepseekModel.callCount() != 1 {
		t.Fatalf("DeepSeek independent miss failed: result=%+v calls=%d", deepseekResult, deepseekModel.callCount())
	}
	if err := saveDeepSeekAIJudgeCache(dir, deepseekResult.Artifact); err != nil {
		t.Fatal(err)
	}

	editorModel = &reviewCacheModel{response: editorCacheTestMarkdown}
	deepseekModel = &reviewCacheModel{response: `{"verdict":"human_like","risk_level":"low","ai_probability_percent":5}`}
	editorResult, deepseekResult = runReviewExistingBranchesConcurrently(
		func() editorReviewBranchResult {
			return loadOrGenerateEditorReview(
				dir, editorModel, "openai", "editor-v1", "premise", "rules", "changed-chapter-context",
				1, body, analysis, time.Second,
			)
		},
		func() deepseekAIJudgeBranchResult {
			return loadOrGenerateDeepSeekAIJudge(
				dir, deepseekModel,
				deepseekAIJudgeModelSelection{Provider: "deepseek", Model: "deepseek-v4-pro", Explicit: true},
				1, body, time.Second,
			)
		},
	)
	if editorResult.Err != nil || editorResult.CacheHit || editorModel.callCount() != 1 {
		t.Fatalf("Editor independent miss failed: result=%+v calls=%d", editorResult, editorModel.callCount())
	}
	if deepseekResult.Err != nil || !deepseekResult.CacheHit || deepseekModel.callCount() != 0 {
		t.Fatalf("DeepSeek independent hit failed: result=%+v calls=%d", deepseekResult, deepseekModel.callCount())
	}
}

func TestRunReviewExistingBranchesConcurrentlyStartsBoth(t *testing.T) {
	started := make(chan string, 2)
	release := make(chan struct{})
	done := make(chan struct{})
	var editorResult editorReviewBranchResult
	var deepseekResult deepseekAIJudgeBranchResult
	go func() {
		editorResult, deepseekResult = runReviewExistingBranchesConcurrently(
			func() editorReviewBranchResult {
				started <- "editor"
				<-release
				return editorReviewBranchResult{Review: "editor-done"}
			},
			func() deepseekAIJudgeBranchResult {
				started <- "deepseek"
				<-release
				return deepseekAIJudgeBranchResult{Artifact: &deepseekAIJudgeArtifact{Summary: "deepseek-done"}}
			},
		)
		close(done)
	}()

	seen := map[string]bool{}
	for len(seen) < 2 {
		select {
		case branch := <-started:
			seen[branch] = true
		case <-time.After(2 * time.Second):
			close(release)
			t.Fatal("both review branches did not start before release")
		}
	}
	close(release)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("parallel review helper did not return")
	}
	if editorResult.Review != "editor-done" || deepseekResult.Artifact == nil || deepseekResult.Artifact.Summary != "deepseek-done" {
		t.Fatalf("unexpected branch results: editor=%+v deepseek=%+v", editorResult, deepseekResult)
	}
}

func editorCacheTestAnalysis(body, generatedAt string) domain.AIVoiceAnalysis {
	return domain.AIVoiceAnalysis{
		Chapter:     1,
		BodySHA256:  reviewreport.BodySHA256(body),
		Label:       "可通过",
		Summary:     "规则分析稳定",
		GeneratedAt: generatedAt,
		Metrics: domain.ChapterAIVoiceMetrics{
			Chapter:     1,
			GeneratedAt: generatedAt,
			AIVoiceScoreHistory: []domain.AIVoiceScorePoint{{
				Round: 1, Source: "rules", Score: 0.1, At: generatedAt,
			}},
		},
	}
}
