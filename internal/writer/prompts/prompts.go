package prompts

import (
	_ "embed"
	"strings"
)

//go:embed anti_ai_voice.md
var antiAIVoice string

// AntiAIVoice 返回 Writer 专用的反 AI 腔硬约束片段。
func AntiAIVoice() string {
	return strings.TrimSpace(antiAIVoice)
}
