package eval

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/diag"
	"github.com/chenhongyang/novel-studio/internal/rag"
	"github.com/chenhongyang/novel-studio/internal/stylestat"
)

// Outcome 是单个 case 的门禁结论。
type Outcome string

const (
	Pass Outcome = "PASS"
	Warn Outcome = "WARN"
	Fail Outcome = "FAIL"
)

// Issue 是门禁判定中的一条记录。
type Issue struct {
	Kind     string `json:"kind"`               // hard_fail / warning / passed
	Source   string `json:"source"`             // runtime / finding:<rule> / contract:<name>
	Severity string `json:"severity,omitempty"` // critical / warning / info
	Detail   string `json:"detail"`
}

// Metrics 是从 diag.Stats 直接借来的概览指标——eval 不重算。
type Metrics struct {
	CompletedChapters int              `json:"completed_chapters"`
	TotalChapters     int              `json:"total_chapters"`
	TotalWords        int              `json:"total_words"`
	AvgWordsPerChap   int              `json:"avg_words_per_chapter"`
	Phase             string           `json:"phase"`
	Flow              string           `json:"flow"`
	ReviewCount       int              `json:"review_count"`
	RewriteCount      int              `json:"rewrite_count"`
	AvgReviewScore    float64          `json:"avg_review_score"`
	CriticalFindings  int              `json:"critical_findings"`
	WarningFindings   int              `json:"warning_findings"`
	ToolCalls         int              `json:"tool_calls"`
	Usage             UsageMetrics     `json:"usage"`
	RAG               RAGMetrics       `json:"rag"`
	StylestatStatus   string           `json:"stylestat_status,omitempty"`
	Stylestat         *stylestat.Stats `json:"stylestat,omitempty"`
}

type RAGMetrics struct {
	IndexPresent      bool   `json:"index_present"`
	Chunks            int    `json:"chunks"`
	VectorStore       string `json:"vector_store,omitempty"`
	VectorPoints      int    `json:"vector_points,omitempty"`
	Collection        string `json:"collection,omitempty"`
	QdrantURL         string `json:"qdrant_url,omitempty"`
	QdrantHealthy     bool   `json:"qdrant_healthy,omitempty"`
	QdrantPoints      int    `json:"qdrant_points,omitempty"`
	EmbeddingProvider string `json:"embedding_provider,omitempty"`
	EmbeddingModel    string `json:"embedding_model,omitempty"`
}

// Result 是单个 case 的完整评测结果。对齐设计稿三层模型：
// HardFails（阻塞）/ Warnings（回归，WARN）/ Notes（信息性，不影响门禁）。
type Result struct {
	CaseID    string  `json:"case_id"`
	Category  string  `json:"category"`
	Role      string  `json:"role,omitempty"`
	Arm       string  `json:"arm,omitempty"`
	Repeat    int     `json:"repeat,omitempty"`
	Outcome   Outcome `json:"outcome"`
	HardFails []Issue `json:"hard_fails"`
	Warnings  []Issue `json:"warnings"`
	Notes     []Issue `json:"notes,omitempty"`
	Passed    []Issue `json:"passed"`
	Metrics   Metrics `json:"metrics"`
	Dir       string  `json:"dir"`
}

// Grade 把采集结果按 case 契约与 diag Finding 严重度映射成门禁结论。这是 MVP 的核心：
// 确定性证据决定 PASS/WARN/FAIL，不掺主观判断。
func Grade(c Case, col Collected) Result {
	r := Result{
		CaseID:   c.ID,
		Category: c.Category,
		Role:     c.Role,
		Dir:      col.Dir,
		Metrics:  metricsFrom(col),
	}

	// 1. 运行时错误：headless 返回 error 直接 hard fail（失败显式暴露）。
	if col.RuntimeErr != "" {
		r.HardFails = append(r.HardFails, Issue{
			Kind: "hard_fail", Source: "runtime", Detail: "运行时错误: " + col.RuntimeErr,
		})
	}

	// 1b. 工件读取失败：契约依赖的事实读不到，宁可 hard fail 也不 false pass（fail-loud）。
	for _, le := range col.LoadErrors {
		r.HardFails = append(r.HardFails, Issue{
			Kind: "hard_fail", Source: "load", Detail: "工件读取失败: " + le,
		})
	}

	// 2. diag Findings 三层映射（rank 越小越严重）：
	//    超过 max_severity → hard fail；等于 → warning（回归）；低于 → note（信息性，不影响门禁）。
	maxRank := severityRank(c.Gate.MaxSeverity)
	for _, f := range col.Report.Findings {
		sev := string(f.Severity)
		issue := Issue{Source: "finding:" + f.Rule, Severity: sev, Detail: findingDetail(f)}
		switch rank := severityRank(sev); {
		case rank < maxRank:
			issue.Kind = "hard_fail"
			r.HardFails = append(r.HardFails, issue)
		case rank == maxRank:
			issue.Kind = "warning"
			r.Warnings = append(r.Warnings, issue)
		default:
			issue.Kind = "note"
			r.Notes = append(r.Notes, issue)
		}
	}

	// 3. case 契约断言：薄断言，只验本 case 强相关的预期。
	gradeContracts(c, col, &r)

	// 4. 汇总结论。
	switch {
	case len(r.HardFails) > 0:
		r.Outcome = Fail
	case len(r.Warnings) > 0:
		r.Outcome = Warn
	default:
		r.Outcome = Pass
	}
	return r
}

// Delta 描述 variant 相对 baseline 的确定性差异。
type Delta struct {
	Outcome   Outcome      `json:"outcome"`
	HardFails []Issue      `json:"hard_fails,omitempty"`
	Warnings  []Issue      `json:"warnings,omitempty"`
	Notes     []Issue      `json:"notes,omitempty"`
	Metrics   DeltaMetrics `json:"metrics"`
}

type DeltaMetrics struct {
	CompletedChapters     int         `json:"completed_chapters"`
	CriticalFindings      int         `json:"critical_findings"`
	WarningFindings       int         `json:"warning_findings"`
	TotalWordsRatio       float64     `json:"total_words_ratio,omitempty"`
	ToolCallDeltaRatio    float64     `json:"tool_call_delta_ratio,omitempty"`
	CostDeltaRatio        float64     `json:"cost_delta_ratio,omitempty"`
	InputTokenDeltaRatio  float64     `json:"input_token_delta_ratio,omitempty"`
	OutputTokenDeltaRatio float64     `json:"output_token_delta_ratio,omitempty"`
	Stylestat             *StyleDelta `json:"stylestat,omitempty"`
}

type StyleDelta struct {
	Status               string  `json:"status"` // ok / insufficient_sample
	PatternTopPerChapter float64 `json:"pattern_top_per_chapter_delta,omitempty"`
	EndingShortRatio     float64 `json:"ending_short_ratio_delta,omitempty"`
	RepeatedSentences    int     `json:"repeated_sentences_delta,omitempty"`
	TitleMixedDelta      int     `json:"title_mixed_delta,omitempty"`
}

// GradeDelta 只比较确定性事实：variant 比 baseline 是否更差。
func GradeDelta(c Case, baseline, variant Result) Delta {
	d := Delta{Metrics: deltaMetrics(baseline, variant)}

	hardFail := func(source, detail string) {
		d.HardFails = append(d.HardFails, Issue{Kind: "hard_fail", Source: source, Detail: detail})
	}
	warn := func(source, detail string) {
		d.Warnings = append(d.Warnings, Issue{Kind: "warning", Source: source, Detail: detail})
	}
	note := func(source, detail string) {
		d.Notes = append(d.Notes, Issue{Kind: "note", Source: source, Detail: detail})
	}

	if baseline.Outcome == Fail {
		note("baseline", "baseline 已失败，本轮 delta 只能作为参考")
	}
	if variant.Outcome == Fail {
		hardFail("variant", "variant 自身门禁失败")
	}
	if d.Metrics.CriticalFindings > 0 {
		hardFail("delta:critical_findings", fmt.Sprintf("critical findings 增加 %d", d.Metrics.CriticalFindings))
	}
	if variant.Metrics.CompletedChapters < baseline.Metrics.CompletedChapters {
		hardFail("delta:completed_chapters", fmt.Sprintf("完成章节减少：baseline=%d variant=%d",
			baseline.Metrics.CompletedChapters, variant.Metrics.CompletedChapters))
	}
	if d.Metrics.WarningFindings > 0 {
		warn("delta:warning_findings", fmt.Sprintf("warning findings 增加 %d", d.Metrics.WarningFindings))
	}
	if baseline.Metrics.TotalWords > 0 {
		ratio := d.Metrics.TotalWordsRatio
		if ratio > 0 && (ratio < 0.6 || ratio > 1.8) {
			warn("delta:total_words", fmt.Sprintf("总字数比例 %.2f 超出 0.6~1.8", ratio))
		}
	}
	if deltaGateEnabled(c.Gate.MaxToolCallDeltaRatio) && d.Metrics.ToolCallDeltaRatio > *c.Gate.MaxToolCallDeltaRatio {
		warn("delta:tool_calls", fmt.Sprintf("tool calls 增幅 %.1f%% 超过阈值 %.1f%%",
			d.Metrics.ToolCallDeltaRatio*100, *c.Gate.MaxToolCallDeltaRatio*100))
	}
	if deltaGateEnabled(c.Gate.MaxCostDeltaRatio) && d.Metrics.CostDeltaRatio > *c.Gate.MaxCostDeltaRatio {
		warn("delta:cost", fmt.Sprintf("成本增幅 %.1f%% 超过阈值 %.1f%%",
			d.Metrics.CostDeltaRatio*100, *c.Gate.MaxCostDeltaRatio*100))
	}
	if deltaGateEnabled(c.Gate.MaxCostDeltaRatio) && d.Metrics.InputTokenDeltaRatio > *c.Gate.MaxCostDeltaRatio {
		warn("delta:input_tokens", fmt.Sprintf("输入 token 增幅 %.1f%% 超过阈值 %.1f%%",
			d.Metrics.InputTokenDeltaRatio*100, *c.Gate.MaxCostDeltaRatio*100))
	}
	if deltaGateEnabled(c.Gate.MaxCostDeltaRatio) && d.Metrics.OutputTokenDeltaRatio > *c.Gate.MaxCostDeltaRatio {
		warn("delta:output_tokens", fmt.Sprintf("输出 token 增幅 %.1f%% 超过阈值 %.1f%%",
			d.Metrics.OutputTokenDeltaRatio*100, *c.Gate.MaxCostDeltaRatio*100))
	}
	if sd := d.Metrics.Stylestat; sd != nil {
		if sd.Status == "insufficient_sample" {
			note("stylestat", "样本不足，至少 5 章才计算文体回归")
		} else if styleRegressed(sd) {
			issue := Issue{
				Kind:   "warning",
				Source: "delta:stylestat",
				Detail: fmt.Sprintf("文体指标回归：pattern_top %+0.1f，ending_short %+0.2f，repeated %+d，title_mixed %+d",
					sd.PatternTopPerChapter, sd.EndingShortRatio, sd.RepeatedSentences, sd.TitleMixedDelta),
			}
			if c.Gate.StylestatRegression == "block" {
				issue.Kind = "hard_fail"
				d.HardFails = append(d.HardFails, issue)
			} else if c.Gate.StylestatRegression != "off" {
				d.Warnings = append(d.Warnings, issue)
			} else {
				issue.Kind = "note"
				d.Notes = append(d.Notes, issue)
			}
		}
	}

	switch {
	case len(d.HardFails) > 0:
		d.Outcome = Fail
	case len(d.Warnings) > 0:
		d.Outcome = Warn
	default:
		d.Outcome = Pass
	}
	return d
}

func deltaGateEnabled(v *float64) bool {
	return v != nil && *v >= 0
}

func deltaMetrics(baseline, variant Result) DeltaMetrics {
	bm, vm := baseline.Metrics, variant.Metrics
	return DeltaMetrics{
		CompletedChapters:     vm.CompletedChapters - bm.CompletedChapters,
		CriticalFindings:      vm.CriticalFindings - bm.CriticalFindings,
		WarningFindings:       vm.WarningFindings - bm.WarningFindings,
		TotalWordsRatio:       ratio(vm.TotalWords, bm.TotalWords),
		ToolCallDeltaRatio:    deltaRatio(vm.ToolCalls, bm.ToolCalls),
		CostDeltaRatio:        deltaRatioFloat(vm.Usage.CostUSD, bm.Usage.CostUSD),
		InputTokenDeltaRatio:  deltaRatio(vm.Usage.Input, bm.Usage.Input),
		OutputTokenDeltaRatio: deltaRatio(vm.Usage.Output, bm.Usage.Output),
		Stylestat:             compareStyleStats(bm.Stylestat, vm.Stylestat),
	}
}

func compareStyleStats(baseline, variant *stylestat.Stats) *StyleDelta {
	if baseline == nil || variant == nil {
		return &StyleDelta{Status: "insufficient_sample"}
	}
	return &StyleDelta{
		Status:               "ok",
		PatternTopPerChapter: round2(maxPatternPerChapter(variant.Patterns) - maxPatternPerChapter(baseline.Patterns)),
		EndingShortRatio:     round2(variant.Ending.ShortRatio - baseline.Ending.ShortRatio),
		RepeatedSentences:    len(variant.RepeatedSentences) - len(baseline.RepeatedSentences),
		TitleMixedDelta:      titleMixedCount(variant.TitleFormats) - titleMixedCount(baseline.TitleFormats),
	}
}

func styleRegressed(d *StyleDelta) bool {
	const epsilon = 0.0001
	return d.PatternTopPerChapter > epsilon ||
		d.EndingShortRatio > epsilon ||
		d.RepeatedSentences > 0 ||
		d.TitleMixedDelta > 0
}

func maxPatternPerChapter(patterns []stylestat.PatternStat) float64 {
	var maxv float64
	for _, p := range patterns {
		if p.PerChapter > maxv {
			maxv = p.PerChapter
		}
	}
	return maxv
}

func titleMixedCount(t *stylestat.TitleStat) int {
	if t == nil {
		return 0
	}
	if t.WithPrefix < t.WithoutPrefix {
		return t.WithPrefix
	}
	return t.WithoutPrefix
}

func ratio(newValue, base int) float64 {
	if base == 0 {
		return 0
	}
	return round2(float64(newValue) / float64(base))
}

func deltaRatio(newValue, base int) float64 {
	if base == 0 {
		return 0
	}
	return round2((float64(newValue) - float64(base)) / float64(base))
}

func deltaRatioFloat(newValue, base float64) float64 {
	if base == 0 {
		return 0
	}
	return round2((newValue - base) / base)
}

func round2(f float64) float64 {
	if f < 0 {
		return -round2(-f)
	}
	return float64(int(f*100+0.5)) / 100
}

func gradeContracts(c Case, col Collected, r *Result) {
	hardFail := func(source, detail string) {
		r.HardFails = append(r.HardFails, Issue{Kind: "hard_fail", Source: "contract:" + source, Detail: detail})
	}
	pass := func(source, detail string) {
		r.Passed = append(r.Passed, Issue{Kind: "passed", Source: "contract:" + source, Detail: detail})
	}

	e := c.Expect

	if e.Phase != "" {
		got := phaseOf(col)
		if got != e.Phase {
			hardFail("phase", fmt.Sprintf("期望 phase=%s，实际 %s", e.Phase, got))
		} else {
			pass("phase", "phase="+got)
		}
	}

	if e.MinCompletedChapters > 0 {
		got := r.Metrics.CompletedChapters
		if got < e.MinCompletedChapters {
			hardFail("min_completed_chapters", fmt.Sprintf("期望 ≥%d 章，实际 %d 章", e.MinCompletedChapters, got))
		} else {
			pass("min_completed_chapters", fmt.Sprintf("完成 %d 章", got))
		}
	}

	for _, spec := range e.RequiredCheckpoints {
		ok, err := col.HasCheckpoint(spec)
		switch {
		case err != nil:
			hardFail("checkpoint", err.Error())
		case !ok:
			hardFail("checkpoint", "缺少 checkpoint: "+spec)
		default:
			pass("checkpoint", spec)
		}
	}

	for _, sig := range e.NoPending {
		if col.Pending[sig] {
			hardFail("no_pending", "残留信号: "+sig)
		} else {
			pass("no_pending", sig+" 已清空")
		}
	}

	for _, file := range e.RequiredFiles {
		path := filepath.Join(col.Dir, filepath.FromSlash(file))
		info, err := os.Stat(path)
		switch {
		case err != nil:
			hardFail("required_file", fmt.Sprintf("缺少产物 %s: %v", file, err))
		case info.IsDir():
			hardFail("required_file", "期望文件但得到目录: "+file)
		case info.Size() == 0:
			hardFail("required_file", "产物为空: "+file)
		default:
			pass("required_file", file)
		}
	}

	gradeRAGContracts(e, col, hardFail, pass)
}

func gradeRAGContracts(e Expect, col Collected, hardFail, pass func(source, detail string)) {
	if e.MinRAGChunks <= 0 && len(e.RequiredRAGFacets) == 0 && len(e.RequiredRAGSourceKinds) == 0 &&
		len(e.RequiredRAGSources) == 0 && len(e.ForbiddenRAGSources) == 0 && e.VectorStore == "" &&
		e.EmbeddingProvider == "" && e.EmbeddingModel == "" && e.MinVectorPoints <= 0 &&
		!e.RequireQdrant && e.MinQdrantPoints <= 0 {
		return
	}
	state := col.RAG.IndexState
	if state == nil {
		hardFail("rag", "缺少 meta/rag/index_state.json")
		return
	}
	chunks := state.Chunks
	if e.MinRAGChunks > 0 {
		if len(chunks) < e.MinRAGChunks {
			hardFail("rag_chunks", fmt.Sprintf("RAG chunks 期望 ≥%d，实际 %d", e.MinRAGChunks, len(chunks)))
		} else {
			pass("rag_chunks", fmt.Sprintf("chunks=%d", len(chunks)))
		}
	}
	facets, kinds, sources := map[string]int{}, map[string]int{}, map[string]struct{}{}
	for _, chunk := range chunks {
		chunk = rag.NormalizeChunk(chunk)
		facets[chunk.Facet]++
		kinds[chunk.SourceKind]++
		sources[filepath.ToSlash(chunk.SourcePath)] = struct{}{}
		for _, marker := range e.ForbiddenRAGSources {
			if marker != "" && strings.Contains(filepath.ToSlash(chunk.SourcePath), marker) {
				hardFail("rag_forbidden_source", fmt.Sprintf("RAG source_path 命中禁用片段 %q: %s", marker, chunk.SourcePath))
			}
		}
	}
	for _, facet := range e.RequiredRAGFacets {
		if facets[facet] == 0 {
			hardFail("rag_facet", "缺少 RAG facet: "+facet)
		} else {
			pass("rag_facet", fmt.Sprintf("%s=%d", facet, facets[facet]))
		}
	}
	for _, kind := range e.RequiredRAGSourceKinds {
		if kinds[kind] == 0 {
			hardFail("rag_source_kind", "缺少 RAG source_kind: "+kind)
		} else {
			pass("rag_source_kind", fmt.Sprintf("%s=%d", kind, kinds[kind]))
		}
	}
	for _, source := range e.RequiredRAGSources {
		source = filepath.ToSlash(source)
		if _, ok := sources[source]; !ok {
			hardFail("rag_source", "缺少 RAG source_path: "+source)
		} else {
			pass("rag_source", source)
		}
	}

	cfg := state.Config
	if col.RAG.VectorStore != nil {
		cfg = col.RAG.VectorStore.Config
	}
	if e.VectorStore != "" {
		if cfg.VectorStore != e.VectorStore {
			hardFail("vector_store", fmt.Sprintf("期望 vector_store=%s，实际 %s", e.VectorStore, cfg.VectorStore))
		} else {
			pass("vector_store", cfg.VectorStore)
		}
	}
	if e.EmbeddingProvider != "" {
		if cfg.EmbeddingProvider != e.EmbeddingProvider {
			hardFail("embedding_provider", fmt.Sprintf("期望 embedding_provider=%s，实际 %s", e.EmbeddingProvider, cfg.EmbeddingProvider))
		} else {
			pass("embedding_provider", cfg.EmbeddingProvider)
		}
	}
	if e.EmbeddingModel != "" {
		if cfg.EmbeddingModel != e.EmbeddingModel {
			hardFail("embedding_model", fmt.Sprintf("期望 embedding_model=%s，实际 %s", e.EmbeddingModel, cfg.EmbeddingModel))
		} else {
			pass("embedding_model", cfg.EmbeddingModel)
		}
	}
	if e.MinVectorPoints > 0 {
		if col.RAG.VectorStore == nil {
			hardFail("vector_points", "缺少 meta/rag/vector_store.json")
		} else if len(col.RAG.VectorStore.Points) < e.MinVectorPoints {
			hardFail("vector_points", fmt.Sprintf("vector points 期望 ≥%d，实际 %d", e.MinVectorPoints, len(col.RAG.VectorStore.Points)))
		} else {
			pass("vector_points", fmt.Sprintf("points=%d", len(col.RAG.VectorStore.Points)))
		}
	}
	if e.RequireQdrant {
		if !col.RAG.QdrantHealthy {
			detail := "Qdrant 不可用"
			if col.RAG.QdrantError != "" {
				detail += ": " + col.RAG.QdrantError
			}
			hardFail("qdrant", detail)
		} else {
			pass("qdrant", "healthy")
		}
	}
	if e.MinQdrantPoints > 0 {
		if !col.RAG.QdrantHealthy {
			detail := "Qdrant 不可用，无法检查 points"
			if col.RAG.QdrantError != "" {
				detail += ": " + col.RAG.QdrantError
			}
			hardFail("qdrant_points", detail)
		} else if col.RAG.QdrantPoints < e.MinQdrantPoints {
			hardFail("qdrant_points", fmt.Sprintf("Qdrant points 期望 ≥%d，实际 %d", e.MinQdrantPoints, col.RAG.QdrantPoints))
		} else {
			pass("qdrant_points", fmt.Sprintf("points=%d", col.RAG.QdrantPoints))
		}
	}
}

func metricsFrom(col Collected) Metrics {
	rep := col.Report
	m := Metrics{
		CompletedChapters: rep.Stats.CompletedChapters,
		TotalChapters:     rep.Stats.TotalChapters,
		TotalWords:        rep.Stats.TotalWords,
		AvgWordsPerChap:   rep.Stats.AvgWordsPerCh,
		Phase:             rep.Stats.Phase,
		Flow:              rep.Stats.Flow,
		ReviewCount:       rep.Stats.ReviewCount,
		RewriteCount:      rep.Stats.RewriteCount,
		AvgReviewScore:    rep.Stats.AvgReviewScore,
		ToolCalls:         col.ToolCalls,
		Usage:             col.Usage,
		RAG:               ragMetricsFrom(col),
		StylestatStatus:   col.Style.Status,
		Stylestat:         col.Style.Stats,
	}
	for _, f := range rep.Findings {
		switch f.Severity {
		case diag.SevCritical:
			m.CriticalFindings++
		case diag.SevWarning:
			m.WarningFindings++
		}
	}
	return m
}

func ragMetricsFrom(col Collected) RAGMetrics {
	var m RAGMetrics
	if state := col.RAG.IndexState; state != nil {
		m.IndexPresent = true
		m.Chunks = len(state.Chunks)
		m.Collection = state.Config.Collection
		m.VectorStore = state.Config.VectorStore
		m.QdrantURL = state.Config.QdrantURL
		m.EmbeddingProvider = state.Config.EmbeddingProvider
		m.EmbeddingModel = state.Config.EmbeddingModel
	}
	if vs := col.RAG.VectorStore; vs != nil {
		m.VectorPoints = len(vs.Points)
		if vs.Config.Collection != "" {
			m.Collection = vs.Config.Collection
		}
		if vs.Config.VectorStore != "" {
			m.VectorStore = vs.Config.VectorStore
		}
		if vs.Config.QdrantURL != "" {
			m.QdrantURL = vs.Config.QdrantURL
		}
		if vs.Config.EmbeddingProvider != "" {
			m.EmbeddingProvider = vs.Config.EmbeddingProvider
		}
		if vs.Config.EmbeddingModel != "" {
			m.EmbeddingModel = vs.Config.EmbeddingModel
		}
	}
	m.QdrantHealthy = col.RAG.QdrantHealthy
	m.QdrantPoints = col.RAG.QdrantPoints
	return m
}

// phaseOf 优先取 progress 的 phase，回落到 diag.Stats（两者同源）。
func phaseOf(col Collected) string {
	if col.Progress != nil {
		return string(col.Progress.Phase)
	}
	return col.Report.Stats.Phase
}

func findingDetail(f diag.Finding) string {
	if f.Evidence != "" {
		return f.Title + "（" + f.Evidence + "）"
	}
	return f.Title
}

// ── 严重度 ─────────────────────────────────────────────

var severityRanks = map[string]int{"critical": 0, "warning": 1, "info": 2}

func validSeverity(s string) bool {
	_, ok := severityRanks[s]
	return ok
}

// severityRank 越小越严重；未知严重度按最不严重处理，避免误判 hard fail。
func severityRank(s string) int {
	if r, ok := severityRanks[s]; ok {
		return r
	}
	return 99
}
