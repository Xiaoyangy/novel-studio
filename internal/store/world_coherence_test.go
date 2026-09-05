package store

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

func TestWorldCoherenceReportRoundTrip(t *testing.T) {
	dir := t.TempDir()
	st := NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	report := domain.AuditWorldCoherence(nil, nil, nil)
	if err := st.SaveWorldCoherenceReport(report); err != nil {
		t.Fatalf("save report: %v", err)
	}
	got, err := st.LoadWorldCoherenceReport()
	if err != nil || got == nil || !reflect.DeepEqual(*got, report) {
		t.Fatalf("round trip got=%+v err=%v want=%+v", got, err, report)
	}
	markdown, err := os.ReadFile(filepath.Join(dir, worldCoherenceReportMD))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(markdown), report.ReportDigest) || !strings.Contains(string(markdown), "阻塞问题") {
		t.Fatalf("markdown lacks audit evidence:\n%s", markdown)
	}
}
