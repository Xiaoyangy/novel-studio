package tools

import (
	"strconv"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

// ApplyProjectAllOutlineObligations exposes the same deterministic host-outline
// projection used by both plan finalization paths. It performs no persistence.
func ApplyProjectAllOutlineObligations(plan *domain.ChapterPlan, scenes []string) {
	applyProjectAllOutlineObligations(plan, scenes)
}

// These reserved tags are produced only by the verified source-qualified v2
// registry projection. Old tags and arbitrary bracketed user prose never pass
// this decoder. No hash or origin chapter becomes a story quantity.
func projectAllV2ObligationProse(scene string, hard bool) (string, bool) {
	prefix := "[project-all v2-simulation-obligation:"
	if hard {
		prefix = "[project-all v2-hard-obligation:"
	}
	if !strings.HasPrefix(scene, prefix) {
		return "", false
	}
	rest := strings.TrimPrefix(scene, prefix)
	end := strings.Index(rest, "] ")
	if end < 0 {
		return "", false
	}
	identity := strings.Split(rest[:end], ":")
	if len(identity) != 4 || identity[0] != "obl" || identity[1] != "character" || len(identity[3]) != 12 {
		return "", false
	}
	chapter, err := strconv.Atoi(identity[2])
	if err != nil || chapter <= 0 || strconv.Itoa(chapter) != identity[2] {
		return "", false
	}
	for _, digit := range identity[3] {
		if !(digit >= '0' && digit <= '9') && !(digit >= 'a' && digit <= 'f') {
			return "", false
		}
	}
	outcome := strings.TrimSpace(rest[end+2:])
	return outcome, outcome != ""
}
