package tools

import (
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func TestEnsureChapterExpandedUsesReservedSkeletonChapterNumbers(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := st.Progress.Init("test", 7); err != nil {
		t.Fatalf("Progress.Init: %v", err)
	}
	if err := st.Outline.SaveLayeredOutline([]domain.VolumeOutline{{
		Index: 1,
		Arcs: []domain.ArcOutline{
			{
				Index: 1,
				Chapters: []domain.OutlineEntry{
					{Title: "前一"},
					{Title: "前二"},
				},
			},
			{Index: 2, EstimatedChapters: 3},
			{
				Index: 3,
				Chapters: []domain.OutlineEntry{
					{Title: "后一"},
					{Title: "后二"},
				},
			},
		},
	}}); err != nil {
		t.Fatalf("SaveLayeredOutline: %v", err)
	}
	if err := st.Progress.SetLayered(true); err != nil {
		t.Fatalf("SetLayered: %v", err)
	}
	if err := st.Progress.UpdatePhase(domain.PhaseWriting); err != nil {
		t.Fatalf("UpdatePhase: %v", err)
	}

	if err := EnsureChapterExpanded(st, 3); err == nil || !strings.Contains(err.Error(), "expand_arc") {
		t.Fatalf("reserved skeleton chapter 3 should be rejected, got %v", err)
	}
	if err := EnsureChapterExpanded(st, 6); err != nil {
		t.Fatalf("later expanded chapter 6 should keep its reserved global number: %v", err)
	}
}
