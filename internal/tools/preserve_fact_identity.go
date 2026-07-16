package tools

import "strings"

// rewriteFactIdentity returns the exact protected-fact identity used across
// rewrite source, world simulation and chapter planning. Typography-only quote
// changes are equivalent; wording, whitespace, punctuation, number spelling,
// causal order and negation remain significant.
//
// ASCII single quotes are normalized only in pairs. This keeps an unmatched
// apostrophe distinct instead of silently treating it as a quotation mark.
func rewriteFactIdentity(fact string) string {
	runes := []rune(strings.TrimSpace(fact))
	var asciiSingleQuotes []int
	for i, r := range runes {
		switch r {
		case '\'', '’', '‘':
			if r == '\'' {
				asciiSingleQuotes = append(asciiSingleQuotes, i)
				continue
			}
			runes[i] = '"'
		case '"', '“', '”', '「', '」', '『', '』', '＂':
			runes[i] = '"'
		}
	}
	for i := 0; i+1 < len(asciiSingleQuotes); i += 2 {
		runes[asciiSingleQuotes[i]] = '"'
		runes[asciiSingleQuotes[i+1]] = '"'
	}
	return string(runes)
}

// canonicalPreserveFacts keeps authoritative source facts first, with their
// original spelling and order, then appends genuinely distinct model extras.
// Deduplication is deliberately limited to rewriteFactIdentity; it performs no
// fuzzy or semantic matching.
func canonicalPreserveFacts(authoritative, extras []string) []string {
	seen := make(map[string]struct{}, len(authoritative)+len(extras))
	out := make([]string, 0, len(authoritative)+len(extras))
	appendFacts := func(facts []string) {
		for _, fact := range facts {
			fact = strings.TrimSpace(fact)
			identity := rewriteFactIdentity(fact)
			if identity == "" {
				continue
			}
			if _, exists := seen[identity]; exists {
				continue
			}
			seen[identity] = struct{}{}
			out = append(out, fact)
		}
	}
	appendFacts(authoritative)
	appendFacts(extras)
	return out
}
