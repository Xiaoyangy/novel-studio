package store

import (
	"strings"
	"testing"
)

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
