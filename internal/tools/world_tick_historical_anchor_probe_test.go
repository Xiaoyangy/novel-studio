package tools

import (
	"os"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func TestActualBookHistoricalAnchorProbe(t *testing.T) {
	root := os.Getenv("NOVEL_ANCHOR_READONLY_SOURCE")
	if root == "" {
		t.Skip("optional read-only production-source probe")
	}
	issues := InitialWorldTickQualityIssues(store.NewStore(root))
	for _, issue := range issues {
		if strings.Contains(issue, `时间锚点 "七年"`) {
			t.Fatalf("historical backstory was incorrectly made a pending deadline: %s", issue)
		}
	}
	if len(issues) > 0 {
		t.Fatalf("unexpected independent quality issues: %v", issues)
	}
}

func TestMinimalHistoricalAnchorProbe(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Characters.Save([]domain.Character{{Name: "沈知微"}}); err != nil {
		t.Fatal(err)
	}
	if err := st.Outline.SaveLayeredOutline([]domain.VolumeOutline{{Index: 1, Arcs: []domain.ArcOutline{{Index: 1, Chapters: []domain.OutlineEntry{{Chapter: 1, CoreEvent: "沈知微的追问开始偏向七年前。"}}}}}}); err != nil {
		t.Fatal(err)
	}
	saveInitialWorldTickGateFixture(t, st, domain.WorldEvent{TickID: "v1-a1", Chapter: 0, Actors: []string{"沈知微"}, Summary: "沈知微等待会面。", VisibilityChapter: 1}, domain.WorldTick{TickID: "v1-a1", Volume: 1, Arc: 1, ThroughChapter: 0, EventCount: 1})
	issues := InitialWorldTickQualityIssues(st)
	if len(issues) > 0 {
		t.Fatalf("historical backstory is not a future deadline: %v", issues)
	}
}
