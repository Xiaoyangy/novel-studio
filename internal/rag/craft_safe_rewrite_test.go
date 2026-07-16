package rag

import (
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

func TestSafeRewriteCraftRecallRequiresStageKindPathAndRelevance(t *testing.T) {
	options := CraftRecallOptions{Stage: StagePlan, RequireRelevant: true, SafeRewrite: true}
	valid := domain.RAGChunk{
		ID:         "dialogue-plan",
		Hash:       "dialogue-plan-hash",
		SourcePath: "deconstruction-library/review-calibration/novel-craft-methodology/11-章节技法.md",
		SourceKind: CalibrationSourceKind,
		Facet:      "craft",
		Summary:    "对白摩擦来自漏答、打断、潜台词与权力转移。",
		Text:       "对白摩擦来自漏答与打断，潜台词通过权力转移显形。",
		Metadata: map[string]any{
			"craft_category": "novel-craft-methodology",
			"craft_facet":    string(FacetDialogue),
			"usage_stage":    "plan,writing",
			"summary_origin": SummaryOriginCuratedMethod,
		},
	}
	stageMismatch := valid
	stageMismatch.ID = "architect-only"
	stageMismatch.Metadata = map[string]any{
		"craft_category": "novel-craft-methodology",
		"craft_facet":    string(FacetDialogue),
		"usage_stage":    "architect",
	}
	spoofedPath := valid
	spoofedPath.ID = "spoofed-project-fact"
	spoofedPath.SourcePath = "output/novel/chapters/01.md"
	unsafeBenchmark := domain.RAGChunk{
		ID:         "plot-material",
		Hash:       "plot-material-hash",
		SourcePath: "deconstruction-library/novel_all/06-爽点与剧情钩子/具体桥段.md",
		SourceKind: BenchmarkSourceKind,
		Facet:      "benchmark",
		Summary:    "具体情节反转桥段，不可用于自动返工。",
		Text:       "对白摩擦和潜台词之后照搬某部小说的具体反转桥段。",
		Metadata: map[string]any{
			"craft_category": "爽点与剧情钩子",
			"craft_facet":    string(FacetDialogue),
			"usage_stage":    "plan",
			"summary_origin": SummaryOriginCuratedMethod,
		},
	}
	unsafeAppearanceExcerpt := valid
	unsafeAppearanceExcerpt.ID = "appearance-story-excerpt"
	unsafeAppearanceExcerpt.Hash = "appearance-story-excerpt-hash"
	unsafeAppearanceExcerpt.SourcePath = "deconstruction-library/writing-techniques/appearance/eyes/金庸段落摘抄.md"
	unsafeAppearanceExcerpt.SourceKind = CraftSourceKind
	unsafeAncientMaterial := valid
	unsafeAncientMaterial.ID = "ancient-instruction-like-material"
	unsafeAncientMaterial.Hash = "ancient-instruction-like-material-hash"
	unsafeAncientMaterial.SourcePath = "deconstruction-library/writing-techniques/ancient-history/古代帝王狩猎资料.md"
	unsafeAncientMaterial.SourceKind = CraftSourceKind
	spoofedSafeCategory := unsafeBenchmark
	spoofedSafeCategory.ID = "plot-material-spoofed-safe"
	spoofedSafeCategory.Metadata = map[string]any{
		"craft_category": "教程方法论",
		"craft_facet":    string(FacetDialogue),
		"usage_stage":    "plan",
		"summary_origin": SummaryOriginCuratedMethod,
	}
	rawPrefixSummary := valid
	rawPrefixSummary.ID = "raw-prefix-summary"
	rawPrefixSummary.Hash = "raw-prefix-summary-hash"
	rawPrefixSummary.Summary = "这是一段从 benchmark 正文开头直接截取的原始句子。"
	rawPrefixSummary.Metadata = map[string]any{
		"craft_category": "novel-craft-methodology",
		"craft_facet":    string(FacetDialogue),
		"usage_stage":    "plan,writing",
		"summary_origin": SummaryOriginRawPrefix,
	}

	result := NewCraftCatalog([]domain.RAGChunk{stageMismatch, spoofedPath, unsafeBenchmark, spoofedSafeCategory, unsafeAppearanceExcerpt, unsafeAncientMaterial, rawPrefixSummary, valid}).RecallWithOptions(
		CraftFieldDialogue, "对白摩擦 潜台词", 5, options,
	)
	if result.NoMaterial || len(result.Hits) != 1 || result.Hits[0].Chunk.ID != valid.ID {
		t.Fatalf("safe rewrite recall leaked wrong-stage/spoofed/unsafe material: %+v", result)
	}
	if result.FilteredReason["unsafe_summary_origin"] == 0 {
		t.Fatalf("raw-prefix summary was not auditable: %+v", result.FilteredReason)
	}

	result = NewCraftCatalog([]domain.RAGChunk{valid}).RecallWithOptions(
		CraftFieldDialogue, "毫不相干的外星矿物", 5, options,
	)
	if !result.NoMaterial || len(result.Hits) != 0 {
		t.Fatalf("automatic rewrite recall must not use zero-score subset fallback: %+v", result)
	}

	// The explicit Architect-facing contract retains its historical fallback.
	explicitValid := valid
	explicitValid.ID = "explicit-craft-dialogue"
	explicitValid.SourceKind = CraftSourceKind
	explicitValid.SourcePath = "deconstruction-library/writing-techniques/novel-craft-methodology/11-章节技法.md"
	result = NewCraftCatalog([]domain.RAGChunk{explicitValid}).Recall(CraftFieldDialogue, "毫不相干的外星矿物", 5)
	if result.NoMaterial || len(result.Hits) != 1 {
		t.Fatalf("explicit recall fallback unexpectedly changed: %+v", result)
	}
}

func TestSafeRewriteCraftRecallRejectsNonRewriteField(t *testing.T) {
	chunk := domain.RAGChunk{
		ID:         "weapon",
		SourcePath: "deconstruction-library/writing-techniques/weapons/sword.md",
		SourceKind: CraftSourceKind,
		Text:       "长剑淬火",
	}
	result := NewCraftCatalog([]domain.RAGChunk{chunk}).RecallWithOptions(
		CraftFieldWeapon,
		"长剑淬火",
		3,
		CraftRecallOptions{Stage: StagePlan, RequireRelevant: true, SafeRewrite: true},
	)
	if !result.NoMaterial {
		t.Fatalf("automatic rewrite must stay inside the safe field allow-list: %+v", result)
	}
}

func TestSafeRewriteDialogueCanUseRelevantMethodologyCard(t *testing.T) {
	card := domain.RAGChunk{
		ID:         "methodology-dialogue-card",
		Hash:       "methodology-dialogue-card-hash",
		SourcePath: "deconstruction-library/review-calibration/novel-craft-methodology/11-章节技法.md",
		SourceKind: CalibrationSourceKind,
		Facet:      "craft",
		Summary:    "安全方法卡；类目=写作方法；内容面=methodology；技法标签=漏答、打断、潜台词、权力转移",
		Text:       "受控方法元数据：对白以漏答、打断和潜台词承载权力转移。",
		Metadata: map[string]any{
			"craft_facet":    string(FacetMethodology),
			"usage_stage":    "architect,plan,writing",
			"summary_origin": SummaryOriginDerivedMethodMetadata,
		},
	}
	options := CraftRecallOptions{Stage: StagePlan, RequireRelevant: true, SafeRewrite: true}
	result := NewCraftCatalog([]domain.RAGChunk{card}).RecallWithOptions(
		CraftFieldDialogue,
		"对白摩擦 潜台词 打断 漏答 权力转移 声口差异 信息释放",
		3,
		options,
	)
	if result.NoMaterial || len(result.Hits) != 1 || result.Hits[0].Chunk.ID != card.ID {
		t.Fatalf("relevant curated methodology card was invisible to dialogue repair: %+v", result)
	}

	// The bridge is specific to safe automatic rewrite.  Explicit recall keeps
	// its field/facet contract and must not gain a zero-score methodology fallback.
	explicit := NewCraftCatalog([]domain.RAGChunk{card}).Recall(
		CraftFieldDialogue,
		"对白摩擦 潜台词",
		3,
	)
	if !explicit.NoMaterial || len(explicit.Hits) != 0 {
		t.Fatalf("safe method-card bridge leaked into explicit craft recall: %+v", explicit)
	}
}

func TestCalibrationMethodCardsStayOutOfExplicitCraftRecall(t *testing.T) {
	for _, tc := range []struct {
		name  string
		field CraftDesignField
		facet CraftFacet
		query string
	}{
		{name: "dialogue", field: CraftFieldDialogue, facet: FacetDialogue, query: "对白打断漏答潜台词"},
		{name: "scene", field: CraftFieldSceneCraft, facet: FacetScene, query: "场景压力空间调度"},
		{name: "methodology", field: CraftFieldMethodology, facet: FacetMethodology, query: "章节节奏信息释放"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			card := domain.RAGChunk{
				ID:         "calibration-" + tc.name,
				Hash:       "hash-" + tc.name,
				SourcePath: "deconstruction-library/review-calibration/novel-craft-methodology/00-master-prompt.md",
				SourceKind: CalibrationSourceKind,
				Summary:    "安全方法卡：" + tc.query,
				Text:       "受控方法元数据：" + tc.query,
				Metadata: map[string]any{
					"craft_category": "novel-craft-methodology",
					"craft_facet":    string(tc.facet),
					"usage_stage":    "architect,plan,writing",
					"summary_origin": SummaryOriginDerivedMethodMetadata,
				},
			}
			explicit := NewCraftCatalog([]domain.RAGChunk{card}).Recall(tc.field, tc.query, 3)
			if !explicit.NoMaterial || len(explicit.Hits) != 0 {
				t.Fatalf("explicit recall leaked review-calibration material: %+v", explicit)
			}
			automatic := NewCraftCatalog([]domain.RAGChunk{card}).RecallWithOptions(
				tc.field, tc.query, 3,
				CraftRecallOptions{Stage: StagePlan, RequireRelevant: true, SafeRewrite: true},
			)
			if automatic.NoMaterial || len(automatic.Hits) != 1 {
				t.Fatalf("safe rewrite lost its curated method card: %+v", automatic)
			}
		})
	}
}

func TestSafeRewriteRelevanceCannotComeFromUndeliveredRawText(t *testing.T) {
	card := domain.RAGChunk{
		ID:         "raw-only-match",
		Hash:       "raw-only-match-hash",
		SourcePath: "deconstruction-library/review-calibration/novel-craft-methodology/11-章节技法.md",
		SourceKind: CalibrationSourceKind,
		Summary:    "安全方法卡；类目=写作方法；内容面=methodology；技法标签=主观因果、节奏变化",
		Text:       "原始工程内容里偶然出现对白摩擦、潜台词、打断、漏答、权力转移和信息释放。",
		Metadata: map[string]any{
			"craft_category": "novel-craft-methodology",
			"craft_facet":    string(FacetMethodology),
			"usage_stage":    "plan,writing",
			"summary_origin": SummaryOriginDerivedMethodMetadata,
		},
	}
	result := NewCraftCatalog([]domain.RAGChunk{card}).RecallWithOptions(
		CraftFieldDialogue,
		"对白摩擦 潜台词 打断 漏答 权力转移 信息释放",
		3,
		CraftRecallOptions{Stage: StagePlan, RequireRelevant: true, SafeRewrite: true},
	)
	if !result.NoMaterial || len(result.Hits) != 0 {
		t.Fatalf("raw text created a hit whose delivered summary was irrelevant: %+v", result)
	}
}

func TestCuratedRewriteMethodPathUsesContentFacet(t *testing.T) {
	path := "deconstruction-library/review-calibration/novel-craft-methodology/11-章节技法.md"
	if got := CraftContentFacet(path, "让对白通过打断、漏答和潜台词改变主动权"); got != FacetDialogue {
		t.Fatalf("curated method chunk facet=%s, want dialogue", got)
	}
}

func TestSearchSafeRewriteMethodCardsDeduplicatesEquivalentSummaries(t *testing.T) {
	dialogueCard := DerivedSafeRewriteMethodSummary(domain.RAGChunk{
		Text:     "对白通过打断和潜台词改变主动权。",
		Metadata: map[string]any{"craft_facet": string(FacetDialogue)},
	})
	evidenceCard := DerivedSafeRewriteMethodSummary(domain.RAGChunk{
		Text:     "证据物件必须改变场景后果。",
		Metadata: map[string]any{"craft_facet": string(FacetMethodology)},
	})
	chunks := []domain.RAGChunk{
		{ID: "dialogue-a", Summary: dialogueCard},
		{ID: "dialogue-b", Summary: dialogueCard},
		{ID: "dialogue-c", Summary: "  " + dialogueCard + "  "},
		{ID: "evidence", Summary: evidenceCard},
	}
	hits := searchSafeRewriteMethodCards(chunks, "打断 潜台词 主动权 证据 物件 场景后果", 5, CraftFieldMethodology)
	if len(hits) != 2 {
		t.Fatalf("equivalent summaries occupied multiple BM25 slots: %+v", hits)
	}
	seen := map[string]bool{}
	for _, hit := range hits {
		key := strings.ToLower(strings.Join(strings.Fields(hit.Chunk.Summary), " "))
		if seen[key] {
			t.Fatalf("duplicate method summary survived ranking: %+v", hits)
		}
		seen[key] = true
	}
}

func TestSearchSafeRewriteMethodCardsDeduplicatesPrimaryOperationVariants(t *testing.T) {
	mechanical := "安全方法卡；内容面=methodology；技法标签=机械同构；机制=用因果和句段职责变化打破模板重复；动作=删除总结句并改换观察动作后果顺序；避免=机械替换；验收=连续三段功能不同"
	mechanicalWithSecondaryTags := "安全方法卡；内容面=methodology；技法标签=机械同构、场景目标、阻力对抗；机制=用因果和句段职责变化打破模板重复；动作=删除总结句并改换观察动作后果顺序；避免=机械替换；验收=连续三段功能不同"
	subjectiveCause := "安全方法卡；内容面=methodology；技法标签=主观因果；机制=让认知偏差驱动选择和后果；动作=补足观察判断选择结果四步链；避免=旁白总结；验收=行动原因可见"
	hits := searchSafeRewriteMethodCards([]domain.RAGChunk{
		{ID: "mechanical", Summary: mechanical},
		{ID: "mechanical-variant", Summary: mechanicalWithSecondaryTags},
		{ID: "subjective-cause", Summary: subjectiveCause},
	}, "机械同构 场景目标 主观因果 观察 判断 选择 后果", 5, CraftFieldMethodology)
	if len(hits) != 2 {
		t.Fatalf("secondary tags allowed one primary operation to monopolize Top-N: %+v", hits)
	}
	seen := map[string]bool{}
	for _, hit := range hits {
		key := safeRewriteMethodDedupKey(hit.Chunk.Summary)
		if seen[key] {
			t.Fatalf("duplicate primary operation survived ranking: %+v", hits)
		}
		seen[key] = true
	}
}

func TestSearchSafeRewriteMethodCardsRequiresPrimaryTechniqueToMatchField(t *testing.T) {
	voice := "安全方法卡；内容面=methodology；技法标签=声口差异；机制=把身份压进选词；动作=限定句长和回避方式"
	interrupt := "安全方法卡；内容面=methodology；技法标签=打断、章末钩子；机制=用中断改变主动权；动作=在关键信息前截断"
	hookWithDialogueSecondaryTags := "安全方法卡；内容面=methodology；技法标签=章末钩子、打断、信息释放；机制=以未完成行动牵引下一章；动作=在新选择出现时截断"
	hits := searchSafeRewriteMethodCards([]domain.RAGChunk{
		{ID: "voice", Summary: voice},
		{ID: "interrupt", Summary: interrupt},
		{ID: "hook", Summary: hookWithDialogueSecondaryTags},
	}, "对白 声口 打断 信息释放 章末钩子", 5, CraftFieldDialogue)
	if len(hits) != 2 {
		t.Fatalf("non-dialogue primary technique occupied dialogue Top-N: %+v", hits)
	}
	for _, hit := range hits {
		if hit.Chunk.ID == "hook" {
			t.Fatalf("secondary dialogue tags smuggled a hook operation into dialogue recall: %+v", hits)
		}
	}
}
