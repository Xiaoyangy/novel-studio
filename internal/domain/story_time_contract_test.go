package domain

import (
	"math"
	"strings"
	"testing"
)

func TestParseStoryScaleSupportsArabicAndChineseHalfYearRanges(t *testing.T) {
	for _, raw := range []string{
		"预计100-130万字，约8-10卷，360-480章；主线时间跨度约3.5-4年",
		"预计100-130万字，约8-10卷，360-480章；主线时间跨度约3年半到4年",
	} {
		got := ParseStoryScale(raw)
		if got.ChapterMin != 360 || got.ChapterMax != 480 ||
			math.Abs(got.DurationYearsMin-3.5) > 1e-9 || math.Abs(got.DurationYearsMax-4) > 1e-9 {
			t.Fatalf("ParseStoryScale(%q) = %+v", raw, got)
		}
	}
}

func TestDeriveStoryTimeContractFor420ChaptersAcrossThreeAndHalfToFourYears(t *testing.T) {
	contract, err := DeriveStoryTimeContract(
		"预计100-130万字，约8-10卷，360-480章；主线时间跨度约3年半到4年",
		420,
	)
	if err != nil {
		t.Fatal(err)
	}
	wantNominal := (3.5 + 4) / 2 * StoryDaysPerYear / 420
	if contract.TargetChapters != 420 || contract.Source != StoryTimeSourceCompassEstimatedScale {
		t.Fatalf("unexpected contract identity: %+v", contract)
	}
	if math.Abs(contract.DurationDaysMin-3.5*StoryDaysPerYear) > 1e-9 ||
		math.Abs(contract.DurationDaysMax-4*StoryDaysPerYear) > 1e-9 ||
		math.Abs(contract.NominalDaysPerChapter-wantNominal) > 1e-9 {
		t.Fatalf("unexpected duration/nominal: %+v want nominal %.9f", contract, wantNominal)
	}
	if math.Abs(contract.NominalDaysPerChapter-3.26109375) > 1e-8 {
		t.Fatalf("420-chapter nominal should be about 3.26 days, got %.9f", contract.NominalDaysPerChapter)
	}
	if contract.CoreDigest == "" || contract.ScheduleDigest == "" {
		t.Fatalf("contract must carry both digests: %+v", contract)
	}
	if err := contract.Validate(); err != nil {
		t.Fatalf("derived contract invalid: %v", err)
	}
}

func TestDeriveStoryTimeContractFallbackAndRangeGuard(t *testing.T) {
	contract, err := DeriveStoryTimeContract("长篇多卷", 70)
	if err != nil {
		t.Fatal(err)
	}
	if contract.Source != StoryTimeSourceFallbackNominal || contract.NominalDaysPerChapter != 2 ||
		contract.DurationDaysMin != 140 || contract.DurationDaysMax != 140 {
		t.Fatalf("fallback contract = %+v", contract)
	}
	if _, err := DeriveStoryTimeContract("360-480章，3.5-4年", 128); err == nil ||
		!strings.Contains(err.Error(), "outside compass chapter range") {
		t.Fatalf("out-of-range finalized target must fail, got %v", err)
	}
}

func TestStoryTimeContractSchedulesAreMonotonicAndDigestSealed(t *testing.T) {
	contract, err := FinalizeStoryTimeContract(StoryTimeContract{
		Version:               StoryTimeContractVersion,
		Source:                StoryTimeSourceExplicit,
		TargetChapters:        10,
		DurationDaysMin:       14,
		DurationDaysMax:       16,
		NominalDaysPerChapter: 1.5,
		ArcSchedule: []StoryTimeArcSchedule{
			{Volume: 1, Arc: 1, StartChapter: 1, EndChapter: 5, StartDay: 0, EndDay: 7},
			{Volume: 1, Arc: 2, StartChapter: 6, EndChapter: 10, StartDay: 7, EndDay: 15},
		},
		ChapterSchedule: []StoryTimeChapterSchedule{
			{Chapter: 2, StartDay: 1, EndDay: 2.5},
			{Chapter: 7, StartDay: 8, EndDay: 10},
			{Chapter: 10, StartDay: 13.5, EndDay: 15},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := contract.Validate(); err != nil {
		t.Fatal(err)
	}

	tampered := contract
	tampered.ArcSchedule = append([]StoryTimeArcSchedule(nil), contract.ArcSchedule...)
	tampered.ArcSchedule[0].EndDay = 6.5
	if err := tampered.Validate(); err == nil || !strings.Contains(err.Error(), "schedule_digest mismatch") {
		t.Fatalf("schedule tampering must fail its digest, got %v", err)
	}

	nonMonotonic := contract
	nonMonotonic.ChapterSchedule = []StoryTimeChapterSchedule{
		{Chapter: 2, StartDay: 3, EndDay: 4},
		{Chapter: 7, StartDay: 2, EndDay: 5},
	}
	nonMonotonic, err = FinalizeStoryTimeContract(nonMonotonic)
	if err == nil || !strings.Contains(err.Error(), "not monotonic") {
		t.Fatalf("non-monotonic schedule must fail even with recomputed digest, got %+v err=%v", nonMonotonic, err)
	}
}

func TestStoryDayForChapterUsesChapterThenArcThenNominal(t *testing.T) {
	contract, err := FinalizeStoryTimeContract(StoryTimeContract{
		Version:               StoryTimeContractVersion,
		Source:                StoryTimeSourceExplicit,
		TargetChapters:        10,
		DurationDaysMin:       14,
		DurationDaysMax:       16,
		NominalDaysPerChapter: 1.5,
		ArcSchedule: []StoryTimeArcSchedule{
			{Volume: 1, Arc: 1, StartChapter: 1, EndChapter: 2, StartDay: 0, EndDay: 6},
		},
		ChapterSchedule: []StoryTimeChapterSchedule{
			{Chapter: 2, StartDay: 4, EndDay: 5},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := contract.StoryDayForChapter(1); got != 3 {
		t.Fatalf("arc interpolation = %v, want 3", got)
	}
	if got := contract.StoryDayForChapter(2); got != 5 {
		t.Fatalf("exact chapter schedule = %v, want 5", got)
	}
	if got := contract.StoryDayForChapter(3); got != 4.5 {
		t.Fatalf("nominal fallback = %v, want 4.5", got)
	}
}
