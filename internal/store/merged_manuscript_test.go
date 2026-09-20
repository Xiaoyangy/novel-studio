package store

import (
	"fmt"
	"strings"
	"testing"
)

func TestBuildMergedManuscriptRemovesHundredthChapterHeading(t *testing.T) {
	chapters := make([]MergedManuscriptChapter, 100)
	for i := range chapters {
		chapters[i] = MergedManuscriptChapter{Number: i + 1, Title: "旧港", Text: fmt.Sprintf("第%d章正文。", i+1)}
	}
	chapters[99].Text = "## 第一百章 旧港\n\n港口终于等到了天明。"
	got, err := BuildMergedManuscript("旧港未眠", chapters)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "第一百章 旧港") || strings.Count(got, "## 第 100 章 旧港") != 1 {
		t.Fatal("hundredth chapter source heading was duplicated instead of canonicalized")
	}
	if !strings.Contains(got, "港口终于等到了天明。") {
		t.Fatal("heading normalization removed the chapter body")
	}
}

func TestMergedManuscriptChineseHeadingNumberBoundaries(t *testing.T) {
	for _, tc := range []struct {
		number int
		word   string
	}{
		{10, "十"}, {19, "十九"}, {99, "九十九"},
		{100, "一百"}, {101, "一百零一"}, {110, "一百一十"},
		{119, "一百一十九"}, {150, "一百五十"}, {200, "二百"}, {999, "九百九十九"},
	} {
		t.Run(tc.word, func(t *testing.T) {
			text := "## 第" + tc.word + "章 旧港\n\n保留正文。"
			if !leadingChapterHeading.MatchString(strings.TrimLeft(strings.Split(text, "\n")[0], "# ")) {
				t.Fatal("heading grammar did not match the Chinese number")
			}
			if got := stripLeadingChapterHeading(text, tc.number); got != "保留正文。" {
				t.Fatalf("matching chapter heading was not removed: %q", got)
			}
			if got := stripLeadingChapterHeading(text, tc.number+1); got != text {
				t.Fatalf("different chapter heading was removed: %q", got)
			}
		})
	}
}

func TestBuildMergedManuscriptRemovesOnlyMatchingLeadingChapterHeadings(t *testing.T) {
	got, err := BuildMergedManuscript("测试书", []MergedManuscriptChapter{
		{Number: 1, Title: "开端", Text: "第1章 开端\n\n第一章正文。"},
		{Number: 2, Title: "收束", Text: "## 第二章 收束\n\n第二章正文。"},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# 测试书", "## 第 1 章 开端", "第一章正文。", "## 第 2 章 收束", "第二章正文。"} {
		if !strings.Contains(got, want) {
			t.Fatalf("canonical manuscript missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "第1章 开端") || strings.Contains(got, "第二章 收束") {
		t.Fatalf("source headings were duplicated:\n%s", got)
	}
}

func TestBuildMergedManuscriptKeepsNarrativeFirstLine(t *testing.T) {
	got, err := BuildMergedManuscript("测试书", []MergedManuscriptChapter{
		{Number: 1, Title: "开端", Text: "第一章里最冷的那天，她敲开了门。"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "第一章里最冷的那天") {
		t.Fatalf("narrative first line was mistaken for a heading:\n%s", got)
	}
}
