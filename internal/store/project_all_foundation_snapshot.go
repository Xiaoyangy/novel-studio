package store

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Exact legacy project-all foundation inventory and hash. Shared with CLI so
// capsule provenance never uses a second, approximately equivalent algorithm.
type ProjectAllFoundationSnapshot struct {
	Version   string            `json:"version"`
	Artifacts map[string]string `json:"artifacts"`
}

func ComputeProjectAllFoundationSnapshotRoot(value ProjectAllFoundationSnapshot) (string, error) {
	if value.Version != "project-all-foundation-snapshot.v1" {
		return "", fmt.Errorf("unsupported foundation inventory")
	}
	hash, err := domain.DeterministicPlanningHash(value)
	if err != nil {
		return "", err
	}
	return "sha256:" + strings.TrimPrefix(hash, "sha256:"), nil
}

func foundationOptionalFileSHA(outputDir, rel string) (string, error) {
	io := newIO(outputDir)
	if err := validateCharacterMemoryPublicationPath(io, rel); err != nil {
		return "", err
	}
	raw, err := os.ReadFile(io.path(rel))
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if len(raw) == 0 {
		return "", nil
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func CaptureProjectAllFoundationSnapshot(outputDir string) (ProjectAllFoundationSnapshot, string, error) {
	artifacts := make(map[string]string)
	add := func(rel string) error {
		rel = filepath.ToSlash(filepath.Clean(rel))
		digest, err := foundationOptionalFileSHA(outputDir, rel)
		if err != nil {
			return err
		}
		if digest != "" {
			artifacts[rel] = digest
		}
		return nil
	}
	for _, rel := range []string{
		"premise.md",
		"characters.json",
		"book_world.json",
		"world_codex.json",
		"world_rules.json",
		"relationship_state.initial.json",
		"relationship_state.json",
		"foreshadow_ledger.initial.json",
		"foreshadow_ledger.json",
		"timeline.json",
		"meta/compass.json",
		"meta/run.json",
		"meta/world_foundation.json",
		"meta/world_coherence_report.json",
		"meta/initial_character_dynamics.json",
		"meta/initial_resource_ledger.json",
		"meta/simulation_restart_policy.json",
		"meta/simulation_profile.json",
		"meta/crowd_role_policy.json",
		"meta/prewrite_storycraft_plan.json",
		"meta/prewrite_storycraft_plan.md",
		"references/prewrite_storycraft_plan.md",
		"meta/world_background_plan.json",
		"meta/world_background_plan.md",
		"references/world_background_plan.md",
		"meta/zero_chapter_context_manifest.json",
		"meta/resource_ledger.json",
		"meta/cast_ledger.json",
		"meta/state_changes.json",
		"meta/chapter_progress.json",
		"meta/project_progress.json",
		"meta/character_continuity.json",
		"meta/character_agents/registry.json",
		"meta/evolution_report.json",
		"meta/world_events.jsonl",
		"meta/world_tick.json",
		"meta/offscreen_agenda.json",
		"meta/story_time_contract.json",
		"meta/story_calendar.json",
		"meta/simulation_tiers.json",
		"meta/event_weave.json",
		"meta/moral_ceiling.json",
		"meta/physics_axioms.json",
		"meta/pacing_contract.json",
		"meta/social_mood.json",
		"meta/info_graph.json",
		"meta/ritual_calendar.json",
		"meta/crowd_life.json",
		"meta/ecological_map.json",
		"meta/cosmology.json",
		"meta/cultural_footnotes.json",
		"meta/user_rules.json",
		"meta/style_rules.json",
		"meta/writing_assets.json",
		"meta/web_reference_brief.json",
		"meta/web_reference_brief.md",
		"references/web_reference_brief.md",
	} {
		if err := add(rel); err != nil {
			return ProjectAllFoundationSnapshot{}, "", err
		}
	}
	for _, sourceRoot := range []struct {
		path       string
		extensions map[string]bool
	}{
		{path: "meta/characters", extensions: map[string]bool{".json": true}},
		{path: "meta/character_agents/memory", extensions: map[string]bool{".json": true}},
		{path: "meta/volume_codex", extensions: map[string]bool{".json": true}},
		{path: "meta/snapshots", extensions: map[string]bool{".json": true}},
		{path: "meta/character_stage", extensions: map[string]bool{".json": true}},
		{path: "meta/side_character_journeys", extensions: map[string]bool{".json": true}},
		{path: "meta/chapter_world_deltas", extensions: map[string]bool{".json": true}},
		{path: "reviews", extensions: map[string]bool{
			".json": true, ".jsonl": true, ".md": true,
		}},
	} {
		base := filepath.Join(outputDir, filepath.FromSlash(sourceRoot.path))
		if _, err := os.Stat(base); os.IsNotExist(err) {
			continue
		} else if err != nil {
			return ProjectAllFoundationSnapshot{}, "", err
		}
		if err := filepath.WalkDir(base, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			rel, err := filepath.Rel(outputDir, path)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			if entry.IsDir() {
				if rel == "reviews/drafts" {
					return filepath.SkipDir
				}
				return nil
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("project-all foundation snapshot refuses symlink %s", rel)
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				return fmt.Errorf("project-all foundation snapshot refuses non-regular file %s", rel)
			}
			if !sourceRoot.extensions[strings.ToLower(filepath.Ext(entry.Name()))] {
				return nil
			}
			return add(rel)
		}); err != nil {
			return ProjectAllFoundationSnapshot{}, "", err
		}
	}
	value := ProjectAllFoundationSnapshot{Version: "project-all-foundation-snapshot.v1", Artifacts: artifacts}
	root, err := ComputeProjectAllFoundationSnapshotRoot(value)
	return value, root, err
}
