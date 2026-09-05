package store

import (
	"fmt"
	"os"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

const (
	worldCoherenceReportJSON = "meta/world_coherence_report.json"
	worldCoherenceReportMD   = "meta/world_coherence_report.md"
)

// SaveWorldCoherenceReport 原子写入世界自洽证明的机器版和审阅版。
func (s *Store) SaveWorldCoherenceReport(report domain.WorldCoherenceReport) error {
	return s.Progress.io.WithWriteLock(func() error {
		if err := s.Progress.io.WriteJSONUnlocked(worldCoherenceReportJSON, report); err != nil {
			return err
		}
		return s.Progress.io.WriteFileUnlocked(worldCoherenceReportMD, []byte(renderWorldCoherenceReport(report)))
	})
}

// LoadWorldCoherenceReport 读取确定性世界自洽证明；历史项目缺失时返回 nil。
func (s *Store) LoadWorldCoherenceReport() (*domain.WorldCoherenceReport, error) {
	var report domain.WorldCoherenceReport
	if err := s.Progress.io.ReadJSON(worldCoherenceReportJSON, &report); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return &report, nil
}

func renderWorldCoherenceReport(report domain.WorldCoherenceReport) string {
	var b strings.Builder
	b.WriteString("# 世界自洽证明\n\n")
	fmt.Fprintf(&b, "- protocol: %s\n", report.Protocol)
	fmt.Fprintf(&b, "- ready: %v\n", report.Ready)
	fmt.Fprintf(&b, "- source_digest: %s\n", report.SourceDigest)
	fmt.Fprintf(&b, "- report_digest: %s\n", report.ReportDigest)
	fmt.Fprintf(&b, "- 统计：规则 %d；法典维度 %d；机制 %d；反事实探针 %d；地点 %d；路线 %d；势力 %d；连通分量 %d\n",
		report.Stats.WorldRules,
		report.Stats.CodexSections,
		report.Stats.Mechanisms,
		report.Stats.CounterfactualTests,
		report.Stats.Places,
		report.Stats.Routes,
		report.Stats.Factions,
		report.Stats.ConnectedComponents,
	)
	if len(report.Findings) == 0 {
		b.WriteString("\n没有发现结构或操作合同问题。\n")
		return b.String()
	}
	for _, severity := range []string{domain.WorldCoherenceSeverityError, domain.WorldCoherenceSeverityWarning} {
		title := "阻塞问题"
		if severity == domain.WorldCoherenceSeverityWarning {
			title = "兼容提示"
		}
		wrote := false
		for _, finding := range report.Findings {
			if finding.Severity != severity {
				continue
			}
			if !wrote {
				fmt.Fprintf(&b, "\n## %s\n\n", title)
				wrote = true
			}
			subject := strings.TrimSpace(finding.Subject)
			if subject == "" {
				subject = finding.Code
			}
			fmt.Fprintf(&b, "- `%s`（%s）：%s\n", subject, finding.Code, finding.Message)
		}
	}
	return b.String()
}
