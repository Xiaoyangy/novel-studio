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
	"github.com/chenhongyang/novel-studio/internal/store"
)

func TestRAGFactReceiptPlanningToRenderChainAndFreshness(t *testing.T) {
	st := newRAGFactReceiptTestStore(t, []domain.RAGChunk{{
		ID:         "fact:night-rent",
		SourcePath: "summaries/00.json",
		SourceKind: "chapter_summary_facts",
		Facet:      "plot",
		Context:    "夜租商铺 租约 账单",
		Summary:    "欠费单必须先由承租人确认，钥匙交接后才开始试营业。",
		Text:       "项目事实：欠费单由承租人确认；钥匙交接后才能开始试营业。",
	}})
	raw, err := NewContextTool(st, References{}, "default").Execute(
		context.Background(), json.RawMessage(`{"chapter":1,"profile":"planning"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		ReferencePack struct {
			Receipt struct {
				ReceiptID           string `json:"receipt_id"`
				SourceToken         string `json:"source_token"`
				SelectedFactsSHA256 string `json:"selected_facts_sha256"`
				NoMaterial          bool   `json:"no_material"`
				Hits                []struct {
					Ref string `json:"ref"`
				} `json:"hits"`
			} `json:"rag_fact_receipt"`
		} `json:"reference_pack"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	contextReceipt := payload.ReferencePack.Receipt
	if contextReceipt.ReceiptID == "" || contextReceipt.SourceToken == "" ||
		contextReceipt.SelectedFactsSHA256 == "" || contextReceipt.NoMaterial || len(contextReceipt.Hits) == 0 {
		t.Fatalf("planning context lost fact receipt: %+v", contextReceipt)
	}
	receipt, err := st.RAG.LoadLatestRAGFactReceipt(1)
	if err != nil || receipt == nil {
		t.Fatalf("load latest fact receipt: receipt=%+v err=%v", receipt, err)
	}
	if receipt.Hits[0].ContentSHA256 != rag.RehashChunk(mustRAGFactChunk(t, st, "fact:night-rent")).Hash {
		t.Fatalf("receipt did not use a fresh semantic chunk hash: %+v", receipt.Hits[0])
	}
	formalPlan, err := decodeChapterPlanArgs(planArgs(1))
	if err != nil {
		t.Fatal(err)
	}
	formalPlan.CausalSimulation.ExternalRefs = append(formalPlan.CausalSimulation.ExternalRefs, domain.ExternalReferencePlan{
		QueryOrNeed: "租约交接事实", SourceType: "RAG", SourceRefs: []string{receipt.Hits[0].Ref},
		UsableDetails: []string{"欠费单需先确认"}, TransformationRule: "转成桌面动作与钥匙交接先后",
		DoNotUse: []string{"不复制摘要"},
	})
	formalPlan.CausalSimulation.GroundingDetails = append(formalPlan.CausalSimulation.GroundingDetails, domain.GroundingDetailPlan{
		Detail: "欠费单先由承租人确认", SourceRef: receipt.Hits[0].Ref,
		TransformedAs: "角色把欠费单推回桌面确认", SceneAnchor: "钥匙交接",
	})
	if _, err := finalizeChapterPlan(st, formalPlan, false); err != nil {
		t.Fatalf("formal plan finalize did not consume current fact receipt: %v", err)
	}
	savedPlan, err := st.Drafts.LoadChapterPlan(1)
	if err != nil || savedPlan == nil {
		t.Fatalf("load finalized receipt-backed plan: plan=%+v err=%v", savedPlan, err)
	}
	if id, factsSHA, count, err := ragFactReceiptIdentityFromSources(savedPlan.CausalSimulation.ContextSources); err != nil ||
		count != 1 || id != receipt.ID || factsSHA != receipt.SelectedFactsSHA256 {
		t.Fatalf("finalize failed to inject exact source token: id=%s facts=%s count=%d err=%v", id, factsSHA, count, err)
	}

	plan := domain.ChapterPlan{Chapter: 1, CausalSimulation: domain.ChapterCausalSimulation{
		ContextSources: []string{"rag_fact_receipt:forged#facts_sha256=forged"},
		ExternalRefs: []domain.ExternalReferencePlan{{
			QueryOrNeed: "租约交接事实", SourceType: "RAG", SourceRefs: []string{receipt.Hits[0].Ref},
			UsableDetails: []string{"欠费单需先确认"}, TransformationRule: "转成桌面动作与钥匙交接先后",
			DoNotUse: []string{"不复制摘要"},
		}},
		GroundingDetails: []domain.GroundingDetailPlan{{
			Detail: "欠费单先由承租人确认", SourceRef: receipt.Hits[0].Ref,
			TransformedAs: "角色把欠费单推回桌面确认", SceneAnchor: "钥匙交接",
		}},
	}}
	if err := bindLatestRAGFactReceiptToPlan(st, &plan); err != nil {
		t.Fatal(err)
	}
	if got := plan.CausalSimulation.ContextSources; len(got) != 1 || got[0] != receipt.SourceToken() {
		t.Fatalf("server did not replace forged token with current receipt: %v", got)
	}
	if err := ValidateRAGFactPlanCurrent(st, plan); err != nil {
		t.Fatalf("valid receipt-backed plan rejected: %v", err)
	}
	unconsumed := domain.ChapterPlan{Chapter: 1, CausalSimulation: domain.ChapterCausalSimulation{
		ContextSources: []string{receipt.SourceToken()},
	}}
	if err := ValidateRAGFactPlanCurrent(st, unconsumed); err == nil || !strings.Contains(err.Error(), "没有通过") {
		t.Fatalf("non-empty RAG receipt must be materially consumed by the formal plan: %v", err)
	}
	hangingRef := domain.ChapterPlan{Chapter: 1, CausalSimulation: domain.ChapterCausalSimulation{
		ContextSources: []string{receipt.SourceToken()},
		ExternalRefs: []domain.ExternalReferencePlan{{
			QueryOrNeed: "rag receipt", SourceType: "rag_recall", SourceRefs: []string{receipt.Hits[0].Ref},
		}},
	}}
	if err := ValidateRAGFactPlanCurrent(st, hangingRef); err == nil ||
		!strings.Contains(err.Error(), "usable_details/transformation_rule/do_not_use") {
		t.Fatalf("an exact hit ref without a scene transformation still only hangs RAG on the plan: %v", err)
	}
	hangingRef.CausalSimulation.ExternalRefs[0].UsableDetails = []string{" "}
	hangingRef.CausalSimulation.ExternalRefs[0].TransformationRule = "转成当前场景动作"
	hangingRef.CausalSimulation.ExternalRefs[0].DoNotUse = []string{""}
	if err := ValidateRAGFactPlanCurrent(st, hangingRef); err == nil ||
		!strings.Contains(err.Error(), "usable_details/transformation_rule/do_not_use") {
		t.Fatalf("blank transformation slices bypassed fact consumption: %v", err)
	}
	hangingRef.CausalSimulation.ExternalRefs[0].UsableDetails = []string{"欠费单需先确认"}
	hangingRef.CausalSimulation.ExternalRefs[0].TransformationRule = "转成承租人先确认欠费单、再交接钥匙的现场动作"
	hangingRef.CausalSimulation.ExternalRefs[0].DoNotUse = []string{"不复制召回摘要或来源人物"}
	if err := ValidateRAGFactPlanCurrent(st, hangingRef); err != nil {
		t.Fatalf("complete rag_recall transformation should be accepted: %v", err)
	}
	unboundRecall := domain.ChapterPlan{Chapter: 1, CausalSimulation: domain.ChapterCausalSimulation{
		ExternalRefs: []domain.ExternalReferencePlan{{
			QueryOrNeed: "伪造召回",
			SourceType:  "rag_recall",
			SourceRefs:  []string{"local:index#chunk=unbound"},
			UsableDetails: []string{
				"看似完整的事实",
			},
			TransformationRule: "写进当前场景",
			DoNotUse:           []string{"不复制原文"},
		}},
	}}
	if err := ValidateRAGFactPlanCurrent(st, unboundRecall); err == nil ||
		!strings.Contains(err.Error(), "不可追溯") {
		t.Fatalf("rag_recall without an exact receipt token/ref bypassed provenance: %v", err)
	}
	literaryOnly := domain.ChapterPlan{Chapter: 1, CausalSimulation: domain.ChapterCausalSimulation{
		ContextSources: []string{receipt.SourceToken()},
		LiteraryRendering: &domain.LiteraryRenderingPlan{
			Focalizer: "林澈", NarrativeAccess: domain.LiteraryNarrativeAccessInternal,
			KnowledgeBoundary: "只写现场", PerceptualBias: "先看票据", SummaryOmissionPolicy: "压缩手续",
			Afterimage: "钥匙留在桌上", SourceRefs: []string{receipt.Hits[0].Ref},
		},
	}}
	if err := ValidateRAGFactPlanCurrent(st, literaryOnly); err == nil ||
		!strings.Contains(err.Error(), "literary source") {
		t.Fatalf("literary metadata falsely satisfied fact-anchor consumption: %v", err)
	}
	realityOnly := domain.ChapterPlan{Chapter: 1, CausalSimulation: domain.ChapterCausalSimulation{
		ContextSources: []string{receipt.SourceToken()},
		RealitySupport: []domain.RealitySupportPlan{{
			SourceRef: receipt.Hits[0].Ref, Domain: "商铺交接", UsableDetail: "欠费单先确认",
			TransformedAs: "承租人把单据推回桌面", ChapterUse: "钥匙交接",
		}},
	}}
	if err := ValidateRAGFactPlanCurrent(st, realityOnly); err == nil ||
		!strings.Contains(err.Error(), "forbidden_direct_use") {
		t.Fatalf("incomplete reality support bypassed fact transformation: %v", err)
	}
	realityOnly.CausalSimulation.RealitySupport[0].ForbiddenDirectUse = []string{"不复制来源摘要"}
	if err := ValidateRAGFactPlanCurrent(st, realityOnly); err != nil {
		t.Fatalf("complete receipt-backed reality support should project an anchor: %v", err)
	}
	packet := newDraftRenderPacket(plan)
	packetJSON, _ := json.Marshal(packet)
	if !strings.Contains(string(packetJSON), "欠费单需先确认") ||
		!strings.Contains(string(packetJSON), `"authority":"rag_fact_receipt"`) {
		t.Fatalf("receipt-backed external fact did not reach Drafter as a transformed anchor: %s", packetJSON)
	}
	badRefPlan := plan
	badRefPlan.CausalSimulation.ExternalRefs = append([]domain.ExternalReferencePlan(nil), plan.CausalSimulation.ExternalRefs...)
	badRefPlan.CausalSimulation.ExternalRefs[0].SourceRefs = []string{
		domain.RAGFactReceiptTokenPrefix + receipt.ID + "#chunk=fact:night-rent#hash=" + strings.Repeat("f", 64),
	}
	if err := ValidateRAGFactPlanCurrent(st, badRefPlan); err == nil || !strings.Contains(err.Error(), "不属于当前 receipt") {
		t.Fatalf("forged selected chunk hash should be rejected: %v", err)
	}

	state, err := st.RAG.LoadIndexState()
	if err != nil || state == nil {
		t.Fatal(err)
	}
	state.Chunks = append(state.Chunks, domain.RAGChunk{
		ID: "fact:unrelated", SourcePath: "summaries/99.json", SourceKind: "chapter_summary_facts",
		Facet: "plot", Text: "与本章无关的新事实。", Summary: "无关新增。",
	})
	if err := st.RAG.SaveIndexState(*state); err != nil {
		t.Fatal(err)
	}
	if err := ValidateRAGFactPlanCurrent(st, plan); err != nil {
		t.Fatalf("unrelated additive chunk must not invalidate selected receipt: %v", err)
	}

	state, _ = st.RAG.LoadIndexState()
	for i := range state.Chunks {
		if state.Chunks[i].ID == "fact:night-rent" {
			state.Chunks[i].Text = "事实已被改写：允许先营业再交钥匙。"
		}
	}
	if err := st.RAG.SaveIndexState(*state); err != nil {
		t.Fatal(err)
	}
	if err := ValidateRAGFactPlanCurrent(st, plan); err == nil || !strings.Contains(err.Error(), "已变化") {
		t.Fatalf("selected chunk mutation should invalidate render plan: %v", err)
	}
	state, _ = st.RAG.LoadIndexState()
	kept := state.Chunks[:0]
	for _, chunk := range state.Chunks {
		if chunk.ID != "fact:night-rent" {
			kept = append(kept, chunk)
		}
	}
	state.Chunks = kept
	if err := st.RAG.SaveIndexState(*state); err != nil {
		t.Fatal(err)
	}
	if err := ValidateRAGFactPlanCurrent(st, plan); err == nil || !strings.Contains(err.Error(), "已删除") {
		t.Fatalf("selected chunk deletion should invalidate render plan: %v", err)
	}
}

func TestRAGFactReceiptNoMaterialIsExplicit(t *testing.T) {
	st := newRAGFactReceiptTestStore(t, nil)
	raw, err := NewContextTool(st, References{}, "default").Execute(
		context.Background(), json.RawMessage(`{"chapter":1,"profile":"planning"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"rag_fact_receipt"`) || !strings.Contains(string(raw), `"no_material":true`) {
		t.Fatalf("no-hit planning context must carry explicit no_material receipt: %s", raw)
	}
	receipt, err := st.RAG.LoadLatestRAGFactReceipt(1)
	if err != nil || receipt == nil || !receipt.NoMaterial || len(receipt.Hits) != 0 {
		t.Fatalf("invalid no_material receipt: %+v err=%v", receipt, err)
	}
	plan := domain.ChapterPlan{Chapter: 1}
	if err := bindLatestRAGFactReceiptToPlan(st, &plan); err != nil {
		t.Fatal(err)
	}
	if err := ValidateRAGFactPlanCurrent(st, plan); err != nil {
		t.Fatalf("explicit no_material plan should remain valid: %v", err)
	}
}

func TestStagedPlanningResponsesReplayCurrentRAGFactReceiptHits(t *testing.T) {
	st := newRAGFactReceiptTestStore(t, []domain.RAGChunk{{
		ID:         "fact:night-rent",
		SourcePath: "summaries/00.json",
		SourceKind: "chapter_summary_facts",
		Facet:      "plot",
		Context:    "夜租商铺 租约 账单",
		Summary:    "欠费单必须先由承租人确认。",
		Text:       "项目事实：欠费单由承租人确认；钥匙交接后才能开始试营业。",
	}})
	if _, err := NewContextTool(st, References{}, "default").Execute(
		context.Background(), json.RawMessage(`{"chapter":1,"profile":"planning"}`),
	); err != nil {
		t.Fatal(err)
	}
	receipt, err := st.RAG.LoadLatestRAGFactReceipt(1)
	if err != nil || receipt == nil || receipt.NoMaterial || len(receipt.Hits) == 0 {
		t.Fatalf("planning prefetch did not issue a material receipt: receipt=%+v err=%v", receipt, err)
	}

	structureRaw, err := NewPlanStructureTool(st).Execute(context.Background(), planStructureArgs(1))
	if err != nil {
		t.Fatal(err)
	}
	assertStagedRAGFactReceipt := func(stage string, raw json.RawMessage) {
		t.Helper()
		var response struct {
			Receipt struct {
				ReceiptID           string `json:"receipt_id"`
				SourceToken         string `json:"source_token"`
				SelectedFactsSHA256 string `json:"selected_facts_sha256"`
				NoMaterial          bool   `json:"no_material"`
				Hits                []struct {
					Ref string `json:"ref"`
				} `json:"hits"`
			} `json:"rag_fact_receipt"`
		}
		if err := json.Unmarshal(raw, &response); err != nil {
			t.Fatalf("decode %s response: %v", stage, err)
		}
		got := response.Receipt
		if got.ReceiptID != receipt.ID || got.SourceToken != receipt.SourceToken() ||
			got.SelectedFactsSHA256 != receipt.SelectedFactsSHA256 || got.NoMaterial ||
			len(got.Hits) != len(receipt.Hits) || got.Hits[0].Ref != receipt.Hits[0].Ref {
			t.Fatalf("%s response lost exact current fact hits: got=%+v want=%+v raw=%s", stage, got, receipt, raw)
		}
	}
	assertStagedRAGFactReceipt("plan_structure", structureRaw)

	detailsRaw, err := NewPlanDetailsTool(st).Execute(context.Background(), json.RawMessage(
		`{"chapter":1,"causal_simulation":{"decision_points":["先核对欠费单再交接钥匙"]}}`,
	))
	if err != nil {
		t.Fatal(err)
	}
	assertStagedRAGFactReceipt("plan_details", detailsRaw)
}

func TestStagedPlanRepairContextReplaysCurrentRAGFactReceiptWithoutRetrieval(t *testing.T) {
	st := newRAGFactReceiptTestStore(t, []domain.RAGChunk{{
		ID:         "fact:night-rent",
		SourcePath: "summaries/00.json",
		SourceKind: "chapter_summary_facts",
		Facet:      "plot",
		Context:    "夜租商铺 租约 账单",
		Summary:    "欠费单必须先由承租人确认。",
		Text:       "项目事实：欠费单由承租人确认；钥匙交接后才能开始试营业。",
	}})
	chunk := rag.RehashChunk(mustRAGFactChunk(t, st, "fact:night-rent"))
	receipt, err := domain.NewRAGFactReceipt(
		1,
		"夜租商铺 租约 账单",
		[]string{"夜租", "租约", "账单"},
		"local_bm25_keyword_hybrid_v1",
		"",
		[]domain.RAGFactReceiptHit{{
			Rank: 1, ChunkID: chunk.ID, ContentSHA256: chunk.Hash,
			SourcePath: chunk.SourcePath, SourceKind: chunk.SourceKind, Facet: chunk.Facet,
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.RAG.SaveRAGFactReceipt(receipt); err != nil {
		t.Fatal(err)
	}
	if err := st.Drafts.SaveChapterPlanPartial(1, map[string]any{
		"structure": map[string]any{
			"chapter": 1, "title": "夜租商铺", "goal": "核对欠费单",
			"conflict": "交接顺序不明", "hook": "钥匙仍在桌上",
		},
		"causal_simulation": map[string]any{"decision_points": []any{"先核对欠费单"}},
	}); err != nil {
		t.Fatal(err)
	}

	before, err := st.RAG.LoadLatestRAGFactReceipt(1)
	if err != nil || before == nil {
		t.Fatalf("load receipt before staged context: receipt=%+v err=%v", before, err)
	}
	raw, err := NewContextTool(st, References{
		LiteraryRenderingCards: `{"version":1,"cards":[{"id":"focalization-boundary","decision":"谁在感知"}]}`,
	}, "default").Execute(context.Background(), json.RawMessage(`{"chapter":1,"profile":"planning"}`))
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	pack, ok := payload["reference_pack"].(map[string]any)
	if !ok {
		t.Fatalf("staged context lost reference_pack: %s", raw)
	}
	if _, ok := pack["literary_rendering_cards"].(map[string]any); !ok {
		t.Fatalf("fact receipt overwrote literary cards: %#v", pack)
	}
	fact, ok := pack["rag_fact_receipt"].(map[string]any)
	if !ok || fact["receipt_id"] != receipt.ID || fact["source_token"] != receipt.SourceToken() {
		t.Fatalf("staged context lost exact receipt identity: %#v", pack["rag_fact_receipt"])
	}
	hits, ok := fact["hits"].([]any)
	if !ok || len(hits) != 1 || hits[0].(map[string]any)["ref"] != receipt.Hits[0].Ref {
		t.Fatalf("staged context lost exact hit refs: %#v", fact)
	}
	if selected, ok := payload["selected_memory"].(map[string]any); ok && selected["rag_recall"] != nil {
		t.Fatalf("staged partial fast path performed/exposed a new retrieval: %#v", selected)
	}
	if pack["retrieval_trace"] != nil {
		t.Fatalf("staged partial fast path exposed a new retrieval trace: %#v", pack["retrieval_trace"])
	}
	after, err := st.RAG.LoadLatestRAGFactReceipt(1)
	if err != nil || after == nil {
		t.Fatalf("load receipt after staged context: receipt=%+v err=%v", after, err)
	}
	if after.ID != before.ID || after.PayloadSHA256 != before.PayloadSHA256 ||
		after.SelectedFactsSHA256 != before.SelectedFactsSHA256 || after.CreatedAt != before.CreatedAt ||
		len(after.Hits) != len(before.Hits) || after.Hits[0].Ref != before.Hits[0].Ref {
		t.Fatalf("staged partial fast path changed latest receipt: before=%+v after=%+v", before, after)
	}
}

func TestNonDraftContextFailsClosedWhenRAGFactReceiptCannotPersist(t *testing.T) {
	st := newRAGFactReceiptTestStore(t, nil)
	blocked := filepath.Join(st.Dir(), "meta", "rag", "fact_receipts")
	if err := os.WriteFile(blocked, []byte("blocks receipt directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := NewContextTool(st, References{}, "default").Execute(
		context.Background(), json.RawMessage(`{"chapter":1,"profile":"planning"}`),
	)
	if err == nil || !strings.Contains(err.Error(), "persist chapter RAG fact receipt") {
		t.Fatalf("non-draft context continued after receipt persistence failure: %v", err)
	}
}

func TestDraftProfileDoesNotRetrieveOrReplaceLatestRAGFactReceipt(t *testing.T) {
	st := newRAGFactReceiptTestStore(t, []domain.RAGChunk{{
		ID: "fact:night-rent", SourcePath: "summaries/00.json", SourceKind: "chapter_summary_facts",
		Facet: "plot", Text: "钥匙交接前先确认欠费单。", Summary: "交接顺序。",
	}})
	tool := NewContextTool(st, References{}, "default")
	envelope := newChapterContextEnvelope()
	state := contextBuildState{
		chapter:          1,
		requestedProfile: "draft",
		profile:          domain.NewContextProfile(3),
		progress:         &domain.Progress{TotalChapters: 3},
		currentEntry:     &domain.OutlineEntry{Chapter: 1, Title: "夜租商铺"},
	}
	tool.buildChapterSelectedMemory(context.Background(), &envelope, state, func(string, error) {})
	if _, exists := envelope.Selected["rag_recall"]; exists {
		t.Fatalf("draft profile performed a live RAG recall: %#v", envelope.Selected)
	}
	if _, exists := envelope.References["retrieval_trace"]; exists {
		t.Fatalf("draft profile exposed a live retrieval trace: %#v", envelope.References)
	}
	receipt, err := st.RAG.LoadLatestRAGFactReceipt(1)
	if err != nil {
		t.Fatal(err)
	}
	if receipt != nil {
		t.Fatalf("draft profile replaced latest plan-bound receipt: %+v", receipt)
	}
}

func TestDraftPacketProjectsTransformedFactAnchorsWithoutRawRAG(t *testing.T) {
	receipt, err := domain.NewRAGFactReceipt(2, "夜市支架", []string{"夜市", "支架"}, "local_bm25_keyword_hybrid_v1", "", []domain.RAGFactReceiptHit{{
		Rank: 1, ChunkID: "fact:stand", ContentSHA256: strings.Repeat("a", 64),
		SourcePath: "summaries/01.json", SourceKind: "chapter_summary_facts", Facet: "plot",
	}})
	if err != nil {
		t.Fatal(err)
	}
	plan := domain.ChapterPlan{
		Chapter: 2, Title: "支架返工",
		CausalSimulation: domain.ChapterCausalSimulation{
			ContextSources: []string{receipt.SourceToken()},
			GroundingDetails: []domain.GroundingDetailPlan{{
				Detail: "周转箱碰撞后支架会偏斜", SourceRef: receipt.Hits[0].Ref,
				TransformedAs: "冷饮摊支架偏斜后返工", SceneAnchor: "冷饮摊",
			}},
			RealitySupport: []domain.RealitySupportPlan{{
				Domain: "夜市经营", SourceRef: receipt.Hits[0].Ref, UsableDetail: "通道边缘不得被支架占满",
				TransformedAs: "摊主主动把箱子收回线内", ChapterUse: "试用现场",
			}},
			EnvironmentState: []domain.EnvironmentSignal{{
				Place: "冷饮摊", VisibleState: "支架一角翘起", InformationCarried: "碰撞后需要重做",
				PressureApplied: "顾客经过时会再次撞上",
			}},
		},
	}
	result := map[string]any{
		"chapter_plan": &plan,
		"working_memory": map[string]any{
			"chapter_plan": &plan,
		},
		"selected_memory": map[string]any{
			"rag_recall": []domain.RecallItem{{Kind: "rag", Key: "fact:stand", Summary: "RAW_RAG_SECRET_SHOULD_NOT_REACH_DRAFTER"}},
		},
		"reference_pack": map[string]any{
			"retrieval_trace":  map[string]any{"query": "RAW_TRACE_SECRET"},
			"rag_fact_receipt": ragFactReceiptContext(&receipt),
		},
	}
	applyChapterContextProfile(result, "draft")
	working := result["working_memory"].(map[string]any)
	packet := working["render_packet"].(draftRenderPacket)
	if len(packet.FactAnchors) != 3 {
		t.Fatalf("transformed grounding/reality/environment facts were not projected: %+v", packet.FactAnchors)
	}
	encoded, _ := json.Marshal(result)
	if strings.Contains(string(encoded), "RAW_RAG_SECRET") || strings.Contains(string(encoded), "RAW_TRACE_SECRET") ||
		strings.Contains(string(encoded), `"rag_recall"`) || strings.Contains(string(encoded), `"retrieval_trace"`) {
		t.Fatalf("draft context leaked raw RAG instead of transformed anchors: %s", encoded)
	}
	formal, _ := result["formal_plan_receipt"].(map[string]any)
	binding, _ := formal["rag_fact_receipt"].(map[string]any)
	if binding["receipt_id"] != receipt.ID || binding["selected_facts_sha256"] != receipt.SelectedFactsSHA256 {
		t.Fatalf("formal plan receipt lost RAG fact binding: %#v", formal)
	}
}

func TestDraftCraftMethodsDropAlgorithmicParagraphRecipes(t *testing.T) {
	methods := draftCraftMethods([]domain.ExternalReferencePlan{{
		QueryOrNeed: "rewrite-methodology", SourceType: craftSourceType,
		SourceRefs: []string{"craft_recall_receipt:abcdef#chunk=x"},
		UsableDetails: []string{
			"P1观察、P2判断、P3动作，按相邻三段轮换职责。",
			"让主角先误判父亲的沉默，再因推来的鱼盘改变选择。",
		},
		TransformationRule: "观察→判断→动作→后果固定周期轮换。",
		DoNotUse:           []string{"不要照抄样本"},
	}})
	if len(methods) != 1 {
		t.Fatalf("expected one bounded craft method: %+v", methods)
	}
	raw, _ := json.Marshal(methods[0])
	text := string(raw)
	for _, forbidden := range []string{"P1观察", "相邻三段轮换职责", "固定周期轮换"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("algorithmic prose recipe leaked into Drafter packet: %s", text)
		}
	}
	if !strings.Contains(text, "主角先误判父亲") || methods[0].Risk == "" || methods[0].PersonCausalGoal == "" ||
		!strings.Contains(strings.Join(methods[0].Avoid, "\n"), "P1/P2/P3") {
		t.Fatalf("filter dropped the human causal target or hard avoid: %+v", methods[0])
	}
}

func newRAGFactReceiptTestStore(t *testing.T, chunks []domain.RAGChunk) *store.Store {
	t.Helper()
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Outline.SaveOutline([]domain.OutlineEntry{{
		Chapter: 1, Title: "夜租商铺", CoreEvent: "林砚用租约和欠费单打开资产链",
		Scenes: []string{"确认欠费单", "钥匙交接", "试营业"},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init("RAG fact receipt", 3); err != nil {
		t.Fatal(err)
	}
	if len(chunks) > 0 {
		if err := st.RAG.SaveIndexState(domain.RAGIndexState{
			SchemaVersion: domain.CurrentRAGIndexSchemaVersion,
			Config:        domain.RAGIndexConfig{Collection: "local_keyword"},
			Chunks:        chunks,
			UpdatedAt:     "fixture",
		}); err != nil {
			t.Fatal(err)
		}
	}
	return st
}

func mustRAGFactChunk(t *testing.T, st *store.Store, id string) domain.RAGChunk {
	t.Helper()
	state, err := st.RAG.LoadIndexState()
	if err != nil || state == nil {
		t.Fatalf("load RAG index: state=%+v err=%v", state, err)
	}
	for _, chunk := range state.Chunks {
		if chunk.ID == id {
			return chunk
		}
	}
	t.Fatalf("missing RAG chunk %s", id)
	return domain.RAGChunk{}
}
