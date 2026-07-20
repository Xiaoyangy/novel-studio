package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func TestPipelineSealedBodyEvidenceMismatchPersistsAndSkipsFormalReview(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	snapshot := &pipelineRenderedChapterSnapshot{
		Store: st,
		Body:  "第一章\n\n候选正文没有实现冻结结果。",
	}
	matchCalls := 0
	formalCalls := 0
	err := runPipelineSealedFormalReviewAfterBodyEvidence(
		dir,
		snapshot,
		func(*pipelineRenderedChapterSnapshot) (pipelineSealedActualDeltaMatch, error) {
			matchCalls++
			return pipelineSealedActualDeltaMatch{
				ActualDelta: domain.ProjectedDelta{Version: domain.ProjectedDeltaV2Version},
				Complete:    true,
				MismatchReasons: []string{
					"timeline[claim] has no locatable semantic body evidence",
				},
			}, nil
		},
		func() error {
			formalCalls++
			return nil
		},
	)
	if !errors.Is(err, errPipelineSealedActualBodyEvidenceMismatch) {
		t.Fatalf("preflight error=%v, want deterministic mismatch", err)
	}
	if matchCalls != 1 || formalCalls != 0 {
		t.Fatalf("match calls=%d formal calls=%d, want 1/0", matchCalls, formalCalls)
	}
	raw, readErr := os.ReadFile(filepath.Join(
		dir,
		"meta",
		"planning",
		"sealed_actual_match.json",
	))
	if readErr != nil {
		t.Fatal(readErr)
	}
	var persisted pipelineSealedActualDeltaMatch
	if err := json.Unmarshal(raw, &persisted); err != nil {
		t.Fatal(err)
	}
	if persisted.ProjectionMatch || len(persisted.MismatchReasons) != 1 {
		t.Fatalf("persisted mismatch=%+v", persisted)
	}
}

func TestPipelineSealedBodyEvidencePassPreservesFormalReviewFlow(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	snapshot := &pipelineRenderedChapterSnapshot{
		Store: st,
		Body:  "第一章\n\n冻结结果已经在正文中完成。",
	}
	wantFormalErr := errors.New("formal review result")
	matchCalls := 0
	formalCalls := 0
	err := runPipelineSealedFormalReviewAfterBodyEvidence(
		dir,
		snapshot,
		func(*pipelineRenderedChapterSnapshot) (pipelineSealedActualDeltaMatch, error) {
			matchCalls++
			return pipelineSealedActualDeltaMatch{
				ActualDelta:     domain.ProjectedDelta{Version: domain.ProjectedDeltaV2Version},
				ProjectionMatch: true,
				Complete:        true,
			}, nil
		},
		func() error {
			formalCalls++
			return wantFormalErr
		},
	)
	if !errors.Is(err, wantFormalErr) {
		t.Fatalf("formal error=%v, want unchanged callback result", err)
	}
	if matchCalls != 1 || formalCalls != 1 {
		t.Fatalf("match calls=%d formal calls=%d, want 1/1", matchCalls, formalCalls)
	}
	if _, statErr := os.Stat(filepath.Join(
		dir,
		"meta",
		"planning",
		"sealed_actual_match.json",
	)); !os.IsNotExist(statErr) {
		t.Fatalf("passing preflight wrote a final receipt before formal review: %v", statErr)
	}
}
