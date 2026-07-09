package reviewreport

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/chenhongyang/novel-studio/internal/aigc"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/rules"
)

// MechanicalGatePayload is the machine-readable result written by commit_chapter.
type MechanicalGatePayload struct {
	Chapter        int               `json:"chapter"`
	AIGCReport     aigc.Report       `json:"aigc_report"`
	RuleViolations []rules.Violation `json:"rule_violations"`
	GeneratedAt    string            `json:"generated_at,omitempty"`
}

type UnifiedMarkdownInput struct {
	Chapter         int
	GeneratedAt     string
	Mechanical      *MechanicalGatePayload
	AIVoice         *domain.AIVoiceAnalysis
	ExternalAIJudge *ExternalAIJudge
	Editor          *domain.ReviewEntry
	EditorMarkdown  string
}

type ExternalAIJudge struct {
	Name                 string
	Source               string
	Verdict              string
	RiskLevel            string
	AIProbabilityPercent int
	Blocking             bool
	Summary              string
}

func WriteUnifiedMarkdown(root string, in UnifiedMarkdownInput) error {
	if in.Chapter <= 0 {
		return fmt.Errorf("chapter must be > 0")
	}
	dir := filepath.Join(root, "reviews")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, fmt.Sprintf("%02d.md", in.Chapter)), []byte(RenderUnifiedMarkdown(in)), 0o644)
}

func LoadMechanicalGate(root string, chapter int) (*MechanicalGatePayload, string, error) {
	if chapter <= 0 {
		return nil, "", nil
	}
	for _, rel := range []string{
		fmt.Sprintf("reviews/%02d_ai_gate.json", chapter),
		fmt.Sprintf("reviews_ai/%02d.json", chapter),
	} {
		var payload MechanicalGatePayload
		raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, rel, err
		}
		if err := json.Unmarshal(raw, &payload); err != nil {
			return nil, rel, err
		}
		return &payload, rel, nil
	}
	return nil, "", nil
}

func RemoveLegacyMarkdown(root string, chapter int) error {
	if chapter <= 0 {
		return nil
	}
	paths := []string{
		filepath.Join(root, "reviews", fmt.Sprintf("第%03d章_AI味审核.md", chapter)),
	}
	for _, path := range paths {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func RenderUnifiedMarkdown(in UnifiedMarkdownInput) string {
	chapter := in.Chapter
	if chapter <= 0 && in.Editor != nil {
		chapter = in.Editor.Chapter
	}
	if chapter <= 0 && in.Mechanical != nil {
		chapter = in.Mechanical.Chapter
	}
	generatedAt := strings.TrimSpace(in.GeneratedAt)
	if generatedAt == "" {
		generatedAt = time.Now().Format(time.RFC3339)
	}

	mechanicalStatus := mechanicalGateStatus(in.Mechanical)
	if AcceptedWarningOnlyGate(in.Mechanical, in.AIVoice, in.Editor) && mechanicalStatus == "未通过" {
		mechanicalStatus = "有警告"
	}
	editorStatus := editorStatus(in.Editor)
	externalStatus := externalAIJudgeStatus(in.ExternalAIJudge)
	needRewrite := unifiedNeedRewrite(in.Mechanical, in.AIVoice, in.ExternalAIJudge, in.Editor)
	diagnosis := unifiedDiagnosis(in)

	var b strings.Builder
	fmt.Fprintf(&b, "# 第%03d章 统一审核\n\n", chapter)
	fmt.Fprintf(&b, "> 生成时间：%s\n", generatedAt)
	b.WriteString("> 文件口径：本文件汇总机械门禁、AI 味信号和 Editor 复审；结构化事实保留在同目录 JSON。\n\n")

	fmt.Fprintf(&b, "## 总体评分：%s\n", editorTotalScore(in.Editor))
	fmt.Fprintf(&b, "## 是否需要改写：%s\n", needRewrite)
	fmt.Fprintf(&b, "## 一句话诊断：%s\n\n", diagnosis)

	b.WriteString("## 门禁结论\n\n")
	fmt.Fprintf(&b, "- 机械门禁：%s\n", mechanicalStatus)
	if in.ExternalAIJudge != nil {
		fmt.Fprintf(&b, "- %s：%s\n", externalAIJudgeName(in.ExternalAIJudge), externalStatus)
	}
	fmt.Fprintf(&b, "- Editor 复审：%s\n", editorStatus)
	fmt.Fprintf(&b, "- 统一结论：%s\n", unifiedConclusion(in.Mechanical, in.AIVoice, in.ExternalAIJudge, in.Editor))
	if in.Editor != nil && len(in.Editor.AffectedChapters) > 0 {
		fmt.Fprintf(&b, "- 受影响章节：%s\n", ints(in.Editor.AffectedChapters))
	}

	b.WriteString("\n## 机械门禁\n\n")
	renderMechanicalGate(&b, in.Mechanical)

	b.WriteString("\n## AI 味信号\n\n")
	renderAIVoice(&b, in.AIVoice)

	b.WriteString("\n## Editor 复审\n\n")
	renderEditorReview(&b, in.Editor)

	b.WriteString("\n## 主要问题（按严重度排序）\n\n")
	issues := unifiedIssues(in)
	writeListOrDash(&b, issues)

	if strings.TrimSpace(in.EditorMarkdown) != "" && len(issues) > 0 {
		b.WriteString("\n## Editor 原始报告摘录\n\n")
		b.WriteString(truncateRunes(stripEditorAuxiliarySections(strings.TrimSpace(in.EditorMarkdown), len(issues) == 0), 2200))
		b.WriteString("\n")
	}

	b.WriteString("\n## 结论\n\n")
	fmt.Fprintf(&b, "- %s\n", unifiedConclusion(in.Mechanical, in.AIVoice, in.ExternalAIJudge, in.Editor))
	return b.String()
}

func mechanicalGateStatus(payload *MechanicalGatePayload) string {
	if payload == nil {
		return "待生成"
	}
	if HasBlockingMechanicalGate(payload) {
		return "未通过"
	}
	if len(payload.RuleViolations) > 0 {
		return "有警告"
	}
	return "通过"
}

func editorStatus(entry *domain.ReviewEntry) string {
	if entry == nil {
		return "待复审"
	}
	switch entry.Verdict {
	case "rewrite":
		return "需重写"
	case "polish":
		return "需打磨"
	case "accept":
		if hasEditorOpenIssues(entry) {
			return "通过（有主要问题）"
		}
		return "通过"
	default:
		return entry.Verdict
	}
}

func unifiedNeedRewrite(mechanical *MechanicalGatePayload, aiVoice *domain.AIVoiceAnalysis, external *ExternalAIJudge, editor *domain.ReviewEntry) string {
	if hasBlockingExternalAIJudge(external) {
		return "是"
	}
	if AcceptedWarningOnlyGate(mechanical, aiVoice, editor) {
		return "可选"
	}
	if HasBlockingMechanicalGate(mechanical) {
		return "是"
	}
	if HasBlockingAIVoice(aiVoice) {
		return "是"
	}
	if editor != nil {
		switch editor.Verdict {
		case "rewrite":
			return "是"
		case "polish":
			return "可选"
		case "accept":
			if mechanical != nil && len(mechanical.RuleViolations) > 0 {
				return "可选"
			}
			if hasEditorOpenIssues(editor) {
				return "可选"
			}
			return "否"
		}
	}
	if mechanical != nil && len(mechanical.RuleViolations) > 0 {
		return "是"
	}
	return "待定"
}

func unifiedConclusion(mechanical *MechanicalGatePayload, aiVoice *domain.AIVoiceAnalysis, external *ExternalAIJudge, editor *domain.ReviewEntry) string {
	if hasBlockingExternalAIJudge(external) {
		return fmt.Sprintf("%s 阻断重写；Writer 必须先按裸正文 AI 判定修复整章节奏、对白功能化、动作标签和作者声口后再复审。", externalAIJudgeName(external))
	}
	if AcceptedWarningOnlyGate(mechanical, aiVoice, editor) {
		return "Editor 已通过，机械/AI voice/Editor 仅剩 warning；本章不能称为完全通过，交付前需清空主要问题，是否进入下一章由当前批处理策略决定。"
	}
	aiMechanicalBlocked := HasBlockingAIMechanicalGate(mechanical)
	contractBlocked := HasBlockingContractMechanicalGate(mechanical)
	if aiMechanicalBlocked && contractBlocked {
		return "AI味/节奏机械门禁与篇幅/硬性规则均未通过，进入返工队列；Writer 必须同时修复 AI 味、结构节奏、篇幅合同和项目硬禁项后再提交复审。"
	}
	if aiMechanicalBlocked {
		return "AI味/节奏机械门禁未通过，进入返工队列；Writer 必须先修复 AI 味、段首/短句、动作/物件响应或高风险维度后再提交复审。"
	}
	if contractBlocked {
		return "篇幅/硬性规则机械门禁未通过，进入返工队列；这不是 AI 味结论。Writer 应先判断是否必须满足本章篇幅合同，确需压缩时只做局部删减和合并，不要整章重写。"
	}
	if HasBlockingAIVoice(aiVoice) {
		return "AI味信号未通过，进入返工队列；Writer 必须先修复 AI 味红/黄旗后再提交复审。"
	}
	if editor != nil {
		switch editor.Verdict {
		case "rewrite":
			return "Editor 判定重写；Writer 必须读取本报告建议完成重写，再重新提交审核。"
		case "polish":
			return "Editor 判定打磨；Writer 应按建议优化后重新审核。"
		case "accept":
			if mechanical != nil && len(mechanical.RuleViolations) > 0 {
				return "Editor 已通过，但机械门禁仍有警告；本章不能称为完全通过，交付前需清空主要问题。"
			}
			if hasEditorOpenIssues(editor) {
				return "Editor 已通过，但主要问题仍未清空；交付前继续打磨到主要问题为空。"
			}
			return "机械门禁、AI voice 与 Editor 复审均通过，主要问题已清空，可进入下一章。"
		}
	}
	return "机械门禁已生成，等待 Editor 章级复审。"
}

func unifiedDiagnosis(in UnifiedMarkdownInput) string {
	if hasBlockingExternalAIJudge(in.ExternalAIJudge) {
		if strings.TrimSpace(in.ExternalAIJudge.Summary) != "" {
			return strings.TrimSpace(in.ExternalAIJudge.Summary)
		}
		return externalAIJudgeName(in.ExternalAIJudge) + " 判定存在阻断项，当前章不能直接放行。"
	}
	if AcceptedWarningOnlyGate(in.Mechanical, in.AIVoice, in.Editor) {
		if in.Editor != nil && strings.TrimSpace(in.Editor.Summary) != "" {
			return strings.TrimSpace(in.Editor.Summary) + "；仍有主要问题未清空，非完全通过。"
		}
		return "Editor 已通过，机械/AI voice/Editor 仅剩可选打磨项；仍有主要问题未清空，非完全通过。"
	}
	if HasBlockingAIMechanicalGate(in.Mechanical) {
		return "AI味、段首/短句、动作/物件响应或高风险维度存在阻断项，当前章不能直接放行。"
	}
	if HasBlockingContractMechanicalGate(in.Mechanical) {
		return "篇幅或硬性规则存在阻断项；当前章不是 AI 味未过，应按发布口径决定是否局部压缩后复审。"
	}
	if HasBlockingAIVoice(in.AIVoice) {
		return "AI味红/黄旗存在阻断项，当前章不能直接放行。"
	}
	if in.Editor != nil && strings.TrimSpace(in.Editor.Summary) != "" {
		if hasEditorOpenIssues(in.Editor) {
			return strings.TrimSpace(in.Editor.Summary) + "；主要问题仍需清空后交付。"
		}
		return strings.TrimSpace(in.Editor.Summary)
	}
	if in.AIVoice != nil && strings.TrimSpace(in.AIVoice.Summary) != "" {
		return strings.TrimSpace(in.AIVoice.Summary)
	}
	if in.Mechanical != nil {
		return "机械门禁已完成，等待 Editor 给出结构和审美复审。"
	}
	return "尚未生成审核结果。"
}

func externalAIJudgeStatus(judge *ExternalAIJudge) string {
	if judge == nil {
		return "待生成"
	}
	status := "通过"
	if judge.Blocking {
		status = "阻断"
	}
	parts := []string{status}
	if judge.Verdict != "" || judge.RiskLevel != "" || judge.AIProbabilityPercent > 0 {
		parts = append(parts, fmt.Sprintf("%s/%s/%d%%", emptyDash(judge.Verdict), emptyDash(judge.RiskLevel), judge.AIProbabilityPercent))
	}
	return strings.Join(parts, " ")
}

func externalAIJudgeName(judge *ExternalAIJudge) string {
	if judge == nil || strings.TrimSpace(judge.Name) == "" {
		return "外部 AI 判定"
	}
	return strings.TrimSpace(judge.Name)
}

func hasBlockingExternalAIJudge(judge *ExternalAIJudge) bool {
	return judge != nil && judge.Blocking
}

func hasEditorOpenIssues(entry *domain.ReviewEntry) bool {
	if entry == nil {
		return false
	}
	for _, issue := range entry.Issues {
		if isEditorOpenIssue(entry, issue) {
			return true
		}
	}
	return false
}

func isEditorOpenIssue(entry *domain.ReviewEntry, issue domain.ConsistencyIssue) bool {
	if strings.TrimSpace(issue.Description) == "" &&
		strings.TrimSpace(issue.Evidence) == "" &&
		strings.TrimSpace(issue.Type) == "" {
		return false
	}
	switch strings.TrimSpace(issue.Severity) {
	case "critical", "error":
		return true
	case "warning":
		return entry == nil || strings.TrimSpace(entry.Verdict) != "accept"
	default:
		return false
	}
}

func editorTotalScore(entry *domain.ReviewEntry) string {
	if entry == nil || len(entry.Dimensions) == 0 {
		return "待复审"
	}
	total := 0
	for _, dim := range entry.Dimensions {
		score := dim.Score
		if score < 0 {
			score = 0
		}
		if score > 100 {
			score = 100
		}
		total += score
	}
	return fmt.Sprintf("%d / %d", total, len(entry.Dimensions)*100)
}

func renderMechanicalGate(b *strings.Builder, payload *MechanicalGatePayload) {
	if payload == nil {
		b.WriteString("- 待生成。\n")
		return
	}
	report := payload.AIGCReport
	fmt.Fprintf(b, "- 引擎：%s\n", emptyDash(report.Engine))
	fmt.Fprintf(b, "- AI 占比：%.2f%%\n", report.AIRatioPercent)
	fmt.Fprintf(b, "- 门禁采用值：%.2f%%\n", aigc.EffectiveGatePercent(report))
	fmt.Fprintf(b, "- 融合值：%.2f%%\n", report.BlendedAIGCPercent)
	fmt.Fprintf(b, "- 朱雀分片风险下限：%.2f%%\n", report.SegmentRiskFloor)
	fmt.Fprintf(b, "- 风险标签：%s｜置信度：%s\n", emptyDash(report.RiskLabel), emptyDash(report.Confidence))
	if len(payload.RuleViolations) > 0 {
		b.WriteString("\n### 规则命中\n\n")
		for _, violation := range payload.RuleViolations {
			fmt.Fprintf(b, "- %s｜%s｜actual=%v", violation.Severity, violation.Rule, violation.Actual)
			if rules.HasLimitValue(violation.Limit) {
				fmt.Fprintf(b, "｜limit=%s", rules.FormatLimitValue(violation.Limit))
			}
			if violation.Target != "" {
				fmt.Fprintf(b, "｜target=%s", violation.Target)
			}
			b.WriteByte('\n')
		}
	}
	dims := sortedDimensions(report.Dimensions)
	if len(dims) > 0 {
		b.WriteString("\n### 高风险维度\n\n")
		for _, dim := range dims[:min(len(dims), 4)] {
			fmt.Fprintf(b, "- %.2f%%｜%s\n", dim.Score, dim.Name)
			for _, sig := range dim.Signals[:min(len(dim.Signals), 2)] {
				fmt.Fprintf(b, "  - %s：%s\n", sig.Name, sig.Evidence)
			}
		}
	}
}

func renderAIVoice(b *strings.Builder, analysis *domain.AIVoiceAnalysis) {
	if analysis == nil {
		b.WriteString("- 待生成或无红旗。\n")
		return
	}
	metrics := analysis.Metrics
	fmt.Fprintf(b, "- 结论：%s\n", emptyDash(analysis.Label))
	fmt.Fprintf(b, "- AI 腔风险分：%.4f\n", metrics.AIVoiceScore)
	fmt.Fprintf(b, "- 对话占比：%.2f%%；比喻密度：%.2f/千字\n", metrics.DialogueRatio*100, metrics.FigurativeDensity)
	if len(analysis.RedFlags) > 0 {
		b.WriteString("- 红旗：\n")
		for _, flag := range analysis.RedFlags[:min(len(analysis.RedFlags), 8)] {
			line := strings.TrimSpace(flag.Rule)
			if flag.Evidence != "" {
				line += "｜" + flag.Evidence
			}
			fmt.Fprintf(b, "  - %s｜%s\n", flag.Severity, line)
		}
	}
}

func renderEditorReview(b *strings.Builder, entry *domain.ReviewEntry) {
	if entry == nil {
		b.WriteString("- 待复审。\n")
		return
	}
	fmt.Fprintf(b, "- verdict：%s\n", entry.Verdict)
	fmt.Fprintf(b, "- contract：%s\n", emptyDash(entry.ContractStatus))
	if strings.TrimSpace(entry.ContractNotes) != "" {
		fmt.Fprintf(b, "- contract notes：%s\n", entry.ContractNotes)
	}
	if len(entry.ContractMisses) > 0 {
		b.WriteString("- contract misses：\n")
		for _, miss := range entry.ContractMisses {
			fmt.Fprintf(b, "  - %s\n", miss)
		}
	}
	if len(entry.Dimensions) > 0 {
		b.WriteString("\n| 维度 | 分 | 结论 | 摘要 |\n|---|---:|---|---|\n")
		for _, dim := range entry.Dimensions {
			fmt.Fprintf(b, "| %s | %d | %s | %s |\n", dim.Dimension, dim.Score, dim.Verdict, sanitizeTable(dim.Comment))
		}
	}
}

func unifiedIssues(in UnifiedMarkdownInput) []string {
	var out []string
	if in.Mechanical != nil {
		for _, violation := range in.Mechanical.RuleViolations {
			label := fmt.Sprintf("机械门禁 %s｜%s actual=%v", violation.Severity, violation.Rule, violation.Actual)
			if violation.Target != "" {
				label += "｜" + violation.Target
			}
			out = append(out, label)
		}
		for _, reason := range BlockingAIGCDimensionReasons(in.Mechanical.AIGCReport) {
			out = append(out, "机械门禁 error｜"+reason)
		}
	}
	if in.AIVoice != nil {
		for _, flag := range in.AIVoice.RedFlags {
			label := fmt.Sprintf("AI %s｜%s", flag.Severity, flag.Rule)
			if flag.Evidence != "" {
				label += "｜" + flag.Evidence
			}
			out = append(out, label)
		}
	}
	if hasBlockingExternalAIJudge(in.ExternalAIJudge) {
		label := externalAIJudgeName(in.ExternalAIJudge) + " 阻断"
		if in.ExternalAIJudge.Verdict != "" || in.ExternalAIJudge.RiskLevel != "" || in.ExternalAIJudge.AIProbabilityPercent > 0 {
			label += fmt.Sprintf("｜%s/%s/%d%%", emptyDash(in.ExternalAIJudge.Verdict), emptyDash(in.ExternalAIJudge.RiskLevel), in.ExternalAIJudge.AIProbabilityPercent)
		}
		if strings.TrimSpace(in.ExternalAIJudge.Summary) != "" {
			label += "｜" + strings.TrimSpace(in.ExternalAIJudge.Summary)
		}
		out = append(out, label)
	}
	if in.Editor != nil {
		for _, issue := range in.Editor.Issues {
			if !isEditorOpenIssue(in.Editor, issue) {
				continue
			}
			label := fmt.Sprintf("Editor %s｜%s", issue.Severity, issue.Description)
			if issue.Evidence != "" {
				label += "｜证据：" + issue.Evidence
			}
			out = append(out, label)
		}
	}
	return uniqueStrings(out, 18)
}

func sortedDimensions(dimensions map[string]aigc.Dimension) []aigc.Dimension {
	out := make([]aigc.Dimension, 0, len(dimensions))
	for _, dim := range dimensions {
		out = append(out, dim)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score == out[j].Score {
			return out[i].Name < out[j].Name
		}
		return out[i].Score > out[j].Score
	})
	return out
}

func writeListOrDash(b *strings.Builder, values []string) {
	if len(values) == 0 {
		b.WriteString("- 暂无。\n")
		return
	}
	for _, value := range values {
		fmt.Fprintf(b, "- %s\n", value)
	}
}

func uniqueStrings(values []string, limit int) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || value == "—" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

func ints(values []int) string {
	if len(values) == 0 {
		return "—"
	}
	parts := make([]string, 0, len(values))
	for _, value := range values {
		parts = append(parts, fmt.Sprintf("%d", value))
	}
	return strings.Join(parts, ", ")
}

func emptyDash(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "—"
	}
	return value
}

func sanitizeTable(value string) string {
	value = strings.ReplaceAll(value, "\n", " ")
	value = strings.ReplaceAll(value, "|", "｜")
	return truncateRunes(value, 160)
}

func truncateRunes(value string, limit int) string {
	runes := []rune(value)
	if limit <= 0 || len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "..."
}

func stripRewriteSuggestionsSection(md string) string {
	return stripMarkdownSection(md, "## 改写建议")
}

func stripEditorAuxiliarySections(md string, stripMainIssues bool) string {
	md = stripRewriteSuggestionsSection(md)
	if stripMainIssues {
		md = stripMarkdownSection(md, "## 主要问题")
	}
	return strings.TrimSpace(md)
}

func stripMarkdownSection(md string, headingPrefix string) string {
	lines := strings.Split(md, "\n")
	out := make([]string, 0, len(lines))
	skip := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, headingPrefix) {
			skip = true
			continue
		}
		if skip && strings.HasPrefix(trimmed, "## ") {
			skip = false
		}
		if skip {
			continue
		}
		out = append(out, line)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}
