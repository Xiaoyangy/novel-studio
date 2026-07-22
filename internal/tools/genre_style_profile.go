package tools

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/aigc"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/rules"
	"github.com/chenhongyang/novel-studio/internal/stylestat"
)

const RenderStyleContractProtocolVersion = "render-style-contract.v4-surface-payload-guard-and-serial-memory"

// genreStyleProfile is a reusable, deterministically selected craft profile.
// It is intentionally not stored in ChapterPlan: the project rule snapshot is
// the source of truth, while plans may be stale or absent during render-only
// rewrites. The selected profile is projected into the prose-facing packet.
type genreStyleProfile struct {
	ID                   string                 `json:"id"`
	Name                 string                 `json:"name"`
	Match                genreStyleProfileMatch `json:"match"`
	Tone                 string                 `json:"tone"`
	DialogueBreathPolicy string                 `json:"dialogue_breath_policy"`
	SceneFlowPolicy      string                 `json:"scene_flow_policy"`
	HumorPolicy          string                 `json:"humor_policy"`
	GrowthPolicy         string                 `json:"growth_policy"`
	RomancePolicy        string                 `json:"romance_policy"`
	SystemPolicy         string                 `json:"system_policy"`
	SourceRefs           []string               `json:"source_refs,omitempty"`
	Cards                []genreStyleCard       `json:"cards,omitempty"`
}

type genreStyleProfileMatch struct {
	RequireAny   []string `json:"require_any,omitempty"`
	ScoreAny     []string `json:"score_any,omitempty"`
	MinimumScore int      `json:"minimum_score,omitempty"`
}

type genreStyleCard struct {
	ID    string `json:"id"`
	Move  string `json:"move"`
	Avoid string `json:"avoid"`
}

type genreStyleCatalog struct {
	Version  int                 `json:"version"`
	Policy   string              `json:"policy,omitempty"`
	Profiles []genreStyleProfile `json:"profiles"`
}

type draftStyleContract struct {
	Version              int                     `json:"version"`
	ConfiguredStyleID    string                  `json:"configured_style_id,omitempty"`
	ConfiguredStyleName  string                  `json:"configured_style_name,omitempty"`
	ConfiguredStyleRules []string                `json:"configured_style_rules,omitempty"`
	ProfileID            string                  `json:"profile_id,omitempty"`
	ProfileName          string                  `json:"profile_name,omitempty"`
	Tone                 string                  `json:"tone,omitempty"`
	SourceIDs            []string                `json:"source_ids,omitempty"`
	ActiveRules          []string                `json:"soft_craft_rules,omitempty"`
	AntiAIRules          []string                `json:"anti_ai_rules,omitempty"`
	Taboos               []string                `json:"taboos,omitempty"`
	DialogueBreathPolicy string                  `json:"dialogue_breath_policy,omitempty"`
	SceneFlowPolicy      string                  `json:"scene_flow_policy,omitempty"`
	HumorPolicy          string                  `json:"humor_policy,omitempty"`
	GrowthPolicy         string                  `json:"growth_policy,omitempty"`
	RomancePolicy        string                  `json:"romance_policy,omitempty"`
	SystemPolicy         string                  `json:"system_policy,omitempty"`
	Cards                []draftGenreStyleCard   `json:"soft_cards,omitempty"`
	SerialMemory         *draftSerialStyleMemory `json:"serial_style_memory,omitempty"`
	SourceRefs           []string                `json:"source_refs,omitempty"`
	UsagePolicy          string                  `json:"usage_policy"`
}

// configuredStyleProfile is the compact, render-only projection of the
// selected assets/styles/<id>.md file.  It is intentionally absent from the
// planning and world-simulation profiles: style may change the telling, never
// the frozen causal result.
type configuredStyleProfile struct {
	ID        string   `json:"id"`
	Name      string   `json:"name,omitempty"`
	Rules     []string `json:"rules"`
	SourceRef string   `json:"source_ref"`
	Protocol  string   `json:"protocol"`
}

// draftSerialStyleMemory carries only high-confidence repetition facts from
// already accepted chapters. It is not a cadence recipe or detector score.
// Its job is to stop the sealed Drafter from unknowingly reusing the book's
// recent surface tics after the raw style_stats dossier is removed.
type draftSerialStyleMemory struct {
	BasedOnChapters int      `json:"based_on_chapters"`
	AvoidPhrases    []string `json:"recent_overused_phrases,omitempty"`
	AvoidExactLines []string `json:"cross_chapter_repeated_lines,omitempty"`
	PatternAlerts   []string `json:"overused_pattern_classes,omitempty"`
	OpeningGuard    string   `json:"opening_variation_guard,omitempty"`
	EndingGuard     string   `json:"ending_variation_guard,omitempty"`
	UsagePolicy     string   `json:"usage_policy"`
}

type draftGenreStyleCard struct {
	ID    string `json:"id"`
	Move  string `json:"move"`
	Avoid string `json:"avoid"`
}

func selectGenreStyleProfile(raw, style string, snap *rules.Snapshot) (*genreStyleProfile, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var catalog genreStyleCatalog
	if err := json.Unmarshal([]byte(raw), &catalog); err != nil {
		return nil, fmt.Errorf("decode genre style profiles: %w", err)
	}
	if catalog.Version <= 0 {
		return nil, fmt.Errorf("genre style profiles version must be positive")
	}

	var genre, preferences string
	if snap != nil {
		genre = snap.Structured.Genre
		preferences = snap.Preferences
	}
	haystack := strings.ToLower(strings.Join([]string{style, genre, preferences}, "\n"))
	style = strings.ToLower(strings.TrimSpace(style))
	bestScore := -1
	var best *genreStyleProfile
	for i := range catalog.Profiles {
		profile := &catalog.Profiles[i]
		if strings.TrimSpace(profile.ID) == "" || strings.TrimSpace(profile.Name) == "" {
			continue
		}
		explicit := style != "" && style != "default" && (style == strings.ToLower(profile.ID) || strings.Contains(strings.ToLower(profile.ID), style))
		if len(profile.Match.RequireAny) > 0 && !explicit && !containsAnyFold(haystack, profile.Match.RequireAny) {
			continue
		}
		score := 0
		for _, token := range profile.Match.ScoreAny {
			if token = strings.TrimSpace(token); token != "" && strings.Contains(haystack, strings.ToLower(token)) {
				score++
			}
		}
		minimum := profile.Match.MinimumScore
		if minimum <= 0 {
			minimum = 1
		}
		if explicit {
			score += minimum + 100
		}
		if score < minimum || score <= bestScore {
			continue
		}
		bestScore = score
		best = profile
	}
	if best == nil {
		return nil, nil
	}
	copy := *best
	copy.SourceRefs = append([]string(nil), best.SourceRefs...)
	copy.Cards = append([]genreStyleCard(nil), best.Cards...)
	return &copy, nil
}

func containsAnyFold(haystack string, needles []string) bool {
	for _, needle := range needles {
		if needle = strings.TrimSpace(needle); needle != "" && strings.Contains(haystack, strings.ToLower(needle)) {
			return true
		}
	}
	return false
}

func (t *ContextTool) addGenreStyleReference(pack map[string]any, refs map[string]string, warn func(string, error)) {
	snap, err := t.store.UserRules.Load()
	if err != nil {
		warn("genre_style_user_rules", err)
		return
	}
	profile, err := selectGenreStyleProfile(t.refs.GenreStyleProfiles, t.style, snap)
	if err != nil {
		warn("genre_style_profiles", err)
		return
	}
	if profile == nil {
		return
	}
	pack["genre_style_profile"] = profile
	if refs != nil && strings.TrimSpace(t.refs.GenreStyleCraft) != "" {
		refs["genre_style_craft"] = t.refs.GenreStyleCraft
	}
}

func newDraftStyleContract(result map[string]any) *draftStyleContract {
	profile := genreStyleProfileFromContext(result)
	engine := writingEngineFromContext(result)
	configured := configuredStyleProfileFromContext(result)
	serial := newDraftSerialStyleMemory(styleStatsFromContext(result))
	if profile == nil && engine == nil && configured == nil && serial == nil {
		return nil
	}
	contract := &draftStyleContract{
		Version:      3,
		SerialMemory: serial,
		UsagePolicy:  "冻结 plan、mandatory_beats、事实、人物决定与知识边界始终优先，风格合同只能改变讲述方式。用户最新规则优先于 configured_style_rules；dialogue/romance/system 边界、anti_ai_rules 与 taboos 必须遵守；soft_craft_rules 和 soft_cards 只是写法候选。serial_style_memory 只防逐字复读与章式同构，不得机械换同义词、轮换句长或为求不同新增剧情。",
	}
	if configured != nil {
		contract.ConfiguredStyleID = configured.ID
		contract.ConfiguredStyleName = configured.Name
		contract.ConfiguredStyleRules = append([]string(nil), configured.Rules...)
		contract.SourceRefs = append(contract.SourceRefs, configured.SourceRef)
	}
	if engine != nil {
		for _, feature := range engine.EnabledFeatures {
			if id := strings.TrimSpace(feature.ID); id != "" {
				contract.SourceIDs = append(contract.SourceIDs, id)
			}
		}
		contract.SourceIDs = limitRenderStrings(compactStrings(contract.SourceIDs), 12)
		contract.ActiveRules = compactSoftStyleContractStrings(engine.ActiveRules, 4)
		contract.AntiAIRules = compactStyleContractStrings(engine.AntiAIRules, 6)
		contract.Taboos = compactStyleContractStrings(engine.Taboos, 6)
	}
	if profile != nil {
		contract.ProfileID = strings.TrimSpace(profile.ID)
		contract.ProfileName = strings.TrimSpace(profile.Name)
		contract.Tone = firstRenderClause(profile.Tone)
		contract.DialogueBreathPolicy = firstRenderClause(profile.DialogueBreathPolicy)
		contract.SceneFlowPolicy = firstRenderClause(profile.SceneFlowPolicy)
		contract.HumorPolicy = firstRenderClause(profile.HumorPolicy)
		contract.GrowthPolicy = firstRenderClause(profile.GrowthPolicy)
		contract.RomancePolicy = firstRenderClause(profile.RomancePolicy)
		contract.SystemPolicy = firstRenderClause(profile.SystemPolicy)
		contract.SourceRefs = append(contract.SourceRefs, profile.SourceRefs...)
		for _, card := range profile.Cards {
			if len(contract.Cards) >= 2 || strings.TrimSpace(card.ID) == "" {
				break
			}
			contract.Cards = append(contract.Cards, draftGenreStyleCard{
				ID:    strings.TrimSpace(card.ID),
				Move:  firstRenderClause(card.Move),
				Avoid: firstRenderClause(card.Avoid),
			})
		}
	}
	contract.SourceRefs = limitRenderStrings(compactStrings(contract.SourceRefs), 10)
	return contract
}

func configuredStyleProfileFromMarkdown(style, raw string) *configuredStyleProfile {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	style = strings.TrimSpace(style)
	if style == "" {
		style = "default"
	}
	profile := &configuredStyleProfile{
		ID:        style,
		SourceRef: "assets/styles/" + style + ".md",
		Protocol:  RenderStyleContractProtocolVersion,
	}
	for _, rawLine := range strings.Split(raw, "\n") {
		line := strings.TrimSpace(rawLine)
		switch {
		case strings.HasPrefix(line, "#") && profile.Name == "":
			profile.Name = strings.TrimSpace(strings.TrimLeft(line, "#"))
		case strings.HasPrefix(line, "- "):
			rule := strings.TrimSpace(strings.TrimPrefix(line, "- "))
			rule = strings.ReplaceAll(rule, "**", "")
			if rule != "" && configuredStyleRuleIsSurfaceOnly(rule) && !slicesContainsString(profile.Rules, rule) {
				profile.Rules = append(profile.Rules, truncateRunes(rule, 180))
			}
		}
		if len(profile.Rules) >= 10 {
			break
		}
	}
	if len(profile.Rules) == 0 {
		return nil
	}
	return profile
}

// configuredStyleRuleIsSurfaceOnly is deliberately allow-listed. The bundled
// genre files also contain semantic planning advice (relationship stages,
// clue counts, conflicts, ability costs and plot hooks). Those lines remain in
// the source asset and its existing planning identity, but must never enter a
// render-only overlay that is allowed to change only how frozen events are
// told. Unknown labels fail closed; custom assets can use an explicit surface
// label from this list.
func configuredStyleRuleIsSurfaceOnly(rule string) bool {
	rule = strings.TrimSpace(rule)
	idx := strings.IndexAny(rule, "：:")
	if idx < 0 {
		return false
	}
	label := strings.TrimSpace(rule[:idx])
	payload := strings.TrimSpace(rule[idx+1:])
	if payload == "" {
		return false
	}
	label = strings.Trim(label, "*_`[]（）() ")
	label = strings.ToLower(strings.TrimSpace(label))
	_, ok := map[string]struct{}{
		"叙事节奏": {}, "描写方式": {}, "对话要求": {}, "情感表达": {}, "文字风格": {},
		"史诗感营造": {}, "旅途叙事": {},
		"内心描写": {}, "互动细节": {}, "对话节奏": {}, "场景氛围": {},
		"氛围营造": {}, "对话风格": {},
		"叙述声音": {}, "叙述距离": {}, "词汇": {}, "语域": {}, "句法": {}, "节奏": {},
		"意象": {}, "感官": {}, "修辞": {}, "段落组织": {}, "留白": {},
		"narrative voice": {}, "narrative distance": {}, "diction": {}, "register": {},
		"syntax": {}, "rhythm": {}, "imagery": {}, "sensory detail": {},
		"dialogue style": {}, "paragraphing": {}, "tone": {},
	}[label]
	return ok && configuredStylePayloadIsSurfaceOnly(payload)
}

// configuredStylePayloadIsSurfaceOnly closes the second half of the boundary:
// an allow-listed surface label must not be used as a carrier for event,
// character-decision or canon mutations. Presentation verbs such as "描写" and
// "呈现" deliberately remain valid; only explicit semantic scheduling/mutation
// language is rejected. The frozen plan is the sole authority for those facts.
func configuredStylePayloadIsSurfaceOnly(payload string) bool {
	payload = strings.ToLower(strings.TrimSpace(payload))
	if payload == "" {
		return false
	}
	for _, marker := range []string{
		"本章", "下一章", "下章", "每章", "章末必须", "必须发生", "必须出现", "必须揭示",
		"不得发生", "不得出现", "this chapter", "next chapter", "each chapter",
		"must happen", "must occur", "must appear", "must reveal", "杀死", "复活", "kill ", "resurrect ",
	} {
		if strings.Contains(payload, marker) {
			return false
		}
	}
	mutations := []string{
		"新增", "增加", "添加", "加入", "引入", "安排", "埋设", "删除", "删去", "移除",
		"替换", "改写", "改动", "更改", "重排", "调换", "提前", "延后", "推迟",
		"add ", "insert ", "introduce ", "remove ", "delete ", "replace ",
		"rewrite ", "change ", "reorder ", "delay ", "advance ",
	}
	semanticObjects := []string{
		"事件", "情节", "剧情", "桥段", "场景", "角色", "人物", "主角", "配角", "决定", "选择",
		"行动", "关系", "线索", "伏笔", "真相", "冲突", "结局", "死亡", "背叛", "能力", "设定",
		"事实", "因果", "知识边界", "地点", "时间线", "目标", "动机", "世界规则",
		"event", "plot", "scene", "character", "decision", "choice", "relationship", "clue",
		"foreshadow", "truth", "conflict", "ending", "death", "betrayal", "ability", "canon",
		"fact", "timeline", "location", "motivation",
	}
	for _, mutation := range mutations {
		if !strings.Contains(payload, mutation) {
			continue
		}
		for _, object := range semanticObjects {
			if strings.Contains(payload, object) {
				return false
			}
		}
	}
	return true
}

func configuredStyleProfileFromContext(result map[string]any) *configuredStyleProfile {
	for _, raw := range contextValuesForKey(result, "configured_style") {
		switch value := raw.(type) {
		case *configuredStyleProfile:
			if value != nil && len(value.Rules) > 0 {
				copy := *value
				copy.Rules = append([]string(nil), value.Rules...)
				return &copy
			}
		case configuredStyleProfile:
			if len(value.Rules) > 0 {
				copy := value
				copy.Rules = append([]string(nil), value.Rules...)
				return &copy
			}
		}
		var profile configuredStyleProfile
		encoded, err := json.Marshal(raw)
		if err == nil && json.Unmarshal(encoded, &profile) == nil && len(profile.Rules) > 0 {
			return &profile
		}
	}
	return nil
}

func styleStatsFromContext(result map[string]any) *stylestat.Stats {
	for _, raw := range contextValuesForKey(result, "style_stats") {
		switch value := raw.(type) {
		case *stylestat.Stats:
			if value != nil {
				return value
			}
		case stylestat.Stats:
			copy := value
			return &copy
		}
		var stats stylestat.Stats
		encoded, err := json.Marshal(raw)
		if err == nil && json.Unmarshal(encoded, &stats) == nil && stats.Chapters > 0 {
			return &stats
		}
	}
	return nil
}

func newDraftSerialStyleMemory(stats *stylestat.Stats) *draftSerialStyleMemory {
	if stats == nil || stats.Chapters <= 0 {
		return nil
	}
	memory := &draftSerialStyleMemory{
		BasedOnChapters: stats.Chapters,
		UsagePolicy:     "这些是已验收正文的表层复读事实，不是节拍配方。必要专名与人物口癖可保留；其余应从本场新的感知、动作、关系或句法入口重写，禁止只换同义词。若与冻结硬合同冲突，以硬合同为准。",
	}
	for _, phrase := range stats.TopPhrases {
		if text := strings.TrimSpace(phrase.Text); text != "" {
			memory.AvoidPhrases = append(memory.AvoidPhrases, text)
			if len(memory.AvoidPhrases) >= 4 {
				break
			}
		}
	}
	for _, sentence := range stats.RepeatedSentences {
		if text := strings.TrimSpace(sentence.Text); text != "" {
			memory.AvoidExactLines = append(memory.AvoidExactLines, text)
			if len(memory.AvoidExactLines) >= 3 {
				break
			}
		}
	}
	for _, pattern := range stats.Patterns {
		if pattern.PerChapter < 1 {
			continue
		}
		if name := strings.TrimSpace(pattern.Name); name != "" {
			memory.PatternAlerts = append(memory.PatternAlerts, name)
			if len(memory.PatternAlerts) >= 3 {
				break
			}
		}
	}
	if stats.OpeningTimeRate >= 0.6 {
		memory.OpeningGuard = "历史章节过多从夜晚、清晨、天亮或醒来起笔；若本章硬开场未指定这种形式，改从正在发生的刺激、关系动作或异常感知进入。"
	}
	if stats.Ending.ShortRatio >= 0.75 {
		memory.EndingGuard = "历史章节短句斩断式收尾已高度集中；在完整兑现 ending_consequence_contract 的前提下，优先用动作余波、对白余音或仍在变化的现场结束。"
	}
	if len(memory.AvoidPhrases) == 0 && len(memory.AvoidExactLines) == 0 &&
		len(memory.PatternAlerts) == 0 && memory.OpeningGuard == "" && memory.EndingGuard == "" {
		return nil
	}
	return memory
}

// applyConfiguredStyleOverlay supplies the selected render asset to historical
// immutable v11 contexts without rewriting their sealed bytes. The current
// render input digest binds the same style body and this protocol before the
// provider permit is armed, so this cannot become a live planning override.
func applyConfiguredStyleOverlay(raw json.RawMessage, style, styleBody string) (json.RawMessage, error) {
	configured := configuredStyleProfileFromMarkdown(style, styleBody)
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("decode render context for configured style overlay: %w", err)
	}
	packet, _, err := aigc.FindUniqueProseRenderPacket(payload)
	if err != nil {
		return nil, err
	}
	contract, _ := packet["style_contract"].(map[string]any)
	if contract == nil {
		if configured == nil {
			return raw, nil
		}
		contract = map[string]any{}
		packet["style_contract"] = contract
	}
	// The effective configured style is replace-or-delete. Frozen historical
	// packets may carry either the old nested projection or the current flat
	// fields; none of that provenance may survive into a different/empty asset.
	delete(contract, "configured_style")
	delete(contract, "configured_style_id")
	delete(contract, "configured_style_name")
	delete(contract, "configured_style_rules")
	refs := styleContractStringSlice(contract["source_refs"])
	refs = slices.DeleteFunc(refs, configuredStyleSourceRef)
	if len(refs) == 0 {
		delete(contract, "source_refs")
	} else {
		contract["source_refs"] = limitRenderStrings(compactStrings(refs), 10)
	}
	contract["version"] = 3
	if configured != nil {
		contract["configured_style_id"] = configured.ID
		contract["configured_style_name"] = configured.Name
		contract["configured_style_rules"] = append([]string(nil), configured.Rules...)
		contract["usage_policy"] = "冻结 plan、mandatory_beats、事实、人物决定与知识边界始终优先，风格合同只能改变讲述方式。用户最新规则优先于 configured_style_rules；dialogue/romance/system 边界、anti_ai_rules 与 taboos 必须遵守；soft_craft_rules 和 soft_cards 只是写法候选。serial_style_memory 只防逐字复读与章式同构，不得机械换同义词、轮换句长或为求不同新增剧情。"
		refs = append(refs, configured.SourceRef)
		contract["source_refs"] = limitRenderStrings(compactStrings(refs), 10)
	}
	overlaid, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode configured style render overlay: %w", err)
	}
	return overlaid, nil
}

func configuredStyleSourceRef(ref string) bool {
	ref = filepath.ToSlash(strings.TrimSpace(ref))
	return strings.HasPrefix(ref, "assets/styles/") && strings.HasSuffix(ref, ".md")
}

func styleContractStringSlice(raw any) []string {
	switch values := raw.(type) {
	case []string:
		return append([]string(nil), values...)
	case []any:
		out := make([]string, 0, len(values))
		for _, value := range values {
			if text, ok := value.(string); ok && strings.TrimSpace(text) != "" {
				out = append(out, text)
			}
		}
		return out
	default:
		return nil
	}
}

func compactSoftStyleContractStrings(values []string, limit int) []string {
	out := make([]string, 0, min(limit, len(values)))
	for _, value := range values {
		value = firstRenderClause(value)
		if value == "" || strings.Contains(value, "chapter_function_repetition") {
			continue
		}
		// Old project snapshots may still carry the former hard quota. Normalize it
		// at projection time so an existing book benefits without rewriting canon or
		// mutating its output directory.
		if strings.Contains(value, "每章至少让 2 个现场物件") ||
			(strings.Contains(value, "scene_anchors") && strings.Contains(value, "至少一次改变")) {
			value = "可从 soft_scene_anchors 择取0—2个真正改变信息、关系或代价的现场承载物；没有合适项可不用，禁止逐项回收。"
		}
		if !slicesContainsString(out, value) {
			out = append(out, value)
		}
		if len(out) >= limit {
			break
		}
	}
	return out
}

func slicesContainsString(values []string, candidate string) bool {
	for _, value := range values {
		if value == candidate {
			return true
		}
	}
	return false
}

func compactStyleContractStrings(values []string, limit int) []string {
	out := make([]string, 0, min(limit, len(values)))
	for _, value := range values {
		if len(out) >= limit {
			break
		}
		if value = firstRenderClause(value); value != "" && !strings.Contains(value, "chapter_function_repetition") {
			out = append(out, value)
		}
	}
	return compactStrings(out)
}

func genreStyleProfileFromContext(result map[string]any) *genreStyleProfile {
	for _, raw := range contextValuesForKey(result, "genre_style_profile") {
		var profile genreStyleProfile
		encoded, err := json.Marshal(raw)
		if err == nil && json.Unmarshal(encoded, &profile) == nil && strings.TrimSpace(profile.ID) != "" {
			return &profile
		}
	}
	return nil
}

func writingEngineFromContext(result map[string]any) *domain.WritingCompiled {
	for _, raw := range contextValuesForKey(result, "writing_engine") {
		switch value := raw.(type) {
		case *domain.WritingCompiled:
			if value != nil {
				return value
			}
		case domain.WritingCompiled:
			copy := value
			return &copy
		}
		var compiled domain.WritingCompiled
		encoded, err := json.Marshal(raw)
		if err == nil && json.Unmarshal(encoded, &compiled) == nil {
			return &compiled
		}
	}
	return nil
}

func contextValuesForKey(result map[string]any, key string) []any {
	values := make([]any, 0, 5)
	if value, ok := result[key]; ok {
		values = append(values, value)
	}
	for _, sectionName := range []string{"working_memory", "episodic_memory", "reference_pack", "selected_memory"} {
		if section, ok := result[sectionName].(map[string]any); ok {
			if value, exists := section[key]; exists {
				values = append(values, value)
			}
		}
	}
	return values
}
