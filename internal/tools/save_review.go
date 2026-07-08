package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/chenhongyang/novel-studio/internal/domain"
	editrules "github.com/chenhongyang/novel-studio/internal/editor/rules"
	"github.com/chenhongyang/novel-studio/internal/rag"
	"github.com/chenhongyang/novel-studio/internal/reviewreport"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/stylestat"
	"github.com/voocel/agentcore/schema"
)

// SaveReviewTool 保存 Editor 的审阅结果。
type SaveReviewTool struct {
	store           *store.Store
	ragEmbedder     rag.Embedder
	ragVectorWriter rag.VectorWriter
}

func NewSaveReviewTool(store *store.Store) *SaveReviewTool {
	return &SaveReviewTool{store: store}
}

func (t *SaveReviewTool) WithRAGEmbedder(embedder rag.Embedder) *SaveReviewTool {
	t.ragEmbedder = embedder
	return t
}

func (t *SaveReviewTool) WithRAGVectorWriter(writer rag.VectorWriter) *SaveReviewTool {
	t.ragVectorWriter = writer
	return t
}

func (t *SaveReviewTool) Name() string { return "save_review" }
func (t *SaveReviewTool) Description() string {
	return "保存审阅结果并更新流程状态。verdict 为 accept/polish/rewrite 之一。" +
		"工具内部执行评分卡、合同和 issue 严重度门禁（可能升级 verdict），直接更新 Progress 的 flow 和 pending_rewrites。" +
		"返回结构化事实：final_verdict / affected_chapters / escalation_reason / next_flow / next_chapter / chapter_progress / project_progress / evolution_report"
}
func (t *SaveReviewTool) Label() string { return "保存审阅" }

// 写工具（同时更新 reviews/ 与 Progress 的 PendingRewrites/Flow），禁止并发。
func (t *SaveReviewTool) ReadOnly(_ json.RawMessage) bool        { return false }
func (t *SaveReviewTool) ConcurrencySafe(_ json.RawMessage) bool { return false }

func (t *SaveReviewTool) Schema() map[string]any {
	issueSchema := schema.Object(
		schema.Property("type", schema.Enum("问题维度", "consistency", "character", "pacing", "continuity", "foreshadow", "hook", "aesthetic", "ai_voice_detection")).Required(),
		schema.Property("severity", schema.Enum("严重程度", "critical", "error", "warning")).Required(),
		schema.Property("description", schema.String("问题描述")).Required(),
		schema.Property("evidence", schema.String("证据：原文片段、具体情节或状态数据")).Required(),
		schema.Property("suggestion", schema.String("修改建议")),
	)
	dimensionSchema := schema.Object(
		schema.Property("dimension", schema.Enum("维度", "consistency", "character", "pacing", "continuity", "foreshadow", "hook", "aesthetic", "ai_voice_detection")).Required(),
		schema.Property("score", schema.Int("评分（0-100）")).Required(),
		schema.Property("verdict", schema.Enum("维度结论（可省略：系统按 score 自动推导，≥80 pass / ≥60 warning / <60 fail）", "pass", "warning", "fail")),
		schema.Property("comment", schema.String("该维度的简要结论；每个维度必填，aesthetic 必须引用原文或具体统计事实")).Required(),
	)
	return schema.Object(
		schema.Property("chapter", schema.Int("审阅的章节号（全局审阅填最新章节号）")).Required(),
		schema.Property("scope", schema.Enum("审阅范围", "chapter", "global", "arc")).Required(),
		schema.Property("dimensions", schema.Array("分维度评分（八个维度各一条）", dimensionSchema)).Required(),
		schema.Property("issues", schema.Array("发现的问题", issueSchema)).Required(),
		schema.Property("contract_status", schema.Enum("章节契约完成度", "met", "partial", "missed")),
		schema.Property("contract_misses", schema.Array("未完成或违背的 contract 条目", schema.String(""))),
		schema.Property("contract_notes", schema.String("对 contract 履行情况的简要说明")),
		schema.Property("verdict", schema.Enum("审阅结论", "accept", "polish", "rewrite")).Required(),
		schema.Property("summary", schema.String("审阅总结")).Required(),
		schema.Property("affected_chapters", schema.Array("需要重写或打磨的章节号列表（verdict 为 polish/rewrite 时必填）", schema.Int(""))),
	)
}

func (t *SaveReviewTool) Execute(_ context.Context, args json.RawMessage) (json.RawMessage, error) {
	var r domain.ReviewEntry
	if err := unmarshalToolArgs(args, &r); err != nil {
		return nil, fmt.Errorf("invalid args: %w", err)
	}
	if r.Chapter <= 0 {
		return nil, fmt.Errorf("chapter must be > 0")
	}
	// verdict 是 score 的纯函数（≥80 pass / ≥60 warning / <60 fail），由代码确定性推导——
	// 不让 LLM 重复提供再校验一致性。既消除冗余，也根除"score=85 却给 warning"这类自相矛盾的参数。
	for i := range r.Dimensions {
		r.Dimensions[i].Verdict = expectedDimensionVerdict(r.Dimensions[i].Score)
	}
	if err := validateReviewEntry(r); err != nil {
		return nil, err
	}

	// 评分卡/issue 门禁：最终 verdict 是模型 verdict 与确定性门禁的最严结果。
	// 不能只在模型给 accept 时升级；否则会出现 "verdict=polish 但关键维度 fail"
	// 或 "verdict=accept 但 issues 里有 critical" 仍被放行的低级绕门。
	finalVerdict := r.Verdict
	var escalationReasons []string
	applyGate := func(verdict, reason string) {
		if verdictRank(verdict) <= verdictRank(finalVerdict) {
			return
		}
		finalVerdict = verdict
		if reason != "" {
			escalationReasons = append(escalationReasons, reason)
		}
	}
	if verdict, reason := contractReviewGate(r); verdict != "" {
		applyGate(verdict, reason)
	}
	if verdict, reason := scorecardReviewGate(r.Dimensions); verdict != "" {
		applyGate(verdict, reason)
	}
	if verdict, reason := issueSeverityReviewGate(r.Issues); verdict != "" {
		applyGate(verdict, reason)
	}
	if verdict, reason := t.aiVoiceReviewGate(r); verdict != "" {
		applyGate(verdict, reason)
	}
	escalationReason := strings.Join(escalationReasons, "；")

	affected := r.AffectedChapters
	if finalVerdict == "rewrite" || finalVerdict == "polish" {
		if len(affected) == 0 && r.Chapter > 0 {
			affected = []int{r.Chapter}
		}
		if err := t.store.Progress.ValidatePendingRewrites(affected); err != nil {
			return nil, fmt.Errorf("validate pending rewrites: %w", err)
		}
	}

	r.Verdict = finalVerdict

	// 根据最终 verdict 更新 Progress。
	// 写失败必须早返回——后续会 append review checkpoint，若此处吞 err 会让 Coordinator
	// 看到 saved:true 但 Store 仍处于旧 Flow / 缺失 PendingRewrites 的中间态。
	progress, _ := t.store.Progress.Load()

	// 复审归档：覆盖前把上一轮章级审阅追加进 history，历史零丢失。
	// review_round = 历史轮数 + 1（本轮），随事实透出供 Coordinator/Editor 感知循环。
	reviewRound := 1
	if r.Scope == "chapter" {
		reviewRound = len(t.store.World.LoadReviewHistory(r.Chapter)) + 1
		if prev, prevErr := t.store.World.LoadReview(r.Chapter); prevErr == nil && prev != nil && prev.Scope == "chapter" {
			if err := t.store.World.ArchiveReviewHistory(*prev); err != nil {
				return nil, fmt.Errorf("archive review history: %w", err)
			}
			reviewRound++
		}
	}

	if err := t.store.World.SaveReview(r); err != nil {
		return nil, fmt.Errorf("save review: %w", err)
	}
	aiVoice, err := t.persistAIVoiceReview(r)
	if err != nil {
		return nil, fmt.Errorf("save ai voice review: %w", err)
	}
	if err := t.writeUnifiedChapterReview(r, aiVoice); err != nil {
		return nil, fmt.Errorf("save unified review: %w", err)
	}

	if finalVerdict == "rewrite" || finalVerdict == "polish" {
		flow := domain.FlowRewriting
		if finalVerdict == "polish" {
			flow = domain.FlowPolishing
		}
		if err := t.store.Progress.SetPendingRewrites(affected, r.Summary); err != nil {
			return nil, fmt.Errorf("set pending rewrites: %w", err)
		}
		if err := t.store.Progress.SetFlow(flow); err != nil {
			return nil, fmt.Errorf("set flow %s: %w", flow, err)
		}
	} else {
		if err := t.store.Progress.SetFlow(domain.FlowWriting); err != nil {
			return nil, fmt.Errorf("set flow writing: %w", err)
		}
	}

	// 读取更新后的 Progress 快照作为事实
	latest, _ := t.store.Progress.Load()
	nextFlow := string(domain.FlowWriting)
	nextChapter := 0
	if latest != nil {
		nextFlow = string(latest.Flow)
		nextChapter = latest.NextChapter()
	}

	writingFeedbackEntries, writingFeedbackFeatures, err := t.store.WritingAssets.ApplyReviewFeedback(r, finalVerdict, escalationReason)
	if err != nil {
		return nil, fmt.Errorf("sync writing feedback: %w", err)
	}
	writingFeedbackPath := ""
	if writingFeedbackEntries > 0 || writingFeedbackFeatures > 0 {
		writingFeedbackPath = "meta/writing_assets.md"
	}

	bookComplete := false
	manuscriptPath := ""
	if finalVerdict == "accept" {
		var completeErr error
		bookComplete, manuscriptPath, completeErr = t.completeBookIfReady(r, latest)
		if completeErr != nil {
			return nil, completeErr
		}
		if bookComplete {
			if latest, _ = t.store.Progress.Load(); latest != nil {
				nextFlow = string(latest.Flow)
				nextChapter = latest.NextChapter()
			}
		}
	}

	// Task 082：书级 AI 味统计——accept 后每 5 章用已定稿正文刷新一次
	// （meta/book_stylestat.json），供 diag 与 review-summary 消费。纯观测。
	bookStatEvery := 5
	if t.isMidVolume(r.Chapter) {
		bookStatEvery = 3 // Task 075：卷中段刷新频率减半
	}
	if finalVerdict == "accept" && r.Scope == "chapter" && r.Chapter >= 2 && r.Chapter%bookStatEvery == 0 {
		var chapters []stylestat.ChapterText
		for ch := 1; ch <= r.Chapter; ch++ {
			if text, err := t.store.Drafts.LoadChapterText(ch); err == nil && strings.TrimSpace(text) != "" {
				chapters = append(chapters, stylestat.ChapterText{Chapter: ch, Text: text})
			}
		}
		if len(chapters) >= 2 {
			stats := map[string]any{"report": stylestat.BookReport(chapters, 3)}
			// Task 079：风格漂移——滚动基线（前 10 章）vs 最近 5 章。
			if len(chapters) >= 8 {
				base := chapters[:len(chapters)-5]
				if len(base) > 10 {
					base = base[len(base)-10:]
				}
				stats["drift_distance"] = stylestat.DriftReport(base, chapters[len(chapters)-5:])
			}
			if rep, ok := stats["report"].(stylestat.BookStats); ok {
				stats["opening_homogeneity"] = rep.OpeningHomogeneity
				stats["ending_homogeneity"] = rep.EndingHomogeneity
				stats["pet_phrases"] = rep.PetPhrases
			}
			if err := t.store.Methodology.SaveBookStylestat(stats); err != nil {
				slog.Warn("书级统计落盘失败，跳过", "module", "review", "err", err)
			}
		}
	}

	chapterProgressPath := ""
	var nextPlan *domain.NextChapterProgressPlan
	if finalVerdict == "accept" && r.Scope == "chapter" {
		ledger, err := t.store.RefreshChapterProgressLedger(r.Chapter, &r)
		if err != nil {
			return nil, fmt.Errorf("refresh chapter progress: %w", err)
		}
		chapterProgressPath = "meta/chapter_progress.md"
		if ledger != nil {
			nextPlan = ledger.NextPlan
		}
	}

	// 追加 checkpoint
	scope := domain.ChapterScope(r.Chapter)
	if r.Scope == "arc" {
		vol, arc := 0, 0
		if progress != nil {
			vol, arc = progress.CurrentVolume, progress.CurrentArc
		}
		scope = domain.ArcScope(vol, arc)
	}
	artifact := fmt.Sprintf("reviews/%02d.json", r.Chapter)
	if r.Scope == "arc" {
		artifact = fmt.Sprintf("reviews/%02d-arc.json", r.Chapter)
	} else if r.Scope == "global" {
		artifact = fmt.Sprintf("reviews/%02d-global.json", r.Chapter)
	}
	if _, err := t.store.Checkpoints.AppendArtifact(scope, "review", artifact); err != nil {
		return nil, fmt.Errorf("checkpoint review: %w", err)
	}
	ragIndexed, ragErr := t.sedimentReviewRAG(r, finalVerdict, affected, escalationReason)
	projectMemoryRAGIndexed := false
	var projectMemoryRAGErr error
	if finalVerdict == "accept" && r.Scope == "chapter" {
		projectMemoryRAGIndexed, projectMemoryRAGErr = upsertProjectMemoryRAG(context.Background(), t.store, t.ragEmbedder, t.ragVectorWriter, r.Chapter)
	}

	result := map[string]any{
		"saved":                      true,
		"chapter":                    r.Chapter,
		"scope":                      r.Scope,
		"verdict":                    r.Verdict,
		"final_verdict":              finalVerdict,
		"review_round":               reviewRound,
		"escalation_reason":          escalationReason,
		"affected_chapters":          affected,
		"issues":                     len(r.Issues),
		"next_flow":                  nextFlow,
		"next_chapter":               nextChapter,
		"book_complete":              bookComplete,
		"manuscript_path":            manuscriptPath,
		"chapter_progress":           chapterProgressPath,
		"project_progress":           projectProgressPath(chapterProgressPath),
		"evolution_report":           evolutionReportPath(chapterProgressPath),
		"next_plan":                  nextPlan,
		"writing_feedback":           writingFeedbackPath,
		"writing_feedback_entries":   writingFeedbackEntries,
		"writing_feedback_features":  writingFeedbackFeatures,
		"rag_indexed":                ragIndexed || projectMemoryRAGIndexed,
		"rag_error":                  joinErrorStrings(ragErr, projectMemoryRAGErr),
		"review_rag_indexed":         ragIndexed,
		"project_memory_rag_indexed": projectMemoryRAGIndexed,
	}
	// Task 068 边界复评事实：verdict 落在 polish/rewrite 边界（关键维度 55-65 分，
	// 或 critical/error issue 与 accept 并存）时透出 boundary_review_suggested，
	// 由 Coordinator 决定是否让同一 judge 复评一次（上限 1 次）；judge 置信度
	// 复用 ConfidenceReport 的观测语义，绝不进自动化控制流。
	if boundary := reviewBoundaryReason(r, finalVerdict, t.isMidVolume(r.Chapter)); boundary != "" {
		result["boundary_review_suggested"] = true
		result["boundary_reason"] = boundary
	}

	// 循环刹车提示（纯事实，不动控制流）：同章第 3 轮及以上仍判 rewrite，
	// 大概率是标准漂移、缺少写法资料或换 issue 循环——提醒先查资料再改，
	// 再决定改走 polish/带备注放行或升级给用户。
	if reviewRound >= 3 && (finalVerdict == "rewrite" || finalVerdict == "polish") {
		result["review_round_note"] = fmt.Sprintf("本章已进入第 %d 轮审阅仍未通过：先对照 reviews/%02d.history.jsonl 确认本轮 issue 与前几轮是否同类；同类=返工无效，必须先 craft_recall(dialogue/methodology/scene_situation) 查人物刻画、情感叙事、对白摩擦、段落节奏或 AI 检测方法，召回弱/无料时先 web_research 查资料并沉淀到 meta/writing-techniques、web_reference_brief 或 review RAG，再决定 polish 局部修/带备注放行/升级用户；不同类=标准可能在漂移，收敛到最初契约", reviewRound, r.Chapter)
	}
	return json.Marshal(result)
}

func (t *SaveReviewTool) sedimentReviewRAG(r domain.ReviewEntry, finalVerdict string, affected []int, escalationReason string) (bool, error) {
	chunks := reviewRAGChunks(r, finalVerdict, affected, escalationReason)
	if len(chunks) == 0 {
		return false, nil
	}
	if err := upsertRAGChunks(context.Background(), t.store, t.ragEmbedder, t.ragVectorWriter, chunks, domain.RAGIndexConfig{}); err != nil {
		return true, err
	}
	return true, nil
}

func reviewRAGChunks(r domain.ReviewEntry, finalVerdict string, affected []int, escalationReason string) []domain.RAGChunk {
	return ReviewRAGChunks(r, finalVerdict, affected, escalationReason)
}

func ReviewRAGChunks(r domain.ReviewEntry, finalVerdict string, affected []int, escalationReason string) []domain.RAGChunk {
	var b strings.Builder
	fmt.Fprintf(&b, "# 第 %d 章审阅沉淀\n", r.Chapter)
	fmt.Fprintf(&b, "范围：%s\n", r.Scope)
	fmt.Fprintf(&b, "结论：%s\n", finalVerdict)
	if strings.TrimSpace(escalationReason) != "" {
		fmt.Fprintf(&b, "升级原因：%s\n", escalationReason)
	}
	if strings.TrimSpace(r.Summary) != "" {
		fmt.Fprintf(&b, "总结：%s\n", r.Summary)
	}
	if r.ContractStatus != "" {
		fmt.Fprintf(&b, "合同状态：%s\n", r.ContractStatus)
	}
	for _, miss := range r.ContractMisses {
		if strings.TrimSpace(miss) != "" {
			fmt.Fprintf(&b, "合同漏项：%s\n", miss)
		}
	}
	for _, issue := range r.Issues {
		fmt.Fprintf(&b, "问题：[%s/%s] %s；证据：%s；建议：%s\n", issue.Type, issue.Severity, issue.Description, issue.Evidence, issue.Suggestion)
	}
	if len(affected) > 0 {
		fmt.Fprintf(&b, "返工章节：%s\n", formatIntList(affected))
	}
	text := strings.TrimSpace(b.String())
	if text == "" {
		return nil
	}
	return chunksFromRAGText(
		fmt.Sprintf("reviews/%02d.json", r.Chapter),
		"review",
		"review",
		fmt.Sprintf("第 %d 章审阅 | %s", r.Chapter, r.Scope),
		text,
		r.Summary,
		reviewRAGKeywords(r, finalVerdict),
		map[string]any{"source": "save_review", "chapter": r.Chapter, "scope": r.Scope, "verdict": finalVerdict},
		1200,
	)
}

func reviewRAGKeywords(r domain.ReviewEntry, finalVerdict string) []string {
	keywords := []string{r.Scope, finalVerdict, "审阅反馈", "历史反馈", "review_lesson"}
	for _, issue := range r.Issues {
		keywords = append(keywords, issue.Type, issue.Severity)
	}
	for _, dim := range r.Dimensions {
		if dim.Score < 80 || dim.Verdict != "pass" {
			keywords = append(keywords, dim.Dimension)
		}
	}
	if len(r.ContractMisses) > 0 {
		keywords = append(keywords, "contract_miss")
	}
	return uniqueStrings(keywords)
}

func formatIntList(values []int) string {
	if len(values) == 0 {
		return ""
	}
	parts := make([]string, 0, len(values))
	for _, v := range values {
		parts = append(parts, fmt.Sprintf("%d", v))
	}
	return strings.Join(parts, "、")
}

func projectProgressPath(chapterProgressPath string) string {
	if chapterProgressPath == "" {
		return ""
	}
	return "meta/project_progress.md"
}

func evolutionReportPath(chapterProgressPath string) string {
	if chapterProgressPath == "" {
		return ""
	}
	return "meta/evolution_report.md"
}

func joinErrorStrings(errs ...error) string {
	var parts []string
	for _, err := range errs {
		if err != nil {
			parts = append(parts, err.Error())
		}
	}
	return strings.Join(parts, "；")
}

func (t *SaveReviewTool) completeBookIfReady(r domain.ReviewEntry, progress *domain.Progress) (bool, string, error) {
	if progress == nil || progress.Phase != domain.PhaseWriting || len(progress.PendingRewrites) > 0 {
		return false, "", nil
	}
	if r.Scope == "global" {
		return t.completeShortBookAfterGlobalReview(r, progress)
	}
	if r.Scope != "chapter" || !t.store.World.HasAcceptedChapterReviews(progress.CompletedChapters) {
		return false, "", nil
	}
	if progress.Layered {
		if progress.ReopenedFromComplete {
			if err := t.store.Progress.MarkComplete(); err != nil {
				return false, "", fmt.Errorf("mark reopened layered book complete: %w", err)
			}
			return true, "", nil
		}
		return false, "", nil
	}
	if !domain.StructurallyComplete(progress) {
		return false, "", nil
	}
	meta, _ := t.store.RunMeta.Load()
	if domain.RequiresFinalGlobalReview(progress, meta) {
		return false, "", nil
	}
	if err := t.store.Progress.MarkComplete(); err != nil {
		return false, "", fmt.Errorf("mark non-layered book complete: %w", err)
	}
	return true, "", nil
}

func (t *SaveReviewTool) completeShortBookAfterGlobalReview(r domain.ReviewEntry, progress *domain.Progress) (bool, string, error) {
	if progress.Layered || !domain.StructurallyComplete(progress) || r.Chapter != progress.LatestCompleted() {
		return false, "", nil
	}
	meta, _ := t.store.RunMeta.Load()
	if !domain.RequiresFinalGlobalReview(progress, meta) {
		return false, "", nil
	}
	if !t.store.World.HasAcceptedChapterReviews(progress.CompletedChapters) {
		return false, "", nil
	}
	if err := t.writeMergedManuscript(progress); err != nil {
		return false, "", fmt.Errorf("write merged manuscript: %w", err)
	}
	if err := t.store.Progress.MarkComplete(); err != nil {
		return false, "", fmt.Errorf("mark short book complete: %w", err)
	}
	return true, filepath.Join(t.store.Dir(), "正文.md"), nil
}

func (t *SaveReviewTool) writeMergedManuscript(progress *domain.Progress) error {
	outline, _ := t.store.Outline.LoadOutline()
	titles := make(map[int]string, len(outline))
	for _, entry := range outline {
		if entry.Title != "" {
			titles[entry.Chapter] = entry.Title
		}
	}
	chapters := append([]int(nil), progress.CompletedChapters...)
	slices.Sort(chapters)

	var b strings.Builder
	if name := strings.TrimSpace(progress.NovelName); name != "" {
		fmt.Fprintf(&b, "# %s\n\n", name)
	}
	for i, ch := range chapters {
		if i > 0 {
			b.WriteString("\n\n")
		}
		title := strings.TrimSpace(titles[ch])
		if title == "" {
			fmt.Fprintf(&b, "## 第 %d 章\n\n", ch)
		} else {
			fmt.Fprintf(&b, "## 第 %d 章 %s\n\n", ch, title)
		}
		text, err := t.store.Drafts.LoadChapterText(ch)
		if err != nil {
			return fmt.Errorf("load chapter %d: %w", ch, err)
		}
		text = strings.TrimSpace(text)
		if text == "" {
			return fmt.Errorf("chapter %d final text is empty", ch)
		}
		b.WriteString(text)
	}
	return t.store.Drafts.SaveMergedManuscript(b.String())
}

var expectedReviewDimensions = map[string]struct{}{
	"consistency":        {},
	"character":          {},
	"pacing":             {},
	"continuity":         {},
	"foreshadow":         {},
	"hook":               {},
	"aesthetic":          {},
	"ai_voice_detection": {},
}

func validateReviewEntry(r domain.ReviewEntry) error {
	if strings.TrimSpace(r.Scope) == "" {
		return fmt.Errorf("scope is required")
	}
	if strings.TrimSpace(r.Summary) == "" {
		return fmt.Errorf("summary is required")
	}
	for _, issue := range r.Issues {
		if strings.TrimSpace(issue.Description) == "" {
			return fmt.Errorf("issue description is required")
		}
		if strings.TrimSpace(issue.Evidence) == "" {
			return fmt.Errorf("issue evidence is required")
		}
	}
	if err := validateDimensions(r.Dimensions); err != nil {
		return err
	}
	if (r.Verdict == "rewrite" || r.Verdict == "polish") && len(r.AffectedChapters) == 0 {
		return fmt.Errorf("affected_chapters is required when verdict=%s", r.Verdict)
	}
	return nil
}

func validateDimensions(dimensions []domain.DimensionScore) error {
	if len(dimensions) != len(expectedReviewDimensions) {
		return fmt.Errorf("dimensions must contain exactly %d entries", len(expectedReviewDimensions))
	}

	seen := make(map[string]struct{}, len(dimensions))
	for _, dim := range dimensions {
		if _, ok := expectedReviewDimensions[dim.Dimension]; !ok {
			return fmt.Errorf("unknown dimension: %s", dim.Dimension)
		}
		if _, ok := seen[dim.Dimension]; ok {
			return fmt.Errorf("duplicate dimension: %s", dim.Dimension)
		}
		seen[dim.Dimension] = struct{}{}
		if dim.Score < 0 || dim.Score > 100 {
			return fmt.Errorf("invalid score for %s: %d", dim.Dimension, dim.Score)
		}
		if strings.TrimSpace(dim.Comment) == "" {
			return fmt.Errorf("dimension comment is required: %s", dim.Dimension)
		}
	}
	return nil
}

func expectedDimensionVerdict(score int) string {
	switch {
	case score >= 80:
		return "pass"
	case score >= 60:
		return "warning"
	default:
		return "fail"
	}
}

// criticalDimensions 定义会触发 verdict 升级到 rewrite 的关键维度。
var criticalDimensions = map[string]struct{}{
	"consistency": {},
	"character":   {},
	"continuity":  {},
}

func verdictRank(verdict string) int {
	switch verdict {
	case "rewrite":
		return 2
	case "polish":
		return 1
	default:
		return 0
	}
}

func contractReviewGate(r domain.ReviewEntry) (string, string) {
	switch r.ContractStatus {
	case "missed":
		return "rewrite", "合同履约状态为 missed，升级为重写"
	case "partial":
		return "polish", "合同履约状态为 partial，升级为打磨"
	default:
		return "", ""
	}
}

func scorecardReviewGate(dimensions []domain.DimensionScore) (string, string) {
	var criticalFails []string
	var polishIssues []string

	for _, dim := range dimensions {
		_, isCritical := criticalDimensions[dim.Dimension]
		if isCritical && (dim.Verdict == "fail" || dim.Score < 60) {
			criticalFails = append(criticalFails, fmt.Sprintf("%s(%d)", dim.Dimension, dim.Score))
		} else if dim.Verdict == "fail" || dim.Verdict == "warning" || (isCritical && dim.Score < 80) {
			polishIssues = append(polishIssues, fmt.Sprintf("%s(%d)", dim.Dimension, dim.Score))
		}
	}

	if len(criticalFails) > 0 {
		return "rewrite", fmt.Sprintf("关键维度不合格 %v", criticalFails)
	}
	if len(polishIssues) > 0 {
		return "polish", fmt.Sprintf("部分维度需打磨 %v", polishIssues)
	}
	return "", ""
}

func (t *SaveReviewTool) aiVoiceReviewGate(r domain.ReviewEntry) (string, string) {
	if r.Scope != "chapter" || r.Chapter <= 0 {
		return "", ""
	}
	analysis, err := t.currentFinalAIVoiceAnalysis(r.Chapter)
	if err != nil || analysis == nil {
		return "", ""
	}
	return aiVoiceAnalysisGate(*analysis)
}

func aiVoiceAnalysisGate(analysis domain.AIVoiceAnalysis) (string, string) {
	var rewriteFlags []string
	for _, flag := range analysis.RedFlags {
		label := flag.Rule
		if strings.TrimSpace(flag.Rule) == "supporting_dialogue_ratio" {
			continue
		}
		if reviewreport.IsBlockingAIVoiceFlagInAnalysis(flag, analysis) {
			rewriteFlags = append(rewriteFlags, label)
			continue
		}
	}
	if len(rewriteFlags) > 0 {
		return "rewrite", fmt.Sprintf("AI味阻断项/AI红旗硬门禁必须重写 %v", compactIssueLabels(rewriteFlags))
	}
	return "", ""
}

func (t *SaveReviewTool) currentFinalAIVoiceAnalysis(chapter int) (*domain.AIVoiceAnalysis, error) {
	text, err := t.store.Drafts.LoadChapterText(chapter)
	if err != nil || strings.TrimSpace(text) == "" {
		return nil, err
	}
	history, _ := t.store.AIVoice.LoadAllChapterMetrics()
	analysis := editrules.AnalyzeChapter(chapter, text, history)
	if previousMetrics, _ := t.store.AIVoice.LoadChapterMetrics(chapter); previousMetrics != nil {
		analysis.Metrics.RevisionRound = previousMetrics.RevisionRound
		analysis.Metrics.BeforeAfterDiff = previousMetrics.BeforeAfterDiff
		if len(previousMetrics.AIVoiceScoreHistory) > 0 {
			analysis.Metrics.AIVoiceScoreHistory = append([]domain.AIVoiceScorePoint(nil), previousMetrics.AIVoiceScoreHistory...)
		}
	}
	return &analysis, nil
}

func (t *SaveReviewTool) persistAIVoiceReview(r domain.ReviewEntry) (*domain.AIVoiceAnalysis, error) {
	if r.Scope != "chapter" || r.Chapter <= 0 {
		return nil, nil
	}
	analysis, err := t.currentFinalAIVoiceAnalysis(r.Chapter)
	if err != nil {
		return nil, err
	}
	if analysis == nil {
		analysis, err = t.store.AIVoice.LoadRedFlags(r.Chapter)
		if err != nil {
			return nil, err
		}
		if analysis == nil {
			return nil, nil
		}
	}
	if dim := r.Dimension("ai_voice_detection"); dim != nil {
		modelRisk := roundReviewRisk(1 - float64(dim.Score)/100)
		analysis.Metrics.AIVoiceScoreHistory = append(analysis.Metrics.AIVoiceScoreHistory, domain.AIVoiceScorePoint{
			Round:  analysis.Metrics.RevisionRound,
			Source: "editor",
			Score:  modelRisk,
			At:     time.Now().Format(time.RFC3339),
		})
		if modelRisk > analysis.Metrics.AIVoiceScore {
			analysis.Metrics.AIVoiceScore = modelRisk
		}
	}
	if err := t.store.AIVoice.SaveChapterMetrics(analysis.Metrics, false); err != nil {
		return nil, err
	}
	if err := t.store.AIVoice.SaveRedFlags(*analysis); err != nil {
		return nil, err
	}
	return analysis, nil
}

func (t *SaveReviewTool) writeUnifiedChapterReview(r domain.ReviewEntry, aiVoice *domain.AIVoiceAnalysis) error {
	if r.Scope != "chapter" || r.Chapter <= 0 {
		return nil
	}
	mechanical, _, err := reviewreport.LoadMechanicalGate(t.store.Dir(), r.Chapter)
	if err != nil {
		return err
	}
	if aiVoice == nil {
		var loadErr error
		aiVoice, loadErr = t.store.AIVoice.LoadRedFlags(r.Chapter)
		if loadErr != nil {
			return loadErr
		}
	}
	return reviewreport.WriteUnifiedMarkdown(t.store.Dir(), reviewreport.UnifiedMarkdownInput{
		Chapter:    r.Chapter,
		Mechanical: mechanical,
		AIVoice:    aiVoice,
		Editor:     &r,
	})
}

func roundReviewRisk(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return float64(int(v*10000+0.5)) / 10000
}

func issueSeverityReviewGate(issues []domain.ConsistencyIssue) (string, string) {
	var critical []string
	var errors []string
	for _, issue := range issues {
		label := issue.Type
		if label == "" {
			label = issue.Description
		}
		switch issue.Severity {
		case "critical":
			critical = append(critical, label)
		case "error":
			errors = append(errors, label)
		}
	}
	if len(critical) > 0 {
		return "rewrite", fmt.Sprintf("critical issues 必须重写 %v", compactIssueLabels(critical))
	}
	if len(errors) > 0 {
		return "polish", fmt.Sprintf("error issues 必须打磨 %v", compactIssueLabels(errors))
	}
	return "", ""
}

func compactIssueLabels(values []string) []string {
	limit := min(len(values), 5)
	return append([]string(nil), values[:limit]...)
}

// reviewBoundaryReason Task 068/075：检测评审是否落在需要复评的边界带。
// 卷中段（30%-70% 区间）阈值放宽到 55-70——Lost in Stories 实证：一致性错误集中在
// 叙事中段，此处更容易建议复评。
func reviewBoundaryReason(r domain.ReviewEntry, finalVerdict string, midVolume bool) string {
	upper := 65
	if midVolume {
		upper = 70
	}
	for _, d := range r.Dimensions {
		if d.Score >= 55 && d.Score <= upper {
			zone := "55-65"
			if midVolume {
				zone = "55-70（卷中段加严）"
			}
			return fmt.Sprintf("维度 %s=%d 落在 polish/rewrite 边界带（%s），建议同一 judge 温度不变复评一次后再定", d.Dimension, d.Score, zone)
		}
	}
	if finalVerdict == "accept" {
		for _, issue := range r.Issues {
			if issue.Severity == "critical" || issue.Severity == "error" {
				return fmt.Sprintf("accept 与 %s 级 issue 并存（%s），建议复评一次", issue.Severity, issue.Description)
			}
		}
	}
	return ""
}

// isMidVolume 判断章节是否处于当前卷的中段（卷章数的 30%-70% 区间）。
// 无分层大纲/定位失败时返回 false（不加严）。
func (t *SaveReviewTool) isMidVolume(chapter int) bool {
	volumes, err := t.store.Outline.LoadLayeredOutline()
	if err != nil || len(volumes) == 0 {
		return false
	}
	offset := 0
	for _, v := range volumes {
		count := 0
		for _, a := range v.Arcs {
			count += len(a.Chapters)
		}
		if count == 0 {
			continue
		}
		if chapter > offset && chapter <= offset+count {
			pos := float64(chapter-offset) / float64(count)
			return pos >= 0.3 && pos <= 0.7
		}
		offset += count
	}
	return false
}
