package tools

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/chenhongyang/novel-studio/internal/aigc"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/errs"
	"github.com/chenhongyang/novel-studio/internal/rag"
	"github.com/chenhongyang/novel-studio/internal/store"
)

const (
	rewriteCraftReceiptVersion = 1
	rewriteCraftStagePlan      = "plan"
	planCraftReceiptKey        = "_craft_recall_receipt_id"
	craftReceiptTokenPrefix    = "craft_recall_receipt:"
	craftSourceType            = "craft_recall"
	benchmarkCraftSourceType   = "benchmark_craft_recall"
)

func projectAllCraftNeeds(st *store.Store, chapter int) ([]domain.CraftRecallNeed, error) {
	if st == nil || chapter <= 0 {
		return nil, fmt.Errorf("project-all craft needs require store and chapter")
	}
	outline, err := st.Outline.GetChapterOutline(chapter)
	if err != nil || outline == nil {
		return nil, fmt.Errorf("load chapter %d outline for project-all craft needs: %w", chapter, err)
	}
	bounded := func(value string, limit int) string {
		value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
		runes := []rune(value)
		if len(runes) > limit {
			value = string(runes[:limit])
		}
		return value
	}
	focusParts := compactStrings([]string{
		bounded(outline.Title, 32),
		bounded(outline.CoreEvent, 72),
		bounded(outline.Hook, 48),
	})
	sceneParts := make([]string, 0, 2)
	for _, scene := range outline.Scenes {
		if scene = bounded(scene, 48); scene != "" {
			sceneParts = append(sceneParts, scene)
		}
		if len(sceneParts) == 2 {
			break
		}
	}
	focus := strings.Join(focusParts, " ")
	sceneFocus := strings.Join(sceneParts, " ")
	probe := strings.ToLower(strings.Join([]string{focus, sceneFocus}, " "))
	secondaryField := rag.CraftFieldSceneCraft
	secondaryID := "project-all-scene"
	secondaryBase := "场景调度 空间压力 环境承载 误判 后果 过场省略"
	if rewriteCraftContainsAny(probe, "对白", "对话", "交涉", "谈判", "争执", "质问", "审讯", "会面", "饭局", "开口", "询问") {
		secondaryField = rag.CraftFieldDialogue
		secondaryID = "project-all-dialogue"
		secondaryBase = "对白摩擦 潜台词 打断 漏答 权力转移 声口差异 信息释放"
	}
	trigger := fmt.Sprintf("outline.json#chapter=%d", chapter)
	needs := []domain.CraftRecallNeed{
		{
			ID:          "project-all-methodology",
			Field:       string(rag.CraftFieldMethodology),
			Topic:       strings.TrimSpace("人物主观因果 章节节奏 信息延迟 场景后果 叙事功能变化 " + focus),
			TriggerRefs: []string{trigger, "project_all.required.methodology"},
		},
		{
			ID:          secondaryID,
			Field:       string(secondaryField),
			Topic:       strings.TrimSpace(strings.Join([]string{secondaryBase, focus, sceneFocus}, " ")),
			TriggerRefs: []string{trigger, "project_all.required.dialogue_or_scene"},
		},
	}
	return needs, nil
}

func deriveRewriteCraftNeeds(st *store.Store, chapter int) []domain.CraftRecallNeed {
	if st == nil || chapter <= 0 {
		return nil
	}
	source, _, brief, err := loadChapterRewriteSource(st, chapter)
	if err != nil || source == nil {
		return nil
	}
	bodySHA := strings.TrimSpace(source.BodySHA256)
	currentBody := func(candidate string) bool {
		candidate = strings.TrimSpace(candidate)
		return candidate != "" && bodySHA != "" && candidate == bodySHA
	}

	triggerByField := map[string][]string{}
	add := func(field, ref string) {
		if strings.TrimSpace(ref) == "" {
			return
		}
		triggerByField[field] = appendUniqueString(triggerByField[field], ref)
	}
	classify := func(text, ref string) {
		lower := strings.ToLower(text)
		if rewriteCraftContainsAny(lower,
			"aigc", "ai率", "ai 率", "ai味", "ai 味", "主视角", "主观", "内省", "情绪因果",
			"句长", "段长", "段落", "节奏", "结构指纹", "概率曲线", "熵", "流程密度", "解释腔") {
			add(string(rag.CraftFieldMethodology), ref)
		}
		if rewriteCraftContainsAny(lower,
			"dialogue_", "对白", "对话", "台词", "声口", "传送带", "信息倾倒", "报幕", "潜台词") {
			add(string(rag.CraftFieldDialogue), ref)
		}
		if rewriteCraftContainsAny(lower, "场景调度", "场景单一", "过场", "环境承载", "流程腔", "验收录像", "场面") {
			add(string(rag.CraftFieldSceneCraft), ref)
		}
	}
	// The rewrite brief is the current, source-bound repair contract even when
	// the review artifact that originally produced it refers to an older body
	// SHA. Ignoring it here made a valid current rewrite lose methodology and
	// dialogue craft recall precisely when the old exact-body review had been
	// superseded.
	classify(brief, "rewrite_brief")

	if review, err := st.World.LoadReview(chapter); err == nil && review != nil && currentBody(review.BodySHA256) {
		classify(review.Summary, "review.summary")
		for i, issue := range review.Issues {
			classify(issue.Type+" "+issue.Description+" "+issue.Suggestion, fmt.Sprintf("review.issue:%d", i))
		}
	}
	if analysis, err := st.AIVoice.LoadRedFlags(chapter); err == nil && analysis != nil && currentBody(analysis.BodySHA256) {
		if actionable := domain.ActionableAIVoiceAnalysis(analysis); actionable != nil {
			for _, flag := range actionable.RedFlags {
				classify(flag.Rule+" "+flag.Suggestion, "ai_voice.rule:"+flag.Rule)
			}
		}
	}
	if gate := loadRewriteCraftGate(st.Dir(), chapter, bodySHA); gate != nil {
		if aigc.EffectiveGatePercent(gate.AIGCReport) >= aigc.PassExclusivePercent {
			add(string(rag.CraftFieldMethodology), "mechanical_gate:aigc_ratio")
		}
		for _, violation := range gate.RuleViolations {
			classify(violation.Rule, "mechanical_gate.rule:"+violation.Rule)
		}
		for _, dimensions := range []map[string]aigc.Dimension{
			gate.AIGCReport.Dimensions,
			gate.AIGCReport.LatestDetectorProxy.Components,
		} {
			for _, dimension := range dimensions {
				for _, signal := range dimension.Signals {
					classify(signal.Name, "aigc.signal:"+signal.Name)
				}
			}
		}
	}

	makeNeed := func(id string, field rag.CraftDesignField, topic string) domain.CraftRecallNeed {
		refs := compactStrings(triggerByField[string(field)])
		sort.Strings(refs)
		return domain.CraftRecallNeed{ID: id, Field: string(field), Topic: topic, TriggerRefs: refs}
	}
	var needs []domain.CraftRecallNeed
	if len(triggerByField[string(rag.CraftFieldMethodology)]) > 0 {
		needs = append(needs, makeNeed(
			"rewrite-methodology",
			rag.CraftFieldMethodology,
			"人物主观因果 叙事节奏 句段功能变化 信息延迟 场景后果 AI检测",
		))
	}
	if len(triggerByField[string(rag.CraftFieldDialogue)]) > 0 && len(needs) < 2 {
		needs = append(needs, makeNeed(
			"rewrite-dialogue",
			rag.CraftFieldDialogue,
			"对白摩擦 潜台词 打断 漏答 权力转移 声口差异 信息释放",
		))
	}
	if len(triggerByField[string(rag.CraftFieldSceneCraft)]) > 0 && len(needs) < 2 {
		needs = append(needs, makeNeed(
			"rewrite-scene",
			rag.CraftFieldSceneCraft,
			"场景调度 流程压缩 环境承载 误判 后果 过场省略",
		))
	}
	return needs
}

func rewriteCraftContainsAny(text string, values ...string) bool {
	for _, value := range values {
		if value != "" && strings.Contains(text, strings.ToLower(value)) {
			return true
		}
	}
	return false
}

func loadRewriteCraftGate(dir string, chapter int, bodySHA string) *mechanicalGateReviewPayload {
	bodySHA = strings.TrimSpace(bodySHA)
	for _, rel := range []string{
		fmt.Sprintf("reviews/%02d_ai_gate.json", chapter),
		fmt.Sprintf("reviews_ai/%02d.json", chapter),
	} {
		raw, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil {
			continue
		}
		var payload mechanicalGateReviewPayload
		if json.Unmarshal(raw, &payload) == nil && payload.Chapter == chapter &&
			bodySHA != "" && strings.TrimSpace(payload.BodySHA256) == bodySHA {
			return &payload
		}
	}
	return nil
}

func projectAllCraftReceiptInputs(
	st *store.Store,
	chapter int,
) (generationID, planningContextDigest string, active bool, err error) {
	if st == nil || chapter <= 0 {
		return "", "", false, nil
	}
	lock, err := st.Runtime.LoadPipelineExecution()
	if err != nil {
		return "", "", false, err
	}
	if lock == nil || lock.Mode != domain.PipelineExecutionProjectAll {
		return "", "", false, nil
	}
	if err := requireCurrentPipelineExecutionProcess(lock, "project-all craft receipt"); err != nil {
		return "", "", false, err
	}
	if lock.TargetChapter != chapter {
		return "", "", false, fmt.Errorf("project-all craft receipt chapter=%d does not match execution target=%d: %w",
			chapter, lock.TargetChapter, errs.ErrToolPrecondition)
	}
	planningContext, _, err := loadProjectAllStateForExecution(st, chapter)
	if err != nil {
		return "", "", false, err
	}
	if planningContext == nil {
		return "", "", false, fmt.Errorf("project-all craft receipt requires authoritative planning context: %w", errs.ErrToolPrecondition)
	}
	return planningContext.GenerationID, planningContext.ContextDigest, true, nil
}

// EnsureProjectAllCraftReceipt materializes the mandatory, content-addressed
// method recall before a fresh project-all plan is finalized. The project index
// itself must be nonempty; individual chapterized needs may still record
// explicit no_material. Persistence and audit failures are fatal.
func EnsureProjectAllCraftReceipt(
	st *store.Store,
	chapter int,
	generationID string,
	planningContextDigest string,
) (*domain.CraftRecallReceipt, error) {
	actualGeneration, actualContext, active, err := projectAllCraftReceiptInputs(st, chapter)
	if err != nil {
		return nil, err
	}
	if !active {
		return nil, fmt.Errorf("project-all craft receipt requires an active project-all execution lease: %w", errs.ErrToolPrecondition)
	}
	if strings.TrimSpace(generationID) != actualGeneration ||
		strings.TrimSpace(planningContextDigest) != actualContext {
		return nil, fmt.Errorf("project-all craft receipt generation/planning context drift: %w", errs.ErrToolPrecondition)
	}
	state, err := st.RAG.LoadIndexStateReadOnly()
	if err != nil {
		return nil, fmt.Errorf("load project-all craft RAG index: %w", err)
	}
	if state == nil || len(state.Chunks) == 0 {
		return nil, fmt.Errorf("project-all requires a non-empty meta/rag/index_state.json before planning chapter %d: %w",
			chapter, errs.ErrToolPrecondition)
	}
	needs, err := projectAllCraftNeeds(st, chapter)
	if err != nil {
		return nil, err
	}
	indexIdentity := craftIndexIdentity(state)
	id := projectAllCraftReceiptID(chapter, actualGeneration, actualContext, indexIdentity, needs)
	if existing, loadErr := st.RAG.LoadCraftRecallReceipt(id); loadErr != nil {
		return nil, loadErr
	} else if projectAllCraftReceiptMatches(existing, chapter, actualGeneration, actualContext, indexIdentity, needs) {
		if auditErr := ensureRewriteCraftReceiptAudit(st, *existing); auditErr != nil {
			return nil, auditErr
		}
		return existing, nil
	} else if existing != nil {
		return nil, fmt.Errorf("project-all craft receipt id collision or immutable payload drift for %s", id)
	}

	receipt := domain.CraftRecallReceipt{
		Version:               rewriteCraftReceiptVersion,
		ID:                    id,
		Chapter:               chapter,
		Stage:                 domain.ProjectAllCraftReceiptStage,
		GenerationID:          actualGeneration,
		PlanningContextDigest: actualContext,
		IndexIdentity:         indexIdentity,
		Enforcement:           true,
		CreatedAt:             time.Now().UTC().Format(time.RFC3339Nano),
	}
	if state != nil {
		receipt.IndexUpdatedAt = state.UpdatedAt
	}
	for _, need := range needs {
		attempt := domain.CraftRecallReceiptAttempt{Need: need, NoMaterial: true}
		if state != nil && len(state.Chunks) > 0 {
			result := rag.NewCraftCatalog(state.Chunks).RecallWithOptions(
				rag.CraftDesignField(need.Field),
				need.Topic,
				3,
				rag.CraftRecallOptions{Stage: rag.StagePlan, RequireRelevant: true, SafeRewrite: true},
			)
			for _, hit := range result.Hits {
				chunk := rag.RehashChunk(hit.Chunk)
				attempt.Hits = append(attempt.Hits, domain.CraftRecallReceiptHit{
					Ref:         craftReceiptHitRef(id, chunk),
					ChunkID:     chunk.ID,
					ChunkHash:   chunk.Hash,
					SourcePath:  chunk.SourcePath,
					SourceKind:  chunk.SourceKind,
					Facet:       chunk.Facet,
					Summary:     compactCraftSummary(chunk.Summary),
					Score:       hit.Score,
					UsageStages: craftReceiptUsageStages(chunk),
				})
			}
			attempt.NoMaterial = len(attempt.Hits) == 0
			attempt.FilteredCount = result.FilteredCount
			attempt.FilteredReason = result.FilteredReason
		}
		receipt.Attempts = append(receipt.Attempts, attempt)
	}
	receipt.PayloadSHA256 = craftReceiptPayloadHash(receipt)
	if err := domain.ValidateProjectAllCraftRecallReceipt(receipt); err != nil {
		return nil, err
	}
	if err := st.RAG.SaveCraftRecallReceipt(receipt); err != nil {
		return nil, fmt.Errorf("save project-all craft recall receipt: %w", err)
	}
	if err := ensureRewriteCraftReceiptAudit(st, receipt); err != nil {
		return nil, fmt.Errorf("append project-all craft recall audit: %w", err)
	}
	return &receipt, nil
}

func EnsureProjectAllCraftReceiptForCurrentContext(
	st *store.Store,
	chapter int,
) (*domain.CraftRecallReceipt, error) {
	generationID, planningContextDigest, active, err := projectAllCraftReceiptInputs(st, chapter)
	if err != nil {
		return nil, err
	}
	if !active {
		return nil, fmt.Errorf("project-all craft receipt requires an active project-all execution lease: %w", errs.ErrToolPrecondition)
	}
	return EnsureProjectAllCraftReceipt(st, chapter, generationID, planningContextDigest)
}

func projectAllCraftReceiptID(
	chapter int,
	generationID string,
	planningContextDigest string,
	indexIdentity string,
	needs []domain.CraftRecallNeed,
) string {
	payload, _ := json.Marshal(struct {
		Chapter               int
		GenerationID          string
		PlanningContextDigest string
		IndexIdentity         string
		Needs                 []domain.CraftRecallNeed
	}{chapter, generationID, planningContextDigest, indexIdentity, needs})
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:12])
}

func projectAllCraftReceiptMatches(
	receipt *domain.CraftRecallReceipt,
	chapter int,
	generationID string,
	planningContextDigest string,
	indexIdentity string,
	needs []domain.CraftRecallNeed,
) bool {
	return receipt != nil && domain.ValidateProjectAllCraftRecallReceipt(*receipt) == nil &&
		receipt.Chapter == chapter && receipt.GenerationID == strings.TrimSpace(generationID) &&
		receipt.PlanningContextDigest == strings.TrimSpace(planningContextDigest) &&
		receipt.IndexIdentity == indexIdentity && craftReceiptNeedsMatch(receipt.Attempts, needs) &&
		receipt.ID == projectAllCraftReceiptID(chapter, generationID, planningContextDigest, indexIdentity, needs)
}

func ensureRewriteCraftReceipt(st *store.Store, chapter int, preferredID string) (*domain.CraftRecallReceipt, error) {
	if st == nil || chapter <= 0 {
		return nil, nil
	}
	if generationID, planningContextDigest, active, err := projectAllCraftReceiptInputs(st, chapter); err != nil {
		return nil, err
	} else if active {
		return EnsureProjectAllCraftReceipt(st, chapter, generationID, planningContextDigest)
	}
	source, _, _, err := loadChapterRewriteSource(st, chapter)
	if err != nil || source == nil {
		return nil, err
	}
	generationID := ""
	if progress, loadErr := st.Progress.Load(); loadErr == nil && progress != nil {
		generationID = strings.TrimSpace(progress.GenerationID)
	}
	needs := deriveRewriteCraftNeeds(st, chapter)
	if len(needs) == 0 {
		return nil, nil
	}
	state, loadErr := st.RAG.LoadIndexStateReadOnly()
	if loadErr != nil {
		return nil, fmt.Errorf("load craft RAG index: %w", loadErr)
	}
	indexIdentity := craftIndexIdentity(state)
	expectedID := rewriteCraftReceiptID(chapter, generationID, source, indexIdentity, needs)
	if preferredID != "" {
		if existing, loadErr := st.RAG.LoadCraftRecallReceipt(preferredID); loadErr != nil {
			return nil, loadErr
		} else if preferredID == expectedID && craftReceiptMatchesRewrite(existing, chapter, generationID, source, indexIdentity, needs) {
			if auditErr := ensureRewriteCraftReceiptAudit(st, *existing); auditErr != nil {
				return nil, auditErr
			}
			return existing, nil
		}
	}
	id := expectedID
	if existing, loadErr := st.RAG.LoadCraftRecallReceipt(id); loadErr != nil {
		return nil, loadErr
	} else if craftReceiptMatchesRewrite(existing, chapter, generationID, source, indexIdentity, needs) {
		if auditErr := ensureRewriteCraftReceiptAudit(st, *existing); auditErr != nil {
			return nil, auditErr
		}
		return existing, nil
	}

	receipt := domain.CraftRecallReceipt{
		Version:            rewriteCraftReceiptVersion,
		ID:                 id,
		Chapter:            chapter,
		Stage:              rewriteCraftStagePlan,
		GenerationID:       generationID,
		RewriteBodyPath:    source.BodyPath,
		RewriteBodySHA256:  source.BodySHA256,
		RewriteBriefPath:   source.BriefPath,
		RewriteBriefSHA256: source.BriefSHA256,
		IndexIdentity:      indexIdentity,
		Enforcement:        true,
		CreatedAt:          time.Now().Format(time.RFC3339),
	}
	if state != nil {
		receipt.IndexUpdatedAt = state.UpdatedAt
	}
	for _, need := range needs {
		attempt := domain.CraftRecallReceiptAttempt{Need: need, NoMaterial: true}
		if state != nil && len(state.Chunks) > 0 {
			result := rag.NewCraftCatalog(state.Chunks).RecallWithOptions(
				rag.CraftDesignField(need.Field),
				need.Topic,
				3,
				rag.CraftRecallOptions{Stage: rag.StagePlan, RequireRelevant: true, SafeRewrite: true},
			)
			for _, hit := range result.Hits {
				attempt.Hits = append(attempt.Hits, domain.CraftRecallReceiptHit{
					Ref:         craftReceiptHitRef(id, hit.Chunk),
					ChunkID:     hit.Chunk.ID,
					ChunkHash:   hit.Chunk.Hash,
					SourcePath:  hit.Chunk.SourcePath,
					SourceKind:  hit.Chunk.SourceKind,
					Facet:       hit.Chunk.Facet,
					Summary:     compactCraftSummary(hit.Chunk.Summary),
					Score:       hit.Score,
					UsageStages: craftReceiptUsageStages(hit.Chunk),
				})
			}
			attempt.NoMaterial = len(attempt.Hits) == 0
			attempt.FilteredCount = result.FilteredCount
			attempt.FilteredReason = result.FilteredReason
		} else {
			attempt.FilteredCount = 1
			attempt.FilteredReason = map[string]int{"missing_or_empty_index": 1}
		}
		receipt.Attempts = append(receipt.Attempts, attempt)
	}
	receipt.PayloadSHA256 = craftReceiptPayloadHash(receipt)
	if err := st.RAG.SaveCraftRecallReceipt(receipt); err != nil {
		return nil, fmt.Errorf("save craft recall receipt: %w", err)
	}
	if err := ensureRewriteCraftReceiptAudit(st, receipt); err != nil {
		return nil, fmt.Errorf("append craft recall audit: %w", err)
	}
	return &receipt, nil
}

func ensureRewriteCraftReceiptAudit(st *store.Store, receipt domain.CraftRecallReceipt) error {
	if st == nil {
		return fmt.Errorf("craft recall audit store is nil")
	}
	return st.RAG.AppendCraftRecallLogOnce(receipt.ID, craftReceiptAuditEntry(receipt))
}

func craftReceiptMatchesRewrite(
	receipt *domain.CraftRecallReceipt,
	chapter int,
	generationID string,
	source *domain.ChapterRewriteSource,
	indexIdentity string,
	needs []domain.CraftRecallNeed,
) bool {
	return receipt != nil && source != nil && receipt.Version == rewriteCraftReceiptVersion &&
		receipt.Enforcement && receipt.Chapter == chapter && receipt.Stage == rewriteCraftStagePlan &&
		strings.TrimSpace(receipt.GenerationID) == strings.TrimSpace(generationID) &&
		strings.TrimSpace(receipt.PlanningContextDigest) == "" &&
		receipt.RewriteBodyPath == source.BodyPath && receipt.RewriteBodySHA256 == source.BodySHA256 &&
		receipt.RewriteBriefPath == source.BriefPath && receipt.RewriteBriefSHA256 == source.BriefSHA256 &&
		receipt.IndexIdentity == indexIdentity && craftReceiptNeedsMatch(receipt.Attempts, needs) &&
		receipt.ID == rewriteCraftReceiptID(chapter, generationID, source, indexIdentity, needs) &&
		receipt.PayloadSHA256 != "" && receipt.PayloadSHA256 == craftReceiptPayloadHash(*receipt)
}

func rewriteCraftReceiptIsCurrent(st *store.Store, chapter int, receipt *domain.CraftRecallReceipt) (bool, error) {
	if st == nil || chapter <= 0 || receipt == nil {
		return false, nil
	}
	source, _, _, err := loadChapterRewriteSource(st, chapter)
	if err != nil || source == nil {
		return false, err
	}
	generationID := ""
	if progress, loadErr := st.Progress.Load(); loadErr != nil {
		return false, loadErr
	} else if progress != nil {
		generationID = strings.TrimSpace(progress.GenerationID)
	}
	state, err := st.RAG.LoadIndexStateReadOnly()
	if err != nil {
		return false, err
	}
	return craftReceiptMatchesRewrite(
		receipt, chapter, generationID, source, craftIndexIdentity(state), deriveRewriteCraftNeeds(st, chapter),
	), nil
}

func craftReceiptNeedsMatch(attempts []domain.CraftRecallReceiptAttempt, needs []domain.CraftRecallNeed) bool {
	if len(attempts) != len(needs) {
		return false
	}
	for i, need := range needs {
		got := attempts[i].Need
		if got.ID != need.ID || got.Field != need.Field || got.Topic != need.Topic || strings.Join(got.TriggerRefs, "\x00") != strings.Join(need.TriggerRefs, "\x00") {
			return false
		}
	}
	return true
}

func craftReceiptPayloadHash(receipt domain.CraftRecallReceipt) string {
	return domain.ComputeCraftRecallReceiptPayloadSHA256(receipt)
}

const rewriteCraftSafeCorpusPolicy = "rewrite-craft-safe-corpus-v5-field-aligned-primary-diversity"

func craftIndexIdentity(state *domain.RAGIndexState) string {
	if state == nil {
		return "missing"
	}
	// SanitizedDigest is a cache marker for the command-side contamination
	// policy.  Incremental RAG writes can legitimately leave that marker stale,
	// so it is not a safe receipt identity.  Bind instead to fresh semantic
	// hashes of the exact method-only corpus automatic rewrites may consume.
	// RehashChunk deliberately ignores a persisted (and potentially stale)
	// chunk.Hash and includes text, summary and routing metadata.
	hashes := make([]string, 0)
	for _, chunk := range state.Chunks {
		if !rag.IsSafeRewriteMethodChunk(chunk) {
			continue
		}
		hashes = append(hashes, rag.RehashChunk(chunk).Hash)
	}
	sort.Strings(hashes)
	h := sha256.New()
	// The policy marker is part of the identity because routing changes can
	// alter selected hits even when the indexed bytes are unchanged. v5 binds
	// receipts to structured, summary-only cards with field-aligned primary
	// operations, so secondary tags cannot smuggle a scene/hook card into a
	// dialogue need or let near-duplicates monopolize Top-N.
	fmt.Fprintf(h, "policy=%s\nschema=%d\nchunks=%d\n", rewriteCraftSafeCorpusPolicy, state.SchemaVersion, len(hashes))
	for _, hash := range hashes {
		fmt.Fprintf(h, "%s\n", hash)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func rewriteCraftReceiptID(chapter int, generationID string, source *domain.ChapterRewriteSource, indexIdentity string, needs []domain.CraftRecallNeed) string {
	payload, _ := json.Marshal(struct {
		Chapter      int
		GenerationID string
		BodySHA      string
		BriefSHA     string
		Index        string
		Needs        []domain.CraftRecallNeed
	}{chapter, generationID, source.BodySHA256, source.BriefSHA256, indexIdentity, needs})
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:12])
}

func craftReceiptHitRef(receiptID string, chunk domain.RAGChunk) string {
	hash := strings.TrimSpace(chunk.Hash)
	if hash == "" {
		sum := sha256.Sum256([]byte(chunk.ID + "\n" + chunk.SourcePath))
		hash = hex.EncodeToString(sum[:8])
	}
	return fmt.Sprintf("%s%s#chunk=%s#hash=%s", craftReceiptTokenPrefix, receiptID, chunk.ID, hash)
}

func craftReceiptSourceToken(id string) string {
	return craftReceiptTokenPrefix + strings.TrimSpace(id)
}

func craftReceiptIDFromSources(sources []string) string {
	for _, source := range sources {
		if strings.HasPrefix(strings.TrimSpace(source), craftReceiptTokenPrefix) {
			id := strings.TrimPrefix(strings.TrimSpace(source), craftReceiptTokenPrefix)
			if idx := strings.Index(id, "#"); idx >= 0 {
				id = id[:idx]
			}
			return strings.TrimSpace(id)
		}
	}
	return ""
}

func compactCraftSummary(summary string) string {
	value := strings.TrimSpace(summary)
	runes := []rune(value)
	if len(runes) > 320 {
		value = string(runes[:320]) + "…"
	}
	return value
}

func craftReceiptUsageStages(chunk domain.RAGChunk) []string {
	if chunk.Metadata != nil {
		if raw, ok := chunk.Metadata["usage_stage"].(string); ok {
			var stages []string
			for _, item := range strings.Split(raw, ",") {
				if item = strings.TrimSpace(item); item != "" {
					stages = append(stages, item)
				}
			}
			return stages
		}
	}
	return []string{rag.StagePlan}
}

func craftReceiptAuditEntry(receipt domain.CraftRecallReceipt) map[string]any {
	attempts := make([]map[string]any, 0, len(receipt.Attempts))
	for _, attempt := range receipt.Attempts {
		hits := make([]map[string]any, 0, len(attempt.Hits))
		for _, hit := range attempt.Hits {
			hits = append(hits, map[string]any{
				"ref": hit.Ref, "chunk_id": hit.ChunkID, "chunk_hash": hit.ChunkHash,
				"source_path": hit.SourcePath, "source_kind": hit.SourceKind,
				"facet": hit.Facet, "score": hit.Score,
			})
		}
		attempts = append(attempts, map[string]any{
			"need": attempt.Need, "no_material": attempt.NoMaterial, "hits": hits,
			"filtered_count": attempt.FilteredCount, "filtered_reason": attempt.FilteredReason,
		})
	}
	return map[string]any{
		"at": receipt.CreatedAt, "receipt_id": receipt.ID, "stage": receipt.Stage,
		"chapter": receipt.Chapter, "generation_id": receipt.GenerationID,
		"planning_context_digest": receipt.PlanningContextDigest,
		"rewrite_body_sha256":     receipt.RewriteBodySHA256,
		"rewrite_brief_sha256":    receipt.RewriteBriefSHA256,
		"index_identity":          receipt.IndexIdentity, "payload_sha256": receipt.PayloadSHA256,
		"automatic": true, "attempts": attempts,
	}
}

func craftReceiptContext(receipt *domain.CraftRecallReceipt) map[string]any {
	if receipt == nil {
		return nil
	}
	policy := "只选择与本章返工问题直接相关的手法；每个有 hits 的 need.id 在 external_reference_plan 恰好写一行，query_or_need 原样写该 need.id，同 need 采用的精确 hit.ref 全部合入 source_refs。calibration_reference/craft_technique 命中统一写 source_type=craft_recall，benchmark_reference 命中写 source_type=benchmark_craft_recall，禁止把 hit.source_kind 当 source_type。必须填写场景化 usable_details、transformation_rule 与 do_not_use；只迁移写法，不复制素材情节、人名、地名或专有设定。"
	if receipt.Stage == domain.ProjectAllCraftReceiptStage {
		policy = "这是 project-all 当前章的强制 planning receipt。每个有 hits 的 need.id 必须在 external_reference_plan 恰好写一行，query_or_need 必须逐字等于 need.id，至少采用一个属于该 need 的精确 hit.ref。必须填写本章化 usable_details、transformation_rule 与 do_not_use；no_material need 不得伪造引用。只迁移写法，不复制素材情节、人名、地名或专有设定。"
	}
	return map[string]any{
		"receipt_id":   receipt.ID,
		"stage":        receipt.Stage,
		"source_token": craftReceiptSourceToken(receipt.ID),
		"binding": map[string]any{
			"chapter": receipt.Chapter, "generation_id": receipt.GenerationID,
			"planning_context_digest": receipt.PlanningContextDigest,
			"rewrite_body_sha256":     receipt.RewriteBodySHA256,
			"rewrite_brief_sha256":    receipt.RewriteBriefSHA256,
			"index_identity":          receipt.IndexIdentity,
		},
		"attempts":           receipt.Attempts,
		"consumption_policy": policy,
	}
}

// validateRewriteCraftFinalization is stricter than the read-side validator:
// a plan being finalized now is not a historical artifact. If current review
// evidence derives craft needs, the new plan must go through the staged
// preflight and carry its receipt, including an explicit no_material result.
func validateRewriteCraftFinalization(st *store.Store, plan domain.ChapterPlan) error {
	if craftReceiptIDFromSources(plan.CausalSimulation.ContextSources) == "" {
		receipt, err := ensureRewriteCraftReceipt(st, plan.Chapter, "")
		if err != nil {
			return err
		}
		if receipt != nil {
			pack, _ := json.Marshal(craftReceiptContext(receipt))
			return fmt.Errorf("第 %d 章是当前新返工计划，检测到 rewrite craft needs 后不能以无收据的 plan_chapter 单发路径绕过 RAG；请先调用 plan_structure，再按 rewrite_craft_pack 补 plan_details。rewrite_craft_pack=%s: %w",
				plan.Chapter, pack, errs.ErrToolPrecondition)
		}
	}
	return validateRewriteCraftConsumption(st, plan)
}

// ValidateRewriteCraftPlanCurrent is the read-side readiness check shared by
// the Host router, draft context and render-only validation. Completed
// historical plans without a receipt remain compatible, but once their chapter
// enters PendingRewrites and current review evidence derives craft needs they
// must be replanned through the automatic receipt preflight.
func ValidateRewriteCraftPlanCurrent(st *store.Store, plan domain.ChapterPlan) error {
	return validateRewriteCraftConsumption(st, plan)
}

func validateRewriteCraftConsumption(st *store.Store, plan domain.ChapterPlan) error {
	id := craftReceiptIDFromSources(plan.CausalSimulation.ContextSources)
	if id == "" {
		if activeRewriteCraftReceiptRequired(st, plan.Chapter) {
			return fmt.Errorf("第 %d 章正在返工且当前审阅已派生 rewrite craft needs，旧计划没有自动 craft receipt，不能直接复用或 render-only；请重新推演计划: %w",
				plan.Chapter, errs.ErrToolPrecondition)
		}
		return nil // completed historical plans are not retroactively invalidated.
	}
	receipt, err := st.RAG.LoadCraftRecallReceipt(id)
	if err != nil {
		return fmt.Errorf("load rewrite craft receipt %s: %w", id, err)
	}
	if receipt == nil || !receipt.Enforcement {
		return fmt.Errorf("第 %d 章引用的 craft receipt %s 不存在或不是当前自动门禁收据: %w", plan.Chapter, id, errs.ErrToolPrecondition)
	}
	if receipt.Stage == domain.ProjectAllCraftReceiptStage {
		return ValidateProjectAllCraftPlanCurrent(st, plan, receipt)
	}
	source, _, _, err := loadChapterRewriteSource(st, plan.Chapter)
	if err != nil {
		return err
	}
	generationID := ""
	if progress, loadErr := st.Progress.Load(); loadErr == nil && progress != nil {
		generationID = progress.GenerationID
	}
	state, loadErr := st.RAG.LoadIndexStateReadOnly()
	if loadErr != nil {
		return fmt.Errorf("load current craft RAG index: %w", loadErr)
	}
	currentIndexIdentity := craftIndexIdentity(state)
	currentNeeds := deriveRewriteCraftNeeds(st, plan.Chapter)
	if !craftReceiptMatchesRewrite(receipt, plan.Chapter, generationID, source, currentIndexIdentity, currentNeeds) {
		return fmt.Errorf("第 %d 章 craft receipt %s 与当前 generation/rewrite body/brief/index/triggers 或 receipt payload 不匹配，必须重新推演: %w", plan.Chapter, id, errs.ErrToolPrecondition)
	}

	needByRef := map[string]map[string]bool{}
	required := map[string]bool{}
	hitByRef := map[string]domain.CraftRecallReceiptHit{}
	for _, attempt := range receipt.Attempts {
		if len(attempt.Hits) > 0 {
			required[attempt.Need.ID] = true
		}
		for _, hit := range attempt.Hits {
			if needByRef[hit.Ref] == nil {
				needByRef[hit.Ref] = map[string]bool{}
			}
			needByRef[hit.Ref][attempt.Need.ID] = true
			hitByRef[hit.Ref] = hit
		}
	}
	if len(required) == 0 {
		return nil // explicit no_material remains auditable but never forces weak material into the plan.
	}
	consumed := map[string]bool{}
	for _, ref := range plan.CausalSimulation.ExternalRefs {
		sourceType := strings.ToLower(strings.TrimSpace(ref.SourceType))
		if sourceType != craftSourceType && sourceType != benchmarkCraftSourceType {
			continue
		}
		if len(ref.UsableDetails) == 0 || strings.TrimSpace(ref.TransformationRule) == "" || len(ref.DoNotUse) == 0 {
			return fmt.Errorf("第 %d 章 receipt-backed external_reference_plan 必须填写 usable_details/transformation_rule/do_not_use: %w", plan.Chapter, errs.ErrToolPrecondition)
		}
		var declared []string
		for candidate := range required {
			if strings.Contains(ref.QueryOrNeed, candidate) {
				declared = append(declared, candidate)
			}
		}
		sort.Strings(declared)
		if len(declared) != 1 {
			return fmt.Errorf("第 %d 章 external_reference_plan.query_or_need 必须且只能声明一个当前 need id，当前识别为 %v: %w",
				plan.Chapter, declared, errs.ErrToolPrecondition)
		}
		declaredNeed := declared[0]
		// A declared need is consumed only when the plan carries at least one
		// exact receipt hit for that same need. Without this guard an empty
		// source_refs array could pass the loop below, mark the need consumed,
		// and then disappear from the Drafter projection.
		if len(ref.SourceRefs) == 0 {
			return fmt.Errorf("第 %d 章 external_reference_plan need=%q 的 source_refs 至少必须包含一个属于该 need 的 receipt hit.ref: %w",
				plan.Chapter, declaredNeed, errs.ErrToolPrecondition)
		}
		validReceiptRefs := 0
		for _, sourceRef := range ref.SourceRefs {
			allowedNeeds, ok := needByRef[sourceRef]
			if !ok {
				return fmt.Errorf("第 %d 章 external_reference_plan 引用了 receipt %s 未授权的 source_ref=%q: %w", plan.Chapter, id, sourceRef, errs.ErrToolPrecondition)
			}
			if !allowedNeeds[declaredNeed] {
				return fmt.Errorf("第 %d 章 external_reference_plan need=%q 引用了只属于 needs=%v 的 source_ref=%q: %w",
					plan.Chapter, declaredNeed, sortedBoolMapKeys(allowedNeeds), sourceRef, errs.ErrToolPrecondition)
			}
			hit := hitByRef[sourceRef]
			expectedSourceType := craftSourceType
			if strings.EqualFold(hit.SourceKind, rag.BenchmarkSourceKind) {
				expectedSourceType = benchmarkCraftSourceType
			}
			if sourceType != expectedSourceType {
				return fmt.Errorf("第 %d 章 receipt hit %s 的 source_type 必须为 %s，当前为 %s: %w", plan.Chapter, sourceRef, expectedSourceType, sourceType, errs.ErrToolPrecondition)
			}
			if craftReferenceCopiesSummary(ref, hit) {
				return fmt.Errorf("第 %d 章 external_reference_plan 复制了 benchmark/craft 摘要原句；只能写成本章场景化手法: %w", plan.Chapter, errs.ErrToolPrecondition)
			}
			validReceiptRefs++
		}
		if validReceiptRefs == 0 {
			return fmt.Errorf("第 %d 章 external_reference_plan need=%q 未提供属于该 need 的合法 receipt hit.ref: %w",
				plan.Chapter, declaredNeed, errs.ErrToolPrecondition)
		}
		if consumed[declaredNeed] {
			return fmt.Errorf("第 %d 章每个 rewrite craft need 只能对应一条 external_reference_plan；need=%q 重复出现，请把同类 hit 合并进同一条 source_refs: %w",
				plan.Chapter, declaredNeed, errs.ErrToolPrecondition)
		}
		consumed[declaredNeed] = true
	}
	var missing []string
	for needID := range required {
		if !consumed[needID] {
			missing = append(missing, needID)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		pack, _ := json.Marshal(craftReceiptContext(receipt))
		return fmt.Errorf("第 %d 章当前 rewrite craft receipt 尚未进入计划，缺少 needs=%s。请只补 external_reference_plan，精确使用 pack 中 hit.ref 并写成本章场景化手法；不要重新调用 craft_recall。craft_pack=%s: %w",
			plan.Chapter, strings.Join(missing, ","), pack, errs.ErrToolPrecondition)
	}
	return nil
}

// ValidateProjectAllCraftPlanCurrent is the stage-aware project-all gate used
// after Planner finalization. While the isolated execution lease is active it
// also proves the receipt was created from the exact current index and
// projected planning context; outside that lease (promote/render) it validates
// only the already-sealed content-addressed receipt and transformed plan refs.
func ValidateProjectAllCraftPlanCurrent(
	st *store.Store,
	plan domain.ChapterPlan,
	receipt *domain.CraftRecallReceipt,
) error {
	if st == nil || receipt == nil {
		return fmt.Errorf("第 %d 章 project-all craft receipt 缺失: %w", plan.Chapter, errs.ErrToolPrecondition)
	}
	if err := domain.ValidateProjectAllCraftRecallReceipt(*receipt); err != nil {
		return fmt.Errorf("第 %d 章 project-all craft receipt 非法: %w", plan.Chapter, err)
	}
	if receipt.Chapter != plan.Chapter {
		return fmt.Errorf("第 %d 章 project-all craft receipt chapter=%d 不匹配: %w",
			plan.Chapter, receipt.Chapter, errs.ErrToolPrecondition)
	}
	if generationID, contextDigest, active, err := projectAllCraftReceiptInputs(st, plan.Chapter); err != nil {
		return err
	} else if active {
		state, loadErr := st.RAG.LoadIndexStateReadOnly()
		if loadErr != nil {
			return fmt.Errorf("load current project-all craft RAG index: %w", loadErr)
		}
		if state == nil || len(state.Chunks) == 0 {
			return fmt.Errorf("第 %d 章 project-all craft index missing or empty: %w", plan.Chapter, errs.ErrToolPrecondition)
		}
		needs, needsErr := projectAllCraftNeeds(st, plan.Chapter)
		if needsErr != nil {
			return needsErr
		}
		if !projectAllCraftReceiptMatches(
			receipt,
			plan.Chapter,
			generationID,
			contextDigest,
			craftIndexIdentity(state),
			needs,
		) {
			return fmt.Errorf("第 %d 章 project-all craft receipt 与当前 generation/planning-context/index 不匹配: %w",
				plan.Chapter, errs.ErrToolPrecondition)
		}
	}
	if err := domain.ValidateProjectAllCraftPlanConsumptionV2(plan, *receipt); err != nil {
		return fmt.Errorf("第 %d 章 project-all craft receipt 未完整进入 external_reference_plan: %w",
			plan.Chapter, err)
	}
	return nil
}

// MaterializeProjectAllCraftPlanConsumption fills only missing hit-bearing
// project-all craft needs. The receipt remains the authority for exact refs;
// the deterministic rows translate its safe method cards into plan-level
// scene craft without copying source prose or inventing story facts. Existing
// model-authored rows always win.
func MaterializeProjectAllCraftPlanConsumption(
	plan *domain.ChapterPlan,
	receipt *domain.CraftRecallReceipt,
) bool {
	if plan == nil || receipt == nil || receipt.Stage != domain.ProjectAllCraftReceiptStage {
		return false
	}
	consumed := make(map[string]struct{}, len(plan.CausalSimulation.ExternalRefs))
	for _, row := range plan.CausalSimulation.ExternalRefs {
		if need := strings.TrimSpace(row.QueryOrNeed); need != "" {
			consumed[need] = struct{}{}
		}
	}
	changed := false
	for _, attempt := range receipt.Attempts {
		needID := strings.TrimSpace(attempt.Need.ID)
		if needID == "" || len(attempt.Hits) == 0 {
			continue
		}
		if _, exists := consumed[needID]; exists {
			continue
		}
		refs := make([]string, 0, len(attempt.Hits))
		sourceType := craftSourceType
		allBenchmark := true
		for _, hit := range attempt.Hits {
			refs = appendUniqueString(refs, hit.Ref)
			if !strings.EqualFold(strings.TrimSpace(hit.SourceKind), rag.BenchmarkSourceKind) {
				allBenchmark = false
			}
		}
		if allBenchmark {
			sourceType = benchmarkCraftSourceType
		}
		usable := []string{
			"按选择成本分层释放本章既定信息，并让每场退出后果约束下一场选择。",
			"用观察—判断—选择—结果链呈现主角主观因果，不由旁白代替人物作结论。",
		}
		if strings.Contains(strings.ToLower(attempt.Need.Field), "scene") {
			usable = []string{
				"用本章已确定的空间边界限制可见范围、接触对象与退出路径。",
				"每轮只增加本章既定的真实代价，并在关键选择前减速取证、结果后压缩过场。",
			}
		}
		plan.CausalSimulation.ExternalRefs = append(plan.CausalSimulation.ExternalRefs, domain.ExternalReferencePlan{
			QueryOrNeed:          needID,
			SourceType:           sourceType,
			SourceRefs:           refs,
			RetrievedAt:          receipt.CreatedAt,
			FreshnessRequirement: "稳定写作方法；精确绑定当前 project-all craft receipt",
			UsableDetails:        usable,
			TransformationRule:   "只把 receipt 方法转化到本章既有 required_beats 与 scene_units；不复制来源人物、地名、情节或原句，不新增故事事实。",
			DoNotUse: []string{
				"不把方法卡写成正文说明或检查清单。",
				"不复制来源中的人物、情节、地名或措辞。",
				"不为了展示技巧增加无因果场景。",
			},
		})
		consumed[needID] = struct{}{}
		changed = true
	}
	return changed
}

// RepairProjectAllCraftPlanCurrent repairs a finalized plan whose last
// plan_details patch replaced earlier craft rows, then writes a fresh causal
// checkpoint so the artifact remains content-addressed and routable.
func RepairProjectAllCraftPlanCurrent(
	st *store.Store,
	plan *domain.ChapterPlan,
	receipt *domain.CraftRecallReceipt,
) (bool, error) {
	if st == nil || plan == nil || receipt == nil {
		return false, nil
	}
	if !MaterializeProjectAllCraftPlanConsumption(plan, receipt) {
		return false, nil
	}
	if err := ValidateProjectAllCraftPlanCurrent(st, *plan, receipt); err != nil {
		return false, err
	}
	if err := st.Drafts.SaveChapterPlan(*plan); err != nil {
		return false, fmt.Errorf("save repaired project-all chapter plan: %w", err)
	}
	if _, err := st.Checkpoints.AppendArtifactLatestAcross(
		domain.ChapterScope(plan.Chapter),
		"plan",
		fmt.Sprintf("drafts/%02d.plan.json", plan.Chapter),
		"plan", "chapter_world_simulation", "review", "draft-structural-block",
	); err != nil {
		return false, fmt.Errorf("checkpoint repaired project-all chapter plan: %w", err)
	}
	return true, nil
}

func activeRewriteCraftReceiptRequired(st *store.Store, chapter int) bool {
	if st == nil || chapter <= 0 {
		return false
	}
	progress, err := st.Progress.Load()
	if err != nil || progress == nil {
		return false
	}
	pending := false
	for _, candidate := range progress.PendingRewrites {
		if candidate == chapter {
			pending = true
			break
		}
	}
	return pending && len(deriveRewriteCraftNeeds(st, chapter)) > 0
}

func sortedBoolMapKeys(values map[string]bool) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func craftReferenceCopiesSummary(ref domain.ExternalReferencePlan, hit domain.CraftRecallReceiptHit) bool {
	source := []rune(strings.TrimSpace(hit.Summary))
	if len(source) < 24 {
		return false
	}
	projected := []string{ref.QueryOrNeed, ref.TransformationRule}
	projected = append(projected, ref.UsableDetails...)
	projected = append(projected, ref.DoNotUse...)
	target := []rune(strings.Join(projected, "\n"))
	return longestCommonRuneRun(source, target, 24) >= 24
}

func longestCommonRuneRun(left, right []rune, stopAt int) int {
	if len(left) == 0 || len(right) == 0 {
		return 0
	}
	prev := make([]int, len(right)+1)
	best := 0
	for _, a := range left {
		curr := make([]int, len(right)+1)
		for j, b := range right {
			if a == b {
				curr[j+1] = prev[j] + 1
				if curr[j+1] > best {
					best = curr[j+1]
					if best >= stopAt {
						return best
					}
				}
			}
		}
		prev = curr
	}
	return best
}

func compactCraftMethodStrings(values []string, limit int) []string {
	var out []string
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if utf8.RuneCountInString(value) > 180 {
			value = string([]rune(value)[:180])
		}
		out = append(out, value)
		if len(out) >= limit {
			break
		}
	}
	return out
}
