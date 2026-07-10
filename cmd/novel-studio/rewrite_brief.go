package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/domain"
	editrules "github.com/chenhongyang/novel-studio/internal/editor/rules"
	"github.com/chenhongyang/novel-studio/internal/reviewreport"
	"github.com/chenhongyang/novel-studio/internal/rules"
)

type revisionPlan struct {
	Chapter       int
	HasRed        bool
	HasYellow     bool
	RedReasons    []string
	YellowReasons []string
	Suggestions   []string
	Sources       []string
	Brief         string
}

func buildRevisionPlan(projectDir string, chapter int, chapterText string, reviewMarkdown string) revisionPlan {
	plan := revisionPlan{Chapter: chapter}
	addSource := func(source string) {
		plan.Sources = appendUnique(plan.Sources, source)
	}
	addRed := func(reason string) {
		reason = strings.TrimSpace(reason)
		if reason == "" {
			return
		}
		plan.HasRed = true
		plan.RedReasons = appendUnique(plan.RedReasons, reason)
	}
	addYellow := func(reason string) {
		reason = strings.TrimSpace(reason)
		if reason == "" {
			return
		}
		plan.HasYellow = true
		plan.YellowReasons = appendUnique(plan.YellowReasons, reason)
	}
	addSuggestion := func(suggestion string) {
		suggestion = strings.TrimSpace(suggestion)
		if suggestion == "" || suggestion == "—" {
			return
		}
		plan.Suggestions = appendUniqueLimit(plan.Suggestions, suggestion, 32)
	}

	if strings.TrimSpace(chapterText) != "" {
		addSource("current_chapter_text")
		analysis := editrules.AnalyzeChapter(chapter, chapterText, nil)
		addAIVoiceAnalysisToPlan(analysis, addRed, addYellow, addSuggestion)
	}

	if reviewMarkdown == "" {
		reviewMarkdown = readTextIfExists(filepath.Join(projectDir, "reviews", fmt.Sprintf("%02d.md", chapter)))
	}
	if strings.TrimSpace(reviewMarkdown) != "" {
		addSource(fmt.Sprintf("reviews/%02d.md", chapter))
		if reviewVerdictFromMarkdown(reviewMarkdown, parseReviewDimensions(reviewMarkdown)) == "rewrite" {
			addRed("Markdown 评审要求 rewrite")
		} else if strings.Contains(extractLine(reviewMarkdown, "## 是否需要改写"), "是") {
			addYellow("Markdown 评审建议改写/打磨")
		}
		for _, item := range sectionListItems(reviewMarkdown, "## 主要问题") {
			addSuggestion(item)
		}
		for _, item := range sectionListItems(reviewMarkdown, "## 改写建议") {
			addSuggestion(item)
		}
	}

	var acceptedReview *domain.ReviewEntry
	var reviewEntry domain.ReviewEntry
	if readJSONIfExists(filepath.Join(projectDir, "reviews", fmt.Sprintf("%02d.json", chapter)), &reviewEntry) {
		acceptedReview = &reviewEntry
		addSource(fmt.Sprintf("reviews/%02d.json", chapter))
		switch reviewEntry.Verdict {
		case "rewrite":
			addRed("结构化评审 verdict=rewrite")
		case "polish":
			addYellow("结构化评审 verdict=polish")
		}
		for _, issue := range reviewEntry.Issues {
			label := issue.Description
			if label == "" {
				label = issue.Type
			}
			if reviewEntry.Verdict == "accept" && issue.Severity == "warning" {
				if !isNonActionableAcceptedIssue(reviewEntry, issue) {
					addSuggestion("Editor accept 观察: " + label)
				}
			} else {
				switch issue.Severity {
				case "critical", "error":
					addRed(fmt.Sprintf("结构化 issue %s: %s", issue.Severity, label))
				case "warning":
					addYellow(fmt.Sprintf("结构化 issue warning: %s", label))
				}
			}
			addSuggestion(issue.Suggestion)
		}
		for _, dim := range reviewEntry.Dimensions {
			switch dim.Verdict {
			case "fail":
				addRed(fmt.Sprintf("八维失败 %s(%d): %s", dim.Dimension, dim.Score, dim.Comment))
			case "warning":
				if reviewEntry.Verdict == "accept" {
					addSuggestion(fmt.Sprintf("Editor accept 观察 %s(%d): %s", dim.Dimension, dim.Score, dim.Comment))
				} else {
					addYellow(fmt.Sprintf("八维警告 %s(%d): %s", dim.Dimension, dim.Score, dim.Comment))
				}
			}
		}
	}

	var aiVoiceAnalysis *domain.AIVoiceAnalysis
	var persisted domain.AIVoiceAnalysis
	for _, rel := range []string{
		fmt.Sprintf("reviews/%02d_ai_voice_redflags.json", chapter),
		fmt.Sprintf("reviews_ai/%02d_ai_voice_redflags.json", chapter),
	} {
		if readJSONIfExists(filepath.Join(projectDir, filepath.FromSlash(rel)), &persisted) {
			aiVoiceAnalysis = &persisted
			addSource(rel)
			addAIVoiceAnalysisToPlan(persisted, addRed, addYellow, addSuggestion)
			break
		}
	}

	var mechanicalPayload *reviewreport.MechanicalGatePayload
	var aigcPayload reviewreport.MechanicalGatePayload
	for _, rel := range []string{
		fmt.Sprintf("reviews/%02d_ai_gate.json", chapter),
		fmt.Sprintf("reviews_ai/%02d.json", chapter),
	} {
		if readJSONIfExists(filepath.Join(projectDir, filepath.FromSlash(rel)), &aigcPayload) {
			mechanicalPayload = &aigcPayload
			addSource(rel)
			for _, violation := range aigcPayload.RuleViolations {
				label := mechanicalViolationBriefLabel(violation)
				if suggestion := mechanicalRewriteBriefSuggestion(violation.Rule); suggestion != "" {
					addSuggestion(suggestion)
				}
				switch violation.Severity {
				case rules.SeverityError:
					if strings.TrimSpace(violation.Rule) == "chapter_words" {
						addRed("篇幅合同 error: " + label)
					} else {
						addRed("机械门禁 error: " + label)
					}
				case rules.SeverityWarning:
					if reviewreport.IsBlockingMechanicalViolation(violation) {
						addRed("机械门禁阻断 warning: " + label)
					} else {
						addYellow("机械门禁 warning: " + label)
					}
				}
			}
			for _, reason := range reviewreport.BlockingAIGCDimensionReasons(aigcPayload.AIGCReport) {
				addRed("AI高风险维度阻断: " + reason)
			}
			break
		}
	}

	plan = downgradeAcceptedWarningOnlyPlan(plan, acceptedReview, mechanicalPayload, aiVoiceAnalysis)
	addDeepSeekAIJudgeToPlan(projectDir, chapter, &plan, addSource, addRed, addYellow, addSuggestion)
	plan.Brief = renderRevisionBrief(plan, reviewMarkdown)
	return plan
}

func addDeepSeekAIJudgeToPlan(
	projectDir string,
	chapter int,
	plan *revisionPlan,
	addSource func(string),
	addRed func(string),
	addYellow func(string),
	addSuggestion func(string),
) {
	var artifact deepseekAIJudgeArtifact
	rel := fmt.Sprintf("reviews/%02d_deepseek_ai_judge.json", chapter)
	if !readJSONIfExists(filepath.Join(projectDir, filepath.FromSlash(rel)), &artifact) {
		mdRel := fmt.Sprintf("reviews/%02d_deepseek_ai_judge.md", chapter)
		md := readTextIfExists(filepath.Join(projectDir, filepath.FromSlash(mdRel)))
		if strings.TrimSpace(md) == "" {
			return
		}
		addSource(mdRel)
		addYellow("DeepSeek 裸正文 AI 判定存在 Markdown 结果但 JSON 不可解析，需人工复核")
		for _, item := range sectionListItems(md, "### 修改方案") {
			addSuggestion("DeepSeek 修改方案: " + item)
		}
		for _, item := range sectionListItems(md, "### 对白专项修改方案") {
			addSuggestion("DeepSeek 对白修复: " + item)
		}
		return
	}
	addSource(rel)
	label := fmt.Sprintf("DeepSeek 裸正文 AI 判定: verdict=%s risk=%s ai=%d%% model=%s/%s", artifact.Verdict, artifact.RiskLevel, artifact.AIProbabilityPercent, artifact.Provider, artifact.Model)
	if artifact.Blocking {
		addRed(label)
	} else if artifact.AIProbabilityPercent >= 15 || artifact.RiskLevel == "medium" {
		addYellow(label)
	} else if deepSeekJudgeHasActionableLowRiskPolish(artifact) {
		addYellow("DeepSeek 裸正文 AI 判定给出非阻断修改方案，按黄旗择优打磨")
	}
	if strings.TrimSpace(artifact.ParseWarning) != "" {
		addYellow("DeepSeek 判定配置/解析提示: " + artifact.ParseWarning)
	}
	for _, reason := range artifact.Reasons {
		addSuggestion("DeepSeek AI味原因: " + reason)
	}
	for _, suggestion := range artifact.RevisionPlan {
		addSuggestion("DeepSeek 修改方案: " + suggestion)
	}
	for _, suggestion := range artifact.DialogueFixPlan {
		addSuggestion("DeepSeek 对白修复: " + suggestion)
	}
	for _, suggestion := range artifact.AuthorVoicePlan {
		addSuggestion("DeepSeek 作者声口: " + suggestion)
	}
	for _, rule := range artifact.RAGRules {
		addSuggestion("DeepSeek 后续规避规则: " + rule)
	}
}

func deepSeekJudgeHasActionableLowRiskPolish(artifact deepseekAIJudgeArtifact) bool {
	if artifact.AIProbabilityPercent < 10 || artifact.RiskLevel == "low" || artifact.Verdict == "human_like" {
		return false
	}
	return len(artifact.RevisionPlan) > 0 || len(artifact.DialogueFixPlan) > 0 || len(artifact.AuthorVoicePlan) > 0
}

func isNonActionableAcceptedIssue(entry domain.ReviewEntry, issue domain.ConsistencyIssue) bool {
	if entry.Verdict != "accept" || issue.Severity != "warning" {
		return false
	}
	text := strings.TrimSpace(strings.Join([]string{issue.Description, issue.Evidence, issue.Suggestion}, " "))
	if text == "" {
		return true
	}
	if strings.Contains(text, "无其他问题") || strings.Contains(text, "暂无问题") || strings.Contains(text, "无突出问题") {
		return true
	}
	if acceptedIssueIsReviewCalibration(text) {
		return true
	}
	if acceptedIssueIsFutureOnly(text) {
		return true
	}
	if !(strings.Contains(text, "无需修改") || strings.Contains(text, "不需要修改") || strings.Contains(text, "无需回头")) {
		return false
	}
	return strings.Contains(text, "无严重问题") ||
		strings.Contains(text, "微小") ||
		strings.Contains(text, "微瑕") ||
		strings.Contains(text, "未达到") ||
		strings.Contains(text, "低风险") ||
		strings.Contains(text, "当前版本")
}

func acceptedIssueIsReviewCalibration(text string) bool {
	return strings.Contains(text, "定位错位") ||
		strings.Contains(text, "评分体系失真") ||
		strings.Contains(text, "评审口径校准") ||
		strings.Contains(text, "评审标尺错位")
}

func acceptedIssueIsFutureOnly(text string) bool {
	if !(strings.Contains(text, "后续") || strings.Contains(text, "下一章")) {
		return false
	}
	return strings.Contains(text, "预防") ||
		strings.Contains(text, "若高频") ||
		strings.Contains(text, "后续类似") ||
		strings.Contains(text, "后续章节") ||
		strings.Contains(text, "在下一章")
}

func mechanicalRewriteBriefSuggestion(rule string) string {
	switch strings.TrimSpace(rule) {
	case "aigc_ratio":
		return "AI率/segment_risk_floor 超标时按整章单检测片段重排：先删“第一/第二/第三”“不是A而是B”等结构标记，合并孤句段，打散连续同功能段落，用对话阻力、现场误判、延迟/缺席的物件响应和具体规则后果替换解释性总结。"
	case "chapter_words":
		return "篇幅超标只做局部压缩：优先删重复规则说明、重复互动问答和同义情绪句；保留已成立的场景、规则链、钩子和人物声口，不要整章重写。"
	case "dangling_order_word":
		return "删掉悬空的顺序词/阶段词（第一组、第二组、第三组、先、再、最后等）；改成角色动作触发或现场打断，让事件自然递进。"
	case "micro_action_overuse":
		return "删掉只负责停顿的微动作；保留会改变权限、关系、证据或空间位置的动作，其余交给对话、沉默或后果承接。"
	case "dramatic_negation_overuse":
		return "减少“没有立刻/没急着/没有顺着”等否定声明，直接写角色做了什么、避开了什么或把话题推向哪里。"
	case "state_clause_pile":
		return "状态说明堆叠要拆成动作链：删掉同句多个“还/没/已经”，让人物动作、页面变化和沉默分别承载信息。"
	case "not_but_overuse":
		return "“不是A而是B”每章最多留一处；多余句子改成普通判断、动作后果或人物误读，不要用解释型转折总结局面。"
	case "isolated_sentence_overuse":
		return "单行孤句只保留最关键 3-4 处；其余并回相邻段，或改成对话、动作余波、物件状态，避免全章像分镜提纲。"
	case "vague_quantifier_overuse":
		return "半/一点/几分等虚量词超量时，删抽象虚量或换成具体物件状态；不要用同一类模糊词铺“人味”。"
	case "object_response_overuse":
		return "屏幕、纸面、门牌、灯光等物件不要每次替人物确认；删掉多余即时响应，改成延迟、缺席、误读、旁人反应或后续代价。"
	case "object_response_rhythm_flat":
		return "物件回应必须不等距：至少一次重话后没有任何外部响应，一次响应延后到下一动作之后；不要句句重话后显字/亮屏/震动。"
	case "structured_note_triplet":
		return "便签和备忘录不要三条工整并列；改成划掉、补字、写半截和现场犹豫。"
	case "card_tos_block":
		return "黑卡/系统提示不要完整列 ToS；改成残字、糊字、空白账单位和读不全的凸字。"
	case "empty_parallel_chant":
		return "童谣只保留有内容的规则链；空对仗三连改成孩子卡壳、背岔或数字错位。"
	case "de_fa_adjective_repetition":
		return "全章只保留一两处最有质感的“X得发Y”，其余换成具体状态、动作或删除。"
	case "duplicate_dialogue_point":
		return "相邻对白不要重复同一骂点或笑点；删一保一，或让第二句转成新信息、新行动、新代价。"
	case "impossible_body_geometry":
		return "身体、影子和空间关系必须能成像；改成视线方向明确的画面。"
	case "impossible_line_of_sight":
		return "猫眼、门缝、侧向夹角不能读清背面小字；让字渗到门内、贴近猫眼或直接写看不清。"
	case "causal_evidence_order":
		return "角色点评证据前，证据必须已经出现；先写昵称/纸面/门牌变化，再让人物指着现成证据反驳。"
	case "identity_effect_delayed":
		return "报身份、报名字或确认后的规则后果要紧贴演示，不要在因果之间插入闲聊和新支线。"
	case "building_floor_mismatch":
		return "楼栋、楼层、门牌号要统一；3栋5楼不能写成5栋承租物，除非剧情明确换楼栋。"
	case "anomalous_phone_unverified":
		return "异常来电不是正常渠道时，主角必须先核验身份，再相信对面声音。"
	case "form_image_mismatch":
		return "票据栏位和印章的比喻要贴合形状；栏位不写成像章，改成拼出来、贴歪或格线不齐。"
	case "card_core_rule_overblurred":
		return "黑卡可以残缺，但核心可玩规则不能全糊掉；保留“可确认”等少量可读信息。"
	default:
		return ""
	}
}

func mechanicalViolationBriefLabel(violation rules.Violation) string {
	parts := []string{violation.Rule}
	if rules.HasLimitValue(violation.Actual) {
		parts = append(parts, fmt.Sprintf("actual=%s", rules.FormatLimitValue(violation.Actual)))
	}
	if rules.HasLimitValue(violation.Limit) {
		parts = append(parts, fmt.Sprintf("limit=%s", rules.FormatLimitValue(violation.Limit)))
	}
	if violation.Target != "" {
		parts = append(parts, "target="+violation.Target)
	}
	return strings.Join(parts, " ")
}

func addAIVoiceAnalysisToPlan(analysis domain.AIVoiceAnalysis, addRed, addYellow, addSuggestion func(string)) {
	flags := analysis.RedFlags
	for _, flag := range flags {
		label := flag.Rule
		if flag.Evidence != "" {
			label += "｜" + flag.Evidence
		}
		switch flag.Severity {
		case "error", "critical":
			addRed("AI红旗 " + label)
		case "warning":
			if reviewreport.IsBlockingAIVoiceFlagInAnalysis(flag, analysis) {
				addRed("AI味阻断 " + label)
			} else {
				addYellow("AI黄旗 " + label)
			}
		}
		if flag.Suggestion != "" {
			addSuggestion(flag.Suggestion)
		}
		if flag.Rule == "supporting_dialogue_ratio" {
			addSuggestion(fmt.Sprintf("supporting_dialogue_ratio 定量修复：当前 %.2f，目标至少 %.2f；不要只补动作，需净增 120-180 字引号内配角主动话轮，例如夏岚误解/转述、程棠拒绝被支开、苏曼私信追问，并同步删同量说明。", flag.Actual, flag.Limit))
		}
		if flag.Replacement != "" {
			addSuggestion(flag.Replacement)
		}
	}
}

func downgradeAcceptedWarningOnlyPlan(plan revisionPlan, reviewEntry *domain.ReviewEntry, mechanical *reviewreport.MechanicalGatePayload, aiVoice *domain.AIVoiceAnalysis) revisionPlan {
	if !reviewreport.AcceptedWarningOnlyGate(mechanical, aiVoice, reviewEntry) || !plan.HasRed {
		return plan
	}
	plan.YellowReasons = appendUnique(plan.YellowReasons, "Editor 已 accept，机械/AI voice 仅剩 warning 且 blended AI 值达标；本轮停止强制重写，剩余项沉淀为后续黄旗打磨。")
	for _, reason := range plan.RedReasons {
		plan.YellowReasons = appendUnique(plan.YellowReasons, "已降级黄旗："+reason)
	}
	plan.RedReasons = nil
	plan.HasRed = false
	plan.HasYellow = true
	return plan
}

func renderRevisionBrief(plan revisionPlan, reviewMarkdown string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# ch%02d rewrite brief\n\n", plan.Chapter)
	b.WriteString("## 门禁结论\n\n")
	fmt.Fprintf(&b, "- 红旗阻断：%t\n", plan.HasRed)
	fmt.Fprintf(&b, "- 黄旗存在：%t\n", plan.HasYellow)
	b.WriteString("- AI率目标：≤5%；不得追求 <1% 而牺牲正文质量\n")
	if len(plan.Sources) > 0 {
		fmt.Fprintf(&b, "- 汇总来源：%s\n", strings.Join(plan.Sources, "、"))
	}

	b.WriteString("\n## 质量优先边界\n\n")
	b.WriteString("- 红旗必须通过更好的剧情动作、对话摩擦、可见事实、人物选择或规则后果解决。\n")
	b.WriteString("- 黄旗只在能提升人物、节奏、信息清晰度或语言质感时采用；若只是为了指标换词，保留原文。\n")
	b.WriteString("- 禁止注水、乱码、OCR 脏码、随机汉字、冷僻词堆砌、无信息清单、拟声长串或刻意错别字。\n")
	b.WriteString("- 不新增改变主线事实的人名、组织、合同、授权、证据或能力。\n")

	b.WriteString("\n## 红旗阻断项\n\n")
	writeStringList(&b, plan.RedReasons)
	b.WriteString("\n## 黄旗择优项\n\n")
	writeStringList(&b, plan.YellowReasons)
	b.WriteString("\n## 汇总改写建议\n\n")
	writeStringList(&b, plan.Suggestions)

	if strings.TrimSpace(reviewMarkdown) != "" {
		b.WriteString("\n## Markdown 评审摘录\n\n")
		b.WriteString(truncateForContext(reviewMarkdown, 2800))
		b.WriteByte('\n')
	}
	return b.String()
}

func writeRevisionBrief(projectDir string, plan revisionPlan) error {
	path := filepath.Join(projectDir, "reviews", fmt.Sprintf("%02d_rewrite_brief.md", plan.Chapter))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(plan.Brief), 0o644)
}

func blockingRevisionChapters(projectDir string, start, end int) ([]int, error) {
	return revisionChaptersNeedingWork(projectDir, start, end, false)
}

func revisionChaptersNeedingWork(projectDir string, start, end int, includeYellow bool) ([]int, error) {
	available, err := chapterNumbersFromFiles(filepath.Join(projectDir, "chapters"))
	if err != nil {
		return nil, err
	}
	selected := filterChaptersForPipelineRange(available, pipelineFlags{Start: start, End: end})
	var chapters []int
	for _, ch := range selected {
		text, err := os.ReadFile(filepath.Join(projectDir, "chapters", fmt.Sprintf("%02d.md", ch)))
		if err != nil {
			return nil, fmt.Errorf("读取第 %d 章用于复核红旗失败: %w", ch, err)
		}
		plan := buildRevisionPlan(projectDir, ch, string(text), "")
		if plan.HasRed || (includeYellow && plan.HasYellow) {
			chapters = append(chapters, ch)
		}
	}
	return chapters, nil
}

func rewriteLoopReviewArgs(flags rewriteFlags) []string {
	var args []string
	if flags.Start > 0 {
		args = append(args, "--from", strconv.Itoa(flags.Start))
	}
	if flags.End > 0 {
		args = append(args, "--to", strconv.Itoa(flags.End))
	}
	if flags.Budget > 0 {
		args = append(args, "--budget", flags.Budget.String())
	}
	return args
}

func sectionListItems(md, header string) []string {
	inSection := false
	var items []string
	for _, line := range strings.Split(md, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, header) {
			inSection = true
			continue
		}
		if inSection && strings.HasPrefix(trimmed, "## ") {
			break
		}
		if !inSection {
			continue
		}
		item := strings.TrimSpace(strings.TrimLeft(trimmed, "-*0123456789.、)） "))
		if item != "" && item != trimmed {
			items = append(items, stripMarkdownCell(item))
		}
	}
	return items
}

func writeStringList(b *strings.Builder, values []string) {
	if len(values) == 0 {
		b.WriteString("- 无\n")
		return
	}
	for _, value := range values {
		fmt.Fprintf(b, "- %s\n", value)
	}
}

func readTextIfExists(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(data)
}

func readJSONIfExists(path string, target any) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return json.Unmarshal(data, target) == nil
}

func appendUnique(values []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return values
	}
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func appendUniqueLimit(values []string, value string, limit int) []string {
	values = appendUnique(values, value)
	if limit > 0 && len(values) > limit {
		return values[:limit]
	}
	return values
}

func formatChapterList(chapters []int) string {
	if len(chapters) == 0 {
		return ""
	}
	sort.Ints(chapters)
	parts := make([]string, 0, len(chapters))
	for _, ch := range chapters {
		parts = append(parts, fmt.Sprintf("%02d", ch))
	}
	return strings.Join(parts, ",")
}
