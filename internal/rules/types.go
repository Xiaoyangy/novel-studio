// Package rules 实现用户偏好的输入层（Policy）：把各来源的写作规则归一化、合并成
// 本书快照（见 snapshot.go），运行时由 novel_context 注入、commit_chapter 机械检查。
//
// Rule 是第四类事实，跟 Progress / Checkpoint / Artifact 并列，但性质相反：
// 前三类是系统输出，Rule 是用户意图的持久化输入。
//
// 设计约束（不可妥协）：
//   - 工具只返事实，不返指令（Violation 是事实，由 editor 决定是否触发重写）
//   - 不引入新的 verdict 路径（复用 PendingRewrites）
//   - 不引入严格度字段（severity 由规则类型固定映射，editor 自主语义裁定）
//   - 不动 Flow Router（rule 不参与路由）
package rules

// SourceKind 标记规则文件来源，仅用于生成来源标签（如 global:my-style.md）。
type SourceKind int

const (
	// SourceGlobal — 用户全局偏好（~/.novel-studio/rules/ 目录下所有 .md，按文件名字典序合并），跨书复用。
	SourceGlobal SourceKind = iota
	// SourceProject — 本书规则（./.novel-studio/rules/ 目录下所有 .md，按文件名字典序合并），优先级最高。
	SourceProject
)

// String 返回来源的可读名称，用于来源标签前缀。
func (k SourceKind) String() string {
	switch k {
	case SourceGlobal:
		return "global"
	case SourceProject:
		return "project"
	default:
		return "unknown"
	}
}

// WordRange 表示章节字数的允许范围；nil 表示未声明。
type WordRange struct {
	Min int `json:"min"`
	Max int `json:"max"`
}

// Structured 装载机械可检的结构化规则字段（归一化各来源后的候选/合并结果）。
type Structured struct {
	Genre            string         `json:"genre,omitempty"`
	ChapterWords     *WordRange     `json:"chapter_words,omitempty"`
	ForbiddenChars   []string       `json:"forbidden_chars,omitempty"`
	ForbiddenPhrases []string       `json:"forbidden_phrases,omitempty"`
	FatigueWords     map[string]int `json:"fatigue_words,omitempty"`
}

// IsEmpty 用于判定是否完全没有结构化规则；checker 可据此跳过。
func (s Structured) IsEmpty() bool {
	return s.Genre == "" &&
		s.ChapterWords == nil &&
		len(s.ForbiddenChars) == 0 &&
		len(s.ForbiddenPhrases) == 0 &&
		len(s.FatigueWords) == 0
}

// Severity 标记 Violation 的严重等级。
// 固定映射（用户不可配置）：
//
//	forbidden_chars 出现             -> Error
//	forbidden_phrases 出现           -> Error
//	fatigue_words 超阈值             -> Warning
//	chapter_words 偏差 < 20%         -> Warning
//	chapter_words 偏差 >= 20%        -> Error
type Severity string

const (
	SeverityWarning Severity = "warning"
	SeverityError   Severity = "error"
)

// ChapterWordsDeviationThreshold 定义 chapter_words 偏差升级为 error 的临界值（20%）。
const ChapterWordsDeviationThreshold = 0.20

// Violation 是 checker 的输出：本章违反了某条机械规则的事实陈述。
//
// 注意：commit_chapter 把 violations 透传到返回 JSON，不阻断 commit；
// editor 在审阅时把这些事实映射到现有八维（aesthetic/pacing/character/consistency/ai_voice_detection），
// 由 LLM 自主决定是否升级 verdict 触发 polish/rewrite。
type Violation struct {
	Rule      string   `json:"rule"`                // forbidden_chars / forbidden_phrases / fatigue_words / chapter_words / project_contamination / deprecated_story_engine / content_count_mismatch / awkward_simile / dangling_order_word / state_clause_pile / semantic_perplexity_low / ending_aphorism_question / micro_action_overuse / dramatic_negation_overuse / paragraph_start_repetition / not_but_overuse / precise_measure_overuse / patch_phrase_overuse / minor_mistake_overuse / isolated_sentence_overuse / supporting_quip_overuse / vague_quantifier_overuse / dialogue_conveyor_overuse / pov_interiority_thin / system_message_inline
	Target    string   `json:"target,omitempty"`    // 具体违规对象（哪个词/字符）；chapter_words 留空
	Limit     any      `json:"limit,omitempty"`     // 阈值；fatigue_words=int / chapter_words="2100-3000" / forbidden_*=空
	Actual    any      `json:"actual"`              // 实际值；fatigue_words/forbidden_*=出现次数 / chapter_words=本章字数
	Deviation float64  `json:"deviation,omitempty"` // chapter_words 偏差率（0~1），其他规则留空
	Severity  Severity `json:"severity"`            // error / warning
}
