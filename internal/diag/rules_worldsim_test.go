package diag

import (
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

func TestOffscreenWorldStaleNoArtifacts(t *testing.T) {
	snap := &Snapshot{Progress: &domain.Progress{CompletedChapters: []int{1, 2, 3}}}
	if got := OffscreenWorldStale(snap); got != nil {
		t.Fatalf("未启用世界推演的项目应零噪音: %+v", got)
	}
}

func TestOffscreenWorldStaleTickLag(t *testing.T) {
	snap := &Snapshot{
		Progress:  &domain.Progress{CompletedChapters: chapters(40)},
		WorldTick: &domain.WorldTick{TickID: "v1-a1", ThroughChapter: 12},
	}
	findings := OffscreenWorldStale(snap)
	if len(findings) != 1 || findings[0].Severity != SevWarning {
		t.Fatalf("落后 28 章应告警一条: %+v", findings)
	}
	// 未超阈值不告警
	snap.WorldTick.ThroughChapter = 30
	if got := OffscreenWorldStale(snap); got != nil {
		t.Fatalf("落后 10 章（≤%d）不应告警: %+v", ThresholdWorldTickLag, got)
	}
}

func TestOffscreenWorldStaleAgenda(t *testing.T) {
	snap := &Snapshot{
		Progress:  &domain.Progress{CompletedChapters: chapters(40)},
		WorldTick: &domain.WorldTick{ThroughChapter: 38},
		OffscreenAgenda: domain.OffscreenAgendaLedger{Agendas: []domain.CharacterAgenda{
			{Name: "沈青", CurrentGoal: "查商队", Status: "active", LastAdvancedChapter: 10},
			{Name: "老周", CurrentGoal: "守店", Status: "dormant", LastAdvancedChapter: 5},
		}},
	}
	findings := OffscreenWorldStale(snap)
	if len(findings) != 1 || findings[0].Severity != SevInfo {
		t.Fatalf("仅 active 停滞日程应产出一条 info: %+v", findings)
	}
}

func chapters(n int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = i + 1
	}
	return out
}
