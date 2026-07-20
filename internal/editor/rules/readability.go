package rules

import (
	"math"
	"regexp"
	"unicode/utf8"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

// 本文件是 RoughnessScore 的正向对偶。RoughnessScore 只回答"这段有多不像平滑
// 模板文"，是一个防检测代理；它从不衡量读者是否愿意读下去。ReaderExperienceScore
// 补上缺失的另一半：从读者视角判断这一章是否好读——现场是否具体可感、对白是否活、
// 节奏是否有起伏、主视角是否在场、章末是否有前推力，以及有没有把正文写成"验收
// 录像"式的流程报告。选稿时两者合成，让系统不再为了粗糙度牺牲可读性。

var (
	// 流程/公文/验收腔：金融、监管、取证类正文最容易滑进这种"读起来像会议纪要"
	// 的质地。命中本身不违规，但当它压过现场动作时，读者的沉浸会被稀释。
	procedureVocabRe = regexp.MustCompile(`(核验|复核|比对|校验|门禁|台账|登记|凭证|编号|受控|元数据|归档|留痕|封存|授权书|竣工图|时间码|音轨|独立见证|提交材料|保管记录|处置组|事项|流程|规程|依照|按照规定|予以|如下|一、|二、|三、)`)
	// 一句里既讲流程又落在具体动作/感官上，就不算"纯报告"。
	sensoryHumanRe       = actionSensoryRe
	proceduralOnlyBudget = 0.34
)

// ReaderExperienceScore 返回 [0,1]：越高表示读者越可能一口气读下去。它是确定性的、
// 与题材无关的相对度量——三候选同题材比较时，选更有画面、更好读的那一稿。
func ReaderExperienceScore(text string, metrics domain.ChapterAIVoiceMetrics) float64 {
	body := stripMarkdownTitle(text)
	paragraphs := splitParagraphs(body)
	if len(paragraphs) == 0 {
		return 0
	}
	sentences := splitSentences(body)

	concreteness := sceneConcreteness(paragraphs)
	dialogueLife := dialogueVitality(metrics)
	rhythm := rhythmVariety(sentences)
	pov := povPresence(metrics)
	pull := forwardPull(paragraphs, metrics)

	base := 0.26*concreteness + 0.18*dialogueLife + 0.20*rhythm + 0.16*pov + 0.20*pull

	base -= wallOfTextPenalty(paragraphs)
	base -= fragmentGridPenalty(paragraphs)
	base -= proceduralReportPenalty(paragraphs)

	return round4(clamp(base, 0, 1))
}

// SelectionScore 合成读者体验与真实感（roughness）。读者体验领先，真实感保留反检测
// 底线；两者本就正相关（清单堆词、格言腔同时伤可读性与真实感），合成后既不会选出
// 粗糙但难读的稿，也不会选出光滑的模板文。
func SelectionScore(readability, roughness float64) float64 {
	r := math.Min(math.Max(roughness, 0), 1.5) / 1.5
	return round4(clamp(0.6*readability+0.4*r, 0, 1))
}

// sceneConcreteness：有多少段真的落在可感知的动作、感官、物件、环境上。抽象概括、
// 纯心理独白、纯流程叙述都不计入。读者靠具体的"看见/听见/摸到"进入场景。
func sceneConcreteness(paragraphs []string) float64 {
	if len(paragraphs) == 0 {
		return 0
	}
	concrete := 0
	for _, p := range paragraphs {
		if sensoryHumanRe.MatchString(p) {
			concrete++
		}
	}
	// 0.35 已算干净可感；线性放大后 clamp，避免要求每段都堆感官。
	return clamp(ratio(concrete, len(paragraphs))/0.55, 0, 1)
}

// dialogueVitality：活的对白（配角主动误解、打断、拒绝逼出的信息）能显著提升网文
// 可读性。全程纯旁白偏干；对白占比过高又变话剧。取中段最高。
func dialogueVitality(metrics domain.ChapterAIVoiceMetrics) float64 {
	r := metrics.SupportingDialogueParagraphRatio
	switch {
	case r <= 0:
		return 0.30
	case r < 0.12:
		return clamp(0.30+r/0.12*0.55, 0, 1)
	case r <= 0.45:
		return 1.0
	case r < 0.70:
		return clamp(1.0-(r-0.45)/0.25*0.35, 0, 1)
	default:
		return 0.55
	}
}

// rhythmVariety：句长变异系数落在健康带宽即高分。过于均匀=机器单调；过高=碎句成网。
func rhythmVariety(sentences []string) float64 {
	if len(sentences) < 4 {
		return 0.5
	}
	lengths := make([]float64, 0, len(sentences))
	for _, s := range sentences {
		lengths = append(lengths, float64(utf8.RuneCountInString(s)))
	}
	cv := coefficientOfVariation(lengths)
	// 目标带宽 [0.35, 0.90]，峰值 ~0.60；带外线性衰减。
	switch {
	case cv <= 0:
		return 0.3
	case cv < 0.35:
		return clamp(0.4+cv/0.35*0.6, 0, 1)
	case cv <= 0.90:
		return 1.0
	case cv < 1.30:
		return clamp(1.0-(cv-0.90)/0.40*0.5, 0, 1)
	default:
		return 0.5
	}
}

// povPresence：主视角是否真的在场做选择。真实动摇（冲动→克制/改口）让读者站进人物
// 处境，是可读性的核心；纯外部叙述缺这一层。
func povPresence(metrics domain.ChapterAIVoiceMetrics) float64 {
	if metrics.ProtagonistWaver {
		return 1.0
	}
	return 0.45
}

// forwardPull：章末是否留下让读者翻页的现场后果。具体钩子加分；主题金句加问号那种
// 假钩子扣分——它读起来像 AI，也不真的制造悬念。
func forwardPull(paragraphs []string, metrics domain.ChapterAIVoiceMetrics) float64 {
	if endingAphorismQuestionFlag(paragraphs) != nil {
		return 0.30
	}
	if metrics.EndingHookUsed {
		return 1.0
	}
	return 0.55
}

// wallOfTextPenalty：超过 220 字且无对白的文字墙拖累移动端可读性。
func wallOfTextPenalty(paragraphs []string) float64 {
	walls := 0
	for _, p := range paragraphs {
		if utf8.RuneCountInString(p) > 220 && !quoteRe.MatchString(p) {
			walls++
		}
	}
	return math.Min(0.20, ratio(walls, len(paragraphs))*0.6)
}

// fragmentGridPenalty：连续 4 段以上、每段 <12 字的无信息碎句网格，是刻意"打碎节奏"
// 骗检测器的招牌，读者体验反而更差。
func fragmentGridPenalty(paragraphs []string) float64 {
	longestRun, run := 0, 0
	for _, p := range paragraphs {
		if utf8.RuneCountInString(p) < 12 {
			run++
			if run > longestRun {
				longestRun = run
			}
		} else {
			run = 0
		}
	}
	if longestRun < 4 {
		return 0
	}
	return math.Min(0.15, float64(longestRun-3)*0.05)
}

// proceduralReportPenalty："验收录像"质地：流程/公文词压过现场动作的段落越多，读起来
// 越像会议纪要而非小说。只有当段落被流程词主导且没有落到人身上时才计。
func proceduralReportPenalty(paragraphs []string) float64 {
	heavy := 0
	for _, p := range paragraphs {
		procedural := len(procedureVocabRe.FindAllString(p, -1))
		if procedural == 0 {
			continue
		}
		runes := float64(utf8.RuneCountInString(p))
		if runes <= 0 {
			continue
		}
		density := float64(procedural) / (runes / 20.0)
		if density >= proceduralOnlyBudget && !sensoryHumanRe.MatchString(p) {
			heavy++
		}
	}
	return math.Min(0.22, ratio(heavy, len(paragraphs))*0.7)
}

func coefficientOfVariation(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	var sum float64
	for _, v := range values {
		sum += v
	}
	mean := sum / float64(len(values))
	if mean == 0 {
		return 0
	}
	var variance float64
	for _, v := range values {
		d := v - mean
		variance += d * d
	}
	variance /= float64(len(values))
	return math.Sqrt(variance) / mean
}
