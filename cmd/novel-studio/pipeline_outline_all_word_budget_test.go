package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/rules"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/tools"
)

func shortOutlineWordContractFixture(t *testing.T, scale string) (*store.Store, *domain.StoryCompass, domain.BookScaleTarget) {
	t.Helper()
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.RunMeta.Save(domain.RunMeta{PlanningTier: domain.PlanningTierShort}); err != nil {
		t.Fatal(err)
	}
	if err := st.UserRules.Save(&rules.Snapshot{Status: rules.StatusReady, Structured: rules.Structured{
		ChapterWords: &rules.WordRange{Min: 2200, Max: 2500},
	}}); err != nil {
		t.Fatal(err)
	}
	compass := &domain.StoryCompass{
		EstimatedScale: scale, EndingDirection: "交还原始账本并承担取证代价",
		OpenThreads: []string{"账页被更改的原因"}, NonNegotiables: []string{"第三章结束全部事件"},
	}
	if err := st.Outline.SaveCompass(*compass); err != nil {
		t.Fatal(err)
	}
	target, err := domain.ResolveBookScaleTarget(scale, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	return st, compass, target
}

func TestOutlineAllShortWordContractDerivesBeforeFreezeAndIsIdempotent(t *testing.T) {
	st, compass, target := shortOutlineWordContractFixture(t, "1-1卷，3-3章；每章2200-2500字")
	before := *compass
	target, changed, err := preparePipelineOutlineAllShortWordContract(st, compass, target)
	if err != nil || !changed {
		t.Fatalf("derive: changed=%v err=%v", changed, err)
	}
	if target.MinWords != 6600 || target.MaxWords != 7500 || target.TargetWords != 7050 || target.TargetWordsPerChapter != 2350 {
		t.Fatalf("unexpected derived contract: %+v", target)
	}
	before.EstimatedScale += "；全书6600-7500字"
	if !reflect.DeepEqual(*compass, before) {
		t.Fatalf("derivation changed author intent: got=%+v want=%+v", *compass, before)
	}
	path := filepath.Join(st.Dir(), "meta", "compass.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	stat, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	again, changed, err := preparePipelineOutlineAllShortWordContract(st, compass, target)
	if err != nil || changed || !reflect.DeepEqual(again, target) {
		t.Fatalf("resume changed word contract: changed=%v err=%v target=%+v", changed, err, again)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	afterStat, err := os.Stat(path)
	if err != nil || !stat.ModTime().Equal(afterStat.ModTime()) || string(raw) != string(after) {
		t.Fatalf("resume rewrote persisted compass: err=%v", err)
	}
}

func TestOutlineAllShortWordContractPreservesExplicitTotalAndRejectsConflicts(t *testing.T) {
	for _, tc := range []struct {
		name, scale, errorText string
	}{
		{"explicit", "1-1卷，3-3章；全书7000-7400字", ""},
		{"impossible total", "1-1卷，3-3章；全书12000-13000字", "conflicts"},
		{"ambiguous chapter count", "1-1卷，3-4章", "fixed chapter count"},
		{"unparsed fixed total", "1-1卷，3-3章；全书7000字", "unparsed"},
		{"unparsed Chinese total", "1-1卷，3-3章；全文七千字", "unparsed"},
		{"total sharing a chapter clause", "1-1卷，3-3章；每章2200-2500字 全书7000字", "unparsed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st, compass, target := shortOutlineWordContractFixture(t, tc.scale)
			before := *compass
			got, changed, err := preparePipelineOutlineAllShortWordContract(st, compass, target)
			if tc.errorText == "" {
				if err != nil || !reflect.DeepEqual(got, target) {
					t.Fatalf("explicit range changed: %+v err=%v", got, err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.errorText) {
				t.Fatalf("want %q error, got %v", tc.errorText, err)
			}
			persisted, loadErr := st.Outline.LoadCompass()
			if changed || loadErr != nil || !reflect.DeepEqual(*compass, before) || !reflect.DeepEqual(*persisted, before) {
				t.Fatalf("contract was rewritten: changed=%v load=%v persisted=%+v", changed, loadErr, persisted)
			}
		})
	}
}

func TestOutlineAllShortWordContractRefusesUnknownChapterRules(t *testing.T) {
	for _, snapshot := range []*rules.Snapshot{
		{},
		{Status: rules.StatusReady},
		{Status: rules.StatusDegraded, Structured: rules.Structured{ChapterWords: &rules.WordRange{Min: 2200, Max: 2500}}},
		{Status: rules.StatusReady, Structured: rules.Structured{ChapterWords: &rules.WordRange{Min: 0, Max: 2500}}},
		{Status: rules.StatusReady, Structured: rules.Structured{ChapterWords: &rules.WordRange{Min: 2500, Max: 2200}}},
	} {
		st, compass, target := shortOutlineWordContractFixture(t, "1-1卷，3-3章")
		if err := st.UserRules.Save(snapshot); err != nil {
			t.Fatal(err)
		}
		if _, changed, err := preparePipelineOutlineAllShortWordContract(st, compass, target); err == nil || changed {
			t.Fatalf("unknown rules were promoted: %+v changed=%v err=%v", snapshot, changed, err)
		}
	}
}

func TestOutlineAllShortWordContractLeavesOtherPlanningTiersUntouched(t *testing.T) {
	for _, tier := range []domain.PlanningTier{"", domain.PlanningTierMid, domain.PlanningTierLong} {
		st, compass, target := shortOutlineWordContractFixture(t, "1-1卷，3-3章")
		if err := st.RunMeta.Save(domain.RunMeta{PlanningTier: tier}); err != nil {
			t.Fatal(err)
		}
		got, changed, err := preparePipelineOutlineAllShortWordContract(st, compass, target)
		if err != nil || changed || !reflect.DeepEqual(got, target) {
			t.Fatalf("tier=%q changed=%v err=%v", tier, changed, err)
		}
	}
}

func TestOutlineAllDerivedWordContractReachesSealedPlanningAndDelivery(t *testing.T) {
	st, compass, target := shortOutlineWordContractFixture(t, "1-1卷，3-3章")
	target, _, err := preparePipelineOutlineAllShortWordContract(st, compass, target)
	if err != nil {
		t.Fatal(err)
	}
	// The ordinary signed receipt is the only transport: do not teach the
	// render/commit path a second inferred contract or bypass its checks.
	fixtureDir := outlineAllGateLiveDir(t)
	fixtureCandidate := filepath.Join(t.TempDir(), "short-word-contract", "candidate", "output")
	receipt := writeOutlineAllGateCompleteReceipt(t, fixtureDir, fixtureCandidate, outlineAllGateDigest)
	receipt.EstimatedScale = compass.EstimatedScale
	receipt.TargetChapters = target.TargetChapters
	receipt.MinChapters, receipt.MaxChapters = target.Range.MinChapters, target.Range.MaxChapters
	receipt.TargetWords, receipt.TargetWordsPerChapter = target.TargetWords, target.TargetWordsPerChapter
	receipt.CompassDigest, err = domain.ComputeStoryCompassDigest(*compass)
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
	if err := st.Progress.Save(&domain.Progress{TotalChapters: 3, CurrentChapter: 1}); err != nil {
		t.Fatal(err)
	}
	bounds, err := tools.InspectShortChapterWordBoundsFromAcceptedProse(st, 1)
	if err != nil || !bounds.Active || bounds.BookMin != 6600 || bounds.BookMax != 7500 || bounds.Min != 2200 || bounds.Max != 2500 {
		t.Fatalf("derived contract not visible to sealed planner: %+v err=%v", bounds, err)
	}
	for chapter := 1; chapter <= 3; chapter++ {
		if err := st.Drafts.SaveFinalChapter(chapter, strings.Repeat("文", 2350)); err != nil {
			t.Fatal(err)
		}
	}
	if err := validatePipelineFullBookWordBudget(st, st.Dir(), []int{1, 2, 3}); err != nil {
		t.Fatalf("delivery rejected valid derived total: %v", err)
	}
	if err := st.Drafts.SaveFinalChapter(3, strings.Repeat("文", 3000)); err != nil {
		t.Fatal(err)
	}
	if err := validatePipelineFullBookWordBudget(st, st.Dir(), []int{1, 2, 3}); err == nil {
		t.Fatal("delivery accepted a total above the frozen derived contract")
	}
}
