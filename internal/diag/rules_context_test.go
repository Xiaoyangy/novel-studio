package diag

import (
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

func TestCastBriefRoleMissing(t *testing.T) {
	snap := &Snapshot{
		Progress: &domain.Progress{CompletedChapters: []int{1, 2, 3, 4, 5, 6}},
		CastLedger: []domain.CastEntry{
			{Name: "老周", AppearanceCount: 2, LastSeenChapter: 4},
			{Name: "阿云", AppearanceCount: 1, LastSeenChapter: 6},
			{Name: "赵掌柜", BriefRole: "客栈掌柜", AppearanceCount: 3, LastSeenChapter: 5},
			{Name: "已升格", AppearanceCount: 4, LastSeenChapter: 6, Promoted: true},
		},
	}

	findings := CastBriefRoleMissing(snap)
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(findings))
	}
	f := findings[0]
	if f.Rule != "CastBriefRoleMissing" || f.Severity != SevInfo {
		t.Fatalf("unexpected finding: %+v", f)
	}
	if !strings.Contains(f.Evidence, "老周") || strings.Contains(f.Evidence, "阿云") || strings.Contains(f.Evidence, "已升格") {
		t.Fatalf("unexpected evidence: %s", f.Evidence)
	}
}

func TestCastBloat(t *testing.T) {
	var entries []domain.CastEntry
	for i := 1; i <= 13; i++ {
		entries = append(entries, domain.CastEntry{
			Name:               "配角" + string(rune('A'+i-1)),
			BriefRole:          "过场角色",
			AppearanceCount:    1,
			LastSeenChapter:    i,
			AppearanceChapters: []int{i},
		})
	}
	snap := &Snapshot{
		Progress:   &domain.Progress{CompletedChapters: []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}},
		CastLedger: entries,
	}

	findings := CastBloat(snap)
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(findings))
	}
	f := findings[0]
	if f.Rule != "CastBloat" || f.Severity != SevWarning {
		t.Fatalf("unexpected finding: %+v", f)
	}
	if !strings.Contains(f.Evidence, "ratio=1.30") || !strings.Contains(f.Evidence, "threshold=8") {
		t.Fatalf("unexpected evidence: %s", f.Evidence)
	}
}

func TestCastPromotionCandidate(t *testing.T) {
	snap := &Snapshot{
		Progress: &domain.Progress{CompletedChapters: []int{1, 2, 3, 4, 5, 6}},
		CastLedger: []domain.CastEntry{
			{Name: "老周", BriefRole: "客栈老板", AppearanceCount: 5, LastSeenChapter: 6},
			{Name: "阿云", BriefRole: "客栈小厮", AppearanceCount: 4, LastSeenChapter: 6},
			{Name: "已升格", BriefRole: "同伴", AppearanceCount: 8, LastSeenChapter: 6, Promoted: true},
		},
	}

	findings := CastPromotionCandidate(snap)
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(findings))
	}
	f := findings[0]
	if f.Rule != "CastPromotionCandidate" || f.Severity != SevInfo {
		t.Fatalf("unexpected finding: %+v", f)
	}
	if !strings.Contains(f.Evidence, "老周") || strings.Contains(f.Evidence, "阿云") || strings.Contains(f.Evidence, "已升格") {
		t.Fatalf("unexpected evidence: %s", f.Evidence)
	}
}
