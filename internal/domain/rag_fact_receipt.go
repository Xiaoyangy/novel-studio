package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	RAGFactReceiptVersion     = 1
	RAGFactReceiptTokenPrefix = "rag_fact_receipt:"
)

// RAGFactReceiptHit is the immutable identity of one project-fact chunk
// selected for a chapter. It intentionally omits raw chunk text: the Planner
// receives the bounded RecallItem summary and must transform any used fact into
// the formal plan before prose rendering.
type RAGFactReceiptHit struct {
	Rank          int    `json:"rank"`
	Ref           string `json:"ref"`
	ChunkID       string `json:"chunk_id"`
	ContentSHA256 string `json:"content_sha256"`
	SourcePath    string `json:"source_path"`
	SourceKind    string `json:"source_kind"`
	Facet         string `json:"facet,omitempty"`
}

// RAGFactReceipt binds one chapter retrieval to the exact selected project
// facts. NoMaterial is a first-class successful result, not a missing receipt.
type RAGFactReceipt struct {
	Version             int                 `json:"version"`
	ID                  string              `json:"id"`
	Chapter             int                 `json:"chapter"`
	Query               string              `json:"query"`
	QueryTerms          []string            `json:"query_terms,omitempty"`
	RetrievalPolicy     string              `json:"retrieval_policy"`
	TraceSHA256         string              `json:"trace_sha256"`
	SelectedFactsSHA256 string              `json:"selected_facts_sha256"`
	PayloadSHA256       string              `json:"payload_sha256"`
	NoMaterial          bool                `json:"no_material"`
	CreatedAt           string              `json:"created_at"`
	Hits                []RAGFactReceiptHit `json:"hits,omitempty"`
}

// NewRAGFactReceipt normalizes a retrieval result and derives a content-addressed
// receipt id. The caller supplies freshly rehashed chunks, never persisted Hash
// fields that may predate a routing or normalization upgrade.
func NewRAGFactReceipt(chapter int, query string, queryTerms []string, retrievalPolicy, traceSHA256 string, hits []RAGFactReceiptHit) (RAGFactReceipt, error) {
	receipt := RAGFactReceipt{
		Version:         RAGFactReceiptVersion,
		Chapter:         chapter,
		Query:           strings.TrimSpace(query),
		QueryTerms:      compactRAGReceiptStrings(queryTerms),
		RetrievalPolicy: strings.TrimSpace(retrievalPolicy),
		TraceSHA256:     strings.TrimSpace(traceSHA256),
		CreatedAt:       time.Now().UTC().Format(time.RFC3339),
		Hits:            normalizeRAGFactReceiptHits(hits),
	}
	receipt.NoMaterial = len(receipt.Hits) == 0
	if receipt.NoMaterial && receipt.RetrievalPolicy == "" {
		receipt.RetrievalPolicy = "no_material_v1"
	}
	if receipt.TraceSHA256 == "" {
		receipt.TraceSHA256 = hashRAGFactReceiptValue(struct {
			Query  string   `json:"query"`
			Terms  []string `json:"query_terms,omitempty"`
			Policy string   `json:"retrieval_policy"`
		}{receipt.Query, receipt.QueryTerms, receipt.RetrievalPolicy})
	}
	receipt.SelectedFactsSHA256 = selectedRAGFactReceiptHash(receipt.Hits)
	receipt.PayloadSHA256 = ragFactReceiptPayloadHash(receipt)
	receipt.ID = receipt.PayloadSHA256[:24]
	for i := range receipt.Hits {
		receipt.Hits[i].Ref = RAGFactReceiptHitRef(receipt.ID, receipt.Hits[i])
	}
	if err := ValidateRAGFactReceipt(receipt); err != nil {
		return RAGFactReceipt{}, err
	}
	return receipt, nil
}

func (r RAGFactReceipt) SourceToken() string {
	if strings.TrimSpace(r.ID) == "" || strings.TrimSpace(r.SelectedFactsSHA256) == "" {
		return ""
	}
	return fmt.Sprintf("%s%s#facts_sha256=%s", RAGFactReceiptTokenPrefix, r.ID, r.SelectedFactsSHA256)
}

func RAGFactReceiptHitRef(receiptID string, hit RAGFactReceiptHit) string {
	return fmt.Sprintf("%s%s#chunk=%s#hash=%s", RAGFactReceiptTokenPrefix, strings.TrimSpace(receiptID), strings.TrimSpace(hit.ChunkID), strings.TrimSpace(hit.ContentSHA256))
}

func ValidateRAGFactReceipt(receipt RAGFactReceipt) error {
	if receipt.Version != RAGFactReceiptVersion {
		return fmt.Errorf("unsupported RAG fact receipt version %d", receipt.Version)
	}
	if receipt.Chapter <= 0 || strings.TrimSpace(receipt.Query) == "" || strings.TrimSpace(receipt.RetrievalPolicy) == "" {
		return fmt.Errorf("RAG fact receipt requires chapter, query, and retrieval_policy")
	}
	if len(receipt.ID) != 24 || !isLowerHex(receipt.ID) {
		return fmt.Errorf("invalid RAG fact receipt id %q", receipt.ID)
	}
	for name, value := range map[string]string{
		"trace_sha256":          receipt.TraceSHA256,
		"selected_facts_sha256": receipt.SelectedFactsSHA256,
		"payload_sha256":        receipt.PayloadSHA256,
	} {
		if len(value) != 64 || !isLowerHex(value) {
			return fmt.Errorf("RAG fact receipt %s must be a lowercase SHA-256", name)
		}
	}
	if receipt.NoMaterial != (len(receipt.Hits) == 0) {
		return fmt.Errorf("RAG fact receipt no_material does not match hit count")
	}
	normalized := normalizeRAGFactReceiptHits(receipt.Hits)
	if len(normalized) != len(receipt.Hits) {
		return fmt.Errorf("RAG fact receipt contains duplicate or incomplete hits")
	}
	for i := range normalized {
		if normalized[i].Rank != receipt.Hits[i].Rank ||
			normalized[i].ChunkID != receipt.Hits[i].ChunkID ||
			normalized[i].ContentSHA256 != receipt.Hits[i].ContentSHA256 {
			return fmt.Errorf("RAG fact receipt hits are not normalized")
		}
		wantRef := RAGFactReceiptHitRef(receipt.ID, receipt.Hits[i])
		if receipt.Hits[i].Ref != wantRef {
			return fmt.Errorf("RAG fact receipt hit %d ref mismatch", i)
		}
		if len(receipt.Hits[i].ContentSHA256) != 64 || !isLowerHex(receipt.Hits[i].ContentSHA256) {
			return fmt.Errorf("RAG fact receipt hit %d content_sha256 is invalid", i)
		}
	}
	if got := selectedRAGFactReceiptHash(receipt.Hits); receipt.SelectedFactsSHA256 != got {
		return fmt.Errorf("RAG fact receipt selected facts hash mismatch")
	}
	if got := ragFactReceiptPayloadHash(receipt); receipt.PayloadSHA256 != got || receipt.ID != got[:24] {
		return fmt.Errorf("RAG fact receipt payload hash mismatch")
	}
	return nil
}

func normalizeRAGFactReceiptHits(in []RAGFactReceiptHit) []RAGFactReceiptHit {
	out := make([]RAGFactReceiptHit, 0, len(in))
	seen := make(map[string]struct{}, len(in))
	for _, hit := range in {
		hit.Ref = strings.TrimSpace(hit.Ref)
		hit.ChunkID = strings.TrimSpace(hit.ChunkID)
		hit.ContentSHA256 = strings.TrimSpace(hit.ContentSHA256)
		hit.SourcePath = strings.TrimSpace(hit.SourcePath)
		hit.SourceKind = strings.TrimSpace(hit.SourceKind)
		hit.Facet = strings.TrimSpace(hit.Facet)
		if hit.ChunkID == "" || hit.ContentSHA256 == "" || hit.SourcePath == "" {
			continue
		}
		if _, duplicate := seen[hit.ChunkID]; duplicate {
			continue
		}
		seen[hit.ChunkID] = struct{}{}
		out = append(out, hit)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Rank != out[j].Rank {
			return out[i].Rank < out[j].Rank
		}
		return out[i].ChunkID < out[j].ChunkID
	})
	for i := range out {
		out[i].Rank = i + 1
	}
	return out
}

func selectedRAGFactReceiptHash(hits []RAGFactReceiptHit) string {
	type selectedHit struct {
		Rank          int    `json:"rank"`
		ChunkID       string `json:"chunk_id"`
		ContentSHA256 string `json:"content_sha256"`
		SourcePath    string `json:"source_path"`
		SourceKind    string `json:"source_kind"`
		Facet         string `json:"facet,omitempty"`
	}
	values := make([]selectedHit, 0, len(hits))
	for _, hit := range normalizeRAGFactReceiptHits(hits) {
		values = append(values, selectedHit{
			Rank: hit.Rank, ChunkID: hit.ChunkID, ContentSHA256: hit.ContentSHA256,
			SourcePath: hit.SourcePath, SourceKind: hit.SourceKind, Facet: hit.Facet,
		})
	}
	return hashRAGFactReceiptValue(values)
}

func ragFactReceiptPayloadHash(receipt RAGFactReceipt) string {
	type payload struct {
		Version             int                 `json:"version"`
		Chapter             int                 `json:"chapter"`
		Query               string              `json:"query"`
		QueryTerms          []string            `json:"query_terms,omitempty"`
		RetrievalPolicy     string              `json:"retrieval_policy"`
		TraceSHA256         string              `json:"trace_sha256"`
		SelectedFactsSHA256 string              `json:"selected_facts_sha256"`
		NoMaterial          bool                `json:"no_material"`
		Hits                []RAGFactReceiptHit `json:"hits,omitempty"`
	}
	hits := normalizeRAGFactReceiptHits(receipt.Hits)
	for i := range hits {
		hits[i].Ref = ""
	}
	return hashRAGFactReceiptValue(payload{
		Version: receipt.Version, Chapter: receipt.Chapter, Query: strings.TrimSpace(receipt.Query),
		QueryTerms: compactRAGReceiptStrings(receipt.QueryTerms), RetrievalPolicy: strings.TrimSpace(receipt.RetrievalPolicy),
		TraceSHA256: strings.TrimSpace(receipt.TraceSHA256), SelectedFactsSHA256: strings.TrimSpace(receipt.SelectedFactsSHA256),
		NoMaterial: receipt.NoMaterial, Hits: hits,
	})
}

func hashRAGFactReceiptValue(value any) string {
	raw, _ := json.Marshal(value)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func compactRAGReceiptStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func isLowerHex(value string) bool {
	for _, r := range value {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return value != ""
}
