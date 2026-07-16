package domain

import (
	"encoding/json"
	"fmt"
	"strings"
)

// TrendLanguageRequested 判断用户是否明确要求热梗进入正文。禁止类表述优先，
// 避免“不要使用热梗”也因命中“热梗”而被误判成必须使用。
func TrendLanguageRequested(text string) bool {
	text = strings.TrimSpace(text)
	if text == "" {
		return false
	}
	// A ceiling such as “每章一个热梗，但不堆网络梗” limits density; it does
	// not cancel the explicit positive request earlier in the sentence. Remove
	// only the ceiling phrase, then evaluate any remaining positive wording.
	for _, ceiling := range []string{
		"不堆热梗", "不堆网络梗", "不堆流行梗", "避免堆热梗", "避免堆网络梗",
	} {
		text = strings.ReplaceAll(text, ceiling, "")
	}
	for _, forbidden := range []string{
		"禁止使用热梗", "禁用热梗", "不要用热梗", "不使用热梗", "不用热梗",
	} {
		if strings.Contains(text, forbidden) {
			return false
		}
	}
	for _, wanted := range []string{"热梗", "网络梗", "流行梗", "轻梗"} {
		if strings.Contains(text, wanted) {
			return true
		}
	}
	return false
}

// ReaderEntertainmentRequested 判断项目是否把轻松、喜剧或爽感列为明确风格承诺。
func ReaderEntertainmentRequested(text string) bool {
	text = strings.TrimSpace(text)
	if text == "" {
		return false
	}
	for _, forbidden := range []string{"禁止搞笑", "不要搞笑", "不写搞笑", "不要爽文", "不写爽文"} {
		if strings.Contains(text, forbidden) {
			return false
		}
	}
	for _, wanted := range []string{"轻松搞笑", "轻喜剧", "搞笑类型", "爽文", "强爽", "强情绪兑现"} {
		if strings.Contains(text, wanted) {
			return true
		}
	}
	return false
}

// SystemCompanionVoiceRequested 判断系统是否被用户定义为会交流、解闷并支持主角的同伴。
func SystemCompanionVoiceRequested(text string) bool {
	text = strings.TrimSpace(text)
	if text == "" {
		return false
	}
	if SystemCompanionVoiceForbidden(text) {
		return false
	}
	for _, wanted := range []string{"交流解闷", "不是一个纯下达任务", "不是纯下达任务", "系统会和", "系统会接话", "系统能接话", "系统短促、会接话", "系统提示短、能聊天", "系统始终支持", "始终支持主角"} {
		if strings.Contains(text, wanted) {
			return true
		}
	}
	if strings.Contains(text, "系统") && (strings.Contains(text, "会聊天") ||
		strings.Contains(text, "吐槽搭子") || strings.Contains(text, "情绪支持")) {
		return true
	}
	return false
}

// SystemCompanionVoiceForbidden reports an explicit project decision that the
// system must not act as a speaking companion. Keeping this separate from the
// positive detector lets callers apply source precedence instead of merging an
// old brief with newer user rules and letting either phrase win accidentally.
func SystemCompanionVoiceForbidden(text string) bool {
	text = strings.TrimSpace(text)
	if text == "" {
		return false
	}
	for _, forbidden := range []string{"系统禁止聊天", "系统不能聊天", "系统不与主角交流", "纯任务机器人即可"} {
		if strings.Contains(text, forbidden) {
			return true
		}
	}
	return false
}

// SystemCompanionFeedbackContradicts 判断审核建议是否把用户明确要求的陪伴型系统
// 反向改成冷硬、静默或纯任务机器人。审核原件可保留，但这类建议不能进入后续 RAG。
func SystemCompanionFeedbackContradicts(text string) bool {
	text = strings.TrimSpace(text)
	if text == "" {
		return false
	}
	// External reviewers sometimes quote a bracketed system line without using
	// the word "系统", then fault it for not being a UI notification. The
	// project's established 【】 companion channel makes that the same conflict.
	if !strings.Contains(text, "系统") {
		return strings.Contains(text, "【") && strings.Contains(text, "】") &&
			containsAnyAttractionPhrase(text, []string{"界面文字", "意识流", "视角锚定", "独立叙事段", "打断叙述视角"})
	}
	for _, aligned := range []string{
		"不能把系统写成冷硬", "不要把系统写成冷硬", "禁止系统保持静默",
		"不能拒绝交流", "不得改成纯任务机器人", "不能改成纯任务机器人",
	} {
		if strings.Contains(text, aligned) {
			return false
		}
	}
	for _, contradiction := range []string{
		"系统口吻偏暖", "系统调性偏软", "减少系统拟人", "弱化系统拟人",
		"系统不予回应", "系统不回应", "系统保持静默", "系统界面保持静默",
		"冷硬的规则重申", "强化系统冷硬", "强化‘系统’冷硬", "强化“系统”冷硬",
		"系统提示语的语气，保持冷感", "系统提示语保持冷感", "去掉'^_^'", "去掉\"^_^\"",
		"纯文本进度条式通知", "改用纯文本进度条",
		"保持‘规则优先’口吻", "保持“规则优先”口吻", "避免系统代偿情绪",
		"减少系统玩笑", "减少系统拟人化玩笑", "系统只用冷硬", "系统保持冷硬",
		"增加措辞刻板或断联", "系统发送一条乱码或重复提示",
		"系统类信息必须绑定界面/载体", "系统类信息必须绑定界面", "必须绑定界面/载体",
		"禁止以【】作为独立叙事段", "以【】直接嵌入叙事", "缺乏视角锚定",
		"系统对白只能以状态报告", "改为纯数据反馈", "只提供事实，不提供感悟",
		"只能以状态报告、数据更新、错误日志等形式出现",
		"系统消息避免添加解释性后半句", "回来拿筷子的那位不扣你",
	} {
		if strings.Contains(text, contradiction) {
			return true
		}
	}
	return false
}

func containsAnyAttractionPhrase(text string, phrases []string) bool {
	for _, phrase := range phrases {
		if strings.Contains(text, phrase) {
			return true
		}
	}
	return false
}

func CompleteTrendLanguagePlan(items []TrendLanguagePlan) bool {
	if len(items) == 0 {
		return false
	}
	for _, item := range items {
		if strings.TrimSpace(item.Item) == "" ||
			strings.TrimSpace(item.SourceContext) == "" ||
			strings.TrimSpace(item.CharacterCarrier) == "" ||
			strings.TrimSpace(item.SceneFunction) == "" ||
			strings.TrimSpace(item.UsageBudget) == "" ||
			strings.TrimSpace(item.ForbiddenUsage) == "" {
			return false
		}
	}
	return true
}

func HasActiveTrendLanguagePlan(items []TrendLanguagePlan) bool {
	for _, item := range items {
		value := strings.Trim(strings.TrimSpace(item.Item), "`'\"“”‘’")
		lower := strings.ToLower(value)
		if value == "" || lower == "none" || lower == "n/a" || lower == "not_applicable" ||
			strings.Contains(lower, "none-or") || strings.HasPrefix(value, "不用") ||
			strings.HasPrefix(value, "不使用") || strings.HasPrefix(value, "无热梗") {
			continue
		}
		return true
	}
	return false
}

// TrendLanguagePlanProblems catches semantic misuse that an exact item match
// cannot see. In this project "呱，" is a network discourse opener followed by
// a complete line, never an animal sound or a standalone vocal action.
func TrendLanguagePlanProblems(items []TrendLanguagePlan) []string {
	var problems []string
	for i, item := range items {
		value := strings.Trim(strings.TrimSpace(item.Item), "`'\"“”‘’")
		if !strings.HasPrefix(value, "呱") {
			continue
		}
		usage := strings.Join([]string{item.SceneFunction, item.CharacterCarrier, item.UsageBudget}, "\n")
		badSound := strings.Contains(usage, "拟声") &&
			!strings.Contains(usage, "不是拟声") && !strings.Contains(usage, "禁止拟声") &&
			!strings.Contains(usage, "禁止写成拟声") && !strings.Contains(usage, "避免拟声") &&
			!strings.Contains(usage, "不得写成拟声")
		if badSound {
			problems = append(problems, fmt.Sprintf("trend_language_plan[%d] 把‘呱，’解释成拟声；必须写成网络语气词起手并后接完整吐槽", i))
		}
		if !strings.Contains(usage, "完整") && !strings.Contains(usage, "后接") {
			problems = append(problems, fmt.Sprintf("trend_language_plan[%d] 未明确‘呱，’后接完整台词", i))
		}
	}
	return problems
}

func CompleteReaderEntertainmentPlan(plan ReaderEntertainmentPlan) bool {
	return strings.TrimSpace(plan.OpeningBeat) != "" &&
		len(nonEmptyWritingStrings(plan.HumorBeats)) >= 2 &&
		len(nonEmptyWritingStrings(plan.ImmediatePayoffs)) >= 2 &&
		strings.TrimSpace(plan.ProcedureCompression) != "" &&
		strings.TrimSpace(plan.CompanionVoiceBeat) != "" &&
		len(nonEmptyWritingStrings(plan.ForbiddenComedy)) > 0
}

func CompleteLongformOpeningDesign(plan LongformOpeningDesign) bool {
	return strings.TrimSpace(plan.TargetReader) != "" &&
		strings.TrimSpace(plan.OpeningHook) != "" &&
		strings.TrimSpace(plan.SerialEngine) != "" &&
		len(nonEmptyWritingStrings(plan.ReaderRewardLoop)) > 0 &&
		len(plan.LongRangePromises) > 0 &&
		len(nonEmptyWritingStrings(plan.RevealBudget)) > 0 &&
		len(nonEmptyWritingStrings(plan.FirstChapterProof)) > 0 &&
		len(nonEmptyWritingStrings(plan.RetentionRisks)) > 0
}

func SystemCompanionPlanCompatible(sim ChapterCausalSimulation) bool {
	return len(SystemCompanionPlanProblems(sim)) == 0
}

func SystemCompanionPlanProblems(sim ChapterCausalSimulation) []string {
	beat := strings.TrimSpace(sim.EntertainmentPlan.CompanionVoiceBeat)
	var problems []string
	if !strings.Contains(beat, "系统") {
		problems = append(problems, "companion_voice_beat 未明确由系统承载")
	}
	positive := false
	for _, marker := range []string{"接话", "吐槽", "交流", "解闷", "撑腰", "支持", "陪伴", "聊天"} {
		if strings.Contains(beat, marker) {
			positive = true
			break
		}
	}
	if !positive {
		problems = append(problems, "companion_voice_beat 未写系统接话/吐槽/交流/解闷/撑腰/支持")
	}
	parts := []string{
		beat,
		sim.AntiAIPlan.ObjectResponseBudget,
		sim.AntiAIPlan.DialogueFunctionPlan,
	}
	parts = append(parts, sim.EntertainmentPlan.ForbiddenComedy...)
	parts = append(parts, sim.AntiAIPlan.CounterMoves...)
	parts = append(parts, sim.AntiAIPlan.ReviewChecks...)
	all := strings.Join(parts, "\n")
	for _, contradiction := range []string{
		"系统不接话", "系统不回应", "系统只用冷硬", "系统保持冷硬", "系统不得接话",
		"系统不得吐槽", "系统禁止吐槽", "系统不参与对话", "系统不参与喜剧",
		"把系统写成会吐槽", "把系统写成聊天伙伴", "未变成陪聊", "不能陪聊",
	} {
		if strings.Contains(all, contradiction) {
			problems = append(problems, "反向表述="+contradiction)
		}
	}
	for _, blueprint := range sim.DialogueBlueprints {
		raw, _ := json.Marshal(blueprint)
		text := string(raw)
		systemScene := strings.Contains(strings.ToLower(blueprint.SceneID), "system") ||
			strings.Contains(text, "手机界面") || strings.Contains(text, "系统界面")
		if !systemScene {
			continue
		}
		for _, contradiction := range []string{"不拟人闲聊", "不暗示系统人格", "系统界面保持静默", "系统保持静默", "系统不参与对话"} {
			if strings.Contains(text, contradiction) {
				problems = append(problems, "系统对白蓝图反向表述="+contradiction)
			}
		}
		hasSystemTurn := false
		for _, turn := range blueprint.TurnProgression {
			if strings.Contains(turn.Speaker, "系统") {
				hasSystemTurn = true
				break
			}
		}
		if !hasSystemTurn {
			problems = append(problems, "系统场景没有由系统承载的对话回合")
		}
	}
	return problems
}

func ChapterAttractionPlanReady(plan ChapterPlan, requireTrend, requireEntertainment, requireLongform, requireSystemCompanion bool) bool {
	if requireTrend && (!CompleteTrendLanguagePlan(plan.CausalSimulation.TrendLanguage) ||
		!HasActiveTrendLanguagePlan(plan.CausalSimulation.TrendLanguage)) {
		return false
	}
	if requireEntertainment && !CompleteReaderEntertainmentPlan(plan.CausalSimulation.EntertainmentPlan) {
		return false
	}
	if requireLongform && !CompleteLongformOpeningDesign(plan.CausalSimulation.LongformOpening) {
		return false
	}
	if requireSystemCompanion && !SystemCompanionPlanCompatible(plan.CausalSimulation) {
		return false
	}
	return true
}

func nonEmptyWritingStrings(items []string) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}
