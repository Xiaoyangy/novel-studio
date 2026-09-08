package main

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func TestSealedCountdownTimeUsesMinutesAndIgnoresTravelDuration(t *testing.T) {
	receipt := domain.OutlineAllExecutionReceipt{EstimatedScale: "1-1卷、3-3章", NonNegotiables: []string{
		"现实与时限合同｜建立章位：第1章；适用范围：全书。开局距最后期限九十分钟；正文不跨过截止时点。",
		"全书倒计时90分钟，单次步行耗时5分钟。",
		"主人公九年前离开；工具每次需要1小时充电。",
	}}
	hint, err := zeroOutlineAllReceiptStoryTimeHint(receipt)
	if err != nil || hint != "0.0625日" {
		t.Fatalf("countdown contract=%q err=%v", hint, err)
	}
	contract, err := domain.DeriveStoryTimeContract(receipt.EstimatedScale+"；主线时间跨度"+hint, 3)
	if err != nil || contract.DurationDaysMax != 0.0625 || math.Abs(contract.NominalDaysPerChapter-1.0/48) > 1e-12 {
		t.Fatalf("minute countdown became multi-day: %+v %v", contract, err)
	}
}

func TestSealedStoryTimeRejectsConflictingOrUnsupportedExplicitHints(t *testing.T) {
	for _, hint := range []string{"90分钟；2小时", "本书在不明时间内发生"} {
		if _, err := zeroOutlineAllReceiptStoryTimeHint(domain.OutlineAllExecutionReceipt{StoryTimeHint: hint}); err == nil {
			t.Fatalf("unresolved explicit hint silently used default: %q", hint)
		}
	}
}

func sealedCountdownStore(t *testing.T) *store.Store {
	t.Helper()
	dir := outlineAllGateLiveDir(t)
	receipt := writeOutlineAllGateCompleteReceipt(t, dir, filepath.Join(t.TempDir(), "candidate", "output"), outlineAllGateDigest)
	st := store.NewStore(dir)
	compass, err := st.Outline.LoadCompass()
	if err != nil {
		t.Fatal(err)
	}
	compass.EstimatedScale = "1-1卷、3-3章"
	compass.NonNegotiables = []string{"现实与时限合同：开局距最后截止时点90分钟；适用范围为全书。"}
	if err := st.Outline.SaveCompass(*compass); err != nil {
		t.Fatal(err)
	}
	volumes, err := st.Outline.LoadLayeredOutline()
	if err != nil {
		t.Fatal(err)
	}
	volumes = volumes[:1]
	volumes[0].Arcs = volumes[0].Arcs[:1]
	volumes[0].Arcs[0].Chapters = volumes[0].Arcs[0].Chapters[:3]
	volumes[0].Arcs[0].EstimatedChapters = 3
	if err := st.Outline.SaveLayeredOutline(volumes); err != nil {
		t.Fatal(err)
	}
	flat := domain.FlattenOutline(volumes)
	if err := st.Outline.SaveOutline(flat); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Save(&domain.Progress{TotalChapters: 3}); err != nil {
		t.Fatal(err)
	}
	receipt.EstimatedScale, receipt.NonNegotiables = compass.EstimatedScale, compass.NonNegotiables
	receipt.MinChapters, receipt.MaxChapters, receipt.TargetChapters = 3, 3, 3
	receipt.CompassDigest, err = domain.ComputeStoryCompassDigest(*compass)
	if err != nil {
		t.Fatal(err)
	}
	receipt.FinalLayeredDigest, err = domain.ComputeLayeredOutlineDigest(volumes)
	if err != nil {
		t.Fatal(err)
	}
	receipt.FinalFlatDigest, err = domain.ComputeFlatOutlineDigest(flat)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err = domain.SignOutlineAllExecutionReceipt(receipt)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SaveOutlineAllExecutionReceipt(receipt); err != nil {
		t.Fatal(err)
	}
	return st
}

func TestZeroStoryTimeDerivesSealedSubdayContract(t *testing.T) {
	st := sealedCountdownStore(t)
	contract, published, err := zeroEnsureStoryTimeContract(st, nil)
	if err != nil || published || contract == nil || contract.DurationDaysMax != 0.0625 {
		t.Fatalf("fresh subday contract=%+v published=%v err=%v", contract, published, err)
	}
	if err := zeroSyncStoryCalendar(st, *contract); err != nil {
		t.Fatal(err)
	}
	if issues := zeroCheckStoryTimeContract(st.Dir()); len(issues) != 0 {
		t.Fatalf("consistent countdown rejected: %v", issues)
	}
	calendar, err := st.WorldSim.LoadStoryCalendar()
	if err != nil || math.Abs(calendar.DaysPerChapter-1.0/48) > 1e-12 {
		t.Fatalf("calendar failed to preserve minutes: %+v %v", calendar, err)
	}
}

func TestZeroStoryTimeRejectsFrozenNominalDriftWithoutRewriting(t *testing.T) {
	st := sealedCountdownStore(t)
	old, err := domain.DeriveStoryTimeContract("1-1卷、3-3章", 3)
	if err != nil {
		t.Fatal(err)
	}
	old.Source = domain.StoryTimeSourceOutlineAll
	if err := st.WorldSim.SaveStoryTimeContract(old); err != nil {
		t.Fatal(err)
	}
	if err := st.WorldSim.SaveStoryCalendar(domain.StoryCalendar{DaysPerChapter: 2}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(st.Dir(), "meta", "story_time_contract.json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := zeroEnsureStoryTimeContract(st, nil); err == nil || !strings.Contains(err.Error(), "successor/rebase") {
		t.Fatalf("frozen wrong time silently rewritten: %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil || string(before) != string(after) {
		t.Fatalf("frozen contract changed: %v", err)
	}
	issues := zeroCheckStoryTimeContract(st.Dir())
	if len(issues) == 0 {
		t.Fatal("six-day countdown still validates against ninety minutes")
	}
	readiness := assessZeroInitReadiness(st.Dir(), zeroInitRAGStats{})
	if readiness.StoryTime.Validated {
		t.Fatal("readiness marked mismatched time as validated")
	}
}
