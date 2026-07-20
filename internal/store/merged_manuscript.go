package store

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// MergedManuscriptChapter is one exact committed chapter plus its canonical
// outline title. The source text may already contain a human/Markdown chapter
// heading; BuildMergedManuscript removes that one leading heading before adding
// the canonical heading so 正文.md never repeats chapter titles.
type MergedManuscriptChapter struct {
	Number int
	Title  string
	Text   string
}

var leadingChapterHeading = regexp.MustCompile(`^第[[:space:]]*([0-9]+|[〇零一二三四五六七八九十百]+)[[:space:]]*章([[:space:]]+|[：:·—_-]+[[:space:]]*|$)(.*)$`)

// BuildMergedManuscript builds the deterministic whole-book Markdown artifact.
func BuildMergedManuscript(title string, chapters []MergedManuscriptChapter) (string, error) {
	if len(chapters) == 0 {
		return "", fmt.Errorf("merged manuscript requires at least one chapter")
	}
	ordered := append([]MergedManuscriptChapter(nil), chapters...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Number < ordered[j].Number })
	for i, chapter := range ordered {
		want := i + 1
		if chapter.Number != want {
			return "", fmt.Errorf("merged manuscript chapters must be contiguous from 1: got chapter %d at index %d", chapter.Number, i)
		}
	}

	var b strings.Builder
	if title = strings.TrimSpace(title); title != "" {
		fmt.Fprintf(&b, "# %s\n\n", title)
	}
	for i, chapter := range ordered {
		if i > 0 {
			b.WriteString("\n\n")
		}
		fmt.Fprintf(&b, "## 第 %d 章", chapter.Number)
		if chapterTitle := strings.TrimSpace(chapter.Title); chapterTitle != "" {
			fmt.Fprintf(&b, " %s", chapterTitle)
		}
		b.WriteString("\n\n")
		body := stripLeadingChapterHeading(chapter.Text, chapter.Number)
		if body == "" {
			return "", fmt.Errorf("chapter %d final text is empty after canonical heading normalization", chapter.Number)
		}
		b.WriteString(body)
	}
	return strings.TrimSpace(b.String()) + "\n", nil
}

func stripLeadingChapterHeading(text string, chapter int) string {
	text = strings.TrimSpace(strings.ReplaceAll(text, "\r\n", "\n"))
	if text == "" {
		return ""
	}
	lines := strings.Split(text, "\n")
	first := strings.TrimSpace(strings.ReplaceAll(lines[0], "\u3000", " "))
	first = strings.TrimSpace(strings.TrimLeft(first, "#"))
	match := leadingChapterHeading.FindStringSubmatch(first)
	if len(match) > 0 && chapterHeadingNumberMatches(match[1], chapter) {
		lines = lines[1:]
		for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
			lines = lines[1:]
		}
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func chapterHeadingNumberMatches(raw string, chapter int) bool {
	if parsed, err := strconv.Atoi(raw); err == nil {
		return parsed == chapter
	}
	return raw == chineseChapterNumber(chapter)
}

func chineseChapterNumber(value int) string {
	if value <= 0 || value >= 100 {
		return ""
	}
	digits := []string{"零", "一", "二", "三", "四", "五", "六", "七", "八", "九"}
	if value < 10 {
		return digits[value]
	}
	if value == 10 {
		return "十"
	}
	if value < 20 {
		return "十" + digits[value-10]
	}
	tens, ones := value/10, value%10
	result := digits[tens] + "十"
	if ones > 0 {
		result += digits[ones]
	}
	return result
}
