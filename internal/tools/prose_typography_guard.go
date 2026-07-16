package tools

import (
	"fmt"

	"github.com/chenhongyang/novel-studio/internal/errs"
	qualityrules "github.com/chenhongyang/novel-studio/internal/rules"
)

// validateFictionProseTypography rejects unambiguous Chinese character
// dialogue written with ASCII quotes. It intentionally delegates detection to
// the narrow rule-layer matcher so quoted terms and concepts remain legal.
func validateFictionProseTypography(content string) error {
	violations := qualityrules.ASCIIChineseDialogueQuotes(content)
	if len(violations) == 0 {
		return nil
	}
	violation := violations[0]
	return fmt.Errorf(
		"小说人物对白命中 %s（%s，共 %v 处）：请使用中文全角引号“……”或「……」，禁止 ASCII 双引号: %w",
		violation.Rule, violation.Target, violation.Actual, errs.ErrToolPrecondition,
	)
}
