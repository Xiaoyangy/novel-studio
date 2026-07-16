package rules

import (
	"regexp"
	"strings"
)

// ASCIIChineseDialogueQuoteRule identifies Chinese character dialogue written
// with ASCII double quotes. Chinese prose may legitimately quote a term with
// ASCII punctuation, so the detector deliberately limits itself to clear
// dialogue positions: a quote opening a paragraph, a speech verb followed by a
// colon, or a name/pronoun script label followed by punctuated speech.
const ASCIIChineseDialogueQuoteRule = "ascii_chinese_dialogue_quote"

var asciiChineseQuoteRe = regexp.MustCompile(`"([^"\r\n]*\p{Han}[^"\r\n]*)"`)

var asciiDialogueSpeechCueBeforeColonRe = regexp.MustCompile(`(?:说(?:道)?|问(?:道)?|答(?:道)?|喊(?:道)?|叫(?:道)?|骂(?:道)?|嚷(?:道)?|吼(?:道)?|嘀咕(?:道)?|解释(?:道)?|提醒(?:道)?|开口|接话|回话|应声|低声|轻声|冷声|笑着说|哭着说)[^：:]{0,8}[：:]$`)

var asciiDialogueScriptLabelRe = regexp.MustCompile(`^(?:他|她|我|你|您|[\p{Han}]{2,4})$`)

var asciiDialogueTerminalRe = regexp.MustCompile(`[。！？!?…—]$`)

var asciiNonDialogueColonLabels = map[string]struct{}{
	"定义": {}, "术语": {}, "概念": {}, "规则": {}, "原则": {}, "机制": {},
	"标题": {}, "字段": {}, "选项": {}, "提示": {}, "备注": {}, "原文": {},
	"字样": {}, "条款": {}, "口号": {}, "命题": {}, "要求": {}, "限制": {},
	"说明": {}, "标签": {}, "页面": {}, "合同": {}, "系统": {}, "关键词": {},
	"摘要": {}, "示例": {}, "样例": {}, "内容": {}, "主题": {}, "核心": {},
	"准则": {}, "格式": {}, "模板": {}, "文案": {}, "宣传语": {}, "答复": {},
}

var asciiNonSpeechCueSuffixes = []string{
	"说法", "说明", "小说", "传说", "解说", "问句", "答复", "表述", "写法",
}

var asciiQuotedTermContinuationPrefixes = []string{
	"这个词", "这一词", "这个短语", "这一短语", "这个说法", "这一说法",
	"这个概念", "这一概念", "这个术语", "这一术语", "这条规则", "这一规则",
	"这个原则", "这一原则", "这个机制", "这一机制", "这个命题", "这一命题",
	"这句话", "这一句话", "这个句子", "这一句子", "这几个字", "这些字",
	"这本书", "这篇文章", "这段话", "这一段话", "这行字", "这一行字",
	"这个名字", "这一名字", "这个标题", "这一标题", "这个标签", "这一标签",
	"一词", "二字", "三字", "四字", "几个字", "的意思", "的定义", "的含义",
	"是指", "指的是", "意味着", "作为", "属于", "被称为", "并非", "不是", "不等于",
}

// ASCIIChineseDialogueQuotes returns hard formatting violations without
// treating every ASCII-quoted Chinese phrase as spoken dialogue.
func ASCIIChineseDialogueQuotes(text string) []Violation {
	count := 0
	firstTarget := ""
	for line := range strings.SplitSeq(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		for _, match := range asciiChineseQuoteRe.FindAllStringSubmatchIndex(trimmed, -1) {
			before := trimmed[:match[0]]
			inner := strings.TrimSpace(trimmed[match[2]:match[3]])
			after := trimmed[match[1]:]
			clearDialogue := false
			if strings.TrimSpace(before) == "" {
				clearDialogue = !asciiQuoteIsTermAtParagraphStart(after)
			} else {
				clearDialogue = asciiQuoteHasDialogueColonContext(before, inner)
			}
			if !clearDialogue {
				continue
			}
			count++
			if firstTarget == "" {
				firstTarget = asciiQuotePreview(inner)
			}
		}
	}
	if count == 0 {
		return nil
	}
	return []Violation{{
		Rule:     ASCIIChineseDialogueQuoteRule,
		Target:   firstTarget,
		Limit:    0,
		Actual:   count,
		Severity: SeverityError,
	}}
}

func asciiQuoteIsTermAtParagraphStart(after string) bool {
	after = strings.TrimSpace(after)
	if after == "" {
		return false
	}
	for _, prefix := range asciiQuotedTermContinuationPrefixes {
		if strings.HasPrefix(after, prefix) {
			return true
		}
	}
	return false
}

func asciiQuoteHasDialogueColonContext(before, inner string) bool {
	before = strings.TrimSpace(before)
	if !strings.HasSuffix(before, "：") && !strings.HasSuffix(before, ":") {
		return false
	}
	label := strings.TrimSpace(strings.TrimSuffix(strings.TrimSuffix(before, "："), ":"))
	if _, nonDialogue := asciiNonDialogueColonLabels[label]; nonDialogue {
		return false
	}
	for _, suffix := range asciiNonSpeechCueSuffixes {
		if strings.HasSuffix(label, suffix) {
			return false
		}
	}
	if asciiDialogueSpeechCueBeforeColonRe.MatchString(before) {
		return true
	}
	return asciiDialogueScriptLabelRe.MatchString(label) && asciiDialogueTerminalRe.MatchString(inner)
}

func asciiQuotePreview(inner string) string {
	runes := []rune(inner)
	if len(runes) > 24 {
		runes = append(runes[:24], '…')
	}
	return `"` + string(runes) + `"`
}
