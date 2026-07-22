package stylestat

import (
	"slices"
	"strings"
	"testing"
)

func chapterWith(body string) string {
	return "# 标题\n" + body
}

func TestComputeBelowMinChapters(t *testing.T) {
	in := Input{Chapters: []string{"a", "b", "c", "d"}}
	if Compute(in) != nil {
		t.Fatal("below minChapters should return nil")
	}
}

func TestSerialMemoryCompilerCanonicalInputsAndRoot(t *testing.T) {
	completed, err := CanonicalCompletedChapters([]int{3, 1, 3, 2})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(completed, []int{1, 2, 3}) {
		t.Fatalf("completed chapter set is not canonical: %#v", completed)
	}
	stopwords := CanonicalStopwords([]string{" 沈岚 ", "阿岚", "沈岚", ""})
	if !slices.Equal(stopwords, []string{"沈岚", "阿岚"}) {
		t.Fatalf("stopwords are not canonical: %#v", stopwords)
	}
	sources := []SerialMemorySourceBody{
		{Chapter: 1, BodySHA256: "sha256:" + strings.Repeat("1", 64)},
		{Chapter: 2, BodySHA256: "sha256:" + strings.Repeat("2", 64)},
		{Chapter: 3, BodySHA256: "sha256:" + strings.Repeat("3", 64)},
	}
	root := SerialMemoryCompilerRoot(completed, sources, stopwords)
	if root == SerialMemoryCompilerRoot(completed, sources, append(stopwords, "新别名")) {
		t.Fatal("compiler root did not bind canonical stopwords")
	}
	if root == SerialMemoryCompilerRoot([]int{1, 2}, sources[:2], stopwords) {
		t.Fatal("compiler root did not bind completed chapter set/body hashes")
	}
}

func TestComputePatterns(t *testing.T) {
	body := "他不是愤怒，而是恐惧。沉默了几息。像一盏灯。\n正文。\n"
	chapters := make([]string, 6)
	for i := range chapters {
		chapters[i] = chapterWith(body)
	}
	s := Compute(Input{Chapters: chapters})
	if s == nil {
		t.Fatal("expected stats")
	}
	want := map[string]int{
		"矫正句『不是…(而)是…』":       6,
		"计时量词『X息/X瞬』":         6,
		"明喻『像一/仿佛/如同/宛如』":     6,
		"沉默节拍『沉默了/没有说话/没有回头』": 6,
	}
	for _, p := range s.Patterns {
		if w, ok := want[p.Name]; ok && p.Total != w {
			t.Errorf("%s total: got %d want %d", p.Name, p.Total, w)
		}
		if p.PerChapter != 1.0 {
			t.Errorf("%s per_chapter: got %v want 1.0", p.Name, p.PerChapter)
		}
	}
	if len(s.Patterns) != 4 {
		t.Errorf("want 4 pattern classes, got %d: %+v", len(s.Patterns), s.Patterns)
	}
}

func TestComputeStripsCanonicalChapterHeadingBeforeAllProseStats(t *testing.T) {
	chapters := make([]string, 10)
	for i := range chapters {
		heading := "# 清晨不是愤怒而是恐惧青云山巅反复出现青云山巅反复出现"
		if i%2 != 0 {
			heading = "第一章 清晨不是愤怒而是恐惧青云山巅反复出现青云山巅反复出现"
		}
		chapters[i] = "\n" + heading + "\n正文。\n结尾。"
	}
	// A heading-only chapter must not contribute its long title as an ending.
	chapters[0] = "\n" + strings.Split(strings.TrimSpace(chapters[0]), "\n")[0]

	s := Compute(Input{Chapters: chapters})
	if s == nil {
		t.Fatal("expected stats")
	}
	if len(s.Patterns) != 0 {
		t.Errorf("chapter headings contaminated pattern stats: %+v", s.Patterns)
	}
	if len(s.TopPhrases) != 0 {
		t.Errorf("chapter headings contaminated phrase stats: %+v", s.TopPhrases)
	}
	if len(s.RepeatedSentences) != 0 {
		t.Errorf("chapter headings contaminated repeated sentences: %+v", s.RepeatedSentences)
	}
	if s.OpeningTimeRate != 0 {
		t.Errorf("chapter headings contaminated opening rate: %v", s.OpeningTimeRate)
	}
	if s.Ending.ShortRatio != 1 || s.Ending.MedianRunes != len([]rune("结尾。")) {
		t.Errorf("ending should come from prose body, got %+v", s.Ending)
	}
}

func TestComputeKeepsLaterMarkdownHeadingAsProse(t *testing.T) {
	chapters := make([]string, 5)
	for i := range chapters {
		chapters[i] = "# 第1章 标题\n正文开头。\n## 不是愤怒而是恐惧\n正文结尾。"
	}
	s := Compute(Input{Chapters: chapters})
	if s == nil || len(s.Patterns) != 1 || s.Patterns[0].Total != 5 {
		t.Fatalf("later body heading should remain observable: %+v", s)
	}
}

func TestComputeTopPhrasesWithStopwords(t *testing.T) {
	// 「青云山巅」高频出现；「陆九渊」是角色名应被过滤
	line := "众人望向青云山巅，陆九渊负手而立。\n"
	chapters := make([]string, 10)
	for i := range chapters {
		chapters[i] = chapterWith(strings.Repeat(line, 3))
	}
	s := Compute(Input{Chapters: chapters, Stopwords: []string{"陆九渊"}})
	if s == nil {
		t.Fatal("expected stats")
	}
	var hasMountain, hasName bool
	for _, p := range s.TopPhrases {
		if strings.Contains(p.Text, "青云山") {
			hasMountain = true
		}
		if strings.Contains(p.Text, "九渊") || strings.Contains(p.Text, "陆九") {
			hasName = true
		}
	}
	if !hasMountain {
		t.Errorf("expected 青云山 phrase mined, got %+v", s.TopPhrases)
	}
	if hasName {
		t.Errorf("character name should be filtered, got %+v", s.TopPhrases)
	}
}

func TestComputeRepeatedSentences(t *testing.T) {
	motto := "此生未能远行，望你替我看看远方的山海。"
	chapters := make([]string, 6)
	for i := range chapters {
		body := "平常正文，没有什么重复。\n"
		if i%2 == 0 {
			body += motto + "\n"
		}
		chapters[i] = chapterWith(body)
	}
	s := Compute(Input{Chapters: chapters})
	if s == nil {
		t.Fatal("expected stats")
	}
	if len(s.RepeatedSentences) == 0 {
		t.Fatalf("expected repeated sentence, got none")
	}
	got := s.RepeatedSentences[0]
	if got.Chapters != 3 || got.Count != 3 {
		t.Errorf("repeated sentence: %+v", got)
	}
	if !strings.HasPrefix(got.Text, "此生未能远行") {
		t.Errorf("text: %q", got.Text)
	}
}

func TestComputeEndingAndOpening(t *testing.T) {
	short := chapterWith("一整夜没有睡。\n正文很长很长很长。\n他走了。")
	long := chapterWith("白天的事。\n正文。\n这是一个非常非常非常长的结尾句子，远远超过三十个字符的阈值长度，用来测试中位数。")
	chapters := []string{short, short, short, long, long}
	s := Compute(Input{Chapters: chapters})
	if s == nil {
		t.Fatal("expected stats")
	}
	if s.Ending.ShortRatio != 0.6 {
		t.Errorf("short_ratio: got %v want 0.6", s.Ending.ShortRatio)
	}
	if s.OpeningTimeRate != 0.6 {
		t.Errorf("opening_time_rate: got %v want 0.6", s.OpeningTimeRate)
	}
}

func TestComputeTitleFormats(t *testing.T) {
	chapters := make([]string, 5)
	for i := range chapters {
		chapters[i] = chapterWith("正文。")
	}
	// 混用 → 上报
	s := Compute(Input{Chapters: chapters, Titles: []string{"第一章 风起", "云涌", "第3章 雷动"}})
	if s.TitleFormats == nil || s.TitleFormats.WithPrefix != 2 || s.TitleFormats.WithoutPrefix != 1 {
		t.Errorf("title formats: %+v", s.TitleFormats)
	}
	// 统一 → 不上报
	s = Compute(Input{Chapters: chapters, Titles: []string{"风起", "云涌"}})
	if s.TitleFormats != nil {
		t.Errorf("uniform titles should not report: %+v", s.TitleFormats)
	}
}
