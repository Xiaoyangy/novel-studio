package tools

import (
	"math"
	"path"
	"sort"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/rag"
)

const (
	// A diverse result is still useless when it is only weakly related to the
	// query. Keep the selector relative to the best hybrid score so it works for
	// BM25-only, vector-only and additive hybrid strategies without a new config.
	ragRecallRelativeScoreFloor = 0.30
	ragRecallMMRRelevanceWeight = 0.82
	ragRecallSameSourcePenalty  = 0.12
	ragRecallNearDuplicate      = 0.92
)

// selectDiverseRAGRecall performs the final top-k selection after the existing
// vector/BM25 score merge. It deliberately does not change backend scores:
// relevance supplies a hard floor, while source and content diversity decide
// which of the still-relevant candidates receive the scarce context slots.
func selectDiverseRAGRecall(scoredByID map[string]*ragScored, limit int) []ragScored {
	if limit <= 0 || len(scoredByID) == 0 {
		return nil
	}
	candidates := make([]ragScored, 0, len(scoredByID))
	for _, item := range scoredByID {
		if item == nil || item.score <= 0 || math.IsNaN(item.score) || math.IsInf(item.score, 0) {
			continue
		}
		candidates = append(candidates, *item)
	}
	sort.Slice(candidates, func(i, j int) bool {
		return ragScoredLess(candidates[i], candidates[j])
	})
	if len(candidates) == 0 {
		return nil
	}

	topScore := candidates[0].score
	floor := topScore * ragRecallRelativeScoreFloor
	eligible := candidates[:0]
	for _, candidate := range candidates {
		if candidate.score+1e-12 < floor {
			break
		}
		eligible = append(eligible, candidate)
	}
	if len(eligible) == 0 {
		return nil
	}

	selected := make([]ragScored, 0, min(limit, len(eligible)))
	selected = append(selected, eligible[0]) // the strongest result is never displaced
	used := make([]bool, len(eligible))
	used[0] = true
	sourceCounts := map[string]int{canonicalRAGSourceKey(eligible[0].chunk.SourcePath): 1}
	familyCounts := map[string]int{}
	if family := ragSourceFamily(eligible[0].chunk.SourcePath); family != "" {
		familyCounts[family] = 1
	}
	fingerprints := make(map[string]map[string]struct{}, len(eligible))

	for len(selected) < limit {
		best := -1
		bestValue := math.Inf(-1)
		for i := range eligible {
			if used[i] {
				continue
			}
			candidate := eligible[i]
			family := ragSourceFamily(candidate.chunk.SourcePath)
			// Flat, layered and accepted outline copies are alternate views of
			// the same authority. One high-scoring view is enough because the
			// current chapter outline is already loaded explicitly elsewhere.
			if family == "project_outline" && familyCounts[family] > 0 {
				continue
			}

			maxSimilarity := 0.0
			for _, picked := range selected {
				similarity := ragChunkContentSimilarity(candidate.chunk, picked.chunk, fingerprints)
				if similarity > maxSimilarity {
					maxSimilarity = similarity
				}
			}
			if maxSimilarity >= ragRecallNearDuplicate {
				continue
			}

			relevance := candidate.score / topScore
			novelty := 1 - maxSimilarity
			sourceKey := canonicalRAGSourceKey(candidate.chunk.SourcePath)
			value := ragRecallMMRRelevanceWeight*relevance +
				(1-ragRecallMMRRelevanceWeight)*novelty -
				ragRecallSameSourcePenalty*float64(sourceCounts[sourceKey])
			if value > bestValue+1e-12 ||
				(math.Abs(value-bestValue) <= 1e-12 && (best < 0 || ragScoredLess(candidate, eligible[best]))) {
				best = i
				bestValue = value
			}
		}
		if best < 0 {
			break
		}
		picked := eligible[best]
		used[best] = true
		selected = append(selected, picked)
		sourceCounts[canonicalRAGSourceKey(picked.chunk.SourcePath)]++
		if family := ragSourceFamily(picked.chunk.SourcePath); family != "" {
			familyCounts[family]++
		}
	}
	return selected
}

func ragScoredLess(a, b ragScored) bool {
	if math.Abs(a.score-b.score) > 1e-12 {
		return a.score > b.score
	}
	aSource := canonicalRAGSourceKey(a.chunk.SourcePath)
	bSource := canonicalRAGSourceKey(b.chunk.SourcePath)
	if aSource != bSource {
		return aSource < bSource
	}
	if a.chunk.ID != b.chunk.ID {
		return a.chunk.ID < b.chunk.ID
	}
	if a.chunk.Hash != b.chunk.Hash {
		return a.chunk.Hash < b.chunk.Hash
	}
	return a.chunk.SourcePath < b.chunk.SourcePath
}

// canonicalRAGSourceKey collapses the two source forms produced by full builds
// and incremental project-memory upserts, for example:
//
//	data/runs/book/output/novel/outline.md -> outline.md
//	outline.md                            -> outline.md
//
// Paths outside an output/novel root keep their library-relative hierarchy so
// craft/reference/calibration sources from different files remain diverse.
func canonicalRAGSourceKey(sourcePath string) string {
	source := strings.ToLower(strings.TrimSpace(strings.ReplaceAll(sourcePath, "\\", "/")))
	if source == "" {
		return "unknown"
	}
	if marker := "/output/novel/"; strings.Contains(source, marker) {
		source = source[strings.LastIndex(source, marker)+len(marker):]
	} else if strings.HasPrefix(source, "output/novel/") {
		source = strings.TrimPrefix(source, "output/novel/")
	}
	source = strings.TrimPrefix(source, "./")
	clean := path.Clean(source)
	if clean == "." || clean == "/" {
		return "unknown"
	}
	return strings.TrimPrefix(clean, "/")
}

func ragSourceFamily(sourcePath string) string {
	base := path.Base(canonicalRAGSourceKey(sourcePath))
	stem := strings.TrimSuffix(base, path.Ext(base))
	stem = strings.NewReplacer("-", "_", " ", "_", ".", "_").Replace(stem)
	stem = strings.Trim(stem, "_")
	switch stem {
	case "outline", "layered_outline", "accepted_outline", "outline_accepted", "final_outline", "approved_outline":
		return "project_outline"
	}
	if strings.HasPrefix(stem, "accepted_outline_") || strings.HasPrefix(stem, "approved_outline_") ||
		strings.HasSuffix(stem, "_accepted_outline") || strings.HasSuffix(stem, "_approved_outline") {
		return "project_outline"
	}
	return ""
}

func ragChunkContentSimilarity(a, b domain.RAGChunk, cache map[string]map[string]struct{}) float64 {
	aTokens := ragChunkFingerprint(a, cache)
	bTokens := ragChunkFingerprint(b, cache)
	if len(aTokens) == 0 || len(bTokens) == 0 {
		return 0
	}
	intersection := 0
	for token := range aTokens {
		if _, ok := bTokens[token]; ok {
			intersection++
		}
	}
	union := len(aTokens) + len(bTokens) - intersection
	if union <= 0 {
		return 0
	}
	return float64(intersection) / float64(union)
}

func ragChunkFingerprint(chunk domain.RAGChunk, cache map[string]map[string]struct{}) map[string]struct{} {
	key := chunk.ID
	if key == "" {
		key = chunk.Hash + "\x00" + chunk.SourcePath
	}
	if tokens, ok := cache[key]; ok {
		return tokens
	}
	text := strings.Join([]string{chunk.Context, chunk.Summary, chunk.Text}, "\n")
	tokens := make(map[string]struct{})
	for _, token := range rag.TokenizeForBM25(text) {
		if token != "" {
			tokens[token] = struct{}{}
		}
	}
	cache[key] = tokens
	return tokens
}
