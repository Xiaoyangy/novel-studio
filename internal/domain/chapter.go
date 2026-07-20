package domain

import (
	"fmt"
	"unicode/utf8"
)

// ReviewInterval 全局审阅间隔（每 N 章触发一次）。
const ReviewInterval = 5

// ShortFormWordLimit 是短篇终稿汇总复审的设计阈值。
// 明确规划为 short 的项目总是走短篇终审；未记录规划级别的旧项目按实际总字数兜底。
const ShortFormWordLimit = 30000

// ShouldArcReview 长篇模式下判断是否需要弧级/卷级评审。
func ShouldArcReview(isArcEnd, isVolumeEnd bool, volume, arc int) (bool, string) {
	if isVolumeEnd {
		return true, fmt.Sprintf("第 %d 卷第 %d 弧结束（卷结束），触发弧级+卷级评审", volume, arc)
	}
	if isArcEnd {
		return true, fmt.Sprintf("第 %d 卷第 %d 弧结束，触发弧级评审", volume, arc)
	}
	return false, ""
}

// StructurallyComplete 判断非语义层面的全书章节是否写满。
func StructurallyComplete(progress *Progress) bool {
	return progress != nil &&
		progress.TotalChapters > 0 &&
		len(progress.CompletedChapters) >= progress.TotalChapters
}

// RequiresFinalGlobalReview 判断是否需要短篇全文终审。
// 规则：显式 short 一定走，即使它用“一卷一弧”的 layered 结构承载；显式
// mid/long 不走。只有没有 planning_tier 的旧项目才把 layered 当成长篇信号，
// 并对非 layered 项目用实际总字数 <= 30000 兜底。
func RequiresFinalGlobalReview(progress *Progress, meta *RunMeta) bool {
	if progress == nil {
		return false
	}
	if meta != nil {
		switch meta.PlanningTier {
		case PlanningTierShort:
			return true
		case PlanningTierMid, PlanningTierLong:
			return false
		}
	}
	if progress.Layered {
		return false
	}
	return progress.TotalWordCount > 0 && progress.TotalWordCount <= ShortFormWordLimit
}

// WordCount 按 rune 计算字数。
func WordCount(content string) int {
	return utf8.RuneCountInString(content)
}
