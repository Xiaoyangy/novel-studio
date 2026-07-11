package tools

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/errs"
	"github.com/chenhongyang/novel-studio/internal/store"
)

var compactProcessTitleTerms = []string{
	"清单", "检查", "整改", "验收", "会议", "大会", "方案", "报告", "看板",
	"样片", "表格", "通知", "记录", "流程", "台账", "额度", "手续", "公示", "总结", "指南针",
}

// validateLightheartedOutlineTitles blocks the clearest report-like titles only
// when this book explicitly asks for both a light tone and attractive titles.
// Semantic title quality remains an Architect/Editor responsibility.
func validateLightheartedOutlineTitles(st *store.Store, entries []domain.OutlineEntry) error {
	if !lightheartedTitleGuardEnabled(st) {
		return nil
	}
	seen := make(map[string]string, len(entries))
	for i, entry := range entries {
		chapter := entry.Chapter
		if chapter <= 0 {
			chapter = i + 1
		}
		label := fmt.Sprintf("第%d章", chapter)
		if err := validateLightheartedReaderTitle(label, entry.Title); err != nil {
			return err
		}
		if previous, ok := seen[strings.TrimSpace(entry.Title)]; ok {
			return fmt.Errorf("轻松题材标题吸引力校验失败：%s 与 %s 重名 %q；请分别抓取各章的反差、笑点、爽点或关系推进: %w",
				previous, label, entry.Title, errs.ErrToolArgs)
		}
		seen[strings.TrimSpace(entry.Title)] = label
	}
	return nil
}

func validateLightheartedLayeredTitles(st *store.Store, volumes []domain.VolumeOutline) error {
	if !lightheartedTitleGuardEnabled(st) {
		return nil
	}
	var chapters []domain.OutlineEntry
	for _, volume := range volumes {
		if err := validateLightheartedReaderTitle(fmt.Sprintf("第%d卷", volume.Index), volume.Title); err != nil {
			return err
		}
		for _, arc := range volume.Arcs {
			if err := validateLightheartedReaderTitle(
				fmt.Sprintf("第%d卷第%d弧", volume.Index, arc.Index), arc.Title,
			); err != nil {
				return err
			}
			chapters = append(chapters, arc.Chapters...)
		}
	}
	return validateLightheartedOutlineTitles(st, chapters)
}

func lightheartedTitleGuardEnabled(st *store.Store) bool {
	if st == nil || st.UserRules == nil {
		return false
	}
	snapshot, err := st.UserRules.Load()
	if err != nil || snapshot == nil {
		return false
	}
	text := snapshot.Structured.Genre + "\n" + snapshot.Preferences
	light := containsAnyTitleTerm(text, "轻松", "欢快", "搞笑", "喜剧", "明亮")
	titleRule := containsAnyTitleTerm(text, "标题", "章名", "卷名", "弧名")
	return light && titleRule
}

func containsAnyTitleTerm(text string, terms ...string) bool {
	for _, term := range terms {
		if strings.Contains(text, term) {
			return true
		}
	}
	return false
}

func validateLightheartedReaderTitle(label, title string) error {
	title = strings.TrimSpace(title)
	if title == "" {
		return fmt.Errorf("轻松题材标题吸引力校验失败：%s 标题为空: %w", label, errs.ErrToolArgs)
	}
	if hanziCount(title) <= 8 {
		for _, term := range compactProcessTitleTerms {
			if strings.Contains(title, term) {
				return fmt.Errorf("轻松题材标题吸引力校验失败：%s title=%q 只是流程/文档标签（命中 %q）；请改成本章反差、笑点、爽点、关系糖或结果钩子: %w",
					label, title, term, errs.ErrToolArgs)
			}
		}
	}
	return nil
}

func hanziCount(text string) int {
	count := 0
	for _, r := range text {
		if unicode.Is(unicode.Han, r) {
			count++
		}
	}
	return count
}
