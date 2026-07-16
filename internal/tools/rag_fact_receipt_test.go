package tools

import (
	"context"
	"encoding/json"
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
