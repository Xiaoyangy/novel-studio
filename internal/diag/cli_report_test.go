package diag

import (
	"strings"
	"testing"
)

func TestRenderCLIReportIncludesStaticFindings(t *testing.T) {
	rep := Report{
		Stats: Stats{Phase: "complete", Flow: "writing", CompletedChapters: 10, TotalChapters: 10, TotalWords: 30000},
		Findings: []Finding{{
			Rule:       "PipelineEvidenceDrift",
			Category:   CatFlow,
			Severity:   SevCritical,
			Confidence: ConfHigh,
			Target:     "meta/pipeline.json",
			Title:      "流水线已完成阶段证据失效: review",
			Evidence:   "missing_artifacts=[reviews/01.md]",
			Suggestion: "重跑同一条 novel-studio --pipeline 命令。",
		}},
	}

	out := string(RenderCLIReport(rep))
	for _, want := range []string{
		"diag findings",
		"phase=complete",
		"[critical/flow] 流水线已完成阶段证据失效: review",
		"target: meta/pipeline.json",
		"missing_artifacts=[reviews/01.md]",
		"重跑同一条 novel-studio --pipeline 命令。",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("report missing %q:\n%s", want, out)
		}
	}
}

func TestRenderCLIReportQuietWhenNoFindings(t *testing.T) {
	out := string(RenderCLIReport(Report{}))
	if !strings.Contains(out, "findings: none") {
		t.Fatalf("quiet report should say findings none:\n%s", out)
	}
}
