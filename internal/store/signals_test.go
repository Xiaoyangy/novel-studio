package store

import (
	"encoding/json"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

func TestPendingCommitLifecycle(t *testing.T) {
	s := NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	pending := domain.PendingCommit{
		Chapter:                     3,
		Mode:                        domain.CommitModeRewrite,
		Stage:                       domain.CommitStageProgressMarked,
		Summary:                     "第3章摘要",
		Payload:                     json.RawMessage(`{"chapter":3,"summary":"第3章摘要"}`),
		PayloadSHA256:               "payload-digest",
		BodySHA256:                  "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		WordCount:                   2400,
		PlanCheckpointSeq:           17,
		PlanCheckpointDigest:        "sha256:plan",
		BodyCheckpointSeq:           18,
		BodyCheckpointDigest:        "sha256:body",
		ConsistencyCheckpointSeq:    19,
		ConsistencyCheckpointDigest: "sha256:body",
		ExternalBodySHA256:          "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		StrictAIGC:                  true,
		PreviousFinalSHA256:         "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		RewriteFlow:                 string(domain.FlowRewriting),
		StartedAt:                   "2026-03-27T10:00:00Z",
		UpdatedAt:                   "2026-03-27T10:01:00Z",
		Result: &domain.CommitResult{
			Chapter:     3,
			Committed:   true,
			WordCount:   2400,
			NextChapter: 4,
		},
	}
	if err := s.Signals.SavePendingCommit(pending); err != nil {
		t.Fatalf("SavePendingCommit: %v", err)
	}

	got, err := s.Signals.LoadPendingCommit()
	if err != nil {
		t.Fatalf("LoadPendingCommit: %v", err)
	}
	if got == nil {
		t.Fatal("expected pending commit, got nil")
	}
	if got.Chapter != 3 || got.Stage != domain.CommitStageProgressMarked {
		t.Fatalf("unexpected pending commit: %+v", got)
	}
	if got.Mode != domain.CommitModeRewrite || got.BodySHA256 != pending.BodySHA256 ||
		got.BodyCheckpointSeq != 18 || got.ConsistencyCheckpointSeq != 19 || !got.StrictAIGC ||
		got.PayloadSHA256 != pending.PayloadSHA256 || !json.Valid(got.Payload) {
		t.Fatalf("pending recovery identity did not round-trip: %+v", got)
	}
	if got.Result == nil || got.Result.NextChapter != 4 {
		t.Fatalf("unexpected pending result: %+v", got.Result)
	}

	if err := s.Signals.ClearPendingCommit(); err != nil {
		t.Fatalf("ClearPendingCommit: %v", err)
	}
	got, err = s.Signals.LoadPendingCommit()
	if err != nil {
		t.Fatalf("LoadPendingCommit after clear: %v", err)
	}
	if got != nil {
		t.Fatalf("expected pending commit cleared, got %+v", got)
	}
}
