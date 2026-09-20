package domain

import "testing"

func TestBookChapterTotalBeforeParenthesizedUnitBudgets(t *testing.T) {
	for _, scale := range []string{
		"7-7卷，150-150章（每卷20-22章，每弧10-11章）",
		"7-7卷，150-150章(每卷20-22章，每弧10-11章)",
		"7-7卷，150-150章，每卷20-22章，每弧10-11章",
		"7-7卷，150-150章（每卷20-22章；每弧10-11章）",
		"7-7 volumes, 150-150 chapters (per volume 20-22 chapters, per arc 10-11 chapters)",
		"7-7卷，20-22章（每卷），全书150-150章",
	} {
		t.Run(scale, func(t *testing.T) {
			got, err := ParseBookScaleRange(scale)
			if err != nil {
				t.Fatalf("explicit whole-book range was lost: %v", err)
			}
			want := (BookScaleRange{MinVolumes: 7, MaxVolumes: 7, MinChapters: 150, MaxChapters: 150})
			if got != want {
				t.Fatalf("got %+v; want %+v", got, want)
			}
		})
	}
}

func TestBookChapterParenthesizedBudgetsKeepLongNovelTarget(t *testing.T) {
	target, err := ResolveBookScaleTarget("7-7卷，150-150章（每卷20-22章，每弧10-11章），全书30-30万字", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if target.TargetVolumes != 7 || target.TargetChapters != 150 || target.TargetWords != 300000 || target.TargetWordsPerChapter != 2000 {
		t.Fatalf("parenthetical unit budgets changed the whole-book target: %+v", target)
	}
}

func TestBookChapterRangeParentheticalQualifierCompatibility(t *testing.T) {
	for _, scale := range []string{
		"7-7卷，20-22章（每卷）",
		"7-7卷，10-11章(每弧)",
		"7-7 volumes, 20-22 chapters (per volume)",
		"7-7卷，20-22章/卷",
		"7-7卷，每卷20-22章",
	} {
		t.Run(scale, func(t *testing.T) {
			if _, err := ParseBookScaleRange(scale); err == nil {
				t.Fatal("per-unit budget must not become the whole-book ceiling")
			}
		})
	}
}
