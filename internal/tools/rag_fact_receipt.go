package tools

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/errs"
	"github.com/chenhongyang/novel-studio/internal/rag"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func persistChapterRAGFactReceipt(st *store.Store, state contextBuildState, trace *domain.RetrievalTrace) (*domain.RAGFactReceipt, error) {
	if st == nil || state.chapter <= 0 {
		return nil, nil
	}
	queryFields := recallFocusTerms(state.currentEntry, state.chapterPlan)
	queryFields = append(queryFields, state.chapterParticipants...)
	query := strings.TrimSpace(stateFocusText(state))
	terms := rag.QueryTerms(queryFields...)
	policy := "no_material_v1"
	traceSHA := ""
	var matches []domain.RetrievalTraceHit
	if trace != nil {
		if strings.TrimSpace(trace.Query) != "" {
			query = strings.TrimSpace(trace.Query)
		}
		terms = append([]string(nil), trace.QueryTerms...)
		if strings.TrimSpace(trace.Strategy) != "" {
			policy = strings.TrimSpace(trace.Strategy)
		}
		matches = append([]domain.RetrievalTraceHit(nil), trace.Matches...)
		traceSHA = ragFactTraceSHA256(*trace)
	}
	if query == "" {
		query = fmt.Sprintf("chapter:%d", state.chapter)
	}

	index, err := st.RAG.LoadIndexStateReadOnly()
	if err != nil {
		return nil, err
	}
	chunks := make(map[string]domain.RAGChunk)
	if index != nil {
		for _, chunk := range index.Chunks {
			chunk = rag.NormalizeChunk(chunk)
			if chunk.ID == "" || rag.IsForbiddenChunk(chunk) || rag.IsDesignOnlySourceKind(chunk.SourceKind) {
				continue
			}
			chunks[chunk.ID] = chunk
		}
	}
	hits := make([]domain.RAGFactReceiptHit, 0, len(matches))
	for i, match := range matches {
		chunk, ok := chunks[strings.TrimSpace(match.ChunkID)]
		if !ok {
			return nil, fmt.Errorf("RAG fact receipt selected chunk %q is absent from current project index", match.ChunkID)
		}
		freshHash := rag.RehashChunk(chunk).Hash
		if match.ContentSHA256 != "" && match.ContentSHA256 != freshHash {
			return nil, fmt.Errorf("RAG fact receipt selected chunk %q changed during retrieval", match.ChunkID)
		}
		hits = append(hits, domain.RAGFactReceiptHit{
			Rank:          i + 1,
			ChunkID:       chunk.ID,
			ContentSHA256: freshHash,
			SourcePath:    chunk.SourcePath,
			SourceKind:    chunk.SourceKind,
			Facet:         chunk.Facet,
		})
	}
	receipt, err := domain.NewRAGFactReceipt(state.chapter, query, terms, policy, traceSHA, hits)
	if err != nil {
		return nil, err
	}
	if err := st.RAG.SaveRAGFactReceipt(receipt); err != nil {
		return nil, err
	}
	return &receipt, nil
}

func ragFactTraceSHA256(trace domain.RetrievalTrace) string {
	trace.CreatedAt = ""
	raw, _ := json.Marshal(trace)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func ragFactReceiptContext(receipt *domain.RAGFactReceipt) map[string]any {
	if receipt == nil {
		return nil
	}
	hits := make([]map[string]any, 0, len(receipt.Hits))
	for _, hit := range receipt.Hits {
		hits = append(hits, map[string]any{
			"rank": hit.Rank, "ref": hit.Ref, "chunk_id": hit.ChunkID,
			"source_path": hit.SourcePath, "source_kind": hit.SourceKind, "facet": hit.Facet,
		})
	}
	return map[string]any{
		"receipt_id":            receipt.ID,
		"source_token":          receipt.SourceToken(),
		"chapter":               receipt.Chapter,
		"retrieval_policy":      receipt.RetrievalPolicy,
		"query":                 receipt.Query,
		"no_material":           receipt.NoMaterial,
		"selected_facts_sha256": receipt.SelectedFactsSHA256,
		"hits":                  hits,
		"usage_policy":          "rag_recall.summary 只供规划理解；若采用事实，external_reference_plan/grounding_details/reality_support_plan 必须使用 hits.ref，并先转成本书事实或现场细节。正文阶段不会重放 raw RAG。",
	}
}

// attachCurrentRAGFactReceiptContext keeps staged planning self-contained after
// the one allowed novel_context call. A convergence Planner may be forbidden
// from reading context again, so every plan_structure/non-final plan_details
// response must replay the exact current hit refs it is later required to
// consume. The live-index check remains fail-closed; this is visibility only,
// not a relaxation of formal-plan provenance validation.
func attachCurrentRAGFactReceiptContext(
	st *store.Store,
	chapter int,
	response map[string]any,
) error {
	if st == nil || chapter <= 0 || response == nil {
		return nil
	}
	context, err := currentRAGFactReceiptContext(st, chapter)
	if err != nil {
		return err
	}
	if context != nil {
		response["rag_fact_receipt"] = context
	}
	return nil
}

func currentRAGFactReceiptContext(st *store.Store, chapter int) (map[string]any, error) {
	if st == nil || chapter <= 0 {
		return nil, nil
	}
	receipt, err := st.RAG.LoadLatestRAGFactReceipt(chapter)
	if err != nil {
		return nil, fmt.Errorf("load current RAG fact receipt: %w", err)
	}
	if receipt == nil {
		return nil, nil
	}
	if err := validateRAGFactReceiptCurrent(st, *receipt); err != nil {
		return nil, err
	}
	return ragFactReceiptContext(receipt), nil
}

func bindLatestRAGFactReceiptToPlan(st *store.Store, plan *domain.ChapterPlan) error {
	if st == nil || plan == nil || plan.Chapter <= 0 {
		return nil
	}
	filtered := plan.CausalSimulation.ContextSources[:0]
	for _, source := range plan.CausalSimulation.ContextSources {
		if strings.HasPrefix(strings.TrimSpace(source), domain.RAGFactReceiptTokenPrefix) {
			continue
		}
		filtered = append(filtered, source)
	}
	plan.CausalSimulation.ContextSources = filtered
	receipt, err := st.RAG.LoadLatestRAGFactReceipt(plan.Chapter)
	if err != nil {
		return fmt.Errorf("load latest RAG fact receipt: %w", err)
	}
	if receipt == nil {
		return nil
	}
	if err := validateRAGFactReceiptCurrent(st, *receipt); err != nil {
		return err
	}
	plan.CausalSimulation.ContextSources = appendUniqueString(plan.CausalSimulation.ContextSources, receipt.SourceToken())
	return nil
}

// ValidateRAGFactPlanCurrent proves both provenance and live-index freshness.
// Only chunks selected by the receipt participate, so an unrelated additive
// index update does not invalidate a staged plan.
func ValidateRAGFactPlanCurrent(st *store.Store, plan domain.ChapterPlan) error {
	return validateRAGFactPlan(st, plan, ragFactPlanValidation{})
}

// ValidateRAGFactPlanSealed replays every plan/receipt provenance,
// transformation and render-anchor check without requiring the selected chunk
// to remain in the mutable live index. Callers must independently prove that
// the receipt came from the exact sealed bundle being rendered.
func ValidateRAGFactPlanSealed(st *store.Store, plan domain.ChapterPlan) error {
	return validateRAGFactPlan(st, plan, ragFactPlanValidation{
		skipLiveIndexMembership: true,
	})
}

type ragFactPlanValidation struct {
	skipLiveIndexMembership bool
	sealedReceipt           *domain.RAGFactReceipt
}

func validateRAGFactPlan(
	st *store.Store,
	plan domain.ChapterPlan,
	validation ragFactPlanValidation,
) error {
	receiptID, factsSHA, count, err := ragFactReceiptIdentityFromSources(plan.CausalSimulation.ContextSources)
	if err != nil {
		return err
	}
	hasFactReferences := planHasRAGFactReferences(plan)
	if planUsesRAGFactReceiptAsLiterarySource(plan) {
		return fmt.Errorf("第 %d 章把普通事实 RAG receipt 当作 literary source；必须先通过 external_reference_plan/grounding_details/reality_support_plan 转成可投影现场事实: %w",
			plan.Chapter, errs.ErrToolPrecondition)
	}
	if planClaimsUntraceableRAGFacts(plan) {
		return fmt.Errorf("第 %d 章普通事实 RAG 引用未使用当前 rag_fact_receipt hit ref，来源不可追溯: %w",
			plan.Chapter, errs.ErrToolPrecondition)
	}
	if count == 0 {
		if hasFactReferences {
			return fmt.Errorf("第 %d 章使用了普通事实 RAG，但 formal plan 没有服务端 rag_fact_receipt source token: %w", plan.Chapter, errs.ErrToolPrecondition)
		}
		return nil
	}
	var receipt *domain.RAGFactReceipt
	if validation.sealedReceipt != nil {
		exact := *validation.sealedReceipt
		receipt = &exact
	} else {
		receipt, err = st.RAG.LoadRAGFactReceipt(receiptID)
		if err != nil {
			return fmt.Errorf("load RAG fact receipt %s: %w", receiptID, err)
		}
	}
	if receipt == nil || receipt.ID != receiptID || receipt.Chapter != plan.Chapter {
		return fmt.Errorf("第 %d 章 RAG fact receipt %s 不存在或章节不匹配: %w", plan.Chapter, receiptID, errs.ErrToolPrecondition)
	}
	if receipt.SelectedFactsSHA256 != factsSHA {
		return fmt.Errorf("第 %d 章 RAG fact receipt selected facts hash 不匹配: %w", plan.Chapter, errs.ErrToolPrecondition)
	}
	if validation.skipLiveIndexMembership {
		if err := domain.ValidateRAGFactReceipt(*receipt); err != nil {
			return fmt.Errorf("invalid sealed RAG fact receipt %s: %w", receipt.ID, err)
		}
	} else {
		if err := validateRAGFactReceiptCurrent(st, *receipt); err != nil {
			return err
		}
	}
	if !receipt.NoMaterial && !hasFactReferences {
		return fmt.Errorf("第 %d 章 RAG fact receipt %s 选中了 %d 个事实 chunk，但 formal plan 没有通过 external_reference_plan/grounding_details/reality_support_plan 消费任何 hit ref；禁止把 RAG 只当挂名来源: %w",
			plan.Chapter, receipt.ID, len(receipt.Hits), errs.ErrToolPrecondition)
	}
	allowed := make(map[string]struct{}, len(receipt.Hits))
	selectedAliases := make(map[string]struct{}, len(receipt.Hits)*2)
	for _, hit := range receipt.Hits {
		allowed[hit.Ref] = struct{}{}
		selectedAliases[hit.ChunkID] = struct{}{}
		selectedAliases[hit.SourcePath] = struct{}{}
	}
	for _, ref := range planRAGFactReferences(plan) {
		if _, ok := allowed[ref]; !ok {
			return fmt.Errorf("第 %d 章普通事实 RAG 引用 %q 不属于当前 receipt %s 的 selected hits: %w",
				plan.Chapter, ref, receipt.ID, errs.ErrToolPrecondition)
		}
	}
	for _, ref := range planPotentialRAGFactSourceRefs(plan) {
		ref = strings.TrimSpace(ref)
		if _, selectedAlias := selectedAliases[ref]; selectedAlias {
			return fmt.Errorf("第 %d 章普通事实 RAG 引用 %q 只写了 chunk/path 别名，必须使用 receipt hits.ref: %w",
				plan.Chapter, ref, errs.ErrToolPrecondition)
		}
	}
	if err := validateRAGFactTransformations(plan); err != nil {
		return err
	}
	if !receipt.NoMaterial {
		packet := newDraftRenderPacket(plan)
		hasProjectedAnchor := false
		for _, anchor := range packet.FactAnchors {
			if anchor.Authority == "rag_fact_receipt" &&
				strings.HasPrefix(strings.TrimSpace(anchor.SourceRef), domain.RAGFactReceiptTokenPrefix) &&
				strings.TrimSpace(anchor.Fact) != "" {
				hasProjectedAnchor = true
				break
			}
		}
		if !hasProjectedAnchor {
			return fmt.Errorf("第 %d 章 RAG fact receipt 已登记引用，但 v11 render_packet 没有任何可消费的 receipt-backed fact anchor；必须重做本章化转换: %w",
				plan.Chapter, errs.ErrToolPrecondition)
		}
	}
	return nil
}

func validateRAGFactReceiptCurrent(st *store.Store, receipt domain.RAGFactReceipt) error {
	if err := domain.ValidateRAGFactReceipt(receipt); err != nil {
		return fmt.Errorf("invalid RAG fact receipt %s: %w", receipt.ID, err)
	}
	index, err := st.RAG.LoadIndexStateReadOnly()
	if err != nil {
		return fmt.Errorf("load current RAG fact index: %w", err)
	}
	current := make(map[string]domain.RAGChunk)
	if index != nil {
		for _, chunk := range index.Chunks {
			chunk = rag.NormalizeChunk(chunk)
			if chunk.ID != "" {
				current[chunk.ID] = chunk
			}
		}
	}
	for _, hit := range receipt.Hits {
		chunk, ok := current[hit.ChunkID]
		if !ok || rag.IsForbiddenChunk(chunk) || rag.IsDesignOnlySourceKind(chunk.SourceKind) {
			return fmt.Errorf("第 %d 章 RAG fact receipt %s 的 selected chunk %q 已删除或不再允许用于事实召回: %w",
				receipt.Chapter, receipt.ID, hit.ChunkID, errs.ErrToolPrecondition)
		}
		freshHash := rag.RehashChunk(chunk).Hash
		if freshHash != hit.ContentSHA256 || chunk.SourcePath != hit.SourcePath ||
			chunk.SourceKind != hit.SourceKind || chunk.Facet != hit.Facet {
			return fmt.Errorf("第 %d 章 RAG fact receipt %s 的 selected chunk %q 内容或路由身份已变化: %w",
				receipt.Chapter, receipt.ID, hit.ChunkID, errs.ErrToolPrecondition)
		}
	}
	return nil
}

func ragFactReceiptIdentityFromSources(sources []string) (id, factsSHA string, count int, err error) {
	for _, source := range sources {
		source = strings.TrimSpace(source)
		if !strings.HasPrefix(source, domain.RAGFactReceiptTokenPrefix) {
			continue
		}
		count++
		payload := strings.TrimPrefix(source, domain.RAGFactReceiptTokenPrefix)
		parts := strings.Split(payload, "#facts_sha256=")
		if len(parts) != 2 || len(parts[0]) != 24 || len(strings.TrimSpace(parts[1])) != 64 {
			return "", "", count, fmt.Errorf("malformed RAG fact receipt source token %q: %w", source, errs.ErrToolPrecondition)
		}
		if count > 1 {
			return "", "", count, fmt.Errorf("formal plan may bind exactly one RAG fact receipt: %w", errs.ErrToolPrecondition)
		}
		id, factsSHA = parts[0], parts[1]
	}
	return id, factsSHA, count, nil
}

func planHasRAGFactReferences(plan domain.ChapterPlan) bool {
	return len(planRAGFactReferences(plan)) > 0 || planClaimsUntraceableRAGFacts(plan)
}

func planRAGFactReferences(plan domain.ChapterPlan) []string {
	var refs []string
	add := func(ref string) {
		ref = strings.TrimSpace(ref)
		if strings.HasPrefix(ref, domain.RAGFactReceiptTokenPrefix) {
			refs = appendUniqueString(refs, ref)
		}
	}
	for _, external := range plan.CausalSimulation.ExternalRefs {
		for _, ref := range external.SourceRefs {
			add(ref)
		}
	}
	for _, grounding := range plan.CausalSimulation.GroundingDetails {
		add(grounding.SourceRef)
	}
	for _, support := range plan.CausalSimulation.RealitySupport {
		add(support.SourceRef)
	}
	return refs
}

func planUsesRAGFactReceiptAsLiterarySource(plan domain.ChapterPlan) bool {
	literary := plan.CausalSimulation.LiteraryRendering
	if literary == nil {
		return false
	}
	for _, ref := range literary.SourceRefs {
		if strings.HasPrefix(strings.TrimSpace(ref), domain.RAGFactReceiptTokenPrefix) {
			return true
		}
	}
	for _, lens := range literary.ActiveLenses {
		for _, ref := range lens.SourceRefs {
			if strings.HasPrefix(strings.TrimSpace(ref), domain.RAGFactReceiptTokenPrefix) {
				return true
			}
		}
	}
	return false
}

func planClaimsUntraceableRAGFacts(plan domain.ChapterPlan) bool {
	for _, external := range plan.CausalSimulation.ExternalRefs {
		sourceType := strings.ToLower(strings.TrimSpace(external.SourceType))
		if strings.Contains(sourceType, "craft") {
			continue
		}
		if isRAGFactSourceType(sourceType) {
			if len(external.SourceRefs) == 0 {
				return true
			}
			for _, ref := range external.SourceRefs {
				if !strings.HasPrefix(strings.TrimSpace(ref), domain.RAGFactReceiptTokenPrefix) {
					return true
				}
			}
		}
	}
	for _, ref := range append(planGroundingSourceRefs(plan), planRealitySupportSourceRefs(plan)...) {
		if looksLikeUnboundRAGFactReference(ref) {
			return true
		}
	}
	return false
}

func isRAGFactSourceType(sourceType string) bool {
	sourceType = strings.ToLower(strings.TrimSpace(sourceType))
	if strings.Contains(sourceType, "craft") {
		return false
	}
	return sourceType == "rag" ||
		strings.Contains(sourceType, "rag_recall") ||
		strings.Contains(sourceType, "fact_rag") ||
		strings.Contains(sourceType, "rag_fact")
}

func validateRAGFactTransformations(plan domain.ChapterPlan) error {
	for i, external := range plan.CausalSimulation.ExternalRefs {
		sourceType := strings.ToLower(strings.TrimSpace(external.SourceType))
		usesReceiptHit := false
		for _, ref := range external.SourceRefs {
			if strings.HasPrefix(strings.TrimSpace(ref), domain.RAGFactReceiptTokenPrefix) {
				usesReceiptHit = true
				break
			}
		}
		claimsFactRAG := isRAGFactSourceType(sourceType)
		if strings.Contains(sourceType, "craft") && usesReceiptHit {
			return fmt.Errorf("第 %d 章 external_reference_plan[%d] 把普通事实 receipt hit 标成 craft；必须按事实来源转换，不能绕过 usable_details/transformation_rule/do_not_use: %w",
				plan.Chapter, i, errs.ErrToolPrecondition)
		}
		if !usesReceiptHit && !claimsFactRAG {
			continue
		}
		if strings.TrimSpace(external.QueryOrNeed) == "" || len(external.SourceRefs) == 0 ||
			len(compactStrings(external.UsableDetails)) == 0 || strings.TrimSpace(external.TransformationRule) == "" ||
			len(compactStrings(external.DoNotUse)) == 0 {
			return fmt.Errorf("第 %d 章 external_reference_plan[%d] 的普通事实 RAG 必须完整填写 query/source_refs/usable_details/transformation_rule/do_not_use: %w",
				plan.Chapter, i, errs.ErrToolPrecondition)
		}
	}
	for i, grounding := range plan.CausalSimulation.GroundingDetails {
		if !strings.HasPrefix(strings.TrimSpace(grounding.SourceRef), domain.RAGFactReceiptTokenPrefix) {
			continue
		}
		if strings.TrimSpace(grounding.Detail) == "" || strings.TrimSpace(grounding.TransformedAs) == "" ||
			strings.TrimSpace(grounding.SceneAnchor) == "" {
			return fmt.Errorf("第 %d 章 grounding_details[%d] 的 receipt-backed fact 必须填写 detail/transformed_as/scene_anchor: %w",
				plan.Chapter, i, errs.ErrToolPrecondition)
		}
	}
	for i, support := range plan.CausalSimulation.RealitySupport {
		if !strings.HasPrefix(strings.TrimSpace(support.SourceRef), domain.RAGFactReceiptTokenPrefix) {
			continue
		}
		if strings.TrimSpace(support.Domain) == "" || strings.TrimSpace(support.UsableDetail) == "" ||
			strings.TrimSpace(support.TransformedAs) == "" || strings.TrimSpace(support.ChapterUse) == "" ||
			len(compactStrings(support.ForbiddenDirectUse)) == 0 {
			return fmt.Errorf("第 %d 章 reality_support_plan[%d] 的 receipt-backed fact 必须填写 domain/usable_detail/transformed_as/chapter_use/forbidden_direct_use: %w",
				plan.Chapter, i, errs.ErrToolPrecondition)
		}
	}
	return nil
}

func planPotentialRAGFactSourceRefs(plan domain.ChapterPlan) []string {
	var refs []string
	for _, external := range plan.CausalSimulation.ExternalRefs {
		refs = append(refs, external.SourceRefs...)
	}
	refs = append(refs, planGroundingSourceRefs(plan)...)
	refs = append(refs, planRealitySupportSourceRefs(plan)...)
	return refs
}

func planGroundingSourceRefs(plan domain.ChapterPlan) []string {
	refs := make([]string, 0, len(plan.CausalSimulation.GroundingDetails))
	for _, item := range plan.CausalSimulation.GroundingDetails {
		refs = append(refs, item.SourceRef)
	}
	return refs
}

func planRealitySupportSourceRefs(plan domain.ChapterPlan) []string {
	refs := make([]string, 0, len(plan.CausalSimulation.RealitySupport))
	for _, item := range plan.CausalSimulation.RealitySupport {
		refs = append(refs, item.SourceRef)
	}
	return refs
}

func looksLikeUnboundRAGFactReference(ref string) bool {
	ref = strings.ToLower(strings.TrimSpace(ref))
	if ref == "" || strings.HasPrefix(ref, domain.RAGFactReceiptTokenPrefix) {
		return false
	}
	return strings.Contains(ref, "rag") || strings.Contains(ref, "retrieval_trace") ||
		strings.Contains(ref, "selected_memory") || strings.HasPrefix(ref, "fact:") ||
		strings.HasPrefix(ref, "chunk:") || strings.HasPrefix(ref, "chapter:")
}
