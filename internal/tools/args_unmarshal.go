package tools

import (
	"encoding/json"
	"log/slog"
	"regexp"
	"strings"
)

// 工具参数容错解析（LLM 输出 JSON 防御的客户端边界层）。
// 2026 年主流 provider 的 tool args 已由 constrained decoding 保证合法 JSON，
// 但经自定义代理/弱模型时仍会出现：markdown 代码围栏包裹、尾逗号、整体字符串化 JSON。
// 分层：严格解析（合法输入零开销直通）→ 围栏/首尾杂质剥离 → 尾逗号修复 → 双重编码解包。
// 全部失败返回原始错误（含 ErrToolArgs 语义由调用方包装，交 LLM 自主重试——
// 这就是架构自带的"自修复闭环"，不做独立的 LLM 修复调用）。
// 修复成功只记 slog（不落正文，避免泄漏内容），绝不阻塞工具执行。

var (
	fenceRe         = regexp.MustCompile("(?s)^```[a-zA-Z]*\\s*(.*?)\\s*```$")
	trailingCommaRe = regexp.MustCompile(`,\s*([}\]])`)
)

// unmarshalToolArgs 是全部工具 Execute 的统一参数解析入口。
func unmarshalToolArgs(raw json.RawMessage, v any) error {
	strictErr := json.Unmarshal(raw, v)
	if strictErr == nil {
		return nil
	}

	// 层 1：剥 markdown 围栏与首尾非 JSON 杂质。
	cleaned := stripJSONWrapping(string(raw))
	if cleaned != string(raw) {
		if err := json.Unmarshal([]byte(cleaned), v); err == nil {
			slog.Warn("工具参数经清洗后解析成功", "module", "tools", "repair", "strip_wrapping")
			return nil
		}
	}

	// 层 2：尾逗号修复。
	repaired := trailingCommaRe.ReplaceAllString(cleaned, "$1")
	if repaired != cleaned {
		if err := json.Unmarshal([]byte(repaired), v); err == nil {
			slog.Warn("工具参数经修复后解析成功", "module", "tools", "repair", "trailing_comma")
			return nil
		}
	}

	// 层 3：双重编码解包（整体是 JSON 字符串时解一层再试）。
	// 注意用原始输入判定字符串形态：cleaned 已剥外层引号，不再是合法字符串字面量。
	var inner string
	if err := json.Unmarshal(raw, &inner); err == nil {
		unwrapped := stripJSONWrapping(inner)
		if err := json.Unmarshal([]byte(unwrapped), v); err == nil {
			slog.Warn("工具参数经解包后解析成功", "module", "tools", "repair", "double_encoded")
			return nil
		}
	}

	return strictErr
}

// stripJSONWrapping 剥掉 markdown 代码围栏，再裁剪到首个 {/[ 与末个 }/] 之间。
func stripJSONWrapping(s string) string {
	s = strings.TrimSpace(s)
	if m := fenceRe.FindStringSubmatch(s); m != nil {
		s = strings.TrimSpace(m[1])
	}
	start := strings.IndexAny(s, "{[")
	end := strings.LastIndexAny(s, "}]")
	if start >= 0 && end > start {
		s = s[start : end+1]
	}
	return s
}
