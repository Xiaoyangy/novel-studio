package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

func TestZeroStoryTimeScopeSeparatesEventDeadlineFromWholeStory(t *testing.T) {
	for _, text := range []string{
		"现实、空间与时限合同｜建立章位：第1章；适用范围：全书。开局距本夜最晚停航90分钟，实际危险可提前阻止离泊；正文场景限四处。",
		"开局距最后一班渡船封航九十分钟。",
		"渡船离泊倒计时90分钟，岸上核账不受该截止限制。",
		"核账必须在90分钟内结束。",
		"必须在90分钟内结束机务检查。",
		"单次运送耗时5分钟；设备可连续照明180分钟。",
	} {
		hint, err := zeroOutlineAllReceiptStoryTimeHint(domain.OutlineAllExecutionReceipt{EstimatedScale: "1卷1弧3章", NonNegotiables: []string{text}})
		if err != nil || hint != "" {
			t.Fatalf("event deadline became global story limit: %q => %q / %v", text, hint, err)
		}
	}
	for _, text := range []string{
		"现实与时限合同：开局距最后截止时点90分钟；适用范围为全书。",
		"现实与时限合同｜开局距最后期限九十分钟；正文不跨过截止时点。",
		"全书倒计时90分钟。", "主线倒计时九十分钟。", "主线跨度90分钟。",
		"全书时限90分钟。", "全书必须在90分钟内结束。", "故事必须在90分钟以内完结。",
		"时间合同｜必须在90分钟内结束。", "主线须在90分钟内收束。", "全书故事90分钟内结束。",
		"必须在90分钟内完成全书。",
	} {
		hint, err := zeroOutlineAllReceiptStoryTimeHint(domain.OutlineAllExecutionReceipt{EstimatedScale: "1卷1弧3章", NonNegotiables: []string{text}})
		if err != nil || hint != "0.0625日" {
			t.Fatalf("real global deadline was weakened: %q => %q / %v", text, hint, err)
		}
	}
}

func TestZeroStoryTimeScopeKeepsExplicitHintsAndConflictingGlobalLimits(t *testing.T) {
	hint, err := zeroOutlineAllReceiptStoryTimeHint(domain.OutlineAllExecutionReceipt{StoryTimeHint: "90分钟", NonNegotiables: []string{"渡船离泊倒计时60分钟"}})
	if err != nil || hint != "0.0625日" {
		t.Fatalf("explicit story_time_hint lost authority: %q %v", hint, err)
	}
	if _, err := zeroOutlineAllReceiptStoryTimeHint(domain.OutlineAllExecutionReceipt{NonNegotiables: []string{"全书时限90分钟", "主线必须在2小时内结束"}}); err == nil {
		t.Fatal("conflicting global time bounds silently selected one")
	}
}

func TestZeroStoryTimeScopeDoesNotRewriteExistingFrozenContract(t *testing.T) {
	st := sealedCountdownStore(t)
	compass, _ := st.Outline.LoadCompass()
	receipt, _ := st.LoadOutlineAllExecutionReceipt()
	compass.NonNegotiables = []string{"现实、空间与时限合同｜适用范围：全书。开局距本夜最晚停航90分钟，实际危险可提前阻止离泊。"}
	if err := st.Outline.SaveCompass(*compass); err != nil {
		t.Fatal(err)
	}
	receipt.NonNegotiables = compass.NonNegotiables
	receipt.CompassDigest, _ = domain.ComputeStoryCompassDigest(*compass)
	*receipt, _ = domain.SignOutlineAllExecutionReceipt(*receipt)
	if err := st.SaveOutlineAllExecutionReceipt(*receipt); err != nil {
		t.Fatal(err)
	}
	target, scale, bound, err := zeroCompletedOutlineAllTimeSource(st)
	if err != nil || !bound || target != 3 || domain.ParseStoryScale(scale).DurationDaysMax != 0 {
		t.Fatalf("event-only sealed outline source acquired a global duration: target=%d scale=%q bound=%v err=%v", target, scale, bound, err)
	}
	fresh, err := domain.DeriveStoryTimeContract(scale, target)
	if err != nil || fresh.Source != domain.StoryTimeSourceFallbackNominal {
		t.Fatalf("unspecified whole-story span did not remain nominal: %+v %v", fresh, err)
	}
	// Reproduce the historical mistaken derived core, without weakening its
	// validator or rewriting it when the corrected parser discovers drift.
	old, err := domain.DeriveStoryTimeContract(receipt.EstimatedScale+"；主线时间跨度0.0625日", 3)
	if err != nil {
		t.Fatal(err)
	}
	old.Source = domain.StoryTimeSourceOutlineAll
	if err := st.WorldSim.SaveStoryTimeContract(old); err != nil {
		t.Fatal(err)
	}
	stored, err := st.WorldSim.LoadStoryTimeContract()
	if err != nil {
		t.Fatal(err)
	}
	clock, err := domain.FinalizeStoryClockContext(domain.StoryClockContext{CurrentDay: 0.0625, DurationDaysMin: stored.DurationDaysMin, DurationDaysMax: stored.DurationDaysMax, TimeContractCoreDigest: stored.CoreDigest})
	if err != nil {
		t.Fatal(err)
	}
	if err := domain.ValidateStoryTimeForClock(2, &domain.StoryTimeChapterSchedule{Chapter: 2, StartDay: 0.0625, EndDay: 91.0 / 1440}, &clock); err == nil {
		t.Fatal("scope extraction repair weakened existing hard-clock validation")
	}
	path := filepath.Join(st.Dir(), "meta/story_time_contract.json")
	before, _ := os.ReadFile(path)
	if _, _, err := zeroEnsureStoryTimeContract(st, nil); err == nil || !strings.Contains(err.Error(), "successor/rebase") {
		t.Fatalf("old frozen time core silently upgraded: %v", err)
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("scope repair rewrote historical time evidence")
	}
}
