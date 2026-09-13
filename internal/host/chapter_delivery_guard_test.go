package host

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chenhongyang/novel-studio/assets"
	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/voocel/agentcore"
)

func TestHostChapterDeadlineGuardsRetriesAndKeepsInflightResponse(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	var expired atomic.Bool
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) == 1 {
			close(entered)
		}
		select {
		case <-release:
		case <-r.Context().Done():
			t.Error("deadline cancelled the in-flight request")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"fake","model":"fake","choices":[{"index":0,"message":{"role":"assistant","content":"saved late result"},"finish_reason":"stop"}],"usage":{"prompt_tokens":7,"completion_tokens":2,"total_tokens":9}}`))
	}))
	defer server.Close()
	cfg := bootstrap.Config{OutputDir: t.TempDir(), Provider: "openai", ModelName: "fake", DisableLiveRAG: true,
		Providers: map[string]bootstrap.ProviderConfig{"openai": {APIKey: "local-test-only", BaseURL: server.URL + "/v1"}},
		BeforeProviderCall: func() error {
			if expired.Load() {
				return store.ErrChapterDeliveryDeadline
			}
			return nil
		}}
	eng, err := New(cfg, assets.Load("default"))
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	model := eng.models.ForRole("writer")
	done := make(chan error, 1)
	go func() {
		response, err := model.Generate(context.Background(), []agentcore.Message{agentcore.UserMsg("fake local test")}, nil)
		if err == nil && (response == nil || response.Message.TextContent() != "saved late result") {
			err = errors.New("late response was lost")
		}
		done <- err
	}()
	select {
	case <-entered:
	case err := <-done:
		t.Fatalf("fake provider failed before entering: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("fake provider never entered")
	}
	expired.Store(true)
	if _, err := model.Generate(context.Background(), nil, nil); !errors.Is(err, store.ErrChapterDeliveryDeadline) {
		t.Errorf("new request escaped deadline: %v", err)
	}
	releaseOnce.Do(func() { close(release) })
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 1 {
		t.Fatalf("HTTP requests=%d, want only original in-flight call", requests.Load())
	}
	if err := eng.usageAccounting.flush(); err != nil {
		t.Fatal(err)
	}
	_, input, output, _, _ := eng.usageAccounting.meter.Tracker().Totals()
	if input != 7 || output != 2 {
		t.Fatalf("late usage lost: %d/%d", input, output)
	}
}
