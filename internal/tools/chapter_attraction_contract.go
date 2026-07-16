package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/errs"
	"github.com/chenhongyang/novel-studio/internal/store"
)

type chapterAttractionRequirements struct {
	Trend           bool
	Entertainment   bool
	Longform        bool
	SystemCompanion bool
}

type projectWebReferencePolicy struct {
	Version              int                                   `json:"version"`
	RetrievedAt          string                                `json:"retrieved_at"`
	ChapterTrendLanguage map[string][]domain.TrendLanguagePlan `json:"chapter_trend_language"`
	SystemCompanion      struct {
		Required           bool     `json:"required"`
		CompanionVoiceBeat string   `json:"companion_voice_beat"`
		ForbiddenComedy    []string `json:"forbidden_comedy"`
	} `json:"system_companion"`
}

func loadProjectWebReferencePolicy(s *store.Store) (*projectWebReferencePolicy, error) {
	if s == nil {
		return nil, nil
	}
	raw, err := os.ReadFile(filepath.Join(s.Dir(), "meta", "web_reference_brief.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var policy projectWebReferencePolicy
	if err := json.Unmarshal(raw, &policy); err != nil {
		return nil, err
	}
	return &policy, nil
}

func chapterTrendLanguagePolicyPlans(s *store.Store, chapter int) []domain.TrendLanguagePlan {
	policy, err := loadProjectWebReferencePolicy(s)
	if err != nil || policy == nil {
		return nil
	}
	return append([]domain.TrendLanguagePlan(nil), policy.ChapterTrendLanguage[fmt.Sprintf("%d", chapter)]...)
}

// ProjectRequiresSystemCompanion centralizes the project-level companion
// contract used by planning, review sedimentation, and external-judge guards.
// The structured JSON policy must remain authoritative even when a legacy
// project has no equivalent prose in user_rules or the Markdown brief.
func ProjectRequiresSystemCompanion(s *store.Store) bool {
	if s == nil {
		return false
	}
	if policy, err := loadProjectWebReferencePolicy(s); err == nil && policy != nil && policy.SystemCompanion.Required {
		return true
	}
	var userSource string
	if snapshot, err := s.UserRules.Load(); err == nil && snapshot != nil {
		userSource = snapshot.Structured.Genre + "\n" + snapshot.Preferences
	}
	if domain.SystemCompanionVoiceRequested(userSource) {
		return true
	}
	if domain.SystemCompanionVoiceForbidden(userSource) {
		return false
	}
	var briefSource string
	if brief, err := os.ReadFile(filepath.Join(s.Dir(), "meta", "web_reference_brief.md")); err == nil {
		briefSource = string(brief)
	}
	return domain.SystemCompanionVoiceRequested(briefSource)
}

func attractionRequirementsForChapter(s *store.Store, chapter int) chapterAttractionRequirements {
	var userSource, briefSource string
	if s != nil {
		if snapshot, err := s.UserRules.Load(); err == nil && snapshot != nil {
			userSource = snapshot.Structured.Genre + "\n" + snapshot.Preferences
		}
		if brief, err := os.ReadFile(filepath.Join(s.Dir(), "meta", "web_reference_brief.md")); err == nil {
			briefSource = string(brief)
		}
	}
	globalSource := userSource + "\n" + briefSource
	userTrendRequired := domain.TrendLanguageRequested(userSource)
	requirements := chapterAttractionRequirements{
		Entertainment:   domain.ReaderEntertainmentRequested(globalSource),
		SystemCompanion: ProjectRequiresSystemCompanion(s),
	}
	// A structured chapter map is authoritative for web-reference trend items:
	// chapter-one curation must not become an active-meme requirement for every
	// later chapter. Legacy Markdown with chapter headings is scoped the same
	// way; only an old unscoped brief falls back to whole-document heuristics.
	policy, policyErr := loadProjectWebReferencePolicy(s)
	if policyErr == nil && policy != nil && policy.ChapterTrendLanguage != nil {
		requirements.Trend = userTrendRequired || len(policy.ChapterTrendLanguage[fmt.Sprintf("%d", chapter)]) > 0
	} else if webReferenceBriefHasChapterTrendSections(briefSource) {
		section := chapterTrendLanguageBriefSection(s, chapter)
		requirements.Trend = userTrendRequired || domain.TrendLanguageRequested(section) || len(backtickBriefItemPattern.FindAllStringSubmatch(section, -1)) > 0
	} else {
		requirements.Trend = domain.TrendLanguageRequested(globalSource)
	}
	if chapter == 1 {
		if progress, err := s.Progress.Load(); err == nil && progress != nil && progress.TotalChapters >= 50 {
			requirements.Longform = true
		}
		if strings.Contains(globalSource, "长篇") || strings.Contains(globalSource, "万字") {
			requirements.Longform = true
		}
	}
	return requirements
}

// ChapterAttractionPlanReadyForProject is the shared prewrite/commit contract.
// Routing must use the same project sources (user rules plus the structured web
// reference brief) as commit validation; otherwise a plan can be dispatched to
// Drafter and only fail after an expensive whole-chapter render.
func ChapterAttractionPlanReadyForProject(s *store.Store, plan domain.ChapterPlan) bool {
	if s == nil || plan.Chapter <= 0 {
		return false
	}
	requirements := attractionRequirementsForChapter(s, plan.Chapter)
	if !domain.ChapterAttractionPlanReady(
		plan,
		requirements.Trend,
		requirements.Entertainment,
		requirements.Longform,
		requirements.SystemCompanion,
	) {
		return false
	}
	trend := plan.CausalSimulation.TrendLanguage
	if len(trend) > 0 && (!domain.CompleteTrendLanguagePlan(trend) ||
		len(domain.TrendLanguagePlanProblems(trend)) > 0 ||
		!trendLanguagePlanGroundedInChapterBrief(s, plan.Chapter, trend)) {
		return false
	}
	entertainment := plan.CausalSimulation.EntertainmentPlan
	if (len(entertainment.HumorBeats) > 0 || strings.TrimSpace(entertainment.OpeningBeat) != "") &&
		!domain.CompleteReaderEntertainmentPlan(entertainment) {
		return false
	}
	return true
}

func trendLanguagePlanGroundedInChapterBrief(s *store.Store, chapter int, items []domain.TrendLanguagePlan) bool {
	if policy, err := loadProjectWebReferencePolicy(s); err == nil && policy != nil && policy.ChapterTrendLanguage != nil {
		allowed := policy.ChapterTrendLanguage[fmt.Sprintf("%d", chapter)]
		if len(allowed) == 0 {
			return !domain.HasActiveTrendLanguagePlan(items)
		}
		allowedItems := map[string]struct{}{}
		for _, item := range allowed {
			allowedItems[strings.TrimSpace(item.Item)] = struct{}{}
		}
		seen := map[string]struct{}{}
		for _, item := range items {
			if !domain.HasActiveTrendLanguagePlan([]domain.TrendLanguagePlan{item}) {
				continue
			}
			phrase := strings.Trim(strings.TrimSpace(item.Item), "`'\"“”‘’")
			if _, ok := allowedItems[phrase]; !ok {
				return false
			}
			if _, duplicate := seen[phrase]; duplicate {
				return false
			}
			seen[phrase] = struct{}{}
			if !strings.Contains(strings.ToLower(item.SourceContext), "meta/web_reference_brief") {
				return false
			}
		}
		return len(seen) == len(allowedItems)
	}
	section := chapterTrendLanguageBriefSection(s, chapter)
	if section == "" {
		if raw, err := os.ReadFile(filepath.Join(s.Dir(), "meta", "web_reference_brief.md")); err == nil && webReferenceBriefHasChapterTrendSections(string(raw)) {
			return !domain.HasActiveTrendLanguagePlan(items)
		}
		return true
	}
	active := 0
	for _, item := range items {
		if !domain.HasActiveTrendLanguagePlan([]domain.TrendLanguagePlan{item}) {
			continue
		}
		active++
		phrase := strings.Trim(strings.TrimSpace(item.Item), "`'\"“”‘’")
		if !strings.Contains(section, phrase) {
			return false
		}
		source := strings.ToLower(strings.TrimSpace(item.SourceContext))
		if !strings.Contains(source, "web_reference_brief") && !strings.Contains(source, "联网简报") && !strings.Contains(source, "本书简报") {
			return false
		}
	}
	return active > 0
}

func webReferenceBriefHasChapterTrendSections(text string) bool {
	return strings.Contains(text, "章热梗落点") &&
		(strings.Contains(text, "## 第") || strings.Contains(text, "### 第"))
}

func chapterTrendLanguageBriefSection(s *store.Store, chapter int) string {
	if s == nil || chapter <= 0 {
		return ""
	}
	raw, err := os.ReadFile(filepath.Join(s.Dir(), "meta", "web_reference_brief.md"))
	if err != nil {
		return ""
	}
	numerals := map[int]string{1: "一", 2: "二", 3: "三", 4: "四", 5: "五", 6: "六", 7: "七", 8: "八", 9: "九", 10: "十"}
	labels := []string{fmt.Sprintf("第%d章热梗落点", chapter)}
	if numeral := numerals[chapter]; numeral != "" {
		labels = append(labels, "第"+numeral+"章热梗落点")
	}
	wanted := make(map[string]struct{}, len(labels))
	for _, label := range labels {
		wanted[label] = struct{}{}
	}
	lines := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
	for i, line := range lines {
		level, title, ok := parseMarkdownATXHeading(line)
		if !ok || (level != 2 && level != 3) {
			continue
		}
		if _, ok := wanted[title]; !ok {
			continue
		}
		end := len(lines)
		for j := i + 1; j < len(lines); j++ {
			nextLevel, nextTitle, nextOK := parseMarkdownATXHeading(lines[j])
			if !nextOK {
				continue
			}
			// Ordinary nested headings remain part of the current chapter
			// section. A same/higher-level heading ends it, and a chapter
			// trend heading at any level always starts another chapter scope.
			if nextLevel <= level || chapterTrendHeadingPattern.MatchString(nextTitle) {
				end = j
				break
			}
		}
		return strings.TrimSpace(strings.Join(lines[i+1:end], "\n"))
	}
	return ""
}

var backtickBriefItemPattern = regexp.MustCompile("`([^`]+)`")
var markdownATXHeadingPattern = regexp.MustCompile(`^ {0,3}(#{1,6})[\t ]+(.+?)[\t ]*$`)
var chapterTrendHeadingPattern = regexp.MustCompile(`^第(?:[0-9]+|[零一二两三四五六七八九十百]+)章热梗落点$`)

func parseMarkdownATXHeading(line string) (int, string, bool) {
	match := markdownATXHeadingPattern.FindStringSubmatch(line)
	if len(match) != 3 {
		return 0, "", false
	}
	title := strings.TrimSpace(match[2])
	if strings.HasSuffix(title, "#") {
		title = strings.TrimSpace(strings.TrimRight(title, "#"))
	}
	return len(match[1]), title, title != ""
}

func chapterTrendLanguageBriefItems(s *store.Store, chapter int) []string {
	if allowed := chapterTrendLanguagePolicyPlans(s, chapter); len(allowed) > 0 {
		items := make([]string, 0, len(allowed))
		for _, item := range allowed {
			items = append(items, strings.TrimSpace(item.Item))
		}
		return compactStrings(items)
	}
	section := chapterTrendLanguageBriefSection(s, chapter)
	var items []string
	for _, match := range backtickBriefItemPattern.FindAllStringSubmatch(section, -1) {
		if len(match) >= 2 {
			items = append(items, strings.TrimSpace(match[1]))
		}
	}
	return compactStrings(items)
}

// normalizeChapterAttractionPlan 把项目明确写入结构化联网简报的硬规则锚回正式 plan。
// 模型仍决定场景和因果，但不能擅自换掉用户指定热梗，或把会交流的系统改成冷机器。
func normalizeChapterAttractionPlan(s *store.Store, plan *domain.ChapterPlan) []string {
	if s == nil || plan == nil || plan.Chapter <= 0 {
		return nil
	}
	policy, err := loadProjectWebReferencePolicy(s)
	if err != nil || policy == nil {
		return nil
	}
	var changes []string
	requirements := attractionRequirementsForChapter(s, plan.Chapter)
	if requirements.Trend {
		allowed := policy.ChapterTrendLanguage[fmt.Sprintf("%d", plan.Chapter)]
		if len(allowed) > 0 && !trendLanguagePlanGroundedInChapterBrief(s, plan.Chapter, plan.CausalSimulation.TrendLanguage) {
			plan.CausalSimulation.TrendLanguage = append([]domain.TrendLanguagePlan(nil), allowed...)
			changes = append(changes, fmt.Sprintf("trend_language_plan 已按 meta/web_reference_brief.json 锚定为 %d 条可选候选", len(allowed)))
		}
	}
	if required, optional := splitOptionalStyleBeats(plan.Contract.RequiredBeats, plan.CausalSimulation.TrendLanguage); len(optional) > 0 {
		plan.Contract.RequiredBeats = required
		changes = append(changes, fmt.Sprintf("已将 %d 条热梗/颜文字原句要求从 required_beats 降为可选风格素材", len(optional)))
	}
	if requirements.SystemCompanion && policy.SystemCompanion.Required {
		if problems := domain.SystemCompanionPlanProblems(plan.CausalSimulation); len(problems) > 0 {
			plan.CausalSimulation.EntertainmentPlan.CompanionVoiceBeat = strings.TrimSpace(policy.SystemCompanion.CompanionVoiceBeat)
			plan.CausalSimulation.EntertainmentPlan.ForbiddenComedy = normalizeSystemCompanionForbiddenComedy(
				plan.CausalSimulation.EntertainmentPlan.ForbiddenComedy,
				policy.SystemCompanion.ForbiddenComedy,
			)
			normalizeSystemCompanionAntiAI(&plan.CausalSimulation.AntiAIPlan)
			normalizeSystemCompanionDialogues(plan.CausalSimulation.DialogueBlueprints)
			changes = append(changes, "system_companion_voice 已按 meta/web_reference_brief.json 恢复接话、吐槽、解闷与支持边界")
		}
	}
	if len(changes) > 0 {
		plan.CausalSimulation.ContextSources = appendUniqueString(plan.CausalSimulation.ContextSources, "style_policy:meta/web_reference_brief.json")
	}
	return changes
}

func normalizeSystemCompanionForbiddenComedy(current, policy []string) []string {
	var out []string
	for _, item := range current {
		if strings.Contains(item, "把系统写成会吐槽") || strings.Contains(item, "把系统写成聊天伙伴") ||
			strings.Contains(item, "系统不得吐槽") || strings.Contains(item, "系统禁止吐槽") {
			continue
		}
		out = appendUniqueString(out, normalizeSystemCompanionText(item))
	}
	for _, item := range policy {
		out = appendUniqueString(out, item)
	}
	return out
}

func normalizeSystemCompanionAntiAI(plan *domain.AntiAIExecutionPlan) {
	if plan == nil {
		return
	}
	plan.ObjectResponseBudget = normalizeSystemCompanionText(plan.ObjectResponseBudget)
	plan.DialogueFunctionPlan = normalizeSystemCompanionText(plan.DialogueFunctionPlan)
	for i := range plan.CounterMoves {
		plan.CounterMoves[i] = normalizeSystemCompanionText(plan.CounterMoves[i])
	}
	for i := range plan.ReviewChecks {
		plan.ReviewChecks[i] = normalizeSystemCompanionText(plan.ReviewChecks[i])
	}
}

func normalizeSystemCompanionText(text string) string {
	for _, replacement := range [][2]string{
		{"系统不回应情绪吐槽", "系统接话保持短促、有性格且始终支持主角"},
		{"系统不接话", "系统用短句接话并支持主角"},
		{"系统不回应", "系统用短句回应并支持主角"},
		{"系统只用冷硬", "系统以短促、有性格的方式"},
		{"系统保持冷硬", "系统保持规则边界，同时用短句支持主角"},
		{"未变成陪聊", "既能接话解闷又未变成菜单或万能剧透"},
		{"不能陪聊", "可以短促陪聊但不能连续抛梗或万能剧透"},
		{"不拟人闲聊", "每次只用一两句有性格地接话，不铺菜单"},
		{"不暗示系统人格", "显出系统支持主角但不剧透后续"},
		{"系统界面保持静默", "系统用一句短回应支持主角，随后不连弹规则"},
		{"系统保持静默", "系统用一句短回应支持主角，随后不连弹规则"},
	} {
		text = strings.ReplaceAll(text, replacement[0], replacement[1])
	}
	return text
}

func normalizeSystemCompanionDialogues(items []domain.DialogueSceneBlueprint) {
	for i := range items {
		raw, _ := json.Marshal(items[i])
		text := string(raw)
		if !strings.Contains(text, "系统") && !strings.Contains(text, "手机界面") {
			continue
		}
		items[i].ScenePressure = normalizeSystemCompanionText(items[i].ScenePressure)
		items[i].RelationshipFrame = normalizeSystemCompanionText(items[i].RelationshipFrame)
		items[i].DialogueObjective = normalizeSystemCompanionText(items[i].DialogueObjective)
		items[i].ExitBeat = normalizeSystemCompanionText(items[i].ExitBeat)
		items[i].DoNotUse = normalizeSystemCompanionStrings(items[i].DoNotUse)
		if !strings.Contains(items[i].RelationshipFrame, "始终支持") {
			items[i].RelationshipFrame += "；系统会接话解闷并始终支持主角，但规则边界不可谈判，也不替主角做决定。"
		}
		for j := range items[i].TurnProgression {
			turn := &items[i].TurnProgression[j]
			if strings.Contains(turn.Speaker, "手机界面") || strings.Contains(turn.Speaker, "系统界面") {
				turn.Speaker = "系统"
			}
			turn.HiddenSubtext = normalizeSystemCompanionText(turn.HiddenSubtext)
			turn.ActionBeat = normalizeSystemCompanionText(turn.ActionBeat)
			turn.SurfaceLineFunction = normalizeSystemCompanionText(turn.SurfaceLineFunction)
			turn.NextPressure = normalizeSystemCompanionText(turn.NextPressure)
		}
	}
}

func normalizeSystemCompanionStrings(items []string) []string {
	out := make([]string, len(items))
	for i, item := range items {
		out[i] = normalizeSystemCompanionText(item)
	}
	return out
}

type chapterAttractionEvidence struct {
	OpeningCandidate string   `json:"opening_candidate,omitempty"`
	HumorCandidates  []string `json:"humor_candidates,omitempty"`
	PayoffCandidates []string `json:"payoff_candidates,omitempty"`
	TrendItems       []string `json:"trend_candidates,omitempty"`
	TrendMatches     []string `json:"trend_matches,omitempty"`
	TrendMisuses     []string `json:"trend_misuses,omitempty"`
	SelectionPolicy  string   `json:"selection_policy"`
}

func inspectChapterAttractionEvidence(plan domain.ChapterPlan, content string) chapterAttractionEvidence {
	evidence := chapterAttractionEvidence{
		OpeningCandidate: strings.TrimSpace(plan.CausalSimulation.EntertainmentPlan.OpeningBeat),
		HumorCandidates:  compactStrings(plan.CausalSimulation.EntertainmentPlan.HumorBeats),
		PayoffCandidates: compactStrings(plan.CausalSimulation.EntertainmentPlan.ImmediatePayoffs),
		SelectionPolicy:  "以上均为写前备选，正文不逐项验收。只评估开篇抓力、自然喜剧/松弛感和可见后果的整体读者效果；可重排、替换或省略。只有 trend_misuses 表示正文实际用了候选却用错。",
	}
	for _, item := range plan.CausalSimulation.TrendLanguage {
		phrase := strings.Trim(strings.TrimSpace(item.Item), "`'\"“”‘’")
		if phrase == "" || !domain.HasActiveTrendLanguagePlan([]domain.TrendLanguagePlan{item}) {
			continue
		}
		evidence.TrendItems = append(evidence.TrendItems, phrase)
		if trendPhraseRealized(content, phrase) {
			evidence.TrendMatches = append(evidence.TrendMatches, phrase)
		} else if trendPhraseAttempted(content, phrase) {
			evidence.TrendMisuses = append(evidence.TrendMisuses, phrase)
		}
	}
	return evidence
}

func trendPhraseAttempted(content, phrase string) bool {
	base := strings.TrimSpace(strings.TrimRight(phrase, "，,。！？!?"))
	return base != "" && strings.Contains(content, base)
}

func trendPhraseRealized(content, phrase string) bool {
	phrase = strings.TrimSpace(phrase)
	if strings.TrimRight(phrase, "，,") == "呱" {
		return strings.Contains(content, "呱，") || strings.Contains(content, "呱,")
	}
	return phrase != "" && strings.Contains(content, phrase)
}

func requireChapterAttractionContent(s *store.Store, chapter int, content string) error {
	plan, err := s.Drafts.LoadChapterPlan(chapter)
	if err != nil || plan == nil {
		return err
	}
	if !ChapterAttractionPlanReadyForProject(s, *plan) {
		return fmt.Errorf("第 %d 章写前吸引力合同不完整：必须先重做 plan，补齐 reader_entertainment_plan、需要时的 trend_language_plan 与第一章 longform_opening: %w", chapter, errs.ErrToolPrecondition)
	}
	// trend_language_plan is a curated option and usage ceiling, not a literal
	// per-chapter delivery gate. Forcing one planned phrase into every chapter
	// turns memes and kaomoji into contract prose even when the scene rejects it.
	if _, _, brief, loadErr := loadChapterRewriteSource(s, chapter); loadErr != nil {
		return loadErr
	} else if brief != "" {
		var missing []string
		optionalStyle := append(chapterTrendLanguageBriefItems(s, chapter), attractionTrendItems(*plan)...)
		for _, literal := range rewriteBriefRequiredLiterals(brief) {
			if literalMatchesOptionalStyle(literal, optionalStyle) {
				continue
			}
			if !strings.Contains(content, literal) {
				missing = append(missing, literal)
			}
		}
		if len(missing) > 0 {
			return fmt.Errorf("第 %d 章未兑现 rewrite brief 要求原样出现的文本：%s: %w", chapter, strings.Join(missing, "、"), errs.ErrToolPrecondition)
		}
	}
	return nil
}

func attractionTrendItems(plan domain.ChapterPlan) []string {
	items := make([]string, 0, len(plan.CausalSimulation.TrendLanguage))
	for _, item := range plan.CausalSimulation.TrendLanguage {
		if value := strings.Trim(strings.TrimSpace(item.Item), "`'\"“”‘’"); value != "" {
			items = append(items, value)
		}
	}
	return items
}

func literalMatchesOptionalStyle(literal string, items []string) bool {
	literal = strings.Trim(strings.TrimSpace(literal), "`'\"“”‘’")
	if literal == "" {
		return false
	}
	for _, item := range items {
		item = strings.Trim(strings.TrimSpace(item), "`'\"“”‘’")
		if item == "" {
			continue
		}
		if literal == item || strings.Contains(item, literal) || strings.Contains(literal, item) {
			return true
		}
		if strings.HasPrefix(item, "呱") && strings.HasPrefix(literal, "呱") {
			return true
		}
	}
	return strings.Contains(literal, "颜文字") || strings.Contains(literal, "^_^")
}
