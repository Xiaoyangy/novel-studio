package rules

import (
	"fmt"
	"strings"
)

func HasLimitValue(limit any) bool {
	switch v := limit.(type) {
	case nil:
		return false
	case string:
		return strings.TrimSpace(v) != ""
	default:
		return true
	}
}

func FormatLimitValue(limit any) string {
	if !HasLimitValue(limit) {
		return ""
	}
	return fmt.Sprint(limit)
}
