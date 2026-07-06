package eval

import "testing"

func TestAggregateRepeatSummary(t *testing.T) {
	c := writerSmokeCase()
	pass := cleanResult()
	pass.Metrics.Usage = UsageMetrics{UsageRecorded: true, CostUSD: 0.1}
	pass.Metrics.ToolCalls = 10
	warn := pass
	warn.Outcome = Warn
	warn.Warnings = []Issue{{Kind: "warning", Source: "x", Detail: "warn"}}
	fail := pass
	fail.Outcome = Fail
	fail.HardFails = []Issue{{Kind: "hard_fail", Source: "x", Detail: "fail"}}

	cr := NewSingleRunsCaseResult(c, []RunResult{
		{Arm: ArmSingle, Repeat: 1, Result: pass},
		{Arm: ArmSingle, Repeat: 2, Result: warn},
		{Arm: ArmSingle, Repeat: 3, Result: fail},
	})
	if cr.Outcome != Fail {
		t.Fatalf("case outcome = %s want FAIL", cr.Outcome)
	}
	if cr.Summary.PassRate != 0.33 {
		t.Fatalf("pass rate = %.2f want 0.33", cr.Summary.PassRate)
	}
	if cr.Summary.HardFailRuns != 1 || cr.Summary.WarningRuns != 1 {
		t.Fatalf("summary runs 不正确: %+v", cr.Summary)
	}
	if cr.Summary.CostUSD == nil || cr.Summary.CostUSD.Avg != 0.1 ||
		cr.Summary.ToolCalls == nil || cr.Summary.ToolCalls.Avg != 10 {
		t.Fatalf("range summary 不正确: %+v", cr.Summary)
	}

	s := Aggregate("run", "single", "", 3, []CaseResult{cr})
	if s.Gate != Fail || s.Repeat != 3 || len(s.Cases) != 1 {
		t.Fatalf("suite 聚合不正确: %+v", s)
	}
}

func TestAggregateABDeltaControlsGate(t *testing.T) {
	c := writerSmokeCase()
	base := cleanResult()
	variant := cleanResult()
	variant.Metrics.WarningFindings = base.Metrics.WarningFindings + 1
	delta := GradeDelta(c, base, variant)
	cr := NewABCaseResult(c, []RunResult{
		{Arm: ArmBaseline, Repeat: 1, Result: base},
		{Arm: ArmVariant, Repeat: 1, Result: variant},
	}, []Delta{delta})

	if cr.Outcome != Warn {
		t.Fatalf("delta warning 应让 case WARN，得到 %s", cr.Outcome)
	}
	if cr.Summary.VariantPassRate != 1 || cr.Summary.DeltaPassRate != 0 {
		t.Fatalf("AB summary 不正确: %+v", cr.Summary)
	}
	s := Aggregate("run", "ab", "writer-x", 1, []CaseResult{cr})
	if s.Gate != Warn {
		t.Fatalf("suite gate = %s want WARN", s.Gate)
	}
}
