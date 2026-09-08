package store

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

// LoadAcceptedCharacterAgentBundle reads the last accepted physical baseline
// from its immutable outcome and sealed bundle. The live simulation supplies
// only a lookup identity: neither completed progress nor projected sidecars
// prove acceptance. Callers must hold their normal pipeline execution lease.
// No model, migration, memory publication or artifact rewrite occurs here.
func (s *Store) LoadAcceptedCharacterAgentBundle(chapter int) (*domain.ProjectedChapterBundle, error) {
	if chapter <= 0 {
		return nil, fmt.Errorf("accepted character baseline requires a positive chapter")
	}
	progress, err := s.Progress.Load()
	if err != nil {
		return nil, err
	}
	if progress == nil || !slices.Contains(progress.CompletedChapters, chapter) {
		return nil, fmt.Errorf("chapter %d has no completed canonical body", chapter)
	}
	projected := s.ProjectedV2()
	cursor, err := projected.LoadRealizationCursor()
	if err != nil {
		return nil, err
	}
	if cursor == nil || cursor.LastAcceptedChapter != chapter || cursor.LastOutcomeReceiptDigest == "" {
		return nil, fmt.Errorf("chapter %d has no exact last-accepted outcome cursor", chapter)
	}
	simulation, err := s.LoadChapterWorldSimulation(chapter)
	if err != nil {
		return nil, err
	}
	if simulation == nil || simulation.CharacterAgentProtocol == nil || simulation.CharacterAgentProtocol.Version != domain.CharacterAgentDecisionProtocolV2Version {
		return nil, fmt.Errorf("chapter %d has no v2 accepted simulation identity", chapter)
	}
	outcome, err := projected.LoadActualOutcomeReceipt(simulation.GenerationID, chapter, cursor.LastOutcomeReceiptDigest)
	if err != nil {
		return nil, fmt.Errorf("accepted physical outcome: %w", err)
	}
	if outcome == nil || !outcome.ProjectionMatch {
		return nil, fmt.Errorf("chapter %d simulation is not bound to its accepted outcome", chapter)
	}
	bundle, err := withProjectedReadResult(projected, func() (*domain.ProjectedChapterBundle, error) {
		return projected.loadBundleAtUnlocked(projectedSealedGenerationPath(outcome.GenerationID), chapter)
	})
	if err != nil {
		return nil, err
	}
	if bundle == nil || !bundle.HasCharacterEvidence() || bundle.ChapterWorldSimulation.PhysicalState == nil {
		return nil, fmt.Errorf("accepted chapter %d lacks sealed character physical evidence", chapter)
	}
	if err := domain.ValidateProjectedChapterBundle(*bundle); err != nil {
		return nil, err
	}
	want, err := domain.DeterministicPlanningHash(bundle.ChapterWorldSimulation)
	if err != nil {
		return nil, err
	}
	got, err := domain.DeterministicPlanningHash(*simulation)
	if err != nil {
		return nil, err
	}
	if want != got {
		return nil, fmt.Errorf("live physical simulation differs from the accepted sealed bundle")
	}
	actualDigest, err := domain.ComputeProjectedDeltaV2Digest(outcome.ActualDelta)
	if err != nil {
		return nil, err
	}
	projectedDigest, err := domain.ComputeProjectedDeltaV2Digest(bundle.ProjectedDelta)
	if err != nil {
		return nil, err
	}
	if actualDigest != projectedDigest || outcome.ActualPostStateRoot != bundle.ProjectedPostStateRoot {
		return nil, fmt.Errorf("accepted actual state differs from the physical sealed projection")
	}
	path := filepath.Join(s.Dir(), "chapters", fmt.Sprintf("%02d.md", chapter))
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("accepted chapter body must be a regular file")
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(body)
	bodySHA := "sha256:" + hex.EncodeToString(sum[:])
	if bodySHA != outcome.ChapterBodySHA256 {
		return nil, fmt.Errorf("accepted physical baseline body SHA differs from its outcome")
	}
	// A fresh read avoids trusting a Store's checkpoint cache from before a
	// different writer completed the chapter.
	commit := NewCheckpointStore(newIO(s.Dir())).LatestByStep(domain.ChapterScope(chapter), "commit")
	if commit == nil || commit.Seq != outcome.CommitCheckpointSeq || commit.Digest != bodySHA {
		return nil, fmt.Errorf("accepted physical baseline lacks its exact current commit")
	}
	after, err := projected.LoadRealizationCursor()
	if err != nil {
		return nil, err
	}
	if after == nil || after.CursorDigest != cursor.CursorDigest {
		return nil, fmt.Errorf("accepted physical baseline changed while being read")
	}
	return bundle, nil
}
