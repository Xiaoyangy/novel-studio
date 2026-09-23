package tools

import "strings"

// worldTickHistoricalDuration excludes explicit retrospective durations, not
// every occurrence of 前: “三日前必须提交” can still be a pending deadline.
// This is deliberately local to each match so a historical “七年前” cannot
// suppress a separate future “七年后” in the same chapter.
func worldTickHistoricalDuration(text string, start, end int) bool {
	before, after := text[:start], text[end:]
	for _, prefix := range []string{"过去", "此前", "以往", "早先"} {
		if strings.HasSuffix(before, prefix) {
			return true
		}
	}
	remaining := ""
	switch {
	case strings.HasPrefix(after, "之前"):
		remaining = strings.TrimPrefix(after, "之前")
	case strings.HasPrefix(after, "以前"):
		remaining = strings.TrimPrefix(after, "以前")
	case strings.HasPrefix(after, "前"):
		remaining = strings.TrimPrefix(after, "前")
	default:
		return false
	}
	// Before a due time is not necessarily in the past. Keep obligations and
	// explicit due-time constructions fail-closed at the original anchor gate.
	obligation := strings.TrimLeft(remaining, " \t\n\r，,、：:")
	for _, prefix := range []string{"必须", "务必", "须", "需要", "应当", "完成", "提交", "交付", "截止"} {
		if strings.HasPrefix(obligation, prefix) {
			return false
		}
	}
	for _, prefix := range []string{"必须在", "须在", "需要在", "应在", "务必在", "请在", "请于", "不得晚于", "最迟在", "截止于"} {
		if strings.HasSuffix(before, prefix) {
			return false
		}
	}
	// “在/于…之前把材料交出” is a bounded due-time construction even
	// without 必须. Prefer retaining an ambiguous deadline; only its explicit
	// retrospective noun phrase (“在七年前的港口”) is unambiguously past here.
	if (strings.HasSuffix(before, "在") || strings.HasSuffix(before, "于")) &&
		!strings.HasPrefix(remaining, "的") && !strings.HasPrefix(remaining, "时") {
		return false
	}
	return true
}
