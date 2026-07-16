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
	canonicalFacts, canonicalPath, canonicalSHA256, err := rewriteCanonicalOutcomeFacts(s, chapter)
	if err != nil {
		return nil, "", "", fmt.Errorf("load rewrite canonical outcomes: %w", err)
	}
	sum := sha256.Sum256([]byte(body))
	briefSum := sha256.Sum256(briefRaw)
	preserveFacts := canonicalPreserveFacts(rewriteBriefPreserveFacts(brief), canonicalFacts)
	source := &domain.ChapterRewriteSource{
		BodyPath:             bodyPath,
		BodySHA256:           hex.EncodeToString(sum[:]),
		WordCount:            utf8.RuneCountInString(body),
		BriefPath:            briefPath,
		BriefSHA256:          hex.EncodeToString(briefSum[:]),
		CanonicalStatePath:   canonicalPath,
		CanonicalStateSHA256: canonicalSHA256,
		PreserveFacts:        preserveFacts,
	}
	return source, body, brief, nil
}

// rewriteCanonicalOutcomeFacts protects result-level canon that an editorial
// rewrite brief may omit. chapter_progress is explicitly the already-written
// fact ledger; only state/resource outcomes are projected here. Timeline prose,
// summaries, and reasons are deliberately excluded because a review may be
// correcting their causal order while keeping the booked result unchanged.
func rewriteCanonicalOutcomeFacts(s *store.Store, chapter int) ([]string, string, string, error) {
	if s == nil || chapter <= 0 {
		return nil, "", "", nil
	}
	const relPath = "meta/chapter_progress.json"
	raw, err := os.ReadFile(filepath.Join(s.Dir(), filepath.FromSlash(relPath)))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, "", "", nil
		}
		return nil, "", "", err
	}
	ledger, err := s.LoadChapterProgressLedger()
	if err != nil {
		return nil, "", "", err
	}
	if ledger == nil {
		return nil, "", "", nil
	}
	var facts []string
	for _, entry := range ledger.Entries {
		if entry.Chapter != chapter {
			continue
		}
		for _, change := range entry.StateChanges {
			entity, field, value := strings.TrimSpace(change.Entity), strings.TrimSpace(change.Field), strings.TrimSpace(change.NewValue)
			if entity == "" || field == "" || value == "" {
				continue
			}
			facts = appendUniqueString(facts, fmt.Sprintf("已提交状态结果：%s.%s = %s", entity, field, value))
		}
		for _, claim := range entry.ResourceChanges {
			name, status := strings.TrimSpace(claim.Name), strings.TrimSpace(claim.Status)
			if name == "" || status == "" {
				continue
			}
			parts := []string{fmt.Sprintf("已提交资源结果：%s；status=%s", name, status)}
			if risk := strings.TrimSpace(claim.Risk); risk != "" {
				parts = append(parts, "边界="+risk)
			}
			if evidence := strings.TrimSpace(claim.Evidence); evidence != "" {
				parts = append(parts, "证据="+evidence)
			}
			facts = appendUniqueString(facts, strings.Join(parts, "；"))
		}
		break
	}
	sum := sha256.Sum256(raw)
	return facts, relPath, hex.EncodeToString(sum[:]), nil
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
		facts = canonicalPreserveFacts(facts, []string{line})
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

// rewriteBriefTopLevelBullets returns only non-indented bullets from selected
// H2 sections. Nested evidence and fix notes remain available in the full
// brief, while the plan receives a compact set of hard failure/acceptance
// anchors. A heading prefix also matches a parenthesized date suffix.
func rewriteBriefTopLevelBullets(markdown string, headingPrefixes ...string) []string {
	lines := strings.Split(strings.ReplaceAll(markdown, "\r\n", "\n"), "\n")
	active := false
	fence := ""
	var items []string
	for _, raw := range lines {
		trimmed := strings.TrimSpace(raw)
		if marker := rewriteBriefFenceMarker(trimmed); marker != "" {
			if fence == "" {
				fence = marker
			} else if fence == marker {
				fence = ""
			}
			continue
		}
		if fence != "" {
			continue
		}
		if level, heading, ok := parseMarkdownATXHeading(raw); ok {
			active = false
			if level == 2 {
				for _, prefix := range headingPrefixes {
					if rewriteBriefRefinementHeadingMatches(heading, strings.TrimSpace(prefix)) {
						active = true
						break
					}
				}
			}
			continue
		}
		if !active || !strings.HasPrefix(raw, "- ") {
			continue
		}
		item := strings.TrimSpace(strings.TrimPrefix(raw, "- "))
		if item != "" && !rewriteBriefEmptyItem(item) &&
			!strings.HasPrefix(item, "说明：") && !strings.HasPrefix(item, "说明:") {
			items = appendUniqueString(items, item)
		}
	}
	return items
}

var rewriteBriefDatedGateHeadingPattern = regexp.MustCompile(`^最新整篇单段门禁(?:（[0-9]{4}-[0-9]{2}-[0-9]{2}）| \([0-9]{4}-[0-9]{2}-[0-9]{2}\))$`)

func rewriteBriefRefinementHeadingMatches(heading, prefix string) bool {
	if heading == prefix {
		return true
	}
	return prefix == "最新整篇单段门禁" && rewriteBriefDatedGateHeadingPattern.MatchString(heading)
}

func rewriteBriefFenceMarker(line string) string {
	if strings.HasPrefix(line, "```") {
		return "```"
	}
	if strings.HasPrefix(line, "~~~") {
		return "~~~"
	}
	return ""
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
		a.BriefPath != b.BriefPath || a.BriefSHA256 != b.BriefSHA256 ||
		a.CanonicalStatePath != b.CanonicalStatePath || a.CanonicalStateSHA256 != b.CanonicalStateSHA256 {
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
	identity := rewriteFactIdentity(fact)
	if identity == "" {
		return false
	}
	for _, constraint := range constraints {
		if rewriteFactIdentity(constraint) == identity {
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
		"preservation_policy": "这是同一章的重新讲述。preserve_facts 同时来自 rewrite brief 与已提交 chapter_progress 结果台账；世界模拟必须逐条覆盖，正文必须守住金额、数量、资源状态、秘密边界、关键选择、最终结果和章末后果。评审明确纠正的因果顺序优先于旧摘要措辞；旧稿的场景数量、过场动作、对白、非关键角色出场可以删除、合并、换序或改写，不得把 preserve_facts 逐条渲染成清单。",
	}
}
