package rag

import (
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

func TestDerivedSafeRewriteMethodSummaryIsStructuredAndDoesNotLeakRawText(t *testing.T) {
	chunk := domain.RAGChunk{
		SourcePath: "私有目录/星槎七号秘本.md",
		Text:       "星槎七号让沈砚在琉璃港打断顾岚，并用潜台词夺回主动权。",
		Metadata: map[string]any{
			"craft_facet": "星槎七号恶意自由文本",
		},
	}
	summary := DerivedSafeRewriteMethodSummary(chunk)
	for _, field := range []string{"机制=", "适用=", "动作=", "避免=", "验收="} {
		if !strings.Contains(summary, field) {
			t.Fatalf("structured card missing %s: %q", field, summary)
		}
	}
	for _, raw := range []string{"星槎七号", "沈砚", "琉璃港", "顾岚", "秘本"} {
		if strings.Contains(summary, raw) {
			t.Fatalf("derived card leaked raw span %q: %q", raw, summary)
		}
	}
	if !strings.Contains(summary, "内容面=uncategorized") || !strings.Contains(summary, "技法标签=打断、潜台词、权力位移") {
		t.Fatalf("derived card did not use controlled facet/tags: %q", summary)
	}
}

func TestDerivedSafeRewriteMethodSummaryProducesDistinctActionableCards(t *testing.T) {
	texts := []string{
		"对白通过打断和漏答留下潜台词。",
		"证据物件的状态变化必须带来场景后果。",
		"章末钩子应回收伏笔并留下未完成行动。",
		"用空间调度和光线变化限制人物行动。",
		"选择与取舍必须造成不可撤销的代价。",
	}
	seen := map[string]struct{}{}
	for index, text := range texts {
		summary := DerivedSafeRewriteMethodSummary(domain.RAGChunk{
			Text: text,
			Metadata: map[string]any{
				"craft_facet": string(FacetMethodology),
			},
		})
		if _, duplicate := seen[summary]; duplicate {
			t.Fatalf("method text %d collapsed to a duplicate default card: %q", index, summary)
		}
		seen[summary] = struct{}{}
	}
}
