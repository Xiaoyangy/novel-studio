package tools

import (
	"reflect"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/rag"
)

func TestFinishRAGRecallCollapsesProjectOutlineAuthorityFamily(t *testing.T) {
	scored := map[string]*ragScored{
		"outline-absolute": scoredRAGTestChunk("outline-absolute", "data/runs/book/output/novel/outline.md", "第二章购车失败后转去夜市。", 10),
		"outline-relative": scoredRAGTestChunk("outline-relative", "outline.md", "第二章购车失败后转去夜市。", 9.8),
		"outline-layered":  scoredRAGTestChunk("outline-layered", "layered_outline.md", "第一卷第二章购车失败，夜市试点推进。", 9.6),
		"outline-accepted": scoredRAGTestChunk("outline-accepted", "meta/accepted-outline.md", "已验收大纲：第二章进入夜市。", 9.4),
		"summary":          scoredRAGTestChunk("summary", "summaries/02.json", "冷饮支架失败后必须重做。", 9.2),
		"progress":         scoredRAGTestChunk("progress", "meta/chapter_progress.md", "当前返工目标是第二章。", 8.9),
		"timeline":         scoredRAGTestChunk("timeline", "timeline.md", "傍晚五块价牌亮起。", 8.5),
	}

	items, trace := finishRAGRecall(scored, "第二章夜市返工", []string{"第二章", "夜市"}, "qdrant_bm25_hybrid_v2")
	if trace == nil || len(items) != len(trace.Matches) {
		t.Fatalf("items/trace mismatch: items=%+v trace=%+v", items, trace)
	}
	outlineHits := 0
	ids := map[string]bool{}
	for i, item := range items {
		ids[item.Key] = true
		if ragSourceFamily(trace.Matches[i].SourcePath) == "project_outline" {
			outlineHits++
		}
	}
	if outlineHits != 1 {
		t.Fatalf("outline authority family should occupy one slot, got %d: %+v", outlineHits, trace.Matches)
	}
	if !ids["outline-absolute"] || !ids["summary"] || !ids["progress"] || !ids["timeline"] {
		t.Fatalf("strongest outline plus diverse continuity sources must survive: %+v", items)
	}
	for _, duplicate := range []string{"outline-relative", "outline-layered", "outline-accepted"} {
		if ids[duplicate] {
			t.Fatalf("duplicate outline view %q consumed a result slot: %+v", duplicate, items)
		}
	}
}

func TestSelectDiverseRAGRecallSuppressesNearDuplicateSameSource(t *testing.T) {
	scored := map[string]*ragScored{
		"summary-a": scoredRAGTestChunk("summary-a", "summaries/02.json", "林澈购车失败后借车，傍晚五块价牌亮起。", 10),
		"summary-b": scoredRAGTestChunk("summary-b", "summaries/02.json", "林澈购车失败后借车，傍晚五块价牌亮起。", 9.8),
		"ledger":    scoredRAGTestChunk("ledger", "meta/resource_ledger.md", "油钱和人工必须据实结算。", 8.4),
	}

	selected := selectDiverseRAGRecall(scored, 3)
	if got := ragScoredIDs(selected); !reflect.DeepEqual(got, []string{"summary-a", "ledger"}) {
		t.Fatalf("near duplicate should be removed without losing another fact source: %v", got)
	}
}

func TestSelectDiverseRAGRecallCanBackfillDistinctChunksFromOnlySource(t *testing.T) {
	scored := map[string]*ragScored{
		"facts-a": scoredRAGTestChunk("facts-a", "meta/project_facts.md", "第一章确认系统不能给个人转账。", 10),
		"facts-b": scoredRAGTestChunk("facts-b", "meta/project_facts.md", "沈知遥只知道资金异常，不知道系统存在。", 8),
	}

	selected := selectDiverseRAGRecall(scored, 2)
	if got := ragScoredIDs(selected); !reflect.DeepEqual(got, []string{"facts-a", "facts-b"}) {
		t.Fatalf("source diversity must not empty distinct continuity facts: %v", got)
	}
}

func TestSelectDiverseRAGRecallKeepsRelevanceFloor(t *testing.T) {
	scored := map[string]*ragScored{
		"top":        scoredRAGTestChunk("top", "timeline.md", "第二章傍晚亮牌。", 10),
		"borderline": scoredRAGTestChunk("borderline", "relationship_state.md", "林澈开始信任沈知遥。", 3.01),
		"low":        scoredRAGTestChunk("low", "meta/unrelated.md", "遥远宗门炼丹大会。", 2.99),
	}

	selected := selectDiverseRAGRecall(scored, 6)
	if got := ragScoredIDs(selected); !reflect.DeepEqual(got, []string{"top", "borderline"}) {
		t.Fatalf("diversity must not force below-floor material: %v", got)
	}
}

func TestSelectDiverseRAGRecallStableAcrossMapOrderAndScoreTies(t *testing.T) {
	items := []*ragScored{
		scoredRAGTestChunk("id-a2", "a.md", "角色关系发生变化。", 5),
		scoredRAGTestChunk("id-b", "b.md", "资源账本产生新支出。", 5),
		scoredRAGTestChunk("id-a1", "data/runs/book/output/novel/a.md", "时间线推进到傍晚。", 5),
	}
	want := []string{"id-a1", "id-b", "id-a2"}
	for run := 0; run < 20; run++ {
		scored := make(map[string]*ragScored, len(items))
		for offset := range items {
			item := items[(offset+run)%len(items)]
			scored[item.chunk.ID] = item
		}
		if got := ragScoredIDs(selectDiverseRAGRecall(scored, 3)); !reflect.DeepEqual(got, want) {
			t.Fatalf("run %d unstable order: got %v want %v", run, got, want)
		}
	}
}

func TestCanonicalRAGSourceKeyPreservesLibraryHierarchy(t *testing.T) {
	if got := canonicalRAGSourceKey("/tmp/project/output/novel/outline.md"); got != "outline.md" {
		t.Fatalf("project source = %q", got)
	}
	left := canonicalRAGSourceKey("deconstruction-library/writing-techniques/dialogue/a.md")
	right := canonicalRAGSourceKey("deconstruction-library/review-calibration/dialogue/a.md")
	if left == right {
		t.Fatalf("craft and calibration hierarchy must stay distinct: %q", left)
	}
}

func scoredRAGTestChunk(id, sourcePath, text string, score float64) *ragScored {
	chunk := rag.NormalizeChunk(domain.RAGChunk{
		ID:         id,
		SourcePath: sourcePath,
		SourceKind: "chapter_summary_facts",
		Facet:      "plot",
		Text:       text,
		Summary:    text,
	})
	return &ragScored{chunk: chunk, score: score, reasons: []string{"test"}}
}

func ragScoredIDs(items []ragScored) []string {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.chunk.ID)
	}
	return ids
}
