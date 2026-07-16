package rules

import "testing"

func TestASCIIChineseDialogueQuotesRejectsClearDialoguePositions(t *testing.T) {
	tests := []string{
		`"你先别签，我回去拿证件。"`,
		`温梨说："你先别签，我回去拿证件。"`,
		`门外有人喊："别开门！"`,
		`温梨："我没答应。"`,
	}
	for _, text := range tests {
		violations := ASCIIChineseDialogueQuotes(text)
		if len(violations) != 1 {
			t.Fatalf("expected one violation for %q, got %+v", text, violations)
		}
		if violations[0].Rule != ASCIIChineseDialogueQuoteRule || violations[0].Severity != SeverityError {
			t.Fatalf("unexpected violation for %q: %+v", text, violations[0])
		}
	}
}

func TestASCIIChineseDialogueQuotesAllowsQuotedTermsAndChineseDialogueMarks(t *testing.T) {
	tests := []string{
		`所谓"只能花在青山县"的规则，并不等于任何钱都能报销。`,
		`"只能花在青山县"这个概念仍需结合受益对象理解。`,
		`"青山县志"这本书记录了旧街的变迁。`,
		`规则："只能花在青山县"`,
		`定义："真实改善消费。"`,
		`常见说法："只能花在青山县。"`,
		`摘要："这一章只处理授权。"`,
		`温梨说：“你先别签。”`,
		`温梨说：「你先别签。」`,
	}
	for _, text := range tests {
		if violations := ASCIIChineseDialogueQuotes(text); len(violations) != 0 {
			t.Fatalf("quoted term or Chinese dialogue marks should pass for %q: %+v", text, violations)
		}
	}
}

func TestLintIncludesASCIIChineseDialogueQuoteHardError(t *testing.T) {
	violation := findRule(Lint("第一章\n\n江烬问：\"明早八点还来吗？\""), ASCIIChineseDialogueQuoteRule)
	if violation == nil {
		t.Fatal("Lint did not include ASCII Chinese dialogue quote violation")
	}
	if violation.Severity != SeverityError || violation.Actual != 1 {
		t.Fatalf("unexpected hard violation: %+v", violation)
	}
}
