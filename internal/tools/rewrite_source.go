package tools

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func loadChapterRewriteSource(s *store.Store, chapter int) (*domain.ChapterRewriteSource, string, string, error) {
	if s == nil || chapter <= 0 {
		return nil, "", "", nil
	}
	target, rewrite := pendingRewriteTarget(s)
	if !rewrite || target != chapter {
		return nil, "", "", nil
	}
	body, err := s.Drafts.LoadChapterText(chapter)
	if err != nil {
		return nil, "", "", fmt.Errorf("load rewrite source chapter: %w", err)
	}
	if strings.TrimSpace(body) == "" {
		return nil, "", "", fmt.Errorf("待返工第 %d 章缺少已提交终稿 chapters/%02d.md", chapter, chapter)
	}
	bodyPath := fmt.Sprintf("chapters/%02d.md", chapter)
	briefPath := fmt.Sprintf("reviews/%02d_rewrite_brief.md", chapter)
	briefRaw, err := os.ReadFile(filepath.Join(s.Dir(), filepath.FromSlash(briefPath)))
	if err != nil {
		return nil, "", "", fmt.Errorf("load rewrite brief %s: %w", briefPath, err)
	}
	brief := string(briefRaw)
	sum := sha256.Sum256([]byte(body))
	briefSum := sha256.Sum256(briefRaw)
	source := &domain.ChapterRewriteSource{
		BodyPath:      bodyPath,
		BodySHA256:    hex.EncodeToString(sum[:]),
		WordCount:     utf8.RuneCountInString(body),
		BriefPath:     briefPath,
		BriefSHA256:   hex.EncodeToString(briefSum[:]),
		PreserveFacts: rewriteBriefPreserveFacts(brief),
	}
	return source, body, brief, nil
}

func rewriteBriefPreserveFacts(markdown string) []string {
	lines := strings.Split(strings.ReplaceAll(markdown, "\r\n", "\n"), "\n")
	inSection := false
	var facts []string
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "## ") {
			if inSection {
				break
			}
			heading := strings.TrimSpace(strings.TrimPrefix(line, "## "))
			inSection = heading == "保留事实" || heading == "必须保留事实" || heading == "保留约束"
			continue
		}
		if !inSection || line == "" {
			continue
		}
		for _, prefix := range []string{"- ", "* ", "+ "} {
			if strings.HasPrefix(line, prefix) {
				line = strings.TrimSpace(strings.TrimPrefix(line, prefix))
				break
			}
		}
		if line == "" {
			continue
		}
		if rewriteBriefEmptyItem(line) {
			continue
		}
		facts = appendUniqueString(facts, line)
	}
	return facts
}

func rewriteBriefEmptyItem(line string) bool {
	line = strings.TrimSpace(strings.TrimRight(line, "。.!！"))
	switch line {
	case "无", "暂无", "无额外条目", "无额外事实", "没有额外条目", "none", "None", "N/A":
		return true
	default:
		return false
	}
}

var rewriteLiteralPatterns = []*regexp.Regexp{
	regexp.MustCompile(`开头用[‘“「"]([^’”」"]+)[’”」"]`),
}

// rewriteBriefRequiredLiterals extracts phrases the user explicitly requires
// verbatim as placement/copy contracts. Meme examples and "must say this"
// dialogue are intentionally excluded: turning them into literal gates made
// characters recite review notes even when the scene no longer supported them.
func rewriteBriefRequiredLiterals(markdown string) []string {
	var literals []string
	for _, pattern := range rewriteLiteralPatterns {
		for _, match := range pattern.FindAllStringSubmatch(markdown, -1) {
			if len(match) > 1 {
				literals = appendUniqueString(literals, strings.TrimSpace(match[1]))
			}
		}
	}
	return literals
}

func rewriteSourceToken(source *domain.ChapterRewriteSource) string {
	if source == nil {
		return ""
	}
	return fmt.Sprintf("rewrite_source:%s#sha256=%s", source.BodyPath, source.BodySHA256)
}

func rewriteBriefToken(source *domain.ChapterRewriteSource) string {
	if source == nil {
		return ""
	}
	return fmt.Sprintf("rewrite_brief:%s#sha256=%s", source.BriefPath, source.BriefSHA256)
}

func rewriteSourceEqual(a, b *domain.ChapterRewriteSource) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.BodyPath != b.BodyPath || a.BodySHA256 != b.BodySHA256 || a.WordCount != b.WordCount ||
		a.BriefPath != b.BriefPath || a.BriefSHA256 != b.BriefSHA256 {
		return false
	}
	return stringSlicesEqual(a.PreserveFacts, b.PreserveFacts)
}

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if strings.TrimSpace(a[i]) != strings.TrimSpace(b[i]) {
			return false
		}
	}
	return true
}

func factCoveredByConstraints(fact string, constraints []string) bool {
	fact = strings.TrimSpace(fact)
	for _, constraint := range constraints {
		constraint = strings.TrimSpace(constraint)
		if fact != "" && (constraint == fact || strings.Contains(constraint, fact)) {
			return true
		}
	}
	return false
}

func rewriteSourceContext(source *domain.ChapterRewriteSource, body, brief string) map[string]any {
	if source == nil {
		return nil
	}
	return map[string]any{
		"chapter":             source,
		"current_body":        body,
		"brief_markdown":      brief,
		"required_sources":    []string{rewriteSourceToken(source), rewriteBriefToken(source)},
		"preservation_policy": "这是同一章的重新讲述。世界模拟必须逐条覆盖 preserve_facts，正文必须守住金额、秘密边界、关键选择、最终结果和章末后果；旧稿的场景数量、事件顺序、过场动作、对白、非关键角色出场都可以删除、合并、换序或改写，不得把 preserve_facts 逐条渲染成清单。",
	}
}
