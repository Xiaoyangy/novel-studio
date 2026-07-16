package tools

import (
	"strings"
	"testing"

	qualityrules "github.com/chenhongyang/novel-studio/internal/rules"
)

func TestValidateFictionProseTypography(t *testing.T) {
	bad := []string{
		`"先把门关上。"`,
		`温梨说："我不签。"`,
	}
	for _, text := range bad {
		err := validateFictionProseTypography(text)
		if err == nil || !strings.Contains(err.Error(), qualityrules.ASCIIChineseDialogueQuoteRule) {
			t.Fatalf("expected hard typography error for %q, got %v", text, err)
		}
	}

	good := []string{
		`所谓"只能花在青山县"的规则仍要结合受益对象理解。`,
		`规则："只能花在青山县"`,
		`温梨说：“我不签。”`,
	}
	for _, text := range good {
		if err := validateFictionProseTypography(text); err != nil {
			t.Fatalf("unexpected typography error for %q: %v", text, err)
		}
	}
}
