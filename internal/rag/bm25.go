package rag

import (
	"math"
	"sort"
	"strings"
	"unicode"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

// BM25 关键词检索：与向量召回互补的精确词法通道。
// 中文按二元组（bigram）切分，拉丁/数字按小写单词切分——与 QueryTerms 的
// 2-gram 输出天然兼容，无需额外分词依赖。

const (
	bm25K1 = 1.4
	bm25B  = 0.75
)

// BM25Hit 一条 BM25 命中。
type BM25Hit struct {
	Chunk domain.RAGChunk
	Score float64
}

// BM25Index 基于 chunk 文本构建的内存倒排索引。
type BM25Index struct {
	docs   []bm25Doc
	df     map[string]int
	avgLen float64
}

type bm25Doc struct {
	chunk  domain.RAGChunk
	tf     map[string]int
	length int
}

// TokenizeForBM25 文本切词：CJK 字符产出相邻二元组，其余字符按单词切分并小写。
func TokenizeForBM25(text string) []string {
	var tokens []string
	var latin []rune
	var prevCJK rune

	flushLatin := func() {
		if len(latin) >= 2 {
			tokens = append(tokens, strings.ToLower(string(latin)))
		}
		latin = latin[:0]
	}
	for _, r := range text {
		switch {
		case unicode.Is(unicode.Han, r):
			flushLatin()
			if prevCJK != 0 {
				tokens = append(tokens, string([]rune{prevCJK, r}))
			}
			prevCJK = r
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			prevCJK = 0
			latin = append(latin, r)
		default:
			prevCJK = 0
			flushLatin()
		}
	}
	flushLatin()
	return tokens
}

// BuildBM25Index 对 chunk 集合建索引；禁入 chunk（拆解库 deconstruction-library/旧代来源）直接跳过。
func BuildBM25Index(chunks []domain.RAGChunk) *BM25Index {
	idx := &BM25Index{df: make(map[string]int)}
	total := 0
	for _, chunk := range chunks {
		chunk = NormalizeChunk(chunk)
		if chunk.ID == "" || IsForbiddenChunk(chunk) {
			continue
		}
		tokens := TokenizeForBM25(SearchText(chunk))
		if len(tokens) == 0 {
			continue
		}
		tf := make(map[string]int, len(tokens))
		for _, tok := range tokens {
			tf[tok]++
		}
		for tok := range tf {
			idx.df[tok]++
		}
		idx.docs = append(idx.docs, bm25Doc{chunk: chunk, tf: tf, length: len(tokens)})
		total += len(tokens)
	}
	if len(idx.docs) > 0 {
		idx.avgLen = float64(total) / float64(len(idx.docs))
	}
	return idx
}

// Len 返回索引中的文档数。
func (idx *BM25Index) Len() int { return len(idx.docs) }

// Search 用查询文本检索 top-limit 命中，按 BM25 分值降序。
func (idx *BM25Index) Search(query string, limit int) []BM25Hit {
	if idx == nil || len(idx.docs) == 0 || limit <= 0 {
		return nil
	}
	queryTokens := TokenizeForBM25(query)
	if len(queryTokens) == 0 {
		return nil
	}
	// 查询侧去重：同一 token 重复出现不放大权重（查询短语拼接容易重复）。
	seen := make(map[string]struct{}, len(queryTokens))
	unique := queryTokens[:0]
	for _, tok := range queryTokens {
		if _, ok := seen[tok]; ok {
			continue
		}
		seen[tok] = struct{}{}
		unique = append(unique, tok)
	}

	n := float64(len(idx.docs))
	hits := make([]BM25Hit, 0, limit)
	for _, doc := range idx.docs {
		score := 0.0
		for _, tok := range unique {
			tf, ok := doc.tf[tok]
			if !ok {
				continue
			}
			df := float64(idx.df[tok])
			idf := math.Log(1 + (n-df+0.5)/(df+0.5))
			tfNorm := (float64(tf) * (bm25K1 + 1)) /
				(float64(tf) + bm25K1*(1-bm25B+bm25B*float64(doc.length)/idx.avgLen))
			score += idf * tfNorm
		}
		if score > 0 {
			hits = append(hits, BM25Hit{Chunk: doc.chunk, Score: score})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].Score > hits[j].Score })
	if len(hits) > limit {
		hits = hits[:limit]
	}
	return hits
}
