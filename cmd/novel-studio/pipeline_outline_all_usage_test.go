package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/assets"
	"github.com/chenhongyang/novel-studio/internal/agents"
	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/voocel/agentcore"
)

func TestPipelineOutlineAllUsagePersistsFailureAndResumeWithoutReplay(t *testing.T) {
	cfg := bootstrap.Config{OutputDir: t.TempDir()}
	st := store.NewStore(cfg.OutputDir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	logger := st.Sessions.SubAgentLogger(nil)
	message := agentcore.Message{Role: agentcore.RoleAssistant,
		Usage:    &agentcore.Usage{Provider: "codex-cli", Model: "gpt-6-astra", Input: 300_000},
		Metadata: map[string]any{"codex_usage_breakdown_complete": true, "codex_usage_breakdown": []*agentcore.Usage{{Input: 150_000}, {Input: 150_000}}},
	}
	// Only a missing snapshot may trigger replay of this historical usage.
	logger("architect_outline_all", "historical", message)
	modelFailure := errors.New("fake operation failure after metered response")
	for attempt := 0; attempt < 2; attempt++ {
		err := runPipelineOutlineAllArchitectWithUsage(context.Background(), cfg, assets.Bundle{}, "new operation", func(ctx context.Context, cfg bootstrap.Config, _ assets.Bundle, output, prompt string, records ...agents.UsageRecorder) error {
			if output != cfg.OutputDir || len(records) != 1 {
				t.Fatalf("invalid runner wiring")
			}
			logger("architect_outline_all", prompt, message)
			records[0]("architect_outline_all", message)
			state, err := st.Usage.Load()
			if err != nil || state == nil {
				t.Fatalf("usage not persisted during operation: state=%+v err=%v", state, err)
			}
			if state.Overall.Input != 300_000*(attempt+2) || state.Overall.Cost != 3*float64(attempt+2) {
				t.Fatalf("request boundaries or resume accounting lost: %+v", state.Overall)
			}
			return modelFailure
		})
		if !errors.Is(err, modelFailure) {
			t.Fatalf("failure swallowed: %v", err)
		}
	}
	state, err := st.Usage.Load()
	if err != nil || state.Overall.Input != 900_000 || state.Overall.Cost != 9 {
		t.Fatalf("double-counted replay after resume: state=%+v err=%v", state, err)
	}
	if state.PerAgent["architect"].Input != 900_000 || state.PerModel["codex-cli/gpt-6-astra"].Input != 900_000 {
		t.Fatalf("lost agent/model accounting: %+v", state)
	}
}

func TestPipelineOutlineAllUsageKeepsUnknownCosts(t *testing.T) {
	cfg := bootstrap.Config{OutputDir: t.TempDir()}
	err := runPipelineOutlineAllArchitectWithUsage(context.Background(), cfg, assets.Bundle{}, "operation", func(_ context.Context, _ bootstrap.Config, _ assets.Bundle, _, _ string, records ...agents.UsageRecorder) error {
		records[0]("architect_outline_all", agentcore.Message{Role: agentcore.RoleAssistant,
			Usage:    &agentcore.Usage{Provider: "codex-cli", Model: "gpt-6-astra", Input: 150_000},
			Metadata: map[string]any{"codex_usage_breakdown_complete": true, "codex_usage_breakdown": []*agentcore.Usage{{Input: 150_000}, nil}},
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	state, err := store.NewStore(cfg.OutputDir).Usage.Load()
	if err != nil || state == nil || state.Overall.Cost != 1.5 || state.MissingUsage != 1 {
		t.Fatalf("unknown failed request lost: state=%+v err=%v", state, err)
	}
}

func TestPipelineOutlineAllUsageSaveFailureReturnedAndCancels(t *testing.T) {
	cfg := bootstrap.Config{OutputDir: t.TempDir()}
	modelFailure := errors.New("original model failure")
	err := runPipelineOutlineAllArchitectWithUsage(context.Background(), cfg, assets.Bundle{}, "operation", func(ctx context.Context, _ bootstrap.Config, _ assets.Bundle, output, _ string, records ...agents.UsageRecorder) error {
		path := filepath.Join(output, "meta", "usage.json")
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(path, 0o755); err != nil {
			t.Fatal(err)
		}
		records[0]("architect_outline_all", agentcore.Message{Role: agentcore.RoleAssistant, Usage: &agentcore.Usage{Input: 1}})
		if !errors.Is(ctx.Err(), context.Canceled) {
			t.Fatal("continued model execution after usage save failed")
		}
		return modelFailure
	})
	if !errors.Is(err, modelFailure) || !strings.Contains(err.Error(), "persist usage") || !strings.Contains(err.Error(), "flush usage") {
		t.Fatalf("metering failure was swallowed: %v", err)
	}
}

func TestPipelineOutlineAllUsageRejectsCorruptSnapshotBeforeModel(t *testing.T) {
	cfg := bootstrap.Config{OutputDir: t.TempDir()}
	st := store.NewStore(cfg.OutputDir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg.OutputDir, "meta", "usage.json"), []byte("invalid JSON"), 0o600); err != nil {
		t.Fatal(err)
	}
	called := false
	err := runPipelineOutlineAllArchitectWithUsage(context.Background(), cfg, assets.Bundle{}, "operation", func(context.Context, bootstrap.Config, assets.Bundle, string, string, ...agents.UsageRecorder) error {
		called = true
		return nil
	})
	if err == nil || called {
		t.Fatalf("corrupt usage was ignored before paid model: called=%t err=%v", called, err)
	}
}
