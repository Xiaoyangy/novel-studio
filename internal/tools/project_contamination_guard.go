package tools

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/errs"
	"github.com/chenhongyang/novel-studio/internal/rules"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func validateProjectContaminationFree(s *store.Store, label string, payload any) error {
	return validateProjectContamination(s, label, payload, false)
}

func validateProjectContaminationFinal(s *store.Store, label string, payload any) error {
	return validateProjectContamination(s, label, payload, true)
}

func validateProjectContamination(s *store.Store, label string, payload any, _ bool) error {
	terms := projectContaminationTerms(s)
	if len(terms) == 0 {
		return nil
	}
	text := payloadText(payload)
	if hits := orderedTermHits(text, terms); len(hits) > 0 {
		return fmt.Errorf("%s 命中当前项目 user_rules.forbidden_phrases：%s；请只使用本项目已冻结的大纲、角色卡、世界规则和用户规则中的事实: %w",
			label, strings.Join(hits, "、"), errs.ErrToolPrecondition)
	}
	return nil
}

func containsProjectContaminationTerm(terms []string, text string) bool {
	return len(orderedTermHits(text, terms)) > 0
}

// sanitizeProjectDiagnosticForPlan keeps a negative rewrite diagnosis useful
// without reintroducing forbidden cross-project nouns into the executable
// plan. The vocabulary comes exclusively from this project's persisted user
// rules; the full review brief remains unchanged as the audit source.
func sanitizeProjectDiagnosticForPlan(s *store.Store, text string) string {
	terms := projectContaminationTerms(s)
	if len(terms) == 0 || strings.TrimSpace(text) == "" {
		return strings.TrimSpace(text)
	}
	for _, term := range terms {
		text = strings.ReplaceAll(text, term, "[项目禁用元素]")
	}
	return strings.TrimSpace(text)
}

// ProjectContaminationViolations reports only terms explicitly persisted in
// this project's user_rules.forbidden_phrases. It never selects a policy by
// title, protagonist, genre keyword, or stale generated output.
func ProjectContaminationViolations(s *store.Store, text string) []rules.Violation {
	terms := projectContaminationTerms(s)
	if len(terms) == 0 {
		return nil
	}
	var out []rules.Violation
	for _, term := range sortedHitTerms(countTermHits(text, terms)) {
		out = append(out, rules.Violation{
			Rule:     "project_contamination",
			Target:   term,
			Actual:   strings.Count(text, term),
			Severity: rules.SeverityError,
		})
	}
	return out
}

// ProjectContaminationPolicyDigest identifies the exact project-owned phrase
// boundary currently enforced by the contamination guard. Callers that cache
// sanitation results must bind their cache marker to this digest so changing
// user_rules.forbidden_phrases forces a fresh scan of persisted material.
func ProjectContaminationPolicyDigest(s *store.Store) string {
	hasher := sha256.New()
	_, _ = hasher.Write([]byte("project-contamination/user-rules-forbidden-phrases/v1"))
	for _, term := range projectContaminationTerms(s) {
		_, _ = hasher.Write([]byte{0})
		_, _ = hasher.Write([]byte(term))
	}
	return fmt.Sprintf("sha256:%x", hasher.Sum(nil))
}

func orderedTermHits(text string, terms []string) []string {
	var hits []string
	for _, term := range terms {
		if strings.Contains(text, term) {
			hits = append(hits, term)
		}
	}
	return hits
}

func countTermHits(text string, terms []string) map[string]int {
	out := map[string]int{}
	for _, term := range terms {
		if term == "" {
			continue
		}
		if n := strings.Count(text, term); n > 0 {
			out[term] = n
		}
	}
	return out
}

func sortedHitTerms(counts map[string]int) []string {
	hits := make([]string, 0, len(counts))
	for term := range counts {
		hits = append(hits, term)
	}
	sort.Strings(hits)
	return hits
}

func projectContaminationTerms(s *store.Store) []string {
	if s == nil || s.UserRules == nil {
		return nil
	}
	snapshot, err := s.UserRules.Load()
	if err != nil || snapshot == nil {
		return nil
	}
	seen := make(map[string]struct{}, len(snapshot.Structured.ForbiddenPhrases))
	terms := make([]string, 0, len(snapshot.Structured.ForbiddenPhrases))
	for _, raw := range snapshot.Structured.ForbiddenPhrases {
		term := strings.TrimSpace(raw)
		if term == "" {
			continue
		}
		if _, ok := seen[term]; ok {
			continue
		}
		seen[term] = struct{}{}
		terms = append(terms, term)
	}
	// Replace longer phrases first so nested terms retain one stable redaction.
	sort.SliceStable(terms, func(i, j int) bool {
		if len([]rune(terms[i])) != len([]rune(terms[j])) {
			return len([]rune(terms[i])) > len([]rune(terms[j]))
		}
		return terms[i] < terms[j]
	})
	return terms
}

func payloadText(payload any) string {
	switch v := payload.(type) {
	case string:
		return v
	case []byte:
		return string(v)
	default:
		data, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprint(v)
		}
		return string(data)
	}
}
