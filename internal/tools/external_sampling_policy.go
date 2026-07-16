package tools

import (
	"strings"
	"unicode"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

const externalSamplingAutomatedReleaseContract = "自动化验收项（本地 AIGC、同哈希 DeepSeek 与事实一致性）全部通过后即可提交；用户手工外部抽查不作为逐章阻塞项"

// externalSamplingTextPolicyActive reports whether legacy human-retest wording
// should be treated as sampling-only. Marker source/evaluator are deliberately
// irrelevant: local or DeepSeek rejudging may replace a registered marker while
// an older plan and brief still carry the retired external release obligation.
// Only an explicit automated hard-detector deployment preserves that wording.
func externalSamplingTextPolicyActive(st *store.Store, chapter int) bool {
	if st == nil || chapter <= 0 {
		return false
	}
	requirement, err := loadDraftExternalRerenderRequirement(st.Dir(), chapter)
	if err != nil {
		return false
	}
	return requirement == nil || !requirement.BlockUntilExternalRetest
}

// sanitizeChapterPlanExternalSamplingPolicy migrates only the in-memory plan
// projection. The persisted historical plan remains an audit artifact, while
// planners and drafters stop seeing a retired requirement to wait for a human
// detector on every replacement hash.
func sanitizeChapterPlanExternalSamplingPolicy(st *store.Store, plan *domain.ChapterPlan) {
	if plan == nil || !externalSamplingTextPolicyActive(st, plan.Chapter) {
		return
	}
	sanitizeReviewRefinementExternalSampling(&plan.CausalSimulation.ReviewRefinement)
}

func sanitizeReviewRefinementExternalSampling(refinement *domain.ReviewRefinementLoop) {
	if refinement == nil {
		return
	}
	refinement.TriggerSources = sanitizeExternalSamplingContractList(refinement.TriggerSources)
	refinement.FailureModes = sanitizeExternalSamplingContractList(refinement.FailureModes)
	refinement.LocalizedTargets = sanitizeExternalSamplingContractList(refinement.LocalizedTargets)
	refinement.PreserveConstraints = sanitizeExternalSamplingContractList(refinement.PreserveConstraints)
	refinement.ReplanningMoves = sanitizeExternalSamplingContractList(refinement.ReplanningMoves)
	refinement.AcceptanceChecks = sanitizeExternalSamplingContractList(refinement.AcceptanceChecks)
	refinement.StopCondition = sanitizeExternalSamplingContractText(refinement.StopCondition)
}

func sanitizeExternalSamplingContractList(items []string) []string {
	var sanitized []string
	for _, item := range items {
		sanitized = appendUniqueString(sanitized, sanitizeExternalSamplingContractText(item))
	}
	return sanitized
}

// sanitizeExternalSamplingContractText removes only future release obligations
// tied to a human external retest. A reported high score, its exact old SHA,
// and prose-repair advice are intentionally retained.
func sanitizeExternalSamplingContractText(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || !legacyMandatoryExternalRetestClause(value, false) {
		return value
	}

	majorClauses := strings.FieldsFunc(value, func(r rune) bool {
		return r == '；' || r == ';' || r == '\n'
	})
	var sanitized []string
	replaced := false
	for _, major := range majorClauses {
		major = strings.TrimSpace(major)
		if major == "" {
			continue
		}
		if !legacyMandatoryExternalRetestClause(major, false) {
			sanitized = appendUniqueString(sanitized, major)
			continue
		}

		inheritedExternal := containsExternalDetectorIdentity(major)
		fragments := strings.FieldsFunc(major, func(r rune) bool {
			return r == '，' || r == ','
		})
		var kept []string
		for _, fragment := range fragments {
			fragment = strings.TrimSpace(fragment)
			if fragment == "" {
				continue
			}
			if legacyMandatoryExternalRetestClause(fragment, inheritedExternal) {
				replaced = true
				continue
			}
			kept = append(kept, fragment)
		}
		if len(kept) > 0 {
			sanitized = appendUniqueString(sanitized, strings.Join(kept, "，"))
		}
		if !replaced {
			// A mandatory major clause can contain connectors rather than comma
			// boundaries. Replace that indivisible clause with the current
			// automated release contract.
			replaced = true
		}
		if replaced {
			sanitized = appendUniqueString(sanitized, externalSamplingAutomatedReleaseContract)
		}
	}
	if !replaced {
		return value
	}
	return strings.Join(sanitized, "；")
}

func legacyMandatoryExternalRetestClause(value string, inheritedExternal bool) bool {
	normalized := strings.ToLower(strings.Join(strings.Fields(value), ""))
	if normalized == "" {
		return false
	}
	// A migrated sentence may contain both an obsolete hard clause and the new
	// non-blocking policy. Do not let the latter hide an explicit "only after
	// retest may submit" contradiction; clause splitting below will retain the
	// non-blocking half and remove only the retired obligation.
	if externalSamplingClauseIsNonBlocking(normalized) && !containsAnySubstring(normalized,
		"才可提交", "才能提交", "方可提交", "才可交付", "才能交付", "方可交付",
		"必须复测", "严格<4", "严格＜4", "同一detector", "同detector",
	) {
		return false
	}

	hasExternal := inheritedExternal || containsExternalDetectorIdentity(normalized)
	hasRetest := containsAnySubstring(normalized,
		"复测", "重测", "retest", "同测", "detector/mode", "namedfreeze",
	)
	// DeepSeek is a required automated same-hash gate. Do not strip it merely
	// because a neighboring sentence also mentions an external sampling result.
	if strings.Contains(normalized, "deepseek") && !containsExternalDetectorIdentity(normalized) {
		return false
	}

	hasNewBody := containsAnySubstring(normalized,
		"新sha", "新正文", "新稿", "替换稿", "替换正文", "改后", "改稿后", "重写后",
	)
	hasStrictThreshold := containsAnySubstring(normalized,
		"<4", "＜4", "低于4", "小于4", "严格4", "strictlybelow4",
	)
	hasSameIdentity := containsAnySubstring(normalized,
		"同一detector", "同detector", "同一检测器", "同模式", "同mode", "same-detector", "全部注册", "所有注册",
	)
	hasReleaseGate := containsAnySubstring(normalized,
		"才可提交", "才能提交", "方可提交", "才可交付", "才能交付", "方可交付",
		"提交前", "交付前", "冻结", "等待复测", "复测后通过", "复测通过", "复测合格",
	)
	currentResult := containsAnySubstring(normalized,
		"当前结果", "当前正文", "当前sha", "旧sha", "抽查结果", "检测结果", "得分", "分数",
	)
	passWithoutCurrentResult := strings.Contains(normalized, "通过") && !currentResult

	// Some persisted plans expressed the same retired human gate without using
	// the word "retest", for example "new SHA Zhuque <4% before submission" or
	// "deliver only after the external platform passes". External identity plus
	// a release condition is sufficient to identify that obligation. A plain
	// current-hash high-score fact has no release condition and remains intact.
	if !hasRetest && !(hasExternal && hasReleaseGate) {
		return false
	}

	// A generic "new SHA must be retested" line occurs under historical
	// external-gate headings and may omit the detector name. Under an active
	// sampling marker it is still the retired human release obligation.
	return (hasExternal || hasNewBody || hasStrictThreshold || hasSameIdentity) &&
		(hasNewBody || hasStrictThreshold || hasSameIdentity || hasReleaseGate || passWithoutCurrentResult)
}

func externalSamplingClauseIsNonBlocking(normalized string) bool {
	return containsAnySubstring(normalized,
		"不等待人工复测", "无需人工复测", "不要求人工复测", "无需逐章复测", "不要求逐章复测",
		"不作为逐章阻塞", "不作为阻塞", "不得阻塞提交", "不得阻塞交付", "抽查不阻塞",
		"仅由用户抽查", "外部平台仅由用户抽查",
	)
}

func containsExternalDetectorIdentity(value string) bool {
	normalized := strings.ToLower(strings.Join(strings.Fields(value), ""))
	return containsAnySubstring(normalized,
		"朱雀", "外部aigc", "外部检测", "外部平台", "external/", "externaldetector",
		"registered_external", "注册平台", "detector/mode", "namedfreeze",
	)
}

func containsAnySubstring(value string, candidates ...string) bool {
	for _, candidate := range candidates {
		if strings.Contains(value, candidate) {
			return true
		}
	}
	return false
}

func sanitizeExternalSamplingMarkdown(markdown string) string {
	if strings.TrimSpace(markdown) == "" {
		return markdown
	}
	lines := strings.Split(markdown, "\n")
	for i, line := range lines {
		prefix, content := markdownListPrefix(line)
		sanitized := sanitizeExternalSamplingContractText(content)
		if sanitized != strings.TrimSpace(content) {
			lines[i] = prefix + sanitized
		}
	}
	return strings.Join(lines, "\n")
}

func markdownListPrefix(line string) (prefix, content string) {
	leading := len(line) - len(strings.TrimLeftFunc(line, unicode.IsSpace))
	indent := line[:leading]
	rest := line[leading:]
	for _, marker := range []string{"- ", "* ", "+ ", "> "} {
		if strings.HasPrefix(rest, marker) {
			return indent + marker, strings.TrimSpace(strings.TrimPrefix(rest, marker))
		}
	}
	for i, r := range rest {
		if unicode.IsDigit(r) {
			continue
		}
		if r == '.' || r == '、' {
			next := i + len(string(r))
			if next < len(rest) && unicode.IsSpace(rune(rest[next])) {
				return indent + rest[:next+1], strings.TrimSpace(rest[next+1:])
			}
		}
		break
	}
	return "", strings.TrimSpace(line)
}

// sanitizeExternalSamplingPolicyContext runs before profile compaction so
// mergePlanningRewriteBriefSections can only extract the current sampling
// policy from historical brief markdown.
func sanitizeExternalSamplingPolicyContext(st *store.Store, chapter int, result map[string]any) {
	if result == nil || !externalSamplingTextPolicyActive(st, chapter) {
		return
	}
	sanitizeExternalSamplingContextContainer(result)
	for _, key := range []string{"working_memory", "planning_memory"} {
		if container, ok := result[key].(map[string]any); ok {
			sanitizeExternalSamplingContextContainer(container)
		}
	}
}

func sanitizeExternalSamplingContextContainer(container map[string]any) {
	if container == nil {
		return
	}
	if review, ok := container["draft_external_ai_review"].(map[string]any); ok {
		sanitizeDraftExternalReviewSamplingPolicy(review)
	}
	if source, ok := container["rewrite_source"].(map[string]any); ok {
		if markdown, ok := source["brief_markdown"].(string); ok {
			source["brief_markdown"] = sanitizeExternalSamplingMarkdown(markdown)
		}
	}
	if brief, ok := container["rewrite_brief"].(map[string]any); ok {
		sanitizeExternalSamplingMap(brief)
	}
	switch plan := container["chapter_plan"].(type) {
	case *domain.ChapterPlan:
		sanitizeReviewRefinementExternalSampling(&plan.CausalSimulation.ReviewRefinement)
	case domain.ChapterPlan:
		sanitizeReviewRefinementExternalSampling(&plan.CausalSimulation.ReviewRefinement)
		container["chapter_plan"] = plan
	case map[string]any:
		sanitizeExternalSamplingPlanMap(plan)
	}
	switch causal := container["causal_simulation"].(type) {
	case domain.ChapterCausalSimulation:
		sanitizeReviewRefinementExternalSampling(&causal.ReviewRefinement)
		container["causal_simulation"] = causal
	case *domain.ChapterCausalSimulation:
		if causal != nil {
			sanitizeReviewRefinementExternalSampling(&causal.ReviewRefinement)
		}
	case map[string]any:
		if refinement, ok := causal["review_refinement"].(map[string]any); ok {
			sanitizeExternalSamplingMap(refinement)
		}
	}
}

// sanitizeDraftExternalReviewSamplingPolicy closes the last legacy prompt path:
// loadDraftExternalJudgeContextWithStore projects a persisted sampling marker
// into draft_external_ai_review before profile compaction. Draft/world compactors
// intentionally retain its summary and evidence, so those fields must stop
// saying "retest required" before the compactors run.
//
// DeepSeek and local mechanical diagnoses use the same context slot. Restrict
// this migration to receipts with registered-external sampling provenance so a
// required DeepSeek current-hash rejudge is never stripped by accident.
func sanitizeDraftExternalReviewSamplingPolicy(review map[string]any) {
	if review == nil || !draftExternalReviewHasSamplingProvenance(review) {
		return
	}
	originalSummary, _ := review["summary"].(string)
	sanitizeExternalSamplingMap(review)
	if summary, ok := review["summary"].(string); ok {
		review["summary"] = preserveExternalSamplingRewriteTrigger(originalSummary, summary)
	}
	if evidence, ok := review["evidence"]; ok {
		review["evidence"] = sanitizeExternalSamplingEvidenceValue(evidence)
	}
}

func draftExternalReviewHasSamplingProvenance(review map[string]any) bool {
	if source, ok := review["source"].(string); ok &&
		strings.EqualFold(strings.TrimSpace(source), "registered_external_detection") {
		return true
	}
	return externalSamplingEvidenceContainsRegisteredEvent(review["evidence"])
}

func externalSamplingEvidenceContainsRegisteredEvent(value any) bool {
	switch typed := value.(type) {
	case string:
		normalized := strings.ToLower(strings.TrimSpace(typed))
		return strings.HasPrefix(normalized, "registered_external_detection:") ||
			strings.HasPrefix(normalized, "registered_external_retest_required:")
	case []string:
		for _, item := range typed {
			if externalSamplingEvidenceContainsRegisteredEvent(item) {
				return true
			}
		}
	case []any:
		for _, item := range typed {
			if externalSamplingEvidenceContainsRegisteredEvent(item) {
				return true
			}
		}
	}
	return false
}

func preserveExternalSamplingRewriteTrigger(original, sanitized string) string {
	if !containsAnySubstring(original, "整章重渲染", "整章重写", "整章返工") ||
		containsAnySubstring(sanitized, "整章重渲染", "整章重写", "整章返工") {
		return sanitized
	}
	const trigger = "当前精确 SHA 的高分已触发一次整章重渲染"
	if strings.Contains(sanitized, externalSamplingAutomatedReleaseContract) {
		return strings.Replace(
			sanitized,
			externalSamplingAutomatedReleaseContract,
			trigger+"；"+externalSamplingAutomatedReleaseContract,
			1,
		)
	}
	if strings.TrimSpace(sanitized) == "" {
		return trigger
	}
	return strings.TrimSpace(sanitized) + "；" + trigger
}

func sanitizeExternalSamplingEvidenceValue(value any) any {
	switch typed := value.(type) {
	case string:
		return sanitizeExternalSamplingEvidenceText(typed)
	case []string:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			out = append(out, sanitizeExternalSamplingEvidenceText(item))
		}
		return out
	case []any:
		out := make([]any, 0, len(typed))
		for _, item := range typed {
			out = append(out, sanitizeExternalSamplingEvidenceValue(item))
		}
		return out
	default:
		return value
	}
}

func sanitizeExternalSamplingEvidenceText(value string) string {
	value = strings.TrimSpace(value)
	const legacyPrefix = "registered_external_retest_required"
	normalized := strings.ToLower(value)
	if normalized == legacyPrefix || strings.HasPrefix(normalized, legacyPrefix+":") {
		suffix := strings.TrimPrefix(value[len(legacyPrefix):], ":")
		if suffix == "" {
			return "registered_external_sampling_trigger"
		}
		return "registered_external_sampling_trigger:" + suffix
	}
	return sanitizeExternalSamplingContractText(value)
}

func sanitizeExternalSamplingPlanMap(plan map[string]any) {
	causal, _ := plan["causal_simulation"].(map[string]any)
	refinement, _ := causal["review_refinement"].(map[string]any)
	if refinement == nil {
		return
	}
	sanitizeExternalSamplingMap(refinement)
}

func sanitizeExternalSamplingMap(values map[string]any) {
	for key, value := range values {
		values[key] = sanitizeExternalSamplingValue(value)
	}
}

func sanitizeExternalSamplingValue(value any) any {
	switch typed := value.(type) {
	case string:
		return sanitizeExternalSamplingContractText(typed)
	case []string:
		return sanitizeExternalSamplingContractList(typed)
	case []any:
		out := make([]any, 0, len(typed))
		for _, item := range typed {
			out = append(out, sanitizeExternalSamplingValue(item))
		}
		return out
	case map[string]any:
		sanitizeExternalSamplingMap(typed)
		return typed
	default:
		return value
	}
}
