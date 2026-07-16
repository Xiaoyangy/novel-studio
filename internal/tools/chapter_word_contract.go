package tools

import (
	"fmt"
	"unicode/utf8"

	"github.com/chenhongyang/novel-studio/internal/errs"
	"github.com/chenhongyang/novel-studio/internal/store"
)

type chapterWordContractResult struct {
	Configured bool `json:"configured"`
	Passed     bool `json:"passed"`
	Min        int  `json:"min,omitempty"`
	Max        int  `json:"max,omitempty"`
	Actual     int  `json:"actual"`
}

func inspectChapterWordContract(st *store.Store, content string) chapterWordContractResult {
	result := chapterWordContractResult{
		Passed: true,
		Actual: utf8.RuneCountInString(content),
	}
	if st == nil {
		return result
	}
	snapshot, err := st.UserRules.Load()
	if err != nil || snapshot == nil || snapshot.Structured.ChapterWords == nil {
		return result
	}
	rangeRule := snapshot.Structured.ChapterWords
	if rangeRule.Min <= 0 && rangeRule.Max <= 0 {
		return result
	}
	result.Configured = true
	result.Min = rangeRule.Min
	result.Max = rangeRule.Max
	result.Passed = (result.Min <= 0 || result.Actual >= result.Min) &&
		(result.Max <= 0 || result.Actual <= result.Max)
	return result
}

func requireChapterWordContract(st *store.Store, chapter int, content string) error {
	// Every initial, resumed and rewrite commit passes this delivery boundary.
	// Keep the metadata guard here as an early, retry-proof precondition so a
	// forged legacy draft or a mechanical-gate retry limit cannot commit leaked
	// planner/RAG/checkpoint fields to the final chapter.
	if err := validateFictionProseMetadataFree(content); err != nil {
		return fmt.Errorf("第 %d 章终稿完整性门禁未通过: %w", chapter, err)
	}
	if err := validateFictionProseTypography(content); err != nil {
		return fmt.Errorf("第 %d 章终稿格式门禁未通过: %w", chapter, err)
	}
	result := inspectChapterWordContract(st, content)
	if !result.Configured || result.Passed {
		return nil
	}
	action := "调整正文长度"
	if result.Max > 0 && result.Actual > result.Max {
		action = fmt.Sprintf("至少压缩 %d 字", result.Actual-result.Max)
	} else if result.Min > 0 && result.Actual < result.Min {
		action = fmt.Sprintf("至少补足 %d 字", result.Min-result.Actual)
	}
	return fmt.Errorf(
		"第 %d 章字数硬门禁未通过：实际 %d 字，user_rules.chapter_words=%d-%d，需%s。草稿已保留；请先 read_chapter(source=draft)，用 edit_chapter 做局部调整，再重新 check_consistency；不要再次整章 draft_chapter，也不能覆盖终稿: %w",
		chapter, result.Actual, result.Min, result.Max, action, errs.ErrToolPrecondition,
	)
}

func draftWordContractNextStep(result chapterWordContractResult) string {
	if !result.Configured || result.Passed {
		return "草稿已成功保存。不要再次调用 draft_chapter 重写同一章；立即 read_chapter(source=draft) 回读草稿，再调用 check_consistency。若无硬伤，必须调用 commit_chapter 提交终稿。"
	}
	return fmt.Sprintf(
		"草稿已保存，但字数硬门禁未通过：实际 %d 字，要求 %d-%d。不要再次整章 draft_chapter；先 read_chapter(source=draft)，用 edit_chapter 局部压缩或补足，达到区间后重新 check_consistency，最后才能 commit_chapter。",
		result.Actual, result.Min, result.Max,
	)
}
