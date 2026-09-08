package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

// ValidateRebasedChapterZeroFoundationRefresh verifies the read-only evidence
// behind an explicit rebase's new chapter-zero generation. A generation name
// or a bare marker alone never grants permission to rewrite foundation.
func (s *Store) ValidateRebasedChapterZeroFoundationRefresh() error {
	p, err := s.Progress.Load()
	if err != nil {
		return err
	}
	if p == nil || strings.TrimSpace(p.GenerationID) == "" || p.GenerationMode != domain.GenerationModeSimulationRestartFromSeed ||
		(p.Phase != domain.PhaseInit && p.Phase != domain.PhasePremise && p.Phase != domain.PhaseOutline && p.Phase != domain.PhaseWriting) ||
		p.LatestCompleted() != 0 || len(p.CompletedChapters) != 0 || p.TotalWordCount != 0 || len(p.ChapterWordCounts) != 0 ||
		len(p.PendingRewrites) != 0 || p.RewriteReason != "" || p.CurrentChapter < 0 || p.CurrentChapter > 1 || p.InProgressChapter < 0 || p.InProgressChapter > 1 ||
		len(p.CompletedScenes) != 0 || p.ReopenedFromComplete || len(p.StrandHistory) != 0 || len(p.HookHistory) != 0 ||
		(p.Flow != "" && p.Flow != domain.FlowWriting) || (p.POV != nil && len(p.POV.History) != 0) {
		return fmt.Errorf("rebase foundation refresh requires a clean chapter-zero restart generation")
	}
	var receipt struct {
		Version          string `json:"version"`
		SourceOutput     string `json:"source_output"`
		SourceRoot       string `json:"source_root"`
		ArchiveOutput    string `json:"archive_output"`
		ArchiveRoot      string `json:"archive_root"`
		PreviousProgress string `json:"previous_progress"`
		NewGenerationID  string `json:"new_generation_id"`
		RebasedAt        string `json:"rebased_at"`
	}
	marker := filepath.Join(s.dir, "meta", "all_chapter_rebase.json")
	info, err := os.Lstat(marker)
	if err != nil {
		return fmt.Errorf("read verified chapter-zero rebase receipt: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("chapter-zero rebase receipt must be a regular file")
	}
	raw, err := os.ReadFile(marker)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, &receipt); err != nil {
		return fmt.Errorf("decode chapter-zero rebase receipt: %w", err)
	}
	if receipt.Version != "pipeline-all-chapter-rebase.v1" || receipt.NewGenerationID != p.GenerationID || receipt.SourceRoot != receipt.ArchiveRoot {
		return fmt.Errorf("chapter-zero rebase receipt version/generation/source-archive binding is invalid")
	}
	if err := validateDirectoryPublishDigest("rebase archive root", receipt.ArchiveRoot); err != nil {
		return err
	}
	rebasedAt, err := time.Parse(time.RFC3339Nano, receipt.RebasedAt)
	if err != nil || rebasedAt.IsZero() || rebasedAt.After(time.Now().Add(5*time.Minute)) {
		return fmt.Errorf("chapter-zero rebase receipt has an invalid rebase time")
	}
	resolve := func(path string) (string, error) {
		if strings.TrimSpace(path) == "" || !filepath.IsAbs(path) {
			return "", fmt.Errorf("rebase receipt paths must be absolute")
		}
		return filepath.EvalSymlinks(filepath.Clean(path))
	}
	livePath, err := filepath.Abs(s.dir)
	if err != nil {
		return err
	}
	live, err := resolve(livePath)
	if err != nil {
		return err
	}
	source, err := resolve(receipt.SourceOutput)
	if err != nil || source != live {
		return fmt.Errorf("chapter-zero rebase receipt belongs to another source output")
	}
	archive, err := resolve(receipt.ArchiveOutput)
	if err != nil {
		return fmt.Errorf("resolve chapter-zero rebase archive: %w", err)
	}
	runRoot := filepath.Dir(filepath.Dir(live))
	rel, err := filepath.Rel(filepath.Join(runRoot, "archives"), archive)
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if err != nil || len(parts) != 3 || !strings.HasPrefix(parts[0], "sealed-rebase-") || parts[1] != "output" || parts[2] != "novel" {
		return fmt.Errorf("chapter-zero rebase archive is outside this run's archive scope")
	}
	previous, err := resolve(receipt.PreviousProgress)
	if err != nil || previous != filepath.Join(archive, "meta", "progress.json") {
		return fmt.Errorf("chapter-zero rebase previous progress is outside its archive")
	}
	actual, err := DirectoryContentRoot(archive)
	if err != nil || actual != receipt.ArchiveRoot {
		return fmt.Errorf("chapter-zero rebase archive content root mismatch: %v", err)
	}
	if old, err := NewStore(archive).Progress.Load(); err != nil || old == nil || old.GenerationID == p.GenerationID {
		return fmt.Errorf("chapter-zero rebase archive does not prove a distinct prior progress epoch: %v", err)
	}
	if err := s.ValidateOutlineAllChapterZeroWorkspace(); err != nil {
		return err
	}
	if info, err := os.Lstat(filepath.Join(s.dir, writingPipelineModePath)); err == nil {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("chapter-zero rebase writing-mode receipt must be a regular file")
		}
		mode, err := s.LoadWritingPipelineMode()
		if err != nil || mode == nil || mode.Mode != domain.WritingPipelineModeSealedTwoPassV2 {
			return fmt.Errorf("chapter-zero rebase writing-mode intent is invalid: %v", err)
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	for _, rel := range []string{
		"meta/first_chapter_generation_readiness.json", "meta/first_chapter_generation_readiness.md",
		"meta/zero_chapter_context_manifest.json", "meta/zero_chapter_context_manifest.md", "meta/ch01_zero_init_plan.md",
		"meta/initial_character_dynamics.json", "meta/initial_character_dynamics.md", "meta/world_foundation.json", "meta/world_foundation.md",
		"meta/world_tick.json", "meta/world_events.jsonl", "meta/project_all_state.json", "meta/initial_resource_ledger.json",
		"relationship_state.initial.json", "foreshadow_ledger.initial.json",
	} {
		if _, err := os.Lstat(filepath.Join(s.dir, filepath.FromSlash(rel))); err == nil {
			return fmt.Errorf("chapter-zero rebase refresh refuses zero-init/planning evidence at %s", rel)
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	for _, rel := range []string{"chapters", "drafts", "summaries", "reviews", "reviews_ai", "meta/planning",
		"meta/chapter_simulations", "meta/chapter_world_deltas", "meta/character_stage", "meta/characters",
		"meta/character_agents/memory", "meta/character_agents/projected", "meta/character_agents/successors"} {
		root := filepath.Join(s.dir, filepath.FromSlash(rel))
		err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !entry.IsDir() {
				// Explicit rebase installs this verified route intent before
				// Architect refresh; it is not a chapter planning artifact.
				if path == filepath.Join(s.dir, writingPipelineModePath) && entry.Type().IsRegular() {
					return nil
				}
				return fmt.Errorf("chapter-zero rebase refresh refuses downstream artifact %s", path)
			}
			return nil
		})
		if err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	checkpoints, err := s.Checkpoints.AllStrict()
	if err != nil {
		return err
	}
	for _, checkpoint := range checkpoints {
		kind := strings.TrimPrefix(checkpoint.Step, "foundation_refresh:")
		allowed := false
		switch kind {
		case "premise", "characters", "world_rules", "book_world", "world_codex", "update_compass", "outline", "layered_outline":
			allowed = true
		}
		if !allowed || checkpoint.Scope != domain.GlobalScope() || checkpoint.OccurredAt.Before(rebasedAt) {
			return fmt.Errorf("chapter-zero rebase refresh refuses prior or downstream checkpoint %s", checkpoint.Step)
		}
	}
	return nil
}
