package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/rag"
	"github.com/chenhongyang/novel-studio/internal/rules"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func TestRewriteCraftSafeCorpusPolicyUsesFieldAlignedPrimaryDiversityV5(t *testing.T) {
	if rewriteCraftSafeCorpusPolicy != "rewrite-craft-safe-corpus-v5-field-aligned-primary-diversity" {
		t.Fatalf("unexpected safe rewrite policy identity: %q", rewriteCraftSafeCorpusPolicy)
	}
}

func TestNormalizePartialCraftReferenceAliases(t *testing.T) {
	ref := "craft_recall_receipt:receipt-v5#chunk=method-1#hash=abc"
	merged := map[string]any{
		"external_reference_plan": []any{map[string]any{
			"need_id": "rewrite-dialogue", "source_type": "craft_recall_receipt", "source_ref": ref,
		}},
	}
	normalizePartialCraftReferenceAliases(merged)
	items := merged["external_reference_plan"].([]any)
	entry := items[0].(map[string]any)
	if entry["query_or_need"] != "rewrite-dialogue" || entry["source_type"] != craftSourceType {
		t.Fatalf("craft aliases were not normalized: %+v", entry)
	}
	refs := stringSliceFromAny(entry["source_refs"])
	if len(refs) != 1 || refs[0] != ref {
		t.Fatalf("source_ref alias was not normalized: %+v", entry)
	}
	if _, exists := entry["need_id"]; exists {
		t.Fatalf("need_id alias survived normalization: %+v", entry)
	}
	if _, exists := entry["source_ref"]; exists {
		t.Fatalf("source_ref alias survived normalization: %+v", entry)
	}
}

func TestNormalizePartialCraftHitShorthandAndMergeByNeed(t *testing.T) {
	receipt := &domain.CraftRecallReceipt{
		ID: "receipt-v5", CreatedAt: "2026-07-16T07:00:00+08:00",
		Attempts: []domain.CraftRecallReceiptAttempt{
			{Need: domain.CraftRecallNeed{ID: "rewrite-methodology"}, Hits: []domain.CraftRecallReceiptHit{
				{Ref: "craft_recall_receipt:receipt-v5#chunk=m1#hash=a", SourceKind: "knowledge"},
				{Ref: "craft_recall_receipt:receipt-v5#chunk=m2#hash=b", SourceKind: "knowledge"},
			}},
		},
	}
	merged := map[string]any{"external_reference_plan": []any{
		map[string]any{"need_id": "rewrite-methodology", "hit_ref": receipt.Attempts[0].Hits[0].Ref, "usable_details": "饭桌用沉默换挡", "transformation_rule": "不逐项复述", "do_not_use": "不复制原句"},
		map[string]any{"need_id": "rewrite-methodology", "hit_ref": receipt.Attempts[0].Hits[1].Ref, "usable_details": "夜市用动作独段", "transformation_rule": "压缩流程", "do_not_use": "不随机碎句"},
	}}
	normalizePartialCraftReferenceAliases(merged, receipt)
	items := merged["external_reference_plan"].([]any)
	if len(items) != 1 {
		t.Fatalf("one-hit rows were not merged by need: %+v", items)
	}
	entry := items[0].(map[string]any)
	if entry["query_or_need"] != "rewrite-methodology" || entry["source_type"] != craftSourceType || entry["retrieved_at"] != receipt.CreatedAt {
		t.Fatalf("receipt aliases were not completed from authority: %+v", entry)
	}
	if refs := stringSliceFromAny(entry["source_refs"]); len(refs) != 2 {
		t.Fatalf("exact hit refs were lost: %+v", entry)
	}
	if details := stringSliceFromAny(entry["usable_details"]); len(details) != 2 {
		t.Fatalf("scalar usable details were not retained: %+v", entry)
	}
	if banned := stringSliceFromAny(entry["do_not_use"]); len(banned) != 2 {
		t.Fatalf("scalar do_not_use values were not retained: %+v", entry)
	}
	if rules, _ := entry["transformation_rule"].(string); !strings.Contains(rules, "不逐项复述") || !strings.Contains(rules, "压缩流程") {
		t.Fatalf("transformation rules were not merged: %+v", entry)
	}
}

func TestNormalizePartialCraftRawReceiptHitsByNeed(t *testing.T) {
	sharedRef := "craft_recall_receipt:receipt-v5#chunk=shared#hash=s"
	receipt := &domain.CraftRecallReceipt{
		ID: "receipt-v5", CreatedAt: "2026-07-16T07:00:00+08:00",
		Attempts: []domain.CraftRecallReceiptAttempt{
			{Need: domain.CraftRecallNeed{ID: "rewrite-methodology"}, Hits: []domain.CraftRecallReceiptHit{
				{Ref: "craft_recall_receipt:receipt-v5#chunk=m1#hash=a", SourceKind: rag.CalibrationSourceKind},
				{Ref: sharedRef, SourceKind: rag.CalibrationSourceKind},
			}},
			{Need: domain.CraftRecallNeed{ID: "rewrite-dialogue"}, Hits: []domain.CraftRecallReceiptHit{
				{Ref: "craft_recall_receipt:receipt-v5#chunk=d1#hash=b", SourceKind: rag.CalibrationSourceKind},
				{Ref: sharedRef, SourceKind: rag.CalibrationSourceKind},
			}},
		},
	}
	row := func(need, ref string, sourceKey string) map[string]any {
		entry := map[string]any{
			"query_or_need": need,
			"ref":           ref,
			"usable_details": []any{
				need + " 的本章场景化手法",
			},
			"transformation_rule": "只迁移写法，不复制原句",
			"do_not_use":          []any{"不照抄摘要"},
		}
		entry[sourceKey] = rag.CalibrationSourceKind
		return entry
	}
	merged := map[string]any{"external_reference_plan": []any{
		row("rewrite-methodology", receipt.Attempts[0].Hits[0].Ref, "source_kind"),
		row("rewrite-methodology", sharedRef, "source_type"),
		row("rewrite-dialogue", receipt.Attempts[1].Hits[0].Ref, "source_kind"),
		row("rewrite-dialogue", sharedRef, "source_kind"),
	}}

	normalizePartialCraftReferenceAliases(merged, receipt)
	items := merged["external_reference_plan"].([]any)
	if len(items) != 2 {
		t.Fatalf("raw receipt rows were not collapsed to one row per need: %+v", items)
	}
	seen := map[string]bool{}
	for _, item := range items {
		entry := item.(map[string]any)
		need, _ := entry["query_or_need"].(string)
		seen[need] = true
		if entry["source_type"] != craftSourceType {
			t.Fatalf("raw calibration kind was not canonicalized: %+v", entry)
		}
		if refs := stringSliceFromAny(entry["source_refs"]); len(refs) != 2 {
			t.Fatalf("exact refs were not merged for %s: %+v", need, entry)
		}
		if _, exists := entry["ref"]; exists {
			t.Fatalf("raw ref alias survived normalization: %+v", entry)
		}
		if _, exists := entry["source_kind"]; exists {
			t.Fatalf("raw source_kind alias survived normalization: %+v", entry)
		}
	}
	if !seen["rewrite-methodology"] || !seen["rewrite-dialogue"] {
		t.Fatalf("need coverage was lost: %+v", items)
	}
}

func TestNormalizePartialCraftSourceTypeFailsClosed(t *testing.T) {
	craftRef := "craft_recall_receipt:receipt-v5#chunk=craft#hash=a"
	benchmarkRef := "craft_recall_receipt:receipt-v5#chunk=benchmark#hash=b"
	receipt := &domain.CraftRecallReceipt{
		ID: "receipt-v5",
		Attempts: []domain.CraftRecallReceiptAttempt{
			{Need: domain.CraftRecallNeed{ID: "rewrite-methodology"}, Hits: []domain.CraftRecallReceiptHit{
				{Ref: craftRef, SourceKind: rag.CalibrationSourceKind},
				{Ref: benchmarkRef, SourceKind: rag.BenchmarkSourceKind},
			}},
		},
	}
	unauthorizedRef := "craft_recall_receipt:other#chunk=spoof#hash=x"
	merged := map[string]any{"external_reference_plan": []any{
		map[string]any{"query_or_need": "rewrite-methodology", "source_type": "web_search", "source_refs": []any{craftRef}},
		map[string]any{"query_or_need": "rewrite-methodology", "source_kind": rag.CalibrationSourceKind, "ref": unauthorizedRef},
		map[string]any{"query_or_need": "rewrite-methodology", "source_type": rag.CalibrationSourceKind, "source_refs": []any{craftRef, unauthorizedRef}},
		map[string]any{"query_or_need": "rewrite-methodology", "source_type": rag.CalibrationSourceKind, "source_refs": []any{craftRef, benchmarkRef}},
		map[string]any{"query_or_need": "current-price", "source_type": "web_search", "source_refs": []any{"https://example.invalid/source"}},
		map[string]any{"query_or_need": "rewrite-methodology", "source_kind": rag.BenchmarkSourceKind, "ref": benchmarkRef},
	}}

	normalizePartialCraftReferenceAliases(merged, receipt)
	items := merged["external_reference_plan"].([]any)
	if got := items[0].(map[string]any)["source_type"]; got != "web_search" {
		t.Fatalf("explicit web source type was overwritten: %+v", items[0])
	}
	if _, exists := items[1].(map[string]any)["source_refs"]; exists {
		t.Fatalf("unauthorized raw ref was promoted: %+v", items[1])
	}
	if got := items[2].(map[string]any)["source_type"]; got != rag.CalibrationSourceKind {
		t.Fatalf("authorized+unauthorized refs were canonicalized: %+v", items[2])
	}
	if got := items[3].(map[string]any)["source_type"]; got != rag.CalibrationSourceKind {
		t.Fatalf("craft+benchmark refs were canonicalized: %+v", items[3])
	}
	if got := items[4].(map[string]any)["source_type"]; got != "web_search" {
		t.Fatalf("ordinary web row was changed: %+v", items[4])
	}
	if got := items[5].(map[string]any)["source_type"]; got != benchmarkCraftSourceType {
		t.Fatalf("authorized benchmark shorthand was not canonicalized: %+v", items[5])
	}
}

func newRewriteCraftTestStore(t *testing.T, withIndex bool) *store.Store {
	t.Helper()
	st := newPhaseTestStore(t)
	source := prepareRewriteSourceTest(t, st,
		"第一章\n\n林澈把票据推回桌面，没有替对方补完沉默。",
		"# 第一章返工\n\n## 必须修正\n\n- 降低整篇单段 AI 味并打破对白传送带。\n",
	)
	gate := mechanicalGateReviewPayload{
		Chapter: 1, BodySHA256: source.BodySHA256,
		RuleViolations: []rules.Violation{
			{Rule: "aigc_ratio"},
			{Rule: "dialogue_conveyor_overuse"},
		},
	}
	raw, err := json.Marshal(gate)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(st.Dir(), "reviews", "01_ai_gate.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if withIndex {
		saveRewriteCraftIndex(t, st, "rewrite-craft-index-v1")
	}
	return st
}

func rewriteCraftTestChunks() []domain.RAGChunk {
	return []domain.RAGChunk{
		{
			ID:         "method-plan",
			Hash:       "method-plan-hash",
			SourcePath: "deconstruction-library/writing-techniques/novel-craft-methodology/人物节奏.md",
			SourceKind: rag.CraftSourceKind,
			Facet:      "craft",
			Summary:    "让人物主观因果改变叙事节奏，并用信息延迟改变句段功能。",
			Text:       "人物主观因果必须先于解释，叙事节奏随选择的后果变化；句段功能与信息延迟共同避免流程密度。",
			Metadata: map[string]any{
				"craft_category": "novel-craft-methodology",
				"craft_facet":    string(rag.FacetMethodology),
				"usage_stage":    "plan,writing",
				"summary_origin": rag.SummaryOriginCuratedMethod,
			},
		},
		{
			ID:         "dialogue-plan",
			Hash:       "dialogue-plan-hash",
			SourcePath: "deconstruction-library/writing-techniques/novel-craft-methodology/对白摩擦.md",
			SourceKind: rag.CraftSourceKind,
			Facet:      "craft",
			Summary:    "对白摩擦来自漏答、打断、潜台词与权力转移。",
			Text:       "对白摩擦不要靠信息倾倒；让漏答和打断暴露潜台词，使权力转移发生在回应方式里。",
			Metadata: map[string]any{
				"craft_category": "novel-craft-methodology",
				"craft_facet":    string(rag.FacetDialogue),
				"usage_stage":    "plan,writing",
				"summary_origin": rag.SummaryOriginCuratedMethod,
			},
		},
		{
			ID:         "architect-only",
			Hash:       "architect-only-hash",
			SourcePath: "deconstruction-library/writing-techniques/novel-craft-methodology/仅架构.md",
			SourceKind: rag.CraftSourceKind,
			Text:       "人物主观因果与对白摩擦。",
			Metadata: map[string]any{
				"craft_category": "novel-craft-methodology",
				"craft_facet":    string(rag.FacetMethodology),
				"usage_stage":    "architect",
			},
		},
		{
			ID:         "spoofed-fact",
			Hash:       "spoofed-fact-hash",
			SourcePath: "output/novel/chapters/01.md",
			SourceKind: rag.CraftSourceKind,
			Text:       "人物主观因果 对白摩擦",
			Metadata: map[string]any{
				"craft_category": "novel-craft-methodology",
				"craft_facet":    string(rag.FacetMethodology),
				"usage_stage":    "plan",
			},
		},
		{
			ID:         "unsafe-plot",
			Hash:       "unsafe-plot-hash",
			SourcePath: "deconstruction-library/novel_all/06-爽点与剧情钩子/具体桥段.md",
			SourceKind: rag.BenchmarkSourceKind,
			Text:       "对白摩擦之后照搬某本小说的反转桥段。",
			Metadata: map[string]any{
				"craft_category": "爽点与剧情钩子",
				"craft_facet":    string(rag.FacetDialogue),
				"usage_stage":    "plan",
			},
		},
	}
}

func saveRewriteCraftIndex(t *testing.T, st *store.Store, digest string) {
	t.Helper()
	chunks := rewriteCraftTestChunks()
	for index := range chunks {
		if chunks[index].Metadata == nil {
			chunks[index].Metadata = map[string]any{}
		}
		chunks[index].Metadata["fixture_revision"] = digest
	}
	if err := st.RAG.SaveIndexState(domain.RAGIndexState{
		SchemaVersion:   1,
		SanitizedDigest: digest,
		UpdatedAt:       "2026-07-15T12:00:00+08:00",
		Chunks:          chunks,
	}); err != nil {
		t.Fatal(err)
	}
}

func receiptBackedCraftPlan(receipt *domain.CraftRecallReceipt) domain.ChapterPlan {
	plan := domain.ChapterPlan{Chapter: receipt.Chapter}
	plan.CausalSimulation.ContextSources = []string{craftReceiptSourceToken(receipt.ID)}
	for _, attempt := range receipt.Attempts {
		if len(attempt.Hits) == 0 {
			continue
		}
		hit := attempt.Hits[0]
		sourceType := craftSourceType
		if strings.EqualFold(hit.SourceKind, rag.BenchmarkSourceKind) {
			sourceType = benchmarkCraftSourceType
		}
		plan.CausalSimulation.ExternalRefs = append(plan.CausalSimulation.ExternalRefs, domain.ExternalReferencePlan{
			QueryOrNeed:        attempt.Need.ID + "：本章返工手法",
			SourceType:         sourceType,
			SourceRefs:         []string{hit.Ref},
			UsableDetails:      []string{"让林澈先拒绝替对方补话，再由票据位置变化逼出回应。"},
			TransformationRule: "只把回应次序转成本章人物选择，不迁移素材中的情节和措辞。",
			DoNotUse:           []string{"不复制示例句", "不增加外部作品人物或设定"},
		})
	}
	return plan
}

func craftTestString(value any) string {
	text, _ := value.(string)
	return text
}

func TestDeriveRewriteCraftNeedsCapsAndRoutesReviewSignals(t *testing.T) {
	st := newRewriteCraftTestStore(t, true)
	needs := deriveRewriteCraftNeeds(st, 1)
	if len(needs) != 2 {
		t.Fatalf("rewrite craft needs must be capped at two: %+v", needs)
	}
	if needs[0].ID != "rewrite-methodology" || needs[1].ID != "rewrite-dialogue" {
		t.Fatalf("unexpected deterministic need routing/order: %+v", needs)
	}
	if len(needs[0].TriggerRefs) == 0 || len(needs[1].TriggerRefs) == 0 {
		t.Fatalf("receipt needs lost review trigger refs: %+v", needs)
	}
}

func TestDeriveRewriteCraftNeedsIgnoresStaleOrUnboundEvidence(t *testing.T) {
	st := newRewriteCraftTestStore(t, true)
	gatePath := filepath.Join(st.Dir(), "reviews", "01_ai_gate.json")
	stale := mechanicalGateReviewPayload{
		Chapter: 1, BodySHA256: strings.Repeat("0", 64),
		RuleViolations: []rules.Violation{{Rule: "aigc_ratio"}, {Rule: "dialogue_conveyor_overuse"}},
	}
	raw, err := json.Marshal(stale)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(gatePath, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if needs := deriveRewriteCraftNeeds(st, 1); len(needs) != 0 {
		t.Fatalf("stale mechanical gate drove current receipt needs: %+v", needs)
	}
	stale.BodySHA256 = ""
	raw, _ = json.Marshal(stale)
	if err := os.WriteFile(gatePath, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if needs := deriveRewriteCraftNeeds(st, 1); len(needs) != 0 {
		t.Fatalf("legacy unbound gate must not become an enforcement trigger: %+v", needs)
	}
}

func TestDeriveRewriteCraftNeedsFallsBackPastStalePrimaryGate(t *testing.T) {
	st := newRewriteCraftTestStore(t, true)
	source, _, _, err := loadChapterRewriteSource(st, 1)
	if err != nil || source == nil {
		t.Fatalf("load rewrite source: source=%+v err=%v", source, err)
	}
	stale := mechanicalGateReviewPayload{
		Chapter: 1, BodySHA256: strings.Repeat("0", 64),
		RuleViolations: []rules.Violation{{Rule: "aigc_ratio"}},
	}
	raw, _ := json.Marshal(stale)
	if err := os.WriteFile(filepath.Join(st.Dir(), "reviews", "01_ai_gate.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	current := mechanicalGateReviewPayload{
		Chapter: 1, BodySHA256: source.BodySHA256,
		RuleViolations: []rules.Violation{{Rule: "dialogue_conveyor_overuse"}},
	}
	raw, _ = json.Marshal(current)
	fallbackDir := filepath.Join(st.Dir(), "reviews_ai")
	if err := os.MkdirAll(fallbackDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fallbackDir, "01.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	needs := deriveRewriteCraftNeeds(st, 1)
	if len(needs) != 1 || needs[0].ID != "rewrite-dialogue" {
		t.Fatalf("current fallback gate was hidden by stale primary: %+v", needs)
	}
}

func TestEnsureRewriteCraftReceiptPersistsBindingHitsFiltersAndAudit(t *testing.T) {
	st := newRewriteCraftTestStore(t, true)
	receipt, err := ensureRewriteCraftReceipt(st, 1, "")
	if err != nil {
		t.Fatal(err)
	}
	state, loadErr := st.RAG.LoadIndexStateReadOnly()
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if receipt == nil || receipt.ID == "" || receipt.IndexIdentity != craftIndexIdentity(state) || receipt.PayloadSHA256 == "" {
		t.Fatalf("receipt binding is incomplete: %+v", receipt)
	}
	if len(receipt.Attempts) != 2 {
		t.Fatalf("expected two bounded attempts: %+v", receipt.Attempts)
	}
	for _, attempt := range receipt.Attempts {
		if attempt.NoMaterial || len(attempt.Hits) == 0 {
			t.Fatalf("expected relevant method hit for %s: %+v", attempt.Need.ID, attempt)
		}
		if attempt.FilteredCount == 0 || len(attempt.FilteredReason) == 0 {
			t.Fatalf("filtered reasons must be persisted for %s: %+v", attempt.Need.ID, attempt)
		}
		for _, hit := range attempt.Hits {
			if !strings.HasPrefix(hit.Ref, craftReceiptSourceToken(receipt.ID)+"#chunk=") || hit.ChunkHash == "" {
				t.Fatalf("hit is not bound to receipt/chunk hash: %+v", hit)
			}
		}
	}
	stored, err := st.RAG.LoadCraftRecallReceipt(receipt.ID)
	if err != nil || stored == nil || stored.PayloadSHA256 != receipt.PayloadSHA256 {
		t.Fatalf("stored receipt mismatch: stored=%+v err=%v", stored, err)
	}
	logPath := filepath.Join(st.Dir(), "meta", "rag", "craft_recall_log.jsonl")
	before, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(before), `"filtered_reason"`) || !strings.Contains(string(before), `"payload_sha256"`) {
		t.Fatalf("audit log lost filter or payload evidence: %s", before)
	}
	reused, err := ensureRewriteCraftReceipt(st, 1, receipt.ID)
	if err != nil || reused == nil || reused.ID != receipt.ID {
		t.Fatalf("current receipt was not idempotently reused: receipt=%+v err=%v", reused, err)
	}
	after, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("idempotent receipt reuse appended a duplicate audit event")
	}
	if err := os.Remove(logPath); err != nil {
		t.Fatal(err)
	}
	if _, err := ensureRewriteCraftReceipt(st, 1, receipt.ID); err != nil {
		t.Fatalf("receipt retry did not recover missing audit evidence: %v", err)
	}
	recoveredLog, err := os.ReadFile(logPath)
	if err != nil || !strings.Contains(string(recoveredLog), receipt.ID) {
		t.Fatalf("missing audit row was not recovered: log=%s err=%v", recoveredLog, err)
	}
}

func TestEnsureRewriteCraftReceiptNeverFallsBackToRawBenchmarkText(t *testing.T) {
	st := newRewriteCraftTestStore(t, true)
	state, err := st.RAG.LoadIndexStateReadOnly()
	if err != nil || state == nil {
		t.Fatalf("load craft index: state=%+v err=%v", state, err)
	}
	rawBenchmark := "这段原始 benchmark prose 绝不能进入 receipt 或 plan context。"
	state.SanitizedDigest = "rewrite-craft-index-missing-summary"
	state.Chunks = append(state.Chunks, domain.RAGChunk{
		ID:         "missing-summary-plan",
		Hash:       "missing-summary-plan-hash",
		SourcePath: "deconstruction-library/writing-techniques/novel-craft-methodology/缺摘要.md",
		SourceKind: rag.CraftSourceKind,
		Facet:      "craft",
		Text:       "人物主观因果 叙事节奏 句段功能 信息延迟 " + rawBenchmark,
		Metadata: map[string]any{
			"craft_category": "novel-craft-methodology",
			"craft_facet":    string(rag.FacetMethodology),
			"usage_stage":    "plan,writing",
		},
	})
	if err := st.RAG.SaveIndexState(*state); err != nil {
		t.Fatal(err)
	}
	receipt, err := ensureRewriteCraftReceipt(st, 1, "")
	if err != nil {
		t.Fatal(err)
	}
	if receipt == nil || len(receipt.Attempts) == 0 {
		t.Fatalf("missing receipt attempts: %+v", receipt)
	}
	foundReason := false
	for _, attempt := range receipt.Attempts {
		if attempt.FilteredReason["missing_summary"] > 0 {
			foundReason = true
		}
		for _, hit := range attempt.Hits {
			if hit.ChunkID == "missing-summary-plan" {
				t.Fatalf("summary-less chunk leaked into receipt: %+v", hit)
			}
		}
	}
	if !foundReason {
		t.Fatalf("missing summary was not audited: %+v", receipt.Attempts)
	}
	raw, err := json.Marshal(craftReceiptContext(receipt))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), rawBenchmark) {
		t.Fatalf("raw benchmark prose leaked into craft context: %s", raw)
	}
}

func TestRewriteCraftReceiptInvalidatesOnCurrentIndexChange(t *testing.T) {
	st := newRewriteCraftTestStore(t, true)
	oldReceipt, err := ensureRewriteCraftReceipt(st, 1, "")
	if err != nil {
		t.Fatal(err)
	}
	oldPlan := receiptBackedCraftPlan(oldReceipt)
	if err := validateRewriteCraftConsumption(st, oldPlan); err != nil {
		t.Fatalf("current receipt should validate: %v", err)
	}

	saveRewriteCraftIndex(t, st, "rewrite-craft-index-v2")
	if err := validateRewriteCraftConsumption(st, oldPlan); err == nil || !strings.Contains(err.Error(), "index") {
		t.Fatalf("old receipt must fail after current index changes: %v", err)
	}
	newReceipt, err := ensureRewriteCraftReceipt(st, 1, oldReceipt.ID)
	if err != nil {
		t.Fatal(err)
	}
	state, loadErr := st.RAG.LoadIndexStateReadOnly()
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if newReceipt == nil || newReceipt.ID == oldReceipt.ID || newReceipt.IndexIdentity != craftIndexIdentity(state) || newReceipt.IndexIdentity == oldReceipt.IndexIdentity {
		t.Fatalf("index change did not issue a new receipt: old=%+v new=%+v", oldReceipt, newReceipt)
	}
	if err := validateRewriteCraftConsumption(st, receiptBackedCraftPlan(newReceipt)); err != nil {
		t.Fatalf("new current-index receipt should validate: %v", err)
	}
}

func TestRewriteCraftReceiptInvalidatesAfterIncrementalSafeCorpusUpsertWithStaleSanitizedDigest(t *testing.T) {
	st := newRewriteCraftTestStore(t, true)
	receipt, err := ensureRewriteCraftReceipt(st, 1, "")
	if err != nil {
		t.Fatal(err)
	}
	plan := receiptBackedCraftPlan(receipt)
	before, err := st.RAG.LoadIndexStateReadOnly()
	if err != nil || before == nil {
		t.Fatalf("load initial state: state=%+v err=%v", before, err)
	}
	staleMarker := before.SanitizedDigest
	changed := before.Chunks[0]
	changed.Hash = ""
	changed.Text += " 新增的安全方法语义会改变自动召回排序。"
	if err := UpsertRAGChunks(context.Background(), st, nil, nil, []domain.RAGChunk{changed}, before.Config); err != nil {
		t.Fatal(err)
	}
	after, err := st.RAG.LoadIndexStateReadOnly()
	if err != nil || after == nil {
		t.Fatalf("load incrementally updated state: state=%+v err=%v", after, err)
	}
	if after.SanitizedDigest != staleMarker {
		t.Fatalf("fixture must exercise a stale sanitization marker: before=%q after=%q", staleMarker, after.SanitizedDigest)
	}
	if craftIndexIdentity(after) == receipt.IndexIdentity {
		t.Fatal("craft identity trusted stale SanitizedDigest instead of current safe corpus")
	}
	if err := validateRewriteCraftConsumption(st, plan); err == nil || !strings.Contains(err.Error(), "index") {
		t.Fatalf("incremental safe-corpus change must invalidate old receipt: %v", err)
	}
}

func TestRewriteCraftReceiptPayloadHashBindsSelectedHits(t *testing.T) {
	st := newRewriteCraftTestStore(t, true)
	receipt, err := ensureRewriteCraftReceipt(st, 1, "")
	if err != nil {
		t.Fatal(err)
	}
	plan := receiptBackedCraftPlan(receipt)
	receipt.Attempts[0].Hits[0].ChunkHash = "tampered-hit-hash"
	if err := st.RAG.SaveCraftRecallReceipt(*receipt); err != nil {
		t.Fatal(err)
	}
	if err := validateRewriteCraftConsumption(st, plan); err == nil || !strings.Contains(err.Error(), "payload") {
		t.Fatalf("tampered selected hit must invalidate receipt payload: %v", err)
	}
}

func TestRewriteCraftMissingIndexPersistsAuditableNoMaterial(t *testing.T) {
	st := newRewriteCraftTestStore(t, false)
	receipt, err := ensureRewriteCraftReceipt(st, 1, "")
	if err != nil {
		t.Fatal(err)
	}
	if receipt == nil || receipt.IndexIdentity != "missing" || len(receipt.Attempts) != 2 {
		t.Fatalf("missing index should still produce a bound receipt: %+v", receipt)
	}
	for _, attempt := range receipt.Attempts {
		if !attempt.NoMaterial || len(attempt.Hits) != 0 || attempt.FilteredReason["missing_or_empty_index"] != 1 {
			t.Fatalf("missing index was not recorded as explicit no_material: %+v", attempt)
		}
	}
	plan := domain.ChapterPlan{Chapter: 1, CausalSimulation: domain.ChapterCausalSimulation{
		ContextSources: []string{craftReceiptSourceToken(receipt.ID)},
	}}
	if err := validateRewriteCraftConsumption(st, plan); err != nil {
		t.Fatalf("audited no_material must not force weak craft into plan: %v", err)
	}
}

func TestRewriteCraftConsumptionAndDraftProjectionUseOnlyTransformedMethods(t *testing.T) {
	st := newRewriteCraftTestStore(t, true)
	if err := validateRewriteCraftConsumption(st, domain.ChapterPlan{Chapter: 1}); err == nil || !strings.Contains(err.Error(), "旧计划没有自动 craft receipt") {
		t.Fatalf("active rewrite reused a historical plan without current RAG receipt: %v", err)
	}
	if err := st.Progress.ClearPendingRewrites(); err != nil {
		t.Fatal(err)
	}
	if err := validateRewriteCraftConsumption(st, domain.ChapterPlan{Chapter: 1}); err != nil {
		t.Fatalf("completed historical plan outside an active rewrite must remain compatible: %v", err)
	}
	if err := st.Progress.SetPendingRewrites([]int{1}, "局部修复"); err != nil {
		t.Fatal(err)
	}
	receipt, err := ensureRewriteCraftReceipt(st, 1, "")
	if err != nil {
		t.Fatal(err)
	}
	missing := domain.ChapterPlan{Chapter: 1, CausalSimulation: domain.ChapterCausalSimulation{
		ContextSources: []string{craftReceiptSourceToken(receipt.ID)},
	}}
	if err := validateRewriteCraftConsumption(st, missing); err == nil || !strings.Contains(err.Error(), "缺少 needs") {
		t.Fatalf("receipt hits must be consumed by the plan: %v", err)
	}

	plan := receiptBackedCraftPlan(receipt)
	if err := validateRewriteCraftConsumption(st, plan); err != nil {
		t.Fatalf("receipt-backed transformed plan should validate: %v", err)
	}
	packet := newDraftRenderPacket(plan)
	if len(packet.CraftMethods) != len(receipt.Attempts) || len(packet.CraftMethods) > 2 {
		t.Fatalf("draft render packet lost compact craft methods: %+v", packet.CraftMethods)
	}
	raw, err := json.Marshal(packet.CraftMethods)
	if err != nil {
		t.Fatal(err)
	}
	for _, attempt := range receipt.Attempts {
		for _, hit := range attempt.Hits {
			if hit.Summary != "" && strings.Contains(string(raw), hit.Summary) {
				t.Fatalf("draft packet leaked raw receipt summary: %s", raw)
			}
		}
	}
	if !strings.Contains(string(raw), "林澈先拒绝替对方补话") || strings.Contains(string(raw), "人物主观因果必须先于解释") {
		t.Fatalf("draft packet must carry only plan-transformed craft moves: %s", raw)
	}
}

func TestRewriteCraftRejectsOneGenericPlanForMultipleNeeds(t *testing.T) {
	st := newRewriteCraftTestStore(t, true)
	receipt, err := ensureRewriteCraftReceipt(st, 1, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(receipt.Attempts) < 2 || len(receipt.Attempts[0].Hits) == 0 || len(receipt.Attempts[1].Hits) == 0 {
		t.Fatalf("fixture needs two hit-bearing attempts: %+v", receipt.Attempts)
	}
	plan := domain.ChapterPlan{Chapter: 1}
	plan.CausalSimulation.ContextSources = []string{craftReceiptSourceToken(receipt.ID)}
	plan.CausalSimulation.ExternalRefs = []domain.ExternalReferencePlan{{
		QueryOrNeed: receipt.Attempts[0].Need.ID + " + " + receipt.Attempts[1].Need.ID,
		SourceType:  craftSourceType,
		SourceRefs: []string{
			receipt.Attempts[0].Hits[0].Ref,
			receipt.Attempts[1].Hits[0].Ref,
		},
		UsableDetails:      []string{"泛化地让本章更自然。"},
		TransformationRule: "泛化处理。",
		DoNotUse:           []string{"不复制原文。"},
	}}
	if err := validateRewriteCraftConsumption(st, plan); err == nil || !strings.Contains(err.Error(), "只能声明一个当前 need id") {
		t.Fatalf("two craft needs were formally consumed by one generic move: %v", err)
	}
}

func TestRewriteCraftRejectsDeclaredNeedWithoutReceiptSourceRef(t *testing.T) {
	st := newRewriteCraftTestStore(t, true)
	receipt, err := ensureRewriteCraftReceipt(st, 1, "")
	if err != nil {
		t.Fatal(err)
	}
	plan := receiptBackedCraftPlan(receipt)
	if len(plan.CausalSimulation.ExternalRefs) == 0 {
		t.Fatalf("fixture needs a hit-bearing craft plan: %+v", receipt.Attempts)
	}
	plan.CausalSimulation.ExternalRefs[0].SourceRefs = nil

	err = validateRewriteCraftConsumption(st, plan)
	if err == nil || !strings.Contains(err.Error(), "source_refs 至少必须包含一个") {
		t.Fatalf("declared need without a receipt ref was marked consumed: %v", err)
	}
	if methods := draftCraftMethods(plan.CausalSimulation.ExternalRefs); len(methods) != len(plan.CausalSimulation.ExternalRefs)-1 {
		t.Fatalf("fixture must prove the empty-ref need disappears from Drafter craft_methods: %+v", methods)
	}
}

func TestRewriteCraftRejectsReceiptRefOwnedByAnotherNeed(t *testing.T) {
	st := newRewriteCraftTestStore(t, true)
	receipt, err := ensureRewriteCraftReceipt(st, 1, "")
	if err != nil {
		t.Fatal(err)
	}
	plan := receiptBackedCraftPlan(receipt)
	if len(plan.CausalSimulation.ExternalRefs) < 2 {
		t.Fatalf("fixture needs two hit-bearing craft plans: %+v", receipt.Attempts)
	}
	firstNeed := plan.CausalSimulation.ExternalRefs[0].QueryOrNeed
	plan.CausalSimulation.ExternalRefs[0].SourceRefs = append([]string(nil), plan.CausalSimulation.ExternalRefs[1].SourceRefs...)

	err = validateRewriteCraftConsumption(st, plan)
	if err == nil || !strings.Contains(err.Error(), "只属于 needs=") {
		t.Fatalf("receipt ref from another need was accepted for %q: %v", firstNeed, err)
	}
}

func TestRewriteCraftRejectsDuplicateNeedAndProjectionKeepsDistinctNeeds(t *testing.T) {
	st := newRewriteCraftTestStore(t, true)
	receipt, err := ensureRewriteCraftReceipt(st, 1, "")
	if err != nil {
		t.Fatal(err)
	}
	plan := receiptBackedCraftPlan(receipt)
	if len(plan.CausalSimulation.ExternalRefs) != 2 {
		t.Fatalf("fixture needs two distinct craft plans: %+v", plan.CausalSimulation.ExternalRefs)
	}
	first := plan.CausalSimulation.ExternalRefs[0]
	plan.CausalSimulation.ExternalRefs = []domain.ExternalReferencePlan{
		first,
		first,
		plan.CausalSimulation.ExternalRefs[1],
	}
	if err := validateRewriteCraftConsumption(st, plan); err == nil || !strings.Contains(err.Error(), "重复出现") {
		t.Fatalf("duplicate plan for one need was accepted: %v", err)
	}
	methods := draftCraftMethods(plan.CausalSimulation.ExternalRefs)
	if len(methods) != 2 || methods[0].Need == methods[1].Need {
		t.Fatalf("duplicate need displaced another craft method in draft projection: %+v", methods)
	}
}

func TestNewRewriteSingleShotFinalizeCannotBypassCraftPreflight(t *testing.T) {
	st := newRewriteCraftTestStore(t, true)
	plan := domain.ChapterPlan{Chapter: 1}
	err := validateRewriteCraftFinalization(st, plan)
	if err == nil || !strings.Contains(err.Error(), "plan_structure") || !strings.Contains(err.Error(), "rewrite_craft_pack") {
		t.Fatalf("new single-shot rewrite bypassed automatic craft preflight: %v", err)
	}
	receipt, loadErr := ensureRewriteCraftReceipt(st, 1, "")
	if loadErr != nil || receipt == nil || receipt.ID == "" {
		t.Fatalf("failed single-shot attempt should still leave a reusable audited receipt: receipt=%+v err=%v", receipt, loadErr)
	}
}

func TestDraftContextRejectsCraftReceiptAfterIndexChanges(t *testing.T) {
	st := newRewriteCraftTestStore(t, true)
	receipt, err := ensureRewriteCraftReceipt(st, 1, "")
	if err != nil {
		t.Fatal(err)
	}
	plan := receiptBackedCraftPlan(receipt)
	if err := st.Drafts.SaveChapterPlan(plan); err != nil {
		t.Fatal(err)
	}
	saveRewriteCraftIndex(t, st, "rewrite-craft-index-v2")
	_, err = NewContextTool(st, References{}, "default").Execute(
		context.Background(),
		json.RawMessage(`{"chapter":1,"profile":"draft"}`),
	)
	if err == nil || !strings.Contains(err.Error(), "craft receipt 已失效") {
		t.Fatalf("draft context emitted stale craft_methods after index change: %v", err)
	}
}

func TestPlanStructurePrefetchAndLegacyPlanDetailsRecovery(t *testing.T) {
	st := newRewriteCraftTestStore(t, true)
	raw, err := NewPlanStructureTool(st).Execute(context.Background(), planStructureArgs(1))
	if err != nil {
		t.Fatal(err)
	}
	var response map[string]any
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatal(err)
	}
	if response["rewrite_craft_pack"] == nil {
		t.Fatalf("plan_structure response lost automatic craft preflight: %s", raw)
	}
	partial, err := st.Drafts.LoadChapterPlanPartial(1)
	if err != nil || partial == nil || strings.TrimSpace(craftTestString(partial[planCraftReceiptKey])) == "" {
		t.Fatalf("plan_structure did not persist receipt before partial: partial=%+v err=%v", partial, err)
	}
	contextRaw, err := NewContextTool(st, References{}, "default").Execute(context.Background(), json.RawMessage(`{"chapter":1}`))
	if err != nil {
		t.Fatal(err)
	}
	var stagedContext map[string]any
	if err := json.Unmarshal(contextRaw, &stagedContext); err != nil {
		t.Fatal(err)
	}
	if stagedContext["rewrite_craft_pack"] == nil {
		t.Fatalf("staged novel_context did not replay the persisted receipt: %s", contextRaw)
	}
	if _, err := NewCraftRecallTool(st).Execute(context.Background(), json.RawMessage(`{"field":"methodology","chapter":1}`)); err == nil || !strings.Contains(err.Error(), "staged plan repair") {
		t.Fatalf("direct craft_recall must remain blocked once staged planning begins: %v", err)
	}

	legacy := newRewriteCraftTestStore(t, true)
	var structure map[string]any
	if err := json.Unmarshal(planStructureArgs(1), &structure); err != nil {
		t.Fatal(err)
	}
	applyOutlineAnchorsToStructure(legacy, 1, structure)
	if err := applyRewriteAnchorsToStructure(legacy, 1, structure); err != nil {
		t.Fatal(err)
	}
	bindPlanStructureToSources(legacy, 1, structure, nil)
	if err := legacy.Drafts.SaveChapterPlanPartial(1, map[string]any{
		"structure": structure, "causal_simulation": map[string]any{}, "rewrite": true,
	}); err != nil {
		t.Fatal(err)
	}
	details, _ := json.Marshal(map[string]any{
		"chapter":           1,
		"causal_simulation": map[string]any{"chapter_function": "重建人物回应次序"},
	})
	if _, err := NewPlanDetailsTool(legacy).Execute(context.Background(), details); err != nil {
		t.Fatal(err)
	}
	recovered, err := legacy.Drafts.LoadChapterPlanPartial(1)
	if err != nil || recovered == nil || strings.TrimSpace(craftTestString(recovered[planCraftReceiptKey])) == "" {
		t.Fatalf("legacy partial did not recover a receipt: partial=%+v err=%v", recovered, err)
	}
	merged, _ := recovered["causal_simulation"].(map[string]any)
	if id := craftReceiptIDFromSources(stringSliceFromAny(merged["context_sources"])); id == "" {
		t.Fatalf("legacy recovery did not anchor receipt consumption token: %+v", merged)
	}
}

func TestStagedContextDoesNotReplayStaleCraftReceipt(t *testing.T) {
	st := newRewriteCraftTestStore(t, true)
	if _, err := NewPlanStructureTool(st).Execute(context.Background(), planStructureArgs(1)); err != nil {
		t.Fatal(err)
	}
	saveRewriteCraftIndex(t, st, "rewrite-craft-index-v2")
	raw, err := NewContextTool(st, References{}, "default").Execute(
		context.Background(), json.RawMessage(`{"chapter":1}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	if result["rewrite_craft_pack"] != nil || result["rewrite_craft_status"] != "stale_refresh_in_plan_details" {
		t.Fatalf("staged context replayed stale receipt: %s", raw)
	}
	if !strings.Contains(craftTestString(result["next_step"]), "自动刷新 receipt") {
		t.Fatalf("staged stale receipt recovery is not actionable: %s", raw)
	}
}

func TestStagedReceiptRefreshDropsOnlyStaleCraftReferences(t *testing.T) {
	current := &domain.CraftRecallReceipt{ID: "0123456789abcdef01234567"}
	currentHit := craftReceiptSourceToken(current.ID) + "#chunk=current#hash=h"
	merged := map[string]any{
		"external_reference_plan": []any{
			map[string]any{
				"source_type": craftSourceType,
				"source_refs": []any{craftReceiptSourceToken("abcdef0123456789abcdef01") + "#chunk=old#hash=h"},
			},
			map[string]any{
				"source_type": benchmarkCraftSourceType,
				"source_refs": []any{currentHit},
			},
			map[string]any{
				"source_type": "web_research",
				"source_refs": []any{"https://example.invalid/source"},
			},
		},
	}
	removeStalePartialCraftReferences(merged, current)
	items, _ := merged["external_reference_plan"].([]any)
	if len(items) != 2 {
		t.Fatalf("receipt refresh should drop stale craft only: %+v", items)
	}
	joined, _ := json.Marshal(items)
	if strings.Contains(string(joined), "chunk=old") || !strings.Contains(string(joined), "chunk=current") || !strings.Contains(string(joined), "web_research") {
		t.Fatalf("stale craft filtering damaged current/non-craft refs: %s", joined)
	}
}
