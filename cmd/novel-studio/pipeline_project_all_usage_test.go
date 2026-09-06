package main

import (
	"testing"

	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/voocel/agentcore"
)

func projectAccountingStores(t *testing.T) (*store.Store, *store.Store) {
	t.Helper()
	live, shadow := store.NewStore(t.TempDir()), store.NewStore(t.TempDir())
	for _, st := range []*store.Store{live, shadow} {
		if err := st.Init(); err != nil {
			t.Fatal(err)
		}
	}
	return live, shadow
}

func projectUsageMessage(id string, cost float64) agentcore.Message {
	return agentcore.Message{Role: agentcore.RoleAssistant, Content: []agentcore.ContentBlock{agentcore.TextBlock("PRIVATE-REASONING-MUST-NOT-BE-AUDITED")},
		Usage: &agentcore.Usage{Provider: "codex-cli", Model: "gpt-6-astra", Input: 100, Output: 10, Cost: &agentcore.Cost{Total: cost}}, Metadata: map[string]any{"usage_audit_id": id}}
}
