package utils

import (
	"strings"
	"unicode"
)

// CleanInputText 删除终端输入中没有业务意义的控制字符，保留用户可见文本。
// 单行输入场景下，粘贴文本里的换行和制表符会被归一为空格。
func CleanInputText(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' {
			return ' '
		}
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
}

// CleanInputLine 清洗单行人工输入，并去掉首尾空白。
func CleanInputLine(s string) string {
	return strings.TrimSpace(CleanInputText(s))
}

func ContainsControl(s string) bool {
	for _, r := range s {
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}
