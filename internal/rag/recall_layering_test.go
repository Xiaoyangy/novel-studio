package rag

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

// loadGhostcityIndex 读取鬼城真实索引；不存在时跳过（CI 环境无此文件）。
func loadGhostcityIndex(t *testing.T) *domain.RAGIndexState {
	t.Helper()
	data, err := os.ReadFile("../../data/runs/鬼城/output/novel/meta/rag/index_state.json")
	if err != nil {
		t.Skip("no real ghostcity index")
	}
	var state domain.RAGIndexState
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	return &state
}

// TestGhostcityIndexLayering 实索引分层审计：
// 每个 chunk 必须落在三层之一且标记完整——
//
//	note（本书事实）/ craft_technique（手法库）/ benchmark_reference（对标库）。
func TestGhostcityIndexLayering(t *testing.T) {
	state := loadGhostcityIndex(t)
	for _, c := range state.Chunks {
		kind := strings.TrimSpace(c.SourceKind)
		switch {
		case strings.EqualFold(kind, CraftSourceKind):
			if !IsCraftTechniquePath(c.SourcePath) {
				t.Errorf("craft chunk %s 路径不在手法库: %s", c.ID, c.SourcePath)
			}
		case strings.EqualFold(kind, BenchmarkSourceKind):
			if !IsBenchmarkLibraryPath(c.SourcePath) {
				t.Errorf("benchmark chunk %s 路径不在对标库: %s", c.ID, c.SourcePath)
			}
		default:
			// 事实层：不得指向两个设计库
			if IsCraftTechniquePath(c.SourcePath) || IsBenchmarkLibraryPath(c.SourcePath) {
				t.Errorf("事实层 chunk %s (kind=%s) 却指向设计库: %s", c.ID, kind, c.SourcePath)
			}
			if IsForbiddenChunk(c) {
				t.Errorf("事实层 chunk %s 命中禁入规则: %s", c.ID, c.SourcePath)
			}
		}
	}
}

// TestGhostcityFactRecallIsolation 事实召回隔离：模拟 novel_context 的词法通道
// （排除设计库后建 BM25），任何查询结果必须全部是本书事实层。
func TestGhostcityFactRecallIsolation(t *testing.T) {
	state := loadGhostcityIndex(t)
	factChunks := make([]domain.RAGChunk, 0, len(state.Chunks))
	for _, c := range state.Chunks {
		if IsDesignOnlySourceKind(c.SourceKind) {
			continue
		}
		factChunks = append(factChunks, c)
	}
	idx := BuildBM25Index(factChunks)
	for _, q := range []string{"冥府黑卡 夜租", "长剑 淬火 名剑", "眼睛 描写 冷峻", "爽点 打脸 反转"} {
		for _, hit := range idx.Search(q, 5) {
			if IsDesignOnlySourceKind(hit.Chunk.SourceKind) {
				t.Errorf("事实通道泄漏设计库 chunk: query=%q path=%s", q, hit.Chunk.SourcePath)
			}
		}
	}
}

// TestGhostcityCraftRoutingAllFields 全字段确定性路由：每个设计字段要么命中
// 且首条来自正确的库/类目，要么显式 no_material（可审计，不静默）。
func TestGhostcityCraftRoutingAllFields(t *testing.T) {
	state := loadGhostcityIndex(t)
	cases := []struct {
		field     CraftDesignField
		topic     string
		wantInTop string // 首条 source_path 必须包含的片段；空=只要求命中
	}{
		{CraftFieldWeapon, "长剑 淬火", "writing-techniques"},
		{CraftFieldAppearance, "眼睛 冷峻", "writing-techniques/appearance"},
		{CraftFieldDialogue, "对白 交涉 信息博弈", ""},
		{CraftFieldAbility, "阶位 神格", "writing-techniques"},
		{CraftFieldSkill, "阵法 雷法", "writing-techniques/magic-arts"},
		{CraftFieldInstitution, "物价 银子", "writing-techniques/ancient-history"},
		{CraftFieldTechnology, "星际 武器", "writing-techniques/scifi"},
		{CraftFieldCosmology, "位面 世界构成", ""},
		{CraftFieldMethodology, "世界构建", ""},
		{CraftFieldOutlineSample, "大纲 填表", "novel_all"},
		{CraftFieldTrope, "题材 套路", "novel_all"},
		{CraftFieldPersona, "反派 人设", "novel_all"},
		{CraftFieldLexicon, "替换词 描写", "novel_all"},
		{CraftFieldPlotBeats, "爽点 打脸", "novel_all"},
		{CraftFieldBenchmark, "拆文 节奏", "novel_all"},
		{CraftFieldMarket, "投稿 渠道", "novel_all"},
		{CraftFieldSceneCraft, "场景 情境", "novel_all"},
	}
	for _, c := range cases {
		res := CraftRecall(state.Chunks, c.field, c.topic, 3)
		if res.NoMaterial {
			t.Errorf("field %s: no_material（索引缺该类目素材）", c.field)
			continue
		}
		top := res.Hits[0].Chunk.SourcePath
		if c.wantInTop != "" && !strings.Contains(top, c.wantInTop) {
			t.Errorf("field %s: 首条 %s 不含 %q", c.field, top, c.wantInTop)
		}
		// 路由纯净性：命中集内不得混入其他库
		recipe := craftFieldRecipes[c.field]
		allowed := recipe.SourceKinds
		if len(allowed) == 0 {
			allowed = []string{CraftSourceKind}
		}
		for _, hit := range res.Hits {
			if !containsFold(allowed, hit.Chunk.SourceKind) {
				t.Errorf("field %s: 混入 %s (%s)", c.field, hit.Chunk.SourceKind, hit.Chunk.SourcePath)
			}
		}
	}
}

// TestGhostcityNoStaleSources 索引内每个本书事实 chunk 的源文件必须存在于磁盘
// （设计库路径按仓库根解析）。防止召回已删除代次的旧事实。
func TestGhostcityNoStaleSources(t *testing.T) {
	state := loadGhostcityIndex(t)
	stale := 0
	for _, c := range state.Chunks {
		if isSyntheticAuditChunk(c) {
			continue
		}
		sp := c.SourcePath
		var candidates []string
		switch {
		case strings.HasPrefix(sp, "deconstruction-library/"):
			candidates = append(candidates, "../../"+sp)
		case strings.HasPrefix(sp, "data/runs/"):
			candidates = append(candidates, "../../"+sp)
		default:
			// RAG source_path 对本书事实层通常相对 output/novel；旧索引里也可能有
			// 相对 run 根的路径，两个都检查，避免把健康 chunk 误判为陈旧。
			candidates = append(candidates,
				"../../data/runs/鬼城/output/novel/"+sp,
				"../../data/runs/鬼城/"+sp,
			)
		}
		found := false
		for _, path := range candidates {
			if _, err := os.Stat(path); err == nil {
				found = true
				break
			}
		}
		if !found {
			stale++
			if stale <= 5 {
				t.Errorf("陈旧 chunk：%s（源文件不存在）", sp)
			}
		}
	}
	if stale > 5 {
		t.Errorf("...陈旧 chunk 共 %d 个", stale)
	}
}

func isSyntheticAuditChunk(c domain.RAGChunk) bool {
	kind := strings.ToLower(strings.TrimSpace(c.SourceKind))
	if kind != "chapter_rewrite" && kind != "review" {
		return false
	}
	if strings.Contains(strings.ToLower(filepath.ToSlash(c.SourcePath)), "rewrite_existing/") {
		return true
	}
	if c.Metadata != nil {
		if source, ok := c.Metadata["source"].(string); ok && strings.EqualFold(strings.TrimSpace(source), "rewrite_existing") {
			return true
		}
	}
	return false
}
