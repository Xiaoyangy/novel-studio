package rag

import (
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

func bm25TestChunks() []domain.RAGChunk {
	return []domain.RAGChunk{
		{
			ID:         "c1",
			SourcePath: "summaries/01.json",
			SourceKind: "chapter_summary_facts",
			Facet:      "plot",
			Text:       "冥雾午夜压下，阴阳公寓3栋门牌泛红。蒋牧用普通现金缴夜租失败，被门牌吞掉半截影子。",
		},
		{
			ID:         "c2",
			SourcePath: "world_rules.md",
			SourceKind: "note",
			Facet:      "world",
			Text:       "冥府黑卡只在诡异世界有效交易中生效，消费越大返还越强，同时账单风险越高。",
		},
		{
			ID:         "c3",
			SourcePath: "characters.md",
			SourceKind: "note",
			Facet:      "character",
			Text:       "周行舟经营行舟小超市，胆小但讲信用，愿意用物资换取庇护。",
		},
		{
			ID:         "forbidden",
			SourcePath: "拆文库/对标作品.md",
			SourceKind: "deconstruction",
			Facet:      "craft",
			Text:       "冥府黑卡 夜租 影子（对标拆解材料，禁止进入写作召回）",
		},
	}
}

func TestTokenizeForBM25MixedCJKLatin(t *testing.T) {
	tokens := TokenizeForBM25("冥府黑卡BlackCard 2026")
	want := map[string]bool{"冥府": true, "府黑": true, "黑卡": true, "blackcard": true, "2026": true}
	for _, tok := range tokens {
		delete(want, tok)
	}
	if len(want) > 0 {
		t.Fatalf("missing tokens %v in %v", want, tokens)
	}
}

func TestBM25SearchRanksExactTopicFirst(t *testing.T) {
	idx := BuildBM25Index(bm25TestChunks())
	if idx.Len() != 3 {
		t.Fatalf("expected forbidden chunk excluded, got %d docs", idx.Len())
	}
	hits := idx.Search("冥府黑卡的账单风险", 3)
	if len(hits) == 0 {
		t.Fatalf("expected hits")
	}
	if hits[0].Chunk.ID != "c2" {
		t.Fatalf("expected c2 (黑卡规则) first, got %s", hits[0].Chunk.ID)
	}
	for _, hit := range hits {
		if hit.Chunk.ID == "forbidden" {
			t.Fatalf("forbidden chunk leaked into results")
		}
	}
}

func TestBM25SearchNoQueryTokens(t *testing.T) {
	idx := BuildBM25Index(bm25TestChunks())
	if hits := idx.Search("！！！", 3); hits != nil {
		t.Fatalf("expected nil hits for token-less query, got %v", hits)
	}
}

func TestCraftCategoryFromPath(t *testing.T) {
	cases := []struct{ path, category, sub string }{
		{"deconstruction-library/writing-techniques/appearance/eyes/描写.md", "appearance", "eyes"},
		{"deconstruction-library/writing-techniques/weapons/名剑篇.md", "weapons", ""},
		{"output/novel/premise.md", "", ""},
	}
	for _, c := range cases {
		category, sub := CraftCategory(c.path)
		if category != c.category || sub != c.sub {
			t.Fatalf("CraftCategory(%s) = (%s,%s), want (%s,%s)", c.path, category, sub, c.category, c.sub)
		}
	}
}

func TestCraftRecallDeterministicFilterAndNoMaterial(t *testing.T) {
	chunks := []domain.RAGChunk{
		{ID: "w1", SourcePath: "deconstruction-library/writing-techniques/weapons/名剑篇.md",
			SourceKind: CraftSourceKind, Facet: "craft", Text: "干将莫邪，铸剑淬火，剑脊如秋水。"},
		{ID: "a1", SourcePath: "deconstruction-library/writing-techniques/appearance/eyes/眼睛.md",
			SourceKind: CraftSourceKind, Facet: "craft", Text: "眼神冷峻如霜，眸底藏锋。"},
		{ID: "fact", SourcePath: "world_rules.md", SourceKind: "note", Facet: "world",
			Text: "冥府黑卡消费越大返还越强。"},
	}
	res := CraftRecall(chunks, CraftFieldWeapon, "铸剑", 3)
	if res.NoMaterial || len(res.Hits) == 0 || res.Hits[0].Chunk.ID != "w1" {
		t.Fatalf("expected weapon recall to hit w1, got %+v", res)
	}
	for _, hit := range res.Hits {
		if hit.Chunk.ID == "fact" || hit.Chunk.ID == "a1" {
			t.Fatalf("filter leaked non-weapon chunk: %s", hit.Chunk.ID)
		}
	}
	res = CraftRecall(chunks, CraftFieldMethodology, "", 3)
	if !res.NoMaterial {
		t.Fatalf("expected no_material for empty methodology subset, got %+v", res)
	}
}

func TestCraftChunksAllowedByPolicy(t *testing.T) {
	chunk := domain.RAGChunk{
		ID:         "w1",
		SourcePath: "deconstruction-library/writing-techniques/weapons/名剑篇.md",
		SourceKind: CraftSourceKind,
		Context:    "deconstruction-library/writing-techniques/weapons/名剑篇.md | 名剑",
		Text:       "干将莫邪。",
	}
	if IsForbiddenChunk(chunk) {
		t.Fatalf("writing-techniques chunk must be allowed")
	}
	banned := domain.RAGChunk{ID: "n1", SourcePath: "deconstruction-library/novel_sucai/对标小说.md", SourceKind: "note", Text: "情节拆解"}
	if !IsForbiddenChunk(banned) {
		t.Fatalf("novel_sucai must stay forbidden")
	}
}

func TestBenchmarkLibraryChannel(t *testing.T) {
	if got := BenchmarkCategory("deconstruction-library/novel_all/03-题材与套路/套路.md"); got != "题材与套路" {
		t.Fatalf("BenchmarkCategory = %q", got)
	}
	if got := BenchmarkCategory("deconstruction-library/novel_all/INDEX.md"); got != "总索引" {
		t.Fatalf("root file category = %q", got)
	}
	// novel_all 允许、novel_sucai 及备份仍禁入
	allowed := domain.RAGChunk{ID: "b1", SourcePath: "deconstruction-library/novel_all/06-爽点与剧情钩子/爆爽点80个.md",
		SourceKind: BenchmarkSourceKind, Facet: "benchmark", Text: "打脸反转、扮猪吃虎、绝境翻盘等爽点模式。"}
	if IsForbiddenChunk(allowed) {
		t.Fatalf("novel_all chunk must be allowed")
	}
	for _, banned := range []string{
		"deconstruction-library/novel_sucai/50个经典小说情节.md",
		"deconstruction-library/novel_sucai2/xx.md",
		"deconstruction-library/novel_su.bak.20260706_055316/xx.md",
	} {
		if !IsForbiddenChunk(domain.RAGChunk{ID: "n", SourcePath: banned, SourceKind: "note", Text: "x"}) {
			t.Fatalf("%s must stay forbidden", banned)
		}
	}
	// 设计字段路由：plot_beats 只命中 benchmark 库
	chunks := []domain.RAGChunk{
		allowed,
		{ID: "c1", SourcePath: "deconstruction-library/writing-techniques/weapons/名剑篇.md",
			SourceKind: CraftSourceKind, Facet: "craft", Text: "干将莫邪铸剑淬火。"},
		{ID: "fact", SourcePath: "world_rules.md", SourceKind: "note", Facet: "world", Text: "冥府黑卡返还规则。"},
	}
	res := CraftRecall(chunks, CraftFieldPlotBeats, "打脸 反转", 3)
	if res.NoMaterial || len(res.Hits) != 1 || res.Hits[0].Chunk.ID != "b1" {
		t.Fatalf("plot_beats should hit only b1, got %+v", res)
	}
	if !IsBenchmarkField(CraftFieldPlotBeats) || IsBenchmarkField(CraftFieldWeapon) {
		t.Fatalf("benchmark field flags wrong")
	}
	// 常规召回隔离判定
	if !IsDesignOnlySourceKind(BenchmarkSourceKind) || !IsDesignOnlySourceKind(CraftSourceKind) || IsDesignOnlySourceKind("note") {
		t.Fatalf("IsDesignOnlySourceKind wrong")
	}
}
