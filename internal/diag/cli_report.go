package diag

import (
	"fmt"
	"strings"
)

// RenderCLIReport renders the local, non-redacted diagnostic findings intended
// for the operator's terminal. Shareable output must still use RenderExport.
func RenderCLIReport(rep Report) []byte {
	var b strings.Builder
	st := rep.Stats

	b.WriteString("diag findings\n")
	fmt.Fprintf(&b, "stats: phase=%s flow=%s chapters=%d/%d words=%d\n",
		orDash(st.Phase), orDash(st.Flow), st.CompletedChapters, st.TotalChapters, st.TotalWords)

	if len(rep.Findings) == 0 {
		b.WriteString("findings: none\n")
	} else {
		fmt.Fprintf(&b, "findings: %d\n", len(rep.Findings))
		for _, finding := range rep.Findings {
			fmt.Fprintf(&b, "- [%s/%s] %s\n", finding.Severity, finding.Category, finding.Title)
			if finding.Target != "" {
				fmt.Fprintf(&b, "  target: %s\n", finding.Target)
			}
			if finding.Evidence != "" {
				fmt.Fprintf(&b, "  evidence: %s\n", finding.Evidence)
			}
			if finding.Suggestion != "" {
				fmt.Fprintf(&b, "  suggestion: %s\n", finding.Suggestion)
			}
		}
	}

	if len(rep.Actions) > 0 {
		fmt.Fprintf(&b, "actions: %d\n", len(rep.Actions))
		for _, action := range rep.Actions {
			fmt.Fprintf(&b, "- [%s] %s: %s\n", action.Severity, action.SourceRule, action.Summary)
		}
	}
	return []byte(b.String())
}
