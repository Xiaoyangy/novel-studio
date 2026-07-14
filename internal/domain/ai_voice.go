package domain

import "strings"

const AIVoiceChapterFunctionRepetitionRule = "chapter_function_repetition"

// AphorismHit 是反 AI 腔规则命中的格言/宣言式句子。
type AphorismHit struct {
	Rule      string `json:"rule"`
	Paragraph int    `json:"paragraph"`
	Sentence  int    `json:"sentence"`
	Text      string `json:"text"`
}

// AIVoiceRedFlag 是确定性规则引擎给 Editor 的红旗。
type AIVoiceRedFlag struct {
	Rule        string  `json:"rule"`
	Severity    string  `json:"severity"`
	Paragraph   int     `json:"paragraph,omitempty"`
	Sentence    int     `json:"sentence,omitempty"`
	Evidence    string  `json:"evidence,omitempty"`
	Actual      float64 `json:"actual,omitempty"`
	Limit       float64 `json:"limit,omitempty"`
	Suggestion  string  `json:"suggestion,omitempty"`
	Replacement string  `json:"replacement,omitempty"`
}

// IsAIVoicePlanningAdvice identifies a future-facing chapter-shape note. It is
// rule-based rather than severity-only so reports persisted before the rule
// changed from warning to info remain non-blocking when reloaded.
func IsAIVoicePlanningAdvice(flag AIVoiceRedFlag) bool {
	return strings.TrimSpace(flag.Rule) == AIVoiceChapterFunctionRepetitionRule
}

// IsAdvisoryAIVoiceFlag identifies diagnostics that must not alter the current
// chapter score, label, rewrite queue, or prose-facing repair rules.
func IsAdvisoryAIVoiceFlag(flag AIVoiceRedFlag) bool {
	if IsAIVoicePlanningAdvice(flag) {
		return true
	}
	switch strings.TrimSpace(flag.Severity) {
	case "info", "note":
		return true
	default:
		return false
	}
}

// ActionableAIVoiceAnalysis returns the current-chapter portion of an analysis.
// Advisory notes remain durable in the review artifact, but must not leak into
// Editor scoring, rewrite briefs, or prose-facing provider contexts.
func ActionableAIVoiceAnalysis(analysis *AIVoiceAnalysis) *AIVoiceAnalysis {
	if analysis == nil {
		return nil
	}
	copy := *analysis
	copy.RedFlags = nil
	for _, flag := range analysis.RedFlags {
		if !IsAdvisoryAIVoiceFlag(flag) {
			copy.RedFlags = append(copy.RedFlags, flag)
		}
	}
	if len(copy.RedFlags) == 0 {
		return nil
	}
	return &copy
}

// AIVoiceScorePoint 记录模型/规则在不同轮次给出的 AI 腔风险。
type AIVoiceScorePoint struct {
	Round  int     `json:"round"`
	Source string  `json:"source"`
	Score  float64 `json:"score"`
	At     string  `json:"at,omitempty"`
}

// ChapterAIVoiceMetrics 是章节级反 AI 腔指标。
type ChapterAIVoiceMetrics struct {
	Chapter             int                 `json:"chapter"`
	FigurativeCount     int                 `json:"figurative_count"`
	FigurativeDensity   float64             `json:"figurative_density"`
	DialogueChars       int                 `json:"dialogue_chars"`
	SupportingDialogue  int                 `json:"supporting_dialogue_chars"`
	DialogueRatio       float64             `json:"dialogue_ratio"`
	ParagraphCount      int                 `json:"paragraph_count"`
	SentenceCount       int                 `json:"sentence_count"`
	AIVoiceScore        float64             `json:"ai_voice_score"`
	ChapterFunction     string              `json:"chapter_function"`
	AphorismHits        []AphorismHit       `json:"aphorism_hits,omitempty"`
	ProtagonistWaver    bool                `json:"protagonist_waver"`
	EndingHookUsed      bool                `json:"ending_hook_used"`
	RevisionRound       int                 `json:"revision_round"`
	BeforeAfterDiff     string              `json:"before_after_diff,omitempty"`
	AIVoiceScoreHistory []AIVoiceScorePoint `json:"ai_voice_score_history,omitempty"`
	GeneratedAt         string              `json:"generated_at,omitempty"`
}

// AIVoiceAnalysis 是规则引擎输出给 Editor 的红旗 JSON。
type AIVoiceAnalysis struct {
	Chapter     int                   `json:"chapter"`
	BodySHA256  string                `json:"body_sha256,omitempty"`
	Label       string                `json:"label"`
	Summary     string                `json:"summary"`
	Metrics     ChapterAIVoiceMetrics `json:"metrics"`
	RedFlags    []AIVoiceRedFlag      `json:"red_flags,omitempty"`
	GeneratedAt string                `json:"generated_at,omitempty"`
}

// SamplingCandidate 记录 Writer 三采样单个候选的确定性评分。
type SamplingCandidate struct {
	Index             int     `json:"index"`
	ContentHash       string  `json:"content_hash"`
	RoughnessScore    float64 `json:"roughness_score"`
	FigurativeDensity float64 `json:"figurative_density"`
	DialogueRatio     float64 `json:"dialogue_ratio"`
	AphorismHitCount  int     `json:"aphorism_hit_count"`
	ProtagonistWaver  bool    `json:"protagonist_waver"`
	ChapterFunction   string  `json:"chapter_function"`
	AIVoiceScore      float64 `json:"ai_voice_score"`
}

// SamplingRecord 记录 Writer 三采样决策。
type SamplingRecord struct {
	Chapter       int                 `json:"chapter"`
	SelectedIndex int                 `json:"selected_index"`
	Candidates    []SamplingCandidate `json:"candidates"`
	GeneratedAt   string              `json:"generated_at,omitempty"`
}
