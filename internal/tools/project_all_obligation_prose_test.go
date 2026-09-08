package tools

import (
	"reflect"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

func TestProjectAllV2ObligationProseNumbersExcludeAuditIDs(t *testing.T) {
	const outcome = "核对90升与60升，到第3章仍须保留记录"
	plan := domain.ChapterPlan{}
	ApplyProjectAllOutlineObligations(&plan, []string{
		"[project-all v2-hard-obligation:obl:character:1:123456789012] " + outcome,
		"[project-all v2-simulation-obligation:obl:character:1:987654321098] 场外库存剩2份，主角未获知",
	})
	outcomes := RenderRequiredOutcomes(plan)
	if len(outcomes) != 1 || outcomes[0] != outcome {
		t.Fatalf("host metadata was treated as a result: %v", outcomes)
	}
	want := map[string]struct{}{"90": {}, "60": {}, "3": {}}
	if numbers := renderOutcomeNumberSet(outcomes[0]); !reflect.DeepEqual(numbers, want) {
		t.Fatalf("real quantities lost or audit IDs became quantity anchors: %v", numbers)
	}
	if strings.Contains(strings.Join(plan.Contract.ContinuityChecks, "\n"), "987654321098") || !strings.Contains(strings.Join(plan.Contract.ContinuityChecks, "\n"), "剩2份") {
		t.Fatalf("hidden continuity lost amount or retained audit identity: %v", plan.Contract.ContinuityChecks)
	}
}
