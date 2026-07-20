package main

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/voocel/agentcore"
)

type blockingReviewCacheModel struct {
	response string
	started  chan struct{}
	release  chan struct{}
	calls    atomic.Int32
}

func (m *blockingReviewCacheModel) Generate(ctx context.Context, _ []agentcore.Message, _ []agentcore.ToolSpec, _ ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	m.calls.Add(1)
	select {
	case m.started <- struct{}{}:
	default:
	}
	select {
	case <-m.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return &agentcore.LLMResponse{Message: agentcore.Message{
		Role:    agentcore.RoleAssistant,
		Content: []agentcore.ContentBlock{agentcore.TextBlock(m.response)},
	}}, nil
}

func (m *blockingReviewCacheModel) GenerateStream(context.Context, []agentcore.Message, []agentcore.ToolSpec, ...agentcore.CallOption) (<-chan agentcore.StreamEvent, error) {
	ch := make(chan agentcore.StreamEvent)
	close(ch)
	return ch, nil
}

func (m *blockingReviewCacheModel) SupportsTools() bool { return false }

func TestEditorExactBodyCacheSingleflightCoalescesConcurrentMisses(t *testing.T) {
	dir := t.TempDir()
	body := "第一章\n\n林澈把门推开。"
	analysis := editorCacheTestAnalysis(body, "2026-07-20T10:00:00+08:00")
	model := &blockingReviewCacheModel{
		response: editorCacheTestMarkdown,
		started:  make(chan struct{}, 2),
		release:  make(chan struct{}),
	}
	results := make(chan editorReviewBranchResult, 2)
	call := func() {
		results <- loadOrGenerateEditorReview(
			dir, model, "openai", "editor-v1", "premise", "rules", "chapter-context",
			1, body, analysis, 2*time.Second,
		)
	}
	go call()
	select {
	case <-model.started:
	case <-time.After(time.Second):
		t.Fatal("first Editor request did not reach model")
	}
	go call()
	time.Sleep(75 * time.Millisecond)
	if got := model.calls.Load(); got != 1 {
		t.Fatalf("concurrent exact-body Editor model calls before release = %d, want 1", got)
	}
	close(model.release)
	first, second := <-results, <-results
	assertSingleflightEditorResults(t, first, second)
	if got := model.calls.Load(); got != 1 {
		t.Fatalf("concurrent exact-body Editor model calls = %d, want 1", got)
	}
}

func assertSingleflightEditorResults(t *testing.T, first, second editorReviewBranchResult) {
	t.Helper()
	for i, result := range []editorReviewBranchResult{first, second} {
		if result.Err != nil || result.CacheArtifact == nil {
			t.Fatalf("Editor result[%d] = %+v", i, result)
		}
	}
	if first.CacheHit == second.CacheHit || first.CachePersisted == second.CachePersisted {
		t.Fatalf("want one Editor producer and one cache follower, first=%+v second=%+v", first, second)
	}
}

func TestDeepSeekExactBodyCacheSingleflightCoalescesConcurrentMisses(t *testing.T) {
	dir := t.TempDir()
	body := "第一章\n\n林澈把账本合上。"
	selection := deepseekAIJudgeModelSelection{Provider: "deepseek", Model: "deepseek-v4-pro", Explicit: true}
	model := &blockingReviewCacheModel{
		response: deepseekCompleteHumanResponse,
		started:  make(chan struct{}, 2),
		release:  make(chan struct{}),
	}
	results := make(chan deepseekAIJudgeBranchResult, 2)
	call := func() {
		results <- loadOrGenerateDeepSeekAIJudge(dir, model, selection, 1, body, 2*time.Second)
	}
	go call()
	select {
	case <-model.started:
	case <-time.After(time.Second):
		t.Fatal("first DeepSeek request did not reach model")
	}
	go call()
	time.Sleep(75 * time.Millisecond)
	if got := model.calls.Load(); got != 1 {
		t.Fatalf("concurrent exact-body DeepSeek model calls before release = %d, want 1", got)
	}
	close(model.release)
	first, second := <-results, <-results
	for i, result := range []deepseekAIJudgeBranchResult{first, second} {
		if result.Err != nil || result.Artifact == nil {
			t.Fatalf("DeepSeek result[%d] = %+v", i, result)
		}
	}
	if first.CacheHit == second.CacheHit || first.CachePersisted == second.CachePersisted {
		t.Fatalf("want one DeepSeek producer and one cache follower, first=%+v second=%+v", first, second)
	}
	if got := model.calls.Load(); got != 1 {
		t.Fatalf("concurrent exact-body DeepSeek model calls = %d, want 1", got)
	}
}
