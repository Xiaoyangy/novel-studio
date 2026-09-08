package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/chenhongyang/novel-studio/internal/agents"
	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/host"
	"github.com/chenhongyang/novel-studio/internal/store"
)

// These recovery runners operate only on the promoted live book. Candidate
// callers must instead receive an accounting capability from their validated
// live parent; never start a fresh candidate-local meter that publication loses.
func newPipelineSealedConvergenceUsage(cfg bootstrap.Config, st *store.Store, intent *pipelineSealedConvergenceReplanIntent) (context.Context, *host.ScopedUsageAccounting, error) {
	if st == nil || intent == nil || cfg.OutputDir == "" || filepath.Clean(cfg.OutputDir) != filepath.Clean(st.Dir()) {
		return nil, nil, fmt.Errorf("sealed convergence usage root differs from validated live output")
	}
	if err := validatePipelineSealedConvergenceUsageRoot(st); err != nil {
		return nil, nil, err
	}
	if _, err := pipelineSealedConvergenceCurrentReplacementLock(st, intent.SourceFrozen.Chapter); err != nil {
		return nil, nil, err
	}
	if _, err := validatePipelineSealedConvergenceReplanIntent(st.Dir(), *intent); err != nil {
		return nil, nil, fmt.Errorf("sealed convergence usage live binding: %w", err)
	}
	scope, err := host.NewScopedUsageAccounting(context.Background(), st, intent.SourceFrozen.PlanningGenerationID, cfg.Budget)
	if err != nil {
		return nil, nil, err
	}
	ctx := agents.WithSealedConvergenceUsageAccounting(scope.Context(), agents.SealedConvergenceUsageAccounting{ExecutionOutputDir: st.Dir(), Decorate: scope.Decorate, RecordUsage: scope.Record})
	return ctx, scope, nil
}

func validatePipelineSealedConvergenceUsageRoot(st *store.Store) error {
	if st == nil || !filepath.IsAbs(st.Dir()) {
		return fmt.Errorf("sealed convergence usage requires absolute live root")
	}
	// Project-all shadows are self-contained stores but not authoritative cost
	// owners. Their marker is never copied to a published book.
	if _, err := os.Lstat(filepath.Join(st.Dir(), filepath.FromSlash(pipelineProjectAllWorkspaceManifestPath))); err == nil {
		return fmt.Errorf("sealed convergence usage refuses project-all shadow; pass verified live accounting")
	} else if !os.IsNotExist(err) {
		return err
	}
	manifest, err := loadPipelineRenderCandidateManifest(st.Dir())
	if err != nil {
		return err
	}
	if manifest != nil {
		_, isolated, err := resolvePipelineRenderCandidateSourceOutput(st.Dir(), manifest)
		if err != nil {
			return err
		}
		if isolated || !pipelineRenderConvergenceManifestIsPublishedLive(st, manifest) {
			return fmt.Errorf("sealed convergence usage refuses isolated render candidate; pass verified live accounting")
		}
	}
	return nil
}
