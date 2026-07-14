package tools

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/rules"
)

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
	Version              int                   `json:"version"`
	ProfileID            string                `json:"profile_id,omitempty"`
	ProfileName          string                `json:"profile_name,omitempty"`
	Tone                 string                `json:"tone,omitempty"`
	SourceIDs            []string              `json:"source_ids,omitempty"`
	ActiveRules          []string              `json:"active_rules,omitempty"`
	AntiAIRules          []string              `json:"anti_ai_rules,omitempty"`
	Taboos               []string              `json:"taboos,omitempty"`
	DialogueBreathPolicy string                `json:"dialogue_breath_policy,omitempty"`
	SceneFlowPolicy      string                `json:"scene_flow_policy,omitempty"`
	HumorPolicy          string                `json:"humor_policy,omitempty"`
	GrowthPolicy         string                `json:"growth_policy,omitempty"`
	RomancePolicy        string                `json:"romance_policy,omitempty"`
	SystemPolicy         string                `json:"system_policy,omitempty"`
	Cards                []draftGenreStyleCard `json:"cards,omitempty"`
	SourceRefs           []string              `json:"source_refs,omitempty"`
	UsagePolicy          string                `json:"usage_policy"`
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
	if profile == nil && engine == nil {
		return nil
	}
	contract := &draftStyleContract{
		Version:     1,
		UsagePolicy: "用户最新规则优先；本合同只约束本章题材语域、口述气口和可读性，不追加剧情义务，不要求卡片齐全或统计次数。",
	}
	if engine != nil {
		for _, feature := range engine.EnabledFeatures {
			if id := strings.TrimSpace(feature.ID); id != "" {
				contract.SourceIDs = append(contract.SourceIDs, id)
			}
		}
		contract.SourceIDs = limitRenderStrings(compactStrings(contract.SourceIDs), 12)
		contract.ActiveRules = compactStyleContractStrings(engine.ActiveRules, 12)
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
		contract.SourceRefs = limitRenderStrings(compactStrings(profile.SourceRefs), 10)
		for _, card := range profile.Cards {
			if len(contract.Cards) >= 8 || strings.TrimSpace(card.ID) == "" {
				break
			}
			contract.Cards = append(contract.Cards, draftGenreStyleCard{
				ID:    strings.TrimSpace(card.ID),
				Move:  firstRenderClause(card.Move),
				Avoid: firstRenderClause(card.Avoid),
			})
		}
	}
	return contract
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
