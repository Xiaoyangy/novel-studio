package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chenhongyang/novel-studio/assets"
	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/voocel/agentcore"
)

// Replay synthetic wire responses through the configured DeepSeek provider,
// streaming adapter and actual operation loop. No credentials/network service
// or historical novel are used; this is not a claim of a live provider repro.
func TestOutlineAllDeepSeekStreamRecoversTextOnlyEnd(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var request struct {
			Model  string            `json:"model"`
			Stream bool              `json:"stream"`
			Tools  []json.RawMessage `json:"tools"`
		}
		if json.Unmarshal(raw, &request) != nil || request.Model != "deepseek-v4-flash" || !request.Stream || len(request.Tools) != 1 {
			t.Errorf("unexpected fixture request model/stream/capability")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		switch requests.Add(1) {
		case 1:
			fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"准备展开目标弧。\"},\"finish_reason\":\"stop\"}]}\n\n")
		case 2:
			if !strings.Contains(string(raw), "operation=29") {
				t.Error("follow-up lost the operation identity")
			}
			wire, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{
				"index": 0, "finish_reason": "tool_calls", "delta": map[string]any{"tool_calls": []any{map[string]any{
					"index": 0, "id": "wire-save", "type": "function", "function": map[string]any{
						"name": "save_foundation", "arguments": `{"type":"expand_arc","volume":3,"arc":3,"content":[]}`,
					},
				}}},
			}}})
			fmt.Fprintf(w, "data: %s\n\n", wire)
		default:
			t.Error("unexpected extra request after successful save")
		}
		fmt.Fprint(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":2,\"total_tokens\":12}}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	cfg := bootstrap.Config{OutputDir: st.Dir(), Provider: "deepseek", ModelName: "deepseek-v4-flash",
		Providers: map[string]bootstrap.ProviderConfig{"deepseek": {APIKey: "synthetic-fixture-key", BaseURL: server.URL}}}
	models, err := bootstrap.NewModelSet(cfg)
	if err != nil {
		t.Fatal(err)
	}
	saves, metered := 0, 0
	tool := agentcore.NewFuncTool("save_foundation", "fixture save", map[string]any{"type": "object"}, func(_ context.Context, raw json.RawMessage) (json.RawMessage, error) {
		if !json.Valid(raw) || !strings.Contains(string(raw), "expand_arc") {
			t.Fatal("stream did not preserve the tool arguments")
		}
		saves++
		return json.RawMessage(`{"saved":true,"outline_all":true,"type":"expand_arc"}`), nil
	})
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	err = runOutlineAllOperationWithModel(ctx, cfg, assets.Bundle{}, st, outlineAllOperationTask(t, 29, 3, 3, 14),
		outlineAllOperationModel{ChatModel: models.ForRole("architect"), Provider: "deepseek", Name: "deepseek-v4-flash"}, tool,
		func(_ string, raw agentcore.AgentMessage) {
			if message, ok := raw.(agentcore.Message); ok && message.Role == agentcore.RoleAssistant && message.Usage != nil {
				metered++
			}
		})
	if err != nil || requests.Load() != 2 || saves != 1 || metered != 2 {
		t.Fatalf("wire recovery: err=%v requests=%d saves=%d metered=%d", err, requests.Load(), saves, metered)
	}
}
