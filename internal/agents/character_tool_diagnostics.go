package agents

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"log/slog"
	"os"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/voocel/agentcore"
)

type characterToolDiagnosticScopeKey struct{}

// A troubleshooting invocation may stop earlier than its frozen upper bound.
// This never changes prompts, models, proposal revisions or acceptance rules,
// and cannot raise a generation's limit. It avoids spending six more calls
// merely to obtain the first private validation error after a failed run.
func characterArbiterDiagnosticTurnLimit(tool string, limit int) int {
	if tool == "resolve_chapter_world" && limit > 1 && os.Getenv("NOVEL_STUDIO_ARBITER_SINGLE_TURN") == "1" {
		slog.Info("arbiter diagnostic single-turn cap enabled", "module", "character_tool_diagnostics")
		return 1
	}
	return limit
}

func withCharacterToolDiagnosticScope(ctx context.Context, scope domain.CharacterAgentUsage) context.Context {
	return context.WithValue(ctx, characterToolDiagnosticScopeKey{}, store.CharacterToolDiagnostic{
		GenerationID: scope.GenerationID, Chapter: scope.Chapter, Cycle: scope.Cycle,
		Round: scope.Round, AgentID: scope.AgentID, Role: scope.Role, UsageID: scope.UsageID,
	})
}

type characterToolDiagnosticObserver struct {
	store        *store.Store
	record       store.CharacterToolDiagnostic
	terminalTool string
	warned       bool
}

func newCharacterToolDiagnosticObserver(ctx context.Context, terminalTool string, stores []*store.Store) *characterToolDiagnosticObserver {
	if len(stores) == 0 || stores[0] == nil {
		return nil
	}
	record, _ := ctx.Value(characterToolDiagnosticScopeKey{}).(store.CharacterToolDiagnostic)
	record.LoopID = "ctd_" + rand.Text()
	return &characterToolDiagnosticObserver{store: stores[0], record: record, terminalTool: terminalTool}
}

func (o *characterToolDiagnosticObserver) observe(event agentcore.Event) {
	if o == nil || event.Type != agentcore.EventToolExecEnd || !event.IsError || event.Tool != o.terminalTool {
		return
	}
	o.record.Sequence++
	var text string
	raw := bytes.TrimSpace(event.Result)
	if len(raw) == 0 || raw[0] != '"' {
		o.warn("unsupported_error_encoding")
		return
	}
	if err := json.Unmarshal(raw, &text); err != nil {
		o.warn("unsupported_error_encoding")
		return // Never substitute Args, a message, or an arbitrary result object.
	}
	record := o.record
	record.Tool, record.ToolCallID = event.Tool, event.ToolID
	if err := o.store.AppendCharacterToolDiagnostic(record, text); err != nil {
		o.warn("private_diagnostic_write_failed")
	}
}

func (o *characterToolDiagnosticObserver) warn(code string) {
	if o.warned {
		return
	}
	o.warned = true
	// Errors and managed paths can contain private story values. Never log
	// either: diagnostics are observational and cannot abort/retry the model.
	slog.Warn("private tool validation diagnostic unavailable", "module", "character_tool_diagnostics", "loop_id", o.record.LoopID, "sequence", o.record.Sequence, "code", code)
}
