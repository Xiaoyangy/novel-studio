package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/assets"
	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
	toolspkg "github.com/chenhongyang/novel-studio/internal/tools"
)

func TestPipelineProjectAllInputDigestUsesOnlyPlanningInputs(t *testing.T) {
	baseConfig := pipelineDigestContractConfig()
	baseBundle := pipelineDigestContractBundle()
	base := pipelineProjectAllInputDigest(baseConfig, baseBundle)

	sameCases := []struct {
		name   string
		mutate func(*bootstrap.Config, *assets.Bundle)
	}{
		{"editor role", func(cfg *bootstrap.Config, _ *assets.Bundle) {
			cfg.Roles["editor"] = bootstrap.RoleConfig{Provider: "other", Model: "editor-drift"}
		}},
		{"reviewer role", func(cfg *bootstrap.Config, _ *assets.Bundle) {
			cfg.Roles["reviewer"] = bootstrap.RoleConfig{Provider: "other", Model: "reviewer-drift"}
		}},
		{"drafter role", func(cfg *bootstrap.Config, _ *assets.Bundle) {
			cfg.Roles["drafter"] = bootstrap.RoleConfig{Provider: "other", Model: "drafter-drift"}
		}},
		{"editor prompt", func(_ *bootstrap.Config, bundle *assets.Bundle) {
			bundle.Prompts.Editor += "\neditor drift"
		}},
		{"drafter prompt", func(_ *bootstrap.Config, bundle *assets.Bundle) {
			bundle.Prompts.Drafter += "\ndrafter drift"
		}},
	}
	for _, tc := range sameCases {
		t.Run("ignores "+tc.name, func(t *testing.T) {
			cfg, bundle := clonePipelineDigestContractInputs(baseConfig, baseBundle)
			tc.mutate(&cfg, &bundle)
			if got := pipelineProjectAllInputDigest(cfg, bundle); got != base {
				t.Fatalf("%s unexpectedly invalidated project-all: got=%s want=%s", tc.name, got, base)
			}
		})
	}

	changedCases := []struct {
		name   string
		mutate func(*bootstrap.Config, *assets.Bundle)
	}{
		{"writer role", func(cfg *bootstrap.Config, _ *assets.Bundle) {
			cfg.Roles["writer"] = bootstrap.RoleConfig{Provider: "openai", Model: "writer-v2", ReasoningEffort: "high"}
		}},
		{"planner prompt", func(_ *bootstrap.Config, bundle *assets.Bundle) {
			bundle.Prompts.Planner += "\nplanner drift"
		}},
		{"reference", func(_ *bootstrap.Config, bundle *assets.Bundle) {
			bundle.References.AntiAITone += "\nreference drift"
		}},
		{"style", func(_ *bootstrap.Config, bundle *assets.Bundle) {
			bundle.Styles["default"] += "\nstyle drift"
		}},
		{"writer context window", func(cfg *bootstrap.Config, _ *assets.Bundle) {
			cfg.ContextWindows["writer-v1"] = 131072
		}},
	}
	for _, tc := range changedCases {
		t.Run("binds "+tc.name, func(t *testing.T) {
			cfg, bundle := clonePipelineDigestContractInputs(baseConfig, baseBundle)
			tc.mutate(&cfg, &bundle)
			if got := pipelineProjectAllInputDigest(cfg, bundle); got == base {
				t.Fatalf("%s drift did not invalidate project-all digest", tc.name)
			}
		})
	}
}

func TestPipelineRenderInputDigestUsesOnlyProseInputs(t *testing.T) {
	baseConfig := pipelineDigestContractConfig()
	baseBundle := pipelineDigestContractBundle()
	base := pipelineRenderInputDigest(baseConfig, baseBundle)

	sameCases := []struct {
		name   string
		mutate func(*bootstrap.Config, *assets.Bundle)
	}{
		{"editor role", func(cfg *bootstrap.Config, _ *assets.Bundle) {
			cfg.Roles["editor"] = bootstrap.RoleConfig{Provider: "other", Model: "editor-drift"}
		}},
		{"planner role", func(cfg *bootstrap.Config, _ *assets.Bundle) {
			cfg.Roles["writer"] = bootstrap.RoleConfig{Provider: "other", Model: "planner-drift"}
		}},
		{"planner prompt", func(_ *bootstrap.Config, bundle *assets.Bundle) {
			bundle.Prompts.Planner += "\nplanner drift"
		}},
		{"editor prompt", func(_ *bootstrap.Config, bundle *assets.Bundle) {
			bundle.Prompts.Editor += "\neditor drift"
		}},
	}
	for _, tc := range sameCases {
		t.Run("ignores "+tc.name, func(t *testing.T) {
			cfg, bundle := clonePipelineDigestContractInputs(baseConfig, baseBundle)
			tc.mutate(&cfg, &bundle)
			if got := pipelineRenderInputDigest(cfg, bundle); got != base {
				t.Fatalf("%s unexpectedly invalidated render: got=%s want=%s", tc.name, got, base)
			}
		})
	}

	changedCases := []struct {
		name   string
		mutate func(*bootstrap.Config, *assets.Bundle)
	}{
		{"drafter model", func(cfg *bootstrap.Config, _ *assets.Bundle) {
			cfg.Roles["drafter"] = bootstrap.RoleConfig{Provider: "openai", Model: "drafter-v2", ReasoningEffort: "high"}
		}},
		{"reviewer model", func(cfg *bootstrap.Config, _ *assets.Bundle) {
			cfg.Roles["reviewer"] = bootstrap.RoleConfig{Provider: "other", Model: "reviewer-drift"}
		}},
		{"coordinator model", func(cfg *bootstrap.Config, _ *assets.Bundle) {
			cfg.Roles["coordinator"] = bootstrap.RoleConfig{Provider: "other", Model: "coordinator-drift"}
		}},
		{"drafter prompt", func(_ *bootstrap.Config, bundle *assets.Bundle) {
			bundle.Prompts.Drafter += "\ndrafter drift"
		}},
		{"selected style", func(_ *bootstrap.Config, bundle *assets.Bundle) {
			bundle.Styles["default"] += "\nstyle drift"
		}},
		{"drafter context window", func(cfg *bootstrap.Config, _ *assets.Bundle) {
			cfg.ContextWindows["drafter-v1"] = 98304
		}},
	}
	for _, tc := range changedCases {
		t.Run("binds "+tc.name, func(t *testing.T) {
			cfg, bundle := clonePipelineDigestContractInputs(baseConfig, baseBundle)
			tc.mutate(&cfg, &bundle)
			if got := pipelineRenderInputDigest(cfg, bundle); got == base {
				t.Fatalf("%s drift did not invalidate render digest", tc.name)
			}
		})
	}
}

func TestProjectAllWorkspaceManifestReuseTamperAndRestartRetirement(t *testing.T) {
	t.Run("exact manifest is reused", func(t *testing.T) {
		runRoot := t.TempDir()
		live := filepath.Join(runRoot, "output", "novel")
		const generationID = "pg2_manifest_reuse"
		const baseChapter = 3
		workspace := pipelineProjectAllWorkspacePath(live, generationID)
		if err := os.MkdirAll(workspace, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := savePipelineProjectAllWorkspaceManifest(workspace, live, generationID, baseChapter); err != nil {
			t.Fatal(err)
		}
		sentinel := filepath.Join(workspace, "reuse-sentinel")
		if err := os.WriteFile(sentinel, []byte("same workspace"), 0o644); err != nil {
			t.Fatal(err)
		}
		got, err := preparePipelineProjectAllWorkspace(live, generationID, baseChapter, false)
		if err != nil {
			t.Fatal(err)
		}
		if got != workspace {
			t.Fatalf("reused workspace=%s want=%s", got, workspace)
		}
		if body, err := os.ReadFile(sentinel); err != nil || string(body) != "same workspace" {
			t.Fatalf("exact reuse recreated or mutated workspace: body=%q err=%v", body, err)
		}
	})

	for _, field := range []string{"generation", "source", "base"} {
		t.Run("tampered "+field+" is rejected", func(t *testing.T) {
			runRoot := t.TempDir()
			live := filepath.Join(runRoot, "output", "novel")
			const generationID = "pg2_manifest_tamper"
			const baseChapter = 2
			workspace := pipelineProjectAllWorkspacePath(live, generationID)
			if err := os.MkdirAll(workspace, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := savePipelineProjectAllWorkspaceManifest(workspace, live, generationID, baseChapter); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(workspace, filepath.FromSlash(pipelineProjectAllWorkspaceManifestPath))
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var manifest pipelineProjectAllWorkspaceManifest
			if err := json.Unmarshal(raw, &manifest); err != nil {
				t.Fatal(err)
			}
			switch field {
			case "generation":
				manifest.GenerationID = "pg2_other"
			case "source":
				manifest.SourceOutput = filepath.Join(runRoot, "other-output")
			case "base":
				manifest.BaseChapter++
			}
			raw, _ = json.MarshalIndent(manifest, "", "  ")
			if err := os.WriteFile(path, raw, 0o644); err != nil {
				t.Fatal(err)
			}
			_, err = preparePipelineProjectAllWorkspace(live, generationID, baseChapter, false)
			if err == nil ||
				!strings.Contains(err.Error(), "workspace identity invalid") ||
				!strings.Contains(err.Error(), "--restart") {
				t.Fatalf("tampered %s manifest was not rejected precisely: %v", field, err)
			}
		})
	}

	t.Run("restart retires old generation workspace before rebuilding", func(t *testing.T) {
		runRoot := t.TempDir()
		live := filepath.Join(runRoot, "output", "novel")
		initializeProjectAllWorkspaceContractLive(t, live)
		const generationID = "pg2_manifest_restart"
		const baseChapter = 0
		workspace := pipelineProjectAllWorkspacePath(live, generationID)
		if err := os.MkdirAll(workspace, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := savePipelineProjectAllWorkspaceManifest(workspace, live, generationID, baseChapter); err != nil {
			t.Fatal(err)
		}
		oldSentinel := filepath.Join(workspace, "old-workspace-sentinel")
		if err := os.WriteFile(oldSentinel, []byte("must be retired"), 0o644); err != nil {
			t.Fatal(err)
		}
		got, err := preparePipelineProjectAllWorkspace(live, generationID, baseChapter, true)
		if err != nil {
			t.Fatal(err)
		}
		if got != workspace {
			t.Fatalf("rebuilt workspace=%s want=%s", got, workspace)
		}
		if _, err := os.Stat(oldSentinel); !os.IsNotExist(err) {
			t.Fatalf("old sentinel survived in rebuilt workspace: %v", err)
		}
		parent := filepath.Dir(filepath.Dir(filepath.Dir(workspace)))
		entries, err := os.ReadDir(parent)
		if err != nil {
			t.Fatal(err)
		}
		retired := ""
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), "retired-"+generationID+"-") {
				retired = filepath.Join(parent, entry.Name())
				break
			}
		}
		if retired == "" {
			t.Fatalf("restart did not leave a retired generation under %s", parent)
		}
		retiredSentinel := filepath.Join(retired, "output", "novel", "old-workspace-sentinel")
		if body, err := os.ReadFile(retiredSentinel); err != nil || string(body) != "must be retired" {
			t.Fatalf("retired workspace evidence missing: body=%q err=%v", body, err)
		}
	})
}

func pipelineDigestContractConfig() bootstrap.Config {
	return bootstrap.Config{
		Provider:        "openai",
		ModelName:       "default-v1",
		ReasoningEffort: "medium",
		Style:           "default",
		Roles: map[string]bootstrap.RoleConfig{
			"writer":   {Provider: "openai", Model: "writer-v1", ReasoningEffort: "high"},
			"drafter":  {Provider: "openai", Model: "drafter-v1", ReasoningEffort: "high"},
			"editor":   {Provider: "deepseek", Model: "editor-v1"},
			"reviewer": {Provider: "deepseek", Model: "reviewer-v1"},
		},
		ContextWindows: map[string]int{
			"writer-v1":   262144,
			"drafter-v1":  196608,
			"editor-v1":   65536,
			"reviewer-v1": 65536,
		},
	}
}

func pipelineDigestContractBundle() assets.Bundle {
	return assets.Bundle{
		Prompts: assets.Prompts{
			Planner: "planner-v1",
			Writer:  "writer-v1",
			Drafter: "drafter-v1",
			Editor:  "editor-v1",
		},
		References: toolspkg.References{AntiAITone: "reference-v1"},
		Styles: map[string]string{
			"default": "default-style-v1",
			"other":   "other-style-v1",
		},
	}
}

func clonePipelineDigestContractInputs(
	cfg bootstrap.Config,
	bundle assets.Bundle,
) (bootstrap.Config, assets.Bundle) {
	clonedConfig := cfg
	clonedConfig.Roles = make(map[string]bootstrap.RoleConfig, len(cfg.Roles))
	for role, value := range cfg.Roles {
		value.Fallbacks = append([]bootstrap.ModelRef(nil), value.Fallbacks...)
		clonedConfig.Roles[role] = value
	}
	clonedConfig.ContextWindows = make(map[string]int, len(cfg.ContextWindows))
	for model, window := range cfg.ContextWindows {
		clonedConfig.ContextWindows[model] = window
	}
	clonedBundle := bundle
	clonedBundle.Styles = make(map[string]string, len(bundle.Styles))
	for name, body := range bundle.Styles {
		clonedBundle.Styles[name] = body
	}
	return clonedConfig, clonedBundle
}

func initializeProjectAllWorkspaceContractLive(t *testing.T, live string) {
	t.Helper()
	st := store.NewStore(live)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init("workspace restart", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.Outline.SaveLayeredOutline([]domain.VolumeOutline{{
		Index: 1,
		Title: "第一卷",
		Arcs: []domain.ArcOutline{{
			Index: 1,
			Title: "开篇",
			Chapters: []domain.OutlineEntry{{
				Chapter:   1,
				Title:     "第一章",
				CoreEvent: "角色作出第一个选择",
				Hook:      "代价即将抵达",
			}},
		}},
	}}); err != nil {
		t.Fatal(err)
	}
}
