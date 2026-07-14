package store

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

// WritingAssetStore 管理本书长期写法资产。
type WritingAssetStore struct{ io *IO }

func NewWritingAssetStore(io *IO) *WritingAssetStore { return &WritingAssetStore{io: io} }

const writingAssetsPath = "meta/writing_assets.json"
const writingFeedbackCompileLimit = 12
const writingFeedbackRenderLimit = 40

func (s *WritingAssetStore) Load() (*domain.WritingAssetLibrary, error) {
	var lib domain.WritingAssetLibrary
	if err := s.io.ReadJSON(writingAssetsPath, &lib); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return &lib, nil
}

func (s *WritingAssetStore) Compile(sampleLimit int) (*domain.WritingCompiled, error) {
	return s.CompileForScope(sampleLimit, nil)
}

func (s *WritingAssetStore) CompileForScope(sampleLimit int, scope *domain.WritingBinding) (*domain.WritingCompiled, error) {
	lib, err := s.Load()
	if err != nil || lib == nil {
		return nil, err
	}
	compiled := CompileWritingAssetsForScope(*lib, sampleLimit, scope)
	return &compiled, nil
}

func (s *WritingAssetStore) Save(lib domain.WritingAssetLibrary) error {
	if lib.Version == 0 {
		lib.Version = 1
	}
	compiled := CompileWritingAssets(lib, 8)
	lib.Compiled = &compiled
	lib.LastCompiled = time.Now().Format(time.RFC3339)
	return s.io.WithWriteLock(func() error {
		if err := s.io.WriteJSONUnlocked(writingAssetsPath, lib); err != nil {
			return err
		}
		return s.io.WriteMarkdownUnlocked("meta/writing_assets.md", renderWritingAssets(lib))
	})
}

// SeedDefaults 注入本书可编辑的基础写法资产，不覆盖用户已有特征。
func (s *WritingAssetStore) SeedDefaults() (featuresAdded, presetsAdded, bindingsAdded int, err error) {
	now := time.Now().Format(time.RFC3339)
	defaults := defaultWritingAssetFeatures(now)
	defaultPreset := domain.WritingPreset{
		ID:          "default:human_readability",
		Name:        "人工感与章节可读性基线",
		FeatureIDs:  defaultWritingAssetFeatureIDs(defaults),
		Scope:       "book",
		Description: "物件证据、主观误判、段落功能差异、单章目标代价和标点声口的默认组合。",
	}
	defaultBinding := domain.WritingBinding{Scope: "book", PresetID: defaultPreset.ID}

	err = s.io.WithWriteLock(func() error {
		var lib domain.WritingAssetLibrary
		if err := s.io.ReadJSONUnlocked(writingAssetsPath, &lib); err != nil && !os.IsNotExist(err) {
			return err
		}
		if lib.Version == 0 {
			lib.Version = 1
		}
		for _, feature := range defaults {
			if writingFeatureExists(lib.Features, feature.ID) {
				continue
			}
			lib.Features = append(lib.Features, feature)
			featuresAdded++
		}
		if !writingPresetExists(lib.Presets, defaultPreset.ID) {
			lib.Presets = append(lib.Presets, defaultPreset)
			presetsAdded++
		}
		if !writingBindingExists(lib.Bindings, defaultBinding) {
			lib.Bindings = append(lib.Bindings, defaultBinding)
			bindingsAdded++
		}
		compiled := CompileWritingAssets(lib, 8)
		lib.Compiled = &compiled
		lib.LastCompiled = now
		if err := s.io.WriteJSONUnlocked(writingAssetsPath, lib); err != nil {
			return err
		}
		return s.io.WriteMarkdownUnlocked("meta/writing_assets.md", renderWritingAssets(lib))
	})
	return featuresAdded, presetsAdded, bindingsAdded, err
}

// ApplyStyleRules 把旧的弧级 style_rules 兼容沉淀为可见 feature pool。
func (s *WritingAssetStore) ApplyStyleRules(rules domain.WritingStyleRules) error {
	return s.io.WithWriteLock(func() error {
		var lib domain.WritingAssetLibrary
		if err := s.io.ReadJSONUnlocked(writingAssetsPath, &lib); err != nil && !os.IsNotExist(err) {
			return err
		}
		if lib.Version == 0 {
			lib.Version = 1
		}
		source := fmt.Sprintf("arc:v%02da%02d", rules.Volume, rules.Arc)
		now := time.Now().Format(time.RFC3339)
		for _, rule := range rules.Prose {
			lib.Features = upsertWritingFeature(lib.Features, domain.WritingFeature{
				ID:          writingFeatureID("prose", rule),
				Name:        truncateRunesLocal(rule, 24),
				Category:    "prose",
				Description: rule,
				Enabled:     true,
				Rules:       []string{rule},
				Source:      source,
				UpdatedAt:   now,
			})
		}
		for _, voice := range rules.Dialogue {
			for _, rule := range voice.Rules {
				desc := voice.Name + "：" + rule
				lib.Features = upsertWritingFeature(lib.Features, domain.WritingFeature{
					ID:          writingFeatureID("dialogue", desc),
					Name:        truncateRunesLocal(desc, 24),
					Category:    "dialogue",
					Description: desc,
					Enabled:     true,
					Rules:       []string{desc},
					Source:      source,
					UpdatedAt:   now,
				})
			}
		}
		for _, taboo := range rules.Taboos {
			lib.Features = upsertWritingFeature(lib.Features, domain.WritingFeature{
				ID:          writingFeatureID("taboo", taboo),
				Name:        truncateRunesLocal(taboo, 24),
				Category:    "taboo",
				Description: taboo,
				Enabled:     true,
				Rules:       []string{"避免：" + taboo},
				Source:      source,
				UpdatedAt:   now,
			})
		}
		compiled := CompileWritingAssets(lib, 8)
		lib.Compiled = &compiled
		lib.LastCompiled = now
		if err := s.io.WriteJSONUnlocked(writingAssetsPath, lib); err != nil {
			return err
		}
		return s.io.WriteMarkdownUnlocked("meta/writing_assets.md", renderWritingAssets(lib))
	})
}

// ApplyReviewFeedback 把章节/全局审阅里可复用的写法反馈沉淀进写法资产库。
// reviews/ 仍保存完整事实；这里负责让后续 writer 的 writing_engine 能看到历史反馈。
func (s *WritingAssetStore) ApplyReviewFeedback(r domain.ReviewEntry, finalVerdict, escalationReason string) (feedbackUpserted, featuresUpserted int, err error) {
	now := time.Now().Format(time.RFC3339)
	feedback := reviewFeedbackEntries(r, finalVerdict, escalationReason, now)
	source := reviewFeedbackSourcePath(r)
	err = s.io.WithWriteLock(func() error {
		var lib domain.WritingAssetLibrary
		if err := s.io.ReadJSONUnlocked(writingAssetsPath, &lib); err != nil && !os.IsNotExist(err) {
			return err
		}
		if lib.Version == 0 {
			lib.Version = 1
		}
		// 当前 reviews/<chapter>.json 是该章最新事实；同源旧反馈必须先撤下，
		// 否则复审纠正后的建议仍会长期污染 writing_engine 与 RAG。早期版本
		// 使用 reviews/NN.md，并可能留下空壳反馈；一并迁移清理。
		legacySource := strings.TrimSuffix(source, ".json") + ".md"
		lib.Feedback = slices.DeleteFunc(lib.Feedback, func(entry domain.WritingFeedback) bool {
			empty := strings.TrimSpace(entry.Signal) == "" && strings.TrimSpace(entry.Suggestion) == "" && strings.TrimSpace(entry.Rule) == ""
			return empty || entry.Source == source || entry.Source == legacySource
		})
		lib.Features = slices.DeleteFunc(lib.Features, func(feature domain.WritingFeature) bool {
			return feature.Source == source || feature.Source == legacySource
		})
		for _, entry := range feedback {
			var added bool
			lib.Feedback, added = upsertWritingFeedback(lib.Feedback, entry)
			if added {
				feedbackUpserted++
			}
			if strings.TrimSpace(entry.Rule) == "" {
				continue
			}
			lib.Features = upsertWritingFeature(lib.Features, domain.WritingFeature{
				ID:          writingFeatureID(entry.Category, "review_feedback\x00"+entry.Rule),
				Name:        truncateRunesLocal(entry.Rule, 24),
				Category:    entry.Category,
				Description: entry.Signal,
				Enabled:     true,
				Weight:      writingFeedbackWeight(entry.Severity),
				Rules:       []string{entry.Rule},
				Source:      entry.Source,
				UpdatedAt:   now,
			})
			featuresUpserted++
		}
		compiled := CompileWritingAssets(lib, 8)
		lib.Compiled = &compiled
		lib.LastCompiled = now
		if err := s.io.WriteJSONUnlocked(writingAssetsPath, lib); err != nil {
			return err
		}
		return s.io.WriteMarkdownUnlocked("meta/writing_assets.md", renderWritingAssets(lib))
	})
	return feedbackUpserted, featuresUpserted, err
}

func reviewFeedbackEntries(r domain.ReviewEntry, finalVerdict, escalationReason, now string) []domain.WritingFeedback {
	var entries []domain.WritingFeedback
	source := reviewFeedbackSourcePath(r)
	add := func(kind, dimension, severity, signal, evidence, suggestion string) {
		signal = strings.TrimSpace(signal)
		suggestion = strings.TrimSpace(suggestion)
		evidence = strings.TrimSpace(evidence)
		if signal == "" && suggestion == "" {
			return
		}
		category := writingFeedbackCategory(dimension, signal, suggestion)
		rule := writingFeedbackRule(kind, dimension, signal, suggestion)
		entry := domain.WritingFeedback{
			ID:         writingFeedbackID(r.Chapter, r.Scope, kind, dimension, signal, evidence, suggestion),
			Chapter:    r.Chapter,
			Scope:      r.Scope,
			Dimension:  dimension,
			Severity:   severity,
			Category:   category,
			Signal:     signal,
			Evidence:   evidence,
			Suggestion: suggestion,
			Rule:       rule,
			Source:     source,
			CreatedAt:  now,
			UpdatedAt:  now,
		}
		entries = append(entries, entry)
	}

	for i, miss := range r.ContractMisses {
		add("contract_miss", "contract", "warning", fmt.Sprintf("contract 漏项：%s", miss), "", fmt.Sprintf("写作前核对 contract，补足或交代取舍：%s", miss))
		if i >= 5 {
			break
		}
	}
	for _, issue := range r.Issues {
		add("issue", issue.Type, issue.Severity, issue.Description, issue.Evidence, issue.Suggestion)
	}
	for _, dim := range r.Dimensions {
		if dim.Score >= 80 && dim.Verdict == "pass" {
			continue
		}
		severity := "warning"
		if dim.Score < 60 || dim.Verdict == "fail" {
			severity = "error"
		}
		add("dimension", dim.Dimension, severity, fmt.Sprintf("%s 维度 %d 分：%s", dim.Dimension, dim.Score, dim.Comment), "", "")
	}
	if strings.TrimSpace(escalationReason) != "" {
		severity := "warning"
		if finalVerdict == "rewrite" {
			severity = "error"
		}
		add("gate", "review_gate", severity, "审阅门禁升级："+escalationReason, "", "后续写作前优先避开本次门禁升级原因。")
	}
	return entries
}

func reviewFeedbackSourcePath(r domain.ReviewEntry) string {
	switch r.Scope {
	case "arc":
		return fmt.Sprintf("reviews/%02d-arc.json", r.Chapter)
	case "global":
		return fmt.Sprintf("reviews/%02d-global.json", r.Chapter)
	default:
		return fmt.Sprintf("reviews/%02d.json", r.Chapter)
	}
}

func writingFeedbackID(chapter int, scope, kind, dimension, signal, evidence, suggestion string) string {
	h := sha1.Sum([]byte(strings.Join([]string{
		fmt.Sprintf("%d", chapter),
		scope,
		kind,
		dimension,
		strings.TrimSpace(signal),
		strings.TrimSpace(evidence),
		strings.TrimSpace(suggestion),
	}, "\x00")))
	return fmt.Sprintf("review:%02d:%s:%s", chapter, kind, hex.EncodeToString(h[:])[:10])
}

func writingFeedbackCategory(dimension, signal, suggestion string) string {
	text := signal + " " + suggestion
	switch dimension {
	case "ai_voice_detection":
		return "anti_ai"
	case "aesthetic":
		return "prose"
	case "pacing":
		return "pacing"
	case "hook", "foreshadow", "continuity", "consistency", "contract", "review_gate":
		return "structure"
	case "character":
		if containsAny(text, []string{"对话", "台词", "声口", "口吻", "语气"}) {
			return "dialogue"
		}
		return "structure"
	default:
		return "structure"
	}
}

func writingFeedbackRule(kind, dimension, signal, suggestion string) string {
	suggestion = strings.TrimSpace(suggestion)
	if suggestion != "" {
		return "历史审阅反馈：" + suggestion
	}
	signal = strings.TrimSpace(signal)
	if signal == "" {
		return ""
	}
	switch kind {
	case "dimension":
		return "历史审阅维度提醒：" + truncateRunesLocal(signal, 80)
	case "contract_miss":
		return "历史审阅反馈：写作前核对 contract，避免重复漏项。"
	default:
		label := dimension
		if label == "" {
			label = "写法"
		}
		return fmt.Sprintf("历史审阅反馈：避免重复出现[%s] %s", label, truncateRunesLocal(signal, 60))
	}
}

func writingFeedbackWeight(severity string) int {
	switch severity {
	case "critical":
		return 4
	case "error":
		return 3
	case "warning":
		return 2
	default:
		return 1
	}
}

func upsertWritingFeedback(existing []domain.WritingFeedback, next domain.WritingFeedback) ([]domain.WritingFeedback, bool) {
	for i := range existing {
		if existing[i].ID != next.ID {
			continue
		}
		if existing[i].CreatedAt == "" {
			existing[i].CreatedAt = next.CreatedAt
		}
		next.CreatedAt = existing[i].CreatedAt
		existing[i] = next
		return existing, false
	}
	return append(existing, next), true
}

func defaultWritingAssetFeatures(now string) []domain.WritingFeature {
	return []domain.WritingFeature{
		{
			ID:          "default:human:scene_anchors",
			Name:        "现场锚点承担信息",
			Category:    "structure",
			Description: "每章让现场物件或痕迹真正承担信息、关系、代价或钩子。",
			Enabled:     true,
			Rules: []string{
				"每章至少让 2 个现场物件或痕迹承担新信息、关系位移、规则代价或章末钩子。",
				"规划时优先把这些物件写入 scene_anchors，正文中不能只重复名字，至少一次改变读者知道的信息或角色选择。",
			},
			Source:    "default:human_feel_craft",
			UpdatedAt: now,
		},
		{
			ID:          "default:human:subjective_misread",
			Name:        "主观误判可复核",
			Category:    "prose",
			Description: "允许误听、误判、改口和嘴硬，但必须用前文证据修正。",
			Enabled:     true,
			Rules: []string{
				"角色可以误听、误判、嘴硬或临时改口，但后文反转或修正必须能追溯到前文可复核证据。",
				"角色说看见、听见或知道某事时，前文必须有读者也能看到的物理线索、台词、视线或规则提示。",
			},
			Source:    "default:human_feel_craft",
			UpdatedAt: now,
		},
		{
			ID:          "default:anti_ai:function_variance",
			Name:        "段落功能换挡",
			Category:    "anti_ai",
			Description: "降低解释腔和语义平滑，保持段落功能差异。",
			Enabled:     true,
			Rules: []string{
				"连续抽象判断后必须换到动作、物件、感官、对白或选择后果。",
				"不要让每段都是同一种概述、心理、转场功能；动作、对话、物件细节、沉默反应要交替出现。",
			},
			Source:    "default:anti_ai_tone",
			UpdatedAt: now,
		},
		{
			ID:          "default:chapter:goal_cost_info",
			Name:        "单章四问",
			Category:    "structure",
			Description: "每章回答目标、阻力、失败代价和新增信息。",
			Enabled:     true,
			Rules: []string{
				"每章写作前确认主角目标、阻力、失败代价和本章新增信息。",
				"过渡章也必须承担结算、下一目标、信息差、人物反应或新钩子之一，不能只做无信息移动。",
			},
			Source:    "default:writing_techniques_digest",
			UpdatedAt: now,
		},
		{
			ID:          "default:dialogue:punctuation_voice",
			Name:        "标点服务声口",
			Category:    "dialogue",
			Description: "标点承担语气、情绪和信息层级，而不是随机短句化。",
			Enabled:     true,
			Rules: []string{
				"问号、叹号、冒号、分号、破折号、省略号必须分别承担疑问、惊惧、条款分层、打断、迟疑或未尽。",
				"恐慌求救、账单条款、备忘录和规则清单要按人物声口和信息层级调整标点，不能只用句号切平。",
			},
			Source:    "default:writing_techniques_digest",
			UpdatedAt: now,
		},
		{
			ID:          "default:dialogue:breath_groups",
			Name:        "普通口述按完整气口",
			Category:    "dialogue",
			Description: "快节奏来自选择和后果，不来自把人口述切成两三个字一段。",
			Enabled:     true,
			Rules: []string{
				"普通平静口述以一个完整气口承载对象、原因、条件或补充；同一意思不得切成连续 2—4 汉字句号短句。",
				"短答可以短；连续碎断只用于抢险、被打断、惊吓、喘不上气或刻意拒绝，且现场必须给出原因。",
			},
			Source:    "default:genre_style_craft",
			UpdatedAt: now,
		},
		{
			ID:          "default:taboo:dirty_humanizer",
			Name:        "禁止脏码绕检",
			Category:    "taboo",
			Description: "人工感来自场景承载，不来自噪声或乱码。",
			Enabled:     true,
			Rules: []string{
				"避免：为了降低 AIGC 风险插入 OCR 脏码、随机汉字串、稀有名词堆叠、无信息清单或拟声长串。",
				"避免：只替换形容词、随机断句或堆生活废话来制造人工感。",
			},
			Source:    "default:anti_ai_tone",
			UpdatedAt: now,
		},
	}
}

func defaultWritingAssetFeatureIDs(features []domain.WritingFeature) []string {
	ids := make([]string, 0, len(features))
	for _, feature := range features {
		ids = append(ids, feature.ID)
	}
	return ids
}

func writingFeatureExists(features []domain.WritingFeature, id string) bool {
	for _, feature := range features {
		if feature.ID == id {
			return true
		}
	}
	return false
}

func writingPresetExists(presets []domain.WritingPreset, id string) bool {
	for _, preset := range presets {
		if preset.ID == id {
			return true
		}
	}
	return false
}

func writingBindingExists(bindings []domain.WritingBinding, target domain.WritingBinding) bool {
	for _, binding := range bindings {
		if binding.Scope == target.Scope &&
			binding.Volume == target.Volume &&
			binding.Arc == target.Arc &&
			binding.Chapter == target.Chapter &&
			binding.PresetID == target.PresetID &&
			binding.FeatureID == target.FeatureID {
			return true
		}
	}
	return false
}

func CompileWritingAssets(lib domain.WritingAssetLibrary, sampleLimit int) domain.WritingCompiled {
	return CompileWritingAssetsForScope(lib, sampleLimit, nil)
}

func CompileWritingAssetsForScope(lib domain.WritingAssetLibrary, sampleLimit int, scope *domain.WritingBinding) domain.WritingCompiled {
	if sampleLimit <= 0 {
		sampleLimit = 6
	}
	scopedFeatureIDs, scopedPresetIDs, matchedFeatureIDs, matchedPresetIDs := writingBindingSets(lib, scope)
	presetFeatures := make(map[string]struct{})
	for _, preset := range lib.Presets {
		if _, ok := matchedPresetIDs[preset.ID]; !ok {
			continue
		}
		for _, id := range preset.FeatureIDs {
			matchedFeatureIDs[id] = struct{}{}
			presetFeatures[id] = struct{}{}
		}
	}

	enabledIDs := make(map[string]struct{})
	compiled := domain.WritingCompiled{}
	for _, feature := range lib.Features {
		if !feature.Enabled || !writingFeatureHasContent(feature) {
			continue
		}
		if _, scoped := scopedFeatureIDs[feature.ID]; scoped {
			if _, matched := matchedFeatureIDs[feature.ID]; !matched {
				continue
			}
		}
		if _, presetScoped := scopedPresetFeatureIDs(feature.ID, lib.Presets, scopedPresetIDs); presetScoped {
			if _, matched := presetFeatures[feature.ID]; !matched {
				continue
			}
		}
		compiled.EnabledFeatures = append(compiled.EnabledFeatures, feature)
		enabledIDs[feature.ID] = struct{}{}
		for _, rule := range feature.Rules {
			if rule == "" || slices.Contains(compiled.ActiveRules, rule) {
				continue
			}
			compiled.ActiveRules = append(compiled.ActiveRules, rule)
		}
		switch feature.Category {
		case "anti_ai":
			compiled.AntiAIRules = appendUniqueString(compiled.AntiAIRules, feature.Rules...)
		case "taboo":
			compiled.Taboos = appendUniqueString(compiled.Taboos, feature.Rules...)
		}
	}
	for _, sample := range lib.Samples {
		if sample.FeatureID != "" {
			if _, ok := enabledIDs[sample.FeatureID]; !ok {
				continue
			}
		}
		compiled.Samples = append(compiled.Samples, sample)
		if len(compiled.Samples) >= sampleLimit {
			break
		}
	}
	compiled.Feedback = selectRecentWritingFeedback(lib.Feedback, writingFeedbackCompileLimit)
	compiled.Trace = append(compiled.Trace,
		fmt.Sprintf("enabled_features=%d", len(compiled.EnabledFeatures)),
		fmt.Sprintf("active_rules=%d", len(compiled.ActiveRules)),
		fmt.Sprintf("samples=%d", len(compiled.Samples)),
		fmt.Sprintf("feedback=%d", len(compiled.Feedback)),
	)
	if scope != nil && scope.Scope != "" {
		compiled.Trace = append(compiled.Trace, fmt.Sprintf("scope=%s", renderWritingBindingTarget(*scope)))
	}
	return compiled
}

func writingFeatureHasContent(feature domain.WritingFeature) bool {
	if strings.TrimSpace(feature.Name) != "" || strings.TrimSpace(feature.Description) != "" || len(feature.SampleIDs) > 0 {
		return true
	}
	for _, rule := range feature.Rules {
		if strings.TrimSpace(rule) != "" {
			return true
		}
	}
	return false
}

func (s *WritingAssetStore) SaveTrial(scope domain.WritingBinding, brief string, compiled domain.WritingCompiled) (string, error) {
	stamp := time.Now().Format("20060102-150405-000000000")
	rel := fmt.Sprintf("meta/writing_trials/%s.md", stamp)
	return rel, s.io.WriteMarkdown(rel, renderWritingTrial(scope, brief, compiled))
}

func writingBindingSets(lib domain.WritingAssetLibrary, scope *domain.WritingBinding) (map[string]struct{}, map[string]struct{}, map[string]struct{}, map[string]struct{}) {
	scopedFeatures := make(map[string]struct{})
	scopedPresets := make(map[string]struct{})
	matchedFeatures := make(map[string]struct{})
	matchedPresets := make(map[string]struct{})
	for _, binding := range lib.Bindings {
		if binding.FeatureID != "" {
			scopedFeatures[binding.FeatureID] = struct{}{}
		}
		if binding.PresetID != "" {
			scopedPresets[binding.PresetID] = struct{}{}
		}
		if !writingBindingMatches(binding, scope) {
			continue
		}
		if binding.FeatureID != "" {
			matchedFeatures[binding.FeatureID] = struct{}{}
		}
		if binding.PresetID != "" {
			matchedPresets[binding.PresetID] = struct{}{}
		}
	}
	return scopedFeatures, scopedPresets, matchedFeatures, matchedPresets
}

func scopedPresetFeatureIDs(featureID string, presets []domain.WritingPreset, scopedPresetIDs map[string]struct{}) (string, bool) {
	for _, preset := range presets {
		if _, ok := scopedPresetIDs[preset.ID]; !ok {
			continue
		}
		for _, id := range preset.FeatureIDs {
			if id == featureID {
				return preset.ID, true
			}
		}
	}
	return "", false
}

func writingBindingMatches(binding domain.WritingBinding, scope *domain.WritingBinding) bool {
	if binding.Scope == "" || binding.Scope == "book" {
		return true
	}
	if scope == nil {
		return false
	}
	switch binding.Scope {
	case "volume":
		return binding.Volume == 0 || binding.Volume == scope.Volume
	case "arc":
		return (binding.Volume == 0 || binding.Volume == scope.Volume) && (binding.Arc == 0 || binding.Arc == scope.Arc)
	case "chapter":
		return scope.Scope == "chapter" && (binding.Chapter == 0 || binding.Chapter == scope.Chapter)
	case "trial":
		return scope.Scope == "trial"
	default:
		return false
	}
}

func upsertWritingFeature(features []domain.WritingFeature, next domain.WritingFeature) []domain.WritingFeature {
	for i := range features {
		if features[i].ID == next.ID {
			if next.Name != "" {
				features[i].Name = next.Name
			}
			if next.Category != "" {
				features[i].Category = next.Category
			}
			if next.Description != "" {
				features[i].Description = next.Description
			}
			if len(next.Rules) > 0 {
				features[i].Rules = appendUniqueString(features[i].Rules, next.Rules...)
			}
			if next.Weight > features[i].Weight {
				features[i].Weight = next.Weight
			}
			if len(next.SampleIDs) > 0 {
				features[i].SampleIDs = appendUniqueString(features[i].SampleIDs, next.SampleIDs...)
			}
			if next.Source != "" {
				features[i].Source = next.Source
			}
			features[i].UpdatedAt = next.UpdatedAt
			return features
		}
	}
	return append(features, next)
}

func appendUniqueString(dst []string, items ...string) []string {
	seen := make(map[string]struct{}, len(dst)+len(items))
	for _, v := range dst {
		seen[v] = struct{}{}
	}
	for _, item := range items {
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		dst = append(dst, item)
	}
	return dst
}

func writingFeatureID(category, text string) string {
	h := sha1.Sum([]byte(category + "\x00" + text))
	return category + ":" + hex.EncodeToString(h[:])[:10]
}

func selectRecentWritingFeedback(feedback []domain.WritingFeedback, limit int) []domain.WritingFeedback {
	if len(feedback) == 0 || limit <= 0 {
		return nil
	}
	start := len(feedback) - limit
	if start < 0 {
		start = 0
	}
	out := make([]domain.WritingFeedback, 0, len(feedback)-start)
	for i := len(feedback) - 1; i >= start; i-- {
		out = append(out, feedback[i])
	}
	return out
}

func renderWritingAssets(lib domain.WritingAssetLibrary) string {
	var b strings.Builder
	b.WriteString("# 写法资产库\n\n")
	fmt.Fprintf(&b, "- 特征数：%d\n", len(lib.Features))
	fmt.Fprintf(&b, "- 样本数：%d\n", len(lib.Samples))
	fmt.Fprintf(&b, "- 历史反馈数：%d\n", len(lib.Feedback))
	fmt.Fprintf(&b, "- 组合数：%d\n", len(lib.Presets))
	fmt.Fprintf(&b, "- 绑定数：%d\n", len(lib.Bindings))
	if lib.LastCompiled != "" {
		fmt.Fprintf(&b, "- 最近编译：%s\n", lib.LastCompiled)
	}
	b.WriteString("\n## 特征池\n\n")
	for _, f := range lib.Features {
		status := "停用"
		if f.Enabled {
			status = "启用"
		}
		fmt.Fprintf(&b, "- **[%s] %s**（%s）：%s\n", f.Category, f.Name, status, f.Description)
		if f.Source != "" || f.Weight > 0 {
			fmt.Fprintf(&b, "  - 来源/权重：%s / %d\n", f.Source, f.Weight)
		}
		for _, rule := range f.Rules {
			fmt.Fprintf(&b, "  - 规则：%s\n", rule)
		}
	}
	if len(lib.Feedback) > 0 {
		b.WriteString("\n## 历史反馈沉淀\n\n")
		for _, item := range selectRecentWritingFeedback(lib.Feedback, writingFeedbackRenderLimit) {
			scope := item.Scope
			if scope == "" {
				scope = "chapter"
			}
			fmt.Fprintf(&b, "- **第%d章 / %s / %s / %s**：%s\n", item.Chapter, scope, item.Dimension, item.Severity, item.Signal)
			if item.Suggestion != "" {
				fmt.Fprintf(&b, "  - 建议：%s\n", item.Suggestion)
			}
			if item.Rule != "" {
				fmt.Fprintf(&b, "  - 沉淀规则：%s\n", item.Rule)
			}
			if item.Evidence != "" {
				fmt.Fprintf(&b, "  - 证据：%s\n", truncateRunesLocal(item.Evidence, 120))
			}
			if item.Source != "" {
				fmt.Fprintf(&b, "  - 来源：%s\n", item.Source)
			}
		}
	}
	if len(lib.Presets) > 0 {
		b.WriteString("\n## 写法组合\n\n")
		for _, p := range lib.Presets {
			fmt.Fprintf(&b, "- **%s**（%s）：%s\n", p.ID, p.Name, p.Description)
			if len(p.FeatureIDs) > 0 {
				fmt.Fprintf(&b, "  - 特征：%s\n", strings.Join(p.FeatureIDs, "、"))
			}
		}
	}
	if len(lib.Bindings) > 0 {
		b.WriteString("\n## 绑定范围\n\n")
		for _, binding := range lib.Bindings {
			target := binding.FeatureID
			if binding.PresetID != "" {
				target = binding.PresetID
			}
			fmt.Fprintf(&b, "- %s -> %s\n", renderWritingBindingTarget(binding), target)
		}
	}
	if lib.Compiled != nil {
		b.WriteString("\n## 当前编译结果\n\n")
		for _, rule := range lib.Compiled.ActiveRules {
			fmt.Fprintf(&b, "- %s\n", rule)
		}
	}
	return b.String()
}

func renderWritingTrial(scope domain.WritingBinding, brief string, compiled domain.WritingCompiled) string {
	var b strings.Builder
	b.WriteString("# 写法试写任务\n\n")
	fmt.Fprintf(&b, "- 范围：%s\n", renderWritingBindingTarget(scope))
	if brief != "" {
		fmt.Fprintf(&b, "- 试写目标：%s\n", brief)
	}
	fmt.Fprintf(&b, "- 启用特征：%d\n", len(compiled.EnabledFeatures))
	fmt.Fprintf(&b, "- 活跃规则：%d\n", len(compiled.ActiveRules))
	fmt.Fprintf(&b, "- 样本锚点：%d\n", len(compiled.Samples))
	b.WriteString("\n## Writer 指令\n\n")
	b.WriteString("按以下写法资产试写 800-1200 字正文片段。只学习样本的节奏、视角、句法和取景方式，不搬运原句，不把规则解释成旁白。\n")
	if brief != "" {
		fmt.Fprintf(&b, "\n试写目标：%s\n", brief)
	}
	b.WriteString("\n## 活跃规则\n\n")
	for _, rule := range compiled.ActiveRules {
		fmt.Fprintf(&b, "- %s\n", rule)
	}
	if len(compiled.AntiAIRules) > 0 {
		b.WriteString("\n## 反 AI 规则\n\n")
		for _, rule := range compiled.AntiAIRules {
			fmt.Fprintf(&b, "- %s\n", rule)
		}
	}
	if len(compiled.Taboos) > 0 {
		b.WriteString("\n## 禁忌\n\n")
		for _, rule := range compiled.Taboos {
			fmt.Fprintf(&b, "- %s\n", rule)
		}
	}
	if len(compiled.Samples) > 0 {
		b.WriteString("\n## 样本锚点\n\n")
		for _, sample := range compiled.Samples {
			label := sample.ID
			if sample.Source != "" {
				label += " / " + sample.Source
			}
			fmt.Fprintf(&b, "### %s\n\n%s\n\n", label, strings.TrimSpace(sample.Text))
		}
	}
	if len(compiled.Trace) > 0 {
		b.WriteString("## 编译 trace\n\n")
		for _, item := range compiled.Trace {
			fmt.Fprintf(&b, "- %s\n", item)
		}
	}
	return b.String()
}

func renderWritingBindingTarget(binding domain.WritingBinding) string {
	switch binding.Scope {
	case "volume":
		return fmt.Sprintf("volume:%d", binding.Volume)
	case "arc":
		return fmt.Sprintf("volume:%d/arc:%d", binding.Volume, binding.Arc)
	case "chapter":
		return fmt.Sprintf("chapter:%d", binding.Chapter)
	case "trial":
		return "trial"
	default:
		return "book"
	}
}

func truncateRunesLocal(s string, n int) string {
	rs := []rune(strings.TrimSpace(s))
	if len(rs) <= n {
		return string(rs)
	}
	return string(rs[:n]) + "..."
}
