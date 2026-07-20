package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/chenhongyang/novel-studio/assets"
	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/entry/headless"
	"github.com/chenhongyang/novel-studio/internal/store"
	toolspkg "github.com/chenhongyang/novel-studio/internal/tools"
)

const (
	pipelineFinalizationJSON       = "meta/finalization.json"
	pipelineFinalizationMD         = "meta/finalization.md"
	pipelinePublicationPackageJSON = "meta/publication_package.json"
	pipelinePublicationPackageMD   = "meta/publication_package.md"
)

type pipelineFinalReviewHost func(bootstrap.Config, assets.Bundle, headless.Options) error

// pipelineTerminalReviewRebaseRequiredError is an explicit control-plane
// transition, not a request for the Host to edit prose.  Once every chapter in
// a sealed generation has an immutable acceptance receipt, an ordinary
// rewrite cannot legally replace one chapter in place.  The existing
// --rebase-all-chapters transaction is the only implemented path that first
// archives the exact old canon and then returns the live project to chapter
// zero for a new sealed generation.
type pipelineTerminalReviewRebaseRequiredError struct {
	OutputDir        string
	Verdict          string
	AffectedChapters []int
	PendingRewrites  []int
}

func (e *pipelineTerminalReviewRebaseRequiredError) Error() string {
	projectDir := pipelineRebaseRunRoot(e.OutputDir)
	return fmt.Sprintf(
		"全文终审 verdict=%s，affected_chapters=%v，pending_rewrites=%v；finalize 已停止，未启动普通 write/rewrite，也未修改正文。已验收 sealed 正史若必须否定，只能跨显式全书 rebase 边界：\n"+
			"1) novel-studio --pipeline --dir %q --rebase-all-chapters --stages architect,zero-init,preplan,project-all,seal --restart\n"+
			"2) 重复 novel-studio --pipeline --dir %q --stages promote,render，直到全部章节重新验收\n"+
			"3) novel-studio --pipeline --dir %q --stages finalize,deliver\n"+
			"第 1 步会先对旧正史做 exact-root 可恢复归档；在用户显式执行前，拒稿与 pending 状态保持不变",
		e.Verdict,
		e.AffectedChapters,
		e.PendingRewrites,
		projectDir,
		projectDir,
		projectDir,
	)
}

type pipelineFinalizationManifest struct {
	Version             int                           `json:"version"`
	Title               string                        `json:"title"`
	ProjectTitle        string                        `json:"project_title,omitempty"`
	AlternateTitles     []string                      `json:"alternate_titles"`
	HookLead            string                        `json:"hook_lead"`
	SpoilerFreeBlurb    string                        `json:"spoiler_free_blurb"`
	Tags                []string                      `json:"tags"`
	CountingPolicy      string                        `json:"counting_policy"`
	TargetMinRunes      int                           `json:"target_min_runes,omitempty"`
	TargetMaxRunes      int                           `json:"target_max_runes,omitempty"`
	TotalRunes          int                           `json:"total_runes"`
	Chapters            []pipelineFinalizationChapter `json:"chapters"`
	GlobalReviewPath    string                        `json:"global_review_path"`
	GlobalReviewVerdict string                        `json:"global_review_verdict"`
	GlobalReviewSummary string                        `json:"global_review_summary,omitempty"`
	GlobalReviewBookSHA string                        `json:"global_review_book_sha256"`
	MergedManuscript    string                        `json:"merged_manuscript"`
	MergedManuscriptSHA string                        `json:"merged_manuscript_sha256"`
	Checks              []pipelineFinalizationCheck   `json:"checks"`
}

type pipelinePublicationPackage struct {
	Version            int                           `json:"version"`
	Platform           string                        `json:"platform"`
	PrimaryTitle       string                        `json:"primary_title"`
	AlternateTitles    []string                      `json:"alternate_titles"`
	HookLead           string                        `json:"hook_lead"`
	SpoilerFreeBlurb   string                        `json:"spoiler_free_blurb"`
	Tags               []string                      `json:"tags"`
	Chapters           []pipelineFinalizationChapter `json:"chapters"`
	TotalRunes         int                           `json:"total_runes"`
	CountingPolicy     string                        `json:"counting_policy"`
	FinalChecks        []pipelineFinalizationCheck   `json:"final_checks"`
	MergedManuscript   string                        `json:"merged_manuscript"`
	FinalizationSource string                        `json:"finalization_source"`
}

type pipelineFinalizationChapter struct {
	Chapter    int    `json:"chapter"`
	Title      string `json:"title"`
	Path       string `json:"path"`
	RuneCount  int    `json:"rune_count"`
	BodySHA256 string `json:"body_sha256"`
}

type pipelineFinalizationCheck struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Status string `json:"status"`
	Source string `json:"source"`
}

func pipelineFinalize(opts cliOptions, flags pipelineFlags) error {
	if flags.Start > 0 || flags.End > 0 {
		return fmt.Errorf("finalize 是全书终审阶段，不接受 --from/--to 章节范围")
	}
	cfg, bundle, err := loadCfgBundle(opts)
	if err != nil {
		return err
	}
	if err := ensurePipelineRAGReady(cfg); err != nil {
		return fmt.Errorf("finalize 阶段 RAG 就绪检查失败: %w", err)
	}
	return pipelineFinalizeConfigured(cfg, bundle, flags, headless.Run)
}

func pipelineFinalizeConfigured(
	cfg bootstrap.Config,
	bundle assets.Bundle,
	flags pipelineFlags,
	runHost pipelineFinalReviewHost,
) error {
	st := store.NewStore(cfg.OutputDir)
	if err := currentPipelineTerminalReviewRecovery(st, cfg.OutputDir); err != nil {
		return err
	}
	chapters, last, progress, err := validatePipelineFinalizePrerequisites(st, cfg.OutputDir, flags)
	if err != nil {
		return err
	}

	globalReview, err := st.World.LoadLastReview(last)
	if err != nil {
		return fmt.Errorf("读取全文终审: %w", err)
	}
	if globalReview != nil && globalReview.Chapter == last && st.World.HasCurrentGlobalReview(last) {
		if globalReview.Verdict != "accept" {
			return terminalReviewRebaseRequired(st, cfg.OutputDir, globalReview)
		}
		if progress.Phase != domain.PhaseComplete || !nonEmptyFile(filepath.Join(cfg.OutputDir, "正文.md")) {
			return fmt.Errorf("全文终审已 accept，但 progress.phase=%s 或 正文.md 缺失；拒绝把不完整完结态伪装为已 finalize", progress.Phase)
		}
		return writePipelineFinalizationArtifacts(st, cfg.OutputDir, chapters, globalReview)
	}

	if runHost == nil {
		return fmt.Errorf("finalize 缺少全文终审 Host")
	}
	if err := runHost(cfg, bundle, headless.Options{
		SkipQueueReplay:              true,
		StopAfterGlobalReviewChapter: last,
	}); err != nil {
		return fmt.Errorf("全文终审 Host 失败: %w", err)
	}

	st = store.NewStore(cfg.OutputDir)
	globalReview, err = st.World.LoadLastReview(last)
	if err != nil || globalReview == nil || globalReview.Chapter != last || globalReview.Scope != "global" ||
		!st.World.HasCurrentGlobalReview(last) {
		return fmt.Errorf("finalize 未产出 reviews/%02d-global.json: %w", last, err)
	}
	if globalReview.Verdict != "accept" {
		return terminalReviewRebaseRequired(st, cfg.OutputDir, globalReview)
	}
	progress, err = st.Progress.Load()
	if err != nil || progress == nil || progress.Phase != domain.PhaseComplete {
		return fmt.Errorf("全文终审 accept 后 progress 未进入 complete: %w", err)
	}
	if !nonEmptyFile(filepath.Join(cfg.OutputDir, "正文.md")) {
		return fmt.Errorf("全文终审 accept 后未生成 正文.md")
	}
	return writePipelineFinalizationArtifacts(st, cfg.OutputDir, chapters, globalReview)
}

// currentPipelineTerminalReviewRecovery makes a rejected, exact-book terminal
// review restart-safe.  SaveReview persists the review before updating the
// pending queue, so this check intentionally recognizes both sides of that
// crash window: a current rejected global review is sufficient even when the
// queue write did not happen yet.  It never clears pending work or mutates
// prose; it only returns the executable explicit recovery transition.
func currentPipelineTerminalReviewRecovery(st *store.Store, outputDir string) error {
	if st == nil {
		return nil
	}
	progress, err := st.Progress.Load()
	if err != nil || progress == nil {
		return err
	}
	meta, _ := st.RunMeta.Load()
	if !domain.RequiresFinalGlobalReview(progress, meta) {
		return nil
	}
	last := progress.LatestCompleted()
	if last <= 0 || last != progress.TotalChapters || !domain.StructurallyComplete(progress) {
		return nil
	}
	review, err := st.World.LoadLastReview(last)
	if err != nil {
		return fmt.Errorf("读取已落盘全文终审恢复状态: %w", err)
	}
	if review == nil || review.Chapter != last || review.Scope != "global" ||
		review.Verdict == "accept" || !st.World.HasCurrentGlobalReview(last) {
		return nil
	}
	return terminalReviewRebaseRequired(st, outputDir, review)
}

func terminalReviewRebaseRequired(
	st *store.Store,
	outputDir string,
	review *domain.ReviewEntry,
) error {
	pending := []int(nil)
	if st != nil {
		if progress, err := st.Progress.Load(); err == nil && progress != nil {
			pending = append(pending, progress.PendingRewrites...)
		}
	}
	verdict := "rewrite"
	affected := []int(nil)
	if review != nil {
		if strings.TrimSpace(review.Verdict) != "" {
			verdict = review.Verdict
		}
		affected = append(affected, review.AffectedChapters...)
	}
	return &pipelineTerminalReviewRebaseRequiredError{
		OutputDir:        outputDir,
		Verdict:          verdict,
		AffectedChapters: affected,
		PendingRewrites:  pending,
	}
}

func validatePipelineFinalizePrerequisites(
	st *store.Store,
	outputDir string,
	flags pipelineFlags,
) ([]int, int, *domain.Progress, error) {
	if st == nil {
		return nil, 0, nil, fmt.Errorf("finalize store is nil")
	}
	progress, err := st.Progress.Load()
	if err != nil || progress == nil {
		return nil, 0, nil, fmt.Errorf("finalize 读取 progress: %w", err)
	}
	meta, _ := st.RunMeta.Load()
	if !domain.RequiresFinalGlobalReview(progress, meta) {
		return nil, 0, nil, fmt.Errorf("当前项目不属于需要 scope=global 全文终审的短篇")
	}
	if progress.Layered && (meta == nil || meta.PlanningTier != domain.PlanningTierShort) {
		return nil, 0, nil, fmt.Errorf("layered 项目只有显式 planning_tier=short 才允许全文终审")
	}
	if !domain.StructurallyComplete(progress) {
		return nil, 0, nil, fmt.Errorf("finalize 前章节尚未写满：completed=%d total=%d", len(progress.CompletedChapters), progress.TotalChapters)
	}
	if len(progress.PendingRewrites) > 0 {
		return nil, 0, nil, fmt.Errorf("finalize 前仍有 pending_rewrites=%v", progress.PendingRewrites)
	}
	chapters, err := chapterNumbersFromFiles(filepath.Join(outputDir, "chapters"))
	if err != nil {
		return nil, 0, nil, err
	}
	if len(chapters) != progress.TotalChapters {
		return nil, 0, nil, fmt.Errorf("finalize 章节文件数=%d，progress.total_chapters=%d", len(chapters), progress.TotalChapters)
	}
	for index, chapter := range chapters {
		if chapter != index+1 {
			return nil, 0, nil, fmt.Errorf("finalize 章节文件必须从 01 连续到 %02d，实际=%v", progress.TotalChapters, chapters)
		}
		if issues := currentRegisteredExternalDeliveryIssues(outputDir, chapter); len(issues) > 0 {
			return nil, 0, nil, fmt.Errorf("第 %d 章全文终审前仍有外部抽查阻断：%s", chapter, strings.Join(issues, ", "))
		}
		current := inspectCurrentChapterReview(outputDir, chapter)
		if len(current.Issues) > 0 || current.Verdict != "accept" || current.Disposition == "是" || current.Disposition == "待定" {
			return nil, 0, nil, fmt.Errorf(
				"第 %d 章不是可进入全文终审的 exact-body accept：verdict=%q disposition=%q issues=%s",
				chapter,
				current.Verdict,
				current.Disposition,
				strings.Join(current.Issues, ", "),
			)
		}
	}
	if err := validatePipelineFullBookWordBudget(st, outputDir, chapters); err != nil {
		return nil, 0, nil, err
	}
	if err := validatePipelineTerminalShortArcProof(st, progress, meta); err != nil {
		return nil, 0, nil, err
	}
	return chapters, chapters[len(chapters)-1], progress, nil
}

func validatePipelineTerminalShortArcProof(
	st *store.Store,
	progress *domain.Progress,
	meta *domain.RunMeta,
) error {
	if progress == nil || !progress.Layered {
		return nil
	}
	if meta == nil || meta.PlanningTier != domain.PlanningTierShort {
		return fmt.Errorf("layered 全文终审缺少显式 short planning tier")
	}
	projected := st.ProjectedV2()
	active, err := projected.LoadActiveGeneration()
	if err != nil {
		return fmt.Errorf("layered 短篇 finalize 缺少 active sealed generation: %w", err)
	}
	if active == nil {
		return fmt.Errorf("layered 短篇 finalize 缺少 active sealed generation")
	}
	generation, err := projected.LoadSealedGeneration(active.GenerationID)
	if err != nil {
		return fmt.Errorf("layered 短篇 finalize 读取 sealed generation: %w", err)
	}
	if generation == nil {
		return fmt.Errorf("layered 短篇 finalize 找不到 sealed generation %s", active.GenerationID)
	}
	last := progress.TotalChapters
	if generation.FirstProjectedChapter != 1 ||
		generation.LastProjectedChapter != last ||
		generation.ExpectedChapterCount != last ||
		generation.BookHorizonChapter != last {
		return fmt.Errorf(
			"layered 短篇终端 generation 范围必须精确覆盖 1..%d，实际=%d..%d count=%d horizon=%d",
			last,
			generation.FirstProjectedChapter,
			generation.LastProjectedChapter,
			generation.ExpectedChapterCount,
			generation.BookHorizonChapter,
		)
	}
	cursor, err := projected.LoadRealizationCursor()
	if err != nil {
		return fmt.Errorf("layered 短篇 finalize 读取 realization cursor: %w", err)
	}
	if cursor == nil {
		return fmt.Errorf("layered 短篇 finalize 缺少 realization cursor")
	}
	if cursor.ActiveGenerationID != generation.GenerationID ||
		cursor.ActivePromotedChapter != 0 ||
		cursor.LastAcceptedChapter != last ||
		cursor.NextPromoteChapter != last+1 ||
		len(cursor.BlockedByRewrites) > 0 ||
		strings.TrimSpace(cursor.LastOutcomeReceiptDigest) == "" {
		return fmt.Errorf(
			"layered 短篇 realization cursor 未在第 %d 章终端收口：active=%d last_accepted=%d next_promote=%d blocked=%v",
			last,
			cursor.ActivePromotedChapter,
			cursor.LastAcceptedChapter,
			cursor.NextPromoteChapter,
			cursor.BlockedByRewrites,
		)
	}
	acceptances, err := st.ArcCycle().ListChapterAcceptanceReceipts(generation.GenerationID)
	if err != nil {
		return fmt.Errorf("读取终端弧 chapter acceptances: %w", err)
	}
	if len(acceptances) != last {
		return fmt.Errorf("终端弧必须有 %d 份 exact-body acceptance receipts，实际=%d", last, len(acceptances))
	}
	for i, acceptance := range acceptances {
		if acceptance.Chapter != i+1 {
			return fmt.Errorf("终端弧 acceptance receipts 必须按 1..%d 连续，index=%d chapter=%d", last, i, acceptance.Chapter)
		}
	}
	if err := st.ArcCycle().ValidateArcCycle(generation.GenerationID); err != nil {
		return fmt.Errorf("终端弧 immutable acceptance chain 无效: %w", err)
	}
	completion, err := requirePipelineArcCompletion(st, generation)
	if err != nil {
		return fmt.Errorf("终端弧 completion receipt 无效: %w", err)
	}
	if completion.FirstChapter != 1 || completion.LastChapter != last ||
		len(completion.Acceptances) != last ||
		completion.FinalOutcomeReceiptDigest != cursor.LastOutcomeReceiptDigest {
		return fmt.Errorf("终端弧 completion receipt 未精确绑定 1..%d 与最终 realization cursor", last)
	}
	return nil
}

func writePipelineFinalizationArtifacts(
	st *store.Store,
	outputDir string,
	chapters []int,
	globalReview *domain.ReviewEntry,
) error {
	manifest, err := buildPipelineFinalizationManifest(st, outputDir, chapters, globalReview)
	if err != nil {
		return err
	}
	if _, err := writePipelinePlanningJSON(filepath.Join(outputDir, pipelineFinalizationJSON), manifest); err != nil {
		return fmt.Errorf("写 finalization.json: %w", err)
	}
	markdown := renderPipelineFinalizationMarkdown(manifest)
	if err := atomicWriteRewriteFile(filepath.Join(outputDir, pipelineFinalizationMD), []byte(markdown), 0o644); err != nil {
		return fmt.Errorf("写 finalization.md: %w", err)
	}
	publication := publicationPackageFromFinalization(manifest)
	if _, err := writePipelinePlanningJSON(filepath.Join(outputDir, pipelinePublicationPackageJSON), publication); err != nil {
		return fmt.Errorf("写 publication_package.json: %w", err)
	}
	if err := atomicWriteRewriteFile(
		filepath.Join(outputDir, pipelinePublicationPackageMD),
		[]byte(renderPipelinePublicationPackageMarkdown(publication)),
		0o644,
	); err != nil {
		return fmt.Errorf("写 publication_package.md: %w", err)
	}
	return nil
}

func buildPipelineFinalizationManifest(
	st *store.Store,
	outputDir string,
	chapters []int,
	globalReview *domain.ReviewEntry,
) (pipelineFinalizationManifest, error) {
	if st == nil || globalReview == nil || globalReview.Scope != "global" || globalReview.Verdict != "accept" {
		return pipelineFinalizationManifest{}, fmt.Errorf("finalization manifest requires an accepted global review")
	}
	if !st.World.HasAcceptedGlobalReview(globalReview.Chapter) {
		return pipelineFinalizationManifest{}, fmt.Errorf("finalization manifest requires a current exact-book global review")
	}
	if err := toolspkg.ValidateShortPublicationPackage(globalReview.Publication); err != nil {
		return pipelineFinalizationManifest{}, fmt.Errorf("finalization publication package: %w", err)
	}
	progress, err := st.Progress.Load()
	if err != nil || progress == nil {
		return pipelineFinalizationManifest{}, fmt.Errorf("finalization manifest 读取 progress: %w", err)
	}
	outline, err := st.Outline.LoadOutline()
	if err != nil {
		return pipelineFinalizationManifest{}, err
	}
	titles := make(map[int]string, len(outline))
	for _, entry := range outline {
		titles[entry.Chapter] = strings.TrimSpace(entry.Title)
	}
	projectTitle := strings.TrimSpace(progress.NovelName)
	if projectTitle == "" {
		if premise, loadErr := st.Outline.LoadPremise(); loadErr == nil {
			projectTitle = domain.ExtractNovelNameFromPremise(premise)
		}
	}
	manifest := pipelineFinalizationManifest{
		Version:             2,
		Title:               globalReview.Publication.PrimaryTitle,
		ProjectTitle:        projectTitle,
		AlternateTitles:     append([]string(nil), globalReview.Publication.AlternateTitles...),
		HookLead:            globalReview.Publication.HookLead,
		SpoilerFreeBlurb:    globalReview.Publication.SpoilerFreeBlurb,
		Tags:                append([]string(nil), globalReview.Publication.Tags...),
		CountingPolicy:      "UTF-8 rune count of each complete chapters/NN.md file, including its chapter heading, punctuation and whitespace; 正文.md is not used for the 2.8万—3万 hard gate",
		GlobalReviewPath:    fmt.Sprintf("reviews/%02d-global.json", globalReview.Chapter),
		GlobalReviewVerdict: globalReview.Verdict,
		GlobalReviewSummary: globalReview.Summary,
		GlobalReviewBookSHA: "sha256:" + globalReview.BookBodySHA256,
		MergedManuscript:    "正文.md",
	}
	if receipt, loadErr := st.LoadOutlineAllExecutionReceipt(); loadErr != nil {
		return pipelineFinalizationManifest{}, loadErr
	} else if receipt != nil {
		target, resolveErr := domain.ResolveBookScaleTarget(receipt.EstimatedScale, receipt.TargetVolumes, receipt.TargetChapters)
		if resolveErr != nil {
			return pipelineFinalizationManifest{}, resolveErr
		}
		manifest.TargetMinRunes = target.MinWords
		manifest.TargetMaxRunes = target.MaxWords
	}
	for _, chapter := range chapters {
		rel := fmt.Sprintf("chapters/%02d.md", chapter)
		raw, readErr := os.ReadFile(filepath.Join(outputDir, filepath.FromSlash(rel)))
		if readErr != nil {
			return pipelineFinalizationManifest{}, readErr
		}
		sum := sha256.Sum256(raw)
		count := utf8.RuneCount(raw)
		manifest.TotalRunes += count
		if titles[chapter] == "" {
			return pipelineFinalizationManifest{}, fmt.Errorf("第 %d 章缺少正式章名，不能生成发布包", chapter)
		}
		manifest.Chapters = append(manifest.Chapters, pipelineFinalizationChapter{
			Chapter: chapter, Title: titles[chapter], Path: rel, RuneCount: count,
			BodySHA256: "sha256:" + hex.EncodeToString(sum[:]),
		})
	}
	manifest.Checks = pipelineFinalizationChecks(globalReview)
	for _, check := range manifest.Checks {
		if check.Status != "pass" {
			return pipelineFinalizationManifest{}, fmt.Errorf("最终检查 %s 未通过：status=%s", check.Label, check.Status)
		}
	}
	mergedSHA, err := validatePipelineMergedManuscript(st, outputDir, chapters)
	if err != nil {
		return pipelineFinalizationManifest{}, err
	}
	manifest.MergedManuscriptSHA = mergedSHA
	return manifest, nil
}

func pipelineFinalizationChecks(review *domain.ReviewEntry) []pipelineFinalizationCheck {
	type checkSpec struct{ id, label, dimension string }
	specs := []checkSpec{
		{"continuity", "连续性", "continuity"},
		{"timeline", "时间线", "consistency"},
		{"clue_payoff", "线索回收", "foreshadow"},
		{"professional_logic", "职业常识", "consistency"},
		{"character_agency", "人物主动性", "character"},
		{"relationship_payoff", "感情兑现", "character"},
	}
	out := make([]pipelineFinalizationCheck, 0, len(specs))
	for _, spec := range specs {
		status := "missing"
		for _, dimension := range review.Dimensions {
			if dimension.Dimension == spec.dimension {
				status = dimension.Verdict
				break
			}
		}
		out = append(out, pipelineFinalizationCheck{
			ID: spec.id, Label: spec.label, Status: status,
			Source: fmt.Sprintf("%s#dimension=%s", fmt.Sprintf("reviews/%02d-global.json", review.Chapter), spec.dimension),
		})
	}
	return out
}

func buildPipelineMergedManuscript(
	st *store.Store,
	chapters []int,
) (string, error) {
	if st == nil {
		return "", fmt.Errorf("merged manuscript store is nil")
	}
	progress, err := st.Progress.Load()
	if err != nil || progress == nil {
		return "", fmt.Errorf("merged manuscript 读取 progress: %w", err)
	}
	outline, err := st.Outline.LoadOutline()
	if err != nil {
		return "", err
	}
	titles := make(map[int]string, len(outline))
	for _, entry := range outline {
		titles[entry.Chapter] = strings.TrimSpace(entry.Title)
	}
	merged := make([]store.MergedManuscriptChapter, 0, len(chapters))
	for _, chapter := range chapters {
		text, err := st.Drafts.LoadChapterText(chapter)
		if err != nil {
			return "", fmt.Errorf("merged manuscript 读取第 %d 章: %w", chapter, err)
		}
		merged = append(merged, store.MergedManuscriptChapter{
			Number: chapter,
			Title:  titles[chapter],
			Text:   text,
		})
	}
	return store.BuildMergedManuscript(progress.NovelName, merged)
}

func validatePipelineMergedManuscript(
	st *store.Store,
	outputDir string,
	chapters []int,
) (string, error) {
	want, err := buildPipelineMergedManuscript(st, chapters)
	if err != nil {
		return "", err
	}
	got, err := os.ReadFile(filepath.Join(outputDir, "正文.md"))
	if err != nil {
		return "", fmt.Errorf("读取合并正文: %w", err)
	}
	if !bytes.Equal(got, []byte(want)) {
		return "", fmt.Errorf("正文.md 不是当前章节的确定性无重复合并；重新执行全文终审")
	}
	sum := sha256.Sum256(got)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func publicationPackageFromFinalization(manifest pipelineFinalizationManifest) pipelinePublicationPackage {
	return pipelinePublicationPackage{
		Version:            1,
		Platform:           "番茄短篇",
		PrimaryTitle:       manifest.Title,
		AlternateTitles:    append([]string(nil), manifest.AlternateTitles...),
		HookLead:           manifest.HookLead,
		SpoilerFreeBlurb:   manifest.SpoilerFreeBlurb,
		Tags:               append([]string(nil), manifest.Tags...),
		Chapters:           append([]pipelineFinalizationChapter(nil), manifest.Chapters...),
		TotalRunes:         manifest.TotalRunes,
		CountingPolicy:     manifest.CountingPolicy,
		FinalChecks:        append([]pipelineFinalizationCheck(nil), manifest.Checks...),
		MergedManuscript:   manifest.MergedManuscript,
		FinalizationSource: pipelineFinalizationJSON,
	}
}

func renderPipelinePublicationPackageMarkdown(publication pipelinePublicationPackage) string {
	var b strings.Builder
	b.WriteString("# 番茄短篇发布包\n\n")
	fmt.Fprintf(&b, "## 主标题\n\n%s\n\n", publication.PrimaryTitle)
	b.WriteString("## 备选书名\n\n")
	for _, title := range publication.AlternateTitles {
		fmt.Fprintf(&b, "- %s\n", title)
	}
	fmt.Fprintf(&b, "\n## 导语\n\n%s\n\n", publication.HookLead)
	fmt.Fprintf(&b, "## 无剧透简介\n\n%s\n\n", publication.SpoilerFreeBlurb)
	fmt.Fprintf(&b, "## 标签\n\n%s\n\n", strings.Join(publication.Tags, " / "))
	b.WriteString("## 章名与字数\n\n")
	for _, chapter := range publication.Chapters {
		fmt.Fprintf(&b, "- 第 %02d 章《%s》：%d 字符\n", chapter.Chapter, chapter.Title, chapter.RuneCount)
	}
	fmt.Fprintf(&b, "\n- 全书合计：%d 字符\n", publication.TotalRunes)
	fmt.Fprintf(&b, "- 统计口径：%s\n", publication.CountingPolicy)
	b.WriteString("\n## 六项终检\n\n")
	for _, check := range publication.FinalChecks {
		fmt.Fprintf(&b, "- %s：%s\n", check.Label, check.Status)
	}
	fmt.Fprintf(&b, "\n- 合并正文：%s\n", publication.MergedManuscript)
	return b.String()
}

func renderPipelineFinalizationMarkdown(manifest pipelineFinalizationManifest) string {
	var b strings.Builder
	b.WriteString("# 全书终审与确定性交付清单\n\n")
	fmt.Fprintf(&b, "- 正式书名：%s\n", manifest.Title)
	if manifest.ProjectTitle != "" && manifest.ProjectTitle != manifest.Title {
		fmt.Fprintf(&b, "- 项目原名：%s\n", manifest.ProjectTitle)
	}
	fmt.Fprintf(&b, "- 备选书名：%s\n", strings.Join(manifest.AlternateTitles, " / "))
	fmt.Fprintf(&b, "- 导语：%s\n", manifest.HookLead)
	fmt.Fprintf(&b, "- 无剧透简介：%s\n", manifest.SpoilerFreeBlurb)
	fmt.Fprintf(&b, "- 标签：%s\n", strings.Join(manifest.Tags, " / "))
	fmt.Fprintf(&b, "- 正文统计：%d 字符", manifest.TotalRunes)
	if manifest.TargetMinRunes > 0 && manifest.TargetMaxRunes > 0 {
		fmt.Fprintf(&b, "（硬合同 %d—%d）", manifest.TargetMinRunes, manifest.TargetMaxRunes)
	}
	b.WriteString("\n")
	fmt.Fprintf(&b, "- 统计口径：%s\n", manifest.CountingPolicy)
	fmt.Fprintf(&b, "- 全文终审：%s（%s）\n", manifest.GlobalReviewVerdict, manifest.GlobalReviewPath)
	fmt.Fprintf(&b, "- 合并正文：%s（%s）\n\n", manifest.MergedManuscript, manifest.MergedManuscriptSHA)
	b.WriteString("## 逐章字数与章名\n\n")
	for _, chapter := range manifest.Chapters {
		fmt.Fprintf(&b, "- 第 %02d 章《%s》：%d 字符（%s）\n", chapter.Chapter, chapter.Title, chapter.RuneCount, chapter.Path)
	}
	b.WriteString("\n## 最终检查映射\n\n")
	for _, check := range manifest.Checks {
		fmt.Fprintf(&b, "- %s：%s（%s）\n", check.Label, check.Status, check.Source)
	}
	return b.String()
}

func requirePipelineFinalizedShortBook(outputDir string) error {
	st := store.NewStore(outputDir)
	progress, err := st.Progress.Load()
	if err != nil {
		return fmt.Errorf("读取 finalize progress: %w", err)
	}
	// Older delivery-only fixtures and non-book workspaces may not have a
	// progress ledger. They cannot be classified as short books here; the
	// ordinary delivery validators remain responsible for their own inputs.
	if progress == nil {
		return nil
	}
	meta, _ := st.RunMeta.Load()
	if !domain.RequiresFinalGlobalReview(progress, meta) {
		return nil
	}
	if progress.Phase != domain.PhaseComplete {
		return fmt.Errorf("短篇交付前必须先完成 finalize：progress.phase=%s", progress.Phase)
	}
	last := progress.LatestCompleted()
	if last <= 0 || !st.World.HasAcceptedGlobalReview(last) {
		return fmt.Errorf("短篇交付前必须先通过 reviews/%02d-global.json 全文终审", last)
	}
	if !nonEmptyFile(filepath.Join(outputDir, "正文.md")) {
		return fmt.Errorf("短篇交付前缺少全文终审生成的 正文.md")
	}
	chapters, err := chapterNumbersFromFiles(filepath.Join(outputDir, "chapters"))
	if err != nil {
		return err
	}
	globalReview, err := st.World.LoadLastReview(last)
	if err != nil || globalReview == nil || globalReview.Chapter != last {
		return fmt.Errorf("读取当前全文终审: %w", err)
	}
	want, err := buildPipelineFinalizationManifest(st, outputDir, chapters, globalReview)
	if err != nil {
		return err
	}
	var got pipelineFinalizationManifest
	raw, err := os.ReadFile(filepath.Join(outputDir, pipelineFinalizationJSON))
	if err != nil {
		return fmt.Errorf("短篇交付前缺少 %s: %w", pipelineFinalizationJSON, err)
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		return fmt.Errorf("读取 %s: %w", pipelineFinalizationJSON, err)
	}
	wantRaw, _ := json.Marshal(want)
	gotRaw, _ := json.Marshal(got)
	if !bytes.Equal(wantRaw, gotRaw) {
		return fmt.Errorf("%s 与当前章节/全文终审事实不一致；重新执行 finalize", pipelineFinalizationJSON)
	}
	wantFinalizationMD := renderPipelineFinalizationMarkdown(want)
	gotFinalizationMD, err := os.ReadFile(filepath.Join(outputDir, pipelineFinalizationMD))
	if err != nil || !bytes.Equal(gotFinalizationMD, []byte(wantFinalizationMD)) {
		return fmt.Errorf("%s 缺失或与当前 finalization.json 不一致", pipelineFinalizationMD)
	}
	wantPublication := publicationPackageFromFinalization(want)
	var gotPublication pipelinePublicationPackage
	publicationRaw, err := os.ReadFile(filepath.Join(outputDir, pipelinePublicationPackageJSON))
	if err != nil {
		return fmt.Errorf("短篇交付前缺少 %s: %w", pipelinePublicationPackageJSON, err)
	}
	if err := json.Unmarshal(publicationRaw, &gotPublication); err != nil {
		return fmt.Errorf("读取 %s: %w", pipelinePublicationPackageJSON, err)
	}
	wantPublicationRaw, _ := json.Marshal(wantPublication)
	gotPublicationRaw, _ := json.Marshal(gotPublication)
	if !bytes.Equal(wantPublicationRaw, gotPublicationRaw) {
		return fmt.Errorf("%s 与当前全文终审/字数/章名不一致", pipelinePublicationPackageJSON)
	}
	wantPublicationMD := renderPipelinePublicationPackageMarkdown(wantPublication)
	gotPublicationMD, err := os.ReadFile(filepath.Join(outputDir, pipelinePublicationPackageMD))
	if err != nil || !bytes.Equal(gotPublicationMD, []byte(wantPublicationMD)) {
		return fmt.Errorf("%s 缺失或与当前 publication_package.json 不一致", pipelinePublicationPackageMD)
	}
	return nil
}

func verifyPipelineFinalizeStage(outputDir string, evidence domain.PipelineStageEvidence) (domain.PipelineStageEvidence, error) {
	if err := requirePipelineFinalizedShortBook(outputDir); err != nil {
		return evidence, err
	}
	progress, _ := store.NewStore(outputDir).Progress.Load()
	last := 0
	if progress != nil {
		last = progress.LatestCompleted()
		evidence.ProgressPhase = string(progress.Phase)
		evidence.ProgressFlow = string(progress.Flow)
		evidence.CompletedChapters = len(progress.CompletedChapters)
	}
	evidence.Artifacts = append(evidence.Artifacts,
		fmt.Sprintf("reviews/%02d-global.json", last),
		"正文.md",
		pipelineFinalizationJSON,
		pipelineFinalizationMD,
		pipelinePublicationPackageJSON,
		pipelinePublicationPackageMD,
	)
	evidence.Message = "accepted exact-book review, canonical merged manuscript and deterministic publication package are current"
	return evidence, nil
}
