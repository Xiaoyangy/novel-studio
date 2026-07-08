package rag

import (
	"path/filepath"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

// craftTechniqueSegment 标记可复用写作手法库的目录名。writing-techniques 下是
// 描写词库、史料常识、体系分级、创作方法论等 craft 参考——不携带任何对标作品的
// 情节与设定事实，允许进入写作 RAG 供召回复用；拆解库其余部分（novel_sucai 等
// 对标素材）仍然禁入。
const craftTechniqueSegment = "writing-techniques"

// benchmarkLibrarySegment 标记对标素材库（novel_all：教程/大纲/题材/人设/词汇/
// 爽点/拆文/运营/文笔/心理/场景 11 类归并库）。它携带对标作品的情节与桥段事实，
// 只允许经设计时刻的 craft_recall 检索，且只可迁移手法/结构，禁止照搬情节与人名；
// 归并前的散源（novel_sucai / novel_sucai2 / *.bak）保持禁入，避免重复计数。
const benchmarkLibrarySegment = "novel_all"

// calibrationLibrarySegment 标记审核校准库（review-calibration：AI 检测校准报告、
// 高质量人工文笔样本、创作方法论）。它只能作为显式校准/审阅参考，chunk 会被
// design-only 隔离，不进入 novel_context 常规事实召回。
const calibrationLibrarySegment = "review-calibration"

// IsCraftTechniquePath reports whether a path points at the curated
// writing-techniques craft library, which is exempt from the deconstruction ban.
func IsCraftTechniquePath(path string) bool {
	return pathHasSegment(path, craftTechniqueSegment)
}

// IsCalibrationPath 判断是否审核校准库路径（review-calibration），同样豁免拆解库禁令。
func IsCalibrationPath(path string) bool {
	return pathHasSegment(path, calibrationLibrarySegment)
}

// IsBenchmarkLibraryPath reports whether a path points at the consolidated
// benchmark material library (novel_all), exempt from the deconstruction ban.
func IsBenchmarkLibraryPath(path string) bool {
	return pathHasSegment(path, benchmarkLibrarySegment)
}

func pathHasSegment(path, want string) bool {
	clean := filepath.ToSlash(strings.TrimSpace(path))
	if clean == "" {
		return false
	}
	for _, segment := range strings.Split(clean, "/") {
		if strings.EqualFold(strings.TrimSpace(segment), want) {
			return true
		}
	}
	return false
}

// IsForbiddenSourcePath reports whether a path points at reference/deconstruction
// material that must never enter the writing RAG. RAG is reserved for the active
// novel project's own facts, plans, ledgers, chapter summaries — plus the curated
// writing-techniques craft library (see IsCraftTechniquePath).
func IsForbiddenSourcePath(path string) bool {
	clean := filepath.ToSlash(strings.TrimSpace(path))
	if clean == "" {
		return false
	}
	if IsCraftTechniquePath(clean) || IsBenchmarkLibraryPath(clean) || IsCalibrationPath(clean) {
		return false
	}
	for _, segment := range strings.Split(clean, "/") {
		if isForbiddenRAGSegment(segment) {
			return true
		}
	}
	lower := strings.ToLower(clean)
	return strings.Contains(lower, "data/reference-library")
}

// MentionsForbiddenSourceMarker catches copied path notes inside otherwise
// current-project files. Those notes are not writing facts and should not be
// embedded into RAG chunks. Mentions of the whitelisted writing-techniques
// craft library do not count as forbidden markers.
func MentionsForbiddenSourceMarker(text string) bool {
	clean := filepath.ToSlash(strings.TrimSpace(text))
	if clean == "" {
		return false
	}
	// 剔除 craft 白名单路径的提及后再查禁入标记，
	// 让手法库自身的 chunk（context/text 带库内路径）不被误杀。
	scrub := strings.NewReplacer(
		"deconstruction-library/"+craftTechniqueSegment, "",
		"拆文库/"+craftTechniqueSegment, "",
		"deconstruction-library/"+benchmarkLibrarySegment, "",
		"deconstruction-library/"+calibrationLibrarySegment, "",
	).Replace(clean)
	lower := strings.ToLower(scrub)
	if strings.Contains(scrub, "拆文库") || strings.Contains(scrub, "对标库") ||
		strings.Contains(scrub, "/对标/") || strings.Contains(scrub, "对标/") ||
		strings.Contains(lower, "deconstruction-library") ||
		strings.Contains(lower, "reference-library") || strings.Contains(lower, "reference_library") ||
		strings.Contains(lower, "data/reference-library") {
		return true
	}
	return false
}

// IsForbiddenChunk is the recall-time backstop for old or manually edited index
// states. Even if a stale index_state.json still contains deconstruction chunks,
// novel_context will not surface them to the writer.
func IsForbiddenChunk(chunk domain.RAGChunk) bool {
	if strings.EqualFold(strings.TrimSpace(chunk.SourceKind), "deconstruction") {
		return true
	}
	if IsForbiddenSourcePath(chunk.SourcePath) || IsForbiddenSourcePath(chunk.Context) {
		return true
	}
	if MentionsForbiddenSourceMarker(chunk.Text) || MentionsForbiddenSourceMarker(chunk.Summary) {
		return true
	}
	for _, value := range chunk.Metadata {
		if metadataMentionsForbiddenRAGSource(value) {
			return true
		}
	}
	return false
}

func isForbiddenRAGSegment(segment string) bool {
	segment = strings.TrimSpace(segment)
	if segment == "" {
		return false
	}
	switch segment {
	case "拆文库", "对标":
		return true
	}
	switch strings.ToLower(segment) {
	case "deconstruction-library", "reference-library", "reference_library", "benchmark", "benchmarks":
		return true
	default:
		return false
	}
}

func metadataMentionsForbiddenRAGSource(value any) bool {
	switch v := value.(type) {
	case nil:
		return false
	case string:
		return IsForbiddenSourcePath(v)
	case []string:
		for _, item := range v {
			if IsForbiddenSourcePath(item) {
				return true
			}
		}
	case []any:
		for _, item := range v {
			if metadataMentionsForbiddenRAGSource(item) {
				return true
			}
		}
	case map[string]any:
		for _, item := range v {
			if metadataMentionsForbiddenRAGSource(item) {
				return true
			}
		}
	}
	return false
}
