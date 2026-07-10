package domain

import (
	"encoding/json"
	"strings"
)

// TrendLanguageRequested 判断用户是否明确要求热梗进入正文。禁止类表述优先，
// 避免“不要使用热梗”也因命中“热梗”而被误判成必须使用。
func TrendLanguageRequested(text string) bool {
	text = strings.TrimSpace(text)
	if text == "" {
		return false
	}
	for _, forbidden := range []string{"禁止使用热梗", "禁用热梗", "不要用热梗", "不使用热梗", "不用热梗"} {
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
	for _, forbidden := range []string{"系统禁止聊天", "系统不能聊天", "系统不与主角交流", "纯任务机器人即可"} {
		if strings.Contains(text, forbidden) {
			return false
		}
	}
	for _, wanted := range []string{"交流解闷", "不是一个纯下达任务", "不是纯下达任务", "系统会和", "系统会接话", "系统能接话", "系统短促、会接话", "系统提示短、能聊天", "系统始终支持", "始终支持主角"} {
		if strings.Contains(text, wanted) {
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
