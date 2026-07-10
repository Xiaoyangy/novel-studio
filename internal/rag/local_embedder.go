package rag

import (
	"context"
	"fmt"
	"hash/fnv"
	"math"
	"strconv"
	"strings"
	"unicode"
)

const defaultLocalHashEmbeddingDimension = 384

type LocalHashEmbedder struct {
	model     string
	dimension int
}

func NewLocalHashEmbedder(model string) *LocalHashEmbedder {
	model = strings.TrimSpace(model)
	if model == "" {
		model = "local-hash-384"
	}
	return &LocalHashEmbedder{
		model:     model,
		dimension: localHashDimension(model),
	}
}

func (e *LocalHashEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, fmt.Errorf("local embedding input is empty")
	}
	if e.dimension <= 0 {
		e.dimension = defaultLocalHashEmbeddingDimension
	}
	vector := make([]float32, e.dimension)
	terms := localEmbeddingTerms(text)
	for _, term := range terms {
		h := hashTerm(term)
		idx := int(h % uint64(e.dimension))
		sign := float32(1)
		if (h>>63)&1 == 1 {
			sign = -1
		}
		weight := float32(1)
		runeLen := len([]rune(term))
		if runeLen > 4 {
			weight = float32(math.Sqrt(float64(runeLen)))
		}
		vector[idx] += sign * weight
	}
	normalizeVector(vector)
	return vector, nil
}

func localHashDimension(model string) int {
	model = strings.TrimSpace(model)
	if model == "" {
		return defaultLocalHashEmbeddingDimension
	}
	idx := strings.LastIndex(model, "-")
	if idx < 0 || idx == len(model)-1 {
		return defaultLocalHashEmbeddingDimension
	}
	n, err := strconv.Atoi(model[idx+1:])
	if err != nil || n <= 0 {
		return defaultLocalHashEmbeddingDimension
	}
	return n
}

func localEmbeddingTerms(text string) []string {
	seen := map[string]struct{}{}
	var terms []string
	add := func(term string) {
		term = strings.TrimSpace(strings.ToLower(term))
		if len([]rune(term)) < 2 {
			return
		}
		if _, ok := seen[term]; ok {
			return
		}
		seen[term] = struct{}{}
		terms = append(terms, term)
	}
	for _, term := range QueryTerms(text) {
		add(term)
	}
	var buf []rune
	flush := func() {
		if len(buf) == 0 {
			return
		}
		for n := 2; n <= 4; n++ {
			if len(buf) < n {
				continue
			}
			for i := 0; i+n <= len(buf); i++ {
				add(string(buf[i : i+n]))
			}
		}
		buf = buf[:0]
	}
	for _, r := range []rune(text) {
		if unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r) {
			flush()
			continue
		}
		buf = append(buf, unicode.ToLower(r))
	}
	flush()
	if len(terms) == 0 {
		add(text)
	}
	return terms
}

func hashTerm(term string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(term))
	return h.Sum64()
}

func normalizeVector(vector []float32) {
	var sum float64
	for _, v := range vector {
		sum += float64(v * v)
	}
	if sum == 0 {
		return
	}
	scale := float32(1 / math.Sqrt(sum))
	for i := range vector {
		vector[i] *= scale
	}
}
