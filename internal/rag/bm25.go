package rag

import (
	"math"
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
	docs     []domain.RAGChunk
	postings map[string][]bm25Posting
	idf      map[string]float64
}

type bm25Posting struct {
	doc    int
	tfNorm float64
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
	idx := &BM25Index{postings: make(map[string][]bm25Posting), idf: make(map[string]float64)}
	var lengths []int
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
		for tok, count := range tf {
			idx.postings[tok] = append(idx.postings[tok], bm25Posting{doc: len(idx.docs), tfNorm: float64(count)})
		}
		idx.docs = append(idx.docs, chunk)
		lengths = append(lengths, len(tokens))
		total += len(tokens)
	}
	if len(idx.docs) > 0 {
		avgLen := float64(total) / float64(len(idx.docs))
		n := float64(len(idx.docs))
		// The corpus is immutable. Cache IDF and normalized term frequency
		// once so queries only visit matching documents. Keep the final
		// multiply/add together to preserve the original floating-point score.
		for term, postings := range idx.postings {
			df := float64(len(postings))
			idx.idf[term] = math.Log(1 + (n-df+0.5)/(df+0.5))
			for i := range postings {
				tf := postings[i].tfNorm
				tfNorm := (tf * (bm25K1 + 1)) /
					(tf + bm25K1*(1-bm25B+bm25B*float64(lengths[postings[i].doc])/avgLen))
				postings[i].tfNorm = tfNorm
			}
		}
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

	scores := make(map[int]float64)
	for _, tok := range unique {
		idf := idx.idf[tok]
		for _, posting := range idx.postings[tok] {
			scores[posting.doc] += idf * posting.tfNorm
		}
	}
	top := newTopScoredIndices(min(limit, len(scores)), func(a, b scoredIndex) bool {
		if a.score == b.score {
			return a.index < b.index // Preserve corpus order for equal scores.
		}
		return a.score > b.score
	})
	for doc, score := range scores {
		top.add(scoredIndex{index: doc, score: score})
	}
	hits := make([]BM25Hit, 0, len(top.items))
	for _, item := range top.sorted() {
		hits = append(hits, BM25Hit{Chunk: idx.docs[item.index], Score: item.score})
	}
	return hits
}
