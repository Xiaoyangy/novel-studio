package tools

import (
	"strings"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

// rewriteDimensionDiagnostic is a bounded, prose-facing projection of an
// Editor dimension comment. The full scorecard remains review evidence; a
// rejected exact body only needs dimensions that require work, plus the
// AI-voice dimension because the deterministic artifact gate can make that
// comment blocking even when the model gave it a nominal pass score.
type rewriteDimensionDiagnostic struct {
	Dimension string `json:"dimension"`
	Score     int    `json:"score"`
	Verdict   string `json:"verdict"`
	Comment   string `json:"comment"`
}

func actionableRewriteDimensionDiagnostics(
	review *domain.ReviewEntry,
	finalVerdict string,
) []rewriteDimensionDiagnostic {
	if review == nil || (finalVerdict != "rewrite" && finalVerdict != "polish") {
		return nil
	}
	diagnostics := make([]rewriteDimensionDiagnostic, 0, len(review.Dimensions))
	for _, dimension := range review.Dimensions {
		comment := strings.Join(strings.Fields(dimension.Comment), " ")
		if comment == "" {
			continue
		}
		verdict := strings.TrimSpace(dimension.Verdict)
		if verdict == "" {
			verdict = expectedDimensionVerdict(dimension.Score)
		}
		// Non-pass dimensions are directly actionable. AI voice is also retained
		// on any blocking formal review: its rule-by-rule comment is the Editor's
		// reconciliation of the mechanical detector, which can independently
		// force rewrite after the scorecard itself appears to pass.
		if verdict == "pass" && dimension.Score >= 80 &&
			strings.TrimSpace(dimension.Dimension) != "ai_voice_detection" {
			continue
		}
		diagnostics = append(diagnostics, rewriteDimensionDiagnostic{
			Dimension: strings.TrimSpace(dimension.Dimension),
			Score:     dimension.Score,
			Verdict:   verdict,
			Comment:   comment,
		})
	}
	return diagnostics
}
