package main

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/rules"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/userrules"
)

// Capture before the initial Host sees its formatted UserRulesPrompt. Existing
// books without a catalog remain legacy; their normalization is not migration
// authority. Explicit repairs initialize only their isolated candidate.
func ensurePipelineArchitectAuthorSources(outputDir, originalPrompt string) error {
	st := store.NewStore(outputDir)
	catalog, err := st.LoadAuthorSources()
	if err != nil {
		return err
	}
	if catalog != nil {
		return nil
	}
	current, err := st.UserRules.Load()
	if err != nil {
		return err
	}
	if current != nil {
		return nil
	}
	progress, err := st.Progress.Load()
	if err != nil {
		return err
	}
	if progress != nil && (progress.LatestCompleted() > 0 || progress.GenerationID != "" || progress.TotalWordCount > 0) {
		return nil
	}
	for _, rel := range []string{"meta/planning/v2", "meta/first_chapter_generation_readiness.json", "meta/runtime/chapter_delivery/ledger.json"} {
		if _, err := os.Lstat(filepath.Join(outputDir, rel)); err == nil {
			return nil
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	if _, err := os.Lstat(filepath.Join(outputDir, "meta/compass.json")); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	brainstorm, err := os.ReadFile(filepath.Join(pipelineRebaseRunRoot(outputDir), "brainstorm.md"))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if strings.TrimSpace(originalPrompt) == "" && strings.TrimSpace(string(brainstorm)) == "" {
		return nil
	}
	value, err := userrules.BuildAuthorSourceCatalog(originalPrompt, rules.DefaultOptions())
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(brainstorm)) != "" {
		value.Digest = ""
		value.Sources = append(value.Sources, domain.AuthorSourceV1{ID: "brainstorm", Text: string(brainstorm)})
		value, err = domain.FinalizeAuthorSourcesV1(value)
		if err != nil {
			return err
		}
	}
	return st.SaveAuthorSources(value)
}

// Call only after Store.LoadCompass authenticated any source-bound mode.
func pipelineCompassHasAuthorContractBoundary(compass *domain.StoryCompass) bool {
	return compass != nil && (compass.AuthorContracts != nil || len(compass.NonNegotiables) > 0)
}

func copyPipelineCompassAuthorContracts(binding *domain.CompassAuthorContractsV1) *domain.CompassAuthorContractsV1 {
	if binding == nil {
		return nil
	}
	owned := *binding
	owned.Refs = append([]domain.AuthorSourceParagraphRefV1(nil), binding.Refs...)
	return &owned
}
