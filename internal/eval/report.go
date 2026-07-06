package eval

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	ArmSingle   = "single"
	ArmBaseline = "baseline"
	ArmVariant  = "variant"
)

// Suite 是一次评测运行的聚合结果。
type Suite struct {
	RunID   string       `json:"run_id"`
	Mode    string       `json:"mode"` // single / ab
	Variant string       `json:"variant,omitempty"`
	Repeat  int          `json:"repeat"`
	Gate    Outcome      `json:"gate"`
	Cases   []CaseResult `json:"cases"`
}

type CaseResult struct {
	CaseID   string        `json:"case_id"`
	Category string        `json:"category"`
	Role     string        `json:"role,omitempty"`
	Outcome  Outcome       `json:"outcome"`
	Runs     []RunResult   `json:"runs"`
	Deltas   []Delta       `json:"deltas,omitempty"`
	Summary  RepeatSummary `json:"summary"`
}

type RunResult struct {
	Arm    string `json:"arm"`
	Repeat int    `json:"repeat"`
	Result Result `json:"result"`
}

type RepeatSummary struct {
	PassRate        float64       `json:"pass_rate"`
	HardFailRuns    int           `json:"hard_fail_runs"`
	WarningRuns     int           `json:"warning_runs"`
	CostUSD         *RangeSummary `json:"cost_usd,omitempty"`
	ToolCalls       *RangeSummary `json:"tool_calls,omitempty"`
	VariantPassRate float64       `json:"variant_pass_rate,omitempty"`
	DeltaPassRate   float64       `json:"delta_pass_rate,omitempty"`
}

type RangeSummary struct {
	Min float64 `json:"min"`
	Avg float64 `json:"avg"`
	Max float64 `json:"max"`
}

// Aggregate 把单 case 结果汇总成 suite，并计算整体门禁：任一 FAIL→FAIL，否则任一 WARN→WARN。
func Aggregate(runID, mode, variant string, repeat int, cases []CaseResult) Suite {
	gate := Pass
	for _, c := range cases {
		gate = worstOutcome(gate, c.Outcome)
	}
	if repeat <= 0 {
		repeat = 1
	}
	return Suite{RunID: runID, Mode: mode, Variant: variant, Repeat: repeat, Gate: gate, Cases: cases}
}

func NewSingleRunsCaseResult(c Case, runs []RunResult) CaseResult {
	cr := CaseResult{
		CaseID:   c.ID,
		Category: c.Category,
		Role:     c.Role,
		Runs:     runs,
	}
	cr.finalize()
	return cr
}

func NewABCaseResult(c Case, runs []RunResult, deltas []Delta) CaseResult {
	cr := CaseResult{
		CaseID:   c.ID,
		Category: c.Category,
		Role:     c.Role,
		Runs:     runs,
		Deltas:   deltas,
	}
	cr.finalize()
	return cr
}

func (c *CaseResult) finalize() {
	outcome := Pass
	for _, r := range c.Runs {
		outcome = worstOutcome(outcome, r.Result.Outcome)
	}
	for _, d := range c.Deltas {
		outcome = worstOutcome(outcome, d.Outcome)
	}
	c.Outcome = outcome
	c.Summary = summarizeRuns(c.Runs, c.Deltas)
}

func summarizeRuns(runs []RunResult, deltas []Delta) RepeatSummary {
	var s RepeatSummary
	total := len(runs)
	if total > 0 {
		pass := 0
		var costs, toolCalls []float64
		for _, run := range runs {
			switch run.Result.Outcome {
			case Fail:
				s.HardFailRuns++
			case Warn:
				s.WarningRuns++
			case Pass:
				pass++
			}
			if run.Result.Metrics.Usage.UsageRecorded {
				costs = append(costs, run.Result.Metrics.Usage.CostUSD)
			}
			if run.Result.Metrics.ToolCalls > 0 {
				toolCalls = append(toolCalls, float64(run.Result.Metrics.ToolCalls))
			}
		}
		s.PassRate = round2(float64(pass) / float64(total))
		s.CostUSD = summarizeRange(costs)
		s.ToolCalls = summarizeRange(toolCalls)
	}
	variantTotal, variantPass := 0, 0
	for _, run := range runs {
		if run.Arm != ArmVariant {
			continue
		}
		variantTotal++
		if run.Result.Outcome == Pass {
			variantPass++
		}
	}
	if variantTotal > 0 {
		s.VariantPassRate = round2(float64(variantPass) / float64(variantTotal))
	}
	if len(deltas) > 0 {
		pass := 0
		for _, d := range deltas {
			if d.Outcome == Pass {
				pass++
			}
		}
		s.DeltaPassRate = round2(float64(pass) / float64(len(deltas)))
	}
	return s
}

func summarizeRange(values []float64) *RangeSummary {
	if len(values) == 0 {
		return nil
	}
	min, max, sum := values[0], values[0], 0.0
	for _, v := range values {
		if v < min {
			min = v
		}
		if v > max {
			max = v
		}
		sum += v
	}
	return &RangeSummary{Min: round2(min), Avg: round2(sum / float64(len(values))), Max: round2(max)}
}

func worstOutcome(a, b Outcome) Outcome {
	if a == Fail || b == Fail {
		return Fail
	}
	if a == Warn || b == Warn {
		return Warn
	}
	return Pass
}

// WriteReport 在 outDir 下写 report.json（机读）与 report.md（人读）。
func WriteReport(s Suite, outDir string) error {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(outDir, "report.json"), data, 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(outDir, "report.md"), []byte(renderMarkdown(s)), 0o644)
}

// Summary 是给 stdout 的精简结论。
func Summary(s Suite) string {
	var hardFails, warnings int
	for _, c := range s.Cases {
		for _, run := range c.Runs {
			hardFails += len(run.Result.HardFails)
			warnings += len(run.Result.Warnings)
		}
		for _, d := range c.Deltas {
			hardFails += len(d.HardFails)
			warnings += len(d.Warnings)
		}
	}
	return fmt.Sprintf("Gate: %s  (%d cases, %d hard fails, %d warnings)",
		s.Gate, len(s.Cases), hardFails, warnings)
}

func renderMarkdown(s Suite) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Eval Report\n\n")
	fmt.Fprintf(&b, "Gate: **%s**\n\n", s.Gate)

	fmt.Fprintf(&b, "## Summary\n\n")
	fmt.Fprintf(&b, "- run_id: %s\n", s.RunID)
	fmt.Fprintf(&b, "- mode: %s\n", s.Mode)
	if s.Variant != "" {
		fmt.Fprintf(&b, "- variant: %s\n", s.Variant)
	}
	fmt.Fprintf(&b, "- repeat: %d\n", s.Repeat)
	fmt.Fprintf(&b, "- cases: %d\n\n", len(s.Cases))

	fmt.Fprintf(&b, "## Cases\n\n")
	for _, c := range s.Cases {
		fmt.Fprintf(&b, "### %s  [%s]\n\n", c.CaseID, c.Outcome)
		if c.Role != "" {
			fmt.Fprintf(&b, "- category=%s role=%s\n", c.Category, c.Role)
		} else {
			fmt.Fprintf(&b, "- category=%s\n", c.Category)
		}
		fmt.Fprintf(&b, "- pass_rate=%.2f hard_fail_runs=%d warning_runs=%d",
			c.Summary.PassRate, c.Summary.HardFailRuns, c.Summary.WarningRuns)
		if c.Summary.VariantPassRate > 0 || len(c.Deltas) > 0 {
			fmt.Fprintf(&b, " variant_pass_rate=%.2f delta_pass_rate=%.2f",
				c.Summary.VariantPassRate, c.Summary.DeltaPassRate)
		}
		fmt.Fprintf(&b, "\n")
		if c.Summary.CostUSD != nil {
			r := c.Summary.CostUSD
			fmt.Fprintf(&b, "- cost_usd: min=%.2f avg=%.2f max=%.2f\n", r.Min, r.Avg, r.Max)
		}
		if c.Summary.ToolCalls != nil {
			r := c.Summary.ToolCalls
			fmt.Fprintf(&b, "- tool_calls: min=%.0f avg=%.0f max=%.0f\n", r.Min, r.Avg, r.Max)
		}

		for _, run := range c.Runs {
			writeRun(&b, run)
		}
		for i, d := range c.Deltas {
			writeDelta(&b, i+1, d)
		}
		fmt.Fprintf(&b, "\n")
	}
	return b.String()
}

func writeRun(b *strings.Builder, run RunResult) {
	r := run.Result
	m := r.Metrics
	label := run.Arm
	if run.Repeat > 1 {
		label = fmt.Sprintf("%s#%d", run.Arm, run.Repeat)
	}
	fmt.Fprintf(b, "- %s: %s phase=%s flow=%s completed=%d/%d words=%d findings(crit=%d warn=%d) tool_calls=%d",
		label, r.Outcome, m.Phase, m.Flow, m.CompletedChapters, m.TotalChapters, m.TotalWords,
		m.CriticalFindings, m.WarningFindings, m.ToolCalls)
	if m.Usage.UsageRecorded {
		fmt.Fprintf(b, " cost=$%.4f tokens(in=%d out=%d)", m.Usage.CostUSD, m.Usage.Input, m.Usage.Output)
	}
	if m.StylestatStatus != "" {
		fmt.Fprintf(b, " stylestat=%s", m.StylestatStatus)
	}
	fmt.Fprintf(b, "\n")
	writeIssues(b, "Hard Fail", r.HardFails)
	writeIssues(b, "Warnings", r.Warnings)
	writeIssues(b, "Notes", r.Notes)
	if r.Dir != "" {
		fmt.Fprintf(b, "  - artifacts: %s\n", r.Dir)
	}
}

func writeDelta(b *strings.Builder, idx int, d Delta) {
	m := d.Metrics
	fmt.Fprintf(b, "- delta#%d: %s completed_delta=%+d crit_delta=%+d warn_delta=%+d words_ratio=%.2f tool_calls_delta=%.2f cost_delta=%.2f\n",
		idx, d.Outcome, m.CompletedChapters, m.CriticalFindings, m.WarningFindings,
		m.TotalWordsRatio, m.ToolCallDeltaRatio, m.CostDeltaRatio)
	if m.Stylestat != nil {
		sd := m.Stylestat
		fmt.Fprintf(b, "  - stylestat: %s pattern_top=%+0.1f ending_short=%+0.2f repeated=%+d title_mixed=%+d\n",
			sd.Status, sd.PatternTopPerChapter, sd.EndingShortRatio, sd.RepeatedSentences, sd.TitleMixedDelta)
	}
	writeIssues(b, "Delta Hard Fail", d.HardFails)
	writeIssues(b, "Delta Warnings", d.Warnings)
	writeIssues(b, "Delta Notes", d.Notes)
}

func writeIssues(b *strings.Builder, title string, issues []Issue) {
	if len(issues) == 0 {
		return
	}
	fmt.Fprintf(b, "  - %s:\n", title)
	for _, it := range issues {
		if it.Severity != "" {
			fmt.Fprintf(b, "    - [%s] %s - %s\n", it.Severity, it.Source, it.Detail)
		} else {
			fmt.Fprintf(b, "    - %s - %s\n", it.Source, it.Detail)
		}
	}
}
